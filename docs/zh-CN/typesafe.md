# TypeSafe / Jev 接入

语言：[English](../typesafe.md) | 简体中文 | [日本語](../ja/typesafe.md)

TokenHub 通过独立 Provider 适配器和 `POST /v1/systemone` 提供 TypeSafe System One 能力。Jev 根据共享状态返回分类、概率和评分，不生成聊天文本。第一阶段覆盖 Provider 配置、模型元数据、模型发现、健康检查、路由、治理和计费。专用 Playground 和网关内部语义路由不在本阶段范围内。

## 配置 Provider

1. 在「Provider 渠道」新增「TypeSafe」。适配器类型为 `typesafe`，上游 Base URL 为 `https://api.typesafe.ai/v1`。
2. 在凭据字段填写 TypeSafe API Key，引入所需的 Jev 模型。凭据沿用 TokenHub 的加密存储和脱敏规则。
3. 发布能力类型为 `decision`、能力标记为 `systemone` 的模型，并配置到已引入 Provider 模型的路由。为项目 Key 开通该模型权限。Provider 库存成本和模型对外价格分别配置。
4. 执行 Provider 健康检查。该检查通过上游 `GET /v1/models` 验证凭据，不执行推理；推理能力通过合成 System One 请求单独验证。

目录包含 `jev-1.13.0`、`jev-latest` 和 `jev-preview`。需要可重复评估时使用固定版本；别名可能变化。实时模型发现可能只返回别名，不代表固定版本不可用。`POST /api/admin/provider-catalog/custom` 接受 `type: "typesafe"`，按原生模型列表格式发现模型，不推断新发现模型的价格。

## 调用网关

请求使用已发布的 TokenHub 模型名和 **TokenHub 项目 API Key**：

```bash
curl https://tokenhub.example/v1/systemone \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "jev-1.13.0",
    "state": "Please refund this order.",
    "questions": {
      "intent": {
        "type": "choice",
        "instructions": "Classify the customer intent.",
        "criteria": {"refund": "Return funds.", "other": "Another request."}
      },
      "urgent": {"type": "noul", "instructions": "Is immediate attention required?"},
      "severity": {"type": "score", "instructions": "Assess urgency.", "criteria": ["low", "high"]}
    }
  }'
```

原生响应包含 `model`、`answers` 和 `usage.input_tokens` / `usage.output_tokens`。答案键与问题键一致。响应中的模型名是上游解析后的版本；请求日志同时保留对外模型名和路由配置的上游模型名。

成功响应包含 TokenHub 的 `x-request-id`；成功上游报告请求 ID 时，还包含 `x-typesafe-request-id`。SDK 的 `client.systemOne(...).withResponse().requestId` 读取后者。两个响应头均向浏览器客户端开放读取；上游未报告 ID 时，不补造该值。

| 原语 | 判断标准 | 结果 |
| --- | --- | --- |
| `choice` | 标签到描述的非空对象 | `choice`、各标签的 `probabilities` 和 `confidence` |
| `noul` | 可选对象，键为 `true` 和／或 `false`，也可为 `null` | `[0, 1]` 范围内的 `noul`；不要求置信度字段 |
| `score` | 至少两级描述组成的有序数组 | `[0, N-1]` 范围内的期望等级索引 `score`，以及 `legend`、各等级的 `probabilities` 和 `confidence` |

