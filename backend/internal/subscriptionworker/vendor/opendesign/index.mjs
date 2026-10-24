// Open Design (open-design.ai) Cloud, as its own CLI signs in to it: the
// sign-in is a device authorization at the Vela control plane
// (amr-api.open-design.ai), which answers with two keys — a control key
// (vela_ctrl_…) for the account's plan and its Design Plan allowance, and a
// runtime key (sk-amr-…) that the model plane (amr-link.open-design.ai)
// takes. Model requests are the OpenAI Responses API at the runtime key;
// the model list, the plan and the 5-hour and 7-day allowance windows all
// come from the control plane.
//
// Not to be confused with the OpenDesign desktop app on this machine: it
// keeps its own sign-in in ~/.amr/config.json, a different (machine-issued)
// pair of keys, and this plugin never touches it.

import { hostname } from "node:os"

// JSON modules have a default export only, in Bun as in Node
import pkg from "./package.json" with { type: "json" }

import { MODELS } from "./models.mjs"

const ID = "opendesign"
const CONTROL = "https://amr-api.open-design.ai"
const LINK = "https://amr-link.open-design.ai/v1"
// WHICH API each model is asked on: magpie reads the protocol off the AI SDK
// package a model declares (its api.npm) — "@ai-sdk/openai" is the Responses
// API, "@ai-sdk/anthropic" Anthropic's messages, the compatible package chat
// completions. amr-link answers thinking on a different one per model
// (sryimnoob123's measurements, plugins#141: a streaming request to each model
// on each of the three): chat completions carries the thinking of ten of the
// eleven; deepseek-v4-pro and claude-haiku-5.5 think longer on Responses, and
// glm-5.3-flash only thinks on Anthropic's messages among the two others.
const WIRE = {
  chat: "@ai-sdk/openai-compatible",
  responses: "@ai-sdk/openai",
  anthropic: "@ai-sdk/anthropic",
}
const MODEL_WIRE = {
  "claude-haiku-5.5": "responses",
  "deepseek-v4-pro": "responses",
  "glm-5.3-flash": "anthropic",
}
const wireOf = (id) => WIRE[MODEL_WIRE[id]] ?? WIRE.chat
// the provider's own default: a model this plugin doesn't name is asked on
// chat completions, where ten of the eleven thought
const NPM = WIRE.chat
const SOURCE = "opencode-opendesign-auth/" + pkg.version // as the manifest has it
const CLI_VERSION = "0.1.4" // what the sign-in says it is, as the CLI does
const TIMEOUT = 20_000
const HOUR = 3600
const WEEK = 7 * 24 * HOUR

// The vendor's spending words as magpie's rate, and its quality words as
// whether the model thinks before it answers.
const COST = { low: "x0.5", medium: "x1", high: "x2", very_high: "x4" }
const THINKING = { advanced: true, standard: false }

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const text = (v) => (typeof v === "string" && v.trim() ? v.trim() : "")

// The runtime key rides model requests; the control key reads the plan. A
// pasted key is one or the other, so each reader takes what fits it.
const runtimeKeyOf = (a) => {
  const access = text(a?.access) || text(a?.key)
  return access.startsWith("vela_ctrl_") ? "" : access
}
const controlKeyOf = (a) => text(a?.control) || text(a?.metadata?.control) || (text(a?.key).startsWith("vela_ctrl_") ? text(a.key) : "")

// authOf keeps a successful sign-in the way magpie saves an oauth one: the
// two keys, the account they belong to, and refresh "" so one is stored.
// The keys don't expire, so there is nothing to renew.
function authOf(user, keys = {}) {
  const access = text(keys.runtimeKey) || text(user?.runtimeKey)
  const control = text(keys.controlKey) || text(user?.controlKey)
  if (!access || !control) return null
  const email = text(user?.email)
  const uid = text(user?.id) || text(user?.appUserId)
  return {
    type: "success",
    refresh: "",
    expires: 0,
    access,
    control,
    ...(email && { accountId: email, email }),
    ...(uid && { uid }),
  }
}

// ---- HTTP --------------------------------------------------------------------

async function readJSON(res) {
  const raw = await res.text().catch(() => "")
  try {
    return raw ? JSON.parse(raw) : null
  } catch {
    return { __text: raw }
  }
}

