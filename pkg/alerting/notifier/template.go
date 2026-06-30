package notifier

import (
	"encoding/json"
	"fmt"
	"time"

	"PromAI/pkg/database"
)

// MessageTemplate 描述一条通知通道的消息渲染配置。
// 存储在 NotificationChannel.ConfigJSON 的 "template" 子键里，可向后兼容（旧通道无该字段时使用默认值）。
//
// 渲染优先级（高→低）：
//  1. CustomMarkdown / CustomSubject 非空 → 走 Go template 沙箱
//  2. Style 预设（simple / table / card）+ 字段勾选
//
// CustomMarkdown 解析失败 / 渲染失败时自动回退到预设风格，并打印警告日志。
type MessageTemplate struct {
	// Style 风格预设：simple / table / card；空值等同 simple
	Style string `json:"style,omitempty"`

	// 标题格式化字符串，支持 {severity} {alertname} {total} {bucketCount} 占位符
	// 留空时按 style 默认
	TitleFormat string `json:"title_format,omitempty"`

	// 字段显隐
	ShowCause      *bool `json:"show_cause,omitempty"`       // 默认 true
	ShowImpact     *bool `json:"show_impact,omitempty"`      // 默认 true
	ShowValueRange *bool `json:"show_value_range,omitempty"` // 默认 true
	ShowHitCount   *bool `json:"show_hit_count,omitempty"`   // 默认 true
	ShowDatasource *bool `json:"show_datasource,omitempty"`  // 默认 true
	ShowTime       *bool `json:"show_time,omitempty"`        // 默认 true
	ShowDetailLink *bool `json:"show_detail_link,omitempty"` // 默认 true

	// HostFormat 主机展示风格：full / short / with_ip
	// short = 取域名第一段
	// with_ip = "短名 (IP)"
	HostFormat string `json:"host_format,omitempty"`

	// 时间格式（Go time 模板），默认 "01-02 15:04:05"
	TimeFormat string `json:"time_format,omitempty"`

	// 数值精度（小数位），默认 2
	ValuePrecision int `json:"value_precision,omitempty"`

	// 单条通知最多展示的聚合项数，超出截断；默认 50；0 = 默认
	MaxEntries int `json:"max_entries,omitempty"`

	// 单条通知 markdown 字节上限，默认 3800；0 = 默认
	MaxBytes int `json:"max_bytes,omitempty"`

	// 字段顺序（仅 simple 风格生效）：datasource,content,cause,impact,time,detail
	// 空值按默认顺序显示（datasource,content,time,detail，不含 cause/impact）
	Fields []string `json:"fields,omitempty"`

	// === 简易文本模板（仅 simple 风格） ===
	// 纯文本 + {field} 占位符，供非技术用户自定义每项条目的显示格式
	// 可用占位符: {datasource} {content} {cause} {impact} {time} {detail} {host} {value} {threshold} {count}
	// 留空时使用上面的 Fields 排序逻辑
	DefaultTemplate string `json:"default_template,omitempty"`

	// === 高级：完全自定义 Go template ===
	// 留空时使用预设风格；非空时优先使用，渲染错误会回退到预设并写日志
	// 模板可用变量见 TemplateContext / TemplateEntry 结构注释
	CustomMarkdown string `json:"custom_markdown,omitempty"` // 自定义 markdown 模板
	CustomSubject  string `json:"custom_subject,omitempty"`  // 自定义标题模板
}

