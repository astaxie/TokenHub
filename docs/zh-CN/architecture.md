# TokenHub 整体架构

Language: [English](../architecture.md) | 简体中文 | [日本語](../ja/architecture.md)

本文描述当前仓库已经实现的 TokenHub 架构，供开发、运维和安全人员理解部署形态、请求链路与数据边界。TokenHub 默认采用 SQLite 单实例部署，同时支持 PostgreSQL 单实例和基于远端 PostgreSQL 的多实例部署。

## 架构概览

后端是一个 Go 进程，承载管理 API、OpenAI 兼容模型 API、路由编排、Provider 适配、审计和持久化逻辑。前端是 Next.js 管理后台。控制面与数据面是逻辑边界；默认部署中它们共享一个后端实例和数据库，多实例模式则通过 PostgreSQL 共享状态。

```mermaid
flowchart TB
    admin["平台管理员 / 团队负责人"]
    app["业务应用 / SDK"]
    ingress["直接端口或 HTTPS 反向代理"]
    frontend["Next.js 管理后台"]
    backend["TokenHub Go 后端"]

    subgraph backendProcess["后端进程"]
        adminApi["管理 API\n/api/admin/*"]
        modelApi["模型 API\n/v1/*"]
        governance["鉴权与治理\nKey、RBAC、配额、并发、IP 白名单"]
        routing["路由编排\n候选路由、策略、权重、回退、会话亲和"]
        adapters["适配器注册表\n通用 Provider / OpenAI Codex"]
        operations["运维与可观测性\n用量、审计、告警、健康检查"]
        store["GORM Store"]

        adminApi --> governance
        adminApi --> store
        modelApi --> governance --> routing --> adapters
        modelApi --> operations --> store
        adminApi --> operations
        routing --> store
    end

    subgraph persistence["持久化与配置"]
        sqlite[("SQLite\n默认单实例")]
        postgres[("PostgreSQL\n生产与多实例")]
        catalog["镜像内置模型目录\n或自定义只读覆盖"]
    end

    subgraph upstream["上游模型服务"]
        compatible["OpenAI 及兼容服务\nDeepSeek / Qwen / vLLM / Ollama 等"]
        azure["Azure OpenAI"]
        anthropic["Anthropic"]
        gemini["Gemini"]
        codex["OpenAI Codex Subscription"]
    end

    admin --> ingress --> frontend
    frontend -->|"TOKENHUB_API_BASE_URL"| backend
    app --> ingress -->|"/v1/*"| backend
    adapters --> compatible
    adapters --> azure
    adapters --> anthropic
    adapters --> gemini
    adapters --> codex
    store --> sqlite
    store --> postgres
    catalog --> store
```

## 平面划分

| 平面 | 入口与使用者 | 主要职责 | 当前实现 |
| --- | --- | --- | --- |
| 控制面 | 管理后台与 `/api/admin/*` | Provider、资源、模型、路由、项目、用户、Key、配额、告警、审批和备份管理 | Next.js 控制台 + Go 管理 API，状态保存到 SQLite 或 PostgreSQL |
| 数据面 | 业务应用与 `/v1/*` | 验证项目 API Key，选择可用路由，调用上游模型，返回兼容响应 | Go `net/http` 服务，支持 Chat Completions、Responses、Responses 流式、`/v1/responses/compact` 和 Embeddings |
| 运维面 | 探针、管理 API、部署工具 | 请求审计、用量、路由尝试、Provider 探测、备份与集群协调 | 与后端同进程；多实例时由 PostgreSQL 持久化协调状态 |

## 部署形态

| 形态 | 编排文件 | 服务与入口 | 数据库与适用边界 |
| --- | --- | --- | --- |
| 默认单实例 | `deploy/docker-compose.yml` | 一个前端和一个后端，直接发布 `3000`、`8080` | SQLite，适合开发、测试和单机私有化部署 |
| PostgreSQL 单实例 | `deploy/docker-compose.postgres.yml` | 一个前端、一个后端和一个本地 PostgreSQL 容器 | PostgreSQL，适合需要更高并发或数据库治理的生产部署 |
| 远端 PostgreSQL 多实例 | `deploy/docker-compose.remote-postgres.yml` | Nginx 网关、可横向扩展的前端和后端副本 | 托管 PostgreSQL，适合高可用与水平扩展 |

