// Xiaomi MiMo, as the MiMo desktop app signs in to it: a Xiaomi account
// signed in on account.xiaomi.com's long-poll page (a QR code for the
// Xiaomi phone app, or the password), whose passToken signs it on at the
// MiMo server, which answers with session cookies. Model requests are
// chat completions at the server's /route, carrying those cookies. An
// account with no app membership but a Token Plan at the open platform
// (the server answers membership_required) is asked at the Token Plan's
// endpoint instead, with the plan's tp- key, which the same passToken
// reads from the platform.
import { randomBytes } from "node:crypto"
import { STATUS_CODES } from "node:http"

const ID = "mimo-app"
const HOSTS = {
  SGP: "https://mimo-server-sgp.xiaomimimo.com/api",
  RU: "https://mimo-server-ru.xiaomimimo.com/api",
  IN: "https://mimo-server-in.xiaomimimo.com/api",
}
const REGION = "SGP"
const ACCOUNT = "https://account.xiaomi.com"
const APP_VERSION = "26.929.292248"
const SOURCE = "mimocode-cli-free"
const UA = "miNative PC/Normal Windows_NT/10.0.26100 SDKV/1.0.0 DEVT/PC DEVS/Windows APP/miaccount_desktop APPV/0.1.0"
const RENEW_AFTER = 24 * 60 * 60 * 1000 // the app signs on again after a day
const LEAD_MS = 10 * 60 * 1000 // this close to it, magpie signs on again ahead of time (auth.refresh)
const SIGN_IN_LIFE = 10 * 60 * 1000

const MODEL = { release_date: "2026-07-01", attachment: true, tool_call: true, limit: { context: 1_000_000, output: 128_000 }, modalities: { input: ["text", "image"], output: ["text"] } }
const MODELS = {
  "mimo-pro": { name: "MiMo Pro", ...MODEL },
  "mimo-flash": { name: "MiMo Flash", ...MODEL },
}

// the Token Plan's names for the app's models; any other passes as it is
const TP_MODELS = { "mimo-pro": "mimo-v2.6-pro", "mimo-flash": "mimo-v2.6-flash" }
const TP_LIFE = 60 * 60 * 1000 // this long, an account the app refused is asked at its Token Plan

const baseOf = (region) => HOSTS[String(region ?? "").trim().toUpperCase()] ?? ""
const deviceId = () => "pc_" + randomBytes(16).toString("hex")
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// the account server's JSON comes after a "&&&START&&&" guard
function accountJSON(text) {
  try {
    return JSON.parse(text.trim().replace(/^&&&START&&&/, ""))
  } catch {
    return null
  }
}

// ---- a cookie jar, enough for the sign-on's redirects -----------------------

class Jar {
  constructor() {
    this.cookies = []
  }
  put(c) {
    this.cookies = this.cookies.filter((o) => !(o.name === c.name && o.domain === c.domain && o.path === c.path))
    if (!c.gone) this.cookies.push(c)
  }
  seed(url, name, value) {
    this.put({ name, value, domain: new URL(url).hostname.toLowerCase(), hostOnly: false, path: "/", secure: false })
  }
  take(res, url) {
    const u = new URL(url)
    const lines = typeof res.headers.getSetCookie === "function" ? res.headers.getSetCookie() : []
    for (const line of lines) {
      const [pair, ...attrs] = line.split(";")
      const eq = pair.indexOf("=")
      if (eq <= 0) continue
      const c = { name: pair.slice(0, eq).trim(), value: pair.slice(eq + 1).trim(), domain: u.hostname.toLowerCase(), hostOnly: true, secure: false }
      const dir = u.pathname.lastIndexOf("/")
      c.path = dir > 0 ? u.pathname.slice(0, dir) : "/"
      for (const a of attrs) {
        const i = a.indexOf("=")
        const k = (i < 0 ? a : a.slice(0, i)).trim().toLowerCase()
        const v = i < 0 ? "" : a.slice(i + 1).trim()
        if (k === "domain" && v) {
          const d = v.replace(/^\./, "").toLowerCase()
          if (c.domain === d || c.domain.endsWith("." + d)) Object.assign(c, { domain: d, hostOnly: false })
        } else if (k === "path" && v.startsWith("/")) c.path = v
        else if (k === "secure") c.secure = true
        else if (k === "max-age" && Number(v) <= 0) c.gone = true
        else if (k === "expires" && Date.parse(v) <= Date.now()) c.gone = true
      }
      this.put(c)
    }
  }
  for(url) {
    const u = new URL(url)
    const host = u.hostname.toLowerCase()
    return this.cookies.filter((c) => {
      if (c.secure && u.protocol !== "https:") return false
      if (c.hostOnly ? host !== c.domain : host !== c.domain && !host.endsWith("." + c.domain)) return false
      return u.pathname === c.path || u.pathname.startsWith(c.path.endsWith("/") ? c.path : c.path + "/") || c.path === "/"
    })
  }
  header(url) {
    return this.for(url).map((c) => `${c.name}=${c.value}`).join("; ")
  }
}

