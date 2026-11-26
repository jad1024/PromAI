package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
	"PromAI/pkg/prometheus"

	"github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// =============================================================================
// 北极星（Polaris）API · 按 Flashcat 真实逻辑重建
//
// 北极星定位：业务/系统核心健康指标（订单、支付、GMV、在线用户）的梳理、采集、
// 计算、智能检测与告警，是故障发现入口。与灭火图分工：
//   北极星 = 业务可读的"业务是否受损"；灭火图 = IT 侧"哪里红了、如何定位"。
//
// 本文件提供：业务线 → 指标池（复用 MetricConfig.PromQL）→ 图表绑定 → 智能检测
// （阈值/同环比/数据中断）→ 告警策略 → 业务 SLO 的 CRUD 与实时查询。
// =============================================================================

// ---------- 对外数据结构 ----------

type PBusinessView struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Owner        string  `json:"owner"`
	Team         string  `json:"team"`
	Icon         string  `json:"icon"`
	Color        string  `json:"color"`
	DatasourceID uint    `json:"datasource_id"`
	Enabled      bool    `json:"enabled"`
	SortOrder    int     `json:"sort_order"`
	ChartCount   int     `json:"chart_count"`
	AlertCount   int     `json:"alert_count"`
	Health       float64 `json:"health"`   // 该业务线当前健康度（异常检测占比）
	Status       string  `json:"status"`   // normal / warning / critical
}

type PMetricPoint struct {
	T   int64   `json:"t"`
	V   float64 `json:"v"`
}

type PChartView struct {
	ID           uint           `json:"id"`
	BusinessID   uint           `json:"business_id"`
	Name         string         `json:"name"`
	ChartType    string         `json:"chart_type"`
	MetricIDs    []uint         `json:"metric_ids"`
	MetricNames  []string       `json:"metric_names"`
	CustomPromQL string         `json:"custom_promql"`
	Unit         string         `json:"unit"`
	Enabled      bool           `json:"enabled"`
	SortOrder    int            `json:"sort_order"`
	Series       []PChartSeries `json:"series"`
	Status       string         `json:"status"` // 汇总检测状态
	LastValue    float64        `json:"last_value"`
}

type PChartSeries struct {
	Name   string         `json:"name"`
	Points []PMetricPoint `json:"points"`
}

type PDetectionView struct {
	ID          uint    `json:"id"`
	BusinessID  uint    `json:"business_id"`
	ChartID     uint    `json:"chart_id"`
	MetricID    uint    `json:"metric_id"`
	DetectType  string  `json:"detect_type"`
	Threshold   float64 `json:"threshold"`
	CompareOp   string  `json:"compare_op"`
	DurationMin int     `json:"duration_min"`
	Sensitivity float64 `json:"sensitivity"`
	Enabled     bool    `json:"enabled"`
	// 运行时判定
	Triggered bool    `json:"triggered"`
	Reason    string  `json:"reason"`
	LastValue float64 `json:"last_value"`
}

type PPolicyView struct {
	ID            uint   `json:"id"`
	BusinessID    uint   `json:"business_id"`
	Name          string `json:"name"`
	Severity      string `json:"severity"`
	Enabled       bool   `json:"enabled"`
	ActiveFrom    string `json:"active_from"`
	ActiveTo      string `json:"active_to"`
	NotifyTarget  string `json:"notify_target"`
	FiremapNodeID uint   `json:"firemap_node_id"`
}

// ---------- 种子数据（仅首次写入） ----------

var defaultPolarisBusinesses = []database.PolarisBusiness{
	{Name: "交易业务线", Description: "下单 / 支付 / 成交额等核心交易指标", Owner: "交易产品负责人", Team: "交易研发/SRE", Icon: "易", Color: "#7c3aed", SortOrder: 1},
	{Name: "行情业务线", Description: "行情推送 / 报价延迟 / 行情覆盖率", Owner: "行情产品负责人", Team: "行情研发/SRE", Icon: "行", Color: "#0f9d8a", SortOrder: 2},
}

func ensureDefaultPolaris() {
	if database.DB == nil {
		return
	}
	var cnt int64
	database.DB.Model(&database.PolarisBusiness{}).Count(&cnt)
	if cnt > 0 {
		return
	}
	for _, b := range defaultPolarisBusinesses {
		database.DB.Create(&b)
	}
}

