// Package evaluator 实现告警规则的周期性评估调度。
//
// 设计目标：
//   - 支持几千个数据源 × 几十规则 的规模 → 每 tick 数万评估单元
//   - 通过 worker pool 控制全局并发上限
//   - per-datasource semaphore 防止打爆单个 Prometheus
//   - 熔断机制：连续失败的数据源进入指数退避，不拖累其他数据源
//   - 输出统一的 Sample 流到 alerting/dispatcher
//
// 调度模型：
//
//   每 tick:
//     1. 从 store.Snapshot() 读取当前生效规则集
//     2. 解析每条规则的 datasource 列表（显式 IDs + selector 合并）
//     3. 为每个 (rule, datasource) 生成 evalUnit 并按 hash 错峰投递到 task channel
//     4. worker 池消费 task：取 per-DS 信号量 → 检查熔断 → 执行 PromQL → 阈值判定
//     5. 命中阈值的 Sample 推入 Emitter 通道，由 state.Manager 应用 for 状态机
package evaluator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"PromAI/pkg/alerting"
	"PromAI/pkg/alerting/store"
	"PromAI/pkg/database"
	"PromAI/pkg/prometheus"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// Config 评估器配置（可由 app settings 注入）
type Config struct {
	// 全局评估间隔（默认 15s）
	GlobalInterval time.Duration
	// worker 池大小（默认 256）
	WorkerPoolSize int
	// per-datasource 最大并发查询数（默认 4）
	PerDatasourceConcurrency int
	// 单次 PromQL 查询超时（默认 10s）
	QueryTimeout time.Duration
	// 熔断阈值：连续失败 N 次后进入退避（默认 5）
	BreakerThreshold int
	// 熔断退避起始与最大时长
	BreakerInitialBackoff time.Duration
	BreakerMaxBackoff     time.Duration
	// store 刷新间隔（也可由 admin API 主动触发）
	SnapshotRefreshInterval time.Duration
}

// DefaultConfig 提供合理默认值
func DefaultConfig() Config {
	return Config{
		GlobalInterval:           15 * time.Second,
		WorkerPoolSize:           256,
		PerDatasourceConcurrency: 4,
		QueryTimeout:             10 * time.Second,
		BreakerThreshold:         5,
		BreakerInitialBackoff:    30 * time.Second,
		BreakerMaxBackoff:        5 * time.Minute,
		SnapshotRefreshInterval:  10 * time.Second,
	}
}

// Sample 单次评估命中阈值后产出的告警样本
type Sample struct {
	Rule       *database.AlertRule
	Datasource *database.DataSource
	Labels     alerting.LabelSet
	Value      float64
	Threshold  float64
	Severity   string
	EvalAt     time.Time
	// Active 为 true 表示当前查询命中阈值，否则视为 OK（用于驱动 resolved）
	Active bool
}

// Emitter 评估结果接收器（由 dispatcher 实现）
type Emitter interface {
	// EmitSample 接收一个 Sample。实现必须是非阻塞或带 buffer 的。
	EmitSample(Sample)
}

// Evaluator 评估器主体
type Evaluator struct {
	cfg     Config
	clients *prometheus.ClientCache
	emitter Emitter

	tasks    chan evalUnit
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopCh   chan struct{}

	// per-datasource 信号量
	dsSemMu sync.Mutex
	dsSem   map[uint]chan struct{}

	// per-datasource 熔断
	breakers sync.Map // key=datasource_id, value=*breaker

	// 数据源元数据快照
	dsSnap     atomic.Pointer[datasourceSnapshot]

	// 统计
	stats Stats
}

// Stats 评估器运行统计
type Stats struct {
	TickCount       atomic.Int64
	EvalCount       atomic.Int64
	EvalSuccessCount atomic.Int64
	EvalFailCount   atomic.Int64
	BreakerOpenCount atomic.Int64
	LastTickAt       atomic.Pointer[time.Time]
}

