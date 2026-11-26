package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"PromAI/pkg/database"
)

// ============================================================================
// 告警驱动 AI 巡检链路（PromAI 核心差异化能力）
//
// 链路：外部告警(LTS/SMN/n9e) → 规则匹配 → 冷却+并发 → 日预算 → 日志检索
//       → Java 日志降噪 → headless AI 巡检 → 落库(token+原始日志) → 报告推送
//
// 本文件提供两类 API：
//   1. /api/promai/pipeline/overview   链路各环节的真实数据统计（产品主线可视化）
//   2. /api/promai/ai/feedback + /api/promai/ai/effectiveness
//      AI 结论确认与告警下降效果评估（数据飞轮）
// ============================================================================

// handlePipelineOverview 汇总链路 7 个环节的真实计数。
func (a *AdminAPI) handlePipelineOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	days := 7
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 90 {
		days = d
	}
	since := time.Now().AddDate(0, 0, -days)

	var enabledSources, totalSources int64
	database.DB.Model(&database.ExternalAlertSource{}).Count(&totalSources)
	database.DB.Model(&database.ExternalAlertSource{}).Where("enabled = ?", true).Count(&enabledSources)

	var alertTotal, alertFiring, alertExternal int64
	database.DB.Model(&database.AlertInstance{}).Where("created_at >= ?", since).Count(&alertTotal)
	database.DB.Model(&database.AlertInstance{}).Where("state IN ?", []string{"firing", "pending"}).Count(&alertFiring)
	database.DB.Model(&database.AlertInstance{}).Where("external_source_id > 0 AND created_at >= ?", since).Count(&alertExternal)

	var incidents int64
	database.DB.Model(&database.AlertGroup{}).Where("created_at >= ?", since).Count(&incidents)

	var aiRecords, aiSuccess int64
	database.DB.Model(&database.AiAnalysisRecord{}).Where("created_at >= ?", since).Count(&aiRecords)
	database.DB.Model(&database.AiAnalysisRecord{}).Where("status = ? AND created_at >= ?", "success", since).Count(&aiSuccess)

	var inspections, inspectionsSuccess int64
	database.DB.Model(&database.InspectRecord{}).Where("created_at >= ?", since).Count(&inspections)
	database.DB.Model(&database.InspectRecord{}).Where("status = ? AND created_at >= ?", "success", since).Count(&inspectionsSuccess)

	var reports int64
	database.DB.Model(&database.ReportRecord{}).Where("created_at >= ?", since).Count(&reports)

	var aiJobs, aiCronJobs int64
	database.DB.Model(&database.CronJob{}).Where("enabled = ?", true).Count(&aiJobs)
	database.DB.Model(&database.CronJob{}).Where("enabled = ? AND ai_analysis_enabled = ?", true, true).Count(&aiCronJobs)

	type stage struct {
		Key   string `json:"key"`
		Name  string `json:"name"`
		Desc  string `json:"desc"`
		Count int64  `json:"count"`
		Link  string `json:"link"`
	}
	stages := []stage{
		{Key: "source", Name: "告警接入", Desc: "外部告警源（LTS/SMN/n9e）已启用数量", Count: enabledSources, Link: "/alert-sources"},
		{Key: "match", Name: "规则匹配", Desc: "近 " + strconv.Itoa(days) + " 天外部平台推送的告警事件", Count: alertExternal, Link: "/alerts"},
		{Key: "guard", Name: "冷却与预算", Desc: "启用的定时任务（冷却/并发/预算在任务层配置）", Count: aiJobs, Link: "/cronjobs"},
		{Key: "denoise", Name: "告警降噪", Desc: "近 " + strconv.Itoa(days) + " 天同源聚合后的故障数", Count: incidents, Link: "/alert-events"},
		{Key: "ai", Name: "AI 巡检分析", Desc: "近 " + strconv.Itoa(days) + " 天 AI 诊断记录", Count: aiRecords, Link: "/ai"},
		{Key: "persist", Name: "巡检落库", Desc: "近 " + strconv.Itoa(days) + " 天完成的巡检任务", Count: inspectionsSuccess, Link: "/inspect-records"},
		{Key: "notify", Name: "报告推送", Desc: "近 " + strconv.Itoa(days) + " 天生成的巡检报告", Count: reports, Link: "/reports"},
	}

	writeJSON(w, map[string]interface{}{
		"days":         days,
		"stages":       stages,
		"alert_firing": alertFiring,
		"ai_success":   aiSuccess,
		"ai_cron_jobs": aiCronJobs,
		"total_sources": totalSources,
		"total_alerts":  alertTotal,
	})
}

// handleAIFeedback 提交/查询 AI 结论效果反馈。
func (a *AdminAPI) handleAIFeedback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		var fb database.AIFeedback
		if err := json.NewDecoder(r.Body).Decode(&fb); err != nil {
			writeError(w, 400, "请求体格式错误: "+err.Error())
			return
		}
		if fb.Verdict == "" {
			fb.Verdict = "pending"
		}
		if fb.CreatedAt.IsZero() {
			fb.CreatedAt = time.Now()
		}
		if database.DB.Create(&fb).Error != nil {
			writeError(w, 500, "保存反馈失败")
			return
		}
		writeJSON(w, fb)
	case "GET":
		analysisID, _ := strconv.Atoi(r.URL.Query().Get("analysis_id"))
		q := database.DB.Model(&database.AIFeedback{}).Order("created_at desc").Limit(200)
		if analysisID > 0 {
			q = q.Where("analysis_id = ?", analysisID)
		}
		if ref := r.URL.Query().Get("ref_id"); ref != "" {
			q = q.Where("ref_id = ?", ref)
		}
		var items []database.AIFeedback
		q.Find(&items)
		writeJSON(w, map[string]interface{}{"items": items})
	default:
		writeError(w, 405, "不支持的请求方法")
	}
}

// handleAIEffectiveness 效果评估汇总：确认率 + 平均告警下降幅度。
func (a *AdminAPI) handleAIEffectiveness(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 365 {
		days = d
	}
	since := time.Now().AddDate(0, 0, -days)

	var confirmed, invalid, pending int64
	database.DB.Model(&database.AIFeedback{}).Where("verdict = ? AND created_at >= ?", "confirmed", since).Count(&confirmed)
	database.DB.Model(&database.AIFeedback{}).Where("verdict = ? AND created_at >= ?", "invalid", since).Count(&invalid)
	database.DB.Model(&database.AIFeedback{}).Where("verdict = ? AND created_at >= ?", "pending", since).Count(&pending)

	// 用 SQL 聚合计算告警下降幅度，避免把反馈明细拉进内存。
	type deltaRow struct {
		N     int64
		Total int64
	}
	var dr deltaRow
	database.DB.Model(&database.AIFeedback{}).
		Select("COUNT(*) AS n, SUM(alerts_before - alerts_after) AS total").
		Where("created_at >= ? AND alerts_before > 0", since).
		Scan(&dr)

	total := confirmed + invalid + pending
	confirmRate := 0.0
	if total > 0 {
		confirmRate = float64(confirmed) / float64(total) * 100
	}
	avgDrop := 0.0
	if dr.N > 0 {
		avgDrop = float64(dr.Total) / float64(dr.N)
	}

	writeJSON(w, map[string]interface{}{
		"days":           days,
		"confirmed":      confirmed,
		"invalid":        invalid,
		"pending":        pending,
		"total":          total,
		"confirm_rate":   confirmRate,
		"avg_alert_drop": avgDrop,
	})
}

// 避免编译期未使用 log 导入（保留 log 用于后续扩展诊断日志）。
var _ = log.Printf
