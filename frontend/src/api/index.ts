import axios from 'axios'
import router from '../router'
import type {
  DataSource, MetricType, MetricConfig,
  NotificationChannel, CronJob, ReportRecord,
  InspectRecord, InspectRequest, DashboardStats,
  SyncSource, SyncLog, AiSkill,
  PortScanTask, PortScanResult, PortInfo
} from '../types'

const api = axios.create({
  baseURL: '/api/promai',
  timeout: 30000,
})

api.interceptors.request.use(config => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

api.interceptors.response.use(
  r => r,
  e => {
    if (e.response?.status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('username')
      router.push('/login')
    }
    const msg = e.response?.data?.error || e.message || '请求失败'
    return Promise.reject(new Error(msg))
  }
)

// Auth
export const login = (username: string, password: string) => api.post('/auth/login', { username, password })
export const getMe = () => api.get('/auth/me')

// Reports（报告静态文件已要求 JWT 鉴权，需带 Authorization 头以 blob 方式获取）
export async function openReportFile(url: string) {
  const res = await api.get(url, { responseType: 'blob' })
  const blobUrl = URL.createObjectURL(res.data)
  window.open(blobUrl, '_blank')
}

// Data Sources
export const getDataSources = (params?: { page?: number; page_size?: number; keyword?: string; enabled?: string }) =>
  api.get<{ items: DataSource[]; total: number; page: number; page_size: number }>('/datasources', { params })

export const getAllDataSources = () =>
  api.get<DataSource[]>('/datasources/all')
export const getDataSource = (id: number) => api.get<DataSource>(`/datasources/${id}`)
export const createDataSource = (d: DataSource) => api.post<DataSource>('/datasources', d)
export const updateDataSource = (id: number, d: Partial<DataSource>) => api.put<DataSource>(`/datasources/${id}`, d)
export const deleteDataSource = (id: number) => api.delete(`/datasources/${id}`)
export const batchDeleteDataSources = (ids: number[]) => api.patch('/datasources', { ids, action: 'delete' })
export const batchToggleDataSources = (ids: number[], enabled: boolean) => api.patch('/datasources', { ids, action: 'toggle', enabled })
export const batchSetTemplate = (ids: number[], templateIds: number[]) => api.patch('/datasources', {
  ids,
  action: 'set-template',
  template_ids: templateIds,
  template_id: templateIds[0] ?? null,
})
export const batchSetNotify = (ids: number[], notifyChannels: string) => api.patch('/datasources', { ids, action: 'set-notify', notify_channels: notifyChannels })
export const batchApplyTemplate = (ids: number[]) => api.patch('/datasources', { ids, action: 'apply-template' })
export const batchInspect = (ids: number[]) => api.patch('/datasources', { ids, action: 'inspect' })
export const batchSetCreds = (ids: number[], username: string, password: string) => api.patch('/datasources', { ids, action: 'set-creds', username, password })
export const importDatasources = (yaml: string) => api.post('/datasources/import', { yaml_content: yaml })
export const applyTemplate = (datasourceId: number) => api.post('/datasources/apply-template', { datasource_id: datasourceId })
export const testDataSource = (id: number) => api.post(`/datasources/${id}/test`)

// Notifications
export const getNotifications = (params?: { page?: number; page_size?: number; keyword?: string; channel_type?: string }) =>
  api.get<{ items: NotificationChannel[]; total: number; page: number; page_size: number }>('/notifications', { params })
export const getAllNotifications = () => api.get<NotificationChannel[]>('/notifications/all')
export const getNotification = (id: number) => api.get<NotificationChannel>(`/notifications/${id}`)
export const createNotification = (n: NotificationChannel) => api.post<NotificationChannel>('/notifications', n)
export const updateNotification = (id: number, n: NotificationChannel) => api.put<NotificationChannel>(`/notifications/${id}`, n)
export const deleteNotification = (id: number) => api.delete(`/notifications/${id}`)
export const testNotification = (id: number) => api.post('/notifications/test', { id })
export interface MessageTemplate {
  style?: 'simple' | 'table' | 'card'
  title_format?: string
  show_cause?: boolean
  show_impact?: boolean
  show_value_range?: boolean
  show_hit_count?: boolean
  show_datasource?: boolean
  show_time?: boolean
  show_detail_link?: boolean
  host_format?: 'full' | 'short' | 'with_ip'
  time_format?: string
  value_precision?: number
  max_entries?: number
  max_bytes?: number
  // 简易文本模板（仅 simple 风格）：{datasource} {content} {time} 等占位符
  default_template?: string
  // 高级：自定义 Go template
  custom_markdown?: string
  custom_subject?: string
}
export interface TemplatePreviewResult {
  title: string
  markdown: string
  html: string
  plain: string
  bytes: number
  errors?: string[]
}
export const previewMessageTemplate = (template: MessageTemplate, resolved = false, mockCount = 0) =>
  api.post<TemplatePreviewResult>('/notifications/template/preview', { template, resolved, mock_count: mockCount })

// Cron Jobs
export const getCronJobs = () => api.get<CronJob[]>('/cronjobs')
export const getCronJob = (id: number) => api.get<CronJob>(`/cronjobs/${id}`)
export const createCronJob = (j: CronJob) => api.post<CronJob>('/cronjobs', j)
export const updateCronJob = (id: number, j: CronJob) => api.put<CronJob>(`/cronjobs/${id}`, j)
export const deleteCronJob = (id: number) => api.delete(`/cronjobs/${id}`)
/** 手动触发一次 AI 巡检分析：立即巡检 -> AI 分析 -> 推送飞书 */
export const triggerCronJobAIAnalyze = (id: number) => api.post(`/cronjobs/${id}/ai-analyze`)

// Reports
export const getReports = (params?: { page?: number; page_size?: number; keyword?: string; status?: string }) =>
  api.get<{ items: ReportRecord[]; total: number; page: number; page_size: number }>('/report-records', { params })
export const deleteReport = (id: number) => api.delete(`/report-records/${id}`)

// Metrics
export const getMetricTypes = (datasourceId?: number) => api.get<MetricType[]>('/metrics/types', { params: datasourceId ? { datasource_id: datasourceId } : {} })
export const createMetricType = (t: MetricType) => api.post<MetricType>('/metrics/types', t)
export const updateMetricType = (id: number, t: MetricType) => api.put<MetricType>(`/metrics/types/${id}`, t)
export const deleteMetricType = (id: number) => api.delete(`/metrics/types/${id}`)
export const createMetricConfig = (c: MetricConfig) => api.post<MetricConfig>('/metrics/configs', c)
export const updateMetricConfig = (id: number, c: MetricConfig) => api.put<MetricConfig>(`/metrics/configs/${id}`, c)
export const deleteMetricConfig = (id: number) => api.delete(`/metrics/configs/${id}`)
export const validatePromQL = (datasourceId: number | undefined, query: string) => api.post('/metrics/validate', { datasource_id: datasourceId, query })
// 引用计数：删除前提示被多少模板/数据源使用
export const getMetricConfigRefs = (id: number) => api.get<{ template_count: number; templates: { id: number; name: string }[] }>(`/metrics/configs/${id}/refs`)
export const getMetricTypeRefs = (id: number) => api.get<{ config_count: number; template_count: number }>(`/metrics/types/${id}/refs`)

// Settings
export const getSettings = () => api.get<Record<string, string>>('/settings')
export const updateSettings = (s: Record<string, string>) => api.put('/settings', s)
// 公开品牌信息（无需登录，用于登录页/侧边栏展示平台名称）
export const getPublicBrand = () => api.get<{ platform_name: string; platform_subtitle: string; report_signature: string }>('/public/brand')

// Inspect
export const triggerInspect = (req: InspectRequest) => api.post('/inspect', req)
export const getInspectTask = (taskId: string) => api.get(`/inspect/task/${taskId}`)
export const getInspectRecords = (params?: { page?: number; page_size?: number; keyword?: string; status?: string }) =>
  api.get<{ items: InspectRecord[]; total: number; page: number; page_size: number }>('/inspect/records', { params })

// Dashboard
export const getDashboardStats = () => api.get<DashboardStats>('/dashboard/stats')
export const getDashboardHealth = (datasourceId?: number) =>
  api.get('/dashboard/health', { params: datasourceId ? { datasource_id: datasourceId } : {} })

export const getDashboardHealthTrend = (days: number = 14) =>
  api.get('/dashboard/health/trend', { params: { days } })

// Sync Sources
export const getSyncSources = () => api.get<SyncSource[]>('/sync-sources')
export const createSyncSource = (s: SyncSource) => api.post<SyncSource>('/sync-sources', s)
export const updateSyncSource = (id: number, s: SyncSource) => api.put<SyncSource>(`/sync-sources/${id}`, s)
export const deleteSyncSource = (id: number) => api.delete(`/sync-sources/${id}`)
export const triggerSync = (id: number) => api.post(`/sync-sources/${id}/sync`)
export const getSyncLogs = (id: number) => api.get<SyncLog[]>(`/sync-sources/${id}/logs`)

// Templates
export const getTemplates = (params?: { page?: number; page_size?: number; keyword?: string }) =>
  api.get<{ items: any[]; total: number; page: number; page_size: number }>('/templates', { params })
export const getAllTemplates = () => api.get<any[]>('/templates/all')
export const initTemplates = () => api.post('/templates/init')
export const getTemplate = (id: number) => api.get(`/templates/${id}`)
export const createTemplate = (name: string, description?: string, category?: string, alert_source_ids?: number[]) => api.post('/templates', { name, description, category, alert_source_ids: JSON.stringify(alert_source_ids || []) })
export const updateTemplate = (id: number, t: any) => api.put(`/templates/${id}`, t)
export const deleteTemplate = (id: number) => api.delete(`/templates/${id}`)
export const getTemplateMetrics = (id: number) => api.get(`/templates/${id}/metrics`)
export const setTemplateMetrics = (id: number, metricConfigIds: number[]) => api.post(`/templates/${id}/metrics`, { metric_config_ids: metricConfigIds })
export const saveTemplateMetricOverride = (templateId: number, configId: number, data: any) => api.put(`/templates/${templateId}/metrics/${configId}/override`, data)
export const inspectWithTemplate = (id: number, req: any) => api.post(`/templates/${id}/inspect`, req)
// 引用计数：删除模板前提示被多少数据源绑定
export const getTemplateRefs = (id: number) => api.get<{ datasource_count: number; datasources: { id: number; name: string }[] }>(`/templates/${id}/refs`)

// AI Agent
export const aiChat = (message: string, sessionId?: string) =>
  api.post<{session_id: string}>('/ai/chat', { message, session_id: sessionId })

export const aiChatStream = (message: string, sessionId?: string): Promise<Response> => {
  const token = localStorage.getItem('token')
  return fetch('/api/promai/ai/chat', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`,
    },
    body: JSON.stringify({ message, session_id: sessionId }),
  })
}

export const getAiSessions = () => api.get('/ai/sessions')
export const deleteAiSession = (id: string) => api.delete(`/ai/sessions/${id}`)
export const testAiModel = (model: {
  name: string; provider: string; model: string; base_url: string;
  api_key: string; thinking_level: string; max_tokens: number; proxy_url?: string
}) => api.post('/ai/test-model', model)

// ===== Alerting =================================================================
import type {
  AlertRule, AlertInstance, AlertHistoryRow, HistorySession, AlertSilence, AlertInhibit, AlertRoute,
  AlertGroup, AlertNotifyLog, AlertStats, EvaluatorStatus, TestRuleResult, TimelineGroup,
  AlertIncidentList, AlertIncident, AlertNoiseTop, DenoiseConfig, InspectionExpectation,
} from '../types/alerting'

export const getAlertRules = (params?: { keyword?: string; severity?: string; enabled?: string; origin?: string; source_type?: string; page?: number; page_size?: number }) =>
  api.get<{ items: AlertRule[]; total: number }>('/alert/rules', { params })
export const getAlertRule = (id: number) => api.get<AlertRule>(`/alert/rules/${id}`)
export const createAlertRule = (r: AlertRule) => api.post<AlertRule>('/alert/rules', r)
export const updateAlertRule = (id: number, r: AlertRule) => api.put<AlertRule>(`/alert/rules/${id}`, r)
export const deleteAlertRule = (id: number) => api.delete(`/alert/rules/${id}`)
export const testAlertRule = (id: number) =>
  api.post<{ rule_id: number; datasources: TestRuleResult[] }>(`/alert/rules/${id}/test`)
export const batchToggleAlertRules = (ids: number[], enabled: boolean) =>
  api.post('/alert/rules/batch-toggle', { ids, enabled })
export const batchDeleteAlertRules = (ids: number[]) =>
  api.post<{ success: boolean; count: number }>('/alert/rules/batch-delete', { ids })
export const batchEditAlertRules = (ids: number[], updates: Record<string, any>) =>
  api.post<{ success: boolean; count: number }>('/alert/rules/batch-edit', { ids, ...updates })
export const generateAlertRulesFromTemplate = (templateId: number) =>
  api.post<{ success: boolean; created: number }>('/alert/rules/generate-from-template', { template_id: templateId })

export const getAlertInstances = (params?: {
  page?: number; page_size?: number; state?: string; severity?: string;
  datasource_id?: number; rule_id?: number; fingerprint?: string;
  keyword?: string; include_masked?: string; from?: string; to?: string
}) => api.get<{ items: AlertInstance[]; total: number; page: number; page_size: number }>('/alert/instances', { params })
export const getAlertInstance = (fingerprint: string) =>
  api.get<{ instance: AlertInstance; history: AlertHistoryRow[]; notify_logs?: AlertNotifyLog[] }>(`/alert/instances/${fingerprint}`)
export const getAlertInstancesTrend = (fingerprints: string[], minutes = 60, includeRepeats = false) =>
  api.post<Record<string, [number, number][]>>('/alert/instances/trend', { fingerprints, minutes, include_repeats: includeRepeats })
export const clearAlertInstances = () =>
  api.delete('/alert/instances')
// 标记单条告警已读（未读红点清零）
export const markAlertInstanceRead = (fingerprint: string) =>
  api.post<{ success: boolean; fingerprint: string }>(`/alert/instances/${fingerprint}/read`)
// 批量操作：delete=删除 / resolve=结束 / silence=静默 / read=标记已读
export const batchAlertInstances = (action: 'delete' | 'resolve' | 'silence' | 'read', fingerprints: string[], opts?: { silence_minutes?: number; comment?: string }) =>
  api.post<{ action: string; done: number; failed: number; errors?: string[] }>('/alert/instances/batch', {
    action, fingerprints, ...(opts || {}),
  })

export const getAlertHistory = (params?: {
  page?: number; page_size?: number; rule_id?: number; rule_name?: string; datasource_id?: number;
  event_type?: string; severity?: string; keyword?: string; from?: string; to?: string
}) => api.get<{ items: AlertHistoryRow[]; total: number; page: number; page_size: number }>('/alert/history', { params })

// 删除历史告警：{ fingerprints: [...] } 按指纹批量删，或 { all: true } 清空全部
export const deleteAlertHistory = (payload: { fingerprints?: string[]; all?: boolean }) =>
  api.delete<{ ok: boolean; deleted: number }>('/alert/history', { data: payload })

// 已恢复告警实例聚合列表（历史页）：同一指纹合并为一条，只含已恢复的实例
export const getAlertHistorySessions = (params?: {
  page?: number; page_size?: number; rule_id?: number; rule_name?: string; datasource_id?: number;
  severity?: string; keyword?: string; from?: string; to?: string
}) => api.get<{ items: HistorySession[]; total: number; page: number; page_size: number }>('/alert/history/sessions', { params })

// 历史告警中出现的去重规则名（用于筛选下拉）
export const getAlertHistoryRuleNames = () =>
  api.get<{ items: string[] }>('/alert/history/rule-names')

export const getAlertHistoryTimeline = (params?: {
  rule_id?: number; datasource_id?: number; rule_name?: string; keyword?: string; from?: string; to?: string
}) => api.get<{ groups: TimelineGroup[] }>('/alert/history/timeline', { params })

export const getAlertSilences = (params?: { include_expired?: boolean; page?: number; page_size?: number }) =>
  api.get<{ items: AlertSilence[]; total: number }>('/alert/silences', { params })
export const getAlertSilence = (id: number) => api.get<AlertSilence>(`/alert/silences/${id}`)
export const createAlertSilence = (s: AlertSilence) => api.post<AlertSilence>('/alert/silences', s)
export const updateAlertSilence = (id: number, s: AlertSilence) => api.put<AlertSilence>(`/alert/silences/${id}`, s)
export const deleteAlertSilence = (id: number) => api.delete(`/alert/silences/${id}`)

export const getAlertInhibits = (params?: { page?: number; page_size?: number }) =>
  api.get<{ items: AlertInhibit[]; total: number }>('/alert/inhibits', { params })
export const getAlertInhibit = (id: number) => api.get<AlertInhibit>(`/alert/inhibits/${id}`)
export const createAlertInhibit = (i: AlertInhibit) => api.post<AlertInhibit>('/alert/inhibits', i)
export const updateAlertInhibit = (id: number, i: AlertInhibit) => api.put<AlertInhibit>(`/alert/inhibits/${id}`, i)
export const deleteAlertInhibit = (id: number) => api.delete(`/alert/inhibits/${id}`)

// 巡检预期行为（按 label 匹配，AI 巡检分析时标注但不排除）
export const getInspectionExpectations = (params?: { page?: number; page_size?: number }) =>
  api.get<{ items: InspectionExpectation[]; total: number }>('/alert/expectations', { params })
export const getInspectionExpectation = (id: number) => api.get<InspectionExpectation>(`/alert/expectations/${id}`)
export const createInspectionExpectation = (e: InspectionExpectation) => api.post<InspectionExpectation>('/alert/expectations', e)
export const updateInspectionExpectation = (id: number, e: InspectionExpectation) => api.put<InspectionExpectation>(`/alert/expectations/${id}`, e)
export const deleteInspectionExpectation = (id: number) => api.delete(`/alert/expectations/${id}`)

export const getAlertRoutes = () => api.get<{ items: AlertRoute[]; total: number }>('/alert/routes')
export const getAlertRoute = (id: number) => api.get<AlertRoute>(`/alert/routes/${id}`)
export const createAlertRoute = (r: AlertRoute) => api.post<AlertRoute>('/alert/routes', r)
export const updateAlertRoute = (id: number, r: AlertRoute) => api.put<AlertRoute>(`/alert/routes/${id}`, r)
export const deleteAlertRoute = (id: number) => api.delete(`/alert/routes/${id}`)

export const getAlertGroups = () => api.get<{ items: AlertGroup[]; total: number }>('/alert/groups')
export const getAlertNotifyLogs = (params?: {
  page?: number; page_size?: number; status?: string; channel_id?: number; rule_id?: number
}) => api.get<{ items: AlertNotifyLog[]; total: number; page: number; page_size: number }>('/alert/notify-logs', { params })

export const getAlertStats = () => api.get<AlertStats>('/alert/stats')
export const getAlertEvaluatorStatus = () => api.get<EvaluatorStatus>('/alert/evaluator/status')

// ===== 告警降噪聚合（分析级，不触碰通知链路） =====
// AlertHistory → Alert（按 fingerprint 去重）→ Incident（按 alertname 在时间窗内聚合）
// 参考 FlashDuty/Nightingale 模型（按用户诉求简化）。通知层面的分组/去重/抑制由 Alertmanager 负责。
export const getAlertIncidents = (params?: {
  hours?: number; datasource_id?: number; severity?: string; alertname?: string; instance?: string;
  state?: string; window_minutes?: number; storm_threshold?: number; resource_labels?: string; limit?: number
}) => api.get<AlertIncidentList>('/alert/incidents', { params })

export const getAlertIncidentDetail = (params: {
  key: string; hours?: number; window_minutes?: number; storm_threshold?: number; resource_labels?: string
}) => api.get<{ incident: AlertIncident; window_minutes: number }>('/alert/incidents/detail', { params })

export const deleteAlertIncidents = (keys: string[], params?: {
  hours?: number; window_minutes?: number; storm_threshold?: number; resource_labels?: string
}) => api.post<{ ok: boolean; matched: number; fingerprints: number }>('/alert/incidents/delete', { keys }, { params })

export const getAlertNoiseTop = (params?: {
  hours?: number; limit?: number; window_minutes?: number; storm_threshold?: number; resource_labels?: string
}) => api.get<AlertNoiseTop>('/alert/noise-top', { params })

export const getDenoiseConfig = () => api.get<DenoiseConfig>('/alert/denoise-config')
export const saveDenoiseConfig = (cfg: DenoiseConfig) => api.put<{ ok: boolean }>('/alert/denoise-config', cfg)

// ===== 外部告警源（n9e / 华为云 / 通用 webhook） =====
import type { ExternalAlertSource, ExternalRule, ExternalSyncResult } from '../types/alerting'

export const getAlertSources = () => api.get<ExternalAlertSource[]>('/alert-sources')
export const createAlertSource = (s: ExternalAlertSource) => api.post<ExternalAlertSource>('/alert-sources', s)
export const updateAlertSource = (id: number, s: Partial<ExternalAlertSource>) => api.put<ExternalAlertSource>(`/alert-sources/${id}`, s)
export const deleteAlertSource = (id: number) => api.delete(`/alert-sources/${id}`)
export const syncAlertSource = (id: number) => api.post<ExternalSyncResult>(`/alert-sources/${id}/sync`)
export const getAlertSourceRules = (id: number, params?: { page?: number; page_size?: number }) =>
  api.get<{ total: number; page: number; page_size: number; rules: ExternalRule[] }>(`/alert-sources/${id}/rules`, { params })

// 手动结束一条活跃告警（含外部告警）
export const resolveAlertInstance = (fingerprint: string) =>
  api.post<{ message: string; fingerprint: string }>(`/alert/instances/${fingerprint}/resolve`)

// AI Skills (SKILL.md 规范 — 文件系统存储)
export const getAISkills = () =>
  api.get<{ items: AiSkill[]; total: number }>('/ai/skills')
export const createAISkill = (s: AiSkill) => api.post<AiSkill>('/ai/skills', s)
export const updateAISkill = (name: string, s: AiSkill) => api.post(`/ai/skills/${name}`, s)
export const deleteAISkill = (name: string) => api.delete(`/ai/skills/${name}`)

// ===== 敏感端口扫描 =====
export const createPortScan = (targets: string, ports: number[]) =>
  api.post<{ success: boolean; task_id: string; total_targets: number; total_ports: number; message: string }>('/portscan', { targets, ports })
export const getPortScanTasks = (params?: { page?: number; page_size?: number }) =>
  api.get<{ items: PortScanTask[]; total: number; page: number; page_size: number }>('/portscan/tasks', { params })
export const getPortScanTask = (id: number) => api.get<PortScanTask>(`/portscan/tasks/${id}`)
export const getPortScanResults = (id: number) =>
  api.get<{ items: PortScanResult[]; total: number }>(`/portscan/tasks/${id}/results`)
export const deletePortScanTask = (id: number) => api.delete(`/portscan/tasks/${id}`)
export const getPortScanPorts = () => api.get<{ items: PortInfo[] }>('/portscan/ports')
export const downloadPortScanReport = (id: number) =>
  api.get(`/portscan/tasks/${id}/export`, { responseType: 'blob' })

// ===== Flashcat 场景层（v2：可配置服务树 / 定向检查 / 北极星 / 事件墙） ==============
export interface FCObject {
  id: string
  node_id: number
  name: string
  path: string
  level: string
  parent_id: number
  datasource_id: number
  datasource: string
  instance: string
  layer: string
  health_score: number
  status: string
  total_checks: number
  critical: number
  warning: number
  normal: number
  alerts: number
  metrics: number
  no_data: boolean
  last_check_at?: string
}
export interface FCLayer {
  name: string
  count: number
  critical: number
  warning: number
  normal: number
  avg_score: number
}
export interface FCTreeNode {
  id: string
  node_id: number
  name: string
  level: string
  count: string
  health_score: number
  status: string
  children?: FCTreeNode[]
  object_id?: string
}
export interface FCStats {
  total: number
  critical: number
  warning: number
  normal: number
  no_data: number
}
export interface FCDatasource { id: number; name: string }
export interface FCBootstrap {
  has_data: boolean
  overall_health: number
  objects: FCObject[]
  layers: FCLayer[]
  tree: FCTreeNode
  stats: FCStats
  last_report_at?: string
  datasources: FCDatasource[]
  message?: string
}
export interface FCCheckItem {
  key: string
  name: string
  check_type: string
  status: string
  result: string
  detail: string
  cost_ms: number
}
export interface FCChecksResponse {
  checks: FCCheckItem[]
  health_score: number
  status: string
  path: string
  node_id: number
}
export interface FCAIResponse {
  success: boolean
  text?: string
  model?: string
  ref_id?: string
  at?: string
  error?: string
  prompt?: string
  checks?: FCCheckItem[]
}
export interface FCPolarisItem {
  id: number
  node_id: number
  name: string
  description: string
  target: number
  current: number
  error_budget: number
  status: string
  window_days: number
  trend: number[]
}
export interface FCEventItem {
  id: string
  node_id: number
  node_name: string
  source: string
  severity: string
  title: string
  detail: string
  status: string
  at: string
}
export interface FCNode {
  id?: number
  parent_id: number
  parent_name?: string
  name: string
  level: string
  datasource_id: number
  datasource?: string
  instance: string
  sort_order: number
  remark?: string
}
export interface FCCheckConfig {
  id?: number
  key: string
  name: string
  enabled: boolean
  sort_order: number
  check_type: string
  promql: string
  threshold: number
  compare_op: string
  unit: string
  remark?: string
}

const fcNode = (v: number | string) => (typeof v === 'string' ? v.replace(/^n/, '') : v)

export const getFiremapObjects = (datasourceId?: number) =>
  api.get<FCBootstrap>('/firemap/objects', { params: datasourceId ? { datasource_id: datasourceId } : {} })
export const getFiremapObject = (node: number | string) =>
  api.get<{
    object: FCObject; path: string; alerts: any[]; metrics: any[]; checks: any[]
    conclusion?: string; conclusion_model?: string; conclusion_at?: string
  }>('/firemap/object', { params: { node: fcNode(node) } })
export const runFiremapChecks = (node: number | string) =>
  api.get<FCChecksResponse>('/firemap/checks', { params: { node: fcNode(node) } })
export const triggerFiremapAIAnalyze = (payload: { node_id: number; prompt?: string }) =>
  api.post<FCAIResponse>('/firemap/ai-analyze', payload)
export const getFiremapPolaris = () =>
  api.get<{ items: FCPolarisItem[]; overall: number }>('/firemap/polaris')
export interface FiremapEventsParams {
  hours?: number      // 相对时间快捷：1/6/24/168；0 或不传 = 不限
  since?: string      // 显式起始（'YYYY-MM-DD HH:mm:ss' 或 ISO8601），优先于 hours
  until?: string      // 显式结束
  limit?: number      // 最多返回条数，默认 300，上限 500
}
export const getFiremapEvents = (params?: FiremapEventsParams) =>
  api.get<{ events: FCEventItem[]; total?: number; since?: string; until?: string }>('/firemap/events', { params })
export const getFiremapNodes = () =>
  api.get<{ nodes: FCNode[]; datasources: { id: number; name: string }[] }>('/firemap/nodes')
export const saveFiremapNode = (node: FCNode) =>
  api.post<{ success: boolean; node: FCNode }>('/firemap/nodes', node)
export const deleteFiremapNode = (id: number) =>
  api.delete('/firemap/nodes', { params: { id } })
export const seedFiremapNodes = () =>
  api.post<{ success: boolean; created: number; message: string }>('/firemap/seed')
export const getFiremapCheckConfigs = () =>
  api.get<{ configs: FCCheckConfig[] }>('/firemap/check-config')
export const saveFiremapCheckConfig = (cfg: FCCheckConfig) =>
  api.post<{ success: boolean; config: FCCheckConfig }>('/firemap/check-config', cfg)
export const deleteFiremapCheckConfig = (id: number) =>
  api.delete('/firemap/check-config', { params: { id } })
export const getAllDataSourcesSimple = () => api.get<any[]>('/datasources/all')

export default api

// ===== 日志分析 / 链路分析（VictoriaLogs） =====================================
export interface LogEntry {
  _time?: string
  _msg?: string
  [key: string]: any
}
export interface LogQueryResponse {
  logs: LogEntry[]
  total: number
  elapsed_ms: number
  fields: string[]
  query: string
  configured: boolean
  error?: string
}
export interface LogHitsResponse {
  timestamps: string[]
  values: number[]
  total: number
}
export interface LogFieldValue { value: string; count: number }

export const getLogStatus = () => api.get<{ configured: boolean }>('/log/status')
export const queryLogs = (p: { q: string; start?: string; end?: string; limit?: number; offset?: number; sort?: string }) =>
  api.get<LogQueryResponse>('/log/query', { params: p })
export const getLogHits = (p: { q: string; start?: string; end?: string; step?: string }) =>
  api.get<LogHitsResponse>('/log/hits', { params: p })
export const getLogFieldValues = (p: { q: string; field: string; start?: string; end?: string; limit?: number }) =>
  api.get<{ field: string; values: LogFieldValue[] }>('/log/fields', { params: p })

export interface TraceItem { trace_id: string; service: string; last_at: string; message: string; field?: string }
export interface TraceSpan {
  trace_id: string; span_id?: string; service: string; method?: string
  level?: string; message: string; time: string; raw: Record<string, any>
}
export const getRecentTraces = (p?: { start?: string; end?: string; field?: string }) =>
  api.get<{ items: TraceItem[]; count: number; field?: string }>('/trace/recent', { params: p || {} })
export const getTraceByID = (id: string, p?: { start?: string; end?: string; field?: string }) =>
  api.get<{ trace_id: string; spans: TraceSpan[]; count: number }>('/trace/by-id', { params: { id, ...(p || {}) } })

// ===== 北极星（Polaris）：业务核心指标故障发现入口 =====
export interface PBusinessView {
  id: number; name: string; description: string; owner: string; team: string
  icon: string; color: string; datasource_id: number; enabled: boolean; sort_order: number
  chart_count: number; alert_count: number; health: number; status: string
}
export interface PMetricPoint { t: number; v: number }
export interface PChartSeries { name: string; points: PMetricPoint[] }
export interface PChartView {
  id: number; business_id: number; name: string; chart_type: string
  metric_ids: number[]; metric_names: string[]; custom_promql: string; unit: string
  enabled: boolean; sort_order: number; series: PChartSeries[]; status: string; last_value: number
}
export interface PDetectionView {
  id: number; business_id: number; chart_id: number; metric_id: number
  detect_type: string; threshold: number; compare_op: string; duration_min: number
  sensitivity: number; enabled: boolean; triggered: boolean; reason: string; last_value: number
}
export interface PPolicyView {
  id: number; business_id: number; name: string; severity: string; enabled: boolean
  active_from: string; active_to: string; notify_target: string; firemap_node_id: number
}
export interface PMetricPoolItem { id: number; name: string; query: string; unit: string; type: string; threshold: number }
export interface PBusinessDetail {
  business: PBusinessView; charts: PChartView[]; detections: PDetectionView[]
  policies: PPolicyView[]; slos: FCPolarisItem[]; has_datasource: boolean
}
export const getPolarisBusinesses = () => api.get<{ items: PBusinessView[] }>('/polaris/businesses')
export const getPolarisBusiness = (id: number) => api.get<PBusinessDetail>('/polaris/business', { params: { id } })
export const getPolarisMetricPool = () => api.get<{ items: PMetricPoolItem[] }>('/polaris/metric-pool')
export const polarisCRUD = (kind: string, data: Record<string, any>) =>
  api.post<{ success: boolean; id: number }>(`/polaris/crud?kind=${kind}`, data)
export const polarisDelete = (kind: string, id: number) =>
  api.delete(`/polaris/crud?kind=${kind}&id=${id}`)

// ===== 灭火图下钻规则 / 告警规则 / 时间轴 =====
export interface FCDrillRule { id: number; node_id: number; name: string; target_type: string; target_url: string; query: string; enabled: boolean; sort_order: number }
export interface FCAlertRule { id: number; node_id: number; name: string; condition: string; score_below: number; enabled: boolean; notify_target: string; last_fired_at?: string }
export interface FCHealthPoint { t: number; score: number; status: string; critical: number; warning: number; alerts: number }
export const getFiremapDrillRules = (nodeId: number) => api.get<{ items: FCDrillRule[] }>('/firemap/rule', { params: { kind: 'drill', node_id: nodeId } })
export const getFiremapAlertRules = () => api.get<{ items: FCAlertRule[] }>('/firemap/rule', { params: { kind: 'alert' } })
export const firemapCRUDRule = (kind: string, data: Record<string, any>) =>
  api.post<{ success: boolean; id: number }>(`/firemap/rule?kind=${kind}`, data)
export const firemapDeleteRule = (kind: string, id: number) =>
  api.delete(`/firemap/rule?kind=${kind}&id=${id}`)
export const getFiremapTimeline = (nodeId?: number, minutes?: number) =>
  api.get<{ points: FCHealthPoint[] }>('/firemap/timeline', { params: { node_id: nodeId, minutes } })
export const getFiremapTemplates = (datasourceId?: number) => api.get<{ items: any[] }>('/firemap/templates', { params: datasourceId ? { datasource_id: datasourceId } : {} })
export const applyFiremapTemplate = (templateId: number, datasourceId: number) =>
  api.post<{ created: number; message: string }>('/firemap/template-apply', { template_id: templateId, datasource_id: datasourceId })

// ===== PromAI 核心链路 + AI 效果评估 =====
export interface PipelineStage {
  key: string; name: string; desc: string; count: number; link: string
}
export interface PipelineOverview {
  days: number; stages: PipelineStage[]; alert_firing: number
  ai_success: number; ai_cron_jobs: number; total_sources: number; total_alerts: number
}
export interface AIFeedback {
  id: number; analysis_id: number; ref_id: string; verdict: string; note: string
  alerts_before: number; alerts_after: number; operator: string; created_at: string
}
export interface AIEffectiveness {
  days: number; confirmed: number; invalid: number; pending: number; total: number
  confirm_rate: number; avg_alert_drop: number
}
export const getPipelineOverview = (days = 7) =>
  api.get<PipelineOverview>('/pipeline/overview', { params: { days } })
export const getAIFeedbacks = (params?: { analysis_id?: number; ref_id?: string }) =>
  api.get<{ items: AIFeedback[] }>('/ai/feedback', { params })
export const submitAIFeedback = (data: Partial<AIFeedback>) =>
  api.post<AIFeedback>('/ai/feedback', data)
export const getAIEffectiveness = (days = 30) =>
  api.get<AIEffectiveness>('/ai/effectiveness', { params: { days } })

// ===== 反馈来源候选 + 记录批量清理 =====
export interface FeedbackSource {
  type: 'inspection' | 'alert' | 'diagnosis' | string
  id: string
  label: string
  created_at: string
}
export const getAIFeedbackSources = () =>
  api.get<{ items: FeedbackSource[] }>('/ai/feedback/sources')

export interface CleanupPayload {
  ids?: number[]
  keep_recent?: number
  older_than_days?: number
  confirm: boolean
}
export interface CleanupResult {
  deleted: number
  scanned: number
  files_removed?: number
}
export const batchDeleteReports = (payload: CleanupPayload) =>
  api.post<CleanupResult>('/report-records/batch-delete', payload)
export const batchDeleteInspectRecords = (payload: CleanupPayload) =>
  api.post<CleanupResult>('/inspect/records/batch-delete', payload)
