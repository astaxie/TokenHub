// OpenCode & Magpie provider plugin for SenseTime Raccoon (商汤小浣熊, xiaohuanxiong.com).
//
// Raccoon signs in through xiaohuanxiong.com's desktop authorization page
// (/code/authorize -> office-raccoon://auth/callback?code=...&state=...), or
// reads the live session Raccoon's desktop app keeps in
// ~/.box-agent/config/auth.json (read-only). Chats are OpenAI-compatible chat
// completions at /api/web/llm/v2/chat/completions.

import { randomUUID, randomBytes } from "node:crypto"
import { createServer } from "node:http"
import { readFileSync, statSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

const PROVIDER_ID = "raccoon"
const PROVIDER_NAME = "商汤小浣熊"
const NPM = "@ai-sdk/openai-compatible"

const mainOrigin = () => (process.env.RACCOON_MAIN_SITE_URL || "https://xiaohuanxiong.com").replace(/\/+$/, "")
const authBase = () => (process.env.RACCOON_AUTH_API_BASE || `${mainOrigin()}/api/web/auth/v1`).replace(/\/+$/, "")
const llmBase = () => (process.env.RACCOON_LLM_BASE_URL || `${mainOrigin()}/api/web/llm/v2`).replace(/\/+$/, "")

const EARLY_MS = 60 * 1000
const LEAD_MS = 5 * 60 * 1000
const KEEP_RENEWED_MS = 60 * 1000
const CATALOG_TTL_MS = 10 * 60 * 1000

const desktopPlatform = (plat = process.platform) => (plat === "darwin" ? "desktop-mac" : "desktop-windows")
const desktopHeaders = () => ({
  "X-Client-Platform": desktopPlatform(),
  "X-Client-Version": "v1.0.0",
})

const FALLBACK_MODELS = [
  { id: "raccoon-8c4485", name: "Raccoon-Work", context: 1_000_000, output: 100_000, images: true, reasoning: true, rate: 1, free: false },
  { id: "raccoon-19b265", name: "Raccoon-Work-260817-A", context: 1_000_000, output: 100_000, images: false, reasoning: true, rate: 1, free: false },
  { id: "raccoon-405a1c", name: "Raccoon-Work-260817-B", context: 1_000_000, output: 100_000, images: false, reasoning: false, rate: 1, free: false },
  { id: "sn-sensenova-6-8-flash", name: "SenseNova-6.8-Flash", context: 256_000, output: 63_999, images: true, reasoning: true, rate: 0, rateWas: 0.5, free: true },
  { id: "sn-sensenova-6-8-flash-lite", name: "SenseNova-6.8-Flash-Lite", context: 256_000, output: 63_999, images: true, reasoning: true, rate: 0, rateWas: 0.5, free: true },
  { id: "sn-glm-5-3", name: "GLM-5-3", context: 1_000_000, output: 100_000, images: true, reasoning: true, rate: 0.75, free: false },
  { id: "sn-kimi-k3", name: "Kimi-K3", context: 1_000_000, output: 100_000, images: true, reasoning: true, rate: 1, free: false },
  { id: "sn-glm-5-3-flash", name: "GLM-5-3-Flash", context: 1_000_000, output: 100_000, images: false, reasoning: true, rate: 0.2, free: false },
  { id: "sn-deepseek-v4-1-flash", name: "DeepSeek-V4.1-Flash", context: 1_000_000, output: 100_000, images: false, reasoning: true, rate: 0.25, free: false },
  { id: "raccoon-chat-ml-5-5", name: "Raccoon Chat (默认)", context: 180_000, output: 80_000, images: false, reasoning: true, rate: 1, free: false },
]

// ---- JWT helpers ------------------------------------------------------------

function stripBearer(value) {
  return String(value ?? "").trim().replace(/^Bearer\s+/i, "").trim()
}

function decodeJwtClaims(token) {
  const raw = stripBearer(token)
  const parts = raw.split(".")
  if (parts.length < 2 || !parts[1]) return null
  try {
    const json = Buffer.from(parts[1], "base64url").toString("utf8")
    const obj = JSON.parse(json)
    return obj && typeof obj === "object" && !Array.isArray(obj) ? obj : null
  } catch {
    return null
  }
}

function jwtExpiryMs(token) {
  const claims = decodeJwtClaims(token)
  const exp = Number(claims?.exp)
  return Number.isFinite(exp) && exp > 0 ? exp * 1000 : 0
}

function hexIdToDecimal(val) {
  const raw = String(val ?? "").trim().replace(/^0x/i, "")
  if (!raw || !/^[0-9a-fA-F]+$/.test(raw)) return ""
  try {
    return BigInt("0x" + raw).toString(10)
  } catch {
    return ""
  }
}

function extractUserId(claims) {
  if (!claims) return ""
  for (const k of ["sid", "iss"]) {
    const dec = hexIdToDecimal(claims[k])
    if (dec) return dec
  }
  for (const k of ["sub", "id", "user_id"]) {
    const s = String(claims[k] ?? "").trim()
    if (s) return s
  }
  return ""
}

function extractDisplayName(claims) {
  return String(claims?.name ?? "").trim()
}

function vendorErrorText(payload, fallback) {
  for (const k of ["details", "message", "msg"]) {
    if (typeof payload?.[k] === "string" && payload[k].trim()) return payload[k].trim()
  }
  if (typeof payload?.error?.message === "string" && payload.error.message.trim()) {
    return payload.error.message.trim()
  }
  return fallback
}

// ---- Desktop auth.json (~/.box-agent/config/auth.json, read-only) -----------

function desktopAuthPath() {
  for (const envKey of ["RACCOON_BOX_AGENT_CONFIG_DIR", "BOX_AGENT_CONFIG_DIR"]) {
    const v = String(process.env[envKey] ?? "").trim()
    if (v) return join(v, "auth.json")
  }
  return join(homedir(), ".box-agent", "config", "auth.json")
}

function readDesktopAuth() {
  const p = desktopAuthPath()
  try {
    const st = statSync(p)
    if (!st.isFile() || st.size <= 0 || st.size > 256 * 1024) return null
    const raw = JSON.parse(readFileSync(p, "utf8"))
    const access = stripBearer(raw?.access_token)
    if (!access) return null
    const claims = decodeJwtClaims(access)
    const uid = extractUserId(claims)
    const name = extractDisplayName(claims)
    const expires = jwtExpiryMs(access)
    return {
      access,
      refresh: "",
      expires,
      uid,
      name: name || (uid ? `Raccoon-${uid}` : PROVIDER_NAME),
      source: "desktop",
    }
  } catch {
    return null
  }
}

// ---- Token refresh (single-flight) ------------------------------------------

const renewing = new Map()
const renewed = new Map()

async function callRefreshApi(refreshToken) {
  const res = await fetch(`${authBase()}/refresh`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ refresh_token: refreshToken }),
    signal: AbortSignal.timeout(30_000),
  })
  const text = await res.text()
  let payload = {}
  try {
    payload = JSON.parse(text) ?? {}
  } catch {}
  if (!res.ok) {
    const msg = vendorErrorText(payload, `HTTP ${res.status}`)
    const err = new Error(`商汤小浣熊刷新 Token 失败: ${msg}`)
    if (res.status >= 400 && res.status < 500) err.signIn = "expired"
    throw err
  }
  const data = payload?.data ?? {}
  const access = stripBearer(data?.access_token)
  if (!access) {
    const msg = vendorErrorText(payload, "未返回新 Token")
    const err = new Error(`商汤小浣熊刷新 Token 失败: ${msg}`)
    err.signIn = "expired"
    throw err
  }
  const refresh = stripBearer(data?.refresh_token) || refreshToken
  const expires = jwtExpiryMs(access)
  const claims = decodeJwtClaims(access)
  const uid = extractUserId(claims)
  const name = extractDisplayName(claims)
  return { access, refresh, expires, ...(uid ? { uid } : {}), ...(name ? { name } : {}) }
}

