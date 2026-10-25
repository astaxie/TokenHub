// ZCode's GLM Coding Plan, as an OpenCode provider plugin.
//
// ZCode (zcode.z.ai) signs in to a Z.ai account, or a BigModel (智谱,
// bigmodel.cn) one, through a flow zcode.z.ai opens and is polled for. With
// the account's sign-in it finds or makes the coding plan key it uses (the
// key named zcode-api-key in the account's default project) and sends the
// models' requests, Anthropic's Messages, to the plan's endpoint:
// api.z.ai/api/anthropic or open.bigmodel.cn/api/anthropic.
//
// An account with no plan of its own may have a seat on a team's plan (a
// project of type 2, and its zcode-team-api-key), or ZCode's free Start
// Plan, served by zcode.z.ai itself to ZCode's own session token (a JWT
// that can't be refreshed: when it runs out, sign in again).
//
// The sign-in is kept as OpenCode keeps an OAuth one: `access` is the key,
// `refresh` the rest of it as JSON (site, base, key, jwt, team project,
// plan, device id), `accountId` the account's email.

import { createDecipheriv, createHash } from "node:crypto"
import { existsSync, readFileSync, readdirSync } from "node:fs"
import { arch, homedir, release, userInfo } from "node:os"
import { isAbsolute, join } from "node:path"
import PROMPT from "./prompt.mjs"

const PROVIDER = "zcode"
const ZCODE = "https://zcode.z.ai"
const ZAI_API = "https://api.z.ai"
const BIGMODEL_API = "https://bigmodel.cn" // BigModel's business API, where ZCode asks it
const ZAI_BASE = "https://api.z.ai/api/anthropic"
const BIGMODEL_BASE = "https://open.bigmodel.cn/api/anthropic"
const START_BASE = ZCODE + "/api/v1/zcode-plan/anthropic"
const APP_VERSION = "3.16.3" // the ZCode whose sign-in this makes
const UA = "ZCode/" + APP_VERSION

// ZCode's models before its config is read; the Start Plan has all but GLM-5.3.
const MODELS = [
  { id: "GLM-5.3", context: 1_000_000, output: 128_000, efforts: ["low", "high", "max"] },
  { id: "GLM-5.3-Flash", context: 1_000_000, output: 128_000, efforts: ["low", "high", "max"] },
  { id: "GLM-5.2", context: 1_000_000, output: 128_000, efforts: ["none", "high", "max"] },
  { id: "GLM-5-Turbo", context: 200_000, output: 64_000, efforts: ["none", "high"] },
]
const START_MODELS = MODELS.filter((m) => m.id !== "GLM-5.3")

const SITES = {
  zai: { name: "Z.ai", api: ZAI_API, base: ZAI_BASE, subscribe: "z.ai/subscribe" },
  bigmodel: { name: "BigModel", api: BIGMODEL_API, base: BIGMODEL_BASE, subscribe: "bigmodel.cn/glm-coding" },
}
const siteOf = (s) => (s === "bigmodel" ? "bigmodel" : "zai")

const platform = () => `${process.platform}-${process.arch}` // darwin-arm64, win32-x64, linux-x64, as ZCode names them
const uuid = () => crypto.randomUUID()
const anyDevice = uuid() // X-Device-Mid for calls made signed out (the model list)
const first = (...ss) => ss.map((s) => (typeof s === "string" ? s.trim() : "")).find((s) => s) ?? ""

// STATUS_TEXT is Go's http.StatusText, which magpie's built-in said a
// refusal with no message in.
const STATUS_TEXT = { 400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed",
  408: "Request Timeout", 429: "Too Many Requests", 500: "Internal Server Error", 502: "Bad Gateway", 503: "Service Unavailable",
  504: "Gateway Timeout" }

class ZError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

// call asks one of Z.ai's JSON endpoints, which wrap what they say in
// {code, msg, data}: a code of 0 or 200 is a success. zcode.z.ai is told
// the machine's id (X-Device-Mid), as ZCode tells it.
async function call(method, url, { auth, body, headers, device, signal } = {}) {
  const h = { "Content-Type": "application/json", Accept: "application/json", "User-Agent": UA }
  if (auth) h.Authorization = auth
  if (url.startsWith(ZCODE + "/")) h["X-Device-Mid"] = device || anyDevice
  Object.assign(h, headers ?? {})
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body), signal })
  const text = await res.text()
  let env
  try {
    env = JSON.parse(text)
  } catch {}
  const code = env?.code == null ? "" : String(env.code)
  if (!res.ok) {
    if (env?.msg) throw new ZError(code && code !== "0" ? `${env.msg} (${res.status}, code ${code})` : `${env.msg} (${res.status})`, res.status)
    throw new ZError(STATUS_TEXT[res.status] ?? `${res.status} ${res.statusText}`.trim(), res.status)
  }
  if (!env || typeof env !== "object") throw new ZError("not JSON", res.status)
  if (code && code !== "0" && code !== "200") throw new ZError(env.msg || `error ${code}`, res.status)
  return env.data ?? null
}

// ---- the account's plan ----------------------------------------------------------

const rootOf = (base) => {
  try {
    const u = new URL(base)
    return `${u.protocol}//${u.host}`
  } catch {
    return ZAI_API
  }
}
const isTeam = (s) => !!(s.org && s.project)

// plan names the coding plan a key has, "" for none.
async function plan(s) {
  const subs = (await call("GET", rootOf(s.base) + "/api/biz/subscription/list", { auth: s.key })) ?? []
  return (Array.isArray(subs) ? subs : []).find((x) => String(x.status).toUpperCase() === "VALID")?.productName ?? ""
}

function jwtExpiry(jwt) {
  const p = String(jwt ?? "").split(".")
  if (p.length !== 3) return 0
  try {
    const claims = JSON.parse(Buffer.from(p[1], "base64url").toString("utf8"))
    return Number(claims.exp) > 0 ? Number(claims.exp) * 1000 : 0
  } catch {
    return 0
  }
}
const jwtExpired = (jwt) => {
  const t = jwtExpiry(jwt)
  return t > 0 && Date.now() > t
}
// EXPIRED is the built-in's words (errZCodeExpired) for a Start Plan
// sign-in past its end.
const EXPIRED = "ZCode's sign-in has expired; sign in to ZCode again (or add the account again in magpie)"

const num = (v) => {
  const n = typeof v === "string" ? parseFloat(v) : v
  return typeof n === "number" && Number.isFinite(n) ? n : undefined
}

// the alias is the catalog spelling of the model plus -trial, so the picker
// reads GLM-5.3-Trial while the loader strips it back to the raw name
const giftID = (m) => (MODELS.find((x) => x.id.toLowerCase() === String(m).toLowerCase())?.id ?? m) + "-Trial"
const uniqueModels = (names) => {
  const byName = new Map()
  for (const m of names) if (!byName.has(m.toLowerCase())) byName.set(m.toLowerCase(), m)
  return [...byName.values()]
}
const giftNames = (g) => g?.allModels ?? g?.models ?? []
const giftModel = (g, id) => giftNames(g).find((m) => giftID(m).toLowerCase() === id.toLowerCase())
const bucketModels = (x) => (Array.isArray(x.capabilities) ? x.capabilities : [])
  .map((c) => String(c ?? "").trim()).filter((c) => c.startsWith("model:") || !c.includes(":"))
  .map((c) => c.replace(/^model:/, "").trim()).filter(Boolean)

// Both the card and the request route use the same test for a spent bucket.
// Older balances may omit remaining_units but still tell used and total.
const giftSpent = (x) => {
  const left = num(x.remaining_units)
  if (left !== undefined) return left <= 0
  const total = num(x.total_units), used = num(x.used_units)
  return total !== undefined && used !== undefined && used >= total
}

// startPlan is the gift an account can spend now ({models}), as ZCode reads
// its balance: several plans can be active at once, and a model routes to
// the gift when any of them has a live bucket serving it — a bucket past
// its plan's own end, or one whose remaining_units says none, is skipped
// rather than asked and refused. models are those live buckets' names.
async function startPlan(jwt, device) {
  if (!jwt) throw new Error("not signed in to ZCode")
  if (jwtExpired(jwt)) throw new Error(EXPIRED)
  const b = (await call("GET", `${ZCODE}/api/v1/zcode-plan/billing/balance?app_version=${APP_VERSION}`, { auth: "Bearer " + jwt, device })) ?? {}
  const now = num(b.server_time) > 0 ? num(b.server_time) : Date.now() / 1000
  const same = (x, p) => (x.user_plan_id && p.user_plan_id ? x.user_plan_id === p.user_plan_id : String(x.plan_id ?? "") === String(p.plan_id ?? ""))
  const plans = Array.isArray(b.plans) ? b.plans : []
  const balances = Array.isArray(b.balances) ? b.balances : []
  const ms = new Set(), all = new Map()
  let ttl = 600_000
  let any = false
  let name = ""
  for (const p of plans) {
    const st = String(p?.status ?? "").trim().toLowerCase()
    const end = num(p?.ends_at)
    if (st !== "active" || (end > 0 && end <= now)) continue
    any = true
    if (end > 0) ttl = Math.min(ttl, (end - now) * 1000)
    if (!name) name = String(p?.name ?? "").trim()
    for (const x of balances) {
      if (!same(x, p)) continue
      const exp = num(x.expires_at)
      if (exp > 0 && exp <= now) continue
      if (exp > 0) ttl = Math.min(ttl, (exp - now) * 1000)
      // Spent buckets still declare models; only live ones can spend now.
      for (const m of bucketModels(x)) {
        if (!all.has(m.toLowerCase())) all.set(m.toLowerCase(), m)
        if (!giftSpent(x)) ms.add(m)
      }
    }
  }
  return any ? { name: name || "Start Plan", models: [...ms], allModels: [...all.values()], ttl } : null
}

// plansOf says where an account's requests go and what more it can spend:
// {start, gift}. start is what onStart said: an account with no coding plan
// sends every request to the gift and reads no balance to do it; a
// sign-in with no ZCode token and a key whose plan-list read found
// no gift stay on the coding plan. gift is what a coding account also holds:
// the gift plans ({models}), and its requests spend the gift for a model it
// serves, until it says it is spent (see blocked). Asked again after 10
// minutes (a minute when unsure — a plan-list read that failed is retried
// sooner; a gift read that failed simply leaves gift null until the next
// ask).
const routes = new Map()
async function plansOf(s) {
  if (!s.jwt) return { start: false, gift: null }
  if (!s.key && !isTeam(s)) return { start: true, gift: null }
  const id = s.key + "\0" + s.jwt
  const r = routes.get(id)
  if (r && Date.now() - r.at < r.ttl) return r
  let start = false
  let gift = null
  let sure = false
  try {
    // A team seat stays on its team even when its personal plan list is
    // empty. Its ZCode session still holds the person's gift plans.
    if (!isTeam(s) && !(await plan(s))) start = true
    else {
      // a gift read that failed is unknown, not "no gift": asked again in
      // a minute, so its trial entries come back with the next list (an
      // expired session is known: the card says to sign in again)
      let failed = false
      gift = await startPlan(s.jwt, s.device).catch(() => ((failed = !jwtExpired(s.jwt)), null))
      sure = !failed
    }
    // a key whose plan list read no plan, where the last read found one,
    // is asked again in a minute rather than sent to the Start Plan for
    // ten: one list without the plan (a renewal's boundary, a hiccup)
    // turned a GLM Coding Plan account's every request into the Start
    // Plan's 405 until the route was read again (#369)
    if (start) sure = !(s.key && r && !r.start)
  } catch {
    // the plan list could not be read: a key account takes the dual route
    // (start stays false, the gift is kept, a minute's ttl), so a gift
    // refusal still replays to the coding plan rather than blacking out
    try {
      gift = await startPlan(s.jwt, s.device)
      sure = false
    } catch {}
  }
  const out = { start, gift, at: Date.now(), ttl: Math.min(sure ? 600_000 : 60_000, gift?.ttl ?? 600_000) }
  routes.set(id, out)
  return out
}