// said() is the vendor's own words, never a page of HTML.
function said(body, fallback) {
  const raw = body?.error?.message ?? body?.message ?? body?.error ?? body?.__text
  const s = typeof raw === "string" ? raw.trim() : ""
  if (!s || s.startsWith("<")) return fallback
  return s.length > 300 ? s.slice(0, 300) + "…" : s
}

async function api(base, path, { key, body, method = "GET", signal } = {}) {
  const headers = { accept: "application/json", "user-agent": SOURCE }
  if (key) headers.authorization = "Bearer " + key
  if (body !== undefined) headers["content-type"] = "application/json"
  const res = await fetch(base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ?? AbortSignal.timeout(TIMEOUT),
  })
  return { status: res.status, ok: res.ok, body: await readJSON(res) }
}

// ---- the sign-in -------------------------------------------------------------

// startDevice opens a device authorization: a code, the page that approves
// it, and the secret the approval is polled with.
// host is this computer's name, as `vela login` sends it (os.Hostname());
// when it can't be read the CLI stops with "resolve hostname", and so does
// this, rather than send a name of its own.
async function startDevice({ signal, host = hostname } = {}) {
  let name = ""
  try { name = String(host() ?? "").trim() } catch {}
  if (!name) throw new Error("Open Design sign-in: this computer's name couldn't be read, and the sign-in sends it")
  const r = await api(CONTROL, "/api/v1/cli/device-authorizations", {
    method: "POST",
    body: { cliVersion: CLI_VERSION, host: name, profile: "prod" },
    signal,
  })
  const d = r.body
  if (!r.ok || !d?.deviceId || !d?.deviceSecret) {
    throw new Error(`Open Design sign-in: the control plane answered ${r.status} (${said(d, "no device authorization")})`)
  }
  return {
    deviceId: String(d.deviceId),
    deviceSecret: String(d.deviceSecret),
    userCode: text(d.userCode),
    url: text(d.activationUrl),
    interval: Number(d.pollIntervalSeconds) > 0 ? Number(d.pollIntervalSeconds) : 2,
    until: Date.parse(d.expiresAt ?? "") || Date.now() + 10 * 60_000,
  }
}

// waitForDevice polls until the page approves, giving the account's two
// keys; a code that runs out or is refused says so.
async function waitForDevice(d, { signal, sleep: nap = sleep } = {}) {
  for (;;) {
    if (signal?.aborted) throw new Error("Open Design sign-in: cancelled")
    if (Date.now() > d.until) throw new Error("Open Design sign-in: the code ran out before it was approved")
    await nap((d.interval ?? 2) * 1000)
    let r
    try {
      r = await api(CONTROL, `/api/v1/cli/device-authorizations/${encodeURIComponent(d.deviceId)}/token`, {
        method: "POST",
        body: { deviceSecret: d.deviceSecret },
        signal,
      })
    } catch {
      // a hiccup on the way to the approval is not the sign-in failing
      if (signal?.aborted) throw new Error("Open Design sign-in: cancelled")
      continue
    }
    const auth = authOf(r.body?.user, { controlKey: r.body?.controlKey, runtimeKey: r.body?.runtimeKey })
    if (auth) return auth
    const status = String(r.body?.status ?? "").toLowerCase()
    if (r.status === 404 || ["expired", "canceled", "cancelled", "denied", "rejected"].includes(status)) {
      throw new Error(`Open Design sign-in: the request was ${status || "not found"} — start again`)
    }
    if (r.status === 401) {
      const why = text(r.body?.error)
      throw new Error(`Open Design sign-in: ${why === "invalid_device_secret" ? "the code is no longer valid" : said(r.body, "the approval is no longer valid")} — start again`)
    }
    // pending, or an answer this plugin doesn't know: wait for the next poll
  }
}

// ---- shaping the readings ----------------------------------------------------

// The workspace whose allowance is shown; a personal account has one.
async function workspaceOf(controlKey, { signal } = {}) {
  const r = await api(CONTROL, "/api/v1/workspaces/current", { key: controlKey, signal })
  if (!r.ok) throw new Error(`the workspaces read answered ${r.status} (${said(r.body, "unauthorized")})`)
  const id = text(r.body?.activeWorkspaceId) || text(r.body?.workspaceId)
  if (!id) throw new Error("the account has no active workspace")
  return { id, tier: text(r.body?.personalMembershipTier) }
}

