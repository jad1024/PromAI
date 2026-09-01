// Package webhook 提供外部告警平台（n9e / 华为云 CES / 通用 Alertmanager 兼容）的
// webhook 事件解析能力。解析结果统一为 AlertEvent，由上层（main 包）负责落库、
// 通知转发与 AI 根因分析。
package webhook

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AlertEvent 外部告警统一事件结构
type AlertEvent struct {
	Source      string            // 源名称（展示用）
	SourceType  string            // n9e / huaweicloud / generic
	SourceID    uint              // 数据库 ExternalAlertSource.ID
	ExternalID  string            // 平台规则/告警唯一 ID（参与指纹计算）
	RuleName    string            // 告警规则名
	Severity    string            // critical / warning / info / normal
	State       string            // firing / resolved
	Labels      map[string]string // 标签
	Annotations map[string]string // 注解
	Value       float64           // 触发值
	Threshold   float64           // 触发阈值（外部平台携带时）
	OccurredAt  time.Time         // 触发时间（缺省用 now）
}

// SubscribeURLKey 华为云 SMN 订阅确认事件中携带的回访地址注解键
const SubscribeURLKey = "smn_subscribe_url"

// Parse 按来源类型分发解析
func Parse(body []byte, sourceType string) ([]AlertEvent, error) {
	switch strings.ToLower(sourceType) {
	case "n9e":
		return ParseN9E(body)
	case "huaweicloud", "huawei", "ces":
		return ParseHuaweiCloud(body)
	case "aliyun", "alibabacloud", "alicloud", "alibaba", "cms":
		return ParseAliyun(body)
	case "generic", "alertmanager":
		return ParseGeneric(body)
	default:
		return ParseGeneric(body)
	}
}

// ParseN9E 解析 n9e 告警回调（callback）事件。
// n9e 不同版本字段略有差异，这里做宽松解析（多候选字段名）。
// 同时兼容 n9e 直接推送数组（如回调脚本里 [{...}]）或对象 {"alerts":[...]} 两种形式。
func ParseN9E(body []byte) ([]AlertEvent, error) {
	// 先尝试数组形式
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var list []map[string]interface{}
		if err := json.Unmarshal(body, &list); err == nil {
			return parseN9EList(list)
		}
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("n9e JSON 解析失败: %w", err)
	}
	// 兼容直接是告警对象 / 包一层 data / alerts 列表
	if list, ok := raw["alerts"].([]interface{}); ok && len(list) > 0 {
		return parseN9EListInterface(list)
	}
	if data, ok := raw["data"].(map[string]interface{}); ok {
		return []AlertEvent{parseN9EOne(data)}, nil
	}
	return []AlertEvent{parseN9EOne(raw)}, nil
}

func parseN9EList(list []map[string]interface{}) ([]AlertEvent, error) {
	events := make([]AlertEvent, 0, len(list))
	for _, m := range list {
		if m == nil {
			continue
		}
		events = append(events, parseN9EOne(m))
	}
	return events, nil
}

func parseN9EListInterface(list []interface{}) ([]AlertEvent, error) {
	events := make([]AlertEvent, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		events = append(events, parseN9EOne(m))
	}
	return events, nil
}

