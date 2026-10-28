// Cline (cline.bot) as a magpie / OpenCode provider plugin.
//
// The same sign-in Cline's own client runs — a WorkOS device code the browser
// approves, traded at Cline's register endpoint for the account's token pair —
// or a plain API key from app.cline.bot. Chat completions go to Cline's
// gateway in OpenAI's format; the model list comes from its recommended-models
// and cloud-models feeds; usage reads the account's credit balance and, on
// ClinePass, the plan's 5-hour, weekly and monthly limits.
import { STATUS_CODES } from "node:http"
import { createHash } from "node:crypto"

const PROVIDER = "cline"
const API = "https://api.cline.bot/api/v1"

// the WorkOS client Cline's own apps sign in with (their source, production)
const WORKOS_CLIENT = "client_01K3A541FN8TA3EPPHTD2325AR"
const WORKOS_DEVICE = "https://api.workos.com/user_management/authorize/device"
const WORKOS_TOKEN = "https://api.workos.com/user_management/authenticate"
const DEVICE_GRANT = "urn:ietf:params:oauth:grant-type:device_code"

// the gateway wants the account's token told apart from a raw API key: a
// signed-in one rides as "workos:<jwt>" (Cline's own clients send it so)
const WORKOS_PREFIX = "workos:"

const REFRESH_LEAD = 5 * 60 * 1000 // a token this close to its end is refreshed before a request
const RENEW_LEAD = 10 * 60 * 1000 // and this close, magpie renews it ahead of time (auth.refresh)
// a refresh that failed transiently keeps a token with this much left, as
// Cline's getValidClineCredentials does (DEFAULT_RETRYABLE_TOKEN_GRACE_MS);
// only once it has actually expired is the sign-in the one to blame
const RETRYABLE_GRACE = 30 * 1000
const DEVICE_CAP = 600 // seconds the browser may take, however long the code lives

// CLIENT is who every request says it comes from: the same client surface
// Cline's own CLI runs as, headers down (apps/cli registers exactly this
// identity, and the free models' gate reads these). Pinned to Cline's current
// release — when they ship a new one, move these along (the numbers are the
// only thing that has to match).
const CLIENT = { type: "cline-cli", version: "3.0.68", platform: "cli", core: "0.0.90" }

// Pass the host's session through the chat.headers hook to the loader's
// fetch. This private header never leaves the plugin.
const SESSION = "x-magpie-cline-session"

// magpie's synthetic session from the first user message doesn't identify a
// task: unrelated conversations can start with that same message.
function sessionOf(input) {
	const s = String(input?.sessionID ?? "").trim()
	if (!s || /^magpie-[0-9a-f]{24}$/.test(s)) return ""
	return s.slice(0, 128)
}

