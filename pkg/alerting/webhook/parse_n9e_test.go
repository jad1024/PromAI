package webhook

import (
	"encoding/json"
	"testing"
)

// TestParseN9ERuleNameFromLabels 验证 n9e 规则名优先取自 labels.rulename，
// 而非顶层/标签里的 name（业务维度，如"行情服务"）。
// 修复前：RuleName 会被错误取成 name（"行情服务"），导致不同规则、不同实例的
// 告警被聚合到同一个 alertname 下，表现为"同一 alertname 下多个实例被当重发"。
func TestParseN9ERuleNameFromLabels(t *testing.T) {
	raw := map[string]interface{}{
		"name": "行情服务", // 业务维度，不是规则名
		"labels": map[string]interface{}{
			"rulename": "行情前置机检测异常",
			"instance": "188.31.25.247:18888",
			"name":     "行情服务",
			"severity": "期货严重2",
		},
	}
	body, _ := json.Marshal(raw)
	events, err := ParseN9E(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 个事件，实际 %d", len(events))
	}
	ev := events[0]
	if ev.RuleName != "行情前置机检测异常" {
		t.Fatalf("规则名应取 labels.rulename，得到 %q", ev.RuleName)
	}
}

// TestParseN9ERuleNameFallbackAlertname 无 rulename 时回退到 labels.alertname。
func TestParseN9ERuleNameFallbackAlertname(t *testing.T) {
	raw := map[string]interface{}{
		"name": "行情服务",
		"labels": map[string]interface{}{
			"alertname": "HTTP检测异常",
			"instance":  "http://10.8.3.94:8081/",
		},
	}
	body, _ := json.Marshal(raw)
	events, err := ParseN9E(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if events[0].RuleName != "HTTP检测异常" {
		t.Fatalf("无 rulename 时应回退到 labels.alertname，得到 %q", events[0].RuleName)
	}
}

// TestParseN9ERuleNameExcludesName 验证顶层 name（业务维度）不会再被当作规则名。
func TestParseN9ERuleNameExcludesName(t *testing.T) {
	raw := map[string]interface{}{
		"name":  "行情服务",
		"title": "行情前置机检测异常",
	}
	body, _ := json.Marshal(raw)
	events, err := ParseN9E(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 无 rulename/alertname/rule_name/alert_name 时，兜底到 title，而非 name
	if events[0].RuleName != "行情前置机检测异常" {
		t.Fatalf("应兜底到顶层 title，得到 %q", events[0].RuleName)
	}
}
