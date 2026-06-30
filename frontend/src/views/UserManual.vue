<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Reading /></el-icon> 用户使用手册</h2>
      <p>按场景查找配置方法</p>
    </div>

    <div class="toc-bar">
      <el-tag v-for="s in sections" :key="s.id" size="small" style="cursor:pointer;margin:2px;"
               :type="openSet.has(s.id) ? 'primary' : 'info'"
               @click="scrollTo(s.id)">{{ s.label }}</el-tag>
    </div>

    <el-collapse v-model="openNames" :accordion="false">
      <el-collapse-item name="sec-rules" id="sec-rules">
        <template #title><b>🔔 告警规则 — 配置案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">案例一：监控节点内存使用率（指标模式）</div>
            <div class="step-list">
              <div><b>①</b> 进入「告警管理 → 告警规则」→ 点击 <el-tag size="small" type="primary">新建</el-tag></div>
              <div><b>②</b> 规则来源选 <b>"指标"</b>，搜索指标名 <code>节点内存使用率</code> 选中</div>
              <div><b>③</b> 阈值填 <code>90</code>，条件选 <b>&gt;</b>，级别选 <b>critical</b></div>
              <div><b>④</b> 持续等待 <code>3m</code>（值持续超过 90% 达 3 分钟才触发告警，避免瞬间毛刺）</div>
              <div><b>⑤</b> 告警原因填 <code>节点内存不足，影响业务稳定性</code></div>
              <div><b>⑥</b> 点击 <el-tag size="small" type="success">预览</el-tag> 确认查询正确，保存</div>
            </div>
          </div>
          <div class="doc-case">
            <div class="case-title">案例二：监控节点磁盘使用率（自定义 PromQL）</div>
            <div class="step-list">
              <div><b>①</b> 新建规则 → 规则来源选 <b>"自定义"</b></div>
              <div><b>②</b> PromQL 输入：<code>(1 - (node_filesystem_avail_bytes{fstype=~"ext4|xfs"} / node_filesystem_size_bytes{fstype=~"ext4|xfs"})) * 100</code></div>
              <div><b>③</b> 阈值 <code>85</code>，条件 <b>&gt;</b>，级别选 <b>warning</b></div>
              <div><b>④</b> 持续等待 <code>5m</code>，避免磁盘 IO 抖动误报</div>
              <div><b>⑤</b> 告警原因填 <code>磁盘空间占用过高，需及时清理或扩容</code></div>
            </div>
          </div>
          <div class="doc-case">
            <div class="case-title">案例三：节点离线检测（up == 0）</div>
            <div class="step-list">
              <div><b>①</b> 新建规则 → 规则来源选 <b>"自定义"</b></div>
              <div><b>②</b> PromQL：<code>up == 0</code></div>
              <div><b>③</b> 阈值 <code>0</code>，条件 <b>=</b>，级别选 <b>critical</b></div>
              <div><b>④</b> 持续等待 <code>1m</code>（1 分钟内探活失败即告警）</div>
              <div><b>⑤</b> 告警原因填 <code>节点离线或 exporter 进程异常</code></div>
            </div>
          </div>
        </div>
      </el-collapse-item>

      <el-collapse-item name="sec-notify" id="sec-notify">
        <template #title><b>📢 通知渠道 — 配置案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">案例一：接入企业微信群机器人</div>
            <div class="step-list">
              <div><b>①</b> 在企业微信群 → 群设置 → 群机器人 → 添加机器人，复制 Webhook URL</div>
              <div><b>②</b> 进入「通知渠道」→ <el-tag size="small" type="primary">新增渠道</el-tag>，类型选 <b>企业微信机器人</b></div>
              <div><b>③</b> 名称填 <code>运维告警群</code>，Webhook URL 粘贴刚才复制的地址</div>
              <div><b>④</b> 保存后，在「通知路由」中把该渠道绑定到路由即可</div>
            </div>
          </div>
          <div class="doc-case">
            <div class="case-title">案例二：自定义消息模板（简洁模式 + 文本模板）</div>
            <div class="step-list">
              <div><b>①</b> 编辑通知渠道 → 切到 <b>消息模板</b> tab → 选 <b>简洁</b> 风格</div>
              <div><b>②</b> 勾选 <b>"自定义每项条目的文本格式"</b>，在文本框填写：</div>
              <div class="code-block"><pre>🔴 {host} · {value} > {threshold}
{content}
🕐 {time}
{detail}</pre></div>
              <div><b>③</b> 每个 <code>{字段}</code> 悬停可看说明，点击可直接插入光标位置</div>
              <div><b>④</b> 点 <el-tag size="small">查看预览</el-tag> 测试效果</div>
            </div>
            <div class="case-tip">💡 还可用 <b>表格</b> 风格做规整排列，或用 <b>卡片</b> 风格做每条独立卡片</div>
          </div>
        </div>
      </el-collapse-item>

      <el-collapse-item name="sec-routes" id="sec-routes">
        <template #title><b>📮 通知路由 — 配置案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">案例一：critical 发企业微信，warning 发邮件</div>
            <div class="step-list">
              <div><b>①</b> 准备两个渠道：企微机器人（ID=3）和邮件（ID=4）</div>
              <div><b>②</b> 新建路由 <b>"严重告警路由"</b>：匹配条件 <code>[{"name":"severity","op":"=","value":"critical"}]</code>，通知渠道选企微</div>
              <div><b>③</b> 新建路由 <b>"普通告警路由"</b>：匹配条件 <code>[{"name":"severity","op":"=","value":"warning"}]</code>，通知渠道选邮件</div>
              <div><b>④</b> 两个路由都设置 <b>不继续匹配</b>，避免重复通知</div>
            </div>
          </div>
          <div class="doc-case">
            <div class="case-title">案例二：减少重复告警（分组聚合 + 限频）</div>
            <div class="step-list">
              <div><b>①</b> 编辑路由 → <b>group_by</b> 填 <code>["alertname","datasource_id"]</code>（去掉 instance）</div>
              <div><b>②</b> 设置 <b>首次等待</b> <code>30s</code>（30 秒内同一规则的告警聚合为一条通知）</div>
              <div><b>③</b> 设置 <b>重复间隔</b> <code>4h</code>（同一个告警组一天最多重复通知 6 次）</div>
              <div><b>④</b> 设置 <b>限流窗口</b> <code>10m</code>（10 分钟内相同内容不再发送）</div>
            </div>
            <div class="case-tip">💡 效果：同一规则下 3 台主机的磁盘告警合并为一条通知，而不是 3 条独立消息</div>
          </div>
        </div>
      </el-collapse-item>

      <el-collapse-item name="sec-inhibits" id="sec-inhibits">
        <template #title><b>🚫 抑制规则 — 配置案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">案例一：critical 告警时屏蔽同节点的 warning</div>
            <div class="guide-config">
              <div><span class="lbl">源条件：</span><code>[{"name":"severity","op":"=","value":"critical"}]</code></div>
              <div><span class="lbl">目标条件：</span><code>[{"name":"severity","op":"=","value":"warning"}]</code></div>
              <div><span class="lbl">equal_labels：</span><code>["instance"]</code></div>
            </div>
            <p class="doc-effect">✓ 节点 A 的 CPU critical 告警触发时，该节点的磁盘 warning 被抑制，不重复通知</p>
          </div>
          <div class="doc-case">
            <div class="case-title">案例二：节点离线时抑制该节点所有其他告警</div>
            <div class="guide-config">
              <div><span class="lbl">源条件：</span><code>[{"name":"alertname","op":"=","value":"节点离线"}]</code></div>
              <div><span class="lbl">目标条件：</span><code>[{"name":"severity","op":"!=","value":"critical"}]</code></div>
              <div><span class="lbl">equal_labels：</span><code>["instance"]</code></div>
            </div>
            <p class="doc-effect">✓ 节点 A 离线后，该节点的磁盘/负载等非 critical 告警全部被抑制，避免告警风暴</p>
          </div>
          <div class="doc-case">
            <div class="case-title">案例三：集群 APIServer 挂了时抑制节点级 warning</div>
            <div class="guide-config">
              <div><span class="lbl">源条件：</span><code>[{"name":"alertname","op":"=","value":"APIServerDown"}]</code></div>
              <div><span class="lbl">目标条件：</span><code>[{"name":"severity","op":"=","value":"warning"}]</code></div>
              <div><span class="lbl">equal_labels：</span><code>["cluster"]</code></div>
            </div>
            <p class="doc-effect">✓ 集群 prod 的 APIServer 挂了，该集群下所有 warning 节点告警被抑制，只追根因</p>
          </div>
          <div class="doc-case">
            <div class="case-title">案例四：生产环境 critical 时抑制同节点的 CPU warning</div>
            <div class="guide-config">
              <div><span class="lbl">源条件：</span><code>[{"name":"severity","op":"=","value":"critical"},{"name":"env","op":"=","value":"prod"}]</code></div>
              <div><span class="lbl">目标条件：</span><code>[{"name":"alertname","op":"=","value":"CPU使用率过高"}]</code></div>
              <div><span class="lbl">equal_labels：</span><code>["instance"]</code></div>
            </div>
            <p class="doc-effect">✓ 生产节点 CPU 已经是 critical 了，再发 CPU warning 无意义，抑制掉</p>
          </div>
        </div>
      </el-collapse-item>

      <el-collapse-item name="sec-silence" id="sec-silence">
        <template #title><b>🔇 告警静默 — 配置案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">案例一：节点计划维护时静默该节点告警</div>
            <div class="step-list">
              <div><b>①</b> 在「实时告警」列表找到该节点告警 → 点击 <el-tag size="small">静默</el-tag></div>
              <div><b>②</b> 确认自动生成的匹配条件：<code>alertname=...</code>、<code>instance=web-01</code></div>
              <div><b>③</b> 持续时间选 <code>4h</code>（覆盖维护窗口）</div>
              <div><b>④</b> 填写静默原因 <code>节点计划内重启维护</code>，提交</div>
            </div>
          </div>
          <div class="doc-case">
            <div class="case-title">案例二：批量静默某类告警</div>
            <div class="step-list">
              <div><b>①</b> 在「告警静默」页面点击 <el-tag size="small" type="primary">新建</el-tag></div>
              <div><b>②</b> 匹配条件填：<code>[{"name":"alertname","op":"=","value":"磁盘利用率高于80"}]</code></div>
              <div><b>③</b> 设置持续 <code>24h</code>，原因 <code>已知磁盘问题，等次日处理</code></div>
              <div><b>④</b> 也可以用正则：<code>[{"name":"instance","op":"=~","value":"web-\\d+"}]</code> 静默所有 web 节点</div>
            </div>
          </div>
        </div>
      </el-collapse-item>

      <el-collapse-item name="sec-history" id="sec-history">
        <template #title><b>📋 告警历史 — 使用案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">排查一个告警的完整生命周期</div>
            <div class="step-list">
              <div><b>①</b> 进入「告警管理 → 告警历史」，搜索某条规则名称或数据源</div>
              <div><b>②</b> 点击展开该规则的时间线，能看到：<b>触发→通知→恢复</b> 全链路</div>
              <div><b>③</b> 展开通知条目可看到实际发送的内容和发送结果</div>
              <div><b>④</b> 支持按时间范围过滤，定位特定时间段的问题</div>
            </div>
          </div>
        </div>
      </el-collapse-item>

      <el-collapse-item name="sec-trend" id="sec-trend">
        <template #title><b>📈 实时告警趋势图 — 使用案例</b></template>
        <div class="doc-body">
          <div class="doc-case">
            <div class="case-title">通过趋势图判断告警是否在持续恶化</div>
            <div class="step-list">
              <div><b>①</b> 进入「实时告警」列表，找到 <b>趋势</b> 列</div>
              <div><b>②</b> 曲线从左到右展示最近 60 分钟的数值变化（每 15s 一个点）</div>
              <div><b>③</b> 曲线平缓 → 指标稳定；曲线陡升 → 正在急剧恶化</div>
              <div><b>④</b> 结合 value 值一起看：当前值高 + 曲线向上 → 需马上处理</div>
            </div>
          </div>
        </div>
      </el-collapse-item>
    </el-collapse>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick } from 'vue'
