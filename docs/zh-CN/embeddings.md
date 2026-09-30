# 文本 Embedding

TokenHub 通过 `POST /v1/embeddings` 提供文本向量，复用 API Key 权限、路由、限流和请求审计。传入 `model` 与单条文本或独立文本数组；批量输入逐条返回向量并保留 `index`。上游返回数量、索引或维度不符时报告错误。

可选参数：`dimensions`、`encoding_format`（`float` / `base64`）、`input_type`（`query` / `document`）、`task`、`normalized`、`truncation`、`late_chunking`、`user`。具体支持取决于上游协议；不支持的参数明确拒绝，不静默丢弃。本接口暂不支持稀疏、量化、多模态和异步 Batch；兼容上游仍可接收 token ID 输入。

网关每次最多接收 2048 条输入，具体协议可能有更低上限。`dimensions` 范围为 1–65536；`task` 与 `input_type` 只能指定其中一个。

## 配置上游

在 Provider 高级设置中打开“文本 Embedding 配置”。协议可选 `openai`、`cohere`、`jina`、`voyage`、`dashscope`、`tei`；Gemini 走原生适配器。留空采用目录默认值或 OpenAI 兼容协议，自定义渠道应明确选择实际协议。

| 协议 | Base URL 结尾示例 | 默认路径 | 说明 |
| --- | --- | --- | --- |
| OpenAI 兼容 | `/v1` | `/embeddings` | OpenAI、SiliconFlow、兼容的 vLLM/Xinference、阿里兼容接口 |
| Cohere | `/v2` | `/embed` | 必须指定 input_type 或 task，返回稠密向量 |
| Voyage / Jina | `/v1` | `/embeddings` | 转换任务和维度参数 |
| DashScope 原生 | `/api/v1` | `/services/embeddings/text-embedding/text-embedding` | 稠密文本向量，网关每次限制 10 条 |
| TEI 原生 | 服务根地址 | `/embed` | 不伪造上游未报告的用量 |
| Gemini | Gemini 基础地址 | `:embedContent` / `:batchEmbedContents` | 独立文本保持独立，不合并批次 |

`embedding_path` 可覆盖追加到 Base URL 的路径，不改变主机或凭据。地域地址以供应商文档为准。目录收录或连接测试通过不代表模型支持全部参数。本地协议测试与真实账号、地域、型号验证分别记录。

## 向量空间

默认要求 Provider、上游模型、适配器类型、实际 Base URL、Embedding 协议和接口路径一致。资源覆盖也参与空间身份判断；不同部署必须显式确认共享空间。确认部署兼容后，可在 `embedding_spaces` 填写上游模型到空间标识的 JSON，例如 `{"my-embedding-model":"space-v1"}`。相同标识意味着管理员确认模型版本与编码行为兼容；仅维度相同不够。更换空间需要业务应用重建已有向量索引，TokenHub 不修改外部索引。

计量证据区分未报告与明确零用量，不将估算冒充实测。上游成本和租户收费分别配置。

新增或重新发布路由时，必须配置租户和供应商两侧价格。租户使用 Embedding 单价，上游库存使用输入单价。若确认免费，在对应的对外模型或上游模型设置 `metadata.retrieval_pricing_confirmed="true"`；历史空配置的零值不代表已确认免费。

```json
{"model":"public-embedding","input":["第一条文本","第二条文本"],"dimensions":1024,"encoding_format":"float"}
```

OpenAI SDK 使用 `client.embeddings.create`。Dify、LangChain/LlamaIndex 使用对应集成并配置 TokenHub 地址和对外模型名；客户端实际版本验收独立于本地协议测试。

模型发现优先采用上游明确声明的 type、modality 或 model_type；缺失时识别常见 BGE、GTE、E5、Voyage 和 sentence-transformer 名称，重排名称优先判为 rerank。名称推断仍不代表部署能力已经验证。

缓存查询和路由前会检查全部启用的路由定义、资源覆盖及可能的 Provider 回退来源；暂时不健康或冷却中的节点也纳入空间判断。健康变化和加权排序不能切换空间。配置冲突返回 `409 embedding_space_conflict`；请移除不兼容线路，或在确认兼容后填写相同的空间标识。

