package main

import (
	"encoding/json"
	"testing"
	"time"

	"PromAI/pkg/database"
)

// 构造一条 AlertHistory 事件
func histRow(fp string, ruleID, dsID uint, rule, ds, state, sev string, v float64, labels string, t time.Time) database.AlertHistory {
	return database.AlertHistory{
		Fingerprint:    fp,
		RuleID:         ruleID,
		RuleName:       rule,
		DatasourceID:   dsID,
		DatasourceName: ds,
		State:          state,
		Severity:       sev,
		Value:          v,
		LabelsJSON:     labels,
		EventType:      state,
		OccurredAt:     t,
	}
}

func mkInstanceLabels(instance, cluster string) string {
	return `{"instance":"` + instance + `","cluster":"` + cluster + `"}`
}

func TestDedupSameFingerprintRepeatFiring(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	rows := []database.AlertHistory{
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 85, mkInstanceLabels("node-1", "a"), base),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 92, mkInstanceLabels("node-1", "a"), base.Add(3*time.Minute)),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "critical", 98, mkInstanceLabels("node-1", "a"), base.Add(6*time.Minute)),
	}
	alerts := dedupToAlerts(rows)
	if len(alerts) != 1 {
		t.Fatalf("同一 fingerprint 连续重发应去重为 1 条告警，实际 %d", len(alerts))
	}
	a := alerts[0]
	if a.State != "ongoing" {
		t.Errorf("未恢复告警 state 应为 ongoing，实际 %s", a.State)
	}
	if a.PeakValue != 98 {
		t.Errorf("PeakValue 应取峰值 98，实际 %v", a.PeakValue)
	}
	if a.Severity != "critical" {
		t.Errorf("Severity 应升级为 critical，实际 %s", a.Severity)
	}
	if a.FirstFiredAt != base || a.LastEventAt != base.Add(6*time.Minute) {
		t.Errorf("时间范围错误: %v ~ %v", a.FirstFiredAt, a.LastEventAt)
	}
}

func TestResolvedThenFiringCreatesNewAlert(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	rows := []database.AlertHistory{
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 85, mkInstanceLabels("node-1", "a"), base),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "resolved", "warning", 30, mkInstanceLabels("node-1", "a"), base.Add(10*time.Minute)),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 88, mkInstanceLabels("node-1", "a"), base.Add(30*time.Minute)),
	}
	alerts := dedupToAlerts(rows)
	if len(alerts) != 2 {
		t.Fatalf("firing→resolved→firing 应产生 2 条告警，实际 %d", len(alerts))
	}
	if alerts[0].State != "resolved" {
		t.Errorf("第 1 条应为 resolved，实际 %s", alerts[0].State)
	}
	if alerts[1].State != "ongoing" {
		t.Errorf("第 2 条应为 ongoing，实际 %s", alerts[1].State)
	}
	if !alerts[1].FirstFiredAt.Equal(base.Add(30 * time.Minute)) {
		t.Errorf("第 2 条 FirstFiredAt 应为恢复后再次触发时间，实际 %v", alerts[1].FirstFiredAt)
	}
}

func TestAggregateByAlertnameOnly(t *testing.T) {
	// 关键模型验证：同一 alertname 多个实例归一故障，跨集群也归一
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(2 * time.Minute),
			PeakValue: 88, Threshold: 80, Labels: map[string]string{"instance": "10.0.0.1:9100", "cluster": "a"},
		},
		{
			Fingerprint: "fp-b", RuleID: 1, RuleName: "CPU高", DatasourceID: 2, DatasourceName: "cluster-b",
			Severity: "warning", State: "ongoing", FirstFiredAt: base.Add(1 * time.Minute), LastEventAt: base.Add(3 * time.Minute),
			PeakValue: 90, Threshold: 80, Labels: map[string]string{"instance": "10.0.0.2:9100", "cluster": "b"},
		},
		{
			Fingerprint: "fp-c", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "critical", State: "ongoing", FirstFiredAt: base.Add(2 * time.Minute), LastEventAt: base.Add(4 * time.Minute),
			PeakValue: 95, Threshold: 80, Labels: map[string]string{"instance": "10.0.0.3:9100", "cluster": "a"},
		},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 1 {
		t.Fatalf("同 alertname 多实例应归 1 个故障，实际 %d", len(incs))
	}
	inc := incs[0]
	if inc.AlertCount != 3 {
		t.Errorf("AlertCount 应为 3，实际 %d", inc.AlertCount)
	}
	if inc.InstanceCount != 3 {
		t.Errorf("InstanceCount 应为 3（10.0.0.1/2/3），实际 %d", inc.InstanceCount)
	}
	if inc.ClusterCount != 2 {
		t.Errorf("ClusterCount 应为 2（cluster-a/b），实际 %d", inc.ClusterCount)
	}
	if len(inc.Datasources) != 2 {
		t.Errorf("Datasources 应列出 2 个集群，实际 %v", inc.Datasources)
	}
	if inc.Severity != "critical" {
		t.Errorf("Severity 应升级为 critical，实际 %s", inc.Severity)
	}
	if len(inc.Alerts) != 3 {
		t.Errorf("下钻 alerts 应为 3 条，实际 %d", len(inc.Alerts))
	}
}

