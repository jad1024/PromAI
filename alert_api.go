package main

// alerting 子系统的所有 HTTP handler 注册在此文件。
//
// 路由前缀统一为 /api/promai/alert/...，权限走现有 authMiddleware。
// 所有 handler 都依赖全局 adminAlerting (在 main.go 注册时注入)，
// 因此即便 evaluator/dispatcher 还未启动，CRUD 接口仍可工作（只是无评估结果）。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/alerting"
	alertstore "PromAI/pkg/alerting/store"
	"PromAI/pkg/database"
)

// ===== AlertRule CRUD ===========================================================

func (a *AdminAPI) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		page, pageSize := parsePagination(r)
		filter := map[string]interface{}{}
		if k := strings.TrimSpace(r.URL.Query().Get("keyword")); k != "" {
			filter["keyword"] = k
		}
		if v := strings.TrimSpace(r.URL.Query().Get("severity")); v != "" {
			filter["severity"] = v
		}
		if v := r.URL.Query().Get("enabled"); v != "" {
			filter["enabled"] = v == "true"
		}
		rows, total, err := alertstore.ListRules(database.DB, filter, page, pageSize)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"items": rows, "total": total})
	case "POST":
		var req database.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, 400, "name is required")
			return
		}
		if err := validateRule(&req); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if len(req.DatasourceIDs) == 0 && strings.TrimSpace(req.DatasourceSelectorJSON) == "" {
			writeError(w, 400, "either datasource_ids or datasource_selector must be provided")
			return
		}
		if err := alertstore.CreateRule(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *AdminAPI) handleAlertRuleByID(w http.ResponseWriter, r *http.Request) {
	// 路径形如 /api/promai/alert/rules/{id}[/test]
	// 特殊动作：/api/promai/alert/rules/batch-toggle, /api/promai/alert/rules/batch-edit, /api/promai/alert/rules/generate-from-template
	path := strings.TrimPrefix(r.URL.Path, "/api/promai/alert/rules/")
	parts := strings.Split(strings.TrimRight(path, "/"), "/")

	if len(parts) >= 1 && parts[0] == "batch-toggle" && r.Method == "POST" {
		a.handleAlertRuleBatchToggle(w, r)
		return
	}
	if len(parts) >= 1 && parts[0] == "batch-delete" && r.Method == "POST" {
		a.handleAlertRuleBatchDelete(w, r)
		return
	}
	if len(parts) >= 1 && parts[0] == "batch-edit" && r.Method == "POST" {
		a.handleAlertRuleBatchEdit(w, r)
		return
	}
	if len(parts) >= 1 && parts[0] == "generate-from-template" && r.Method == "POST" {
		a.handleAlertRuleGenerateFromTemplate(w, r)
		return
	}

	idStr := parts[0]
	id64, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	id := uint(id64)

	// 子动作
	if len(parts) >= 2 && parts[1] == "test" && r.Method == "POST" {
		a.handleAlertRuleTest(w, r, id)
		return
	}

	switch r.Method {
	case "GET":
		rule, err := alertstore.GetRule(database.DB, id)
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, rule)
	case "PUT":
		var req database.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		req.ID = id
		if err := validateRule(&req); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if err := alertstore.UpdateRule(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	case "DELETE":
		if err := alertstore.DeleteRule(database.DB, id); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true})
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *AdminAPI) handleAlertRuleBatchToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs     []uint `json:"ids"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, 400, "ids is required")
		return
	}
	if err := alertstore.BatchToggleRules(database.DB, req.IDs, req.Enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "count": len(req.IDs)})
}

func (a *AdminAPI) handleAlertRuleGenerateFromTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TemplateID uint `json:"template_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.TemplateID == 0 {
		writeError(w, 400, "template_id is required")
		return
	}
	created, err := alertstore.GenerateRulesFromTemplate(database.DB, req.TemplateID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "created": created})
}