// ---- the session ------------------------------------------------------------

class Lapsed extends Error {}

const appHeaders = () => ({ "User-Agent": UA, "X-Client-Version": APP_VERSION })

// session signs the account on at a MiMo server as the app's window does:
// the server's /user/xiaomi/me, asked with no session, sends the browser to
// account.xiaomi.com's serviceLogin, which (holding the passToken) sends it
// back through the server's /sts, where the session cookies are set, to
// /user/xiaomi/me again. The server's cookies are the session.
async function session(creds, base) {
  const jar = new Jar()
  for (const [k, v] of [["userId", creds.userId], ["passToken", creds.passToken], ["cUserId", creds.cUserId],
    ["deviceId", creds.deviceId], ["pass_ua", "pc"], ["uLocale", "zh_CN"]]) {
    if (v) jar.seed(ACCOUNT, k, v)
  }
  const bu = new URL(base)
  const signal = AbortSignal.timeout(30_000)
  let url = base.replace(/\/+$/, "") + "/user/xiaomi/me"
  let res
  for (let hop = 0; ; hop++) {
    if (hop > 10) throw new Error("Xiaomi MiMo sign-in: too many redirects")
    const headers = appHeaders()
    const cookie = jar.header(url)
    if (cookie) headers.Cookie = cookie
    try {
      res = await fetch(url, { headers, redirect: "manual", signal })
    } catch (e) {
      throw new Error("Xiaomi MiMo sign-in: " + (e?.message ?? e))
    }
    jar.take(res, url)
    const loc = res.headers.get("location")
    if (res.status < 300 || res.status >= 400 || !loc) break
    await res.arrayBuffer().catch(() => {})
    const next = new URL(loc, url)
    // the server's followup is written http://; its session cookies are
    // only sent back over https
    if (next.protocol === "http:" && bu.protocol === "https:" && next.host.toLowerCase() === bu.host.toLowerCase()) next.protocol = "https:"
    url = next.href
  }
  const text = await res.text()
  let me
  try {
    me = JSON.parse(text)
  } catch {
    // Xiaomi's sign-in page, not the server's answer: the passToken no
    // longer signs the account on
    throw new Lapsed("Xiaomi MiMo: the Xiaomi sign-in has lapsed; sign in again")
  }
  if (res.status === 403 || me?.code === 403 || me?.code === 46109) {
    throw new Error("Xiaomi MiMo doesn't serve this Xiaomi account (its region isn't served here)")
  }
  if (me?.code !== 0 || !String(me?.data?.userId ?? "")) throw new Lapsed("Xiaomi MiMo: the Xiaomi sign-in has lapsed; sign in again")
  const cookies = {}
  for (const c of jar.for(bu.href)) cookies[c.name] = c.value
  if (!Object.keys(cookies).length) throw new Error("Xiaomi MiMo sign-in: the server set no session")
  return { cookies, me: me.data }
}

// The open platform (platform.xiaomimimo.com) sells the Token Plan, a plan
// of its own spent with its tp- API key at token-plan-<region>.xiaomimimo.com,
// not through the app's /route. The same Xiaomi account signs on there as at
// the MiMo server: a page asked with no session answers 401 with a
// serviceLogin URL (sid api-platform), which the passToken follows back
// through the platform's /sts to the page.
const PLATFORM = "https://platform.xiaomimimo.com"

