// Package dispatcher 实现告警事件的完整流水线（替代 Alertmanager）。
//
// 流水线（每个 Sample 依次经过）：
//
//   evaluator.Sample
//        │
//        ▼
//   [state.Manager]   pending → firing → resolved 状态机，fingerprint 去重
//        │
//        ▼
//   [silence.Filter]  按 label matcher 匹配，命中则不进入路由
//        │
//        ▼
//   [inhibit.Filter]  高优先级抑制低优先级告警（labels equal）
//        │
//        ▼
//   [route.Matcher]   匹配 routes 树叶子节点 → 计算 group_key
//        │
//        ▼
//   [group.Aggregator] 写 AlertGroup 表 + 调度 next_notify_at
//        │
//        ▼
//   notifier.Notify (group_wait / group_interval / repeat_interval)
package dispatcher

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"PromAI/pkg/alerting/evaluator"
	"PromAI/pkg/alerting/store"
	"PromAI/pkg/database"
)

// ErrNoChannels 表示分组没有任何可用通道（路由+规则的并集为空 / 全部 disabled）。
// dispatcher 收到该错误后不会把分组标记为 notified、不会累加 send_count，
// 但仍会按路由的 repeat_interval 重新调度，以便用户后续补配通道时自动恢复。
var ErrNoChannels = errors.New("no available notification channels")

// Notifier 通知发送接口（由 alerting/notifier 实现）
type Notifier interface {
	// SendGroup 同步发送一个分组的所有活跃告警。返回 nil 表示全部通道成功。
	SendGroup(ctx context.Context, group *database.AlertGroup, instances []database.AlertInstance) error
	// SendResolvedGroup 发送恢复通知
	SendResolvedGroup(ctx context.Context, group *database.AlertGroup, instances []database.AlertInstance) error
}

// Dispatcher 主体
type Dispatcher struct {
	in       chan evaluator.Sample
	state    *stateManager
	notifier Notifier

	wg       sync.WaitGroup
	stopOnce sync.Once
	stopCh   chan struct{}

	cfg            Config
	lastResetDate  string // YYYY-MM-DD，用于每日 send_count 重置
}

// Config dispatcher 配置
type Config struct {
	InputBufferSize int           // 入站 channel 大小
	BatchInterval   time.Duration // 状态机批量 flush 间隔
	DispatchTick    time.Duration // 分组调度 tick
	DefaultGroupWait      time.Duration
	DefaultGroupInterval  time.Duration
	DefaultRepeatInterval time.Duration
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		InputBufferSize:       8192,
		BatchInterval:         500 * time.Millisecond,
		DispatchTick:          1 * time.Second,
		DefaultGroupWait:      30 * time.Second,
		DefaultGroupInterval:  5 * time.Minute,
		DefaultRepeatInterval: 4 * time.Hour,
	}
}

// New 创建 dispatcher
func New(cfg Config, notifier Notifier) *Dispatcher {
	if cfg.InputBufferSize <= 0 {
		cfg = DefaultConfig()
	}
	d := &Dispatcher{
		cfg:      cfg,
		in:       make(chan evaluator.Sample, cfg.InputBufferSize),
		notifier: notifier,
		stopCh:   make(chan struct{}),
	}
	d.state = newStateManager(d)
	return d
}

// EmitSample 实现 evaluator.Emitter 接口
func (d *Dispatcher) EmitSample(s evaluator.Sample) {
	select {
	case d.in <- s:
	default:
		// 入站队列满：丢弃 + 日志（评估器侧 worker pool 会回压，避免单 Prom 拖垮整条流水线）
		log.Printf("[Alerting] dispatcher input channel full, dropping sample (rule=%d ds=%d)",
			ruleID(s), dsID(s))
	}
}

func ruleID(s evaluator.Sample) uint {
	if s.Rule != nil {
		return s.Rule.ID
	}
	return 0
}
func dsID(s evaluator.Sample) uint {
	if s.Datasource != nil {
		return s.Datasource.ID
	}
	return 0
}

// Start 启动入站消费 + 调度循环
func (d *Dispatcher) Start(ctx context.Context) {
	d.wg.Add(1)
	go d.consumeLoop(ctx)
	d.wg.Add(1)
	go d.dispatchLoop(ctx)
	log.Printf("[Alerting] dispatcher 已启动 (buffer=%d)", d.cfg.InputBufferSize)
}

