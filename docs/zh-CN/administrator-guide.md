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
2. 添加上游 Provider，例如 `OpenAI Production`、`Azure East US` 或 `Internal Model Gateway`。保存前先用「测试连接」校验 Base URL 和 API Key，并查看实测响应耗时，再引入它可提供的上游模型。
3. 为每个已引入的 Provider 模型记录真实的输入、缓存读取和输出成本，用于审计。
4. 创建需要向业务开放的对外模型，并设置统一对客价格。
5. 将每个对外模型路由到一个或多个已引入的 Provider 模型。
6. 创建团队、项目、成本中心和默认额度策略。
7. 用 Model Playground 和请求日志验证链路。
8. 在大规模发放 Key 前检查用量归因。

## 模型演练场诊断

从控制台打开「模型演练场」，可以通过与网关流量相同的路由和 Provider 适配器验证模型。每个 assistant 轮次都保留独立的紧凑诊断摘要，包括返回模式、网关实测首 Token 时间（TTFT）、输出吞吐、总耗时、完整上下文输入 Tokens、输出 Tokens、估算成本、本地完成时间和 Request ID。展开「诊断详情」可查看毫秒级时间及实际响应详情。除非用户明确导出，否则会话只保留在当前浏览器页面。

TokenHub 会向演练场输出统一的 SSE 事件格式。所选上游支持流式时，TTFT 从网关接收请求计到首个内容增量，输出吞吐按首个到最后一个内容增量之间的时长计算。上游只支持非流式响应时，TokenHub 自动退化为缓冲模式，将 TTFT 标为不适用，并展示端到端输出吞吐，不伪造首 Token 指标。停止请求会保留已生成的部分文本并把候选标为已取消；只有 Provider 返回权威 Token 用量时才展示对应数值。

重跑某个 assistant 轮次会为该轮创建新候选，并移除后续轮次，防止新分支悄悄复用旧上下文。切换模型默认新建会话；如需沿用上下文，必须显式选择。参数控件以所选模型声明的 `supported_parameters` 为准，避免发送模型目录已标记为不支持的参数。

所有获准使用演练场的用户都能查看性能、用量、Request ID 以及自己的响应详情。Provider、资源、上游 Request ID 和逐次路由尝试仅对拥有路由读取权限的角色展示。成本会明确标为「估算」，因为它采用对外模型配置价格，而不是上游账单。

## 内容安全策略

在「安全策略 > 内容安全」中，可以创建适用于全部项目或指定项目的策略。一条策略可以组合关键词或正则表达式匹配、敏感数据检测，以及可选的 Qwen3Guard 模型检测器。所有检测项会共同判定，并采用命中动作中最严格的一项：`block` 高于 `mask`，`mask` 高于 `audit`。保存后立即生效。

确定性检测器在 TokenHub 内部运行。启用 Qwen3Guard 检测器后，TokenHub 会把待检测的用户可见请求文本发送到 `TOKENHUB_GUARDRAIL_MODEL_URL` 配置的服务。该服务应部署在获准的数据边界内，并评估其传输、日志和保留策略；远端服务保留的副本不受 TokenHub 管理。URL 留空时不会发起模型调用，并按各模型检测项配置的不可用行为处理。

敏感数据检测覆盖带标签或经过结构校验的中国大陆身份证号、手机号、电子邮箱、银行卡号、凭证与私钥、姓名、地址和出生日期等样例。日期合法性、身份证校验位和 Luhn 校验等规则用于降低常见数字误报。策略广泛启用前，应使用「测试策略」同时验证有代表性的正例和反例。

当前请求侧拦截覆盖 `/v1/chat/completions`、`/v1/responses` 和 `/v1/messages`，也包括模型演练场发出的请求。TokenHub 会在路由到 Provider 前检查普通的用户可见文本。本版本暂不检查结构化工具参数、JSON 载荷中的值、需要代码语义解析的内容，也不检查 Provider 响应。安全检查本身不再设置独立的文本大小上限，请求仍受已配置的请求体大小限制。确定性检测还会应用按规则复杂度加权的累计工作预算和命中数量预算，避免超长文本、高开销表达式或密集脱敏命中的病态组合长期占用 CPU 或内存；超限时返回 HTTP 503 `guardrail_evaluation_budget_exceeded`，普通长上下文配合适量规则仍可正常处理。

