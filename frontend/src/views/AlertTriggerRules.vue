<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Bell /></el-icon> LTS 触发规则</h2>
      <p>外部告警命中规则后自动触发华为云 LTS 日志检索与 AI 巡检分析</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 规则列表</h3>
        <div class="header-actions">
          <el-input v-model="keyword" placeholder="搜索规则名称 / 匹配条件 / 告警源" clearable style="width: 240px;">
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-button type="primary" size="small" @click="openCreate">
            <el-icon><Plus /></el-icon> 新建规则
          </el-button>
        </div>
      </div>

      <el-table :data="filteredRules" v-loading="loading" stripe>
        <el-table-column label="启用" width="70">
          <template #default="{ row }">
            <el-switch v-model="row.enabled" size="small" @change="toggleRule(row)" />
          </template>
        </el-table-column>
        <el-table-column prop="name" label="规则名称" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="rule-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="匹配条件" min-width="220">
          <template #default="{ row }">
            <div class="matcher-tags">
              <el-tag v-for="(m, i) in parseMatchers(row.matchers_json)" :key="i" size="small" effect="plain" class="matcher-tag">
                {{ shortField(m.field) }} {{ opLabel(m.operator) }} {{ truncate(m.value, 14) }}
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="告警源" width="120">
          <template #default="{ row }">
            <span v-if="row.source_id && sourceMap[row.source_id]">{{ sourceMap[row.source_id].name }}</span>
            <span v-else class="muted">全部</span>
          </template>
        </el-table-column>
        <el-table-column label="LTS 检索" width="200">
          <template #default="{ row }">
            <div class="lts-cell">
              <div>组: <el-tooltip :content="row.log_group_id" placement="top" :disabled="!row.log_group_id"><span class="mono">{{ truncate(row.log_group_id, 12) }}</span></el-tooltip></div>
              <div>流: <el-tooltip :content="row.log_stream_id" placement="top" :disabled="!row.log_stream_id"><span class="mono">{{ truncate(row.log_stream_id, 12) }}</span></el-tooltip></div>
              <div class="lts-meta">{{ row.time_window_minutes }}min · {{ row.level_filter || 'ERROR,FATAL' }}</div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="巡检联动" width="110">
          <template #default="{ row }">
            <el-tooltip v-if="row.inspection_template_id && templateMap[row.inspection_template_id]" :content="templateMap[row.inspection_template_id].name" placement="top">
              <el-tag size="small" type="warning" effect="plain">
                {{ truncate(templateMap[row.inspection_template_id].name, 8) }}
              </el-tag>
            </el-tooltip>
            <span v-else class="muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="通知渠道" width="130">
          <template #default="{ row }">
            <span v-if="row.notify_channel_ids?.length" class="channel-names">
              {{ channelNames(row.notify_channel_ids) }}
            </span>
            <span v-else class="muted">全部启用</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text class="link-btn link-edit" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" text class="link-btn link-test" @click="runTest(row)">试运行</el-button>
            <el-popconfirm title="确认删除该规则？" confirm-button-text="删除" cancel-button-text="取消" @confirm="removeRule(row)">
              <template #reference>
                <el-button size="small" text class="link-btn link-danger">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && filteredRules.length === 0" :description="keyword ? '未找到匹配的规则' : '暂无触发规则，点击右上角「新建规则」创建'" />
    </div>

    <!-- 新建/编辑对话框 -->
    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑触发规则' : '新建触发规则'" width="780px" top="4vh" destroy-on-close>
      <el-form :model="form" :rules="formRules" ref="formRef" label-width="120px" v-loading="saving">
        <h4 class="form-section-title">基本信息</h4>
        <div class="form-row">
          <el-form-item label="规则名称" prop="name" class="form-grow">
            <el-input v-model="form.name" placeholder="如：订单服务 CPU 告警触发日志巡检" />
          </el-form-item>
          <el-form-item label="启用">
            <el-switch v-model="form.enabled" />
          </el-form-item>
        </div>
        <el-form-item label="规则说明">
          <el-input v-model="form.description" placeholder="可选，会注入 AI 分析上下文" />
        </el-form-item>
        <el-form-item label="限定告警源">
          <el-select v-model="form.source_id" placeholder="全部告警源" clearable filterable style="width: 100%;">
            <el-option v-for="s in sources" :key="s.id" :label="`${s.name}（${s.type}）`" :value="s.id" />
          </el-select>
          <div class="form-tip">留空则匹配所有外部告警源（含华为云 CES/AOM）</div>
        </el-form-item>

        <h4 class="form-section-title">匹配条件（全部命中才触发）</h4>
        <div v-for="(m, i) in formMatchers" :key="i" class="matcher-row">
          <el-select v-model="m.field" placeholder="字段" filterable allow-create default-first-option class="matcher-field">
            <el-option label="alertname（告警名）" value="alertname" />
            <el-option label="any（任意字段）" value="any" />
            <el-option label="label:service" value="label:service" />
            <el-option label="label:ip" value="label:ip" />
            <el-option label="annotation:summary" value="annotation:summary" />
          </el-select>
          <el-select v-model="m.operator" class="matcher-op">
            <el-option label="等于" value="equals" />
            <el-option label="包含" value="contains" />
            <el-option label="通配符" value="wildcard" />
            <el-option label="正则" value="regex" />
            <el-option label="IP 段" value="cidr" />
          </el-select>
          <el-input v-model="m.value" placeholder="值" class="matcher-value" />
          <el-button text type="danger" :disabled="formMatchers.length <= 1" @click="formMatchers.splice(i, 1)">
            <el-icon><Delete /></el-icon>
          </el-button>
        </div>
        <el-button size="small" plain @click="formMatchers.push({ field: '', operator: 'contains', value: '' })" class="matcher-add">
          <el-icon><Plus /></el-icon> 添加条件
        </el-button>

        <h4 class="form-section-title">LTS 日志检索配置</h4>
        <div class="form-row">
          <el-form-item label="日志组 ID" prop="log_group_id" class="form-grow">
            <el-input v-model="form.log_group_id" placeholder="LTS 日志组 ID" />
          </el-form-item>
          <el-form-item label="日志流 ID" prop="log_stream_id" class="form-grow">
            <el-input v-model="form.log_stream_id" placeholder="LTS 日志流 ID" />
          </el-form-item>
        </div>
        <div class="form-row">
          <el-form-item label="时间窗（分钟）">
            <el-input-number v-model="form.time_window_minutes" :min="1" :max="1440" style="width: 140px;" />
          </el-form-item>
          <el-form-item label="拉取上限（行）">
            <el-input-number v-model="form.limit" :min="50" :max="5000" :step="50" style="width: 140px;" />
          </el-form-item>
        </div>
        <div class="form-row">
          <el-form-item label="级别过滤">
            <el-input v-model="form.level_filter" placeholder="ERROR,FATAL" style="width: 180px;" />
          </el-form-item>
          <el-form-item label="附加关键字">
            <el-input v-model="form.keywords" placeholder="可选，空格分隔（分词级匹配）" style="width: 260px;" />
          </el-form-item>
        </div>

        <h4 class="form-section-title">联动与通知</h4>
        <el-form-item label="绑定巡检模板">
          <el-select v-model="form.inspection_template_id" placeholder="可选，命中规则时同时采集模板指标" clearable style="width: 100%;">
            <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id" />
          </el-select>
          <div class="form-tip">绑定后，AI 分析会把「关联指标巡检结果」一并纳入根因判断</div>
        </el-form-item>
        <el-form-item label="通知渠道">
          <el-select v-model="form.notify_channel_ids" multiple placeholder="空 = 推送到全部启用渠道" style="width: 100%;">
            <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id!" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveRule">{{ isEdit ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>

    <!-- 试运行结果 -->
    <el-dialog v-model="testVisible" title="试运行结果（回放最近历史告警）" width="680px">
      <div v-loading="testing">
        <template v-if="testResult">
          <el-alert type="info" :closable="false" class="test-alert">
            回放最近 {{ testResult.scanned }} 条历史告警，命中 <b>{{ testResult.matched }}</b> 条
          </el-alert>
          <el-table v-if="testResult.matched_list.length" :data="testResult.matched_list" size="small" max-height="360">
            <el-table-column prop="rule_name" label="告警规则" min-width="180" show-overflow-tooltip />
            <el-table-column prop="severity" label="级别" width="90">
              <template #default="{ row }">
                <el-tag size="small" :type="row.severity === 'critical' ? 'danger' : 'warning'" effect="plain">{{ row.severity }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="state" label="状态" width="80" />
            <el-table-column prop="occurred_at" label="时间" width="160">
              <template #default="{ row }">{{ dayjs(row.occurred_at).format('MM-DD HH:mm:ss') }}</template>
            </el-table-column>
          </el-table>
          <el-empty v-else description="历史告警中无命中（可能时间窗内没有匹配的告警）" />
        </template>
      </div>
      <template #footer>
        <el-button @click="testVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import dayjs from 'dayjs'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import {
  getAlertTriggerRules, createAlertTriggerRule, updateAlertTriggerRule, deleteAlertTriggerRule, testAlertTriggerRule,
  getAlertSources, getAllNotifications, getAllTemplates,
  type AlertTriggerRule, type TriggerMatcher, type TriggerRuleTestResult,
} from '../api'
import type { NotificationChannel } from '../types'

function getCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

function truncate(s: string, n: number): string {
  return s && s.length > n ? s.slice(0, n) + '…' : (s || '')
}

function shortField(f: string): string {
  if (f === 'alertname') return '告警名'
  if (f === 'any') return '任意'
  if (f.startsWith('label:')) return f.slice(6)
  if (f.startsWith('annotation:')) return '注解:' + f.slice(11)
  return f
}

function opLabel(op: string): string {
  switch (op) {
    case 'equals': return '='
    case 'contains': return '包含'
    case 'wildcard': return '通配'
    case 'regex': return '正则'
    case 'cidr': return '网段'
    default: return op
  }
}

function parseMatchers(raw: string): TriggerMatcher[] {
  try { return JSON.parse(raw) || [] } catch { return [] }
}

const loading = ref(false)
const rules = ref<AlertTriggerRule[]>([])
const keyword = ref('')
const filteredRules = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return rules.value
  return rules.value.filter(r => {
    if (r.name.toLowerCase().includes(kw)) return true
    // 匹配条件
    const matchers = parseMatchers(r.matchers_json)
    if (matchers.some(m => (m.field + m.value).toLowerCase().includes(kw))) return true
    // 告警源
    if (r.source_id && sourceMap.value[r.source_id]?.name.toLowerCase().includes(kw)) return true
    return false
  })
})

