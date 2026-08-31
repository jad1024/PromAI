package main

// 华为云 LTS 告警触发 AI 巡检 —— 触发规则 CRUD API。
//
//   - GET|POST /api/promai/alert-trigger-rules         列表 / 创建
//   - GET|PUT|DELETE /api/promai/alert-trigger-rules/:id 单条读写 / 删除
//   - POST /api/promai/alert-trigger-rules/:id/test     试运行：回放最近历史告警预览命中
//   - GET /api/promai/alert-trigger-rules/token-stats    LTS 巡检 token 汇总（按 天/类型/模型）

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"PromAI/pkg/alerting"
	"PromAI/pkg/alerting/lts"
	"PromAI/pkg/alerting/webhook"
	"PromAI/pkg/database"
)

// decodeTriggerMatchers 解析 MatchersJSON 为 []database.TriggerMatcher，失败返回 error。
func decodeTriggerMatchers(raw string) ([]database.TriggerMatcher, error) {
	var ms []database.TriggerMatcher
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// validateTriggerRule 校验触发规则必填字段与匹配条件，返回错误信息（空串=合法）。
func validateTriggerRule(r *database.AlertTriggerRule) string {
	if strings.TrimSpace(r.Name) == "" {
		return "名称不能为空"
	}
	if strings.TrimSpace(r.LogGroupID) == "" {
		return "日志组 ID 不能为空"
	}
	if strings.TrimSpace(r.LogStreamID) == "" {
		return "日志流 ID 不能为空"
	}
	ms, err := decodeTriggerMatchers(r.MatchersJSON)
	if err != nil {
		return "匹配条件 JSON 格式错误: " + err.Error()
	}
	if len(ms) == 0 {
		return "至少需要一个匹配条件"
	}
	validOp := map[string]bool{"equals": true, "contains": true, "wildcard": true, "regex": true, "cidr": true}
	for i := range ms {
		if strings.TrimSpace(ms[i].Field) == "" {
			return "匹配条件字段不能为空"
		}
		if !validOp[strings.ToLower(strings.TrimSpace(ms[i].Operator))] {
			return "匹配条件操作符不支持: " + ms[i].Operator
		}
		if strings.TrimSpace(ms[i].Value) == "" {
			return "匹配条件值不能为空"
		}
	}
	return ""
}

// prepareTriggerRuleFields 归一化默认值 + 序列化 NotifyChannelIDs（供创建/更新前调用）。
func prepareTriggerRuleFields(r *database.AlertTriggerRule) {
	if r.TimeWindowMinutes <= 0 {
		r.TimeWindowMinutes = 15
	}
	if r.Limit <= 0 {
		r.Limit = 200
	}
	if strings.TrimSpace(r.LevelFilter) == "" {
		r.LevelFilter = "ERROR,FATAL"
	}
	r.NotifyChannelIDsRaw = alerting.EncodeUintSlice(r.NotifyChannelIDs)
}

// handleAlertTriggerRules 列表 / 创建。
func (a *AdminAPI) handleAlertTriggerRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		var list []database.AlertTriggerRule
		database.DB.Order("created_at desc").Find(&list)
		for i := range list {
			list[i].NotifyChannelIDs = alerting.DecodeUintSlice(list[i].NotifyChannelIDsRaw)
		}
		writeJSON(w, list)
	case "POST":
		var req database.AlertTriggerRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "请求体格式错误")
			return
		}
		if msg := validateTriggerRule(&req); msg != "" {
			writeError(w, 400, msg)
			return
		}
		prepareTriggerRuleFields(&req)
		if err := database.DB.Create(&req).Error; err != nil {
			writeError(w, 500, "创建触发规则失败: "+err.Error())
			return
		}
		req.NotifyChannelIDs = alerting.DecodeUintSlice(req.NotifyChannelIDsRaw)
		writeJSON(w, req)
	default:
		writeError(w, 405, "不支持的请求方法")
	}
}

// handleAlertTriggerRuleByID 单条读写 / 删除 + 试运行子路径。
func (a *AdminAPI) handleAlertTriggerRuleByID(w http.ResponseWriter, r *http.Request) {
	// 子路径 /:id/test 的 ID 在倒数第二位
	var id uint
	var idErr error
	if strings.HasSuffix(r.URL.Path, "/test") {
		id, idErr = parseParentID(r.URL.Path)
	} else {
		id, idErr = getLastPathID(r.URL.Path)
	}
	if id == 0 || idErr != nil {
		writeError(w, 400, "无效的触发规则 ID")
		return
	}

	// 试运行：回放最近历史告警预览命中（不查 LTS、不跑 AI、不推送）
	if strings.HasSuffix(r.URL.Path, "/test") {
		if r.Method != "POST" {
			writeError(w, 405, "不支持的请求方法")
			return
		}
		a.handleAlertTriggerRuleTest(w, r, id)
		return
	}

	var rule database.AlertTriggerRule
	if err := database.DB.First(&rule, id).Error; err != nil {
		writeError(w, 404, "触发规则不存在")
		return
	}

	switch r.Method {
	case "GET":
		rule.NotifyChannelIDs = alerting.DecodeUintSlice(rule.NotifyChannelIDsRaw)
		writeJSON(w, rule)
	case "PUT":
		var req database.AlertTriggerRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "请求体格式错误")
			return
		}
		if msg := validateTriggerRule(&req); msg != "" {
			writeError(w, 400, msg)
			return
		}
		prepareTriggerRuleFields(&req)
		updates := map[string]interface{}{
			"name":                   req.Name,
			"description":            req.Description,
			"matchers_json":          req.MatchersJSON,
			"source_id":              req.SourceID,
			"log_group_id":           req.LogGroupID,
			"log_stream_id":          req.LogStreamID,
			"time_window_minutes":    req.TimeWindowMinutes,
			"keywords":               req.Keywords,
			"level_filter":           req.LevelFilter,
			"limit":                  req.Limit,
			"inspection_template_id": req.InspectionTemplateID,
			"notify_channel_ids":     req.NotifyChannelIDsRaw,
			"enabled":                req.Enabled,
		}
		if err := database.DB.Model(&database.AlertTriggerRule{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			writeError(w, 500, "更新触发规则失败: "+err.Error())
			return
		}
		database.DB.First(&rule, id)
		rule.NotifyChannelIDs = alerting.DecodeUintSlice(rule.NotifyChannelIDsRaw)
		writeJSON(w, rule)
	case "DELETE":
		if err := database.DB.Delete(&database.AlertTriggerRule{}, id).Error; err != nil {
			writeError(w, 500, "删除触发规则失败: "+err.Error())
			return
		}
		w.WriteHeader(204)
	default:
		writeError(w, 405, "不支持的请求方法")
	}
}