async function renewToken(a, save) {
  const key = a.refresh
  const done = renewed.get(key)
  if (done && Date.now() - done.at < KEEP_RENEWED_MS) {
    const merged = { ...a, ...done.next }
    await save?.(merged)
    return merged
  }
  let r = renewing.get(key)
  if (!r) {
    r = (async () => {
      const next = await callRefreshApi(a.refresh)
      renewed.set(key, { next, at: Date.now() })
      return next
    })().finally(() => renewing.delete(key))
    renewing.set(key, r)
  }
  const next = await r
  const merged = { ...a, ...next }
  await save?.(merged)
  return merged
}

function isStale(a, marginMs = EARLY_MS) {
  const exp = Number(a?.expires) || jwtExpiryMs(a?.access)
  return exp > 0 && Date.now() >= exp - marginMs
}

// Desktop sign-in is strictly read-only: never write ~/.box-agent/config/auth.json
// and never spend the desktop app's refresh token. The app holds one account
// at a time, so a seat keeps the token it was signed in with, saved on its own
// record through the client (as kiro does), and reads the app only to take a
// newer token for that same account. The app's current account never stands in
// for a seat it does not name: a seat answered with another account's token
// was billed to the wrong account and reported that account's credits, with
// nothing anywhere saying so (plugins #166).
//
// sameAccount says whether a seat names the account the app holds now. A uid
// the read token does not carry never agrees: the seat's own token is used, or
// the seat asks to be signed in again, rather than being answered with
// credentials it cannot be shown to own. A seat that names no account at all
// has nothing to protect and means the app's own account.
function sameAccount(seat, read) {
  if (!read) return false
  if (seat?.uid) return !!read.uid && String(seat.uid) === String(read.uid)
  if (seat?.accountId) return !!read.name && String(seat.accountId) === String(read.name)
  return true
}

