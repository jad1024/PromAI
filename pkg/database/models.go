package database

import (
	"time"

	"gorm.io/gorm"
)

type DataSource struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	Name                string     `gorm:"uniqueIndex;size:100;not null" json:"name"`
	URL                 string     `gorm:"size:500;not null" json:"url"`
	Username            string     `gorm:"size:100" json:"username"`
	Password            string     `gorm:"size:100" json:"password"`
	IsDefault           bool       `gorm:"default:false" json:"is_default"`
	Enabled             bool       `gorm:"default:true" json:"enabled"`
	TemplateID          *uint      `json:"template_id"`
	TemplateIDsRaw      string     `gorm:"column:template_ids;type:text" json:"-"`
	TemplateIDs         []uint     `gorm:"-" json:"template_ids,omitempty"`
	ProjectName         string     `gorm:"size:200" json:"project_name"`
	ConnectionStatus    string     `gorm:"size:50" json:"connection_status"`
	ConnectionCheckedAt *time.Time `json:"connection_checked_at,omitempty"`
	NotifyChannels      string     `gorm:"type:text" json:"notify_channels"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	HealthStatus        string     `gorm:"-" json:"health_status"`
	ReportStatus        string     `gorm:"-" json:"report_status,omitempty"`
	LastReportAt        *time.Time `gorm:"-" json:"last_report_at,omitempty"`
}

type MetricType struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	TypeName  string         `gorm:"uniqueIndex;size:200;not null" json:"type_name"`
	SortOrder int            `gorm:"default:0" json:"sort_order"`
	Configs   []MetricConfig `gorm:"foreignKey:MetricTypeID" json:"configs"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type MetricConfig struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	MetricTypeID    uint      `gorm:"not null;index" json:"metric_type_id"`
	DatasourceID    *uint     `gorm:"index" json:"datasource_id"`
	Name            string    `gorm:"size:200;not null" json:"name"`
	Description     string    `gorm:"size:500" json:"description"`
	Query           string    `gorm:"type:text;not null" json:"query"`
	Threshold       float64   `json:"threshold"`
	ThresholdType   string    `gorm:"size:50;default:greater" json:"threshold_type"`
	ThresholdStatus string    `gorm:"size:50;default:critical" json:"threshold_status"`
	Unit            string    `gorm:"size:50" json:"unit"`
	LabelsJSON      string    `gorm:"type:text" json:"labels_json"`
	SortOrder       int       `gorm:"default:0" json:"sort_order"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// 动态基线异常检测
	BaselineEnabled    bool    `gorm:"default:false" json:"baseline_enabled"`
	BaselineWindow     string  `gorm:"size:20" json:"baseline_window"`         // 历史窗口，如 7d
	BaselineZScore     float64 `gorm:"default:3" json:"baseline_zscore"`       // z-score 阈值
	BaselineMinSamples int     `gorm:"default:10" json:"baseline_min_samples"` // 最少样本数

	// 接近阈值预警告（可选，默认关闭）
	WarningEnabled bool    `gorm:"default:false" json:"warning_enabled"` // 是否开启接近阈值预警（阈值未触发但逼近时标为警告）
	WarningMargin  float64 `gorm:"default:5" json:"warning_margin"`      // 预警带宽度：占阈值的百分比，如 5 = 5%
}

type NotificationChannel struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ChannelType string    `gorm:"size:50;not null;index" json:"channel_type"`
	Name        string    `gorm:"size:200;not null" json:"name"`
	Enabled     bool      `gorm:"default:false" json:"enabled"`
	ConfigJSON  string    `gorm:"type:text" json:"config_json"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CronJob struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	Name           string `gorm:"size:200;not null" json:"name"`
	Schedule       string `gorm:"size:100;not null" json:"schedule"`
	DatasourceID   *uint  `json:"datasource_id"`                   // 旧版单数据源（向后兼容）
	DatasourceIDs  string `gorm:"type:text" json:"datasource_ids"` // 多数据源 JSON 数组
	AllDatasources bool   `json:"all_datasources"`                 // 全部数据源
	Enabled        bool   `gorm:"default:true" json:"enabled"`
	NotifyChannels string `gorm:"type:text" json:"notify_channels"`
	// AI 巡检分析：巡检完成后调用 AI 对结果做健康分析，并推送到飞书通道
	AiAnalysisEnabled bool       `json:"ai_analysis_enabled"`                 // 是否启用 AI 巡检分析
	AiAnalysisPrompt  string     `gorm:"type:text" json:"ai_analysis_prompt"` // 自定义 AI 分析提示词（可选，空则使用内置模板）
	LastRunAt         *time.Time `json:"last_run_at"`
	LastStatus        string     `gorm:"size:50" json:"last_status"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type ReportRecord struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	Title          string    `gorm:"size:200" json:"title"`
	DatasourceID   *uint     `json:"datasource_id"`
	DatasourceName string    `gorm:"size:200" json:"datasource_name"`
	FilePath       string    `gorm:"size:500" json:"file_path"`
	FileSize       int64     `json:"file_size"`
	TotalMetrics   int       `json:"total_metrics"`
	AlertCount     int       `json:"alert_count"`
	CriticalCount  int       `json:"critical_count"`
	WarningCount   int       `json:"warning_count"`
	Status         string    `gorm:"size:50" json:"status"`
	Duration       string    `gorm:"size:50" json:"duration"`
	MetricsJSON    string    `gorm:"type:text" json:"metrics_json"`
	CreatedAt      time.Time `json:"created_at"`
}

type AppSetting struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"uniqueIndex;size:100;not null" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type InspectionTemplate struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:200;not null" json:"name"`
	Description string    `gorm:"size:500" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type InspectionTemplateMetric struct {
	TemplateID     uint `gorm:"primaryKey;autoIncrement:false" json:"template_id"`
	MetricConfigID uint `gorm:"primaryKey;autoIncrement:false" json:"metric_config_id"`
}

type TemplateMetricOverride struct {
	ID              uint    `gorm:"primaryKey" json:"id"`
	TemplateID      uint    `gorm:"not null;index;uniqueIndex:idx_tmpl_override" json:"template_id"`
	MetricConfigID  uint    `gorm:"not null;index;uniqueIndex:idx_tmpl_override" json:"metric_config_id"`
	Query           string  `gorm:"type:text" json:"query"`
	Threshold       float64 `json:"threshold"`
	ThresholdType   string  `gorm:"size:50;default:greater" json:"threshold_type"`
	ThresholdStatus string  `gorm:"size:50;default:critical" json:"threshold_status"`
	Unit            string  `gorm:"size:50" json:"unit"`
	LabelsJSON      string  `gorm:"type:text" json:"labels_json"`
}

// InspectRecord 巡检任务记录（持久化）
type InspectRecord struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	TaskID         string     `gorm:"size:100;index" json:"task_id"`
	Status         string     `gorm:"size:50;default:running" json:"status"`
	DatasourceID   *uint      `json:"datasource_id"`
	DatasourceName string     `gorm:"size:200" json:"datasource_name"`
	Message        string     `gorm:"size:500" json:"message"`
	Error          string     `gorm:"size:500" json:"error"`
	ReportURL      string     `gorm:"size:500" json:"report_url"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Apply applies a template-level override onto a MetricConfig
