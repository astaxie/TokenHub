// OpenCode provider plugins for MiniMax Code (mcode) accounts: the credits
// a MiniMax Code account gets (the daily check-in's among them) and its
// M Plan, on MiniMax Code's own models.
//   - minimax-code:        the China site (account.minimax.cn, agent.minimax.cn)
//   - minimax-code-global: the international site (account.minimax.io, agent.minimax.io)
// An account signs in here, with MiniMax Code's device sign-in (OAuth 2
// device flow with PKCE, client mcode-public): nothing is read from or
// written to MiniMax Code's own sign-in (~/.minimax). Its chats are
// Anthropic messages at <agent>/mavis/api/v1/llm/v1/messages, sent with
// MiniMax Code's headers. The account's daily check-in (每日签到) is
// claimed with the same token, on the minimax-cloud endpoints.

import { createHash, randomBytes, randomUUID } from "node:crypto"

const CLIENT_ID = "mcode-public"
const SCOPE = "agent.default"
const AUDIENCE = "agent-backend"
const DEVICE_GRANT = "urn:ietf:params:oauth:grant-type:device_code"
const EARLY_MS = 2 * 60 * 1000 // a token this close to its end is refreshed before a request
const LEAD_MS = 10 * 60 * 1000 // and this close, magpie renews it ahead of time (auth.refresh)
const SIGN_IN_MS = 10 * 60 * 1000 // when the device code gives no end of its own
const KEEP_RENEWED_MS = 60 * 1000 // how long a saved refresh's result answers for the old token
const SIGN_SECRET = "I*7Cf%WZ#S&%1RlZJ&C2" // MiniMax Code's request signing (x-signature)
const NPM = "@ai-sdk/anthropic"
// the key the Anthropic SDK is given in MiniMax Code: a placeholder, the
// account going in Authorization
const PLACEHOLDER_KEY = "sk-xxx"

// Each site's hosts, as MiniMax Code's prod build has them. Tests point
// these at fakes of their own.
const SITES = {
  "minimax-code": {
    id: "minimax-code",
    name: "MiniMax Code",
    region: "cn",
    lang: "zh",
    account: "https://account.minimax.cn", // OAuth
    llm: "https://agent.minimax.cn", // chats and the model list
    agent: "https://agent.minimaxi.com", // the account and its membership
    platform: "https://www.minimaxi.com", // the M Plan's windows
  },
  "minimax-code-global": {
    id: "minimax-code-global",
    name: "MiniMax Code (Global)",
    region: "en",
    lang: "en",
    account: "https://account.minimax.io",
    llm: "https://agent.minimax.io",
    agent: "https://agent.minimax.io",
    platform: "https://platform.minimax.io",
  },
}

const api = (site) => site.llm + "/mavis/api/v1/llm/v1"
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const md5 = (s) => createHash("md5").update(s).digest("hex")
const str = (o, k) => (typeof o?.[k] === "string" && o[k].trim() ? o[k].trim() : undefined)
const pos = (o, k) => (typeof o?.[k] === "number" && Number.isFinite(o[k]) && o[k] > 0 ? o[k] : undefined)
const obj = (v) => (v && typeof v === "object" && !Array.isArray(v) ? v : undefined)
const num = (v) => {
  if (typeof v === "number" && Number.isFinite(v)) return v
  if (typeof v === "string" && v.trim() && Number.isFinite(Number(v))) return Number(v)
  return undefined
}

// ---- the models -------------------------------------------------------------

// The limits the endpoint really allows, for the models whose advertised
// ones are MiniMax Code's conservative declaration. Its model config says
// 512K/128K for the M3 family, but the endpoint accepts 1,048,576 input
// tokens and 524,288 output tokens for every one of them: a request of
// 1,048,140 input tokens is answered, one of 1,048,700 is refused with
// "context window exceeds limit (2013)", and max_tokens of 524,288 is
// accepted where 600,000 is refused with "does not support max tokens >
// 524288". The advertised numbers are the same for a signed-in account and
// for nobody, so they are not a plan's limit, and an agent that believes
// them compacts its history about half way too early.
const VERIFIED_LIMITS = {
  "MiniMax-M3.1-Flash": { context: 1048576, output: 524288 },
  "MiniMax-M3.1-Flash-Preview": { context: 1048576, output: 524288 },
  "MiniMax-M3": { context: 1048576, output: 524288 },
}

// limits is a model's context and output: the verified ones where they are
// known, the advertised ones otherwise.
function limits(id, context, output) {
  const v = VERIFIED_LIMITS[id]
  return {
    context: v ? v.context : context ?? 0,
    output: v ? v.output : output ?? 0,
  }
}

// MiniMax Code's own list, for when the live one can't be had.
// thinking: "switchable" (on or off), "forced_on" (always on, at an effort)
const MODELS = [
  { id: "MiniMax-M3.1-Flash-Preview", name: "M3.1-Flash-Preview", context: 1048576, output: 524288, images: true, thinking: "forced_on", efforts: ["low", "medium", "high", "xhigh", "max"] },
  { id: "MiniMax-M3", name: "MiniMax-M3", context: 1048576, output: 524288, images: true, thinking: "switchable", efforts: [] },
  { id: "MiniMax-M2.7", name: "MiniMax-M2.7", context: 200000, output: 128000, images: false, thinking: "on", efforts: [] },
  { id: "MiniMax-M2.7-highspeed", name: "MiniMax-M2.7-highspeed", context: 200000, output: 128000, images: false, thinking: "on", efforts: [] },
]