// ---------- 纯函数（可单测） ----------

// detectStatus 根据检测类型和最近值判定是否触发。
// threshold: 按比较符与阈值判定；nodata: 无数据触发；predict/mom: 用敏感度作简化 z-score 阈值。
func detectStatus(d database.PolarisDetection, last *float64, hasData bool, history []float64) (bool, string) {
	if !d.Enabled {
		return false, "已禁用"
	}
	switch d.DetectType {
	case "nodata":
		if !hasData {
			return true, "指标数据中断"
		}
		return false, "数据正常"
	case "threshold":
		if !hasData || last == nil {
			return false, "无数据"
		}
		if d.CompareOp == "gt" && *last > d.Threshold {
			return true, fmt.Sprintf("当前 %.4g > 阈值 %.4g", *last, d.Threshold)
		}
		if d.CompareOp != "gt" && *last < d.Threshold {
			return true, fmt.Sprintf("当前 %.4g < 阈值 %.4g", *last, d.Threshold)
		}
		return false, "阈值内"
	case "mom", "predict":
		if !hasData || last == nil || len(history) < 2 {
			return false, "样本不足"
		}
		mean := 0.0
		for _, v := range history {
			mean += v
		}
		mean /= float64(len(history))
		// 简化标准差
		variance := 0.0
		for _, v := range history {
			variance += (v - mean) * (v - mean)
		}
		std := sqrt64(variance / float64(len(history)))
		if std <= 0 {
			return false, "无波动"
		}
		z := (*last - mean) / std
		sens := d.Sensitivity
		if sens <= 0 {
			sens = 3
		}
		if z > sens || z < -sens {
			return true, fmt.Sprintf("%s 偏离 %.1f 个标准差", map[string]string{"mom": "同环比", "predict": "智能预测"}[d.DetectType], abs64(z))
		}
		return false, "趋势正常"
	}
	return false, "未知检测类型"
}

func abs64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func sqrt64(f float64) float64 {
	if f <= 0 {
		return 0
	}
	// 牛顿迭代，避免引入 math 之外的复杂依赖（math 亦可直接用，这里保持纯函数简单）
	x := f
	for i := 0; i < 20; i++ {
		x = (x + f/x) / 2
	}
	return x
}

// businessHealth 按触发的检测项比例算业务线健康度。
func businessHealth(total, triggered int) float64 {
	if total <= 0 {
		return 100
	}
	return round1(float64(total-triggered) / float64(total) * 100)
}

func polarisStatus(health float64, triggered int) string {
	if triggered > 0 || health < 70 {
		return "critical"
	}
	if health < 95 {
		return "warning"
	}
	return "normal"
}

// ---------- PromQL 实时查询 ----------

// querySeries 查询一条 PromQL 在时间窗口内的时序数据（复用默认数据源缓存）。
func querySeries(ctx context.Context, ds database.DataSource, expr string, start, end time.Time, step time.Duration) ([]PMetricPoint, error) {
	client, err := prometheus.DefaultCache.Get(ds.ID, ds.URL, ds.Username, ds.Password)
	if err != nil {
		return nil, err
	}
	rng := v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	}
	result, _, err := client.API.QueryRange(ctx, expr, rng)
	if err != nil {
		return nil, err
	}
	points := []PMetricPoint{}
	switch v := result.(type) {
	case model.Matrix:
		if len(v) > 0 {
			// 取第一条序列（北极星图表简化展示；多序列由调用方按指标拆分）
			for _, p := range v[0].Values {
				points = append(points, PMetricPoint{T: int64(p.Timestamp) / 1000, V: float64(p.Value)})
			}
		}
	case model.Vector:
		if len(v) > 0 {
			points = append(points, PMetricPoint{T: end.Unix(), V: float64(v[0].Value)})
		}
	}
	return points, nil
}

// loadChartPromQL 解析图表绑定的指标 ID，返回每条序列的 PromQL + 名称。
type polarisPair struct{ Name, Expr string }

func loadChartPromQL(c database.PolarisChart) []polarisPair {
	out := []polarisPair{}
	var ids []uint
	_ = json.Unmarshal([]byte(c.MetricIDs), &ids)
	if len(ids) > 0 {
		for _, id := range ids {
			var mc database.MetricConfig
			if database.DB.First(&mc, id).Error == nil {
				out = append(out, polarisPair{Name: mc.Name, Expr: mc.Query})
			}
		}
	}
	if len(out) == 0 && strings.TrimSpace(c.CustomPromQL) != "" {
		out = append(out, polarisPair{Name: c.Name, Expr: c.CustomPromQL})
	}
	return out
}