func (o *TemplateMetricOverride) Apply(cfg *MetricConfig) {
	if o.ID == 0 {
		return
	}
	cfg.Query = o.Query
	cfg.Threshold = o.Threshold
	cfg.ThresholdType = o.ThresholdType
	cfg.ThresholdStatus = o.ThresholdStatus
	cfg.Unit = o.Unit
	cfg.LabelsJSON = o.LabelsJSON
}

type SyncSource struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"size:200;not null" json:"name"`
	URL           string    `gorm:"size:500;not null" json:"url"`
	Method        string    `gorm:"size:10;default:GET" json:"method"`
	Headers       string    `gorm:"type:text" json:"headers"`              // JSON object
	Body          string    `gorm:"type:text" json:"body"`                 // request body template
	AuthType      string    `gorm:"size:20;default:none" json:"auth_type"` // none, basic, bearer
	AuthUsername  string    `gorm:"size:100" json:"auth_username"`
	AuthPassword  string    `gorm:"size:100" json:"auth_password"`
	AuthToken     string    `gorm:"size:500" json:"auth_token"`
	DataPath      string    `gorm:"size:200" json:"data_path"` // JSON path to data array (e.g. "data.items")
	NameField     string    `gorm:"size:100;not null" json:"name_field"`
	URLField      string    `gorm:"size:100" json:"url_field"`
	URLTemplate   string    `gorm:"size:500" json:"url_template"` // e.g. http://{host}:{port}
	UsernameField string    `gorm:"size:100" json:"username_field"`
	PasswordField string    `gorm:"size:100" json:"password_field"`
	CronExpr      string    `gorm:"size:50" json:"cron_expr"`
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type SyncLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	SyncSourceID uint      `json:"sync_source_id"`
	Status       string    `gorm:"size:20" json:"status"` // success, partial, failed
	Message      string    `gorm:"type:text" json:"message"`
	TotalItems   int       `json:"total_items"`
	CreatedItems int       `json:"created_items"`
	UpdatedItems int       `json:"updated_items"`
	ErrorItems   int       `json:"error_items"`
	CreatedAt    time.Time `json:"created_at"`
}

type AiSession struct {
	ID        string      `gorm:"primaryKey;size:100" json:"id"`
	ModelName string      `gorm:"size:100" json:"model_name"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Messages  []AiMessage `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE" json:"messages,omitempty"`
}

