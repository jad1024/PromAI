// Package alerting 实现 PromAI 内置的告警评估与分发子系统。
//
// 整体由四个子包组成：
//   - alerting/store        AlertRule/Silence/Inhibit/Route 的持久化 CRUD
//   - alerting/evaluator    周期评估各数据源 + PromQL，产出告警事件（替代 Prometheus Ruler）
//   - alerting/dispatcher   去重 / 抑制 / 静默 / 路由 / 分组聚合（替代 Alertmanager）
//   - alerting/notifier     复用 pkg/notify 通道并渲染告警消息模板
//
// 本文件提供子包共享的通用类型：Matcher、LabelSet、Fingerprint。
package alerting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MatcherOp 标签匹配操作符（与 Alertmanager 语义一致）
type MatcherOp string

const (
	MatchEqual    MatcherOp = "="
	MatchNotEqual MatcherOp = "!="
	MatchRegex    MatcherOp = "=~"
	MatchNotRegex MatcherOp = "!~"
)

// Matcher 通用标签匹配条件
type Matcher struct {
	Name  string    `json:"name"`
	Op    MatcherOp `json:"op"`
	Value string    `json:"value"`

	re     *regexp.Regexp
	reOnce sync.Once
	reErr  error
}

// Validate 校验 Matcher 字段合法性
func (m *Matcher) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("matcher name is empty")
	}
	switch m.Op {
	case MatchEqual, MatchNotEqual, MatchRegex, MatchNotRegex:
	default:
		return fmt.Errorf("invalid matcher op: %q", m.Op)
	}
	if m.Op == MatchRegex || m.Op == MatchNotRegex {
		if _, err := regexp.Compile("^(?:" + m.Value + ")$"); err != nil {
			return fmt.Errorf("invalid regex %q: %w", m.Value, err)
		}
	}
	return nil
}

// compile 懒编译正则（线程安全）
func (m *Matcher) compile() {
	m.reOnce.Do(func() {
		// 与 Prometheus / Alertmanager 一致：完全匹配，等价于 ^(?:expr)$
		m.re, m.reErr = regexp.Compile("^(?:" + m.Value + ")$")
	})
}

// Match 检查给定 label 值是否命中该 matcher
func (m *Matcher) Match(value string) bool {
	switch m.Op {
	case MatchEqual:
		return value == m.Value
	case MatchNotEqual:
		return value != m.Value
	case MatchRegex:
		m.compile()
		return m.reErr == nil && m.re.MatchString(value)
	case MatchNotRegex:
		m.compile()
		return m.reErr == nil && !m.re.MatchString(value)
	}
	return false
}

// MatchAll 给定一组 matcher，全部命中才返回 true（matcher 之间是 AND）
func MatchAll(matchers []Matcher, labels map[string]string) bool {
	for i := range matchers {
		v := labels[matchers[i].Name]
		if !matchers[i].Match(v) {
			return false
		}
	}
	return true
}

// EncodeMatchers 序列化 matcher 列表为 JSON 字符串（DB 存储用）
func EncodeMatchers(matchers []Matcher) (string, error) {
	if len(matchers) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(matchers)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeMatchers 反序列化 JSON 字符串为 matcher 列表
func DecodeMatchers(s string) ([]Matcher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []Matcher
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LabelSet key-value 标签集合（用于 fingerprint 与 group_key 计算）
type LabelSet map[string]string

// Sorted 返回按 key 排序后的标签切片，保证 fingerprint 稳定
func (ls LabelSet) Sorted() [][2]string {
	keys := make([]string, 0, len(ls))
	for k := range ls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, ls[k]})
	}
	return out
}

// Fingerprint 计算告警实例的指纹：rule_id + datasource_id + 所有 label 的稳定哈希。
// 用于实例去重：同一个规则在同一个数据源上对同一组 label 维度命中时，视为同一告警。
func Fingerprint(ruleID, datasourceID uint, labels LabelSet) string {
	h := sha256.New()
	fmt.Fprintf(h, "r=%d|ds=%d|", ruleID, datasourceID)
	for _, kv := range labels.Sorted() {
		// 用 \x00/\x01 作为不可能出现在正常 label 中的分隔符
		h.Write([]byte(kv[0]))
		h.Write([]byte{0})
		h.Write([]byte(kv[1]))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// GroupKey 计算分组键：route_id + 选中的 group_by 标签值的稳定哈希
func GroupKey(routeID uint, groupBy []string, labels LabelSet) string {
	h := sha256.New()
	fmt.Fprintf(h, "rt=%d|", routeID)
	sortedGroupBy := append([]string(nil), groupBy...)
	sort.Strings(sortedGroupBy)
	for _, k := range sortedGroupBy {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(labels[k]))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// PayloadHash 计算通知载荷哈希（用于发送去重防风暴）
func PayloadHash(channelID uint, content string) string {
	h := sha256.New()
	fmt.Fprintf(h, "ch=%d|", channelID)
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// EncodeJSON 通用 JSON 编码（DB 字段用，失败返回空 JSON）
func EncodeJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeJSON 通用 JSON 解码
func DecodeJSON(s string, out interface{}) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), out)
}

// DecodeStringSlice 工具：解码字符串切片字段（[]string）
func DecodeStringSlice(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// DecodeUintSlice 工具：解码 uint 切片字段（[]uint）
func DecodeUintSlice(s string) []uint {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []uint
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// EncodeUintSlice 工具：编码 uint 切片字段（[]uint）
func EncodeUintSlice(v []uint) string {
	if len(v) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// DecodeLabels 工具：解码 LabelSet 字段
func DecodeLabels(s string) LabelSet {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out LabelSet
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// MergeLabels 合并多组 labels（后者覆盖前者）
func MergeLabels(sets ...LabelSet) LabelSet {
	out := LabelSet{}
	for _, s := range sets {
		for k, v := range s {
			out[k] = v
		}
	}
	return out
}
