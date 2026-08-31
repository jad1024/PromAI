package piagent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"PromAI/pkg/config"
	"PromAI/pkg/database"
	"PromAI/pkg/prometheus"
	"github.com/jay-y/pi/pkg/ai"
	agent "github.com/jay-y/pi/pkg/ai-agent"
	"github.com/prometheus/common/model"
)

type AnalyzeAlertTool struct {
	config *config.Config
	db     DB
}

func (t *AnalyzeAlertTool) GetName() string  { return "analyze_alert" }
func (t *AnalyzeAlertTool) GetLabel() string { return "分析告警" }
func (t *AnalyzeAlertTool) GetDescription() string {
	return "分析告警根因，查询关联指标进行综合判断"
}

func (t *AnalyzeAlertTool) GetParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"metric_name": map[string]any{
				"type":        "string",
				"description": "告警指标名称或告警规则名，例如：CPU性能状态监控",
			},
			"datasource": map[string]any{
				"type":        "string",
				"description": "数据源名称",
			},
			"instance": map[string]any{
				"type":        "string",
				"description": "实例地址，限定分析范围",
			},
		},
		"required": []string{"metric_name"},
	}
}

// resolveAlertTarget 从数据库解析告警对应的真实 PromQL / 阈值配置。
// 解析顺序：AlertRule（精确名 → 模糊名）→ AlertRule.MetricConfigID 指向的指标配置
// → MetricConfig（精确名 → 模糊名）。返回匹配到的规则、指标配置、真实 PromQL、
// 单位、阈值与阈值方向。这替代了原先硬编码 CPU/内存/磁盘 PromQL 的做法，
// 保证告警分析使用「指标配置 / 告警规则」里维护的真实查询与阈值。
func (t *AnalyzeAlertTool) resolveAlertTarget(name string) (*database.AlertRule, *database.MetricConfig, string, string, float64, string) {
	var rule database.AlertRule
	if err := t.db.Model(&database.AlertRule{}).Where("LOWER(name) = LOWER(?)", name).First(&rule).Error(); err != nil {
		rule = database.AlertRule{}
		t.db.Model(&database.AlertRule{}).Where("name LIKE ?", "%"+name+"%").First(&rule)
	}

	var mc database.MetricConfig
	if rule.ID != 0 && rule.MetricConfigID != nil {
		t.db.Model(&database.MetricConfig{}).Where("id = ?", *rule.MetricConfigID).First(&mc)
	}
	if mc.ID == 0 {
		if err := t.db.Model(&database.MetricConfig{}).Where("LOWER(name) = LOWER(?)", name).First(&mc).Error(); err != nil {
			mc = database.MetricConfig{}
			t.db.Model(&database.MetricConfig{}).Where("name LIKE ?", "%"+name+"%").First(&mc)
		}
	}

	query, unit := "", ""
	var threshold float64
	thresholdType := "greater"

	if rule.ID != 0 {
		if rule.Expr != "" {
			query = rule.Expr
		}
		if rule.HasThreshold || rule.SourceType == "custom" {
			threshold = rule.Threshold
			if rule.ThresholdType != "" {
				thresholdType = rule.ThresholdType
			}
		}
	}
	if mc.ID != 0 {
		if query == "" {
			query = mc.Query
		}
		if threshold == 0 {
			threshold = mc.Threshold
			if mc.ThresholdType != "" {
				thresholdType = mc.ThresholdType
			}
		}
		if unit == "" {
			unit = mc.Unit
		}
	}

	var rulePtr *database.AlertRule
	if rule.ID != 0 {
		rulePtr = &rule
	}
	var mcPtr *database.MetricConfig
	if mc.ID != 0 {
		mcPtr = &mc
	}
	return rulePtr, mcPtr, query, unit, threshold, thresholdType
}

// formatThresholdVerdict 依据阈值方向给出「是否触发」的可读结论。
func formatThresholdVerdict(value, threshold float64, thresholdType string) string {
	if threshold == 0 {
		return ""
	}
	switch strings.ToLower(thresholdType) {
	case "less", "lt", "le":
		if value < threshold {
			return " ⚠️ 已触发（低于阈值）"
		}
	case "equal", "eq":
		if value == threshold {
			return " ⚠️ 已触发（等于阈值）"
		}
	case "not_equal", "ne":
		if value != threshold {
			return " ⚠️ 已触发（不等于阈值）"
		}
	default: // greater / ge / gt
		if value > threshold {
			return " ⚠️ 已触发（超过阈值）"
		}
	}
	return " ✅ 未触发"
}

