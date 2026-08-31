package piagent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"PromAI/pkg/alerting/webhook"
	"PromAI/pkg/database"
	"PromAI/pkg/metrics"
)

// =============================================================================
// LTS 告警巡检编排（headless）
//
// 输入：告警上下文 + LTS 日志折叠摘要（Java 特化降噪后，2k-4k token）
// 产出：AI 分析结论 + AiAnalysisRecord(Type=lts_alert，含 token 计量与 LogsJSON 留档)
// =============================================================================

// buildLTSAnalysisPrompt 组装 LTS 告警巡检分析 Prompt。
// inspectionSummary 为绑定的巡检模板跑出的指标巡检摘要（可为空，Phase 2 联动）。
func buildLTSAnalysisPrompt(rule *database.AlertTriggerRule, ev *webhook.AlertEvent, summary, inspectionSummary string) string {
	var b strings.Builder
	b.WriteString("你是 PromAI 的资深运维监控专家。请结合告警上下文与华为云 LTS 日志证据，定位本次告警的根因。\n\n")

	b.WriteString("## 告警上下文\n")
	b.WriteString(fmt.Sprintf("- 告警规则: %s\n", ev.RuleName))
	b.WriteString(fmt.Sprintf("- 级别: %s\n", ev.Severity))
	if ev.Value != 0 {
		b.WriteString(fmt.Sprintf("- 触发值: %v\n", ev.Value))
	}
	b.WriteString("- 触发时间: " + ev.OccurredAt.Format("2006-01-02 15:04:05") + "\n")
	if rule != nil && rule.Description != "" {
		b.WriteString(fmt.Sprintf("- 触发规则说明: %s\n", rule.Description))
	}
	if len(ev.Labels) > 0 {
		keys := make([]string, 0, len(ev.Labels))
		for k := range ev.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("- 标签: ")
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, ev.Labels[k]))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}
	if len(ev.Annotations) > 0 {
		b.WriteString("- 注解: ")
		ann := make([]string, 0, len(ev.Annotations))
		for k, v := range ev.Annotations {
			ann = append(ann, fmt.Sprintf("%s=%s", k, v))
		}
		b.WriteString(strings.Join(ann, " | "))
		b.WriteString("\n")
	}

	b.WriteString("\n## LTS 日志证据（Java 应用日志，已降噪折叠）\n")
	b.WriteString(summary)
	b.WriteString("\n（折叠说明：IP/数字/UUID/时间戳已归一为占位符；堆栈已折叠为异常类型+Caused by 链+应用帧前 3 帧，框架帧计为 <framework> x N。）\n")

	if strings.TrimSpace(inspectionSummary) != "" {
		b.WriteString("\n## 关联指标巡检结果（绑定巡检模板，实时采集）\n")
		b.WriteString(inspectionSummary)
		b.WriteString("\n")
	}

	b.WriteString(`
## 输出要求
请用 Markdown 输出，控制在 500 字以内：
1. 🔴 结论：最可能的根因（一句话，尽量关联告警指标与日志证据）
2. 🔍 证据链：引用日志模式（异常类型/logger/traceId）说明如何得出该结论
3. 🛠️ 处置建议：立即措施 + 长期优化
4. ⏰ 观察点：恢复后应关注什么

若日志证据不足，请明确指出告警与日志的关联缺口，以及需要补充检索的关键字或时间窗。`)
	return b.String()
}

// AnalyzeLTSAlertAndRecord 对 LTS 告警做 AI 巡检分析并持久化（含 token 计量与日志留档）。
// refID 用告警指纹；inspectionSummary 为绑定的巡检模板摘要（可为空）；失败不阻断调用方（返回结果 Error 非空）。
func (h *AgentHandler) AnalyzeLTSAlertAndRecord(ctx context.Context, rule *database.AlertTriggerRule, ev *webhook.AlertEvent, refID, summary, logsJSON, inspectionSummary string) (*HeadlessResult, error) {
	prompt := buildLTSAnalysisPrompt(rule, ev, summary, inspectionSummary)
	res, err := h.RunHeadless(ctx, prompt, 0)

	if h.db != nil {
		cost := estimateCost(res.ModelName, res.PromptTokens, res.CompletionTokens)
		rec := database.AiAnalysisRecord{
			Type:             "lts_alert",
			RefID:            refID,
			RuleID:           rule.ID,
			ModelName:        res.ModelName,
			Prompt:           prompt,
			Result:           res.Text,
			Status:           "success",
			DurationMs:       res.Duration.Milliseconds(),
			PromptTokens:     res.PromptTokens,
			CompletionTokens: res.CompletionTokens,
			TotalTokens:      res.TotalTokens,
			CostEst:          cost,
			TokensEstimated:  res.TokensEstimated,
			LogsJSON:         logsJSON,
			CreatedAt:        time.Now(),
		}
		if err != nil {
			rec.Status = "failed"
			rec.Error = err.Error()
		}
		if e := h.db.Create(&rec).Error; e != nil {
			log.Printf("[PiAgent] 保存 LTS 告警巡检分析记录失败: %v", e)
		} else {
			log.Printf("[PiAgent] 已保存 LTS 告警巡检分析记录 (ref=%s rule=%d tokens=%d)", refID, rule.ID, res.TotalTokens)
		}
	}
	return res, err
}

// CollectInspectionSummary 对绑定的巡检模板跑一次实时指标采集，返回压缩后的摘要文本。
// 复用 trigger_inspect 的配置解析（loadToolTemplateMetricConfigs + buildToolMetricTypesFromConfigs），
// 但只采集不落报告记录，摘要作为 LTS 巡检 prompt 的关联证据（Phase 2 联动）。
//
// 失败时返回空串（联动为非关键路径，不阻断 LTS 巡检），错误仅记日志。
func (h *AgentHandler) CollectInspectionSummary(ctx context.Context, templateID uint) string {
	if h == nil || h.collector == nil || h.config == nil || templateID == 0 {
		return ""
	}

	configs, err := loadToolTemplateMetricConfigs(&gormDBWrapper{db: h.db}, []uint{templateID})
	if err != nil {
		log.Printf("[PiAgent] 巡检联动：加载模板[%d]指标配置失败: %v", templateID, err)
		return ""
	}
	if len(configs) == 0 {
		log.Printf("[PiAgent] 巡检联动：模板[%d]无指标配置，跳过", templateID)
		return ""
	}

	runtimeCfg := cloneToolConfig(h.config)
	runtimeCfg.MetricTypes = buildToolMetricTypesFromConfigs(&gormDBWrapper{db: h.db}, configs)

	dataCollector := metrics.NewCollectorWithURL(h.collector.Client, runtimeCfg, runtimeCfg.PrometheusURL)
	data, err := dataCollector.CollectMetricsWithContext(ctx)
	if err != nil {
		log.Printf("[PiAgent] 巡检联动：模板[%d]指标采集失败: %v", templateID, err)
		return ""
	}

	var b strings.Builder
	total, crit, warn := summarizeReport(data)
	b.WriteString(fmt.Sprintf("- 巡检模板 #%d，共 %d 项指标：异常 %d（严重 %d / 警告 %d）\n", templateID, total, crit+warn, crit, warn))
	lines := collectAbnormalMetrics(data)
	if len(lines) == 0 {
		b.WriteString("- 本次未发现异常指标。\n")
	} else {
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}
