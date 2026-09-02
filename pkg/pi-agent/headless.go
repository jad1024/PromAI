package piagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/alerting/webhook"
	"PromAI/pkg/database"
	"PromAI/pkg/report"

	"github.com/jay-y/pi/pkg/ai"
	agent "github.com/jay-y/pi/pkg/ai-agent"
)

// =============================================================================
// 无头（Headless）AI 分析入口
//
// 之前的 AI 能力只挂在 HTTP 会话（handleChat）上，后台任务（定时巡检、告警通知）
// 无法直接调用。本文件提供无头调用：复用 newAgent 创建 Agent，但不建立 HTTP 会话，
// 直接 Prompt + Subscribe 收集完整文本返回。
//
// 关键点：newAgent 会 SetTools(h.tools)，因此无头分析时 AI 同样可以自动调用
// query_metrics / analyze_alert / list_reports 等工具获取实时数据，编排函数只需
// 给出任务上下文与输出要求，剩下的交给 Agent 自行取证分析。
// =============================================================================

// HeadlessResult 无头 AI 调用的结果
type HeadlessResult struct {
	Text      string        // AI 完整响应文本（已剔除 think/skill 标记）
	ModelName string        // 实际使用的模型名
	Duration  time.Duration // 耗时
	Error     string        // 模型层错误（如非空则 Text 可能不完整）
}

// DefaultAgentHandler 全局 AI Agent 句柄，由 main.go 在 setupRoutes 时赋值，
// 供告警通知器、定时巡检等后台任务调用无头分析。
var DefaultAgentHandler *AgentHandler

// AIEnabled 是否已配置并启用 AI（无头分析与对话共用）
func (h *AgentHandler) AIEnabled() bool {
	if h == nil || h.config == nil {
		return false
	}
	return h.config.AI.Enabled && len(h.config.AI.Models) > 0
}

// RunHeadless 执行一次无头 Prompt（不创建 HTTP 会话），收集完整响应文本。
// timeout <= 0 时使用默认 90s。
func (h *AgentHandler) RunHeadless(ctx context.Context, prompt string, timeout time.Duration) (*HeadlessResult, error) {
	if h == nil {
		return nil, fmt.Errorf("AI agent handler 未初始化")
	}
	ag, modelName := h.newAgent("")
	if ag == nil {
		return nil, fmt.Errorf("未配置 AI 模型，无法执行无头分析")
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	res := &HeadlessResult{ModelName: modelName}

	var sb strings.Builder
	var runErr error
	done := make(chan struct{})

	unsub := ag.Subscribe(func(event agent.AgentEvent) {
		switch e := event.(type) {
		case *agent.AgentEventMessageUpdate:
			if delta, ok := e.AssistantMessageEvent.(*ai.AssistantMessageEventTextDelta); ok {
				sb.WriteString(stripThinkTags(delta.Delta))
			}
		case *agent.AgentEventTurnEnd:
			if msg, ok := e.Message.(*ai.AssistantMessage); ok {
				if msg.ErrorMessage != "" {
					runErr = fmt.Errorf("%s", msg.ErrorMessage)
				}
				// 部分模型不流式输出，从 turn end 兜底取全文
				for _, block := range msg.Content {
					if tb, ok := block.(*ai.TextContentBlock); ok && tb.Text != "" {
						sb.WriteString(stripThinkTags(tb.Text))
					}
				}
			}
		case *agent.AgentEventEnd:
			close(done)
		}
	})
	defer unsub()

	if err := ag.Prompt(runCtx, prompt); err != nil {
		return res, fmt.Errorf("AI 无头分析启动失败: %w", err)
	}

	select {
	case <-done:
	case <-runCtx.Done():
		return res, fmt.Errorf("AI 无头分析超时（%v）", timeout)
	}

	res.Duration = time.Since(started)
	res.Text = strings.TrimSpace(stripSkillMarkers(sb.String()))
	if runErr != nil {
		res.Error = runErr.Error()
		return res, runErr
	}
	if res.Text == "" {
		return res, fmt.Errorf("AI 无头分析无返回内容")
	}
	log.Printf("[PiAgent] 无头分析完成: 模型=%s 耗时=%v 文本=%dB", modelName, res.Duration, len(res.Text))
	return res, nil
}

// =============================================================================
// 编排函数
// =============================================================================

// AnalyzeInspectionResult 对一次巡检结果做 AI 健康分析。
// 适用于定时巡检完成后生成分析结论（可推送到飞书等渠道）。
func (h *AgentHandler) AnalyzeInspectionResult(ctx context.Context, data *report.ReportData, reportURL string) (*HeadlessResult, error) {
	prompt := buildInspectionAnalysisPrompt(data, reportURL, nil)
	return h.RunHeadless(ctx, prompt, 0)
}

// AnalyzeInspectionWithPrompt 使用自定义提示词执行巡检分析并持久化记录。
// prompt 为空时回退到内置模板；适用于定时任务配置了 ai_analysis_prompt 的场景。
// alertSourceIDs 非空时，巡检分析的告警上下文只保留这些外部告警源的实例（按平台/告警源隔离）。
func (h *AgentHandler) AnalyzeInspectionWithPrompt(ctx context.Context, data *report.ReportData, reportURL, prompt, refID string, alertSourceIDs []uint) (*HeadlessResult, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = buildInspectionAnalysisPrompt(data, reportURL, alertSourceIDs)
	}
	res, err := h.RunHeadless(ctx, prompt, 0)
	if h.db != nil {
		rec := database.AiAnalysisRecord{
			Type:       "inspection",
			RefID:      refID,
			ModelName:  res.ModelName,
			Prompt:     prompt,
			Result:     res.Text,
			Status:     "success",
			DurationMs: res.Duration.Milliseconds(),
			CreatedAt:  time.Now(),
		}
		if err != nil {
			rec.Status = "failed"
			rec.Error = err.Error()
		}
		if err := h.db.Create(&rec).Error; err != nil {
			log.Printf("[PiAgent] 保存巡检 AI 分析记录失败: %v", err)
		} else {
			log.Printf("[PiAgent] 已保存巡检 AI 分析记录 (ref=%s)", refID)
		}
	}
	return res, err
}