type AiMessage struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID string    `gorm:"index;size:100;not null" json:"session_id"`
	Role      string    `gorm:"size:20;not null" json:"role"`
	Content   string    `gorm:"type:text" json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// SkillUsage 记录每次 AI 会话中 Skill 的曝光（被注入系统提示词）
// 用于统计 Skill 使用趋势
type SkillUsage struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SkillName string    `gorm:"index:idx_skill_usage_name_day;size:100;not null" json:"skill_name"`
	SessionID string    `gorm:"index;size:100;not null" json:"session_id"`
	Day       string    `gorm:"index:idx_skill_usage_name_day;size:10;not null" json:"day"` // YYYY-MM-DD
	CreatedAt time.Time `json:"created_at"`
}

func (SkillUsage) TableName() string { return "skill_usage" }

// ===== Alerting 子系统模型 =====================================================
//
// AlertRule       告警规则定义（可复用 MetricConfig，也可自定义 PromQL）
// AlertInstance   实时告警实例（按 fingerprint 去重）
// AlertHistory    告警历史归档（状态变迁追加，便于回溯/统计）
// AlertSilence    静默规则（按 label matcher 静默匹配的活跃告警）
// AlertInhibit    抑制规则（高优先级告警抑制低优先级告警）
// AlertRoute      通知路由树（扁平表，parent_id 0 表示根）
// AlertGroup      运行时分组聚合状态（dispatcher 调度通知的最小单位）
// AlertNotifyLog  通知发送日志（成功/失败/降级）
// AlertMatcher    通用 matcher（=, !=, =~, !~）以 JSON 嵌入到字符串字段
//
// 字段约定：所有 JSON 字段统一存为 string（type:text），由上层 Marshal/Unmarshal。
// =================================================================================

// AlertRule 告警规则
//   - SourceType=metric  时使用 MetricConfigID 引用现有指标，无需重复维护 PromQL
//   - SourceType=custom  时使用 Expr/Threshold/ThresholdType 字段，自定义 PromQL
//   - 数据源选择：DatasourceIDs（显式列表）和 DatasourceSelector（按 tag/project 批量）二选一
type AlertRule struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:200;not null;index" json:"name"`
	Description string `gorm:"size:500" json:"description"`

	// 规则来源
	SourceType     string `gorm:"size:20;default:metric;index" json:"source_type"` // metric / custom / external
	MetricConfigID *uint  `gorm:"index" json:"metric_config_id,omitempty"`

	// 规则创建渠道（区分来源）：manual=手动创建 / template=模板生成 / sync=外部平台同步
	Origin           string `gorm:"size:20;default:manual;index" json:"origin"`
	OriginSourceID   uint   `gorm:"index" json:"origin_source_id,omitempty"`            // 来源外部告警源 ID（origin=sync 时）
	OriginExternalID string `gorm:"size:200;index" json:"origin_external_id,omitempty"` // 外部平台规则 ID（origin=sync 时）

	// 关联巡检模版（可选）
	TemplateID *uint `gorm:"index" json:"template_id,omitempty"`

	// 自定义 PromQL（SourceType=custom 时使用，metric 时可作为覆盖）
	Expr          string  `gorm:"type:text" json:"expr"`
	Threshold     float64 `json:"threshold"`
	ThresholdType string  `gorm:"size:20;default:greater" json:"threshold_type"` // gt/ge/lt/le/eq/ne (复用 MetricConfig 的语义)
	HasThreshold  bool    `json:"has_threshold"`                                 // 是否覆盖指标自带阈值

	// 数据源选择
	DatasourceIDsRaw       string `gorm:"column:datasource_ids;type:text" json:"-"`
	DatasourceIDs          []uint `gorm:"-" json:"datasource_ids,omitempty"`
	DatasourceSelectorJSON string `gorm:"column:datasource_selector;type:text" json:"datasource_selector,omitempty"` // {"tag":"prod","project":"core","all":false}

	// 触发判定
	Severity        string `gorm:"size:20;default:warning;index" json:"severity"` // critical / warning / info
	ForDuration     string `gorm:"size:20" json:"for_duration"`                   // e.g. 5m
	KeepFiringFor   string `gorm:"size:20" json:"keep_firing_for"`                // e.g. 5m
	EvalIntervalSec int    `gorm:"default:0" json:"eval_interval_sec"`            // 0=使用全局间隔

	// 通知频率（优先级高于路由配置）
	RepeatInterval string `gorm:"size:20" json:"repeat_interval"`  // 重复通知间隔, e.g. 4h；空=继承路由
	MaxSendCount   int    `gorm:"default:0" json:"max_send_count"` // 最大发送次数；0=继承路由

	// 元数据
	LabelsJSON      string `gorm:"type:text" json:"labels_json"`      // {"team":"infra"}
	AnnotationsJSON string `gorm:"type:text" json:"annotations_json"` // {"summary":"...","description":"...","runbook_url":"..."}

	// 告警原因 & 影响范围（用户在创建规则时填写，发通知时附上）
	Cause  string `gorm:"type:text" json:"cause"`
	Impact string `gorm:"type:text" json:"impact"`

	// AI 根因分析（规则级开关，默认开启；受全局 app_settings.ai_alert_analysis_enabled 约束）
	AiAnalysisEnabled bool `gorm:"default:true" json:"ai_analysis_enabled"`

	// 通知路径（二选一；route_id 优先）
	RouteID             *uint  `gorm:"index" json:"route_id,omitempty"`
	NotifyChannelIDsRaw string `gorm:"column:notify_channel_ids;type:text" json:"-"`
	NotifyChannelIDs    []uint `gorm:"-" json:"notify_channel_ids,omitempty"`

	Enabled   bool      `gorm:"default:true;index" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AlertInstance 当前活跃 / 待恢复的告警实例（热表）
type AlertInstance struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Fingerprint string `gorm:"uniqueIndex;size:64;not null" json:"fingerprint"`

	RuleID       uint `gorm:"index;index:idx_ai_rule_state,priority:1" json:"rule_id"`
	DatasourceID uint `gorm:"index;index:idx_ai_ds_state,priority:1" json:"datasource_id"`

	// 外部告警源关联（外部平台告警时填充，用于展示 n9e/华为云 源名）
	ExternalSourceID uint `gorm:"index" json:"external_source_id,omitempty"`

	// 未读标记：收到新告警 +1，用户查看详情后清零（前端红点）
	UnreadCount int `gorm:"default:0" json:"unread_count"`
	// 累计触发次数（每次收到 firing 事件 +1，用于外部告警展示"触发次数"）
	FiringCount int `gorm:"default:0" json:"firing_count"`

	LabelsJSON      string `gorm:"type:text" json:"labels_json"`
	AnnotationsJSON string `gorm:"type:text" json:"annotations_json"`

	State    string `gorm:"size:20;index:idx_ai_rule_state,priority:2;index:idx_ai_ds_state,priority:2;index" json:"state"` // pending / firing / resolved
	Severity string `gorm:"size:20;index" json:"severity"`

	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`

	ActiveAt   time.Time  `json:"active_at"`
	FiredAt    *time.Time `json:"fired_at,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	LastEvalAt time.Time  `gorm:"index" json:"last_eval_at"`

	GroupKey        string `gorm:"size:64;index" json:"group_key"`
	SilencedByJSON  string `gorm:"type:text" json:"silenced_by_json"`
	InhibitedByJSON string `gorm:"type:text" json:"inhibited_by_json"`

	NotifiedCount  int        `json:"notified_count"`
	LastNotifiedAt *time.Time `json:"last_notified_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AlertHistory 告警状态变迁追加表，单条事件不可变
