<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><AlarmClock /></el-icon> 告警规则</h2>
      <p>定义告警规则：可复用指标列表的 PromQL 和阈值，也可自定义。规则不出 PromAI，由内置评估器执行</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 规则列表</h3>
        <div class="action-bar">
          <el-select v-model="filterSeverity" placeholder="全部级别" clearable style="width: 120px;" @change="fetchData">
            <el-option label="严重 critical" value="critical" />
            <el-option label="警告 warning" value="warning" />
            <el-option label="提醒 info" value="info" />
          </el-select>
          <el-select v-model="filterEnabled" placeholder="全部状态" clearable style="width: 120px;" @change="fetchData">
            <el-option label="已启用" :value="true" />
            <el-option label="已禁用" :value="false" />
          </el-select>
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button plain :disabled="selectedIds.length===0" @click="openBatchEdit"><el-icon><Edit /></el-icon> 批量编辑</el-button>
          <el-button @click="openGenerateDialog" :disabled="allTemplates.length===0"><el-icon><Collection /></el-icon> 从模版生成</el-button>
          <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新建规则</el-button>
        </div>
      </div>

      <div v-if="selectedIds.length>0" class="batch-bar">
        <span style="font-size:13px;color:var(--text-tertiary);">已选 {{ selectedIds.length }} 条</span>
        <el-button size="small" style="color:var(--emerald);" @click="batchToggle(true)"><el-icon><Check /></el-icon> 批量启用</el-button>
        <el-button size="small" style="color:var(--red);" @click="batchToggle(false)"><el-icon><Close /></el-icon> 批量禁用</el-button>
        <el-button size="small" style="color:var(--red);" @click="batchDelete"><el-icon><Delete /></el-icon> 批量删除</el-button>
        <el-button size="small" text @click="selectedIds=[]">取消选择</el-button>
      </div>

      <el-table :data="rules" v-loading="loading" stripe size="default" @selection-change="onSelectionChange">
        <el-table-column type="selection" width="40" />
        <el-table-column label="名称" min-width="180">
          <template #default="{ row }">
            <span style="font-weight:600;">{{ row.name }}</span>
            <div style="font-size:11px;color:var(--text-tertiary);">{{ row.description }}</div>
          </template>
        </el-table-column>
        <el-table-column label="来源" width="80">
          <template #default="{ row }">
            <el-tag size="small">{{ row.source_type === 'metric' ? '指标' : '自定义' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="巡检模版" width="120">
          <template #default="{ row }">
            <el-tag v-if="row.template_id" size="small" style="background:rgba(139,92,246,0.12);color:#a78bfa;border:none;">{{ templateName(row.template_id) }}</el-tag>
            <span v-else style="color:var(--text-tertiary);font-size:12px;">-</span>
          </template>
        </el-table-column>
        <el-table-column label="级别" width="80" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="dark" :style="severityStyle(row.severity)">{{ severityLabel(row.severity) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="持续等待(for)" width="80">
          <template #default="{ row }">{{ row.for_duration || '-' }}</template>
        </el-table-column>
        <el-table-column label="数据源" width="120" align="center">
          <template #default="{ row }">
            <el-tooltip placement="top" :show-after="200" effect="dark">
              <template #content>
                <div style="max-width:300px;line-height:1.6;">
                  <div v-if="resolveRuleDS(row).names.length === 0" style="color:#fca5a5;">无匹配数据源</div>
                  <template v-else>
                    <div style="font-size:12px;color:#9ca3af;margin-bottom:4px;">
                      命中 {{ resolveRuleDS(row).count }} 个数据源（最多展示 10）:
                    </div>
                    <div v-for="n in resolveRuleDS(row).names.slice(0, 10)" :key="n" style="font-size:12px;">· {{ n }}</div>
                    <div v-if="resolveRuleDS(row).count > 10" style="font-size:12px;color:#9ca3af;margin-top:2px;">
                      …还有 {{ resolveRuleDS(row).count - 10 }} 个
                    </div>
                  </template>
                </div>
              </template>
              <span style="cursor:help;">
                <el-tag v-if="resolveRuleDS(row).isAll" size="small" style="background:rgba(16,185,129,0.12);color:#10b981;border:none;">All ({{ resolveRuleDS(row).count }})</el-tag>
                <span v-else style="color:var(--text-secondary);">{{ resolveRuleDS(row).count }}</span>
              </span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="70" align="center">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" size="small" @change="toggleEnabled(row)" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text style="color:var(--cyan);" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" text style="color:var(--emerald);" @click="handleTest(row)">测试</el-button>
            <el-button size="small" text style="color:var(--red);" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pager">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100, 200]"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchData"
          @size-change="fetchData"
        />
      </div>
      <el-empty v-if="!loading && rules.length === 0" description="暂无告警规则" :image-size="60" />
    </div>

    <!-- 新建/编辑对话框 -->
    <el-dialog v-model="dialog" :title="editingId ? '编辑规则' : '新建规则'" width="800" top="2vh" :close-on-click-modal="false" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rulesValidator" label-width="100px">
        <el-row :gutter="16">
          <el-col :span="12">
            <el-form-item label="规则名称" prop="name">
              <el-input v-model="form.name" placeholder="如：CPU 使用率过高" />
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="来源" prop="source_type">
              <el-select v-model="form.source_type" style="width:100%;">
                <el-option label="复用指标" value="metric" />
                <el-option label="自定义 PromQL" value="custom" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <div v-if="form.source_type === 'metric'">
          <el-form-item label="巡检模版">
            <el-select v-model="form.template_id" clearable placeholder="选模版可过滤关联指标" style="width:100%;" @change="handleTemplateChange">
              <el-option v-for="t in allTemplates" :key="t.id" :label="`${t.name} (${t.metric_count} 指标)`" :value="t.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="关联指标" prop="metric_config_id">
            <el-select v-model="form.metric_config_id" filterable placeholder="搜索选择指标..." style="width:100%;">
              <el-option v-for="m in filteredMetrics" :key="m.id" :label="`${m.name} [${m.type_name}]`" :value="m.id">
                <span>{{ m.name }}</span>
                <span style="color:var(--text-tertiary); margin-left:8px;">{{ m.type_name }}</span>
                <span v-if="m.threshold !== undefined && m.threshold !== null" style="color:#10b981;margin-left:8px;font-size:11px;">
                  {{ thresholdOpSymbol(m.threshold_type) }} {{ m.threshold }}
                </span>
                <code style="float:right;font-size:10px;color:var(--text-tertiary);max-width:200px;overflow:hidden;">{{ m.query.substring(0,60) }}</code>
              </el-option>
            </el-select>
          </el-form-item>
          <div v-if="selectedMetric" style="margin:-8px 0 12px 100px;font-size:12px;color:var(--text-tertiary);">
            <div>PromQL: <code>{{ selectedMetric.query }}</code></div>
            <div v-if="selectedMetric.threshold !== undefined && selectedMetric.threshold !== null" style="margin-top:2px;">
              指标自带阈值：<span style="color:#10b981;">{{ thresholdOpSymbol(selectedMetric.threshold_type) }} {{ selectedMetric.threshold }}</span>
              <span style="color:#9ca3af;">（已自动填入下方"阈值"和"条件"，可编辑覆盖）</span>
            </div>
          </div>
        </div>

        <div v-else>
          <el-form-item label="PromQL" prop="expr">
            <el-input v-model="form.expr" type="textarea" :rows="2" placeholder="avg(rate(node_cpu_seconds_total[5m])) * 100 > 80" />
          </el-form-item>
        </div>

        <!-- 数据源 + 预览（两种来源共用） -->
        <div style="display:flex;gap:8px;justify-content:flex-end;margin:-16px 0 12px;">
          <el-select v-model="previewDSId" size="small" filterable placeholder="选择数据源" style="width:180px;" @change="previewDSId=$event">
            <el-option v-for="ds in allDS" :key="ds.id" :label="ds.name" :value="ds.id" />
          </el-select>
          <el-button size="small" :loading="promqlPreviewing" @click="handlePromqlPreview" style="color:var(--cyan);" :disabled="form.source_type==='metric' && !selectedMetric">
            <el-icon><Connection /></el-icon> 预览
          </el-button>
        </div>
        <div v-if="promqlPreview" :class="['validation-panel', promqlPreview.valid ? 'valid' : 'invalid']" style="margin-bottom:12px;">
          <div v-if="promqlPreview.valid" style="font-size:13px;">
            <div style="display:flex;gap:16px;margin-bottom:8px;">
              <span style="color:var(--emerald);">语法正确</span>
              <span style="color:var(--text-tertiary);">类型: {{ promqlPreview.type }}</span>
              <span v-if="promqlPreview.count!==undefined" style="color:var(--text-tertiary);">样本: {{ promqlPreview.count }}</span>
            </div>
            <div v-if="promqlPreview.labels?.length" style="margin-bottom:6px;">
              <span style="font-size:12px;color:var(--text-tertiary);">返回标签（点击插入到 Labels/Annotations）: </span>
              <el-tag v-for="l in promqlPreview.labels" :key="l" size="small" style="cursor:pointer;background:rgba(99,102,241,0.1);color:#818cf8;border:none;margin:1px 4px 1px 0;" @click="insertLabelKey(l)">{{ l }}</el-tag>
            </div>
            <div v-if="promqlPreview.samples?.length" style="max-height:140px;overflow-y:auto;">
              <div v-for="(s,i) in promqlPreview.samples" :key="i" style="font-size:12px;padding:2px 0;border-bottom:1px solid var(--border);display:flex;gap:8px;">
                <span style="color:var(--text-tertiary);min-width:40px;">#{{ i+1 }}</span>
                <span style="color:var(--text-secondary);flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{{ formatLabels(s.labels) }}</span>
                <span style="color:var(--cyan);font-weight:600;">{{ typeof s.value==='number' ? s.value.toFixed(2) : s.value }}</span>
              </div>
            </div>
          </div>
          <div v-else style="color:var(--red);font-size:13px;">
            <el-icon><WarningFilled /></el-icon> {{ promqlPreview.error || promqlPreview.message }}
          </div>
        </div>

        <el-row :gutter="16">
          <el-col :span="6">
            <el-form-item label="阈值">
              <el-input v-model.number="form.threshold" type="number" step="0.5" min="0" style="width:100%;" placeholder="阈值" />
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="条件">
              <el-select v-model="form.threshold_type" style="width:100%;">
                <el-option label=">" value="greater" />
                <el-option label=">=" value="greater_equal" />
                <el-option label="<" value="less" />
                <el-option label="<=" value="less_equal" />
                <el-option label="=" value="equal" />
                <el-option label="!=" value="not_equal" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="严重级别" prop="severity">
              <el-select v-model="form.severity" style="width:100%;">
                <el-option label="严重 critical" value="critical" />
                <el-option label="警告 warning" value="warning" />
                <el-option label="提醒 info" value="info" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="持续等待" prop="for_duration">
              <el-select v-model="form.for_duration" allow-create filterable style="width:100%;">
                <el-option label="立即" value="" />
                <el-option label="30s" value="30s" />
                <el-option label="1m" value="1m" />
                <el-option label="5m" value="5m" />
                <el-option label="10m" value="10m" />
                <el-option label="30m" value="30m" />
                <el-option label="1h" value="1h" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="持续保留" prop="keep_firing_for">
              <el-select v-model="form.keep_firing_for" allow-create filterable style="width:100%;">
                <el-option label="立即恢复" value="" />
                <el-option label="30s" value="30s" />
                <el-option label="1m" value="1m" />
                <el-option label="5m" value="5m" />
                <el-option label="10m" value="10m" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="重复间隔" prop="repeat_interval">
              <el-select v-model="form.repeat_interval" allow-create filterable clearable style="width:100%;">
                <el-option label="继承路由配置" value="" />
                <el-option label="30m" value="30m" />
                <el-option label="1h" value="1h" />
                <el-option label="2h" value="2h" />
                <el-option label="4h" value="4h" />
                <el-option label="8h" value="8h" />
                <el-option label="12h" value="12h" />
                <el-option label="24h" value="24h" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="最大发送" prop="max_send_count">
              <el-select v-model="form.max_send_count" allow-create filterable clearable style="width:100%;">
                <el-option label="继承路由配置" :value="0" />
                <el-option label="1 次" :value="1" />
                <el-option label="2 次" :value="2" />
                <el-option label="3 次" :value="3" />
                <el-option label="5 次" :value="5" />
                <el-option label="10 次" :value="10" />
                <el-option label="20 次" :value="20" />
                <el-option label="50 次" :value="50" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="数据源">
          <div style="width:100%;">
            <el-radio-group v-model="dsMode" size="small" style="margin-bottom:8px;">
              <el-radio-button value="selector">标签选择器</el-radio-button>
              <el-radio-button value="explicit">显式列表</el-radio-button>
            </el-radio-group>
            <div v-if="dsMode==='explicit'">
              <el-select v-model="form.datasource_ids" multiple filterable placeholder="选择数据源" style="width:100%;">
                <el-option v-for="ds in allDS" :key="ds.id" :label="ds.name" :value="ds.id" />
              </el-select>
            </div>
            <div v-else>
              <el-row :gutter="8">
                <el-col :span="6">
                  <el-checkbox v-model="selectorAll">全部</el-checkbox>
                </el-col>
                <el-col :span="8">
                  <el-input v-model="selectorProject" placeholder="按项目名(project)" size="small" />
                </el-col>
                <el-col :span="10">
                  <el-input v-model="selectorNameRegex" placeholder="按名称正则匹配" size="small" />
                </el-col>
              </el-row>
              <div style="font-size:11px;color:var(--text-tertiary);margin-top:4px;">
                匹配数据源的条件之间是 AND，与显式 ID 列表互补
              </div>
            </div>
          </div>
        </el-form-item>

        <el-form-item label="Labels">
          <div style="width:100%;">
            <div style="font-size:12px;color:var(--text-tertiary);margin-bottom:4px;">
              告警身份标识，参与路由/静默/去重匹配。{{ labelsCount ? labelsCount+' 项' : '' }}
            </div>
            <el-input v-model="labelsStr" type="textarea" :rows="2" placeholder='{"team":"infra","env":"prod"}' />
            <div style="margin-top:4px;display:flex;gap:4px;flex-wrap:wrap;">
              <span style="font-size:11px;color:var(--text-tertiary);line-height:22px;">预览标签: </span>
              <el-tag v-for="l in (promqlPreview?.labels||[])" :key="l" size="small" style="cursor:pointer;background:rgba(99,102,241,0.08);color:#818cf8;border:none;" @click="insertLabelKey(l)">+ {{ l }}</el-tag>
              <span v-if="!promqlPreview?.labels?.length" style="font-size:11px;color:var(--text-tertiary);line-height:22px;">先预览 PromQL 后可从返回标签中选择</span>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="告警描述">
          <div style="width:100%;">
            <div style="font-size:12px;color:var(--text-tertiary);margin-bottom:4px;">
              告警通知中展示的描述文本，可自由编写。点击下方变量可引用预览返回的标签或当前值
            </div>
            <el-input v-model="annStr" ref="annInputRef" type="textarea" :rows="3" :placeholder="'例：' + labelTpl('instance') + ' 当前值 ' + valueTpl + ' 超过阈值 ' + thresholdTpl" />
            <div style="margin-top:4px;display:flex;gap:4px;flex-wrap:wrap;">
              <span style="font-size:11px;color:var(--text-tertiary);line-height:22px;">插入变量: </span>
              <el-tag size="small" style="cursor:pointer;background:rgba(16,185,129,0.1);color:#10b981;border:none;" @click="appendAnnVar(valueTpl)">{{ valueTpl }}</el-tag>
              <el-tag size="small" style="cursor:pointer;background:rgba(245,158,11,0.1);color:#f59e0b;border:none;" @click="appendAnnVar(thresholdTpl)">{{ thresholdTpl }}</el-tag>
              <el-tag v-for="l in (promqlPreview?.labels||[])" :key="'a-'+l" size="small" style="cursor:pointer;background:rgba(99,102,241,0.08);color:#818cf8;border:none;" @click="appendAnnVar(labelTpl(l))">{{ labelTpl(l) }}</el-tag>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="可能原因">
          <el-input v-model="form.cause" type="textarea" :rows="2" placeholder="告警触发可能的原因，如：节点负载过高导致响应变慢" />
        </el-form-item>
        <el-form-item label="影响范围">
          <el-input v-model="form.impact" type="textarea" :rows="2" placeholder="告警影响的范围，如：影响所有 /api/v1/order 接口调用" />
        </el-form-item>

        <el-row :gutter="16">
          <el-col :span="10">
            <el-form-item label="通知路由">
              <el-select v-model="form.route_id" clearable placeholder="按路由树匹配" style="width:100%;">
                <el-option v-for="r in allRoutes" :key="r.id" :label="r.name" :value="r.id" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="14" v-if="!form.route_id">
            <el-form-item label="直发通道">
              <el-select v-model="form.notify_channel_ids" multiple clearable placeholder="不走路由直发" style="width:100%;">
                <el-option v-for="ch in allChannels" :key="ch.id" :label="ch.name" :value="ch.id" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>

      <template #footer>
        <el-button @click="dialog=false">取消</el-button>
        <el-button :loading="saving" type="primary" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>

    <!-- 测试结果 -->
    <el-dialog v-model="testDialog" title="规则测试结果" width="720" top="3vh">
      <div v-loading="testing">
        <div v-if="testResults.length === 0 && !testing" style="text-align:center;padding:40px;color:var(--text-tertiary);">
          无命中数据，规则可能匹配不到任何数据源或 PromQL 返回空
        </div>
        <div v-for="dsr in testResults" :key="dsr.datasource_id" class="test-ds-block">
          <div class="test-ds-header">
            <el-icon><Connection /></el-icon>
            <span>{{ dsr.datasource_name }}</span>
            <el-tag v-if="dsr.success" size="small" type="success">成功</el-tag>
            <el-tag v-else size="small" type="danger">{{ dsr.error }}</el-tag>
          </div>
          <div v-if="dsr.success && dsr.samples?.length" class="test-samples">
            <div v-for="(s, i) in dsr.samples" :key="i" class="test-sample">
              <span class="idx">#{{ i+1 }}</span>
              <span class="labels">{{ formatLabels(s.labels) }}</span>
              <span class="val">{{ s.value }}</span>
              <el-tag v-if="s.triggered" size="small" type="danger" style="margin-left:auto;">触发</el-tag>
              <el-tag v-else size="small" style="margin-left:auto;background:rgba(16,185,129,0.12);color:#10b981;border:none;">正常</el-tag>
            </div>
          </div>
          <div v-else-if="dsr.success" style="padding:8px;font-size:12px;color:var(--text-tertiary);">
            查询成功，返回 0 条样本
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- 从模版生成 -->
    <el-dialog v-model="generateDialog" title="从巡检模版生成告警规则" width="480">
      <p style="font-size:13px;color:var(--text-tertiary);margin-bottom:16px;">自动为所选模版中尚无告警规则的指标创建默认告警规则</p>
      <el-select v-model="generateTemplateId" filterable placeholder="选择巡检模版" style="width:100%;">
        <el-option v-for="t in allTemplates" :key="t.id" :label="`${t.name} (${t.metric_count} 指标)`" :value="t.id" />
      </el-select>
      <div v-if="generateTemplateId" style="margin-top:12px;font-size:12px;color:var(--text-tertiary);">
        <span>将扫描模版中所有指标，仅创建不存在的规则</span>
      </div>
      <template #footer>
        <el-button @click="generateDialog=false">取消</el-button>
        <el-button type="primary" :loading="generating" @click="handleGenerate">开始生成</el-button>
      </template>
    </el-dialog>

    <!-- 批量编辑对话框 -->
    <el-dialog v-model="batchEditDialog" title="批量编辑" width="560">
      <div style="font-size:13px;color:var(--text-tertiary);margin-bottom:16px;">将同时对 {{ selectedIds.length }} 条规则应用以下修改。留空的字段不会被更改。</div>
      <el-form label-width="100px">
        <el-form-item label="严重级别">
          <el-select v-model="batchEditForm.severity" clearable placeholder="不修改" style="width:100%;">
            <el-option label="严重 critical" value="critical" />
            <el-option label="警告 warning" value="warning" />
            <el-option label="提醒 info" value="info" />
          </el-select>
        </el-form-item>
        <el-row :gutter="8">
          <el-col :span="8">
            <el-form-item label="持续等待">
              <el-select v-model="batchEditForm.for_duration" clearable allow-create filterable placeholder="不修改" style="width:100%;">
                <el-option label="立即" value="" />
                <el-option label="30s" value="30s" />
                <el-option label="1m" value="1m" />
                <el-option label="5m" value="5m" />
                <el-option label="10m" value="10m" />
                <el-option label="30m" value="30m" />
                <el-option label="1h" value="1h" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="持续保留">
              <el-select v-model="batchEditForm.keep_firing_for" clearable allow-create filterable placeholder="不修改" style="width:100%;">
                <el-option label="立即恢复" value="" />
                <el-option label="30s" value="30s" />
                <el-option label="1m" value="1m" />
                <el-option label="5m" value="5m" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="重复间隔">
              <el-select v-model="batchEditForm.repeat_interval" clearable allow-create filterable placeholder="不修改" style="width:100%;">
                <el-option label="继承路由" value="" />
                <el-option label="30m" value="30m" />
                <el-option label="1h" value="1h" />
                <el-option label="2h" value="2h" />
                <el-option label="4h" value="4h" />
                <el-option label="8h" value="8h" />
                <el-option label="12h" value="12h" />
                <el-option label="24h" value="24h" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item label="最大发送">
          <el-select v-model="batchEditForm.max_send_count" clearable allow-create filterable placeholder="不修改" style="width:100%;">
            <el-option label="继承路由" :value="0" />
            <el-option label="1 次" :value="1" />
            <el-option label="2 次" :value="2" />
            <el-option label="3 次" :value="3" />
            <el-option label="5 次" :value="5" />
            <el-option label="10 次" :value="10" />
            <el-option label="20 次" :value="20" />
            <el-option label="50 次" :value="50" />
          </el-select>
        </el-form-item>
        <el-form-item label="数据源">
          <div style="width:100%;">
            <el-radio-group v-model="batchEditDSMode" size="small" style="margin-bottom:8px;">
              <el-radio-button value="selector">标签选择器</el-radio-button>
              <el-radio-button value="explicit">显式列表</el-radio-button>
            </el-radio-group>
            <div v-if="batchEditDSMode==='explicit'">
              <el-select v-model="batchEditForm.datasource_ids" multiple filterable clearable placeholder="不修改（选中即覆盖）" style="width:100%;">
                <el-option v-for="ds in allDS" :key="ds.id" :label="ds.name" :value="ds.id" />
              </el-select>
              <div style="font-size:11px;color:var(--text-tertiary);margin-top:4px;">选中任意数据源将覆盖规则原有的数据源列表</div>
            </div>
            <div v-else>
              <el-row :gutter="8">
                <el-col :span="6">
                  <el-checkbox v-model="batchEditSelAll">全部数据源</el-checkbox>
                </el-col>
                <el-col :span="8">
                  <el-input v-model="batchEditSelProject" placeholder="按项目名(project)" size="small" />
                </el-col>
                <el-col :span="10">
                  <el-input v-model="batchEditSelNameRegex" placeholder="按名称正则匹配" size="small" />
                </el-col>
              </el-row>
              <div style="font-size:11px;color:var(--text-tertiary);margin-top:4px;">条件之间是 AND，与显式 ID 列表互补</div>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="通知路由">
          <el-select v-model="batchEditForm.route_id" clearable placeholder="不修改" style="width:100%;">
            <el-option label="无（走直发通道）" :value="0" />
            <el-option v-for="r in allRoutes" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="直发通道" v-if="!batchEditForm.route_id">
          <el-select v-model="batchEditForm.notify_channel_ids" multiple clearable placeholder="不修改" style="width:100%;">
            <el-option v-for="ch in allChannels" :key="ch.id" :label="ch.name" :value="ch.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用">
          <el-select v-model="batchEditForm.enabled" clearable placeholder="不修改" style="width:100%;">
            <el-option label="启用" :value="true" />
            <el-option label="禁用" :value="false" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="batchEditDialog=false">取消</el-button>
        <el-button type="primary" :loading="batchEditSaving" @click="handleBatchEdit">保存修改</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'
import {
  getAlertRules, createAlertRule, updateAlertRule, deleteAlertRule, testAlertRule,
  getAllDataSources, getMetricTypes, getAlertRoutes, getAllNotifications,
  getAllTemplates, getTemplateMetrics, batchToggleAlertRules, batchDeleteAlertRules, batchEditAlertRules,
  generateAlertRulesFromTemplate, validatePromQL,
} from '../api'
import type { AlertRule, AlertRoute, EvaluatorStatus } from '../types/alerting'
import type { DataSource, NotificationChannel, MetricConfig } from '../types'

function getCssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

const loading = ref(false)
const rules = ref<AlertRule[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const allDS = ref<DataSource[]>([])
const allChannels = ref<NotificationChannel[]>([])
const allRoutes = ref<AlertRoute[]>([])
const allMetrics = ref<(MetricConfig & { type_name: string })[]>([])
const allTemplates = ref<any[]>([])
const templateMetrics = ref<(MetricConfig & { type_name: string })[]>([])
const filterSeverity = ref('')
const filterEnabled = ref<boolean | ''>('')
const dialog = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const formRef = ref<FormInstance>()

const dsMode = ref<'explicit' | 'selector'>('selector')
const selectorAll = ref(false)
const selectorProject = ref('')
const selectorNameRegex = ref('')
const labelsStr = ref('')
const annStr = ref('')

const testDialog = ref(false)
const testing = ref(false)
const testResults = ref<any[]>([])
const selectedIds = ref<number[]>([])
const batchEditDialog = ref(false)
const batchEditSaving = ref(false)
const batchEditForm = ref<Record<string, any>>({})
const batchEditDSMode = ref<'explicit' | 'selector'>('selector')
const batchEditSelAll = ref(false)
const batchEditSelProject = ref('')
const batchEditSelNameRegex = ref('')
const generateDialog = ref(false)
const generateTemplateId = ref<number | null>(null)
const generating = ref(false)
const promqlPreviewing = ref(false)
const promqlPreview = ref<any>(null)
const previewDSId = ref<number | null>(null)

const form = ref<AlertRule>({
  name: '',
  source_type: 'metric',
  severity: 'warning',
  enabled: true,
  datasource_ids: [],
})

const rulesValidator: Record<string, any> = {
  name: [{ required: true, message: '请输入规则名称', trigger: 'blur' }],
}

const filteredMetrics = computed(() => {
  if (!form.value.template_id) return allMetrics.value
  return templateMetrics.value
})
const selectedMetric = computed(() => {
  if (form.value.source_type !== 'metric' || !form.value.metric_config_id) return null
  return filteredMetrics.value.find(m => m.id === form.value.metric_config_id) || null
})

// suppressMetricAutoFill 用于编辑现有规则时初次加载，避免覆盖用户原本的设置。
const suppressMetricAutoFill = ref(false)
watch(selectedMetric, (m) => {
  if (suppressMetricAutoFill.value) return
  if (!m || form.value.source_type !== 'metric') return
  const t = Number(m.threshold); if (!isNaN(t)) form.value.threshold = t
  if (m.threshold_type) form.value.threshold_type = m.threshold_type
  if (m.threshold_status) form.value.severity = m.threshold_status as any
  form.value.has_threshold = true
})

// 同步 selector <-> form.datasource_selector JSON
watch([selectorAll, selectorProject, selectorNameRegex], () => {
  const sel: any = {}
  if (selectorAll.value) sel.all = true
  if (selectorProject.value) sel.project_name = selectorProject.value
  if (selectorNameRegex.value) sel.name_regex = selectorNameRegex.value
  const keys = Object.keys(sel)
  form.value.datasource_selector = keys.length > 0 ? JSON.stringify(sel) : ''
}, { deep: true })

// load existing selector from form
function parseSelector() {
  const s = form.value.datasource_selector
  if (!s) {
    selectorAll.value = false
    selectorProject.value = ''
    selectorNameRegex.value = ''
    return
  }
  try {
    const o = JSON.parse(s)
    selectorAll.value = !!o.all
    selectorProject.value = o.project_name || ''
    selectorNameRegex.value = o.name_regex || ''
    if (o.project_name || o.name_regex || o.all) dsMode.value = 'selector'
  } catch { /* ignore */ }
}

function severityLabel(s: string) { return { critical: '严重', warning: '警告', info: '提醒' }[s] || s }
function severityStyle(s: string) {
  const map: Record<string, any> = {
    critical: { background: '#ef4444', color: '#fff', border: 'none' },
    warning: { background: '#f59e0b', color: '#fff', border: 'none' },
    info: { background: '#3b82f6', color: '#fff', border: 'none' },
  }
  return map[s] || map.warning
}
function formatLabels(labels: Record<string, string>) {
  if (!labels) return ''
  return Object.entries(labels).map(([k, v]) => `${k}="${v}"`).join(', ')
}
function templateName(id: number | null | undefined) {
  if (!id) return ''
  const t = allTemplates.value.find(t => t.id === id)
  return t?.name || `模版#${id}`
}
function thresholdOpSymbol(t: string | undefined | null) {
  return ({
    greater: '>', greater_equal: '>=',
    less: '<', less_equal: '<=',
    equal: '=', not_equal: '!=',
  } as Record<string, string>)[t || ''] || '>'
}
// 解析一条规则实际匹配到的数据源（与后端 resolveDatasources 等价）
function resolveRuleDS(rule: AlertRule): { isAll: boolean; count: number; names: string[] } {
  const picked = new Map<number, string>()
  // 1. 显式 ID 列表（仅 enabled）
  for (const id of (rule.datasource_ids || [])) {
    const ds = allDS.value.find(d => d.id === id && d.enabled)
    if (ds && ds.id != null) picked.set(ds.id, ds.name)
  }
  // 2. selector 匹配
  let sel: any = null
  const raw = (rule.datasource_selector || '').trim()
  if (raw) {
    try { sel = JSON.parse(raw) } catch { /* ignore */ }
  }
  let isAll = false
  if (sel && (sel.all || sel.project_name || sel.name_regex || sel.url_contains)) {
    isAll = !!sel.all
    let regex: RegExp | null = null
    if (sel.name_regex) {
      try { regex = new RegExp(sel.name_regex) } catch { regex = null }
    }
    for (const ds of allDS.value) {
      if (!ds.enabled) continue
      if (!sel.all && !sel.project_name && !sel.name_regex && !sel.url_contains) continue
      if (sel.project_name && (ds.project_name || '').toLowerCase() !== String(sel.project_name).toLowerCase()) continue
      if (sel.url_contains && !(ds.url || '').includes(sel.url_contains)) continue
      if (regex && !regex.test(ds.name || '')) continue
      if (ds.id != null) picked.set(ds.id, ds.name)
    }
  }
  const names = Array.from(picked.values()).sort()
  return { isAll, count: picked.size, names }
}
function onSelectionChange(rows: any[]) {
  selectedIds.value = rows.map((r: any) => r.id).filter(Boolean)
}
async function batchToggle(enabled: boolean) {
  if (selectedIds.value.length === 0) return
  try {
    await batchToggleAlertRules(selectedIds.value, enabled)
    ElMessage.success(enabled ? '已批量启用' : '已批量禁用')
    selectedIds.value = []
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
}
async function batchDelete() {
  if (selectedIds.value.length === 0) return
  try {
    await ElMessageBox.confirm(`确定批量删除 ${selectedIds.value.length} 条告警规则？`, '确认', { type: 'warning' })
    await batchDeleteAlertRules(selectedIds.value)
    ElMessage.success('已批量删除')
    selectedIds.value = []
    await fetchData()
  } catch (e: any) {
    if (e !== 'cancel') ElMessage.error(e.message)
  }
}
function openBatchEdit() {
  if (selectedIds.value.length === 0) {
    ElMessage.warning('请先在表格中勾选要编辑的规则')
    return
  }
  batchEditForm.value = {}
  batchEditDSMode.value = 'selector'
  batchEditSelAll.value = false
  batchEditSelProject.value = ''
  batchEditSelNameRegex.value = ''
  batchEditDialog.value = true
}
async function handleBatchEdit() {
  const updates: Record<string, any> = {}
  const f = batchEditForm.value
  if (f.severity !== undefined && f.severity !== '') updates.severity = f.severity
  if (f.for_duration !== undefined) updates.for_duration = f.for_duration
  if (f.keep_firing_for !== undefined) updates.keep_firing_for = f.keep_firing_for
  if (f.repeat_interval !== undefined) updates.repeat_interval = f.repeat_interval
  if (f.max_send_count !== undefined) updates.max_send_count = f.max_send_count
  if (batchEditDSMode.value === 'explicit') {
    if (f.datasource_ids !== undefined) updates.datasource_ids = f.datasource_ids
  } else {
    const sel: any = {}
    if (batchEditSelAll.value) sel.all = true
    if (batchEditSelProject.value) sel.project_name = batchEditSelProject.value
    if (batchEditSelNameRegex.value) sel.name_regex = batchEditSelNameRegex.value
    const json = Object.keys(sel).length > 0 ? JSON.stringify(sel) : ''
    if (json) updates.datasource_selector = json
  }
  if (f.route_id !== undefined) updates.route_id = f.route_id === 0 ? null : f.route_id
  if (f.notify_channel_ids !== undefined) updates.notify_channel_ids = f.notify_channel_ids
  if (f.enabled !== undefined && f.enabled !== '') updates.enabled = f.enabled
  if (Object.keys(updates).length === 0) {
    ElMessage.warning('请至少选择一个要修改的字段')
    return
  }
  batchEditSaving.value = true
  try {
    await batchEditAlertRules(selectedIds.value, updates)
    ElMessage.success(`已批量修改 ${selectedIds.value.length} 条规则`)
    batchEditDialog.value = false
    selectedIds.value = []
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
  finally { batchEditSaving.value = false }
}
function openGenerateDialog() {
  generateTemplateId.value = null
  generateDialog.value = true
}
async function handlePromqlPreview() {
  // 来源：metric 模式用指标的 PromQL，custom 模式用 form.expr
  const q = form.value.source_type === 'metric'
    ? (selectedMetric.value?.query || '').trim()
    : (form.value.expr || '').trim()
  if (!q) { ElMessage.warning(form.value.source_type === 'metric' ? '请先选择关联指标' : '请先输入 PromQL'); return }
  const dsId = previewDSId.value || form.value.datasource_ids?.[0] || allDS.value[0]?.id || 0
  if (!dsId) { ElMessage.warning('请先选择数据源'); return }
  promqlPreviewing.value = true
  promqlPreview.value = null
  try {
    const res = await validatePromQL(dsId, q)
    promqlPreview.value = res.data
    if (!res.data.valid) ElMessage.warning(res.data.error || '语法错误')
  } catch (e: any) {
    promqlPreview.value = { valid: false, error: e.message }
    ElMessage.error(e.message)
  } finally { promqlPreviewing.value = false }
}
function insertLabelKey(key: string) {
  let current: Record<string, string> = {}
  if (labelsStr.value) {
    try { current = JSON.parse(labelsStr.value) || {} } catch { current = {} }
  }
  current[key] = '{{ .' + key + ' }}'
  labelsStr.value = JSON.stringify(current, null, 2)
  ElMessage.success(`已添加 ${key} 到 Labels`)
}
// 用 String.fromCharCode 避免 Vue 模板源里出现 {{ }} 造成误解析
const _OB = String.fromCharCode(123, 123) // '{{'
const _CB = String.fromCharCode(125, 125) // '}}'
const valueTpl = _OB + ' $value ' + _CB
const thresholdTpl = _OB + ' $threshold ' + _CB
function labelTpl(l: string) { return _OB + ' .' + l + ' ' + _CB }
const annPlaceholder = '{"summary":"' + valueTpl + ' CPU usage","description":"..."}'
const labelsCount = computed(() => {
  if (!labelsStr.value) return 0
  try { return Object.keys(JSON.parse(labelsStr.value) || {}).length } catch { return 0 }
})
function appendAnnVar(tpl: string) {
  const cur = annStr.value || ''
  const sep = cur && !cur.endsWith(' ') && !cur.endsWith('\n') ? ' ' : ''
  annStr.value = cur + sep + tpl
}
// 编辑时把存储的 JSON annotations 反解为纯文本（兼容旧数据）
function parseAnnForEdit(raw: string): string {
  const s = (raw || '').trim()
  if (!s) return ''
  try {
    const o = JSON.parse(s)
    if (o && typeof o === 'object') {
      // 优先 description，其次 summary，其次拼接所有字段
      if (o.description) return String(o.description)
      if (o.summary) return String(o.summary)
      return Object.entries(o).map(([k, v]) => `${k}: ${v}`).join('\n')
    }
  } catch {
    // 非 JSON，按纯文本返回
  }
  return s
}
async function handleGenerate() {
  if (!generateTemplateId.value) { ElMessage.warning('请选择模版'); return }
  generating.value = true
  try {
    const res = await generateAlertRulesFromTemplate(generateTemplateId.value)
    ElMessage.success(`已生成 ${res.data.created} 条告警规则`)
    generateDialog.value = false
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
  finally { generating.value = false }
}

async function fetchData() {
  loading.value = true
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (filterSeverity.value) params.severity = filterSeverity.value
    if (filterEnabled.value !== '') params.enabled = filterEnabled.value ? 'true' : 'false'
    const res = await getAlertRules(params)
    rules.value = res.data.items
    total.value = res.data.total
  } catch (e: any) { ElMessage.error(e.message) }
  finally { loading.value = false }
}

async function loadMeta() {
  try {
    const [dsRes, chRes, rtRes, mtRes, tmplRes] = await Promise.all([
      getAllDataSources(),
      getAllNotifications(),
      getAlertRoutes(),
      getMetricTypes(),
      getAllTemplates(),
    ])
    allDS.value = dsRes.data
    allChannels.value = chRes.data || []
    allRoutes.value = rtRes.data?.items || []
    allTemplates.value = tmplRes.data || []
    allMetrics.value = []
    for (const mt of mtRes.data) {
      for (const cfg of mt.configs || []) {
        allMetrics.value.push({ ...cfg, type_name: mt.type_name })
      }
    }
  } catch { /* ignore */ }
}

async function handleTemplateChange() {
  templateMetrics.value = []
  if (!form.value.template_id) return
  try {
    const res = await getTemplateMetrics(form.value.template_id)
    const list = res.data || []
    templateMetrics.value = list.map((m: any) => ({ ...m, type_name: m.type_name || '' }))
    // 如果当前已选的指标不在新模版中，清空
    if (form.value.metric_config_id && !templateMetrics.value.find(m => m.id === form.value.metric_config_id)) {
      form.value.metric_config_id = undefined
    }
  } catch { templateMetrics.value = [] }
}

function openCreate() {
  editingId.value = null
  form.value = { name: '', source_type: 'metric', severity: 'warning', enabled: true, datasource_ids: [], template_id: null }
  templateMetrics.value = []
  labelsStr.value = ''
  annStr.value = ''
  form.value.cause = ''
  form.value.impact = ''
  dsMode.value = 'selector'
  selectorAll.value = false
  selectorProject.value = ''
  selectorNameRegex.value = ''
  suppressMetricAutoFill.value = false  // 新建：选了指标就自动填充
  dialog.value = true
}

function openEdit(row: AlertRule) {
  editingId.value = row.id!
  suppressMetricAutoFill.value = true   // 编辑：先抑制自动填充，加载完再放开
  form.value = { ...row, datasource_ids: [...(row.datasource_ids || [])], template_id: row.template_id || null }
  labelsStr.value = row.labels_json || ''
  annStr.value = parseAnnForEdit(row.annotations_json || '')
  dsMode.value = row.datasource_ids?.length ? 'explicit' : 'selector'
  parseSelector()
  templateMetrics.value = []
  if (row.template_id) {
    getTemplateMetrics(row.template_id).then(r => {
      templateMetrics.value = (r.data || []).map((m: any) => ({ ...m, type_name: m.type_name || '' }))
    }).catch(() => {})
  }
  dialog.value = true
  // 下一个 tick 后放开自动填充，让用户改指标时仍然触发
  setTimeout(() => { suppressMetricAutoFill.value = false }, 100)
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (form.value.datasource_ids?.length === 0 && dsMode.value === 'explicit') {
    ElMessage.warning('请至少选择一个数据源')
    return
  }
  // labels/annotations
  form.value.labels_json = labelsStr.value || ''
  form.value.annotations_json = annStr.value || ''
  if (form.value.source_type === 'metric' && !form.value.metric_config_id) {
    ElMessage.warning('请选择关联指标')
    return
  }
  if (form.value.source_type === 'custom' && !form.value.expr?.trim()) {
    ElMessage.warning('请填写 PromQL')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await updateAlertRule(editingId.value, form.value)
      ElMessage.success('更新成功')
    } else {
      await createAlertRule(form.value)
      ElMessage.success('创建成功')
    }
    dialog.value = false
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
  finally { saving.value = false }
}

async function handleDelete(row: AlertRule) {
  try {
    await ElMessageBox.confirm(`确定删除规则「${row.name}」？`, '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
    await deleteAlertRule(row.id!)
    ElMessage.success('删除成功')
    await fetchData()
  } catch { /* ignore */ }
}

async function toggleEnabled(row: AlertRule) {
  try {
    await updateAlertRule(row.id!, { ...row, enabled: !row.enabled })
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
}

async function handleTest(row: AlertRule) {
  if (!row.id) { ElMessage.warning('规则未保存'); return }
  testing.value = true
  testDialog.value = true
  testResults.value = []
  try {
    const res = await testAlertRule(row.id)
    testResults.value = res.data.datasources || []
  } catch (e: any) { ElMessage.error(e.message) }
  finally { testing.value = false }
}

onMounted(async () => {
  await loadMeta()
  await fetchData()
})
</script>

<style scoped>
.test-ds-block {
  margin-bottom: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.test-ds-header {
  padding: 8px 12px;
  background: var(--bg-secondary);
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
}
.test-samples {
  padding: 4px 0;
}
.test-sample {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
}
.test-sample .idx { color: var(--text-tertiary); min-width: 30px; }
.test-sample .labels { color: var(--text-secondary); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.test-sample .val { color: var(--cyan); font-weight: 600; font-family: monospace; }
.batch-bar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; background: rgba(99,102,241,0.08); border-radius: 6px; margin-bottom: 8px; }
.pager { display: flex; justify-content: flex-end; padding: 12px 4px; }
.validation-panel { margin: 8px 0 12px 100px; padding: 8px 12px; border-radius: 6px; font-size: 13px; }
.validation-panel.valid { background: rgba(16,185,129,0.08); border: 1px solid rgba(16,185,129,0.2); }
.validation-panel.invalid { background: rgba(239,68,68,0.08); border: 1px solid rgba(239,68,68,0.2); }
</style>
