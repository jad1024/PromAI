<template>
  <div class="page-container log-page">
    <div class="log-hd">
      <div>
        <h2>日志分析</h2>
        <p class="log-sub">VictoriaLogs · LogsQL 检索 · 字段下钻 · 直方图</p>
      </div>
      <el-tag v-if="configured" type="success" size="small" effect="plain">● 数据源已连接</el-tag>
      <el-tag v-else type="danger" size="small" effect="plain">● 数据源未配置</el-tag>
    </div>

    <el-alert v-if="!configured" type="warning" :closable="false" class="log-alert">
      <template #title>
        未配置 VictoriaLogs 数据源。请在容器启动命令里加
        <code>-e PROMAI_VICTORIALOGS_URL=http://你的VictoriaLogs地址:9428</code>
        ，或在 config.yaml 中配置 <code>log_analysis.victorialogs.url</code> 后重启。
      </template>
    </el-alert>

    <!-- 查询栏 -->
    <div class="log-bar">
      <el-select v-model="timeRange" size="small" style="width:120px" @change="onTimeChange">
        <el-option label="最近 15 分钟" value="15m" />
        <el-option label="最近 1 小时" value="1h" />
        <el-option label="最近 6 小时" value="6h" />
        <el-option label="最近 24 小时" value="24h" />
        <el-option label="最近 7 天" value="7d" />
      </el-select>
      <el-input v-model="query" size="small" placeholder="LogsQL，例如：level:error AND service:order-api" clearable
                @keyup.enter="doSearch" style="flex:1" />
      <el-select v-model="step" size="small" style="width:100px">
        <el-option label="1 分钟" value="1m" /><el-option label="5 分钟" value="5m" />
        <el-option label="1 小时" value="1h" /><el-option label="1 天" value="1d" />
      </el-select>
      <el-button type="primary" size="small" :loading="loading" @click="doSearch">查询</el-button>
      <el-button size="small" @click="reset">重置</el-button>
    </div>

    <!-- 快捷过滤（参考 Nightingale 字段侧栏 hover 操作） -->
    <div class="log-quick">
      <span class="log-quick-t">快捷过滤：</span>
      <el-tag v-for="f in quickFilters" :key="f.label" size="small" effect="plain" class="log-quick-tag"
              @click="toggleQuick(f)">{{ f.active ? '✓ ' : '' }}{{ f.label }}</el-tag>
    </div>

    <!-- 直方图 -->
    <div class="log-hist">
      <div class="log-hist-hd">
        <span>所有日志统计</span>
        <span class="log-meta">命中 {{ histTotal }} 条 · 耗时 {{ elapsed }} ms</span>
      </div>
      <div class="log-hist-body" @mousedown.prevent="onBrushStart" @mousemove="onBrushMove"
           @mouseup="onBrushEnd" @mouseleave="onBrushEnd">
        <div v-for="(v, i) in histValues" :key="i" :class="['log-hist-bar', { brushed: inBrush(i) }]"
             :title="`${histTimes[i]} : ${v}`">
          <div class="log-hist-fill" :style="{ height: barHeight(v) + '%' }"></div>
        </div>
        <div v-if="!histValues.length" class="log-empty-hist">暂无数据</div>
      </div>
      <div v-if="customRange" class="log-range-tag">
        已按直方图筛选 {{ fmtTime(customRange.start) }} → {{ fmtTime(customRange.end) }}
        <el-button link size="small" @click="clearBrush">清除筛选</el-button>
      </div>
    </div>

    <div class="log-main">
      <!-- 字段侧栏 -->
      <aside class="log-fields">
        <div class="log-fields-hd">可用字段 <span>({{ fields.length }})</span></div>
        <div v-for="f in fields" :key="f" class="log-field" @click="loadField(f)">
          <span class="log-field-name">{{ f }}</span>
          <span v-if="fieldLoading === f" class="log-field-spin">…</span>
        </div>
        <div v-if="fieldValues.length" class="log-field-panel">
          <div class="log-field-panel-hd">
            {{ activeField }} Top 值
            <el-button link size="small" @click="fieldValues = []">×</el-button>
          </div>
          <div v-for="v in fieldValues" :key="v.value" class="log-fv" @click="addFilter(activeField, v.value)">
            <span class="log-fv-v" :title="v.value">{{ v.value }}</span>
            <span class="log-fv-c">{{ v.count }}</span>
          </div>
        </div>
      </aside>

      <!-- 日志表格 -->
      <div class="log-results">
        <div class="log-res-hd">
          <span>日志列表（{{ logs.length }}）</span>
          <el-checkbox v-model="wrap" size="small">换行</el-checkbox>
          <el-checkbox v-model="showTime" size="small">日志时间</el-checkbox>
          <el-select v-model="view" size="small" style="width:100px">
            <el-option label="原始" value="raw" /><el-option label="表格" value="table" />
          </el-select>
        </div>
        <el-table :data="logs" size="small" border v-loading="loading" max-height="520" row-key="_rowKey">
          <el-table-column v-if="showTime" prop="_time" label="时间" width="190" />
          <el-table-column prop="level" label="级别" width="80">
            <template #default="{ row }">
              <el-tag v-if="row.level" size="small" :type="levelTag(row.level)">{{ row.level }}</el-tag>
              <span v-else>—</span>
            </template>
          </el-table-column>
          <el-table-column prop="_msg" label="日志内容" min-width="420">
            <template #default="{ row }">
              <div :class="wrap ? 'log-msg-wrap' : 'log-msg'">{{ row._msg || JSON.stringify(row) }}</div>
              <div class="log-row-actions">
                <el-button v-if="traceOf(row)" link type="primary" size="small"
                           @click="goTrace(traceOf(row))">traceId: {{ traceOf(row) }} → 链路</el-button>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="70">
            <template #default="{ row }">
              <el-button link size="small" @click="showDetail(row)">详情</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="log-pager">
          <el-button size="small" :disabled="offset === 0" @click="prevPage">上一页</el-button>
          <span>第 {{ Math.floor(offset / limit) + 1 }} 页</span>
          <el-button size="small" :disabled="logs.length < limit" @click="nextPage">下一页</el-button>
        </div>
      </div>
    </div>

    <el-drawer v-model="detailOpen" title="日志详情" size="46%">
      <pre class="log-detail-json">{{ JSON.stringify(detailRow, null, 2) }}</pre>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  getLogStatus, queryLogs, getLogHits, getLogFieldValues,
  type LogEntry, type LogFieldValue,
} from '../api'

