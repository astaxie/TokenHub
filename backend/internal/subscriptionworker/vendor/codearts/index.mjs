// CodeArts (华为云码道 代码智能体) as a magpie / OpenCode provider plugin.
//
// CodeArts, Huawei Cloud's coding agent (码道), serves its models over an
// OpenAI-compatible Chat Completions API at
// https://snap-access.cn-north-4.myhuaweicloud.com/api/v2 — but unlike a plain
// OpenAI-compatible provider it does NOT take an API key. Requests are signed
// with临时 AK/SK/security-token credentials using Huawei's SDK-HMAC-SHA256
// scheme, and those credentials are obtained by signing in to Huawei Cloud
// with OAuth.
//
// This plugin implements exactly that flow, mirroring the real CodeArts agent
// (and the `Ebony-Vinyl/dsh-our-free-model` CodeArts channel, which this plugin
// is modelled on):
//
//   1. `auth.methods[0]` is `{ type: "oauth" }`. `authorize()` starts a
//      loopback callback server on 127.0.0.1 (port >= 10000), returns the
//      Huawei Cloud portal URL for magpie to open, and completes with the
//      credentials once the browser is redirected back with an auth code.
//   2. The sign-in is PKCE: a random code_verifier is sent to the STS token
//      endpoint together with the code and the S256 challenge's pre-image. It
//      is also a DPoP flow: a fresh ES256 (P-256) key pair signs each token
//      request, and the private key JWK is persisted so refresh requests can
//      sign with it too.
//   3. STS answers with rotating AK/SK/security-token credentials plus a
//      refresh_token. The whole credential (including the verifier and DPoP
//      key) is stored as JSON in the account's `access` field.
//   4. `auth.refresh` silently renews before expiry via `grant_type =
//      refresh_token`; the same verifier and DPoP key are reused.
//   5. Every model request is signed with SDK-HMAC-SHA256 in the loader's
//      `fetch`, and the account's live model list + quota are read from
//      signed GETs.
//   6. The gateway caps an account at 3 concurrent sessions (`TM.00001041`).
//      Each account keeps one stable session id (`x-ot-session-id`), at most
//      3 of its chats are in flight, and a cap answer is retried until a
//      session frees, then handed to magpie as a 429 so it can fail over.
//   7. Chats always go upstream as SSE streams: CodeArts meters only streamed
//      answers, so a caller that asked for JSON gets the stream folded back.
//   8. `auth.checkin` presses the daily 「每日领取」 check-in the way the
//      CodeArts IDE does (`/v1/ops/delivery` → `/claim` → `/confirm`).
//
// It imitates @magpie-community/opencode-workbuddy-auth for structure but
// replaces its browser sign-in + bearer-token requests with CodeArts' OAuth +
// signed-request flow. See README.md.

import { createServer } from "node:http"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

const PROVIDER = "codearts"
const NPM = "@ai-sdk/openai-compatible" // chat completions

// ---- endpoints & flow constants --------------------------------------------

// Chat Completions base (loader `baseURL`). A chat goes to
// `${CHAT_API_BASE}/chat/completions`.
const CHAT_API_BASE = "https://snap-access.cn-north-4.myhuaweicloud.com/api/v2"
// Snap-access manager host: model list, account/quota, check-in.
const SNAP_HOST = "https://snap-access.cn-north-4.myhuaweicloud.com"
const SNAP_MODEL_BUILTIN_URL = `${SNAP_HOST}/v1/model/builtin`
const SNAP_STATISTICS_URL = `${SNAP_HOST}/snap-manager/v1/statistics/plugin`
// opengw gateway config: the benefit/free-quota model set.
const OPENGW_HOST = "https://opengw.developer.huaweicloud.com"
const OPENGW_GATEWAY_CONFIG_URL = `${OPENGW_HOST}/api/v1/gateway/config`
// The only hosts the account's credentials are signed for and sent to: the
// two the plugin itself calls, by name. STS takes the refresh token, never a
// signature, and the portal only ever opens in the browser.
const SIGNED_HOSTS = new Set([SNAP_HOST, OPENGW_HOST].map((u) => new URL(u).host))
// The daily free token pool a `maas_type: benefit` chat draws on (never the
// credit packages), so a free model's spend shows up in the usage panel.
const BENEFIT_BALANCE_URL = `${OPENGW_HOST}/api/v1/user/tokens/balance`
// The free models are the account's only once today's benefit is claimed: the
// CodeArts agent POSTs this before it lists them (`claimBenefit`), and leaves
// them out when it fails (4004 no benefit / expired, 4006 today's places gone).
const BENEFIT_CLAIM_URL = `${OPENGW_HOST}/api/v1/benefit/claim`
const BENEFIT_OK = "0000"
// The agent's own model list, with each model's credit rate (`credit[].
// ratio_display`, shown as "1.50x") and its parameters: the CodeAgent's detail.
const AGENT_LIST_URL = `${SNAP_HOST}/v1/agent-center/agents/useragents?offset=0&limit=100`
const AGENT_DETAIL_URL = (id) => `${SNAP_HOST}/v1/agent-center/agents/detail?agent_id=${encodeURIComponent(id)}`
const CODE_AGENT_ID = "CodeAgent"
// The agent the IDE reads its model list from (its CODE_AGENT_AGENT_ID): the
// one whose `original_id` is "build", 「自主规划与执行」; older lists call it
// CodeAgent. Its id, as the IDE asks for it, stands in when the list can't be read.
const MAIN_AGENT_ORIGINAL_IDS = ["build", CODE_AGENT_ID]
const MAIN_AGENT_FALLBACK_ID = "a8bcb36232554267a5142361cc25a393"
// Agents tried for a model list before giving up.
const AGENT_DETAIL_TRIES = 3
const AGENT_CENTER_HEADERS = { "Content-Type": "application/json", "Agent-Type": "AgentCenter", "X-Language": "zh-cn" }
// Check-in credits are good for 30 days from when they are given.
const CHECKIN_CREDIT_DAYS = 30
// Every credit batch with its own expiry, as the console's 「获取明细」 reads
// them (its /portal/snap-manager and /portal/promptcenter calls are these):
// the plan's and bought packages, then the campaigns' grants (sign-up, check-in).
const PACKAGE_CREDIT_PAGE_URL = `${SNAP_HOST}/snap-manager/v1/package/credit/page`
const PACKAGE_CREDIT_QUERIES = [
  { pageNum: 1, pageSize: 100, sortField: "created_time", sortOrder: "desc", packageTypes: ["basic", "ondemand"] },
  { pageNum: 1, pageSize: 100, packageTypes: ["bonus"] },
]
const OPS_CREDIT_OVERVIEW_URL = `${SNAP_HOST}/v1/ops/credit/overview`
const OPS_CREDIT_CAMPAIGN_URL = (id) => `${SNAP_HOST}/v1/ops/credit/campaign/${encodeURIComponent(id)}?pageNum=1&pageSize=100`
// The batches change slowly; read them again this often (and after a check-in).
const CREDIT_BATCHES_TTL_MS = 10 * 60 * 1000
// Daily check-in (「限时福利 → 每日领取」), as the IDE's ActivityWelfarePane calls it.
const OPS_DELIVERY_URL = `${SNAP_HOST}/v1/ops/delivery?channel=IDE`
const OPS_CLAIM_URL = `${SNAP_HOST}/v1/ops/claim`
const OPS_CONFIRM_URL = `${SNAP_HOST}/v1/ops/confirm`
const OPS_CHANNEL = "IDE"
const DAILY_CHECKIN_TYPE = "USER_LOGIN"
// States the IDE disables the claim button in: today's benefit is in already.
const CLAIMED_STATUSES = ["CLAIMED", "CONFIRMED", "CONSUMED"]
// Unsigned headers snap-access routes on (signing them breaks the signature).
const SNAP_EXTRA_HEADERS = { "Agent-Type": "PromptCenter", "X-Language": "zh-cn" }
// OAuth portal (new IAM flow) and the STS token endpoint.
const PORTAL_AUTHORIZE_BASE = "https://codearts.huaweicloud.com/portal/authorize"
const PORTAL_LOGIN_BASE = "https://codearts.huaweicloud.com/portal/login"
const STS_TOKEN_ENDPOINT = "https://sts.cn-north-4.myhuaweicloud.com/v1/oauth2/tokens"

// OAuth client id (equals the plugin's URI scheme) and local callback path.
const CLIENT_ID = "codearts-agent"
const REDIRECT_PATH = "/oauth/callback"
// What the portal expects the calling plugin to be called (reverse-engineered
// constants — the plugin/version match the real IDE extension, not this package).
const LOGIN_PLUGIN_NAME = "snap_AIIDE"
const LOGIN_PLUGIN_VERSION = "5.2.0"
const OAUTH_THEME = "2" // activeColorTheme.kind: 2 = Dark
const OAUTH_LOCALE = "zh-cn"

const GRANT_AUTHORIZATION_CODE = "authorization_code"
const GRANT_REFRESH_TOKEN = "refresh_token"

const TOKEN_TIMEOUT_MS = 60_000
const REQUEST_TIMEOUT_MS = 30_000
const OAUTH_CALLBACK_TIMEOUT_MS = 180_000
const MIN_CALLBACK_PORT = 10_000

// magpie renews this long before the credential expires.
const REFRESH_LEAD_MS = 30 * 60 * 1000
// The loader renews in-band only this close to expiry, well after magpie's own
// renewal, so the two never spend the one-time refresh token twice.
const INBAND_REFRESH_LEAD_MS = 2 * 60 * 1000

// The gateway allows 3 concurrent sessions per account; the 4th is answered
// `TM.00001041` (并发会话数已达上限(3个)) — as an HTTP 400, or as the first event
// of a 200 SSE stream — and a finished streaming session holds its slot for
// about a minute. Our own in-flight chats are capped first, and a cap answer is
// retried (re-signed each time) within a budget, then handed back as a 429.
const SESSION_LIMIT_ERROR = "TM.00001041"
// The gateway tells sessions apart by `x-ot-session-id` (the CodeArts agent
// sends its conversation id there; `Chat-Id`/`Session-Id` count for nothing):
// a request without it is a session of its own, held for about a minute, so
// three requests in a minute used to fill the account.
const SESSION_HEADER = "x-ot-session-id"
// A gateway that queues the request answers 429 with `queue-status: queued`
// and a `trace-id`; the agent polls this until the trace is `promoted`, then
// sends the request again, and DELETEs it when it gives up.
const TRACE_QUEUE_URL = (traceId) => `${SNAP_HOST}/api/v1/concurrency/trace-queues/${encodeURIComponent(traceId)}`
const QUEUE_BUDGET_MS = 300_000
// Every chat of an account goes out under its one session id, so this gate is
// what keeps the account within the vendor's 3 sessions: an option may only
// lower it.
const ACCOUNT_MAX_INFLIGHT = 3
const SESSION_RETRY_DELAYS_MS = [2_000, 4_000, 6_000, 8_000, 10_000] // the last repeats
const SESSION_RETRY_BUDGET_MS = 150_000
// A body nobody drains must not hold an in-flight permit forever.
const PERMIT_MAX_HOLD_MS = 15 * 60 * 1000
// How much of a 200 body to read while looking for an embedded error.
const SSE_PEEK_LIMIT = 16 * 1024
// The gateway rejects a larger output budget (RANGE_VALIDATOR, 0..65536).
const MAX_OUTPUT_TOKENS = 65_536
// Left to its default the gateway streams the reasoning inside `content`, so a
// caller shows the thinking as the answer; asked for explicitly it goes to
// `reasoning_content` and `content` is the reply only.
const ENABLE_THINKING = { type: "enabled" }

// Benefits (free-quota) models require `maas_type: benefit` as a *signed*
// header, or the backend answers InferHub.002002009.404 "model is not
// registered". The authoritative list comes from opengw gateway/config at
// runtime; these are the known defaults so a cold start still works.
//
// The dated ids are the registered free models (measured: with the benefit
// header `deepseek-v4-flash-0731` answers 200 against the free pool, while the
// bare `deepseek-v4-flash` is a different, paid model that refuses the header
// with `InferHub.4005.200`), so they are sent exactly as published.
const BENEFIT_MODEL_FALLBACK = [
  "glm-5.3-flash",
  "deepseek-v4.1-flash",
  "deepseek-v4-flash-0731",
  "deepseek-v4-pro-0813",
]
// Which models draw on the free pool is each account's own answer, kept on the
// account's state (`state.benefit`, with its builtin ids in `state.regular`):
// the fallback stands only until that account's gateway/config has been read.
// An account without the free campaign lists `deepseek-v4.1-flash` as a plain
// builtin model, and the benefit header then gets `InferHub.4004.200 benefit
// not found`. Nothing about models is shared between accounts.

// Context windows (in tokens), keyed by the exact chat model id. Models
// without an entry leave the window to the backend's default.
const CONTEXT_WINDOWS = {
  "GLM-5.2": 202752,
  "glm-5.3-flash": 1048576,
  "deepseek-v4-flash": 1048576,
  "deepseek-v4-pro": 1048576,
  "deepseek-v4.1-flash": 1000000,
}

