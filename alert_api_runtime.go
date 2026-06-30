package main

// 运行时相关的告警 API：实例 / 历史 / 分组 / 通知日志 / 统计 / 评估器状态。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/alerting/store"
	"PromAI/pkg/database"
	"PromAI/pkg/prometheus"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// ===== AlertInstance =============================================================

func (a *AdminAPI) handleAlertInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		if err := clearAlertInstances(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
		return
	}
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	q := database.DB.Model(&database.AlertInstance{})
	params := r.URL.Query()
	if v := params.Get("state"); v != "" {
		states := strings.Split(v, ",")
		q = q.Where("state IN ?", states)
	}
	if v := params.Get("severity"); v != "" {
		q = q.Where("severity = ?", v)
	}
	if v := params.Get("datasource_id"); v != "" {
		q = q.Where("datasource_id = ?", v)
	}
	if v := params.Get("rule_id"); v != "" {
		q = q.Where("rule_id = ?", v)
	}
	if v := params.Get("fingerprint"); v != "" {
		q = q.Where("fingerprint = ?", v)
	}
	if v := strings.TrimSpace(params.Get("keyword")); v != "" {
		k := "%" + v + "%"
		q = q.Where("labels_json LIKE ? OR annotations_json LIKE ?", k, k)
	}
	// 默认只看可见的（未静默/未抑制）
	if params.Get("include_masked") != "true" {
		q = q.Where("(silenced_by_json IS NULL OR silenced_by_json = '' OR silenced_by_json = '[]' OR silenced_by_json = 'null')")
		q = q.Where("(inhibited_by_json IS NULL OR inhibited_by_json = '' OR inhibited_by_json = '[]' OR inhibited_by_json = 'null')")
	}
	page, _ := strconv.Atoi(params.Get("page"))
	pageSize, _ := strconv.Atoi(params.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	var total int64
	q.Count(&total)
	var rows []database.AlertInstance
	q.Order("severity asc, fired_at desc, id desc").
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&rows)

	// 附加关联展开（rule_name / datasource_name）
	items := make([]map[string]interface{}, 0, len(rows))
	for _, ai := range rows {
		item := map[string]interface{}{
			"id":               ai.ID,
			"fingerprint":      ai.Fingerprint,
			"rule_id":          ai.RuleID,
			"datasource_id":    ai.DatasourceID,
			"labels":           jsonRaw(ai.LabelsJSON),
			"annotations":      jsonRaw(ai.AnnotationsJSON),
			"state":            ai.State,
			"severity":         ai.Severity,
			"value":            ai.Value,
			"threshold":        ai.Threshold,
			"active_at":        ai.ActiveAt,
			"fired_at":         ai.FiredAt,
			"resolved_at":      ai.ResolvedAt,
			"last_eval_at":     ai.LastEvalAt,
			"group_key":        ai.GroupKey,
			"silenced_by":      jsonRaw(ai.SilencedByJSON),
			"inhibited_by":     jsonRaw(ai.InhibitedByJSON),
			"notified_count":   ai.NotifiedCount,
			"last_notified_at": ai.LastNotifiedAt,
		}
		items = append(items, item)
	}
	writeJSON(w, map[string]interface{}{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (a *AdminAPI) handleAlertInstancesTrend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	var req struct {
		Fingerprints []string `json:"fingerprints"`
		Minutes      int      `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(req.Fingerprints) == 0 {
		writeError(w, 400, "fingerprints required")
		return
	}
	if req.Minutes <= 0 || req.Minutes > 1440 {
		req.Minutes = 60
	}

	// Load instances
	var instances []database.AlertInstance
	database.DB.Where("fingerprint IN ?", req.Fingerprints).Find(&instances)
	instMap := make(map[string]*database.AlertInstance, len(instances))
	for i := range instances {
		instMap[instances[i].Fingerprint] = &instances[i]
	}

	// Load rules
	ruleIDs := make(map[uint]bool)
	for i := range instances {
		ruleIDs[instances[i].RuleID] = true
	}
	var rules []database.AlertRule
	if len(ruleIDs) > 0 {
		ids := make([]uint, 0, len(ruleIDs))
		for id := range ruleIDs {
			ids = append(ids, id)
		}
		database.DB.Where("id IN ?", ids).Find(&rules)
	}
	ruleMap := make(map[uint]*database.AlertRule, len(rules))
	for i := range rules {
		ruleMap[rules[i].ID] = &rules[i]
	}

	// Load datasources
	dsIDs := make(map[uint]bool)
	for i := range instances {
		dsIDs[instances[i].DatasourceID] = true
	}
	var dss []database.DataSource
	if len(dsIDs) > 0 {
		ids := make([]uint, 0, len(dsIDs))
		for id := range dsIDs {
			ids = append(ids, id)
		}
		database.DB.Where("id IN ?", ids).Find(&dss)
	}
	dsMap := make(map[uint]*database.DataSource, len(dss))
	for i := range dss {
		dsMap[dss[i].ID] = &dss[i]
	}

	// Group by (rule_id, datasource_id) for batch PromQL
	type gk struct{ r, d uint }
	groups := make(map[gk][]*database.AlertInstance)
	for i := range instances {
		inst := &instances[i]
		k := gk{inst.RuleID, inst.DatasourceID}
		groups[k] = append(groups[k], inst)
	}

	result := make(map[string][]model.SamplePair)

	for key, insts := range groups {
		rule, ok := ruleMap[key.r]
		if !ok {
			continue
		}
		ds, ok := dsMap[key.d]
		if !ok {
			continue
		}

		expr := rule.Expr
		if expr == "" && rule.SourceType == "metric" && rule.MetricConfigID != nil {
			if snap := store.Snapshot(); snap != nil {
				if mc, ok := snap.MetricConfigs[*rule.MetricConfigID]; ok {
					expr = mc.Query
				}
			}
		}
		if expr == "" {
			continue
		}

		client, cErr := prometheus.DefaultCache.Get(ds.ID, ds.URL, ds.Username, ds.Password)
		if cErr != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		now := time.Now()
		rng := v1.Range{
			Start: now.Add(-time.Duration(req.Minutes) * time.Minute),
			End:   now,
			Step:  15 * time.Second,
		}
		val, _, qErr := client.API.QueryRange(ctx, expr, rng)
		cancel()
		if qErr != nil {
			continue
		}

		matrix, ok := val.(model.Matrix)
		if !ok {
			continue
		}

		for _, inst := range insts {
			var instLabels map[string]string
			if err := json.Unmarshal([]byte(inst.LabelsJSON), &instLabels); err != nil || instLabels == nil {
				continue
			}
			for _, ss := range matrix {
				matches := true
				for ln, lv := range ss.Metric {
					if iv, ok := instLabels[string(ln)]; !ok || iv != string(lv) {
						matches = false
						break
					}
				}
				if matches {
					result[inst.Fingerprint] = ss.Values
					break
				}
			}
		}
	}

	writeJSON(w, result)
}

func (a *AdminAPI) handleAlertInstanceByFP(w http.ResponseWriter, r *http.Request) {
	fp := strings.TrimPrefix(r.URL.Path, "/api/promai/alert/instances/")
	fp = strings.TrimRight(fp, "/")
	if fp == "" {
		writeError(w, 400, "missing fingerprint")
		return
	}
	var ai database.AlertInstance
	if err := database.DB.Where("fingerprint = ?", fp).First(&ai).Error; err != nil {
		writeError(w, 404, "not found")
		return
	}
	// 取最近 50 条历史
	var hist []database.AlertHistory
	database.DB.Where("fingerprint = ?", fp).
		Order("occurred_at desc").Limit(50).Find(&hist)
	// 取该分组下最近 50 条通知日志（通知去向 + 结果）
	var notifyLogs []database.AlertNotifyLog
	if ai.GroupKey != "" {
		database.DB.Where("group_key = ?", ai.GroupKey).
			Order("sent_at desc").Limit(50).Find(&notifyLogs)
	}
	// 查 AlertGroup 获取下一轮告警触发时间
	var nextNotifyAt *time.Time
	if ai.GroupKey != "" {
		var grp database.AlertGroup
		if err := database.DB.Where("group_key = ?", ai.GroupKey).First(&grp).Error; err == nil {
			nextNotifyAt = grp.NextNotifyAt
		}
	}
	writeJSON(w, map[string]interface{}{
		"instance": map[string]interface{}{
			"id":               ai.ID,
			"fingerprint":      ai.Fingerprint,
			"rule_id":          ai.RuleID,
			"datasource_id":    ai.DatasourceID,
			"labels":           jsonRaw(ai.LabelsJSON),
			"annotations":      jsonRaw(ai.AnnotationsJSON),
			"state":            ai.State,
			"severity":         ai.Severity,
			"value":            ai.Value,
			"threshold":        ai.Threshold,
			"active_at":        ai.ActiveAt,
			"fired_at":         ai.FiredAt,
			"resolved_at":      ai.ResolvedAt,
			"last_eval_at":     ai.LastEvalAt,
			"group_key":        ai.GroupKey,
			"silenced_by":      jsonRaw(ai.SilencedByJSON),
			"inhibited_by":     jsonRaw(ai.InhibitedByJSON),
			"notified_count":   ai.NotifiedCount,
			"last_notified_at": ai.LastNotifiedAt,
			"next_notify_at":   nextNotifyAt,
		},
		"history":      hist,
		"notify_logs":  notifyLogs,
	})
}

// ===== AlertHistory ==============================================================

func (a *AdminAPI) handleAlertHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	params := r.URL.Query()
	q := database.DB.Model(&database.AlertHistory{})
	if v := params.Get("rule_id"); v != "" {
		q = q.Where("rule_id = ?", v)
	}
	if v := params.Get("datasource_id"); v != "" {
		q = q.Where("datasource_id = ?", v)
	}
	if v := params.Get("event_type"); v != "" {
		q = q.Where("event_type = ?", v)
	}
	if v := params.Get("severity"); v != "" {
		q = q.Where("severity = ?", v)
	}
	if v := params.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("occurred_at >= ?", t)
		}
	}
	if v := params.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("occurred_at <= ?", t)
		}
	}
	if v := strings.TrimSpace(params.Get("keyword")); v != "" {
		k := "%" + v + "%"
		q = q.Where("rule_name LIKE ? OR datasource_name LIKE ? OR labels_json LIKE ?", k, k, k)
	}
	page, _ := strconv.Atoi(params.Get("page"))
	pageSize, _ := strconv.Atoi(params.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	var total int64
	q.Count(&total)
	var rows []database.AlertHistory
	q.Order("occurred_at desc, id desc").
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&rows)
	writeJSON(w, map[string]interface{}{
		"items":     rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// ===== AlertHistory Timeline =====================================================

// TimelineEntry 时间线的单条事件
type TimelineEntry struct {
	EventID        uint           `json:"event_id,omitempty"`
	Type           string         `json:"type"` // firing / resolved / notify
	Severity       string         `json:"severity,omitempty"`
	Value          float64        `json:"value,omitempty"`
	Threshold      float64        `json:"threshold,omitempty"`
	LabelsJSON     string         `json:"labels_json,omitempty"`
	AnnotationsJSON string        `json:"annotations_json,omitempty"`
	State          string         `json:"state,omitempty"`
	NotifyChannels string         `json:"notify_channels,omitempty"`
	NotifyResult   string         `json:"notify_result,omitempty"`
	ChannelType    string         `json:"channel_type,omitempty"`
	ChannelName    string         `json:"channel_name,omitempty"`
	Error          string         `json:"error,omitempty"`
	Content        string         `json:"content,omitempty"`
	SentAt         *time.Time     `json:"sent_at,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
}

// TimelineGroup 一个 (数据源 × 规则) 的时间线
type TimelineGroup struct {
	RuleID        uint            `json:"rule_id"`
	RuleName      string          `json:"rule_name"`
	DatasourceID  uint            `json:"datasource_id"`
	DatasourceName string         `json:"datasource_name"`
	NextNotifyAt  *time.Time      `json:"next_notify_at,omitempty"`
	Entries       []TimelineEntry `json:"entries"`
}

func (a *AdminAPI) handleAlertHistoryTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	params := r.URL.Query()

	q := database.DB.Model(&database.AlertHistory{})
	if v := params.Get("rule_id"); v != "" {
		q = q.Where("rule_id = ?", v)
	}
	if v := params.Get("datasource_id"); v != "" {
		q = q.Where("datasource_id = ?", v)
	}
	if v := params.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("occurred_at >= ?", t)
		}
	}
	if v := params.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("occurred_at <= ?", t)
		}
	}
	if v := strings.TrimSpace(params.Get("keyword")); v != "" {
		k := "%" + v + "%"
		q = q.Where("rule_name LIKE ? OR datasource_name LIKE ?", k, k)
	}

	var histories []database.AlertHistory
	q.Order("occurred_at desc").Limit(500).Find(&histories)

	// Collect unique rule_ids for notify log query
	ruleIDs := make(map[uint]bool)
	for _, h := range histories {
		ruleIDs[h.RuleID] = true
	}

	// Load notification logs for all relevant rules
	notifByRule := make(map[uint][]database.AlertNotifyLog)
	if len(ruleIDs) > 0 {
		ids := make([]uint, 0, len(ruleIDs))
		for id := range ruleIDs {
			ids = append(ids, id)
		}
		var logs []database.AlertNotifyLog
		database.DB.Where("rule_id IN ?", ids).Order("sent_at desc").Limit(500).Find(&logs)
		for _, l := range logs {
			notifByRule[l.RuleID] = append(notifByRule[l.RuleID], l)
		}
	}

	// Fetch AlertGroups for NextNotifyAt
	groupNextNotify := make(map[uint]*time.Time)
	if len(ruleIDs) > 0 {
		ids := make([]uint, 0, len(ruleIDs))
		for id := range ruleIDs {
			ids = append(ids, id)
		}
		var ags []database.AlertGroup
		database.DB.Where("rule_id IN ? AND next_notify_at IS NOT NULL", ids).Find(&ags)
		for _, ag := range ags {
			if ag.NextNotifyAt == nil {
				continue
			}
			if existing, ok := groupNextNotify[ag.RuleID]; !ok || existing == nil || ag.NextNotifyAt.Before(*existing) {
				groupNextNotify[ag.RuleID] = ag.NextNotifyAt
			}
		}
	}

	// Group by (rule_id, datasource_id)
	type gk struct {
		RuleID       uint
		DatasourceID uint
	}
	groupMap := make(map[gk]*TimelineGroup)
	var groupKeys []gk

	for _, h := range histories {
		if h.EventType != "firing" && h.EventType != "resolved" {
			continue
		}
		k := gk{RuleID: h.RuleID, DatasourceID: h.DatasourceID}
		grp, ok := groupMap[k]
		if !ok {
			grp = &TimelineGroup{
				RuleID:        h.RuleID,
				RuleName:      h.RuleName,
				DatasourceID:   h.DatasourceID,
				DatasourceName: h.DatasourceName,
				NextNotifyAt:  groupNextNotify[h.RuleID],
			}
			groupMap[k] = grp
			groupKeys = append(groupKeys, k)
		}
		// Update rule/datasource name if it was empty
		if grp.RuleName == "" && h.RuleName != "" {
			grp.RuleName = h.RuleName
		}
		if grp.DatasourceName == "" && h.DatasourceName != "" {
			grp.DatasourceName = h.DatasourceName
		}
		entry := TimelineEntry{
			EventID:         h.ID,
			Type:            h.EventType,
			Severity:        h.Severity,
			Value:           h.Value,
			Threshold:       h.Threshold,
			LabelsJSON:      h.LabelsJSON,
			AnnotationsJSON: h.AnnotationsJSON,
			State:           h.State,
			NotifyChannels:  h.NotifyChannels,
			NotifyResult:    h.NotifyResult,
			OccurredAt:      h.OccurredAt,
		}
		grp.Entries = append(grp.Entries, entry)
	}

	// Attach notification logs to groups (by rule_id)
	for _, k := range groupKeys {
		grp := groupMap[k]
		logs := notifByRule[grp.RuleID]
		for _, l := range logs {
			grp.Entries = append(grp.Entries, TimelineEntry{
				Type:           "notify",
				ChannelType:    l.ChannelType,
				NotifyChannels: l.ChannelType,
				NotifyResult:   l.Status,
				Error:          l.Error,
				Content:        l.Content,
				SentAt:         &l.SentAt,
				OccurredAt:     l.SentAt,
			})
		}
	}

	// Sort entries chronologically within each group
	for _, grp := range groupMap {
		sort.Slice(grp.Entries, func(i, j int) bool {
			return grp.Entries[i].OccurredAt.Before(grp.Entries[j].OccurredAt)
		})
	}

	// Assemble result preserving key order
	result := make([]TimelineGroup, 0, len(groupKeys))
	for _, k := range groupKeys {
		result = append(result, *groupMap[k])
	}

	writeJSON(w, map[string]interface{}{"groups": result})
}

