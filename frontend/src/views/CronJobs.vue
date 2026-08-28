<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Clock /></el-icon> 定时任务</h2>
      <p>配置定时巡检任务，支持选择数据源</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 任务列表</h3>
        <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新增任务</el-button>
      </div>
      <el-table :data="jobs" v-loading="loading" stripe>
        <el-table-column type="index" label="#" width="56" />
        <el-table-column prop="name" label="任务名称" min-width="180">
          <template #default="{ row }">
            <span style="font-weight: 600; color: var(--text-primary);">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="schedule" label="调度表达式" width="150">
          <template #default="{ row }">
            <el-tag size="small" style="background: rgba(0,212,255,0.1); color: var(--cyan); border: none; font-family: monospace;">{{ row.schedule }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="数据源" width="260">
          <template #default="{ row }">
            <span style="color: var(--text-secondary); font-size: 13px;">{{ dsNames(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="指标范围" width="180">
          <template #default="{ row }">
            <span v-if="metricScopeNames(row)" style="color: var(--text-secondary); font-size: 12px;">{{ metricScopeNames(row) }}</span>
            <span v-else style="color: var(--text-tertiary);">全部</span>
          </template>
        </el-table-column>
        <el-table-column label="通知渠道" min-width="160">
          <template #default="{ row }">
            <span v-if="notifNames(row.notify_channels)" style="color: var(--text-tertiary); font-size: 12px;">{{ notifNames(row.notify_channels) }}</span>
            <span v-else style="color: var(--text-tertiary);">-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90" align="center">
          <template #default="{ row }">
            <el-switch v-model="row.enabled" @change="toggleEnabled(row)" />
          </template>
        </el-table-column>
        <el-table-column label="上次执行" width="200">
          <template #default="{ row }">
            <span style="color: var(--text-tertiary); font-size: 13px;" v-if="row.last_run_at">{{ dayjs(row.last_run_at).format('MM-DD HH:mm') }}</span>
            <span v-else style="color: var(--text-tertiary);">-</span>
            <el-tag v-if="row.last_status" :style="{ background: row.last_status === 'success' ? 'rgba(16,185,129,0.1)' : 'rgba(239,68,68,0.1)', color: row.last_status === 'success' ? '#10b981' : '#ef4444', border: 'none', marginLeft: '6px' }" size="small">
              {{ row.last_status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text @click="openEdit(row)" style="color: var(--cyan);">编辑</el-button>
            <el-button size="small" text :loading="aiAnalyzingId === row.id" :disabled="!!aiAnalyzingId" @click="handleAiAnalyze(row)" style="color: #a855f7;">
              AI 分析
            </el-button>
            <el-button size="small" text @click="handleDelete(row)" style="color: var(--red);">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑定时任务' : '新增定时任务'" width="540" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <el-form-item label="任务名称" prop="name">
          <el-input v-model="form.name" placeholder="例如：每日巡检" />
        </el-form-item>
        <el-form-item label="调度表达式" prop="schedule">
          <el-input v-model="form.schedule" placeholder="Cron 表达式，如 00 08,17 * * *">
            <template #append>
              <el-tooltip content="分 时 日 月 周" placement="top">
                <el-icon><QuestionFilled /></el-icon>
              </el-tooltip>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item label="数据源">
          <div style="width: 100%;">
            <el-checkbox v-model="form.all_datasources" style="margin-bottom: 6px;" @change="onAllDSChange">全部数据源</el-checkbox>
            <el-select v-model="selectedDSIds" placeholder="选择数据源" multiple clearable filterable style="width: 100%;" :disabled="form.all_datasources">
              <el-option v-for="ds in datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item label="巡检指标">
          <div style="width: 100%;">
            <el-select v-model="selectedMetricTypeIds" placeholder="留空表示巡检全部有效指标（按分组指定）" multiple clearable filterable collapse-tags style="width: 100%;">
              <el-option v-for="mt in metricTypes" :key="mt.id" :label="mt.type_name" :value="mt.id!" />
            </el-select>
            <div style="color: var(--text-tertiary); font-size: 12px; line-height: 1.4; margin-top: 4px;">按指标分组（如 L1-基础设施层）限定巡检范围；留空则巡检该数据源的全部指标</div>
          </div>
        </el-form-item>
        <el-form-item label="并发批次">
          <el-input-number v-model="form.batch_size" :min="0" :max="500" :step="5" style="width: 100%;" controls-position="right" />
          <div style="color: var(--text-tertiary); font-size: 12px; line-height: 1.4; margin-top: 4px;">大集群分批巡检：每批并发巡检的数据源数量，0 表示不限（一次全量并发）</div>
        </el-form-item>
        <el-form-item label="通知渠道">
          <el-select v-model="notifChannelIds" placeholder="选择通知渠道" multiple clearable filterable style="width: 100%">
            <el-option v-for="nc in notifications" :key="nc.id" :label="nc.name" :value="nc.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="AI 巡检分析">
          <el-switch v-model="form.ai_analysis_enabled" active-text="开启（巡检后 AI 分析并推送飞书）" inactive-text="关闭" />
          <div style="color: var(--text-tertiary); font-size: 12px; line-height: 1.4;">定时触发时需开启此开关才会推送飞书 AI 分析；列表页手动「AI 分析」不受此开关限制</div>
        </el-form-item>
        <el-form-item v-if="form.ai_analysis_enabled" label="自定义提示词">
          <el-input v-model="form.ai_analysis_prompt" type="textarea" :rows="3" placeholder="可选。留空使用内置模板（健康总览 / 异常分析 / 处理建议 / 风险提示）" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="dialogVisible = false" style="color: var(--text-secondary);">取消</el-button>
          <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import dayjs from 'dayjs'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'
import { getCronJobs, createCronJob, updateCronJob, deleteCronJob, triggerCronJobAIAnalyze, getAllDataSources, getAllNotifications, getMetricTypes } from '../api'
import type { CronJob, DataSource, NotificationChannel, MetricType } from '../types'

function getCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

const loading = ref(false)
const saving = ref(false)
const jobs = ref<CronJob[]>([])
const datasources = ref<DataSource[]>([])
const notifications = ref<NotificationChannel[]>([])
const metricTypes = ref<MetricType[]>([])
const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const formRef = ref<FormInstance>()
const notifChannelIds = ref<number[]>([])
const selectedDSIds = ref<number[]>([])
const selectedMetricTypeIds = ref<number[]>([])
const aiAnalyzingId = ref<number | null>(null)
const form = ref<CronJob>({ name: '', schedule: '', datasource_id: null, all_datasources: false, enabled: true, ai_analysis_enabled: false, batch_size: 0 })
const rules = {
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
  schedule: [{ required: true, message: '请输入调度表达式', trigger: 'blur' }],
}

function dsNames(row: CronJob) {
  if (row.all_datasources) return '全部数据源'
  if (row.datasource_ids) {
    let ids: number[] = []
    try { ids = JSON.parse(row.datasource_ids) } catch { ids = [] }
    return ids.map(id => datasources.value.find(d => d.id === id)?.name).filter(Boolean).join(', ') || '默认数据源'
  }
  if (row.datasource_id) {
    const d = datasources.value.find(x => x.id === row.datasource_id)
    return d ? d.name : `ID: ${row.datasource_id}`
  }
  return '默认数据源'
}

function notifNames(channels: string | undefined | null) {
  if (!channels) return ''
  let ids: number[] = []
  try { ids = JSON.parse(channels) } catch { ids = [] }
  return ids.map(id => notifications.value.find(n => n.id === id)?.name).filter(Boolean).join(', ')
}

function metricScopeNames(row: CronJob) {
  if (row.metric_type_ids) {
    let ids: number[] = []
    try { ids = JSON.parse(row.metric_type_ids) } catch { ids = [] }
    return ids.map(id => metricTypes.value.find(t => t.id === id)?.type_name).filter(Boolean).join('、')
  }
  return ''
}

function onAllDSChange(val: boolean) {
  if (val) selectedDSIds.value = []
}

// Sync form.datasource_ids with selectedDSIds
watch(selectedDSIds, (ids) => {
  form.value.datasource_ids = ids.length > 0 ? JSON.stringify(ids) : ''
  form.value.datasource_id = null
})

// Sync form.notify_channels with notifChannelIds
watch(notifChannelIds, (ids) => {
  form.value.notify_channels = ids.length > 0 ? JSON.stringify(ids) : ''
})

// Sync form.metric_type_ids with selectedMetricTypeIds
watch(selectedMetricTypeIds, (ids) => {
  form.value.metric_type_ids = ids.length > 0 ? JSON.stringify(ids) : ''
})

async function fetchData() {
  loading.value = true
  try {
    const [jr, dr, nr, mr] = await Promise.all([getCronJobs(), getAllDataSources(), getAllNotifications(), getMetricTypes()])
    jobs.value = jr.data; datasources.value = dr.data; notifications.value = nr.data; metricTypes.value = mr.data
  } finally { loading.value = false }
}

function openCreate() {
  editingId.value = null
  form.value = { name: '', schedule: '', datasource_id: null, all_datasources: false, enabled: true, ai_analysis_enabled: false, ai_analysis_prompt: '', batch_size: 0 }
  notifChannelIds.value = []
  selectedDSIds.value = []
  selectedMetricTypeIds.value = []
  dialogVisible.value = true
}
function openEdit(row: CronJob) {
  editingId.value = row.id!
  form.value = { ...row, all_datasources: row.all_datasources ?? false, batch_size: row.batch_size ?? 0 }
  try { notifChannelIds.value = row.notify_channels ? JSON.parse(row.notify_channels) : [] } catch { notifChannelIds.value = [] }
  try { selectedDSIds.value = row.datasource_ids ? JSON.parse(row.datasource_ids) : [] } catch { selectedDSIds.value = [] }
  try { selectedMetricTypeIds.value = row.metric_type_ids ? JSON.parse(row.metric_type_ids) : [] } catch { selectedMetricTypeIds.value = [] }
  dialogVisible.value = true
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    if (editingId.value) { await updateCronJob(editingId.value, form.value); ElMessage.success('更新成功') }
    else { await createCronJob(form.value); ElMessage.success('创建成功') }
    dialogVisible.value = false; await fetchData()
  } catch (e: any) { ElMessage.error(e.message) } finally { saving.value = false }
}

async function handleDelete(row: CronJob) {
  try {
    await ElMessageBox.confirm(`确定删除定时任务「${row.name}」？`, '确认删除', { type: 'warning', cancelButtonText: '取消', confirmButtonText: '删除' })
    await deleteCronJob(row.id!); ElMessage.success('删除成功'); await fetchData()
  } catch { /* ignore */ }
}

// 手动触发一次 AI 巡检分析（立即巡检 -> AI 分析 -> 推送飞书）
async function handleAiAnalyze(row: CronJob) {
  try {
    await ElMessageBox.confirm(
      `将立即对任务「${row.name}」执行一次巡检，并由 AI 生成健康分析推送到飞书。\n耗时约 1-3 分钟，是否继续？`,
      '触发 AI 巡检分析', { type: 'info', cancelButtonText: '取消', confirmButtonText: '触发', confirmButtonClass: 'el-button--primary' }
    )
  } catch { return }
  aiAnalyzingId.value = row.id!
  try {
    const res: any = await triggerCronJobAIAnalyze(row.id!)
    if (res.data?.success) {
      ElMessage.success('AI 巡检分析已触发，结果将推送到飞书')
    } else {
      ElMessage.warning(res.data?.message || '任务已执行，请查看巡检记录')
    }
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message || '触发失败') } finally { aiAnalyzingId.value = null }
}

async function toggleEnabled(row: CronJob) {
  try { await updateCronJob(row.id!, row); ElMessage.success(row.enabled ? '已启用' : '已禁用') }
  catch { row.enabled = !row.enabled }
}

onMounted(fetchData)
</script>
