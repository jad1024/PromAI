package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// ===== 告警降噪聚合（分析级） =====================================================
//
// 模型参考 FlashDuty / Nightingale：
//   1) 原始事件 AlertHistory  →  告警 Alert
//      按 fingerprint（rule_id+datasource_id+labels 的稳定哈希）分组；
//      同一 fingerprint 内 firing→resolved→firing 视为两条独立告警。
//   2) 告警 Alert  →  故障 Incident
//      按 (alertname, resource 标签值) 聚合；时间窗内的告警合入同一故障；
//      故障内告警数超过风暴阈值时打风暴标记。
//
// 不触碰告警评估与通知链路——通知侧分组/去重/抑制是 Alertmanager 的职责。
//
// 配置通过 AppSetting 持久化：
//   alert_denoise_window_minutes  默认 10；0 表示不切窗
//   alert_denoise_storm_threshold  默认 10；0 表示不预警
//   alert_denoise_resource_labels  CSV，默认 "resource"

const (
	defaultDenoiseWindowMinutes  = 10
	defaultDenoiseStormThreshold = 10
	defaultDenoiseResourceLabels = "resource"
)

// incidentKey 故障的聚合主键：alertname + 选中的 resource 标签值组合
type incidentKey struct {
	Alertname string
	Resource  string
}

// alertInstance 一次完整告警实例（去重后）。
// 由 AlertHistory 中同一 fingerprint 的连续 firing 段（含首尾 resolved）合并而成。
type alertInstance struct {
	Fingerprint    string
	RuleID         uint
	RuleName       string
	DatasourceID   uint
	DatasourceName string
	Severity       string
	State          string // ongoing | resolved
	FirstFiredAt   time.Time
	LastEventAt    time.Time
	PeakValue      float64
	Threshold      float64
	Labels         map[string]string
}

// alertInIncident 故障下钻用的告警明细
type alertInIncident struct {
	Time           time.Time         `json:"time"`
	State          string            `json:"state"`
	Severity       string            `json:"severity"`
	Value          float64           `json:"value"`
	Threshold      float64           `json:"threshold"`
	DatasourceID   uint              `json:"datasource_id"`
	DatasourceName string            `json:"datasource_name"`
	Labels         map[string]string `json:"labels"`
	Duration       string            `json:"duration"`
}

