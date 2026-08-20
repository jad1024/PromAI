# Prometheus 监控报告生成器

> Prometheus Automated Inspection

## 项目简介

基于 Prometheus 的监控巡检工具，支持自动采集指标、生成可视化 HTML 报告，并提供 **Web 管理后台**（SQLite 持久化）进行数据源、指标、模板、定时任务、通知渠道的全生命周期管理。

## 管理后台表格规范

所有管理后台的列表型页面统一支持 **列级筛选、列级排序、组合筛选和分页**。

### 统一要求

- 每列支持筛选
- 每列支持升序/降序排序
- 支持多个列同时组合筛选
- 支持关键字筛选
- 支持枚举/状态类字段下拉筛选
- 支持时间范围筛选
- 支持一键清空筛选条件
- 切换分页后保留筛选和排序条件
- 数据量较大的列表使用服务端筛选、排序和分页，避免一次加载全部数据

### 适用页面

| 页面 | 列筛选 | 列排序 | 服务端分页 |
|------|--------|--------|------------|
| 数据源管理 | ✅ | ✅ | ✅ |
| 指标配置 | ✅ | ✅ | ✅ |
| 巡检模板 | ✅ | ✅ | ✅ |
| 模板指标 | ✅ | ✅ | ✅ |
| 通知渠道 | ✅ | ✅ | ✅ |
| 定时任务 | ✅ | ✅ | ✅ |
| 巡检记录 | ✅ | ✅ | ✅ |
| 报告管理 | ✅ | ✅ | ✅ |
| 告警记录 | ✅ | ✅ | ✅ |
| 用户管理 | ✅ | ✅ | ✅ |
| 审计日志 | ✅ | ✅ | ✅ |

### 筛选交互建议

文本字段使用关键字筛选，例如数据源名称、地址；状态、类型等枚举字段使用下拉筛选；创建时间、执行时间等时间字段支持时间范围筛选。

排序、筛选和分页参数由前端统一提交给后端 API，由后端转换为数据库查询条件并返回分页结果。

## 快速开始

### 推荐部署：Docker Compose

现在推荐使用 Docker Compose，**不需要安装 Go、Node.js、npm，也不需要手工编译前后端**。

```bash
# 1. 获取项目
 git clone https://github.com/jad1024/PromAI.git
 cd PromAI

# 2. 配置 Prometheus
cp .env.example .env
vi .env

# 3. 一键启动
 docker compose up -d --build
```

启动完成后访问：

```text
http://服务器IP:8091/promai
```

查看日志：

```bash
docker compose logs -f promai
```

停止：

```bash
docker compose down
```

升级：

```bash
git pull
docker compose up -d --build
```

数据默认保存在 Docker volume：

```text
promai-data       SQLite 数据库
promai-reports    巡检报告
```

因此容器重建或升级不会丢失业务数据。

### Docker 环境变量

`.env` 示例：

```env
PROMETHEUS_URL=http://prometheus:9090
PROMETHEUS_USERNAME=
PROMETHEUS_PASSWORD=
```

如果 Prometheus 不在同一个 Docker Compose 网络中，直接填写实际地址，例如：

```env
PROMETHEUS_URL=http://192.168.1.100:9090
```

### Helm 部署

如果运行在 Kubernetes 环境，仍然可以使用 Helm：

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

## 源码开发

如果需要本地开发，再使用源码方式：

```bash
go mod download
cd frontend && npm install && npm run build && cd ..
go build -o promai .
./promai
```

首次运行自动从 `config/config.yaml` 导入种子数据到 SQLite。

## 访问地址

| 用途 | 地址 |
|------|------|
| 管理后台 | http://localhost:8091/promai |
| 健康大屏（BI） | http://localhost:8091/promai/bi |
| 巡检报告 | http://localhost:8091/promai/reports |
| 触发巡检 | http://localhost:8091/promai/inspection/ |

## 管理后台功能

| 页面 | 功能 |
|------|------|
| **控制台** | 系统概览，数据源/报告/模板/通知数量统计 |
| **健康大屏** | ECharts 图表展示各数据源健康评分、指标分布、告警统计 |
| **数据源管理** | 添加/编辑/删除 Prometheus 数据源，绑定巡检模板 |
| **指标配置** | 管理指标类型和 PromQL 配置，按数据源筛选，PromQL 验证 |
| **巡检模板** | 创建巡检模板，绑定指标，支持指标级别覆盖（不影响全局） |
| **通知渠道** | 配置钉钉、企业微信、飞书、邮件等通知方式 |
| **定时任务** | Cron 表达式定时巡检，选择通知渠道自动告警推送 |
| **触发巡检** | 手动触发巡检，支持选择数据源、指标、模板 |
| **报告管理** | 查看/删除历史巡检报告 |
| **系统设置** | 项目名称、调度 Cron、报告清理策略等 |

## 配置说明（config/config.yaml）

仅用于首次启动时的种子数据导入，后续所有配置通过管理后台 UI 操作，持久化在 SQLite。

```yaml
prometheus_url: "http://prometheus.k8s.kubehan.cn"
project_name: "PromAI"
cron_schedule: "00 08,17 * * *"

data_sources:
  - name: "cluster1"
    url: "http://prometheus.cluster1.example.com"
```

## 多数据源

通过管理后台「数据源管理」页面添加，每个数据源可独立绑定不同的巡检模板。

## 已实现的核心功能

- ✅ 多数据源支持
- ✅ SQLite 持久化（GORM）
- ✅ 巡检模板系统（模板指标覆盖）
- ✅ 智能告警（警告、严重两级告警）
- ✅ 管理后台 SPA（Vue 3 + Element Plus）
- ✅ 健康大屏（ECharts）
- ✅ 定时巡检（Cron 表达式）
- ✅ 多渠道通知（钉钉、企微、飞书、邮件）
- ✅ 数据导出（HTML 报告）
- ✅ PromQL 在线验证
- ✅ 响应式设计
- ✅ 管理后台列表统一支持列级筛选、排序、组合筛选和服务端分页
- ✅ Docker Compose 一键部署

## 许可证

该项目采用 MIT 许可证，详细信息请查看 LICENSE 文件。
