---
name: inspect-report
description: 触发巡检任务并推送报告到通知渠道。
user-invocable: true
x-enabled: true
metadata: {}
---

# Inspection Report

当用户要求执行巡检、发报告、检查系统状态并通知团队时，使用此工作流。

## 步骤

### 1. 列出数据源

确定要巡检的目标数据源：

```
list_datasources()
```

用户可能指定了名称，从结果中找到对应的数据源。注意数据源是否绑定了巡检模板（输出中 `[模板IDs: ...]`）；若未绑定模板/指标，需先提示用户配置，否则巡检无法执行。

### 2. 触发巡检

```
trigger_inspect(datasource="<datasource_name_or_url>")
```

返回 task_id，记录下来。

**可选：限定巡检范围**（用户只关心部分指标时使用，避免全量巡检）：

```
# 指定巡检模板
trigger_inspect(datasource="<name>", template_id="<模板ID或名称>")

# 指定具体指标（ID 或名称，逗号分隔）
trigger_inspect(datasource="<name>", metric_config_ids="1,2,3")

# 指定指标分组（类型 ID 或名称，逗号分隔）
trigger_inspect(datasource="<name>", metric_type_ids="CPU,内存")
```

三者优先级：template_id > metric_config_ids > metric_type_ids。未指定时按数据源绑定的模板全量巡检。

### 3. 轮询任务状态

使用 `query_task` 等待任务完成：

```
query_task(task_id="<task_id>")
```

如果任务还在进行中（status=running），等待几秒后重试。最多重试 5 次。

### 4. 查看报告详情

任务完成后，使用返回的 report_id：

```
get_report_detail(report_id=<report_id>)
```

### 5. 推送报告

根据内容判断是否需要推送通知：

如果发现异常指标（状态码非 OK），使用 `push_report`：

```
push_report(
  channel="wechat_work",     # 或 dingtalk / feishu
  report_id=<report_id>
)
```

如果用户指定了特定渠道或 webhook，优先使用：

```
push_report(
  channel="dingtalk",
  report_id=<report_id>,
  webhook_url="<custom_webhook>"
)
```

**可选：结合日志确认根因**。若发现异常指标，且相关数据源/触发规则绑定了华为云 LTS 日志源，可先用 `query_lts` 检索日志证据（按异常指标关联的服务名/IP 关键字），把日志异常与指标异常相互印证后再推送，让结论更有依据：

```
query_lts(keywords="<异常指标关联的服务名/IP>", time_range_minutes=15)
```

### 6. 汇总结果

告知用户巡检结果摘要：
- 检查了多少个指标
- 多少正常 / 多少异常
- 是否已结合日志确认根因（如适用）
- 是否已推送通知
