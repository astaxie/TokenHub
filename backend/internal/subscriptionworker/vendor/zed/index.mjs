// Zed subscriptions (Zed Pro, its trial, a student or business plan) for
// OpenCode and magpie: the models Zed hosts — Anthropic's, OpenAI's,
// Google's and xAI's — asked through cloud.zed.dev, as the Zed editor asks
// them. Ported from magpie's built-in Zed account (internal/zed,
// internal/provider/zed*.go, internal/gateway/zed.go), which was read from
// Zed's source (github.com/zed-industries/zed, 1.23.0):
//
//   - Sign-in: an RSA-2048 key is made, a port of 127.0.0.1 listened on, and
//     zed.dev/native_app_signin opened with the port and the key's public
//     half. The browser comes back to the port with user_id and an
//     access_token encrypted to the key. The pair is sent as
//     "Authorization: <user_id> <access_token>" to cloud.zed.dev/client/*
//     and lasts until a 401.
//   - POST /client/llm_tokens trades the pair for a short-lived model token,
//     minted again on a 401 or an x-zed-expired-token / x-zed-outdated-token
//     reply.
//   - GET /models lists what the account may call; POST /completions takes
//     {provider, model, provider_request} — the provider's own request — and
//     streams newline-delimited JSON, each line {"event": <the provider's own
//     stream event>} or a status.
//
// Each model is declared on the AI SDK package of its own provider, so the
// request the SDK writes is already the provider_request Zed wants; the
// fetch below wraps it, and turns Zed's lines back into that provider's own
// server-sent events.

import crypto from "node:crypto"
import http from "node:http"

const PROVIDER = "zed"

const SITE = "https://zed.dev"
const CLOUD = "https://cloud.zed.dev"
// the Zed release the requests say they come from
const VERSION = "1.23.0"
const SUCCEEDED = SITE + "/native_app_signin_succeeded"
// the base URL the AI SDK is given; the fetch sends every request to
// CLOUD/completions, whatever path the SDK adds
const BASE = CLOUD + "/v1"
// how long a sign-in waits for the browser (magpie: 10 minutes)
const SIGN_IN_TIMEOUT = 10 * 60 * 1000
// how long the account's model list is kept
const MODELS_TTL = 10 * 60 * 1000

// The API each of Zed's providers is asked in, as the AI SDK package that
// speaks it.
const NPM = {
  anthropic: "@ai-sdk/anthropic",
  open_ai: "@ai-sdk/openai",
  x_ai: "@ai-sdk/openai-compatible",
  google: "@ai-sdk/google",
}

// What is declared before the account's own list is read (it replaces
// these once signed in): Zed's hosted families, one model each.
const MODELS = [
  { provider: "anthropic", id: "claude-sonnet-5", display_name: "Claude Sonnet 5", max_token_count: 1000000, max_output_tokens: 64000, supports_images: true, supports_thinking: true },
  { provider: "anthropic", id: "claude-opus-5-5", display_name: "Claude Opus 5.5", max_token_count: 1000000, max_output_tokens: 128000, supports_images: true, supports_thinking: true },
  { provider: "anthropic", id: "claude-haiku-4-5", display_name: "Claude Haiku 4.5", max_token_count: 200000, max_output_tokens: 64000, supports_images: true, supports_thinking: true },
  { provider: "open_ai", id: "gpt-5.5", display_name: "GPT-5.5", max_token_count: 400000, max_output_tokens: 128000, supports_images: true, supports_thinking: true },
  { provider: "google", id: "gemini-3.5-flash", display_name: "Gemini 3.5 Flash", max_token_count: 1048576, max_output_tokens: 65536, supports_images: true, supports_thinking: true },
  { provider: "x_ai", id: "grok-4.7", display_name: "Grok 4.7", max_token_count: 500000, max_output_tokens: 64000, supports_images: true, supports_thinking: true },
]

// userAgent is Zed's: Zed/<version> (<os>; <arch>), in Rust's names.
function userAgent() {
  const os = { darwin: "macos", win32: "windows" }[process.platform] ?? process.platform
  const arch = { arm64: "aarch64", x64: "x86_64" }[process.arch] ?? process.arch
  return `Zed/${VERSION} (${os}; ${arch})`
}

// ---- errors -------------------------------------------------------------------

class ZedStatus extends Error {
  constructor(status, body, headers) {
    super(`Zed: ${failure(status, body)} (${status})`)
    this.status = status
    this.body = body
    this.headers = headers
  }
}

// SignInExpired is Zed refusing the account's own sign-in (a 401 for its
// model token): the one failure the built-in marked the account lapsed for
// (zedLapse).
class SignInExpired extends Error {}

const parse = (s) => {
  try {
    return JSON.parse(s)
  } catch {
    return undefined
  }
}

// failure is the message in an error body: {code, message}, else the body
// itself, else the status's name.
function failure(status, body) {
  const j = parse(body)
  if (j && typeof j === "object") {
    if (j.message) return String(j.message)
    if (typeof j.error === "string" && j.error) return j.error
    if (j.code) return String(j.code)
  }
  const s = String(body ?? "").trim()
  if (s && s.length < 500) return s
  if (status === 402) return "payment required: this Zed account has no plan that calls models, or its allowance is used up"
  return http.STATUS_CODES[status] ?? String(status)
}

// failedStatus is the HTTP status a failure's code stands for:
// upstream_http_<n> or http_<n>, else what the code names.
function failedStatus(code) {
  code = String(code ?? "")
  for (const p of ["upstream_http_", "http_"]) {
    if (code.startsWith(p)) {
      const n = Number(code.slice(p.length))
      if (Number.isInteger(n)) return n
    }
  }
  if (code.includes("rate_limit")) return 429
  if (code.includes("overloaded")) return 529
  if (code.includes("billing") || code.includes("payment")) return 402
  if (code.includes("context_length")) return 400
  return 502
}

