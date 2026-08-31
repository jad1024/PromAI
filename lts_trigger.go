package main

// 华为云 LTS 告警触发 AI 巡检（Phase 1 核心闭环）：
//
//	外部告警(CES/AOM) → 关键字触发规则匹配 → 防抖/冷却/并发上限 → 日预算护栏
//	  → LTS 日志检索 → Java 日志降噪折叠 → headless AI 巡检分析
//	  → AiAnalysisRecord 落库(token 计量 + 日志留档) → push 报告
//
// 挂载点：upsertExternalAlert 写入告警后调用 safeTriggerLTSInspection（异步）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"PromAI/pkg/alerting"
	"PromAI/pkg/alerting/lts"
	"PromAI/pkg/alerting/webhook"
	"PromAI/pkg/database"
	piagent "PromAI/pkg/pi-agent"
)

// LTS 触发防抖：同规则同告警指纹 30 分钟冷却 + 全局并发上限 2。
const ltsCooldown = 30 * time.Minute
const ltsMaxConcurrency = 2

var (
	ltsTriggerMu       sync.Mutex
	ltsTriggerCooldown = map[string]time.Time{}
	ltsTriggerSem      = make(chan struct{}, ltsMaxConcurrency)
)

// ltsTriggerAllowed 冷却判定（同规则+指纹 30 分钟内只触发一次），并顺带清理过期项。
func ltsTriggerAllowed(key string) bool {
	ltsTriggerMu.Lock()
	defer ltsTriggerMu.Unlock()
	if t, ok := ltsTriggerCooldown[key]; ok && time.Since(t) < ltsCooldown {
		return false
	}
	ltsTriggerCooldown[key] = time.Now()
	// 超过 1 万条做一次过期清理，防止 map 无界增长
	if len(ltsTriggerCooldown) > 10000 {
		for k, t := range ltsTriggerCooldown {
			if time.Since(t) >= ltsCooldown {
				delete(ltsTriggerCooldown, k)
			}
		}
	}
	return true
}

// matchEnabledTriggerRules 查询启用且匹配的触发规则（source 限定 + 多条件 AND 匹配）。
func matchEnabledTriggerRules(ev *webhook.AlertEvent, source *database.ExternalAlertSource) []database.AlertTriggerRule {
	var rules []database.AlertTriggerRule
	q := database.DB.Where("enabled = ?", true)
	if source != nil && source.ID != 0 {
		q = q.Where("source_id IS NULL OR source_id = ?", source.ID)
	}
	if err := q.Find(&rules).Error; err != nil {
		log.Printf("[LTSTrigger] 查询触发规则失败: %v", err)
		return nil
	}
	matched := make([]database.AlertTriggerRule, 0, len(rules))
	for _, r := range rules {
		ok, err := lts.MatchRule(&r, ev)
		if err != nil {
			log.Printf("[LTSTrigger] 规则[%d]匹配条件解析失败: %v", r.ID, err)
			continue
		}
		if ok {
			matched = append(matched, r)
		}
	}
	return matched
}

// safeTriggerLTSInspection 外部告警触发 LTS 巡检的入口（异步，防 panic）。
func safeTriggerLTSInspection(ev *webhook.AlertEvent, source *database.ExternalAlertSource, fp string) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[LTSTrigger] LTS 巡检 panic: %v", p)
		}
	}()
	if piagent.DefaultAgentHandler == nil || !piagent.DefaultAgentHandler.AIEnabled() {
		return
	}
	if source == nil || source.AccessKey == "" || source.SecretKey == "" {
		log.Printf("[LTSTrigger] 告警源[%s]未配置 AK/SK，跳过 LTS 巡检", sourceName(source))
		return
	}

	rules := matchEnabledTriggerRules(ev, source)
	if len(rules) == 0 {
		return
	}
	log.Printf("[LTSTrigger] 告警[%s]命中 %d 条 LTS 触发规则", ev.RuleName, len(rules))

	for i := range rules {
		rule := rules[i]
		key := fmt.Sprintf("%d|%s", rule.ID, fp)
		if !ltsTriggerAllowed(key) {
			log.Printf("[LTSTrigger] 规则[%d]在冷却期内，跳过", rule.ID)
			continue
		}
		// 并发上限（非阻塞，满则跳过，避免告警风暴时堆积）
		select {
		case ltsTriggerSem <- struct{}{}:
		default:
			log.Printf("[LTSTrigger] 并发上限已满，规则[%d]跳过", rule.ID)
			continue
		}
		runLTSInspection(ev, source, fp, &rule)
	}
}

