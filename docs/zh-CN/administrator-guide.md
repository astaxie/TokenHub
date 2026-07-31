# 管理员指南

Language: [English](../administrator-guide.md) | 简体中文 | [日本語](../ja/administrator-guide.md)

本指南面向将 TokenHub 作为企业 AI 网关运行的平台管理员、安全运维和基础设施负责人。

## 管理员范围

| 区域 | 责任 |
| --- | --- |
| Provider Channels | 配置上游 Base URL、凭证、资源和健康检查 |
| 模型目录 | 发布对外 API 模型名，并管理 Provider 上游模型库存 |
| Routing Policies | 用优先级、权重和故障转移策略把对外模型映射到 Provider 模型 |
| Projects and Teams | 定义 Key、额度和成本归因的组织边界 |
| Identity Sources | 配置 OAuth 或 OIDC 企业登录 |
| Security and Audit | 审查请求日志、后台操作、Key 轮换和策略变更 |

## 生产上线顺序

1. 至少配置一个身份源，并保留可控的管理员账号。
2. 添加上游 Provider，例如 `OpenAI Production`、`Azure East US` 或 `Internal Model Gateway`。
3. 从 Provider 中选择并引入需要的上游模型，形成 Provider 模型库存。
4. 将选中模型按同名 1:1 方式发布，或设置自定义对外名称与映射。
5. 创建团队、项目、成本中心和默认额度策略。
6. 用 Model Playground 和请求日志验证链路。
7. 在大规模发放 Key 前检查用量归因。

## API Key 归属与用量归因

发放 API Key 时，应在「归属用户」中选择实际使用人。发放人仍保留在审计元数据中，但 Key 的用量会统计到归属用户。平台管理员可以选择任一启用用户；团队负责人只能选择本团队的启用用户；普通用户只能把 Key 归属给自己。

每条新用量记录都会固化当时的归属用户，因此以后转移归属或删除 Key 不会改写已记录的历史。对该字段上线前的旧记录，系统依次回退到 Key 当前归属用户、旧的发放人、项目负责人，最后显示为「未知」。个人排行会分别展示用量中实际出现过的 Key 数，以及当前归属且未吊销的 Key 数。

## Provider 目录可用性

TokenHub 会把最后一次成功加载的 Provider 目录保存在数据库中。每次后端启动时，系统都会校验并加载配置的本地 `provider-catalog.json`，然后原子替换数据库快照。普通「Provider 渠道」请求只读取数据库快照，管理员也可以手动刷新同一份本地目录。若本地目录读取、解析或完整性校验失败，TokenHub 会继续使用最后一次有效快照。

## 模型目录与发布

新的模型目录将过去容易混淆的三个概念分开展示：

| 视图 | 含义 |
| --- | --- |
| **对外模型** | 提供给业务应用的 API 契约。这是默认视图，初始只展示已发布模型。 |
| **Provider 上游模型** | 已引入到某个具体 Provider 的真实模型库存。只引入库存不会对客户端暴露模型。 |
| **候选模板库** | 来自跟踪目录的参考元数据。模板在引入并建立映射前，既未接入也不可调用。 |

从 Provider 引入模型时，「引入并发布」默认创建同名 1:1 映射，管理员也可以在引入前修改对外名称。例如，对外发布 `DeepSeek`，但映射到 `OpenAI Production / gpt-4.5`。一个 Provider 上游模型也可以映射到多个对外别名。

管理员也可以手工创建对外模型，并从某个 Provider 已引入的上游模型中选择映射目标。如果所需模型尚未出现，应先把它引入 Provider 库存，再创建映射，让「引入」和「发布」保持为两个明确步骤。

「发布状态」与「运行健康」相互独立。模型要出现在 `GET /v1/models` 中，必须同时满足：对外 `Model` 已启用、至少有一条已启用 `ModelRoute`，且在 API Key 配置了模型白名单时获得授权。Provider 或 Provider Resource 短时不健康不会改变该列表，只会影响当前请求能否成功，并在目录和路由诊断中单独展示。下线对外模型会将它从 `GET /v1/models` 移除，但保留映射，便于之后重新发布。

