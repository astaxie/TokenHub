# 文本重排

`POST /v1/rerank` 根据查询对候选文档排序，接入 API Key、模型权限、限流、适用安全策略、插件、路由和审计。对外模型必须为 `rerank`，调用时要求渠道协议及租户价格配置完整；新线路发布还要求配置供应商价格。

```json
{"model":"public-reranker","query":"如何续费证书？","documents":["证书续费说明","无关内容"],"top_n":1,"return_documents":true}
```

响应包含 `model` 和 `results`，结果保留原始 `index`、`relevance_score`，可选回显 `document.text`。重复文本按索引区分。不修改分数范围，不拆批后假定全局排序等价；空查询/文档、非法 top_n、不支持的参数、上游缺项或错误索引均明确报错。本次仅支持文本，不包含图片、视频和异步任务。

`documents` 必须包含 1–2048 条非空文本字符串或 `{"id":"doc-1","text":"文档正文"}` 对象，两种格式可以混用。指定 `top_n` 时，其范围为 1 至文档总数；省略时返回全部文档。

## 配置渠道

在“高级 → 文本重排配置”选择服务实际提供的协议。路径追加到 Base URL，可用 `rerank_path` 显式覆盖。

| 协议 | Base URL 结尾 | 默认路径 | 场景 |
| --- | --- | --- | --- |
| jina | `/v1` | `/rerank` | Jina、SiliconFlow、兼容的 vLLM/Xinference；本地 BGE 必须由服务框架提供 API |
| cohere | `/v2` | `/rerank` | Cohere 文本重排 |
| voyage | `/v1` | `/rerank` | top_n 转换为 top_k |
| qwen | 对应地域兼容 API 地址 | `/reranks` | 阿里 qwen3-rerank |
| dashscope | `/api/v1` | `/services/rerank/text-rerank/text-rerank` | 阿里 gte-rerank-v2 原生封装 |
| tei | 服务根地址 | `/rerank` | TEI，documents 转换为 texts |

不能从模型名称推断部署协议。Xinference 应填写实际 MODEL_UID。阿里官方资料存在不同 URL 前缀写法，地域和模型可用性也不同，必须核实实际端点；不会把 qwen-rerank 自动替换为其他模型。

适用的 jina/qwen/dashscope 协议支持 instruction 并映射原生字段；voyage/tei 支持 truncation。具体模型限制仍由上游校验，不支持的协议参数明确拒绝。

## 价格与既有配置

按 Token 计量的模型使用输入单价；免费检索价格需要显式确认，空配置不视为免费。Cohere 的 search_units 使用上下游各自的“搜索单元价格 USD/次”，API 对应 `metadata.search_unit_price_usd`；明确免费 Token 价格使用 `metadata.retrieval_pricing_confirmed="true"`。保存渠道成本时确认该渠道价格。上游成本与租户收费独立计算，计量证据区分未报告与实测零值。

目录中无法调用的模型保留展示并标记未支持，不可用于新增路由；新发布检查能力和价格。升级不会自动停用既有可工作路由；更换模型/渠道或重新发布时执行新校验。启动时幂等规范化 reranker 类型别名，并修正被旧版本存为 chat 的明确 reranker 库存。公共 chat 别名仅在其所有已配置上游均明确为 reranker 时修正；混合用途别名保持原样。价格、路由配置和历史账单不变。

发布前，管理员可用会话调用 `POST /api/admin/playground/rerank`：

```json
{"provider_id":"configured-provider","request":{"model":"实际上游模型或UID","query":"测试查询","documents":["候选文本"]}}
```

可选 resource_id；结果返回 response、usage_evidence、pricing_status。此入口验证管理员的上游配置，不替代租户权限和计费验收；审计保存模型与计量事实，不记录测试查询/文档全文。

## 验证边界