function taskIdOf(session) {
	const bytes = createHash("sha256").update("cline task\0").update(session).digest().subarray(0, 16)
	const b = Buffer.from(bytes)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const h = b.toString("hex")
	return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

// clientHeaders is the header set resolveProviderRequestHeaders builds for a
// client with that identity; official clients supply a task id per task.
function clientHeaders(taskId) {
	return {
		"HTTP-Referer": "https://cline.bot",
		"X-Title": "Cline",
		"User-Agent": `Cline/${CLIENT.version}`,
		"X-IS-MULTIROOT": "false",
		"X-CLIENT-TYPE": CLIENT.type,
		"X-CLIENT-VERSION": CLIENT.version,
		"X-PLATFORM": CLIENT.platform,
		"X-PLATFORM-VERSION": CLIENT.version,
		"X-CORE-VERSION": CLIENT.core,
		...(taskId ? { "X-Task-ID": taskId } : {}),
	}
}

// ---- small helpers ------------------------------------------------------------

const firstOf = (...vs) => vs.find((v) => typeof v === "string" && v.trim())?.trim() ?? ""

// compactNumber is a whole number bare, else two decimals with the trailing
// zeroes off (magpie's subscription_usage.go)
function compactNumber(n) {
	if (!Number.isFinite(n)) return "0"
	if (n === Math.trunc(n)) return String(Math.trunc(n))
	return String(Number(n.toFixed(2)))
}

// toMs is an expiry Cline sends as an ISO datetime; one it didn't send (or one
// that won't parse) is taken as an hour out rather than as "never"
function toMs(v) {
	const t = typeof v === "string" ? Date.parse(v) : Number(v)
	if (Number.isFinite(t) && t > Date.now()) return t
	return Date.now() + 60 * 60 * 1000
}

// timeOf reads a reset time: epoch seconds, epoch ms, or a date string
function timeOf(m, keys) {
	for (const k of keys) {
		const v = m?.[k]
		if (typeof v === "number" && v > 1e12) return new Date(v).toISOString()
		if (typeof v === "number" && v > 1e9) return new Date(v * 1000).toISOString()
		if (typeof v === "string") {
			const t = Date.parse(v)
			if (!Number.isNaN(t)) return new Date(t).toISOString()
		}
	}
	return ""
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// ---- the sign-in as it is kept --------------------------------------------------

// authOf reads a sign-in as magpie keeps it: OpenCode's type field, and what
// this plugin needs beside it to renew it later
const authOf = (a) => ({
	type: a?.type === "api" ? "api" : "oauth",
	access: typeof a?.access === "string" ? a.access : "",
	refresh: typeof a?.refresh === "string" ? a.refresh : "",
	expires: Number(a?.expires) || 0,
	key: typeof a?.key === "string" ? a.key : "",
	uid: typeof a?.uid === "string" ? a.uid : "",
	email: typeof a?.email === "string" ? a.email : "",
	name: typeof a?.name === "string" ? a.name : "",
	accountId: typeof a?.accountId === "string" ? a.accountId : "",
})

// bearerOf is what an Authorization header carries: a signed-in account's
// access token with Cline's routing prefix, or the raw API key
function bearerOf(a) {
	if (a?.type === "api") return a.key
	const access = typeof a?.access === "string" ? a.access : ""
	if (!access) return ""
	// the prefix is matched the way Cline's own client matches it, whatever case
	return access.toLowerCase().startsWith(WORKOS_PREFIX) ? access : WORKOS_PREFIX + access
}

// parseAuth reads Cline's token answer ({accessToken, refreshToken, expiresAt,
// userInfo}) into what this plugin keeps, with a refresh to fall back on when
// the answer didn't rotate it
function parseAuth(data, fallbackRefresh = "") {
	const access = typeof data?.accessToken === "string" ? data.accessToken : ""
	if (!access) return { error: "Cline sent back no access token" }
	const refresh = firstOf(data?.refreshToken, fallbackRefresh)
	if (!refresh) return { error: "Cline sent back no refresh token" }
	const ui = data?.userInfo && typeof data.userInfo === "object" ? data.userInfo : {}
	const uid = firstOf(ui.clineUserId, ui.subject)
	const email = firstOf(ui.email)
	return {
		access,
		refresh,
		expires: toMs(data?.expiresAt),
		uid,
		email,
		name: firstOf(ui.name),
		accountId: firstOf(email, uid),
	}
}

// ---- talking to Cline ------------------------------------------------------------

// Lapsed is the sign-in itself gone: a refused refresh, or an API that turned
// the account's token away. magpie marks the account from e.expired.
class Lapsed extends Error {
	constructor(message) {
		super(message)
		this.expired = true
	}
}

// clineApi is one call to Cline's own API, and the {success, data} envelope it
// answers in. lapsed says which answers mean the sign-in itself is gone (401
// and 403, unless the caller knows this endpoint refuses otherwise); the rest
// is a request that failed.
async function clineApi(url, { method = "GET", body, headers = {}, signal, lapsed } = {}) {
	const gone = lapsed ?? ((status) => status === 401 || status === 403)
	let res
	try {
		res = await fetch(url, {
			method,
			headers: {
				Accept: "application/json",
				...clientHeaders(),
				...headers,
				...(body !== undefined ? { "Content-Type": "application/json" } : {}),
			},
			...(body !== undefined ? { body: JSON.stringify(body) } : {}),
			signal: signal ?? AbortSignal.timeout(15_000),
		})
	} catch (e) {
		throw new Error(`Cline: ${e?.message ?? e}`)
	}
	const text = (await res.text().catch(() => "")).trim()
	if (gone(res.status, text))
		throw new Lapsed(`the sign-in lapsed — Cline refused it (HTTP ${res.status}${text ? `: ${text.slice(0, 200)}` : ""}); sign in to Cline again`)
	if (!res.ok) throw Object.assign(new Error(`Cline request failed (HTTP ${res.status}${text ? `: ${text.slice(0, 200)}` : ""})`), { status: res.status })
	const env = safeJson(text)
	if (env && typeof env === "object" && "success" in env) {
		if (!env.success) throw new Error(`Cline request failed${env.error ? `: ${env.error}` : ""}`)
		return env.data
	}
	return env
}

// safeJson is text as JSON, or null
const safeJson = (text) => {
	try {
		return JSON.parse(text)
	} catch {
		return null
	}
}

// refresh trades the refresh token for a new pair; Cline rotates it, so the
// caller saves what comes back. A dead token is refused as 400 invalid_grant
// (WorkOS's wording rides in the body), not 401 — that refusal is the sign-in
// gone, anything else is worth a retry later.
async function refresh(a) {
	if (!a.refresh) throw new Lapsed("Cline's refresh token is gone; sign in to Cline again")
	const data = await clineApi(`${API}/auth/refresh`, {
		method: "POST",
		body: { refreshToken: a.refresh, grantType: "refresh_token" },
		lapsed: (status, text) => [400, 401, 403].includes(status) && /invalid|expired|revoked|unauthorized/i.test(text),
	})
	const p = parseAuth(data, a.refresh)
	if (p.error) throw new Error(`Cline token refresh: ${p.error}`)
	return p
}

// ---- the device sign-in ----------------------------------------------------------

// deviceAuthorize starts the flow: a code to approve in the browser
async function deviceAuthorize() {
	let res
	try {
		res = await fetch(WORKOS_DEVICE, {
			method: "POST",
			headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
			body: new URLSearchParams({ client_id: WORKOS_CLIENT }).toString(),
			signal: AbortSignal.timeout(15_000),
		})
	} catch (e) {
		throw new Error(`Cline sign-in: ${e?.message ?? e}`)
	}
	if (!res.ok) throw new Error(`Cline sign-in: the device code request failed (HTTP ${res.status})`)
	const d = await res.json().catch(() => null)
	if (!d?.device_code || !d?.verification_uri) throw new Error("Cline sign-in: the device code answer was incomplete")
	return d
}

// pollDevice waits the device code out, as Cline's pollWorkOSTokens does: the
// interval only grows (slow_down raises it for every later poll, RFC 8628), it
// starts at a second, and a 5xx, a non-JSON reply or a network error is a poll
// to try again, not the sign-in failing.
async function pollDevice(d, { fetchImpl = fetch, sleep: sleeper = sleep, now = Date.now } = {}) {
	const deadline = now() + Math.min(Number(d?.expires_in) > 0 ? Number(d.expires_in) : 300, DEVICE_CAP) * 1000
	let interval = Math.max(1, Number(d?.interval) > 0 ? Math.floor(Number(d.interval)) : 5)
	while (now() < deadline) {
		await sleeper(interval * 1000)
		let res = null
		let j = null
		try {
			res = await fetchImpl(WORKOS_TOKEN, {
				method: "POST",
				headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
				body: new URLSearchParams({ grant_type: DEVICE_GRANT, device_code: d.device_code, client_id: WORKOS_CLIENT }).toString(),
				signal: AbortSignal.timeout(15_000),
			})
			j = await res.json().catch(() => null)
		} catch {}
		// WorkOS having a moment, or the network: not the sign-in
		if (!res || res.status >= 500) continue
		// a reply that isn't JSON and isn't a 5xx is the poll turned away: an
		// HTML 400 would otherwise keep polling to the deadline (ten minutes)
		// and fail there anyway
		if (j === null) {
			if (res.status >= 400) throw new Error(`Cline sign-in: the token poll was refused (HTTP ${res.status})`)
			continue
		}
		if (j.error === "authorization_pending") continue
		if (j.error === "slow_down") {
			interval += 1
			continue
		}
		if (j.error) throw new Error(`Cline sign-in: ${j.error}${j.error_description ? ` (${j.error_description})` : ""}`)
		if (typeof j.access_token !== "string" || !j.access_token) throw new Error("Cline sign-in: the token answer had no access token")
		return j
	}
	throw new Error("the sign-in wasn't finished in time")
}

// deviceSignIn is the browser method: the device code page opened, WorkOS
// polled until the code is approved, and the WorkOS tokens traded at Cline's
// register endpoint for the account's own pair
async function deviceSignIn() {
	const d = await deviceAuthorize()
	return {
		url: firstOf(d.verification_uri_complete, d.verification_uri, "https://app.cline.bot"),
		instructions: `Approve the sign-in in the browser${d.user_code ? ` — the code is ${d.user_code}` : ""}.`,
		method: "auto",
		async callback() {
			let j
			try {
				j = await pollDevice(d)
			} catch (e) {
				return { type: "failed", error: e?.message ?? String(e) }
			}
			try {
				const data = await clineApi(`${API}/auth/register`, { method: "POST", body: { accessToken: j.access_token, refreshToken: j.refresh_token ?? "" }, signal: AbortSignal.timeout(30_000) })
				const p = parseAuth(data, j.refresh_token ?? "")
				if (p.error) throw new Error(`Cline sign-in: ${p.error}`)
				return { type: "success", ...p }
			} catch (e) {
				return { type: "failed", error: e?.message ?? String(e) }
			}
		},
	}
}

// ---- models ----------------------------------------------------------------------

// CAPS is the shorthand a model id spells in capitals, MIXED the ones with a
// capital of their own
const CAPS = new Set(["gpt", "glm", "ai", "api", "llm", "k3"])
const MIXED = { mimo: "MiMo" }

// prettify is a model id as a name: anthropic/claude-sonnet-5.5 becomes
// "Claude Sonnet 5.5"
function prettify(id) {
	const seg = String(id).split("/").pop() ?? ""
	return seg
		.split(/[-_]/)
		.filter(Boolean)
		.map((w) => MIXED[w.toLowerCase()] ?? (CAPS.has(w.toLowerCase()) ? w.toUpperCase() : w.charAt(0).toUpperCase() + w.slice(1).toLowerCase()))
		.join(" ")
}

// DEFAULT_MODELS is the list before Cline's feed answered: the bundled
// snapshot of Cline's own recommended-models feed — its recommended models,
// its free ones and ClinePass's (cline-recommended.generated.ts). The first is
// also Cline's own default.
const DEFAULT_MODELS = [
	{ id: "anthropic/claude-sonnet-5.5" },
	{ id: "anthropic/claude-opus-5.5" },
	{ id: "anthropic/claude-sonnet-5" },
	{ id: "openai/gpt-6-astra" },
	{ id: "openai/gpt-6.1-sol" },
	{ id: "spacexai/grok-4.7" },
	{ id: "moonshotai/kimi-k3" },
	{ id: "cline-free/deepseek-v4.1-flash", free: true },
	{ id: "stealth/space-bunny-alpha", free: true },
	{ id: "cline-free/mimo-v2.6-flash", free: true },
	{ id: "cline-free/muse-spark-1.3-contributor", free: true },
	{ id: "cline-pass/deepseek-v4.1-flash-1017", input: { text: true, image: true } },
	{ id: "cline-pass/deepseek-v4.1-flash", input: { text: true, image: true } },
	{ id: "cline-pass/mimo-v2.6-flash" },
	{ id: "cline-pass/mimo-v2.6-pro" },
	{ id: "cline-pass/glm-5.3" },
	{ id: "cline-pass/deepseek-v4-pro" },
	{ id: "cline-pass/qwen3.8-max" },
	{ id: "cline-pass/muse-spark-1.3-contributor" },
	{ id: "cline-pass/kimi-k3" },
	{ id: "cline-pass/glm-5.3-flash" },
	{ id: "cline-pass/qwen3.7-max" },
	{ id: "cline-pass/qwen3.7-plus" },
	{ id: "cline-pass/minimax-m3" },
	{ id: "cline-pass/mimo-v2.5-pro" },
	{ id: "cline-pass/mimo-v2.5" },
].map((m) => ({ name: prettify(m.id) + (m.free ? " (free)" : m.id.startsWith("cline-pass/") ? " (ClinePass)" : ""), ...m }))

const MODEL_ID = /^[a-z0-9][a-z0-9_.-]*\/[a-z0-9][a-z0-9_.-]*$/i

// FEED_GROUPS is the recommended-models answer's shape, as Cline's own client
// reads it (cline-recommended.generated.ts): four arrays, and nothing else.
// clineCloud rides only under the client's includeClineCloudModels, so it is
// left out here the way the client leaves it out by default.
const FEED_GROUPS = ["recommended", "free", "clinePass"]

// parseFeed reads the recommended-models answer: the four arrays by name, not
// every value in the object (a description or tag is not a model). A model the
// feed lists twice keeps the stronger gate, so one in both recommended and
// free keeps its "(free)" mark.
function parseFeed(v) {
	const payload = v && typeof v === "object" && !Array.isArray(v) ? v : {}
	const by = new Map()
	// entry is one model: an id bare or in an object, named for the plan gate a
	// picker should show. A feed name that is just the id (or its last part) is
	// the raw id, not a name, so it is prettified like the bundled list's.
	const entry = (e, group) => {
		const o = e && typeof e === "object" && !Array.isArray(e) ? e : {}
		const raw = typeof e === "string" ? e.trim() : firstOf(o.id, o.model, o.modelId, o.model_id, o.key, o.slug)
		if (!MODEL_ID.test(raw)) return null
		const free = group === "free" || o.free === true || o.free === "true"
		const pass = group === "clinePass" || raw.startsWith("cline-pass/")
		const given = firstOf(o.display_name, o.displayName, o.name, o.label)
		const last = raw.split("/").pop()
		return { id: raw, name: given && given !== raw && given !== last ? given : prettify(raw), free, pass }
	}
	const add = (e, group) => {
		const m = entry(e, group)
		if (!m) return
		const was = by.get(m.id)
		if (!was) {
			by.set(m.id, m)
			return
		}
		// a model in both recommended and free keeps the free mark (and name)
		if (m.free && !was.free) {
			was.free = true
			was.name = m.name
		}
		if (m.pass) was.pass = true
	}
	for (const group of FEED_GROUPS) for (const e of Array.isArray(payload[group]) ? payload[group] : []) add(e, group)
	return [...by.values()].map((m) => ({ ...m, name: m.name + (m.free ? " (free)" : m.pass ? " (ClinePass)" : "") }))
}

// fetchRecommended is Cline's recommended-models feed, which it serves without
// a sign-in
async function fetchRecommended() {
	let res
	try {
		res = await fetch(`${API}/ai/cline/recommended-models`, {
			headers: { Accept: "application/json", ...clientHeaders() },
			signal: AbortSignal.timeout(10_000),
		})
	} catch (e) {
		throw new Error(`Cline model list: ${e?.message ?? e}`)
	}
	if (!res.ok) throw new Error(`Cline model list: HTTP ${res.status}`)
	const env = await res.json().catch(() => null)
	return parseFeed(env && typeof env === "object" && "data" in env ? env.data : env)
}

// tokens is a positive whole count of tokens, or undefined
function tokens(...vs) {
	for (const v of vs) {
		const n = typeof v === "string" ? Number(v) : v
		if (Number.isFinite(n) && n > 0) return Math.floor(n)
	}
	return undefined
}

// fetchCloudModels is Cline's whole cloud catalog, which its clients load
// beside the recommended feed (loadCloudModels reads /ai/cline/models): the
// usage-billed models the recommended feed doesn't name. Entries carry an id,
// a display name and, OpenRouter's way, the window (context_length, or its
// top provider's).
async function fetchCloudModels() {
	let res
	try {
		res = await fetch(`${API}/ai/cline/models`, {
			headers: { Accept: "application/json", ...clientHeaders() },
			signal: AbortSignal.timeout(10_000),
		})
	} catch (e) {
		throw new Error(`Cline model catalog: ${e?.message ?? e}`)
	}
	if (!res.ok) throw new Error(`Cline model catalog: HTTP ${res.status}`)
	const j = await res.json().catch(() => null)
	const list = Array.isArray(j) ? j : Array.isArray(j?.data) ? j.data : []
	return list.flatMap((e) => {
		// the catalog's ids are bare (no provider prefix), unlike the
		// recommended feed's: any non-empty id is one
		const id = typeof e?.id === "string" ? e.id.trim() : ""
		if (!id) return []
		const given = firstOf(e.display_name, e.displayName, e.name)
		const last = id.split("/").pop()
		const context = tokens(e.context_length, e.top_provider?.context_length, e.context)
		// the reply limit is left out: OpenRouter's max_completion_tokens can
		// be most of the window (kimi-k3: 943718 of 1048576), and an agent
		// keeps that much of the window free for the reply, so it would
		// compact a tenth of the way in
		const input = inputsOf(e)
		return [{ id, name: given && given !== id && given !== last ? given : prettify(id), free: false, pass: id.startsWith("cline-pass/"), ...(context && { context }), ...(input && { input }) }]
	})
}

// KINDS are the kinds of input OpenCode's capabilities name
const KINDS = ["text", "image", "audio", "video", "pdf"]

// inputsOf is what a catalog entry says the model takes (OpenRouter's
// architecture.input_modalities: ["text", "image"]), undefined when it says
// nothing
function inputsOf(e) {
	const l = e?.architecture?.input_modalities ?? e?.input_modalities
	if (!Array.isArray(l) || !l.length) return undefined
	const has = new Set(l.map((k) => String(k).toLowerCase()))
	return Object.fromEntries(KINDS.map((k) => [k, has.has(k)]))
}

// bareOf is a model id without its provider and a variant after ":"
// (deepseek/deepseek-v4.1-flash:batch is deepseek-v4.1-flash)
const bareOf = (id) => String(id).split("/").pop().split(":")[0].toLowerCase()

// inputsFor is what Cline's catalog says a model takes: by its own id, else,
// for a model the catalog lists under its maker's id only (the feed's
// cline-pass/deepseek-v4.1-flash is the catalog's deepseek/deepseek-v4.1-flash),
// by the model after the prefix while every entry of that model agrees.
// Undefined when the catalog doesn't say, so magpie asks models.dev.
function inputsFor(id, cloud) {
	const own = cloud.find((c) => c.id === id)
	if (own) return own.input
	const bare = bareOf(id)
	const said = cloud.filter((c) => c.input && bareOf(c.id) === bare).map((c) => c.input)
	if (!said.length || said.some((x) => KINDS.some((k) => x[k] !== said[0][k]))) return undefined
	return said[0]
}

// fetchFeed is both of Cline's model lists merged, recommended first: the
// recommended feed is required, the cloud catalog is what it adds.
async function fetchFeed() {
	const [rec, cloud] = await Promise.all([fetchRecommended(), fetchCloudModels().catch(() => [])])
	// a recommended model takes the catalog's window for it: the recommended
	// feed gives none
	// and what the catalog says it takes (images: Cline's own client reads
	// it there), else a ClinePass or free model takes what the catalog says
	// the model after its prefix takes (silent on Discord: DeepSeek V4.1
	// Flash and GLM 5.3 Flash were taken for text-only, so magpie had their
	// images described by another model)
	const sizes = new Map(cloud.map((m) => [m.id, m]))
	const ms = rec.map((m) => {
		const c = sizes.get(m.id)
		const input = inputsFor(m.id, cloud)
		return { ...m, ...(c?.context && { context: c.context }), ...(input && { input }) }
	})
	const seen = new Set(ms.map((m) => m.id))
	for (const m of cloud) {
		if (seen.has(m.id)) continue
		seen.add(m.id)
		ms.push(m)
	}
	if (!ms.length) throw new Error("Cline listed no models")
	return ms
}

const configModel = (m) => ({
	name: m.name ?? m.id,
	limit: { context: m.context ?? 0, output: 0 },
	tool_call: true,
})

const runtimeModel = (m) => ({
	id: m.id,
	providerID: PROVIDER,
	name: m.name || m.id,
	api: { id: m.id, url: API, npm: "@ai-sdk/openai-compatible" },
	status: "active",
	headers: {},
	options: {},
	cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
	// 0 where Cline's catalog doesn't list the model (its cline-free/… and
	// cline-pass/… ids): magpie then gives the window models.dev has for the
	// model after the prefix
	limit: { context: m.context ?? 0, output: 0 },
	capabilities: {
		temperature: true,
		reasoning: false,
		attachment: !!m.input?.image,
		toolcall: true,
		// what Cline's catalog says it takes; where it says nothing, images
		// are left unsaid rather than said no to, so magpie takes models.dev's
		// word for the model
		input: m.input ?? { text: true },
		output: { text: true, image: false, audio: false, video: false, pdf: false },
		interleaved: false,
	},
	release_date: "",
	variants: {},
	free: !!m.free,
})

// ---- the reply as errors -----------------------------------------------------------

// the refusals Cline's own client keys on, as it spells them (errors.ts and
// ClineError.ts): whole phrases and codes, never a loose substring — a 403
// that happens to mention "tokens" is not a lapsed sign-in
const NOT_SUBSCRIBED = "the user is not subscribed to required model plan"
const NOT_SUBSCRIBED_FORMATTED = "no access to clinepass subscription models yet. subscribe to clinepass"

// failure is an upstream chat refusal as OpenAI's API gives it. A refused
// chat is the sign-in at work, not the sign-in gone: only a refused refresh
// marks the account (errorResponse keeps it). The gates Cline puts in front
// of a model each say what they want: a plan subscription, or money.
function failure(status, text) {
	if (status < 400 || status > 599) status = 502
	let msg = String(text ?? "").trim()
	let j = null
	try {
		j = JSON.parse(text)
	} catch {}
	const code = firstOf(j?.error?.code, j?.code, j?.error?.error)
	if (j) {
		if (typeof j?.error?.message === "string") msg = j.error.message
		else if (typeof j?.message === "string") msg = j.message
	}
	msg ||= STATUS_CODES[status] ?? `HTTP ${status}`
	const low = msg.toLowerCase()
	// a model the account isn't entitled to: the sign-in is fine, the plan
	// isn't there (the cline-pass group, mostly)
	if (code === "ENTITLEMENT_ERROR" || low.includes(NOT_SUBSCRIBED) || low.includes(NOT_SUBSCRIBED_FORMATTED))
		return {
			status: 403,
			message: "this model needs a subscription the account hasn't got (ClinePass) — pick a usage-billed model, or subscribe at app.cline.bot (" + msg.replace(/^Error\s*\d*[:：]?\s*/, "") + ")",
		}
	// an empty account: say what the gateway said and where to fill it. The
	// code is the one Cline's client keys on; "Insufficient balance" is the
	// gateway's older wording for the same answer
	if (status === 402 || code === "insufficient_credits" || low.includes("insufficient_credits") || (low.includes("insufficient balance") && low.includes("cline credits balance"))) {
		const bal = Number(j?.error?.current_balance ?? j?.current_balance)
		const at = firstOf(j?.error?.buy_credits_url, j?.buy_credits_url) || "https://app.cline.bot/credits"
		return { status: 429, message: `out of credits ($${Number.isFinite(bal) ? bal.toFixed(2) : "0.00"}) — top up at ${at}` }
	}
	// the account's token was refused: 401 always, and a 403 only when it
	// says so in so many words — not when it merely mentions "token"
	if (status === 401 || (status === 403 && /^(unauthorized|invalid[ _]token|sign[ -]?in|not signed in|token (?:expired|invalid|revoked)|(?:access |refresh )?token expired)$/i.test(msg.trim())))
		return { status: 401, message: "the sign-in lapsed — sign in again" }
	// a spend cap or a rate limit, as Cline's client names them
	if (status === 429 || code === "SPEND_LIMIT_EXCEEDED" || /\b(?:rate limit|too many requests|quota exceeded|spend limit)\b/i.test(msg)) return { status: 429, message: "usage limit reached: " + msg }
	return { status, message: msg.slice(0, 2000) }
}

const errorResponse = ({ status, message, signIn }) =>
	new Response(JSON.stringify({ error: { message, type: "cline_error", code: status } }), {
		status,
		headers: { "Content-Type": "application/json", ...(signIn ? { "X-Magpie-Sign-In": signIn } : {}) },
	})

// signed is res saying what it means for the sign-in: a renewed token cleared
// the lapse mark whatever came of the request
function signed(res, renewed) {
	const said = renewed ? "renewed" : res.ok ? "kept" : null
	if (!said) return res
	const headers = new Headers(res.headers)
	headers.delete("content-length")
	headers.delete("content-encoding")
	headers.set("X-Magpie-Sign-In", said)
	return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}

async function bodyText(input, init) {
	const b = init?.body ?? (input instanceof Request ? await input.clone().text() : undefined)
	if (b === undefined || b === null) return ""
	if (typeof b === "string") return b
	if (b instanceof URLSearchParams) return b.toString()
	if (b instanceof ArrayBuffer || ArrayBuffer.isView(b)) return new TextDecoder().decode(b)
	try {
		return await new Response(b).text()
	} catch {
		return ""
	}
}

// unwrapped is Cline's non-streaming answer with its envelope taken off: a
// finished chat completion rides as {"data": …}, the shape the official
// clients take apart. Streaming answers are plain SSE and pass untouched.
async function unwrapped(res) {
	if (!(res.headers.get("content-type") ?? "").includes("application/json")) return res
	const text = await res.text()
	let j
	try {
		j = JSON.parse(text)
	} catch {
		return new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers })
	}
	if (j && typeof j === "object" && Array.isArray(j.data?.choices))
		return new Response(JSON.stringify(j.data), { status: res.status, statusText: res.statusText, headers: { "Content-Type": "application/json" } })
	return new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers })
}

