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

type Config struct {
	GlobalInterval           time.Duration
	WorkerPoolSize           int
	PerDatasourceConcurrency int
	QueryTimeout             time.Duration
	BreakerThreshold         int
	BreakerInitialBackoff    time.Duration
	BreakerMaxBackoff        time.Duration
	SnapshotRefreshInterval  time.Duration

	AdaptiveEnabled          bool
	AdaptiveMinInterval      time.Duration
	AdaptiveMaxInterval      time.Duration
	AdaptiveTargetUtilization float64
	HighPriorityChanSize     int
	LowPriorityChanSize      int
}

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

		AdaptiveEnabled:           true,
		AdaptiveMinInterval:       15 * time.Second,
		AdaptiveMaxInterval:       5 * time.Minute,
		AdaptiveTargetUtilization: 0.7,
		HighPriorityChanSize:      2048,
		LowPriorityChanSize:       8192,
	}
}

type Sample struct {
	Rule       *database.AlertRule
	Datasource *database.DataSource
	Labels     alerting.LabelSet
	Value      float64
	Threshold  float64
	Severity   string
	EvalAt     time.Time
	Active     bool
}

type Emitter interface {
	EmitSample(Sample)
}

type Stats struct {
	TickCount             atomic.Int64
	EvalCount             atomic.Int64
	EvalSuccessCount      atomic.Int64
	EvalFailCount         atomic.Int64
	BreakerOpenCount      atomic.Int64
	LastTickAt            atomic.Pointer[time.Time]
	DroppedCount          atomic.Int64
	TotalEvalTimeMs       atomic.Int64
	LastTickDurationMs    atomic.Int64
	RunnerCount           atomic.Int64
	AdaptiveIntervalMs    atomic.Int64
	PendingLowTasks       atomic.Int64
	PendingHighTasks      atomic.Int64
}

type Evaluator struct {
	cfg     Config
	clients *prometheus.ClientCache
	emitter Emitter

	highTasks chan evalUnit
	lowTasks  chan evalUnit
	wg        sync.WaitGroup
	stopOnce  sync.Once
	stopCh    chan struct{}

	dsSemMu sync.Mutex
	dsSem   map[uint]chan struct{}

	breakers sync.Map

	dsSnap atomic.Pointer[datasourceSnapshot]

	runnersMu sync.Mutex
	runners   map[uint]*ruleRunner

	adaptiveInterval atomic.Int64

	stats Stats
}

type ruleRunner struct {
	ruleID  uint
	stop    chan struct{}
	stopped atomic.Bool
}

func (r *ruleRunner) close() {
	if !r.stopped.Load() {
		r.stopped.Store(true)
		close(r.stop)
	}
}

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
	if cfg.HighPriorityChanSize <= 0 {
		cfg.HighPriorityChanSize = 2048
	}
	if cfg.LowPriorityChanSize <= 0 {
		cfg.LowPriorityChanSize = 8192
	}
	if cfg.AdaptiveMinInterval <= 0 {
		cfg.AdaptiveMinInterval = 15 * time.Second
	}
	if cfg.AdaptiveMaxInterval <= 0 {
		cfg.AdaptiveMaxInterval = 5 * time.Minute
	}
	if cfg.AdaptiveTargetUtilization <= 0 || cfg.AdaptiveTargetUtilization > 1 {
		cfg.AdaptiveTargetUtilization = 0.7
	}
	if emitter == nil {
		emitter = noopEmitter{}
	}
	e := &Evaluator{
		cfg:       cfg,
		clients:   prometheus.DefaultCache,
		emitter:   emitter,
		highTasks: make(chan evalUnit, cfg.HighPriorityChanSize),
		lowTasks:  make(chan evalUnit, cfg.LowPriorityChanSize),
		stopCh:    make(chan struct{}),
		dsSem:     make(map[uint]chan struct{}, 32),
		runners:   make(map[uint]*ruleRunner),
	}
	e.adaptiveInterval.Store(int64(cfg.GlobalInterval))
	e.stats.AdaptiveIntervalMs.Store(int64(cfg.GlobalInterval / time.Millisecond))
	return e
}

func (e *Evaluator) Start(ctx context.Context) {
	for i := 0; i < e.cfg.WorkerPoolSize; i++ {
		e.wg.Add(1)
		go e.worker(ctx)
	}
	e.wg.Add(1)
	go e.ruleOrchestrator(ctx)
	e.wg.Add(1)
	go e.refreshDatasourceLoop(ctx)
	log.Printf("[Alerting] evaluator 已启动 (workers=%d, base_interval=%s, per-ds=%d, adaptive=%v)",
		e.cfg.WorkerPoolSize, e.cfg.GlobalInterval, e.cfg.PerDatasourceConcurrency, e.cfg.AdaptiveEnabled)
}

