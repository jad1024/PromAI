<template>
  <div class="fm">
    <!-- 工具条（顶栏由 App.vue 提供，此处不再套第二层） -->
    <div class="fm-toolbar">
      <div class="fm-vseg" v-if="tab === ''">
        <button :class="{ on: viewMode === 'cards' }" title="卡片模式" @click="viewMode = 'cards'">▦</button>
        <button :class="{ on: viewMode === 'tree' }" title="树形透视" @click="viewMode = 'tree'">⑂</button>
      </div>
      <div class="fm-fseg" v-if="tab === ''">
        <button :class="{ on: statusFilter === 'all' }" @click="statusFilter = 'all'">全部 <b>{{ data.stats.total }}</b></button>
        <button :class="{ on: statusFilter === 'abnormal' }" @click="statusFilter = 'abnormal'">异常 <b class="red">{{ data.stats.critical + data.stats.warning }}</b></button>
        <button :class="{ on: statusFilter === 'normal' }" @click="statusFilter = 'normal'">正常 <b class="green">{{ data.stats.normal }}</b></button>
        <button :class="{ on: statusFilter === 'nodata' }" @click="statusFilter = 'nodata'">无数据 <b>{{ data.stats.no_data }}</b></button>
      </div>
      <button class="fm-tb-i" title="刷新" @click="loadObjects">⟳</button>
      <button class="fm-tb-i" :class="{ on: timelineOpen }" title="时间轴回放" @click="toggleTimeline">◷</button>
      <span class="fm-spacer"></span>
      <span class="fm-overall" v-if="data.has_data">
        整体健康分 <b :class="healthClass(data.overall_health)">{{ data.overall_health }}</b>
      </span>
      <span class="fm-time" v-if="data.last_report_at">最近巡检 {{ formatTime(data.last_report_at) }}</span>
      <el-select v-model="dsFilter" size="small" clearable placeholder="全部空间" style="width: 150px" @change="loadObjects">
        <el-option v-for="ds in data.datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
      </el-select>
      <button class="fm-ai-top" @click="openDrawer(firstObjectNodeId)">✦ PromAI</button>
    </div>

    <!-- 时间轴回放：历史每分钟健康状态 -->
    <div v-if="timelineOpen" class="fm-timeline">
      <div class="fm-tl-hd">
        <b>◷ 时间轴回放</b>
        <span>回溯任意时刻的全局健康状态，与故障/发布事件对照</span>
        <el-select v-model="tlMinutes" size="small" style="width:100px" @change="loadTimeline">
          <el-option :value="30" label="最近 30 分钟" /><el-option :value="60" label="最近 1 小时" />
          <el-option :value="360" label="最近 6 小时" /><el-option :value="1440" label="最近 24 小时" />
        </el-select>
        <button class="fm-tb-i" @click="timelineOpen = false">✕</button>
      </div>
      <div class="fm-tl-chart" v-loading="tlLoading">
        <svg viewBox="0 0 1000 90" preserveAspectRatio="none" @mousemove="onTimelineHover">
          <line v-for="g in [0,1,2]" :key="g" x1="0" :y1="g*30+10" x2="1000" :y2="g*30+10" stroke="#eef0f2" stroke-width="1" />
          <polyline v-if="tlPoints.length > 1" :points="timelinePoints" fill="none" stroke="#7c3aed" stroke-width="2" />
          <rect v-for="(p, i) in tlEventMarks" :key="'e'+i" :x="p.x" y="8" width="2" height="80" fill="#e8635a" opacity="0.35" />
          <line v-if="tlCursorX >= 0" :x1="tlCursorX" y1="8" :x2="tlCursorX" y2="90" stroke="#7c3aed" stroke-dasharray="3 3" stroke-width="1" />
        </svg>
        <div class="fm-tl-axis"><span>{{ tlTimeLabel(tlPoints[0]?.t) }}</span><span>现在</span></div>
        <div v-if="tlHover" class="fm-tl-tip" :style="{ left: tlCursorX / 10 + '%' }">
          {{ tlTimeLabel(tlHover.t) }} · 健康分 <b :class="healthClass(tlHover.score)">{{ Math.round(tlHover.score) }}</b> · 告警 {{ tlHover.alerts }}
        </div>
      </div>
      <div v-if="!tlPoints.length && !tlLoading" class="fm-tl-empty">暂无历史快照。每执行一次定向检查会记录一条健康分，多跑几次即可回放。</div>
    </div>

    <div class="fm-body" v-loading="loading">
      <!-- 空数据引导 -->
      <div v-if="!data.has_data && !loading" class="fm-empty">
        <div class="fm-empty-icon">◉</div>
        <h3>服务树里还没有观测对象</h3>
        <p>灭火图的观测对象来自<strong>可编辑的透视服务树</strong>（业务 → 服务 → 实例），不是按巡检 label 临时分组。</p>
        <p class="fm-hint">可先在「数据源管理」触发一次巡检，再点下面按钮自动发现实例；也可以到「卡片池」手工添加节点。</p>
        <div class="fm-empty-btns">
          <el-button type="primary" size="small" @click="doSeed">从巡检数据自动发现</el-button>
          <el-button size="small" @click="goTab('pool')">去卡片池添加</el-button>
          <el-button size="small" @click="$router.push('/inspection')">触发巡检</el-button>
        </div>
        <p v-if="data.message" class="fm-msg">{{ data.message }}</p>
      </div>

      <!-- 灭火图：卡片模式 -->
      <template v-else-if="tab === '' && viewMode === 'cards'">
        <div v-for="layer in filteredLayers" :key="layer.name" class="fm-layer">
          <div class="fm-layer-hd">
            <span class="fm-caret">▼</span>{{ layer.name }}: <span class="fm-cnt">{{ layer.count }}</span>
            <span class="fm-layer-score">均分 {{ layer.avg_score }}</span>
          </div>
          <div class="fm-cards">
            <div v-for="obj in objectsOfLayer(layer.name)" :key="obj.id" class="fm-card" @click="openDrawer(obj.node_id)">
              <div class="fm-card-top">
                <span class="fm-card-nm">{{ obj.name }}</span>
                <span class="fm-card-src">{{ obj.layer }}</span>
                <span class="fm-card-mk">↗</span>
              </div>
              <div class="fm-card-path">{{ obj.path }}</div>
              <div class="fm-card-mid">
                <div class="fm-ring" :style="{ '--p': obj.health_score, '--rc': scoreColor(obj.health_score) }">
                  <div class="fm-ring-in">
                    <span class="fm-ring-sc">{{ Math.round(obj.health_score) }}分</span>
                    <span class="fm-ring-ic">{{ layerIcon(obj.layer) }}</span>
                  </div>
                </div>
                <div class="fm-card-stat">
                  检查 <b>{{ obj.total_checks }}</b> · 告警 <b :class="{ red: obj.alerts > 0 }">{{ obj.alerts }}</b>
                  <br />异常 <b :class="{ red: obj.critical > 0 }">{{ obj.critical }}</b> / 警告 <b :class="{ amber: obj.warning > 0 }">{{ obj.warning }}</b>
                  <br />
                  <span class="fm-dots">
                    <i v-if="obj.critical > 0" class="d-red"></i>
                    <i v-if="obj.warning > 0" class="d-amber"></i>
                    <i v-if="obj.normal > 0" class="d-green"></i>
                  </span>
                  <span v-if="obj.no_data" class="fm-nodata">尚未执行检查</span>
                  <span v-else>{{ obj.metrics }} 项指标</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>

      <!-- 灭火图：树形透视（异常向上透传） -->
      <div v-else-if="tab === ''" class="fm-tree-wrap">
        <div class="fm-tree-hint">⌄ 透视模式：业务 → 服务 → 实例。下层异常会向上透传（父节点取最严重状态），点叶子对象打开 PromAI。</div>
        <div class="fm-tree">
          <div v-for="row in treeRows" :key="row.node.id" class="ft-row"
               :style="{ paddingLeft: row.depth * 20 + 10 + 'px' }"
               :class="{ leaf: !!row.node.object_id }" @click="row.node.object_id && openDrawer(row.node.object_id)">
            <span class="ft-caret">{{ row.node.children?.length ? '▼' : '·' }}</span>
            <span class="ft-dot" :class="row.node.status"></span>
            <span class="ft-name">{{ row.node.name }}</span>
            <span class="ft-lv">{{ levelText(row.node.level) }}</span>
            <span class="ft-cnt">{{ row.node.count }}</span>
            <span class="ft-score" :class="healthClass(row.node.health_score)">{{ Math.round(row.node.health_score) }}分</span>
            <span class="ft-go" v-if="row.node.object_id">↗</span>
          </div>
        </div>
      </div>

      <!-- 卡片池：服务树节点 + 检查项配置 -->
      <div v-else-if="tab === 'pool'" class="fm-pane">
        <div class="fm-pane-hd">
          <h3>卡片池 · 透视服务树</h3>
          <div>
            <el-button size="small" type="primary" @click="doSeed">从巡检数据自动发现</el-button>
            <el-button size="small" @click="loadNodes">刷新</el-button>
          </div>
        </div>
        <p class="fm-pane-desc">节点持久化在 <code>firemap_nodes</code>，可重命名 / 调整层级 / 删除。健康分与异常透传都基于这棵树计算。</p>
        <el-table :data="nodes" size="small" border style="width:100%" row-key="id">
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column prop="name" label="名称" min-width="160">
            <template #default="{ row }">
              <span :style="{ paddingLeft: (row.level === 'instance' ? 32 : row.level === 'service' ? 16 : 0) + 'px' }">
                {{ row.level === 'instance' ? '└ ' : row.level === 'service' ? '├ ' : '' }}{{ row.name }}
              </span>
            </template>
          </el-table-column>
          <el-table-column prop="level" label="层级" width="90">
            <template #default="{ row }"><el-tag size="small" :type="levelTag(row.level)">{{ levelText(row.level) }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="parent_name" label="父节点" min-width="140" />
          <el-table-column prop="datasource" label="数据源" min-width="140" />
          <el-table-column prop="instance" label="实例" min-width="160" />
          <el-table-column label="操作" width="80">
            <template #default="{ row }">
              <el-button link type="danger" size="small" @click="removeNode(row.id)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="fm-form">
          <span class="fm-form-t">新增节点</span>
          <el-input v-model="newNode.name" size="small" placeholder="名称" style="width:150px" />
          <el-select v-model="newNode.level" size="small" style="width:100px" @change="newNode.parent_id = 0">
            <el-option label="业务" value="business" /><el-option label="服务" value="service" /><el-option label="实例" value="instance" />
          </el-select>
          <el-select v-model="newNode.parent_id" size="small" filterable placeholder="父节点" style="width:160px">
            <el-option v-if="newNode.level !== 'business'" :value="0" label="（根节点，不适用）" disabled />
            <el-option v-for="n in parentOptions" :key="n.id" :value="n.id" :label="n.name" />
          </el-select>
          <el-select v-model="newNode.datasource_id" size="small" filterable placeholder="数据源" style="width:150px">
            <el-option v-for="d in dsOptions" :key="d.id" :value="d.id" :label="d.name" />
          </el-select>
          <el-input v-if="newNode.level === 'instance'" v-model="newNode.instance" size="small" placeholder="instance 值" style="width:150px" />
          <el-button size="small" type="primary" @click="addNode">添加</el-button>
        </div>

        <div class="fm-pane-hd" style="margin-top:22px">
          <h3>PromAI 检查项配置</h3>
          <el-button size="small" @click="loadConfigs">刷新</el-button>
        </div>
        <p class="fm-pane-desc">每条检查项 = 一个真实诊断动作（PromQL 实时查询 / firing 告警 / 巡检快照 / SLO 预算 / AI 诊断）。</p>
        <el-table :data="configs" size="small" border style="width:100%">
          <el-table-column prop="key" label="Key" width="150" />
          <el-table-column prop="name" label="名称" min-width="130" />
          <el-table-column prop="check_type" label="动作类型" width="110" />
          <el-table-column prop="promql" label="PromQL" min-width="160" />
          <el-table-column prop="enabled" label="启用" width="80">
            <template #default="{ row }">
              <el-switch v-model="row.enabled" size="small" @change="saveConfig(row)" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="90">
            <template #default="{ row }">
              <el-button link type="danger" size="small" @click="removeConfig(row.id)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="fm-form">
          <span class="fm-form-t">新增检查项</span>
          <el-input v-model="newCfg.key" size="small" placeholder="key" style="width:130px" />
          <el-input v-model="newCfg.name" size="small" placeholder="名称" style="width:140px" />
          <el-select v-model="newCfg.check_type" size="small" style="width:120px">
            <el-option label="PromQL" value="promql" /><el-option label="告警" value="alert" />
            <el-option label="巡检快照" value="snapshot" /><el-option label="SLO" value="slo" /><el-option label="AI" value="ai" />
          </el-select>
          <el-input v-model="newCfg.promql" size="small" placeholder="PromQL（可留空）" style="width:200px" />
          <el-button size="small" type="primary" @click="addConfig">添加</el-button>
        </div>

        <div class="fm-pane-hd" style="margin-top:22px">
          <h3>观测规则模板 · 一键建卡</h3>
          <el-select v-model="tplDs" size="small" clearable filterable placeholder="选数据源评估匹配度" style="width:200px" @change="loadTemplates">
            <el-option v-for="d in dsOptions" :key="d.id" :label="d.name" :value="d.id" />
          </el-select>
        </div>
        <p class="fm-pane-desc">按 PromAI 模板优先逻辑：选数据源后自动评估匹配度，一键生成该类型的检查项（无需从零写 PromQL）。</p>
        <div class="fm-tpls">
          <div v-for="t in templates" :key="t.id" class="fm-tpl">
            <div class="fm-tpl-hd">
              <b>{{ t.name }}</b>
              <span class="fm-tpl-match" :class="t.match_score >= 100 ? 'hi' : t.match_score > 0 ? 'mid' : ''">{{ t.match_score ? t.match_score + '% 匹配' : '未选数据源' }}</span>
            </div>
            <div class="fm-tpl-prefix">{{ t.metric_prefixes }}</div>
            <el-button size="small" :type="t.match_score > 0 ? 'primary' : ''" @click="applyTemplate(t)">应用模板</el-button>
          </div>
        </div>

        <div class="fm-pane-hd" style="margin-top:22px">
          <h3>告警规则 · 飘红触发告警</h3>
          <el-button size="small" @click="openAlertRuleDialog">+ 新增告警规则</el-button>
        </div>
        <el-table :data="alertRules" size="small" border style="width:100%">
          <el-table-column prop="name" label="规则" min-width="140" />
          <el-table-column label="节点" min-width="120">
            <template #default="{ row }">{{ nodeName(row.node_id) }}</template>
          </el-table-column>
          <el-table-column label="条件" width="120">
            <template #default="{ row }">{{ alertCondText(row.condition) }}</template>
          </el-table-column>
          <el-table-column prop="notify_target" label="通知" min-width="120" />
          <el-table-column label="启用" width="70">
            <template #default="{ row }"><el-tag size="small" :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '是' : '否' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="60">
            <template #default="{ row }"><el-button link type="danger" size="small" @click="delAlertRule(row.id)">删</el-button></template>
          </el-table-column>
        </el-table>
      </div>

      <!-- 状态总览 -->
      <div v-else-if="tab === 'status'" class="fm-pane">
        <div class="fm-pane-hd"><h3>状态总览</h3></div>
        <div class="fm-stat-grid">
          <div class="fm-stat"><span>观测对象</span><b>{{ data.stats.total }}</b></div>
          <div class="fm-stat"><span>严重</span><b class="red">{{ data.stats.critical }}</b></div>
          <div class="fm-stat"><span>警告</span><b class="amber">{{ data.stats.warning }}</b></div>
          <div class="fm-stat"><span>正常</span><b class="green">{{ data.stats.normal }}</b></div>
          <div class="fm-stat"><span>未检查</span><b>{{ data.stats.no_data }}</b></div>
          <div class="fm-stat"><span>整体健康分</span><b :class="healthClass(data.overall_health)">{{ data.overall_health }}</b></div>
        </div>
        <el-table :data="data.layers" size="small" border style="width:100%;margin-top:16px">
          <el-table-column prop="name" label="分层" min-width="140" />
          <el-table-column prop="count" label="对象数" width="100" />
          <el-table-column prop="critical" label="严重" width="90" />
          <el-table-column prop="warning" label="警告" width="90" />
          <el-table-column prop="normal" label="正常" width="90" />
          <el-table-column prop="avg_score" label="均分" width="100" />
        </el-table>
      </div>

      <!-- SLO 报表 -->
      <div v-else-if="tab === 'slo'" class="fm-pane">
        <div class="fm-pane-hd"><h3>SLO 报表 · 错误预算</h3></div>
        <p class="fm-pane-desc">错误预算 = 允许不可用窗口 (100 - target) 相对实际不可用 (100 - 健康分) 的剩余比例。</p>
        <div class="fm-slo" v-for="s in sloItems" :key="s.id">
          <div class="fm-slo-hd"><b>{{ s.name }}</b><span>目标 {{ s.target }}% · 窗口 {{ s.window_days }} 天</span></div>
          <div class="fm-slo-bar"><i :style="{ width: s.error_budget + '%', background: scoreColor(s.error_budget) }"></i></div>
          <div class="fm-slo-ft">
            当前 {{ s.current }} 分 · 剩余预算 <b :class="healthClass(s.error_budget)">{{ s.error_budget }}%</b> · {{ statusText(s.status) }}
          </div>
        </div>
        <div v-if="!sloItems.length" class="fm-empty-sm">暂无 SLO 定义</div>
      </div>
    </div>

    <!-- PromAI 右侧对话抽屉 -->
    <div class="fm-mask" :class="{ on: drawerOpen }" @click="closeDrawer"></div>
    <div class="fm-drawer" :class="{ on: drawerOpen }">
      <div class="fm-dw-hd">
        <span class="fm-dw-tab on">PromAI</span>
        <span class="fm-dw-tools"><span class="fm-dw-x" @click="closeDrawer">✕</span></span>
      </div>
      <div class="fm-dw-body">
        <div class="fm-msg-u"><div class="fm-bubble">分析 {{ currentObj?.name || '当前对象' }} 的异常原因</div></div>
        <div class="fm-crumb">
          <span>{{ currentObj?.path || '—' }}</span>
          <span class="fm-now">数据源: {{ currentObj?.datasource || '—' }}</span>
        </div>
        <div class="fm-obj-line">
          健康分 <b :class="healthClass(currentObj?.health_score ?? 100)">{{ Math.round(currentObj?.health_score ?? 100) }}</b>
          · 告警 {{ currentObj?.alerts ?? 0 }} · 指标 {{ currentObj?.metrics ?? 0 }}
        </div>

        <div class="fm-actions">
          <button @click="runChecks" :disabled="checksLoading">{{ checksLoading ? '检查中…' : '▷ 执行定向检查' }}</button>
          <button class="primary" @click="runAI" :disabled="aiLoading">{{ aiLoading ? '诊断中…' : '✦ PromAI 定向诊断' }}</button>
        </div>

        <div class="fm-checkpanel">
          <div class="fm-check-hd">检查项 <span class="fm-arrow">»</span></div>
          <div v-if="checksLoading" class="fm-check-loading">检查中 <span class="fm-typing">•••</span></div>
          <div v-for="c in checks" :key="c.key" class="fm-crow" :title="c.detail">
            <span class="fm-cst" :class="c.status">{{ checkIcon(c.status) }}</span>
            <span class="fm-cnm">{{ c.name }}</span>
            <span class="fm-crs" :class="{ bad: c.status === 'err', run: c.status === 'warn' }">{{ c.result }}</span>
            <span class="fm-ctm">{{ c.cost_ms }}ms</span>
          </div>
          <div v-if="!checks.length && !checksLoading" class="fm-check-loading">点「执行定向检查」拉取真实数据</div>
        </div>

        <template v-if="conclusion">
          <div class="fm-dw-reply">
            <div class="fm-reply-h"><span class="fm-dot"></span>PromAI 定向诊断结论 <span class="fm-model">{{ conclusionModel }}</span></div>
            <div class="fm-reply-time">{{ conclusionAt }}</div>
            <pre class="fm-conclusion">{{ conclusion }}</pre>
          </div>
        </template>
        <template v-else>
          <div class="fm-no-ai">
            暂无该对象的 AI 结论。点击「PromAI 定向诊断」将<strong>只针对这个对象</strong>组装检查项 / 告警 / 指标上下文并交给 Agent 分析，不会触发全量巡检。
          </div>
        </template>
        <div v-if="aiError" class="fm-ai-err">{{ aiError }}</div>

        <div v-if="detailAlerts.length" class="fm-extra">
          <div class="fm-extra-hd">相关告警（{{ detailAlerts.length }}）</div>
          <div v-for="(a, i) in detailAlerts.slice(0, 5)" :key="i" class="fm-extra-row">
            [{{ a.severity }}] value={{ a.value }} threshold={{ a.threshold }}
          </div>
        </div>

        <div class="fm-extra">
          <div class="fm-extra-hd">
            下钻排障 · 配置路径
            <el-button link type="primary" size="small" @click="openDrillDialog">+ 添加</el-button>
          </div>
          <div v-for="r in drillRules" :key="r.id" class="fm-drill-row" @click="followDrill(r)">
            <span class="fm-drill-ico">{{ drillIcon(r.target_type) }}</span>
            <span class="fm-drill-nm">{{ r.name }}</span>
            <span class="fm-drill-ty">{{ drillTypeName(r.target_type) }}</span>
            <el-button link type="danger" size="small" @click.stop="delDrill(r.id)">删</el-button>
          </div>
          <div v-if="!drillRules.length" class="fm-drill-empty">暂无下钻路径，点「添加」配置到指标/日志/链路/大盘/事件。</div>
        </div>
      </div>
      <div class="fm-dw-ft">
        <el-input v-model="extraPrompt" size="small" placeholder="补充诊断要求（可选）" />
      </div>
    </div>

    <!-- 下钻规则弹窗 -->
    <el-dialog v-model="drillDialog" title="添加下钻路径" width="500px" append-to-body>
      <el-form label-width="90px" size="small">
        <el-form-item label="名称"><el-input v-model="drillForm.name" placeholder="如：查看该实例错误日志" /></el-form-item>
        <el-form-item label="目标类型">
          <el-select v-model="drillForm.target_type" style="width:160px">
            <el-option label="指标查询" value="metric" /><el-option label="日志检索" value="log" />
            <el-option label="链路分析" value="trace" /><el-option label="仪表盘" value="dashboard" />
            <el-option label="事件墙" value="event" /><el-option label="外部链接" value="external" />
          </el-select>
        </el-form-item>
        <el-form-item label="查询/地址">
          <el-input v-model="drillForm.query" placeholder="PromQL / 日志关键词 / traceId" />
        </el-form-item>
        <el-form-item v-if="drillForm.target_type === 'external' || drillForm.target_type === 'dashboard'" label="跳转地址">
          <el-input v-model="drillForm.target_url" placeholder="https://…" />
        </el-form-item>
      </el-form>
      <template #footer><el-button size="small" @click="drillDialog = false">取消</el-button><el-button size="small" type="primary" @click="saveDrill">保存</el-button></template>
    </el-dialog>

    <!-- 告警规则弹窗 -->
    <el-dialog v-model="alertRuleDialog" title="新增告警规则" width="500px" append-to-body>
      <el-form label-width="90px" size="small">
        <el-form-item label="规则名称"><el-input v-model="alertRuleForm.name" /></el-form-item>
        <el-form-item label="观测节点">
          <el-select v-model="alertRuleForm.node_id" filterable style="width:100%">
            <el-option v-for="n in flatNodes" :key="n.id" :label="n.name" :value="n.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="触发条件">
          <el-select v-model="alertRuleForm.condition" style="width:180px">
            <el-option label="对象飘红（critical）" value="critical" />
            <el-option label="任意异常（警告及以上）" value="any_abnormal" />
            <el-option label="健康分低于" value="score_below" />
          </el-select>
          <el-input-number v-if="alertRuleForm.condition === 'score_below'" v-model="alertRuleForm.score_below" :min="0" :max="100" style="margin-left:8px" />
        </el-form-item>
        <el-form-item label="通知目标"><el-input v-model="alertRuleForm.notify_target" placeholder="如：值班群 / sre@corp.com" /></el-form-item>
      </el-form>
      <template #footer><el-button size="small" @click="alertRuleDialog = false">取消</el-button><el-button size="small" type="primary" @click="saveAlertRule">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  getFiremapObjects, getFiremapObject, runFiremapChecks, triggerFiremapAIAnalyze,
  getFiremapPolaris, getFiremapNodes, saveFiremapNode, deleteFiremapNode, seedFiremapNodes,
  getFiremapCheckConfigs, saveFiremapCheckConfig, deleteFiremapCheckConfig,
  getFiremapDrillRules, getFiremapAlertRules, firemapCRUDRule, firemapDeleteRule, getFiremapTimeline,
  getFiremapTemplates, applyFiremapTemplate,
  type FCBootstrap, type FCObject, type FCTreeNode, type FCCheckItem,
  type FCPolarisItem, type FCNode, type FCCheckConfig,
  type FCDrillRule, type FCAlertRule, type FCHealthPoint,
} from '../api'