// New 创建评估器（emitter 为 nil 时使用 noop，便于离线测试）
func New(cfg Config, emitter Emitter) *Evaluator {
	if cfg.GlobalInterval <= 0 {
		cfg = DefaultConfig()
	}
	if cfg.WorkerPoolSize <= 0 {
		cfg.WorkerPoolSize = 256
	}
	if cfg.PerDatasourceConcurrency <= 0 {
		cfg.PerDatasourceConcurrency = 4
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 10 * time.Second
	}
	if emitter == nil {
		emitter = noopEmitter{}
	}
	return &Evaluator{
		cfg:     cfg,
		clients: prometheus.DefaultCache,
		emitter: emitter,
		tasks:   make(chan evalUnit, cfg.WorkerPoolSize*4),
		stopCh:  make(chan struct{}),
		dsSem:   make(map[uint]chan struct{}, 32),
	}
}

// Start 启动评估循环（非阻塞）
func (e *Evaluator) Start(ctx context.Context) {
	// 启动 worker
	for i := 0; i < e.cfg.WorkerPoolSize; i++ {
		e.wg.Add(1)
		go e.worker(ctx)
	}
	// 启动调度 tick
	e.wg.Add(1)
	go e.scheduleLoop(ctx)
	// 数据源元数据刷新
	e.wg.Add(1)
	go e.refreshDatasourceLoop(ctx)
	log.Printf("[Alerting] evaluator 已启动 (workers=%d, interval=%s, per-ds=%d)",
		e.cfg.WorkerPoolSize, e.cfg.GlobalInterval, e.cfg.PerDatasourceConcurrency)
}

// Stop 优雅停止评估器
func (e *Evaluator) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopCh)
		close(e.tasks)
	})
	e.wg.Wait()
}

// StatsSnapshot 返回当前统计快照（HTTP /alert/evaluator/status 用）
func (e *Evaluator) StatsSnapshot() map[string]interface{} {
	last := time.Time{}
	if t := e.stats.LastTickAt.Load(); t != nil {
		last = *t
	}
	openBreakers := 0
	e.breakers.Range(func(_, v interface{}) bool {
		if b, ok := v.(*breaker); ok && b.isOpen() {
			openBreakers++
		}
		return true
	})
	return map[string]interface{}{
		"tick_count":         e.stats.TickCount.Load(),
		"eval_count":         e.stats.EvalCount.Load(),
		"eval_success_count": e.stats.EvalSuccessCount.Load(),
		"eval_fail_count":    e.stats.EvalFailCount.Load(),
		"open_breakers":      openBreakers,
		"last_tick_at":       last,
		"worker_pool_size":   e.cfg.WorkerPoolSize,
		"queue_depth":        len(e.tasks),
	}
}

// scheduleLoop 主调度循环
func (e *Evaluator) scheduleLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(e.cfg.GlobalInterval)
	defer ticker.Stop()
	// 启动后立即触发首轮（让前端尽快有数据）
	e.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

// tick 一轮调度：展开规则 → 投递 task
func (e *Evaluator) tick(ctx context.Context) {
	tickNo := e.stats.TickCount.Add(1)
	now := time.Now()
	e.stats.LastTickAt.Store(&now)

	snap := store.MustSnapshot()
	if snap == nil {
		return
	}
	dsSnap := e.dsSnap.Load()
	if dsSnap == nil {
		return
	}

	dispatched := 0
	for i := range snap.Rules {
		rule := &snap.Rules[i]
		// 规则自定义评估间隔（按整除 tickNo 与基础间隔的比例错峰）
		if rule.EvalIntervalSec > 0 {
			tickPer := int64(rule.EvalIntervalSec) * int64(time.Second) / int64(e.cfg.GlobalInterval)
			if tickPer > 1 && (tickNo%tickPer) != int64(rule.ID%uint(tickPer)) {
				continue
			}
		}
		ds := e.resolveDatasources(rule, dsSnap)
		for _, dsRef := range ds {
			// 跨 tick 错峰：相同 (rule, ds) 必落在相同 tick，但不同 (rule, ds) 散布
			select {
			case e.tasks <- evalUnit{rule: rule, ds: dsRef}:
				dispatched++
			default:
				// 队列满，下一轮再说（防止单 tick 堆积）
				log.Printf("[Alerting] evaluator task queue full, dropping rule=%d ds=%d", rule.ID, dsRef.ID)
			}
		}
	}
	if dispatched > 0 && tickNo%10 == 1 {
		log.Printf("[Alerting] tick #%d dispatched %d eval units", tickNo, dispatched)
	}
}