// credits() shows the plan's credits short: whole below five figures, then
// thousands, then millions, as a plan's own card rounds them.
function credits(v) {
  const n = Number(v)
  if (!Number.isFinite(n)) return text(v)
  const whole = Math.round(n)
  const a = Math.abs(whole)
  if (a < 10_000) return String(whole)
  if (a < 1_000_000) {
    const k = whole / 1000
    return (Math.abs(k) >= 100 ? Math.round(k) : Math.round(k * 10) / 10) + "k"
  }
  return Math.round((whole / 1_000_000) * 10) / 10 + "M"
}

function usedPercent(used, limit) {
  const u = Number(used)
  const l = Number(limit)
  if (!Number.isFinite(u) || !Number.isFinite(l) || l <= 0) return null
  return Math.round(Math.max(0, (u / l) * 100) * 10) / 10
}

// The window's name as the vendor's card says it: hours under a day, days
// above, the week's as "7 days".
function windowName(seconds) {
  const s = Number(seconds)
  if (!Number.isFinite(s) || s <= 0) return "Allowance"
  const says = (n, one, many) => `${n} ${n === 1 ? one : many}`
  if (s % WEEK === 0) return says((s / WEEK) * 7, "day", "days")
  if (s % (24 * HOUR) === 0) return says(s / (24 * HOUR), "day", "days")
  if (s % HOUR === 0) return says(s / HOUR, "hour", "hours")
  return says(Math.round(s / 60), "minute", "minutes")
}

const money = (v) => {
  const n = Number(v)
  return Number.isFinite(n) ? `$${n.toFixed(2)}` : ""
}

const when = (v) => {
  const t = typeof v === "number" ? (v < 1e11 ? v * 1000 : v) : Date.parse(text(v))
  return Number.isFinite(t) ? new Date(t).toISOString() : undefined
}

// allowances() turns the Design Plan's windows into magpie's, shortest
// first, as the vendor's card lists them.
function allowances(plan) {
  const out = []
  for (const w of plan?.windows ?? []) {
    const used = usedPercent(w?.usedCredits, w?.limitCredits)
    if (used === null) continue
    out.push({
      name: windowName(w?.durationSeconds),
      used,
      span: Math.max(0, Number(w?.durationSeconds) || 0),
      resetsAt: when(w?.resetsAt),
      display: `${credits(w?.usedCredits)} / ${credits(w?.limitCredits)} credits`,
    })
  }
  return out.sort((a, b) => a.span - b.span)
}

// thePlan names the subscription the way the dashboard does.
function thePlan(summary, tier) {
  const t = text(summary?.membershipTier) || text(tier)
  if (!t) return "Open Design"
  return "Design Plan " + t[0].toUpperCase() + t.slice(1)
}

// levelsOf is a model's reasoning levels: the ones models.mjs records as
// measured against amr-link (#137), where the vendor's capability word
// misleads both ways — "standard" models took reasoning_effort and thought
// at it, and GPT-6 Luna took it and never thought. A model the seed doesn't
// know has none until it is measured too.
function levelsOf(m) {
  return Array.isArray(m?.efforts) ? m.efforts.filter((e) => typeof e === "string" && e) : []
}

// modelOf turns one model's facts — models.mjs's, or amr-link's catalog
// read again — into the model magpie shows, as the provider's config has
// it. A model reasons when it has levels, or else when the vendor calls it
// "advanced". variants are its levels as @ai-sdk/openai options: magpie
// shows a model's levels only from its variants, and only when it reasons.
function modelOf(id, m) {
  const rate = COST[text(m?.cost)]
  const levels = levelsOf(m)
  const thinking = levels.length > 0 || (THINKING[text(m?.capability)] ?? !!m?.reasoning)
  const context = Number(m?.context)
  const output = Number(m?.output)
  const input = Array.isArray(m?.input) ? m.input.filter((x) => typeof x === "string" && x !== "text") : []
  return {
    id,
    name: text(m?.name) || id,
    attachment: true,
    tool_call: true,
    reasoning: thinking,
    ...(levels.length && { variants: Object.fromEntries(levels.map((e) => [e, { reasoningEffort: e }])) }),
    // the API magpie asks this model on (WIRE above), per model
    api: { npm: wireOf(id) },
    provider: { npm: wireOf(id) },
    ...(rate && { rate }),
    limit: {
      context: Number.isFinite(context) && context > 0 ? context : 200_000,
      output: Number.isFinite(output) && output > 0 ? output : 32_000,
    },
    modalities: { input: ["text", ...input], output: ["text"] },
  }
}