const route = useRoute()
const router = useRouter()
const tab = computed(() => (route.query.view as string) || '')
const viewMode = ref<'cards' | 'tree'>('cards')
const statusFilter = ref<'all' | 'abnormal' | 'normal' | 'nodata'>('all')
const dsFilter = ref<number | ''>('')
const loading = ref(false)
const data = ref<FCBootstrap>({
  has_data: false, overall_health: 100, objects: [], layers: [],
  tree: { id: 'root', node_id: 0, name: '服务树', level: 'business', count: '0/0', health_score: 100, status: 'normal' },
  stats: { total: 0, critical: 0, warning: 0, normal: 0, no_data: 0 },
  datasources: [],
})

const nodes = ref<FCNode[]>([])
const dsOptions = ref<{ id: number; name: string }[]>([])
const configs = ref<FCCheckConfig[]>([])
const sloItems = ref<FCPolarisItem[]>([])
const newNode = reactive<FCNode>({ parent_id: 0, name: '', level: 'business', datasource_id: 0, instance: '', sort_order: 0 })
// 按新增节点的层级过滤合法父节点：服务挂业务下、实例挂服务下
const parentOptions = computed(() => {
  const want = newNode.level === 'service' ? 'business' : newNode.level === 'instance' ? 'service' : ''
  if (!want) return []
  return nodes.value.filter(n => n.level === want).map(n => ({ id: n.id!, name: n.name }))
})
function levelTag(level: string) {
  return level === 'business' ? 'success' : level === 'service' ? 'warning' : 'info'
}
const newCfg = reactive<FCCheckConfig>({
  key: '', name: '', enabled: true, sort_order: 10, check_type: 'promql',
  promql: '', threshold: 0, compare_op: 'gt', unit: '',
})