func (e *Evaluator) resolveDatasources(rule *database.AlertRule, dsSnap *datasourceSnapshot) []*database.DataSource {
	selector := alerting.DecodeDatasourceSelector(rule.DatasourceSelectorJSON)
	idsByID := dsSnap.byID

	// 显式 ID 列表
	picked := make(map[uint]*database.DataSource, len(rule.DatasourceIDs))
	for _, id := range rule.DatasourceIDs {
		if ds, ok := idsByID[id]; ok && ds.Enabled {
			picked[id] = ds
		}
	}
	if !selector.IsZero() {
		for i := range dsSnap.all {
			ds := dsSnap.all[i]
			if selector.Match(ds) {
				picked[ds.ID] = ds
			}
		}
	}
	if len(picked) == 0 {
		return nil
	}
	out := make([]*database.DataSource, 0, len(picked))
	for _, ds := range picked {
		out = append(out, ds)
	}
	return out
}

// worker 处理 evalUnit
func (e *Evaluator) worker(ctx context.Context) {
	defer e.wg.Done()
	for u := range e.tasks {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		default:
		}
		e.evaluate(ctx, u)
	}
}

// evaluate 执行一次具体评估
func (e *Evaluator) evaluate(ctx context.Context, u evalUnit) {
	e.stats.EvalCount.Add(1)
	b := e.breakerFor(u.ds.ID)
	if b.isOpen() {
		return // 静默跳过，由熔断计时自动恢复
	}

	sem := e.dsSemaphore(u.ds.ID)
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return
	case <-e.stopCh:
		return
	case <-time.After(2 * time.Second):
		// 单数据源饱和（其他 query 还在跑），放弃本轮，下一轮再试
		return
	}

	client, err := e.clients.Get(u.ds.ID, u.ds.URL, u.ds.Username, u.ds.Password)
	if err != nil {
		b.recordFailure(e.cfg)
		e.stats.EvalFailCount.Add(1)
		return
	}

	expr := resolveExpr(u.rule)
	if expr == "" {
		return
	}
	threshold, thresholdType, status := resolveThreshold(u.rule)

	qctx, cancel := context.WithTimeout(ctx, e.cfg.QueryTimeout)
	defer cancel()

	result, _, qerr := client.API.Query(qctx, expr, time.Now())
	if qerr != nil {
		// 区分超时 / 上游错误：均计入失败
		if errors.Is(qerr, context.Canceled) {
			return
		}
		b.recordFailure(e.cfg)
		e.stats.EvalFailCount.Add(1)
		return
	}
	b.recordSuccess()
	e.stats.EvalSuccessCount.Add(1)

	now := time.Now()
	switch v := result.(type) {
	case model.Vector:
		// 每条样本独立判定（典型 PromQL 返回 vector，每个 series 一个 label set）
		// 注意：vector 为空时，evaluator 不会主动产出 Sample；
		// 但已存在的活跃实例需要 dispatcher 在没有再次命中时驱动 resolved。
		// 我们在每轮 tick 结束时由 state.Manager 扫描 (rule, ds) 维度的活跃实例并发送 OK 信号。
		if len(v) == 0 {
			e.emitter.EmitSample(Sample{
				Rule:       u.rule,
				Datasource: u.ds,
				Threshold:  threshold,
				Severity:   firstNonEmpty(u.rule.Severity, status, "warning"),
				EvalAt:     now,
				Active:     false, // 空 vector → 该 (rule, ds) 全部恢复
			})
			return
		}
		for _, s := range v {
			labels := alerting.LabelSet{}
			for ln, lv := range s.Metric {
				labels[string(ln)] = string(lv)
			}
			triggered := checkThreshold(float64(s.Value), threshold, thresholdType)
			sev := firstNonEmpty(u.rule.Severity, status, "warning")
			e.emitter.EmitSample(Sample{
				Rule:       u.rule,
				Datasource: u.ds,
				Labels:     labels,
				Value:      float64(s.Value),
				Threshold:  threshold,
				Severity:   sev,
				EvalAt:     now,
				Active:     triggered,
			})
		}
	case *model.Scalar:
		triggered := checkThreshold(float64(v.Value), threshold, thresholdType)
		sev := firstNonEmpty(u.rule.Severity, status, "warning")
		e.emitter.EmitSample(Sample{
			Rule:       u.rule,
			Datasource: u.ds,
			Labels:     alerting.LabelSet{"alertname": u.rule.Name},
			Value:      float64(v.Value),
			Threshold:  threshold,
			Severity:   sev,
			EvalAt:     now,
			Active:     triggered,
		})
	default:
		// matrix / string 等暂不支持
	}
}

