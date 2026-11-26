package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// =============================================================================
// 灭火图补齐能力 · 按 Flashcat 真实逻辑
//   1) 时间轴回放：健康分快照每分钟/每次检查落库，按时间窗回放全局状态
//   2) 下钻规则：把指标/日志/链路/大盘/事件的排障路径挂到观测对象
//   3) 告警规则：飘红触发告警（在 executeChecks -> fireAlertRules 中执行）
//   4) 模板建卡：按数据源类型匹配模板，评估匹配度并一键生成检查项
// =============================================================================

// ---------- 对外结构 ----------

type FCHealthPoint struct {
	T        int64   `json:"t"`
	Score    float64 `json:"score"`
	Status   string  `json:"status"`
	Critical int     `json:"critical"`
	Warning  int     `json:"warning"`
	Alerts   int     `json:"alerts"`
}

type FCDrillRuleView struct {
	ID         uint   `json:"id"`
	NodeID     uint   `json:"node_id"`
	Name       string `json:"name"`
	TargetType string `json:"target_type"`
	TargetURL  string `json:"target_url"`
	Query      string `json:"query"`
	Enabled    bool   `json:"enabled"`
	SortOrder  int    `json:"sort_order"`
}

type FCAlertRuleView struct {
	ID           uint   `json:"id"`
	NodeID       uint   `json:"node_id"`
	Name         string `json:"name"`
	Condition    string `json:"condition"`
	ScoreBelow   float64 `json:"score_below"`
	Enabled      bool   `json:"enabled"`
	NotifyTarget string `json:"notify_target"`
	LastFiredAt  *time.Time `json:"last_fired_at,omitempty"`
}

type FCTemplateView struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	MatchKeywords  string `json:"match_keywords"`
	MetricPrefixes string `json:"metric_prefixes"`
	Builtin        bool   `json:"builtin"`
	MatchScore     int    `json:"match_score"` // 与当前数据源的匹配度 0-100
}

// ---------- 种子：内置观测规则模板（AI 生成规则的基座） ----------

var defaultFiremapTemplates = []database.FiremapTemplate{
	{Name: "MySQL_Local_Collect_Categraf", Category: "mysql", MatchKeywords: "mysql,数据库,db",
		MetricPrefixes: "mysql_up,mysql_slow_queries,mysql_connections,mysql_qps",
		CheckConfigJSON: `[{"key":"mysql_up","name":"MySQL存活","check_type":"promql","promql":"mysql_up{instance=\"$instance\"}","threshold":1,"compare_op":"lt"},{"key":"mysql_slow","name":"慢查询","check_type":"promql","promql":"rate(mysql_slow_queries_total{instance=\"$instance\"}[5m])","threshold":10,"compare_op":"gt"}]`,
		Builtin: true},
	{Name: "Redis_Local_Collect_Categraf", Category: "redis", MatchKeywords: "redis,缓存,cache",
		MetricPrefixes: "redis_up,redis_connected_clients,redis_keyspace_hits,redis_memory_used",
		CheckConfigJSON: `[{"key":"redis_up","name":"Redis存活","check_type":"promql","promql":"redis_up{instance=\"$instance\"}","threshold":1,"compare_op":"lt"},{"key":"redis_conn","name":"连接数","check_type":"promql","promql":"redis_connected_clients{instance=\"$instance\"}","threshold":1000,"compare_op":"gt"}]`,
		Builtin: true},
	{Name: "Kafka_Local_Collect_Categraf", Category: "kafka", MatchKeywords: "kafka,队列,mq",
		MetricPrefixes: "kafka_up,kafka_consumer_lag,kafka_messages_in",
		CheckConfigJSON: `[{"key":"kafka_up","name":"Kafka存活","check_type":"promql","promql":"kafka_up{instance=\"$instance\"}","threshold":1,"compare_op":"lt"}]`,
		Builtin: true},
	{Name: "APM_Microservice_Categraf", Category: "apm", MatchKeywords: "apm,微服务,service,java,jvm",
		MetricPrefixes: "http_server_request_duration_seconds,http_server_requests_total,jvm_heap_used",
		CheckConfigJSON: `[{"key":"api_success","name":"接口成功率","check_type":"promql","promql":"sum(rate(http_server_requests_total{instance=\"$instance\",status!~\"5..\"}[5m]))/sum(rate(http_server_requests_total{instance=\"$instance\"}[5m]))","threshold":0.99,"compare_op":"lt"},{"key":"api_latency","name":"P99延迟","check_type":"promql","promql":"histogram_quantile(0.99,sum(rate(http_server_request_duration_seconds_bucket{instance=\"$instance\"}[5m])) by (le))","threshold":1,"compare_op":"gt"}]`,
		Builtin: true},
	{Name: "Host_Local_Collect_Categraf", Category: "host", MatchKeywords: "host,主机,node,服务器,linux",
		MetricPrefixes: "cpu_usage,mem_used_percent,disk_used_percent,node_load1",
		CheckConfigJSON: `[{"key":"host_cpu","name":"CPU使用率","check_type":"promql","promql":"100-cpu_usage{instance=\"$instance\"}","threshold":90,"compare_op":"gt"}]`,
		Builtin: true},
}

