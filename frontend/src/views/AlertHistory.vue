<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Histogram /></el-icon> 告警历史</h2>
      <p>只展示<b>已恢复</b>的告警实例（同一实例的重复触发/重发合并为一条，完整事件时间线在详情中查看）</p>
    </div>
    <div class="section-card">
      <div class="section-header">
        <div class="action-bar">
          <el-select v-model="rangeHours" placeholder="时间范围" style="width:120px;" @change="onRangeChange">
            <el-option label="最近 1 小时" :value="1" />
            <el-option label="最近 6 小时" :value="6" />
            <el-option label="最近 12 小时" :value="12" />
            <el-option label="最近 24 小时" :value="24" />
            <el-option label="最近 3 天" :value="72" />
            <el-option label="最近 7 天" :value="168" />
          </el-select>
          <el-select v-model="filters.severity" placeholder="告警级别" clearable style="width:120px;" @change="fetchData">
            <el-option label="严重" value="critical" />
            <el-option label="警告" value="warning" />
            <el-option label="提醒" value="info" />
          </el-select>
          <el-select v-model="filters.datasource_id" placeholder="数据源" clearable filterable style="width:160px;" @change="fetchData">
            <el-option v-for="ds in datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
          </el-select>
          <el-select v-model="filters.rule_name" placeholder="规则名称" clearable filterable style="width:180px;" @change="fetchData">
            <el-option v-for="n in ruleNames" :key="n" :label="ruleNameDisplay(n)" :value="n" />
          </el-select>
          <el-input v-model="filters.keyword" placeholder="搜索规则/数据源/标签" style="width:200px;" clearable @keyup.enter="fetchData">
            <template #suffix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>

      <div v-if="loading" style="text-align:center;padding:40px;"><el-icon class="is-loading" :size="24"><Loading /></el-icon></div>
      <div v-else-if="items.length === 0" style="text-align:center;padding:40px;color:var(--text-tertiary);">
        暂无已恢复的告警记录（正在告警 / 未恢复的记录不在此展示）
      </div>

      <div v-else class="event-list">
        <div v-for="row in items" :key="row.fingerprint" class="event-row">
          <div class="event-bar resolved"></div>
          <div class="event-body">
            <div class="event-main">
              <div class="event-title-line">
                <div class="event-title">
                  <span class="event-ds">{{ sessionDs(row) }}</span>
                  <span class="event-divider">/</span>
                  <span class="event-rule">{{ ruleNameDisplay(row.rule_name) }}</span>
                  <el-tag size="small" style="background:rgba(16,185,129,0.15);color:#10b981;border:none;margin-left:8px;">已恢复</el-tag>
                  <el-tag v-if="row.severity" size="small" :style="sevStyle(row.severity)" style="margin-left:6px;">{{ sevLabel(row.severity) }}</el-tag>
                  <el-tag v-if="row.repeat_count > 0" size="small" style="background:rgba(245,158,11,0.15);color:#f59e0b;border:none;margin-left:6px;">
                    重发 {{ row.repeat_count }} 次
                  </el-tag>
                </div>
                <div class="event-times">
                  <div class="time-item">
                    <span class="time-label">首次触发</span>
                    <span class="time-value">{{ fmt(row.first_fired_at) }}</span>
                  </div>
                  <div class="time-item">
                    <span class="time-label">恢复时间</span>
                    <span class="time-value">{{ fmt(row.resolved_at) }}</span>
                  </div>
                  <div v-if="row.duration_sec > 0" class="time-item">
                    <span class="time-label">持续</span>
                    <span class="time-value">{{ durationText(row.duration_sec) }}</span>
                  </div>
                </div>
              </div>
              <div class="event-meta">
                <span v-if="row.value !== undefined" class="meta-val">
                  value={{ formatNum(row.value) }}
                  <span class="meta-threshold">threshold={{ formatNum(row.threshold) }}</span>
                </span>
                <el-tag v-for="(v, k) in safeLabels(row.labels_json)" :key="k" size="small" class="event-label-tag">
                  {{ k }}={{ v }}
                </el-tag>
              </div>
            </div>
            <div class="event-actions">
              <el-button size="small" text style="color: var(--cyan);" @click="openDetail(row)">详情</el-button>
            </div>
          </div>
        </div>
      </div>

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

    <!-- 详情抽屉：该实例的完整事件时间线（含重发） -->
    <el-drawer v-model="detailDrawer" size="600" :title="detailTitle" direction="rtl">
      <div v-if="detailRow" class="detail">
        <div class="detail-row">
          <span class="k">状态</span>
          <el-tag size="small" style="background:rgba(16,185,129,0.15);color:#10b981;border:none;">已恢复</el-tag>
          <el-tag v-if="detailRow.severity" size="small" :style="sevStyle(detailRow.severity)" style="margin-left:6px;">{{ sevLabel(detailRow.severity) }}</el-tag>
        </div>
        <div class="detail-row"><span class="k">规则名称</span><span class="v">{{ ruleNameDisplay(detailRow.rule_name) }}</span></div>
        <div class="detail-row" v-if="sourceName(detailRow.rule_name)">
          <span class="k">告警源</span><span class="v strong" style="color:var(--purple);">{{ sourceName(detailRow.rule_name) }}</span>
        </div>
        <div class="detail-row"><span class="k">数据源</span><span class="v">{{ sessionDs(detailRow) }}</span></div>
        <div class="detail-row"><span class="k">首次触发</span><span class="v">{{ fmt(detailRow.first_fired_at) }}</span></div>
        <div class="detail-row"><span class="k">恢复时间</span><span class="v">{{ fmt(detailRow.resolved_at) }}</span></div>
        <div class="detail-row">
          <span class="k">触发次数</span>
          <span class="v strong" style="color:var(--amber);">{{ detailRow.firing_count }} 次</span>
          <span class="hint" v-if="detailRow.repeat_count > 0">（其中重发 {{ detailRow.repeat_count }} 次）</span>
        </div>
        <div class="detail-row"><span class="k">持续时长</span><span class="v">{{ durationText(detailRow.duration_sec) }}</span></div>

        <div class="detail-section">
          <div class="section-title">完整事件时间线（含重发，共 {{ historyRows.length }} 条）</div>
          <div v-if="historyRows.length === 0" class="empty-hint">暂无事件记录</div>
          <div v-for="(h, idx) in historyRows" :key="h.id" class="hist-row">
            <div class="hist-left">
              <el-tag size="small" :style="eventTypeStyle(h.event_type)">{{ eventTypeLabel(h.event_type) }}</el-tag>
              <span class="ts">{{ fmt(h.occurred_at) }}</span>
              <span v-if="historyRows.length > 1" class="hist-seq">#{{ idx + 1 }}</span>
            </div>
            <div class="hist-metrics">
              <span class="vv">value={{ formatNum(h.value) }}</span>
              <span class="vv-dim">threshold={{ formatNum(h.threshold) }}</span>
            </div>
          </div>
        </div>

        <div class="detail-section">
          <div class="section-title">Labels</div>
          <pre class="kv-block">{{ JSON.stringify(safeLabels(detailRow.labels_json), null, 2) }}</pre>
        </div>
        <div class="detail-section">
          <div class="section-title">Annotations</div>
          <pre class="kv-block">{{ JSON.stringify(safeAnnotations(detailRow.annotations_json), null, 2) }}</pre>
        </div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import dayjs from 'dayjs'