// handleAlertTriggerRuleTest 试运行：对最近 N 条历史告警回放匹配，返回命中预览。
// 仅评估 Matchers 匹配，不查询 LTS、不调用 AI、不推送，用于创建规则前的效果预览。
func (a *AdminAPI) handleAlertTriggerRuleTest(w http.ResponseWriter, r *http.Request, id uint) {
	var rule database.AlertTriggerRule
	if err := database.DB.First(&rule, id).Error; err != nil {
		writeError(w, 404, "触发规则不存在")
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	var history []database.AlertHistory
	database.DB.Where("removed_at IS NULL").Order("occurred_at desc").Limit(limit).Find(&history)

	matched := make([]map[string]interface{}, 0)
	for i := range history {
		h := &history[i]
		ev := historyToAlertEvent(h)
		ok, err := lts.MatchRule(&rule, ev)
		if err != nil {
			writeError(w, 500, "匹配条件解析失败: "+err.Error())
			return
		}
		if ok {
			matched = append(matched, map[string]interface{}{
				"fingerprint": h.Fingerprint,
				"rule_name":   h.RuleName,
				"severity":    h.Severity,
				"state":       h.State,
				"occurred_at": h.OccurredAt,
			})
		}
	}
	writeJSON(w, map[string]interface{}{
		"rule_id":      id,
		"scanned":      len(history),
		"matched":      len(matched),
		"matched_list": matched,
	})
}

// historyToAlertEvent 将一条历史告警还原为 webhook.AlertEvent（用于规则试运行匹配）。
func historyToAlertEvent(h *database.AlertHistory) *webhook.AlertEvent {
	labels := map[string]string{}
	if h.LabelsJSON != "" {
		_ = json.Unmarshal([]byte(h.LabelsJSON), &labels)
	}
	annotations := map[string]string{}
	if h.AnnotationsJSON != "" {
		_ = json.Unmarshal([]byte(h.AnnotationsJSON), &annotations)
	}
	return &webhook.AlertEvent{
		Source:      h.DatasourceName,
		RuleName:    h.RuleName,
		Severity:    h.Severity,
		State:       h.State,
		Labels:      labels,
		Annotations: annotations,
		Value:       h.Value,
		Threshold:   h.Threshold,
		OccurredAt:  h.OccurredAt,
	}
}

// handleLTSTokenStats 返回 LTS 巡检 token 汇总（按 天/类型/模型），含今日已用与日预算。
func (a *AdminAPI) handleLTSTokenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	type row struct {
		Day              string  `json:"day"`
		Type             string  `json:"type"`
		ModelName        string  `json:"model_name"`
		Calls            int64   `json:"calls"`
		PromptTokens     int64   `json:"prompt_tokens"`
		CompletionTokens int64   `json:"completion_tokens"`
		TotalTokens      int64   `json:"total_tokens"`
		CostEst          float64 `json:"cost_est"`
	}
	var rows []row
	database.DB.Model(&database.AiAnalysisRecord{}).
		Select("date(created_at) as day, type, model_name, count(*) as calls, "+
			"coalesce(sum(prompt_tokens),0) as prompt_tokens, "+
			"coalesce(sum(completion_tokens),0) as completion_tokens, "+
			"coalesce(sum(total_tokens),0) as total_tokens, "+
			"coalesce(sum(cost_est),0) as cost_est").
		Where("type = ?", "lts_alert").
		Group("date(created_at), type, model_name").
		Order("day desc, model_name asc").
		Limit(days).
		Scan(&rows)

	// 今日总览（跨类型，便于与日预算对比）
	var todayTotal int64
	var todayCalls int64
	database.DB.Model(&database.AiAnalysisRecord{}).
		Where("type = ? AND date(created_at) = date('now','localtime')", "lts_alert").
		Select("coalesce(sum(total_tokens),0)").
		Scan(&todayTotal)
	database.DB.Model(&database.AiAnalysisRecord{}).
		Where("type = ? AND date(created_at) = date('now','localtime')", "lts_alert").
		Count(&todayCalls)

	writeJSON(w, map[string]interface{}{
		"items":              rows,
		"today_total_tokens": todayTotal,
		"today_calls":        todayCalls,
		"daily_budget":       ltsDailyTokenBudget(),
	})
}

// ltsDailyTokenBudget 读取日 token 预算（复用 pi-agent 的 AppSetting 约定：ai_daily_token_budget，默认 500000，0=不限）。
func ltsDailyTokenBudget() int {
	const fallback = 500000
	if v := strings.TrimSpace(database.GetAppSetting("ai_daily_token_budget")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
