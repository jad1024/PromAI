package webhook

import "testing"

// TestEnrichInstanceLabelsKeepsExistingInstance 已有 instance 标签时不应触发兜底，
// 也不应生成 instance_summary。
func TestEnrichInstanceLabelsKeepsExistingInstance(t *testing.T) {
	ev := &AlertEvent{
		Labels:      map[string]string{"alertname": "Mysql 主从复制延迟", "instance": "192.168.2.174:9104"},
		Annotations: map[string]string{"summary": "MySQL Slave replication lag (instance 192.168.2.174:9104)"},
	}
	EnrichInstanceLabels(ev)
	if ev.Labels["instance"] != "192.168.2.174:9104" {
		t.Fatalf("instance 被覆盖：%q", ev.Labels["instance"])
	}
	if _, ok := ev.Labels["instance_summary"]; ok {
		t.Fatalf("已有 instance 时不应生成 instance_summary label")
	}
}

// TestEnrichInstanceLabelsFallbackWritesAnnotation 兜底场景（无实例维度、summary 无 IP）：
// 实例摘要应写入 Annotations 而非 Labels，避免污染 fingerprint 去重维度。
func TestEnrichInstanceLabelsFallbackWritesAnnotation(t *testing.T) {
	ev := &AlertEvent{
		Labels:      map[string]string{"alertname": "行情前置机检测异常"},
		Annotations: map[string]string{"summary": "公司前置机总数"},
	}
	EnrichInstanceLabels(ev)
	// 关键断言：instance_summary 不能再出现在 Labels 里（否则参与指纹导致多实例误合并）
	if _, ok := ev.Labels["instance_summary"]; ok {
		t.Fatalf("兜底摘要不应写入 Labels.instance_summary")
	}
	// 兜底摘要应落在 Annotations 里，仅用于展示
	if got := ev.Annotations["instance_summary"]; got != "公司前置机总数" {
		t.Fatalf("instance_summary 应写入 Annotations，得到 %q", got)
	}
}

// TestEnrichInstanceLabelsExtractsFromDescription 验证 instance 缺失但 description 含 IP:端口 时，
// 应从 description 提取 IP:端口 作为 instance。
// n9e 的 blackbox 探测场景：summary 是业务摘要（无 IP），description 才含目标 IP:端口。
// 修复前只看 summary，导致 instance 维度丢失，多实例告警被错误合并。
func TestEnrichInstanceLabelsExtractsFromDescription(t *testing.T) {
	ev := &AlertEvent{
		Labels: map[string]string{"alertname": "行情前置机检测异常"},
		Annotations: map[string]string{
			"summary":     "公司前置机总数",
			"description": "blackbox 探测失败, 端口:188.31.25.247:18888无法使用",
		},
	}
	EnrichInstanceLabels(ev)
	if got := ev.Labels["instance"]; got != "188.31.25.247:18888" {
		t.Fatalf("应从 description 提取 IP:端口 作为 instance，得到 %q", got)
	}
	// 已有 instance 后，不应再写兜底 instance_summary（避免冗余）
	if _, ok := ev.Annotations["instance_summary"]; ok {
		t.Fatalf("提取到 instance 后不应再写 instance_summary 兜底")
	}
}

// TestEnrichInstanceLabelsDescriptionPreferredOverSummary 同时有 description 和 summary 时，
// description 含的 IP 优先于 summary。
func TestEnrichInstanceLabelsDescriptionPreferredOverSummary(t *testing.T) {
	ev := &AlertEvent{
		Labels: map[string]string{"alertname": "行情前置机检测异常"},
		Annotations: map[string]string{
			"summary":     "公司前置机总数 192.168.1.1 故障",
			"description": "blackbox 探测失败, 端口:188.31.25.247:18888无法使用",
		},
	}
	EnrichInstanceLabels(ev)
	// 应优先取 description 的 188.31.25.247:18888，而非 summary 的 192.168.1.1
	if got := ev.Labels["instance"]; got != "188.31.25.247:18888" {
		t.Fatalf("description 应优先于 summary，得到 %q", got)
	}
}

// TestEnrichInstanceLabelsFallbackFromDescription 兜底场景：summary 和 description 都没 IP，
// 应从 description 截取前 32 字符写到 instance_summary（用于展示）。
func TestEnrichInstanceLabelsFallbackFromDescription(t *testing.T) {
	ev := &AlertEvent{
		Labels: map[string]string{"alertname": "HTTP检测异常"},
		Annotations: map[string]string{
			"description": "blackbox 探测失败, 接口:no response from server",
		},
	}
	EnrichInstanceLabels(ev)
	if _, ok := ev.Labels["instance"]; ok {
		t.Fatalf("无 IP 时不应设置 instance")
	}
	// 兜底从 description 截取（按字节截到 32："blackbox 探测失败, 接口:no" 刚好 32 字节）
	if got := ev.Annotations["instance_summary"]; got != "blackbox 探测失败, 接口:no" {
		t.Fatalf("instance_summary 应从 description 截取前 32 字节，得到 %q", got)
	}
}
