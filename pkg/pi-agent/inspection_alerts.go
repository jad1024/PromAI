package piagent

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"PromAI/pkg/database"
)

// InspectionAlertItem 表示一条用于报告渲染的告警实例（结构化版本）。
// 与 collectActiveAlerts 输出 prompt 字符串不同：本结构体供 pkg/report
// 直接 JSON 序列化到 ChartData，由 templates/report.html 前端 JS 渲染。
type InspectionAlertItem struct {
	Severity         string            `json:"severity"`  // critical / warning / info / unknown
	State            string            `json:"state"`     // firing / pending
	Alertname        string            `json:"alertname"` // 优先取 AlertRule.Name，回退 labels.alertname
	Source           string            `json:"source"`    // 数据源 / 外部告警源 显示名（"名称(type)"）
	Duration         string            `json:"duration"`  // 已持续时长（人类可读）
	ActiveAt         time.Time         `json:"active_at"`
	Value            float64           `json:"value,omitempty"`
	Threshold        float64           `json:"threshold,omitempty"`
	Labels           map[string]string `json:"labels"`             // 完整 labels（键值字符串）
	Annotations      map[string]string `json:"annotations"`        // 完整 annotations
	Expected         string            `json:"expected,omitempty"` // 命中的预期行为规则名（如 "实时流Redis"）；空表示不命中
	DatasourceID     uint              `json:"datasource_id"`
	ExternalSourceID uint              `json:"external_source_id,omitempty"`
	RuleID           uint              `json:"rule_id"`
}

// InspectionIncidentRow 表示聚合后的一行故障（按 alertname 汇总）。
type InspectionIncidentRow struct {
	Alertname     string `json:"alertname"`
	Severity      string `json:"severity"` // 取该故障下实例最严重等级
	InstanceCount int    `json:"instance_count"`
	SourceCount   int    `json:"source_count"`
	IsStorm       bool   `json:"is_storm"`       // 达到风暴阈值
	ExpectedCount int    `json:"expected_count"` // 命中预期行为的实例数
}

// AlertSummary 是给报告用的告警聚合视图。
type AlertSummary struct {
	SourceFilterLabel string                  `json:"source_filter_label"` // "按告警源过滤：[名称A][名称B]" / "全部告警源"
	Alerts            []InspectionAlertItem   `json:"alerts"`
	Incidents         []InspectionIncidentRow `json:"incidents"`
	TotalFiring       int                     `json:"total_firing"`
	TotalPending      int                     `json:"total_pending"`
	StormThreshold    int                     `json:"storm_threshold"`
	WindowMinutes     int                     `json:"window_minutes"`
	GeneratedAt       time.Time               `json:"generated_at"`
}

// ToMap 返回一个可直接被 json.Marshal 序列化的 map，供 pkg/report 注入 ChartData。
// 这样 pkg/report 就不需要反向依赖 pkg/pi-agent 的具体类型。
func (s *AlertSummary) ToMap() map[string]interface{} {
	if s == nil {
		return nil
	}
	return map[string]interface{}{
		"source_filter_label": s.SourceFilterLabel,
		"alerts":              s.Alerts,
		"incidents":           s.Incidents,
		"total_firing":        s.TotalFiring,
		"total_pending":       s.TotalPending,
		"storm_threshold":     s.StormThreshold,
		"window_minutes":      s.WindowMinutes,
		"generated_at":        s.GeneratedAt,
	}
}

