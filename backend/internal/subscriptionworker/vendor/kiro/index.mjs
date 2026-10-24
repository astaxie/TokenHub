// Kiro's subscriptions (kiro.dev) as an OpenCode provider plugin.
//
// An account is signed in the way the Kiro IDE signs in: Kiro's sign-in
// page (app.kiro.dev) offers Google, GitHub, AWS Builder ID and IAM
// Identity Center, and sends the browser back to one of the ports the IDE
// listens on. Google and GitHub come back with a code Kiro's auth service
// trades for tokens; Builder ID and Identity Center come back with where to
// sign in at AWS, which is then done with a client registered for it.
// kiro-cli's or the IDE's own sign-in can be used instead: it is read where
// they keep it each time, refreshed by kiro-cli first, and a token
// refreshed here is written back, so they go on with it. A Kiro API key
// (ksk_…) signs in too.
//
// Kiro has no API OpenCode speaks: the models are declared on Anthropic's
// Messages, and the fetch here sends each request whole to Kiro's own API
// (runtime.<region>.kiro.dev/generateAssistantResponse, the one kiro-cli
// talks to) as a conversation, and turns the AWS event stream it answers
// with back into Messages' events.
import { createServer } from "node:http"
import { createHash, randomBytes, randomUUID } from "node:crypto"
import { execFile } from "node:child_process"
import { existsSync, statSync } from "node:fs"
import { readFile, rename, writeFile } from "node:fs/promises"
import { homedir, hostname } from "node:os"
import { delimiter, join } from "node:path"

const ID = "kiro"
const MESSAGES = "@ai-sdk/anthropic"
const BASE = "https://runtime.us-east-1.kiro.dev" // what OpenCode is given; the fetch sends each request to the account's region

const PORTAL = "https://app.kiro.dev"
const AUTH_SERVICE = "https://prod.us-east-1.auth.desktop.kiro.dev"
const oidc = (region) => `https://oidc.${region}.amazonaws.com`
const runtime = (region) => `https://runtime.${region}.kiro.dev`
const management = (region) => `https://management.${region}.kiro.dev`
const refreshURL = (region) => `https://prod.${region}.auth.desktop.kiro.dev/refreshToken`
// the ports Kiro's sign-in page sends the browser back to, the IDE's
const CALLBACK_PORTS = [3128, 4649, 6588, 8008, 9091, 49153, 50153, 51153, 52153, 53153]
// what the IDE asks AWS for
const SCOPES = ["codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations",
  "codewhisperer:transformations", "codewhisperer:taskassist"]
// how the IDE names itself to Kiro's auth service, and when refreshing
const IDE_UA = "KiroIDE-1.1.70-" + createHash("sha256").update("magpie:" + hostname()).digest("hex")
const DESKTOP_UA = "Kiro-Desktop/0.2.13 (darwin; arm64)"
const SIGN_IN_TIMEOUT = 10 * 60 * 1000
// A Builder ID sign-in has no profile of its own, and Kiro won't list one
// for it (List-Available-Profiles is a 403, "AWS Builder ID is not supported
// for this operation"); the Kiro IDE and kiro-cli name this service profile
// for it instead, so its models, usage and chat are asked with it too.
const BUILDER_ID_START = "https://view.awsapps.com/start"
const BUILDER_ID_PROFILE = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
const isBuilderID = (provider) => String(provider ?? "").toLowerCase() === "builderid"
// uidOf is what tells two Kiro accounts of one email apart: the way it was
// signed in to. Kiro keeps a Google and a GitHub sign-in of one email (or
// Builder ID's, or Identity Center's) as different accounts, and magpie
// keeps a sign-in of an email already signed in as that account unless
// both have a uid and the two differ (#309). "" when it isn't known.
const uidOf = (loginProvider) => String(loginProvider ?? "").trim().toLowerCase()
// nameOf is what an account is called: its email and the way it was signed
// in to, as Kiro's sign-in page names it, so two accounts of one email read
// apart (#309; magpie names a plugin's account by its accountId). A way not
// known, kiro-cli's or the Kiro IDE's sign-in among them, leaves the email
// as it is, and magpie tells two such apart by their slot.
const WAYS = { google: "Google", github: "GitHub", builderid: "Builder ID", enterprise: "IAM Identity Center", internal: "Internal" }
const nameOf = (email, loginProvider) => (WAYS[uidOf(loginProvider)] ? `${email} (${WAYS[uidOf(loginProvider)]})` : email)
// a bare email: what an account was called before nameOf
const bareEmail = (id) => typeof id === "string" && /^[^\s()]+@[^\s()]+$/.test(id)

const env = (k) => process.env[k] ?? ""
const home = () => env("HOME") || homedir()

// ---- where Kiro's own apps keep their sign-in -----------------------------------

function cliDB() {
  if (process.platform === "win32") return join(env("APPDATA") || join(home(), "AppData", "Roaming"), "kiro-cli", "data.sqlite3")
  if (process.platform === "darwin") return join(home(), "Library", "Application Support", "kiro-cli", "data.sqlite3")
  return join(home(), ".local", "share", "kiro-cli", "data.sqlite3")
}
const ideDir = () => join(home(), ".aws", "sso", "cache")

// kiroExecutable finds kiro-cli, which refreshes its own sign-in.
function kiroExecutable() {
  for (const d of env("PATH").split(delimiter)) {
    const p = join(d, process.platform === "win32" ? "kiro-cli.exe" : "kiro-cli")
    if (d && isFile(p)) return p
  }
  for (const p of [join(home(), ".local", "bin", "kiro-cli"), "/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli",
    "/usr/local/bin/kiro-cli", "/opt/homebrew/bin/kiro-cli"])
    if (isFile(p)) return p
  return ""
}
function isFile(p) {
  try {
    return statSync(p).isFile()
  } catch {
    return false
  }
}

// openDB opens kiro-cli's SQLite database, with Bun's driver or Node's.
async function openDB(path, readonly) {
  try {
    const { Database } = await import("bun:sqlite")
    return new Database(path, readonly ? { readonly: true } : { readwrite: true })
  } catch (e) {
    if (typeof Bun !== "undefined") throw e
  }
  const { DatabaseSync } = await import("node:sqlite")
  return new DatabaseSync(path, { readOnly: readonly })
}

const time = (s) => {
  const t = Date.parse(s ?? "")
  return Number.isFinite(t) ? t : 0
}

// readCLI reads kiro-cli's sign-in: with Google or GitHub, with AWS (Builder
// ID, IAM Identity Center), or with a company's own identity provider.
async function readCLI() {
  const path = cliDB()
  if (!existsSync(path)) return null
  let db
  try {
    db = await openDB(path, true)
    const get = (key) => {
      const row = db.prepare("SELECT value FROM auth_kv WHERE key = ?").get(key)
      if (!row?.value) return null
      try {
        return JSON.parse(row.value)
      } catch {
        return null
      }
    }
    for (const kind of ["social", "odic", "external-idp"]) {
      const key = `kirocli:${kind}:token`
      const m = get(key)
      if (!m?.access_token) continue
      const c = { access: m.access_token, refresh: m.refresh_token ?? "", expires: time(m.expires_at), region: m.region || "us-east-1",
        profile: m.profile_arn ?? "", dbKey: key }
      if (kind === "social") c.method = "social"
      else if (kind === "odic") {
        c.method = "idc"
        // as kiro-cli tells them apart: Builder ID's start URL, or none
        c.builderID = !m.start_url || m.start_url === BUILDER_ID_START
        const reg = get("kirocli:odic:device-registration")
        if (reg) [c.clientId, c.clientSecret] = [reg.client_id ?? "", reg.client_secret ?? ""]
      } else {
        c.method = "external-idp"
        c.clientId = m.client_id ?? ""
        c.tokenURL = m.token_endpoint || (m.issuer_url ? m.issuer_url.replace(/\/$/, "") + "/v1/token" : "")
      }
      return c
    }
  } catch {
  } finally {
    db?.close()
  }
  return null
}

// readIDE reads the Kiro IDE's sign-in.
async function readIDE() {
  const path = join(ideDir(), "kiro-auth-token.json")
  let t
  try {
    t = JSON.parse(await readFile(path, "utf8"))
  } catch {
    return null
  }
  if (!t?.accessToken) return null
  const c = { access: t.accessToken, refresh: t.refreshToken ?? "", expires: time(t.expiresAt), region: t.region || "us-east-1",
    profile: t.profileArn ?? "", method: "idc", idePath: path, builderID: isBuilderID(t.provider) }
  if (t.clientIdHash) {
    try {
      const reg = JSON.parse(await readFile(join(ideDir(), t.clientIdHash + ".json"), "utf8"))
      ;[c.clientId, c.clientSecret] = [reg.clientId ?? "", reg.clientSecret ?? ""]
    } catch {}
  }
  if (String(t.authMethod).toLowerCase() === "social" || !c.clientId) c.method = "social"
  return c
}

