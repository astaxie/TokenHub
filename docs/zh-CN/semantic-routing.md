# Jev 模型路由策略

Jev 与固定比例、自适应、质量优先、成本优先、主备顺序和综合评分并列，是一种路由策略。客户端向 TokenHub 请求普通的对外模型名称；TokenHub 调用 Jev 对最近一条用户任务进行分类，选择已配置的模型，再通过现有 Provider 适配器调用目标模型。Jev 不生成最终回答，也不需要注册为 Provider。

## 配置与调用

1. 创建对外模型，例如 `auto-chat`，并添加指向已批准上游模型的线路。这是普通模型别名：任何名称都可以采用任意路由策略，名称本身不会启用 Jev。
2. 在「路由策略 → 模型级路由策略」中选择「Jev 智能路由」。
3. 选择分类器：TypeSafe Jev，或 TokenHub 模型（参见[模型分类器](#模型分类器)）。从已有线路中选择候选模型，填写各模型的适用条件、模型选择指令和默认模型；使用 TypeSafe 时还需填写最低置信度。然后应用策略。
4. 使用该对外模型名称调用 `/v1/chat/completions` 或 `/v1/responses`。两个接口均支持流式输出；工具和其他生成参数保留在目标模型请求中。

例如，一个候选处理提取与翻译，另一个处理复杂代码修改。适用条件应来自实际工作负载的评估，模型名称本身不能证明质量。同一 Provider 和模型的多个资源账号合并为一个候选。其他策略使用相同的对外模型与线路配置入口。

修改线路的 Provider 或上游模型后，Jev 编辑器会刷新对应候选，并保留已保存的候选顺序。应用策略前需检查新模型的适用条件；目录未提供时需补充。

```json
{"model":"auto-chat","messages":[{"role":"user","content":"Explain this code change."}],"max_tokens":128}
```

```json
{"model":"auto-chat","input":"Explain this code change.","max_output_tokens":128,"stream":true}
```

## 服务端配置

Jev 分类默认关闭。服务端需要开启该功能并允许当前项目；使用 TypeSafe 分类器时还需配置 TypeSafe 密钥：

```dotenv
TOKENHUB_SEMANTIC_ROUTING_ENABLED=true
TOKENHUB_SEMANTIC_ROUTING_PROJECTS=prj_example
TOKENHUB_TYPESAFE_API_KEY=<server-side-secret>
TOKENHUB_TYPESAFE_MODEL=jev-1.13.0
TOKENHUB_SEMANTIC_ROUTING_TIMEOUT_MS=1000
```

项目白名单是以逗号分隔的精确 ID。空名单不允许外部评估；开启功能但缺少白名单时，启动校验失败。只有使用 TypeSafe 分类器的策略需要 TypeSafe 密钥；未配置密钥时，这些策略使用默认模型。评估模型应固定具体版本。超时覆盖候选元数据查询与评估，默认 1000 ms，上限 10000 ms。每个进程最多同时进行 8 次评估，不排队、不重试。

## 模型分类器

策略可以不使用 TypeSafe，改由 TokenHub 自身的统一模型判断任务类型。将 `evaluator` 设为 `model`，并将 `classifier_model` 设为统一模型名称。`classifier_timeout_ms` 限定分类耗时，取值 100–10000，默认 3000。在控制台中选择「分类器 → TokenHub 模型」。

```json
{"mode":"enforce","min_confidence":0.65,"instructions":"Choose using the configured task criteria.","default_candidate_id":"fast","evaluator":"model","classifier_model":"router-small","classifier_timeout_ms":3000,"candidates":[...]}
```

- 分类模型须处于启用状态、不同于被路由的模型、至少有一条启用的线路，且自身不使用任何语义路由（包括 Jev 策略和旧版附加配置）。保存策略时会校验这些条件。分类模型之后若被改为使用语义路由，请求时会被拒绝，当前请求使用回退模型。
- TokenHub 通过自身的 `/v1/chat/completions` 发起分类，作为同一项目和 API Key 的普通请求，User-Agent 为 `tokenhub-router/1`，客户端 IP 沿用调用方。模型访问控制、速率限制、配额、并发限制、隐私与护栏钩子、路由、故障转移、请求日志和计费均与普通请求相同。API Key 无权使用分类模型、配额耗尽或已达并发上限时，请求使用默认模型；并发上限为 1 的 API Key 始终如此。审计事件 `routing.semantic` 通过 `classifier_request_id` 关联分类请求。
- 提示词只包含以编号列出的候选适用条件、选择指令，以及编码为 JSON 字符串的最近一条用户文本，不发送候选标识和模型名。请求使用 `temperature` 0 和 `max_tokens` 8。回答必须恰好是一个选项编号；`0` 表示无明确偏好，其他回答视为无效决策。建议选择不启用深度推理的快速模型。
- 模型分类器不提供置信度。API 仍要求提供 `min_confidence`，但它不生效，审计事件也不记录置信度和概率分布。
- 服务端开关和项目白名单与 TypeSafe 相同，但不需要 TypeSafe 密钥。分类模型的上游会收到最近一条用户文本，应选择其 Provider 已获准处理此类数据的分类模型。适用条件只是路由提示，不构成授权或数据驻留边界。
- 超时或上游返回 502、503、504 时，该项目的分类器暂停 30 秒（`evaluator_cooldown`）。本地拒绝和无效回答不会触发暂停。超时限定准入与上游调用；分类请求的结算完成后才继续路由。每个进程最多同时进行 8 次分类。

## 选择与回退

TokenHub 先完成鉴权、额度检查、隐私处理、安全检查和符合条件的响应缓存查询。曾使用显式 Jev 策略的别名，以及所有带 `previous_response_id` 的续接请求，会跳过结果缓存读写，因为缓存条目不包含经过验证的线路绑定。项目与 API Key 限制、作用域策略、Provider 健康状态、资源可用性和协议支持先筛选线路。Jev 只能选择准入结果中仍然可用的显式候选。

Jev 可以跨线路优先级选择候选模型；现有规划器继续决定同一 Provider 和模型内部的资源账号顺序。选择有效且达到阈值时，依次尝试所选模型的资源、默认模型、配置顺序中的其他候选。故障转移沿用既有重试规则，不再次调用 Jev。没有选入候选集的线路不会参与执行。

评估超时、服务不可用、并发槽满、结果无效、`no_preference` 或置信度不足时使用默认模型。关闭服务端开关或项目不在白名单时，也使用默认模型且不发送文本。默认模型不满足条件时，使用配置顺序中的首个可用候选。没有候选能够处理请求，或模型目录查询失败时返回 503，不选择未配置的模型。只有一个可用候选时无需评估。

候选的模型目录记录必须处于启用状态。已声明的模态、协议参数和上下文限制用于约束候选；其他协议的参数名称不视为等价。上下文检查使用保守的序列化字节估计加输出 Token 预算。缺少声明不能证明能力或适用性，Provider 模型元数据需要准确维护。目录规范化按端点分别补充预算字段：声明 Chat Completions 时补充 `max_tokens`，声明 Responses 时补充 `max_output_tokens`，不因已声明其他预算字段而跳过。显式声明会保留，请求参数名称按原样转发。内置 GPT-6 Astra 记录显式声明 `max_tokens`、`max_completion_tokens` 和 `max_output_tokens`。已保存的 Provider 模型需要重新导入以刷新声明。

已有缓存或会话亲和性以及粘性线路顺序优先生效。作用域策略显式覆盖算法时，跳过 Jev 评估，仅在已配置候选中按覆盖算法排序。带会话标识的请求不触发新的 Jev 判断。路由模拟器不调用 Jev，也不能预测分类结果。Anthropic Messages、向量和图片接口保留既有路由行为。

## Responses 续接

Jev Responses 请求会保存所选线路、Provider 和模型、资源账号及上游响应 ID；对外返回的上游响应 ID 保持不变。使用 `previous_response_id` 时，必须沿用同一 API Key 和对外模型，直接复用原线路，不重新分类。之后修改路由策略也不会改变该绑定。后台 Responses 在输出钩子批准响应后，将对外任务 ID 与实际上游响应 ID 的绑定和任务成功完成一并提交。待完成、被拒绝、已取消或失去执行权的任务不会发布绑定。输出插件改写 ID 时，TokenHub 会将改写后的对外 ID 映射回原始上游 ID。

详细线路绑定通过 SQLite 或 PostgreSQL 跨实例共享，30 天后过期。每个受保护响应 ID 还会保留一个不包含原始 ID、账号或提示词的长期带密钥哈希标记，用于在详细绑定过期后阻止通过其他对外别名重放。标记存储量会随受保护 ID 数量增长。各实例需要使用相同且稳定的服务端密钥。绑定过期、Jev 模型上的未知 ID、候选被移除，或原线路不可用或失去访问权限时，返回 HTTP 409。续接还会重新检查原模型目录记录的启用状态、协议参数、模态及上下文限制；不满足条件时返回 409，不重新分类或故障转移。绑定读写失败返回 503。续接不会转移到其他模型或账号；原线路无法使用时，应携带完整输入发起新请求。TokenHub 的绑定期限不会延长上游自身的响应保留期。

## 数据处理与计费

TypeSafe 分类器只接收最近一条符合条件的用户文本、选择指令、候选标识、上游模型名和已配置的适用条件。系统或开发者指令、助手历史、工具定义与结果、媒体、凭证、请求头及任意模型元数据不会发送给 Jev。处理后的完整生成请求保留给目标 Provider。用户文本超过 8192 字节、没有用户文本，或最近一条用户消息包含非文本内容时，使用回退模型，不截断文本或发送媒体。

接口固定为 `https://api.typesafe.ai/v1/systemone`，拒绝重定向。Jev 返回受约束的 TypeSafe `Choice`，TokenHub 在本地验证候选归属和置信度。外部路由只应覆盖已批准的工作负载。初始阈值 `0.65` 是评估起点，不是质量保证；置信度描述选项分布，不代表任务成功率。

审计事件 `routing.semantic` 按网关请求 ID 记录选择或回退结果、策略、协议、所选模型和候选 ID、评估模型版本、置信度及概率分布、评估 Token 数和耗时，不记录用户文本或凭证。生成请求日志保留实际执行的上游模型。计费仍使用对外模型价格，不随选模自动变价；TypeSafe 评估器用量单独计算，不计入生成模型的 Token 用量或客户账单；模型分类器的用量按其自身请求计费，参见[模型分类器](#模型分类器)。

## 管理 API 与兼容性

`PATCH /api/admin/model-routing-policies/{model}` 原子保存策略、全部线路和选模配置。以下示例使用两条已有线路：

```json
{
  "strategy": "jev",
  "routes": [
    {"route_id":"route_fast","weight":100,"quality_score":50,"cost_score":50},
    {"route_id":"route_deep","weight":100,"quality_score":50,"cost_score":50}
  ],
  "semantic_routing": {
    "mode":"enforce",
    "min_confidence":0.65,
    "instructions":"Choose using the configured task criteria.",
    "default_candidate_id":"fast",
    "candidates":[
      {"id":"fast","provider_id":"provider_a","provider_model":"small-model","criteria":"Simple extraction and translation"},
      {"id":"deep","provider_id":"provider_b","provider_model":"reasoning-model","criteria":"Complex analysis and code changes"}
    ]
  }
}
```

Jev 策略要求 `enforce`、明确的选择指令、1–32 个不同 Provider 和模型组合、非空适用条件、候选集中的默认模型，以及显式提供的 0 到 1 之间有限置信度。为保持 API 兼容，使用模型分类器时仍须提供该阈值，但不会生效。指令上限为 4096 字节，每个适用条件上限为 2048 字节。候选 ID 必须唯一且不能为 `no_preference`。每个候选必须属于当前对外模型的已有线路；任一设置无效时，整个更新回滚。

配置仍存储在 `Model.metadata.tokenhub_semantic_routing`，普通模型编辑和目录导入会保留它。旧版没有显式候选的 `off`、`shadow`、`enforce` 附加配置，在重新配置前保留原来的 Chat 同优先级行为，控制台会显示提示。应用普通策略会关闭旧配置；应用 Jev 则替换为显式候选。省略 `semantic_routing` 会保留旧版附加配置；显式 Jev 策略切换为普通策略时，即使省略该字段也会关闭分类。选择 `jev` 必须提供完整配置。别名使用过 Jev 后，服务端管理的 `response_binding_required` 标记持续为 true：切换策略后，新 Responses 仍保存并校验绑定，未知、跨 Key 或过期的续接仍被拒绝。客户端不能通过策略更新清除此标记。

Schema 迁移 6 新增持久化表 `jev_response_bindings` 和过期索引。启动时先完成这项增量迁移，再接收请求，不重写基线 Schema。选择普通策略可回退路由行为，服务端开关可停止外部评估；两者都不会删除已有续接绑定。二进制版本回滚必须满足数据库兼容性声明，不应删除迁移记录或绑定表来强制回滚。

## 验证与上线

合成测试覆盖 TypeSafe 协议、受限选项、配置校验与原子性、参数兼容、候选约束、回退、Chat 和 Responses 的流式与工具请求、续接归属与资源绑定、后台任务、SQLite 和 PostgreSQL 持久化，以及界面保存与重载。这些测试不证明真实工作负载准确率或生产延迟。先在获批的测试项目中，对照带标注样例评估任务结果、额外耗时和评估与生成总成本，再扩大项目白名单。

可选真实 API 冒烟测试为 `TestJevRoutingClientLive`。仅在测试进程环境中设置 `TOKENHUB_LIVE_TYPESAFE_API_KEY`，并在 `backend/` 执行 `go test ./internal/server -run ^TestJevRoutingClientLive$ -count=1 -v`。它向 TypeSafe 发送合成请求，不调用生成 Provider，默认跳过。

参考：[LangChain Jev harness](https://www.langchain.com/blog/building-a-harness-with-jev)、[TypeSafe API](https://docs.typesafe.ai/api)、[Choice](https://docs.typesafe.ai/primitives/choice)、[Confidence](https://docs.typesafe.ai/confidence)。
