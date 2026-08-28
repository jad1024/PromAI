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
          <el-select v-model="filters.severity" placeholder="告警级别" clearable style="width:120px;" @change="fetchEvents">
            <el-option label="严重" value="critical" />
            <el-option label="警告" value="warning" />
            <el-option label="提醒" value="info" />
          </el-select>
          <el-select v-model="filters.datasource_id" placeholder="集群/数据源" clearable filterable style="width:170px;" @change="fetchEvents">
            <el-option v-for="ds in datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
          </el-select>
          <el-input v-model="filters.keyword" placeholder="搜索规则/集群/标签" style="width:200px;" clearable @keyup.enter="fetchEvents">
            <template #suffix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-button plain @click="fetchAll"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>

      <!-- 聚合效果概览 -->
      <div class="stat-strip">
        <div class="stat-item">
          <div class="stat-num">{{ agg.total_raw }}</div>
          <div class="stat-label">原始告警条数</div>
        </div>
        <div class="stat-arrow"><el-icon><Right /></el-icon></div>
        <div class="stat-item highlight">
          <div class="stat-num">{{ agg.total_events }}</div>
          <div class="stat-label">聚合后事件数</div>
        </div>
        <div class="stat-item">
          <div class="stat-num compress">-{{ agg.compression }}%</div>
          <div class="stat-label">压缩率</div>
        </div>
        <div class="stat-item">
          <div class="stat-num flapping">{{ flapEventCount }}</div>
          <div class="stat-label">震荡事件</div>
        </div>
        <div class="stat-item">
          <div class="stat-num correlated">{{ correlatedEventCount }}</div>
          <div class="stat-label">跨集群关联</div>
        </div>
      </div>

      <!-- TOP 噪音排行 -->
      <div v-if="noiseItems.length > 0" class="noise-panel">
        <div class="panel-title">
          <span>TOP 噪音规则</span>
          <span class="panel-hint">触发次数最多 / 震荡最频繁的规则，优先去 Alertmanager 调阈值或静默</span>
        </div>
        <div class="noise-list">
          <div v-for="(n, idx) in noiseItems" :key="n.rule_id + '-' + n.datasource_id" class="noise-row">
            <div class="noise-rank" :class="{ top: idx < 3 }">{{ idx + 1 }}</div>
            <div class="noise-main">
              <div class="noise-name-line">
                <span class="noise-ds">{{ n.datasource_name || '-' }}</span>
                <span class="noise-divider">/</span>
                <span class="noise-rule">{{ ruleNameDisplay(n.rule_name) }}</span>
                <el-tag v-if="n.flapping" size="small" class="tag-flap">震荡 x{{ n.flap_count }}</el-tag>
                <el-tag size="small" :style="sevStyle(n.severity)" style="margin-left:6px;">{{ sevLabel(n.severity) }}</el-tag>
              </div>
              <div class="noise-bar-wrap">
                <div class="noise-bar" :style="{ width: noiseBarWidth(n), background: n.flapping ? '#f59e0b' : '#ef4444' }"></div>
              </div>
            </div>
            <div class="noise-count">
              <span class="cnt-num">{{ n.firing_count }}</span>
              <span class="cnt-unit">次</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 事件列表 -->
      <div v-if="loading" style="text-align:center;padding:40px;"><el-icon class="is-loading" :size="24"><Loading /></el-icon></div>
      <div v-else-if="events.length === 0" style="text-align:center;padding:40px;color:var(--text-tertiary);">
        所选时间窗内没有告警记录
      </div>

      <div v-else class="event-list">
        <div v-for="(ev, i) in events" :key="ev.rule_id + '-' + ev.datasource_id + '-' + i" class="event-row">
          <div class="event-bar" :class="ev.state === 'ongoing' ? 'ongoing' : 'resolved'"></div>
          <div class="event-body">
            <div class="event-main">
              <div class="event-title-line">
                <div class="event-title">
                  <span class="event-ds">{{ ev.datasource_name || '-' }}</span>
                  <span class="event-divider">/</span>
                  <span class="event-rule">{{ ruleNameDisplay(ev.rule_name) }}</span>
                  <el-tag v-if="ev.severity" size="small" :style="sevStyle(ev.severity)" style="margin-left:8px;">{{ sevLabel(ev.severity) }}</el-tag>
                  <el-tag size="small" :class="ev.state === 'ongoing' ? 'tag-ongoing' : 'tag-resolved'" style="margin-left:6px;">
                    {{ ev.state === 'ongoing' ? '持续中' : '已恢复' }}
                  </el-tag>
                  <el-tag v-if="ev.flapping" size="small" class="tag-flap" style="margin-left:6px;">震荡 x{{ ev.flap_count }}</el-tag>
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
                </div>
              </div>
              <div class="event-meta">
                <span class="meta-val">
                  peak={{ formatNum(ev.peak_value) }}
                  <span class="meta-threshold">threshold={{ formatNum(ev.threshold) }}</span>
                </span>
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
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import dayjs from 'dayjs'
import { getAlertEvents, getAlertNoiseTop, getAllDataSources } from '../api'
import type { AlertEvent, NoiseTopRule } from '../types/alerting'
import type { DataSource } from '../types'

