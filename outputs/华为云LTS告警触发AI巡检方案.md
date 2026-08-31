# 华为云 LTS 告警触发 AI 巡检方案

> 目标：核心业务服务的华为云告警（CES/AOM）到达后，按关键字（IP、服务名、通配符等）匹配触发，
> 自动检索 LTS 日志（Java 应用日志）做 AI 根因分析，输出巡检报告并推送通知。
> 约束：token 花费必须可计量、可控制；每次分析提取的日志必须留档可回溯。

## 一、总体链路

```
华为云 CES/AOM 告警 → PromAI webhook（已有）
  → 关键字触发规则匹配（新增：alertname / labels / annotations，通配/正则）
    → 未命中：走原有告警链路（通知转发 / AI 根因分析）
    → 命中：
        防抖去重（复用事件聚合窗口：同故障窗口内只触发一次 + 30 分钟冷却 + 并发上限）
        → AI 巡检编排（新增 headless）：
            输入1 告警上下文（规则名/级别/标签/触发值 + 事件聚合降噪结论）
            输入2 LTS 日志折叠摘要（Java 日志特化降噪，见第四节）
            输入3 可选指标巡检（绑定巡检模板）
        → AI 分析（token 全程计量，见第三节）
        → 分析留档 + 日志证据留档（见第三节）
        → push_report 推送（企微/钉钉/飞书/邮件，已有）
```

## 二、现状盘点（已就绪 / 需新增）

| 环节 | 现状 |
|------|------|
| 华为云告警接收 | ✅ ExternalAlertSource(huaweicloud) webhook 接入，IP/资源名在 labels/annotations |
| 华为云凭据与签名 | ✅ AK/SK/Region/ProjectID 已加密存库；`sync/huawei.go` 的 SDK-HMAC-SHA256 签名可直接复用 |
| 外部告警 AI 分析 | ✅ `safeAnalyzeExternal` → headless 链路已跑通（目前无关键字过滤） |
| 报告推送 | ✅ push_report + sendExternalText 两条路 |
| 事件聚合防抖 | ✅ alertname 聚合 + 时间窗 + 风暴标记 |
| AI 分析记录 | ✅ AiAnalysisRecord 已有 ModelName/Prompt/Result/DurationMs |
| 关键字触发规则 | ❌ 新增 |
| LTS 查询客户端 | ❌ 新增（核心工作量） |
| AI 巡检编排（告警+日志合并分析） | ❌ 新增（小） |
| token 计量与预算护栏 | ❌ 新增（小） |
| 日志证据留档 | ❌ 新增（小） |

## 三、token 花费统计与日志留档（本节为新增需求）

### 3.1 token 全程计量

**数据层**：
- `HeadlessResult` 增加 PromptTokens / CompletionTokens / TotalTokens（从模型响应 usage 字段提取；
  流式响应在 turn end 事件取 usage，库未暴露则按「prompt 字节数 / 2.5」保守估算并标记 estimated）
- `AiAnalysisRecord` 增加列：`prompt_tokens`、`completion_tokens`、`total_tokens`、`cost_est`（nullable）
- 成本估算：AppSetting 维护模型单价表（`ai_price_<model>` = 每百万 token 输入/输出价），
  未配置价格时只展示 token 数不折算金额

**统计与展示**：
- 聚合 API：按 天 / 类型（inspection / alert / alert_external / lts_alert）/ 模型 汇总 token 与次数
- 前端：AI 分析记录页加 token 列；顶部汇总卡（今日 / 本月消耗，token 数 + 估算金额）；
  触发规则详情显示「该规则累计消耗」
- 分析报告落 AiAnalysisRecord（Type=lts_alert），复用现有记录页

**预算护栏（防失控）**：
- AppSetting：`ai_daily_token_budget`（默认 500k/天，0=不限）
- 超预算自动暂停触发（降级为普通通知并在通知里注明「AI 分析已暂停：日预算耗尽」），次日自动恢复
- 单次 prompt 超过硬上限（8k token）先截断日志段再触发