func parseN9EOne(raw map[string]interface{}) AlertEvent {
	ev := AlertEvent{
		SourceType:  "n9e",
		State:       "firing",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		OccurredAt:  time.Now(),
	}
	ev.ExternalID = firstStr(raw, "rule_id", "alert_id", "id")

	// 状态：firing/resolved/pending/ok
	switch strings.ToLower(firstStr(raw, "status", "state", "alert_state", "event_type")) {
	case "resolved", "ok", "recovered", "recovery", "resolve":
		ev.State = "resolved"
	case "pending":
		ev.State = "pending"
	}
	// n9e 常用 is_recovered 字段标记恢复（0/1 或 bool），需单独识别
	if ev.State != "resolved" && parseRecoveredFlag(firstAny(raw, "is_recovered", "isRecovered", "recovered", "recovery", "recovered_flag")) {
		ev.State = "resolved"
	}

	// 级别：n9e severity 1~5 或 critical/warning/info
	ev.Severity = parseSeverity(firstAny(raw, "severity", "priority", "level"))

	// 标签
	mergeMap(ev.Labels, asMap(raw["labels"]))
	mergeMap(ev.Labels, asMap(raw["tags"]))
	if host, ok := raw["target_ident"].(string); ok && host != "" {
		ev.Labels["instance"] = host
	}
	// 注解
	mergeMap(ev.Annotations, asMap(raw["annotations"]))
	if s := firstStr(raw, "summary", "description", "alert_msg", "alert_content"); s != "" {
		ev.Annotations["summary"] = s
	}

	// 规则名：n9e 的规则名标准字段在 labels.rulename（如"行情前置机检测异常"），
	// 而顶层 raw.name / labels.name 是 metric 的业务维度（如"行情服务"），二者极易混淆。
	// 若误把 name 当规则名，会导致不同规则、不同实例的告警被错误聚合到同一个 alertname 下。
	// 优先级：labels.rulename > labels.alertname > 顶层 rule_name/alert_name > 顶层 title。
	// 明确排除 name，避免业务维度污染规则名。
	ev.RuleName = firstNonEmpty(
		ev.Labels["rulename"],
		ev.Labels["alertname"],
		firstStr(raw, "rule_name", "alert_name"),
		firstStr(raw, "title"),
	)

	ev.Value = firstFloat(raw, "value", "trigger_value", "triggerValue", "metric_value", "current_value")
	ev.Threshold = firstFloat(raw, "threshold", "trigger_threshold", "alert_threshold")
	if ts := firstInt64(raw, "trigger_time", "first_trigger_time", "timestamp", "started_at"); ts > 0 {
		if ts > 1e12 { // 毫秒级时间戳
			ev.OccurredAt = time.UnixMilli(ts)
		} else {
			ev.OccurredAt = time.Unix(ts, 0)
		}
	}
	return ev
}

