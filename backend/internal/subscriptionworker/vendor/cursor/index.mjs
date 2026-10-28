// Cursor subscriptions as an OpenCode provider plugin, served through the
// API cursor-agent talks to. Ported from magpie's built-in Cursor account
// (internal/provider/cursor*.go, internal/gateway/cursor.go).
//
// An account is a Cursor access token, got one of three ways:
// - the sign-in cursor-agent does (cursor.com/loginDeepControl with a PKCE
//   challenge, then /auth/poll until the browser has signed in);
// - the token cursor-agent itself keeps (the Mac keychain, else its
//   auth.json), read each time and never written;
// - a Cursor API key, exchanged for a token as the CLI does with
//   CURSOR_API_KEY.
//
// Requests go to AgentService/Run, a Connect stream both ways over HTTP/2.
// Where HTTP/2 can't open one (a proxy or network that blocks it), the Run
// goes as Cursor's clients run it without HTTP/2: its replies down one
// server stream, AgentService/RunSSE, and each client message up as a call
// of its own, BidiService/BidiAppend, both over HTTP/1.1 on api2.
// Cursor keeps a conversation on its client: each message of the prompt is
// an AI SDK message in JSON, a blob named by its sha256, which the server
// asks the client for as it reads them. So each request goes whole, as such
// a conversation, the caller's system prompt at its head; the Run only
// resumes it. The caller's tools are MCP tools, which the model calls
// through Cursor's CallDynamicTool: the server hands each call to the
// client to run, and once it has said how many a step made, the calls
// go back to OpenCode. When the owner is available their results continue
// that Run; otherwise the caller's history starts a stateless Run. Nothing
// else the server asks the client to run (a shell, a file read) is run.
//
// OpenCode speaks chat completions to it (@ai-sdk/openai-compatible); the
// fetch here answers them.
import { NATIVE_LIMITS, nativeSessions, NativeSessionError, nativeFields, nativeMcpArgs, Quiet } from "./native-session.mjs"
import { isDeepStrictEqual } from "node:util"
import http2 from "node:http2"
import { STATUS_CODES } from "node:http"
import { gunzipSync } from "node:zlib"
import { Duplex } from "node:stream"
import { createHash, randomBytes, randomUUID } from "node:crypto"
import { execFile } from "node:child_process"
import { readFile, stat, readdir, realpath } from "node:fs/promises"
import { homedir, platform, tmpdir } from "node:os"
import { basename, dirname, join } from "node:path"

const ID = "cursor"
const API = "https://api2.cursor.sh"
const WEBSITE = "https://cursor.com"
const AGENT = "https://agentn.global.api5.cursor.sh" // when the server config can't say
const CHAT = "@ai-sdk/openai-compatible"
const VERSION_FALLBACK = "2026.09.23-86fc751" // the CLI version said when no install names one
const DEFAULT_CONTEXT = 200_000 // a model Cursor gives no limit for
const MODELS_KEEP = 10 * 60 * 1000
const CALL = "CallDynamicTool" // how the model calls an MCP tool
const NO_RESULT = "Tool use was interrupted and did not produce a result."
const CLI_MARK = "cursor-agent" // an oauth sign-in's refresh when the token is cursor-agent's own

const GOOS = { win32: "windows" }[platform()] ?? platform()
const EMPTY = Buffer.alloc(0)
const enc = new TextEncoder()

// ---- cursor-agent ---------------------------------------------------------------

const exists = (p) => stat(p).then((s) => !s.isDirectory(), () => false)

function run(file, args, { timeout = 10_000, env } = {}) {
  return new Promise((resolve) => {
    execFile(file, args, { timeout, env: env ?? process.env, maxBuffer: 8 << 20 }, (err, stdout) =>
      resolve({ ok: !err, out: String(stdout ?? ""), code: typeof err?.code === "number" ? err.code : err ? -1 : 0 }))
  })
}

// cursorExecutable finds the cursor-agent CLI, or Cursor's `agent` by that
// name.
async function cursorExecutable() {
  const dirs = (process.env.PATH ?? "").split(GOOS === "windows" ? ";" : ":").filter(Boolean)
  for (const name of ["cursor-agent", "agent"]) {
    for (const d of dirs) {
      const p = join(d, GOOS === "windows" ? name + ".exe" : name)
      if (!(await exists(p))) continue
      if (name === "cursor-agent") return p
      if ((await realpath(p).catch(() => "")).includes("cursor-agent")) return p
    }
  }
  const home = homedir()
  for (const p of [join(home, ".local", "bin", "cursor-agent"), "/usr/local/bin/cursor-agent", "/opt/homebrew/bin/cursor-agent"])
    if (await exists(p)) return p
  return ""
}

const VERSION_RE = /^\d{4}\.\d{2}\.\d{2}-[0-9a-f]+$/

// clientVersion is the cursor-agent the API is told it is talking to,
// "cli-<version>": the installed one, as the API turns away a version it no
// longer supports.
let versionSaid = ""
async function clientVersion() {
  if (versionSaid) return versionSaid
  let v = ""
  const exe = await cursorExecutable()
  if (exe) {
    const d = basename(dirname(await realpath(exe).catch(() => exe)))
    if (VERSION_RE.test(d)) v = d
  }
  if (!v) {
    const dirs = [join(homedir(), ".local", "share", "cursor-agent", "versions")]
    if (GOOS === "windows") dirs.push(join(process.env.LOCALAPPDATA ?? "", "cursor-agent", "versions"))
    for (const dir of dirs)
      for (const n of await readdir(dir).catch(() => [])) if (VERSION_RE.test(n) && n > v) v = n
  }
  versionSaid = "cli-" + (v || VERSION_FALLBACK)
  return versionSaid
}

// authFile is where cursor-agent keeps its sign-in off a Mac's keychain.
function authFile() {
  const home = homedir()
  if (GOOS === "windows") return join(process.env.APPDATA || join(home, "AppData", "Roaming"), "Cursor", "auth.json")
  if (GOOS === "darwin") return join(home, ".cursor", "auth.json")
  return join(process.env.XDG_CONFIG_HOME || join(home, ".config"), "cursor", "auth.json")
}

// cliConfigFile is cursor-agent's cli-config.json, where it keeps the team
// it signed in to (authInfo.activeTeamId); its directory found as the CLI
// finds it, on every OS.
function cliConfigFile() {
  const dir = process.env.CURSOR_CONFIG_DIR?.trim()
    || (process.env.XDG_CONFIG_HOME?.trim() ? join(process.env.XDG_CONFIG_HOME, "cursor") : join(homedir(), ".cursor"))
  return join(dir, "cli-config.json")
}

// tokenSub is a JWT's subject (the auth id, "auth0|user_…"); "" when it
// doesn't say.
function tokenSub(tok) {
  const parts = String(tok ?? "").split(".")
  if (parts.length !== 3) return ""
  try {
    const sub = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"))?.sub
    return typeof sub === "string" ? sub : ""
  } catch {
    return ""
  }
}

const teamOf = (v) => (Number.isSafeInteger(v) && v > 0 ? v : undefined)

// activeTeamId is the team a request is billed to, as cursor-agent sends it
// (x-cursor-team-id): cli-config.json's authInfo.activeTeamId. Without it
// Cursor takes the token's default team, which in an organization may be one
// with on-demand spend turned off ("Team budget limit reached"). The config's
// team is only taken for the account cursor-agent signed in to: a token
// whose subject isn't its authInfo.authId, or a config with none, gets none.
// Read again after 10 s; cursor-agent rewrites the file in place, so a read
// that fails to parse keeps the last one, and only a missing file clears it.
const TEAM_TTL = 10_000
const teamRead = { at: 0, info: null }
async function activeTeamId(tok) {
  if (Date.now() - teamRead.at >= TEAM_TTL) {
    try {
      teamRead.info = JSON.parse(await readFile(cliConfigFile(), "utf8"))?.authInfo ?? null
    } catch (e) {
      if (e?.code === "ENOENT") teamRead.info = null
    }
    teamRead.at = Date.now()
  }
  const info = teamRead.info
  if (!info) return undefined
  if (!info.authId || info.authId !== tokenSub(tok)) return undefined
  return teamOf(info.activeTeamId)
}

// teamHeader names that team in a request's headers, and teamBody in
// GetFilteredUsageEvents' body; neither says anything when there is none.
// GetHardLimit and GetCurrentPeriodUsage go out with "{}", as cursor-agent
// sends them: the team rides in the header only.
async function teamHeader(tok) {
  const id = await activeTeamId(tok)
  return id ? { "x-cursor-team-id": String(id) } : {}
}
async function teamBody(tok) {
  const id = await activeTeamId(tok)
  return id ? { teamId: id } : {}
}

// TOKEN_TTL is how long a token read from the Keychain is taken as it: a
// sign-in reads it afresh, one made in cursor-agent is seen within it.
// KEYCHAIN_DENIED is how long a read the user may have been asked about,
// and answered no to, is not asked again: with the item's trust list
// holding no `security`, every ask can show the dialog again. Both as the
// built-in's cursorKeychainToken (magpie #746, #2408).
const TOKEN_TTL = 10_000
const KEYCHAIN_DENIED = 10 * 60_000
const keychain = { tok: "", at: 0, ask: 0, busy: null, now: () => Date.now(), read: (args) => run("security", args) }

// keychainToken is cursor-agent's token in the Keychain, "" for none; fresh
// reads it whatever was read last. Reads go one at a time, so callers at
// once share one `security` run (and one dialog).
function keychainToken(fresh = false) {
  const go = async () => {
    const now = keychain.now()
    const ttl = !keychain.tok && now - keychain.ask < KEYCHAIN_DENIED ? KEYCHAIN_DENIED : TOKEN_TTL
    if (!fresh && now - keychain.at < ttl) return keychain.tok
    const r = await keychain.read(["find-generic-password", "-s", "cursor-access-token", "-a", "cursor-user", "-w"])
    keychain.tok = ""
    keychain.at = keychain.now()
    if (r.ok) {
      keychain.tok = r.out.trim()
      keychain.ask = 0
    } else if (r.code !== 44) {
      // 44 is "no such item"; anything else may have been a dialog the
      // user said no to, or a keychain that refused
      keychain.ask = keychain.at
    } else keychain.ask = 0
    return keychain.tok
  }
  const p = (keychain.busy ?? Promise.resolve()).then(go, go)
  keychain.busy = p.catch(() => {})
  return p
}

// cliToken is the access token cursor-agent signed in with; read, never
// written. fresh reads the Keychain whatever was read last.
async function cliToken(fresh = false) {
  if (GOOS === "darwin") {
    const t = await keychainToken(fresh)
    if (t) return t
  }
  try {
    return JSON.parse(await readFile(authFile(), "utf8"))?.accessToken ?? ""
  } catch {
    return ""
  }
}

// expiry is when a JWT runs out, in ms; 0 when it doesn't say.
function expiry(tok) {
  const parts = String(tok ?? "").split(".")
  if (parts.length !== 3) return 0
  try {
    const exp = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"))?.exp
    return typeof exp === "number" && exp > 0 ? exp * 1000 : 0
  } catch {
    return 0
  }
}

const SOON = 5 * 60 * 1000

class AuthError extends Error {
  constructor(msg) {
    super(msg)
    this.status = 401
  }
}

// cliTokenNow is cursor-agent's token; one about to run out is renewed by
// cursor-agent, which does that whenever it runs.
let cliRefresh = null
async function cliTokenNow() {
  let tok = await cliToken()
  const exp = expiry(tok)
  if (tok && (!exp || exp - Date.now() > SOON)) return tok
  const exe = await cursorExecutable()
  if (exe) {
    cliRefresh ??= run(exe, ["status"], { timeout: 30_000 }).finally(() => (cliRefresh = null))
    await cliRefresh
    tok = await cliToken(true)
  }
  // the built-in's words (provider.CursorToken)
  if (!tok) throw new AuthError("Cursor isn't signed in; sign in from magpie's Providers page or run `cursor-agent login`")
  const e = expiry(tok)
  if (e && e <= Date.now()) throw new AuthError("Cursor's sign-in has run out; sign in again from magpie's Providers page or run `cursor-agent login`")
  return tok
}

// ---- the account -----------------------------------------------------------------

async function headersFor(tok) {
  return {
    "Content-Type": "application/json",
    "Connect-Protocol-Version": "1",
    ...(tok ? { Authorization: `Bearer ${tok}` } : {}),
    "x-cursor-client-version": await clientVersion(),
    "x-cursor-client-type": "cli",
    "x-ghost-mode": "true",
    ...await teamHeader(tok),
  }
}

// unary is a Connect call in JSON.
async function unary(base, path, tok, body = {}, timeout = 15_000) {
  const res = await fetch(base + "/" + path, {
    method: "POST",
    headers: await headersFor(tok),
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(timeout),
  })
  const text = await res.text()
  if (!res.ok) {
    const f = failure(res.status, text)
    const e = new Error(f.message)
    e.status = f.status
    throw e
  }
  return JSON.parse(text || "{}")
}

// whoIs is the account's email and plan, as `cursor-agent about` says them.
async function whoIs(tok) {
  const [me, plan] = await Promise.allSettled([
    unary(API, "aiserver.v1.DashboardService/GetMe", tok),
    unary(API, "aiserver.v1.DashboardService/GetPlanInfo", tok),
  ])
  return {
    email: me.status === "fulfilled" ? String(me.value?.email ?? "").trim() : "",
    plan: plan.status === "fulfilled" ? String(plan.value?.planInfo?.planName ?? "").trim() : "",
  }
}

// plans are the plan each token's account is on, as `cursor-agent about`
// names it (GetPlanInfo's planName, magpie's card title), asked again after
// an hour.
const plans = new Map()
const PLAN_TTL = 3600_000
async function planOf(tok) {
  const had = plans.get(tok)
  if (had && Date.now() - had.at < PLAN_TTL) return had.plan
  try {
    const plan = String((await unary(API, "aiserver.v1.DashboardService/GetPlanInfo", tok))?.planInfo?.planName ?? "").trim()
    if (plan) plans.set(tok, { plan, at: Date.now() })
    return plan || had?.plan || ""
  } catch {
    return had?.plan ?? ""
  }
}

// exchanged are API keys' tokens, by the key's hash.
const exchanged = new Map()

// keyToken is the access token for a Cursor API key, as the CLI gets it
// for CURSOR_API_KEY: /auth/exchange_user_api_key, again when it runs out.
async function keyToken(key) {
  const h = createHash("sha256").update(key).digest("hex")
  const had = exchanged.get(h)
  if (had && (!had.exp || had.exp - Date.now() > SOON)) return had.tok
  const res = await fetch(API + "/auth/exchange_user_api_key", {
    method: "POST",
    headers: { ...(await headersFor(key)), "Connect-Protocol-Version": undefined },
    body: "{}",
    signal: AbortSignal.timeout(15_000),
  }).catch((e) => {
    throw Object.assign(new Error("couldn't reach Cursor's API: " + e.message), { status: 502 })
  })
  if (res.status >= 500) throw Object.assign(new Error(`exchange_user_api_key: HTTP ${res.status}`), { status: 502 })
  const j = res.ok ? await res.json().catch(() => null) : null
  if (!j?.accessToken) throw new AuthError("Cursor didn't take this API key")
  exchanged.set(h, { tok: j.accessToken, exp: expiry(j.accessToken) })
  return j.accessToken
}