import { getAlertHistorySessions, getAlertInstance, getAlertHistoryRuleNames, getAllDataSources } from '../api'
import type { HistorySession, AlertHistoryRow } from '../types/alerting'
import type { DataSource } from '../types'

const loading = ref(false)
const items = ref<HistorySession[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const datasources = ref<DataSource[]>([])
const ruleNames = ref<string[]>([])
const rangeHours = ref<number>(6)
const filters = ref<{
  severity: string; datasource_id: number | ''; rule_name: string; keyword: string
}>({ severity: '', datasource_id: '', rule_name: '', keyword: '' })

const detailDrawer = ref(false)
const detailRow = ref<HistorySession | null>(null)
const historyRows = ref<AlertHistoryRow[]>([])
const detailTitle = computed(() => detailRow.value ? `已恢复实例 · ${ruleNameDisplay(detailRow.value.rule_name)}` : '告警实例详情')

// 外部告警规则名格式 "[源名] 规则名"：解析源名 / 去掉前缀
function sourceName(ruleName: string): string {
  const m = ruleName.match(/^\[([^\]]+)\]\s*(.*)$/)
  return m ? m[1] : ''
}
function ruleNameDisplay(ruleName: string): string {
  const m = ruleName.match(/^\[([^\]]+)\]\s*(.*)$/)
  return m ? m[2] || ruleName : ruleName
}
// 数据源展示兜底：datasource_name → 规则名解析出的源名 → '-'
function sessionDs(row: HistorySession): string {
  if (row.datasource_name) return row.datasource_name
  const src = sourceName(row.rule_name || '')
  return src || '-'
}

