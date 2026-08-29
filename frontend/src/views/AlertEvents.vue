<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Files /></el-icon> 告警聚合</h2>
      <p>
        两阶段降噪：<b>AlertHistory → 告警（按 fingerprint 去重）→ 故障（按 alertname + resource 在时间窗内聚合）</b>。
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
          <el-input v-model="filters.alertname" placeholder="搜索 alertname" style="width:160px;" clearable @keyup.enter="fetchAll">
            <template #suffix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-input v-model="filters.resource" placeholder="搜索 resource 标签" style="width:170px;" clearable @keyup.enter="fetchAll">
            <template #suffix><el-icon><Connection /></el-icon></template>
          </el-input>
          <el-button plain @click="fetchAll"><el-icon><Refresh /></el-icon></el-button>
          <el-button plain @click="openSettings"><el-icon><Setting /></el-icon><span style="margin-left:4px;">降噪配置</span></el-button>
          <div class="auto-refresh">
            <span class="ar-label">自动刷新</span>
            <el-switch v-model="autoRefresh" size="small" @change="toggleAutoRefresh" />
          </div>
        </div>
        <div v-if="lastUpdated" class="updated-at">更新于 {{ lastUpdated }}</div>
      </div>

      <!-- 三段降噪统计 -->
      <div class="stat-cards">
        <div class="s-card">
          <div class="s-icon raw"><el-icon><DataLine /></el-icon></div>
          <div class="s-body">
            <div class="s-num">{{ agg.total_raw }}</div>
            <div class="s-label">原始告警事件</div>
            <div class="s-sub">AlertHistory 行数 · {{ hours }}h</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon evt"><el-icon><Bell /></el-icon></div>
          <div class="s-body">
            <div class="s-num cyan">{{ agg.total_alerts }}</div>
            <div class="s-label">告警数（去重）</div>
            <div class="s-sub">同 fingerprint 合并</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon inc"><el-icon><Files /></el-icon></div>
          <div class="s-body">
            <div class="s-num indigo">{{ agg.total_incidents }}</div>
            <div class="s-label">故障数（聚合）</div>
            <div class="s-sub">同 alertname+resource 合</div>
          </div>
        </div>
        <div class="s-card">
          <div class="s-icon comp"><el-icon><TrendCharts /></el-icon></div>
          <div class="s-body">
            <div class="s-num green">-{{ agg.compression }}%</div>
            <div class="s-label">降噪比</div>
            <div class="s-sub">{{ compressionRatio }}× 压缩</div>
          </div>
        </div>
      </div>

      <!-- 故障时间轴 -->
      <div v-if="incidents.length > 0" class="chart-panel">
        <div class="panel-title">
          <span>故障时间轴</span>
          <span class="panel-hint">每条横杠 = 一个故障（首次触发 → 最近告警），颜色按级别、风暴描边</span>
        </div>
        <div ref="tlEl" class="chart" :style="{ height: tlHeight + 'px' }"></div>
        <div class="tl-legend">
          <span class="lg"><i class="dot critical"></i>严重</span>
          <span class="lg"><i class="dot warning"></i>警告</span>
          <span class="lg"><i class="dot info"></i>提醒</span>
          <span class="lg"><i class="dot storm"></i>风暴（描边）</span>
        </div>
      </div>

      <!-- TOP 噪音 -->
      <div v-if="noiseItems.length > 0" class="chart-panel">
        <div class="panel-title">
          <span>TOP 噪音故障</span>
          <span class="panel-hint">按 (alertname + resource) 聚合的告警数排序，优先治理风暴与高频故障</span>
        </div>
        <div ref="noiseEl" class="chart" :style="{ height: noiseHeight + 'px' }"></div>
      </div>

      <!-- 故障列表 -->
      <div v-if="loading" class="list-state">
        <el-icon class="is-loading" :size="24"><Loading /></el-icon>
      </div>
      <div v-else-if="incidents.length === 0" class="list-state empty">
        <el-icon :size="36"><Bell /></el-icon>
        <p>所选时间窗内没有故障</p>
      </div>

      <div v-else class="incident-list">
        <div
          v-for="inc in incidents"
          :key="inc.key"
          class="incident-row"
          :class="{ ongoing: inc.state === 'ongoing' }"
          @click="openDetail(inc)"
        >
          <div class="inc-bar"></div>
          <div class="inc-body">
            <div class="inc-title-line">
              <div class="inc-title">
                <span class="inc-alertname" :title="inc.alertname">{{ inc.alertname }}</span>
                <el-tag v-if="inc.resource" size="small" class="tag-resource">resource: {{ inc.resource }}</el-tag>
                <el-tag v-if="inc.severity" size="small" :style="sevStyle(inc.severity)">{{ sevLabel(inc.severity) }}</el-tag>
                <el-tag size="small" :class="inc.state === 'ongoing' ? 'tag-ongoing' : 'tag-resolved'">
                  {{ inc.state === 'ongoing' ? '持续中' : '已恢复' }} · {{ incDuration(inc) }}
                </el-tag>
                <el-tag v-if="inc.storm" size="small" class="tag-storm">⚡ 风暴 x{{ inc.alert_count }}</el-tag>
                <el-tag size="small" class="tag-count">{{ inc.alert_count }} 条告警</el-tag>
                <el-tag v-if="inc.datasources.length > 1" size="small" class="tag-cluster">跨 {{ inc.datasources.length }} 集群</el-tag>
              </div>
              <div class="inc-times">
                <div class="time-item">
                  <span class="time-label">首次触发</span>
                  <span class="time-value">{{ fmt(inc.first_fired_at) }}</span>
                </div>
                <div class="time-item">
                  <span class="time-label">最近告警</span>
                  <span class="time-value">{{ fmt(inc.last_event_at) }}</span>
                </div>
                <el-icon class="expand-caret"><Right /></el-icon>
              </div>
            </div>
            <div v-if="inc.datasources.length > 0" class="inc-meta">
              <span class="meta-label">涉及集群：</span>
              <span v-for="(ds, idx) in inc.datasources" :key="ds" class="meta-ds">
                {{ ds }}<span v-if="idx < inc.datasources.length - 1" class="meta-sep">/</span>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 故障详情抽屉 -->
    <el-drawer
      v-model="drawerOpen"
      :title="drawerTitle"
      direction="rtl"
      size="640px"
      :destroy-on-close="false"
    >
      <div v-if="detailLoading" class="list-state">
        <el-icon class="is-loading" :size="24"><Loading /></el-icon>
      </div>
      <div v-else-if="detail" class="detail-view">
        <div class="detail-header">
          <div class="detail-row">
            <span class="d-label">alertname</span>
            <span class="d-value">{{ detail.alertname }}</span>
          </div>
          <div class="detail-row">
            <span class="d-label">resource</span>
            <span class="d-value mono">{{ detail.resource || '（无 resource 标签）' }}</span>
          </div>
          <div class="detail-row">
            <span class="d-label">状态</span>
            <span class="d-value">
              <el-tag size="small" :class="detail.state === 'ongoing' ? 'tag-ongoing' : 'tag-resolved'">
                {{ detail.state === 'ongoing' ? '持续中' : '已恢复' }} · {{ incDuration(detail) }}
              </el-tag>
              <el-tag v-if="detail.severity" size="small" :style="sevStyle(detail.severity)" style="margin-left:6px;">{{ sevLabel(detail.severity) }}</el-tag>
              <el-tag v-if="detail.storm" size="small" class="tag-storm" style="margin-left:6px;">⚡ 风暴</el-tag>
            </span>
          </div>
          <div class="detail-row">
            <span class="d-label">时间范围</span>
            <span class="d-value mono">{{ fmt(detail.first_fired_at) }} ~ {{ fmt(detail.last_event_at) }}</span>
          </div>
          <div class="detail-row">
            <span class="d-label">涉及集群</span>
            <span class="d-value">{{ (detail.datasources || []).join('、') || '-' }}</span>
          </div>
        </div>

        <div v-if="detail.storm" class="detail-hint warn">
          ⚡ 故障内告警数（{{ detail.alert_count }}）超过风暴阈值（{{ agg.storm_threshold || cfg.storm_threshold }}），说明该故障在反复抖动或在多对象/多集群上同时发作，建议立即排查根因。
        </div>
        <div v-if="(detail.datasources || []).length > 1" class="detail-hint info">
          🔗 故障跨 {{ detail.datasources.length }} 个集群（{{ detail.datasources.join('、') }}），可能是共享依赖故障或公共标签匹配导致，建议结合 AI 根因分析确认是否同源。
        </div>

        <div class="detail-section-title">告警明细（{{ (detail.alerts || []).length }} 条）</div>
        <div v-if="(detail.alerts || []).length === 0" class="list-state empty" style="padding:20px;">
          故障内暂无告警明细
        </div>
        <div v-else class="alert-list">
          <div
            v-for="(a, i) in detail.alerts"
            :key="i"
            class="alert-row"
            :class="{ ongoing: a.state === 'ongoing' }"
          >
            <div class="alert-bar"></div>
            <div class="alert-body">
              <div class="alert-line1">
                <span class="alert-idx">#{{ i + 1 }}</span>
                <el-tag size="small" :class="a.state === 'ongoing' ? 'tag-ongoing' : 'tag-resolved'">{{ a.state === 'ongoing' ? 'firing' : 'resolved' }}</el-tag>
                <el-tag v-if="a.severity" size="small" :style="sevStyle(a.severity)">{{ sevLabel(a.severity) }}</el-tag>
                <span class="alert-time mono">{{ fmt(a.time) }}</span>
                <span class="alert-ds">@ {{ a.datasource_name || '-' }}</span>
              </div>
              <div class="alert-line2">
                <span class="alert-val">
                  value <b>{{ formatNum(a.value) }}</b>
                  <span class="alert-thr">/ threshold {{ formatNum(a.threshold) }}</span>
                </span>
                <span class="alert-dur">持续 {{ a.duration || '-' }}</span>
              </div>
              <div v-if="a.labels && Object.keys(a.labels).length > 0" class="alert-labels">
                <span v-for="(v, k) in a.labels" :key="k" class="label-chip">{{ k }}={{ v }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </el-drawer>

    <!-- 降噪配置对话框 -->
    <el-dialog v-model="settingsOpen" title="降噪配置" width="520px" :close-on-click-modal="false">
      <el-form :model="cfg" label-width="120px" label-position="right">
        <el-form-item label="聚合窗口（分钟）">
          <el-input-number v-model="cfg.window_minutes" :min="0" :max="1440" :step="1" style="width:200px;" />
          <div class="form-hint">相邻告警间隔小于等于该值时合入同一故障；0 表示不切窗（每次告警独立故障）</div>
        </el-form-item>
        <el-form-item label="风暴阈值（条）">
          <el-input-number v-model="cfg.storm_threshold" :min="0" :max="1000" :step="1" style="width:200px;" />
          <div class="form-hint">故障内告警数大于该值时打风暴标记；0 表示不预警</div>
        </el-form-item>
        <el-form-item label="Resource 标签">
          <el-input v-model="resourceLabelsInput" placeholder="resource,instance" />
          <div class="form-hint">用告警 labels_json 中的哪些标签做故障聚合维度，多个用英文逗号分隔；默认 resource</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="settingsOpen = false">取消</el-button>
        <el-button type="primary" :loading="savingConfig" @click="saveConfig">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import * as echarts from 'echarts'
import dayjs from 'dayjs'
import { ElMessage } from 'element-plus'
import {
  getAlertIncidents, getAlertIncidentDetail, getAlertNoiseTop,
  getDenoiseConfig, saveDenoiseConfig, getAllDataSources,
} from '../api'
import type { AlertIncident, NoiseTopItem, DenoiseConfig } from '../types/alerting'
import type { DataSource } from '../types'

const SEV_COLORS: Record<string, string> = {
  critical: '#ef4444',
  warning: '#f59e0b',
  info: '#3b82f6',
}

const loading = ref(false)
const incidents = ref<AlertIncident[]>([])
const noiseItems = ref<NoiseTopItem[]>([])
const datasources = ref<DataSource[]>([])
const hours = ref<number>(24)
const autoRefresh = ref(false)
const lastUpdated = ref('')
const agg = ref<{ total_raw: number; total_alerts: number; total_incidents: number; compression: number; storm_threshold: number }>({
  total_raw: 0, total_alerts: 0, total_incidents: 0, compression: 0, storm_threshold: 0,
})
const filters = ref<{ severity: string; datasource_id: number | ''; alertname: string; resource: string }>({
  severity: '', datasource_id: '', alertname: '', resource: '',
})

// 详情抽屉
const drawerOpen = ref(false)
const detailLoading = ref(false)
const detail = ref<AlertIncident | null>(null)

// 配置
const settingsOpen = ref(false)
const savingConfig = ref(false)
const cfg = ref<DenoiseConfig>({ window_minutes: 10, storm_threshold: 10, resource_labels: ['resource'] })
const resourceLabelsInput = ref('resource')

// 图表实例
const tlEl = ref<HTMLElement>()
const noiseEl = ref<HTMLElement>()
let tlChart: echarts.ECharts | null = null
let noiseChart: echarts.ECharts | null = null
let refreshTimer: number | null = null

const compressionRatio = computed(() => {
  if (agg.value.total_incidents > 0) {
    return Math.round((agg.value.total_raw / agg.value.total_incidents) * 10) / 10
  }
  return 0
})
const tlIncidents = computed(() => incidents.value.slice(0, 25))
const tlHeight = computed(() => Math.max(140, tlIncidents.value.length * 26 + 46))
const noiseHeight = computed(() => noiseItems.value.length * 30 + 40)

const drawerTitle = computed(() => {
  if (!detail.value) return '故障详情'
  return `故障 · ${detail.value.alertname}${detail.value.resource ? ' · ' + detail.value.resource : ''}`
})

function incDuration(inc: AlertIncident): string {
  const start = dayjs(inc.first_fired_at)
  const end = inc.state === 'ongoing' ? dayjs() : dayjs(inc.last_event_at)
  if (!start.isValid() || start.year() < 2000) return '-'
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
function esc(s: string | undefined | null) {
  return (s || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function themeColors() {
  const t = document.documentElement.getAttribute('data-theme')
  const isLight = t !== 'dark' && t !== 'cyber'
  return {
    axis: isLight ? '#64748b' : '#94a3b8',
    split: isLight ? 'rgba(59,130,246,0.12)' : 'rgba(56,189,248,0.1)',
  }
}

function incidentLabel(inc: AlertIncident): string {
  const short = inc.alertname.length > 30 ? inc.alertname.slice(0, 30) + '…' : inc.alertname
  return inc.resource ? `${short} · ${inc.resource}` : short
}

// ===== 故障时间轴 =====
function renderTimeline() {
  if (!tlEl.value || tlIncidents.value.length === 0) return
  if (!tlChart) tlChart = echarts.init(tlEl.value)
  const { axis, split } = themeColors()
  const now = Date.now()

  const labels = tlIncidents.value.map(incidentLabel)
  const labelIdx = (l: string) => labels.indexOf(l)
  const data = tlIncidents.value.map((inc) => {
    const start = dayjs(inc.first_fired_at).valueOf()
    const last = dayjs(inc.last_event_at).valueOf()
    const end = inc.state === 'ongoing' ? Math.max(last, now) : last
    const color = SEV_COLORS[inc.severity] || '#6366f1'
    return {
      value: [labelIdx(incidentLabel(inc)), start, end],
      itemStyle: {
        fill: color,
        opacity: inc.state === 'ongoing' ? 0.9 : 0.55,
        stroke: inc.storm ? '#f59e0b' : 'transparent',
        lineWidth: inc.storm ? 1.5 : 0,
      },
    }
  })

  tlChart.setOption({
    backgroundColor: 'transparent',
    animation: true,
    tooltip: {
      trigger: 'item',
      formatter: (p: any) => {
        const inc = tlIncidents.value[p.dataIndex]
        if (!inc) return ''
        return [
          `<b>${esc(inc.alertname)}</b>`,
          `resource：${esc(inc.resource || '-')}`,
          `状态：${inc.state === 'ongoing' ? '持续中' : '已恢复'} · ${incDuration(inc)}`,
          `告警数：${inc.alert_count}` + (inc.storm ? ' · 风暴' : ''),
          `首末：${fmt(inc.first_fired_at)} ~ ${fmt(inc.last_event_at)}`,
          `集群：${(inc.datasources || []).join('、') || '-'}`,
        ].join('<br/>')
      },
    },
    grid: { left: 220, right: 24, top: 8, bottom: 30 },
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
      axisLabel: { color: axis, fontSize: 11, width: 212, overflow: 'truncate' },
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

// ===== TOP 噪音（按 alertname + resource） =====
function renderNoise() {
  if (!noiseEl.value || noiseItems.value.length === 0) return
  if (!noiseChart) noiseChart = echarts.init(noiseEl.value)
  const { axis } = themeColors()
  const items = noiseItems.value

  const labels = items.map(n => {
    const short = n.alertname.length > 24 ? n.alertname.slice(0, 24) + '…' : n.alertname
    return n.resource ? `${short} · ${n.resource}` : short
  })
  labels.reverse()
  const counts = [...items].map(n => n.alert_count).reverse()
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
          `<b>${esc(n.alertname)}</b>`,
          `resource：${esc(n.resource || '-')}`,
          `告警数：${n.alert_count}` + (n.storm ? ' · 风暴' : ''),
          `状态：${n.state === 'ongoing' ? '持续中' : '已恢复'}`,
          `集群：${(n.datasources || []).join('、') || '-'}`,
          n.storm ? '⚠️ 风暴故障，建议立即排查根因或添加静默' : '',
        ].filter(Boolean).join('<br/>')
      },
    },
    grid: { left: 220, right: 46, top: 6, bottom: 6 },
    xAxis: { type: 'value', show: false },
    yAxis: {
      type: 'category',
      data: labels,
      axisTick: { show: false },
      axisLine: { show: false },
      axisLabel: { color: axis, fontSize: 11, width: 212, overflow: 'truncate' },
    },
    series: [{
      type: 'bar',
      barWidth: 12,
      itemStyle: { borderRadius: 6 },
      label: {
        show: true, position: 'right', color: axis, fontSize: 11,
        fontFamily: 'SF Mono, Monaco, monospace',
        formatter: (p: any) => `${p.value} 条`,
      },
      data: counts.map((c, i) => ({
        value: c,
        itemStyle: { color: meta[i].storm ? '#f59e0b' : '#ef4444' },
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

// ===== 数据拉取 =====
async function fetchConfig() {
  try {
    const r = await getDenoiseConfig()
    cfg.value = r.data
    resourceLabelsInput.value = (r.data.resource_labels || []).join(',')
  } catch { /* 默认值 */ }
}

function buildCommonParams(): any {
  const p: any = {}
  if (cfg.value.window_minutes >= 0) p.window_minutes = cfg.value.window_minutes
  if (cfg.value.storm_threshold >= 0) p.storm_threshold = cfg.value.storm_threshold
  if (cfg.value.resource_labels.length > 0) p.resource_labels = cfg.value.resource_labels.join(',')
  return p
}

async function fetchIncidents() {
  loading.value = true
  try {
    const p: any = { hours: hours.value, limit: 200, ...buildCommonParams() }
    if (filters.value.severity) p.severity = filters.value.severity
    if (filters.value.datasource_id !== '') p.datasource_id = filters.value.datasource_id
    if (filters.value.alertname) p.alertname = filters.value.alertname
    if (filters.value.resource) p.resource = filters.value.resource
    const r = await getAlertIncidents(p)
    incidents.value = r.data.incidents || []
    agg.value = {
      total_raw: r.data.total_raw || 0,
      total_alerts: r.data.total_alerts || 0,
      total_incidents: r.data.total_incidents || 0,
      compression: r.data.compression || 0,
      storm_threshold: r.data.storm_threshold || 0,
    }
  } catch {
    ElMessage.error('加载故障聚合数据失败')
  } finally {
    loading.value = false
  }
}

async function fetchNoise() {
  try {
    const p: any = { hours: hours.value, limit: 10, ...buildCommonParams() }
    const r = await getAlertNoiseTop(p)
    noiseItems.value = r.data.items || []
  } catch { /* 噪音排行失败不阻塞主列表 */ }
}

async function fetchAll() {
  await Promise.all([fetchIncidents(), fetchNoise()])
  lastUpdated.value = dayjs().format('HH:mm:ss')
  renderCharts()
}

// ===== 详情抽屉 =====
async function openDetail(inc: AlertIncident) {
  drawerOpen.value = true
  detailLoading.value = true
  detail.value = null
  try {
    const p: any = { key: inc.key, hours: hours.value, ...buildCommonParams() }
    const r = await getAlertIncidentDetail(p)
    detail.value = r.data.incident
  } catch {
    ElMessage.error('加载故障详情失败')
  } finally {
    detailLoading.value = false
  }
}

function openSettings() {
  resourceLabelsInput.value = (cfg.value.resource_labels || []).join(',')
  settingsOpen.value = true
}

async function saveConfig() {
  savingConfig.value = true
  try {
    const labels = resourceLabelsInput.value
      .split(',').map((s: string) => s.trim()).filter((s: string) => s.length > 0)
    const next: DenoiseConfig = {
      window_minutes: cfg.value.window_minutes,
      storm_threshold: cfg.value.storm_threshold,
      resource_labels: labels.length > 0 ? labels : ['resource'],
    }
    await saveDenoiseConfig(next)
    cfg.value = next
    settingsOpen.value = false
    ElMessage.success('降噪配置已保存，重新计算中…')
    fetchAll()
  } catch (e: any) {
    ElMessage.error('保存失败：' + (e?.message || e))
  } finally {
    savingConfig.value = false
  }
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
  await fetchConfig()
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
.s-icon.inc { background: rgba(99,102,241,0.12); color: #818cf8; }
.s-icon.comp { background: rgba(16,185,129,0.12); color: #10b981; }
.s-num {
  font-size: 22px;
  font-weight: 700;
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
  line-height: 1.15;
}
.s-num.cyan { color: var(--cyan); }
.s-num.indigo { color: #818cf8; }
.s-num.green { color: #10b981; }
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
.dot.storm { background: transparent; border: 1.5px solid #f59e0b; }

/* ===== 列表状态 ===== */
.list-state { text-align: center; padding: 40px; color: var(--text-tertiary); }
.list-state.empty p { margin-top: 10px; font-size: 13px; }

/* ===== 故障列表 ===== */
.incident-list { display: flex; flex-direction: column; gap: 10px; padding: 16px 0; }
.incident-row {
  display: flex;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  cursor: pointer;
  transition: border-color .15s ease, box-shadow .15s ease;
}
.incident-row:hover { border-color: rgba(99, 102, 241, 0.4); box-shadow: 0 2px 10px rgba(0,0,0,0.05); }
.incident-row.ongoing .inc-body { background: rgba(239, 68, 68, 0.03); }
.inc-bar { width: 4px; flex-shrink: 0; }
.incident-row.ongoing .inc-bar { background: #ef4444; }
.incident-row:not(.ongoing) .inc-bar { background: #10b981; }

.inc-body { flex: 1; display: flex; flex-direction: column; padding: 13px 16px 13px 14px; min-width: 0; }
.inc-title-line {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
}
.inc-title { display: flex; align-items: center; flex-wrap: wrap; gap: 4px; min-width: 0; }
.inc-alertname {
  font-size: 14px;
  color: var(--text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}
.inc-times { display: flex; align-items: center; gap: 16px; flex-shrink: 0; }
.time-item { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; }
.time-label { font-size: 11px; color: var(--text-tertiary); }
.time-value { font-size: 12px; color: var(--text-secondary); font-family: 'SF Mono', Monaco, monospace; }
.expand-caret { color: var(--text-tertiary); font-size: 14px; }
.inc-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  color: var(--text-tertiary);
}
.meta-label { color: var(--text-tertiary); }
.meta-ds { color: var(--text-secondary); }
.meta-sep { color: var(--text-tertiary); margin: 0 4px; }

.tag-ongoing { background: rgba(239,68,68,0.15); color: #ef4444; border: none; }
.tag-resolved { background: rgba(16,185,129,0.15); color: #10b981; border: none; }
.tag-storm { background: rgba(245,158,11,0.18); color: #d97706; border: none; font-weight: 600; }
.tag-resource { background: rgba(99,102,241,0.10); color: #818cf8; border: none; font-family: 'SF Mono', Monaco, monospace; }
.tag-count { background: var(--bg-elevated, var(--bg-card)); color: var(--text-tertiary); border: none; font-family: 'SF Mono', Monaco, monospace; }
.tag-cluster { background: rgba(148,163,184,0.12); color: var(--text-secondary); border: none; }

/* ===== 详情抽屉 ===== */
.detail-view { padding: 0 4px; }
.detail-header {
  background: var(--bg-elevated, var(--bg-card));
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 16px;
  margin-bottom: 12px;
}
.detail-row {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 12px;
  font-size: 12px;
  padding: 4px 0;
  align-items: center;
}
.d-label { color: var(--text-tertiary); white-space: nowrap; }
.d-value { color: var(--text-secondary); min-width: 0; word-break: break-all; }
.d-value.mono { font-family: 'SF Mono', Monaco, monospace; }

.detail-hint {
  margin-bottom: 10px;
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.6;
}
.detail-hint.warn { background: rgba(245,158,11,0.10); color: #b45309; }
.detail-hint.info { background: rgba(99,102,241,0.10); color: #6366f1; }
[data-theme="dark"] .detail-hint.warn, [data-theme="cyber"] .detail-hint.warn { color: #fbbf24; }
[data-theme="dark"] .detail-hint.info, [data-theme="cyber"] .detail-hint.info { color: #a5b4fc; }

.detail-section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 8px 0 6px;
}

.alert-list { display: flex; flex-direction: column; gap: 8px; }
.alert-row {
  display: flex;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 6px;
  overflow: hidden;
}
.alert-row.ongoing { border-color: rgba(239,68,68,0.4); }
.alert-bar { width: 3px; flex-shrink: 0; }
.alert-row.ongoing .alert-bar { background: #ef4444; }
.alert-row:not(.ongoing) .alert-bar { background: #10b981; }
.alert-body { flex: 1; padding: 8px 12px; min-width: 0; }
.alert-line1 { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; font-size: 12px; }
.alert-idx {
  color: var(--text-tertiary);
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 11px;
}
.alert-time { color: var(--text-secondary); font-size: 12px; }
.alert-ds { color: var(--text-tertiary); font-size: 12px; }
.alert-line2 {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
  font-size: 12px;
}
.alert-val { color: var(--cyan); font-family: 'SF Mono', Monaco, monospace; }
.alert-thr { color: var(--text-tertiary); margin-left: 4px; }
.alert-dur { color: var(--text-tertiary); font-family: 'SF Mono', Monaco, monospace; }
.alert-labels {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 6px;
}
.label-chip {
  background: var(--bg-elevated, var(--bg-card));
  color: var(--text-secondary);
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  font-family: 'SF Mono', Monaco, monospace;
}

/* ===== 设置对话框 ===== */
.form-hint {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-top: 4px;
  line-height: 1.5;
}
</style>
