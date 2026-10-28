// Nous Portal's public device flow and rotating inference JWTs. The host owns
// persistence; this module never reads Hermes's (or any other app's) files.
import { createHash } from "node:crypto"

export const ID = "nous-portal"
export const PORTAL = "https://portal.nousresearch.com"
export const INFERENCE = "https://inference-api.nousresearch.com/v1"
export const WELCOME = "https://welcome-api.nousresearch.com/v1"
export const CLIENT_ID = "hermes-cli"
export const SCOPE = "inference:invoke"
export const LEAD = 120000
const FALLBACK_LIFETIME = 60 * 60 * 1000
const GRANT = "urn:ietf:params:oauth:grant-type:device_code"
const DEAD = new Set(["invalid_grant", "invalid_token", "refresh_token_reused"])
const KNOWN = new Set([...DEAD, "access_denied", "expired_token", "authorization_pending", "slow_down", "invalid_client", "invalid_scope"])
const text = (v) => typeof v === "string" ? v.trim() : ""
const positive = (v) => typeof v === "number" && Number.isFinite(v) && v > 0
const object = (v) => v !== null && typeof v === "object" && !Array.isArray(v)
const same = (a, b) => a?.type === b?.type && a?.access === b?.access && a?.refresh === b?.refresh

export class AuthError extends Error {
  constructor(code, expired = false, status = 0) {
    // Never include a vendor's raw body, description, URL or token in an error.
    super(`Nous Portal: ${code}${status ? ` (HTTP ${status})` : ""}. ${expired ? "Sign in again." : "Credentials kept; retry when the problem is resolved."}`)
    this.code = code
    this.signIn = expired ? "expired" : "kept"
  }
}

export function inferenceURL(value) {
  // A network-provided address is not authority to receive the bearer. Keep
  // only the two production bases used by Hermes; no custom ports or userinfo.
  try {
    const u = new URL(value)
    if (!u.username && !u.password && !u.search && !u.hash &&
        [INFERENCE, WELCOME].includes(u.href.replace(/\/$/, ""))) return u.href.replace(/\/$/, "")
  } catch {}
  return INFERENCE
}

function verificationURL(value) {
  try {
    const u = new URL(value)
    if (u.origin === PORTAL && !u.username && !u.password && !u.hash) return u.href
  } catch {}
  throw new AuthError("invalid_verification_uri")
}

export function claims(token) {
  try {
    const parts = token.split(".")
    if (parts.length !== 3 || parts.some((p) => !p)) return {}
    const value = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"))
    return object(value) ? value : {}
  } catch { return {} }
}

function scopes(value) {
  return (Array.isArray(value) ? value : [value]).flatMap((s) => text(s).split(/[\s,]+/))
}

export function metadata(auth) {
  try {
    const m = JSON.parse(auth.refresh)
    if (m?.version === 1 && text(m.token) && typeof m.scope === "string" && typeof m.tokenType === "string") {
      return { ...m, baseURL: inferenceURL(m.baseURL) }
    }
  } catch {}
  throw new AuthError("unreadable_saved_sign_in")
}

function usable(a, now, lead = 0) {
  const m = metadata(a)
  const c = claims(a.access)
  // Decoding is only a scope/expiry check, NOT signature verification. TLS
  // protects the token exchange; the inference server verifies the signature.
  return Object.keys(c).length > 0 && m.tokenType.toLowerCase() === "bearer" &&
    [...scopes(m.scope), ...scopes(c.scope), ...scopes(c.scp)].includes(SCOPE) &&
    positive(a.expires) && a.expires > now + lead &&
    (c.exp === undefined || (typeof c.exp === "number" && Number.isFinite(c.exp) && c.exp * 1000 > now + lead))
}

function assertUsable(a, now, lead = 0) {
  if (!usable(a, now, lead)) throw new AuthError("unusable_inference_jwt")
}

