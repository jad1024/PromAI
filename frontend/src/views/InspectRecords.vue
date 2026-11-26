<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Monitor /></el-icon> 巡检记录</h2>
      <p>查看所有巡检任务的执行历史</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 任务列表</h3>
        <div style="display: flex; gap: 8px; align-items: center; flex-wrap: wrap;">
          <el-input v-model="keyword" placeholder="搜索目标/消息" clearable style="width: 190px;" @keyup.enter="fetchRecords" @clear="fetchRecords" />
          <el-select v-model="statusFilter" placeholder="状态" clearable style="width: 100px;" @change="fetchRecords">
            <el-option label="运行中" value="running" />
            <el-option label="已完成" value="completed" />
            <el-option label="失败" value="failed" />
          </el-select>
          <el-select v-model="triggerFilter" placeholder="触发类型" clearable style="width: 120px;" @change="fetchRecords">
            <el-option label="手动" value="manual" />
            <el-option label="定时" value="cron" />
            <el-option label="手动+AI" value="manual_ai" />
            <el-option label="定时+AI" value="cron_ai" />
          </el-select>
          <el-button plain type="danger" @click="openCleanup"><el-icon><Delete /></el-icon> 清理/保留</el-button>
        </div>
      </div>
      <div v-if="selection.length > 0" class="batch-bar">
        <span>已选 <b>{{ selection.length }}</b> 条</span>
        <el-button size="small" type="danger" plain :loading="batchDeleting" @click="batchDeleteSelected">
          <el-icon><Delete /></el-icon> 批量删除
        </el-button>
        <el-button size="small" text @click="selection = []">取消选择</el-button>
      </div>
      <el-table :data="records" v-loading="loading" stripe row-class-name="clickable-row"
        @row-click="onRowClick" @selection-change="onSelectionChange">
        <el-table-column type="selection" width="46" @click.stop />
        <el-table-column prop="task_id" label="任务 ID" width="190" />
        <el-table-column label="触发类型" width="120">
          <template #default="{ row }">
            <el-tag :type="triggerTagType(row.trigger_type)" effect="plain" size="small">
              {{ triggerLabel(row.trigger_type) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="使用模版" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.template_names">{{ row.template_names }}</span>
            <span v-else style="color: var(--text-tertiary);">-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.status === 'completed'" type="success" effect="dark">完成</el-tag>
            <el-tag v-else-if="row.status === 'running'" type="warning" effect="dark">运行中</el-tag>
            <el-tag v-else type="danger" effect="dark">失败</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="datasource_name" label="巡检目标" min-width="180" />
        <el-table-column prop="message" label="消息" min-width="180" show-overflow-tooltip />
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">{{ dayjs(row.started_at).format('MM-DD HH:mm:ss') }}</template>
        </el-table-column>
        <el-table-column label="完成时间" width="170">
          <template #default="{ row }">
            <span v-if="row.completed_at">{{ dayjs(row.completed_at).format('MM-DD HH:mm:ss') }}</span>
            <span v-else style="color: var(--text-tertiary);">-</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.report_url" size="small" text style="color: var(--cyan);" @click.stop="viewReport(row)">查看报告</el-button>
            <el-button v-if="row.cron_job_id" size="small" text style="color: var(--cyan);" @click.stop="goCronJob(row)">查看任务</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="total > pageSize" style="display: flex; justify-content: flex-end; margin-top: 16px; padding: 0 24px 16px;">
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

    <el-dialog v-model="cleanupOpen" title="清理巡检记录" width="520px" :close-on-click-modal="false">
      <div class="cleanup-body">
        <p class="cleanup-tip">当前共 {{ total }} 条巡检记录。请选择清理方式，操作不可恢复。</p>
        <el-radio-group v-model="cleanupMode">
          <el-radio value="keep">
            只保留最近 <el-input-number v-model="keepRecent" :min="0" :max="100000" size="small" style="width:110px;margin:0 4px;" /> 条
            <span class="cleanup-sub">（删除更早的全部记录）</span>
          </el-radio>
          <el-radio value="days" style="margin-top:12px;">
            删除 <el-input-number v-model="olderDays" :min="1" :max="3650" size="small" style="width:100px;margin:0 4px;" /> 天前的记录
          </el-radio>
        </el-radio-group>
      </div>
      <template #footer>
        <el-button @click="cleanupOpen = false">取消</el-button>
        <el-button type="danger" :loading="cleanupBusy" @click="doCleanup">确认清理</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getInspectRecords, openReportFile, batchDeleteInspectRecords } from '../api'

function getCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

type TriggerType = 'manual' | 'cron' | 'manual_ai' | 'cron_ai' | ''

interface InspectRecord {
  id: number
  task_id: string
  status: string
  trigger_type?: TriggerType
  cron_job_id?: number | null
  template_ids?: string
  template_names?: string
  datasource_name: string
  message: string
  error: string
  report_url: string
  started_at: string
  completed_at: string | null
}

const route = useRoute()
const router = useRouter()

const loading = ref(false)
const records = ref<InspectRecord[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const keyword = ref('')
const statusFilter = ref('')
const triggerFilter = ref<TriggerType>('')

// 批量删除 / 保留清理
const selection = ref<InspectRecord[]>([])
const batchDeleting = ref(false)
const cleanupOpen = ref(false)
const cleanupBusy = ref(false)
const cleanupMode = ref<'keep' | 'days'>('keep')
const keepRecent = ref(50)
const olderDays = ref(30)

function onSelectionChange(rows: InspectRecord[]) { selection.value = rows }

async function batchDeleteSelected() {
  const ids = selection.value.map(r => r.id).filter(Boolean)
  if (!ids.length) return
  try {
    await ElMessageBox.confirm(`确定批量删除选中的 ${ids.length} 条巡检记录？此操作不可恢复。`, '批量删除', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch { return }
  batchDeleting.value = true
  try {
    const res = await batchDeleteInspectRecords({ ids, confirm: true })
    ElMessage.success(`已删除 ${res.data.deleted} 条记录`)
    selection.value = []
    await fetchRecords()
  } catch (e: any) { ElMessage.error(e?.response?.data?.error || e.message) }
  finally { batchDeleting.value = false }
}

function openCleanup() { cleanupOpen.value = true }

async function doCleanup() {
  const payload = cleanupMode.value === 'keep'
    ? { keep_recent: keepRecent.value, confirm: true }
    : { older_than_days: olderDays.value, confirm: true }
  const desc = cleanupMode.value === 'keep'
    ? `只保留最近 ${keepRecent.value} 条，其余全部删除`
    : `删除 ${olderDays.value} 天前的全部记录`
  try {
    await ElMessageBox.confirm(`${desc}。此操作不可恢复，确认继续？`, '清理确认', {
      type: 'warning', confirmButtonText: '确认清理', cancelButtonText: '取消',
    })
  } catch { return }
  cleanupBusy.value = true
  try {
    const res = await batchDeleteInspectRecords(payload)
    ElMessage.success(`已清理 ${res.data.deleted} 条记录`)
    cleanupOpen.value = false
    selection.value = []
    await fetchRecords()
  } catch (e: any) { ElMessage.error(e?.response?.data?.error || e.message) }
  finally { cleanupBusy.value = false }
}

function triggerLabel(t?: TriggerType): string {
  switch (t) {
    case 'manual': return '手动'
    case 'cron': return '定时'
    case 'manual_ai': return '手动+AI'
    case 'cron_ai': return '定时+AI'
    default: return '未知'
  }
}
function triggerTagType(t?: TriggerType): 'info' | 'primary' | 'warning' | 'success' {
  switch (t) {
    case 'manual': return 'info'
    case 'cron': return 'primary'
    case 'manual_ai': return 'warning'
    case 'cron_ai': return 'success'
    default: return 'info'
  }
}

async function fetchRecords() {
  loading.value = true
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (keyword.value) params.keyword = keyword.value
    if (statusFilter.value) params.status = statusFilter.value
    if (triggerFilter.value) params.trigger_type = triggerFilter.value
    const res = await getInspectRecords(params)
    records.value = res.data.items
    total.value = res.data.total
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

function viewReport(row: InspectRecord) {
  const filename = row.report_url.split('/').pop()
  if (!filename) { ElMessage.warning('报告文件路径无效'); return }
  openReportFile('reports/' + filename).catch((e: any) => ElMessage.error('打开报告失败：' + e.message))
}

function goCronJob(row: InspectRecord) {
  if (!row.cron_job_id) return
  router.push({ path: '/cronjobs', query: { highlight: String(row.cron_job_id) } })
}

function onRowClick(row: InspectRecord) {
  // 行点击：有报告则跳报告页；否则跳关联任务
  if (row.report_url) viewReport(row)
  else if (row.cron_job_id) goCronJob(row)
}

onMounted(() => {
  // 支持 ?task_id=xxx 高亮（来自 Reports.vue 跳转）
  if (route.query.task_id) {
    keyword.value = String(route.query.task_id)
  }
  fetchRecords()
})
</script>

<style scoped>
.clickable-row {
  cursor: pointer;
}
.batch-bar {
  display: flex; align-items: center; gap: 12px;
  margin: 0 24px 12px; padding: 8px 14px;
  background: var(--el-color-primary-light-9, #eef2ff);
  border: 1px solid var(--el-color-primary-light-5, #c7d2fe);
  border-radius: 6px; font-size: 13px;
}
.batch-bar b { color: var(--el-color-primary, #6366f1); }
.cleanup-body { padding: 4px 0; }
.cleanup-tip { margin: 0 0 14px; font-size: 13px; color: var(--text-secondary, #64748b); }
.cleanup-sub { font-size: 12px; color: var(--text-tertiary, #94a3b8); }
</style>