package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
	piagent "PromAI/pkg/pi-agent"
	"PromAI/pkg/prometheus"

	"github.com/prometheus/common/model"
)

// =============================================================================
// Flashcat 场景层 API（v2 · 按 Flashcat 逻辑重建）
//
// 与旧实现的根本区别：
//   - 旧：观测对象 = 巡检快照按 instance label 临时分组；健康分 = (总数-告警)/总数；
//         FlashAI 检查项 = 本地伪规则；"AI 分析" = 触发一次全量巡检。
//   - 新：观测对象 = 持久化、可编辑的透视服务树（业务 → 服务 → 实例）；
//         健康分 = 检查项结果 + firing 告警 + 巡检异常指标 + SLO 的加权判定；
//         检查项 = 可配置的真实诊断动作（PromQL / 告警 / 快照 / SLO / AI）；
//         FlashAI = 针对「当前对象」做定向根因诊断，不跑全量巡检。
//         异常沿服务树向上透传（状态取最严重，分数取子节点均值）。
//
// 服务树首次由巡检数据自动发现并落库，之后完全由用户维护（nodes CRUD）。
// =============================================================================

const (
	fcLevelBusiness = "business"
	fcLevelService  = "service"
	fcLevelInstance = "instance"
)

// ---------- 默认配置（系统种子，仅首次写入，之后以数据库为准） ----------

var defaultFiremapCheckConfigs = []database.FlashAICheckConfig{
	{Key: "instance_up", Name: "实例存活", Enabled: true, SortOrder: 1, CheckType: "promql",
		PromQL: "up", Threshold: 1, CompareOp: "lt", Unit: "", Remark: "up < 1 判定实例不可用"},
	{Key: "alert_firing", Name: "活跃告警", Enabled: true, SortOrder: 2, CheckType: "alert",
		Remark: "检查该实例当前 firing 告警"},
	{Key: "snapshot_health", Name: "巡检异常指标", Enabled: true, SortOrder: 3, CheckType: "snapshot",
		Remark: "检查最新巡检快照里该实例的 critical / warning 指标"},
	{Key: "slo_budget", Name: "SLO 错误预算", Enabled: true, SortOrder: 4, CheckType: "slo",
		Remark: "北极星 SLO 剩余错误预算低于 25% 判定异常"},
	{Key: "metric_custom", Name: "自定义指标阈值", Enabled: true, SortOrder: 5, CheckType: "promql",
		PromQL: "", Threshold: 0, CompareOp: "gt", Unit: "", Remark: "填写 PromQL 后按阈值判定，留空则跳过"},
	{Key: "ai_diagnosis", Name: "PromAI 定向诊断", Enabled: true, SortOrder: 6, CheckType: "ai",
		Remark: "由 /ai-analyze 针对本对象执行，不在此处跑全量巡检"},
}

var defaultFiremapSLOs = []database.FiremapSLO{
	{Name: "核心链路可用性", Description: "业务整体可用性目标", Target: 99.9, WindowDays: 30, Enabled: true, SortOrder: 1},
	{Name: "接口响应延迟", Description: "P99 延迟达标率", Target: 99.5, WindowDays: 30, Enabled: true, SortOrder: 2},
	{Name: "请求错误率", Description: "非 5xx 请求占比", Target: 99.0, WindowDays: 30, Enabled: true, SortOrder: 3},
}

// ---------- 对外数据结构 ----------

type FCObject struct {
	ID           string     `json:"id"`
	NodeID       uint       `json:"node_id"`
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	Level        string     `json:"level"`
	ParentID     uint       `json:"parent_id"`
	DatasourceID uint       `json:"datasource_id"`
	Datasource   string     `json:"datasource"`
	Instance     string     `json:"instance"`
	Layer        string     `json:"layer"`
	HealthScore  float64    `json:"health_score"`
	Status       string     `json:"status"`
	TotalChecks  int        `json:"total_checks"`
	Critical     int        `json:"critical"`
	Warning      int        `json:"warning"`
	Normal       int        `json:"normal"`
	Alerts       int        `json:"alerts"`
	Metrics      int        `json:"metrics"`
	NoData       bool       `json:"no_data"`
	LastCheckAt  *time.Time `json:"last_check_at,omitempty"`
}

// FCNodeView 卡片池表格用的节点视图：把 datasource_id 映射成可读名称。
type FCNodeView struct {
	ID           uint   `json:"id"`
	ParentID     uint   `json:"parent_id"`
	ParentName   string `json:"parent_name"`
	Name         string `json:"name"`
	Level        string `json:"level"`
	DatasourceID uint   `json:"datasource_id"`
	Datasource   string `json:"datasource"`
	Instance     string `json:"instance"`
	SortOrder    int    `json:"sort_order"`
	Remark       string `json:"remark"`
}

type FCLayer struct {
	Name     string  `json:"name"`
	Count    int     `json:"count"`
	Critical int     `json:"critical"`
	Warning  int     `json:"warning"`
	Normal   int     `json:"normal"`
	AvgScore float64 `json:"avg_score"`
}

type FCTreeNode struct {
	ID          string        `json:"id"`
	NodeID      uint          `json:"node_id"`
	Name        string        `json:"name"`
	Level       string        `json:"level"`
	Count       string        `json:"count"`
	HealthScore float64       `json:"health_score"`
	Status      string        `json:"status"`
	Children    []*FCTreeNode `json:"children,omitempty"`
	ObjectID    string        `json:"object_id,omitempty"`
}

type FCCheckItem struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	CheckType string `json:"check_type"`
	Status    string `json:"status"` // ok / warn / err / na
	Result    string `json:"result"`
	Detail    string `json:"detail"`
	CostMs    int64  `json:"cost_ms"`
}

type FCStats struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Normal   int `json:"normal"`
	NoData   int `json:"no_data"`
}

type FCDatasource struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type FCBootstrap struct {
	HasData       bool           `json:"has_data"`
	OverallHealth float64        `json:"overall_health"`
	Objects       []FCObject     `json:"objects"`
	Layers        []FCLayer      `json:"layers"`
	Tree          *FCTreeNode    `json:"tree"`
	Stats         FCStats        `json:"stats"`
	LastReportAt  *time.Time     `json:"last_report_at,omitempty"`
	Datasources   []FCDatasource `json:"datasources"`
	Message       string         `json:"message,omitempty"`
}

type FCPolarisItem struct {
	ID          uint      `json:"id"`
	NodeID      uint      `json:"node_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Target      float64   `json:"target"`
	Current     float64   `json:"current"`
	ErrorBudget float64   `json:"error_budget"`
	Status      string    `json:"status"`
	WindowDays  int       `json:"window_days"`
	Trend       []float64 `json:"trend"`
}

type FCEventItem struct {
	ID       string    `json:"id"`
	NodeID   uint      `json:"node_id"`
	NodeName string    `json:"node_name"`
	Source   string    `json:"source"`
	Severity string    `json:"severity"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	Status   string    `json:"status"`
	At       time.Time `json:"at"`
}

// FCContext 一个对象定向诊断所需的全部上下文（一次取齐，避免检查项各自查库）。
type FCContext struct {
	Node         database.FiremapNode
	DS           database.DataSource
	Path         string
	Snapshot     []MetricHealthData
	Alerts       []database.AlertInstance
	LatestChecks []database.FiremapCheckResult
}

// ---------- 纯函数（可单测，不依赖 DB / 网络） ----------

var layerKeywords = []struct {
	Layer    string
	Keywords []string
}{
	{"业务接口", []string{"业务", "接口", "api", "gateway", "网关", "下单", "支付", "交易", "biz", "http"}},
	{"微服务", []string{"微服务", "服务", "service", "jvm", "java", "应用", "app", "容器", "k8s", "pod", "deployment", "daemonset"}},
	{"中间件", []string{"中间件", "mysql", "redis", "kafka", "es", "elasticsearch", "mongo", "postgres", "doris", "clickhouse", "zk", "zookeeper", "数据库", "缓存", "队列"}},
	{"基础设施", []string{"基础设施", "主机", "host", "node", "cpu", "内存", "磁盘", "disk", "network", "网络", "系统"}},
}

func mapMetricTypeToLayer(typeName string) string {
	s := strings.ToLower(typeName)
	for _, l := range layerKeywords {
		for _, kw := range l.Keywords {
			if strings.Contains(s, strings.ToLower(kw)) {
				return l.Layer
			}
		}
	}
	return "未分组"
}