// refusal is the status and message for a request Zed turned away: its
// {code, message, upstream_status}, a 402 as the plan it is.
function refusal(status, body) {
  if (status === 402) return [402, "payment required — this account's plan doesn't include Zed's hosted models, or its allowance is used up (see zed.dev/account)"]
  // a model token refused right after it was minted: the account's own
  // sign-in just worked, so the built-in answered 401 without marking it
  // (errorResponse says kept)
  if (status === 401) return [401, "the sign-in was refused — sign in again"]
  const j = parse(body) ?? {}
  let code = status
  const up = Number(j.upstream_status)
  if (up >= 400 && up <= 599) code = up
  else if (j.code) {
    const n = failedStatus(j.code)
    if (n !== 502) code = n
  }
  if (code < 400 || code > 599) code = 502
  let msg = failure(status, body)
  if (Number(j.retry_after) > 0) msg += ` (retry after ${Number(j.retry_after)}s)`
  return [code, msg]
}

// stale says a model request was turned away for its model token, which a
// new one would fix.
const stale = (status, headers) => status === 401 || !!headers.get("x-zed-expired-token") || !!headers.get("x-zed-outdated-token")

// ---- the account --------------------------------------------------------------

// cloud sends one /client/* request with the account's pair.
async function cloud(method, path, s, body, timeout = 20_000) {
  const headers = { Authorization: `${s.userId} ${s.access}`, "Content-Type": "application/json", "User-Agent": userAgent() }
  if (s.systemId) headers["x-zed-system-id"] = s.systemId
  const res = await fetch(CLOUD + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(timeout),
  })
  const text = await res.text()
  if (!res.ok) throw new ZedStatus(res.status, text, res.headers)
  return text
}

// who names the account: its GitHub login, its username, its name, its id.
function who(me) {
  for (const s of [me.user?.github_login, me.user?.username, me.user?.name]) {
    if (typeof s === "string" && s.trim()) return s.trim()
  }
  if (me.user?.legacy_user_id) return String(me.user.legacy_user_id)
  return "Zed account"
}

// orgOf is the organization the account's models are asked under. Zed's
// editor starts in the default organization, else the first, and asks for
// its model token with whichever one is current (cloud.rs auth_context);
// its title bar lets the user switch away from one without a subscription,
// and plans_by_organization names each organization's plan (an
// organization it leaves out has none: user.rs
// current_organization_has_no_active_subscription). The plugin has no such
// switcher, so it picks as a user would (#232, ReneXiong): the
// organization it saved, while that one still has a plan that calls
// models; else the default, if it has one; else such an organization, the
// personal one first. When none has, it keeps what Zed would pick: the one
// saved, else the default, else the first.
function orgOf(me, saved = "") {
  const orgs = (me.organizations ?? []).filter((o) => o?.id)
  const usable = (o) => !!o && paid(me.plans_by_organization?.[o.id]) && !modelsOff(me, o.id)
  const keep = orgs.find((o) => o.id === saved)
  const def = orgs.find((o) => o.id === me.default_organization_id)
  const pick = [keep, def].find(usable) ?? orgs.find((o) => o.is_personal && usable(o)) ?? orgs.find(usable) ?? keep ?? def ?? orgs[0]
  if (pick) return pick.id
  return saved || me.default_organization_id || ""
}

// paid says a plan calls Zed's hosted models: any plan but none and Free.
const paid = (plan) => typeof plan === "string" && plan !== "" && plan !== "zed_free"

// planOf is the plan of the organization the models are asked under, as
// Zed's editor reads it (user.rs plan): that organization's entry, none
// when it has none, never the account's own plan in its place. Only a
// reply with no organizations at all falls back to the account's plan.
function planOf(me, org) {
  const known = org && (me.organizations ?? []).some((o) => o?.id === org) && me.plans_by_organization && typeof me.plans_by_organization === "object"
  if (known) return me.plans_by_organization[org] || ""
  return me.plan?.plan_v3 || ""
}

function modelsOff(me, org) {
  const c = me.configuration_by_organization?.[org]
  return !!c && c.is_zed_model_provider_enabled === false
}

// planName is a plan's id as a reader would name it.
function planName(id) {
  const names = { zed_pro: "Pro", zed_pro_trial: "Pro Trial", zed_business: "Business", zed_student: "Student", zed_vip: "VIP", zed_free: "Free", "": "Free" }
  return names[id ?? ""] ?? String(id).replace(/^zed_/, "")
}

// The sign-in is kept as OpenCode's oauth entry: access is the account's
// access token, refresh the rest of what the calls need (Zed has no refresh
// token: the pair lasts until Zed refuses it), accountId its name.
function stateOf(auth) {
  if (auth?.type !== "oauth" || !auth.access) return null
  const r = parse(auth.refresh ?? "")
  if (!r?.userId) return null
  const s = { access: auth.access, userId: String(r.userId), systemId: r.systemId ?? "", org: r.org ?? "", saved: r.org ?? "", plan: r.plan ?? "", who: auth.accountId ?? "", auth }
  // an organization picked again since this sign-in was saved, in case the
  // save didn't reach it
  const p = picked.get(s.userId)
  if (p && p.from === s.org) s.org = p.to
  return s
}

// picked is each account's organization picked again after the saved one
// lost its plan: {from: the one its sign-in names, to: the new one}. Once
// the sign-in is saved with the new one, from no longer matches.
const picked = new Map()

// withOrg is a sign-in with its organization and plan replaced.
function withOrg(auth, org, plan) {
  const r = parse(auth.refresh ?? "") ?? {}
  return { ...auth, refresh: JSON.stringify({ ...r, org, plan, planName: planName(plan) }) }
}

// repick reads the account again after Zed refused its organization (a
// 402: no active subscription) and moves it to one with a plan, if there
// is one. It says whether the organization changed; s is moved, and the
// sign-in saved with it when save is given.
async function repick(s) {
  let me
  try {
    me = parse(await cloud("GET", "/client/users/me", s, undefined, 15_000))
  } catch {
    return false
  }
  if (!me || typeof me !== "object") return false
  const org = orgOf(me, s.org)
  if (!org || org === s.org) return false
  move(s, org, planOf(me, org))
  return true
}

// move puts s under org, remembers it and saves it with the sign-in.
function move(s, org, plan) {
  picked.set(s.userId, { from: s.saved ?? "", to: org })
  s.org = org
  s.plan = plan
  if (s.save && s.auth) Promise.resolve(s.save(withOrg(s.auth, org, plan))).catch(() => {})
}

// ---- the model token ------------------------------------------------------------

// tokens keeps each account's model token in memory: Zed mints them for an
// hour or so and says when one is stale, so none is written down.
const tokens = new Map()
const minting = new Map()

