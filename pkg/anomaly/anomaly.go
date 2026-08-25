// Package anomaly 提供基于历史基线的动态异常检测能力。
//
// 与传统"静态阈值"不同，动态基线根据指标的历史样本（均值/标准差）判断当前值是否偏离正常范围，
// 能够捕捉"缓慢爬升""周期性波动中的突变"等静态阈值难以覆盖的异常。
//
// 核心概念：
//   - z-score：当前值偏离历史均值的标准差倍数，|z| 越大偏离越严重
//   - 阈值：|z| >= zThreshold 判定为异常（默认 3.0，即 3σ 原则）
//   - 最少样本数：样本过少时统计不可靠，回退到静态阈值判断
package anomaly

import (
	"math"
	"sort"
)

// DefaultZScoreThreshold 默认 z-score 阈值（3σ 原则）
const DefaultZScoreThreshold = 3.0

// DefaultMinSamples 默认最少样本数，不足时基线判断不可信
const DefaultMinSamples = 10

// BaselineStats 一组历史样本的基线统计信息
type BaselineStats struct {
	Mean      float64 // 历史均值
	StdDev    float64 // 历史标准差
	Min       float64 // 历史最小值
	Max       float64 // 历史最大值
	Count     int     // 参与计算的样本数
	ZScore    float64 // 当前值相对基线的 z-score
	IsAnomaly bool    // 是否超出基线（|z| >= threshold）
	Level     string  // 异常等级：""（正常）/ "warning" / "critical"
}

// ComputeStats 计算样本集的均值 / 标准差 / 极值。
// 返回 (mean, stddev, min, max, count)。空输入时 mean/stddev/min/max 为 0。
func ComputeStats(values []float64) (mean, stddev, min, max float64, count int) {
	n := len(values)
	if n == 0 {
		return 0, 0, 0, 0, 0
	}
	sum := 0.0
	min, max = values[0], values[0]
	for _, v := range values {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	mean = sum / float64(n)
	if n < 2 {
		return mean, 0, min, max, n
	}
	variance := 0.0
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	stddev = math.Sqrt(variance / float64(n-1))
	if math.IsNaN(stddev) || math.IsInf(stddev, 0) {
		stddev = 0
	}
	return mean, stddev, min, max, n
}

// ZScore 计算 value 相对 (mean, stddev) 的 z-score。
// 当 stddev <= 0（历史数据无波动）时，若 value 与 mean 相等返回 0，否则返回一个极大值
// 表示"任何偏离都是异常"。
func ZScore(value, mean, stddev float64) float64 {
	if stddev <= 0 {
		if value == mean {
			return 0
		}
		return math.MaxFloat64
	}
	return (value - mean) / stddev
}

// Analyze 对当前值执行基线异常判定。
//
//	zThreshold: 偏离阈值（<=0 时使用 DefaultZScoreThreshold）
//	minSamples: 最少样本数（<=0 时使用 DefaultMinSamples）
//
// 判定规则：
//   - 样本数不足 → IsAnomaly=false（回退静态阈值）
//   - |z| >= 2*zThreshold → critical
//   - |z| >= zThreshold   → warning
//   - 否则                 → 正常
func Analyze(value float64, values []float64, zThreshold float64, minSamples int) BaselineStats {
	if zThreshold <= 0 {
		zThreshold = DefaultZScoreThreshold
	}
	if minSamples <= 0 {
		minSamples = DefaultMinSamples
	}

	mean, stddev, min, max, count := ComputeStats(values)
	stats := BaselineStats{
		Mean:   mean,
		StdDev: stddev,
		Min:    min,
		Max:    max,
		Count:  count,
	}
	if count < minSamples {
		return stats
	}

	z := ZScore(value, mean, stddev)
	stats.ZScore = z
	absZ := math.Abs(z)
	if absZ >= zThreshold {
		stats.IsAnomaly = true
		if absZ >= zThreshold*2 {
			stats.Level = "critical"
		} else {
			stats.Level = "warning"
		}
	}
	return stats
}

// SuggestThreshold 从历史样本中建议一个合理阈值：mean + k * stddev。
// 返回 (threshold, true)；样本不足 2 个时返回 (0, false)。
// 该值可用于静态阈值配置的初始化参考（如 k=3 对应 3σ）。
func SuggestThreshold(values []float64, k float64) (float64, bool) {
	if len(values) < 2 {
		return 0, false
	}
	if k == 0 {
		k = DefaultZScoreThreshold
	}
	mean, stddev, _, _, _ := ComputeStats(values)
	return mean + k*stddev, true
}

// Quantile 计算样本集的 p 分位数（p ∈ [0,1]），用于更稳健的基线参考。
// 返回 (value, true)；空输入返回 (0, false)。
func Quantile(values []float64, p float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx], true
}