// platformPage is one of the platform's /api/v1 pages, as {cookies, data}:
// cookies the session it was read with (given in, else signed on afresh),
// data the page's; null data is a page the account can't read.
async function platformPage(creds, path, cookies) {
  const signal = AbortSignal.timeout(20_000)
  const url = PLATFORM + "/api/v1" + path
  const ask = async (c) => {
    const res = await fetch(url, { headers: c ? { Cookie: c } : {}, redirect: "manual", signal })
    return { res, text: await res.text() }
  }
  const data = (text) => {
    try {
      const v = JSON.parse(text)
      return v?.code === 0 ? (v.data ?? null) : undefined
    } catch {}
  }
  if (cookies) {
    const { res, text } = await ask(cookieHeader(cookies))
    const d = res.status === 200 ? data(text) : undefined
    if (d !== undefined) return { cookies, data: d }
  }
  let { res, text } = await ask("")
  let login
  try {
    login = JSON.parse(text)?.loginUrl
  } catch {}
  if (res.status !== 401 || typeof login !== "string" || !login.startsWith(ACCOUNT + "/")) {
    throw new Error(`Xiaomi MiMo platform ${path}: ${vendorError(text, statusLine(res.status))}`)
  }
  const jar = new Jar()
  for (const [k, v] of [["userId", creds.userId], ["passToken", creds.passToken], ["cUserId", creds.cUserId],
    ["deviceId", creds.deviceId], ["pass_ua", "pc"], ["uLocale", "zh_CN"]]) {
    if (v) jar.seed(ACCOUNT, k, v)
  }
  let at = login
  for (let hop = 0; ; hop++) {
    if (hop > 10) throw new Error("Xiaomi MiMo platform sign-in: too many redirects")
    const c = jar.header(at)
    res = await fetch(at, { headers: c ? { Cookie: c } : {}, redirect: "manual", signal })
    jar.take(res, at)
    const loc = res.headers.get("location")
    if (res.status < 300 || res.status >= 400 || !loc) break
    await res.arrayBuffer().catch(() => {})
    const next = new URL(loc, at)
    // the followup is written http://; the session's cookies go back over https
    if (next.protocol === "http:" && next.host === new URL(PLATFORM).host) next.protocol = "https:"
    at = next.href
  }
  text = await res.text()
  const d = res.status === 200 ? data(text) : undefined
  // back on Xiaomi's sign-in page: the passToken doesn't sign on here
  if (d === undefined) throw new Error(`Xiaomi MiMo platform ${path}: the Xiaomi account didn't sign on (${statusLine(res.status)})`)
  const got = {}
  for (const c of jar.for(PLATFORM + "/")) got[c.name] = c.value
  return { cookies: got, data: d }
}

// cookieHeader is a session as a Cookie header, in a steady order
function cookieHeader(cookies) {
  const first = ["serviceToken", "userId", "cUserId"]
  const parts = first.filter((k) => k in cookies).map((k) => `${k}=${cookies[k]}`)
  for (const [k, v] of Object.entries(cookies)) if (!first.includes(k)) parts.push(`${k}=${v}`)
  return parts.join("; ")
}

// The sign-in is kept as OpenCode keeps an OAuth one: refresh is the
// Xiaomi account (what signs it on again), access the session's cookies,
// expires when the app would sign on again.
function toAuth(creds, cookies, issued) {
  return {
    type: "oauth",
    refresh: JSON.stringify(creds),
    access: JSON.stringify(cookies),
    expires: issued + RENEW_AFTER,
    accountId: creds.userId,
  }
}

function fromAuth(auth) {
  if (auth?.type !== "oauth") return null
  try {
    const creds = JSON.parse(auth.refresh)
    let cookies = {}
    try {
      cookies = JSON.parse(auth.access || "{}") ?? {}
    } catch {}
    if (!creds?.passToken || !creds?.userId) return null
    creds.base ||= baseOf(creds.region) || HOSTS[REGION]
    return { creds, cookies, expires: Number(auth.expires) || 0 }
  } catch {
    return null
  }
}

// said is an answer with magpie's X-Magpie-Sign-In, which tells what it
// means for the account's sign-in whatever its status: "expired" marks it
// lapsed, "kept" leaves it be. magpie takes it off before the agent sees it.
function said(res, v) {
  const h = new Headers(res.headers)
  h.set("X-Magpie-Sign-In", v)
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers: h })
}

// ---- the sign-in ------------------------------------------------------------

// serviceOf is the sid and callback the MiMo server sends a browser with
// no session to account.xiaomi.com with
async function serviceOf(base) {
  let sid = "mimosgp"
  let callback = base.replace(/\/+$/, "") + "/sts"
  try {
    const res = await fetch(base.replace(/\/+$/, "") + "/user/xiaomi/me", { headers: appHeaders(), redirect: "manual", signal: AbortSignal.timeout(15_000) })
    await res.arrayBuffer().catch(() => {})
    const loc = res.headers.get("location")
    if (loc) {
      const q = new URL(loc, base).searchParams
      sid = q.get("sid") || sid
      callback = q.get("callback") || callback
    }
  } catch {}
  return { sid, callback }
}

