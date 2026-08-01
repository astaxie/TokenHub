# 管理员指南

Language: [English](../administrator-guide.md) | 简体中文 | [日本語](../ja/administrator-guide.md)

本指南面向将 TokenHub 作为企业 AI 网关运行的平台管理员、安全运维和基础设施负责人。

## 管理员范围

| 区域 | 责任 |
| --- | --- |
| Provider Channels | 配置上游连接、引入模型库存并维护 Provider 真实成本 |
| 模型目录 | 从内置模型目录挑选模型、创建对外 API 契约、选择初始 Provider 线路并设置统一对客价格 |
| Routing Policies | 细调 Provider 映射、优先级、权重、项目作用域和故障转移策略 |
| Projects and Teams | 定义 Key、额度和成本归因的组织边界 |
| Identity Sources | 配置 OAuth 或 OIDC 企业登录 |
| Security and Audit | 审查请求日志、后台操作、Key 轮换和策略变更 |

## 生产上线顺序

1. 至少配置一个身份源，并保留可控的管理员账号。
2. 添加上游 Provider，例如 `OpenAI Production`、`Azure East US` 或 `Internal Model Gateway`，并引入它可提供的上游模型。
3. 为每个已引入的 Provider 模型记录真实的输入、缓存读取和输出成本，用于审计。
4. 创建需要向业务开放的对外模型，并设置统一对客价格。
5. 将每个对外模型路由到一个或多个已引入的 Provider 模型。
6. 创建团队、项目、成本中心和默认额度策略。
7. 用 Model Playground 和请求日志验证链路。
8. 在大规模发放 Key 前检查用量归因。

## API Key 归属与用量归因

发放 API Key 时，应在「归属用户」中选择实际使用人。发放人仍保留在审计元数据中，但 Key 的用量会统计到归属用户。平台管理员可以选择任一启用用户；团队负责人只能选择本团队的启用用户；普通用户只能把 Key 归属给自己。

每条新用量记录都会固化当时的归属用户，因此以后转移归属或删除 Key 不会改写已记录的历史。对该字段上线前的旧记录，系统依次回退到 Key 当前归属用户、旧的发放人、项目负责人，最后显示为「未知」。个人排行会分别展示用量中实际出现过的 Key 数，以及当前归属且未吊销的 Key 数。

## Provider 目录可用性

TokenHub 会把最后一次成功加载的 Provider 目录保存在数据库中。每次后端启动时，系统都会校验并加载配置的本地 `provider-catalog.json`，然后原子替换数据库快照。普通「Provider 渠道」请求只读取数据库快照，管理员也可以手动刷新同一份本地目录。若本地目录读取、解析或完整性校验失败，TokenHub 会继续使用最后一次有效快照。

## Provider 库存、模型目录与发布

TokenHub 将模型生命周期拆成三个独立的管理区域：

| 管理区域 | 含义 |
| --- | --- |
| **Provider Channels** | 上游连接及其已引入的模型库存。基于目录创建 Provider 时必须至少选择一个模型，但只引入库存不会对客户端开放；自定义 Provider 可以先空建，待上游连接可用后再加载模型。 |
| **模型目录** | 只管理提供给业务应用的对外模型，即 API 契约。新建时先从内置模型参考目录选择模板，也可选择空白自定义模型；然后选择一个或多个已引入 Provider 模型，同步生成初始路由。这里的价格是统一对客价格，不随实际命中的 Provider 路由变化。 |
| **Routing Policies** | 管理对外模型的 Provider 映射，并细调优先级、权重、项目作用域、流量分配和故障转移策略。 |

各区域的职责仍然分开：先添加 Provider 并引入库存；然后从内置参考目录挑选模型，创建它的对外契约、选择至少一条初始 Provider 线路并设置统一对外价格。所选模板会带出模型名、能力、上下文和建议价格，保存前均可调整。创建后，映射的新增、修改和删除只在 Routing Policies 中进行。模型目录只保留只读的上游摘要，行内入口会打开 Routing Policies 并筛选当前对外模型；Provider 渠道列表的“配置路由”会进入完整的 Routing Policies 页面，以便新增映射。例如，可以向客户端开放 `DeepSeek`，但将它路由到 `OpenAI Production / gpt-4.5`。同一个 Provider 模型可以支持多个对外别名，一个对外模型也可以路由到多个 Provider。

Provider 模型价格代表真实上游成本，用于内部审计；模型目录价格代表统一对外收费，用于客户计费估算、额度计算、指标和用量报表。路由只选择上游实现，不会改变对外价格。

当 Provider 渠道、模型目录或路由策略还没有配置数据时，控制台会展示同一套三步引导：引入 Provider 库存、从内置的 165 个模型中创建对外模型、再配置路由。主操作按钮始终指向最早尚未完成的前置步骤，避免管理员进入当前还无法完成的表单。

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