// blocked remembers the models a gift said it has none left for: a key+jwt+
// model for a minute, so one turn that is refused does not ask again.
const blocked = new Map()

// giftServes: the gift's buckets name their models lowercased ("glm-5.3-
// flash"); what is asked for arrives as the plan lists it ("GLM-5.3-Flash").
const giftServes = (gift, m) => (gift.models ?? []).some((x) => x.toLowerCase() === m.toLowerCase())
const giftUsable = (s, gift, m) => !jwtExpired(s.jwt) && giftServes(gift, m) &&
  Date.now() >= (blocked.get(s.key + "\0" + s.jwt + "\0" + m.toLowerCase()) ?? 0)

// spentUp is the gift saying it has nothing left to spend: its own no-
// resource-package code 1113, its "exceed quota limit" 1005 — a spent gift
// can answer a 200 with a JSON body carrying one of those codes. Every 429
// counts as spent, Z.ai's 1302/1303 rate limits among them.
function spentUp(text, status) {
  if (status === 429) return true
  let b
  try { b = JSON.parse(text) } catch { return false }
  if (!b || typeof b !== "object") return false
  const code = (v) => String(v ?? "")
  const c1 = code(b.code), c2 = code(b.error?.code)
  return c1 === "1113" || c1 === "1005" || c2 === "1113" || c2 === "1005"
}

// ---- keys ------------------------------------------------------------------------

function teamHeaders(base, org, project) {
  return {
    "Bigmodel-Organization": org,
    "Bigmodel-Project": project,
    "Set-Language": base.includes("bigmodel.cn") ? "zh" : "en",
    "Accept-Language": "en-US,en",
  }
}

const keysURL = (root, org, project) =>
  `${root}/api/biz/v1/organization/${encodeURIComponent(org)}/projects/${encodeURIComponent(project)}/api_keys`

// projectKey finds the project's key named want.name (of want.keyType, when
// given), makes it when it isn't there and reads its secret: `<id>.<secret>`.
async function projectKey(site, keys, auth, headers, want) {
  const name = SITES[site].name
  let list
  try {
    list = (await call("GET", keys, { auth, headers })) ?? []
  } catch (e) {
    throw new Error(`${name} API keys: ${e.message}`)
  }
  const typed = "keyType" in want
  let id = ""
  for (const k of Array.isArray(list) ? list : []) {
    if (k.name === want.name && (!typed || String(k.keyType) === String(want.keyType)) && first(k.apiKey)) id = first(k.apiKey)
  }
  if (!id) {
    try {
      id = first((await call("POST", keys, { auth, headers, body: want }))?.apiKey)
    } catch (e) {
      throw new Error(`${name} API key: ${e.message}`)
    }
  }
  let secret = ""
  if (id) {
    try {
      secret = first((await call("GET", `${keys}/copy/${encodeURIComponent(id)}`, { auth, headers }))?.secretKey)
    } catch (e) {
      throw new Error(`${name} API key: ${e.message}`)
    }
  }
  if (id && !secret && typed) return id // a team's key with no secret goes as it is
  if (!id || !secret) throw new Error(`${name} gave no API key`)
  return `${id}.${secret}`
}

// teamKey is a team seat's key when the sign-in kept none.
const teamKeys = new Map()
async function teamKey(s) {
  if (s.key) return s.key
  const id = [s.token, s.org, s.project].join("\0")
  const got = teamKeys.get(id)
  if (got && (!got.err || Date.now() - got.at < 60_000)) {
    if (got.err) throw got.err
    return got.key
  }
  const site = s.base.includes("bigmodel.cn") ? "bigmodel" : "zai"
  try {
    const key = await projectKey(site, keysURL(SITES[site].api, s.org, s.project), s.token, teamHeaders(s.base, s.org, s.project), { name: "zcode-team-api-key", keyType: 2 })
    teamKeys.set(id, { key, at: Date.now() })
    return key
  } catch (e) {
    const err = new Error(`ZCode's team plan: ${e.message} — sign in to ZCode again`)
    teamKeys.set(id, { err, at: Date.now() })
    throw err
  }
}

// ---- allowance ---------------------------------------------------------------------
// As magpie's built-in told it (internal/provider zcode.go, zcode_start.go,
// zcode_team.go): the Coding Plan's windows and term, the Start Plan's
// buckets, or a team seat's windows and its plan's end.

// bizRoot is the business API of the site a plan is served from.
function bizRoot(base) {
  let h = ""
  try {
    h = new URL(base).host.toLowerCase()
  } catch {}
  return h === new URL(BIGMODEL_BASE).host || h.endsWith("bigmodel.cn") ? BIGMODEL_API : rootOf(base)
}

// spanOf reads a limit's window, in seconds: unit 3 counts hours, 6 weeks
// (and 1, 4, 5 minutes, days and months by the same count). A team's five
// hours come with no count.
function spanOf(unit, n) {
  n = Math.trunc(Number(n) || 0)
  if (unit === 3 && n <= 0) n = 5
  n = Math.max(n, 1)
  return { 1: 60, 3: 3600, 4: 86400, 5: 30 * 86400, 6: 7 * 86400 }[unit] * n || 0
}

function windowName(span) {
  if (span === 0) return "Credits"
  if (span < 86400) return `${Math.trunc(span / 3600)} hours`
  if (span === 7 * 86400) return "Weekly"
  if (span >= 28 * 86400) return "Monthly"
  return `${Math.trunc(span / 86400)} days`
}

// compact is a count as magpie says one: whole, else to two places.
const compact = (n) => (Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/0+$/, "").replace(/\.$/, ""))
const isNum = (v) => typeof v === "number" && Number.isFinite(v)

// limitWindows are /api/monitor/usage/quota/limit's limits as windows: what
// is used of the whole when both are told (the whole less what remains, or
// the current value), else the percentage given. TIME_LIMIT is the month's
// MCP tool calls, which ZCode shows but never stops the models on, so it is
// set aside, as is a limit whose whole is told as 0: no cap (an older
// plan's), which the vendor may still give as 100% used. A window whose
// count is told carries it as amount of limit, so magpie's card says the
// count as used or as left, as it says the share beside it (#659).
function limitWindows(d) {
  const out = []
  for (const x of d?.limits ?? []) {
    const span = spanOf(Math.trunc(Number(x?.unit) || 0), x?.number)
    const w = { name: windowName(span), used: 0 }
    if (String(x?.type ?? "").toUpperCase() === "TIME_LIMIT") (w.name = "MCP · Month"), (w.aside = true)
    if (isNum(x?.percentage)) w.used = x.percentage
    if (x?.usage === 0) (w.used = 0), (w.aside = true)
    if (isNum(x?.usage) && x.usage > 0) {
      const total = x.usage
      if (isNum(x.remaining)) {
        const used = total - x.remaining
        w.used = (100 * used) / total
        w.amount = used
        w.limit = total
        w.display = `${compact(used)} / ${compact(total)}`
      } else if (isNum(x.currentValue)) {
        if (!isNum(x.percentage)) w.used = (100 * x.currentValue) / total
        w.amount = x.currentValue
        w.limit = total
        w.display = `${compact(x.currentValue)} / ${compact(total)}`
      }
    }
    const reset = Math.trunc(Number(x?.nextResetTime) || 0)
    if (reset > 0) w.resetsAt = new Date(reset).toISOString()
    if (span) w.span = span
    out.push(w)
  }
  return out
}

// beijing reads the plan's times, "2026-10-18 12:00:00" in Beijing: ms, or 0.
function beijing(s) {
  s = String(s ?? "").trim().replace(" ", "T")
  if (!/^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2})?$/.test(s)) return 0
  const t = Date.parse(s + (s.length === 10 ? "T00:00:00" : "") + "+08:00")
  return Number.isFinite(t) ? t : 0
}

// termOf is how long a GLM Coding plan is paid for: the valid
// subscription's next renewal, a charge when it renews itself, else the
// end of its time, the last date its "valid" span names.
function termOf(subs) {
  for (const x of Array.isArray(subs) ? subs : []) {
    if (String(x?.status ?? "").toUpperCase() !== "VALID") continue
    const auto = x.autoRenew === true || x.autoRenew === 1
    const t = beijing(typeof x.nextRenewTime === "string" ? x.nextRenewTime : "")
    if (t) return { until: new Date(t).toISOString(), renew: auto ? "auto" : "off" }
    const ds = String(typeof x.valid === "string" ? x.valid : "").match(/\d{4}-\d{2}-\d{2}(?:[ T]\d{2}:\d{2}:\d{2})?/g) ?? []
    if (ds.length && !auto) {
      const end = beijing(ds[ds.length - 1])
      if (end) return { until: new Date(end).toISOString(), renew: "off" }
    }
    return {}
  }
  return {}
}

// codingUsage is a Coding Plan key's allowance: credits per five hours and
// per week, the plan's level and its term.
async function codingUsage(s) {
  const root = rootOf(s.base)
  const d = (await call("GET", root + "/api/monitor/usage/quota/limit", { auth: s.key })) ?? {}
  const out = {}
  const level = typeof d.level === "string" ? d.level : ""
  if (level) out.plan = "GLM Coding " + level[0].toUpperCase() + level.slice(1)
  out.windows = limitWindows(d)
  const resets = personalResets(root, s.key)
  let subs = null
  try {
    subs = await call("GET", root + "/api/biz/subscription/list", { auth: s.key })
    Object.assign(out, termOf(subs))
  } catch {}
  const r = await resets
  if (r) out.resets = r
  // a key whose account has no plan in force, read as such, says so
  // rather than showing an empty card: what a key can spend is a GLM
  // Coding Plan alone (a ZCode gift plan is never one, see NO_PLAN_KEY).
  // A list that couldn't be read, a level, a counted window or a plan
  // the sign-in saved keeps the card as it was.
  const valid = (Array.isArray(subs) ? subs : []).some((x) => String(x?.status ?? "").toUpperCase() === "VALID")
  if (subs !== null && !valid && !level && !s.plan && !out.windows.some((w) => !w.aside)) return { error: NO_PLAN_KEY }
  return out
}

// NO_PLAN_KEY is a key with no GLM Coding Plan behind it. ZCode's gift
// plans (its Start Plan, ZCode 1024 Dev Day) are served by zcode.z.ai
// to ZCode's own session token, which a sign-in with ZCode (the app's,
// or Z.ai's or BigModel's in the browser) carries and a key never does:
// with a key, a gift-only account has nothing to spend, and Z.ai answers
// each request 1113 (rtbeilin on X, 10-24).
const NO_PLAN_KEY =
  "this key's account has no GLM Coding Plan; ZCode's gift plans (the Start Plan, ZCode 1024 Dev Day) are spent only with ZCode's sign-in, never with a key — add the account with \"ZCode app's sign-in\" or a ZCode sign-in in the browser"

// noPlanKey is a coding plan's 1113 refusal (no plan, no resource
// package) to a sign-in with no ZCode session, its message followed by
// NO_PLAN_KEY; anything else goes on as it came. Read only once the
// answer has failed.
function noPlanKey(res, text) {
  let v
  try {
    v = JSON.parse(text)
  } catch {
    return null
  }
  // Z.ai names the code in error.code ("1113", as magpie's
  // zhipu_coding_test has it) or error.type (its 401's "1000"), BigModel's
  // envelope in code
  if (![v?.error?.code, v?.error?.type, v?.code].some((c) => String(c ?? "") === "1113")) return null
  const said = first(v?.error?.message, v?.msg, v?.message)
  const out = v?.error && typeof v.error === "object" ? { ...v, error: { ...v.error, message: (said ? said + " — " : "") + NO_PLAN_KEY } } : { ...v, msg: (said ? said + " — " : "") + NO_PLAN_KEY }
  const h = new Headers(res.headers)
  h.set("content-type", "application/json")
  return new Response(JSON.stringify(out), { status: res.status, statusText: res.statusText, headers: h })
}