// saveBack writes a refreshed token back where it was read from, so
// kiro-cli or the IDE goes on with it (a refresh may replace the refresh
// token).
async function saveBack(c) {
  const expires = new Date(c.expires).toISOString()
  if (c.dbKey) {
    let db
    try {
      db = await openDB(cliDB(), false)
      db.exec("PRAGMA busy_timeout = 3000")
      const row = db.prepare("SELECT value FROM auth_kv WHERE key = ?").get(c.dbKey)
      if (!row?.value) return
      const m = JSON.parse(row.value)
      Object.assign(m, { access_token: c.access, refresh_token: c.refresh, expires_at: expires })
      if (c.profile && c.method === "social") m.profile_arn = c.profile
      db.prepare("UPDATE auth_kv SET value = ? WHERE key = ?").run(JSON.stringify(m), c.dbKey)
    } catch {
    } finally {
      db?.close()
    }
  } else if (c.idePath) {
    try {
      const m = JSON.parse(await readFile(c.idePath, "utf8"))
      Object.assign(m, { accessToken: c.access, refreshToken: c.refresh, expiresAt: expires })
      await writeFile(c.idePath + ".magpie-tmp", JSON.stringify(m, null, 2), { mode: 0o600 })
      await rename(c.idePath + ".magpie-tmp", c.idePath)
    } catch {}
  }
}

// ---- tokens --------------------------------------------------------------------

// EXPIRED is the built-in's words for a sign-in Kiro refused, or one with
// nothing to refresh it.
const EXPIRED = "Kiro's sign-in has expired; sign in again"
const EXPIRED_CLI = EXPIRED + " with `kiro-cli login` or the Kiro IDE"

// gone is an error saying the sign-in itself is gone — refused, or never
// there. The built-in marked no Kiro account lapsed, not even then, nor
// cleared one: every answer and usage read says the account is kept.
const gone = (msg) => Object.assign(new Error(msg), { gone: true })
const EARLY_MS = 2 * 60 * 1000 // a token this close to its end is refreshed before a request
const LEAD_MS = 10 * 60 * 1000 // and this close, magpie renews it ahead of time (auth.refresh)
const fresh = (c, early = EARLY_MS) => c.method === "apikey" || !c.expires || c.expires - Date.now() > early

async function post(url, contentType, body, headers = {}) {
  let res
  try {
    res = await fetch(url, { method: "POST", headers: { "Content-Type": contentType, Accept: "application/json", ...headers }, body,
      signal: AbortSignal.timeout(30_000) })
  } catch (e) {
    throw new Error("refreshing Kiro's sign-in: " + e.message)
  }
  const text = await res.text()
  if (!res.ok) {
    if ([400, 401, 403].includes(res.status)) throw gone(EXPIRED_CLI)
    throw new Error(`refreshing Kiro's sign-in: ${res.status} ${STATUS_TEXT[res.status] ?? ""}`.trimEnd()) // Go's res.Status
  }
  return JSON.parse(text)
}

// refresh refreshes a sign-in's token the way its owner would: kiro-cli's
// by kiro-cli first.
async function refresh(c) {
  if (c.dbKey) {
    const bin = kiroExecutable()
    if (bin) {
      await new Promise((r) => execFile(bin, ["debug", "refresh-auth-token"], { timeout: 20_000 }, () => r()))
      const n = await readCLI()
      if (n && n.dbKey === c.dbKey && n.access !== c.access && fresh(n)) return n
    }
  }
  if (!c.refresh) throw gone(EXPIRED_CLI)
  let out
  switch (c.method) {
    case "social": {
      const r = await post(refreshURL(c.region), "application/json", JSON.stringify({ refreshToken: c.refresh }), { "User-Agent": DESKTOP_UA })
      out = { access: r.accessToken, refresh: r.refreshToken, expiresIn: r.expiresIn }
      if (r.profileArn) c.profile = r.profileArn
      break
    }
    case "idc": {
      const r = await post(oidc(c.region) + "/token", "application/json",
        JSON.stringify({ clientId: c.clientId, clientSecret: c.clientSecret, refreshToken: c.refresh, grantType: "refresh_token" }))
      out = { access: r.accessToken, refresh: r.refreshToken, expiresIn: r.expiresIn }
      break
    }
    case "external-idp": {
      if (!c.tokenURL) throw gone(EXPIRED + " with `kiro-cli login`")
      const form = new URLSearchParams({ grant_type: "refresh_token", client_id: c.clientId ?? "", refresh_token: c.refresh })
      const r = await post(c.tokenURL, "application/x-www-form-urlencoded", form.toString())
      out = { access: r.access_token, refresh: r.refresh_token, expiresIn: r.expires_in }
      break
    }
    default:
      throw gone("Kiro's sign-in has expired")
  }
  if (!out.access) throw gone(EXPIRED_CLI)
  const n = { ...c, access: out.access, expires: Date.now() + (out.expiresIn > 0 ? out.expiresIn : 3600) * 1000 }
  if (out.refresh) n.refresh = out.refresh
  await saveBack(n)
  return n
}

// headers say who is calling: the token, and what kind it is.
function authHeaders(a) {
  const h = { Authorization: `Bearer ${a.token}` }
  if (a.tokenType) h.tokentype = a.tokenType
  return h
}
const tokenType = (c) => (c.method === "apikey" ? "API_KEY" : c.method === "external-idp" ? "EXTERNAL_IDP" : "")

// region is where the account's API is: the region its profile is in, else
// the nearer of Kiro's two to where it signed in.
function regionOf(profile, signedIn) {
  const parts = String(profile ?? "").split(":")
  if (parts.length > 4 && parts[3]) return parts[3]
  return String(signedIn ?? "").startsWith("eu-") ? "eu-central-1" : "us-east-1"
}

// STATUS_TEXT is Go's http.StatusText for the answers Kiro gives.
const STATUS_TEXT = { 400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 408: "Request Timeout",
  409: "Conflict", 413: "Request Entity Too Large", 429: "Too Many Requests", 500: "Internal Server Error", 502: "Bad Gateway",
  503: "Service Unavailable", 504: "Gateway Timeout" }

class KiroError extends Error {
  constructor(status, body) {
    let msg = ""
    try {
      msg = JSON.parse(body)?.message ?? ""
    } catch {}
    super(msg ? `Kiro: ${msg} (${status})` : `Kiro: ${STATUS_TEXT[status] ?? ""}`)
    this.status = status
    this.body = String(body ?? "")
  }
}

