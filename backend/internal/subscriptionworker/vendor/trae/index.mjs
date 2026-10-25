// Trae CN (trae.cn), ByteDance's AI IDE, as the IDE signs in to it and
// asks its models: trae.cn's authorization page sends the browser back to
// a callback on 127.0.0.1 with the account's Cloud-IDE-JWT and refresh
// token; the refresh token is exchanged for a new JWT at
// /cloudide/api/v3/trae/oauth/ExchangeToken; model requests go to the IDE
// agent's /api/agent/v3/llm_utils_chat, which answers in its own SSE
// events. Worked out from two open-source relays (wangqi233/trae2api and
// autumnsentiment/Trae2api-cn); not run against a real account here.
// The account's fetch also sends Trae CN's own pages on api.trae.cn
// (/trae/api/…) as the account, for magpie's daily check-in (每日签到:
// /trae/api/v2/ug/checkin_credits/status, then /claim).
import { createDecipheriv, createHash, generateKeyPairSync, randomBytes, randomUUID, randomInt, sign } from "node:crypto"
import { readFile } from "node:fs/promises"
import { createServer, STATUS_CODES } from "node:http"
import { homedir, release, version as osVersion } from "node:os"
import { join } from "node:path"

// SEES is a fallback model that takes images, as Trae's lists mark it
// (multimodal: true)
const SEES = { attachment: true, modalities: { input: ["text", "image"], output: ["text"] } }

const SITES = {
  // Trae CN (trae.cn): the authorization page sends the browser back to a
  // callback on 127.0.0.1 with the account's Cloud-IDE-JWT and refresh
  // token; the refresh token is exchanged for a new JWT at
  // /cloudide/api/v3/trae/oauth/ExchangeToken; model requests go to the IDE
  // agent's /api/agent/v3/llm_utils_chat, which answers in its own SSE
  // events. Worked out from two open-source relays (wangqi233/trae2api and
  // autumnsentiment/Trae2api-cn); not run against a real account here.
  // The account's fetch also sends Trae CN's own pages on api.trae.cn
  // (/trae/api/…) as the account, for magpie's daily check-in (每日签到:
  // /trae/api/v2/ug/checkin_credits/status, then /claim).
  "trae-cn": {
    id: "trae-cn",
    name: "Trae CN",
    realm: "Trae CN", // the name the errors and messages carry
    webHost: "trae.cn", // the bare host the sign-in copy names
    // where each part of Trae CN is served; tests point them at a fake
    hosts: {
      web: "https://www.trae.cn", // the authorization page
      auth: "https://api.trae.cn", // tokens, the account, credits
      api: "https://trae-api-cn.mchost.guru", // the models
    },
    clientId: "ono9krqynydwx5", // Trae CN's IDE
    // the clients' own renewal, which binds the JWT to a device: ExchangeToken
    // under /trae/api/v3/oauth, signed with the device's P-256 key
    // (DeviceProof). A JWT renewed at /cloudide/… carries no device, and
    // Trae CN answers its check-in claim 9074 「当前参与用户太多」 every time,
    // while the client's own token is let through (mintonight's side-by-side
    // on yetone/magpie#808). Tried as TRAE SOLO CN first, the one
    // qilimixingkong/trae-checkin found the server takes, then as Trae CN's
    // IDE. It doesn't spend the refresh token.
    deviceExchange: {
      path: "/trae/api/v3/oauth/ExchangeToken",
      as: [
        { clientId: "en1oxy7wnw8j9n", platform: "SOLO_PC", version: "0.1.64" },
        { clientId: "ono9krqynydwx5", platform: "IDE_PC", version: "3.3.99" },
      ],
    },
    appId: "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8",
    ideVersion: "3.3.65", // named to the authorization page
    ideVersionCode: "20260401",
    // the client the model host is told it serves: TRAE SOLO CN 0.1.69, as
    // its ai-agent names itself (same app id). Trae offers a model only to
    // clients new enough for it, and an April IDE was offered no
    // deepseek-v4.1-flash.
    clientVersion: "0.1.69",
    clientVersionCode: "20260917",
    pluginVersion: "2.3.24254",
    brand: "ASUS TUF Gaming A15 FA507RM_FA507RM",
    // the IDE's chat functions: the classic IDE's agent, then SOLO's Work
    // mode, then the TRAE agent (solo_agent, which has deepseek-v4.1-flash),
    // then SOLO Lite's agent, then SOLO's coder roster, which lists models no
    // other chat function does (glm-5v-turbo, kimi-k2.5, minimax-m2.7,
    // qwen-3.6-plus — each answers through it)
    functions: ["chat_v3", "solo_work_lite", "solo_agent", "solo_agent_lite", "solo_coder"],
    // what Trae CN's chat_v3 is known to serve; the live list replaces it
    // (the MODEL defaults are spread in at the end of the module, once MODEL
    // is defined, so a site's entry only names what differs)
    models: {
      "glm-5.2": { name: "GLM-5.2", limit: { context: 200_000, output: 32_000 } },
      "glm-5": { name: "GLM-5" },
      "kimi-k2.6": { name: "Kimi K2.6", limit: { context: 256_000, output: 0 }, ...SEES },
      "qwen-3.7-plus": { name: "Qwen 3.7 Plus", ...SEES },
      "DeepSeek-V4-Pro": { name: "DeepSeek V4 Pro" },
      "DeepSeek-V4-Flash": { name: "DeepSeek V4 Flash" },
    },
    // the model host for an account: Trae CN's own hosts serve no models,
    // so only a host that is one is taken, else the one gateway
    apiOf: (a) => {
      const h = traeHost(a?.api)
      return h && SITES["trae-cn"].modelHost(h) ? h : SITES["trae-cn"].hosts.api
    },
    // whether a host named by a sign-in is one of the model hosts: an https
    // host on mchost.guru or named trae-api-…, as the consumer gateway is,
    // or another trae.cn host than the consumer site's own (www, api,
    // grow…), which an enterprise (SaaS) sign-in may name for its tenant
    modelHost: (h) => {
      const t = traeHost(h)
      if (!t) return false
      const n = new URL(t).hostname
      return /(^|\.)mchost\.guru$|^trae-api-/i.test(n) || (/\.trae\.cn$/i.test(n) && !/^(www|api|grow[^.]*)\./i.test(n))
    },
    // ownPage: url is one of Trae CN's own JSON pages on api.trae.cn
    // (/trae/api/…: the daily check-in, credits), which the account's fetch
    // sends as the account rather than as a chat
    ownPage: (url) => {
      try {
        const u = new URL(url)
        return u.origin === new URL(SITES["trae-cn"].hosts.auth).origin && u.pathname.startsWith("/trae/api/")
      } catch {
        return false
      }
    },
    // Trae CN's account info carries no region: its one deployment is the
    // one it is asked through
    regionOf: () => "",
    checkin: {
      status: { path: "/trae/api/v2/ug/checkin_credits/status" },
      claim: { path: "/trae/api/v2/ug/checkin_credits/claim" },
    },
    // where usage is asked, and which page it is (CN: credits)
    usage: {
      kind: "credits",
      path: "/trae/api/v2/pay/ide_user_ent_usage",
      label: "credits",
      host: (site) => site.hosts.auth,
      body: { require_usage: true, req_source: 0 },
      read: (v, a, signIn, { credits, round }) => {
        const c = credits(v)
        const out = { plan: v.is_credits_billing ? "Credits" : "Free", user: a.name || a.uid, signIn }
        if (c.unlimited) out.balance = "unlimited"
        else if (c.limit > 0) {
          // the count rides on the window (amount of limit, in credits), as
          // WorkBuddy's does, so magpie says it in the window's row, used or
          // left, in its own words and number format, rather than as an
          // English balance line beside it (yetone/magpie#694)
          const w = { name: "Credits", used: Math.max(0, Math.min(100, (c.used / c.limit) * 100)), amount: round(c.used), limit: round(c.limit), unit: "credits" }
          if (c.until) w.resetsAt = new Date(c.until).toISOString()
          out.windows = [w]
        }
        return out
      },
    },
  },
  // Trae international (trae.ai): the same IDE sign-in flow against the
  // international deployment. Its own authorization page, its own auth and
  // chat hosts, a second (US) deployment the account's region selects, and
  // the dollar billing's entitlements rather than the CN credits page.
  // Worked out from the realm table of wangqi233/trae2api (cn/sg dual-region)
  // and the login URL of wefew/trae2api; run against one Free SG account.
  "trae-global": {
    id: "trae-global",
    name: "Trae Global",
    realm: "Trae Global",
    // its lists mark Auto-mode channels and pay-as-you-go twins
    // is_invisible_to_user, which its picker hides; Trae CN's lists are
    // served whole, as they were before Global joined this package
    hidesInvisible: true,
    webHost: "trae.ai",
    hosts: {
      web: "https://www.trae.ai", // the authorization page
      auth: "https://growsg-normal.trae.ai", // tokens, the account
      api: "https://coresg-normal.trae.ai", // the models, SG
      us: "https://coreva-normal.trae.ai", // the models, US
      pay: "https://api-sg-central.trae.ai", // usage
      usPay: "https://api-us-east.trae.ai", // usage, the US deployment
    },
    clientId: "ono9krqynydwx5",
    appId: "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8",
    ideVersion: "3.5.51", // named to the international IDE's current build
    ideVersionCode: "20260401",
    clientVersion: "0.1.69",
    clientVersionCode: "20260917",
    pluginVersion: "2.3.62834",
    brand: "ASUS TUF Gaming A15 FA507RM_FA507RM",
    functions: ["chat_v3", "solo_work_lite", "solo_agent", "solo_agent_lite", "solo_coder"],
    // what Trae Global's chat is known to serve (its catalog barely overlaps
    // the CN one's); the live list replaces it
    models: {
      "gpt-5.4": { name: "GPT-5.4" },
      "gemini-3-flash": { name: "Gemini 3 Flash", limit: { context: 1_000_000, output: 0 } },
      "kimi-k2.5": { name: "Kimi K2.5", limit: { context: 256_000, output: 0 } },
      "minimax-m2": { name: "MiniMax M2" },
      "deepseek-v3.2": { name: "DeepSeek V3.2" },
    },
    // the model host: the account's region (or a US host the sign-in named)
    // selects the US deployment, else the SG one
    apiOf: (a) => {
      const h = traeHost(a?.api)
      const n = h ? new URL(h).hostname : ""
      const region = String(a?.region ?? "").toUpperCase()
      if (/coreva/i.test(n) || /api-us(?:[-.]|$)/i.test(n) || /^US(?:[-_ ]|$)/.test(region)) return SITES["trae-global"].hosts.us
      if (/coresg/i.test(n)) return h
      // another trae.ai host than the site's own (www, api-…, grow…): one an
      // enterprise (SaaS) sign-in named for its tenant
      if (/\.trae\.ai$/i.test(n) && !/^(www\.|api[-.]|grow)/i.test(n)) return h
      return SITES["trae-global"].hosts.api
    },
    // a sign-in names a host of Trae's, which can be an auth host
    // (api-us-east), not the chat host
    modelHost: (h) => {
      const t = traeHost(h)
      return !!t && /(^|\.)trae\.ai$/i.test(new URL(t).hostname)
    },
    // Trae Global serves no own JSON pages through the account's fetch
    ownPage: () => false,
    // the account's region, from the sign-in's user info
    regionOf: (s) => String(s?.region ?? s?.aiRegion ?? s?.Region ?? s?.AIRegion ?? "").trim(),
    // the international deployment also answers these
    lapsed: [1001, "1001", 20101, "20101", 401, "401"],
    quota: [4008, "4008", 1005, "1005", 4011, "4011"],
    // where usage is asked, and which page it is (Global: the dollar
    // billing's entitlements)
    usage: {
      kind: "dollar",
      path: "/trae/api/v1/pay/user_current_entitlement_list",
      label: "usage",
      // the US account pays on its own host, the rest on the central one —
      // the auth host's own /cloudide/api/v2/pay pages answer 403 Api Scope
      // Forbidden here
      host: (site, a) => (/^US(?:[-_ ]|$)/i.test(String(a?.region ?? "")) || /api-us/i.test(String(a?.api ?? "")) ? site.hosts.usPay : site.hosts.pay),
      body: {},
      read: (v, a, signIn, { round }) => {
        const d = dollarUsageOf(v)
        if (!d) return { error: "Trae Global usage: no entitlement pack in the answer", signIn, user: a.name }
        const total = d.basic + d.bonus
        const used = d.used + d.usedBonus
        const out = { plan: d.plan || "Free", user: a.name || a.uid, signIn }
        if (total > 0) {
          // the allowance rides the window (amount of limit, in dollars), as
          // WorkBuddy's does, so magpie says it in the window's row, used or
          // left, in its own words and number format (yetone/magpie#694)
          const w = { name: "Dollar Usage", used: Math.round(Math.max(0, Math.min(100, (used / total) * 100)) * 100) / 100, amount: round(used), limit: round(total), unit: "usd" }
          if (d.end) w.resetsAt = new Date(d.end).toISOString()
          out.windows = [w]
        }
        return out
      },
    },
  },
}

// traeHost is a host a sign-in named, when it is a well-formed https one
// on Trae's own domains (no port, no credentials), as origin plus any path;
// "" for anything else, which is then not taken
const TRAE_DOMAINS = ["trae.cn", "trae.ai", "mchost.guru"]
function traeHost(h) {
  try {
    const u = new URL(String(h ?? "").trim())
    if (u.protocol !== "https:" || u.username || u.password || u.port || u.search || u.hash) return ""
    const n = u.hostname.toLowerCase()
    if (!TRAE_DOMAINS.some((d) => n === d || n.endsWith("." + d))) return ""
    return (u.origin + u.pathname).replace(/\/+$/, "")
  } catch {
    return ""
  }
}

// authHostOf is the host a sign-in named for its tokens, when it named one
// on the site's own domain (an enterprise's may differ from the consumer
// one); "" when it named none, and the site's own is used
function authHostOf(h, site) {
  const t = traeHost(h)
  if (!t) return ""
  const n = new URL(t).hostname
  return n === site.webHost || n.endsWith("." + site.webHost) ? t : ""
}

// accountOf keeps the account fields of the sign-in's user info that say
// what kind of account it is (tenant, enterprise, login type), plain values
// only and never anything that looks like a secret, for later use; nothing
// sends them
function accountOf(info) {
  const out = {}
  for (const [k, v] of Object.entries(info ?? {})) {
    if (!/tenant|enterprise|saas|login|org|team|sso|^ent|usertype|accounttype/i.test(k)) continue
    if (/token|secret|key|password|cookie|session|jwt/i.test(k)) continue
    if (v === null || ["string", "number", "boolean"].includes(typeof v)) out[k] = v
  }
  return out
}

const SIGN_IN_TIMEOUT = 10 * 60 * 1000
const EARLY_MS = 2 * 60 * 1000 // a token this close to its end is renewed before a request
const LEAD_MS = 10 * 60 * 1000 // and this close, magpie renews it ahead of time (auth.refresh)
const DAY = 24 * 3600 * 1000
// REREAD_MS is how long a list read keeps its answer before a request that
// wants levels may read the list again: it holds whether the read produced an
// entry for the model or not, so a list answering badly (or one that doesn't
// name the model) costs one read, not one per request. A request path that
// reads on every attempt would otherwise pay two 20s timeouts on each turn —
// the branch costs the chat itself nothing to answer from.
const REREAD_MS = 5 * 60 * 1000

// a reply limit of 0 is one Trae doesn't say: magpie then takes models.dev's
// for the model (magpie's catalog), where a made-up 32K told agents a far
// smaller one than DeepSeek V4.1 Flash's (ARNO on magpie's Discord)
const MODEL = { attachment: false, tool_call: true, reasoning: true, temperature: true, limit: { context: 128_000, output: 0 }, modalities: { input: ["text"], output: ["text"] } }
// a site's fallback list names only what differs from MODEL: the defaults go
// in here, where MODEL is defined (a site table above can't spread it)
for (const site of Object.values(SITES)) {
  for (const [id, m] of Object.entries(site.models)) site.models[id] = { ...MODEL, ...m }
}

const digits = (n) => Array.from({ length: n }, (_, i) => (i === 0 ? randomInt(1, 10) : randomInt(0, 10))).join("")
const statusLine = (status) => `${status} ${STATUS_CODES[status] ?? ""}`.trim()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// device is the machine an account signs in from, made once at sign-in and
// named to trae.cn's authorization page, then sent with every request: a
// fresh one each time is what the relays saw requests dropped for. Trae
// CN's id has the shape its clients' have, 16 digits: the device service
// gives the IDE a 15- or 16-digit one (iCubeAuthInfo://icube-dc:<id>, see
// clientDevice), and Trae CN answers a check-in claim on a 19-digit id
// 9074 where the same account and token on a 16-digit one are taken
// (magpie-community/plugins#194). Trae's global site keeps the 19 digits
// it was signed in with: nothing says its ids are shaped otherwise.
const newDevice = (site) => ({ deviceId: digits(site?.id === "trae-cn" ? 16 : 19), machineId: randomBytes(16).toString("hex"), ...newDeviceKey() })

// ugDevice is the device id a Trae CN account's check-in goes on when this
// machine's client isn't signed in as it: its own when that already has a
// client's shape (15 or 16 digits), else, for an account signed in before
// 0.2.16 with 19 digits, a 16-digit id made from its own device id and
// uid. That one is the same on every check-in of the account, never
// rotated and never another account's: it is the account's own device in
// the shape Trae CN takes (#194), not a new device to get past a refusal,
// and a 9095 (this device is in for today) on it stays the day's answer.
// The chat's pages and the renewal keep the device the account signed in
// with.
function ugDevice(a) {
  const id = String(a.deviceId ?? "")
  if (/^\d{15,16}$/.test(id)) return id
  if (!id && !a.uid) return id
  const n = BigInt("0x" + createHash("sha256").update(`trae-cn-device\0${id}\0${a.uid ?? ""}`).digest("hex").slice(0, 30))
  return String(1_000_000_000_000_000n + (n % 9_000_000_000_000_000n))
}

// newDeviceKey is the device's key pair, an ECDSA P-256 one as the clients
// keep (iCubeAuthInfo://icube-dc:<device>), whose public half goes with a
// renewal and whose private half signs it (DeviceProof). An account signed
// in before 0.2.3 has none and is given one at its next renewal.
function newDeviceKey() {
  const { publicKey, privateKey } = generateKeyPairSync("ec", { namedCurve: "prime256v1" })
  return {
    devicePublicKey: publicKey.export({ type: "spki", format: "pem" }),
    devicePrivateKey: privateKey.export({ type: "pkcs8", format: "pem" }),
  }
}

// whenOf reads one of Trae's expiry times: seconds, milliseconds or ISO
function whenOf(v) {
  if (v === undefined || v === null || v === "") return 0
  if (typeof v === "string" && !/^\d+$/.test(v.trim())) {
    const t = Date.parse(v)
    return isNaN(t) ? 0 : t
  }
  const n = Number(v)
  if (!Number.isFinite(n) || n <= 0) return 0
  return n < 1e11 ? n * 1000 : n
}

// jwtExp is a JWT's exp, in ms, 0 when it has none
function jwtExp(token) {
  try {
    const p = JSON.parse(Buffer.from(String(token).split(".")[1], "base64url").toString())
    return whenOf(p?.exp)
  } catch {
    return 0
  }
}

const parseJSON = (s) => {
  if (s && typeof s === "object") return s
  try {
    const v = JSON.parse(String(s ?? ""))
    return v && typeof v === "object" ? v : {}
  } catch {
    return {}
  }
}

// ---- the sign-in as kept ------------------------------------------------------

