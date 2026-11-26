package main

import (
	"strings"
	"testing"

	"PromAI/pkg/database"
)

func TestCalcHealthScore(t *testing.T) {
	cases := []struct {
		name                  string
		total, critical, warn int
		want                  float64
	}{
		{"空指标视为健康", 0, 0, 0, 100.0},
		{"全正常", 10, 0, 0, 100.0},
		{"一个严重", 10, 1, 0, 90.0},
		{"一个警告", 10, 0, 1, 90.0},
		{"严重+警告", 10, 1, 2, 70.0},
		{"全异常", 4, 4, 0, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := calcHealthScore(c.total, c.critical, c.warn); got != c.want {
				t.Fatalf("calcHealthScore(%d,%d,%d)=%.2f, want %.2f", c.total, c.critical, c.warn, got, c.want)
			}
		})
	}
}

func TestScoreStatus(t *testing.T) {
	cases := []struct {
		score    float64
		critical int
		want     string
	}{
		{100, 0, "normal"},
		{95, 0, "normal"},
		{94.9, 0, "warning"},
		{85, 0, "warning"},
		{69.9, 0, "critical"},
		{100, 1, "critical"},
	}
	for _, c := range cases {
		if got := scoreStatus(c.score, c.critical); got != c.want {
			t.Fatalf("scoreStatus(%.1f,%d)=%s, want %s", c.score, c.critical, got, c.want)
		}
	}
}

func TestPickInstance(t *testing.T) {
	cases := []struct {
		labels map[string]string
		want   string
	}{
		{map[string]string{"instance": "10.20.1.9:6379", "job": "redis"}, "10.20.1.9:6379"},
		{map[string]string{"nodename": "node-01", "region": "cn-north"}, "node-01"},
		{map[string]string{"pod": "order-svc-abc", "instance": ""}, "order-svc-abc"},
		{map[string]string{"instance": "-", "host": "web-02"}, "web-02"},
		{map[string]string{"job": "mysql"}, ""},
	}
	for _, c := range cases {
		if got := pickInstance(c.labels); got != c.want {
			t.Fatalf("pickInstance(%v)=%q, want %q", c.labels, got, c.want)
		}
	}
}

func TestMapMetricTypeToLayer(t *testing.T) {
	cases := []struct {
		typeName string
		want     string
	}{
		{"MySQL集群", "中间件"}, {"Redis缓存", "中间件"}, {"Kafka队列", "中间件"},
		{"JVM应用", "微服务"}, {"容器服务", "微服务"},
		{"业务接口", "业务接口"}, {"主机资源", "基础设施"}, {"CPU监控", "基础设施"},
		{"自定义指标", "未分组"},
	}
	for _, c := range cases {
		if got := mapMetricTypeToLayer(c.typeName); got != c.want {
			t.Fatalf("mapMetricTypeToLayer(%q)=%q, want %q", c.typeName, got, c.want)
		}
	}
}

func TestRound1(t *testing.T) {
	if got := round1(93.333); got != 93.3 {
		t.Fatalf("round1(93.333)=%.1f, want 93.3", got)
	}
	if got := round1(100); got != 100 {
		t.Fatalf("round1(100)=%.1f, want 100", got)
	}
}

// TestCalcObjectHealth 新健康分：检查项 + 告警 + 巡检异常指标加权扣分，不是 (total-alerts)/total。
func TestCalcObjectHealth(t *testing.T) {
	checks := []database.FiremapCheckResult{
		{Status: "err"}, {Status: "warn"}, {Status: "ok"},
	}
	// 18 + 6 + 1*10 + 2*4 = 42 -> 58
	if got := calcObjectHealth(checks, 1, 2); got != 58 {
		t.Fatalf("calcObjectHealth=%v, want 58", got)
	}
	if got := calcObjectHealth(nil, 0, 0); got != 100 {
		t.Fatalf("无异常应为 100，实际 %v", got)
	}
	many := make([]database.FiremapCheckResult, 10)
	for i := range many {
		many[i].Status = "err"
	}
	if got := calcObjectHealth(many, 5, 5); got != 0 {
		t.Fatalf("扣分应被夹到 0，实际 %v", got)
	}
}

