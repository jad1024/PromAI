<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Files /></el-icon> 告警聚合</h2>
      <p>
        两阶段降噪：<b>AlertHistory → 告警（按 fingerprint 去重）→ 故障（按 alertname 在时间窗内聚合）</b>。
        每个故障下挂载多个实例/集群告警，点击查看「具体实例」。通知层面的分组/去重/抑制由 Alertmanager 负责，<b>本页不参与任何通知链路</b>。
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
          <el-input v-model="filters.instance" placeholder="搜索实例 (instance/pod)" style="width:170px;" clearable @keyup.enter="fetchAll">
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

      <!-- 四段降噪统计 -->
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
            <div class="s-sub">同 alertname 合</div>
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
          <span>TOP 噪音告警</span>
          <span class="panel-hint">按 alertname 聚合的告警数排序，优先治理风暴与高频告警</span>
        </div>
        <div ref="noiseEl" class="chart" :style="{ height: noiseHeight + 'px' }"></div>
      </div>

      <!-- 状态切换：告警中 / 已恢复 / 全部 -->
      <el-tabs v-model="stateTab" class="state-tabs" @tab-change="onStateTabChange">
        <el-tab-pane label="告警中" name="ongoing" />
        <el-tab-pane label="已恢复" name="resolved" />
        <el-tab-pane label="全部" name="all" />
      </el-tabs>

      <!-- 故障列表 -->
      <div v-if="loading" class="list-state">
        <el-icon class="is-loading" :size="24"><Loading /></el-icon>
      </div>
      <div v-else-if="incidents.length === 0" class="list-state empty">
        <el-icon :size="36"><Bell /></el-icon>
        <p>{{ emptyText }}</p>
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
                <el-tag v-if="inc.severity" size="small" :style="sevStyle(inc.severity)">{{ sevLabel(inc.severity) }}</el-tag>
                <el-tag size="small" :class="inc.state === 'ongoing' ? 'tag-ongoing' : 'tag-resolved'">
                  {{ inc.state === 'ongoing' ? '持续中' : '已恢复' }} · {{ incDuration(inc) }}
                </el-tag>
                <el-tag v-if="inc.storm" size="small" class="tag-storm">⚡ 风暴</el-tag>
                <el-tag size="small" class="tag-count">{{ inc.alert_count }} 条告警</el-tag>
                <el-tag size="small" class="tag-instance" type="info">
                  <el-icon style="vertical-align:-2px;"><Monitor /></el-icon>
                  {{ inc.instance_count }} 实例
                </el-tag>
                <el-tooltip v-if="inc.peak_instance_count && inc.peak_instance_count > inc.instance_count" placement="top" content="窗口内曾出现的不重复实例数（含已恢复），大于当前活跃实例数说明已有实例自动恢复过">
                  <el-tag size="small" type="warning" plain>
                    <el-icon style="vertical-align:-2px;"><DataLine /></el-icon>
                    峰值 {{ inc.peak_instance_count }} 实例
                  </el-tag>
                </el-tooltip>
                <el-tooltip v-if="inc.total_firing_count && inc.total_firing_count > inc.alert_count" placement="top" :content="`窗口内累计触发的告警事件总数 ${inc.total_firing_count}（含 n9e 周期性重发的重复推送），大于独立告警数 ${inc.alert_count} 说明存在反复抖动`">
                  <el-tag size="small" type="warning" plain>
                    <el-icon style="vertical-align:-2px;"><Refresh /></el-icon>
                    累计 {{ inc.total_firing_count }} 次告警事件
                  </el-tag>
                </el-tooltip>
                <el-tag v-if="inc.cluster_count > 1" size="small" class="tag-cluster">跨 {{ inc.cluster_count }} 集群</el-tag>
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
              <el-button
                class="inc-delete"
                size="small"
                text
                type="danger"
                title="删除该故障（同步清除其告警历史，不可恢复）"
                @click.stop="confirmDelete(inc)"
              >
                <el-icon><Delete /></el-icon>
              </el-button>
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
      size="720px"
      :destroy-on-close="false"
    >
      <div v-if="detailLoading" class="list-state">
        <el-icon class="is-loading" :size="24"><Loading /></el-icon>
      </div>
      <div v-else-if="detail" class="detail-view">
        <div class="detail-header">
          <div class="detail-row">
            <span class="d-label">alertname</span>
            <span class="d-value mono">{{ detail.alertname }}</span>
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
            <span class="d-label">影响范围</span>
            <span class="d-value">
              <el-tag size="small" type="info">
                <el-icon style="vertical-align:-2px;"><Monitor /></el-icon>
                {{ detail.instance_count }} 个实例
              </el-tag>
              <el-tag v-if="detail.cluster_count > 0" size="small" class="tag-cluster" style="margin-left:6px;">
                跨 {{ detail.cluster_count }} 集群
              </el-tag>
            </span>
          </div>
          <div class="detail-row">
            <span class="d-label">涉及集群</span>
            <span class="d-value">{{ (detail.datasources || []).join('、') || '-' }}</span>
          </div>
        </div>

        <div v-if="detail.storm" class="detail-hint warn">
          ⚡ 故障内告警数（{{ detail.alert_count }}）超过风暴阈值（{{ agg.storm_threshold || cfg.storm_threshold }}），说明该 alertname 在反复抖动或在多对象/多集群上同时发作，建议立即排查根因。
        </div>
        <div v-if="(detail.datasources || []).length > 1" class="detail-hint info">
          🔗 故障跨 {{ detail.datasources.length }} 个集群（{{ detail.datasources.join('、') }}），可能是共享依赖故障或公共标签匹配导致，建议结合 AI 根因分析确认是否同源。
        </div>

        <div class="detail-section-title">
          告警明细（{{ (detail.alerts || []).length }} 条 · 不重复 {{ detail.instance_count }} 个实例）
        </div>
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
                <el-tag size="small" class="tag-instance" type="info" v-if="a.instance">
                  <el-icon style="vertical-align:-2px;"><Monitor /></el-icon>
                  {{ a.instance }}
                </el-tag>
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

    <!-- 删除故障确认对话框 -->
    <el-dialog v-model="deleteOpen" title="删除故障" width="480px" :close-on-click-modal="false">
      <div v-if="pendingDelete" class="delete-confirm">
        <p class="dc-warn">
          <el-icon><WarningFilled /></el-icon>
          此操作不可恢复！
        </p>
        <p class="dc-target"><b>{{ pendingDelete.alertname }}</b></p>
        <p class="dc-detail">
          {{ pendingDelete.alert_count }} 条告警 · {{ pendingDelete.instance_count }} 个实例
          <span v-if="pendingDelete.cluster_count > 1"> · 跨 {{ pendingDelete.cluster_count }} 集群</span>
          <span v-if="pendingDelete.storm"> · 风暴</span>
        </p>
        <p class="dc-note">
          将同步清除该故障涉及的所有告警历史记录（软删除），聚合列表和历史页都不会再显示。
          手动删除没有恢复记录，删除后不可恢复。
        </p>
      </div>
      <template #footer>
        <el-button @click="deleteOpen = false">取消</el-button>
        <el-button type="danger" :loading="deleting" @click="doDelete">确认删除</el-button>
      </template>
    </el-dialog>

    <!-- 降噪配置对话框 -->
    <el-dialog v-model="settingsOpen" title="降噪配置" width="520px" :close-on-click-modal="false">
      <el-form :model="cfg" label-width="120px" label-position="right">
        <el-form-item label="聚合窗口（分钟）">
          <el-input-number v-model="cfg.window_minutes" :min="0" :max="1440" :step="1" style="width:200px;" />
          <div class="form-hint">相邻告警间隔小于等于该值时合入同一故障（按 alertname）；0 表示不切窗（每次告警独立故障）</div>
        </el-form-item>
        <el-form-item label="风暴阈值（条）">
          <el-input-number v-model="cfg.storm_threshold" :min="0" :max="1000" :step="1" style="width:200px;" />
          <div class="form-hint">故障内告警数大于该值时打风暴标记；0 表示不预警</div>
        </el-form-item>
        <el-form-item label="实例标签">
          <el-input v-model="resourceLabelsInput" placeholder="resource,instance" />
          <div class="form-hint">从告警 labels 中按顺序提取「实例标识」（CSV 即优先级）。仅用于下钻展示和实例过滤，<b>不影响故障聚合主键（alertname）</b>。默认 resource</div>
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
  getAlertIncidents, getAlertIncidentDetail, getAlertNoiseTop, deleteAlertIncidents,
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
const filters = ref<{ severity: string; datasource_id: number | ''; alertname: string; instance: string }>({
  severity: '', datasource_id: '', alertname: '', instance: '',
})