// incidentListItem 列表项（不含 alerts 数组，控制响应体积）
type incidentListItem struct {
	Key          string    `json:"key"`
	Alertname    string    `json:"alertname"`
	Resource     string    `json:"resource"`
	Severity     string    `json:"severity"`
	State        string    `json:"state"`
	AlertCount   int       `json:"alert_count"`
	FirstFiredAt time.Time `json:"first_fired_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	Storm        bool      `json:"storm"`
	Datasources  []string  `json:"datasources"`
}

// incidentDetailFull 内部使用的完整故障（带 alerts）
type incidentDetailFull struct {
	Key          string
	Alertname    string
	Resource     string
	Severity     string
	State        string
	AlertCount   int
	FirstFiredAt time.Time
	LastEventAt  time.Time
	Storm        bool
	Datasources  []string
	Alerts       []alertInIncident
}

// incidentListResp 列表响应
type incidentListResp struct {
	Incidents      []incidentListItem `json:"incidents"`
	TotalRaw       int                `json:"total_raw"`       // 原始 AlertHistory 行数
	TotalAlerts    int                `json:"total_alerts"`    // 去重后告警数
	TotalIncidents int                `json:"total_incidents"` // 聚合后故障数
	Compression    float64            `json:"compression"`     // 降噪比：(raw-incidents)/raw
	WindowMinutes  int                `json:"window_minutes"`
	StormThreshold int                `json:"storm_threshold"`
	ResourceLabels []string           `json:"resource_labels"`
}

// denoiseConfig 降噪配置
type denoiseConfig struct {
	WindowMinutes  int      `json:"window_minutes"`
	StormThreshold int      `json:"storm_threshold"`
	ResourceLabels []string `json:"resource_labels"`
}

// ===== 配置读写 =====

func loadDenoiseConfig() denoiseConfig {
	cfg := denoiseConfig{
		WindowMinutes:  defaultDenoiseWindowMinutes,
		StormThreshold: defaultDenoiseStormThreshold,
		ResourceLabels: []string{defaultDenoiseResourceLabels},
	}
	rows := []database.AppSetting{}
	if err := database.DB.Where("key IN ?", []string{
		"alert_denoise_window_minutes",
		"alert_denoise_storm_threshold",
		"alert_denoise_resource_labels",
	}).Find(&rows).Error; err != nil {
		return cfg
	}
	for _, r := range rows {
		switch r.Key {
		case "alert_denoise_window_minutes":
			if v, err := strconv.Atoi(r.Value); err == nil && v >= 0 && v <= 1440 {
				cfg.WindowMinutes = v
			}
		case "alert_denoise_storm_threshold":
			if v, err := strconv.Atoi(r.Value); err == nil && v >= 0 && v <= 1000 {
				cfg.StormThreshold = v
			}
		case "alert_denoise_resource_labels":
			parts := splitCSV(r.Value)
			if len(parts) > 0 {
				cfg.ResourceLabels = parts
			}
		}
	}
	return cfg
}

func saveDenoiseSetting(key, value string) error {
	var s database.AppSetting
	if err := database.DB.Where("key = ?", key).First(&s).Error; err != nil {
		return database.DB.Create(&database.AppSetting{Key: key, Value: value}).Error
	}
	return database.DB.Model(&s).Update("value", value).Error
}

// resolveDenoiseParams 用 query 参数覆盖默认配置（-1 表示沿用默认）
func resolveDenoiseParams(query denoiseConfig) denoiseConfig {
	def := loadDenoiseConfig()
	if query.WindowMinutes >= 0 {
		def.WindowMinutes = query.WindowMinutes
	}
	if query.StormThreshold >= 0 {
		def.StormThreshold = query.StormThreshold
	}
	if len(query.ResourceLabels) > 0 {
		def.ResourceLabels = query.ResourceLabels
	}
	if def.WindowMinutes > 1440 {
		def.WindowMinutes = 1440
	}
	if def.StormThreshold > 1000 {
		def.StormThreshold = 1000
	}
	return def
}

// ===== 聚合主流程 =====

// parseLabels 解析 labels_json；失败返回空 map
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// extractResource 从 labels 中按选中的键提取并拼接 resource 值；
// 键按字典序排序保证稳定性；任一键缺失则该位空。
func extractResource(labels map[string]string, keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	ks := append([]string(nil), keys...)
	sort.Strings(ks)
	parts := make([]string, 0, len(ks))
	missing := 0
	for _, k := range ks {
		if v, ok := labels[k]; ok && v != "" {
			parts = append(parts, v)
		} else {
			parts = append(parts, "-")
			missing++
		}
	}
	if missing == len(ks) {
		return "" // 一个 resource 标签都没取到
	}
	return strings.Join(parts, "|")
}

// incidentHashKey 故障的稳定 key（用于前端下钻）
func incidentHashKey(alertname, resource string, firstFiredAt time.Time) string {
	h := sha256.New()
	h.Write([]byte(alertname))
	h.Write([]byte{0})
	h.Write([]byte(resource))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(firstFiredAt.UnixNano(), 10)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// splitCSV 简单的 CSV 切分
func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinCSV(parts []string) string {
	return strings.Join(parts, ",")
}

// formatDuration 友好时长显示
func formatDuration(ms int64) string {
	if ms <= 0 {
		return "<1m"
	}
	m := ms / 60000
	if m < 60 {
		return strconv.FormatInt(m, 10) + "m"
	}
	h := m / 60
	if h < 24 {
		return strconv.FormatInt(h, 10) + "h" + strconv.FormatInt(m%60, 10) + "m"
	}
	d := h / 24
	return strconv.FormatInt(d, 10) + "d" + strconv.FormatInt(h%24, 10) + "h"
}

// dedupToAlerts 把 AlertHistory 序列按 fingerprint 折叠为独立告警。
// firing→resolved→firing（同 fingerprint）= 两条告警。
// silenced/inhibited/pending 不开启新告警。
func dedupToAlerts(rows []database.AlertHistory) []alertInstance {
	if len(rows) == 0 {
		return nil
	}
	open := map[string]*alertInstance{}
	alerts := []*alertInstance{}
	for _, r := range rows {
		ts := r.OccurredAt
		if ts.IsZero() {
			ts = r.CreatedAt
		}
		cur, exists := open[r.Fingerprint]
		switch r.EventType {
		case "firing", "pending":
			if !exists {
				cur = &alertInstance{
					Fingerprint:    r.Fingerprint,
					RuleID:         r.RuleID,
					RuleName:       r.RuleName,
					DatasourceID:   r.DatasourceID,
					DatasourceName: r.DatasourceName,
					Severity:       r.Severity,
					State:          "ongoing",
					FirstFiredAt:   ts,
					LastEventAt:    ts,
					PeakValue:      r.Value,
					Threshold:      r.Threshold,
					Labels:         parseLabels(r.LabelsJSON),
				}
				open[r.Fingerprint] = cur
				alerts = append(alerts, cur)
			} else {
				cur.LastEventAt = ts
				if r.Value > cur.PeakValue {
					cur.PeakValue = r.Value
				}
				if r.Threshold != 0 {
					cur.Threshold = r.Threshold
				}
				if r.Severity != "" && sevRank(r.Severity) > sevRank(cur.Severity) {
					cur.Severity = r.Severity
				}
				cur.State = "ongoing"
			}
		case "resolved":
			if exists {
				cur.LastEventAt = ts
				cur.State = "resolved"
				if r.Severity != "" && sevRank(r.Severity) > sevRank(cur.Severity) {
					cur.Severity = r.Severity
				}
				delete(open, r.Fingerprint)
			}
		default:
			// silenced/inhibited/notified：不改变告警生命周期
		}
	}
	out := make([]alertInstance, 0, len(alerts))
	for _, p := range alerts {
		out = append(out, *p)
	}
	return out
}

// aggregateIncidents 把告警按 (alertname, resource) 在时间窗内聚合为故障。
func aggregateIncidents(alerts []alertInstance, cfg denoiseConfig) []incidentDetailFull {
	incidentsByKey := map[incidentKey][]*incidentDetailFull{}
	for _, a := range alerts {
		res := extractResource(a.Labels, cfg.ResourceLabels)
		k := incidentKey{Alertname: a.RuleName, Resource: res}
		list := incidentsByKey[k]
		var inc *incidentDetailFull
		if n := len(list); n > 0 {
			last := list[n-1]
			if cfg.WindowMinutes <= 0 || a.FirstFiredAt.Sub(last.LastEventAt) <= time.Duration(cfg.WindowMinutes)*time.Minute {
				inc = last
			}
		}
		if inc == nil {
			inc = &incidentDetailFull{
				Alertname:    a.RuleName,
				Resource:     res,
				Severity:     a.Severity,
				State:        a.State,
				FirstFiredAt: a.FirstFiredAt,
				LastEventAt:  a.LastEventAt,
			}
			incidentsByKey[k] = append(list, inc)
		} else {
			if a.FirstFiredAt.Before(inc.FirstFiredAt) {
				inc.FirstFiredAt = a.FirstFiredAt
			}
			if a.LastEventAt.After(inc.LastEventAt) {
				inc.LastEventAt = a.LastEventAt
			}
			if a.State == "ongoing" {
				inc.State = "ongoing"
			}
			if sevRank(a.Severity) > sevRank(inc.Severity) {
				inc.Severity = a.Severity
			}
		}
		inc.AlertCount++
		inc.Alerts = append(inc.Alerts, alertInIncident{
			Time:           a.FirstFiredAt,
			State:          a.State,
			Severity:       a.Severity,
			Value:          a.PeakValue,
			Threshold:      a.Threshold,
			DatasourceID:   a.DatasourceID,
			DatasourceName: a.DatasourceName,
			Labels:         a.Labels,
			Duration:       formatDuration(a.LastEventAt.Sub(a.FirstFiredAt).Milliseconds()),
		})
		if !containsStr(inc.Datasources, a.DatasourceName) {
			inc.Datasources = append(inc.Datasources, a.DatasourceName)
		}
	}
	var all []*incidentDetailFull
	for _, list := range incidentsByKey {
		for _, inc := range list {
			inc.Key = incidentHashKey(inc.Alertname, inc.Resource, inc.FirstFiredAt)
			if cfg.StormThreshold > 0 && inc.AlertCount > cfg.StormThreshold {
				inc.Storm = true
			}
			all = append(all, inc)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].LastEventAt.After(all[j].LastEventAt)
	})
	out := make([]incidentDetailFull, 0, len(all))
	for _, p := range all {
		out = append(out, *p)
	}
	return out
}

func toListItem(d incidentDetailFull) incidentListItem {
	return incidentListItem{
		Key: d.Key, Alertname: d.Alertname, Resource: d.Resource, Severity: d.Severity,
		State: d.State, AlertCount: d.AlertCount,
		FirstFiredAt: d.FirstFiredAt, LastEventAt: d.LastEventAt,
		Storm: d.Storm, Datasources: d.Datasources,
	}
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

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// ===== 路由处理 =====

// handleAlertIncidents GET /api/promai/alert/incidents
func (a *AdminAPI) handleAlertIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	p := r.URL.Query()
	hours, _ := strconv.Atoi(p.Get("hours"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	limit, _ := strconv.Atoi(p.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	dsID, _ := strconv.Atoi(p.Get("datasource_id"))
	wm, _ := strconv.Atoi(p.Get("window_minutes"))
	st, _ := strconv.Atoi(p.Get("storm_threshold"))
	rl := splitCSV(p.Get("resource_labels"))
	cfg := resolveDenoiseParams(denoiseConfig{
		WindowMinutes:  wm,
		StormThreshold: st,
		ResourceLabels: rl,
	})

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	q := database.DB.Model(&database.AlertHistory{}).
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ?",
			[]string{"firing", "resolved", "pending"}, since)
	if dsID > 0 {
		q = q.Where("datasource_id = ?", dsID)
	}
	if sev := strings.TrimSpace(p.Get("severity")); sev != "" {
		q = q.Where("severity = ?", sev)
	}
	if an := strings.TrimSpace(p.Get("alertname")); an != "" {
		q = q.Where("rule_name = ?", an)
	}
	if res := strings.TrimSpace(p.Get("resource")); res != "" {
		q = q.Where("labels_json LIKE ?", "%\""+res+"\"%")
	}
	var rows []database.AlertHistory
	if err := q.Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	totalRaw := len(rows)
	alerts := dedupToAlerts(rows)
	totalAlerts := len(alerts)
	all := aggregateIncidents(alerts, cfg)

	items := make([]incidentListItem, 0, len(all))
	for _, inc := range all {
		if an := strings.TrimSpace(p.Get("alertname")); an != "" && inc.Alertname != an {
			continue
		}
		if res := strings.TrimSpace(p.Get("resource")); res != "" && !strings.Contains(inc.Resource, res) {
			continue
		}
		items = append(items, toListItem(inc))
		if len(items) >= limit {
			break
		}
	}

	compression := 0.0
	if totalRaw > 0 {
		compression = float64(totalRaw-len(items)) / float64(totalRaw) * 100
		if compression < 0 {
			compression = 0
		}
	}
	writeJSON(w, incidentListResp{
		Incidents:      items,
		TotalRaw:       totalRaw,
		TotalAlerts:    totalAlerts,
		TotalIncidents: len(items),
		Compression:    round2(compression),
		WindowMinutes:  cfg.WindowMinutes,
		StormThreshold: cfg.StormThreshold,
		ResourceLabels: cfg.ResourceLabels,
	})
}

// handleAlertIncidentDetail GET /api/promai/alert/incidents/detail?key=...
func (a *AdminAPI) handleAlertIncidentDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	p := r.URL.Query()
	targetKey := strings.TrimSpace(p.Get("key"))
	if targetKey == "" {
		writeError(w, 400, "缺少 key 参数")
		return
	}
	hours, _ := strconv.Atoi(p.Get("hours"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	wm, _ := strconv.Atoi(p.Get("window_minutes"))
	st, _ := strconv.Atoi(p.Get("storm_threshold"))
	rl := splitCSV(p.Get("resource_labels"))
	cfg := resolveDenoiseParams(denoiseConfig{
		WindowMinutes:  wm,
		StormThreshold: st,
		ResourceLabels: rl,
	})
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var rows []database.AlertHistory
	if err := database.DB.Model(&database.AlertHistory{}).
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ?",
			[]string{"firing", "resolved", "pending"}, since).
		Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	alerts := dedupToAlerts(rows)
	all := aggregateIncidents(alerts, cfg)
	for _, inc := range all {
		if inc.Key == targetKey {
			writeJSON(w, map[string]interface{}{
				"incident":       inc,
				"window_minutes": cfg.WindowMinutes,
			})
			return
		}
	}
	writeError(w, 404, "故障不存在或已过期")
}

// handleAlertNoiseTop GET /api/promai/alert/noise-top
// TOP 噪音：按 (alertname, resource) 聚合的告警数排序
func (a *AdminAPI) handleAlertNoiseTop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	p := r.URL.Query()
	hours, _ := strconv.Atoi(p.Get("hours"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	limit, _ := strconv.Atoi(p.Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	wm, _ := strconv.Atoi(p.Get("window_minutes"))
	st, _ := strconv.Atoi(p.Get("storm_threshold"))
	rl := splitCSV(p.Get("resource_labels"))
	cfg := resolveDenoiseParams(denoiseConfig{
		WindowMinutes:  wm,
		StormThreshold: st,
		ResourceLabels: rl,
	})

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var rows []database.AlertHistory
	if err := database.DB.Model(&database.AlertHistory{}).
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ?",
			[]string{"firing", "resolved", "pending"}, since).
		Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	alerts := dedupToAlerts(rows)
	all := aggregateIncidents(alerts, cfg)

	type item struct {
		Alertname   string   `json:"alertname"`
		Resource    string   `json:"resource"`
		AlertCount  int      `json:"alert_count"`
		Severity    string   `json:"severity"`
		State       string   `json:"state"`
		Storm       bool     `json:"storm"`
		Datasources []string `json:"datasources"`
	}
	items := make([]item, 0, len(all))
	for _, inc := range all {
		items = append(items, item{
			Alertname: inc.Alertname, Resource: inc.Resource, AlertCount: inc.AlertCount,
			Severity: inc.Severity, State: inc.State, Storm: inc.Storm, Datasources: inc.Datasources,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AlertCount != items[j].AlertCount {
			return items[i].AlertCount > items[j].AlertCount
		}
		return items[i].Alertname < items[j].Alertname
	})
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, map[string]interface{}{
		"items":           items,
		"window_hours":    hours,
		"window_minutes":  cfg.WindowMinutes,
		"storm_threshold": cfg.StormThreshold,
		"resource_labels": cfg.ResourceLabels,
	})
}

// handleAlertDenoiseConfig GET/PUT /api/promai/alert/denoise-config
func (a *AdminAPI) handleAlertDenoiseConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, loadDenoiseConfig())
	case "PUT", "POST":
		var in struct {
			WindowMinutes  int      `json:"window_minutes"`
			StormThreshold int      `json:"storm_threshold"`
			ResourceLabels []string `json:"resource_labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, 400, "请求体不合法: "+err.Error())
			return
		}
		if err := saveDenoiseSetting("alert_denoise_window_minutes", strconv.Itoa(in.WindowMinutes)); err != nil {
			writeError(w, 500, "保存失败: "+err.Error())
			return
		}
		if err := saveDenoiseSetting("alert_denoise_storm_threshold", strconv.Itoa(in.StormThreshold)); err != nil {
			writeError(w, 500, "保存失败: "+err.Error())
			return
		}
		rl := joinCSV(in.ResourceLabels)
		if rl == "" {
			rl = defaultDenoiseResourceLabels
		}
		if err := saveDenoiseSetting("alert_denoise_resource_labels", rl); err != nil {
			writeError(w, 500, "保存失败: "+err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		writeError(w, 405, "method not allowed")
	}
}