// tokenOf is the token to call Cursor's API with, for a sign-in.
async function tokenOf(auth) {
  if (auth?.type === "api" && auth.key) return keyToken(auth.key)
  if (auth?.type !== "oauth") throw new AuthError("Cursor isn't signed in")
  if (auth.refresh === CLI_MARK) return cliTokenNow()
  const exp = auth.expires || expiry(auth.access)
  if (!auth.access) throw new AuthError("Cursor isn't signed in")
  // Cursor's sign-in has nothing to renew it with: cursor-agent itself
  // signs in again when it runs out (about two months)
  if (exp && exp <= Date.now()) throw new AuthError("Cursor's sign-in has run out; sign in to Cursor again")
  return auth.access
}

// browserSignIn is cursor-agent's `login`: the page signs in, and
// /auth/poll hands the tokens over once it has.
async function browserSignIn() {
  const verifier = randomBytes(32).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  const uuid = randomUUID()
  const url = new URL("/loginDeepControl", WEBSITE)
  url.searchParams.set("challenge", challenge)
  url.searchParams.set("uuid", uuid)
  url.searchParams.set("mode", "login")
  url.searchParams.set("redirectTarget", "cli")
  return {
    url: url.toString(),
    instructions: "Sign in to Cursor in the browser; this finishes by itself.",
    method: "auto",
    async callback() {
      const headers = { "Content-Type": "application/json", "x-cursor-client-version": await clientVersion(), "x-cursor-client-type": "cli" }
      let errors = 0
      for (let r = 0; r < 150; r++) {
        const wait = Math.min(1000 * 1.2 ** r, 10_000)
        let res
        try {
          res = await fetch(`${API}/auth/poll?uuid=${uuid}&verifier=${verifier}`, { headers, signal: AbortSignal.timeout(15_000) })
        } catch {
          if (++errors >= 3) return { type: "failed", error: "couldn't reach Cursor" }
          await sleep(wait)
          continue
        }
        if (res.status === 404) {
          errors = 0
          await sleep(wait)
          continue
        }
        if (!res.ok) {
          if (res.status === 403 || ++errors >= 3) return { type: "failed", error: res.status === 403 ? "Cursor refused the sign-in (403)" : `Cursor's sign-in answered HTTP ${res.status}` }
          await sleep(wait)
          continue
        }
        const j = await res.json().catch(() => null)
        if (!j?.accessToken || !("refreshToken" in j)) return { type: "failed", error: "Cursor sent back no token" }
        const who = await whoIs(j.accessToken)
        return {
          type: "success",
          access: j.accessToken,
          refresh: j.refreshToken || "none",
          expires: expiry(j.accessToken) || Date.now() + 30 * 24 * 3600 * 1000,
          ...(who.email ? { accountId: who.email } : {}),
          ...(who.plan ? { plan: who.plan } : {}),
        }
      }
      return { type: "failed", error: "the sign-in timed out" }
    },
  }
}

// cliSignIn takes the account cursor-agent is signed in to; its token is
// read each time from where cursor-agent keeps it.
async function cliSignIn() {
  return {
    url: "",
    instructions: "Uses the account `cursor-agent login` signed in to.",
    method: "auto",
    async callback() {
      const tok = await cliToken(true)
      const exp = expiry(tok)
      if (!tok) return { type: "failed", error: "cursor-agent isn't signed in: run `cursor-agent login`" }
      if (exp && exp <= Date.now()) return { type: "failed", error: "cursor-agent's sign-in has run out: run `cursor-agent login` again" }
      const who = await whoIs(tok)
      if (!who.email) return { type: "failed", error: "Cursor couldn't say which account is signed in" }
      return { type: "success", access: "", refresh: CLI_MARK, expires: 0, accountId: who.email, ...(who.plan ? { plan: who.plan } : {}) }
    },
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// ---- usage ------------------------------------------------------------------------
//
// How much of the plan's included usage is gone, as magpie's built-in Cursor
// account shows it (internal/provider/cursor_usage.go): the dashboard's
// current period, split into the Cursor Models and Other Models pools.

// statusText is a refused request's status as magpie says it (Go's
// http.StatusText).
const statusText = (s) =>
  ({ 413: "Request Entity Too Large", 414: "Request URI Too Long", 416: "Requested Range Not Satisfiable", 418: "I'm a teapot", 509: "" })[s] ?? STATUS_CODES[s] ?? ""

const POOL_VARIANT = /-(fast|none|low|medium|high|xhigh|extra-high|max|thinking)$/

// poolBase names a model's family: lower case, without cursor-, the
// context size the list adds to it (@300k) and the effort and speed the
// CLI adds to it.
function poolBase(model) {
  model = String(model).trim().toLowerCase().replace(/@[^@]*$/, "")
  if (model.startsWith("cursor-")) model = model.slice("cursor-".length)
  for (;;) {
    const b = model.replace(POOL_VARIANT, "")
    if (b === model) return model
    model = b
  }
}

// Cursor's autoBucketModels can lag model releases: it still omitted Grok
// 4.6/4.7 when the published Cursor Models pool already included them.
// Keep those documented families alongside the server's exact model list.
// See https://cursor.com/docs/models-and-pricing.
function firstParty(model) {
  model = model.replace(/@[^@]*$/, "")
  if (model.startsWith("cursor-")) model = model.slice("cursor-".length)
  if (model === "default" || model === "composer" || model.startsWith("composer-")) return true
  return ["grok-4.5", "grok-4.6", "grok-4.7"].some((b) => model === b || model.startsWith(b + "-"))
}

// onDemandOf is what Cursor still serves once the included usage is spent,
// as cursor-agent's own usage view tells it (its On-demand line): from the
// period's spendLimitUsage (cents) and, where that sets no limit of the
// user's, GetHardLimit's reply (dollars; undefined when it couldn't be
// read). kind is "fixed" (up to limitDollars), "unlimited", "disabled" or
// "unavailable" (not known). A team member with no limit of their own
// spends the team's pool, which cursor-agent's view calls unlimited but
// which runs out at pooledLimit for the whole team (magpie #1868): that is
// a fixed limit on the pool's spend, with the member's own in mineDollars.
function onDemandOf(data, hard) {
  const s = data?.spendLimitUsage
  const usedDollars = Number(s?.individualUsed ?? 0) / 100
  const own =
    s?.individualLimit === undefined
      ? undefined
      : Number(s.individualLimit) > 0
        ? { kind: "fixed", usedDollars, limitDollars: Number(s.individualLimit) / 100 }
        : { kind: "disabled", usedDollars }
  if (s?.limitType === "team") {
    if (own) return own
    if (hard && (hard.noUsageBasedAllowed || !(Number(hard.hardLimit) > 0))) return { kind: "disabled", usedDollars }
    // pooledLimit and pooledRemaining come as int64 strings, pooledUsed as a number
    const pool = Number(s.pooledLimit ?? 0)
    if (pool > 0) return { kind: "fixed", usedDollars: Number(s.pooledUsed ?? 0) / 100, limitDollars: pool / 100, mineDollars: usedDollars }
    return hard ? { kind: "unlimited", usedDollars } : { kind: "unavailable", usedDollars }
  }
  if (!hard) return own ?? { kind: "unavailable", usedDollars }
  if (hard.noUsageBasedAllowed) return { kind: "disabled", usedDollars }
  const limit = Number(hard.hardLimit ?? 0)
  if (limit >= 2147483647) return { kind: "unlimited", usedDollars }
  return limit > 0 ? { kind: "fixed", usedDollars, limitDollars: limit } : { kind: "disabled", usedDollars }
}

// hardLimit is GetHardLimit's reply, undefined when it couldn't be read.
async function hardLimit(tok) {
  try {
    const res = await fetch(API + "/aiserver.v1.DashboardService/GetHardLimit", {
      method: "POST",
      headers: { Authorization: `Bearer ${tok}`, "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...await teamHeader(tok) },
      body: "{}",
      signal: AbortSignal.timeout(15_000),
    })
    return res.ok ? ((await res.json()) ?? undefined) : undefined
  } catch {
    return undefined
  }
}

// dollars, thousands grouped: a team's pool runs to six figures ($451,200.00)
const dollars = (n) => "$" + n.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })

// usage is the account's windows this billing period; an enterprise plan
// reports spend instead, and gets none. ids are the models it can be
// asked for, each counted by the pool Cursor bills it to. An account that
// may spend on-demand once its included usage is gone (fottencity on
// Discord: a team that pays for more) is still served when the pools are
// spent, so they are shown aside, and its On-demand spend is what can run
// out: at its limit (a team member with none of their own: the team's
// pool), or never when it has none.
async function usage(tok, ids) {
  const [res, hard] = await Promise.all([
    fetch(API + "/aiserver.v1.DashboardService/GetCurrentPeriodUsage", {
      method: "POST",
      headers: { Authorization: `Bearer ${tok}`, "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...await teamHeader(tok) },
      body: "{}",
      signal: AbortSignal.timeout(15_000),
    }),
    hardLimit(tok),
  ])
  if (!res.ok) return { error: statusText(res.status), windows: [] }
  const data = await res.json()
  const u = data?.planUsage
  if (!u) return { windows: [] }
  const ms = /^[+-]?\d+$/.test(data.billingCycleEnd ?? "") ? Number(data.billingCycleEnd) : 0
  const resets = ms > 0 ? { resetsAt: new Date(ms).toISOString() } : {}
  const bucket = new Set((data.autoBucketModels ?? []).map(poolBase))
  const inPool = (model) => {
    model = poolBase(model)
    if (model === "auto") model = "default" // the CLI's Auto is default in Cursor's API
    // the server names a family (grok-4.8); the CLI asks for one at an
    // effort or speed (grok-4.8-high-fast)
    return bucket.has(model) || firstParty(model)
  }
  // Auto is always one, so the pool's list is never empty (which would
  // count every model). A model listed at its context sizes (grok-4.7@256k)
  // is still asked for by its bare name (grok-4.7) where it was picked
  // before the list had sizes, and runs as its default, so the pool names
  // that too (Will on Discord)
  const asked = ids.flatMap((id) => [id, id.replace(/@[^@]*$/, "")])
  const pool = [...new Set(["auto", ...asked, ...(data.autoBucketModels ?? [])])].filter(inPool)
  const num = (v) => (typeof v === "number" ? v : 0)
  const od = onDemandOf(data, hard)
  const pooled = od.kind === "fixed" || od.kind === "unlimited" ? { aside: true } : {}
  const onDemand =
    od.kind === "fixed"
      ? [{ name: "On-demand", used: Math.max(0, Math.min(100, (100 * od.usedDollars) / od.limitDollars)), display: `${dollars(od.usedDollars)} / ${dollars(od.limitDollars)}` + (od.mineDollars === undefined ? "" : ` (you: ${dollars(od.mineDollars)})`), ...resets }]
      : od.kind === "unlimited"
        ? [{ name: "On-demand", used: 0, display: dollars(od.usedDollars), ...resets, aside: true }]
        : []
  // the two pools fit the line; the total goes in its tooltip
  return {
    windows: [
      { name: "Cursor Models", used: num(u.autoPercentUsed), ...resets, models: pool, ...pooled },
      { name: "Other Models", used: num(u.apiPercentUsed), ...resets, notModels: pool, ...pooled },
      ...onDemand,
      { name: "Total", used: num(u.totalPercentUsed), ...resets, aside: true },
    ],
  }
}

// Grok Bot's separate allowance, using the CLI JWT as a dashboard session.
async function botUsage(tok) {
  try {
    const subject = JSON.parse(Buffer.from(tok.split(".")[1], "base64url").toString()).sub
    if (typeof subject !== "string") return []
    const user = subject.split("|").filter(Boolean).at(-1)
    if (!user || !/^[a-zA-Z0-9._-]+$/.test(user)) return []
    const res = await fetch(WEBSITE + "/api/dashboard/get-sand-usage-status", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        Origin: WEBSITE,
        Cookie: "WorkosCursorSessionToken=" + encodeURIComponent(user + "::" + tok),
      },
      body: "{}",
      redirect: "error",
      signal: AbortSignal.timeout(5_000),
    })
    if (!res.ok) return []
    const data = await res.json()
    const paid = typeof data?.includedLimitZero === "boolean"
      ? !data.includedLimitZero : data?.hasNonZeroIncludedLimit === true
    const trial = !paid && typeof data?.sandTrialExpiresAt === "string" && Date.parse(data.sandTrialExpiresAt) > Date.now()
    if ((!paid && !trial) || typeof data?.usagePercent !== "number" || !Number.isFinite(data.usagePercent)) return []
    const reset = paid && typeof data.nextResetTimestampUtc === "string" ? Date.parse(data.nextResetTimestampUtc) : NaN
    return [{
      name: trial ? "Grok Bot (trial)" : "Grok Bot",
      used: Math.max(0, Math.min(100, data.usagePercent)),
      aside: true, // Bot exhaustion must not stop Cursor model requests.
      ...(Number.isFinite(reset) ? { resetsAt: new Date(reset).toISOString(), span: 604800 } : {}),
    }]
  } catch {
    return []
  }
}

// ---- models ----------------------------------------------------------------------
//
// The list is Cursor's model picker (AiService/AvailableModels, asked as the
// CLI asks it), the way pi-cursor-sdk lists Cursor's SDK catalog: each model
// is offered once for each of its context sizes, named "Claude Opus 5.5 @
// 300k" and with that many tokens as its window; one without a context
// parameter is offered once, with Cursor's limit for it. Effort is the
// request's reasoning_effort and fast its service_tier, sent as the model's
// own parameters, so neither is a model of its own. A request goes as the
// variant those parameters pick, in Max Mode when that variant is Max Mode's.

const LEVEL_RANK = ["none", "minimal", "low", "medium", "high", "xhigh", "max"]
const EFFORT_PARAMS = ["effort", "reasoning_effort", "reasoning"]

// the effort words of Cursor's own ids and the levels they are; extra-high
// before high
const ID_EFFORTS = [["extra-high", "xhigh"], ["xhigh", "xhigh"], ["minimal", "minimal"], ["none", "none"], ["low", "low"], ["medium", "medium"], ["high", "high"], ["max", "max"]]

const cut = (s, suffix) => (s.endsWith(suffix) && s.length > suffix.length ? s.slice(0, -suffix.length) : null)

// splitID is one of Cursor's own ids (a variant's legacySlug, as
// cursor-agent models lists it) taken apart: its family, and the effort
// ("" for none named) it is at. Thinking comes before the effort or after
// it; in the family it goes after, as does fast. The family is the id 0.1.x
// listed a model by.
function splitID(id) {
  let s = id
  let fast = false
  let thinking = false
  let effort = ""
  let b
  if ((b = cut(s, "-fast")) !== null) [s, fast] = [b, true]
  if ((b = cut(s, "-thinking")) !== null) [s, thinking] = [b, true]
  for (const [word, level] of ID_EFFORTS) {
    if ((b = cut(s, "-" + word)) !== null) {
      ;[s, effort] = [b, level]
      break
    }
  }
  if (!thinking && (b = cut(s, "-thinking")) !== null) [s, thinking] = [b, true]
  if (thinking) s += "-thinking"
  if (fast) s += "-fast"
  return [s, effort]
}

// left out of the CLI's picker as it leaves them out (model-service.ts)
const PICKER_SKIP = new Set(["claude-4.5-haiku", "claude-4.5-haiku-thinking", "gemini-2.5-pro", "gemini-2.5-flash", "claude-haiku-4-5"])

const clean = (s) => String(s ?? "").replaceAll("\u200b", "").split(/\s+/).filter(Boolean).join(" ")

// tokensOf is a context value as tokens: "300k" 300000, "1m" 1000000.
function tokensOf(value) {
  const m = /^(\d+(?:\.\d+)?)([km])$/i.exec(String(value ?? "").trim())
  return m ? Math.round(Number(m[1]) * (m[2].toLowerCase() === "m" ? 1_000_000 : 1_000)) : 0
}

