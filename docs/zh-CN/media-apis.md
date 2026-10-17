# 文本、图片、音频与视频 API 调用指南

语言：[English](../media-apis.md) | 简体中文 | [日本語](../ja/media-apis.md)

使用同一个 TokenHub 网关和项目 API Key，调用获准使用的文本与多模态模型。本文参考 [APIMart API 手册](https://docs.apimart.ai/cn/index)按任务组织的方式，说明 TokenHub 自身的鉴权、路由、任务隔离和限制。

**支持接口不等于模型已经可用。** 管理员需要配置上游凭证、发布公开模型 ID、映射上游模型并授权项目密钥。格式、时长、分辨率、音色、模型版本和素材有效期取决于上游。TokenHub 不内置供应商账户，也不承诺支持 APIMart 的全部模型与功能。

## 选择接口

| 任务 | 方法与路径 | 返回结果 |
| --- | --- | --- |
| 查询可用模型 | `GET /v1/models` | 当前密钥可访问的模型 |
| 文本、视觉理解、工具调用 | `POST /v1/chat/completions` | Chat JSON 或 SSE |
| Responses 兼容请求 | `POST /v1/responses` | Response JSON 或 SSE；可选本地后台任务 |
| Anthropic Messages | `POST /v1/messages` | Anthropic JSON 或 SSE |
| Gemini 原生请求 | `POST /v1beta/models/{model}:generateContent` | Gemini 原生 JSON |
| Gemini 原生流式请求 | `POST /v1beta/models/{model}:streamGenerateContent?alt=sse` | Gemini 原生 SSE |
| 生成图片 | `POST /v1/images/generations` | 图片或上游任务回执 |
| 编辑图片、生成变体 | `POST /v1/images/edits`、`POST /v1/images/variations` | 供应商图片响应 |
| 文字转语音 | `POST /v1/audio/speech` | 音频字节或供应商支持的流 |
| 语音识别、翻译 | `POST /v1/audio/transcriptions`、`POST /v1/audio/translations` | JSON、文本或字幕 |
| 生成视频 | `POST /v1/videos/generations` | 上游任务回执 |
| 查询图片、视频任务 | `GET /v1/tasks/{task_id}` | 供应商任务状态和结果 |

向量和文档排序分别见[文本向量](embeddings.md)与[文本重排](rerank.md)。部署后的交互式接口文档位于 `/docs`，机器可读契约位于 `/openapi.json`。

## 鉴权与快速开始

使用网关地址，不要使用控制台端口。示例环境变量是没有 `/v1` 的根地址；OpenAI SDK 的 `base_url` 则应设置为 `$TOKENHUB_URL/v1`。在**密钥管理**中获取项目 API Key；控制台登录令牌不能调用模型接口。

```bash
export TOKENHUB_URL='http://localhost:8080'
export TOKENHUB_API_KEY='YOUR_TOKENHUB_PROJECT_KEY'

curl --fail-with-body "$TOKENHUB_URL/v1/models" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY"
```

从返回结果的 `data[].id` 选择 `model`。下文 `text-model`、`image-model`、`speech-model`、`transcription-model`、`seedance-video` 均为需发布或替换的**公开别名**，不必与上游模型 ID 相同。

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/chat/completions" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"text-model","messages":[{"role":"user","content":"Explain an API gateway in one sentence."}],"stream":false}'
```

从 `choices[].message.content` 读取文本，保留请求 ID 以便在**请求日志**中排查。出现在 `/v1/models` 中只表示模型可见且有符合条件的路由，不代表上游所有功能均已实时验证。TokenHub 未实现 APIMart 的 `expand` 元数据结构及账户余额接口。

## 配置供应商与选择模型

接入 APIMart 兼容账户时，添加 **OpenAI-compatible** 供应商，基础地址填写 `https://api.apimart.ai/v1`，配置供应商凭证。可发布 `text-model` 映射到账户可用文本模型、`image-model` 映射到 `gpt-image-2`、`seedance-video` 映射到 `seedance-2.0`。按用途选择 `image`、`audio` 或 `video` 模态，多模态对话模型同时配置输入/输出模态。媒体 Chat/Responses 请求会跳过响应缓存。

Anthropic、Gemini 原生请求需要路由支持相应原生协议和响应结构；仅设置 OpenAI-compatible 基础地址并不意味着原生协议也兼容。适配器和 SDK 配置见[用户指南](user-guide.md)。供应商文档可能展示带包装层的文本响应，需验证所选路由是否返回客户端要求的协议结构。

公开名称 `gpt-image-2`、Codex 订阅图片模型及配置为托管模式的图片插件采用 TokenHub 托管生图流程。需要完整上游 Images 参数或 APIMart 异步生图时，应另外发布 `image-model` 等别名并映射到 `gpt-image-2`。见[托管生图指南](codex-image-generation-api.md)。

升级时通过正常启动迁移流程应用数据库 v8 迁移 `add-media-task-bindings`。任务绑定在重启后保留；保持服务器密钥不变，以便解密保存的上游任务 ID。无需新增环境变量。

## 文本与多模态理解

### Chat Completions

| 字段 | 要求 | 说明 |
| --- | --- | --- |
| `model` | 必填 | 已发布的公开模型 ID |
| `messages` | 必填 | 对话消息；支持的模型可接收分块内容 |
| `stream` | 可选布尔值 | 显式使用 `true` 返回 SSE，`false` 返回完整 JSON |
| `max_tokens`、`max_completion_tokens` | 可选 | 输出上限，实际行为取决于路由和模型 |
| `temperature`、`reasoning_effort` | 可选 | 模型支持的生成控制参数 |
| `tools`、`tool_choice`、`response_format` | 可选 | 模型支持的工具调用与结构化输出 |

流式调用使用 `curl -N` 和 `"stream":true`，持续读取 `choices[].delta` 事件直到完成。不要将其他供应商文档中的默认流式行为视为所有模型的默认值。

视觉理解需选择支持图片输入的模型，并将用户消息替换为以下内容：

```json
{"role":"user","content":[{"type":"text","text":"Describe this image."},{"type":"image_url","image_url":{"url":"https://your-assets.example/reference.png"}}]}
```

将素材 URL 替换为可访问的文件。OpenAI-compatible 路由保留所支持的图像/音频字段；转译到原生协议的路由能力更有限。Chat 转 Gemini 接受 base64 图片 data URI，不支持的音频内容会报错而非静默丢弃。详见[用户指南中的协议差异](user-guide.md)。

### Responses、Anthropic 与 Gemini

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/responses" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"text-model","input":"Summarize the benefits of a shared gateway.","stream":false}'

curl --fail-with-body "$TOKENHUB_URL/v1/messages" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-model","max_tokens":256,"messages":[{"role":"user","content":"Hello"}]}'

curl --fail-with-body "$TOKENHUB_URL/v1beta/models/gemini-model:generateContent" \
  -H "x-goog-api-key: $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Hello"}]}]}'