// AnalyzeAlertRootCause 对一条告警规则及其活跃实例做 AI 根因分析。
// 适用于告警触发通知时附加分析结论。
func (h *AgentHandler) AnalyzeAlertRootCause(ctx context.Context, rule *database.AlertRule, instances []database.AlertInstance) (*HeadlessResult, error) {
	prompt := h.buildAlertRootCausePrompt(rule, instances)
	return h.RunHeadless(ctx, prompt, 0)
}

// crossClusterAlert 跨集群（数据源）关联告警摘要，作为根因分析上下文。
// 已按「规则 + 集群」折叠：同一规则在同一集群上的多个实例合并为一条，
// 避免 flapping 或批量实例把上下文刷满、浪费 token。
type crossClusterAlert struct {
	RuleName       string
	DatasourceName string
	Severity       string
	Labels         string
	Value          float64
	ActiveAt       time.Time
	// CollapsedCount 折叠的实例数；>1 时 prompt 会标注"（共 N 个实例）"
	CollapsedCount int
	// Flapping 该规则在近 30 分钟内的触发↔恢复来回次数达到阈值，判定为震荡
	Flapping  bool
	FlapCount int
}

// crossClusterRawLimit 跨集群上下文的原始数据拉取上限（聚合前的候选池大小）。
const crossClusterRawLimit = 200

// crossClusterMaxEntries 折叠后进入 AI 上下文的最大条目数。
// 超过则按 严重级别 → 实例数 → 最近活跃 排序截断，保证上下文干净且 token 可控。
const crossClusterMaxEntries = 8