// paramDefs is a picker model's parameters: id to the values it takes.
function paramDefs(m) {
  const out = {}
  for (const d of m.parameterDefinitions ?? []) {
    const t = d?.parameterType ?? {}
    const vs = (t.enumParameter ?? t.booleanParameter)?.values ?? []
    if (d?.id) out[d.id] = vs.map((v) => String(v.value))
  }
  return out
}

const valuesOf = (v) => Object.fromEntries((v?.parameterValues ?? []).map((p) => [String(p.id), String(p.value)]))

// defaultVariant is the variant the CLI picks for a model asked for by
// name: its default without Max Mode, else with it, else the first.
const defaultVariant = (m) => {
  const vs = m?.variants ?? []
  return vs.find((v) => v.isDefaultNonMaxConfig) ?? vs.find((v) => v.isDefaultMaxConfig) ?? vs[0]
}

// needsMax is whether a variant is run in Max Mode only, as the CLI reads
// it: a model with no other mode, or a Max Mode variant.
const needsMax = (m, v) => m?.supportsNonMaxMode === false || v?.isMaxMode === true

// effortsOf is the efforts a model takes, as pi's levels, each with the
// value Cursor's parameter has for it (extra-high is xhigh).
function effortsOf(defs) {
  const pid = EFFORT_PARAMS.find((p) => defs[p]?.length)
  // thinking alone, on or off: high and none
  if (!pid && defs.thinking?.includes("true") && defs.thinking.includes("false")) return { pid: "thinking", levels: { none: "false", high: "true" } }
  if (!pid) return { pid: "", levels: {} }
  const levels = {}
  for (const v of defs[pid]) {
    const l = v === "extra-high" ? "xhigh" : v
    if (LEVEL_RANK.includes(l)) levels[l] = v
  }
  return { pid, levels }
}

// served is a picker model's variants the account is served. With the
// usable list at hand (the ids cursor-agent models shows), a variant whose
// own id isn't in it is one the account hasn't got (Sonnet 4.6 at any effort
// but medium, say). A model the usable list has nothing of is the picker's
// alone (GLM-5.3), and is kept whole.
function served(m, ids) {
  const vs = m.variants ?? []
  if (!ids) return vs
  const got = vs.filter((v) => !v.legacySlug || ids.has(v.legacySlug))
  return got.some((v) => v.legacySlug) ? got : vs
}

// modelsOf is the picker's models as offered: one entry for each context
// size a model has, or one with Cursor's limit when it has none. A model
// served in part is offered at the sizes and efforts it is served at.
function modelsOf(picker, ids) {
  const out = []
  const used = new Set()
  for (const m of picker) {
    if (!m?.name || m.isHidden || m.isChatOnly || m.onlySupportsCmdK || m.supportsAgent === false || PICKER_SKIP.has(m.name)) continue
    const defs = paramDefs(m)
    const variants = served(m, ids)
    const partial = variants.length < (m.variants ?? []).length
    const has = (pid, value) => !partial || !variants.some((v) => pid in valuesOf(v)) || variants.some((v) => valuesOf(v)[pid] === value)
    const def = defaultVariant({ variants })
    const { pid, levels: all } = effortsOf(defs)
    const levels = Object.fromEntries(Object.entries(all).filter(([, value]) => has(pid, value)))
    const base = { run: m.name, title: clean(m.clientDisplayName || m.name), pid, levels, fast: !!defs.fast?.length, variants, def, partial, model: m }
    const contexts = (defs.context ?? []).filter((c) => has("context", c))
    if (contexts.length) {
      for (const c of contexts) {
        const id = m.name + "@" + c
        if (used.has(id)) continue
        used.add(id)
        out.push({ ...base, id, name: base.title + " @ " + c, context: tokensOf(c) || DEFAULT_CONTEXT, size: c, offersFast: fastAt(variants, c) })
      }
    } else if (!used.has(m.name)) {
      used.add(m.name)
      const limit = needsMax(m, def) ? m.contextTokenLimitForMaxMode || m.contextTokenLimit : m.contextTokenLimit
      out.push({ ...base, id: m.name === "default" ? "auto" : m.name, name: base.title, context: limit > 0 ? limit : DEFAULT_CONTEXT, size: "", offersFast: fastAt(variants, "") })
    }
  }
  return out
}

// fastAt is whether Cursor has a fast variant the account is served at
// the context size (any, for a model without sizes): what magpie offers
// fast for, which 0.1.x said with a -fast model of its own.
const fastAt = (variants, size) =>
  variants.some((v) => {
    const have = valuesOf(v)
    return have.fast === "true" && (!size || have.context === undefined || have.context === size)
  })

// nearest is the variant of vs most like the parameters wanted, one at
// their context size before any other.
function nearest(vs, want) {
  let best
  let most = -1
  for (const v of vs) {
    const have = valuesOf(v)
    const n = Object.keys(want).filter((k) => have[k] === want[k]).length + (have.context === want.context ? 100 : 0)
    if (n > most) [best, most] = [v, n]
  }
  return best
}

// pick is the variant a request runs as: the model's context size, the
// effort asked for (else the default's) and fast as asked, matched against
// Cursor's variants. With no variant that matches, the parameters go as
// they are, from the default's; of a model served in part, as the nearest
// variant the account has. seed is the variant an id of Cursor's own named
// (request), in the default's place.
function pick(entry, effort, fast, seed) {
  const def = entry.def
  const want = { ...valuesOf(seed ?? def) }
  if (entry.size) want.context = entry.size
  if (effort === "off") effort = "none"
  if (entry.pid && entry.pid !== "thinking" && effort === "none" && !("none" in entry.levels) && "thinking" in want) {
    // a Claude asked not to think: thinking off at its default effort
    want.thinking = "false"
  } else if (entry.pid) {
    const at = effort ? fitEffort(effort, Object.keys(entry.levels)) : ""
    if (at && entry.levels[at]) want[entry.pid] = entry.levels[at]
    // a variant named says for itself whether it thinks
    if (!seed && entry.pid !== "thinking" && "thinking" in want && at) want.thinking = "true"
  }
  // fast only when asked: it costs more
  if (entry.fast) want.fast = fast ? "true" : "false"
  const same = (v) => {
    const have = valuesOf(v)
    return Object.keys({ ...have, ...want }).every((k) => have[k] === undefined || want[k] === undefined || have[k] === want[k])
  }
  const v = entry.variants.find(same) ?? (seed ? branch(entry, want) : undefined) ?? (entry.partial ? nearest(entry.variants, want) : undefined)
  const params = Object.entries(v ? { ...want, ...valuesOf(v) } : want).map(([id, value]) => ({ id, value }))
  // no variant: Max Mode when the size is more than Cursor allows outside it
  const maxMode = v ? needsMax(entry.model, v) : needsMax(entry.model, def) || tokensOf(entry.size) > (entry.model.contextTokenLimit || Infinity)
  // the agent API takes the variant's own id (its legacySlug, as
  // cursor-agent models lists it: claude-opus-5-5-high), not the picker's
  // model name; the default's when no variant matched
  const id = v?.legacySlug || def?.legacySlug || entry.run
  return { id, params, maxMode }
}

// branch is, for an id of Cursor's own asked at an effort no variant has
// with the rest of its parameters (claude-opus-5-low at xhigh: Opus 5
// without thinking stops at high), the variant with those parameters at the
// nearest effort it has, not the default's id with parameters that
// contradict it (claude-opus-5-thinking-high with thinking=false). Where
// the default differs from what is asked in the effort alone, and no
// variant at all is named for that effort, the effort goes as asked, as for
// a listed model (claude-opus-5-5-xhigh). When another branch has it, the
// default's own branch stops short there, and its nearest goes (#55).
function branch(entry, want) {
  if (!entry.pid || entry.pid === "thinking") return undefined
  const level = (x) => (x === "extra-high" ? "xhigh" : x)
  const def = valuesOf(entry.def)
  const named = entry.variants.some((v) => level(valuesOf(v)[entry.pid]) === level(want[entry.pid]))
  if (!named && Object.keys(want).every((k) => k === entry.pid || k === "context" || def[k] === undefined || def[k] === want[k])) return undefined
  const vs = entry.variants.filter((v) => {
    const have = valuesOf(v)
    return entry.pid in have && Object.keys(want).every((k) => k === entry.pid || have[k] === undefined || have[k] === want[k])
  })
  const at = fitEffort(level(want[entry.pid]), vs.map((v) => level(valuesOf(v)[entry.pid])))
  return at ? vs.find((v) => level(valuesOf(v)[entry.pid]) === at) : undefined
}

// fitEffort is the nearest effort a model has to the one asked for; max
// for ultra, and none only when asked.
function fitEffort(want, levels) {
  if (want === "ultra") want = "max"
  if (!levels.length || levels.includes(want)) return want
  const at = LEVEL_RANK.indexOf(want)
  if (at < 0) return ""
  let best = ""
  let dist = LEVEL_RANK.length
  for (const l of levels) {
    const i = LEVEL_RANK.indexOf(l)
    if (i < 0 || l === "none") continue
    const d = Math.abs(i - at)
    if (d < dist || (d === dist && i > at)) [best, dist] = [l, d]
  }
  return best
}

// usable is the account's list, kept a while, by token: Cursor's picker,
// less the variants the usable list (AgentService/GetUsableModels, what
// cursor-agent models shows) says the account hasn't got. A picker Cursor
// can't give is a failure: magpie keeps the list it had. A usable list it
// can't give leaves the picker as it is.
const lists = new Map()
async function usable(tok) {
  const h = createHash("sha256").update(tok).digest("hex")
  const had = lists.get(h)
  if (had && Date.now() - had.at < MODELS_KEEP) return had.raw
  const [j, ids] = await Promise.all([
    unary(API, "aiserver.v1.AiService/AvailableModels", tok, { useModelParameters: true, doNotUseMarkdown: true }, 30_000),
    unary(API, "agent.v1.AgentService/GetUsableModels", tok, {}, 8_000).then(
      (u) => {
        const ids = new Set((u?.models ?? []).flatMap((m) => [m.modelId, m.displayModelId]).filter(Boolean))
        return ids.size ? ids : null
      },
      () => null,
    ),
  ])
  const raw = modelsOf(Array.isArray(j?.models) ? j.models : [], ids)
  if (!raw.length) throw new Error("Cursor listed no models")
  lists.set(h, { at: Date.now(), raw })
  return raw
}

// named is the model and variant an id the list doesn't have stands for,
// when it is one of Cursor's own, as the CLI reads one and as 0.1.x listed
// them, so a model picked before the list changed still runs: a variant's
// own id (claude-opus-5-5-high) is that variant; a family of them, the id
// 0.1.x listed (claude-opus-5-5, claude-opus-5-5-fast, cursor-grok-4.6,
// or with an effort the account lacks), is the family's variant nearest
// the default, fast when the family is and at the id's effort; the picker's
// name for a model, or a legacy slug of it, is its default. Each at the
// variant's context size, the default's when the id doesn't say.
function named(raw, id) {
  const models = raw.filter((e, i) => raw.findIndex((x) => x.model === e.model) === i)
  const at = (e, v) => raw.find((x) => x.model === e.model && x.size === (valuesOf(v).context ?? "")) ?? e
  const as = (e, vs, effort) => {
    const seed = nearest(vs, valuesOf(e.def))
    return { entry: at(e, seed), seed, effort, fast: valuesOf(seed).fast === "true" }
  }
  for (const e of models) {
    const vs = e.variants.filter((v) => v.legacySlug === id)
    if (vs.length) return as(e, vs, "")
  }
  const [family, effort] = splitID(id)
  for (const e of models) {
    const vs = e.variants.filter((v) => v.legacySlug && splitID(v.legacySlug)[0] === family)
    if (vs.length) return as(e, vs, effort)
  }
  for (const e of models) {
    const m = e.model
    if ([m.name, ...(m.legacySlugs ?? []), ...(m.idAliases ?? [])].includes(id)) return { entry: at(e, e.def), seed: undefined, effort: "", fast: false }
  }
  return null
}

// request is how a chat's model is asked of Cursor: the variant's own id,
// its parameters and its mode. An id the list doesn't have that is one of
// Cursor's own goes as the variant it names, at the effort asked for, fast
// when it says so or the request does; any other goes as it is named.
function request(raw, model, effort, fast) {
  const entry = raw.find((m) => m.id === model)
  if (entry) return pick(entry, effort, fast)
  const old = named(raw, model)
  if (old) return pick(old.entry, effort || old.effort, fast || old.fast, old.seed)
  return { id: model === "auto" ? "default" : model, params: [], maxMode: false }
}