// KINDS are the kinds of input and output OpenCode's capabilities name.
const KINDS = ["text", "image", "audio", "video", "pdf"]
const kinds = (list) => Object.fromEntries(KINDS.map((k) => [k, list.includes(k)]))

// servedOf is modelOf's model as provider.models hands it back: OpenCode's
// model, not the config's. magpie reads a listed model's thinking from
// capabilities.reasoning and its kinds from capabilities.input, so a config
// shape there (reasoning: true beside no capabilities) listed every model a
// signed-in account has as one that doesn't reason, with no levels (#137).
function servedOf(id, m) {
  const c = modelOf(id, m)
  return {
    id,
    name: c.name,
    api: { id, url: LINK, npm: wireOf(id) },
    status: "active",
    headers: {},
    options: {},
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: c.limit,
    capabilities: {
      temperature: true,
      reasoning: c.reasoning,
      attachment: c.attachment,
      toolcall: c.tool_call,
      input: kinds(c.modalities.input),
      output: kinds(c.modalities.output),
      interleaved: false,
    },
    release_date: "",
    variants: c.variants ?? {},
    ...(c.rate && { rate: c.rate }),
  }
}

// facts() is amr-link's catalog entry in the seed's own shape.
function facts(m) {
  const meta = m?.metadata ?? {}
  const input = Array.isArray(m?.architecture?.input_modalities)
    ? m.architecture.input_modalities.filter((x) => typeof x === "string" && x !== "text")
    : []
  return {
    context: Number(meta.context_limit),
    output: Number(meta.context_budget),
    input,
    cost: text(meta.cost),
    capability: text(meta.capability),
  }
}

// seed() is what the provider has before anyone signs in, from models.mjs.
function seed() {
  return Object.fromEntries(Object.entries(MODELS).map(([id, m]) => [id, modelOf(id, m)]))
}

// modelsOf() shapes amr-link's live catalog as magpie's models: the list an
// account's plan entitles it to right now, as provider.models gives them. A
// model the seed knows keeps its name and its measured levels; the catalog
// carries ids only.
function modelsOf(catalog) {
  const out = {}
  for (const m of catalog?.data ?? []) {
    const id = text(m?.id)
    if (!id) continue
    out[id] = servedOf(id, { ...facts(m), name: MODELS[id]?.name ?? id, efforts: MODELS[id]?.efforts })
  }
  return out
}

// ---- the plugin --------------------------------------------------------------

