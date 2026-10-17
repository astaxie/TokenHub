# Text, image, audio, and video API guide

Language: English | [简体中文](zh-CN/media-apis.md) | [日本語](ja/media-apis.md)

Call approved text and media models through one TokenHub gateway and a project API key. This guide follows the task-oriented organization of the [APIMart API manual](https://docs.apimart.ai/cn/index), while documenting TokenHub's authentication, routing, task isolation, and limits.

**Endpoint support does not imply model availability.** An administrator must configure the upstream credential, publish a public model ID, map it to the upstream model, and grant your key access. Formats, duration, resolution, voices, model versions, and asset expiration depend on the upstream. TokenHub does not bundle vendor accounts or promise the entire APIMart catalog.

## Choose an API

| Task | Method and path | Result |
| --- | --- | --- |
| Discover approved models | `GET /v1/models` | Models available to this key |
| Text, vision, tool calls | `POST /v1/chat/completions` | Chat JSON or SSE |
| Responses-compatible requests | `POST /v1/responses` | Response JSON or SSE; optional local background job |
| Anthropic Messages | `POST /v1/messages` | Anthropic JSON or SSE |
| Gemini native requests | `POST /v1beta/models/{model}:generateContent` | Native Gemini JSON |
| Gemini native streaming | `POST /v1beta/models/{model}:streamGenerateContent?alt=sse` | Native Gemini SSE |
| Generate images | `POST /v1/images/generations` | Images or an upstream task receipt |
| Edit / vary images | `POST /v1/images/edits`, `POST /v1/images/variations` | Provider image response |
| Text to speech | `POST /v1/audio/speech` | Audio bytes or provider-supported stream |
| Speech recognition / translation | `POST /v1/audio/transcriptions`, `POST /v1/audio/translations` | JSON, text, or subtitles |
| Generate video | `POST /v1/videos/generations` | Upstream task receipt |
| Poll an image / video task | `GET /v1/tasks/{task_id}` | Provider status and results |

For embeddings and document ranking, see [Embeddings](embeddings.md) and [Reranking](rerank.md). The deployment's interactive contract is at `/docs`, with machine-readable schemas at `/openapi.json`.

## Authentication and quickstart

Use the gateway address, not the console's port. Examples use a root URL without `/v1`; OpenAI SDK clients instead receive `base_url="$TOKENHUB_URL/v1"`. Project API keys come from **Key Management**; console login tokens cannot call model APIs.

```bash
export TOKENHUB_URL='http://localhost:8080'
export TOKENHUB_API_KEY='YOUR_TOKENHUB_PROJECT_KEY'

curl --fail-with-body "$TOKENHUB_URL/v1/models" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY"
```

Use a returned `data[].id` as `model`. Names such as `text-model`, `image-model`, `speech-model`, `transcription-model`, and `seedance-video` below are example **public aliases** to publish or replace. They need not equal upstream model IDs.

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/chat/completions" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"text-model","messages":[{"role":"user","content":"Explain an API gateway in one sentence."}],"stream":false}'
```

Read text from `choices[].message.content`. Save the request ID for **Request Logs**. A model appearing in `/v1/models` confirms visibility and routing eligibility, not live availability of every upstream feature. APIMart's `expand` metadata schema and account-balance APIs are not implemented.

## Provider setup and model selection

For an APIMart-compatible account, add an **OpenAI-compatible** provider with base URL `https://api.apimart.ai/v1` and its credential. Publish aliases, for example `text-model` to the account's text model, `image-model` to `gpt-image-2`, and `seedance-video` to `seedance-2.0`. Select `image`, `audio`, or `video` as appropriate; configure input/output modalities for multimodal chat. Media Chat/Responses requests bypass response caches.

Native Anthropic and Gemini requests require a route that supports the native protocol and response shape; an OpenAI-compatible base URL alone does not establish native compatibility. See the [User Guide](user-guide.md) for adapters and SDK configuration. Vendor documentation may show wrapped text responses; verify the selected route returns the protocol your client expects.

Public `gpt-image-2`, Codex subscription image models, and configured managed image plugins use TokenHub's managed image workflow. To use the full upstream Images contract, including APIMart asynchronous generation, publish a separate alias such as `image-model` mapped to `gpt-image-2`. See the [managed image guide](user-guide.md#codex-subscription-image-generation).

Upgrading applies database migration v8, `add-media-task-bindings`, through the normal startup migration process. Task bindings survive restarts; keep the configured server secret stable so encrypted upstream task IDs remain readable. No new environment variable is required.

## Text and multimodal understanding

### Chat Completions

| Field | Requirement | Meaning |
| --- | --- | --- |
| `model` | Required | Published public model ID |
| `messages` | Required | Conversation; supported models accept content parts |
| `stream` | Optional boolean | Explicit `true` for SSE or `false` for one JSON response |
| `max_tokens`, `max_completion_tokens` | Optional | Output limit, subject to route/model behavior |
| `temperature`, `reasoning_effort` | Optional | Model-dependent generation controls |
| `tools`, `tool_choice`, `response_format` | Optional | Tools and structured output when supported |

For streaming, use `curl -N` and `"stream":true`; consume incremental `choices[].delta` events until completion. Do not infer a universal streaming default from another provider's documentation.

For vision, replace the user message with content parts on a model configured for image input:

```json
{"role":"user","content":[{"type":"text","text":"Describe this image."},{"type":"image_url","image_url":{"url":"https://your-assets.example/reference.png"}}]}
```

Replace asset URLs with accessible files. OpenAI-compatible routes preserve supported image/audio fields; translated native routes have narrower capabilities. Chat-to-Gemini accepts base64 image data URIs, and unsupported audio parts return an error rather than being dropped. See [protocol differences](user-guide.md#tool-calling-and-multimodal-content-on-anthropic-and-gemini-routes).

### Responses, Anthropic, and Gemini

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

Read Responses output from `output`, Anthropic text from `content[]`, and Gemini content from `candidates[].content.parts[]`. Preserve tool and reasoning fields for subsequent turns. Responses also carries supported vendor media objects. Native Gemini media preserves `generationConfig`, `responseModalities`, `imageConfig`, inline media, and thought signatures; media JSON/SSE is buffered under the media limit.

## Images

### Generate an image

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/images/generations" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"image-model","prompt":"A watercolor lighthouse at sunrise","n":1,"size":"16:9","resolution":"2k"}'
```

This uses APIMart `gpt-image-2` parameters through a public alias. Other providers may require dimensions such as `1024x1024`; do not mix model-specific conventions.

| Field | Meaning |
| --- | --- |
| `model`, `prompt` | Public model and image description |
| `n` | Image count; upstream enforces its maximum |
| `size`, `resolution`, `quality` | Upstream-specific aspect ratio, dimensions, resolution tier, and quality |
| `image_urls`, other reference fields | Reference images when supported |
| `response_format` | Upstream-supported format, such as `url`, `b64_json`, or `base64` |

Synchronous responses commonly contain `data[].url` or `data[].b64_json`. Asynchronous responses contain `data[].task_id`; poll each ID using the task API below. Provider result fields are preserved. TokenHub does not download returned URLs or extend their lifetime.

### Edit an image or supply a mask

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/images/edits" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F 'model=image-model' \
  -F 'prompt=Replace the background with a sunrise sky' \
  -F 'image=@input.png' \
  -F 'mask=@mask.png'
```

Use local files and a route supporting multipart edits. Let cURL generate the multipart boundary. JSON edits and `/v1/images/variations` work with compatible providers; masks, repeated image fields, and options follow that provider's contract. Managed image jobs retain separate one-image validation, stored assets, and `Prefer: respond-async` behavior.

## Audio

### Text to speech

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/audio/speech" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"speech-model","input":"Hello from TokenHub.","voice":"alloy","response_format":"opus","speed":1}' \
  --output speech.opus
```

| Field | Meaning |
| --- | --- |
| `model` | Published speech model |
| `input` | Text; length limit belongs to the upstream |
| `voice` | Upstream voice ID; `alloy` is an example, not universal |
| `response_format` | Supported format; save with a matching extension |
| `speed` | Speaking speed if supported |

A successful response is audio bytes with the upstream content type, not JSON or a base64 wrapper. Check HTTP status before using the saved file. Provider SSE speech is accepted where supported, but direct media delivery is buffered rather than low-latency audio streaming.

### Transcribe or translate audio

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F 'model=transcription-model' \
  -F 'file=@sample.wav' \
  -F 'response_format=json'
```

`file` is the local upload and `model` the public speech-recognition model. Optional `language`, `prompt`, `response_format`, and repeated `timestamp_granularities[]` follow the upstream contract. JSON commonly returns `{"text":"..."}`; text, SRT, VTT, and verbose JSON are provider-dependent. Use `/v1/audio/translations` with a compatible model for translation; target-language behavior belongs to the model.

Voice cloning and music remain available through existing provider-specific Chat/Responses workflows in [media operations](media-api-operations.md). APIMart's `/v1/music/*`, voice/asset management, uploads, and webhook management are not implemented as a general TokenHub API family.

## Videos, including Seedance

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/videos/generations" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-video","prompt":"A calm ocean at sunrise, slow camera movement","duration":5,"size":"16:9","resolution":"720p","generate_audio":true}'
```

Configure `seedance-video` to an upstream model such as APIMart `seedance-2.0`. Other compatible video models use the same endpoint with their own options.

| Field | Meaning |
| --- | --- |
| `model`, `prompt` | Public model and scene description |
| `duration` | Requested seconds; allowed values depend on the model |
| `size`, `resolution` | Model-specific aspect ratio and resolution |
| `generate_audio` | Accompanying audio when supported |
| `image_urls`, `video_urls`, `audio_urls` | Model-supported reference assets; use reachable URLs |
| Other options | Provider extensions are preserved and validated upstream |

First/last frames, multiple references, duration ranges, and reference combinations are not universal. TokenHub does not implement Volcengine-native `/content_generation/tasks`.

DMXAPI Seedance remains available through `/v1/responses`, with a generation model and an auxiliary model such as `seedance-2-0-get`. Its task IDs and polling rules differ from `/v1/tasks`; see [DMXAPI setup and examples](media-api-operations.md).

## Submit and poll asynchronous tasks

APIMart-compatible image/video submissions return a receipt shaped like this:

```json
{"code":200,"data":[{"status":"submitted","task_id":"task_example"}]}
```

This means the upstream accepted work, not that generation is complete. TokenHub replaces vendor task IDs with opaque IDs and persists the submitting project/key and provider/resource binding. Use **the same project API key** and exact returned ID:

```bash
export TOKENHUB_TASK_ID='TASK_ID_FROM_SUBMISSION'
curl --fail-with-body "$TOKENHUB_URL/v1/tasks/$TOKENHUB_TASK_ID?language=en" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY"
```

The optional `language` query parameter accepts `en`, `zh`, `ja`, `ko`, `ru`, `fr`, `de`, `id`, `pt`, or `es` for upstream task error text. Repeated `language` values and other query parameters return `400 invalid_task_query`.

| Response field | Meaning |
| --- | --- |
| `data.id` | TokenHub task ID for this polling workflow |
| `data.status` | Upstream task status |
| `data.progress` | Optional upstream progress, not a gateway estimate |
| `data.result` | Model-specific output; APIMart may return `images[]` or `videos[]` with `url` arrays |
| `data.error` | Upstream task failure details when present |
| `data.cost`, `data.credits_cost` | Upstream metadata, not a TokenHub invoice |

Poll `pending`/`processing` with a bounded timeout and a delay, for example 3–5 seconds with backoff. `completed` means success; `failed` and `cancelled` are terminal failures. Treat other nonterminal states according to the upstream contract. Save result URLs promptly and honor `expires_at`. HTTP 200 may contain a failed task; inspect both HTTP status and `data.status`.

Queries use the original route/account without failover. The key must remain active and permitted to use the model. Another key, even in the same project, cannot retrieve the task; arbitrary vendor IDs are not accepted. Route/provider changes may make existing tasks unavailable. The upstream runs the background work; TokenHub adds no worker, cancellation, or completion webhook service. Provider callbacks configured outside TokenHub use provider identifiers and semantics.

Bindings expire **30 days after submission**, independently of the upstream asset URL's lifetime. Expired, unknown, or inaccessible tasks return `404 media_task_not_found`; rotating a project key does not transfer its tasks to the replacement key. Replacing the original provider credential, URL, headers, or other account settings prevents polling with `503 media_task_route_unavailable`, even if the replacement account can serve the same model. If saving a binding fails, submission returns `503 media_task_binding_unavailable`; the upstream may already have accepted the generation, so inspect logs before retrying.

### Continue or regenerate a video

For video models that support a previous-task reference, pass the **TokenHub task ID returned by the source submission** in the relevant top-level field:

| Field | Upstream operation example |
| --- | --- |
| `draft_task_id` | Seedance 2.5 draft to final video |
| `draft_from_task_id` | FLUX.3 draft to final video |
| `extend_from_task_id` | Gemini Omni or PixVerse video extension |
| `source_task_id` | MiniMax H3 regeneration using a different target model |

These fields are recognized only on `POST /v1/videos/generations`. For example, `{"model":"followup-video","source_task_id":"task_example"}` uses an administrator-published alias for a compatible regeneration model. Model-specific completion, age, resolution, and parameter restrictions still apply upstream; not every model supports every field. Prompt text, asset URLs, and nested extension identifiers are not interpreted as task references.

Use the same project API key, retain permission to both the source and target models, and reference an unexpired binding within its 30-day lifetime. Malformed, unknown, expired, foreign-key, or raw vendor task IDs return `404 media_task_not_found` before generation. TokenHub resolves authorized handles to provider IDs and uses the original provider account/resource. The target alias, route, and upstream model may differ when that account supports the follow-up operation. TokenHub never switches to another account. A changed account fingerprint, including changed credentials or settings, returns `503 media_task_route_unavailable`; ordinary routing, policy, and capacity errors still apply when the original account is unavailable.

References to conflicting accounts return `400 invalid_media_task_reference`. The upstream decides whether a combination of otherwise valid references is supported. Each accepted follow-up creates a new task and may incur a new upstream charge. Request hooks retain the public handles; provider-call hooks receive the resolved provider IDs.

Do not interchange these asynchronous mechanisms:

| Mechanism | ID and query |
| --- | --- |
| APIMart-compatible image/video task | Returned `task_id`; `GET /v1/tasks/{task_id}` |
| Managed TokenHub image job | Managed job contract in the [image guide](user-guide.md#codex-subscription-image-generation) |
| Responses with `background:true` | Local Responses job; completion means the upstream API call finished, not that vendor rendering finished |

Completed background media Responses expose a valid vendor root ID in `x-tokenhub-upstream-response-id`; pass it to the provider's auxiliary query model. It is not a `/v1/tasks` ID.

## Errors, limits, and billing

Gateway errors normally use this envelope; native Anthropic/Gemini use protocol-specific errors:

```json
{"error":{"message":"Model is not allowed for this API key","type":"model_not_allowed","code":"model_not_allowed"},"request_id":"REQUEST_ID"}
```

| HTTP status | Typical cause | Next step |
| --- | --- | --- |
| `400` | Invalid JSON, multipart controls, or parameters | Correct the request for the selected model |
| `401` / `403` | Invalid, disabled, expired key or model access denied | Check project key and model grants |
| `404` | Unknown or inaccessible task/model | Check ID and submitting key |
| `413` | Request exceeds size limit | Reduce upload or adjust administrator limit |
| `429` | Rate, concurrency, quota, or budget limit | Inspect error code and retry headers |
| `501` | Route cannot serve the protocol | Configure a supported provider/route |
| `502` / `503` | Invalid upstream output, unavailable route, or provider failure | Inspect Request Logs with the request ID |

Direct media requests use `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` (32 MiB default); multipart allows at most 128 parts and 1 MiB per text field. Direct responses are buffered up to 128 MiB. Managed image limits remain separate; upstream file/format limits may be lower. Send multipart `model` and `stream` only once; JSON `stream` must be boolean.

A submission can bind at most 128 returned task IDs. Invalid task IDs or oversized task batches fail with `502 invalid_media_task`; this is not evidence that the upstream canceled its work.

Authentication, allowlists, quotas, rate limits, text policies, scoped hooks, guarded outbound transport, and logging apply. File bytes stay opaque to text policies. Client credentials and cookies are not forwarded; configured provider credentials are used. Ambiguous generation failures do not automatically resubmit on another route. Avoid blind client retries after timeouts: the upstream may already have accepted a billable generation.

Token accounting uses reported usage; binary audio, subtitles, and providers without usage do not acquire invented token charges. Polling is not counted again as generation-token usage. Per-image, per-second, character, or vendor-credit charges are not automatically converted into token prices; reconcile with the upstream bill. Detailed hook and stream validation is in [media protocol operations](media-api-operations.md).

## Integration recipes

| Goal | Sequence |
| --- | --- |
| Chat assistant | List models → send conversation → preserve tool/reasoning continuity |
| Image generation | Submit Images → use synchronous result or poll every returned task ID |
| Narrated video | Generate speech → host audio for the provider → submit a model supporting audio references → poll → save assets |
| Meeting notes | Transcribe uploaded recording → send returned text to Chat/Responses |
| DMXAPI Seedance | Publish generation/query models on one account → submit Responses → query with vendor ID |

Regression tests use synthetic local providers to verify protocol behavior, access isolation, uploads, hooks, and accounting. They do not verify paid generation or all vendor models with a live account. Sources: [APIMart text](https://docs.apimart.ai/en/api-reference/texts/general/chat-completions.md), [images](https://docs.apimart.ai/en/api-reference/images/gpt-image-2/generation.md), [speech](https://docs.apimart.ai/en/api-reference/audios/tts.md), [Seedance](https://docs.apimart.ai/en/api-reference/videos/seedance-2-0/generation.md), and [task status](https://docs.apimart.ai/en/api-reference/tasks/status.md).
