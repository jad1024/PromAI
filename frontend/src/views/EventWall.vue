<template>
  <div class="ew">
    <div class="ew-hd">
      <div>
        <h2>事件墙</h2>
        <p>聚合告警、定向检查异常、PromAI 诊断记录，按时间倒序排列。</p>
      </div>
      <el-button size="small" @click="load">⟳ 刷新</el-button>
    </div>

    <div class="ew-filters">
      <div class="ew-fseg">
        <button :class="{ on: source === 'all' }" @click="source = 'all'">全部 <b>{{ events.length }}</b></button>
        <button v-for="s in sources" :key="s.k" :class="{ on: source === s.k }" @click="source = s.k">
          {{ s.label }} <b>{{ countSource(s.k) }}</b>
        </button>
      </div>
      <div class="ew-fseg">
        <button :class="{ on: severity === 'all' }" @click="severity = 'all'">全部级别</button>
        <button :class="{ on: severity === 'critical' }" @click="severity = 'critical'">严重</button>
        <button :class="{ on: severity === 'warning' }" @click="severity = 'warning'">警告</button>
        <button :class="{ on: severity === 'info' }" @click="severity = 'info'">信息</button>
      </div>
      <div class="ew-fseg ew-time-seg">
        <button v-for="r in ranges" :key="r.k" :class="{ on: range === r.k }" @click="setRange(r.k)">{{ r.label }}</button>
      </div>
      <el-date-picker
        v-model="customRange"
        type="datetimerange"
        size="small"
        value-format="YYYY-MM-DD HH:mm:ss"
        :clearable="true"
        range-separator="→"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
        style="width: 330px"
        @change="onCustomRange"
      />
    </div>

    <div class="ew-summary">
      时间范围：<b>{{ rangeLabel }}</b>
      <span class="ew-summary-sep">·</span>
      命中 <b>{{ filtered.length }}</b> 条 / 已加载 {{ events.length }} 条
      <span v-if="range !== 'all'" class="ew-clear" @click="setRange('all')">清除时间筛选</span>
    </div>

    <div class="ew-list" v-loading="loading">
      <div v-if="!filtered.length && !loading" class="ew-empty">
        当前时间范围内暂无事件。可放宽时间范围，或执行一次 PromAI 检查 / 等待告警触发。
      </div>
      <div v-for="e in filtered" :key="e.id" class="ew-item">
        <div class="ew-time">{{ formatTime(e.at) }}</div>
        <div class="ew-line">
          <span class="ew-dot" :class="e.severity"></span>
          <div class="ew-card">
            <div class="ew-card-hd">
              <span class="ew-src" :class="e.source">{{ sourceLabel(e.source) }}</span>
              <b>{{ e.title }}</b>
              <span class="ew-node">{{ e.node_name || '全局' }}</span>
            </div>
            <div class="ew-detail" v-if="e.detail">{{ e.detail }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getFiremapEvents, type FCEventItem, type FiremapEventsParams } from '../api'

const events = ref<FCEventItem[]>([])
const loading = ref(false)
const source = ref('all')
const severity = ref('all')
const range = ref('all')
const customRange = ref<any>(null)
const sources = [
  { k: 'alert', label: '告警' },
  { k: 'check', label: '检查' },
  { k: 'ai', label: 'AI 诊断' },
  { k: 'inspection', label: '巡检' },
  { k: 'external', label: '外部' },
]
const ranges = [
  { k: '1h', label: '近 1 小时', hours: 1 },
  { k: '6h', label: '近 6 小时', hours: 6 },
  { k: '24h', label: '近 24 小时', hours: 24 },
  { k: '7d', label: '近 7 天', hours: 168 },
  { k: 'all', label: '全部', hours: 0 },
]

const rangeLabel = computed(() => {
  if (range.value === 'custom') {
    return customRange.value ? `${customRange.value[0]} ~ ${customRange.value[1]}` : '自定义区间'
  }
  return ranges.find((r) => r.k === range.value)?.label || '全部'
})

function setRange(k: string) {
  range.value = k
  if (k !== 'custom') customRange.value = null
  load()
}

function onCustomRange(v: any) {
  if (Array.isArray(v) && v.length === 2 && v[0] && v[1]) {
    range.value = 'custom'
    load()
  } else if (range.value === 'custom') {
    range.value = 'all'
    load()
  }
}

