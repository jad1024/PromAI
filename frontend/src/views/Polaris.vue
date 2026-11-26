<template>
  <div class="pl">
    <!-- ========== 业务线首页（卡片） ========== -->
    <template v-if="!activeBiz">
      <div class="pl-hd">
        <div>
          <h2>北极星 · 业务核心指标</h2>
          <p>梳理、检测、告警业务关键指标（订单 / 支付 / GMV），指标异常即代表真实故障。与灭火图分工：这里看业务是否受损，灭火图看 IT 侧哪里红了。</p>
        </div>
        <el-button size="small" type="primary" @click="openBizDialog()">+ 新建业务线</el-button>
      </div>
      <div v-loading="loading" class="pl-body">
        <div v-for="b in businesses" :key="b.id" class="pl-biz" @click="enterBiz(b.id)">
          <div class="pl-biz-top">
            <span class="pl-biz-icon" :style="{ background: b.color + '18', color: b.color }">{{ b.icon || '★' }}</span>
            <div class="pl-biz-title">
              <b>{{ b.name }}</b>
              <span>{{ b.description }}</span>
            </div>
            <span class="pl-biz-status" :class="b.status">{{ statusText(b.status) }}</span>
          </div>
          <div class="pl-biz-metrics">
            <div><span>图表</span><b>{{ b.chart_count }}</b></div>
            <div><span>告警策略</span><b>{{ b.alert_count }}</b></div>
            <div><span>业务健康度</span><b :class="healthClass(b.health)">{{ Math.round(b.health) }}%</b></div>
          </div>
          <div class="pl-biz-bar"><i :style="{ width: b.health + '%', background: scoreColor(b.health) }"></i></div>
          <div class="pl-biz-ft">
            <span>负责人 {{ b.owner || '—' }} · {{ b.team || '—' }}</span>
            <span class="pl-biz-go">进入 →</span>
          </div>
        </div>
        <div v-if="!businesses.length && !loading" class="pl-empty">
          暂无业务线。点右上角「新建业务线」，从指标池选取核心业务指标开始。
        </div>
      </div>
    </template>

    <!-- ========== 业务线详情 ========== -->
    <template v-else>
      <div class="pl-detail-hd">
        <el-button link @click="activeBiz = null">← 返回业务线</el-button>
        <span class="pl-detail-sep">/</span>
        <b>{{ detail?.business?.name }}</b>
        <el-tag size="small" :type="detail?.business?.status === 'critical' ? 'danger' : detail?.business?.status === 'warning' ? 'warning' : 'success'">
          {{ statusText(detail?.business?.status || 'normal') }}
        </el-tag>
        <span class="pl-spacer"></span>
        <el-button size="small" @click="openChartDialog()">+ 新增图表</el-button>
        <el-button size="small" @click="openDetectDialog()">+ 智能检测</el-button>
        <el-button size="small" @click="openPolicyDialog()">+ 告警策略</el-button>
      </div>

      <div v-if="detail && !detail.has_datasource" class="pl-alert">
        未检测到可用 Prometheus 数据源，图表暂无法出数。请先在「数据源管理」添加数据源。
      </div>

      <div v-loading="detailLoading" class="pl-detail">
        <!-- 图表墙 -->
        <div class="pl-section">
          <div class="pl-section-hd"><h3>核心指标图表</h3><span>绑定指标池中的 PromQL 指标，每分钟出数</span></div>
          <div class="pl-charts">
            <div v-for="c in detail?.charts || []" :key="c.id" class="pl-chart"
                 :class="{ 'is-abnormal': c.status === 'critical' }">
              <div class="pl-chart-hd">
                <b>{{ c.name }}</b>
                <span class="pl-chart-tag">{{ chartTypeName(c.chart_type) }}</span>
                <el-button link type="danger" size="small" class="pl-chart-del" @click="del('chart', c.id)">删除</el-button>
              </div>
              <div class="pl-chart-value">
                <span :class="c.status === 'critical' ? 'red' : ''">{{ fmtVal(c.last_value) }}</span>
                <small>{{ c.unit }}</small>
              </div>
              <svg class="pl-chart-spark" viewBox="0 0 300 70" preserveAspectRatio="none">
                <g v-for="(s, si) in c.series" :key="si">
                  <polyline v-if="(s.points || []).length > 1" :points="sparkPoints(s.points || [])" fill="none"
                            :stroke="c.status === 'critical' ? '#e8635a' : ['#7c3aed','#0f9d8a','#e0a026'][si % 3]" stroke-width="2" />
                </g>
                <text v-if="!(c.series || []).some(s => (s.points || []).length > 1)" x="150" y="40" text-anchor="middle" fill="#b0b6bf" font-size="11">暂无数据</text>
              </svg>
              <div class="pl-chart-legend">
                <span v-for="(n, i) in ((c.metric_names || []).length ? c.metric_names : (c.series || []).map(s => s.name))" :key="i">
                  <i :style="{ background: ['#7c3aed','#0f9d8a','#e0a026'][i % 3] }"></i>{{ n }}
                </span>
              </div>
            </div>
            <div v-if="!detail?.charts?.length" class="pl-empty-sm">暂无图表，点右上「新增图表」绑定指标</div>
          </div>
        </div>

        <!-- 智能检测 + 告警策略 两栏 -->
        <div class="pl-cols">
          <div class="pl-section">
            <div class="pl-section-hd"><h3>智能检测</h3><span>检测异常产出事件</span></div>
            <el-table :data="detail?.detections || []" size="small" border>
              <el-table-column label="状态" width="80">
                <template #default="{ row }"><span class="pl-dot" :class="row.triggered ? 'red' : 'green'"></span>{{ row.triggered ? '异常' : '正常' }}</template>
              </el-table-column>
              <el-table-column label="检测方式" width="100">
                <template #default="{ row }">{{ detectTypeName(row.detect_type) }}</template>
              </el-table-column>
              <el-table-column label="判定" min-width="140">
                <template #default="{ row }"><span :class="{ red: row.triggered }">{{ row.reason }}</span></template>
              </el-table-column>
              <el-table-column label="操作" width="60">
                <template #default="{ row }"><el-button link type="danger" size="small" @click="del('detection', row.id)">删</el-button></template>
              </el-table-column>
            </el-table>
            <div v-if="!detail?.detections?.length" class="pl-empty-sm">暂无检测配置</div>
          </div>

          <div class="pl-section">
            <div class="pl-section-hd"><h3>告警策略</h3><span>决定通知谁、什么等级</span></div>
            <el-table :data="detail?.policies || []" size="small" border>
              <el-table-column prop="name" label="策略" min-width="120" />
              <el-table-column label="等级" width="80">
                <template #default="{ row }"><el-tag size="small" :type="row.severity === 'critical' ? 'danger' : 'warning'">{{ row.severity === 'critical' ? '严重' : '警告' }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="notify_target" label="接收组" min-width="100" />
              <el-table-column label="操作" width="60">
                <template #default="{ row }"><el-button link type="danger" size="small" @click="del('policy', row.id)">删</el-button></template>
              </el-table-column>
            </el-table>
            <div v-if="!detail?.policies?.length" class="pl-empty-sm">暂无告警策略</div>
          </div>
        </div>

        <!-- SLO -->
        <div class="pl-section">
          <div class="pl-section-hd"><h3>业务 SLO · 错误预算</h3><span>基于业务 SLI 的可用性配额（命中灭火图节点健康分）</span></div>
          <div class="pl-slos">
            <div v-for="s in detail?.slos || []" :key="s.id" class="pl-slo">
              <div class="pl-slo-hd"><b>{{ s.name }}</b><span>目标 {{ s.target }}% · {{ s.window_days }} 天</span></div>
              <div class="pl-slo-bar"><i :style="{ width: s.error_budget + '%', background: scoreColor(s.error_budget) }"></i></div>
              <div class="pl-slo-ft">当前 {{ Math.round(s.current) }} 分 · 剩余预算 <b>{{ s.error_budget }}%</b> · {{ statusText(s.status) }}</div>
            </div>
            <div v-if="!detail?.slos?.length" class="pl-empty-sm">暂无 SLO 定义</div>
          </div>
        </div>
      </div>
    </template>

    <!-- 业务线弹窗 -->
    <el-dialog v-model="bizDialog" :title="bizForm.id ? '编辑业务线' : '新建业务线'" width="520px">
      <el-form label-width="90px" size="small">
        <el-form-item label="名称"><el-input v-model="bizForm.name" placeholder="如：交易业务线" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="bizForm.description" /></el-form-item>
        <el-form-item label="负责人"><el-input v-model="bizForm.owner" /></el-form-item>
        <el-form-item label="团队"><el-input v-model="bizForm.team" /></el-form-item>
        <el-form-item label="图标"><el-input v-model="bizForm.icon" placeholder="一个汉字，如：易" style="width:100px" /></el-form-item>
        <el-form-item label="数据源">
          <el-select v-model="bizForm.datasource_id" clearable placeholder="默认数据源" style="width:100%">
            <el-option v-for="d in datasources" :key="d.id" :label="d.name" :value="d.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer><el-button size="small" @click="bizDialog = false">取消</el-button><el-button size="small" type="primary" @click="saveBiz">保存</el-button></template>
    </el-dialog>

    <!-- 图表弹窗 -->
    <el-dialog v-model="chartDialog" title="新增图表" width="560px">
      <el-form label-width="90px" size="small">
        <el-form-item label="图表名称"><el-input v-model="chartForm.name" /></el-form-item>
        <el-form-item label="类型">
          <el-select v-model="chartForm.chart_type" style="width:160px">
            <el-option label="折线图" value="line" /><el-option label="柱状图" value="bar" />
            <el-option label="数字卡" value="number" /><el-option label="仪表盘" value="gauge" />
          </el-select>
        </el-form-item>
        <el-form-item label="指标池">
          <el-select v-model="chartForm.metric_ids" multiple filterable style="width:100%" placeholder="从现有指标选择（复用 PromQL）">
            <el-option v-for="m in metricPool" :key="m.id" :label="m.name + ' — ' + m.query" :value="m.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="单位"><el-input v-model="chartForm.unit" style="width:120px" placeholder="如 笔 / % / ms" /></el-form-item>
        <el-form-item label="自定义 PromQL">
          <el-input v-model="chartForm.custom_promql" placeholder="不选指标时可直接写 PromQL" />
        </el-form-item>
      </el-form>
      <template #footer><el-button size="small" @click="chartDialog = false">取消</el-button><el-button size="small" type="primary" @click="saveChart">保存</el-button></template>
    </el-dialog>

    <!-- 检测弹窗 -->
    <el-dialog v-model="detectDialog" title="配置智能检测" width="520px">
      <el-form label-width="90px" size="small">
        <el-form-item label="图表">
          <el-select v-model="detectForm.chart_id" style="width:100%">
            <el-option v-for="c in detail?.charts || []" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="检测方式">
          <el-select v-model="detectForm.detect_type" style="width:160px">
            <el-option label="智能预测" value="predict" /><el-option label="同环比" value="mom" />
            <el-option label="阈值" value="threshold" /><el-option label="数据中断" value="nodata" />
          </el-select>
        </el-form-item>
        <template v-if="detectForm.detect_type === 'threshold'">
          <el-form-item label="比较">
            <el-select v-model="detectForm.compare_op" style="width:120px"><el-option label="小于 <" value="lt" /><el-option label="大于 >" value="gt" /></el-select>
            <el-input-number v-model="detectForm.threshold" :precision="2" style="margin-left:8px" />
          </el-form-item>
        </template>
        <template v-if="detectForm.detect_type === 'predict' || detectForm.detect_type === 'mom'">
          <el-form-item label="敏感度"><el-input-number v-model="detectForm.sensitivity" :min="1" :max="10" :step="0.5" /></el-form-item>
        </template>
        <el-form-item label="持续分钟"><el-input-number v-model="detectForm.duration_min" :min="1" :max="60" /></el-form-item>
      </el-form>
      <template #footer><el-button size="small" @click="detectDialog = false">取消</el-button><el-button size="small" type="primary" @click="saveDetect">保存</el-button></template>
    </el-dialog>

    <!-- 策略弹窗 -->
    <el-dialog v-model="policyDialog" title="配置告警策略" width="520px">
      <el-form label-width="90px" size="small">
        <el-form-item label="策略名称"><el-input v-model="policyForm.name" /></el-form-item>
        <el-form-item label="等级">
          <el-select v-model="policyForm.severity" style="width:140px"><el-option label="警告" value="warning" /><el-option label="严重" value="critical" /></el-select>
        </el-form-item>
        <el-form-item label="接收组"><el-input v-model="policyForm.notify_target" placeholder="如：交易值班群 / sre@corp.com" /></el-form-item>
        <el-form-item label="下钻灭火图">
          <el-select v-model="policyForm.firemap_node_id" clearable filterable style="width:100%" placeholder="告警后一键跳到该对象（可选观测实例）">
            <el-option v-for="n in drillNodeOptions" :key="n.id" :label="n.label" :value="n.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer><el-button size="small" @click="policyDialog = false">取消</el-button><el-button size="small" type="primary" @click="savePolicy">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getFiremapNodes, type FCNode } from '../api'