function token(body, old, started) {
  const previous = old ? metadata(old) : {}
  const access = text(body?.access_token)
  const c = claims(access)
  // An absent lifetime needs a stable renewal schedule, not expires=1. An
  // explicitly expired or malformed bound must never be replaced by that default.
  const expiries = []
  // A null or non-numeric expires_in beside a valid JWT exp is ignored, the
  // exp decides, as Hermes's _remaining_ttl does (#328); with no valid exp it
  // is still a malformed bound.
  const expOK = typeof c.exp === "number" && Number.isFinite(c.exp * 1000)
  if (body?.expires_in !== undefined && (typeof body.expires_in === "number" || !expOK)) {
    expiries.push(typeof body.expires_in === "number" ? started + body.expires_in * 1000 : NaN)
  }
  if (c.exp !== undefined) expiries.push(typeof c.exp === "number" ? c.exp * 1000 : NaN)
  let expires = started + FALLBACK_LIFETIME
  if (expiries.length) expires = expiries.every(Number.isFinite) ? Math.max(1, Math.min(...expiries)) : 1
  const m = {
    version: 1,
    token: text(body?.refresh_token) || previous.token || "",
    scope: typeof body?.scope === "string" ? body.scope : previous.scope ?? "",
    tokenType: text(body?.token_type) || previous.tokenType || "Bearer",
    baseURL: inferenceURL(body?.inference_base_url),
  }
  // Keep metadata in the standard refresh field, so OpenCode stores that keep
  // only type/access/refresh/expires retain routing and the granted scope too.
  const result = { access, refresh: JSON.stringify(m), expires }
  if (!m.token) throw new AuthError("missing_refresh_token")
  if (!usable(result, started)) result.expires = 1
  return result
}

function transient(r) {
  return r.status === 408 || r.status === 429 || r.status >= 500 ||
    (r.status === 403 && r.headers.has("x-vercel-mitigated"))
}

function refused(r, refreshing = false) {
  if (transient(r)) return new AuthError("temporarily_unavailable", false, r.status)
  const raw = text(r.body?.error)
  // A proxy or WAF can return 401/403 without the token endpoint seeing the
  // grant. Only an explicit OAuth code is evidence that the grant is dead.
  const code = KNOWN.has(raw) ? raw : "oauth_request_failed"
  return new AuthError(code, refreshing && DEAD.has(code), r.status)
}

function retryAfter(r, now) {
  const value = r.headers.get("retry-after")
  if (!value) return 0
  const seconds = Number(value)
  return Number.isFinite(seconds) ? Math.max(0, seconds * 1000) : Math.max(0, Date.parse(value) - now) || 0
}