function runtimeModel(m) {
  const efforts = LEVEL_RANK.filter((l) => l in m.levels)
  return {
    id: m.id,
    providerID: ID,
    name: m.name,
    api: { id: m.id, url: AGENT, npm: CHAT },
    status: "active",
    headers: {},
    options: {},
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: { context: m.context || DEFAULT_CONTEXT, output: 0 },
    capabilities: {
      temperature: false,
      reasoning: efforts.length > 0,
      attachment: true,
      toolcall: true,
      input: { text: true, image: true, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: Object.fromEntries(efforts.map((e) => [e, { reasoningEffort: e }])),
    // a request's service_tier priority (or fast) runs it as Cursor's fast
    // variant: magpie offers Fast for the models that have one
    ...(m.offersFast ? { fast: true } : {}),
  }
}


// ---- protobuf, by hand ------------------------------------------------------------

class PB {
  parts = []
  push(b) {
    this.parts.push(b)
    return this
  }
  uv(v) {
    const out = []
    while (v >= 0x80) {
      out.push((v % 128) | 0x80)
      v = Math.floor(v / 128)
    }
    out.push(v)
    return this.push(Uint8Array.from(out))
  }
  tag(num, wire) {
    return this.uv(num * 8 + wire)
  }
  varint(num, v) {
    return this.tag(num, 0).uv(v)
  }
  bytes(num, v) {
    const b = v instanceof PB ? v.done() : v ?? EMPTY
    return this.tag(num, 2).uv(b.length).push(b)
  }
  str(num, s) {
    return this.bytes(num, Buffer.from(s, "utf8"))
  }
  double(num, v) {
    const b = Buffer.alloc(8)
    b.writeDoubleLE(v)
    return this.tag(num, 1).push(b)
  }
  done() {
    return Buffer.concat(this.parts)
  }
}
const pb = () => new PB()

// fields reads a message's fields; a malformed tail is dropped.
function fields(b) {
  b = Buffer.isBuffer(b) ? b : Buffer.from(b ?? EMPTY)
  const out = []
  let i = 0
  const uv = () => {
    let v = 0
    let m = 1
    while (i < b.length) {
      const c = b[i++]
      v += (c & 0x7f) * m
      if (c < 0x80) return v
      m *= 128
    }
    return -1
  }
  while (i < b.length) {
    const key = uv()
    if (key < 0) break
    const f = { num: Math.floor(key / 8), wire: key % 8, n: 0, data: EMPTY }
    switch (f.wire) {
      case 0: {
        const v = uv()
        if (v < 0) return out
        f.n = v
        break
      }
      case 1:
        if (i + 8 > b.length) return out
        f.data = b.subarray(i, i + 8)
        f.n = Number(b.readBigUInt64LE(i))
        i += 8
        break
      case 2: {
        const l = uv()
        if (l < 0 || i + l > b.length) return out
        f.data = b.subarray(i, i + l)
        i += l
        break
      }
      case 5:
        if (i + 4 > b.length) return out
        f.n = b.readUInt32LE(i)
        i += 4
        break
      default:
        return out
    }
    out.push(f)
  }
  return out
}

const field = (fs, num) => fs.find((f) => f.num === num)
const pbStr = (fs, num) => field(fs, num)?.data.toString("utf8") ?? ""
const pbNum = (fs, num) => field(fs, num)?.n ?? 0

// pbValue is a google.protobuf.Value.
function pbValue(v) {
  if (v === null || v === undefined) return pb().varint(1, 0)
  if (typeof v === "number") return pb().double(2, v)
  if (typeof v === "string") return pb().str(3, v)
  if (typeof v === "boolean") return pb().varint(4, v ? 1 : 0)
  if (Array.isArray(v)) {
    const l = pb()
    for (const e of v) l.bytes(1, pbValue(e))
    return pb().bytes(6, l)
  }
  if (typeof v === "object") {
    const st = pb()
    for (const k of Object.keys(v).sort()) st.bytes(1, pb().str(1, k).bytes(2, pbValue(v[k])))
    return pb().bytes(5, st)
  }
  return pb().varint(1, 0)
}

// pbAny reads a google.protobuf.Value.
function pbAny(b) {
  for (const f of fields(b)) {
    switch (f.num) {
      case 1:
        return null
      case 2:
        return f.data.length === 8 ? f.data.readDoubleLE(0) : 0
      case 3:
        return f.data.toString("utf8")
      case 4:
        return f.n !== 0
      case 5: {
        const m = {}
        for (const e of fields(f.data)) {
          if (e.num !== 1) continue
          const kv = fields(e.data)
          const v = field(kv, 2)
          m[pbStr(kv, 1)] = v ? pbAny(v.data) : null
        }
        return m
      }
      case 6:
        return fields(f.data).filter((e) => e.num === 1).map((e) => pbAny(e.data))
    }
  }
  return null
}

// frame wraps a message as one frame of a Connect stream.
function frame(msg) {
  const head = Buffer.alloc(5)
  head.writeUInt32BE(msg.length, 1)
  return Buffer.concat([head, msg])
}

// frames reads a Connect stream's frames: a message, or the stream's end
// with its JSON.
async function* frames(body) {
  let buf = EMPTY
  for await (const chunk of body) {
    buf = buf.length ? Buffer.concat([buf, chunk]) : Buffer.from(chunk)
    while (buf.length >= 5) {
      const n = buf.readUInt32BE(1)
      if (n > 64 << 20) throw new Error("a malformed stream")
      if (buf.length < 5 + n) break
      const flags = buf[0]
      let data = buf.subarray(5, 5 + n)
      buf = buf.subarray(5 + n)
      if (flags & 1) data = gunzipSync(data)
      yield { end: (flags & 2) !== 0, data }
    }
  }
  if (buf.length) throw new Error("unexpected EOF")
}

// ---- failures -----------------------------------------------------------------------

const regional = (msg) => msg.toLowerCase().includes("region")

// failure is the status and message for Cursor's Connect error, whose
// details say it best: {"code", "message", "details": [{"debug": {"error",
// "details": {"title", "detail"}}}]}, or the same under "error" at a
// stream's end.
function failure(status, text) {
  let e
  try {
    e = JSON.parse(typeof text === "string" ? text : Buffer.from(text).toString("utf8"))
  } catch {
    return statusOf(status, "", String(text ?? "").trim())
  }
  const f = e?.error && typeof e.error === "object" ? e.error : e ?? {}
  let msg = f.message ?? ""
  for (const d of f.details ?? []) {
    const t = `${d?.debug?.details?.title ?? ""}: ${d?.debug?.details?.detail ?? ""}`.trim()
    if (t !== ":") msg = t.replace(/^[:\s]+|[:\s]+$/g, "")
  }
  if (!msg || msg === "Error") msg = f.code ?? ""
  if (!f.code && status >= 200 && status < 300) return { status, message: msg } // a stream that ended well
  return statusOf(status, f.code ?? "", msg)
}

// statusOf is the status and words for Cursor's error of this code and
// message. A region the team isn't served in says so, not to sign in.
function statusOf(status, code, msg) {
  msg ||= statusText(status)
  const low = msg.toLowerCase()
  // magpie names the provider before the message itself
  const out = (status, message) => ({ status, message })
  if (regional(msg)) return out(403, msg + " — Cursor serves your team only in some regions and turned this request away; signing in again won't change that")
  if (code === "permission_denied") return out(403, msg)
  if (code === "unauthenticated" || status === 401 || low.includes("expired"))
    return out(401, msg + " — sign in to Cursor again in magpie")
  if (code === "resource_exhausted" || low.includes("quota") || low.includes("rate limit") || low.includes("usage limit"))
    return out(429, "usage limit reached: " + msg)
  if (low.includes("too long") || low.includes("context length") || low.includes("too many tokens"))
    return out(400, "input is too long for the model's context: " + msg)
  if (code === "invalid_argument") return out(400, msg)
  if (code === "unavailable") return out(503, msg)
  if (code) return out(502, msg)
  return out(status, msg)
}

// ---- the conversation -------------------------------------------------------------

const textOf = (c) =>
  typeof c === "string" ? c : Array.isArray(c) ? c.filter((p) => p?.type === "text").map((p) => p.text ?? "").join("") : ""

// Chat clients replay thinking beside content. Cursor's transcript blobs
// use AI SDK reasoning parts; putting it in text would change its role.
const reasoningOf = (m) => [m.reasoning_content, m.reasoning].find((s) => typeof s === "string" && s.trim()) ?? ""

// joinSplitCalls joins an assistant message onto the assistant message
// before it when that one made tool calls: Anthropic takes consecutive
// assistant messages as one turn, and an agent can send a turn's parallel
// calls split over two of them, answered together after (yetone/magpie#1275).
// Answered at the second message, the first calls would be marked
// interrupted and their real results dropped. msgs is left as it is.
function joinSplitCalls(msgs) {
  const out = []
  for (const m of msgs) {
    const prev = out.at(-1)
    if (m?.role === "assistant" && prev?.role === "assistant" && prev.tool_calls?.length) {
      const content = [textOf(prev.content), textOf(m.content)].filter(Boolean).join("\n\n")
      const reasoning_content = [reasoningOf(prev), reasoningOf(m)].filter(Boolean).join("\n\n")
      out[out.length - 1] = { ...prev, content, reasoning_content, tool_calls: [...prev.tool_calls, ...(m.tool_calls ?? [])] }
      continue
    }
    out.push(m)
  }
  return out
}

function parseArgs(s) {
  if (s && typeof s === "object") return s
  try {
    const v = JSON.parse(s || "{}")
    return v && typeof v === "object" ? v : {}
  } catch {
    return {}
  }
}

// tools are the caller's tools the model may call: none for tool_choice
// "none", the one named when one is.
function toolsOf(chat) {
  const choice = chat.tool_choice
  const only = typeof choice === "object" ? choice?.function?.name : ""
  if (choice === "none") return []
  return (chat.tools ?? [])
    .filter((t) => t?.type === "function" && t.function?.name && (!only || t.function.name === only))
    .map((t) => {
      const schema = t.function.parameters ?? { type: "object", properties: {} }
      return { name: t.function.name, description: t.function.description ?? "", schema, schemaText: JSON.stringify(schema) }
    })
}

// NS is the MCP server the caller's tools are served as, the way Cursor's
// own client names a user's server by its mcp.json label (providerIdentifier,
// with "<label>-<tool>" as a call's name). It goes to Cursor in the prompt,
// each call and each tool definition, so it names nothing of the plugin or
// the app it runs in. Calls are read back under NAMESPACES: before 0.2.20
// the server was named "magpie", and a session begun then can still call it.
const NS = "tools"
const NAMESPACES = [NS, "magpie"]
const bareName = (s) => {
  for (const ns of NAMESPACES) if (s.startsWith(ns + "-")) return s.slice(ns.length + 1)
  return s
}

// catalog lists the caller's tools for the model. It sees MCP tools only
// through Cursor's GetDynamicTools and CallDynamicTool, and Cursor names
// them in a prompt of its own, which this conversation goes without:
// without the list the model says it has no such tool. Each entry is
// written as the CallDynamicTool call itself: listed as <tool name=...>,
// some models (GLM-5.3) called the names bare, as tools of their own, and
// those calls went nowhere (#11).
function catalog(tools) {
  if (!tools.length) return ""
  let s = `\n\n<dynamic_tool_catalog>\nThe MCP namespace "${NS}" has the tools below. They are reached only through \`${CALL}\`, never as tools of their own. Each entry is the exact call to make; its arguments must match the schema given. The schemas are complete, so there is no need to call \`GetDynamicTools\` first.\n`
  for (const t of tools) s += `<call>${CALL}({"namespace":"${NS}","toolName":"${t.name}","arguments":{...}})\n${t.description}\narguments schema: ${t.schemaText}\n</call>\n`
  return s + "</dynamic_tool_catalog>"
}

// callID is a call's id as Cursor gave it, before the line in it was sent
// to the caller as __.
const callID = (id) => (id.startsWith("call_") ? id.replace("__fc_", "\nfc_") : id)

function result(id, text, isError) {
  let res = text
  try {
    res = JSON.parse(text)
  } catch {}
  return {
    type: "tool-result",
    toolCallId: callID(id),
    toolName: CALL,
    result: res,
    experimental_content: [{ type: "text", text }],
    ...(isError ? { isError: true } : {}),
  }
}

// messages is the conversation as AI SDK messages, in JSON: the system
// prompt with the caller's tools listed, then each turn. A tool result
// answers a call of the reply before it; a call left unanswered is
// answered for, as the API wants every call to have its result. With it,
// the user's last words and a guess at the prompt's size.
function conversation(chat, tools) {
  const out = []
  const add = (m) => out.push(Buffer.from(JSON.stringify(m)))
  let size = 0
  const system = []
  let last = "."
  for (const m of chat.messages ?? [])
    if (m.role === "system" || m.role === "developer") system.push(textOf(m.content))
  const sys = system.filter(Boolean).join("\n\n") + catalog(tools)
  size += sys.length
  if (sys) add({ role: "system", content: sys })
  let pending = []
  let results = []
  const answer = () => {
    for (const id of pending) results.push(result(id, NO_RESULT, true))
    pending = []
    if (results.length) add({ role: "tool", content: results })
    results = []
  }
  for (const m of joinSplitCalls(chat.messages ?? [])) {
    if (m.role === "system" || m.role === "developer") continue
    if (m.role === "tool") {
      const id = m.tool_call_id ?? ""
      const i = pending.indexOf(id)
      if (i >= 0) {
        pending.splice(i, 1)
        const text = textOf(m.content)
        size += text.length
        // a failed tool's result says so, as the built-in's cursorResult
        // told Cursor isError
        results.push(result(id, text, m.is_error === true || m.isError === true))
      }
      continue
    }
    if (m.role === "assistant") {
      answer()
      const content = []
      const reasoning = reasoningOf(m)
      if (reasoning) content.push({ type: "reasoning", text: reasoning })
      const text = textOf(m.content)
      if (text) content.push({ type: "text", text })
      size += reasoning.length + text.length
      for (const c of m.tool_calls ?? []) {
        if (c?.type && c.type !== "function") continue
        const args = parseArgs(c.function?.arguments)
        size += (c.function?.arguments ?? "").length + (c.function?.name ?? "").length
        content.push({ type: "tool-call", toolCallId: callID(c.id), toolName: CALL, args: { namespace: NS, toolName: c.function?.name, arguments: args } })
        pending.push(c.id)
      }
      if (content.length) add({ role: "assistant", content })
      continue
    }
    // the user's
    const content = []
    const parts = typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content ?? []
    let first = ""
    for (const p of parts) {
      if (p?.type === "text" && p.text) {
        content.push({ type: "text", text: p.text })
        size += p.text.length
        first ||= p.text
      } else if (p?.type === "image_url") {
        const u = typeof p.image_url === "string" ? p.image_url : p.image_url?.url ?? ""
        const d = /^data:([^;,]+);base64,(.*)$/s.exec(u)
        if (d) content.push({ type: "image", mimeType: d[1], image: { __type: "Uint8Array", hex: Buffer.from(d[2], "base64").toString("hex") } })
        else content.push({ type: "text", text: `[attachment image: ${u}]` })
      } else if (p?.type === "file") {
        content.push({ type: "text", text: `[attachment ${p.file?.filename ?? "file"}]` })
      }
    }
    answer()
    if (first) last = first
    if (content.length) add({ role: "user", content })
  }
  answer()
  for (const t of tools) size += t.name.length + t.description.length + t.schemaText.length
  return { msgs: out, last, estimate: Math.floor(size / 4) }
}

// ---- the request ---------------------------------------------------------------------

// toolDef is an McpToolDefinition.
function toolDef(t) {
  const d = pb().str(1, t.name)
  if (t.description) d.str(2, t.description)
  d.bytes(3, pbValue(t.schema))
  return d.str(4, NS).str(5, t.name).str(6, t.schemaText).done()
}

const env = () => pb().str(1, GOOS).str(2, tmpdir()).str(10, "UTC")

// SESSION carries the session magpie (or OpenCode) names a request's
// conversation by, from the chat.headers hook to the loader's fetch. It
// goes no further: nothing of the request's headers is sent to Cursor.
const SESSION = "x-magpie-cursor-session"

// sessionOf is the session a chat.headers hook is told, "" when it names
// none. magpie, with no session header from the agent, makes one of the
// conversation's first user message ("magpie-" and 24 hex digits): that
// names no session, and is taken as none, as the built-in's
// nativeSessionOf takes no such header as none.
function sessionOf(input) {
  const s = String(input?.sessionID ?? "").trim()
  if (!s || /^magpie-[0-9a-f]{24}$/.test(s)) return ""
  return s.slice(0, 128)
}

// uuidOf is a version 4 UUID made of a hash's first 16 bytes.
function uuidOf(b) {
  b = Buffer.from(b.subarray(0, 16))
  b[6] = (b[6] & 0x0f) | 0x40
  b[8] = (b[8] & 0x3f) | 0x80
  const h = b.toString("hex")
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

// conversationID is the AgentRunRequest's conversation_id for a
// conversation, as the built-in's cursorConversation makes it (magpie
// #498): the same on every request of it, so that Cursor's backend keeps
// sending it to the machine that has its prompt cached — Grok on Cursor
// caches by machine. Cursor's own client keeps one conversationId per
// agent session, so it is made from what names the session — the
// client's prompt_cache_key (Codex's thread id), else the session magpie
// or OpenCode names — together with the conversation's first user
// message, which every later request repeats: subagents running at once
// under one session (Claude Code's Task agents share its session id) are
// separate conversations to Cursor, as they are to its own client. With
// nothing naming the session it is "", and each Run gets a new id as
// before: a first message alone ("hi") would put strangers' conversations
// under one id.
function conversationID(chat, session) {
  const key = (typeof chat?.prompt_cache_key === "string" ? chat.prompt_cache_key.trim() : "") || session || ""
  if (!key) return ""
  const msgs = Array.isArray(chat?.messages) ? chat.messages : []
  const first = msgs.find((m) => m?.role === "user") ?? msgs[0]
  const sum = createHash("sha256")
    .update("cursor conversation\0" + key + "\0" + (first === undefined ? "" : JSON.stringify(first)))
    .digest()
  return uuidOf(sum)
}

// usageOf is a TurnEndedUpdate as magpie counts usage, as the built-in's
// cursorUsage has it (magpie #498). Its input_tokens is the whole prompt,
// what was read from the cache and written to it included, as Cursor's
// own client has it (it takes both out to get the prompt's uncached
// rest); input is that rest, the cache added back for prompt_tokens. Its
// reasoning_tokens is kept as reasoning.
function usageOf(uf) {
  const [inp, cr, cw] = [pbNum(uf, 1), pbNum(uf, 3), pbNum(uf, 4)]
  return { input: Math.max(inp - cr - cw, 0), output: pbNum(uf, 2), cacheRead: cr, cacheWrite: cw, reasoning: pbNum(uf, 5) }
}

// A step that calls the caller's tools has no TurnEndedUpdate: Cursor ends
// the turn, and tells what it used, only once the client has run the tools
// and sent their results in the same Run, and the caller runs them after
// the Run is closed (magpie #676). Before that it sends nothing more than
// the conversation's checkpoint and heartbeats, and a cancelled Run ends
// with an error and no usage either. The dashboard's usage events count
// every Run, by its conversation_id, the uncached input apart from what
// was read from the cache and written to it, as TurnEndedUpdate does; one
// shows about 2.5 s after the Run is closed. STEP_WAIT is how long a step's
// is looked for: first after that long, then every so often until the
// end (each ask given `ask` to answer).
//
// Far from Cursor none shows in that time (yetone/magpie#1053: a round
// trip of 4.2 s, the event 4–5 s after the Run closes), and waiting longer
// would hold every tool step longer. So a tool step's Run is owed by its
// conversation (owed): the step counts every event of the conversation's
// owed Runs that has shown by the end of its wait, its own or earlier
// ones, and what hasn't shown is collected by the conversation's next
// steps, in the background from `owedAt` after the Run closed. A step
// whose count is still owed says 0 used; the conversation's steps add up
// to what Cursor counted, some of it a step or two late. A step that ends
// the turn waits for what is still owed, as nothing may follow it soon.
// After `skipAfter` steps in a row of an account's left something owed,
// its steps stop waiting (but every `retryEvery`th), and leave it to the
// next. A debt not collected in `owedFor` is let go. With no
// conversation_id naming the session nothing later could collect a Run's
// event: its step looks for it as before, and is guessed without it.
const STEP_WAIT = { first: 1500, every: 500, until: 6000, ask: 5000, skipAfter: 3, retryEvery: 10, owedAt: 5000, owedFor: 600_000 }

// claimed are the usage events already counted, by account, conversation
// and time, so that two steps of one conversation never count the same one.
const claimed = new Map()

// owed are what each account's conversation still owes, by credential
// fingerprint and conversation_id: n Runs whose events are to be counted,
// the earliest started at since, looked for from ready on; and carry, a
// count collected that no step gave out.
const owed = new Map()

// flights are each account's conversation collections still going, which
// a step that ends the turn waits for.
const flights = new Map()

// unreadable are the accounts (by token) whose usage events Cursor won't
// give (a 4xx), for owedFor: their steps are guessed, as before, and owe
// nothing. misses are how many steps in a row of an account's left
// something owed.
const unreadable = new Map()
const misses = new Map()
const tokKey = (tok) => createHash("sha256").update(tok).digest("hex")
const usageKey = (tok, conv) => tokKey(tok) + "\0" + conv
function canRead(tok) {
  const at = unreadable.get(tokKey(tok))
  if (at === undefined) return true
  if (Date.now() - at < STEP_WAIT.owedFor) return false
  unreadable.delete(tokKey(tok))
  return true
}

const ZERO = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 })
function addUsage(a, b) {
  if (!b) return a
  if (!a) return { ...b }
  for (const k of Object.keys(a)) a[k] += b[k] ?? 0
  return a
}

// owe adds to the account/conversation's debt. A native Run bounds older
// event timestamps before its start: its authoritative final must never be
// collected as an earlier Run. Keep that bound when a later stateless Run
// adds unbounded debt. Ordinary stateless debt retains its original shape.
const DEBT_GROUP_LIMIT = 64
function owe(key, { since = Date.now(), n = 0, ready = Date.now(), carry = null, before }) {
  for (const [k, d] of owed) {
    if (d.groups) {
      d.groups = d.groups.filter((g) => Date.now() - g.since <= STEP_WAIT.owedFor)
      d.n = d.groups.reduce((sum, g) => sum + g.n, 0)
      if (Date.now() - d.since > STEP_WAIT.owedFor) d.carry = null
      if (!d.n && !d.carry) owed.delete(k)
      else if (d.groups.length) d.since = Math.min(...d.groups.map((g) => g.since))
    } else if (Date.now() - d.since > STEP_WAIT.owedFor) owed.delete(k)
  }
  if (n <= 0 && !carry) return
  const d = owed.get(key)
  if (!d) return void owed.set(key, { since, n, ready, carry: carry && { ...carry }, ...(before !== undefined ? { before } : {}) })
  if (n > 0 && (d.groups || d.before !== undefined || before !== undefined)) {
    const groups = d.groups ?? (d.n > 0 ? [{ since: d.since, n: d.n, ready: d.ready, before: d.before }] : [])
    const same = groups.find((g) => g.before === before)
    if (same) {
      same.since = Math.min(same.since, since)
      same.n += n
      same.ready = Math.min(same.ready, ready)
    } else groups.push({ since, n, ready, before })
    // As with expired dashboard debt, overflow stays unknown, never estimated
    // or merged across bounds into an authoritative native Run's event.
    groups.sort((a, b) => a.since - b.since)
    d.groups = groups.slice(-DEBT_GROUP_LIMIT)
    delete d.before
    d.n = d.groups.reduce((sum, g) => sum + g.n, 0)
    d.since = Math.min(...d.groups.map((g) => g.since))
  } else {
    d.since = Math.min(d.since, since)
    d.n += n
  }
  d.ready = Math.min(d.ready, ready)
  d.carry = addUsage(d.carry, carry)
}

// usageEvents are the dashboard's events of conversation conv from since
// on, the `want` oldest not counted yet, summed: usage (null when none),
// each with its time (events), how many were found (got), and refused
// when the account can't read them.
// Polling, they are looked for as STEP_WAIT says, from `first` on, until
// all show; else asked for once.
async function usageEvents(tok, conv, since, want, { poll = false, first = STEP_WAIT.first, signal, before = () => Infinity, groups, abortFetch = false } = {}) {
  const key = usageKey(tok, conv)
  const end = Date.now() + STEP_WAIT.until
  const wait = (ms) => new Promise((r) => {
    const t = setTimeout(r, ms)
    signal?.addEventListener("abort", () => (clearTimeout(t), r()), { once: true })
  })
  for (const [k, at] of claimed) if (Date.now() - at > 2 * STEP_WAIT.owedFor) claimed.delete(k)
  const num = (v) => (Number.isFinite(Number(v)) ? Number(v) : 0)
  let usage = null
  let got = 0
  const events = []
  const counts = groups?.map(() => 0)
  if (poll) await wait(first)
  while (!signal?.aborted) {
    try {
      const res = await fetch(API + "/aiserver.v1.DashboardService/GetFilteredUsageEvents", {
        method: "POST",
        headers: await headersFor(tok),
        body: JSON.stringify({ ...await teamBody(tok), startDate: String(since - 60_000), endDate: String(Date.now() + 60_000), page: 1, pageSize: 50 }),
        signal: abortFetch && signal ? AbortSignal.any([signal, AbortSignal.timeout(STEP_WAIT.ask)]) : AbortSignal.timeout(STEP_WAIT.ask),
      })
      if (res.status >= 400 && res.status < 500 && res.status !== 429) {
        // not this account's to read
        unreadable.set(tokKey(tok), Date.now())
        return { usage, events, got, counts, refused: true }
      }
      const evs = res.ok ? ((await res.json())?.usageEventsDisplay ?? []) : []
      const found = evs
        .filter((e) => e?.conversationId === conv && e.tokenUsage && num(e.timestamp) >= since - 1000 && !claimed.has(key + "\0" + e.timestamp))
        .sort((a, b) => num(a.timestamp) - num(b.timestamp))
      for (const ev of found) {
        if (got >= want) break
        const at = num(ev.timestamp)
        // found already excludes timestamps claimed by earlier fetches. Two
        // distinct events in this response may share a millisecond; count both.
        if (at >= before()) continue
        const group = groups?.findIndex((g, i) => counts[i] < g.n && at >= g.since - 1000 && at < (g.before ?? Infinity))
        if (groups && group === -1) continue
        if (groups) counts[group]++
        claimed.set(key + "\0" + ev.timestamp, Date.now())
        const t = ev.tokenUsage
        const u = { input: num(t.inputTokens), output: num(t.outputTokens), cacheRead: num(t.cacheReadTokens), cacheWrite: num(t.cacheWriteTokens), reasoning: 0 }
        usage = addUsage(usage ?? ZERO(), u)
        events.push({ at: num(ev.timestamp), usage: u })
        got++
      }
      if (got >= want) break
    } catch {}
    if (!poll || Date.now() + STEP_WAIT.every > end) break
    await wait(STEP_WAIT.every)
  }
  return { usage, events, got, counts, refused: false }
}

// stepUsage is the usage of the Run of conversation conv started at since,
// as the dashboard's usage events count it; null when none shows in time.
async function stepUsage(tok, conv, since, signal) {
  return (await usageEvents(tok, conv, since, 1, { poll: true, signal })).usage
}

// collect takes the account's debt for conversation conv and asks for its
// events (polling when poll): what was carried and what was found (usage),
// what was carried alone (carry) and the events found (events), and whether any is
// still owed (short), which is owed on. What comes back once the Run that
// asked is over (over() true) is carried to the next instead.
function collect(tok, conv, { poll = false, first, signal, over = () => false, abortFetch = false } = {}) {
  const key = usageKey(tok, conv)
  const d = owed.get(key)
  if (!d) return Promise.resolve({ usage: null, carry: null, events: [], short: false })
  owed.delete(key)
  const p = (async () => {
    const groups = (d.groups ?? [d]).filter((g) => (g.before === undefined && !d.groups) || Date.now() - g.since <= STEP_WAIT.owedFor)
    const want = groups.reduce((sum, g) => sum + g.n, 0)
    let r = { usage: null, events: [], got: 0 }
    if (want > 0 && canRead(tok)) r = await usageEvents(tok, conv, Math.min(...groups.map((g) => g.since)), want, {
      poll, first, signal, abortFetch, before: () => d.before ?? Infinity, ...(d.groups ? { groups } : {}),
    }).catch(() => r)
    let short = false
    for (const [i, g] of groups.entries()) {
      const remaining = g.n - (d.groups ? r.counts?.[i] ?? 0 : r.got)
      if (remaining > 0 && canRead(tok)) {
        short = true
        owe(key, { since: g.since, n: remaining, ready: g.ready, before: g.before })
      }
    }
    const carry = d.carry && { ...d.carry }
    const usage = addUsage(d.carry, r.usage)
    if (!over()) return { usage, carry, events: r.events ?? [], short }
    owe(key, { carry: usage })
    return { usage: null, carry: null, events: [], short }
  })()
  // Native admission may happen while an older collector is still in flight.
  // Its dynamic event bound must apply before that response is claimed too.
  p.debt = d
  const set = flights.get(key) ?? new Set()
  flights.set(key, set.add(p))
  p.finally(() => {
    set.delete(p)
    if (!set.size && flights.get(key) === set) flights.delete(key)
  })
  return p
}

// ledger is how a Run of conversation conv, started at since, has its
// usage counted, for decode; kept when the conversation_id names the
// session, so that a later Run of it can collect what this one owes. A
// debt the conversation has is collected in the background. own() is a
// step's count once it has called tools: its Run owed, what has shown of
// the debt by the end of the wait (or at once, when the account's never
// show in time); false when nothing has, null when the step is to be
// guessed (not kept, and its event not found, or an account that can't
// read its events). owed(final) is what the Run collected otherwise,
// all that can be had when final. done() ends the Run: what was collected
// and not given out goes back.
//
// What a Run collects is told apart by its event's time: Cursor stamps a
// Run's event as the Run begins (1.4–3.4 s after since), so one from since
// on (less the second usageEvents allows) is this Run's own (mine), and one
// before it, or a count carried, an earlier Run's (theirs). own() gives out only mine, as the step's usage,
// which a client reads as how full its context is; theirs goes out by
// owed(), apart. A Run of its conversation begins only once the one
// before has ended, so the two never meet; were the time off, the step's
// count would be told in the wrong field, never twice nor lost.
function ledger(tok, conv, since, signal, kept, { before } = {}) {
  const key = tokKey(tok)
  const debtKey = usageKey(tok, conv)
  if (kept && before !== undefined) {
    const debts = [owed.get(debtKey), ...[...(flights.get(debtKey) ?? [])].map((p) => p.debt)]
    for (const d of debts) for (const g of d?.groups ?? (d ? [d] : [])) g.before = Math.min(g.before ?? Infinity, before)
  }
  let mine = null // this Run's own, collected, not given out yet
  let theirs = null // earlier Runs', collected, not given out yet
  let over = false
  const gather = (opts) =>
    collect(tok, conv, { ...opts, over: () => over, abortFetch: before !== undefined }).then((r) => {
      theirs = addUsage(theirs, r.carry)
      for (const e of r.events ?? []) {
        if (e.at >= since - 1000) mine = addUsage(mine, e.usage)
        else theirs = addUsage(theirs, e.usage)
      }
      return r
    })
  let collecting = null
  const debt = kept ? owed.get(debtKey) : undefined
  const timer = debt ? setTimeout(() => (collecting = gather()), Math.max(0, debt.ready - Date.now())) : null
  timer?.unref?.()
  const takeMine = () => {
    const u = mine
    mine = null
    return u
  }
  const take = () => {
    const u = addUsage(theirs, mine)
    mine = theirs = null
    return u
  }
  return {
    async own() {
      if (!canRead(tok)) return null
      const m = misses.get(key) ?? 0
      const waits = m < STEP_WAIT.skipAfter || m % STEP_WAIT.retryEvery === 0
      if (!kept) {
        const r = waits ? await usageEvents(tok, conv, since, 1, { poll: true, signal }).catch(() => ({})) : {}
        if (r.usage) misses.delete(key)
        else if (!r.refused) misses.set(key, m + 1)
        return r.usage ?? null
      }
      owe(debtKey, { since, n: 1, ready: Date.now() + STEP_WAIT.owedAt })
      if (waits) {
        const r = await gather({ poll: true, signal })
        if (!canRead(tok)) return null
        if (r.short) misses.set(key, m + 1)
        else misses.delete(key)
      } else misses.set(key, m + 1)
      return takeMine() ?? false
    },
    async owed(final) {
      if (!kept) return null
      if (final) {
        await collecting
        await Promise.all([...(flights.get(debtKey) ?? [])])
        if (owed.has(debtKey)) await gather({ poll: owed.get(debtKey).n > 0, first: 0, signal })
      }
      return take()
    },
    done() {
      over = true
      clearTimeout(timer)
      const u = take()
      if (u) owe(debtKey, { carry: u })
    },
  }
}

// buildRun is the Run's first message, an AgentClientMessage with its
// run_request, and the blobs it names. The conversation state is the
// messages and one turn, which the server wants there to sample at all.
// conv is the conversation_id (conversationID), a new one when "". The
// model goes as the CLI sends it: in Max Mode when maxMode is set (Cursor
// refuses a model it serves only that way otherwise: "Max Mode
// Required"), with the picker variant's parameters when it has them.
function buildRun(msgs, lastUser, tools, model, conv = "", { maxMode = false, params = [] } = {}) {
  const blobs = new Map()
  const put = (b) => {
    const id = createHash("sha256").update(b).digest()
    blobs.set(id.toString("hex"), b)
    return id
  }
  const state = pb()
  for (const m of msgs) state.bytes(1, put(m))
  const mid = randomUUID()
  const user = pb().str(1, lastUser).str(2, mid).varint(4, 1).done() // mode: agent
  const turn = pb().bytes(1, pb().bytes(1, put(user)).str(10, mid)).done()
  state.bytes(8, put(turn)).varint(10, 1).str(22, "cli")
  const defs = tools.map(toolDef)
  const rc = pb().bytes(4, env())
  const mcp = pb()
  for (const d of defs) {
    rc.bytes(7, d)
    mcp.bytes(1, d)
  }
  const action = pb().bytes(2, pb().bytes(2, rc)) // resume_action
  // ModelDetails: model_id, display_model_id, display_name, max_mode (7);
  // RequestedModel: model_id, max_mode (2), parameters (3)
  const details = pb().str(1, model).str(3, model).str(4, model)
  const requested = pb().str(1, model)
  if (maxMode) {
    details.varint(7, 1)
    requested.varint(2, 1)
  }
  for (const p of params) requested.bytes(3, pb().str(1, p.id).str(2, p.value))
  const rr = pb()
    .bytes(1, state)
    .bytes(2, action)
    .bytes(3, details)
    .bytes(4, mcp)
    .str(5, conv || randomUUID())
    .bytes(9, requested)
    .varint(19, 1) // inline images
  return { first: pb().bytes(1, rr).done(), blobs }
}

// ---- the agent API ---------------------------------------------------------------------

// endpoints are the agent API picked for each token (by its hash). As
// cursor-agent does, it is what ServerConfigService/GetServerConfig names —
// a team may be served in one region only, and the global API turns it
// away — asked once a token, and again when fresh is set; the global one
// while it can't be had.
const endpoints = new Map()
async function agentURL(tok, fresh) {
  const key = createHash("sha256").update(tok).digest("hex")
  const had = endpoints.get(key)
  if (!fresh && had && (had.listed || Date.now() - had.at < 60_000)) return had.url
  let url = AGENT
  let listed = false
  try {
    const cfg = await unary(API, "aiserver.v1.ServerConfigService/GetServerConfig", tok, {}, 10_000)
    for (const raw of [cfg?.agentUrlConfig?.agentUrl, cfg?.agentUrlConfig?.agentnUrl]) {
      try {
        const u = new URL(raw)
        if ((u.protocol === "https:" || u.protocol === "http:") && u.host) {
          url = raw.replace(/\/+$/, "")
          listed = true
          break
        }
      } catch {}
    }
  } catch {}
  endpoints.set(key, { url, at: Date.now(), listed })
  return url
}

// open starts a Run at base: the session, the stream, and the response's
// status, or an error the request never got past. A Run HTTP/2 can't open
// goes over HTTP/1.1 (openH1). When the failure says HTTP/2 itself can't be
// had here (h2Blocked), the Runs after it go straight to HTTP/1.1 for
// H2.retryAfter, or until a region error sends one back (answerOf); a
// connection that failed (refused, no network) or a stream the server
// refused leaves the next Run to try HTTP/2 again.
let h1Until = 0
const cancelledOpen = () => ({ error: { status: 410, message: "the request was cancelled" }, h2Failed: false })
async function open(base, headers, signal) {
  if (signal?.aborted) return cancelledOpen()
  if (Date.now() >= h1Until) {
    const o = await openH2(base, headers, signal)
    // The head can arrive just before abort, before this continuation owns it.
    if (signal?.aborted) {
      o.req?.close(http2.constants.NGHTTP2_CANCEL)
      o.session?.destroy()
      return cancelledOpen()
    }
    if (!o.h2Failed) return o
    if (o.blocked) h1Until = Date.now() + H2.retryAfter
  }
  if (signal?.aborted) return cancelledOpen()
  return openH1(headers, signal)
}

const H2 = {
  wait: 15_000, // ms an HTTP/2 Run waits for its response's head
  retryAfter: 10 * 60_000, // ms the Runs after one found HTTP/2 blocked stay on HTTP/1.1
}

// NET are the failures of a connection itself, which say nothing of HTTP/2
const NET = ["ECONNREFUSED", "ENOTFOUND", "EAI_AGAIN", "ENETUNREACH", "EHOSTUNREACH", "ECONNRESET", "ETIMEDOUT", "ECONNABORTED", "EPIPE"]

// h2Blocked is whether a failure to open an HTTP/2 Run says HTTP/2 itself
// can't be had: no head in H2.wait, the "h2 is not supported" magpie's
// host gives a tunnel that can't carry it, a server or proxy that won't
// negotiate it (ALPN), an HTTP/2 protocol error. Anything else, a failed
// connection above all, is not.
function h2Blocked(e) {
  if (e?.h2Timeout) return true
  const code = String(e?.code ?? "")
  const text = `${code} ${e?.message ?? ""}`
  if (NET.some((c) => text.includes(c))) return false
  // a stream the server refused or reset: HTTP/2 itself got through
  if (code === "ERR_HTTP2_STREAM_ERROR") return false
  return code.startsWith("ERR_HTTP2") || /h2 is not supported|alpn|nghttp2|protocol error/i.test(text)
}

// openH2 is open over HTTP/2. A Run that fails before its response, other
// than by being cancelled, says h2Failed, and blocked when h2Blocked says
// so of the failure.
function openH2(base, headers, signal) {
  if (signal?.aborted) return Promise.resolve(cancelledOpen())
  return new Promise((resolve) => {
    const u = new URL(base)
    const session = http2.connect(u.origin)
    let done = false
    const fail = (e, h2Failed = true) => {
      if (done) return
      done = true
      clearTimeout(timer)
      signal?.removeEventListener("abort", abort)
      session.destroy()
      resolve({ error: { status: h2Failed ? 502 : 410, message: e?.message ?? String(e) }, h2Failed, blocked: h2Failed && h2Blocked(e) })
    }
    const abort = () => fail(new Error("the request was cancelled"), false)
    const timer = setTimeout(() => fail(Object.assign(new Error(`no answer over HTTP/2 in ${H2.wait / 1000}s`), { h2Timeout: true })), H2.wait)
    session.on("error", fail)
    const req = session.request({
      ":method": "POST",
      ":path": u.pathname.replace(/\/+$/, "") + "/agent.v1.AgentService/Run",
      ...headers,
    })
    req.on("error", fail)
    // a stream reset before its head (RST_STREAM, REFUSED_STREAM) may close
    // it with no error said
    req.once("close", () => fail(Object.assign(new Error(`the HTTP/2 stream closed before its response (code ${req.rstCode ?? "?"})`), { code: "ERR_HTTP2_STREAM_ERROR" })))
    req.once("response", (h) => {
      if (done) return
      done = true
      clearTimeout(timer)
      signal?.removeEventListener("abort", abort)
      resolve({ session, req, status: Number(h[":status"]) })
    })
    signal?.addEventListener("abort", abort, { once: true })
    if (signal?.aborted) abort()
  })
}

const H1 = { appendWait: 60_000 } // ms a BidiAppend may take before the Run fails, as cursor-agent's appendTimeoutMs

// connectCode is Connect's code for an HTTP status that came with no
// Connect error of its own
function connectCode(status) {
  return { 400: "invalid_argument", 401: "unauthenticated", 403: "permission_denied", 429: "resource_exhausted" }[status] ?? "unavailable"
}

// openH1 is open over HTTP/1.1, as Cursor's clients run a Run without
// HTTP/2: what runOnce writes (a Connect frame each time) goes up as
// BidiAppend calls in turn, numbered from 0, and what RunSSE sends down is
// the stream it reads, Connect's frames as the HTTP/2 Run's. Its response
// is a 200 at once; an error Cursor says later, or a call that fails,
// ends the stream with Connect's end frame, which runOnce reads as the
// Run's own.
function openH1(headers, signal) {
  if (signal?.aborted) return Promise.resolve(cancelledOpen())
  const ac = new AbortController()
  const id = headers["x-request-id"]
  const h = { ...headers, "accept-encoding": "identity", "x-cursor-streaming": "true" }
  let ended = false
  let seq = 0
  let reader
  const req = new Duplex({
    read() {},
    write(chunk, _enc, cb) {
      // runOnce writes one whole frame each time
      const msg = Buffer.from(chunk).subarray(5)
      const n = seq++
      // Keep the write buffered until its append finishes, so callbacks and
      // drain reflect the pending upload rather than an unbounded promise chain.
      append(msg, n).then(() => cb(), (e) => cb(e))
    },
  })
  const cleanup = () => {
    signal?.removeEventListener("abort", close)
    ac.abort()
    reader?.cancel().catch(() => {})
  }
  const end = (body) => {
    if (ended || req.destroyed) return
    ended = true
    const f = frame(Buffer.from(body))
    f[0] = 2
    req.push(f)
    req.push(null)
    cleanup()
  }
  const failed = async (res) => {
    const text = await res.text().catch(() => "")
    let e
    try {
      e = JSON.parse(text)
    } catch {}
    end(JSON.stringify({ error: typeof e?.code === "string" ? e : { code: connectCode(res.status), message: text.trim() || `HTTP ${res.status}` } }))
  }
  // append sends one message up. One that gets no answer by its deadline
  // (more time for a large one) ends the Run, as cursor-agent's
  // bidi_append_deadline_exceeded does: the messages after it wait on it.
  const append = async (msg, n) => {
    if (ended || ac.signal.aborted) return
    const body = pb().str(1, msg.toString("hex")).bytes(2, pb().str(1, id).done()).varint(3, n).done()
    const ms = H1.appendWait + Math.ceil(body.length / 65536) * 1000
    const deadline = AbortSignal.timeout(ms)
    try {
      const res = await fetch(API + "/aiserver.v1.BidiService/BidiAppend", {
        method: "POST",
        headers: { ...h, "content-type": "application/proto", "connect-protocol-version": "1" },
        body,
        signal: AbortSignal.any([ac.signal, deadline]),
      })
      if (ended || ac.signal.aborted) { await res.body?.cancel().catch(() => {}); return }
      if (!res.ok) return failed(res)
      await res.arrayBuffer()
    } catch (e) {
      if (ac.signal.aborted) return
      if (deadline.aborted) return end(JSON.stringify({ error: { code: "deadline_exceeded", message: `BidiAppend got no answer in ${Math.round(ms / 1000)}s` } }))
      end(JSON.stringify({ error: { code: "unavailable", message: `BidiAppend: ${e?.message ?? e}` } }))
    }
  }
  const close = () => {
    ended = true
    cleanup()
    req.destroy()
  }
  signal?.addEventListener("abort", close, { once: true })
  if (signal?.aborted) {
    close()
    return Promise.resolve(cancelledOpen())
  }
  ;(async () => {
    try {
      const res = await fetch(API + "/agent.v1.AgentService/RunSSE", {
        method: "POST",
        headers: { ...h, "content-type": "application/connect+proto", "connect-protocol-version": "1" },
        body: frame(pb().str(1, id).done()),
        signal: ac.signal,
      })
      if (ended || ac.signal.aborted) { await res.body?.cancel().catch(() => {}); return }
      if (!res.ok) return failed(res)
      reader = res.body.getReader()
      try {
        while (!ended && !req.destroyed) {
          const { value, done } = await reader.read()
          if (done) break
          if (!ended && !req.destroyed) req.push(Buffer.from(value))
        }
      } finally {
        reader.releaseLock()
        reader = undefined
      }
      if (!ended && !req.destroyed) {
        ended = true
        req.push(null)
        cleanup()
      }
    } catch (e) {
      if (!ac.signal.aborted) end(JSON.stringify({ error: { code: "unavailable", message: `RunSSE: ${e?.message ?? e}` } }))
    }
  })()
  return Promise.resolve(signal?.aborted ? cancelledOpen() : { session: { close, destroy: close }, req: Object.assign(req, { close }), status: 200, h1: true })
}

// Every outbound frame, including KV replies and heartbeats, uses the same
// bounded FIFO. Admission is synchronous; a result is reserved only once even
// when a later write fails. Count bytes until both callback and drain complete.
function nativeTransport(req, close, fail, limits = NATIVE_LIMITS) {
  const outbound = { bytes: 0, messages: 0 }
  const queue = []
  let closed = false, flushing = false, rejectWrite
  // waiters for room (KV replies), woken as writes complete. An eighth of
  // each bound stays free for heartbeats and exec answers, which never wait.
  const waiters = new Set()
  const spare = (n) => Math.floor(n / 8)
  const fits = (size) => outbound.messages === 0 || (outbound.messages + 1 + spare(limits.outboundMessages) <= limits.outboundMessages
    && outbound.bytes + size + spare(limits.outboundBytes) <= limits.outboundBytes)
  const wake = () => {
    for (const w of waiters) if (closed || fits(w.size)) w.done()
  }
  const flush = async () => {
    if (closed || flushing) return
    flushing = true
    try {
      while (!closed && queue.length) {
        const packet = queue.shift()
        await new Promise((resolve, reject) => {
          let written = false, drained = false, started = false, settled = false
          const finish = (error) => {
            if (settled) return
            settled = true
            rejectWrite = undefined
            req.removeListener("drain", drain)
            req.removeListener("error", failed)
            req.removeListener("close", lost)
            error ? reject(error) : resolve()
          }
          const check = () => { if (started && written && drained) finish() }
          const drain = () => { drained = true; check() }
          const failed = (e) => finish(e)
          const lost = () => finish(new NativeSessionError(502, "native transport closed during a write; its outcome is uncertain"))
          rejectWrite = finish
          req.once("drain", drain)
          req.once("error", failed)
          req.once("close", lost)
          try {
            drained = req.write(packet, (e) => {
              if (e) return finish(e)
              written = true
              check()
            }) || drained
            started = true
            if (drained) req.removeListener("drain", drain)
            check()
          } catch (e) { finish(e) }
        })
        if (!closed) { outbound.bytes -= packet.length; outbound.messages--; wake() }
      }
    } catch (e) {
      if (!closed) fail(e)
    } finally { flushing = false }
  }
  return {
    outbound,
    send(message) {
      if (closed || req.destroyed || !req.writable) throw new NativeSessionError(502, "native transport is no longer writable")
      const size = message.length + 5
      if (outbound.bytes + size > limits.outboundBytes) throw new NativeSessionError(503, "native outbound bytes limit reached")
      if (outbound.messages + 1 > limits.outboundMessages) throw new NativeSessionError(503, "native outbound messages limit reached")
      outbound.bytes += size
      outbound.messages++
      queue.push(frame(message))
      void flush()
    },
    // room resolves once a message of size fits the queue with the spare
    // kept, and fails the Run when the queue doesn't drain for writeWait.
    room(size) {
      if (closed || fits(size)) return Promise.resolve()
      return new Promise((resolve, reject) => {
        const w = {
          size,
          done() {
            waiters.delete(w)
            clearTimeout(w.timer)
            resolve()
          },
        }
        w.timer = setTimeout(() => {
          waiters.delete(w)
          const which = outbound.bytes + size + spare(limits.outboundBytes) > limits.outboundBytes ? "bytes" : "messages"
          reject(new NativeSessionError(503, `native outbound ${which} limit reached: the queue did not drain for ${Math.round(limits.writeWait / 1000)}s`))
        }, limits.writeWait)
        w.timer.unref?.()
        waiters.add(w)
      })
    },
    close() {
      if (closed) return
      closed = true
      queue.length = 0
      outbound.bytes = 0
      outbound.messages = 0
      wake()
      rejectWrite?.(new NativeSessionError(410, "native transport was cancelled"))
      close()
    },
  }
}

// runNative opens a session-owned Run. Its background decoder survives the end
// of each Chat segment; only terminal completion, failure, or cancellation
// releases the transport.
async function runNative({ tok, base, id, tools, conv, convID, signal, maxMode, params, native }) {
  const cid = convID || randomUUID()
  const since = Date.now()
  // Transport shutdown precedes final debt collection. Keep its cancellation
  // separate, and return any undelivered collection through the ledger's carry.
  const accounting = new AbortController()
  const book = ledger(tok, cid, since, accounting.signal, !!convID, { before: since })
  const debtKey = usageKey(tok, cid)
  let started = false, settled = false, deferred = null
  native.accounting = {
    started() { started = true },
    async settle() {
      const more = await book.owed(true)
      if (settled) {
        if (more) owe(debtKey, { carry: more })
        return null
      }
      deferred = more
      return more
    },
    delivered() {
      if (settled) return
      settled = true
      deferred = null
      book.done()
      accounting.abort()
    },
    abandon() {
      if (settled) return
      settled = true
      book.done()
      accounting.abort()
      if (deferred) owe(debtKey, { carry: deferred })
      deferred = null
      // Every handoff of this owner belongs to one Run. Only an abandoned
      // started/pending Run is owed; a final snapshot (including zero) is not.
      if (started && native.segments > 0 && !native.finalUsage && convID && canRead(tok)) {
        owe(debtKey, { since, n: 1, ready: Date.now() + STEP_WAIT.owedAt })
      }
    },
  }
  const { first, blobs } = buildRun(conv.msgs, conv.last, tools, id, cid, { maxMode, params })
  native.chatTools = tools
  native.attach(signal)
  if (native.error) {
    nativeSessions.forget(native)
    return { error: native.error }
  }
  const o = await open(
    base,
    {
      "content-type": "application/connect+proto",
      "connect-protocol-version": "1",
      authorization: `Bearer ${tok}`,
      "x-cursor-client-version": await clientVersion(),
      "x-cursor-client-type": "cli", // else Cursor adds a prompt of its own
      "x-ghost-mode": "true", // privacy mode: nothing kept for training
      "x-request-id": randomUUID(),
      ...await teamHeader(tok),
      // none of Cursor's own tools, only the caller's
      "x-cursor-agent-allowed-tools": "mcp_tool_call,get_mcp_tools_tool_call",
    },
    native.ac.signal,
  )
  if (o.error) {
    native.fail(o.error)
    nativeSessions.forget(native)
    return o
  }
  const { session, req } = o
  const close = () => {
    try { req.close(http2.constants.NGHTTP2_CANCEL) } catch {}
    session.close()
    session.destroy()
  }
  const transport = nativeTransport(req, close, (e) => native.fail({ status: e.status ?? 502, message: e.message }), native.limits)
  try {
    native.bind(transport, blobs, conv.estimate)
    if (native.error) {
      nativeSessions.forget(native)
      return { error: native.error }
    }
    native.send(first)
    native.accounting.started()
    if (o.status < 200 || o.status > 299) {
      const chunks = []
      for await (const c of req) chunks.push(c)
      native.fail(failure(o.status, Buffer.concat(chunks).toString("utf8").slice(0, 1 << 20)))
      nativeSessions.forget(native)
      return { error: native.error, h1: !!o.h1 }
    }
    native.pumpTask = (async () => {
      await native.pump(frames(req))
    })()
    const response = native.response()
    const it = response.events
    const head = await it.next()
    if (head.done || head.value.error) {
      const error = head.value?.error ?? { status: 502, message: "an empty reply" }
      await native.cancel(error.message)
      nativeSessions.forget(native)
      return { error, h1: !!o.h1 }
    }
    let firstEvent = head
    response.events = {
      next() {
        if (firstEvent) {
          const r = firstEvent
          firstEvent = null
          return Promise.resolve(r)
        }
        return it.next()
      },
      return: () => it.return(),
      [Symbol.asyncIterator]() { return this },
    }
    return response
  } catch (e) {
    native.fail({ status: e.status ?? 502, message: e.message })
    await native.pumpTask
    nativeSessions.forget(native)
    return { error: native.error, h1: !!o.h1 }
  }
}

// runOnce is one Run of the chat on the agent API at base: an async
// iterator of the answer's pieces, or the error it failed with before any.
async function runOnce({ tok, base, id, tools, conv, convID, signal, maxMode, params }) {
  // the Run's conversation_id, a new one when nothing names the session:
  // its usage event is found by it
  const cid = convID || randomUUID()
  const since = Date.now()
  // how its usage is counted: a debt of the conversation's is collected
  // while it goes, which only a conversation_id that names the session
  // could have run up
  const book = ledger(tok, cid, since, signal, !!convID)
  const { first, blobs } = buildRun(conv.msgs, conv.last, tools, id, cid, { maxMode, params })
  const o = await open(
    base,
    {
      "content-type": "application/connect+proto",
      "connect-protocol-version": "1",
      authorization: `Bearer ${tok}`,
      "x-cursor-client-version": await clientVersion(),
      "x-cursor-client-type": "cli", // else Cursor adds a prompt of its own
      "x-ghost-mode": "true", // privacy mode: nothing kept for training
      "x-request-id": randomUUID(),
      ...await teamHeader(tok),
      // none of Cursor's own tools, only the caller's
      "x-cursor-agent-allowed-tools": "mcp_tool_call,get_mcp_tools_tool_call",
    },
    signal,
  )
  if (o.error) return book.done(), o
  const { session, req } = o
  let closed = false
  let heartbeat
  const send = (msg) => {
    if (!closed && !req.destroyed && req.writable) req.write(frame(msg))
  }
  const close = () => {
    if (closed) return
    closed = true
    clearInterval(heartbeat)
    signal?.removeEventListener("abort", close)
    try {
      req.close(http2.constants.NGHTTP2_CANCEL)
    } catch {}
    session.close()
    setTimeout(() => session.destroy(), 1000).unref?.()
  }
  signal?.addEventListener("abort", close, { once: true })
  // Abort can win after open's head check but before this Run takes ownership.
  if (signal?.aborted) {
    close()
    book.done()
    return cancelledOpen()
  }
  heartbeat = setInterval(() => send(pb().bytes(7, EMPTY).done()), 5000) // the client is still there, as the CLI says
  send(first)
  if (o.status < 200 || o.status > 299) {
    const chunks = []
    try {
      for await (const c of req) chunks.push(c)
    } catch {}
    close()
    book.done()
    return { error: failure(o.status, Buffer.concat(chunks).toString("utf8").slice(0, 1 << 20)) }
  }
  // a Run Cursor stops answering ends, as a retained one does (#2672)
  const it = decode(new Quiet(NATIVE_LIMITS, close).frames(frames(req)), {
    send,
    blobs,
    tools,
    estimate: conv.estimate,
    close,
    stepUsage: book.own,
    owedUsage: book.owed,
  })
  const wrapped = (async function* () {
    try {
      yield* it
    } finally {
      close()
      book.done()
    }
  })()
  // an error comes before anything of the answer: out of quota, a model the
  // plan doesn't have — answered with its own status
  const head = await wrapped.next()
  if (head.done) return book.done(), { error: { status: 502, message: "an empty reply" } }
  if (head.value.error) {
    await wrapped.return()
    return { error: head.value.error, h1: !!o.h1 }
  }
  return {
    events: (async function* () {
      yield head.value
      yield* wrapped
    })(),
  }
}

// decode follows the server's messages into the answer's pieces until the
// turn ends, or the model has made its tool calls.
async function* decode(stream, { send, blobs, tools, estimate, close, stepUsage, owedUsage }) {
  let calls = 0
  // answered: the step's calls answered here (a refusal, the MCP tool
  // list), which Cursor's count of the step's calls takes in too (#142)
  let answered = 0
  // ended: Cursor said what the turn used
  let ended = false
  let listed = 0
  // stepDone is whether every call the step listed has come. A step whose
  // calls were all answered here goes on on Cursor's side, under a count of
  // its own.
  const stepDone = () => {
    if (!listed || calls + answered < listed) return false
    if (calls > 0) return true
    listed = 0
    answered = 0
    return false
  }
  let said = 0
  // wrote: the reply's text alone; a turn of thinking only says nothing
  let wrote = 0
  // what the Run sent, for an empty reply to name (#60): the thinking's
  // length, Cursor's own tools it asked for and was refused, and updates
  // and messages this doesn't read
  const seen = { thinking: 0, refused: new Map(), updates: new Map(), messages: new Map() }
  const count = (m, k) => m.set(k, (m.get(k) ?? 0) + 1)
  const empty = (how) => ({ error: { status: 502, message: "an empty reply" + emptyWhy(how, seen, ended ? usage : null) } })
  let usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 }
  // counted: the step's usage is the dashboard's events, its own counted
  // with it or by a later step (ledger), never guessed
  let counted = false
  // finish is the step's end, with what earlier steps owed (more), which
  // is no reason not to guess the step's own. The step's usage is its own
  // Run's alone: a client reads its prompt_tokens as how full the context
  // is (pi, OpenCode), and an earlier step's prompt added to it read twice
  // the context. What earlier steps owed goes apart, as magpie_deferred,
  // which magpie counts with the step and no client reads.
  const finish = (more) => {
    if (!counted && !usage.output) usage.output = Math.floor((said + 3) / 4)
    // a guess only when Cursor counted nothing: a prompt read whole from
    // the cache leaves no uncached rest, and is no reason to guess; nor is
    // a prompt counted from the events
    if (!counted && !usage.input && !usage.cacheRead && !usage.cacheWrite) usage.input = estimate
    const prompt = usage.input + usage.cacheRead + usage.cacheWrite
    const deferred = more && more.input + more.output + more.cacheRead + more.cacheWrite > 0
    return {
      stop: calls > 0 ? "tool_calls" : "stop",
      usage: {
        prompt_tokens: prompt,
        completion_tokens: usage.output,
        total_tokens: prompt + usage.output,
        prompt_tokens_details: { cached_tokens: usage.cacheRead, cache_write_tokens: usage.cacheWrite },
        ...(usage.reasoning ? { completion_tokens_details: { reasoning_tokens: usage.reasoning } } : {}),
        ...(deferred
          ? {
              magpie_deferred: {
                prompt_tokens: more.input + more.cacheRead + more.cacheWrite,
                completion_tokens: more.output,
                cached_tokens: more.cacheRead,
                cache_write_tokens: more.cacheWrite,
              },
            }
          : {}),
      },
    }
  }
  // settled is finish once a step that called tools has its usage: the
  // dashboard's count, the Run closed first, when the turn didn't end (or
  // owed); and with what the conversation's earlier steps owed, which a
  // step that ends the turn waits for
  const settled = async () => {
    if (calls > 0 && !ended && stepUsage) {
      close?.()
      const u = await stepUsage().catch(() => null)
      if (u) usage = u
      counted = u === false || !!u
    }
    return finish(owedUsage ? await owedUsage(calls === 0).catch(() => null) : null)
  }
  const closeExec = (id) => send(pb().bytes(5, pb().bytes(1, pb().varint(1, id))).done())
  try {
    for await (const f of stream) {
      if (f.end) {
        const e = failure(200, f.data)
        if (e.status < 200 || e.status > 299) yield { error: e }
        else if (wrote === 0 && calls === 0) yield empty(endFrame(f.data))
        else yield await settled()
        return
      }
      for (const m of fields(f.data)) {
        switch (m.num) {
          case 1: // interaction_update
            for (const u of fields(m.data)) {
              const uf = fields(u.data)
              switch (u.num) {
                case 1: {
                  // text
                  const t = pbStr(uf, 1)
                  if (t) {
                    said += t.length
                    wrote += t.length
                    yield { text: t }
                  }
                  break
                }
                case 4: {
                  // thinking
                  const t = pbStr(uf, 1)
                  if (t) {
                    said += t.length
                    seen.thinking += t.length
                    yield { reasoning: t }
                  }
                  break
                }
                case 14: // turn ended, with what it used
                  usage = usageOf(uf)
                  ended = true
                  if (calls === 0) {
                    // nothing written and nothing called: an empty reply,
                    // which the client asks again, not a finished turn
                    yield wrote === 0 ? empty("Cursor ended the turn") : await settled()
                    return
                  }
                  break
                case 27: // how many tool calls the step makes, sent before or after them
                  listed = pbNum(uf, 1)
                  if (stepDone()) {
                    yield await settled()
                    return
                  }
                  break
                default:
                  count(seen.updates, u.num)
              }
            }
            break
          case 2: {
            // exec_server_message: the server asks the client to run something
            const call = exec(m.data, tools, send, closeExec, seen.refused)
            if (call) {
              said += call.args.length
              yield { tool: { index: calls++, ...call } }
            } else if (!fields(m.data).some((e) => e.num === 10)) answered++
            if (stepDone()) {
              // every call made: the caller runs them
              yield await settled()
              return
            }
            break
          }
          case 4: {
            // kv_server_message: a blob wanted, or one to keep
            const kv = fields(m.data)
            const id = pbNum(kv, 1)
            for (const k of kv) {
              if (k.num === 2) {
                const want = field(fields(k.data), 1)?.data.toString("hex") ?? ""
                const b = blobs.get(want)
                const res = b ? pb().bytes(1, b) : pb().bytes(2, pb().str(1, "blob not found"))
                send(pb().bytes(3, pb().varint(1, id).bytes(2, res)).done())
              } else if (k.num === 3) {
                // the server's own record of the turn, not needed
                send(pb().bytes(3, pb().varint(1, id).bytes(3, EMPTY)).done())
              }
            }
            break
          }
          default:
            count(seen.messages, m.num)
        }
      }
    }
    if (calls > 0) {
      yield await settled()
      return
    }
    yield { error: { status: 502, message: "the reply broke off: EOF" } }
  } catch (e) {
    // gone quiet is no answer, calls or not: the step's count never came
    if (e?.quiet) {
      yield { error: { status: e.status, message: e.message } }
      return
    }
    if (calls > 0 && /EOF/.test(e?.message ?? "")) {
      yield await settled()
      return
    }
    yield { error: { status: 502, message: "the reply broke off: " + (e?.message ?? e) } }
  }
}

