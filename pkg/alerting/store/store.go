// Package store 封装 Alerting 子系统的所有持久化操作。
//
// 关键设计：
//   - 所有写入都走全局 database.DB；读取支持简单分页/过滤。
//   - 规则缓存内置一个 sync/atomic 指针的快照，由 ReloadRules() 触发刷新；
//     evaluator 调度 tick 直接读快照，避免每轮 tick 都打 DB。
//   - 数据源元数据也做了相同的快照机制。
package store

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"PromAI/pkg/alerting"
	"PromAI/pkg/database"

	"gorm.io/gorm"
)

// RuleSnapshot 是规则缓存快照（不可变，整体替换）
type RuleSnapshot struct {
	Rules        []database.AlertRule
	Routes       []database.AlertRoute
	Silences     []database.AlertSilence
	Inhibits     []database.AlertInhibit
	MetricConfigs map[uint]database.MetricConfig // ID → MetricConfig，供 evaluator 无锁读取
	Loaded       time.Time
}

var (
	ruleSnapshot atomic.Pointer[RuleSnapshot]
	reloadMu     sync.Mutex
)

// Snapshot 返回当前快照（可能为 nil，表示尚未加载）
func Snapshot() *RuleSnapshot {
	return ruleSnapshot.Load()
}

// MustSnapshot 返回快照，若为 nil 则触发一次同步加载
func MustSnapshot() *RuleSnapshot {
	s := ruleSnapshot.Load()
	if s != nil {
		return s
	}
	_ = Reload()
	return ruleSnapshot.Load()
}

// Reload 从数据库重新加载快照，并原子替换
// 所有查询在无锁下并发执行（WAL 模式支持并行读），最后仅指针交换加锁。
func Reload() error {
	db := database.DB
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	var (
		rules    []database.AlertRule
		routes   []database.AlertRoute
		silences []database.AlertSilence
		inhibits []database.AlertInhibit
		mcs      []database.MetricConfig
	)
	if err := db.Where("enabled = ?", true).Find(&rules).Error; err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	for i := range rules {
		decodeRuleRaw(&rules[i])
	}
	if err := db.Where("enabled = ?", true).Order("priority desc, id asc").Find(&routes).Error; err != nil {
		return fmt.Errorf("load routes: %w", err)
	}
	for i := range routes {
		decodeRouteRaw(&routes[i])
	}
	now := time.Now().UTC()
	if err := db.Where("enabled = ? AND starts_at <= ? AND ends_at >= ?", true, now, now).Find(&silences).Error; err != nil {
		return fmt.Errorf("load silences: %w", err)
	}
	if err := db.Where("enabled = ?", true).Find(&inhibits).Error; err != nil {
		return fmt.Errorf("load inhibits: %w", err)
	}
	// 预加载 MetricConfig（供 resolveExpr/resolveThreshold 快照查询，避免每轮 tick 逐条查 DB）
	if err := db.Find(&mcs).Error; err != nil {
		return fmt.Errorf("load metric_configs: %w", err)
	}
	mcMap := make(map[uint]database.MetricConfig, len(mcs))
	for i := range mcs {
		mcMap[mcs[i].ID] = mcs[i]
	}

	snap := &RuleSnapshot{
		Rules:        rules,
		Routes:       routes,
		Silences:     silences,
		Inhibits:     inhibits,
		MetricConfigs: mcMap,
		Loaded:       time.Now(),
	}
	reloadMu.Lock()
	ruleSnapshot.Store(snap)
	reloadMu.Unlock()
	return nil
}

func decodeRuleRaw(r *database.AlertRule) {
	r.DatasourceIDs = alerting.DecodeUintSlice(r.DatasourceIDsRaw)
	r.NotifyChannelIDs = alerting.DecodeUintSlice(r.NotifyChannelIDsRaw)
}

func encodeRuleRaw(r *database.AlertRule) {
	r.DatasourceIDsRaw = alerting.EncodeUintSlice(r.DatasourceIDs)
	r.NotifyChannelIDsRaw = alerting.EncodeUintSlice(r.NotifyChannelIDs)
}

func decodeRouteRaw(r *database.AlertRoute) {
	r.NotifyChannelIDs = alerting.DecodeUintSlice(r.NotifyChannelIDsRaw)
}