function eventTypeLabel(t: string) {
  return { firing: '触发', resolved: '恢复', pending: '等待' }[t] || t
}
function eventTypeStyle(t: string) {
  const m: Record<string, any> = {
    firing: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    resolved: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    pending: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
  }
  return m[t] || {}
}
function sevStyle(s: string | undefined) {
  const m: Record<string, any> = {
    critical: { background: '#ef4444', color: '#fff', border: 'none' },
    warning: { background: '#f59e0b', color: '#fff', border: 'none' },
    info: { background: '#3b82f6', color: '#fff', border: 'none' },
  }
  return (s && m[s]) || {}
}
function sevLabel(s: string | undefined) {
  return { critical: '严重', warning: '警告', info: '提醒' }[s || ''] || s
}
function safeLabels(s: string | undefined): Record<string, string> {
  if (!s) return {}
  try { const o = JSON.parse(s); return typeof o === 'object' && o !== null ? o : {} }
  catch { return {} }
}
function safeAnnotations(s: string | undefined): Record<string, string> {
  if (!s) return {}
  try { const o = JSON.parse(s); return typeof o === 'object' && o !== null ? o : {} }
  catch { return {} }
}
function formatNum(v: number | null | undefined) {
  if (v === null || v === undefined || isNaN(Number(v))) return '-'
  if (Math.abs(v) >= 100) return Number(v).toFixed(2)
  return Number(v).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}
function fmt(t: string | undefined | null) {
  if (!t) return '-'
  const d = dayjs(t)
  if (!d.isValid()) return '-'
  if (d.year() < 2000) return '-' // 零值时间（0001-01-01/1970）不展示
  return d.format('YYYY-MM-DD HH:mm:ss')
}
function durationText(sec: number) {
  if (!sec || sec <= 0) return '-'
  if (sec < 60) return `${sec} 秒`
  if (sec < 3600) return `${Math.floor(sec / 60)} 分 ${sec % 60} 秒`
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return h >= 24 ? `${Math.floor(h / 24)} 天 ${h % 24} 小时` : `${h} 小时 ${m} 分`
}
function rangeFrom() {
  const d = new Date(Date.now() - rangeHours.value * 3600 * 1000)
  return d.toISOString()
}
function onRangeChange() {
  page.value = 1
  fetchData()
}

async function fetchData() {
  loading.value = true
  try {
    const p: any = { page: page.value, page_size: pageSize.value, from: rangeFrom() }
    if (filters.value.severity) p.severity = filters.value.severity
    if (filters.value.datasource_id !== '') p.datasource_id = filters.value.datasource_id
    if (filters.value.rule_name) p.rule_name = filters.value.rule_name
    if (filters.value.keyword) p.keyword = filters.value.keyword
    const r = await getAlertHistorySessions(p)
    items.value = r.data.items || []
    total.value = r.data.total || 0
  } catch { /* ignore */ }
  finally { loading.value = false }
}

// 详情：加载该实例的完整事件时间线（含重发）
async function openDetail(row: HistorySession) {
  detailRow.value = row
  historyRows.value = []
  detailDrawer.value = true
  try {
    const res = await getAlertInstance(row.fingerprint)
    historyRows.value = res.data.history || []
  } catch { /* ignore */ }
}