// variants are a model's reasoning levels as @ai-sdk/anthropic options,
// sent as MiniMax Code sends them: adaptive thinking at an effort
// (output_config.effort), "none" turning thinking off where it can be.
function variants(m) {
  const out = {}
  if (m.thinking === "switchable") out.none = { thinking: { type: "disabled" } }
  if (m.efforts?.length) for (const e of m.efforts) out[e] = { thinking: { type: "adaptive" }, effort: e }
  else if (m.thinking === "switchable") out.high = { thinking: { type: "adaptive" } }
  return out
}

const reasons = (m) => m.thinking !== "off"

function configModels() {
  return Object.fromEntries(MODELS.map((m) => [m.id, {
    name: m.name,
    reasoning: reasons(m),
    tool_call: true,
    attachment: !!m.images,
    modalities: { input: m.images ? ["text", "image"] : ["text"], output: ["text"] },
    limit: limits(m.id, m.context, m.output),
    cost: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
    variants: variants(m),
  }]))
}

function modelOf(site, provider, m) {
  return {
    id: m.id,
    providerID: provider.id,
    name: m.name,
    api: { id: m.id, url: api(site), npm: NPM },
    status: "active",
    headers: {},
    options: {},
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: limits(m.id, m.context, m.output),
    capabilities: {
      temperature: true,
      reasoning: reasons(m),
      attachment: !!m.images,
      toolcall: true,
      input: { text: true, image: !!m.images, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: variants(m),
  }
}

// parseModels reads MiniMax Code's model config ({providers:[{providerId:
// "minimax", config:{models:{<id>:{…}}, model_order?}}]}) as models.
function parseModels(body) {
  const p = (Array.isArray(body?.providers) ? body.providers : []).find((x) => x?.providerId === "minimax")
  const ms = obj(obj(p?.config)?.models)
  if (!ms) return []
  const order = Array.isArray(p.config.model_order) ? p.config.model_order.filter((id) => ms[id]) : []
  const ids = [...order, ...Object.keys(ms).filter((id) => !order.includes(id))]
  const out = []
  for (const id of ids) {
    const d = obj(ms[id])
    if (!d || !id.trim()) continue
    const input = Array.isArray(d.modalities?.input) ? d.modalities.input : []
    const mode = str(d.thinking_config, "mode")
    const efforts = (d.thinking?.effortOptions ?? d.thinking?.effort_options ?? d.effort_options ?? [])
      .filter((e) => typeof e === "string" && e && e !== "default" && e !== "none")
    out.push({
      id,
      name: str(d, "name") ?? id,
      context: num(d.limit?.context) ?? 0,
      output: num(d.limit?.output) ?? 0,
      images: input.includes("image") || d.attachment === true,
      thinking: mode === "switchable" ? "switchable"
        : mode === "forced_off" ? "off"
        : mode === "forced_on" ? "forced_on"
        : d.reasoning === false ? "off" : "on",
      efforts,
    })
  }
  return out
}

// liveModels asks MiniMax Code's model config, as mcode does at start: the
// signed-in account's first, then, when that is refused, no one's.
async function liveModels(site, access) {
  const url = new URL("/mavis/api/v1/models", site.llm)
  url.searchParams.set("region", site.region)
  url.searchParams.set("buildEnv", "prod")
  const ask = (auth) => fetch(url, {
    headers: { Accept: "application/json", ...(auth ? { Authorization: "Bearer " + auth } : {}) },
    signal: AbortSignal.timeout(10000),
  })
  let res = await ask(access)
  if (res.status === 401 || res.status === 403) res = await ask("")
  if (!res.ok) throw new Error(`${site.name}'s models: HTTP ${res.status}`)
  return parseModels(await res.json())
}

// ---- signing in (OAuth device flow) ---------------------------------------------

class OAuthError extends Error {
  constructor(code, status, message) {
    super(message ?? `MiniMax's sign-in server said ${code}${status ? ` (HTTP ${status})` : ""}`)
    this.code = code
    this.httpStatus = status
  }
}

// postForm posts an OAuth form, as MiniMax Code's client does; ok is false
// when the answer is an HTTP error or carries an "error".
async function postForm(url, fields) {
  const res = await fetch(url, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(fields).toString(),
    signal: AbortSignal.timeout(30000),
  })
  let body
  try {
    body = obj(JSON.parse(await res.text()))
  } catch {}
  if (!body) throw new OAuthError("invalid_json_response", res.status)
  const error = str(body, "error")
  return { ok: res.ok && !error, status: res.status, body, error }
}

const jwt = (t) => {
  try {
    return obj(JSON.parse(Buffer.from(String(t).split(".")[1] ?? "", "base64url").toString("utf8")))
  } catch {
    return undefined
  }
}

// token reads a token answer: refresh is the one to keep when none is sent.
function token(body, refresh) {
  const access = str(body, "access_token")
  const expiresIn = pos(body, "expires_in")
  if (!access || !expiresIn) throw new OAuthError("invalid_token_response")
  const claims = jwt(access)
  return {
    access,
    refresh: str(body, "refresh_token") ?? refresh ?? "",
    expires: Date.now() + expiresIn * 1000,
    subject: str(claims, "sub"),
    accountID: str(claims, "account_id"),
  }
}

async function startDevice(site) {
  const verifier = randomBytes(32).toString("base64url")
  const challenge = createHash("sha256").update(verifier, "ascii").digest("base64url")
  const r = await postForm(site.account + "/oauth2/device/code", {
    client_id: CLIENT_ID,
    scope: SCOPE,
    audience: AUDIENCE,
    code_challenge: challenge,
    code_challenge_method: "S256",
  })
  if (!r.ok) throw new OAuthError(r.error ?? "oauth_request_failed", r.status)
  const b = r.body
  const userCode = str(b, "user_code")
  const deviceCode = str(b, "device_code")
  const expiredIn = pos(b, "expired_in")
  // MiniMax's own variant: no device_code, the user code polled for, an
  // end that may be an at-ms, an interval in ms
  const byUser = !deviceCode && !!userCode && expiredIn !== undefined
  const uri = str(b, "verification_uri") ?? str(b, "verification_url")
  const at = expiredIn === undefined ? undefined : expiredIn < 1e12 ? expiredIn : Math.ceil((expiredIn - Date.now()) / 1000)
  const expiresIn = pos(b, "expires_in") ?? (at > 0 ? at : undefined)
  const interval = pos(b, "interval")
  if (!(deviceCode ?? (byUser ? userCode : undefined)) || !userCode || !uri) throw new OAuthError("invalid_device_authorization_response")
  return {
    verifier,
    userCode,
    poll: byUser ? { user_code: userCode } : { device_code: deviceCode },
    url: str(b, "verification_uri_complete") ?? uri,
    deadline: Date.now() + (expiresIn ? expiresIn * 1000 : SIGN_IN_MS),
    intervalMs: byUser && interval !== undefined ? interval : (interval ?? 5) * 1000,
  }
}

async function pollDevice(site, d) {
  let wait = d.intervalMs
  while (Date.now() < d.deadline) {
    await sleep(wait)
    const r = await postForm(site.account + "/oauth2/token", {
      grant_type: DEVICE_GRANT,
      ...d.poll,
      client_id: CLIENT_ID,
      code_verifier: d.verifier,
    })
    const status = str(r.body, "status")
    if (r.ok && status === "pending") continue
    if (r.ok && status === "slow_down") {
      wait += 5000
      continue
    }
    if (r.ok && (status === "denied" || status === "access_denied")) throw new OAuthError("access_denied", 0, "the sign-in was turned down")
    if (r.ok && (status === "expired" || status === "expired_token")) throw new OAuthError("expired_token", 0, "the sign-in code expired")
    if (r.ok) return token(r.body)
    if (r.error === "authorization_pending") continue
    if (r.error === "slow_down") {
      wait += 5000
      continue
    }
    throw new OAuthError(r.error ?? "device_authorization_failed", r.status)
  }
  throw new OAuthError("expired_token", 0, "the sign-in code expired")
}

// refreshToken trades a refresh token for a new access token.
async function refreshToken(site, refresh) {
  const r = await postForm(site.account + "/oauth2/token", {
    grant_type: "refresh_token",
    refresh_token: refresh,
    client_id: CLIENT_ID,
    scope: SCOPE,
    audience: AUDIENCE,
  })
  if (!r.ok) throw new OAuthError(r.error ?? "oauth_request_failed", r.status)
  return token(r.body, refresh)
}

// signedOut is the one answer that means the account is signed out, as
// MiniMax Code reads it: invalid_grant with HTTP 400. Anything else (a
// 5xx, a timeout, another error) is passing, and the sign-in is kept.
const signedOut = (e) => e instanceof OAuthError && e.code === "invalid_grant" && e.httpStatus === 400

class SignInExpired extends Error {
  constructor(site) {
    super(`this ${site.name} account is signed out; sign in again`)
    this.signIn = "expired"
  }
}

// ---- MiniMax Code's signed account API -------------------------------------------

// signed is a request of MiniMax Code's account API, signed as mcode signs
// one: yy over the path and query, the body ("{}" for a GET) and the time;
// x-signature over the time (and the body of a POST).
function signed(site, path, { access, userID, body, origin = site.agent, query, now = Date.now(), tz = new Date().getTimezoneOffset() * -60, platform = process.platform } = {}) {
  const url = new URL(path, origin)
  url.search = new URLSearchParams({
    device_platform: "mcode", biz_id: "3", app_id: "3001", version_code: "22201",
    ...query,
    unix: String(now), timezone_offset: String(tz), sys_language: site.lang, lang: site.lang,
    device_id: "0", os_name: platform, browser_name: "mcode", user_id: String(userID ?? "").trim() || "0", client: "mcode",
  }).toString()
  const at = url.pathname + url.search
  const ts = Math.floor(now / 1000)
  const json = body === undefined ? undefined : JSON.stringify(body)
  const headers = {
    Accept: "application/json",
    "Content-Type": "application/json",
    "User-Agent": "MiniMaxCode",
    Authorization: "Bearer " + access,
    yy: json === undefined ? md5(`${encodeURIComponent(at)}_{}${md5(String(now))}ooui`) : md5(`${encodeURIComponent(at)}_${json}${md5(String(now))}ooui`),
    "x-timestamp": String(ts),
    "x-signature": json === undefined ? md5(`${ts}${SIGN_SECRET}`) : md5(`${ts}${SIGN_SECRET}${json}`),
  }
  return { url: url.toString(), init: { method: json === undefined ? "GET" : "POST", headers, ...(json === undefined ? {} : { body: json }) } }
}

class AccountRefused extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

// checked throws on an answer MiniMax's account API says failed in its body.
function checked(env, what) {
  const si = obj(env.statusInfo)
  if (typeof si?.code === "number" && si.code !== 0) {
    if (si.code === 1000048) throw new AccountRefused(`${what}: the sign-in was refused`)
    throw new Error(`${what}: ${str(si, "message") ?? str(si, "msg") ?? "status " + si.code}`)
  }
  const br = obj(env.base_resp)
  if (typeof br?.status_code === "number" && br.status_code !== 0) throw new Error(`${what}: ${str(br, "status_msg") ?? "status " + br.status_code}`)
  return env
}

async function accountCall(site, path, opts, what) {
  const { url, init } = signed(site, path, opts)
  const res = await fetch(url, { ...init, signal: AbortSignal.timeout(15000) })
  if (res.status === 401 || res.status === 403) throw new AccountRefused(`${what}: the sign-in was refused (HTTP ${res.status})`, res.status)
  if (!res.ok) throw new Error(`${what}: HTTP ${res.status}`)
  let env
  try {
    env = obj(JSON.parse(await res.text()))
  } catch {}
  if (!env) throw new Error(`${what}: not JSON`)
  return checked(env, what)
}

// identity is who the account is: its MiniMax user id, email and name.
async function identity(site, access) {
  const env = await accountCall(site, "/v1/api/user/info", { access }, "account")
  const d = obj(env.data)
  const u = obj(d?.userInfo) ?? obj(d?.user_info) ?? obj(env.userInfo) ?? obj(env.user_info)
  const id = str(u, "realUserID") ?? str(u, "real_user_id")
  if (!id) throw new Error("account: no user id in the reply")
  const first = (...ks) => ks.map((k) => str(u, k)).find(Boolean)
  return { realUserID: id, email: first("userEmail", "email", "userMail", "user_email"), name: first("name", "userName", "user_name") }
}

// membershipOf reads a membership answer (or a workspace) as mcode does.
function membershipOf(e) {
  const d = obj(e?.data)
  const pick = (k) => e?.[k] ?? d?.[k]
  const s = (k) => (typeof pick(k) === "string" && pick(k).trim() ? pick(k).trim() : undefined)
  const has = typeof pick("has_token_plan") === "boolean" ? pick("has_token_plan") : undefined
  const ends = num(pick("token_plan_expires_at"))
  const sum = obj(e?.op_credit_summary) ?? obj(d?.op_credit_summary)
  const bal = str(sum, "total_remaining_amount") ?? (() => {
    const v = pick("opcredit_balance")
    return typeof v === "string" && v.trim() ? v.trim() : typeof v === "number" && Number.isFinite(v) ? String(v) : undefined
  })()
  return {
    ...(has !== undefined ? { hasTokenPlan: has } : {}),
    ...(s("op_group_id") ? { opGroupId: s("op_group_id") } : {}),
    ...(s("token_plan_tier") ? { tier: s("token_plan_tier") } : {}),
    ...(ends > 0 ? { expiresAt: ends } : {}),
    ...(bal !== undefined ? { balance: bal } : {}),
  }
}

// membership is the account's plan and credits: its own workspace's
// (workspace_type 0), with what the membership API says of that workspace.
async function membership(site, access, userID) {
  let ws
  try {
    const env = await accountCall(site, "/matrix/api/v1/user/get_user_extra_info", { access, userID, body: {} }, "workspace")
    const list = Array.isArray(env.workspaces) ? env.workspaces : Array.isArray(env.data?.workspaces) ? env.data.workspaces : []
    for (const w of list) {
      if (num(w?.workspace_type) !== 0) continue
      const id = typeof w.workspace_id === "number" && w.workspace_id >= 0 ? w.workspace_id : str(w, "workspace_id")
      if (id !== undefined) {
        ws = { id, m: membershipOf(w) }
        break
      }
    }
  } catch (e) {
    if (e instanceof AccountRefused) throw e
    return membershipOf(await accountCall(site, "/matrix/api/v1/commerce/get_membership_info", { access, userID, body: {} }, "membership"))
  }
  if (!ws) return {}
  try {
    const m = membershipOf(await accountCall(site, "/matrix/api/v1/commerce/get_membership_info", { access, userID, body: { workspace_id: ws.id } }, "membership"))
    const has = ws.m.hasTokenPlan === true || m.hasTokenPlan === true ? true : ws.m.hasTokenPlan === false || m.hasTokenPlan === false ? false : undefined
    return { ...ws.m, ...m, ...(has !== undefined ? { hasTokenPlan: has } : {}), ...(ws.m.opGroupId || m.opGroupId ? { opGroupId: ws.m.opGroupId ?? m.opGroupId } : {}) }
  } catch (e) {
    if (e instanceof AccountRefused) throw e
    return ws.m
  }
}

// planWindows reads the M Plan's windows from coding_plan/remains, as
// magpie's own MiniMax plan reader (readMiniMaxPlan) does: each model
// group's interval and week, "general" the plan's own, the rest aside.
function planWindows(body) {
  const br = obj(body?.base_resp)
  if (br && typeof br.status_code === "number" && br.status_code !== 0) throw new Error(str(br, "status_msg") ?? `MiniMax said ${br.status_code}`)
  if (!br && !Array.isArray(body?.model_remains)) throw new Error("no plan in the reply")
  const ms = (n) => (n < 1e12 ? n * 1000 : n)
  const zero = (v) => num(v) === 0
  const out = []
  for (const k of Array.isArray(body.model_remains) ? body.model_remains : []) {
    const name = String(k?.model_name ?? "").trim()
    if (!name || (num(k.current_interval_status) === 3 && num(k.current_weekly_status) === 3 && zero(k.current_interval_total_count) && zero(k.current_weekly_total_count))) continue
    const general = name.toLowerCase() === "general"
    for (const x of [
      { left: num(k.current_interval_remaining_percent), status: num(k.current_interval_status) ?? 0, start: num(k.start_time) ?? 0, end: num(k.end_time) ?? 0, week: false },
      { left: num(k.current_weekly_remaining_percent), status: num(k.current_weekly_status) ?? 0, start: num(k.weekly_start_time) ?? 0, end: num(k.weekly_end_time) ?? 0, week: true },
    ]) {
      if (x.status === 3 || (x.left === undefined && x.status !== 2)) continue
      const w = { used: x.status === 2 ? 100 : Math.max(0, Math.min(100, 100 - x.left)) }
      let span = 0
      if (x.start > 0 && x.end > x.start) span = ms(x.end) - ms(x.start)
      else if (x.week) span = 7 * 86400_000
      if (span) w.span = span / 1000
      if (x.end > 0) w.resetsAt = new Date(ms(x.end)).toISOString()
      w.name = x.week ? "7 days"
        : span > 86400_000 && span % 86400_000 === 0 ? `${span / 86400_000} days`
        : span >= 3600_000 && span % 3600_000 === 0 ? `${span / 3600_000} hours`
        : span > 0 ? `${Math.floor(span / 60_000)} minutes`
        : "Allowance"
      if (!general) {
        w.name = name[0].toUpperCase() + name.slice(1) + " · " + w.name
        w.aside = true
      }
      out.push(w)
    }
  }
  return out
}

async function planQuota(site, access, group) {
  const res = await fetch(site.platform + "/v1/api/openplatform/coding_plan/remains", {
    headers: { Accept: "application/json", Authorization: "Bearer " + access, ...(group ? { "X-Group-Id": group } : {}) },
    signal: AbortSignal.timeout(15000),
  })
  if (!res.ok) throw new Error(`M Plan: HTTP ${res.status}`)
  return planWindows(await res.json())
}

// credits says a balance as a count of credits: "1234.50" is "1234.5 credits".
function credits(b) {
  const n = Number(b)
  if (!Number.isFinite(n)) return String(b)
  return (Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/0+$/, "").replace(/\.$/, "")) + " credits"
}

