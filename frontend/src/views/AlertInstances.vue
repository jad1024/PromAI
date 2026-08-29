<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Warning /></el-icon> 实时告警</h2>
      <p>当前监控到的告警实例（按指纹去重）。未读红点数字 = 新增告警次数，点开详情自动清零</p>
    </div>

    <!-- 顶部统计 -->
    <div class="stats-row">
      <div class="stat-card stat-critical">
        <div class="stat-label">严重 critical</div>
        <div class="stat-value">{{ summary.critical }}</div>
      </div>
      <div class="stat-card stat-warning">
        <div class="stat-label">警告 warning</div>
        <div class="stat-value">{{ summary.warning }}</div>
      </div>
      <div class="stat-card stat-info">
        <div class="stat-label">提醒 info</div>
        <div class="stat-value">{{ summary.info }}</div>
      </div>
      <div class="stat-card stat-firing">
        <div class="stat-label">firing 中</div>
        <div class="stat-value">{{ summary.firing }}</div>
      </div>
      <div class="stat-card stat-pending">
        <div class="stat-label">pending 中</div>
        <div class="stat-value">{{ summary.pending }}</div>
      </div>
      <div class="stat-card stat-resolved">
        <div class="stat-label">已恢复</div>
        <div class="stat-value">{{ resolvedTotal }}</div>
        <div class="stat-extra">含历史保留实例</div>
      </div>
      <div class="stat-card stat-unread">
        <div class="stat-label">未读</div>
        <div class="stat-value" :class="{ zero: unreadTotal === 0 }">{{ unreadTotal }}</div>
        <div class="stat-extra">当前页新增告警</div>
      </div>
    </div>

    <!-- 近 24 小时告警趋势（按来源） -->
    <div class="section-card src-trend-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><TrendCharts /></el-icon> 近 24 小时告警趋势（按来源）</h3>
        <div class="action-bar">
          <span v-if="sourceTrendEmpty" class="src-trend-empty">暂无趋势数据（产生告警事件后展示）</span>
        </div>
      </div>
      <div ref="srcTrendEl" v-show="!sourceTrendEmpty" class="src-trend-chart"></div>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 活跃告警列表</h3>
        <div class="action-bar action-bar--compact">
          <div class="refresh-ctl" title="自动刷新间隔（秒），修改后立即生效并保存">
            <el-icon><Timer /></el-icon>
            <span>刷新</span>
            <el-input-number v-model="refreshSec" :min="5" :max="600" :step="5" size="small" controls-position="right" style="width: 92px" @change="restartTimer" />
            <span>秒</span>
          </div>
          <el-select v-model="filters.severity" placeholder="全部级别" clearable size="small" style="width: 110px;" @change="fetchData">
            <el-option label="严重 critical" value="critical" />
            <el-option label="警告 warning" value="warning" />
            <el-option label="提醒 info" value="info" />
          </el-select>
          <el-select v-model="filters.state" placeholder="全部状态" clearable size="small" style="width: 140px;" @change="fetchData">
            <el-option label="活跃 (pending+firing)" value="pending,firing" />
            <el-option label="firing" value="firing" />
            <el-option label="pending" value="pending" />
            <el-option label="resolved" value="resolved" />
            <el-option label="全部（含已恢复）" value="" />
          </el-select>
          <el-select v-model="filters.datasource_id" placeholder="全部数据源" clearable filterable size="small" style="width: 150px;" @change="fetchData">
            <el-option v-for="ds in datasources" :key="ds.id" :label="ds.name" :value="ds.id" />
          </el-select>
          <el-date-picker
            v-model="timeRange"
            type="datetimerange"
            range-separator="至"
            start-placeholder="触发时间起"
            end-placeholder="触发时间止"
            size="small"
            style="width: 280px;"
            :clearable="true"
            @change="onTimeRangeChange"
          />
          <el-input v-model="filters.keyword" placeholder="搜索 label" size="small" style="width: 150px;" clearable @keyup.enter="fetchData">
            <template #suffix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-checkbox v-model="includeMasked" @change="fetchData">含已静默/抑制</el-checkbox>
          <el-button size="small" plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button-group>
            <el-button size="small" plain :type="viewMode === 'card' ? 'primary' : 'default'" @click="setViewMode('card')" title="卡片视图"><el-icon><Grid /></el-icon></el-button>
            <el-button size="small" plain :type="viewMode === 'table' ? 'primary' : 'default'" @click="setViewMode('table')" title="表格视图"><el-icon><Tickets /></el-icon></el-button>
          </el-button-group>
          <el-dropdown trigger="click">
            <el-button size="small" plain title="自定义显示列"><el-icon><Setting /></el-icon></el-button>
            <template #dropdown>
              <div style="padding:8px 12px;min-width:150px;">
                <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">
                  <span style="font-size:12px;color:var(--text-tertiary);">显示字段</span>
                  <div style="display:flex;gap:6px;">
                    <el-button size="small" text style="padding:0;height:auto;" @click="resetColumns">重置</el-button>
                    <el-button size="small" text style="padding:0;height:auto;" @click="selectAllColumns">全选</el-button>
                  </div>
                </div>
                <el-checkbox v-for="c in ALL_COLUMNS" :key="c.key" :model-value="isVisible(c.key)" @change="(v: any) => toggleColumn(c.key, Boolean(v))">
                  {{ c.label }}
                </el-checkbox>
                <div style="margin-top:8px;padding-top:8px;border-top:1px solid var(--border);font-size:11px;color:var(--text-tertiary);">
                  表格/卡片视图均生效
                </div>
              </div>
            </template>
          </el-dropdown>
          <el-button size="small" plain type="danger" @click="handleClearAll"><el-icon><Delete /></el-icon> 清空所有</el-button>
        </div>
      </div>

      <!-- 批量操作栏 -->
      <div class="batch-bar" v-if="selection.length > 0">
        <span class="batch-info">已选 <b>{{ selection.length }}</b> 条</span>
        <el-button size="small" plain @click="batchRead"><el-icon><Check /></el-icon> 标记已读</el-button>
        <el-button size="small" type="success" plain @click="batchResolve"><el-icon><CircleCheck /></el-icon> 结束</el-button>
        <el-button size="small" type="warning" plain @click="batchSilence"><el-icon><Mute /></el-icon> 静默</el-button>
        <el-button size="small" type="danger" plain @click="batchDelete"><el-icon><Delete /></el-icon> 删除</el-button>
        <el-button size="small" text @click="clearSelection"><el-icon><Close /></el-icon> 取消选择</el-button>
      </div>

      <el-table v-if="viewMode === 'table'" :data="rows" v-loading="loading" stripe size="default" @selection-change="onSelectionChange">
        <el-table-column type="selection" width="44" align="center" />
        <el-table-column v-if="isVisible('severity')" label="级别" width="80" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="dark" :style="severityStyle(row.severity)">{{ severityLabel(row.severity) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('state')" label="状态" width="92" align="center">
          <template #default="{ row }">
            <el-tag size="small" :style="stateStyle(row.state)">{{ row.state }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('unread')" label="未读" width="62" align="center">
          <template #default="{ row }">
            <span v-if="(row.unread_count || 0) > 0" class="unread-badge"
                  :title="`${row.unread_count} 次新增告警未读，点击详情或标记已读清零`">
              {{ row.unread_count > 99 ? '99+' : row.unread_count }}
            </span>
            <span v-else class="unread-none">-</span>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('masked')" label="抑制/静默" width="98" align="center">
          <template #default="{ row }">
            <el-tag v-if="row.silenced_by?.length" size="small" class="tag-silence">静默</el-tag>
            <el-tag v-if="row.inhibited_by?.length" size="small" class="tag-inhibit">抑制</el-tag>
            <span v-if="!row.silenced_by?.length && !row.inhibited_by?.length" class="unread-none">-</span>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('rule')" label="规则" min-width="200">
          <template #default="{ row }">
            <div class="table-cell-main">
              <span class="main" :title="row.rule_name || ruleName(row.rule_id)">{{ row.rule_name || ruleName(row.rule_id) }}</span>
              <span class="sub" :title="row.annotations?.summary || row.external_source_name || '-'">{{ row.annotations?.summary || row.external_source_name || '-' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('datasource')" label="数据源" min-width="120">
          <template #default="{ row }">
            <span class="ds-name cell-ellipsis" :title="dsDisplay(row)">{{ dsDisplay(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('value')" label="value / threshold" width="140" align="right">
          <template #default="{ row }">
            <span class="val-cyan">{{ formatNum(row.value) }}</span>
            <span class="val-dim"> / {{ formatNum(row.threshold) }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('firingCount')" label="触发" width="64" align="center">
          <template #default="{ row }">
            <span class="firing-count">{{ row.firing_count || 0 }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('labels')" label="标签" min-width="220">
          <template #default="{ row }">
            <div class="label-stack">
              <el-tag v-for="(v, k) in row.labels || {}" :key="k" size="small" class="label-pill" :title="`${k}=${v}`">
                <span class="pill-k">{{ k }}=</span><span class="pill-v">{{ v }}</span>
              </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('trend')" label="趋势" width="120" align="center">
          <template #default="{ row }">
            <Sparkline :data="trends[row.fingerprint]" :color="row.state === 'resolved' ? '#10b981' : '#3b82f6'" />
          </template>
        </el-table-column>
        <el-table-column v-if="isVisible('time')" label="触发时间" width="148">
          <template #default="{ row }">
            <span class="time-cell cell-ellipsis" :title="formatTime(row.fired_at || row.active_at)">{{ formatTime(row.fired_at || row.active_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="148" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <el-tooltip content="详情" placement="top">
                <el-button size="small" text style="color: var(--cyan);" @click="openDetail(row)"><el-icon><View /></el-icon></el-button>
              </el-tooltip>
              <el-tooltip v-if="(row.unread_count || 0) > 0" content="标记已读" placement="top">
                <el-button size="small" text style="color: var(--red);" @click="markRead(row)"><el-icon><Check /></el-icon></el-button>
              </el-tooltip>
              <el-tooltip content="静默" placement="top">
                <el-button size="small" text style="color: var(--amber);" @click="openSilence(row)"><el-icon><Mute /></el-icon></el-button>
              </el-tooltip>
              <el-tooltip v-if="row.state === 'firing' || row.state === 'pending'" content="结束告警" placement="top">
                <el-button size="small" text type="danger" :loading="resolvingFp === row.fingerprint" @click="doResolve(row)"><el-icon><CircleCheck /></el-icon></el-button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 卡片视图（参考告警历史的事件列表样式，支持自定义显示字段） -->
      <div v-else class="event-list">
        <div v-for="row in rows" :key="row.fingerprint" class="event-row" :class="row.state">
          <div class="event-bar" :class="row.state"></div>
          <div class="event-body">
            <el-checkbox
              :model-value="selection.some(s => s.fingerprint === row.fingerprint)"
              @change="(v: any) => toggleCardSelection(row, Boolean(v))"
              style="margin-right:8px;"
            />
            <div class="event-main">
              <div class="event-title-line">
                <div class="event-title">
                  <template v-if="isVisible('datasource')">
                    <span class="event-ds">{{ dsDisplay(row) }}</span>
                    <span v-if="isVisible('rule')" class="event-divider">/</span>
                  </template>
                  <span v-if="isVisible('rule')" class="event-rule">{{ row.rule_name || ruleName(row.rule_id) }}</span>
                  <el-tag v-if="isVisible('state')" size="small" :style="stateStyle(row.state)" style="margin-left:8px;">{{ row.state }}</el-tag>
                  <el-tag v-if="isVisible('severity')" size="small" effect="dark" :style="severityStyle(row.severity)" style="margin-left:6px;">{{ severityLabel(row.severity) }}</el-tag>
                  <span v-if="isVisible('unread') && (row.unread_count || 0) > 0" class="unread-badge" style="margin-left:6px;">{{ row.unread_count }}</span>
                  <template v-if="isVisible('masked')">
                    <el-tag v-if="row.silenced_by?.length" size="small" class="tag-silence" style="margin-left:6px;">静默</el-tag>
                    <el-tag v-if="row.inhibited_by?.length" size="small" class="tag-inhibit" style="margin-left:6px;">抑制</el-tag>
                  </template>
                </div>
                <div v-if="isVisible('time')" class="event-times">
                  <div class="time-item">
                    <span class="time-label">触发时间</span>
                    <span class="time-value">{{ formatTime(row.fired_at || row.active_at) }}</span>
                  </div>
                </div>
              </div>
              <div class="event-meta">
                <span v-if="isVisible('value')" class="meta-val">
                  value={{ formatNum(row.value) }}
                  <span class="meta-threshold">threshold={{ formatNum(row.threshold) }}</span>
                </span>
                <span v-if="isVisible('firingCount') && (row.firing_count || 0) > 0" class="meta-count">触发 {{ row.firing_count }} 次</span>
                <template v-if="isVisible('labels')">
                  <el-tag v-for="(v, k) in row.labels || {}" :key="k" size="small" class="event-label-tag">{{ k }}={{ v }}</el-tag>
                </template>
              </div>
              <div v-if="isVisible('trend') && trends[row.fingerprint]?.length" class="card-trend">
                <Sparkline :data="trends[row.fingerprint]" :color="row.state === 'resolved' ? '#10b981' : '#3b82f6'" />
              </div>
            </div>
            <div class="event-actions">
              <el-button size="small" text style="color: var(--cyan);" @click="openDetail(row)">详情</el-button>
              <el-button size="small" text style="color: var(--amber);" @click="openSilence(row)">静默</el-button>
              <el-button
                v-if="row.state === 'firing' || row.state === 'pending'"
                size="small" text type="danger"
                :loading="resolvingFp === row.fingerprint"
                @click="doResolve(row)"
              >结束</el-button>
            </div>
          </div>
        </div>
      </div>

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
    </div>

    <!-- 详情抽屉 -->
    <el-drawer v-model="detailDrawer" size="620" :title="detailTitle" direction="rtl">
      <div v-if="detailRow" class="detail">
        <div class="detail-section">
          <div class="detail-row">
            <span class="k">状态</span>
            <el-tag size="small" :style="stateStyle(detailRow.state)">{{ detailRow.state }}</el-tag>
            <el-tag size="small" effect="dark" :style="severityStyle(detailRow.severity)" style="margin-left:8px;">
              {{ severityLabel(detailRow.severity) }}
            </el-tag>
            <span v-if="(detailRow.unread_count || 0) > 0" class="unread-badge" style="margin-left:8px;">{{ detailRow.unread_count }} 条未读</span>
          </div>
          <div class="detail-row">
            <span class="k">规则名称</span>
            <span class="v strong">{{ detailRow.rule_name || ruleName(detailRow.rule_id) }}</span>
          </div>
          <div class="detail-row">
            <span class="k">数据源</span>
            <span class="v">{{ dsDisplay(detailRow) }}</span>
          </div>
          <div class="detail-row" v-if="detailRow.external_source_id">
            <span class="k">外部告警源</span>
            <span class="v strong" style="color: var(--purple);">{{ detailRow.external_source_name }}</span>
          </div>
          <div class="detail-row">
            <span class="k">触发值 value</span>
            <span class="v mono" style="color: var(--cyan);">{{ formatNum(detailRow.value) }}</span>
          </div>
          <div class="detail-row">
            <span class="k">阈值 threshold</span>
            <span class="v mono">{{ formatNum(detailRow.threshold) }}</span>
          </div>
          <div class="detail-row">
            <span class="k">触发次数</span>
            <span class="v strong" style="color: var(--amber);">{{ detailRow.firing_count || 0 }}</span>
          </div>
          <div class="detail-row">
            <span class="k">通知次数</span>
            <span class="v">{{ detailRow.notified_count || 0 }}</span>
            <span class="hint" v-if="detailRow.external_source_id">（外部告警按需转发通知）</span>
          </div>
          <div class="detail-row"><span class="k">首次触发</span><span class="v">{{ formatTime(detailRow.fired_at) }}</span></div>
          <div class="detail-row"><span class="k">恢复时间</span><span class="v">{{ formatTime(detailRow.resolved_at) }}</span></div>
          <div class="detail-row"><span class="k">最近评估</span><span class="v">{{ formatTime(detailRow.last_eval_at) }}</span></div>
          <div class="detail-row"><span class="k">下轮通知</span><span class="v" style="color: var(--red);font-weight:600;">{{ formatTime(detailRow.next_notify_at) }}</span></div>
        </div>

        <div class="detail-section">
          <div class="section-title">趋势（最近 60 分钟）</div>
          <div style="margin-bottom:8px;display:flex;align-items:center;gap:8px;">
            <el-checkbox v-model="detailIncludeRepeats" @change="loadDetailTrend">包括重发的</el-checkbox>
            <span class="hint">勾选后把当前实例自身的历史触发事件也画进趋势（PromQL 无数据时兜底，不跨实例聚合）</span>
          </div>
          <div v-if="detailTrendLoading" class="trend-empty">趋势加载中…</div>
          <div v-else-if="detailTrendPts.length > 0" ref="trendChartEl" class="trend-chart"></div>
          <div v-else class="trend-empty">暂无趋势数据：本地告警需 Prometheus 数据源可查询且最近 60 分钟有采样；外部告警需收到至少 1 次触发事件。勾选"包括重发的"可从历史事件中补全曲线。</div>
        </div>

        <div class="detail-section">
          <div class="section-title">Labels</div>
          <pre class="kv-block">{{ JSON.stringify(detailRow.labels || {}, null, 2) }}</pre>
        </div>
        <div class="detail-section">
          <div class="section-title">Annotations</div>
          <pre class="kv-block">{{ JSON.stringify(detailRow.annotations || {}, null, 2) }}</pre>
        </div>
        <div class="detail-section">
          <div class="section-title">通知去向 ({{ notifyLogsRows.length }})</div>
          <div v-if="notifyLogsRows.length === 0" class="empty-hint">该分组暂无通知发送记录</div>
          <div v-for="n in notifyLogsRows" :key="n.id" class="notify-row">
            <el-tag size="small" :style="notifyStatusStyle(n.status)">{{ notifyStatusLabel(n.status) }}</el-tag>
            <span class="chan">{{ n.channel_type }}</span>
            <span class="cname">{{ channelName(n.channel_id) }}</span>
            <span class="cnt">{{ n.alert_count }} 条告警</span>
            <span class="ts">{{ formatTime(n.sent_at) }}</span>
            <div v-if="n.error" class="err">错误: {{ n.error }}</div>
          </div>
        </div>
        <div class="detail-section" v-if="historyRows.length">
          <div class="section-title">历史事件（最近 {{ historyRows.length }} 条）</div>
          <div v-for="(h, idx) in historyRows" :key="h.id" class="hist-row">
            <div class="hist-left">
              <el-tag size="small" :style="eventStyle(h.event_type)">{{ h.event_type }}</el-tag>
              <span class="ts">{{ formatTime(h.occurred_at) }}</span>
              <span v-if="historyRows.length > 1" class="hist-seq">#{{ idx + 1 }}</span>
            </div>
            <div class="hist-metrics">
              <span class="vv">value={{ formatNum(h.value) }}</span>
              <span class="vv-dim">threshold={{ formatNum(h.threshold) }}</span>
            </div>
          </div>
        </div>
      </div>
    </el-drawer>

    <!-- 静默对话框（单个 / 批量） -->
    <el-dialog v-model="silenceDialog" :title="silenceBatchMode ? `批量静默（${silenceFps.length} 条）` : '创建静默'" width="560">
      <el-form :model="silenceForm" label-width="100px">
        <el-form-item label="静默原因">
          <el-input v-model="silenceForm.comment" type="textarea" :rows="2" placeholder="必填，描述静默原因" />
        </el-form-item>
        <el-form-item label="持续时间">
          <el-radio-group v-model="silenceForm.durationMin">
            <el-radio :label="15">15m</el-radio>
            <el-radio :label="60">1h</el-radio>
            <el-radio :label="240">4h</el-radio>
            <el-radio :label="1440">24h</el-radio>
            <el-radio :label="10080">7d</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="!silenceBatchMode" label="匹配条件">
          <div class="matcher-hint">将基于当前告警的 alertname / 关键标签自动生成，可手动微调</div>
          <el-table :data="silenceForm.matchers" size="small">
            <el-table-column label="标签" width="160">
              <template #default="{ row }">
                <el-input v-model="row.name" size="small" />
              </template>
            </el-table-column>
            <el-table-column label="op" width="80">
              <template #default="{ row }">
                <el-select v-model="row.op" size="small">
                  <el-option label="=" value="=" />
                  <el-option label="!=" value="!=" />
                  <el-option label="=~" value="=~" />
                  <el-option label="!~" value="!~" />
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="值">
              <template #default="{ row }">
                <el-input v-model="row.value" size="small" />
              </template>
            </el-table-column>
            <el-table-column width="60">
              <template #default="{ $index }">
                <el-button size="small" text style="color: var(--red);"
                  @click="silenceForm.matchers.splice($index, 1)">删</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-button size="small" plain style="margin-top: 6px;"
            @click="silenceForm.matchers.push({ name: '', op: '=', value: '' })">
            <el-icon><Plus /></el-icon> 添加
          </el-button>
        </el-form-item>
        <el-form-item v-else label="匹配条件">
          <div class="matcher-hint">批量静默将按每条告警的 alertname + 关键标签分别生成匹配条件</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="silenceDialog = false">取消</el-button>
        <el-button type="primary" :loading="silenceSaving" @click="handleSilenceSubmit">创建静默</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import * as echarts from 'echarts'
import dayjs from 'dayjs'
import Sparkline from '../components/Sparkline.vue'
import { ElMessage, ElMessageBox, ElNotification } from 'element-plus'
import {
  getAlertInstances, getAlertInstance, getAlertInstancesTrend,
  createAlertSilence, clearAlertInstances, resolveAlertInstance,
  getAllDataSources, getAlertRules, getAlertEvaluatorStatus, getAllNotifications,
  getAlertStats, markAlertInstanceRead, batchAlertInstances,
} from '../api'
import type { AlertInstance, AlertHistoryRow, AlertRule, AlertStats, EvaluatorStatus } from '../types/alerting'
import type { DataSource, NotificationChannel } from '../types'

const router = useRouter()

function getCssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

const rows = ref<AlertInstance[]>([])
const datasources = ref<DataSource[]>([])
const rules = ref<AlertRule[]>([])
const allChannels = ref<NotificationChannel[]>([])
const evaluator = ref<EvaluatorStatus>({ running: false })
const stats = ref<AlertStats | null>(null)
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const includeMasked = ref(false)
const filters = ref<{ severity: string; state: string; datasource_id: number | ''; keyword: string }>({
  severity: '', state: 'pending,firing', datasource_id: '', keyword: '',
})
// 触发时间范围筛选（datetimerange），值为 [起始, 结束] 或 null
const timeRange = ref<[Date, Date] | null>(null)
function onTimeRangeChange() {
  page.value = 1
  fetchData()
}

// 视图模式：card / table（默认表格视图，更符合传统告警列表习惯）
const VIEW_MODE_KEY = 'promai_alert_view_mode'
const viewMode = ref<'card' | 'table'>('table')
function initViewMode() {
  const saved = localStorage.getItem(VIEW_MODE_KEY)
  viewMode.value = saved === 'card' ? 'card' : 'table'
}
function setViewMode(mode: 'card' | 'table') {
  viewMode.value = mode
  localStorage.setItem(VIEW_MODE_KEY, mode)
}

// 可自定义显示列
const COLUMN_STORAGE_KEY = 'promai_alert_visible_columns'
const ALL_COLUMNS = [
  { key: 'severity', label: '级别' },
  { key: 'state', label: '状态' },
  { key: 'unread', label: '未读' },
  { key: 'masked', label: '抑制/静默' },
  { key: 'rule', label: '规则' },
  { key: 'datasource', label: '数据源' },
  { key: 'value', label: 'value / threshold' },
  { key: 'firingCount', label: '触发次数' },
  { key: 'labels', label: '标签' },
  { key: 'trend', label: '趋势' },
  { key: 'time', label: '触发时间' },
]
const visibleColumns = ref<string[]>([])
function initVisibleColumns() {
  try {
    const saved = JSON.parse(localStorage.getItem(COLUMN_STORAGE_KEY) || 'null')
    if (Array.isArray(saved) && saved.length > 0) visibleColumns.value = saved
    else visibleColumns.value = ALL_COLUMNS.map(c => c.key)
  } catch {
    visibleColumns.value = ALL_COLUMNS.map(c => c.key)
  }
}
function toggleColumn(key: string, checked: boolean) {
  const set = new Set(visibleColumns.value)
  if (checked) set.add(key)
  else set.delete(key)
  visibleColumns.value = ALL_COLUMNS.filter(c => set.has(c.key)).map(c => c.key)
  localStorage.setItem(COLUMN_STORAGE_KEY, JSON.stringify(visibleColumns.value))
}
function selectAllColumns() {
  visibleColumns.value = ALL_COLUMNS.map(c => c.key)
  localStorage.setItem(COLUMN_STORAGE_KEY, JSON.stringify(visibleColumns.value))
}
function resetColumns() {
  visibleColumns.value = ALL_COLUMNS.map(c => c.key)
  localStorage.setItem(COLUMN_STORAGE_KEY, JSON.stringify(visibleColumns.value))
}
const isVisible = computed(() => {
  const set = new Set(visibleColumns.value)
  return (key: string) => set.has(key)
})

// 批量选择
const selection = ref<AlertInstance[]>([])
function onSelectionChange(sel: AlertInstance[]) {
  selection.value = sel
}
function toggleCardSelection(row: AlertInstance, checked: boolean) {
  const idx = selection.value.findIndex(s => s.fingerprint === row.fingerprint)
  if (checked && idx === -1) selection.value.push(row)
  if (!checked && idx !== -1) selection.value.splice(idx, 1)
}
function clearSelection() {
  selection.value = []
}

// 自动刷新间隔（秒），持久化到 localStorage
const refreshSec = ref(Number(localStorage.getItem('promai_alert_refresh_sec') || 30))
function persistRefreshSec() {
  localStorage.setItem('promai_alert_refresh_sec', String(refreshSec.value))
}
watch(refreshSec, persistRefreshSec)

const detailDrawer = ref(false)
const detailRow = ref<AlertInstance | null>(null)
const historyRows = ref<AlertHistoryRow[]>([])
const notifyLogsRows = ref<any[]>([])

const trends = ref<Record<string, [number, number][]>>({})
const resolvingFp = ref<string | null>(null)
const silenceDialog = ref(false)
const silenceSaving = ref(false)
const silenceBatchMode = ref(false)
const silenceFps = ref<string[]>([])
const silenceForm = ref<{ comment: string; durationMin: number; matchers: Array<{ name: string; op: string; value: string }> }>({
  comment: '', durationMin: 60, matchers: [],
})

const detailTitle = computed(() => {
  if (!detailRow.value) return '告警详情'
  return `告警详情 · ${detailRow.value.rule_name || detailRow.value.fingerprint?.slice(0, 12) || ''}`
})

const summary = computed(() => {
  const s = { critical: 0, warning: 0, info: 0, firing: 0, pending: 0 }
  for (const r of rows.value) {
    if (r.severity === 'critical') s.critical++
    else if (r.severity === 'warning') s.warning++
    else if (r.severity === 'info') s.info++
    if (r.state === 'firing') s.firing++
    else if (r.state === 'pending') s.pending++
  }
  return s
})

const resolvedTotal = computed(() => {
  // 已恢复实例会被清理，从 stats 的历史表统计读取（24h 内）
  return stats.value?.resolved_count || 0
})

const unreadTotal = computed(() => rows.value.reduce((acc, r) => acc + (r.unread_count || 0), 0))

// 详情趋势图（最近 60 分钟；勾选"包括重发的"后按 alertname 汇总全部触发事件）
const trendChartEl = ref<HTMLDivElement | null>(null)
let trendChart: echarts.ECharts | null = null
const detailIncludeRepeats = ref(false)
const detailTrendPts = ref<[number, number][]>([])
const detailTrendLoading = ref(false)

// 趋势数据异步到达后自动渲染图表
watch(detailTrendPts, () => {
  if (detailDrawer.value) renderTrendChart()
}, { deep: true })

async function loadDetailTrend() {
  if (!detailRow.value) return
  detailTrendLoading.value = true
  try {
    const res = await getAlertInstancesTrend([detailRow.value.fingerprint], 60, detailIncludeRepeats.value)
    detailTrendPts.value = res.data?.[detailRow.value.fingerprint] || []
  } catch {
    detailTrendPts.value = []
  } finally {
    detailTrendLoading.value = false
    renderTrendChart()
  }
}

// 后端趋势点时间戳是 Unix 秒（浮点，如 1787627244.735）；同时兼容毫秒(>1e11)与 RFC3339 字符串
function trendTimeToDate(t: number | string): Date {
  if (typeof t === 'string') return new Date(t)
  const n = Number(t)
  return new Date(n < 1e11 ? n * 1000 : n)
}

function renderTrendChart() {
  const pts = detailTrendPts.value || []
  if (!detailRow.value || pts.length === 0) {
    if (trendChart) trendChart.clear()
    return
  }
  nextTick(() => {
    if (!trendChartEl.value) return
    if (!trendChart) {
      trendChart = echarts.init(trendChartEl.value)
    }
    const isLight = document.documentElement.getAttribute('data-theme') !== 'dark' && document.documentElement.getAttribute('data-theme') !== 'cyber'
    const axisColor = isLight ? '#64748b' : '#94a3b8'
    const splitColor = isLight ? 'rgba(59,130,246,0.12)' : 'rgba(56,189,248,0.1)'
    const chartPts = pts.map(p => [trendTimeToDate(p[0]), Number(p[1])] as [Date, number])
    const threshold = detailRow.value?.threshold || 0
    trendChart.setOption({
      backgroundColor: 'transparent',
      tooltip: {
        trigger: 'axis',
        valueFormatter: (v: any) => formatNum(Number(v)),
      },
      grid: { left: 48, right: 16, top: 28, bottom: 28 },
      xAxis: {
        type: 'time',
        axisLabel: { color: axisColor, fontSize: 11 },
        axisLine: { lineStyle: { color: splitColor } },
        splitLine: { lineStyle: { color: splitColor } },
      },
      yAxis: {
        type: 'value',
        scale: true,
        axisLabel: { color: axisColor, fontSize: 11 },
        splitLine: { lineStyle: { color: splitColor } },
      },
      series: [
        {
          name: 'value',
          type: 'line',
          data: chartPts,
          showSymbol: chartPts.length <= 24,
          symbol: 'circle',
          symbolSize: 6,
          connectNulls: true,
          lineStyle: { color: '#3b82f6', width: 2 },
          itemStyle: { color: '#3b82f6' },
          areaStyle: { color: 'rgba(59,130,246,0.12)' },
          markLine: threshold ? {
            silent: true,
            symbol: 'none',
            data: [{ yAxis: threshold }],
            lineStyle: { color: '#ef4444', type: 'dashed', width: 1.5 },
            label: { formatter: 'threshold ' + formatNum(threshold), color: '#ef4444', fontSize: 11 },
          } : undefined,
        },
      ],
    })
  })
}

async function fetchData() {
  loading.value = true
  try {
    const params: any = {
      page: page.value, page_size: pageSize.value,
      include_masked: includeMasked.value ? 'true' : 'false',
    }
    if (filters.value.severity) params.severity = filters.value.severity
    if (filters.value.state) params.state = filters.value.state
    if (filters.value.datasource_id !== '') params.datasource_id = filters.value.datasource_id
    if (filters.value.keyword) params.keyword = filters.value.keyword
    if (timeRange.value && timeRange.value[0] && timeRange.value[1]) {
      params.from = timeRange.value[0].toISOString()
      params.to = timeRange.value[1].toISOString()
    }
    const res = await getAlertInstances(params)
    rows.value = res.data.items as AlertInstance[]
    total.value = res.data.total
    // 清理已不在列表中的选择
    const fps = new Set(rows.value.map(r => r.fingerprint))
    selection.value = selection.value.filter(s => fps.has(s.fingerprint))
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally { loading.value = false }
}

async function refreshStats() {
  try {
    const res = await getAlertStats()
    stats.value = res.data
    renderSourceTrend()
  } catch { /* ignore */ }
}

// ---------- 近 24 小时告警趋势（按来源） ----------
const srcTrendEl = ref<HTMLDivElement | null>(null)
let srcTrendChart: echarts.ECharts | null = null
const sourceTrendEmpty = ref(true)

const SOURCE_COLORS = ['#3b82f6', '#f59e0b', '#ef4444', '#10b981', '#8b5cf6', '#06b6d4', '#ec4899', '#84cc16', '#f97316', '#64748b']

function renderSourceTrend() {
  const buckets = stats.value?.trend_24h_by_source || []
  sourceTrendEmpty.value = buckets.length === 0
  if (buckets.length === 0) {
    if (srcTrendChart) { srcTrendChart.clear() }
    return
  }
  // 生成完整的 24 小时横轴
  const hours: string[] = []
  const now = new Date()
  now.setMinutes(0, 0, 0)
  for (let i = 23; i >= 0; i--) {
    const d = new Date(now.getTime() - i * 3600 * 1000)
    const p = (n: number) => String(n).padStart(2, '0')
    hours.push(`${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:00`)
  }
  // 按来源聚合
  const sourceSet = new Set(buckets.map(b => b.Source || '本地'))
  const sources = Array.from(sourceSet)
  const series = sources.map((src, idx) => {
    const m = new Map(buckets.filter(b => (b.Source || '本地') === src).map(b => [b.Hour, b.Count]))
    return {
      name: src,
      type: 'line' as const,
      smooth: true,
      symbol: 'circle',
      symbolSize: 5,
      showSymbol: false,
      lineStyle: { width: 2 },
      itemStyle: { color: SOURCE_COLORS[idx % SOURCE_COLORS.length] },
      areaStyle: idx === 0 ? { opacity: 0.08 } : undefined,
      data: hours.map(h => m.get(h) || 0),
    }
  })
  nextTick(() => {
    if (!srcTrendEl.value) return
    if (!srcTrendChart) srcTrendChart = echarts.init(srcTrendEl.value)
    srcTrendChart.setOption({
      grid: { left: 44, right: 16, top: 34, bottom: 28 },
      tooltip: { trigger: 'axis' },
      legend: {
        top: 0,
        type: 'scroll',
        textStyle: { color: getCssVar('--text-secondary') || '#64748b', fontSize: 11 },
        itemWidth: 14, itemHeight: 8,
      },
      xAxis: {
        type: 'category',
        data: hours.map(h => h.slice(5, 13)),
        axisLine: { lineStyle: { color: getCssVar('--border') || '#334155' } },
        axisLabel: { color: getCssVar('--text-tertiary') || '#94a3b8', fontSize: 10 },
        axisTick: { show: false },
        boundaryGap: false,
      },
      yAxis: {
        type: 'value',
        minInterval: 1,
        axisLabel: { color: getCssVar('--text-tertiary') || '#94a3b8', fontSize: 10 },
        splitLine: { lineStyle: { color: getCssVar('--border') || '#334155', opacity: 0.5 } },
      },
      color: SOURCE_COLORS,
      series,
    }, { notMerge: true })
  })
}

async function refreshEvaluator() {
  try {
    const res = await getAlertEvaluatorStatus()
    evaluator.value = res.data
  } catch { evaluator.value = { running: false } }
}

async function loadTrends() {
  const fps = rows.value.map(r => r.fingerprint)
  if (fps.length === 0) { trends.value = {}; return }
  try {
    const res = await getAlertInstancesTrend(fps, 60)
    trends.value = res.data
  } catch { /* trend is optional */ }
}

async function loadMeta() {
  try {
    const [dsRes, rulesRes, chRes] = await Promise.all([getAllDataSources(), getAlertRules({ page_size: 500 }), getAllNotifications()])
    datasources.value = dsRes.data
    rules.value = rulesRes.data.items
    allChannels.value = chRes.data || []
  } catch { /* ignore */ }
}

function dsName(id: number | undefined) {
  if (!id) return '-'
  return datasources.value.find(d => d.id === id)?.name || `#${id}`
}
// 从外部告警规则名 "[源名] 规则名" 解析源名
function parseSourceName(ruleName: string): string {
  const m = ruleName.match(/^\[([^\]]+)\]\s*(.*)$/)
  return m ? m[1] : ''
}
// 数据源展示兜底链：datasource_name → 数据源表 → 外部告警源 → labels/规则名解析的源名
function dsDisplay(row: AlertInstance) {
  if (row.datasource_name) return row.datasource_name
  const viaDs = dsName(row.datasource_id)
  if (viaDs && viaDs !== '-') return viaDs
  if (row.external_source_name) return row.external_source_name
  const src = parseSourceName(row.rule_name || '')
  return src || '-'
}
function ruleName(id: number) {
  return rules.value.find(r => r.id === id)?.name || `规则 #${id}`
}

function severityLabel(s: string) {
  return { critical: '严重', warning: '警告', info: '提醒' }[s] || s
}
function severityStyle(s: string) {
  const map: Record<string, any> = {
    critical: { background: '#ef4444', color: '#fff', border: 'none' },
    warning: { background: '#f59e0b', color: '#fff', border: 'none' },
    info: { background: '#3b82f6', color: '#fff', border: 'none' },
  }
  return map[s] || map.warning
}
function stateStyle(s: string) {
  const map: Record<string, any> = {
    firing: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    pending: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
    resolved: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
  }
  return map[s] || map.firing
}
function eventStyle(t: string) {
  const m: Record<string, any> = {
    firing: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    resolved: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    pending: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
  }
  return m[t] || { background: 'rgba(148,163,184,0.15)', color: '#64748b', border: 'none' }
}
function formatNum(v: number | null | undefined) {
  if (v === null || v === undefined || isNaN(Number(v))) return '-'
  if (Math.abs(v) >= 100) return Number(v).toFixed(2)
  return Number(v).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}
function formatTime(t: string | null | undefined) {
  if (!t) return '-'
  const d = dayjs(t)
  if (!d.isValid()) return '-'
  if (d.year() < 2000) return '-' // 零值时间（0001-01-01/1970）不展示
  return d.format('YYYY-MM-DD HH:mm:ss')
}
function notifyStatusLabel(s: string) {
  return ({ success: '成功', failed: '失败', throttled: '限流' } as Record<string, string>)[s] || s
}
function notifyStatusStyle(s: string) {
  const m: Record<string, any> = {
    success: { background: 'rgba(16,185,129,0.15)', color: '#10b981', border: 'none' },
    failed: { background: 'rgba(239,68,68,0.15)', color: '#ef4444', border: 'none' },
    throttled: { background: 'rgba(245,158,11,0.15)', color: '#f59e0b', border: 'none' },
  }
  return m[s] || { background: 'rgba(148,163,184,0.15)', color: '#94a3b8', border: 'none' }
}
function channelName(id: number | undefined) {
  if (!id) return ''
  return allChannels.value.find(c => c.id === id)?.name || `#${id}`
}

// 打开详情：自动标记已读（红点清零）
async function openDetail(row: AlertInstance) {
  detailRow.value = row
  detailDrawer.value = true
  historyRows.value = []
  notifyLogsRows.value = []
  try {
    const res = await getAlertInstance(row.fingerprint)
    if (res.data.instance) {
      detailRow.value = { ...row, ...res.data.instance }
    }
    historyRows.value = res.data.history || []
    notifyLogsRows.value = res.data.notify_logs || []
  } catch { /* ignore */ }
  // 标记已读（无论详情接口是否成功）
  if ((row.unread_count || 0) > 0) {
    markRead(row)
  }
  detailTrendPts.value = []
  loadDetailTrend()
}

// 标记单条已读
async function markRead(row: AlertInstance) {
  if ((row.unread_count || 0) <= 0) return
  try {
    await markAlertInstanceRead(row.fingerprint)
    row.unread_count = 0
    const d = detailRow.value
    if (d && d.fingerprint === row.fingerprint) d.unread_count = 0
  } catch { /* ignore */ }
}

// ===== 批量操作 =====
async function batchRead() {
  const fps = selection.value.map(s => s.fingerprint)
  if (!fps.length) return
  try {
    await batchAlertInstances('read', fps)
    rows.value.forEach(r => { if (fps.includes(r.fingerprint)) r.unread_count = 0 })
    clearSelection()
    ElMessage.success(`已标记 ${fps.length} 条为已读`)
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

async function batchResolve() {
  const fps = selection.value.map(s => s.fingerprint)
  if (!fps.length) return
  try {
    await ElMessageBox.confirm(`确定批量结束 ${fps.length} 条活跃告警？实例将标记为已恢复，历史记录保留。`, '批量结束告警', {
      type: 'warning', cancelButtonText: '取消', confirmButtonText: '结束',
    })
  } catch { return }
  try {
    const res = await batchAlertInstances('resolve', fps)
    ElMessage.success(`结束成功 ${res.data.done} 条${res.data.failed ? `，失败 ${res.data.failed} 条` : ''}`)
    clearSelection()
    await fetchData()
    await refreshStats()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

async function batchDelete() {
  const fps = selection.value.map(s => s.fingerprint)
  if (!fps.length) return
  try {
    await ElMessageBox.confirm(
      `确定永久删除 ${fps.length} 条告警实例？此操作不可恢复，历史记录将保留。`,
      '批量删除告警', { type: 'error', cancelButtonText: '取消', confirmButtonText: '删除' }
    )
  } catch { return }
  try {
    const res = await batchAlertInstances('delete', fps)
    ElMessage.success(`删除成功 ${res.data.done} 条${res.data.failed ? `，失败 ${res.data.failed} 条` : ''}`)
    clearSelection()
    await fetchData()
    await refreshStats()
  } catch (e: any) {
    ElMessage.error(e.message)
  }
}

function batchSilence() {
  const fps = selection.value.map(s => s.fingerprint)
  if (!fps.length) return
  silenceBatchMode.value = true
  silenceFps.value = fps
  silenceForm.value = { comment: '', durationMin: 60, matchers: [] }
  silenceDialog.value = true
}

async function doResolve(row: AlertInstance) {
  try {
    await ElMessageBox.confirm(
      `确定手动结束告警「${row.fingerprint.slice(0, 12)}」？实例将标记为已恢复，历史记录保留。`,
      '结束告警', { type: 'warning', cancelButtonText: '取消', confirmButtonText: '结束' }
    )
  } catch {
    return
  }
  resolvingFp.value = row.fingerprint
  try {
    await resolveAlertInstance(row.fingerprint)
    ElMessage.success('告警已结束')
    row.state = 'resolved'
    await fetchData()
    await refreshStats()
  } catch (e: any) {
    ElMessage.error(e.message || '结束失败')
  } finally {
    resolvingFp.value = null
  }
}

function openSilence(row: AlertInstance) {
  detailRow.value = row
  silenceBatchMode.value = false
  silenceFps.value = [row.fingerprint]
  silenceForm.value = {
    comment: '',
    durationMin: 60,
    matchers: buildDefaultMatchers(row),
  }
  silenceDialog.value = true
}

function buildDefaultMatchers(row: AlertInstance) {
  const ms: Array<{ name: string; op: string; value: string }> = []
  const labels = row.labels || {}
  if (labels.alertname) ms.push({ name: 'alertname', op: '=', value: labels.alertname })
  for (const k of ['instance', 'job', 'datasource_name']) {
    if (labels[k]) ms.push({ name: k, op: '=', value: labels[k] })
  }
  if (ms.length === 0 && Object.keys(labels).length > 0) {
    const k = Object.keys(labels)[0]
    ms.push({ name: k, op: '=', value: labels[k] })
  }
  return ms
}

async function handleClearAll() {
  try {
    await ElMessageBox.confirm('确定要清空所有实时告警吗？此操作不可恢复。', '确认清空', {
      confirmButtonText: '确认清空',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await clearAlertInstances()
    ElMessage.success('已清空所有实时告警')
    await fetchData()
    await refreshStats()
  } catch { /* cancelled or error */ }
}

async function handleSilenceSubmit() {
  if (!silenceForm.value.comment.trim()) {
    ElMessage.warning('请填写静默原因')
    return
  }
  silenceSaving.value = true
  try {
    const minutes = silenceForm.value.durationMin
    if (silenceBatchMode.value) {
      // 批量静默：按每条告警 labels 生成 matcher
      const res = await batchAlertInstances('silence', silenceFps.value, {
        silence_minutes: minutes,
        comment: silenceForm.value.comment,
      })
      if (res.data.failed > 0 && res.data.done === 0) {
        ElMessage.error((res.data.errors || ['批量静默失败']).join('；'))
      } else {
        ElMessage.success(`静默成功 ${res.data.done} 条${res.data.failed ? `，失败 ${res.data.failed} 条` : ''}`)
      }
    } else {
      const validMatchers = silenceForm.value.matchers.filter(m => m.name && m.value)
      if (validMatchers.length === 0) {
        ElMessage.warning('至少需要一条匹配条件')
        return
      }
      const now = new Date()
      const ends = new Date(now.getTime() + minutes * 60 * 1000)
      await createAlertSilence({
        comment: silenceForm.value.comment,
        matchers_json: JSON.stringify(validMatchers),
        starts_at: now.toISOString(),
        ends_at: ends.toISOString(),
        enabled: true,
      })
      ElMessage.success('静默创建成功，下一轮调度后生效')
    }
    silenceDialog.value = false
    clearSelection()
    ElNotification({
      title: '静默已创建',
      message: '点击跳转到静默规则管理',
      type: 'success',
      duration: 6000,
      onClick: () => router.push('/alert-silences'),
    })
    fetchData()
  } catch (e: any) {
    ElMessage.error(e.message)
  } finally {
    silenceSaving.value = false
  }
}

// ===== 自动刷新定时器 =====
let timer: number | null = null
function startTimer() {
  stopTimer()
  const sec = Math.max(5, Math.min(600, refreshSec.value || 30))
  timer = window.setInterval(async () => {
    await fetchData()
    await loadTrends()
    refreshEvaluator()
  }, sec * 1000)
}
function stopTimer() {
  if (timer !== null) {
    window.clearInterval(timer)
    timer = null
  }
}
function restartTimer() {
  startTimer()
}

onMounted(async () => {
  initViewMode()
  initVisibleColumns()
  await loadMeta()
  await Promise.all([fetchData(), refreshStats()])
  await loadTrends()
  await refreshEvaluator()
  startTimer()
})
onBeforeUnmount(() => {
  stopTimer()
  if (trendChart) {
    trendChart.dispose()
    trendChart = null
  }
  if (srcTrendChart) {
    srcTrendChart.dispose()
    srcTrendChart = null
  }
})
</script>

<style scoped>
.stats-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.stat-card {
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  transition: all 0.3s ease;
}
.stat-label {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-bottom: 6px;
}
.stat-value {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
}
.stat-extra {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-top: 4px;
}
.stat-critical .stat-value { color: #ef4444; }
.stat-warning .stat-value { color: #f59e0b; }
.stat-info .stat-value { color: #3b82f6; }
.stat-firing .stat-value { color: #ef4444; }
.stat-pending .stat-value { color: #f59e0b; }
.stat-resolved .stat-value { color: #10b981; }
.stat-unread .stat-value { color: #ef4444; }
.stat-unread .stat-value.zero { color: var(--text-tertiary); }

.refresh-ctl {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-tertiary);
  white-space: nowrap;
}
.refresh-ctl .el-icon { color: var(--cyan); }

.batch-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 24px;
  background: var(--cyan-dim);
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
.batch-info { color: var(--text-secondary); margin-right: 6px; }
.batch-info b { color: var(--cyan); font-size: 15px; }

.unread-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  border-radius: 10px;
  background: #ef4444;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
  box-shadow: 0 0 8px rgba(239, 68, 68, 0.5);
}
.unread-none { color: var(--text-tertiary); font-size: 12px; }

.ds-name { color: var(--text-secondary); font-size: 13px; }
.val-cyan { color: var(--cyan); font-weight: 600; font-family: 'SF Mono', Monaco, monospace; font-size: 13px; }
.val-dim { color: var(--text-tertiary); font-family: 'SF Mono', Monaco, monospace; font-size: 12px; }
.firing-count { color: var(--amber); font-weight: 600; font-size: 13px; }
.time-cell { font-size: 12px; color: var(--text-tertiary); font-family: 'SF Mono', Monaco, monospace; }
.label-pill { margin: 0; }
.tag-silence { background: rgba(96, 165, 250, 0.15); color: #60a5fa; border: none; }
.tag-inhibit { background: rgba(251, 191, 36, 0.15); color: #fbbf24; border: none; }

.event-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 0;
}
.event-row {
  display: flex;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  transition: all .15s ease;
}
.event-row:hover {
  border-color: rgba(99, 102, 241, 0.4);
  box-shadow: 0 2px 10px rgba(0,0,0,0.04);
}
.event-bar {
  width: 4px;
  flex-shrink: 0;
}
.event-bar.firing { background: #ef4444; }
.event-bar.pending { background: #f59e0b; }
.event-bar.resolved { background: #10b981; }
.event-body {
  flex: 1;
  display: flex;
  align-items: center;
  padding: 14px 16px;
  gap: 8px;
  min-width: 0;
}
.event-main {
  flex: 1;
  min-width: 0;
}
.event-title-line {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.event-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  min-width: 0;
}
.event-ds {
  font-size: 13px;
  color: var(--text-secondary);
  font-weight: 500;
}
.event-divider {
  color: var(--text-tertiary);
  font-size: 12px;
  margin: 0 2px;
}
.event-rule {
  font-size: 14px;
  color: var(--text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.event-times {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-shrink: 0;
}
.time-item {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
}
.time-label {
  font-size: 11px;
  color: var(--text-tertiary);
}
.time-value {
  font-size: 12px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
}
.event-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.meta-val {
  font-size: 12px;
  color: var(--cyan);
  font-family: 'SF Mono', Monaco, monospace;
  margin-right: 4px;
}
.meta-threshold {
  color: var(--text-tertiary);
  margin-left: 4px;
}
.meta-count {
  font-size: 12px;
  color: var(--amber);
  font-weight: 600;
}
.event-label-tag {
  background: rgba(99, 102, 241, 0.08);
  color: #818cf8;
  border: none;
  font-family: 'SF Mono', Monaco, monospace;
}
.event-actions {
  flex-shrink: 0;
}

.card-trend {
  margin-top: 8px;
  height: 36px;
  max-width: 220px;
}

.pager {
  display: flex;
  justify-content: flex-end;
  padding: 12px 4px;
}

.detail {
  font-size: 14px;
}
.detail-section {
  margin-bottom: 20px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border);
}
.detail-row {
  display: flex;
  align-items: center;
  margin-bottom: 10px;
  line-height: 1.5;
}
.detail-row .k {
  width: 116px;
  color: var(--text-tertiary);
  font-size: 13px;
  flex-shrink: 0;
}
.detail-row .v {
  color: var(--text-primary);
  font-size: 14px;
  word-break: break-all;
}
.detail-row .v.strong { font-weight: 600; }
.detail-row .v.mono { font-family: 'SF Mono', Monaco, monospace; font-size: 14px; }
.detail-row .hint { color: var(--text-tertiary); font-size: 12px; margin-left: 8px; }
.section-title {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-bottom: 8px;
  text-transform: uppercase;
  letter-spacing: 1px;
  font-weight: 600;
}
.kv-block {
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-secondary);
  max-height: 240px;
  overflow: auto;
  font-family: 'SF Mono', Monaco, monospace;
}
.empty-hint {
  color: var(--text-tertiary);
  font-size: 13px;
  padding: 8px 0;
}
.src-trend-card { margin-bottom: 16px; }
.src-trend-chart {
  width: 100%;
  height: 260px;
}
.src-trend-empty {
  font-size: 12px;
  color: var(--text-tertiary);
}
.trend-chart {
  width: 100%;
  height: 200px;
}
.trend-empty {
  color: var(--text-tertiary);
  font-size: 12px;
  padding: 24px 0;
  text-align: center;
  background: var(--bg-elevated);
  border-radius: 8px;
}
.hist-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 7px 0;
  font-size: 13px;
  border-bottom: 1px dashed var(--border);
}
.hist-row:last-child { border-bottom: none; }
.hist-row .hist-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
}
.hist-row .ts { color: var(--text-tertiary); font-size: 12px; min-width: 142px; }
.hist-row .hist-seq {
  color: var(--text-tertiary);
  font-size: 11px;
  font-family: 'SF Mono', Monaco, monospace;
  background: var(--bg-elevated);
  padding: 1px 6px;
  border-radius: 4px;
  min-width: 32px;
  text-align: center;
}
.hist-row .hist-metrics {
  display: grid;
  grid-template-columns: 96px 96px;
  gap: 12px;
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 12px;
  flex-shrink: 0;
}
.hist-row .vv { color: var(--cyan); text-align: right; white-space: nowrap; }
.hist-row .vv-dim { color: var(--text-tertiary); text-align: right; white-space: nowrap; }
.notify-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 6px 0;
  font-size: 13px;
  border-bottom: 1px solid var(--border);
}
.notify-row .chan { color: var(--text-secondary); font-weight: 500; }
.notify-row .cname { color: var(--text-tertiary); font-size: 12px; }
.notify-row .cnt { color: var(--cyan); margin-left: auto; font-size: 12px; }
.notify-row .ts { color: var(--text-tertiary); margin-left: 8px; font-size: 12px; }
.notify-row .err {
  width: 100%;
  color: #ef4444;
  font-size: 11px;
  padding-left: 4px;
  margin-top: 2px;
  word-break: break-all;
}
.matcher-hint {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-bottom: 6px;
  line-height: 1.6;
}
</style>