策略阻断请求时，兼容 API 返回 HTTP 403 和 `guardrail_blocked`。错误详情包含 `categories`、`reason_codes` 和 `policy_matches`；每条策略命中会标明策略、检测项、检测器类型、分类和原因码。响应还包含用于关联审计记录的 `request_id`，但不会返回命中的原始文本。模型演练场展示相同的策略与原因信息，方便管理员反馈可复现的问题，而不只是看到“请求已被内容安全策略阻断”。

## API Key 归属与用量归因

发放 API Key 时，应在「归属用户」中选择实际使用人。发放人仍保留在审计元数据中，但 Key 的用量会统计到归属用户。平台管理员可以选择任一启用用户；团队负责人只能选择本团队的启用用户；普通用户只能把 Key 归属给自己。

每条新用量记录都会固化当时的归属用户，因此以后转移归属或删除 Key 不会改写已记录的历史。对该字段上线前的旧记录，系统依次回退到 Key 当前归属用户、旧的发放人、项目负责人，最后显示为「未知」。个人排行会分别展示用量中实际出现过的 Key 数，以及当前归属且未吊销的 Key 数。

## 本地 Agent 只读成本访问

不要为了采集用量而把管理员会话或模型调用 API Key 交给自动化 Agent。应创建独立的 `tha_` 分析凭证，尽可能限制到单个项目，并可单独吊销。[Agent Token 成本 API 指南](agent-token-cost-api.md)说明凭证生命周期、过滤、聚合、JSON/CSV Schema、快照分页、增量 watermark、审计行为和查询限制。

## 单 Key RPM 与 TPM 限制

每个 API Key 都可以设置可选的每分钟请求数（RPM）和每分钟 Token 数（TPM）限制。未设置或 `null` 表示继承适用的全局、项目和团队策略；`0` 表示不增加 Key 自身的限制，但不能绕过上层限制；正数表示增加 Key 级限制。当多个正数限制同时适用时，TokenHub 执行其中最严格的值。禁用 Key 后，无论限制值如何，所有请求都会被拒绝。

RPM 在调用 Provider 前扣减。TPM 同时按请求的预估输入量和最大输出量进行预留；文本请求未显式指定最大输出时，会预留 4,096 个输出 Token。请求结束后，系统按 Provider 返回的总 Token 数结算；若无总数，则使用提示词与补全 Token 之和。缓存与推理 Token 已包含在这些总数中，不会重复累加。失败或中断的请求会返还未使用的预留量。

超过限制时返回 HTTP 429，错误码为 `api_key_rpm_exceeded` 或 `api_key_tpm_exceeded`，并附带 `Retry-After` 以及对应的 `X-RateLimit-Limit-*`、`X-RateLimit-Remaining-*` 和 `X-RateLimit-Reset-*` 响应头。分钟桶保存在数据库中，在 SQLite 和 PostgreSQL 上都会由多个 TokenHub 实例共享。指标只暴露短哈希形式的 Key 引用，绝不会包含完整 API Key。

## Provider 目录可用性

TokenHub 会把最后一次成功加载的 Provider 目录保存在数据库中。每次后端启动时，系统都会校验并加载配置的本地 `provider-catalog.json`，然后原子替换数据库快照。普通「Provider 渠道」请求只读取数据库快照，管理员也可以手动刷新同一份本地目录。若本地目录读取、解析或完整性校验失败，TokenHub 会继续使用最后一次有效快照。

## Claude Code 归因块处理

Claude Code 可能在 Anthropic Messages 请求的 `system` 数组开头插入归因文本块。该块包含可能随请求变化的客户端元数据，可能导致第三方上游无法复用原本稳定的提示词前缀。

每个 Provider 都可以设置 `claude_code_attribution_policy`。新建 Anthropic 官方 Provider 时默认使用 `preserve`；明确非官方的 Provider 默认使用 `strip`，以提高第三方上游的提示词前缀缓存复用率；来源不明的自定义 Anthropic 端点默认使用 `preserve`。已有 Provider 未配置该字段时也继续保留归因块。`strip` 只在第一个顶层 `system` 元素的 `type` 为 `"text"`，且文本严格以 `x-anthropic-billing-header:` 开头时移除该元素。字符串形式的 `system` 提示词、后续元素、带前导空格的文本及其他元素类型均不会被移除。

Provider Resource 默认继承 Provider 策略，也可以通过 `options.claude_code_attribution_policy` 将策略覆盖为 `preserve` 或 `strip`；省略该 Resource 选项即可恢复继承。TokenHub 会为每次路由尝试单独应用实际生效的策略，因此故障切换后的 Resource 会收到原始请求，再执行自身策略。审计载荷同样保留原始请求。`POST /v1/messages/count_tokens` 不会选择具体的 Provider Resource，因此仍按原始请求计数。