网关插件可改写文本，但必须保留输入条数、维度、编码与任务语义。缓存、Provider 插件及后置处理结果在返回前统一按原始客户端契约校验。缓存插件通过 cache_key 获得绑定空间、请求和调用方范围的宿主键；命中时必须在 cache_key 写入中返回缓存条目保存的键，缺失或不匹配按未命中处理。缓存写入阶段收到相同键，旧缓存插件需适配此约定。

公共模型必须为 embedding 类型，并在执行插件前配置 Embedding 价格或明确确认免费。插件不能切换文本/token 输入模式或改写 token ID。上游未报告 token 用量时，TPM 结算保留入场估算用于额度控制；计费证据仍为未报告。

模型目录展示 Embedding 单价，不使用聊天输入单价替代。渠道库存使用紧凑的检索价格编辑区，保存时保留未展示的聊天/缓存价格。请求详情优先展示响应，可切换查看请求，并展开其他元数据和完整用量明细。

明确确认免费的 Embedding 即使保留旧聊天输入价或适配器返回费用，也按零租户费用结算；供应商成本与 token 计数仍保留。模型目录将确认免费显示为零，未配置显示为未知。部署身份变化会使旧向量缓存失效，无需数据库迁移。

token ID 输入按实际 ID 数量（包含批次总和）预留 TPM。全局路由前插件可在缓存绑定前改写文本；路由级 request_transform 不得改变向量输入。缓存命名空间已更新，旧变换契约生成的缓存将失效。执行前重新检查非 mock 路由的上游库存类型和文本能力，覆盖发布后的配置变化。

## 排查路由被拒绝

运行时筛选排除全部候选线路时，为兼容客户端，API 保留 HTTP `501` 和 `provider_capability_not_supported`。响应及请求日志中保存的响应包含可操作的 `error.message`，以及 `error.details`：`stage="route_selection"`、`upstream_attempted=false` 和 `reasons` 数组。每项包含稳定的原因 `code`、处理建议 `message`、候选线路数 `route_count`。该数量不是上游尝试次数；每条候选线路只报告首先遇到的阻断原因，多种原因按代码排序。不返回渠道标识、地址、凭据或具体成本金额。

| 原因代码 | 检查项 |
| --- | --- |
| `provider_capability_or_protocol_unsupported` | 渠道适配器及 Embedding 协议是否支持 |
| `tenant_price_not_configured` | 对外模型 Embedding 价格或明确免费确认 |
| `upstream_model_inventory_missing` | 渠道库存是否存在与路由上游模型名匹配的记录 |
| `upstream_model_modality_mismatch` | 库存模型类型须为 `embedding` |
| `upstream_text_input_unsupported` | 库存是否支持文本输入 |

此时尚未发送上游请求，路由尝试列表为空属于预期结果。补填 `/embeddings` 无法解决库存或租户价格问题。启动时可能根据已有路由补建库存；补建记录中未确认的零成本不再阻断已有线路。更早的对外模型准入失败仍返回 `400 embedding_model_not_configured`，向量空间冲突等其他失败也保留原有错误。仅部分线路被排除时，合格线路继续正常执行。

## 本地 Embedding 线路升级

已发布线路不会仅因上游输入成本为零或尚未确认而停止调用，保留 v0.8 对自托管模型的调用行为。未知采购成本不会自动标为免费；用量和租户费用照常记录，缺失的上游成本证据在计量中保持待确认，不视为已确认的零费用。管理员可在渠道库存保存实际成本，包括明确确认的零成本。新线路发布仍要求配置租户及供应商价格；模型类型、文本能力、权限和向量空间检查继续生效。

若本地 OpenAI 兼容接口为 `http://model-host:8000/v1/embeddings`，Base URL 填 `http://model-host:8000/v1`，Embedding 协议选“自动选择”或 `openai`，接口路径留空或填 `/embeddings`。路径追加到 Base URL，不填完整 URL。升级无需重写数据库，也不会自动确认免费。