async function accountGet(url, device, signal) {
  const res = await fetch(url, { headers: { "User-Agent": UA, Cookie: `deviceId=${device}; pass_ua=pc; uLocale=zh_CN` }, signal })
  return { status: res.status, text: await res.text() }
}

async function startSignIn() {
  const base = HOSTS[REGION]
  const device = deviceId()
  const { sid, callback } = await serviceOf(base)
  const q = new URLSearchParams({
    _group: "DEFAULT",
    _qrsize: "240",
    qs: "%3Fsid%3D" + encodeURIComponent(sid) + "%26_json%3Dtrue",
    callback,
    _hasLogo: "false",
    sid,
    serviceParam: "",
    _locale: "en_US",
  })
  const { status, text } = await accountGet(`${ACCOUNT}/longPolling/loginUrl?${q}`, device, AbortSignal.timeout(20_000))
  const lp = accountJSON(text)
  if (!lp || lp.code !== 0 || !lp.loginUrl || !lp.lp) {
    throw new Error(`Xiaomi MiMo sign-in: account.xiaomi.com answered ${status} ${lp?.desc || text.trim().slice(0, 200)}`)
  }
  const life = lp.timeout > 0 ? Math.min(lp.timeout * 1000, SIGN_IN_LIFE) : SIGN_IN_LIFE
  return { url: lp.loginUrl, lp: lp.lp, device, until: Date.now() + life }
}

// waitPoll asks the ticket's long poll until Xiaomi says the account is in
async function waitPoll(t) {
  while (Date.now() < t.until) {
    try {
      const left = t.until - Date.now()
      const { status, text } = await accountGet(t.lp, t.device, AbortSignal.timeout(Math.min(70_000, Math.max(left, 1))))
      const p = status === 200 ? accountJSON(text) : null
      if (p?.passToken && String(p.userId ?? "")) return p
    } catch {}
    await sleep(1000)
  }
  throw new Error("the Xiaomi sign-in page expired before the sign-in finished; start it again")
}

async function signedInWith(p, device) {
  let creds = { userId: String(p.userId), cUserId: p.cUserId ?? "", passToken: p.passToken, deviceId: device, region: REGION, base: HOSTS[REGION] }
  let s
  try {
    s = await session(creds, creds.base)
  } catch (e) {
    if (e instanceof Lapsed) throw new Error("Xiaomi signed the account in, but the MiMo server didn't take it; try again")
    throw e
  }
  // an account of another region is served by that region's server
  const r = String(s.me?.region ?? "").trim().toUpperCase()
  const nb = baseOf(r)
  if (r && r !== creds.region && nb && nb !== creds.base) {
    try {
      s = await session(creds, nb)
      creds = { ...creds, region: r, base: nb }
    } catch {}
  }
  return { creds: { ...creds, userId: String(s.me?.userId ?? "") || creds.userId }, cookies: s.cookies }
}

// ---- usage ------------------------------------------------------------------
//
// What the account says of its allowance, as magpie's built-in MiMo
// account shows it (internal/provider/mimo_usage.go), from the pages the
// app's "Usage & billing" reads: /user/usage, how much of the week's
// allowance is left (percent remaining, and the day it resets; no reset
// date is no plan), and /user/xiaomi/subscription/self, the plan in force
// and when it ends. An account with no plan is on MiMo's free offer.

// credits is a count of the Token Plan's credits, short: 6.81M, 4.1B
function credits(n) {
  for (const [d, u] of [[1e9, "B"], [1e6, "M"], [1e3, "K"]]) {
    if (n >= d) return `${Number((n / d).toPrecision(3))}${u}`
  }
  return String(n)
}

// the app's names for its plans' tiers
const TIERS = { 1: "Starter", 2: "Plus", 3: "Pro", 4: "Ultra" }

// SignInGone is an account Xiaomi no longer signs in: it must be signed in
// again.
class SignInGone extends Error {}

// statusLine is a status as Go's HTTP client names it, "502 Bad Gateway".
const statusLine = (status) => `${status} ${STATUS_CODES[status] ?? ""}`.trim()