// ---- 时间轴回放 ----
const timelineOpen = ref(false)
const tlLoading = ref(false)
const tlPoints = ref<FCHealthPoint[]>([])
const tlMinutes = ref(60)
const tlCursorX = ref(-1)
const tlHover = ref<FCHealthPoint | null>(null)
const timelinePoints = computed(() => tlPoints.value.map((p, i) => {
  const x = tlPoints.value.length > 1 ? (i / (tlPoints.value.length - 1)) * 1000 : 0
  const y = 86 - (p.score / 100) * 76
  return `${x.toFixed(1)},${y.toFixed(1)}`
}).join(' '))
// 事件标记：把低于 70 分的时间点标红
const tlEventMarks = computed(() => tlPoints.value.map((p, i) => ({
  x: tlPoints.value.length > 1 ? (i / (tlPoints.value.length - 1)) * 1000 : 0,
  bad: p.score < 70,
})).filter(m => m.bad))

// ---- 下钻规则 / 告警规则 / 模板 ----
const drillRules = ref<FCDrillRule[]>([])
const drillDialog = ref(false)
const drillForm = reactive<any>({ name: '', target_type: 'log', query: '', target_url: '' })
const alertRules = ref<FCAlertRule[]>([])
const alertRuleDialog = ref(false)
const alertRuleForm = reactive<any>({ name: '', node_id: 0, condition: 'critical', score_below: 70, notify_target: '' })
const templates = ref<any[]>([])
const tplDs = ref<number | ''>('')
const flatNodes = computed(() => nodes.value)

