package lts

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"PromAI/pkg/database"
	"PromAI/pkg/alerting/webhook"
)

// MatchRule 判断一条外部告警是否命中触发规则（多条件 AND，任一条件不满足即不匹配）。
// 匹配范围：alertname / 任意 label / 任意 annotation；操作符：equals/contains/wildcard/regex/cidr。
func MatchRule(rule *database.AlertTriggerRule, ev *webhook.AlertEvent) (bool, error) {
	if rule == nil || ev == nil {
		return false, nil
	}
	var matchers []database.TriggerMatcher
	if err := json.Unmarshal([]byte(rule.MatchersJSON), &matchers); err != nil {
		return false, fmt.Errorf("解析触发规则匹配条件失败: %w", err)
	}
	if len(matchers) == 0 {
		return false, nil
	}
	for _, m := range matchers {
		values := collectValues(m.Field, ev)
		if !matchAnyValue(values, m) {
			return false, nil
		}
	}
	return true, nil
}

// collectValues 按 field 收集告警事件中待匹配的字符串值。
func collectValues(field string, ev *webhook.AlertEvent) []string {
	switch {
	case field == "alertname":
		vals := []string{}
		if ev.RuleName != "" {
			vals = append(vals, ev.RuleName)
		}
		if n := ev.Labels["alertname"]; n != "" && n != ev.RuleName {
			vals = append(vals, n)
		}
		return vals
	case strings.HasPrefix(field, "label:"):
		key := strings.TrimPrefix(field, "label:")
		if v := ev.Labels[key]; v != "" {
			return []string{v}
		}
		return nil
	case strings.HasPrefix(field, "annotation:"):
		key := strings.TrimPrefix(field, "annotation:")
		if v := ev.Annotations[key]; v != "" {
			return []string{v}
		}
		return nil
	case field == "any":
		vals := collectValues("alertname", ev)
		for _, v := range ev.Labels {
			if v != "" {
				vals = append(vals, v)
			}
		}
		for _, v := range ev.Annotations {
			if v != "" {
				vals = append(vals, v)
			}
		}
		return vals
	default:
		// 未加前缀的字段名，兼容直接写 label key
		if v := ev.Labels[field]; v != "" {
			return []string{v}
		}
		if v := ev.Annotations[field]; v != "" {
			return []string{v}
		}
		return nil
	}
}

// matchAnyValue 判断任一候选值是否满足操作符条件。
func matchAnyValue(values []string, m database.TriggerMatcher) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if matchValue(v, m) {
			return true
		}
	}
	return false
}

func matchValue(v string, m database.TriggerMatcher) bool {
	switch strings.ToLower(m.Operator) {
	case "equals", "eq":
		return v == m.Value
	case "contains":
		return strings.Contains(strings.ToLower(v), strings.ToLower(m.Value))
	case "wildcard", "glob":
		re, err := wildcardToRegex(m.Value)
		if err != nil {
			return false
		}
		return re.MatchString(v)
	case "regex":
		re, err := regexp.Compile(m.Value)
		if err != nil {
			return false
		}
		return re.MatchString(v)
	case "cidr":
		return ipInCIDR(v, m.Value)
	default:
		return false
	}
}

// wildcardToRegex 将 glob 通配符（* 任意串，? 单字符）编译为不区分大小写的全匹配正则。
func wildcardToRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ipInCIDR 判断 IP 是否落在 CIDR 网段内（如 10.0.0.0/8）。
func ipInCIDR(ip, cidr string) bool {
	if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
		if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
			return ipNet.Contains(parsed)
		}
	}
	return false
}