```mermaid
flowchart LR
    users["浏览器与业务调用方"] --> nginx["Nginx 负载均衡器\n远端 PostgreSQL 多实例模式"]
    nginx --> frontends["Next.js 副本 x N"]
    nginx --> backends["Go 后端副本 x N"]
    frontends --> backends
    backends --> database[("远端 PostgreSQL")]
    catalog["镜像内置模型目录\n或自定义只读挂载"] --> backends
    backends --> providers["外部 Provider API"]
```

默认 Compose 不包含反向代理，直接发布前端和后端端口。生产环境可在外部部署 HTTPS 反向代理；远端 PostgreSQL 多实例编排则内置 Nginx，并将 `/api/*`、`/v1/*`、`/livez`、`/readyz` 和 `/healthz` 转发到后端副本。

默认镜像直接使用构建时内置的 `model-catalog.yaml`，以保证后端程序和模型目录版本一致。需要自定义目录时，通过 `./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml` 显式覆盖；该文件不是默认 Compose 挂载项。

## 关键组件

| 组件 | 位置 | 责任 |
| --- | --- | --- |
| 管理后台 | `frontend/` | 分角色控制台；服务端运行时通过 `TOKENHUB_API_BASE_URL` 读取后端地址，`NEXT_PUBLIC_API_BASE_URL` 仅作兼容回退 |
| HTTP 服务 | `backend/internal/server/http.go` | 注册 API、执行鉴权、路由调用、写入响应和健康检查 |
| 路由编排 | `backend/internal/server/http.go` | 为统一模型名选择候选路由，按优先级、资源优先级、策略、权重和亲和性确定尝试顺序 |
| 适配器注册与探测 | `adapter_registry.go`、`integration_service.go` | 声明各 Provider 的能力，执行 Provider 与资源探测 |
| Provider 适配层 | `providers.go`、`provider_account_codex.go` | 将统一请求转换为上游协议；管理 Codex 订阅资源的 OAuth、刷新与会话亲和 |
| 持久化层 | `store.go` | GORM 数据访问、配额计数、凭证加密、SQLite 备份、PostgreSQL 租约和集群锁 |
| 模型目录 | `data/model-catalog.yaml` | 为镜像构建提供标准模型元数据，启动时同步到数据库 |

Provider 类型与主要能力如下：

| Provider 类型 | 适配器与能力 |
| --- | --- |
| `openai`、`openai_compatible`、`qwen`、`local` | OpenAI 兼容；Chat、流式 Chat、Responses、Embeddings、探测 |
| `deepseek` | OpenAI 兼容；Chat、流式 Chat、Embeddings、探测。Responses 与流式 Responses 按模型声明，对 `deepseek-v4-flash` 和 `deepseek-v4-pro` 开放 |
| `azure_openai` | Chat、流式 Chat、Embeddings、探测 |
| `anthropic` | Chat、流式 Chat、探测 |
| `gemini` | Chat、流式 Chat、Embeddings、探测 |
| `openai_codex` | OpenAI Codex Subscription；Responses、Responses 流式、模型发现、额度、OAuth、会话亲和和 Compact |
| `mock` | 内置 Mock，供本地验证与测试 |

## 模型请求链路

`Model` 是对外 API 契约，`ProviderModel` 是某个 Provider 下持久化的上游模型库存，`ModelRoute` 在两者之间建立映射。对外模型带有明确且持久化的目录角色，因此删除最后一条路由后会保留为草稿，而不会重新变成候选模板；创建或编辑路由时，所选 `ProviderModel` 必须已经存在于库存中。唯一的窄例外是订阅制虚拟模型 `codex-gpt-image-2`：它的路由必须指向 OpenAI Codex Provider，并固定使用上游模型 `gpt-image-2`；这是执行能力，不属于聊天模型库存。这既支持同名 1:1 映射，也支持自定义别名，调用方无需感知具体 Provider 模型名。`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/responses/compact` 和 `POST /v1/embeddings` 都遵循相同的鉴权、配额与路由起点。

