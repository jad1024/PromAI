<template>
  <div class="skills-page">
    <!-- Toolbar -->
    <div class="toolbar">
      <h3>Skills</h3>
      <div class="toolbar-actions">
        <el-input v-model="searchQuery" placeholder="搜索..." size="small" clearable style="width:200px;" />
        <el-button size="small" @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
        <el-button size="small" type="primary" @click="openCreate"><el-icon><Plus /></el-icon>新建</el-button>
      </div>
    </div>

    <!-- Table -->
    <el-table :data="pageData" v-loading="loading" stripe highlight-current-row
      @row-click="openDetail" style="width:100%;">
      <el-table-column prop="name" label="名称" width="180" />
      <el-table-column prop="description" label="描述" min-width="280" show-overflow-tooltip />
      <el-table-column label="状态" width="80" align="center">
        <template #default="{ row }">
          <el-tag size="small" :type="row.enabled ? 'success' : 'info'" effect="dark" round>
            {{ row.enabled ? '启用' : '禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="可调用" width="80" align="center">
        <template #default="{ row }">
          <el-icon v-if="row.user_invocable" color="var(--success)"><Check /></el-icon>
          <el-icon v-else color="var(--text-quaternary)"><Close /></el-icon>
        </template>
      </el-table-column>
      <el-table-column label="使用次数" width="120" align="center">
        <template #header>
          <span>使用次数</span>
          <el-tooltip placement="top">
            <template #content>
              基于 AI 自报（`&lt;used-skill&gt;`）+ 关键字匹配的近似值。<br />
              AI 未必总是标记，仅供参考不代表精确使用量。
            </template>
            <el-icon style="margin-left:4px;vertical-align:middle;color:var(--text-tertiary);"><QuestionFilled /></el-icon>
          </el-tooltip>
        </template>
        <template #default="{ row }">
          {{ statsMap[row.name] ?? 0 }}
        </template>
      </el-table-column>
      <el-table-column label="操作" width="120" align="center">
        <template #default="{ row }">
          <el-button size="small" link @click.stop="openDetail(row)">编辑</el-button>
          <el-button size="small" link type="danger" @click.stop="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div style="display:flex;justify-content:flex-end;padding:8px 0 0;">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        background
        @change="handlePageChange"
      />
    </div>

    <!-- Detail Dialog -->
    <el-dialog v-model="dialogVisible" :title="isEditingNew ? '新建 Skill' : editing.name" width="800px"
      top="3vh" destroy-on-close>
      <div class="dialog-body">
        <div class="dialog-form">
          <div class="form-row">
            <el-input v-model="editing.name" placeholder="名称（小写字母、数字、连字符）" size="small">
              <template #prepend>Name</template>
            </el-input>
          </div>
          <div class="form-row">
            <el-input v-model="editing.description" placeholder="一行描述（< 160 字符）" size="small">
              <template #prepend>描述</template>
            </el-input>
          </div>
          <div class="form-row" style="display:flex;gap:12px;">
            <el-checkbox v-model="editing.user_invocable">可作为 slash 命令</el-checkbox>
            <el-checkbox v-model="editing.enabled">启用</el-checkbox>
            <span style="font-size:11px;color:var(--text-tertiary);margin-left:auto;">
              Source: {{ editing.source || 'workspace' }} &nbsp; 约 {{ statsMap[editing.name] ?? 0 }} 次使用
            </span>
          </div>
          <div class="form-row">
            <el-input v-model="editing.metadata" type="textarea" :rows="2"
              placeholder='{"openclaw":{"requires":{"bins":["curl"]}}}' size="small">
              <template #prepend>Metadata</template>
            </el-input>
          </div>
          <div class="form-row" style="flex:1;display:flex;flex-direction:column;">
            <label style="font-size:12px;font-weight:600;color:var(--text-secondary);margin-bottom:4px;">
              Instruction (markdown)
            </label>
            <el-input v-model="editing.instruction" type="textarea" :rows="16"
              placeholder="# 描述此 skill 的完整工作流程&#10;&#10;When the user asks for X:&#10;1. First use `list_datasources`..."
              style="flex:1;" />
          </div>
        </div>

        <!-- Trend Chart -->
        <div class="dialog-chart" v-if="!isEditingNew && trendData.length > 0">
          <div ref="trendChartRef" style="width:100%;height:180px;"></div>
        </div>
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button @click="resetForm" v-if="!isEditingNew">重置</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Check, Close, QuestionFilled } from '@element-plus/icons-vue'
import * as echarts from 'echarts'
import api, { getAISkills, createAISkill, updateAISkill, deleteAISkill } from '../api'
import type { AiSkill } from '../types'

const loading = ref(false)
const saving = ref(false)
const items = ref<AiSkill[]>([])
const searchQuery = ref('')
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const dialogVisible = ref(false)
const isEditingNew = ref(false)
const editing = ref<AiSkill>(defaultSkill())
const statsMap = ref<Record<string, number>>({})
const trendData = ref<{ day: string; count: number }[]>([])
const trendChartRef = ref<HTMLElement | null>(null)
let trendChart: echarts.ECharts | null = null

function defaultSkill(): AiSkill {
  return { name: '', description: '', instruction: '', metadata: '', user_invocable: true, enabled: true, source: 'workspace' }
}

const filteredItems = computed(() => {
  if (!searchQuery.value) return items.value
  const q = searchQuery.value.toLowerCase()
  return items.value.filter(i =>
    i.name.toLowerCase().includes(q) || (i.description || '').toLowerCase().includes(q)
  )
})

const pageData = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return filteredItems.value.slice(start, start + pageSize.value)
})