// pickInstance 从 labels 里挑出能代表一个实例的值（用于自动发现，不参与服务树判定）。
func pickInstance(labels map[string]string) string {
	for _, k := range []string{"instance", "Instance", "nodename", "node_name",
		"host", "hostname", "pod", "pod_name", "target", "address"} {
		if v := strings.TrimSpace(labels[k]); v != "" && v != "-" {
			return v
		}
	}
	return ""
}

func calcHealthScore(total, critical, warning int) float64 {
	if total <= 0 {
		return 100.0
	}
	return round1(float64(total-critical-warning) / float64(total) * 100)
}

func scoreStatus(score float64, critical int) string {
	if critical > 0 || score < 70 {
		return "critical"
	}
	if score < 95 {
		return "warning"
	}
	return "normal"
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

// calcObjectHealth Flashcat 式健康分：检查项 + firing 告警 + 巡检异常指标 加权扣分。
func calcObjectHealth(checks []database.FiremapCheckResult, firingAlerts, abnormalMetrics int) float64 {
	penalty := 0.0
	for _, c := range checks {
		switch c.Status {
		case "err":
			penalty += 18
		case "warn":
			penalty += 6
		}
	}
	penalty += float64(firingAlerts) * 10
	penalty += float64(abnormalMetrics) * 4
	s := 100 - penalty
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return round1(s)
}

// calcErrorBudget 剩余错误预算：allowed = 100-target，consumed = 100-健康分。
// 例：target=99.9（允许 0.1% 不可用），score=99.95（实际 0.05%）→ 剩余 50%。
func calcErrorBudget(score, target float64) float64 {
	allowed := 100 - target
	if allowed <= 0 {
		return 100
	}
	remaining := 100 - (100-score)/allowed*100
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return round1(remaining)
}

// evaluateCheckResult 检查项判定（纯逻辑）。value 为实时查询值，count 为异常条数。
func evaluateCheckResult(cfg database.FlashAICheckConfig, value *float64, count int, hasData bool) (string, string) {
	if !hasData {
		return "na", "无数据"
	}
	typ := cfg.CheckType
	if typ == "" { // 兼容旧版种子配置
		typ = "snapshot"
	}
	switch typ {
	case "promql":
		if value == nil {
			return "na", "无数据"
		}
		v := *value
		bad := false
		switch cfg.CompareOp {
		case "lt":
			bad = v < cfg.Threshold
		case "gt":
			bad = v > cfg.Threshold
		case "eq":
			bad = v == cfg.Threshold
		case "ne":
			bad = v != cfg.Threshold
		default:
			bad = v > cfg.Threshold
		}
		if bad {
			return "err", fmt.Sprintf("%.4g 触发阈值 %s %.4g", v, cfg.CompareOp, cfg.Threshold)
		}
		return "ok", fmt.Sprintf("%.4g %s（阈值 %s %.4g）", v, cfg.Unit, cfg.CompareOp, cfg.Threshold)
	case "alert", "snapshot":
		if count > 0 {
			return "err", fmt.Sprintf("发现 %d 项异常", count)
		}
		return "ok", "无异常"
	case "slo":
		if value == nil {
			return "na", "无数据"
		}
		b := *value
		if b < 25 {
			return "err", fmt.Sprintf("错误预算剩余 %.1f%%", b)
		}
		if b < 60 {
			return "warn", fmt.Sprintf("错误预算剩余 %.1f%%", b)
		}
		return "ok", fmt.Sprintf("错误预算剩余 %.1f%%", b)
	case "ai":
		return "na", "待 AI 诊断"
	default:
		return "na", "未知检查类型"
	}
}

// injectInstanceMatcher 把 instance matcher 正确插进 PromQL 的指标名后花括号内。
// 旧实现把 {instance="..."} 直接追加到表达式尾部，遇到已有 {} 的表达式会生成非法 PromQL。
func injectInstanceMatcher(expr, instance string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" || instance == "" {
		return expr
	}
	m := fmt.Sprintf("instance=%q", instance)
	if i := strings.Index(expr, "{"); i >= 0 {
		if j := strings.Index(expr[i:], "}"); j > 0 {
			inner := strings.TrimSpace(expr[i+1 : i+j])
			if inner == "" {
				return expr[:i+1] + m + expr[i+j:]
			}
			return expr[:i+1] + m + "," + expr[i+1:i+j] + expr[i+j:]
		}
	}
	k := 0
	for k < len(expr) {
		c := expr[k]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == ':' {
			k++
		} else {
			break
		}
	}
	if k == 0 {
		return expr
	}
	// 函数 / 聚合表达式（rate(...) / sum by(...) (...) 等）不能简单在首标识符后加 {}，
	// 否则会生成 rate{...}(...) 这类非法 PromQL。交由调用方手写 {instance="..."}。
	if isPromQLFunction(expr[:k]) {
		return expr
	}
	return expr[:k] + "{" + m + "}" + expr[k:]
}

func isPromQLFunction(name string) bool {
	switch strings.ToLower(name) {
	case "sum", "avg", "min", "max", "count", "stddev", "stdvar", "topk", "bottomk", "quantile",
		"rate", "irate", "increase", "delta", "idelta", "deriv", "predict_linear", "holt_winters",
		"histogram_quantile", "label_replace", "label_join", "vector", "scalar", "time", "timestamp",
		"abs", "ceil", "floor", "round", "clamp", "clamp_max", "clamp_min", "sgn", "sort", "sort_desc":
		return true
	}
	return false
}

func abnormalCount(ms []MetricHealthData) int {
	c := 0
	for _, m := range ms {
		if m.Status == "critical" || m.Status == "warning" {
			c++
		}
	}
	return c
}

func countStatus(checks []database.FiremapCheckResult, status string) int {
	c := 0
	for _, x := range checks {
		if x.Status == status {
			c++
		}
	}
	return c
}

