<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Files /></el-icon> 告警事件聚合</h2>
      <p>
        把原始告警流按「<b>规则 + 集群 + 时间窗</b>」合并成能看懂的事件，用于噪音治理与 AI 分析降噪。
        通知层面的分组/去重/抑制由 Alertmanager 负责，<b>本页不参与任何通知链路</b>。
      </p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <div class="action-bar">
          <el-select v-model="hours" style="width:130px;" @change="fetchAll">
            <el-option label="最近 1 小时" :value="1" />
            <el-option label="最近 6 小时" :value="6" />
            <el-option label="最近 12 小时" :value="12" />
            <el-option label="最近 24 小时" :value="24" />
            <el-option label="最近 3 天" :value="72" />
            <el-option label="最近 7 天" :value="168" />
          </el-select>
          <el-select v-model="filters.severity" placeholder="级别" clearable style="width:110px;" @change="fetchAll">
            <el-option label="严重" value="critical" />
            <el-option label="警告" value="warning" />
            <el-option label="提醒" value="info" />
          </el-select>
          <el-select v-model="filters.datasource_id" placeholder="集群/数据源" clearable filterable style="width:160px;" @change="fetchAll">
            <el-option v-for="ds in datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
          </el-select>
          <el-select v-model="sortBy" style="width:130px;" @change="applySort">
            <el-option label="按最近活动" value="recent" />
            <el-option label="按严重度优先" value="severity" />
            <el-option label="按触发次数" value="firing" />
          </el-select>
          <el-input v-model="filters.keyword" placeholder="搜索规则/集群/标签" style="width:190px;" clearable @keyup.enter="fetchAll">
            <template #suffix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-button plain @click="fetchAll"><el-icon><Refresh /></el-icon></el-button>
          <div class="auto-refresh">
            <span class="ar-label">自动刷新</span>
            <el-switch v-model="autoRefresh" size="small" @change="toggleAutoRefresh" />
          </div>
        </div>
        <div v-if="lastUpdated" class="updated-at">更新于 {{ lastUpdated }}</div>
      </div>

      <!-- 聚合效果概览 -->
      <div class="stat-cards">
        <div class="s-card">
          <div class="s-icon raw"><el-icon><Bell /></el-icon></div>
          <div class="s-body">
            <div class="s-num">{{ agg.total_raw }}</div>
            <div class="s-label">原始告警条数</div>
            <div class="s-sub">时间窗 {{ hours }}h</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon evt"><el-icon><Files /></el-icon></div>
          <div class="s-body">
            <div class="s-num cyan">{{ agg.total_events }}</div>
            <div class="s-label">聚合后事件数</div>
            <div class="s-sub">含持续中 {{ ongoingCount }}</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon comp"><el-icon><TrendCharts /></el-icon></div>
          <div class="s-body">
            <div class="s-num green">-{{ agg.compression }}%</div>
            <div class="s-label">压缩率</div>
            <div class="s-sub">约 {{ compressionRatio }}× 降噪</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon flap"><el-icon><WarningFilled /></el-icon></div>
          <div class="s-body">
            <div class="s-num amber">{{ flapEventCount }}</div>
            <div class="s-label">震荡事件</div>
            <div class="s-sub">{{ flapPct }}% 的事件</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon corr"><el-icon><Connection /></el-icon></div>
          <div class="s-body">
            <div class="s-num indigo">{{ correlatedEventCount }}</div>
            <div class="s-label">跨集群关联</div>
            <div class="s-sub">时间重叠提示</div>
          </div>
        </div>
      </div>

      <!-- 事件时间轴 -->
      <div v-if="events.length > 0" class="chart-panel">
        <div class="panel-title">
          <span>事件时间轴</span>
          <span class="panel-hint">每条横杠 = 一个事件（首次触发 → 最近活动，持续中延伸至当前）</span>
        </div>
        <div ref="tlEl" class="chart" :style="{ height: tlHeight + 'px' }"></div>
        <div class="tl-legend">
          <span class="lg"><i class="dot critical"></i>严重</span>
          <span class="lg"><i class="dot warning"></i>警告</span>
          <span class="lg"><i class="dot info"></i>提醒</span>
          <span class="lg"><i class="dot flap"></i>震荡（描边）</span>
        </div>
      </div>

      <!-- TOP 噪音排行 -->
      <div v-if="noiseItems.length > 0" class="chart-panel">
        <div class="panel-title">
          <span>TOP 噪音规则</span>
          <span class="panel-hint">触发次数最多 / 震荡最频繁的规则，优先去 Alertmanager 调阈值或静默</span>
        </div>
        <div ref="noiseEl" class="chart" :style="{ height: noiseHeight + 'px' }"></div>
      </div>

      <!-- 事件列表 -->
      <div v-if="loading" class="list-state">
        <el-icon class="is-loading" :size="24"><Loading /></el-icon>
      </div>
      <div v-else-if="events.length === 0" class="list-state empty">
        <el-icon :size="36"><Bell /></el-icon>
        <p>所选时间窗内没有告警记录</p>
      </div>

      <div v-else class="event-list">
        <div
          v-for="(ev, i) in events"
          :key="ev.rule_id + '-' + ev.datasource_id + '-' + i"
          class="event-row"
          :class="{ ongoing: ev.state === 'ongoing', expanded: isExpanded(ev, i) }"
          @click="toggleExpand(ev, i)"
        >
          <div class="event-bar"></div>
          <div class="event-body">
            <div class="event-main">
              <div class="event-title-line">
                <div class="event-title">
                  <span class="event-ds">{{ ev.datasource_name || '-' }}</span>
                  <span class="event-divider">/</span>
                  <span class="event-rule" :title="ev.rule_name">{{ ruleNameDisplay(ev.rule_name) }}</span>
                  <el-tag v-if="ev.severity" size="small" :style="sevStyle(ev.severity)">{{ sevLabel(ev.severity) }}</el-tag>
                  <el-tag size="small" :class="ev.state === 'ongoing' ? 'tag-ongoing' : 'tag-resolved'">
                    {{ ev.state === 'ongoing' ? '持续中 · 已持续 ' + fmtDur(ev, true) : '已恢复 · 持续 ' + fmtDur(ev, false) }}
                  </el-tag>
                  <el-tag v-if="ev.flapping" size="small" class="tag-flap">震荡 x{{ ev.flap_count }}</el-tag>
                </div>
                <div class="event-times">
                  <div class="time-item">
                    <span class="time-label">首次触发</span>
                    <span class="time-value">{{ fmt(ev.first_fired_at) }}</span>
                  </div>
                  <div class="time-item">
                    <span class="time-label">最近活动</span>
                    <span class="time-value">{{ fmt(ev.last_event_at) }}</span>
                  </div>
                  <el-icon class="expand-caret" :class="{ open: isExpanded(ev, i) }"><CaretBottom /></el-icon>
                </div>
              </div>
              <div class="event-meta">
                <span class="meta-val" :class="{ over: peakRatio(ev) > 1 }">
                  peak {{ formatNum(ev.peak_value) }}
                  <span class="meta-threshold">/ threshold {{ formatNum(ev.threshold) }}</span>
                </span>
                <el-tag v-if="peakRatio(ev) > 1" size="small" class="tag-over">峰值超阈值 {{ peakRatio(ev).toFixed(1) }}×</el-tag>
                <el-tag size="small" class="tag-count">原始 {{ ev.raw_count }} 条</el-tag>
                <el-tag size="small" class="tag-count">触发 {{ ev.firing_count }} 次</el-tag>
                <el-tag
                  v-for="ds in ev.correlated_datasources || []"
                  :key="ds"
                  size="small"
                  class="tag-correlated"
                >跨集群关联 · {{ ds }}</el-tag>
              </div>
            </div>

            <!-- 展开详情 -->
            <el-collapse-transition>
              <div v-show="isExpanded(ev, i)" class="event-detail" @click.stop>
                <div class="detail-grid">
                  <div class="d-label">规则全名</div>
                  <div class="d-value mono">{{ ev.rule_name || '-' }}</div>
                  <div class="d-label">集群/数据源</div>
                  <div class="d-value">{{ ev.datasource_name || '-' }}</div>
                  <div class="d-label">时间范围</div>
                  <div class="d-value mono">{{ fmt(ev.first_fired_at) }} ~ {{ fmt(ev.last_event_at) }}</div>
                  <div class="d-label">聚合明细</div>
                  <div class="d-value">
                    {{ ev.raw_count }} 条原始历史合并为 1 个事件；其中触发评估 {{ ev.firing_count }} 次，
                    触发↔恢复来回 {{ ev.flap_count }} 次
                  </div>
                </div>
                <div v-if="ev.flapping" class="detail-hint warn">
                  ⚡ 该规则反复触发↔恢复 {{ ev.flap_count }} 次，属于震荡噪音。建议在 Alertmanager 提高规则的
                  <b>for（持续时长）</b>、放宽阈值，或对非工作时间添加静默。
                </div>
                <div v-if="(ev.correlated_datasources || []).length > 0" class="detail-hint info">
                  🔗 同一规则在 {{ ev.correlated_datasources.join('、') }} 的时间窗与本事件重叠，
                  可能存在跨集群关联（共享上游依赖、网络等），也可能只是巧合；可在告警 AI 根因分析中确认是否同源。
                </div>
              </div>
            </el-collapse-transition>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import * as echarts from 'echarts'
