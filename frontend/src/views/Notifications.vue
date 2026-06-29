<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Bell /></el-icon> 通知渠道</h2>
      <p>配置告警通知渠道，支持企业微信、钉钉、飞书、邮件、Webhook</p>
    </div>

    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 渠道列表</h3>
        <div style="display: flex; gap: 8px; align-items: center;">
          <el-input v-model="keyword" placeholder="搜索名称" clearable style="width: 200px;" @keyup.enter="fetchData" @clear="fetchData" />
          <el-select v-model="typeFilter" placeholder="类型" clearable style="width: 140px;" @change="fetchData">
            <el-option label="企业微信机器人" value="wechat_work" />
            <el-option label="企业微信应用" value="wechat_app" />
            <el-option label="钉钉机器人" value="dingtalk" />
            <el-option label="飞书机器人" value="feishu" />
            <el-option label="邮件" value="email" />
            <el-option label="Webhook" value="webhook" />
          </el-select>
          <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新增渠道</el-button>
        </div>
      </div>
      <el-table :data="channels" v-loading="loading" stripe>
        <el-table-column type="index" label="#" width="56" />
        <el-table-column label="类型" width="130">
          <template #default="{ row }">
            <el-tag :style="{ background: channelBg(row.channel_type), color: channelColor(row.channel_type), border: 'none' }">
              {{ channelLabel(row.channel_type) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="180">
          <template #default="{ row }">
            <span style="font-weight: 600; color: var(--text-primary);">{{ row.name }}</span>
            <el-tag
              v-if="hasCustomTemplate(row)"
              size="small"
              style="margin-left:8px;background:rgba(99,102,241,0.12);color:#818cf8;border:none;"
              title="此通道使用了自定义消息模板">
              <el-icon style="margin-right:2px;"><EditPen /></el-icon> 自定义模板
            </el-tag>
            <el-tag
              v-else-if="hasTemplateConfig(row)"
              size="small"
              style="margin-left:8px;background:rgba(16,185,129,0.10);color:#10b981;border:none;"
              :title="'模板风格: ' + templateStyle(row)">
              {{ templateStyle(row) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90" align="center">
          <template #default="{ row }">
            <el-switch v-model="row.enabled" @change="toggleEnabled(row)" />
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">{{ dayjs(row.created_at).format('MM-DD HH:mm') }}</template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text @click="openEdit(row)" style="color: var(--cyan);">编辑</el-button>
            <el-button size="small" text @click="handleTest(row)" style="color: var(--emerald);">测试</el-button>
            <el-button size="small" text @click="handleDelete(row)" style="color: var(--red);">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="total > pageSize" style="display: flex; justify-content: flex-end; margin-top: 16px; padding: 0 24px 16px;">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next"
          background
          @change="fetchData"
        />
      </div>
    </div>

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑通知渠道' : '新增通知渠道'" width="780" :close-on-click-modal="false" top="2vh">
      <el-tabs v-model="activeTab" type="border-card">
        <el-tab-pane label="通道配置" name="channel">
          <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <el-form-item label="渠道类型" prop="channel_type">
          <el-select v-model="form.channel_type" style="width: 100%" :disabled="!!editingId" @change="onTypeChange">
            <el-option label="企业微信机器人" value="wechat_work" />
            <el-option label="企业微信应用" value="wechat_app" />
            <el-option label="钉钉机器人" value="dingtalk" />
            <el-option label="飞书机器人" value="feishu" />
            <el-option label="邮件" value="email" />
            <el-option label="Webhook" value="webhook" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="例如：运维告警群" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>

        <template v-if="form.channel_type === 'wechat_work'">
          <el-form-item label="Webhook 地址" prop="config.webhook">
            <el-input v-model="cfg.webhook" placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..." />
          </el-form-item>
          <el-form-item label="代理地址">
            <el-input v-model="cfg.proxy_url" placeholder="可选，http://proxy:port" />
          </el-form-item>
        </template>

        <template v-if="form.channel_type === 'wechat_app'">
          <el-form-item label="企业 ID (CorpID)" prop="config.corpid">
            <el-input v-model="cfg.corpid" placeholder="ww..." />
          </el-form-item>
          <el-form-item label="AgentID" prop="config.agentid">
            <el-input v-model.number="cfg.agentid" placeholder="1000001" type="number" />
          </el-form-item>
          <el-form-item label="Secret" prop="config.secret">
            <el-input v-model="cfg.secret" placeholder="应用 Secret" type="password" show-password />
          </el-form-item>
          <el-form-item label="接收人">
            <el-input v-model="cfg.touser" placeholder="@all 或 企业微信 ID，多个用 | 分隔" />
          </el-form-item>
          <el-form-item label="代理地址">
            <el-input v-model="cfg.proxy_url" placeholder="可选，http://proxy:port" />
          </el-form-item>
        </template>

        <template v-if="form.channel_type === 'dingtalk'">
          <el-form-item label="Webhook 地址" prop="config.webhook">
            <el-input v-model="cfg.webhook" placeholder="https://oapi.dingtalk.com/robot/send?access_token=..." />
          </el-form-item>
          <el-form-item label="加签密钥">
            <el-input v-model="cfg.secret" placeholder="可选，安全设置中的签名密钥" type="password" show-password />
          </el-form-item>
        </template>

        <template v-if="form.channel_type === 'feishu'">
          <el-form-item label="Webhook 地址" prop="config.webhook">
            <el-input v-model="cfg.webhook" placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/..." />
          </el-form-item>
          <el-form-item label="加签密钥">
            <el-input v-model="cfg.secret" placeholder="可选，安全设置中的签名密钥" type="password" show-password />
          </el-form-item>
          <el-form-item label="验签开关">
            <el-switch v-model="cfg.verify_sign" />
          </el-form-item>
        </template>

        <template v-if="form.channel_type === 'webhook'">
          <el-form-item label="Webhook URL" prop="config.url">
            <el-input v-model="cfg.url" placeholder="https://hooks.example.com/webhook" />
          </el-form-item>
          <el-form-item label="请求方法">
            <el-select v-model="cfg.method" style="width:100%;">
              <el-option label="POST" value="POST" />
              <el-option label="PUT" value="PUT" />
              <el-option label="PATCH" value="PATCH" />
            </el-select>
          </el-form-item>
          <el-form-item label="请求头 (JSON)">
            <el-input v-model="cfg.headers" type="textarea" :rows="2" placeholder='{"Authorization":"Bearer xxx"}' />
          </el-form-item>
          <el-form-item label="消息模板">
            <el-input v-model="cfg.body_template" type="textarea" :rows="4" placeholder='可选，Go 模板语法。可用变量: {{.title}} {{.markdown}} {{.plain}} {{.html}}。不填则发送默认 JSON' />
          </el-form-item>
        </template>

        <template v-if="form.channel_type === 'email'">
          <el-form-item label="SMTP 服务器" prop="config.smtp_host">
            <el-input v-model="cfg.smtp_host" placeholder="smtp.example.com" />
          </el-form-item>
          <el-form-item label="SMTP 端口" prop="config.smtp_port">
            <el-input v-model.number="cfg.smtp_port" placeholder="465" type="number" />
          </el-form-item>
          <el-form-item label="加密方式">
            <el-select v-model="cfg.encryption" style="width: 100%;">
              <el-option label="SSL/TLS (端口 465)" value="ssl" />
              <el-option label="STARTTLS (端口 587)" value="starttls" />
              <el-option label="无" value="none" />
            </el-select>
          </el-form-item>
          <el-form-item label="SMTP 用户名">
            <el-input v-model="cfg.username" placeholder="可选，通常与发件人邮箱相同" />
          </el-form-item>
          <el-form-item label="发件人邮箱" prop="config.from">
            <el-input v-model="cfg.from" placeholder="alert@example.com" />
          </el-form-item>
          <el-form-item label="发件人密码" prop="config.password">
            <el-input v-model="cfg.password" placeholder="SMTP 密码或授权码" type="password" show-password />
          </el-form-item>
          <el-form-item label="收件人" prop="config.to">
            <el-input v-model="cfg.to" placeholder="多个邮箱用逗号分隔" type="textarea" :rows="2" />
          </el-form-item>
        </template>
      </el-form>
        </el-tab-pane>

        <el-tab-pane label="消息模板" name="template">
          <el-form :model="tpl" label-width="120px">
            <div style="display:flex;align-items:center;justify-content:space-between;padding:0 0 12px;margin-bottom:8px;border-bottom:1px dashed var(--border);">
              <span style="font-size:12px;color:var(--text-tertiary);">配置消息模板。改任何字段会自动重新预览（防抖 400ms）</span>
              <el-button size="small" plain @click="handleResetTpl">
                <el-icon><RefreshLeft /></el-icon> 恢复默认
              </el-button>
            </div>
            <el-form-item label="风格">
              <el-radio-group v-model="tpl.style" @change="onTplChange">
                <el-radio-button label="simple">简洁</el-radio-button>
                <el-radio-button label="table">表格</el-radio-button>
                <el-radio-button label="card">卡片</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item label="主机名格式">
              <el-radio-group v-model="tpl.host_format" @change="onTplChange">
                <el-radio-button label="full">完整域名</el-radio-button>
                <el-radio-button label="short">短主机名</el-radio-button>
                <el-radio-button label="with_ip">短名 + IP</el-radio-button>
              </el-radio-group>
              <div style="font-size:11px;color:var(--text-tertiary);margin-top:4px;">
                决定通知里如何展示节点：full=arch-web1.idc1.x.cn，short=arch-web1，with_ip=arch-web1 (10.10.12.70)
              </div>
            </el-form-item>
            <el-form-item label="显示字段">
              <el-checkbox v-model="tpl.show_cause" @change="onTplChange">可能原因</el-checkbox>
              <el-checkbox v-model="tpl.show_impact" @change="onTplChange">影响范围</el-checkbox>
              <el-checkbox v-model="tpl.show_value_range" @change="onTplChange">value 区间</el-checkbox>
              <el-checkbox v-model="tpl.show_hit_count" @change="onTplChange">命中处数</el-checkbox>
              <el-checkbox v-model="tpl.show_datasource" @change="onTplChange">数据源</el-checkbox>
              <el-checkbox v-model="tpl.show_time" @change="onTplChange">时间</el-checkbox>
              <el-checkbox v-model="tpl.show_detail_link" @change="onTplChange">详情链接</el-checkbox>
            </el-form-item>
            <el-row :gutter="12">
              <el-col :span="8">
                <el-form-item label="数值精度">
                  <el-input-number v-model="tpl.value_precision" :min="0" :max="6" @change="onTplChange" style="width:100%;" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="最多展示项">
                  <el-input-number v-model="tpl.max_entries" :min="1" :max="200" @change="onTplChange" style="width:100%;" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="字节上限">
                  <el-input-number v-model="tpl.max_bytes" :min="500" :max="30000" :step="100" @change="onTplChange" style="width:100%;" />
                  <div style="font-size:11px;color:var(--text-tertiary);margin-top:2px;">
                    企微 ≤4096, 钉钉 ≤5000, 飞书 ≤30000
                  </div>
                </el-form-item>
              </el-col>
            </el-row>
            <el-form-item label="时间格式">
              <el-input v-model="tpl.time_format" @change="onTplChange" placeholder="01-02 15:04:05" />
              <div style="font-size:11px;color:var(--text-tertiary);margin-top:2px;">Go time 模板，常用：2006-01-02 15:04:05 / 01-02 15:04:05 / 15:04:05</div>
            </el-form-item>
            <el-form-item label="标题格式">
              <el-input v-model="tpl.title_format" @change="onTplChange" placeholder="留空使用默认（支持 {severity} {alertname} {total} {bucketCount}）" />
              <div v-if="tpl.custom_subject" style="font-size:11px;color:#f59e0b;margin-top:4px;">
                ⚠️ 下方"高级"区已配置自定义标题模板，本字段将被忽略
              </div>
            </el-form-item>

            <!-- 高级：自定义 Go template（折叠） -->
            <div class="advanced-toggle" @click="advancedOpen = !advancedOpen">
              <el-icon><component :is="advancedOpen ? 'ArrowDown' : 'ArrowRight'" /></el-icon>
              <span>高级：自定义 Go template</span>
              <span v-if="tpl.custom_markdown || tpl.custom_subject" class="adv-badge">已启用</span>
            </div>
            <div v-show="advancedOpen" class="advanced-panel">
              <el-alert type="info" :closable="false" style="margin-bottom:12px;">
                <template #title>
                  <div style="font-size:12px;line-height:1.6;">
                    <div>填写后将<b>覆盖上面的预设风格</b>。语法错误会自动回退到预设并在预览中标红。可用变量见下方文档。</div>
                  </div>
                </template>
              </el-alert>

              <el-form-item label="标题模板">
                <el-input v-model="tpl.custom_subject" @input="onTplChange" placeholder="例：⚠️ [{{.Severity}}] {{.Alertname}} ({{.Total}}条)" type="textarea" :rows="2" />
              </el-form-item>

              <el-form-item label="正文模板">
                <el-input v-model="tpl.custom_markdown" @input="onTplChange" type="textarea" :rows="10" placeholder='例：
# {{.Title}}
{{if .Cause}}🔍 **可能原因**: {{.Cause}}
{{end}}
{{range $i, $e := .Entries}}**{{add $i 1}}. {{$e.Host}}**
   `{{$e.ValueStr}}` / 阈值 `{{$e.Threshold}}` · 命中 {{$e.Count}} 处
   {{$e.Summary}}
   🕐 {{$e.Time}}
{{end}}' style="font-family: 'SF Mono', Monaco, Menlo, monospace; font-size: 12px;" />
              </el-form-item>

              <el-form-item label=" ">
                <el-button size="small" @click="insertSnippet('default')">填入默认模板</el-button>
                <el-button size="small" @click="insertSnippet('compact')">紧凑示例</el-button>
                <el-button size="small" @click="insertSnippet('detailed')">详细示例</el-button>
                <el-button size="small" type="danger" plain @click="clearCustom">清空自定义</el-button>
              </el-form-item>

              <el-form-item label="可用变量">
                <div class="vars-doc">
                  <details open>
                    <summary>顶层变量（直接 <code>{{ OB }} .X {{ CB }}</code>）</summary>
                    <table class="vars-table">
                      <tr><td><code>.Title</code></td><td>标题（默认风格生成的标题）</td></tr>
                      <tr><td><code>.Severity</code></td><td>严重级别（大写 CRITICAL/WARNING/INFO）</td></tr>
                      <tr><td><code>.Alertname</code></td><td>告警名</td></tr>
                      <tr><td><code>.Total</code></td><td>原始实例数</td></tr>
                      <tr><td><code>.Cause</code></td><td>规则的"可能原因"</td></tr>
                      <tr><td><code>.Impact</code></td><td>规则的"影响范围"</td></tr>
                      <tr><td><code>.Resolved</code></td><td>true=恢复通知 / false=告警通知</td></tr>
                      <tr><td><code>.BaseURL</code></td><td>PromAI 告警详情链接前缀</td></tr>
                      <tr><td><code>.Entries</code></td><td>聚合后的告警条目列表（见下表）</td></tr>
                    </table>
                  </details>
                  <details>
                    <summary>条目变量（<code>{{ OB }} range .Entries {{ CB }} ... {{ OB }} .X {{ CB }} ... {{ OB }} end {{ CB }}</code>）</summary>
                    <table class="vars-table">
                      <tr><td><code>.Summary</code></td><td>告警内容（已渲染）</td></tr>
                      <tr><td><code>.State</code> / <code>.Severity</code></td><td>状态 / 级别</td></tr>
                      <tr><td><code>.RuleName</code> / <code>.RuleID</code></td><td>规则名 / ID</td></tr>
                      <tr><td><code>.DatasourceName</code> / <code>.DatasourceID</code></td><td>数据源</td></tr>
                      <tr><td><code>.Host</code></td><td>主机（按 host_format 配置格式化）</td></tr>
                      <tr><td><code>.ValueStr</code></td><td>当前值字符串（按 value_precision，区间用 A~B）</td></tr>
                      <tr><td><code>.MinValue</code> / <code>.MaxValue</code></td><td>原始最小/最大值</td></tr>
                      <tr><td><code>.Threshold</code></td><td>阈值</td></tr>
                      <tr><td><code>.Count</code></td><td>该聚合内合并了几个实例</td></tr>
                      <tr><td><code>.Time</code></td><td>最近一次发生时间</td></tr>
                      <tr><td><code>.Fingerprint</code></td><td>实例指纹（用于拼链接）</td></tr>
                      <tr><td><code>.DetailURL</code></td><td>详情链接（完整 URL）</td></tr>
                      <tr><td><code>.Labels</code></td><td>第一条样本的标签 map（如 <code>.Labels.instance</code>）</td></tr>
                    </table>
                  </details>
                  <details>
                    <summary>可用函数</summary>
                    <table class="vars-table">
                      <tr><td><code>upper / lower / title / trim</code></td><td>字符串大小写、去空白</td></tr>
                      <tr><td><code>replace</code></td><td>替换 <code>{{ OB }} replace "x" "y" .S {{ CB }}</code></td></tr>
                      <tr><td><code>contains / hasPrefix / hasSuffix</code></td><td>字符串包含 / 前后缀</td></tr>
                      <tr><td><code>split / join</code></td><td>分割 / 合并</td></tr>
                      <tr><td><code>truncate N s</code></td><td>截断字符串</td></tr>
                      <tr><td><code>printf / format</code></td><td>格式化（同 fmt.Sprintf）</td></tr>
                      <tr><td><code>now / formatTime</code></td><td>当前时间 / 时间格式化</td></tr>
                      <tr><td><code>default v fallback</code></td><td>空值回退</td></tr>
                      <tr><td><code>add / sub / mul</code></td><td>整数算术（用于 1-based 索引）</td></tr>
                      <tr><td><code>gt / lt / eq</code></td><td>比较</td></tr>
                      <tr><td><code>len</code></td><td>长度（entries/labels/strings）</td></tr>
                    </table>
                  </details>
                </div>
              </el-form-item>
            </div>
          </el-form>
        </el-tab-pane>

        <el-tab-pane label="实时预览" name="preview">
          <div style="display:flex;gap:12px;margin-bottom:8px;flex-wrap:wrap;">
            <el-button size="small" @click="loadPreview">
              <el-icon><Refresh /></el-icon> 刷新预览
            </el-button>
            <el-segmented v-model="previewMode" :options="[{label:'告警通知', value:'firing'},{label:'恢复通知', value:'resolved'}]" @change="loadPreview" size="small" />
            <div style="display:flex;align-items:center;gap:6px;">
              <span style="font-size:12px;color:var(--text-tertiary);">Mock 数量:</span>
              <el-input-number v-model="previewMockCount" :min="0" :max="500" :step="1" size="small" controls-position="right" style="width:100px;" @change="loadPreview" />
              <span style="font-size:11px;color:var(--text-tertiary);">(0=默认3条)</span>
            </div>
            <span v-if="previewResult" style="font-size:12px;color:var(--text-tertiary);align-self:center;margin-left:auto;">
              markdown 字节：<b :style="{ color: bytesColor }">{{ previewResult.bytes }}</b>
              / 上限 {{ tpl.max_bytes || 3800 }}
              <span v-if="bytesPct >= 100" style="color:#ef4444;margin-left:4px;">⚠️ 已超</span>
              <span v-else-if="bytesPct >= 85" style="color:#f59e0b;margin-left:4px;">⚠️ 接近</span>
            </span>
          </div>
          <div v-loading="previewLoading" v-if="previewResult" class="preview-box">
            <el-alert
              v-if="previewResult.errors && previewResult.errors.length"
              type="error"
              :closable="false"
              show-icon
              style="margin-bottom:12px;">
              <template #title>
                <div style="font-weight:600;">自定义模板有错误，预览已回退到默认风格：</div>
              </template>
              <ul style="margin:6px 0 0;padding-left:18px;font-size:12px;color:#ef4444;">
                <li v-for="(err, i) in previewResult.errors" :key="i" style="margin-bottom:2px;">{{ err }}</li>
              </ul>
            </el-alert>
            <div class="preview-section">
              <div class="preview-label" style="display:flex;align-items:center;">
                <span>标题</span>
                <code class="preview-title">{{ previewResult.title }}</code>
                <el-button size="small" text style="margin-left:auto;" @click="copyToClipboard(previewResult.title, '标题')">
                  <el-icon><DocumentCopy /></el-icon>
                </el-button>
              </div>
              <div class="preview-label" style="display:flex;align-items:center;margin-top:8px;">
                <span>Markdown（钉钉/企微/飞书 webhook）</span>
                <el-button size="small" text style="margin-left:auto;" @click="copyToClipboard(previewResult.markdown, 'Markdown')">
                  <el-icon><DocumentCopy /></el-icon> 复制
                </el-button>
              </div>
              <pre class="preview-content">{{ previewResult.markdown }}</pre>
            </div>
            <details :open="form.channel_type === 'email'">
              <summary style="cursor:pointer;color:var(--text-tertiary);font-size:12px;padding:4px 0;">
                查看 HTML（邮件）<span v-if="form.channel_type === 'email'" style="color:#10b981;margin-left:4px;">· 当前渠道使用</span>
              </summary>
              <div class="preview-section">
                <div class="preview-label" style="display:flex;align-items:center;">
                  <span>HTML 源码</span>
                  <el-button size="small" text style="margin-left:auto;" @click="copyToClipboard(previewResult.html, 'HTML')">
                    <el-icon><DocumentCopy /></el-icon> 复制
                  </el-button>
                </div>
                <pre class="preview-content">{{ previewResult.html }}</pre>
              </div>
            </details>
            <details>
              <summary style="cursor:pointer;color:var(--text-tertiary);font-size:12px;padding:4px 0;">查看 Plain（纯文本）</summary>
              <div class="preview-section">
                <pre class="preview-content">{{ previewResult.plain }}</pre>
              </div>
            </details>
          </div>
          <div v-else style="text-align:center;padding:40px;color:var(--text-tertiary);">
            点击「刷新预览」加载示例
          </div>
        </el-tab-pane>
      </el-tabs>
      <template #footer>
        <div class="dialog-footer" style="display:flex;align-items:center;gap:8px;">
          <span v-if="activeTab !== 'preview' && previewResult?.errors?.length" style="font-size:12px;color:#ef4444;margin-right:auto;">
            ⚠️ 模板有 {{ previewResult.errors.length }} 处错误（详见"实时预览"Tab）
          </span>
          <span v-else-if="activeTab !== 'preview' && previewResult" style="font-size:12px;color:var(--text-tertiary);margin-right:auto;">
            预览：{{ previewResult.bytes }} 字节
          </span>
          <el-button v-if="activeTab !== 'preview'" size="small" @click="activeTab = 'preview'">查看预览</el-button>
          <el-button @click="dialogVisible = false" style="color: var(--text-secondary);">取消</el-button>
          <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, reactive, watch } from 'vue'
import dayjs from 'dayjs'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, RefreshLeft, ArrowDown, ArrowRight, DocumentCopy, EditPen } from '@element-plus/icons-vue'
import type { FormInstance } from 'element-plus'
import { getNotifications, createNotification, updateNotification, deleteNotification, testNotification, previewMessageTemplate, type MessageTemplate, type TemplatePreviewResult } from '../api'
import type { NotificationChannel } from '../types'

function getCssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

const loading = ref(false)
const channels = ref<NotificationChannel[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const keyword = ref('')
const typeFilter = ref('')

const dialogVisible = ref(false)
const saving = ref(false)
const editingId = ref<number | null>(null)
const formRef = ref<FormInstance>()
const form = reactive({ channel_type: '', name: '', enabled: true })
const cfg = reactive<any>({})
const rules = { name: [{ required: true, message: '请输入渠道名称', trigger: 'blur' }] }

// 对话框的 Tab 切换
const activeTab = ref<'channel' | 'template' | 'preview'>('channel')

// 消息模板配置（保存到 cfg.template）
const tpl = reactive<MessageTemplate>({
  style: 'simple',
  host_format: 'short',
  show_cause: true,
  show_impact: true,
  show_value_range: true,
  show_hit_count: true,
  show_datasource: true,
  show_time: true,
  show_detail_link: true,
  value_precision: 2,
  max_entries: 50,
  max_bytes: 3800,
  time_format: '01-02 15:04:05',
  title_format: '',
  custom_markdown: '',
  custom_subject: '',
})

// 高级面板展开/折叠
const advancedOpen = ref(false)

// 文档里展示双花括号用的常量（避免 Vue 模板把 {{ ... }} 当插值）
const OB = String.fromCharCode(123, 123)
const CB = String.fromCharCode(125, 125)

// 预览
const previewMode = ref<'firing' | 'resolved'>('firing')
const previewMockCount = ref(0)
const previewResult = ref<TemplatePreviewResult | null>(null)
const previewLoading = ref(false)

// 字节占用百分比 & 颜色
const bytesPct = computed(() => {
  if (!previewResult.value) return 0
  const max = tpl.max_bytes || 3800
  return Math.round((previewResult.value.bytes / max) * 100)
})
const bytesColor = computed(() => {
  const p = bytesPct.value
  if (p >= 100) return '#ef4444' // 已超
  if (p >= 85) return '#f59e0b' // 接近
  return '#10b981' // 正常
})

function resetTpl() {
  tpl.style = 'simple'
  tpl.host_format = 'short'
  tpl.show_cause = true
  tpl.show_impact = true
  tpl.show_value_range = true
  tpl.show_hit_count = true
  tpl.show_datasource = true
  tpl.show_time = true
  tpl.show_detail_link = true
  tpl.value_precision = 2
  tpl.max_entries = 50
  tpl.max_bytes = 3800
  tpl.time_format = '01-02 15:04:05'
  tpl.title_format = ''
  tpl.custom_markdown = ''
  tpl.custom_subject = ''
  advancedOpen.value = false
}

function loadTplFromCfg() {
  const t: any = cfg.template || {}
  tpl.style = t.style ?? 'simple'
  tpl.host_format = t.host_format ?? 'short'
  tpl.show_cause = t.show_cause ?? true
  tpl.show_impact = t.show_impact ?? true
  tpl.show_value_range = t.show_value_range ?? true
  tpl.show_hit_count = t.show_hit_count ?? true
  tpl.show_datasource = t.show_datasource ?? true
  tpl.show_time = t.show_time ?? true
  tpl.show_detail_link = t.show_detail_link ?? true
  tpl.value_precision = t.value_precision ?? 2
  tpl.max_entries = t.max_entries ?? 50
  tpl.max_bytes = t.max_bytes ?? 3800
  tpl.time_format = t.time_format ?? '01-02 15:04:05'
  tpl.title_format = t.title_format ?? ''
  tpl.custom_markdown = t.custom_markdown ?? ''
  tpl.custom_subject = t.custom_subject ?? ''
  // 有自定义就自动展开
  advancedOpen.value = !!(t.custom_markdown || t.custom_subject)
}

function saveTplToCfg() {
  cfg.template = { ...tpl }
}

let previewDebounce: ReturnType<typeof setTimeout> | null = null
function onTplChange() {
  // 自动重新预览（防抖 400ms）
  if (previewDebounce) clearTimeout(previewDebounce)
  previewDebounce = setTimeout(() => { loadPreview() }, 400)
}

async function loadPreview() {
  previewLoading.value = true
  try {
    const res = await previewMessageTemplate({ ...tpl }, previewMode.value === 'resolved', previewMockCount.value)
    previewResult.value = res.data
  } catch (e: any) {
    // 网络错或后端 panic：仍展示一个最小的错误结果，避免用户切到预览看到空白
    previewResult.value = {
      title: '(预览请求失败)',
      markdown: '',
      html: '',
      plain: '',
      bytes: 0,
      errors: ['预览 API 调用失败: ' + (e?.message || String(e))],
    }
    ElMessage.error('预览失败，请检查服务')
  } finally {
    previewLoading.value = false
  }
}

// 自定义模板片段
const TPL_SNIPPETS: Record<string, { subject: string; markdown: string }> = {
  default: {
    subject: '',
    markdown: '',
  },
  compact: {
    subject: '⚠️ [{{.Severity}}] {{.Alertname}} · {{.Total}}条',
    markdown: `# {{.Title}}
{{range .Entries}}- **{{.Host}}** | {{.ValueStr}}/阈值{{.Threshold}} | x{{.Count}}
  {{.Summary}}
{{end}}`,
  },
  detailed: {
    subject: '🚨 {{.Severity}} {{.Alertname}}',
    markdown: `# {{.Title}}

{{if .Cause}}🔍 **可能原因**: {{.Cause}}

{{end}}{{if .Impact}}🎯 **影响范围**: {{.Impact}}

{{end}}---

{{range $i, $e := .Entries}}**{{add $i 1}}. {{$e.Host}}**
   📊 \`{{$e.ValueStr}}\` / 阈值 \`{{$e.Threshold}}\` · 命中 {{$e.Count}} 处
   📝 {{$e.Summary}}
   🕐 {{$e.Time}} · 📡 {{$e.DatasourceName}}
   🔗 [查看详情]({{$e.DetailURL}})

{{end}}`,
  },
}

function insertSnippet(key: string) {
  const s = TPL_SNIPPETS[key]
  if (!s) return
  tpl.custom_subject = s.subject
  tpl.custom_markdown = s.markdown
  onTplChange()
}

function clearCustom() {
  tpl.custom_subject = ''
  tpl.custom_markdown = ''
  onTplChange()
}

async function handleResetTpl() {
  try {
    await ElMessageBox.confirm('将所有模板字段恢复为默认值。继续吗？', '恢复默认', { type: 'warning' })
    resetTpl()
    onTplChange()
    ElMessage.success('已恢复默认模板')
  } catch { /* cancel */ }
}

async function copyToClipboard(text: string, label: string) {
  if (!text) {
    ElMessage.warning(`${label} 为空，无内容可复制`)
    return
  }
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(`${label} 已复制到剪贴板`)
  } catch {
    // 老浏览器 fallback
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
      ElMessage.success(`${label} 已复制`)
    } catch {
      ElMessage.error('复制失败，请手动选中复制')
    } finally {
      document.body.removeChild(ta)
    }
  }
}

function channelLabel(t: string) {
  const map: Record<string, string> = { wechat_work: '企业微信机器人', wechat_app: '企业微信应用', dingtalk: '钉钉', feishu: '飞书', email: '邮件', webhook: 'Webhook' }
  return map[t] || t
}

// 提取 row 的 template 子对象（用于列表显示徽标）
function rowTpl(row: NotificationChannel): any {
  if (!row.config_json) return null
  try {
    const c = JSON.parse(row.config_json)
    return c.template || null
  } catch {
    return null
  }
}
function hasCustomTemplate(row: NotificationChannel): boolean {
  const t = rowTpl(row)
  return !!(t && (t.custom_markdown || t.custom_subject))
}
function hasTemplateConfig(row: NotificationChannel): boolean {
  const t = rowTpl(row)
  return !!(t && Object.keys(t).length > 0)
}
function templateStyle(row: NotificationChannel): string {
  const t = rowTpl(row)
  const style = t?.style || 'simple'
  return ({ simple: '简洁', table: '表格', card: '卡片' } as Record<string, string>)[style] || style
}
function channelBg(t: string) {
  const map: Record<string, string> = { wechat_work: 'rgba(81,179,80,0.12)', wechat_app: 'rgba(81,179,80,0.12)', dingtalk: 'rgba(0,150,255,0.12)', feishu: 'rgba(83,119,241,0.12)', email: 'rgba(128,128,128,0.12)', webhook: 'rgba(139,92,246,0.12)' }
  return map[t] || 'rgba(128,128,128,0.12)'
}
function channelColor(t: string) {
  const map: Record<string, string> = { wechat_work: '#51b350', wechat_app: '#51b350', dingtalk: '#0096ff', feishu: '#5377f1', email: '#888', webhook: '#a78bfa' }
  return map[t] || '#888'
}

async function fetchData() {
  loading.value = true
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (keyword.value) params.keyword = keyword.value
    if (typeFilter.value) params.channel_type = typeFilter.value
    const res = await getNotifications(params)
    channels.value = res.data.items
    total.value = res.data.total
  } catch (e: any) { ElMessage.error(e.message) }
  finally { loading.value = false }
}

