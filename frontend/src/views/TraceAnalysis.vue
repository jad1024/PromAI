<template>
  <div class="page-container trace-page">
    <div class="trace-hd">
      <div>
        <h2>链路分析</h2>
        <p class="trace-sub">从 Java 应用日志的 traceId / requestId 串出一次请求的调用链，无需额外 APM 系统。业务号（如 qid）不唯一，不能用于串链。</p>
      </div>
      <el-tag v-if="configured" type="success" size="small" effect="plain">● 数据源已连接</el-tag>
      <el-tag v-else type="danger" size="small" effect="plain">● 数据源未配置</el-tag>
    </div>

    <el-alert v-if="!configured" type="warning" :closable="false" class="trace-alert">
      <template #title>链路分析依赖日志数据源。请配置 <code>PROMAI_VICTORIALOGS_URL</code> 并确保 Java 应用日志中包含 traceId / requestId 字段。</template>
    </el-alert>
    <el-alert v-else-if="!loading && !recent.length && !spans.length" type="info" :closable="false" class="trace-alert">
      <template #title>最近日志中未发现 traceId / requestId，链路分析暂无可串链的数据。</template>
      <template #default>
        <div class="trace-guide">
          链路分析要求每条日志带一个全局唯一的请求标识。请在 Java 应用的日志格式里加入 MDC 变量
          <code>%X{traceId:-}</code>，或在日志字段中输出 <code>traceId</code> / <code>requestId</code>；
          由链路过滤器或网关把标识写入 MDC 后，日志分析页即可看到该字段并跳到此处串链。
          业务号（qid、订单号等）会重复，不能作为链路 ID。
        </div>
      </template>
    </el-alert>

    <div class="trace-bar">
      <el-input v-model="traceId" size="small" placeholder="输入 traceId / requestId，或从下方最近链路中选择" clearable
                @keyup.enter="doQuery" style="flex:1; max-width:560px" />
      <el-select v-model="linkField" size="small" style="width:126px" @change="loadRecent">
        <el-option label="自动识别" value="auto" /><el-option label="traceId" value="trace_id" />
        <el-option label="requestId" value="request_id" />
      </el-select>
      <el-select v-model="range" size="small" style="width:120px">
        <el-option label="最近 1 小时" value="1h" /><el-option label="最近 6 小时" value="6h" />
        <el-option label="最近 24 小时" value="24h" /><el-option label="最近 7 天" value="7d" />
      </el-select>
      <el-button type="primary" size="small" :loading="loading" @click="doQuery">查询链路</el-button>
    </div>

    <div v-if="spans.length" class="trace-result">
      <div class="trace-summary">
        <el-tag type="primary" size="small">{{ fieldLabel }}: {{ traceId }}</el-tag>
        <span>共 {{ spans.length }} 个日志节点</span>
        <span v-if="firstAt() && lastAt()">时间跨度 {{ firstAt() }} → {{ lastAt() }}</span>
        <span v-if="spans.length > 100">仅展示前 100 条，共 {{ spans.length }} 条</span>
      </div>
      <el-timeline>
        <el-timeline-item v-for="(s, i) in visibleSpans" :key="i"
                          :timestamp="s.time || '—'" placement="top"
                          :color="spanColor(s.level || '')">
          <div class="trace-span">
            <div class="trace-span-hd">
              <el-tag v-if="s.service" size="small" effect="plain">{{ s.service }}</el-tag>
              <el-tag v-if="s.level" size="small" :type="levelTag(s.level)">{{ s.level }}</el-tag>
              <span v-if="s.method" class="trace-method">{{ s.method }}</span>
              <span v-if="s.span_id" class="trace-spanid">span: {{ s.span_id }}</span>
              <el-button link size="small" @click="showRaw(s)">原始日志</el-button>
            </div>
            <div class="trace-msg">{{ s.message || '（无消息内容）' }}</div>
          </div>
        </el-timeline-item>
      </el-timeline>
    </div>

    <div v-else-if="searched && !loading" class="trace-empty">
      <el-empty description="未找到该关联号的日志。请确认 ID 正确、关联字段匹配，或扩大时间范围。" />
    </div>

    <div class="trace-recent">
      <div class="trace-recent-hd">
        <h3>最近链路</h3>
        <el-button size="small" @click="loadRecent">刷新</el-button>
      </div>
      <el-table :data="recent" size="small" border max-height="360">
        <el-table-column prop="last_at" label="最近时间" width="190" />
        <el-table-column prop="service" label="服务" width="150">
          <template #default="{ row }">{{ row.service || '—' }}</template>
        </el-table-column>
        <el-table-column prop="trace_id" label="关联 ID" min-width="240">
          <template #default="{ row }">
            <el-button link size="small" class="trace-link-id" @click="chooseTrace(row.trace_id, row.field)">{{ row.trace_id }}</el-button>
          </template>
        </el-table-column>
        <el-table-column prop="message" label="摘要" min-width="300" />
      </el-table>
      <div v-if="!recent.length && configured" class="trace-no-recent">最近日志中未发现 traceId / requestId。请在 Java 应用中开启请求标识打印后重试。</div>
    </div>

    <el-drawer v-model="rawOpen" title="原始日志" size="46%">
      <pre class="trace-raw-json">{{ JSON.stringify(rawRow, null, 2) }}</pre>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  getLogStatus, getRecentTraces, getTraceByID,
  type TraceItem, type TraceSpan,
} from '../api'

