<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Histogram /></el-icon> 告警时间线</h2>
      <p>按（数据源 × 规则）聚合，展示触发→通知→恢复的闭环时间线</p>
    </div>
    <div class="section-card">
      <div class="section-header">
        <div class="action-bar">
          <el-input v-model="keyword" placeholder="规则/数据源" style="width:160px;" clearable @keyup.enter="fetchData" />
          <el-date-picker v-model="dateRange" type="datetimerange" range-separator="至" start-placeholder="开始" end-placeholder="结束" value-format="YYYY-MM-DDTHH:mm:ssZ" style="width:280px;" @change="fetchData" />
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>

      <div v-if="loading" style="text-align:center;padding:40px;"><el-icon class="is-loading" :size="24"><Loading /></el-icon></div>
      <div v-else-if="groups.length === 0" style="text-align:center;padding:40px;color:var(--text-tertiary);">暂无时间线数据</div>

      <div v-else class="timeline-container">
        <div v-for="(grp, gi) in groups" :key="gi" class="timeline-group">
          <div class="group-header" @click="grp._collapsed = !grp._collapsed">
            <el-icon class="collapse-icon" :class="{ rotated: !grp._collapsed }"><ArrowRight /></el-icon>
            <el-tag size="small" style="background:rgba(99,102,241,0.12);color:#818cf8;border:none;">{{ grp.datasource_name }}</el-tag>
            <span class="group-rule-name">{{ grp.rule_name }}</span>
            <span class="group-count">{{ grp.entries.length }} 条</span>
            <span v-if="grp.next_notify_at" class="group-next">下轮 <strong>{{ fmt(grp.next_notify_at) }}</strong></span>
            <el-tag size="small" effect="dark" :style="groupStateStyle(grp)">{{ groupStateLabel(grp) }}</el-tag>
          </div>

          <div v-show="!grp._collapsed" class="timeline-line">
            <div v-for="(entry, ei) in grp.entries" :key="ei" class="timeline-item" :class="entry.type">
              <div class="timeline-dot" :class="entry.type"></div>
              <div class="timeline-content clickable" @click="entry._expanded = !entry._expanded">
                <div class="tl-row1">
                  <div class="tl-left">
                    <el-tag size="small" :style="tlTypeStyle(entry.type)">{{ tlTypeLabel(entry.type) }}</el-tag>
                    <el-tag v-if="entry.severity" size="small" :style="sevStyle(entry.severity)">{{ sevLabel(entry.severity) }}</el-tag>
                    <span class="tl-time">{{ fmt(entry.occurred_at) }}</span>
                  </div>
                  <div class="tl-right">
                    <template v-if="entry.type === 'notify'">
                      <span class="tl-channel">{{ channelLabel(entry.channel_type) }}</span>
                      <el-tag size="small" :style="notifyStyle(entry.notify_result||'')" v-if="entry.notify_result">{{ notifyLabel(entry.notify_result) }}</el-tag>
                      <el-tag v-else size="small" style="background:rgba(99,102,241,0.12);color:#818cf8;border:none;">成功</el-tag>
                      <el-tag v-if="entry.error" size="small" style="background:rgba(239,68,68,0.15);color:#ef4444;border:none;margin-left:4px;">{{ entry.error }}</el-tag>
                    </template>
                    <template v-else>
                      <span v-if="entry.value !== undefined" class="tl-value">{{ entry.value?.toFixed(2) }} / {{ entry.threshold?.toFixed(2) }}</span>
                      <el-tag v-if="entry.notify_result" size="small" :style="notifyStyle(entry.notify_result)" style="margin-left:4px;">{{ notifyLabel(entry.notify_result) }}</el-tag>
                      <el-icon v-if="entry._expanded !== true" style="margin-left:4px;color:var(--text-tertiary);"><ArrowDown /></el-icon>
                    </template>
                  </div>
                </div>
                <div v-if="entry._expanded && entry.type === 'notify'" class="tl-detail">
                  <div class="tl-content-label">实际发送内容 (Markdown):</div>
                  <pre class="tl-content">{{ entry.content }}</pre>
                </div>
                <div v-if="entry._expanded && entry.type !== 'notify'" class="tl-detail">
                  <div v-if="entry.annotations_json" class="tl-desc" v-html="renderAnn(entry.annotations_json)"></div>
                  <div v-if="entry.labels_json" class="tl-labels">
                    <el-tag v-for="(v,k) in safeLabels(entry.labels_json)" :key="k" size="small" style="margin:2px;background:rgba(99,102,241,0.08);color:#818cf8;border:none;">{{ k }}={{ v }}</el-tag>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getAlertHistoryTimeline } from '../api'
import type { TimelineGroup } from '../types/alerting'

const loading = ref(false)
const groups = ref<(TimelineGroup & { _collapsed?: boolean })[]>([])
const keyword = ref('')
const dateRange = ref<[string, string] | null>(null)