type AlertHistory struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Fingerprint     string    `gorm:"index;size:64" json:"fingerprint"`
	RuleID          uint      `gorm:"index" json:"rule_id"`
	RuleName        string    `gorm:"size:200" json:"rule_name"`
	DatasourceID    uint      `gorm:"index" json:"datasource_id"`
	DatasourceName  string    `gorm:"size:200" json:"datasource_name"`
	State           string    `gorm:"size:20;index" json:"state"`
	Severity        string    `gorm:"size:20;index" json:"severity"`
	Value           float64   `json:"value"`
	Threshold       float64   `json:"threshold"`
	LabelsJSON      string    `gorm:"type:text" json:"labels_json"`
	AnnotationsJSON string    `gorm:"type:text" json:"annotations_json"`
	EventType       string    `gorm:"size:20;index" json:"event_type"`  // pending/firing/resolved/silenced/inhibited/notified
	NotifyChannels  string    `gorm:"type:text" json:"notify_channels"` // 通知渠道 JSON: [{"id":1,"type":"wechat_work","name":"企业微信"}]
	NotifyResult    string    `gorm:"size:20" json:"notify_result"`     // 通知结果: success/failed/throttled
	OccurredAt      time.Time `gorm:"index" json:"occurred_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// AlertSilence 静默规则
type AlertSilence struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Comment      string    `gorm:"size:500;not null" json:"comment"`
	CreatedBy    string    `gorm:"size:100" json:"created_by"`
	MatchersJSON string    `gorm:"type:text;not null" json:"matchers_json"`
	StartsAt     time.Time `gorm:"index" json:"starts_at"`
	EndsAt       time.Time `gorm:"index" json:"ends_at"`
	Enabled      bool      `gorm:"default:true;index" json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AlertInhibit 抑制规则
