# 模型目录更新

Language: [English](../model-catalog-updates.md) | 简体中文 | [日本語](../ja/model-catalog-updates.md)

2026-10-10 的目录核查补充了当前上游模型，并记录访问条件、生命周期、调用兼容性和价格边界。目录收录用于发现候选模型，不代表已具备调用支持、账号已获授权、上游实时健康或生产环境已上线。

## 本次范围

更新覆盖标准模型模板、官方 Provider 目录，以及生成的内置 Provider 插件目录。包括 OpenAI GPT-6 Sol/Luna 与新媒体型号、Claude 5.5 与 Fable 5.1、Gemini 3.8 与 Embedding 2、Qwen 3.8 与文本检索/图片/实时型号、DeepSeek V4.1 Flash、Grok 4.7 与 Imagine、MiMo V2.6、豆包 Seed 2.1、Mistral Large 4、Cohere Embed 5/Rerank 4、Voyage Rerank 3，以及核实后的 Kimi、GLM、MiniMax 和 StepFun 型号。同一模型可能出现在多个地域或订阅 Provider 中，因此 Provider 条目数与去重后的模板数不是同一口径。

标准候选模板同时补齐 OpenAI `text-embedding-3-small` 和 `text-embedding-3-large`。Amazon Nova 2 Sonic 与 Nova Multimodal Embeddings 仅目录收录（`call_support=unsupported`）；Bedrock 原生推理和计费契约仍需单独验证。

核查同时保留旧型号的弃用、退役和重定向证据。更新不会自动改模型名称、迁移线路、修改已保存成本或发布生产版本。

新增图片、音频、视频和实时会话型号中，标为 `call_support=unsupported` 的条目仅供目录展示。本次未新增媒体或实时协议适配器。已有媒体接入仍以其自身支持范围为准，参见[媒体 API](media-apis.md)。Embedding 与重排仍以已验证的文本契约为范围，参见[文本 Embedding](embeddings.md)与[文本重排](rerank.md)。

## 管理员可见行为

Provider 选择、模型创建和 Provider 模型库存会展示预览状态、订阅或额外开通要求、停用时间、替代 ID、重定向别名、暂不支持的调用，以及需要确认的成本。日期按所选界面语言和本地时区显示。

- `active` 表示上游产品状态，不保证当前账号有权调用。
- `preview` 保留官方预览状态；`restricted` 或访问要求说明指定套餐、审批或特殊权限。
- `deprecated` 提醒规划迁移；有效的 `shutdown_at` 到期后成为新线路发布限制。
- `retired` 阻止向这个确切的官方型号发布新的启用线路。
- `redirected` 表示上游仍接受旧名称，但实际由其他模型服务，不自动视为不可调用。
- `call_support=unsupported` 的模型仍可展示，但不能发布新的启用线路。

生命周期策略按 Provider 的 `catalog_id` 与精确的上游模型 ID 匹配。官方 API 退役不会全局套用到名称相近的第三方托管或自托管权重。已发布线路不会被自动停用或迁移；不改变既有线路目标与状态的无关编辑仍可保存，切换目标或重新启用会再次执行发布检查。既有运行时能力检查仍然生效。

标准模板保留来源 `provider_catalog_id` 和 `provider_model_id`。若来源与所选 Provider 目录或上游 ID 不同，继承的生命周期标记不会限制该部署；所选 Provider 目录中的精确匹配项仍执行自身的生命周期策略。DeepSeek 仍接受温度参数，思考模式下不生效的说明仅作为 metadata，不作为参数准入限制。

## 发布前确认上游成本

目录价格字段是参考信息，不是供应商账单，也不会自动覆盖已保存的 Provider 成本。参考价缺失或为零不代表免费。计价可能取决于地域、套餐、上下文档位、时段、缓存类型、图片质量、分辨率、秒数、字符数或搜索单元。

当 Provider 模型库存项标为 `pricing_status=unverified`，且既没有已配置的正数输入/输出成本，也没有明确确认时，新启用线路发布会被阻止。进入该 Provider 的模型库存，核对实际账号的价格与计价单位，填写上游成本并保存。保存审核后的成本会将其标记为已配置。若实际价格确为零，应在核实后明确填写并保存零；保留未核实的目录零值不算确认。检索模型还需满足其原有的 Token 或搜索单元价格与成本确认要求。