// managementCall calls Kiro's management API: GET with a query, or POST
// with a JSON body.
async function managementCall(region, a, method, query, body) {
  let url = `${management(region)}/${method}`
  if (!body && query) url += "?" + new URLSearchParams(query)
  const res = await fetch(url, {
    method: body ? "POST" : "GET",
    headers: { ...authHeaders(a), Accept: "application/json", ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(20_000),
  })
  const text = await res.text()
  if (!res.ok) throw new KiroError(res.status, text)
  return JSON.parse(text)
}

// controlCall is one of Kiro's management operations as kiro-cli sends it
// for an API key: AWS JSON 1.0, the operation named in X-Amz-Target, POSTed
// to the endpoint's root (with ?origin=KIRO_CLI where kiro-cli adds it).
async function controlCall(region, a, target, body, query) {
  const res = await fetch(management(region) + "/" + (query ? "?" + new URLSearchParams(query) : ""), {
    method: "POST",
    headers: { ...authHeaders(a), "Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": target,
      "x-amzn-codewhisperer-optout": "false" },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(20_000),
  })
  const text = await res.text()
  if (!res.ok) throw new KiroError(res.status, text)
  return JSON.parse(text)
}

// KEY_REGIONS are where kiro-cli asks GetProfile for an API key's profile,
// in its order (the GovCloud and ISO ones after them aren't Kiro's
// subscriptions).
const KEY_REGIONS = ["us-east-1", "eu-central-1"]

// keyProfile is an API key's profile, as kiro-cli finds it: GetProfile in
// each region until one answers with a profile. kiro-cli goes on without
// one when every region turns it down (a key from an account with no
// CodeWhisperer profile answers 400), and so does this: "" then, and the
// key's calls are made in us-east-1 without a profile, as kiro-cli makes
// them (Hollowhere, on magpie's Discord: a key kiro-cli 2.33.1 served
// failed every request here with "Kiro didn't take the API key: 400").
async function keyProfile(c) {
  for (const region of KEY_REGIONS) {
    try {
      const arn = (await controlCall(region, { token: c.access, tokenType: "API_KEY" }, "AmazonCodeWhispererService.GetProfile", {}))
        ?.profile?.arn
      if (arn) return arn
    } catch {}
  }
  return ""
}

// profileOf finds the profile a sign-in that doesn't name one uses:
// Builder ID's, or the first Kiro lists in either of its regions.
async function profileOf(c) {
  if (c.builderID) return BUILDER_ID_PROFILE
  let last
  for (const region of ["us-east-1", "eu-central-1"]) {
    try {
      const out = await managementCall(region, { token: c.access, tokenType: tokenType(c) }, "List-Available-Profiles", null, {})
      const arn = (out?.profiles ?? []).find((p) => p?.arn)?.arn
      if (arn) return arn
    } catch (e) {
      // a Builder ID sign-in the token didn't say was one
      if (c.method === "idc" && e?.status === 403 && e.body.includes("Builder ID")) return BUILDER_ID_PROFILE
      last = e
    }
  }
  throw last ?? new Error("Kiro has no profile for this sign-in")
}

// usageLimits is Get-Usage-Limits: the plan, the account's email, credits.
function usageLimits(a, email) {
  // an API key's, as kiro-cli asks for it: no profile, no email asked
  if (a.tokenType === "API_KEY") return controlCall(a.region, a, "AmazonCodeWhispererService.GetUsageLimits", { origin: "KIRO_CLI" }, { origin: "KIRO_CLI" })
  return managementCall(a.region, a, "Get-Usage-Limits",
    { origin: "KIRO_CLI", profileArn: a.profile, resourceType: "CREDIT", isEmailRequired: String(email) })
}

// planName is the subscription's name as Kiro gives it ("KIRO FREE"), said
// "Kiro Free".
const planName = (title) =>
  String(title ?? "").toLowerCase().split(/\s+/).filter(Boolean).map((w) => (w === "kiro" ? "Kiro" : w[0].toUpperCase() + w.slice(1))).join(" ")

// goG is Go's %g of x: an exponent from a million up.
function goG(x) {
  const [m, e] = x.toExponential().split("e")
  const exp = Number(e)
  if (exp < -4 || exp >= 6) return `${m}e${exp < 0 ? "-" : "+"}${String(Math.abs(exp)).padStart(2, "0")}`
  return String(x)
}

// usageOf is the allowance Get-Usage-Limits tells, as magpie's built-in
// said it: the account's email, the credits of each allowance, the month's and a free trial's
// while it runs.
function usageOf(l) {
  const at = (secs) => (secs > 0 ? new Date(Math.trunc(secs) * 1000).toISOString() : undefined)
  const count = (used, limit) => used.toFixed(2).replace(/0+$/, "").replace(/\.$/, "") + " / " + goG(limit)
  const num = (x) => (typeof x === "number" && Number.isFinite(x) ? x : 0)
  const windows = []
  for (const u of l?.usageBreakdownList ?? []) {
    const t = u?.freeTrialInfo
    if (t && String(t.freeTrialStatus ?? "").toUpperCase() === "ACTIVE" && num(t.usageLimitWithPrecision) > 0) {
      const used = num(t.currentUsageWithPrecision), limit = num(t.usageLimitWithPrecision)
      windows.push({ name: "Free trial", used: (100 * used) / limit, resetsAt: at(num(t.freeTrialExpiry)), display: count(used, limit) })
    }
    const used = num(u?.currentUsageWithPrecision), limit = num(u?.usageLimitWithPrecision)
    if (limit <= 0) continue
    const reset = num(u?.nextDateReset) || num(l?.nextDateReset)
    windows.push({ name: u?.displayNamePlural || u?.displayName || "Credits", used: (100 * used) / limit, resetsAt: at(reset),
      display: count(used, limit), span: 30 * 24 * 3600 })
  }
  const email = l?.userInfo?.email
  return { plan: planName(l?.subscriptionInfo?.subscriptionTitle), ...(typeof email === "string" && email ? { user: email } : {}), windows }
}

// ---- the account, as the plugin holds it -------------------------------------------

// credOf is the sign-in a stored auth names: a key, one signed in here, or
// kiro-cli's or the IDE's, read where they keep it now.
async function credOf(auth) {
  if (auth?.type === "api" && auth.key) return { access: auth.key, method: "apikey", region: "us-east-1" }
  if (auth?.type !== "oauth") return null
  if (auth.source === "kiro-cli") return readCLI()
  if (auth.source === "kiro-ide") return readIDE()
  if (auth.source === "kiro") return (await readCLI()) ?? (await readIDE())
  if (!auth.access) return null
  return { access: auth.access, refresh: auth.refresh ?? "", expires: auth.expires ?? 0, method: auth.method || "social",
    region: auth.region || "us-east-1", profile: auth.profileArn ?? "", clientId: auth.clientId ?? "", clientSecret: auth.clientSecret ?? "",
    builderID: isBuilderID(auth.loginProvider), own: true }
}

// Account holds the credentials in use, refreshing them when near their
// end or when Kiro turned one down; one refresh at a time, as a refresh
// may replace the refresh token.
//
// after is what each refresh gave, by the access token it replaced: a
// sign-in read with that token (its save failed, or magpie hasn't yet
// saved what auth.refresh gave) goes on with the newer one rather than
// spending the replaced refresh token again.
function account(client) {
  let held = null // { id, c }
  const after = new Map()
  const newest = (c) => {
    for (let i = 0; i < 16 && after.has(c.access); i++) c = { ...after.get(c.access) }
    return c
  }
  const renewed = (c, n) => {
    after.set(c.access, n)
    if (after.size > 16) after.delete(after.keys().next().value)
    return n
  }
  let lock = Promise.resolve()
  const locked = (fn) => {
    const run = lock.then(fn, fn)
    lock = run.catch(() => {})
    return run
  }
  // an account signed in here before its uid was kept is given it, so a
  // sign-in with another way to the same email no longer replaces it
  const uidSaved = new Set()
  const uidField = (auth) => (!auth?.uid && uidOf(auth?.loginProvider) ? { uid: uidOf(auth.loginProvider) } : {})
  // and one called by its bare email is given its name, the way it was
  // signed in to with it
  const nameField = (auth) =>
    bareEmail(auth?.accountId) && nameOf(auth.accountId, auth.loginProvider) !== auth.accountId
      ? { accountId: nameOf(auth.accountId, auth.loginProvider) } : {}
  const idFields = (auth) => ({ ...uidField(auth), ...nameField(auth) })
  const save = async (auth, c) => {
    if (!c.own || !client?.auth?.set) return
    const { type: _t, ...rest } = auth
    await client.auth.set({ path: { id: ID }, body: { ...rest, type: "oauth", access: c.access, refresh: c.refresh, expires: c.expires,
      profileArn: c.profile, region: c.region, ...idFields(auth) } }).catch(() => {})
  }
  const creds = (auth, stale = false) =>
    locked(async () => {
      const id = auth?.type === "api" ? "key:" + auth.key : "oauth:" + (auth?.source ?? "own")
      // what its owner holds now: it may have refreshed it, signed out, or
      // signed in to another account since
      let read = await credOf(auth)
      if (!read) {
        held = null
        throw gone(
          auth?.source
            ? "Kiro isn't signed in; add the Kiro subscription in magpie, sign in with `kiro-cli login` or the Kiro IDE, or save a Kiro API key on the provider"
            : "this Kiro account's sign-in is gone; add it again in magpie",
        )
      }
      read = newest(read)
      let c = held?.id === id ? held.c : null
      let saved = false
      if (!c || read.access !== c.access || !fresh(c) || stale) {
        if (!read.profile && c && read.access === c.access) read.profile = c.profile
        if (stale && c && read.access !== c.access && fresh(read)) stale = false // the owner refreshed it already
        c = read
        if (!fresh(c) || stale) {
          if (c.method === "apikey") throw gone("Kiro turned down the API key saved on the provider")
          c = renewed(c, await refresh(c))
          await save(auth, c)
          saved = true
        }
      }
      if (c.method === "apikey") {
        if (!c.asked) {
          c.profile = await keyProfile(c)
          c.asked = true
        }
      } else if (!c.profile) {
        c.profile = await profileOf(c)
        if (!auth?.profileArn && !saved) {
          await save(auth, c)
          saved = true
        }
      }
      // once per token: a save that didn't take isn't tried on every request
      if (!saved && c.own && Object.keys(idFields(auth)).length && !uidSaved.has(c.access)) {
        uidSaved.add(c.access)
        await save(auth, c)
      }
      held = { id, c }
      // kiro-cli names no profile in an API key's calls, even one GetProfile
      // found; the profile only says the key's region
      const profile = c.method === "apikey" ? "" : c.profile
      return { token: c.access, tokenType: tokenType(c), profile, region: regionOf(c.profile, c.region) }
    })
  // renew is auth.refresh's: a sign-in kept here (not kiro-cli's or the
  // IDE's, which their owners keep) renewed under the same lock as the
  // requests' refresh, so the two never spend one refresh token. It saves
  // nothing — magpie saves what it gives — and gives only what changed.
  creds.renew = (auth) =>
    locked(async () => {
      const read = await credOf(auth)
      if (!read?.own || !read.refresh) return undefined
      let c = newest(read)
      // renewed here already (a request's refresh, or this hook's whose
      // result magpie hasn't saved yet), the store not yet saying so
      if (c.access === read.access || !fresh(c, LEAD_MS)) c = renewed(c, await refresh(c))
      held = { id: "oauth:own", c }
      const out = { access: c.access, expires: c.expires }
      if (c.refresh !== auth.refresh) out.refresh = c.refresh
      if (c.profile && c.profile !== auth.profileArn) out.profileArn = c.profile
      return { ...out, ...idFields(auth) }
    })
  return creds
}

// ---- models ----------------------------------------------------------------------

const windows = new Map() // a model's context, as Kiro last listed it

const thinks = (id) => /claude/i.test(id) || id === "auto"
// the levels a thinking model offers, as the built-in thinks them (xhigh as
// long as max: #120)
const BUDGETS = { low: 10000, medium: 20000, high: 30000, xhigh: 50000, max: 50000 }

function configModel(m) {
  return {
    name: m.name,
    limit: { context: m.context ?? 0, output: m.output ?? 0 },
    ...(m.images ? { attachment: true, modalities: { input: ["text", "image"], output: ["text"] } } : {}),
    ...(thinks(m.id)
      ? { reasoning: true, variants: Object.fromEntries(Object.entries(BUDGETS).map(([k, b]) => [k, { thinking: { type: "enabled", budgetTokens: b } }])) }
      : {}),
    tool_call: true,
  }
}

function runtimeModel(m) {
  const input = { text: true, image: !!m.images, audio: false, video: false, pdf: false }
  return {
    id: m.id,
    providerID: ID,
    name: m.name ?? m.id,
    api: { id: m.id, url: BASE, npm: MESSAGES },
    status: "active",
    headers: {},
    options: {},
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: { context: m.context ?? 0, output: m.output ?? 0 },
    capabilities: {
      temperature: true,
      reasoning: thinks(m.id),
      attachment: !!m.images,
      toolcall: true,
      input,
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: thinks(m.id) ? Object.fromEntries(Object.entries(BUDGETS).map(([k, b]) => [k, { thinking: { type: "enabled", budgetTokens: b } }])) : {},
  }
}

// Kiro's own list is asked for once signed in; until then, Auto, which
// picks a model for each request.
const MODELS = [{ id: "auto", name: "Auto" }]

// keyModels is an API key's list, asked as kiro-cli asks it: Kiro's
// control plane's ListAvailableModels, with no profile, each page.
async function keyModels(a) {
  const out = { models: [] }
  let next
  for (let i = 0; i < 20; i++) {
    const page = await controlCall(a.region, a, "KiroControlPlaneBearerService.ListAvailableModels",
      { origin: "KIRO_CLI", ...(next ? { nextToken: next } : {}) }, { origin: "KIRO_CLI" })
    out.models.push(...(page?.models ?? []))
    out.defaultModel ??= page?.defaultModel
    next = page?.nextToken
    if (!next) break
  }
  return out
}

// listModels is List-Available-Models, the default first.
async function listModels(a) {
  const out = a.tokenType === "API_KEY" ? await keyModels(a) : await managementCall(a.region, a, "List-Available-Models", { origin: "KIRO_CLI", profileArn: a.profile })
  const list = []
  for (const m of out?.models ?? []) {
    if (!m?.modelId) continue
    let name = m.modelName
    if (!name || name === m.modelId) name = m.modelId === "auto" ? "Auto" : m.modelId
    const model = { id: m.modelId, name, context: m.tokenLimits?.maxInputTokens ?? 0, output: m.tokenLimits?.maxOutputTokens ?? 0,
      images: (m.supportedInputTypes ?? []).some((t) => String(t).toUpperCase() === "IMAGE") }
    if (m.modelId === out?.defaultModel?.modelId) list.unshift(model)
    else list.push(model)
  }
  if (!list.length) throw new Error("Kiro listed no models")
  for (const m of list) windows.set(m.id, m.context)
  return list
}

// ---- the request: Messages → a Kiro conversation ---------------------------------------

const PROCEED = "Please proceed with the task." // Kiro takes no empty message
const NO_RESULT = "Tool use was interrupted and did not produce a result."
const RESULT_LIMIT = 250000 // a tool's output is cut at this, as Kiro's own agent cuts it
const TOOL_ID = /^[a-zA-Z0-9_.:-]{1,64}$/

// toolID is a tool call's id as Kiro takes one: another vendor's becomes
// one of Kiro's own, the same each time.
const toolID = (id) => (TOOL_ID.test(id ?? "") ? id : "t_" + createHash("sha256").update(String(id ?? "")).digest("base64url").slice(0, 32))

// Kiro answers a tool named past 64 characters, or outside these, "Invalid
// tool use format." and won't raise the limit (kirodotdev/Kiro#7684), while
// MCP tools in Claude Code are named mcp__plugin_<plugin>_<server>__<tool>
// (yetone/magpie#1393).
const TOOL_NAME = /^[a-zA-Z0-9_-]{1,64}$/

// toolName is a tool's name as Kiro takes one: another is its head with a
// hash of the whole name, the same each time, so the conversation's later
// turns name it the same way. toolNames maps each such name back.
const toolName = (name) => {
  const n = String(name ?? "")
  if (TOOL_NAME.test(n)) return n
  const hash = createHash("sha256").update(n).digest("hex").slice(0, 12)
  return n.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, 64 - 1 - hash.length) + "_" + hash
}

// toolNames is the name the caller knows for each tool a request names
// that Kiro is sent under another name.
function toolNames(req) {
  const names = new Map()
  const add = (n) => {
    if (typeof n !== "string" || !n) return
    const k = toolName(n)
    if (k !== n) names.set(k, n)
  }
  for (const t of req.tools ?? []) add(t?.name)
  for (const m of req.messages ?? []) if (Array.isArray(m.content)) for (const p of m.content) if (p?.type === "tool_use") add(p.name)
  return names
}

function imageFormat(mediaType) {
  const t = String(mediaType ?? "").toLowerCase()
  const f = t.replace(/^image\//, "")
  if (f === "jpg") return "jpeg"
  if (!f || f === t) return "png"
  return f
}

// addImage puts an Anthropic image block in images as Kiro takes it: the
// bytes inline. One named only by a URL has no bytes to send and is left out.
function addImage(images, p) {
  if (p.source?.type === "base64" && p.source.data) images.push({ format: imageFormat(p.source.media_type), source: { bytes: p.source.data } })
}

const textOf = (c) =>
  typeof c === "string" ? c : Array.isArray(c) ? c.filter((p) => p?.type === "text").map((p) => p.text ?? "").join("\n\n") : ""
const joinNonEmpty = (a, b) => (a && b ? a + "\n\n" + b : a + b)

function documentText(p) {
  const title = p.title ? `${p.title}\n` : ""
  if (p.source?.type === "text") return title + (p.source.data ?? "")
  if (p.source?.type === "content") return title + textOf(p.source.content)
  return `${title}[a ${p.source?.media_type ?? "document"} attachment]`
}

// thinking is whether the model is asked to think aloud, and for how
// long: when the caller asked for it, of a Claude model (or Auto).
function thinking(req, model) {
  const effort = req.output_config?.effort ?? ""
  const on = req.thinking?.type === "enabled" || req.thinking?.type === "adaptive" || (effort && effort !== "low")
  if (!on || !thinks(String(model).toLowerCase())) return 0
  const budget = { low: 10000, high: 30000, xhigh: 50000, max: 50000 }[effort]
  if (budget) return budget
  if (!effort && req.thinking?.budget_tokens > 0) return req.thinking.budget_tokens
  return 20000
}

// formatText is the structured output a request asks for, said in words:
// Anthropic's output_config.format (or the AI SDK's output_format) with a
// JSON schema, "" when it asks for none. A JSON object with no schema
// reaches the plugin already told in the system prompt, as Messages has no
// field for it.
function formatText(req) {
  const f = req.output_config?.format ?? req.output_format
  if (f?.type !== "json_schema" || !f.schema || typeof f.schema !== "object") return ""
  return "Respond with a single JSON value and nothing else, matching this JSON schema:\n" + JSON.stringify(f.schema) + "."
}

// buildKiro is the body of a generateAssistantResponse call.
function buildKiro(req, model, profile, budget) {
  const entries = []
  // the messages the caller marked for its prompt cache (cache_control),
  // which go to Kiro as a cachePoint on the message they end up in (#110)
  const marked = new WeakSet()
  const mark = (msg, parts) => {
    if (parts.some((p) => p?.cache_control)) marked.add(msg)
  }
  const user = () => ({ content: "", modelId: model, origin: "KIRO_CLI" })
  for (const m of req.messages ?? []) {
    const parts = typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content ?? []
    const texts = []
    if (m.role === "assistant") {
      const a = { content: "", toolUses: [] }
      for (const p of parts) {
        if (p?.type === "text" && p.text) texts.push(p.text)
        else if (p?.type === "tool_use") {
          const input = p.input && typeof p.input === "object" && !Array.isArray(p.input) ? p.input : {}
          a.toolUses.push({ name: toolName(p.name), toolUseId: toolID(p.id), input })
        }
      }
      a.content = texts.join("\n\n")
      if (!a.content && !a.toolUses.length) continue // a turn that only thought
      const prev = entries.at(-1)?.assistantResponseMessage
      if (prev) {
        prev.content = joinNonEmpty(prev.content, a.content)
        prev.toolUses.push(...a.toolUses)
      } else entries.push({ assistantResponseMessage: a })
      mark(prev ?? a, parts)
      continue
    }
    const u = user()
    u.images = []
    const results = []
    for (const p of parts) {
      if (p?.type === "text") {
        if (p.text) texts.push(p.text)
      } else if (p?.type === "document") texts.push(documentText(p))
      else if (p?.type === "image") addImage(u.images, p)
      else if (p?.type === "tool_result") {
        let out = textOf(p.content)
        if (out.length > RESULT_LIMIT) out = out.slice(0, RESULT_LIMIT) + "\n… (cut)"
        results.push({ toolUseId: toolID(p.tool_use_id), status: p.is_error ? "error" : "success", content: [{ text: out || "(no output)" }] })
        // a tool's images (a screenshot, Codex's view_image) go with the
        // message, as Kiro's tool results carry text alone (magpie #369, #1861)
        if (Array.isArray(p.content)) for (const c of p.content) if (c?.type === "image") addImage(u.images, c)
      }
    }
    u.content = texts.join("\n\n")
    if (results.length) u.userInputMessageContext = { toolResults: results }
    const prev = entries.at(-1)?.userInputMessage
    if (prev) {
      prev.content = joinNonEmpty(prev.content, u.content)
      prev.images.push(...u.images)
      if (results.length) (prev.userInputMessageContext ??= { toolResults: [] }).toolResults.push(...results)
    } else entries.push({ userInputMessage: u })
    mark(prev ?? u, parts)
  }
  // the conversation opens with something the user said: tool results with
  // no call before them go, and a reply the caller began with
  while (entries.length) {
    const u = entries[0].userInputMessage
    if (u) {
      if (u.userInputMessageContext && !u.content && !u.images.length) {
        entries.shift()
        continue
      }
      delete u.userInputMessageContext
      break
    }
    entries.shift()
  }
  // and ends with it, the message being answered
  if (!entries.at(-1)?.userInputMessage) entries.push({ userInputMessage: { ...user(), images: [] } })
  // each call is answered in the message after it, and only calls are
  entries.forEach((e, i) => {
    const u = e.userInputMessage
    if (!u) return
    const calls = entries[i - 1]?.assistantResponseMessage?.toolUses ?? []
    const results = u.userInputMessageContext?.toolResults ?? []
    const answered = new Set()
    const kept = []
    for (const r of results)
      if (!answered.has(r.toolUseId) && calls.some((c) => c.toolUseId === r.toolUseId)) {
        answered.add(r.toolUseId)
        kept.push(r)
      }
    for (const c of calls)
      if (!answered.has(c.toolUseId)) {
        answered.add(c.toolUseId)
        kept.push({ toolUseId: c.toolUseId, status: "error", content: [{ text: NO_RESULT }] })
      }
    delete u.userInputMessageContext
    if (kept.length) u.userInputMessageContext = { toolResults: kept }
    if (!u.content && !u.userInputMessageContext) u.content = PROCEED
  })
  // only the latest images are sent again, as Kiro's own agent does
  let latest = -1
  for (let i = entries.length - 1; i >= 0; i--)
    if (entries[i].userInputMessage?.images.length) {
      latest = i
      break
    }
  entries.forEach((e, i) => {
    const u = e.userInputMessage
    if (u && (i !== latest || !u.images.length)) delete u.images
  })
  for (const e of entries) if (e.assistantResponseMessage && !e.assistantResponseMessage.toolUses.length) delete e.assistantResponseMessage.toolUses
  // the instructions go at the head of the first message
  let system = typeof req.system === "string" ? req.system : textOf(req.system)
  // Kiro has no field for the answer's format: it is told in words, at the
  // end of the instructions, as magpie's built-in tells it (inSystem)
  const format = formatText(req)
  if (format) system = system ? system + "\n\n" + format : format
  if (budget) system = `<thinking_mode>enabled</thinking_mode><max_thinking_length>${budget}</max_thinking_length>` + (system ? "\n" + system : "")
  if (system) entries[0].userInputMessage.content = system + "\n\n" + entries[0].userInputMessage.content
  if (Array.isArray(req.system)) mark(entries[0].userInputMessage, req.system)

  // the caller's tools, and any the conversation used that it no longer
  // offers — Kiro rejects a history naming a tool it wasn't given
  const current = entries.at(-1).userInputMessage
  const tools = []
  const offered = new Set()
  const EMPTY = { type: "object", properties: {} }
  for (const t of req.tools ?? []) {
    if (!t?.name || (t.type && t.type !== "custom" && !t.input_schema)) continue // server tools Kiro hasn't
    const name = toolName(t.name)
    offered.add(name)
    tools.push({ toolSpecification: { name, description: t.description || t.name, inputSchema: { json: t.input_schema ?? EMPTY } } })
    if (t.cache_control) tools.push({ cachePoint: { type: "default" } })
  }
  for (const e of entries)
    for (const c of e.assistantResponseMessage?.toolUses ?? [])
      if (!offered.has(c.name)) {
        offered.add(c.name)
        tools.push({ toolSpecification: { name: c.name, description: "Tool", inputSchema: { json: EMPTY } } })
      }
  if (tools.length) (current.userInputMessageContext ??= {}).tools = tools
  // Kiro's cachePoint is Bedrock's: it caches the prompt up to the point,
  // and a request may hold 4 at most, as Anthropic's cache_control; past
  // that the last ones are kept, the most of the prompt they cover
  const points = []
  for (const t of tools) if (t.cachePoint) points.push(() => tools.splice(tools.indexOf(t), 1))
  for (const e of entries) {
    const msg = e.userInputMessage ?? e.assistantResponseMessage
    if (marked.has(msg)) {
      msg.cachePoint = { type: "default" }
      points.push(() => delete msg.cachePoint)
    }
  }
  for (const drop of points.slice(0, Math.max(0, points.length - 4))) drop()

  const state = { chatTriggerType: "MANUAL", agentTaskType: "vibe", conversationId: randomUUID(), currentMessage: { userInputMessage: current } }
  if (entries.length > 1) state.history = entries.slice(0, -1)
  const out = { conversationState: state, agentMode: "vibe" }
  if (profile) out.profileArn = profile
  return JSON.stringify(out)
}

function sendKiro(a, body, signal) {
  const ua = `aws-sdk-rust/1.0.0 ua/2.1 os/other lang/rust api/codewhispererstreaming#1.28.3 m/E app/AmazonQ-For-CLI md/appVersion-1.28.3-${randomUUID().replaceAll("-", "")}`
  return fetch(runtime(a.region) + "/generateAssistantResponse", {
    method: "POST",
    headers: {
      ...authHeaders(a),
      "Content-Type": "application/json",
      Accept: "application/vnd.amazon.eventstream",
      "x-amzn-codewhisperer-optout": "true",
      "amz-sdk-invocation-id": randomUUID(),
      "amz-sdk-request": "attempt=1; max=1",
      "x-amzn-kiro-agent-mode": "vibe",
      "x-amz-user-agent": ua,
      "User-Agent": ua,
    },
    body,
    signal,
  })
}

// failure is the status and message for an error Kiro answered with:
// {"message": "...", "reason": "..."}.
function failure(status, body) {
  let msg = String(body ?? "").trim()
  try {
    const e = JSON.parse(body)
    if (e?.message) msg = e.message + (e.reason ? ` (${e.reason})` : "")
  } catch {}
  msg ||= STATUS_TEXT[status] ?? ""
  if (msg.includes("CONTENT_LENGTH_EXCEEDS_THRESHOLD") || msg.toLowerCase().includes("input is too long"))
    return { status: 400, message: "input is too long for the model's context: " + msg }
  if (msg.includes("INSUFFICIENT_MODEL_CAPACITY")) return { status: 503, message: msg }
  if (msg.includes("MONTHLY_REQUEST_COUNT") || msg.includes("USAGE_LIMIT")) return { status: 429, message: "usage limit reached: " + msg }
  return { status, message: msg }
}

const ERROR_TYPES = { 400: "invalid_request_error", 401: "authentication_error", 402: "billing_error", 403: "permission_error",
  404: "not_found_error", 413: "request_too_large", 429: "rate_limit_error", 503: "overloaded_error", 529: "overloaded_error" }
const errorType = (status) => ERROR_TYPES[status] ?? "api_error"
// Each failure says X-Magpie-Sign-In: kept, as the built-in never marked
// an account lapsed (see gone).
const errorResponse = (status, message) =>
  new Response(JSON.stringify({ type: "error", error: { type: errorType(status), message } }), {
    status,
    headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "kept" },
  })

// kept is res saying the account is kept: a success too, which the
// built-in never took for a sign-in come back.
function kept(res) {
  if (res.headers.has("X-Magpie-Sign-In")) return res
  const headers = new Headers(res.headers)
  headers.set("X-Magpie-Sign-In", "kept")
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}

// ---- the reply: an AWS event stream → Messages ---------------------------------------

// frames reads an AWS event stream: each message its length, its headers'
// length and a checksum, the headers, the payload, a checksum of the whole.
async function* frames(body) {
  let buf = new Uint8Array(0)
  const reader = body.getReader()
  try {
    for (;;) {
      while (buf.length >= 12) {
        const v = new DataView(buf.buffer, buf.byteOffset, buf.byteLength)
        const total = v.getUint32(0)
        const hlen = v.getUint32(4)
        if (total < 16 || total > 16 << 20 || hlen > total - 16) throw new Error("a malformed event stream")
        if (buf.length < total) break
        const rest = buf.subarray(12, total)
        yield { headers: frameHeaders(rest.subarray(0, hlen)), payload: rest.subarray(hlen, rest.length - 4) }
        buf = buf.slice(total)
      }
      const { value, done } = await reader.read()
      if (done) {
        if (buf.length) throw new Error("unexpected EOF")
        return
      }
      const n = new Uint8Array(buf.length + value.length)
      n.set(buf)
      n.set(value, buf.length)
      buf = n
    }
  } finally {
    reader.releaseLock?.()
  }
}

function frameHeaders(h) {
  const out = {}
  const dec = new TextDecoder()
  const v = new DataView(h.buffer, h.byteOffset, h.byteLength)
  let i = 0
  while (i < h.length) {
    const n = h[i]
    if (h.length < i + 2 + n) break
    const name = dec.decode(h.subarray(i + 1, i + 1 + n))
    const type = h[i + 1 + n]
    i += 2 + n
    const size = { 0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16 }[type]
    if (type === 6 || type === 7) {
      if (h.length < i + 2) break
      const l = v.getUint16(i)
      if (h.length < i + 2 + l) break
      if (type === 7) out[name] = dec.decode(h.subarray(i + 2, i + 2 + l))
      i += 2 + l
      continue
    }
    if (size === undefined || h.length < i + size) break
    i += size
  }
  return out
}

// exception is what an error in the stream says.
function exception(kind, payload) {
  let e = {}
  try {
    e = JSON.parse(new TextDecoder().decode(payload))
  } catch {}
  let msg = e.message || e.Message || new TextDecoder().decode(payload).trim()
  if (e.reason) msg += ` (${e.reason})`
  if (String(kind).toLowerCase().includes("throttl")) return "rate limited: " + msg
  return kind ? `${kind}: ${msg}` : msg
}

// ThinkParser splits a reply that opens with <thinking>…</thinking> into its
// thinking and the text after, however the tags fall across pieces.
const OPEN = "<thinking>"
const CLOSE = "</thinking>"
function partialSuffix(s, tag) {
  for (let n = Math.min(tag.length - 1, s.length); n > 0; n--) if (s.endsWith(tag.slice(0, n))) return n
  return 0
}
class ThinkParser {
  state = 0 // 0: not yet known, 1: thinking, 2: text
  buf = ""
  said = false
  feed(s) {
    this.buf += s
    const out = []
    for (;;) {
      if (this.state === 0) {
        const t = this.buf.replace(/^[ \t\r\n]+/, "")
        if (t.startsWith(OPEN)) {
          ;[this.buf, this.state] = [t.slice(OPEN.length), 1]
          continue
        }
        if (t.length < OPEN.length && OPEN.startsWith(t)) return out // may yet be the tag
        this.state = 2
        continue
      }
      if (this.state === 1) {
        if (!this.said) {
          this.buf = this.buf.replace(/^[\r\n]+/, "")
          if (!this.buf) return out
        }
        const i = this.buf.indexOf(CLOSE)
        if (i >= 0) {
          if (i > 0) {
            out.push({ kind: "think", text: this.buf.slice(0, i) })
            this.said = true
          }
          ;[this.buf, this.state] = [this.buf.slice(i + CLOSE.length).replace(/^[\r\n]+/, ""), 2]
          continue
        }
        // hold back what may be the start of the closing tag
        const n = this.buf.length - partialSuffix(this.buf, CLOSE)
        if (n > 0) {
          out.push({ kind: "think", text: this.buf.slice(0, n) })
          ;[this.buf, this.said] = [this.buf.slice(n), true]
        }
        return out
      }
      if (this.buf) out.push({ kind: "text", text: this.buf })
      this.buf = ""
      return out
    }
  }
  end() {
    if (!this.buf) return []
    const ev = { kind: this.state === 1 ? "think" : "text", text: this.buf }
    this.buf = ""
    if (this.state === 0) this.state = 2
    return [ev]
  }
}

// events turns Kiro's reply into events: text, thinking, tool calls (the
// arguments in pieces), then the usage and why it stopped — or an error.
async function* events(body, model, budget) {
  const think = new ThinkParser()
  let tool = ""
  let tools = 0
  let stop = ""
  // reported: Kiro sent a tokenUsage. It doesn't today: a reply has a
  // contextUsageEvent and a meteringEvent (credits) and nothing more, and
  // kiro-cli 2.32's own trace log shows its MetadataEvent with every token
  // count None, while a repeated prompt is billed about half the credits,
  // so Kiro does cache and doesn't say how much
  const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reported: false }
  let pct = 0
  let said = 0
  let failed = ""
  const dec = new TextDecoder()
  try {
    for await (const f of frames(body)) {
      const type = f.headers[":message-type"]
      if (type === "exception" || type === "error") {
        failed = exception(f.headers[":exception-type"] || f.headers[":error-code"], f.payload)
        break
      }
      let m
      try {
        m = JSON.parse(dec.decode(f.payload))
      } catch {
        continue
      }
      const kind = f.headers[":event-type"]
      if (kind === "assistantResponseEvent") {
        const text = typeof m.content === "string" ? m.content : ""
        said += text.length
        if (budget) yield* think.feed(text)
        else if (text) yield { kind: "text", text }
      } else if (kind === "reasoningContentEvent") {
        if (m.text) {
          said += m.text.length
          yield { kind: "think", text: m.text }
        } else if (m.signature) yield { kind: "sig", text: m.signature }
      } else if (kind === "toolUseEvent") {
        if (m.toolUseId && m.toolUseId !== tool) {
          yield* think.end() // the text before a call is all said
          tool = m.toolUseId
          tools++
          yield { kind: "toolStart", id: m.toolUseId, name: m.name ?? "" }
        }
        if (m.input !== undefined && m.input !== null) {
          if (typeof m.input === "string") {
            if (m.input) yield { kind: "toolArgs", text: m.input }
            said += m.input.length
          } else {
            const s = JSON.stringify(m.input)
            if (s !== "{}") yield { kind: "toolArgs", text: s }
            said += s.length
          }
        }
      } else if (kind === "metadataEvent" || kind === "messageMetadataEvent") {
        if (m.stopReason) stop = m.stopReason
        const tu = m.tokenUsage
        if (tu && typeof tu === "object") {
          usage.reported = true
          usage.input += tu.uncachedInputTokens || tu.inputTokens || 0
          usage.output += tu.outputTokens || 0
          usage.cacheRead += tu.cacheReadInputTokens || 0
          usage.cacheWrite += tu.cacheWriteInputTokens || 0
          if (tu.contextUsagePercentage > 0) pct = tu.contextUsagePercentage
        }
      } else if (kind === "contextUsageEvent") {
        if (m.contextUsagePercentage > 0) pct = m.contextUsagePercentage
      } else if (["error", "throttlingError", "validationError", "serviceUnavailableError", "internalServerException"].includes(kind)) {
        failed = exception(kind, f.payload)
        break
      }
    }
  } catch (e) {
    if (e?.name !== "AbortError") failed = "the reply broke off: " + e.message
  }
  yield* think.end()
  if (failed) {
    yield { kind: "error", text: failed }
    return
  }
  const window = windows.get(model) ?? 0
  // Kiro says how full the context is rather than how many tokens
  if (!usage.input && pct > 0 && window > 0) usage.input = Math.floor((pct / 100) * window)
  if (!usage.output) usage.output = Math.floor((said + 3) / 4)
  let reason = "end_turn"
  if (tools > 0) reason = "tool_use"
  else if (/^max_tokens$/i.test(stop)) reason = "max_tokens"
  else if (/^content_filtered$/i.test(stop)) reason = "refusal"
  yield { kind: "stop", reason, usage }
}