// fetchCrossClusterAlerts 查询当前其他集群（数据源）近 30 分钟内活跃的告警，
// 作为跨集群关联上下文。不同集群的告警可能同源（如共享依赖故障）也可能完全独立，
// 是否关联交由 AI 结合时间/指标/标签判断，不做硬性分组。
//
// 去重策略（避免噪音污染上下文、节省 token）：
//  1. 按「规则 + 集群」折叠：同一规则在同一集群上的 N 个实例合并为 1 条，
//     保留峰值实例的代表性标签，并标注折叠数量；
//  2. 震荡（flapping）规则单独标记：近 30 分钟触发/恢复来回 ≥ 3 次，
//     AI 应降低其权重，不要把它当成跨集群关联的强证据；
//  3. 排序截断到 crossClusterMaxEntries 条。
func (h *AgentHandler) fetchCrossClusterAlerts(instances []database.AlertInstance) []crossClusterAlert {
	if h.db == nil || len(instances) == 0 {
		return nil
	}
	dsIDs := make([]uint, 0, len(instances))
	seen := map[uint]bool{}
	for _, inst := range instances {
		if !seen[inst.DatasourceID] {
			seen[inst.DatasourceID] = true
			dsIDs = append(dsIDs, inst.DatasourceID)
		}
	}
	cutoff := time.Now().Add(-30 * time.Minute)
	var raw []database.AlertInstance
	if err := h.db.Where("state IN ? AND datasource_id NOT IN ? AND last_eval_at > ?",
		[]string{"pending", "firing"}, dsIDs, cutoff).
		Order("active_at DESC").Limit(crossClusterRawLimit).Find(&raw).Error; err != nil || len(raw) == 0 {
		return nil
	}

	// 名称解析（规则名 / 数据源名）
	ruleIDs := make([]uint, 0, len(raw))
	allDsIDs := make([]uint, 0, len(raw))
	seenDs, seenRule := map[uint]bool{}, map[uint]bool{}
	for _, r := range raw {
		if !seenRule[r.RuleID] {
			seenRule[r.RuleID] = true
			ruleIDs = append(ruleIDs, r.RuleID)
		}
		if !seenDs[r.DatasourceID] {
			seenDs[r.DatasourceID] = true
			allDsIDs = append(allDsIDs, r.DatasourceID)
		}
	}
	ruleNames := map[uint]string{}
	var rules []database.AlertRule
	if err := h.db.Select("id, name").Where("id IN ?", ruleIDs).Find(&rules).Error; err == nil {
		for _, r := range rules {
			ruleNames[r.ID] = r.Name
		}
	}
	dsNames := map[uint]string{}
	var dss []database.DataSource
	if err := h.db.Select("id, name").Where("id IN ?", allDsIDs).Find(&dss).Error; err == nil {
		for _, d := range dss {
			dsNames[d.ID] = d.Name
		}
	}

	// 1) 按「规则 + 集群」折叠
	type groupKey struct {
		ruleID uint
		dsID   uint
	}
	type group struct {
		item    crossClusterAlert
		count   int
		peakSev string
		firstAt time.Time
	}
	groups := map[groupKey]*group{}
	order := make([]groupKey, 0, len(raw))
	for _, r := range raw {
		k := groupKey{r.RuleID, r.DatasourceID}
		g, ok := groups[k]
		if !ok {
			labels := r.LabelsJSON
			if len(labels) > 160 {
				labels = labels[:160] + "..."
			}
			g = &group{
				item: crossClusterAlert{
					RuleName:       ruleNames[r.RuleID],
					DatasourceName: dsNames[r.DatasourceID],
					Severity:       r.Severity,
					Labels:         labels,
					Value:          r.Value,
					ActiveAt:       r.ActiveAt,
				},
				peakSev: r.Severity,
				firstAt: r.ActiveAt,
			}
			groups[k] = g
			order = append(order, k)
		}
		g.count++
		// 保留峰值实例的信息（数值最高的实例最具代表性）
		if r.Value > g.item.Value {
			g.item.Value = r.Value
			g.item.ActiveAt = r.ActiveAt
			labels := r.LabelsJSON
			if len(labels) > 160 {
				labels = labels[:160] + "..."
			}
			g.item.Labels = labels
		}
		if severityRank(r.Severity) > severityRank(g.peakSev) {
			g.peakSev = r.Severity
			g.item.Severity = r.Severity
		}
		if r.ActiveAt.Before(g.firstAt) {
			g.firstAt = r.ActiveAt
		}
	}

	// 2) 震荡检测：近 30 分钟各 (规则,集群) 的 firing/resolved 事件计数
	type flapRow struct {
		RuleID       uint
		DatasourceID uint
		EventType    string
		Cnt          int64
	}
	var flapRows []flapRow
	if err := h.db.Model(&database.AlertHistory{}).
		Select("rule_id, datasource_id, event_type, count(*) as cnt").
		Where("rule_id IN ? AND datasource_id IN ? AND event_type IN ? AND COALESCE(occurred_at, created_at) >= ? AND removed_at IS NULL",
			ruleIDs, allDsIDs, []string{"firing", "resolved"}, cutoff).
		Group("rule_id, datasource_id, event_type").Scan(&flapRows).Error; err == nil {
		// 触发与恢复成对出现才可能震荡，用较小的一侧作为来回次数下界
		stats := map[groupKey][2]int64{} // [firing, resolved]
		for _, fr := range flapRows {
			s := stats[groupKey{fr.RuleID, fr.DatasourceID}]
			if fr.EventType == "firing" {
				s[0] += fr.Cnt
			} else {
				s[1] += fr.Cnt
			}
			stats[groupKey{fr.RuleID, fr.DatasourceID}] = s
		}
		for k, s := range stats {
			g, ok := groups[k]
			if !ok {
				continue
			}
			flaps := s[0]
			if s[1] < flaps {
				flaps = s[1]
			}
			g.item.FlapCount = int(flaps)
			g.item.Flapping = flaps >= 3
		}
	}

	// 3) 排序：严重级别 → 是否震荡（震荡靠后，降权）→ 实例数 → 最近活跃
	related := make([]crossClusterAlert, 0, len(order))
	for _, k := range order {
		g := groups[k]
		g.item.CollapsedCount = g.count
		related = append(related, g.item)
	}
	sort.SliceStable(related, func(i, j int) bool {
		if ri, rj := severityRank(related[i].Severity), severityRank(related[j].Severity); ri != rj {
			return ri > rj
		}
		if related[i].Flapping != related[j].Flapping {
			return !related[i].Flapping // 非震荡优先
		}
		if related[i].CollapsedCount != related[j].CollapsedCount {
			return related[i].CollapsedCount > related[j].CollapsedCount
		}
		return related[i].ActiveAt.After(related[j].ActiveAt)
	})
	if len(related) > crossClusterMaxEntries {
		related = related[:crossClusterMaxEntries]
	}
	return related
}