// ParseHuaweiCloud 解析华为云 SMN HTTP 订阅推送。
// 华为云告警通知 -> SMN 主题 -> HTTP 订阅，推送结构：
//
//	{"type":"notification","message":"<告警JSON字符串>",...}
//
// message 内告警 JSON 存在两种形态：
//  1. 新版（v1）：{"version":"v1","data":{"AlarmRuleName":"...","MetricName":"...",...}}
//  2. 旧版（flat）：{"alarm_name":"...","alarm_status":"alarm","resource_name":"...",...}
//
// 首次订阅时 SMN 会推 SubscriptionConfirmation（需回访 subscribe_url 完成订阅确认）。
func ParseHuaweiCloud(body []byte) ([]AlertEvent, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("华为云 SMN JSON 解析失败: %w", err)
	}
	msgType := firstStr(raw, "type", "Type")
	switch strings.ToLower(msgType) {
	case "subscriptionconfirmation", "subscription_confirmation", "unsubscriptionconfirmation":
		// 返回一个特殊事件，由上层回访 subscribe_url 完成订阅确认
		ev := AlertEvent{
			SourceType:  "huaweicloud",
			State:       "confirm",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			OccurredAt:  time.Now(),
		}
		ev.ExternalID = firstStr(raw, "message_id", "MessageId")
		if u := firstStr(raw, "subscribe_url", "SubscribeURL"); u != "" {
			ev.Annotations[SubscribeURLKey] = u
		}
		return []AlertEvent{ev}, nil
	}

	// notification：message 字段是字符串，内含告警 JSON
	msgStr := firstStr(raw, "message", "Message")
	var alarmRaw map[string]interface{}
	if err := json.Unmarshal([]byte(msgStr), &alarmRaw); err != nil {
		// message 不是 JSON 时退化为纯文本注解
		alarmRaw = map[string]interface{}{"message": msgStr}
	}

	// 兼容新版 v1：顶层 {version:"v1", data:{...}}，把 data 里的字段扁平合并到 alarmRaw
	if ver := firstStr(alarmRaw, "version", "Version"); ver == "v1" {
		if data, ok := alarmRaw["data"].(map[string]interface{}); ok {
			// 保留原始 data JSON，便于完整查看
			if b, err := json.Marshal(data); err == nil {
				alarmRaw["_raw_data"] = string(b)
			}
			for k, v := range data {
				if _, exists := alarmRaw[k]; !exists {
					alarmRaw[k] = v
				}
			}
		}
	}

	ev := AlertEvent{
		SourceType:  "huaweicloud",
		ExternalID:  firstStr(alarmRaw, "alarm_record_id", "AlarmRecordID", "alarm_id", "alarmID") + "|" + firstStr(alarmRaw, "resource_id", "ResourceID", "instance_id"),
		State:       "firing",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		OccurredAt:  time.Now(),
	}

	// 规则名：新版 AlarmRuleName / 旧版 alarm_name / alarmName；再 fallback 到 Namespace-MetricName
	ruleName := firstStr(alarmRaw, "alarm_rule_name", "AlarmRuleName", "alarm_name", "alarmName", "metric_name", "MetricName")
	if ruleName == "" {
		ruleName = strings.TrimSpace(firstStr(alarmRaw, "namespace", "Namespace") + " " + firstStr(alarmRaw, "metric_name", "MetricName"))
	}
	ev.RuleName = ruleName

	// 华为云状态：新版 IsAlarm (bool) / 旧版 alarm_status: alarm|OK|insufficient_data
	if isAlarm := firstAny(alarmRaw, "is_alarm", "IsAlarm"); isAlarm != nil {
		switch t := isAlarm.(type) {
		case bool:
			if !t {
				ev.State = "resolved"
			}
		case string:
			if strings.EqualFold(t, "false") || strings.EqualFold(t, "ok") {
				ev.State = "resolved"
			}
		case float64:
			if t == 0 {
				ev.State = "resolved"
			}
		}
	} else {
		switch strings.ToLower(firstStr(alarmRaw, "alarm_status", "alarmStatus", "status")) {
		case "ok", "resolved", "recovered":
			ev.State = "resolved"
		case "insufficient_data", "insufficient":
			ev.State = "pending"
		}
	}

	// 级别：新版 AlarmLevel 中文/英文 / 旧版 alarm_level 数值或中文
	ev.Severity = parseSeverity(firstAny(alarmRaw, "alarm_level", "AlarmLevel", "alarmLevel", "level"))

	// Labels：保留小写 snake_case key，统一内部命名
	labelMap := map[string]string{
		"namespace":       firstStr(alarmRaw, "namespace", "Namespace"),
		"metric":          firstStr(alarmRaw, "metric_name", "MetricName"),
		"resource_name":   firstStr(alarmRaw, "resource_name", "ResourceName"),
		"resource_id":     firstStr(alarmRaw, "resource_id", "ResourceID"),
		"region":          firstStr(alarmRaw, "region", "Region"),
		"region_id":       firstStr(alarmRaw, "region_id", "RegionId"),
		"private_ip":      firstStr(alarmRaw, "private_ip", "PrivateIP", "private_ipv6", "PrivateIPV6"),
		"public_ip":       firstStr(alarmRaw, "public_ip", "PublicIP"),
		"alarm_rule_name": firstStr(alarmRaw, "alarm_rule_name", "AlarmRuleName"),
	}
	for k, v := range labelMap {
		if v != "" {
			ev.Labels[k] = v
		}
	}

	// Annotations：把关键可读信息都放进去，便于详情展示与飞书卡片
	annoKeys := []string{
		"alarm_desc", "AlarmDesc", "description",
		"alarm_content", "alarmContent", "content",
		"summary", "message",
	}
	if summary := firstStr(alarmRaw, annoKeys...); summary != "" {
		ev.Annotations["summary"] = summary
	}
	for _, pair := range []struct{ k, label string }{
		{"current_data", "CurrentData"},
		{"comparison_operator", "ComparisonOperator"},
		{"value", "Value"},
		{"alarm_time", "AlarmTime"},
		{"alarm_duration_time", "AlarmDurationTime"},
		{"filter", "Filter"},
		{"unit", "Unit"},
		{"count", "Count"},
		{"last_alarm_level", "LastAlarmLevel"},
		{"account_name", "AccountName"},
		{"ep_name", "EPName"},
	} {
		if v := firstStr(alarmRaw, pair.k, pair.label); v != "" {
			ev.Annotations[pair.k] = v
		}
	}
	if rawData := firstStr(alarmRaw, "_raw_data"); rawData != "" {
		ev.Annotations["raw_data"] = rawData
	}

	// Value / Threshold
	// 新版：CurrentData = "100.00 %" 是当前值；Value = "0%" 是阈值
	if v := parsePercentOrNumber(firstStr(alarmRaw, "current_data", "CurrentData")); v != 0 {
		ev.Value = v
	} else {
		ev.Value = firstFloat(alarmRaw, "value", "Value", "current_value", "metric_value")
	}
	if v := parsePercentOrNumber(firstStr(alarmRaw, "value", "Value")); v != 0 {
		ev.Threshold = v
	} else {
		ev.Threshold = firstFloat(alarmRaw, "threshold", "trigger_threshold", "alert_threshold")
	}

	// 触发时间
	if s := firstStr(alarmRaw, "alarm_time", "AlarmTime", "trigger_time", "triggerTime", "occur_time"); s != "" {
		if t, err := parseFlexibleTime(s); err == nil {
			ev.OccurredAt = t
		}
	}

	if ev.ExternalID == "|" {
		ev.ExternalID = fmt.Sprintf("%s|%s", ev.RuleName, hashLabels(ev.Labels))
	}
	return []AlertEvent{ev}, nil
}

