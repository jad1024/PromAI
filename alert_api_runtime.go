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
	// 按触发时间范围筛选（fired_at 为空时退化为 active_at）
	if v := params.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("(fired_at IS NOT NULL AND fired_at >= ?) OR (fired_at IS NULL AND active_at >= ?)", t, t)
		}
	}
	if v := params.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("(fired_at IS NOT NULL AND fired_at <= ?) OR (fired_at IS NULL AND active_at <= ?)", t, t)
		}
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

	// 附加关联展开（rule_name / datasource_name / external_source_name）
	items := make([]map[string]interface{}, 0, len(rows))
	for _, ai := range rows {
		item := map[string]interface{}{
			"id":                   ai.ID,
			"fingerprint":          ai.Fingerprint,
			"rule_id":              ai.RuleID,
			"rule_name":            instanceRuleName(&ai),
			"datasource_id":        ai.DatasourceID,
			"datasource_name":      instanceDatasourceName(&ai),
			"external_source_id":   ai.ExternalSourceID,
			"external_source_name": instanceExternalSourceName(&ai),
			"unread_count":         ai.UnreadCount,
			"firing_count":         ai.FiringCount,
			"labels":               jsonRaw(ai.LabelsJSON),
			"annotations":          jsonRaw(ai.AnnotationsJSON),
			"state":                ai.State,
			"severity":             ai.Severity,
			"value":                ai.Value,
			"threshold":            ai.Threshold,
			"active_at":            ai.ActiveAt,
			"fired_at":             ai.FiredAt,
			"resolved_at":          ai.ResolvedAt,
			"last_eval_at":         ai.LastEvalAt,
			"group_key":            ai.GroupKey,
			"silenced_by":          jsonRaw(ai.SilencedByJSON),
			"inhibited_by":         jsonRaw(ai.InhibitedByJSON),
			"notified_count":       ai.NotifiedCount,
			"last_notified_at":     ai.LastNotifiedAt,
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
		Fingerprints   []string `json:"fingerprints"`
		Minutes        int      `json:"minutes"`
		IncludeRepeats bool     `json:"include_repeats"`
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
	// 外部告警（external_source_id != 0）无 Prometheus 数据源，跳过 PromQL，走历史序列
	type gk struct{ r, d uint }
	groups := make(map[gk][]*database.AlertInstance)
	var externalInsts []*database.AlertInstance
	for i := range instances {
		inst := &instances[i]
		if inst.ExternalSourceID != 0 {
			externalInsts = append(externalInsts, inst)
			continue
		}
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

	// 外部告警趋势：从 AlertHistory 提取 (时间, value) 序列。
	// 外部平台（n9e/华为云/阿里云）每次推送 firing 事件都会落历史，足以画出触发值变化曲线。
	if len(externalInsts) > 0 {
		fps := make([]string, 0, len(externalInsts))
		for _, inst := range externalInsts {
			fps = append(fps, inst.Fingerprint)
		}
		start := time.Now().Add(-time.Duration(req.Minutes) * time.Minute)
		var histRows []database.AlertHistory
		database.DB.Where("fingerprint IN ? AND event_type = ? AND occurred_at >= ?",
			fps, "firing", start).Order("occurred_at asc").Find(&histRows)
		extSeries := make(map[string][]model.SamplePair, len(fps))
		for _, h := range histRows {
			extSeries[h.Fingerprint] = append(extSeries[h.Fingerprint], model.SamplePair{
				Timestamp: model.TimeFromUnixNano(h.OccurredAt.UnixNano()),
				Value:     model.SampleValue(h.Value),
			})
		}
		for _, inst := range externalInsts {
			if pts := extSeries[inst.Fingerprint]; len(pts) > 0 {
				result[inst.Fingerprint] = pts
			}
		}
	}

	// 勾选"包括重发的"：仅聚合当前请求指纹自身最近 N 分钟的 firing 历史事件（重发合并）。
	// 不再跨 alertname / 跨指纹汇总，避免把同一规则下的不同实例错误地合并成一条线。
	// 仅作为 PromQL 无数据/该指纹无历史时的兜底，已有 PromQL 数据时不覆盖。
	if req.IncludeRepeats {
		start := time.Now().Add(-time.Duration(req.Minutes) * time.Minute)
		fps := make([]string, 0, len(instances))
		for i := range instances {
			fps = append(fps, instances[i].Fingerprint)
		}
		var histRows []database.AlertHistory
		database.DB.Where("event_type = ? AND occurred_at >= ? AND fingerprint IN ?",
			"firing", start, fps).Order("occurred_at asc, id asc").Find(&histRows)

		byFP := make(map[string]map[int64]float64)
		for _, h := range histRows {
			if _, ok := byFP[h.Fingerprint]; !ok {
				byFP[h.Fingerprint] = make(map[int64]float64)
			}
			// 同一毫秒取最后一条 value
			byFP[h.Fingerprint][h.OccurredAt.UnixMilli()] = h.Value
		}
		for fp, tsMap := range byFP {
			if len(tsMap) == 0 {
				continue
			}
			// 兜底：不覆盖已有 PromQL 曲线
			if existing, ok := result[fp]; ok && len(existing) > 0 {
				continue
			}
			tss := make([]int64, 0, len(tsMap))
			for ts := range tsMap {
				tss = append(tss, ts)
			}
			sort.Slice(tss, func(i, j int) bool { return tss[i] < tss[j] })
			pts := make([]model.SamplePair, 0, len(tss))
			for _, ts := range tss {
				pts = append(pts, model.SamplePair{
					Timestamp: model.Time(ts),
					Value:     model.SampleValue(tsMap[ts]),
				})
			}
			result[fp] = pts
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
	// POST .../instances/:fp/resolve 手动结束告警
	if r.Method == "POST" && strings.HasSuffix(fp, "/resolve") {
		a.handleAlertResolve(w, r)
		return
	}
	// POST .../instances/:fp/read 标记已读（红点清零）
	if r.Method == "POST" && strings.HasSuffix(fp, "/read") {
		fp2 := strings.TrimSuffix(fp, "/read")
		fp2 = strings.TrimRight(fp2, "/")
		if err := database.DB.Model(&database.AlertInstance{}).
			Where("fingerprint = ?", fp2).Update("unread_count", 0).Error; err != nil {
			writeError(w, 500, "标记已读失败: "+err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "fingerprint": fp2})
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
	ruleName := instanceRuleName(&ai)
	dsName := instanceDatasourceName(&ai)
	extName := instanceExternalSourceName(&ai)
	writeJSON(w, map[string]interface{}{
		"instance": map[string]interface{}{
			"id":                   ai.ID,
			"fingerprint":          ai.Fingerprint,
			"rule_id":              ai.RuleID,
			"rule_name":            ruleName,
			"datasource_id":        ai.DatasourceID,
			"datasource_name":      dsName,
			"external_source_id":   ai.ExternalSourceID,
			"external_source_name": extName,
			"unread_count":         ai.UnreadCount,
			"firing_count":         ai.FiringCount,
			"labels":               jsonRaw(ai.LabelsJSON),
			"annotations":          jsonRaw(ai.AnnotationsJSON),
			"state":                ai.State,
			"severity":             ai.Severity,
			"value":                ai.Value,
			"threshold":            ai.Threshold,
			"active_at":            ai.ActiveAt,
			"fired_at":             ai.FiredAt,
			"resolved_at":          ai.ResolvedAt,
			"last_eval_at":         ai.LastEvalAt,
			"group_key":            ai.GroupKey,
			"silenced_by":          jsonRaw(ai.SilencedByJSON),
			"inhibited_by":         jsonRaw(ai.InhibitedByJSON),
			"notified_count":       ai.NotifiedCount,
			"last_notified_at":     ai.LastNotifiedAt,
			"next_notify_at":       nextNotifyAt,
		},
		"history":     hist,
		"notify_logs": notifyLogs,
	})
}

// instanceRuleName 解析实例规则名：优先关联规则表，其次 labels.alertname / annotations.summary
func instanceRuleName(ai *database.AlertInstance) string {
	if ai.RuleID != 0 {
		var r database.AlertRule
		if err := database.DB.First(&r, ai.RuleID).Error; err == nil && r.Name != "" {
			return r.Name
		}
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(ai.LabelsJSON), &labels); err == nil {
		if n := labels["alertname"]; n != "" {
			return n
		}
	}
	var ann map[string]string
	if err := json.Unmarshal([]byte(ai.AnnotationsJSON), &ann); err == nil {
		if n := ann["summary"]; n != "" {
			return n
		}
	}
	if ai.ExternalSourceID != 0 {
		var s database.ExternalAlertSource
		if err := database.DB.First(&s, ai.ExternalSourceID).Error; err == nil {
			return "外部告警 [" + s.Name + "]"
		}
	}
	return "未知规则"
}

// instanceDatasourceName 解析实例数据源名：优先数据源表，其次外部告警源名，最后 labels 兜底
func instanceDatasourceName(ai *database.AlertInstance) string {
	if ai.DatasourceID != 0 {
		var ds database.DataSource
		if err := database.DB.First(&ds, ai.DatasourceID).Error; err == nil && ds.Name != "" {
			return ds.Name
		}
	}
	if n := instanceExternalSourceName(ai); n != "" {
		return n
	}
	// 兜底：外部告警写入实例时会把源名带进 labels.datasource_name
	var labels map[string]string
	if err := json.Unmarshal([]byte(ai.LabelsJSON), &labels); err == nil {
		if n := labels["datasource_name"]; n != "" {
			return n
		}
	}
	return ""
}

// instanceExternalSourceName 外部告警源展示名（n9e / 华为云）
func instanceExternalSourceName(ai *database.AlertInstance) string {
	if ai.ExternalSourceID == 0 {
		return ""
	}
	var s database.ExternalAlertSource
	if err := database.DB.First(&s, ai.ExternalSourceID).Error; err != nil {
		return ""
	}
	name := s.Name
	switch s.Type {
	case "n9e":
		name = "n9e · " + name
	case "huaweicloud", "huawei", "ces":
		name = "华为云 · " + name
	}
	return name
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
	// 按规则名筛选（精确匹配规则名，用于历史页筛选下拉）
	if v := strings.TrimSpace(params.Get("rule_name")); v != "" {
		q = q.Where("rule_name = ?", v)
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

// handleAlertHistoryRuleNames 返回历史告警中出现的去重规则名（用于前端筛选下拉）
func (a *AdminAPI) handleAlertHistoryRuleNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	type nameRow struct {
		RuleName string
		Count    int64
	}
	var rows []nameRow
	database.DB.Model(&database.AlertHistory{}).
		Select("rule_name, count(*) as count").
		Where("rule_name != ''").
		Group("rule_name").Order("count desc").Limit(500).Scan(&rows)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.RuleName != "" {
			names = append(names, r.RuleName)
		}
	}
	writeJSON(w, map[string]interface{}{"items": names})
}

// ===== AlertHistory Sessions（已恢复实例聚合视图） =================================

// HistorySession 一个已恢复告警实例的聚合会话：
// 同一指纹（同一规则+标签）的多次触发/恢复被合并为一条，重发细节在详情中查看。
type HistorySession struct {
	Fingerprint     string     `json:"fingerprint"`
	RuleID          uint       `json:"rule_id"`
	RuleName        string     `json:"rule_name"`
	DatasourceID    uint       `json:"datasource_id"`
	DatasourceName  string     `json:"datasource_name"`
	Severity        string     `json:"severity"`
	FirstFiredAt    *time.Time `json:"first_fired_at"`
	ResolvedAt      *time.Time `json:"resolved_at"`
	FiringCount     int        `json:"firing_count"` // 含首次触发与重发
	RepeatCount     int        `json:"repeat_count"` // 重发次数 = firing_count - 1
	Value           float64    `json:"value"`        // 最后一次触发值
	Threshold       float64    `json:"threshold"`
	LabelsJSON      string     `json:"labels_json"`
	AnnotationsJSON string     `json:"annotations_json"`
	DurationSec     int64      `json:"duration_sec"`
}

// handleAlertHistorySessions 历史告警页数据源：只返回已恢复的告警实例（按 fingerprint 聚合）。
// 未恢复（仍在 firing）或重复触发未恢复的记录不在此列表出现，重发事件在实例详情中查看。
// GET /api/promai/alert/history/sessions
func (a *AdminAPI) handleAlertHistorySessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	params := r.URL.Query()

	// 1) 找出所有已恢复的指纹，记录其最后一次恢复时间，并应用筛选
	// COALESCE(occurred_at, created_at)：兜底旧数据中 occurred_at 为 NULL 的行，
	// 避免 MAX() 返回 NULL 被扫描成零值时间（前端显示 0001-01-01）。
	q := database.DB.Model(&database.AlertHistory{}).
		Select("fingerprint, MAX(COALESCE(occurred_at, created_at)) as resolved_at").
		Where("event_type = ?", "resolved")
	if v := params.Get("rule_id"); v != "" {
		q = q.Where("rule_id = ?", v)
	}
	if v := strings.TrimSpace(params.Get("rule_name")); v != "" {
		q = q.Where("rule_name = ?", v)
	}
	if v := params.Get("datasource_id"); v != "" {
		q = q.Where("datasource_id = ?", v)
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
	type fpRow struct {
		Fingerprint string
		ResolvedAt  time.Time
	}
	var fps []fpRow
	q.Group("fingerprint").Order("resolved_at desc").Scan(&fps)
	total := len(fps)

	page, _ := strconv.Atoi(params.Get("page"))
	pageSize, _ := strconv.Atoi(params.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageFps := fps[start:end]
	if len(pageFps) == 0 {
		writeJSON(w, map[string]interface{}{"items": []HistorySession{}, "total": total, "page": page, "page_size": pageSize})
		return
	}
	fpList := make([]string, 0, len(pageFps))
	for _, f := range pageFps {
		fpList = append(fpList, f.Fingerprint)
	}

	// 2) 拉取这些指纹的全部历史事件，在内存中聚合会话
	var all []database.AlertHistory
	database.DB.Where("fingerprint IN ?", fpList).Order("occurred_at asc, id asc").Find(&all)
	agg := make(map[string]*HistorySession, len(fpList))
	for _, h := range all {
		s, ok := agg[h.Fingerprint]
		if !ok {
			s = &HistorySession{
				Fingerprint:     h.Fingerprint,
				RuleID:          h.RuleID,
				RuleName:        h.RuleName,
				DatasourceID:    h.DatasourceID,
				DatasourceName:  h.DatasourceName,
				Severity:        h.Severity,
				LabelsJSON:      h.LabelsJSON,
				AnnotationsJSON: h.AnnotationsJSON,
			}
			agg[h.Fingerprint] = s
		}
		if h.RuleName != "" {
			s.RuleName = h.RuleName
		}
		if h.DatasourceName != "" {
			s.DatasourceName = h.DatasourceName
		}
		if h.Severity != "" {
			s.Severity = h.Severity
		}
		switch h.EventType {
		case "firing":
			s.FiringCount++
			eff := historyEffectiveAt(h)
			if s.FirstFiredAt == nil || eff.Before(*s.FirstFiredAt) {
				s.FirstFiredAt = &eff
			}
			s.Value = h.Value
			s.Threshold = h.Threshold
			if h.LabelsJSON != "" {
				s.LabelsJSON = h.LabelsJSON
			}
			if h.AnnotationsJSON != "" {
				s.AnnotationsJSON = h.AnnotationsJSON
			}
		case "resolved":
			eff := historyEffectiveAt(h)
			if s.ResolvedAt == nil || eff.After(*s.ResolvedAt) {
				s.ResolvedAt = &eff
			}
		}
	}

	// 3) 用指纹列表校准恢复时间（MAX），计算持续时长与重发次数
	items := make([]HistorySession, 0, len(pageFps))
	for _, f := range pageFps {
		s := agg[f.Fingerprint]
		if s == nil {
			continue
		}
		// SQL 聚合值仅在有效（非零）且更晚时采用；零值说明旧数据异常，保留内存兜底结果
		if !f.ResolvedAt.IsZero() && f.ResolvedAt.Year() > 1970 {
			if s.ResolvedAt == nil || f.ResolvedAt.After(*s.ResolvedAt) {
				rt := f.ResolvedAt
				s.ResolvedAt = &rt
			}
		}
		// 兜底：仍无有效恢复时间时退回 CreatedAt，避免 0001-01-01
		if s.ResolvedAt == nil || s.ResolvedAt.IsZero() || s.ResolvedAt.Year() <= 1970 {
			var ct *time.Time
			for _, h := range all {
				if h.Fingerprint == f.Fingerprint && h.EventType == "resolved" && !h.CreatedAt.IsZero() {
					t := h.CreatedAt
					if ct == nil || t.After(*ct) {
						ct = &t
					}
				}
			}
			if ct != nil {
				s.ResolvedAt = ct
			}
		}
		if s.FirstFiredAt != nil && s.ResolvedAt != nil {
			s.DurationSec = int64(s.ResolvedAt.Sub(*s.FirstFiredAt).Seconds())
			if s.DurationSec < 0 {
				s.DurationSec = 0
			}
		}
		if s.FiringCount > 1 {
			s.RepeatCount = s.FiringCount - 1
		}
		items = append(items, *s)
	}
	writeJSON(w, map[string]interface{}{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// historyEffectiveAt 取历史事件的有效时间：
// occurred_at 为零值/1970 之前（旧数据异常）时退回 created_at，避免聚合出 0001-01-01。
func historyEffectiveAt(h database.AlertHistory) time.Time {
	if !h.OccurredAt.IsZero() && h.OccurredAt.Year() > 1970 {
		return h.OccurredAt
	}
	if !h.CreatedAt.IsZero() && h.CreatedAt.Year() > 1970 {
		return h.CreatedAt
	}
	return h.OccurredAt
}

// ===== AlertHistory Timeline =====================================================

// TimelineEntry 时间线的单条事件
type TimelineEntry struct {
	EventID         uint       `json:"event_id,omitempty"`
	Type            string     `json:"type"` // firing / resolved / notify
	Severity        string     `json:"severity,omitempty"`
	Value           float64    `json:"value,omitempty"`
	Threshold       float64    `json:"threshold,omitempty"`
	LabelsJSON      string     `json:"labels_json,omitempty"`
	AnnotationsJSON string     `json:"annotations_json,omitempty"`
	State           string     `json:"state,omitempty"`
	NotifyChannels  string     `json:"notify_channels,omitempty"`
	NotifyResult    string     `json:"notify_result,omitempty"`
	ChannelType     string     `json:"channel_type,omitempty"`
	ChannelName     string     `json:"channel_name,omitempty"`
	Error           string     `json:"error,omitempty"`
	Content         string     `json:"content,omitempty"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	OccurredAt      time.Time  `json:"occurred_at"`
}

// TimelineGroup 一个 (数据源 × 规则) 的时间线
type TimelineGroup struct {
	RuleID         uint            `json:"rule_id"`
	RuleName       string          `json:"rule_name"`
	DatasourceID   uint            `json:"datasource_id"`
	DatasourceName string          `json:"datasource_name"`
	NextNotifyAt   *time.Time      `json:"next_notify_at,omitempty"`
	Entries        []TimelineEntry `json:"entries"`
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
	// 按规则名精确筛选（历史页筛选下拉）
	if v := strings.TrimSpace(params.Get("rule_name")); v != "" {
		q = q.Where("rule_name = ?", v)
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

	// Group by (rule_id, rule_name, datasource_id, datasource_name)
	// 外部告警 RuleID/DatasourceID 可能为 0，必须用名称避免不同 alertname 混在一起
	type gk struct {
		RuleID         uint
		RuleName       string
		DatasourceID   uint
		DatasourceName string
	}
	groupMap := make(map[gk]*TimelineGroup)
	var groupKeys []gk

	for _, h := range histories {
		if h.EventType != "firing" && h.EventType != "resolved" {
			continue
		}
		k := gk{
			RuleID: h.RuleID, RuleName: h.RuleName,
			DatasourceID: h.DatasourceID, DatasourceName: h.DatasourceName,
		}
		grp, ok := groupMap[k]
		if !ok {
			grp = &TimelineGroup{
				RuleID:         h.RuleID,
				RuleName:       h.RuleName,
				DatasourceID:   h.DatasourceID,
				DatasourceName: h.DatasourceName,
				NextNotifyAt:   groupNextNotify[h.RuleID],
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

	// 24h 趋势（按来源细分：外部告警=告警源名，本地告警=数据源名）
	type srcBucket struct {
		Hour   string
		Source string
		Count  int64
	}
	var trendBySource []srcBucket
	database.DB.Model(&database.AlertHistory{}).
		Select("strftime('%Y-%m-%d %H:00', occurred_at) as hour, COALESCE(NULLIF(datasource_name,''),'本地') as source, count(*) as count").
		Where("event_type = ? AND occurred_at >= ?", "firing", since).
		Group("hour, source").Order("hour asc").Scan(&trendBySource)

	// 未读总数（只统计活跃实例，resolved 后不应再显示红点）
	var unreadCount int64
	database.DB.Model(&database.AlertInstance{}).
		Select("COALESCE(SUM(unread_count),0)").
		Where("state IN ?", []string{"pending", "firing"}).
		Scan(&unreadCount)

	// 已恢复统计（实例 resolved 后会被清理，从历史表统计更稳定）
	var resolvedCount24h, resolvedTotal int64
	database.DB.Model(&database.AlertHistory{}).
		Where("event_type = ?", "resolved").Count(&resolvedTotal)
	database.DB.Model(&database.AlertHistory{}).
		Where("event_type = ? AND occurred_at >= ?", "resolved", since).
		Count(&resolvedCount24h)

	writeJSON(w, map[string]interface{}{
		"by_severity":         bySeverity,
		"by_state":            byState,
		"top_rules":           topRules,
		"top_datasources":     topDS,
		"trend_24h":           trend,
		"trend_24h_by_source": trendBySource,
		"unread_count":        unreadCount,
		"resolved_count":      resolvedCount24h,
		"resolved_total":      resolvedTotal,
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
