# Subscription and account providers

Language: English | [简体中文](zh-CN/subscription-providers.md) | [日本語](ja/subscription-providers.md)

TokenHub integrates the six active built-in account providers and all 29 account-provider IDs from the pinned Magpie community inventory. Administrators connect each account as a provider resource, load its models, and route requests through TokenHub's existing authentication, authorization, and usage accounting. A Magpie service is not required.

The account adapters and API-key presets are separate integration paths. A paid application subscription does not automatically grant an API plan, every listed model, or unlimited usage. This integration does not assert that a provider permits account sharing or automated access; use the account according to its applicable plan terms.

## Runtime requirements

- The community adapters require Node.js **22.23.1 or newer** on the backend host. The supported Docker image and native release bundle include Node; source deployments must provide it on the backend process's `PATH`. `start.sh` checks the version. No npm packages are installed at runtime.
- Claude Code requires the genuine `claude` CLI on the backend process's `PATH`. TokenHub does not currently bundle or pin that CLI. Install it in the backend execution environment, including inside the container when using Docker, before connecting Claude accounts. A missing executable returns `claude_cli_unavailable`.
- Go handles Codex, Copilot, ChatGPT API, Gemini CLI, and Antigravity integration. The other 29 adapters run as application-owned Node workers with bundled, fixed source files. They do not run a Magpie daemon or load arbitrary JavaScript plugins.

See [Deployment](deployment.md) for the supported deployment paths.

## Account-provider matrix

The six native integrations use these TokenHub provider types. “Chat” means `/v1/chat/completions`; “Responses” means `/v1/responses`. Streaming and caller-defined tools follow the listed text protocol and upstream model support.

| Provider | TokenHub provider type | Connection | Public text scope |
| --- | --- | --- | --- |
| Claude Code | `claude_subscription` | Browser OAuth, setup token, or account session | Chat through the genuine Claude Code CLI |
| OpenAI Codex | `openai_codex` | Existing Codex OAuth or explicit account import | Responses and existing compatibility bridges |
| GitHub Copilot | `github-copilot-subscription` | GitHub device code | Chat and Responses |
| ChatGPT API | `chatgpt-subscription` | Sign in with ChatGPT, or explicit token import | Responses using a direct API token; separate from the Codex backend |
| Gemini CLI | `gemini_cli_subscription` | Google browser OAuth with built-in desktop-client defaults and optional custom configuration | Chat through Code Assist |
| Antigravity | `antigravity_subscription` | Google browser OAuth with built-in desktop-client defaults and optional custom configuration | Chat through Code Assist |

All 29 community integrations expose **Chat**, including streaming and caller-defined tools where supported by the upstream model. Their internal Chat, Anthropic, Responses, or Gemini wire format does not add public Responses, embeddings, or media support. Every row also supports explicit session JSON import; this is the only connection path for Kimi Work.