// ---- usage -----------------------------------------------------------------------

// usd is a dollar amount as it's usually written: cents above a cent, four
// decimals under one (an account with half a cent isn't "$0.00" to its owner)
const usd = (n) => (n === 0 || n >= 0.01 ? `$${n.toFixed(2)}` : `$${Number(n.toFixed(4))}`)

// balanceUSD is the one balance shape Cline serves today: {balance} in
// millionths of a dollar — the chat gateway's current_balance is the same
// number with the decimal point moved six places
function balanceUSD(v) {
	const n = typeof v?.balance === "number" ? v.balance : typeof v?.balance === "string" && Number.isFinite(Number(v.balance)) ? Number(v.balance) : NaN
	if (!Number.isFinite(n) || n < 0) return null
	return usd(n / 1e6)
}

// creditsOf is the account's credit balance, a shape Cline hasn't fixed: a
// total with what is used or left of it, or a count alone. A whole is what
// magpie says as used or as left, so it is { window } — carrying the count
// itself (amount of limit) for a card that says either. A count alone is no
// share of anything: what is left is { balance }, the card's balance, as
// magpie's built-in Cline says it; what is used alone is { window } set
// aside, a line that holds nothing up. (A window made of a count alone read
// as 0% used — its bar full, "100% left", whatever the balance.)
function creditsOf(v) {
	if (!v || typeof v !== "object") return {}
	const flat = {}
	const collect = (n, depth) => {
		if (!n || typeof n !== "object" || depth > 2) return
		for (const [k, x] of Object.entries(n)) {
			if (typeof x === "number" && Number.isFinite(x)) flat[k.toLowerCase()] = x
			else if (typeof x === "string" && x.trim() !== "" && Number.isFinite(Number(x.trim()))) flat[k.toLowerCase()] = Number(x.trim())
			else collect(x, depth + 1)
		}
	}
	collect(v, 0)
	const pick = (keys) => {
		for (const k of keys) if (typeof flat[k] === "number") return flat[k]
		return NaN
	}
	const total = pick(["total", "totalcredits", "totalbalance", "creditstotal", "limit", "amount", "quota"])
	const used = pick(["used", "usedcredits", "usedamount", "spent", "consumed", "usagetotal"])
	const remaining = pick(["remaining", "remainingcredits", "remainingbalance", "left", "available", "balance", "credits", "current"])
	if (Number.isFinite(total) && total > 0) {
		const w = { name: "Credits", used: 0, limit: total }
		if (Number.isFinite(used)) {
			w.used = (100 * used) / total
			w.amount = used
			w.display = `${compactNumber(used)} / ${compactNumber(total)}`
		} else if (Number.isFinite(remaining) && remaining <= total) {
			w.used = (100 * (total - remaining)) / total
			w.amount = total - remaining
			w.display = `${compactNumber(total - remaining)} / ${compactNumber(total)}`
		} else return {}
		const at = timeOf(v, ["resetAt", "resetTime", "nextResetAt", "renewsAt", "expiresAt"])
		if (at) w.resetsAt = at
		return { window: w }
	}
	if (Number.isFinite(remaining) && remaining > 0) return { balance: `${compactNumber(remaining)} credits` }
	if (Number.isFinite(used) && used > 0) return { window: { name: "Credits", used: 0, aside: true, display: `${compactNumber(used)} credits used` } }
	return {}
}