func sortFCObjects(objs []FCObject) []FCObject {
	out := append([]FCObject(nil), objs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HealthScore != out[j].HealthScore {
			return out[i].HealthScore < out[j].HealthScore
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func buildLayersFromObjects(objs []FCObject) []FCLayer {
	m := map[string]*FCLayer{}
	order := []string{}
	for _, o := range objs {
		l, ok := m[o.Layer]
		if !ok {
			l = &FCLayer{Name: o.Layer}
			m[o.Layer] = l
			order = append(order, o.Layer)
		}
		l.Count++
		switch o.Status {
		case "critical":
			l.Critical++
		case "warning":
			l.Warning++
		default:
			l.Normal++
		}
	}
	out := []FCLayer{}
	for _, k := range order {
		l := m[k]
		sum := 0.0
		for _, o := range objs {
			if o.Layer == k {
				sum += o.HealthScore
			}
		}
		if l.Count > 0 {
			l.AvgScore = round1(sum / float64(l.Count))
		}
		out = append(out, *l)
	}
	return out
}

func worstStatus(nodes []*FCTreeNode) string {
	weight := map[string]int{"normal": 0, "warning": 1, "critical": 2}
	s := "normal"
	for _, n := range nodes {
		if weight[n.Status] > weight[s] {
			s = n.Status
		}
	}
	return s
}

func countBadNodes(nodes []*FCTreeNode) int {
	c := 0
	for _, n := range nodes {
		if n.Status == "critical" || n.Status == "warning" {
			c++
		}
	}
	return c
}

// buildTreeFromNodes 由服务树节点构建透视树：叶子取对象健康分，父节点异常向上透传。
func buildTreeFromNodes(nodes []database.FiremapNode, objects map[uint]FCObject) *FCTreeNode {
	byParent := map[uint][]database.FiremapNode{}
	byID := map[uint]database.FiremapNode{}
	for _, n := range nodes {
		byID[n.ID] = n
		// 自指/指向不存在父节点的脏数据统一挂到根，避免死循环或节点丢失。
		p := n.ParentID
		if p == n.ID {
			p = 0
		}
		if p != 0 {
			if _, ok := byID[p]; !ok {
				p = 0
			}
		}
		byParent[p] = append(byParent[p], n)
	}
	var build func(parent uint, path map[uint]bool, depth int) []*FCTreeNode
	build = func(parent uint, path map[uint]bool, depth int) []*FCTreeNode {
		if depth >= 8 {
			return nil
		}
		children := append([]database.FiremapNode(nil), byParent[parent]...)
		sort.SliceStable(children, func(i, j int) bool {
			if children[i].SortOrder != children[j].SortOrder {
				return children[i].SortOrder < children[j].SortOrder
			}
			return children[i].ID < children[j].ID
		})
		out := []*FCTreeNode{}
		for _, n := range children {
			if path[n.ID] { // 环路保护
				continue
			}
			nextPath := make(map[uint]bool, len(path)+1)
			for k, v := range path {
				nextPath[k] = v
			}
			nextPath[n.ID] = true
			tn := &FCTreeNode{
				ID: fmt.Sprintf("n%d", n.ID), NodeID: n.ID, Name: n.Name, Level: n.Level,
			}
			kids := build(n.ID, nextPath, depth+1)
			if len(kids) > 0 {
				tn.Children = kids
				tn.Status = worstStatus(kids)
				sum := 0.0
				for _, k := range kids {
					sum += k.HealthScore
				}
				tn.HealthScore = round1(sum / float64(len(kids)))
				tn.Count = fmt.Sprintf("%d/%d", countBadNodes(kids), len(kids))
			} else if o, ok := objects[n.ID]; ok {
				tn.HealthScore = o.HealthScore
				tn.Status = o.Status
				tn.ObjectID = o.ID
				n := 0
				if o.Status != "normal" {
					n = 1
				}
				tn.Count = fmt.Sprintf("%d/1", n)
			} else {
				tn.HealthScore = 100
				tn.Status = "normal"
				tn.Count = "0/0"
			}
			out = append(out, tn)
		}
		return out
	}
	roots := build(0, map[uint]bool{}, 0)
	if len(roots) == 0 {
		return &FCTreeNode{ID: "root", Name: "服务树", Level: fcLevelBusiness,
			Status: "normal", HealthScore: 100, Count: "0/0"}
	}
	if len(roots) == 1 {
		return roots[0]
	}
	root := &FCTreeNode{ID: "root", Name: "全部业务", Level: fcLevelBusiness, Children: roots}
	root.Status = worstStatus(roots)
	sum := 0.0
	for _, r := range roots {
		sum += r.HealthScore
	}
	root.HealthScore = round1(sum / float64(len(roots)))
	root.Count = fmt.Sprintf("%d/%d", countBadNodes(roots), len(roots))
	return root
}

func nodePathFromList(n database.FiremapNode, all []database.FiremapNode) string {
	byID := map[uint]database.FiremapNode{}
	for _, x := range all {
		byID[x.ID] = x
	}
	parts := []string{n.Name}
	cur := n
	for i := 0; i < 8 && cur.ParentID > 0; i++ {
		p, ok := byID[cur.ParentID]
		if !ok {
			break
		}
		parts = append([]string{p.Name}, parts...)
		cur = p
	}
	return strings.Join(parts, " / ")
}

func filterNodesByDatasource(nodes []database.FiremapNode, dsID uint) []database.FiremapNode {
	if dsID == 0 {
		return nodes
	}
	out := []database.FiremapNode{}
	for _, n := range nodes {
		if n.DatasourceID == dsID {
			out = append(out, n)
		}
	}
	return out
}

func parseUintParam(s string) uint {
	s = strings.TrimPrefix(strings.TrimSpace(s), "n")
	v, _ := strconv.ParseUint(s, 10, 32)
	return uint(v)
}

// buildObjectPrompt FlashAI 定向诊断提示词：只给当前对象上下文，明确禁止扩展到全系统。
func buildObjectPrompt(fc *FCContext, checks []FCCheckItem, obj FCObject) string {
	var b strings.Builder
	b.WriteString("你是 PromAI，正在对【" + obj.Path + "】这**一个**观测对象做定向根因诊断。\n")
	b.WriteString("只分析这个对象，不要扩展到其他系统，也不要输出全局巡检报告。\n\n")
	fmt.Fprintf(&b, "## 对象信息\n- 路径：%s\n- 实例：%s\n- 数据源：%s\n- 健康分：%.1f（%s）\n- 活跃告警：%d，巡检指标：%d（异常 %d）\n\n",
		obj.Path, obj.Instance, obj.Datasource, obj.HealthScore, obj.Status,
		len(fc.Alerts), len(fc.Snapshot), abnormalCount(fc.Snapshot))

	b.WriteString("## 检查项结果（眼睛）\n")
	if len(checks) == 0 {
		b.WriteString("- 暂无检查结果\n")
	}
	for _, c := range checks {
		fmt.Fprintf(&b, "- %s [%s] %s %s\n", c.Name, c.Status, c.Result, c.Detail)
	}

	b.WriteString("\n## 相关告警\n")
	if len(fc.Alerts) == 0 {
		b.WriteString("- 无 firing 告警\n")
	}
	for i, a := range fc.Alerts {
		if i >= 10 {
			fmt.Fprintf(&b, "- ...另有 %d 条\n", len(fc.Alerts)-10)
			break
		}
		fmt.Fprintf(&b, "- [%s] value=%.4g threshold=%.4g labels=%s\n", a.Severity, a.Value, a.Threshold, a.LabelsJSON)
	}

	b.WriteString("\n## 巡检指标异常\n")
	n := 0
	for _, m := range fc.Snapshot {
		if m.Status == "normal" {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s) status=%s value=%.4g threshold=%.4g labels=%v\n",
			m.MetricName, m.TypeName, m.Status, m.Value, m.Threshold, m.Labels)
		n++
		if n >= 15 {
			break
		}
	}
	if n == 0 {
		b.WriteString("- 无异常指标\n")
	}

	b.WriteString("\n## 输出要求\n")
	b.WriteString("1) 给出最可能的根因 Top 3，按可能性排序，并说明依据（引用上面的检查项/告警/指标）。\n")
	b.WriteString("2) 每个根因给出可直接执行的排查或处置动作（命令 / 配置 / 数据源查询）。\n")
	b.WriteString("3) 只基于上述上下文；数据不足时明确写「数据不足，建议补充 X」，不要编造指标或结论。\n")
	b.WriteString("4) 输出简洁 Markdown，不要复述本提示词。\n")
	return b.String()
}

// ---------- 数据访问 / 种子 ----------

func loadFiremapNodes() []database.FiremapNode {
	var nodes []database.FiremapNode
	if database.DB == nil {
		return nodes
	}
	database.DB.Order("sort_order asc, id asc").Find(&nodes)
	return nodes
}

// loadFCNodeViews 把原始节点转成表格视图：补 datasource 名称与父节点名称。
func loadFCNodeViews() []FCNodeView {
	nodes := loadFiremapNodes()
	dsNames := map[uint]string{}
	if database.DB != nil {
		var dss []database.DataSource
		database.DB.Select("id, name").Find(&dss)
		for _, d := range dss {
			dsNames[d.ID] = d.Name
		}
	}
	byID := map[uint]database.FiremapNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	out := make([]FCNodeView, 0, len(nodes))
	for _, n := range nodes {
		v := FCNodeView{
			ID: n.ID, ParentID: n.ParentID, Name: n.Name, Level: n.Level,
			DatasourceID: n.DatasourceID, Instance: n.Instance,
			SortOrder: n.SortOrder, Remark: n.Remark,
		}
		v.Datasource = dsNames[n.DatasourceID]
		if v.Datasource == "" {
			v.Datasource = "—"
		}
		if p, ok := byID[n.ParentID]; ok {
			v.ParentName = p.Name
		} else if n.ParentID == 0 {
			v.ParentName = "（根节点）"
		} else {
			v.ParentName = fmt.Sprintf("已失效(%d)", n.ParentID)
		}
		out = append(out, v)
	}
	return out
}

type fcDiscovered struct {
	Instance string
	Layer    string
	Metrics  []MetricHealthData
}

// discoverInstances 从最新巡检快照自动发现实例（仅用于首次构建服务树，之后树由用户维护）。
func discoverInstances(ds database.DataSource) []fcDiscovered {
	snap := getLatestReport(ds.URL)
	if snap == nil {
		return nil
	}
	m := map[string]*fcDiscovered{}
	order := []string{}
	for _, met := range snap.Metrics {
		inst := pickInstance(met.Labels)
		if inst == "" {
			inst = ds.Name
		}
		if _, ok := m[inst]; !ok {
			m[inst] = &fcDiscovered{Instance: inst, Layer: mapMetricTypeToLayer(met.TypeName)}
			order = append(order, inst)
		}
		m[inst].Metrics = append(m[inst].Metrics, met)
	}
	out := make([]fcDiscovered, 0, len(order))
	for _, k := range order {
		out = append(out, *m[k])
	}
	return out
}

// cleanupFiremapNodes 修复历史脏数据：自指节点、环、孤儿、重复节点。
// 旧版自动发现曾反复插入同名节点，且被删除的 business 父节点下还残留
// service/instance 子节点，导致卡片池出现多条「同一路径」的重复行。
// 这里统一按 (数据源, 层级, 名称, 实例) 去重：优先保留父链完整、ID 最小的节点，
// 并把检查结果/SLO/事件引用迁移到保留节点后再删除重复项，避免误伤历史留档。
func cleanupFiremapNodes() {
	if database.DB == nil {
		return
	}
	nodes := loadFiremapNodes()
	byID := map[uint]database.FiremapNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	rootify := func(id uint) {
		database.DB.Model(&database.FiremapNode{}).Where("id = ?", id).Update("parent_id", 0)
		if n, ok := byID[id]; ok {
			n.ParentID = 0
			byID[id] = n
		}
	}
	for _, n := range nodes {
		// 自指或父节点不存在：提升为根，避免建树死循环 / 节点丢失。
		if n.ParentID == n.ID {
			rootify(n.ID)
			continue
		}
		if n.ParentID != 0 {
			if _, ok := byID[n.ParentID]; !ok {
				rootify(n.ID)
				continue
			}
		}
		// 检测父链上的环（含间接自指），命中则断开为根节点。
		cur, seenCycle := n.ParentID, map[uint]bool{n.ID: true}
		for depth := 0; depth < 32 && cur != 0; depth++ {
			if seenCycle[cur] {
				rootify(n.ID)
				break
			}
			seenCycle[cur] = true
			p, ok := byID[cur]
			if !ok {
				break
			}
			cur = p.ParentID
		}
	}

	// 按业务身份去重（忽略 parent_id，因为脏数据的父节点可能已被删除）。
	groups := map[string][]database.FiremapNode{}
	for _, n := range nodes {
		key := fmt.Sprintf("%d|%s|%s|%s", n.DatasourceID, n.Level, n.Name, n.Instance)
		groups[key] = append(groups[key], n)
	}
	better := func(a, b database.FiremapNode) bool {
		// 父链完整的节点优先；都一样则保留最早创建的（ID 最小）。
		_, aHasParent := byID[a.ParentID]
		_, bHasParent := byID[b.ParentID]
		if a.ParentID != 0 && aHasParent != (b.ParentID != 0 && bHasParent) {
			return a.ParentID != 0 && aHasParent
		}
		return a.ID < b.ID
	}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		keep := g[0]
		for _, n := range g[1:] {
			if better(n, keep) {
				keep = n
			}
		}
		for _, n := range g {
			if n.ID == keep.ID {
				continue
			}
			// 历史留档迁移到保留节点，避免外键悬空。
			database.DB.Model(&database.FiremapCheckResult{}).Where("node_id = ?", n.ID).Update("node_id", keep.ID)
			database.DB.Model(&database.FiremapSLO{}).Where("node_id = ?", n.ID).Update("node_id", keep.ID)
			database.DB.Model(&database.FiremapEvent{}).Where("node_id = ?", n.ID).Update("node_id", keep.ID)
			database.DB.Where("id = ?", n.ID).Delete(&database.FiremapNode{})
			delete(byID, n.ID)
		}
	}
}

// ensureDefaultFiremapNodes 首次访问时生成一棵可编辑的服务树。
// firemapSeededKey 标记服务树已完成过首次自动构建。
// 一旦置位，即使用户把节点全部删空也不再自动重新 seed —— 服务树由用户维护。
const firemapSeededKey = "firemap_seeded"

func firemapSeeded() bool {
	if database.DB == nil {
		return false
	}
	var s database.AppSetting
	if database.DB.Where("key = ?", firemapSeededKey).First(&s).Error != nil {
		return false
	}
	return s.Value == "true"
}

func markFiremapSeeded() {
	if database.DB == nil {
		return
	}
	database.DB.Where("key = ?", firemapSeededKey).FirstOrCreate(&database.AppSetting{Key: firemapSeededKey}).Update("value", "true")
}

func ensureDefaultFiremapNodes() {
	if database.DB == nil {
		return
	}
	cleanupFiremapNodes()
	// 只做一次首次构建；构建过（含用户主动删空）后不再自动 seed。
	if firemapSeeded() {
		return
	}
	var cnt int64
	database.DB.Model(&database.FiremapNode{}).Count(&cnt)
	if cnt > 0 {
		// 已有节点但缺标记（旧版本升级上来）：补写标记，避免用户删空后被重新 seed。
		markFiremapSeeded()
		return
	}
	seedFiremapNodes(false)
	markFiremapSeeded()
}

// fcNodeCreatesCycle 判断把 nodeID 的父节点设为 parentID 是否会形成环。
func fcNodeCreatesCycle(nodeID, parentID uint, all []database.FiremapNode) bool {
	if nodeID == 0 || parentID == 0 || nodeID == parentID {
		return nodeID != 0 && nodeID == parentID
	}
	byID := map[uint]database.FiremapNode{}
	for _, n := range all {
		byID[n.ID] = n
	}
	cur := parentID
	for depth := 0; depth < 32 && cur != 0; depth++ {
		if cur == nodeID {
			return true
		}
		n, ok := byID[cur]
		if !ok {
			return false
		}
		cur = n.ParentID
	}
	return false
}

// findOrCreateFCNode 按 (datasource_id, parent_id, level, name, instance) 幂等取节点。
// 存在则直接复用（用户重命名/改层级后的节点不会再被重复创建），不存在才插入。
func findOrCreateFCNode(n database.FiremapNode) (database.FiremapNode, bool) {
	var exist database.FiremapNode
	err := database.DB.Where("datasource_id = ? AND parent_id = ? AND level = ? AND name = ? AND instance = ?",
		n.DatasourceID, n.ParentID, n.Level, n.Name, n.Instance).First(&exist).Error
	if err == nil {
		return exist, false
	}
	if e := database.DB.Create(&n).Error; e != nil {
		return database.FiremapNode{}, false
	}
	return n, true
}

// seedFiremapNodes 从巡检快照补齐服务树。force=true 仅表示"允许重新扫描补齐缺失节点"，
// 不再无条件重复插入——这是修复卡片池同名节点刷屏的关键。
func seedFiremapNodes(force bool) (int, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("数据库未就绪")
	}
	var dss []database.DataSource
	database.DB.Where("enabled = ?", true).Find(&dss)
	if len(dss) == 0 {
		database.DB.Find(&dss)
	}
	created := 0
	for _, ds := range dss {
		// 非 force 模式：该数据源已有任何节点就跳过（首次初始化路径）。
		if !force {
			var exist int64
			database.DB.Model(&database.FiremapNode{}).Where("datasource_id = ?", ds.ID).Count(&exist)
			if exist > 0 {
				continue
			}
		}
		bizName := strings.TrimSpace(ds.ProjectName)
		if bizName == "" {
			bizName = ds.Name
		}
		root, ok := findOrCreateFCNode(database.FiremapNode{
			ParentID: 0, Name: bizName, Level: fcLevelBusiness,
			DatasourceID: ds.ID, SortOrder: 0,
			Remark: "由巡检数据自动发现生成，可重命名/调整层级",
		})
		if ok {
			created++
		}

		addLeaf := func(parentID uint, name, instance string, sort int) {
			if _, createdNow := findOrCreateFCNode(database.FiremapNode{
				ParentID: parentID, Name: name, Level: fcLevelInstance,
				DatasourceID: ds.ID, Instance: instance, SortOrder: sort,
			}); createdNow {
				created++
			}
		}

		insts := discoverInstances(ds)
		if len(insts) == 0 {
			svc, createdNow := findOrCreateFCNode(database.FiremapNode{
				ParentID: root.ID, Name: "默认服务", Level: fcLevelService,
				DatasourceID: ds.ID, SortOrder: 0,
			})
			if createdNow {
				created++
			}
			addLeaf(svc.ID, ds.Name, ds.Name, 0)
			continue
		}

		byLayer := map[string][]fcDiscovered{}
		for _, it := range insts {
			byLayer[it.Layer] = append(byLayer[it.Layer], it)
		}
		order := 0
		layerNames := []string{"业务接口", "微服务", "中间件", "基础设施", "未分组"}
		for _, ln := range append(layerNames, mapKeysSorted(byLayer)...) {
			group := byLayer[ln]
			if len(group) == 0 {
				continue
			}
			svc, createdNow := findOrCreateFCNode(database.FiremapNode{
				ParentID: root.ID, Name: ln, Level: fcLevelService,
				DatasourceID: ds.ID, SortOrder: order,
			})
			if createdNow {
				created++
			}
			order++
			for i, it := range group {
				addLeaf(svc.ID, it.Instance, it.Instance, i)
			}
			delete(byLayer, ln)
		}
	}
	return created, nil
}