// The models offered before (or instead of) the account's own live list. `GLM-5`
// is gone: the endpoint answers `InferHub.002002009.404` for it.
const CATALOG = [
  { id: "GLM-5.2", name: "GLM-5.2" },
  { id: "GLM-5.1", name: "GLM-5.1" },
  { id: "glm-5.3-flash", name: "GLM-5.3 Flash" },
  { id: "openpangu-2.0-flash", name: "openPangu 2.0 Flash" },
  { id: "openpangu-2.0-pro", name: "openPangu 2.0 Pro" },
  { id: "deepseek-v4-flash-0731", name: "DeepSeek V4 Flash" },
  { id: "deepseek-v4-pro-0813", name: "DeepSeek V4 Pro" },
  { id: "deepseek-v4.1-flash", name: "DeepSeek V4.1 Flash" },
]

// ---- small helpers ---------------------------------------------------------

const firstOf = (...vs) => vs.find((v) => typeof v === "string" && v.trim())?.trim() ?? ""

const asRecord = (v) => (typeof v === "object" && v !== null && !Array.isArray(v) ? v : {})

function safeJson(text) {
  try {
    const parsed = JSON.parse(text)
    return typeof parsed === "object" && parsed !== null ? parsed : null
  } catch {
    return null
  }
}

function num(v) {
  if (typeof v === "number" && Number.isFinite(v)) return v
  if (typeof v === "string" && v.trim()) {
    const n = Number(v)
    if (Number.isFinite(n)) return n
  }
  return 0
}

const clamp = (n) => Math.max(0, Math.min(100, n))