import { Reading } from '@element-plus/icons-vue'

const sections = [
  { id: 'sec-rules', label: '告警规则' },
  { id: 'sec-notify', label: '通知渠道' },
  { id: 'sec-routes', label: '通知路由' },
  { id: 'sec-inhibits', label: '抑制规则' },
  { id: 'sec-silence', label: '告警静默' },
  { id: 'sec-history', label: '告警历史' },
  { id: 'sec-trend', label: '趋势图' },
]

const openNames = ref<string[]>([])
const openSet = computed(() => new Set(openNames.value))

function scrollTo(id: string) {
  if (!openNames.value.includes(id)) {
    openNames.value = [...openNames.value, id]
  }
  nextTick(() => {
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}
</script>

<style scoped>
.toc-bar {
  display: flex; flex-wrap: wrap; gap: 4px;
  padding: 12px 16px; margin-bottom: 16px;
  background: var(--bg-elevated); border-radius: 12px;
  border: 1px solid var(--border);
}
.doc-body { font-size: 13px; line-height: 1.8; color: var(--text-secondary); padding: 4px 0; }
.doc-case { margin: 12px 0; padding: 12px 14px; background: rgba(255,255,255,0.03); border-radius: 8px; border: 1px solid var(--border); }
.case-title { font-weight: 600; color: var(--text-primary); margin-bottom: 8px; font-size: 14px; }
.step-list { line-height: 2.2; font-size: 13px; }
.step-list b { color: var(--cyan); }
.code-block {
  background: rgba(0,0,0,0.3); border-radius: 6px; padding: 8px 12px;
  margin: 6px 0; font-family: 'SF Mono', Monaco, monospace; font-size: 12px;
}
.code-block pre { margin: 0; color: var(--cyan); white-space: pre-wrap; }
.case-tip { color: var(--amber); font-size: 12px; margin-top: 8px; }
.guide-config {
  background: rgba(0,0,0,0.2); border-radius: 6px; padding: 8px 14px;
  margin: 6px 0; font-size: 12px; line-height: 2;
}
.guide-config .lbl { color: var(--text-tertiary); display: inline-block; width: 80px; }
.guide-config code { color: var(--cyan); }
.doc-effect { color: var(--success); font-size: 12px; margin: 4px 0; padding-left: 12px; border-left: 3px solid var(--success); }
</style>