func (t *AnalyzeAlertTool) Execute(ctx context.Context, params map[string]any, onUpdate func(*agent.AgentToolResult)) (*agent.AgentToolResult, error) {
	metricName, _ := params["metric_name"].(string)
	dsParam, _ := params["datasource"].(string)
	instance, _ := params["instance"].(string)
	log.Printf("[PiAgent] 工具调用: analyze_alert metric=%s datasource=%s instance=%s", metricName, dsParam, instance)

	promURL := t.config.PrometheusURL
	promUser := t.config.PrometheusUsername
	promPass := t.config.PrometheusPassword

	if dsParam != "" {
		var ds DataSource
		if t.db.Model(&DataSource{}).Where("enabled = ? AND LOWER(name) = LOWER(?)", true, dsParam).First(&ds).Error() == nil {
			promURL = ds.URL
			promUser = ds.Username
			promPass = ds.Password
		} else {
			like := "%" + dsParam + "%"
			if t.db.Model(&DataSource{}).Where("enabled = ? AND name LIKE ?", true, like).First(&ds).Error() == nil {
				promURL = ds.URL
				promUser = ds.Username
				promPass = ds.Password
			}
		}
	}

	// 从数据库解析真实 PromQL / 阈值（替代硬编码的 CPU/内存/磁盘）
	rule, mc, query, unit, threshold, thresholdType := t.resolveAlertTarget(metricName)

	client, err := prometheus.NewClient(promURL, promUser, promPass)
	if err != nil {
		return &agent.AgentToolResult{
			Content: []ai.ContentBlock{ai.NewTextContentBlock(fmt.Sprintf("创建客户端失败: %v", err))},
		}, nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	lines := []string{}
	lines = append(lines, fmt.Sprintf("🔍 告警分析: %s", metricName))
	lines = append(lines, fmt.Sprintf("数据源: %s", promURL))
	if instance != "" {
		lines = append(lines, fmt.Sprintf("实例: %s", instance))
	}
	lines = append(lines, "")

	// 1) 告警规则 / 指标配置元信息
	if rule != nil {
		lines = append(lines, "📌 告警规则:")
		if rule.Severity != "" {
			lines = append(lines, fmt.Sprintf("  • 级别: %s", rule.Severity))
		}
		if rule.Description != "" {
			lines = append(lines, fmt.Sprintf("  • 描述: %s", rule.Description))
		}
		if rule.Cause != "" {
			lines = append(lines, fmt.Sprintf("  • 已知原因: %s", rule.Cause))
		}
		if rule.Expr != "" {
			lines = append(lines, fmt.Sprintf("  • PromQL: %s", rule.Expr))
		}
	}
	if mc != nil {
		lines = append(lines, "📌 指标配置:")
		lines = append(lines, fmt.Sprintf("  • 指标: %s", mc.Name))
		if query != "" {
			lines = append(lines, fmt.Sprintf("  • PromQL: %s", query))
		}
		if threshold != 0 {
			lines = append(lines, fmt.Sprintf("  • 阈值: %.2f (%s)%s", threshold, thresholdType, unit))
		}
	}
	if rule == nil && mc == nil {
		lines = append(lines, "📌 未在指标配置/告警规则中找到该名称，将仅查询通用关联指标。")
	}
	lines = append(lines, "")

	// 2) 真实 PromQL 查询（当前值）
	if query != "" {
		lines = append(lines, "📈 目标指标（当前值）:")
		result, _, err := client.API.Query(queryCtx, query, time.Now())
		if err != nil {
			lines = append(lines, fmt.Sprintf("  • 查询失败: %v", err))
		} else if vec, ok := result.(model.Vector); ok && len(vec) > 0 {
			avg := 0.0
			for _, s := range vec {
				avg += float64(s.Value)
			}
			avg /= float64(len(vec))
			lines = append(lines, fmt.Sprintf("  • 当前值: %.2f%s (样本数: %d)%s", avg, unit, len(vec), formatThresholdVerdict(avg, threshold, thresholdType)))
		} else {
			lines = append(lines, "  • 无数据")
		}
		lines = append(lines, "")
	}

	// 3) 关联指标（CPU/内存/磁盘，作为综合判断上下文）
	relatedQueries := map[string]string{
		"CPU 使用率": "100 - avg by(instance) (irate(node_cpu_seconds_total{mode='idle'}[5m]) * 100)",
		"内存使用率":   "100 - (node_memory_MemAvailable_bytes * 100 / node_memory_MemTotal_bytes)",
		"磁盘使用率":   "100 - (node_filesystem_avail_bytes * 100 / node_filesystem_size_bytes)",
	}
	if instance != "" {
		for k, q := range relatedQueries {
			relatedQueries[k] = q + fmt.Sprintf(`{instance="%s"}`, instance)
		}
	}

	lines = append(lines, "📈 关联指标（当前值）:")
	for name, q := range relatedQueries {
		result, _, err := client.API.Query(queryCtx, q, time.Now())
		if err != nil {
			lines = append(lines, fmt.Sprintf("  • %s: 查询失败", name))
			continue
		}
		if vec, ok := result.(model.Vector); ok && len(vec) > 0 {
			avg := 0.0
			for _, s := range vec {
				avg += float64(s.Value)
			}
			avg /= float64(len(vec))
			lines = append(lines, fmt.Sprintf("  • %s: %.1f%% (样本数: %d)", name, avg, len(vec)))
		} else {
			lines = append(lines, fmt.Sprintf("  • %s: 无数据", name))
		}
	}

	lines = append(lines, "")

	// 4) 事件聚合上下文（告警降噪后的结构化结论）
	var activeInst int64
	if rule != nil {
		t.db.Model(&database.AlertInstance{}).Where("rule_id = ? AND state IN ?", rule.ID, []string{"firing", "pending"}).Count(&activeInst)
	}
	var totalActive int64
	t.db.Model(&database.AlertInstance{}).Where("state IN ?", []string{"firing", "pending"}).Count(&totalActive)

	lines = append(lines, "🗂 事件聚合:")
	if rule != nil {
		lines = append(lines, fmt.Sprintf("  • 本规则当前活跃实例: %d", activeInst))
	}
	lines = append(lines, fmt.Sprintf("  • 全系统当前活跃告警实例: %d", totalActive))
	if activeInst >= 10 {
		lines = append(lines, "  • ⚠️ 本规则活跃实例较多，疑似告警风暴，请结合事件聚合页确认降噪结论（是否同源/是否震荡）")
	} else if activeInst > 1 {
		lines = append(lines, "  • 本规则存在多个活跃实例，可能为多实例/多集群同源告警，建议关注是否共享根因")
	}
	lines = append(lines, "")

	lines = append(lines, "📋 最近相关巡检记录:")
	var records []ReportRecord
	t.db.Model(&ReportRecord{}).
		Where("datasource_name LIKE ? AND (critical_count > 0 OR warning_count > 0)", "%"+promURL+"%").
		Order("created_at desc").
		Limit(3).
		Find(&records)
	if len(records) == 0 {
		lines = append(lines, "  暂无近期异常记录")
	} else {
		for _, r := range records {
			dt := ""
			if !r.CreatedAt.IsZero() {
				dt = r.CreatedAt.Format("01-02 15:04")
			}
			lines = append(lines, fmt.Sprintf("  • %s — 严重: %d, 警告: %d", dt, r.CriticalCount, r.WarningCount))
		}
	}

	lines = append(lines, "")
	lines = append(lines, "💡 分析建议:")
	lines = append(lines, "  1. 检查关联指标趋势，确认是否为资源瓶颈")
	lines = append(lines, "  2. 对比历史报告，观察指标变化趋势")
	lines = append(lines, "  3. 如需进一步排查，可以触发一次新的巡检")

	return &agent.AgentToolResult{
		Content: []ai.ContentBlock{ai.NewTextContentBlock(strings.Join(lines, "\n"))},
	}, nil
}