import dayjs from 'dayjs'
import { ElMessage } from 'element-plus'
import { getAlertEvents, getAlertNoiseTop, getAllDataSources } from '../api'
import type { AlertEvent, NoiseTopRule } from '../types/alerting'
import type { DataSource } from '../types'

const SEV_COLORS: Record<string, string> = {
  critical: '#ef4444',
  warning: '#f59e0b',
  info: '#3b82f6',
}

const loading = ref(false)
const events = ref<AlertEvent[]>([])
const noiseItems = ref<NoiseTopRule[]>([])
const datasources = ref<DataSource[]>([])
const hours = ref<number>(24)
const sortBy = ref<'recent' | 'severity' | 'firing'>('recent')
const autoRefresh = ref(false)
const lastUpdated = ref('')
const agg = ref<{ total_raw: number; total_events: number; compression: number }>({
  total_raw: 0, total_events: 0, compression: 0,
})
const filters = ref<{ severity: string; datasource_id: number | ''; keyword: string }>({
  severity: '', datasource_id: '', keyword: '',
})
const expandedKeys = ref<Set<string>>(new Set())

// 图表实例
const tlEl = ref<HTMLElement>()
const noiseEl = ref<HTMLElement>()
let tlChart: echarts.ECharts | null = null
let noiseChart: echarts.ECharts | null = null
let refreshTimer: number | null = null