// The sign-in is kept as OpenCode keeps an OAuth one: access the
// Cloud-IDE-JWT, refresh the refresh token, expires the JWT's end; beside
// them the account, the client it was issued to and its device.
function toAuth(s, site) {
  return {
    type: "oauth",
    access: s.token,
    refresh: s.refresh,
    expires: s.expires || jwtExp(s.token) || Date.now() + DAY,
    accountId: s.name || s.uid,
    uid: s.uid,
    clientId: s.clientId || site.clientId,
    deviceId: s.deviceId,
    machineId: s.machineId,
    ...(s.devicePrivateKey ? { devicePublicKey: s.devicePublicKey, devicePrivateKey: s.devicePrivateKey } : {}),
    // the model host the sign-in named, if it named one
    ...(site.modelHost(s.api) ? { api: s.api } : {}),
    ...(s.region ? { region: s.region } : {}),
    // the host the sign-in named, its own token host if it named one, and
    // what kind of account it is (an enterprise one's tenant), kept as given
    ...(traeHost(s.host) ? { host: traeHost(s.host) } : {}),
    ...(authHostOf(s.authHost, site) ? { authHost: authHostOf(s.authHost, site) } : {}),
    ...(s.account && Object.keys(s.account).length ? { account: s.account } : {}),
  }
}

function fromAuth(auth, site) {
  if (auth?.type !== "oauth" || !auth.access) return null
  return {
    token: auth.access,
    refresh: auth.refresh ?? "",
    expires: Number(auth.expires) || 0,
    uid: String(auth.uid ?? ""),
    name: String(auth.accountId ?? ""),
    clientId: auth.clientId || site.clientId,
    deviceId: auth.deviceId || "",
    machineId: auth.machineId || "",
    devicePublicKey: auth.devicePublicKey || "",
    devicePrivateKey: auth.devicePrivateKey || "",
    api: auth.api || "",
    region: site.regionOf(auth),
    host: auth.host || "",
    authHost: authHostOf(auth.authHost, site),
    account: auth.account && typeof auth.account === "object" ? auth.account : {},
  }
}

// apiOf is the model host for an account
function apiOf(a, site) {
  return site.apiOf(a)
}

// ownPage: url is one of Trae's own JSON pages (the daily check-in,
// credits), which the account's fetch sends as the account rather than as
// a chat
function ownPage(url, site) {
  return site.ownPage(url)
}

// ---- errors --------------------------------------------------------------------

class Expired extends Error {
  constructor(msg) {
    super(msg)
    this.signIn = "expired"
  }
}

// errorOf is what Trae said went wrong, from its several shapes: a business
// {code, message}, {error: {code, message}}, or ByteDance's
// {ResponseMetadata: {Error: {Code, Message}}}
function errorOf(v) {
  v = parseJSON(v)
  const meta = v?.ResponseMetadata?.Error
  if (meta && (meta.Code || meta.Message)) return { code: meta.Code ?? "", message: String(meta.Message ?? meta.Code ?? "") }
  const e = v?.error && typeof v.error === "object" ? v.error : v
  const code = e?.code ?? e?.Code ?? v?.code ?? ""
  const message = e?.message ?? e?.msg ?? e?.Message ?? (typeof v?.error === "string" ? v.error : "") ?? ""
  return { code, message: String(message || "") }
}

// lapsed: Trae turned the token away. 1001 is its "not signed in".
// The realms differ in which codes they send: the international deployment
// also answers 20101 for a token it no longer takes.
const CN_LAPSED = [1001, "1001", 401, "401"]
const lapsedCode = (code, site) => (site?.lapsed ?? CN_LAPSED).includes(code)
const lapsedWords = /not ?log(ged)? ?in|unauthori[sz]ed|token (is )?(expired|invalid)|jwt|未登录|登录(已)?(过期|失效)/i
const CN_QUOTA = [4008, "4008", 1005, "1005"]
const quotaCode = (code, site) => (site?.quota ?? CN_QUOTA).includes(code)
const quotaWords = /quota|credit|insufficient|exceed|limit|额度|积分|次数|上限|用完/i

// errorResponse is an OpenAI-style error, as the agent reads one
const errorResponse = (status, message, signIn) => {
  const headers = { "content-type": "application/json" }
  if (signIn) headers["X-Magpie-Sign-In"] = signIn
  return new Response(JSON.stringify({ error: { message, type: status === 429 ? "rate_limit_error" : "api_error", code: null } }), { status, headers })
}

// ---- tokens -----------------------------------------------------------------------

// exchange trades a refresh token for a new Cloud-IDE-JWT, as the IDE
// does: the answer's Result holds the new token pair and the account.
// Trae issues a new refresh token each time and spends the old one.
async function exchange(refresh, clientId, site, host = site.hosts.auth) {
  let res
  try {
    res = await fetch(host.replace(/\/+$/, "") + "/cloudide/api/v3/trae/oauth/ExchangeToken", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ClientID: clientId || site.clientId, RefreshToken: refresh, ClientSecret: "-", UserID: "" }),
      signal: AbortSignal.timeout(20_000),
    })
  } catch (e) {
    throw new Error(`${site.realm}: renewing the sign-in: ` + (e?.message ?? e))
  }
  const text = await res.text()
  const v = parseJSON(text)
  const r = v.Result ?? v.result ?? v
  const token = r?.Token ?? r?.token ?? ""
  if (res.ok && token) {
    const info = parseJSON(r.UserInfo ?? r.userInfo)
    return {
      token,
      refresh: r.RefreshToken ?? r.refreshToken ?? refresh,
      expires: whenOf(r.TokenExpireAt ?? r.tokenExpireAt) || jwtExp(token),
      clientId: r.ClientID ?? r.clientId ?? clientId,
      uid: String(info.UserID ?? info.userId ?? r.UserID ?? ""),
      name: String(info.ScreenName ?? info.screenName ?? ""),
      api: info.Host ?? "",
      region: site.regionOf(info),
      account: accountOf(info),
    }
  }
  const e = errorOf(text)
  const msg = e.message || (text.trim() ? text.trim().slice(0, 200) : statusLine(res.status))
  // a refresh token Trae no longer takes (spent, revoked, past its end, or
  // of another client) is a sign-in to make again
  if ([400, 401, 403].includes(res.status) || /refresh|expired|invalid|not matched|revoked|unauthori/i.test(msg) || String(e.code) === "10101") {
    throw new Expired(`${site.realm}'s sign-in has expired (${msg}); sign in again`)
  }
  throw new Error(`${site.realm}: renewing the sign-in: ${statusLine(res.status)} ${msg}`.trim())
}

// proofOf is a renewal's DeviceProof: the device key's ECDSA-SHA256
// signature (DER, base64) of the method, path, client, refresh token, time
// and nonce, one to a line, as the clients sign it
function proofOf(path, clientId, refresh, privateKey, at = Math.floor(Date.now() / 1000), nonce = randomBytes(16).toString("hex")) {
  const text = ["POST", path, clientId, refresh, String(at), nonce].join("\n")
  return { Signature: sign("sha256", Buffer.from(text), privateKey).toString("base64"), Timestamp: at, Nonce: nonce }
}

// deviceInfo is the device a renewal names, its public key with it
function deviceInfo(a, as, site) {
  return {
    DeviceID: a.deviceId,
    MachineID: a.machineId,
    PlatformCode: as.platform,
    DeviceType: "PC",
    DeviceName: "",
    DeviceModel: "",
    ClientVersion: as.version,
    DevicePublicKey: a.devicePublicKey,
    DeviceBrand: site.brand,
    DeviceCPU: "AMD",
    OSInfo: "Windows",
    OSVersion: "10.0.22631",
  }
}

// boundExchange renews the JWT as the clients do (site.deviceExchange), so
// that it is bound to the account's device; an account with no device key
// is given one first. It tries each client the site names, and throws the
// last answer when none takes it.
async function boundExchange(a, site, host = site.hosts.auth) {
  const dx = site.deviceExchange
  const d = a.devicePrivateKey && a.devicePublicKey ? a : { ...a, ...newDeviceKey() }
  let last
  for (const as of dx.as) {
    let res
    try {
      res = await fetch(host.replace(/\/+$/, "") + dx.path, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...(a.token ? { Authorization: `Cloud-IDE-JWT ${a.token}` } : {}) },
        body: JSON.stringify({
          ClientID: as.clientId,
          ClientSecret: "",
          RefreshToken: a.refresh,
          DeviceInfo: deviceInfo(d, as, site),
          DeviceProof: proofOf(dx.path, as.clientId, a.refresh, d.devicePrivateKey),
          IDEVersion: as.version,
        }),
        signal: AbortSignal.timeout(20_000),
      })
    } catch (e) {
      last = new Error(`${site.realm}: renewing the sign-in for its device: ` + (e?.message ?? e))
      continue
    }
    const text = await res.text()
    const v = parseJSON(text)
    const r = v.Result ?? v.result ?? {}
    const token = r.Token ?? r.token ?? ""
    if (res.ok && token) {
      return {
        token,
        refresh: r.RefreshToken ?? r.refreshToken ?? a.refresh,
        expires: whenOf(r.TokenExpireAt ?? r.tokenExpireAt) || jwtExp(token),
        devicePublicKey: d.devicePublicKey,
        devicePrivateKey: d.devicePrivateKey,
      }
    }
    const e = errorOf(text)
    last = new Error(`${site.realm}: renewing the sign-in for its device: ${statusLine(res.status)} ${e.message || text.trim().slice(0, 200)}`.trim())
  }
  throw last
}

// renewal renews an account's JWT: bound to its device where the site can
// (boundExchange), else, or when Trae turns that away, at /cloudide/… as
// before 0.2.3 (exchange). A device key made for the account goes with
// either, so the next renewal signs with the same one.
async function renewal(a, site) {
  // the token host the sign-in named, else the site's
  const host = authHostOf(a.authHost, site) || site.hosts.auth
  if (!site.deviceExchange) return exchange(a.refresh, a.clientId, site, host)
  const keys = a.devicePrivateKey && a.devicePublicKey ? {} : newDeviceKey()
  try {
    return await boundExchange({ ...a, ...keys }, site, host)
  } catch {}
  return { ...(await exchange(a.refresh, a.clientId, site, host)), ...keys }
}

// ---- the browser sign-in --------------------------------------------------------

const page = (ok, title, text) => `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>${title}</title>
<style>body{font:15px/1.5 -apple-system,system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#f6f6f4;color:#222}
@media(prefers-color-scheme:dark){body{background:#1c1c1c;color:#eee}}main{max-width:420px;padding:24px}h1{font-size:20px;margin:0 0 8px}p{margin:0;opacity:.75}
.dot{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:8px;background:${ok ? "#2a9d58" : "#d1453b"}}</style>
<main><h1><span class="dot"></span>${title}</h1><p>${String(text).replace(/[<>&]/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;" })[c])}</p></main>`

// signedIn reads trae.cn's callback: userJwt (the token pair) and userInfo
// (the account), or, from its older page, a refresh token alone, which is
// exchanged for the pair
async function signedIn(q, device, site) {
  const jwt = parseJSON(q.get("userJwt"))
  const info = parseJSON(q.get("userInfo"))
  const host = q.get("host") || info.Host || ""
  let s = {
    token: jwt.Token ?? jwt.token ?? "",
    refresh: jwt.RefreshToken ?? jwt.refreshToken ?? q.get("refreshToken") ?? "",
    expires: whenOf(jwt.TokenExpireAt ?? jwt.tokenExpireAt),
    clientId: jwt.ClientID ?? jwt.clientId ?? q.get("clientId") ?? site.clientId,
    uid: String(info.UserID ?? info.userId ?? q.get("userId") ?? ""),
    name: String(info.ScreenName ?? info.screenName ?? ""),
    api: host,
    region: site.regionOf(info),
    host,
    // a token host the callback named, if it named one
    authHost: authHostOf(q.get("authHost") || q.get("auth_host") || info.AuthHost || info.authHost, site),
    account: accountOf(info),
  }
  const authHost = s.authHost || site.hosts.auth
  if (!s.token && s.refresh) {
    const x = await exchange(s.refresh, s.clientId, site, authHost)
    s = { ...s, ...x, uid: x.uid || s.uid, name: x.name || s.name, api: x.api || s.api, region: x.region || s.region, host: s.host || x.api, account: { ...x.account, ...s.account } }
  }
  if (!s.token) throw new Error(`${site.webHost} sent back no token`)
  if (!s.refresh) throw new Error(`${site.webHost} sent back no refresh token`)
  s = { ...s, ...device }
  // the token the page gives is bound to no device: it is renewed for this
  // one at once, so a check-in takes it; when Trae won't, it does as is
  if (site.deviceExchange) {
    try {
      const x = await boundExchange(s, site, authHost)
      s = { ...s, token: x.token, refresh: x.refresh, expires: x.expires || s.expires }
    } catch {}
  }
  return s
}

