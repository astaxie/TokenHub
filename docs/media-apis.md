# Images, audio, video, and music APIs

Language: English | [简体中文](zh-CN/media-apis.md) | [日本語](ja/media-apis.md)

TokenHub supports the media protocol families used in the [DMXAPI documentation](https://doc.dmxapi.cn/jichu.html). Configure the upstream provider and publish each generation, upload, query, or download model before calling it. This is protocol compatibility: model availability and accepted parameters remain the upstream provider's responsibility. No vendor credentials or prices are bundled.

## Endpoint coverage

| Endpoint | Uses and examples |
| --- | --- |
| `POST /v1/images/generations` | GPT Image, Seedream/Jimeng, Qwen Image; preserves vendor options, reference images, multiple results, URLs, and base64 |
| `POST /v1/images/edits` | JSON or multipart edits; preserves masks and uploaded files |
| `POST /v1/images/variations` | Compatible provider image variations |
| `POST /v1/audio/speech` | Speech synthesis, including MiniMax speech-2.6; returns provider audio bytes |
| `POST /v1/audio/transcriptions` | Multipart speech recognition, including gpt-4o-transcribe; JSON, text, SRT, or VTT results |
| `POST /v1/audio/translations` | Compatible provider audio translation |
| `POST /v1/responses` | Seedance, Hailuo, Kling, Vidu, PixVerse/Paiwo, Wan, HappyHorse videos; Seedream, Wan, Qwen, Agnes and SciDraw images; MiniMax voice upload/clone, advanced speech, music/lyrics and Mureka |
| `POST /v1/chat/completions` | MiMo TTS/voice design/clone, Qwen Omni captioning and multimodal audio, Recraft drawing |
| `POST /v1beta/models/{model}:generateContent` and `:streamGenerateContent` | Gemini native image generation/editing and multimodal output |

Use the endpoint specified for the particular model in the upstream documentation. The DMXAPI video examples submit and query through `/v1/responses`; they do not require a new `/v1/videos` endpoint. Existing Chat, Responses, and Gemini APIs retain their streaming support and vendor extension fields.

## Provider and model setup

1. Add an **OpenAI-compatible** provider with base URL `https://www.dmxapi.cn/v1` and its credential. Native Gemini examples require a **Gemini** provider using the documented base URL and the same upstream account.
2. Publish the public model and map its route to the exact upstream model ID. Select `image`, `video`, or `audio` as its modality (music uses `audio`), or include those output modalities for a multimodal chat model. These Chat/Responses requests bypass response caches so generation is not reused and task status remains fresh.
3. Publish auxiliary models too, for example `seedance-2-0-get`, `MiniMax-Hailuo-query`, `MiniMax-Hailuo-get`, and voice/asset upload models. Grant the project key access to every model needed in the workflow.
4. Pin generation and auxiliary routes to the same provider account/resource. Provider task/file IDs are passed through unchanged, including large JSON integers. TokenHub does not translate these into local background jobs or automatically bind vendor task ownership. Use separate upstream accounts for tenants needing isolated vendor task namespaces.

The managed `gpt-image-2`, Codex subscription, and plugin image profiles retain their existing validation, one-image job flow, stored assets, and `Prefer: respond-async` support. To use a provider's full Images contract for an upstream model with a managed public name, publish a separate public alias (for example `vendor-gpt-image`) mapped to that upstream ID. Ordinary image routes return the provider's response directly; local job polling and signed TokenHub image URLs apply only to managed jobs.

## Examples

Replace `TOKENHUB_API_KEY` with a project key in your environment. Model names below must already be published and allowed for that key.

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

# Save the returned id, then query until the upstream reports completion.
curl https://tokenhub.example/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-2-0-get","input":"TASK_ID_FROM_SUBMISSION"}'
```

## Limits, accounting, and verification

The new direct media routes use `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` (32 MiB by default). Multipart requests allow up to 128 parts and 1 MiB per text field. Responses are buffered up to 128 MiB before delivery and keep the provider content type; this direct path does not provide low-latency chunk delivery. Existing managed image limits remain unchanged. Files are not downloaded from response URLs by TokenHub.

Authentication, model allowlists, quotas, scoped routing, provider resource capacity, text preflight policies, response hooks, and usage attribution apply. Request hooks see JSON/text fields; multipart file bytes are opaque. Media Responses text policies also inspect vendor prompts, lyrics, and nested Wan messages while preserving opaque asset/task IDs. Direct media audit records contain model, content type, and byte count, not uploaded/generated media. Client cookies and authorization are not forwarded; configured provider credentials and guarded outbound transport are used. An ambiguous media submission failure does not trigger an automatic second generation on another route, including media models on Chat/Responses. Definite authentication or rate-limit rejections still allow failover.

Matching `provider_call` hooks run before direct media adapters and can deny, skip, or handle a route. Non-stream hooks return a JSON provider response, or a binary envelope with string `data_base64` and `content_type` fields. Streaming hooks return `stream_events`; delivery remains buffered. Response/guardrail hooks processing binary or text bodies must preserve valid `data_base64`; malformed patches fail instead of returning an empty successful response.

Token billing uses upstream-reported usage. Binary audio, text subtitles, and providers that report no tokens do not acquire an invented token cost; request/concurrency limits and request logs still apply. Per-second, per-image, and character-based billing are not converted into token prices. Configure pricing for token-reporting models and reconcile other charges with the upstream bill.

Regression tests use local HTTP providers and synthetic payloads for media uploads, binary and text responses, vendor image fields, asynchronous video request shapes, voice assets, large IDs, permission failures, hooks, and cache bypass. They verify protocol handling without paid live generation; individual vendor/model availability must be checked with your account.
