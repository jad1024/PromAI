package notifier

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"
)

// TemplateContext 是自定义 Go template 渲染时可访问的根上下文。
//
// 在模板里访问示例：
//
//	{{ .Title }}                    告警标题
//	{{ .Severity }}                 严重级别 (大写: CRITICAL/WARNING/INFO)
//	{{ .Alertname }}                告警名（来自 labels.alertname）
//	{{ .Total }}                    原始实例数
//	{{ .Cause }} / {{ .Impact }}    规则的可能原因 / 影响范围
//	{{ .Resolved }}                 true=恢复通知, false=告警通知
//	{{ .BaseURL }}                  PromAI 告警详情链接前缀
//
//	{{ range .Entries }}            遍历聚合后的告警项
//	  - {{ .Summary }}              告警内容（已渲染模板变量）
//	    严重 {{ .Severity }} / 状态 {{ .State }}
//	    数据源: {{ .DatasourceName }}（{{ .DatasourceID }}）
//	    规则:   {{ .RuleName }}（{{ .RuleID }}）
//	    Host:   {{ .Host }}（按 host_format 渲染过）
//	    Value:  {{ .ValueStr }}（按 value_precision 格式化）
//	    阈值:   {{ .Threshold }}
//	    命中:   {{ .Count }}        同一聚合里实例数
//	    时间:   {{ .Time }}（按 time_format 格式化）
//	    指纹:   {{ .Fingerprint }}
//	    详情:   {{ .DetailURL }}
//	{{ end }}
type TemplateContext struct {
	Title     string
	Severity  string
	Alertname string
	Total     int
	Cause     string
	Impact    string
	Resolved  bool
	BaseURL   string
	Entries   []TemplateEntry
}

// TemplateEntry 是 TemplateContext.Entries 里每一项
type TemplateEntry struct {
	Summary        string
	State          string
	Severity       string
	RuleID         uint
	RuleName       string
	DatasourceID   uint
	DatasourceName string
	Host           string
	ValueStr       string // 按 value_precision 格式化（区间用 "A~B"）
	MinValue       float64
	MaxValue       float64
	Threshold      float64
	Count          int
	Time           string // 按 time_format 格式化
	Fingerprint    string
	DetailURL      string
	Labels         map[string]string // 第一条样本的 labels
}

// sandboxFuncs 提供受限的模板函数集。
// 不暴露 os/exec/network/file 函数，仅字符串与格式化辅助。
func sandboxFuncs() template.FuncMap {
	return template.FuncMap{
		// 字符串
		"upper":    strings.ToUpper,
		"lower":    strings.ToLower,
		"title":    strings.Title, // nolint: SA1019
		"trim":     strings.TrimSpace,
		"replace":  func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
		"contains": strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"split":     strings.Split,
		"join":      func(sep string, ss []string) string { return strings.Join(ss, sep) },
		"truncate": func(n int, s string) string {
			if n <= 0 || n >= len(s) {
				return s
			}
			return s[:n] + "..."
		},
		// 数字格式化
		"printf": fmt.Sprintf,
		"format": func(layout string, v interface{}) string { return fmt.Sprintf(layout, v) },
		// 时间
		"now":     func() string { return time.Now().Format("2006-01-02 15:04:05") },
		"formatTime": func(layout string, t time.Time) string { return t.Format(layout) },
		// 条件
		"default": func(def, v interface{}) interface{} {
			if v == nil {
				return def
			}
			if s, ok := v.(string); ok && s == "" {
				return def
			}
			return v
		},
		"gt": func(a, b interface{}) bool { return toFloat(a) > toFloat(b) },
		"lt": func(a, b interface{}) bool { return toFloat(a) < toFloat(b) },
		"ge": func(a, b interface{}) bool { return toFloat(a) >= toFloat(b) },
		"le": func(a, b interface{}) bool { return toFloat(a) <= toFloat(b) },
		"eq": func(a, b interface{}) bool { return fmt.Sprint(a) == fmt.Sprint(b) },
		// 算术（用于 {{range $i, $_ := ...}}{{add $i 1}}）
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		// 集合
		"len": func(v interface{}) int {
			switch x := v.(type) {
			case []TemplateEntry:
				return len(x)
			case []string:
				return len(x)
			case []interface{}:
				return len(x)
			case string:
				return len(x)
			case map[string]string:
				return len(x)
			}
			return 0
		},
	}
}

// renderCustomTemplate 用沙箱函数集渲染自定义 Go template。
// 返回错误时调用方应回退到预设风格。
func renderCustomTemplate(tplText string, ctx *TemplateContext) (string, error) {
	if strings.TrimSpace(tplText) == "" {
		return "", fmt.Errorf("template is empty")
	}
	t, err := template.New("custom").
		Funcs(sandboxFuncs()).
		Option("missingkey=zero"). // 缺字段不报错，置零
		Parse(tplText)
	if err != nil {
		return "", fmt.Errorf("模板语法错误: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("模板渲染失败: %w", err)
	}
	return buf.String(), nil
}

// toFloat 把任意数值类型转 float64，便于 gt/lt 等比较函数同时支持 int / float / string
func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case string:
		var f float64
		_, _ = fmt.Sscanf(x, "%f", &f)
		return f
	}
	return 0
}
