// Command Code's plans (commandcode.ai) as an OpenCode provider plugin.
//
// An account is a Command Code API key. It is got the way Command Code's
// CLI gets one (Studio approves a key for this machine and posts it to a
// callback on 127.0.0.1), taken from the CLI's own sign-in
// (~/.commandcode/auth.json, read only), or pasted from Studio's keys page.
// A key doesn't expire: there is nothing to refresh.
//
// Every plan but Go is served on the Provider API: chat completions and
// Responses at /provider/v1, Anthropic's Messages at /provider/v1/messages,
// the key in Authorization and x-api-key. Go's key has no Provider API: it
// is only taken where the CLI itself asks, POST /alpha/generate, in the
// CLI's own format (command-code 1.72.2), which the fetch here writes from
// the chat completion OpenCode sends and turns back into one.
//
// Zero Data Retention (commandcode.ai/docs/resources/zdr) is asked for as
// the CLI asks it: x-cmd-zdr: 1 on every request for a reply, the Provider
// API's and /alpha/generate's alike (command-code's buildCommandAuthHeaders
// on CMD_ZDR=1: 1.84.0's bundle, and 1.74.1 as captured). It is on when the user turns on the provider's "Zero Data
// Retention" switch in magpie, or when CMD_ZDR is "1" in the environment,
// as for the CLI.
import { createServer, STATUS_CODES } from "node:http"
import { randomBytes, randomUUID } from "node:crypto"
import { readFile } from "node:fs/promises"
import { homedir } from "node:os"
import { basename, join } from "node:path"

const ID = "commandcode-plan"
const API = "https://api.commandcode.ai"
const STUDIO = "https://commandcode.ai"
const BASE = API + "/provider/v1"
const CLI_VERSION = "1.72.2"
const MAX_TOKENS = 64000 // the CLI's max_tokens when none is asked
// MAX_OUTPUT is the most max_tokens Command Code takes at /alpha/generate,
// for any model: more is refused, the whole request with it ("Too big:
// expected number to be <=200000 at params.max_tokens") — magpie's
// CommandCodeMaxOutput
const MAX_OUTPUT = 200_000
const ORIGINS = ["https://commandcode.ai", "https://staging.commandcode.ai"]
const SIGN_IN_TIMEOUT = 10 * 60 * 1000

const CHAT = "@ai-sdk/openai-compatible"
const MESSAGES = "@ai-sdk/anthropic"
const RESPONSES = "@ai-sdk/openai" // the Responses API, which magpie speaks to it on

// ---- models -------------------------------------------------------------------

// THINK_OFF are the models the Provider API turns thinking off for at
// reasoning_effort "off" (CHENJIAMIAN, magpie-community/plugins#334, on a
// GOAT key: 0 reasoning tokens on each, where "none" is refused as no
// option and thinking {"type":"disabled"} is ignored). Others, DeepSeek V4
// Flash Fast and Kimi K3 among them, refuse "off" ("does not support
// reasoning_effort \"off\""). Each is offered magpie's off level, none,
// beside the levels magpie gave it before (DeepSeek's: low, high, max),
// and none or "off" asked of it is sent as "off" (providerBody). Go's
// /alpha/generate is not known to take "off": its levels stay the CLI's.
const THINK_OFF = new Set([
  "deepseek/deepseek-v4-pro", "deepseek/deepseek-v4-flash", "deepseek/deepseek-v4-flash-vision-exp",
  "deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash-fast",
])
const OFF_EFFORTS = ["none", "low", "high", "max"]

// The plan's models when its list can't be asked: Claude on Messages, the
// rest on chat completions. Pictures as models.dev has them (DeepSeek V4
// and GLM-5.3 read text alone, so say nothing).
const MODELS = [
  { id: "claude-sonnet-5", name: "Claude Sonnet 5", context: 1_000_000, images: true },
  { id: "claude-opus-5-5", name: "Claude Opus 5.5", context: 1_000_000, images: true },
  { id: "gpt-6-sol", name: "GPT-6 Sol", context: 1_050_000, images: true },
  { id: "deepseek/deepseek-v4-pro", name: "DeepSeek V4 Pro", context: 1_000_000, efforts: OFF_EFFORTS },
  { id: "deepseek/deepseek-v4-flash", name: "DeepSeek V4 Flash", context: 1_000_000, efforts: OFF_EFFORTS },
  { id: "moonshotai/Kimi-K3", name: "Kimi K3", context: 1_000_000, images: true },
  { id: "zai-org/GLM-5.3", name: "GLM-5.3", context: 1_000_000 },
  { id: "MiniMaxAI/MiniMax-M3", name: "MiniMax M3", context: 1_000_000, images: true },
]

// The models the Go plan is let use, as the CLI's own table has it
// (1.73.0): every model its picker shows (less the hidden) but the
// "premium" ones and those it blocks for Go (GO_REFUSED). They stand in
// until Command Code's list is fetched (goModels), or when it can't be,
// and give that list the reasoning levels and pictures it doesn't say —
// magpie's cmdGoModels.
const EFF5 = ["low", "medium", "high", "xhigh", "max"]
// FELL_BACK marks a list handed back for one that can't be had (magpie's
// plugin host reads it; OpenCode never sees a symbol's key)
const FELL_BACK = Symbol.for("magpie.fellBack")