func TestDifferentAlertnameDifferentIncident(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
			Labels: map[string]string{"instance": "10.0.0.1:9100"},
		},
		{
			Fingerprint: "fp-b", RuleID: 2, RuleName: "内存高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
			Labels: map[string]string{"instance": "10.0.0.1:9100"},
		},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 2 {
		t.Fatalf("不同 alertname 应拆分为 2 个故障，实际 %d", len(incs))
	}
}

func TestWindowSlicing(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "resolved", FirstFiredAt: base, LastEventAt: base.Add(5 * time.Minute),
			Labels: map[string]string{"instance": "10.0.0.1:9100"},
		},
		{
			Fingerprint: "fp-b", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base.Add(30 * time.Minute), LastEventAt: base.Add(31 * time.Minute),
			Labels: map[string]string{"instance": "10.0.0.1:9100"},
		},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 2 {
		t.Fatalf("间隔超过窗口应切成 2 个故障，实际 %d", len(incs))
	}
	// 窗口为 0：不切窗，全部合并
	cfg0 := denoiseConfig{WindowMinutes: 0, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs0 := aggregateIncidents(alerts, cfg0)
	if len(incs0) != 1 {
		t.Fatalf("window=0 表示不切窗，应合并为 1 个故障，实际 %d", len(incs0))
	}
}