// LIMITS are ClinePass's three limits as /users/me/plan/usage-limits names
// them (app.cline.bot's subscription page reads the same): a rolling 5
// hours, the calendar week and the calendar month
const LIMITS = {
	five_hour: { name: "5 hours", span: 5 * 60 * 60 },
	weekly: { name: "Weekly", span: 7 * 24 * 60 * 60 },
	monthly: { name: "Month", span: 30 * 24 * 60 * 60 },
}

// limitWindows are the plan's limits, {limits: [{type, percentUsed,
// resetsAt}]}, in LIMITS' order; a type it doesn't know is left out, as the
// dashboard leaves it
function limitWindows(v) {
	const out = []
	for (const l of Array.isArray(v?.limits) ? v.limits : []) {
		const k = LIMITS[l?.type]
		if (!k) continue
		const n = Number(l.percentUsed ?? 0)
		const w = { name: k.name, used: Math.min(100, Math.max(0, Number.isFinite(n) ? n : 0)), span: k.span }
		const at = timeOf(l, ["resetsAt"])
		if (at) w.resetsAt = at
		out.push([Object.keys(LIMITS).indexOf(l.type), w])
	}
	return out.sort((a, b) => a[0] - b[0]).map(([, w]) => w)
}

function usageOf(me, balance, limits) {
	const out = { windows: limitWindows(limits) }
	const plan = firstOf(me?.plan, me?.planType, me?.planName, me?.membership, me?.tier)
	if (plan) out.plan = plan
	// the credits as magpie says them: a window when Cline tells a whole to
	// be a share of, else the card's balance (magpie's built-in Cline, which
	// reads the same reply, says the same)
	const dollars = balanceUSD(balance)
	const credits = dollars ? {} : creditsOf(balance)
	if (credits.window) out.windows.push(credits.window)
	const left = dollars ?? credits.balance
	if (left) out.balance = left
	return out
}