## 模型路由策略

管理控制台按整个对外模型配置一次路由策略。打开模型卡片并选择策略 Tab，当前 Tab 会说明适合场景、实际选择行为、参数含义和具体示例。在该策略下调整各 Provider 显示的参数，然后点击「应用策略」。策略及全部 Provider 参数会原子保存，模型不会处于部分更新的中间状态。

使用固定比例时，直接在各 Provider 旁填写相对权重。例如两个 Provider 分别设置 75 和 25，界面会显示目标占比 75% 和 25%。自适应模式把这些值作为基础权重，再动态调整实际配额；质量、成本和综合平衡模式只展示各自相关的评分参数。这些策略都会把符合条件的 Provider 放入同一个流量池。只有「顺序故障转移」使用 Provider 顺序，可拖动线路设置第一、第二及后续选择。

| 策略 | 行为 |
| --- | --- |
| `priority_weighted` | 将同一优先级下各路由的配置权重作为目标流量比例。例如权重分别为 75 和 25 时，在有代表性的请求量下目标比例为 75:25。 |
| `adaptive` | 以配置权重为基础，使用近 15 分钟内实际发起的调用动态调整有效权重。单条路由积累 5 个样本后开始自适应，近期成功率和成功请求延迟共同影响流量占比；调整范围设有上下限，避免线路饿死或比例剧烈波动。 |
| `quality` | 每次固定先尝试质量分最高的 Provider；只有评分相同时才用权重决定先后。 |
| `cost` | 每次固定先尝试成本效率分最高的 Provider。分数越高表示越省、越优先。 |
| `priority_only` | 严格按 Provider 列表执行主备切换，正常情况下不分流。 |
| `balanced` | 使用 `权重 + 质量分 + 成本分` 作为有效权重进行概率分流，以兼容旧配置；新配置通常应选择固定比例或自适应。 |

Provider 连接信息和项目限制仍按线路配置。编辑单条 Provider 线路时，只调整上游模型、项目作用域、粘性会话和状态；整体策略、权重与评分统一在模型策略区域编辑。`all` 对所有项目开放；`include` 仅对选中的项目开放；`exclude` 对选中项目之外的其他项目开放。系统会先按项目过滤路由，再执行流量分配和故障转移，界面中的目标占比也会基于过滤后的可用 Provider 重新计算。

如果私有项目只能调用内部模型，可为内部 Provider 路由设置 `include`，并选中这些私有项目；再为对应的外部 Provider 路由设置 `exclude`，选择同一批项目。这样私有项目只会命中内部线路，其他项目仍走外部 Provider。

项目作用域也会影响模型发现：除了模型启用状态和 API Key 模型白名单外，只有调用方 API Key 所属项目至少存在一条已启用且符合项目作用域的路由时，对外模型才会出现在 `GET /v1/models` 中。

## Provider 资源自动恢复

连续失败达到 `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` 次的 Provider 资源会被摘除：停止接收流量并进入冷却。恢复过程全自动，无需管理员介入。

| 阶段 | 行为 |
| --- | --- |
| 摘除 | 该资源在 `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` 内不参与路由 |
| 半开 | 冷却期满后，只放行一个请求作为试探，其余请求仍被拒绝 |
| 恢复 | 试探请求成功到达上游即关闭熔断、重置失败计数，并产生 `provider_resource_recovered` 告警 |
| 重新摘除 | 试探失败会立即进入下一轮冷却，每次翻倍，上限为 `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` |

只有试探请求自身成功才能关闭熔断；熔断触发时已经在途的请求无论结果如何都无法关闭它。客户端中途断连、策略拒绝、模型不支持这几种情况既不计入失败、也不清零失败计数，因此「失败、断连、失败」这样的交替模式仍然会触发熔断。

在控制台对资源执行「测试」时，如果适配器支持主动探测，资源仍会立即恢复，因为该探测会发起一次真实的上游请求。禁用资源仍然是管理员的最高优先级操作：被禁用的资源无论上游是否正常，都不会被自动恢复。