// vendorError is the message in an error body as magpie reads it: its
// {error: {message}} or {error}, {message}, {msg}, {detail}, else the body
// cut short after the status.
function vendorError(text, fallback) {
  let v
  try {
    v = JSON.parse(text)
  } catch {}
  if (v && typeof v === "object" && !Array.isArray(v)) {
    const err = v.detail !== undefined && v.detail !== null ? v.detail : v.error
    if (Array.isArray(v.errors) && typeof v.errors[0]?.message === "string" && v.errors[0].message) return v.errors[0].message
    if (err && typeof err === "object" && typeof err.message === "string" && err.message) return err.message
    if (typeof err === "string" && err) return err
    const m = (typeof v.message === "string" && v.message) || (typeof v.msg === "string" && v.msg)
    if (m) return m
  }
  const s = String(text ?? "").split(/\s+/).filter(Boolean).join(" ")
  if (!s || s.startsWith("<")) return fallback
  const r = [...s]
  return fallback + ": " + (r.length > 300 ? r.slice(0, 300).join("") + "…" : s)
}

// serverTime reads one of the server's times: "2026-10-01T00:00:00", or a
// day alone. They carry no zone, and the app shows them as they come,
// which is Beijing's (and Singapore's) time.
function serverTime(v) {
  const s = String(v ?? "").trim()
  let iso = ""
  if (/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/i.test(s)) iso = s
  else if (/^\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(\.\d+)?$/.test(s)) iso = s.replace(" ", "T") + "+08:00"
  else if (/^\d{4}-\d\d-\d\d$/.test(s)) iso = s + "T00:00:00+08:00"
  const t = iso ? Date.parse(iso) : NaN
  return isNaN(t) ? undefined : new Date(t).toISOString()
}

// ---- the plugin -------------------------------------------------------------