| Provider | Inventory ID | Available connection methods |
| --- | --- | --- |
| Cursor | `cursor` | Browser; API key |
| Kiro | `kiro` | Browser with Google, GitHub, AWS Builder ID, or IAM Identity Center; `ksk_` API key |
| Qoder | `qoder` | Browser/device flow; personal access token |
| Qoder China | `qoder-cn` | Browser/device flow; personal access token; official Qoder enterprise VPC |
| QwenWork | `qwenwork` | Browser/device flow |
| QwenWork Enterprise | `qwenwork-enterprise` | Browser/device flow with the explicit enterprise domain |
| GCP AI | `gcp-ai` | Google desktop OAuth; explicit authorized-user ADC JSON import with project and location |
| WorkBuddy | `workbuddy` | Browser |
| WorkBuddy AI | `workbuddy-ai` | Browser |
| Xiaomi MiMo | `mimo-app` | Xiaomi account browser/poll flow; seed models only |
| ZCode | `zcode` | Z.ai, BigModel, or team browser flow; GLM Coding Plan API key with site selection |
| Factory | `factory` | Device code; `fk-` API key |
| Grok | `grok` | Device code |
| Devin | `devin` | Browser |
| Command Code Go | `commandcode-plan` | Browser; API key |
| Zed | `zed` | Browser with loopback callback |
| Kimi Work | `kimi-work` | Explicit session JSON import only |
| MiniMax Coding Plan | `minimax-code` | Account device/browser flow |
| MiniMax Coding Plan Global | `minimax-code-global` | Account device/browser flow |
| Cline | `cline` | Browser/device flow; API key |
| Trae China | `trae-cn` | Browser; experimental upstream adapter |
| Trae Global | `trae-global` | Browser; experimental upstream adapter |
| OpenDesign | `opendesign` | Account device flow; API key |
| Nous Portal | `nous-portal` | Device code |
| Raccoon | `raccoon` | Browser; manual callback/token flow |
| AutoClaw | `autoclaw` | Google or z.ai via a local loopback landing page |
| AutoClaw China | `autoclaw-cn` | Phone number and SMS code |
| CodeArts | `codearts` | Huawei Cloud browser flow |
| JoyCode | `joycode` | JD browser flow; `ptKey` with optional enterprise tenant |

For community entries, replace `-` with `_` and append `_subscription` to obtain the provider type, or `_account` for the resource type. For example, `kimi-work` maps to `kimi_work_subscription` and `kimi_work_account`. The platform plugin ID is `tokenhub.provider.<inventory-id>-subscription`.

## Connect an account

1. Open the administrator provider editor and select the relevant account provider.
2. Add an account resource, choose an available connection method, and follow the provider's browser, device-code, or token instructions. An explicit JSON import is available for existing sessions. TokenHub does not search the host's CLI credentials or desktop account databases.
3. Complete the authorization or import, then **save the resource**. The editor temporarily receives the new credentials so it can populate the resource; completing authorization alone does not guarantee durable resource storage.
4. Load the account's models, verify the desired model with the resource probe, and configure routing and prices before enabling application traffic. A probe can consume the account's allowance.

The model list supports **Select all** and **Deselect all** when adding or editing a provider, including API-key connections. With a search query, these actions apply only to matching importable models and preserve selections outside the results. Already imported models remain selected and cannot be deselected here. Save the provider to import the selected models.