const router = useRouter()
const configured = ref(false)
const loading = ref(false)
const query = ref('*')
const timeRange = ref('1h')
const step = ref('5m')
const limit = ref(100)
const offset = ref(0)
const logs = ref<LogEntry[]>([])
const fields = ref<string[]>([])
const histTimes = ref<string[]>([])
const histValues = ref<number[]>([])
const histTotal = ref(0)
const customRange = ref<{ start: string; end: string } | null>(null)
const brushStart = ref<number | null>(null)
const brushEnd = ref<number | null>(null)
const elapsed = ref(0)
const wrap = ref(true)
const showTime = ref(true)
const view = ref('raw')
const activeField = ref('')
const fieldValues = ref<LogFieldValue[]>([])
const fieldLoading = ref('')
const detailOpen = ref(false)
const detailRow = ref<any>(null)

const quickFilters = ref([
  { label: 'ERROR', q: 'level:error', active: false },
  { label: 'WARN', q: 'level:warn', active: false },
  { label: '有 traceId', q: 'traceId:*', active: false },
])
const activeQuicks = computed(() => quickFilters.value.filter(f => f.active).map(f => f.q))

function levelTag(level: string) {
  const l = String(level).toLowerCase()
  if (l.includes('error') || l === 'fatal') return 'danger'
  if (l.includes('warn')) return 'warning'
  if (l.includes('info')) return 'success'
  return 'info'
}

function barHeight(v: number) {
  const max = Math.max(...histValues.value, 1)
  return Math.round((v / max) * 100)
}