`state` 必填，接受字符串、JSON 对象、数组或 `null`。每个问题可以省略 `instructions`，也可以传入字符串、对象、数组或 `null`；判断标准中的描述支持相同类型。转发时保留省略字段与显式 `null` 的区别，与 [SDK 0.6.0 类型定义](https://github.com/typesafe-ai/typesafe-sdk-js/blob/v0.6.0/src/types.ts) 一致。嵌套值保留 JSON 数值精度。多个问题共享状态、独立评估。低置信度属于正常结果，会原样返回，不自动重试或升级到其他模型。应用自行设定接受阈值。

网关接受 1–1,024 个问题，每个问题最多 4,096 个判断标准，每个 JSON 条目最多嵌套 64 层容器，同时受通用请求体大小限制。目录按上游文档记录 64,000 token 总上下文和 32,000 token 的「状态加最大问题」限制。TokenHub 为配额准入估算 token，依赖 TypeSafe 按其 tokenizer 校验实际上下文。流式参数和聊天专属字段会被拒绝。JSON 格式错误或未知字段返回 `400`，原语语义校验失败返回 `422`。

## JavaScript SDK 兼容范围

`@typesafe-ai/sdk@0.6.0` 的 `client.systemOne()` 可直接使用网关。`baseURL` 设置为 **不含 `/v1` 的 TokenHub 站点地址**，SDK 会追加 `/v1/systemone`；凭据和模型分别使用 TokenHub 项目 Key 与已发布模型名。SDK 冒烟测试使用该方法：

```bash
cd sdk
npm ci
TOKENHUB_BASE_URL=http://localhost:8080 \
TOKENHUB_API_KEY="$TOKENHUB_API_KEY" \
TOKENHUB_MODEL=jev-1.13.0 npm run test:systemone
```

本阶段只覆盖 `systemOne()`，不承诺完整 TypeSafe SDK 兼容。TokenHub 保持已有的多协议 `/v1/models` 契约，不为 SDK 的 `models.list()` 模拟 TypeSafe 模型列表响应。OpenAI Chat、Responses、Embeddings 和流式协议保持不变。现有聊天 Playground 不执行 Jev 请求。

## 治理与计费

评分结果的每个 `legend` 条目必须与实际发送到上游的对应判断标准相等。请求钩子转换判断标准后，校验使用成功路由的实际标准，响应保留这些值，包括脱敏结果。响应钩子不能替换为其他判断标准。问题 ID、答案类型、选项标签和评分等级数仍须与公共前处理后的请求一致。

网关校验结果一致性：概率总和与 1 的偏差不超过一个百分点；Choice 所选项的概率比最高概率低不超过一个百分点；Score 与「索引 × 概率」原始总和的偏差不超过 `0.01 × (N-1)`。这些包含边界的网关容差附加少量浮点舍入余量，不保证接受上游分别舍入后产生的所有分布，也不重新归一化概率。不一致的响应返回脱敏 `502`，已报告的有效用量仍保留。TypeSafe 模型发现同样在请求上游前执行网关统一的自定义请求头校验。

请求复用项目鉴权、模型权限、配额、路由选择、Provider 凭据、错误分类、用量保存、请求日志和 Trace 导出。隐私和安全钩子收到 `route_protocol: "systemone"`。确定性出站检查覆盖状态、问题 ID、指令和判断标准，包括嵌套对象键。若策略要求脱敏结构性键，网关会拒绝请求，避免改变答案契约。可选的请求前处理、路由、Provider 调用、响应和用量钩子遵循已有网关生命周期。本阶段不对 System One 响应启用缓存。

`422` 等客户端错误不触发故障切换；上游 `429`、`529` 等暂时性错误可按既有策略切换到其他兼容路由。答案缺失或格式错误、概率或评分越界、用量缺失或无效均返回脱敏后的 `502`。失败尝试已报告的有效用量会保留。上游凭据错误属于 Provider 错误，不会伪装成项目 Key 鉴权失败。

截至 **2026-09-19** 核验的目录价格为 **每百万输入 token 0.042 美元，输出单价为 0**。输出 token 仍计入用量和 token 配额。最终按上游报告的 token 数和配置价格计费，不使用估算 token，也不按问题数量收费。上线前核对别名价格和 Provider 成本价格。来源：[TypeSafe 模型文档](https://docs.typesafe.ai/models)。

## 上线与回滚

先配置专用项目 Key 和明确的 Jev 路由。应用依赖评分前，使用目标语言和业务的标注样本验证效果，并观察延迟、上游错误、实际用量和成本。本次接入不增加数据库迁移或部署环境变量。禁用对外模型或路由即可停止流量；禁用目录插件只会从 Provider 新增页移除 TypeSafe，不会禁用已有路由。

协议参考：[HTTP API](https://docs.typesafe.ai/api)、[高级输入](https://docs.typesafe.ai/primitives/advanced)、[置信度](https://docs.typesafe.ai/confidence)，以及 TokenHub 的 `/openapi.json` 契约。