// usageOf is the card: the plan (the M Plan's tier, else Free), the credits
// left, and the M Plan's windows when there are any.
function usageOf(m, windows = []) {
  const plan = m.hasTokenPlan ? (m.tier ? "M Plan " + m.tier : "M Plan") : "Free"
  return {
    plan,
    ...(m.hasTokenPlan && m.expiresAt ? { until: new Date(m.expiresAt < 1e12 ? m.expiresAt * 1000 : m.expiresAt).toISOString() } : {}),
    ...(m.balance !== undefined ? { balance: credits(m.balance) } : {}),
    windows,
  }
}

// ---- check-in ----------------------------------------------------------------

// MiniMax pays a few credits for the daily check-in (每日签到). The panel
// lists the week's seven days — status 1 not started, 2 claimable, 3
// claimed, 4 not available — and the claim answers 1 claimed now or 2
// already today. Both calls use the same signed account client as the rest
// of the account API: the gateway checks the signature, and a Bearer-only
// Bun fetch is not what MiniMax Code sends.
//
// They go as MiniMax Code's own /checkin sends them (its daily-check-in
// gateway, 0.6.12): as the account, user_id its realUserID — MiniMax Code
// won't ask without one, and every minimax-cloud call it makes names the
// user — and the desktop's device_platform web, is_desktop 1 and its
// version. Its host is the account API's (agent.minimaxi.com on the China
// site, agent.minimax.io on the international one), not the chat host:
// MiniMax Code's minimax-cloud origin table has these.
//
// A 401 there ({"error":"invalid access token"}) is answered as MiniMax
// Code answers it: the sign-in renewed (a newer token if one is had, else
// refreshed), the user id read again for that token, and the check-in
// asked once more. The chat path does the same on its 401, which is why a
// token minimax-cloud turned away still chatted (Magic_zero on Discord).
const CHECKIN_QUERY = { device_platform: "web", is_desktop: "1", desktop_version: "0.6.12" }