// 直方图刷选：按住鼠标横向拖动选择时间区间，松开后按绝对时间重新检索。
function barIndex(e: MouseEvent) {
  const n = histValues.value.length
  if (!n) return 0
  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
  return Math.max(0, Math.min(n - 1, Math.floor((e.clientX - rect.left) / rect.width * n)))
}
function onBrushStart(e: MouseEvent) {
  brushStart.value = barIndex(e)
  brushEnd.value = brushStart.value
}
function onBrushMove(e: MouseEvent) {
  if (brushStart.value !== null) brushEnd.value = barIndex(e)
}
function onBrushEnd() {
  if (brushStart.value === null || brushEnd.value === null) return
  const a = Math.min(brushStart.value, brushEnd.value)
  const b = Math.max(brushStart.value, brushEnd.value)
  brushStart.value = null
  brushEnd.value = null
  if (a === b) return // 单击不筛选
  const start = histTimes.value[a]
  const end = histTimes.value[b + 1] || new Date().toISOString()
  if (!start) return
  customRange.value = { start, end }
  offset.value = 0
  doSearch()
}
function inBrush(i: number) {
  if (brushStart.value === null || brushEnd.value === null) return false
  return i >= Math.min(brushStart.value, brushEnd.value) && i <= Math.max(brushStart.value, brushEnd.value)
}
function fmtTime(s: string) { return s.length >= 19 ? s.slice(11, 19) : s }
function clearBrush() { customRange.value = null; offset.value = 0; doSearch() }
function timeParams() {
  return customRange.value
    ? { start: customRange.value.start, end: customRange.value.end }
    : { start: timeRange.value }
}

function traceOf(row: LogEntry) {
  for (const k of ['traceId', 'trace_id', 'traceID', 'requestId', 'request_id']) {
    if (row[k]) return String(row[k])
  }
  return ''
}

function toggleQuick(f: { active: boolean }) { f.active = !f.active; offset.value = 0; doSearch() }
function addFilter(field: string, value: string) {
  const part = `${field}:"${value}"`
  if (!query.value.includes(part)) query.value = query.value === '*' ? part : `${query.value} AND ${part}`
  offset.value = 0
  doSearch()
}
function onTimeChange() { customRange.value = null; offset.value = 0; doSearch() }
function reset() {
  query.value = '*'; timeRange.value = '1h'; customRange.value = null; offset.value = 0
  quickFilters.value.forEach(f => f.active = false)
  doSearch()
}
function prevPage() { offset.value = Math.max(0, offset.value - limit.value); doSearch() }
function nextPage() { offset.value += limit.value; doSearch() }
function showDetail(row: LogEntry) { detailRow.value = row; detailOpen.value = true }
function goTrace(id: string) { router.push({ path: '/trace-analysis', query: { id } }) }

function buildQuery() {
  const parts = [query.value || '*', ...activeQuicks.value]
  return [...new Set(parts)].join(' AND ')
}

async function doSearch() {
  if (!configured.value) return
  loading.value = true
  const q = buildQuery()
  try {
    const tp = timeParams()
    const [r, h] = await Promise.all([
      queryLogs({ q, ...tp, limit: limit.value, offset: offset.value }),
      getLogHits({ q, ...tp, step: step.value }),
    ])
    logs.value = (r.data.logs || []).map((x, i) => ({ _rowKey: `${Date.now()}-${offset.value + i}`, ...x }))
    fields.value = r.data.fields || []
    elapsed.value = r.data.elapsed_ms || 0
    histTimes.value = h.data.timestamps || []
    histValues.value = h.data.values || []
    histTotal.value = h.data.total || 0
  } catch (e: any) {
    ElMessage.error('日志查询失败: ' + (e?.response?.data?.error || e.message))
    logs.value = []; histValues.value = []
  } finally {
    loading.value = false
  }
}

async function loadField(f: string) {
  activeField.value = f
  fieldLoading.value = f
  try {
    const r = await getLogFieldValues({ q: buildQuery(), field: f, ...timeParams(), limit: 15 })
    fieldValues.value = r.data.values || []
  } catch { fieldValues.value = [] }
  finally { fieldLoading.value = '' }
}