const drawerOpen = ref(false)
const currentObj = ref<FCObject | null>(null)
const checks = ref<FCCheckItem[]>([])
const checksLoading = ref(false)
const aiLoading = ref(false)
const conclusion = ref('')
const conclusionModel = ref('')
const conclusionAt = ref('')
const aiError = ref('')
const extraPrompt = ref('')
const detailAlerts = ref<any[]>([])

const firstObjectNodeId = computed(() => data.value.objects[0]?.node_id || 0)

interface TreeRow { node: FCTreeNode; depth: number }
function flatten(n: FCTreeNode, depth = 0): TreeRow[] {
  const rows: TreeRow[] = [{ node: n, depth }]
  for (const c of n.children || []) rows.push(...flatten(c, depth + 1))
  return rows
}
const treeRows = computed(() => (data.value.tree ? flatten(data.value.tree) : []))

const filteredLayers = computed(() => data.value.layers.filter((l) => l.count > 0))
function objectsOfLayer(name: string) {
  return data.value.objects.filter((o) => o.layer === name && matchFilter(o))
}
function matchFilter(o: FCObject) {
  if (statusFilter.value === 'abnormal') return o.status === 'critical' || o.status === 'warning'
  if (statusFilter.value === 'normal') return o.status === 'normal'
  if (statusFilter.value === 'nodata') return o.no_data
  return true
}

