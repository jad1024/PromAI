package lts

import (
	"strings"
	"testing"

	"PromAI/pkg/alerting/webhook"
	"PromAI/pkg/database"
)

func TestFoldJavaLogs_MultilineMerge(t *testing.T) {
	lines := []string{
		"2024-08-29 14:55:08.123 [ERROR] com.acme.OrderService - order create failed",
		"java.sql.SQLException: ORA-00001 unique constraint violated",
		"\tat com.acme.OrderService.create(OrderService.java:120)",
		"\tat org.springframework.jdbc.core.JdbcTemplate.update(JdbcTemplate.java:870)",
		"\tat sun.reflect.NativeMethodAccessorImpl.invoke0(Native Method)",
		"2024-08-29 14:55:09.000 [INFO] com.acme.OrderService - order created",
	}
	fr := FoldJavaLogs(lines, "ERROR,FATAL")
	if fr.TotalLines != 6 {
		t.Fatalf("TotalLines=%d, want 6", fr.TotalLines)
	}
	if fr.MergedLines != 2 {
		t.Fatalf("MergedLines=%d, want 2", fr.MergedLines)
	}
	// ERROR 级别过滤后只剩 1 个模式
	if len(fr.Patterns) != 1 {
		t.Fatalf("Patterns=%d, want 1 (INFO filtered)", len(fr.Patterns))
	}
	p := fr.Patterns[0]
	if p.Level != "ERROR" {
		t.Fatalf("Level=%q, want ERROR", p.Level)
	}
	if p.Logger != "com.acme.OrderService" {
		t.Fatalf("Logger=%q, want com.acme.OrderService", p.Logger)
	}
	// 堆栈折叠：框架帧折叠
	if !contains(p.Signature, "SQLException") {
		t.Fatalf("Signature missing exception type: %q", p.Signature)
	}
	if !contains(p.Signature, "<framework>") {
		t.Fatalf("Signature missing framework folding: %q", p.Signature)
	}
}

func TestFoldJavaLogs_VariableNormalization(t *testing.T) {
	lines := []string{
		"2024-08-29 14:55:08.123 [ERROR] com.acme.Api - request from 10.0.12.34 id=a1b2c3d4-e5f6-7890-abcd-ef1234567890 took 1234ms",
		"2024-08-29 14:55:09.000 [ERROR] com.acme.Api - request from 10.0.12.35 id=11111111-2222-3333-4444-555555555555 took 9999ms",
	}
	fr := FoldJavaLogs(lines, "")
	if len(fr.Patterns) != 1 {
		t.Fatalf("Patterns=%d, want 1 (变量归一后同模式)", len(fr.Patterns))
	}
	p := fr.Patterns[0]
	if p.Count != 2 {
		t.Fatalf("Count=%d, want 2", p.Count)
	}
	if !contains(p.Signature, "<ip>") || !contains(p.Signature, "<uuid>") || !contains(p.Signature, "<n>ms") {
		t.Fatalf("Signature 未完成变量归一: %q", p.Signature)
	}
	// 采样原文保留完整原始日志
	if !contains(p.Sample, "10.0.12.34") {
		t.Fatalf("Sample 未保留原始 IP: %q", p.Sample)
	}
}

func TestMatchRule(t *testing.T) {
	ev := &webhook.AlertEvent{
		RuleName: "订单服务CPU使用率过高",
		Labels: map[string]string{
			"alertname": "订单服务CPU使用率过高",
			"ip":        "10.0.12.34",
			"service":   "order-service",
		},
		Annotations: map[string]string{
			"summary": "订单服务 CPU 持续超过 90%",
		},
	}

	cases := []struct {
		name     string
		matchers string
		want     bool
	}{
		{"contains 命中", `[{"field":"alertname","operator":"contains","value":"CPU"}]`, true},
		{"contains 未命中", `[{"field":"alertname","operator":"contains","value":"内存"}]`, false},
		{"wildcard 命中", `[{"field":"label:service","operator":"wildcard","value":"order-*"}]`, true},
		{"regex 命中", `[{"field":"annotation:summary","operator":"regex","value":"超过\\s+90%"}]`, true},
		{"cidr 命中", `[{"field":"label:ip","operator":"cidr","value":"10.0.0.0/8"}]`, true},
		{"cidr 未命中", `[{"field":"label:ip","operator":"cidr","value":"192.168.0.0/16"}]`, false},
		{"多条件 AND 全命中", `[{"field":"alertname","operator":"contains","value":"CPU"},{"field":"label:ip","operator":"cidr","value":"10.0.0.0/8"}]`, true},
		{"多条件 AND 一假", `[{"field":"alertname","operator":"contains","value":"CPU"},{"field":"label:ip","operator":"cidr","value":"192.168.0.0/16"}]`, false},
		{"any 命中", `[{"field":"any","operator":"contains","value":"order-service"}]`, true},
	}
	for _, c := range cases {
		rule := &database.AlertTriggerRule{MatchersJSON: c.matchers}
		got, err := MatchRule(rule, ev)
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got=%v want=%v", c.name, got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