// ===== AlertGroup / NotifyLog ====================================================

func (a *AdminAPI) handleAlertGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	var rows []database.AlertGroup
	database.DB.Order("first_seen_at desc").Limit(500).Find(&rows)
	writeJSON(w, map[string]interface{}{"items": rows, "total": len(rows)})
}

func (a *AdminAPI) handleAlertNotifyLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	params := r.URL.Query()
	q := database.DB.Model(&database.AlertNotifyLog{})
	if v := params.Get("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := params.Get("channel_id"); v != "" {
		q = q.Where("channel_id = ?", v)
	}
	if v := params.Get("rule_id"); v != "" {
		q = q.Where("rule_id = ?", v)
	}
	if v := params.Get("group_key"); v != "" {
		q = q.Where("group_key = ?", v)
	}
	page, _ := strconv.Atoi(params.Get("page"))
	pageSize, _ := strconv.Atoi(params.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	var total int64
	q.Count(&total)
	var rows []database.AlertNotifyLog
	q.Order("sent_at desc, id desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&rows)
	writeJSON(w, map[string]interface{}{
		"items":     rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// ===== 统计与运行状态 ============================================================

func (a *AdminAPI) handleAlertStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	type sevCount struct {
		Severity string
		Count    int64
	}
	var bySeverity []sevCount
	database.DB.Model(&database.AlertInstance{}).
		Select("severity, count(*) as count").
		Where("state IN ?", []string{"pending", "firing"}).
		Group("severity").Scan(&bySeverity)

	type stateCount struct {
		State string
		Count int64
	}
	var byState []stateCount
	database.DB.Model(&database.AlertInstance{}).
		Select("state, count(*) as count").Group("state").Scan(&byState)

	type ruleTop struct {
		RuleID uint
		Count  int64
	}
	var topRules []ruleTop
	database.DB.Model(&database.AlertInstance{}).
		Select("rule_id, count(*) as count").
		Where("state IN ?", []string{"pending", "firing"}).
		Group("rule_id").Order("count desc").Limit(10).Scan(&topRules)

	type dsTop struct {
		DatasourceID uint
		Count        int64
	}
	var topDS []dsTop
	database.DB.Model(&database.AlertInstance{}).
		Select("datasource_id, count(*) as count").
		Where("state IN ?", []string{"pending", "firing"}).
		Group("datasource_id").Order("count desc").Limit(10).Scan(&topDS)

	// 24h 趋势（按小时聚合 firing 事件数）
	type bucket struct {
		Hour  string
		Count int64
	}
	var trend []bucket
	since := time.Now().Add(-24 * time.Hour)
	database.DB.Model(&database.AlertHistory{}).
		Select("strftime('%Y-%m-%d %H:00', occurred_at) as hour, count(*) as count").
		Where("event_type = ? AND occurred_at >= ?", "firing", since).
		Group("hour").Order("hour asc").Scan(&trend)

	writeJSON(w, map[string]interface{}{
		"by_severity":     bySeverity,
		"by_state":        byState,
		"top_rules":       topRules,
		"top_datasources": topDS,
		"trend_24h":       trend,
	})
}

func clearAlertInstances() error {
	// 清除运行时状态（dispatcher 的 tracked map）
	if adminAlerting != nil {
		adminAlerting.ClearInstances()
	}
	// 清除 DB 数据
	tx := database.DB.Begin()
	if err := tx.Exec("DELETE FROM alert_instances").Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("delete instances: %w", err)
	}
	if err := tx.Exec("DELETE FROM alert_histories").Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("delete histories: %w", err)
	}
	if err := tx.Exec("DELETE FROM alert_notify_logs").Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("delete notify_logs: %w", err)
	}
	if err := tx.Exec("DELETE FROM alert_groups").Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("delete groups: %w", err)
	}
	return tx.Commit().Error
}

func (a *AdminAPI) handleAlertEvaluatorStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	if adminAlerting == nil {
		writeJSON(w, map[string]interface{}{"running": false})
		return
	}
	stats := adminAlerting.EvaluatorStats()
	stats["running"] = true
	writeJSON(w, stats)
}

// jsonRaw 把 string 字段直接以 JSON 形式回写（避免双重转义）
func jsonRaw(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(s)
}