function scoreColor(s: number) { return s >= 95 ? '#3fb37f' : s >= 70 ? '#e0a026' : '#e8635a' }
function healthClass(s: number) { return s >= 95 ? 'green' : s >= 70 ? 'amber' : 'red' }
function checkIcon(s: string) { return s === 'ok' ? '✓' : s === 'err' ? '✕' : s === 'warn' ? '!' : '·' }
function statusText(s: string) { return s === 'critical' ? '严重' : s === 'warning' ? '警告' : s === 'normal' ? '正常' : '无数据' }
function levelText(l: string) { return l === 'business' ? '业务' : l === 'service' ? '服务' : l === 'instance' ? '实例' : l }
function layerIcon(l: string) {
  if (l.includes('业务')) return '业务'
  if (l.includes('微服务')) return '服务'
  if (l.includes('中间件')) return '中间件'
  if (l.includes('基础设施')) return '主机'
  return '对象'
}
function formatTime(s: string) {
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString('zh-CN', { hour12: false })
}
function goTab(v: string) { router.push(v ? `/firemap?view=${v}` : '/firemap') }

async function loadObjects() {
  loading.value = true
  try {
    const r = await getFiremapObjects(dsFilter.value || undefined)
    data.value = r.data
    if (tab.value === 'pool') await Promise.all([loadNodes(), loadConfigs(), loadAlertRules(), loadTemplates()])
    if (tab.value === 'slo') await loadSLO()
  } catch (e: any) {
    ElMessage.error('加载服务树失败: ' + (e?.response?.data?.error || e.message))
  } finally {
    loading.value = false
  }
}
async function loadNodes() {
  try {
    const r = await getFiremapNodes()
    nodes.value = r.data.nodes || []
    dsOptions.value = r.data.datasources || []
    if (!newNode.datasource_id && dsOptions.value.length) newNode.datasource_id = dsOptions.value[0].id
  } catch { /* ignore */ }
}
async function loadConfigs() {
  try { configs.value = (await getFiremapCheckConfigs()).data.configs || [] } catch { /* ignore */ }
}

// ---- 时间轴 ----
async function toggleTimeline() {
  timelineOpen.value = !timelineOpen.value
  if (timelineOpen.value) await loadTimeline()
}
async function loadTimeline() {
  tlLoading.value = true
  try {
    tlPoints.value = (await getFiremapTimeline(0, tlMinutes.value)).data.points || []
  } catch { tlPoints.value = [] }
  finally { tlLoading.value = false }
}
function onTimelineHover(e: MouseEvent) {
  const rect = (e.currentTarget as SVGElement).getBoundingClientRect()
  const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
  tlCursorX.value = ratio * 1000
  if (!tlPoints.value.length) return
  const idx = Math.round(ratio * (tlPoints.value.length - 1))
  tlHover.value = tlPoints.value[idx] || null
}
function tlTimeLabel(t?: number) {
  if (!t) return '—'
  return new Date(t * 1000).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })
}

// ---- 下钻规则 ----
function drillIcon(t: string) { return { metric: '∿', log: '≡', trace: '⑂', dashboard: '▦', event: '◷', external: '↗' }[t] || '·' }
function drillTypeName(t: string) { return { metric: '指标', log: '日志', trace: '链路', dashboard: '大盘', event: '事件', external: '外链' }[t] || t }
async function loadDrillRules(nodeID: number) {
  try { drillRules.value = (await getFiremapDrillRules(nodeID)).data.items || [] }
  catch { drillRules.value = [] }
}
function openDrillDialog() {
  Object.assign(drillForm, { name: '', target_type: 'log', query: '', target_url: '' })
  drillDialog.value = true
}
async function saveDrill() {
  if (!drillForm.name.trim()) { ElMessage.warning('请填写名称'); return }
  await firemapCRUDRule('drill', { ...drillForm, node_id: currentObj.value?.node_id || 0 })
  drillDialog.value = false
  ElMessage.success('下钻路径已添加')
  if (currentObj.value) await loadDrillRules(currentObj.value.node_id)
}
async function delDrill(id: number) {
  await firemapDeleteRule('drill', id)
  if (currentObj.value) await loadDrillRules(currentObj.value.node_id)
}
function followDrill(r: FCDrillRule) {
  if (r.target_type === 'external' || r.target_type === 'dashboard') {
    if (r.target_url) window.open(r.target_url, '_blank')
    else ElMessage.info('未配置跳转地址')
    return
  }
  // 内部下钻：跳到对应页面，带上查询条件
  const q: Record<string, string> = {}
  if (r.query) q.q = r.query
  if (r.target_type === 'log') router.push({ path: '/log-analysis', query: q })
  else if (r.target_type === 'trace') router.push({ path: '/trace-analysis', query: { id: r.query } })
  else if (r.target_type === 'event') router.push({ path: '/eventwall' })
  else router.push({ path: '/metrics' })
}