// ---- the plugin --------------------------------------------------------------------

// MAKERS maps a model name to its maker's own upstream on the router Cline
// sends it through. Most go through Vercel's AI Gateway, pinned by
// providerOptions.gateway.only with Vercel's provider slug
// (ai-gateway.vercel.sh/v1/models/<model>/endpoints lists "deepseek" for
// deepseek/deepseek-v4-pro and "zai" for zai/glm-5.3). Some go through
// OpenRouter directly, which ignores providerOptions and reads only a
// top-level provider.only with OpenRouter's own slug
// (openrouter.ai/api/v1/providers: Z.AI is "z-ai"): sdytman (#feedback,
// 10-20) found glm-5.3-flash among them, while deepseek-v4.1-flash and
// deepseek-v4.1-flash-1017 go through Vercel. An exact entry is a model
// name; the others match a name's start, the first match winning. magpie's
// built-in ClinePass has the same table (internal/provider/cline.go).
const MAKERS = [
	{ name: "glm-5.3-flash", exact: true, upstream: "z-ai", openRouter: true },
	{ name: "deepseek", upstream: "deepseek" },
	{ name: "glm", upstream: "zai" },
]

// pinned is where a DeepSeek or GLM model of Cline's is pinned when the
// pinUpstream option is on (magpie's built-in ClinePass has the same tick):
// { upstream, openRouter }, DeepSeek's own API for DeepSeek's, Z.ai's for
// GLM, openRouter true for a model Cline sends through OpenRouter. Cline's
// routers otherwise pick among the providers serving the model, and some
// answer worse than the maker's own API, which also keeps the prompt cache.
// A cline-free/ model is left to Cline, as is every other maker's model:
// null.
function pinned(on, model) {
	if (!on || typeof model !== "string" || model.startsWith("cline-free/")) return null
	const name = model.slice(model.lastIndexOf("/") + 1).toLowerCase()
	const m = MAKERS.find((m) => (m.exact ? name === m.name : name.startsWith(m.name)))
	return m ? { upstream: m.upstream, openRouter: !!m.openRouter } : null
}