// startNum reads a number the balance gives as a number or a string.
function startNum(v) {
  if (isNum(v)) return v
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v.trim())
    if (!Number.isNaN(n)) return n
  }
  return undefined
}

// giftOf reads the balance and returns the gift's card fields: its plans and
// windows. Several plans can be active at once, so every live plan's buckets
// show, each keeping its own models and end; the plan and end put on the
// card (for a gift-only account) are the soonest-ending live plan's. null
// when there is no live plan to show.
async function giftOf(s) {
  if (!s.jwt) throw new Error("not signed in to ZCode")
  if (jwtExpired(s.jwt)) throw new Error(EXPIRED)
  const b = (await call("GET", `${ZCODE}/api/v1/zcode-plan/billing/balance?app_version=${APP_VERSION}`, { auth: "Bearer " + s.jwt, device: s.device })) ?? {}
  const str = (v) => (typeof v === "string" ? v : "")
  const plans = (Array.isArray(b.plans) ? b.plans : []).map((p) => ({ ...p, status: str(p?.status), user_plan_id: str(p?.user_plan_id), plan_id: str(p?.plan_id) }))
  const now = startNum(b.server_time) > 0 ? startNum(b.server_time) : Math.trunc(Date.now() / 1000)
  const over = new Set()
  for (const p of plans) {
    const end = startNum(p.ends_at)
    if (end > 0 && end <= now && p.status.trim().toLowerCase() === "active") p.status = "expired"
    if (p.status.trim().toLowerCase() === "expired") over.add(p.user_plan_id + "\0" + p.plan_id)
  }
  const same = (x, p) => (x.user_plan_id && p.user_plan_id ? x.user_plan_id === p.user_plan_id : String(x.plan_id ?? "") === String(p.plan_id ?? ""))
  const balances = (Array.isArray(b.balances) ? b.balances : [])
    .map((x) => ({ ...x, user_plan_id: str(x?.user_plan_id), plan_id: str(x?.plan_id) }))
    .filter((x) => {
      let keep = true
      for (const p of plans) {
        if (!same(x, p)) continue
        keep = !over.has(p.user_plan_id + "\0" + p.plan_id)
        if (keep) break
      }
      return keep
    })

  // every live plan counts as the gift: no plan-name filter decides it,
  // the balance says what the session can spend
  let active = null
  for (const p of plans) {
    if (p.status.trim().toLowerCase() !== "active") continue
    const end = startNum(p.ends_at)
    if (end > 0 && end <= now) continue
    if (active && !(end > 0 && (!startNum(active.end) || end < startNum(active.end)))) continue
    active = { name: first(str(p.name), "Start Plan"), end, until: end > 0 ? new Date(Math.trunc(end) * 1000).toISOString() : "" }
  }
  if (!active) return null
  const out = { plan: active.name, windows: [] }
  if (active.until) Object.assign(out, { until: active.until, renew: "off" })
  const live = plans.filter((p) => {
    if (p.status.trim().toLowerCase() !== "active") return false
    const end = startNum(p.ends_at)
    return !(end > 0 && end <= now)
  })
  for (const x of balances) {
    const owner = live.find((p) => same(x, p))
    if (!owner) continue
    const exp = startNum(x.expires_at)
    if (exp > 0 && exp <= now) continue // a bucket past its own end is over
    const total = startNum(x.total_units), left = startNum(x.remaining_units)
    let used = startNum(x.used_units)
    if (total === undefined && used === undefined && left === undefined) continue
    const models = bucketModels(x)
    const base = first(str(x.show_name), models.join(", "), "Credits")
    const w = { name: base, used: 0, _plan: str(owner.name), _spent: giftSpent(x) }
    if (used === undefined) used = total !== undefined && left !== undefined ? total - left : 0
    if (total > 0) {
      // the bucket's count as amount of limit, as the Coding windows' is
      // (#659): magpie says it as used or as left, as it says the share
      w.used = (100 * used) / total
      w.amount = used
      w.limit = total
      w.display = `${compact(used)} / ${compact(total)}`
    }
    if (w._spent) w.used = Math.max(100, w.used)
    if (exp > 0) w.resetsAt = new Date(Math.trunc(exp) * 1000).toISOString()
    let span = 0
    for (const p of plans) {
      if (!same(x, p)) continue
      for (const e of Array.isArray(p.entitlements) ? p.entitlements : []) {
        if (str(e?.entitlement_id) === str(x.entitlement_id)) span = periodOf(str(e?.period))
      }
    }
    if (!span) {
      const a = startNum(x.period_start), z = startNum(x.period_end)
      if (a !== undefined && z !== undefined && z > a) span = Math.trunc(z - a)
    }
    if (span) w.span = span
    if (models.length) w.models = models
    out.windows.push(w)
  }
  return out
}

// CLAIM_HINT is the claim line's name: a fixed sentence, so magpie
// translates it with the interface's own language (the plan's name and
// the count are data, and go in the display, which is shown as it came).
const CLAIM_HINT = "Gift plans to claim in the ZCode app"

// claimHint is the card's line for gift plans ZCode is holding for the
// account but has not put on it: GET /billing/preview lists them, and
// only the ZCode app claims one — its claim takes the Aliyun captcha
// attestation the app's own renderer makes, so the plugin reads the list
// and asks the user to claim it there. A window, set aside (it is no
// allowance: using it up stops nothing), so the card shows it and
// routing, caps and the menu bar pass it over. The window's name is a
// fixed sentence, so magpie can translate it (a display is shown as it
// came); the count and the plan's own name go in the display. null when
// there is nothing to claim, the preview failing or listing none among
// it: a failed read is no line at all, never a card or a refusal of its
// own.
async function claimHint(s) {
  if (!s.jwt || jwtExpired(s.jwt)) return null
  let d
  try {
    d = await call("GET", `${ZCODE}/api/v1/zcode-plan/billing/preview?app_version=${APP_VERSION}&platform=${platform()}`, { auth: "Bearer " + s.jwt, device: s.device })
  } catch {
    return null
  }
  const plans = (Array.isArray(d?.plans) ? d.plans : []).filter((p) => typeof p?.plan_id === "string" && p.plan_id.trim())
  if (!plans.length) return null
  const n = plans.length
  const plan = first(...plans.map((p) => (typeof p?.name === "string" ? p.name : "")), "ZCode gift plan")
  return { name: CLAIM_HINT, used: 0, aside: true, display: `${n} · ${plan}` }
}

// GIFT_SIGN_IN is the card's line for an account whose gift plans can't
// be read: ZCode's gift plans (the Start Plan, ZCode 1024 Dev Day, ZCode
// Trust Build) are served by zcode.z.ai to ZCode's own session alone, so
// a GLM Coding Plan key, or a sign-in whose ZCode session has run out
// (that token can't be renewed), lists no <model>-Trial entries and
// spends the coding plan alone. A fixed sentence, so magpie can
// translate it; why goes in the display. Set aside: it stops nothing.
const GIFT_SIGN_IN = "ZCode gift plans (-Trial models) need ZCode's sign-in"
function giftSignIn(s, key) {
  if (!s.jwt) return { name: GIFT_SIGN_IN, used: 0, aside: true, display: key ? "not read with an API key: add the account with a ZCode sign-in" : "this sign-in has no ZCode session: sign in again" }
  if (jwtExpired(s.jwt)) return { name: GIFT_SIGN_IN, used: 0, aside: true, display: "ZCode's sign-in has expired: sign in again" }
  return null
}

// startUsage is a gift-only account's card: its buckets, or the words
// for having none to show. The card's header already names the plan, so
// the windows of one plan keep their own names; with several plans each
// bucket is named after its plan (see byPlan).
const NO_START = "this account has no GLM Coding Plan, and ZCode's Start Plan has ended or was never started"
async function startUsage(s) {
  const g = await giftOf(s)
  if (!g) return { error: NO_START }
  const best = bestBuckets(g.windows)
  const label = byPlan(g.windows)
  g.windows = countedFirst(g.windows.map(({ _plan, _spent, ...w }, i) => {
    w.name = label(w.name, _plan)
    if (!w.models?.length) return w
    // a model two plans give (Trust Build's and the Start Plan's Flash)
    // counts on the bucket ZCode spends now; the other stays on the card
    // without holding the model up (ganlerk, Discord)
    const models = w.models.filter((m) => best.get(m.toLowerCase()) === i)
    if (!models.length) {
      delete w.models
      return { ...w, aside: true }
    }
    return { ...w, models: [...models, ...models.map(giftID)] }
  }))
  return g
}

// byPlan names a bucket after its plan ("ZCode 1024 Dev Day · GLM-5.3")
// when the gift's buckets come from more than one plan: two plans each
// give a GLM-5.3, and named alike a spent one and a live one can't be
// told apart (01huadalang, Discord: the new-user plan's spent GLM-5.3
// showed, the 1024 plan's own GLM-5.3 didn't). One plan's buckets keep
// their own names, as the card's header names the plan.
function byPlan(windows) {
  const several = new Set(windows.map((w) => w._plan || "")).size > 1
  return (name, plan) => (several && plan ? plan + " · " + name : name)
}

// countedFirst puts the buckets a request spends before those set aside
// (a spent bucket another plan's live one stands in for), each group in
// the balance's order: magpie's account row shows a card's first two
// windows and leaves the rest to its tooltip, so a spent plan listed first
// hid the live plan's allowance for the same model.
const countedFirst = (ws) => [...ws.filter((w) => !w.aside), ...ws.filter((w) => w.aside)]

// bestBuckets is, for each model the gift's buckets serve, the index of
// the one a request spends: a live one before a spent one, the least
// used of those. The server picks among an account's buckets itself
// (ZCode sends no plan with a request), so one spent bucket doesn't stop
// a model another still has.
function bestBuckets(windows) {
  const best = new Map()
  windows.forEach((w, i) => {
    for (const m of w.models ?? []) {
      const key = m.toLowerCase(), old = best.get(key), prev = windows[old]
      if (old === undefined || (prev._spent && !w._spent) || (prev._spent === w._spent && w.used < prev.used)) best.set(key, i)
    }
  })
  return best
}

