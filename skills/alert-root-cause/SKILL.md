---
name: alert-root-cause
description: 对活跃告警进行根因分析，关联指标和巡检记录。
user-invocable: true
x-enabled: true
metadata: {}
---

# Alert Root Cause Analysis

当用户询问告警原因、触发根因、或要求分析某个告警时，使用此工作流。

## 前置条件

用户通常会说 "分析一下这个告警" 或 "为什么 CPU 告警了"。先使用已知的 `metric_name` 或从上下文推断。

## 步骤

### 1. 使用 analyze_alert 工具

这是分析告警的核心工具。直接传入指标名称：

```
analyze_alert(metric_name="node_cpu_seconds_total")
```

如果用户提到了具体实例，带上 instance：

```
analyze_alert(metric_name="node_memory_MemAvailable_bytes", instance="192.168.1.100:9100")
```

### 2. 查询关联指标

analyze_alert 返回后，根据结果补充查询关联指标：

如果是 CPU 告警，检查负载：

```promql
node_load1
```

如果是内存告警，检查 OOM：

```promql
(node_memory_Active_bytes / node_memory_MemTotal_bytes) * 100
```

如果是磁盘告警，检查 inode：

```promql
(node_filesystem_files_free{fstype!=""} / node_filesystem_files{fstype!=""}) * 100
```

### 3. 查看巡检记录

使用 `list_reports` 获取近期巡检报告：

```
list_reports(status="problem", page_size=5)
```

如果找到相关报告，用 `get_report_detail` 查看详情：

```
get_report_detail(report_id=123)
```

### 4. 汇总分析

- 对比告警触发前后的指标变化趋势
- 结合巡检报告中的异常发现
- 给出根因判断和处理建议
- 如果确认是严重问题，使用 `push_report` 通知值班人员

## 常见场景示例

### CPU 持续高负载
1. `analyze_alert(metric_name="node_cpu_seconds_total")`
2. `query_metrics(promql="topk(5, sum by (process) (rate(node_procs_running{}[5m])))")`
3. 建议使用 `exec` 确认是否有异常进程