func mapKeysSorted(m map[string][]fcDiscovered) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func snapshotForInstance(ds database.DataSource, instance string) []MetricHealthData {
	snap := getLatestReport(ds.URL)
	if snap == nil {
		return nil
	}
	out := []MetricHealthData{}
	for _, m := range snap.Metrics {
		pi := pickInstance(m.Labels)
		if pi == "" {
			pi = ds.Name
		}
		if instance == "" || pi == instance {
			out = append(out, m)
		}
	}
	return out
}

func firingAlertsForInstance(ds database.DataSource, instance string) []database.AlertInstance {
	var all []database.AlertInstance
	if database.DB == nil {
		return nil
	}
	database.DB.Where("state = ?", "firing").Order("id desc").Limit(200).Find(&all)
	out := []database.AlertInstance{}
	for _, a := range all {
		if ds.ID != 0 && a.DatasourceID != ds.ID {
			continue
		}
		if instance == "" {
			out = append(out, a)
			continue
		}
		var labels map[string]string
		_ = json.Unmarshal([]byte(a.LabelsJSON), &labels)
		if labels["instance"] == instance || pickInstance(labels) == instance {
			out = append(out, a)
		}
	}
	return out
}

func latestChecksForNode(nodeID uint) ([]database.FiremapCheckResult, *time.Time) {
	var raw []database.FiremapCheckResult
	if database.DB == nil {
		return nil, nil
	}
	database.DB.Where("node_id = ?", nodeID).Order("id desc").Limit(200).Find(&raw)
	seen := map[string]bool{}
	checks := []database.FiremapCheckResult{}
	var at *time.Time
	for _, c := range raw {
		if seen[c.CheckKey] {
			continue
		}
		seen[c.CheckKey] = true
		checks = append(checks, c)
		if at == nil || c.CreatedAt.After(*at) {
			t := c.CreatedAt
			at = &t
		}
	}
	return checks, at
}