// finished is the message's blocks with each call's arguments read.
function finished(content) {
  for (const b of content)
    if (b.type === "tool_use") {
      try {
        b.input = b.input ? JSON.parse(b.input) : {}
      } catch {
        b.input = {}
      }
    }
  return content
}

// anthropicUsage is the usage in Messages' words. With no tokenUsage from
// Kiro the input is reckoned from how full the context is, and what of it
// Kiro read from its cache is unknown, not 0: the cache fields are left
// out and magpie is told so (magpie_cache_unknown), so it shows no hit
// rate rather than 0%.
const anthropicUsage = (u) =>
  u.reported
    ? { input_tokens: u.input, output_tokens: u.output, cache_read_input_tokens: u.cacheRead, cache_creation_input_tokens: u.cacheWrite }
    : { input_tokens: u.input, output_tokens: u.output, magpie_cache_unknown: true }

// quotaWords are magpie's (internal/gateway/fallback.go): how a vendor says
// "out of quota" or "slow down".
const QUOTA_WORDS = /quota|insufficient|balance|credit|billing|exceeded|rate.?limit|usage.?limit|limit.?reached|hit your .*limit|limit.{0,24}resets|too many requests|overloaded|余额|额度|欠费|限流|频率|套餐|用量|上限/i

// failedBefore is the reply to an error before any of the answer, as
// magpie's built-in relay gives it: a status, not a stream, so another
// account can take over — a 429 when it reads as a quota or rate limit,
// else a 502.
const failedBefore = (text) => errorResponse(QUOTA_WORDS.test(text) ? 429 : 502, text)