// prettify is a model id as a name: openpangu-2.0-flash -> "Openpangu 2.0 Flash"
function prettify(id) {
  return String(id)
    .split(/[-_]/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ")
}

// normalizeModelId drops a trailing 4-digit version tag: deepseek-v4-flash-0731
// -> deepseek-v4-flash. Only for display names and the CONTEXT_WINDOWS lookup:
// the dated id is the registered free model, so ids sent upstream keep it.
function normalizeModelId(id) {
  if (typeof id === "string" && id.length > 5) {
    const suffix = id.slice(-5)
    if (suffix.startsWith("-") && /^\d{4}$/.test(suffix.slice(1))) return id.slice(0, -5)
  }
  return id
}

const BEIJING_MS = 8 * 3600 * 1000

/** Today as CodeArts counts days: a Beijing (UTC+8) date, "2026-10-21". */
const beijingDay = (at = Date.now()) => new Date(at + BEIJING_MS).toISOString().slice(0, 10)

/** The next Beijing midnight after `at`, in ms: when a daily allowance comes back. */
const nextBeijingMidnight = (at = Date.now()) => Date.parse(`${beijingDay(at)}T00:00:00+08:00`) + 86_400_000

/**
 * A date as CodeArts writes it, in ms (undefined when it can't be read):
 * epoch seconds or ms, ISO, or a zone-less "2026-11-01[ 00:00:00]", which is
 * Beijing time.
 */
function vendorDate(v) {
  if (typeof v === "number" && Number.isFinite(v) && v > 0) return v < 1e11 ? v * 1000 : v
  if (typeof v !== "string" || !v.trim()) return undefined
  const s = v.trim()
  if (/^\d+$/.test(s)) return vendorDate(Number(s))
  const local = /^(\d{4}-\d{2}-\d{2})(?:[ T](\d{2}:\d{2}(?::\d{2})?))?$/.exec(s)
  const at = local ? Date.parse(`${local[1]}T${local[2] ?? "00:00:00"}+08:00`) : Date.parse(s)
  return Number.isNaN(at) ? undefined : at
}

/**
 * A model's credit rate (官方「消耗系数」, shown "1.50x"): `credit` is one price
 * tier or a list of them, `ratio_display` the multiple. 0 when it has none.
 */
function creditRateOf(credit) {
  const tier = asRecord(Array.isArray(credit) ? credit[0] : credit)
  const n = parseFloat(String(tier.ratio_display ?? tier.ratioDisplay ?? ""))
  return Number.isFinite(n) && n > 0 ? n : 0
}

// upstreamModelId drops the provider prefix a host may leave on the id
// (`codearts/deepseek-v4.1-flash`): the gateway knows only the bare id, and
// answers anything else `InferHub.002002009.404`, inside a 200 when streaming.
function upstreamModelId(id) {
  return typeof id === "string" && id.startsWith(`${PROVIDER}/`) ? id.slice(PROVIDER.length + 1) : id
}

// ---- WebCrypto primitives --------------------------------------------------

function subtle() {
  const c = globalThis.crypto
  if (!c?.subtle) throw new Error("CodeArts: WebCrypto (globalThis.crypto.subtle) is unavailable")
  return c.subtle
}

const typed = (u8) => (u8 instanceof Uint8Array ? u8 : new Uint8Array(u8))

function randomBytes(n) {
  const out = new Uint8Array(n)
  globalThis.crypto.getRandomValues(out)
  return out
}

const hex = (u8) => Array.from(u8).map((b) => b.toString(16).padStart(2, "0")).join("")

function randomHex(n) {
  return hex(randomBytes(n))
}

function b64url(u8) {
  let s = ""
  for (const b of u8) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}

const utf8 = (s) => new TextEncoder().encode(s)

async function sha256Hex(data) {
  const digest = await subtle().digest("SHA-256", typed(data))
  return hex(new Uint8Array(digest))
}

async function hmacSha256Hex(key, data) {
  const cryptoKey = await subtle().importKey(
    "raw", typed(key), { name: "HMAC", hash: "SHA-256" }, false, ["sign"],
  )
  const sig = await subtle().sign("HMAC", cryptoKey, typed(data))
  return hex(new Uint8Array(sig))
}

/** PKCE pair: a random 48-byte base64url verifier + its S256 challenge. */
async function generatePkcePair() {
  const codeVerifier = b64url(randomBytes(48))
  const digest = await subtle().digest("SHA-256", utf8(codeVerifier))
  return { codeVerifier, codeChallenge: b64url(new Uint8Array(digest)) }
}

/** Generate an ES256 (P-256) DPoP key pair as JWKs. */
async function generateDpopKeyPair() {
  const pair = await subtle().generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"])
  const jwk = await subtle().exportKey("jwk", pair.privateKey)
  return {
    privateKeyJwk: { kty: "EC", crv: "P-256", x: jwk.x, y: jwk.y, d: jwk.d },
    publicKeyJwk: { kty: "EC", crv: "P-256", x: jwk.x, y: jwk.y },
  }
}

/** Recover a DPoP key pair from a persisted private JWK. */
function keyPairFromStoredJwk(jwk) {
  return {
    privateKeyJwk: { kty: "EC", crv: "P-256", x: jwk.x, y: jwk.y, d: jwk.d },
    publicKeyJwk: { kty: "EC", crv: "P-256", x: jwk.x, y: jwk.y },
  }
}

/** Sign a `dpop+jwt` proof (htm=method, htu=URL) with the DPoP private key. */
async function signDpopJws(keyPair, htm, htu) {
  const header = { alg: "ES256", typ: "dpop+jwt", jwk: keyPair.publicKeyJwk }
  const payload = { htm, htu, iat: Math.floor(Date.now() / 1000), jti: randomHex(32) }
  const signingInput = `${b64url(utf8(JSON.stringify(header)))}.${b64url(utf8(JSON.stringify(payload)))}`
  const key = await subtle().importKey(
    "jwk", keyPair.privateKeyJwk, { name: "ECDSA", namedCurve: "P-256" }, false, ["sign"],
  )
  const sig = await subtle().sign({ name: "ECDSA", hash: "SHA-256" }, key, utf8(signingInput))
  // WebCrypto ECDSA returns raw r||s, which is exactly the JWS ES256 format.
  return `${signingInput}.${b64url(new Uint8Array(sig))}`
}

// ---- Huawei SDK-HMAC-SHA256 request signing --------------------------------

function buildCanonicalRequest(method, uri, query, headers, payloadHash) {
  const signedHeaders = [...headers.keys()].sort()
  const headerLines = signedHeaders.map((k) => `${k}:${headers.get(k) ?? ""}`)
  return [method, uri, query, headerLines.join("\n"), "", signedHeaders.join(";"), payloadHash].join("\n")
}

// RFC 3986: everything but the unreserved characters is %-encoded, as the
// gateway encodes it when it checks the signature.
const rfc3986 = (s) => encodeURIComponent(s).replace(/[!'()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`)

/** The query as the signature has it: pairs sorted by key, then value, each encoded. */
function canonicalQuery(url) {
  return [...url.searchParams.entries()]
    .sort(([a, x], [b, y]) => (a < b ? -1 : a > b ? 1 : x < y ? -1 : x > y ? 1 : 0))
    .map(([k, v]) => `${rfc3986(k)}=${rfc3986(v)}`)
    .join("&")
}

/**
 * Sign a Huawei request; returns the header map to merge into the request.
 * Extra headers (e.g. `maas_type: benefit`) participate in the canonical
 * request and must be sent verbatim, or the server rejects the signature.
 */
async function signRequestHuawei(ak, sk, securityToken, method, urlStr, body, extraHeaders, dateStampOverride) {
  const url = new URL(urlStr)
  let uri = url.pathname
  if (!uri.endsWith("/")) uri += "/"
  const query = canonicalQuery(url)
  const dateStamp = dateStampOverride
    ?? new Date().toISOString().replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z")
  const payloadHash = await sha256Hex(body)

  const headers = new Map()
  headers.set("host", url.host)
  headers.set("x-sdk-date", dateStamp)
  headers.set("x-sdk-content-sha256", payloadHash)
  headers.set("x-security-token", securityToken)
  if (extraHeaders !== undefined) {
    for (const [k, v] of Object.entries(extraHeaders)) headers.set(k, v)
  }
  if (method.toUpperCase() !== "GET") headers.set("content-type", "application/json")

  const signedHeaders = [...headers.keys()].sort()
  const canonicalRequest = buildCanonicalRequest(method.toUpperCase(), uri, query, headers, payloadHash)
  const canonicalHash = await sha256Hex(utf8(canonicalRequest))
  const stringToSign = `SDK-HMAC-SHA256\n${dateStamp}\n${canonicalHash}`
  const signature = await hmacSha256Hex(utf8(sk), utf8(stringToSign))
  headers.set(
    "Authorization",
    `SDK-HMAC-SHA256 Access=${ak},SignedHeaders=${signedHeaders.join(";")},Signature=${signature}`,
  )
  return headers
}

// ---- OAuth token exchange --------------------------------------------------

/** Normalize an STS token response into a persisted CodeArts credential. */
function credentialFromTokenResponse(token, codeVerifier, keyPair) {
  const c = token?.credentials ?? {}
  return {
    access_key_id: c.access_key_id ?? "",
    secret_access_key: c.secret_access_key ?? "",
    security_token: c.security_token ?? "",
    expires_at: c.expiration ?? "",
    refresh_token: token?.refresh_token,
    code_verifier: codeVerifier,
    dpop_private_key_jwk: keyPair.privateKeyJwk,
    // Minted once per sign-in and kept across refreshes: the account's one
    // upstream session, whichever process sends the request.
    session_id: randomHex(16),
  }
}

/** Base64url (or plain base64) text to a parsed JSON object, or null. */
function b64Json(s) {
  if (typeof s !== "string" || !s) return null
  try {
    const t = s.replace(/-/g, "+").replace(/_/g, "/")
    return safeJson(new TextDecoder().decode(Uint8Array.from(atob(t + "=".repeat((4 - (t.length % 4)) % 4)), (c) => c.charCodeAt(0))))
  } catch {
    return null
  }
}

/**
 * Who signed in. STS answers with no user fields, so without this every
 * sign-in looked like a new account to magpie (one more `codearts#xxxxxx`);
 * the refresh_token is a JWT whose `user_profile` claim names the signer.
 * `id` tells accounts apart, `name` is what the usage card shows.
 */
function accountIdentityOf(cred) {
  const legacy = { id: firstOf(cred?.user_name, cred?.user_id), name: firstOf(cred?.user_name) }
  const token = typeof cred?.refresh_token === "string" ? cred.refresh_token : ""
  const parts = token.split(".")
  if (parts.length !== 3) return legacy
  const payload = b64Json(parts[1])
  const raw = payload?.user_profile
  const profile = typeof raw === "string" ? (safeJson(raw) ?? b64Json(raw)) : asRecord(raw)
  const id = firstOf(profile?.principal_urn, profile?.account_name, profile?.account_id, profile?.user_id, legacy.id)
  const name = firstOf(profile?.user_name, profile?.account_name, profile?.domain_name, legacy.name)
  return { id, name }
}

/** The credential a sign-in carries; magpie keeps it in the `access` field. */
function credentialOf(auth) {
  if (!auth || auth.type !== "oauth") return null
  const raw = typeof auth.access === "string" ? auth.access : ""
  if (!raw) return null
  const cred = safeJson(raw)
  return cred && typeof cred.access_key_id === "string" && cred.access_key_id ? cred : null
}

function expiresFromCredential(cred) {
  if (cred?.expires_at) {
    const parsed = Date.parse(cred.expires_at)
    if (!Number.isNaN(parsed)) return parsed
  }
  return Date.now() + 86_400_000
}

/** POST the STS token endpoint with a DPoP proof. */
async function requestToken(body, keyPair, fetcher) {
  const dpop = await signDpopJws(keyPair, "POST", STS_TOKEN_ENDPOINT)
  let response
  try {
    response = await fetcher(STS_TOKEN_ENDPOINT, {
      method: "POST",
      headers: { DPoP: dpop, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(body).toString(),
      // A 307/308 would send the refresh token and verifier on with the body.
      redirect: "error",
      signal: AbortSignal.timeout(TOKEN_TIMEOUT_MS),
    })
  } catch (e) {
    throw new Error(`CodeArts token request network error: ${String(e)}`)
  }
  let data = null
  try {
    data = await response.json()
  } catch {
    data = null
  }
  if (!response.ok || !data?.credentials) {
    const message = `CodeArts token request failed: ${response.status}${data ? ` ${JSON.stringify(data)}` : ""}`
    // Only refresh_token invalidation is terminal; a transient DPoP rejection
    // must not permanently mark the account.
    if (data?.error === "invalid_grant" || isDeadRefreshToken(message)) {
      throw Object.assign(new Error(message), { signIn: "expired" })
    }
    throw new Error(message)
  }
  return data
}

/**
 * A refresh STS turns away for good: the token expired, or the one-time token
 * was already spent (`STS5.1806`). Only signing in again brings the account
 * back; a network hiccup or DPoP rejection must not read as this.
 */
function isDeadRefreshToken(text) {
  return /ExpiredRefreshToken|STS5\.1806|invalid refresh token|refresh token has been used/i.test(String(text ?? ""))
}

const isPermanentRefreshFailure = (e) => e?.signIn === "expired" || isDeadRefreshToken(e?.message ?? e)

function exchangeAuthorizationCode(code, codeVerifier, port, keyPair, fetcher) {
  return requestToken({
    client_id: CLIENT_ID,
    code,
    code_verifier: codeVerifier,
    grant_type: GRANT_AUTHORIZATION_CODE,
    redirect_uri: `http://127.0.0.1:${port}${REDIRECT_PATH}`,
  }, keyPair, fetcher)
}

function exchangeRefreshToken(refreshToken, codeVerifier, keyPair, fetcher) {
  return requestToken({
    client_id: CLIENT_ID,
    code_verifier: codeVerifier,
    grant_type: GRANT_REFRESH_TOKEN,
    refresh_token: refreshToken,
  }, keyPair, fetcher)
}

/** Refresh a stored credential, reusing its verifier + DPoP private key. */
async function refreshCredential(cred, fetcher) {
  const refreshToken = firstOf(cred.refresh_token)
  if (!refreshToken || !cred.code_verifier || !cred.dpop_private_key_jwk) {
    // Nothing to renew it with: it lapses for good, so the account is marked
    // to sign in again rather than retried forever.
    throw Object.assign(
      new Error("CodeArts: credential cannot be refreshed (missing refresh_token/code_verifier/dpop key); sign in again"),
      { signIn: "expired" },
    )
  }
  const keyPair = keyPairFromStoredJwk(cred.dpop_private_key_jwk)
  const token = await exchangeRefreshToken(refreshToken, cred.code_verifier, keyPair, fetcher)
  const next = credentialFromTokenResponse(token, cred.code_verifier, keyPair)
  if (!next.refresh_token) next.refresh_token = refreshToken
  // Preserve non-rotating identity fields a legacy login may have carried, and
  // the session id: it belongs to the account, not to the token.
  for (const k of ["domain_id", "user_id", "user_name", "session_id"]) {
    if (cred[k]) next[k] = cred[k]
  }
  return next
}

/**
 * The account's upstream session headers, the same in every process and across
 * refreshes: a fresh Chat-Id per request opened a new session each time and
 * pinned the 3-session cap. Credentials from before `session_id` existed fall
 * back to the signer's identity, then to the access key.
 */
async function accountSessionIds(cred) {
  const seed = firstOf(cred.session_id, accountIdentityOf(cred).id, cred.access_key_id)
  const digest = await sha256Hex(utf8(`codearts-session:${seed}`))
  return { sessionId: digest.slice(0, 32), chatId: digest.slice(32, 64) }
}

// ---- OAuth login: URL + loopback callback ----------------------------------

/** Build the Huawei Cloud portal authorize URL (params mirror the real IDE). */
function buildOAuthLoginUrl(port, pkce, ticketId) {
  return `${PORTAL_AUTHORIZE_BASE}?theme=${OAUTH_THEME}&locale=${OAUTH_LOCALE}`
    + `&uri_scheme=${CLIENT_ID}&client_id=${CLIENT_ID}&port=${port}`
    + `&code_challenge=${pkce.codeChallenge}&code_challenge_method=SHA-256`
    + `&ticket_id=${ticketId}&plugin-name=${LOGIN_PLUGIN_NAME}&plugin-version=${LOGIN_PLUGIN_VERSION}`
}

const buildPortalLoginResultUrl = (ok) =>
  `${PORTAL_LOGIN_BASE}?login_succeed=${ok}&uri_scheme=${CLIENT_ID}&locale=${OAUTH_LOCALE}`

/**
 * Start the loopback callback server, listening on 127.0.0.1:port>=10000. It
 * takes one thing: a GET to the callback path, exactly as sent, with the
 * authorization code, which it exchanges at STS (PKCE + DPoP), then sends the
 * browser on to the portal's own result page. A callback without a code ends
 * the sign-in at once, with the portal's own words when it gave any. The
 * redirect target is always that page, never a parameter of the request.
 */
function startOAuthCallbackServer(pkce, keyPair, fetcher) {
  let resolveResult
  let rejectResult
  const result = new Promise((resolve, reject) => {
    resolveResult = resolve
    rejectResult = reject
  })
  let answered = false

  const server = createServer((request, response) => {
    // The path as sent, not as a URL would normalise it (`/oauth/./callback`).
    const raw = request.url ?? "/"
    const query = raw.indexOf("?")
    if ((query < 0 ? raw : raw.slice(0, query)) !== REDIRECT_PATH) {
      response.writeHead(404).end("Not found")
      return
    }
    if (request.method !== "GET") {
      response.writeHead(405, { Allow: "GET" }).end("Method not allowed")
      return
    }
    // One sign-in, one callback: another one for the same flow is refused.
    if (answered) {
      response.writeHead(409).end("This sign-in is already being completed")
      return
    }
    answered = true
    const url = new URL(raw, `http://127.0.0.1:${request.socket.localPort}`)
    const code = url.searchParams.get("code")
    if (!code) {
      response.writeHead(307, { Location: buildPortalLoginResultUrl(false) }).end()
      const said = firstOf(url.searchParams.get("error_description"), url.searchParams.get("error"))
      rejectResult(new Error(`CodeArts sign-in failed: ${said || "the callback came back without an authorization code"}`))
      return
    }
    const port = request.socket.localPort ?? 0
    void exchangeAuthorizationCode(code, pkce.codeVerifier, port, keyPair, fetcher).then(
      (token) => {
        response.writeHead(307, { Location: buildPortalLoginResultUrl(true) }).end()
        const cred = credentialFromTokenResponse(token, pkce.codeVerifier, keyPair)
        resolveResult({ access: JSON.stringify(cred), expires: expiresFromCredential(cred) })
      },
      (error) => {
        response.writeHead(307, { Location: buildPortalLoginResultUrl(false) }).end()
        rejectResult(error)
      },
    )
  })

  return listenOnCallbackPort(server).then((port) => ({ port, server, result }))
}

const CALLBACK_PORT_TRIES = 20
const randomCallbackPort = () => Math.floor(Math.random() * (65_536 - MIN_CALLBACK_PORT)) + MIN_CALLBACK_PORT

/**
 * Listen on 127.0.0.1: the port the system gives, or, when that is below
 * 10000, a random one >= 10000, another while the one picked is taken.
 */
function listenOnCallbackPort(server) {
  return new Promise((resolve, reject) => {
    let tries = 0
    const tryListen = (port) => {
      const failed = (e) => {
        if (port !== 0 && e?.code === "EADDRINUSE" && ++tries < CALLBACK_PORT_TRIES) tryListen(randomCallbackPort())
        else reject(e)
      }
      server.once("error", failed)
      server.listen(port, "127.0.0.1", () => {
        server.off("error", failed)
        const address = server.address()
        const assigned = typeof address === "object" && address ? address.port : 0
        if (assigned >= MIN_CALLBACK_PORT) {
          resolve(assigned)
          return
        }
        server.close(() => tryListen(randomCallbackPort()))
      })
    }
    tryListen(0)
  })
}

/**
 * Start an OAuth flow and return its login URL immediately (magpie opens it).
 * The result resolves once the browser is redirected back and STS answers;
 * the callback server is closed as soon as the result settles.
 */
async function startOAuthFlow(fetcher) {
  const pkce = await generatePkcePair()
  const keyPair = await generateDpopKeyPair()
  const { port, server, result } = await startOAuthCallbackServer(pkce, keyPair, fetcher)
  // The authorize URL carries a ticket_id as the IDE's does; the code flow
  // itself never reads it back.
  const loginUrl = buildOAuthLoginUrl(port, pkce, randomHex(32))

  let closed = false
  const close = async () => {
    if (closed) return
    closed = true
    await new Promise((resolve) => server.close(() => resolve()))
  }

  const resultWithUrl = Promise.race([
    result,
    new Promise((_, reject) => {
      const timer = setTimeout(() => reject(new Error("CodeArts OAuth login timed out")), OAUTH_CALLBACK_TIMEOUT_MS)
      timer.unref?.()
    }),
  ]).then((outcome) => ({ ...outcome, loginUrl }))

  // Settle in the background so an early result never becomes an unhandled
  // rejection, and so the listening socket is always released.
  resultWithUrl.catch(() => {}).finally(() => { void close() })

  return { loginUrl, result: resultWithUrl, close }
}

// ---- signed requests to CodeArts -------------------------------------------

/** Turn a request body (string / bytes / stream / blob) into bytes. */
async function toBytes(body) {
  if (body == null) return new Uint8Array()
  if (typeof body === "string") return utf8(body)
  if (body instanceof Uint8Array) return body
  if (body instanceof ArrayBuffer) return new Uint8Array(body)
  if (ArrayBuffer.isView(body)) return new Uint8Array(body.buffer, body.byteOffset, body.byteLength)
  if (typeof body?.getReader === "function") {
    const chunks = []
    const reader = body.getReader()
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      chunks.push(typed(value))
    }
    const total = chunks.reduce((n, c) => n + c.length, 0)
    const out = new Uint8Array(total)
    let offset = 0
    for (const c of chunks) {
      out.set(c, offset)
      offset += c.length
    }
    return out
  }
  if (typeof body?.arrayBuffer === "function") return new Uint8Array(await body.arrayBuffer())
  return utf8(String(body))
}

/**
 * Is `model` one of the account's free models, sent with the signed
 * `maas_type: benefit` header? `state.benefit` is the account's own list once
 * read (null until then, when the fallback stands); one the gateway said has
 * no benefit (`state.noBenefit`) is not.
 */
function isBenefitModel(model, state) {
  const id = String(upstreamModelId(model) ?? "")
  const free = state?.benefit ? state.benefit.has(id) : BENEFIT_MODEL_FALLBACK.includes(id)
  return free && !state?.noBenefit?.has(id)
}

/** Can `model` be called without the benefit header: one of the account's builtin models, or not known yet? */
function isRegularModel(model, state) {
  return !state?.regular || state.regular.has(String(upstreamModelId(model) ?? ""))
}

/**
 * Reshape a chat body into what the gateway accepts and meters:
 * - the bare model id, without the host's `codearts/` prefix;
 * - thinking asked for explicitly (unless the caller said, or the plugin's
 *   `thinking: false` option), so `content` carries only the reply;
 * - the output budget inside the gateway's 65536;
 * - always `stream: true`. CodeArts meters only streamed answers — a
 *   non-streaming 200 returns tokens yet leaves the credits untouched — so a
 *   caller that asked for JSON gets the stream folded back (`fold`).
 * A body that is not a chat (no `model` + `messages`) gives null.
 */
function prepareChatBody(parsed, { thinking = true } = {}) {
  if (!parsed || typeof parsed.model !== "string" || !Array.isArray(parsed.messages)) return null
  const body = { ...parsed, model: upstreamModelId(parsed.model) }
  // `injected`: ours to take back should a model refuse the parameter.
  const injected = thinking && body.thinking === undefined
  if (injected) body.thinking = ENABLE_THINKING
  for (const k of ["max_tokens", "max_completion_tokens"]) {
    if (typeof body[k] === "number" && body[k] > MAX_OUTPUT_TOKENS) body[k] = MAX_OUTPUT_TOKENS
  }
  const fold = body.stream !== true
  body.stream = true
  return { body, fold, injected }
}

// ---- reading the gateway's answers -----------------------------------------

/** The JSON payloads of an SSE body, in arrival order, `[DONE]` skipped. */
function parseSseEvents(text) {
  const events = []
  for (const line of String(text).split(/\r?\n/)) {
    const s = line.trim()
    if (!s.startsWith("data:")) continue
    const payload = s.slice(5).trim()
    if (!payload || payload === "[DONE]") continue
    const parsed = safeJson(payload)
    if (parsed) events.push(parsed)
  }
  return events
}

/** The error code an answer carries (`TM.00001041`, `InferHub.4291.200`, …), or "". */
function errorCodeOf(text) {
  // Any number of named parts before the digits: `InferHub.ModelArts.81111.429`.
  const m = /"(?:error_code|code)"\s*:\s*"((?:[A-Za-z]+\.)*[A-Za-z]*[0-9][0-9A-Za-z.]*)"/.exec(String(text ?? ""))
  return m ? m[1] : ""
}

/** The vendor's own words for an error, so the agent can show them. */
function errorMessageOf(text) {
  const m = /"(?:error_msg|message)"\s*:\s*"((?:[^"\\]|\\.)*)"/.exec(String(text ?? ""))
  if (!m) return ""
  try {
    return JSON.parse(`"${m[1]}"`)
  } catch {
    return m[1]
  }
}

