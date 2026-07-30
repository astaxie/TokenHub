# 团队负责人大模型 API 接入指南

Language: [English](../team-leader-guide.md) | 简体中文 | [日本語](../ja/team-leader-guide.md)

本指南面向帮助业务应用通过项目级 TokenHub API Key 调用已批准大语言模型的团队负责人。

## 团队负责人职责

| 范围 | 要管理什么 |
| --- | --- |
| Project | 成员、Key、额度和成本归因边界 |
| Members | 在项目详情侧边栏添加应用负责人或开发者 |
| API Keys | 在承担用量和成本的项目下发放 Key |
| Models | 验证 Key 能看到预期模型列表 |
| Reports | 按成员、项目、模型和成本中心复盘用量 |

## 跨团队协作

一个项目可以关联多个团队。项目的**主团队**仍是默认责任、成本归属和审批责任的唯一主体，项目 Owner 也仍为单一用户。关联其他团队只授予访问权限，不会复制项目的 API Key、模型权限、额度、预算或路由策略。

每个关联团队拥有一个项目角色：

| 团队项目角色 | 生效权限 |
| --- | --- |
| `viewer` | 查看项目及其有权查看的报表 |
| `developer` | 在 viewer 基础上，可为当前用户发放项目 Key |
| `maintainer` | 在 developer 基础上，可执行当前后台角色允许的项目、成员和 Key 管理操作 |

直接项目成员角色与所有关联团队角色按固定规则合并：`owner` > `maintainer` > `developer` > `viewer`。用户属于多个关联团队时取最高角色；管理员可在**用户管理**中配置用户的主团队和其他团队。现有单团队项目会以兼容模式迁移，只保留原团队 Leader 的访问权限，不会让同团队普通用户自动获得新权限；管理员可在项目详情中替换该兼容角色。

在**项目空间**中选中项目，通过**关联团队**添加团队、修改角色或移除团队。权限变更从下一次请求立即生效。只能将活跃团队分配给用户或新关联到项目；禁用已关联团队后，该团队的项目角色会立即停止授权。更换主团队前不能移除主团队，不能移除最后一个关联团队；仍被项目或用户引用的团队也不能删除。

管理 API 提供相同操作：

| 方法 | 地址 | 用途 |
| --- | --- | --- |
| `GET` | `/api/admin/projects/{project_id}/teams?limit=50&offset=0` | 分页查看关联团队 |
| `POST` | `/api/admin/projects/{project_id}/teams` | 关联 `{ "team_id": "...", "role": "viewer|developer|maintainer" }` |
| `PATCH` | `/api/admin/projects/{project_id}/teams/{team_id}` | 修改关联团队角色 |
| `DELETE` | `/api/admin/projects/{project_id}/teams/{team_id}` | 移除非主团队且非最后一个团队的关联 |

## 发放项目 Key

1. 在 **项目空间** 中创建或选择项目。
2. 点击项目，在右侧成员面板添加应用负责人。
3. 打开 **Key 管理**，在该项目下创建 Key。
4. 将 Key 限制到应用实际需要的模型和额度。
5. 用 `GET /v1/models` 验证 Key 的模型范围。
6. 通过内部密钥流程把 Key 交给应用负责人。

## 验证可用模型

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models" \
  --header "Authorization: Bearer PROJECT_API_KEY" \
  --header "Content-Type: application/json"
```

返回的 `data[].id` 就是应用可以使用的模型 ID。

## 验证聊天调用

```bash
curl --request POST \
  --url "http://localhost:8080/v1/chat/completions" \
  --header "Authorization: Bearer PROJECT_API_KEY" \
  --header "Content-Type: application/json" \
  --data '{
    "model": "gpt-4.1-mini",
    "messages": [
      {"role": "user", "content": "Write a concise project onboarding checklist."}
    ],
    "stream": false
  }'
```

## 治理检查

| 检查项 | 为什么重要 |
| --- | --- |
| 项目 Owner | 用量和成本需要明确归属 |
| 成员角色 | 只有可信项目成员可以发放或轮换 Key |
| 模型范围 | Key 只应该暴露应用需要的模型 |
| 额度 | 额度和并发要匹配预期流量 |
| 日志 | 失败请求必须能通过 `request_id` 追踪 |

## 常见错误

| 状态 | 团队负责人处理方式 |
| --- | --- |
| 401 | 确认应用使用的是启用状态的项目 Key |
| 403 | 检查项目成员和 Key 允许模型范围 |
| 429 | 检查额度、并发和 Key/项目限制 |
| 503 | 请管理员检查路由和 Provider 健康状态 |
| 500 | 在请求日志中用 `request_id` 查看上游错误 |

## 截图

![Gateway documentation](../assets/screenshots/gateway-en.png)