// ---- 告警规则 ----
function alertCondText(c: string) { return { critical: '对象飘红', any_abnormal: '任意异常', score_below: '健康分低于' }[c] || c }
function nodeName(id: number) { return nodes.value.find(n => n.id === id)?.name || ('#' + id) }
async function loadAlertRules() {
  try { alertRules.value = (await getFiremapAlertRules()).data.items || [] }
  catch { alertRules.value = [] }
}
function openAlertRuleDialog() {
  Object.assign(alertRuleForm, { name: '', node_id: nodes.value[0]?.id || 0, condition: 'critical', score_below: 70, notify_target: '' })
  alertRuleDialog.value = true
}
async function saveAlertRule() {
  if (!alertRuleForm.name.trim()) { ElMessage.warning('请填写规则名称'); return }
  await firemapCRUDRule('alert', alertRuleForm)
  alertRuleDialog.value = false
  ElMessage.success('告警规则已添加')
  await loadAlertRules()
}
async function delAlertRule(id: number) {
  await firemapDeleteRule('alert', id)
  await loadAlertRules()
}

// ---- 模板建卡 ----
async function loadTemplates() {
  try { templates.value = (await getFiremapTemplates(tplDs.value || undefined)).data.items || [] }
  catch { templates.value = [] }
}
async function applyTemplate(t: any) {
  const dsId = tplDs.value || newNode.datasource_id
  const r = await applyFiremapTemplate(t.id, Number(dsId) || 0)
  ElMessage.success(r.data.message || '已应用模板')
  await loadConfigs()
}
async function loadSLO() {
  try { sloItems.value = (await getFiremapPolaris()).data.items || [] } catch { /* ignore */ }
}
async function doSeed() {
  try {
    const r = await seedFiremapNodes()
    ElMessage.success(r.data.message || `已补齐 ${r.data.created} 个节点`)
    await loadObjects()
  } catch (e: any) { ElMessage.error('自动发现失败: ' + (e?.response?.data?.error || e.message)) }
}
async function addNode() {
  if (!newNode.name.trim()) { ElMessage.warning('请填写名称'); return }
  try {
    await saveFiremapNode({ ...newNode })
    newNode.name = ''; newNode.instance = ''
    ElMessage.success('节点已添加')
    await Promise.all([loadNodes(), loadObjects()])
  } catch (e: any) { ElMessage.error('添加失败: ' + (e?.response?.data?.error || e.message)) }
}
async function removeNode(id?: number) {
  if (!id) return
  await deleteFiremapNode(id)
  await Promise.all([loadNodes(), loadObjects()])
}
async function addConfig() {
  if (!newCfg.key.trim() || !newCfg.name.trim()) { ElMessage.warning('请填写 key 与名称'); return }
  await saveFiremapCheckConfig({ ...newCfg })
  newCfg.key = ''; newCfg.name = ''; newCfg.promql = ''
  await loadConfigs()
}
async function saveConfig(c: FCCheckConfig) { await saveFiremapCheckConfig({ ...c }) }
async function removeConfig(id?: number) { if (id) { await deleteFiremapCheckConfig(id); await loadConfigs() } }

async function openDrawer(node: number | string) {
  const id = typeof node === 'string' ? Number(String(node).replace(/^n/, '')) : node
  if (!id) { ElMessage.info('暂无观测对象，请先在卡片池添加'); return }
  currentObj.value = data.value.objects.find((o) => o.node_id === id) || null
  drawerOpen.value = true
  checks.value = []; conclusion.value = ''; conclusionModel.value = ''; aiError.value = ''
  detailAlerts.value = []
  try {
    const r = await getFiremapObject(id)
    if (r.data.object) currentObj.value = r.data.object
    detailAlerts.value = r.data.alerts || []
    if (r.data.conclusion) {
      conclusion.value = r.data.conclusion
      conclusionModel.value = r.data.conclusion_model || ''
      conclusionAt.value = formatTime(r.data.conclusion_at || new Date().toISOString())
    }
  } catch { /* ignore */ }
  await Promise.all([runChecks(), loadDrillRules(id)])
}
function closeDrawer() { drawerOpen.value = false }

async function runChecks() {
  if (!currentObj.value) return
  checksLoading.value = true
  try {
    const r = await runFiremapChecks(currentObj.value.node_id)
    checks.value = r.data.checks || []
    if (currentObj.value) {
      currentObj.value.health_score = r.data.health_score
      currentObj.value.status = r.data.status
    }
    const target = data.value.objects.find((o) => o.node_id === r.data.node_id)
    if (target) { target.health_score = r.data.health_score; target.status = r.data.status; target.no_data = false }
  } catch (e: any) {
    ElMessage.error('定向检查失败: ' + (e?.response?.data?.error || e.message))
  } finally { checksLoading.value = false }
}

async function runAI() {
  if (!currentObj.value) return
  aiLoading.value = true; aiError.value = ''
  try {
    const r = await triggerFiremapAIAnalyze({ node_id: currentObj.value.node_id, prompt: extraPrompt.value })
    if (r.data.success) {
      conclusion.value = r.data.text || ''
      conclusionModel.value = r.data.model || ''
      conclusionAt.value = formatTime(r.data.at || new Date().toISOString())
      if (r.data.checks?.length) checks.value = r.data.checks
    } else {
      aiError.value = r.data.error || 'AI 诊断失败'
    }
  } catch (e: any) {
    aiError.value = e?.response?.data?.error || e.message || 'AI 诊断失败'
  } finally { aiLoading.value = false }
}

watch(tab, (v) => {
  if (v === 'pool') { loadNodes(); loadConfigs(); loadAlertRules(); loadTemplates() }
  if (v === 'slo') loadSLO()
})
onMounted(loadObjects)
</script>