### 3.2 每次提取的日志留档（证据链）

`AiAnalysisRecord` 增加 `LogsJSON`（text），每次 LTS 分析写入：

```
{
  "query":   { 组ID, 流ID, 时间窗, keywords, limit, 实际返回行数 },
  "folded":  [ { 模式签名, 出现次数, 首次时间, 末次时间, 级别, logger } ],
  "samples": [ 每种模式的 1 条完整采样原文（不截断，含完整堆栈） ],
  "tokens":  { prompt_tokens, completion_tokens }
}
```

- 用途：审计（AI 结论可回溯到日志证据）、复现、调参验证（折叠是否丢关键信息）
- 体积控制：单次约 50-100KB；定时任务清理 N 天前（默认 30 天，AppSetting 可调）
- 报告详情页加「本次分析依据的日志」折叠面板，前端默认收起只显示模式统计

## 四、Java 应用日志特化降噪（漏斗）

用户日志全部为 Java 应用日志（logback/log4j 多行堆栈），漏斗各层针对性设计：

```
LTS 原始行（上万行，不进 AI）
  ↓ L1 查询侧限死：时间窗 15 分钟、limit 200、keywords 只取 ERROR/FATAL + 告警关键字
  ↓ L2 多行合并：非「时间戳开头」的行归并到上一条日志（堆栈帧不再是独立日志行）
  ↓ L3 模板折叠（Java 特化，见下）
  ↓ L4 模式摘要 + 每模式 1 条采样 → 2k-4k token 进 AI prompt（硬上限 8k）
  → AI 输出报告（限 500 字）
```

**Java 模板折叠规则**：
1. **变量归一**：IP / 数字 / UUID / 时间戳 / 请求耗时 → `<ip> <n> <uuid> <ts> <n>ms`
2. **异常归一**：消息体中的变量同样归一（`SQLException: ORA-12345` → `ORA-*`）
3. **堆栈折叠**（降 token 大头，单条 100 行堆栈 ≈ 2k token → ≈ 50 token）：
   - 保留：异常类型 + `Caused by` 链 + 应用包名（com.公司.*）前 3 帧
   - 折叠：框架帧（org.springframework / org.apache / java.* / sun.* / io.netty 等）→ `<framework> x N`
4. **logger 维度统计**：ERROR 集中在哪个 logger（如 com.xxx.OrderService）本身就是定位信号
5. **traceId 提取**：MDC 字段（traceId/requestId）用于关联同链路的 WARN→ERROR 序列

**成本对比**（裸奔 vs 漏斗）：

| 方案 | 单次输入 token | 100 次/天 | 按 ¥4/百万 token |
|------|------------|-----------|----------------|
| 全量塞 | 7万-20万 | 700万-2000万 | ¥28-80/天 |
| 漏斗后 | 3k-8k | 30万-80万 | ¥1.2-3.2/天 |

再叠加防抖（告警风暴 1000 条通常只触发 1-2 次分析），实际触发次数远低于告警条数。

## 五、三块新增组件设计

### A. 关键字触发规则（AlertTriggerRule）

- 匹配范围：alertname / 任意 label key / annotations(summary, description)
- 操作符：equals / contains / wildcard（glob，内部编译为正则）/ regex / cidr（IP 段）
- 规则内多条件 AND，多规则间 OR
- 动作配置：绑定的 LTS 日志组/流、时间窗、是否同时跑指标巡检（绑巡检模板）、推送渠道、级别过滤（默认 ERROR+FATAL）
- 支持试运行：对最近 N 条历史告警回放，预览命中结果
- 挂载点：外部告警 webhook 入口，告警落库后匹配；未命中零影响

### B. LTS 查询客户端

- API：`POST /v2/{project_id}/groups/{log_group_id}/streams/{log_stream_id}/content/query`
  （start_time/end_time 毫秒、keywords、labels、limit ≤5000，建议 100-500）