// parsePercentOrNumber 从 "100.00 %" / "0%" / "12.5" 中解析数值。
func parsePercentOrNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, "%", "")
	s = strings.ReplaceAll(s, ",", "")
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return f
	}
	return 0
}

// ParseAliyun 解析阿里云云监控（CloudMonitor）报警回调。
// 配置路径：云监控控制台 → 报警规则 → 高级配置「报警回调」，POST 的 JSON 形如：
//
//	{"alertName":"CPU过高","alertState":"ALERT","curValue":"95.5",
//	 "expression":"$Average>=90","metricName":"CPUUtilization",
//	 "namespace":"acs_ecs_dashboard","ruleId":"applyRulexxx",
//	 "dimensions":"{\"instanceId\":\"i-xxx\"}","timestamp":1552314147040}
//
// alertState 取值：ALERT（触发）/ OK（恢复）/ NO_DATA（数据不足）。
func ParseAliyun(body []byte) ([]AlertEvent, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		// 批量回调：数组形式
		var list []map[string]interface{}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("阿里云 JSON 解析失败（数组）: %w", err)
		}
		events := make([]AlertEvent, 0, len(list))
		for _, item := range list {
			if item == nil {
				continue
			}
			events = append(events, parseAliyunOne(item))
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("未识别到阿里云告警事件（数组为空）")
		}
		return events, nil
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("阿里云 JSON 解析失败: %w", err)
	}
	// 兼容包一层 alerts 的形式
	if list, ok := raw["alerts"].([]interface{}); ok && len(list) > 0 {
		events := make([]AlertEvent, 0, len(list))
		for _, item := range list {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			events = append(events, parseAliyunOne(m))
		}
		if len(events) > 0 {
			return events, nil
		}
	}
	return []AlertEvent{parseAliyunOne(raw)}, nil
}

func parseAliyunOne(raw map[string]interface{}) AlertEvent {
	ev := AlertEvent{
		SourceType:  "aliyun",
		State:       "firing",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		OccurredAt:  time.Now(),
	}
	ev.RuleName = firstStr(raw, "alertName", "alert_name", "ruleName", "rule_name", "name")
	ev.ExternalID = firstStr(raw, "ruleId", "rule_id", "id")

	// alertState：ALERT / OK / NO_DATA
	switch strings.ToUpper(strings.TrimSpace(firstStr(raw, "alertState", "alert_state", "status", "state"))) {
	case "OK", "RESOLVED", "RECOVERED":
		ev.State = "resolved"
	case "NO_DATA", "NO DATA", "INSUFFICIENT":
		ev.State = "pending"
	}

	// 级别（回调通常不携带，缺省 warning）
	if v := firstAny(raw, "severity", "level", "priority"); v != nil {
		ev.Severity = parseSeverity(v)
	} else {
		ev.Severity = "warning"
	}

	// 标签：metric / namespace / instanceName / region + dimensions（JSON 字符串）
	if s := firstStr(raw, "metricName", "metric_name"); s != "" {
		ev.Labels["metric"] = s
	}
	if s := firstStr(raw, "namespace"); s != "" {
		ev.Labels["namespace"] = s
	}
	if s := firstStr(raw, "instanceName", "instance_name"); s != "" {
		ev.Labels["instance"] = s
	}
	if s := firstStr(raw, "regionId", "region", "region_id"); s != "" {
		ev.Labels["region"] = s
	}
	mergeMap(ev.Labels, parseAliyunDimensions(firstStr(raw, "dimensions", "Dimensions")))

	// 触发值 / 阈值（阈值从 expression 如 "$Average>=90" 中提取）
	ev.Value = firstFloat(raw, "curValue", "cur_value", "value", "metric_value")
	ev.Threshold = thresholdFromExpression(firstStr(raw, "expression", "Expression"))

	if s := firstStr(raw, "message", "content", "summary", "description"); s != "" {
		ev.Annotations["summary"] = s
	}
	// 外部 ID 缺省时用 规则名+实例 兜底
	if ev.ExternalID == "" {
		ev.ExternalID = ev.RuleName + "|" + firstStr(raw, "instanceName", "instance_name") + hashLabels(ev.Labels)
	}
	if ts := firstInt64(raw, "timestamp", "alertTime", "alert_time", "occur_time"); ts > 0 {
		if ts > 1e12 {
			ev.OccurredAt = time.UnixMilli(ts)
		} else {
			ev.OccurredAt = time.Unix(ts, 0)
		}
	}
	return ev
}