func TestCalcErrorBudget(t *testing.T) {
	// target 99.9（允许 0.1% 不可用），score 99.95（实际 0.05%）→ 剩余 50%
	if got := calcErrorBudget(99.95, 99.9); got != 50 {
		t.Fatalf("calcErrorBudget=%v, want 50", got)
	}
	if got := calcErrorBudget(100, 99.9); got != 100 {
		t.Fatalf("满分应剩余 100，实际 %v", got)
	}
	if got := calcErrorBudget(99.5, 99.9); got != 0 {
		t.Fatalf("超预算应夹到 0，实际 %v", got)
	}
}

func TestInjectInstanceMatcher(t *testing.T) {
	cases := []struct {
		expr, inst, want string
	}{
		{"up", "10.0.0.1:9100", `up{instance="10.0.0.1:9100"}`},
		{`http_requests_total{job="api"}`, "h1", `http_requests_total{instance="h1",job="api"}`},
		{`up{}`, "h1", `up{instance="h1"}`},
		{"rate(http_requests_total[5m])", "h1", "rate(http_requests_total[5m])"}, // 函数式不注入，避免非法 PromQL
		{"sum by(job) (rate(foo[5m]))", "h1", "sum by(job) (rate(foo[5m]))"},
		{"", "h1", ""},
		{"up", "", "up"},
	}
	for _, c := range cases {
		if got := injectInstanceMatcher(c.expr, c.inst); got != c.want {
			t.Fatalf("injectInstanceMatcher(%q,%q)=%q, want %q", c.expr, c.inst, got, c.want)
		}
	}
}

// TestBuildTreeFromNodes 异常向上透传：叶子 critical → 服务节点 → 业务根节点均为 critical。
func TestBuildTreeFromNodes(t *testing.T) {
	nodes := []database.FiremapNode{
		{ID: 1, ParentID: 0, Name: "交易业务", Level: fcLevelBusiness},
		{ID: 2, ParentID: 1, Name: "中间件", Level: fcLevelService},
		{ID: 3, ParentID: 2, Name: "redis-01", Level: fcLevelInstance},
		{ID: 4, ParentID: 2, Name: "redis-02", Level: fcLevelInstance},
	}
	objects := map[uint]FCObject{
		3: {ID: "n3", Name: "redis-01", HealthScore: 40, Status: "critical"},
		4: {ID: "n4", Name: "redis-02", HealthScore: 100, Status: "normal"},
	}
	tree := buildTreeFromNodes(nodes, objects)
	if tree.NodeID != 1 || tree.Status != "critical" {
		t.Fatalf("业务根节点应透传 critical: %+v", tree)
	}
	if tree.HealthScore != 70 {
		t.Fatalf("父节点分数应为子节点均值 70，实际 %.1f", tree.HealthScore)
	}
	if len(tree.Children) != 1 || tree.Children[0].Status != "critical" {
		t.Fatalf("服务节点应透传 critical: %+v", tree.Children)
	}
	svc := tree.Children[0]
	if len(svc.Children) != 2 || svc.Count != "1/2" {
		t.Fatalf("服务节点统计错误: %+v", svc)
	}
	if svc.Children[0].ObjectID != "n3" || svc.Children[1].ObjectID != "n4" {
		t.Fatalf("叶子应绑定 object_id: %+v", svc.Children)
	}
	if countBadNodes(svc.Children) != 1 {
		t.Fatal("countBadNodes 应为 1")
	}
	if worstStatus(svc.Children) != "critical" {
		t.Fatal("worstStatus 应为 critical")
	}
}

func TestBuildLayersAndSort(t *testing.T) {
	objs := []FCObject{
		{Name: "a", Layer: "中间件", HealthScore: 40, Status: "critical"},
		{Name: "b", Layer: "中间件", HealthScore: 100, Status: "normal"},
		{Name: "c", Layer: "微服务", HealthScore: 80, Status: "warning"},
	}
	sorted := sortFCObjects(objs)
	if sorted[0].Name != "a" {
		t.Fatalf("异常对象应排最前，实际 %s", sorted[0].Name)
	}
	layers := buildLayersFromObjects(sorted)
	if len(layers) != 2 {
		t.Fatalf("应有 2 层，实际 %d", len(layers))
	}
	for _, l := range layers {
		if l.Name == "中间件" && (l.Count != 2 || l.Critical != 1 || l.Normal != 1 || l.AvgScore != 70) {
			t.Fatalf("中间件层汇总错误: %+v", l)
		}
	}
}

