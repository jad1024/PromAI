package alerting

import (
	"encoding/json"
	"strings"

	"PromAI/pkg/database"
)

// DatasourceSelector 描述一条告警规则作用到哪些数据源。
//   - All=true       → 应用到全部启用的数据源
//   - ProjectName    → 按 DataSource.ProjectName 精确匹配
//   - NameRegex      → 按 DataSource.Name 正则匹配
//   - URLContains    → DataSource.URL 包含该子串
// 字段之间是 AND 关系；与显式 DatasourceIDs 是互补（任一命中即可）。
type DatasourceSelector struct {
	All         bool   `json:"all,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	NameRegex   string `json:"name_regex,omitempty"`
	URLContains string `json:"url_contains,omitempty"`
}

// IsZero 判断 selector 是否未设置任何条件
func (s *DatasourceSelector) IsZero() bool {
	if s == nil {
		return true
	}
	return !s.All && s.ProjectName == "" && s.NameRegex == "" && s.URLContains == ""
}

// Match 判断给定数据源是否命中本 selector
func (s *DatasourceSelector) Match(ds *database.DataSource) bool {
	if s == nil || ds == nil {
		return false
	}
	if !ds.Enabled {
		return false
	}
	if s.All {
		// All 模式下，其他条件仍然作为收窄过滤
	} else if s.IsZero() {
		return false
	}
	if s.ProjectName != "" && !strings.EqualFold(ds.ProjectName, s.ProjectName) {
		return false
	}
	if s.URLContains != "" && !strings.Contains(ds.URL, s.URLContains) {
		return false
	}
	if s.NameRegex != "" {
		m := Matcher{Name: "name", Op: MatchRegex, Value: s.NameRegex}
		if !m.Match(ds.Name) {
			return false
		}
	}
	return true
}

// DecodeDatasourceSelector 反序列化 selector JSON
func DecodeDatasourceSelector(s string) *DatasourceSelector {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var sel DatasourceSelector
	if err := json.Unmarshal([]byte(s), &sel); err != nil {
		return nil
	}
	if sel.IsZero() {
		return nil
	}
	return &sel
}

// EncodeDatasourceSelector 序列化 selector
func EncodeDatasourceSelector(sel *DatasourceSelector) string {
	if sel == nil || sel.IsZero() {
		return ""
	}
	b, _ := json.Marshal(sel)
	return string(b)
}

// ResolveDatasourceIDs 根据规则的显式 IDs + selector 解析出最终的数据源列表
func ResolveDatasourceIDs(explicit []uint, selector *DatasourceSelector, allDataSources []database.DataSource) []uint {
	picked := make(map[uint]struct{}, len(explicit))
	for _, id := range explicit {
		picked[id] = struct{}{}
	}
	if !selector.IsZero() {
		for i := range allDataSources {
			ds := &allDataSources[i]
			if selector.Match(ds) {
				picked[ds.ID] = struct{}{}
			}
		}
	}
	if len(picked) == 0 {
		return nil
	}
	out := make([]uint, 0, len(picked))
	for id := range picked {
		out = append(out, id)
	}
	return out
}