onMounted(async () => {
  try {
    configured.value = (await getLogStatus()).data.configured
  } catch { configured.value = false }
  if (configured.value) await doSearch()
})
</script>

<style scoped>
.log-page { padding: 16px 20px; }
.log-hd { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.log-hd h2 { margin: 0; font-size: 19px; font-weight: 600; }
.log-sub { margin: 3px 0 0; color: var(--text-tertiary); font-size: 12px; }
.log-alert { margin-bottom: 12px; }
.log-alert code { background: rgba(0,0,0,.06); padding: 1px 5px; border-radius: 3px; }
.log-bar { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; }
.log-quick { display: flex; gap: 6px; align-items: center; margin-bottom: 10px; }
.log-quick-t { font-size: 12px; color: var(--text-tertiary); }
.log-quick-tag { cursor: pointer; user-select: none; }
.log-hist { border: 1px solid var(--border); border-radius: 6px; padding: 10px 12px; margin-bottom: 12px; background: var(--bg-card); }
.log-hist-hd { display: flex; justify-content: space-between; font-size: 12px; color: var(--text-secondary); margin-bottom: 8px; }
.log-meta { color: var(--text-tertiary); }
.log-hist-body { height: 90px; display: flex; align-items: flex-end; gap: 2px; cursor: crosshair; user-select: none; }
.log-hist-bar { flex: 1; height: 100%; display: flex; align-items: flex-end; min-width: 2px; }
.log-hist-fill { width: 100%; background: linear-gradient(180deg, #7c3aed, #a78bfa); border-radius: 2px 2px 0 0; min-height: 1px; opacity: .75; }
.log-hist-bar:hover .log-hist-fill { opacity: 1; }
.log-hist-bar.brushed .log-hist-fill { background: linear-gradient(180deg, #f59e0b, #fbbf24); opacity: 1; }
.log-range-tag { margin-top: 8px; font-size: 12px; color: var(--primary, #6d4bc4); display: flex; align-items: center; gap: 6px; }
.log-empty-hist { width: 100%; text-align: center; color: var(--text-tertiary); font-size: 12px; padding-bottom: 30px; }
.log-main { display: flex; gap: 12px; align-items: flex-start; }
.log-fields { width: 190px; flex-shrink: 0; border: 1px solid var(--border); border-radius: 6px; background: var(--bg-card); font-size: 12px; max-height: 580px; overflow: auto; }
.log-fields-hd { padding: 9px 10px; border-bottom: 1px solid var(--border); font-weight: 600; }
.log-fields-hd span { color: var(--text-tertiary); font-weight: 400; }
.log-field { padding: 6px 10px; cursor: pointer; display: flex; justify-content: space-between; }
.log-field:hover { background: var(--bg-elevated); }
.log-field-name { font-family: monospace; word-break: break-all; }
.log-field-spin { color: var(--text-tertiary); }
.log-field-panel { border-top: 1px solid var(--border); padding: 8px; }
.log-field-panel-hd { display: flex; justify-content: space-between; font-weight: 600; margin-bottom: 5px; }
.log-fv { display: flex; justify-content: space-between; padding: 4px; cursor: pointer; border-radius: 3px; }
.log-fv:hover { background: var(--bg-elevated); }
.log-fv-v { max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: monospace; }
.log-fv-c { color: var(--text-tertiary); }
.log-results { flex: 1; min-width: 0; }
.log-res-hd { display: flex; gap: 14px; align-items: center; margin-bottom: 8px; font-size: 12px; color: var(--text-secondary); }
.log-msg { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-family: monospace; font-size: 12px; }
.log-msg-wrap { white-space: pre-wrap; word-break: break-all; font-family: monospace; font-size: 12px; }
.log-row-actions { margin-top: 2px; }
.log-pager { display: flex; gap: 10px; align-items: center; justify-content: flex-end; margin-top: 8px; font-size: 12px; }
.log-detail-json { font-size: 12px; white-space: pre-wrap; word-break: break-all; }
</style>