export const OpenDesignAuthPlugin = async () => {
  // the account's workspace, one read for a while
  const workspaces = new Map()
  const workspace = async (controlKey) => {
    const had = workspaces.get(controlKey)
    if (had && Date.now() - had.at < 10 * 60_000) return had.id
    const { id } = await workspaceOf(controlKey)
    workspaces.set(controlKey, { id, at: Date.now() })
    return id
  }

  // usage is magpie's own hook: the plan, the wallet, and the Design Plan's
  // 5-hour and 7-day allowance, as the dashboard shows them. The control
  // plane doesn't take an API key, so an API key sign-in has no allowance
  // to read; an account sign-in does. Each reading keeps the sign-in
  // (signIn "kept"), as the account's keys neither lapse nor renew.
  const usage = async (getAuth) => {
    const a = await getAuth().catch(() => null)
    const controlKey = controlKeyOf(a)
    if (!controlKey) {
      return { windows: [], error: "Signed in with an API key: the Design Plan allowance needs the account sign-in.", signIn: "kept" }
    }
    const signal = AbortSignal.timeout(30_000)
    try {
      const id = await workspace(controlKey)
      const preflight = await api(CONTROL, `/api/v1/workspaces/${encodeURIComponent(id)}/billing/preflight`, { key: controlKey, signal })
      if (preflight.status === 401 || preflight.status === 403) {
        return { windows: [], error: "Open Design refused the account's sign-in — sign in again.", signIn: "expired" }
      }
      if (!preflight.ok) throw new Error(`the Design Plan read answered ${preflight.status} (${said(preflight.body, "no reading")})`)
      // the summary takes a timeZone, but nothing read here depends on it
      // (only its own updatedAt and usageCount do), so none is sent
      const summary = await api(CONTROL, "/api/v1/billing/summary", { key: controlKey, signal })
      const plan = preflight.body?.codingPlan ?? null
      const out = {
        plan: thePlan(summary.ok ? summary.body : null, ""),
        user: text(a?.email) || text(a?.accountId),
        balance: money(preflight.body?.balanceUsd ?? (summary.ok ? summary.body?.balanceUsd : "")),
        windows: allowances(plan),
        signIn: "kept",
      }
      if (summary.ok) {
        const until = when(summary.body?.subscriptionCurrentPeriodEnd)
        if (until) out.until = until
        if (text(summary.body?.subscriptionStatus)) out.renew = summary.body?.subscriptionCancelAtPeriodEnd ? "off" : "auto"
      }
      if (plan && plan.eligible === false) {
        out.error = "This account has no Design Plan allowance: its requests draw on the wallet balance."
      }
      return out
    } catch (e) {
      return { windows: [], error: `Open Design usage: ${e?.message ?? e}`, signIn: "kept" }
    }
  }

  return {
    config: async (cfg) => {
      cfg.provider ??= {}
      const was = cfg.provider[ID] ?? {}
      // the wire is this plugin's to declare, never a kept copy's: magpie
      // hands back what the hook last wrote, and a model kept from 0.1.5
      // would otherwise stay on the Responses API it chose for every model
      const models = { ...seed(), ...(was.models ?? {}) }
      for (const [id, m] of Object.entries(models)) {
        models[id] = { ...m, api: { ...(m.api ?? {}), npm: wireOf(id) }, provider: { ...(m.provider ?? {}), npm: wireOf(id) } }
      }
      cfg.provider[ID] = {
        name: "Open Design",
        api: LINK,
        ...was,
        npm: NPM,
        models,
      }
    },
    // the plan decides which models an account gets, so the list is read
    // per account, at that account's runtime key
    provider: {
      id: ID,
      async models(provider, { auth } = {}) {
        const key = runtimeKeyOf(auth)
        if (!key) return provider.models
        const r = await api(LINK, "/models", { key })
        if (r.status === 401 || r.status === 403) {
          throw Object.assign(new Error(`Open Design refused the account's key (${said(r.body, "unauthorized")}) — sign in again`), { signIn: "expired" })
        }
        if (!r.ok) return provider.models // magpie keeps the list it had
        const models = modelsOf(r.body)
        return Object.keys(models).length ? models : provider.models
      },
    },
    auth: {
      provider: ID,
      usage,
      loader: async (getAuth) => {
        const key = runtimeKeyOf(await getAuth().catch(() => null))
        if (!key) return {}
        return { baseURL: LINK, apiKey: key }
      },
      methods: [
        {
          type: "oauth",
          label: "Open Design account",
          async authorize() {
            const d = await startDevice()
            return {
              url: d.url,
              instructions: d.userCode
                ? `Approve the CLI sign-in on the page that opens (code ${d.userCode}). It finishes here by itself.`
                : "Approve the CLI sign-in on the page that opens. It finishes here by itself.",
              method: "auto",
              async callback() {
                try {
                  return await waitForDevice(d)
                } catch (e) {
                  return { type: "failed", error: e?.message ?? String(e) }
                }
              },
            }
          },
        },
        {
          // magpie's own key prompt: the key it saves is taken as it is, so
          // a key that isn't right is the vendor's own answer to say
          type: "api",
          label: "Open Design API key",
          placeholder: "sk-amr-…",
        },
      ],
    },
  }
}

// _internal is what the tests read through; magpie calls every exported
// function as a plugin, so only the plugin itself is exported bare.
export const _internal = { authOf, allowances, controlKeyOf, credits, modelsOf, money, runtimeKeyOf, said, startDevice, thePlan, usedPercent, waitForDevice, windowName, when, workspaceOf }
