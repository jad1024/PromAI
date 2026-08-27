package metrics

import "testing"

func TestGetStatus_LessThreshold(t *testing.T) {
	// 命中率指标：阈值 95，条件 小于，只有 <95 才触发，>=95 全部正常
	cases := []struct {
		value         float64
		threshold     float64
		thresholdType string
		want          string
	}{
		{94.0, 95.0, "less", "critical"},        // 触发
		{94.9, 95.0, "less", "critical"},        // 触发
		{95.0, 95.0, "less", "normal"},          // 等于阈值，未触发
		{95.5, 95.0, "less", "normal"},          // 高于阈值
		{98.74, 95.0, "less", "normal"},         // 截图中的实际值
		{99.99, 95.0, "less", "normal"},         // 截图中的实际值
		{100.0, 95.0, "less", "normal"},         // 最佳状态
		{104.5, 95.0, "less", "normal"},         // 旧 bug 的 warning 边界，现在应正常
		{105.0, 95.0, "less", "normal"},         // 远离阈值
		{94.9, 95.0, "less_equal", "critical"},  // less_equal 触发
		{95.0, 95.0, "less_equal", "critical"},  // less_equal 等于也触发
		{98.0, 95.0, "less_equal", "normal"},    // 未触发
	}

	for _, c := range cases {
		got := getStatus(c.value, c.threshold, c.thresholdType, "critical")
		if got != c.want {
			t.Errorf("getStatus(%.2f, %.2f, %q, critical) = %q, want %q",
				c.value, c.threshold, c.thresholdType, got, c.want)
		}
	}
}

func TestGetStatus_GreaterThreshold(t *testing.T) {
	// CPU 使用率：阈值 80，只有 >80 触发，<=80 全部正常（不再有 72~80 预警告带）
	cases := []struct {
		value         float64
		threshold     float64
		thresholdType string
		want          string
	}{
		{85.0, 80.0, "greater", "critical"},
		{80.0, 80.0, "greater", "normal"},       // 等于阈值未触发
		{79.0, 80.0, "greater", "normal"},       // 旧 bug 的预警告带，现在应正常
		{75.0, 80.0, "greater", "normal"},
		{72.0, 80.0, "greater", "normal"},
		{71.9, 80.0, "greater", "normal"},
		{80.0, 80.0, "greater_equal", "critical"},
		{79.0, 80.0, "greater_equal", "normal"},
		{71.0, 80.0, "greater_equal", "normal"},
	}

	for _, c := range cases {
		got := getStatus(c.value, c.threshold, c.thresholdType, "critical")
		if got != c.want {
			t.Errorf("getStatus(%.2f, %.2f, %q, critical) = %q, want %q",
				c.value, c.threshold, c.thresholdType, got, c.want)
		}
	}
}

func TestGetStatus_EqualNotEqual(t *testing.T) {
	cases := []struct {
		value         float64
		threshold     float64
		thresholdType string
		want          string
	}{
		{80.0, 80.0, "equal", "critical"},       // 等于触发
		{95.0, 80.0, "equal", "normal"},         // 接近但不等，未触发
		{60.0, 80.0, "equal", "normal"},         // 远离
		{70.0, 80.0, "not_equal", "critical"},   // 不等于触发
		{80.0, 80.0, "not_equal", "normal"},     // 等于阈值不触发
		{88.0, 80.0, "not_equal", "critical"},   // 不等于触发（即使接近）
	}

	for _, c := range cases {
		got := getStatus(c.value, c.threshold, c.thresholdType, "critical")
		if got != c.want {
			t.Errorf("getStatus(%.2f, %.2f, %q, critical) = %q, want %q",
				c.value, c.threshold, c.thresholdType, got, c.want)
		}
	}
}

func TestGetStatus_CustomStatus(t *testing.T) {
	// 自定义触发状态：阈值 80 大于，触发时返回 warning 而非默认 critical
	got := getStatus(90.0, 80.0, "greater", "warning")
	if got != "warning" {
		t.Errorf("getStatus(90, 80, greater, warning) = %q, want %q", got, "warning")
	}
	// 未触发时返回 normal，不返回配置状态
	got = getStatus(75.0, 80.0, "greater", "warning")
	if got != "normal" {
		t.Errorf("getStatus(75, 80, greater, warning) = %q, want %q", got, "normal")
	}
}

func TestGetStatus_AliasOperators(t *testing.T) {
	// 兼容别名写法（与告警规则评估器 checkThreshold 一致）
	cases := []struct {
		value     float64
		threshold float64
		op        string
		want      bool
	}{
		{90, 80, ">", true},
		{80, 80, ">", false},
		{80, 80, ">=", true},
		{70, 80, "<", true},
		{80, 80, "<", false},
		{80, 80, "<=", true},
		{80, 80, "==", true},
		{81, 80, "!=", true},
	}

	for _, c := range cases {
		got := getStatus(c.value, c.threshold, c.op, "critical") == "critical"
		if got != c.want {
			t.Errorf("getStatus(%.0f, %.0f, %q) triggered = %v, want %v",
				c.value, c.threshold, c.op, got, c.want)
		}
	}
}