/** A 401/400 that only means the temporary AK/SK/security-token went stale. */
function isStaleCredential(text) {
  return /APIG\.0(301|307|602)|security token has expired|verify ak sk signature fail/i.test(String(text ?? ""))
}

/** Codes magpie should treat as "try another account later": quota or a rate limit. */
function isRateLimitCode(code) {
  return /4291|8111\d|(^|\.)429(\.|$)/.test(String(code ?? ""))
}

/**
 * The status for an error the gateway put inside a 200: 429 for a spent quota
 * or a rate limit (magpie moves to another account), 400 for a model or benefit
 * the account doesn't have (retrying can't help), 502 for the rest.
 */
function embeddedErrorStatus(code) {
  if (isRateLimitCode(code)) return 429
  if (/(^|\.)40[0-4](\.|$)|InferHub\.400[0-9]\./.test(String(code ?? ""))) return 400
  return 502
}

/** Does this event carry part of an answer (not only a role or a keepalive)? */
function eventHasContent(event) {
  const choice = Array.isArray(event?.choices) ? event.choices[0] : undefined
  if (!choice) return false
  if (choice.finish_reason != null || choice.message) return true
  const delta = asRecord(choice.delta)
  if (typeof delta.content === "string" && delta.content) return true
  if (typeof delta.reasoning_content === "string" && delta.reasoning_content) return true
  return Array.isArray(delta.tool_calls) && delta.tool_calls.length > 0
}

/**
 * What the head of a 200 body is: "answer", an error code, or undefined (read
 * on). The gateway reports the session cap, a spent quota or an unknown model
 * as an error event inside a 200, sometimes after an empty role-only delta, and
 * a 200 never fails over — the caller just saw an empty reply.
 */
function classifyHead(text, done) {
  if (parseSseEvents(text).some(eventHasContent)) return "answer"
  if (done && Array.isArray(safeJson(text)?.choices)) return "answer"
  const code = errorCodeOf(text)
  // Wait for the error event's end, so its message is whole.
  if (code) return done || /\n/.test(text.slice(text.indexOf(code))) ? code : undefined
  // The stream's own end, never a "[DONE]" inside an event: the gateway's
  // rate-limit error carries `"text":"[DONE]"`.
  if (done || /^data:\s*\[DONE\]\s*$/m.test(text)) return "answer"
  return undefined
}

/** Read a 200's head until it is an answer or an error; keep what was read. */
async function peekAnswer(res) {
  const head = { code: "", text: "", chunks: [], reader: null, decoder: new TextDecoder() }
  if (!res.body) return head
  head.reader = res.body.getReader()
  let done = false
  for (;;) {
    const verdict = classifyHead(head.text, done)
    if (verdict) {
      if (verdict !== "answer") head.code = verdict
      break
    }
    if (head.text.length >= SSE_PEEK_LIMIT) break
    const r = await head.reader.read()
    if (r.done) {
      done = true
      continue
    }
    head.chunks.push(typed(r.value))
    head.text += head.decoder.decode(r.value, { stream: true })
  }
  // An error answer is dropped; not awaited, as Node's cancel can hang on a live body.
  if (head.code) void head.reader.cancel().catch(() => {})
  return head
}

/** Headers for a body handed over re-read: length/encoding no longer hold. */
function streamHeaders(headers) {
  const out = new Headers(headers)
  out.delete("content-encoding")
  out.delete("content-length")
  return out
}

/** The peeked bytes, then the rest of the stream; `release` once it ends. */
function replayBody(head, release) {
  let i = 0
  return new ReadableStream({
    async pull(controller) {
      try {
        if (i < head.chunks.length) {
          controller.enqueue(head.chunks[i++])
          return
        }
        const { done, value } = head.reader ? await head.reader.read() : { done: true }
        if (done) {
          release()
          controller.close()
          return
        }
        controller.enqueue(typed(value))
      } catch (e) {
        release()
        controller.error(e)
      }
    },
    cancel(reason) {
      release()
      void head.reader?.cancel(reason).catch(() => {})
    },
  })
}

/** Fold streamed `choices[].delta` chunks into one OpenAI completion object. */
function completionFromChunks(events) {
  const first = events[0] ?? {}
  const content = []
  const reasoning = []
  const toolCalls = []
  let finish = null
  let usage = null
  let model = first.model
  for (const ev of events) {
    if (ev.usage) usage = ev.usage
    if (typeof ev.model === "string" && ev.model) model = ev.model
    const choice = Array.isArray(ev.choices) ? ev.choices[0] : undefined
    if (!choice) continue
    if (choice.finish_reason != null) finish = choice.finish_reason
    const delta = asRecord(choice.delta)
    if (typeof delta.content === "string") content.push(delta.content)
    if (typeof delta.reasoning_content === "string") reasoning.push(delta.reasoning_content)
    for (const call of Array.isArray(delta.tool_calls) ? delta.tool_calls : []) {
      const slot = (toolCalls[num(call.index)] ??= { id: "", type: "function", function: { name: "", arguments: "" } })
      if (typeof call.id === "string" && call.id) slot.id = call.id
      if (typeof call.type === "string" && call.type) slot.type = call.type
      if (typeof call.function?.name === "string") slot.function.name += call.function.name
      if (typeof call.function?.arguments === "string") slot.function.arguments += call.function.arguments
    }
  }
  const message = { role: "assistant", content: content.join("") }
  if (reasoning.length > 0) message.reasoning_content = reasoning.join("")
  const calls = toolCalls.filter(Boolean)
  if (calls.length > 0) message.tool_calls = calls
  return {
    id: firstOf(first.id) || `chatcmpl-${randomHex(12)}`,
    object: "chat.completion",
    created: num(first.created) || Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, message, finish_reason: finish ?? (calls.length ? "tool_calls" : "stop") }],
    ...(usage ? { usage } : {}),
  }
}

/** Answer a caller that asked for JSON: read the whole stream, fold it. */
async function foldAnswer(res, head) {
  let text = head.text
  if (head.reader) {
    for (;;) {
      const { done, value } = await head.reader.read()
      if (done) break
      text += head.decoder.decode(value, { stream: true })
    }
    text += head.decoder.decode()
  }
  const headers = streamHeaders(res.headers)
  headers.set("content-type", "application/json")
  const events = parseSseEvents(text)
  if (events.some((ev) => Array.isArray(ev.choices) && ev.choices.length > 0)) {
    return new Response(JSON.stringify(completionFromChunks(events)), { status: res.status, statusText: res.statusText, headers })
  }
  // The gateway answered JSON after all, or nothing at all.
  if (Array.isArray(safeJson(text)?.choices)) return new Response(text, { status: res.status, statusText: res.statusText, headers })
  return errorResponse(502, errorCodeOf(text), firstOf(errorMessageOf(text), "the gateway returned an empty answer"))
}

/** An OpenAI-shaped error carrying the vendor's code and words (magpie adds the provider name). */
function errorResponse(status, code, message, extraHeaders = {}) {
  return new Response(
    JSON.stringify({ error: { message: `${message}${code ? ` (${code})` : ""}`, type: status === 429 ? "rate_limit_exceeded" : "upstream_error", code: code || null } }),
    { status, headers: { "content-type": "application/json", ...extraHeaders } },
  )
}

/** A 401 magpie reads as "this sign-in is gone": it marks it and fails over. */
function signInExpiredResponse(text) {
  const body = text || JSON.stringify({ error: { message: "the sign-in expired; sign in again", type: "authentication_error" } })
  return new Response(body, { status: 401, headers: { "content-type": "application/json", "X-Magpie-Sign-In": "expired" } })
}

/**
 * A stale credential that couldn't be renewed: turned away for good, a 401
 * that marks the sign-in; or only for now (STS unreachable, a DPoP proof
 * refused), a 503 that keeps it, for magpie to try again or fail over.
 */
function unrenewedResponse(context, text) {
  if (context.signInDead?.()) return signInExpiredResponse(text)
  return errorResponse(503, errorCodeOf(text), "临时凭据已过期，续期暂时失败，请稍后再试", { "X-Magpie-Sign-In": "kept" })
}

/** Rebuild a response whose body was read, keeping the vendor's answer. */
function rebuildResponse(res, text, extraHeaders = {}) {
  const headers = streamHeaders(res.headers)
  for (const [k, v] of Object.entries(extraHeaders)) headers.set(k, v)
  return new Response(text, { status: res.status, statusText: res.statusText, headers })
}

// ---- the account's session budget ------------------------------------------

/**
 * Per-account gate for in-flight chats: a burst queues here rather than come
 * back as `TM.00001041`. `acquire` resolves to that permit's release (safe to
 * call twice); a permit is let go by itself after PERMIT_MAX_HOLD_MS.
 */
function createConcurrencyGate(limit) {
  let active = 0
  const waiters = []
  const grant = () => {
    active += 1
    let held = true
    const release = () => {
      if (!held) return
      held = false
      clearTimeout(timer)
      active -= 1
      while (active < limit && waiters.length > 0) waiters.shift().admit()
    }
    const timer = setTimeout(release, PERMIT_MAX_HOLD_MS)
    timer.unref?.()
    return release
  }
  return {
    get active() { return active },
    get waiting() { return waiters.length },
    acquire(signal) {
      if (signal?.aborted) return Promise.reject(abortError(signal))
      if (active < limit) return Promise.resolve(grant())
      return new Promise((resolve, reject) => {
        const onAbort = () => {
          const i = waiters.indexOf(waiter)
          if (i >= 0) waiters.splice(i, 1)
          reject(abortError(signal))
        }
        const waiter = {
          admit() {
            signal?.removeEventListener("abort", onAbort)
            resolve(grant())
          },
        }
        signal?.addEventListener("abort", onAbort, { once: true })
        waiters.push(waiter)
      })
    },
  }
}

function abortError(signal) {
  return signal?.reason instanceof Error ? signal.reason : new DOMException("The operation was aborted.", "AbortError")
}

/** Sleep, but give up as soon as the caller aborts the request. */
function delay(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError(signal))
      return
    }
    const onAbort = () => {
      clearTimeout(timer)
      reject(abortError(signal))
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort)
      resolve()
    }, ms)
    signal?.addEventListener("abort", onAbort, { once: true })
  })
}

/** The wait before retry `attempt` (±20% so concurrent callers spread out). */
function retryDelayMs(attempt, delays = SESSION_RETRY_DELAYS_MS) {
  const base = delays[Math.min(attempt, delays.length - 1)] ?? 0
  return Math.round(base * (0.8 + Math.random() * 0.4))
}

/** The queue a 429 put the request in, from its headers; undefined when it is no queue. */
function queueInfoOf(headers) {
  if (String(headers?.get("queue-status") ?? "").toLowerCase() !== "queued") return undefined
  const traceId = headers.get("trace-id")
  if (!traceId) return undefined
  const after = Number(headers.get("retry-after") ?? 5)
  return { traceId, retryAfter: Number.isFinite(after) && after >= 0 ? after : 5 }
}

/** One signed call on a queued trace: GET is where it stands, DELETE takes it out. */
async function traceQueueCall(method, cred, traceId, context, signal) {
  const url = TRACE_QUEUE_URL(traceId)
  const headers = await signFor(cred, method, url, new Uint8Array())
  headers.set(SESSION_HEADER, context.sessionId)
  return context.fetcher(url, { method, headers, signal, redirect: "error" })
}

/** The caller's signal, bounded to `ms` from now: a call that hangs can't outlast the wait it is part of. */
function withinMs(signal, ms) {
  const timeout = AbortSignal.timeout(Math.max(0, Math.min(ms, REQUEST_TIMEOUT_MS)))
  return signal ? AbortSignal.any([signal, timeout]) : timeout
}

/**
 * Wait in the gateway's queue the way the CodeArts agent does: poll every
 * `retry-after` seconds until the trace is promoted (then the request is sent
 * again), and leave the queue when the caller gives up or the wait runs out.
 * Gives "promoted" or "gone"; throws the abort.
 */
async function waitInQueue(cred, queued, context, signal, deadline) {
  const leave = () => void traceQueueCall("DELETE", cred, queued.traceId, context, AbortSignal.timeout(REQUEST_TIMEOUT_MS)).catch(() => {})
  let retryAfter = queued.retryAfter
  let failures = 0
  try {
    for (;;) {
      if (Date.now() + retryAfter * 1000 > deadline) {
        leave()
        return "gone"
      }
      await delay(retryAfter * 1000, signal)
      let body = null
      try {
        // The poll and its answer are bounded by what is left of the wait.
        const res = await traceQueueCall("GET", cred, queued.traceId, context, withinMs(signal, deadline - Date.now()))
        body = safeJson(await res.text())
        if (!res.ok && !body?.status) throw new Error(`HTTP ${res.status}`)
      } catch (e) {
        if (signal?.aborted) throw e
        if (++failures >= 3) {
          leave()
          return "gone"
        }
        continue
      }
      const status = String(body?.status ?? "queued")
      if (status === "promoted") return "promoted"
      if (status === "not_found" || status === "error") return "gone"
      retryAfter = num(body?.retry_after) || retryAfter
    }
  } catch (e) {
    leave()
    throw e
  }
}

/** The host of a URL, "" when it isn't one. */
function hostOf(url) {
  try {
    return new URL(url).hostname
  } catch {
    return ""
  }
}