func ensureFiremapTemplates() {
	if database.DB == nil {
		return
	}
	var cnt int64
	database.DB.Model(&database.FiremapTemplate{}).Count(&cnt)
	if cnt > 0 {
		return
	}
	for _, t := range defaultFiremapTemplates {
		database.DB.Create(&t)
	}
}

// ---------- 纯函数 ----------

// matchScore 计算模板与数据源名称的匹配度（0-100）。
// 命中一个关键词按权重加分，命中越多分越高；模板 category 直接命中满分。
func matchScore(templateName, category, keywords, dsName string) int {
	if dsName == "" {
		return 0
	}
	s := strings.ToLower(dsName)
	if strings.Contains(s, strings.ToLower(category)) || strings.Contains(strings.ToLower(templateName), s) {
		return 100
	}
	score := 0
	for _, kw := range strings.Split(keywords, ",") {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw != "" && strings.Contains(s, kw) {
			score += 40
		}
	}
	if score > 100 {
		score = 100
	}
	return score
}

// ---------- handlers ----------

// handleFiremapTimeline GET ?node_id=&minutes= 健康分历史时间轴。
// node_id=0 返回全局平均健康分（所有节点每分钟均值）。
func (a *AdminAPI) handleFiremapTimeline(w http.ResponseWriter, r *http.Request) {
	points := []FCHealthPoint{}
	if database.DB == nil {
		writeJSON(w, map[string]any{"points": points})
		return
	}
	nodeID := parseUintParam(r.URL.Query().Get("node_id"))
	minutes := 60
	if v := parseUintParam(r.URL.Query().Get("minutes")); v > 0 {
		minutes = int(v)
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute)

	type row struct {
		T        int64
		Score    float64
		Status   string
		Critical int
		Warning  int
		Alerts   int
	}
	buckets := map[int64][]row{}
	if nodeID > 0 {
		var snaps []database.FiremapHealthSnapshot
		database.DB.Where("node_id = ? AND created_at >= ?", nodeID, since).Order("created_at asc").Find(&snaps)
		for _, s := range snaps {
			k := s.CreatedAt.Unix()
			buckets[k] = append(buckets[k], row{k, s.HealthScore, s.Status, s.Critical, s.Warning, s.Alerts})
		}
	} else {
		var snaps []database.FiremapHealthSnapshot
		database.DB.Select("created_at,health_score,status,critical,warning,alerts").
			Where("created_at >= ?", since).Order("created_at asc").Find(&snaps)
		for _, s := range snaps {
			k := s.CreatedAt.Unix()
			buckets[k] = append(buckets[k], row{k, s.HealthScore, s.Status, s.Critical, s.Warning, s.Alerts})
		}
	}
	for k, rows := range buckets {
		sum := 0.0
		status := "normal"
		crit, warn, alerts := 0, 0, 0
		for _, rw := range rows {
			sum += rw.Score
			crit += rw.Critical
			warn += rw.Warning
			alerts += rw.Alerts
			if rw.Status == "critical" {
				status = "critical"
			} else if rw.Status == "warning" && status != "critical" {
				status = "warning"
			}
		}
		points = append(points, FCHealthPoint{T: k, Score: round1(sum / float64(len(rows))), Status: status, Critical: crit, Warning: warn, Alerts: alerts})
	}
	sortPointsByTime(points)
	writeJSON(w, map[string]any{"points": points})
}