// CollectInspectionAlertData 收集当前活跃告警 + 事件聚合，按 alertSourceIDs 过滤。
// 供 pkg/report 渲染到报告 HTML；与 collectActiveAlerts/collectIncidentSummary
// （用于 prompt 字符串）的查询逻辑保持一致，但返回结构体。
func CollectInspectionAlertData(max int, alertSourceIDs []uint) *AlertSummary {
	if max <= 0 {
		max = 15
	}
	sum := &AlertSummary{
		GeneratedAt:    time.Now(),
		StormThreshold: 10,
		WindowMinutes:  10,
	}
	if alertSourceIDs == nil {
		sum.SourceFilterLabel = "全部告警源"
	} else {
		// 解析外部告警源名字（用于报告头部展示）
		var extSources []database.ExternalAlertSource
		database.DB.Select("id, name").Where("id IN ?", alertSourceIDs).Find(&extSources)
		if len(extSources) == 0 {
			sum.SourceFilterLabel = "按告警源过滤（无匹配源）"
		} else {
			label := "按告警源过滤："
			for _, s := range extSources {
				label += "[" + s.Name + "]"
			}
			sum.SourceFilterLabel = label
		}
	}
	if database.DB == nil {
		return sum
	}

	// 加载 denoise 配置
	var settings []database.AppSetting
	if err := database.DB.Where("key IN ?", []string{"alert_denoise_storm_threshold", "alert_denoise_window_minutes"}).Find(&settings).Error; err == nil {
		for _, s := range settings {
			switch s.Key {
			case "alert_denoise_storm_threshold":
				if v, err := strconv.Atoi(s.Value); err == nil && v >= 0 && v <= 1000 {
					sum.StormThreshold = v
				}
			case "alert_denoise_window_minutes":
				if v, err := strconv.Atoi(s.Value); err == nil && v >= 0 && v <= 1440 {
					sum.WindowMinutes = v
				}
			}
		}
	}

	// 预期行为
	expectations := loadInspectionExpectations()

	// 名称映射
	var rules []database.AlertRule
	database.DB.Select("id, name").Find(&rules)
	ruleNames := make(map[uint]string, len(rules))
	for _, r := range rules {
		ruleNames[r.ID] = r.Name
	}
	var dss []database.DataSource
	database.DB.Select("id, name").Find(&dss)
	dsNames := make(map[uint]string, len(dss))
	for _, d := range dss {
		dsNames[d.ID] = d.Name
	}
	var extSourcesAll []database.ExternalAlertSource
	database.DB.Select("id, name, type").Find(&extSourcesAll)
	extNames := make(map[uint]string, len(extSourcesAll))
	for _, s := range extSourcesAll {
		extNames[s.ID] = fmt.Sprintf("%s(%s)", s.Name, s.Type)
	}

	// 1) 取活跃告警实例（按时间倒序，最多 max 条）
	q := database.DB.Where("state IN ?", []string{"firing", "pending"})
	if len(alertSourceIDs) > 0 {
		q = q.Where("external_source_id IN ?", alertSourceIDs)
	}
	var insts []database.AlertInstance
	if err := q.Order("active_at desc").Limit(max).Find(&insts).Error; err != nil {
		return sum
	}

	items := make([]InspectionAlertItem, 0, len(insts))
	for _, in := range insts {
		labels := parseAlertLabels(in.LabelsJSON)
		name := ruleNames[in.RuleID]
		if name == "" {
			name = labels["alertname"]
		}
		if name == "" {
			name = fmt.Sprintf("告警#%d", in.RuleID)
		}
		ds := dsNames[in.DatasourceID]
		if src := extNames[in.ExternalSourceID]; src != "" {
			ds = src
		}
		if ds == "" {
			ds = "external"
		}
		sev := in.Severity
		if sev == "" {
			sev = "unknown"
		}
		if in.State == "firing" {
			sum.TotalFiring++
		} else {
			sum.TotalPending++
		}
		ann := parseAlertLabels(in.AnnotationsJSON)
		// 清理空值的 label/annotation
		cleanLabels := make(map[string]string, len(labels))
		for k, v := range labels {
			if v != "" {
				cleanLabels[k] = v
			}
		}
		cleanAnn := make(map[string]string, len(ann))
		for k, v := range ann {
			if v != "" {
				cleanAnn[k] = v
			}
		}
		expName := matchExpectation(expectations, labels)
		items = append(items, InspectionAlertItem{
			Severity:         sev,
			State:            in.State,
			Alertname:        name,
			Source:           ds,
			Duration:         durationHuman(time.Since(in.ActiveAt)),
			ActiveAt:         in.ActiveAt,
			Value:            in.Value,
			Threshold:        in.Threshold,
			Labels:           cleanLabels,
			Annotations:      cleanAnn,
			Expected:         expName,
			DatasourceID:     in.DatasourceID,
			ExternalSourceID: in.ExternalSourceID,
			RuleID:           in.RuleID,
		})
	}
	sum.Alerts = items

	// 2) 事件聚合：按 alertname 分组（最多 500 条用于计算 storm）
	q2 := database.DB.Where("state IN ?", []string{"firing", "pending"})
	if len(alertSourceIDs) > 0 {
		q2 = q2.Where("external_source_id IN ?", alertSourceIDs)
	}
	var allInsts []database.AlertInstance
	if err := q2.Order("active_at desc").Limit(500).Find(&allInsts).Error; err == nil && len(allInsts) > 0 {
		type bucket struct {
			severity      string
			instanceCount int
			sources       map[string]struct{}
			expectedCount int
		}
		buckets := make(map[string]*bucket)
		sevRank := map[string]int{"critical": 3, "warning": 2, "info": 1, "unknown": 0}
		for _, in := range allInsts {
			labels := parseAlertLabels(in.LabelsJSON)
			name := ruleNames[in.RuleID]
			if name == "" {
				name = labels["alertname"]
			}
			if name == "" {
				name = fmt.Sprintf("告警#%d", in.RuleID)
			}
			b, ok := buckets[name]
			if !ok {
				b = &bucket{sources: make(map[string]struct{}), severity: "unknown"}
				buckets[name] = b
			}
			b.instanceCount++
			srcName := dsNames[in.DatasourceID]
			if src := extNames[in.ExternalSourceID]; src != "" {
				srcName = src
			}
			if srcName == "" {
				srcName = "external"
			}
			b.sources[srcName] = struct{}{}
			sev := in.Severity
			if sev == "" {
				sev = "unknown"
			}
			if sevRank[sev] > sevRank[b.severity] {
				b.severity = sev
			}
			if matchExpectation(expectations, labels) != "" {
				b.expectedCount++
			}
		}
		// 按严重度 + 实例数排序
		rows := make([]InspectionIncidentRow, 0, len(buckets))
		for name, b := range buckets {
			rows = append(rows, InspectionIncidentRow{
				Alertname:     name,
				Severity:      b.severity,
				InstanceCount: b.instanceCount,
				SourceCount:   len(b.sources),
				IsStorm:       b.instanceCount >= sum.StormThreshold,
				ExpectedCount: b.expectedCount,
			})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].IsStorm != rows[j].IsStorm {
				return rows[i].IsStorm
			}
			if rows[i].InstanceCount != rows[j].InstanceCount {
				return rows[i].InstanceCount > rows[j].InstanceCount
			}
			return rows[i].Alertname < rows[j].Alertname
		})
		if len(rows) > 20 {
			rows = rows[:20]
		}
		sum.Incidents = rows
	}

	return sum
}