func encodeRouteRaw(r *database.AlertRoute) {
	r.NotifyChannelIDsRaw = alerting.EncodeUintSlice(r.NotifyChannelIDs)
}

// ===== AlertRule CRUD ===========================================================

func ListRules(db *gorm.DB, filter map[string]interface{}, page, pageSize int) ([]database.AlertRule, int64, error) {
	q := db.Model(&database.AlertRule{}).Order("id desc")
	if v, ok := filter["enabled"]; ok {
		q = q.Where("enabled = ?", v)
	}
	if v, ok := filter["severity"]; ok && v != "" {
		q = q.Where("severity = ?", v)
	}
	if v, ok := filter["keyword"]; ok && v != "" {
		k := "%" + fmt.Sprint(v) + "%"
		q = q.Where("name LIKE ? OR description LIKE ?", k, k)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	var rules []database.AlertRule
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&rules).Error; err != nil {
		return nil, 0, err
	}
	for i := range rules {
		decodeRuleRaw(&rules[i])
	}
	return rules, total, nil
}

func GetRule(db *gorm.DB, id uint) (*database.AlertRule, error) {
	var r database.AlertRule
	if err := db.First(&r, id).Error; err != nil {
		return nil, err
	}
	decodeRuleRaw(&r)
	return &r, nil
}

