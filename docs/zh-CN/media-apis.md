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

以具体模型的上游文档为准。DMXAPI 视频示例通过 `/v1/responses` 提交和查询，不需要新增 `/v1/videos`。现有 Chat、Responses 和 Gemini 接口保留流式能力与供应商扩展字段。

## 配置供应商和模型

1. 添加 **OpenAI-compatible** 供应商，基础地址为 `https://www.dmxapi.cn/v1`，填入供应商凭证。原生 Gemini 示例使用 **Gemini** 类型，按文档设置基础地址并使用同一上游账户。
2. 发布公开模型，将路由映射到准确的上游模型 ID。模态选择 `image`、`video` 或 `audio`（音乐使用 `audio`），多模态对话模型也可配置这些输出模态。这些 Chat/Responses 请求跳过响应缓存，避免复用生成结果或读到过期任务状态。
3. 同时发布辅助模型，例如 `seedance-2-0-get`、`MiniMax-Hailuo-query`、`MiniMax-Hailuo-get` 和声音/素材上传模型。项目密钥必须获准访问整个流程所需的模型。
4. 将生成和辅助模型的路由固定到同一个供应商账户/资源。供应商任务 ID、文件 ID 和大整数原样传递。TokenHub 不将它们转换成本地后台任务，也不自动绑定供应商任务归属；需要隔离任务命名空间的租户应使用独立上游账户。

托管的 `gpt-image-2`、Codex 订阅及插件图片配置继续使用原有校验、单图任务、素材存储和 `Prefer: respond-async`。如果上游模型与托管公开模型同名，又需要完整供应商 Images 参数，请发布另一个公开别名（如 `vendor-gpt-image`），映射到该上游 ID。普通图片路由直接返回供应商结果；本地任务查询和 TokenHub 签名图片链接仅适用于托管任务。

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

接口执行鉴权、模型白名单、配额、作用域路由、供应商资源容量限制、文本前置策略、响应钩子和用量归属。请求钩子接收 JSON/文本字段，multipart 文件字节保持不透明。直通媒体审计只记录模型、内容类型和字节数，不保存上传或生成的媒体。客户端 Cookie 和鉴权头不转发，使用已配置供应商凭证及受保护的出站传输。结果不确定的直通媒体提交失败不会自动换路由再次生成。

Token 计费使用上游返回的用量。二进制音频、字幕文本或未返回 Token 的供应商不会被虚构 Token 成本；请求数/并发限制和请求日志仍生效。按秒、按图、按字符的费用不转换成 Token 单价。为返回 Token 的模型配置价格，其他费用与供应商账单核对。

回归测试使用本地 HTTP 模拟供应商及合成请求，覆盖文件上传、二进制/文本响应、绘图扩展参数、视频异步请求结构、声音素材、大整数、权限拒绝、钩子和缓存跳过。测试不消耗付费生成额度；具体供应商和模型的可用性需使用实际账户验证。