## 请求用量审计

「请求日志」中的每条记录会显示 Token 总量和估算成本。详情面板会在上游返回数据时保留完整计费明细，包括缓存读、缓存写和音频输入 Token，以及推理、音频、接受预测和拒绝预测输出 Token；Provider 未返回的字段显示为 0。输入和输出总量已经包含对应明细，不能再次相加，否则会重复计算。

## 指标

TokenHub 可以在 `GET /metrics` 暴露 Prometheus 指标。该功能默认关闭，需设置 `TOKENHUB_METRICS_ENABLED=true` 才会启用；关闭时不采集任何数据，端点返回 404。该端点始终需要鉴权：指标会泄露模型名、Provider 和资源标识以及花费，因此不允许匿名访问。请使用 `Authorization: Bearer <token>`，令牌取自 `TOKENHUB_METRICS_TOKEN`；该变量为空时回落到管理员令牌。建议配置独立令牌，避免把管理员凭据放进 Prometheus 抓取配置。通过查询参数传令牌会被拒绝，因为它会被记进访问日志。

| 指标 | 类型 | 含义 |
| --- | --- | --- |
| `tokenhub_gateway_requests_total` | counter | 逻辑模型 API 请求数。一次请求即使经过多个候选失败转移也只计一次。 |
| `tokenhub_gateway_request_duration_seconds` | histogram | 端到端耗时，包含失败转移。分桶上限为 300 秒。 |
| `tokenhub_gateway_requests_in_flight` | gauge | 正在处理的模型 API 请求数，不含管理后台流量和抓取请求。 |
| `tokenhub_gateway_tokens_total` | counter | 按类型统计的 Token：`prompt`、`completion`、`cached`、`cache_write`、`reasoning`。 |
| `tokenhub_gateway_cost_usd_total` | counter | 估算成本，与用量记录采用同一套价格。 |

同时暴露 Go 运行时和进程指标。

**Token 各类型之间不是互斥划分，不能相加。** `prompt` 已经包含 `cached` 和 `cache_write`，`reasoning` 是 `completion` 的子集，相加会重复计算。

在路由之前就被拒绝的请求（API Key 无效、额度耗尽、模型不存在）只增加请求计数。它们没有到达任何 Provider，因此不产生 Token、成本和耗时。目录中不存在的模型名会被记为 `unknown` 而不是原样上报，避免客户端用随机模型名刷高时间序列数量。

标签为 `model`、`provider_type`、`provider_id`、`resource_id`、`status_code`、`error_code` 和 `stream`。设置 `TOKENHUB_METRICS_PROJECT_LABEL=true` 会追加 `project_id`，使每个网关指标的时间序列数量按活跃项目数成倍增长；除非确实需要按项目看板，否则建议保持关闭，按 Key 的归因请改用用量报表。

如果需要 push 而不是抓取，可以让 OpenTelemetry Collector 的 `prometheus` receiver 抓取该端点再转发；网关自身只提供 Prometheus exposition 格式。

## Prompt Cache 计价

模型目录支持按每百万 Token 配置可选的缓存读取价格。配置后，命中缓存的输入 Token 按该价格估算成本；留空时，DeepSeek V4 Pro 按标准输入价的约 0.83% 估算，其他 DeepSeek 模型按 2% 估算，其余非 Embedding 模型按 10% 估算。模型定价表会标记估算值，并在悬停时说明采用的比例。

## 候选模板恢复

删除对外模型会移除其数据库记录及路由，但不会修改 `data/model-catalog.yaml` 或 `TOKENHUB_MODEL_CATALOG_FILE` 指向的文件。后端启动时会再次同步其中的候选元数据。管理员也可以在「模型目录」的「候选模板库」页签使用「恢复候选模板」刷新这些元数据，同时保留自定义对外模型。恢复模板不会把模型引入 Provider、创建映射，也不会将其发布到 `GET /v1/models`。

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