```

分别从 Responses 的 `output`、Anthropic 的 `content[]`、Gemini 的 `candidates[].content.parts[]` 读取结果。后续对话需要保留工具调用与推理字段。Responses 也支持相应供应商的媒体对象。Gemini 原生媒体请求保留 `generationConfig`、`responseModalities`、`imageConfig`、内嵌媒体与思考签名；媒体 JSON/SSE 在媒体大小上限内缓冲交付。

## 图片

### 生成图片

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/images/generations" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"image-model","prompt":"A watercolor lighthouse at sunrise","n":1,"size":"16:9","resolution":"2k"}'
```

示例通过公开别名使用 APIMart `gpt-image-2` 的参数风格。其他供应商可能要求 `1024x1024` 等像素尺寸，不应混用不同模型的参数约定。

| 字段 | 说明 |
| --- | --- |
| `model`、`prompt` | 公开模型与画面描述 |
| `n` | 图片数量，最大值由上游限制 |
| `size`、`resolution`、`quality` | 上游定义的宽高比、像素尺寸、分辨率档位和质量 |
| `image_urls` 等参考字段 | 模型支持时使用的参考图片 |
| `response_format` | 上游支持的格式，如 `url`、`b64_json` 或 `base64` |

同步响应通常包含 `data[].url` 或 `data[].b64_json`；异步响应包含 `data[].task_id`，需按下文任务接口逐个查询。供应商结果字段会保留。TokenHub 不自动下载响应 URL，也不延长素材有效期。