// PASS_DEEPSEEK is a ClinePass DeepSeek model: cline-pass/deepseek-…
const PASS_DEEPSEEK = /^cline-pass\/deepseek/i

// passPin is where a ClinePass DeepSeek request is pinned by default:
// DeepSeek's own API, with the gateway's capability filter beside it
// (hy125245 on X, 10-23). On ClinePass the server's own routing for the
// model wins over gateway.only alone, and a fake upstream in only was
// served all the same; with has beside it, the request went to DeepSeek
// (finalProvider "deepseek") and a fake upstream was refused with "no
// matching upstream". has names what the upstream must do: cache a
// repeated prefix by itself ("implicit-caching") and, for a model that
// takes images, take them ("vision") — never for a text-only model
// (deepseek-v4-pro), whose upstreams it would all rule out.
const passPin = (images) => ({ gateway: { only: ["deepseek"], has: [...(images ? ["vision"] : []), "implicit-caching"] } })

// pinOf is the field a request for model is pinned by, or null: a
// ClinePass DeepSeek model always (unless pinUpstream is set off), with
// fallback true when it is magpie's default rather than the user's
// pinUpstream, so a request the pin can't be served under goes again as
// sent; and, with pinUpstream on, the makers' table above.
function pinOf(on, model, images, off = false) {
	if (typeof model !== "string") return null
	if (PASS_DEEPSEEK.test(model) && !off) return { key: "providerOptions", value: passPin(images), fallback: !on }
	const pin = pinned(on, model)
	if (!pin) return null
	return pin.openRouter
		? { key: "provider", value: { only: [pin.upstream] }, fallback: false }
		: { key: "providerOptions", value: { gateway: { only: [pin.upstream] } }, fallback: false }
}

