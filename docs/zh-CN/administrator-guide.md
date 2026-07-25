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

## Provider 目录可用性

TokenHub 会把最后一次成功获取的公共 Provider 目录保存在数据库中。首次安装会先写入内置目录，并在后端开始接收请求前尝试下载完整公共目录；如果初始化下载失败，后端会使用内置快照启动，并在下次启动时重试。超过 24 小时未更新的公共快照会在后台刷新。普通「Provider 渠道」请求只读取数据库；只有下载结果通过完整性校验后，刷新才会原子替换数据库快照。GitHub 响应缓慢或不可用时，管理员仍可继续使用数据库中的最后可用目录。

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

## 目录恢复

删除模型会移除数据库记录及其路由，但不会修改 `data/model-catalog.yaml` 或 `TOKENHUB_MODEL_CATALOG_FILE` 指向的文件。后端启动时会再次同步当前配置的目录。管理员也可以在「模型目录」页面使用「恢复出厂目录」，从配置文件重新导入并覆盖标准模型，同时保留手动新增的其他模型。

## 安全检查清单

| 控制项 | 要求 |
| --- | --- |
| API keys | 完整 Secret 只展示一次，之后只保存前缀和后缀 |
| OAuth redirect URI | 在身份源中登记本地和生产回调地址 |
| RBAC | 区分 user、team leader、administrator、finance、security 和 operator 范围 |
| Audit retention | 请求日志和后台事件保留时间要满足合规审查 |
| Cost controls | 尽可能将每个请求归因到 user、project、team 和 cost center |

## 中国企业身份源

在「身份源」中选择钉钉、飞书或企业微信模板。模板会自动填充公网端点和 Claim 映射；只有需要经过企业代理或对接兼容的私有化服务时，才需要在高级配置中覆盖端点。

新增身份源包含三个必填步骤：选择身份源、填写连接方式、配置登录入口与首次登录授权。连接方式步骤会展示所选身份源的官方配置文档，可据此创建应用并获取相应凭据。通用 OIDC/OAuth2 模板则会提示查阅实际身份平台的应用注册文档，并提供相应协议参考。在第三步，已预置完整端点的模板可选择「跳过并完成」；如果模板缺少必要端点，高级设置会变为必填。也可主动进入高级设置，覆盖端点、Scope 和 Claim 默认值。编辑已有身份源时仍会在同一页展示完整表单。

请使用 TokenHub 后端公开地址和回调路径 `/api/admin/auth/oauth/callback`。Callback URL 可留空，让系统按后端请求 Host 自动生成；如果显式填写，完整 URL 必须与身份平台中登记的回调地址完全一致。

| 平台 | 应用侧必填配置 | TokenHub 处理方式 |
| --- | --- | --- |
| 钉钉 | 创建网页应用，开启用户授权，登记回调地址，复制 App Key 和 App Secret | 使用钉钉 v1.0 JSON Token API 和专用的用户 Token 请求头。如授权资料不包含邮箱，TokenHub 会基于 `unionId` 生成稳定的内部邮箱。 |
| 飞书 | 创建企业自建应用，开启网页授权，登记回调地址，复制 App ID 和 App Secret；在可用时授予用户资料和企业邮箱权限 | 使用飞书 OAuth v2 Token API，并解析用户信息响应的 `data` 层。如邮箱不可用，TokenHub 会基于 `union_id` 生成稳定的内部邮箱。 |
| 企业微信 | 创建自建应用并配置可信网页授权域，复制 Corp ID、应用 Secret 和 Agent ID，同时授予读取所需通讯录成员的权限 | 使用企微 CorpApp 登录，先获取应用 Token，再将回调 code 解析为 `UserId` 并读取成员资料。优先使用 `biz_mail`；缺失时基于 `userid` 生成稳定的内部邮箱。 |

内部邮箱以 `<provider>.tokenhub.local` 结尾，只用于账号标识，不是可投递邮箱。在新登录链路完整验证前，请保留一个可控的密码管理员账号。

## 截图

![Routing policies](../assets/screenshots/routes-en.png)
