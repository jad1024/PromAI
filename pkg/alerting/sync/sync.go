// Package sync 提供外部告警平台（n9e / 华为云 CES）告警规则的只读同步能力。
// 同步结果写入 database.ExternalRule 表，供 PromAI 统一展示平台规则视图。
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"PromAI/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SyncRules 按源类型分发同步规则，返回 新增数/更新数/总数
func SyncRules(ctx context.Context, source *database.ExternalAlertSource) (created, updated, total int, err error) {
	start := time.Now()
	log.Printf("[ExternalSync] 开始同步告警源[%s] %s 的规则", source.Name, source.Type)
	switch source.Type {
	case "n9e":
		created, updated, total, err = syncN9ERules(ctx, source)
	case "huaweicloud", "huawei", "ces":
		created, updated, total, err = syncHuaweiCloudRules(ctx, source)
	case "aliyun", "alibabacloud", "alicloud":
		err = fmt.Errorf("阿里云告警源当前支持 webhook 告警接收（云监控报警回调），规则自动同步暂未开放")
	default:
		err = errUnsupportedType(source.Type)
	}
	dur := time.Since(start).Round(time.Millisecond)
	if err != nil {
		database.DB.Model(source).Updates(map[string]interface{}{
			"last_sync_at": time.Now(), "sync_status": "failed", "sync_error": truncate(err.Error(), 500),
		})
		log.Printf("[ExternalSync] 告警源[%s] 同步失败: %v (%s)", source.Name, err, dur)
		return
	}
	database.DB.Model(source).Updates(map[string]interface{}{
		"last_sync_at": time.Now(), "sync_status": "success", "sync_error": "",
	})
	log.Printf("[ExternalSync] 告警源[%s] 同步完成: 新增%d 更新%d 共%d (%s)", source.Name, created, updated, total, dur)
	return
}

// upsertExternalRule 按 (source_id, external_id) 幂等写入
func upsertExternalRule(source *database.ExternalAlertSource, rule *database.ExternalRule) (created bool, err error) {
	rule.SourceID = source.ID
	rule.SourceType = source.Type
	rule.LastSeenAt = time.Now()
	var existing database.ExternalRule
	err = database.DB.Where("source_id = ? AND external_id = ?", source.ID, rule.ExternalID).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		if e := database.DB.Create(rule).Error; e != nil {
			return false, e
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	rule.ID = existing.ID
	err = database.DB.Model(&database.ExternalRule{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
		"rule_name":    rule.RuleName,
		"severity":     rule.Severity,
		"status":       rule.Status,
		"condition":    rule.Condition,
		"raw_json":     rule.RawJSON,
		"last_seen_at": rule.LastSeenAt,
		"updated_at":   time.Now(),
	}).Error
	return false, err
}

// upsertExternalRulesClause 用 OnConflict 批量幂等（简单路径）
func upsertExternalRules(source *database.ExternalAlertSource, rules []database.ExternalRule) (created, updated int) {
	for i := range rules {
		rules[i].SourceID = source.ID
		rules[i].SourceType = source.Type
		rules[i].LastSeenAt = time.Now()
	}
	// 使用唯一索引 (source_id, external_id) OnConflict
	err := database.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_id"}, {Name: "external_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"rule_name", "severity", "status", "condition", "raw_json", "last_seen_at", "updated_at"}),
	}).Create(&rules).Error
	if err != nil {
		log.Printf("[ExternalSync] 批量 upsert 失败，退化为逐条: %v", err)
		created, updated = 0, 0
		for i := range rules {
			isNew, e := upsertExternalRule(source, &rules[i])
			if e != nil {
				log.Printf("[ExternalSync] 写入规则[%s]失败: %v", rules[i].RuleName, e)
				continue
			}
			if isNew {
				created++
			} else {
				updated++
			}
		}
		return created, updated
	}
	// OnConflict 无法直接区分 created/updated，粗略统计：总数减去已存在数
	var existCount int64
	database.DB.Model(&database.ExternalRule{}).Where("source_id = ? AND external_id IN ?", source.ID, externalIDs(rules)).Count(&existCount)
	updated = int(existCount)
	created = len(rules) - updated

	// 同步写入 AlertRule（source_type=external, origin=sync），供"告警规则"页面统一管理展示
	for i := range rules {
		if e := upsertSyncedAlertRule(source, &rules[i]); e != nil {
			log.Printf("[ExternalSync] 同步规则[%s]到告警规则失败: %v", rules[i].RuleName, e)
		}
	}
	return created, updated
}

// upsertSyncedAlertRule 将外部规则镜像为 AlertRule（origin=sync）。
// 幂等键：(origin_source_id, origin_external_id)。仅更新同步字段，不覆盖用户本地配置。
func upsertSyncedAlertRule(source *database.ExternalAlertSource, er *database.ExternalRule) error {
	var rule database.AlertRule
	err := database.DB.Where("origin = ? AND origin_source_id = ? AND origin_external_id = ?",
		"sync", source.ID, er.ExternalID).First(&rule).Error
	enabled := strings.ToLower(er.Status) != "disabled"
	labels := map[string]interface{}{
		"source_type": er.SourceType,
		"source_name": source.Name,
		"external_id": er.ExternalID,
	}
	labelsJSON, _ := json.Marshal(labels)
	updates := map[string]interface{}{
		"name":        er.RuleName,
		"severity":    er.Severity,
		"expr":        er.Condition,
		"description": "由外部告警源[" + source.Name + "]同步",
		"labels_json": string(labelsJSON),
		"enabled":     enabled,
		"source_type": "external",
		"origin":      "sync",
	}
	if err == gorm.ErrRecordNotFound {
		rule = database.AlertRule{
			Name:             er.RuleName,
			Description:      "由外部告警源[" + source.Name + "]同步",
			SourceType:       "external",
			Origin:           "sync",
			OriginSourceID:   source.ID,
			OriginExternalID: er.ExternalID,
			Severity:         er.Severity,
			Expr:             er.Condition,
			LabelsJSON:       string(labelsJSON),
			Enabled:          enabled,
		}
		return database.DB.Create(&rule).Error
	}
	if err != nil {
		return err
	}
	return database.DB.Model(&database.AlertRule{}).Where("id = ?", rule.ID).Updates(updates).Error
}

func externalIDs(rules []database.ExternalRule) []string {
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ExternalID)
	}
	return ids
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func errUnsupportedType(t string) error {
	return &UnsupportedTypeError{Type: t}
}

// UnsupportedTypeError 未支持的告警源类型
type UnsupportedTypeError struct{ Type string }

func (e *UnsupportedTypeError) Error() string { return "不支持的告警源类型: " + e.Type }