import {
  getPolarisBusinesses, getPolarisBusiness, getPolarisMetricPool,
  polarisCRUD, polarisDelete,
  type PBusinessView, type PBusinessDetail, type PMetricPoolItem, type PMetricPoint,
} from '../api'

const loading = ref(false)
const detailLoading = ref(false)
const businesses = ref<PBusinessView[]>([])
const detail = ref<PBusinessDetail | null>(null)
const activeBiz = ref<number | null>(null)
const metricPool = ref<PMetricPoolItem[]>([])
const datasources = ref<{ id: number; name: string }[]>([])
const firemapNodes = ref<FCNode[]>([])

// 下钻目标只列可观测的实例叶子节点，并显示「数据源 / 父服务 / 实例」完整路径避免同名混淆。
const drillNodeOptions = computed(() => {
  const byId = new Map<number, FCNode>()
  firemapNodes.value.forEach(n => { if (n.id != null) byId.set(n.id, n) })
  const labelOf = (n: FCNode): string => {
    const segs: string[] = []
    if (n.datasource && n.datasource !== '—') segs.push(n.datasource)
    if (n.parent_name && n.parent_name !== '（根节点）') segs.push(n.parent_name)
    segs.push(n.name)
    return segs.join(' / ')
  }
  const leaves = firemapNodes.value.filter(n => n.level === 'instance')
  const source = leaves.length ? leaves : firemapNodes.value
  return source.map(n => ({ id: n.id!, label: labelOf(n) }))
})

