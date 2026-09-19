# TypeSafe / Jev integration

Language: English | [简体中文](zh-CN/typesafe.md) | [日本語](ja/typesafe.md)

TokenHub exposes TypeSafe System One through a dedicated Provider adapter and `POST /v1/systemone`. Jev evaluates typed decisions against shared state. It returns labels, probabilities, and scores; it does not generate chat text. This first phase adds Provider setup, model metadata, discovery, health checks, routing, governance, and accounting. A dedicated playground and internal semantic routing are outside this phase.

## Configure a Provider

1. In **Provider Channels**, add **TypeSafe**. The adapter type is `typesafe` and the upstream Base URL is `https://api.typesafe.ai/v1`.
2. Enter the TypeSafe API key in the credential field and import the required Jev models. Credentials use TokenHub's existing encrypted storage and redaction rules.
3. Publish a model with modality `decision`, capability `systemone`, and a route to the imported Provider model. Grant the project key access to the published model. Provider inventory costs and client-facing model prices are configured separately.
4. Run the Provider health check. It authenticates against upstream `GET /v1/models`; it does not run inference. Use a synthetic System One request to verify inference separately.

The catalog includes `jev-1.13.0`, `jev-latest`, and `jev-preview`. Pin a version for repeatable evaluations; aliases can change. Live discovery may return only aliases and does not imply that a pinned version is unavailable. Discovery via `POST /api/admin/provider-catalog/custom` accepts `type: "typesafe"` and uses the native model-list format. It does not infer prices for newly discovered models.

## Call the gateway

Use the published TokenHub model name and a **TokenHub project API key**:

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

The native response contains `model`, `answers`, and `usage.input_tokens` / `usage.output_tokens`. Answer keys match question keys. The response model is the upstream resolved version; request logs also retain the published model and route's upstream model name.

Successful responses include TokenHub's `x-request-id` and, when reported by the winning upstream, `x-typesafe-request-id`. The SDK's `client.systemOne(...).withResponse().requestId` reads the latter. Both headers are exposed to browser clients; a missing upstream ID remains absent.

| Primitive | Criteria | Answer |
| --- | --- | --- |
| `choice` | Nonempty object of label-to-description entries | `choice`, label `probabilities`, and `confidence` |
| `noul` | Optional object with `true` and/or `false` descriptions, or `null` | `noul` in `[0, 1]`; no confidence field is required |
| `score` | Ordered array of at least two level descriptions | Expected level index `score` in `[0, N-1]`, `legend`, level `probabilities`, and `confidence` |

`state` is required and accepts a string, JSON object, array, or `null`. Each question may omit `instructions` or provide a string, object, array, or `null`; criterion descriptions accept the same types. Omitted fields and explicit `null` values remain distinct when forwarded, following the [SDK 0.6.0 types](https://github.com/typesafe-ai/typesafe-sdk-js/blob/v0.6.0/src/types.ts). Nested values retain JSON numeric precision. Questions share state and are evaluated independently. Low confidence is a normal result, returned without automatic retry or escalation. Applications define their own acceptance thresholds.

The gateway accepts 1–1,024 questions, at most 4,096 criteria per question, and at most 64 nested container levels per JSON entry. Normal request-body limits also apply. The catalog records a 64,000-token total context and a 32,000-token state-plus-largest-question limit from the upstream model documentation. TokenHub estimates tokens for quota admission; TypeSafe remains authoritative for tokenizer-specific context validation. Streaming and chat-only fields are rejected. Invalid JSON or unknown fields return `400`; invalid primitive semantics return `422`.

## JavaScript SDK compatibility

`@typesafe-ai/sdk@0.6.0` can call `client.systemOne()` with `baseURL` set to the **TokenHub origin without `/v1`**; the SDK appends `/v1/systemone` itself. Supply the TokenHub key and published model name. The SDK smoke test uses this method:

```bash
cd sdk
npm ci
TOKENHUB_BASE_URL=http://localhost:8080 \
TOKENHUB_API_KEY="$TOKENHUB_API_KEY" \
TOKENHUB_MODEL=jev-1.13.0 npm run test:systemone
```

This phase covers `systemOne()`, not the complete TypeSafe SDK surface. TokenHub retains its existing multi-protocol `/v1/models` contract; it does not emulate TypeSafe's model-list response for SDK `models.list()`. OpenAI chat, Responses, embeddings, and streaming contracts are unchanged. The existing chat playground does not execute Jev.

## Governance and accounting

Each score `legend` entry must equal the corresponding criterion sent upstream. When a request hook transforms criteria, validation uses the winning route's effective criteria and preserves those values in the response, including any masking. Response hooks cannot replace the legend with different criteria. Question IDs, answer types, choice labels, and score-level counts must still match the shared request after preprocessing.

The gateway validates response consistency: probabilities must sum to within one percentage point of 1; a Choice's selected probability may trail the maximum by at most one percentage point; a Score may differ from the raw sum of index × probability by at most `0.01 × (N-1)`. These inclusive gateway tolerances include a small floating-point epsilon and do not guarantee acceptance of every independently rounded upstream distribution. Probabilities are not renormalized. Inconsistent responses return sanitized `502` errors while valid reported usage is retained. TypeSafe model discovery also applies the gateway's shared custom-header validation before any upstream request.

Requests use project authentication, model permissions, quotas, route selection, Provider credentials, error classification, usage persistence, request logs, and trace export. Privacy and guardrail hooks receive `route_protocol: "systemone"`. Deterministic outbound checks inspect state, question IDs, instructions, and criteria, including nested object keys. A policy requesting redaction of a structural key blocks the request rather than renaming the response contract. Optional pre-request, route, Provider-call, response, and usage hooks follow the existing gateway lifecycle. System One does not use response caching in this phase.

Client errors such as `422` do not trigger failover. Upstream transient errors such as `429` or `529` can move to another compatible route under existing routing policy. Missing or malformed answers, out-of-range probabilities/scores, and missing or invalid usage produce a sanitized `502`. Failed attempts with valid usage retain their metering. Upstream authorization failures are Provider errors, not project-key authentication failures.

The catalog snapshot verified on **2026-09-19** uses **USD 0.042 per million input tokens** and **USD 0 output price**. Output tokens are still counted toward usage and token quotas. Final billing uses upstream-reported tokens and configured prices, never estimated tokens or a charge per question. Check alias pricing and Provider cost rates before rollout. Source: [TypeSafe models](https://docs.typesafe.ai/models).

## Rollout and rollback

Start with a dedicated project key and an explicit Jev route. Validate labeled examples for the intended language and task before application decisions depend on the scores. Observe latency, upstream errors, actual usage, and costs. This integration adds no database migration or deployment environment variable. Disable the published model or its routes to stop traffic. Disabling the catalog plugin only removes it from Provider setup; it does not disable existing routes.

Protocol references: [HTTP API](https://docs.typesafe.ai/api), [advanced inputs](https://docs.typesafe.ai/primitives/advanced), [confidence](https://docs.typesafe.ai/confidence), and TokenHub's live `/openapi.json` contract.