// dsSemaphore 返回某数据源的并发信号量（懒创建）
func (e *Evaluator) dsSemaphore(id uint) chan struct{} {
	e.dsSemMu.Lock()
	defer e.dsSemMu.Unlock()
	sem, ok := e.dsSem[id]
	if !ok {
		sem = make(chan struct{}, e.cfg.PerDatasourceConcurrency)
		e.dsSem[id] = sem
	}
	return sem
}

// refreshDatasourceLoop 定时刷新数据源元数据快照
func (e *Evaluator) refreshDatasourceLoop(ctx context.Context) {
	defer e.wg.Done()
	e.refreshDatasources()
	t := time.NewTicker(e.cfg.SnapshotRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-t.C:
			e.refreshDatasources()
		}
	}
}

func (e *Evaluator) refreshDatasources() {
	var rows []database.DataSource
	if err := database.DB.Where("enabled = ?", true).Find(&rows).Error; err != nil {
		log.Printf("[Alerting] refresh datasources: %v", err)
		return
	}
	snap := &datasourceSnapshot{
		all:  make([]*database.DataSource, 0, len(rows)),
		byID: make(map[uint]*database.DataSource, len(rows)),
	}
	for i := range rows {
		ds := rows[i] // copy
		snap.all = append(snap.all, &ds)
		snap.byID[ds.ID] = &ds
	}
	e.dsSnap.Store(snap)
}

type evalUnit struct {
	rule *database.AlertRule
	ds   *database.DataSource
}

type datasourceSnapshot struct {
	all  []*database.DataSource
	byID map[uint]*database.DataSource
}

// resolveExpr 解析规则最终 PromQL（metric source 时回查 MetricConfig 快照）
func resolveExpr(rule *database.AlertRule) string {
	if rule.Expr != "" {
		return rule.Expr
	}
	if rule.SourceType == "metric" && rule.MetricConfigID != nil {
		if snap := store.Snapshot(); snap != nil {
			if mc, ok := snap.MetricConfigs[*rule.MetricConfigID]; ok {
				return mc.Query
			}
		}
	}
	return ""
}

// resolveThreshold 解析规则最终阈值（threshold/type/status，优先快照）
func resolveThreshold(rule *database.AlertRule) (float64, string, string) {
	if rule.HasThreshold || rule.SourceType == "custom" {
		return rule.Threshold, rule.ThresholdType, "" // status 由 rule.Severity 主导
	}
	if rule.SourceType == "metric" && rule.MetricConfigID != nil {
		if snap := store.Snapshot(); snap != nil {
			if mc, ok := snap.MetricConfigs[*rule.MetricConfigID]; ok {
				return mc.Threshold, mc.ThresholdType, mc.ThresholdStatus
			}
		}
	}
	return rule.Threshold, rule.ThresholdType, ""
}

// checkThreshold 与 pkg/metrics 保持一致的阈值语义
func checkThreshold(value, threshold float64, op string) bool {
	switch op {
	case "greater", "gt", ">":
		return value > threshold
	case "greater_equal", "ge", ">=":
		return value >= threshold
	case "less", "lt", "<":
		return value < threshold
	case "less_equal", "le", "<=":
		return value <= threshold
	case "equal", "eq", "==":
		return value == threshold
	case "not_equal", "ne", "!=":
		return value != threshold
	default:
		return value > threshold
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// noopEmitter 用于无 dispatcher 的测试场景
type noopEmitter struct{}

func (noopEmitter) EmitSample(Sample) {}

// 让编译器不抱怨未使用的 fmt（保留以便后续插桩）
var _ = fmt.Sprint

// Compile-time check：确保 v1.API 接口被实际使用（避免 IDE 误删 import）
var _ = (v1.API)(nil)