func loadFiremapCheckConfigs() []database.FlashAICheckConfig {
	var cfgs []database.FlashAICheckConfig
	if database.DB != nil {
		if err := database.DB.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&cfgs).Error; err == nil && len(cfgs) > 0 {
			return cfgs
		}
	}
	return defaultFiremapCheckConfigs
}

// ensureDefaultCheckConfigs 迁移旧版种子（只有 key 没有诊断动作）并补齐新版种子。
func ensureDefaultCheckConfigs() {
	if database.DB == nil {
		return
	}
	var existing []database.FlashAICheckConfig
	database.DB.Find(&existing)
	legacy := map[string]bool{"firemap": true, "report": true, "logsearch": true,
		"tracing": true, "dashboard": true, "event": true}
	onlyLegacy := len(existing) > 0
	for _, e := range existing {
		if !legacy[e.Key] {
			onlyLegacy = false
			break
		}
	}
	if onlyLegacy {
		database.DB.Where("id > ?", 0).Delete(&database.FlashAICheckConfig{})
		existing = nil
	}
	have := map[string]bool{}
	for _, e := range existing {
		have[e.Key] = true
	}
	for _, c := range defaultFiremapCheckConfigs {
		if have[c.Key] {
			continue
		}
		cc := c
		cc.ID = 0
		database.DB.Create(&cc)
	}
}

func ensureDefaultSLOs() {
	if database.DB == nil {
		return
	}
	var cnt int64
	database.DB.Model(&database.FiremapSLO{}).Count(&cnt)
	if cnt > 0 {
		return
	}
	var roots []database.FiremapNode
	database.DB.Where("level = ?", fcLevelBusiness).Order("id asc").Find(&roots)
	nodeID := uint(0)
	if len(roots) > 0 {
		nodeID = roots[0].ID
	}
	for _, s := range defaultFiremapSLOs {
		ss := s
		ss.ID = 0
		ss.NodeID = nodeID
		database.DB.Create(&ss)
	}
}

func buildFCContext(nodeID uint) (*FCContext, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	var node database.FiremapNode
	if err := database.DB.First(&node, nodeID).Error; err != nil {
		return nil, fmt.Errorf("服务树节点不存在: %w", err)
	}
	var ds database.DataSource
	_ = database.DB.First(&ds, node.DatasourceID).Error
	checks, _ := latestChecksForNode(nodeID)
	all := loadFiremapNodes()
	return &FCContext{
		Node: node, DS: ds, Path: nodePathFromList(node, all),
		Snapshot:     snapshotForInstance(ds, node.Instance),
		Alerts:       firingAlertsForInstance(ds, node.Instance),
		LatestChecks: checks,
	}, nil
}

func buildFCObject(n database.FiremapNode, ds database.DataSource, serviceNames map[uint]string, all []database.FiremapNode) FCObject {
	layer := serviceNames[n.ParentID]
	if layer == "" {
		layer = "未分组"
	}
	snap := snapshotForInstance(ds, n.Instance)
	alerts := firingAlertsForInstance(ds, n.Instance)
	checks, checkAt := latestChecksForNode(n.ID)
	score := calcObjectHealth(checks, len(alerts), abnormalCount(snap))
	status := scoreStatus(score, countStatus(checks, "err"))
	return FCObject{
		ID: fmt.Sprintf("n%d", n.ID), NodeID: n.ID, Name: n.Name,
		Path: nodePathFromList(n, all), Level: n.Level, ParentID: n.ParentID,
		DatasourceID: n.DatasourceID, Datasource: ds.Name, Instance: n.Instance,
		Layer: layer, HealthScore: score, Status: status,
		TotalChecks: len(checks), Critical: countStatus(checks, "err"),
		Warning: countStatus(checks, "warn"), Normal: countStatus(checks, "ok"),
		Alerts: len(alerts), Metrics: len(snap),
		NoData: len(checks) == 0 && len(snap) == 0, LastCheckAt: checkAt,
	}
}

func recalcNodeHealth(nodeID uint) (float64, string) {
	var n database.FiremapNode
	if database.DB == nil || database.DB.First(&n, nodeID).Error != nil {
		return 100, "normal"
	}
	var ds database.DataSource
	database.DB.First(&ds, n.DatasourceID)
	snap := snapshotForInstance(ds, n.Instance)
	alerts := firingAlertsForInstance(ds, n.Instance)
	checks, _ := latestChecksForNode(nodeID)
	s := calcObjectHealth(checks, len(alerts), abnormalCount(snap))
	return s, scoreStatus(s, countStatus(checks, "err"))
}

func nodeHealthTrend(nodeID uint, current float64) []float64 {
	var raw []database.FiremapCheckResult
	if database.DB == nil {
		return []float64{current}
	}
	database.DB.Where("node_id = ?", nodeID).Order("id asc").Limit(300).Find(&raw)
	byRound := map[string][]database.FiremapCheckResult{}
	order := []string{}
	for _, r := range raw {
		k := r.CreatedAt.Format("2006-01-02 15:04")
		if _, ok := byRound[k]; !ok {
			order = append(order, k)
		}
		byRound[k] = append(byRound[k], r)
	}
	points := []float64{}
	for _, k := range order {
		points = append(points, calcObjectHealth(byRound[k], 0, 0))
	}
	if len(points) == 0 {
		return []float64{current}
	}
	if len(points) > 7 {
		points = points[len(points)-7:]
	}
	return points
}

// ---------- 检查项执行 ----------

func queryPromScalar(ctx context.Context, ds database.DataSource, expr string) (*float64, string, error) {
	client, err := prometheus.DefaultCache.Get(ds.ID, ds.URL, ds.Username, ds.Password)
	if err != nil {
		return nil, "", err
	}
	val, _, err := client.API.Query(ctx, expr, time.Now())
	if err != nil {
		return nil, "", err
	}
	if vec, ok := val.(model.Vector); ok && len(vec) > 0 {
		f := float64(vec[0].Value)
		return &f, fmt.Sprintf("%.4g", f), nil
	}
	return nil, "", nil
}