// 弹窗
const bizDialog = ref(false)
const chartDialog = ref(false)
const detectDialog = ref(false)
const policyDialog = ref(false)
const bizForm = reactive<any>({ id: 0, name: '', description: '', owner: '', team: '', icon: '', datasource_id: null })
const chartForm = reactive<any>({ name: '', chart_type: 'line', metric_ids: [], custom_promql: '', unit: '' })
const detectForm = reactive<any>({ chart_id: 0, detect_type: 'threshold', compare_op: 'lt', threshold: 0, sensitivity: 3, duration_min: 3 })
const policyForm = reactive<any>({ name: '', severity: 'warning', notify_target: '', firemap_node_id: null })

function scoreColor(s: number) { return s >= 95 ? '#3fb37f' : s >= 70 ? '#e0a026' : '#e8635a' }
function healthClass(s: number) { return s >= 95 ? 'green' : s >= 70 ? 'amber' : 'red' }
function statusText(s: string) { return s === 'critical' ? '异常' : s === 'warning' ? '需关注' : '健康' }
function chartTypeName(t: string) { return { line: '折线', bar: '柱状', number: '数字', gauge: '仪表' }[t] || t }
function detectTypeName(t: string) { return { predict: '智能预测', mom: '同环比', threshold: '阈值', nodata: '数据中断' }[t] || t }
function fmtVal(v: number) { return v == null ? '—' : (Math.abs(v) >= 100 ? Math.round(v) : Math.round(v * 100) / 100).toLocaleString() }
function sparkPoints(pts: PMetricPoint[]) {
  if (pts.length < 2) return ''
  const vals = pts.map(p => p.v)
  const min = Math.min(...vals), max = Math.max(...vals)
  const span = max - min || 1
  return pts.map((p, i) => {
    const x = (i / (pts.length - 1)) * 298 + 1
    const y = 66 - ((p.v - min) / span) * 58
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
}

async function load() {
  loading.value = true
  try {
    businesses.value = (await getPolarisBusinesses()).data.items || []
  } catch (e: any) { ElMessage.error('加载北极星失败: ' + (e?.message || e)) }
  finally { loading.value = false }
}
async function loadDetail(id: number) {
  detailLoading.value = true
  try { detail.value = (await getPolarisBusiness(id)).data }
  catch (e: any) { ElMessage.error('加载业务线失败: ' + (e?.message || e)) }
  finally { detailLoading.value = false }
}
async function enterBiz(id: number) {
  activeBiz.value = id
  await loadDetail(id)
}
async function loadMeta() {
  try {
    metricPool.value = (await getPolarisMetricPool()).data.items || []
  } catch { /* ignore */ }
  try {
    const r = await getFiremapNodes()
    datasources.value = r.data.datasources || []
    firemapNodes.value = r.data.nodes || []
  } catch { /* ignore */ }
}

function openBizDialog() {
  Object.assign(bizForm, { id: 0, name: '', description: '', owner: '', team: '', icon: '', datasource_id: null })
  bizDialog.value = true
}
async function saveBiz() {
  if (!bizForm.name.trim()) { ElMessage.warning('请填写名称'); return }
  await polarisCRUD('business', { ...bizForm })
  bizDialog.value = false
  ElMessage.success('已保存')
  await load()
}
function openChartDialog() {
  Object.assign(chartForm, { name: '', chart_type: 'line', metric_ids: [], custom_promql: '', unit: '' })
  chartDialog.value = true
}
async function saveChart() {
  if (!chartForm.name.trim()) { ElMessage.warning('请填写图表名称'); return }
  await polarisCRUD('chart', { ...chartForm, business_id: activeBiz.value })
  chartDialog.value = false
  ElMessage.success('图表已添加')
  await loadDetail(activeBiz.value!)
}
function openDetectDialog() {
  const first = detail.value?.charts?.[0]
  Object.assign(detectForm, { chart_id: first?.id || 0, detect_type: 'threshold', compare_op: 'lt', threshold: 0, sensitivity: 3, duration_min: 3 })
  detectDialog.value = true
}
async function saveDetect() {
  if (!detectForm.chart_id) { ElMessage.warning('请先添加图表'); return }
  const chart = detail.value?.charts?.find(c => c.id === detectForm.chart_id)
  await polarisCRUD('detection', { ...detectForm, business_id: activeBiz.value, metric_id: chart?.metric_ids?.[0] || 0 })
  detectDialog.value = false
  ElMessage.success('检测已配置')
  await loadDetail(activeBiz.value!)
}
function openPolicyDialog() {
  Object.assign(policyForm, { name: '', severity: 'warning', notify_target: '', firemap_node_id: null })
  policyDialog.value = true
}
async function savePolicy() {
  if (!policyForm.name.trim()) { ElMessage.warning('请填写策略名称'); return }
  await polarisCRUD('policy', { ...policyForm, business_id: activeBiz.value })
  policyDialog.value = false
  ElMessage.success('策略已配置')
  await loadDetail(activeBiz.value!)
}
async function del(kind: string, id: number) {
  await ElMessageBox.confirm('确认删除？', '提示', { type: 'warning' })
  await polarisDelete(kind, id)
  ElMessage.success('已删除')
  if (activeBiz.value) await loadDetail(activeBiz.value)
  else await load()
}
onMounted(async () => { await Promise.all([load(), loadMeta()]) })
</script>

<style scoped>
.pl { padding: 16px 18px 40px; font-size: 12px; }
.pl-hd { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px; gap: 16px; }
.pl-hd h2 { margin: 0 0 4px; font-size: 16px; color: var(--text-primary); }
.pl-hd p { margin: 0; font-size: 11.5px; color: var(--text-tertiary); max-width: 820px; line-height: 1.6; }
.pl-body { display: grid; grid-template-columns: repeat(auto-fill, minmax(330px, 1fr)); gap: 14px; }
.pl-biz { background: #fff; border: 1px solid var(--border); border-radius: 10px; padding: 16px; cursor: pointer; transition: .15s; }
.pl-biz:hover { border-color: var(--cyan); box-shadow: 0 4px 16px rgba(31,35,41,.08); transform: translateY(-1px); }
.pl-biz-top { display: flex; align-items: center; gap: 10px; }
.pl-biz-icon { width: 38px; height: 38px; border-radius: 9px; display: flex; align-items: center; justify-content: center; font-size: 16px; font-weight: 600; flex-shrink: 0; }
.pl-biz-title { display: flex; flex-direction: column; gap: 3px; min-width: 0; flex: 1; }
.pl-biz-title b { font-size: 14px; }
.pl-biz-title span { font-size: 10.5px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pl-biz-status { font-size: 10px; padding: 2px 8px; border-radius: 10px; }
.pl-biz-status.normal { background: var(--emerald-dim); color: var(--emerald); }
.pl-biz-status.warning { background: var(--amber-dim); color: var(--amber); }
.pl-biz-status.critical { background: var(--red-dim); color: var(--red); }
.pl-biz-metrics { display: flex; gap: 22px; margin: 16px 0 10px; }
.pl-biz-metrics div { display: flex; flex-direction: column; gap: 4px; }
.pl-biz-metrics span { font-size: 10px; color: var(--text-tertiary); }
.pl-biz-metrics b { font-size: 17px; }
.pl-biz-bar { height: 7px; background: #eef0f2; border-radius: 4px; overflow: hidden; }
.pl-biz-bar i { display: block; height: 100%; border-radius: 4px; }
.pl-biz-ft { display: flex; justify-content: space-between; margin-top: 11px; font-size: 10px; color: var(--text-tertiary); }
.pl-biz-go { color: var(--cyan); }
.pl-empty { grid-column: 1/-1; background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 40px; text-align: center; color: var(--text-tertiary); }
.pl-empty-sm { color: var(--text-tertiary); font-size: 11px; padding: 18px; text-align: center; }

.pl-detail-hd { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.pl-detail-hd b { font-size: 15px; }
.pl-detail-sep { color: var(--text-tertiary); }
.pl-spacer { flex: 1; }
.pl-alert { background: var(--amber-dim); color: var(--amber); padding: 8px 12px; border-radius: 6px; margin-bottom: 14px; font-size: 11.5px; }
.pl-section { background: #fff; border: 1px solid var(--border); border-radius: 10px; padding: 14px; margin-bottom: 14px; }
.pl-section-hd { display: flex; align-items: baseline; gap: 12px; margin-bottom: 12px; }
.pl-section-hd h3 { margin: 0; font-size: 13px; }
.pl-section-hd span { font-size: 10.5px; color: var(--text-tertiary); }
.pl-charts { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 12px; }
.pl-chart { border: 1px solid var(--border); border-radius: 8px; padding: 12px; }
.pl-chart.is-abnormal { border-color: #e8635a; background: #fffaf9; }
.pl-chart-hd { display: flex; align-items: center; gap: 6px; }
.pl-chart-hd b { font-size: 12px; }
.pl-chart-tag { font-size: 9.5px; color: var(--text-tertiary); background: #f7f7fa; padding: 1px 5px; border-radius: 3px; }
.pl-chart-del { margin-left: auto; }
.pl-chart-value { margin: 8px 0 2px; }
.pl-chart-value span { font-size: 22px; font-weight: 600; }
.pl-chart-value small { font-size: 10px; color: var(--text-tertiary); margin-left: 4px; }
.pl-chart-spark { width: 100%; height: 60px; }
.pl-chart-legend { display: flex; gap: 12px; font-size: 10px; color: var(--text-tertiary); flex-wrap: wrap; }
.pl-chart-legend span { display: inline-flex; align-items: center; gap: 4px; }
.pl-chart-legend i { width: 8px; height: 3px; border-radius: 2px; }
.pl-cols { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.pl-slos { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 12px; }
.pl-slo { border: 1px solid var(--border); border-radius: 8px; padding: 12px; }
.pl-slo-hd { display: flex; justify-content: space-between; font-size: 11.5px; }
.pl-slo-hd span { color: var(--text-tertiary); font-size: 10px; }
.pl-slo-bar { height: 7px; background: #eef0f2; border-radius: 4px; overflow: hidden; margin: 10px 0 6px; }
.pl-slo-bar i { display: block; height: 100%; border-radius: 4px; }
.pl-slo-ft { font-size: 10.5px; color: var(--text-secondary); }
.pl-dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-right: 5px; }
.pl-dot.red, .red { background: #e8635a; }
.pl-dot.green { background: #3fb37f; }
.green { color: #3fb37f; } .amber { color: #e0a026; }
</style>