// modelToken is a live model token for s, minted anew when renew is set
// (the last one was refused) or none is kept.
async function modelToken(s, renew) {
  // a token is minted for one organization
  const key = s.userId + " " + s.org + " " + s.access
  if (!renew && tokens.has(key)) return tokens.get(key)
  if (minting.has(key)) return minting.get(key)
  const run = (async () => {
    try {
      const text = await cloud("POST", "/client/llm_tokens", s, { organization_id: s.org })
      const tok = parse(text)?.token
      if (!tok) throw new Error("Zed: no model token in the reply")
      tokens.set(key, tok)
      return tok
    } catch (e) {
      tokens.delete(key)
      if (e instanceof ZedStatus && e.status === 401) throw new SignInExpired(`${s.who || "the account"}: the Zed sign-in has expired — sign in again`)
      throw e
    }
  })()
  minting.set(key, run)
  try {
    return await run
  } finally {
    minting.delete(key)
  }
}

// ---- models -----------------------------------------------------------------------

// vendors is each model's provider as the last list said, so a request
// goes out in its own provider's API.
const vendors = new Map()
const lists = new Map()

// guess is the provider of a model id no list names.
function guess(model) {
  const m = String(model).toLowerCase()
  if (m.startsWith("claude")) return "anthropic"
  if (m.startsWith("gemini")) return "google"
  if (m.startsWith("grok")) return "x_ai"
  return "open_ai"
}

const vendorOf = (model) => vendors.get(model) ?? guess(model)

// fetchModels reads GET /models, minting the token again once if it is
// stale, and moving the account once to an organization with a plan when
// Zed refuses the one it is under (402) or lists nothing callable for it.
async function fetchModels(s) {
  let moved = false
  for (let t = 0; ; t++) {
    let out
    try {
      const tok = await modelToken(s, t > 0)
      const res = await fetch(CLOUD + "/models", {
        headers: { Authorization: "Bearer " + tok, "x-zed-client-supports-x-ai": "true", "User-Agent": userAgent() },
        signal: AbortSignal.timeout(15_000),
      })
      const text = await res.text()
      if (!res.ok && t === 0 && stale(res.status, res.headers)) continue
      if (!res.ok) throw new ZedStatus(res.status, text, res.headers)
      out = parseModels(text)
    } catch (e) {
      const refused = (e instanceof ZedStatus && e.status === 402) || e instanceof NothingListed
      if (refused && !moved) {
        moved = true
        if (await repick(s)) continue
      }
      throw e
    }
    return out
  }
}

class NothingListed extends Error {}

// parseModels is what /models lists that can be called, the disabled ones
// and those of providers the plugin can't write left out.
function parseModels(text) {
  const r = parse(text)
  const out = (r?.models ?? []).filter((m) => m && m.id && !m.is_disabled && NPM[m.provider])
  if (!out.length) throw new NothingListed("Zed lists no models for this account — its plan may not include Zed's hosted models (see zed.dev/account)")
  for (const m of out) vendors.set(m.id, m.provider)
  return out
}

// variant is what the AI SDK package a model is on is given for an effort.
function variant(provider, effort) {
  if (provider === "anthropic") return { effort }
  if (provider === "google") return { thinkingConfig: { includeThoughts: true, thinkingLevel: effort } }
  return { reasoningEffort: effort }
}

const effortsOf = (m) => (m.supported_effort_levels ?? []).map((e) => e?.value).filter(Boolean)

// entry is a model as OpenCode's config declares it.
function entry(m) {
  const img = !!m.supports_images
  return {
    id: m.id,
    name: m.display_name || m.id,
    provider: { npm: NPM[m.provider], api: BASE },
    reasoning: !!m.supports_thinking || effortsOf(m).length > 0,
    attachment: img,
    tool_call: true,
    temperature: true,
    modalities: { input: img ? ["text", "image"] : ["text"], output: ["text"] },
    cost: { input: 0, output: 0 },
    limit: { context: m.max_token_count || 0, output: m.max_output_tokens || 0 },
    variants: Object.fromEntries(effortsOf(m).map((e) => [e, variant(m.provider, e)])),
  }
}

