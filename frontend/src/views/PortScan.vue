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

    <el-drawer v-model="drawerVisible" size="70%" :title="`扫描结果 - ${currentTask?.task_id || ''}`">
      <div v-if="currentTask" class="drawer-head">
        <span>目标 {{ currentTask.total_targets }} 个 · 端口 {{ currentTask.total_ports }} 个 · 开放敏感端口 <b :style="{ color: currentTask.open_ports > 0 ? 'var(--red)' : 'var(--emerald)' }">{{ currentTask.open_ports }}</b> 个</span>
        <el-button size="small" type="primary" @click="downloadReport(currentTask)">
          <el-icon><Download /></el-icon> 导出报告
        </el-button>
      </div>

      <!-- 筛选栏 -->
      <div class="filter-bar">
        <el-input
          v-model="filterIP"
          placeholder="按 IP 搜索"
          clearable
          :prefix-icon="Search"
          style="width: 200px"
        />
        <el-select v-model="filterState" placeholder="状态" style="width: 140px">
          <el-option label="仅开放" value="open" />
          <el-option label="全部状态" value="all" />
        </el-select>
        <el-select v-model="filterRisk" placeholder="风险等级" clearable style="width: 140px">
          <el-option label="高危" value="high" />
          <el-option label="中危" value="medium" />
          <el-option label="低危" value="low" />
        </el-select>
        <span class="filter-count">已显示 {{ filteredResultCount }} / {{ results.length }} 条</span>
      </div>

      <!-- 按 IP 分组展示 -->
      <div v-loading="resultsLoading">
        <el-empty v-if="groupedResults.length === 0" description="无匹配结果" :image-size="60" />
        <el-collapse v-model="expandedIPs">
          <el-collapse-item v-for="g in groupedResults" :key="g.ip" :name="g.ip">
            <template #title>
              <div class="ip-group-title">
                <span class="ip-addr">{{ g.ip }}</span>
                <el-tag v-if="g.openHigh > 0" type="danger" size="small" effect="dark">高危 {{ g.openHigh }}</el-tag>
                <el-tag v-if="g.openMed > 0" type="warning" size="small" effect="dark">中危 {{ g.openMed }}</el-tag>
                <span class="ip-sub">开放 {{ g.openCount }} 个敏感端口</span>
              </div>
            </template>
            <div class="port-tags">
              <div
                v-for="r in g.rows"
                :key="r.port"
                class="port-tag"
                :class="[r.state === 'open' ? 'open' : 'closed', r.risk]"
              >
                <span class="pt-port">{{ r.port }}</span>
                <span class="pt-name">{{ r.port_name }}</span>
                <span v-if="r.state === 'open'" class="pt-latency">{{ r.latency_ms }}ms</span>
                <span v-else class="pt-state">{{ stateText(r.state) }}</span>
              </div>
            </div>
          </el-collapse-item>
        </el-collapse>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import dayjs from 'dayjs'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
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

// 结果筛选
const filterIP = ref('')
const filterState = ref<'open' | 'all'>('open') // 默认只看开放端口
const filterRisk = ref<'' | 'high' | 'medium' | 'low'>('')
const expandedIPs = ref<string[]>([])

let pollTimer: ReturnType<typeof setInterval> | null = null

// 目标数量预览：按行粗略统计（不含 CIDR 展开的精确数，仅作提示）
const parsedTargetCount = computed(() => {
  return targetsText.value
    .split(/[\n,;\s]+/)
    .map(s => s.trim())
    .filter(s => s !== '' && !s.startsWith('#')).length
})

function stateText(s: string): string {
  switch (s) {
    case 'open': return '开放'
    case 'timeout': return '超时'
    case 'refused': return '拒绝'
    default: return '关闭'
  }
}

// 按筛选条件过滤后的结果
const filteredResults = computed(() => {
  return results.value.filter(r => {
    if (filterState.value === 'open' && r.state !== 'open') return false
    if (filterRisk.value && r.risk !== filterRisk.value) return false
    if (filterIP.value && !r.ip.includes(filterIP.value.trim())) return false
    return true
  })
})

const filteredResultCount = computed(() => filteredResults.value.length)

// 按 IP 分组的展示结构
interface IPGroup {
  ip: string
  rows: PortScanResult[]
  openCount: number
  openHigh: number
  openMed: number
}
const groupedResults = computed<IPGroup[]>(() => {
  const map = new Map<string, PortScanResult[]>()
  for (const r of filteredResults.value) {
    const arr = map.get(r.ip) || []
    arr.push(r)
    map.set(r.ip, arr)
  }
  const groups: IPGroup[] = []
  for (const [ip, rows] of map) {
    // 开放端口排前，同状态按端口号升序
    rows.sort((a, b) => {
      if (a.state === 'open' && b.state !== 'open') return -1
      if (a.state !== 'open' && b.state === 'open') return 1
      return a.port - b.port
    })
    const openRows = rows.filter(r => r.state === 'open')
    groups.push({
      ip,
      rows,
      openCount: openRows.length,
      openHigh: openRows.filter(r => r.risk === 'high').length,
      openMed: openRows.filter(r => r.risk === 'medium').length,
    })
  }
  groups.sort((a, b) => b.openHigh - a.openHigh || b.openCount - a.openCount || a.ip.localeCompare(b.ip))
  return groups
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
  // 重置筛选，默认只看开放端口
  filterIP.value = ''
  filterState.value = 'open'
  filterRisk.value = ''
  try {
    const { data } = await getPortScanResults(task.id)
    results.value = data.items
    // 默认展开所有 IP 分组
    expandedIPs.value = [...new Set(data.items.map((r: PortScanResult) => r.ip))]
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
.filter-bar { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; flex-wrap: wrap; }
.filter-count { font-size: 12px; color: var(--text-tertiary, #909399); margin-left: auto; }
.ip-group-title { display: flex; align-items: center; gap: 8px; flex: 1; }
.ip-addr { font-family: ui-monospace, Consolas, monospace; font-weight: 600; font-size: 14px; }
.ip-sub { font-size: 12px; color: var(--text-tertiary, #909399); margin-left: 4px; }
.port-tags { display: flex; flex-wrap: wrap; gap: 8px; padding: 8px 4px 16px; }
.port-tag { display: inline-flex; align-items: center; gap: 8px; padding: 6px 10px; border-radius: 6px; font-size: 13px; border: 1px solid transparent; }
.port-tag .pt-port { font-family: ui-monospace, Consolas, monospace; font-weight: 700; }
.port-tag .pt-name { color: var(--text-secondary, #5e6d82); }
.port-tag .pt-latency { font-size: 12px; opacity: 0.8; }
.port-tag .pt-state { font-size: 12px; color: var(--text-tertiary, #909399); }
/* 开放端口按风险着色 */
.port-tag.open.high { background: #fef0f0; border-color: #fbc4c4; }
.port-tag.open.high .pt-port { color: #f56c6c; }
.port-tag.open.medium { background: #fdf6ec; border-color: #f5dab1; }
.port-tag.open.medium .pt-port { color: #e6a23c; }
.port-tag.open.low { background: #f4f4f5; border-color: #e1e4e8; }
.port-tag.open.low .pt-port { color: #909399; }
/* 非开放端口弱化展示 */
.port-tag.closed { background: transparent; border-color: transparent; opacity: 0.5; }

</style>