// resolveBusinessDS 取业务线的数据源；未指定则取第一个可用数据源。
func resolveBusinessDS(b database.PolarisBusiness) (database.DataSource, bool) {
	var ds database.DataSource
	if b.DatasourceID > 0 {
		if database.DB.First(&ds, b.DatasourceID).Error == nil {
			return ds, true
		}
	}
	if database.DB.First(&ds).Error == nil {
		return ds, true
	}
	return ds, false
}

// ---------- HTTP handlers ----------

func (a *AdminAPI) handlePolarisBusinesses(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeJSON(w, map[string]any{"items": []PBusinessView{}})
		return
	}
	ensureDefaultPolaris()
	var bs []database.PolarisBusiness
	database.DB.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&bs)
	items := make([]PBusinessView, 0, len(bs))
	for _, b := range bs {
		var charts, policies int64
		database.DB.Model(&database.PolarisChart{}).Where("business_id = ? AND enabled = ?", b.ID, true).Count(&charts)
		database.DB.Model(&database.PolarisAlertPolicy{}).Where("business_id = ? AND enabled = ?", b.ID, true).Count(&policies)
		var dets []database.PolarisDetection
		database.DB.Where("business_id = ? AND enabled = ?", b.ID, true).Find(&dets)
		triggered := 0
		for _, d := range dets {
			// 业务线列表用轻量判定：只统计 threshold/nodata 配置的静态触发，时序类在详情里实时算
			if d.DetectType == "nodata" {
				triggered++
			}
		}
		items = append(items, PBusinessView{
			ID: b.ID, Name: b.Name, Description: b.Description, Owner: b.Owner, Team: b.Team,
			Icon: b.Icon, Color: b.Color, DatasourceID: b.DatasourceID, Enabled: b.Enabled, SortOrder: b.SortOrder,
			ChartCount: int(charts), AlertCount: int(policies),
			Health: businessHealth(len(dets), triggered),
			Status: polarisStatus(businessHealth(len(dets), triggered), triggered),
		})
	}
	writeJSON(w, map[string]any{"items": items})
}

