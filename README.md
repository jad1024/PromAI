# PromAI — Prometheus 智能监控巡检平台

> Prometheus Automated Inspection · AI-Powered Monitoring

## 项目简介

PromAI 是基于 Prometheus 的智能监控巡检平台，支持自动采集指标、生成可视化 HTML 报告，并提供 **Web 管理后台**（SQLite 持久化）进行数据源、指标、模板、定时任务、通知渠道、告警规则的全生命周期管理。

在传统静态阈值巡检的基础上，PromAI 引入了三大 **AI 能力**：

- 🤖 **AI 告警根因分析**：告警触发时由 AI 结合实时指标自动分析根因，随通知一起推送
- 📈 **动态基线异常检测**：基于历史样本的 z-score 统计（3σ 原则）判断异常，替代单一静态阈值
- 🔔 **定时巡检 AI 分析推送**：定时巡检完成后 AI 生成健康分析结论，自动推送到飞书

## 核心功能矩阵

| 模块 | 功能 | 说明 |
|------|------|------|
| **数据源** | 多数据源管理 | 支持多 Prometheus 实例，独立绑定巡检模板、独立连通性检测 |
| **指标采集** | 指标类型 / PromQL 配置 | 内置常见指标模板，支持 PromQL 在线验证 |
| **异常检测** | 静态阈值告警 | 警告 / 严重两级告警（gt/ge/lt/le/eq/ne 六种判定） |
| **异常检测** | 🆕 动态基线异常检测 | 历史窗口 z-score 统计（3σ 原则），捕捉缓慢爬升、周期性突变，样本不足自动回退静态阈值 |
| **巡检** | 巡检模板系统 | 模板指标绑定、指标级阈值覆盖（不影响全局） |
| **巡检** | 定时巡检 | Cron 表达式调度，支持多数据源批量巡检 |
| **巡检** | 手动触发巡检 | 支持选择数据源、指标、模板即时巡检 |
| **AI 智能体** | AI 对话助手 | 基于 pi-agent 的智能体，支持工具调用（查询指标 / 触发巡检 / 分析告警 / 读取报告）与技能系统 |
| **AI 告警** | 🆕 告警根因分析 | 告警通知时由 AI 分析根因、推理过程、处置建议，随消息推送；规则级 + 全局双开关 |
| **AI 巡检** | 🆕 巡检健康分析 | 巡检完成后 AI 生成健康总览 / 异常分析 / 处理建议 / 风险提示 |
| **AI 推送** | 🆕 分析结果推送飞书 | AI 巡检分析结论自动推送到任务配置的飞书机器人；支持自定义分析提示词 |
| **AI 记录** | 🆕 AI 分析记录落库 | 每次告警 / 巡检 AI 分析均持久化到 `ai_analysis_records` 表（含模型、耗时、结果） |
| **告警系统** | Alertmanager 式告警 | 规则评估 → 实例派发 → 分组聚合 → 路由通知，支持抑制 / 静默 / 重复通知控制 |
| **外部告警** | 🆕 外部告警源接入 | n9e / 华为云 CES 规则同步 + Webhook 接收汇聚（SMN 订阅确认），统一告警历史与 AI 分析 |
| **通知** | 多渠道通知 | 钉钉 / 企业微信 / 飞书 / 邮件 / 通用 Webhook |
| **报告** | HTML 报告 | 巡检结果自动生成可视化 HTML 报告，可导出、可外链 |
| **管理后台** | 后台 SPA | Vue 3 + Element Plus，全量 CRUD 管理 |
| **健康大屏** | BI 可视化 | ECharts 展示各数据源健康评分、指标分布、告警统计 |
| **持久化** | SQLite（GORM） | 配置与数据全部落库，重启 / 重建容器不丢失 |
| **表格规范** | 列级筛选 / 排序 / 分页 | 所有列表页支持组合筛选、列排序、服务端分页 |

## 新增功能详解

### 1. AI 告警根因分析

告警触发需要发送通知时，系统自动调用 AI 对告警规则与当前触发实例做根因分析，分析结果追加在通知正文中（钉钉 / 企业微信 / 飞书 / 邮件均支持）。

