// Package notifier 把告警分组送到通知通道。
//
// 与现有 pkg/notify 的关系：
//   - pkg/notify 的 Send* 函数面向"巡检报告"（reportPath + AlertSummary），
//     消息格式固定为"X 个critical / Y 个warning + 报告链接"。
//   - 告警事件需要列出每一条告警的 labels / value / 时间，不适合复用 Send* 函数。
//   - 因此 notifier 直接调通道底层 webhook / SMTP，但复用 NotificationChannel 表中
//     保存的配置 JSON（schema 与 notify 包一致）。
package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"PromAI/pkg/alerting"
	"PromAI/pkg/alerting/dispatcher"
	"PromAI/pkg/database"
	"PromAI/pkg/notify"
	"PromAI/pkg/utils"

	"github.com/jordan-wright/email"
)

// Notifier 告警通知器：实现 dispatcher.Notifier 接口
type chAttempt struct {
	ch     database.NotificationChannel
	status string
}

// ErrNoChannels 转发自 dispatcher 包，便于直接 import notifier 的调用方使用
var ErrNoChannels = dispatcher.ErrNoChannels

// numberRe 匹配 summary 中可能浮动的数值，用于聚合去重时归一化。
// 覆盖：整数、浮点、百分比、带千分位逗号的数。
var numberRe = regexp.MustCompile(`-?\d+(?:[,\.]\d+)*%?`)

// normalizeSummary 把 summary 中的数值替换为 *，用作聚合 key。
// 例如：
//   "磁盘使用率过高了/home 当前值 82.72"   → "磁盘使用率过高了/home 当前值 *"
//   "磁盘使用率过高了/home 当前值 82.75"   → "磁盘使用率过高了/home 当前值 *"
// 这两条会被聚合为同一条告警，count++，value 记录区间。
func normalizeSummary(s string) string {
	return numberRe.ReplaceAllString(s, "*")
}

type Notifier struct {
	httpClient *http.Client
	// 简单的发送限流：相同 payload_hash 在 throttleWindow 内不重复发送
	throttleWindow time.Duration
	recent         sync.Map // payload_hash → time.Time
	// 单条通知最多展示的「(rule,ds)聚合后」条目数，超过的截断为「其余 N 条略」
	maxEntriesPerMessage int
	// 单条通知 markdown 字节上限（按渠道挑选最严格的；超出时再截断）
	defaultMarkdownByteLimit int
}

// New 创建 Notifier
func New() *Notifier {
	return &Notifier{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
				MaxIdleConns:          50,
				MaxIdleConnsPerHost:   20,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
			},
		},
		throttleWindow:           30 * time.Second,
		maxEntriesPerMessage:     50,
		defaultMarkdownByteLimit: 3800, // 留出 cause/impact/title 的空间，企业微信硬上限 4096
	}
}