func sourceName(source *database.ExternalAlertSource) string {
	if source == nil {
		return "unknown"
	}
	return source.Name
}

// runLTSInspection 单条规则的完整巡检：预算 → 查日志 → 折叠 → AI 分析 → 推送。
func runLTSInspection(ev *webhook.AlertEvent, source *database.ExternalAlertSource, fp string, rule *database.AlertTriggerRule) {
	defer func() {
		<-ltsTriggerSem
		if p := recover(); p != nil {
			log.Printf("[LTSTrigger] 规则[%d]巡检 panic: %v", rule.ID, p)
		}
	}()

	// 日预算护栏：超预算降级为普通通知（在报告里注明）
	if piagent.TokenBudgetExceeded() {
		log.Printf("[LTSTrigger] 日 token 预算耗尽，规则[%d]跳过 AI 巡检，降级为通知", rule.ID)
		pushLTSDegraded(ev, source, rule)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// LTS 检索（时间窗默认 15 分钟，limit 默认 200）
	window := rule.TimeWindowMinutes
	if window <= 0 {
		window = 15
	}
	limit := rule.Limit
	if limit <= 0 {
		limit = 200
	}
	end := time.Now()
	start := end.Add(-time.Duration(window) * time.Minute)

	client := lts.NewClient(source.Region, source.ProjectID, source.AccessKey, source.SecretKey)
	lines, err := client.Query(ctx, lts.QueryParams{
		LogGroupID:  rule.LogGroupID,
		LogStreamID: rule.LogStreamID,
		StartTime:   start,
		EndTime:     end,
		Keywords:    rule.Keywords,
		Limit:       limit,
		IsDesc:      false, // 按时间正序
	})
	if err != nil {
		// 降级：LTS 查询失败不阻断，纯告警分析兜底（在报告注明）
		log.Printf("[LTSTrigger] 规则[%d] LTS 查询失败: %v", rule.ID, err)
		pushLTSDegraded(ev, source, rule)
		return
	}

	folded := lts.FoldJavaLogs(lines, rule.LevelFilter)
	summary := lts.RenderSummary(folded)
	logsJSON := buildLogsJSON(rule, start, end, lines, folded)

	res, err := piagent.DefaultAgentHandler.AnalyzeLTSAlertAndRecord(ctx, rule, ev, fp, summary, logsJSON)
	if err != nil {
		log.Printf("[LTSTrigger] 规则[%d] AI 巡检分析失败: %v", rule.ID, err)
		return
	}
	log.Printf("[LTSTrigger] 规则[%d] AI 巡检完成 fp=%s len=%d tokens=%d", rule.ID, fp[:12], len(res.Text), res.TotalTokens)

	pushLTSReport(ev, source, rule, res.Text, summary)
}

// buildLogsJSON 构造日志证据留档（检索参数 + 折叠模式 + 采样原文）。
func buildLogsJSON(rule *database.AlertTriggerRule, start, end time.Time, lines []string, fr *lts.FoldResult) string {
	type foldedEv struct {
		Signature string `json:"signature"`
		Count     int    `json:"count"`
		FirstAt   string `json:"first_at,omitempty"`
		LastAt    string `json:"last_at,omitempty"`
		Level     string `json:"level,omitempty"`
		Logger    string `json:"logger,omitempty"`
	}
	folded := make([]foldedEv, 0, len(fr.Patterns))
	samples := make([]string, 0, len(fr.Patterns))
	for _, p := range fr.Patterns {
		folded = append(folded, foldedEv{p.Signature, p.Count, p.FirstAt, p.LastAt, p.Level, p.Logger})
		samples = append(samples, p.Sample)
	}
	ev := map[string]any{
		"query": map[string]any{
			"log_group_id":  rule.LogGroupID,
			"log_stream_id": rule.LogStreamID,
			"start_time":    start.Format("2006-01-02 15:04:05"),
			"end_time":      end.Format("2006-01-02 15:04:05"),
			"keywords":      rule.Keywords,
			"limit":         rule.Limit,
			"returned_rows": len(lines),
		},
		"folded":  folded,
		"samples": samples,
	}
	b, _ := json.Marshal(ev)
	return string(b)
}

// pushLTSReport 推送 LTS 巡检报告到规则配置的渠道（空则全部启用渠道）。
func pushLTSReport(ev *webhook.AlertEvent, source *database.ExternalAlertSource, rule *database.AlertTriggerRule, result, summary string) {
	var channels []database.NotificationChannel
	if ids := alerting.DecodeUintSlice(rule.NotifyChannelIDsRaw); len(ids) > 0 {
		database.DB.Where("id IN ?", ids).Find(&channels)
	} else {
		database.DB.Where("enabled = ?", true).Find(&channels)
	}
	if len(channels) == 0 {
		log.Printf("[LTSTrigger] 规则[%d]未配置可用通知渠道，跳过推送", rule.ID)
		return
	}

	title := fmt.Sprintf("🔎 LTS 告警巡检 · %s · %s", sourceName(source), ev.RuleName)
	var b strings.Builder
	fmt.Fprintf(&b, "级别: %s\n", ev.Severity)
	fmt.Fprintf(&b, "规则: %s\n", ev.RuleName)
	if ev.Value != 0 {
		fmt.Fprintf(&b, "触发值: %v\n", ev.Value)
	}
	fmt.Fprintf(&b, "时间: %s\n", ev.OccurredAt.Format("2006-01-02 15:04:05"))
	b.WriteString("\n" + summary + "\n\n---\n" + result)

	ctx := context.Background()
	for i := range channels {
		if err := sendExternalText(ctx, &channels[i], title, b.String()); err != nil {
			log.Printf("[LTSTrigger] 规则[%d]推送渠道[%s]失败: %v", rule.ID, channels[i].Name, err)
		}
	}
}

// pushLTSDegraded 降级通知：预算耗尽或 LTS 查询失败时，只发纯告警文本注明原因。
func pushLTSDegraded(ev *webhook.AlertEvent, source *database.ExternalAlertSource, rule *database.AlertTriggerRule) {
	var channels []database.NotificationChannel
	if ids := alerting.DecodeUintSlice(rule.NotifyChannelIDsRaw); len(ids) > 0 {
		database.DB.Where("id IN ?", ids).Find(&channels)
	} else {
		database.DB.Where("enabled = ?", true).Find(&channels)
	}
	if len(channels) == 0 {
		return
	}
	title := fmt.Sprintf("🔎 LTS 告警巡检 · %s · %s（降级）", sourceName(source), ev.RuleName)
	var b strings.Builder
	fmt.Fprintf(&b, "级别: %s\n", ev.Severity)
	fmt.Fprintf(&b, "规则: %s\n", ev.RuleName)
	fmt.Fprintf(&b, "时间: %s\n", ev.OccurredAt.Format("2006-01-02 15:04:05"))
	if piagent.TokenBudgetExceeded() {
		b.WriteString("\n⚠️ AI 巡检已暂停：日 token 预算耗尽，次日自动恢复。")
	} else {
		b.WriteString("\n⚠️ LTS 日志检索失败，本次仅转发告警文本。")
	}
	ctx := context.Background()
	for i := range channels {
		_ = sendExternalText(ctx, &channels[i], title, b.String())
	}
}