/** Is this an https URL on one of SIGNED_HOSTS (its default port), the only ones the credentials go to? */
function isSignedHostUrl(url) {
  try {
    const u = new URL(url)
    return u.protocol === "https:" && SIGNED_HOSTS.has(u.host)
  } catch {
    return false
  }
}

const refusalTo = (url) =>
  `refusing to sign a request to ${hostOf(url) || "an unknown host"}: only CodeArts' own hosts (${[...SIGNED_HOSTS].join(", ")}) get the account's credentials`

/**
 * The SDK-HMAC-SHA256 headers for one request (all but `host`, which fetch
 * sets), signed only for SIGNED_HOSTS: whichever route asks, now or added
 * later, the account's credentials sign nothing else. Every signed call also
 * goes with `redirect: "error"`: a redirect would forward the security token
 * to wherever it points.
 */
async function signFor(cred, method, url, body, extraSigned) {
  if (!isSignedHostUrl(url)) throw new Error(refusalTo(url))
  const signed = await signRequestHuawei(
    cred.access_key_id, cred.secret_access_key, cred.security_token,
    method, url, body, extraSigned,
  )
  const headers = new Headers()
  signed.forEach((value, key) => {
    if (key !== "host") headers.set(key, value)
  })
  return headers
}

/** Mark a 401/403 the way magpie reads a sign-in that is gone. */
function withSignInMark(res) {
  if (res.status !== 401 && res.status !== 403) return res
  const h = new Headers(res.headers)
  h.set("X-Magpie-Sign-In", "expired")
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers: h })
}

/**
 * The plugin's per-request fetch. A chat is reshaped (see prepareChatBody),
 * signed with the account's temporary credentials and sent under the account's
 * one Chat-Id/Session-Id, after taking one of its in-flight permits. Then:
 * - `TM.00001041` (a 400, or a 200's first event) is retried, re-signed, until
 *   a session frees or the budget runs out — then a 429, which magpie fails
 *   over on;
 * - a stale credential (401, `APIG.0602`) is renewed once and the request sent
 *   again; when STS turns the renewal away for good, a 401 marks the sign-in,
 *   and when it only fails for now, a 503 keeps it;
 * - `benefit not found` on a day not yet claimed claims it and sends again;
 *   otherwise the model goes as the account's builtin one, if it has it;
 * - a 200 carrying only an error (a spent quota, an unknown model) becomes a
 *   429/502 with the vendor's words instead of an empty reply.
 * The permit is held until the answer's body ends: the gateway counts the
 * session while it streams. Only SIGNED_HOSTS are signed for: a request
 * anywhere else (an overridden `api`, say) is refused before the access key or
 * security token leave.
 */
async function signedFetch(cred, input, init, context) {
  const req = typeof Request !== "undefined" && input instanceof Request ? input : null
  const url = req ? req.url : String(input)
  if (!isSignedHostUrl(url)) return errorResponse(400, "", refusalTo(url))
  const method = String(init?.method ?? req?.method ?? "GET").toUpperCase()
  const baseHeaders = new Headers(init?.headers ?? req?.headers)
  let body
  if (init?.body != null) body = await toBytes(init.body)
  else if (req) body = new Uint8Array(await req.clone().arrayBuffer())
  else body = new Uint8Array()
  // The body is part of the signature, so it is reshaped before signing.
  const parsed = body.length > 0 ? safeJson(new TextDecoder().decode(body)) : null
  const chat = prepareChatBody(parsed, context)
  if (chat) body = utf8(JSON.stringify(chat.body))
  const model = upstreamModelId(parsed?.model)
  let extra = isBenefitModel(model, context.state) ? { maas_type: "benefit" } : undefined
  const signal = init?.signal ?? req?.signal ?? undefined

  let active = cred
  const send = async () => {
    const signed = await signFor(active, method, url, body, extra)
    const headers = new Headers(baseHeaders)
    // The body may have been reshaped: the transport counts it again.
    headers.delete("content-length")
    signed.forEach((value, key) => headers.set(key, value))
    headers.set("Chat-Id", context.chatId)
    headers.set("Session-Id", context.sessionId)
    // The session headers the CodeArts agent sends: one session per account,
    // a fresh trace per request.
    headers.set(SESSION_HEADER, context.sessionId)
    headers.set("user-session-id", context.sessionId)
    headers.set("x-ot-parent-session-id", "")
    headers.set("x-ot-trace-id", randomHex(16))
    headers.set("x-ot-span-id", randomHex(8))
    headers.set("x-snap-traceid", randomHex(16))
    headers.set("lang", "en")
    return context.fetcher(url, {
      ...init,
      method,
      headers,
      ...(body.length > 0 ? { body } : {}),
      ...(signal ? { signal } : {}),
      // After the caller's init, so no caller turns it back on.
      redirect: "error",
    })
  }

  // A sign-in known to be beyond renewal never reaches the network again.
  if (context.signInDead?.()) return signInExpiredResponse()
  if (!chat) {
    const res = await send()
    if (res.status !== 401 || !context.renewCredential) return withSignInMark(res)
    const text = await res.text().catch(() => "")
    const next = await context.renewCredential(active)
    if (!next) return unrenewedResponse(context, text)
    active = next
    return withSignInMark(await send())
  }

  const release = await context.gate.acquire(signal)
  let handedOver = false
  try {
    const clock = context.now ?? Date.now
    const started = clock()
    let renewed = false
    let claimTried = false
    let queueDeadline = 0
    for (let attempt = 0; ; attempt += 1) {
      const res = await send()
      let text
      let code
      if (res.status === 429) {
        const queued = queueInfoOf(res.headers)
        if (queued) {
          void res.body?.cancel().catch(() => {})
          queueDeadline ||= Date.now() + (context.queueBudgetMs ?? QUEUE_BUDGET_MS)
          if ((await waitInQueue(active, queued, context, signal, queueDeadline)) === "promoted") continue
          return errorResponse(429, "", "排队等待超时或已被移出队列，请稍后再试")
        }
      }
      if (res.ok) {
        const head = await peekAnswer(res)
        if (!head.code) {
          if (chat.fold) return await foldAnswer(res, head)
          handedOver = true
          return new Response(replayBody(head, release), { status: res.status, statusText: res.statusText, headers: streamHeaders(res.headers) })
        }
        text = head.text
        code = head.code
      } else {
        text = await res.text().catch(() => "")
        code = errorCodeOf(text)
      }

      if (res.status === 401 || isStaleCredential(text)) {
        if (!renewed && context.renewCredential) {
          renewed = true
          const next = await context.renewCredential(active)
          if (next) {
            active = next
            continue
          }
          return unrenewedResponse(context, text)
        }
        return signInExpiredResponse(text)
      }
      if (res.status === 403) return rebuildResponse(res, text, { "X-Magpie-Sign-In": "expired" })
      // The thinking parameter is the plugin's own: a model that refuses it
      // gets the request again as the caller wrote it.
      if (res.status === 400 && chat.injected && /thinking/i.test(text)) {
        chat.injected = false
        delete chat.body.thinking
        body = utf8(JSON.stringify(chat.body))
        continue
      }
      if (extra && /InferHub\.400[45]\.200|benefit not found/i.test(code || text)) {
        // A new Beijing day whose benefit isn't claimed yet: claim it, as the
        // agent does each day, and send it again as the free model.
        if (!claimTried && context.state && context.state.claim?.day !== beijingDay()) {
          claimTried = true
          if (await ensureBenefitClaimed(active, context.fetcher, context.state)) continue
        }
        // No free benefit for this model on this account (`4004`), or the
        // header isn't taken for it (`4005`): it is the account's plain
        // builtin model, till the next day's claim.
        if (isRegularModel(model, context.state)) {
          context.state?.noBenefit.add(model)
          extra = undefined
          continue
        }
      }
      if (code === SESSION_LIMIT_ERROR || text.includes(SESSION_LIMIT_ERROR)) {
        const wait = retryDelayMs(attempt, context.retryDelays)
        if (clock() - started + wait <= (context.retryBudgetMs ?? SESSION_RETRY_BUDGET_MS)) {
          await (context.sleep ?? delay)(wait, signal)
          continue
        }
        return errorResponse(429, SESSION_LIMIT_ERROR, firstOf(errorMessageOf(text), "并发会话数已达上限，请稍后再试"))
      }
      if (!res.ok) return rebuildResponse(res, text)
      return errorResponse(embeddedErrorStatus(code), code, firstOf(errorMessageOf(text), "the gateway answered with an error"))
    }
  } finally {
    if (!handedOver) release()
  }
}

/** A signed GET returning the response text, or undefined on any failure. */
async function signedGet(url, cred, fetcher, extraUnsignedHeaders) {
  try {
    const headers = await signFor(cred, "GET", url, new Uint8Array())
    for (const [k, v] of Object.entries(extraUnsignedHeaders ?? {})) headers.set(k, v)
    const res = await fetcher(url, { method: "GET", headers, redirect: "error", signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) })
    if (!res.ok) return undefined
    return await res.text()
  } catch {
    return undefined
  }
}

/** A failed answer as one line: the gateway's own code and words, not just the status. */
function describeHttpFailure(status, text) {
  const raw = safeJson(text)
  const detail = raw
    ? [firstOf(raw.error_code, String(raw.code ?? "")), firstOf(raw.error_msg, raw.message, raw.msg)].filter(Boolean).join(" ")
    : String(text ?? "").trim().slice(0, 200)
  return detail ? `HTTP ${status}: ${detail}` : `HTTP ${status}`
}

/**
 * A signed snap-access call the way the IDE's ActivityWelfarePane makes it:
 * `{ code, message, data }` back, `code !== 0` being a refusal.
 * Gives `{ ok: true, data }` or `{ ok: false, message }`, never throws.
 */
async function snapRequest(method, url, cred, fetcher, payload) {
  const body = payload === undefined ? new Uint8Array() : utf8(JSON.stringify(payload))
  let res
  let text
  try {
    const headers = await signFor(cred, method, url, body)
    for (const [k, v] of Object.entries(SNAP_EXTRA_HEADERS)) headers.set(k, v)
    res = await fetcher(url, {
      method,
      headers,
      ...(payload === undefined ? {} : { body }),
      redirect: "error",
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    })
    text = await res.text()
  } catch (e) {
    return { ok: false, message: e?.message ?? String(e) }
  }
  if (!res.ok) return { ok: false, message: describeHttpFailure(res.status, text) }
  const raw = safeJson(text)
  if (!raw || Array.isArray(raw)) return { ok: false, message: "the answer could not be read" }
  if (raw.code !== undefined && Number(raw.code) !== 0) {
    return { ok: false, message: firstOf(raw.message, raw.msg, `code ${raw.code}`) }
  }
  return { ok: true, data: "data" in raw ? raw.data : raw }
}

// ---- daily check-in ----------------------------------------------------------

/** The account's 「每日领取」 campaign from the activity list, or a reason it can't be read. */
async function dailyCampaign(cred, fetcher) {
  const listed = await snapRequest("GET", OPS_DELIVERY_URL, cred, fetcher)
  if (!listed.ok) return { error: `活动列表读取失败：${listed.message}` }
  const items = asRecord(listed.data).items
  if (!Array.isArray(items)) return { error: "活动列表缺少 items" }
  return { item: items.find((it) => asRecord(it).type === DAILY_CHECKIN_TYPE) }
}

const isClaimedToday = (item) => !item?.claimable && CLAIMED_STATUSES.includes(item?.status)

// ---- credit batches and when they lapse ------------------------------------

const PACKAGE_TYPE_METRICS = {
  basic: "usageBasicPackageCredit",
  ondemand: "usageOnDemandPackageCredit",
  bonus: "usageBonusPackageCredit",
}

/**
 * Every credit batch the account holds, as `{ metric, amount, left, expiresAt }`
 * (`metric` the statistics bucket it counts in, `expiresAt` ms): the packages
 * from `package/credit/page`, and each campaign's grants from `ops/credit`.
 * Undefined when none of it could be read.
 */
async function fetchCreditBatches(cred, fetcher) {
  const batches = []
  let reached = false
  for (const query of PACKAGE_CREDIT_QUERIES) {
    const r = await snapRequest("POST", PACKAGE_CREDIT_PAGE_URL, cred, fetcher, query)
    const list = asRecord(r.data).list
    if (!r.ok || !Array.isArray(list)) continue
    reached = true
    for (const p of list) {
      const metric = PACKAGE_TYPE_METRICS[asRecord(p).packageType]
      if (!metric) continue
      const amount = num(p.creditAmount)
      batches.push({ metric, amount, left: Math.max(0, amount - num(p.creditUsed)), expiresAt: vendorDate(p.expiredTime) })
    }
  }
  const overview = await snapRequest("GET", OPS_CREDIT_OVERVIEW_URL, cred, fetcher)
  const campaigns = asRecord(overview.data).items
  if (overview.ok && Array.isArray(campaigns)) {
    reached = true
    for (const c of campaigns) {
      const left = num(c?.totalClaimed) - num(c?.totalConsumed) - num(c?.totalExpired)
      if (left <= 0 || c.campaignId === undefined || c.campaignId === null) continue
      const page = await snapRequest("GET", OPS_CREDIT_CAMPAIGN_URL(c.campaignId), cred, fetcher)
      for (const b of Array.isArray(asRecord(page.data).benefits) ? page.data.benefits : []) {
        const amount = num(b?.claimedAmount)
        batches.push({
          metric: PACKAGE_TYPE_METRICS.bonus,
          amount,
          left: Math.max(0, amount - num(b.consumedAmount) - num(b.expiredAmount)),
          expiresAt: vendorDate(b.expireAt),
        })
      }
    }
  }
  return reached ? batches : undefined
}