- 签名：复用 `signHWSRequest`（SDK-HMAC-SHA256）；若实测要求 Token，则 AK/SK 换一次 IAM Token 兜底
- IAM 权限：现有 AK/SK 补 `lts:logStream:searchLog`（只读）
- 通配符检索：keywords 为分词级匹配；复杂语法（`ip:192.168.* AND error`）需在 LTS 控制台开字段索引，走搜索语法接口
- 限流：客户端侧节流，失败不阻断（降级为纯告警分析并在报告注明）

### C. AI 巡检编排（headless 新函数）

1. **预取**：按触发规则查 LTS → L1-L4 降噪 → 摘要进 prompt
2. **工具化（增强）**：Agent 注册 `query_lts` 工具（参数：关键字/时间范围），AI 可二次深挖；
   限调用 ≤2 次、每次 limit 50、返回折叠后摘要
3. **prompt 组装**：告警上下文 + LTS 折叠摘要 +（可选）指标巡检结果 + 事件聚合降噪结论
4. **产出**：AiAnalysisRecord(Type=lts_alert，含 token 与 LogsJSON 留档) + push_report 推送

## 六、风险与对策

| 风险 | 对策 |
|------|------|
| 告警风暴打爆 AI | 事件聚合窗口去重 + 30 分钟冷却 + 并发上限（2） |
| 日志量撑爆 prompt | 漏斗四层降噪 + prompt 硬上限 8k + 工具调用限次 |
| token 失控 | 日预算护栏 + 全程计量 + 汇总展示（见 3.1） |
| LTS API 限流/认证 | 客户端节流；签名复用 CES 已验证实现；IAM Token 兜底 |
| 关键字误匹配 | 试运行回放预览，先验证再启用 |
| 折叠丢关键信息 | LogsJSON 留档完整采样原文，可回溯审计（见 3.2） |

## 七、分阶段落地

- **Phase 1（核心闭环）**：触发规则（contains/regex）+ LTS 客户端 + headless 编排（Java 日志漏斗降噪）+ push_report；同步落地 token 计量与 LogsJSON 留档（结构定了就一起做，避免后补）
- **Phase 2（增强）**：query_lts 工具化、指标巡检联动（绑模板）、防抖参数配置化、token 汇总看板、日志留档查看面板、留档过期清理
- **Phase 3（进阶）**：LTS SQL 分析、跨日志流关联、根因结论回填告警规则 Cause/Impact、traceId 链路关联分析

## 八、skills 与系统提示词同步调整

前提认知：**自动触发链路不走 skill**（webhook → 触发规则 → headless 编排为固定流程），
skill 只影响对话场景（用户在聊天窗口让 AI 查告警/日志时）。因此调整都是轻量文本，
且必须与 Phase 2 的 `query_lts` 工具化同步落地——工具没上线前不能改 skill 引用它。

| 位置 | 调整内容 | 时机 |
|------|---------|------|
| `system_prompt.go` 工具清单 | 新增 query_lts 说明（参数：关键字/日志组流/时间范围，返回折叠摘要），并在「告警与事件聚合」节补充日志分析能力描述 | Phase 2 |
| `alert-root-cause/SKILL.md` | 根因分析步骤增加「LTS 日志取证」环节：华为云告警或配置了日志源时，先用 query_lts 按告警 IP/服务名/时间窗检索日志再下结论 | Phase 2 |
| 新增 `lts-log-query/SKILL.md` | 对话场景查日志的引导：用户说「看看 XX 服务的日志」「这个报错什么情况」时，按 时间窗 → 关键字 → 折叠摘要 → 结论 的流程用 query_lts | Phase 2 |
| `inspect-report/SKILL.md` | 巡检报告 skill 补一步可选环节：发现异常指标时，如数据源绑定了 LTS 日志源，结合日志确认根因再推送 | Phase 2 |

参考先例：事件聚合/模板体系改造后（提交 4ec55a7 同期）就是按同样模式同步更新了
system_prompt + inspect-report + alert-root-cause 三个文件。