// handlePolarisBusinessDetail 返回业务线详情：图表（含时序数据）、检测状态、告警策略、SLO。
func (a *AdminAPI) handlePolarisBusinessDetail(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeError(w, 503, "数据库未就绪")
		return
	}
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, 400, "缺少业务线 id")
		return
	}
	var b database.PolarisBusiness
	if database.DB.First(&b, id).Error != nil {
		writeError(w, 404, "业务线不存在")
		return
	}
	ds, hasDS := resolveBusinessDS(b)

	// 图表 + 实时时序
	var charts []database.PolarisChart
	database.DB.Where("business_id = ?", b.ID).Order("sort_order asc, id asc").Find(&charts)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	chartViews := make([]PChartView, 0, len(charts))
	// 收集每个指标的最近值/历史，供检测使用
	metricLast := map[uint]*float64{}
	metricHist := map[uint][]float64{}
	metricHasData := map[uint]bool{}
	for _, c := range charts {
		cv := PChartView{
			ID: c.ID, BusinessID: c.BusinessID, Name: c.Name, ChartType: c.ChartType,
			CustomPromQL: c.CustomPromQL, Unit: c.Unit, Enabled: c.Enabled, SortOrder: c.SortOrder,
			Series: []PChartSeries{}, MetricIDs: []uint{}, MetricNames: []string{},
		}
		_ = json.Unmarshal([]byte(c.MetricIDs), &cv.MetricIDs)
		for _, pair := range loadChartPromQL(c) {
			if !hasDS {
				cv.Series = append(cv.Series, PChartSeries{Name: pair.Name, Points: []PMetricPoint{}})
				continue
			}
			pts, qerr := querySeries(ctx, ds, pair.Expr, start, now, 2*time.Minute)
			if qerr != nil || len(pts) == 0 {
				cv.Series = append(cv.Series, PChartSeries{Name: pair.Name, Points: []PMetricPoint{}})
				continue
			}
			cv.Series = append(cv.Series, PChartSeries{Name: pair.Name, Points: pts})
			last := pts[len(pts)-1].V
			cv.LastValue = last
			// 找到对应的 MetricConfig ID 记录到检测用 map
			for _, mid := range cv.MetricIDs {
				var mc database.MetricConfig
				if database.DB.First(&mc, mid).Error == nil && mc.Query == pair.Expr {
					metricLast[mid] = &last
					metricHasData[mid] = true
					for _, p := range pts {
						metricHist[mid] = append(metricHist[mid], p.V)
					}
				}
			}
		}
		for _, mid := range cv.MetricIDs {
			var mc database.MetricConfig
			if database.DB.First(&mc, mid).Error == nil {
				cv.MetricNames = append(cv.MetricNames, mc.Name)
			}
		}
		cv.Status = "normal"
		chartViews = append(chartViews, cv)
	}

	// 检测状态
	var dets []database.PolarisDetection
	database.DB.Where("business_id = ?", b.ID).Order("id asc").Find(&dets)
	detViews := make([]PDetectionView, 0, len(dets))
	triggered := 0
	for _, d := range dets {
		last, ok := metricLast[d.MetricID]
		hist := metricHist[d.MetricID]
		tr, reason := detectStatus(d, last, ok || len(hist) > 0, hist)
		if tr {
			triggered++
			// 回填图表状态
			for i := range chartViews {
				if chartViews[i].ID == d.ChartID {
					chartViews[i].Status = "critical"
				}
			}
		}
		lv := 0.0
		if last != nil {
			lv = *last
		}
		detViews = append(detViews, PDetectionView{
			ID: d.ID, BusinessID: d.BusinessID, ChartID: d.ChartID, MetricID: d.MetricID,
			DetectType: d.DetectType, Threshold: d.Threshold, CompareOp: d.CompareOp,
			DurationMin: d.DurationMin, Sensitivity: d.Sensitivity, Enabled: d.Enabled,
			Triggered: tr, Reason: reason, LastValue: lv,
		})
	}

	// 告警策略
	var policies []database.PolarisAlertPolicy
	database.DB.Where("business_id = ?", b.ID).Order("id asc").Find(&policies)
	policyViews := make([]PPolicyView, 0, len(policies))
	for _, p := range policies {
		policyViews = append(policyViews, PPolicyView{
			ID: p.ID, BusinessID: p.BusinessID, Name: p.Name, Severity: p.Severity, Enabled: p.Enabled,
			ActiveFrom: p.ActiveFrom, ActiveTo: p.ActiveTo, NotifyTarget: p.NotifyTarget, FiremapNodeID: p.FiremapNodeID,
		})
	}

	// 业务 SLO（关联该业务线的启用 SLO）
	var slos []database.FiremapSLO
	database.DB.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&slos)
	sloViews := make([]FCPolarisItem, 0, len(slos))
	for _, s := range slos {
		score, _ := recalcNodeHealth(s.NodeID)
		sloViews = append(sloViews, FCPolarisItem{
			ID: s.ID, NodeID: s.NodeID, Name: s.Name, Description: s.Description,
			Target: s.Target, Current: score, ErrorBudget: calcErrorBudget(score, s.Target),
			WindowDays: s.WindowDays, Trend: nodeHealthTrend(s.NodeID, score),
			Status: func() string {
				b := calcErrorBudget(score, s.Target)
				if b < 25 { return "critical" }
				if b < 60 { return "warning" }
				return "normal"
			}(),
		})
	}

	health := businessHealth(len(detViews), triggered)
	writeJSON(w, map[string]any{
		"business": PBusinessView{
			ID: b.ID, Name: b.Name, Description: b.Description, Owner: b.Owner, Team: b.Team,
			Icon: b.Icon, Color: b.Color, DatasourceID: b.DatasourceID, Enabled: b.Enabled,
			ChartCount: len(chartViews), AlertCount: len(policyViews), Health: health,
			Status: polarisStatus(health, triggered),
		},
		"charts": chartViews, "detections": detViews, "policies": policyViews, "slos": sloViews,
		"has_datasource": hasDS,
	})
}

