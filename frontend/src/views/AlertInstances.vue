<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Warning /></el-icon> 实时告警</h2>
      <p>当前 PromAI 监控到的活跃告警实例（按指纹去重，自动 30s 刷新）</p>
    </div>

    <!-- 顶部统计 -->
    <div class="stats-row">
      <div class="stat-card stat-critical">
        <div class="stat-label">严重 critical</div>
        <div class="stat-value">{{ summary.critical }}</div>
      </div>
      <div class="stat-card stat-warning">
        <div class="stat-label">警告 warning</div>
        <div class="stat-value">{{ summary.warning }}</div>
      </div>
      <div class="stat-card stat-info">
        <div class="stat-label">提醒 info</div>
        <div class="stat-value">{{ summary.info }}</div>
      </div>
      <div class="stat-card stat-firing">
        <div class="stat-label">firing 中</div>
        <div class="stat-value">{{ summary.firing }}</div>
      </div>
      <div class="stat-card stat-pending">
        <div class="stat-label">pending 中</div>
        <div class="stat-value">{{ summary.pending }}</div>
      </div>
      <div class="stat-card stat-evaluator">
        <div class="stat-label">评估器</div>
        <div class="stat-value" :style="{ color: evaluator.running ? 'var(--emerald)' : 'var(--red)' }">
          {{ evaluator.running ? '运行中' : '未运行' }}
        </div>
        <div class="stat-extra" v-if="evaluator.running">
          tick #{{ evaluator.tick_count || 0 }} · queue {{ evaluator.queue_depth || 0 }}
        </div>
      </div>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 活跃告警列表</h3>
        <div class="action-bar">
          <el-select v-model="filters.severity" placeholder="全部级别" clearable style="width: 120px;" @change="fetchData">
            <el-option label="严重 critical" value="critical" />
            <el-option label="警告 warning" value="warning" />
            <el-option label="提醒 info" value="info" />
          </el-select>
          <el-select v-model="filters.state" placeholder="全部状态" clearable style="width: 120px;" @change="fetchData">
            <el-option label="firing" value="firing" />
            <el-option label="pending" value="pending" />
            <el-option label="resolved" value="resolved" />
          </el-select>
          <el-select v-model="filters.datasource_id" placeholder="全部数据源" clearable filterable style="width: 180px;" @change="fetchData">
            <el-option v-for="ds in datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
          </el-select>
          <el-input v-model="filters.keyword" placeholder="搜索 label" style="width: 180px;" clearable @keyup.enter="fetchData">
            <template #suffix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-checkbox v-model="includeMasked" @change="fetchData">含已静默/抑制</el-checkbox>
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button plain type="danger" @click="handleClearAll"><el-icon><Delete /></el-icon> 清空所有</el-button>
        </div>
      </div>

      <el-table :data="rows" v-loading="loading" stripe size="default" @row-click="openDetail">
        <el-table-column label="级别" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="dark" :style="severityStyle(row.severity)">{{ severityLabel(row.severity) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" :style="stateStyle(row.state)">{{ row.state }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="抑制/静默" width="100" align="center">
          <template #default="{ row }">
            <el-tag v-if="row.silenced_by?.length" size="small" style="background:rgba(96,165,250,0.15);color:#60a5fa;border:none;">静默</el-tag>
            <el-tag v-if="row.inhibited_by?.length" size="small" style="background:rgba(251,191,36,0.15);color:#fbbf24;border:none;">抑制</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="规则" min-width="200">
          <template #default="{ row }">
            <div style="font-weight:600;color:var(--text-primary);">{{ ruleName(row.rule_id) }}</div>
            <div style="font-size:11px;color:var(--text-tertiary);">{{ row.annotations?.summary || '-' }}</div>
          </template>
        </el-table-column>
        <el-table-column label="数据源" width="160">
          <template #default="{ row }">
            <span style="color:var(--text-secondary);">{{ dsName(row.datasource_id) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="value / threshold" width="180">
          <template #default="{ row }">
            <span style="color: var(--cyan); font-weight: 600;">{{ formatNum(row.value) }}</span>
            <span style="color: var(--text-tertiary);"> / {{ formatNum(row.threshold) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="标签" min-width="260">
          <template #default="{ row }">
            <el-tag v-for="(v, k) in row.labels || {}" :key="k" size="small"
                    style="margin: 1px 4px 1px 0; background: rgba(99,102,241,0.1); color:#818cf8; border:none;">
              {{ k }}={{ v }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="触发时间" width="170">
          <template #default="{ row }">
            <span style="font-size: 12px; color: var(--text-tertiary);">{{ formatTime(row.fired_at || row.active_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text style="color: var(--cyan);" @click.stop="openDetail(row)">详情</el-button>
            <el-button size="small" text style="color: var(--amber);" @click.stop="openSilence(row)">静默</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pager">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100, 200]"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchData"
          @size-change="fetchData"
        />
      </div>
    </div>

    <!-- 详情抽屉 -->
    <el-drawer v-model="detailDrawer" size="540" :title="`告警详情 · ${detailRow?.fingerprint?.slice(0,12)}`" direction="rtl">
      <div v-if="detailRow" class="detail">
        <div class="detail-section">
          <div class="detail-row">
            <span class="k">状态</span>
            <el-tag size="small" :style="stateStyle(detailRow.state)">{{ detailRow.state }}</el-tag>
            <el-tag size="small" effect="dark" :style="severityStyle(detailRow.severity)" style="margin-left:8px;">
              {{ severityLabel(detailRow.severity) }}
            </el-tag>
          </div>
          <div class="detail-row"><span class="k">规则</span><span class="v">{{ ruleName(detailRow.rule_id) }}</span></div>
          <div class="detail-row"><span class="k">数据源</span><span class="v">{{ dsName(detailRow.datasource_id) }}</span></div>
          <div class="detail-row"><span class="k">value</span><span class="v" style="color:var(--cyan);">{{ formatNum(detailRow.value) }}</span></div>
          <div class="detail-row"><span class="k">threshold</span><span class="v">{{ formatNum(detailRow.threshold) }}</span></div>
          <div class="detail-row"><span class="k">active_at</span><span class="v">{{ formatTime(detailRow.active_at) }}</span></div>
          <div class="detail-row"><span class="k">fired_at</span><span class="v">{{ formatTime(detailRow.fired_at) }}</span></div>
          <div class="detail-row"><span class="k">last_eval_at</span><span class="v">{{ formatTime(detailRow.last_eval_at) }}</span></div>
          <div class="detail-row"><span class="k">通知次数</span><span class="v">{{ detailRow.notified_count }}</span></div>
          <div class="detail-row"><span class="k">上次通知</span><span class="v">{{ formatTime(detailRow.last_notified_at) }}</span></div>
          <div class="detail-row"><span class="k">下轮触发</span><span class="v" style="color:var(--danger);font-weight:600;">{{ formatTime(detailRow.next_notify_at) }}</span></div>
        </div>
        <div class="detail-section">
          <div class="section-title">Labels</div>
          <pre>{{ JSON.stringify(detailRow.labels || {}, null, 2) }}</pre>
        </div>
        <div class="detail-section">
          <div class="section-title">Annotations</div>
          <pre>{{ JSON.stringify(detailRow.annotations || {}, null, 2) }}</pre>
        </div>
        <div class="detail-section">
          <div class="section-title">通知去向 ({{ notifyLogsRows.length }})</div>
          <div v-if="notifyLogsRows.length === 0" style="color:var(--text-tertiary);font-size:12px;padding:8px 0;">
            该分组暂无通知发送记录
          </div>
          <div v-for="n in notifyLogsRows" :key="n.id" class="notify-row">
            <el-tag size="small" :style="notifyStatusStyle(n.status)">{{ notifyStatusLabel(n.status) }}</el-tag>
            <span class="chan">{{ n.channel_type }}</span>
            <span class="cname">{{ channelName(n.channel_id) }}</span>
            <span class="cnt">{{ n.alert_count }} 条告警</span>
            <span class="ts">{{ formatTime(n.sent_at) }}</span>
            <div v-if="n.error" class="err">错误: {{ n.error }}</div>
          </div>
        </div>
        <div class="detail-section" v-if="historyRows.length">
          <div class="section-title">历史事件（最近 {{ historyRows.length }} 条）</div>
          <div v-for="h in historyRows" :key="h.id" class="hist-row">
            <el-tag size="small">{{ h.event_type }}</el-tag>
            <span class="ts">{{ formatTime(h.occurred_at) }}</span>
            <span class="vv">value={{ formatNum(h.value) }}</span>
          </div>
        </div>
      </div>
    </el-drawer>

    <!-- 一键静默 -->
    <el-dialog v-model="silenceDialog" title="创建静默" width="560">
      <el-form :model="silenceForm" label-width="100px">
        <el-form-item label="静默原因">
          <el-input v-model="silenceForm.comment" type="textarea" :rows="2" placeholder="必填，描述静默原因" />
        </el-form-item>
        <el-form-item label="持续时间">
          <el-radio-group v-model="silenceForm.durationMin">
            <el-radio :label="15">15m</el-radio>
            <el-radio :label="60">1h</el-radio>
            <el-radio :label="240">4h</el-radio>
            <el-radio :label="1440">24h</el-radio>
            <el-radio :label="10080">7d</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="匹配条件">
          <div style="font-size: 12px; color: var(--text-tertiary); margin-bottom: 6px;">
            将基于当前告警的 alertname / 关键标签自动生成，可手动微调
          </div>
          <el-table :data="silenceForm.matchers" size="small">
            <el-table-column label="标签" width="160">
              <template #default="{ row }">
                <el-input v-model="row.name" size="small" />
              </template>
            </el-table-column>
            <el-table-column label="op" width="80">
              <template #default="{ row }">
                <el-select v-model="row.op" size="small">
                  <el-option label="=" value="=" />
                  <el-option label="!=" value="!=" />
                  <el-option label="=~" value="=~" />
                  <el-option label="!~" value="!~" />
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="值">
              <template #default="{ row }">
                <el-input v-model="row.value" size="small" />
              </template>
            </el-table-column>
            <el-table-column width="60">
              <template #default="{ $index }">
                <el-button size="small" text style="color: var(--red);"
                  @click="silenceForm.matchers.splice($index, 1)">删</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-button size="small" plain style="margin-top: 6px;"
            @click="silenceForm.matchers.push({ name: '', op: '=', value: '' })">
            <el-icon><Plus /></el-icon> 添加
          </el-button>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="silenceDialog = false">取消</el-button>
        <el-button type="primary" :loading="silenceSaving" @click="handleSilenceSubmit">创建静默</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, ElNotification } from 'element-plus'
import {
  getAlertInstances, getAlertInstance, createAlertSilence, clearAlertInstances,
  getAllDataSources, getAlertRules, getAlertEvaluatorStatus, getAllNotifications,
} from '../api'
import type { AlertInstance, AlertHistoryRow, AlertRule, EvaluatorStatus } from '../types/alerting'
import type { DataSource, NotificationChannel } from '../types'

const router = useRouter()

function getCssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

const rows = ref<AlertInstance[]>([])
const datasources = ref<DataSource[]>([])
const rules = ref<AlertRule[]>([])
const allChannels = ref<NotificationChannel[]>([])
const evaluator = ref<EvaluatorStatus>({ running: false })
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const includeMasked = ref(false)
const filters = ref<{ severity: string; state: string; datasource_id: number | ''; keyword: string }>({
  severity: '', state: '', datasource_id: '', keyword: '',
})

const detailDrawer = ref(false)
const detailRow = ref<AlertInstance | null>(null)
const historyRows = ref<AlertHistoryRow[]>([])
const notifyLogsRows = ref<any[]>([])

const silenceDialog = ref(false)
const silenceSaving = ref(false)
const silenceForm = ref<{ comment: string; durationMin: number; matchers: Array<{ name: string; op: string; value: string }> }>({
  comment: '', durationMin: 60, matchers: [],
})

const summary = computed(() => {
  const s = { critical: 0, warning: 0, info: 0, firing: 0, pending: 0 }
  for (const r of rows.value) {
    if (r.severity === 'critical') s.critical++
    else if (r.severity === 'warning') s.warning++
    else if (r.severity === 'info') s.info++
    if (r.state === 'firing') s.firing++
    else if (r.state === 'pending') s.pending++
  }
  return s
})

async function fetchData() {
  loading.value = true
  try {
    const params: any = {
      page: page.value, page_size: pageSize.value,
      include_masked: includeMasked.value ? 'true' : 'false',
    }
    if (filters.value.severity) params.severity = filters.value.severity
    if (filters.value.state) params.state = filters.value.state
    if (filters.value.datasource_id !== '') params.datasource_id = filters.value.datasource_id
    if (filters.value.keyword) params.keyword = filters.value.keyword
    const res = await getAlertInstances(params)
    rows.value = res.data.items as AlertInstance[]
    total.value = res.data.total
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally { loading.value = false }
}

async function refreshEvaluator() {
  try {
    const res = await getAlertEvaluatorStatus()
    evaluator.value = res.data
  } catch { evaluator.value = { running: false } }
}

async function loadMeta() {
  try {
    const [dsRes, rulesRes, chRes] = await Promise.all([getAllDataSources(), getAlertRules(), getAllNotifications()])
    datasources.value = dsRes.data
    rules.value = rulesRes.data.items
    allChannels.value = chRes.data || []
  } catch { /* ignore */ }
}

function dsName(id: number | undefined) {
  if (!id) return '-'
  return datasources.value.find(d => d.id === id)?.name || `#${id}`
}
function ruleName(id: number) {
  return rules.value.find(r => r.id === id)?.name || `规则 #${id}`
}

function severityLabel(s: string) {
  return { critical: '严重', warning: '警告', info: '提醒' }[s] || s
}
function severityStyle(s: string) {
  const map: Record<string, any> = {
    critical: { background: '#ef4444', color: '#fff', border: 'none' },
    warning: { background: '#f59e0b', color: '#fff', border: 'none' },
    info: { background: '#3b82f6', color: '#fff', border: 'none' },
  }
  return map[s] || map.warning
}
function stateStyle(s: string) {
  const map: Record<string, any> = {
    firing: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    pending: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
    resolved: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
  }
  return map[s] || map.firing
}
function formatNum(v: number) {
  if (v === null || v === undefined) return '-'
  if (Math.abs(v) >= 100) return v.toFixed(2)
  return v.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}
function formatTime(t: string | null | undefined) {
  if (!t) return '-'
  const d = new Date(t)
  return isNaN(d.getTime()) ? '-' : d.toLocaleString()
}
function notifyStatusLabel(s: string) {
  return ({ success: '成功', failed: '失败', throttled: '限流' } as Record<string, string>)[s] || s
}
function notifyStatusStyle(s: string) {
  const m: Record<string, any> = {
    success: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    failed: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    throttled: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
  }
  return m[s] || { background: 'rgba(148,163,184,0.15)', color: '#94a3b8', border: 'none' }
}
function channelName(id: number | undefined) {
  if (!id) return ''
  return allChannels.value.find(c => c.id === id)?.name || `#${id}`
}

async function openDetail(row: AlertInstance) {
  detailRow.value = row
  detailDrawer.value = true
  historyRows.value = []
  notifyLogsRows.value = []
  try {
    const res = await getAlertInstance(row.fingerprint)
    if (res.data.instance) {
      detailRow.value = { ...row, ...res.data.instance }
    }
    historyRows.value = res.data.history || []
    notifyLogsRows.value = res.data.notify_logs || []
  } catch { /* ignore */ }
}

function openSilence(row: AlertInstance) {
  detailRow.value = row
  silenceForm.value = {
    comment: '',
    durationMin: 60,
    matchers: buildDefaultMatchers(row),
  }
  silenceDialog.value = true
}

function buildDefaultMatchers(row: AlertInstance) {
  const ms: Array<{ name: string; op: string; value: string }> = []
  const labels = row.labels || {}
  // 优先 alertname
  if (labels.alertname) ms.push({ name: 'alertname', op: '=', value: labels.alertname })
  // instance / job 是常见聚合维度
  for (const k of ['instance', 'job', 'datasource_name']) {
    if (labels[k]) ms.push({ name: k, op: '=', value: labels[k] })
  }
  if (ms.length === 0 && Object.keys(labels).length > 0) {
    const k = Object.keys(labels)[0]
    ms.push({ name: k, op: '=', value: labels[k] })
  }
  return ms
}

async function handleClearAll() {
  try {
    await ElMessageBox.confirm('确定要清空所有实时告警吗？此操作不可恢复。', '确认清空', {
      confirmButtonText: '确认清空',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await clearAlertInstances()
    ElMessage.success('已清空所有实时告警')
    await fetchData()
  } catch { /* cancelled or error */ }
}

async function handleSilenceSubmit() {
  if (!silenceForm.value.comment.trim()) {
    ElMessage.warning('请填写静默原因')
    return
  }
  const validMatchers = silenceForm.value.matchers.filter(m => m.name && m.value)
  if (validMatchers.length === 0) {
    ElMessage.warning('至少需要一条匹配条件')
    return
  }
  silenceSaving.value = true
  try {
    const now = new Date()
    const ends = new Date(now.getTime() + silenceForm.value.durationMin * 60 * 1000)
    await createAlertSilence({
      comment: silenceForm.value.comment,
      matchers_json: JSON.stringify(validMatchers),
      starts_at: now.toISOString(),
      ends_at: ends.toISOString(),
      enabled: true,
    })
    silenceDialog.value = false
    ElMessage.success('静默创建成功，下一轮调度后生效')
    ElNotification({
      title: '静默已创建',
      message: '点击跳转到静默规则管理',
      type: 'success',
      duration: 6000,
      onClick: () => router.push('/alert-silences'),
    })
    fetchData()
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    silenceSaving.value = false
  }
}

let timer: number | null = null
onMounted(async () => {
  await loadMeta()
  await fetchData()
  await refreshEvaluator()
  timer = window.setInterval(() => {
    fetchData()
    refreshEvaluator()
  }, 30000)
})
onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})
</script>

<style scoped>
.stats-row {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 12px;
  margin-bottom: 16px;
}
.stat-card {
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-elevated);
}
.stat-label {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-bottom: 6px;
}
.stat-value {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
}
.stat-extra {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-top: 4px;
}
.stat-critical .stat-value { color: #ef4444; }
.stat-warning .stat-value { color: #f59e0b; }
.stat-info .stat-value { color: #3b82f6; }
.stat-firing .stat-value { color: #ef4444; }
.stat-pending .stat-value { color: #f59e0b; }

.pager {
  display: flex;
  justify-content: flex-end;
  padding: 12px 4px;
}

.detail {
  font-size: 13px;
}
.detail-section {
  margin-bottom: 18px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--border);
}
.detail-row {
  display: flex;
  align-items: center;
  margin-bottom: 6px;
}
.detail-row .k {
  width: 110px;
  color: var(--text-tertiary);
  font-size: 12px;
}
.detail-row .v {
  color: var(--text-primary);
}
.section-title {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-bottom: 6px;
  text-transform: uppercase;
  letter-spacing: 1px;
}
.detail pre {
  background: rgba(0,0,0,0.3);
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 12px;
  color: var(--text-secondary);
  max-height: 220px;
  overflow: auto;
}
.hist-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
  font-size: 12px;
}
.hist-row .ts { color: var(--text-tertiary); }
.hist-row .vv { color: var(--cyan); margin-left: auto; }
.notify-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 6px 0;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
}
.notify-row .chan { color: var(--text-secondary); font-weight: 500; }
.notify-row .cname { color: var(--text-tertiary); }
.notify-row .cnt { color: var(--cyan); margin-left: auto; }
.notify-row .ts { color: var(--text-tertiary); margin-left: 8px; }
.notify-row .err {
  width: 100%;
  color: #ef4444;
  font-size: 11px;
  padding-left: 4px;
  margin-top: 2px;
  word-break: break-all;
}
</style>