/**
 * When the soonest still-good batch lapses, and what lapses that Beijing day
 * (a sign-up grant and that day's check-in go a minute apart). Undefined when
 * nothing is left to lapse.
 */
function soonestLapse(batches, now = Date.now()) {
  const live = (batches ?? []).filter((b) => b.left > 0 && b.expiresAt > now)
  if (live.length === 0) return undefined
  const at = Math.min(...live.map((b) => b.expiresAt))
  const day = beijingDay(at)
  return { at, credit: live.filter((b) => beijingDay(b.expiresAt) === day).reduce((n, b) => n + b.left, 0) }
}

/** The plugin's own check-in ledger as batches, for when the vendor's can't be read. */
const grantBatches = (grants) =>
  (grants ?? []).map((g) => ({ metric: PACKAGE_TYPE_METRICS.bonus, amount: num(g.credit), left: num(g.credit), expiresAt: vendorDate(g.expiresAt) }))

// ---- the check-in ledger -----------------------------------------------------
//
// CodeArts tells how many credits a bucket holds, never when each given batch
// lapses; check-in credits are good for 30 days from their day. The plugin keeps
// the claims it made, per account, so the usage card can say when the soonest
// of them runs out. Best effort: a ledger that can't be read or written only
// leaves that line out.

const LEDGER_KEEP_DAYS = 40

const defaultLedgerPath = () =>
  process.env.MAGPIE_CODEARTS_LEDGER || path.join(os.homedir(), ".config", "magpie", "codearts-checkins.json")

function readLedger(file) {
  try {
    const raw = safeJson(fs.readFileSync(file, "utf8"))
    return raw && !Array.isArray(raw) ? raw : {}
  } catch {
    return {}
  }
}

/** The check-in credits the plugin claimed for `account`. */
function readGrants(file, account) {
  const list = readLedger(file)[account]
  return Array.isArray(list) ? list : []
}

function writeLedger(file, all) {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  const tmp = `${file}.${process.pid}.tmp`
  fs.writeFileSync(tmp, JSON.stringify(all, null, 2))
  fs.renameSync(tmp, file)
}

/** Keep one claim (one a Beijing day), dropping what lapsed long ago. */
function recordGrant(file, account, grant) {
  try {
    const all = readLedger(file)
    const keep = Date.now() - LEDGER_KEEP_DAYS * 86_400_000
    const day = beijingDay(vendorDate(grant.at))
    const list = (Array.isArray(all[account]) ? all[account] : [])
      .filter((g) => (vendorDate(g.expiresAt) ?? 0) > keep && beijingDay(vendorDate(g.at) ?? 0) !== day)
    all[account] = [...list, grant]
    writeLedger(file, all)
  } catch {
    /* best effort */
  }
}

// The claim this plugin made today whose confirm failed, per account: the
// next check-in confirms it, whichever magpie (or OpenCode) on this machine
// runs it. A claim the IDE leaves unconfirmed (`id: null`) is never kept
// here, so it is never confirmed. Kept for its Beijing day only.
const UNCONFIRMED = "_unconfirmed"

/** Today's claim this plugin left unconfirmed for `account`, if any. */
function readUnconfirmed(file, account) {
  const left = asRecord(asRecord(readLedger(file)[UNCONFIRMED])[account])
  return left.day === beijingDay() ? left : undefined
}

/** Keep the claim left unconfirmed for `account`, or with null drop it; other days' go. */
function recordUnconfirmed(file, account, left) {
  try {
    const all = readLedger(file)
    const today = beijingDay()
    const kept = Object.fromEntries(
      Object.entries(asRecord(all[UNCONFIRMED])).filter(([k, v]) => k !== account && asRecord(v).day === today),
    )
    if (left) kept[account] = { ...left, day: today }
    all[UNCONFIRMED] = kept
    writeLedger(file, all)
  } catch {
    /* best effort */
  }
}

/** When a claimed benefit lapses: the vendor's date when the claim says one, else 30 days on. */
function grantExpiry(benefit, at) {
  for (const k of ["expire_time", "expireTime", "expired_time", "expiredTime", "expire_at", "expireAt", "valid_end_time", "end_time", "endTime"]) {
    const v = vendorDate(benefit?.[k])
    if (v) return v
  }
  return at + CHECKIN_CREDIT_DAYS * 86_400_000
}

/**
 * Press the daily check-in, exactly as the IDE's 「限时福利 → 每日领取」 does:
 * list the activities, claim the `USER_LOGIN` one with `{campaignId, channel}`,
 * and confirm it when the claim answers an id (`benefit.id !== null`), or the
 * credits stay pending. Answers magpie's check-in outcome; never throws.
 * `unconfirmed` is today's claim this plugin left unconfirmed (the ledger's);
 * an answer's own `unconfirmed` is one to keep there.
 */
async function dailyCheckin(cred, fetcher, { unconfirmed } = {}) {
  const first = await dailyCampaign(cred, fetcher)
  if (first.error) return { outcome: "failed", message: first.error }
  const item = first.item
  if (!item) return { outcome: "inactive", message: "当前没有每日领取活动" }
  const credit = num(item.benefitAmount)
  // Claimed here, but the confirm failed: confirm it now, or the credits stay
  // pending. Only that claim: one the IDE leaves unconfirmed is `CLAIMED` as
  // well, and the IDE never confirms it. When the confirm fails, only the
  // activity tells whether it was confirmed after all; still claimed, it is a
  // failure magpie tries again later that day.
  const campaign = item.campaignId !== undefined && item.campaignId !== null && item.campaignId !== ""
  const leftHere = unconfirmed && String(unconfirmed.campaignId) === String(item.campaignId)
  if (!item.claimable && item.status === "CLAIMED" && campaign && leftHere) {
    const confirmed = await snapRequest("POST", OPS_CONFIRM_URL, cred, fetcher, { campaignId: item.campaignId })
    if (confirmed.ok) return claimedOutcome(asRecord(unconfirmed.benefit), credit)
    const again = await dailyCampaign(cred, fetcher)
    if (isClaimedToday(again.item) && again.item.status !== "CLAIMED") return { outcome: "done", message: "今天已领取" }
    return { outcome: "failed", message: `领取确认失败：${confirmed.message}` }
  }
  if (isClaimedToday(item)) return { outcome: "done", message: "今天已领取" }
  if (!item.claimable) return { outcome: "ineligible", message: `当前不可领取（status=${item.status ?? "null"}）` }
  if (!campaign) return { outcome: "failed", message: "活动缺少 campaignId" }

  const claimed = await snapRequest("POST", OPS_CLAIM_URL, cred, fetcher, { campaignId: item.campaignId, channel: OPS_CHANNEL })
  if (!claimed.ok) {
    // Claimed elsewhere a moment ago (the IDE, another magpie): look again.
    const again = await dailyCampaign(cred, fetcher)
    if (isClaimedToday(again.item)) return { outcome: "done", message: "今天已领取" }
    return { outcome: "failed", message: `领取失败：${claimed.message}` }
  }
  const benefit = asRecord(claimed.data)
  if (claimed.data && benefit.id !== null) {
    const confirmed = await snapRequest("POST", OPS_CONFIRM_URL, cred, fetcher, { campaignId: item.campaignId })
    if (!confirmed.ok) {
      return { outcome: "failed", message: `领取确认失败：${confirmed.message}`, unconfirmed: { campaignId: item.campaignId, benefit } }
    }
  }
  return claimedOutcome(benefit, credit)
}

/** The outcome of a check-in that went through, with the grant the ledger keeps. */
function claimedOutcome(benefit, credit) {
  const got = num(benefit.benefitAmount) || credit
  const at = Date.now()
  const grant = { at: new Date(at).toISOString(), credit: got, expiresAt: new Date(grantExpiry(benefit, at)).toISOString() }
  return { outcome: "claimed", ...(got > 0 ? { credit: got } : {}), message: got > 0 ? `+${got} 积分` : "已领取", grant }
}

// ---- model list & account info ---------------------------------------------

/**
 * One gateway/builtin entry as a model row. The id is the one published, never
 * the date-stripped one (a dated id and its bare form are two models with
 * opposite billing), so dedupe is by exact id too.
 */
function parseModelInfo(m, seen) {
  const id = m?.model_id
  if (typeof id !== "string" || !id) return undefined
  const base = normalizeModelId(id)
  // Hide vision models: small context, no tool calls — not agent main models.
  // Case-insensitive: the gateway lists some lowercase (`kimi-k2.6-vl`).
  if (/-vl(-|$)/i.test(base)) return undefined
  if (seen.has(id)) return undefined
  seen.add(id)
  const rawName = m.model_name
  const name = typeof rawName === "string" && rawName ? normalizeModelId(rawName) : prettify(base)
  const info = { id, name, context: num(m.context_window) || CONTEXT_WINDOWS[base] }
  const output = num(m.max_tokens)
  if (output > 0) info.output = Math.min(output, MAX_OUTPUT_TOKENS)
  return info
}

function extractArray(text, path) {
  const root = safeJson(text)
  if (!root) return undefined
  let value = root
  for (const key of path) {
    if (typeof value !== "object" || value === null) return undefined
    value = value[key]
  }
  return Array.isArray(value) ? value : undefined
}

/**
 * Claim today's free benefit (`POST /api/v1/benefit/claim`, an empty JSON
 * body), as the CodeArts agent does before it offers the free models.
 * Gives `{ ok, code, message }`; `network: true` when the vendor wasn't reached.
 */
async function claimBenefit(cred, fetcher) {
  const body = utf8("{}")
  try {
    const headers = await signFor(cred, "POST", BENEFIT_CLAIM_URL, body)
    headers.set("x-snap-traceid", randomHex(16))
    const res = await fetcher(BENEFIT_CLAIM_URL, { method: "POST", headers, body, redirect: "error", signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) })
    const data = safeJson(await res.text())
    const code = firstOf(String(data?.error_code ?? "")) || `HTTP ${res.status}`
    return { ok: code === BENEFIT_OK, code, message: firstOf(data?.error_msg, data?.message) }
  } catch (e) {
    return { ok: false, network: true, code: "", message: e?.message ?? String(e) }
  }
}

// A refused claim is asked again this long after, not on every listing.
const CLAIM_RETRY_MS = 60 * 60 * 1000

/**
 * Is today's free benefit the account's? Claimed once a Beijing day (one claim
 * at a time); a refusal is asked again an hour later, and a vendor that can't
 * be reached leaves the last answer standing. A claim that goes through starts
 * the day afresh: the models the last one had no benefit for are asked again.
 */
function ensureBenefitClaimed(cred, fetcher, state) {
  const day = beijingDay()
  const was = state.claim
  if (was?.day === day && (was.ok || Date.now() - was.at < CLAIM_RETRY_MS)) return Promise.resolve(was.ok)
  state.claiming ??= claimBenefit(cred, fetcher)
    .then((r) => {
      if (r.network) return was?.ok ?? false
      state.claim = { day, ok: r.ok, at: Date.now(), code: r.code, message: r.message }
      if (r.ok) state.noBenefit?.clear()
      return r.ok
    })
    .finally(() => { state.claiming = null })
  return state.claiming
}

/**
 * One model of the CodeAgent's detail: its id is `model_parameters.model_id`
 * (what a request names), with the credit rate and the parameters the agent
 * itself reads — `thinking_type` set means a reasoning model.
 */
function parseAgentModel(m) {
  const p = asRecord(m?.model_parameters)
  const id = firstOf(p.model_id, m?.model_id)
  if (!id) return undefined
  const output = num(p.max_tokens)
  return {
    id,
    name: firstOf(m.display_name, m.model_name),
    rate: creditRateOf(m.credit ?? p.credit),
    ...(p.thinking_type != null ? { reasoning: true } : {}),
    context: num(p.context_window),
    ...(output > 0 ? { output: Math.min(output, MAX_OUTPUT_TOKENS) } : {}),
    // Configured by a user or a tenant, not one of CodeArts' own.
    own: m.model_type === "third" || m.model_type === "custom" || m.is_custom_model === true,
    hidden: m.display_enabled === false || p.display_enabled === false,
  }
}

/**
 * The agents to ask for a model list, best first: the account's main agent
 * (`original_id` "build", else CodeAgent), the rest of its agents, then the
 * IDE's own id for the main agent.
 */
async function agentCandidates(cred, fetcher) {
  const root = safeJson(await signedGet(AGENT_LIST_URL, cred, fetcher, AGENT_CENTER_HEADERS) ?? "")
  const agents = (Array.isArray(root?.agents) ? root.agents : Array.isArray(root?.data?.agents) ? root.data.agents : [])
    .filter((a) => typeof a?.agent_id === "string" && a.agent_id)
  const rank = (a) => {
    const i = MAIN_AGENT_ORIGINAL_IDS.indexOf(firstOf(a.original_id) || a.agent_id)
    return i < 0 ? MAIN_AGENT_ORIGINAL_IDS.length : i
  }
  const ids = [...agents].sort((a, b) => rank(a) - rank(b)).map((a) => a.agent_id)
  return [...new Set([...ids, MAIN_AGENT_FALLBACK_ID])]
}

/**
 * The main agent's models (`agents/detail` → `gpts.models`), carrying each
 * model's credit rate. The agent that answered with a list is kept on `state`.
 */
