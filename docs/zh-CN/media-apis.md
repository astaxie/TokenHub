# 绘图、音频、视频和音乐 API

语言：[English](../media-apis.md) | 简体中文 | [日本語](../ja/media-apis.md)

TokenHub 支持 [DMXAPI 文档](https://doc.dmxapi.cn/jichu.html)使用的媒体协议。调用前需配置上游供应商，并分别发布生成、上传、查询和下载模型。这里的支持指协议兼容；模型可用性和参数取值由上游决定，不内置供应商凭证或价格。

## 接口范围

| 接口 | 用途与示例 |
| --- | --- |
| `POST /v1/images/generations` | GPT Image、Seedream/即梦、Qwen Image；保留供应商参数、参考图、多张结果、URL 和 base64 |
| `POST /v1/images/edits` | JSON 或 multipart 图片编辑，保留蒙版和上传文件 |
| `POST /v1/images/variations` | 兼容供应商的图片变体 |
| `POST /v1/audio/speech` | 包括 MiniMax speech-2.6 的语音合成，返回上游音频字节 |
| `POST /v1/audio/transcriptions` | 包括 gpt-4o-transcribe 的 multipart 语音识别，支持 JSON、文本、SRT、VTT 结果 |
| `POST /v1/audio/translations` | 兼容供应商的音频翻译 |
| `POST /v1/responses` | Seedance、海螺、可灵、Vidu、PixVerse/拍我、万相、HappyHorse 视频；Seedream、万相、Qwen、Agnes、SciDraw 绘图；MiniMax 声音上传/克隆、高级语音、音乐/歌词以及 Mureka |
| `POST /v1/chat/completions` | MiMo 语音合成/音色设计/克隆、Qwen Omni 音频描述与多模态音频、Recraft 绘图 |
| `POST /v1beta/models/{model}:generateContent` 和 `:streamGenerateContent` | Gemini 原生绘图、图片编辑和多模态输出 |

以具体模型的上游文档为准。DMXAPI 视频示例通过 `/v1/responses` 提交和查询，不需要新增 `/v1/videos`。现有 Chat、Responses 和 Gemini 接口保留流式能力与供应商扩展字段。OpenAPI 契约包含 Chat 的 `input_audio` 内容，以及 Wan 消息、MiniMax 语音上传等供应商自定义 Responses 输入/输出对象；媒体字段由供应商按自身规则校验。

## 配置供应商和模型

1. 添加 **OpenAI-compatible** 供应商，基础地址为 `https://www.dmxapi.cn/v1`，填入供应商凭证。原生 Gemini 示例使用 **Gemini** 类型，基础地址为 `https://www.dmxapi.cn/v1beta`，使用同一上游账户。
2. 发布公开模型，将路由映射到准确的上游模型 ID。模态选择 `image`、`video` 或 `audio`（音乐使用 `audio`），多模态对话模型也可配置这些输出模态。这些 Chat/Responses 请求跳过响应缓存，避免复用生成结果或读到过期任务状态。
3. 同时发布辅助模型，例如 `seedance-2-0-get`、`MiniMax-Hailuo-query`、`MiniMax-Hailuo-get` 和声音/素材上传模型。项目密钥必须获准访问整个流程所需的模型。
4. 将生成和辅助模型的路由固定到同一个供应商账户/资源。供应商任务 ID、文件 ID 和大整数原样传递。TokenHub 不将它们转换成本地后台任务，也不自动绑定供应商任务归属；需要隔离任务命名空间的租户应使用独立上游账户。

调用 Gemini 原生媒体接口时，为公开模型配置媒体模态或输出模态，然后调用 `/v1beta/models/{model}:generateContent` 或 `:streamGenerateContent?alt=sse`。这些请求跳过缓存，保留原生 `generationConfig`（包括 `responseModalities` 和 `imageConfig`）、内嵌媒体及思考签名；上游模型由路由选择。原生 JSON/SSE 响应保留生成的媒体，并按 `usageMetadata` 统计 Token。两种响应均在 128 MiB 媒体上限内缓冲交付，执行鉴权、文本策略、作用域钩子和受保护的出站传输。

Gemini 原生媒体流会拒绝终止错误事件名、`type:"error"` 或 `*.failed` 数据，以及非 null 的嵌套 `response.error`，供应商钩子输出同样适用。即使候选结果已报告结束原因，这些错误仍会记为失败；保留已知 Token 用量和插件显式用量覆盖，且不返回错误中的供应商凭证。嵌套 `response.error:null` 仍允许作为成功事件返回。

托管的 `gpt-image-2`、Codex 订阅及插件图片配置继续使用原有校验、单图任务、素材存储和 `Prefer: respond-async`。如果上游模型与托管公开模型同名，又需要完整供应商 Images 参数，请发布另一个公开别名（如 `vendor-gpt-image`），映射到该上游 ID。普通图片路由直接返回供应商结果；本地任务查询和 TokenHub 签名图片链接仅适用于托管任务。直通图片请求接受供应商自定义的 `response_format` 字符串，例如 Qwen Image 的 `base64`；内置托管图片仍仅接受 `url` 或 `b64_json`，托管插件模型按其配置校验格式。

对 `/v1/responses` 设置 `background:true` 时，响应正文的 `id` 标识 TokenHub 本地请求任务。媒体任务完成后的查询响应通过 `x-tokenhub-upstream-response-id` 返回供应商原始根 ID，可将其用于供应商的辅助查询模型。本地任务完成表示上游 API 调用结束，供应商的生成任务可能仍需继续轮询。字符串和数字 ID 保持精确；非标量 ID、含控制字符或首尾空白、超过 2048 字节的值不会写入该响应头。

## 调用示例

在环境变量 `TOKENHUB_API_KEY` 中设置项目密钥。以下模型名称需要已发布且获准访问。

```bash
curl https://tokenhub.example/v1/audio/speech \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"speech-2.6-hd","input":"Hello from TokenHub","voice":"male-qn-qingse","response_format":"mp3"}' \
  --output speech.mp3

curl https://tokenhub.example/v1/audio/transcriptions \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F model=gpt-4o-transcribe -F file=@sample.wav

curl https://tokenhub.example/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"doubao-seedance-2-0-260128","input":[{"type":"text","text":"A calm ocean at sunrise"}],"duration":4,"resolution":"720p","generate_audio":true}'

# 保存返回的 id，持续查询直到上游任务完成。
curl https://tokenhub.example/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-2-0-get","input":"TASK_ID_FROM_SUBMISSION"}'
```

## 限制、计量和验证

新增直通媒体接口使用 `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES`（默认 32 MiB）。multipart 最多 128 个部分，每个文本字段最多 1 MiB。响应最多缓存 128 MiB 后交付，保留供应商内容类型；这条路径不提供低延迟逐块交付。托管图片原有限制保持不变，TokenHub 不主动下载响应中的 URL。

multipart 的 `model` 和 `stream` 必须是单个文本字段。`/v1/audio/speech` 的 `stream_format:"sse"` 同样会选择流式准入和供应商钩子；`stream_format` 必须是单个非空文本值，请求钩子不能改变实际流式模式。JSON 的 `stream` 必须是布尔值，multipart 的 `stream` 必须是布尔值文本。有歧义的控制字段会在路由前被拒绝，请求钩子的修改也接受同样的校验。文本策略检查 `input`、`instructions`、`prompt`、`negative_prompt` 和 `text` 的每个值，包括重复的 multipart 字段，同时保留 `timestamp_granularities[]` 等重复选项。

接口执行鉴权、模型白名单、配额、作用域路由、供应商资源容量限制、文本前置策略、响应钩子和用量归属。请求钩子接收 JSON/文本字段，multipart 文件字节保持不透明。媒体 Responses 的文本策略也检查供应商提示词、Kling `multi_prompt[].prompt` 分镜描述、歌词和 Wan 嵌套消息或 `input.input.prompt` 封装及 `parameters.negative_prompt`，同时保留不透明的素材/任务 ID。直通媒体审计记录模型、脱敏后的策略决定、内容类型和字节数，不保存上传或生成的媒体。客户端 Cookie 和鉴权头不转发，使用已配置供应商凭证及受保护的出站传输。结果不确定的媒体提交失败不会自动换路由再次生成，包括通过 Chat/Responses 调用的媒体模型；明确的鉴权或限流拒绝仍可触发故障切换。

匹配的 `provider_call` 钩子会在直通媒体适配器之前执行，可以拒绝、跳过或接管路由。直通图片钩子按实际接口协议（`images/generations`、`images/edits` 或 `images/variations`）匹配作用域；托管图片任务仍使用 `images/generations`。非流式钩子返回供应商 JSON 响应，或包含字符串 `data_base64` 和 `content_type` 的二进制封装。流式钩子返回 `stream_events`，交付仍采用缓冲方式。处理二进制或文本响应的响应/护栏钩子必须保留有效的 `data_base64`；格式错误的修改会报错，不会返回空的成功响应。供应商钩子和响应钩子均允许 Base64 中的 CR/LF 换行，但填充符之后不能追加编码数据。128 MiB 响应上限适用于两类钩子的输出，按序列化 JSON 或解码后的二进制字节计算，不计入 Base64 换行；超限的替换结果会报错，但保留已报告的用量。

非流式供应商钩子的 JSON 超过响应大小上限时，请求和路由尝试记录仍会保留已报告的 Token 用量，包括 Gemini 原生 `usageMetadata`。插件显式用量（包括零）优先。响应仍记为失败，不重试，也不扣减供应商健康状态。

Responses 的 OpenAPI 定义要求提供 `model`；`input` 是否必填取决于所选供应商模型。例如 DMX 的歌词/音乐生成可以省略 `input`，在没有用户提示词的情况下生成。省略的输入会原样转发，不自动补空值。`input` 数组还接受图片 URL 或 data URI 字符串，用于 Vidu Q3 首尾帧视频生成等场景。Responses 的 `output` 支持标准条目数组或供应商自定义对象，例如 Qwen Image 3.0 的图片结果位于 `output.choices[].message.content[].image`；已完成的后台任务响应也支持这些结构。输出数组还接受 Seedream 的 `{"type":"image_url","image_url":{"url":"..."}}` 图片结果条目。

JSON 响应处理识别 `application/json` 和带 `+json` 后缀的 MIME 类型，并支持参数及大小写差异。这些响应使用相同的校验、Token 用量提取和 JSON 钩子数据格式，同时保留原始内容类型。已识别的 JSON 基础类型即使参数格式错误或重复，也仍会接受校验。参数中出现 `application/json` 字样不会把文本或二进制响应误判为 JSON。

直通媒体 JSON 的顶层 `error` 非 null 时，即使上游返回 HTTP 200，也会记为失败。供应商钩子输出和响应/护栏钩子修改后的最终结果同样适用。失败返回不含上游凭证的通用消息，保留已报告的 Token 用量，且不重新提交生成；`error:null` 仍允许作为成功响应返回。

SSE 响应按 `text/event-stream` MIME 基础类型识别，即使参数格式错误或重复也会执行事件校验、Token 计量和事件钩子，避免这些处理被绕过。参数中出现 `text/event-stream` 字样不会把文本或二进制响应误判为 SSE。

直通媒体 SSE 中，除 `[DONE]` 外的非空事件数据必须是一个完整 JSON 对象。上游或供应商钩子产生的畸形事件会在缓冲交付前报错，并保留已知用量，包括尾随无效数据之前完整对象中的用量。响应和流式钩子不能把合法事件改成畸形数据或被记为成功的错误流；最终校验保留原始上游用量。

SSE 响应在缓冲交付前逐事件执行 `stream_transform`、`response_post` 和 `guardrail_post`。即使后续流式错误或策略阻止结果交付，上游已报告的 Token 用量仍被保留。终止流的错误会记为失败且不会触发再次生成，错误消息中的供应商凭证会被脱敏。客户端取消请求和已识别的出站代理故障不会计入供应商健康故障；代理故障也不会触发故障切换。供应商钩子流超出响应缓冲上限时，仍保留此前完整事件报告的用量，除非插件显式提供了覆盖用量；超限仍记为失败且不重试。插件生成的 SSE 同样接受错误和用量检查；插件显式提供的用量（包括零值）优先于事件用量。插件生成的 JSON 未显式提供用量时，同样使用响应体中的 Token 用量；显式零值仍优先。JSON 响应必须包含一个完整对象，不能尾随其他值或垃圾数据。响应/护栏钩子处理后的最终 JSON 也必须保持对象结构，拒绝 null、数组或标量替换。无效响应会报错且不重新提交，并保留首个完整对象报告的用量，即使随后读取响应失败或超出大小限制。不完整的 JSON 对象不会被虚构用量。用量归因钩子失败时保留原始上游用量。归因成功后若最终响应校验失败，请求和路由尝试仍保留一致的归因用量。直通媒体接口收到 HTTP 408 时不会自动再次提交。审计记录保留脱敏后的策略决定，不保存替换文本或媒体内容。

媒体模型的流式 Responses API 识别标准 Responses 完成事件、Wan 的 `[DONE]` 标记和 MiniMax 的 `data.status=2` 完成帧。顶层和嵌套的 Token 用量均被保留；未收到完成标记就结束的流记为失败。MiniMax 音乐事件的顶层 `status:"failed"` 表示供应商终止失败：错误消息在交付前脱敏，已知用量继续保留，后续完成标记不能将失败改为成功。除 `[DONE]` 外，非空事件数据必须是一个完整 JSON 对象。格式损坏的事件在转发前被拒绝，已知用量仍会保留，且不会被误判为成功完成；心跳和合法供应商事件保持原样。单个媒体事件上限为 128 MiB。文本模型 Responses 保留原有完成判定规则。

配置了媒体输出的模型通过 OpenAI 兼容 Chat 接口返回流时，单事件同样支持最多 128 MiB，可接收供应商一次性返回的完整生成音频。文本模型的 Chat 流仍限制单事件 8 MiB，客户端仅添加音频字段不会提高上限。

Token 计费使用上游返回的用量。语音转录的 JSON 和 SSE 用量保留 `input_token_details.audio_tokens` 中报告的音频 Token 明细。二进制音频、字幕文本或未返回 Token 的供应商不会被虚构 Token 成本；请求数/并发限制和请求日志仍生效。按秒、按图、按字符的费用不转换成 Token 单价。为返回 Token 的模型配置价格，其他费用与供应商账单核对。

回归测试使用本地 HTTP 模拟供应商及合成请求，覆盖文件上传、二进制/文本响应、绘图扩展参数、视频异步请求结构、声音素材、大整数、权限拒绝、钩子和缓存跳过。测试不消耗付费生成额度；具体供应商和模型的可用性需使用实际账户验证。