// endFrame is how a Run's Connect end frame that said no error reads in
// an empty reply's words: its JSON, cut short, or that it was empty.
function endFrame(data) {
  const t = Buffer.from(data ?? []).toString("utf8").trim()
  if (!t || t === "{}") return "the Run's stream ended (an empty end frame)"
  return `the Run's stream ended (end frame: ${t.length > 300 ? t.slice(0, 300) + "…" : t})`
}

// emptyWhy is what an empty reply says of what came before it (#60): how
// it ended, the thinking, the turn's own count of output, Cursor's own
// tools refused, and what Cursor sent that isn't read. Field numbers are
// agent.v1's, so a report names the message that wasn't understood.
function emptyWhy(how, seen, usage) {
  const list = (m) => [...m].map(([k, n]) => (n > 1 ? `#${k} ×${n}` : `#${k}`)).join(", ")
  const parts = [how]
  parts.push(seen.thinking ? `after ${seen.thinking} chars of thinking, no text and no tool call` : "with no thinking, no text and no tool call")
  if (usage) parts.push(`Cursor counted ${usage.output} output tokens`)
  if (seen.refused.size) parts.push(`Cursor's own tools asked for and refused: ${list(seen.refused)}`)
  if (seen.updates.size) parts.push(`updates not read: ${list(seen.updates)}`)
  if (seen.messages.size) parts.push(`messages not read: ${list(seen.messages)}`)
  return ` (${parts.join("; ")}; try again)`
}