// parseAliyunDimensions 解析阿里云 dimensions 字段（JSON 字符串，如 {"instanceId":"i-xx"}）
func parseAliyunDimensions(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return asMap(m)
}

// thresholdFromExpression 从表达式（如 "$Average>=90"、"$Average>3"）中提取阈值数字
func thresholdFromExpression(expr string) float64 {
	expr = strings.TrimSpace(expr)
	i := strings.LastIndexAny(expr, "><=")
	if i < 0 || i+1 >= len(expr) {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(expr[i+1:]), 64)
	if err != nil {
		return 0
	}
	return f
}

// ParseGeneric 解析 Alertmanager webhook 兼容格式（通用兜底）。
// 兼容三种推送形态：
//  1. Alertmanager 标准对象：{"status":"firing","alerts":[{...}]}
//  2. 纯数组：[{...}]（n9e HTTP 回调脚本、Alertmanager 数组推送等直接发数组的场景）
//  3. 单告警对象：{"labels":{...},"annotations":{...},"alertname":"..."}
func ParseGeneric(body []byte) ([]AlertEvent, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		// 数组形式：每条都是一个告警对象（n9e 回调脚本直接推数组时触发）
		var list []map[string]interface{}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("通用 webhook JSON 解析失败（数组）: %w", err)
		}
		events := make([]AlertEvent, 0, len(list))
		for _, item := range list {
			if item == nil {
				continue
			}
			events = append(events, parseGenericOne(item))
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("未识别到告警事件（数组为空或元素非对象）")
		}
		return events, nil
	}

	// 对象形式：{status, alerts:[...]} 或单告警对象
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("通用 webhook JSON 解析失败: %w", err)
	}
	status := strings.ToLower(firstStr(raw, "status", "state"))
	if list, ok := raw["alerts"].([]interface{}); ok && len(list) > 0 {
		events := make([]AlertEvent, 0, len(list))
		for _, item := range list {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			ev := parseGenericOne(m)
			if status == "resolved" && ev.State != "resolved" {
				ev.State = "resolved"
			}
			events = append(events, ev)
		}
		if len(events) > 0 {
			return events, nil
		}
	}
	// 单告警对象（含 labels，或 alertname / rule_name 等可识别字段）
	if _, ok := raw["labels"]; ok || firstStr(raw, "alertname") != "" || firstStr(raw, "rule_name") != "" || firstStr(raw, "name") != "" {
		return []AlertEvent{parseGenericOne(raw)}, nil
	}
	return nil, fmt.Errorf("未识别到告警事件（缺少 alerts 列表或 labels）")
}

