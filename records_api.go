package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// ===== AI 效果反馈：可关联来源候选 =====
// 反馈表单的 ref_id 以前是自由文本，用户不知道 task_id / group_key 去哪看。
// 这里直接把系统中真实存在的巡检 task_id、告警 group_key、AI 诊断 ref_id 列出来供选择。

type feedbackSource struct {
	Type      string `json:"type"`       // inspection / alert / diagnosis
	ID        string `json:"id"`         // 实际写入 ref_id 的值
	Label     string `json:"label"`      // 下拉展示文案
	CreatedAt string `json:"created_at"` // 展示用时间
}

func (a *AdminAPI) handleAIFeedbackSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	items := []feedbackSource{}
	seen := map[string]bool{}
	add := func(typ, id, label string, t time.Time) {
		id = strings.TrimSpace(id)
		key := typ + "|" + id
		if id == "" || seen[key] {
			return
		}
		seen[key] = true
		items = append(items, feedbackSource{
			Type: typ, ID: id, Label: label,
			CreatedAt: t.Format("2006-01-02 15:04"),
		})
	}

	var inspects []database.InspectRecord
	database.DB.Select("task_id, datasource_name, created_at").
		Order("created_at desc").Limit(50).Find(&inspects)
	for _, rec := range inspects {
		add("inspection", rec.TaskID, "巡检 · "+rec.TaskID+" · "+rec.DatasourceName, rec.CreatedAt)
	}

	var alerts []database.AlertInstance
	database.DB.Select("group_key, state, last_eval_at").
		Where("group_key <> ''").Order("last_eval_at desc").Limit(50).Find(&alerts)
	for _, rec := range alerts {
		add("alert", rec.GroupKey, "告警 · "+rec.GroupKey+" · "+rec.State, rec.LastEvalAt)
	}

	var diagnoses []database.AiAnalysisRecord
	database.DB.Select("ref_id, type, created_at").
		Where("ref_id <> ''").Order("created_at desc").Limit(50).Find(&diagnoses)
	for _, rec := range diagnoses {
		add("diagnosis", rec.RefID, "AI 诊断 · "+rec.RefID+" · "+rec.Type, rec.CreatedAt)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	if len(items) > 150 {
		items = items[:150]
	}
	writeJSON(w, map[string]interface{}{"items": items})
}

// ===== 通用清理请求 =====
// ids：精确批量删除；keep_recent：保留最近 N 条；older_than_days：删除 N 天前的。
// 三者可组合；ids 存在时按 ids 精确删除，其余两项用于"一键清理"场景。
type cleanupRequest struct {
	IDs           []uint `json:"ids"`
	KeepRecent    *int   `json:"keep_recent"`
	OlderThanDays *int   `json:"older_than_days"`
	Confirm       bool   `json:"confirm"`
}

func decodeCleanupRequest(w http.ResponseWriter, r *http.Request) (cleanupRequest, bool) {
	var req cleanupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "请求格式错误: "+err.Error())
		return req, false
	}
	if len(req.IDs) == 0 && req.KeepRecent == nil && req.OlderThanDays == nil {
		writeError(w, 400, "需指定 ids、keep_recent 或 older_than_days")
		return req, false
	}
	if !req.Confirm {
		writeError(w, 400, "缺少 confirm 确认标记")
		return req, false
	}
	return req, true
}

// applyCleanupPolicy 按保留策略从时间倒序的候选中筛出要删除的记录。
// ids 非空时直接返回 ids 命中的候选（忽略保留策略），避免误删。
func applyCleanupPolicy(rows []database.ReportRecord, req cleanupRequest) []database.ReportRecord {
	if len(req.IDs) > 0 {
		want := map[uint]bool{}
		for _, id := range req.IDs {
			want[id] = true
		}
		out := rows[:0]
		for _, rec := range rows {
			if want[rec.ID] {
				out = append(out, rec)
			}
		}
		return out
	}
	out := rows
	if req.KeepRecent != nil {
		keep := *req.KeepRecent
		if keep < 0 {
			keep = 0
		}
		if keep >= len(out) {
			out = nil
		} else {
			out = out[keep:]
		}
	}
	if req.OlderThanDays != nil {
		cutoff := time.Now().Add(-time.Duration(*req.OlderThanDays) * 24 * time.Hour)
		filtered := out[:0]
		for _, rec := range out {
			if rec.CreatedAt.Before(cutoff) {
				filtered = append(filtered, rec)
			}
		}
		out = filtered
	}
	return out
}