// browserSignIn is the IDE's sign-in: trae.cn's authorization page, back
// to a callback on 127.0.0.1 (the page takes no other kind)
async function browserSignIn(site) {
  const device = newDevice(site)
  const trace = randomUUID()
  let over = false
  let settle
  const done = new Promise((r) => (settle = r))
  const finish = (result) => {
    if (over) return
    over = true
    settle(result)
  }
  const server = createServer(async (req, res) => {
    // the site's page may call the callback from script as well as open it
    const cors = { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "GET, OPTIONS", "Access-Control-Allow-Headers": "Content-Type" }
    if (req.method === "OPTIONS") return res.writeHead(204, cors).end()
    const url = new URL(req.url ?? "/", "http://127.0.0.1")
    const html = (body) => {
      res.writeHead(200, { ...cors, "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" })
      res.end(body)
    }
    if (url.pathname !== "/authorize") return res.writeHead(404, cors).end()
    const q = url.searchParams
    if (over) return html(page(false, "This sign-in is over", "Start it again in magpie."))
    if (q.get("error")) {
      const msg = q.get("error_description") || q.get("error")
      finish({ type: "failed", error: msg })
      return html(page(false, "Sign-in didn't finish", msg))
    }
    if (!q.get("userJwt") && !q.get("refreshToken")) return html(page(false, "Nothing to sign in with", `${site.webHost} sent no token; start the sign-in again.`))
    try {
      const s = await signedIn(q, device, site)
      finish({ ...toAuth(s, site), type: "success" })
      html(page(true, "You're signed in", `${s.name || s.uid || `Your ${site.name} account`} is signed in. You can close this tab.`))
    } catch (e) {
      finish({ type: "failed", error: e.message })
      html(page(false, "Sign-in didn't finish", e.message))
    }
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const callback = `http://127.0.0.1:${server.address().port}/authorize`
  const timer = setTimeout(() => finish({ type: "failed", error: "the sign-in timed out" }), SIGN_IN_TIMEOUT)
  done.then(() => {
    clearTimeout(timer)
    setTimeout(() => server.close(), 5_000).unref?.()
  })
  const q = new URLSearchParams({
    login_version: "1",
    auth_from: "solo",
    login_channel: "native_ide",
    plugin_version: site.pluginVersion,
    auth_type: "local",
    client_id: site.clientId,
    redirect: "0",
    login_trace_id: trace,
    auth_callback_url: callback,
    machine_id: device.machineId,
    device_id: device.deviceId,
    x_device_id: device.deviceId,
    x_machine_id: device.machineId,
    x_device_brand: site.brand,
    x_device_type: "windows",
    x_os_version: "Windows 10 Pro",
    x_env: "",
    x_app_version: site.ideVersion,
    x_app_type: "stable",
    // the enterprise (SaaS) sign-in is offered beside the personal one: an
    // enterprise subscription signs in there (@shad70w0 on X)
    hide_saas_login: "false",
  })
  return {
    url: `${site.hosts.web}/authorization?${q}`,
    instructions: `Sign in to ${site.name} (${site.webHost}) in the browser and allow the sign-in. It finishes here by itself.`,
    method: "auto",
    callback: () => done,
  }
}

// ---- requests ---------------------------------------------------------------------

// headers are the IDE's, for the account and its device
function ideHeaders(a, site, extra = {}) {
  return {
    "Content-Type": "application/json",
    Authorization: `Cloud-IDE-JWT ${a.token}`,
    "X-Cloudide-Token": a.token,
    "x-ide-token": a.token,
    "x-uid": a.uid,
    "x-app-id": site.appId,
    "x-device-id": a.deviceId,
    "x-machine-id": a.machineId,
    "x-request-id": randomUUID(),
    "x-app-version": site.clientVersion,
    "x-app-version-code": site.clientVersionCode,
    "x-ide-version": site.clientVersion,
    "x-ide-version-code": site.clientVersionCode,
    "x-ide-version-type": "stable",
    "x-device-cpu": "AMD",
    "x-device-brand": site.brand,
    "x-device-type": "windows",
    "x-os-version": "Windows 10",
    "x-system-type": "Windows",
    ...extra,
  }
}

// ugUA is the User-Agent a growth page (/trae/api/v2/ug/…: the daily
// check-in) is sent with: TRAE SOLO CN's Electron shell, as its version
// is named. Bun's own (Bun/1.x) reads as a script, and Trae answers a
// script's check-in with 9074 「当前参与用户太多，请稍后再试」 however
// seldom it asks (yetone/magpie#808; qilimixingkong/trae-checkin found
// the same).
const ugUA = (site) =>
  `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) TRAE-SOLO-CN/${site.clientVersion} Chrome/120.0.6099.291 Electron/28.2.10 Safari/537.36`

// ugHeaders are the headers Trae CN's IDE (3.3.104) sends a growth page
// with, and only those: its JWT, then its device (fb: x-device-id,
// x-device-brand, x-device-type, x-os-version, x-app-version), not the
// chat's tokens and versions. own is this machine's Trae CN client, when
// it is signed in as this very account (ownClient): its device id and
// this machine's OS go instead of the account's, and no brand is named
// (fb leaves it out when it has no model to name).
function ugHeaders(a, site, extra = {}, own) {
  const h = {
    "Content-Type": "application/json",
    Authorization: `Cloud-IDE-JWT ${a.token}`,
    "User-Agent": ugUA(site),
    "x-device-id": a.deviceId,
    "x-device-brand": site.brand,
    "x-device-type": "Windows",
    "x-os-version": "10.0.22631",
    "x-app-version": site.clientVersion,
  }
  if (own) {
    h["x-device-id"] = own.deviceId
    h["x-device-type"] = own.type
    h["x-os-version"] = own.os
    delete h["x-device-brand"]
  }
  return { ...h, ...extra }
}

// ---- this machine's own Trae CN client ----------------------------------------------

// Trae CN's desktop client keeps its state in a VS Code-style
// globalStorage/storage.json. The device id Trae's device service gave
// it is in a key's name, iCubeAuthInfo://icube-dc:<digits> (15 or 16
// digits seen); its sign-in is the value of iCubeAuthInfo://icube.cloudide,
// a JSON envelope (userId, token, …) stored as is or obfuscated with the
// client's fixed AES-128-CBC codec. Seen in real clients' files
// (yetone/magpie#808: the same id in the client's own did= query;
// yetone/magpie#1705) and read the same way by dingminhua/dsh-connect-trae
// (MIT), whose codec this is.
const DC_KEY = "iCubeAuthInfo://icube-dc:"
const AUTH_KEY = "iCubeAuthInfo://icube.cloudide"

// ownClient.paths are where storage.json may be, in order; tests replace it
const ownClient = {
  paths: () => clientStoragePaths(process.platform, homedir(), process.env),
}

// CLIENT_PROFILES are the folder names a Trae CN client profile is kept
// under: the IDE's own, and TRAE SOLO CN's, which is a client of the same
// realm and signs in its own account. A profile started with its own
// --user-data-dir is found only if it is one of these; there is no list of
// them to read, and nothing is guessed from a folder's contents.
const CLIENT_PROFILES = ["Trae CN", "TRAE SOLO CN", "trae-cn"]

function clientStoragePaths(platform, home, env) {
  const file = (root, name) => join(root, name, "User", "globalStorage", "storage.json")
  if (platform === "darwin") return CLIENT_PROFILES.filter((n) => n !== "trae-cn").map((n) => file(join(home, "Library", "Application Support"), n))
  if (platform === "win32") {
    // the two roots are often the same folder: APPDATA and the home's own
    // AppData\Roaming. They are compared as one system spells them, so a
    // difference of separator or case alone doesn't read the same file twice
    const spell = (r) => String(r ?? "").replace(/[\\/]+/g, "\\").replace(/\\+$/, "").toLowerCase()
    const roots = []
    for (const r of [env.APPDATA, join(home, "AppData", "Roaming")]) {
      if (!r) continue
      if (roots.some((was) => spell(was) === spell(r))) continue
      roots.push(r)
    }
    return roots.flatMap((r) => CLIENT_PROFILES.map((n) => file(r, n)))
  }
  if (platform === "linux") return CLIENT_PROFILES.map((n) => file(env.XDG_CONFIG_HOME || join(home, ".config"), n))
  return []
}

// clientDevice is the device id of a Trae CN client profile on this machine
// that is signed in as uid, else undefined. It only reads: nothing of the
// client's is written, kept or sent but that id, and the client's own token
// is read past, never used. Never an id made up or changed: an account no
// profile is signed in as never gets a client's id.
//
// A machine can hold more than one profile — Trae CN and TRAE SOLO CN each
// write one, and a client started with its own --user-data-dir writes
// another — and each profile's storage.json names the account it is signed
// in as, with that account's device. So every profile is read and the one
// signed in as uid answers with its device, which is what lets two accounts
// each check in on a real client's device. A profile that isn't signed in
// as uid, that can't be read or made out, or that names no single device
// is passed over rather than ending the search: before, the first profile
// read decided for all of them, so a second account's own profile was never
// seen (and one unreadable file hid every profile after it).
async function clientDevice(uid) {
  if (!uid) return undefined
  for (const p of ownClient.paths()) {
    let text
    try {
      text = await readFile(p, "utf8")
    } catch {
      continue // not installed here
    }
    let doc
    try {
      doc = JSON.parse(text)
    } catch {
      continue // not a file we can make out: the next profile may be
    }
    try {
      const ids = Object.keys(doc ?? {}).filter((k) => k.startsWith(DC_KEY)).map((k) => k.slice(DC_KEY.length))
      if (ids.length !== 1 || !/^\d{8,20}$/.test(ids[0])) continue
      const who = clientAuth(doc[AUTH_KEY])?.userId
      if (who != null && String(who) === String(uid)) return ids[0]
    } catch {
      continue // a sign-in that doesn't decode: the next profile may be
    }
  }
  return undefined
}

// clientAuth is the client's sign-in envelope, as stored or decoded
function clientAuth(v) {
  if (typeof v !== "string" || !v.trim()) return undefined
  const t = v.trim()
  return JSON.parse(t.startsWith("{") ? t : clientDecode(t))
}

const xorOf = (a, b) => Buffer.from(a.map((x, i) => x ^ b[i]))
const CODEC = {
  "746305100000": xorOf(
    [82, 9, 106, 213, 48, 54, 165, 56, 191, 64, 163, 158, 129, 243, 215, 251, 124, 227, 57, 130, 155, 47, 255, 135, 52, 142, 67, 68, 196, 222, 233, 203, 84, 123, 148, 50, 166, 194, 35, 61, 238, 76, 149, 11, 66, 250, 195, 78, 8, 46, 161, 102, 40, 217, 36, 178, 118, 91, 162, 73, 109, 139, 209, 37],
    [31, 221, 168, 51, 136, 7, 199, 49, 177, 18, 16, 89, 39, 128, 236, 95, 96, 81, 127, 169, 25, 181, 74, 13, 45, 229, 122, 159, 147, 201, 156, 239, 160, 224, 59, 77, 174, 42, 245, 176, 200, 235, 187, 60, 131, 83, 153, 97, 23, 43, 4, 126, 186, 119, 214, 38, 225, 105, 20, 99, 85, 33, 12, 125],
  ),
  "123920200203": xorOf(
    [191, 192, 216, 250, 122, 246, 220, 97, 31, 254, 98, 27, 8, 72, 71, 176, 135, 99, 96, 18, 127, 101, 203, 104, 211, 102, 191, 125, 37, 72, 150, 156, 51, 229, 121, 35, 17, 153, 141, 177, 110, 131, 150, 128, 172, 255, 254, 6, 18, 140, 55, 62, 236, 249, 135, 64, 135, 12, 117, 4, 89, 149, 168, 209],
    [246, 204, 26, 232, 232, 70, 129, 109, 223, 146, 169, 242, 23, 241, 105, 145, 50, 196, 165, 42, 254, 120, 3, 54, 244, 207, 209, 85, 53, 6, 138, 106, 175, 148, 31, 204, 186, 186, 165, 182, 87, 142, 49, 10, 39, 110, 26, 154, 86, 56, 173, 125, 18, 64, 198, 225, 99, 99, 83, 82, 191, 134, 76, 170],
  ),
}

// clientDecode undoes the client's codec: a 6-byte header naming the salt,
// 32 random bytes, then AES-128-CBC under sha512(sha512(random) + salt);
// the plaintext is led by its own sha512, which is checked
function clientDecode(b64) {
  const buf = Buffer.from(b64, "base64")
  const salt = CODEC[buf.subarray(0, 6).toString("hex")]
  if (!salt || buf.length <= 102) throw new Error("not the client's codec")
  const key = createHash("sha512").update(Buffer.concat([createHash("sha512").update(buf.subarray(6, 38)).digest(), salt])).digest()
  const d = createDecipheriv("aes-128-cbc", key.subarray(0, 16), key.subarray(16, 32))
  const out = Buffer.concat([d.update(buf.subarray(38)), d.final()])
  const body = out.subarray(64)
  if (out.length < 64 || !createHash("sha512").update(body).digest().equals(out.subarray(0, 64))) throw new Error("the client's value doesn't check")
  return body.toString("utf8")
}

// thisMachine is how the client names this machine's OS: its type, and
// the OS's own version (Windows' product name, as "Windows 11 Home China"
// was seen, else its build; elsewhere the kernel's release)
function thisMachine(platform = process.platform) {
  if (platform === "win32") {
    let v = ""
    try {
      v = osVersion()
    } catch {}
    return { type: "windows", os: /^Windows \d/.test(v) ? v : `Windows ${release()}` }
  }
  return { type: platform === "darwin" ? "mac" : platform, os: `${platform === "darwin" ? "macOS" : platform} ${release()}` }
}

// ugPage: url is one of Trae's growth pages (the check-in)
const ugPage = (url) => {
  try {
    return new URL(url).pathname.startsWith("/trae/api/v2/ug/")
  } catch {
    return false
  }
}

// A model that writes a tool call as text still writes it, mostly GLM and
// DeepSeek: the blocks are read as the calls they mean (the tools also go
// natively in the body, and a past call is a native turn, so an answer is
// usually native; the text form is read for when it is not).
const OPEN = "<tool_call>"
const CLOSE = "</tool_call>"


const partText = (content) => {
  if (typeof content === "string") return content
  if (!Array.isArray(content)) return content == null ? "" : String(content)
  return content
    .map((p) => (typeof p === "string" ? p : p?.type === "text" || p?.type === "input_text" ? p.text ?? "" : p?.type === "image_url" ? "[an image, which this model can't see]" : ""))
    .filter(Boolean)
    .join("\n")
}

// imageOf is an image part of a message as Trae's chat takes one: OpenAI's
// image_url part, {url} in it, which is what TRAE's ai-agent sends
// (LLMRawMultiContent's image_url, LLMRawChatImageURL) and what the
// open-source relays pass llm_utils_chat unchanged. null for a part that
// isn't an image, or one with no url.
function imageOf(p) {
  if (p?.type !== "image_url" && p?.type !== "input_image") return null
  const url = typeof p.image_url === "string" ? p.image_url : p.image_url?.url ?? p.url
  return typeof url === "string" && url ? { type: "image_url", image_url: { url } } : null
}

// partsOf is a message's text and, for a model that sees, its images apart
// from it; a model that doesn't is told in the text that one was there
const partsOf = (content, sees) => {
  if (!sees || !Array.isArray(content)) return { text: partText(content), images: [] }
  const images = content.map(imageOf).filter(Boolean)
  return { text: partText(content.filter((p) => !imageOf(p))), images }
}

// traeMessages is the conversation as Trae's chat takes it. sees is whether
// the model takes images: Trae's list says so of each model (multimodal),
// and a model it says doesn't is sent the placeholder, not the image.
function traeMessages(req, sees = false) {
  // native is whether the body names the tools to Trae (chatBody's own
  // test). Past calls go back as Trae's own turns only then: every live
  // run of that shape carried the tools, and a request without them (a
  // summary, tool_choice "none") keeps the text form it always had
  const native = Array.isArray(req.tools) && req.tools.length > 0 && req.tool_choice !== "none"
  const names = new Map()
  const out = []
  // dropped is every call id a turn carried but Trae cannot be sent: an
  // assistant turn whose calls all lost their name takes its results with it
  const dropped = new Set()
  // images of tool results, held until the run of results ends: Trae
  // takes no message between a call's results, so they cannot go after
  // each one — one user message once they are all in
  const pending = []
  const flushImages = () => {
    if (pending.length) out.push({ role: "user", images: pending.splice(0) })
  }
  for (const m of req.messages ?? []) {
    const role = m.role === "developer" ? "system" : m.role
    let { text, images } = partsOf(m.content, sees)
    if (!native && role === "assistant" && Array.isArray(m.tool_calls) && m.tool_calls.length) {
      const calls = m.tool_calls.map((c) => {
        names.set(String(c.id ?? ""), c.function?.name)
        let args = c.function?.arguments ?? "{}"
        try {
          args = JSON.parse(args)
        } catch {}
        return OPEN + JSON.stringify({ name: c.function?.name, arguments: args }) + CLOSE
      })
      out.push({ role: "assistant", text: [text, ...calls].filter(Boolean).join("\n") })
      continue
    }
    if (role === "assistant" && Array.isArray(m.tool_calls) && m.tool_calls.length) {
      // a past call goes back as the turn Trae made it: an assistant
      // tool_calls message, its result in the tool message after it. Trae
      // answers such a history with native calls; written out as a
      // <tool_call> block in the text it answers in blocks too, and a block
      // the stream cuts short ends the turn as an error (plugins#38).
      const calls = []
      for (const c of m.tool_calls) {
        const name = String(c.function?.name ?? c.name ?? "").trim()
        // a call Trae cannot be sent is dropped here; its result goes too
        // (an empty tool_calls list and a result naming no call are not a
        // turn Trae takes)
        if (!name) {
          if (c.id != null) dropped.add(String(c.id))
          continue
        }
        names.set(String(c.id ?? ""), name)
        let args = c.function?.arguments ?? c.arguments ?? "{}"
        if (typeof args !== "string") {
          try {
            args = JSON.stringify(args)
          } catch {
            args = "{}"
          }
        }
        calls.push({ id: String(c.id ?? "call_" + (names.size + 1)), type: "function", function_call: { name, arguments: args } })
      }
      if (calls.length) out.push({ role: "assistant", text, images, calls })
      else if (text || images.length) out.push({ role: "assistant", text, images })
      continue
    }
    if (role === "assistant") {
      flushImages()
      out.push({ role: "assistant", text, images })
    } else if (role === "tool") {
      const id = String(m.tool_call_id ?? "")
      if (dropped.has(id)) continue
      // a result answering no call this history sends (the text form, or
      // a call cut away before it) is no turn Trae takes: it goes as text
      if (!native || !names.has(id)) {
        flushImages()
        out.push({ role: "user", text: `Result of ${names.get(id) || m.name || "the tool"} (call ${m.tool_call_id ?? "?"}):\n${text}`, images })
        continue
      }
      // Trae's own result turn: the call it answers, its text as parts
      // (an empty one for a result with no text). Its images wait: a
      // tool turn's image_url reaches some models (Kimi) and not others
      // (Doubao-Seed), so they go in a user message after the whole run
      // of results, which every multimodal model reads
      if (images.length) pending.push(...images)
      // Trae refuses a result with no text (4027: the calls turn reads as
      // unanswered), so one that printed nothing says so (plugins#251)
      out.push({ role: "tool", tool_call_id: id, text: text || (images.length ? "(the result's image follows)" : "(no output)") })
    } else {
      flushImages()
      out.push({ role: role === "system" ? "system" : "user", text, images })
    }
  }
  flushImages()
  // Trae takes no two messages of one role in a row, so an assistant turn is
  // folded into the turn after it: text into the calls turn it introduces
  // (agents send "let me read" and then the call, and parallel calls split
  // over two assistant turns), calls joined into one list. A message of an
  // image alone is not empty; tool results are turns of their own, each
  // answering its own call.
  const merged = []
  for (const m of out) {
    const images = m.images ?? []
    if (m.role === "tool") {
      merged.push(m)
      continue
    }
    if (m.role === "assistant") {
      const last = merged.at(-1)
      if (last && last.role === "assistant") {
        // fold: text and images join, calls append to the same list
        last.text = [last.text, m.text].filter(Boolean).join("\n\n")
        last.images = [...(last.images ?? []), ...images]
        if (m.calls) last.calls = [...(last.calls ?? []), ...m.calls]
        if (!last.text && !last.images.length && !last.calls) merged.pop()
        continue
      }
      if (!m.text && !images.length && !m.calls) continue
      merged.push({ role: "assistant", text: m.text, images, ...(m.calls ? { calls: m.calls } : {}) })
      continue
    }
    if (!m.text && !images.length) continue
    const last = merged.at(-1)
    if (last && last.role === m.role && m.role !== "system") {
      last.text = [last.text, m.text].filter(Boolean).join("\n\n")
      last.images = [...(last.images ?? []), ...images]
    } else merged.push({ role: m.role, text: m.text, images })
  }
  return merged.map((m) => {
    const content = [...(m.text ? [{ type: "text", text: m.text }] : []), ...(m.images ?? [])]
    if (m.calls) return { role: "assistant", content: content.length ? content : null, tool_calls: m.calls }
    if (m.role === "tool") return { role: "tool", tool_call_id: m.tool_call_id, content } // results of one turn stay consecutive: nothing may go between them (11148)
    return { role: m.role, content }
  })
}

// nativeTools are the tools as Trae names them: parameters a JSON string
const nativeTools = (tools) =>
  tools.map((t) => {
    const f = t.function ?? t
    return { type: "function", function: { name: f.name, description: f.description ?? "", parameters: typeof f.parameters === "string" ? f.parameters : JSON.stringify(f.parameters ?? {}) } }
  })

// SESSION carries the conversation magpie (or OpenCode) names a request by,
// from the chat.headers hook to the loader's fetch. It goes no further.
const SESSION = "x-magpie-trae-session"

// SALT is what the id is derived with, so it says nothing of the words it was
// made from. New in each process, as Trae's own session ids are one session's
// own within a run.
const SALT = randomBytes(16).toString("hex")

// conversationID is the conversation a chat belongs to, as the UUID the
// request's session_id wants: the session magpie or OpenCode names (its
// chat.headers hook), else the chat's first user message, which every later
// turn of the conversation repeats. "" when neither names one — a body that
// isn't a chat, or one with no user message in it — and the caller then makes
// a fresh id per request, as before: a made-up conversation of the request's
// own is the very splitting this is here to end.
//
// who is the account the request is made as (its uid, else its name), and it
// is part of what the id is made of, so one conversation answered by two
// accounts (magpie failing over, or rotating, inside it) is two conversations
// as Trae is concerned — an id a fresh random one never linked, and one that
// would let Trae read one conversation as another's. The cost is that such a
// conversation's two halves are counted apart again, which is what it did
// before this named one at all.
function conversationID(chat, session, who = "") {
  const account = String(who ?? "").trim()
  if (session) return uuidOf("magpie-trae-session|" + account + "|" + session)
  const first = (Array.isArray(chat?.messages) ? chat.messages : []).find((m) => m?.role === "user")
  const text = partText(first?.content ?? "")
  return text.trim() ? uuidOf("magpie-trae-first|" + account + "|" + text) : ""
}

// uuidOf is a version 4 UUID made of a hash's first 16 bytes: the shape
// randomUUID() returns, since that is what session_id carried before.
function uuidOf(s) {
  const b = Buffer.from(createHash("sha256").update(SALT + "|" + s).digest().subarray(0, 16))
  b[6] = (b[6] & 0x0f) | 0x40
  b[8] = (b[8] & 0x3f) | 0x80
  const h = b.toString("hex")
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

// sessionOf takes the session a chat.headers hook named off the headers, with
// the header: it goes no further than here. Its length is capped as magpie's
// own session ids are, the id being derived from it anyway.
function sessionOf(headers) {
  const s = String(headers.get(SESSION) ?? "").trim()
  headers.delete(SESSION)
  return s.slice(0, 128)
}

// chatBody is the request for llm_utils_chat; modelName is the model the
// function's list names for the config (its __dev one), when it named one,
// and most the max_tokens it gives that model (0: none given), which a
// request asking more is held to. A Max model (max: {base, window}) asks
// the config base by its __max model, max_tokens always set, and gives
// the prompt the room the window has beside it, as the IDE's Max mode does.
//
// session is the conversation's id, made by conversationID, so every request
// of one conversation carries one: Trae counts a conversation by the session
// the requests name, and a request naming a new session each time is counted
// as a conversation of its own — one user send that takes several steps (a
// tool result sent back, a retry, another function answering) shows up in the
// usage detail as several, each under no title at all. request_id stays a
// fresh id per request, being that request's own.
//
// levels is the model's own levels (levelsOf of its entry): the effort the
// request asks for is sent as the Trae name that model takes, and left out
// when it takes none of that name — Trae rejects a level a model has no entry
// for, and serves its default when none is asked.
function chatBody(req, fn, modelName, most = 0, max = null, session = "", sees = false, levels = null) {
  const config = max?.base ?? req.model
  const body = {
    messages: traeMessages(req, sees),
    function: fn,
    config_name: config,
    model: config,
    ...(modelName ? { model_name: modelName } : {}),
    stream: true, // Trae answers in SSE either way
    request_id: randomUUID(),
    session_id: session || randomUUID(),
  }
  const effort = effortOf(levels, req.reasoning_effort ?? req.reasoningEffort)
  if (effort) body.reasoning_effort = effort
  const asked = req.max_completion_tokens ?? req.max_tokens
  if (Number.isFinite(asked) && asked > 0) body.max_tokens = most > 0 ? Math.min(Math.floor(asked), most) : Math.floor(asked)
  if (max?.window) {
    if (!body.max_tokens && most > 0) body.max_tokens = most
    body.user_message_context = { model_info: { prompt_max_tokens: max.window - (body.max_tokens || 0) } }
  }
  if (typeof req.temperature === "number") body.temperature = req.temperature
  if (Array.isArray(req.tools) && req.tools.length && req.tool_choice !== "none") {
    body.tools = nativeTools(req.tools)
    const c = req.tool_choice
    if (c && typeof c === "object") body.tool_choice = c.function?.name || c.name || "auto"
    else if (typeof c === "string") body.tool_choice = c
    if (typeof req.parallel_tool_calls === "boolean") body.parallel_tool_calls = req.parallel_tool_calls
  }
  return body
}

// sse reads Trae's events: {event, data} for each, data parsed when JSON
async function* sse(stream) {
  const reader = stream.getReader()
  const dec = new TextDecoder()
  let buf = ""
  let event = ""
  let data = []
  const flush = function* () {
    if (!data.length) return
    const raw = data.join("\n")
    data = []
    let v = raw
    try {
      v = JSON.parse(raw)
    } catch {}
    yield { event: event || (v && typeof v === "object" ? String(v.event ?? v.type ?? "") : ""), data: v }
    event = ""
  }
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      buf += dec.decode(value, { stream: true })
      let i
      while ((i = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, i).replace(/\r$/, "")
        buf = buf.slice(i + 1)
        if (!line) {
          yield* flush()
          continue
        }
        if (line.startsWith(":")) continue
        if (line.startsWith("event:")) {
          yield* flush()
          event = line.slice(6).trim()
        } else if (line.startsWith("data:")) data.push(line.slice(5).trimStart())
      }
    }
    buf += dec.decode()
    if (buf.startsWith("data:")) data.push(buf.slice(5).trimStart())
    yield* flush()
  } finally {
    // the answer the agent goes away from (a tool call made, a request
    // aborted) closes this generator mid-read: let Trae's connection go,
    // or the read hangs on and the host the next ask reuses is one Trae
    // has dropped — a stream that runs, then dies
    reader.cancel().catch(() => {})
  }
}

// normalised event names: "TokenUsage", "token-usage" → token_usage
const eventName = (s) =>
  String(s ?? "")
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/[^A-Za-z0-9]+/g, "_")
    .toLowerCase()
    .replace(/^_|_$/g, "")