// parseGenericOne 解析单条通用告警（宽松字段，兼容 Alertmanager 与 n9e 回调字段）。
func parseGenericOne(m map[string]interface{}) AlertEvent {
	ev := AlertEvent{
		SourceType:  "generic",
		State:       "firing",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		OccurredAt:  time.Now(),
	}
	mergeMap(ev.Labels, asMap(m["labels"]))
	mergeMap(ev.Labels, asMap(m["tags"]))
	mergeMap(ev.Annotations, asMap(m["annotations"]))

	ev.RuleName = firstNonEmpty(
		firstStr(m, "alertname", "rule_name", "name", "title"),
		ev.Labels["alertname"], ev.Annotations["summary"],
	)
	ev.ExternalID = firstStr(m, "alert_id", "rule_id", "id")
	if ev.ExternalID == "" {
		ev.ExternalID = ev.RuleName + "|" + hashLabels(ev.Labels)
	}

	// 状态：firing / pending / resolved（含 n9e 的 ok/recovered 与 Alertmanager 的 endsAt）
	switch strings.ToLower(firstStr(m, "status", "state", "alert_state", "event_type")) {
	case "resolved", "ok", "recovered", "recovery", "resolve":
		ev.State = "resolved"
	case "pending":
		ev.State = "pending"
	}
	// n9e 常用 is_recovered 字段标记恢复
	if ev.State != "resolved" && parseRecoveredFlag(firstAny(m, "is_recovered", "isRecovered", "recovered", "recovery", "recovered_flag")) {
		ev.State = "resolved"
	}
	if ev.State == "firing" {
		endsAt := firstStr(m, "endsAt", "ends_at", "end_time", "end")
		if endsAt != "" && endsAt != "0001-01-01T00:00:00Z" {
			ev.State = "resolved"
		}
	}

	ev.Severity = severityFromLabels(ev.Labels)
	if sev := firstStr(m, "severity", "level", "priority"); sev != "" {
		ev.Severity = parseSeverity(sev)
	}
	ev.Value = firstFloat(m, "value", "trigger_value", "triggerValue", "metric_value", "current_value")
	ev.Threshold = firstFloat(m, "threshold", "trigger_threshold", "alert_threshold")
	if s := firstStr(m, "summary", "description", "message", "alert_msg"); s != "" {
		ev.Annotations["summary"] = s
	}
	if ts := firstStr(m, "startsAt", "starts_at", "start_time", "trigger_time", "first_trigger_time"); ts != "" {
		if t, err := parseFlexibleTime(ts); err == nil {
			ev.OccurredAt = t
		}
	}
	return ev
}

// ---------- 工具函数 ----------

func firstStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case json.Number:
				return t.String()
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			case int64:
				return strconv.FormatInt(t, 10)
			case int:
				return strconv.Itoa(t)
			}
		}
	}
	return ""
}

func firstAny(m map[string]interface{}, keys ...string) interface{} {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func firstFloat(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return t
		case int:
			return float64(t)
		case int64:
			return float64(t)
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
				return f
			}
		case json.Number:
			if f, err := t.Float64(); err == nil {
				return f
			}
		}
	}
	return 0
}

func firstInt64(m map[string]interface{}, keys ...string) int64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return int64(t)
		case int64:
			return t
		case int:
			return int64(t)
		case string:
			if i, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
				return i
			}
		case json.Number:
			if i, err := t.Int64(); err == nil {
				return i
			}
		}
	}
	return 0
}

// parseSeverity 统一级别映射：n9e/华为云数值或英文字符串 -> critical/warning/info/normal
func parseSeverity(v interface{}) string {
	switch t := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "critical", "emergency", "disaster", "fatal", "1", "一级", "紧急", "严重":
			return "critical"
		case "warning", "major", "important", "2", "二级", "重要", "警告":
			return "warning"
		case "info", "minor", "hint", "3", "4", "三级", "四级", "次要", "提示":
			return "info"
		case "normal", "ok", "0", "无":
			return "normal"
		}
		return t
	case float64:
		switch int(t) {
		case 1:
			return "critical"
		case 2:
			return "warning"
		default:
			return "info"
		}
	case int:
		switch t {
		case 1:
			return "critical"
		case 2:
			return "warning"
		default:
			return "info"
		}
	}
	return "warning"
}

func severityFromLabels(labels map[string]string) string {
	for _, k := range []string{"severity", "level", "priority"} {
		if v := labels[k]; v != "" {
			return parseSeverity(v)
		}
	}
	return "warning"
}

func mergeMap(dst, src map[string]string) {
	if src == nil {
		return
	}
	for k, v := range src {
		if v != "" {
			dst[k] = v
		}
	}
}

func asMap(v interface{}) map[string]string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		switch t := val.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(t)
		default:
			if b, err := json.Marshal(val); err == nil {
				out[k] = string(b)
			}
		}
	}
	return out
}

