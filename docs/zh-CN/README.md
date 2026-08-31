# TokenHub 文档

Language: [English](../README.md) | 简体中文 | [日本語](../ja/README.md)

TokenHub 文档现在按企业 Token Governance 里的三种角色组织。默认文档语言是英文；中文和日文版本复用同一套英文截图和英文样例数据，避免多语言截图不一致。

## 架构与部署

- [整体架构](architecture.md)：部署拓扑、控制面和数据面、模型请求链路、持久化与安全边界。
- [部署](deployment.md)：Docker Compose、环境变量、数据与反向代理配置。
- [数据库演进](database-evolution.md)：只前进迁移、采纳基线、维护命令与回退兼容性。
- [PostgreSQL 设置指南](../postgresql-setup.md)：PostgreSQL 配置、运维和迁移。
- [性能基准测试](performance-benchmarking.md)：可复现的网关对比、进程内分配基准和回退预算。

## 插件平台

- [插件开发指南](plugin-development.md)：插件种类、运行位置、manifest 结构、运行时契约、安全、测试、发布流程，以及从内置能力迁移到插件的路径。

## 角色指南

| 指南 | 读者 | 主要工作流 |
| --- | --- | --- |
| [普通用户指南](user-guide.md) | 员工和应用开发者 | 查看可用模型、创建项目 Key、调用模型 API、查看个人用量 |
| [团队负责人指南](team-leader-guide.md) | 团队 Owner 和项目维护者 | 管理项目空间、成员、API Key、团队报表和项目成本归因 |
| [管理员指南](administrator-guide.md) | 平台管理员和安全运维 | 配置 Provider、模型目录、路由、身份源、RBAC、审计和成本治理 |
| [Agent Token 成本 API](agent-token-cost-api.md) | 本地报表 Agent 和平台管理员 | 创建最小权限分析凭证，以 JSON 或 CSV 拉取过滤、聚合和增量 Token 成本 |
| [Codex 生图 API 调用与测试指南](codex-image-generation-api.md) | 使用生图模型的应用开发者 | 调用文生图与图片编辑、轮询异步任务，并区分 Codex 订阅额度与 OpenAI API 用量 |
| [Codex 接入 TokenHub：Profile 快速配置](codex-tokenhub-profile-quick-start.md) | 仅需使用独立 Profile 的 Codex 用户 | 快速完成配置文件创建、Key 设置、启动验证和恢复 |
| [Codex 接入 TokenHub：四种配置方式与恢复指南](codex-tokenhub-configuration.md) | 需要比较不同接入方式的 Codex 用户和项目开发者 | 使用 Profile、进程级临时配置、CLI 全局配置或桌面端配置 TokenHub |

## 共享英文样例数据

| 对象 | 样例 |
| --- | --- |
| Organization | Acme AI Platform |
| Team | Platform Engineering |
| Project | Payments Assistant |
| Cost center | AI Platform Cost Center |
| Model | gpt-4.1-mini |
| API key placeholder | YOUR_TOKENHUB_API_KEY |

## 截图集

产品截图来自英文 UI；Codex 指南在三种语言版本中复用相同的实际终端截图，且敏感信息均已脱敏。

| 页面 | 文件 |
| --- | --- |
| Gateway documentation | `../assets/screenshots/gateway-en.png` |
| Overview | `../assets/screenshots/overview-en.png` |
| Models | `../assets/screenshots/models-en.png` |
| Routes | `../assets/screenshots/routes-en.png` |
| Usage | `../assets/screenshots/usage-en.png` |
| Settings | `../assets/screenshots/settings-en.png` |
| Codex Profile 配置 | `../assets/codex-profile/tokenhub-profile-config-redacted.png` |
| Codex Profile 状态 | `../assets/codex-profile/codex-status-redacted.png` |