function openCreate() {
  editingId.value = null
  form.channel_type = ''; form.name = ''; form.enabled = true
  Object.keys(cfg).forEach(k => delete cfg[k])
  resetTpl()
  activeTab.value = 'channel'
  previewResult.value = null
  dialogVisible.value = true
}

function openEdit(row: NotificationChannel) {
  editingId.value = row.id ?? null
  form.channel_type = row.channel_type; form.name = row.name; form.enabled = row.enabled ?? false
  Object.keys(cfg).forEach(k => delete cfg[k])
  if (row.config_json) {
    try {
      const parsed = JSON.parse(row.config_json)
      if (parsed.to && Array.isArray(parsed.to)) {
        parsed.to = parsed.to.join(', ')
      }
      Object.assign(cfg, parsed)
    } catch { /* ignore */ }
  }
  loadTplFromCfg()
  activeTab.value = 'channel'
  previewResult.value = null
  dialogVisible.value = true
}

function onTypeChange() {
  // 切换渠道类型时只清掉与渠道类型相关的字段（webhook/dingtalk 等的连接参数）
  // 保留 template 子对象，让用户配的模板不会因换类型就丢失
  const preserve: any = {}
  if (cfg.template) preserve.template = cfg.template
  Object.keys(cfg).forEach(k => delete cfg[k])
  Object.assign(cfg, preserve)
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  // 预览阶段已发现错误？保存前再确认一次
  if (previewResult.value?.errors && previewResult.value.errors.length > 0) {
    try {
      await ElMessageBox.confirm(
        `检测到自定义模板有 ${previewResult.value.errors.length} 个错误，告警发送时会自动回退到预设风格。仍要保存吗？`,
        '模板存在错误',
        { type: 'warning', confirmButtonText: '仍要保存', cancelButtonText: '回去修改' },
      )
    } catch {
      activeTab.value = 'template'
      return
    }
  }

  // 字节预算超出预警
  if (previewResult.value && tpl.max_bytes && previewResult.value.bytes > tpl.max_bytes) {
    try {
      await ElMessageBox.confirm(
        `预览渲染 ${previewResult.value.bytes} 字节，超过当前字节上限 ${tpl.max_bytes}，告警实际发送时会被截断。建议增大字节上限或精简模板。仍要保存吗？`,
        '消息字节超限',
        { type: 'warning', confirmButtonText: '仍要保存', cancelButtonText: '回去调整' },
      )
    } catch {
      activeTab.value = 'template'
      return
    }
  }

  saving.value = true
  try {
    saveTplToCfg()
    const saveCfg = { ...cfg }
    if (saveCfg.to && typeof saveCfg.to === 'string') {
      saveCfg.to = saveCfg.to.split(/[,，]\s*/).filter(Boolean)
    }
    saveCfg.enabled = form.enabled ?? true
    const payload = { ...form, config_json: JSON.stringify(saveCfg) }
    if (editingId.value) {
      await updateNotification(editingId.value, payload)
      ElMessage.success('更新成功')
    } else {
      await createNotification(payload)
      ElMessage.success('创建成功')
    }
    dialogVisible.value = false
    await fetchData()
  } catch (e: any) { ElMessage.error(e.message) }
  finally { saving.value = false }
}