func (e *Evaluator) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopCh)
	})
	e.wg.Wait()
}

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
		"tick_count":          e.stats.TickCount.Load(),
		"eval_count":          e.stats.EvalCount.Load(),
		"eval_success_count":  e.stats.EvalSuccessCount.Load(),
		"eval_fail_count":     e.stats.EvalFailCount.Load(),
		"dropped_count":       e.stats.DroppedCount.Load(),
		"open_breakers":       openBreakers,
		"last_tick_at":        last,
		"worker_pool_size":    e.cfg.WorkerPoolSize,
		"runner_count":        e.stats.RunnerCount.Load(),
		"adaptive_interval":   time.Duration(e.adaptiveInterval.Load()).String(),
		"pending_low":         len(e.lowTasks),
		"pending_high":        len(e.highTasks),
	}
}

func (e *Evaluator) ruleOrchestrator(ctx context.Context) {
	defer e.wg.Done()
	e.reconcileRunners(ctx)
	statsTicker := time.NewTicker(30 * time.Second)
	defer statsTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-statsTicker.C:
			e.reconcileRunners(ctx)
			if e.cfg.AdaptiveEnabled {
				e.adaptInterval()
			}
			if e.stats.EvalCount.Load()%100 == 0 {
				e.logStats()
			}
		}
	}
}

func (e *Evaluator) reconcileRunners(ctx context.Context) {
	snap := store.MustSnapshot()
	if snap == nil {
		return
	}
	currentIDs := make(map[uint]*database.AlertRule)
	for i := range snap.Rules {
		rule := &snap.Rules[i]
		if !rule.Enabled {
			continue
		}
		currentIDs[rule.ID] = rule
	}
	e.runnersMu.Lock()
	defer e.runnersMu.Unlock()
	for id, runner := range e.runners {
		if _, ok := currentIDs[id]; !ok {
			runner.close()
			delete(e.runners, id)
			log.Printf("[Alerting] 规则 runner #%d 已停止", id)
		}
	}
	for id, rule := range currentIDs {
		if _, ok := e.runners[id]; !ok {
			stopCh := make(chan struct{})
			e.runners[id] = &ruleRunner{ruleID: id, stop: stopCh}
			e.wg.Add(1)
			e.stats.RunnerCount.Add(1)
			go e.runRuleLoop(ctx, rule, stopCh)
			log.Printf("[Alerting] 规则 runner #%d 已启动 (interval=%s, severity=%s)", id, e.resolveRuleInterval(rule), rule.Severity)
		}
	}
	e.stats.RunnerCount.Store(int64(len(e.runners)))
}

func (e *Evaluator) resolveRuleInterval(rule *database.AlertRule) time.Duration {
	base := time.Duration(e.adaptiveInterval.Load())
	if rule != nil && rule.EvalIntervalSec > 0 {
		ruleInterval := time.Duration(rule.EvalIntervalSec) * time.Second
		if ruleInterval > base {
			return ruleInterval
		}
	}
	if base < e.cfg.GlobalInterval {
		return e.cfg.GlobalInterval
	}
	return base
}

func (e *Evaluator) runRuleLoop(ctx context.Context, rule *database.AlertRule, stopCh chan struct{}) {
	defer e.wg.Done()
	defer e.stats.RunnerCount.Add(-1)
	interval := e.resolveRuleInterval(rule)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	e.evaluateRule(ctx, rule)
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-stopCh:
			return
		case <-ticker.C:
			snap := store.MustSnapshot()
			if snap == nil {
				continue
			}
			cur := findRuleByID(snap.Rules, rule.ID)
			if cur == nil {
				return
			}
			e.stats.TickCount.Add(1)
			e.evaluateRule(ctx, cur)
			if e.cfg.AdaptiveEnabled {
				newInterval := e.resolveRuleInterval(cur)
				if newInterval != interval {
					interval = newInterval
					ticker.Reset(interval)
				}
			}
		}
	}
}

