---
name: disk-check
description: 检查磁盘使用率并给出处理建议 — 综合 metrics 查询和 exec 脚本两种方式。
user-invocable: true
metadata:
  openclaw:
    requires:
      bins: ["df"]
---

# Disk Check

当用户询问磁盘使用情况、磁盘告警或存储空间不足时，使用此工作流。

## 步骤

### 1. 查询磁盘指标

首先用 `query_metrics` 获取磁盘使用率：

```promql
(1 - (node_filesystem_avail_bytes{fstype!="",mountpoint="/"} / node_filesystem_size_bytes{fstype!="",mountpoint="/"})) * 100
```

如果需要查看所有挂载点，使用：

```promql
(1 - (node_filesystem_avail_bytes{fstype!=""} / node_filesystem_size_bytes{fstype!=""})) * 100
```

### 2. 执行本地探测

使用 `exec` 工具在目标机器上执行快速磁盘检查：

```bash
df -h --output=source,fstype,size,used,avail,pcent,target | head -20
```

若已知某个磁盘占用异常，进一步定位大目录：

```bash
du -sh /* 2>/dev/null | sort -rh | head -10
```

### 3. 结果分析

- **使用率 > 80%**: 建议清理临时文件（`/tmp`、日志），或扩展磁盘。
- **使用率 > 90%**: 警告用户立即处理，推荐使用 `push_report` 发送告警报告。
- **使用率 > 95%**: 严重状态，建议立即扩容，并通过通知渠道推送紧急报告。

### 4. 推送报告

如果需要将结果推送给团队成员，使用 `push_report`：

```
channel: "wechat_work" (或 "dingtalk" / "feishu")
content: 包含挂载点、使用率、建议操作的格式化报告
```