type AlertInhibit struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Name               string    `gorm:"size:200;not null" json:"name"`
	SourceMatchersJSON string    `gorm:"type:text;not null" json:"source_matchers_json"`
	TargetMatchersJSON string    `gorm:"type:text;not null" json:"target_matchers_json"`
	EqualLabelsJSON    string    `gorm:"type:text" json:"equal_labels_json"` // ["cluster","instance"]
	Enabled            bool      `gorm:"default:true;index" json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// AlertRoute 通知路由（扁平表模拟树）
type AlertRoute struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	ParentID            *uint     `gorm:"index" json:"parent_id,omitempty"`
	Name                string    `gorm:"size:200;not null" json:"name"`
	MatchersJSON        string    `gorm:"type:text" json:"matchers_json"`
	Continue            bool      `gorm:"column:cont" json:"continue"`
	GroupByJSON         string    `gorm:"type:text" json:"group_by_json"`
	GroupWait           string    `gorm:"size:20;default:30s" json:"group_wait"`
	GroupInterval       string    `gorm:"size:20;default:5m" json:"group_interval"`
	RepeatInterval      string    `gorm:"size:20;default:4h" json:"repeat_interval"`
	ThrottleWindow      string    `gorm:"size:20;default:''" json:"throttle_window"`
	NotifyChannelIDsRaw string    `gorm:"column:notify_channel_ids;type:text" json:"-"`
	NotifyChannelIDs    []uint    `gorm:"-" json:"notify_channel_ids,omitempty"`
	Priority            int       `gorm:"default:0;index" json:"priority"`
	SendResolved        bool      `gorm:"default:false" json:"send_resolved"`
	MaxSendCount        int       `gorm:"default:0" json:"max_send_count"`
	Enabled             bool      `gorm:"default:true" json:"enabled"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// AlertGroup 运行时分组聚合状态