// 状态 Tab：ongoing=告警中 / resolved=已恢复 / all=全部
const stateTab = ref<'ongoing' | 'resolved' | 'all'>('all')

// 详情抽屉
const drawerOpen = ref(false)
const detailLoading = ref(false)
const detail = ref<AlertIncident | null>(null)

// 删除故障
const deleteOpen = ref(false)
const deleting = ref(false)
const pendingDelete = ref<AlertIncident | null>(null)

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
const emptyText = computed(() => {
  if (stateTab.value === 'ongoing') return '当前没有「告警中」的故障'
  if (stateTab.value === 'resolved') return '当前没有「已恢复」的故障'
  return '所选时间窗内没有故障'
})
const tlIncidents = computed(() => incidents.value.slice(0, 25))
const tlHeight = computed(() => Math.max(140, tlIncidents.value.length * 26 + 46))
const noiseHeight = computed(() => noiseItems.value.length * 30 + 40)

const drawerTitle = computed(() => {
  if (!detail.value) return '故障详情'
  return `故障 · ${detail.value.alertname}`
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
  return short
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
          `状态：${inc.state === 'ongoing' ? '持续中' : '已恢复'} · ${incDuration(inc)}`,
          `告警数：${inc.alert_count} · 实例：${inc.instance_count} · 集群：${inc.cluster_count}` + (inc.storm ? ' · 风暴' : ''),
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

// ===== TOP 噪音（按 alertname） =====
function renderNoise() {
  if (!noiseEl.value || noiseItems.value.length === 0) return
  if (!noiseChart) noiseChart = echarts.init(noiseEl.value)
  const { axis } = themeColors()
  const items = noiseItems.value

  const labels = items.map(n => n.alertname.length > 32 ? n.alertname.slice(0, 32) + '…' : n.alertname)
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
          `告警数：${n.alert_count} · 实例：${n.instance_count} · 集群：${n.cluster_count}` + (n.storm ? ' · 风暴' : ''),
          `状态：${n.state === 'ongoing' ? '持续中' : '已恢复'}`,
          `集群：${(n.datasources || []).join('、') || '-'}`,
          n.storm ? '⚠️ 风暴告警，建议立即排查根因或添加静默' : '',
        ].filter(Boolean).join('<br/>')
      },
    },
    grid: { left: 240, right: 46, top: 6, bottom: 6 },
    xAxis: { type: 'value', show: false },
    yAxis: {
      type: 'category',
      data: labels,
      axisTick: { show: false },
      axisLine: { show: false },
      axisLabel: { color: axis, fontSize: 11, width: 232, overflow: 'truncate' },
    },
    series: [{
      type: 'bar',
      barWidth: 12,
      itemStyle: { borderRadius: 6 },
      label: {
        show: true,
        position: 'right',
        color: axis,
        fontSize: 11,
        formatter: (p: any) => {
          const n = meta[p.dataIndex]
          if (!n) return ''
          return `${n.alert_count} 告警 / ${n.instance_count} 实例` + (n.storm ? ' ⚡' : '')
        },
      },
      data: counts,
    }],
  }, true)
}