// SendGroup 实现 dispatcher.Notifier 接口：把分组下的所有活跃告警发到该路由配置的通道
func (n *Notifier) SendGroup(ctx context.Context, group *database.AlertGroup, instances []database.AlertInstance) error {
	if group == nil || len(instances) == 0 {
		return nil
	}
	startedAt := time.Now()
	ruleID := instances[0].RuleID
	gkShort := group.GroupKey
	if len(gkShort) > 8 {
		gkShort = gkShort[:8]
	}
	log.Printf("[Notify] ▶ group=%s rule=%d instances=%d route=%d 开始派发",
		gkShort, ruleID, len(instances), group.RouteID)

	// 查路由对应的通道列表（路由变更时自动反映）
	var route database.AlertRoute
	if err := database.DB.First(&route, group.RouteID).Error; err != nil {
		log.Printf("[Notify] ✗ group=%s rule=%d 路由 %d 查询失败: %v",
			gkShort, ruleID, group.RouteID, err)
		return fmt.Errorf("route not found: %w", err)
	}
	// 先取路由上的通道，再叠加规则上的通道（合并去重），
	// 这样规则上显式配置的通道不会因路由也配了通道就被忽略。
	routeChIDs := alerting.DecodeUintSlice(route.NotifyChannelIDsRaw)
	channelIDSet := make(map[uint]struct{})
	for _, id := range routeChIDs {
		channelIDSet[id] = struct{}{}
	}
	var rule database.AlertRule
	var ruleChIDs []uint
	if err := database.DB.First(&rule, ruleID).Error; err == nil {
		ruleChIDs = alerting.DecodeUintSlice(rule.NotifyChannelIDsRaw)
		for _, id := range ruleChIDs {
			channelIDSet[id] = struct{}{}
		}
	}
	channelIDs := make([]uint, 0, len(channelIDSet))
	for id := range channelIDSet {
		channelIDs = append(channelIDs, id)
	}
	log.Printf("[Notify] · group=%s rule=%d 候选通道: route=%v rule=%v 合并=%v",
		gkShort, ruleID, routeChIDs, ruleChIDs, channelIDs)

	if len(channelIDs) == 0 {
		log.Printf("[Notify] ✗ group=%s rule=%d 路由(%d) 和规则均未配置 notify_channel_ids → 丢弃",
			gkShort, ruleID, group.RouteID)
		n.logSendError(group, instances, "no_channel", "未配置通知通道：路由和规则均未指定 notify_channel_ids")
		return ErrNoChannels
	}
	var channels []database.NotificationChannel
	if err := database.DB.Where("id IN ? AND enabled = ?", channelIDs, true).Find(&channels).Error; err != nil {
		log.Printf("[Notify] ✗ group=%s rule=%d 查询通道失败: %v", gkShort, ruleID, err)
		return err
	}
	if len(channels) == 0 {
		log.Printf("[Notify] ✗ group=%s rule=%d 候选通道 %v 全部 disabled → 丢弃",
			gkShort, ruleID, channelIDs)
		n.logSendError(group, instances, "all_disabled", fmt.Sprintf("候选通道 %v 全部为 disabled 状态", channelIDs))
		return ErrNoChannels
	}
	chanNames := make([]string, 0, len(channels))
	for _, c := range channels {
		chanNames = append(chanNames, fmt.Sprintf("%s/#%d", c.ChannelType, c.ID))
	}
	log.Printf("[Notify] · group=%s rule=%d 启用通道: %v", gkShort, ruleID, chanNames)

	var firstErr error
	var attempts []chAttempt
	for _, ch := range channels {
		// 每个通道按自身的 template 配置独立渲染
		tpl := parseChannelTemplate(&ch)
		rendered := n.renderWithTemplate(group, instances, tpl, false)
		log.Printf("[Notify] · group=%s rule=%d channel=%s/#%d 渲染(模板style=%s): md=%dB",
			gkShort, ruleID, ch.ChannelType, ch.ID, tpl.resolve().Style, len(rendered.markdown))

		hash := alerting.PayloadHash(ch.ID, rendered.markdown)
		content := rendered.markdown
		if n.shouldThrottle(hash) {
			log.Printf("[Notify] ⊘ group=%s rule=%d channel=%s/#%d throttled (30s 窗口内重复)",
				gkShort, ruleID, ch.ChannelType, ch.ID)
			n.logSend(group, instances, &ch, hash, "throttled", "30 秒内消息内容未变化，已自动去重", content)
			attempts = append(attempts, chAttempt{ch, "throttled"})
			continue
		}
		sendStart := time.Now()
		if err := n.sendOne(ctx, &ch, rendered); err != nil {
			log.Printf("[Notify] ✗ group=%s rule=%d channel=%s/#%d 发送失败(%dms): %v",
				gkShort, ruleID, ch.ChannelType, ch.ID, time.Since(sendStart).Milliseconds(), err)
			n.logSend(group, instances, &ch, hash, "failed", err.Error(), content)
			attempts = append(attempts, chAttempt{ch, "failed"})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		log.Printf("[Notify] ✓ group=%s rule=%d channel=%s/#%d 发送成功(%dms)",
			gkShort, ruleID, ch.ChannelType, ch.ID, time.Since(sendStart).Milliseconds())
		n.logSend(group, instances, &ch, hash, "success", "", content)
		attempts = append(attempts, chAttempt{ch, "success"})
	}
	n.notifyHistoryUpdate(instances, attempts)

	// 总结
	succ, fail, thro := 0, 0, 0
	for _, a := range attempts {
		switch a.status {
		case "success":
			succ++
		case "failed":
			fail++
		case "throttled":
			thro++
		}
	}
	log.Printf("[Notify] ◼ group=%s rule=%d 完成: 成功=%d 失败=%d 限流=%d 耗时=%dms",
		gkShort, ruleID, succ, fail, thro, time.Since(startedAt).Milliseconds())
	return firstErr
}

// SendResolvedGroup 恢复通知：与 SendGroup 类似，但消息标明"已恢复"
func (n *Notifier) SendResolvedGroup(ctx context.Context, group *database.AlertGroup, instances []database.AlertInstance) error {
	if group == nil || len(instances) == 0 {
		return nil
	}
	startedAt := time.Now()
	ruleID := instances[0].RuleID
	gkShort := group.GroupKey
	if len(gkShort) > 8 {
		gkShort = gkShort[:8]
	}
	log.Printf("[Notify] ▶ group=%s rule=%d resolved instances=%d route=%d 开始恢复通知",
		gkShort, ruleID, len(instances), group.RouteID)

	var route database.AlertRoute
	if err := database.DB.First(&route, group.RouteID).Error; err != nil {
		log.Printf("[Notify] ✗ group=%s rule=%d (resolved) 路由查询失败: %v", gkShort, ruleID, err)
		return fmt.Errorf("route not found: %w", err)
	}
	channelIDSet := make(map[uint]struct{})
	for _, id := range alerting.DecodeUintSlice(route.NotifyChannelIDsRaw) {
		channelIDSet[id] = struct{}{}
	}
	var rule database.AlertRule
	if err := database.DB.First(&rule, ruleID).Error; err == nil {
		for _, id := range alerting.DecodeUintSlice(rule.NotifyChannelIDsRaw) {
			channelIDSet[id] = struct{}{}
		}
	}
	channelIDs := make([]uint, 0, len(channelIDSet))
	for id := range channelIDSet {
		channelIDs = append(channelIDs, id)
	}
	if len(channelIDs) == 0 {
		log.Printf("[Notify] · group=%s rule=%d (resolved) 无通道配置 → 跳过", gkShort, ruleID)
		return nil
	}
	var channels []database.NotificationChannel
	if err := database.DB.Where("id IN ? AND enabled = ?", channelIDs, true).Find(&channels).Error; err != nil {
		log.Printf("[Notify] ✗ group=%s rule=%d (resolved) 查询通道失败: %v", gkShort, ruleID, err)
		return err
	}
	if len(channels) == 0 {
		log.Printf("[Notify] · group=%s rule=%d (resolved) 候选通道全部 disabled → 跳过", gkShort, ruleID)
		return nil
	}

	var firstErr error
	var attempts []chAttempt
	for _, ch := range channels {
		// 每个通道按自身的 template 配置独立渲染
		tpl := parseChannelTemplate(&ch)
		rendered := n.renderWithTemplate(group, instances, tpl, true)
		log.Printf("[Notify] · group=%s rule=%d (resolved) channel=%s/#%d 渲染: md=%dB",
			gkShort, ruleID, ch.ChannelType, ch.ID, len(rendered.markdown))

		hash := alerting.PayloadHash(ch.ID, rendered.markdown)
		content := rendered.markdown
		if n.shouldThrottle(hash) {
			log.Printf("[Notify] ⊘ group=%s rule=%d (resolved) channel=%s/#%d throttled",
				gkShort, ruleID, ch.ChannelType, ch.ID)
			n.logSend(group, instances, &ch, hash, "throttled", "30 秒内消息内容未变化，已自动去重", content)
			continue
		}
		sendStart := time.Now()
		if err := n.sendOne(ctx, &ch, rendered); err != nil {
			log.Printf("[Notify] ✗ group=%s rule=%d (resolved) channel=%s/#%d 发送失败(%dms): %v",
				gkShort, ruleID, ch.ChannelType, ch.ID, time.Since(sendStart).Milliseconds(), err)
			n.logSend(group, instances, &ch, hash, "failed", err.Error(), content)
			attempts = append(attempts, chAttempt{ch, "failed"})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		log.Printf("[Notify] ✓ group=%s rule=%d (resolved) channel=%s/#%d 发送成功(%dms)",
			gkShort, ruleID, ch.ChannelType, ch.ID, time.Since(sendStart).Milliseconds())
		n.logSend(group, instances, &ch, hash, "success", "", content)
		attempts = append(attempts, chAttempt{ch, "success"})
	}
	n.notifyHistoryUpdate(instances, attempts)
	log.Printf("[Notify] ◼ group=%s rule=%d (resolved) 完成: 通道数=%d 耗时=%dms",
		gkShort, ruleID, len(attempts), time.Since(startedAt).Milliseconds())
	return firstErr
}

func (n *Notifier) shouldThrottle(hash string) bool {
	if v, ok := n.recent.Load(hash); ok {
		if t, ok := v.(time.Time); ok && time.Since(t) < n.throttleWindow {
			return true
		}
	}
	n.recent.Store(hash, time.Now())
	// 简单清理：随机扫一遍过期项
	if time.Now().UnixNano()%32 == 0 {
		cutoff := time.Now().Add(-n.throttleWindow * 4)
		n.recent.Range(func(k, v interface{}) bool {
			if t, ok := v.(time.Time); ok && t.Before(cutoff) {
				n.recent.Delete(k)
			}
			return true
		})
	}
	return false
}

func (n *Notifier) logSend(group *database.AlertGroup, instances []database.AlertInstance, ch *database.NotificationChannel, payloadHash, status, errMsg, content string) {
	ruleID := uint(0)
	if len(instances) > 0 {
		ruleID = instances[0].RuleID
	}
	row := database.AlertNotifyLog{
		GroupKey:    group.GroupKey,
		RuleID:      ruleID,
		ChannelID:   ch.ID,
		ChannelType: ch.ChannelType,
		Status:      status,
		Error:       errMsg,
		PayloadHash: payloadHash,
		AlertCount:  len(instances),
		Content:     content,
		SentAt:      time.Now(),
	}
	_ = database.DB.Create(&row).Error
}

// logSendError 记录无候选通道 / 通道全部禁用等"未发出"的失败，便于用户在告警实例详情中看到
func (n *Notifier) logSendError(group *database.AlertGroup, instances []database.AlertInstance, errCode, errMsg string) {
	ruleID := uint(0)
	if len(instances) > 0 {
		ruleID = instances[0].RuleID
	}
	row := database.AlertNotifyLog{
		GroupKey:    group.GroupKey,
		RuleID:      ruleID,
		ChannelID:   0,
		ChannelType: errCode,
		Status:      "failed",
		Error:       errMsg,
		AlertCount:  len(instances),
		SentAt:      time.Now(),
	}
	_ = database.DB.Create(&row).Error
}

// notifyHistoryUpdate 更新告警历史中对应实例的通知渠道和结果
func (n *Notifier) notifyHistoryUpdate(instances []database.AlertInstance, attempts []chAttempt) {
	if len(instances) == 0 || len(attempts) == 0 {
		return
	}
	type chInfo struct {
		ID   uint   `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	chList := make([]chInfo, 0, len(attempts))
	result := "success"
	for _, a := range attempts {
		chList = append(chList, chInfo{ID: a.ch.ID, Type: a.ch.ChannelType, Name: a.ch.Name})
		if a.status == "failed" {
			result = "failed"
		} else if a.status == "throttled" && result != "failed" {
			result = "throttled"
		}
	}
	chJSON, _ := json.Marshal(chList)
	chStr := string(chJSON)
	if chStr == "" || chStr == "null" {
		return
	}

	fps := make([]string, 0, len(instances))
	for _, inst := range instances {
		if inst.Fingerprint != "" {
			fps = append(fps, inst.Fingerprint)
		}
	}
	if len(fps) == 0 {
		return
	}
	_ = database.DB.Exec(`
		UPDATE alert_histories
		SET notify_channels = ?, notify_result = ?
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY fingerprint ORDER BY occurred_at DESC) AS rn
				FROM alert_histories
				WHERE fingerprint IN (?)
			) sub WHERE sub.rn = 1
		)
	`, chStr, result, fps)
}

// renderedMessage 通用消息载荷
type renderedMessage struct {
	title    string
	subtitle string
	markdown string
	plain    string
	// 用于邮件 HTML
	html string
}

// aggregatedEntry 把同一 (rule_id, ds_id) 下多条同质实例合并展示，避免告警轰炸
type aggregatedEntry struct {
	ruleID         uint
	datasourceID   uint
	state          string  // pending/firing/resolved (取第一条)
	severity       string
	summary        string  // 聚合的汇总文本（来自 annotations）
	count          int     // 这个聚合命中多少条实例
	minValue       float64
	maxValue       float64
	threshold      float64
	earliest       time.Time
	latest         time.Time
	sampleLabels   []alerting.LabelSet // 抽样最多 3 条用作示例
	sampleFps      []string            // 对应 fingerprint，用于详情链接
}

// aggregateInstances 按"告警内容"聚合：(rule_id, ds_id, 归一化 summary) 三元组。
// 归一化 summary 会把数值（包括 {{ $value }} 渲染出的浮点）替换为 *，
// 因此同一逻辑告警仅数值漂移的多次 firing 会被合并为一条，count++ 并保留 value 区间，
// 而不同 mountpoint / device / instance 等会产生不同 summary 文本，各自独立条目，不漏告警。
func aggregateInstances(instances []database.AlertInstance) []aggregatedEntry {
	type bucketKey struct {
		ruleID    uint
		dsID      uint
		summaryNk string // normalized summary
	}
	buckets := make(map[bucketKey]*aggregatedEntry)
	keys := make([]bucketKey, 0)
	for _, ai := range instances {
		var ann alerting.LabelSet
		_ = json.Unmarshal([]byte(ai.AnnotationsJSON), &ann)
		summary := ann["summary"]
		if summary == "" {
			summary = ann["description"]
		}
		k := bucketKey{
			ruleID:    ai.RuleID,
			dsID:      ai.DatasourceID,
			summaryNk: normalizeSummary(summary),
		}
		entry, ok := buckets[k]
		if !ok {
			entry = &aggregatedEntry{
				ruleID:       ai.RuleID,
				datasourceID: ai.DatasourceID,
				state:        ai.State,
				severity:     ai.Severity,
				summary:      summary, // 展示用第一条原文
				minValue:     ai.Value,
				maxValue:     ai.Value,
				threshold:    ai.Threshold,
				earliest:     ai.LastEvalAt,
				latest:       ai.LastEvalAt,
			}
			buckets[k] = entry
			keys = append(keys, k)
		}
		entry.count++
		if ai.Value < entry.minValue {
			entry.minValue = ai.Value
		}
		if ai.Value > entry.maxValue {
			entry.maxValue = ai.Value
		}
		if ai.LastEvalAt.Before(entry.earliest) {
			entry.earliest = ai.LastEvalAt
		}
		if ai.LastEvalAt.After(entry.latest) {
			entry.latest = ai.LastEvalAt
		}
		// 抽样至多 3 条，保留 labels 和 fingerprint
		if len(entry.sampleLabels) < 3 {
			var labels alerting.LabelSet
			_ = json.Unmarshal([]byte(ai.LabelsJSON), &labels)
			entry.sampleLabels = append(entry.sampleLabels, labels)
			entry.sampleFps = append(entry.sampleFps, ai.Fingerprint)
		}
	}
	out := make([]aggregatedEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, *buckets[k])
	}
	return out
}

// ruleByID/datasourceByID 用于查询规则/数据源名展示
func loadRuleNames(ids []uint) map[uint]string {
	if len(ids) == 0 {
		return nil
	}
	var rows []database.AlertRule
	database.DB.Where("id IN ?", ids).Find(&rows)
	m := make(map[uint]string, len(rows))
	for _, r := range rows {
		m[r.ID] = r.Name
	}
	return m
}

func loadDatasourceNames(ids []uint) map[uint]string {
	if len(ids) == 0 {
		return nil
	}
	var rows []database.DataSource
	database.DB.Where("id IN ?", ids).Find(&rows)
	m := make(map[uint]string, len(rows))
	for _, r := range rows {
		m[r.ID] = r.Name
	}
	return m
}

// hostFromLabels 根据模板配置抽取主机字符串
//
//	full     → instance label 原样
//	short    → 取域名第一段（"web1.idc1.x.com:9100" → "web1"）
//	with_ip  → "<short> (<ip>)"
//
// 找不到合适的 label 时返回空串，让调用方决定是否省略整段
func hostFromLabels(labels alerting.LabelSet, style string) string {
	inst := labels["instance"]
	host := labels["nodename"]
	ip := ""
	// 从 instance 拆 host:port 中的 IP（如果像 IP）
	if inst != "" {
		if idx := strings.Index(inst, ":"); idx > 0 {
			ip = inst[:idx]
		} else {
			ip = inst
		}
	}
	if host == "" {
		host = ip
	}
	switch style {
	case "full":
		if host != "" {
			return host
		}
		return inst
	case "with_ip":
		short := host
		if dot := strings.Index(host, "."); dot > 0 {
			short = host[:dot]
		}
		if short != "" && ip != "" && short != ip {
			return fmt.Sprintf("%s (%s)", short, ip)
		}
		if short != "" {
			return short
		}
		return inst
	default: // short
		if dot := strings.Index(host, "."); dot > 0 {
			return host[:dot]
		}
		return host
	}
}

// renderWithTemplate 按指定模板配置渲染告警消息。
// tpl 可为 nil（使用默认模板）。
// resolved=true 表示渲染恢复通知。
func (n *Notifier) renderWithTemplate(group *database.AlertGroup, instances []database.AlertInstance, tpl *MessageTemplate, resolved bool) renderedMessage {
	t := tpl.resolve()

	// 加载规则
	var rule database.AlertRule
	var cause, impact string
	if len(instances) > 0 {
		if err := database.DB.First(&rule, instances[0].RuleID).Error; err == nil {
			cause = strings.TrimSpace(rule.Cause)
			impact = strings.TrimSpace(rule.Impact)
		}
	}

	entries := aggregateInstances(instances)
	ruleIDSet := make(map[uint]struct{})
	dsIDSet := make(map[uint]struct{})
	for _, e := range entries {
		ruleIDSet[e.ruleID] = struct{}{}
		dsIDSet[e.datasourceID] = struct{}{}
	}
	ruleIDs := make([]uint, 0, len(ruleIDSet))
	for id := range ruleIDSet {
		ruleIDs = append(ruleIDs, id)
	}
	dsIDs := make([]uint, 0, len(dsIDSet))
	for id := range dsIDSet {
		dsIDs = append(dsIDs, id)
	}
	dsNames := loadDatasourceNames(dsIDs)
	ruleNames := loadRuleNames(ruleIDs)

	// 标题
	sev := ""
	alertname := ""
	if len(instances) > 0 {
		sev = strings.ToUpper(instances[0].Severity)
		var labels alerting.LabelSet
		_ = json.Unmarshal([]byte(instances[0].LabelsJSON), &labels)
		alertname = labels["alertname"]
		if alertname == "" {
			var glabels alerting.LabelSet
			_ = json.Unmarshal([]byte(group.LabelsJSON), &glabels)
			alertname = glabels["alertname"]
		}
	}
	titlePrefix := fmt.Sprintf("[%s]", sev)
	if resolved {
		titlePrefix = "[已恢复] " + sev
	}
	// 标题格式：用户自定义或默认
	var title string
	if t.TitleFormat != "" {
		title = t.TitleFormat
		title = strings.ReplaceAll(title, "{severity}", sev)
		title = strings.ReplaceAll(title, "{alertname}", alertname)
		title = strings.ReplaceAll(title, "{total}", fmt.Sprintf("%d", len(instances)))
		title = strings.ReplaceAll(title, "{bucketCount}", fmt.Sprintf("%d", len(entries)))
	} else {
		title = fmt.Sprintf("%s %s", titlePrefix, alertname)
		if len(instances) == len(entries) {
			title += fmt.Sprintf(" · 共 %d 条", len(instances))
		} else {
			title += fmt.Sprintf(" · 共 %d 条 (聚合 %d 项)", len(instances), len(entries))
		}
	}

	port := utils.GetGlobalPort()
	if port == "" {
		port = "8091"
	}
	publicBase := utils.GetGlobalPublicURL()
	if publicBase == "" {
		publicBase = fmt.Sprintf("http://127.0.0.1:%s", port)
	}
	baseURL := strings.TrimRight(publicBase, "/") + "/promai/alerts?fingerprint="

	var mdBuf, plainBuf, htmlBuf bytes.Buffer
	if resolved {
		mdBuf.WriteString("# ✅ ")
		htmlBuf.WriteString("<h2>✅ ")
	} else {
		mdBuf.WriteString("# ")
		htmlBuf.WriteString("<h2>")
	}
	mdBuf.WriteString(title)
	mdBuf.WriteString("\n\n")
	htmlBuf.WriteString(title)
	htmlBuf.WriteString("</h2>")

	if t.ShowCause && cause != "" {
		mdBuf.WriteString(fmt.Sprintf("> **可能原因**: %s\n\n", cause))
		htmlBuf.WriteString(fmt.Sprintf("<p><b>可能原因:</b> %s</p>", cause))
		plainBuf.WriteString(fmt.Sprintf("可能原因: %s\n", cause))
	}
	if t.ShowImpact && impact != "" {
		mdBuf.WriteString(fmt.Sprintf("> **影响范围**: %s\n\n", impact))
		htmlBuf.WriteString(fmt.Sprintf("<p><b>影响范围:</b> %s</p>", impact))
		plainBuf.WriteString(fmt.Sprintf("影响范围: %s\n", impact))
	}
	htmlBuf.WriteString("<ul>")

	truncated := 0
	valFmt := fmt.Sprintf("%%.%df", t.ValuePrecision)
	// 同步构造 TemplateEntry 列表（自定义 Go template 用）
	tplEntries := make([]TemplateEntry, 0, len(entries))
	for i, e := range entries {
		if i >= t.MaxEntries {
			truncated = len(entries) - i
			break
		}
		// 数值（按精度，区间）
		valStr := fmt.Sprintf(valFmt, e.minValue)
		if t.ShowValueRange && e.minValue != e.maxValue {
			valStr = fmt.Sprintf(valFmt+"~"+valFmt, e.minValue, e.maxValue)
		}
		thStr := fmt.Sprintf("%g", e.threshold)
		dname := dsNames[e.datasourceID]
		if dname == "" {
			dname = fmt.Sprintf("数据源#%d", e.datasourceID)
		}

		// 主机展示
		var sampleLabels alerting.LabelSet
		if len(e.sampleLabels) > 0 {
			sampleLabels = e.sampleLabels[0]
		}
		host := hostFromLabels(sampleLabels, t.HostFormat)

		// 命中处数（只在 count>1 时显示）
		hitStr := ""
		if t.ShowHitCount && e.count > 1 {
			hitStr = fmt.Sprintf(" · 命中 %d 处", e.count)
		}

		// 时间
		timeStr := ""
		if t.ShowTime {
			timeStr = e.latest.Format(t.TimeFormat)
		}

		// 数据源
		dsStr := ""
		if t.ShowDatasource {
			dsStr = dname
		}

		// 详情链接（markdown）
		detailMD := ""
		detailHTML := ""
		detailURL := ""
		if t.ShowDetailLink && len(e.sampleFps) > 0 {
			detailURL = baseURL + e.sampleFps[0]
			detailMD = fmt.Sprintf("[详情](%s)", detailURL)
			detailHTML = fmt.Sprintf(`<a href="%s">详情</a>`, detailURL)
		}

		// 收集到 TemplateEntry（供自定义 Go template 使用）
		rname := ruleNames[e.ruleID]
		if rname == "" {
			rname = fmt.Sprintf("规则#%d", e.ruleID)
		}
		entryLabels := map[string]string{}
		for k, v := range sampleLabels {
			entryLabels[k] = v
		}
		fp := ""
		if len(e.sampleFps) > 0 {
			fp = e.sampleFps[0]
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
			Time:           timeStr,
			Fingerprint:    fp,
			DetailURL:      detailURL,
			Labels:         entryLabels,
		})

		// 拼接：根据 style 决定每项格式（目前先 simple，table/card 后续）
		var mdChunk string
		switch t.Style {
		case "table":
			// 单行表格风格（每项一行 | 分隔）
			mdChunk = fmt.Sprintf("| %s | %s | %s | %s%s | %s | %s |\n",
				host, e.summary, valStr, thStr, hitStr, timeStr, detailMD)
		case "card":
			// 卡片风格：更突出，多用图标
			mdChunk = fmt.Sprintf("**▎ %s**\n", e.summary)
			if host != "" {
				mdChunk += fmt.Sprintf("   📍 %s\n", host)
			}
			mdChunk += fmt.Sprintf("   📊 当前 %s · 阈值 %s%s\n", valStr, thStr, hitStr)
			if timeStr != "" || dsStr != "" {
				parts := []string{}
				if timeStr != "" {
					parts = append(parts, "🕐 "+timeStr)
				}
				if dsStr != "" {
					parts = append(parts, "📡 "+dsStr)
				}
				if detailMD != "" {
					parts = append(parts, "🔗 "+detailMD)
				}
				mdChunk += "   " + strings.Join(parts, " · ") + "\n"
			}
			mdChunk += "\n"
		default: // simple
			// 单条紧凑 3 行
			mdChunk = fmt.Sprintf("▸ %s\n   value=%s / 阈值=%s%s\n",
				e.summary, valStr, thStr, hitStr)
			tail := []string{}
			if timeStr != "" {
				tail = append(tail, timeStr)
			}
			if dsStr != "" {
				tail = append(tail, dsStr)
			}
			if detailMD != "" {
				tail = append(tail, detailMD)
			}
			if len(tail) > 0 {
				mdChunk += "   " + strings.Join(tail, " · ") + "\n"
			}
			mdChunk += "\n"
		}

		// 字节预算
		if mdBuf.Len()+len(mdChunk) > t.MaxBytes && i > 0 {
			truncated = len(entries) - i
			break
		}
		mdBuf.WriteString(mdChunk)

		// HTML
		htmlBuf.WriteString(fmt.Sprintf(
			"<li><b>%s</b><br/>value=%s / 阈值=%s%s · %s · %s %s</li>",
			e.summary, valStr, thStr, hitStr, timeStr, dsStr, detailHTML,
		))
		// Plain
		plainBuf.WriteString(fmt.Sprintf("%s | value=%s 阈值=%s | x%d | %s\n",
			e.summary, valStr, thStr, e.count, dsStr))
	}
	if truncated > 0 {
		mdBuf.WriteString(fmt.Sprintf("\n…（其余 %d 项已聚合略，请到 PromAI 查看完整列表）\n", truncated))
		htmlBuf.WriteString(fmt.Sprintf("<li>…其余 %d 项已聚合略</li>", truncated))
		plainBuf.WriteString(fmt.Sprintf("…其余 %d 项已聚合略\n", truncated))
	}
	htmlBuf.WriteString("</ul>")

	md := mdBuf.String()
	// 最后一道保险：硬截断到 MaxBytes
	if len(md) > t.MaxBytes {
		cut := t.MaxBytes - 200
		if cut > 0 && cut < len(md) {
			md = md[:cut] + "\n\n…（消息超出渠道长度上限，已截断。请到 PromAI 查看完整告警列表）\n"
		}
	}

	// === 高级：自定义 Go template 覆盖 ===
	// 仅当 CustomMarkdown / CustomSubject 非空时启用；任意环节失败则回退到上面的默认渲染并打印警告
	if t.CustomMarkdown != "" || t.CustomSubject != "" {
		ctx := &TemplateContext{
			Title:     title,
			Severity:  sev,
			Alertname: alertname,
			Total:     len(instances),
			Cause:     cause,
			Impact:    impact,
			Resolved:  resolved,
			BaseURL:   baseURL,
			Entries:   tplEntries,
		}
		if t.CustomSubject != "" {
			if out, err := renderCustomTemplate(t.CustomSubject, ctx); err != nil {
				log.Printf("[Notify] custom_subject 渲染失败，回退默认: %v", err)
			} else {
				title = out
				ctx.Title = out // 让 markdown 里 {{.Title}} 拿到新标题
			}
		}
		if t.CustomMarkdown != "" {
			if out, err := renderCustomTemplate(t.CustomMarkdown, ctx); err != nil {
				log.Printf("[Notify] custom_markdown 渲染失败，回退默认: %v", err)
			} else {
				md = out
				// 自定义模板的字节上限也要兜底
				if len(md) > t.MaxBytes {
					cut := t.MaxBytes - 200
					if cut > 0 && cut < len(md) {
						md = md[:cut] + "\n\n…（自定义模板渲染超出长度上限，已截断）\n"
					}
				}
			}
		}
	}

	return renderedMessage{
		title:    title,
		subtitle: alertname,
		markdown: md,
		plain:    plainBuf.String(),
		html:     htmlBuf.String(),
	}
}

// render 渲染告警消息（向后兼容：不指定模板时用默认）
func (n *Notifier) render(group *database.AlertGroup, instances []database.AlertInstance) renderedMessage {
	return n.renderWithTemplate(group, instances, nil, false)
}

// renderResolved 渲染恢复通知消息
func (n *Notifier) renderResolved(group *database.AlertGroup, instances []database.AlertInstance) renderedMessage {
	return n.renderWithTemplate(group, instances, nil, true)
}

func buildLabelKV(labels alerting.LabelSet) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("`%s=%s`", k, v))
	}
	return strings.Join(parts, " ")
}

// sendOne 分发到具体通道
func (n *Notifier) sendOne(ctx context.Context, ch *database.NotificationChannel, msg renderedMessage) error {
	switch ch.ChannelType {
	case "dingtalk":
		var cfg notify.DingtalkConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return n.sendDingtalk(ctx, cfg, msg)
	case "wechat_work":
		var cfg notify.WeChatWorkConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return n.sendWeChatWork(ctx, cfg, msg)
	case "feishu":
		var cfg notify.FeishuConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return n.sendFeishu(ctx, cfg, msg)
	case "email":
		var cfg notify.EmailConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return n.sendEmail(cfg, msg)
	case "wechat_app":
		var cfg notify.WeChatAppConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return n.sendWeChatApp(ctx, cfg, msg)
	case "webhook":
		var cfg notify.WebhookConfig
		if err := json.Unmarshal([]byte(ch.ConfigJSON), &cfg); err != nil {
			return err
		}
		return n.sendWebhook(ctx, cfg, msg)
	default:
		return fmt.Errorf("unsupported channel type: %s", ch.ChannelType)
	}
}

// ===== 各通道实现 ================================================================

func (n *Notifier) sendDingtalk(ctx context.Context, cfg notify.DingtalkConfig, msg renderedMessage) error {
	webhook := cfg.Webhook
	if webhook == "" {
		return fmt.Errorf("dingtalk webhook is empty")
	}
	if cfg.Secret != "" {
		ts := time.Now().UnixMilli()
		sign := signDingtalk(ts, cfg.Secret)
		sep := "?"
		if strings.Contains(webhook, "?") {
			sep = "&"
		}
		webhook = fmt.Sprintf("%s%stimestamp=%d&sign=%s", webhook, sep, ts, url.QueryEscape(sign))
	}
	body := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": msg.title,
			"text":  msg.markdown,
		},
	}
	return n.postJSON(ctx, webhook, body)
}

func signDingtalk(ts int64, secret string) string {
	str := fmt.Sprintf("%d\n%s", ts, secret)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(str))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (n *Notifier) sendWeChatWork(ctx context.Context, cfg notify.WeChatWorkConfig, msg renderedMessage) error {
	if cfg.Webhook == "" {
		return fmt.Errorf("wechat_work webhook is empty")
	}
	body := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": msg.markdown,
		},
	}
	return n.postJSONVia(ctx, cfg.Webhook, cfg.ProxyURL, body)
}

func (n *Notifier) sendFeishu(ctx context.Context, cfg notify.FeishuConfig, msg renderedMessage) error {
	if cfg.Webhook == "" {
		return fmt.Errorf("feishu webhook is empty")
	}
	body := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title":    map[string]string{"tag": "plain_text", "content": msg.title},
				"template": "red",
			},
			"elements": []map[string]interface{}{
				{
					"tag":     "markdown",
					"content": msg.markdown,
				},
			},
		},
	}
	if cfg.Secret != "" {
		ts := time.Now().Unix()
		sign, err := signFeishu(ts, cfg.Secret)
		if err == nil {
			body["timestamp"] = fmt.Sprintf("%d", ts)
			body["sign"] = sign
		}
	}
	return n.postJSON(ctx, cfg.Webhook, body)
}

func signFeishu(ts int64, secret string) (string, error) {
	str := fmt.Sprintf("%d\n%s", ts, secret)
	h := hmac.New(sha256.New, []byte(str))
	if _, err := h.Write([]byte{}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func (n *Notifier) sendEmail(cfg notify.EmailConfig, msg renderedMessage) error {
	if cfg.SMTPHost == "" || cfg.From == "" || len(cfg.To) == 0 {
		return fmt.Errorf("email config incomplete")
	}
	e := email.NewEmail()
	e.From = cfg.From
	e.To = cfg.To
	e.Subject = msg.title
	e.Text = []byte(msg.plain)
	e.HTML = []byte(msg.html)
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	if cfg.SMTPPort == 465 {
		return e.SendWithTLS(addr,
			smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost),
			&tls.Config{ServerName: cfg.SMTPHost, InsecureSkipVerify: true})
	}
	return e.Send(addr, smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost))
}

// sendWeChatApp 调用企业微信应用 API 发送文本/markdown
func (n *Notifier) sendWeChatApp(ctx context.Context, cfg notify.WeChatAppConfig, msg renderedMessage) error {
	if cfg.CorpID == "" || cfg.Secret == "" || cfg.AgentID == 0 {
		return fmt.Errorf("wechat_app config incomplete")
	}
	// 1) 获取 access_token
	tokenURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		url.QueryEscape(cfg.CorpID), url.QueryEscape(cfg.Secret))
	resp, err := n.getVia(ctx, tokenURL, cfg.ProxyURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var tk struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &tk); err != nil {
		return err
	}
	if tk.ErrCode != 0 || tk.AccessToken == "" {
		return fmt.Errorf("wechat_app gettoken failed: %s", tk.ErrMsg)
	}
	touser := cfg.ToUser
	if touser == "" {
		touser = "@all"
	}
	sendURL := "https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=" + tk.AccessToken
	body := map[string]interface{}{
		"touser":  touser,
		"msgtype": "markdown",
		"agentid": cfg.AgentID,
		"markdown": map[string]string{
			"content": msg.markdown,
		},
	}
	return n.postJSONVia(ctx, sendURL, cfg.ProxyURL, body)
}

// sendWebhook 万能 Webhook 通道
func (n *Notifier) sendWebhook(ctx context.Context, cfg notify.WebhookConfig, msg renderedMessage) error {
	if cfg.URL == "" {
		return fmt.Errorf("webhook url is empty")
	}
	method := strings.ToUpper(cfg.Method)
	if method == "" {
		method = "POST"
	}
	// 构建消息体
	var bodyBytes []byte
	if cfg.BodyTemplate != "" {
		tmpl, err := template.New("webhook").Parse(cfg.BodyTemplate)
		if err != nil {
			return fmt.Errorf("webhook body_template parse error: %w", err)
		}
		var buf bytes.Buffer
		data := map[string]string{
			"title":    msg.title,
			"subtitle": msg.subtitle,
			"markdown": msg.markdown,
			"plain":    msg.plain,
			"html":     msg.html,
		}
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("webhook body_template execute error: %w", err)
		}
		bodyBytes = buf.Bytes()
	} else {
		body := map[string]interface{}{
			"title":    msg.title,
			"subtitle": msg.subtitle,
			"markdown": msg.markdown,
			"plain":    msg.plain,
		}
		bodyBytes, _ = json.Marshal(body)
	}
	// 构建请求
	req, err := http.NewRequestWithContext(ctx, method, cfg.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook http %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ===== HTTP 帮手 ================================================================

func (n *Notifier) postJSON(ctx context.Context, target string, body interface{}) error {
	return n.postJSONVia(ctx, target, "", body)
}

func (n *Notifier) postJSONVia(ctx context.Context, target, proxyURL string, body interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := n.httpClient
	if proxyURL != "" {
		c, err := n.clientWithProxy(proxyURL)
		if err != nil {
			return err
		}
		client = c
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}
	// 部分通道返回 200 但 errcode != 0
	var generic struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &generic); err == nil {
		if generic.ErrCode != 0 && generic.ErrMsg != "" {
			return fmt.Errorf("upstream errcode=%d msg=%s", generic.ErrCode, generic.ErrMsg)
		}
		if generic.Code != 0 && generic.Msg != "" && generic.Msg != "success" {
			// Feishu 成功 code=0 / msg="success"；非 0 视为失败
			return fmt.Errorf("upstream code=%d msg=%s", generic.Code, generic.Msg)
		}
	}
	return nil
}

func (n *Notifier) getVia(ctx context.Context, target, proxyURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	client := n.httpClient
	if proxyURL != "" {
		c, err := n.clientWithProxy(proxyURL)
		if err != nil {
			return nil, err
		}
		client = c
	}
	return client.Do(req)
}

func (n *Notifier) clientWithProxy(proxyURL string) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(u),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}, nil
}