func validateRule(r *database.AlertRule) error {
	r.SourceType = strings.TrimSpace(r.SourceType)
	if r.SourceType == "" {
		r.SourceType = "metric"
	}
	switch r.SourceType {
	case "metric":
		if r.MetricConfigID == nil || *r.MetricConfigID == 0 {
			return fmt.Errorf("metric_config_id is required when source_type=metric")
		}
	case "custom":
		if strings.TrimSpace(r.Expr) == "" {
			return fmt.Errorf("expr is required when source_type=custom")
		}
	default:
		return fmt.Errorf("invalid source_type: %s", r.SourceType)
	}
	if r.Severity == "" {
		r.Severity = "warning"
	}
	// 校验 datasource_selector JSON
	if strings.TrimSpace(r.DatasourceSelectorJSON) != "" {
		if alerting.DecodeDatasourceSelector(r.DatasourceSelectorJSON) == nil {
			// 允许空 JSON，但格式错误时报错
			var probe map[string]interface{}
			if err := json.Unmarshal([]byte(r.DatasourceSelectorJSON), &probe); err != nil {
				return fmt.Errorf("invalid datasource_selector: %v", err)
			}
		}
	}
	return nil
}

// handleAlertRuleTest 用当前规则对所有目标数据源执行一次评估，返回原始结果（不持久化）
func (a *AdminAPI) handleAlertRuleTest(w http.ResponseWriter, r *http.Request, id uint) {
	rule, err := alertstore.GetRule(database.DB, id)
	if err != nil {
		writeError(w, 404, "rule not found")
		return
	}
	if adminAlerting == nil {
		writeError(w, 503, "alerting subsystem not started")
		return
	}
	results := adminAlerting.TestRule(r.Context(), rule)
	writeJSON(w, map[string]interface{}{
		"rule_id":     id,
		"datasources": results,
	})
}

// ===== AlertSilence CRUD ========================================================

func (a *AdminAPI) handleAlertSilences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		page, pageSize := parsePagination(r)
		includeExpired := r.URL.Query().Get("include_expired") == "true"
		rows, total, err := alertstore.ListSilences(database.DB, includeExpired, page, pageSize)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		// 计算每条静默规则当前匹配的告警实例数
		silenceCounts := computeSilenceMatchedCounts()
		items := make([]map[string]interface{}, len(rows))
		for i, s := range rows {
			m := map[string]interface{}{
				"id":            s.ID,
				"comment":       s.Comment,
				"created_by":    s.CreatedBy,
				"matchers_json": s.MatchersJSON,
				"starts_at":     s.StartsAt,
				"ends_at":       s.EndsAt,
				"enabled":       s.Enabled,
				"created_at":    s.CreatedAt,
				"updated_at":    s.UpdatedAt,
				"matched_count": silenceCounts[s.ID],
			}
			items[i] = m
		}
		writeJSON(w, map[string]interface{}{"items": items, "total": total})
	case "POST":
		var req database.AlertSilence
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Comment) == "" {
			writeError(w, 400, "comment is required")
			return
		}
		if u, ok := r.Context().Value("username").(string); ok && req.CreatedBy == "" {
			req.CreatedBy = u
		}
		if req.StartsAt.IsZero() {
			req.StartsAt = time.Now()
		}
		if req.EndsAt.IsZero() {
			writeError(w, 400, "ends_at is required")
			return
		}
		if req.MatchersJSON == "" {
			req.MatchersJSON = "[]"
		}
		// 校验 matcher
		if ms, err := alerting.DecodeMatchers(req.MatchersJSON); err != nil {
			writeError(w, 400, "invalid matchers: "+err.Error())
			return
		} else if len(ms) == 0 {
			writeError(w, 400, "at least one matcher is required")
			return
		} else {
			for i := range ms {
				if err := ms[i].Validate(); err != nil {
					writeError(w, 400, err.Error())
					return
				}
			}
		}
		if err := alertstore.CreateSilence(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *AdminAPI) handleAlertSilenceByID(w http.ResponseWriter, r *http.Request) {
	id, err := getLastPathID(r.URL.Path)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	switch r.Method {
	case "GET":
		row, err := alertstore.GetSilence(database.DB, id)
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, row)
	case "PUT":
		var req database.AlertSilence
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		req.ID = id
		if err := alertstore.UpdateSilence(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	case "DELETE":
		if err := alertstore.DeleteSilence(database.DB, id); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true})
	default:
		writeError(w, 405, "method not allowed")
	}
}