```mermaid
sequenceDiagram
    participant C as 业务应用
    participant G as TokenHub /v1
    participant S as Store + 数据库
    participant A as Provider Adapter
    participant U as 上游模型服务

    C->>G: Bearer 项目 API Key + 模型请求
    G->>S: 校验 Key、项目、到期时间、IP 白名单
    G->>G: 对项目与 API Key 模型权限取交集
    G->>S: 以一致性快照读取适用的内容安全策略
    G->>G: 检测、审计、脱敏或阻断用户可见请求文本
    G->>S: 检查配额与并发租约，创建调用上下文
    G->>S: 查询活跃且健康的 Provider / Resource / Route
    G->>G: 解析 API Key、项目或全局策略，筛选候选路由
    G->>G: 按策略、权重与会话亲和生成尝试顺序
    loop 可回退的候选路由
        G->>A: 统一请求 + 路由选择
        A->>U: Provider 协议请求
        U-->>A: 响应或错误
        A-->>G: 统一响应、用量、响应头或错误
    end
    G->>S: 写入路由尝试、请求日志、用量和资源状态
    G-->>C: OpenAI 兼容响应 + x-request-id
```

路由筛选会跳过非活跃或不健康的 Provider、Provider Resource 和 Route，但有一个例外：冷却期已过的资源会作为半开候选重新进入候选池。第一个选中它的请求通过把冷却截止时间向后推进来占用这次试探，因此并发请求仍会被拒绝，而试探失败时下一个（更长的）冷却窗口已经就位。只有该次试探自身成功才会关闭熔断器并自动恢复资源，无需管理员介入——熔断触发时已经在途的请求无法把资源救活；反复失败则按指数退避加长窗口，上限为 `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS`。被管理员禁用的资源永远不会被重新纳入。非流式调用依次尝试候选路由。已开始输出的流无法安全切换到另一条上游路由；Responses 流式仅选择声明 `response_stream` 能力的适配器。对于 `openai_codex` 路由，系统可根据请求与 Key 派生会话亲和键，并持久化资源绑定以保持会话连续性。

对于设置 `background: true` 的 `POST /v1/responses`，同步请求链路会在认证和持久化提交后结束。每个副本即使在启动时队列为空，也会持续轮询持久化队列。Worker 领取任务并重新验证原始授权后，会在同一个数据库事务中提交 admitted 阶段、请求 ID、配额计数、Token 预留与并发租约，再进入相同的 Guardrail、路由、Provider、计量、审计和链路追踪流程。租约代次用于隔离过期 Worker。PostgreSQL 多实例通过行锁和 `SKIP LOCKED` 领取任务；SQLite 在受支持的单后端部署中通过原子操作领取。准入前的租约丢失可安全重放；准入后的租约丢失会进入明确终态，避免重复请求 Provider，尚未分发的 Token 预留会在恢复时退还。

项目与 API Key 的模型访问是路由选择前的显式最小权限层：限制列表互相取交集，限制且空列表禁止全部模型，而旧记录的空模式仍保持继承。作用域路由策略以 `routing-policies` 种类的可审计 `AdminResource` 记录保存。运行时按 API Key → 项目 → 全局的严格优先级最多选择一个绑定，然后将其 Provider、资源、模型、标签、地域和环境约束与路由项目作用域取交集。已停用、冲突或候选为空的高优先级绑定会安全拒绝。策略覆盖、亲和、半开恢复和故障转移只在筛选后的候选中运行。有效策略 ID、作用域与优先级会复制到请求审计记录。

## 鉴权、网络与健康检查

