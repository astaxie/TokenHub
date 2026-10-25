# Subscription provider third-party notices

These notices cover TokenHub's Magpie-derived subscription integration. They
supplement TokenHub's own license and do not change upstream license terms.

## yetone/magpie

Repository: https://github.com/yetone/magpie

Pinned commit: `d580537201938b9f510070a213e09b46479e53ad`

TokenHub adapts protocol and authentication implementations from this MIT-licensed
project into its own provider interfaces, encrypted credential store, HTTP
transport, authorization actions, and process lifecycle. The corresponding source
families are:

| TokenHub source | Upstream reference |
| --- | --- |
| `backend/internal/server/provider_subscription_copilot*.go` | `internal/provider/copilot*.go`: GitHub device authorization, short-lived Copilot credentials, request headers, model discovery, and Chat/Responses routing |
| `backend/internal/server/provider_subscription_chatgpt*.go` | `internal/provider/chatgpt_api.go`: Sign in with ChatGPT, dynamic client registration, direct API tokens, and Responses requests |
| `backend/internal/server/provider_subscription_claude*.go` | `internal/claudecode/claudecode.go` and `internal/provider/claude*.go`: genuine Claude Code process protocol, account credential format, tool bridging, and model aliases |
| `backend/internal/server/provider_subscription_google*.go` | `internal/provider/google.go`, `internal/provider/gemini_api.go`, `internal/provider/antigravity*.go`, and `internal/gateway/codeassist.go`: Google OAuth, Code Assist envelopes and tool schemas, model discovery, and streaming |
| `tools/magpie-provider-presets.mjs` and its generated catalog packages | `internal/provider/presets.go` at `d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1`: separately pinned API endpoint and seed-model inventory |

These are TokenHub adaptations, not byte-identical vendored Go files. The existing
OpenAI Codex integration is retained separately. The native adapters do not run
Magpie or include its graphical application. Claude Code itself is not bundled
or licensed by this notice; its separately installed distribution retains its
own terms.

MIT License

Copyright (c) 2026 yetone

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## magpie-community/plugins

Repository: https://github.com/magpie-community/plugins

Pinned commit: `5c9b7a413756c2b4f139290047085075c83eb3e8`

`backend/internal/subscriptionworker/vendor/` contains byte-identical pinned
community adapter sources and their dependencies within that repository.
`vendor/PROVENANCE.json` records the package versions, source paths, and SHA256
hashes. TokenHub's runtime adaptations are outside the vendor directory.

The selected inventory is Cursor, Kiro, Qoder (global and China), WorkBuddy,
WorkBuddy AI, MiMo, ZCode, Factory, Grok, Devin, Command Code Go, Zed, Kimi Work,
MiniMax Code (China and global), Cline, Trae (China and global), OpenDesign,
Nous Portal, Raccoon, AutoClaw (global and China), CodeArts, JoyCode, QwenWork
(public and enterprise), and GCP AI.

QwenWork shares the pinned Qoder module. `backend/internal/subscriptionworker/gcp-ai.mjs`
is a TokenHub adaptation of the GCP AI Vertex protocol and model discovery; it uses
explicit OAuth/ADC credentials instead of host gcloud accounts or browser profiles.
It is not a byte-identical vendored file.

MIT License

Copyright (c) 2026 magpie-community contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