### 编辑图片与使用蒙版

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/images/edits" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F 'model=image-model' \
  -F 'prompt=Replace the background with a sunrise sky' \
  -F 'image=@input.png' \
  -F 'mask=@mask.png'
```

使用本地文件和支持 multipart 编辑的路由，由 cURL 自动生成 multipart boundary。兼容供应商还可使用 JSON 编辑和 `/v1/images/variations`；蒙版、重复图片字段及其他参数遵循该供应商契约。托管图片任务继续使用独立的单图校验、素材存储与 `Prefer: respond-async` 行为。

## 音频

### 文字转语音

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/audio/speech" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"speech-model","input":"Hello from TokenHub.","voice":"alloy","response_format":"opus","speed":1}' \
  --output speech.opus
```

| 字段 | 说明 |
| --- | --- |
| `model` | 已发布的语音模型 |
| `input` | 待合成文本，长度由上游限制 |
| `voice` | 上游音色 ID；`alloy` 只是示例，并非通用音色 |
| `response_format` | 模型支持的音频格式，文件扩展名应匹配 |
| `speed` | 模型支持时使用的语速 |

成功响应为音频字节，保留上游 Content-Type，不包装成 JSON 或 base64。使用保存的文件前先检查 HTTP 状态。供应商支持时可使用 SSE 语音，但直通媒体采用缓冲交付，不提供低延迟分块播放。

### 语音识别与翻译

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F 'model=transcription-model' \
  -F 'file=@sample.wav' \
  -F 'response_format=json'