// ===== AlertInhibit CRUD =========================================================

func (a *AdminAPI) handleAlertInhibits(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		page, pageSize := parsePagination(r)
		rows, total, err := alertstore.ListInhibits(database.DB, page, pageSize)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"items": rows, "total": total})
	case "POST":
		var req database.AlertInhibit
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, 400, "name is required")
			return
		}
		if err := alertstore.CreateInhibit(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *AdminAPI) handleAlertInhibitByID(w http.ResponseWriter, r *http.Request) {
	id, err := getLastPathID(r.URL.Path)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	switch r.Method {
	case "GET":
		row, err := alertstore.GetInhibit(database.DB, id)
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, row)
	case "PUT":
		var req database.AlertInhibit
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		req.ID = id
		if err := alertstore.UpdateInhibit(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	case "DELETE":
		if err := alertstore.DeleteInhibit(database.DB, id); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true})
	default:
		writeError(w, 405, "method not allowed")
	}
}

// ===== AlertRoute CRUD ===========================================================

func (a *AdminAPI) handleAlertRoutes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		rows, err := alertstore.ListRoutes(database.DB)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"items": rows, "total": len(rows)})
	case "POST":
		var req database.AlertRoute
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, 400, "name is required")
			return
		}
		if err := alertstore.CreateRoute(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	default:
		writeError(w, 405, "method not allowed")
	}
}

// computeSilenceMatchedCounts 查询当前活跃告警中被每条静默规则匹配的实例数
func computeSilenceMatchedCounts() map[uint]int {
	var instances []struct {
		SilencedByJSON string
	}
	database.DB.Model(&database.AlertInstance{}).
		Where("state IN ?", []string{"pending", "firing"}).
		Where("silenced_by_json NOT IN ?", []string{"", "[]", "null"}).
		Select("silenced_by_json").
		Find(&instances)
	counts := make(map[uint]int)
	for _, inst := range instances {
		ids := alerting.DecodeUintSlice(inst.SilencedByJSON)
		for _, id := range ids {
			counts[id]++
		}
	}
	return counts
}

func parsePagination(r *http.Request) (page, pageSize int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	return
}

func (a *AdminAPI) handleAlertRuleBatchDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []uint `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, 400, "ids is required")
		return
	}
	if err := alertstore.BatchDeleteRules(database.DB, req.IDs); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "count": len(req.IDs)})
}

func (a *AdminAPI) handleAlertRuleBatchEdit(w http.ResponseWriter, r *http.Request) {
	var req alertstore.BatchEditRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, 400, "ids is required")
		return
	}
	if err := alertstore.BatchUpdateRules(database.DB, &req); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"success": true, "count": len(req.IDs)})
}

func (a *AdminAPI) handleAlertRouteByID(w http.ResponseWriter, r *http.Request) {
	id, err := getLastPathID(r.URL.Path)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	switch r.Method {
	case "GET":
		row, err := alertstore.GetRoute(database.DB, id)
		if err != nil {
			writeError(w, 404, "not found")
			return
		}
		writeJSON(w, row)
	case "PUT":
		var req database.AlertRoute
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid body: "+err.Error())
			return
		}
		req.ID = id
		if err := alertstore.UpdateRoute(database.DB, &req); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, req)
	case "DELETE":
		if err := alertstore.DeleteRoute(database.DB, id); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"deleted": true})
	default:
		writeError(w, 405, "method not allowed")
	}
}