const GO_MODELS = [
  { id: "gpt-6-luna", name: "GPT-6 Luna", context: 1_050_000, images: true, efforts: EFF5 },
  { id: "gpt-5.6-luna", name: "GPT-5.6 Luna", context: 1_050_000, images: true, efforts: EFF5 },
  { id: "deepseek/deepseek-v4-pro", name: "DeepSeek V4 Pro", context: 1_000_000, efforts: ["high", "max"] },
  { id: "deepseek/deepseek-v4-flash", name: "DeepSeek V4 Flash", context: 1_000_000, efforts: ["high", "max"] },
  { id: "deepseek/deepseek-v4-flash-vision-exp", name: "DeepSeek V4 Flash Vision (exp)", context: 1_000_000, images: true, efforts: ["high", "max"] },
  { id: "deepseek/deepseek-v4-flash-fast", name: "DeepSeek V4 Flash Fast", context: 1_000_000, efforts: ["low", "high", "max"] },
  { id: "deepseek/deepseek-v4.1-flash", name: "DeepSeek V4.1 Flash", context: 1_000_000, images: true, efforts: ["low", "high", "max"] },
  { id: "deepseek/deepseek-v4.1-flash-fast", name: "DeepSeek V4.1 Flash Fast", context: 1_000_000, images: true, efforts: ["low", "high", "max"] },
  { id: "moonshotai/Kimi-K3", name: "Kimi K3", context: 1_000_000, images: true, efforts: ["low", "high", "max"] },
  { id: "moonshotai/Kimi-K2.7-Code", name: "Kimi K2.7 Code", context: 256_000, images: true },
  { id: "moonshotai/Kimi-K2.7-Code-Highspeed", name: "Kimi K2.7 Code HighSpeed", context: 262_000, images: true },
  { id: "moonshotai/Kimi-K2.6", name: "Kimi K2.6", context: 256_000, images: true },
  { id: "moonshotai/Kimi-K2.5", name: "Kimi K2.5", context: 256_000, images: true },
  { id: "z-ai/glm-5.3-flash", name: "GLM-5.3 Flash", context: 1_048_576, images: true, efforts: ["low", "high", "max"] },
  { id: "z-ai/glm-5.3-flashx", name: "GLM-5.3 FlashX", context: 1_000_000, images: true, efforts: ["low", "high", "max"] },
  { id: "zai-org/GLM-5.3", name: "GLM-5.3", context: 1_000_000, efforts: ["low", "high", "max"] },
  { id: "zai-org/GLM-5.2", name: "GLM-5.2", context: 1_000_000, efforts: ["high", "max"] },
  { id: "zai-org/GLM-5.2-Fast", name: "GLM-5.2 Fast", context: 1_000_000 },
  { id: "zai-org/GLM-5.1", name: "GLM-5.1", context: 200_000 },
  { id: "zai-org/GLM-5", name: "GLM-5", context: 200_000 },
  { id: "MiniMaxAI/MiniMax-M3", name: "MiniMax M3", context: 1_000_000, images: true, efforts: ["low", "medium", "high"] },
  { id: "MiniMaxAI/MiniMax-M2.7", name: "MiniMax M2.7", context: 200_000 },
  { id: "MiniMaxAI/MiniMax-M2.5", name: "MiniMax M2.5", context: 200_000 },
  { id: "xiaomi/mimo-v2.6-pro", name: "MiMo V2.6 Pro", context: 1_048_576, images: true },
  { id: "xiaomi/mimo-v2.6-flash", name: "MiMo V2.6 Flash", context: 1_048_576, images: true },
  { id: "xiaomi/mimo-v2.5-pro", name: "MiMo V2.5 Pro", context: 1_000_000 },
  { id: "xiaomi/mimo-v2.5", name: "MiMo V2.5", context: 1_000_000, images: true },
  { id: "Qwen/Qwen3.8-Omni-Flash", name: "Qwen 3.8 Omni Flash", context: 1_000_000, images: true, efforts: ["low", "medium", "xhigh"] },
  { id: "Qwen/Qwen3.8-Max-0902", name: "Qwen 3.8 Max 0902", context: 1_000_000, images: true, efforts: ["low", "medium", "xhigh"] },
  { id: "Qwen/Qwen3.8-Max", name: "Qwen 3.8 Max", context: 1_000_000, images: true, efforts: ["low", "medium", "xhigh"] },
  { id: "Qwen/Qwen3.8-27B", name: "Qwen 3.8 27B", context: 262_144, images: true, efforts: ["low", "medium", "xhigh"] },
  { id: "Qwen/Qwen3.8-Flash", name: "Qwen 3.8 Flash", context: 1_000_000, images: true, efforts: ["low", "medium", "xhigh"] },
  { id: "Qwen/Qwen3.7-Max", name: "Qwen 3.7 Max", context: 1_000_000 },
  { id: "Qwen/Qwen3.7-Plus", name: "Qwen 3.7 Plus", context: 1_000_000, images: true },
  { id: "Qwen/Qwen3.7-Flash", name: "Qwen 3.7 Flash", context: 1_000_000, images: true },
  { id: "Qwen/Qwen3.6-Max-Preview", name: "Qwen 3.6 Max Preview", context: 200_000 },
  { id: "Qwen/Qwen3.6-Plus", name: "Qwen 3.6 Plus", context: 200_000, images: true },
  { id: "meituan/LongCat-2.0", name: "LongCat 2.0", context: 1_048_576 },
  { id: "stepfun/Step-5-Preview", name: "Step 5 Preview", context: 1_000_000, images: true, efforts: ["low", "medium", "high"] },
  { id: "stepfun/Step-3.7-Flash", name: "Step 3.7 Flash", context: 256_000, images: true },
  { id: "stepfun/Step-3.5-Flash", name: "Step 3.5 Flash", context: 262_144 },
  { id: "tencent/hy3-paid", name: "Tencent Hy3", context: 262_144 },
  { id: "tencent/hy4-preview", name: "Tencent Hy4 Preview", context: 1_048_576, efforts: ["low", "medium", "high"] },
  { id: "google/gemini-3.6-flash", name: "Gemini 3.6 Flash", context: 1_000_000, images: true, efforts: ["low", "medium", "high"] },
  { id: "google/gemini-3.5-flash-lite", name: "Gemini 3.5 Flash Lite", context: 1_000_000, images: true, efforts: ["low", "medium", "high"] },
  { id: "nvidia/nemotron-3-ultra-550b-a55b", name: "Nemotron 3 Ultra", context: 1_000_000 },
  { id: "thinkingmachines/inkling", name: "Inkling", context: 256_000, images: true },
  { id: "thinkingmachines/inkling-small", name: "Inkling Small", context: 1_000_000, images: true },
  { id: "stealth/space-bunny-alpha", name: "Space Bunny Alpha", context: 1_000_000, images: true, efforts: ["low", "medium", "high"] },
  { id: "stealth/pixel-canary", name: "Pixel Canary", context: 262_144, images: true, efforts: ["low", "medium", "xhigh"] },
  { id: "poolside/laguna-s-2.1-free", name: "Laguna S 2.1", context: 256_000 },
  { id: "inclusionai/ling-3.0-flash-free", name: "Ling 3.0 Flash", context: 256_000 },
  { id: "inclusionai/ling-3.0-flash-sante:free", name: "Ling 3.0 Flash Sante", context: 262_144 },
  { id: "inclusionai/ling-3.1-flash:free", name: "Ling 3.1 Flash", context: 262_144, efforts: ["low", "medium", "high"] },
  { id: "meta/muse-spark-1.2-contributor", name: "Muse Spark 1.2 Contributor", context: 1_048_576, images: true, efforts: ["low", "medium", "high", "xhigh"] },
  { id: "meta/muse-spark-1.3-contributor", name: "Muse Spark 1.3 Contributor", context: 1_048_576, images: true, efforts: ["low", "medium", "high", "xhigh"] },
  { id: "xai/grok-4.5", name: "Grok 4.5", context: 500_000, images: true, efforts: ["low", "medium", "high"] },
]

// The models each plan is let use, as the CLI's own table has it
// (command-code 1.79.2: evaluateModelAccess over its model categories and
// its plans' allowedCategories and blockedModels). Command Code's list is
// the same 87 models for every plan, asked with a key or without, and the
// Provider API turns a model the plan hasn't away only when it is asked
// (403 "MODEL_NOT_IN_PLAN: Claude Opus 5.5 available in Provider and above
// plans or extra on demand usage"). So a plan's list is Command Code's less
// what its plan refuses: a premium model when the plan has the open models
// alone (Go, GOAT), and the models it blocks. A model the table doesn't
// name the CLI lets any plan pick; one the plan hasn't after all is refused
// with MODEL_NOT_IN_PLAN, which the agent is told. Blocks are by the
// model's id: the CLI's own block of "vercel-ai-gateway:xiaomi/mimo-v2.6-
// pro-ultraspeed" misses its "cai" model, and Go is still kept off it.
const PREMIUM = new Set([
  "claude-sonnet-5", "claude-sonnet-4-6", "claude-fable-5-1", "claude-fable-5",
  "claude-opus-5-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7",
  "claude-haiku-4-5-20251001", "gpt-5.6-terra", "gpt-6-astra", "gpt-6.1-sol",
  "gpt-6-sol", "gpt-5.5", "gpt-5.4", "gpt-5.3-codex", "gpt-5.4-mini",
  "google/gemini-3.5-flash", "google/gemini-3.1-flash-lite", "sakana/fugu-ultra",
  "meta/muse-spark-1.1",
])
const GO_BLOCKED = new Set([
  "meta/muse-spark-1.2", "meta/muse-spark-1.3", "xai/grok-4.6", "xai/grok-4.7",
  "xiaomi/mimo-v2.6-pro-ultraspeed", "google/gemini-3.7-flash", "google/gemini-3.8-flash",
  "gpt-5.6-sol", "claude-sonnet-5-5", "claude-haiku-5-5",
])
const PRO_BLOCKED = new Set([
  "claude-fable-5-1", "claude-fable-5", "claude-opus-5-5", "claude-opus-5",
  "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-opus-4-5-20251101",
  "gpt-6-astra", "gpt-6.1-sol", "sakana/fugu-ultra",
])
// by the plan's name (PLAN_NAMES): the old Pro and the new are let the same
const PLAN_MODELS = {
  Go: { premium: false, blocked: GO_BLOCKED },
  GOAT: { premium: false },
  Pro: { premium: true, blocked: PRO_BLOCKED },
  Provider: { premium: true, blocked: new Set(["gpt-6.1-sol"]) },
  Max: { premium: true },
  Ultra: { premium: true },
  "Teams Pro": { premium: true },
}

// refuses is whether the plan named refuses the model: never for a plan
// the table doesn't know, "No plan" and an unread one among them.
function refuses(plan, id) {
  const p = PLAN_MODELS[plan]
  if (!p) return false
  return (!p.premium && PREMIUM.has(id)) || !!p.blocked?.has(id)
}

// The models of Command Code's list the Go plan is refused — magpie's
// cmdGoRefused.
const GO_REFUSED = new Set([...PREMIUM, ...GO_BLOCKED])
const GO_EFFORTS = Object.fromEntries(GO_MODELS.map((m) => [m.id, m.efforts ?? []]))

const isClaude = (id) => /^claude-/i.test(id) || id.startsWith("anthropic/")

