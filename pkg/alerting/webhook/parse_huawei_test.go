package webhook

import (
	"encoding/json"
	"testing"
)

func TestParseHuaweiCloudV1(t *testing.T) {
	payload := map[string]interface{}{
		"type":    "notification",
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