## Codex 用量重置资格

对于已启用的 OpenAI Codex Subscription 账号，打开「Provider 渠道」，编辑对应 Provider，再展开「高级 > 订阅额度」。账号卡片会显示 OpenAI 返回的权威剩余重置次数及最近到期时间。「重置套餐用量」会先弹出二次确认，确认后消耗 1 次不可恢复的资格，并重置当前符合条件的 Codex 用量窗口；该操作不会更改 ChatGPT 计费套餐。操作成功或幂等重试成功后，界面会重新拉取额度和重置资格明细。

重置操作的幂等状态会作为 `codex-quota-reset-operations` 类型的 `AdminResource` 记录写入现有管理资源表，因此不需要数据库结构迁移。成功和失败记录会持续保留且不会自动删除，因为它们用于阻止重放请求再次消耗重置资格；应将这些记录纳入正常的数据库保留和备份策略。升级时必须保留数据库。`pending` 或 `unknown` 记录会阻止同一账号发起不同的重置，服务重启后仍会在控制台恢复显示；在 OpenAI 返回确定结果前，只能使用相同的幂等键、预期次数和资格 ID 重试。

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

### 作用域路由策略

可在「作用域策略」中为全局网关、项目或 API Key 绑定独立路由策略。TokenHub 按 API Key、项目、全局的顺序解析且只选择一个有效策略。一旦命中更高优先级的绑定，即使该策略已停用、存在冲突或筛选后无合格候选，也会安全拒绝而不会回退到低优先级作用域。每个作用域对象最多绑定一个策略；未绑定的策略定义可以提前准备，不影响流量。

模型访问权先于路由执行。项目和 API Key 均支持 `inherit`（继承）与 `restricted`（限制）模式。限制列表会与所有上层列表取交集，因此 API Key 不能扩大项目权限；`restricted` 且列表为空表示禁止全部模型。为保持兼容，在访问模式上线前创建、且模式与列表均为空的记录仍按继承处理。`GET /v1/models` 使用同一有效访问范围，并要求至少存在一条被有效路由策略允许的路由。

作用域策略可约束模型名、Provider、Provider Resource、必需路由标签、资源地域和资源环境，也可覆盖路由算法。路由标签在模型路由上配置，地域与环境在 Provider Resource 上配置。已有的路由项目作用域会与这些约束取交集。流量分配、会话/缓存亲和、半开恢复和故障转移都在筛选之后运行，不会把已排除路由重新加回候选池。因此内部模型专属策略会安全失败，而不会静默跨界到外部 Provider。

策略预览/模拟面板接收项目、API Key 和模型，展示有效策略、访问判定、最终路由，以及每个候选的安全允许/排除原因。策略失败使用 `routing_policy_unavailable`、`routing_policy_conflict` 和 `routing_policy_no_candidate` 等可诊断错误码，不暴露凭据。请求日志记录 `routing_policy_id`、`routing_policy_scope` 和 `routing_policy_priority`；通用策略创建/更新/删除以及显式绑定/解绑操作也会写入管理员审计事件。

