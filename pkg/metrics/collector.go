package metrics

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"PromAI/pkg/anomaly"
	"PromAI/pkg/config"
	"PromAI/pkg/prometheus"
	"PromAI/pkg/report"
)

// Collector 处理指标收集
type Collector struct {
	Client        PrometheusAPI
	config        *config.Config
	prometheusURL string
}

type PrometheusAPI interface {
	Query(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error)
	QueryRange(ctx context.Context, query string, r v1.Range, opts ...v1.Option) (model.Value, v1.Warnings, error)
}

// NewCollector 创建新的收集器
func NewCollector(client PrometheusAPI, config *config.Config) *Collector {
	return &Collector{
		Client:        client,
		config:        config,
		prometheusURL: config.PrometheusURL,
	}
}

// NewCollectorWithURL 创建带有指定URL的收集器
func NewCollectorWithURL(client PrometheusAPI, config *config.Config, prometheusURL string) *Collector {
	return &Collector{
		Client:        client,
		config:        config,
		prometheusURL: prometheusURL,
	}
}

// UpdatePrometheusURL 更新Prometheus URL和客户端
func (c *Collector) UpdatePrometheusURL(url,username,password string) error {
	client, err := prometheus.NewClient(url,username,password)
	if err != nil {
		return fmt.Errorf("creating prometheus client: %w", err)
	}
	c.Client = client.API
	c.prometheusURL = url
	return nil
}

// CollectMetrics 收集指标数据
func (c *Collector) CollectMetrics() (*report.ReportData, error) {
	return c.CollectMetricsWithContext(context.Background())
}

// CollectMetricsWithContext 使用指定context收集指标数据
func (c *Collector) CollectMetricsWithContext(ctx context.Context) (*report.ReportData, error) {
	log.Printf("[DEBUG] 开始收集指标，使用数据源: %s", c.prometheusURL)

	data := &report.ReportData{
		Timestamp:    time.Now(),
		MetricGroups: make(map[string]*report.MetricGroup),
		ChartData:    make(map[string]template.JS),
		Project:      c.config.ProjectName,
		Datasource:   c.prometheusURL, //在CollectMetrics函数开始时设置默认数据源
	}

	for _, metricType := range c.config.MetricTypes {
		group := &report.MetricGroup{
			Type:          metricType.Type,
			MetricsByName: make(map[string][]report.MetricData),
		}
		data.MetricGroups[metricType.Type] = group

		for _, metric := range metricType.Metrics {
			log.Printf("[DEBUG] 查询指标 %s, 查询语句: %s, 数据源: %s", metric.Name, metric.Query, c.prometheusURL)

			// 动态基线：先拉取历史窗口数据（仅当启用基线检测）
			var baselineStreams model.Matrix
			if metric.BaselineEnabled {
				baselineStreams = c.fetchBaselineSeries(ctx, metric.Query, metric.BaselineWindow)
			}

			result, _, err := c.Client.Query(ctx, metric.Query, time.Now())
			if err != nil {
				log.Printf("警告: 查询指标 %s 失败: %v", metric.Name, err)
				continue
			}
			log.Printf("指标 [%s] 查询结果: %+v", metric.Name, result)

			switch v := result.(type) {
			case model.Vector:
				metrics := make([]report.MetricData, 0, len(v))
				for _, sample := range v {
					log.Printf("指标 [%s] 原始数据: %+v, 值: %+v", metric.Name, sample.Metric, sample.Value)

					availableLabels := make(map[string]string)
					for labelName, labelValue := range sample.Metric {
						availableLabels[string(labelName)] = string(labelValue)
					}

					labels := make([]report.LabelData, 0, len(metric.Labels))
					for configLabel, configAlias := range metric.Labels {
						labelValue := "-"
						if rawValue, exists := availableLabels[configLabel]; exists && rawValue != "" {
							labelValue = rawValue
						} else {
							log.Printf("警告: 指标 [%s] 标签 [%s] 缺失或为空", metric.Name, configLabel)
						}

						labels = append(labels, report.LabelData{
							Name:  configLabel,
							Alias: configAlias,
							Value: labelValue,
						})
					}

					value := float64(sample.Value)

					// 检查值是否有效（非NaN且有限）
					if math.IsNaN(value) || math.IsInf(value, 0) {
						log.Printf("警告: 指标 [%s] 返回无效值 (NaN/Inf): %v, 跳过该条记录", metric.Name, value)
						continue
					}

					status := getStatus(value, metric.Threshold, metric.ThresholdType, metric.ThresholdStatus)
					metricData := report.MetricData{
						Name:          metric.Name,
						Description:   metric.Description,
						Value:         value,
						Threshold:     metric.Threshold,
						ThresholdType: metric.ThresholdType,
						Unit:          metric.Unit,
						Status:        status,
						StatusText:    report.GetStatusText(status),
						Timestamp:     time.Now(),
						Labels:        labels,
					}

					// 动态基线异常检测：基线命中异常时覆盖状态（静态阈值仍作为兜底）
					if metric.BaselineEnabled && len(baselineStreams) > 0 {
						if histValues, ok := matchBaselineSeries(baselineStreams, sample.Metric); ok {
							bs := anomaly.Analyze(value, histValues, metric.BaselineZScore, metric.BaselineMinSamples)
							metricData.BaselineEnabled = true
							metricData.BaselineMean = bs.Mean
							metricData.BaselineStdDev = bs.StdDev
							metricData.BaselineMin = bs.Min
							metricData.BaselineMax = bs.Max
							metricData.BaselineCount = bs.Count
							metricData.BaselineZScore = bs.ZScore
							if bs.Level != "" {
								metricData.Status = bs.Level
								metricData.StatusText = report.GetStatusText(bs.Level)
								log.Printf("指标 [%s] 动态基线异常: value=%.2f 基线均值=%.2f 标准差=%.2f z-score=%.2f 等级=%s",
									metric.Name, value, bs.Mean, bs.StdDev, bs.ZScore, bs.Level)
							}
						} else {
							log.Printf("指标 [%s] 未在历史序列中找到匹配 (labels=%v)，跳过基线判断", metric.Name, sample.Metric)
						}
					}

					if err := validateMetricData(metricData, metric.Labels); err != nil {
						log.Printf("警告: 指标 [%s] 数据验证失败: %v", metric.Name, err)
						continue
					}

					metrics = append(metrics, metricData)
				}
				group.MetricsByName[metric.Name] = metrics
			}
		}
	}
	return data, nil
}