// FREE are the models Command Code's CLI marks FREE in its picker (1.73.2:
// badge:"free", "{name} is free and uses shared capacity"); its API's list
// doesn't say so, and Space Bunny Alpha's and Pixel Canary's ids name
// nothing free — magpie's cmdFree. Neither says a model is served at a
// discount.
const FREE = new Set([
  "stealth/space-bunny-alpha", "stealth/pixel-canary",
  "poolside/laguna-s-2.1-free", "inclusionai/ling-3.0-flash-free",
  "inclusionai/ling-3.0-flash-sante:free", "inclusionai/ling-3.1-flash:free",
  "MiniMaxAI/MiniMax-M3-Free", "minimax/minimax-m3-free",
  "minimax/minimax-m2.7-free", "meituan/LongCat-2.0:free", "tencent/Hy3",
])

// configModel is a model as opencode.json's provider.models has it.
function configModel(m) {
  const npm = m.npm ?? (isClaude(m.id) ? MESSAGES : CHAT)
  return {
    name: m.name,
    ...(npm !== CHAT ? { provider: { npm, api: BASE } } : {}),
    limit: { context: m.context ?? 0, output: m.output ?? 0 },
    ...(m.images ? { attachment: true, modalities: { input: ["text", "image"], output: ["text"] } } : {}),
    ...(m.efforts?.length
      ? { reasoning: true, variants: Object.fromEntries(m.efforts.map((e) => [e, { reasoningEffort: e }])) }
      : {}),
    tool_call: true,
  }
}

