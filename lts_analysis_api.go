package main

// AI 分析记录 API（Phase 2 —— token 汇总看板 + 日志留档查看面板）：
//
//   - GET /api/promai/ai-analysis-records            记录列表（含 token 计量列，分页/筛选）
//   - GET /api/promai/ai-analysis-records/summary    汇总卡（今日 / 本月消耗，token 数 + 估算金额）
//   - GET /api/promai/ai-analysis-records/:id        单条详情（含 LogsJSON 留档面板数据）

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// analysisRecordItem 列表项（剔除 Prompt/LogsJSON 大字段，只保留 token 计量等展示列）。
type analysisRecordItem struct {
	ID               uint      `json:"id"`
	Type             string    `json:"type"`
	RefID            string    `json:"ref_id"`
	RuleID           uint      `json:"rule_id"`
	ModelName        string    `json:"model_name"`
	Status           string    `json:"status"`
	DurationMs       int64     `json:"duration_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CostEst          *float64  `json:"cost_est,omitempty"`
	TokensEstimated  bool      `json:"tokens_estimated"`
	HasLogs          bool      `json:"has_logs"` // 是否留档了日志证据（LogsJSON 非空）
	CreatedAt        time.Time `json:"created_at"`
}

// handleLTSAnalysisRecords 返回 AI 分析记录列表（分页 + 类型筛选）。
func (a *AdminAPI) handleLTSAnalysisRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}

	db := database.DB.Model(&database.AiAnalysisRecord{})
	if t := q.Get("type"); t != "" {
		db = db.Where("type = ?", t)
	}
	if kw := strings.TrimSpace(q.Get("keyword")); kw != "" {
		like := "%" + kw + "%"
		db = db.Where("ref_id LIKE ? OR model_name LIKE ? OR result LIKE ?", like, like, like)
	}

	var total int64
	db.Count(&total)

	var recs []database.AiAnalysisRecord
	db.Order("created_at desc").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&recs)

	items := make([]analysisRecordItem, 0, len(recs))
	for _, rec := range recs {
		items = append(items, analysisRecordItem{
			ID:               rec.ID,
			Type:             rec.Type,
			RefID:            rec.RefID,
			RuleID:           rec.RuleID,
			ModelName:        rec.ModelName,
			Status:           rec.Status,
			DurationMs:       rec.DurationMs,
			PromptTokens:     rec.PromptTokens,
			CompletionTokens: rec.CompletionTokens,
			TotalTokens:      rec.TotalTokens,
			CostEst:          rec.CostEst,
			TokensEstimated:  rec.TokensEstimated,
			HasLogs:          strings.TrimSpace(rec.LogsJSON) != "",
			CreatedAt:        rec.CreatedAt,
		})
	}

	writeJSON(w, map[string]interface{}{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleLTSAnalysisSummary 返回 token 汇总卡：今日 / 本月消耗（次数 + token 数 + 估算金额）+ 日预算。
func (a *AdminAPI) handleLTSAnalysisSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}

	type bucket struct {
		Calls            int64   `json:"calls"`
		PromptTokens     int64   `json:"prompt_tokens"`
		CompletionTokens int64   `json:"completion_tokens"`
		TotalTokens      int64   `json:"total_tokens"`
		CostEst          float64 `json:"cost_est"`
	}
	agg := func(cond string) bucket {
		var b bucket
		row := struct {
			Calls            int64
			PromptTokens     int64
			CompletionTokens int64
			TotalTokens      int64
			CostEst          float64
		}{}
		database.DB.Model(&database.AiAnalysisRecord{}).
			Select("count(*) as calls, coalesce(sum(prompt_tokens),0) as prompt_tokens, " +
				"coalesce(sum(completion_tokens),0) as completion_tokens, " +
				"coalesce(sum(total_tokens),0) as total_tokens, coalesce(sum(cost_est),0) as cost_est").
			Where(cond).
			Scan(&row)
		b.Calls = row.Calls
		b.PromptTokens = row.PromptTokens
		b.CompletionTokens = row.CompletionTokens
		b.TotalTokens = row.TotalTokens
		b.CostEst = row.CostEst
		return b
	}

	today := agg("date(created_at) = date('now','localtime')")
	month := agg("date(created_at) >= date('now','start of month')")

	writeJSON(w, map[string]interface{}{
		"today":        today,
		"month":        month,
		"daily_budget": ltsDailyTokenBudget(),
	})
}

// handleLTSAnalysisRecordByID 返回单条分析记录详情，含 LogsJSON 留档面板数据。
func (a *AdminAPI) handleLTSAnalysisRecordByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "不支持的请求方法")
		return
	}
	id, err := getLastPathID(r.URL.Path)
	if err != nil || id == 0 {
		writeError(w, 400, "无效的记录 ID")
		return
	}
	var rec database.AiAnalysisRecord
	if err := database.DB.First(&rec, id).Error; err != nil {
		writeError(w, 404, "分析记录不存在")
		return
	}

	// 解析 LogsJSON 为结构化面板数据（query / folded / samples）
	var logsData map[string]interface{}
	if strings.TrimSpace(rec.LogsJSON) != "" {
		_ = json.Unmarshal([]byte(rec.LogsJSON), &logsData)
	}

	writeJSON(w, map[string]interface{}{
		"id":                rec.ID,
		"type":              rec.Type,
		"ref_id":            rec.RefID,
		"rule_id":           rec.RuleID,
		"model_name":        rec.ModelName,
		"status":            rec.Status,
		"error":             rec.Error,
		"duration_ms":       rec.DurationMs,
		"prompt_tokens":     rec.PromptTokens,
		"completion_tokens": rec.CompletionTokens,
		"total_tokens":      rec.TotalTokens,
		"cost_est":          rec.CostEst,
		"tokens_estimated":  rec.TokensEstimated,
		"prompt":            rec.Prompt,
		"result":            rec.Result,
		"logs":              logsData,
		"created_at":        rec.CreatedAt,
	})
}