function fmt(t: string) {
  if (!t) return '-'
  const d = new Date(t)
  return isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

function tlTypeLabel(t: string) {
  return { firing: '触发', resolved: '恢复', notify: '通知' }[t] || t
}
function tlTypeStyle(t: string) {
  const m: Record<string, any> = {
    firing: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    resolved: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    notify: { background: 'rgba(59,130,246,0.15)', color: '#3b82f6', border: 'none' },
  }
  return m[t] || m.firing
}

function sevStyle(s: string | undefined) {
  const m: Record<string, any> = {
    critical: { background: '#ef4444', color: '#fff', border: 'none' },
    warning: { background: '#f59e0b', color: '#fff', border: 'none' },
    info: { background: '#3b82f6', color: '#fff', border: 'none' },
  }
  return (s && m[s]) || m.warning
}
function sevLabel(s: string | undefined) {
  if (!s) return ''
  return { critical: '严重', warning: '警告', info: '提醒' }[s] || s
}
function notifyStyle(s: string) {
  const m: Record<string, any> = {
    success: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    failed: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    throttled: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
  }
  return m[s] || {}
}
function notifyLabel(s: string) {
  return { success: '成功', failed: '失败', throttled: '限流' }[s] || s
}
function channelLabel(t: string | undefined) {
  if (!t) return '-'
  return { wechat_work: '企业微信', mail: '邮件', webhook: 'Webhook', dingtalk: '钉钉' }[t] || t
}

function safeLabels(s: string | undefined): Record<string, string> {
  if (!s) return {}
  try { const o = JSON.parse(s); return typeof o === 'object' && o !== null ? o : {} }
  catch { return {} }
}

function renderAnn(s: string | undefined) {
  if (!s) return ''
  try {
    const o = JSON.parse(s)
    if (typeof o === 'object' && o !== null) {
      return Object.values(o).filter(Boolean).join('<br>')
    }
    return String(o)
  } catch {
    // Plain text annotations
    // Replace {{ $value }} / {{ $threshold }} with actual values from entry context
    return s.replace(/\{\{\s*\$value\s*\}\}/g, '{{ $value }}')
         .replace(/\{\{\s*\$threshold\s*\}\}/g, '{{ $threshold }}')
  }
}

function groupStateStyle(grp: TimelineGroup) {
  const last = grp.entries[grp.entries.length - 1]
  if (!last) return { background: '#94a3b8', color: '#fff', border: 'none' }
  if (last.type === 'resolved') return { background: '#10b981', color: '#fff', border: 'none' }
  if (last.type === 'firing') return { background: '#ef4444', color: '#fff', border: 'none' }
  return { background: '#94a3b8', color: '#fff', border: 'none' }
}
function groupStateLabel(grp: TimelineGroup) {
  const last = grp.entries[grp.entries.length - 1]
  if (!last) return '无状态'
  if (last.type === 'resolved') return '已恢复'
  if (last.type === 'firing') return '告警中'
  return '未知'
}

async function fetchData() {
  loading.value = true
  try {
    const p: any = {}
    if (keyword.value) p.keyword = keyword.value
    if (dateRange.value?.[0]) p.from = dateRange.value[0]
    if (dateRange.value?.[1]) p.to = dateRange.value[1]
    const r = await getAlertHistoryTimeline(p)
    groups.value = (r.data.groups || []).map((g: any) => ({
      ...g,
      _collapsed: true,
      entries: g.entries.map((e: any) => ({ ...e, _expanded: false })),
    }))
  } catch { /* ignore */ }
  finally { loading.value = false }
}

onMounted(fetchData)
</script>

<style scoped>
.timeline-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 8px 0;
}

.timeline-group {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}

.group-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 16px;
  background: rgba(99, 102, 241, 0.04);
  border-bottom: 1px solid var(--border);
  cursor: pointer;
  user-select: none;
}
.group-header:hover {
  background: rgba(99, 102, 241, 0.08);
}

.collapse-icon {
  font-size: 14px;
  transition: transform .2s;
  color: var(--text-tertiary);
}
.collapse-icon.rotated {
  transform: rotate(90deg);
}

.group-rule-name {
  flex: 1;
  font-weight: 600;
  font-size: 14px;
}

.group-count {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-right: 4px;
}

.group-next {
  font-size: 11px;
  color: var(--danger);
  margin-right: 6px;
}

.timeline-line {
  position: relative;
  padding: 8px 0 8px 28px;
}

.timeline-line::before {
  content: '';
  position: absolute;
  left: 16px;
  top: 0;
  bottom: 0;
  width: 2px;
  background: var(--border);
}

.timeline-item {
  position: relative;
  padding: 3px 12px 3px 0;
}

.timeline-dot {
  position: absolute;
  left: -10px;
  top: 10px;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  z-index: 1;
  border: 2px solid var(--bg-card);
}
.timeline-dot.firing { background: #ef4444; }
.timeline-dot.resolved { background: #10b981; }
.timeline-dot.notify { background: #3b82f6; }

.timeline-content {
  border-radius: 6px;
  padding: 5px 8px;
  transition: background .15s;
}
.timeline-content.clickable { cursor: pointer; }
.timeline-content.clickable:hover { background: rgba(148, 163, 184, 0.06); }

.tl-row1 {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.tl-left, .tl-right {
  display: flex;
  align-items: center;
  gap: 4px;
}

.tl-time {
  font-size: 12px;
  color: var(--text-tertiary);
}

.tl-value {
  font-size: 12px;
  color: var(--cyan);
  font-weight: 600;
}

.tl-channel {
  font-size: 12px;
  color: var(--text-secondary);
}

.tl-detail {
  margin-top: 4px;
  padding: 4px 6px;
  background: rgba(0,0,0,0.02);
  border-radius: 4px;
}

.tl-desc {
  font-size: 12px;
  color: var(--text-secondary);
  line-height: 1.5;
  margin-bottom: 4px;
}

.tl-labels {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}

.tl-content-label {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-bottom: 4px;
}

.tl-content {
  background: rgba(0,0,0,0.04);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 8px;
  font-size: 12px;
  line-height: 1.5;
  max-height: 300px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
</style>