// runOneCheck 执行一个检查项的真实取证动作。
func runOneCheck(ctx context.Context, cfg database.FlashAICheckConfig, fc *FCContext) FCCheckItem {
	start := time.Now()
	it := FCCheckItem{Key: cfg.Key, Name: cfg.Name, CheckType: cfg.CheckType}
	var value *float64
	count := 0
	hasData := true
	detail := ""
	typ := cfg.CheckType
	if typ == "" {
		typ = "snapshot"
	}
	switch typ {
	case "promql":
		if strings.TrimSpace(cfg.PromQL) == "" || fc.DS.URL == "" {
			hasData = false
			detail = "未配置 PromQL 或数据源"
		} else {
			expr := injectInstanceMatcher(cfg.PromQL, fc.Node.Instance)
			if expr == strings.TrimSpace(cfg.PromQL) && !strings.Contains(cfg.PromQL, "instance=") {
				hasData = false
				detail = "函数/聚合式 PromQL 需手动写入 {instance=\"" + fc.Node.Instance + "\"} 过滤"
			} else {
				v, raw, err := queryPromScalar(ctx, fc.DS, expr)
				if err != nil {
					hasData = false
					detail = "查询失败: " + err.Error()
				} else if v == nil {
					hasData = false
					detail = "PromQL 无返回序列: " + expr
				} else {
					value = v
					detail = expr + " → " + raw
				}
			}
		}
	case "alert":
		count = len(fc.Alerts)
		detail = fmt.Sprintf("该实例 firing 告警 %d 条", count)
	case "snapshot":
		count = abnormalCount(fc.Snapshot)
		detail = fmt.Sprintf("巡检快照 %d 项指标，异常 %d 项", len(fc.Snapshot), count)
	case "slo":
		b := calcErrorBudget(calcObjectHealth(nil, len(fc.Alerts), abnormalCount(fc.Snapshot)), 99.9)
		value = &b
		detail = "按当前对象健康分估算错误预算"
	case "ai":
		detail = "由 /ai-analyze 定向执行"
	case "log":
		hasData = false
		detail = "未配置日志数据源"
	default:
		hasData = false
		detail = "未知检查类型"
	}
	st, rs := evaluateCheckResult(cfg, value, count, hasData)
	it.Status, it.Result, it.Detail = st, rs, detail
	it.CostMs = time.Since(start).Milliseconds()
	return it
}

func executeChecks(ctx context.Context, fc *FCContext) []FCCheckItem {
	cfgs := loadFiremapCheckConfigs()
	items := make([]FCCheckItem, 0, len(cfgs))
	for _, c := range cfgs {
		items = append(items, runOneCheck(ctx, c, fc))
	}
	if database.DB != nil {
		now := time.Now()
		for _, it := range items {
			database.DB.Create(&database.FiremapCheckResult{
				NodeID: fc.Node.ID, CheckKey: it.Key, Status: it.Status,
				Result: it.Result, Detail: it.Detail, CostMs: it.CostMs, CreatedAt: now,
			})
			if it.Status == "err" {
				database.DB.Create(&database.FiremapEvent{
					NodeID: fc.Node.ID, Source: "check", Severity: "warning",
					Title: fmt.Sprintf("%s：%s", it.Name, it.Result), Detail: it.Detail,
					Status: "open", CreatedAt: now,
				})
			}
		}
		// 每次检查落一条健康分快照，供时间轴回放（灭火图"时间轴回溯历史状态"）。
		score, status := recalcNodeHealth(fc.Node.ID)
		critical, warning := 0, 0
		for _, it := range items {
			if it.Status == "err" {
				critical++
			}
			if it.Status == "warn" {
				warning++
			}
		}
		database.DB.Create(&database.FiremapHealthSnapshot{
			NodeID: fc.Node.ID, HealthScore: score, Status: status,
			Critical: critical, Warning: warning, Normal: len(items) - critical - warning,
			Alerts: len(fc.Alerts), CreatedAt: now,
		})
		// 告警规则：飘红时按条件触发并记录事件。
		fireAlertRules(fc.Node.ID, score, status, now)
	}
	return items
}

// fireAlertRules 检查灭火图告警规则，命中则落事件并更新 LastFiredAt。
func fireAlertRules(nodeID uint, score float64, status string, now time.Time) {
	if database.DB == nil {
		return
	}
	var rules []database.FiremapAlertRule
	database.DB.Where("node_id = ? AND enabled = ?", nodeID, true).Find(&rules)
	for _, rule := range rules {
		hit := false
		switch rule.Condition {
		case "critical":
			hit = status == "critical"
		case "any_abnormal":
			hit = status == "critical" || status == "warning"
		case "score_below":
			hit = score < rule.ScoreBelow
		}
		if !hit {
			continue
		}
		// 5 分钟内不重复触发同一规则，避免刷屏
		if rule.LastFiredAt != nil && now.Sub(*rule.LastFiredAt) < 5*time.Minute {
			continue
		}
		database.DB.Model(&database.FiremapAlertRule{}).Where("id = ?", rule.ID).Update("last_fired_at", now)
		database.DB.Create(&database.FiremapEvent{
			NodeID: nodeID, Source: "alert", Severity: "critical",
			Title:  fmt.Sprintf("灭火图告警：%s", rule.Name),
			Detail: fmt.Sprintf("触发条件=%s 当前健康分=%.1f 状态=%s 通知=%s", rule.Condition, score, status, rule.NotifyTarget),
			Status: "open", CreatedAt: now,
		})
	}
}

// ---------- HTTP handlers ----------

func (a *AdminAPI) handleFiremapObjects(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeJSON(w, FCBootstrap{HasData: false, Message: "数据库未就绪"})
		return
	}
	ensureDefaultFiremapNodes()
	ensureDefaultCheckConfigs()

	nodes := loadFiremapNodes()
	dsID, _ := strconv.ParseUint(r.URL.Query().Get("datasource_id"), 10, 32)

	var dss []database.DataSource
	database.DB.Find(&dss)
	dsMap := map[uint]database.DataSource{}
	options := []FCDatasource{}
	for _, d := range dss {
		dsMap[d.ID] = d
		options = append(options, FCDatasource{ID: d.ID, Name: d.Name})
	}
	serviceNames := map[uint]string{}
	for _, n := range nodes {
		if n.Level == fcLevelService {
			serviceNames[n.ID] = n.Name
		}
	}

	objects := []FCObject{}
	objMap := map[uint]FCObject{}
	for _, n := range nodes {
		if n.Level != fcLevelInstance {
			continue
		}
		if dsID > 0 && n.DatasourceID != uint(dsID) {
			continue
		}
		o := buildFCObject(n, dsMap[n.DatasourceID], serviceNames, nodes)
		objects = append(objects, o)
		objMap[n.ID] = o
	}
	sorted := sortFCObjects(objects)

	stats := FCStats{Total: len(sorted)}
	sum := 0.0
	for _, o := range sorted {
		sum += o.HealthScore
		switch o.Status {
		case "critical":
			stats.Critical++
		case "warning":
			stats.Warning++
		default:
			stats.Normal++
		}
		if o.NoData {
			stats.NoData++
		}
	}
	overall := 100.0
	if len(sorted) > 0 {
		overall = round1(sum / float64(len(sorted)))
	}

	var lastReport database.ReportRecord
	var lastAt *time.Time
	// 只要最新报告时间，不读 metrics_json（单条可达 ~670KB）
	if database.DB.Model(&database.ReportRecord{}).Select("created_at").
		Order("created_at desc").First(&lastReport).Error == nil {
		t := lastReport.CreatedAt
		lastAt = &t
	}

	msg := ""
	if len(sorted) == 0 {
		msg = "服务树暂无实例节点，可在「卡片池」里添加，或先在「数据源管理」触发一次巡检后调用 /firemap/seed 自动发现。"
	}
	writeJSON(w, FCBootstrap{
		HasData: len(sorted) > 0, OverallHealth: overall,
		Objects: sorted, Layers: buildLayersFromObjects(sorted),
		Tree:  buildTreeFromNodes(filterNodesByDatasource(nodes, uint(dsID)), objMap),
		Stats: stats, LastReportAt: lastAt, Datasources: options, Message: msg,
	})
}