// severityRank 严重级别排序权重，用于跨集群上下文排序。
func severityRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

// buildAlertRootCausePrompt 组装带跨集群关联上下文的告警根因分析 Prompt。
func (h *AgentHandler) buildAlertRootCausePrompt(rule *database.AlertRule, instances []database.AlertInstance) string {
	return buildAlertRootCausePrompt(rule, instances, h.fetchCrossClusterAlerts(instances))
}

// AnalyzeAlertAndRecord 分析告警根因并持久化分析记录（AiAnalysisRecord）。
// 失败时不阻断调用方：返回的部分结果中 Error 非空。
func (h *AgentHandler) AnalyzeAlertAndRecord(ctx context.Context, rule *database.AlertRule, instances []database.AlertInstance, refID string) (*HeadlessResult, error) {
	prompt := h.buildAlertRootCausePrompt(rule, instances)
	res, err := h.RunHeadless(ctx, prompt, 0)
	if h.db != nil {
		rec := database.AiAnalysisRecord{
			Type:       "alert",
			RefID:      refID,
			RuleID:     rule.ID,
			ModelName:  res.ModelName,
			Prompt:     prompt,
			Result:     res.Text,
			Status:     "success",
			DurationMs: res.Duration.Milliseconds(),
			CreatedAt:  time.Now(),
		}
		if err != nil {
			rec.Status = "failed"
			rec.Error = err.Error()
		}
		if err := h.db.Create(&rec).Error; err != nil {
			log.Printf("[PiAgent] 保存告警 AI 分析记录失败: %v", err)
		} else {
			log.Printf("[PiAgent] 已保存告警 AI 分析记录 (ref=%s rule=%d)", refID, rule.ID)
		}
	}
	return res, err
}

// AnalyzeInspectionAndRecord 分析巡检结果并持久化分析记录（AiAnalysisRecord）。
func (h *AgentHandler) AnalyzeInspectionAndRecord(ctx context.Context, data *report.ReportData, reportURL, refID string) (*HeadlessResult, error) {
	return h.AnalyzeInspectionWithPrompt(ctx, data, reportURL, "", refID, nil)
}

// AnalyzeExternalAlertAndRecord 对外部平台（n9e/华为云）告警做根因分析并落库。
// ev 为 webhook 解析出的统一告警事件；refID 用事件指纹。
func (h *AgentHandler) AnalyzeExternalAlertAndRecord(ctx context.Context, sourceName string, ev *webhook.AlertEvent, refID string) (*HeadlessResult, error) {
	prompt := buildExternalAlertPrompt(sourceName, ev)
	res, err := h.RunHeadless(ctx, prompt, 0)
	if h.db != nil {
		rec := database.AiAnalysisRecord{
			Type:       "alert_external",
			RefID:      refID,
			ModelName:  res.ModelName,
			Prompt:     prompt,
			Result:     res.Text,
			Status:     "success",
			DurationMs: res.Duration.Milliseconds(),
			CreatedAt:  time.Now(),
		}
		if err != nil {
			rec.Status = "failed"
			rec.Error = err.Error()
		}
		if e := h.db.Create(&rec).Error; e != nil {
			log.Printf("[PiAgent] 保存外部告警 AI 分析记录失败: %v", e)
		} else {
			log.Printf("[PiAgent] 已保存外部告警 AI 分析记录 (ref=%s)", refID)
		}
	}
	return res, err
}