type AlertGroup struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	GroupKey             string     `gorm:"uniqueIndex;size:64;not null" json:"group_key"`
	RuleID               uint       `gorm:"index" json:"rule_id"`
	DatasourceID         uint       `gorm:"index" json:"datasource_id"`
	RouteID              uint       `gorm:"index" json:"route_id"`
	LabelsJSON           string     `gorm:"type:text" json:"labels_json"`
	AlertCount           int        `json:"alert_count"`
	FirstSeenAt          time.Time  `json:"first_seen_at"`
	LastNotifiedAt       *time.Time `json:"last_notified_at,omitempty"`
	NextNotifyAt         *time.Time `gorm:"index" json:"next_notify_at,omitempty"`
	ResolvedNextNotifyAt *time.Time `json:"resolved_next_notify_at,omitempty"` // 恢复通知批次调度
	SendCount            int        `gorm:"default:0" json:"send_count"`
	State                string     `gorm:"size:20;index" json:"state"` // idle/pending/notified
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// AlertNotifyLog 通知发送日志
type AlertNotifyLog struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	GroupKey    string    `gorm:"size:64;index" json:"group_key"`
	RuleID      uint      `gorm:"index" json:"rule_id"`
	ChannelID   uint      `gorm:"index" json:"channel_id"`
	ChannelType string    `gorm:"size:50" json:"channel_type"`
	Status      string    `gorm:"size:20;index" json:"status"` // success / failed / throttled
	Error       string    `gorm:"size:500" json:"error"`
	PayloadHash string    `gorm:"size:64;index" json:"payload_hash"`
	AlertCount  int       `json:"alert_count"`
	Content     string    `gorm:"type:text" json:"content"` // 实际发送的内容（markdown）
	SentAt      time.Time `gorm:"index" json:"sent_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// AiAnalysisRecord AI 分析记录（巡检健康分析 / 告警根因分析）
type AiAnalysisRecord struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Type       string    `gorm:"size:20;index" json:"type"`    // inspection / alert / alert_external
	RefID      string    `gorm:"size:100;index" json:"ref_id"` // 巡检 task_id 或告警 group_key
	RuleID     uint      `gorm:"index" json:"rule_id"`
	ModelName  string    `gorm:"size:100" json:"model_name"`
	Prompt     string    `gorm:"type:text" json:"prompt"`
	Result     string    `gorm:"type:text" json:"result"`
	Status     string    `gorm:"size:20" json:"status"` // success / failed
	Error      string    `gorm:"size:500" json:"error"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// ExternalAlertSource 外部告警源（n9e / 华为云 CES / 阿里云 CloudMonitor / 通用 webhook）