只有试探请求自身成功才能关闭熔断；熔断触发时已经在途的请求无论结果如何都无法关闭它。计不计入失败，取决于这次上游失败说明了资源的什么问题，而不是调用方看到的状态码。客户端中途断连、策略拒绝、模型不支持、上游判定请求本身格式错误，以及其它「问题出在请求而非账号」的情况，既不计入失败、也不清零失败计数，因此「失败、断连、失败」这样的交替模式仍然会触发熔断。凭据被拒、账号无法计费、限流以及上游自身故障则会计入失败。

在控制台对资源执行「测试」时，如果适配器支持主动探测，资源仍会立即恢复，因为该探测会发起一次真实的上游请求。禁用资源仍然是管理员的最高优先级操作：被禁用的资源无论上游是否正常，都不会被自动恢复。

## 上游错误分类

一次上游失败要分别回答三个问题，单个状态码无法同时答对：告诉调用方什么、路由要不要换下一个候选、这次尝试要不要计入 Provider Resource 的失败次数。格式错误的请求换任何 Provider 都是错的，因此直接返回给调用方、不再切换；凭据被拒只属于某一个账号，因此请求继续切换、并由该账号承担失败计数。

| 上游 | 调用方看到 | 错误码 | 是否换候选 | 是否计入资源失败 |
| --- | --- | --- | --- | --- |
| `400`、`422` | 同一状态码 | `provider_invalid_request` | 否 | 否 |
| `401`、`403` | `502` | `provider_auth_error` | 是 | 是 |
| `402` | `502` | `provider_payment_required` | 是 | 是 |
| `404` | `502` | `provider_model_not_found` | 是 | 否 |
| `408` | `504` | `provider_upstream_timeout` | 是 | 是 |
| `413` | `413` | `provider_invalid_request` | 否 | 否 |
| `429` | `429`，并带 `Retry-After` | `provider_rate_limited` | 是 | 是 |
| `502`、`503`、`504` | 同一状态码 | `provider_upstream_unavailable` | 是 | 是 |
| 其它 `5xx` | `502` | `provider_upstream_error` | 是 | 是 |
| 其它 `4xx` | `502` | `provider_error` | 否 | 否 |

上游的 `401` / `403` 不会原样透传：它表示网关自己配置的该 Provider 凭据被拒绝，调用方看到 `401` 会误以为自己的 TokenHub API Key 失效。出于同样原因，这两种情况不会把上游响应体返回给调用方——Provider 常在其中回显被拒绝的密钥。原始状态码会记录在每次路由尝试的 `upstream_status` 上，供运维查看。

每次路由尝试都会同时记录两者：`status_code` 是告诉调用方的状态，`upstream_status` 是 Provider 实际返回的状态。

## 请求用量审计

「请求日志」中的每条记录会显示 Token 总量和对外计费金额。拥有全局运维可见权限的管理员还可以在详情面板查看根据命中 Provider 模型计算的真实成本；其他用户不会收到该成本字段。详情面板会在上游返回数据时保留完整计费明细，包括缓存读、缓存写和音频输入 Token，以及推理、音频、接受预测和拒绝预测输出 Token；Provider 未返回的字段显示为 0。输入和输出总量已经包含对应明细，不能再次相加，否则会重复计算。

## 指标

TokenHub 可以在 `GET /metrics` 暴露 Prometheus 指标。该功能默认关闭，需设置 `TOKENHUB_METRICS_ENABLED=true` 才会启用；关闭时不采集任何数据，端点返回 404。该端点始终需要鉴权：指标会泄露模型名、Provider 和资源标识以及花费，因此不允许匿名访问。请使用 `Authorization: Bearer <token>`，令牌取自 `TOKENHUB_METRICS_TOKEN`；该变量为空时回落到管理员令牌。建议配置独立令牌，避免把管理员凭据放进 Prometheus 抓取配置。通过查询参数传令牌会被拒绝，因为它会被记进访问日志。

| 指标 | 类型 | 含义 |
| --- | --- | --- |
| `tokenhub_gateway_requests_total` | counter | 逻辑模型 API 请求数。一次请求即使经过多个候选失败转移也只计一次。 |
| `tokenhub_gateway_request_duration_seconds` | histogram | 端到端耗时，包含失败转移。分桶上限为 300 秒。 |
| `tokenhub_gateway_requests_in_flight` | gauge | 正在处理的模型 API 请求数，不含管理后台流量和抓取请求。 |
| `tokenhub_gateway_tokens_total` | counter | 按类型统计的 Token：`prompt`、`completion`、`cached`、`cache_write`、`reasoning`。 |
| `tokenhub_gateway_cost_usd_total` | counter | 使用模型目录价格计算的统一对外计费估算；Provider 真实成本只保留在有权限的请求审计中，不进入该指标。 |
| `tokenhub_gateway_trace_completions_total` | counter | 已完成调用在链路导出中的去向：`converted` 或 `dropped`。仅在开启追踪时存在。 |
| `tokenhub_gateway_trace_spans_total` | counter | span 在 OTLP 导出中的结果：`exported` 或 `failed`。仅在开启追踪时存在。 |