// Stop 优雅停止
func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
	})
	d.wg.Wait()
}

// ClearInstances 清空所有内存中追踪的告警实例（状态机 reset）
func (d *Dispatcher) ClearInstances() {
	d.state.clear()
}

// consumeLoop 入站 sample → 状态机
func (d *Dispatcher) consumeLoop(ctx context.Context) {
	defer d.wg.Done()
	flush := time.NewTicker(d.cfg.BatchInterval)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case s := <-d.in:
			d.state.absorb(s)
		case <-flush.C:
			d.state.flush()
		}
	}
}

// dispatchLoop 周期性调度待通知分组
func (d *Dispatcher) dispatchLoop(ctx context.Context) {
	defer d.wg.Done()
	t := time.NewTicker(d.cfg.DispatchTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-t.C:
			d.dispatchPending(ctx)
		}
	}
}

// dispatchPending 取出到期的分组，组装告警列表，下发通知
func (d *Dispatcher) dispatchPending(ctx context.Context) {
	now := time.Now()

	// 每日 0 点重置所有分组的 send_count，避免达到 max_send_count 后永续停滞。
	today := now.Format("2006-01-02")
	if d.lastResetDate != today {
		if d.lastResetDate != "" {
			log.Printf("[Dispatch] 日期变更 %s → %s，重置所有分组 send_count=0", d.lastResetDate, today)
			database.DB.Model(&database.AlertGroup{}).Where("send_count > 0").Update("send_count", 0)
		}
		d.lastResetDate = today
	}

	var groups []database.AlertGroup
	if err := database.DB.
		Where("next_notify_at IS NOT NULL AND next_notify_at <= ? AND state IN ?", now, []string{"pending", "notified"}).
		Limit(64).
		Find(&groups).Error; err != nil {
		return
	}
	if len(groups) == 0 {
		return
	}

	if len(groups) > 0 {
		log.Printf("[Dispatch] 本轮到期分组 %d 个", len(groups))
	}

	snap := store.MustSnapshot()
	if snap == nil {
		return
	}
	routesByID := make(map[uint]*database.AlertRoute, len(snap.Routes))
	for i := range snap.Routes {
		routesByID[snap.Routes[i].ID] = &snap.Routes[i]
	}
	rulesByID := make(map[uint]*database.AlertRule, len(snap.Rules))
	for i := range snap.Rules {
		rulesByID[snap.Rules[i].ID] = &snap.Rules[i]
	}

	for i := range groups {
		g := &groups[i]
		route := routesByID[g.RouteID]
		gkShort := g.GroupKey
		if len(gkShort) > 8 {
			gkShort = gkShort[:8]
		}

		// 拉取本组当前活跃且未静默/未抑制的实例
		var instances []database.AlertInstance
		if err := database.DB.
			Where("group_key = ? AND state IN ?", g.GroupKey, []string{"pending", "firing"}).
			Find(&instances).Error; err != nil {
			log.Printf("[Dispatch] group=%s 拉取实例失败: %v", gkShort, err)
			continue
		}
		if len(instances) == 0 {
			// 全部消失 → 检查是否需要发送恢复通知
			if route != nil && route.SendResolved && g.AlertCount > 0 {
				var resolved []database.AlertInstance
				database.DB.Where("group_key = ? AND state = ?", g.GroupKey, "resolved").Find(&resolved)
				if len(resolved) > 0 {
					log.Printf("[Dispatch] group=%s 全部告警已恢复 → 发送恢复通知 (%d 条)", gkShort, len(resolved))
					_ = d.notifier.SendResolvedGroup(ctx, g, resolved)
				}
			} else {
				log.Printf("[Dispatch] group=%s 已空 → 转 idle", gkShort)
			}
			now := time.Now()
			g.State = "idle"
			g.NextNotifyAt = nil
			g.LastNotifiedAt = &now
			g.AlertCount = 0
			_ = database.DB.Save(g).Error
			continue
		}

		// 根据首个实例获取规则（用于 rule 级重复间隔/最大发送次数覆盖）
		rule := rulesByID[instances[0].RuleID]

		// 检查最大发送次数（规则级 > 路由级）
		maxCount := resolveMaxSendCount(rule, route)
		if maxCount > 0 && g.SendCount >= maxCount {
			log.Printf("[Dispatch] group=%s 已达最大发送次数 (%d/%d) → 停止通知",
				gkShort, g.SendCount, maxCount)
			continue
		}

		// 过滤掉被静默/抑制的实例
		visible := make([]database.AlertInstance, 0, len(instances))
		silencedCount, inhibitedCount := 0, 0
		for _, ai := range instances {
			if hasMaskedIDs(ai.SilencedByJSON) {
				silencedCount++
				continue
			}
			if hasMaskedIDs(ai.InhibitedByJSON) {
				inhibitedCount++
				continue
			}
			visible = append(visible, ai)
		}
		if len(visible) == 0 {
			log.Printf("[Dispatch] group=%s 全部实例被静默(%d)/抑制(%d) → 跳过本轮",
				gkShort, silencedCount, inhibitedCount)
			interval := parseDurationOr(resolveRepeatInterval(rule, route), d.cfg.DefaultRepeatInterval)
			n := time.Now().Add(interval)
			g.NextNotifyAt = &n
			_ = database.DB.Save(g).Error
			continue
		}
		if silencedCount+inhibitedCount > 0 {
			log.Printf("[Dispatch] group=%s 可见=%d 静默=%d 抑制=%d → 派发通知",
				gkShort, len(visible), silencedCount, inhibitedCount)
		}

		if err := d.notifier.SendGroup(ctx, g, visible); err != nil {
			if errors.Is(err, ErrNoChannels) {
				log.Printf("[Dispatch] group=%s 无可用通道，按 repeat_interval 重排，state 不变",
					gkShort)
				repeat := parseDurationOr(resolveRepeatInterval(rule, route), d.cfg.DefaultRepeatInterval)
				next := time.Now().Add(repeat)
				g.NextNotifyAt = &next
				g.AlertCount = len(visible)
				_ = database.DB.Save(g).Error
				continue
			}
			log.Printf("[Dispatch] group=%s 派发通知出错: %v", gkShort, err)
			// 非通道错误也按 repeat_interval 重排，避免死循环重试，但不增加 SendCount
			repeat := parseDurationOr(resolveRepeatInterval(rule, route), d.cfg.DefaultRepeatInterval)
			next := time.Now().Add(repeat)
			g.NextNotifyAt = &next
			g.AlertCount = len(visible)
			_ = database.DB.Save(g).Error
			continue
		}
		// 更新分组节流时间
		repeat := parseDurationOr(resolveRepeatInterval(rule, route), d.cfg.DefaultRepeatInterval)
		ts := time.Now()
		next := ts.Add(repeat)
		g.LastNotifiedAt = &ts
		g.NextNotifyAt = &next
		g.AlertCount = len(visible)
		g.SendCount++
		g.State = "notified"
		_ = database.DB.Save(g).Error
		// 更新实例 NotifiedCount / LastNotifiedAt
		ids := make([]uint, 0, len(visible))
		for _, v := range visible {
			ids = append(ids, v.ID)
		}
		_ = database.DB.Model(&database.AlertInstance{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"notified_count":   gorm_inc("notified_count", 1),
				"last_notified_at": ts,
			}).Error
	}
}