管理 API 通过 `/api/admin/resources/routing-policies` 提供通用资源 CRUD，并提供 `POST /api/admin/routing-policies/{id}/bind`、`POST /api/admin/routing-policies/{id}/unbind` 和 `POST /api/admin/routing-policies/simulate`。同一执行规则适用于 OpenAI 兼容模型请求、Anthropic Messages、图像生成和管理员 Playground。

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
| `tokenhub_gateway_route_attempts_total` | counter | 物理候选尝试数。`rate(route_attempts_total) / rate(routed_requests_total)` 即为平均失败转移深度；`routed_requests_total` 只统计真正发起过尝试的请求，因此从未到达任何 Provider 的拒绝流量不会稀释该比率。`invoked` 标签可单独区分因容量不足被跳过的候选。`status_code` 为网关映射后的状态（上游 401 会报告为 502）；原始上游状态见 `RouteAttemptLog`。 |
| `tokenhub_gateway_attempt_duration_seconds` | histogram | 单个被调用（invoked）尝试的完整耗时，围绕整个尝试测量：上游传输、流式翻译与写入客户端。流式调用因此包含慢客户端背压时间；网关自身开销单独见 `overhead_seconds`。不包含因容量被跳过的候选。 |
| `tokenhub_gateway_routed_requests_total` | counter | 至少发起过一次候选尝试的逻辑请求数——失败转移深度比率的尝试承载分母。其 `provider_type` 标签为最后一个尝试的候选；跨 Provider 失败转移时，请按 `model` 而非 `provider_type` 聚合深度比率。 |
| `tokenhub_gateway_overhead_seconds` | histogram | 网关自身开销的近似值：端到端耗时减去被调用尝试耗时之和，负值截断为 0。已在路由阶段被接纳、但在任何尝试前失败的请求，其开销计为完整端到端耗时。图像作业的端到端耗时包含队列等待，因此其开销为上限估计。 |
| `tokenhub_gateway_requests_in_flight` | gauge | 正在处理的模型 API 请求数，不含管理后台流量和抓取请求。 |
| `tokenhub_gateway_tokens_total` | counter | 按类型统计的 Token：`prompt`、`completion`、`cached`、`cache_write`、`reasoning`。 |
| `tokenhub_gateway_cost_usd_total` | counter | 使用模型目录价格计算的统一对外计费估算；Provider 真实成本只保留在有权限的请求审计中，不进入该指标。 |
| `tokenhub_gateway_rate_limit_hits_total` | counter | 按实际生效的策略作用域和限制类型统计被拒请求；仅 `api_key` 限制使用短哈希 `key_ref`，继承自全局、项目和团队的限制统一使用 `none`，以控制时间序列基数。 |
| `tokenhub_gateway_trace_completions_total` | counter | 已完成调用在链路导出中的去向：`converted` 或 `dropped`。仅在开启追踪时存在。 |
| `tokenhub_gateway_trace_spans_total` | counter | span 在 OTLP 导出中的结果：`exported` 或 `failed`。仅在开启追踪时存在。 |

同时暴露 Go 运行时和进程指标。

**Token 各类型之间不是互斥划分，不能相加。** `prompt` 已经包含 `cached` 和 `cache_write`，`reasoning` 是 `completion` 的子集，相加会重复计算。

在路由之前就被拒绝的请求（API Key 无效、额度耗尽、模型不存在）只增加请求计数。它们没有到达任何 Provider，因此不产生 Token、成本和耗时。对应的尝试承载计数 `routed_requests_total` 只统计到达过至少一个候选的请求，因此拒绝流量突发不会稀释失败转移深度比率。目录中不存在的模型名会被记为 `unknown` 而不是原样上报，避免客户端用随机模型名刷高时间序列数量。

标签为 `model`、`provider_type`、`provider_id`、`resource_id`、`status_code`、`error_code` 和 `stream`。上游失败不再统一上报为 `status_code="502"` 加 `provider_error`，而是使用「上游错误分类」一节列出的状态码与错误码，按旧取值匹配的看板和告警需要相应更新。设置 `TOKENHUB_METRICS_PROJECT_LABEL=true` 会追加 `project_id`，使每个网关指标的时间序列数量按活跃项目数成倍增长；除非确实需要按项目看板，否则建议保持关闭，按 Key 的归因请改用用量报表。

常用 PromQL 示例：

```promql
# 每个模型的平均失败转移深度。
sum by (model) (rate(tokenhub_gateway_route_attempts_total[5m]))
/
sum by (model) (rate(tokenhub_gateway_routed_requests_total[5m]))

# 网关开销的 P99。必须先按 bucket 聚合再调用 histogram_quantile；
# 两个聚合后的分位数相减在数学上是无效的。
histogram_quantile(
  0.99,
  sum by (le, stream) (rate(tokenhub_gateway_overhead_seconds_bucket[5m]))
)
```

多实例部署时，直方图分位数可基于所有实例的 `sum(rate(..._bucket)) by (le)` 计算，因为每个 bucket 都是计数器。`tokenhub_gateway_requests_in_flight` 是 gauge，若需要按实例并发则聚合时需保留 `instance` 标签；跨实例求和得到的是总并发请求数。

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

平台管理员可在「成本账单」中管理外部账单源。TokenHub 支持阿里云 `QueryInstanceBill`、NewAPI 额度数据和 OneAPI 兼容日志源。连接器可以测试连接、立即同步、按分钟设置定时同步、停用并保留历史记录，也可以随后重新启用。连接器需配置对应的 TokenHub Provider ID；若账单只对应一个账号，还可配置 TokenHub 资源账号 ID。对账会使用这一持久化范围，避免其他 Provider 或账号的用量混入结果。