// dualUsage shows both pools but only the pool a request can use counts
// for its model. Live gift models don't spend the Coding windows; spent
// or blocked gift buckets stay visible without holding up the Coding Plan.
// Fresh usage also updates the request cache, so a replenished or spent
// gift takes effect before its ten-minute route cache expires. A failed
// gift read leaves the working Coding Plan's card and route alone.
async function dualUsage(s, out) {
  out ??= await codingUsage(s)
  const cached = routes.get(s.key + "\0" + s.jwt)
  try {
    const g = await giftOf(s)
    const gift = g ? { name: g.plan, models: [...new Set(g.windows.filter((w) => !w._spent)
      .flatMap((w) => w.models ?? []).map((m) => m.toLowerCase()))],
      allModels: uniqueModels(g.windows.flatMap((w) => w.models ?? [])) } : null
    if (cached) {
      cached.gift = gift
      const ends = g ? [g.until, ...g.windows.map((w) => w.resetsAt)].map((t) => Date.parse(t)).filter(Number.isFinite) : []
      if (ends.length) cached.ttl = Math.min(cached.ttl, Math.max(0, Math.min(...ends) - cached.at))
    }
    if (g) {
      // the gift's models are its trial entries alone: a plain model stays
      // on the coding plan, and the user picks the gift by its entry
      const notModels = gift.allModels.map((m) => giftID(m).toLowerCase())
      if (notModels.length) out.windows = out.windows.map((w) => w.aside ? w : { ...w, notModels })
      // Several buckets may serve one model: one spent sibling cannot block it.
      const best = bestBuckets(g.windows)
      const label = byPlan(g.windows)
      out.windows.push(...countedFirst(g.windows.map(({ _plan: p, _spent, ...w }, i) => {
        const selected = gift.allModels.filter((m) => best.get(m.toLowerCase()) === i)
        const models = selected.map(giftID)
        const scoped = models.length ? { ...w, models } : { ...w, aside: true }
        // the card names a bucket's model as the picker does (GLM-5.3-Flash-Trial);
        // no plan prefix while one plan gives them all — the card's section
        // header already says the account; with several, byPlan names each
        let name = w.name
        for (const m of selected) {
          const at = name.toLowerCase().indexOf(m.toLowerCase())
          if (at >= 0) name = name.slice(0, at) + giftID(m) + name.slice(at + m.length)
        }
        return { ...scoped, name: label(name, p) }
      })))
    }
  } catch {
    if (cached) {
      cached.gift = null
      cached.at = Date.now()
      cached.ttl = 60_000 // an uncertain gift is read again sooner
    }
  }
  return out
}

// periodOf reads an entitlement's period: "daily", "weekly", "monthly".
function periodOf(p) {
  p = p.toLowerCase()
  if (p.includes("day") || p.includes("daily")) return 86400
  if (p.includes("week")) return 7 * 86400
  if (p.includes("month")) return 30 * 86400
  return 0
}

// when is a team plan's end: a time in Beijing, or a Unix time in seconds
// or milliseconds; ms, or 0.
function when(v) {
  if (typeof v === "string") {
    const t = beijing(v)
    if (t) return t
  }
  const n = startNum(v)
  if (n > 0) return n > 1e12 ? Math.trunc(n) : Math.trunc(n) * 1000
  return 0
}

// teamUsage is a team seat's allowance: the team plan's five hours and
// week, and the plan's name and end.
async function teamUsage(s, key) {
  const root = bizRoot(s.base)
  const headers = teamHeaders(s.base, s.org, s.project)
  const d = (await call("GET", root + "/api/monitor/usage/quota/limit?type=2", { auth: key, headers })) ?? {}
  const out = { windows: limitWindows(d) }
  if (!s.token) return out
  const resets = teamResets(root, s.token, headers)
  try {
    const t = (await call("GET", root + "/api/biz/team/subscribe/product/querySubscribeDetail", { auth: s.token, headers })) ?? {}
    const usable = (t.hasSubscription == null || t.hasSubscription === true) && String(t.status ?? "").toUpperCase() === "EFFECTIVE" &&
      String(t.memberGrantStatus ?? "").toUpperCase() === "VALID"
    if (usable) {
      if (typeof t.productName === "string" && t.productName) out.plan = t.productName
      const end = when(t.subscribeEndTime)
      if (end) Object.assign(out, { until: new Date(end).toISOString(), renew: "off" })
    }
  } catch {}
  const r = await resets
  if (r) out.resets = r
  return out
}

// teamResets is how many resets a team member may spend, of the five hours
// and of the week, marked a team's (team: true), as the built-in's
// zhipuTeamResets did. null when it can't be told or there are none.
async function teamResets(root, auth, headers) {
  const r = await resetsOf(root + "/api/biz/customer-package-reset/list?targetType=TEAM", auth, headers)
  if (r) r.team = true
  return r
}

// personalResets is how many resets a GLM Coding Plan of a person's own
// holds, as bigmodel.cn's usage page lists them, asked with the plan's
// key (the built-in's zhipuPersonalResets). Only read: a reset is spent
// on the vendor's own page, never from here.
async function personalResets(root, key) {
  return resetsOf(root + "/api/biz/customer-package-reset/list?targetType=PERSONAL", key)
}

// resetsOf reads customer-package-reset/list: the resets of the five
// hours and of the week, of which only those still available count (one
// run out stays listed a week, available false), each with its own end
// (each, soonest first, one with no end last) and the soonest as until.
// null when the list can't be read, so no count is shown rather than 0,
// and when it holds none.
async function resetsOf(url, auth, headers) {
  let d
  try {
    d = (await call("GET", url, { auth, headers })) ?? {}
  } catch {
    return null
  }
  let until = 0
  const each = []
  const count = (rs, window) => {
    let n = 0
    for (const x of Array.isArray(rs) ? rs : []) {
      if (x?.available !== true) continue
      n++
      const t = when(x.expireTime)
      if (t && (!until || t < until)) until = t
      each.push(t ? { until: new Date(t).toISOString(), window } : { window })
    }
    return n
  }
  const fiveHour = count(d.fiveHourResets, "fiveHour")
  const weekly = count(d.weekResets, "weekly")
  if (fiveHour + weekly === 0) return null
  const end = (c) => (c.until ? Date.parse(c.until) : Infinity)
  each.sort((a, b) => (end(a) === end(b) ? 0 : end(a) < end(b) ? -1 : 1))
  const r = { count: fiveHour + weekly, byWindow: true, fiveHour, weekly }
  if (until) r.until = new Date(until).toISOString()
  r.each = each
  return r
}

// ---- signing in ------------------------------------------------------------------

// bizAuth is the Authorization a sign-in gives the business API: Z.ai's
// token exchanged for a business one, as a Bearer; BigModel's goes bare.
async function bizAuth(site, token) {
  if (site === "bigmodel") return token
  let biz
  try {
    biz = await call("POST", ZAI_API + "/api/auth/z/login", { body: { token } })
  } catch (e) {
    throw new Error(`Z.ai sign-in: ${e.message}`)
  }
  if (!biz?.access_token) throw new Error("Z.ai sign-in: no token")
  return "Bearer " + biz.access_token
}

// mintKey is the account's own coding plan key, in its default project
// ("默认机构" / "默认项目", else the first), and the plan it has.
async function mintKey(site, root, auth, info) {
  let org = ""
  let proj = ""
  for (const o of info?.organizations ?? []) {
    const ps = (o.projects ?? []).filter((p) => p.projectId && String(p.projectType) !== "2")
    if (!o.organizationId || !ps.length) continue
    const def = (ps.find((p) => String(p.projectName ?? "").includes("默认项目")) ?? ps[0]).projectId
    const named = String(o.organizationName ?? "").includes("默认机构")
    if (!org || named) {
      org = o.organizationId
      proj = def
      if (named) break
    }
  }
  if (!org) throw new Error(`this ${SITES[site].name} account has no project for an API key`)
  const key = await projectKey(site, keysURL(root, org, proj), auth, undefined, { name: "zcode-api-key" })
  const s = { key, base: SITES[site].base }
  try {
    return { s, plan: await plan(s) }
  } catch (e) {
    throw new Error(`GLM Coding Plan: ${e.message}`)
  }
}

// teamSignIn is the account's seat on a team's plan: the first team project
// whose plan is in force and gives it a seat, and that project's key.
async function teamSignIn(site, root, auth, info) {
  const base = SITES[site].base
  const ps = []
  for (const o of info?.organizations ?? [])
    for (const p of o.projects ?? []) if (o.organizationId && p.projectId && String(p.projectType) === "2") ps.push({ org: o.organizationId, project: p.projectId })
  if (!ps.length) return { err: "no team plan", none: true }
  let why = ""
  let last = ""
  for (const p of ps) {
    let d
    try {
      d = (await call("GET", root + "/api/biz/team/subscribe/product/querySubscribeDetail", { auth, headers: teamHeaders(base, p.org, p.project) })) ?? {}
    } catch (e) {
      last = e.message
      continue
    }
    const status = String(d.status ?? "").toUpperCase()
    const grant = String(d.memberGrantStatus ?? "").toUpperCase()
    if (!(d.hasSubscription !== false && status === "EFFECTIVE" && grant === "VALID")) {
      if (!why && status === "EFFECTIVE" && grant === "UNASSIGNED")
        why = `this ${SITES[site].name} account is in a team with a GLM Coding Plan but has no seat on it yet — ask the team's admin to give it one, then sign in again`
      if (!why && status === "EXPIRED") why = `the ${SITES[site].name} team's GLM Coding Plan this account is in has expired`
      continue
    }
    let key
    try {
      key = await projectKey(site, keysURL(root, p.org, p.project), auth, teamHeaders(base, p.org, p.project), { name: "zcode-team-api-key", keyType: 2 })
    } catch (e) {
      return { err: `the team's GLM Coding Plan: ${e.message}`, why: `the team's GLM Coding Plan: ${e.message}` }
    }
    return { s: { key, base, token: auth, org: p.org, project: p.project }, plan: first(d.productName, "GLM Coding Team") }
  }
  if (why) return { err: why, why }
  const err = `the team's GLM Coding Plan: ${last || "no seat on it"}`
  return { err, why: err }
}

// signedIn is what a sign-in gives: the account's coding plan key and plan;
// or, with none of its own, a seat on a team's plan; or else ZCode's token
// for its Start Plan while the account has one. Asked for the team's plan
// (onTeam), it is the team seat alone, as ZCode's plan picker gives a team
// plan beside the account's own (a "team-coding-plan" selection): an
// account with both is signed in to each as an account of its own (#285).
async function signedIn(site, token, jwt, device, onTeam = false) {
  const name = SITES[site].name
  const root = SITES[site].api
  let s = null
  let err = null
  let team = ""
  if (onTeam) {
    const auth = await bizAuth(site, token)
    let info
    try {
      info = await call("GET", root + "/api/biz/customer/getCustomerInfo", { auth })
    } catch (e) {
      throw new Error(`${name} account: ${e.message}`)
    }
    const t = await teamSignIn(site, root, auth, info)
    if (t.s) return { ...t.s, jwt, plan: t.plan }
    if (t.none) throw new Error(`this ${name} account has no seat on a team's GLM Coding Plan — sign in with "ZCode: ${name} GLM Coding Plan" for its own plan`)
    throw new Error(t.why ?? t.err)
  }
  try {
    const auth = await bizAuth(site, token)
    let info
    try {
      info = await call("GET", root + "/api/biz/customer/getCustomerInfo", { auth })
    } catch (e) {
      throw new Error(`${name} account: ${e.message}`)
    }
    const own = await mintKey(site, root, auth, info).catch((e) => ({ e }))
    if (own.e) err = own.e
    else if (own.plan) return { ...own.s, jwt, plan: own.plan }
    else s = own.s
    const t = await teamSignIn(site, root, auth, info)
    if (t.s) return { ...t.s, jwt, plan: t.plan }
    team = t.why ?? ""
  } catch (e) {
    err = e
  }
  let berr = null
  if (jwt) {
    try {
      const sp = await startPlan(jwt, device)
      if (sp) return { ...(err || !s?.key ? { base: SITES[site].base } : s), jwt, plan: sp.name }
    } catch (e) {
      berr = e
    }
  }
  if (team && berr) throw new Error(`${team}; ZCode's Start Plan: ${berr.message}`)
  if (team) throw new Error(team)
  if (err && berr) throw new Error(`${err.message}; ZCode's Start Plan: ${berr.message}`)
  if (err) throw err
  if (berr) throw new Error(`this ${name} account has no GLM Coding Plan, of its own or a team's, and ZCode's Start Plan could not be read: ${berr.message}`)
  throw new Error(
    `this ${name} account has no GLM Coding Plan, of its own or a team's, and ZCode's Start Plan has ended or was never started — subscribe at ${SITES[site].subscribe}, then sign in again`,
  )
}