func TestStormFlag(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	var alerts []alertInstance
	for i := 0; i < 12; i++ {
		alerts = append(alerts, alertInstance{
			Fingerprint: "fp-" + string(rune('a'+i)), RuleID: 1, RuleName: "连接数高", DatasourceID: uint(1 + i),
			DatasourceName: "cluster", Severity: "warning", State: "ongoing",
			FirstFiredAt: base.Add(time.Duration(i) * time.Minute), LastEventAt: base.Add(time.Duration(i+1) * time.Minute),
			PeakValue: 200, Threshold: 100,
			Labels: map[string]string{"instance": "10.0.0." + string(rune('1'+i)) + ":9100"},
		})
	}
	cfg := denoiseConfig{WindowMinutes: 60, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 1 {
		t.Fatalf("同一窗口同 alertname 应合并为 1 个故障，实际 %d", len(incs))
	}
	if !incs[0].Storm {
		t.Error("告警数 12 > 阈值 10，应打风暴标记")
	}
	if incs[0].InstanceCount != 12 {
		t.Errorf("InstanceCount 应为 12，实际 %d", incs[0].InstanceCount)
	}
}

func TestExtractInstancePriority(t *testing.T) {
	// 优先级：resource > instance
	labels := map[string]string{"resource": "db-prod", "instance": "10.0.0.1:9100"}
	got := extractInstance(labels, []string{"resource", "instance"})
	if got != "db-prod" {
		t.Errorf("应按顺序取 resource=db-prod，实际 %q", got)
	}
	// 优先级：pod > instance（pod 缺失，回退 instance）
	labels = map[string]string{"instance": "10.0.0.1:9100"}
	got = extractInstance(labels, []string{"pod", "instance"})
	if got != "10.0.0.1:9100" {
		t.Errorf("应回退到 instance，实际 %q", got)
	}
	// 全缺失
	labels = map[string]string{"namespace": "default"}
	got = extractInstance(labels, []string{"resource"})
	if got != "" {
		t.Errorf("应为空串，实际 %q", got)
	}
}

func TestIncidentHashKeyStable(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	k1 := incidentHashKey("CPU高", base)
	k2 := incidentHashKey("CPU高", base)
	if k1 != k2 {
		t.Errorf("相同输入应产生相同 hash，实际 %s vs %s", k1, k2)
	}
	k3 := incidentHashKey("内存高", base)
	if k1 == k3 {
		t.Error("不同 alertname 应产生不同 hash")
	}
	k4 := incidentHashKey("CPU高", base.Add(time.Minute))
	if k1 == k4 {
		t.Error("不同 firstFiredAt 应产生不同 hash")
	}
}

func TestIncidentDetailFullJSONTags(t *testing.T) {
	// 回归：确保 incidentDetailFull 序列化字段为小写（下钻抽屉不能 undefined）
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	inc := incidentDetailFull{
		Key:          "abc123",
		Alertname:    "CPU高",
		Severity:     "warning",
		State:        "ongoing",
		AlertCount:   5,
		InstanceSet:  map[string]struct{}{},
		Alerts:       []alertInIncident{},
		FirstFiredAt: base,
		LastEventAt:  base.Add(5 * time.Minute),
		Datasources:  []string{"cluster-a"},
	}
	b, err := json.Marshal(inc)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	for _, k := range []string{"key", "alertname", "severity", "state", "alert_count", "alerts", "first_fired_at", "last_event_at", "datasources"} {
		if _, ok := got[k]; !ok {
			t.Errorf("JSON 输出应包含小写字段 %q，实际: %s", k, string(b))
		}
	}
	if _, ok := got["InstanceSet"]; ok {
		t.Error("InstanceSet 不应出现在 JSON 输出中")
	}
}

func TestCollectIncidentFingerprints(t *testing.T) {
	// 删除故障：按 key 匹配，收集故障内所有去重指纹
	incs := []incidentDetailFull{
		{
			Key: "key-1", Alertname: "CPU高",
			Alerts: []alertInIncident{
				{Fingerprint: "fp-a"},
				{Fingerprint: "fp-b"},
				{Fingerprint: "fp-a"}, // 同指纹去重
			},
		},
		{
			Key: "key-2", Alertname: "内存高",
			Alerts: []alertInIncident{
				{Fingerprint: "fp-c"},
			},
		},
	}
	fps, matched := collectIncidentFingerprints(incs, []string{"key-1"})
	if matched != 1 {
		t.Fatalf("应匹配 1 个故障，实际 %d", matched)
	}
	if len(fps) != 2 || fps[0] != "fp-a" || fps[1] != "fp-b" {
		t.Errorf("应收集去重后 [fp-a fp-b]，实际 %v", fps)
	}
	// 多 key
	fps2, matched2 := collectIncidentFingerprints(incs, []string{"key-1", "key-2"})
	if matched2 != 2 || len(fps2) != 3 {
		t.Errorf("多 key 应匹配 2 故障 3 指纹，实际 matched=%d fps=%v", matched2, fps2)
	}
	// 不存在的 key
	_, matched3 := collectIncidentFingerprints(incs, []string{"nope"})
	if matched3 != 0 {
		t.Errorf("不存在的 key 应匹配 0，实际 %d", matched3)
	}
}

func TestRemovedAtFilterInDedup(t *testing.T) {
	// 软删语义：removed 的 fingerprint 不应再产出告警。
	// dedupToAlerts 本身接收行数据（过滤在 SQL 层），这里验证被过滤后
	// 聚合结果为空——模拟"手动删除实时告警 → 聚合消失"。
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	rows := []database.AlertHistory{
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 85, mkInstanceLabels("node-1", "a"), base),
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(dedupToAlerts(rows), cfg)
	if len(incs) != 1 {
		t.Fatalf("删除前应有 1 个故障，实际 %d", len(incs))
	}
	// 模拟 removed_at 过滤：行被过滤掉（SQL 层 WHERE removed_at IS NULL）
	incsRemoved := aggregateIncidents(dedupToAlerts(nil), cfg)
	if len(incsRemoved) != 0 {
		t.Fatalf("删除后故障应为空，实际 %d", len(incsRemoved))
	}
}

// TestDismissedIncidentReappearsOnNewEvent 验证事件聚合软 dismiss 语义：
// 1) 旧 AlertHistory 标记 dismissed_at 后，聚合查询过滤后该故障消失
// 2) 新告警事件到来（AlertHistory 新增一行，无 dismissed_at），dedup 取最新
//    行后该故障自动重新出现
// 3) 已恢复告警的 dismissed 行始终被过滤，达成彻底隐藏
func TestDismissedIncidentReappearsOnNewEvent(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	cfg := denoiseConfig{WindowMinutes: 60, StormThreshold: 10, ResourceLabels: []string{"resource"}}

	// 场景 A: 用户删除一个 firing 故障 → 后续新告警事件让故障重新出现
	dismissT := base.Add(10 * time.Minute)
	newEventT := base.Add(20 * time.Minute)
	rowsA := []database.AlertHistory{
		histRow("fp-A", 1, 1, "Mysql主从延迟", "cluster-a", "firing", "critical", 100, mkInstanceLabels("node-1", "a"), base),
	}
	// dismissT 之前：1 个故障
	if got := len(aggregateIncidents(dedupToAlerts(rowsA), cfg)); got != 1 {
		t.Fatalf("A1: 预期 1 个故障，实际 %d", got)
	}
	// 用户点击删除：标记 dismissed_at，SQL 层过滤后该行不再参与聚合 → 0 个故障
	rowA0 := rowsA[0]
	rowA0.DismissedAt = &dismissT
	afterDismiss := aggregateIncidents(dedupToAlerts(filterDismissed([]database.AlertHistory{rowA0})), cfg)
	if got := len(afterDismiss); got != 0 {
		t.Fatalf("A2: 预期 dismiss 后 0 个故障，实际 %d", got)
	}
	// 新告警事件到来：插入一条新的 AlertHistory（无 dismissed_at）
	rowA1 := histRow("fp-A", 1, 1, "Mysql主从延迟", "cluster-a", "firing", "critical", 110, mkInstanceLabels("node-1", "a"), newEventT)
	rowsARefreshed := []database.AlertHistory{rowA0, rowA1}
	reappear := aggregateIncidents(dedupToAlerts(filterDismissed(rowsARefreshed)), cfg)
	if got := len(reappear); got != 1 {
		t.Fatalf("A3: 预期新事件后故障重新出现为 1，实际 %d", got)
	}

	// 场景 B: 用户删除一个已恢复告警的故障 → 旧 dismissed 行永远不再出现
	resolveT := base.Add(5 * time.Minute)
	rowsB := []database.AlertHistory{
		histRow("fp-B", 2, 2, "CPU高", "cluster-b", "firing", "warning", 90, mkInstanceLabels("node-2", "b"), base),
		histRow("fp-B", 2, 2, "CPU高", "cluster-b", "resolved", "warning", 30, mkInstanceLabels("node-2", "b"), resolveT),
	}
	// dismiss 之前：故障已结束、存在
	if got := len(aggregateIncidents(dedupToAlerts(rowsB), cfg)); got != 1 {
		t.Fatalf("B1: 预期 1 个故障，实际 %d", got)
	}
	// dismiss 之后：过滤 dismissed 行
	rowB0 := rowsB[0]
	rowB0.DismissedAt = &dismissT
	rowB1 := rowsB[1]
	rowB1.DismissedAt = &dismissT
	b2 := aggregateIncidents(dedupToAlerts(filterDismissed([]database.AlertHistory{rowB0, rowB1})), cfg)
	if got := len(b2); got != 0 {
		t.Fatalf("B2: 预期已恢复且 dismiss 后 0 个故障，实际 %d", got)
	}
	// 没有新告警事件（已恢复），故障始终不出现
	b3 := aggregateIncidents(dedupToAlerts(filterDismissed([]database.AlertHistory{rowB0, rowB1})), cfg)
	if got := len(b3); got != 0 {
		t.Fatalf("B3: 预期已恢复告警 dismiss 后永远不出现，实际 %d", got)
	}
}

// filterDismissed 模拟 SQL 层 `WHERE dismissed_at IS NULL` 过滤
func filterDismissed(rows []database.AlertHistory) []database.AlertHistory {
	out := make([]database.AlertHistory, 0, len(rows))
	for _, r := range rows {
		if r.DismissedAt == nil {
			out = append(out, r)
		}
	}
	return out
}