阿里云连接器需要账单 RPC Base URL、AccessKey ID、AccessKey Secret、源时区，可选填写 Product Code。TokenHub 使用 HMAC-SHA1 为每个 RPC 请求签名，并按账期逐月推进。NewAPI 连接器需要 Base URL、访问令牌、`New-Api-User` 用户 ID、币种，以及一个币种单位对应的 Quota 数量；TokenHub 会按照官方文档携带鉴权请求头调用 `GET /api/data/self`，并自动把同步范围切分为最长 30 天的窗口。OneAPI 兼容连接器需要 Base URL、API Token、日志路径、币种和 Quota 换算值。所有连接器都可以设置每秒请求上限；临时网络错误、`429` 和 `5xx` 会使用有上限的指数退避重试。

手动同步可以传入 RFC 3339 格式的 `from` 和 `to`。未指定时间范围时，从上一次成功同步的结束时间继续。每页保存一次上游 Cursor，因此失败重试会从断点继续当前区间，而不是重新开始。规范化账单以 `(connector_id, external_id)` 作为幂等键，并保留币种、源时区、税费、折扣、退款、账期和用量起止时间。最近同步会展示页数、请求尝试数、新增/更新记录数和经过清理的失败代码。

连接器凭证和原始账单快照都使用 `TOKENHUB_SECRET_KEY` 派生的 AES-GCM 密钥加密，不会由管理 API 返回，也不会写入审计 Payload。重启和多副本部署时必须保持该密钥稳定。相关端点包括 `GET/POST /api/admin/billing/connectors`、`PATCH /api/admin/billing/connectors/{id}`、`POST /api/admin/billing/connectors/{id}/test`、`POST /api/admin/billing/connectors/{id}/sync`、`GET /api/admin/billing/records` 和 `GET /api/admin/billing/sync-runs`。

## 成本对账

平台管理员可以在「成本账单 → 成本对账规则」中，将已同步的 Provider 账单与 TokenHub 用量进行比较。规则可选择一个账单连接器、明细/小时/天/月粒度、匹配维度、IANA 时区、一个 ISO 币种、金额与比例容差、明细时间窗口、账单延迟窗口和可选定时周期。币种始终是匹配维度，因此不同币种需要分别建立规则。TokenHub 用量成本以 USD 保存，每条规则记录固定的“1 USD = 目标币种”汇率；USD 规则的汇率必须为 `1`。可选的 Provider 侧映射可将外部 Provider、资源账号、模型和项目值规范化为 TokenHub 标识。明细规则必须包含 `request_id`；聚合规则可按 Provider、资源账号、模型、项目和币种分组。NewAPI 账单没有请求级标识，因此 NewAPI 连接器只支持小时、天和月粒度，不支持明细粒度。手工输入的账期时间会先按规则的 IANA 时区解释，再发送给 API。

选择账期执行规则后，结果会给出已匹配、仅 Provider 存在、仅 TokenHub 存在和金额不一致四类数量，以及两侧总额、差异、可能原因和可下钻的源记录 ID。金额先按亚微美元精度累加，只在保存或展示结果时舍入到最多六位小数。明细匹配会先在时间窗口内最大化一对一匹配数量，再最小化总时间距离；账期边界外的 TokenHub 记录若与账期内 Provider 账单匹配，也会纳入结果。Provider 记录按实际用量时间而非入库时间归属账期，因此迟到的账单仍会归入原始账期。定时任务会在配置的账单延迟后，对最近一个完整的小时、天或月执行对账。

每次结果都保存完整规则快照、规则版本与哈希、输入哈希、执行人、时间和审计事件。重新计算使用该次执行保存的规则快照；若重新计算失败，上一次成功结果及其明细仍会保留，失败尝试会写入审计。源记录不变时，输入哈希和分类金额可复现。成功结果可锁定，锁定后不能再重新计算。明细接口使用服务端 `limit`/`offset` 分页；CSV 默认按有界批次流式导出全部差异行和源记录引用，不做静默行数截断。其中不包含 Provider 凭证或原始快照，资源账号在管理 API 和 CSV 中都会脱敏，资源账号映射也不会写入审计快照。

相关端点包括 `GET/POST /api/admin/billing/reconciliation-rules`、`GET/PATCH /api/admin/billing/reconciliation-rules/{id}`、`POST /api/admin/billing/reconciliation-rules/{id}/run`、`GET /api/admin/billing/reconciliations`、`GET /api/admin/billing/reconciliations/{id}`，以及 `{id}/lock`、`{id}/recalculate` 和 `{id}/export` 操作。这些端点仅允许平台管理员访问。

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