export const MimoAuthPlugin = async ({ client }) => {
  // renewals under way, one to an account: two accounts signing on at
  // once each keep their own session. renewed is the last session each
  // account got here, so magpie's auth.refresh handed a sign-in from
  // before it gives that one rather than signing on again.
  const renewing = new Map()
  const renewed = new Map()

  const save = async (auth) => {
    try {
      await client?.auth?.set?.({ path: { id: ID }, body: auth })
    } catch {}
  }

  // renew signs the account on again, once at a time: those who come
  // while it runs wait for it. keep saves the new session (the request
  // path); magpie's auth.refresh saves what it gives itself.
  const renew = (a, keep) => {
    const who = String(a.creds.userId ?? "")
    let r = renewing.get(who)
    if (!r) {
      r = (async () => {
        const s = await session(a.creds, a.creds.base)
        const issued = Date.now()
        const got = { creds: a.creds, cookies: s.cookies, expires: issued + RENEW_AFTER, renewed: true }
        renewed.set(who, got)
        if (keep) await save(toAuth(a.creds, s.cookies, issued))
        return got
      })().finally(() => {
        renewing.delete(who)
      })
      renewing.set(who, r)
    }
    return r
  }

  // fresh is the account with a live session, signed on again when it is
  // older than the app lets one get, or when force is set (the server
  // turned the last one away); renewed when it was signed on again, which
  // took the built-in's lapse mark off
  const fresh = async (getAuth, force) => {
    const a = fromAuth(await getAuth())
    if (!a) throw new Error("Xiaomi MiMo: not signed in")
    if (!force && Object.keys(a.cookies).length && Date.now() < a.expires) return a
    try {
      return await renew(a, true)
    } catch (e) {
      // a hiccup: the session in hand may still do
      if (!(e instanceof Lapsed) && !force && Object.keys(a.cookies).length) return a
      throw e
    }
  }

  // refresh is magpie's auth.refresh: the session signed on again
  // LEAD_MS before the app would, as the fields that changed (the Xiaomi
  // account in refresh stays as it is). A passToken Xiaomi no longer takes
  // is signIn "expired"; any other failure throws as it is.
  const refresh = async (auth) => {
    const a = fromAuth(auth)
    if (!a) return undefined
    const last = renewed.get(String(a.creds.userId ?? ""))
    // signed on again here already, the store not yet saying so
    if (last && last.expires > a.expires && Date.now() < last.expires - LEAD_MS) {
      return { access: JSON.stringify(last.cookies), expires: last.expires }
    }
    try {
      const s = await renew(a, false)
      return { access: JSON.stringify(s.cookies), expires: s.expires }
    } catch (e) {
      if (e instanceof Lapsed) throw Object.assign(new Error(e.message), { signIn: "expired" })
      throw e
    }
  }

  // page asks the MiMo server for one of the account's pages, signing on
  // again once when it is turned away, and gives its data. read.signIn is
  // what that did to the built-in's lapse mark: signed on again took it
  // off, a sign-in Xiaomi no longer takes (mimoLapse) put it on.
  const page = async (getAuth, path, read) => {
    for (let t = 0; ; t++) {
      let s
      try {
        s = await fresh(getAuth, t > 0)
      } catch (e) {
        if (e instanceof Lapsed) {
          read.signIn = "expired"
          throw new SignInGone(`${fromAuth(await getAuth())?.creds.userId}: the Xiaomi MiMo sign-in has expired — sign in again`)
        }
        throw e
      }
      if (s.renewed) read.signIn = "renewed"
      const res = await fetch(s.creds.base.replace(/\/+$/, "") + path, {
        headers: { Cookie: cookieHeader(s.cookies), ...appHeaders() },
        signal: AbortSignal.timeout(20_000),
      })
      const text = await res.text()
      let env
      try {
        env = JSON.parse(text)
      } catch {}
      const bad = !env || typeof env !== "object" || Array.isArray(env) || (env.code != null && !Number.isInteger(env.code)) || (env.msg != null && typeof env.msg !== "string")
      if (res.status === 401 || (res.status === 200 && bad)) {
        // a session gone stale, or a redirect to Xiaomi's sign-in
        if (t === 0) continue
        read.signIn = "expired"
        throw new SignInGone(`${s.creds.userId}: the Xiaomi MiMo sign-in has expired — sign in again`)
      }
      if (res.status !== 200 || bad) throw new Error(`Xiaomi MiMo ${path}: ${vendorError(text, statusLine(res.status))}`)
      if ((env.code ?? 0) !== 0) throw new Error(`Xiaomi MiMo ${path}: code ${env.code} ${env.msg ?? ""}`)
      return env.data
    }
  }

  // usage is the plan and the week's allowance, magpie's own hook; signIn
  // is the lapse mark as the built-in's reads left it, a clean read with
  // no signing on again leaving it be
  const usage = async (getAuth) => {
    const read = { signIn: "kept" }
    let self
    try {
      self = await page(getAuth, "/user/xiaomi/subscription/self", read)
    } catch (e) {
      return { error: e?.message ?? String(e), signIn: read.signIn }
    }
    const c = self?.current
    const out = { plan: "Free" }
    const app = c && typeof c === "object"
    const tp = await tokenPlan(getAuth, app)
    if (app) {
      const text = (v) => (typeof v === "string" ? v.trim() : "")
      out.plan = text(c.title) || TIERS[c.planTier] || text(c.planCode) || "MiMo"
      const until = serverTime(c.endTime)
      if (until) out.until = until
      if (c.renewalMode === "MONTHLY" || c.renewalMode === "YEARLY") out.renew = "auto"
      else if (c.renewalMode === "ONE_TIME") out.renew = "off"
    } else if (tp) {
      // no app plan: the account's plan is the platform's Token Plan
      out.plan = tp.plan
      if (tp.until) out.until = tp.until
      out.renew = tp.renew
    }
    let use
    try {
      use = await page(getAuth, "/user/usage", read)
    } catch (e) {
      if (e instanceof SignInGone) out.error = e.message
      // the plan alone, when the week's allowance can't be read
      return { ...out, ...(tp?.window && { windows: [tp.window] }), signIn: read.signIn }
    }
    out.signIn = read.signIn
    const percent = use?.percent
    const reset = use?.resetDate
    const windows = []
    // no plan: the free offer shows no allowance
    if (typeof percent === "number" && typeof reset === "string") {
      const w = { name: "7 days", used: Math.max(0, Math.min(100, 100 - percent)), span: 7 * 24 * 3600 }
      const at = serverTime(reset)
      if (at) w.resetsAt = at
      windows.push(w)
    }
    if (tp?.window) windows.push(tp.window)
    if (windows.length) out.windows = windows
    return out
  }

  // tokenPlan is the account's Token Plan at the open platform, as
  // {plan, until, renew, window}, or null: none, or the platform can't be
  // read (the app's card goes on without it). Its credits are spent by
  // its tp- key, not by this account's requests, so they are an aside.
  const platform = new Map() // userId -> the platform's session cookies
  const tokenPlan = async (getAuth, app) => {
    const a = fromAuth(await getAuth())
    if (!a) return null
    const who = String(a.creds.userId)
    try {
      const d = await platformPage(a.creds, "/tokenPlan/detail", platform.get(who))
      platform.set(who, d.cookies)
      const p = d.data
      const name = typeof p?.planName === "string" ? p.planName.trim() : ""
      if (!name || p.expired !== false) return null
      const out = { plan: `Token Plan ${name}`, renew: p.enableAutoRenew || p.hasAutoRenewSubscribed ? "auto" : "off" }
      // the platform's times are UTC ("有效期至 2026-11-06 23:59:59 (UTC)")
      const end = /^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$/.test(p.currentPeriodEnd ?? "") ? Date.parse(p.currentPeriodEnd.replace(" ", "T") + "Z") : NaN
      if (!isNaN(end)) out.until = new Date(end).toISOString()
      try {
        const u = await platformPage(a.creds, "/tokenPlan/usage", d.cookies)
        platform.set(who, u.cookies)
        const items = [...(u.data?.monthUsage?.items ?? []), ...(u.data?.usage?.items ?? [])]
        const it = items.find((x) => x?.name === "month_total_token") ?? items.find((x) => x?.name === "plan_total_token")
        if (Number.isFinite(it?.used) && Number(it?.limit) > 0) {
          out.window = {
            name: app ? "Token Plan · API key" : "Token Plan",
            used: Math.max(0, Math.min(100, (100 * it.used) / it.limit)),
            display: `${credits(it.used)} / ${credits(it.limit)} credits`,
          }
          // with an app plan, the account's requests go to the app: the
          // Token Plan's credits are its key's, an aside
          if (app) out.window.aside = true
          if (out.until) out.window.resetsAt = out.until
        }
      } catch {}
      return out
    } catch {
      platform.delete(who)
      return null
    }
  }

  // planKey is the account's Token Plan as {key, base, at}: its tp- key
  // and OpenAI base URL, or null when it has no live plan or no key (the
  // platform's page makes one; magpie doesn't make it for them)
  const planKeys = new Map() // userId -> {key, base, at}
  const planKey = async (a) => {
    const who = String(a.creds.userId)
    const had = planKeys.get(who)
    if (had && Date.now() - had.at < TP_LIFE) return had
    planKeys.delete(who)
    try {
      let c = platform.get(who)
      const read = async (path) => {
        const r = await platformPage(a.creds, path, c)
        c = r.cookies
        platform.set(who, c)
        return r.data
      }
      const p = await read("/tokenPlan/detail")
      if (!p?.planName || p.expired !== false) return null
      const k = await read("/tokenPlan/apiKey")
      const base = typeof k?.openaiBaseUrl === "string" && /^https:\/\/[^/]+\.xiaomimimo\.com\//.test(k.openaiBaseUrl) ? k.openaiBaseUrl.replace(/\/+$/, "") : ""
      if (!base) return null
      const key = await read("/tokenPlan/apiKey/raw")
      if (typeof key !== "string" || !key.startsWith("tp-")) return null
      const got = { key, base, at: Date.now() }
      planKeys.set(who, got)
      return got
    } catch {
      platform.delete(who)
      return null
    }
  }
  // onPlan is the accounts the app refused (membership_required), and when
  const onPlan = new Map()

  return {
    config: async (config) => {
      config.provider ??= {}
      const was = config.provider[ID] ?? {}
      config.provider[ID] = {
        name: "Xiaomi MiMo",
        npm: "@ai-sdk/openai-compatible",
        api: HOSTS[REGION] + "/route",
        ...was,
        models: { ...MODELS, ...(was.models ?? {}) },
      }
    },
    auth: {
      provider: ID,
      usage,
      // magpie signs the account on again LEAD_MS before the day is out,
      // once, before its requests, models and usage ask; fresh's check
      // before each request stays for OpenCode, which doesn't call this
      refreshLead: LEAD_MS,
      refresh,
      loader: async (getAuth) => {
        const a = fromAuth(await getAuth())
        if (!a) return {}
        return {
          baseURL: a.creds.base.replace(/\/+$/, "") + "/route",
          apiKey: "mimo", // the engine's placeholder; the app strips it
          async fetch(input, init = {}) {
            const req = input instanceof Request ? input : null
            const url = req ? req.url : String(input)
            let body = init.body ?? (req ? await req.text() : undefined)
            // a body handed over as bytes is read as the string it is
            if (body instanceof ArrayBuffer) body = new TextDecoder().decode(body)
            else if (ArrayBuffer.isView(body)) body = new TextDecoder().decode(new Uint8Array(body.buffer, body.byteOffset, body.byteLength))
            // mimo-auto, the app's default, is asked as the model it stands for
            if (typeof body === "string" && body.includes('"mimo-auto"')) {
              try {
                const j = JSON.parse(body)
                if (j?.model === "mimo-auto") body = JSON.stringify({ ...j, model: "mimo-pro" })
              } catch {}
            }
            let s
            try {
              s = await fresh(getAuth, false)
            } catch (e) {
              // the passToken no longer signs the account on: the built-in
              // failed the request (magpie's 502) in mimoLapse's words and
              // marked the account lapsed
              if (!(e instanceof Lapsed)) throw e
              const who = fromAuth(await getAuth())?.creds.userId ?? a.creds.userId
              const message = `${who}: the Xiaomi MiMo sign-in has expired — sign in again`
              return said(new Response(JSON.stringify({ error: { message, type: "api_error", code: null } }), { status: 502, headers: { "content-type": "application/json" } }), "expired")
            }
            const who = String(s.creds.userId)
            const method = init.method ?? req?.method ?? "POST"
            // the account's Token Plan, at its own endpoint with its key:
            // what the app's /route was asked for, as the plan names it
            const toPlan = async () => {
              const tp = await planKey(s)
              if (!tp) return null
              let b = body
              if (typeof b === "string") {
                try {
                  const j = JSON.parse(b)
                  if (TP_MODELS[j?.model]) b = JSON.stringify({ ...j, model: TP_MODELS[j.model] })
                } catch {}
              }
              const rest = new URL(url).pathname.replace(/^.*?\/route(?=\/|$)/, "")
              const h = new Headers(init.headers ?? req?.headers)
              for (const k of ["cookie", "x-mimo-source", "x-client-version", "user-agent"]) h.delete(k)
              h.set("Authorization", "Bearer " + tp.key)
              const res = await fetch(tp.base + rest, { ...init, method, headers: h, body: b })
              // the key was reset at the platform: read it again next time
              if (res.status === 401) planKeys.delete(who)
              return said(res, s.renewed ? "renewed" : "kept")
            }
            const since = onPlan.get(who)
            if (since && Date.now() - since < TP_LIFE) {
              const res = await toPlan()
              if (res) return res
              onPlan.delete(who)
            }
            const headers = new Headers(init.headers ?? req?.headers)
            headers.delete("authorization")
            headers.set("Cookie", cookieHeader(s.cookies))
            headers.set("X-Mimo-Source", SOURCE)
            headers.set("User-Agent", UA)
            headers.set("X-Client-Version", APP_VERSION)
            // the server's answer goes through as it is, a 401 too, as the
            // built-in's did: it took the lapse off only when it signed on
            // again, whatever the server then answered
            const res = await fetch(url, { ...init, method, headers, body })
            if (res.status === 403) {
              const text = await res.text()
              let code
              try {
                code = JSON.parse(text)?.error?.code
              } catch {}
              if (code === "membership_required") {
                const tp = await toPlan()
                if (tp) {
                  onPlan.set(who, Date.now())
                  return tp
                }
              }
              const h = new Headers(res.headers)
              h.delete("content-encoding")
              h.delete("content-length")
              return said(new Response(text, { status: res.status, statusText: res.statusText, headers: h }), s.renewed ? "renewed" : "kept")
            }
            return said(res, s.renewed ? "renewed" : "kept")
          },
        }
      },
      methods: [
        {
          type: "oauth",
          label: "Xiaomi account",
          authorize: async () => {
            const t = await startSignIn()
            return {
              url: t.url,
              instructions: "Sign in to your Xiaomi account on the page that opens (or scan its QR code with the Xiaomi app). The sign-in finishes here by itself.",
              method: "auto",
              callback: async () => {
                try {
                  const p = await waitPoll(t)
                  const { creds, cookies } = await signedInWith(p, t.device)
                  return { ...toAuth(creds, cookies, Date.now()), type: "success" }
                } catch (e) {
                  return { type: "failed", error: e.message }
                }
              },
            }
          },
        },
      ],
    },
  }
}

// for tests
export const _internal = { serverTime, vendorError, credits }