正数也可能仅是目录参考价。当 `pricing_status=unverified` 且 `catalog_price_reference=true` 时，正数本身不能代替新线路的成本确认。已核查的 GPT-6 条目保留了短上下文参考单价，但输入超过 272,000 Token 时适用另一档价格。应核对适用成本并明确保存为 `pricing_status=configured`，再解除这项发布限制。提示信息叠加不会覆盖既有库存中已配置的正数成本，也不会把这组价格标记复制到该记录。

若当前目录的精确匹配项为未核实价格，而历史库存的输入/输出成本均为零、没有价格状态或明确确认，列表返回与新线路检查也会叠加未核实提示。该叠加不持久化，不改变既有线路计费；已保存的正数成本和明确配置的成本仍被保留。未精确匹配当前目录的库存不在追溯检查范围内。租户价格与上游成本仍应分开配置。本次不会自动把人民币换成美元、把峰谷或长上下文阶梯价压成单价、把按量价套用于订阅，也不会把按图片/秒/字符计费转换成 Token 价。结构化价格说明用于记录上游条件，不会安装新的计费公式；发布前还需确认所需计价单位已受支持。

保存完整的 Token 成本表单会持久化 `pricing_status=configured`，明确确认界面中的输入、输出和缓存读 Token 单价，包括零价。线路发布检查和上游计费快照使用同一确认状态。缓存写入继续使用各自的配置标记与继承规则，原生计价单位仍需单独配置价格。未确认的旧零值仍为未知，确认操作不会重新定价此前的请求快照。

检索模型的紧凑编辑器仍按原有约定确认输入或原生单位价格，不会把隐藏的输出和缓存读价格视为已确认。

## 维护者 metadata 约定

目录模型的 `metadata` 以字符串值传递。结构化证据应序列化为 JSON 字符串；除非代码明确读取，否则仅作为说明。

| 字段 | 含义与维护要求 |
| --- | --- |
| `upstream_source`、`verified_at` | 一手来源 URL 与核验日期（`YYYY-MM-DD`）；记录实际拥有该 ID 和参数定义的来源。 |
| `catalog_reviewed_at` | 人工核查标记；后续公共目录快照过旧时保留该精确模型条目。上游事实变化后需重新核查。 |
| `availability`、`access_requirement` | `active`、`preview` 或 `restricted`，以及必要套餐或审批。不可由按量 API 可用推断订阅可用。 |
| `lifecycle_status`、`lifecycle_source` | `active`、`deprecated`、`retired` 或 `redirected`，依据该 Provider 产品的官方公告。 |
| `shutdown_at`、`replacement_model` | 用带时区偏移的 RFC 3339 时间表示停用检查边界，记录精确替代 ID。官方只给日期时，在 `shutdown_time_note` 保留不确定性，不虚构官方具体时刻。 |
| `redirect_at`、`replacement_note` | 上游计划重定向的时间与条件；不可把重定向日期等同于调用必然失败。 |
| `call_support`、`support_note`、`call_support_scope` | 仅目录收录的操作用 `unsupported`；写明缺失契约或 `text-only` 等范围。未设置该标记不代表已做账号或集成测试。 |
| `pricing_status`、`pricing_source`、`pricing_note` | 可用目录参考价用 `reference`；需管理员配置成本时用 `unverified`；明确审核保存后用 `configured`。保留来源与适用条件。 |
| `catalog_price_reference` | `true` 表示已填写的价格仍仅供参考；与 `pricing_status=unverified` 同时存在时，即使输入/输出价格为正数，也须明确确认成本才能发布新的启用线路，保存为 `configured` 后解除这项限制。 |
| `original_currency`、`original_unit`、`original_prices` | 原始报价币种、单位与结构化价格，仅是证据，不隐含换汇。 |
| `pricing_tiers`、`pricing_schedule`、`reference_token_prices` | 有条件或促销参考价；保留条件与日期，不把最低值默认为通用成本。 |
| `billing_unit` 与原生单位价格字段 | 区分 Token、搜索单元、图片、秒和字符；搜索单元价不是每百万 Token 价。 |
| `max_input_tokens`、`max_output_tokens`、思考及向量字段 | 分开记录输入、输出、上下文、思考预算、维度与向量空间标识；未核实的上限留空并说明。 |