// EnrichInstanceLabels 在计算指纹前，从 labels/annotations 中补齐可用于区分实例的维度。
// 外部告警 webhook 常常只在 annotations.description/summary 里写明目标 IP:端口，
// labels 里只有 alertname，导致多个实例被合并到同一个 fingerprint。
// 该函数会把 IPv4:端口、分区/挂载点、主机名等从 description/summary 提取出来回填到 labels。
func EnrichInstanceLabels(ev *AlertEvent) {
	if ev.Labels == nil {
		ev.Labels = map[string]string{}
	}

	// 1. 收集可用文本：description 优先于 summary 优先于 message
	// n9e 的 blackbox 探测告警中，目标实例信息通常在 description（如"端口:188.31.25.247:18888无法使用"），
	// 而 summary 可能是业务摘要（如"公司前置机总数"），没有 IP。仅看 summary 会漏掉真正的实例维度。
	description := ev.Annotations["description"]
	summary := ev.Annotations["summary"]
	if summary == "" {
		summary = ev.Annotations["message"]
	}

	// 2. 把 annotations 中已存在的实例维度复制到 labels（如果 labels 没有）
	for _, k := range []string{"instance", "host", "hostname", "nodename", "device", "mountpoint", "resource_name", "resource_id"} {
		if ev.Labels[k] == "" && ev.Annotations[k] != "" {
			ev.Labels[k] = ev.Annotations[k]
		}
	}

	// 3. 从 description/summary 里正则提取 IP:端口
	// 优先级：description > summary，因为 description 通常是具体错误描述，含真实目标 IP
	if ev.Labels["instance"] == "" {
		ipv4PortRe := regexp.MustCompile(`(\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?)`)
		if description != "" {
			if m := ipv4PortRe.FindString(description); m != "" {
				ev.Labels["instance"] = m
			}
		}
		if ev.Labels["instance"] == "" && summary != "" {
			if m := ipv4PortRe.FindString(summary); m != "" {
				ev.Labels["instance"] = m
			}
		}
	}

	// 4. 分区/挂载点（只从 summary 提取，description 是具体错误描述）
	if summary != "" {
		if ev.Labels["device"] == "" && ev.Labels["mountpoint"] == "" {
			if m := regexp.MustCompile(`(?i)(\d+|/[^\s,，]+|[a-zA-Z0-9_/-]+?)\s*分区`).FindStringSubmatch(summary); len(m) > 1 {
				ev.Labels["device"] = m[1]
			}
		}
		if ev.Labels["mountpoint"] == "" && ev.Labels["device"] == "" {
			if m := regexp.MustCompile(`(/[a-zA-Z0-9_/.-]+)`).FindString(summary); m != "" {
				ev.Labels["mountpoint"] = m
			}
		}
	}

	// 5. 兜底：如果没有任何实例维度，就生成一个「实例摘要」仅用于展示。
	// 注意：写入 Annotations 而非 Labels——它只是展示用摘要，不能参与 fingerprint 去重，
	// 否则静态摘要文本会让同一 alertname 下本应区分的多个实例被错误合并成同一条告警。
	if ev.Labels["instance"] == "" && ev.Labels["host"] == "" && ev.Labels["device"] == "" && ev.Labels["mountpoint"] == "" && ev.Labels["resource_name"] == "" {
		fallback := summary
		if fallback == "" {
			fallback = description
		}
		if fallback != "" {
			if ev.Annotations == nil {
				ev.Annotations = map[string]string{}
			}
			// 取前 32 个字符作为实例摘要，避免过长
			if len(fallback) > 32 {
				fallback = fallback[:32]
			}
			ev.Annotations["instance_summary"] = fallback
		}
	}
}

// hashLabels 计算标签集的简短哈希（用于无 external_id 时的指纹）
func hashLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(labels[k]))
		h.Write([]byte{1})
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// parseRecoveredFlag 判断"是否已恢复"标记，兼容 bool / 0/1 / "true"/"1"/"yes" / "recovered"/"ok" 等写法。
func parseRecoveredFlag(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case float32:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case json.Number:
		f, err := t.Float64()
		return err == nil && f != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "y", "ok", "recovered", "recovery", "resolve", "resolved", "alert_ok":
			return true
		}
	}
	return false
}

func parseFlexibleTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04:05 GMT-07:00",
		"2006-01-02 15:04",
		time.RFC1123,
	} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ms > 1e12 {
			return time.UnixMilli(ms), nil
		}
		return time.Unix(ms, 0), nil
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