// tokensOf maps Trae's token counts to OpenAI's. Trae's token_usage event
// (TRAE SOLO CN's ai-agent, TokenUsageEvent: name, prompt_tokens,
// completion_tokens, total_tokens, reasoning_tokens,
// cache_creation_input_tokens, cache_read_input_tokens,
// prompt_tokens_total, completion_tokens_total) says what the prompt cache
// read and wrote under Anthropic's names. They go on under those names,
// which magpie reads beside prompt_tokens as it reads a relay's in front of
// Claude (whether prompt_tokens counts them too is judged there, by
// whether it is at least as big); without them every request read as no
// cache hit (01huadalang on magpie's Discord: K3, 53 calls, all 0)
function tokensOf(u) {
  if (!u || typeof u !== "object") return null
  const n = (...v) => Number(v.find((x) => x != null) ?? 0) || 0
  const p = n(u.prompt_tokens, u.input_tokens, u.promptTokens, u.inputTokens)
  const c = n(u.completion_tokens, u.output_tokens, u.completionTokens, u.outputTokens)
  if (!p && !c) return null
  const out = { prompt_tokens: p, completion_tokens: c, total_tokens: n(u.total_tokens, u.totalTokens) || p + c }
  const read = n(u.prompt_tokens_details?.cached_tokens)
  const write = n(u.prompt_tokens_details?.cache_write_tokens)
  if (read || write) out.prompt_tokens_details = { cached_tokens: read, ...(write ? { cache_write_tokens: write } : {}) }
  else {
    const r = n(u.cache_read_input_tokens, u.cacheReadInputTokens)
    const w = n(u.cache_creation_input_tokens, u.cacheCreationInputTokens)
    if (r) out.cache_read_input_tokens = r
    if (w) out.cache_creation_input_tokens = w
  }
  const thought = n(u.reasoning_tokens, u.completion_tokens_details?.reasoning_tokens)
  if (thought) out.completion_tokens_details = { reasoning_tokens: thought }
  return out
}

let callSeq = 0
const callId = () => "call_" + randomBytes(12).toString("hex") + (callSeq++).toString(36)

// TextTools splits the model's text into what the agent sees and the tool
// calls it wrote as blocks, holding back the start of a block until it
// is whole. A block is ours (<tool_call>{…}</tool_call>), its JSON taken
// once whole even when DeepSeek closes it with its own tags in place of
// </tool_call>, or DeepSeek's (<｜DSML｜invoke name="…">, magpie#823); the
// tags left around a call are dropped.
//
// GLM writes a call as its own template has it, <tool_call>name
// <arg_key>k</arg_key><arg_value>v</arg_value></tool_call>, which is read
// too; and a {"reasoning_content": …} object in the text is the model's
// reasoning, not its answer (歧路亡羊 on magpie's Discord).
class TextTools {
  constructor(tools = []) {
    this.buf = ""
    this.after = false
    this.tools = tools
    this.sent = "" // the text given out so far, to tell a block in code from a call
    this.bad = new Set() // where the waiting block's JSON was tried and not read, not tried again
    this.mark = 0 // where in sent the text after the last call starts
  }
  push(s, end = false) {
    this.buf += s
    let text = ""
    let reasoning = ""
    const calls = []
    for (;;) {
      if (this.after) {
        const m = this.buf.match(LEFTOVER)
        if (m) this.buf = this.buf.slice(m[0].length)
        if (!this.buf || (!end && MARKS.some((k) => k.startsWith(this.buf)))) break
        // a tag cut short at the end (</), after the others: dropped too
        if (end && this.buf.length > 1 && MARKS.some((k) => k.startsWith(this.buf.trimEnd()))) {
          this.buf = ""
          break
        }
        this.after = false
      }
      const om = this.buf.match(OPEN_AT)
      const i = om ? om.index : -1
      const d = this.buf.search(DSML_AT)
      const r = this.buf.search(REASON_AT)
      const sm = this.buf.match(STRAY_AT)
      const s = sm ? sm.index : -1
      const x = this.buf.search(XML_AT)
      if (s >= 0 && [i, d, r, x].every((y) => y < 0 || s < y)) {
        // a close with no call open, with what GLM left before it
        // (}}</tool_call>, magpie#1945): not the answer, dropped; in code,
        // shown
        const head = this.buf.slice(0, s)
        // only a tail: a close with nothing before it since the last call
        // (or the start), or with GLM's braces, quotes or > right before
        // it; a close a sentence names in passing stays
        const tail = /^[}\]"'>]/.test(sm[0]) || !(this.sent + text + head).slice(this.mark).trim()
        if (!tail || quoted(this.sent + text + head)) {
          text += this.buf.slice(0, s + sm[0].length)
          this.buf = this.buf.slice(s + sm[0].length)
          continue
        }
        text += head
        this.buf = this.buf.slice(s + sm[0].length)
        this.after = true
        continue
      }
      if (r >= 0 && (i < 0 || r < i) && (d < 0 || r < d) && (x < 0 || r < x)) {
        const o = objectEnd(this.buf, r)
        if (o === -1 && !end) {
          // not whole yet: held, the text before it sent
          text += this.buf.slice(0, r)
          this.buf = this.buf.slice(r)
          break
        }
        const v = o > 0 ? looseJSON(this.buf.slice(r, o)) : null
        if (v && typeof v.reasoning_content === "string") {
          text += this.buf.slice(0, r)
          reasoning += v.reasoning_content
          this.buf = this.buf.slice(o)
          continue
        }
        // not one after all: text, and what follows read on
        text += this.buf.slice(0, r + 1)
        this.buf = this.buf.slice(r + 1)
        continue
      }
      if (i < 0 && d < 0 && x < 0) break
      if (x >= 0 && (i < 0 || x < i) && (d < 0 || x < d)) {
        // Anthropic's <function_calls><invoke name="…"> or Qwen's
        // <function=…>, as Kimi and MiMo write a call (01huadalang on
        // magpie's Discord, dsh): a call only for a tool the request
        // declared, and never in a code fence or backticks
        const head = this.buf.slice(0, x)
        const at = this.buf.slice(x)
        const opener = at.match(/^<[^<>]*>?/)[0]
        if (quoted(this.sent + text + head)) {
          text += head + opener
          this.buf = at.slice(opener.length)
          continue
        }
        const b = this.xmlCall(at, end)
        if (b.wait) break
        if (b.text) {
          text += head + opener
          this.buf = at.slice(opener.length)
          continue
        }
        text += head
        if (b.error) {
          this.buf = ""
          this.sent += text
          return { text, calls, reasoning, error: b.error }
        }
        calls.push(b.call)
        this.mark = this.sent.length + text.length
        this.buf = b.rest
        this.after = true
        continue
      }
      if (d >= 0 && (i < 0 || d < i)) {
        text += this.buf.slice(0, d)
        this.buf = this.buf.slice(d)
        const tag = this.buf.match(DSML_TAG)
        if (!tag) {
          // a tag not closed yet; at the end, or with another tag after it,
          // a stray one dropped
          const next = this.buf.indexOf("<", 1)
          if (next < 0 && !end) break
          this.buf = next < 0 ? "" : this.buf.slice(next)
          continue
        }
        const inv = tag[0].match(DSML_INVOKE)
        if (!inv) {
          // function_calls, a parameter's close, any other tag: dropped,
          // with the blanks after it
          this.buf = this.buf.slice(tag[0].length)
          this.after = true
          continue
        }
        const body = this.buf.slice(tag[0].length)
        const c = body.match(DSML_CLOSE)
        const n = body.search(DSML_INVOKE_AT)
        let stop = c ? c.index : n >= 0 ? n : -1
        if (n >= 0 && n < stop) stop = n
        if (stop < 0) {
          if (!end) break
          stop = body.length
        }
        calls.push({ name: inv[1], arguments: dsmlArgs(body.slice(0, stop)) })
        this.mark = this.sent.length + text.length
        this.buf = body.slice(c && c.index === stop ? stop + c[0].length : stop)
        this.after = true
        continue
      }
      const head = this.buf.slice(0, i)
      const body = this.buf.slice(i + om[0].length)
      if (quoted(this.sent + text + head)) {
        // a <tool_call> in backticks or a code fence is prose about one
        text += head + om[0]
        this.buf = body
        continue
      }
      const named = om[1]?.match(/\bname\s*=\s*"([^"]+)"/)?.[1]
      const inner = body.trimStart()
      if (!named && (inner.startsWith("<") || (end && !inner))) {
        // no JSON in it: DeepSeek's own invoke, or only the closes of one
        // (magpie#823 again); the opener is dropped and what follows read
        // as it is
        text += head
        this.buf = inner
        this.after = true
        continue
      }
      const b = this.block(body, named, end)
      if (b.wait) break
      this.bad.clear()
      if (b.text) {
        // not a call: the opener is text, and what follows read on
        text += head + om[0]
        this.buf = body
        continue
      }
      text += head
      if (b.error) {
        // a call that can't be read is not the answer (plugins#38): the
        // turn ends as a failure, so the gateway can try it again
        this.buf = ""
        this.sent += text
        return { text, calls, reasoning, error: b.error }
      }
      calls.push(b.call)
      this.mark = this.sent.length + text.length
      this.buf = b.rest
      this.after = true
    }
    if (end) {
      // an opener the stream ended inside (<tool_call name): a call cut
      // short, not the answer (plugins#38)
      const h = this.buf.search(OPEN_HOLD)
      if (h >= 0 && !quoted(this.sent + text + this.buf.slice(0, h))) {
        text += this.buf.slice(0, h)
        this.buf = ""
        this.sent += text
        return { text, calls, reasoning, error: CUT_SHORT }
      }
      text += this.buf
      this.buf = ""
    } else {
      // keep back an opened block, or what may be the start of one
      const at = [this.buf.search(OPEN_AT), this.buf.search(OPEN_HOLD), this.buf.search(DSML_AT), this.buf.search(REASON_AT), this.buf.search(XML_AT), this.buf.search(XML_HOLD)].filter((x) => x >= 0)
      let keep = at.length ? this.buf.length - Math.min(...at) : 0
      if (!keep) for (let k = Math.min(MARK_MAX - 1, this.buf.length); k > 0; k--) if (MARKS.some((m) => m.startsWith(this.buf.slice(-k)))) { keep = k; break }
      // and braces, brackets, quotes or a > a close may still come after
      const sh = this.buf.slice(0, this.buf.length - keep).match(STRAY_HOLD)
      if (sh) keep = this.buf.length - sh.index
      text += this.buf.slice(0, this.buf.length - keep)
      this.buf = this.buf.slice(this.buf.length - keep)
    }
    this.sent += text
    return { text, calls, reasoning }
  }

  // xmlCall reads a call written in Anthropic's tags (<function_calls>
  // around one or more <invoke name="…"><parameter name="k">v</parameter>
  // </invoke>) or Qwen's (<function=…><parameter=k>v</parameter>
  // </function>), s starting at its first tag: {call, rest}, {wait}
  // while it may still become one, {text} when it is no call (a tool the
  // request didn't declare, a tag named in prose), {error} for one cut
  // short. Each invoke is a call of its own; the tags between them are
  // dropped as the leftovers of one.
  xmlCall(s, end) {
    const w = s.match(/^<(?:antml:)?function_calls>/)
    if (w) {
      const rest = s.slice(w[0].length).replace(/^\s+/, "")
      if (!end && (!rest || XML_STARTS.some((k) => k.startsWith(rest)))) return { wait: true }
      return this.xmlInvoke(rest, end)
    }
    return this.xmlInvoke(s, end)
  }

  xmlInvoke(s, end) {
    let m = s.match(/^<(?:antml:)?invoke\s+name\s*=\s*"([^"<>]+)"\s*>/)
    let closeRe = /<\/(?:antml:)?invoke\s*>/
    if (!m) {
      m = s.match(/^<function=([^\s<>]+?)\s*>/)
      closeRe = /<\/function\s*>/
    }
    if (!m) {
      // an opener still being written
      if (!end && (XML_STARTS.some((k) => k.startsWith(s)) || /^<(?:antml:)?(?:invoke\s|function=)[^<>]{0,300}$/.test(s))) return { wait: true }
      return { text: true }
    }
    const declared = this.tools.map((t) => (t.function ?? t)?.name).filter(Boolean)
    const name = toolNamed(m[1].trim(), this.tools)
    if (!declared.includes(name)) return { text: true }
    const body = s.slice(m[0].length)
    const c = body.match(closeRe)
    const n = body.search(XML_NEXT)
    let stop = c ? c.index : -1
    if (n >= 0 && (stop < 0 || n < stop)) stop = n
    if (stop < 0) {
      if (!end) return { wait: true }
      stop = body.length
    }
    const whole = !!c && c.index === stop
    const region = body.slice(0, stop).replace(/<(\/?)antml:parameter\b/g, "<$1parameter")
    const p = paramArgs(region, name, this.tools)
    if (!whole && !p.closed) {
      // no parameter and no close: a tag named in passing; a parameter
      // left open: the call cut short (plugins#38)
      if (!/<parameter\b/.test(region)) return { text: true }
      return { error: CUT_SHORT }
    }
    return { call: { name, arguments: JSON.stringify(p.args) }, rest: whole ? body.slice(stop + c[0].length) : body.slice(stop) }
  }

  // block reads what follows a <tool_call> opener: {call, rest} for a
  // call, {wait} while it may still become one, {text} when it is no call
  // at all, {error} for one that can't be read: cut short, or not JSON
  // even as models get it wrong (plugins#38). named is the name an opener
  // gave as <tool_call name="…">.
  block(body, named, end) {
    if (!end && !body.trim()) return { wait: true }
    const [close, closeLen] = closeAt(body)
    if (named) {
      if (close < 0 && !end) return { wait: true }
      const region = close >= 0 ? body.slice(0, close) : body
      const rest = close >= 0 ? body.slice(close + closeLen) : ""
      if (region.trimStart().startsWith("{")) {
        const v = repairedJSON(region.trim())
        if (!v) return { error: close >= 0 ? UNREADABLE : CUT_SHORT }
        return { call: v.name ? { name: String(v.name), arguments: callArgs(v) } : { name: named, arguments: JSON.stringify(v) }, rest }
      }
      if (region.includes("<arg_key>")) {
        const w = glmCall(named + region, this.tools)
        return w ? { call: { name: w.name, arguments: callArgs(w) }, rest } : { error: close >= 0 ? UNREADABLE : CUT_SHORT }
      }
      const p = paramArgs(region, named, this.tools)
      // cut short at the end: whole only when each parameter was closed
      if (close < 0 && !p.closed) return { error: CUT_SHORT }
      return { call: { name: named, arguments: JSON.stringify(p.args) }, rest }
    }
    if (body.trimStart().startsWith("{")) {
      const ph = body.match(PARAM_HEAD)
      if (ph) {
        // DeepSeek's half-and-half: the name as JSON, then its parameters
        // (magpie#917)
        if (close < 0 && !end) return { wait: true }
        const region = body.slice(ph[0].length, close >= 0 ? close : body.length)
        const p = paramArgs(region, ph[1], this.tools)
        if (close < 0 && !p.closed) return { error: CUT_SHORT }
        return { call: { name: ph[1], arguments: JSON.stringify(p.args) }, rest: close >= 0 ? body.slice(close + closeLen) : "" }
      }
      const o = objectEnd(body, 0)
      if (o > 0 && !this.bad.has(":" + o)) {
        const v = callJSON(body.slice(0, o))
        // a name alone may have its parameters still to come
        if (v && !end && Object.keys(v).length === 1 && "<parameter".startsWith(body.slice(o).replace(/^\s*>?\s*/, ""))) return { wait: true }
        if (v) {
          // what follows the object up to the close: GLM leaves braces,
          // brackets, quotes or a > of its own there (}}</tool_call>,
          // "></tool_call>, magpie#1945), dropped with the close; held
          // while only those have come
          const tail = body.slice(o)
          const junk = tail.match(TAIL_JUNK)[0]
          const next = tail.slice(junk.length)
          const call = { name: String(v.name), arguments: callArgs(v) }
          if (!end && (!next || ["</tool_call>", "</toolcall>"].some((k) => k.startsWith(next)))) return { wait: true }
          const [c, n] = closeAt(next)
          if (c === 0) return { call, rest: next.slice(n) }
          // after blanks alone, or with no close to come: the call, and
          // what follows read on as before. Other text before the close
          // right after the object means the object ended early, at a
          // quote inside a string left unescaped: that cut is wrong (a
          // heredoc's command cut short, magpie#1945), so it is read up to
          // the close below, or not at all
          if (!/\S/.test(junk) && !/^[^\s<]/.test(next)) return { call, rest: tail }
          if (close < 0 && end) return { call, rest: tail }
        } else this.bad.add(":" + o)
      }
      // not whole, or not JSON as it stands: read up to a close (a file's
      // content may hold one), or at the end all of it
      for (let [j, n] = [close, closeLen]; j >= 0; [j, n] = closeAt(body, j + 1)) {
        if (this.bad.has("<" + j)) continue
        const v = callJSON(body.slice(0, j))
        if (v) return { call: { name: String(v.name), arguments: callArgs(v) }, rest: body.slice(j + n) }
        this.bad.add("<" + j)
      }
      if (!end) return { wait: true }
      const v = callJSON(body)
      if (v) return { call: { name: String(v.name), arguments: callArgs(v) }, rest: "" }
      return { error: close >= 0 ? UNREADABLE : CUT_SHORT }
    }
    // GLM's own (a name, then <arg_key>/<arg_value> pairs), or text
    if (!/^\s*[\w.:-]{1,128}\s*(?:<|$)/.test(body)) return { text: true }
    if (close < 0 && !end) return { wait: true }
    const region = close >= 0 ? body.slice(0, close) : body
    const w = glmCall(region, this.tools)
    // GLM's call, cut short at the end, is still the call
    if (w) return { call: { name: w.name, arguments: callArgs(w) }, rest: close >= 0 ? body.slice(close + closeLen) : "" }
    if (/^\s*[\w.:-]{1,128}\s*<arg_key>/.test(body)) return { error: close >= 0 ? UNREADABLE : CUT_SHORT }
    return { text: true }
  }
}

const CUT_SHORT = "the model's tool call, written as text, was cut short; it is not sent on as the answer"
const UNREADABLE = "the model's tool call, written as text, can't be read; it is not sent on as the answer"