- 业务调用使用项目 API Key：后端校验摘要、状态、项目状态、过期时间、模型范围、IP 白名单、配额和并发。
- 管理调用使用管理员登录生成的会话 Token，或受控运维场景中的 `TOKENHUB_ADMIN_TOKEN`。初始 `admin` 用户由 `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` 创建。
- 非开发环境启动时，后端会拒绝占位值、少于 32 字节的 Admin Token 或后端密钥，以及少于 12 字节的初始管理员密码。
- `TOKENHUB_TRUSTED_PROXY_CIDRS` 限定哪些代理可提供 `X-Forwarded-For`、`X-Forwarded-Host` 和 `X-Forwarded-Proto`，可信代理必须覆盖这些请求头；`TOKENHUB_CORS_ALLOWED_ORIGINS` 限定允许携带浏览器凭证的 Origin。反向代理部署必须正确配置这两项。
- `/livez` 只用于进程存活探针；`/readyz` 以及兼容的 `/healthz` 会检查数据库可用性与数据库演进状态：数据库不可用、迁移处于脏状态、账本校验失败，或阻塞型数据回填未完成时返回 `503`。待执行的在线回填不影响就绪状态。

Provider API Key、Provider Resource 凭证、账单连接器凭证、原始账单快照和持久化后台 Responses 载荷写入数据库前使用 `TOKENHUB_SECRET_KEY` 派生的 AES-GCM 密钥加密。项目 API Key 不保存原文，只保存 SHA-256 摘要以及用于展示的前缀和后缀。所有副本必须共享稳定的 `TOKENHUB_SECRET_KEY`，否则既有凭证和会话相关数据无法可靠使用。

## 数据与运行边界

| 类别 | 核心实体 | 用途 |
| --- | --- | --- |
| 租户与凭证 | `Project`、`APIKey`、`AdminUser`、`AdminSession` | 项目归属、调用权限与管理会话 |
| 路由配置 | `Provider`、`ProviderResource`、`ProviderModel`、`Model`、`ModelRoute`、`AdminResource (routing-policies)` | 上游渠道、资源池、上游模型库存、对外模型、路由规则和作用域策略绑定 |
| 内容安全 | `guardrails.Policy`、`guardrails.DetectionItem`、`guardrails.Binding` | 按项目执行请求检测，保存检测器配置、动作和策略绑定 |
| 治理与计量 | `QuotaBucket`、`UsageRecord`、`ProviderResourceBucket`、`InFlightLease` | 配额、Token 与成本计量、跨副本并发控制 |
| 外部账单 | `BillingConnector`、`BillingRecord`、`BillingRawSnapshot`、`BillingSyncRun` | 云厂商账单采集、规范化、同步断点和运行历史 |
| 多实例协调 | `ClusterLease`、`ClusterTaskState`、`AdapterSessionBinding` | 配置同步、集群操作与 Codex 会话资源绑定 |
| 后台 Responses | `ResponseJob`、`ResponseJobEvent` | 加密请求与结果保留、带隔离的执行状态、取消、过期和状态转换审计 |
| 可观测性 | `RequestLog`、`RequestPayloadLog`、`RouteAttemptLog`、`ProviderObservation`、`AuditEvent` | 请求追踪、载荷审计、路由尝试、Provider 观测与管理审计 |

SQLite 使用单连接和 5 秒 `busy_timeout`，适合单实例；不得让多个后端实例共享 SQLite 文件。PostgreSQL 支持连接池、迁移咨询锁、请求并发租约和集群锁，供多实例共享状态。内置备份 API 仅适用于 SQLite；PostgreSQL 应使用数据库平台的备份与恢复机制，例如 `pg_dump` 和 `pg_restore`。

当前部署不依赖 Redis、消息队列或服务网格。同步请求与响应载荷可用于审计，生产环境应结合保留策略、最小权限、磁盘加密和备份访问控制评估隐私风险。持久化后台 Responses 不会写入明文载荷审计或链路追踪导出，其内容只保留在受 TTL 约束的加密任务记录中。

## 相关文档

- [部署](deployment.md)：三种部署形态、环境变量、反向代理和健康检查。
- [PostgreSQL 设置指南](../postgresql-setup.md)：PostgreSQL 配置、运维和迁移。
- [管理员指南](administrator-guide.md)：Provider、路由、访问控制、审计和成本治理。
- [普通用户指南](user-guide.md)：项目 API Key 与模型 API 调用方式。
- [团队负责人指南](team-leader-guide.md)：团队、项目、成员和成本归因管理。