func findRuleByID(rules []database.AlertRule, id uint) *database.AlertRule {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

func (e *Evaluator) evaluateRule(ctx context.Context, rule *database.AlertRule) {
	dsSnap := e.dsSnap.Load()
	if dsSnap == nil {
		return
	}
	now := time.Now()
	e.stats.LastTickAt.Store(&now)
	dss := e.resolveDatasources(rule, dsSnap)
	dispatched := 0
	for _, dsRef := range dss {
		u := evalUnit{rule: rule, ds: dsRef}
		target := e.lowTasks
		if rule.Severity == "critical" || rule.Severity == "Critical" {
			target = e.highTasks
		}
		select {
		case target <- u:
			dispatched++
		default:
			e.stats.DroppedCount.Add(1)
			if e.stats.DroppedCount.Load()%100 == 1 {
				log.Printf("[Alerting] task queue full, dropping rule=%d ds=%d (severity=%s)", rule.ID, dsRef.ID, rule.Severity)
			}
		}
	}
	e.stats.PendingHighTasks.Store(int64(len(e.highTasks)))
	e.stats.PendingLowTasks.Store(int64(len(e.lowTasks)))
}

func (e *Evaluator) worker(ctx context.Context) {
	defer e.wg.Done()
	for {
		select {
		case u := <-e.highTasks:
			e.evaluate(ctx, u)
			continue
		default:
		}
		select {
		case u := <-e.lowTasks:
			e.evaluate(ctx, u)
			continue
		default:
		}
		select {
		case u := <-e.highTasks:
			e.evaluate(ctx, u)
		case u := <-e.lowTasks:
			e.evaluate(ctx, u)
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		}
	}
}

func (e *Evaluator) evaluate(ctx context.Context, u evalUnit) {
	e.stats.EvalCount.Add(1)
	start := time.Now()
	b := e.breakerFor(u.ds.ID)
	if b.isOpen() {
		return
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
		if len(v) == 0 {
			e.emitter.EmitSample(Sample{
				Rule:       u.rule,
				Datasource: u.ds,
				Threshold:  threshold,
				Severity:   firstNonEmpty(u.rule.Severity, status, "warning"),
				EvalAt:     now,
				Active:     false,
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
	}
	elapsed := time.Since(start)
	e.stats.TotalEvalTimeMs.Add(elapsed.Milliseconds())
}

func (e *Evaluator) adaptInterval() {
	current := time.Duration(e.adaptiveInterval.Load())
	totalCap := cap(e.highTasks) + cap(e.lowTasks)
	pending := len(e.highTasks) + len(e.lowTasks)
	utilization := float64(pending) / float64(totalCap)
	target := e.cfg.AdaptiveTargetUtilization
	if utilization > target*1.2 {
		newInterval := current * 15 / 10
		if newInterval > e.cfg.AdaptiveMaxInterval {
			newInterval = e.cfg.AdaptiveMaxInterval
		}
		e.adaptiveInterval.Store(int64(newInterval))
		e.stats.AdaptiveIntervalMs.Store(int64(newInterval / time.Millisecond))
		log.Printf("[Alerting] 自适应: 负载 %.0f%% > 目标 %.0f%%, interval %s → %s",
			utilization*100, target*100, current, newInterval)
	} else if utilization < target*0.5 {
		newInterval := current * 10 / 15
		if newInterval < e.cfg.AdaptiveMinInterval {
			newInterval = e.cfg.AdaptiveMinInterval
		}
		e.adaptiveInterval.Store(int64(newInterval))
		e.stats.AdaptiveIntervalMs.Store(int64(newInterval / time.Millisecond))
	} else if current != e.cfg.AdaptiveMinInterval && utilization < target*0.3 {
		newInterval := current * 5 / 10
		if newInterval < e.cfg.AdaptiveMinInterval {
			newInterval = e.cfg.AdaptiveMinInterval
		}
		e.adaptiveInterval.Store(int64(newInterval))
		e.stats.AdaptiveIntervalMs.Store(int64(newInterval / time.Millisecond))
	}
}

func (e *Evaluator) logStats() {
	log.Printf("[Stats] runners=%d eval=%d (ok=%d fail=%d drop=%d) queue=(high=%d low=%d) interval=%s",
		e.stats.RunnerCount.Load(),
		e.stats.EvalCount.Load(),
		e.stats.EvalSuccessCount.Load(),
		e.stats.EvalFailCount.Load(),
		e.stats.DroppedCount.Load(),
		len(e.highTasks),
		len(e.lowTasks),
		time.Duration(e.adaptiveInterval.Load()),
	)
}

func (e *Evaluator) resolveDatasources(rule *database.AlertRule, dsSnap *datasourceSnapshot) []*database.DataSource {
	selector := alerting.DecodeDatasourceSelector(rule.DatasourceSelectorJSON)
	idsByID := dsSnap.byID
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
		ds := rows[i]
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

func resolveThreshold(rule *database.AlertRule) (float64, string, string) {
	if rule.HasThreshold || rule.SourceType == "custom" {
		return rule.Threshold, rule.ThresholdType, ""
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

type noopEmitter struct{}

func (noopEmitter) EmitSample(Sample) {}

var _ = fmt.Sprint
var _ = (v1.API)(nil)