// withField is body with "key":value written in at the end, so the rest of
// the body — the prefix a cache matches — is the client's byte for byte
function withField(body, key, value) {
	const end = body.lastIndexOf("}")
	if (end < 0) return body
	const head = body.slice(0, end)
	const sep = /[{,]\s*$/.test(head) ? "" : ","
	return head + sep + `${JSON.stringify(key)}:${JSON.stringify(value)}` + body.slice(end)
}

// ownsRouting is a body that names its own provider or providerOptions —
// the client's, or the user's extra body in magpie: theirs to keep, and
// nothing is added beside it (magpie's default never overrides a value
// the user set)
const ownsRouting = (chat) => chat?.providerOptions !== undefined || chat?.provider !== undefined

// pinBody is the request's body with its pin written in (pinOf), or the
// body as sent
function pinBody(body, chat, on, images = false, off = false) {
	const pin = ownsRouting(chat) ? null : pinOf(on, chat?.model, images, off)
	return pin ? withField(body, pin.key, pin.value) : body
}

// hasImages is a chat body carrying an image part (OpenAI's image_url, or
// a bare image part)
const hasImages = (body) => /"type"\s*:\s*"(?:image_url|image|input_image)"/.test(body)

// NO_UPSTREAM is the gateway saying no upstream meets the pin
const NO_UPSTREAM = /no (?:matching|available|eligible) (?:upstream|provider|endpoint)s?|no upstream/i

// unpinWorthy is a pinned request's failure that the same request unpinned
// may well not meet: no upstream matching the pin, or the pinned upstream
// failing (a 5xx). An account's refusal (401, 402, 403, 429) is the
// account's, whatever the pin.
const unpinWorthy = (status, text) => status >= 500 || NO_UPSTREAM.test(String(text ?? ""))

// isOn is whether an option is set: true, or "true" in a config written by hand
const isOn = (v) => v === true || v === "true"
// isOff is an option set off in so many words: false, or "false"
const isOff = (v) => v === false || v === "false"

export const ClinePlugin = async ({ client } = {}, options = {}) => {
	// the accounts fresh renewed this run: the sign-in mark comes off on the
	// renewed token, whatever the request then met
	const renewals = new WeakSet()
	const renewed = (cred) => renewals.has(cred)
	// the rotated pair, kept in memory beside what magpie saved: a save through
	// client.auth.set may fail, and the next request must not spend a refresh
	// token that was already spent (kiro's held). Every token this run has spent
	// is kept, not only the last one — a store that failed once usually goes on
	// failing, so getAuth keeps handing back the first of them — and each token
	// is kept against the pair its own rotation chain reached, since one plugin
	// instance serves every account of the provider at once: one newest pair
	// shared by all of them would send an account on another account's token
	const held = new Map() // a spent refresh token → its chain's newest pair
	// whether a model takes images, as the bundled list and then Cline's
	// catalog say (the models hook): a ClinePass DeepSeek pin asks for a
	// vision upstream only for a model that has one
	const images = new Map(DEFAULT_MODELS.map((m) => [m.id, !!m.input?.image]))

	// remember saves a refreshed pair where magpie keeps it
	const remember = async (next) => {
		try {
			await client?.auth?.set?.({
				path: { id: PROVIDER },
				body: {
					type: "oauth",
					access: next.access,
					refresh: next.refresh,
					expires: next.expires,
					uid: next.uid ?? "",
					email: next.email ?? "",
					name: next.name ?? "",
					accountId: next.accountId ?? "",
				},
			})
		} catch {}
	}

	// refreshes are serialized: the refresh token is single-use, and two
	// requests renewing at once spend one and lose the other
	let lock = Promise.resolve()
	const locked = (fn) => {
		const run = lock.then(fn, fn)
		lock = run.catch(() => {})
		return run
	}

	// spend trades r's refresh token for a new pair and holds it, whatever a
	// save then does, against the token it spent and the one getAuth handed
	// back (a's): the next request offering either of them finds this chain's
	// newest. Called under the lock only.
	const spend = async (a, r) => {
		const p = await refresh(r)
		const next = { ...r, access: p.access, refresh: p.refresh, expires: p.expires, uid: p.uid || r.uid, email: p.email || r.email, name: p.name || r.name, accountId: r.accountId || p.accountId }
		const chain = held.get(a.refresh) ?? {}
		Object.assign(chain, { access: next.access, refresh: next.refresh, expires: next.expires })
		held.set(a.refresh, chain)
		held.set(r.refresh, chain)
		return next
	}

	// fresh is the account with a token still good: one near its end is
	// renewed, and the rotated pair saved back. A transient refresh failure
	// keeps the token that is still good, as Cline's own client does; only a
	// token that has actually expired is the sign-in gone.
	const fresh = (getAuth) =>
		locked(async () => {
			const a = authOf(await getAuth())
			if (a.type === "api") {
				if (!a.key) throw new Lapsed("Cline: no API key; add one to the account")
				return { bearer: a.key, renewed: false, accountId: a.accountId }
			}
			if (!a.access && !a.refresh) throw new Lapsed("Cline: not signed in")
			if (a.access && a.expires - Date.now() > REFRESH_LEAD) return { ...a, bearer: bearerOf(a), renewed: false }
			// a pair this run already rotated is newer than the one getAuth
			// still hands back: use it rather than re-spending a spent token
			const h = held.get(a.refresh)
			const r = h ? { ...a, access: h.access, refresh: h.refresh, expires: h.expires } : a
			if (r.access && r.expires - Date.now() > REFRESH_LEAD) return { ...r, bearer: bearerOf(r), renewed: false }
			if (!r.refresh) throw new Lapsed("Cline's access token has expired and there is no refresh token; sign in to Cline again")
			let next
			try {
				next = await spend(a, r)
			} catch (e) {
				// a token that is still good rides on through a refresh that
				// failed for a while; an expired one is the sign-in gone
				if (!e?.expired && r.access && r.expires - Date.now() > RETRYABLE_GRACE) return { ...r, bearer: bearerOf(r), renewed: false }
				throw e
			}
			await remember(next)
			const cred = { ...next, bearer: bearerOf(next), renewed: true }
			renewals.add(cred)
			return cred
		})

	const signedInError = (e) =>
		errorResponse({
			status: e?.expired ? 401 : 502,
			message: String(e?.message ?? e).replace(/^Cline: /, ""),
			signIn: e?.expired ? "expired" : undefined,
		})

	// usage is the account's credit balance and its ClinePass limits,
	// magpie's own hook. A read the account's token couldn't make is the
	// sign-in, not the read. The limits are asked alongside: a 404 there is
	// an account without ClinePass, which has its credits alone (AxonHub's
	// Cline checker reads it the same way); any other failure says so on the
	// card rather than leaving the limits off unsaid (#79). Whichever of the
	// two reads succeeds is shown when the other fails: windows read are
	// kept when the balance can't be, as magpie's built-in key card keeps
	// them.
	const usage = async (getAuth) => {
		try {
			const cred = await fresh(getAuth)
			const authz = { Authorization: `Bearer ${cred.bearer}` }
			const limits = clineApi(`${API}/users/me/plan/usage-limits`, { headers: authz, lapsed: () => false }).then(
				(v) => ({ v }),
				(e) => (e?.status === 404 ? { v: null } : { e }),
			)
			let me = null
			let lastErr = null
			try {
				me = await clineApi(`${API}/users/me`, { headers: authz })
			} catch (e) {
				if (e?.expired) throw e
				lastErr = e
			}
			const ids = [me?.clineUserId, me?.subject, me?.id, cred.uid].filter((v) => typeof v === "string" && v.trim())
			let balance = null
			for (const id of ids) {
				try {
					balance = await clineApi(`${API}/users/${encodeURIComponent(id)}/balance`, { headers: authz })
					break
				} catch (e) {
					lastErr = e
				}
			}
			const lim = await limits
			const out = usageOf(me, balance, lim.v)
			if (!balance && !out.windows.length) throw lastErr ?? lim.e ?? new Error("Cline usage: no user id")
			if (lim.e) {
				// the words magpie's card knows (quotaError), the reason after
				const why = String(lim.e?.message ?? lim.e).replace(/^Cline(?: request failed)?:?\s*/, "")
				out.error = "ClinePass limits couldn't be read" + (why.startsWith("(") ? " " : ": ") + why
			}
			return { ...out, user: firstOf(cred.accountId, cred.email, cred.uid, ids[0]), signIn: renewed(cred) ? "renewed" : "kept" }
		} catch (e) {
			return { windows: [], error: e?.message ?? String(e), signIn: e?.expired ? "expired" : "kept" }
		}
	}

	return {
		async "chat.headers"(input, output) {
			if (input?.model?.providerID !== PROVIDER && input?.provider?.info?.id !== PROVIDER) return
			const session = sessionOf(input)
			if (session) output.headers[SESSION] = session
		},
		config: async (cfg) => {
			cfg.provider ??= {}
			const was = cfg.provider[PROVIDER] ?? {}
			cfg.provider[PROVIDER] = {
				name: "Cline",
				npm: "@ai-sdk/openai-compatible",
				api: API,
				...was,
				models: { ...Object.fromEntries(DEFAULT_MODELS.map((m) => [m.id, configModel(m)])), ...(was.models ?? {}) },
			}
		},

		auth: {
			provider: PROVIDER,

			// magpie renews the sign-in RENEW_LEAD before its end, once for the
			// account, before its requests, models and usage ask for it; the
			// check in fresh before each request stays for OpenCode, which
			// doesn't call this. It takes the same lock and the same held pairs
			// as fresh, so the two never spend one refresh token twice. magpie
			// saves what this gives (it is merged over the sign-in): only what
			// changed comes back, and remember isn't called here.
			refreshLead: RENEW_LEAD,
			refresh: (auth) =>
				locked(async () => {
					const a = authOf(auth)
					// an API key, or a sign-in with nothing to renew with
					if (auth?.type !== "oauth" || !a.refresh) return undefined
					// renewed here already (a request's refresh), the store not
					// yet saying so: that pair, not another spend
					const h = held.get(a.refresh)
					const r = h ? { ...a, access: h.access, refresh: h.refresh, expires: h.expires } : a
					if (h && r.access && r.expires - Date.now() > RENEW_LEAD) return { access: r.access, refresh: r.refresh, expires: r.expires }
					let next
					try {
						next = await spend(a, r)
					} catch (e) {
						// a refused refresh is the sign-in gone; anything else is
						// retried by magpie, the token in hand riding on meanwhile
						if (e?.expired) throw Object.assign(new Error(String(e.message ?? e).replace(/^Cline: /, "")), { signIn: "expired" })
						throw e
					}
					const out = { access: next.access, refresh: next.refresh, expires: next.expires }
					for (const k of ["uid", "email", "name", "accountId"]) if (next[k] && next[k] !== a[k]) out[k] = next[k]
					return out
				}),

			async loader(getAuth, provider) {
				const a = await getAuth()
				// pinUpstream: the plugin's own option, or the provider's in
				// OpenCode's config (provider.cline.options.pinUpstream)
				const pin = isOn(options?.pinUpstream) || isOn(provider?.options?.pinUpstream)
				// set off in so many words, nothing is pinned, ClinePass's
				// DeepSeek models included
				const unpinned = isOff(options?.pinUpstream) || isOff(provider?.options?.pinUpstream)
				if (a?.type !== "oauth" && a?.type !== "api") return {}
				return {
					baseURL: API,
					apiKey: "cline",
					// every chat completion as Cline's gateway takes it: OpenAI's
					// format, the account's token, Cline's own headers
					async fetch(input, init = {}) {
						const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
						if (!/\/chat\/completions$/.test(new URL(url).pathname))
							return errorResponse({ status: 404, message: "only chat completions are served" })
						let body
						// sent is the body as the client sent it; fallback is a
						// default pin's way back to it
						let sent
						let fallback = false
						try {
							body = sent = await bodyText(input, init)
							const chat = JSON.parse(body)
							if (!chat || typeof chat !== "object" || Array.isArray(chat)) throw new Error("not a chat completion")
							const p = ownsRouting(chat) ? null : pinOf(pin, chat.model, images.get(chat.model) || hasImages(body), unpinned)
							if (p) {
								body = withField(body, p.key, p.value)
								fallback = p.fallback
							}
						} catch {
							return errorResponse({ status: 400, message: "a request that isn't a chat completion" })
						}
						let cred
						try {
							cred = await fresh(getAuth)
						} catch (e) {
							return signedInError(e)
						}
						const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined))
						const session = headers.get(SESSION)
						headers.delete(SESSION)
						headers.delete("X-Task-ID")
						headers.set("Authorization", `Bearer ${cred.bearer}`)
						for (const [k, v] of Object.entries(clientHeaders(session ? taskIdOf(session) : undefined))) headers.set(k, v)
						headers.delete("content-length")
						headers.delete("host")
						const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined)
						let res
						try {
							res = await fetch(url, { ...init, method: init?.method ?? "POST", headers, body, signal })
						} catch (e) {
							return signed(errorResponse({ status: 502, message: String(e?.message ?? e) }), renewed(cred))
						}
						// a default pin never fails a request that would have
						// worked: no upstream matching it, or the pinned one
						// down, and the request goes once more as sent
						if (!res.ok && fallback) {
							const text = (await res.text()).slice(0, 1 << 20)
							if (!unpinWorthy(res.status, text)) return signed(errorResponse(failure(res.status, text)), renewed(cred))
							try {
								res = await fetch(url, { ...init, method: init?.method ?? "POST", headers, body: sent, signal })
							} catch (e) {
								return signed(errorResponse({ status: 502, message: String(e?.message ?? e) }), renewed(cred))
							}
						}
						// a renewed token cleared the lapse mark whatever the
						// request then met, a refused one included (qoder's
						// renewed()): the failure is reported, the mark stays off
						if (!res.ok) return signed(errorResponse(failure(res.status, (await res.text()).slice(0, 1 << 20))), renewed(cred))
						return signed(await unwrapped(res), renewed(cred))
					},
				}
			},

			usage,

			methods: [
				{ type: "oauth", label: "Sign in to Cline (browser)", authorize: deviceSignIn },
				{ type: "api", label: "Cline API key (app.cline.bot → Settings → API Keys)" },
			],
		},

		provider: {
			id: PROVIDER,
			async models(provider) {
				const have = provider?.models ?? {}
				try {
					const ms = await fetchFeed()
				for (const m of ms) if (m.input) images.set(m.id, !!m.input.image)
					return Object.fromEntries(ms.map((m) => [m.id, runtimeModel(m)]))
				} catch {
					// a list Cline couldn't give is a failure, not the few
					// configured: magpie keeps the list it had
					return have
				}
			},
		},
	}
}

// for tests
export const _internal = {
	firstOf,
	compactNumber,
	toMs,
	timeOf,
	authOf,
	bearerOf,
	parseAuth,
	parseFeed,
	prettify,
	fetchFeed,
	fetchRecommended,
	fetchCloudModels,
	inputsFor,
	creditsOf,
	balanceUSD,
	usd,
	usageOf,
	limitWindows,
	failure,
	errorResponse,
	bodyText,
	pinned,
	pinOf,
	pinBody,
	unpinWorthy,
	hasImages,
	unwrapped,
	clientHeaders,
	deviceAuthorize,
	pollDevice,
	refresh,
	constants: { PROVIDER, API, WORKOS_CLIENT, WORKOS_PREFIX, CLIENT, DEFAULT_MODELS },
	errors: { Lapsed },
}

// The shape the plugin host looks for: default {id, server}. The host takes
// this over the named exports, so also exporting ClinePlugin by name (as the
// tests import it) changes nothing the host does.
export default { id: PROVIDER, server: ClinePlugin }