// handleReportBatchDelete POST /api/promai/report-records/batch-delete
func (a *AdminAPI) handleReportBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	req, ok := decodeCleanupRequest(w, r)
	if !ok {
		return
	}
	q := database.DB.Model(&database.ReportRecord{})
	if len(req.IDs) > 0 {
		q = q.Where("id IN ?", req.IDs)
	}
	var rows []database.ReportRecord
	if err := q.Select("id, title, file_path, created_at").
		Order("created_at desc, id desc").Limit(10000).Find(&rows).Error; err != nil {
		writeError(w, 500, "查询报告失败: "+err.Error())
		return
	}
	toDelete := applyCleanupPolicy(rows, req)

	ids := make([]uint, 0, len(toDelete))
	filesRemoved := 0
	for _, rec := range toDelete {
		ids = append(ids, rec.ID)
		if rec.FilePath != "" {
			if err := os.Remove(rec.FilePath); err == nil {
				filesRemoved++
			}
		}
	}
	if len(ids) > 0 {
		if err := database.DB.Delete(&database.ReportRecord{}, ids).Error; err != nil {
			writeError(w, 500, "删除报告记录失败: "+err.Error())
			return
		}
	}
	auditLog("report_batch_delete", fmt.Sprintf("records=%d files=%d", len(ids), filesRemoved))
	writeJSON(w, map[string]interface{}{
		"deleted": len(ids), "files_removed": filesRemoved, "scanned": len(rows),
	})
}

// handleInspectRecordBatchDelete POST /api/promai/inspect/records/batch-delete
func (a *AdminAPI) handleInspectRecordBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	req, ok := decodeCleanupRequest(w, r)
	if !ok {
		return
	}
	q := database.DB.Model(&database.InspectRecord{})
	if len(req.IDs) > 0 {
		q = q.Where("id IN ?", req.IDs)
	}
	var rows []database.InspectRecord
	if err := q.Select("id, task_id, created_at").
		Order("created_at desc, id desc").Limit(10000).Find(&rows).Error; err != nil {
		writeError(w, 500, "查询巡检记录失败: "+err.Error())
		return
	}
	// 复用同一策略：InspectRecord 与 ReportRecord 的 id/created_at 字段一致，
	// 这里转成通用候选结构，避免为两种模型各写一份重复的筛选逻辑。
	candidates := make([]cleanupCandidate, 0, len(rows))
	for _, rec := range rows {
		candidates = append(candidates, cleanupCandidate{ID: rec.ID, CreatedAt: rec.CreatedAt})
	}
	toDelete := filterCleanupCandidates(candidates, req)
	ids := make([]uint, 0, len(toDelete))
	for _, c := range toDelete {
		ids = append(ids, c.ID)
	}
	if len(ids) > 0 {
		if err := database.DB.Delete(&database.InspectRecord{}, ids).Error; err != nil {
			writeError(w, 500, "删除巡检记录失败: "+err.Error())
			return
		}
	}
	auditLog("inspect_record_batch_delete", fmt.Sprintf("records=%d", len(ids)))
	writeJSON(w, map[string]interface{}{
		"deleted": len(ids), "scanned": len(rows),
	})
}

type cleanupCandidate struct {
	ID        uint
	CreatedAt time.Time
}

func filterCleanupCandidates(rows []cleanupCandidate, req cleanupRequest) []cleanupCandidate {
	if len(req.IDs) > 0 {
		want := map[uint]bool{}
		for _, id := range req.IDs {
			want[id] = true
		}
		out := rows[:0]
		for _, rec := range rows {
			if want[rec.ID] {
				out = append(out, rec)
			}
		}
		return out
	}
	out := rows
	if req.KeepRecent != nil {
		keep := *req.KeepRecent
		if keep < 0 {
			keep = 0
		}
		if keep >= len(out) {
			out = nil
		} else {
			out = out[keep:]
		}
	}
	if req.OlderThanDays != nil {
		cutoff := time.Now().Add(-time.Duration(*req.OlderThanDays) * 24 * time.Hour)
		filtered := out[:0]
		for _, rec := range out {
			if rec.CreatedAt.Before(cutoff) {
				filtered = append(filtered, rec)
			}
		}
		out = filtered
	}
	return out
}