async function checkinCall(site, access, userID, path, body) {
  const opts = { access, userID, origin: site.agent, query: CHECKIN_QUERY }
  if (body !== undefined) opts.body = body
  return accountCall(site, path, opts, "check-in")
}

// checkinOf reads the panel and claims today: claimed (checked in now,
// credit = the points it paid), done (already today), inactive (the day
// hasn't started, or the panel has none), ineligible (the day isn't
// available); a throw is failed. streak counts the claimed days up to and
// including today.
async function checkinOf(site, access, userID) {
  if (!String(userID ?? "").trim()) userID = (await identity(site, access)).realUserID
  const panel = await checkinCall(site, access, userID, "/minimax-cloud/api/v1/signin/status")
  const days = Array.isArray(panel?.data?.days) ? panel.data.days : null
  if (!days?.length) throw new Error("check-in: no panel in the reply")
  const sorted = [...days].sort((a, b) => (a?.day_no ?? 0) - (b?.day_no ?? 0))
  if (sorted.length !== 7 || sorted.some((d, i) => d?.day_no !== i + 1)) throw new Error("check-in: malformed panel")
  const todays = sorted.filter((d) => d?.is_today)
  const claimable = sorted.filter((d) => d?.status === 2)
  if (todays.length > 1 || claimable.length > 1) throw new Error("check-in: malformed panel")
  const today = todays[0]
  if (!today) return { outcome: "inactive" }
  const streakFrom = (i) => {
    let n = 0
    for (let j = i; j >= 0 && sorted[j]?.status === 3; j--) n++
    return n
  }
  if (today.status === 3) {
    const points = pos(today, "points")
    return { outcome: "done", ...(points ? { credit: points } : {}), streak: streakFrom(sorted.indexOf(today)) }
  }
  if (today.status === 2) {
    const claim = await checkinCall(site, access, userID, "/minimax-cloud/api/v1/signin/claim", {})
    const r = pos(claim?.data, "claim_result")
    const credit = pos(claim?.data, "points")
    const got = { ...(credit ? { credit } : {}), streak: streakFrom(sorted.indexOf(today) - 1) + 1 }
    if (r === 1) return { outcome: "claimed", ...got }
    if (r === 2) return { outcome: "done", ...got }
    throw new Error(`check-in: unknown claim_result ${JSON.stringify(claim?.data?.claim_result)}`)
  }
  if (today.status === 4) return { outcome: "ineligible" }
  return { outcome: "inactive" } // 1: the day hasn't started
}

