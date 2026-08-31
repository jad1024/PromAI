package piagent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"PromAI/pkg/database"
	"PromAI/pkg/alerting/webhook"
)

// =============================================================================
// LTS 告警巡检编排（headless）
//
// 输入：告警上下文 + LTS 日志折叠摘要（Java 特化降噪后，2k-4k token）
// 产出：AI 分析结论 + AiAnalysisRecord(Type=lts_alert，含 token 计量与 LogsJSON 留档)
// =============================================================================

// buildLTSAnalysisPrompt 组装 LTS 告警巡检分析 Prompt。
func buildLTSAnalysisPrompt(rule *database.AlertTriggerRule, ev *webhook.AlertEvent, summary string) string {
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
// refID 用告警指纹；失败不阻断调用方（返回结果 Error 非空）。
func (h *AgentHandler) AnalyzeLTSAlertAndRecord(ctx context.Context, rule *database.AlertTriggerRule, ev *webhook.AlertEvent, refID, summary, logsJSON string) (*HeadlessResult, error) {
	prompt := buildLTSAnalysisPrompt(rule, ev, summary)
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
