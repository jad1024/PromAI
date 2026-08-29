package main

import (
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

func mkLabels(a, b string) string { return `{"resource":"` + a + `","cluster":"` + b + `"}` }

func TestDedupSameFingerprintRepeatFiring(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	rows := []database.AlertHistory{
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 85, mkLabels("node-1", "a"), base),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 92, mkLabels("node-1", "a"), base.Add(3*time.Minute)),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "critical", 98, mkLabels("node-1", "a"), base.Add(6*time.Minute)),
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
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 85, mkLabels("node-1", "a"), base),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "resolved", "warning", 30, mkLabels("node-1", "a"), base.Add(10*time.Minute)),
		histRow("fp-1", 1, 1, "CPU高", "cluster-a", "firing", "warning", 88, mkLabels("node-1", "a"), base.Add(30*time.Minute)),
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

func TestCrossClusterSameResourceSameIncident(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(2 * time.Minute),
			PeakValue: 88, Threshold: 80, Labels: map[string]string{"resource": "node-1", "cluster": "a"},
		},
		{
			Fingerprint: "fp-b", RuleID: 1, RuleName: "CPU高", DatasourceID: 2, DatasourceName: "cluster-b",
			Severity: "warning", State: "ongoing", FirstFiredAt: base.Add(1 * time.Minute), LastEventAt: base.Add(3 * time.Minute),
			PeakValue: 90, Threshold: 80, Labels: map[string]string{"resource": "node-1", "cluster": "b"},
		},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 1 {
		t.Fatalf("跨集群相同 alertname+resource 应归入同一故障，实际 %d 个", len(incs))
	}
	inc := incs[0]
	if inc.AlertCount != 2 {
		t.Errorf("AlertCount 应为 2，实际 %d", inc.AlertCount)
	}
	if len(inc.Datasources) != 2 || inc.Datasources[0] != "cluster-a" || inc.Datasources[1] != "cluster-b" {
		t.Errorf("Datasources 应包含两个集群，实际 %v", inc.Datasources)
	}
	if len(inc.Alerts) != 2 {
		t.Errorf("下钻 alerts 应为 2 条，实际 %d", len(inc.Alerts))
	}
}

func TestWindowSlicing(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "resolved", FirstFiredAt: base, LastEventAt: base.Add(5 * time.Minute),
			PeakValue: 85, Threshold: 80, Labels: map[string]string{"resource": "node-1"},
		},
		{
			Fingerprint: "fp-b", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base.Add(30 * time.Minute), LastEventAt: base.Add(31 * time.Minute),
			PeakValue: 86, Threshold: 80, Labels: map[string]string{"resource": "node-1"},
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
			PeakValue: 200, Threshold: 100, Labels: map[string]string{"resource": "db-1", "pod": "p" + string(rune('a'+i))},
		})
	}
	cfg := denoiseConfig{WindowMinutes: 60, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 1 {
		t.Fatalf("同一窗口内应合并为 1 个故障，实际 %d", len(incs))
	}
	if !incs[0].Storm {
		t.Error("告警数 12 > 阈值 10，应打风暴标记")
	}
}

func TestDifferentResourceDifferentIncident(t *testing.T) {
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
			Labels: map[string]string{"resource": "node-1"},
		},
		{
			Fingerprint: "fp-b", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
			Labels: map[string]string{"resource": "node-2"},
		},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 2 {
		t.Fatalf("不同 resource 应拆分为 2 个故障，实际 %d", len(incs))
	}
}

func TestMultiResourceLabels(t *testing.T) {
	// 配置多个 resource 标签时，按 CSV 顺序取第一个非空值（优先级）
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	a := alertInstance{
		Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
		Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
		Labels: map[string]string{"instance": "10.0.0.1:9100", "pod": "app-abc"},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"pod", "instance"}}
	incs := aggregateIncidents([]alertInstance{a}, cfg)
	if len(incs) != 1 {
		t.Fatalf("应产生 1 个故障")
	}
	if incs[0].Resource != "app-abc" {
		t.Errorf("resource 应按优先级取 pod=app-abc，实际 %q", incs[0].Resource)
	}
}

func TestResourceFallbackToInstance(t *testing.T) {
	// 默认配置只有 resource 标签，而规则通常只带 instance —— 应回退到 instance，
	// 避免同 alertname 的告警全部糊成一个大故障
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	alerts := []alertInstance{
		{
			Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
			Labels: map[string]string{"instance": "10.0.0.1:9100"},
		},
		{
			Fingerprint: "fp-b", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
			Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
			Labels: map[string]string{"instance": "10.0.0.2:9100"},
		},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents(alerts, cfg)
	if len(incs) != 2 {
		t.Fatalf("无 resource 标签时应回退 instance 拆分故障，实际 %d 个", len(incs))
	}
	if incs[0].Resource != "10.0.0.1:9100" || incs[1].Resource != "10.0.0.2:9100" {
		t.Errorf("回退后的 resource 应为 instance 值，实际 %q / %q", incs[0].Resource, incs[1].Resource)
	}
}

func TestResourceMissingFallbackEmpty(t *testing.T) {
	// 配置的与回退链都不命中时，退化为仅按 alertname 聚合（resource=""）
	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	a := alertInstance{
		Fingerprint: "fp-a", RuleID: 1, RuleName: "CPU高", DatasourceID: 1, DatasourceName: "cluster-a",
		Severity: "warning", State: "ongoing", FirstFiredAt: base, LastEventAt: base.Add(1 * time.Minute),
		Labels: map[string]string{"namespace": "default"},
	}
	cfg := denoiseConfig{WindowMinutes: 10, StormThreshold: 10, ResourceLabels: []string{"resource"}}
	incs := aggregateIncidents([]alertInstance{a}, cfg)
	if len(incs) != 1 {
		t.Fatalf("应产生 1 个故障")
	}
	if incs[0].Resource != "" {
		t.Errorf("resource 应为空串，实际 %q", incs[0].Resource)
	}
}