// TEAM marks a team seat's account name beside the account's own.
const TEAM = "team"
const who = (u) => first(String(u?.email ?? "").replace(/@phone\.local$/, ""), u?.name, u?.user_id, "ZCode")
// uidOf is the account's user id, kept beside its name: magpie tells two
// accounts of one name apart by it, a name being no one's own (wakkana on
// Discord, WorkBuddy's)
const uidOf = (u) => (u?.user_id === undefined || u?.user_id === null ? "" : String(u.user_id).trim())
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// authorize opens ZCode's sign-in flow on site; its callback polls it.
// onTeam signs in to the account's seat on a team's plan, named apart from
// the account's own plan (the host keeps one entry per account name).
async function authorize(site, log, onTeam = false) {
  const poll = "Bearer " + Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString("hex")
  const device = uuid()
  let flow
  try {
    flow = await call("POST", ZCODE + "/api/v1/oauth/cli/init", { auth: poll, body: { provider: site }, device })
  } catch (e) {
    throw new Error(`ZCode sign-in: ${e.message}`)
  }
  let u
  try {
    u = new URL(flow?.authorize_url)
  } catch {}
  if (!flow?.flow_id || u?.protocol !== "https:") throw new Error("ZCode gave no sign-in page")
  // where Z.ai or BigModel sends the browser back, as ZCode sets it (BigModel's page names it redirect)
  const back = `${ZCODE}/app/oauth/login?` + new URLSearchParams({ redirect: "zcode://oauth/callback", app_version: APP_VERSION })
  u.searchParams.set(site === "bigmodel" ? "redirect" : "redirect_uri", back)
  const interval = Math.max(Number(flow.poll_interval_sec) || 0, 1) * 1000
  const deadline = Number(flow.expires_at) > 0 ? Number(flow.expires_at) * 1000 : Date.now() + 5 * 60_000
  return {
    url: u.toString(),
    instructions: `Sign in to ${SITES[site].name} in the browser; this finishes on its own.`,
    method: "auto",
    async callback() {
      const fail = (msg) => (log(msg), { type: "failed", error: msg })
      for (;;) {
        await sleep(interval)
        if (Date.now() > deadline) return fail("the sign-in expired; start again")
        let got
        try {
          got = (await call("GET", `${ZCODE}/api/v1/oauth/cli/poll/${encodeURIComponent(flow.flow_id)}`, { auth: poll, device })) ?? {}
        } catch (e) {
          if (e.status >= 400 && e.status < 500 && e.status !== 408 && e.status !== 429) return fail("ZCode sign-in: " + e.message)
          continue // a hiccup: ask again
        }
        const token = site === "bigmodel" ? first(got.bigmodel?.access_token, got.bigmodel?.accessToken) : first(got.zai?.access_token)
        if (got.status === "pending" || !got.status) continue
        if (got.status === "failed") return fail(`the sign-in was declined on ${SITES[site].name}`)
        if (got.status !== "ready" || !token) return fail("ZCode sign-in: unexpected answer " + got.status)
        let s
        try {
          s = await signedIn(site, token, first(got.token), device, onTeam)
        } catch (e) {
          return fail(e.message)
        }
        const state = { site, device, ...s }
        return {
          type: "success",
          refresh: JSON.stringify(state),
          access: s.key || s.jwt || "",
          // the key doesn't run out; ZCode's token, when it is all there is, does
          expires: s.key || isTeam(s) ? 0 : jwtExpiry(s.jwt),
          accountId: onTeam ? `${who(got.user)} (${TEAM})` : who(got.user),
          ...(uidOf(got.user) ? { uid: uidOf(got.user) } : {}),
        }
      }
    },
  }
}

// ---- ZCode's own sign-in ------------------------------------------------------------
// ZCode keeps its sign-in in ~/.zcode/v2/credentials.json, each value
// encrypted (AES-256-GCM, "enc:v1:<iv>.<tag>.<data>") with a key made from
// the machine's user; ~/.zcode/v2/setting.json says whether it is on a
// team's plan. An account signed in "as ZCode" reads them for every
// request, so it follows ZCode's own sign-in, its renewed session and its
// switches between plans, as magpie's built-in did (zcode.go zcodeOwn).
//
// ZCode 3.14 can move its data (数据存储路径, "data storage path"): it copies
// ~/.zcode/v2 to <folder>/.zcode/v2, all but setting.json, and from its next
// start reads credentials.json and runtime/provider from there. The choice
// stays in the home folder's setting.json as dataBaseDir, which ZCode's main
// process reads before anything else (readBootstrapDataBaseDirFromDisk);
// setting.json itself, and the team plan it names, stay in the home folder.

// settingDir is where ZCode keeps setting.json, moved or not.
const settingDir = () => join(homedir(), ".zcode", "v2")

// dataIn is the folder ZCode keeps its .zcode data in: the home folder,
// unless dataBaseDir moved it. A value that isn't an absolute path is
// ignored, as magpie's built-in does (internal/appdir ZCodeDataIn).
function dataIn() {
  const d = readJSON(join(settingDir(), "setting.json"))?.dataBaseDir
  const t = typeof d === "string" ? d.trim() : ""
  return t && isAbsolute(t) ? t : homedir()
}

// zcodeDir is where ZCode keeps credentials.json and runtime/provider.
const zcodeDir = () => join(dataIn(), ".zcode", "v2")

// secret is the key ZCode encrypts its credentials with.
function secret() {
  let seed = process.env.ZCODE_CREDENTIAL_SECRET
  if (!seed) {
    let name = ""
    try {
      name = userInfo().username
    } catch {}
    name = name.slice(name.lastIndexOf("\\") + 1) // DOMAIN\user
    seed = `zcode-credential-fallback:${process.platform}:${homedir()}:${name}`
  }
  return createHash("sha256").update(seed).digest()
}

function decrypt(key, v) {
  if (typeof v !== "string" || !v.startsWith("enc:v1:")) return null
  const parts = v.slice(7).split(".")
  if (parts.length !== 3) return null
  try {
    const [iv, tag, data] = parts.map((p) => Buffer.from(p.replace(/=+$/, ""), "base64url"))
    const d = createDecipheriv("aes-256-gcm", key, iv)
    d.setAuthTag(tag)
    return Buffer.concat([d.update(data), d.final()]).toString("utf8")
  } catch {
    return null
  }
}

function readJSON(path) {
  try {
    return JSON.parse(readFileSync(path, "utf8"))
  } catch {
    return null
  }
}

// ownTeam is the team plan ZCode switched to, when it did.
function ownTeam(store, key) {
  const set = readJSON(join(settingDir(), "setting.json"))
  if (!set) return null
  const families = set.providerFamilyDomain === "zai" || set.providerFamilyDomain === "bigmodel" ? [set.providerFamilyDomain] : ["zai", "bigmodel"]
  for (const fam of families) {
    let org = ""
    let project = ""
    const sel = set.providerFamilyConnectionSelections?.[fam]
    if (sel) {
      if (sel.kind === "team-coding-plan") [org, project] = [first(sel.organizationId), first(sel.projectId)]
    } else {
      // before providerFamilyConnectionSelections
      const legacy = first(set.modelProviderFamilySelectedKeys?.[fam])
      const pre = `team-plan:builtin:${fam}-coding-plan:`
      if (legacy.startsWith(pre)) {
        const ps = legacy.slice(pre.length).split(":")
        if (ps.length === 3) {
          try {
            ;[org, project] = [decodeURIComponent(ps[1]), decodeURIComponent(ps[2])]
          } catch {}
        }
      }
    }
    if (!org || !project) continue
    const tok = first(decrypt(key, store[`oauth:${fam}:access_token`]))
    if (!tok) continue
    // a stale token ZCode won't use either
    if (fam === "bigmodel" && first(decrypt(key, store.zcodejwttoken)) === tok) continue
    return { site: fam, base: SITES[fam].base, key: "", token: tok, org, project }
  }
  return null
}

