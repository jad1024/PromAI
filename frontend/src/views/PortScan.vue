<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Aim /></el-icon> 敏感端口检测</h2>
      <p>针对公网暴露资产（如华为云公网 IP）检测敏感端口开放情况，手动触发、独立报告、不推送告警</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><Connection /></el-icon> 发起扫描</h3>
      </div>
      <div class="scan-form">
        <div class="form-col">
          <div class="form-label">目标公网 IP（一行一个，支持批量粘贴、逗号/空格分隔、CIDR）</div>
          <el-input
            v-model="targetsText"
            type="textarea"
            :rows="8"
            placeholder="例如：&#10;1.2.3.4&#10;5.6.7.8&#10;10.0.0.0/28"
            class="targets-input"
          />
          <div class="form-hint">已识别 <b>{{ parsedTargetCount }}</b> 个目标</div>
        </div>
        <div class="form-col">
          <div class="form-label">敏感端口（内置默认，可增删 / 手动添加）</div>
          <el-select
            v-model="selectedPorts"
            multiple
            filterable
            allow-create
            default-first-option
            placeholder="选择或输入端口"
            style="width: 100%"
          >
            <el-option
              v-for="p in defaultPorts"
              :key="p.port"
              :label="`${p.port} (${p.name})`"
              :value="p.port"
            />
          </el-select>
          <div class="form-hint">已选 <b>{{ selectedPorts.length }}</b> 个端口</div>
          <div class="risk-legend">
            <span class="legend-item"><span class="dot high"></span>高危（数据库/缓存/容器）</span>
            <span class="legend-item"><span class="dot medium"></span>中危（远程管理）</span>
            <span class="legend-item"><span class="dot low"></span>低危（管理后台）</span>
          </div>
          <el-button
            type="primary"
            :loading="scanning"
            :disabled="!targetsText.trim() || selectedPorts.length === 0"
            style="margin-top: 16px; width: 100%"
            @click="startScan"
          >
            <el-icon v-if="!scanning"><Search /></el-icon> {{ scanning ? '扫描中...' : '开始扫描' }}
          </el-button>
        </div>
      </div>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 扫描记录</h3>
        <el-button size="small" @click="fetchTasks"><el-icon><Refresh /></el-icon> 刷新</el-button>
      </div>
      <el-table :data="tasks" v-loading="loading" stripe>
        <el-table-column prop="task_id" label="任务 ID" width="180" />
        <el-table-column label="目标数" width="90" align="center">
          <template #default="{ row }">{{ row.total_targets }}</template>
        </el-table-column>
        <el-table-column label="端口数" width="90" align="center">
          <template #default="{ row }">{{ row.total_ports }}</template>
        </el-table-column>
        <el-table-column label="开放敏感端口" width="130" align="center">
          <template #default="{ row }">
            <span :style="{ color: row.open_ports > 0 ? 'var(--red)' : 'var(--emerald)', fontWeight: 600 }">{{ row.open_ports }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.status === 'completed'" type="success" effect="dark">完成</el-tag>
            <el-tag v-else-if="row.status === 'running'" type="warning" effect="dark">运行中</el-tag>
            <el-tag v-else type="danger" effect="dark">失败</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="消息" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">{{ row.message || '-' }}</template>
        </el-table-column>
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">{{ dayjs(row.started_at).format('MM-DD HH:mm:ss') }}</template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text style="color: var(--cyan);" :disabled="row.status !== 'completed'" @click="viewResults(row)">查看结果</el-button>
            <el-button size="small" text style="color: var(--cyan);" :disabled="row.status !== 'completed'" @click="downloadReport(row)">导出</el-button>
            <el-button size="small" text type="danger" @click="removeTask(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="total > pageSize" style="display: flex; justify-content: flex-end; margin-top: 16px; padding: 0 24px 16px;">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50]"
          :total="total"
          layout="total, sizes, prev, pager, next"
          background
          @change="fetchTasks"
        />
      </div>
    </div>

    <el-drawer v-model="drawerVisible" size="60%" :title="`扫描结果 - ${currentTask?.task_id || ''}`">
      <div v-if="currentTask" class="drawer-head">
        <span>目标 {{ currentTask.total_targets }} 个 · 端口 {{ currentTask.total_ports }} 个 · 开放敏感端口 <b :style="{ color: currentTask.open_ports > 0 ? 'var(--red)' : 'var(--emerald)' }">{{ currentTask.open_ports }}</b> 个</span>
        <el-button size="small" type="primary" @click="downloadReport(currentTask)">
          <el-icon><Download /></el-icon> 导出报告
        </el-button>
      </div>
      <el-table :data="results" v-loading="resultsLoading" stripe size="small">
        <el-table-column prop="ip" label="IP" width="150" />
        <el-table-column prop="port" label="端口" width="80" align="center" />
        <el-table-column prop="port_name" label="服务" min-width="120" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.state === 'open'" type="success" effect="dark">开放</el-tag>
            <el-tag v-else-if="row.state === 'timeout'" type="info">超时</el-tag>
            <el-tag v-else-if="row.state === 'refused'" type="info">拒绝</el-tag>
            <el-tag v-else type="info">关闭</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="风险" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.state === 'open'" :type="row.risk === 'high' ? 'danger' : row.risk === 'medium' ? 'warning' : 'info'" effect="dark">
              {{ row.risk === 'high' ? '高危' : row.risk === 'medium' ? '中危' : '低危' }}
            </el-tag>
            <span v-else style="color: var(--text-tertiary);">-</span>
          </template>
        </el-table-column>
        <el-table-column label="延迟" width="90" align="center">
          <template #default="{ row }">{{ row.state === 'open' ? row.latency_ms + 'ms' : '-' }}</template>
        </el-table-column>
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import dayjs from 'dayjs'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  createPortScan, getPortScanTasks, getPortScanResults, deletePortScanTask,
  getPortScanPorts, downloadPortScanReport
} from '../api'
import type { PortScanTask, PortScanResult, PortInfo } from '../types'

function getCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

const targetsText = ref('')
const selectedPorts = ref<number[]>([])
const defaultPorts = ref<PortInfo[]>([])
const scanning = ref(false)

const tasks = ref<PortScanTask[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const drawerVisible = ref(false)
const currentTask = ref<PortScanTask | null>(null)
const results = ref<PortScanResult[]>([])
const resultsLoading = ref(false)

let pollTimer: ReturnType<typeof setInterval> | null = null

// 目标数量预览：按行粗略统计（不含 CIDR 展开的精确数，仅作提示）
const parsedTargetCount = computed(() => {
  return targetsText.value
    .split(/[\n,;\s]+/)
    .map(s => s.trim())
    .filter(s => s !== '' && !s.startsWith('#')).length
})

async function fetchDefaultPorts() {
  try {
    const { data } = await getPortScanPorts()
    defaultPorts.value = data.items
    // 默认全选内置端口
    if (selectedPorts.value.length === 0) {
      selectedPorts.value = data.items.map(p => p.port)
    }
  } catch (e) {
    // 忽略，用户可手动输入
  }
}

async function fetchTasks() {
  loading.value = true
  try {
    const { data } = await getPortScanTasks({ page: page.value, page_size: pageSize.value })
    tasks.value = data.items
    total.value = data.total
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || '加载扫描记录失败')
  } finally {
    loading.value = false
  }
}

async function startScan() {
  if (!targetsText.value.trim()) { ElMessage.warning('请输入目标 IP'); return }
  if (selectedPorts.value.length === 0) { ElMessage.warning('请选择端口'); return }
  scanning.value = true
  try {
    const { data } = await createPortScan(targetsText.value, selectedPorts.value)
    ElMessage.success(`扫描任务已创建：${data.total_targets} 个目标 × ${data.total_ports} 个端口`)
    await fetchTasks()
    startPolling()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || '创建扫描任务失败')
  } finally {
    scanning.value = false
  }
}

function startPolling() {
  stopPolling()
  let rounds = 0
  pollTimer = setInterval(async () => {
    rounds++
    await fetchTasks()
    const hasRunning = tasks.value.some(t => t.status === 'running')
    if (!hasRunning || rounds >= 60) {
      stopPolling()
    }
  }, 3000)
}

function stopPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

async function viewResults(task: PortScanTask) {
  currentTask.value = task
  drawerVisible.value = true
  resultsLoading.value = true
  try {
    const { data } = await getPortScanResults(task.id)
    results.value = data.items
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || '加载结果失败')
  } finally {
    resultsLoading.value = false
  }
}

async function downloadReport(task: PortScanTask) {
  try {
    const res = await downloadPortScanReport(task.id)
    const blobUrl = URL.createObjectURL(res.data as Blob)
    const a = document.createElement('a')
    a.href = blobUrl
    a.download = `portscan-${task.task_id}.html`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(blobUrl)
  } catch (e: any) {
    ElMessage.error('导出报告失败')
  }
}

async function removeTask(task: PortScanTask) {
  try {
    await ElMessageBox.confirm(`确认删除任务 ${task.task_id} 及其结果？`, '删除确认', { type: 'warning' })
  } catch { return }
  try {
    await deletePortScanTask(task.id)
    ElMessage.success('已删除')
    await fetchTasks()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || '删除失败')
  }
}

onMounted(() => {
  fetchDefaultPorts()
  fetchTasks()
})

onBeforeUnmount(() => stopPolling())
</script>

<style scoped>
.scan-form {
  display: grid;
  grid-template-columns: 1.4fr 1fr;
  gap: 24px;
  padding: 20px 24px;
}
@media (max-width: 900px) {
  .scan-form { grid-template-columns: 1fr; }
}
.form-label { font-size: 13px; color: var(--text-secondary, #5e6d82); margin-bottom: 8px; }
.form-hint { font-size: 12px; color: var(--text-tertiary, #909399); margin-top: 8px; }
.targets-input :deep(textarea) { font-family: ui-monospace, Consolas, monospace; font-size: 13px; line-height: 1.6; }
.risk-legend { margin-top: 14px; display: flex; flex-wrap: wrap; gap: 12px; }
.legend-item { font-size: 12px; color: var(--text-tertiary, #909399); display: inline-flex; align-items: center; gap: 4px; }
.dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
.dot.high { background: #f56c6c; }
.dot.medium { background: #e6a23c; }
.dot.low { background: #909399; }
.drawer-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; font-size: 13px; color: var(--text-secondary, #5e6d82); }
</style>