同时暴露 Go 运行时和进程指标。

**Token 各类型之间不是互斥划分，不能相加。** `prompt` 已经包含 `cached` 和 `cache_write`，`reasoning` 是 `completion` 的子集，相加会重复计算。

在路由之前就被拒绝的请求（API Key 无效、额度耗尽、模型不存在）只增加请求计数。它们没有到达任何 Provider，因此不产生 Token、成本和耗时。目录中不存在的模型名会被记为 `unknown` 而不是原样上报，避免客户端用随机模型名刷高时间序列数量。

标签为 `model`、`provider_type`、`provider_id`、`resource_id`、`status_code`、`error_code` 和 `stream`。上游失败不再统一上报为 `status_code="502"` 加 `provider_error`，而是使用「上游错误分类」一节列出的状态码与错误码，按旧取值匹配的看板和告警需要相应更新。设置 `TOKENHUB_METRICS_PROJECT_LABEL=true` 会追加 `project_id`，使每个网关指标的时间序列数量按活跃项目数成倍增长；除非确实需要按项目看板，否则建议保持关闭，按 Key 的归因请改用用量报表。

如果指标需要 push 而不是被抓取，可以让 OpenTelemetry Collector 的 `prometheus` receiver 抓取该端点再转发。链路追踪是另一路信号，由网关直接推送，见下节。

## 链路追踪导出

TokenHub 可以通过 OTLP/HTTP 为每次网关调用导出一条 OpenTelemetry 链路。每条链路包含一个代表该请求的根 span，以及每个进入调用流程的候选各一个 generation span，因此一次故障转移会同时呈现两个候选，以及各自消耗的 Token 与成本。因容量不足而被跳过的候选从未触达 Provider，改为记录成根 span 上的一个 event。指标只能告诉你延迟升高了，链路能告诉你是哪个账号服务了这次请求、花了多少钱。该功能默认关闭：它会把运行数据发送到另一个系统。

`TOKENHUB_TRACING_ENDPOINT` 需填写 OTLP traces 的完整信号级 URL（含完整路径），网关按原样使用、不追加任何路径——因为猜测出来的路径后缀只会表现为静默的 404，而不是启动报错。不带路径，或带有 query、fragment、userinfo 的 URL 会在启动时被拒绝，而不是被悄悄导出到别处。任何 OTLP/HTTP 后端都可以接入；**不需要 OpenTelemetry Collector**，因为网关直接使用 OTLP over HTTP，而不是 gRPC。

接入 Langfuse：

```bash
TOKENHUB_TRACING_ENABLED=true
TOKENHUB_TRACING_ENDPOINT=https://cloud.langfuse.com/api/public/otel/v1/traces
TOKENHUB_TRACING_HEADERS="Authorization=Basic $(printf '%s' 'pk-lf-...:sk-lf-...' | base64),x-langfuse-ingestion-version=4"
```

属性映射面向 Langfuse v4 的摄入模型；该版本对自建部署已 GA，并且是 2026-04-14 之后创建的 Langfuse Cloud 组织的默认版本。自建 Langfuse v4 除 PostgreSQL、Redis 和对象存储外，还要求 ClickHouse 25.12 或更高版本。TokenHub 不会把 Langfuse 打包进自己的 Compose 文件：那是另一套有独立升级周期的有状态服务，把两者耦合会让一次 Langfuse 迁移变成网关停机。

是否导出提示词和响应，与是否开启追踪是两个独立决策，`TOKENHUB_TRACING_CAPTURE_PAYLOADS` 默认关闭。关闭时链路依然携带状态码、耗时、每次尝试所用的 Provider 与资源、Token、成本、传输方式和上游请求 ID。开启后，请求与响应体会经过与本地 payload 日志相同的脱敏与截断；上游错误文本同样按 payload 对待，因为上游错误里可能夹带响应体、URL 或账号标识。

Token 用量与成本只挂在 generation span 上，绝不挂在根 span 上。Langfuse v4 按 observation 聚合，若在根上重复携带，项目总量中的每个 Token 和每一分钱都会被算两遍。Token 计数在导出时还会被改写为互斥的分桶。TokenHub 的输入与输出总数本身已包含各自的明细类别——输入侧的缓存命中、缓存写入、音频，输出侧的推理、音频、预测——而 Langfuse 会把收到的分桶直接相加，因此每项明细都要从所属总数中扣除，并以剩余额度封顶。导出的成本只有对外计费金额；Provider 自身成本不导出，它被 TokenHub 有意限制在特权请求审计中。