func CreateRule(db *gorm.DB, r *database.AlertRule) error {
	encodeRuleRaw(r)
	if err := db.Create(r).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func UpdateRule(db *gorm.DB, r *database.AlertRule) error {
	encodeRuleRaw(r)
	if err := db.Save(r).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func BatchToggleRules(db *gorm.DB, ids []uint, enabled bool) error {
	if len(ids) == 0 {
		return nil
	}
	if err := db.Model(&database.AlertRule{}).Where("id IN ?", ids).Update("enabled", enabled).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

// GenerateRulesFromTemplate 为模版中尚无告警规则的指标自动创建规则
func GenerateRulesFromTemplate(db *gorm.DB, templateID uint) (int, error) {
	// 查询模版关联的指标 ID
	var links []database.InspectionTemplateMetric
	if err := db.Where("template_id = ?", templateID).Find(&links).Error; err != nil {
		return 0, err
	}
	if len(links) == 0 {
		return 0, nil
	}
	cfgIDs := make([]uint, len(links))
	for i, l := range links {
		cfgIDs[i] = l.MetricConfigID
	}
	// 查询已有规则的 metric_config_id
	var existingIDs []uint
	db.Model(&database.AlertRule{}).Where("metric_config_id IN ?", cfgIDs).Pluck("metric_config_id", &existingIDs)
	existingMap := make(map[uint]bool, len(existingIDs))
	for _, id := range existingIDs {
		existingMap[id] = true
	}
	// 查询指标配置详情
	var configs []database.MetricConfig
	if err := db.Where("id IN ?", cfgIDs).Find(&configs).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, cfg := range configs {
		if existingMap[cfg.ID] {
			continue
		}
		// 使用模版的名称加上指标 Config 类名生成唯一规则名称
		ruleName := cfg.Name
		if idx := strings.Index(ruleName, " - "); idx > 0 {
			ruleName = ruleName[idx+3:]
		}
		tmplID := templateID
		rule := &database.AlertRule{
			Name:           cfg.Name + " 告警",
			SourceType:     "metric",
			MetricConfigID:  &cfg.ID,
			TemplateID:     &tmplID,
			Threshold:      cfg.Threshold,
			ThresholdType:  cfg.ThresholdType,
			HasThreshold:   true,
			Severity:       cfg.ThresholdStatus,
			Description:    cfg.Description,
			LabelsJSON:     cfg.LabelsJSON,
			Enabled:        true,
		}
		if rule.Severity == "" {
			rule.Severity = "warning"
		}
		// severity 沿用指标配的基础值
		switch rule.Severity {
		case "critical", "warning", "info":
		default:
			rule.Severity = "warning"
		}
		// 加上 RuleName 的后缀
		rule.Name = cfg.Name + " 告警"
		// 处理已有相同名称的情况
		var count int64
		db.Model(&database.AlertRule{}).Where("name = ?", rule.Name).Count(&count)
		if count > 0 {
			rule.Name = fmt.Sprintf("%s 告警 %d", cfg.Name, cfg.ID)
		}
		encodeRuleRaw(rule)
		if err := db.Create(rule).Error; err != nil {
			continue
		}
		created++
	}
	if created > 0 {
		go func() { _ = Reload() }()
	}
	return created, nil
}

func BatchDeleteRules(db *gorm.DB, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	if err := db.Where("id IN ?", ids).Delete(&database.AlertRule{}).Error; err != nil {
		return err
	}
	// 同步清理关联的活跃实例（保留历史）
	_ = db.Where("rule_id IN ?", ids).Delete(&database.AlertInstance{}).Error
	go func() { _ = Reload() }()
	return nil
}

// BatchEditRuleRequest 批量编辑请求
type BatchEditRuleRequest struct {
	IDs              []uint   `json:"ids"`
	DatasourceIDs    []uint   `json:"datasource_ids,omitempty"`
	DatasourceSel    *string  `json:"datasource_selector,omitempty"`
	NotifyChannelIDs []uint   `json:"notify_channel_ids,omitempty"`
	RouteID          *uint    `json:"route_id,omitempty"`
	Severity         *string  `json:"severity,omitempty"`
	ForDuration      *string  `json:"for_duration,omitempty"`
	KeepFiringFor    *string  `json:"keep_firing_for,omitempty"`
	RepeatInterval   *string  `json:"repeat_interval,omitempty"`
	MaxSendCount     *int     `json:"max_send_count,omitempty"`
	Threshold        *float64 `json:"threshold,omitempty"`
	ThresholdType    *string  `json:"threshold_type,omitempty"`
	Cause            *string  `json:"cause,omitempty"`
	Impact           *string  `json:"impact,omitempty"`
	Enabled          *bool    `json:"enabled,omitempty"`
}

// BatchUpdateRules 批量更新告警规则
func BatchUpdateRules(db *gorm.DB, req *BatchEditRuleRequest) error {
	if len(req.IDs) == 0 {
		return nil
	}
	cols := map[string]interface{}{}
	if req.DatasourceIDs != nil {
		cols["datasource_ids"] = alerting.EncodeUintSlice(req.DatasourceIDs)
	}
	if req.DatasourceSel != nil {
		cols["datasource_selector"] = *req.DatasourceSel
	}
	if req.NotifyChannelIDs != nil {
		cols["notify_channel_ids"] = alerting.EncodeUintSlice(req.NotifyChannelIDs)
	}
	if req.RouteID != nil {
		cols["route_id"] = *req.RouteID
	}
	if req.Severity != nil {
		cols["severity"] = *req.Severity
	}
	if req.ForDuration != nil {
		cols["for_duration"] = *req.ForDuration
	}
	if req.KeepFiringFor != nil {
		cols["keep_firing_for"] = *req.KeepFiringFor
	}
	if req.RepeatInterval != nil {
		cols["repeat_interval"] = *req.RepeatInterval
	}
	if req.MaxSendCount != nil {
		cols["max_send_count"] = *req.MaxSendCount
	}
	if req.Threshold != nil {
		cols["threshold"] = *req.Threshold
	}
	if req.ThresholdType != nil {
		cols["threshold_type"] = *req.ThresholdType
	}
	if req.Cause != nil {
		cols["cause"] = *req.Cause
	}
	if req.Impact != nil {
		cols["impact"] = *req.Impact
	}
	if req.Enabled != nil {
		cols["enabled"] = *req.Enabled
	}
	if len(cols) == 0 {
		return nil
	}
	if err := db.Model(&database.AlertRule{}).Where("id IN ?", req.IDs).Updates(cols).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func DeleteRule(db *gorm.DB, id uint) error {
	if err := db.Delete(&database.AlertRule{}, id).Error; err != nil {
		return err
	}
	// 同步清理该规则的活跃实例（保留历史）
	_ = db.Where("rule_id = ?", id).Delete(&database.AlertInstance{}).Error
	go func() { _ = Reload() }()
	return nil
}

// ===== AlertSilence CRUD ========================================================

func ListSilences(db *gorm.DB, includeExpired bool, page, pageSize int) ([]database.AlertSilence, int64, error) {
	q := db.Model(&database.AlertSilence{}).Order("id desc")
	if !includeExpired {
		now := time.Now().UTC()
		q = q.Where("ends_at >= ?", now)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	var rows []database.AlertSilence
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func GetSilence(db *gorm.DB, id uint) (*database.AlertSilence, error) {
	var s database.AlertSilence
	if err := db.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func CreateSilence(db *gorm.DB, s *database.AlertSilence) error {
	if s.EndsAt.Before(s.StartsAt) {
		return fmt.Errorf("ends_at must be after starts_at")
	}
	s.StartsAt = s.StartsAt.UTC()
	s.EndsAt = s.EndsAt.UTC()
	if err := db.Create(s).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func UpdateSilence(db *gorm.DB, s *database.AlertSilence) error {
	if s.EndsAt.Before(s.StartsAt) {
		return fmt.Errorf("ends_at must be after starts_at")
	}
	s.StartsAt = s.StartsAt.UTC()
	s.EndsAt = s.EndsAt.UTC()
	if err := db.Save(s).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func DeleteSilence(db *gorm.DB, id uint) error {
	if err := db.Delete(&database.AlertSilence{}, id).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

// ===== AlertInhibit CRUD =========================================================

func ListInhibits(db *gorm.DB, page, pageSize int) ([]database.AlertInhibit, int64, error) {
	q := db.Model(&database.AlertInhibit{}).Order("id desc")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	var rows []database.AlertInhibit
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func GetInhibit(db *gorm.DB, id uint) (*database.AlertInhibit, error) {
	var r database.AlertInhibit
	if err := db.First(&r, id).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

func CreateInhibit(db *gorm.DB, r *database.AlertInhibit) error {
	if err := db.Create(r).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func UpdateInhibit(db *gorm.DB, r *database.AlertInhibit) error {
	if err := db.Save(r).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func DeleteInhibit(db *gorm.DB, id uint) error {
	if err := db.Delete(&database.AlertInhibit{}, id).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

// ===== AlertRoute CRUD ===========================================================

func ListRoutes(db *gorm.DB) ([]database.AlertRoute, error) {
	var rows []database.AlertRoute
	if err := db.Order("parent_id asc, priority desc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		decodeRouteRaw(&rows[i])
	}
	return rows, nil
}

func GetRoute(db *gorm.DB, id uint) (*database.AlertRoute, error) {
	var r database.AlertRoute
	if err := db.First(&r, id).Error; err != nil {
		return nil, err
	}
	decodeRouteRaw(&r)
	return &r, nil
}

func CreateRoute(db *gorm.DB, r *database.AlertRoute) error {
	if r.ParentID != nil && *r.ParentID == 0 {
		r.ParentID = nil
	}
	encodeRouteRaw(r)
	if err := db.Create(r).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func UpdateRoute(db *gorm.DB, r *database.AlertRoute) error {
	if r.ParentID != nil && *r.ParentID == 0 {
		r.ParentID = nil
	}
	if r.ParentID != nil && *r.ParentID == r.ID {
		return fmt.Errorf("route parent cannot reference itself")
	}
	encodeRouteRaw(r)
	if err := db.Save(r).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

func DeleteRoute(db *gorm.DB, id uint) error {
	// 拒绝删除有子节点的路由
	var childCount int64
	db.Model(&database.AlertRoute{}).Where("parent_id = ?", id).Count(&childCount)
	if childCount > 0 {
		return fmt.Errorf("route has %d children; delete them first", childCount)
	}
	if err := db.Delete(&database.AlertRoute{}, id).Error; err != nil {
		return err
	}
	go func() { _ = Reload() }()
	return nil
}

// EnsureRootRoute 确保存在一棵根路由（首次启动时使用）
func EnsureRootRoute(db *gorm.DB) error {
	var count int64
	if err := db.Model(&database.AlertRoute{}).Where("parent_id IS NULL").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	root := database.AlertRoute{
		Name:           "默认路由",
		MatchersJSON:   "[]",
		GroupByJSON:    `["alertname","datasource_id"]`,
		GroupWait:      "30s",
		GroupInterval:  "5m",
		RepeatInterval: "4h",
		Enabled:        true,
	}
	return db.Create(&root).Error
}