const loading = ref(false)
const events = ref<AlertEvent[]>([])
const noiseItems = ref<NoiseTopRule[]>([])
const datasources = ref<DataSource[]>([])
const hours = ref<number>(24)
const agg = ref<{ total_raw: number; total_events: number; compression: number }>({
  total_raw: 0, total_events: 0, compression: 0,
})
const filters = ref<{ severity: string; datasource_id: number | ''; keyword: string }>({
  severity: '', datasource_id: '', keyword: '',
})

const flapEventCount = computed(() => events.value.filter(e => e.flapping).length)
const correlatedEventCount = computed(() => events.value.filter(e => (e.correlated_datasources || []).length > 0).length)
const maxNoiseCount = computed(() => Math.max(1, ...noiseItems.value.map(n => n.firing_count)))

// 外部告警规则名格式 "[源名] 规则名"：去掉前缀，只展示规则名
function ruleNameDisplay(ruleName: string): string {
  const m = ruleName.match(/^\[([^\]]+)\]\s*(.*)$/)
  return m ? m[2] || ruleName : ruleName
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
function formatNum(v: number | null | undefined) {
  if (v === null || v === undefined || isNaN(Number(v))) return '-'
  if (Math.abs(v) >= 100) return Number(v).toFixed(2)
  return Number(v).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}
function fmt(t: string | undefined | null) {
  if (!t) return '-'
  const d = dayjs(t)
  if (!d.isValid()) return '-'
  if (d.year() < 2000) return '-' // 零值时间不展示
  return d.format('MM-DD HH:mm:ss')
}
function noiseBarWidth(n: NoiseTopRule) {
  const pct = (n.firing_count / maxNoiseCount.value) * 100
  return `${Math.max(3, pct)}%`
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
  } catch { /* ignore */ }
  finally { loading.value = false }
}

async function fetchNoiseTop() {
  try {
    const r = await getAlertNoiseTop({ hours: hours.value, limit: 8 })
    noiseItems.value = r.data.items || []
  } catch { /* ignore */ }
}

async function fetchAll() {
  await Promise.all([fetchEvents(), fetchNoiseTop()])
}

onMounted(async () => {
  try {
    const ds = await getAllDataSources()
    datasources.value = ds.data || []
  } catch { /* ignore */ }
  fetchAll()
})
</script>

<style scoped>
.stat-strip {
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 16px 4px;
  margin-bottom: 4px;
  border-bottom: 1px solid var(--border);
}
.stat-item {
  min-width: 84px;
}
.stat-item.highlight .stat-num {
  color: var(--cyan);
}
.stat-num {
  font-size: 22px;
  font-weight: 600;
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
  line-height: 1.2;
}
.stat-num.compress { color: #10b981; }
.stat-num.flapping { color: #f59e0b; }
.stat-num.correlated { color: #818cf8; }
.stat-label {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-top: 2px;
}
.stat-arrow {
  color: var(--text-tertiary);
  font-size: 16px;
}

.noise-panel {
  padding: 16px 4px;
  border-bottom: 1px solid var(--border);
}
.panel-title {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 12px;
}
.panel-hint {
  font-size: 12px;
  font-weight: 400;
  color: var(--text-tertiary);
}
.noise-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.noise-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.noise-rank {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  background: var(--bg-elevated);
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.noise-rank.top {
  background: rgba(239, 68, 68, 0.12);
  color: #ef4444;
}
.noise-main {
  flex: 1;
  min-width: 0;
}
.noise-name-line {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 5px;
  font-size: 13px;
}
.noise-ds { color: var(--text-secondary); }
.noise-divider { color: var(--text-tertiary); margin: 0 2px; }
.noise-rule {
  color: var(--text-primary);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.noise-bar-wrap {
  height: 6px;
  background: var(--bg-elevated);
  border-radius: 3px;
  overflow: hidden;
}
.noise-bar {
  height: 100%;
  border-radius: 3px;
  transition: width .3s ease;
}
.noise-count {
  width: 62px;
  text-align: right;
  flex-shrink: 0;
}
.cnt-num {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
}
.cnt-unit {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-left: 2px;
}

.event-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px 0;
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
  border-color: rgba(99, 102, 241, 0.4);
  box-shadow: 0 2px 10px rgba(0,0,0,0.04);
}
.event-bar { width: 4px; flex-shrink: 0; }
.event-bar.ongoing { background: #ef4444; }
.event-bar.resolved { background: #10b981; }

.event-body {
  flex: 1;
  display: flex;
  align-items: center;
  padding: 14px 16px;
  min-width: 0;
}
.event-main { flex: 1; min-width: 0; }
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
.time-value {
  font-size: 12px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
}
.event-meta { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; }
.meta-val {
  font-size: 12px;
  color: var(--cyan);
  font-family: 'SF Mono', Monaco, monospace;
  margin-right: 4px;
}
.meta-threshold { color: var(--text-tertiary); margin-left: 4px; }

.tag-ongoing { background: rgba(239,68,68,0.15); color: #ef4444; border: none; }
.tag-resolved { background: rgba(16,185,129,0.15); color: #10b981; border: none; }
.tag-flap { background: rgba(245,158,11,0.15); color: #f59e0b; border: none; }
.tag-count {
  background: var(--bg-elevated);
  color: var(--text-tertiary);
  border: none;
  font-family: 'SF Mono', Monaco, monospace;
}
.tag-correlated {
  background: rgba(99,102,241,0.10);
  color: #818cf8;
  border: none;
}
</style>
