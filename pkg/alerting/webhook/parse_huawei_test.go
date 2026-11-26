package webhook

import (
	"encoding/json"
	"testing"
)

func TestParseHuaweiCloudV1(t *testing.T) {
	payload := map[string]interface{}{
		"type": "notification",
		"message": mustJSON(map[string]interface{}{
			"version": "v1",
			"data": map[string]interface{}{
				"Namespace":             "弹性云服务器",
				"DimensionName":         "云服务器",
				"MetricName":            "CPU使用率",
				"IsAlarm":               true,
				"AlarmLevel":            "重要",
				"Region":                "华北-乌兰察布-二零三",
				"RegionId":              "cn-north-7",
				"AlarmRuleName":         "test-zcy",
				"AccountName":           "RDS_test",
				"LastAlarmLevel":        "提示",
				"ResourceID":            "853f8ead-428e-4c54-babc-f1bdae6d34a0",
				"PrivateIP":             "10.0.0.39",
				"EPName":                "default",
				"ResourceName":          "ecs-hce330-test",
				"PrivateIPV6":           "2407:c080:11f0:1115:490e:36fe:ba94:a410",
				"PublicIP":              "100.85.113.106",
				"CurrentData":           "100.00 %",
				"Count":                 1,
				"IsOriginalValue":       true,
				"Filter":                "原始值",
				"ComparisonOperator":    ">=",
				"Value":                 "0%",
				"AlarmTime":             "2025/11/17 16:39:42 GMT+08:00",
				"AlarmDesc":             "test",
				"AlarmRecordID":         "ah260110T172316SmCFRcx7X",
				"AlarmNotificationType": "指标",
				"Unit":                  "%",
				"ResourceTags":          "_sys_type_hcss_x,test=test",
				"AlarmDurationTime":     "0",
			},
		}),
	}
	body, _ := json.Marshal(payload)
	events, err := ParseHuaweiCloud(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.RuleName != "test-zcy" {
		t.Errorf("RuleName = %q, want test-zcy", ev.RuleName)
	}
	if ev.State != "firing" {
		t.Errorf("State = %q, want firing", ev.State)
	}
	if ev.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", ev.Severity)
	}
	if ev.Value != 100.0 {
		t.Errorf("Value = %v, want 100.0", ev.Value)
	}
	if ev.Threshold != 0 {
		t.Errorf("Threshold = %v, want 0", ev.Threshold)
	}
	wantLabels := map[string]string{
		"namespace":       "弹性云服务器",
		"metric":          "CPU使用率",
		"resource_name":   "ecs-hce330-test",
		"resource_id":     "853f8ead-428e-4c54-babc-f1bdae6d34a0",
		"region":          "华北-乌兰察布-二零三",
		"region_id":       "cn-north-7",
		"private_ip":      "10.0.0.39",
		"public_ip":       "100.85.113.106",
		"alarm_rule_name": "test-zcy",
	}
	for k, want := range wantLabels {
		if got := ev.Labels[k]; got != want {
			t.Errorf("Labels[%s] = %q, want %q", k, got, want)
		}
	}
	if ev.Annotations["summary"] != "test" {
		t.Errorf("Annotations[summary] = %q, want test", ev.Annotations["summary"])
	}
	if ev.Annotations["current_data"] != "100.00 %" {
		t.Errorf("Annotations[current_data] = %q", ev.Annotations["current_data"])
	}
	if ev.Annotations["alarm_time"] != "2025/11/17 16:39:42 GMT+08:00" {
		t.Errorf("Annotations[alarm_time] = %q", ev.Annotations["alarm_time"])
	}
	if ev.OccurredAt.IsZero() {
		t.Errorf("OccurredAt is zero")
	}
	if ev.ExternalID == "" || ev.ExternalID == "|" {
		t.Errorf("ExternalID = %q", ev.ExternalID)
	}
}