// handlePolarisMetricPool 返回可入池的现有指标（复用 MetricConfig 的 PromQL）。
func (a *AdminAPI) handlePolarisMetricPool(w http.ResponseWriter, r *http.Request) {
	type poolItem struct {
		ID        uint    `json:"id"`
		Name      string  `json:"name"`
		Query     string  `json:"query"`
		Unit      string  `json:"unit"`
		Type      string  `json:"type"`
		Threshold float64 `json:"threshold"`
	}
	items := []poolItem{}
	if database.DB != nil {
		var mcs []database.MetricConfig
		database.DB.Order("id asc").Limit(500).Find(&mcs)
		for _, mc := range mcs {
			var mt database.MetricType
			typeName := ""
			if database.DB.First(&mt, mc.MetricTypeID).Error == nil {
				typeName = mt.TypeName
			}
			items = append(items, poolItem{ID: mc.ID, Name: mc.Name, Query: mc.Query, Unit: mc.Unit, Type: typeName, Threshold: mc.Threshold})
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

// handlePolarisCRUD 统一处理业务线/图表/检测/策略的增删改。
// ?kind=business|chart|detection|policy ，POST=创建/更新（带 id），DELETE=删除（带 id）。
func (a *AdminAPI) handlePolarisCRUD(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeError(w, 503, "数据库未就绪")
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		writeError(w, 400, "缺少 kind")
		return
	}
	if r.Method == http.MethodDelete {
		id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id == 0 {
			writeError(w, 400, "缺少 id")
			return
		}
		switch kind {
		case "business":
			database.DB.Delete(&database.PolarisBusiness{}, id)
			database.DB.Where("business_id = ?", id).Delete(&database.PolarisChart{})
			database.DB.Where("business_id = ?", id).Delete(&database.PolarisDetection{})
			database.DB.Where("business_id = ?", id).Delete(&database.PolarisAlertPolicy{})
		case "chart":
			database.DB.Delete(&database.PolarisChart{}, id)
			database.DB.Where("chart_id = ?", id).Delete(&database.PolarisDetection{})
		case "detection":
			database.DB.Delete(&database.PolarisDetection{}, id)
		case "policy":
			database.DB.Delete(&database.PolarisAlertPolicy{}, id)
		}
		writeJSON(w, map[string]any{"success": true})
		return
	}
	// POST：按 kind 解码
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, 400, "参数错误")
		return
	}
	var id uint
	if v, ok := raw["id"]; ok {
		switch n := v.(type) {
		case float64:
			id = uint(n)
		case string:
			n2, _ := strconv.ParseUint(n, 10, 64)
			id = uint(n2)
		}
	}
	b, _ := json.Marshal(raw)
	switch kind {
	case "business":
		var obj database.PolarisBusiness
		_ = json.Unmarshal(b, &obj)
		if id > 0 { obj.ID = id; database.DB.Model(&obj).Updates(&obj) } else { database.DB.Create(&obj) }
		writeJSON(w, map[string]any{"success": true, "id": obj.ID})
	case "chart":
		var obj database.PolarisChart
		_ = json.Unmarshal(b, &obj)
		if id > 0 { obj.ID = id; database.DB.Model(&obj).Updates(&obj) } else { database.DB.Create(&obj) }
		writeJSON(w, map[string]any{"success": true, "id": obj.ID})
	case "detection":
		var obj database.PolarisDetection
		_ = json.Unmarshal(b, &obj)
		if id > 0 { obj.ID = id; database.DB.Model(&obj).Updates(&obj) } else { database.DB.Create(&obj) }
		writeJSON(w, map[string]any{"success": true, "id": obj.ID})
	case "policy":
		var obj database.PolarisAlertPolicy
		_ = json.Unmarshal(b, &obj)
		if id > 0 { obj.ID = id; database.DB.Model(&obj).Updates(&obj) } else { database.DB.Create(&obj) }
		writeJSON(w, map[string]any{"success": true, "id": obj.ID})
	default:
		writeError(w, 400, "未知 kind")
	}
}

// registerPolarisHandlers 注册北极星路由。
func (a *AdminAPI) registerPolarisHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/promai/polaris/businesses", a.logRequest(a.authMiddleware(a.handlePolarisBusinesses)))
	mux.HandleFunc("/api/promai/polaris/business", a.logRequest(a.authMiddleware(a.handlePolarisBusinessDetail)))
	mux.HandleFunc("/api/promai/polaris/metric-pool", a.logRequest(a.authMiddleware(a.handlePolarisMetricPool)))
	mux.HandleFunc("/api/promai/polaris/crud", a.logRequest(a.authMiddleware(a.handlePolarisCRUD)))
}