// exec answers what the server asks the client to run: a call of the
// caller's tools is returned, the list of them is given, and anything else
// is refused.
function exec(msg, tools, send, closeExec, refused, native = false) {
  const es = native ? nativeFields(msg) : fields(msg)
  // ExecServerMessage.id is a proto3 uint32: an omitted field means zero.
  // Explicit malformed fields must not acquire that valid default.
  if (native && es.some((e) => e.num === 1 && (e.wire !== 0 || !Number.isInteger(e.n) || e.n < 0 || e.n > 0xffffffff)))
    throw new NativeSessionError(502, "invalid native exec request ID")
  const id = pbNum(es, 1)
  const execID = pbStr(es, 15)
  // Validate every MCP envelope before any exec can publish a call or send a
  // response. Differing duplicates have no supported selection/merge contract.
  let nativeCall, nativeUnavailable = false
  if (native) for (const e of es) if (e.num === 11) {
    if (e.wire !== 2) throw new NativeSessionError(502, "invalid native MCP argument envelope")
    const { fields: a, args } = nativeMcpArgs(e.data)
    const name = pbStr(a, 5) || bareName(pbStr(a, 1))
    const provider = pbStr(a, 4)
    const server = pbStr(a, 9)
    if (provider && !NAMESPACES.includes(provider) || server && !NAMESPACES.includes(server)) nativeUnavailable = true
    if (pbNum(a, 7) || field(a, 6)) nativeUnavailable = true
    if (!tools.some((t) => t.name === name)) nativeUnavailable = true
    if (!pbStr(a, 5) && !NAMESPACES.some((ns) => pbStr(a, 1) === ns + "-" + name)) nativeUnavailable = true
    const call = { requestID: id, execID, nativeID: pbStr(a, 3), name, args }
    if (nativeCall && !isDeepStrictEqual(nativeCall, call)) throw new NativeSessionError(502, "ambiguous repeated native MCP argument envelopes")
    nativeCall ??= call
  }
  const answer = (num, res) => {
    send(pb().bytes(2, pb().varint(1, id).str(15, execID).bytes(num, res)).done())
    closeExec(id)
  }
  const unavailable = () => {
    const kind = es.find((e) => ![1, 15, 19, 55].includes(e.num))?.num ?? 0
    refused?.set(kind, (refused.get(kind) ?? 0) + 1)
    send(pb().bytes(5, pb().bytes(2, pb().varint(1, id).str(2, "not available"))).done())
    closeExec(id)
    return null
  }
  // A foreign/unregistered MCP or approval flow is unavailable to this bridge.
  // Refuse it like a native shell request, then let the model continue.
  if (nativeUnavailable) return unavailable()
  for (const e of es) {
    switch (e.num) {
      case 11: {
        // mcp_args: a call of the caller's tools
        if (native) return { ...nativeCall, args: JSON.stringify(nativeCall.args) }
        const a = fields(e.data)
        const args = {}
        for (const kv of a) {
          if (kv.num !== 2) continue
          const ef = fields(kv.data)
          const v = field(ef, 2)
          args[pbStr(ef, 1)] = v ? pbAny(v.data) : null
        }
        const name = pbStr(a, 5) || bareName(pbStr(a, 1))
        // an OpenAI model's id is its call's and its item's, a line apart,
        // which no caller would take as an id: the line goes as __
        const cid = pbStr(a, 3).replaceAll("\n", "__") || "call_" + randomBytes(12).toString("hex")
        return { id: cid, name, args: JSON.stringify(args) }
      }
      case 36: {
        // mcp_state_exec_args: the tools there are
        const srv = pb().str(1, NS).str(2, NS).str(7, "connected")
        for (const t of tools) srv.bytes(5, toolDef(t))
        answer(36, pb().bytes(1, pb().bytes(1, srv)))
        return null
      }
      case 10: // request_context_args
        answer(10, pb().bytes(1, pb().bytes(1, pb().bytes(4, env()))))
        return null
    }
  }
  // a shell, a file read or an edit of Cursor's own: never run
  return unavailable()
}

