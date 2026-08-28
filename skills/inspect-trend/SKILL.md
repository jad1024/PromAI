---
name: inspect-trend
description: 对比最近 N 次巡检报告，输出恶化/恢复的指标趋势清单。
user-invocable: true
x-enabled: true
metadata: {}
---

# Inspection Trend Analysis

当用户要求对比历史巡检、看趋势、找恶化/恢复的指标时，使用此工作流。

## 步骤

### 1. 确定范围

先明确用户要对比的数据源（集群）和时间跨度（默认最近 5 次）：

```
list_datasources()
```

如用户未指定数据源，列出所有数据源让用户选择。

### 2. 拉取历史报告

```
list_reports(datasource="<数据源名称>", page=1, page_size=10)
```

取最近的 N 份报告（默认 5），记录报告 ID 和时间。如不足 2 份，直接告知用户无法对比。

### 3. 逐份查看详情

对每份报告：

```
get_report_detail(report_id=<id>)
```

重点记录每个指标的：指标名称、当前值、状态（normal/warning/critical）、阈值。

### 4. 对比分析

按指标维度对比各期数据，输出三类清单：

- **持续恶化**：连续 2 次以上状态变差，或数值持续朝危险方向变化
- **新发异常**：上期正常、本期转为 warning/critical
- **已恢复**：上期异常、本期恢复正常

对持续恶化项，可用 `query_metrics` 补充当前实时值确认趋势仍在延续：

```
query_metrics(promql="<该指标的PromQL>", datasource="<数据源名称>", description="确认恶化趋势")
```

### 5. 输出结论

按以下结构输出：

1. 趋势总览：N 期报告中异常数的变化曲线（如 3→2→4）
2. 恶化清单（含每期的值和状态变化）
3. 恢复清单
4. 建议：对恶化项给出需要关注的原因推测和处置建议

如用户要求推送，用 `push_report` 发送到指定渠道。
