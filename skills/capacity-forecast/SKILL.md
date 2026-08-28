---
name: capacity-forecast
description: 按磁盘/内存等资源的使用增长速率，预测多少天后写满，输出容量预警。
user-invocable: true
x-enabled: true
metadata: {}
---

# Capacity Forecast

当用户要求做容量预测、估算磁盘/内存还能撑多久、什么时候会写满时，使用此工作流。

## 步骤

### 1. 确定目标

先确认用户要预测的资源类型（磁盘/内存/连接数等）和数据源（集群）。未指定时默认对主要数据源做磁盘和内存两项。

### 2. 取当前使用率

```
query_metrics(promql="100 - (node_filesystem_avail_bytes{fstype!~'tmpfs|overlay'} / node_filesystem_size_bytes{fstype!~'tmpfs|overlay'} * 100)", datasource="<数据源名称>", description="各挂载点磁盘使用率")
```

```
query_metrics(promql="100 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes * 100)", datasource="<数据源名称>", description="内存使用率")
```

### 3. 计算增长速率

用区间聚合取过去 7 天与当前的使用率差值，估算每日增长：

```
query_metrics(promql="avg_over_time((100 - (node_filesystem_avail_bytes / node_filesystem_size_bytes * 100))[7d:1d]", datasource="<数据源名称>", description="磁盘使用率7天历史采样")
```

每日增长率 = (当前值 - 7天前值) / 7。

### 4. 预测写满时间

对每个挂载点/节点：

- 剩余可用 = 100 - 当前使用率
- 预测天数 = 剩余可用 / 每日增长率

注意：增长率接近 0 或为负时标注"增长停滞，无写满风险"；短期波动型指标（如内存缓存）说明预测仅作参考。

### 5. 输出结论

按以下结构输出：

1. 风险排行：按预测天数升序列出（<30 天标红，<90 天标黄）
2. 每项包含：挂载点/节点、当前使用率、每日增长率、预计写满日期
3. 建议：接近阈值的给出扩容或清理建议（如日志清理、旧数据归档）

如需要，可用 `push_report` 将预警结果推送到通知渠道。