// ---- requests -----------------------------------------------------------------

// QUOTA says a refusal is about the account's credits or plan running out.
const QUOTA = /insufficient|balance|credit|quota|exhaust|limit|余额|积分|额度|不足|用完|上限/i

// answer is the upstream's answer as magpie reads it: a refusal for running
// out of credits a 429 (so another account can take over), the sign-in
// noted as kept, or renewed when this request refreshed it.
async function answer(res, renewed) {
  const headers = new Headers(res.headers)
  headers.set("X-Magpie-Sign-In", renewed && res.ok ? "renewed" : "kept")
  if (res.status !== 402 && res.status !== 403 && res.status !== 429) return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
  const text = await res.text()
  headers.delete("content-length")
  headers.delete("content-encoding")
  if (res.status === 429 || !QUOTA.test(text)) return new Response(text, { status: res.status, statusText: res.statusText, headers })
  let msg = text.trim()
  try {
    const v = JSON.parse(text)
    msg = v?.error?.message ?? v?.message ?? v?.msg ?? v?.base_resp?.status_msg ?? msg
  } catch {}
  headers.set("content-type", "application/json")
  return new Response(JSON.stringify({ type: "error", error: { type: "rate_limit_error", message: "usage limit reached: " + msg } }), { status: 429, statusText: "Too Many Requests", headers })
}