func TestParseHuaweiCloudV1Resolved(t *testing.T) {
	payload := map[string]interface{}{
		"type": "notification",
		"message": mustJSON(map[string]interface{}{
			"version": "v1",
			"data": map[string]interface{}{
				"AlarmRuleName": "test-zcy",
				"IsAlarm":       false,
				"ResourceID":    "r-111",
				"ResourceName":  "ecs-1",
				"CurrentData":   "0 %",
				"Value":         "80%",
				"AlarmTime":     "2025/11/17 16:40:00 GMT+08:00",
			},
		}),
	}
	body, _ := json.Marshal(payload)
	events, err := ParseHuaweiCloud(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if events[0].State != "resolved" {
		t.Errorf("State = %q, want resolved", events[0].State)
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestParseHuaweiCloudRawDataLabels 复现用户生产环境真实 payload：
// 华为云 CES 新版消息模板把关键资源字段放在 raw_data 这个「内嵌 JSON 字符串」里，
// 且字段名为 PascalCase 驼峰（PrivateIp / ResourceName / ResourceId / AlarmRuleName），
// 顶层只有 snake_case 聚合字段（account_name / current_data / value / unit ...）。
// 旧解析器不解析 raw_data，导致 labels 里没有任何实例维度 → 告警聚合「0 实例」。
// 同时覆盖两种投递形态：SMN 标准外层(type+message) 与 直接告警对象。
func TestParseHuaweiCloudRawDataLabels(t *testing.T) {
	inner := map[string]interface{}{
		"AccountName":           "hid_h_6alwdc1rdq5f0",
		"AlarmDurationTime":     "20分钟",
		"AlarmLevel":            "紧急",
		"AlarmNotificationType": "指标",
		"AlarmRecordID":         "ah261125T161830Sr7o9Vk2I",
		"AlarmRuleName":         "alarm-proccess",
		"AlarmTime":             "2026/11/25 16:38:30 GMT+08:00",
		"ComparisonOperator":    "<",
		"Count":                 3,
		"CurrentData":           "0.00 count",
		"DimensionName":         "云服务器,进程",
		"EpName":                "gkoudaiNobleMetal",
		"Filter":                "原始值",
		"IsAlarm":               true,
		"IsOriginalValue":       true,
		"MetricName":            "(Agent) 指定进程数",
		"Namespace":             "主机监控",
		"PrivateIp":             "10.8.3.48",
		"Region":                "华北-北京四",
		"RegionId":              "cn-north-4",
		"ResourceId":            "953b9b78-a83c-4604-9f71-ef6bbbfdb50e，进程：6e8efbe3a442748b716644025479aab5",
		"ResourceName":          "TransactionDataBackFutures-node-0001 > python3",
		"Unit":                  "count",
		"Value":                 "1 count",
	}
	innerJSON, _ := json.Marshal(inner)
	msgObj := map[string]interface{}{
		"account_name":        "hid_h_6alwdc1rdq5f0",
		"alarm_duration_time": "20分钟",
		"alarm_time":          "2026/11/25 16:38:30 GMT+08:00",
		"comparison_operator": "<",
		"count":               "3",
		"current_data":        "0.00 count",
		"filter":              "原始值",
		"raw_data":            string(innerJSON),
		"unit":                "count",
		"value":               "1 count",
	}
	msgJSON, _ := json.Marshal(msgObj)
	bodySMN, _ := json.Marshal(map[string]interface{}{
		"type": "Notification", "message": string(msgJSON),
	})

	cases := map[string][]byte{
		"smn-envelope":  bodySMN,
		"direct-object": msgJSON,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			events, err := ParseHuaweiCloud(body)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %d", len(events))
			}
			ev := events[0]
			t.Logf("labels = %#v", ev.Labels)
			if ev.RuleName != "alarm-proccess" {
				t.Errorf("RuleName = %q, want alarm-proccess", ev.RuleName)
			}
			if ev.State != "firing" {
				t.Errorf("State = %q, want firing (IsAlarm=true)", ev.State)
			}
			if ev.Severity != "critical" {
				t.Errorf("Severity = %q, want critical (AlarmLevel=紧急)", ev.Severity)
			}
			if got := ev.Labels["private_ip"]; got != "10.8.3.48" {
				t.Errorf("Labels[private_ip] = %q, want 10.8.3.48", got)
			}
			if got := ev.Labels["resource_name"]; got == "" {
				t.Errorf("Labels[resource_name] 不应为空（来自 raw_data.ResourceName）")
			}
			if got := ev.Labels["resource_id"]; got == "" {
				t.Errorf("Labels[resource_id] 不应为空（来自 raw_data.ResourceId）")
			}
			if got := ev.Labels["namespace"]; got != "主机监控" {
				t.Errorf("Labels[namespace] = %q, want 主机监控", got)
			}
			// 标准 instance 标签：聚合层 extractInstance 第一优先命中它
			if got := ev.Labels["instance"]; got == "" {
				t.Errorf("Labels[instance] 不应为空，否则告警聚合显示 0 实例")
			}
		})
	}
}
