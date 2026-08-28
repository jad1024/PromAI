package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// ===== 告警事件聚合（纯读路径，不触碰告警评估与通知链路） ===========================
//
// 设计说明：
//   - 通知层面的分组/去重/抑制是 Alertmanager 的职责，PromAI 不做；
//   - 这里只做"分析级聚合"：把 AlertHistory 原始事件流按 规则+数据源+时间窗
//     合并成人能看懂的"事件"，用于前端事件视图、噪音排行和 AI 分析上下文。

// alertEvent 聚合后的一条告警事件。
type alertEvent struct {
	RuleID          uint      `json:"rule_id"`
	RuleName        string    `json:"rule_name"`
	DatasourceID    uint      `json:"datasource_id"`
	DatasourceName  string    `json:"datasource_name"`
	Severity        string    `json:"severity"`
	State           string    `json:"state"` // ongoing | resolved
	FirstFiredAt    time.Time `json:"first_fired_at"`
	LastEventAt     time.Time `json:"last_event_at"`
	FiringCount     int       `json:"firing_count"`  // 时间窗内触发次数（含重复评估）
	RawCount        int       `json:"raw_count"`     // 聚合的原始历史条数
	FlapCount       int       `json:"flap_count"`   // 触发↔恢复来回次数
	Flapping        bool      `json:"flapping"`     // flap_count >= 3 视为震荡
	PeakValue       float64   `json:"peak_value"`
	Threshold       float64   `json:"threshold"`
	// CorrelatedDS 同一规则在其他数据源上、时间窗重叠的数据源名（跨集群关联提示）
	CorrelatedDS []string `json:"correlated_datasources"`
}

// alertEventAggResp 事件聚合接口响应。
type alertEventAggResp struct {
	Events       []alertEvent `json:"events"`
	TotalRaw     int          `json:"total_raw"`
	TotalEvents  int          `json:"total_events"`
	Compression  float64      `json:"compression"`
	WindowHours  int          `json:"window_hours"`
}

// noiseTopRule 噪音排行条目。
type noiseTopRule struct {
	RuleID         uint   `json:"rule_id"`
	RuleName       string `json:"rule_name"`
	DatasourceID   uint   `json:"datasource_id"`
	DatasourceName string `json:"datasource_name"`
	Severity       string `json:"severity"`
	FiringCount    int    `json:"firing_count"`
	FlapCount      int    `json:"flap_count"`
	Flapping       bool   `json:"flapping"`
	RawCount       int    `json:"raw_count"`
}

// eventAggWindowGap 同一 (rule, datasource) 内两条历史间隔超过该值则切分为新事件。
const eventAggWindowGap = 10 * time.Minute

// eventAggFlapThreshold 一次事件内触发/恢复来回次数达到该值判定为震荡。
const eventAggFlapThreshold = 3