![Langfuse 中的一次故障转移链路：根请求 span、两次失败的不可达账号尝试，以及最终提供服务、带有自身 Token 数与成本的 generation](../assets/screenshots/tracing-langfuse-trace-en.png)

链路 ID 与 span ID 都由请求 ID 推导而来，因此返回给客户端的 `x-request-id` 无需任何对照表即可直接定位到对应链路。但这只是查找便利，**不是**去重保证：Langfuse 对已见过的 span ID 仍可能生成重复 observation。因此导出重试被关闭，投递语义是 at-most-once——因瞬时故障丢失的批次不会重发。对携带 Token 数与花费的链路来说，成本被算多了才是更糟的失败；何况这条管道在饱和时本来就是丢弃而非等待。Playground 流量会带上 `playground` 标签导出，便于从成本分析中排除；在路由之前就被拒绝的请求同样会导出，因为额度或准入失败往往正是你要排查的对象。

![Langfuse 链路列表中并列展示的网关流量、Playground 流量与被拒绝请求，可按标签过滤](../assets/screenshots/tracing-langfuse-list-en.png)

导出永远不会拖慢请求。完成事件先入队，由独立的 goroutine 转换成 span；队列满时直接丢弃该事件，而不是让它排队等待。丢弃数计入 `tokenhub_gateway_trace_completions_total` 的 `outcome="dropped"`，并且每分钟最多记录一条日志，这样 Langfuse 里的空档就能与「网关本来就没有流量」区分开。是否真正送达则单独计入 `tokenhub_gateway_trace_spans_total`——队列打满和后端拒收是两类不同的问题，处理方式也不同。图片生成任务目前尚未纳入追踪：它们在 worker 上异步完成，需要先单独设计幂等方案。

## Prompt Cache 计价

模型目录支持按每百万 Token 配置可选的缓存读取价格。配置后，命中缓存的输入 Token 按该价格估算成本；留空时，DeepSeek V4 Pro 按标准输入价的约 0.83% 估算，其他 DeepSeek 模型按 2% 估算，其余非 Embedding 模型按 10% 估算。模型定价表会标记估算值，并在悬停时说明采用的比例。

## 目录元数据恢复

删除对外模型会移除其数据库记录及路由，但不会修改 `data/model-catalog.yaml` 或 `TOKENHUB_MODEL_CATALOG_FILE` 指向的文件。后端启动时会再次同步其中的跟踪目录元数据；管理员也可以在「系统设置 → 基础设置 → 同步模型参考目录」中免重启触发同一操作。该同步不会把模型引入 Provider、创建路由或将其发布到 `GET /v1/models`；这些操作仍需在各自的管理区域中显式完成。

## 外部账单连接器

平台管理员可在「成本账单」中管理外部账单源。TokenHub 支持阿里云 `QueryInstanceBill`、NewAPI 额度数据和 OneAPI 兼容日志源。连接器可以测试连接、立即同步、按分钟设置定时同步、停用并保留历史记录，也可以随后重新启用。

阿里云连接器需要账单 RPC Base URL、AccessKey ID、AccessKey Secret、源时区，可选填写 Product Code。TokenHub 使用 HMAC-SHA1 为每个 RPC 请求签名，并按账期逐月推进。NewAPI 连接器需要 Base URL、访问令牌、`New-Api-User` 用户 ID、币种，以及一个币种单位对应的 Quota 数量；TokenHub 会按照官方文档携带鉴权请求头调用 `GET /api/data/self`，并自动把同步范围切分为最长 30 天的窗口。OneAPI 兼容连接器需要 Base URL、API Token、日志路径、币种和 Quota 换算值。所有连接器都可以设置每秒请求上限；临时网络错误、`429` 和 `5xx` 会使用有上限的指数退避重试。

手动同步可以传入 RFC 3339 格式的 `from` 和 `to`。未指定时间范围时，从上一次成功同步的结束时间继续。每页保存一次上游 Cursor，因此失败重试会从断点继续当前区间，而不是重新开始。规范化账单以 `(connector_id, external_id)` 作为幂等键，并保留币种、源时区、税费、折扣、退款、账期和用量起止时间。最近同步会展示页数、请求尝试数、新增/更新记录数和经过清理的失败代码。

连接器凭证和原始账单快照都使用 `TOKENHUB_SECRET_KEY` 派生的 AES-GCM 密钥加密，不会由管理 API 返回，也不会写入审计 Payload。重启和多副本部署时必须保持该密钥稳定。相关端点包括 `GET/POST /api/admin/billing/connectors`、`PATCH /api/admin/billing/connectors/{id}`、`POST /api/admin/billing/connectors/{id}/test`、`POST /api/admin/billing/connectors/{id}/sync`、`GET /api/admin/billing/records` 和 `GET /api/admin/billing/sync-runs`。

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
