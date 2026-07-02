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

用户可能指定了名称，从结果中找到对应的数据源。

### 2. 触发巡检

```
trigger_inspect(datasource="<datasource_name_or_url>")
```

返回 task_id，记录下来。

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

### 6. 汇总结果

告知用户巡检结果摘要：
- 检查了多少个指标
- 多少正常 / 多少异常
- 是否已推送通知