const ongoingCount = computed(() => events.value.filter(e => e.state === 'ongoing').length)
const flapEventCount = computed(() => events.value.filter(e => e.flapping).length)
const correlatedEventCount = computed(() => events.value.filter(e => (e.correlated_datasources || []).length > 0).length)
const flapPct = computed(() => {
  const n = events.value.length
  return n > 0 ? Math.round((flapEventCount.value / n) * 100) : 0
})
const compressionRatio = computed(() => {
  if (agg.value.total_events > 0) return Math.round((agg.value.total_raw / agg.value.total_events) * 10) / 10
  return 0
})
// 时间轴最多展示 25 条保证可读性
const tlEvents = computed(() => events.value.slice(0, 25))
const tlHeight = computed(() => Math.max(140, tlEvents.value.length * 26 + 46))
const noiseHeight = computed(() => noiseItems.value.length * 30 + 40)

function eventKey(ev: AlertEvent, i: number) {
  return `${ev.rule_id}-${ev.datasource_id}-${i}`
}
function isExpanded(ev: AlertEvent, i: number) {
  return expandedKeys.value.has(eventKey(ev, i))
}
function toggleExpand(ev: AlertEvent, i: number) {
  const k = eventKey(ev, i)
  const s = new Set(expandedKeys.value)
  if (s.has(k)) s.delete(k)
  else s.add(k)
  expandedKeys.value = s
}