// ---- the plugins ----------------------------------------------------------------

// MiniMax spends a refresh token once: the refresh gives a new one, and
// the old one asked again is invalid_grant, which signs the account out
// (MiniMax Code takes a lock across its processes for it). So a refresh
// token is never sent twice from here.
//
// renewing is each refresh under way, by site and the refresh token it
// spends; renewed is what each gave, kept a minute once saved so a request
// that read the old token after the refresh was saved joins it rather than
// spending the old token again, and kept for good when the save failed:
// the account's newest tokens are then here only.
const renewing = new Map()
const renewed = new Map()

// latest is the sign-in a with the newest tokens this process got for it.
function latest(site, a) {
  for (let i = 0; i < 16 && a.refresh; i++) {
    const done = renewed.get(site.id + "\n" + a.refresh)
    if (!done || (done.saved && Date.now() - done.at >= KEEP_RENEWED_MS)) break
    a = { ...a, ...done.got }
  }
  return a
}

// renew spends a's refresh token, once: those who come while it runs wait
// for it. save keeps what it gave, saying whether it could.
function renew(site, a, save) {
  const key = site.id + "\n" + a.refresh
  let r = renewing.get(key)
  if (!r) {
    r = (async () => {
      const t = await refreshToken(site, a.refresh)
      const got = { access: t.access, refresh: t.refresh, expires: t.expires }
      const done = { got, at: Date.now(), saved: false }
      renewed.set(key, done)
      done.saved = await save({ ...a, ...got })
      done.at = Date.now()
      return got
    })().finally(() => renewing.delete(key))
    renewing.set(key, r)
  }
  return r
}

