package piagent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"PromAI/pkg/config"
	"PromAI/pkg/database"
)

func resolveToolDatasource(base *config.Config, db DB, datasource string) (*database.DataSource, string, string, string, string) {
	promURL := base.PrometheusURL
	promUser := base.PrometheusUsername
	promPass := base.PrometheusPassword
	dsName := promURL

	datasource = strings.TrimSpace(datasource)
	if datasource == "" {
		return nil, promURL, promUser, promPass, dsName
	}
	if strings.HasPrefix(datasource, "http://") || strings.HasPrefix(datasource, "https://") {
		return nil, datasource, promUser, promPass, datasource
	}

	var ds database.DataSource
	if db.Model(&database.DataSource{}).Where("enabled = ? AND LOWER(name) = LOWER(?)", true, datasource).First(&ds).Error() != nil {
		like := "%" + datasource + "%"
		if db.Model(&database.DataSource{}).Where("enabled = ? AND (name LIKE ? OR url LIKE ?)", true, like, like).First(&ds).Error() != nil {
			return nil, promURL, promUser, promPass, dsName
		}
	}

	database.NormalizeDataSourceTemplateFields(&ds)
	return &ds, ds.URL, ds.Username, ds.Password, ds.Name
}

func buildToolRuntimeMetricConfig(base *config.Config, db DB, ds *database.DataSource) (*config.Config, error) {
	cfg := cloneToolConfig(base)
	if ds != nil && strings.TrimSpace(ds.ProjectName) != "" {
		cfg.ProjectName = strings.TrimSpace(ds.ProjectName)
	}
	if ds == nil {
		return cfg, nil
	}

	configs, err := loadToolEffectiveMetricConfigs(db, ds)
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		cfg.MetricTypes = nil
		return cfg, nil
	}
	cfg.MetricTypes = buildToolMetricTypesFromConfigs(db, configs)
	return cfg, nil
}

func loadToolEffectiveMetricConfigs(db DB, ds *database.DataSource) ([]database.MetricConfig, error) {
	if ds == nil {
		return nil, nil
	}
	database.NormalizeDataSourceTemplateFields(ds)
	if len(ds.TemplateIDs) > 0 {
		return loadToolTemplateMetricConfigs(db, ds.TemplateIDs)
	}
	var configs []database.MetricConfig
	if err := db.Model(&database.MetricConfig{}).
		Where("(datasource_id IS NULL OR datasource_id = ?) AND metric_type_id IS NOT NULL", ds.ID).
		Order("metric_type_id asc, sort_order asc, id asc").
		Find(&configs).Error(); err != nil {
		return nil, err
	}
	return configs, nil
}

func loadToolTemplateMetricConfigs(db DB, templateIDs []uint) ([]database.MetricConfig, error) {
	templateIDs = database.ParseTemplateIDs(database.EncodeTemplateIDs(templateIDs))
	if len(templateIDs) == 0 {
		return nil, nil
	}

	var merged []database.MetricConfig
	indexByID := make(map[uint]int)
	for _, templateID := range templateIDs {
		var links []database.InspectionTemplateMetric
		if err := db.Model(&database.InspectionTemplateMetric{}).Where("template_id = ?", templateID).Find(&links).Error(); err != nil {
			return nil, err
		}
		if len(links) == 0 {
			continue
		}

		configIDs := make([]uint, 0, len(links))
		for _, link := range links {
			configIDs = append(configIDs, link.MetricConfigID)
		}

		var configs []database.MetricConfig
		if err := db.Model(&database.MetricConfig{}).
			Where("id IN ?", configIDs).
			Order("metric_type_id asc, sort_order asc, id asc").
			Find(&configs).Error(); err != nil {
			return nil, err
		}

		for i := range configs {
			var override database.TemplateMetricOverride
			if db.Model(&database.TemplateMetricOverride{}).Where("template_id = ? AND metric_config_id = ?", templateID, configs[i].ID).First(&override).Error() == nil {
				override.Apply(&configs[i])
			}
			if idx, ok := indexByID[configs[i].ID]; ok {
				merged[idx] = configs[i]
			} else {
				indexByID[configs[i].ID] = len(merged)
				merged = append(merged, configs[i])
			}
		}
	}
	return merged, nil
}