```

`file` 为本地上传文件，`model` 为公开语音识别模型。可选的 `language`、`prompt`、`response_format` 和重复的 `timestamp_granularities[]` 遵循上游契约。JSON 通常返回 `{"text":"..."}`；文本、SRT、VTT、verbose JSON 是否可用由供应商决定。音频翻译使用 `/v1/audio/translations` 及兼容模型，目标语言规则取决于模型。

声音克隆与音乐可继续通过已有供应商特定的 Chat/Responses 工作流使用，见[媒体运维说明](media-api-operations.md)。APIMart 的 `/v1/music/*`、声音/素材管理、上传及 webhook 管理未作为通用 TokenHub API 接口族实现。

## 视频与 Seedance

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/videos/generations" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-video","prompt":"A calm ocean at sunrise, slow camera movement","duration":5,"size":"16:9","resolution":"720p","generate_audio":true}'
```

将 `seedance-video` 映射到 APIMart `seedance-2.0` 等上游模型。其他兼容视频模型使用同一接口，参数按各自契约填写。

| 字段 | 说明 |
| --- | --- |
| `model`、`prompt` | 公开模型与场景描述 |
| `duration` | 期望秒数，可选范围由模型决定 |
| `size`、`resolution` | 模型定义的宽高比与分辨率 |
| `generate_audio` | 模型支持时生成配套声音 |
| `image_urls`、`video_urls`、`audio_urls` | 模型支持的参考素材，需使用上游可访问的 URL |
| 其他字段 | 保留供应商扩展，由上游校验 |

首尾帧、多参考素材、时长范围和素材组合限制并非所有模型通用。TokenHub 未实现火山引擎原生 `/content_generation/tasks` 协议。

DMXAPI 的 Seedance 仍可通过 `/v1/responses` 使用，分别调用生成模型与 `seedance-2-0-get` 等辅助模型。其任务 ID 与查询规则不同于 `/v1/tasks`，见 [DMXAPI 配置与示例](media-api-operations.md)。

## 提交与查询异步任务

APIMart 兼容图片/视频提交后，回执结构如下：

```json
{"code":200,"data":[{"status":"submitted","task_id":"task_example"}]}
```

回执表示上游已接受任务，不表示生成完成。TokenHub 将供应商任务 ID 替换为不透明 ID，持久化保存提交项目、密钥及供应商/资源绑定。使用**提交时的同一项目 API Key**和原样返回的 ID 查询：

```bash
export TOKENHUB_TASK_ID='TASK_ID_FROM_SUBMISSION'
curl --fail-with-body "$TOKENHUB_URL/v1/tasks/$TOKENHUB_TASK_ID?language=en" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY"
```

可选查询参数 `language` 支持 `en`、`zh`、`ja`、`ko`、`ru`、`fr`、`de`、`id`、`pt`、`es`，用于上游任务错误文本的语言。重复传入 `language` 或使用其他查询参数会返回 `400 invalid_task_query`。

| 响应字段 | 说明 |
| --- | --- |
| `data.id` | 此查询流程使用的 TokenHub 任务 ID |
| `data.status` | 上游任务状态 |
| `data.progress` | 可选的上游进度，不是网关估算值 |
| `data.result` | 模型特定结果；APIMart 可返回 `images[]` 或 `videos[]`，其中 `url` 可能是数组 |
| `data.error` | 存在时表示上游任务失败信息 |
| `data.cost`、`data.credits_cost` | 上游元数据，不是 TokenHub 账单 |

对 `pending`/`processing` 设置总超时和查询间隔，例如每 3–5 秒查询并逐步退避。`completed` 表示成功，`failed`、`cancelled` 为终止失败状态；其他非终止状态按上游契约处理。及时保存结果 URL，并遵守 `expires_at`。HTTP 200 也可能包含失败任务，必须同时检查 HTTP 状态和 `data.status`。

查询始终使用原路由/账户，不切换到账户之外的其他路由。密钥必须有效且继续拥有模型权限。即使属于同一项目，另一把密钥也不能查询任务；不能直接传入任意供应商任务 ID。路由或供应商配置变化可能使旧任务不可查询。后台生成由上游执行；TokenHub 不新增任务执行器、取消或完成回调服务。在 TokenHub 外配置的供应商回调使用供应商自己的 ID 和规则。

任务绑定在**提交后 30 天**过期，与上游素材 URL 的有效期分别计算。过期、不存在或无权访问的任务返回 `404 media_task_not_found`；轮换项目密钥不会将旧任务转移给新密钥。更换原供应商凭证、URL、请求头或其他账户配置后，查询返回 `503 media_task_route_unavailable`，即使替换后的账户仍支持同一模型也不例外。绑定保存失败时，提交返回 `503 media_task_binding_unavailable`；上游可能已经接受生成，应先检查日志再决定是否重试。

### 继续生成或重新生成视频

对于支持引用先前任务的视频模型，在相应顶层字段中传入**来源提交返回的 TokenHub 任务 ID**：

| 字段 | 上游操作示例 |
| --- | --- |
| `draft_task_id` | Seedance 2.5 草稿转正式视频 |
| `draft_from_task_id` | FLUX.3 草稿转正式视频 |
| `extend_from_task_id` | Gemini Omni 或 PixVerse 视频延长 |
| `source_task_id` | 使用不同目标模型的 MiniMax H3 重新生成 |

这些字段仅在 `POST /v1/videos/generations` 中识别。例如，`{"model":"followup-video","source_task_id":"task_example"}` 使用管理员发布的兼容重新生成模型别名。任务完成状态、有效时长、分辨率和参数限制仍由上游模型验证，并非每个模型都支持所有字段。提示词、素材 URL 和嵌套扩展标识符不会被当成任务引用。

必须使用同一把项目 API 密钥，保留来源和目标模型的权限，并在任务绑定的 30 天有效期内引用。格式错误、不存在、过期、属于其他密钥或直接传入的供应商任务 ID，会在发起生成前返回 `404 media_task_not_found`。TokenHub 将授权通过的任务 ID 转换为供应商 ID，并使用原供应商账户/资源；该账户支持后续操作时，目标别名、路由和上游模型可以不同。TokenHub 不会切换到其他账户。凭证或配置等变化导致账户指纹改变时，返回 `503 media_task_route_unavailable`；原账户不可用时，仍适用普通的路由、策略和容量限制错误。

多个引用指向不同账户时，返回 `400 invalid_media_task_reference`。其余有效引用的组合是否受支持由上游判断。每次接受的后续生成都会创建新任务，并可能产生新的上游费用。请求钩子仍使用公开任务 ID，供应商调用钩子收到转换后的供应商 ID。

以下三种异步机制不能混用：

| 机制 | ID 与查询方式 |
| --- | --- |
| APIMart 兼容图片/视频任务 | 返回的 `task_id`；`GET /v1/tasks/{task_id}` |
| TokenHub 托管图片任务 | 使用[生图指南](codex-image-generation-api.md)的托管任务契约 |
| Responses 的 `background:true` | 本地 Responses 任务；完成只表示上游 API 调用结束，不表示供应商渲染完成 |

后台媒体 Responses 完成后的查询响应通过 `x-tokenhub-upstream-response-id` 暴露有效的供应商根 ID；应将其交给供应商辅助查询模型，不能用于 `/v1/tasks`。

## 错误、限制与计费

网关错误通常使用以下结构；原生 Anthropic/Gemini 使用对应协议的错误格式：

```json
{"error":{"message":"Model is not allowed for this API key","type":"model_not_allowed","code":"model_not_allowed"},"request_id":"REQUEST_ID"}
```

| HTTP 状态 | 常见原因 | 处理方式 |
| --- | --- | --- |
| `400` | JSON、multipart 控制字段或参数错误 | 按所选模型修正请求 |
| `401` / `403` | 密钥无效、禁用、过期或模型未授权 | 检查项目密钥和模型权限 |
| `404` | 任务/模型不存在或不可访问 | 核对 ID 和提交密钥 |
| `413` | 请求超过大小限制 | 减少上传大小或由管理员调整上限 |
| `429` | 速率、并发、配额或预算限制 | 检查错误码与重试响应头 |
| `501` | 路由不支持该协议 | 配置支持的供应商/路由 |
| `502` / `503` | 上游输出无效、无可用路由或供应商失败 | 根据请求 ID 查看请求日志 |

直通媒体请求受 `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` 限制，默认 32 MiB；multipart 最多 128 个部分，每个文本字段最多 1 MiB。直通响应最多缓冲 128 MiB。托管图片另有独立限制，上游文件和格式上限可能更低。multipart 中 `model`、`stream` 只能出现一次；JSON 的 `stream` 必须为布尔值。

一次提交最多绑定 128 个返回任务 ID。无效任务 ID 或超出此数量的任务批次返回 `502 invalid_media_task`，这不代表上游已取消生成。

统一执行鉴权、模型白名单、配额、限流、文本策略、作用域钩子、受保护的出站传输和日志。文件字节不参与文本策略解析。不会转发客户端凭证或 Cookie，而是使用配置好的供应商凭证。结果不明确的生成失败不会自动换路由重新提交。客户端超时后也应避免盲目重试，上游可能已经接受计费任务。

Token 计量以返回的用量为准；二进制音频、字幕或没有用量信息的供应商不会产生虚构的 Token 费用。轮询不会重复计入生成 Token 用量。按图、秒、字符或供应商积分计费的费用不会自动折算为 Token 单价，应与上游账单核对。钩子与流校验细节见[媒体协议运维](media-api-operations.md)。

## 组合调用示例

| 目标 | 调用顺序 |
| --- | --- |
| 对话助手 | 查询模型 → 发送对话 → 保留工具调用和推理上下文 |
| 图片生成 | 提交 Images → 使用同步结果或逐个查询任务 ID |
| 配音视频 | 生成语音 → 将音频放到供应商可访问的位置 → 提交支持音频参考的视频模型 → 轮询 → 保存素材 |
| 会议纪要 | 上传录音转写 → 将文本交给 Chat/Responses |
| DMXAPI Seedance | 同一账户发布生成/查询模型 → 提交 Responses → 用供应商 ID 查询 |

回归测试使用本地模拟供应商，验证协议、访问隔离、上传、钩子和计量；没有验证付费生成或真实账户下全部模型的可用性。参考来源：[APIMart 文本](https://docs.apimart.ai/en/api-reference/texts/general/chat-completions.md)、[图片](https://docs.apimart.ai/en/api-reference/images/gpt-image-2/generation.md)、[语音](https://docs.apimart.ai/en/api-reference/audios/tts.md)、[Seedance](https://docs.apimart.ai/en/api-reference/videos/seedance-2-0/generation.md)、[任务查询](https://docs.apimart.ai/en/api-reference/tasks/status.md)。