// fetchBaselineSeries 拉取指标在历史窗口内的时序数据（matrix），用于动态基线计算。
// 查询失败或结果类型不匹配时返回 nil（调用方会跳过基线判断）。
func (c *Collector) fetchBaselineSeries(ctx context.Context, query, window string) model.Matrix {
	win := parseBaselineWindow(window)
	step := win / 96 // 约 96 个采样点
	if step < time.Minute {
		step = time.Minute
	}
	r := v1.Range{
		Start: time.Now().Add(-win),
		End:   time.Now(),
		Step:  step,
	}
	qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, _, err := c.Client.QueryRange(qctx, query, r)
	if err != nil {
		log.Printf("警告: 指标 [%s] 拉取基线数据失败: %v", query, err)
		return nil
	}
	if m, ok := result.(model.Matrix); ok {
		return m
	}
	return nil
}

// parseBaselineWindow 解析历史窗口字符串，支持 "7d"、"168h"、"24h" 等格式，默认 7 天。
func parseBaselineWindow(s string) time.Duration {
	if s == "" {
		return 7 * 24 * time.Hour
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < time.Hour {
			return time.Hour
		}
		return d
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(s), "d")
	if n, err := strconv.Atoi(trimmed); err == nil && n > 0 {
		return time.Duration(n) * 24 * time.Hour
	}
	return 7 * 24 * time.Hour
}

// matchBaselineSeries 从历史 matrix 中寻找与目标标签集匹配的序列，返回其样本值。
// 匹配规则：目标（当前 sample）的所有标签（__name__ 除外）必须是序列标签的子集。
func matchBaselineSeries(streams model.Matrix, target model.Metric) ([]float64, bool) {
	for _, s := range streams {
		if !labelsContain(s.Metric, target) {
			continue
		}
		values := make([]float64, 0, len(s.Values))
		for _, p := range s.Values {
			v := float64(p.Value)
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				values = append(values, v)
			}
		}
		if len(values) > 0 {
			return values, true
		}
	}
	return nil, false
}

// labelsContain 判断 streamLabels 是否包含 target 的所有标签（__name__ 除外）。
func labelsContain(streamLabels, target model.Metric) bool {
	for k, v := range target {
		if k == model.MetricNameLabel {
			continue
		}
		sv, ok := streamLabels[k]
		if !ok || sv != v {
			return false
		}
	}
	return true
}

// validateMetricData 验证指标数据的完整性
func validateMetricData(data report.MetricData, configLabels map[string]string) error {
	if len(data.Labels) != len(configLabels) {
		return fmt.Errorf("标签数量不匹配: 期望 %d, 实际 %d",
			len(configLabels), len(data.Labels))
	}

	labelMap := make(map[string]bool)
	for _, label := range data.Labels {
		if _, exists := configLabels[label.Name]; !exists {
			return fmt.Errorf("发现未配置的标签: %s", label.Name)
		}
		if label.Value == "" {
			return fmt.Errorf("标签 %s 值为空", label.Name)
		}
		labelMap[label.Name] = true
	}

	return nil
}

// getStatus 获取状态 - 支持threshold_status配置
func getStatus(value, threshold float64, thresholdType, thresholdStatus string) string {
	if thresholdType == "" {
		thresholdType = "greater"
	}
	if thresholdStatus == "" {
		thresholdStatus = "critical" // 默认阈值触发时为严重
	}

	// 判断是否触发阈值条件
	triggered := false
	switch thresholdType {
	case "greater":
		triggered = value > threshold
	case "greater_equal":
		triggered = value >= threshold
	case "less":
		triggered = value < threshold
	case "less_equal":
		triggered = value <= threshold
	case "equal":
		triggered = value == threshold
	case "not_equal":
		triggered = value != threshold
	}

	if triggered {
		// 阈值条件触发，返回配置的状态
		return thresholdStatus
	}

	// 未触发阈值条件，判断是否接近阈值（警告状态）
	const warningMargin = 0.1 // 10% 的预警告区间
	warningTriggered := false
	switch thresholdType {
	case "greater", "greater_equal":
		if threshold > 0 {
			warningTriggered = value >= threshold*(1-warningMargin)
		}
	case "less", "less_equal":
		if threshold > 0 {
			warningTriggered = value <= threshold*(1+warningMargin)
		}
	case "equal":
		if threshold > 0 {
			warningTriggered = math.Abs(value-threshold) <= threshold*0.2
		}
	case "not_equal":
		if threshold > 0 {
			warningTriggered = math.Abs(value-threshold) <= threshold*0.1
		}
	}

	if warningTriggered {
		return "warning"
	}

	// 既未触发阈值也未接近阈值，正常状态
	return "normal"
}

// validateLabels 验证标签数据的完整性
func validateLabels(labels []report.LabelData) bool {
	for _, label := range labels {
		if label.Value == "" || label.Value == "-" {
			return false
		}
	}
	return true
}