// hasMaskedIDs 判断 JSON 数组是否非空 [] / 非空字符串
func hasMaskedIDs(s string) bool {
	if s == "" || s == "[]" || s == "null" {
		return false
	}
	return true
}

func resolveRepeatInterval(rule *database.AlertRule, route *database.AlertRoute) string {
	if rule != nil && rule.RepeatInterval != "" {
		return rule.RepeatInterval
	}
	if route != nil && route.RepeatInterval != "" {
		return route.RepeatInterval
	}
	return ""
}

func resolveMaxSendCount(rule *database.AlertRule, route *database.AlertRoute) int {
	if rule != nil && rule.MaxSendCount > 0 {
		return rule.MaxSendCount
	}
	if route != nil && route.MaxSendCount > 0 {
		return route.MaxSendCount
	}
	return 0
}

func parseDurationOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// gorm_inc 用 SQL 表达式做原子自增
func gorm_inc(col string, n int) interface{} {
	// gorm.Expr 在外面引入；这里返回字符串触发警告太麻烦，因此用 raw SQL fragment
	// 但 Updates 不支持表达式 string，我们改为 column+raw 操作：用 gorm.Expr 才行
	// 使用 go pkg 内函数：返回 gorm.Expr
	return gormExpr(col + " + ?", n)
}
