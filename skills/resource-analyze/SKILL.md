---
name: resource-analyze
description: 综合分析 CPU、内存、负载等系统资源使用情况，定位瓶颈。
user-invocable: true
x-enabled: true
metadata: {}
---

# Resource Analyze

当用户询问系统资源、服务器性能、卡顿原因或 CPU/内存异常时，使用此工作流。

## 步骤

### 1. 查询 CPU 使用率

使用 `query_metrics` 获取 CPU 使用率：

```promql
100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)
```

如果需要查看每个 CPU 核的情况：

```promql
100 - (avg by (instance, cpu) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)
```

### 2. 查询内存使用率

```promql
(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100
```

查看内存分布详情：

```promql
node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes
```

### 3. 查询系统负载

```promql
node_load1 / on (instance) count by (instance) (node_cpu_seconds_total{mode="system"})
```

### 4. 检查 Swap 使用

```promql
(1 - (node_memory_SwapFree_bytes / node_memory_SwapTotal_bytes)) * 100
```

### 5. 结果分析

| 指标 | 正常 | 需关注 | 严重 |
|------|------|--------|------|
| CPU 使用率 | < 70% | 70-90% | > 90% |
| 内存使用率 | < 80% | 80-90% | > 90% |
| 负载/核心数 | < 1 | 1-2 | > 2 |
| Swap 使用 | < 10% | 10-50% | > 50% |

- **需关注**：使用 `exec` 执行 `top -bn1 | head -20` 查看进程详情
- **严重**：使用 `push_report` 推送紧急性能报告
