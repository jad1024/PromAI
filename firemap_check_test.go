package main

import (
	"testing"

	"PromAI/pkg/database"
)

// TestEvaluateCheckResult 检查项判定：真实值 + 阈值 + 无数据降级。
func TestEvaluateCheckResult(t *testing.T) {
	promLT := database.FlashAICheckConfig{Key: "instance_up", CheckType: "promql", CompareOp: "lt", Threshold: 1}
	bad, _ := evaluateCheckResult(promLT, floatPtr(0), 0, true)
	if bad != "err" {
		t.Fatalf("up=0 应判 err，实际 %s", bad)
	}
	good, _ := evaluateCheckResult(promLT, floatPtr(1), 0, true)
	if good != "ok" {
		t.Fatalf("up=1 应判 ok，实际 %s", good)
	}
	na, _ := evaluateCheckResult(promLT, nil, 0, true)
	if na != "na" {
		t.Fatalf("无序列应判 na，实际 %s", na)
	}
	noData, _ := evaluateCheckResult(promLT, nil, 0, false)
	if noData != "na" {
		t.Fatalf("hasData=false 应判 na，实际 %s", noData)
	}

	alert := database.FlashAICheckConfig{CheckType: "alert"}
	if st, _ := evaluateCheckResult(alert, nil, 3, true); st != "err" {
		t.Fatalf("3 条告警应判 err，实际 %s", st)
	}
	if st, _ := evaluateCheckResult(alert, nil, 0, true); st != "ok" {
		t.Fatalf("0 条告警应判 ok，实际 %s", st)
	}

	slo := database.FlashAICheckConfig{CheckType: "slo"}
	if st, _ := evaluateCheckResult(slo, floatPtr(10), 0, true); st != "err" {
		t.Fatalf("预算 10%% 应判 err，实际 %s", st)
	}
	if st, _ := evaluateCheckResult(slo, floatPtr(50), 0, true); st != "warn" {
		t.Fatalf("预算 50%% 应判 warn，实际 %s", st)
	}
	if st, _ := evaluateCheckResult(slo, floatPtr(90), 0, true); st != "ok" {
		t.Fatalf("预算 90%% 应判 ok，实际 %s", st)
	}

	ai := database.FlashAICheckConfig{CheckType: "ai"}
	if st, rs := evaluateCheckResult(ai, nil, 0, true); st != "na" || rs != "待 AI 诊断" {
		t.Fatalf("ai 检查项应降级为待诊断: %s %s", st, rs)
	}

	// 旧版种子（无 CheckType）兼容为 snapshot
	legacy := database.FlashAICheckConfig{Key: "report"}
	if st, _ := evaluateCheckResult(legacy, nil, 1, true); st != "err" {
		t.Fatalf("旧配置应兼容 snapshot 判定，实际 %s", st)
	}
}

func floatPtr(v float64) *float64 { return &v }

// TestDefaultFiremapCheckConfigs 新种子：6 项、每条都带真实诊断动作、key 唯一、顺序递增。
func TestDefaultFiremapCheckConfigs(t *testing.T) {
	cfgs := defaultFiremapCheckConfigs
	if len(cfgs) != 6 {
		t.Fatalf("默认检查项应为 6 项，实际 %d", len(cfgs))
	}
	seen := map[string]bool{}
	prev := -1
	types := map[string]bool{}
	for _, c := range cfgs {
		if seen[c.Key] {
			t.Fatalf("重复 key: %s", c.Key)
		}
		seen[c.Key] = true
		if c.SortOrder <= prev {
			t.Fatalf("排序未递增: %s order=%d prev=%d", c.Key, c.SortOrder, prev)
		}
		prev = c.SortOrder
		if !c.Enabled {
			t.Fatalf("默认项应启用: %s", c.Key)
		}
		if c.CheckType == "" {
			t.Fatalf("检查项 %s 缺少诊断动作类型", c.Key)
		}
		types[c.CheckType] = true
	}
	for _, typ := range []string{"promql", "alert", "snapshot", "slo", "ai"} {
		if !types[typ] {
			t.Fatalf("缺少诊断动作类型: %s", typ)
		}
	}
	// 确认 GORM 模型可用于迁移（编译期检查）
	_ = database.FlashAICheckConfig{}
	_ = database.FiremapNode{}
	_ = database.FiremapCheckResult{}
	_ = database.FiremapSLO{}
	_ = database.FiremapEvent{}
}

// TestAbnormalCount 巡检异常指标统计只数 critical / warning。
func TestAbnormalCount(t *testing.T) {
	ms := []MetricHealthData{
		{Status: "critical"}, {Status: "warning"}, {Status: "normal"}, {Status: "normal"},
	}
	if got := abnormalCount(ms); got != 2 {
		t.Fatalf("abnormalCount=%d, want 2", got)
	}
}

// TestCountStatus 检查结果分状态计数。
func TestCountStatus(t *testing.T) {
	cs := []database.FiremapCheckResult{
		{Status: "err"}, {Status: "err"}, {Status: "warn"}, {Status: "ok"},
	}
	if countStatus(cs, "err") != 2 || countStatus(cs, "warn") != 1 || countStatus(cs, "ok") != 1 {
		t.Fatalf("countStatus 统计错误: %+v", cs)
	}
}
