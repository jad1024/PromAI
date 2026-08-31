<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><DataAnalysis /></el-icon> AI 分析记录</h2>
      <p>AI 巡检 / 告警根因 / LTS 告警巡检的 token 消耗与日志证据留档</p>
    </div>

    <!-- token 汇总卡 -->
    <div class="summary-cards" v-loading="summaryLoading">
      <div class="stat-card">
        <div class="stat-label">今日消耗</div>
        <div class="stat-value">{{ formatTokens(summary?.today.total_tokens ?? 0) }}</div>
        <div class="stat-sub">{{ summary?.today.calls ?? 0 }} 次调用 · 约 ¥{{ formatCost(summary?.today.cost_est ?? 0) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">本月消耗</div>
        <div class="stat-value">{{ formatTokens(summary?.month.total_tokens ?? 0) }}</div>
        <div class="stat-sub">{{ summary?.month.calls ?? 0 }} 次调用 · 约 ¥{{ formatCost(summary?.month.cost_est ?? 0) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">
          日预算
          <el-button size="small" text class="refresh-btn" :loading="summaryLoading" @click="fetchSummary">
            <el-icon><Refresh /></el-icon>
          </el-button>
        </div>
        <div class="stat-value">{{ budgetLabel }}</div>
        <div class="stat-sub" :style="budgetUsageStyle">{{ budgetUsageLabel }}</div>
        <el-progress
          v-if="hasBudget"
          :percentage="budgetUsagePct"
          :stroke-width="6"
          :color="budgetProgressColor"
          :show-text="false"
          class="budget-progress"
        />
      </div>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 分析记录</h3>
        <div class="header-actions">
          <el-input v-model="keyword" placeholder="搜索 ref / 模型 / 结果" clearable style="width: 220px;" @keyup.enter="onSearch" @clear="onSearch">
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-select v-model="typeFilter" placeholder="类型" clearable style="width: 150px;" @change="onSearch">
            <el-option label="LTS 告警巡检" value="lts_alert" />
            <el-option label="巡检分析" value="inspection" />
            <el-option label="告警根因" value="alert" />
            <el-option label="外部告警" value="alert_external" />
          </el-select>
        </div>
      </div>

      <el-table :data="records" v-loading="loading" stripe @row-click="openDetail">
        <el-table-column label="时间" width="165">
          <template #default="{ row }">{{ dayjs(row.created_at).format('MM-DD HH:mm:ss') }}</template>
        </el-table-column>
        <el-table-column label="类型" width="130">
          <template #default="{ row }">
            <el-tag size="small" :type="typeTagType(row.type)" effect="light">{{ typeLabel(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ref_id" label="关联 ID" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono">{{ row.ref_id || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="model_name" label="模型" width="150" show-overflow-tooltip />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.status === 'success'" type="success" effect="dark">成功</el-tag>
            <el-tag v-else type="danger" effect="dark">失败</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="Token" width="120" align="right">
          <template #default="{ row }">
            <span :title="`输入 ${row.prompt_tokens} / 输出 ${row.completion_tokens}`">
              {{ formatTokens(row.total_tokens) }}
              <el-tooltip v-if="row.tokens_estimated" content="token 为估算值（模型未返回 usage）" placement="top">
                <span class="estimated-mark">≈</span>
              </el-tooltip>
            </span>
          </template>
        </el-table-column>
        <el-table-column label="成本" width="90" align="right">
          <template #default="{ row }">
            <span v-if="row.cost_est != null">¥{{ row.cost_est.toFixed(3) }}</span>
            <span v-else class="muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="日志留档" width="90" align="center">
          <template #default="{ row }">
            <el-tag v-if="row.has_logs" size="small" type="info" effect="plain">有</el-tag>
            <span v-else class="muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text class="link-btn link-edit" @click.stop="openDetail(row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-if="!loading && records.length === 0" :description="keyword || typeFilter ? '未找到匹配的记录' : '暂无分析记录，触发一次 AI 巡检后在此查看'" />

      <div v-if="total > pageSize" class="pagination-wrap">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next"
          background
          @change="fetchRecords"
        />
      </div>
    </div>

    <!-- 详情对话框 -->
    <el-dialog v-model="detailVisible" title="分析记录详情" width="860px" top="6vh" destroy-on-close>
      <div v-loading="detailLoading" v-if="detail">
        <div class="detail-meta">
          <el-tag size="small" :type="typeTagType(detail.type)" effect="light">{{ typeLabel(detail.type) }}</el-tag>
          <el-tag size="small" :type="detail.status === 'success' ? 'success' : 'danger'" effect="dark">{{ detail.status === 'success' ? '成功' : '失败' }}</el-tag>
          <span>模型：{{ detail.model_name }}</span>
          <span>耗时：{{ detail.duration_ms }}ms</span>
          <span>Token：{{ formatTokens(detail.total_tokens) }}<template v-if="detail.tokens_estimated"> ≈</template></span>
          <span v-if="detail.cost_est != null">成本：¥{{ detail.cost_est.toFixed(4) }}</span>
          <span v-if="detail.ref_id">关联：<span class="mono">{{ detail.ref_id }}</span></span>
        </div>

        <el-tabs v-model="activeTab">
          <el-tab-pane label="分析结论" name="result">
            <div class="markdown-body" v-html="renderMarkdown(detail.result)"></div>
          </el-tab-pane>
          <el-tab-pane :name="'logs'">
            <template #label>
              <span>日志证据</span>
              <el-badge v-if="!hasLogs" is-dot class="logs-tab-badge" />
            </template>
            <div v-if="hasLogs" class="logs-panel">
              <!-- 检索参数 -->
              <div class="logs-block" v-if="detail.logs?.query">
                <div class="logs-block-title">检索参数</div>
                <div class="logs-kv">
                  <template v-for="(v, k) in detail.logs.query" :key="k">
                    <span class="logs-k">{{ k }}:</span>
                    <span class="logs-v">{{ v }}</span>
                  </template>
                </div>
              </div>

              <!-- 折叠模式统计 -->
              <div class="logs-block" v-if="foldedList.length">
                <div class="logs-block-title">折叠模式统计（{{ foldedList.length }} 种）</div>
                <el-table :data="foldedList" size="small" max-height="260">
                  <el-table-column label="级别" width="70">
                    <template #default="{ row }">
                      <el-tag size="small" :type="row.level === 'ERROR' || row.level === 'FATAL' ? 'danger' : 'warning'" effect="plain">{{ row.level || '-' }}</el-tag>
                    </template>
                  </el-table-column>
                  <el-table-column prop="count" label="次数" width="70" align="right" />
                  <el-table-column prop="logger" label="Logger" min-width="160" show-overflow-tooltip />
                  <el-table-column prop="signature" label="模式签名" min-width="260" show-overflow-tooltip />
                  <el-table-column label="首/末次" width="150">
                    <template #default="{ row }">
                      <span v-if="row.first_at" class="time-range">{{ row.first_at }} ~ {{ row.last_at }}</span>
                    </template>
                  </el-table-column>
                </el-table>
              </div>

              <!-- 采样原文 -->
              <div class="logs-block" v-if="samplesList.length">
                <div class="logs-block-title">采样原文（{{ samplesList.length }} 条）</div>
                <pre v-for="(s, i) in samplesList" :key="i" class="logs-sample">{{ s }}</pre>
              </div>
            </div>
            <el-empty v-else description="本次分析未留档日志证据" />
          </el-tab-pane>
          <el-tab-pane label="完整 Prompt" name="prompt">
            <pre class="logs-sample">{{ detail.prompt }}</pre>
          </el-tab-pane>
        </el-tabs>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import dayjs from 'dayjs'
import { ElMessage } from 'element-plus'
import { marked } from 'marked'
import {
  getAiAnalysisRecords, getAiAnalysisSummary, getAiAnalysisRecord,
  type AiAnalysisRecordItem, type AiAnalysisRecordDetail, type AiAnalysisSummary,
} from '../api'

marked.setOptions({ breaks: true, gfm: true })

function getCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

function formatTokens(n: number): string {
  if (n >= 1000000) return (n / 1000000).toFixed(2) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return n.toLocaleString('zh-CN')
}

function formatCost(n: number): string {
  return n.toFixed(n < 0.01 ? 4 : 2)
}

function typeLabel(t: string): string {
  switch (t) {
    case 'lts_alert': return 'LTS 告警巡检'
    case 'inspection': return '巡检分析'
    case 'alert': return '告警根因'
    case 'alert_external': return '外部告警'
    default: return t
  }
}

function typeTagType(t: string): string {
  switch (t) {
    case 'lts_alert': return 'primary'
    case 'inspection': return 'info'
    case 'alert': return 'warning'
    case 'alert_external': return 'danger'
    default: return 'info'
  }
}

function renderMarkdown(text: string): string {
  if (!text) return ''
  return marked.parse(text) as string
}

const loading = ref(false)
const records = ref<AiAnalysisRecordItem[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const keyword = ref('')
const typeFilter = ref('')

const summaryLoading = ref(false)
const summary = ref<AiAnalysisSummary | null>(null)

async function fetchSummary() {
  summaryLoading.value = true
  try {
    const res = await getAiAnalysisSummary()
    summary.value = res.data
  } catch (e: any) {
    ElMessage.error('加载汇总失败：' + e.message)
  } finally {
    summaryLoading.value = false
  }
}

async function fetchRecords() {
  loading.value = true
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (keyword.value) params.keyword = keyword.value
    if (typeFilter.value) params.type = typeFilter.value
    const res = await getAiAnalysisRecords(params)
    records.value = res.data.items
    total.value = res.data.total
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

function onSearch() {
  page.value = 1
  fetchRecords()
}

// 详情
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<AiAnalysisRecordDetail | null>(null)
const activeTab = ref('result')

const hasLogs = computed(() => !!detail.value?.logs)
const foldedList = computed(() => detail.value?.logs?.folded || [])
const samplesList = computed(() => detail.value?.logs?.samples || [])

async function openDetail(row: AiAnalysisRecordItem) {
  detailVisible.value = true
  activeTab.value = 'result'
  detailLoading.value = true
  detail.value = null
  try {
    const res = await getAiAnalysisRecord(row.id)
    detail.value = res.data
    // 有日志留档时默认切到日志面板
    if (detail.value?.logs) activeTab.value = 'logs'
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    detailLoading.value = false
  }
}

// 预算展示
const hasBudget = computed(() => (summary.value?.daily_budget ?? 0) > 0)
const budgetLabel = computed(() => {
  const b = summary.value?.daily_budget ?? 0
  return b <= 0 ? '不限' : formatTokens(b)
})
const budgetUsagePct = computed(() => {
  const b = summary.value?.daily_budget ?? 0
  if (b <= 0) return 0
  const used = summary.value?.today.total_tokens ?? 0
  return Math.min(100, Math.round((used / b) * 1000) / 10)
})
const budgetUsageLabel = computed(() => {
  const b = summary.value?.daily_budget ?? 0
  if (b <= 0) return '未设置预算上限'
  return `已用 ${budgetUsagePct.value}%`
})
const budgetProgressColor = computed(() => {
  const pct = budgetUsagePct.value
  return pct >= 100 ? 'var(--el-color-danger)' : pct >= 80 ? 'var(--el-color-warning)' : 'var(--cyan)'
})
const budgetUsageStyle = computed(() => {
  const b = summary.value?.daily_budget ?? 0
  if (b <= 0) return {}
  const used = summary.value?.today.total_tokens ?? 0
  const pct = b > 0 ? used / b : 0
  return { color: pct >= 1 ? 'var(--el-color-danger)' : pct >= 0.8 ? 'var(--el-color-warning)' : 'var(--text-tertiary)' }
})

onMounted(() => {
  fetchSummary()
  fetchRecords()
})
</script>

<style scoped>
.header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
.mono {
  font-family: monospace;
  font-size: 12px;
}
.muted {
  color: var(--text-tertiary, #909399);
}
.estimated-mark {
  color: var(--text-tertiary, #909399);
  font-size: 12px;
}
.link-btn {
  padding: 0 6px;
}
.link-edit {
  color: var(--cyan);
}
.time-range {
  font-size: 12px;
  color: var(--text-tertiary, #909399);
}
.pagination-wrap {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
  padding: 0 24px 16px;
}
.summary-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
  margin-bottom: 16px;
}
.stat-card {
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 10px;
  padding: 16px 20px;
}
.stat-label {
  font-size: 13px;
  color: var(--text-tertiary, #909399);
  margin-bottom: 6px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.refresh-btn {
  padding: 0;
  margin-left: 4px;
}
.stat-value {
  font-size: 26px;
  font-weight: 600;
  line-height: 1.2;
}
.stat-sub {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-tertiary, #909399);
}
.budget-progress {
  margin-top: 10px;
}
.detail-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
  font-size: 13px;
  color: var(--text-secondary, #606266);
  margin-bottom: 12px;
}
.markdown-body {
  max-height: 52vh;
  overflow-y: auto;
  line-height: 1.7;
}
.logs-tab-badge {
  margin-left: 4px;
}
.logs-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.logs-block-title {
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
  color: var(--text-primary, #303133);
}
.logs-kv {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  font-size: 12px;
}
.logs-k {
  color: var(--text-tertiary, #909399);
}
.logs-v {
  font-family: monospace;
  color: var(--text-primary, #303133);
}
.logs-sample {
  background: var(--bg-muted, #f5f7fa);
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 6px;
  padding: 10px 12px;
  font-family: monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 300px;
  overflow-y: auto;
  margin: 0 0 10px;
}
</style>