// aggregateAlertEvents 拉取时间窗内的 AlertHistory 并在内存中聚合为事件。
// 纯读路径，最多读取 maxRawRows 条，超出截断（按时间倒序取最新）。
func aggregateAlertEvents(hours int, datasourceID uint, severity, ruleName, keyword string, maxRawRows int) ([]alertEvent, int, int) {
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	if maxRawRows <= 0 {
		maxRawRows = 20000
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)

	q := database.DB.Model(&database.AlertHistory{}).
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ?",
			[]string{"firing", "resolved", "pending"}, since)
	if datasourceID > 0 {
		q = q.Where("datasource_id = ?", datasourceID)
	}
	if severity != "" {
		q = q.Where("severity = ?", severity)
	}
	if ruleName != "" {
		q = q.Where("rule_name = ?", ruleName)
	}
	if keyword != "" {
		k := "%" + keyword + "%"
		q = q.Where("rule_name LIKE ? OR datasource_name LIKE ? OR labels_json LIKE ?", k, k, k)
	}

	var rows []database.AlertHistory
	if err := q.Order("occurred_at ASC").Limit(maxRawRows).Scan(&rows).Error; err != nil {
		return nil, 0, 0
	}
	totalRaw := len(rows)

	type key struct {
		ruleID uint
		dsID   uint
	}
	// 每个 (rule, datasource) 维护一个事件切片；时间间隔超过 eventAggWindowGap 切新事件
	eventsBySrc := map[key][]*alertEvent{}
	for _, r := range rows {
		k := key{r.RuleID, r.DatasourceID}
		ts := r.OccurredAt
		if ts.IsZero() {
			ts = r.CreatedAt
		}
		list := eventsBySrc[k]
		var ev *alertEvent
		if n := len(list); n > 0 {
			last := list[n-1]
			if ts.Sub(last.LastEventAt) <= eventAggWindowGap {
				ev = last
			}
		}
		if ev == nil {
			ev = &alertEvent{
				RuleID:         r.RuleID,
				RuleName:       r.RuleName,
				DatasourceID:   r.DatasourceID,
				DatasourceName: r.DatasourceName,
				Severity:       r.Severity,
				FirstFiredAt:   ts,
				PeakValue:      r.Value,
				Threshold:      r.Threshold,
			}
			eventsBySrc[k] = append(list, ev)
		}
		ev.LastEventAt = ts
		ev.RawCount++
		if r.Value > ev.PeakValue {
			ev.PeakValue = r.Value
		}
		switch r.EventType {
		case "firing", "pending":
			ev.FiringCount++
			ev.State = "ongoing"
		case "resolved":
			if ev.State == "ongoing" {
				ev.FlapCount++
				ev.State = "resolved"
			}
		}
		if sevRank(r.Severity) > sevRank(ev.Severity) {
			ev.Severity = r.Severity
		}
	}

	// 展平 + 震荡标记 + 跨数据源关联
	var events []alertEvent
	for _, list := range eventsBySrc {
		for _, ev := range list {
			ev.Flapping = ev.FlapCount >= eventAggFlapThreshold
			if ev.State == "" {
				ev.State = "ongoing"
			}
			events = append(events, *ev)
		}
	}

	// 同一规则在其他数据源、时间重叠 → 标注关联（提示而非硬分组）
	byRule := map[uint][]*alertEvent{}
	for i := range events {
		byRule[events[i].RuleID] = append(byRule[events[i].RuleID], &events[i])
	}
	for _, list := range byRule {
		for i := range list {
			for j := range list {
				if i == j || list[i].DatasourceID == list[j].DatasourceID {
					continue
				}
				if list[i].FirstFiredAt.Before(list[j].LastEventAt) && list[j].FirstFiredAt.Before(list[i].LastEventAt) {
					if !containsStr(list[i].CorrelatedDS, list[j].DatasourceName) {
						list[i].CorrelatedDS = append(list[i].CorrelatedDS, list[j].DatasourceName)
					}
				}
			}
		}
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].LastEventAt.After(events[j].LastEventAt)
	})
	return events, totalRaw, len(events)
}

func sevRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// handleAlertEvents GET /api/promai/alert/events
// 参数：hours（默认24）、datasource_id、severity、rule_name、keyword、limit（默认50）
func (a *AdminAPI) handleAlertEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	params := r.URL.Query()
	hours, _ := strconv.Atoi(params.Get("hours"))
	dsID, _ := strconv.Atoi(params.Get("datasource_id"))
	limit, _ := strconv.Atoi(params.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	events, totalRaw, _ := aggregateAlertEvents(hours, uint(dsID),
		strings.TrimSpace(params.Get("severity")),
		strings.TrimSpace(params.Get("rule_name")),
		strings.TrimSpace(params.Get("keyword")), 20000)

	totalEvents := len(events)
	if len(events) > limit {
		events = events[:limit]
	}
	compression := 0.0
	if totalRaw > 0 {
		compression = float64(totalRaw-totalEvents) / float64(totalRaw) * 100
	}
	writeJSON(w, alertEventAggResp{
		Events:      events,
		TotalRaw:    totalRaw,
		TotalEvents: totalEvents,
		Compression: round2(compression),
		WindowHours: hours,
	})
}

// handleAlertNoiseTop GET /api/promai/alert/noise-top
// TOP 噪音排行：按聚合事件的触发次数排序（含震荡标记）。
func (a *AdminAPI) handleAlertNoiseTop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	params := r.URL.Query()
	hours, _ := strconv.Atoi(params.Get("hours"))
	limit, _ := strconv.Atoi(params.Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	events, _, _ := aggregateAlertEvents(hours, 0, "", "", "", 20000)
	items := make([]noiseTopRule, 0, len(events))
	for _, ev := range events {
		items = append(items, noiseTopRule{
			RuleID:         ev.RuleID,
			RuleName:       ev.RuleName,
			DatasourceID:   ev.DatasourceID,
			DatasourceName: ev.DatasourceName,
			Severity:       ev.Severity,
			FiringCount:    ev.FiringCount,
			FlapCount:      ev.FlapCount,
			Flapping:       ev.Flapping,
			RawCount:       ev.RawCount,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].FiringCount != items[j].FiringCount {
			return items[i].FiringCount > items[j].FiringCount
		}
		return items[i].RawCount > items[j].RawCount
	})
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, map[string]interface{}{
		"items":       items,
		"window_hours": hours,
	})
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