// loadToolScopedMetricConfigs 按显式指定的巡检范围解析指标配置。
// 范围优先级：模板（templateID）> 具体指标（metricConfigIDs）> 指标分组（metricTypeIDs）。
// 返回 nil 表示未指定范围（调用方应走默认的模板/数据源绑定逻辑）。
func loadToolScopedMetricConfigs(db DB, ds *database.DataSource, metricTypeIDs, metricConfigIDs, templateID string) ([]database.MetricConfig, error) {
	// 1) 模板优先
	if id := strings.TrimSpace(templateID); id != "" {
		tid, err := resolveToolTemplateID(db, id)
		if err != nil {
			return nil, err
		}
		return loadToolTemplateMetricConfigs(db, []uint{tid})
	}

	// 2) 具体指标
	if raw := strings.TrimSpace(metricConfigIDs); raw != "" {
		ids, names := parseToolIDList(raw)
		q := db.Model(&database.MetricConfig{}).Where("metric_type_id IS NOT NULL")
		if len(ids) > 0 {
			q = q.Where("id IN ?", ids)
		} else if len(names) > 0 {
			q = q.Where("name IN ?", names)
		}
		var configs []database.MetricConfig
		q.Order("metric_type_id asc, sort_order asc, id asc").Find(&configs)
		return configs, nil
	}

	// 3) 指标分组
	if raw := strings.TrimSpace(metricTypeIDs); raw != "" {
		ids, names := parseToolIDList(raw)
		q := db.Model(&database.MetricConfig{}).Where("metric_type_id IS NOT NULL")
		if len(ids) > 0 {
			q = q.Where("metric_type_id IN ?", ids)
		} else if len(names) > 0 {
			var mts []database.MetricType
			db.Model(&database.MetricType{}).Where("type_name IN ?", names).Find(&mts)
			mtIDs := make([]uint, 0, len(mts))
			for _, mt := range mts {
				mtIDs = append(mtIDs, mt.ID)
			}
			if len(mtIDs) == 0 {
				return nil, nil
			}
			q = q.Where("metric_type_id IN ?", mtIDs)
		}
		var configs []database.MetricConfig
		q.Order("metric_type_id asc, sort_order asc, id asc").Find(&configs)
		return configs, nil
	}
	return nil, nil
}

// resolveToolTemplateID 按 ID 或名称解析巡检模板 ID。
func resolveToolTemplateID(db DB, s string) (uint, error) {
	if id, err := strconv.ParseUint(s, 10, 64); err == nil && id > 0 {
		var t database.InspectionTemplate
		if db.Model(&database.InspectionTemplate{}).Where("id = ?", id).First(&t).Error() == nil {
			return uint(id), nil
		}
	}
	var t database.InspectionTemplate
	if err := db.Model(&database.InspectionTemplate{}).Where("LOWER(name) = LOWER(?)", s).First(&t).Error(); err == nil {
		return t.ID, nil
	}
	if err := db.Model(&database.InspectionTemplate{}).Where("name LIKE ?", "%"+s+"%").First(&t).Error(); err == nil {
		return t.ID, nil
	}
	return 0, fmt.Errorf("未找到巡检模板: %s", s)
}

// parseToolIDList 解析逗号分隔的 ID 或名称列表。
// 若所有片段都是数字则视为 ID 列表；否则视为名称列表（按名称精确匹配）。
func parseToolIDList(raw string) ([]uint, []string) {
	parts := splitTrim(raw, ",")
	if len(parts) == 0 {
		return nil, nil
	}
	allNumeric := true
	ids := make([]uint, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || n == 0 {
			allNumeric = false
			break
		}
		ids = append(ids, uint(n))
	}
	if allNumeric {
		return ids, nil
	}
	return nil, parts
}

// splitTrim 按分隔符拆分并去空、去首尾空白。
func splitTrim(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func buildToolMetricTypesFromConfigs(db DB, selectedConfigs []database.MetricConfig) []config.MetricType {
	if len(selectedConfigs) == 0 {
		return nil
	}
	grouped := make(map[uint][]database.MetricConfig)
	for _, cfg := range selectedConfigs {
		grouped[cfg.MetricTypeID] = append(grouped[cfg.MetricTypeID], cfg)
	}

	var metricTypes []database.MetricType
	db.Model(&database.MetricType{}).Order("sort_order asc, id asc").Find(&metricTypes)

	result := make([]config.MetricType, 0, len(grouped))
	for _, mt := range metricTypes {
		configs := grouped[mt.ID]
		if len(configs) == 0 {
			continue
		}
		item := config.MetricType{Type: mt.TypeName}
		for _, cfg := range configs {
			var labels map[string]string
			if cfg.LabelsJSON != "" {
				_ = json.Unmarshal([]byte(cfg.LabelsJSON), &labels)
			}
			item.Metrics = append(item.Metrics, config.MetricConfig{
				Name:            cfg.Name,
				Description:     cfg.Description,
				Query:           cfg.Query,
				Threshold:       cfg.Threshold,
				Unit:            cfg.Unit,
				Labels:          labels,
				ThresholdType:   cfg.ThresholdType,
				ThresholdStatus: cfg.ThresholdStatus,
				WarningEnabled:  cfg.WarningEnabled,
				WarningMargin:   cfg.WarningMargin,
			})
		}
		result = append(result, item)
	}
	return result
}

func cloneToolConfig(base *config.Config) *config.Config {
	if base == nil {
		return &config.Config{}
	}
	cloned := *base
	if base.MetricTypes != nil {
		cloned.MetricTypes = append([]config.MetricType(nil), base.MetricTypes...)
	}
	return &cloned
}