// an opener: <tool_call>, one with attributes (<tool_call name="bash">),
// or <tool_call right before its JSON; and one still being written. GLM
// also writes it without the underscore, <toolcall> (plugins#38)
const OPEN_AT = /<tool_?call(?:(\s[^<>{}]*)?>|(?=\s*\{))/
const OPEN_HOLD = /<tool_?call(?:\s[^<>{}\n]{0,200})?$/
// what GLM leaves between a call's JSON and its close: blanks, closing
// braces and brackets, quotes, a > (magpie#1945)
const TAIL_JUNK = /^[\s}\]"'>]*/
// a close with no call open, and that leftover before it: the tail of a
// call Trae's service read itself, or of one already read (magpie#1945)
const STRAY_AT = /[}\]"'>]*\s*<\/tool_?calls?>/
// the end of text that may be such a tail still being written
const STRAY_HOLD = /[}\]"'>][\s}\]"'>]*$/
// closeAt is where the next close is from i, </tool_call> or </toolcall>,
// and its length: [-1, 0] when there is none
function closeAt(s, i = 0) {
  const re = /<\/tool_?call>/g
  re.lastIndex = i
  const m = re.exec(s)
  return m ? [m.index, m[0].length] : [-1, 0]
}
// DeepSeek's half-and-half call: {"name":"x"} (or its brace or > left
// out), then <parameter name="k">v</parameter> or Qwen's <parameter=k>
const PARAM_HEAD = /^\s*\{\s*"name"\s*:\s*"([^"]+)"\s*\}?\s*>?\s*(?=<parameter\b)/
const PARAM_OPEN = /<parameter(?:\s+name\s*=\s*"([^"]+)"|=([^\s>]+))\s*>/g