本地测试覆盖代表协议、请求/响应转换、网关鉴权、结果索引和搜索单元费用；配置界面使用浏览器场景验证。生产账号权限、地域端点和具体服务版本仍需真实上游验证。Dify、LangChain、LlamaIndex 使用对应 HTTP/重排集成；OpenAI SDK 没有原生 rerank 方法。目录存在模型不等于客户端和供应商已完成验收。

最终响应校验保留后置处理和脱敏结果，不会用请求原文恢复文档内容。管理员试调用通过正常执行配置路径加载解密凭据与所选资源的覆盖配置。原生单位计费遵循相同的非负用量保护，租户免费请求仍保留供应商成本记录。

主计量矛盾（例如 total_tokens 明确为 0 而 prompt_tokens 大于 0）或原生数量非法时返回 502 invalid_provider_usage，不作为成功的零费用请求交付。合法搜索单元与异常辅助 Token 分开处理：搜索单元按配置计费，Token 计数保持非负并保留异常证据。按搜索单元收费的 Provider 插件声明 rerank_protocol=cohere，通过 usage.retrieval_evidence 提供 unit、可空 quantity、source（upstream、plugin 或 unreported）。旧插件仅返回全零 Token 对象时保持未报告，不能推断为实测免费。

内置 qwen/local 渠道支持显式配置的重排协议。发布检查各候选资源的有效覆盖及具备支持能力的渠道回退配置；库存可用性包含仅在资源上配置的能力。匹配作用域的 provider_call 插件路由仍可调用。TPM 预留包含 instruction；仅报告搜索单元或未报告 token 时保留入场 token 估算用于额度控制，不转换计费单位。

渠道库存将检索价格和保存操作放在一起。若同时配置搜索单元与 token 价格，模型目录分别标明两种单价；实际计费取决于路由协议。请求/响应切换和可展开元数据让长重排结果更易阅读，审计证据仍完整保留。

导入时会修正明显 reranker ID 对应的旧目录 chat 回退类型。运行时重新检查上游操作类型、文本能力和租户价格。插件、缓存和后置处理的最终结果必须保持 relevance_score 降序；排序非法时拒绝响应，不重建已脱敏文档。检索模型 USD 单价按控制台所选语言格式化。

Rerank 缓存插件必须使用并回传网关提供的 `cache_key`（`rerank:v1:`），该键绑定调用者范围、隐私处理后的请求和选中路由的有效配置。缺失或不匹配的键视为缓存未命中。缓存查询按路由计划顺序检查符合条件且已配置价格的路由，并使用命中路由执行协议及后置插件校验。回退响应按实际执行路由写入缓存，后续请求可直接复用，避免重复调用上游；不查询不再符合条件的路由缓存。建立缓存键后，路由级请求转换不能再改变重排输入；内容脱敏应在路由前的插件阶段执行。缓存和插件响应的计量单位均按选中路由的协议校验。插件不能修改 `top_n` 或 `return_documents`；`return_documents` 为 false 时，最终响应必须省略 `document`，为 true 时则保留已有的省略或脱敏结果。

## 本地兼容服务与升级

自定义 `openai-compatible` 和 `local` 渠道未指定供应商目录，或目录为 `custom` / `local` / `openai-compatible` 时，“自动选择”采用 Jina 兼容的 `/rerank` 协议。显式协议设置和已知供应商目录默认值优先；未知供应商目录仍需明确选择支持的协议，不会仅凭模型名猜测厂商原生 API。

若实际接口是 `http://model-host:8000/v1/rerank`，Base URL 填 `http://model-host:8000/v1`，路径留空或填 `/rerank`。若服务直接提供根路径 `/rerank`，Base URL 不加 `/v1`。请以实际部署接口和协议为准。

已发布线路在上游成本为零但未确认时仍可调用；缺失的成本证据保持未知，不自动认定免费，用量和租户收费保留。新线路发布仍要求双方价格，运行时继续检查租户价格、能力及库存。v0.8 尚无原生 `/v1/rerank` 接口，本项属于本地服务配置修复，不表示两个版本的重排入口行为相同。

### 控制台配置提示