// model is a model as OpenCode's provider hands it to provider.models.
function model(m) {
  const e = entry(m)
  const img = !!m.supports_images
  return {
    id: m.id,
    providerID: PROVIDER,
    name: e.name,
    api: { id: m.id, url: BASE, npm: NPM[m.provider] },
    status: "active",
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: e.limit,
    options: {},
    headers: {},
    capabilities: {
      temperature: true,
      reasoning: e.reasoning,
      attachment: img,
      toolcall: true,
      input: { text: true, image: img, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: e.variants,
  }
}

// ---- sign-in -----------------------------------------------------------------------

// padded is URL-safe base64 with its padding, as Rust's URL_SAFE writes it.
const padded = (b) => {
  const s = b.toString("base64url")
  return s + "=".repeat((4 - (s.length % 4)) % 4)
}

// decrypt reads the access token the callback carried: RSA-OAEP SHA-256,
// PKCS#1 v1.5 before that.
function decrypt(key, ciphertext) {
  const ct = Buffer.from(String(ciphertext).replace(/=+$/, ""), "base64url")
  try {
    return crypto.privateDecrypt({ key, padding: crypto.constants.RSA_PKCS1_OAEP_PADDING, oaepHash: "sha256" }, ct).toString("utf8")
  } catch {}
  // Node and Bun refuse PKCS#1 v1.5 decryption, so its padding is taken off
  // here: 00 02 <non-zero bytes> 00 <message>
  try {
    const em = crypto.privateDecrypt({ key, padding: crypto.constants.RSA_NO_PADDING }, ct)
    const end = em.indexOf(0, 2)
    if (em[0] === 0 && em[1] === 2 && end >= 10) return em.subarray(end + 1).toString("utf8")
  } catch {}
  throw new Error("Zed sign-in: the access token couldn't be decrypted")
}

// signedInWith reads the callback's token, asks Zed who the account is and
// reads its models, so the picker has them before the first request.
async function signedInWith(key, uid, ciphertext, systemId) {
  const access = decrypt(key, ciphertext)
  const s = { userId: uid, access, systemId }
  const me = parse(await cloud("GET", "/client/users/me", s))
  if (!me) throw new Error("Zed: an unreadable account")
  const org = orgOf(me)
  if (modelsOff(me, org)) throw new Error("Zed: this account's organization has Zed's hosted models turned off")
  s.org = org
  s.plan = planOf(me, org)
  s.who = who(me)
  try {
    const models = await fetchModels(s)
    lists.set(uid, { at: Date.now(), org: s.org, models })
  } catch {}
  // what this sign-in saves is the organization now, whatever was picked
  // for the account before
  picked.delete(uid)
  return {
    type: "success",
    access,
    refresh: JSON.stringify({ userId: uid, systemId, org: s.org, plan: s.plan, planName: planName(s.plan), login: me.user?.github_login || me.user?.username || "", name: me.user?.name ?? "" }),
    expires: 0,
    accountId: s.who,
  }
}

// signIn is Zed's own sign-in, run as the editor runs it.
async function signIn() {
  const { privateKey, publicKey } = crypto.generateKeyPairSync("rsa", { modulusLength: 2048 })
  const pub = padded(publicKey.export({ type: "pkcs1", format: "der" }))
  const systemId = crypto.randomUUID()
  let got
  const back = new Promise((resolve) => (got = resolve))
  // Zed's page sends the browser to the port on whatever path; the query is
  // what counts
  const server = http.createServer((req, res) => {
    const q = new URL(req.url ?? "/", "http://127.0.0.1").searchParams
    const uid = q.get("user_id")
    const tok = q.get("access_token")
    if (!uid || !tok) {
      res.writeHead(404).end()
      return
    }
    got({ uid, tok })
    res.writeHead(302, { Location: SUCCEEDED }).end()
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const port = server.address().port
  const q = new URLSearchParams({ native_app_port: String(port), native_app_public_key: pub, system_id: systemId })
  const close = () => {
    try {
      server.close()
      server.closeAllConnections?.()
    } catch {}
  }
  return {
    url: `${SITE}/native_app_signin?${q}`,
    instructions: "Sign in to Zed in the browser; it comes back here when you're done.",
    method: "auto",
    async callback() {
      let timer
      try {
        const cb = await Promise.race([back, new Promise((r) => (timer = setTimeout(() => r(null), SIGN_IN_TIMEOUT)))])
        if (!cb) return { type: "failed", error: "the sign-in timed out" }
        return await signedInWith(privateKey, cb.uid, cb.tok, systemId)
      } catch (e) {
        return { type: "failed", error: e.message }
      } finally {
        clearTimeout(timer)
        close()
      }
    },
  }
}

// ---- requests ---------------------------------------------------------------------

async function bodyText(body) {
  if (body == null) return ""
  if (typeof body === "string") return body
  if (body instanceof ArrayBuffer || ArrayBuffer.isView(body)) return Buffer.from(body).toString("utf8")
  return await new Response(body).text()
}

// wireOf is the API a request was written in, from the path the AI SDK
// sent it to, and the model named in a Gemini path.
function wireOf(url) {
  const p = new URL(url).pathname
  const g = p.match(/\/models\/([^/:]+):(streamGenerateContent|generateContent)$/)
  if (g) return { wire: "gemini", model: decodeURIComponent(g[1]), stream: g[2] === "streamGenerateContent" }
  if (p.endsWith("/messages")) return { wire: "anthropic" }
  if (p.endsWith("/responses")) return { wire: "responses" }
  if (p.endsWith("/chat/completions")) return { wire: "chat" }
  return null
}

const WIRE_VENDOR = { anthropic: "anthropic", responses: "open_ai", chat: "x_ai", gemini: "google" }

// Claude Code (2.1.288 on) puts {role: "system"} messages inside an
// Anthropic request's messages: a turn's effort as output_config on an empty
// one, and its hooks' notices and what the user typed while a tool ran after
// the tool results (2.1.296). Anthropic's own API takes them where they sit;
// Zed's cloud takes only user and assistant there and answers 400 "failed
// to parse Anthropic request: unknown variant `system`, expected `user` or
// `assistant`". withoutSystemTurns keeps each one's text where it was, as
// text blocks of a user turn: appended to the user message before it, else
// put after the tool results at the head of the user message after it, else
// a user message of its own. One with no text (an effort alone) is left out;
// the request's own output_config still asks for the turn's effort. Kept in
// place, not moved to the top-level system: system comes before every
// message in the prompt cache's prefix, so text added there each turn would
// miss the cache for the whole conversation, and the notice would lose
// where in the turn it was said.
function systemBlocks(content) {
  if (typeof content === "string") return content ? [{ type: "text", text: content }] : []
  if (!Array.isArray(content)) return []
  return content.filter((b) => b && typeof b === "object" && !(b.type === "text" && !b.text))
}

function asBlocks(content) {
  if (Array.isArray(content)) return content
  if (typeof content === "string" && content) return [{ type: "text", text: content }]
  return []
}

function withoutSystemTurns(messages) {
  if (!messages.some((m) => m?.role === "system")) return messages
  const out = []
  let pending = [] // system text waiting for the user message after it
  for (const m of messages) {
    if (m?.role === "system") {
      const blocks = systemBlocks(m.content)
      if (!blocks.length) continue
      const prev = out.at(-1)
      if (prev?.role === "user" && !pending.length) prev.content = [...asBlocks(prev.content), ...blocks]
      else pending.push(...blocks)
      continue
    }
    if (pending.length) {
      if (m?.role === "user") {
        const c = asBlocks(m.content)
        let i = 0
        while (i < c.length && c[i]?.type === "tool_result") i++
        out.push({ ...m, content: [...c.slice(0, i), ...pending, ...c.slice(i)] })
        pending = []
        continue
      }
      out.push({ role: "user", content: pending })
      pending = []
    }
    out.push(m)
  }
  if (pending.length) {
    const prev = out.at(-1)
    if (prev?.role === "user") prev.content = [...asBlocks(prev.content), ...pending]
    else out.push({ role: "user", content: pending })
  }
  return out
}

// providerRequest is the request as Zed takes it for its provider's API,
// and whether the caller asked for a stream (Zed always streams).
function providerRequest(wire, req, model) {
  switch (wire) {
    case "anthropic": {
      const stream = !!req.stream
      // Zed's Anthropic request has no stream field: the cloud always streams
      delete req.stream
      // Zed's cloud refuses a tool result without is_error ("missing field
      // `is_error`"), which Anthropic's own API leaves out when it is false
      for (const m of req.messages ?? []) {
        for (const b of Array.isArray(m?.content) ? m.content : []) {
          if (b?.type === "tool_result" && !("is_error" in b)) b.is_error = false
        }
      }
      if (Array.isArray(req.messages)) req.messages = withoutSystemTurns(req.messages)
      return { req, stream }
    }
    case "responses": {
      const stream = !!req.stream
      req.stream = true
      return { req, stream }
    }
    case "chat": {
      const stream = !!req.stream
      req.stream = true
      // as Zed writes an xAI request
      if ("max_tokens" in req) {
        req.max_completion_tokens = req.max_tokens
        delete req.max_tokens
      }
      return { req, stream }
    }
    case "gemini": {
      delete req.session_id
      delete req.sessionId
      req.model = "models/" + model
      return { req }
    }
  }
}

// lines reads a completion's stream line by line. wrapped is whether the
// reply said it sends statuses; without, each line is a bare event.
async function* lines(body, wrapped) {
  const dec = new TextDecoder()
  let buf = ""
  const one = function* (raw) {
    const s = raw.trim()
    if (!s) return
    const j = parse(s)
    if (j === undefined) throw new Error("Zed: an unreadable line")
    if (!wrapped) return yield { event: j }
    if (j.event !== undefined) return yield { event: j.event }
    if (j.status === "stream_ended") return yield { ended: true }
    if (j.status?.failed) return yield { failed: j.status.failed }
    // "started", {"queued": …} and newer statuses say nothing to act on
  }
  for await (const chunk of body) {
    buf += dec.decode(chunk, { stream: true })
    let i
    while ((i = buf.indexOf("\n")) >= 0) {
      const l = buf.slice(0, i)
      buf = buf.slice(i + 1)
      yield* one(l)
    }
  }
  buf += dec.decode()
  yield* one(buf)
}

// sse writes one event the way its provider's API streams it.
function sse(wire, ev) {
  const data = "data: " + JSON.stringify(ev) + "\n\n"
  if ((wire === "anthropic" || wire === "responses") && ev?.type) return `event: ${ev.type}\n` + data
  return data
}

const STATUS_NAME = { 400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 402: "RESOURCE_EXHAUSTED", 403: "PERMISSION_DENIED", 404: "NOT_FOUND", 429: "RESOURCE_EXHAUSTED", 529: "UNAVAILABLE" }

// errorBody is a failure in the caller's API's own shape.
function errorBody(wire, status, message) {
  switch (wire) {
    case "anthropic": {
      const type = { 400: "invalid_request_error", 401: "authentication_error", 402: "billing_error", 403: "permission_error", 404: "not_found_error", 429: "rate_limit_error", 529: "overloaded_error" }[status] ?? "api_error"
      return { type: "error", error: { type, message } }
    }
    case "responses":
      return { type: "error", code: String(status), message, error: { message, code: String(status) } }
    case "gemini":
      return { error: { code: status, message, status: STATUS_NAME[status] ?? "INTERNAL" } }
    default:
      return { error: { message, type: "api_error", code: status } }
  }
}

// errorResponse is a failure as the caller's API gives it. signIn is what it
// means for the account (magpie's X-Magpie-Sign-In): the built-in marked
// the account lapsed only when Zed refused its sign-in, so any other 401
// (a model token refused twice, a vendor's upstream_http_401) is kept.
const errorResponse = (wire, status, message, signIn = status === 401 ? "kept" : undefined) =>
  new Response(JSON.stringify(errorBody(wire, status, message)), {
    status,
    headers: { "content-type": "application/json", ...(signIn ? { "x-magpie-sign-in": signIn } : {}) },
  })

// KEPT is on an answer that went through: the built-in took the mark off
// only when the account was signed in again, never on a request.
const KEPT = { "x-magpie-sign-in": "kept" }

// said tells whether one of the provider's own events says anything —
// text, reasoning, a call, a finish — rather than only framing the reply,
// as magpie's decoders tell a reply begun from its lead (message_start,
// response.created, a role-only chunk).
function said(wire, ev) {
  if (!ev || typeof ev !== "object") return true
  switch (wire) {
    case "anthropic":
      switch (ev.type) {
        case "message_start":
        case "ping":
        case "content_block_stop":
          return false
        case "content_block_start":
          return !["text", "thinking", "redacted_thinking"].includes(ev.content_block?.type) || !!(ev.content_block?.text || ev.content_block?.thinking)
        case "content_block_delta": {
          const d = ev.delta ?? {}
          return !(d.text === "" || d.thinking === "" || d.partial_json === "")
        }
      }
      return true
    case "responses":
      switch (ev.type) {
        case "response.created":
        case "response.in_progress":
        case "response.queued":
        case "response.content_part.added":
        case "response.reasoning_summary_part.added":
          return false
        case "response.output_item.added":
          return !["message", "reasoning"].includes(ev.item?.type)
        case "response.output_text.delta":
        case "response.reasoning_summary_text.delta":
        case "response.reasoning_text.delta":
          return ev.delta !== ""
      }
      return true
    case "chat":
      if (ev.error) return true
      if (!Array.isArray(ev.choices)) return false // usage alone
      return ev.choices.some((c) => {
        const d = c?.delta ?? {}
        return !!(c?.finish_reason || d.content || d.reasoning_content || d.reasoning || d.tool_calls?.length)
      })
    case "gemini":
      if (ev.error) return true
      return (ev.candidates ?? []).some((c) => c?.finishReason || (c?.content?.parts ?? []).some((p) => p?.text || p?.functionCall || p?.thought))
  }
  return true
}

// eventError is the message of an error the provider's own stream carries
// (Anthropic's error event, a failed response, a chat or Gemini chunk's
// error), undefined for any other event.
function eventError(wire, ev) {
  if (!ev || typeof ev !== "object") return undefined
  if (wire === "anthropic" && ev.type === "error") return String(ev.error?.message ?? "")
  if (wire === "responses") {
    if (["response.completed", "response.incomplete", "response.failed"].includes(ev.type) && ev.response?.error) return String(ev.response.error.message ?? "")
    if (ev.type === "error") return String((ev.error ? ev.error.message : ev.message) ?? "")
    return undefined
  }
  if ((wire === "chat" || wire === "gemini") && ev.error) return String(ev.error?.message ?? "")
  return undefined
}

// CUT_SHORT_NOTE ends a streamed Chat Completions reply (xAI's) that Zed
// broke off after it said something, as zen-free does (#91): magpie's
// gateway can go on with an Anthropic or Responses reply cut short, not
// with a Chat one, and failing it would throw away what the client was
// already shown.
const CUT_SHORT_NOTE = "(The reply was cut short: Zed closed the stream before it finished. Ask again to go on.)"

// chatSaid follows a streamed chat reply's choices: which wrote text,
// which have finished, and whether a tool call was begun (its arguments
// may be half written, so a reply cut short after one stays an error).
function chatSaid() {
  const wrote = new Set()
  const open = new Set()
  const tools = new Set()
  const done = new Set()
  let meta
  return {
    see(ev) {
      if (!Array.isArray(ev?.choices)) return
      meta ??= { id: ev.id, created: ev.created, model: ev.model }
      for (const c of ev.choices) {
        const i = c?.index ?? 0
        const d = c?.delta ?? {}
        if (d.content) wrote.add(i)
        if (d.content || d.reasoning_content || d.reasoning) open.add(i)
        if (d.tool_calls?.length) tools.add(i)
        if (c?.finish_reason) done.add(i)
      }
    },
    // the frames that end the reply cut short, or undefined when it must
    // fail: nothing said, or a tool call begun and not finished
    end() {
      if (!open.size && !tools.size && !done.size) return undefined
      if ([...tools].some((i) => !done.has(i))) return undefined
      const left = [...open].filter((i) => !done.has(i)).sort((a, b) => a - b)
      if (!left.length) return ["data: [DONE]\n\n"]
      const chunk = (choices) => sse("chat", { id: meta?.id ?? "chatcmpl-cut-short", object: "chat.completion.chunk", created: meta?.created ?? Math.floor(Date.now() / 1000), model: meta?.model, choices })
      return [
        chunk(left.map((index) => ({ index, delta: { content: (wrote.has(index) ? "\n\n" : "") + CUT_SHORT_NOTE }, finish_reason: null }))),
        chunk(left.map((index) => ({ index, delta: {}, finish_reason: "stop" }))),
        "data: [DONE]\n\n",
      ]
    },
  }
}

// streamed turns Zed's lines into the provider's own server-sent events.
// What comes before the reply says anything is held, as magpie's built-in
// holds it (relayStatus): a failure there — a failed status, the
// provider's own error event, a reply that broke off or ended without an
// answer — is answered as a refused request with its status (a 429, a 402
// is another account's turn), not a 200 whose stream carries it.
async function streamed(wire, res) {
  const wrapped = !!res.headers.get("x-zed-server-supports-status-messages")
  const it = lines(res.body, wrapped)
  const head = []
  let ended = false
  let first = null // the first line that says something
  try {
    for (;;) {
      const r = await it.next()
      if (r.done) break
      const l = r.value
      if (l.ended) {
        ended = true
        continue
      }
      if (l.failed) {
        let status = failedStatus(l.failed.code)
        if (status < 400 || status > 599) status = 502
        return errorResponse(wire, status, l.failed.message || l.failed.code)
      }
      const bad = eventError(wire, l.event)
      if (bad !== undefined) return errorResponse(wire, 502, bad)
      if (said(wire, l.event)) {
        first = l
        break
      }
      head.push(l.event)
    }
  } catch (e) {
    return errorResponse(wire, 502, String(e?.message ?? e).replace(/^Zed: /, ""))
  }
  if (!first) {
    if (wrapped && !ended) return errorResponse(wire, 502, "the reply ended before it was complete")
    return errorResponse(wire, 502, "Zed ended without an answer")
  }
  const enc = new TextEncoder()
  const body = new ReadableStream({
    async start(ctl) {
      const put = (s) => ctl.enqueue(enc.encode(s))
      const fail = (status, message) => {
        if (wire === "anthropic") put(`event: error\ndata: ${JSON.stringify(errorBody(wire, status, message))}\n\n`)
        else if (wire === "responses") put(`event: error\ndata: ${JSON.stringify(errorBody(wire, status, message))}\n\n`)
        else put(`data: ${JSON.stringify(errorBody(wire, status, message))}\n\n`)
      }
      const chat = wire === "chat" ? chatSaid() : null
      for (const ev of head) {
        chat?.see(ev)
        put(sse(wire, ev))
      }
      chat?.see(first.event)
      put(sse(wire, first.event))
      try {
        for (;;) {
          const r = await it.next()
          if (r.done) break
          const l = r.value
          if (l.ended) ended = true
          else if (l.failed) {
            fail(failedStatus(l.failed.code), l.failed.message || l.failed.code)
            ctl.close()
            return
          } else {
            chat?.see(l.event)
            put(sse(wire, l.event))
          }
        }
        // Zed ends every whole reply with stream_ended; a chat reply cut
        // short after it said something ends with a note instead
        const cut = wrapped && !ended ? chat?.end() : undefined
        if (cut) for (const f of cut) put(f)
        else if (wrapped && !ended) fail(502, "the reply ended before it was complete")
        else if (wire === "chat") put("data: [DONE]\n\n")
        ctl.close()
      } catch (e) {
        try {
          fail(502, String(e?.message ?? e).replace(/^Zed: /, ""))
          ctl.close()
        } catch {}
      }
    },
    cancel() {
      it.return?.().catch?.(() => {})
      res.body?.cancel?.().catch(() => {})
    },
  })
  return new Response(body, { status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache", ...KEPT } })
}

// ---- whole replies, for a caller that didn't ask for a stream ------------------

function anthropicWhole(events) {
  let msg = null
  const blocks = []
  const json = {}
  for (const ev of events) {
    switch (ev?.type) {
      case "message_start":
        msg = { ...ev.message, content: [] }
        break
      case "content_block_start":
        blocks[ev.index] = { ...ev.content_block }
        if (ev.content_block?.type === "tool_use") json[ev.index] = ""
        break
      case "content_block_delta": {
        const b = blocks[ev.index]
        const d = ev.delta ?? {}
        if (!b) break
        if (d.type === "text_delta") b.text = (b.text ?? "") + d.text
        else if (d.type === "input_json_delta") json[ev.index] = (json[ev.index] ?? "") + d.partial_json
        else if (d.type === "thinking_delta") b.thinking = (b.thinking ?? "") + d.thinking
        else if (d.type === "signature_delta") b.signature = (b.signature ?? "") + d.signature
        break
      }
      case "message_delta":
        if (msg) {
          Object.assign(msg, ev.delta ?? {})
          msg.usage = { ...(msg.usage ?? {}), ...(ev.usage ?? {}) }
        }
        break
    }
  }
  for (const [i, s] of Object.entries(json)) if (blocks[i]) blocks[i].input = s ? parse(s) ?? {} : {}
  msg ??= { type: "message", role: "assistant", content: [] }
  msg.content = blocks.filter(Boolean)
  return msg
}

function responsesWhole(events) {
  for (const ev of events.slice().reverse()) {
    if (["response.completed", "response.incomplete", "response.failed"].includes(ev?.type) && ev.response) return ev.response
  }
  return null
}

function chatWhole(events) {
  let out = null
  const choices = []
  for (const ev of events) {
    out ??= { id: ev.id, object: "chat.completion", created: ev.created, model: ev.model }
    if (ev.usage) out.usage = ev.usage
    for (const c of ev.choices ?? []) {
      const ch = (choices[c.index ?? 0] ??= { index: c.index ?? 0, message: { role: "assistant", content: "" }, finish_reason: null })
      const d = c.delta ?? {}
      if (d.content) ch.message.content += d.content
      if (d.reasoning_content) ch.message.reasoning_content = (ch.message.reasoning_content ?? "") + d.reasoning_content
      for (const t of d.tool_calls ?? []) {
        const calls = (ch.message.tool_calls ??= [])
        const tc = (calls[t.index ?? 0] ??= { id: "", type: "function", function: { name: "", arguments: "" } })
        if (t.id) tc.id = t.id
        if (t.function?.name) tc.function.name += t.function.name
        if (t.function?.arguments) tc.function.arguments += t.function.arguments
      }
      if (c.finish_reason) ch.finish_reason = c.finish_reason
    }
  }
  out ??= { object: "chat.completion" }
  out.choices = choices.filter(Boolean)
  return out
}

function geminiWhole(events) {
  const out = { candidates: [] }
  for (const ev of events) {
    if (ev.usageMetadata) out.usageMetadata = ev.usageMetadata
    if (ev.modelVersion) out.modelVersion = ev.modelVersion
    for (const [i, c] of (ev.candidates ?? []).entries()) {
      const k = c.index ?? i
      const cand = (out.candidates[k] ??= { content: { role: "model", parts: [] }, index: k })
      for (const p of c.content?.parts ?? []) {
        const last = cand.content.parts.at(-1)
        if (typeof p.text === "string" && last && typeof last.text === "string" && !!last.thought === !!p.thought && Object.keys(p).every((x) => x === "text" || x === "thought")) last.text += p.text
        else cand.content.parts.push({ ...p })
      }
      if (c.finishReason) cand.finishReason = c.finishReason
    }
  }
  return out
}

const WHOLE = { anthropic: anthropicWhole, responses: responsesWhole, chat: chatWhole, gemini: geminiWhole }

// whole reads all of Zed's lines into one reply of the provider's API.
async function whole(wire, res) {
  const wrapped = !!res.headers.get("x-zed-server-supports-status-messages")
  const events = []
  let ended = false
  try {
    for await (const l of lines(res.body, wrapped)) {
      if (l.ended) ended = true
      else if (l.failed) {
        const n = failedStatus(l.failed.code)
        return errorResponse(wire, n >= 400 && n <= 599 ? n : 502, l.failed.message || l.failed.code)
      } else {
        // the provider's own error, as the built-in's decoders read it
        const bad = eventError(wire, l.event)
        if (bad !== undefined) return errorResponse(wire, 502, bad)
        events.push(l.event)
      }
    }
  } catch (e) {
    return errorResponse(wire, 502, String(e?.message ?? e).replace(/^Zed: /, ""))
  }
  if (wrapped && !ended) return errorResponse(wire, 502, "the reply ended before it was complete")
  const out = WHOLE[wire](events)
  if (!out) return errorResponse(wire, 502, "the reply ended before it was complete")
  return new Response(JSON.stringify(out), { status: 200, headers: { "content-type": "application/json", ...KEPT } })
}

// complete sends one request through Zed's /completions.
async function complete(s, url, init) {
  const w = wireOf(url)
  if (!w) return new Response(JSON.stringify({ error: { message: "nothing is served at " + new URL(url).pathname } }), { status: 404, headers: { "content-type": "application/json" } })
  const req = parse(await bodyText(init?.body))
  if (!req || typeof req !== "object") return errorResponse(w.wire, 400, "an unreadable request")
  const id = w.model ?? String(req.model ?? "")
  const vendor = vendors.get(id) ?? WIRE_VENDOR[w.wire]
  const { req: pr, stream = w.stream } = providerRequest(w.wire, req, id)
  const body = JSON.stringify({ provider: vendor, model: id, provider_request: pr })
  let res
  let renewed = false
  let moved = false
  // Zed refusing the organization (402, no active subscription) moves the
  // account once to one with a plan and asks again (#232)
  const another = async () => {
    if (moved) return false
    moved = true
    return await repick(s)
  }
  for (;;) {
    let tok
    try {
      tok = await modelToken(s, renewed)
    } catch (e) {
      if (e instanceof SignInExpired) return errorResponse(w.wire, 401, e.message, "expired")
      if (e instanceof ZedStatus && e.status === 402) {
        if (await another()) continue
        const [status, msg] = refusal(402, e.body)
        return errorResponse(w.wire, status, msg)
      }
      return errorResponse(w.wire, 502, String(e?.message ?? e).replace(/^Zed: /, ""))
    }
    res = await fetch(CLOUD + "/completions", {
      method: "POST",
      headers: {
        Authorization: "Bearer " + tok,
        "Content-Type": "application/json",
        "x-zed-version": VERSION,
        "x-zed-client-supports-status-messages": "true",
        "x-zed-client-supports-stream-ended-request-completion-status": "true",
        "User-Agent": userAgent(),
      },
      body,
      signal: init?.signal,
    })
    // a model token Zed calls stale is minted again, once
    if (!renewed && stale(res.status, res.headers)) {
      await res.body?.cancel?.().catch(() => {})
      renewed = true
      continue
    }
    if (res.status === 402 && !moved) {
      const text = await res.text()
      if (await another()) continue
      const [status, msg] = refusal(402, text)
      return errorResponse(w.wire, status, msg)
    }
    break
  }
  if (!res.ok) {
    const [status, msg] = refusal(res.status, await res.text())
    return errorResponse(w.wire, status, msg)
  }
  return stream ? streamed(w.wire, res) : whole(w.wire, res)
}

// ---- usage ----------------------------------------------------------------------------

// usage is what the account says of its plan, as magpie's built-in Zed
// account shows it (zed_usage.go): Zed tells its editor the plan and its
// billing period, not how much of the allowance is spent, so there are no
// windows — the plan's name, when the period ends, and a plan Zed won't
// serve (overdue invoices) as the error it is.
// jsonError is what Go's encoding/json says of a /users/me reply it can't
// read into zed.Me.
function jsonError(text, v) {
  if (v !== undefined) return `json: cannot unmarshal ${Array.isArray(v) ? "array" : typeof v === "string" ? "string" : typeof v === "number" ? "number" : "bool"} into Go value of type zed.Me`
  const c = String(text ?? "").trimStart()[0]
  return c === undefined ? "unexpected end of JSON input" : `invalid character '${c}' looking for beginning of value`
}

const RFC3339 = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/i

async function usage(auth, save) {
  const s = stateOf(auth)
  // the built-in's read marked the account only when Zed refused its
  // sign-in, and took the mark off never: a clean read keeps it
  if (!s) return { error: "no such Zed account", signIn: "kept" }
  let me
  try {
    const text = await cloud("GET", "/client/users/me", s, undefined, 15_000)
    me = parse(text)
    if (me === null) me = {}
    else if (typeof me !== "object" || Array.isArray(me)) return { error: "Zed: an unreadable account: " + jsonError(text, me), signIn: "kept" }
  } catch (e) {
    if (e?.status === 401) return { error: `${s.who}: the Zed sign-in has expired — sign in again`, signIn: "expired" }
    return { error: e?.message ?? String(e), signIn: "kept" }
  }
  // the organization the account is under, picked again when it lost its
  // plan (#232), and its plan, saved with the sign-in when either changed
  const org = orgOf(me, s.org)
  const plan = planOf(me, org)
  const out = { plan: planName(plan), signIn: "kept" }
  if (org !== s.org) {
    move(s, org, plan)
    if (save) await save(withOrg(auth, org, plan)).catch(() => {})
  } else if (plan !== s.plan && save) {
    await save(withOrg(auth, s.org, plan)).catch(() => {})
  }
  const end = me.plan?.subscription_period?.ended_at
  if (typeof end === "string" && RFC3339.test(end) && !isNaN(Date.parse(end))) out.until = end
  // the account's row keeps naming a free plan "Free" (planName, saved
  // above); its card says "No plan", which magpie's card knows and
  // translates, unless an overdue invoice is what it says, as the
  // built-in's zedLoginQuota did
  if (me.plan?.has_overdue_invoices) out.error = "Zed: this account has an overdue invoice, so its models are paused (see zed.dev/account)"
  else if (plan === "zed_free" || plan === "") out.plan = "No plan"
  return out
}

// ---- the plugin ---------------------------------------------------------------------

export async function ZedAuthPlugin({ client } = {}) {
  // saves a sign-in under the account the call is for
  const save = client?.auth?.set ? (body) => client.auth.set({ path: { id: PROVIDER }, body }) : null
  return {
    config: async (cfg) => {
      cfg.provider ??= {}
      const was = cfg.provider[PROVIDER] ?? {}
      cfg.provider[PROVIDER] = {
        name: "Zed",
        npm: NPM.anthropic,
        api: BASE,
        ...was,
        models: { ...Object.fromEntries(MODELS.map((m) => [m.id, entry(m)])), ...(was.models ?? {}) },
      }
    },
    provider: {
      id: PROVIDER,
      // the models the account may call, as Zed's /models lists them now
      async models(provider, { auth } = {}) {
        const s = stateOf(auth)
        if (!s) return provider.models
        s.save = save
        let l = lists.get(s.userId)
        // a list is of one organization's models
        if (l && l.org !== undefined && l.org !== s.org) l = undefined
        if (!l || Date.now() - l.at > MODELS_TTL) {
          try {
            const models = await fetchModels(s)
            l = { at: Date.now(), org: s.org, models }
            lists.set(s.userId, l)
          } catch (e) {
            // Zed refusing the account's own sign-in while its list was
            // read: the built-in marked the account then (zedFetchModels →
            // ZedToken → zedLapse), so magpie is told as an answer's
            // X-Magpie-Sign-In would tell it
            if (e instanceof SignInExpired) throw Object.assign(e, { signIn: "expired" })
            // never read for this account: the six declared before sign-in
            // aren't its list, nor their limits its limits, so say why
            // there is none rather than hand them back as if they were.
            // A 200 that lists nothing callable (every model disabled, an
            // organization without a subscription) is a failure too, which
            // a host judging by the response's status alone wouldn't see
            // (#229, ReneXiong). A list read before is kept below.
            if (!l) throw /^Zed\b/.test(e?.message ?? "") ? e : new Error(`Zed: this account's models couldn't be read: ${e?.message ?? e}`)
          }
        }
        return Object.fromEntries(l.models.map((m) => [m.id, model(m)]))
      },
    },
    auth: {
      provider: PROVIDER,
      methods: [{ type: "oauth", label: "Sign in with Zed (browser)", authorize: signIn }],
      async loader(getAuth) {
        if (!stateOf(await getAuth())) return {}
        return {
          baseURL: BASE,
          apiKey: "zed", // Zed's model token is put on by the fetch below
          async fetch(input, init) {
            const s = stateOf(await getAuth())
            if (!s) throw new Error("not signed in to Zed")
            s.save = save
            const url = input instanceof Request ? input.url : String(input)
            if (input instanceof Request) init = { method: input.method, body: await input.text(), signal: input.signal, ...init }
            return complete(s, url, init)
          },
        }
      },
      // magpie's own hook: the account's plan and its period
      async usage(getAuth) {
        return usage(await getAuth(), save)
      },
    },
  }
}

// for tests
export const _internal = { said, eventError, stateOf, parseModels, entry, providerRequest, wireOf, refusal, failedStatus, decrypt, padded, signedInWith, complete, vendors, tokens, lists, orgOf, planOf, picked, who, planName, usage }