async function currentAuth(client, auth, force = false) {
  if (auth?.source === "desktop") {
    const d = readDesktopAuth()
    // the app holds this seat's account: its token is the newest there is,
    // and a seat asked again after a refusal takes it
    if (sameAccount(auth, d) && !isStale(d, EARLY_MS)) {
      if (d.access && d.access !== auth.access) {
        try {
          await client?.auth?.set?.({ path: { id: PROVIDER_ID }, body: { ...auth, ...d } })
        } catch {}
      }
      return { a: { ...auth, ...d }, didRenew: false }
    }
    // the app holds another account, or none: this seat goes on with the
    // token it was signed in with until that token ends
    if (!force && auth.access && !isStale(auth, EARLY_MS)) return { a: { ...auth }, didRenew: false }
    if (sameAccount(auth, d)) {
      const err = new Error("商汤小浣熊桌面端登录态已过期，请打开商汤小浣熊客户端重新登录")
      err.signIn = "expired"
      throw err
    }
    const err = new Error(
      d ? "商汤小浣熊桌面端已切换为其他账号，请重新登录" : "商汤小浣熊桌面端未登录，请打开商汤小浣熊客户端登录",
    )
    err.signIn = "expired"
    throw err
  }

  let a = { ...auth }
  for (let i = 0; i < 8 && a.refresh; i++) {
    const d = renewed.get(a.refresh)
    if (!d || Date.now() - d.at >= KEEP_RENEWED_MS) break
    a = { ...a, ...d.next }
  }
  if (!a.expires && a.access) a.expires = jwtExpiryMs(a.access)
  const alreadyRenewed = a.access !== auth.access || a.refresh !== auth.refresh
  if ((alreadyRenewed || !force) && !isStale(a, EARLY_MS)) {
    if (alreadyRenewed) {
      try {
        await client?.auth?.set?.({ path: { id: PROVIDER_ID }, body: a })
      } catch {}
    }
    return { a, didRenew: alreadyRenewed }
  }
  if (!a.refresh) {
    if (!force && a.access && (!a.expires || Date.now() < a.expires)) return { a, didRenew: alreadyRenewed }
    const err = new Error("商汤小浣熊登录态已过期且无 refresh_token，请重新登录")
    err.signIn = "expired"
    throw err
  }
  const next = await renewToken(a, async (updated) => {
    try {
      await client?.auth?.set?.({ path: { id: PROVIDER_ID }, body: updated })
    } catch {}
  })
  return { a: next, didRenew: true }
}

// ---- Models catalog ---------------------------------------------------------

let catalogCache = { origin: "", models: null, at: 0 }

function numOf(v) {
  const n = typeof v === "number" ? v : typeof v === "string" && v.trim() ? Number(v.trim()) : NaN
  return Number.isFinite(n) && n >= 0 ? n : undefined
}

function parseCatalog(payload) {
  const data = payload?.data && typeof payload.data === "object" ? payload.data : payload
  const categories = Array.isArray(data?.categories) ? data.categories : []
  const seen = new Set()
  const out = []
  for (const cat of categories) {
    for (const entry of Array.isArray(cat?.models) ? cat.models : []) {
      const id = String(entry?.name || entry?.model_name || "").trim()
      if (!id || seen.has(id)) continue
      seen.add(id)
      const codename = String(entry?.description ?? "").trim()
      const displayName = String(entry?.display_name ?? "").trim() || (codename && codename.length <= 64 ? codename : id)
      const tags = (Array.isArray(entry?.tags) ? entry.tags : []).map((t) => String(t ?? "").trim().toLowerCase()).filter(Boolean)
      const images = tags.some((t) => t === "vision" || t === "image" || t === "image-understanding")
      const context = Math.floor(numOf(entry?.context_window ?? entry?.contextWindow ?? entry?.params?.context_window ?? entry?.params?.contextWindow) || 200_000)
      const output = Math.floor(numOf(entry?.max_tokens ?? entry?.maxTokens ?? entry?.params?.max_tokens ?? entry?.params?.maxTokens) || 64_000)
      const baseRate = numOf(entry?.billing_multiplier)
      const effRate = numOf(entry?.billing_effective_multiplier)
      const rate = effRate ?? baseRate
      const free = rate === 0
      const rateWas = baseRate !== undefined && effRate !== undefined && effRate < baseRate ? baseRate : undefined
      out.push({
        id,
        name: displayName,
        context,
        output,
        images,
        reasoning: true,
        free,
        ...(rate !== undefined ? { rate } : {}),
        ...(rateWas !== undefined ? { rateWas } : {}),
      })
    }
  }
  if (!seen.has("raccoon-chat-ml-5-5")) {
    out.push({ id: "raccoon-chat-ml-5-5", name: "Raccoon Chat (默认)", context: 180_000, output: 80_000, images: false, reasoning: true, rate: 1, free: false })
  }
  return out
}