const sources = ref<any[]>([])
const channels = ref<NotificationChannel[]>([])
const templates = ref<any[]>([])
const sourceMap = computed(() => Object.fromEntries(sources.value.map(s => [s.id, s])))
const channelMap = computed(() => Object.fromEntries(channels.value.map(c => [c.id, c])))
const templateMap = computed(() => Object.fromEntries(templates.value.map(t => [t.id, t])))

function channelNames(ids: number[]): string {
  return ids.map((id: number) => channelMap.value[id]?.name || `#${id}`).join('、')
}

async function fetchAll() {
  loading.value = true
  try {
    const [rulesRes, srcRes, chRes, tplRes] = await Promise.all([
      getAlertTriggerRules(), getAlertSources(), getAllNotifications(), getAllTemplates(),
    ])
    rules.value = rulesRes.data
    sources.value = srcRes.data
    channels.value = chRes.data
    templates.value = tplRes.data
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

// ===== 新建/编辑 =====
const dialogVisible = ref(false)
const saving = ref(false)
const isEdit = ref(false)
const editingId = ref(0)
const form = ref<AlertTriggerRule>(emptyForm())
const formMatchers = ref<TriggerMatcher[]>([{ field: '', operator: 'contains', value: '' }])
const formRef = ref<FormInstance>()

const formRules: FormRules = {
  name: [{ required: true, message: '请填写规则名称', trigger: 'blur' }],
  log_group_id: [{ required: true, message: '请填写日志组 ID', trigger: 'blur' }],
  log_stream_id: [{ required: true, message: '请填写日志流 ID', trigger: 'blur' }],
}

function emptyForm(): AlertTriggerRule {
  return {
    name: '', description: '',
    matchers_json: '[]',
    source_id: null,
    log_group_id: '', log_stream_id: '',
    time_window_minutes: 15,
    keywords: '',
    level_filter: 'ERROR,FATAL',
    limit: 200,
    inspection_template_id: null,
    notify_channel_ids: [],
    enabled: true,
  }
}

function openCreate() {
  isEdit.value = false
  editingId.value = 0
  form.value = emptyForm()
  formMatchers.value = [{ field: '', operator: 'contains', value: '' }]
  dialogVisible.value = true
}

function openEdit(row: AlertTriggerRule) {
  isEdit.value = true
  editingId.value = row.id!
  form.value = { ...emptyForm(), ...row }
  formMatchers.value = parseMatchers(row.matchers_json)
  if (formMatchers.value.length === 0) formMatchers.value = [{ field: '', operator: 'contains', value: '' }]
  dialogVisible.value = true
}

async function saveRule() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (valid === false) return
  const ms = formMatchers.value.filter(m => m.field.trim() && m.value.trim())
  if (ms.length === 0) { ElMessage.warning('至少需要一个有效的匹配条件'); return }

  const payload: AlertTriggerRule = {
    ...form.value,
    matchers_json: JSON.stringify(ms),
  }
  saving.value = true
  try {
    if (isEdit.value) {
      await updateAlertTriggerRule(editingId.value, payload)
      ElMessage.success('规则已更新')
    } else {
      await createAlertTriggerRule(payload)
      ElMessage.success('规则已创建')
    }
    dialogVisible.value = false
    fetchAll()
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function toggleRule(row: AlertTriggerRule) {
  try {
    await updateAlertTriggerRule(row.id!, row)
    ElMessage.success(row.enabled ? '已启用' : '已停用')
  } catch (e: any) {
    ElMessage.error(e.message)
    fetchAll()
  }
}

async function removeRule(row: AlertTriggerRule) {
  try {
    await deleteAlertTriggerRule(row.id!)
    ElMessage.success('已删除')
    fetchAll()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

// ===== 试运行 =====
const testVisible = ref(false)
const testing = ref(false)
const testResult = ref<TriggerRuleTestResult | null>(null)

async function runTest(row: AlertTriggerRule) {
  testVisible.value = true
  testing.value = true
  testResult.value = null
  try {
    const res = await testAlertTriggerRule(row.id!)
    testResult.value = res.data
  } catch (e: any) {
    ElMessage.error(e.message)
    testVisible.value = false
  } finally {
    testing.value = false
  }
}

onMounted(fetchAll)
</script>

<style scoped>
.header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
.rule-name {
  font-weight: 500;
}
.mono {
  font-family: monospace;
}
.muted {
  color: var(--text-tertiary, #909399);
}
.lts-cell {
  font-size: 12px;
  line-height: 1.7;
}
.lts-meta {
  color: var(--text-tertiary, #909399);
}
.channel-names {
  font-size: 12px;
}
.link-btn {
  padding: 0 6px;
}
.link-edit {
  color: var(--cyan);
}
.link-test {
  color: var(--amber, #f59e0b);
}
.link-danger {
  color: var(--el-color-danger);
}
.matcher-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.matcher-tag {
  font-size: 12px;
}
.matcher-row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin: 0 0 8px 120px;
}
.matcher-field {
  width: 190px;
}
.matcher-op {
  width: 110px;
}
.matcher-value {
  flex: 1;
}
.matcher-add {
  margin-left: 120px;
}
.form-row {
  display: flex;
  gap: 16px;
}
.form-grow {
  flex: 1;
}
.form-section-title {
  margin: 18px 0 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border, #e4e7ed);
  font-size: 14px;
  color: var(--text-primary, #303133);
}
.form-tip {
  font-size: 12px;
  color: var(--text-tertiary, #909399);
  line-height: 1.5;
  margin-top: 4px;
}
.test-alert {
  margin-bottom: 12px;
}
</style>