function makePlugin(site) {
  return async ({ client }) => {
    const save = async (a) => {
      if (typeof client?.auth?.set !== "function") return false
      try {
        await client.auth.set({ path: { id: site.id }, body: a })
        return true
      } catch {
        return false
      }
    }

    // fresh is the account with an access token that isn't about to end,
    // refreshed (and saved) when it is, or when MiniMax turned away the
    // access token rejected and no newer one is had. renewed says the
    // token isn't the one getAuth read: this call or another refreshed it.
    const fresh = async (getAuth, rejected, again = false) => {
      const stored = await getAuth()
      if (stored?.type !== "oauth" || !stored.access) throw new Error(`${site.name} isn't signed in`)
      const a = latest(site, stored)
      const newer = a.access !== stored.access
      if (rejected !== undefined && a.access !== rejected) return { a, renewed: true }
      const force = rejected !== undefined
      if (!force && !(a.expires && Date.now() >= a.expires - EARLY_MS)) return { a, renewed: newer }
      if (!a.refresh) {
        if (Date.now() < a.expires) return { a, renewed: newer }
        throw new SignInExpired(site)
      }
      try {
        return { a: { ...a, ...(await renew(site, a, save)) }, renewed: true }
      } catch (e) {
        if (signedOut(e)) {
          // spent by someone else (magpie's renewal, another process of
          // magpie's), who saved the new one: that one is used
          const now = again ? undefined : await getAuth().catch(() => undefined)
          if (now?.type === "oauth" && now.refresh && now.refresh !== a.refresh && now.refresh !== stored.refresh) return fresh(getAuth, rejected, true)
          throw new SignInExpired(site)
        }
        // passing: the token in hand may still do
        if (!force && Date.now() < a.expires) return { a, renewed: newer }
        throw Object.assign(new Error(`${site.name} token refresh: ${e?.message ?? e}`), { signIn: "kept" })
      }
    }

    // one conversation id for requests that come with none of their own
    const session = randomUUID()

    return {
      config: async (config) => {
        config.provider ??= {}
        config.provider[site.id] ??= {}
        const p = config.provider[site.id]
        p.name ??= site.name
        p.npm ??= NPM
        p.api ??= api(site)
        p.models = { ...configModels(), ...(p.models ?? {}) }
      },
      // the conversation's id, which MiniMax Code sends with each request
      "chat.headers": async (input, output) => {
        const id = input?.model?.providerID ?? input?.provider?.info?.id
        if (id !== site.id || !input?.sessionID) return
        output.headers["X-Mavis-Session-Id"] = input.sessionID
      },
      provider: {
        id: site.id,
        // MiniMax Code's live list, when it answers; else the list above
        async models(provider, { auth }) {
          if (auth?.type !== "oauth" || !auth.access) return provider.models
          try {
            const { a } = await fresh(async () => auth)
            const ms = await liveModels(site, a.access)
            if (!ms.length) return provider.models
            return Object.fromEntries(ms.map((m) => [m.id, modelOf(site, provider, m)]))
          } catch (e) {
            if (e instanceof SignInExpired) throw e
            return provider.models
          }
        },
      },
      auth: {
        provider: site.id,
        // magpie renews the sign-in LEAD_MS before its end, once for the
        // account, before its requests, models and usage ask for it; the
        // check before each request below stays for OpenCode, which
        // doesn't call this
        refreshLead: LEAD_MS,
        async refresh(auth) {
          if (auth?.type !== "oauth" || !auth.access || !auth.refresh) return undefined
          const a = latest(site, auth)
          // renewed here already, the store not yet saying so
          if (a.access !== auth.access && Date.now() < a.expires - LEAD_MS) return { access: a.access, refresh: a.refresh, expires: a.expires }
          try {
            // magpie saves what this gives
            return await renew(site, a, async () => true)
          } catch (e) {
            if (signedOut(e)) throw new SignInExpired(site)
            throw e
          }
        },
        async loader(getAuth) {
          const auth = await getAuth()
          if (auth?.type !== "oauth") return {}
          return {
            baseURL: api(site),
            apiKey: PLACEHOLDER_KEY,
            async fetch(input, init) {
              const req = input instanceof Request ? input : null
              let body = init?.body
              if (body === undefined && req) body = await req.clone().text()
              const send = (a) => {
                const headers = new Headers(init?.headers ?? req?.headers)
                headers.delete("content-length")
                headers.set("Authorization", "Bearer " + a.access)
                headers.set("x-api-key", PLACEHOLDER_KEY)
                headers.set("User-Agent", "MiniMaxAgent")
                headers.set("X-Mavis-Agent-Id", "main")
                headers.set("X-Mavis-Timezone-Offset", String(new Date().getTimezoneOffset() * -60))
                if (!headers.get("X-Mavis-Session-Id")) headers.set("X-Mavis-Session-Id", session)
                if (!headers.has("anthropic-version")) headers.set("anthropic-version", "2023-06-01")
                return fetch(req ? req.url : input, { ...init, method: init?.method ?? req?.method ?? "POST", headers, body })
              }
              let { a, renewed: did } = await fresh(getAuth)
              let res = await send(a)
              if (res.status === 401) {
                // the token was turned away before its end: a newer one
                // if there is one, else refreshed, and sent once more
                ;({ a } = await fresh(getAuth, a.access))
                did = true
                res = await send(a)
              }
              return answer(res, did)
            },
          }
        },
        // the account's credits and M Plan
        async usage(getAuth) {
          const auth = await getAuth()
          if (auth?.type !== "oauth" || !auth.access) return { error: "not signed in" }
          let a, did
          try {
            ;({ a, renewed: did } = await fresh(getAuth))
          } catch (e) {
            return { error: e?.message ?? String(e), ...(e?.signIn ? { signIn: e.signIn } : {}) }
          }
          try {
            const who = a.realUserID ? { realUserID: a.realUserID, email: a.email, name: a.name } : await identity(site, a.access)
            const m = await membership(site, a.access, who.realUserID)
            let windows = []
            let error
            if (m.hasTokenPlan && m.opGroupId) {
              try {
                windows = await planQuota(site, a.access, m.opGroupId)
              } catch (e) {
                error = e?.message ?? String(e)
              }
            }
            return {
              ...usageOf(m, windows),
              ...(error ? { error } : {}),
              user: who.email || who.name || who.realUserID,
              signIn: did ? "renewed" : "kept",
            }
          } catch (e) {
            return { error: e?.message ?? String(e), signIn: "kept" }
          }
        },
        // the daily check-in (每日签到): magpie runs it once a day per
        // account, and its Check in now button asks right away; the
        // outcome lands on the account's Usage card.
        async checkin(getAuth) {
          const auth = await getAuth()
          if (auth?.type !== "oauth" || !auth.access) return { outcome: "failed", message: "not signed in" }
          let a
          try {
            ;({ a } = await fresh(getAuth))
          } catch (e) {
            return { outcome: "failed", message: e?.message ?? String(e), ...(e?.signIn ? { signIn: e.signIn } : {}) }
          }
          const failed = (e) => ({ outcome: "failed", message: e?.message ?? String(e), ...(e?.signIn ? { signIn: e.signIn } : {}) })
          try {
            return await checkinOf(site, a.access, a.realUserID)
          } catch (e) {
            if (!(e instanceof AccountRefused && e.status === 401)) return failed(e)
            // MiniMax Code's /checkin on a 401: renewed, and asked once more
            try {
              ;({ a } = await fresh(getAuth, a.access))
            } catch (e2) {
              return e2 instanceof SignInExpired ? failed(e2) : failed(e)
            }
            try {
              return await checkinOf(site, a.access, undefined)
            } catch (e2) {
              return failed(e2)
            }
          }
        },
        methods: [
          {
            type: "oauth",
            label: `${site.name} account`,
            async authorize() {
              const d = await startDevice(site)
              return {
                url: d.url,
                instructions: `Sign in to ${site.name} in the browser and confirm the code ${d.userCode}; this finishes by itself.`,
                method: "auto",
                async callback() {
                  try {
                    const t = await pollDevice(site, d)
                    let who = {}
                    try {
                      who = await identity(site, t.access)
                    } catch {}
                    const uid = who.realUserID ?? t.accountID ?? t.subject ?? ""
                    return {
                      type: "success",
                      access: t.access,
                      refresh: t.refresh,
                      expires: t.expires,
                      accountId: who.email || who.name || uid || site.name,
                      uid,
                      ...(who.realUserID ? { realUserID: who.realUserID } : {}),
                      ...(who.email ? { email: who.email } : {}),
                      ...(who.name ? { name: who.name } : {}),
                    }
                  } catch (e) {
                    return { type: "failed", error: `${site.name} sign-in: ${e?.message ?? e}` }
                  }
                },
              }
            },
          },
        ],
      },
    }
  }
}

export const MiniMaxCodeAuthPlugin = makePlugin(SITES["minimax-code"])
export const MiniMaxCodeGlobalAuthPlugin = makePlugin(SITES["minimax-code-global"])

// for tests
export const _internal = { SITES, MODELS, signed, md5, parseModels, planWindows, membershipOf, usageOf, credits, variants, renewing, renewed, startDevice, pollDevice, checkinOf }