func (a *AdminAPI) handleFiremapObjectDetail(w http.ResponseWriter, r *http.Request) {
	nodeID := parseUintParam(r.URL.Query().Get("node"))
	if nodeID == 0 {
		nodeID = parseUintParam(r.URL.Query().Get("object"))
	}
	if nodeID == 0 {
		writeError(w, 400, "缺少 node 参数")
		return
	}
	fc, err := buildFCContext(nodeID)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	obj := buildFCObject(fc.Node, fc.DS, serviceNameMap(loadFiremapNodes()), loadFiremapNodes())
	alerts := make([]map[string]any, 0, len(fc.Alerts))
	for _, al := range fc.Alerts {
		alerts = append(alerts, map[string]any{
			"id": al.ID, "severity": al.Severity, "state": al.State,
			"value": al.Value, "threshold": al.Threshold, "labels": al.LabelsJSON,
			"created_at": al.CreatedAt,
		})
	}
	// 该对象最近一次 FlashAI 定向诊断结论（刷新抽屉后仍可回看）
	var aiRec database.AiAnalysisRecord
	conclusion, modelName, conclusionAt := "", "", ""
	if database.DB.Where("type = ? AND ref_id LIKE ?", "firemap_object",
		fmt.Sprintf("fcnode_%d_", nodeID)+"%").Order("id desc").First(&aiRec).Error == nil {
		conclusion, modelName = aiRec.Result, aiRec.ModelName
		conclusionAt = aiRec.CreatedAt.Format(time.RFC3339)
	}
	writeJSON(w, map[string]any{
		"object": obj, "path": fc.Path, "alerts": alerts,
		"metrics": fc.Snapshot, "checks": fc.LatestChecks,
		"conclusion": conclusion, "conclusion_model": modelName, "conclusion_at": conclusionAt,
	})
}

func serviceNameMap(nodes []database.FiremapNode) map[uint]string {
	m := map[uint]string{}
	for _, n := range nodes {
		if n.Level == fcLevelService {
			m[n.ID] = n.Name
		}
	}
	return m
}

func (a *AdminAPI) handleFiremapChecks(w http.ResponseWriter, r *http.Request) {
	nodeID := parseUintParam(r.URL.Query().Get("node"))
	if nodeID == 0 {
		nodeID = parseUintParam(r.URL.Query().Get("object"))
	}
	if nodeID == 0 {
		writeError(w, 400, "缺少 node 参数")
		return
	}
	fc, err := buildFCContext(nodeID)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	items := executeChecks(r.Context(), fc)
	score, status := recalcNodeHealth(nodeID)
	writeJSON(w, map[string]any{
		"checks": items, "health_score": score, "status": status,
		"path": fc.Path, "node_id": nodeID,
	})
}

