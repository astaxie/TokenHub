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

Use the endpoint specified for the particular model in the upstream documentation. The DMXAPI video examples submit and query through `/v1/responses`; they do not require a new `/v1/videos` endpoint. Existing Chat, Responses, and Gemini APIs retain their streaming support and vendor extension fields. The OpenAPI contract includes Chat `input_audio` parts and provider-specific Responses input/output objects, including Wan messages and MiniMax voice uploads; providers validate their own media fields.

## Provider and model setup

1. Add an **OpenAI-compatible** provider with base URL `https://www.dmxapi.cn/v1` and its credential. Native Gemini examples require a **Gemini** provider with base URL `https://www.dmxapi.cn/v1beta` and the same upstream account.
2. Publish the public model and map its route to the exact upstream model ID. Select `image`, `video`, or `audio` as its modality (music uses `audio`), or include those output modalities for a multimodal chat model. These Chat/Responses requests bypass response caches so generation is not reused and task status remains fresh.
3. Publish auxiliary models too, for example `seedance-2-0-get`, `MiniMax-Hailuo-query`, `MiniMax-Hailuo-get`, and voice/asset upload models. Grant the project key access to every model needed in the workflow.
4. Pin generation and auxiliary routes to the same provider account/resource. Provider task/file IDs are passed through unchanged, including large JSON integers. TokenHub does not translate these into local background jobs or automatically bind vendor task ownership. Use separate upstream accounts for tenants needing isolated vendor task namespaces.

For native Gemini media, configure a media modality or output modality on the published model and call its `/v1beta/models/{model}:generateContent` or `:streamGenerateContent?alt=sse` endpoint. These requests bypass caches and preserve native `generationConfig` (including `responseModalities` and `imageConfig`), inline media, and thought signatures. The provider model is selected by the route. Native JSON/SSE responses preserve generated media and use `usageMetadata` for token accounting. Both responses are buffered under the 128 MiB media limit; authentication, text policies, scoped hooks, and guarded transport apply.

Native Gemini media streams reject terminal SSE event names, `type:"error"` or `*.failed` payloads, and non-null nested `response.error` fields, including provider-hook output. Such errors remain failures even after a candidate reports a finish reason; known token usage and explicit plugin usage overrides are retained, and error credentials are not returned. A nested `response.error:null` remains compatible with successful events.

The managed `gpt-image-2`, Codex subscription, and plugin image profiles retain their existing validation, one-image job flow, stored assets, and `Prefer: respond-async` support. To use a provider's full Images contract for an upstream model with a managed public name, publish a separate public alias (for example `vendor-gpt-image`) mapped to that upstream ID. Ordinary image routes return the provider's response directly; local job polling and signed TokenHub image URLs apply only to managed jobs. Direct image requests accept provider-specific `response_format` strings, such as Qwen Image `base64`; built-in managed images still accept only `url` or `b64_json`, while managed plugin models enforce their configured formats.

With `background:true` on `/v1/responses`, the response body's `id` identifies TokenHub's local request job. Completed media polling replies expose the vendor's original root ID in `x-tokenhub-upstream-response-id`; use that ID with the vendor's auxiliary query model. Local completion means the upstream API call finished; vendor rendering may still require polling. String and numeric IDs are preserved exactly. The header is omitted for nonscalar IDs, control characters, surrounding whitespace, or values over 2048 bytes.

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

Multipart `model` and `stream` controls must be single text fields. On `/v1/audio/speech`, `stream_format:"sse"` also selects streaming admission and provider hooks; `stream_format` must be a single non-empty text value, and request hooks cannot change the effective stream mode. JSON `stream` must be a boolean; multipart `stream` must contain a boolean text value. Ambiguous controls are rejected before routing, including request-hook patches. Text policies inspect every value of `input`, `instructions`, `prompt`, `negative_prompt`, and `text`, including repeated multipart fields, while preserving repeated options such as `timestamp_granularities[]`.

Authentication, model allowlists, quotas, scoped routing, provider resource capacity, text preflight policies, response hooks, and usage attribution apply. Request hooks see JSON/text fields; multipart file bytes are opaque. Media Responses text policies also inspect vendor prompts, Kling `multi_prompt[].prompt` shot descriptions, lyrics, and Wan text in nested messages, `input.input.prompt`, and `parameters.negative_prompt` while preserving opaque asset/task IDs. Direct media audit records contain model, sanitized policy decisions, content type, and byte count, not uploaded/generated media. Client cookies and authorization are not forwarded; configured provider credentials and guarded outbound transport are used. An ambiguous media submission failure does not trigger an automatic second generation on another route, including media models on Chat/Responses. Definite authentication or rate-limit rejections still allow failover.

Matching `provider_call` hooks run before direct media adapters and can deny, skip, or handle a route. Direct image hooks use the actual endpoint protocol (`images/generations`, `images/edits`, or `images/variations`) for scope matching; managed image jobs retain `images/generations`. Non-stream hooks return a JSON provider response, or a binary envelope with string `data_base64` and `content_type` fields. Streaming hooks return `stream_events`; delivery remains buffered. Response/guardrail hooks processing binary or text bodies must preserve valid `data_base64`; malformed patches fail instead of returning an empty successful response. Provider and response hooks both allow CR/LF line breaks in Base64, but reject additional encoded data after padding. The 128 MiB response limit applies to both hook outputs, using serialized JSON bytes or decoded binary bytes without counting Base64 line breaks; oversized replacements fail while retaining reported usage.