// ===== 数据加载 =====
async function loadDenoiseConfig() {
  try {
    const r = await getDenoiseConfig()
    cfg.value = r.data
    resourceLabelsInput.value = (r.data.resource_labels || ['resource']).join(',')
  } catch (_) { /* 静默 */ }
}

async function loadDataSources() {
  try {
    const list = await getAllDataSources()
    datasources.value = list.data || []
  } catch (_) { /* 静默 */ }
}

async function fetchIncidents() {
  loading.value = true
  try {
    const params: any = { hours: hours.value, limit: 100 }
    if (filters.value.severity) params.severity = filters.value.severity
    if (filters.value.datasource_id) params.datasource_id = filters.value.datasource_id
    if (filters.value.alertname) params.alertname = filters.value.alertname
    if (filters.value.instance) params.instance = filters.value.instance
    if (stateTab.value === 'ongoing' || stateTab.value === 'resolved') params.state = stateTab.value
    const resp = await getAlertIncidents(params)
    const data = resp.data
    incidents.value = data.incidents || []
    agg.value = {
      total_raw: data.total_raw,
      total_alerts: data.total_alerts,
      total_incidents: data.total_incidents,
      compression: data.compression,
      storm_threshold: data.storm_threshold,
    }
    cfg.value.resource_labels = data.resource_labels
  } catch (e: any) {
    ElMessage.error('加载故障列表失败: ' + (e?.message || e))
  } finally {
    loading.value = false
  }
}

