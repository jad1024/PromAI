package database

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// ResolveTemplateNames 从模板 ID 数组查表得到 `[名称A][名称B]` 形式的展示串。
// 任一 ID 查不到时跳过该 ID（不影响其它）。所有 ID 都为空时返回空串。
func ResolveTemplateNames(db *gorm.DB, ids []uint) string {
	if len(ids) == 0 {
		return ""
	}
	var tpls []InspectionTemplate
	if err := db.Where("id IN ?", ids).Find(&tpls).Error; err != nil {
		return ""
	}
	nameByID := make(map[uint]string, len(tpls))
	for _, t := range tpls {
		nameByID[t.ID] = t.Name
	}
	var b strings.Builder
	for _, id := range ids {
		if name, ok := nameByID[id]; ok {
			b.WriteString("[")
			b.WriteString(name)
			b.WriteString("]")
		}
	}
	return b.String()
}

// StringifyTemplateIDs 将 ID 数组序列化为 JSON 字符串（空数组返回 ""）。
func StringifyTemplateIDs(ids []uint) string {
	if len(ids) == 0 {
		return ""
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return fmt.Sprintf("%v", ids)
	}
	return string(b)
}

func ParseTemplateIDs(raw string) []uint {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var ids []uint
	if strings.HasPrefix(raw, "[") {
		var arr []uint
		if json.Unmarshal([]byte(raw), &arr) == nil {
			return uniqueTemplateIDs(arr)
		}
	}

	parts := strings.Split(raw, ",")
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, "[]"))
		if part == "" {
			continue
		}
		id, err := strconv.ParseUint(part, 10, 64)
		if err != nil || id == 0 {
			continue
		}
		ids = append(ids, uint(id))
	}
	return uniqueTemplateIDs(ids)
}

func EncodeTemplateIDs(ids []uint) string {
	ids = uniqueTemplateIDs(ids)
	if len(ids) == 0 {
		return ""
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return ""
	}
	return string(b)
}

func PrimaryTemplateID(ids []uint) *uint {
	if len(ids) == 0 || ids[0] == 0 {
		return nil
	}
	id := ids[0]
	return &id
}

func NormalizeDataSourceTemplateFields(ds *DataSource) {
	if ds == nil {
		return
	}
	ids := uniqueTemplateIDs(ds.TemplateIDs)
	if len(ids) == 0 {
		ids = ParseTemplateIDs(ds.TemplateIDsRaw)
	}
	if len(ids) == 0 && ds.TemplateID != nil && *ds.TemplateID > 0 {
		ids = []uint{*ds.TemplateID}
	}
	ds.TemplateIDs = ids
	ds.TemplateIDsRaw = EncodeTemplateIDs(ids)
	ds.TemplateID = PrimaryTemplateID(ids)
}

func NormalizeDataSourcesTemplateFields(list []DataSource) {
	for i := range list {
		NormalizeDataSourceTemplateFields(&list[i])
	}
}

func migrateTemplateIDs(db *gorm.DB) error {
	var sources []DataSource
	if err := db.Find(&sources).Error; err != nil {
		return err
	}
	for i := range sources {
		beforeRaw := sources[i].TemplateIDsRaw
		beforePrimary := sources[i].TemplateID
		NormalizeDataSourceTemplateFields(&sources[i])
		if beforeRaw == sources[i].TemplateIDsRaw && samePrimaryTemplateID(beforePrimary, sources[i].TemplateID) {
			continue
		}
		if err := db.Model(&DataSource{}).Where("id = ?", sources[i].ID).Updates(map[string]any{
			"template_id":  sources[i].TemplateID,
			"template_ids": sources[i].TemplateIDsRaw,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func uniqueTemplateIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func samePrimaryTemplateID(a, b *uint) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
