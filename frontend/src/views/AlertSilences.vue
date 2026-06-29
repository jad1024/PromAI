<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Mute /></el-icon> 告警静默</h2>
      <p>按标签匹配静默告警，静默期内不进行通知路由与分发</p>
    </div>
    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 静默规则</h3>
        <div class="action-bar">
          <el-checkbox v-model="showExpired" @change="fetchData">含已过期</el-checkbox>
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新建静默</el-button>
        </div>
      </div>
      <el-table :data="items" v-loading="loading" stripe size="default">
        <el-table-column prop="comment" label="原因" min-width="200" />
        <el-table-column label="匹配条件" min-width="280">
          <template #default="{ row }">
            <el-tag v-for="m in safeMatchers(row.matchers_json)" :key="m.name+m.op+m.value" size="small" style="margin:1px 4px 1px 0;">
              {{ m.name }} {{ m.op }} {{ m.value }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="开始" width="160"><template #default="{ row }">{{ fmt(row.starts_at) }}</template></el-table-column>
        <el-table-column label="结束" width="160"><template #default="{ row }">{{ fmt(row.ends_at) }}</template></el-table-column>
        <el-table-column label="创建人" width="120" prop="created_by" />
        <el-table-column label="已静默" width="80" align="center">
          <template #default="{ row }">
            <span v-if="row.matched_count !== undefined" style="color:var(--text-secondary);">{{ row.matched_count }}</span>
            <span v-else style="color:var(--text-tertiary);">-</span>
          </template>
        </el-table-column>
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
      <el-empty v-if="!loading && items.length===0" description="暂无静默" :image-size="60" />
    </div>

    <el-dialog v-model="dialog" :title="editingId?'编辑静默':'新建静默'" width="560">
      <el-form :model="form" label-width="100px">
        <el-form-item label="静默原因">
          <el-input v-model="form.comment" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="匹配条件">
          <div style="font-size:12px;color:var(--text-tertiary);margin-bottom:6px;">多条之间是 AND 关系</div>
          <el-table :data="matchers" size="small">
            <el-table-column label="标签名" width="140"><template #default="{row,$index}"><el-input v-model="row.name" size="small" /></template></el-table-column>
            <el-table-column label="op" width="80"><template #default="{row,$index}"><el-select v-model="row.op" size="small"><el-option label="=" value="=~" disabled/><el-option label="=" value="=" /><el-option label="!=" value="!=" /><el-option label="=~" value="=~" /><el-option label="!~" value="!~" /></el-select></template></el-table-column>
            <el-table-column label="值"><template #default="{row,$index}"><el-input v-model="row.value" size="small" /></template></el-table-column>
            <el-table-column width="50"><template #default="{row,$index}"><el-button size="small" text style="color:var(--red);" @click="matchers.splice($index,1)">✕</el-button></template></el-table-column>
          </el-table>
          <el-button size="small" plain style="margin-top:6px;" @click="matchers.push({name:'',op:'=',value:''})"><el-icon><Plus /></el-icon>添加</el-button>
        </el-form-item>
        <el-row :gutter="16">
          <el-col :span="12">
            <el-form-item label="开始时间">
              <el-date-picker v-model="form.starts_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" style="width:100%;" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="结束时间">
              <el-date-picker v-model="form.ends_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" style="width:100%;" />
            </el-form-item>
          </el-col>
        </el-row>
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
import { getAlertSilences, createAlertSilence, updateAlertSilence, deleteAlertSilence } from '../api'
import type { AlertSilence } from '../types/alerting'

function getCssVar(n: string) { return getComputedStyle(document.documentElement).getPropertyValue(n).trim() }

const loading = ref(false)
const items = ref<AlertSilence[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const showExpired = ref(false)
const dialog = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const form = ref<AlertSilence>({ comment: '', matchers_json: '[]', starts_at: '', ends_at: '', enabled: true })
const matchers = ref<Array<{ name: string; op: string; value: string }>>([])

function safeMatchers(s: string) { try { return JSON.parse(s)||[] } catch { return [] } }
function fmt(t: string) { if (!t) return '-'; const d=new Date(t); return isNaN(d.getTime())?'-':d.toLocaleString() }

async function fetchData() {
  loading.value = true
  try {
    const r = await getAlertSilences({ include_expired: showExpired.value, page: page.value, page_size: pageSize.value })
    items.value = r.data.items
    total.value = r.data.total
  } catch (e: any) { ElMessage.error(e.message) }
  finally { loading.value = false }
}

function openCreate() {
  editingId.value = null
  form.value = { comment: '', matchers_json: '[]', starts_at: new Date().toISOString(), ends_at: '', enabled: true }
  matchers.value = [{ name: 'alertname', op: '=', value: '' }]
  dialog.value = true
}
function openEdit(row: AlertSilence) {
  editingId.value = row.id!
  form.value = { ...row }
  try { matchers.value = JSON.parse(row.matchers_json)||[] } catch { matchers.value=[] }
  dialog.value = true
}
async function handleSave() {
  if (!form.value.comment.trim()) { ElMessage.warning('请填写静默原因'); return }
  if (form.value.starts_at && !form.value.ends_at) { ElMessage.warning('请选择结束时间'); return }
  const valid = matchers.value.filter(m => m.name && m.value)
  if (valid.length===0) { ElMessage.warning('至少需要一条匹配条件'); return }
  form.value.matchers_json = JSON.stringify(valid)
  saving.value = true
  try {
    if (editingId.value) { await updateAlertSilence(editingId.value, form.value); ElMessage.success('更新成功') }
    else { await createAlertSilence(form.value); ElMessage.success('创建成功') }
    dialog.value = false; await fetchData()
  } catch (e: any) { ElMessage.error(e.message) } finally { saving.value = false }
}
async function toggle(row: AlertSilence) {
  try { await updateAlertSilence(row.id!, { ...row, enabled: !row.enabled }); await fetchData() } catch (e: any) { ElMessage.error(e.message) }
}
async function handleDelete(row: AlertSilence) {
  try { await ElMessageBox.confirm('确定删除此静默？','确认',{type:'warning',confirmButtonText:'删除',cancelButtonText:'取消'}); await deleteAlertSilence(row.id!); ElMessage.success('已删除'); await fetchData() } catch {}
}
onMounted(fetchData)
</script>

<style scoped>
.pager { display: flex; justify-content: flex-end; padding: 12px 4px; }
</style>