// 外部告警规则名格式 "[源名] 规则名"：去掉前缀，只展示规则名
function ruleNameDisplay(ruleName: string): string {
  const m = ruleName.match(/^\[([^\]]+)\]\s*(.*)$/)
  return m ? m[2] || ruleName : ruleName
}
function sevStyle(s: string | undefined) {
  const m: Record<string, any> = {
    critical: { background: SEV_COLORS.critical, color: '#fff', border: 'none' },
    warning: { background: SEV_COLORS.warning, color: '#fff', border: 'none' },
    info: { background: SEV_COLORS.info, color: '#fff', border: 'none' },
  }
  return (s && m[s]) || {}
}
function sevLabel(s: string | undefined) {
  return ({ critical: '严重', warning: '警告', info: '提醒' } as Record<string, string>)[s || ''] || s
}
function formatNum(v: number | null | undefined) {
  if (v === null || v === undefined || isNaN(Number(v))) return '-'
  if (Math.abs(Number(v)) >= 100) return Number(v).toFixed(2)
  return Number(v).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}
function fmt(t: string | undefined | null) {
  if (!t) return '-'
  const d = dayjs(t)
  if (!d.isValid() || d.year() < 2000) return '-'
  return d.format('MM-DD HH:mm:ss')
}
function peakRatio(ev: AlertEvent): number {
  const th = Number(ev.threshold)
  if (!th || isNaN(th)) return 0
  return Number(ev.peak_value) / th
}
function fmtDur(ev: AlertEvent, ongoing: boolean): string {
  const start = dayjs(ev.first_fired_at)
  if (!start.isValid() || start.year() < 2000) return '-'
  const end = ongoing ? dayjs() : dayjs(ev.last_event_at)
  let ms = end.diff(start)
  if (ms < 0) ms = 0
  const m = Math.floor(ms / 60000)
  if (m < 1) return '<1m'
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d${h % 24}h`
}

function applySort() {
  const sevOrder: Record<string, number> = { critical: 3, warning: 2, info: 1 }
  const list = [...events.value]
  if (sortBy.value === 'severity') {
    list.sort((a, b) =>
      (sevOrder[b.severity] || 0) - (sevOrder[a.severity] || 0) ||
      dayjs(b.last_event_at).valueOf() - dayjs(a.last_event_at).valueOf())
  } else if (sortBy.value === 'firing') {
    list.sort((a, b) => b.firing_count - a.firing_count || b.raw_count - a.raw_count)
  } else {
    list.sort((a, b) => dayjs(b.last_event_at).valueOf() - dayjs(a.last_event_at).valueOf())
  }
  events.value = list
}

function themeColors() {
  const t = document.documentElement.getAttribute('data-theme')
  const isLight = t !== 'dark' && t !== 'cyber'
  return {
    axis: isLight ? '#64748b' : '#94a3b8',
    split: isLight ? 'rgba(59,130,246,0.12)' : 'rgba(56,189,248,0.1)',
  }
}

// ===== 事件时间轴（甘特式 custom series） =====
function renderTimeline() {
  if (!tlEl.value || tlEvents.value.length === 0) return
  if (!tlChart) tlChart = echarts.init(tlEl.value)
  const { axis, split } = themeColors()
  const now = Date.now()

  const labels = tlEvents.value.map(ev => {
    const name = ruleNameDisplay(ev.rule_name) || ev.rule_name
    const ds = (ev.datasource_name || '-').slice(0, 12)
    const short = name.length > 26 ? name.slice(0, 26) + '…' : name
    return `${ds} · ${short}`
  })

  const data = tlEvents.value.map((ev, i) => {
    const start = dayjs(ev.first_fired_at).valueOf()
    const last = dayjs(ev.last_event_at).valueOf()
    // 持续中的事件延伸到当前时刻
    const end = ev.state === 'ongoing' ? Math.max(last, now) : last
    const color = SEV_COLORS[ev.severity] || '#6366f1'
    return {
      value: [i, start, end],
      itemStyle: {
        fill: color,
        opacity: ev.state === 'ongoing' ? 0.9 : 0.55,
        stroke: ev.flapping ? '#f59e0b' : 'transparent',
        lineWidth: ev.flapping ? 1.5 : 0,
      },
    }
  })

  tlChart.setOption({
    backgroundColor: 'transparent',
    animation: true,
    tooltip: {
      trigger: 'item',
      formatter: (p: any) => {
        const ev = tlEvents.value[p.dataIndex]
        if (!ev) return ''
        const rows = [
          `<b>${esc(ev.rule_name)}</b>`,
          `集群：${esc(ev.datasource_name || '-')} · ${sevLabel(ev.severity)} · ${ev.state === 'ongoing' ? '持续中' : '已恢复'}`,
          `首末：${fmt(ev.first_fired_at)} ~ ${fmt(ev.last_event_at)}`,
          `触发 ${ev.firing_count} 次 / 原始 ${ev.raw_count} 条` + (ev.flapping ? ` / 震荡 x${ev.flap_count}` : ''),
          `peak ${formatNum(ev.peak_value)} / threshold ${formatNum(ev.threshold)}`,
        ]
        if ((ev.correlated_datasources || []).length > 0) {
          rows.push(`跨集群关联：${ev.correlated_datasources.join('、')}`)
        }
        return rows.join('<br/>')
      },
    },
    grid: { left: 190, right: 24, top: 8, bottom: 30 },
    xAxis: {
      type: 'time',
      axisLabel: { color: axis, fontSize: 11, hideOverlap: true },
      axisLine: { lineStyle: { color: split } },
      splitLine: { lineStyle: { color: split } },
    },
    yAxis: {
      type: 'category',
      data: labels,
      inverse: true,
      axisTick: { show: false },
      axisLine: { lineStyle: { color: split } },
      axisLabel: { color: axis, fontSize: 11, width: 182, overflow: 'truncate' },
    },
    series: [{
      type: 'custom',
      encode: { x: [1, 2], y: 0 },
      renderItem: (params: any, api: any) => {
        const cat = api.value(0)
        const startPt = api.coord([api.value(1), cat])
        const endPt = api.coord([api.value(2), cat])
        const barH = api.size([0, 1])[1] * 0.42
        const rect = echarts.graphic.clipRectByRect({
          x: startPt[0],
          y: startPt[1] - barH / 2,
          width: Math.max(endPt[0] - startPt[0], 3),
          height: barH,
        }, {
          x: params.coordSys.x, y: params.coordSys.y,
          width: params.coordSys.width, height: params.coordSys.height,
        })
        return rect && {
          type: 'rect',
          shape: { ...rect, r: 3 },
          style: api.style(),
        }
      },
      data,
    }],
  }, true)
}

function esc(s: string | undefined | null) {
  return (s || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// ===== TOP 噪音排行（横向条形图） =====
function renderNoise() {
  if (!noiseEl.value || noiseItems.value.length === 0) return
  if (!noiseChart) noiseChart = echarts.init(noiseEl.value)
  const { axis, split } = themeColors()
  const items = noiseItems.value

  const labels = items.map(n => {
    const name = ruleNameDisplay(n.rule_name) || n.rule_name
    const ds = (n.datasource_name || '-').slice(0, 12)
    const short = name.length > 24 ? name.slice(0, 24) + '…' : name
    return `${ds} · ${short}`
  })
  // 噪音排行：值小的放上面（rank 1 在顶部）
  labels.reverse()
  const counts = [...items].map(n => n.firing_count).reverse()
  const meta = [...items].reverse()

  noiseChart.setOption({
    backgroundColor: 'transparent',
    animation: true,
    tooltip: {
      trigger: 'item',
      formatter: (p: any) => {
        const n = meta[p.dataIndex]
        if (!n) return ''
        return [
          `<b>${esc(n.rule_name)}</b>`,
          `集群：${esc(n.datasource_name || '-')}`,
          `触发 ${n.firing_count} 次 / 原始 ${n.raw_count} 条` + (n.flapping ? ` / 震荡 x${n.flap_count}` : ''),
          n.flapping ? '⚠️ 震荡规则，建议调阈值或静默' : '',
        ].filter(Boolean).join('<br/>')
      },
    },
    grid: { left: 190, right: 46, top: 6, bottom: 6 },
    xAxis: { type: 'value', show: false },
    yAxis: {
      type: 'category',
      data: labels,
      axisTick: { show: false },
      axisLine: { show: false },
      axisLabel: { color: axis, fontSize: 11, width: 182, overflow: 'truncate' },
    },
    series: [{
      type: 'bar',
      barWidth: 12,
      itemStyle: { borderRadius: 6 },
      label: {
        show: true, position: 'right', color: axis, fontSize: 11,
        fontFamily: 'SF Mono, Monaco, monospace',
        formatter: (p: any) => `${p.value} 次`,
      },
      data: counts.map((c, i) => ({
        value: c,
        itemStyle: { color: meta[i].flapping ? '#f59e0b' : '#ef4444' },
      })),
    }],
  }, true)
}

function renderCharts() {
  nextTick(() => {
    renderTimeline()
    renderNoise()
  })
}

function onResize() {
  tlChart?.resize()
  noiseChart?.resize()
}

async function fetchEvents() {
  loading.value = true
  try {
    const p: any = { hours: hours.value, limit: 100 }
    if (filters.value.severity) p.severity = filters.value.severity
    if (filters.value.datasource_id !== '') p.datasource_id = filters.value.datasource_id
    if (filters.value.keyword) p.keyword = filters.value.keyword
    const r = await getAlertEvents(p)
    events.value = r.data.events || []
    agg.value = {
      total_raw: r.data.total_raw || 0,
      total_events: r.data.total_events || 0,
      compression: r.data.compression || 0,
    }
    applySort()
  } catch {
    ElMessage.error('加载事件聚合数据失败')
  } finally {
    loading.value = false
  }
}

async function fetchNoiseTop() {
  try {
    const r = await getAlertNoiseTop({ hours: hours.value, limit: 8 })
    noiseItems.value = r.data.items || []
  } catch { /* 噪音排行失败不阻塞主列表 */ }
}

async function fetchAll() {
  await Promise.all([fetchEvents(), fetchNoiseTop()])
  lastUpdated.value = dayjs().format('HH:mm:ss')
  expandedKeys.value = new Set()
  renderCharts()
}

function toggleAutoRefresh(on: boolean) {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  if (on) refreshTimer = window.setInterval(fetchAll, 30000)
}

onMounted(async () => {
  try {
    const ds = await getAllDataSources()
    datasources.value = ds.data || []
  } catch { /* ignore */ }
  await fetchAll()
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  toggleAutoRefresh(false)
  window.removeEventListener('resize', onResize)
  tlChart?.dispose()
  noiseChart?.dispose()
  tlChart = null
  noiseChart = null
})
</script>

<style scoped>
.action-bar { flex-wrap: wrap; gap: 8px; }
.updated-at {
  font-size: 12px;
  color: var(--text-tertiary);
  font-family: 'SF Mono', Monaco, monospace;
  white-space: nowrap;
}
.auto-refresh {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: 6px;
}
.ar-label { font-size: 12px; color: var(--text-tertiary); }

/* ===== 概览卡片 ===== */
.stat-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
  padding: 14px 0;
  border-bottom: 1px solid var(--border);
}
.s-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  background: var(--bg-elevated, var(--bg-card));
  border: 1px solid var(--border);
  border-radius: 10px;
}
.s-icon {
  width: 38px;
  height: 38px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: 18px;
}
.s-icon.raw { background: rgba(148,163,184,0.15); color: #94a3b8; }
.s-icon.evt { background: rgba(0,212,255,0.12); color: var(--cyan); }
.s-icon.comp { background: rgba(16,185,129,0.12); color: #10b981; }
.s-icon.flap { background: rgba(245,158,11,0.12); color: #f59e0b; }
.s-icon.corr { background: rgba(99,102,241,0.12); color: #818cf8; }
.s-num {
  font-size: 22px;
  font-weight: 700;
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
  line-height: 1.15;
}
.s-num.cyan { color: var(--cyan); }
.s-num.green { color: #10b981; }
.s-num.amber { color: #f59e0b; }
.s-num.indigo { color: #818cf8; }
.s-label { font-size: 12px; color: var(--text-secondary); margin-top: 2px; }
.s-sub { font-size: 11px; color: var(--text-tertiary); margin-top: 1px; }

/* ===== 图表面板 ===== */
.chart-panel { padding: 16px 0 6px; border-bottom: 1px solid var(--border); }
.panel-title {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 10px;
}
.panel-hint { font-size: 12px; font-weight: 400; color: var(--text-tertiary); }
.chart { width: 100%; }
.tl-legend {
  display: flex;
  gap: 14px;
  padding: 6px 0 10px;
  font-size: 11px;
  color: var(--text-tertiary);
}
.lg { display: inline-flex; align-items: center; gap: 4px; }
.dot { width: 8px; height: 8px; border-radius: 3px; display: inline-block; }
.dot.critical { background: #ef4444; }
.dot.warning { background: #f59e0b; }
.dot.info { background: #3b82f6; }
.dot.flap { background: transparent; border: 1.5px solid #f59e0b; }

/* ===== 列表状态 ===== */
.list-state { text-align: center; padding: 40px; color: var(--text-tertiary); }
.list-state.empty p { margin-top: 10px; font-size: 13px; }

/* ===== 事件列表 ===== */
.event-list { display: flex; flex-direction: column; gap: 10px; padding: 16px 0; }
.event-row {
  display: flex;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  cursor: pointer;
  transition: border-color .15s ease, box-shadow .15s ease;
}
.event-row:hover { border-color: rgba(99, 102, 241, 0.4); box-shadow: 0 2px 10px rgba(0,0,0,0.05); }
.event-row.expanded { border-color: rgba(99, 102, 241, 0.55); }
.event-row.ongoing .event-body { background: rgba(239, 68, 68, 0.03); }
.event-bar { width: 4px; flex-shrink: 0; }
.event-row.ongoing .event-bar { background: #ef4444; }
.event-row:not(.ongoing) .event-bar { background: #10b981; }

.event-body { flex: 1; display: flex; flex-direction: column; padding: 13px 16px 13px 14px; min-width: 0; }
.event-main { min-width: 0; }
.event-title-line {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.event-title { display: flex; align-items: center; flex-wrap: wrap; gap: 4px; min-width: 0; }
.event-ds { font-size: 13px; color: var(--text-secondary); font-weight: 500; }
.event-divider { color: var(--text-tertiary); font-size: 12px; margin: 0 2px; }
.event-rule {
  font-size: 14px;
  color: var(--text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.event-times { display: flex; align-items: center; gap: 16px; flex-shrink: 0; }
.time-item { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; }
.time-label { font-size: 11px; color: var(--text-tertiary); }
.time-value { font-size: 12px; color: var(--text-secondary); font-family: 'SF Mono', Monaco, monospace; }
.expand-caret {
  color: var(--text-tertiary);
  transition: transform .2s ease;
  font-size: 14px;
}
.expand-caret.open { transform: rotate(180deg); }

.event-meta { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; }
.meta-val {
  font-size: 12px;
  color: var(--cyan);
  font-family: 'SF Mono', Monaco, monospace;
  margin-right: 4px;
}
.meta-val.over { color: #ef4444; font-weight: 600; }
.meta-threshold { color: var(--text-tertiary); margin-left: 4px; }

/* ===== 展开详情 ===== */
.event-detail {
  margin-top: 10px;
  padding: 12px 14px;
  background: var(--bg-elevated, var(--bg-card));
  border: 1px dashed var(--border);
  border-radius: 8px;
}
.detail-grid {
  display: grid;
  grid-template-columns: 76px 1fr;
  gap: 6px 12px;
  font-size: 12px;
}
.d-label { color: var(--text-tertiary); white-space: nowrap; }
.d-value { color: var(--text-secondary); min-width: 0; word-break: break-all; }
.d-value.mono { font-family: 'SF Mono', Monaco, monospace; }
.detail-hint {
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.6;
}
.detail-hint.warn { background: rgba(245,158,11,0.10); color: #b45309; }
.detail-hint.info { background: rgba(99,102,241,0.10); color: #6366f1; }
[data-theme="dark"] .detail-hint.warn, [data-theme="cyber"] .detail-hint.warn { color: #fbbf24; }
[data-theme="dark"] .detail-hint.info, [data-theme="cyber"] .detail-hint.info { color: #a5b4fc; }

.tag-ongoing { background: rgba(239,68,68,0.15); color: #ef4444; border: none; }
.tag-resolved { background: rgba(16,185,129,0.15); color: #10b981; border: none; }
.tag-flap { background: rgba(245,158,11,0.15); color: #f59e0b; border: none; }
.tag-over { background: rgba(239,68,68,0.12); color: #ef4444; border: none; font-family: 'SF Mono', Monaco, monospace; }
.tag-count { background: var(--bg-elevated, var(--bg-card)); color: var(--text-tertiary); border: none; font-family: 'SF Mono', Monaco, monospace; }
.tag-correlated { background: rgba(99,102,241,0.10); color: #818cf8; border: none; }
</style>