// defaultTrue 返回 *bool 的实际取值，nil 视为 true
func defaultTrue(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// resolve 把 MessageTemplate 的所有字段填上默认值，便于 render 端无脑读取
type resolvedTemplate struct {
	Style           string
	TitleFormat     string
	ShowCause       bool
	ShowImpact      bool
	ShowValueRange  bool
	ShowHitCount    bool
	ShowDatasource  bool
	ShowTime        bool
	ShowDetailLink  bool
	HostFormat      string
	TimeFormat      string
	ValuePrecision  int
	MaxEntries      int
	MaxBytes        int
	Fields          []string
	DefaultTemplate string
	CustomMarkdown  string
	CustomSubject   string
}

func (t *MessageTemplate) resolve() resolvedTemplate {
	r := resolvedTemplate{
		Style:          "simple",
		TitleFormat:    "",
		ShowCause:      true,
		ShowImpact:     true,
		ShowValueRange: true,
		ShowHitCount:   true,
		ShowDatasource: true,
		ShowTime:       true,
		ShowDetailLink: true,
		HostFormat:     "short",
		TimeFormat:     "01-02 15:04:05",
		ValuePrecision: 2,
		MaxEntries:     50,
		MaxBytes:       3800,
	}
	if t == nil {
		return r
	}
	if t.Style != "" {
		r.Style = t.Style
	}
	r.TitleFormat = t.TitleFormat
	r.ShowCause = defaultTrue(t.ShowCause)
	r.ShowImpact = defaultTrue(t.ShowImpact)
	r.ShowValueRange = defaultTrue(t.ShowValueRange)
	r.ShowHitCount = defaultTrue(t.ShowHitCount)
	r.ShowDatasource = defaultTrue(t.ShowDatasource)
	r.ShowTime = defaultTrue(t.ShowTime)
	r.ShowDetailLink = defaultTrue(t.ShowDetailLink)
	if t.HostFormat != "" {
		r.HostFormat = t.HostFormat
	}
	if t.TimeFormat != "" {
		r.TimeFormat = t.TimeFormat
	}
	if t.ValuePrecision > 0 {
		r.ValuePrecision = t.ValuePrecision
	}
	if t.MaxEntries > 0 {
		r.MaxEntries = t.MaxEntries
	}
	if t.MaxBytes > 0 {
		r.MaxBytes = t.MaxBytes
	}
	if len(t.Fields) > 0 {
		r.Fields = t.Fields
	} else {
		r.Fields = nil // 明确用 nil 表示"未配置"，让 render 走旧行为
	}
	r.DefaultTemplate = t.DefaultTemplate
	r.CustomMarkdown = t.CustomMarkdown
	r.CustomSubject = t.CustomSubject
	return r
}

// parseChannelTemplate 从 NotificationChannel.ConfigJSON 中提取 template 子对象。
// 找不到 / 解析失败时返回 nil（render 端用 (*MessageTemplate)(nil).resolve() 拿到全默认）。
func parseChannelTemplate(ch *database.NotificationChannel) *MessageTemplate {
	if ch == nil || ch.ConfigJSON == "" {
		return nil
	}
	var wrapper struct {
		Template *MessageTemplate `json:"template,omitempty"`
	}
	if err := json.Unmarshal([]byte(ch.ConfigJSON), &wrapper); err != nil {
		return nil
	}
	return wrapper.Template
}

// PreviewResult 模板预览输出
type PreviewResult struct {
	Title    string   `json:"title"`
	Markdown string   `json:"markdown"`
	HTML     string   `json:"html"`
	Plain    string   `json:"plain"`
	Bytes    int      `json:"bytes"`  // markdown 字节数，用于 UI 显示是否超限
	Errors   []string `json:"errors"` // 自定义模板语法/渲染错误（非空表示已回退到预设）
}

// RenderPreview 用给定模板配置 + Mock 数据渲染消息，仅用于 UI 预览，不发送。
// mockCount > 0 时会复制 base mock 实例到指定条数，便于测试聚合/截断场景。
// 如果 tpl.CustomMarkdown / CustomSubject 有语法或运行时错误，会在 Errors 字段返回但不抛异常。
func (n *Notifier) RenderPreview(tpl *MessageTemplate, resolved bool, mockCount int) PreviewResult {
	res := PreviewResult{}

	// 构造 mock group / instances 并先做默认渲染
	group := mockGroup()
	instances := mockInstancesWithCount(mockCount)
	// 暂时传 nil 走默认渲染，拿到 entries / labels 后再尝试自定义
	defaultTpl := *tpl // 拷贝一份，把 custom 字段置空，先拿默认 markdown 作为兜底
	defaultTpl.CustomMarkdown = ""
	defaultTpl.CustomSubject = ""
	defaultMsg := n.renderWithTemplate(group, instances, &defaultTpl, resolved)
	res.Title = defaultMsg.title
	res.Markdown = defaultMsg.markdown
	res.HTML = defaultMsg.html
	res.Plain = defaultMsg.plain
	res.Bytes = len(defaultMsg.markdown)

	// 没有自定义模板，直接返回默认
	if tpl == nil || (tpl.CustomMarkdown == "" && tpl.CustomSubject == "") {
		return res
	}

	// 显式做一次"完整渲染"测试，把解析+执行错误都捕获到 res.Errors
	// 为了 UI 友好，标题和正文分开尝试
	ctx := buildPreviewContext(group, instances, tpl, resolved, defaultMsg.title, defaultMsg.subtitle)
	if tpl.CustomSubject != "" {
		if out, err := renderCustomTemplate(tpl.CustomSubject, ctx); err != nil {
			res.Errors = append(res.Errors, "标题模板错误: "+err.Error())
		} else {
			res.Title = out
		}
	}
	if tpl.CustomMarkdown != "" {
		if out, err := renderCustomTemplate(tpl.CustomMarkdown, ctx); err != nil {
			res.Errors = append(res.Errors, "正文模板错误: "+err.Error())
		} else {
			res.Markdown = out
			res.Bytes = len(out)
		}
	}
	return res
}

// buildPreviewContext 在预览时构造 TemplateContext。
// 调用方传入由默认渲染得到的 title/subtitle，避免重复计算。
func buildPreviewContext(group *database.AlertGroup, instances []database.AlertInstance, tpl *MessageTemplate, resolved bool, defaultTitle, alertname string) *TemplateContext {
	t := tpl.resolve()
	var cause, impact string
	var rule database.AlertRule
	if len(instances) > 0 {
		if err := database.DB.First(&rule, instances[0].RuleID).Error; err == nil {
			cause = rule.Cause
			impact = rule.Impact
		}
	}
	// 构造 entries：跑一次聚合，把字段填齐
	entries := aggregateInstances(instances)
	dsIDSet := map[uint]struct{}{}
	ruleIDSet := map[uint]struct{}{}
	for _, e := range entries {
		ruleIDSet[e.ruleID] = struct{}{}
		dsIDSet[e.datasourceID] = struct{}{}
	}
	dsIDs := make([]uint, 0, len(dsIDSet))
	for id := range dsIDSet {
		dsIDs = append(dsIDs, id)
	}
	ruleIDs := make([]uint, 0, len(ruleIDSet))
	for id := range ruleIDSet {
		ruleIDs = append(ruleIDs, id)
	}
	dsNames := loadDatasourceNames(dsIDs)
	ruleNames := loadRuleNames(ruleIDs)

	tplEntries := make([]TemplateEntry, 0, len(entries))
	for _, e := range entries {
		var sampleLabels database.AlertInstance // reuse type? actually need LabelSet
		_ = sampleLabels
		labels := map[string]string{}
		if len(e.sampleLabels) > 0 {
			for k, v := range e.sampleLabels[0] {
				labels[k] = v
			}
		}
		host := hostFromLabels(e.sampleLabels[0], t.HostFormat)
		valStr := formatValue(e.minValue, e.maxValue, t.ValuePrecision, t.ShowValueRange)
		fp := ""
		if len(e.sampleFps) > 0 {
			fp = e.sampleFps[0]
		}
		rname := ruleNames[e.ruleID]
		if rname == "" {
			rname = "规则#0"
		}
		dname := dsNames[e.datasourceID]
		if dname == "" {
			dname = "数据源#0"
		}
		tplEntries = append(tplEntries, TemplateEntry{
			Summary:        e.summary,
			State:          e.state,
			Severity:       e.severity,
			RuleID:         e.ruleID,
			RuleName:       rname,
			DatasourceID:   e.datasourceID,
			DatasourceName: dname,
			Host:           host,
			ValueStr:       valStr,
			MinValue:       e.minValue,
			MaxValue:       e.maxValue,
			Threshold:      e.threshold,
			Count:          e.count,
			Time:           e.latest.Format(t.TimeFormat),
			Fingerprint:    fp,
			Labels:         labels,
		})
	}
	sev := ""
	if len(instances) > 0 {
		sev = instances[0].Severity
	}
	return &TemplateContext{
		Title:     defaultTitle,
		Severity:  sev,
		Alertname: alertname,
		Total:     len(instances),
		Cause:     cause,
		Impact:    impact,
		Resolved:  resolved,
		Entries:   tplEntries,
	}
}

// formatValue 提取 value 渲染逻辑（min/max + 精度 + 区间）
func formatValue(min, max float64, precision int, showRange bool) string {
	if !showRange || min == max {
		return fmt.Sprintf("%.*f", precision, min)
	}
	return fmt.Sprintf("%.*f", precision, min) + "~" + fmt.Sprintf("%.*f", precision, max)
}

func mockGroup() *database.AlertGroup {
	return &database.AlertGroup{
		ID:         99999,
		GroupKey:   "preview-mock",
		RouteID:    0,
		LabelsJSON: `{"alertname":"磁盘利用率高于80"}`,
	}
}

// mockInstancesWithCount 返回 count 条 mock 实例。count <= 0 时返回 base 三条
func mockInstancesWithCount(count int) []database.AlertInstance {
	base := mockInstances()
	if count <= 0 || count <= len(base) {
		if count > 0 && count < len(base) {
			return base[:count]
		}
		return base
	}
	// 通过循环复制 base 到指定数量，每条 fingerprint 微调，summary 把数字递增
	out := make([]database.AlertInstance, 0, count)
	for i := 0; i < count; i++ {
		src := base[i%len(base)]
		// 深拷贝 + 调整 fingerprint / value / labels 让聚合有差异
		ai := src
		ai.ID = uint(2000 + i)
		ai.Fingerprint = fmt.Sprintf("mock-fp-%d", i+1)
		ai.Value = src.Value + float64(i%10)*0.1
		// 复制 labels 后改 instance/nodename 让不同 entry 分桶
		var lbs map[string]string
		_ = json.Unmarshal([]byte(src.LabelsJSON), &lbs)
		if lbs == nil {
			lbs = map[string]string{}
		}
		lbs["instance"] = fmt.Sprintf("10.10.%d.%d:9100", 12+(i/254), (i%254)+1)
		lbs["nodename"] = fmt.Sprintf("web%d.idc1.kubehan.cn", i+1)
		b, _ := json.Marshal(lbs)
		ai.LabelsJSON = string(b)
		out = append(out, ai)
	}
	return out
}

func mockInstances() []database.AlertInstance {
	now := time.Now()
	return []database.AlertInstance{
		{
			ID:              1001,
			Fingerprint:     "mock-fp-1",
			RuleID:          0,
			DatasourceID:    0,
			LabelsJSON:      `{"alertname":"磁盘利用率高于80","instance":"10.10.12.70:9100","nodename":"web1.idc1.kubehan.cn","mountpoint":"/home","device":"/dev/vdb1"}`,
			AnnotationsJSON: `{"summary":"主机web1.idc1.kubehan.cn IP地址10.10.12.70:9100 磁盘/home /dev/vdb1 使用率过高了当前值 82.43"}`,
			State:           "firing",
			Severity:        "warning",
			Value:           82.43,
			Threshold:       80,
			ActiveAt:        now,
			LastEvalAt:      now,
		},
		{
			ID:              1002,
			Fingerprint:     "mock-fp-2",
			RuleID:          0,
			DatasourceID:    0,
			LabelsJSON:      `{"alertname":"磁盘利用率高于80","instance":"10.10.12.71:9100","nodename":"web2.idc1.kubehan.cn","mountpoint":"/home","device":"/dev/vdb1"}`,
			AnnotationsJSON: `{"summary":"主机web2.idc1.kubehan.cn IP地址10.10.12.71:9100 磁盘/home /dev/vdb1 使用率过高了当前值 85.43"}`,
			State:           "firing",
			Severity:        "warning",
			Value:           85.43,
			Threshold:       80,
			ActiveAt:        now,
			LastEvalAt:      now,
		},
		{
			ID:              1003,
			Fingerprint:     "mock-fp-3",
			RuleID:          0,
			DatasourceID:    0,
			LabelsJSON:      `{"alertname":"磁盘利用率高于80","instance":"10.10.12.94:9100","nodename":"web10.idc1.kubehan.cn","mountpoint":"/home","device":"/dev/sdf1"}`,
			AnnotationsJSON: `{"summary":"主机web10.idc1.kubehan.cn IP地址10.10.12.94:9100 磁盘/home /dev/sdf1 使用率过高了当前值 85.51"}`,
			State:           "firing",
			Severity:        "warning",
			Value:           85.51,
			Threshold:       80,
			ActiveAt:        now,
			LastEvalAt:      now,
		},
	}
}
