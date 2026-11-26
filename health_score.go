package main

// health_score.go
//
// 健康分计算的唯一公共实现。健康大屏（admin_api.go）与灭火图（firemap_api.go）
// 共用本文件的函数，避免两处口径漂移。
//
// 口径：健康分 = (总指标数 - 告警数) / 总指标数 × 100
// 其中 告警数 = critical + warning（与健康大屏原有实现完全一致）。

// CalcHealthScore 按总指标数与告警数计算健康分（0~100）。
// total <= 0 时视为 100（无指标不扣分）。
func CalcHealthScore(total, alerts int) float64 {
	if total <= 0 {
		return 100.0
	}
	return float64(total-alerts) / float64(total) * 100
}

// CalcHealthScoreByStatus 按 critical/warning/normal 明细计算健康分。
// 内部仍走 CalcHealthScore，保证与大屏单一口径。
func CalcHealthScoreByStatus(total, critical, warning int) float64 {
	return CalcHealthScore(total, critical+warning)
}
