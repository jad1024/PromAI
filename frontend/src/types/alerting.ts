// PromAI 告警子系统的 TypeScript 类型定义。
// 与 pkg/database/models.go 中的 AlertRule / AlertInstance / ... 保持字段一致。

export type Severity = 'critical' | 'warning' | 'info'
export type AlertState = 'pending' | 'firing' | 'resolved'
export type MatcherOp = '=' | '!=' | '=~' | '!~'

export interface Matcher {
  name: string
  op: MatcherOp
  value: string
}

export interface DatasourceSelector {
  all?: boolean
  project_name?: string
  name_regex?: string
  url_contains?: string
}

export interface AlertRule {
  id?: number
  name: string
  description?: string
  source_type: 'metric' | 'custom' | 'external'
  metric_config_id?: number | null
  template_id?: number | null
  // 规则创建渠道：manual=手动创建 / template=模板生成 / sync=外部平台同步
  origin?: 'manual' | 'template' | 'sync'
  origin_source_id?: number
  origin_external_id?: string
  expr?: string
  threshold?: number
  threshold_type?: string
  has_threshold?: boolean
  datasource_ids?: number[]
  datasource_selector?: string  // JSON 字符串
  severity: Severity
  for_duration?: string
  keep_firing_for?: string
  repeat_interval?: string
  max_send_count?: number
  eval_interval_sec?: number
  labels_json?: string
  annotations_json?: string
  cause?: string
  impact?: string
  route_id?: number | null
  notify_channel_ids?: number[]
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface AlertInstance {
  id: number
  fingerprint: string
  rule_id: number
  datasource_id: number
  // 展示用（服务端展开）
  rule_name?: string
  datasource_name?: string
  external_source_id?: number
  external_source_name?: string
  // 未读红点计数 / 累计触发次数
  unread_count?: number
  firing_count?: number
  labels: Record<string, string> | null
  annotations: Record<string, string> | null
  state: AlertState
  severity: Severity
  value: number
  threshold: number
  active_at: string
  fired_at?: string | null
  resolved_at?: string | null
  last_eval_at: string
  group_key: string
  silenced_by?: number[] | null
  inhibited_by?: number[] | null
  notified_count: number
  last_notified_at?: string | null
  next_notify_at?: string | null
}

export interface AlertHistoryRow {
  id: number
  fingerprint: string
  rule_id: number
  rule_name: string
  datasource_id: number
  datasource_name: string
  state: string
  severity: Severity
  value: number
  threshold: number
  labels_json: string
  annotations_json: string
  event_type: string
  notify_channels: string
  notify_result: string
  occurred_at: string
  created_at: string
}

// 已恢复告警实例的聚合会话（历史页数据源，同一指纹合并为一条）
export interface HistorySession {
  fingerprint: string
  rule_id: number
  rule_name: string
  datasource_id: number
  datasource_name: string
  severity: Severity
  first_fired_at?: string | null
  resolved_at?: string | null
  firing_count: number
  repeat_count: number
  value: number
  threshold: number
  labels_json: string
  annotations_json: string
  duration_sec: number
}

export interface TimelineEntry {
  event_id?: number
  type: 'firing' | 'resolved' | 'notify'
  severity?: string
  value?: number
  threshold?: number
  labels_json?: string
  annotations_json?: string
  state?: string
  notify_channels?: string
  notify_result?: string
  channel_type?: string
  channel_name?: string
  error?: string
  content?: string
  sent_at?: string
  occurred_at: string
  // UI state
  _expanded?: boolean
}

export interface TimelineGroup {
  rule_id: number
  rule_name: string
  datasource_id: number
  datasource_name: string
  next_notify_at?: string | null
  entries: TimelineEntry[]
  // UI state
  _collapsed?: boolean
}

export interface NotifyChannelInfo {
  id: number
  type: string
  name: string
}

export interface AlertSilence {
  id?: number
  comment: string
  created_by?: string
  matchers_json: string
  starts_at: string
  ends_at: string
  enabled: boolean
  created_at?: string
  updated_at?: string
  matched_count?: number
}

export interface AlertInhibit {
  id?: number
  name: string
  source_matchers_json: string
  target_matchers_json: string
  equal_labels_json: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface AlertRoute {
  id?: number
  parent_id?: number | null
  name: string
  matchers_json?: string
  continue?: boolean
  group_by_json?: string
  group_wait?: string
  group_interval?: string
  repeat_interval?: string
  throttle_window?: string
  notify_channel_ids?: number[]
  priority?: number
  send_resolved?: boolean
  max_send_count?: number
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface AlertGroup {
  id: number
  group_key: string
  route_id: number
  labels_json: string
  alert_count: number
  first_seen_at: string
  last_notified_at?: string | null
  next_notify_at?: string | null
  state: string
}

export interface AlertNotifyLog {
  id: number
  group_key: string
  rule_id: number
  channel_id: number
  channel_type: string
  status: string
  error: string
  payload_hash: string
  alert_count: number
  sent_at: string
}

export interface AlertStats {
  by_severity: Array<{ Severity: string; Count: number }>
  by_state: Array<{ State: string; Count: number }>
  top_rules: Array<{ RuleID: number; Count: number }>
  top_datasources: Array<{ DatasourceID: number; Count: number }>
  trend_24h: Array<{ Hour: string; Count: number }>
  trend_24h_by_source?: Array<{ Hour: string; Source: string; Count: number }>
  unread_count: number
  resolved_count: number
  resolved_total: number
}

export interface EvaluatorStatus {
  running: boolean
  tick_count?: number
  eval_count?: number
  eval_success_count?: number
  eval_fail_count?: number
  open_breakers?: number
  last_tick_at?: string
  worker_pool_size?: number
  queue_depth?: number
}

export interface TestRuleResultSample {
  labels: Record<string, string>
  value: number
  triggered: boolean
}

export interface TestRuleResult {
  datasource_id: number
  datasource_name: string
  success: boolean
  error?: string
  samples: TestRuleResultSample[]
}

// ===== 外部告警源（n9e / 华为云 CES / 通用 webhook） =====
export type ExternalSourceType = 'n9e' | 'huaweicloud' | 'aliyun' | 'generic'

export interface ExternalAlertSource {
  id?: number
  name: string
  type: ExternalSourceType
  enabled: boolean
  url?: string
  // 华为云 CES
  region?: string
  project_id?: string
  access_key?: string
  secret_key?: string // 列表/详情接口返回时脱敏为空
  // n9e
  username?: string
  password?: string // 列表/详情接口返回时脱敏为空
  n9e_token?: string // n9e v8+ 个人中心 X-User-Token（编辑时回填真实值，可修改/清空），优先于账号密码
  // webhook 接收鉴权（返回时脱敏为空）
  token?: string // webhook 鉴权 token（编辑时回填真实值，可修改/清空，不再脱敏）
  sync_interval?: string // 30m / 1h / 1d
  last_sync_at?: string | null
  sync_status?: string // success / failed / pending / ''
  sync_error?: string
  notify_enabled?: boolean
  ai_analysis_enabled?: boolean
  created_at?: string
  updated_at?: string
}

export interface ExternalRule {
  id: number
  source_id: number
  source_type: string
  external_id: string
  rule_name: string
  severity: Severity | string
  status: string // enabled / disabled
  condition?: string
  raw_json?: string
  last_seen_at: string
  created_at: string
  updated_at: string
}

export interface ExternalSyncResult {
  source: string
  created: number
  updated: number
  total: number
  status: string
}

// ===== 告警降噪聚合（分析级） =====
// 参考 FlashDuty/Nightingale 模型：AlertHistory → Alert（按 fingerprint 去重） → Incident（按 alertname+resource 聚合）
// 说明：通知层面的分组/去重/抑制由 Alertmanager 负责，PromAI 不做。
// 这里仅做"分析级聚合"，用于人看懂告警、噪音治理与 AI 上下文降噪。

export interface AlertInIncident {
  time: string
  state: string // ongoing | resolved
  severity: string
  value: number
  threshold: number
  datasource_id: number
  datasource_name: string
  labels: Record<string, string>
  duration: string
}

export interface AlertIncident {
  key: string
  alertname: string
  resource: string
  severity: string
  state: string // ongoing | resolved
  alert_count: number
  first_fired_at: string
  last_event_at: string
  storm: boolean
  datasources: string[]
  alerts?: AlertInIncident[]
}

export interface AlertIncidentList {
  incidents: AlertIncident[]
  total_raw: number
  total_alerts: number
  total_incidents: number
  compression: number
  window_minutes: number
  storm_threshold: number
  resource_labels: string[]
}

export interface NoiseTopItem {
  alertname: string
  resource: string
  alert_count: number
  severity: string
  state: string
  storm: boolean
  datasources: string[]
}

export interface AlertNoiseTop {
  items: NoiseTopItem[]
  window_hours: number
  window_minutes: number
  storm_threshold: number
  resource_labels: string[]
}

export interface DenoiseConfig {
  window_minutes: number
  storm_threshold: number
  resource_labels: string[]
}