// configKey is the coding plan key ZCode's config.json gives its built-in
// coding plan provider, Z.ai's first, as ZCode lists them; ZCode reads it
// as it is, and one written encrypted is read too.
function configKey(key) {
  const providers = readJSON(join(zcodeDir(), "config.json"))?.provider
  if (!providers || typeof providers !== "object") return null
  for (const [ids, base] of [
    [["builtin:zai-coding-plan", "account:zai-individual-coding-plan"], ZAI_BASE],
    [["builtin:bigmodel-coding-plan", "account:bigmodel-individual-coding-plan"], BIGMODEL_BASE],
  ]) {
    for (const id of ids) {
      let k = providers[id]?.options?.apiKey
      if (typeof k !== "string") continue
      k = k.trim()
      if (k.startsWith("enc:")) k = first(decrypt(key, k))
      if (k.includes(".") && !/[\s{$]/.test(k)) return { key: k, base }
    }
  }
  return null
}

// ownSignIn is what ZCode is signed in to now: the account, its coding
// plan key, ZCode's session token (the Start Plan's key) and the team plan
// it is switched to; null when it has none of them.
function ownSignIn() {
  const store = readJSON(join(zcodeDir(), "credentials.json"))
  if (!store || typeof store !== "object") return null
  const key = secret()
  let s = { key: "", base: "" }
  for (const [name, v] of Object.entries(store)) {
    // account-provider:coding-plan:account:zai-individual-coding-plan:account:<uuid>:api-key
    if (!name.includes(":coding-plan:") || !name.endsWith(":api-key")) continue
    const k = decrypt(key, v)
    if (!k || !k.includes(".")) continue
    const base = name.includes(":bigmodel-") ? BIGMODEL_BASE : ZAI_BASE
    // Z.ai's first, as ZCode lists it
    if (!s.key || (base === ZAI_BASE && s.base !== ZAI_BASE)) s = { key: k, base }
  }
  // ZCode 3.15 keeps no plan key in its credentials; the one it uses sits
  // in config.json, the provider's options.apiKey (yetone/magpie#2130)
  if (!s.key) s = configKey(key) || s
  const jwt = first(first(decrypt(key, store.zcodejwttoken)).replace(/^Bearer /, ""))
  const team = ownTeam(store, key)
  if (team) s = { ...team, jwt }
  else s = { ...s, jwt, base: s.base || ZAI_BASE, site: s.base === BIGMODEL_BASE ? "bigmodel" : "zai" }
  if (!s.key && !s.jwt && !isTeam(s)) return null
  let info = {}
  for (const name of ["oauth:zai:user_info", "oauth:bigmodel:user_info"]) {
    const v = decrypt(key, store[name])
    if (v && !first(info.email, info.name, info.user_id)) {
      try {
        info = JSON.parse(v)
      } catch {}
    }
  }
  return { ...s, user: who(info), uid: uidOf(info) }
}

// asZCode is the sign-in "as ZCode": what it reads is ZCode's own.
function asZCode() {
  return {
    url: "",
    instructions: "Uses the account the ZCode app is signed in to, and follows it while ZCode stays signed in to that account.",
    method: "auto",
    async callback() {
      const own = ownSignIn()
      if (!own) return { type: "failed", error: "ZCode isn't signed in on this computer; sign in to ZCode, or sign in to Z.ai or BigModel here" }
      const { user, uid, ...s } = own
      const state = { source: "zcode", device: anyDevice, ...s }
      return { type: "success", refresh: JSON.stringify(state), access: s.key || s.jwt || "", expires: 0, accountId: user, ...(uid ? { uid } : {}) }
    },
  }
}

// stateOf is a stored sign-in as the requests need it.
function stateOf(auth) {
  if (auth?.type === "oauth") {
    let s = {}
    try {
      s = JSON.parse(auth.refresh)
    } catch {}
    if (s.source === "zcode") {
      // ZCode's own, read now, while ZCode is signed in to this same
      // account. Signed out, or signed in to another account since, it is
      // what it was when this was signed in: another account's key would
      // send this account's requests as that one, and an expired session
      // says sign in again (EXPIRED) rather than borrowing another's.
      const own = ownSignIn()
      // (by user id where both have one: another account may share the name)
      if (own && own.user === String(auth.accountId ?? "") && (!own.uid || !auth.uid || own.uid === String(auth.uid))) {
        const { user, uid: _uid, ...now } = own
        s = { source: "zcode", device: s.device, plan: s.plan, ...now }
      }
    }
    const site = siteOf(s.site)
    return { ...s, site, base: s.base || SITES[site].base, key: s.key ?? (s.jwt ? "" : auth.access), device: s.device || anyDevice }
  }
  if (auth?.type === "api") {
    const site = siteOf(auth.metadata?.site)
    return { site, base: SITES[site].base, key: String(auth.key ?? "").trim(), device: anyDevice }
  }
  return null
}

// ---- models ----------------------------------------------------------------------

// planID is ZCode's providerId for the plan served at base; a team's plan
// has the individual one's models.
const planID = (base) =>
  base.includes("/zcode-plan/")
    ? "account:zai-start-plan"
    : base.includes("bigmodel.cn")
      ? "account:bigmodel-individual-coding-plan"
      : "account:zai-individual-coding-plan"

const efforts = (vs) => [...new Set(vs.map((v) => (v === "disabled" ? "none" : v === "enabled" ? "high" : v)))]

// modelsOf is a plan's models in ZCode's provider config.
function modelsOf(cfg, plan) {
  const c = cfg?.config ?? {}
  const ids = []
  const on = new Set()
  const add = (id) => {
    if (!on.has(id.toLowerCase())) on.add(id.toLowerCase()), ids.push(id)
  }
  for (const r of c.providerConfigRules?.providerRules ?? []) if (r.providerId === plan) (r.builtinModelIds ?? []).forEach(add)
  const off = new Set()
  for (const r of c.modelConfigRules?.builtinProviderModelRules ?? []) {
    if (r.providerId !== plan) continue
    if (r.config?.enabled === false) off.add(String(r.modelId).toLowerCase())
    else add(r.modelId)
  }
  const out = []
  for (const id of ids) {
    if (off.has(id.toLowerCase())) continue
    const m = { id, context: 0, output: 0, efforts: [] }
    for (const r of c.modelConfigRules?.modelRules ?? []) {
      let re
      try {
        re = new RegExp(`^(?:${r.modelMatch})$`, "i")
      } catch {
        continue
      }
      if (!re.test(id)) continue
      const x = r.config ?? {}
      if (x.properties?.contextWindow > 0) m.context = x.properties.contextWindow
      if (x.optionSpecs?.maxOutputTokens?.max > 0) m.output = x.optionSpecs.maxOutputTokens.max
      if (typeof x.properties?.inputFormat?.supportsImage === "boolean") m.image = x.properties.inputFormat.supportsImage
      if (x.optionSpecs?.reasoningLevel?.values?.length) m.efforts = efforts(x.optionSpecs.reasoningLevel.values)
    }
    out.push(m)
  }
  return out
}

// localConfig is the latest provider config an installed ZCode has.
function localConfig() {
  const files = []
  const rt = join(zcodeDir(), "runtime", "provider")
  const walk = (dir, depth) => {
    let es = []
    try {
      es = readdirSync(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of es) {
      if (depth < 3 && e.isDirectory()) walk(join(dir, e.name), depth + 1)
      else if (depth === 3 && e.name === "zcode-builtin.json") files.push(join(dir, e.name))
    }
  }
  walk(rt, 0)
  if (process.platform === "darwin") files.push("/Applications/ZCode.app/Contents/Resources/config/provider/zcode-builtin.json")
  let best = null
  for (const f of files) {
    try {
      if (!existsSync(f)) continue
      const b = JSON.parse(readFileSync(f, "utf8"))
      if (!best || (b.revision ?? 0) > (best.revision ?? 0)) best = b
    } catch {}
  }
  return best
}

// remoteConfig is the release of ZCode's provider config zcode.z.ai names now.
async function remoteConfig() {
  const signal = AbortSignal.timeout(8000)
  const c = await call("GET", `${ZCODE}/api/v1/client/configs?app_version=${APP_VERSION}&platform=${platform()}`, { signal })
  const url = c?.configs?.builtin_provider_config_json
  if (!String(url ?? "").startsWith("https://")) throw new Error("ZCode's configs name no provider config")
  const res = await fetch(url, { headers: { "User-Agent": UA }, signal })
  if (!res.ok) throw new Error(`ZCode's provider config: ${res.status}`)
  return res.json()
}

// config is ZCode's provider config, the later of zcode.z.ai's and an
// installed ZCode's, kept for 10 minutes.
let cfgCache = null
async function zcodeConfig() {
  if (cfgCache && Date.now() - cfgCache.at < 600_000) return cfgCache.cfg
  let cfg = null
  try {
    cfg = await remoteConfig()
  } catch {}
  const local = localConfig()
  if (local && (!cfg || (local.revision ?? 0) > (cfg.revision ?? 0))) cfg = local
  cfgCache = { cfg, at: Date.now() }
  return cfg
}

// variants are a model's reasoning efforts as @ai-sdk/anthropic options.
const BUDGET = { minimal: 1024, low: 4096, medium: 8192, high: 16_000, max: 31_999 }
function variants(es) {
  const out = {}
  for (const e of es) out[e] = e === "none" ? { thinking: { type: "disabled" } } : { thinking: { type: "enabled", budgetTokens: BUDGET[e] ?? 16_000 } }
  return out
}

// entry is a model as a config hook declares it.
const entry = (m) => ({
  id: m.id,
  name: m.id,
  reasoning: m.efforts.some((e) => e !== "none"),
  tool_call: true,
  attachment: !!m.image,
  modalities: { input: m.image ? ["text", "image"] : ["text"], output: ["text"] },
  // what ZCode's config names, else 0 (unknown), as magpie's built-in
  limit: { context: m.context || 0, output: m.output || 0 },
  cost: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
  variants: variants(m.efforts),
})

// model is a model as OpenCode's provider hands it to provider.models.
// modelOf reads the model a Messages body asks for.
function modelOf(text) {
  try {
    const m = JSON.parse(text)?.model
    return typeof m === "string" ? m : ""
  } catch {
    return ""
  }
}

// kept passes a response on as it came, the account kept: the built-in
// never marked a ZCode account lapsed nor cleared one.
function kept(res, text) {
  const h = new Headers(res.headers)
  h.delete("content-length")
  h.delete("content-encoding")
  h.set("X-Magpie-Sign-In", "kept")
  return new Response(text !== undefined ? text : res.body, { status: res.status, statusText: res.statusText, headers: h })
}

function model(m, url) {
  const e = entry(m)
  return {
    id: m.id,
    providerID: PROVIDER,
    name: m.id,
    api: { id: m.id, url, npm: "@ai-sdk/anthropic" },
    status: "active",
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: e.limit,
    options: {},
    headers: {},
    capabilities: {
      temperature: true,
      reasoning: e.reasoning,
      attachment: e.attachment,
      toolcall: true,
      input: { text: true, image: !!m.image, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: e.variants,
  }
}

// ---- the Start Plan's requests ----------------------------------------------------
// zcode.z.ai answers 405 "request has been blocked due to unusual
// activity" (code 3012) to a request that doesn't look like ZCode's own
// (#343: the agent's system prompt as a fourth block), and to every
// request of an account its risk control blocked, ZCode's own app's too
// (plugins#348). A Start Plan request goes as the plugin
// the plan serves (ARNO's "Freeflow", provider zcode-start) sends it: its
// headers (startHeaders) and its body (dress). The texts are prompt.mjs.

// START_RESTRICTED is what ZCode's 3012, "request has been blocked due to
// unusual activity", means: ZCode's risk control blocked the account
// (ZCode's support says so, and ZCode's own app gets the same 405 on such
// an account, plugins#348), not that the request was sent wrong. Word for
// word magpie's provider.ZCodeRestrictedHint: the gateway reads it as the
// account restricted, rests it and asks the group's next member, and the
// Routing page says it in the user's language.
const START_RESTRICTED = "ZCode's risk control blocked this account for unusual activity (code 3012); it rests, and the group's other providers are used. If the ZCode app fails on this account too, only ZCode support can lift the block"
// START_EDGE is what a block page from ZCode's edge firewall (Aliyun's)
// means: this network's address, not the account. Word for word magpie's
// provider.BlockedHint.
const START_EDGE = "the provider's network firewall blocked requests from this IP; wait a while, or switch to another network or proxy"
const EDGE_PAGE = /errors\.aliyun\.com|<html/i
const START_REFUSED = /unusual activity|request has been blocked|"code"\s*:\s*"?3012\b/i

// startRefused says a Start Plan answer is a refusal: a 405, a block page
// or code 3012.
const startRefused = (status, text) => status >= 400 && (status === 405 || START_REFUSED.test(text))

// startBlocked is the refusal's answer: what the vendor said (its status
// for a block page), then what it means. A block page is the network's;
// anything else, ZCode's JSON 3012 or a bare 405, the account's.
function startBlocked(res, text) {
  const page = EDGE_PAGE.test(text)
  let said = ""
  if (!page) {
    try {
      const v = JSON.parse(text)
      said = first(v?.error?.message, typeof v?.error === "string" ? v.error : "", v?.message, v?.msg)
      const code = v?.code ?? v?.error?.code
      if (said && code != null && !String(said).includes(String(code))) said = `${said} (${res.status}, code ${code})`
    } catch {}
  }
  said ||= `${res.status} ${STATUS_TEXT[res.status] ?? res.statusText}`.trim()
  const h = new Headers(res.headers)
  h.set("content-type", "application/json")
  const error = { type: "permission_error", message: `${said} — ${page ? START_EDGE : START_RESTRICTED}` }
  return new Response(JSON.stringify({ type: "error", error }), { status: res.status, statusText: res.statusText, headers: h })
}

const osVersion = () => `${process.platform} ${release()} ${arch()}`
const osCategory = () => (process.platform === "darwin" ? "macos" : process.platform === "win32" ? "windows" : "linux")

function intl() {
  try {
    return Intl.DateTimeFormat().resolvedOptions()
  } catch {
    return {}
  }
}

// startHeaders is a Start Plan request's headers, jwt its key: ZCode's
// own (3.16.3's Frn and Vdt in glm/zcode.cjs) and the body's content-type,
// and nothing else. The agent's headers never go on: Claude Code's
// anthropic-beta, x-app, x-stainless-*, anthropic-dangerous-direct-browser-
// access and x-claude-code-session-id reach the plugin through magpie's
// gateway, and ZCode sends none of them (no anthropic-beta at all on the
// Start Plan: its tool search beta is for coding plans only). Accept and
// accept-encoding are left to the HTTP client, as ZCode leaves them to
// Node's.
function startHeaders(h, jwt) {
  const out = new Headers()
  out.set("content-type", h.get("content-type") || "application/json")
  out.set("Authorization", "Bearer " + jwt)
  out.set("anthropic-version", "2023-06-01")
  out.set("HTTP-Referer", ZCODE)
  out.set("User-Agent", UA + " ai-sdk/anthropic/3.0.81")
  out.set("X-ZCode-App-Version", APP_VERSION)
  out.set("X-Title", "Z Code@cli")
  out.set("X-Release-Channel", "production")
  const o = intl()
  out.set("X-Client-Language", o.locale || "unknown")
  out.set("X-Client-Timezone", o.timeZone || "unknown")
  out.set("X-ZCode-Agent", "glm")
  out.set("X-Platform", platform())
  out.set("X-Os-Category", osCategory())
  // os.release() alone, as ZCode's buildZCodeSourceHeadersFromContext
  // has it; the prompt's OS line is osVersion()
  out.set("X-Os-Version", release())
  out.set("x-request-id", uuid())
  out.set("x-zcode-session-type", "main")
  out.set("x-zcode-trace-id", uuid())
  // 3.16's createModelRequestAttributionHeaders: what asked the model
  out.set("x-zcode-query-source", "main_turn")
  return out
}

// Claude Code's tool search, as ZCode's plans take it. Claude Code
// (2.1.310) offers a tool named ToolSearch, names its deferred tools in a
// system turn, and sends a deferred tool's schema with defer_loading only
// once a search loaded it (a tool_reference in a ToolSearch result), with
// a DeferredToolPlaceholder that keeps the search on. api.z.ai's
// Anthropic endpoint, given a tool named exactly ToolSearch, shows the
// model none of the other tools, Bash, Read and Write among them, but
// those a tool_reference loaded: a subagent, which hasn't searched,
// reports only the deferred tools, and the main turn has to search for
// Bash before it can run it (小八 on Discord, 10-24; the beta header
// changes nothing, the name alone does). ZCode itself sends no ToolSearch
// and no defer_loading. So the search goes as SEARCH_ALIAS, in the tools
// and in the turns that called it, the tools Claude Code sent with their
// schemas go as ZCode's do, without defer_loading, and the placeholder is
// left out; the model's calls to SEARCH_ALIAS come back as ToolSearch
// (searchNamed).
const SEARCH = "ToolSearch"
const SEARCH_ALIAS = "ToolSearchTool"
const PLACEHOLDER = "DeferredToolPlaceholder"

// searchless is a Messages request body without Claude Code's ToolSearch
// name, and whether it had the tool; anything else goes as it is.
function searchless(text) {
  if (typeof text !== "string" || !text.includes(`"${SEARCH}"`)) return { text, renamed: false }
  let b
  try {
    b = JSON.parse(text)
  } catch {
    return { text, renamed: false }
  }
  if (!b || !Array.isArray(b.tools) || !b.tools.some((t) => t?.name === SEARCH)) return { text, renamed: false }
  // a tool of the alias's own name keeps it; the search can't then be renamed
  if (b.tools.some((t) => t?.name === SEARCH_ALIAS)) return { text, renamed: false }
  b.tools = b.tools
    .filter((t) => t?.name !== PLACEHOLDER)
    .map((t) => {
      if (!t || typeof t !== "object") return t
      const { defer_loading, ...rest } = t
      return t.name === SEARCH ? { ...rest, name: SEARCH_ALIAS } : rest
    })
  if (Array.isArray(b.messages))
    for (const m of b.messages)
      if (m?.role === "assistant" && Array.isArray(m.content))
        for (const x of m.content) if (x?.type === "tool_use" && x.name === SEARCH) x.name = SEARCH_ALIAS
  if (b.tool_choice?.type === "tool" && b.tool_choice.name === SEARCH) b.tool_choice.name = SEARCH_ALIAS
  return { text: JSON.stringify(b), renamed: true }
}

// searchNamed is an answer to a request searchless renamed, the model's
// calls to SEARCH_ALIAS named ToolSearch again, as Claude Code runs them:
// a stream's content_block_start events, or a whole message's content.
function searchNamed(res) {
  if (!res.body) return res
  const type = res.headers.get("content-type") ?? ""
  const named = (block) => {
    if (block?.type === "tool_use" && block.name === SEARCH_ALIAS) {
      block.name = SEARCH
      return true
    }
    return false
  }
  const line = (l) => {
    if (!l.startsWith("data:") || !l.includes(`"${SEARCH_ALIAS}"`)) return l
    try {
      const d = JSON.parse(l.slice(5))
      return named(d?.content_block) ? "data: " + JSON.stringify(d) : l
    } catch {
      return l
    }
  }
  if (type.includes("text/event-stream")) {
    const dec = new TextDecoder()
    const enc = new TextEncoder()
    let held = ""
    const body = res.body.pipeThrough(
      new TransformStream({
        transform(chunk, ctl) {
          held += dec.decode(chunk, { stream: true })
          const i = held.lastIndexOf("\n")
          if (i < 0) return
          ctl.enqueue(enc.encode(held.slice(0, i + 1).split("\n").map(line).join("\n")))
          held = held.slice(i + 1)
        },
        flush(ctl) {
          held += dec.decode()
          if (held) ctl.enqueue(enc.encode(line(held)))
        },
      }),
    )
    return new Response(body, { status: res.status, statusText: res.statusText, headers: res.headers })
  }
  if (!type.includes("json")) return res
  return res.text().then((text) => {
    let out = text
    if (text.includes(`"${SEARCH_ALIAS}"`)) {
      try {
        const v = JSON.parse(text)
        if (Array.isArray(v?.content) && v.content.map(named).some(Boolean)) out = JSON.stringify(v)
      } catch {}
    }
    return new Response(out, { status: res.status, statusText: res.statusText, headers: res.headers })
  })
}

// startURL is a Start Plan request's URL without the query the agent put
// on it: Claude Code asks /v1/messages?beta=true, which magpie passes on,
// and ZCode asks /v1/messages alone.
function startURL(url) {
  const i = url.indexOf("?")
  return i < 0 ? url : url.slice(0, i)
}

// WORK_DIR and WORK_GIT are where magpie tells a plugin's fetch the agent
// that asked works: the folder, percent-encoded, and "yes" or "no" for a
// git repository, each left off when magpie doesn't know it (magpie's
// provider.WorkDirHeader and WorkGitHeader). They are for the plugin, not
// the vendor.
const WORK_DIR = "x-magpie-work-dir"
const WORK_GIT = "x-magpie-work-git"

// workPlace takes WORK_DIR and WORK_GIT off h: {dir?, git?}, what magpie
// said of where the agent works.
function workPlace(h) {
  const place = {}
  const dir = h.get(WORK_DIR)
  if (dir) {
    try {
      place.dir = decodeURIComponent(dir)
    } catch {}
  }
  const git = h.get(WORK_GIT)
  if (place.dir && (git === "yes" || git === "no")) place.git = git
  h.delete(WORK_DIR)
  h.delete(WORK_GIT)
  return place
}

// environment is the prompt's environment section; provider and model
// name the powered-by line, left out with no model. The plugin runs in
// magpie, not in the agent's folder: the working directory and whether it
// is a git repository are the agent's, as magpie tells them (place), and
// each is left out when it isn't told rather than said as magpie's own
// (#86). The agent's shell isn't known here either, so it isn't said; the
// platform and OS version are this computer's, as ZCode's X-Os-* headers
// say them.
function environment(provider, model, place = {}) {
  const e = PROMPT.environment
  const lines = [e.heading, e.invokedLine]
  if (place.dir) {
    lines.push(`- ${e.cwdLabel}: ${place.dir}`)
    if (place.git) lines.push(`- ${e.gitLabel}: ${place.git === "yes" ? "yes" : e.gitNo}`)
  }
  lines.push(`- ${e.platformLabel}: ${process.platform}`, `- ${e.osVersionLabel}: ${osVersion()}`)
  if (model) lines.push(e.poweredByLine.replace("{provider}", provider).replace("{model}", model))
  return lines.join("\n")
}

// today is the local date, YYYY-MM-DD.
function today(now = new Date()) {
  const p = (n) => String(n).padStart(2, "0")
  return `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`
}

// contextPrefix is the user turn ZCode puts before the agent's: the day in
// a <system-reminder>, the tags hugging the text.
function contextPrefix() {
  const c = PROMPT.context
  const body = [c.intro, c.currentDateHeading + "\n" + c.currentDateLine.replace("{date}", today()), "", c.outro].join("\n")
  return { role: "user", content: [{ type: "text", text: "<system-reminder>" + body + "</system-reminder>" }] }
}

// dress is a Messages request body as ZCode sends it: ZCode's three cached
// system blocks and no more, the agent's own system text at the end of the
// third (#343); the context
// prefix as the first turn; no cache mark in the turns but on the last
// block of the last; none on the tools; metadata.user_id naming the
// sign-in's device. site names the powered-by line's provider, place where
// the agent works (workPlace). A body that isn't a Messages request goes as
// it is.
function dress(text, site, device, place) {
  if (typeof text !== "string" || !text.includes("messages")) return text
  let b
  try {
    b = JSON.parse(text)
  } catch {
    return text
  }
  if (!b || typeof b !== "object" || !Array.isArray(b.messages)) return text
  const cached = (t) => ({ type: "text", text: t, cache_control: { type: "ephemeral" } })
  const provider = site === "bigmodel" ? "bigmodel-api" : "zai-api"
  const model = typeof b.model === "string" ? b.model : ""
  // The agent's own system text joins the third block, as ZCode's
  // ContextBuilder (3.14.3, glm/zcode.cjs assembleSystemMessages) joins
  // every dynamic system section, an added one too, with "\n\n" into
  // that one block: ZCode never sends more than three. zcode.z.ai turns a
  // fourth block away with 405/3012 (#343), and the first two must stay
  // byte for byte ZCode's.
  const own = []
  if (typeof b.system === "string") {
    if (b.system.trim()) own.push(b.system)
  } else if (Array.isArray(b.system)) {
    for (const x of b.system) if (x?.type === "text" && typeof x.text === "string" && x.text.trim()) own.push(x.text)
  }
  const dynamic = [PROMPT.beforeEnvironment, environment(provider, model, place), PROMPT.afterEnvironment, ...own]
  b.system = [cached(PROMPT.cliPrefix), cached(PROMPT.stableSections.join("\n\n")), cached("\n\n" + dynamic.join("\n\n"))]
  b.messages = [contextPrefix(), ...b.messages]
  let last = null
  for (const m of b.messages) {
    if (!m || m.role === "system") continue
    last = m
    if (Array.isArray(m.content)) for (const x of m.content) if (x && typeof x === "object") delete x.cache_control
  }
  if (last && typeof last.content === "string") last.content = [cached(last.content)]
  else if (last && Array.isArray(last.content) && last.content.length) {
    const x = last.content[last.content.length - 1]
    if (x && typeof x === "object" && !x.cache_control) x.cache_control = { type: "ephemeral" }
  }
  if (Array.isArray(b.tools)) for (const t of b.tools) if (t && typeof t === "object") delete t.cache_control
  const meta = b.metadata && typeof b.metadata === "object" ? b.metadata : {}
  b.metadata = { ...meta, user_id: JSON.stringify({ device_id: device, account_uuid: "", session_id: "" }) }
  return JSON.stringify(b)
}

// ---- the plugin ------------------------------------------------------------------

export async function ZCodeAuthPlugin({ client }) {
  const log = (message) => {
    try {
      client?.app?.log?.({ body: { service: "zcode-auth", level: "error", message } })
    } catch {}
  }
  const oauth = (site) => ({
    type: "oauth",
    label: `ZCode: ${SITES[site].name}${site === "bigmodel" ? " (智谱)" : ""} GLM Coding Plan`,
    authorize: () => authorize(site, log),
  })
  return {
    config: async (cfg) => {
      cfg.provider ??= {}
      const was = cfg.provider[PROVIDER] ?? {}
      cfg.provider[PROVIDER] = {
        name: "ZCode",
        npm: "@ai-sdk/anthropic",
        api: ZAI_BASE + "/v1",
        ...was,
        models: { ...Object.fromEntries(MODELS.map((m) => [m.id, entry(m)])), ...(was.models ?? {}) },
      }
    },
    provider: {
      id: PROVIDER,
      // the account's plan's models, as ZCode's config lists them now
      async models(provider, { auth } = {}) {
        const s = stateOf(auth)
        if (!s) return provider.models
        const { start, gift: known } = await plansOf(s).catch(() => ({ start: false, gift: null }))
        const gift = start ? await startPlan(s.jwt, s.device).catch(() => null) : known
        const base = start ? START_BASE : s.base
        const cfg = await zcodeConfig().catch(() => null)
        let ms = modelsOf(cfg, planID(base))
        // ZCode's config can't be had: its table stands in, and magpie keeps
        // the list it was told last, as the built-in keeps the one it fetched last
        const fell = !ms.length
        if (fell) ms = start ? START_MODELS : MODELS
        const url = s.base + "/v1"
        const out = Object.fromEntries(ms.map((m) => [m.id, model(m, url)]))
        const details = [...ms, ...modelsOf(cfg, planID(START_BASE)), ...modelsOf(cfg, planID(ZAI_BASE)), ...MODELS]
        const about = (raw) => details.find((m) => m.id.toLowerCase() === raw.toLowerCase()) ?? { efforts: [] }
        const has = new Set(Object.keys(out).map((id) => id.toLowerCase()))
        for (const raw of giftNames(gift)) {
          // a gift-only account's models are its gift's, by their own
          // names, as ZCode lists them (its Start Plan's GLM-5.3, which the
          // plan's config leaves out): every request of it goes to the gift
          // anyway, and the card counts them by that name too (#1261)
          if (start && !has.has(raw.toLowerCase())) {
            const plain = about(raw).id ?? raw
            has.add(raw.toLowerCase())
            out[plain] = model({ ...about(raw), id: plain }, url)
          }
          const id = giftID(raw)
          if (out[id]) continue
          out[id] = model({ ...about(raw), id }, url)
        }
        // a fallback tells magpie to keep the list it was told last, which
        // predates the gift read just made: an account whose gift lists
        // trial entries is told this list, so ZCode's config out of reach
        // (a remote box that can't get its CDN) never hides them
        if (fell && !giftNames(gift).length) out[Symbol.for("magpie.fellBack")] = true
        return out
      },
    },
    auth: {
      provider: PROVIDER,
      async loader(getAuth, provider) {
        const s0 = stateOf(await getAuth())
        if (!s0) return {}
        const id = provider?.id ?? PROVIDER
        return {
          baseURL: s0.base + "/v1",
          apiKey: s0.key || "zcode", // the fetch below puts the real one on
          async fetch(input, init) {
            const auth = await getAuth()
            const s = stateOf(auth)
            if (!s) throw new Error("not signed in to ZCode")
            let url = input instanceof Request ? input.url : String(input)
            const opts = input instanceof Request ? { method: input.method, headers: input.headers, body: input.body, signal: input.signal, duplex: "half", ...init } : { ...init }
            const { start: alone, gift: known } = await plansOf(s)
            let gift = known
            let key = s.key
            if (isTeam(s) && !key) {
              key = await teamKey(s)
              if (auth?.type === "oauth") {
                const next = { ...auth, refresh: JSON.stringify({ ...s, key }), access: key }
                await client?.auth?.set?.({ path: { id }, body: next }).catch?.(() => {})
              }
            }
            // a dual account's body is read ahead of sending, so a gift that
            // says it is spent can spend nothing and replay what was asked for
            let start = alone
            let orig, forcedGift = false
            let renamed = false
            const done = (res) => (renamed ? searchNamed(res) : res)
            if (opts.body != null) {
              orig = opts.body = typeof opts.body === "string" ? opts.body : await new Response(opts.body).text()
              delete opts.duplex
              // Claude Code's tool search goes under another name, or
              // ZCode's plans show the model no other tool (searchless)
              const plain = searchless(orig)
              if (plain.renamed) {
                orig = opts.body = plain.text
                renamed = true
              }
              let m = modelOf(orig)
              if (m.toLowerCase().endsWith("-trial")) {
                gift ??= await startPlan(s.jwt, s.device).catch(() => null)
                const raw = giftModel(gift, m)
                const available = raw && giftUsable(s, gift, raw)
                if (!available) {
                  const error = { type: raw ? "rate_limit_error" : "invalid_request_error", message: raw ? `ZCode's limited quota for ${raw} is exhausted or temporarily unavailable` : `ZCode has no active limited quota for ${m}` }
                  return kept(new Response(JSON.stringify({ type: "error", error }), { status: raw ? 429 : 400, headers: { "content-type": "application/json" } }))
                }
                orig = opts.body = JSON.stringify({ ...JSON.parse(orig), model: raw })
                m = raw
                forcedGift = true
              }
              start = alone || forcedGift
            }
            const swap = (base) => {
              for (const b of [START_BASE, ZAI_BASE, BIGMODEL_BASE]) {
                if (url.startsWith(b)) {
                  url = base + url.slice(b.length)
                  break
                }
              }
            }
            let h = new Headers(opts.headers)
            const place = workPlace(h)
            if (start) {
              if (jwtExpired(s.jwt)) throw new Error(EXPIRED)
              if (!s.jwt) throw new Error("ZCode's sign-in has no key; sign in again")
              swap(START_BASE)
              // the Start Plan is served only to what looks like ZCode's own request
              url = startURL(url)
              h = startHeaders(h, s.jwt)
              if (opts.body != null) {
                const text = orig ?? (typeof opts.body === "string" ? opts.body : await new Response(opts.body).text())
                opts.body = dress(text, s.site, s.device, place)
                delete opts.duplex
                h.delete("content-length")
              }
            } else {
              if (!key) throw new Error("ZCode's sign-in has no key; sign in again")
              swap(s.base)
              if (renamed) h.delete("content-length")
              h.delete("authorization")
              h.set("x-api-key", key)
              h.set("Authorization", "Bearer " + key)
            }
            let res = await fetch(url, { ...opts, headers: h })
            // the Start Plan turning the request away says what to do about
            // it: the account blocked, or the network
            if (start && res.status >= 400) {
              const text = await res.text()
              if (startRefused(res.status, text)) {
                // a key account sent to the Start Plan (its plan list read
                // none) reads its plans again on the next request, so a
                // coding plan that is there after all is used (#369)
                if (s.key && !forcedGift) routes.delete(s.key + "\0" + s.jwt)
                return kept(startBlocked(res, text))
              }
              res = new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers })
            }
            // a key with no plan behind it: what Z.ai said, and why a
            // ZCode gift plan can't be had with a key (NO_PLAN_KEY)
            if (!start && !s.jwt && res.status >= 400) {
              const text = await res.text()
              const why = noPlanKey(res, text)
              if (why) return done(kept(why))
              res = new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers })
            }
            // a trial entry's gift answer that can't serve the request is
            // the agent's answer, not the coding plan's: the user picked the
            // gift, so a spent bucket says 429 (its quota error inside a 200
            // among them) and anything else goes on as it came. A quota
            // answer keeps that model off the gift for a minute, so a rate
            // limit is not knocked twice. A stream's body is never read here.
            if (start && forcedGift) {
              const json = (res.headers.get("content-type") ?? "").includes("application/json")
              if (res.status >= 400 || json) {
                const text = await res.text()
                const spent = spentUp(text, res.status)
                if (!spent && res.status < 400) return done(kept(res, text))
                if (spent) {
                  for (const [k, until] of blocked) if (Date.now() >= until) blocked.delete(k)
                  blocked.set(s.key + "\0" + s.jwt + "\0" + modelOf(orig).toLowerCase(), Date.now() + 60_000)
                }
                return spent && res.status < 400 ? kept(new Response(text, { status: 429, headers: res.headers })) : done(kept(res, text))
              }
            }
            // the plan's answer goes on as it came, the account kept: the
            // built-in never marked a ZCode account lapsed nor cleared one
            return done(kept(res))
          },
        }
      },
      // the plan's allowance, as magpie's built-in showed it. Each read
      // keeps the sign-in (signIn): the built-in's marked no account lapsed,
      // its expired Start Plan sign-in among them, and cleared none
      async usage(getAuth, provider) {
        const auth = await getAuth()
        const s = stateOf(auth)
        if (!s) return { error: "not signed in", signIn: "kept" }
        // every card starts from the plan the sign-in saved, as magpie's
        // built-in did: a reply without a level, a team detail that
        // failed or an error keeps it
        const saved = (out) => {
          const plan = s.plan || auth?.plan
          if (plan && !out.plan) out.plan = plan
          // MCP reads as a footnote: its window goes last on the card
          if (Array.isArray(out.windows) && out.windows.some((w) => w.name === "MCP · Month"))
            out.windows = [...out.windows.filter((w) => w.name !== "MCP · Month"), ...out.windows.filter((w) => w.name === "MCP · Month")]
          out.signIn = "kept"
          return out
        }
        try {
          // a card read is a fresh read: the route is read again with it,
          // so a plan bought, a gift claimed or a plan lapsed reaches the
          // requests when the card shows it, not ten minutes later (#379)
          routes.delete(s.key + "\0" + s.jwt)
          const { start, gift } = await plansOf(s)
          let card
          if (isTeam(s)) {
            let key = s.key
            if (!key) {
              key = await teamKey(s)
              if (auth?.type === "oauth") {
                const next = { ...auth, refresh: JSON.stringify({ ...s, key }), access: key }
                await client?.auth?.set?.({ path: { id: provider?.id ?? PROVIDER }, body: next }).catch?.(() => {})
              }
            }
            const team = await teamUsage(s, key)
            card = saved(gift ? await dualUsage(s, team) : team)
          } else card = saved(start ? await startUsage(s) : gift ? await dualUsage(s) : await codingUsage(s))
          // a gift plan ZCode holds for the account but has not put on it:
          // named on the card, claimed in the ZCode app (its claim needs
          // the app's captcha attestation, see claimHint). A card that is
          // an error keeps its error and asks nothing
          if (!card.error) {
            const hint = await claimHint(s)
            if (hint) card.windows = [...(card.windows ?? []), hint]
            // a key, or a ZCode session that ran out: no gift read, so no
            // -Trial models, said on the card rather than left out unsaid
            const need = start ? null : giftSignIn(s, auth?.type === "api")
            if (need) card.windows = [...(card.windows ?? []), need]
          }
          return card
        } catch (e) {
          return saved({ error: e?.message ?? String(e) })
        }
      },
      methods: [
        oauth("zai"),
        oauth("bigmodel"),
        // a team's plan beside the account's own (#285): the sign-ins above
        // take the account's own plan when it has one
        {
          type: "oauth",
          label: "ZCode: a team's GLM Coding Plan",
          prompts: [
            {
              type: "select",
              key: "site",
              message: "Where is the team's plan?",
              options: [
                { label: "Z.ai", value: "zai", hint: "z.ai" },
                { label: "BigModel (智谱)", value: "bigmodel", hint: "bigmodel.cn" },
              ],
            },
          ],
          authorize: (inputs) => authorize(inputs?.site === "bigmodel" ? "bigmodel" : "zai", log, true),
        },
        { type: "oauth", label: "ZCode app's sign-in", authorize: async () => asZCode() },
        {
          type: "api",
          label: "GLM Coding Plan API key",
          prompts: [
            {
              type: "select",
              key: "site",
              message: "Where is the key from?",
              options: [
                { label: "Z.ai", value: "zai", hint: "api.z.ai" },
                { label: "BigModel (智谱)", value: "bigmodel", hint: "open.bigmodel.cn" },
              ],
            },
          ],
        },
      ],
    },
  }
}

// for tests
// config sets what zcodeConfig has read, for tests: null is ZCode's config
// out of reach, undefined forgets it
const config = (cfg) => (cfgCache = cfg === undefined ? null : { cfg, at: Date.now() })
export const _internal = { config, entry, limitWindows, termOf, startUsage, routes, blocked, plansOf, giftOf, dualUsage, claimHint, spentUp, giftServes, modelOf, teamKeys, ownSignIn, stateOf, dress, PROMPT }
