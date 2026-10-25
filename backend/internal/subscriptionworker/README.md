# Bundled subscription adapters

This package embeds TokenHub's statically selected community subscription adapters.
It does not run Magpie, install npm packages at runtime, scan local agent accounts,
or load administrator-supplied JavaScript. `WriteRuntime` extracts the application
assets into a private directory for the Go subscription worker pool. Node 22.23.1
is the tested runtime.

`vendor/PROVENANCE.json` records the upstream repository, commit, package versions,
and SHA256 of each vendored file. The sources are byte-identical to that manifest;
their MIT license is included. Host adaptations belong in this directory rather
than in vendored source.

## Provider scope

The static registry contains 29 account/subscription IDs:

| Package | Provider IDs |
| --- | --- |
| Cursor | `cursor` |
| Kiro | `kiro` |
| Qoder | `qoder`, `qoder-cn` |
| QwenWork | `qwenwork`, `qwenwork-enterprise` |
| GCP AI | `gcp-ai` |
| WorkBuddy | `workbuddy`, `workbuddy-ai` |
| Xiaomi MiMo | `mimo-app` |
| ZCode | `zcode` |
| Factory | `factory` |
| Grok | `grok` |
| Devin | `devin` |
| Command Code | `commandcode-plan` |
| Zed | `zed` |
| Kimi Work | `kimi-work` |
| MiniMax Code | `minimax-code`, `minimax-code-global` |
| Cline | `cline` |
| Trae | `trae-cn`, `trae-global` |
| Open Design | `opendesign` |
| Nous Portal | `nous-portal` |
| Raccoon | `raccoon` |
| AutoClaw | `autoclaw`, `autoclaw-cn` |
| CodeArts | `codearts` |
| JoyCode | `joycode` |

Normal API-key providers such as Cloudflare Workers AI and OpenCode Zen Free do
not use this worker. The six active Magpie built-ins are integrated separately in
TokenHub's Go provider code.

## Private transport

The worker reads newline-delimited JSON commands from stdin and emits
newline-delimited JSON events on stdout. Stdout is reserved for this protocol;
vendor console diagnostics are suppressed. The parent must not log transport
bodies because commands and credential events contain secrets.

Every command has `id`, `operation`, `provider`, and `resource_id`. Operations
requiring credentials accept `auth` as an object. A command ends with either
`{"id":"...","event":"result","result":{...}}` or an `error` event containing
a sanitized `{code,message,status?,sign_in?}` object. Fetch commands instead emit
`response`, zero or more `chunk` events, and `end`.

Supported operations:

| Operation | Additional fields / result |
| --- | --- |
| `providers` | Returns the static `providers` array; no provider is required. |
| `describe` | Returns methods, seed models, and capability flags without local discovery. |
| `models` | Returns `{provider,models,fallback}`. Every model declares its native `protocol`. |
| `checkin` | Runs the supported account check-in hook and returns its outcome. |
| `usage` | Returns the upstream adapter's account allowance windows. |
| `refresh` | Refreshes when supported and returns the current internal `auth`. |
| `import` | Validates and durably saves the explicit `auth` object. |
| `auth.start` | `method_id`, optional `inputs`, optional API `key`; returns a session and browser instructions. |
| `auth.status` | `session_id`; returns pending, complete, or failed state. |
| `auth.callback` | `session_id` and either a pasted callback `url` or authorization `code`. |
| `auth.cancel` | `session_id`; closes callback listeners and cancels the session. |
| `fetch` | `model`, `protocol`, JSON `body`, optional `headers`, `session_id`, and `stream`. |
| `cancel` | `target_id`; aborts a pending command. No terminal event. |

All header maps use `map[string][]string` JSON, including `set-cookie`. A fetch
body is a JSON object or JSON text, never base64. The worker derives the upstream
URL from the model and account loader; the caller cannot supply a URL. Native
protocols are `chat`, `anthropic`, `responses`, and `gemini`.

