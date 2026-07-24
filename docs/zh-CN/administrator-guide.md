# 管理员指南

Language: [English](../administrator-guide.md) | 简体中文 | [日本語](../ja/administrator-guide.md)

本指南面向将 TokenHub 作为企业 AI 网关运行的平台管理员、安全运维和基础设施负责人。

## 管理员范围

| 区域 | 责任 |
| --- | --- |
| Provider Channels | 配置上游 Base URL、凭证、资源和健康检查 |
| Model Catalog | 维护标准模型名、能力、上下文窗口和计价单位 |
| Routing Policies | 用优先级、权重和故障转移策略把标准模型映射到 Provider 模型 |
| Projects and Teams | 定义 Key、额度和成本归因的组织边界 |
| Identity Sources | 配置 OAuth 或 OIDC 企业登录 |
| Security and Audit | 审查请求日志、后台操作、Key 轮换和策略变更 |

## 生产上线顺序

1. 至少配置一个身份源，并保留可控的管理员账号。
2. 添加上游 Provider，例如 `OpenAI Production`、`Azure East US` 或 `Internal Model Gateway`。
3. 使用英文模型名维护模型目录，例如 `gpt-4.1-mini`。
4. 为每个要开放调用的模型创建启用状态的路由策略。
5. 创建团队、项目、成本中心和默认额度策略。
6. 用 Model Playground 和请求日志验证链路。
7. 在大规模发放 Key 前检查用量归因。

## 路由要求

普通用户只应该看到可调用模型。模型必须在目录中启用，并且至少有一条启用路由，才算可调用。

| 状态 | 管理端表现 |
| --- | --- |
| 启用模型且有启用路由 | 正常模型卡片 |
| 启用模型但没有路由 | 使用不同背景色提示缺少配置 |
| 禁用模型 | 对普通用户隐藏 |
| Provider 路由不健康 | 在路由诊断和请求日志中可见 |

## Prompt Cache 计价

模型目录支持按每百万 Token 配置可选的缓存读取价格。配置后，命中缓存的输入 Token 按该价格估算成本；留空时，DeepSeek V4 Pro 按标准输入价的约 0.83% 估算，其他 DeepSeek 模型按 2% 估算，其余非 Embedding 模型按 10% 估算。模型定价表会标记估算值，并在悬停时说明采用的比例。

## 安全检查清单

| 控制项 | 要求 |
| --- | --- |
| API keys | 完整 Secret 只展示一次，之后只保存前缀和后缀 |
| OAuth redirect URI | 在身份源中登记本地和生产回调地址 |
| RBAC | 区分 user、team leader、administrator、finance、security 和 operator 范围 |
| Audit retention | 请求日志和后台事件保留时间要满足合规审查 |
| Cost controls | 尽可能将每个请求归因到 user、project、team 和 cost center |

## 截图

![Routing policies](../assets/screenshots/routes-en.png)