function buildParams(): FiremapEventsParams {
  const p: FiremapEventsParams = { limit: 300 }
  if (range.value === 'custom' && Array.isArray(customRange.value)) {
    p.since = customRange.value[0]
    p.until = customRange.value[1]
  } else {
    const hit = ranges.find((r) => r.k === range.value)
    if (hit && hit.hours > 0) p.hours = hit.hours
  }
  return p
}

function sourceLabel(s: string) {
  const hit = sources.find((x) => x.k === s)
  return hit ? hit.label : s
}
function countSource(k: string) { return events.value.filter((e) => e.source === k).length }
function formatTime(s: string) {
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString('zh-CN', { hour12: false })
}
const filtered = computed(() => events.value.filter((e) => {
  if (source.value !== 'all' && e.source !== source.value) return false
  if (severity.value !== 'all' && e.severity !== severity.value) return false
  return true
}))

async function load() {
  loading.value = true
  try {
    events.value = (await getFiremapEvents(buildParams())).data.events || []
  } catch (e: any) {
    ElMessage.error('加载事件墙失败: ' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<style scoped>
.ew { padding: 16px 18px 40px; font-size: 12px; }
.ew-hd { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.ew-hd h2 { margin: 0 0 4px; font-size: 16px; }
.ew-hd p { margin: 0; font-size: 11.5px; color: var(--text-tertiary); }
.ew-filters { display: flex; gap: 10px; flex-wrap: wrap; align-items: center; margin-bottom: 10px; }
.ew-fseg { display: flex; gap: 3px; background: #fff; border: 1px solid var(--border); border-radius: 6px; padding: 3px; }
.ew-fseg button { border: 1px solid transparent; background: transparent; color: var(--text-secondary); border-radius: 4px; padding: 5px 9px; font-size: 11.5px; cursor: pointer; white-space: nowrap; }
.ew-fseg button.on { background: var(--cyan-dim); color: var(--cyan); border-color: var(--border-glow); font-weight: 600; }
.ew-time-seg { border-color: var(--border-glow); }
.ew-summary { font-size: 11.5px; color: var(--text-tertiary); margin-bottom: 10px; }
.ew-summary b { color: var(--text-secondary); font-weight: 600; }
.ew-summary-sep { margin: 0 6px; }
.ew-clear { margin-left: 12px; color: var(--cyan); cursor: pointer; text-decoration: underline; }
.ew-list { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 8px 16px 16px; }
.ew-item { display: flex; gap: 14px; padding: 10px 0; }
.ew-time { width: 130px; flex-shrink: 0; font-size: 10.5px; color: var(--text-tertiary); padding-top: 8px; }
.ew-line { flex: 1; display: flex; gap: 10px; border-left: 1px solid var(--border); padding-left: 16px; position: relative; }
.ew-dot { position: absolute; left: -4px; top: 12px; width: 7px; height: 7px; border-radius: 50%; background: #3fb37f; }
.ew-dot.critical { background: #e8635a; } .ew-dot.warning { background: #e0a026; } .ew-dot.info { background: #3a7bd5; }
.ew-card { flex: 1; border: 1px solid var(--border); border-radius: 6px; padding: 9px 12px; }
.ew-card:hover { border-color: var(--border-glow); }
.ew-card-hd { display: flex; align-items: center; gap: 8px; }
.ew-card-hd b { font-size: 12px; font-weight: 600; }
.ew-src { font-size: 10px; padding: 1px 6px; border-radius: 3px; background: var(--cyan-dim); color: var(--cyan); flex-shrink: 0; }
.ew-src.alert { background: var(--red-dim); color: var(--red); }
.ew-src.check { background: var(--amber-dim); color: var(--amber); }
.ew-src.ai { background: var(--emerald-dim); color: var(--emerald); }
.ew-node { margin-left: auto; font-size: 10.5px; color: var(--text-tertiary); }
.ew-detail { font-size: 10.5px; color: var(--text-secondary); margin-top: 5px; line-height: 1.6; word-break: break-all; }
.ew-empty { text-align: center; padding: 40px 10px; color: var(--text-tertiary); font-size: 11.5px; }
</style>