async function fetchAgentModels(cred, fetcher, state) {
  const read = async (id) => {
    const root = safeJson(await signedGet(AGENT_DETAIL_URL(id), cred, fetcher, AGENT_CENTER_HEADERS) ?? "")
    const models = root?.gpts?.models ?? root?.data?.gpts?.models
    return Array.isArray(models) && models.length ? models.map(parseAgentModel).filter(Boolean) : undefined
  }
  if (state.agentId) {
    const known = await read(state.agentId)
    if (known) return known
    state.agentId = ""
  }
  for (const id of (await agentCandidates(cred, fetcher)).slice(0, AGENT_DETAIL_TRIES)) {
    const models = await read(id)
    if (models) {
      state.agentId = id
      return models
    }
  }
  return undefined
}

/**
 * Fetch the account's live model list and merge it:
 * - the free models gateway/config lists, but only once today's benefit is
 *   claimed for the account (else they are left out: they answer `benefit not
 *   found`, and the plain builtin model of the same id stays);
 * - the builtin models;
 * - the main agent's own list, which gives every model its credit rate and
 *   adds the ones the builtin list leaves out.
 * `state` is the account's own: its claim, agent id, free models
 * (`state.benefit`) and builtin ones (`state.regular`) are kept there and
 * nowhere else. Without one the free list is taken as it is published.
 */
async function fetchRemoteModels(cred, fetcher, state) {
  const models = []
  const seen = new Set()
  const benefitIds = []

  const gatewayText = await signedGet(OPENGW_GATEWAY_CONFIG_URL, cred, fetcher)
  const gateway = gatewayText === undefined ? undefined : extractArray(gatewayText, ["result", "models"])
  const claimed = gateway?.length ? (state ? await ensureBenefitClaimed(cred, fetcher, state) : true) : false
  for (const item of claimed ? gateway : []) {
    if (typeof item !== "object" || item === null) continue
    const mi = parseModelInfo(item, seen)
    if (!mi) continue
    mi.benefit = true
    benefitIds.push(mi.id)
    models.push(mi)
  }

  const snapText = await signedGet(SNAP_MODEL_BUILTIN_URL, cred, fetcher, {
    "Content-Type": "application/json",
    ...SNAP_EXTRA_HEADERS,
  })
  const builtin = snapText === undefined ? undefined : extractArray(snapText, ["builtinModels"])
  const regularIds = []
  for (const item of builtin ?? []) {
    if (typeof item !== "object" || item === null) continue
    if (typeof item.model_id === "string") regularIds.push(item.model_id)
    const mi = parseModelInfo(item, seen)
    if (mi) models.push(mi)
  }

  // Rates and parameters from the agent's own list; a model only it lists is
  // one more the account can call.
  const agent = await fetchAgentModels(cred, fetcher, state ?? {}).catch(() => undefined)
  for (const a of agent ?? []) {
    const mi = models.find((m) => m.id === a.id)
    if (mi) {
      if (a.rate > 0 && !mi.benefit) mi.rate = a.rate
      if (a.reasoning) mi.reasoning = true
      // The limits the agent itself runs the model with.
      if (a.context > 0) mi.context = a.context
      if (a.output) mi.output = a.output
    } else if (!a.own && !a.hidden && !/-vl(-|$)/i.test(normalizeModelId(a.id))) {
      models.push({
        id: a.id,
        name: a.name ? normalizeModelId(a.name) : prettify(normalizeModelId(a.id)),
        context: a.context || CONTEXT_WINDOWS[normalizeModelId(a.id)],
        ...(a.output ? { output: a.output } : {}),
        ...(a.rate > 0 ? { rate: a.rate } : {}),
        ...(a.reasoning ? { reasoning: true } : {}),
      })
      regularIds.push(a.id)
    }
  }

  // The account's own lists: an answered list is the truth, an empty one
  // included; a failed read keeps what was known.
  if (state) {
    if (gateway) state.benefit = new Set(benefitIds)
    if (builtin) state.regular = new Set(regularIds)
  }
  return models
}

/** Chinese labels for the credit metric buckets CodeArts reports. */
const CREDIT_METRIC_LABELS = {
  usageTotalPackageCredit: "总积分",
  usageBasicPackageCredit: "基础积分包",
  usageOnDemandPackageCredit: "按需积分包",
  usageBonusPackageCredit: "赠送积分包",
}
const TOTAL_CREDIT_METRIC = "usageTotalPackageCredit"

/** Compact credit counts for the display string (12345 → "1.2万"). */
function formatCredit(n) {
  const v = Math.max(0, Number.isFinite(n) ? n : 0)
  if (v >= 1e8) return `${(v / 1e8).toFixed(1)}亿`
  if (v >= 1e4) return `${(v / 1e4).toFixed(1)}万`
  return String(Math.round(v))
}

const BENEFIT_WINDOW_LABEL = "免费额度"

/**
 * A credit bucket as a usage window. magpie takes its routing cap from every
 * window it is given, so only the package total may pause the account: the
 * buckets are that same money broken down, and a spent 500-credit basic bucket
 * used to stop an account with 30 000 credits left elsewhere.
 */
function creditWindow(name, limit, remain, { resetsAt, expiring = 0 } = {}) {
  const used = Math.max(0, limit - remain)
  return {
    name,
    used: clamp(limit > 0 ? (used / limit) * 100 : 0),
    display: `${formatCredit(used)} / ${formatCredit(limit)}${expiring > 0 ? ` · ${formatCredit(expiring)} 即将过期` : ""}`,
    amount: used,
    limit,
    unit: "credits",
    ...(resetsAt ? { resetsAt } : {}),
    ...(name !== CREDIT_METRIC_LABELS[TOTAL_CREDIT_METRIC] ? { aside: true } : {}),
  }
}

// The buckets that are the month's allowance and come back at the period's
// end (the IDE's 「等待至 {resetDate} 额度重置」); bought and given credits run
// out on their own dates instead.
const MONTHLY_CREDIT_METRICS = ["usageTotalPackageCredit", "usageBasicPackageCredit"]

/**
 * The free daily token pool as a window, scoped to the models that draw on it.
 * `aside`: an empty free pool leaves the paid models usable.
 */
function benefitWindow(raw, ids = BENEFIT_MODEL_FALLBACK) {
  const rec = asRecord(asRecord(raw).result ?? asRecord(raw).data ?? raw)
  const limit = num(rec.daily_token_limit)
  if (limit <= 0) return undefined
  const used = Math.max(0, Math.min(num(rec.daily_tokens_used), limit))
  return {
    name: BENEFIT_WINDOW_LABEL,
    used: clamp((used / limit) * 100),
    display: `${formatCredit(used)} / ${formatCredit(limit)}`,
    amount: used,
    limit,
    unit: "tokens",
    // A daily pool: back at the next Beijing midnight.
    resetsAt: new Date(nextBeijingMidnight()).toISOString(),
    span: 86_400,
    aside: true,
    ...(ids.length ? { models: [...ids].sort() } : {}),
  }
}

/**
 * Map the account's `statistics/plugin` (+ free pool) answer to magpie's usage
 * shape. `batches` are its credit batches with their own expiry (see
 * fetchCreditBatches): the plan's batch gives the month's reset to the second,
 * and the soonest of the rest to lapse is each bucket's (and the total's)
 * `runsOutAt`/`runsOut`. Without them, `grants` (the check-ins the plugin
 * claimed, 30 days each) stand in, and the reset is `end_date`.
 */
function usageFromStatistics(raw, benefitRaw, { batches, grants, benefitIds } = {}) {
  let data = raw
  if (raw && typeof raw.code === "number") {
    if (raw.code !== 0) return { error: firstOf(raw.message, raw.msg, `CodeArts code ${raw.code}`), windows: [] }
    data = asRecord(raw.data)
  }
  if (!data || typeof data !== "object") return { error: "CodeArts could not be parsed", windows: [] }
  const pkg = asRecord(data.package)
  const plan = firstOf(pkg.package_name_cn, pkg.package_name_en, pkg.spec_code)
  const metrics = Array.isArray(data.metrics) ? data.metrics : []
  const now = Date.now()
  const known = batches ?? grantBatches(grants)
  // The plan's own batch ends when the month's allowance comes back.
  const planEnds = known.filter((b) => b.metric === PACKAGE_TYPE_METRICS.basic && b.expiresAt > now).map((b) => b.expiresAt)
  const reset = planEnds.length ? Math.min(...planEnds) : vendorDate(data.end_date)
  const resetsAt = reset ? new Date(reset).toISOString() : undefined

  const windows = []
  const metricOf = new Map()
  for (const item of metrics) {
    const rec = asRecord(item)
    const name = CREDIT_METRIC_LABELS[rec.name]
    if (!name) continue
    const limit = num(rec.package_credit_amount)
    const remain = num(rec.package_credit_remain)
    if (limit <= 0 && remain <= 0) continue // empty bucket, nothing to show
    const w = creditWindow(name, limit, remain, {
      resetsAt: MONTHLY_CREDIT_METRICS.includes(rec.name) ? resetsAt : undefined,
      expiring: num(rec.package_credit_expiring_amount),
    })
    metricOf.set(w, rec.name)
    windows.push(w)
  }

  // What lapses soonest, apart from the month's reset: a bucket's own
  // batches; for the total, every batch but the plan's.
  for (const w of windows) {
    const metric = metricOf.get(w)
    if (metric === PACKAGE_TYPE_METRICS.basic) continue
    const own = known.filter((b) => (metric === TOTAL_CREDIT_METRIC ? b.metric !== PACKAGE_TYPE_METRICS.basic : b.metric === metric))
    const lapse = soonestLapse(own, now)
    const left = Math.max(0, w.limit - w.amount)
    if (lapse && left > 0) {
      w.runsOutAt = new Date(lapse.at).toISOString()
      w.runsOut = Math.min(lapse.credit, left)
    }
  }

  // Keep the package total on top, then the per-bucket breakdown, then the
  // separate free pool.
  const totalLabel = CREDIT_METRIC_LABELS[TOTAL_CREDIT_METRIC]
  windows.sort((a, b) => (a.name === totalLabel ? -1 : b.name === totalLabel ? 1 : 0))

  const benefit = benefitRaw ? benefitWindow(benefitRaw, benefitIds) : undefined
  if (benefit) windows.push(benefit)

  if (windows.length > 0) return { plan: plan || undefined, signIn: "kept", windows }
  return { plan: plan || undefined, windows, error: "该账号无积分额度" }
}

// ---- the plugin -------------------------------------------------------------