// reply answers the Messages request from Kiro's events: a stream of
// Messages' events, or one message. names gives back the caller's name of
// a tool Kiro was sent under another (toolNames).
async function reply(it, model, stream, names = new Map()) {
  const id = "msg_" + randomBytes(12).toString("hex")
  if (!stream) {
    const content = []
    let last = null
    const add = (block) => {
      content.push(block)
      last = block
    }
    for await (const e of it) {
      if (e.kind === "text") {
        if (last?.type === "text") last.text += e.text
        else add({ type: "text", text: e.text })
      } else if (e.kind === "think") {
        if (last?.type === "thinking") last.thinking += e.text
        else add({ type: "thinking", thinking: e.text, signature: "" })
      } else if (e.kind === "sig") {
        if (last?.type === "thinking") last.signature += e.text
      } else if (e.kind === "toolStart") add({ type: "tool_use", id: e.id, name: names.get(e.name) ?? e.name, input: "" })
      else if (e.kind === "toolArgs") {
        if (last?.type === "tool_use") last.input += e.text
      } else if (e.kind === "error") {
        // with none of the answer said, a status; else what was said
        if (!content.length) return failedBefore(e.text)
        return Response.json({ id, type: "message", role: "assistant", model, content: finished(content), stop_reason: "end_turn",
          stop_sequence: null, usage: anthropicUsage({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }) })
      } else if (e.kind === "stop") {
        return Response.json({ id, type: "message", role: "assistant", model, content: finished(content), stop_reason: e.reason, stop_sequence: null,
          usage: anthropicUsage(e.usage) })
      }
    }
    return errorResponse(502, "the reply ended before it was complete")
  }

  // an error before any of the answer is a status, as the built-in's
  const head = await it.next()
  if (head.value?.kind === "error") return failedBefore(head.value.text)
  let pending = head

  const enc = new TextEncoder()
  const sse = (type, data) => enc.encode(`event: ${type}\ndata: ${JSON.stringify({ type, ...data })}\n\n`)
  let index = -1
  let open = "" // the kind of block open
  let started = false
  const body = new ReadableStream({
    async pull(ctl) {
      if (!started) {
        started = true
        ctl.enqueue(sse("message_start", { message: { id, type: "message", role: "assistant", model, content: [], stop_reason: null,
          stop_sequence: null, usage: { input_tokens: 0, output_tokens: 0 } } }))
        return
      }
      const close = () => {
        if (open) ctl.enqueue(sse("content_block_stop", { index }))
        open = ""
      }
      const begin = (kind, block) => {
        close()
        index++
        open = kind
        ctl.enqueue(sse("content_block_start", { index, content_block: block }))
      }
      const r = pending ?? (await it.next())
      pending = null
      if (r.done) return ctl.close()
      const e = r.value
      switch (e.kind) {
        case "text":
          if (open !== "text") begin("text", { type: "text", text: "" })
          ctl.enqueue(sse("content_block_delta", { index, delta: { type: "text_delta", text: e.text } }))
          break
        case "think":
          if (open !== "thinking") begin("thinking", { type: "thinking", thinking: "", signature: "" })
          ctl.enqueue(sse("content_block_delta", { index, delta: { type: "thinking_delta", thinking: e.text } }))
          break
        case "sig":
          if (open === "thinking") ctl.enqueue(sse("content_block_delta", { index, delta: { type: "signature_delta", signature: e.text } }))
          break
        case "toolStart":
          begin("tool", { type: "tool_use", id: e.id, name: names.get(e.name) ?? e.name, input: {} })
          break
        case "toolArgs":
          if (open === "tool") ctl.enqueue(sse("content_block_delta", { index, delta: { type: "input_json_delta", partial_json: e.text } }))
          break
        case "error":
          close()
          ctl.enqueue(sse("error", { error: { type: "api_error", message: e.text } }))
          return ctl.close()
        case "stop":
          close()
          ctl.enqueue(sse("message_delta", { delta: { stop_reason: e.reason, stop_sequence: null }, usage: anthropicUsage(e.usage) }))
          ctl.enqueue(sse("message_stop", {}))
          return ctl.close()
      }
    },
    cancel() {
      it.return?.()
    },
  })
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" } })
}