目录刷新会保留已核查的模型条目，同时允许未核查条目和新的上游 ID 继续刷新。提示信息叠加只更新生命周期与支持范围，不覆盖管理员保存的成本或自定义元数据。

## 核验与更新流程

1. 检查三层目录，对照精确 Provider ID、上游模型 ID、地域与套餐类型，保留无关本地改动。
2. 阅读官方模型卡、API 契约、价格页与停用公告。区分发布公告、预览、托管 API 与可下载权重。参数冲突以当前 API/模型页复核，并记录尚未解决的差异。
3. 核实输入输出模态、端点、思考控制、工具历史要求、上下文/输出上限及计价单位。未支持的操作明确标注，不宣称已经新增适配器。切换 Embedding 模型可能需要重建索引，维度相同不足以证明向量空间兼容。
4. 更新标准模板与适当的官方 Provider 条目，保留来源和核查 metadata，再执行 `node tools/generate-builtin-provider-plugins.mjs` 重新生成内置插件目录。不要把官方退役或价格自动复制到聚合服务商。
5. 执行与改动相关的目录、发布策略、协议契约和翻译检查。分别验证新线路与既有线路，包括明确保存零成本、不支持操作和退役型号。
6. 开启客户流量前，以实际账号核实地域、模型权限、参数、流式/工具调用、计量和实际价格。本地测试或 PR 不代表完成账号验证或生产上线；线路迁移需另行规划。

## 一手来源

以下是 2026-10-10 核查使用的官方入口。逐型号的更具体来源保存在 metadata 中；后续更新前需重新确认。

| 厂商 | 官方来源 |
| --- | --- |
| OpenAI | [API changelog](https://developers.openai.com/api/docs/changelog) |
| Amazon Nova | [Core inference](https://docs.aws.amazon.com/nova/latest/nova2-userguide/core-inference.html) |
| Anthropic | [Release notes](https://platform.claude.com/docs/en/release-notes/overview) |
| Google | [Gemini changelog](https://ai.google.dev/gemini-api/docs/changelog), [deprecations](https://ai.google.dev/gemini-api/docs/deprecations) |
| Qwen / Alibaba Cloud | [Model releases](https://help.aliyun.com/zh/model-studio/newly-released-models), [China pricing](https://help.aliyun.com/zh/model-studio/model-pricing), [international pricing](https://www.alibabacloud.com/help/en/model-studio/model-pricing) |
| DeepSeek | [Models and pricing](https://api-docs.deepseek.com/quick_start/pricing/), [Chat API](https://api-docs.deepseek.com/api/create-chat-completion/) |
| xAI | [Models](https://docs.x.ai/developers/models), [pricing](https://docs.x.ai/developers/pricing), [Image Quality migration](https://docs.x.ai/developers/migration/imagine-image-quality-nov-2) |
| Xiaomi MiMo | [Models](https://mimo.mi.com/docs/en-US/quick-start/model), [pricing](https://mimo.mi.com/docs/pricing) |
| Doubao | [Models](https://docs.volcengine.com/docs/ark/model-list?lang=zh), [retirement notices](https://docs.volcengine.com/docs/ark/model-deprecation-notice?lang=zh) |
| Mistral | [Models and lifecycle](https://docs.mistral.ai/models), [changelog](https://docs.mistral.ai/resources/changelogs) |
| Cohere | [Models](https://docs.cohere.com/docs/models), [pricing](https://cohere.com/pricing) |
| Voyage AI | [Rerank](https://docs.voyageai.com/docs/reranker), [pricing](https://docs.voyageai.com/docs/pricing) |
| MiniMax | [Models](https://platform.minimax.io/docs/guides/models-intro), [M Plan](https://platform.minimax.io/docs/m-plan/intro) |
| Kimi | [Models](https://platform.kimi.ai/docs/models) |
| GLM / Z.AI | [Model documentation](https://docs.z.ai/guides/llm/glm-5.3) |
| StepFun | [Model migration](https://platform.stepfun.ai/docs/en/guides/model-migration) |