async function fetchNoise() {
  try {
    const resp = await getAlertNoiseTop({ hours: hours.value, limit: 10 })
    noiseItems.value = resp.data.items || []
  } catch (_) { /* 静默 */ }
}

async function fetchAll() {
  await Promise.all([fetchIncidents(), fetchNoise()])
  lastUpdated.value = dayjs().format('HH:mm:ss')
  await nextTick()
  renderTimeline()
  renderNoise()
}

function onStateTabChange() {
  fetchAll()
}

function toggleAutoRefresh(v: boolean) {
  if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null }
  if (v) refreshTimer = window.setInterval(fetchAll, 30000)
}

async function openDetail(inc: AlertIncident) {
  drawerOpen.value = true
  detailLoading.value = true
  detail.value = null
  try {
    const resp = await getAlertIncidentDetail({ key: inc.key, hours: hours.value })
    detail.value = resp.data.incident
  } catch (e: any) {
    ElMessage.error('加载故障详情失败: ' + (e?.message || e))
  } finally {
    detailLoading.value = false
  }
}

function openSettings() { settingsOpen.value = true }

function confirmDelete(inc: AlertIncident) {
  pendingDelete.value = inc
  deleteOpen.value = true
}

async function doDelete() {
  if (!pendingDelete.value) return
  deleting.value = true
  try {
    const r = await deleteAlertIncidents([pendingDelete.value.key], { hours: hours.value })
    ElMessage.success(`已删除故障（${r.data.matched} 个故障，清除 ${r.data.fingerprints} 条告警历史）`)
    deleteOpen.value = false
    pendingDelete.value = null
    await fetchAll()
  } catch (e: any) {
    ElMessage.error('删除失败: ' + (e?.message || e))
  } finally {
    deleting.value = false
  }
}

async function saveConfig() {
  savingConfig.value = true
  try {
    const labels = resourceLabelsInput.value.split(',').map((s: string) => s.trim()).filter(Boolean)
    if (labels.length === 0) labels.push('resource')
    const newCfg: DenoiseConfig = {
      window_minutes: cfg.value.window_minutes,
      storm_threshold: cfg.value.storm_threshold,
      resource_labels: labels,
    }
    await saveDenoiseConfig(newCfg)
    ElMessage.success('已保存')
    settingsOpen.value = false
    await fetchAll()
  } catch (e: any) {
    ElMessage.error('保存失败: ' + (e?.message || e))
  } finally {
    savingConfig.value = false
  }
}

function handleResize() {
  tlChart?.resize()
  noiseChart?.resize()
}

onMounted(async () => {
  await loadDenoiseConfig()
  await loadDataSources()
  await fetchAll()
  window.addEventListener('resize', handleResize)
})

onBeforeUnmount(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  window.removeEventListener('resize', handleResize)
  tlChart?.dispose()
  noiseChart?.dispose()
})
</script>

