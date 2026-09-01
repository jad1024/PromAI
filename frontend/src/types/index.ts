export interface DataSource {
  id?: number
  name: string
  url: string
  username?: string
  password?: string
  is_default?: boolean
  enabled?: boolean
  template_id?: number | null
  template_ids?: number[]
  project_name?: string
  notify_channels?: string
  created_at?: string
  updated_at?: string
  health_status?: string
  connection_status?: string
  connection_checked_at?: string | null
  report_status?: string
  last_report_at?: string | null
}

export interface MetricConfig {
  id?: number
  metric_type_id: number
  datasource_id?: number | null
  name: string
  description?: string
  query: string
  threshold?: number
  threshold_type?: string
  threshold_status?: string
  unit?: string
  labels_json?: string
  // 动态基线异常检测（z-score / 3σ）
  baseline_enabled?: boolean
  baseline_window?: string
  baseline_zscore?: number
  baseline_min_samples?: number
  // 接近阈值预警告（可选，默认关闭）
  warning_enabled?: boolean
  warning_margin?: number
  sort_order?: number
  created_at?: string
  updated_at?: string
}

export interface MetricType {
  id?: number
  type_name: string
  description?: string
  color?: string
  sort_order?: number
  configs?: MetricConfig[]
  created_at?: string
  updated_at?: string
}

export interface NotificationChannel {
  id?: number
  channel_type: string
  name: string
  enabled?: boolean
  config_json?: string
  created_at?: string
  updated_at?: string
}

export interface CronJob {
  id?: number
  name: string
  schedule: string
  datasource_id?: number | null
  datasource_ids?: string
  all_datasources?: boolean
  enabled?: boolean
  notify_channels?: string
  // 巡检指标范围（按巡检模版/指标分组/具体指标指定；为空表示全部有效指标）
  template_ids?: string
  metric_type_ids?: string
  metric_config_ids?: string
  // 大集群分批巡检：每批并发数据源数，0 表示不限
  batch_size?: number
  // AI 巡检分析：巡检完成后 AI 分析结果推送到飞书
  ai_analysis_enabled?: boolean
  ai_analysis_prompt?: string
  // 仅当巡检存在异常时才调用 AI 分析，正常时跳过以节省 token
  ai_only_abnormal?: boolean
  last_run_at?: string | null
  last_status?: string
  created_at?: string
  updated_at?: string
}

export interface ReportRecord {
  id?: number
  title: string
  datasource_id?: number | null
  datasource_name: string
  file_path: string
  file_size: number
  total_metrics: number
  alert_count: number
  critical_count: number
  warning_count: number
  status: string
  duration?: string
  created_at?: string
}

export interface DashboardStats {
  total_datasources: number
  total_cronjobs: number
  total_reports: number
  total_notifications: number
  recent_reports: ReportRecord[]
}

export interface InspectRecord {
  id: number
  task_id: string
  status: string
  datasource_name: string
  message: string
  error: string
  report_url: string
  started_at: string
  completed_at: string | null
}

export interface SyncSource {
  id?: number
  name: string
  url: string
  method?: string
  headers?: string
  body?: string
  auth_type?: string
  auth_username?: string
  auth_password?: string
  auth_token?: string
  data_path?: string
  name_field: string
  url_field?: string
  url_template?: string
  username_field?: string
  password_field?: string
  cron_expr?: string
  enabled?: boolean
  created_at?: string
  updated_at?: string
}

export interface SyncLog {
  id: number
  sync_source_id: number
  status: string
  message: string
  total_items: number
  created_items: number
  updated_items: number
  error_items: number
  created_at: string
}

// AiSkill 对应 OpenClaw SKILL.md 规范
// 参考 https://docs.openclaw.ai/tools/skills
export interface AiSkill {
  name: string               // skill 标识符
  description: string         // 一行描述
  instruction: string         // SKILL.md markdown 正文
  metadata?: string           // JSON: openclaw gating 条件
  user_invocable?: boolean    // 是否可作为 slash 命令
  enabled?: boolean
  source?: string             // workspace | plugin | bundled
}

export interface InspectRequest {
  datasource_id?: number
  datasource_url?: string
  wechat_bot_key?: string
  touser?: string
  template_ids?: number[]
  metric_config_ids?: number[]
  metric_type_ids?: number[]
}

// ===== 敏感端口扫描 =====
export interface PortInfo {
  port: number
  name: string
  risk: 'high' | 'medium' | 'low'
}

export interface PortScanTask {
  id: number
  task_id: string
  targets: string[]
  ports: number[]
  status: 'running' | 'completed' | 'failed'
  total_targets: number
  total_ports: number
  open_ports: number
  message: string
  error: string
  started_at: string
  completed_at: string | null
  created_at: string
}

export interface PortScanResult {
  id: number
  task_id: string
  ip: string
  port: number
  port_name: string
  state: 'open' | 'closed' | 'timeout' | 'refused'
  risk: 'high' | 'medium' | 'low'
  latency_ms: number
  created_at: string
}
