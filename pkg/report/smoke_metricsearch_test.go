package report

import (
	"html/template"
	"os"
	"strings"
	"testing"
	"time"
)

// 冒烟：确保 templates/report.html 能解析并渲染出每指标内嵌搜索框
func TestSmokeReportMetricSearchBox(t *testing.T) {
	// GenerateReport 用的是相对仓库根的 templates/report.html，测试时 CWD 在包目录，需先回到根
	if _, err := os.Stat("templates/report.html"); err != nil {
		if err := os.Chdir("../.."); err != nil {
			t.Fatalf("切换到仓库根失败: %v", err)
		}
	}
	data := ReportData{
		Timestamp:  time.Now(),
		Project:    "测试项目",
		Datasource: "测试数据源",
		MetricGroups: map[string]*MetricGroup{
			"基础监控": {
				Type:        "基础监控",
				MetricOrder: []string{`CPU"使用率`, "内存 (used) %", "磁盘[分区]/home"},
				MetricsByName: map[string][]MetricData{
					`CPU"使用率`: {
						{Name: `CPU"使用率`, Value: 91.5, Unit: "%", Status: "critical", Threshold: 80},
						{Name: `CPU"使用率`, Value: 12.3, Unit: "%", Status: "normal", Threshold: 80},
					},
					"内存 (used) %":   {{Name: "内存 (used) %", Value: 44.2, Unit: "%", Status: "warning", Threshold: 40}},
					"磁盘[分区]/home":  {{Name: "磁盘[分区]/home", Value: 33.3, Unit: "%", Status: "normal", Threshold: 90}},
				},
			},
			"空组": {
				Type:          "空组",
				MetricOrder:   []string{"无数据指标"},
				MetricsByName: map[string][]MetricData{"无数据指标": {}},
			},
		},
		ChartData: map[string]template.JS{"metricsData": "[]"},
	}

	if err := os.MkdirAll("reports", 0o755); err != nil {
		t.Fatalf("创建 reports 目录失败: %v", err)
	}

	path, err := GenerateReport(data)
	if err != nil {
		t.Fatalf("GenerateReport 失败: %v", err)
	}
	defer os.Remove(path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取报告失败: %v", err)
	}
	html := string(raw)

	// 有数据的指标应有搜索框；无数据的指标（空组）不应有
	if n := strings.Count(html, `class="metric-search"`); n != 3 {
		t.Errorf("搜索框数量应为 3（空指标不渲染），实际 %d", n)
	}
	// 表格需带 data-metric-table 供 JS 关联
	if n := strings.Count(html, "data-metric-table="); n != 3 {
		t.Errorf("data-metric-table 数量应为 3，实际 %d", n)
	}
	// 搜索框绑定脚本应存在
	if !strings.Contains(html, "bindMetricSearch") {
		t.Error("缺少 bindMetricSearch 脚本")
	}
	// 特殊字符指标名应被正确转义，不破坏 HTML 属性
	if strings.Contains(html, `data-metric-table="基础监控_CPU"使用率"`) {
		t.Error("指标名中的双引号未转义，会破坏 HTML 属性")
	}
	t.Logf("报告生成成功: %s (%d bytes)", path, len(html))
}