<style scoped>
.fm { min-height: 100%; background: var(--bg-primary); font-size: 12px; color: var(--text-primary); }
.fm-toolbar {
  background: #fff; border-bottom: 1px solid var(--border); padding: 8px 16px;
  display: flex; align-items: center; gap: 9px; flex-wrap: wrap;
  position: sticky; top: 0; z-index: 4;
}
.fm-vseg { display: flex; border: 1px solid var(--border); border-radius: 6px; overflow: hidden; }
.fm-vseg button { border: none; background: #fff; padding: 5px 8px; cursor: pointer; color: var(--text-secondary); width: 30px; height: 27px; }
.fm-vseg button + button { border-left: 1px solid var(--border); }
.fm-vseg button.on { background: var(--cyan-dim); color: var(--cyan); }
.fm-fseg { display: flex; gap: 3px; }
.fm-fseg button { border: 1px solid transparent; background: transparent; color: var(--text-secondary); border-radius: 5px; padding: 5px 9px; font-size: 11.5px; cursor: pointer; }
.fm-fseg button.on { background: var(--cyan-dim); color: var(--cyan); border-color: var(--border-glow); font-weight: 600; }
.fm-tb-i { width: 27px; height: 27px; border: 1px solid var(--border); border-radius: 5px; background: #fff; color: var(--text-secondary); cursor: pointer; }
.fm-spacer { flex: 1; }
.fm-overall, .fm-time { font-size: 11px; color: var(--text-secondary); }
.fm-overall b { font-size: 14px; margin-left: 4px; }
.fm-ai-top { background: var(--cyan); color: #fff; border: none; border-radius: 6px; padding: 6px 13px; font-size: 11.5px; font-weight: 600; cursor: pointer; }
.fm-body { padding: 14px 16px 40px; }

.fm-empty { text-align: center; padding: 60px 20px; color: var(--text-secondary); }
.fm-empty-icon { font-size: 40px; color: var(--cyan); opacity: .5; }
.fm-empty h3 { margin: 12px 0 8px; font-size: 15px; color: var(--text-primary); }
.fm-empty p { margin: 5px 0; line-height: 1.7; }
.fm-hint, .fm-msg { font-size: 11px; color: var(--text-tertiary); }
.fm-empty-btns { margin-top: 16px; display: flex; gap: 8px; justify-content: center; }

.fm-layer { margin-bottom: 16px; }
.fm-layer-hd { display: flex; align-items: center; gap: 7px; margin-bottom: 9px; font-size: 12.5px; font-weight: 600; color: #3a4049; }
.fm-caret { color: var(--text-tertiary); font-size: 9px; }
.fm-cnt { color: var(--text-tertiary); font-weight: 400; }
.fm-layer-score { margin-left: auto; font-weight: 400; color: var(--text-tertiary); font-size: 11px; }
.fm-cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 10px; }
.fm-card { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 12px 13px; cursor: pointer; transition: .15s; min-height: 112px; }
.fm-card:hover { border-color: var(--cyan); box-shadow: 0 3px 12px rgba(31,35,41,.08); }
.fm-card-top { display: flex; align-items: center; gap: 6px; }
.fm-card-nm { font-size: 12.5px; font-weight: 600; color: #2b2f36; }
.fm-card-src { font-size: 10px; color: var(--text-tertiary); background: #f7f7fa; padding: 1px 6px; border-radius: 3px; }
.fm-card-mk { margin-left: auto; color: #c2c8d0; }
.fm-card-path { font-size: 10px; color: var(--text-tertiary); margin: 4px 0 8px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.fm-card-mid { display: flex; align-items: center; gap: 12px; }
.fm-ring { width: 60px; height: 60px; border-radius: 50%; flex-shrink: 0; position: relative; background: conic-gradient(var(--rc) calc(var(--p) * 1%), #eef0f2 0); }
.fm-ring::after { content: ''; position: absolute; inset: 5px; border-radius: 50%; background: #fff; }
.fm-ring-in { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; z-index: 1; line-height: 1.15; }
.fm-ring-sc { font-size: 14px; font-weight: 700; color: var(--rc); }
.fm-ring-ic { font-size: 9.5px; color: var(--rc); font-weight: 600; }
.fm-card-stat { font-size: 10.5px; color: var(--text-tertiary); line-height: 1.9; }
.fm-card-stat b { color: var(--text-secondary); font-weight: 600; }
.fm-dots { display: inline-flex; gap: 3px; align-items: center; margin-right: 4px; }
.fm-dots i { width: 5px; height: 5px; border-radius: 50%; display: inline-block; }
.d-red { background: #e8635a; } .d-amber { background: #e0a026; } .d-green { background: #3fb37f; }
.fm-nodata { color: var(--text-tertiary); }

.fm-tree-wrap { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 12px; }
.fm-tree-hint { font-size: 11px; color: var(--text-tertiary); margin-bottom: 10px; }
.ft-row { display: flex; align-items: center; gap: 8px; padding: 6px 8px; border-radius: 5px; font-size: 12px; cursor: default; }
.ft-row.leaf { cursor: pointer; }
.ft-row.leaf:hover { background: var(--cyan-dim); }
.ft-caret { width: 12px; color: var(--text-tertiary); font-size: 9px; }
.ft-dot { width: 7px; height: 7px; border-radius: 50%; background: #3fb37f; flex-shrink: 0; }
.ft-dot.warning { background: #e0a026; } .ft-dot.critical { background: #e8635a; }
.ft-name { font-weight: 500; }
.ft-lv { font-size: 10px; color: var(--text-tertiary); background: #f7f7fa; padding: 1px 5px; border-radius: 3px; }
.ft-cnt { font-size: 10.5px; color: var(--text-tertiary); }
.ft-score { margin-left: auto; font-weight: 600; }
.ft-go { color: #c2c8d0; }

.fm-pane { background: #fff; border: 1px solid var(--border); border-radius: 8px; padding: 16px; }
.fm-pane-hd { display: flex; align-items: center; justify-content: space-between; }
.fm-pane-hd h3 { margin: 0; font-size: 13.5px; }
.fm-pane-desc { font-size: 11px; color: var(--text-tertiary); margin: 6px 0 12px; line-height: 1.6; }
.fm-form { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--border); }
.fm-form-t { font-size: 11.5px; color: var(--text-secondary); font-weight: 600; }
.fm-stat-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 10px; }
.fm-stat { border: 1px solid var(--border); border-radius: 8px; padding: 14px; display: flex; flex-direction: column; gap: 6px; }
.fm-stat span { font-size: 11px; color: var(--text-tertiary); }
.fm-stat b { font-size: 20px; }
.fm-slo { border: 1px solid var(--border); border-radius: 8px; padding: 12px; margin-bottom: 10px; }
.fm-slo-hd { display: flex; justify-content: space-between; font-size: 12px; }
.fm-slo-hd span { color: var(--text-tertiary); font-size: 11px; }
.fm-slo-bar { height: 8px; background: #eef0f2; border-radius: 4px; overflow: hidden; margin: 10px 0 6px; }
.fm-slo-bar i { display: block; height: 100%; border-radius: 4px; }
.fm-slo-ft { font-size: 11px; color: var(--text-secondary); }
.fm-empty-sm { color: var(--text-tertiary); font-size: 11px; padding: 20px; text-align: center; }

.red { color: #e8635a; } .green { color: #3fb37f; } .amber { color: #e0a026; }

.fm-mask { position: fixed; inset: 0; background: rgba(31,35,41,.25); z-index: 20; opacity: 0; pointer-events: none; transition: .2s; }
.fm-mask.on { opacity: 1; pointer-events: auto; }
.fm-drawer { position: fixed; top: 0; right: 0; bottom: 0; width: 420px; max-width: 92vw; background: #fff; z-index: 21;
  box-shadow: -4px 0 20px rgba(31,35,41,.12); transform: translateX(100%); transition: transform .25s ease; display: flex; flex-direction: column; }
.fm-drawer.on { transform: none; }
.fm-dw-hd { height: 46px; border-bottom: 1px solid var(--border); display: flex; align-items: center; padding: 0 14px; flex-shrink: 0; }
.fm-dw-tab { font-size: 13px; font-weight: 600; color: var(--cyan); border-bottom: 2px solid var(--cyan); padding: 12px 4px; }
.fm-dw-tools { margin-left: auto; display: flex; gap: 12px; color: var(--text-tertiary); }
.fm-dw-x { cursor: pointer; font-size: 15px; }
.fm-dw-body { flex: 1; overflow-y: auto; padding: 14px; }
.fm-msg-u { display: flex; justify-content: flex-end; }
.fm-bubble { background: var(--cyan-dim); color: var(--cyan); border-radius: 8px 8px 2px 8px; padding: 8px 12px; font-size: 11.5px; }
.fm-crumb { display: flex; justify-content: space-between; font-size: 10.5px; color: var(--text-tertiary); margin: 10px 0 6px; gap: 8px; }
.fm-crumb span:first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.fm-now { flex-shrink: 0; }
.fm-obj-line { font-size: 11px; color: var(--text-secondary); margin-bottom: 10px; }
.fm-actions { display: flex; gap: 8px; margin-bottom: 12px; }
.fm-actions button { flex: 1; border: 1px solid var(--border); background: #fff; color: var(--text-secondary); border-radius: 5px; padding: 7px; font-size: 11.5px; cursor: pointer; }
.fm-actions button.primary { background: var(--cyan); color: #fff; border-color: var(--cyan); font-weight: 600; }
.fm-actions button:disabled { opacity: .6; cursor: wait; }
.fm-checkpanel { border: 1px solid var(--border); border-radius: 6px; overflow: hidden; }
.fm-check-hd { padding: 8px 10px; background: #fafbfc; font-weight: 600; font-size: 11.5px; border-bottom: 1px solid var(--border); }
.fm-arrow { float: right; color: var(--text-tertiary); }
.fm-crow { display: flex; align-items: center; gap: 8px; padding: 7px 10px; border-bottom: 1px solid #f0f1f3; font-size: 11px; }
.fm-crow:last-child { border-bottom: none; }
.fm-cst { width: 16px; height: 16px; border-radius: 50%; display: flex; align-items: center; justify-content: center; color: #fff; font-size: 10px; background: #3fb37f; flex-shrink: 0; }
.fm-cst.err { background: #e8635a; } .fm-cst.warn { background: #e0a026; } .fm-cst.na, .fm-cst.run { background: #c2c8d0; }
.fm-cnm { width: 92px; flex-shrink: 0; color: var(--text-primary); }
.fm-crs { flex: 1; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.fm-crs.bad { color: #e8635a; } .fm-crs.run { color: #e0a026; }
.fm-ctm { color: var(--text-tertiary); font-size: 10px; }
.fm-check-loading { padding: 12px; font-size: 11px; color: var(--text-tertiary); text-align: center; }
.fm-typing { color: var(--cyan); }
.fm-dw-reply { margin-top: 14px; border: 1px solid var(--border-glow); border-radius: 6px; padding: 12px; background: #fcfbff; }
.fm-reply-h { font-size: 12px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.fm-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--cyan); }
.fm-model { margin-left: auto; font-size: 10px; color: var(--text-tertiary); font-weight: 400; }
.fm-reply-time { font-size: 10px; color: var(--text-tertiary); margin: 4px 0 8px 13px; }
.fm-conclusion { white-space: pre-wrap; word-break: break-word; font-size: 11.5px; line-height: 1.7; font-family: inherit; margin: 0; color: var(--text-primary); }
.fm-no-ai { margin-top: 14px; font-size: 11px; color: var(--text-tertiary); line-height: 1.7; border: 1px dashed var(--border); border-radius: 6px; padding: 12px; }
.fm-ai-err { margin-top: 10px; font-size: 11px; color: #e8635a; }
.fm-extra { margin-top: 14px; }
.fm-extra-hd { font-size: 11.5px; font-weight: 600; margin-bottom: 6px; display: flex; align-items: center; justify-content: space-between; }
.fm-extra-row { font-size: 10.5px; color: var(--text-secondary); padding: 3px 0; }
.fm-drill-row { display: flex; align-items: center; gap: 8px; padding: 6px 8px; border: 1px solid var(--border); border-radius: 5px; margin-bottom: 5px; cursor: pointer; font-size: 11px; }
.fm-drill-row:hover { border-color: var(--cyan); background: var(--cyan-dim); }
.fm-drill-ico { width: 20px; height: 20px; border-radius: 4px; background: var(--cyan-dim); color: var(--cyan); display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
.fm-drill-nm { flex: 1; }
.fm-drill-ty { font-size: 10px; color: var(--text-tertiary); }
.fm-drill-empty { font-size: 10.5px; color: var(--text-tertiary); padding: 8px; border: 1px dashed var(--border); border-radius: 5px; }

.fm-timeline { background: #fff; border-bottom: 1px solid var(--border); padding: 10px 16px; }
.fm-tl-hd { display: flex; align-items: center; gap: 12px; margin-bottom: 6px; }
.fm-tl-hd b { font-size: 12px; }
.fm-tl-hd span { font-size: 10.5px; color: var(--text-tertiary); }
.fm-tl-chart { position: relative; }
.fm-tl-chart svg { width: 100%; height: 70px; }
.fm-tl-axis { display: flex; justify-content: space-between; font-size: 9.5px; color: var(--text-tertiary); }
.fm-tl-tip { position: absolute; top: 0; transform: translateX(-50%); background: #2b2f36; color: #fff; font-size: 10px; padding: 3px 8px; border-radius: 4px; white-space: nowrap; pointer-events: none; }
.fm-tl-tip b { display: inline; }
.fm-tl-empty { font-size: 10.5px; color: var(--text-tertiary); padding: 8px; text-align: center; }
.fm-tb-i.on { background: var(--cyan-dim); color: var(--cyan); border-color: var(--cyan); }

.fm-tpls { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 10px; }
.fm-tpl { border: 1px solid var(--border); border-radius: 8px; padding: 12px; }
.fm-tpl-hd { display: flex; justify-content: space-between; align-items: center; gap: 8px; margin-bottom: 6px; }
.fm-tpl-hd b { font-size: 11.5px; }
.fm-tpl-match { font-size: 9.5px; color: var(--text-tertiary); background: #f7f7fa; padding: 2px 6px; border-radius: 8px; white-space: nowrap; }
.fm-tpl-match.hi { background: var(--emerald-dim); color: var(--emerald); }
.fm-tpl-match.mid { background: var(--amber-dim); color: var(--amber); }
.fm-tpl-prefix { font-size: 9.5px; color: var(--text-tertiary); margin-bottom: 10px; word-break: break-all; line-height: 1.5; }
.fm-dw-ft { padding: 10px 14px; border-top: 1px solid var(--border); flex-shrink: 0; }
</style>