export function createAuth(client, { now = Date.now, clock = () => performance.now(), sleep = (ms) => new Promise((r) => setTimeout(r, ms)), request = (...args) => fetch(...args) } = {}) {
  // Each fingerprint points at the SAME session through every known rotation.
  // Old token bytes are not retained. No timer can evict an unsaved successor
  // and make a stale caller spend an already-spent refresh token again.
  const sessions = new Map()
  const key = (a) => createHash("sha256").update(metadata(a).token).digest("hex")
  const session = (a) => {
    const k = key(a)
    if (!sessions.has(k)) sessions.set(k, { fields: { access: a.access, refresh: a.refresh, expires: a.expires } })
    return sessions.get(k)
  }

  async function post(path, form, extra = {}, timeout = 10000) {
    let res
    try {
      res = await request(PORTAL + path, {
        method: "POST", redirect: "error",
        headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded", ...extra },
        body: new URLSearchParams(form).toString(), signal: AbortSignal.timeout(Math.max(1, Math.ceil(timeout))),
      })
    } catch { throw new AuthError("token_endpoint_unavailable") }
    let body
    try { body = await res.json() } catch {}
    return { ok: res.ok && object(body) && !body.error, status: res.status, headers: res.headers, body }
  }

  function renew(s) {
    if (s.pending) return s.pending
    if (s.error && now() < s.retryAt) return Promise.reject(s.error)
    s.pending = (async () => {
      await s.saving?.catch(() => {})
      const started = now()
      const r = await post("/api/oauth/token", { grant_type: "refresh_token", client_id: CLIENT_ID }, {
        "x-nous-refresh-token": metadata(s.fields).token,
      })
      if (!r.ok) throw refused(r, true)
      const got = token(r.body, s.fields, started)
      // Record the rotated pair BEFORE persistence or JWT validation. A bad
      // JWT or a failed write must never make the old refresh token usable.
      s.fields = got
      sessions.set(key(got), s)
      s.error = undefined
      if (!usable(got, now(), LEAD)) {
        s.error = new AuthError("unusable_inference_jwt")
        s.retryAt = now() + 30000
      }
      return got
    })().catch((e) => {
      s.error = e
      s.retryAt = e.signIn === "expired" ? Infinity : now() + 30000
      throw e
    }).finally(() => { s.pending = undefined })
    return s.pending
  }

  async function read(getAuth) {
    try { return await getAuth() } catch { throw new AuthError("could_not_read_sign_in") }
  }

  function persist(s, getAuth, was) {
    const saved = (s.saving ?? Promise.resolve()).catch(() => {}).then(async () => {
      const current = await read(getAuth)
      if (!same(current, was)) return undefined
      const fields = s.fields
      if (current.access === fields.access && current.refresh === fields.refresh && current.expires === fields.expires) return fields
      try {
        if (typeof client?.auth?.set !== "function") throw new Error()
        const r = await client.auth.set({ path: { id: ID }, body: { ...current, ...fields, type: "oauth" } })
        if (r?.error) throw new Error()
      } catch { throw new AuthError("could_not_save_rotated_token") }
      return fields
    })
    const finished = saved.finally(() => { if (s.saving === finished) s.saving = undefined })
    s.saving = finished
    return finished
  }

  async function fresh(getAuth, rejected) {
    // Re-read after another writer wins rather than overwriting a new login.
    // Magpie also guards the host-owned refresh save atomically; OpenCode's
    // auth.set has no compare-and-swap interface, so this is an optimistic check.
    for (let attempt = 0; attempt < 3; attempt++) {
      const was = await read(getAuth)
      if (was?.type !== "oauth") throw new AuthError("not_signed_in", true)
      const s = session(was)
      let a = s.fields
      const force = rejected !== undefined && rejected === a.access
      if (force || !usable(a, now(), LEAD)) {
        try { a = await renew(s) } catch (e) {
          if (!same(await read(getAuth), was)) continue
          if (e.signIn === "expired" || force || !usable(a, now())) throw e
          // A temporary early-refresh outage does not kill a still-valid JWT.
        }
      }
      a = await persist(s, getAuth, was)
      if (!a) continue
      assertUsable(a, now())
      return { ...was, ...a, signIn: a.access !== was.access || a.refresh !== was.refresh ? "renewed" : "kept" }
    }
    throw new AuthError("sign_in_changed_during_refresh")
  }

  async function refresh(auth) {
    if (auth?.type !== "oauth") return undefined
    const s = session(auth)
    if (usable(s.fields, now(), LEAD)) return { ...s.fields }
    // The host, not this hook, persists the result with its account identity
    // guard. Return even an unusable successor so a rotated token is not lost;
    // fresh() will not send that JWT, and a later refresh can recover it.
    return { ...await renew(s) }
  }

  async function authorize() {
    const started = clock()
    const r = await post("/api/oauth/device/code", { client_id: CLIENT_ID, scope: SCOPE })
    if (!r.ok) throw refused(r)
    const b = r.body
    if (!text(b.device_code) || !text(b.user_code) || !positive(b.expires_in) ||
        (b.interval !== undefined && !positive(b.interval))) throw new AuthError("invalid_device_code_response")
    const plain = verificationURL(b.verification_uri)
    const url = b.verification_uri_complete ? verificationURL(b.verification_uri_complete) : plain
    const deadline = started + Math.min(b.expires_in * 1000, 30 * 60 * 1000)
    let interval = (b.interval ?? 5) * 1000
    let wait = interval
    let pending
    const poll = async () => {
      while (clock() < deadline) {
        // RFC 8628: the advertised interval is a minimum, including the first
        // poll. slow_down adds five seconds; never use Hermes's one-second cap.
        await sleep(Math.min(wait, deadline - clock()))
        if (clock() >= deadline) break
        let answer
        const at = now()
        try {
          answer = await post("/api/oauth/token", { grant_type: GRANT, client_id: CLIENT_ID, device_code: b.device_code }, {}, Math.min(10000, deadline - clock()))
        } catch (e) {
          if (e.code !== "token_endpoint_unavailable") throw e
          wait = Math.max(interval, wait * 2)
          continue
        }
        if (answer.ok) {
          const got = token(answer.body, undefined, at)
          assertUsable(got, now(), LEAD)
          const subject = claims(got.access).sub
          return { type: "success", ...got, ...(typeof subject === "string" && subject ? { accountId: subject } : {}) }
        }
        if (transient(answer)) {
          wait = Math.max(interval, wait * 2, retryAfter(answer, now()))
          continue
        }
        if (answer.body?.error === "authorization_pending") { wait = interval; continue }
        if (answer.body?.error === "slow_down") { interval += 5000; wait = interval; continue }
        throw refused(answer)
      }
      throw new AuthError("expired_token")
    }
    return {
      url, method: "auto",
      instructions: `Sign in to Nous Portal in your browser and confirm code ${b.user_code}. Finish any CAPTCHA in the browser. This finishes automatically.`,
      callback() {
        pending ??= poll().catch((e) => ({ type: "failed", error: e instanceof AuthError ? e.message : "Nous Portal sign-in failed; please retry." }))
        return pending
      },
    }
  }
  function current(auth) {
    if (auth?.type !== "oauth") throw new AuthError("not_signed_in", true)
    assertUsable(auth, now())
    return auth
  }
  return { authorize, refresh, fresh, current, read }
}