// runtimeModel is a model as OpenCode's provider.models hook returns it.
// Whether it takes pictures is said only when known (m.images a boolean):
// Command Code's list doesn't say, and a false said for every model
// unknown is taken by magpie as the plan's answer, over models.dev's,
// which the built-in asks for a model its list says nothing of
// (yetone/magpie: every model of the plan without image input).
function runtimeModel(m) {
  const npm = m.npm ?? (isClaude(m.id) ? MESSAGES : CHAT)
  const input = { text: true, ...(typeof m.images === "boolean" ? { image: m.images } : {}), audio: false, video: false, pdf: false }
  return {
    id: m.id,
    providerID: ID,
    name: m.name ?? m.id,
    api: { id: m.id, url: BASE, npm },
    status: "active",
    headers: {},
    options: {},
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: { context: m.context ?? 0, output: m.output ?? 0 },
    capabilities: {
      temperature: true,
      reasoning: !!m.efforts?.length,
      attachment: !!m.images,
      toolcall: true,
      input,
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: Object.fromEntries((m.efforts ?? []).map((e) => [e, { reasoningEffort: e }])),
    // magpie's plugin host reads it; the CLI calls the model free
    ...(FREE.has(m.id) ? { free: true } : {}),
  }
}

// liveModels is the Provider API's list, with the APIs each model is
// served on (its Claude models on /messages alone, the open ones on
// /chat/completions and /responses, some on /responses alone); the default list's names and
// windows fill in what it leaves out. A model that turns thinking off
// (THINK_OFF) has its levels, off among them; the list names no levels,
// and magpie gives every other model its maker's. The list says nothing
// of pictures: a model takes them as the default list or the CLI's table (GO_MODELS)
// says, and every Claude model does; of any other nothing is said, and
// magpie answers from models.dev.
async function liveModels(key) {
  const res = await fetch(BASE + "/models", { headers: { Authorization: `Bearer ${key}`, "x-api-key": key } })
  if (!res.ok) throw new Error(`models: ${res.status}`)
  const body = await res.json()
  const known = Object.fromEntries(MODELS.map((m) => [m.id, m]))
  const table = Object.fromEntries(GO_MODELS.map((m) => [m.id, m]))
  const out = []
  for (const m of body?.data ?? []) {
    if (!m?.id) continue
    const k = known[m.id]
    let images = Array.isArray(m.modalities?.input) ? m.modalities.input.includes("image") : undefined
    if (images === undefined) images = k?.images ?? table[m.id]?.images ?? (isClaude(m.id) || undefined)
    const eps = (m.supported_endpoints ?? []).map((e) => String(e).replace(/\/$/, "").replace(/^\/v1/, ""))
    let npm = isClaude(m.id) ? MESSAGES : CHAT
    // the first of the APIs it is served on, in magpie's order (chat
    // completions, Responses, Messages), as the built-in picks it
    if (eps.length) npm = eps.includes("/chat/completions") ? CHAT : eps.includes("/responses") ? RESPONSES : eps.includes("/messages") ? MESSAGES : CHAT
    const ctx = Number(m.context_length)
    out.push({
      id: m.id,
      name: m.name && m.name !== m.id ? m.name : m.display_name || k?.name || m.id,
      context: Number.isFinite(ctx) && ctx > 0 ? ctx : k?.context ?? 0,
      images,
      npm,
      ...(THINK_OFF.has(m.id) ? { efforts: OFF_EFFORTS } : {}),
    })
  }
  if (!out.length) throw new Error("models: an empty list")
  return out
}

// goModels is the Go plan's list, as magpie's cmdGoFetch has it: the
// Provider API's list, which answers without a key (Go's key has no
// Provider API) and is asked without it, less what Go is refused; each
// model takes its name when the list gives none, its reasoning levels and,
// when the list doesn't say, its pictures and window from GO_MODELS
// (catalog.Decorate). Every Go model is asked on chat completions, which
// the fetch sends on to /alpha/generate. An account with credits beyond
// the plan's (open) is let every model, as the CLI lets it.
async function goModels(open = false) {
  const url = BASE + "/models"
  // the CLI asks its catalog with accept alone, under Node's fetch, whose
  // User-Agent is "node" (installModelCatalogManager)
  const res = await fetch(url, { headers: { Accept: "application/json", "User-Agent": "node" }, signal: AbortSignal.timeout(8_000) })
  const text = await res.text()
  // catalog.FetchURL's errors
  if (res.status !== 200) {
    const status = `${res.status} ${statusText(res.status)}`.trimEnd()
    const msg = listError(text)
    throw new Error(msg ? `${url}: ${status} (${msg})` : `${url}: ${status}`)
  }
  let body
  try {
    body = JSON.parse(text)
    if (body === null || typeof body !== "object" || Array.isArray(body)) throw 0
  } catch {
    throw new Error(`${url}: not a model list`)
  }
  let rows = Array.isArray(body.data) ? body.data : []
  if (!rows.length) rows = Array.isArray(body.models) ? body.models : []
  const known = Object.fromEntries(GO_MODELS.map((m) => [m.id, m]))
  const listed = []
  for (const r of rows) {
    const id = (typeof r?.id === "string" && r.id) || (typeof r?.name === "string" && r.name) || ""
    // a model that draws, or isn't for text (embeddings, speech), is
    // left out, as catalog.Chat and fetchOne leave it
    if (!id || drawsID(id) || !textID(id)) continue
    const input = Array.isArray(r.modalities?.input) ? r.modalities.input.includes("image") : undefined
    const ctx = typeof r.context_length === "number" && r.context_length > 0 ? Math.trunc(r.context_length) : 0
    let m = { id, name: (typeof r.display_name === "string" && r.display_name) || id, context: ctx, images: input, npm: CHAT }
    let k = known[id]
    const i = id.lastIndexOf("/")
    if (!k && i >= 0) k = known[id.slice(i + 1)]
    if (k) {
      m = {
        ...m,
        name: m.name === id ? k.name : m.name,
        efforts: k.efforts,
        images: input === undefined ? k.images : input,
        context: m.context || k.context,
      }
    }
    listed.push(m)
  }
  if (!listed.length) throw new Error(`${url}: no models listed`)
  const out = open ? listed : listed.filter((m) => !GO_REFUSED.has(m.id))
  if (!out.length) throw new Error("Command Code listed no models for the Go plan")
  return out
}

// drawsID and textID are catalog.DrawsID and catalog.textModel on an id.
const drawsID = (id) =>
  ["image", "imagen", "imagine", "dall-e", "flux", "seedream", "cogview", "stable-diffusion", "sdxl", "wanx", "kolors", "hidream"].some((w) => id.toLowerCase().includes(w))
const textID = (id) =>
  !["embed", "-tts", "image", "audio", "-live", "robotics", "computer-use", "deep-research", "transcribe", "realtime", "moderation", "whisper", "dall-e", "sora"].some((w) => id.includes(w))

// listError is a model list's error reply's message ({"error":{"message"}},
// {"error":"…"}, {"message"}), cut at 160 characters, as catalog's
// errorMessage reads it.
function listError(text) {
  let v
  try {
    v = JSON.parse(text)
  } catch {
    return ""
  }
  if (v === null || typeof v !== "object" || Array.isArray(v)) return ""
  let msg = typeof v.message === "string" ? v.message : ""
  if (typeof v.error?.message === "string" && v.error.message) msg = v.error.message
  else if (typeof v.error === "string" && v.error) msg = v.error
  msg = msg.trim()
  const r = [...msg]
  return r.length > 160 ? r.slice(0, 160).join("") + "…" : msg
}

// ---- the account --------------------------------------------------------------

async function accountJSON(path, key, timeout = 15_000) {
  const res = await fetch(API + path, {
    headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
    signal: AbortSignal.timeout(timeout),
  })
  if (!res.ok) {
    const e = new Error(`${path}: ${res.status}`)
    e.status = res.status
    throw e
  }
  return res.json()
}

// The plan names, by planId prefix, the longest first, with the dollars of
// credits each gives a month (the CLI's getPlanInfo; "individual-pro-v1"
// is the old Pro, $80 of them).
const PLAN_NAMES = [
  ["individual-provider", "Provider", 15],
  ["individual-pro-v1", "Pro", 80],
  ["individual-goat", "GOAT", 70],
  ["individual-ultra", "Ultra", 300],
  ["individual-max", "Max", 150],
  ["individual-pro", "Pro", 30],
  ["individual-go", "Go", 10],
  ["teams-pro", "Teams Pro", 40],
]
const NO_PLAN = "No plan"

function planOf(id) {
  const s = String(id ?? "").toLowerCase().replaceAll("_", "-")
  return PLAN_NAMES.find(([p]) => s.startsWith(p)) ?? ["", "", 0]
}

const planName = (id) => planOf(id)[1]

// subscriptionOf is the account's plan as billing/subscriptions says: its
// id and name, when its period ends and whether it renews then; NO_PLAN
// when it has none, undefined when it can't be read.
async function subscriptionOf(key, timeout) {
  let d
  try {
    const r = await accountJSON("/alpha/billing/subscriptions", key, timeout)
    // a 200 with success false when Command Code couldn't tell
    // ("write CONNECTION_CLOSED …"): unread, not no plan
    if (r?.success === false) return undefined
    d = r?.data
  } catch {
    return undefined
  }
  if (d?.planId && d.status !== "canceled" && d.status !== "incomplete_expired") {
    const renew = typeof d.cancelAtPeriodEnd === "boolean" ? (d.cancelAtPeriodEnd ? "off" : "auto") : ""
    return { id: d.planId, plan: planName(d.planId) || d.planId, until: when(d.currentPeriodEnd), renew }
  }
  return { id: "", plan: NO_PLAN }
}

// planNow is the account's plan, for the models and the endpoint Go takes:
// the subscription read in the last ten minutes, else read now, with the
// plan the sign-in saved going meanwhile when it takes more than a moment.
async function planNow(key, saved) {
  return (await subscriptionNow(key, saved ? 2_000 : waits.subWait))?.plan || saved || ""
}

// ---- the allowance ------------------------------------------------------------
// As magpie's built-in Command Code account shows it
// (internal/provider/commandcode_plan.go, cmdQuota).

// statusText is a refused request's status as magpie says it (Go's
// http.StatusText).
const statusText = (s) =>
  ({ 413: "Request Entity Too Large", 414: "Request URI Too Long", 416: "Requested Range Not Satisfiable", 418: "I'm a teapot", 509: "" })[s] ?? STATUS_CODES[s] ?? ""

// number reads an amount given as a JSON number or as a string of one.
function number(v) {
  if (typeof v === "number") return v
  if (typeof v !== "string" || !v.trim()) return undefined
  const n = Number(v.trim())
  return Number.isNaN(n) ? undefined : n
}

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/

// when reads a reset time: unix seconds or milliseconds, or RFC 3339; an
// ISO time, undefined when there is none.
function when(v) {
  if (typeof v === "string" && RFC3339.test(v) && !Number.isNaN(Date.parse(v))) return new Date(v).toISOString()
  let n = number(v)
  if (n === undefined || !(n > 0)) return undefined
  if (n < 1e12) n *= 1000
  const d = new Date(Math.trunc(n))
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

// money is dollars to the cent, a half cent to the even one, as magpie
// writes them.
function money(v) {
  const exact = Math.abs(v).toFixed(20)
  const tie = /\.\d\d50*$/.test(exact)
  const s = tie && Number(exact[exact.indexOf(".") + 2]) % 2 === 0 ? exact.slice(0, exact.indexOf(".") + 3) : Math.abs(v).toFixed(2)
  return "$" + (v < 0 ? "-" : "") + s
}

// limits is the plan's 5-hour and weekly windows in a credits reply,
// those it gives a cap.
function limits(c) {
  const out = []
  const w = c?.windowLimits
  if (!w) return out
  for (const [name, span, x] of [["5 hours", 5 * 3600, w.fiveHour], ["Weekly", 7 * 24 * 3600, w.weekly]]) {
    if (!x) continue
    const used = number(x.used)
    const cap = number(x.cap)
    if (used === undefined || cap === undefined || !(cap > 0)) continue
    const at = when(x.resetAt)
    out.push({ name, used: Math.min(100, (100 * Math.max(0, used)) / cap), span, ...(at ? { resetsAt: at } : {}) })
  }
  return out
}

// allowanceOf makes a credits reply into the plan's windows: the 5-hour
// and weekly limits, and the dollars as one more, used of the CLI's pool
// (the plan's month of credits, or what is left of it if more, and the
// bought and free ones) as its /usage bar is. A reply with neither windows
// nor a plan tells a balance: a key's money, not an allowance. The month's
// credits come back at until, the end of the plan's period
// (billing/subscriptions' currentPeriodEnd), when it is known
// (yetone/magpie#2454).
function allowanceOf(c, until) {
  const out = { windows: limits(c) }
  const [, name, planMonthly] = planOf(c?.credits?.planId)
  if (name) out.plan = name
  let monthly = 0
  let left = 0
  let known = false
  for (const [i, v] of [c?.credits?.monthlyCredits, c?.credits?.purchasedCredits, c?.credits?.freeCredits].entries()) {
    let n = number(v)
    if (n === undefined) continue
    n = Math.max(0, n)
    if (i === 0) monthly = n
    left += n
    known = true
  }
  if (known && planMonthly > 0) {
    const pool = Math.max(planMonthly, monthly) + left - monthly
    out.windows.push({ name: "Credits", used: Math.min(100, (100 * (pool - left)) / pool), display: money(pool - left) + " / " + money(pool), ...(until ? { resetsAt: until } : {}) })
  } else if (known && !out.windows.length) out.balance = money(left)
  return out
}

// Each key's subscription as last read, and the reading under way.
// billing/subscriptions takes 15–20 seconds at times, longer than magpie
// waits for a card (15): the allowance waits for it waits.subWait at most, then
// goes with the one read before while the reading goes on for next time.
const subsSeen = new Map()

// GO_NO_API is the Provider API refusing a Go plan's key: "Your Go plan
// doesn't include API access. Upgrade to Provider or higher …" (#969)
const GO_NO_API = /\bGo plan doesn.t include API access/i
const SUB_KEEP = 10 * 60 * 1000
const waits = { subWait: 9_000, creditWait: 5_000 }

// Each key's credits beyond its plan's, as last read: bought or free ones
// let the account every model, the plan's or not (the CLI's
// evaluateModelAccess; Command Code's refusal: "… or extra on demand
// usage").
const extraSeen = new Map()
const hasExtra = (c) => number(c?.credits?.purchasedCredits) > 0 || number(c?.credits?.freeCredits) > 0

// extraNow is whether the key has such credits: read now, else as last
// read, else none, so the plan's list stands when they can't be read and
// a model it leaves out is still refused by Command Code, as it was asked.
async function extraNow(key) {
  try {
    const v = hasExtra(await accountJSON("/alpha/billing/credits", key, waits.creditWait))
    extraSeen.set(key, v)
    return v
  } catch {
    return extraSeen.get(key) ?? false
  }
}

function subscriptionNow(key, wait = waits.subWait) {
  const seen = subsSeen.get(key) ?? {}
  if (seen.sub && Date.now() - seen.at < SUB_KEEP) return Promise.resolve(seen.sub)
  if (!seen.reading) {
    seen.reading = subscriptionOf(key, 60_000).then((sub) => {
      const now = subsSeen.get(key) ?? {}
      subsSeen.set(key, sub ? { sub, at: Date.now() } : { sub: now.sub, at: now.at })
      return sub
    })
    subsSeen.set(key, seen)
  }
  let timer
  const late = new Promise((r) => (timer = setTimeout(() => r(seen.sub), wait)))
  return Promise.race([seen.reading.then((sub) => sub ?? seen.sub), late]).finally(() => clearTimeout(timer))
}

// usage is the account's allowance, and the subscription read for it. The
// plan is said even while the credits can't be read (Command Code answers
// 503, "Couldn't verify your credit balance just now", at times), and the
// plan saved (saved: { id, plan }) stands for the subscription while it
// can't be read, or is slow to: then it is waited for a moment only.
async function usage(key, saved) {
  const [read, credits] = await Promise.all([
    subscriptionNow(key, saved ? 2_000 : waits.subWait),
    accountJSON("/alpha/billing/credits", key, 12_000).then(
      (c) => ({ c }),
      (e) => ({ e }),
    ),
  ])
  const sub = read ?? saved
  let out = { windows: [] }
  if (credits.e) out.error = credits.e.status ? statusText(credits.e.status) : credits.e.message
  else {
    // billing/credits leaves the plan out: the month of credits the pool
    // is made of is the subscription's plan's
    const c = credits.c ?? {}
    extraSeen.set(key, hasExtra(c))
    out = allowanceOf({ ...c, credits: { ...(c.credits ?? {}), planId: c.credits?.planId || sub?.id || "" } }, sub?.until)
  }
  if (sub) {
    out.plan = sub.plan
    if (sub.until) out.until = sub.until
    if (sub.renew) out.renew = sub.renew
  }
  return { out, read }
}

// signedIn names a new key's account with whoami and reads its plan. Only
// whoami turning the key down (401, 403) fails: it has answered a key just
// made with a 500, so one that errs is asked again a few times, then
// passed over for the name Studio sent with the key.
async function signedIn(a) {
  let me
  for (let tries = 0; ; tries++) {
    try {
      me = (await accountJSON("/alpha/whoami", a.apiKey))?.user
      break
    } catch (e) {
      if (e.status === 401 || e.status === 403) throw new Error("Command Code didn't take the new key")
      if (tries >= 2) break
      await new Promise((r) => setTimeout(r, 1000))
    }
  }
  const who = me?.userName || a.userName || me?.email || me?.id || a.userId || "Command Code"
  const sub = await subscriptionOf(a.apiKey, 30_000)
  if (sub) subsSeen.set(a.apiKey, { sub, at: Date.now() })
  const plan = sub?.plan ?? ""
  return { who, plan, email: me?.email ?? "" }
}

// success is what a sign-in gives OpenCode: the key, and who it is.
async function success(a) {
  const { who, plan } = await signedIn(a)
  const metadata = { email: who }
  if (a.userId) metadata.userId = a.userId
  if (a.keyName) metadata.keyName = a.keyName
  if (plan) metadata.plan = plan
  return { type: "success", provider: ID, key: a.apiKey, metadata }
}

// ---- the browser sign-in --------------------------------------------------------

function page(ok, title, text) {
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c])
  return `<!doctype html><meta charset="utf-8"><title>${esc(title)}</title>
<style>body{font:15px system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;margin:0;color:#222;background:#fafafa}
@media (prefers-color-scheme:dark){body{color:#eee;background:#161616}}main{text-align:center;max-width:28rem;padding:1rem}
.d{font-size:2rem;color:${ok ? "#2a9d5c" : "#c0392b"}}</style>
<main><div class="d">${ok ? "✓" : "✕"}</div><h1>${esc(title)}</h1><p>${esc(text)}</p></main>`
}