onMounted(async () => {
  fetchData()
  try {
    const [dsRes, nameRes] = await Promise.all([getAllDataSources(), getAlertHistoryRuleNames()])
    datasources.value = dsRes.data || []
    ruleNames.value = nameRes.data?.items || []
  } catch { /* ignore */ }
})
</script>

<style scoped>
.event-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 0;
}
.event-row {
  display: flex;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  transition: all .15s ease;
}
.event-row:hover {
  border-color: rgba(16, 185, 129, 0.4);
  box-shadow: 0 2px 10px rgba(0,0,0,0.04);
}
.event-bar {
  width: 4px;
  flex-shrink: 0;
}
.event-bar.resolved { background: #10b981; }

.event-body {
  flex: 1;
  display: flex;
  align-items: center;
  padding: 14px 16px;
  gap: 16px;
  min-width: 0;
}
.event-main {
  flex: 1;
  min-width: 0;
}
.event-title-line {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.event-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  min-width: 0;
}
.event-ds {
  font-size: 13px;
  color: var(--text-secondary);
  font-weight: 500;
}
.event-divider {
  color: var(--text-tertiary);
  font-size: 12px;
  margin: 0 2px;
}
.event-rule {
  font-size: 14px;
  color: var(--text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.event-times {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-shrink: 0;
}
.time-item {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
}
.time-label {
  font-size: 11px;
  color: var(--text-tertiary);
}
.time-value {
  font-size: 12px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
}
.event-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.meta-val {
  font-size: 12px;
  color: var(--cyan);
  font-family: 'SF Mono', Monaco, monospace;
  margin-right: 4px;
}
.meta-threshold {
  color: var(--text-tertiary);
  margin-left: 4px;
}
.event-label-tag {
  background: rgba(99, 102, 241, 0.08);
  color: #818cf8;
  border: none;
  font-family: 'SF Mono', Monaco, monospace;
}
.event-actions {
  flex-shrink: 0;
}

.pager {
  display: flex;
  justify-content: flex-end;
  padding: 12px 4px;
}

.detail {
  font-size: 14px;
}
.detail-row {
  display: flex;
  align-items: center;
  margin-bottom: 12px;
  line-height: 1.5;
}
.detail-row .k {
  width: 90px;
  color: var(--text-tertiary);
  font-size: 13px;
  flex-shrink: 0;
}
.detail-row .v {
  color: var(--text-primary);
  font-size: 14px;
  word-break: break-all;
}
.detail-row .v.strong { font-weight: 600; }
.detail-row .hint { color: var(--text-tertiary); font-size: 12px; margin-left: 8px; }
.detail-section {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}
.section-title {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-bottom: 8px;
  text-transform: uppercase;
  letter-spacing: 1px;
  font-weight: 600;
}
.kv-block {
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-secondary);
  max-height: 240px;
  overflow: auto;
  font-family: 'SF Mono', Monaco, monospace;
  margin-bottom: 16px;
}
.empty-hint {
  color: var(--text-tertiary);
  font-size: 13px;
  padding: 8px 0;
}
.hist-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 7px 0;
  font-size: 13px;
  border-bottom: 1px dashed var(--border);
}
.hist-row:last-child { border-bottom: none; }
.hist-row .hist-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
}
.hist-row .ts { color: var(--text-tertiary); font-size: 12px; min-width: 142px; }
.hist-row .hist-seq {
  color: var(--text-tertiary);
  font-size: 11px;
  font-family: 'SF Mono', Monaco, monospace;
  background: var(--bg-elevated);
  padding: 1px 6px;
  border-radius: 4px;
  min-width: 32px;
  text-align: center;
}
.hist-row .hist-metrics {
  display: grid;
  grid-template-columns: 96px 96px;
  gap: 12px;
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 12px;
  flex-shrink: 0;
}
.hist-row .vv { color: var(--cyan); text-align: right; white-space: nowrap; }
.hist-row .vv-dim { color: var(--text-tertiary); text-align: right; white-space: nowrap; }
</style>