After importing inventory, use **Model Directory > Import from Provider** to create a separate same-name external model and initial route for each selected model. Review client prices and confirm any unknown upstream costs before publishing. See [Provider inventory, model directory, and publication](administrator-guide.md#provider-inventory-model-directory-and-publication) for bulk selection, subscription cost confirmation, and retry behavior.

WorkBuddy and WorkBuddy AI browser sign-in completes by polling automatically. Finish signing in on the provider page and return to TokenHub; no authorization code or token needs to be pasted. After the credentials appear, save the account resource. Authorization accepts numeric or string status codes. WorkBuddy’s account `domain` is preserved as bounded `X-Domain` header metadata, while API destinations remain restricted to the fixed official hosts. If the flow fails, diagnostics identify the stage and available HTTP/provider status codes without exposing account credentials or upstream response text.

WorkBuddy Chat endpoints require streaming requests. TokenHub uses that upstream format for provider tests and non-streaming Chat API calls as well, then assembles a complete JSON response with text, reasoning, tool calls, finish reasons, and reported usage. Existing streaming calls continue to stream; incomplete responses are rejected instead of being reported as successful tests.

Credentials belong in the resource's protected credential fields, including encrypted session JSON, not in provider Options, ordinary headers, logs, or source control. Pending authorization sessions are bound to the initiating administrator and provider, use random state, expire, and are consumed once. Most flows expire after ten minutes; the Claude setup-token handoff expires after five minutes. Cancel unused flows from the editor. If cancellation or a timeout stops the worker during an authorization exchange, TokenHub releases that unusable session immediately.

When replacing an existing account or switching between a session JSON and a token through `PATCH /api/admin/provider-resources/{id}`, set `credentials.replace` to `true` and include a new `access_token` or complete `session_json`. This replaces the credential snapshot and clears omitted old tokens, OAuth client settings, and account metadata. Include any required new client configuration and account metadata in `credentials`; ordinary credential patches without this flag continue to merge only the supplied credential fields. Replacement with no new token or session is rejected.

During explicit credential replacement, omitted connection overrides, health state, and rate/concurrency settings retain their current values. Explicit empty values or zero limits still use the existing reset/default rules. The previous account’s cached quota is invalidated when the replacement is saved.

For existing subscription accounts, open the provider editor, select one account from the account list, and use **Reauthorize account** on the **Connection** tab. Complete sign-in or import, then choose **Save new account credentials**. This replaces only that account's credential snapshot and preserves its scheduling settings. Switching the selected account cancels pending authorization and discards unsaved credentials.

Grok device authorization now requests the `api:access` scope needed for inference. Accounts saved or imported before this change may lack that permission even when model discovery succeeds. If `grok_api_scope_missing` appears, edit the saved Grok account, choose **Reauthorize account**, complete device authorization, and **Save new account credentials**; deleting and recreating the account is unnecessary. Refreshing the old grant cannot add consent for the missing scope. Test the account after saving: model and inference access still depend on the upstream plan and account eligibility.

Native Copilot and ChatGPT HTTP errors preserve the upstream status and valid `Retry-After` delay for routing; credential and model errors use the normal account and model failover behavior.

ChatGPT API fills credentials automatically when the browser interface and backend API use local HTTP loopback addresses and the backend's configured public address supports this flow. Click **Open authorization**, finish signing in, return to TokenHub, wait for the credentials to appear, and save the account. This local flow returns to the running TokenHub backend; no callback URL needs to be copied and no separate listener at `localhost:1455` is required.

If ChatGPT API reports that client registration is complete and another sign-in is needed, click **Open authorization** again in the same editor. TokenHub retains the issued client identity and starts a new authorization with fresh state, nonce, and PKCE values; it never resends the rejected code. Keep this attempt separate from adding another account. The gateway persists its installation identity across restarts, and registration retries remain bound to the administrator who started them. See OpenAI's [registration and sign-in protocol](https://developers.openai.com/siwc/token-sharing-open-source/sign-in).

This update adds schema v10 for encrypted host and client-registration records, while retaining existing saved accounts. Startup applies the additive migration. Managed rollback requires a release compatible with database state v10; otherwise restore the pre-upgrade database backup as part of rollback.

Remote deployments and other configurations retain the manual callback flow. Promptly paste the latest complete callback URL, including `client_id`; do not reuse an old code. Without a listener at `localhost:1455`, a browser connection-refused page there is expected and does not by itself mean authorization failed. Rejected authorization-code exchanges are reported separately from expired saved credentials. A failed manual exchange clears the one-time session and callback and enables **Open authorization** again; start a fresh authorization to retry.

Copilot and community Chat requests routed to Responses models retain `temperature`, `top_p`, `user`, and `service_tier`, including explicit zero values, subject to the selected model's support. For Factory, an explicit `service_tier: "priority"` can select the model's Fast variant. Gemini CLI and Antigravity retain reported usage from terminal error events and classify upstream authorization, quota, and transient failures for routing.

Gemini CLI and Antigravity keep proxy authentication failures and connection timeouts classified as egress failures, so they do not penalize a valid account. GCP token refresh and model discovery keep temporary upstream failures and rate limits distinct from rejected credentials. ChatGPT, Gemini CLI, and Antigravity token refresh also preserve transient upstream status and valid retry delays, including account-bound rate limits.

For Gemini CLI and Antigravity, select **Google account (default)** and click the blue **Open authorization** button. No client ID or client secret is required: each provider uses its own public installed-app OAuth configuration. These application parameters do not authorize an account; the user still signs in to Google and grants access. Select **Custom Google OAuth (advanced)** to use a complete custom client ID/secret pair. Partial overrides are rejected. The selected pair is bound to the encrypted authorization session and saved account for token refresh; existing accounts retain their own client configuration.

If a Google account needs an explicit Cloud project, select **Google account with Cloud project (advanced)** and enter only the project ID; it uses the same built-in client configuration. The custom-client method also accepts a project ID. The selected project is saved with the new credentials and is not silently inherited from an old account during reauthorization. Complete Google sign-in, paste the callback URL into TokenHub, and save the account after credentials are filled. Account eligibility and model access are still determined by the upstream service.

If Google returns `UNSUPPORTED_LOCATION`, TokenHub reports `google_subscription_location_unsupported`. Google sign-in and model discovery can succeed while inference is blocked by regional availability. Check the account's regional eligibility and the actual outbound region of the TokenHub server, including any configured proxy. Filling in OAuth client parameters or repeatedly authorizing the account does not resolve this regional restriction.

Antigravity tool parameters are projected to its supported OpenAPI schema subset. Local references and nullable fields are supported; recursive, external, unresolved, or excessively expanded schemas are rejected with HTTP 400 before inference. Gemini CLI retains its existing JSON Schema handling.

For Kimi Work, import an object with `type: "oauth"`, `access`, `webToken`, and `uid`, with optional `deviceId`. Obtain those values explicitly from your own authorized account; TokenHub never reads the desktop database. Other session schemas remain provider-specific and must match the selected provider.

For Claude Code, start browser login in the editor using the genuine `claude auth login --claudeai` flow. Complete its loopback callback or paste the authorization code. TokenHub uses a private configuration directory and reads only that login's credential file or scoped macOS keychain entry. You can also import a long-lived token obtained with `claude setup-token`, or a session JSON object containing `claudeAiOauth.accessToken` and, where available, refresh-token metadata. The `sonnet`, `opus`, `haiku`, and `fable` catalog entries are CLI aliases checked at request time, not a live entitlement list.

### Remote browsers and callbacks

Device-code flows can be completed from a browser on another machine. For manual callback flows, paste the complete callback URL into the editor when prompted. Community flows that use a loopback GET listener relay the callback only to the exact listener owned by the pending authorization session; the upstream adapter checks the callback path, state, and PKCE.

Some upstream flows POST credentials or depend on local landing-page assets, so copying a URL is insufficient. AutoClaw's global Google/z.ai flow opens an HTTP loopback landing page on the backend host. Use a browser on that host, or a trusted tunnel that reaches that process's loopback listener. A container has its own loopback namespace; a normal reverse proxy to TokenHub does not expose these temporary listeners. Command Code also offers API-key login when its browser flow is unsuitable for a remote deployment.

In a multi-instance deployment, keep an authorization flow's start, polling, exchange, and cancellation on the same TokenHub backend instance. Community authorization workers and callback listeners retain process-local state. Saved credentials are durable and shareable through the database, but pending authorization and active conversation processes cannot migrate between instances. Configure session affinity for these operations; a shared PostgreSQL database alone does not provide it.

## Account options and allowance

Copilot supports `auto`, including plans whose regular model list is empty. The upstream chooser selects the model and protocol before inference; explicit Responses requests require a Responses-capable selection. For enterprise managed users, enter the approved `github_host` (`<tenant>.ghe.com`) during device authorization. The host is encrypted with the account and determines its authorization, session, and API destinations.

Copilot model protocol and Auto caches are bound to the account credentials. Replacing credentials loads the new account’s routes; a discovery response still in flight for the previous account cannot overwrite them. Each inference request keeps the account used for its model selection.

Gemini CLI reads account experiment flags and maps advertised model IDs to the account's wire IDs before inference. Providing a project does not bypass Code Assist eligibility/onboarding. Antigravity tool schemas also supply empty array-item schemas where required by the upstream API.

For GCP AI, supply a Google desktop OAuth client ID/secret, a project ID or number, and a Vertex location (default `global`). Alternatively paste an `authorized_user` ADC JSON object containing `client_id`, `client_secret`, and `refresh_token` through its import method. TokenHub verifies project access, stores refreshable credentials, and discovers Gemini text models using Vertex. Service-account JSON, file paths, host gcloud profiles, and browser sessions are not imported. Enable Vertex AI and grant the user access to the chosen project. GCP Cloud Console promotional credit scraping is not included, so no fabricated remaining-credit value is shown.

Cursor team accounts can include an explicit positive numeric `teamId` in their imported session JSON. Team headers and usage queries remain bound to that resource; TokenHub does not read the host's selected Cursor team.

QwenWork Enterprise asks for the enterprise domain during authorization. TokenHub binds credentials and subsequent refresh/inference to that exact domain; the shared Go transport still applies DNS/IP restrictions. Each enterprise account retains its own domain across rotations.

The Provider editor’s advanced settings expose Grok X search (`x_search`), Factory Standard-only (`standard_only`), and Command Code zero data retention (`zdr`) as boolean options. Resource options override provider defaults. Factory Standard-only refuses inference with a retryable quota error when its Standard allowance is exhausted, enabling normal route fallback. Missing or malformed Standard usage also blocks inference until the allowance can be verified, even if extra usage credit is available. Provider support and plan eligibility still govern the effect of these options.

Quota reads are available for community accounts except Nous Portal and GCP AI. Where the adapter supports it, a manual check-in action reports `claimed`, `done`, `inactive`, or `ineligible`; it does not schedule automatic claims. Qoder international may require native device identity that TokenHub does not collect. Model-specific and display-only allowance windows remain separate from account-wide exhaustion. Quota percentages stay unknown when the upstream reports only a balance or incomplete windows; monitoring shows `-`, while an explicit exhausted allowance remains `0%`, including after a manual refresh.

Saved community OAuth accounts with a refresh token and a supported refresh hook expose **Renew Token** in the account resource list. API-key accounts and adapters without token refresh do not expose this operation.

An expired subscription reported by quota or check-in returns HTTP 409 with `provider_resource_reauthorization_required`, keeping the administrator signed in. Reauthorize that resource. Factory Standard-only also treats this state as a broken account credential and permits normal fallback to another eligible route.

The coverage inventory includes all 35 account-provider IDs at the pinned revisions. Cloudflare Workers AI and OpenCode Zen Free are API/public services, and Kimi Desktop is an agent connector; they are not additional subscription-account providers. Account coverage does not imply full Magpie feature parity: host account scanning, agent switching, browser credit scraping, and new media-generation APIs are outside this integration.


## Additional API-key presets

The catalog adds the following 12 presets to the existing API-key providers. They use `openai_compatible` resources and their own plan/API keys, rather than the account-worker login above.

| Catalog ID | Plan or API | Initial model source |
| --- | --- | --- |
| `baidu-qianfan-personal` | Baidu Qianfan Token Plan Personal | Pinned seed IDs |
| `baidu-qianfan-team` | Baidu Qianfan Token Plan Enterprise | Pinned seed IDs |
| `huawei-maas-token-plan` | Huawei Cloud MaaS Token Plan | Pinned seed IDs |
| `volcengine-coding-plan` | Volcengine Ark Coding Plan | Pinned seed IDs |
| `volcengine-agent-plan` | Volcengine Ark Agent Plan | Pinned seed IDs |
| `qwen-token-plan` | Qwen AI Token Plan | Pinned seed IDs |
| `kimi-code-global` | Kimi Code Global | Account model discovery |
| `tencent-token-plan-enterprise-intl` | Tencent Cloud Token Plan Enterprise Global | Pinned seed IDs |
| `clinepass` | ClinePass | Pinned seed IDs |
| `ollama-cloud` | Ollama Cloud | Account model discovery |
| `commandcode` | Command Code API | Account model discovery |
| `nous` | Nous Portal API | Account model discovery |

The four account-discovery presets begin without candidate models. The existing StepFun Global Step Plan preset remains `stepfun-ai`. Existing `ollama` catalog models are not treated as hosted Ollama Cloud entitlements. See [Model Catalog Updates](model-catalog-updates.md) for refresh behavior and provenance.

## Credentials, isolation, and limits

Saved account credentials use TokenHub's encrypted resource storage. Credential loads verify the selected adapter type before returning secrets; moving an account to another provider type makes stale requests fail with HTTP 409 instead of using the new provider's credentials. Community workers are isolated by account and authenticated caller, with private home directories, working directories, and environments. Account gates and cluster leases serialize operations that can rotate tokens. Once the parent receives a credential rotation, cancellation or a request timeout still allows a separate save bounded to ten seconds. Account gates and cluster leases remain held, with lease renewal active, until that save and worker cleanup finish. Credential updates use compare-and-swap persistence; the worker must receive a successful durable-save acknowledgement before using rotated credentials. A concurrent administrator edit or persistence failure fails the operation instead of overwriting newer credentials. Session rotations also reject accounts replaced or recreated under the same resource ID while persistence is in progress. Model and quota results are bound to the resource identity as well as its replacement generation; deletion/recreation or movement to another provider invalidates in-flight results, including unchanged-model responses. A successful save acknowledgement that arrives after the worker wait timeout restores only its matching saved revision for the next request; failed or unrelated acknowledgements never authorize new credentials.

Native token refresh, including background renewal, also validates the original account identity and replacement generation before returning or saving its result. Moving, replacing, or recreating an account invalidates an in-flight refresh even when the token values are unchanged; an old failure cannot mark the new account for reauthorization. The final credential/state write also checks the current provider type atomically, including native renewal, plugin refresh, and session rotation.

The bundled worker is trusted application code, not an arbitrary-code sandbox. Direct HTTP/HTTPS and subprocess creation are disabled; supported upstream fetches pass through the Go transport's destination and network protections. Provider-specific domain restrictions apply, including refusal of arbitrary Factory enterprise hostnames. Cursor uses the upstream HTTP/1 RunSSE fallback. Valid upstream retry delays remain available to routing after error responses are sanitized; private error headers and credential values are not forwarded.

When a bundled adapter reports that the saved account credentials should be retained, its HTTP 401/403 response is treated as a temporary failure instead of broken credentials. An explicit expired sign-in still requires reauthorization. Only trusted adapter metadata can make this distinction; upstream headers cannot override it.

Community worker requests preserve proxy configuration, connection, authentication, and timeout failures as egress errors, including when a bundled adapter catches the network exception and returns an HTTP error. These shared transport failures do not penalize an account or trigger account failover; private transport diagnostics remain redacted.

Each relayed upstream response is capped at 64 MiB in total, including streaming responses, with a separate 32 MiB queued-data bound. Exceeding a limit cancels the upstream request. The pool holds at most 32 Node workers, each launched with a 256 MiB V8 old-space limit. This is not a process RSS limit; provision memory for the aggregate workers, native buffers, and the rest of TokenHub.

Separately, the subscription Responses stream collector limits cumulative SSE data to 32 MiB, including many individually small events. Exceeding this assembly limit stops the response while preserving usage already reported by the provider.

Claude Code runs separately with an isolated home/configuration, disabled host file/shell tools, settings sources, hooks, skills, and browser integration. Only tools declared in the caller's request are exposed through a private local MCP connection. The genuine CLI performs its own upstream network requests outside the Go worker relay; apply host outbound policy for this process. Its executable is application-controlled, not a provider option. This isolation is not an OS sandbox.

Claude allows one active request or parked tool call per account, with at most 16 runs per backend process. Tool results must return through the same authenticated tenant/API credential and backend process. Chat and Messages continuations stay on the original account and upstream model even when weighted routing would select another account; they fail if that route is no longer eligible. Parked tool calls expire after five minutes, and a run lasts at most 30 minutes. Restarting or switching backend instances loses parked continuation state.

When caller-defined tools are enabled, Claude rejects `parallel_tool_calls: false` as an unsupported capability because the CLI cannot enforce serial tool calls. Requests without active tools, including `tool_choice: "none"`, remain supported. Claude Code preserves reasoning text, reported cache usage, and upstream completion reasons, including token/context limits and refusals, in both buffered and streaming Chat responses.

Claude also rejects inference controls that the CLI cannot apply, including sampling penalties, `min_p`, `top_k`, multiple completions, seed/logit-bias/log-probability settings, and API-only output or tool controls. Zero penalties, `min_p: 0`, `top_k: 0`, supported defaults such as `n: 1` and `logprobs: false`, null controls, and caller metadata remain accepted. Tool continuations must retain the original effective token limit and `reasoning_effort`; a rejected settings change does not consume the pending continuation.

A renewable account lease covers Claude credential reload, execution, parked tool rounds, credential persistence, and process cleanup across backend instances. Cleanup still attempts to save rotated credentials after cancellation or timeout, with lease renewal active until the bounded credential save and process cleanup finish. Waiting for a lease remains cancellable and retains the request timeout. If a transient storage failure persists after three attempts, TokenHub retains the private temporary recovery directory and logs its path, without logging credential values. Recovery is manual: preserve that directory until the account credentials are durably restored. Concurrent administrator edits are never overwritten by a stale session.

Model discovery may fall back to pinned seed IDs. Catalog metadata distinguishes `subscription-account-live`, `subscription-account-fallback`, and `subscription-account-empty`; fallback entitlement and pricing remain unverified. Raw upstream variants are retained as metadata. Explicit reasoning efforts and named, complete thinking controls become selectable options. Messages models preserve their declared effort, adaptive or disabled thinking, and token budgets in native requests; unsupported efforts return HTTP 400. A listed model does not establish account access, price, context size, or remaining quota. Supported worker allowance hooks are exposed through `quota.read`; check-in hooks use `checkin.run`. Model-specific allowance windows do not exhaust the entire account.

Tests cover synthetic HTTP flows, protocol translation, streams, tool calls, encrypted credential rotation, cancellation, process isolation, and callback restrictions. **Real paid accounts, current plan allowances, and every upstream login flow have not been verified live.** Trae remains experimental, and upstream private interfaces can change independently of TokenHub. Existing Codex image support is unchanged; these additions do not promise new embeddings, image, audio, or video capabilities.

## Pinned sources and licenses

- [yetone/magpie](https://github.com/yetone/magpie), commit `d580537201938b9f510070a213e09b46479e53ad`, supplies the reviewed built-in inventory, native protocol references. Its MIT license is retained in `tools/licenses/magpie-LICENSE`. `backend/internal/server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md` identifies native Copilot, ChatGPT, Claude, Gemini CLI, and Antigravity derivations; their platform packages and generated API preset packages include third-party notices.
- [magpie-community/plugins](https://github.com/magpie-community/plugins), commit `5c9b7a413756c2b4f139290047085075c83eb3e8`, supplies the statically bundled community adapters under MIT. `backend/internal/subscriptionworker/vendor/PROVENANCE.json` records package versions and SHA256 hashes; the upstream license is retained beside it.

Vendored sources stay pinned and byte-identical to the provenance manifest. TokenHub adaptations live outside the vendor tree; no runtime update or package installation occurs.

API preset facts remain separately pinned to `d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1`.

The reproducible account inventory and explicit exclusions are recorded in [`tools/magpie-subscription-coverage.json`](../tools/magpie-subscription-coverage.json) and checked against the bundled registry and plugin packages.