// quoted says text ends inside a code fence or inline code: what comes
// next is shown, not done
function quoted(text) {
  if ((text.match(/```/g)?.length ?? 0) % 2) return true
  const line = text.slice(text.lastIndexOf("\n") + 1).replace(/```/g, "")
  return (line.match(/`/g)?.length ?? 0) % 2 === 1
}

// paramArgs reads <parameter name="k">v</parameter> (or <parameter=k>)
// arguments; a value runs to its close, the next parameter or the end. A
// value is its text where the tool's schema says string (or it isn't
// JSON), else its JSON. closed says each one had its close.
function paramArgs(s, name, tools = []) {
  const props = (tools.find((t) => (t.function ?? t)?.name === name)?.function ?? {}).parameters?.properties ?? {}
  const opens = [...s.matchAll(PARAM_OPEN)]
  const args = {}
  let closed = opens.length > 0
  opens.forEach((m, k) => {
    const from = m.index + m[0].length
    const to = k + 1 < opens.length ? opens[k + 1].index : s.length
    let v = s.slice(from, to)
    const c = v.indexOf("</parameter>")
    if (c >= 0) v = v.slice(0, c)
    else closed = false
    v = v.replace(/^\r?\n|\r?\n$/g, "")
    const key = m[1] ?? m[2]
    if (props[key]?.type === "string") {
      args[key] = v
      return
    }
    try {
      args[key] = JSON.parse(v.trim())
    } catch {
      args[key] = v
    }
  })
  return { args, closed }
}

// callJSON is a block's call ({name, …}) as a model may get its JSON
// wrong, null when it isn't one even so
function callJSON(s) {
  const v = repairedJSON(s)
  return v && typeof v.name === "string" && v.name ? v : null
}

// repairedJSON reads a JSON object as looseJSON does, and else as models
// get it wrong (plugins#38): every quote escaped ({\"name\":…}), a
// backslash that escapes nothing JSON knows (\_ \| C:\Users in a file's
// content), or quotes inside a string left unescaped ("官方" in a write's
// content)
function repairedJSON(s) {
  const v = looseJSON(s)
  if (v) return v
  const t = escapeRaw(s)
  const tries = []
  if (/^\s*\{\s*\\"/.test(s)) {
    try {
      const u = JSON.parse('"' + s.trim().replace(/[\n\r\t]/g, (c) => (c === "\n" ? "\\n" : c === "\r" ? "\\r" : "\\t")) + '"')
      tries.push(u, escapeRaw(u))
    } catch {}
    tries.push(s.replace(/\\"/g, '"'))
  }
  tries.push(fixEscapes(t), quoteFix(fixEscapes(t)))
  for (const x of tries) {
    try {
      const v = JSON.parse(x)
      if (v && typeof v === "object" && !Array.isArray(v)) return v
    } catch {}
  }
  return null
}

// fixEscapes makes a backslash that escapes nothing JSON knows a backslash
// of its own (\' is the quote it meant)
function fixEscapes(s) {
  return s.replace(/\\(u[0-9a-fA-F]{4}|["\\/bfnrt])|\\'|\\/g, (m, ok) => (ok ? m : m === "\\'" ? "'" : "\\\\"))
}

// quoteFix escapes the quotes inside strings a model left unescaped: a
// quote ends a key only before its colon, and a value only before what
// may follow the last of one (closing braces or brackets, then the end or
// a comma and the next key or value)
function quoteFix(s) {
  let out = ""
  const stack = []
  let key = false
  for (let i = 0; i < s.length; ) {
    const ch = s[i]
    if (ch !== '"') {
      if (ch === "{") {
        stack.push("{")
        key = true
      } else if (ch === "[") stack.push("[")
      else if (ch === "}" || ch === "]") stack.pop()
      else if (ch === ",") key = stack.at(-1) === "{"
      else if (ch === ":") key = false
      out += ch
      i++
      continue
    }
    const isKey = key && stack.at(-1) === "{"
    const inArray = stack.at(-1) === "["
    out += ch
    i++
    for (; i < s.length; i++) {
      const c = s[i]
      if (c === "\\") {
        out += c + (s[i + 1] ?? "")
        i++
        continue
      }
      if (c !== '"') {
        out += c
        continue
      }
      const after = s.slice(i + 1)
      const ends = isKey ? /^\s*:/.test(after) : (inArray ? /^\s*(?:[}\]]\s*)*(?:$|,\s*["{[\d\-tfn])/ : /^\s*(?:[}\]]\s*)*(?:$|,\s*")/).test(after)
      if (ends) {
        out += c
        i++
        break
      }
      out += '\\"'
    }
    key = false
  }
  return out
}

// GLM's call template: the tool's name, then each argument as
// <arg_key>k</arg_key><arg_value>v</arg_value>
const GLM_ARG = /<arg_key>\s*([\s\S]*?)\s*<\/arg_key>\s*<arg_value>([\s\S]*?)<\/arg_value>/g

// glmCall is a call GLM wrote in its template ({name, arguments}), null
// when s isn't one. A value is its text where the tool's schema says
// string (or the text isn't JSON), else its JSON, as GLM's template writes
// a value that isn't a string. A name alone is a call only to a tool the
// request has.
function glmCall(s, tools = []) {
  s = String(s ?? "").replace(/<\/tool_?call>\s*$/, "")
  const k = s.indexOf("<arg_key>")
  const name = (k < 0 ? s : s.slice(0, k)).trim()
  if (!/^[\w.:-]{1,128}$/.test(name)) return null
  if (k < 0 && !tools.some((t) => (t.function ?? t)?.name === name)) return null
  const props = (tools.find((t) => (t.function ?? t)?.name === name)?.function ?? {}).parameters?.properties ?? {}
  const args = {}
  for (const m of s.matchAll(GLM_ARG)) {
    const raw = m[2]
    if (props[m[1]]?.type === "string") {
      args[m[1]] = raw
      continue
    }
    try {
      args[m[1]] = JSON.parse(raw.trim())
    } catch {
      args[m[1]] = raw
    }
  }
  if (k >= 0 && !Object.keys(args).length) return null
  return { name, arguments: args }
}

// Trae reads a model's call for it and hands a call it could not read on as
// the native call: the wrapper the client's prompt asked for, with GLM's own
// template tags where the model wrote them and a field's close left behind
// (``{"name":"bash","arguments":{"command</arg_key><arg_value>ls</arg_value>``).
// The name then holds the call the model meant, and the call's own arguments
// — where the model wrote them — hold the rest of it.
const NAMECALL = /^\s*\{\s*"name"\s*:\s*"([^"\\]*)"\s*,\s*"(?:arguments|input|parameters)"\s*:\s*\{/
// a wrapper the service put text in front of (`bash_abstract_command_0>{…}`):
// the same shape, wherever it starts
const NAMEHEAD = /\{\s*"name"\s*:\s*"([^"\\]*)"\s*,\s*"(?:arguments|input|parameters)"\s*:\s*\{/
const NAMETAG = /<\/?arg_(?:key|value)\s*>/

// nameCall is the call a name holds when it is one the model wrote there
// ({name, arguments}), null when it isn't: a name without the wrapper, one
// whose tool the request doesn't have, or one no field could be read from.
// The wrapper and the tags are the service's own handoff, so what stands
// between them is the model's text as it wrote it — a quote it left
// unescaped is read as the quote it is, the way repairedJSON reads a file's
// content. A value is JSON where the tool's schema says the field is no
// string and the text is one; else the text, cut where the value was cut.
function nameCall(s, tools = []) {
  const text = String(s ?? "")
  // a wrapper the service put text in front of (`bash_abstract_command_0>{…}`)
  // is the call it holds, that text being no name the request has
  const at = text.indexOf("{")
  if (at < 0) return null
  const m = NAMECALL.exec(text.slice(at))
  if (!m) return null
  const name = m[1]
  const tool = tools.map((t) => t.function ?? t).find((t) => t?.name === name)
  if (!tool) return null
  const fields = nameFields(text.slice(at + m[0].length))
  const args = {}
  for (const [key, value] of fields) {
    // a field the schema says is no string is its JSON where it is one (a
    // number the model wrote, an object), else the text it is
    if (tool.parameters?.properties?.[key]?.type !== "string") {
      try {
        args[key] = JSON.parse(value.trim())
        continue
      } catch {}
    }
    args[key] = value
  }
  return Object.keys(args).length ? { name, arguments: args } : null
}

// nameFields reads a wrapper's body as its fields: a key, then its value.
// A tag stands where the service cut a colon or a quote, so a value ends at
// the tag that ends it, at the next key, or at the structure that closes it
// — and at a closing tag (`</description>`) whose name is a field's, because
// what follows such a tag is the service's own text, not the model's.
// What none of those reached is no field's text and is left out, so a call
// cut in the middle keeps what it has.
function nameFields(body) {
  const out = []
  let i = 0
  for (let n = 0; n < 64 && i < body.length; n++) {
    // between fields: blanks, a comma, or the tags that stand for either
    i += (body.slice(i).match(/^[\s,]+|^<\/?[a-z_]+\s*>/) || [""])[0].length
    if (body[i] !== '"') break
    const key = nameString(body, i, () => true)
    i = key.i
    if (!key.text) break
    const colon = body.slice(i).match(/^\s*:\s*/)
    if (colon) i += colon[0].length
    // a value the service cut at its tag: what stands after the tag is the
    // model's text (limit</arg_key><arg_value>55)
    i += (body.slice(i).match(/^(?:<\/?[a-z_]+\s*>)+/) || [""])[0].length
    let value
    if (body[i] === '"') {
      const v = nameString(body, i, (rest) => endsValue(rest))
      value = v.text
      i = v.i
      if (!value && !v.closed) break // an open quote with nothing in it
    } else {
      const rest = body.slice(i)
      const stop = valueEnd(rest)
      value = rest.slice(0, stop).replace(/^[\s,:]+/, "")
      // the field's own close: the structure that follows it, and a quote
      // the service cut it at where an escape isn't what stands before it
      value = value.replace(/[\s,}\]]+$/, "").replace(/(?<!\\)"$/, "").replace(/[\s,}\]]+$/, "")
      i += stop
      // nothing introduced a value (no colon, no quote, no tag) and none
      // stands: the wrapper is cut at the key, and an empty call read out
      // of it would be a call the model never wrote
      if (!value) break
    }
    out.push([key.text, value])
  }
  return out
}

// endsValue is what may stand after a string's closing quote: the structure
// that closes the field (`,`, `}`, `]`, the end), a tag that stands for one,
// or the next field's key.
function endsValue(rest) {
  if (/^\s*(?:[,}\]$]|<\/?[a-z_]+\s*>)/.test(rest)) return true
  return /^\s*"(?:[^"\\]|\\.)*"\s*(?::|<\/?[a-z_]+\s*>)/.test(rest)
}

// valueEnd is where an unquoted value runs out: the next key, a tag that
// ends it, or the structure that closes the field. A comma inside it is the
// model's own (sed -n 1,9p), so only a brace or a bracket closes a field.
function valueEnd(rest) {
  const stops = [
    rest.search(/"(?:(?:[^"\\<>]*)"\s*(?::|,|\s*<\/?[a-z_]+\s*>)|[^"\\<>]*<\/?[a-z_]+\s*>)/),
    rest.search(/<\/?[a-z_]+\s*>/),
    rest.search(/[}\]]/),
  ].filter((x) => x >= 0)
  return stops.length ? Math.min(...stops) : rest.length
}

// nameString reads the string at s[i] === '"', its escapes kept as written.
// ends says, for what follows a quote, whether that quote ended the string:
// a quote the model left unescaped inside a value is escaped as read on.
// `closed` is whether the model's own quote ended it, where the read ran
// into a tag or the end of the text.
function nameString(s, i, ends) {
  i++ // the opening quote
  let src = ""
  while (i < s.length) {
    const ch = s[i]
    if (ch === "\\") {
      src += s[i] + (s[i + 1] ?? "")
      i += 2
      continue
    }
    if (ch === "<") {
      // a tag ends the run: the service cut the value at it, whether it is
      // one of its own (`</arg_value>`) or a field's (`</description>`),
      // for neither is text the model wrote
      if (/^<\/?[a-z_][\w-]*\s*>/.test(s.slice(i))) break
    }
    if (ch === '"') {
      if (ends(s.slice(i + 1))) {
        i++
        return { text: decode(src), i, closed: true }
      }
      src += '\\"'
      i++
      continue
    }
    src += ch
    i++
  }
  return { text: decode(src), i, closed: false }
}

// decode is a string's own source as the string: the escapes the model wrote
// are the characters they stand for, a newline among them
function decode(src) {
  try {
    return JSON.parse('"' + escapeRaw(src) + '"')
  } catch {
    return src
  }
}

// toolNamed is the request's own name for a tool a model named otherwise
// (Read for read, list-files for list_files); name itself when the request
// has none like it. A tool it has none of goes on as named: the agent tells
// the model there's no such tool, which it can act on, where a call
// dropped here would end the turn with nothing.
function toolNamed(name, tools) {
  const names = tools.map((t) => (t.function ?? t)?.name).filter(Boolean)
  if (!names.length || names.includes(name)) return name
  const norm = (n) => String(n).toLowerCase().replace(/[^a-z0-9]/g, "")
  return names.find((n) => norm(n) === norm(name)) ?? name
}

// unwrapped is a call's arguments with the layer a model put around them
// taken off: asked for {"name":…,"arguments":{…}}, some write the
// arguments as {"arguments":{…}} again, natively or in the block, and the
// agent got {"arguments":{"content":…,"path":…}} for its write (bee2an on
// X). A lone arguments, input, parameters or args key holding an object is
// that layer when the tool has no parameter of that name.
const WRAPS = ["arguments", "input", "parameters", "args"]
function unwrapped(args, name, tools) {
  const t = tools.map((x) => x.function ?? x).find((f) => f?.name === name)
  if (!t) return args
  const props = t.parameters?.properties ?? {}
  let v = looseJSON(args)
  let changed = false
  for (let k = 0; k < 3 && v && typeof v === "object" && !Array.isArray(v); k++) {
    const keys = Object.keys(v)
    if (keys.length !== 1 || !WRAPS.includes(keys[0]) || keys[0] in props) break
    let inner = v[keys[0]]
    if (typeof inner === "string") inner = looseJSON(inner)
    if (!inner || typeof inner !== "object" || Array.isArray(inner)) break
    v = inner
    changed = true
  }
  return changed ? JSON.stringify(v) : args
}

// DeepSeek's tags, with its full-width bar or a plain one
const DSML = String.raw`<\s*\/?\s*[|｜][\s|｜]*DSML[\s|｜]*`
const DSML_AT = new RegExp(DSML)
const DSML_TAG = new RegExp("^" + DSML + "[^<>]*>")
const DSML_INVOKE = new RegExp(String.raw`^<\s*[|｜][\s|｜]*DSML[\s|｜]*invoke\s+name\s*=\s*"([^"]+)"\s*>`)
const DSML_INVOKE_AT = new RegExp(String.raw`<\s*[|｜][\s|｜]*DSML[\s|｜]*invoke\s+name\s*=`)
const DSML_CLOSE = new RegExp(String.raw`<\s*\/\s*[|｜][\s|｜]*DSML[\s|｜]*invoke\s*>`)
const DSML_PARAM = new RegExp(String.raw`<\s*[|｜][\s|｜]*DSML[\s|｜]*parameter\s+name\s*=\s*"([^"]+)"((?:\s+\w+\s*=\s*"[^"]*")*)\s*>([\s\S]*?)` + DSML + String.raw`parameter\s*>`, "g")
// Anthropic's and Qwen's call tags (xmlCall): where one starts, an
// opener still being written, the openers a wrapper is followed by, and
// the next call's opener
const XML_AT = /<(?:antml:)?(?:function_calls>|invoke\s+name\s*=|function=)/
const XML_HOLD = /<(?:antml:)?(?:invoke\s[^<>]{0,300}|function=[^<>]{0,300})$/
const XML_STARTS = ["<invoke ", "<antml:invoke ", "<function="]
const XML_NEXT = /<(?:antml:)?invoke\s+name\s*=|<function=/
// what is left after a call: blanks, our close, DeepSeek's tags but the
// next call's, the closes of Anthropic's and Qwen's
const LEFTOVER = new RegExp(String.raw`^(?:\s+|<\/tool_?calls?>|<tool_?calls>|<\/(?:antml:)?(?:function_calls|invoke|function)>|` + DSML + String.raw`(?![\s|｜]*invoke\b)[^<>]*>)+`)
// what text may be the start of, so held back
const MARKS = [OPEN, CLOSE, "<toolcall>", "</toolcall>", "<｜DSML｜", "<|DSML|", "</｜DSML｜", "</|DSML|", '{"reasoning_content"', "<function_calls>", "</function_calls>", "<antml:function_calls>", "</antml:function_calls>", "<invoke ", "<antml:invoke ", "</invoke>", "</antml:invoke>", "<function=", "</function>"]
// reasoning a model wrote into its text as JSON
const REASON_AT = /\{\s*"reasoning_content"\s*:/
const MARK_MAX = Math.max(...MARKS.map((m) => m.length))

// dsmlArgs is the arguments of a DSML invoke as JSON: a parameter marked
// string="true" is its text, any other its JSON (its text when not JSON)
function dsmlArgs(body) {
  const args = {}
  for (const p of body.matchAll(DSML_PARAM)) {
    if (/string\s*=\s*"true"/.test(p[2])) {
      args[p[1]] = p[3]
      continue
    }
    try {
      args[p[1]] = JSON.parse(p[3].trim())
    } catch {
      args[p[1]] = p[3]
    }
  }
  return JSON.stringify(args)
}

// objectEnd is where the JSON object starting at from (after blanks) ends:
// past its closing brace, -1 when it isn't whole yet, -2 when there's none
function objectEnd(s, from) {
  let k = from
  while (k < s.length && /\s/.test(s[k])) k++
  if (k >= s.length) return -1
  if (s[k] !== "{") return -2
  let depth = 0
  let str = false
  let esc = false
  for (; k < s.length; k++) {
    const ch = s[k]
    if (str) {
      if (esc) esc = false
      else if (ch === "\\") esc = true
      else if (ch === '"') str = false
      continue
    }
    if (ch === '"') str = true
    else if (ch === "{") depth++
    else if (ch === "}" && --depth === 0) return k + 1
  }
  return -1
}

// looseJSON reads a JSON object as a model writes one: a string holding a
// raw newline or tab (a file's content written out, #799) is read as if
// they were escaped. null when it isn't one even so.
function looseJSON(s) {
  for (const t of [s, escapeRaw(s)]) {
    try {
      const v = JSON.parse(t)
      if (v && typeof v === "object" && !Array.isArray(v)) return v
    } catch {}
  }
  return null
}

// escapeRaw escapes the control characters inside JSON strings
function escapeRaw(s) {
  let out = ""
  let str = false
  let esc = false
  for (const ch of String(s ?? "")) {
    if (str && !esc && ch < " ") {
      out += ch === "\n" ? "\\n" : ch === "\r" ? "\\r" : ch === "\t" ? "\\t" : "\\u" + ch.charCodeAt(0).toString(16).padStart(4, "0")
      continue
    }
    if (str && ch === "\\" && !esc) esc = true
    else {
      if (ch === '"' && !esc) str = !str
      esc = false
    }
    out += ch
  }
  return out
}

// callArgs is a tagged block's arguments as JSON: under arguments (an
// object, or a string of one), input or parameters, else the block's other
// keys, as a model that writes them beside the name means them (#799: a
// write that went out as {})
function callArgs(v) {
  let a = v.arguments ?? v.input ?? v.parameters
  if (a === undefined) {
    const { name, type, id, ...rest } = v
    a = rest
  }
  if (typeof a === "string") return a.trim() ? (looseJSON(a) ? JSON.stringify(looseJSON(a)) : a) : "{}"
  return JSON.stringify(a ?? {})
}

// nativeCall is a tool call as Trae's events carry one
function nativeCall(tc) {
  const f = tc?.function ?? tc?.function_call ?? tc
  const name = f?.name ?? tc?.tool_name ?? ""
  const index = Number.isInteger(tc?.index) ? tc.index : null
  const a = f?.arguments ?? f?.args ?? tc?.params ?? tc?.input ?? tc?.parameters ?? ""
  const args = typeof a === "string" ? a : JSON.stringify(a)
  if (!name && index === null && !tc?.id && !args) return null
  return { id: tc?.id || tc?.tool_call_id || "", index, name: String(name), arguments: args }
}

// NativeCalls puts together the tool calls Trae streams: a call comes in
// several events under one id (or index), each with the arguments so far
// or only the new piece, so a call is sent on once the answer is whole,
// not from its first event (#799: bash's command cut to `echo "T`)
class NativeCalls {
  constructor() {
    this.calls = []
  }
  push(tc) {
    const c = nativeCall(tc)
    if (!c) return
    let o = this.calls.find((x) => (c.id && x.id === c.id) || (!c.id && c.index !== null && x.index === c.index))
    if (!o && !c.id && c.index === null)
      o = this.calls.findLast((x) => x.name === c.name && (x.arguments === c.arguments || (!done(x.arguments) && c.arguments.startsWith(x.arguments))))
    // a piece with nothing to say whose it is (or an index its call's
    // first event didn't give) belongs to the call still being written
    if (!o && !c.name && !c.id) {
      o = this.calls.findLast((x) => !done(x.arguments))
      if (o && c.index !== null && o.index !== null && o.index !== c.index) o = null
    }
    if (!o) {
      if (!c.name) return
      this.calls.push({ ...c })
      return
    }
    // a name comes as the arguments do: a snapshot that grows. A name that
    // starts with what was read is that snapshot further along, and takes
    // its place — a wrapper cut short at a tag, then the whole call again.
    // A name that doesn't start with it is a piece of the name's own, or
    // one no snapshot explains, and the first is kept as it was
    if (!o.name) o.name = c.name
    else if (c.name && c.name.startsWith(o.name) && c.name !== o.name) o.name = c.name
    if (c.index !== null && o.index === null) o.index = c.index
    const prev = o.arguments
    const next = c.arguments
    if (!next || prev.startsWith(next)) return // nothing new
    if (next.startsWith(prev) || done(prev)) o.arguments = next // the arguments so far
    else if (resent(prev, next)) o.arguments = next // the whole call again, written apart (plugins#24)
    else o.arguments = prev + next // a piece
  }
  take(tools = []) {
    return this.calls.filter((c) => c.name).map(({ id, name, arguments: a }) => {
      // a name that holds the whole call, as the client's wrapper or in
      // GLM's template: that call, its arguments the name's when it gave
      // none of its own. The wrapper is read as JSON where it is whole —
      // its nesting is its own — and as the service's handoff where it was
      // left mid-call, a wrapper written with a space (`{ "name"`) whole
      // like any other
      const w = name.trimStart().startsWith("{") ? looseJSON(name) ?? nameCall(name, tools)
        : NAMEHEAD.test(name) ? nameCall(name, tools)
        : name.includes("<arg_key>") ? glmCall(name, tools) : null
      if (w?.name) {
        name = String(w.name)
        // the name's own arguments are the model's; what the name read fills
        // the fields they left (the wrapper's text is the whole call where
        // Trae moved the arguments with it, and the rest where it did not)
        a = nameArgs(a, w)
      }
      const v = a && !done(a) ? looseJSON(a) : null
      return { id: id || callId(), name, arguments: v ? JSON.stringify(v) : a || "{}" }
    })
  }
}

// nameArgs is the arguments of a call a name held: the native call's own
// where it carried them, else the name's, and the two together where each
// has a field the other hasn't — the service moves the arguments it read
// into the native call and leaves the rest in the name. The name's side is
// read the way main reads a whole name (`callArgs`: `arguments`, `input`,
// `parameters`, arguments given as a string, or the flat fields), so what
// main already read keeps reading the same. The two stand together only as
// two sets of fields; a call's own arguments that are not that — text the
// service cut, or a value that isn't a call's fields — go on as they stand,
// which is what main passed on and what the agent can answer.
function nameArgs(native, fromName) {
  const a = typeof native === "string" ? native.trim() : ""
  const named = callArgs(fromName)
  if (!a || a === "{}") return named
  const own = looseJSON(a), side = looseJSON(named)
  if (!own || !side || typeof own !== "object" || typeof side !== "object" || Array.isArray(own) || Array.isArray(side)) return a
  return JSON.stringify({ ...side, ...own })
}

// resent says next is the call's whole arguments sent again after a
// partial snapshot, not a piece of them: Trae resends a snapshot whose
// punctuation isn't the first one's (− for -, ′ for '), so it doesn't
// start with it. A whole object under the same first key as prev's, which
// prev and it joined don't make whole; a piece that is a value of its own
// (12, "ls", {}) doesn't open with prev's first key.
function resent(prev, next) {
  const key = (s) => s.match(/^\s*\{\s*"((?:[^"\\]|\\.)*)"/)?.[1]
  if (!next.trimStart().startsWith("{") || !done(next) || done(prev + next)) return false
  const k = key(prev)
  return k !== undefined && k === key(next)
}

// done says arguments are a whole JSON value already
function done(a) {
  if (!a) return false
  try {
    JSON.parse(a)
    return true
  } catch {
    return false
  }
}

// the events that carry the answer: output and message, and the names
// Trae has put a reply's text under otherwise (delta, or none at all);
// any other is queueing, metadata or timing
const ANSWER = new Set(["", "output", "message", "delta", "content", "text", "answer", "chunk", "output_delta", "message_delta"])

// finishOf is OpenAI's finish_reason for the one Trae's last event gives,
// "" for none it says or one it has no word for
function finishOf(d) {
  const f = String(d?.finish_reason ?? d?.stop_reason ?? d?.finishReason ?? "").toLowerCase()
  if (["length", "max_tokens", "max_output_tokens", "max_length"].includes(f)) return "length"
  if (f === "content_filter" || f === "sensitive") return "content_filter"
  return ""
}

// Thoughts is the reasoning sent on so far: a piece Trae sends again (the
// same one twice in a row, or the reasoning so far, again or with more
// after it) sends only what is new
class Thoughts {
  constructor() {
    this.all = ""
    this.last = ""
  }
  add(r) {
    const last = this.last
    this.last = r
    if (r === last && r.length >= 8) return ""
    if (this.all.length >= 16 && r.length >= 16 && this.all.endsWith(r)) return ""
    if (this.all.length >= 16 && r.startsWith(this.all)) r = r.slice(this.all.length)
    this.all += r
    return r
  }
}

// parts turns Trae's events into what the answer is made of: text,
// reasoning, tool calls, the token counts, the finish, or an error. tools
// are the request's, a call to one named otherwise going on under its name.
async function* parts(events, tools = []) {
  const tt = new TextTools(tools)
  const nc = new NativeCalls()
  const th = new Thoughts()
  const call = (c) => {
    const name = toolNamed(c.name, tools)
    return { call: { ...c, name, arguments: unwrapped(c.arguments, name, tools) } }
  }
  let finish = ""
  for await (const { event, data } of events) {
    const name = eventName(event)
    const d = data && typeof data === "object" ? data : {}
    if (data === "[DONE]") break
    if (name === "error" || (name !== "output" && d.code && d.message && !d.response && !d.content)) {
      const e = errorOf(d)
      // the realm answering is named by the caller, which knows it; a bare
      // word here reads as neither realm's
      yield { error: e.message || "Trae returned an error", code: e.code }
      return
    }
    if (name === "token_usage") {
      const u = tokensOf(d.usage ?? d)
      if (u) yield { usage: u }
      continue
    }
    if (name === "done" || name === "response_done" || name === "stream_done") {
      const u = tokensOf(d.usage)
      if (u) yield { usage: u }
      finish = finishOf(d) || finish
      break
    }
    if (!ANSWER.has(name)) continue
    const delta = d.delta && typeof d.delta === "object" ? d.delta : {}
    // the reasoning's own fields, in the order Trae has put it under: an
    // empty one is no answer, so the next is asked (an event carrying both
    // reasoning_content: "" and reasoning: "…" says the thought in the second)
    const said = [d.reasoning_content, d.reasoning, delta.reasoning_content, delta.reasoning].find((v) => typeof v === "string" && v) ?? ""
    if (said) {
      const r = th.add(said)
      if (r) yield { reasoning: r }
    }
    let text = typeof d.response === "string" ? d.response : typeof d.content === "string" ? d.content : typeof delta.content === "string" ? delta.content : typeof d.delta === "string" ? d.delta : ""
    // the IDE's own progress notes, not the model's
    if (/^(Building prompt:|Completed building prompt)/.test(text)) text = ""
    if (text) {
      const r = tt.push(text)
      const t = r.reasoning && th.add(r.reasoning)
      if (t) yield { reasoning: t }
      if (r.text) yield { text: r.text }
      for (const c of r.calls) yield call({ ...c, id: callId() })
      if (r.error) {
        yield { error: r.error, code: null }
        return
      }
    }
    for (const tc of Array.isArray(d.tool_calls) ? d.tool_calls : []) nc.push(tc)
    const u = tokensOf(d.usage)
    if (u) yield { usage: u }
    finish = finishOf(d) || finish
  }
  const r = tt.push("", true)
  const t = r.reasoning && th.add(r.reasoning)
  if (t) yield { reasoning: t }
  if (r.text) yield { text: r.text }
  for (const c of r.calls) yield call({ ...c, id: callId() })
  if (r.error) {
    yield { error: r.error, code: null }
    return
  }
  for (const c of nc.take(tools)) yield call(c)
  if (finish) yield { finish }
}

// first reads events up to the first that is the answer's, so an answer
// that is only an error (signed out, out of credit, a model this account
// lacks) is known before anything is sent on
async function first(it) {
  const held = []
  for (;;) {
    const n = await it.next()
    if (n.done) return { held, done: true }
    held.push(n.value)
    if (!n.value.usage) return { held, done: false }
  }
}

async function* chain(held, it) {
  yield* held
  for (;;) {
    const n = await it.next()
    if (n.done) return
    yield n.value
  }
}

// failure is the response for an error Trae answered with
function failure(site, status, code, message) {
  message = message || statusLine(status)
  if (status === 401 || lapsedCode(code, site) || (status === 403 && lapsedWords.test(message))) {
    return errorResponse(401, `${site.realm}'s sign-in has expired (${message}); sign in again`, "expired")
  }
  if (status === 429 || quotaCode(code, site) || (status === 403 && quotaWords.test(message))) return errorResponse(429, `${site.realm}: ${message}`)
  return errorResponse(status >= 400 ? status : 502, `${site.realm}: ${message}${code ? ` (code ${code})` : ""}`)
}

// openai is the answer as an OpenAI chat completion: a stream, or one body
// abort, when given, lets the answer's reader close Trae's connection the
// moment the agent goes away (a tool call made, a request cancelled): a
// generator's return can't break a read that is already waiting, so the
// fetch itself is what has to be stopped
async function openai(req, it, abort, site) {
  const id = "chatcmpl-" + randomBytes(12).toString("hex")
  const created = Math.floor(Date.now() / 1000)
  const model = req.model
  if (!req.stream) {
    let text = ""
    let reasoning = ""
    const calls = []
    let usage = null
    let finish = ""
    for await (const p of it) {
      if (p.error) return failure(site, 502, p.code, p.error)
      if (p.text) text += p.text
      if (p.reasoning) reasoning += p.reasoning
      if (p.call) calls.push(p.call)
      if (p.usage) usage = p.usage
      if (p.finish) finish = p.finish
    }
    const message = { role: "assistant", content: text || (calls.length ? null : "") }
    if (reasoning) message.reasoning_content = reasoning
    if (calls.length) message.tool_calls = calls.map((c) => ({ id: c.id, type: "function", function: { name: c.name, arguments: c.arguments } }))
    const body = { id, object: "chat.completion", created, model, choices: [{ index: 0, message, finish_reason: calls.length ? "tool_calls" : finish || "stop" }] }
    if (usage) body.usage = usage
    return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })
  }
  const enc = new TextEncoder()
  const chunk = (delta, finish = null, extra = {}) => enc.encode(`data: ${JSON.stringify({ id, object: "chat.completion.chunk", created, model, choices: [{ index: 0, delta, finish_reason: finish }], ...extra })}\n\n`)
  // a reply that failed (an error event, or Trae's stream breaking off)
  // ends with the error and [DONE], not a finish that says it stopped; the
  // stream is closed whatever happens, and an agent that goes away stops
  // the reading of Trae's. The realm answering names itself, so a Global
  // account's failure reads as Trae Global's
  const error = (message, code = null) => enc.encode(`data: ${JSON.stringify({ error: { message: `${site.realm}: ${message}`, type: "api_error", code } })}\n\n`)
  let gone = false
  const stream = new ReadableStream({
    async start(ctl) {
      let calls = 0
      let usage = null
      let finish = ""
      let failed = false
      const put = (b) => {
        if (!gone) ctl.enqueue(b)
      }
      try {
        put(chunk({ role: "assistant", content: "" }))
        for await (const p of it) {
          if (gone) break
          if (p.error) {
            put(error(p.error, p.code ?? null))
            failed = true
            break
          }
          if (p.text) put(chunk({ content: p.text }))
          if (p.reasoning) put(chunk({ reasoning_content: p.reasoning }))
          if (p.call) put(chunk({ tool_calls: [{ index: calls++, id: p.call.id, type: "function", function: { name: p.call.name, arguments: p.call.arguments } }] }))
          if (p.usage) usage = p.usage
          if (p.finish) finish = p.finish
        }
        if (!failed) {
          put(chunk({}, calls ? "tool_calls" : finish || "stop"))
          if (usage) put(enc.encode(`data: ${JSON.stringify({ id, object: "chat.completion.chunk", created, model, choices: [], usage })}\n\n`))
        }
      } catch (e) {
        try {
          put(error(e?.message ?? e))
        } catch {}
      }
      try {
        put(enc.encode("data: [DONE]\n\n"))
        if (!gone) ctl.close()
      } catch {}
    },
    cancel() {
      gone = true
      abort?.()
      it.return?.()
    },
  })
  return new Response(stream, { status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" } })
}

// a model or function this account's chat_v3 doesn't take: another may
const wrongFunction = (code) => ["4001", "4023", "1005"].includes(String(code))

// ---- the model lists ------------------------------------------------------------

// chatModel: an entry of a function's list that is a model to chat with.
// The lists also hold the IDE's own helpers (summary, fast_apply, title
// generation: usage other than chat_completion), configs switched off, and
// the slots of custom models, which need a provider bound in the IDE.
function chatModel(m, site) {
  const id = String(m?.config_name ?? "")
  if (!id || /^custom_model/i.test(id)) return false
  if (m.usage && m.usage !== "chat_completion") return false
  if (m.config_switch === false) return false
  if (m.is_custom_model === true || m.display_config?.is_custom_model === true) return false
  // the IDE's own picker hides these: Auto-mode routing channels and
  // pay-as-you-go twins of another entry (gemini-3.1-pro vs -paygo/-auto,
  // the search_agent family), not entries to pick by hand
  if (site?.hidesInvisible && m.is_invisible_to_user === true) return false
  return true
}

// seesOf is whether a list's entry says its model takes images: Trae's
// lists mark each one multimodal true or false (Doubao-Seed, Kimi K3,
// Qwen 3.7 Plus take them; GLM-5 and DeepSeek V4 don't). null when the
// entry doesn't say.
const seesOf = (m) => (typeof m?.multimodal === "boolean" ? m.multimodal : typeof m?.display_config?.multimodal === "boolean" ? m.display_config.multimodal : null)

// devModel is the model an entry serves requests with: its __dev one
const devModel = (m) => (Array.isArray(m?.model_detail_list) ? m.model_detail_list : []).find((d) => /__dev$/.test(String(d?.model_name ?? "")))

// maxModel is a model's Max mode, from the entries its lists give it (the
// one it is asked through first): the first __max model one names, and the
// window context_window_tokens.max gives it, when that is bigger than dev's.
// The lists needn't agree: chat_v3 can name the __max model while the SOLO
// list the model is asked through names only __dev, and the window can be
// another entry's again. A model with no __max, or no bigger window, has no
// Max.
const maxModel = (...ms) => {
  const details = (m) => (Array.isArray(m?.model_detail_list) ? m.model_detail_list : [])
  const at = ms.find((m) => details(m).some((d) => /__max$/.test(String(d?.model_name ?? ""))))
  if (!at) return null
  const d = details(at).find((d) => /__max$/.test(String(d?.model_name ?? "")))
  const first = (k) => [at, ...ms].map((m) => Number(m?.context_window_tokens?.[k]) || 0).find((n) => n > 0) || 0
  const window = first("max")
  return window > first("dev") ? { name: String(d.model_name), most: Number(d.max_tokens) || 0, window, entry: at } : null
}

// MAX is the suffix of a model's Max: deepseek-v4.1-flash-max is
// deepseek-v4.1-flash in Max mode
const MAX = "-max"

// ---- reasoning levels --------------------------------------------------------------
//
// A list entry carries a reasoning_effort_config: the levels its model takes
// (light, high, extra_high) and the one it defaults to. The request takes the
// same names, as reasoning_effort, so a level an agent picks is sent as Trae
// spells it. OpenCode knows the three as low/high/xhigh, so the map goes both
// ways.
//
// An entry without the config takes no level: Trae serves it at its own
// default and nothing is sent. The plugin used to send none for every model,
// which read as "Trae takes no level" — it takes them, per model, and an
// entry that names none is the exception rather than the rule.
const LEVELS = { light: "low", high: "high", extra_high: "xhigh" }

// levelsOf is an entry's levels in OpenCode's names, in Trae's order: [] when
// the entry names none, null when it names ones this map can't spell (so a
// caller can tell "takes no level" from "takes a level we can't name").
const levelsOf = (m) => {
  const cfg = m?.reasoning_effort_config
  if (!cfg || typeof cfg !== "object") return []
  const options = Array.isArray(cfg.options) ? cfg.options : []
  const known = options.filter((o) => typeof o === "string" && LEVELS[o])
  return known.length ? known.map((o) => LEVELS[o]) : options.length ? null : []
}

// variantsOf is a model's levels as OpenCode's variants. The value is the
// effort OpenCode puts in the request body, so it names the level the way this
// plugin's levels do: an agent in OpenCode writes `reasoning_effort` straight
// from it, and effortOf below turns that name into Trae's. Naming Trae's
// spelling here would make the body say "light", which is not one of the
// levels this plugin offers, and the field would then be dropped. {} when the
// model takes none.
const variantsOf = (levels) => Object.fromEntries((levels ?? []).map((level) => [level, { reasoningEffort: level }]))

// effortOf is the name to send Trae for an effort the request carries, or
// undefined when the model doesn't take that one: a level Trae has no entry for
// is rejected, so nothing is sent instead. The effort arrives in either
// spelling — this plugin's own (low/high/xhigh) from its variants, or Trae's
// (light/extra_high) from a client that read them off the list — and both name
// the same level.
const effortOf = (levels, effort) => {
  if (typeof effort !== "string" || !levels) return undefined
  const mine = Object.values(LEVELS).includes(effort) ? effort
    : Object.prototype.hasOwnProperty.call(LEVELS, effort) ? LEVELS[effort]
    : undefined
  if (mine === undefined || !levels.includes(mine)) return undefined
  return Object.keys(LEVELS).find((k) => LEVELS[k] === mine)
}

// ---- usage ------------------------------------------------------------------------

// credits adds up the account's entitlement packs as the IDE shows them:
// each pack with a credit limit (-1 unlimited) and what it used
function credits(v) {
  let limit = 0
  let used = 0
  let unlimited = false
  let soonest = 0
  for (const p of v?.user_entitlement_pack_list ?? []) {
    const bi = p?.entitlement_base_info ?? {}
    const l = Number(bi?.quota?.credits_limit)
    if (!Number.isFinite(l)) continue
    const u = Number(p?.usage?.credits_amount) || 0
    if (l === -1) unlimited = true
    else if (l >= 0) limit += l
    if (u > 0) used += u
    const end = whenOf(bi.end_time ?? bi.expire_time ?? p.end_time)
    if (end && (!soonest || end < soonest)) soonest = end
  }
  return { limit, used, unlimited, until: soonest }
}

const round = (n) => Math.round(n * 100) / 100

// dollarUsageOf reads an account's entitlement pack as the dollar billing
// has it (the international deployment): the period's allowance in dollars
// (basic_usage_limit plus bonus_usage_limit) and what was used of each
// (basic_usage_amount, bonus_usage_amount). null when the answer carries no
// pack.
function dollarUsageOf(v) {
  const pack = (v?.user_entitlement_pack_list ?? []).find((p) => p?.usage || p?.entitlement_base_info?.quota)
  const quota = pack?.entitlement_base_info?.quota
  if (!pack || !quota) return null
  const basic = Number(quota.basic_usage_limit) || 0
  const bonus = Number(quota.bonus_usage_limit) || 0
  const used = Number(pack.usage?.basic_usage_amount) || 0
  const usedBonus = Number(pack.usage?.bonus_usage_amount) || 0
  const end = whenOf(pack.entitlement_base_info.end_time)
  return { plan: String(pack.display_desc ?? ""), basic, bonus, used, usedBonus, end }
}

// ---- the plugin ---------------------------------------------------------------------

const makePlugin = (site) => async ({ client }) => {
  // renewals under way, one to a refresh token (Trae spends it once), and
  // the last each account got here, so a sign-in from before it is given
  // that one rather than spending the token again
  const renewing = new Map()
  const renewed = new Map()
  // the chat function that served each account last
  const fnOf = new Map()
  // the chat function whose model list has each model, and the __dev
  // model it names there
  const listedBy = new Map()
  const modelNames = new Map()
  // the Max each model the list last read has (null: none)
  const maxes = new Map()
  // whether each model the list last read takes images (its multimodal)
  const sight = new Map()
  // the entry the lists last gave each model, for what only the entry says:
  // the reasoning levels it takes (reasoning_effort_config)
  const liveEntry = new Map()
  const liveEntryOf = (id) => liveEntry.get(String(id)) ?? site.models?.[String(id)]?.entry ?? null
  // when each account's list was last read, and what that read returned:
  // {at, p, done, expired}. A read that answered, failed or hung all leave
  // their stamp, so a list that fails or leaves the model out costs one read
  // per REREAD_MS rather than one per request. The promise is kept beside the
  // stamp because a caller that arrives while a read is in flight must ride
  // THAT read, not start a second and not skip the answer it was about to
  // learn: agents send parallel requests at a session's start (a title request
  // beside the first turn, subagents), and a caller that skipped the entry a
  // read just produced would send a Max out under the agent's own id, which
  // Trae's list doesn't name.
  //
  // The record is the thing the promise resolves to, so a joiner sees the same
  // outcome the read settled on — including an expiry it hit — rather than the
  // bare `null` a failed read answers. One account per key; a read is that
  // account's.
  const readAt = new Map()
  // runRead starts the one read behind readList and answers its record. `p`
  // resolves to that record — not to the models — so a caller that joins a
  // read in flight sees the same outcome the read settled on, an expiry
  // included, rather than the bare null a failed read answers.
  const runRead = (a, who) => {
    const rec = { at: Date.now(), p: null, done: undefined, expired: null }
    // the record goes in BEFORE the read starts, so a caller arriving during it
    // finds `rec.p` and joins; `p` resolves to this same record
    readAt.set(who, rec)
    const body = (async () => {
      try {
        return await liveModels(a)
      } catch (e) {
        // a request's read swallows this (the chat can still answer); a caller
        // that must hear it reads `expired` off the record. Recorded on the
        // record, so a joiner hears it too.
        if (e instanceof Expired) rec.expired = e
        return null
      }
    })()
    // every rejection is claimed here, at creation: a read nobody awaits (the
    // host's refresh ending early, a caller that walked away) must not surface
    // as an unhandled rejection. `p` itself never rejects — the expiry rides on
    // the record instead.
    body.catch(() => {})
    rec.p = body.then((models) => {
      // only this read settles its own record: a later read may have replaced it
      if (readAt.get(who) === rec) {
        rec.at = Date.now()
        rec.done = models ?? []
        rec.p = null
      }
      return rec
    })
    rec.p.catch(() => {})
    return rec.p
  }
  // readList answers the models of the read this caller rode on (its own, or
  // the one in flight, whose record it waits for and then reads), and whether
  // that read found the account lapsed. The record is the account's, not the
  // token's: a re-sign-in is the same account's list, and the host's refresh is
  // strict, so it reads again rather than serving a cached lapse. A request
  // path only reaches here for a model it has no entry for, and that read is
  // the one that fills the entry in.
  //
  // The lapse is answered, not thrown: several callers can ride one read, and a
  // throw out of a shared promise surfaces once as an unhandled rejection for
  // the callers that did not expect it. Each caller decides — provider.models
  // raises it, a request path keeps going. `strict` only says whether a cached
  // lapse is worth reading again for (the host wants the truth; a request does
  // not pay a round trip to learn it).
  const readList = async (a, { strict = false } = {}) => {
    const who = a.uid || a.name || String(site.id)
    const was = readAt.get(who)
    let rec
    if (was === undefined) rec = await runRead(a, who)
    else if (was.p) rec = await was.p
    else if (was.done === undefined || Date.now() - was.at >= REREAD_MS) rec = await runRead(a, who)
    else if (strict && was.expired) rec = await runRead(a, who)
    else rec = was
    return { models: rec.done, expired: rec.expired }
  }

  const save = async (auth) => {
    try {
      await client?.auth?.set?.({ path: { id: site.id }, body: auth })
    } catch {}
  }

  const renew = (a, keep) => {
    const key = a.refresh
    let r = renewing.get(key)
    if (!r) {
      r = (async () => {
        const x = await renewal(a, site)
        const got = { ...a, token: x.token, refresh: x.refresh, expires: x.expires || jwtExp(x.token) || Date.now() + DAY, clientId: x.clientId || a.clientId, renewed: true }
        if (x.devicePrivateKey) Object.assign(got, { devicePublicKey: x.devicePublicKey, devicePrivateKey: x.devicePrivateKey })
        renewed.set(a.uid || a.name, { was: a.refresh, got })
        if (keep) await save(toAuth(got, site))
        return got
      })().finally(() => renewing.delete(key))
      renewing.set(key, r)
    }
    return r
  }

  // fresh is the account with a token that has a while to go, renewed
  // when it hasn't
  const fresh = async (getAuth) => {
    let a = fromAuth(await getAuth(), site)
    if (!a) throw new Expired(`${site.realm}: not signed in`)
    // a renewal the sign-in doesn't hold yet. A device-bound one keeps the
    // refresh token, so the token tells it, and it is renewed in its turn
    if (newerHere(a)) a = { ...renewed.get(a.uid || a.name).got, renewed: false }
    if (!a.expires || a.expires - Date.now() > EARLY_MS || !a.refresh) return a
    try {
      return await renew(a, true)
    } catch (e) {
      // a hiccup: the token in hand may still do
      if (!(e instanceof Expired) && a.expires > Date.now()) return a
      throw e
    }
  }

  // refresh is magpie's auth.refresh: the token renewed LEAD_MS before its
  // end, as the fields that changed
  const refresh = async (auth) => {
    const a = fromAuth(auth, site)
    if (!a || !a.refresh) return undefined
    const last = renewed.get(a.uid || a.name)
    // a device key made for the account is kept with the sign-in
    const out = (s) => ({
      access: s.token,
      refresh: s.refresh,
      expires: s.expires,
      ...(s.devicePrivateKey && s.devicePrivateKey !== a.devicePrivateKey ? { devicePublicKey: s.devicePublicKey, devicePrivateKey: s.devicePrivateKey } : {}),
    })
    if (newerHere(a)) return out(last.got)
    return out(await renew(a, false))
  }

  // newerHere: this account was renewed here since the sign-in a was read
  // from, to a token that runs longer
  const newerHere = (a) => {
    const last = renewed.get(a.uid || a.name)
    return !!last && last.was === a.refresh && last.got.token !== a.token && (last.got.expires || 0) > (a.expires || 0)
  }

  // post asks one of Trae's JSON pages with the account
  const post = async (a, url, body) => {
    const res = await fetch(url, { method: "POST", headers: ideHeaders(a, site, { Accept: "application/json" }), body: JSON.stringify(body), signal: AbortSignal.timeout(20_000) })
    const text = await res.text()
    return { status: res.status, text, v: parseJSON(text) }
  }

  // ownRequest sends a request for one of Trae CN's own JSON pages as the
  // account, as its IDE does: magpie asks the daily check-in
  // (/trae/api/v2/ug/checkin_credits/status, then /claim) through the
  // account's fetch, as it does WorkBuddy's. Trae's answer comes back as it
  // is; a token it turned away (401, or code 1001 in a 200) marks the
  // sign-in, as usage does.
  const ownRequest = async (getAuth, a0, url, method, body, signal) => {
    let a
    try {
      a = await fresh(getAuth)
    } catch (e) {
      if (e instanceof Expired) return errorResponse(401, `${a0.name || a0.uid}: ${e.message}`, "expired")
      throw e
    }
    // a growth page (the check-in) goes as the IDE sends it: its headers,
    // a client's User-Agent, and {req_source: 1} for an empty body. Every
    // check-in sent with Bun's User-Agent came back 9074, with the chat's
    // headers and {} (0.1.6, 0.1.9) as with the IDE's (0.1.7)
    // (yetone/magpie#808). Other pages go with the chat's headers.
    const ug = ugPage(url)
    let sent = method === "GET" || method === "HEAD" ? undefined : typeof body === "string" ? body : "{}"
    if (ug && sent !== undefined && /^\s*(\{\s*\})?\s*$/.test(sent)) sent = JSON.stringify({ req_source: 1 })
    // the account this machine's own Trae CN client is signed in as checks
    // in on the client's device, the one Trae registered: on one account
    // the client's own claim went through while the plugin's, on a device
    // it had made up, came back 9074 (yetone/magpie#808, #1920). Every
    // other account keeps its own device; none is given the client's.
    const deviceId = ug && site.id === "trae-cn" ? await clientDevice(a.uid) : undefined
    const own = deviceId ? { deviceId, ...thisMachine() } : undefined
    // every other Trae CN account checks in on its own device, in the
    // client's shape (ugDevice, #194)
    if (ug && site.id === "trae-cn" && !own) a = { ...a, deviceId: ugDevice(a) }
    const res = await fetch(url, {
      method,
      headers: ug ? ugHeaders(a, site, { Accept: "application/json" }, own) : ideHeaders(a, site, { Accept: "application/json" }),
      body: sent,
      signal: signal ?? AbortSignal.timeout(20_000),
    })
    const text = await res.text()
    const lapsed = res.status === 401 || lapsedCode(errorOf(text).code, site)
    const headers = { "content-type": res.headers.get("content-type") ?? "application/json", "X-Magpie-Sign-In": lapsed ? "expired" : a.renewed ? "renewed" : "kept" }
    return new Response(text, { status: res.status, headers })
  }

  // checkinStatus reads the check-in's status page: its body, or an outcome
  // to give back as it is (a turned-away sign-in, Trae's own error, a reply
  // that isn't one). checkin reads it before the claim, and again after it.
  const checkinStatus = async (getAuth, a0) => {
    const res = await ownRequest(getAuth, a0, site.hosts.auth + site.checkin.status.path, "POST", "{}")
    const text = await res.text()
    let body
    try { body = JSON.parse(text) } catch {}
    if (!body || typeof body !== "object" || Array.isArray(body)) return { outcome: { outcome: "failed", message: `check-in status: unreadable reply ${text.trim().slice(0, 120) || "(empty)"}` } }
    const e = errorOf(body)
    if (res.status === 401 || lapsedCode(e.code, site)) return { outcome: { outcome: "failed", message: e.message || `${a0.name || a0.uid}: Trae's sign-in has expired` } }
    if (res.status !== 200 || (e.code && String(e.code) !== "0")) return { outcome: { outcome: "failed", message: e.message || statusLine(res.status) } }
    return { body }
  }

  const checkin = async (getAuth) => {
    if (!site.checkin) return { outcome: "inactive", message: `${site.realm} has no daily check-in` }
    const a0 = fromAuth(await getAuth(), site)
    if (!a0) return { outcome: "failed", message: "not signed in" }
    const first = await checkinStatus(getAuth, a0)
    if (first.outcome) return first.outcome
    const body = first.body
    if (body.checked_in) return { outcome: "done", ...(Number(body.credits) > 0 ? { credit: Number(body.credits) } : {}) }
    if (!body.enable) return { outcome: "inactive" }
    const claim = await ownRequest(getAuth, a0, site.hosts.auth + site.checkin.claim.path, "POST", "{}")
    const claimText = await claim.text()
    let answer
    try { answer = JSON.parse(claimText) } catch {}
    const ce = errorOf(answer)
    if (claim.status === 401 || lapsedCode(ce.code, site)) return { outcome: "failed", message: ce.message || `${a0.name || a0.uid}: Trae's sign-in has expired` }
    // 9095: this device is in for today, whoever took it — the day's answer
    if (String(ce.code) === "9095") return { outcome: "ineligible", message: ce.message || "this device has checked in today" }
    // 9074: Trae did not take this claim for this account's device. Its own
    // words say the pool is busy, which measurements do not bear out: the
    // same account, token and headers were taken on a 16-digit id where a
    // 19-digit one got 9074 (plugins#194), and on 2026-10-23 the same
    // account and headers were taken on a device id Trae had never seen
    // while this account's own id answered 9074. So the message says what
    // Trae said and that the device is what it refused, rather than
    // sending people to check in in Trae CN's app, which is not where the
    // problem is. No device id is made up or rotated to get round this:
    // the account keeps its own until a client of its own is here to sign
    // it in. Nothing is said of signing one in: it is the one thing seen
    // to change a 9074, but it hasn't been tried on a real account, and
    // on a claim that already rode a client's device it is what the user
    // did — the advice would only send them back to do it again.
    if (String(ce.code) === "9074") return { outcome: "ineligible", message: `${ce.message} (9074) — Trae CN did not take this check-in for this account's device` }
    if (claim.status !== 200 || (ce.code && String(ce.code) !== "0")) return { outcome: "failed", message: ce.message || statusLine(claim.status) }
    if (!answer || typeof answer !== "object" || Array.isArray(answer)) return { outcome: "failed", message: `check-in claim: unreadable reply ${claimText.trim().slice(0, 120) || "(empty)"}` }
    // a claim's own code 0 is not proof the day is in: Trae answers it for
    // a claim that registered nothing, which cli2api works around by asking
    // the status again (internal/providers/trae/checkin.go). So success is
    // what the status page says after the claim, and one that didn't land
    // is never reported as claimed.
    //
    // Nor is it a `failed`. magpie retries a failed check-in with backoff
    // for the rest of the day, up to every 30 minutes, and each retry is
    // another claim: a claim answered code 0 while the status still says
    // the day is out is the very case that would multiply, since code 0
    // for a claim that registered nothing is this plugin's own premise —
    // an account that claimed once a day would claim many times. A status
    // that lags a claim that did land reads as not in once, which is a
    // false failure and a second claim. So once a claim has been sent,
    // what the read-back says is the day's answer and nothing is claimed
    // again: `ineligible`, as a 9095 is. A read-back that itself comes to
    // nothing — the network, an answer that isn't one — is the same: the
    // claim was made, and whether the day is in is not known, so it is
    // read again on the next press rather than claimed again.
    const noMore = (o) => (o.outcome === "failed" ? { outcome: "ineligible", message: `${o.message} — nothing is claimed again today` } : o)
    const back = await checkinStatus(getAuth, a0)
    if (back.outcome) return noMore(back.outcome)
    if (!back.body.checked_in) return { outcome: "ineligible", message: `${site.realm}: the claim answered success, but the day's check-in is not in — nothing is claimed again today` }
    // The credit is the day's credits, never credits plus extra_credits.
    // Trae CN's real claim answers {"code":0,"message":"success"}, with no
    // credit at all (plugins#194), so the status's credits stand in. Its
    // status carries extra_credits beside credits, at the same 100 on a
    // real account (yetone/magpie#808: credits 100, extra_credits 100),
    // while the account gets 100: adding the two showed 200 (zhujiu on
    // magpie's Discord, 10-25). A claim that names its own credit is
    // taken first; one naming extra_credits alone is that (#260). Either
    // may be absent, and Number(undefined) is NaN, which reads as none.
    const n = (v) => (Number.isFinite(Number(v)) ? Number(v) : 0)
    const credit = n(answer.credits) || n(answer.extra_credits) || n(back.body.credits)
    return { outcome: "claimed", ...(credit > 0 ? { credit } : {}) }
  }

  const usage = async (getAuth) => {
    let a
    try {
      a = await fresh(getAuth)
    } catch (e) {
      return { error: e.message, signIn: e instanceof Expired ? "expired" : "kept" }
    }
    const signIn = a.renewed ? "renewed" : "kept"
    let r
    try {
      r = await post(a, site.usage.host(site, a) + site.usage.path, site.usage.body)
    } catch (e) {
      return { error: `${site.realm} ${site.usage.label}: ` + (e?.message ?? e), signIn }
    }
    const e = errorOf(r.v)
    if (r.status === 401 || lapsedCode(e.code, site)) return { error: `${a.name || a.uid}: ${site.realm}'s sign-in has expired — sign in again`, signIn: "expired" }
    if (r.status !== 200 || (e.code && String(e.code) !== "0")) return { error: `${site.realm} ${site.usage.label}: ${e.message || statusLine(r.status)}`, signIn, user: a.name }
    return site.usage.read(r.v, a, signIn, { credits, round, statusLine })
  }

  // batchLists is every chat function's model list in one ask, as TRAE
  // SOLO CN's ai-agent asks (batch_get_detail_param): {function: entries}.
  // null when Trae answers it with no lists.
  const batchLists = async (a) => {
    const r = await post(a, apiOf(a, site) + "/api/ide/v1/batch_get_detail_param", {
      functions: site.functions, agent_type: "", current_config_info: { config_name: "", is_custom_model: false },
      mode_type: 0, access_type: 0, ab_force_vids: "", ab_autotest_advanced_mode: 0, show_custom_model: true,
    })
    if (r.status === 401 || lapsedCode(errorOf(r.v).code, site)) throw new Expired(`${site.realm}'s sign-in has expired; sign in again`)
    if (r.status !== 200) throw new Error(`${site.realm} models: ${statusLine(r.status)}`)
    const groups = r.v.function_configs ?? r.v.data?.function_configs
    if (!Array.isArray(groups)) return null
    const out = {}
    for (const g of groups) {
      const fn = String(g?.function ?? "")
      if (!site.functions.includes(fn) || !Array.isArray(g.config_info_list)) continue
      out[fn] = [...(out[fn] ?? []), ...g.config_info_list.filter((m) => m?.config_name)]
    }
    return Object.keys(out).length ? out : null
  }

  // listOf is one function's model list, as the IDE asks for it
  const listOf = async (a, fn) => {
    const r = await post(a, apiOf(a, site) + "/api/ide/v1/get_detail_param", {
      function: fn, config_names: null, need_prompt: false, current_config_info: null, poly_prompt: true, mode_type: null, agent_type: null,
    })
    if (r.status === 401 || lapsedCode(errorOf(r.v).code, site)) throw new Expired(`${site.realm}'s sign-in has expired; sign in again`)
    if (r.status !== 200) throw new Error(`${site.realm} models: ${statusLine(r.status)}`)
    const list = r.v.config_info_list ?? r.v.data?.config_info_list ?? []
    return list.filter((m) => m?.config_name)
  }

  // liveModels is the account's model list: every chat function's, since
  // SOLO lists models the classic IDE's chat_v3 doesn't (deepseek-v4.1-flash
  // came to the TRAE agent's first, yetone/magpie#681); a model listed by one function
  // only is asked through that one. One function's list failing leaves the
  // others'; all failing is the error.
  //
  // The lists come from batch_get_detail_param, as TRAE SOLO CN asks them;
  // when that gives none, from get_detail_param, one function at a time.
  // A model several functions list is asked through the first whose entry
  // names a __dev model (one Trae serves) and isn't misnamed, else the
  // first that names one, else the first.
  const liveModels = async (a) => {
    let lists = null
    try {
      lists = await batchLists(a)
    } catch (e) {
      if (e instanceof Expired) throw e
    }
    if (!lists) {
      const got = await Promise.allSettled(site.functions.map((fn) => listOf(a, fn)))
      const expired = got.find((g) => g.status === "rejected" && g.reason instanceof Expired)
      if (expired) throw expired.reason
      if (got.every((g) => g.status === "rejected")) throw got[0].reason
      lists = {}
      got.forEach((g, i) => {
        if (g.status === "fulfilled") lists[site.functions[i]] = g.value
      })
    }
    // a function can give a model another's name: chat_v3 calls
    // deepseek-v4.1-flash "DeepSeek-V4-Flash 正式版", as it does
    // DeepSeek-V4-Flash-Official, where SOLO's lists call it
    // DeepSeek-V4.1-Flash (yetone/magpie#681) — Trae's usage page then books
    // a request under the name of the list it was served through. Such an
    // entry is misnamed: its name is another model's in the same list, and
    // other lists name it otherwise.
    const nameOf = (m) => String(m.display_config?.display_name || m.display_name || m.display_model_name || m.config_name)
    const names = new Map() // id → the names its lists give it, and how many give each
    for (const fn of site.functions) {
      for (const m of lists[fn] ?? []) {
        if (!chatModel(m, site)) continue
        const id = String(m.config_name), name = nameOf(m)
        const by = names.get(id) ?? new Map()
        by.set(name, (by.get(name) ?? 0) + 1)
        names.set(id, by)
      }
    }
    // the name most of a model's lists give it: the one Trae's own client is
    // served under, and so the one its usage page books it as. Trae renames
    // entries when it likes, and its 2026-10-09 rename of
    // DeepSeek-V4-Flash-Official left chat_v3 the only list still calling
    // deepseek-v4.1-flash by another's name — which neither signal above
    // catches. A tie leaves the name to its entry.
    const agreed = new Map() // id → that name, "" when the lists tie
    for (const [id, by] of names) {
      const top = Math.max(...by.values())
      const most = [...by].filter(([, n]) => n === top)
      agreed.set(id, most.length === 1 ? most[0][0] : "")
    }
    const misnamed = (fn, m) => {
      const id = String(m.config_name), name = nameOf(m)
      return names.get(id).size > 1 && (lists[fn] ?? []).some((o) => chatModel(o, site) && String(o.config_name) !== id && nameOf(o) === name)
    }
    // a function's entry is worth more when it names a __dev model, then
    // when it isn't misnamed, then when it carries the name its lists agree
    // on; the first of the best is taken. Those three decide first, so the
    // entry kept is the one Trae's own client asks through.
    //
    // Last among equals comes whether the entry names reasoning levels: the
    // entry kept is the one the request is asked through, and so the one
    // whose reasoning_effort_config the request is held to (liveEntryOf). An
    // entry that names none while a sibling names some would leave the model
    // with no level to pick although Trae serves it with levels — doubling
    // the three keeps them decisive and lets this only break a tie.
    const worth = (fn, m) => ((devModel(m) ? 2 : 0) + (misnamed(fn, m) ? 0 : 1) + (nameOf(m) === agreed.get(String(m.config_name)) ? 1 : 0)) * 2 + (levelsOf(m)?.length ? 1 : 0)
    const out = new Map()
    for (const fn of site.functions) {
      for (const m of lists[fn] ?? []) {
        if (!chatModel(m, site)) continue
        const id = String(m.config_name)
        const was = out.get(id)
        if (!was || worth(fn, m) > was.worth) out.set(id, { m, fn, worth: worth(fn, m) })
      }
    }
    for (const [id, { m, fn }] of out) {
      listedBy.set(id, fn)
      liveEntry.set(id, m)
      const sees = seesOf(m)
      if (sees === null) sight.delete(id)
      else sight.set(id, sees)
      const dev = devModel(m)
      if (dev) modelNames.set(id, { fn, name: dev.model_name, most: Number(dev.max_tokens) || 0 })
      else modelNames.delete(id)
      if (out.has(id + MAX)) continue
      // its Max is asked through the function whose entry names the __max
      // model, the entries carrying the agreed name first: Trae books the
      // Max under that name too
      const plain = (e) => (nameOf(e.m) === agreed.get(id) ? 0 : 1)
      const entries = [{ m, fn }, ...site.functions.flatMap((f) => (lists[f] ?? []).filter((o) => o !== m && chatModel(o, site) && String(o.config_name) === id).map((o) => ({ m: o, fn: f })))].sort((a, b) => plain(a) - plain(b))
      const max = maxModel(...entries.map((e) => e.m))
      maxes.set(id, max)
      if (max) {
        const by = entries.find((e) => e.m === max.entry).fn
        listedBy.set(id + MAX, by)
        modelNames.set(id + MAX, { fn: by, name: max.name, most: max.most, max: { base: id, window: max.window } })
      } else {
        listedBy.delete(id + MAX)
        modelNames.delete(id + MAX)
      }
    }
    // this account's list has just answered: recorded, so a request that wants
    // levels for a model the list doesn't name waits REREAD_MS before reading
    // again, and a request arriving meanwhile joins this read instead of
    // asking — the catalog read a host makes counts as that read too
    const models = [...out.values()].map((x) => x.m)
    const who = a.uid || a.name || String(site.id)
    const at = readAt.get(who)
    // a readList run records itself; a read nobody recorded (a caller reaching
    // liveModels directly) is recorded here, unless a read is in flight
    if (at === undefined || !at.p) readAt.set(who, { at: Date.now(), p: null, done: models, expired: null })
    return models
  }

  // modelOf is a model of the account's list as OpenCode's provider
  // carries one. A model of the site's fallback list has capabilities
  // already (magpie's host spreads the config's into them); one the list
  // doesn't have gets them from MODEL's flat fields, so that magpie reads
  // it as a model that reasons rather than one that doesn't (its host
  // reads capabilities.reasoning alone, host.js). The fallback list stays
  // the authority where it has an entry.
  const modelOf = (provider, m) => {
    const id = String(m.config_name)
    const was = provider.models?.[id] ?? {}
    // context_window_tokens: dev is what a request gets, max only in Max mode
    const ctx = Number(m.context_window_tokens?.dev ?? m.context_window_size?.max?.[0] ?? m.context_window_size?.max ?? m.context_window_tokens?.max ?? m.prompt_max_tokens) || was.limit?.context || MODEL.limit.context
    const out = Number(devModel(m)?.max_tokens) || was.limit?.output || 0
    const name = m.display_config?.display_name || m.display_name || m.display_model_name || was.name || id
    const input = (k) => (MODEL.modalities?.input ?? []).includes(k)
    // the list's multimodal says whether the model takes images, over the
    // fallback list's word and MODEL's
    const sees = seesOf(m)
    const inputs = was.capabilities?.input ?? { text: true, image: input("image"), audio: input("audio"), video: input("video"), pdf: input("pdf") }
    const sight = sees === null ? {} : {
      modalities: { input: [...new Set([...((was.modalities ?? MODEL.modalities).input ?? ["text"]).filter((k) => k !== "image"), ...(sees ? ["image"] : [])])], output: (was.modalities ?? MODEL.modalities).output ?? ["text"] },
      attachment: sees,
    }
    return {
      ...MODEL, ...was, ...sight, id, providerID: site.id, name: String(name), limit: { context: ctx, output: out },
      api: was.api ?? { id, url: site.hosts.api, npm: "@ai-sdk/openai-compatible" },
      capabilities: {
        ...(was.capabilities ?? {}),
        reasoning: was.capabilities?.reasoning ?? MODEL.reasoning,
        temperature: was.capabilities?.temperature ?? MODEL.temperature,
        attachment: sees ?? was.capabilities?.attachment ?? MODEL.attachment,
        toolcall: was.capabilities?.toolcall ?? MODEL.tool_call,
        input: sees === null ? inputs : { ...inputs, image: sees },
        output: was.capabilities?.output ?? { text: true, image: false, audio: false, video: false, pdf: false },
      },
      // the levels this entry takes, as OpenCode's variants: Trae's own
      // reasoning_effort_config, which a model without one doesn't carry (it
      // is then served at Trae's default, with no level to pick). A config
      // model with an empty variants says nothing — magpie's host fills one in
      // for every model it hands over — so only a non-empty one is kept, or a
      // model in the fallback table would lose the levels its entry names.
      variants: Object.keys(was.variants ?? {}).length ? was.variants : variantsOf(levelsOf(m)),
    }
  }

  return {
    config: async (config) => {
      config.provider ??= {}
      const was = config.provider[site.id] ?? {}
      config.provider[site.id] = {
        name: site.name,
        npm: "@ai-sdk/openai-compatible",
        api: site.hosts.api + "/v1",
        ...was,
        models: { ...site.models, ...(was.models ?? {}) },
      }
    },
    // the conversation's id, which the requests below carry as their
    // session_id: magpie's (or OpenCode's) session for this request, when it
    // names one
    "chat.headers": async (input, output) => {
      const id = input?.model?.providerID ?? input?.provider?.info?.id
      if (id !== site.id || !input?.sessionID) return
      output.headers[SESSION] = String(input.sessionID).slice(0, 128)
    },
    provider: {
      id: site.id,
      // the account's own list, when Trae answers; else the list above.
      // A refresh rides readList's one read per REREAD_MS, so a request that
      // wants levels while the host is refreshing joins that read instead of
      // asking for the list a second time (and vice versa).
      async models(provider, { auth }) {
        if (auth?.type !== "oauth" || !auth.access) return provider.models
        try {
          const { models: ms, expired } = await readList(await fresh(async () => auth), { strict: true })
          // a lapse must reach the host here, so the account is marked: a
          // request path deliberately lets it pass instead. A fresh instance:
          // the recorded one already served as a rejection inside the read, and
          // re-throwing that same object carries its unhandled state with it
          if (expired) throw new Expired(expired.message)
          if (!ms.length) return provider.models
          const ids = new Set(ms.map((m) => String(m.config_name)))
          return Object.fromEntries(ms.flatMap((m) => {
            const id = String(m.config_name)
            const own = modelOf(provider, m)
            const max = maxes.get(id)
            if (!max || ids.has(id + MAX)) return [[id, own]]
            // its Max, a model of its own: the window and output Max mode gives
            const was = provider.models?.[id + MAX] ?? {}
            return [[id, own], [id + MAX, { ...own, ...was, id: id + MAX, name: own.name + " (Max)", limit: { context: max.window, output: max.most || own.limit.output }, api: { ...own.api, id: id + MAX } }]]
          }))
        } catch (e) {
          if (e instanceof Expired) throw e
          return provider.models
        }
      },
    },
    auth: {
      provider: site.id,
      usage,
      checkin,
      // magpie renews the token LEAD_MS before its end, once, before its
      // requests, models and usage ask; the check before each request
      // stays for OpenCode, which doesn't call this
      refreshLead: LEAD_MS,
      refresh,
      loader: async (getAuth) => {
        const a0 = fromAuth(await getAuth(), site)
        if (!a0) return {}
        return {
          baseURL: apiOf(a0, site) + "/v1",
          apiKey: "trae", // the engine's placeholder; the request carries the JWT
          async fetch(input, init = {}) {
            const r0 = input instanceof Request ? input : null
            let body = init.body ?? (r0 ? await r0.text() : undefined)
            if (body instanceof ArrayBuffer) body = new TextDecoder().decode(body)
            else if (ArrayBuffer.isView(body)) body = new TextDecoder().decode(new Uint8Array(body.buffer, body.byteOffset, body.byteLength))
            const url = r0 ? r0.url : input instanceof URL ? input.href : String(input)
            if (ownPage(url, site)) return ownRequest(getAuth, a0, url, init.method ?? r0?.method ?? "POST", body, init.signal ?? r0?.signal)
            const req = parseJSON(body)
            if (!Array.isArray(req.messages)) return errorResponse(400, `${site.realm}: only chat completions are served`)
            let a
            try {
              a = await fresh(getAuth)
            } catch (e) {
              if (e instanceof Expired) return errorResponse(401, `${a0.name || a0.uid}: ${e.message}`, "expired")
              throw e
            }
            const signIn = a.renewed ? "renewed" : "kept"
            const who = a.uid || a.name
            // the function that lists the model first, then the one that
            // served this account last, then the rest
            const fns = [...new Set([listedBy.get(String(req.model)), fnOf.get(who), ...site.functions].filter(Boolean))]
            let last
            let named = modelNames.get(String(req.model))
            if (!named && String(req.model).endsWith(MAX)) {
              // a Max asked before the list was read here: read it (or ride
              // the read already in flight), once per REREAD_MS — a list that
              // doesn't name this Max is not read for again on every request
              // (the chat can answer without it), and a Max the read does name
              // is asked through its base, not under this unlisted id
              await readList(a)
              named = modelNames.get(String(req.model))
              if (named) fns.unshift(named.fn)
            }
            const max = named?.max ?? null
            // a model the list hasn't said of is sent its images: the agent
            // sends them only to a model it was told takes them
            const base = max?.base ?? String(req.model)
            const sees = sight.get(base) ?? (site.models[base]?.modalities?.input?.includes("image") ?? true)
            // the levels the base model takes, from the entry its lists gave
            // it: what the request's effort is sent as, and left out when the
            // model has no entry for it. An entry naming levels this build
            // can't spell counts as no level here — levelsOf says null for it
            // so a caller can tell the two apart, and this one sends nothing
            // either way.
            //
            // The list is read once per REREAD_MS for a model it hasn't named,
            // and a read already in flight is shared rather than skipped: a
            // parallel request that skipped it would drop its level for that
            // turn. An answer that leaves the model out — a list that failed,
            // or one that simply doesn't name it — is remembered too, instead
            // of being asked for again on every request that carries an effort.
            let levels = levelsOf(liveEntryOf(base)) ?? []
            if (!liveEntry.has(base) && (req.reasoning_effort ?? req.reasoningEffort)) {
              await readList(a)
              levels = levelsOf(liveEntryOf(base)) ?? []
            }
            // the answer's own signal: the caller's abort (a cancelled
            // request) and the stream's cancel both stop Trae's fetch, so
            // no read is left hanging on a connection the agent walked
            // away from
            const ac = new AbortController()
            const outer = init.signal ?? r0?.signal ?? null
            if (outer) {
              if (outer.aborted) ac.abort()
              else outer.addEventListener("abort", () => ac.abort(), { once: true })
            }
            // the conversation this request belongs to, from the chat.headers
            // hook (magpie's session) or the chat's own first user message, as
            // the account answering it, so a failover inside one conversation
            // is not one Trae reads as another's
            const session = conversationID(req, sessionOf(new Headers(init.headers ?? r0?.headers ?? {})), who)
            for (const fn of [...new Set(fns)]) {
              const res = await fetch(apiOf(a, site) + "/api/agent/v3/llm_utils_chat", {
                method: "POST",
                headers: ideHeaders(a, site, { Accept: "text/event-stream", "X-Request-ID": randomUUID() }),
                body: JSON.stringify(chatBody(req, fn, named?.fn === fn || max ? named.name : "", named?.fn === fn || max ? named.most : 0, max, session, sees, levels)),
                signal: ac.signal,
              })
              if (!res.ok) {
                const text = await res.text()
                const e = errorOf(text)
                last = failure(site, res.status, e.code, e.message || text.trim().slice(0, 300))
                if (res.status === 400 && wrongFunction(e.code)) continue
                break
              }
              const ct = res.headers.get("content-type") ?? ""
              if (!ct.includes("event-stream") && ct.includes("json")) {
                // an answer that isn't a stream is an error in a 200
                const e = errorOf(await res.text())
                last = failure(site, 502, e.code, e.message)
                if (wrongFunction(e.code)) continue
                break
              }
              const it = parts(sse(res.body), Array.isArray(req.tools) ? req.tools : [])[Symbol.asyncIterator]()
              const { held, done } = await first(it)
              const err = held.find((p) => p.error)
              if (err) {
                last = failure(site, 502, err.code, err.error)
                if (wrongFunction(err.code)) {
                  // another function may take it: this answer's read goes
                  // with the function it came from, not left hanging
                  it.return?.()
                  res.body.cancel?.().catch(() => {})
                  continue
                }
                break
              }
              fnOf.set(who, fn)
              const out = await openai(req, done ? held : chain(held, it), () => ac.abort(), site)
              out.headers.set("X-Magpie-Sign-In", signIn)
              return out
            }
            if (!last.headers.has("X-Magpie-Sign-In")) last.headers.set("X-Magpie-Sign-In", signIn)
            return last
          },
        }
      },
      methods: [{ type: "oauth", label: `${site.name} account (browser)`, authorize: () => browserSignIn(site) }],
    },
  }
}

// for tests: the CN realm's table as before, plus both realms' factories
export const TraeCNAuthPlugin = makePlugin(SITES["trae-cn"])
export const TraeGlobalAuthPlugin = makePlugin(SITES["trae-global"])

export const _internal = { ownClient, clientStoragePaths, clientDevice, clientDecode, thisMachine, traeHost, accountOf, HOSTS: SITES["trae-cn"].hosts, MODELS: SITES["trae-cn"].models, TextTools, NativeCalls, looseJSON, glmCall, nameCall, toolNamed, unwrapped, traeMessages, chatBody, credits, dollarUsageOf, whenOf, newDevice, ugDevice, proofOf, SITES, SESSION, conversationID, LEVELS, levelsOf, variantsOf, effortOf }
