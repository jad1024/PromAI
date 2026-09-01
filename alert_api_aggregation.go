package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// ===== 告警降噪聚合（分析级） =====================================================
//
// 模型（参考 FlashDuty / Nightingale 思路，按用户实际诉求简化）：
//   1) 原始事件 AlertHistory  →  告警 Alert
//      按 fingerprint（rule_id+datasource_id+labels 的稳定哈希）分组；
//      同一 fingerprint 内 firing→resolved→firing 视为两条独立告警。
//   2) 告警 Alert  →  故障 Incident
//      以 alertname 为主键聚合；同一 alertname 在时间窗内的所有实例/集群告警
//      都归入同一故障；超过窗口则开新故障。故障的元信息（首次触发、最近告警、
//      严重度、风暴标记）按告警序列聚合计算。
//      每条告警自带其 instance 标签值（用于下钻时定位「具体实例」）。
//
// 重要约束：不触碰告警评估与通知链路——通知侧分组/去重/抑制是 Alertmanager 的职责。
//
// 配置通过 AppSetting 持久化：
//   alert_denoise_window_minutes  默认 10；0 表示不切窗
//   alert_denoise_storm_threshold  默认 10；0 表示不预警
//   alert_denoise_resource_labels  CSV，默认 "resource" —— 仅用于下钻/筛选时
//                                   从告警 labels 中提取 instance 展示，不影响聚合主键

const (
	defaultDenoiseWindowMinutes  = 10
	defaultDenoiseStormThreshold = 10
	defaultDenoiseResourceLabels = "resource"
)

// incidentKey 故障的聚合主键：以 alertname 为主
type incidentKey struct {
	Alertname string
}

// incidentSupplements 补充指标：按 alertname（带 [源名] 前缀的 rule_name）预计算的
// 窗口统计，用于故障卡片并列参考。
type incidentSupplements struct {
	// PeakInstanceCount 窗口内 alert_history 中曾出现过的全部不重复 instance 数
	// （基于 fingerprint 去重），等价于「这个 alertname 一共影响了多少独立实例」，
	// 含已恢复的实例，能回答「我说的 16 个独立实例到底有多少」。
	PeakInstanceCount int
	// TotalFiringCount 窗口内 alert_history 中 event_type='firing' 的总记录数，
	// 即平台实际推送的告警事件总数（含 n9e 周期性重发的重复推送），
	// 能反映告警风暴 / 反复抖动的强度。
	TotalFiringCount int
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
	// Instance 从 labels 中按 instance_labels 链提取的实例标识（IP/Pod 等），
	// 由调用方在写入 incident.Alerts 时填入；这里冗余存储便于排序/去重。
	Instance string
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
	Instance       string            `json:"instance"` // 该告警对应的实例标识
	Labels         map[string]string `json:"labels"`
	Duration       string            `json:"duration"`
	Fingerprint    string            `json:"-"` // 内部使用：删除故障时反查指纹
}

// incidentListItem 列表项（不含 alerts 数组，控制响应体积）
type incidentListItem struct {
	Key           string    `json:"key"`
	Alertname     string    `json:"alertname"`
	Severity      string    `json:"severity"`
	State         string    `json:"state"`
	AlertCount    int       `json:"alert_count"`     // 故障内告警数
	InstanceCount int       `json:"instance_count"`  // 故障涉及的不重复实例数
	ClusterCount  int       `json:"cluster_count"`   // 故障涉及的集群（数据源）数
	FirstFiredAt  time.Time `json:"first_fired_at"`
	LastEventAt   time.Time `json:"last_event_at"`
	Storm         bool      `json:"storm"`
	Datasources   []string  `json:"datasources"`
	// 补充指标（不改 AlertCount/InstanceCount 主指标语义，作为并列参考）：
	//   PeakInstanceCount  窗口内曾触发过该故障的全部不重复实例数（含已恢复的），
	//                       用于看出「这个 alertname 一共影响了多少实例」。
	//   TotalFiringCount   窗口内 alert_history 中 event_type='firing' 的总记录数，
	//                       即 n9e/平台实际推送的告警事件总数（含重复推送/重发），
	//                       用于识别「反复抖动」「告警风暴」。
	PeakInstanceCount int `json:"peak_instance_count"`
	TotalFiringCount  int `json:"total_firing_count"`
}