高级设置显示当前生效协议及该协议的默认路径。路径留空即使用默认值；渠道地址预览把路径追加到 Base URL，不会改写已保存的配置。展开配置示例可查看 Base URL、路径和拼接结果。资源账号可覆盖连接配置，预览不是连通性测试。应填写以 `/` 开头的接口路径，不填完整 URL 或文件系统路径；Embedding 与重排配置互相独立。

请求详情在原始响应上方直接显示保存的错误信息；路由检查拒绝会明确说明尚未发送上游请求。没有用量记录时金额显示横线，不把缺少记录显示为零费用。渠道库存区分未确认的零输入成本与明确免费；保存成本表示确认所填值，包括零。搜索单元采购价缺失时成本保持未知，不会用辅助 token 数和 token 单价代替。

编辑兼容渠道时，控制台保留已保存的运行时目录及自动协议默认值，模型发现或导入使用的目录模板不会静默改写它们。管理接口 PATCH 可通过 `preserve_catalog: true` 启用此行为：仅在渠道类型未变时保留 `catalog_id`、`catalog_source` 和 `doc_url`。创建请求、显式修改渠道类型或不传该标志的客户端保持原有行为。完整 URL 等非法接口路径、格式错误的向量空间 JSON 会在保存前被控制台拦截，即使已经切换离开高级页签也会检查。

## 文档标识、限制与隐私

对象文档必须包含非空白字符串 `id`（最多 256 UTF-8 字节）和非空字符串 `text`（最多 64 KiB UTF-8 字节）；字符串文档使用相同的正文限制。ID 作为不透明数据保留：重复 ID 或正文仍由原始 `index` 区分。传入 ID 时，结果在顶层返回原始 `id`，即使 `return_documents` 为 false；该选项仅控制 `document.text`。上游只接收正文，不接收调用方 ID。插件不能修改 ID，缓存键包含 ID，最终结果按原始索引验证 ID，且不恢复已脱敏正文。

总请求使用既有 JSON 大小限制（默认 8 MiB），文档最多 2048 条。每次插件请求补丁也必须符合配置的 JSON 请求上限，超限补丁在调用上游前返回 `502 gateway_hook_patch_invalid`。原生内容安全脱敏也必须遵守单文档和总请求大小限制；超限请求在查询缓存或调用供应商前返回 `413 request_too_large`。上游 JSON 和最终插件/缓存响应最多 8 MiB。超时与取消沿用网关传输策略。解码错误（包括 `id`/`text` 缺失、类型错误或空白 ID）返回 `400 invalid_request`；空正文、长度超限、非法 `top_n` 等其他输入校验失败返回 `400 invalid_rerank_request`；损坏、超限、重复索引或非法分数的响应返回 `502 invalid_rerank_response`。总请求超限沿用公共请求限制错误。保留供应商鉴权、限流、超时分类，但替换供应商错误正文，防止上游回显的查询或文档进入错误和尝试日志。

网关在持久化计量证据中记录 `document_count`，不依赖审计载荷保存开关或上游是否返回 Token/搜索单元。缺失供应商用量保持未知，不把文档数转换为虚构 Token 费用。重排请求/响应审计保存模型、文档/结果数量和脱敏策略决策，遵循配置的审计保留期；查询、ID、正文和任意上游元数据均被省略，开启追踪载荷捕获时也不会包含。插件错误消息、任意错误代码和非数值限流响应头会在写入错误响应、尝试日志或内部插件失败日志前脱敏。插件审计事件只保留插件、阶段、状态等元数据和省略标记，不保存任意事件载荷。

在 `sdk/` 配置具备模型权限的 `TOKENHUB_API_KEY`、`TOKENHUB_BASE_URL` 后，运行 `TOKENHUB_RERANK_MODEL=public-reranker npm run test:rerank`。Node 客户端检查字符串/对象/混合文档、ID、排序、Top-N、可选正文和参数校验；后端假 HTTP 测试使用同一客户端，无需真实供应商凭证。