func (a *AdminAPI) handleFiremapAIAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "仅支持 POST")
		return
	}
	var req struct {
		NodeID uint   `json:"node_id"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if req.NodeID == 0 {
		req.NodeID = parseUintParam(r.URL.Query().Get("node"))
	}
	fc, err := buildFCContext(req.NodeID)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	// 眼睛：先跑一轮定向检查，把真实结果交给 AI
	checks := executeChecks(r.Context(), fc)
	obj := buildFCObject(fc.Node, fc.DS, serviceNameMap(loadFiremapNodes()), loadFiremapNodes())
	prompt := buildObjectPrompt(fc, checks, obj)
	if strings.TrimSpace(req.Prompt) != "" {
		prompt += "\n\n## 用户补充\n" + strings.TrimSpace(req.Prompt)
	}

	if piagent.DefaultAgentHandler == nil || !piagent.DefaultAgentHandler.AIEnabled() {
		writeError(w, 503, "AI 未启用或未配置模型，无法执行定向诊断")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	res, aiErr := piagent.DefaultAgentHandler.RunHeadless(ctx, prompt, 0)

	refID := fmt.Sprintf("fcnode_%d_%d", req.NodeID, time.Now().Unix())
	rec := database.AiAnalysisRecord{
		Type: "firemap_object", RefID: refID, Prompt: prompt,
		Status: "success", CreatedAt: time.Now(),
	}
	text := ""
	modelName := ""
	if aiErr != nil {
		rec.Status = "failed"
		rec.Error = aiErr.Error()
	} else if res != nil {
		text = res.Text
		modelName = res.ModelName
		rec.Result = res.Text
		rec.ModelName = res.ModelName
		rec.DurationMs = res.Duration.Milliseconds()
	}
	if database.DB != nil {
		database.DB.Create(&rec)
		title := "PromAI 完成 " + fc.Node.Name + " 定向诊断"
		if aiErr != nil {
			title = "PromAI 定向诊断失败：" + fc.Node.Name
		}
		database.DB.Create(&database.FiremapEvent{
			NodeID: req.NodeID, Source: "ai", Severity: "info",
			Title: title, Detail: truncate(text, 300), Status: "open",
			RefID: refID, CreatedAt: time.Now(),
		})
	}
	if aiErr != nil {
		writeJSON(w, map[string]any{
			"success": false, "error": aiErr.Error(), "ref_id": refID,
			"prompt": prompt, "checks": checks,
		})
		return
	}
	writeJSON(w, map[string]any{
		"success": true, "text": text, "model": modelName,
		"ref_id": refID, "at": time.Now(), "checks": checks,
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *AdminAPI) handleFiremapPolaris(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeJSON(w, map[string]any{"items": []FCPolarisItem{}, "overall": 100})
		return
	}
	ensureDefaultSLOs()
	var slos []database.FiremapSLO
	database.DB.Where("enabled = ?", true).Order("sort_order asc, id asc").Find(&slos)
	items := make([]FCPolarisItem, 0, len(slos))
	budgetSum := 0.0
	for _, s := range slos {
		score, _ := recalcNodeHealth(s.NodeID)
		budget := calcErrorBudget(score, s.Target)
		st := "normal"
		if budget < 25 {
			st = "critical"
		} else if budget < 60 {
			st = "warning"
		}
		items = append(items, FCPolarisItem{
			ID: s.ID, NodeID: s.NodeID, Name: s.Name, Description: s.Description,
			Target: s.Target, Current: score, ErrorBudget: budget,
			Status: st, WindowDays: s.WindowDays,
			Trend: nodeHealthTrend(s.NodeID, score),
		})
		budgetSum += budget
	}
	overall := 100.0
	if len(items) > 0 {
		overall = round1(budgetSum / float64(len(items)))
	}
	writeJSON(w, map[string]any{"items": items, "overall": overall})
}

func (a *AdminAPI) handleFiremapEvents(w http.ResponseWriter, r *http.Request) {
	events := []FCEventItem{}
	if database.DB == nil {
		writeJSON(w, map[string]any{"events": events, "total": 0})
		return
	}

	// 时间范围筛选：hours 为相对快捷（1/6/24/168），since/until 为显式区间（优先）。
	q := r.URL.Query()
	var since, until time.Time
	if t, ok := parseEventWallTime(q.Get("since")); ok {
		since = t
	}
	if t, ok := parseEventWallTime(q.Get("until")); ok {
		until = t
	}
	if since.IsZero() {
		if h, _ := strconv.Atoi(q.Get("hours")); h > 0 {
			since = time.Now().Add(-time.Duration(h) * time.Hour)
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 300
	}

	nodes := loadFiremapNodes()
	names := map[uint]string{}
	for _, n := range nodes {
		names[n.ID] = n.Name
	}

	// 场景事件：按时间范围过滤，不再用固定 Limit(100) 把区间内的数据挤掉
	evQ := database.DB.Model(&database.FiremapEvent{})
	if !since.IsZero() {
		evQ = evQ.Where("created_at >= ?", since)
	}
	if !until.IsZero() {
		evQ = evQ.Where("created_at <= ?", until)
	}
	var evs []database.FiremapEvent
	evQ.Order("created_at desc").Limit(limit).Find(&evs)
	for _, e := range evs {
		events = append(events, FCEventItem{
			ID: fmt.Sprintf("ev%d", e.ID), NodeID: e.NodeID, NodeName: names[e.NodeID],
			Source: e.Source, Severity: e.Severity, Title: e.Title,
			Detail: e.Detail, Status: e.Status, At: e.CreatedAt,
		})
	}

	// 实时 firing 告警也进事件墙（不依赖检查是否跑过），同样套用时间范围
	alQ := database.DB.Model(&database.AlertInstance{}).Where("state = ?", "firing")
	if !since.IsZero() {
		alQ = alQ.Where("created_at >= ?", since)
	}
	if !until.IsZero() {
		alQ = alQ.Where("created_at <= ?", until)
	}
	var alerts []database.AlertInstance
	alQ.Order("created_at desc").Limit(limit).Find(&alerts)
	for _, al := range alerts {
		var labels map[string]string
		_ = json.Unmarshal([]byte(al.LabelsJSON), &labels)
		inst := pickInstance(labels)
		if inst == "" {
			inst = al.GroupKey
		}
		events = append(events, FCEventItem{
			ID: fmt.Sprintf("al%d", al.ID), NodeID: 0, NodeName: inst,
			Source: "alert", Severity: al.Severity,
			Title:  fmt.Sprintf("告警触发：%s", inst),
			Detail: fmt.Sprintf("value=%.4g threshold=%.4g labels=%s", al.Value, al.Threshold, al.LabelsJSON),
			Status: "open", At: al.CreatedAt,
		})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > limit {
		events = events[:limit]
	}
	writeJSON(w, map[string]any{
		"events": events,
		"total":  len(events),
		"since":  nullOrTimeStr(since),
		"until":  nullOrTimeStr(until),
	})
}

// parseEventWallTime 兼容前端传来的多种时间格式（本地时间字符串 / ISO8601）。
func parseEventWallTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05", "2006-01-02T15:04:05",
		"2006-01-02 15:04", "2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func nullOrTimeStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

func (a *AdminAPI) handleFiremapNodes(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeError(w, 503, "数据库未就绪")
		return
	}
	// 卡片池每次读取都做幂等自愈：清理历史重复/孤儿节点，避免同名路径刷屏。
	ensureDefaultFiremapNodes()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"nodes": loadFCNodeViews(),
			"datasources": func() []FCDatasource {
				var dss []database.DataSource
				database.DB.Select("id, name").Order("id asc").Find(&dss)
				out := make([]FCDatasource, 0, len(dss))
				for _, d := range dss {
					out = append(out, FCDatasource{ID: d.ID, Name: d.Name})
				}
				return out
			}(),
		})
	case http.MethodPost:
		var n database.FiremapNode
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			writeError(w, 400, "解析失败: "+err.Error())
			return
		}
		if strings.TrimSpace(n.Name) == "" {
			writeError(w, 400, "名称不能为空")
			return
		}
		if n.Level == "" {
			n.Level = fcLevelInstance
		}
		if n.Level != fcLevelBusiness && n.Level != fcLevelService && n.Level != fcLevelInstance {
			writeError(w, 400, "层级只能是 business/service/instance")
			return
		}
		all := loadFiremapNodes()
		if fcNodeCreatesCycle(n.ID, n.ParentID, all) {
			writeError(w, 400, "父节点不能是自己或自己的后代（会形成环）")
			return
		}
		// 层级与父节点一致性：实例必须有服务父节点，服务必须有业务父节点
		if n.Level == fcLevelInstance && n.ParentID == 0 {
			writeError(w, 400, "实例节点必须挂在某个服务下，不能作为根节点")
			return
		}
		if n.Level == fcLevelService && n.ParentID == 0 {
			writeError(w, 400, "服务节点必须挂在某个业务下，不能作为根节点")
			return
		}
		if n.Level == fcLevelBusiness {
			n.ParentID = 0
		}
		// 同层同名判重
		for _, x := range all {
			if x.ID == n.ID || x.ParentID != n.ParentID || x.Level != n.Level || x.Name != n.Name {
				continue
			}
			writeError(w, 409, "同级下已存在同名节点："+n.Name)
			return
		}
		if n.ID > 0 {
			database.DB.Model(&database.FiremapNode{}).Where("id = ?", n.ID).
				Updates(map[string]any{"name": n.Name, "parent_id": n.ParentID,
					"level": n.Level, "instance": n.Instance, "datasource_id": n.DatasourceID,
					"sort_order": n.SortOrder, "remark": n.Remark})
		} else {
			database.DB.Create(&n)
		}
		writeJSON(w, map[string]any{"success": true, "node": n})
	case http.MethodDelete:
		id := parseUintParam(r.URL.Query().Get("id"))
		if id == 0 {
			writeError(w, 400, "缺少 id")
			return
		}
		// 删除节点及其子树（最多 3 层）
		ids := []uint{id}
		all := loadFiremapNodes()
		for depth := 0; depth < 4; depth++ {
			added := false
			for _, n := range all {
				for _, pid := range ids {
					if n.ParentID == pid && !containsUint(ids, n.ID) {
						ids = append(ids, n.ID)
						added = true
					}
				}
			}
			if !added {
				break
			}
		}
		database.DB.Where("id IN ?", ids).Delete(&database.FiremapNode{})
		writeJSON(w, map[string]any{"success": true, "deleted": len(ids)})
	default:
		writeError(w, 405, "不支持的方法")
	}
}

func containsUint(list []uint, v uint) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (a *AdminAPI) handleFiremapSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "仅支持 POST")
		return
	}
	created, err := seedFiremapNodes(true)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	markFiremapSeeded()
	writeJSON(w, map[string]any{"success": true, "created": created,
		"message": "已从巡检数据自动发现实例并补齐服务树节点，可在卡片池里重命名/调整层级"})
}

func (a *AdminAPI) handleFiremapCheckConfig(w http.ResponseWriter, r *http.Request) {
	if database.DB == nil {
		writeError(w, 503, "数据库未就绪")
		return
	}
	ensureDefaultCheckConfigs()
	switch r.Method {
	case http.MethodGet:
		var cfgs []database.FlashAICheckConfig
		database.DB.Order("sort_order asc, id asc").Find(&cfgs)
		writeJSON(w, map[string]any{"configs": cfgs})
	case http.MethodPost:
		var c database.FlashAICheckConfig
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeError(w, 400, "解析失败: "+err.Error())
			return
		}
		if strings.TrimSpace(c.Key) == "" || strings.TrimSpace(c.Name) == "" {
			writeError(w, 400, "key 与 name 不能为空")
			return
		}
		if c.CheckType == "" {
			c.CheckType = "snapshot"
		}
		if c.ID > 0 {
			database.DB.Model(&database.FlashAICheckConfig{}).Where("id = ?", c.ID).
				Updates(map[string]any{"key": c.Key, "name": c.Name, "enabled": c.Enabled,
					"sort_order": c.SortOrder, "check_type": c.CheckType, "promql": c.PromQL,
					"threshold": c.Threshold, "compare_op": c.CompareOp, "unit": c.Unit, "remark": c.Remark})
		} else {
			database.DB.Create(&c)
		}
		writeJSON(w, map[string]any{"success": true, "config": c})
	case http.MethodDelete:
		id := parseUintParam(r.URL.Query().Get("id"))
		if id == 0 {
			writeError(w, 400, "缺少 id")
			return
		}
		database.DB.Delete(&database.FlashAICheckConfig{}, id)
		writeJSON(w, map[string]any{"success": true})
	default:
		writeError(w, 405, "不支持的方法")
	}
}

func (a *AdminAPI) registerFiremapHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/promai/firemap/objects", a.logRequest(a.authMiddleware(a.handleFiremapObjects)))
	mux.HandleFunc("/api/promai/firemap/object", a.logRequest(a.authMiddleware(a.handleFiremapObjectDetail)))
	mux.HandleFunc("/api/promai/firemap/checks", a.logRequest(a.authMiddleware(a.handleFiremapChecks)))
	mux.HandleFunc("/api/promai/firemap/ai-analyze", a.logRequest(a.authMiddleware(a.handleFiremapAIAnalyze)))
	mux.HandleFunc("/api/promai/firemap/check-config", a.logRequest(a.authMiddleware(a.handleFiremapCheckConfig)))
	mux.HandleFunc("/api/promai/firemap/nodes", a.logRequest(a.authMiddleware(a.handleFiremapNodes)))
	mux.HandleFunc("/api/promai/firemap/polaris", a.logRequest(a.authMiddleware(a.handleFiremapPolaris)))
	mux.HandleFunc("/api/promai/firemap/events", a.logRequest(a.authMiddleware(a.handleFiremapEvents)))
	mux.HandleFunc("/api/promai/firemap/seed", a.logRequest(a.authMiddleware(a.handleFiremapSeed)))
}
