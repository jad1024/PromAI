<template>
  <div class="pl">
    <div class="pl-hd">
      <div>
        <h2>告警驱动 AI 巡检</h2>
        <p>PromAI 核心链路：外部告警触发 → 规则匹配 → 冷却与预算 → 日志检索降噪 → AI 根因诊断 → 落库 → 报告推送</p>
      </div>
      <div class="pl-hd-r">
        <el-select v-model="days" size="small" style="width:120px" @change="load">
          <el-option :value="7" label="近 7 天" />
          <el-option :value="14" label="近 14 天" />
          <el-option :value="30" label="近 30 天" />
        </el-select>
        <el-button size="small" @click="load">⟳ 刷新</el-button>
      </div>
    </div>

    <div v-loading="loading">
      <!-- 链路主视图：7 环节流程图 -->
      <div class="pl-flow">
        <template v-for="(s, i) in overview?.stages || []" :key="s.key">
          <div class="pl-node" @click="go(s.link)">
            <div class="pl-node-idx">{{ i + 1 }}</div>
            <div class="pl-node-name">{{ s.name }}</div>
            <div class="pl-node-count">{{ s.count }}</div>
            <div class="pl-node-desc">{{ s.desc }}</div>
          </div>
          <div v-if="i < (overview?.stages?.length || 0) - 1" class="pl-arrow">→</div>
        </template>
      </div>

      <!-- 关键指标卡 -->
      <div class="pl-kpis">
        <div class="pl-kpi">
          <span class="pl-kpi-label">活跃告警</span>
          <b class="pl-kpi-val warn">{{ overview?.alert_firing ?? '-' }}</b>
        </div>
        <div class="pl-kpi">
          <span class="pl-kpi-label">AI 诊断成功</span>
          <b class="pl-kpi-val ok">{{ overview?.ai_success ?? '-' }}</b>
        </div>
        <div class="pl-kpi">
          <span class="pl-kpi-label">启用告警源</span>
          <b class="pl-kpi-val">{{ overview?.total_sources ?? '-' }}</b>
        </div>
        <div class="pl-kpi">
          <span class="pl-kpi-label">AI 巡检任务</span>
          <b class="pl-kpi-val">{{ overview?.ai_cron_jobs ?? '-' }}</b>
        </div>
      </div>

      <!-- 效果评估：AI 结论确认率 + 告警下降 -->
      <div class="pl-effect">
        <div class="pl-effect-hd">
          <h3>AI 结论效果评估</h3>
          <el-select v-model="effectDays" size="small" style="width:120px" @change="loadEffect">
            <el-option :value="7" label="近 7 天" />
            <el-option :value="30" label="近 30 天" />
            <el-option :value="90" label="近 90 天" />
          </el-select>
        </div>
        <div class="pl-effect-body">
          <div class="pl-gauge">
            <div class="pl-gauge-ring" :style="{ '--pct': effect.confirm_rate || 0 }">
              <div class="pl-gauge-inner">
                <b>{{ Math.round(effect.confirm_rate || 0) }}%</b>
                <span>结论有效确认率</span>
              </div>
            </div>
            <div class="pl-gauge-legend">
              <span><i class="dot ok"></i>有效 {{ effect.confirmed || 0 }}</span>
              <span><i class="dot bad"></i>无效 {{ effect.invalid || 0 }}</span>
              <span><i class="dot gray"></i>待确认 {{ effect.pending || 0 }}</span>
            </div>
          </div>
          <div class="pl-drop">
            <div class="pl-drop-label">平均告警下降</div>
            <div class="pl-drop-val">{{ (effect.avg_alert_drop || 0).toFixed(1) }}<small> 条/次</small></div>
            <div class="pl-drop-desc">AI 诊断后活跃告警数的平均减少量，基于人工反馈的 before/after 统计</div>
          </div>
          <div class="pl-feedback-form">
            <div class="pl-feedback-title">登记一条效果反馈</div>
            <el-form :model="fb" size="small" label-width="76px">
              <el-form-item label="结论来源">
                <el-select v-model="fb.ref_id" filterable clearable allow-create
                  placeholder="从下方真实记录中选择（可搜索/留空）" style="width:100%">
                  <el-option-group v-for="grp in groupedSources" :key="grp.label" :label="grp.label">
                    <el-option v-for="s in grp.items" :key="s.type + '|' + s.id" :value="s.id" :label="s.label">
                      <span>{{ s.label }}</span>
                      <span style="float:right;color:var(--text-tertiary);font-size:11px;">{{ s.created_at }}</span>
                    </el-option>
                  </el-option-group>
                </el-select>
                <div class="pl-fb-hint">from 巡检记录 / 实时告警 group_key / AI 诊断记录；也可手填或留空</div>
              </el-form-item>
              <el-form-item label="评价">
                <el-select v-model="fb.verdict" style="width:100%">
                  <el-option label="有效（结论准确）" value="confirmed" />
                  <el-option label="无效（结论偏差）" value="invalid" />
                  <el-option label="待确认" value="pending" />
                </el-select>
              </el-form-item>
              <el-form-item label="告警变化">
                <el-input-number v-model="fb.alerts_before" :min="0" controls-position="right" style="width:90px" />
                <span style="margin:0 6px">→</span>
                <el-input-number v-model="fb.alerts_after" :min="0" controls-position="right" style="width:90px" />
              </el-form-item>
              <el-form-item label="备注">
                <el-input v-model="fb.note" placeholder="可选" />
              </el-form-item>
              <el-form-item>
                <el-button type="primary" :loading="fbSaving" @click="saveFb">提交反馈</el-button>
              </el-form-item>
            </el-form>
          </div>
        </div>
      </div>

      <!-- 最近反馈 -->
      <div class="pl-recent" v-if="feedbacks.length">
        <h3>最近效果反馈</h3>
        <el-table :data="feedbacks" size="small" border>
          <el-table-column prop="created_at" label="时间" width="160">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column prop="ref_id" label="结论来源" min-width="160" />
          <el-table-column label="评价" width="100">
            <template #default="{ row }">
              <span :class="['pl-verdict', row.verdict]">{{ verdictLabel(row.verdict) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="alerts_before" label="告警前" width="80" align="center" />
          <el-table-column prop="alerts_after" label="告警后" width="80" align="center" />
          <el-table-column prop="note" label="备注" min-width="160" />
        </el-table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  getPipelineOverview, getAIFeedbacks, submitAIFeedback, getAIEffectiveness, getAIFeedbackSources,
  type PipelineOverview, type AIEffectiveness, type AIFeedback, type FeedbackSource,
} from '../api'

const router = useRouter()
const loading = ref(false)
const days = ref(7)
const effectDays = ref(30)
const overview = ref<PipelineOverview | null>(null)
const effect = ref<AIEffectiveness>({
  days: 30, confirmed: 0, invalid: 0, pending: 0, total: 0, confirm_rate: 0, avg_alert_drop: 0,
})
const feedbacks = ref<AIFeedback[]>([])
const feedbackSources = ref<FeedbackSource[]>([])
const fbSaving = ref(false)
const fb = reactive({ ref_id: '', verdict: 'confirmed', alerts_before: 0, alerts_after: 0, note: '' })

const SOURCE_LABELS: Record<string, string> = {
  inspection: '巡检记录（task_id）',
  alert: '实时告警（group_key）',
  diagnosis: 'AI 诊断记录',
}
const groupedSources = computed(() => {
  const groups: Record<string, FeedbackSource[]> = {}
  for (const s of feedbackSources.value) {
    const label = SOURCE_LABELS[s.type] || s.type
    if (!groups[label]) groups[label] = []
    groups[label].push(s)
  }
  return Object.keys(groups).map(label => ({ label, items: groups[label] }))
})

async function loadSources() {
  try {
    feedbackSources.value = (await getAIFeedbackSources()).data.items || []
  } catch { /* 来源列表非关键，失败不影响反馈提交 */ }
}

async function load() {
  loading.value = true
  try {
    overview.value = (await getPipelineOverview(days.value)).data
  } catch (e: any) {
    ElMessage.error('加载链路数据失败: ' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
}
async function loadEffect() {
  try {
    effect.value = (await getAIEffectiveness(effectDays.value)).data
    feedbacks.value = (await getAIFeedbacks()).data.items || []
  } catch (e: any) {
    ElMessage.error('加载效果评估失败: ' + (e?.response?.data?.error || e.message))
  }
}
async function saveFb() {
  fbSaving.value = true
  try {
    await submitAIFeedback({ ...fb, operator: localStorage.getItem('username') || 'Admin' })
    ElMessage.success('反馈已提交')
    fb.ref_id = ''; fb.note = ''; fb.alerts_before = 0; fb.alerts_after = 0
    await loadEffect()
  } catch (e: any) {
    ElMessage.error('提交失败: ' + (e?.response?.data?.error || e.message))
  } finally {
    fbSaving.value = false
  }
}
function verdictLabel(v: string) {
  return v === 'confirmed' ? '有效' : v === 'invalid' ? '无效' : '待确认'
}
function formatTime(s: string) {
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString('zh-CN', { hour12: false })
}
function go(link: string) {
  if (link) router.push(link)
}
onMounted(() => { load(); loadEffect(); loadSources() })
</script>

<style scoped>
.pl { padding: 16px 18px 40px; font-size: 12px; }
.pl-hd { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 18px; }
.pl-hd h2 { margin: 0 0 5px; font-size: 17px; }
.pl-hd p { margin: 0; font-size: 11.5px; color: var(--text-tertiary); max-width: 860px; line-height: 1.6; }
.pl-hd-r { display: flex; gap: 8px; flex-shrink: 0; }
.pl-flow { display: flex; align-items: stretch; gap: 4px; overflow-x: auto; padding: 6px 2px 14px; }
.pl-node { flex: 1 0 128px; min-width: 128px; background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 12px 10px; cursor: pointer; transition: .15s; position: relative; }
.pl-node:hover { border-color: var(--cyan); box-shadow: 0 2px 10px rgba(0,0,0,.06); transform: translateY(-2px); }
.pl-node-idx { position: absolute; top: 8px; right: 9px; width: 18px; height: 18px; border-radius: 50%; background: var(--cyan-dim); color: var(--cyan); font-size: 10px; display: flex; align-items: center; justify-content: center; font-weight: 700; }
.pl-node-name { font-size: 12.5px; font-weight: 600; color: var(--text-primary); margin-bottom: 6px; }
.pl-node-count { font-size: 22px; font-weight: 700; color: var(--cyan); line-height: 1.1; margin-bottom: 6px; }
.pl-node-desc { font-size: 10px; color: var(--text-tertiary); line-height: 1.5; min-height: 30px; }
.pl-arrow { align-self: center; color: var(--text-tertiary); font-size: 15px; flex: 0 0 auto; }
.pl-kpis { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin: 6px 0 20px; }
.pl-kpi { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 14px 16px; display: flex; flex-direction: column; gap: 6px; }
.pl-kpi-label { font-size: 11px; color: var(--text-tertiary); }
.pl-kpi-val { font-size: 24px; font-weight: 700; color: var(--text-primary); }
.pl-kpi-val.warn { color: var(--red); } .pl-kpi-val.ok { color: var(--emerald); }
.pl-effect { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 16px 18px; margin-bottom: 20px; }
.pl-effect-hd { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.pl-effect-hd h3 { margin: 0; font-size: 14px; }
.pl-effect-body { display: grid; grid-template-columns: 220px 1fr 340px; gap: 24px; align-items: center; }
.pl-gauge { display: flex; flex-direction: column; align-items: center; gap: 12px; }
.pl-gauge-ring { width: 130px; height: 130px; border-radius: 50%; background: conic-gradient(var(--cyan) calc(var(--pct) * 1%), var(--border) 0); display: flex; align-items: center; justify-content: center; }
.pl-gauge-inner { width: 104px; height: 104px; border-radius: 50%; background: #fff; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 3px; }
.pl-gauge-inner b { font-size: 26px; color: var(--cyan); }
.pl-gauge-inner span { font-size: 10px; color: var(--text-tertiary); }
.pl-gauge-legend { display: flex; gap: 12px; font-size: 10.5px; color: var(--text-secondary); }
.pl-gauge-legend .dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-right: 4px; }
.pl-gauge-legend .dot.ok { background: var(--emerald); } .pl-gauge-legend .dot.bad { background: var(--red); } .pl-gauge-legend .dot.gray { background: #b0b6bf; }
.pl-drop { border-left: 1px solid var(--border); padding-left: 24px; }
.pl-drop-label { font-size: 11px; color: var(--text-tertiary); margin-bottom: 8px; }
.pl-drop-val { font-size: 38px; font-weight: 700; color: var(--emerald); line-height: 1; margin-bottom: 10px; }
.pl-drop-val small { font-size: 12px; color: var(--text-tertiary); font-weight: 400; }
.pl-drop-desc { font-size: 10.5px; color: var(--text-tertiary); line-height: 1.6; max-width: 280px; }
.pl-feedback-form { border-left: 1px solid var(--border); padding-left: 24px; }
.pl-feedback-title { font-size: 12px; font-weight: 600; margin-bottom: 10px; }
.pl-fb-hint { font-size: 11px; color: var(--text-tertiary, #94a3b8); line-height: 1.4; margin-top: 4px; }
.pl-recent { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 16px 18px; }
.pl-recent h3 { margin: 0 0 12px; font-size: 14px; }
.pl-verdict { font-size: 10.5px; padding: 2px 7px; border-radius: 3px; }
.pl-verdict.confirmed { background: var(--emerald-dim); color: var(--emerald); }
.pl-verdict.invalid { background: var(--red-dim); color: var(--red); }
.pl-verdict.pending { background: #f0f1f3; color: #8f96a0; }
</style>