// browserSignIn is the CLI's: Studio asks the user to approve a key for
// this machine, then posts it (a form, or JSON from an older Studio) to
// the callback, which sends the browser on to a page saying how it went.
async function browserSignIn() {
  const state = randomBytes(24).toString("base64url")
  let status = { state: "waiting" }
  let settle
  const done = new Promise((r) => (settle = r))
  const finish = (st, result) => {
    if (status.state !== "waiting") return
    status = st
    settle(result)
  }

  const server = createServer(async (req, res) => {
    const origin = ORIGINS.includes(req.headers.origin) ? req.headers.origin : ORIGINS[0]
    res.setHeader("Access-Control-Allow-Origin", origin)
    res.setHeader("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
    res.setHeader("Access-Control-Allow-Headers", "Content-Type")
    if (req.headers["access-control-request-private-network"] === "true") res.setHeader("Access-Control-Allow-Private-Network", "true")
    const path = new URL(req.url ?? "/", "http://127.0.0.1").pathname
    const html = (code, body) => {
      res.writeHead(code, { "Content-Type": "text/html; charset=utf-8" })
      res.end(body)
    }
    if (req.method === "OPTIONS") return res.writeHead(204).end()
    if (path === "/callback/complete" && req.method === "GET") {
      return status.state === "done"
        ? html(200, page(true, "You're signed in", `${status.who} is signed in. You can close this tab.`))
        : html(200, page(false, "Sign-in didn't finish", status.error || "Start it again."))
    }
    if (path === "/cancel") {
      finish({ state: "canceled" }, { type: "failed", error: "the sign-in was canceled" })
      return res.writeHead(204).end()
    }
    if (path !== "/callback") return res.writeHead(404).end()
    if (req.method !== "POST") return res.writeHead(405).end()

    let raw = ""
    for await (const chunk of req) {
      raw += chunk
      if (raw.length > 10_000) break
    }
    const isJSON = String(req.headers["content-type"] ?? "").trim().toLowerCase().startsWith("application/json")
    let got = {}
    if (isJSON) {
      try {
        got = JSON.parse(raw) ?? {}
      } catch {}
    } else got = Object.fromEntries(new URLSearchParams(raw))
    const answer = (ok, msg) => {
      if (isJSON) {
        res.writeHead(ok ? 200 : 400, { "Content-Type": "application/json" })
        return res.end(JSON.stringify({ success: ok, error: msg }))
      }
      res.writeHead(303, { Location: "/callback/complete?state=" + encodeURIComponent(got.state ?? ""), "Cache-Control": "no-store" })
      res.end()
    }
    if (got.state !== state) return html(403, page(false, "This link isn't from this sign-in", "Start it again."))
    if (status.state !== "waiting") return answer(false, "this sign-in is over")
    if (got.error) {
      const msg = got.error === "access_denied" ? "the sign-in was denied" : got.error_description || got.error
      finish({ state: "failed", error: msg }, { type: "failed", error: msg })
      return answer(false, msg)
    }
    if (!got.apiKey) return answer(false, "Command Code sent back no key")
    try {
      const r = await success({ apiKey: got.apiKey, userId: got.userId, userName: got.userName, keyName: got.keyName })
      finish({ state: "done", who: r.metadata.email }, r)
      answer(true, "")
    } catch (e) {
      finish({ state: "failed", error: e.message }, { type: "failed", error: e.message })
      answer(false, e.message)
    }
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const port = server.address().port
  const timer = setTimeout(() => finish({ state: "failed", error: "the sign-in timed out" }, { type: "failed", error: "the sign-in timed out" }), SIGN_IN_TIMEOUT)
  // the browser comes back for its page after the key: the server stays
  // up a while for it
  done.then(() => {
    clearTimeout(timer)
    setTimeout(() => server.close(), 10_000).unref?.()
  })
  const q = new URLSearchParams({ callback: `http://127.0.0.1:${port}/callback`, state, mode: "redirect" })
  return {
    url: `${STUDIO}/studio/auth/cli?${q}`,
    instructions: "Approve the key for this machine in Command Code Studio.",
    method: "auto",
    callback: () => done,
  }
}

// cliSignIn takes the account Command Code's CLI is signed in to, reading
// ~/.commandcode/auth.json and changing nothing.
async function cliSignIn() {
  const path = join(homedir(), ".commandcode", "auth.json")
  return {
    url: "",
    instructions: `Uses the account in ${path}.`,
    method: "auto",
    callback: async () => {
      try {
        const a = JSON.parse(await readFile(path, "utf8"))
        if (!String(a?.apiKey ?? "").trim()) return { type: "failed", error: `Command Code's CLI isn't signed in: ${path} has no key` }
        const r = await success(a)
        r.metadata.cli = true
        return r
      } catch (e) {
        return { type: "failed", error: e?.code === "ENOENT" ? "Command Code's CLI isn't signed in: run `commandcode login`" : e instanceof SyntaxError ? `${path} can't be read` : e.message }
      }
    },
  }
}

// liveKey is the key an account has now: one taken from the CLI's sign-in
// reads the CLI's again, as a `commandcode login` since may have changed
// it, and keeps the one it was saved with while the CLI has none.
async function liveKey(auth) {
  if (!auth?.metadata?.cli) return auth?.key
  try {
    const a = JSON.parse(await readFile(join(homedir(), ".commandcode", "auth.json"), "utf8"))
    const k = String(a?.apiKey ?? "").trim()
    if (k) return k
  } catch {}
  return auth.key
}

// ---- the Go plan: /alpha/generate ----------------------------------------------

const session = randomUUID() // one x-session-id for as long as this runs, as one CLI session has

const EFFORT_RANK = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]

// fitEffort is the level the model has nearest the one asked, the higher
// on a tie.
function fitEffort(want, levels) {
  if (want === "ultra" && !levels.includes(want)) want = "max"
  if (!levels.length || levels.includes(want)) return want
  const at = EFFORT_RANK.indexOf(want)
  if (at < 0) return want
  let best = want
  let dist = EFFORT_RANK.length
  for (const l of levels) {
    const i = EFFORT_RANK.indexOf(l)
    if (i < 0 || l === "none") continue
    const d = Math.abs(i - at)
    if (d < dist || (d === dist && i > at)) [best, dist] = [l, d]
  }
  return best
}

const textOf = (c) =>
  typeof c === "string" ? c : Array.isArray(c) ? c.filter((p) => p?.type === "text").map((p) => p.text ?? "").join("") : ""

function imagePart(p) {
  const u = typeof p.image_url === "string" ? p.image_url : p.image_url?.url
  if (!u) return null
  const m = /^data:([^;,]+)[;,]/.exec(u)
  return m ? { type: "image", image: u, mimeType: m[1] } : { type: "image", image: u }
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

// generateBody is a chat completion request as the CLI asks
// /alpha/generate, with no structure, branch or status behind it. The
// working directory and whether it is a git repository are the agent's as
// magpie tells them (place, yetone magpie-community/plugins#86); untold,
// the CLI's config outside a git repository, in the user's home.
function generateBody(chat, place = {}) {
  const system = []
  const messages = []
  const names = {} // a tool call's id → its tool
  let results = null
  const flush = () => {
    if (results?.length) messages.push({ role: "tool", content: results })
    results = null
  }
  for (const m of chat.messages ?? []) {
    if (m.role === "system" || m.role === "developer") {
      system.push(textOf(m.content))
      continue
    }
    if (m.role === "tool") {
      results ??= []
      results.push({
        type: "tool-result",
        toolCallId: m.tool_call_id,
        toolName: names[m.tool_call_id] || "unknown",
        output: { type: "text", value: textOf(m.content) },
      })
      continue
    }
    flush()
    if (m.role === "assistant") {
      const parts = []
      const thought = m.reasoning_content ?? m.reasoning
      if (typeof thought === "string" && thought) parts.push({ type: "reasoning", text: thought })
      const t = textOf(m.content)
      if (t) parts.push({ type: "text", text: t })
      for (const c of m.tool_calls ?? []) {
        names[c.id] = c.function?.name
        parts.push({ type: "tool-call", toolCallId: c.id, toolName: c.function?.name, input: parseArgs(c.function?.arguments) })
      }
      if (parts.length) messages.push({ role: "assistant", content: parts })
      continue
    }
    const parts = []
    if (typeof m.content === "string") {
      if (m.content) parts.push({ type: "text", text: m.content })
    } else
      for (const p of m.content ?? []) {
        if (p?.type === "text" && p.text) parts.push({ type: "text", text: p.text })
        else if (p?.type === "image_url") {
          const im = imagePart(p)
          if (im) parts.push(im)
        }
      }
    if (parts.length) messages.push({ role: "user", content: parts })
  }
  flush()
  const tools = (chat.tools ?? [])
    .filter((t) => t?.type === "function" && t.function?.name)
    .map((t) => ({
      name: t.function.name,
      description: t.function.description ?? "",
      input_schema: t.function.parameters ?? { type: "object", properties: {} },
    }))
  const params = {
    model: chat.model,
    messages,
    tools,
    system: system.join("\n\n"),
    // within what Command Code takes: more is refused, the whole request
    // with it
    max_tokens: Math.min(chat.max_completion_tokens || chat.max_tokens || MAX_TOKENS, MAX_OUTPUT),
    stream: true,
  }
  if (typeof chat.temperature === "number") params.temperature = chat.temperature
  // off ("none", or a client's "off") is not known to be taken here: it
  // is sent as nothing, the model's own default
  const effort = chat.reasoning_effort
  if (effort && effort !== "none" && effort !== "off") params.reasoning_effort = fitEffort(effort, GO_EFFORTS[chat.model] ?? [])
  const d = new Date()
  const date = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
  return {
    config: {
      workingDir: place.dir || homedir(),
      date,
      environment: process.platform,
      structure: [],
      isGitRepo: place.git === "yes",
      currentBranch: "",
      mainBranch: "",
      gitStatus: "",
      recentCommits: [],
    },
    memory: null,
    taste: null,
    skills: null,
    permissionMode: "standard",
    params,
  }
}

// ZDR is the header that asks for Zero Data Retention, and ZDR_OPTION the
// provider's switch for it in magpie (auth.options, read through
// input.magpie.option).
const ZDR = "x-cmd-zdr"
const ZDR_OPTION = "zdr"

// zdrOn says whether requests ask for Zero Data Retention: magpie's switch
// for it, read at each request so a change needs no restart, or CMD_ZDR
// as the CLI reads it ("1" alone; isCmdZdrEnabled).
function zdrOn(magpie) {
  try {
    if (magpie?.option?.(ID, ZDR_OPTION) === true) return true
  } catch {}
  return process.env.CMD_ZDR === "1"
}

// projectSlug is the CLI's x-project-slug for a working directory: its
// last segment, "root" when it has none (getProjectDirName's slugify of
// basename(cwd)); a Windows folder's too, as the CLI's win32 basename
// splits it. A name a header can't carry (past Latin-1: the CLI's own
// request fails there, Node refusing the header) goes percent-encoded.
function projectSlug(dir) {
  const name = basename(String(dir || homedir()).replaceAll("\\", "/")) || "root"
  return /^[\x20-\x7e\xa0-\xff]*$/.test(name) ? name : encodeURIComponent(name)
}

// generateHeaders are the CLI's own (buildCommandAuthHeaders), with its
// x-cmd-zdr: 1 when ZDR is on; the project slug is the working folder's
// name, as the CLI's is.
const generateHeaders = (key, zdr = false, place = {}) => ({
  "Content-Type": "application/json",
  "User-Agent": "cli",
  "x-command-code-version": CLI_VERSION,
  "x-cli-environment": "production",
  "x-project-slug": projectSlug(place.dir),
  "x-taste-learning": "false",
  "x-session-id": session,
  Authorization: `Bearer ${key}`,
  ...(zdr ? { [ZDR]: "1" } : {}),
})

// QUOTA_WORDS are magpie's quotaWords (internal/gateway/fallback.go): a
// message already in them isn't said again as out of credits.
const QUOTA_WORDS = /quota|insufficient|balance|credit|billing|exceeded|rate.?limit|usage.?limit|limit.?reached|hit your .*limit|limit.{0,24}resets|too many requests|overloaded|余额|额度|欠费|限流|频率|套餐|用量|上限/i

// failure is the status and message for a failure, from Command Code's
// {"error":{"type","message"}} or its text: a model the plan hasn't is a
// 403, credits run out a 402, a window's limit a 429.
function failure(status, text) {
  let msg = ""
  try {
    const j = JSON.parse(text)
    const err = j?.error && typeof j.error === "object" ? JSON.stringify(j.error) : j?.error
    msg = [j?.error?.message, j?.message, err].find((s) => s != null && String(s).trim()) ?? ""
  } catch {}
  // an empty body is its status's name, as Go's http.StatusText says it:
  // an empty 429 reads "Too Many Requests", which quotaWords knows
  msg = String(msg || text || "").trim() || statusText(status)
  if (msg.length > 600) msg = msg.slice(0, 600)
  const low = msg.toLowerCase()
  if (msg.includes("MODEL_NOT_IN_PLAN:")) {
    status = 403
    msg = "Model not in plan: " + msg.replace("MODEL_NOT_IN_PLAN:", "").trim()
  } else if (low.includes("model_not_in_plan")) status = 403
  else if (low.includes("premium_credits_exhausted") || low.includes("insufficient credits")) {
    status = 402
    msg = msg.replace("PREMIUM_CREDITS_EXHAUSTED:", "").trim()
    if (!QUOTA_WORDS.test(msg)) msg = "out of credits: " + msg
  } else if (msg.includes("RATE_LIMITED") || low.includes("usage limit") || low.includes("window_limit")) {
    if (status < 400 || status === 500) status = 429
  }
  if (status < 400 || status > 599) status = 502
  return { status, message: msg }
}

// streamError is an error line's status and message: a string, or
// {message, statusCode}, whose message may hold the HTTP error it was.
function streamError(e) {
  let msg = typeof e === "string" ? e : e?.message ?? ""
  let status = typeof e === "object" ? Number(e?.statusCode) || 0 : 0
  msg ||= "Stream error"
  const i = msg.indexOf("{")
  if (i >= 0) {
    try {
      const j = JSON.parse(msg.slice(i))
      const n = parseInt(msg.slice(0, i).trim(), 10)
      if (n >= 400) status = n
      if (j?.error?.message) msg = j.error.message
    } catch {}
  }
  const f = failure(status || 500, msg)
  // a ZDR request for a model no upstream that keeps nothing serves: its
  // type goes on with it, as the Provider API's 422 says it
  // ({"type":"cmd_zdr_no_providers","code":"CMD_ZDR_NO_PROVIDERS",
  // "statusCode":400} on /alpha/generate)
  if (typeof e === "object" && (e?.type === "cmd_zdr_no_providers" || e?.code === "CMD_ZDR_NO_PROVIDERS")) f.type = "cmd_zdr_no_providers"
  return f
}

// errorResponse is a failure as the built-in answered it, the account
// kept: the built-in never marked a Command Code account lapsed, a 401 of
// Command Code's among them.
const errorResponse = ({ status, message, type }) =>
  new Response(JSON.stringify({ error: { message, type: type || "commandcode_error", code: status } }), {
    status,
    headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "kept" },
  })

// kept is a Provider API answer, as it came, saying the account is kept.
function kept(res) {
  const headers = new Headers(res.headers)
  headers.delete("content-length")
  headers.delete("content-encoding")
  headers.set("X-Magpie-Sign-In", "kept")
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}

// lines reads the reply a JSON object a line.
async function* lines(body) {
  const dec = new TextDecoder()
  let buf = ""
  for await (const chunk of body) {
    buf += dec.decode(chunk, { stream: true })
    let n
    while ((n = buf.indexOf("\n")) >= 0) {
      const s = buf.slice(0, n).trim()
      buf = buf.slice(n + 1)
      if (s) yield s
    }
  }
  if (buf.trim()) yield buf.trim()
}

const FINISH = { "tool-calls": "tool_calls", length: "length", "content-filter": "content_filter" }

// CUT_SHORT_NOTE ends a streamed reply Command Code stopped sending
// before its finish line (as zen-free does, #91): magpie's gateway can't go
// on with a Chat Completions reply, and failing it would throw away what
// the client was already shown.
const CUT_SHORT_NOTE = "(The reply was cut short: Command Code closed the stream before it finished. Ask again to go on.)"

// events turns the reply's lines into chat completion pieces: text,
// reasoning, whole tool calls, then the finish with the usage. A reply
// with no finish line was cut short: { cut } when it said something
// (its tool calls come whole, never half written), else an error.
async function* events(body) {
  let tools = 0
  let cacheWrite = 0
  let said = false
  let wrote = false
  for await (const s of lines(body)) {
    let l
    try {
      l = JSON.parse(s)
    } catch {
      continue
    }
    switch (l.type) {
      case "text-delta":
        if (l.text) {
          said = wrote = true
          yield { text: l.text }
        }
        break
      case "reasoning-delta":
        if (l.text) {
          said = true
          yield { reasoning: l.text }
        }
        break
      case "tool-call": {
        if (l.providerExecuted) break // run by the server, its result beside it
        let input = l.input ?? l.args
        input = typeof input === "string" ? parseArgs(input) : input ?? {}
        said = true
        yield { tool: { index: tools++, id: l.toolCallId || "call_" + randomBytes(12).toString("hex"), name: l.toolName, args: JSON.stringify(input) } }
        break
      }
      case "cache-write-tokens":
        cacheWrite = Math.max(cacheWrite, l.cacheWriteTokens ?? 0)
        break
      case "finish": {
        const t = l.totalUsage ?? {}
        const d = t.inputTokenDetails ?? {}
        let stop = FINISH[l.finishReason] ?? "stop"
        if (tools > 0 && stop === "stop") stop = "tool_calls"
        const input = t.inputTokens ?? 0 // the AI SDK's counts the cache in it, as OpenAI's does
        yield {
          stop,
          usage: {
            prompt_tokens: input,
            completion_tokens: t.outputTokens ?? 0,
            total_tokens: input + (t.outputTokens ?? 0),
            prompt_tokens_details: { cached_tokens: d.cacheReadTokens ?? 0, cache_write_tokens: Math.max(cacheWrite, d.cacheWriteTokens ?? 0) },
            completion_tokens_details: { reasoning_tokens: t.outputTokenDetails?.reasoningTokens ?? 0 },
          },
        }
        return
      }
      case "abort":
        yield { stop: "stop" }
        return
      case "error":
        yield { error: streamError(l.error) }
        return
    }
  }
  const error = { status: 502, message: "the reply ended before it was complete" }
  yield said ? { cut: tools > 0 ? "tool_calls" : "stop", wrote, error } : { error }
}

// generate answers a chat completion through /alpha/generate.
async function generate(key, chat, signal, place, zdr = false) {
  const res = await fetch(API + "/alpha/generate", {
    method: "POST",
    headers: generateHeaders(key, zdr, place),
    body: JSON.stringify(generateBody(chat, place)),
    signal,
  })
  if (!res.ok) return errorResponse(failure(res.status, (await res.text()).slice(0, 1 << 20)))
  const id = "chatcmpl-" + randomBytes(12).toString("hex")
  const created = Math.floor(Date.now() / 1000)
  const it = events(res.body)
  // the first piece before the answer's status: a reply that fails
  // straight away keeps its status
  const first = await it.next()
  if (first.value?.error) return errorResponse(first.value.error)

  if (!chat.stream) {
    const msg = { role: "assistant", content: "" }
    let reasoning = ""
    const calls = []
    let stop = "stop"
    let usage
    for (let r = first; !r.done; r = await it.next()) {
      const e = r.value
      if (e.error) return errorResponse(e.error)
      if (e.text) msg.content += e.text
      if (e.reasoning) reasoning += e.reasoning
      if (e.tool) calls.push({ id: e.tool.id, type: "function", function: { name: e.tool.name, arguments: e.tool.args } })
      if (e.stop) [stop, usage] = [e.stop, e.usage]
    }
    if (reasoning) msg.reasoning_content = reasoning
    if (calls.length) msg.tool_calls = calls
    return Response.json({ id, object: "chat.completion", created, model: chat.model, choices: [{ index: 0, message: msg, finish_reason: stop }], ...(usage ? { usage } : {}) })
  }

  const enc = new TextEncoder()
  const chunk = (delta, finish_reason = null, extra = {}) =>
    enc.encode(`data: ${JSON.stringify({ id, object: "chat.completion.chunk", created, model: chat.model, choices: [{ index: 0, delta, finish_reason }], ...extra })}\n\n`)
  let pending = first
  let began = false
  const stream = new ReadableStream({
    async pull(ctl) {
      const r = pending ?? (await it.next())
      pending = null
      if (!began) {
        began = true
        ctl.enqueue(chunk({ role: "assistant", content: "" }))
      }
      if (r.done) {
        ctl.enqueue(enc.encode("data: [DONE]\n\n"))
        return ctl.close()
      }
      const e = r.value
      if (e.text) ctl.enqueue(chunk({ content: e.text }))
      else if (e.reasoning) ctl.enqueue(chunk({ reasoning_content: e.reasoning }))
      else if (e.tool) ctl.enqueue(chunk({ tool_calls: [{ index: e.tool.index, id: e.tool.id, type: "function", function: { name: e.tool.name, arguments: e.tool.args } }] }))
      else if (e.stop) ctl.enqueue(chunk({}, e.stop, e.usage ? { usage: e.usage } : {}))
      else if (e.cut) {
        // the client has its start: a shorter turn that says so, not a lost one
        ctl.enqueue(chunk({ content: (e.wrote ? "\n\n" : "") + CUT_SHORT_NOTE }))
        ctl.enqueue(chunk({}, e.cut))
        ctl.enqueue(enc.encode("data: [DONE]\n\n"))
        ctl.close()
      } else if (e.error) {
        ctl.enqueue(enc.encode(`data: ${JSON.stringify({ error: { message: e.error.message, code: e.error.status } })}\n\n`))
        ctl.close()
      }
    },
    cancel() {
      it.return?.()
    },
  })
  return new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" } })
}

// providerBody is the chat completion chat as the Provider API is sent it,
// or undefined when it goes as it came: off, magpie's none or a client's
// "off", is "off" for a model that turns thinking off (THINK_OFF), and
// left out for any other, which refuses both "none" and "off" (#334).
function providerBody(chat) {
  const e = chat?.reasoning_effort
  if (e !== "none" && e !== "off") return undefined
  const out = { ...chat }
  if (THINK_OFF.has(chat.model)) out.reasoning_effort = "off"
  else delete out.reasoning_effort
  return e === out.reasoning_effort ? undefined : JSON.stringify(out)
}

// ---- the plugin ---------------------------------------------------------------

export const _internal = { projectSlug, subsSeen, extraSeen, waits, liveKey, failure, goModels, refuses, GO_MODELS, GO_REFUSED, zdrOn }

export async function CommandCodePlugin({ client, magpie } = {}) {
  // isGo keeps a key as Go's, for its models and endpoint: in the
  // subscriptions seen, and saved with the key as usage saves the plan read
  async function isGo(key, auth) {
    const was = subsSeen.get(key)?.sub
    subsSeen.set(key, { sub: { id: was?.plan === "Go" ? was.id : "", plan: "Go" }, at: Date.now() })
    const md = auth?.metadata ?? {}
    if (md.plan === "Go" || !client?.auth?.set) return
    try {
      await client.auth.set({ path: { id: ID }, body: { ...auth, metadata: { ...md, plan: "Go" } } })
    } catch {}
  }
  return {
    auth: {
      provider: ID,
      async loader(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "api" || !auth.key) return {}
        const key = await liveKey(auth)
        const saved = auth.metadata?.plan
        return {
          baseURL: BASE,
          apiKey: key,
          headers: { "x-api-key": key },
          // every request signed with the key, both ways Command Code
          // takes it; a Go key's chat completions to /alpha/generate
          async fetch(input, init = {}) {
            const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
            const headers = new Headers(init.headers ?? (input instanceof Request ? input.headers : undefined))
            const key = await liveKey((await getAuth()) ?? auth)
            headers.set("Authorization", `Bearer ${key}`)
            headers.set("x-api-key", key)
            const place = workPlace(headers)
            // Zero Data Retention, as the CLI asks it; a 422
            // cmd_zdr_no_providers (no upstream that keeps nothing serves
            // the model) goes on to the agent as it came
            const zdr = zdrOn(magpie)
            if (zdr) headers.set(ZDR, "1")
            const chatting = /\/chat\/completions$/.test(new URL(url).pathname)
            let chat
            if (chatting) {
              try {
                const b = init.body ?? (input instanceof Request ? await input.clone().text() : undefined)
                chat = JSON.parse(typeof b === "string" ? b : new TextDecoder().decode(b))
              } catch {
                return errorResponse({ status: 400, message: "a request that isn't JSON" })
              }
              if ((await planNow(key, saved)) === "Go") return kept(await generate(key, chat, init.signal, place, zdr))
            }
            // the Provider API's answer goes on as it came, the account
            // kept: the built-in neither marked one lapsed nor cleared it
            const body = chatting ? providerBody(chat) : undefined
            const res = await (body === undefined
              ? fetch(input, { ...init, headers })
              : fetch(url, { ...init, method: init.method ?? (input instanceof Request ? input.method : "POST"), headers, body }))
            if (res.ok) return kept(res)
            // a Go key the plan wasn't known for (billing/subscriptions slow
            // or failing, nothing saved yet) is refused by the Provider API
            // (#969): it is Go from now on, and a chat completion is asked
            // again where Go is served
            const text = await res.text()
            if (GO_NO_API.test(text)) {
              await isGo(key, (await getAuth()) ?? auth)
              if (chatting) return kept(await generate(key, chat, init.signal, place, zdr))
            }
            return kept(new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers }))
          },
        }
      },
      // magpie's switches for the provider (#2038): shown in its editor,
      // read with input.magpie.option at each request
      options: [
        {
          key: ZDR_OPTION,
          label: "Zero Data Retention",
          description:
            "Sends x-cmd-zdr: 1, as the CLI's CMD_ZDR=1 does: requests go only to upstreams that keep no prompts or replies. They usually cost more, and a model with no such upstream fails (422 cmd_zdr_no_providers).",
          confirm: "ZDR requests usually cost more, and a model with no zero-data-retention upstream fails instead of falling back.",
        },
      ],
      methods: [
        { type: "oauth", label: "Command Code (browser)", authorize: browserSignIn },
        { type: "oauth", label: "Command Code CLI's sign-in", authorize: cliSignIn },
        { type: "api", label: "API key (commandcode.ai/settings/keys)" },
      ],
      // magpie's: the plan and how much of it is used
      async usage(getAuth) {
        const auth = await getAuth()
        // signIn kept on each: the built-in's usage read never marked the
        // account lapsed nor cleared it
        if (auth?.type !== "api" || !auth.key) return { error: "not signed in", signIn: "kept" }
        const md = auth.metadata ?? {}
        const { out, read } = await usage(await liveKey(auth), md.plan ? { id: md.planId ?? "", plan: md.plan } : undefined)
        // the plan read is saved with the key, so a start doesn't wait on
        // billing/subscriptions again, for the models, Go's endpoint or this
        if (read && (read.plan !== md.plan || read.id !== (md.planId ?? "")) && client?.auth?.set) {
          try {
            await client.auth.set({ path: { id: ID }, body: { ...auth, metadata: { ...md, plan: read.plan, planId: read.id } } })
          } catch {}
        }
        return { ...out, signIn: "kept" }
      },
    },
    async config(config) {
      config.provider ??= {}
      const was = config.provider[ID] ?? {}
      config.provider[ID] = {
        name: "Command Code Plan",
        npm: CHAT,
        api: BASE,
        ...was,
        models: { ...Object.fromEntries(MODELS.map((m) => [m.id, configModel(m)])), ...(was.models ?? {}) },
      }
    },
    // the plan's list: the Provider API's, with what each model is served
    // on, less what the plan refuses (PLAN_MODELS); Go's key has no
    // Provider API: its list is the same one, asked without it, less what
    // Go is refused, and the CLI's table when that can't be had. A plan
    // not read (nothing saved, billing/subscriptions failing) keeps the
    // whole list, and so does an account with credits beyond its plan's.
    provider: {
      id: ID,
      async models(provider, { auth } = {}) {
        if (auth?.type !== "api" || !auth.key) return provider.models
        const key = await liveKey(auth)
        const [plan, extra] = await Promise.all([planNow(key, auth.metadata?.plan), extraNow(key)])
        const keep = (id) => extra || !refuses(plan, id)
        if (plan === "Go") {
          let ms = GO_MODELS
          let fell = false
          try {
            ms = await goModels(extra)
          } catch {
            fell = true
          }
          const out = Object.fromEntries(ms.map((m) => [m.id, runtimeModel({ ...m, npm: CHAT })]))
          // the CLI's table stands in: magpie keeps the list it was told
          // last, as the built-in keeps the one it fetched last
          if (fell) out[FELL_BACK] = true
          return out
        }
        try {
          const out = Object.fromEntries((await liveModels(key)).filter((m) => keep(m.id)).map((m) => [m.id, runtimeModel(m)]))
          if (Object.keys(out).length) return out
        } catch {}
        // the default list stands in, less what the plan refuses
        const out = Object.fromEntries(Object.entries(provider.models ?? {}).filter(([id]) => keep(id)))
        return Object.assign(out, { [FELL_BACK]: true })
      },
    },
  }
}