export const CodeArtsAuthPlugin = async ({ client } = {}, options = {}) => {
  const fetcher = typeof options?.fetch === "function" ? options.fetch : fetch
  // Tunables (magpie: `magpie plugin options <name> '{…}'`). `maxInflight` can
  // only lower the gate: above 3 the account would pass the vendor's cap.
  const maxInflight = Number.isInteger(options?.maxInflight) && options.maxInflight > 0
    ? Math.min(options.maxInflight, ACCOUNT_MAX_INFLIGHT)
    : ACCOUNT_MAX_INFLIGHT
  const retryBudgetMs = Number.isFinite(options?.retryBudgetMs) ? Math.max(0, options.retryBudgetMs) : SESSION_RETRY_BUDGET_MS
  const retryDelays = Array.isArray(options?.retryDelays) && options.retryDelays.length ? options.retryDelays : SESSION_RETRY_DELAYS_MS
  const queueBudgetMs = Number.isFinite(options?.queueBudgetMs) ? Math.max(0, options.queueBudgetMs) : QUEUE_BUDGET_MS
  const thinking = options?.thinking !== false
  const ledgerPath = typeof options?.ledgerPath === "string" && options.ledgerPath ? options.ledgerPath : defaultLedgerPath()
  // Test hooks: the clock and the sleep the retry budget runs on.
  const now = typeof options?.now === "function" ? options.now : Date.now
  const sleep = typeof options?.sleep === "function" ? options.sleep : delay

  // Per account, shared by every loader this process runs for it and by
  // nothing else: the gate; its free models today (null until its
  // gateway/config was read; the fallback stands until then), its builtin
  // ones, and the ones the gateway said it has no benefit for; today's
  // benefit claim; its main agent's id; its credit batches; the renewal of its
  // credential under way, and the last one made; today's check-in claim whose
  // confirm failed.
  const accounts = new Map()
  const accountState = (key) => {
    if (!accounts.has(key)) {
      accounts.set(key, {
        gate: createConcurrencyGate(maxInflight), benefit: null, regular: null, noBenefit: new Set(),
        claim: null, claiming: null, agentId: "", batches: undefined, batchesAt: 0,
        renewing: null, renewed: null, unconfirmed: null,
      })
    }
    return accounts.get(key)
  }
  const stateOf = async (cred) => accountState((await accountSessionIds(cred)).sessionId)

  // One renewal per refresh token, whoever asks for it (each of the account's
  // loaders, magpie's auth.refresh): STS spends a refresh token once, so a
  // second use of it would turn the account away. Whoever comes later with
  // the same token gets what that renewal gave.
  const renewOnce = async (cred) => {
    const token = firstOf(cred.refresh_token)
    if (!token) return refreshCredential(cred, fetcher)
    const state = await stateOf(cred)
    if (state.renewed?.from === token) return state.renewed.next
    if (state.renewing?.from !== token) {
      const promise = refreshCredential(cred, fetcher)
        .then((next) => {
          state.renewed = { from: token, next }
          return next
        })
        .finally(() => {
          if (state.renewing?.promise === promise) state.renewing = null
        })
      state.renewing = { from: token, promise }
    }
    return state.renewing.promise
  }
  // The ledger's key: the signer, which outlives a sign-in; else the session.
  const ledgerKey = async (cred) => accountIdentityOf(cred).id || (await accountSessionIds(cred)).sessionId

  const configModel = (m) => ({
    name: m.name ?? m.id,
    // Declared so a caller never sends its own, larger default.
    limit: { context: m.context ?? 0, output: MAX_OUTPUT_TOKENS },
    tool_call: true,
  })

  const modelOf = (provider, live, state) => {
    const base = provider.models?.[live.id] ?? {}
    const capabilities = {
      temperature: true, toolcall: true, attachment: false, input: { text: true }, output: { text: true },
      ...(base.capabilities ?? {}),
      ...(live.reasoning ? { reasoning: true } : {}),
    }
    // magpie's own fields: `free` (from the free daily pool, the same answer
    // the signed `maas_type: benefit` header follows, so a model never shows
    // as free while billed as paid) and `rate` (the credits a request costs,
    // as a multiple: the IDE's 「消耗系数」).
    const free = live.benefit === true && !state?.noBenefit.has(live.id)
    return {
      ...base,
      id: live.id,
      name: live.name || base.name || live.id,
      limit: { context: live.context ?? base.limit?.context ?? 0, output: live.output ?? MAX_OUTPUT_TOKENS },
      capabilities,
      free,
      ...(!free && live.rate > 0 ? { rate: live.rate } : {}),
    }
  }

  return {
    // The provider and the models it has before anyone is signed in.
    config: async (config) => {
      config.provider ??= {}
      config.provider[PROVIDER] ??= {}
      const p = config.provider[PROVIDER]
      p.name ??= "CodeArts"
      p.npm ??= NPM
      p.api ??= CHAT_API_BASE
      p.models = { ...Object.fromEntries(CATALOG.map((m) => [m.id, configModel(m)])), ...(p.models ?? {}) }
    },

    // The account's own list when signed in and CodeArts answers.
    provider: {
      id: PROVIDER,
      async models(provider, { auth } = {}) {
        const cred = credentialOf(auth)
        if (!cred?.access_key_id) return provider.models
        try {
          const state = await stateOf(cred)
          const list = await fetchRemoteModels(cred, fetcher, state)
          if (!list.length) return provider.models
          return Object.fromEntries(list.map((m) => [m.id, modelOf(provider, m, state)]))
        } catch {
          return provider.models
        }
      },
    },

    auth: {
      provider: PROVIDER,
      refreshLead: REFRESH_LEAD_MS,
      // The gateway's own cap: more in flight only queues in the plugin.
      maxConcurrency: maxInflight,

      // magpie's own hook: renew the temporary credentials before they expire.
      async refresh(auth) {
        const cred = credentialOf(auth)
        if (!cred) return undefined
        const next = await renewOnce(cred)
        const accountId = auth.accountId ? "" : accountIdentityOf(next).id
        return {
          access: JSON.stringify(next),
          refresh: next.refresh_token,
          expires: expiresFromCredential(next),
          ...(accountId ? { accountId } : {}),
        }
      },

      // Every request the account makes is signed here.
      async loader(getAuth) {
        let account = await getAuth()
        let cred = credentialOf(account)
        if (!cred?.access_key_id || !cred?.secret_access_key) return {}

        // magpie renews ahead of expiry by itself; this covers hosts that
        // ignore `auth.refresh` (OpenCode) and a credential that went stale
        // anyway (a 401 / APIG.0602). One renewal at a time, shared with the
        // account's other loaders and auth.refresh; null when it fails.
        let refreshing = null
        let signInDead = false
        // Another renewal may have landed already (magpie's own, or another
        // process's): the stored one, when it is newer than `than`.
        const takeStored = async (than) => {
          const latest = await getAuth().catch(() => null)
          const stored = credentialOf(latest)
          if (!stored || !than(stored)) return { latest, stored: null }
          cred = stored
          account = latest
          return { latest, stored }
        }
        const renewCredential = (stale) => {
          if (signInDead) return Promise.resolve(null)
          refreshing ??= (async () => {
            const first = await takeStored((s) => s.access_key_id !== stale.access_key_id && !nearExpiry(s))
            if (first.stored) return first.stored
            const base = credentialOf(first.latest) ?? cred
            try {
              const next = await renewOnce(base)
              cred = next
              account = await persistCredential(client, first.latest ?? account, next)
              return next
            } catch (e) {
              if (!isPermanentRefreshFailure(e)) return null
              // The token was spent a moment ago by someone else: theirs stands.
              const after = await takeStored((s) => !!s.refresh_token && s.refresh_token !== base.refresh_token)
              if (after.stored) return after.stored
              signInDead = true
              return null
            }
          })()
            .catch(() => null)
            .finally(() => { refreshing = null })
          return refreshing
        }
        const current = async () => {
          if (signInDead || !nearExpiry(cred)) return cred
          return (await renewCredential(cred)) ?? cred
        }

        // One account = one upstream session and one in-flight budget.
        const ids = await accountSessionIds(cred)
        const state = accountState(ids.sessionId)
        const context = {
          ...ids,
          gate: state.gate,
          // The account's own model lists, read at each request (the models
          // hook keeps them current).
          state,
          now,
          sleep,
          fetcher,
          retryBudgetMs,
          retryDelays,
          queueBudgetMs,
          thinking,
          renewCredential,
          signInDead: () => signInDead,
        }
        return {
          baseURL: CHAT_API_BASE,
          async fetch(input, init = {}) {
            const c = await current()
            return signedFetch(c, input, init, context)
          },
        }
      },

      // magpie's own hook: how much of the plan is left.
      async usage(getAuth) {
        const auth = await getAuth()
        const cred = credentialOf(auth)
        if (!cred?.access_key_id) return { error: "not signed in", windows: [] }
        try {
          const text = await signedGet(SNAP_STATISTICS_URL, cred, fetcher, SNAP_EXTRA_HEADERS)
          if (text === undefined) return { error: "CodeArts answered an error", windows: [] }
          const state = await stateOf(cred)
          // The free pool only once today's benefit is the account's; best
          // effort either way: a credit account without one is normal.
          const free = state.benefit === null || state.benefit.size > 0
          const benefitText = free ? await signedGet(BENEFIT_BALANCE_URL, cred, fetcher) : undefined
          // Each batch's own expiry, read again every ten minutes (and after a
          // check-in); a failed read keeps the last one, else the ledger stands in.
          if (!state.batchesAt || Date.now() - state.batchesAt > CREDIT_BATCHES_TTL_MS) {
            const read = await fetchCreditBatches(cred, fetcher).catch(() => undefined)
            if (read) state.batches = read
            state.batchesAt = Date.now()
          }
          const usage = usageFromStatistics(safeJson(text), benefitText === undefined ? undefined : safeJson(benefitText), {
            batches: state.batches,
            grants: readGrants(ledgerPath, await ledgerKey(cred)),
            benefitIds: state.benefit ? [...state.benefit] : BENEFIT_MODEL_FALLBACK,
          })
          const user = accountIdentityOf(cred).name
          return user ? { ...usage, user } : usage
        } catch (e) {
          return { error: e?.message ?? String(e), windows: [] }
        }
      },

      // magpie's own hook: the daily 「每日领取」 check-in (OpenCode ignores it).
      // magpie renews the sign-in before asking, and asks once a Beijing day.
      async checkin(getAuth) {
        const cred = credentialOf(await getAuth())
        if (!cred?.access_key_id) return { outcome: "failed", message: "not signed in" }
        const key = await ledgerKey(cred)
        const state = await stateOf(cred)
        const today = beijingDay()
        // The account's own memory first: the ledger is written best effort,
        // and a write that failed mustn't lose the claim; the ledger carries it
        // to another process, or past a restart.
        const held = state.unconfirmed?.day === today ? state.unconfirmed : undefined
        const left = held ?? readUnconfirmed(ledgerPath, key)
        const { grant, unconfirmed, ...r } = await dailyCheckin(cred, fetcher, { unconfirmed: left })
        // A claim whose confirm failed is kept for the next check-in; one
        // confirmed now, or found done, is dropped.
        if (unconfirmed) {
          state.unconfirmed = { ...unconfirmed, day: today }
          recordUnconfirmed(ledgerPath, key, unconfirmed)
        } else if (left && r.outcome !== "failed") {
          state.unconfirmed = null
          recordUnconfirmed(ledgerPath, key, null)
        }
        if (r.outcome === "claimed") {
          // The new batch shows at the next usage read; the ledger keeps it in
          // case the vendor's batch list can't be read.
          state.batchesAt = 0
          if (grant) recordGrant(ledgerPath, key, grant)
        }
        return r
      },

      methods: [
        {
          type: "oauth",
          label: "Sign in with Huawei Cloud",
          async authorize() {
            const flow = await startOAuthFlow(fetcher)
            return {
              url: flow.loginUrl,
              instructions: "Sign in to Huawei Cloud in the browser; this window closes itself when done.",
              method: "auto",
              async callback() {
                try {
                  const outcome = await flow.result
                  const cred = safeJson(outcome.access)
                  if (!cred?.access_key_id) return { type: "failed", error: "CodeArts did not return a credential" }
                  // Who signed in, so signing in again replaces the account
                  // instead of adding a `codearts#xxxxxx` beside it.
                  const accountId = accountIdentityOf(cred).id
                  return {
                    type: "success",
                    access: outcome.access,
                    refresh: cred.refresh_token,
                    expires: outcome.expires,
                    ...(accountId ? { accountId } : {}),
                  }
                } catch (e) {
                  return { type: "failed", error: e?.message ?? String(e) }
                } finally {
                  await flow.close()
                }
              },
            }
          },
        },
      ],
    },
  }
}

/** Is the credential this close to its expiry (unknown expiry: no)? */
function nearExpiry(cred) {
  const at = Date.parse(cred?.expires_at || "")
  return Number.isFinite(at) && at - Date.now() <= INBAND_REFRESH_LEAD_MS
}

/**
 * Best-effort: store a freshly refreshed credential back on the account, and
 * give the record as stored. The host keeps what it is given whole (nothing is
 * merged), so the account's other fields — its accountId — go along.
 */
async function persistCredential(client, account, cred) {
  const accountId = firstOf(account?.accountId, accountIdentityOf(cred).id)
  const body = {
    ...asRecord(account),
    type: "oauth",
    access: JSON.stringify(cred),
    refresh: cred.refresh_token,
    expires: expiresFromCredential(cred),
    ...(accountId ? { accountId } : {}),
  }
  try {
    await client?.auth?.set?.({ path: { id: PROVIDER }, body })
  } catch {
    /* best effort */
  }
  return body
}

// for tests (never a plugin: magpie only calls exported functions)
export const _internal = {
  PROVIDER, NPM, CATALOG, CONTEXT_WINDOWS, BENEFIT_MODEL_FALLBACK, MAX_OUTPUT_TOKENS,
  CHAT_API_BASE, SNAP_MODEL_BUILTIN_URL, SNAP_STATISTICS_URL, OPENGW_GATEWAY_CONFIG_URL, BENEFIT_BALANCE_URL,
  OPS_DELIVERY_URL, OPS_CLAIM_URL, OPS_CONFIRM_URL, SESSION_LIMIT_ERROR, ACCOUNT_MAX_INFLIGHT,
  PORTAL_AUTHORIZE_BASE, STS_TOKEN_ENDPOINT, CLIENT_ID, REDIRECT_PATH,
  firstOf, asRecord, safeJson, num, clamp, prettify, normalizeModelId, upstreamModelId, formatCredit,
  b64url, utf8, sha256Hex, hmacSha256Hex,
  generatePkcePair, generateDpopKeyPair, keyPairFromStoredJwk, signDpopJws,
  buildCanonicalRequest, signRequestHuawei,
  credentialFromTokenResponse, credentialOf, expiresFromCredential, accountIdentityOf, accountSessionIds,
  requestToken, exchangeAuthorizationCode, exchangeRefreshToken, refreshCredential,
  isDeadRefreshToken, isPermanentRefreshFailure, nearExpiry, persistCredential,
  buildOAuthLoginUrl, buildPortalLoginResultUrl,
  startOAuthCallbackServer, listenOnCallbackPort, startOAuthFlow, isSignedHostUrl, hostOf, SIGNED_HOSTS, signFor, traceQueueCall,
  BENEFIT_CLAIM_URL, AGENT_LIST_URL, AGENT_DETAIL_URL, CODE_AGENT_ID,
  canonicalQuery, beijingDay, nextBeijingMidnight, vendorDate, creditRateOf,
  claimBenefit, ensureBenefitClaimed, parseAgentModel, fetchAgentModels,
  readGrants, recordGrant, readUnconfirmed, recordUnconfirmed, grantExpiry, grantBatches, soonestLapse, fetchCreditBatches,
  PACKAGE_CREDIT_PAGE_URL, OPS_CREDIT_OVERVIEW_URL, OPS_CREDIT_CAMPAIGN_URL,
  toBytes, isBenefitModel, prepareChatBody, signedFetch, signedGet,
  parseSseEvents, errorCodeOf, errorMessageOf, isStaleCredential, isRateLimitCode, embeddedErrorStatus, classifyHead,
  peekAnswer, completionFromChunks, createConcurrencyGate, delay, retryDelayMs,
  isRegularModel, queueInfoOf, waitInQueue, TRACE_QUEUE_URL, SESSION_HEADER,
  snapRequest, dailyCheckin,
  parseModelInfo, extractArray, fetchRemoteModels, creditWindow, benefitWindow, usageFromStatistics,
}
