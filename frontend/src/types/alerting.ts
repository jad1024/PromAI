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
  source_type: 'metric' | 'custom'
  metric_config_id?: number | null
  template_id?: number | null
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