// ---- chat completions ------------------------------------------------------------------

// Every answer says X-Magpie-Sign-In: kept. The built-in marked no Cursor
// account lapsed, not for a 401 nor for any "expired" (a token, a session,
// a trial), and cleared none on a success or a renewed token.
const errorResponse = ({ status, message }) =>
  new Response(JSON.stringify({ error: { message, type: "cursor_error", code: status } }), {
    status,
    headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "kept" },
  })

function kept(res) {
  if (res.headers.has("X-Magpie-Sign-In")) return res
  const headers = new Headers(res.headers)
  headers.set("X-Magpie-Sign-In", "kept")
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}

// answer runs a chat completion on Cursor, the account kept (errorResponse).
const answer = async (auth, chat, signal, session) => kept(await answerOf(auth, chat, signal, session))

// maxOnly are the ids Cursor said it serves in Max Mode only.
const maxOnly = new Set()

// maxRequired is Cursor's refusal of a model asked for without Max Mode
// (the CLI's MAX_MODE_REQUIRED): "Max Mode Required: The model "x"
// requires Max Mode to be enabled. …"
const maxRequired = (msg) => /max[ _]mode[ _]required|requires max mode/i.test(String(msg ?? ""))

// answerOf runs a chat completion on Cursor and answers it as one, streamed
// or not.
async function answerOf(auth, chat, signal, session = "") {
  let tok
  try {
    tok = await tokenOf(auth)
  } catch (e) {
    return errorResponse({ status: e.status ?? 401, message: e.message })
  }
  if (signal?.aborted) return errorResponse({ status: 410, message: "the request was cancelled" })
  const tools = toolsOf(chat)
  const convID = conversationID(chat, session)
  const prepared = nativeSessions.prepare(tok, chat, convID, tools)
  let r
  // the continuation's own prompt, which its final step reports (#156)
  if (prepared.owner) r = prepared.owner.resume({ ...prepared, estimate: conversation(chat, tools).estimate }, signal)
  const model = String(chat.model ?? "")
  if (!r) {
    let raw = []
    try {
      raw = await usable(tok)
    } catch {}
    // fast only when asked: a service_tier of priority or fast is on; any
    // other tier, or none, is off, unless the id is a fast one of Cursor's own
    const tier = chat.service_tier
    const fast = tier === "priority" || tier === "fast" ? true : tier ? false : undefined
    const req = request(raw, model, chat.reasoning_effort ?? "", fast)
    const id = req.id
    const params = req.params
    let maxMode = req.maxMode || maxOnly.has(id)
    const conv = conversation(chat, tools)
    let base = await agentURL(tok, false)
    const once = async () => {
      // Retention is an optimization. Admission pressure or unavailable ownership
      // uses the original history builder/decoder without cancelling a live parent.
      const native = prepared.fallback ? null : nativeSessions.create(prepared, { id, maxMode, params }, { pb, exec, usageOf, failure })
      const args = { tok, base, id, tools, conv, convID, signal, maxMode, params }
      let response = native ? await runNative({ ...args, native }) : await runOnce(args)
      const rebuild = async () => {
        const fallback = await runOnce(args)
        fallback.diagnostic = "stateless; bootstrap=imported-history; reason=" + (response.error?.continuityReason || "missing-batch-count")
        return fallback
      }
      // A missing or conflicting native batch count may be retried once,
      // only before Chat output is exposed or any native result was submitted.
      // Never repeat an uncertain tool write or progressive stream output.
      if (response.error?.retentionIneligible && !signal?.aborted) response = await rebuild()
      else if (native) response.rebuild = rebuild
      response.diagnostic ??= "stateless; bootstrap=imported-history; reason=" + (prepared.fallback || "retention capacity or reservation unavailable")
      return response
    }
    r = await once()
    if (r.error && regional(r.error.message)) {
      // the team moved, or the config was kept from before: once more with
      // what the config says now. A Run that went over HTTP/1.1 went to api2,
      // not the region's agent host: once more over HTTP/2 there.
      const viaH1 = !!r.h1
      h1Until = 0
      const fresh = await agentURL(tok, true)
      if (fresh !== base || viaH1) {
        base = fresh
        r = await once()
      }
    }
    if (r.error && !maxMode && maxRequired(r.error.message)) {
      // a model Cursor serves only in Max Mode that neither list said so of:
      // in Max Mode, as the CLI turns it on for such a model, and so from
      // now on. An account Max Mode isn't open to is answered as Cursor
      // answers that.
      maxMode = true
      maxOnly.add(id)
      r = await once()
    }
  }
  if (r.error) return errorResponse(r.error)
  // A resumed owner may fail while submitting results. Surface that uncertain
  // write once; later history rebuilds never submit it again on that owner.
  const it = r.events
  let head = await it.next()
  if (head.value?.error) return errorResponse(head.value.error)
  let events = {
    next() {
      if (head) { const value = head; head = null; return Promise.resolve(value) }
      return it.next()
    },
    return: () => it.return?.(),
    [Symbol.asyncIterator]() { return this },
  }
  const responseHeaders = { "X-Magpie-Cursor-Continuity": r.diagnostic }
  const cid = "chatcmpl-" + randomBytes(12).toString("hex")
  const created = Math.floor(Date.now() / 1000)

  if (!chat.stream) {
    while (true) {
      let retry = false
      const msg = { role: "assistant", content: "" }
      let reasoning = ""
      const calls = []
      let stop = "stop"
      let usage
      for await (const e of events) {
        if (e.error) {
          if (!e.error.retentionIneligible || !r.rebuild || signal?.aborted) return errorResponse(e.error)
          const reason = e.error.continuityReason
          r = await r.rebuild()
          if (r.error) return errorResponse(r.error)
          if (reason) r.diagnostic = "stateless; bootstrap=imported-history; reason=" + reason
          events = r.events
          responseHeaders["X-Magpie-Cursor-Continuity"] = r.diagnostic
          retry = true
          break
        }
        if (e.text) msg.content += e.text
        if (e.reasoning) reasoning += e.reasoning
        if (e.tool) calls.push({ id: e.tool.id, type: "function", function: { name: e.tool.name, arguments: e.tool.args } })
        if (e.stop) [stop, usage] = [e.stop, e.usage]
      }
      if (retry) continue
      if (reasoning) msg.reasoning_content = reasoning
      if (calls.length) msg.tool_calls = calls
      r.delivered?.()
      return Response.json({ id: cid, object: "chat.completion", created, model, choices: [{ index: 0, message: msg, finish_reason: stop }], ...(usage ? { usage } : {}) }, { headers: responseHeaders })
    }
  }

  const chunk = (delta, finish_reason = null, extra = {}) =>
    enc.encode(`data: ${JSON.stringify({ id: cid, object: "chat.completion.chunk", created, model, choices: [{ index: 0, delta, finish_reason }], ...extra })}\n\n`)
  let began = false
  const stream = new ReadableStream({
    async pull(ctl) {
      const next = await events.next()
      if (!began) {
        began = true
        ctl.enqueue(chunk({ role: "assistant", content: "" }))
      }
      if (next.done) {
        r.delivered?.()
        ctl.enqueue(enc.encode("data: [DONE]\n\n"))
        return ctl.close()
      }
      const e = next.value
      if (e.text) ctl.enqueue(chunk({ content: e.text }))
      else if (e.reasoning) ctl.enqueue(chunk({ reasoning_content: e.reasoning }))
      else if (e.tool) ctl.enqueue(chunk({ tool_calls: [{ index: e.tool.index, id: e.tool.id, type: "function", function: { name: e.tool.name, arguments: e.tool.args } }] }))
      else if (e.stop) ctl.enqueue(chunk({}, e.stop, e.usage ? { usage: e.usage } : {}))
      else if (e.error) {
        ctl.enqueue(enc.encode(`data: ${JSON.stringify({ error: { message: e.error.message, code: e.error.status } })}\n\n`))
        ctl.close()
      }
    },
    async cancel() {
      await r.cancel?.()
      await events.return()
    },
  })
  return new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", ...responseHeaders } })
}