async function toggleEnabled(row: NotificationChannel) {
  try {
    let cfg: any = {}
    if (row.config_json) {
      try { cfg = JSON.parse(row.config_json) } catch {}
    }
    cfg.enabled = row.enabled
    await updateNotification(row.id!, {
      enabled: row.enabled,
      config_json: JSON.stringify(cfg)
    } as any)
  } catch { ElMessage.error('更新失败') }
}

async function handleTest(row: NotificationChannel) {
  try {
    await testNotification(row.id!)
    ElMessage.success('测试消息已发送，请检查通知渠道')
  } catch (e: any) { ElMessage.error(e.message) }
}

async function handleDelete(row: NotificationChannel) {
  try {
    await ElMessageBox.confirm(`确定删除「${row.name}」？`, '确认删除', { type: 'warning', cancelButtonText: '取消', confirmButtonText: '删除' })
    await deleteNotification(row.id!); ElMessage.success('删除成功'); await fetchData()
  } catch { /* ignore */ }
}

watch(dialogVisible, v => { if (!v) editingId.value = null })

// 切到预览 tab 时自动加载一次
watch(activeTab, (v) => {
  if (v === 'preview' && !previewResult.value) {
    loadPreview()
  }
})

onMounted(fetchData)
</script>

<style scoped>
.preview-box {
  background: var(--bg-secondary, #1a1a1a);
  border: 1px solid var(--border, #333);
  border-radius: 6px;
  padding: 12px;
  max-height: 500px;
  overflow-y: auto;
}
.preview-section {
  margin-bottom: 12px;
}
.preview-label {
  font-size: 12px;
  color: var(--text-tertiary, #888);
  margin-bottom: 4px;
}
.preview-content {
  font-family: 'SF Mono', 'Monaco', 'Menlo', monospace;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--bg-primary, #0f0f0f);
  border-radius: 4px;
  padding: 10px;
  margin: 0;
  color: var(--text-primary, #e0e0e0);
}
.preview-title {
  font-family: 'SF Mono', 'Monaco', 'Menlo', monospace;
  font-size: 12px;
  background: var(--bg-primary, #0f0f0f);
  color: var(--cyan, #06b6d4);
  padding: 2px 8px;
  border-radius: 3px;
  margin-left: 6px;
}

/* 高级面板 */
.advanced-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  padding: 8px 12px;
  margin: 8px 0;
  border: 1px dashed var(--border, #333);
  border-radius: 4px;
  font-size: 13px;
  color: var(--text-secondary, #aaa);
  user-select: none;
}
.advanced-toggle:hover {
  background: rgba(99, 102, 241, 0.05);
  border-color: rgba(99, 102, 241, 0.4);
  color: var(--cyan, #6366f1);
}
.advanced-toggle .adv-badge {
  margin-left: auto;
  font-size: 11px;
  background: rgba(16, 185, 129, 0.15);
  color: #10b981;
  padding: 1px 8px;
  border-radius: 10px;
}
.advanced-panel {
  border: 1px solid var(--border, #333);
  border-radius: 4px;
  padding: 12px;
  margin-bottom: 12px;
  background: rgba(99, 102, 241, 0.03);
}
.vars-doc {
  width: 100%;
  font-size: 12px;
}
.vars-doc details {
  margin-bottom: 6px;
  border: 1px solid var(--border, #333);
  border-radius: 4px;
  padding: 4px 8px;
  background: var(--bg-secondary, #1a1a1a);
}
.vars-doc summary {
  cursor: pointer;
  color: var(--text-secondary, #aaa);
  user-select: none;
  padding: 4px 0;
}
.vars-table {
  width: 100%;
  border-collapse: collapse;
  margin-top: 6px;
}
.vars-table td {
  padding: 3px 8px;
  border-bottom: 1px solid var(--border, #2a2a2a);
}
.vars-table td:first-child {
  width: 35%;
  white-space: nowrap;
}
.vars-table code {
  font-family: 'SF Mono', 'Monaco', 'Menlo', monospace;
  font-size: 11px;
  background: var(--bg-primary, #0f0f0f);
  color: var(--cyan, #06b6d4);
  padding: 1px 5px;
  border-radius: 2px;
}
</style>
