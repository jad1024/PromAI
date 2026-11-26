package main

import "testing"

func TestCalcHealthScoreUnified(t *testing.T) {
	cases := []struct {
		name           string
		total          int
		critical       int
		warning        int
		wantScore      float64
	}{
		{"全部正常", 10, 0, 0, 100},
		{"一项严重", 4, 1, 0, 75},
		{"一严重一警告", 4, 1, 1, 50},
		{"仅警告", 4, 0, 1, 75},
		{"无指标", 0, 0, 0, 100},
		{"全告警", 5, 3, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 大屏口径：alerts = critical + warning
			alerts := c.critical + c.warning
			bigScreen := CalcHealthScore(c.total, alerts)
			// 灭火图口径：按明细算
			firemap := CalcHealthScoreByStatus(c.total, c.critical, c.warning)
			// 本地别名（firemap_api.go）也须一致
			local := calcHealthScore(c.total, c.critical, c.warning)

			if bigScreen != c.wantScore {
				t.Fatalf("CalcHealthScore(%d,%d)=%.2f, want %.2f", c.total, alerts, bigScreen, c.wantScore)
			}
			if firemap != bigScreen || local != bigScreen {
				t.Fatalf("口径不一致: 大屏=%.2f 灭火图=%.2f 本地=%.2f", bigScreen, firemap, local)
			}
		})
	}
}
