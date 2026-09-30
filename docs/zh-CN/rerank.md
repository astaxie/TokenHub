# 文本重排

`POST /v1/rerank` 根据查询对候选文档排序，接入 API Key、模型权限、限流、适用安全策略、插件、路由和审计。对外模型必须为 `rerank`，调用时要求渠道协议及租户价格配置完整；新线路发布还要求配置供应商价格。

```json
{"model":"public-reranker","query":"如何续费证书？","documents":["证书续费说明","无关内容"],"top_n":1,"return_documents":true}
```

响应包含 `model` 和 `results`，结果保留原始 `index`、`relevance_score`，可选回显 `document.text`。重复文本按索引区分。不修改分数范围，不拆批后假定全局排序等价；空查询/文档、非法 top_n、不支持的参数、上游缺项或错误索引均明确报错。本次仅支持文本，不包含图片、视频和异步任务。

`documents` 必须包含 1–2048 条非空文本。指定 `top_n` 时，其范围为 1 至文档总数；省略时返回全部文档。

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

自定义 `openai-compatible` 和 `local` 渠道未指定供应商目录，或目录为 `local` / `openai-compatible` 时，“自动选择”采用 Jina 兼容的 `/rerank` 协议。显式协议设置和已知供应商目录默认值优先；未知供应商目录仍需明确选择支持的协议，不会仅凭模型名猜测厂商原生 API。

若实际接口是 `http://model-host:8000/v1/rerank`，Base URL 填 `http://model-host:8000/v1`，路径留空或填 `/rerank`。若服务直接提供根路径 `/rerank`，Base URL 不加 `/v1`。请以实际部署接口和协议为准。

已发布线路在上游成本为零但未确认时仍可调用；缺失的成本证据保持未知，不自动认定免费，用量和租户收费保留。新线路发布仍要求双方价格，运行时继续检查租户价格、能力及库存。v0.8 尚无原生 `/v1/rerank` 接口，本项属于本地服务配置修复，不表示两个版本的重排入口行为相同。
