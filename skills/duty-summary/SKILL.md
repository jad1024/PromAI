---
name: duty-summary
description: 生成值班日报：汇总当日巡检结论与当前异常，输出一段式摘要。
user-invocable: true
x-enabled: true
metadata: {}
---

# Duty Summary

当用户要求生成值班日报、今日总结、交接班摘要时，使用此工作流。

## 步骤

### 1. 拉取今日巡检报告

```
list_reports(page=1, page_size=20)
```

筛选出今天的报告（按时间过滤）。多集群场景下每个数据源会各有一份，全部纳入汇总。

### 2. 查看各报告详情

对今日每份报告：

```
get_report_detail(report_id=<id>)
```

记录：数据源名称、总指标数、warning 数、critical 数、异常指标明细（名称/值/阈值/状态）。

### 3. 深挖重点异常

对 critical 级别的异常，做根因分析：

```
analyze_alert(metric_name="<异常指标名称>", datasource="<数据源名称>")
```

如未配置 AI 或分析失败，跳过此步，仅汇总数据。

### 4. 生成日报

按以下结构输出一段式日报：

```
【值班日报 YYYY-MM-DD】

一、总体状况：N 个集群巡检，M 个存在异常（其中 critical X 项、warning Y 项）
二、重点事件：每个 critical 异常一行（集群/指标/当前值/阈值/根因要点）
三、处置建议：按优先级排列，标注 [需立即处理] / [今日内处理] / [观察]
四、遗留事项：昨日异常中今日仍未恢复的项
```

无异常时输出一句话即可：所有集群巡检正常，无待处理事项。

### 5. 推送（可选）

用户要求推送时：

```
push_report(channel="<渠道>", report_id=<最新报告id>)
```

或直接把日报文本回复给用户自行转发。