func sortPointsByTime(points []FCHealthPoint) {
	for i := 1; i < len(points); i++ {
		for j := i; j > 0 && points[j].T < points[j-1].T; j-- {
			points[j], points[j-1] = points[j-1], points[j]
		}
	}
}

// handleFiremapRule GET/POST/DELETE 下钻规则与告警规则（?kind=drill|alert）。
func (a *AdminAPI) handleFiremapRule(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeError(w, 503, "数据库未就绪")
		return
	}
	kind := r.URL.Query().Get("kind")
	nodeID := parseUintParam(r.URL.Query().Get("node_id"))
	if r.Method == http.MethodGet {
		if kind == "alert" {
			var rules []database.FiremapAlertRule
			q := database.DB.Order("id asc")
			if nodeID > 0 {
				q = q.Where("node_id = ?", nodeID)
			}
			q.Find(&rules)
			items := make([]FCAlertRuleView, 0, len(rules))
			for _, x := range rules {
				items = append(items, FCAlertRuleView{ID: x.ID, NodeID: x.NodeID, Name: x.Name, Condition: x.Condition, ScoreBelow: x.ScoreBelow, Enabled: x.Enabled, NotifyTarget: x.NotifyTarget, LastFiredAt: x.LastFiredAt})
			}
			writeJSON(w, map[string]any{"items": items})
			return
		}
		var rules []database.FiremapDrillRule
		q := database.DB.Order("sort_order asc, id asc")
		if nodeID > 0 {
			q = q.Where("node_id = ?", nodeID)
		}
		q.Find(&rules)
		items := make([]FCDrillRuleView, 0, len(rules))
		for _, x := range rules {
			items = append(items, FCDrillRuleView{ID: x.ID, NodeID: x.NodeID, Name: x.Name, TargetType: x.TargetType, TargetURL: x.TargetURL, Query: x.Query, Enabled: x.Enabled, SortOrder: x.SortOrder})
		}
		writeJSON(w, map[string]any{"items": items})
		return
	}
	if r.Method == http.MethodDelete {
		id := parseUintParam(r.URL.Query().Get("id"))
		if id == 0 {
			writeError(w, 400, "缺少 id")
			return
		}
		if kind == "alert" {
			database.DB.Delete(&database.FiremapAlertRule{}, id)
		} else {
			database.DB.Delete(&database.FiremapDrillRule{}, id)
		}
		writeJSON(w, map[string]any{"success": true})
		return
	}
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, 400, "参数错误")
		return
	}
	b, _ := json.Marshal(raw)
	var id uint
	if v, ok := raw["id"]; ok {
		if n, ok := v.(float64); ok { id = uint(n) }
	}
	if kind == "alert" {
		var x database.FiremapAlertRule
		_ = json.Unmarshal(b, &x)
		if id > 0 { x.ID = id; database.DB.Model(&x).Updates(&x) } else { database.DB.Create(&x) }
		writeJSON(w, map[string]any{"success": true, "id": x.ID})
		return
	}
	var x database.FiremapDrillRule
	_ = json.Unmarshal(b, &x)
	if id > 0 { x.ID = id; database.DB.Model(&x).Updates(&x) } else { database.DB.Create(&x) }
	writeJSON(w, map[string]any{"success": true, "id": x.ID})
}

