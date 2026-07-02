---
name: node-health
description: 对指定节点进行一键健康检查（CPU、内存、磁盘、网络、运行时间）。
user-invocable: true
x-enabled: true
metadata: {}
---

# Node Health Check

当用户要求检查某台机器是否健康、做体检或全面状态检查时，使用此工作流。

## 步骤

### 1. 查询节点基础信息

使用 `exec` 获取节点运行信息：

```bash
uptime && echo "---" && uname -a
```

### 2. CPU 健康

```promql
100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)
```

如果用户指定了实例，加上 `{instance="<ip>:9100"}`。

### 3. 内存健康

```promql
(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100
```

检查是否存在 OOM 风险：

```promql
node_memory_MemAvailable_bytes < 100 * 1024 * 1024
```

### 4. 磁盘健康

用 `query_metrics` 检查所有挂载点的磁盘使用率：

```promql
(1 - (node_filesystem_avail_bytes{fstype!="",instance="<instance>"} / node_filesystem_size_bytes{fstype!="",instance="<instance>"})) * 100
```

以及 inode 使用率：

```promql
(1 - (node_filesystem_files_free{fstype!="",instance="<instance>"} / node_filesystem_files{fstype!="",instance="<instance>"})) * 100
```

### 5. 网络健康

检查网络接口错误率：

```promql
rate(node_network_receive_errors_total[5m]) > 0
```

### 6. 系统运行时间

```promql
time() - node_boot_time_seconds{instance="<instance>"}
```

### 7. 综合评分

| 类别 | 通过条件 | 异常 |
|------|---------|------|
| CPU | < 80% | ≥ 80% |
| 内存 | < 85% | ≥ 85% |
| 磁盘 | < 80% | ≥ 80% |
| inode | < 80% | ≥ 80% |
| 网络错误 | 0 | > 0 |

汇总结果——几项通过、几项异常——告知用户整体状态。