// generate answers a Messages request through Kiro's API: once more,
// refreshed, when Kiro turns the token down (403). As the built-in: no
// credentials is a 401, a failed refresh after the 403 a 502; none marks
// the account.
async function generate(creds, auth, req, signal) {
  const model = String(req.model ?? "auto")
  const budget = thinking(req, model)
  let a
  try {
    a = await creds(auth)
  } catch (e) {
    return errorResponse(401, e.message)
  }
  let res
  try {
    res = await sendKiro(a, buildKiro(req, model, a.profile, budget), signal)
  } catch (e) {
    if (e?.name === "AbortError") throw e
    return errorResponse(502, e.message)
  }
  if (res.status === 403) {
    await res.body?.cancel()
    try {
      a = await creds(auth, true)
    } catch (e) {
      return errorResponse(502, e.message)
    }
    try {
      res = await sendKiro(a, buildKiro(req, model, a.profile, budget), signal)
    } catch (e) {
      if (e?.name === "AbortError") throw e
      return errorResponse(502, e.message)
    }
  }
  if (!res.ok) {
    // Kiro's own 401 is passed on, as the built-in did, the sign-in kept
    const f = failure(res.status, (await res.text()).slice(0, 1 << 20))
    return errorResponse(f.status, f.message)
  }
  return kept(await reply(events(res.body, model, budget), model, req.stream === true, toolNames(req)))
}