- **分析内容**：🔴 根因结论 → 🔍 推理过程 → 📈 关联证据（AI 自动查询实时指标 / 历史巡检）→ 🛠️ 处置建议 → ⏰ 恢复观察点
- **双开关控制**：
  - 全局开关：`app_settings` 表 `ai_alert_analysis_enabled`（默认开启，需已配置 AI 模型）
  - 规则开关：告警规则 `ai_analysis_enabled`（默认开启，可单规则关闭）
- **结果落库**：每次分析写入 `ai_analysis_records` 表（type=`alert`），可追溯模型与耗时

### 2. 动态基线异常检测

不再依赖单一静态阈值，而是根据指标**历史窗口的均值 / 标准差**判断当前值是否偏离正常范围：

```
z-score = (当前值 - 历史均值) / 历史标准差
|z| >= 阈值(默认 3.0，即 3σ)  →  warning
|z| >= 2 × 阈值             →  critical
```

- **适用场景**：缓慢爬升的 CPU / 内存、周期性波动中的突变、随业务变化的流量水位
- **自动回退**：历史样本数不足（默认 < 10）或窗口查询失败时，自动回退到静态阈值判断
- **配置项**（指标配置 / `config.yaml`）：

  ```yaml
  baseline_enabled: true    # 是否启用动态基线
  baseline_window: "7d"     # 历史窗口：7d / 24h / 168h
  baseline_zscore: 3.0      # z-score 阈值（3σ）
  baseline_min_samples: 10  # 最少样本数
  ```

- **报告呈现**：启用基线的指标在报告中附带 `均值 / 标准差 / z-score / 极值 / 样本数`，异常明细标注 `[动态基线]` 标记

### 3. 定时巡检 AI 分析推送飞书

定时任务新增「AI 巡检分析」开关：巡检完成后自动调用 AI 对本次巡检结果做健康分析，并把分析结论推送到任务绑定的**飞书机器人**。

- **触发方式**：
  - 定时触发：任务按 Cron 执行巡检，完成后自动分析 + 推送（需开启 `ai_analysis_enabled`）
  - 手动触发：`POST /api/promai/cronjobs/:id/ai-analyze` 立即执行一次巡检 → 分析 → 推送（用于测试链路）
- **自定义提示词**：任务可配置 `ai_analysis_prompt` 覆盖内置分析模板，按团队诉求定制输出
- **推送内容**：🤖 标题 + 巡检时间 / 任务名 / 报告链接 + AI 分析正文（健康总览 / 异常分析 / 处理建议 / 风险提示）
- **内置模板已带告警上下文**：AI 分析时自动附上当前活跃告警（firing/pending，含规则名 / 级别 / 数据源 / 持续时长 / 关键标签），本地告警与外部告警源（n9e / 华为云）汇入的实例都会包含
- **结果落库**：每次分析写入 `ai_analysis_records` 表（type=`inspection`）
- **前置条件**：任务需绑定至少一个飞书通知渠道，且系统已配置 AI 模型

> **两套定时机制（不冲突，但建议二选一）**：
> - **定时任务页**（推荐）：功能完整——多数据源、通知渠道、AI 分析开关、自定义提示词；
> - **系统设置 → 基本设置 → 定时巡检表达式**（兼容旧版 `config.yaml` 的 `cron_schedule`）：仅对默认数据源执行，不绑定通知渠道 / 不开 AI 分析，只发送配置文件里全局启用的通知（如企业微信）。
> 两者由同一个调度器并行调度，同时配置会在各自时间重复巡检。建议只在**定时任务页**维护，把系统设置里的表达式留空。
>
> **时区注意**：Cron 表达式按**服务所在时区**触发。Docker / K8s 镜像已默认 `TZ=Asia/Shanghai`；若自行构建镜像或调整部署，请确保容器设置了 `TZ` 环境变量，否则表达式会按 UTC 触发（比北京时间晚 8 小时）。启动日志会打印 `[Cron] 定时调度器已启动（时区: ...）` 便于核对。

### 4. 外部告警源接入（n9e / 华为云 CES / 通用 Webhook）

