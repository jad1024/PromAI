---
name: datasource-check
description: 检查 Prometheus 数据源的连通性、数据延迟和指标采集健康度。
user-invocable: true
x-enabled: true
metadata: {}
---

# Datasource Health Check

当用户询问数据源状态、Prometheus 是否正常、数据是否有延迟时，使用此工作流。

## 步骤

### 1. 列出数据源

查看当前所有已配置的数据源：

```
list_datasources()
```

关注 `enabled` 字段，只对已启用的数据源检查。

### 2. 检查数据源可用性

对每个数据源执行一个轻量级查询，验证其响应能力：

```
query_metrics(datasource="<datasource_name>", promql="up")
```

如果返回空值或报错，说明该数据源不可达。

### 3. 检查数据延迟

查询最新数据点的时间戳与当前时间的差距：

```promql
time() - max by (instance) (node_boot_time_seconds)
```

更通用的延迟检查：

```promql
time() - max by (job) (scrape_duration_seconds)
```

如果数据延迟超过 5 分钟，标记为异常。

### 4. 检查 target 状态

检查 up 指标，确认有多少 target 在线：

```promql
count by (job) (up == 1)
```

对比：

```promql
count by (job) (up == 0)
```

### 5. 结果汇总

对每个数据源给出：
- **状态**：正常 / 延迟 / 不可达
- **Target 在线率**：正常 target / 总 target
- **数据延迟**：X 分钟

如果发现数据源不可达或大量 target 下线，使用 `push_report` 通知管理员。