// incidentDetailFull 内部使用的完整故障（带 alerts）
// 注意：必须显式带 JSON tag，否则序列化为大写字段名导致前端解析失败。
type incidentDetailFull struct {
	Key           string             `json:"key"`
	Alertname     string             `json:"alertname"`
	Severity      string             `json:"severity"`
	State         string             `json:"state"`
	AlertCount    int                `json:"alert_count"`
	InstanceCount int                `json:"instance_count"`
	ClusterCount  int                `json:"cluster_count"`
	FirstFiredAt  time.Time          `json:"first_fired_at"`
	LastEventAt   time.Time          `json:"last_event_at"`
	Storm         bool               `json:"storm"`
	Datasources   []string           `json:"datasources"`
	Alerts        []alertInIncident  `json:"alerts"`
	InstanceSet   map[string]struct{} `json:"-"` // 用于内部去重计数
	// 补充指标（不影响现有 AlertCount/InstanceCount 计算，仅用于故障卡片展示）
	PeakInstanceCount int `json:"peak_instance_count"`
	TotalFiringCount  int `json:"total_firing_count"`
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

// extractInstance 从告警 labels 中按选中的键顺序提取「实例标识」：
//   1) 按配置顺序（CSV 即优先级）取第一个非空值；
//   2) 全部未命中时回退到常见实例标签（instance/host/pod/node/target）；
//   3) 全缺失则返回 ""。
// 提取结果用于下钻时定位「具体实例」，不作为故障聚合的主键。
func extractInstance(labels map[string]string, keys []string) string {
	if len(keys) == 0 {
		keys = []string{defaultDenoiseResourceLabels}
	}
	for _, k := range keys {
		if v := labels[k]; v != "" {
			return v
		}
	}
	for _, k := range []string{"instance", "host", "pod", "node", "target"} {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}

// incidentHashKey 故障的稳定 key（用于前端下钻）。主键只含 alertname +
// firstFiredAt：同一 alertname 的相邻告警落入同一 incident，跨故障不会冲突。
func incidentHashKey(alertname string, firstFiredAt time.Time) string {
	h := sha256.New()
	h.Write([]byte(alertname))
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

// aggregateIncidents 把告警按 alertname 在时间窗内聚合为故障。
// 同一 alertname 下的所有实例/集群告警（去重后）按时间顺序归入同一故障，
// 跨窗口则开新故障。每个故障内统计：告警数、不重复实例数、涉及集群列表、风暴标记。
//
// supplements 是由调用方按 rule_name 一次性 GROUP BY alert_history 算出的补充指标
// （峰值实例数 = 窗口内曾出现的不重复 instance 数；累计触发次数 = firing 事件总数），
// 用作故障卡片并列参考，不影响现有 AlertCount/InstanceCount 语义。
func aggregateIncidents(alerts []alertInstance, cfg denoiseConfig, supplements map[string]incidentSupplements) []incidentDetailFull {
	incidentsByKey := map[incidentKey][]*incidentDetailFull{}
	for _, a := range alerts {
		// 故障主键：alertname
		k := incidentKey{Alertname: a.RuleName}
		list := incidentsByKey[k]
		var inc *incidentDetailFull
		if n := len(list); n > 0 {
			last := list[n-1]
			// 同一 alertname、相邻告警间隔 <= 窗口时合入上一个故障
			if cfg.WindowMinutes <= 0 || a.FirstFiredAt.Sub(last.LastEventAt) <= time.Duration(cfg.WindowMinutes)*time.Minute {
				inc = last
			}
		}
		inst := a.Instance
		if inst == "" {
			// 兜底：从 labels 里直接拿一个常见的实例标签
			inst = extractInstance(a.Labels, cfg.ResourceLabels)
		}
		if inc == nil {
			inc = &incidentDetailFull{
				Alertname:    a.RuleName,
				Severity:     a.Severity,
				State:        a.State,
				FirstFiredAt: a.FirstFiredAt,
				LastEventAt:  a.LastEventAt,
				InstanceSet:  map[string]struct{}{},
				Alerts:       []alertInIncident{},
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
			Instance:       inst,
			Labels:         a.Labels,
			Duration:       formatDuration(a.LastEventAt.Sub(a.FirstFiredAt).Milliseconds()),
			Fingerprint:    a.Fingerprint,
		})
		if inst != "" {
			inc.InstanceSet[inst] = struct{}{}
		}
		if !containsStr(inc.Datasources, a.DatasourceName) {
			inc.Datasources = append(inc.Datasources, a.DatasourceName)
		}
	}
	var all []*incidentDetailFull
	for _, list := range incidentsByKey {
		for _, inc := range list {
			inc.Key = incidentHashKey(inc.Alertname, inc.FirstFiredAt)
			inc.InstanceCount = len(inc.InstanceSet)
			inc.ClusterCount = len(inc.Datasources)
			if cfg.StormThreshold > 0 && inc.AlertCount > cfg.StormThreshold {
				inc.Storm = true
			}
			// 补充指标：按 alertname 从一次性 GROUP BY 结果里取（避免 N+1 查询）
			if supp, ok := supplements[inc.Alertname]; ok {
				inc.PeakInstanceCount = supp.PeakInstanceCount
				inc.TotalFiringCount = supp.TotalFiringCount
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

// computeIncidentSupplements 按 rule_name 一次性从 alert_history 行算出补充指标：
//   - PeakInstanceCount：窗口内曾出现的不重复 fingerprint 数（即独立实例数，含已恢复）
//   - TotalFiringCount：窗口内 event_type='firing' 的总记录数（即平台推送的告警事件总数，含重发）
//
// 用 Go 内存聚合避免 N+1 SQL；输入 rows 已是窗口内全集（受 filterAlertname/filterInstance 等过滤影响）。
// 同一 rule_name 的多个历史故障会合并为一份指标（与 aggregateIncidents 内的所有 incident 共享）。
func computeIncidentSupplements(rows []database.AlertHistory) map[string]incidentSupplements {
	out := map[string]incidentSupplements{}
	seenFP := map[string]map[string]struct{}{}
	for i := range rows {
		r := &rows[i]
		s := out[r.RuleName]
		if seenFP[r.RuleName] == nil {
			seenFP[r.RuleName] = map[string]struct{}{}
		}
		if _, ok := seenFP[r.RuleName][r.Fingerprint]; !ok {
			seenFP[r.RuleName][r.Fingerprint] = struct{}{}
			s.PeakInstanceCount++
		}
		if r.EventType == "firing" {
			s.TotalFiringCount++
		}
		out[r.RuleName] = s
	}
	return out
}

func toListItem(d incidentDetailFull) incidentListItem {
	return incidentListItem{
		Key: d.Key, Alertname: d.Alertname, Severity: d.Severity,
		State: d.State, AlertCount: d.AlertCount,
		InstanceCount: d.InstanceCount, ClusterCount: d.ClusterCount,
		FirstFiredAt: d.FirstFiredAt, LastEventAt: d.LastEventAt,
		Storm: d.Storm, Datasources: d.Datasources,
		PeakInstanceCount: d.PeakInstanceCount, TotalFiringCount: d.TotalFiringCount,
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
// 列表：以 alertname 为主聚合的故障概览，每条故障下挂多个实例/集群告警
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

	filterAlertname := strings.TrimSpace(p.Get("alertname"))
	filterInstance := strings.TrimSpace(p.Get("instance"))
	// 状态过滤：ongoing=告警中 / resolved=已恢复，空=全部
	filterState := strings.TrimSpace(p.Get("state"))

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	q := database.DB.Model(&database.AlertHistory{}).
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ? AND removed_at IS NULL AND dismissed_at IS NULL",
			[]string{"firing", "resolved", "pending"}, since)
	if dsID > 0 {
		q = q.Where("datasource_id = ?", dsID)
	}
	if sev := strings.TrimSpace(p.Get("severity")); sev != "" {
		q = q.Where("severity = ?", sev)
	}
	if filterAlertname != "" {
		q = q.Where("rule_name = ?", filterAlertname)
	}
	if filterInstance != "" {
		// 通用搜索：按 instance/host/pod 等常见标签子串匹配
		q = q.Where("labels_json LIKE ?", "%\""+filterInstance+"\"%")
	}
	var rows []database.AlertHistory
	if err := q.Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	totalRaw := len(rows)
	// 给每条 alert 预先填好 instance（extractInstance 在 alertInstance 上不持久，由 aggregateIncidents 内回退）
	for i := range rows {
		// 用一个临时对象计算并塞回 rows 的 labels（不修改原始 labels 字符串，仅临时填充）
		_ = rows[i]
	}
	alerts := dedupToAlerts(rows)
	totalAlerts := len(alerts)
	// 一次性按 rule_name 计算补充指标（PeakInstanceCount = 窗口内曾出现的不重复 fingerprint 数；
	// TotalFiringCount = 窗口内 firing 事件总数），避免在 aggregateIncidents 内做 N+1 查询。
	supplements := computeIncidentSupplements(rows)
	all := aggregateIncidents(alerts, cfg, supplements)

	items := make([]incidentListItem, 0, len(all))
	for _, inc := range all {
		if filterState != "" && inc.State != filterState {
			continue
		}
		if filterInstance != "" {
			// 故障级筛选：任一告警匹配即保留
			matched := false
			for _, a := range inc.Alerts {
				if a.Instance == filterInstance || strings.Contains(a.Instance, filterInstance) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
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
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ? AND removed_at IS NULL AND dismissed_at IS NULL",
			[]string{"firing", "resolved", "pending"}, since).
		Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	alerts := dedupToAlerts(rows)
	all := aggregateIncidents(alerts, cfg, computeIncidentSupplements(rows))
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

// collectIncidentFingerprints 按 key 匹配故障，收集故障内所有去重 fingerprint。
// 返回 (指纹列表, 匹配到的故障数)。
func collectIncidentFingerprints(all []incidentDetailFull, keys []string) ([]string, int) {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	fps := []string{}
	seen := map[string]bool{}
	matched := 0
	for _, inc := range all {
		if !want[inc.Key] {
			continue
		}
		matched++
		for _, a := range inc.Alerts {
			if a.Fingerprint != "" && !seen[a.Fingerprint] {
				seen[a.Fingerprint] = true
				fps = append(fps, a.Fingerprint)
			}
		}
	}
	return fps, matched
}

// handleAlertIncidentDelete POST /api/promai/alert/incidents/delete
// 删除聚合故障（聚合层软隐藏）：把故障内所有 AlertHistory 标记 dismissed_at（不动 removed_at）。
// 聚合查询过滤 dismissed_at IS NULL 后该故障立即消失；但只要底层 AlertInstance 仍在
// firing/pending，下一次新的告警事件会插入一条新的 AlertHistory（无 dismissed_at），
// 聚合按 fingerprint 取最新一行的状态，告警会自动重新出现。AlertInstance 已被恢复的，
// 没有新行产生，旧 dismissed 行始终被过滤，达到「彻底隐藏」。
func (a *AdminAPI) handleAlertIncidentDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	p := r.URL.Query()
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

	var body struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求体格式错误: "+err.Error())
		return
	}
	if len(body.Keys) == 0 {
		writeError(w, 400, "keys 不能为空")
		return
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var rows []database.AlertHistory
	if err := database.DB.Model(&database.AlertHistory{}).
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ? AND removed_at IS NULL AND dismissed_at IS NULL",
			[]string{"firing", "resolved", "pending"}, since).
		Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	all := aggregateIncidents(dedupToAlerts(rows), cfg, computeIncidentSupplements(rows))
	fps, matched := collectIncidentFingerprints(all, body.Keys)
	if matched == 0 {
		writeError(w, 404, "故障不存在或已过期")
		return
	}
	now := time.Now()
	// 只设 dismissed_at（不动 removed_at），让告警恢复后能再次显示
	if err := database.DB.Model(&database.AlertHistory{}).
		Where("fingerprint IN ? AND dismissed_at IS NULL", fps).
		Update("dismissed_at", now).Error; err != nil {
		writeError(w, 500, "删除故障失败: "+err.Error())
		return
	}
	auditLog("alert_incident_delete", fmt.Sprintf("keys=%d 故障, fingerprints=%d 条", matched, len(fps)))
	writeJSON(w, map[string]interface{}{
		"ok":           true,
		"matched":      matched,
		"fingerprints": len(fps),
	})
}

// handleAlertNoiseTop GET /api/promai/alert/noise-top
// TOP 噪音：以 alertname 聚合的告警数 + 实例数 + 集群数排序，优先治理反复触发
// 或在多实例/多集群上同时发作的高频告警。
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
		Where("event_type IN ? AND COALESCE(occurred_at, created_at) >= ? AND removed_at IS NULL AND dismissed_at IS NULL",
			[]string{"firing", "resolved", "pending"}, since).
		Order("occurred_at ASC").Limit(50000).Scan(&rows).Error; err != nil {
		writeError(w, 500, "查询告警历史失败: "+err.Error())
		return
	}
	alerts := dedupToAlerts(rows)
	all := aggregateIncidents(alerts, cfg, computeIncidentSupplements(rows))

	type item struct {
		Alertname     string   `json:"alertname"`
		AlertCount    int      `json:"alert_count"`
		InstanceCount int      `json:"instance_count"`
		ClusterCount  int      `json:"cluster_count"`
		Severity      string   `json:"severity"`
		State         string   `json:"state"`
		Storm         bool     `json:"storm"`
		Datasources   []string `json:"datasources"`
		LastEventAt   time.Time `json:"last_event_at"`
	}
	items := make([]item, 0, len(all))
	for _, inc := range all {
		items = append(items, item{
			Alertname: inc.Alertname, AlertCount: inc.AlertCount,
			InstanceCount: inc.InstanceCount, ClusterCount: inc.ClusterCount,
			Severity: inc.Severity, State: inc.State, Storm: inc.Storm,
			Datasources: inc.Datasources, LastEventAt: inc.LastEventAt,
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
