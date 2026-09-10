<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Aim /></el-icon> 预期行为</h2>
      <p>按标签匹配标记"超阈值但属正常"的告警，AI 巡检分析时标注为预期行为（不排除、不影响通知）</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 预期行为规则</h3>
        <div class="action-bar">
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新建规则</el-button>
        </div>
      </div>
      <el-table :data="items" v-loading="loading" stripe>
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column prop="comment" label="说明" min-width="220" show-overflow-tooltip />
        <el-table-column label="匹配条件" min-width="280">
          <template #default="{ row }">
            <el-tag v-for="m in safeMatchers(row.matchers_json)" :key="m.name+m.op+m.value" size="small" style="margin:1px 4px 1px 0;">
              {{ m.name }} {{ m.op }} {{ m.value }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建人" width="120" prop="created_by" />
        <el-table-column label="启用" width="70" align="center">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" size="small" @change="toggle(row)" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text style="color:var(--cyan);" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" text style="color:var(--red);" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pager">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchData"
          @size-change="fetchData"
        />
      </div>
      <el-empty v-if="!loading && items.length===0" description="暂无预期行为规则" :image-size="60" />
    </div>

    <el-dialog v-model="dialog" :title="editingId?'编辑预期行为':'新建预期行为'" width="560">
      <el-form :model="form" label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="如：实时流 Redis 命中率低" />
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="form.comment" type="textarea" :rows="2" placeholder="为什么这是预期行为，如：实时流数据为预期行为" />
        </el-form-item>
        <el-form-item label="匹配条件">
          <div style="font-size:12px;color:var(--text-tertiary);margin-bottom:6px;">
            按 alert 标签匹配（如 <code>alertname</code>=<code>Redis命中率低</code> 且 <code>redis_type</code>=<code>streaming</code>）；多条之间是 AND 关系
          </div>
          <el-table :data="matchers" size="small">
            <el-table-column label="标签名" width="140"><template #default="{row,$index}"><el-input v-model="row.name" size="small" placeholder="alertname" /></template></el-table-column>
            <el-table-column label="op" width="80"><template #default="{row,$index}"><el-select v-model="row.op" size="small"><el-option label="=" value="=" /><el-option label="!=" value="!=" /><el-option label="=~" value="=~" /><el-option label="!~" value="!~" /></el-select></template></el-table-column>
            <el-table-column label="值"><template #default="{row,$index}"><el-input v-model="row.value" size="small" placeholder="Redis命中率低" /></template></el-table-column>
            <el-table-column width="50"><template #default="{row,$index}"><el-button size="small" text style="color:var(--red);" @click="matchers.splice($index,1)">✕</el-button></template></el-table-column>
          </el-table>
          <el-button size="small" plain style="margin-top:6px;" @click="matchers.push({name:'',op:'=',value:''})"><el-icon><Plus /></el-icon>添加</el-button>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getInspectionExpectations, createInspectionExpectation, updateInspectionExpectation, deleteInspectionExpectation,
} from '../api'
import type { InspectionExpectation } from '../types/alerting'

function getCssVar(n: string) { return getComputedStyle(document.documentElement).getPropertyValue(n).trim() }

const loading = ref(false)
const items = ref<InspectionExpectation[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const dialog = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const form = ref<InspectionExpectation>({ name: '', matchers_json: '[]', comment: '', enabled: true })
const matchers = ref<Array<{ name: string; op: string; value: string }>>([])

function safeMatchers(s: string) { try { return JSON.parse(s)||[] } catch { return [] } }

async function fetchData() {
  loading.value = true
  try {
    const r = await getInspectionExpectations({ page: page.value, page_size: pageSize.value })
    items.value = r.data.items
    total.value = r.data.total
  } catch (e: any) { ElMessage.error(e.message) }
  finally { loading.value = false }
}

function openCreate() {
  editingId.value = null
  form.value = { name: '', matchers_json: '[]', comment: '', enabled: true }
  matchers.value = [{ name: 'alertname', op: '=', value: '' }]
  dialog.value = true
}
function openEdit(row: InspectionExpectation) {
  editingId.value = row.id!
  form.value = { ...row }
  try { matchers.value = JSON.parse(row.matchers_json)||[] } catch { matchers.value = [] }
  dialog.value = true
}
async function handleSave() {
  if (!form.value.name.trim()) { ElMessage.warning('请填写名称'); return }
  const valid = matchers.value.filter(m => m.name && m.value)
  if (valid.length === 0) { ElMessage.warning('至少需要一条匹配条件'); return }
  form.value.matchers_json = JSON.stringify(valid)
  saving.value = true
  try {
    if (editingId.value) { await updateInspectionExpectation(editingId.value, form.value); ElMessage.success('更新成功') }
    else { await createInspectionExpectation(form.value); ElMessage.success('创建成功') }
    dialog.value = false; await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
  finally { saving.value = false }
}
async function toggle(row: InspectionExpectation) {
  try { await updateInspectionExpectation(row.id!, { ...row, enabled: !row.enabled }); await fetchData() } catch (e: any) { ElMessage.error(e.message) }
}
async function handleDelete(row: InspectionExpectation) {
  try {
    await ElMessageBox.confirm(`确定删除「${row.name}」？`, '确认', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
    await deleteInspectionExpectation(row.id!); ElMessage.success('已删除'); await fetchData()
  } catch {}
}
onMounted(fetchData)
</script>

<style scoped>
.pager { display: flex; justify-content: flex-end; padding: 12px 4px; }
.action-bar { display: flex; gap: 8px; }
code { background: rgba(99,102,241,0.08); padding: 1px 5px; border-radius: 3px; font-size: 11.5px; color: #818cf8; }
</style>