func TestNodePathFromList(t *testing.T) {
	nodes := []database.FiremapNode{
		{ID: 1, ParentID: 0, Name: "交易业务"},
		{ID: 2, ParentID: 1, Name: "中间件"},
		{ID: 3, ParentID: 2, Name: "redis-01"},
	}
	if got := nodePathFromList(nodes[2], nodes); got != "交易业务 / 中间件 / redis-01" {
		t.Fatalf("路径错误: %q", got)
	}
	if got := filterNodesByDatasource(nodes, 7); len(got) != 0 {
		t.Fatalf("按数据源过滤应为空，实际 %d", len(got))
	}
	if got := filterNodesByDatasource(nodes, 0); len(got) != 3 {
		t.Fatalf("dsID=0 应返回全部，实际 %d", len(got))
	}
}

func TestBuildObjectPrompt(t *testing.T) {
	fc := &FCContext{
		Path: "交易业务 / 中间件 / redis-01",
		Snapshot: []MetricHealthData{
			{MetricName: "redis_up", TypeName: "Redis缓存", Status: "critical", Value: 0},
		},
		Alerts: []database.AlertInstance{{Severity: "critical", Value: 0, Threshold: 1}},
	}
	checks := []FCCheckItem{{Key: "instance_up", Name: "实例存活", Status: "err", Result: "0 触发阈值 lt 1"}}
	obj := FCObject{Path: fc.Path, Instance: "10.0.0.1:6379", Datasource: "生产Prometheus",
		HealthScore: 58, Status: "critical", Alerts: 1, Metrics: 1}
	p := buildObjectPrompt(fc, checks, obj)
	for _, want := range []string{"redis-01", "10.0.0.1:6379", "实例存活", "redis_up", "不要扩展到其他系统", "数据不足"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt 缺少 %q\n%s", want, p)
		}
	}
	if strings.Contains(p, "全量巡检报告") && !strings.Contains(p, "不要输出全局巡检报告") {
		t.Fatal("prompt 未禁止全量巡检语义")
	}
}

func TestFCNodeCreatesCycle(t *testing.T) {
	all := []database.FiremapNode{
		{ID: 1, ParentID: 0, Name: "biz", Level: "business"},
		{ID: 2, ParentID: 1, Name: "svc", Level: "service"},
		{ID: 3, ParentID: 2, Name: "inst", Level: "instance"},
	}
	cases := []struct {
		name       string
		id, parent uint
		want       bool
	}{
		{"自指", 2, 2, true},
		{"父是后代", 2, 3, true},
		{"正常挂父", 3, 2, false},
		{"新建挂根", 0, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fcNodeCreatesCycle(c.id, c.parent, all); got != c.want {
				t.Fatalf("fcNodeCreatesCycle(%d,%d)=%v, want %v", c.id, c.parent, got, c.want)
			}
		})
	}
}

func TestBuildTreeFromNodesNoInfiniteRecursion(t *testing.T) {
	// 自指节点 + 环：必须在有限时间内返回而不是栈溢出
	nodes := []database.FiremapNode{
		{ID: 1, ParentID: 0, Name: "biz", Level: "business"},
		{ID: 2, ParentID: 2, Name: "self", Level: "service"},
		{ID: 3, ParentID: 4, Name: "a", Level: "service"},
		{ID: 4, ParentID: 3, Name: "b", Level: "service"},
	}
	tree := buildTreeFromNodes(nodes, map[uint]FCObject{})
	if tree == nil {
		t.Fatal("tree is nil")
	}
	if tree.Name != "全部业务" && tree.Name != "服务树" {
		t.Fatalf("unexpected root name: %s", tree.Name)
	}
}