async function fetchLiveModels(token) {
  const base = llmBase()
  if (catalogCache.models && catalogCache.origin === base && Date.now() - catalogCache.at < CATALOG_TTL_MS) {
    return catalogCache.models
  }
  const headers = { Accept: "application/json" }
  if (token) headers.Authorization = "Bearer " + stripBearer(token)
  const res = await fetch(`${base}/model_catalog`, {
    headers,
    signal: AbortSignal.timeout(10_000),
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const payload = await res.json()
  const models = parseCatalog(payload)
  if (!models.length) throw new Error("empty catalog")
  catalogCache = { origin: base, models, at: Date.now() }
  return models
}

function configModels() {
  return Object.fromEntries(
    FALLBACK_MODELS.map((m) => [
      m.id,
      {
        name: m.name,
        limit: { context: m.context, output: m.output },
        tool_call: true,
        reasoning: !!m.reasoning,
        attachment: !!m.images,
        modalities: { input: m.images ? ["text", "image"] : ["text"], output: ["text"] },
        free: !!m.free,
        ...(m.rate !== undefined ? { rate: m.rate } : {}),
        ...(m.rateWas !== undefined ? { rateWas: m.rateWas } : {}),
      },
    ]),
  )
}

function runtimeModel(provider, m) {
  return {
    id: m.id,
    providerID: provider.id,
    name: m.name,
    api: { id: m.id, url: llmBase(), npm: NPM },
    status: "active",
    headers: {},
    options: {},
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: { context: m.context ?? 0, output: m.output ?? 0 },
    capabilities: {
      temperature: true,
      reasoning: !!m.reasoning,
      attachment: !!m.images,
      toolcall: true,
      input: { text: true, image: !!m.images, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: {},
    free: !!m.free,
    ...(m.rate !== undefined ? { rate: m.rate } : {}),
    ...(m.rateWas !== undefined ? { rateWas: m.rateWas } : {}),
  }
}

// ---- SSE model rewrite ------------------------------------------------------

function rewriteSseModel(res, requestedModel, signInHeader) {
  const headers = new Headers(res.headers)
  headers.set("X-Magpie-Sign-In", signInHeader)
  if (!requestedModel || !res.body) {
    return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
  }
  const ct = (headers.get("content-type") || "").toLowerCase()
  if (ct.includes("application/json")) {
    return (async () => {
      const text = await res.text()
      try {
        const obj = JSON.parse(text)
        if (obj && typeof obj === "object" && "model" in obj) {
          obj.model = requestedModel
          headers.delete("content-length")
          return new Response(JSON.stringify(obj), { status: res.status, statusText: res.statusText, headers })
        }
      } catch {}
      return new Response(text, { status: res.status, statusText: res.statusText, headers })
    })()
  }
  if (!ct.includes("text/event-stream")) {
    return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
  }
  const dec = new TextDecoder()
  const enc = new TextEncoder()
  let buf = ""
  const rewriteLine = (line) => {
    if (!line.startsWith("data:")) return line
    const dataStr = line.slice(5).trim()
    if (!dataStr || dataStr === "[DONE]") return line
    try {
      const obj = JSON.parse(dataStr)
      if (obj && typeof obj === "object" && "model" in obj && obj.model !== requestedModel) {
        obj.model = requestedModel
        return "data: " + JSON.stringify(obj)
      }
    } catch {}
    return line
  }
  const stream = res.body.pipeThrough(
    new TransformStream({
      transform(chunk, controller) {
        buf += dec.decode(chunk, { stream: true })
        const lines = buf.split(/\r?\n/)
        buf = lines.pop() ?? ""
        if (lines.length) {
          controller.enqueue(enc.encode(lines.map(rewriteLine).join("\n") + "\n"))
        }
      },
      flush(controller) {
        buf += dec.decode()
        if (buf) controller.enqueue(enc.encode(rewriteLine(buf.replace(/\r$/, ""))))
      },
    }),
  )
  headers.delete("content-length")
  return new Response(stream, { status: res.status, statusText: res.statusText, headers })
}

// ---- Usage, Check-in & Onboarding -------------------------------------------

async function apiGetJson(path, token, extraHeaders = {}, method = "GET") {
  const res = await fetch(`${mainOrigin()}${path}`, {
    method,
    headers: {
      Accept: "application/json",
      Authorization: "Bearer " + stripBearer(token),
      ...extraHeaders,
    },
    signal: AbortSignal.timeout(20_000),
  })
  const text = await res.text()
  let payload = {}
  try {
    payload = JSON.parse(text) ?? {}
  } catch {}
  if (res.status === 401) {
    const err = new Error(vendorErrorText(payload, "登录态已过期"))
    err.status = 401
    err.signIn = "expired"
    throw err
  }
  if (!res.ok) throw new Error(vendorErrorText(payload, `HTTP ${res.status}`))
  if (typeof payload?.code === "number" && payload.code !== 0) {
    const err = new Error(vendorErrorText(payload, `code=${payload.code}`))
    if (payload.code === 200001 || payload.code === 200003) {
      err.status = 401
      err.signIn = "expired"
    }
    throw err
  }
  return payload?.data ?? payload
}

function beijingTodayKey(nowMs = Date.now()) {
  return new Date(nowMs + 8 * 3600_000).toISOString().slice(0, 10)
}

async function claimDesktopOnboardingGrant(token) {
  try {
    const d = await apiGetJson("/api/web/desktop/v1/login/points/grant", token, desktopHeaders(), "POST")
    if (d?.granted === true) {
      const pts = Number(d?.popup?.points)
      return { points: Number.isFinite(pts) && pts > 0 ? Math.floor(pts) : 3000, settled: true }
    }
    if (d?.granted === false) {
      return { points: 0, settled: true }
    }
  } catch {}
  return { points: 0, settled: false }
}

// allBills is the account's whole bill history, newest first. The bills
// endpoint ignores paging.offset and always says total 0, so it can only be
// asked for the newest N rows; 200 covered under a day of an active account
// and left out the large grants a balance is made of (plugins #223).
async function allBills(token) {
  let bills = null
  for (const limit of [1000, 5000]) {
    bills = await apiGetJson(`/api/web/points/v1/bills?paging.limit=${limit}&paging.offset=0`, token)
    if (!Array.isArray(bills?.items) || bills.items.length < limit) break
  }
  return bills
}

async function queryUsage(token) {
  const [points, entitlement, bills] = await Promise.all([
    apiGetJson("/api/web/points/v1/balance", token),
    apiGetJson("/api/web/auth/v1/entitlement_info", token).catch(() => null),
    allBills(token).catch(() => null),
  ])
  const avail = Math.floor(Number(points?.available_points) || 0)

  const office = entitlement?.office ?? entitlement?.code ?? {}
  const proEnable = office?.pro_enable === true || entitlement?.code?.pro_enable === true
  const planType = office?.active_plan?.type || office?.plan_type || ""
  const plan = proEnable ? (planType && planType !== "none" ? planType : "Pro") : "Free"
  const expireAt = office?.pro_expired_time || office?.active_plan?.subscription_expired_at || null

  // Sum only what the vendor actually reports as granted in unexpired bills.
  // If bills don't account for the current balance, report the balance with no window.
  let grantedFromBills = 0
  let soonestExpMs = 0
  let expiringPts = 0
  const now = Date.now()
  for (const item of Array.isArray(bills?.items) ? bills.items : []) {
    const pts = Math.floor(Number(item?.points) || 0)
    if (pts <= 0) continue
    const expStr = typeof item?.expire_at === "string" ? item.expire_at : ""
    const expMs = expStr ? Date.parse(expStr) : 0
    if (expMs > 0 && expMs <= now) continue
    grantedFromBills += pts
    if (expMs > now) {
      if (!soonestExpMs || expMs < soonestExpMs) {
        soonestExpMs = expMs
        expiringPts = pts
      } else if (expMs === soonestExpMs) {
        expiringPts += pts
      }
    }
  }
  const runsOutAt = soonestExpMs ? new Date(soonestExpMs).toISOString() : null
  const runsOut = soonestExpMs ? Math.min(avail, expiringPts) : 0

  if (grantedFromBills > 0 && grantedFromBills >= avail) {
    const used = grantedFromBills - avail
    return {
      plan,
      ...(proEnable && expireAt ? { until: new Date(expireAt).toISOString() } : {}),
      windows: [
        {
          name: "Credits",
          used: (100 * used) / grantedFromBills,
          display: `${used} / ${grantedFromBills}`,
          amount: used,
          limit: grantedFromBills,
          unit: "credits",
          ...(runsOutAt ? { runsOutAt, runsOut } : {}),
        },
      ],
    }
  }

  return {
    plan,
    ...(proEnable && expireAt ? { until: new Date(expireAt).toISOString() } : {}),
    balance: `${avail} credits`,
    windows: [],
  }
}

function billKey(item) {
  return String(item?.bill_id || item?.biz_id || `${item?.created_at ?? ""}:${item?.points ?? 0}:${item?.event_name ?? ""}`)
}

function isCheckinGrantBill(item, today) {
  const pts = Math.floor(Number(item?.points) || 0)
  if (pts <= 0) return false
  const createdAt = String(item?.created_at ?? "")
  if (createdAt.slice(0, 10) !== today) return false
  const biz = String(item?.biz_type ?? "")
  return !biz || biz === "daily_grant" || biz === "reward_grant"
}

const desktopOnboardingSettled = new Set()

async function performCheckIn(a, save) {
  const token = a.access
  const today = beijingTodayKey()

  // Snapshot today's grant bills before triggering setting_info so a second
  // check-in on the same day reports "done" rather than "claimed" again.
  let beforeKeys = null
  try {
    const before = await apiGetJson("/api/web/points/v1/bills?paging.limit=20&paging.offset=0", token)
    const items = Array.isArray(before?.items) ? before.items : []
    beforeKeys = new Set(items.filter((x) => isCheckinGrantBill(x, today)).map(billKey))
  } catch {}

  await apiGetJson("/api/web/office/v3/setting_info", token, {
    ...desktopHeaders(),
    "X-Raccoon-Language": "zh",
  })

  let onboardingPts = 0
  const accountKey = a?.uid || a?.accountId || a?.access
  const alreadySettled = a?.onboardingSettled || (a?.source === "desktop" && desktopOnboardingSettled.has(accountKey))
  if (!alreadySettled) {
    const grant = await claimDesktopOnboardingGrant(token)
    onboardingPts = grant.points
    if (grant.settled) {
      if (a?.source === "desktop") {
        desktopOnboardingSettled.add(accountKey)
      } else if (save) {
        await save({ ...a, onboardingSettled: true }).catch(() => {})
      }
    }
  }

  // If the before-snapshot of bills could not be read, avoid false positives
  // from historical bills or repeating popup grants, while still reporting an
  // onboarding grant that this call just claimed.
  if (!beforeKeys) {
    if (onboardingPts > 0) {
      return {
        outcome: "claimed",
        credit: onboardingPts,
        message: `桌面端首次登录奖励 +${onboardingPts}`,
      }
    }
    return {
      outcome: "done",
      message: "今日已签到",
    }
  }

  const bills = await apiGetJson("/api/web/points/v1/bills?paging.limit=20&paging.offset=0", token)
  let newlyGranted = 0
  let hasNewRewardBill = false
  const names = []
  const items = Array.isArray(bills?.items) ? bills.items : []
  items.forEach((item) => {
    if (!isCheckinGrantBill(item, today)) return
    if (beforeKeys.has(billKey(item))) return
    const pts = Math.floor(Number(item?.points) || 0)
    newlyGranted += pts
    if (String(item?.biz_type ?? "") === "reward_grant") hasNewRewardBill = true
    const ev = String(item?.event_name || "积分发放")
    if (!names.includes(ev)) names.push(ev)
  })

  // Sum daily check-in bills and the one-time onboarding grant when both land
  // in one call, unless the onboarding grant is already present as a new
  // reward_grant bill in /points/v1/bills (which would double-count it).
  if (onboardingPts > 0 && !hasNewRewardBill) {
    newlyGranted += onboardingPts
    if (!names.includes("桌面端首次登录奖励")) names.push("桌面端首次登录奖励")
  }

  const totalCredit = newlyGranted
  if (totalCredit > 0) {
    return {
      outcome: "claimed",
      credit: totalCredit,
      message: `${names.join(" + ") || "今日积分"} +${totalCredit}`,
    }
  }
  return {
    outcome: "done",
    message: "今日已签到",
  }
}

// ---- Sign-in flows ----------------------------------------------------------

function buildAuthorizeUrl(state) {
  const u = new URL("/code/authorize", mainOrigin())
  u.searchParams.set("login_source", "desktop")
  u.searchParams.set("appname", "办公小浣熊客户端")
  u.searchParams.set("state", state)
  return u.toString()
}

function parseCallbackInput(raw, expectedState) {
  const text = String(raw ?? "").trim()
  if (!text) throw new Error("请输入回调链接或 Token")
  if (text.startsWith("{")) {
    const obj = JSON.parse(text)
    const access = stripBearer(obj?.access_token ?? obj?.accessToken)
    const refresh = stripBearer(obj?.refresh_token ?? obj?.refreshToken)
    if (!access) throw new Error("JSON 中缺少 access_token")
    return { direct: { access, refresh } }
  }
  if (text.split(".").length === 3 && !text.includes("://")) {
    return { direct: { access: stripBearer(text), refresh: "" } }
  }
  let u
  try {
    u = new URL(text)
  } catch {
    throw new Error("无效的回调链接或 Token 格式")
  }
  const code = (u.searchParams.get("code") || "").trim()
  const state = (u.searchParams.get("state") || "").trim()
  if (!code) throw new Error("回调链接中缺少授权码 code")
  if (expectedState && state && state !== expectedState) {
    throw new Error("回调链接 state 不匹配，请使用本次打开页面产生的回调链接")
  }
  return { code }
}

async function exchangeAuthCode(code) {
  const res = await fetch(`${authBase()}/login_with_authorization_code`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ authorization_code: code }),
    signal: AbortSignal.timeout(30_000),
  })
  const payload = await res.json().catch(() => ({}))
  if (payload?.code === 200035) {
    throw new Error("授权码已失效，请重新发起网页登录")
  }
  const data = payload?.data ?? {}
  const access = stripBearer(data?.access_token)
  const refresh = stripBearer(data?.refresh_token)
  if (!res.ok || !access || !refresh) {
    throw new Error(vendorErrorText(payload, `换取凭证失败 (HTTP ${res.status})`))
  }
  return { access, refresh }
}

function toSuccessAuth(access, refresh, extra = {}) {
  const claims = decodeJwtClaims(access)
  const uid = extractUserId(claims)
  const name = extractDisplayName(claims)
  const expires = jwtExpiryMs(access)
  return {
    type: "success",
    access,
    refresh: refresh || "",
    expires,
    accountId: name || (uid ? `Raccoon-${uid}` : PROVIDER_NAME),
    uid: uid || "",
    ...(name ? { name } : {}),
    ...extra,
  }
}

async function browserSignIn(timeoutMs = 10 * 60 * 1000) {
  // The current authorize page accepts desktop loopback callbacks only with
  // a 64-character hex state and a 127.0.0.1 /auth/callback URL.
  const state = randomBytes(32).toString("hex")
  const url = new URL(buildAuthorizeUrl(state))
  url.searchParams.set("desktop_callback", "loopback")
  let complete, timer, origin, received = false, settled = false
  const done = new Promise((resolve) => { complete = resolve })
  const finish = (result) => {
    if (settled) return
    settled = true
    clearTimeout(timer)
    complete(result)
    server.close()
    server.closeIdleConnections?.()
  }
  const server = createServer(async (req, res) => {
    const reply = (status, text) => {
      res.writeHead(status, {
        "Content-Type": "text/plain; charset=utf-8",
        "Cache-Control": "no-store",
        "Referrer-Policy": "no-referrer",
        "Content-Security-Policy": "default-src 'none'",
      })
      res.end(text)
    }
    const url = new URL(req.url || "/", "http://127.0.0.1")
    if (req.headers.host !== origin || url.pathname !== "/auth/callback") return reply(404, "Not found")
    if (req.method !== "GET") return reply(405, "Method not allowed")
    const q = url.searchParams
    if (q.getAll("state").length !== 1 || q.get("state") !== state) return reply(400, "回调不属于本次登录，请回到原授权页重试。")
    if (settled || received) return reply(409, "本次回调已经处理，请回到 Magpie 查看结果。")
    if (q.get("error")) {
      reply(400, "登录未获授权，请回到 Magpie 重新登录。")
      finish({ type: "failed", error: "商汤小浣熊授权被拒绝，请重新登录" })
      return
    }
    const code = q.get("code") || ""
    if (q.getAll("code").length !== 1 || !code || code.length > 4096) return reply(400, "回调缺少有效授权码，请回到原授权页重试。")
    received = true
    try {
      const tokens = await exchangeAuthCode(code)
      if (settled) return reply(408, "登录已超时，请回到 Magpie 重新登录。")
      reply(200, "商汤小浣熊登录成功，可以关闭此页并返回 Magpie。")
      finish(toSuccessAuth(tokens.access, tokens.refresh))
    } catch (e) {
      reply(502, "登录未完成，请返回 Magpie 查看错误并重新登录。")
      finish({ type: "failed", error: e?.message ?? String(e) })
    }
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  origin = `127.0.0.1:${server.address().port}`
  url.searchParams.set("redirect", `http://${origin}/auth/callback`)
  timer = setTimeout(() => finish({ type: "failed", error: "商汤小浣熊登录超时，请重新登录" }), timeoutMs)
  timer.unref?.()
  return {
    url: url.toString(),
    instructions: "在浏览器完成商汤小浣熊登录并确认授权后，会自动返回此处；无需填写短信验证码或复制网页地址。",
    method: "auto",
    async callback() { return await done },
  }
}

function manualSignIn() {
  const state = randomUUID()
  const url = buildAuthorizeUrl(state)
  return {
    url,
    instructions:
      "在浏览器完成商汤小浣熊登录后，复制地址栏或弹窗跳转的 office-raccoon://auth/callback?code=... 链接粘贴到下方（也支持直接粘贴 JWT access_token）：",
    method: "code",
    async callback(input) {
      try {
        const parsed = parseCallbackInput(input, state)
        const tokens = parsed.direct ? parsed.direct : await exchangeAuthCode(parsed.code)
        return toSuccessAuth(tokens.access, tokens.refresh)
      } catch (e) {
        return { type: "failed", error: e?.message ?? String(e) }
      }
    },
  }
}

function desktopSignIn() {
  return {
    url: "",
    instructions: "直接复用本机商汤小浣熊客户端的实时登录态 (~/.box-agent/config/auth.json)。",
    method: "auto",
    async callback() {
      // A new seat takes the account the app holds now, and its token, and
      // keeps them on its own record: the app is signed in to one account at
      // a time and drops the previous one's token as soon as it rotates off
      // disk. Only the app's own account is offered here, so a seat is never
      // quietly handed some other account's credentials.
      const d = readDesktopAuth()
      if (!d) return { type: "failed", error: "未检测到本机商汤小浣熊登录态 (~/.box-agent/config/auth.json)" }
      if (isStale(d, EARLY_MS)) {
        return { type: "failed", error: "商汤小浣熊桌面端登录态已过期，请先打开商汤小浣熊客户端重新登录" }
      }
      return {
        type: "success",
        access: d.access,
        refresh: "",
        expires: d.expires,
        source: "desktop",
        accountId: d.name || PROVIDER_NAME,
        uid: d.uid || "",
      }
    },
  }
}

// ---- Plugin export ----------------------------------------------------------

export const RaccoonAuthPlugin = async ({ client }) => {
  const saveAuth = async (next) => {
    try {
      await client?.auth?.set?.({ path: { id: PROVIDER_ID }, body: next })
    } catch {}
  }

  return {
    config: async (config) => {
      config.provider ??= {}
      const was = config.provider[PROVIDER_ID] ?? {}
      config.provider[PROVIDER_ID] = {
        name: PROVIDER_NAME,
        npm: NPM,
        api: llmBase(),
        ...was,
        models: { ...configModels(), ...(was.models ?? {}) },
      }
    },
    provider: {
      id: PROVIDER_ID,
      async models(provider, { auth } = {}) {
        try {
          let token = ""
          if (auth?.type === "oauth" && (auth.access || auth.source === "desktop")) {
            const { a } = await currentAuth(client, auth)
            token = a.access
          }
          const ms = await fetchLiveModels(token)
          if (!ms.length) return provider.models
          return Object.fromEntries(ms.map((m) => [m.id, runtimeModel(provider, m)]))
        } catch {
          return provider.models
        }
      },
    },
    auth: {
      provider: PROVIDER_ID,
      refreshLead: LEAD_MS,
      async refresh(auth) {
        if (auth?.type !== "oauth" || auth.source === "desktop" || !auth.access || !auth.refresh) return undefined
        const { a, didRenew } = await currentAuth(client, auth, true)
        if (!didRenew) return undefined
        return {
          access: a.access,
          refresh: a.refresh,
          expires: a.expires,
          ...(a.uid ? { uid: a.uid } : {}),
          ...(a.name ? { name: a.name } : {}),
        }
      },
      async loader(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "oauth") return {}
        return {
          baseURL: llmBase(),
          apiKey: "raccoon",
          async fetch(input, init = {}) {
            const req = input instanceof Request ? input : null
            let bodyText = init?.body
            if (bodyText === undefined && req) bodyText = await req.clone().text()
            let requestedModel = ""
            try {
              const parsed = typeof bodyText === "string" ? JSON.parse(bodyText) : null
              if (typeof parsed?.model === "string") requestedModel = parsed.model
            } catch {}

            let a
            let didRenew = false
            try {
              ;({ a, didRenew } = await currentAuth(client, await getAuth()))
            } catch (e) {
              const expired = e?.signIn === "expired"
              return new Response(
                JSON.stringify({
                  error: {
                    message: e?.message ?? String(e),
                    type: expired ? "authentication_error" : "api_error",
                  },
                }),
                {
                  status: expired ? 401 : 502,
                  headers: {
                    "Content-Type": "application/json",
                    "X-Magpie-Sign-In": expired ? "expired" : "kept",
                  },
                },
              )
            }
            const sendOnce = (cred) => {
              const headers = new Headers(init?.headers ?? req?.headers)
              headers.delete("x-api-key")
              headers.delete("content-length")
              headers.set("Authorization", "Bearer " + cred.access)
              headers.set("Content-Type", "application/json")
              headers.set("Accept", "*/*")
              return fetch(req ? req.url : input, {
                ...init,
                method: init?.method ?? req?.method ?? "POST",
                headers,
                body: bodyText,
              })
            }

            let res = await sendOnce(a)
            if (res.status === 401 && a.refresh && a.source !== "desktop") {
              try {
                const retryAuth = await currentAuth(client, a, true)
                a = retryAuth.a
                didRenew = true
                res = await sendOnce(a)
              } catch {}
            }
            const signInState = res.status === 401 ? "expired" : didRenew && res.ok ? "renewed" : "kept"
            return rewriteSseModel(res, requestedModel, signInState)
          },
        }
      },
      async usage(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "oauth" || !(auth.access || auth.source === "desktop")) {
          return { error: "not signed in", signIn: "kept" }
        }
        try {
          let { a, didRenew } = await currentAuth(client, auth)
          let result
          try {
            result = await queryUsage(a.access)
          } catch (e) {
            if (e?.status === 401 && a.refresh && a.source !== "desktop") {
              const retryAuth = await currentAuth(client, a, true)
              a = retryAuth.a
              didRenew = true
              result = await queryUsage(a.access)
            } else {
              throw e
            }
          }
          return {
            ...result,
            user: a.name || auth.accountId || a.uid,
            signIn: didRenew ? "renewed" : "kept",
          }
        } catch (e) {
          return {
            error: e?.message ?? String(e),
            signIn: e?.signIn === "expired" ? "expired" : "kept",
          }
        }
      },
      async checkin(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "oauth" || !(auth.access || auth.source === "desktop")) {
          return { outcome: "failed", message: "not signed in" }
        }
        try {
          let { a } = await currentAuth(client, auth)
          try {
            return await performCheckIn(a, saveAuth)
          } catch (e) {
            if (e?.status === 401 && a.refresh && a.source !== "desktop") {
              ;({ a } = await currentAuth(client, a, true))
              return await performCheckIn(a, saveAuth)
            }
            throw e
          }
        } catch (e) {
          return { outcome: "failed", message: e?.message ?? String(e) }
        }
      },
      methods: [
        {
          type: "oauth",
          label: "商汤小浣熊账号 (浏览器登录 / 自动返回)",
          authorize: async () => browserSignIn(),
        },
        {
          type: "oauth",
          label: "商汤小浣熊桌面端登录态 (~/.box-agent/config/auth.json)",
          authorize: async () => desktopSignIn(),
        },
        {
          type: "oauth",
          label: "手动粘贴回调链接或 Token（非短信验证码）",
          authorize: async () => manualSignIn(),
        },
      ],
    },
  }
}

export const _internal = {
  browserSignIn,
  sameAccount,
  stripBearer,
  decodeJwtClaims,
  jwtExpiryMs,
  hexIdToDecimal,
  extractUserId,
  extractDisplayName,
  parseCatalog,
  parseCallbackInput,
  rewriteSseModel,
  beijingTodayKey,
  billKey,
  renewToken,
  desktopPlatform,
  desktopOnboardingSettled,
  renewed,
  renewing,
}