// ---- signing in ----------------------------------------------------------------------

function page(ok, title, text) {
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c])
  return `<!doctype html><meta charset="utf-8"><title>${esc(title)}</title>
<style>body{font:15px system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;margin:0;color:#222;background:#fafafa}
@media (prefers-color-scheme:dark){body{color:#eee;background:#161616}}main{text-align:center;max-width:28rem;padding:1rem}
.d{font-size:2rem;color:${ok ? "#2a9d5c" : "#c0392b"}}</style>
<main><div class="d">${ok ? "✓" : "✕"}</div><h1>${esc(title)}</h1><p>${esc(text)}</p></main>`
}

// signInPost posts JSON to Kiro's auth service or AWS's sign-in.
async function signInPost(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", "User-Agent": IDE_UA },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(30_000),
  })
  const text = await res.text()
  if (!res.ok) {
    let e = {}
    try {
      e = JSON.parse(text)
    } catch {}
    throw new Error(`the sign-in was refused (${res.status}): ${e.error_description || e.message || e.error || res.statusText}`)
  }
  return JSON.parse(text)
}

const expiry = (s) => Date.now() + (s > 0 ? s : 3600) * 1000

// whoIs names a new sign-in with the email Kiro has for it, and its plan.
async function whoIs(c) {
  try {
    const profile = c.profile || (await profileOf(c))
    const a = { token: c.access, tokenType: tokenType(c), profile, region: regionOf(profile, c.region) }
    const l = await usageLimits(a, true)
    return { profile, email: l?.userInfo?.email ?? "", plan: planName(l?.subscriptionInfo?.subscriptionTitle) }
  } catch {
    return { profile: c.profile ?? "", email: "", plan: "" }
  }
}

async function listen() {
  for (const port of CALLBACK_PORTS) {
    const server = createServer()
    const ok = await new Promise((resolve) => {
      server.once("error", () => resolve(false))
      server.listen(port, "127.0.0.1", () => resolve(true))
    })
    if (ok) return { server, port }
  }
  throw new Error("the ports Kiro's sign-in comes back to are all busy; close the Kiro IDE's sign-in and try again")
}