watch(searchQuery, () => { page.value = 1 })

function handlePageChange(p: number, ps: number) {
  page.value = p
  pageSize.value = ps
}

async function fetchData() {
  loading.value = true
  try {
    const [res, statsRes] = await Promise.all([
      getAISkills(),
      api.get('/ai/skills/stats'),
    ])
    items.value = res.data.items || res.data
    total.value = items.value.length
    const sm: Record<string, number> = {}
    for (const s of (Array.isArray(statsRes.data) ? statsRes.data : [])) {
      sm[s.skill_name] = s.count
    }
    statsMap.value = sm
  } finally {
    loading.value = false
  }
}

function openCreate() {
  isEditingNew.value = true
  editing.value = defaultSkill()
  trendData.value = []
  dialogVisible.value = true
}

async function openDetail(row: AiSkill) {
  isEditingNew.value = false
  editing.value = { ...row }
  dialogVisible.value = true
  // fetch trend
  try {
    const res = await api.get(`/ai/skills/stats/trend?name=${row.name}&days=14`)
    trendData.value = res.data || []
    await nextTick()
    renderTrendChart()
  } catch {
    trendData.value = []
  }
}

function renderTrendChart() {
  if (!trendChartRef.value || trendData.value.length === 0) return
  if (!trendChart) trendChart = echarts.init(trendChartRef.value)
  const dates = trendData.value.map(d => d.day)
  const counts = trendData.value.map(d => d.count)
  trendChart.setOption({
    tooltip: { trigger: 'axis' },
    grid: { left: 40, right: 16, top: 20, bottom: 24 },
    xAxis: { type: 'category', data: dates, axisLabel: { fontSize: 10 } },
    yAxis: { type: 'value', minInterval: 1 },
    series: [{
      type: 'line',
      data: counts,
      smooth: true,
      lineStyle: { color: '#00d4ff', width: 2 },
      areaStyle: { color: 'rgba(0,212,255,0.15)' },
      symbol: 'circle',
      symbolSize: 6,
    }],
  })
}

function resetForm() {
  if (isEditingNew.value) {
    editing.value = defaultSkill()
  } else {
    const orig = items.value.find(i => i.name === editing.value.name)
    if (orig) editing.value = { ...orig }
  }
}

async function handleSave() {
  if (!editing.value.name) { ElMessage.warning('名称不能为空'); return }
  if (!editing.value.instruction) { ElMessage.warning('指令内容不能为空'); return }
  saving.value = true
  try {
    if (isEditingNew.value) {
      await createAISkill(editing.value)
      ElMessage.success('创建成功')
    } else {
      await updateAISkill(editing.value.name, editing.value)
      ElMessage.success('保存成功')
    }
    dialogVisible.value = false
    await fetchData()
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  } finally {
    saving.value = false
  }
}

async function handleDelete(row: AiSkill) {
  ElMessageBox.confirm(`确定删除 "${row.name}"？`, '确认删除', { type: 'warning' }).then(async () => {
    try {
      await deleteAISkill(row.name)
      ElMessage.success('删除成功')
      await fetchData()
    } catch (e: any) {
      ElMessage.error(e.message || '删除失败')
    }
  })
}

onMounted(fetchData)
</script>

<style scoped>
.skills-page {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.toolbar h3 { margin: 0; font-size: 16px; }
.toolbar-actions { display: flex; gap: 8px; align-items: center; }

.dialog-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-height: 70vh;
  overflow-y: auto;
}
.dialog-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.form-row { display: flex; gap: 8px; }
.dialog-chart {
  border-top: 1px solid var(--border, rgba(255,255,255,0.08));
  padding-top: 12px;
}
</style>