如果告警规则分别维护在 n9e（夜莺）、华为云 CES 等平台，PromAI 提供两条通道将外部告警统一汇聚到本系统：

- **规则同步（拉取）**：在「告警源管理」页面配置 n9e（API Token 或 账号，二选一）或华为云 Region / Project ID + AK/SK，即可**周期拉取（30m / 1h / 6h / 1d，也可手动触发）**两个平台的告警规则到本地只读展示，便于统一查看。
  - **n9e 认证**：v8.0.0-beta.5+（含 v9）官方认证方式为个人中心「Token 管理」创建的 **X-User-Token**（需 n9e 配置 `[HTTP.TokenAuth] Enable=true`），规则走 `/api/n9e/busi-groups/alert-rules` 跨业务组接口；账号密码登录仅兼容旧版本（v6/v7 的 `/api/n9e/auth/login` → `dat.access_token`）。若登录失败，同步日志会输出各端点的 HTTP 状态码辅助诊断。
- **Webhook 接收（推送）**：每个告警源生成独立的推送地址 `POST /api/promai/webhook/alerts/:id`，外部平台把告警事件推送到本系统后，自动转换为内部告警实例 / 历史记录，与本地告警共用**告警历史时间线**视图，并支持：
  - **华为云 SMN**：自动回访 `subscribe_url` 完成订阅确认，可直接接收告警通知
  - **n9e / Alertmanager 兼容格式**：宽松 JSON 解析，直接落库
  - 可选**通知转发**（按所有已启用渠道发送文本：钉钉 / 企微 / 飞书 / Webhook）
  - 可选 **AI 根因分析**（异步分析，结果写入 `ai_analysis_records`，type=`alert_external`）
  - **手动结束告警**：在实时告警中对外部告警执行「结束」

> 推送鉴权：外部平台推送时须携带请求头 `Authorization: Bearer <token>`（token 在告警源配置中生成，列表界面脱敏显示）。

## 效果展示

![xx](images/screenshot-02.png)
![xx](images/screenshot-03.png)
![xx](images/screenshot-04.png)
![xx](images/screenshot-05.png)
![xx](images/screenshot-06.png)
![xx](images/screenshot-07.png)
![xx](images/screenshot-01.png)
![xx](images/image.png)
![xx](images/image2.png)

## 快速开始

### 源码编译

1. 克隆仓库：

   ```bash
   git clone https://github.com/kubehan/PromAI.git
   cd PromAI
   ```

2. 安装依赖：

   ```bash
   go mod download
   cd frontend && npm install && cd ..
   ```

3. 修改配置文件 `config/config.yaml`，设置 Prometheus 地址；如需 AI 能力，配置 `ai` 段模型参数

4. 构建前端 + 后端：

   ```bash
   cd frontend && npm run build
   cd ..
   go build -o promai .
   ```

5. 运行：

   ```bash
   ./promai
   ```

   首次运行自动从 `config/config.yaml` 导入种子数据到 SQLite。

### 访问地址

| 用途 | 地址 |
|------|------|
| 首页 | http://localhost:8091/promai |
| 健康大屏（BI） | http://localhost:8091/promai/bi |
| 巡检报告 | http://localhost:8091/promai/reports |
| 触发巡检 | http://localhost:8091/promai/inspection/ |

### Helm 部署

```bash
helm install promai ./deploy/helm/promai \
  --set image.repository=promai \
  --set image.tag=v2.0.3
```

升级：

```bash
helm upgrade promai ./deploy/helm/promai \
  --set image.repository=promai \
  --set image.tag=v2.0.3
```

常用配置：

```yaml
env:
  reportUrl: "https://promai.example.com"
bootstrapSql:
  enabled: true
  content: |
    -- 自定义初始化 SQL
```

说明：
- `env.reportUrl` 用于生成报告外链，推荐配置成外部可访问地址。
- `deploy/helm/promai/files/metric_types_seed.sql` 是默认初始化 SQL。
- 如果需要自定义初始化内容，直接改 `bootstrapSql.content` 或替换 `files/metric_types_seed.sql`。