// browserSignIn is the IDE's: Kiro's sign-in page, back to a port here.
async function browserSignIn() {
  const { server, port } = await listen()
  const state = randomBytes(24).toString("base64url")
  const verifier = randomBytes(48).toString("base64url")
  const redirect = `http://localhost:${port}`
  const aws = {} // once Kiro's page has sent the browser on to AWS
  const awsRedirect = `http://127.0.0.1:${port}/oauth/callback`
  let waiting = true
  let settle
  const done = new Promise((r) => (settle = r))
  const finish = (result) => {
    if (!waiting) return
    waiting = false
    settle(result)
  }

  server.on("request", async (req, res) => {
    const url = new URL(req.url ?? "/", redirect)
    const html = (code, body) => {
      res.writeHead(code, { "Content-Type": "text/html; charset=utf-8" })
      res.end(body)
    }
    if (url.pathname !== "/oauth/callback" && url.pathname !== "/signin/callback") return res.writeHead(404).end()
    const q = url.searchParams
    if (!waiting) return html(200, page(false, "This sign-in is over", "Start it again."))
    const fail = (msg) => {
      finish({ type: "failed", error: msg })
      html(200, page(false, "Sign-in didn't finish", msg))
    }
    if (q.get("error")) return fail(q.get("error_description") || q.get("error"))
    let c
    try {
      if (aws.state && q.get("state") === aws.state) {
        // back from AWS
        if (!q.get("code")) return fail("AWS sent back no code")
        const t = await signInPost(oidc(aws.region) + "/token", { clientId: aws.clientId, clientSecret: aws.clientSecret,
          grantType: "authorization_code", redirectUri: awsRedirect, code: q.get("code"), codeVerifier: aws.verifier })
        if (!t.accessToken) throw new Error("AWS sent back no token")
        c = { access: t.accessToken, refresh: t.refreshToken ?? "", expires: expiry(t.expiresIn), method: "idc", loginProvider: aws.provider,
          region: aws.region, clientId: aws.clientId, clientSecret: aws.clientSecret, profile: "", builderID: isBuilderID(aws.provider) }
      } else if (q.get("state") !== state) {
        // not ours: someone else's page, or a stale tab
        return html(200, page(false, "This link isn't from this sign-in", "Start it again."))
      } else {
        const opt = q.get("login_option") ?? ""
        if (opt === "google" || opt === "github") {
          const t = await signInPost(AUTH_SERVICE + "/oauth/token", { code: q.get("code"), code_verifier: verifier,
            redirect_uri: redirect + url.pathname + "?login_option=" + opt })
          if (!t.accessToken) throw new Error("Kiro sent back no token")
          c = { access: t.accessToken, refresh: t.refreshToken ?? "", expires: expiry(t.expiresIn), method: "social",
            loginProvider: opt === "google" ? "Google" : "Github", region: "us-east-1", profile: t.profileArn ?? "" }
        } else if (opt === "builderid" || opt === "awsidc" || opt === "internal") {
          // on to AWS, with a client registered for this sign-in
          const issuer = q.get("issuer_url")
          const region = q.get("idc_region")
          if (!issuer || !region) return fail("Kiro's page didn't say where to sign in at AWS")
          const reg = await signInPost(oidc(region) + "/client/register", { clientName: "Kiro IDE", clientType: "public", scopes: SCOPES,
            grantTypes: ["authorization_code", "refresh_token"], redirectUris: ["http://127.0.0.1/oauth/callback"], issuerUrl: issuer })
          if (!reg.clientId) throw new Error("AWS registered no client")
          Object.assign(aws, { region, clientId: reg.clientId, clientSecret: reg.clientSecret,
            provider: { builderid: "BuilderId", awsidc: "Enterprise", internal: "Internal" }[opt],
            state: randomBytes(24).toString("base64url"), verifier: randomBytes(48).toString("base64url") })
          const a = new URLSearchParams({ response_type: "code", client_id: aws.clientId, redirect_uri: awsRedirect, scopes: SCOPES.join(","),
            state: aws.state, code_challenge: createHash("sha256").update(aws.verifier).digest("base64url"), code_challenge_method: "S256" })
          res.writeHead(302, { Location: `${oidc(region)}/authorize?${a}` })
          return res.end()
        } else if (opt === "external_idp") {
          return fail("A company's own identity provider can't be signed in to here yet; sign in with `kiro-cli login`, and use Kiro CLI's sign-in")
        } else return fail(`Kiro's page came back with a sign-in this plugin doesn't know (${JSON.stringify(opt)})`)
      }
    } catch (e) {
      return fail(e.message)
    }
    const who = await whoIs(c)
    const user = who.email ? nameOf(who.email, c.loginProvider) : "Kiro account"
    finish({ type: "success", provider: ID, refresh: c.refresh, access: c.access, expires: c.expires, accountId: user,
      ...(uidOf(c.loginProvider) ? { uid: uidOf(c.loginProvider) } : {}),
      method: c.method, loginProvider: c.loginProvider, region: c.region, profileArn: who.profile,
      ...(c.clientId ? { clientId: c.clientId, clientSecret: c.clientSecret } : {}), ...(who.plan ? { plan: who.plan } : {}) })
    html(200, page(true, "You're signed in", `${user} is signed in. You can close this tab.`))
  })

  const timer = setTimeout(() => finish({ type: "failed", error: "the sign-in timed out" }), SIGN_IN_TIMEOUT)
  done.then(() => {
    clearTimeout(timer)
    setTimeout(() => server.close(), 10_000).unref?.()
  })
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  const q = new URLSearchParams({ state, code_challenge: challenge, code_challenge_method: "S256", redirect_uri: redirect, redirect_from: "KiroIDE" })
  return {
    url: `${PORTAL}/signin?${q}`,
    instructions: "Sign in to Kiro with Google, GitHub, AWS Builder ID or IAM Identity Center.",
    method: "auto",
    callback: () => done,
  }
}

// ownSignIn uses kiro-cli's sign-in, else the Kiro IDE's: nothing is copied,
// they are read where they keep it each time.
async function ownSignIn() {
  return {
    url: "",
    instructions: "Uses the account kiro-cli or the Kiro IDE is signed in to.",
    method: "auto",
    callback: async () => {
      const cli = await readCLI()
      const c = cli ?? (await readIDE())
      if (!c) return { type: "failed", error: "neither kiro-cli nor the Kiro IDE is signed in" }
      const who = fresh(c) ? await whoIs(c) : { email: "", plan: "" }
      return { type: "success", provider: ID, refresh: "", access: "", expires: 0, source: cli ? "kiro-cli" : "kiro-ide",
        accountId: who.email || (cli ? "kiro-cli's account" : "Kiro IDE's account"), ...(who.plan ? { plan: who.plan } : {}) }
    },
  }
}

// ---- the plugin ------------------------------------------------------------------------

async function bodyOf(input, init) {
  const b = init.body ?? (input instanceof Request ? await input.clone().text() : undefined)
  return JSON.parse(typeof b === "string" ? b : new TextDecoder().decode(b))
}

export async function KiroAuthPlugin({ client } = {}) {
  const creds = account(client)
  return {
    auth: {
      provider: ID,
      // magpie renews a sign-in kept here LEAD_MS before its end, once for
      // the account, before its requests, models and usage ask for it; the
      // check before each request stays for OpenCode, which doesn't call
      // this. kiro-cli's and the IDE's sign-ins have no end magpie knows
      // (expires 0) and are never asked for.
      refreshLead: LEAD_MS,
      // A failure is thrown as it is, a refusal too: the built-in marked
      // no Kiro account lapsed (see gone).
      async refresh(auth) {
        if (auth?.type !== "oauth" || auth.source || !auth.access || !auth.refresh) return undefined
        return creds.renew(auth)
      },
      async loader(getAuth) {
        const auth = await getAuth()
        if (!(auth?.type === "api" && auth.key) && auth?.type !== "oauth") return {}
        return {
          baseURL: BASE,
          apiKey: "kiro", // the fetch signs each request itself
          // every Messages request answered through Kiro's own API
          async fetch(input, init = {}) {
            const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
            if (!/\/messages$/.test(new URL(url).pathname))
              return errorResponse(404, `this plugin only answers Anthropic Messages requests, not ${new URL(url).pathname}`)
            let req
            try {
              req = await bodyOf(input, init)
            } catch {
              return errorResponse(400, "a request that isn't JSON")
            }
            return generate(creds, (await getAuth()) ?? auth, req, init.signal)
          },
        }
      },
      // the account's credits, as magpie's built-in showed them; a read,
      // clean or failed, marked the built-in's account nothing
      async usage(getAuth) {
        const auth = await getAuth()
        if (!(auth?.type === "api" && auth.key) && auth?.type !== "oauth") return { error: "not signed in", signIn: "kept" }
        try {
          const u = usageOf(await usageLimits(await creds(auth), true))
          // named as the account is
          if (u.user && auth.type === "oauth" && !auth.source) u.user = nameOf(u.user, auth.loginProvider)
          return { ...u, signIn: "kept" }
        } catch (e) {
          return { error: e.message, signIn: "kept" }
        }
      },
      methods: [
        { type: "oauth", label: "Kiro (Google, GitHub, AWS Builder ID, IAM Identity Center)", authorize: browserSignIn },
        { type: "oauth", label: "Kiro CLI's or Kiro IDE's sign-in", authorize: ownSignIn },
        { type: "api", label: "Kiro API key (ksk_…)" },
      ],
    },
    async config(config) {
      config.provider ??= {}
      const was = config.provider[ID] ?? {}
      config.provider[ID] = {
        name: "Kiro",
        npm: MESSAGES,
        api: BASE,
        ...was,
        models: { ...Object.fromEntries(MODELS.map((m) => [m.id, configModel(m)])), ...(was.models ?? {}) },
      }
    },
    // the account's own list, the default first
    provider: {
      id: ID,
      async models(provider, { auth } = {}) {
        if (!(auth?.type === "api" && auth.key) && auth?.type !== "oauth") return provider.models
        try {
          const list = await listModels(await creds(auth))
          return Object.fromEntries(list.map((m) => [m.id, runtimeModel(m)]))
        } catch {
          return provider.models
        }
      },
    },
  }
}

// for tests
export const _internal = { generate, refresh, usageOf, buildKiro, events, reply, failure, toolID, toolName, toolNames, thinking, configModel, runtimeModel, readCLI, readIDE, regionOf, planName, frames }
