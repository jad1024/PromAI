<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Link /></el-icon> 告警源管理</h2>
      <p>接入 n9e / 华为云 CES / 阿里云 CloudMonitor / 通用 Webhook 的外部告警：规则同步 + 告警推送统一汇聚</p>
    </div>

    <!-- 接入说明 -->
    <div class="section-card" style="margin-bottom: 16px;">
      <div class="webhook-guide">
        <el-icon :size="16" color="var(--cyan)"><Promotion /></el-icon>
        <div class="guide-text">
          <span>外部平台告警推送到本系统：</span>
          <code>POST /api/promai/webhook/alerts/:id</code>
          <span class="guide-hint">n9e（回调 / 通知脚本）、华为云 SMN（HTTPS 订阅）、阿里云云监控（报警回调）、Alertmanager 兼容格式均支持；需携带 <code>Authorization: Bearer &lt;token&gt;</code>。</span>
        </div>
      </div>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 告警源列表</h3>
        <div class="action-bar">
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon> 刷新</el-button>
          <el-button type="primary" @click="openCreate">
            <el-icon><Plus /></el-icon> 新增告警源
          </el-button>
        </div>
      </div>

      <el-table :data="sources" v-loading="loading" stripe>
        <el-table-column prop="name" label="名称" min-width="160">
          <template #default="{ row }">
            <div style="display: flex; align-items: center; gap: 8px;">
              <span :style="{ fontWeight: 600, color: row.enabled === false ? 'var(--text-tertiary)' : 'var(--text-primary)', textDecoration: row.enabled === false ? 'line-through' : 'none' }">{{ row.name }}</span>
              <el-tag size="small" :style="typeStyle(row.type)">{{ typeLabel(row.type) }}</el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="连接信息" min-width="220">
          <template #default="{ row }">
            <div style="font-size: 12px; line-height: 1.7; color: var(--text-secondary);">
              <div v-if="row.url"><code>{{ row.url }}</code></div>
              <div v-if="row.type === 'huaweicloud'">
                <span>{{ row.region || '未配置区域' }} · {{ row.project_id ? 'project ' + row.project_id : '未配置 project_id' }}</span>
              </div>
              <div v-if="row.type === 'n9e' && !row.url" style="color: var(--danger);">未配置 n9e 地址</div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="规则同步" min-width="150">
          <template #default="{ row }">
            <div style="display: flex; flex-direction: column; gap: 3px; font-size: 12px;">
              <span style="color: var(--text-secondary);">
                周期 {{ syncIntervalLabel(row.sync_interval) }}
              </span>
              <span v-if="row.last_sync_at" style="color: var(--text-tertiary);">{{ fmt(row.last_sync_at) }}</span>
              <span v-else style="color: var(--text-tertiary);">尚未同步</span>
              <el-tag v-if="row.sync_status" size="small" :style="syncStyle(row.sync_status)">{{ syncLabel(row.sync_status) }}</el-tag>
              <el-tooltip v-if="row.sync_error" :content="row.sync_error" placement="top">
                <span style="color: var(--danger); cursor: help; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ row.sync_error }}</span>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="Webhook 地址" min-width="200">
          <template #default="{ row }">
            <div style="display: flex; align-items: center; gap: 6px;">
              <code style="font-size: 11px; color: var(--text-tertiary); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">/api/promai/webhook/alerts/{{ row.id }}</code>
              <el-button size="small" text @click="copyWebhook(row)" title="复制完整地址">
                <el-icon><CopyDocument /></el-icon>
              </el-button>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="开关" width="190" align="center">
          <template #default="{ row }">
            <div class="switch-group">
              <div class="switch-row">
                <el-switch :model-value="row.enabled !== false" size="small" @change="toggleField(row, 'enabled', $event)" />
                <span class="switch-label" :class="{ off: row.enabled === false }">启用源</span>
              </div>
              <div class="switch-row">
                <el-switch :model-value="!!row.notify_enabled" size="small" @change="toggleField(row, 'notify_enabled', $event)" />
                <span class="switch-label" :class="{ off: !row.notify_enabled }">通知转发</span>
              </div>
              <div class="switch-row">
                <el-switch :model-value="!!row.ai_analysis_enabled" size="small" @change="toggleField(row, 'ai_analysis_enabled', $event)" />
                <span class="switch-label" :class="{ off: !row.ai_analysis_enabled }">AI 分析</span>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="240" align="center">
          <template #default="{ row }">
            <el-button size="small" plain type="primary" :disabled="row.type === 'generic' || row.type === 'aliyun' || !row.enabled" :loading="syncingId === row.id" @click="doSync(row)" class="sync-btn">
              <el-icon><Refresh /></el-icon> 同步规则
            </el-button>
            <el-button size="small" text @click="openRules(row)">
              <el-icon><Collection /></el-icon> 规则
            </el-button>
            <el-button size="small" text @click="openEdit(row)">
              <el-icon><Edit /></el-icon> 编辑
            </el-button>
            <el-button size="small" text type="danger" @click="doDelete(row)">
              <el-icon><Delete /></el-icon>
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 30px; color: var(--text-tertiary);">
            暂无告警源，点击右上角「新增告警源」接入 n9e / 华为云 / 阿里云 / 通用 Webhook
          </div>
        </template>
      </el-table>
    </div>

    <!-- 创建 / 编辑 -->
    <el-dialog v-model="dialogVisible" :title="form.id ? '编辑告警源' : '新增告警源'" width="640" :close-on-click-modal="false">
      <el-form label-width="130px" style="padding-right: 12px;">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="如：夜莺告警 / 华为云生产告警" maxlength="100" />
        </el-form-item>
        <el-form-item label="类型" required>
          <el-select v-model="form.type" :disabled="!!form.id" style="width: 100%;">
            <el-option label="n9e 夜莺" value="n9e" />
            <el-option label="华为云 CES" value="huaweicloud" />
            <el-option label="阿里云 CloudMonitor" value="aliyun" />
            <el-option label="通用 Webhook" value="generic" />
          </el-select>
        </el-form-item>
        <el-form-item :label="form.type === 'huaweicloud' ? 'Endpoint（可选）' : '服务地址'">
          <el-input v-model="form.url" :placeholder="urlPlaceholder" maxlength="500" />
          <div class="form-tip">{{ urlTip }}</div>
        </el-form-item>

        <!-- n9e 凭据 -->
        <template v-if="form.type === 'n9e'">
          <el-form-item label="API Token">
            <el-input v-model="form.n9e_token" type="password" show-password :placeholder="form.id ? '留空则不修改' : 'n9e 个人中心创建的 Token（推荐）'" maxlength="300" />
            <div class="form-tip">n9e v8.0.0-beta.5+ 官方认证方式：在 n9e 右上角头像 → 个人中心 → 「Token 管理」创建 Token（需 n9e 配置 <code>[HTTP.TokenAuth] Enable=true</code>）。填写后优先使用，无需账号密码。</div>
          </el-form-item>
          <el-form-item label="用户名">
            <el-input v-model="form.username" :placeholder="form.id ? '留空则不修改' : '登录 n9e 的用户名（v6/v7 兼容）'" maxlength="100" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="form.password" type="password" show-password :placeholder="form.id ? '留空则不修改' : '登录密码'" maxlength="200" />
          </el-form-item>
          <div class="form-tip" style="margin-top: -8px; padding-bottom: 8px;">API Token 与账号密码二选一：v8+ 新版本已不再使用 /api/n9e/auth/login 登录接口，建议用 Token；旧版本（v6/v7）用账号密码。</div>
        </template>

        <!-- 华为云凭据 -->
        <template v-if="form.type === 'huaweicloud'">
          <el-form-item label="区域 Region">
            <el-input v-model="form.region" placeholder="如 cn-north-4" maxlength="50" />
          </el-form-item>
          <el-form-item label="Project ID">
            <el-input v-model="form.project_id" placeholder="华为云项目 ID" maxlength="100" />
          </el-form-item>
          <el-form-item label="Access Key">
            <el-input v-model="form.access_key" :placeholder="form.id ? '留空则不修改' : 'AK'" maxlength="200" />
          </el-form-item>
          <el-form-item label="Secret Key">
            <el-input v-model="form.secret_key" type="password" show-password :placeholder="form.id ? '留空则不修改' : 'SK'" maxlength="200" />
          </el-form-item>
        </template>

        <!-- 阿里云：报警回调说明 -->
        <template v-if="form.type === 'aliyun'">
          <div class="form-tip" style="margin-top: -8px; padding-bottom: 8px;">
            阿里云接入方式：云监控控制台 → 报警规则 → 编辑规则 → 高级配置「报警回调」，填入本页保存后列表中的 Webhook 地址（请求头携带
            <code>Authorization: Bearer &lt;token&gt;</code>）。alertState ALERT / OK 会自动识别为触发 / 恢复，value 与阈值从 curValue / expression 解析。规则自动同步暂未开放。
          </div>
        </template>

        <!-- 通用 webhook：SMN 订阅确认 -->
        <template v-if="form.type === 'generic'">
          <div class="form-tip" style="margin-top: -8px; padding-bottom: 8px;">通用源仅用于接收 webhook 推送（如 Alertmanager / 自研脚本 / SMN），无需登录凭据。</div>
        </template>

        <el-form-item label="Webhook Token">
          <div style="display: flex; gap: 8px; width: 100%;">
            <el-input v-model="form.token" :placeholder="form.id ? '留空则不修改' : '外部平台推送时的鉴权 token'" maxlength="200">
              <template #append>
                <el-button @click="genToken"><el-icon><MagicStick /></el-icon> 生成</el-button>
              </template>
            </el-input>
          </div>
          <div class="form-tip">推送请求须携带 <code>Authorization: Bearer &lt;token&gt;</code>；保存后列表不再明文显示。</div>
        </el-form-item>

        <el-form-item label="规则同步周期">
          <el-select v-model="form.sync_interval" style="width: 100%;">
            <el-option label="30 分钟" value="30m" />
            <el-option label="1 小时" value="1h" />
            <el-option label="6 小时" value="6h" />
            <el-option label="1 天" value="1d" />
          </el-select>
          <div class="form-tip">周期拉取平台告警规则到本地展示（generic 类型不适用）。</div>
        </el-form-item>

        <el-form-item label="功能开关">
          <div class="dialog-switch-group">
            <div class="dialog-switch-row">
              <el-switch v-model="form.enabled" />
              <div class="dialog-switch-label">
                <div class="dialog-switch-title">启用告警源</div>
                <div class="dialog-switch-desc">接收该平台的 webhook 告警推送与规则同步</div>
              </div>
            </div>
            <div class="dialog-switch-row">
              <el-switch v-model="form.notify_enabled" />
              <div class="dialog-switch-label">
                <div class="dialog-switch-title">告警通知转发</div>
                <div class="dialog-switch-desc">触发时按所有已启用渠道发送文本告警</div>
              </div>
            </div>
            <div class="dialog-switch-row">
              <el-switch v-model="form.ai_analysis_enabled" />
              <div class="dialog-switch-label">
                <div class="dialog-switch-title">AI 根因分析</div>
                <div class="dialog-switch-desc">外部告警触发时异步调用 AI 生成根因与处理建议</div>
              </div>
            </div>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="dialogVisible = false" style="color: var(--text-secondary);">取消</el-button>
          <el-button type="primary" :loading="saving" @click="save">{{ form.id ? '保存修改' : '创建' }}</el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 同步规则抽屉 -->
    <el-drawer v-model="rulesVisible" :title="'已同步规则 · ' + (rulesSource?.name || '')" size="68%">
      <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 12px;">
        <el-button size="small" type="primary" :loading="syncingId === rulesSource?.id" @click="doSync(rulesSource)">
          <el-icon><Refresh /></el-icon> 立即同步
        </el-button>
        <span style="font-size: 12px; color: var(--text-tertiary);">共 {{ rulesTotal }} 条 · 仅展示只读，不参与本地评估</span>
      </div>
      <el-table :data="rules" v-loading="rulesLoading" stripe size="small">
        <el-table-column prop="rule_name" label="规则名称" min-width="180" show-overflow-tooltip />
        <el-table-column prop="external_id" label="平台 ID" min-width="100" show-overflow-tooltip>
          <template #default="{ row }">
            <code style="font-size: 11px; color: var(--text-tertiary);">{{ row.external_id }}</code>
          </template>
        </el-table-column>
        <el-table-column label="级别" width="90" align="center">
          <template #default="{ row }">
            <el-tag size="small" :style="sevStyle(row.severity)">{{ sevLabel(row.severity) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90" align="center">
          <template #default="{ row }">
            <el-tag size="small" :style="statusStyle(row.status)">{{ row.status === 'enabled' ? '启用' : '禁用' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="condition" label="条件 / 表达式" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <code style="font-size: 11px; color: var(--text-secondary);">{{ row.condition || '-' }}</code>
          </template>
        </el-table-column>
        <el-table-column label="最近发现" width="150">
          <template #default="{ row }">
            <span style="font-size: 12px; color: var(--text-tertiary);">{{ fmt(row.last_seen_at) }}</span>
          </template>
        </el-table-column>
        <template #empty>
          <div style="padding: 24px; color: var(--text-tertiary); text-align: center;">
            暂无规则，点击「立即同步」从平台拉取
          </div>
        </template>
      </el-table>
      <div style="display: flex; justify-content: flex-end; margin-top: 12px;">
        <el-pagination
          layout="prev, pager, next, total"
          :total="rulesTotal"
          :page-size="rulesPageSize"
          :current-page="rulesPage"
          @current-change="onRulesPage"
        />
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getAlertSources, createAlertSource, updateAlertSource, deleteAlertSource,
  syncAlertSource, getAlertSourceRules,
} from '../api'
import type { ExternalAlertSource, ExternalRule } from '../types/alerting'

const loading = ref(false)
const saving = ref(false)
const syncingId = ref<number | null>(null)
const sources = ref<ExternalAlertSource[]>([])

const dialogVisible = ref(false)
const form = ref<ExternalAlertSource>(emptyForm())
const rulesVisible = ref(false)
const rulesSource = ref<ExternalAlertSource | null>(null)
const rules = ref<ExternalRule[]>([])
const rulesTotal = ref(0)
const rulesPage = ref(1)
const rulesPageSize = ref(10)
const rulesLoading = ref(false)

function emptyForm(): ExternalAlertSource {
  return {
    name: '', type: 'n9e', enabled: true, url: '',
    region: '', project_id: '', access_key: '', secret_key: '',
    username: '', password: '', n9e_token: '', token: '',
    sync_interval: '1h', notify_enabled: false, ai_analysis_enabled: false,
  }
}

function getCssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#00d4ff'
}

const typeLabel = (t: string) => ({ n9e: 'N9E', huaweicloud: '华为云', aliyun: '阿里云', generic: '通用' } as Record<string, string>)[t] || t
const typeStyle = (t: string) => {
  const m: Record<string, any> = {
    n9e: { background: 'rgba(99,102,241,0.12)', color: '#818cf8', border: 'none' },
    huaweicloud: { background: 'rgba(59,130,246,0.12)', color: '#3b82f6', border: 'none' },
    aliyun: { background: 'rgba(255,106,0,0.12)', color: '#ff6a00', border: 'none' },
    generic: { background: 'rgba(148,163,184,0.12)', color: '#94a3b8', border: 'none' },
  }
  return m[t] || m.generic
}

const urlPlaceholder = () => form.value.type === 'huaweicloud'
  ? '默认 https://ces.{region}.myhuaweicloud.com，可留空'
  : form.value.type === 'n9e' ? '如 https://n9e.example.com'
  : form.value.type === 'aliyun' ? '可留空（阿里云仅接收回调推送）' : '留空即可'

const urlTip = () => form.value.type === 'n9e'
  ? 'n9e 服务地址，用于 X-User-Token 或账号密码认证并拉取告警规则'
  : form.value.type === 'huaweicloud'
    ? '留空则按所选 region 使用华为云默认 CES endpoint'
    : form.value.type === 'aliyun'
      ? '阿里云源通过云监控「报警回调」推送告警，无需服务地址'
      : '通用源通常无需服务地址'

const syncIntervalLabel = (s?: string) => {
  const m: Record<string, string> = { '30m': '30 分钟', '1h': '1 小时', '6h': '6 小时', '1d': '1 天' }
  return m[s || ''] || s || '1 小时'
}
const syncLabel = (s: string) => ({ success: '同步成功', failed: '同步失败', pending: '同步中' } as Record<string, string>)[s] || s
const syncStyle = (s: string) => {
  const m: Record<string, any> = {
    success: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    failed: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    pending: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
  }
  return m[s] || {}
}
const sevLabel = (s: string) => ({ critical: '严重', warning: '警告', info: '提醒' } as Record<string, string>)[s] || s
const sevStyle = (s: string) => {
  const m: Record<string, any> = {
    critical: { background: '#ef4444', color: '#fff', border: 'none' },
    warning: { background: '#f59e0b', color: '#fff', border: 'none' },
    info: { background: '#3b82f6', color: '#fff', border: 'none' },
  }
  return m[s] || { background: '#94a3b8', color: '#fff', border: 'none' }
}
const statusStyle = (s: string) => s === 'enabled'
  ? { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' }
  : { background: 'rgba(148,163,184,0.15)', color: '#94a3b8', border: 'none' }

function fmt(t?: string | null) {
  if (!t) return '-'
  const d = new Date(t)
  return isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

function webhookURL(id: number) {
  return `${window.location.origin}/api/promai/webhook/alerts/${id}`
}

async function copyWebhook(row: ExternalAlertSource) {
  if (!row.id) return
  try {
    await navigator.clipboard.writeText(webhookURL(row.id))
    ElMessage.success('Webhook 地址已复制')
  } catch {
    ElMessage.warning('复制失败，请手动复制')
  }
}

async function fetchData() {
  loading.value = true
  try {
    const r = await getAlertSources()
    sources.value = r.data || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载告警源失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.value = emptyForm()
  dialogVisible.value = true
}

function openEdit(row: ExternalAlertSource) {
  form.value = {
    ...emptyForm(),
    id: row.id,
    name: row.name,
    type: row.type,
    enabled: row.enabled !== false,
    url: row.url || '',
    region: row.region || '',
    project_id: row.project_id || '',
    access_key: row.access_key || '',
    username: row.username || '',
    sync_interval: row.sync_interval || '1h',
    notify_enabled: !!row.notify_enabled,
    ai_analysis_enabled: !!row.ai_analysis_enabled,
    // secret_key / password / n9e_token / token 后端脱敏，编辑时留空表示不修改
  }
  dialogVisible.value = true
}

function genToken() {
  const arr = new Uint8Array(24)
  crypto.getRandomValues(arr)
  form.value.token = Array.from(arr, b => b.toString(16).padStart(2, '0')).join('')
}

async function save() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  saving.value = true
  try {
    if (form.value.id) {
      await updateAlertSource(form.value.id, form.value)
      ElMessage.success('已保存')
    } else {
      await createAlertSource(form.value)
      ElMessage.success('告警源已创建')
    }
    dialogVisible.value = false
    await fetchData()
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function toggleField(row: ExternalAlertSource, field: 'enabled' | 'notify_enabled' | 'ai_analysis_enabled', val: boolean | string | number) {
  const payload: any = { ...row }
  payload[field] = !!val
  try {
    await updateAlertSource(row.id!, { [field]: !!val })
    row[field] = !!val
  } catch (e: any) {
    ElMessage.error(e.message || '切换失败')
  }
}

async function doSync(row?: ExternalAlertSource | null) {
  const src = row || rulesSource.value
  if (!src?.id || src.type === 'generic') return
  syncingId.value = src.id
  try {
    const r = await syncAlertSource(src.id)
    ElMessage.success(`同步完成：新增 ${r.data.created}，更新 ${r.data.updated}，共 ${r.data.total} 条`)
    await fetchData()
    if (rulesVisible.value) await loadRules(1)
  } catch (e: any) {
    ElMessage.error(e.message || '同步失败')
  } finally {
    syncingId.value = null
  }
}

async function openRules(row: ExternalAlertSource) {
  rulesSource.value = row
  rules.value = []
  rulesTotal.value = 0
  rulesPage.value = 1
  rulesVisible.value = true
  await loadRules(1)
}

async function loadRules(page: number) {
  const src = rulesSource.value
  if (!src?.id) return
  rulesLoading.value = true
  try {
    const r = await getAlertSourceRules(src.id, { page, page_size: rulesPageSize.value })
    rules.value = r.data.rules || []
    rulesTotal.value = r.data.total || 0
    rulesPage.value = page
  } catch (e: any) {
    ElMessage.error(e.message || '加载规则失败')
  } finally {
    rulesLoading.value = false
  }
}

function onRulesPage(p: number) {
  loadRules(p)
}

async function doDelete(row: ExternalAlertSource) {
  try {
    await ElMessageBox.confirm(
      `确定删除告警源「${row.name}」？其同步的规则将一并删除，历史告警记录保留。`,
      '删除告警源', { type: 'warning', cancelButtonText: '取消', confirmButtonText: '删除' }
    )
  } catch {
    return
  }
  try {
    await deleteAlertSource(row.id!)
    ElMessage.success('已删除')
    await fetchData()
  } catch (e: any) {
    ElMessage.error(e.message || '删除失败')
  }
}

onMounted(fetchData)
</script>

<style scoped>
.webhook-guide {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 14px;
  background: rgba(0, 212, 255, 0.05);
  border: 1px solid rgba(0, 212, 255, 0.15);
  border-radius: 8px;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.7;
}
.webhook-guide code {
  background: rgba(0, 0, 0, 0.06);
  border-radius: 4px;
  padding: 1px 6px;
  font-size: 12px;
  color: var(--text-primary);
}
.guide-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.guide-hint {
  font-size: 12px;
  color: var(--text-tertiary);
}
.form-tip {
  font-size: 11px;
  color: var(--text-tertiary);
  line-height: 1.5;
  margin-top: 4px;
}

.switch-group {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding-left: 8px;
}
.switch-row {
  display: flex;
  align-items: center;
  gap: 8px;
  line-height: 1;
}
.switch-label {
  font-size: 12px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.switch-label.off {
  color: var(--text-tertiary);
}

.sync-btn {
  font-family: inherit;
  font-weight: 500;
}

.dialog-switch-group {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 6px 0;
}
.dialog-switch-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.dialog-switch-label {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: -2px;
}
.dialog-switch-title {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.dialog-switch-desc {
  font-size: 11px;
  color: var(--text-tertiary);
  line-height: 1.4;
}
</style>