Non-stream provider-hook JSON that exceeds the response size limit also retains reported token usage in both request and route-attempt accounting, including native Gemini `usageMetadata`. Explicit plugin usage, including zero, takes precedence. The response remains a failure without retry or a provider health penalty.

The Responses OpenAPI schema requires `model`; `input` requirements depend on the chosen provider model. For example, DMX lyric/music generation can omit `input` to generate without a supplied prompt. Omitted input is forwarded without adding an empty value. The `input` array also accepts image URL or data-URI strings, as used by Vidu Q3 start/end-frame generation. Responses `output` accepts a standard item array or a provider-specific object, such as Qwen Image 3.0 results under `output.choices[].message.content[].image`; the same shapes apply to completed background jobs. Output arrays also accept Seedream image results as `{"type":"image_url","image_url":{"url":"..."}}` items.

JSON response handling recognizes `application/json` and MIME types with a `+json` suffix, including parameters and case variations. These responses receive the same validation, token metering, and JSON hook payloads; their original content type is preserved. A recognized JSON base type still receives validation when its parameters are malformed or duplicated. A parameter containing the text `application/json` does not turn a text or binary response into JSON.

Direct media JSON containing a non-null top-level `error` is a failure even when the upstream HTTP status is 200. This also applies to provider-hook output and final response/guardrail-hook replacements. Failures return a generic message without upstream credentials, retain reported token usage, and do not resubmit generation; `error:null` remains compatible with successful responses.

SSE response handling recognizes the `text/event-stream` MIME base type even when its parameters are malformed or duplicated, so invalid parameters cannot bypass event validation, token metering, or event hooks. A parameter containing `text/event-stream` does not turn a text or binary response into SSE.

Direct media SSE requires each non-empty event payload other than `[DONE]` to be one complete JSON object. Malformed upstream or provider-hook events fail before buffered delivery and retain known usage, including a complete leading object with invalid trailing data. Response and stream hooks cannot turn valid events into malformed payloads or successful error streams; final validation retains the original upstream usage.

For SSE responses, `stream_transform`, `response_post`, and `guardrail_post` run on individual events before buffered delivery. Upstream token usage is retained even if a subsequent stream error or policy blocks the result. Terminal stream errors are recorded as failures and do not trigger a second generation; provider credentials in error messages are redacted. Client cancellation and classified outbound proxy failures do not count as provider health failures; proxy failures also stop failover. If a provider-hook stream exceeds the response buffer limit, usage from earlier complete events is retained unless explicit plugin usage overrides it; overflow remains a failure without retry. Plugin-produced SSE receives the same error and usage inspection; explicit plugin usage, including zero, overrides event usage. Plugin-produced JSON also uses response-body token usage when no explicit plugin usage is supplied; explicit zero still overrides it. JSON responses must contain one complete object with no trailing values or garbage. Final JSON output after response/guardrail hooks must also remain an object; null, arrays, and scalar replacements are rejected. Invalid responses fail without resubmission, retaining usage from a complete leading object even if reading the response later fails or exceeds the size limit. Incomplete JSON objects do not invent usage. Failed usage-attribution hooks retain original upstream usage. If final post-hook response validation fails after attribution, request and route-attempt accounting retain the same attributed usage. Direct media HTTP 408 responses do not trigger automatic resubmission. Audit records retain sanitized policy decisions without replacement text or media content.

Media models on the streaming Responses API recognize standard Responses completion events, Wan `[DONE]` markers, and MiniMax `data.status=2` completion frames. Top-level and nested token usage is retained; streams ending without a completion marker are recorded as failed. MiniMax music events with top-level `status:"failed"` are terminal provider failures: error messages are redacted before delivery, known usage is retained, and later completion markers cannot turn the failure into success. Non-empty event data other than `[DONE]` must be one complete JSON object. Malformed events are rejected before forwarding, retain known usage, and cannot turn a broken stream into a successful completion; heartbeats and valid vendor events remain unchanged. Media events are bounded at 128 MiB per event. Text-model Responses retain their existing completion rules.

OpenAI-compatible Chat streams for models configured with media output also allow events up to 128 MiB, including providers that return a complete generated audio clip in one event. Text-model Chat streams retain the 8 MiB event limit; client audio fields alone do not raise it.

Token billing uses upstream-reported usage. Transcription JSON and SSE usage preserves the audio-token breakdown reported in `input_token_details.audio_tokens`. Binary audio, text subtitles, and providers that report no tokens do not acquire an invented token cost; request/concurrency limits and request logs still apply. Per-second, per-image, and character-based billing are not converted into token prices. Configure pricing for token-reporting models and reconcile other charges with the upstream bill.

Regression tests use local HTTP providers and synthetic payloads for media uploads, binary and text responses, vendor image fields, asynchronous video request shapes, voice assets, large IDs, permission failures, hooks, and cache bypass. They verify protocol handling without paid live generation; individual vendor/model availability must be checked with your account.