// buildExternalAlertPrompt 构造外部告警根因分析 Prompt
func buildExternalAlertPrompt(sourceName string, ev *webhook.AlertEvent) string {
	var b strings.Builder
	b.WriteString("你是 PromAI 的资深运维监控专家。请针对一条外部告警做根因分析。\n\n")
	b.WriteString("## 告警信息\n")
	b.WriteString(fmt.Sprintf("- 告警来源: %s\n", sourceName))
	if ev.RuleName != "" {
		b.WriteString(fmt.Sprintf("- 告警规则: %s\n", ev.RuleName))
	}
	b.WriteString(fmt.Sprintf("- 级别: %s\n", ev.Severity))
	if ev.Value != 0 {
		b.WriteString(fmt.Sprintf("- 触发值: %v\n", ev.Value))
	}
	b.WriteString("- 触发时间: " + ev.OccurredAt.Format("2006-01-02 15:04:05") + "\n")
	if len(ev.Labels) > 0 {
		b.WriteString("\n## 标签\n")
		for k, v := range ev.Labels {
			b.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}
	if len(ev.Annotations) > 0 {
		b.WriteString("\n## 注解\n")
		for k, v := range ev.Annotations {
			b.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}

	b.WriteString(`
## 输出要求
请用 Markdown 输出，控制在 300 字以内：
1. 🎯 可能根因：按概率从高到低列出 2-3 个可能原因
2. 🔍 排查建议：给出 2-3 条可执行的排查命令或步骤
3. 🛠 处理建议：简要给出应对措施

如果信息不足，请明确指出需要补充哪些信息。`)
	return b.String()
}

// buildInspectionAnalysisPrompt 构造巡检分析 Prompt
func buildInspectionAnalysisPrompt(data *report.ReportData, reportURL string, alertSourceIDs []uint) string {
	var b strings.Builder
	b.WriteString("你是 PromAI 的资深运维监控专家。请基于下面的巡检结果做一次健康分析。\n\n")
	b.WriteString("## 巡检概览\n")
	if data == nil {
		b.WriteString("- 无巡检数据\n")
	} else {
		b.WriteString(fmt.Sprintf("- 项目: %s\n", data.Project))
		b.WriteString(fmt.Sprintf("- 数据源: %s\n", data.Datasource))
		b.WriteString(fmt.Sprintf("- 巡检时间: %s\n", data.Timestamp.Format("2006-01-02 15:04:05")))
		total, crit, warn := summarizeReport(data)
		b.WriteString(fmt.Sprintf("- 总指标: %d，异常: %d（严重: %d，警告: %d）\n", total, crit+warn, crit, warn))
	}

	b.WriteString("\n## 异常明细\n")
	if data != nil {
		lines := collectAbnormalMetrics(data)
		if len(lines) == 0 {
			b.WriteString("本次巡检未发现异常指标。\n")
		} else {
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	// 当前活跃告警（本地告警 + 外部告警源汇入的实例），便于 AI 结合告警综合判断
	b.WriteString("\n## 当前活跃告警\n")
	if alerts := collectActiveAlerts(15, alertSourceIDs); len(alerts) > 0 {
		b.WriteString(fmt.Sprintf("共 %d 条活跃告警（按触发时间倒序）：\n", len(alerts)))
		b.WriteString(strings.Join(alerts, "\n"))
		b.WriteString("\n（告警为实时查询结果，可能与本次巡检时间点存在秒级偏差）")
	} else {
		b.WriteString("当前无活跃告警。\n")
	}

	// 事件聚合降噪结论（按 alertname 聚合故障，标注风暴），让 AI 用降噪后结论而非逐条平铺
	b.WriteString("\n## 事件聚合降噪\n")
	if incidents := collectIncidentSummary(8, alertSourceIDs); len(incidents) > 0 {
		b.WriteString(strings.Join(incidents, "\n"))
	} else {
		b.WriteString("当前无活跃故障。\n")
	}

	if reportURL != "" {
		b.WriteString(fmt.Sprintf("\n\n完整报告: %s\n", reportURL))
	}

	b.WriteString(`
## 输出要求
请用 Markdown 输出，控制在 400 字以内：
1. 📊 健康总览：一句话概括当前系统状态
2. 🔍 异常分析：逐条说明异常指标的含义与可能原因；若存在活跃告警，请结合告警清单与「事件聚合降噪」结论一并解读（同源故障合并判断、告警风暴单独提示；无异常则说明状态良好）
3. 🛠️ 处理建议：针对异常与活跃告警给出可操作建议
4. ⚠️ 风险提示：需要提前关注的潜在风险

你可以调用 query_metrics / analyze_alert / list_reports 等工具查询实时数据辅助判断。`)
	return b.String()
}

// collectActiveAlerts 收集当前活跃告警（firing/pending），供巡检 AI 分析作为上下文。
// 覆盖本地告警与外部告警源（n9e/华为云）汇入的实例。
// alertSourceIDs 非空时，仅保留 external_source_id 属于该集合的实例（用于按平台/告警源隔离上下文）；
// 为空表示不过滤（沿用全部告警）。
// 每条告警输出完整信息：名称/级别/来源/状态/持续时长/当前值/阈值/标签/注解，
// 避免只传 alertname 导致 AI 分析缺乏上下文。
func collectActiveAlerts(max int, alertSourceIDs []uint) []string {
	if max <= 0 {
		max = 15
	}
	if database.DB == nil {
		return nil
	}
	q := database.DB.Where("state IN ?", []string{"firing", "pending"})
	if len(alertSourceIDs) > 0 {
		q = q.Where("external_source_id IN ?", alertSourceIDs)
	}
	var insts []database.AlertInstance
	if err := q.Order("active_at desc").Limit(max).Find(&insts).Error; err != nil {
		log.Printf("[PiAgent] 查询活跃告警失败: %v", err)
		return nil
	}
	if len(insts) == 0 {
		return nil
	}

	// 规则名映射（本地告警）
	var rules []database.AlertRule
	database.DB.Select("id, name").Find(&rules)
	ruleNames := make(map[uint]string, len(rules))
	for _, r := range rules {
		ruleNames[r.ID] = r.Name
	}
	// 数据源名映射（本地告警）
	var dss []database.DataSource
	database.DB.Select("id, name").Find(&dss)
	dsNames := make(map[uint]string, len(dss))
	for _, d := range dss {
		dsNames[d.ID] = d.Name
	}
	// 外部告警源名映射（n9e/华为云/通用 webhook 等）
	var extSources []database.ExternalAlertSource
	database.DB.Select("id, name, type").Find(&extSources)
	extNames := make(map[uint]string, len(extSources))
	for _, s := range extSources {
		extNames[s.ID] = fmt.Sprintf("%s(%s)", s.Name, s.Type)
	}

	lines := make([]string, 0, len(insts))
	for _, in := range insts {
		labels := parseAlertLabels(in.LabelsJSON)
		name := ruleNames[in.RuleID]
		if name == "" {
			name = labels["alertname"]
		}
		if name == "" {
			name = fmt.Sprintf("告警#%d", in.RuleID)
		}
		ds := dsNames[in.DatasourceID]
		if src := extNames[in.ExternalSourceID]; src != "" {
			ds = src
		}
		if ds == "" {
			ds = "external"
		}
		sev := in.Severity
		if sev == "" {
			sev = "unknown"
		}
		detail := fmt.Sprintf("- [%s] %s（来源: %s，状态: %s，已持续 %s）", sev, name, ds, in.State, durationHuman(time.Since(in.ActiveAt)))

		// 当前值 / 阈值（外部告警与本地告警均有，AI 判断严重程度的关键）
		if in.Value != 0 || in.Threshold != 0 {
			detail += fmt.Sprintf(" 当前值=%.2f 阈值=%.2f", in.Value, in.Threshold)
		}

		// 完整标签（排除已在名称中展示的 alertname，其余全部输出，不挑固定 key）
		labelParts := make([]string, 0, len(labels))
		for k, v := range labels {
			if k == "alertname" || v == "" {
				continue
			}
			labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, truncateValue(v, 120)))
		}
		sort.Strings(labelParts)
		if len(labelParts) > 0 {
			detail += " 标签: " + strings.Join(labelParts, ", ")
		}

		// 注解关键信息（summary/description 等，外部告警解析结果大多在这里）
		if ann := parseAlertLabels(in.AnnotationsJSON); len(ann) > 0 {
			annParts := make([]string, 0, len(ann))
			for k, v := range ann {
				if v == "" {
					continue
				}
				annParts = append(annParts, fmt.Sprintf("%s=%s", k, truncateValue(v, 150)))
			}
			sort.Strings(annParts)
			if len(annParts) > 0 {
				detail += " 注解: " + strings.Join(annParts, " | ")
			}
		}
		lines = append(lines, detail)
	}
	return lines
}

// truncateValue 超长文本截断，防止告警注解/标签值撑爆 prompt
func truncateValue(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// collectIncidentSummary 基于事件聚合模型汇总当前活跃告警的降噪结论。
// 按 alertname 聚合（与事件聚合页一致），输出故障数、各故障涉及的实例/集群数、
// 是否告警风暴，供巡检 AI 分析用降噪后的结构化结论而非平铺实例。
// alertSourceIDs 非空时仅保留 external_source_id 属于该集合的实例（按平台/告警源隔离）。
func collectIncidentSummary(max int, alertSourceIDs []uint) []string {
	if database.DB == nil {
		return nil
	}

	stormThreshold := 10
	windowMinutes := 10
	var settings []database.AppSetting
	if err := database.DB.Where("key IN ?", []string{"alert_denoise_storm_threshold", "alert_denoise_window_minutes"}).Find(&settings).Error; err == nil {
		for _, s := range settings {
			switch s.Key {
			case "alert_denoise_storm_threshold":
				if v, err := strconv.Atoi(s.Value); err == nil && v >= 0 && v <= 1000 {
					stormThreshold = v
				}
			case "alert_denoise_window_minutes":
				if v, err := strconv.Atoi(s.Value); err == nil && v >= 0 && v <= 1440 {
					windowMinutes = v
				}
			}
		}
	}

	q := database.DB.Where("state IN ?", []string{"firing", "pending"})
	if len(alertSourceIDs) > 0 {
		q = q.Where("external_source_id IN ?", alertSourceIDs)
	}
	var insts []database.AlertInstance
	if err := q.Order("active_at desc").Limit(500).Find(&insts).Error; err != nil || len(insts) == 0 {
		return nil
	}

	// 规则名 / 数据源名映射
	var rules []database.AlertRule
	database.DB.Select("id, name").Find(&rules)
	ruleNames := make(map[uint]string, len(rules))
	for _, r := range rules {
		ruleNames[r.ID] = r.Name
	}
	var dss []database.DataSource
	database.DB.Select("id, name").Find(&dss)
	dsNames := make(map[uint]string, len(dss))
	for _, d := range dss {
		dsNames[d.ID] = d.Name
	}

	type agg struct {
		name     string
		severity string
		count    int
		clusters map[uint]struct{}
		lastAt   time.Time
	}
	groups := map[string]*agg{}
	var order []string
	for _, in := range insts {
		labels := parseAlertLabels(in.LabelsJSON)
		name := ruleNames[in.RuleID]
		if name == "" {
			name = labels["alertname"]
		}
		if name == "" {
			name = fmt.Sprintf("告警#%d", in.RuleID)
		}
		g, ok := groups[name]
		if !ok {
			g = &agg{name: name, clusters: map[uint]struct{}{}, lastAt: in.ActiveAt}
			groups[name] = g
			order = append(order, name)
		}
		g.count++
		if severityRank(in.Severity) > severityRank(g.severity) {
			g.severity = in.Severity
		}
		g.clusters[in.DatasourceID] = struct{}{}
		if in.ActiveAt.After(g.lastAt) {
			g.lastAt = in.ActiveAt
		}
	}

	// 排序：实例数降序（风暴/大故障靠前）
	sort.SliceStable(order, func(i, j int) bool {
		return groups[order[i]].count > groups[order[j]].count
	})

	lines := make([]string, 0, len(order)+1)
	totalIncidents := len(order)
	stormCount := 0
	for i, name := range order {
		if max > 0 && i >= max {
			lines = append(lines, fmt.Sprintf("- ... 其余 %d 个故障略", totalIncidents-i))
			break
		}
		g := groups[name]
		storm := g.count >= stormThreshold
		if storm {
			stormCount++
		}
		sev := g.severity
		if sev == "" {
			sev = "unknown"
		}
		stormMark := ""
		if storm {
			stormMark = " ⚠️告警风暴"
		}
		clusterNames := make([]string, 0, len(g.clusters))
		for id := range g.clusters {
			if n := dsNames[id]; n != "" {
				clusterNames = append(clusterNames, n)
			}
		}
		sort.Strings(clusterNames)
		clusterStr := ""
		if len(clusterNames) > 0 && len(clusterNames) <= 4 {
			clusterStr = "（" + strings.Join(clusterNames, ", ") + "）"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s：%d 个实例 / %d 个集群%s%s", sev, g.name, g.count, len(g.clusters), clusterStr, stormMark))
	}

	header := fmt.Sprintf("事件聚合降噪（窗口 %d 分钟，风暴阈值 %d 实例）：共 %d 个故障、%d 个活跃实例，其中 %d 个疑似风暴",
		windowMinutes, stormThreshold, totalIncidents, len(insts), stormCount)
	return append([]string{header}, lines...)
}

// parseAlertLabels 解析告警实例的标签 JSON（兼容数值/布尔值）
func parseAlertLabels(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" {
		return out
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out
	}
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(t)
		default:
			out[k] = fmt.Sprintf("%v", t)
		}
	}
	return out
}

// durationHuman 将时长格式化为可读字符串（如 2h15m / 45m / 30s）
func durationHuman(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dh", h)
}

// buildAlertRootCausePrompt 构造告警根因分析 Prompt。
// related 为其他集群（数据源）当前活跃告警，用于跨集群关联判断（可为空）。
func buildAlertRootCausePrompt(rule *database.AlertRule, instances []database.AlertInstance, related []crossClusterAlert) string {
	var b strings.Builder
	b.WriteString("你是 PromAI 的告警根因分析专家。以下是一条正在触发的告警，请基于告警上下文并结合工具查询到的实时数据，分析其根因。\n\n")
	b.WriteString("## 告警上下文\n")
	if rule != nil {
		b.WriteString(fmt.Sprintf("- 规则名称: %s\n", rule.Name))
		b.WriteString(fmt.Sprintf("- 严重级别: %s\n", rule.Severity))
		if rule.Description != "" {
			b.WriteString(fmt.Sprintf("- 描述: %s\n", rule.Description))
		}
		if rule.Expr != "" {
			b.WriteString(fmt.Sprintf("- PromQL: %s\n", rule.Expr))
		}
		if rule.Cause != "" {
			b.WriteString(fmt.Sprintf("- 已知原因（用户填写）: %s\n", rule.Cause))
		}
		if rule.Impact != "" {
			b.WriteString(fmt.Sprintf("- 已知影响（用户填写）: %s\n", rule.Impact))
		}
		if rule.HasThreshold || rule.SourceType == "custom" {
			b.WriteString(fmt.Sprintf("- 阈值: %.2f（判定: %s）\n", rule.Threshold, rule.ThresholdType))
		}
	}

	b.WriteString("\n## 当前触发实例\n")
	if len(instances) == 0 {
		b.WriteString("- 无实例信息\n")
	} else {
		for i, inst := range instances {
			if i >= 8 {
				b.WriteString(fmt.Sprintf("- ... 其余 %d 条略\n", len(instances)-8))
				break
			}
			labels := inst.LabelsJSON
			if len(labels) > 120 {
				labels = labels[:120] + "..."
			}
			active := ""
			if !inst.ActiveAt.IsZero() {
				active = inst.ActiveAt.Format("01-02 15:04")
			}
			b.WriteString(fmt.Sprintf("- 实例: %s，当前值: %.2f（阈值: %.2f），触发时间: %s\n", labels, inst.Value, inst.Threshold, active))
		}
	}

	if len(related) > 0 {
		b.WriteString("\n## 其他集群当前活跃告警（近30分钟）\n")
		b.WriteString("以下为其他数据源/集群当前活跃的告警，可能与本告警同源（如共享依赖、网络、上游服务故障），也可能完全无关。请结合时间、指标类型、标签综合判断关联性：\n")
		b.WriteString("（已按「规则 + 集群」折叠，括号内为折叠的实例数；震荡规则的触发↔恢复反复，请降低其作为关联证据的权重）\n")
		for _, r := range related {
			active := ""
			if !r.ActiveAt.IsZero() {
				active = r.ActiveAt.Format("01-02 15:04")
			}
			suffix := ""
			if r.CollapsedCount > 1 {
				suffix += fmt.Sprintf("（共 %d 个实例）", r.CollapsedCount)
			}
			if r.Flapping {
				suffix += fmt.Sprintf("[震荡 x%d，勿作为强关联证据]", r.FlapCount)
			}
			b.WriteString(fmt.Sprintf("- [%s][%s] 规则: %s，值: %.2f，触发: %s，标签: %s%s\n",
				r.DatasourceName, r.Severity, r.RuleName, r.Value, active, r.Labels, suffix))
		}
	}

	b.WriteString(`
## 输出要求
请用 Markdown 输出，控制在 500 字以内：
1. 🔴 结论：最可能的根因（一句话）
2. 🔍 推理过程：依据哪些指标/数据得出该结论
3. 📈 关联证据：列出工具查询到的关键数据（CPU/内存/磁盘、历史巡检等）
4. 🔗 跨集群关联：若提供了"其他集群当前活跃告警"，判断与本告警是否同源——关联则指出共同根因，无关则一句话说明本告警为独立事件
5. 🛠️ 处置建议：立即措施 + 长期优化
6. ⏰ 观察点：恢复后应关注什么

你可以调用 analyze_alert / query_metrics / list_reports 等工具获取关联指标、历史巡检记录等实时数据来辅助判断。`)
	return b.String()
}

// summarizeReport 统计总指标与异常数量
func summarizeReport(data *report.ReportData) (total, critical, warning int) {
	for _, group := range data.MetricGroups {
		for _, metrics := range group.MetricsByName {
			for _, m := range metrics {
				total++
				switch m.Status {
				case "critical":
					critical++
				case "warning":
					warning++
				}
			}
		}
	}
	return total, critical, warning
}

// collectAbnormalMetrics 收集异常指标明细行
func collectAbnormalMetrics(data *report.ReportData) []string {
	var lines []string
	for _, group := range data.MetricGroups {
		for _, metrics := range group.MetricsByName {
			for _, m := range metrics {
				if m.Status != "critical" && m.Status != "warning" {
					continue
				}
				line := fmt.Sprintf("- [%s] %s: 当前值=%.2f%s", m.Status, m.Name, m.Value, m.Unit)
				if m.ThresholdType != "" {
					line += fmt.Sprintf("（阈值=%.2f, %s）", m.Threshold, m.ThresholdType)
				}
				if m.BaselineEnabled {
					line += fmt.Sprintf(" [动态基线: 均值=%.2f 标准差=%.2f z=%.2f]", m.BaselineMean, m.BaselineStdDev, m.BaselineZScore)
				}
				for _, l := range m.Labels {
					if l.Value != "" && l.Value != "-" {
						line += fmt.Sprintf(" %s=%s", l.Alias, l.Value)
					}
				}
				lines = append(lines, line)
			}
		}
	}
	return lines
}