The `response` event contains `status` and `headers`. Each `chunk` event contains
base64 `data`. Adapters may transform the wire representation, while the parent
remains responsible for translation into TokenHub's public API contract.

## Network relay and persistence

All vendor `fetch` calls emit a `network.request` event with `request_id`,
`resource_id`, `provider`, `url`, `method`, array-valued `headers`, and base64 `body`.
The Go parent performs DNS/IP policy enforcement and the actual network request.
Its HTTP client must disable automatic redirects (`http.ErrUseLastResponse`): the
worker checks each redirect and rejects authenticated cross-origin redirects.

The parent responds using `network.response` (`request_id`, `status`, `headers`),
`network.chunk` (`request_id`, base64 `data`), and `network.end`. An optional base64
`body` on `network.response` is a complete, nonstreaming response. `network.error`
signals a failed transport. `network.cancel` events require the parent to cancel
the matching request. Routing uses `request_id` rather than the outer command ID:
automatic OAuth polling can continue after `auth.start` returns its result.

Each relayed upstream response has a 64 MiB cumulative byte ceiling, including
streams consumed immediately. The separate 32 MiB queued-data limit remains in
effect. Exceeding either bound fails the response and emits `network.cancel`,
which aborts the parent's HTTP request instead of leaving it running.
The Go launcher fixes V8 old-space at 256 MiB per worker; this does not cap RSS
or native buffers. The worker pool permits up to 32 processes.

Credential changes emit `auth.update` with `update_id`, `resource_id`, and `auth`.
The parent must atomically persist the encrypted credential revision before
sending `{"operation":"auth.ack","update_id":"..."}`. An `error` field rejects
the update. Inference cannot use a rotated token before acknowledgement, and a
failed save disables subsequent network calls for that worker account.

The parent serializes commands per resource and obtains each credential snapshot
after acquiring that resource's gate. This prevents queued commands from restoring
an older refresh token. Raw `auth` returned by refresh/status/import is internal;
ordinary management responses must project only safe connection state. The
explicit administrator login/import action may hand credentials to the protected
editor for saving, using its declared secret-result policy and no-store response.
That handoff does not by itself save a durable provider resource.

## Boundaries and verification

The process contains trusted bundled application code, not an untrusted-code
sandbox. Subprocess launch and direct HTTP/HTTPS calls are disabled. Cursor uses
its upstream HTTP/1 RunSSE fallback instead of direct HTTP/2. Local account methods
are omitted; imports containing file, command, or local-session references are
rejected. The parent supplies an isolated home, working directory, and environment.
Only loopback listeners belonging to a pending OAuth session accept pasted
callbacks; the upstream adapter verifies its own path, state, and PKCE values.

Grok uses a direct device OAuth wrapper around the pinned adapter's response
conversion helpers, avoiding its CLI/home dependency. Kimi Work requires an
explicit imported session containing `type: "oauth"`, `access`, `webToken`, and
`uid`, with optional `deviceId`; it never opens the desktop database. Arbitrary
Factory enterprise hostnames are refused. Provider-specific limitations in the
vendored READMEs still apply, including Trae's experimental implementation status.

Model discovery can return seed models during upstream failures. `fallback` marks
known seed fallbacks; a model in the catalog alone does not establish subscription
entitlement. Paid-account calls have not been made by the automated test suite.

From the repository root, run:

```sh
node --test backend/internal/subscriptionworker/*.test.mjs
```

From `backend/`, run `go test ./internal/subscriptionworker`. Tests use synthetic
credentials and parent-network fixtures, and verify provenance, offline startup,
native protocols, credential persistence, cancellation, and resource isolation.

GCP AI uses explicit desktop OAuth or authorized-user ADC JSON, with a selected
project and location. It never invokes gcloud or scans ADC/browser profiles.
QwenWork enterprise credentials are bound to the explicitly supplied exact domain.
Declared boolean options (`x_search`, `standard_only`, `zdr`) travel with each
command; provider defaults can be overridden on the account resource. Quota and
check-in hooks are available through registered administrator actions.
