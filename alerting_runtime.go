package main

// alerting 子系统的协调器（Coordinator）：
//   - 串联 evaluator + dispatcher + notifier
//   - 负责启动 / 停止 / 配置热加载
//   - 暴露给 admin API 的便捷方法（TestRule、EvaluatorStats）
//
// 仅本文件知道三个子模块的具体实现细节，外部代码通过 *Alerting 句柄使用。

import (
	"context"
	"log"
	"time"

	"PromAI/pkg/alerting"
	"PromAI/pkg/alerting/dispatcher"
	"PromAI/pkg/alerting/evaluator"
	"PromAI/pkg/alerting/notifier"
	"PromAI/pkg/alerting/store"
	"PromAI/pkg/database"
	"PromAI/pkg/prometheus"

	"github.com/prometheus/common/model"
)

// Alerting 是告警子系统的顶层句柄
type Alerting struct {
	evaluator  *evaluator.Evaluator
	dispatcher *dispatcher.Dispatcher
	notifier   *notifier.Notifier
	ctx        context.Context
	cancel     context.CancelFunc
}

// adminAlerting 是全局句柄，由 main.go 在启动时设置
var adminAlerting *Alerting

// startAlerting 在主启动序列中调用：建立子系统并启动评估循环
func startAlerting() (*Alerting, error) {
	// 首次启动：确保根路由存在 + 加载快照
	if err := store.EnsureRootRoute(database.DB); err != nil {
		return nil, err
	}
	if err := store.Reload(); err != nil {
		return nil, err
	}

	n := notifier.New()
	d := dispatcher.New(dispatcher.DefaultConfig(), n)
	e := evaluator.New(evaluator.DefaultConfig(), d)

	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	e.Start(ctx)

	// 每 60s 主动刷新一次规则快照（捕获通过 SQL 直接改库等情况）
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = store.Reload()
			}
		}
	}()

	return &Alerting{
		evaluator:  e,
		dispatcher: d,
		notifier:   n,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Stop 停止子系统（用于优雅关闭，可在 SIGTERM 时调用）
func (a *Alerting) Stop() {
	if a == nil {
		return
	}
	a.evaluator.Stop()
	a.dispatcher.Stop()
	a.cancel()
}

// ClearInstances 清空所有内存中追踪的告警实例
func (a *Alerting) ClearInstances() {
	if a == nil || a.dispatcher == nil {
		return
	}
	a.dispatcher.ClearInstances()
}

// EvaluatorStats 返回评估器统计快照
func (a *Alerting) EvaluatorStats() map[string]interface{} {
	if a == nil || a.evaluator == nil {
		return map[string]interface{}{}
	}
	return a.evaluator.StatsSnapshot()
}

// TestRuleResult 单数据源的测试结果
type TestRuleResult struct {
	DatasourceID   uint                     `json:"datasource_id"`
	DatasourceName string                   `json:"datasource_name"`
	Success        bool                     `json:"success"`
	Error          string                   `json:"error,omitempty"`
	Samples        []map[string]interface{} `json:"samples"`
}

// TestRule 用给定规则在所有目标数据源上立即跑一次 PromQL，返回原始命中数据（不入库）
func (a *Alerting) TestRule(ctx context.Context, rule *database.AlertRule) []TestRuleResult {
	if rule == nil {
		return nil
	}
	// 解析 PromQL / 阈值
	expr := resolveRuleExpr(rule)
	if expr == "" {
		return []TestRuleResult{{Error: "expr is empty"}}
	}
	threshold, thresholdType, _ := resolveRuleThreshold(rule)

	// 解析数据源
	var allDS []database.DataSource
	database.DB.Where("enabled = ?", true).Find(&allDS)
	selector := alerting.DecodeDatasourceSelector(rule.DatasourceSelectorJSON)
	targetIDs := alerting.ResolveDatasourceIDs(rule.DatasourceIDs, selector, allDS)
	if len(targetIDs) == 0 {
		return []TestRuleResult{{Error: "no datasource matched"}}
	}
	dsByID := make(map[uint]*database.DataSource, len(allDS))
	for i := range allDS {
		dsByID[allDS[i].ID] = &allDS[i]
	}

	results := make([]TestRuleResult, 0, len(targetIDs))
	// 单数据源测试上限 50，避免一键测试上千数据源造成压力
	limit := 50
	for idx, id := range targetIDs {
		if idx >= limit {
			break
		}
		ds := dsByID[id]
		if ds == nil {
			continue
		}
		client, err := prometheus.DefaultCache.Get(ds.ID, ds.URL, ds.Username, ds.Password)
		if err != nil {
			results = append(results, TestRuleResult{
				DatasourceID:   ds.ID,
				DatasourceName: ds.Name,
				Success:        false,
				Error:          err.Error(),
			})
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		v, _, qerr := client.API.Query(qctx, expr, time.Now())
		cancel()
		if qerr != nil {
			results = append(results, TestRuleResult{
				DatasourceID:   ds.ID,
				DatasourceName: ds.Name,
				Success:        false,
				Error:          qerr.Error(),
			})
			continue
		}
		samples := make([]map[string]interface{}, 0, 8)
		switch vv := v.(type) {
		case model.Vector:
			for _, s := range vv {
				labels := map[string]string{}
				for ln, lv := range s.Metric {
					labels[string(ln)] = string(lv)
				}
				val := float64(s.Value)
				triggered := evalThreshold(val, threshold, thresholdType)
				samples = append(samples, map[string]interface{}{
					"labels":    labels,
					"value":     val,
					"triggered": triggered,
				})
			}
		case *model.Scalar:
			val := float64(vv.Value)
			samples = append(samples, map[string]interface{}{
				"labels":    map[string]string{},
				"value":     val,
				"triggered": evalThreshold(val, threshold, thresholdType),
			})
		}
		results = append(results, TestRuleResult{
			DatasourceID:   ds.ID,
			DatasourceName: ds.Name,
			Success:        true,
			Samples:        samples,
		})
	}
	return results
}

// resolveRuleExpr 解析规则最终 PromQL（与 evaluator 同语义）
func resolveRuleExpr(rule *database.AlertRule) string {
	if rule.Expr != "" {
		return rule.Expr
	}
	if rule.SourceType == "metric" && rule.MetricConfigID != nil {
		var mc database.MetricConfig
		if err := database.DB.First(&mc, *rule.MetricConfigID).Error; err == nil {
			return mc.Query
		}
	}
	return ""
}

func resolveRuleThreshold(rule *database.AlertRule) (float64, string, string) {
	if rule.HasThreshold || rule.SourceType == "custom" {
		return rule.Threshold, rule.ThresholdType, ""
	}
	if rule.SourceType == "metric" && rule.MetricConfigID != nil {
		var mc database.MetricConfig
		if err := database.DB.First(&mc, *rule.MetricConfigID).Error; err == nil {
			return mc.Threshold, mc.ThresholdType, mc.ThresholdStatus
		}
	}
	return rule.Threshold, rule.ThresholdType, ""
}

func evalThreshold(value, threshold float64, op string) bool {
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

// stopAlertingOnExit 由 main 的延迟 defer 调用
func stopAlertingOnExit() {
	if adminAlerting != nil {
		adminAlerting.Stop()
		log.Printf("[Alerting] 子系统已停止")
	}
}