const route = useRoute()
const router = useRouter()
const configured = ref(false)
const traceId = ref((route.query.id as string) || '')
const linkField = ref((route.query.field as string) || 'auto')
const range = ref('24h')
const visibleSpans = computed(() => spans.value.slice(0, 100))
const fieldLabel = computed(() => ({
  auto: '关联 ID', trace_id: 'traceId', request_id: 'requestId',
}[linkField.value] || '关联 ID'))
const loading = ref(false)
const searched = ref(false)
const spans = ref<TraceSpan[]>([])
const recent = ref<TraceItem[]>([])
const rawOpen = ref(false)
const rawRow = ref<any>(null)

function levelTag(level: string) {
  const l = String(level).toLowerCase()
  if (l.includes('error') || l === 'fatal') return 'danger'
  if (l.includes('warn')) return 'warning'
  if (l.includes('info')) return 'success'
  return 'info'
}
function spanColor(level: string) {
  const l = String(level || '').toLowerCase()
  if (l.includes('error') || l === 'fatal') return '#ef4444'
  if (l.includes('warn')) return '#f59e0b'
  return '#7c3aed'
}
function firstAt() { return spans.value[0]?.time || '' }
function lastAt() { return spans.value[spans.value.length - 1]?.time || '' }
function showRaw(s: TraceSpan) { rawRow.value = s.raw; rawOpen.value = true }
function chooseTrace(id: string, f?: string) {
  if (f && f !== 'auto') linkField.value = f
  traceId.value = id
  router.replace({ path: '/trace-analysis', query: { id, field: linkField.value } })
  doQuery()
}
async function doQuery() {
  if (!configured.value) return
  if (!traceId.value.trim()) { ElMessage.warning('请输入关联 ID'); return }
  loading.value = true; searched.value = true
  try {
    const r = await getTraceByID(traceId.value.trim(), { start: range.value, field: linkField.value })
    spans.value = r.data.spans || []
  } catch (e: any) {
    ElMessage.error('链路查询失败: ' + (e?.response?.data?.error || e.message))
    spans.value = []
  } finally { loading.value = false }
}
async function loadRecent() {
  if (!configured.value) return
  try { recent.value = (await getRecentTraces({ start: range.value, field: linkField.value })).data.items || [] }
  catch { recent.value = [] }
}
onMounted(async () => {
  try { configured.value = (await getLogStatus()).data.configured } catch { configured.value = false }
  if (!configured.value) return
  await loadRecent()
  if (traceId.value) await doQuery()
})
</script>

<style scoped>
.trace-page { padding: 16px 20px; }
.trace-hd { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.trace-hd h2 { margin: 0; font-size: 19px; font-weight: 600; }
.trace-sub { margin: 3px 0 0; font-size: 12px; color: var(--text-tertiary); }
.trace-alert { margin-bottom: 12px; }
.trace-alert code { background: rgba(0,0,0,.06); padding: 1px 5px; border-radius: 3px; }
.trace-guide { font-size: 12px; line-height: 1.8; color: var(--text-secondary); }
.trace-link-id.el-button {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: .2px;
  color: var(--primary, #6d4bc4) !important;
}
.trace-bar { display: flex; gap: 8px; margin-bottom: 16px; }
.trace-summary { display: flex; gap: 14px; align-items: center; font-size: 12px; color: var(--text-secondary); margin-bottom: 14px; }
.trace-span { background: var(--bg-card); border: 1px solid var(--border); border-radius: 6px; padding: 10px 12px; }
.trace-span-hd { display: flex; gap: 8px; align-items: center; margin-bottom: 6px; }
.trace-method { font-family: monospace; font-size: 12px; color: var(--text-secondary); }
.trace-spanid { font-size: 11px; color: var(--text-tertiary); }
.trace-msg { font-family: monospace; font-size: 12px; white-space: pre-wrap; word-break: break-all; }
.trace-empty { padding: 20px 0; }
.trace-recent { margin-top: 24px; }
.trace-recent-hd { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.trace-recent-hd h3 { margin: 0; font-size: 15px; }
.trace-no-recent { font-size: 12px; color: var(--text-tertiary); padding-top: 8px; }
.trace-raw-json { font-size: 12px; white-space: pre-wrap; word-break: break-all; }
</style>