// 用于将外部平台维护的告警汇聚进 PromAI：规则只读同步 + webhook 事件接收
type ExternalAlertSource struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	Name    string `gorm:"size:100;not null" json:"name"`
	Type    string `gorm:"size:20;not null;index" json:"type"` // n9e / huaweicloud / aliyun / generic
	Enabled bool   `gorm:"default:true" json:"enabled"`
	URL     string `gorm:"size:500" json:"url"` // n9e 服务地址 / 华为云 endpoint（可留空用默认）

	// 华为云 CES 凭据（列表接口返回时脱敏）
	Region    string `gorm:"size:50" json:"region"`      // 如 cn-north-4
	ProjectID string `gorm:"size:100" json:"project_id"` // 华为云项目 ID
	AccessKey string `gorm:"size:200" json:"access_key"` // 华为云 AK
	SecretKey string `gorm:"size:200" json:"secret_key"` // 华为云 SK

	// n9e 登录凭据（列表接口返回时脱敏）
	Username string `gorm:"size:100" json:"username"`
	Password string `gorm:"size:200" json:"password"`
	// n9e v8.0.0-beta.5+ 官方推荐的 X-User-Token（个人中心创建），优先于账号密码登录
	N9eToken string `gorm:"size:300" json:"n9e_token"`

	// webhook 接收鉴权 token：外部平台推送时须带 Authorization: Bearer <token>
	Token string `gorm:"size:200" json:"token"`

	SyncInterval string     `gorm:"size:20;default:'1h'" json:"sync_interval"` // 规则同步周期，如 30m / 1h / 1d
	LastSyncAt   *time.Time `json:"last_sync_at,omitempty"`
	SyncStatus   string     `gorm:"size:20" json:"sync_status"` // success / failed / pending
	SyncError    string     `gorm:"size:500" json:"sync_error"`

	// 外部告警处理开关
	NotifyEnabled     bool `gorm:"default:false" json:"notify_enabled"`      // 外部告警是否按路由转发通知
	AIAnalysisEnabled bool `gorm:"default:false" json:"ai_analysis_enabled"` // 外部告警是否做 AI 根因分析

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ExternalRule 从外部平台同步的告警规则（只读展示，不参与本地评估）
type ExternalRule struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	SourceID   uint      `gorm:"index;not null;uniqueIndex:idx_ext_rule_source,priority:1" json:"source_id"`
	SourceType string    `gorm:"size:20;index" json:"source_type"`                                       // n9e / huaweicloud
	ExternalID string    `gorm:"size:200;uniqueIndex:idx_ext_rule_source,priority:2" json:"external_id"` // 平台规则 ID
	RuleName   string    `gorm:"size:300" json:"rule_name"`
	Severity   string    `gorm:"size:20" json:"severity"`
	Status     string    `gorm:"size:20" json:"status"`      // enabled / disabled
	Condition  string    `gorm:"type:text" json:"condition"` // 条件/表达式摘要
	RawJSON    string    `gorm:"type:text" json:"raw_json"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&DataSource{},
		&MetricType{},
		&MetricConfig{},
		&NotificationChannel{},
		&CronJob{},
		&ReportRecord{},
		&AppSetting{},
		&InspectionTemplate{},
		&InspectionTemplateMetric{},
		&TemplateMetricOverride{},
		&InspectRecord{},
		&SyncSource{},
		&SyncLog{},
		&AiSession{},
		&AiMessage{},
		&SkillUsage{},
		// alerting
		&AlertRule{},
		&AlertInstance{},
		&AlertHistory{},
		&AlertSilence{},
		&AlertInhibit{},
		&AlertRoute{},
		&AlertGroup{},
		&AlertNotifyLog{},
		&AiAnalysisRecord{},
		&ExternalAlertSource{},
		&ExternalRule{},
	)
}