## 管理后台功能

| 页面 | 功能 |
|------|------|
| **控制台** | 系统概览，数据源 / 报告 / 模板 / 通知 / 告警数量统计 |
| **健康大屏** | ECharts 图表展示各数据源健康评分、指标分布、告警统计 |
| **数据源管理** | 添加 / 编辑 / 删除 Prometheus 数据源，绑定巡检模板，连通性检测 |
| **指标配置** | 管理指标类型和 PromQL 配置，动态基线参数，PromQL 验证 |
| **巡检模板** | 创建巡检模板，绑定指标，支持指标级别覆盖（不影响全局） |
| **通知渠道** | 配置钉钉、企业微信、飞书、邮件等通知方式 |
| **定时任务** | Cron 表达式定时巡检，AI 巡检分析开关与自定义提示词，选择通知渠道 |
| **告警规则** | 规则评估（阈值 / PromQL）、告警分组、路由、抑制、静默，AI 根因分析开关 |
| **告警源管理** | 外部告警源（n9e / 华为云 CES / 通用 Webhook）CRUD、规则同步、推送地址与 token 管理 |
| **触发巡检** | 手动触发巡检，支持选择数据源、指标、模板 |
| **报告管理** | 查看 / 删除历史巡检报告 |
| **AI 助手** | 与监控数据对话：查询指标、分析告警、触发巡检、读取报告 |
| **系统设置** | 项目名称、调度 Cron、报告清理策略、AI 告警分析全局开关等 |

## 配置说明（config/config.yaml）

仅用于首次启动时的种子数据导入，后续所有配置通过管理后台 UI 操作，持久化在 SQLite。

```yaml
prometheus_url: "http://prometheus.k8s.kubehan.cn"
project_name: "PromAI"
cron_schedule: "00 08,17 * * *"

# AI 能力配置（告警根因分析 / 巡检分析推送）
ai:
  enabled: true
  models:
    - name: "qwen-max"
      provider: "dashscope"
      api_key: "${DASHSCOPE_API_KEY}"
      base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"

data_sources:
  - name: "cluster1"
    url: "http://prometheus.cluster1.example.com"

metric_types:
  - name: "内存使用率"
    category: "资源"
    query: '100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)'
    unit: "%"
    threshold: 90
    threshold_type: "greater"
    # 动态基线（可选，优先于静态阈值）
    baseline_enabled: true
    baseline_window: "7d"
    baseline_zscore: 3.0
    baseline_min_samples: 10
```

## 多数据源

通过管理后台「数据源管理」页面添加，每个数据源可独立绑定不同的巡检模板。

## 已实现的核心功能

- ✅ 多数据源支持
- ✅ SQLite 持久化（GORM）
- ✅ 巡检模板系统（模板指标覆盖）
- ✅ 智能告警（警告、严重两级告警）
- ✅ 🆕 AI 告警根因分析（通知自动附带根因与处置建议）
- ✅ 🆕 动态基线异常检测（z-score / 3σ，静态阈值自动回退）
- ✅ 🆕 定时巡检 AI 分析 + 飞书推送（支持自定义提示词、手动触发测试）
- ✅ 🆕 AI 分析记录落库（`ai_analysis_records`）
- ✅ AI 对话助手（工具调用 + 技能系统）
- ✅ Alertmanager 式告警体系（分组 / 路由 / 抑制 / 静默）
- ✅ 🆕 外部告警源接入（n9e / 华为云规则同步 + Webhook 汇聚 + SMN 订阅确认 + AI 分析）
- ✅ 管理后台 SPA（Vue 3 + Element Plus）
- ✅ 健康大屏（ECharts）
- ✅ 定时巡检（Cron 表达式）
- ✅ 多渠道通知（钉钉、企微、飞书、邮件、Webhook）
- ✅ 数据导出（HTML 报告）
- ✅ PromQL 在线验证
- ✅ 响应式设计
- ✅ 管理后台列表统一支持列级筛选、排序、组合筛选和服务端分页

## 许可证

该项目采用 MIT 许可证，详细信息请查看 LICENSE 文件。