// ---- the plugin ---------------------------------------------------------------------------

export async function CursorAuthPlugin() {
  return {
    auth: {
      provider: ID,
      async loader(getAuth) {
        const auth = await getAuth()
        if (!auth || (auth.type !== "oauth" && auth.type !== "api")) return {}
        return {
          baseURL: AGENT,
          apiKey: "cursor",
          // every chat completion run on Cursor's agent API, with the
          // sign-in as it is now
          async fetch(input, init = {}) {
            const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
            if (!/\/chat\/completions$/.test(new URL(url).pathname))
              return errorResponse({ status: 404, message: "only chat completions are served" })
            let chat
            try {
              const b = init.body ?? (input instanceof Request ? await input.clone().text() : undefined)
              chat = JSON.parse(typeof b === "string" ? b : new TextDecoder().decode(b))
            } catch {
              return errorResponse({ status: 400, message: "a request that isn't JSON" })
            }
            const session = new Headers(init.headers ?? (input instanceof Request ? input.headers : undefined)).get(SESSION) ?? ""
            return answer((await getAuth()) ?? auth, chat, init.signal ?? (input instanceof Request ? input.signal : undefined), session)
          },
        }
      },
      methods: [
        { type: "oauth", label: "Cursor (browser)", authorize: browserSignIn },
        { type: "oauth", label: "cursor-agent's sign-in", authorize: cliSignIn },
        { type: "api", label: "Cursor API key (cursor.com/dashboard → Integrations)" },
      ],
      // magpie's: how much of the plan's included usage is gone (the plan
      // is the one the sign-in read). The built-in's read, clean or not,
      // neither marked the account nor cleared it.
      async usage(getAuth, provider) {
        let tok
        try {
          tok = await tokenOf(await getAuth())
        } catch (e) {
          return { error: e.message, windows: [], signIn: "kept" }
        }
        const [u, plan, bot] = await Promise.all([usage(tok, Object.keys(provider?.models ?? {})), planOf(tok), botUsage(tok)])
        // Bot goes among magpie's three menu-bar rings: before Total, and
        // before an On-demand with no limit, which is only a dollar figure on
        // a ring that never fills (magpie #2213, Morganwan90). An On-demand
        // with a limit stays ahead of it: that is what can stop the account.
        const last = (w) => w.name === "Total" || (w.name === "On-demand" && w.aside)
        const windows = [...u.windows.filter((w) => !last(w)), ...bot, ...u.windows.filter(last)]
        return { ...u, windows, ...(plan ? { plan } : {}), signIn: "kept" }
      },
    },
    // the session the request is part of, for the conversation_id
    // (conversationID): magpie and OpenCode name it here, and the loader's
    // fetch reads it back. Every plugin's hook sees every provider's
    // requests, so it is put on Cursor's alone.
    async "chat.headers"(input, output) {
      if (input?.model?.providerID !== ID && input?.provider?.info?.id !== ID) return
      const s = sessionOf(input)
      if (s) output.headers[SESSION] = s
    },
    async config(config) {
      config.provider ??= {}
      const was = config.provider[ID] ?? {}
      config.provider[ID] = {
        name: "Cursor",
        npm: CHAT,
        api: AGENT,
        ...was,
        models: {
          auto: { name: "Auto", limit: { context: DEFAULT_CONTEXT, output: 0 }, attachment: true, tool_call: true },
          ...(was.models ?? {}),
        },
      }
    },
    // the account's list: each picker model once for each context size,
    // with that window
    provider: {
      id: ID,
      async models(provider, { auth } = {}) {
        if (!auth) return provider.models
        // a list Cursor couldn't give is a failure, not the few models
        // configured: magpie keeps the list it had
        const tok = await tokenOf(auth)
        return Object.fromEntries((await usable(tok)).map((m) => [m.id, runtimeModel(m)]))
      },
    },
  }
}

// for tests
export const _internal = { keychain, keychainToken, nativeTransport, onDemandOf,resetH1: () => (h1Until = 0), onH1: () => Date.now() < h1Until, resetTeam: () => { teamRead.at = 0; teamRead.info = null }, expireTeam: () => (teamRead.at = 0),H2, H1, h2Blocked, open, openH1, openH2, STEP_WAIT, stepUsage, usageKey, ledger, owed, flights, misses, unreadable, catalog, usable, request, modelsOf, tokensOf, maxRequired, conversationID, usageOf, sessionOf, SESSION, errorResponse, kept, poolBase, conversation, buildRun, decode, exec, fields, pb, pbValue, pbAny, frame, frames, failure, toolsOf, expiry }