<style scoped>
.page-container { padding: 16px; }
.page-header h2 { display: flex; align-items: center; gap: 8px; font-size: 20px; margin: 0 0 6px; }
.page-header p { color: var(--el-text-color-secondary, #64748b); font-size: 13px; margin: 0 0 12px; line-height: 1.6; }
.section-card { background: var(--el-bg-color, #fff); border-radius: 8px; padding: 16px; box-shadow: 0 1px 3px rgba(0,0,0,0.05); }
.section-header { display: flex; justify-content: space-between; align-items: flex-start; flex-wrap: wrap; gap: 12px; margin-bottom: 12px; }
.action-bar { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
.auto-refresh { display: flex; align-items: center; gap: 6px; margin-left: 8px; }
.ar-label { color: var(--el-text-color-secondary); font-size: 12px; }
.updated-at { color: var(--el-text-color-secondary); font-size: 12px; }

.state-tabs { margin-bottom: 4px; }
.state-tabs :deep(.el-tabs__header) { margin-bottom: 12px; }

.stat-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 12px; }.s-card { display: flex; align-items: center; gap: 12px; padding: 14px; border-radius: 8px; background: var(--el-fill-color-light, #f8fafc); }
.s-icon { width: 40px; height: 40px; border-radius: 8px; display: flex; align-items: center; justify-content: center; font-size: 20px; color: #fff; }
.s-icon.raw { background: linear-gradient(135deg, #6366f1, #8b5cf6); }
.s-icon.evt { background: linear-gradient(135deg, #06b6d4, #0ea5e9); }
.s-icon.inc { background: linear-gradient(135deg, #8b5cf6, #a855f7); }
.s-icon.comp { background: linear-gradient(135deg, #10b981, #22c55e); }
.s-num { font-size: 22px; font-weight: 600; }
.s-num.cyan { color: #06b6d4; }
.s-num.indigo { color: #8b5cf6; }
.s-num.green { color: #10b981; }
.s-label { font-size: 13px; color: var(--el-text-color-primary); }
.s-sub { font-size: 11px; color: var(--el-text-color-secondary); }

.chart-panel { background: var(--el-fill-color-light, #f8fafc); border-radius: 8px; padding: 10px 12px; margin-bottom: 12px; }
.panel-title { display: flex; justify-content: space-between; align-items: center; font-size: 13px; font-weight: 500; margin-bottom: 6px; }
.panel-hint { font-size: 11px; color: var(--el-text-color-secondary); font-weight: 400; }
.chart { width: 100%; }
.tl-legend { display: flex; gap: 14px; font-size: 11px; color: var(--el-text-color-secondary); padding-top: 4px; }
.lg { display: flex; align-items: center; gap: 4px; }
.dot { display: inline-block; width: 10px; height: 10px; border-radius: 2px; }
.dot.critical { background: #ef4444; }
.dot.warning { background: #f59e0b; }
.dot.info { background: #3b82f6; }
.dot.storm { background: transparent; border: 1.5px solid #f59e0b; }

.list-state { padding: 40px; text-align: center; color: var(--el-text-color-secondary); }
.list-state.empty { color: var(--el-text-color-secondary); }
.list-state .is-loading { animation: rotating 1.5s linear infinite; }
@keyframes rotating { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }

.incident-list { display: flex; flex-direction: column; gap: 8px; }
.incident-row { display: flex; align-items: stretch; background: var(--el-fill-color-light, #f8fafc); border-radius: 6px; cursor: pointer; transition: all 0.15s; border: 1px solid transparent; }
.incident-row:hover { background: var(--el-color-primary-light-9, #eef2ff); border-color: var(--el-color-primary-light-5, #c7d2fe); }
.incident-row.ongoing { border-left: 3px solid var(--el-color-primary); }
.inc-bar { width: 4px; background: var(--el-color-primary); border-radius: 2px 0 0 2px; opacity: 0.6; }
.inc-body { flex: 1; padding: 10px 12px; }
.inc-title-line { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.inc-title { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; flex: 1; }
.inc-alertname { font-weight: 600; font-size: 14px; color: var(--el-text-color-primary); max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.inc-times { display: flex; gap: 14px; align-items: center; flex-shrink: 0; }
.inc-delete { flex-shrink: 0; opacity: 0; transition: opacity 0.15s; margin-top: -4px; }
.incident-row:hover .inc-delete { opacity: 1; }
.time-item { display: flex; flex-direction: column; gap: 2px; }
.time-label { font-size: 10px; color: var(--el-text-color-secondary); }
.time-value { font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--el-text-color-primary); }
.expand-caret { color: var(--el-text-color-secondary); }
.inc-meta { margin-top: 6px; font-size: 11px; color: var(--el-text-color-secondary); }
.meta-label { font-weight: 500; }
.meta-ds { color: var(--el-text-color-primary); }
.meta-sep { margin: 0 4px; color: var(--el-text-color-secondary); }

.tag-ongoing { background: #fef2f2; color: #dc2626; border: 1px solid #fecaca; }
.tag-resolved { background: #f0fdf4; color: #16a34a; border: 1px solid #bbf7d0; }
.tag-storm { background: #fffbeb; color: #d97706; border: 1px solid #fed7aa; font-weight: 600; }
.tag-count { background: #f1f5f9; color: #475569; }
.tag-instance { background: #eff6ff; color: #1d4ed8; border: 1px solid #bfdbfe; }
.tag-cluster { background: #f5f3ff; color: #7c3aed; border: 1px solid #ddd6fe; }

.detail-view { padding: 0 4px; }
.detail-header { display: flex; flex-direction: column; gap: 10px; padding: 12px 16px; background: var(--el-fill-color-light, #f8fafc); border-radius: 6px; margin-bottom: 12px; }
.detail-row { display: flex; gap: 12px; align-items: center; }
.d-label { width: 80px; font-size: 12px; color: var(--el-text-color-secondary); flex-shrink: 0; }
.d-value { font-size: 13px; color: var(--el-text-color-primary); }
.d-value.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.detail-hint { padding: 8px 12px; border-radius: 4px; font-size: 12px; line-height: 1.5; margin-bottom: 8px; }
.detail-hint.warn { background: #fffbeb; color: #92400e; border: 1px solid #fde68a; }
.detail-hint.info { background: #eff6ff; color: #1e40af; border: 1px solid #bfdbfe; }
.detail-section-title { font-size: 13px; font-weight: 600; margin: 12px 0 8px; color: var(--el-text-color-primary); }

.alert-list { display: flex; flex-direction: column; gap: 6px; }
.alert-row { display: flex; align-items: stretch; background: var(--el-fill-color-light, #f8fafc); border-radius: 4px; border: 1px solid transparent; }
.alert-row.ongoing { border-left: 3px solid var(--el-color-primary); }
.alert-bar { width: 3px; background: var(--el-color-primary); opacity: 0.5; border-radius: 2px 0 0 2px; }
.alert-body { flex: 1; padding: 8px 10px; }
.alert-line1 { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; margin-bottom: 4px; }
.alert-idx { font-size: 11px; color: var(--el-text-color-secondary); font-family: ui-monospace, monospace; }
.alert-time { font-size: 11px; color: var(--el-text-color-secondary); }
.alert-ds { font-size: 11px; color: var(--el-text-color-secondary); }
.alert-line2 { display: flex; gap: 12px; font-size: 12px; color: var(--el-text-color-regular); margin-bottom: 4px; }
.alert-val b { color: var(--el-color-primary); font-weight: 600; }
.alert-thr { color: var(--el-text-color-secondary); margin-left: 4px; }
.alert-dur { color: var(--el-text-color-secondary); }
.alert-labels { display: flex; gap: 4px; flex-wrap: wrap; }
.label-chip { background: var(--el-fill-color, #e2e8f0); padding: 1px 6px; border-radius: 3px; font-size: 10px; color: var(--el-text-color-secondary); font-family: ui-monospace, monospace; }

.form-hint { font-size: 11px; color: var(--el-text-color-secondary); margin-top: 4px; line-height: 1.4; }

.delete-confirm { padding: 4px 8px; }
.dc-warn { display: flex; align-items: center; gap: 6px; color: #dc2626; font-weight: 500; font-size: 14px; margin: 0 0 8px; }
.dc-target { font-size: 15px; font-weight: 600; margin: 0 0 6px; color: var(--el-text-color-primary); }
.dc-detail { font-size: 13px; color: var(--el-text-color-regular); margin: 0 0 10px; }
.dc-note { font-size: 12px; color: var(--el-text-color-secondary); line-height: 1.6; margin: 0; padding: 8px 10px; background: var(--el-fill-color-light, #f8fafc); border-radius: 4px; }
</style>
