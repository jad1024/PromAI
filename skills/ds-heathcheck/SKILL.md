---
name: ds-heathcheck
description: 对现有数据源进行测试，验证数据源是否可用，是否在线
user-invocable: true
x-enabled: true
metadata: {}
---

# 数据源可用性检测

1. 当用户提出看看xxx集群是否可用，是否在线，是否正常时
2. 调用数据源管理里面的`测试`接口进行检查可用性
3. 回答用户数据源是否可以使用