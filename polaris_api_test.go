package main

import (
	"testing"

	"PromAI/pkg/database"
)

func TestDetectStatusThreshold(t *testing.T) {
	v := 99.0
	tests := []struct {
		name      string
		d         database.PolarisDetection
		last      *float64
		hasData   bool
		wantTrige bool
	}{
		{"阈值 lt 触发", database.PolarisDetection{DetectType: "threshold", CompareOp: "lt", Threshold: 99.5, Enabled: true}, &v, true, true},
		{"阈值 lt 未触发", database.PolarisDetection{DetectType: "threshold", CompareOp: "lt", Threshold: 98.5, Enabled: true}, &v, true, false},
		{"阈值 gt 触发", database.PolarisDetection{DetectType: "threshold", CompareOp: "gt", Threshold: 98.5, Enabled: true}, &v, true, true},
		{"禁用不触发", database.PolarisDetection{DetectType: "threshold", CompareOp: "lt", Threshold: 99.5, Enabled: false}, &v, true, false},
		{"无数据不触发", database.PolarisDetection{DetectType: "threshold", CompareOp: "lt", Threshold: 99.5, Enabled: true}, nil, false, false},
	}
	for _, tt := range tests {
		tr, _ := detectStatus(tt.d, tt.last, tt.hasData, nil)
		if tr != tt.wantTrige {
			t.Errorf("%s: got %v want %v", tt.name, tr, tt.wantTrige)
		}
	}
}

func TestDetectStatusNoData(t *testing.T) {
	d := database.PolarisDetection{DetectType: "nodata", Enabled: true}
	if tr, _ := detectStatus(d, nil, false, nil); !tr {
		t.Error("数据中断应触发")
	}
	if tr, _ := detectStatus(d, func() *float64 { v := 1.0; return &v }(), true, nil); tr {
		t.Error("有数据不应触发")
	}
}

func TestDetectStatusPredict(t *testing.T) {
	d := database.PolarisDetection{DetectType: "predict", Enabled: true, Sensitivity: 2}
	hist := []float64{100, 101, 99, 100, 101, 99}
	v := 110.0
	if tr, reason := detectStatus(d, &v, true, hist); !tr {
		t.Errorf("智能预测异常点应触发, reason=%s", reason)
	}
	v2 := 100.5
	if tr, _ := detectStatus(d, &v2, true, hist); tr {
		t.Error("正常点不应触发")
	}
}

func TestBusinessHealth(t *testing.T) {
	if got := businessHealth(10, 2); got != 80 {
		t.Errorf("businessHealth(10,2)=%v want 80", got)
	}
	if got := businessHealth(0, 0); got != 100 {
		t.Errorf("businessHealth(0,0)=%v want 100", got)
	}
}

func TestPolarisStatus(t *testing.T) {
	if s := polarisStatus(100, 0); s != "normal" {
		t.Errorf("got %s want normal", s)
	}
	if s := polarisStatus(80, 0); s != "warning" {
		t.Errorf("got %s want warning", s)
	}
	if s := polarisStatus(100, 1); s != "critical" {
		t.Errorf("有检测触发应为 critical, got %s", s)
	}
}

func TestLoadChartPromQLCustom(t *testing.T) {
	// 纯函数不依赖 DB：自定义 PromQL 分支
	c := database.PolarisChart{Name: "下单量", CustomPromQL: "sum(rate(order_total[5m]))"}
	// 该分支会尝试查库（MetricIDs 空时走 custom），无需 DB
	pairs := loadChartPromQL(c)
	if len(pairs) != 1 || pairs[0].Name != "下单量" || pairs[0].Expr == "" {
		t.Errorf("custom PromQL 解析失败: %+v", pairs)
	}
}