// handleFiremapTemplates 列出观测规则模板，并按 datasource_id 评估匹配度。
func (a *AdminAPI) handleFiremapTemplates(w http.ResponseWriter, r *http.Request) {
	ensureFiremapTemplates()
	items := []FCTemplateView{}
	if database.DB == nil {
		writeJSON(w, map[string]any{"items": items})
		return
	}
	var ts []database.FiremapTemplate
	database.DB.Order("id asc").Find(&ts)
	dsName := ""
	if dsID := parseUintParam(r.URL.Query().Get("datasource_id")); dsID > 0 {
		var ds database.DataSource
		if database.DB.First(&ds, dsID).Error == nil {
			dsName = ds.Name
		}
	}
	for _, t := range ts {
		items = append(items, FCTemplateView{
			ID: t.ID, Name: t.Name, Category: t.Category, MatchKeywords: t.MatchKeywords,
			MetricPrefixes: t.MetricPrefixes, Builtin: t.Builtin,
			MatchScore: matchScore(t.Name, t.Category, t.MatchKeywords, dsName),
		})
	}
	writeJSON(w, map[string]any{"items": items})
}

// handleFiremapTemplateApply 应用模板到数据源：解析 CheckConfigJSON 生成检查项配置。
func (a *AdminAPI) handleFiremapTemplateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "仅支持 POST")
		return
	}
	if database.DB == nil {
		writeError(w, 503, "数据库未就绪")
		return
	}
	var req struct {
		TemplateID   uint `json:"template_id"`
		DatasourceID uint `json:"datasource_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TemplateID == 0 {
		writeError(w, 400, "参数错误")
		return
	}
	var t database.FiremapTemplate
	if database.DB.First(&t, req.TemplateID).Error != nil {
		writeError(w, 404, "模板不存在")
		return
	}
	var cfgs []struct {
		Key       string  `json:"key"`
		Name      string  `json:"name"`
		CheckType string  `json:"check_type"`
		PromQL    string  `json:"promql"`
		Threshold float64 `json:"threshold"`
		CompareOp string  `json:"compare_op"`
	}
	if err := json.Unmarshal([]byte(t.CheckConfigJSON), &cfgs); err != nil {
		writeError(w, 500, "模板检查项解析失败: "+err.Error())
		return
	}
	created := 0
	for _, c := range cfgs {
		key := c.Key
		if req.DatasourceID > 0 {
			key = key + "_ds" + strconv.FormatUint(uint64(req.DatasourceID), 10)
		}
		var exists int64
		database.DB.Model(&database.FlashAICheckConfig{}).Where("key = ?", key).Count(&exists)
		if exists > 0 {
			continue
		}
		database.DB.Create(&database.FlashAICheckConfig{
			Key: key, Name: c.Name, Enabled: true, SortOrder: created + 10,
			CheckType: c.CheckType, PromQL: c.PromQL, Threshold: c.Threshold,
			CompareOp: c.CompareOp, Remark: "由模板 " + t.Name + " 生成",
		})
		created++
	}
	writeJSON(w, map[string]any{"created": created, "message": "已从模板生成 " + strconv.Itoa(created) + " 条检查项"})
}

// registerFiremapExtraHandlers 注册补齐能力路由。
func (a *AdminAPI) registerFiremapExtraHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/promai/firemap/timeline", a.logRequest(a.authMiddleware(a.handleFiremapTimeline)))
	mux.HandleFunc("/api/promai/firemap/rule", a.logRequest(a.authMiddleware(a.handleFiremapRule)))
	mux.HandleFunc("/api/promai/firemap/templates", a.logRequest(a.authMiddleware(a.handleFiremapTemplates)))
	mux.HandleFunc("/api/promai/firemap/template-apply", a.logRequest(a.authMiddleware(a.handleFiremapTemplateApply)))
}
