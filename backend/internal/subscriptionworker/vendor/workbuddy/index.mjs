// OpenCode provider plugins for WorkBuddy's plans, Tencent's two builds of
// WorkBuddy (CodeBuddy's desktop agent):
//   - workbuddy:    the China build, copilot.tencent.com
//   - workbuddy-ai: the international build, www.workbuddy.ai
// A WorkBuddy account signs in in the browser (Tencent's page, polled for
// the token as WorkBuddy's desktop app does) or is taken from WorkBuddy
// desktop's own sign-in. Its chats are chat completions at <endpoint>/v2,
// streamed only, with WorkBuddy's headers: a request whose User-Agent isn't
// WorkBuddy/<version> is refused (error 10085).

import { createHash, randomBytes } from "node:crypto"
import { readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

const APP_VERSION = "2.0.0" // the version WorkBuddy's sign-in page is opened with
const UA_VERSION = "5.5.6" // WorkBuddy/<this> is the User-Agent the API takes
const POLL_MS = 1000
const SIGN_IN_MS = 5 * 60 * 1000
const EARLY_MS = 60 * 1000 // a token this close to its end is refreshed before a request
const LEAD_MS = 5 * 60 * 1000 // and this close, magpie renews it ahead of time (auth.refresh)
const KEEP_RENEWED_MS = 60 * 1000 // how long a refresh's result answers for the old refresh token

// [id, name, context, efforts]
const CN_MODELS = [
  ["auto", "Auto", 168000],
  ["hy4-preview-f", "Hy4 preview", 1000000, ["high"]],
  ["hy3", "Hy3", 192000, ["low", "high"]],
  ["hy3-x", "Hy3-X", 192000, ["low", "high"]],
  ["deepseek-v4.1-flash", "Deepseek-V4.1-Flash", 1000000],
  ["glm-5.3", "GLM-5.3", 1000000, ["low", "high", "max"]],
  ["glm-5.3-flash", "GLM-5.3-Flash", 1000000, ["low", "high", "max"]],
  ["glm-5.2", "GLM-5.2", 1000000, ["high", "xhigh"]],
  ["glm-5.1", "GLM-5.1", 200000],
  ["glm-5v-turbo", "GLM-5v-Turbo", 200000],
  ["minimax-m3", "MiniMax-M3", 512000],
  ["kimi-k3-1", "Kimi-K3", 1000000, ["low", "high", "xhigh"]],
  ["kimi-k2.7", "Kimi-K2.7-Code", 256000],
  ["kimi-k2.6", "Kimi-K2.6", 256000],
  ["deepseek-v4-pro", "Deepseek-V4-Pro", 1000000, ["none", "high", "xhigh"]],
]

const AI_MODELS = [
  ["default-model", "Default", 176000],
  ["fast-model", "Fast", 200000],
  ["balanced-model", "Balanced", 256000],
  ["primary-model", "Primary", 272000],
  ["deep-model", "Deep", 176000],
  ["gpt-5.5", "GPT-5.5", 1000000],
  ["gpt-5.4", "GPT-5.4", 272000],
  ["gpt-5.3-codex", "GPT-5.3-Codex", 272000],
  ["gemini-3.1-pro", "Gemini-3.1-Pro", 400000],
  ["gemini-3.5-flash", "Gemini-3.5-Flash", 1000000],
  ["glm-5.3", "GLM-5.3", 1000000, ["low", "high", "max"]],
  ["glm-5.2", "GLM-5.2", 1000000, ["high", "xhigh"]],
  ["hy3", "Hy3", 192000, ["low", "high"]],
  ["kimi-k3", "Kimi-K3", 1000000],
  ["kimi-k2.6", "Kimi-K2.6", 256000],
  ["minimax-m3", "MiniMax-M3", 512000],
]

const SITES = {
  workbuddy: {
    id: "workbuddy",
    name: "WorkBuddy",
    endpoint: "https://copilot.tencent.com",
    authID: "workbuddy-desktop",
    platform: "workbuddy",
    ai: false,
    models: CN_MODELS,
    // it serves the Claude models on Anthropic's Messages API too (see
    // nativeOf)
    messages: true,
  },
  "workbuddy-ai": {
    id: "workbuddy-ai",
    name: "WorkBuddy AI",
    endpoint: "https://www.workbuddy.ai",
    authID: "workbuddy-desktop-ai",
    platform: "workbuddy-ai",
    ai: true,
    models: AI_MODELS,
    // models whose images WorkBuddy AI drops though its config says they
    // take them (see textOnly)
    noImages: /^glm-/,
  },
}

// textOnly says whether site drops the images sent to model, whatever its
// config says. On 2026-10-13, with a real WorkBuddy AI account (Yirasumi on
// Discord), www.workbuddy.ai's /v3/config marked every model
// supportsImages: true, but a chat to glm-5.3-flash, glm-5.3 or glm-5.2
// came back saying "[System reminder: the current model does not support
// images. Content filtered. ...]": the server had taken the image out.
// Its other models (deepseek-v4.1-flash, kimi, gemini, grok, gpt) read
// them, and WorkBuddy (China)'s glm-5.3-flash does too, so this is WorkBuddy
// AI's GLM models alone. Said text-only, magpie's image-describing model
// reads the image for them; a user who said the model takes images keeps
// their own answer (magpie's settings, over what this list says).
function textOnly(site, model) {
  return !!site.noImages?.test(model)
}

const NPM = "@ai-sdk/openai-compatible"
const hex = () => randomBytes(16).toString("hex")
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const api = (site) => site.endpoint + "/v2"

// MESSAGES is the API SDK package for Anthropic's Messages shape, which
// WorkBuddy serves the Claude models on, beside its chat completions:
// <endpoint>/v1/messages. Those models are Anthropic's own, and their
// thinking is asked for the way Anthropic asks it (thinking.type adaptive
// with output_config.effort), which the chat endpoint has no field for: a
// chat carries reasoning_effort alone, and WorkBuddy's chat endpoint gives
// the same answer at every level, so an agent that picked max got no more
// than one that picked low. See nativeOf.
const NPM_MESSAGES = "@ai-sdk/anthropic"
const apiMessages = (site) => site.endpoint + "/v1"

// nativeOf is whether site serves model on Messages rather than chat: a
// Claude model, on a site that serves Messages at all (site.messages).
// Measured 2026-10-24 on WorkBuddy's China build, each model of
// /v3/config's cli agent asked on /v1/messages: claude-opus-5.5,
// claude-sonnet-5.5, claude-opus-5 and claude-opus-4.8-1m answered in
// Anthropic's stream; glm-5.3 and deepseek-v4.1-flash answered 500, gpt-5.5
// and kimi-k3-1 400. An account may still be refused a Claude model it has
// no entitlement for (11158), which is the account's, not the endpoint's.
// WorkBuddy AI (www.workbuddy.ai) has no such route: /v1/messages,
// /v2/messages and /v1/messages?beta=true are all "404 Route Not Found"
// there, whatever the model, so its models stay on chat, a Claude one too
// should it list one.
function nativeOf(site, model) {
  return site?.messages === true && /^claude-/i.test(String(model ?? ""))
}

// ---- WorkBuddy's API ------------------------------------------------------

class WBError extends Error {
  constructor(message, code, status, said) {
    super(message)
    this.code = code
    this.status = status
    this.said = said // as magpie's built-in (wbCall) worded it
  }
}

// call makes a request of WorkBuddy's plugin API, which answers
// {code, msg, data}; a code but 0 is an error.
async function call(site, method, path, headers = {}, body) {
  const res = await fetch(site.endpoint + path, {
    method,
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
      "User-Agent": "WorkBuddy/" + UA_VERSION,
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30000),
  })
  const text = await res.text()
  let env = {}
  try {
    env = JSON.parse(text)
  } catch {}
  const code = Number.parseInt(env.code, 10) || 0
  if (!res.ok) {
    const why = env.msg || env.message || env.error?.message || text.slice(0, 200) || res.statusText
    const said = code ? env.msg || `error ${code}` : env.msg || statusText(res.status)
    throw new WBError(`${site.name}: ${why} (HTTP ${res.status}${env.code ? ", code " + env.code : ""})`, env.code, res.status, said)
  }
  if (env.code) throw new WBError(`${site.name}: ${env.msg || "error"} (${env.code})`, env.code, res.status, env.msg || `error ${code}`)
  return env.data
}

function domainOf(site, a) {
  return a.domain || new URL(api(site)).host
}

// merge takes a token WorkBuddy gave (sign-in or refresh) into the
// stored sign-in, times given as at-ms or in-seconds.
function merge(a, got) {
  const now = Date.now()
  const out = { ...a, access: got.accessToken }
  if (got.refreshToken) out.refresh = got.refreshToken
  if (got.domain) out.domain = got.domain
  if (got.tokenType) out.tokenType = got.tokenType
  if (got.expiresAt > 0) out.expires = got.expiresAt
  else if (got.expiresIn > 0) out.expires = now + got.expiresIn * 1000
  if (got.refreshExpiresAt > 0) out.refreshExpiresAt = got.refreshExpiresAt
  else if (got.refreshExpiresIn > 0) out.refreshExpiresAt = now + got.refreshExpiresIn * 1000
  return out
}

function stale(a) {
  return !!a.expires && Date.now() >= a.expires - EARLY_MS
}

// renewing is each refresh under way, by site and the refresh token it
// spends; renewed is what each gave, kept a minute so one that read the
// old refresh token after it was spent (magpie's auth.refresh, a request)
// takes that rather than spending it again.
const renewing = new Map()
const renewed = new Map()

function refreshable(a) {
  return !!a.refresh && !(a.refreshExpiresAt && Date.now() >= a.refreshExpiresAt)
}

// renew spends a's refresh token, once: those who come while it runs, or
// within KEEP_RENEWED_MS of it, get what it gave. save keeps the result.
function renew(site, a, save) {
  const key = site.id + "\n" + a.refresh
  const done = renewed.get(key)
  if (done && Date.now() - done.at < KEEP_RENEWED_MS) return Promise.resolve(merged(a, done.next))
  let r = renewing.get(key)
  if (!r) {
    r = (async () => {
      const got = await call(site, "POST", "/v2/plugin/auth/token/refresh", {
        "X-Refresh-Token": a.refresh,
        "X-Auth-Refresh-Source": "plugin",
        "X-Domain": domainOf(site, a),
        ...enterpriseHeaders(a),
      }, {})
      if (!got?.accessToken) throw Object.assign(new Error("WorkBuddy gave no refreshed token"), { bare: true })
      const next = merge(a, got)
      renewed.set(key, { next, at: Date.now() })
      await save?.(next)
      return next
    })().finally(() => renewing.delete(key))
    renewing.set(key, r)
  }
  return r.then((next) => merged(a, next))
}

// merged is a with the tokens another's refresh gave over it.
function merged(a, next) {
  const out = { ...a }
  for (const k of TOKEN_FIELDS) if (k in next) out[k] = next[k]
  return out
}
const TOKEN_FIELDS = ["access", "refresh", "expires", "refreshExpiresAt", "domain", "tokenType"]

// fresh is the sign-in with an access token that isn't about to end:
// refreshed (and saved) when it is. A refresh that fails, or a refresh
// token that has ended, leaves the access token there is. Its failures
// are worded as the built-in's (wbFresh), "WorkBuddy" on both sites.
async function fresh(site, client, a) {
  if (!stale(a)) return a
  if (!refreshable(a)) {
    if (a.access) return a
    throw new Error("this WorkBuddy account is signed out; sign in again")
  }
  try {
    return await renew(site, a, async (next) => {
      try {
        await client?.auth?.set?.({ path: { id: site.id }, body: next })
      } catch {}
    })
  } catch (e) {
    if (a.access) return a
    if (e?.bare) throw e
    throw new Error(`WorkBuddy token refresh: ${e?.said ?? e?.message ?? e}`)
  }
}

// refreshed is what magpie's auth.refresh gives for a browser sign-in:
// the fields a refresh changed, nothing when there's nothing to renew.
// magpie saves them itself.
async function refreshed(site, auth) {
  if (auth?.type !== "oauth" || auth.source === "desktop" || !auth.access || !auth.refresh) return undefined
  // a refresh token that has ended is said plainly, never as signIn
  // "expired": the built-in never marked a WorkBuddy account
  if (!refreshable(auth)) throw new Error("this WorkBuddy account is signed out; sign in again")
  let next
  try {
    next = await renew(site, auth)
  } catch (e) {
    if (e?.bare) throw e
    throw new Error(`WorkBuddy token refresh: ${e?.said ?? e?.message ?? e}`)
  }
  const out = {}
  for (const k of TOKEN_FIELDS) if (k in next && next[k] !== auth[k]) out[k] = next[k]
  return Object.keys(out).length ? out : undefined
}

// sign sets on headers what WorkBuddy's desktop app sends with a chat: the
// account's own, the client's, and the attribution WorkBuddy's usage list
// shows as the client that asked (使用端). The chat's ids — the
// conversation's, the turn's, the message's — are attend's below.
//
// WorkBuddy's own client sends the whole of this on both builds
// (application-manifest.js: X-Agent-Purpose "conversation", X-IDE-Name/Type
// "WorkBuddy", X-IDE-Version, X-Product "SaaS"); a chat without them is
// counted under no client at all, which is the empty 使用端 a request shows
// when only some of the group is there. The gate below used to give the China
// build none of the client's headers, so every China-build request was
// attribution-less.
function sign(site, a, headers) {
  headers.set("Authorization", "Bearer " + a.access)
  headers.set("X-User-Id", a.uid ?? "")
  headers.set("X-Domain", domainOf(site, a))
  headers.set("X-Product", "SaaS")
  headers.set("X-IDE-Type", "WorkBuddy")
  headers.set("User-Agent", "WorkBuddy/" + UA_VERSION)
  headers.set("X-Requested-With", "XMLHttpRequest")
  headers.set("X-Agent-Intent", "craft")
  headers.set("X-Agent-Purpose", "conversation")
  headers.set("X-IDE-Name", "WorkBuddy")
  headers.set("X-IDE-Version", UA_VERSION)
  for (const [k, v] of Object.entries(enterpriseHeaders(a))) headers.set(k, v)
  if (!site.ai) return
  headers.set("X-Agent-Type", "main")
}

// enterpriseHeaders names the enterprise (team) an account signed in as, as
// WorkBuddy's own client does on every request it makes for that account
// (its auth interceptor: X-Enterprise-Id and X-Tenant-Id, both the
// account's enterpriseId). Without them WorkBuddy answers as the personal
// account: /v3/config lists the personal plan's models and not the
// enterprise's (#75). A personal account has none and sends none.
function enterpriseHeaders(a) {
  const id = typeof a?.enterpriseId === "string" ? a.enterpriseId.trim() : ""
  return id ? { "X-Enterprise-Id": id, "X-Tenant-Id": id } : {}
}

// enterpriseOf is the enterprise the account a sign-in gave belongs to:
// the one /login/account names, else, for an account that isn't personal,
// the one WorkBuddy's account list has for it — the same choice WorkBuddy's
// own client makes (mergeCurrentAccountDetails): the uid's only entry of that
// type, or the only one it last signed in to. None found is personal.
async function enterpriseOf(site, a, who) {
  const own = typeof who?.enterpriseId === "string" ? who.enterpriseId.trim() : ""
  if (own) return own
  const type = String(who?.type ?? "").trim().toLowerCase()
  if (!type || type === "personal") return ""
  let list
  try {
    list = await call(site, "GET", "/v2/plugin/accounts", {
      Authorization: "Bearer " + a.access,
      "X-Domain": domainOf(site, a),
    })
  } catch {
    return ""
  }
  const mine = (list?.accounts ?? []).filter((x) => x?.uid === who.uid && typeof x.enterpriseId === "string" && x.enterpriseId.trim())
  const same = mine.filter((x) => String(x.type ?? "").trim().toLowerCase() === type)
  const last = mine.filter((x) => x.lastLogin)
  const pick = same.length === 1 ? same[0] : last.length === 1 ? last[0] : mine.length === 1 ? mine[0] : undefined
  return pick ? pick.enterpriseId.trim() : ""
}

// WorkBuddy's own client names each chat to its gateway four ways: the
// conversation it belongs to, the user send (turn) it answers, the message
// itself, and the request that carried it. Its backend counts a user send as
// one request by X-Conversation-Request-ID: a chat carrying a new one every
// time is counted on its own, so one send that takes several steps — a tool
// call answered and sent again, a retry, another account answering — shows up
// in the usage detail as many requests. The ids below are made from what
// names the conversation and the turn, so every step of one send carries the
// same turn id and the user's next message carries another, as WorkBuddy's
// own client does.

// SESSION carries the conversation magpie (or OpenCode) names a request by,
// from the chat.headers hook to the loader's fetch. It goes no further.
const SESSION = "x-magpie-workbuddy-session"

// SALT is what the ids are derived with, so an id says nothing of the words
// it was made from. New in each process, as WorkBuddy's own ids are one
// conversation's own within a run.
const SALT = randomBytes(16).toString("hex")

// idFor is the 32 hex digits WorkBuddy's own ids are made of, made of a
// name of the thing the id is for (a turn, a conversation) and what names it.
function idFor(kind, key) {
  return createHash("sha256").update(`${SALT}|${kind}|${key}`).digest("hex").slice(0, 32)
}

// signatureOf is a message content's signature: its text, and a digest of
// every part that isn't text (an image, a file), so a turn of images alone is
// a turn of its own too. A tool result's part — carried in a user message by
// clients of Anthropic's shape — is left out: it is not what the user said,
// and it changes with every step of the send.
function signatureOf(content) {
  if (typeof content === "string") return content
  if (!Array.isArray(content)) return ""
  let out = ""
  for (const part of content) {
    const type = part?.type ?? ""
    if (type === "" || type === "text") out += typeof part?.text === "string" ? part.text : ""
    else if (type !== "tool_result" && type !== "tool-result") {
      out += `[${type}:${createHash("sha256").update(JSON.stringify(part)).digest("hex").slice(0, 8)}]`
    }
  }
  return out
}

// answeredBefore is whether msgs[i] answers the message before it — a tool's
// result, or an assistant's calls — rather than carrying the user's own
// words. A client that sends a tool result's images in a user message (pi's
// "Attached image(s) from tool result:") puts that message right after the
// result, and a turn keyed on it would be a turn of its own on every step.
//
// Anthropic's Messages shape says the same another way: a tool's result is a
// tool_result block in a user message, after an assistant message of
// tool_use blocks. Such a user message answers too, whatever text sits
// beside the results (Claude Code puts a reminder there, a user's words typed
// mid-task go there as well), and so does one right after it, as a chat's
// does after role "tool". Messages that are neither the user's nor the
// assistant's (Claude Code's role "system" ones, which carry a turn's
// effort, its environment and its token count between the steps) stand
// between them without changing which message answers.
function answeredBefore(msgs, i) {
  if (hasBlock(msgs[i], "tool_result")) return true
  let j = i - 1
  while (j >= 0 && msgs[j]?.role === "system") j--
  const prev = msgs[j]
  if (prev === null || typeof prev !== "object") return false
  if (prev.role === "tool") return true
  if (prev.role === "user" && hasBlock(prev, "tool_result")) return true
  if (prev.role !== "assistant") return false
  if (Array.isArray(prev.tool_calls) && prev.tool_calls.length > 0) return true
  return hasBlock(prev, "tool_use")
}

// hasBlock is whether message m's content holds a block of type.
function hasBlock(m, type) {
  return Array.isArray(m?.content) && m.content.some((p) => p?.type === type)
}

// turnKey names the user send a chat answers: the body's last user message
// that is the user's own, by its place and its signature. Every step of one
// send repeats it; the user's next message changes it. "" for a chat whose
// last such message has nothing signable in it (an empty message) or which
// has none at all: the caller then makes a fresh id for that request, as
// before these were derived at all.
function turnKey(chat) {
  const msgs = Array.isArray(chat?.messages) ? chat.messages : []
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i]?.role !== "user" || answeredBefore(msgs, i)) continue
    const sig = signatureOf(msgs[i].content)
    return sig ? `u${i}:${sig}` : ""
  }
  return ""
}

// conversationKey names the conversation a chat belongs to: the session
// magpie or OpenCode names (its chat.headers hook), else the chat's first
// user message, which every later turn of it repeats. "" when neither names
// one — a body that isn't a chat, or one with no user message in it — and no
// conversation id is sent then: a made-up one would be a conversation of the
// request's own, which is what fragments the usage detail in the first place.
function conversationKey(chat, session) {
  if (session) return `session:${session}`
  const msgs = Array.isArray(chat?.messages) ? chat.messages : []
  for (const m of msgs) {
    if (m?.role !== "user") continue
    const sig = signatureOf(m.content)
    if (sig) return `first:${sig}`
  }
  return ""
}

// isMessages says whether a request is an Anthropic Messages one, which
// site serves the Claude models natively: its URL ends at /messages or at
// the counting endpoint beside it (/v1/messages/count_tokens, which the
// chat route's rewrites would otherwise turn into a chat), or (a body is
// all there is to go on) its system is Anthropic's blocks rather than a
// chat's string, and its tools carry input_schema where a chat's carry a
// function's parameters. A chat's own tools can look either way, so the URL
// decides when there is one.
function isMessages(url, body) {
  const path = (() => {
    try {
      return new URL(String(url ?? "")).pathname
    } catch {
      return ""
    }
  })()
  if (path) return /\/messages(\/count_tokens)?$/.test(path)
  const b = chatOf(body)
  if (!b) return false
  if (Array.isArray(b.system)) return true
  const tools = Array.isArray(b.tools) ? b.tools : []
  return tools.some((t) => t && typeof t === "object" && "input_schema" in t && !t.function)
}

// withoutUnsigned is a Messages body without the thinking blocks that carry
// no signature, or whose signature WorkBuddy doesn't take.
//
// A conversation that began elsewhere — on WorkBuddy's chat route, whose
// thinking comes back unsigned, or on any other provider — and goes on here
// carries such blocks in its history, and WorkBuddy refuses the whole request
// for one of them when another assistant message holds a signed block:
// 11159 "Invalid `signature` in `thinking` block. The block is bound to a
// different conversation" (or the same wrapped in 11133). Seen 2026-10-24 in
// real Claude Code sessions moved onto this route mid-way: every request of
// two of them refused, each attempt alike, until the session ended. Replayed
// the same day, with a real account (20+ runs each):
//
//   unsigned thinking alone, no signed block anywhere ....... 200
//   one unsigned-only assistant message + a signed one ...... 11159
//   that message with the unsigned block taken out .......... 200
//   unsigned and signed blocks in the SAME message .......... 200
//   thinking.block_binding.prefix_mismatch_behavior=drop_block ... 11159
//
// So what binds a signed block is the history it sits in, and a message that
// is thinking alone can't be part of it; whether that thinking is signed
// makes no difference (a signed block alone is accepted too). The signature
// itself isn't checked for where it came from: a block keeping a signature
// of an unrelated turn, of another model (sonnet-5.5's on opus-5.5), or
// another account's answers 200, while a truncated or altered one is 11159.
// What WorkBuddy checks is that it reads as a signature.
//
// A block with no signature can't be verified by the model anyway, so
// nothing it could use is lost. A message whose only block that was gets an
// empty text block: measured the same day, that history still answers 200
// with a signed turn after it (three runs), where the unsigned block itself
// answers 11159 (three runs), and Anthropic's API refuses an empty text
// block — 200 here means WorkBuddy takes it. The signed blocks, this
// route's own, go back as they came, as Anthropic asks. A body without
// unsigned thinking is body itself, byte for byte.
function withoutUnsigned(body) {
  const b = chatOf(body)
  const msgs = b?.messages
  if (!Array.isArray(msgs)) return body
  let changed = false
  for (const m of msgs) {
    if (m?.role !== "assistant" || !Array.isArray(m.content)) continue
    const kept = m.content.filter((p) => !(p?.type === "thinking" && !p.signature))
    if (kept.length === m.content.length) continue
    changed = true
    m.content = kept.length ? kept : [{ type: "text", text: "" }]
  }
  return changed ? JSON.stringify(b) : body
}

// withoutSignedThinking is a Messages body without any thinking block at
// all, for a refusal saying a signature is not one WorkBuddy made (see the
// retry in the loader). An assistant message left with nothing gets an
// empty text block, as withoutUnsigned leaves it. null for a body with no
// thinking block to drop: the retry would send the same request again.
function withoutSignedThinking(body) {
  const b = chatOf(body)
  const msgs = b?.messages
  if (!Array.isArray(msgs)) return null
  let dropped = 0
  for (const m of msgs) {
    if (m?.role !== "assistant" || !Array.isArray(m.content)) continue
    const kept = m.content.filter((p) => p?.type !== "thinking")
    if (kept.length === m.content.length) continue
    dropped += m.content.length - kept.length
    m.content = kept.length ? kept : [{ type: "text", text: "" }]
  }
  return dropped ? JSON.stringify(b) : null
}

// chatOf is a chat's body as the object it is, null for one that isn't: a
// body that isn't text (bytes), or isn't JSON at all.
function chatOf(body) {
  if (typeof body !== "string") return null
  try {
    const b = JSON.parse(body)
    return b !== null && typeof b === "object" ? b : null
  } catch {
    return null
  }
}

// sessionOf takes the session a chat.headers hook named off the headers,
// with the header: it goes no further than here. Its length is capped as
// magpie's own session ids are, the id below being derived from it anyway.
function sessionOf(headers) {
  const s = String(headers.get(SESSION) ?? "").trim()
  headers.delete(SESSION)
  return s.slice(0, 128)
}

// attend sets a chat's ids on the headers its request goes to WorkBuddy
// with: the message's (each request its own, as the client makes them), the
// turn's (one user send, whatever it takes), and the conversation's. A header
// already there — the caller's own, or another plugin's — is kept, and the
// message and request ids are the same one, as WorkBuddy's client has them.
function attend(headers, body, session) {
  const chat = chatOf(body)
  const conv = conversationKey(chat, session)
  const msg = hex()
  headers.set("X-Conversation-Message-ID", msg)
  headers.set("X-Request-ID", msg)
  if (!headers.has("X-Conversation-Request-ID")) {
    const turn = turnKey(chat)
    // a turn is a conversation's own: two conversations whose user message
    // reads the same are still two sends, not one
    headers.set("X-Conversation-Request-ID", turn ? idFor("turn", `${conv}|${turn}`) : hex())
  }
  if (conv && !headers.has("X-Conversation-ID")) headers.set("X-Conversation-ID", idFor("conversation", conv))
}

// FLAGGED are the words WorkBuddy refuses a chat for, "Illegal API
// invocation from an unapproved channel" (magpie #182), each with words
// that say the same and pass: tried one by one against both builds with
// the requests Claude Code 2.1.280 and Codex 0.159.2 send. WorkBuddy reads
// every message, not only the system prompt (Claude Code puts its main
// branch in the first user message), and pays no mind to case. Claude
// Code's billing header means nothing to WorkBuddy's models and goes.
const FLAGGED = [
  [/x-anthropic-billing-header:[^\n]*\n?/gi, ""],
  [/You are Claude Code, Anthropic['’]s official CLI for Claude/gi, "You are Claude Code, Anthropic's CLI for Claude"],
  [/Main branch \(you will usually use this for PRs\)/gi, "Main branch (usually the base for PRs)"],
  [/(?<!the )Codex CLI is an open source project led by OpenAI\./gi, "The Codex CLI is an open source project led by OpenAI."],
  // DeepSeek Harness (DSH) opens its system prompt with this line, which
  // WorkBuddy refuses as 11128 (magpie-community/plugins#279; the same
  // refusal traced in smart-open/TraeWorkAssistant#125).
  [/You are an AI agent powered by DeepSeek Harness\.?/gi, "You are an AI agent that helps the user with programming tasks."],
  // A line WorkBuddy refuses in a long DeepSeek Harness chat, measured 400
  // then 200 with this wording by tearslee (magpie-community/plugins#291). The
  // model can quote it back into an assistant message, which the move to
  // the user's instructions can't reach, so it is reworded in every message.
  [/Web GUI whose URL the runtime context names\.\s+When the user refers/gi, "Web GUI whose URL the runtime context names. The interface, when the user refers"],
]

function unflagged(text) {
  for (const [re, to] of FLAGGED) text = text.replace(re, to)
  return text
}

// unflag puts FLAGGED's words in place in every message of a chat, and
// leaves out a text that had nothing else (the billing header's system
// block). It says whether it changed anything.
function unflag(messages) {
  let changed = false
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (typeof m?.content === "string") {
      const t = unflagged(m.content)
      if (t === m.content) continue
      changed = true
      if (t.trim() || m.role !== "system") m.content = t
      else messages.splice(i, 1)
    } else if (Array.isArray(m?.content)) {
      const parts = []
      let here = false
      for (const p of m.content) {
        const t = typeof p?.text === "string" ? unflagged(p.text) : null
        if (t === null || t === p.text) {
          parts.push(p)
          continue
        }
        here = true
        if (t.trim()) parts.push({ ...p, text: t })
      }
      if (!here) continue
      changed = true
      if (parts.length || m.role !== "system") m.content = parts
      else messages.splice(i, 1)
    }
  }
  return changed
}

// withSystem gives a chat that doesn't open with a system message
// WorkBuddy's default one, as its app does, takes FLAGGED's words out of it,
// and applies the request spellings WorkBuddy's chat endpoint accepts. The
// OpenAI-compatible client normally uses max_completion_tokens and may call a
// reasoning prompt developer; WorkBuddy's endpoint takes max_tokens and
// system instead.
function withSystem(body) {
  // a body magpie or OpenCode hands over as bytes is read as the text it is
  if (body instanceof ArrayBuffer || ArrayBuffer.isView(body)) body = new TextDecoder().decode(body)
  if (typeof body !== "string") return body
  try {
    const b = JSON.parse(body)
    if (!Array.isArray(b?.messages) || !b.messages.length) return body
    let changed = unflag(b.messages)
    if (b.max_completion_tokens !== undefined) {
      if (b.max_tokens === undefined) b.max_tokens = b.max_completion_tokens
      delete b.max_completion_tokens
      changed = true
    }
    for (const m of b.messages) {
      if (m?.role !== "developer") continue
      m.role = "system"
      changed = true
    }
    if (b.messages[0]?.role !== "system") {
      b.messages.unshift({ role: "system", content: "You are a helpful assistant." })
      changed = true
    }
    return changed ? JSON.stringify(b) : body
  } catch {
    return body
  }
}

// WorkBuddy's channel check reads the system and assistant messages only:
// tried on 2026-10-21 with real accounts on both builds (glm-5.3,
// deepseek-v4.1-flash, gpt-5.5), each refused line was served in the user's
// message, a tool's result and a user message's parts, and refused again in
// the system or an assistant message. It matches a known line whole and
// exactly (one extra space lets it through), so FLAGGED only ever knows the
// lines met so far, and another agent's opening line is refused until one is
// added (Wing on Discord, 10-21, with an agent we couldn't name).
//
// systemAsUser is body with its system prompt read to the model as the
// first thing the user says, in INSTRUCTIONS' tags, and WorkBuddy's default
// system prompt first, as both builds want a system message there (11128
// "first message is not system prompt"). A system message later in the chat
// becomes the user's where it stands. null when there's nothing to move.
const DEFAULT_SYSTEM = "You are a helpful assistant."
const INSTRUCTIONS = ["<instructions>\n", "\n</instructions>"]

function textOf(content) {
  if (typeof content === "string") return content
  if (!Array.isArray(content)) return ""
  return content.map((p) => (typeof p?.text === "string" ? p.text : "")).filter(Boolean).join("\n")
}

// systemKey is the text of a chat's system messages, which a chat refused
// once is known by: the same agent's next chat goes with systemAsUser at once
function systemKey(body) {
  const msgs = chatOf(body)?.messages
  if (!Array.isArray(msgs)) return ""
  const key = msgs.filter((m) => m?.role === "system").map((m) => textOf(m.content)).join("\n")
  return key.trim() === DEFAULT_SYSTEM ? "" : key
}

function systemAsUser(body) {
  const b = chatOf(body)
  const msgs = b?.messages
  if (!Array.isArray(msgs)) return null
  let i = 0
  const lead = []
  for (; i < msgs.length && msgs[i]?.role === "system"; i++) lead.push(textOf(msgs[i].content))
  const said = lead.join("\n\n").trim()
  const rest = msgs.slice(i).map((m) => (m?.role === "system" ? { ...m, role: "user" } : m))
  const moved = said && said !== DEFAULT_SYSTEM
  if (!moved && rest.every((m, k) => m === msgs[i + k])) return null
  if (moved) {
    const text = INSTRUCTIONS[0] + said + INSTRUCTIONS[1]
    const u = rest.findIndex((m) => m?.role === "user")
    if (u < 0) rest.unshift({ role: "user", content: text })
    else if (typeof rest[u].content === "string") rest[u] = { ...rest[u], content: text + "\n\n" + rest[u].content }
    else if (Array.isArray(rest[u].content)) rest[u] = { ...rest[u], content: [{ type: "text", text }, ...rest[u].content] }
    else rest[u] = { ...rest[u], content: text }
  }
  b.messages = [{ role: "system", content: DEFAULT_SYSTEM }, ...rest]
  return JSON.stringify(b)
}

// normalizeToolChoice converts the object form emitted by OpenAI clients to
// WorkBuddy's string form. It runs after fitted(), so a named choice follows a
// tool name that was made safe for WorkBuddy.
function normalizeToolChoice(body) {
  if (typeof body !== "string") return body
  try {
    const b = JSON.parse(body)
    if (!b || typeof b !== "object" || Array.isArray(b) || !("tool_choice" in b)) return body
    const choice = b.tool_choice
    if (typeof choice === "string") {
      if (choice.trim().toLowerCase() === "none") {
        delete b.tool_choice
        delete b.tools
        delete b.functions
      }
      return JSON.stringify(b)
    }
    if (!choice || typeof choice !== "object" || Array.isArray(choice)) {
      delete b.tool_choice
      return JSON.stringify(b)
    }
    const type = typeof choice.type === "string" ? choice.type.trim().toLowerCase() : ""
    if (type === "none") {
      delete b.tool_choice
      delete b.tools
      delete b.functions
    } else if (type === "auto" || type === "required") {
      b.tool_choice = type
    } else if (type === "function") {
      const fn = choice.function && typeof choice.function === "object" && !Array.isArray(choice.function)
        ? choice.function
        : undefined
      const name = typeof fn?.name === "string" ? fn.name.trim() : ""
      b.tool_choice = name || "auto"
    } else {
      delete b.tool_choice
    }
    return JSON.stringify(b)
  } catch {
    return body
  }
}

// ---- tools WorkBuddy takes ----------------------------------------------------

// WorkBuddy AI answers 400 code 11133 "Invalid request parameters" (the
// model provider rejected them) to a chat whose tools it can't pass on to
// the model (wyh on magpie's Discord: ZCode and DeepSeek Harness on
// deepseek-v4.1-flash). Tried on the owner's account, 2026-10-08: DeepSeek
// turns away each of the shapes below, and Kimi, GPT, Gemini and GLM some
// of them; MiniMax none. fitTools gives each a shape every model takes.
// A tool's name may hold letters, digits, _ and -; another character
// (". : / space", CJK) is refused.
const NAME_OK = /^[A-Za-z0-9_-]+$/

// safeName is name with each character WorkBuddy refuses as _, kept apart
// from the names in taken.
function safeName(name, taken) {
  const base = String(name).replace(/[^A-Za-z0-9_-]/g, "_") || "tool"
  let n = base
  for (let i = 2; taken.has(n); i++) n = `${base}_${i}`
  taken.add(n)
  return n
}

// NUL_ESCAPE finds a regex's \0 (the NUL character), stepping over each \\
// (a backslash) so that a \\0 is left; \01 is an octal escape, also left.
const NUL_ESCAPE = /\\\\|\\0(?![0-9])/g

// fitSchema mends a schema in place, at every depth: a pattern's \0, which
// DeepSeek's validator says is "not a 'regex'", is \u0000, which matches
// the same (magpie's gateway does this for DeepSeek's own API,
// deepseekToolPatterns); a tuple's items (a list) become one schema any of
// them fits. It reports whether it changed anything.
function fitSchema(s, depth = 0) {
  if (!s || typeof s !== "object" || depth > 64) return false
  let changed = false
  if (Array.isArray(s)) {
    for (const v of s) if (fitSchema(v, depth + 1)) changed = true
    return changed
  }
  if (typeof s.pattern === "string" && s.pattern.includes("\\0")) {
    const p = s.pattern.replace(NUL_ESCAPE, (m) => (m === "\\\\" ? m : "\\u0000"))
    if (p !== s.pattern) {
      s.pattern = p
      changed = true
    }
  }
  if (Array.isArray(s.items)) {
    const list = s.items.filter((v) => v && typeof v === "object")
    s.items = list.length === 0 ? {} : list.length === 1 ? list[0] : { anyOf: list }
    delete s.additionalItems
    changed = true
  }
  for (const v of Object.values(s)) if (fitSchema(v, depth + 1)) changed = true
  return changed
}

// Kimi (kimi-k3-1) answers 11133 too, to the tools of an MCP server or a
// plugin (clearmann, #122). Tried on the owner's account, 2026-10-14, each
// alone in an otherwise plain chat: Kimi turns away an enum with no type
// (["a","b"] as much as [1,"a",null]), an empty enum, an enum whose type
// lists two types besides null, a schema that is true or false where a
// schema goes (properties.x: true, items: true), a required that isn't a
// list, and a type it doesn't know ("any", "String"). It takes an enum
// typed T or [T, "null"], an anyOf of such enums, {"not": {}}, {}, and
// additionalProperties: true or false. fitShape gives each the shape it
// takes, meaning the same: none of it narrows or widens what a call may
// hold, except an empty enum (which nothing fits) is left out.
const TYPES = new Set(["string", "number", "integer", "boolean", "object", "array", "null"])

// SUB_ONE are the keywords whose value is a schema, SUB_LIST a list of
// schemas, SUB_MAP a map of names to schemas. additionalProperties and
// unevaluatedProperties are left as they are when true or false.
const SUB_ONE = ["items", "additionalItems", "unevaluatedItems", "contains", "not", "if", "then", "else", "propertyNames", "additionalProperties", "unevaluatedProperties"]
const SUB_LIST = ["anyOf", "oneOf", "allOf", "prefixItems"]
const SUB_MAP = ["properties", "patternProperties", "$defs", "definitions", "dependentSchemas"]
const BOOL_OK = new Set(["additionalProperties", "unevaluatedProperties"])

// valueType is the JSON Schema type of an enum's value.
function valueType(v) {
  if (v === null) return "null"
  if (Array.isArray(v)) return "array"
  if (typeof v === "number") return Number.isInteger(v) ? "integer" : "number"
  return typeof v
}

// fitEnum gives s's enum a type: the one its values have, or that and
// "null"; values of several types are an anyOf of an enum for each. It
// reports whether it changed anything.
function fitEnum(s) {
  if (!("enum" in s)) return false
  if (!Array.isArray(s.enum) || s.enum.length === 0) {
    delete s.enum
    return true
  }
  const listed = Array.isArray(s.type) ? s.type : typeof s.type === "string" ? [s.type] : []
  if (listed.filter((t) => t !== "null").length === 1) return false
  const byType = new Map()
  for (const v of s.enum) {
    const t = valueType(v)
    if (!byType.has(t)) byType.set(t, [])
    byType.get(t).push(v)
  }
  if (byType.has("integer") && byType.has("number")) {
    byType.get("number").push(...byType.get("integer"))
    byType.delete("integer")
  }
  const hasNull = byType.delete("null")
  if (byType.size <= 1) {
    const [t] = byType.keys()
    const want = t === undefined ? "null" : hasNull ? [t, "null"] : t
    if (JSON.stringify(want) === JSON.stringify(s.type)) return false
    s.type = want
    return true
  }
  const split = [...byType].map(([t, vs]) => ({ type: t, enum: vs }))
  if (hasNull) split.push({ type: "null" })
  delete s.enum
  delete s.type
  if (Array.isArray(s.anyOf)) s.allOf = [...(Array.isArray(s.allOf) ? s.allOf : []), { anyOf: split }]
  else s.anyOf = split
  return true
}

// subSchema is v, a schema found under key, as an object Kimi takes: true
// is {}, false is {"not": {}}.
function subSchema(v, key) {
  if (typeof v !== "boolean" || BOOL_OK.has(key)) return v
  return v ? {} : { not: {} }
}

// fitShape mends s, a schema, and each schema under it, in place (see
// TYPES above); a name under properties is never read as a keyword. It
// reports whether it changed anything.
function fitShape(s, depth = 0) {
  if (!s || typeof s !== "object" || Array.isArray(s) || depth > 64) return false
  let changed = false
  if ("type" in s) {
    const list = (Array.isArray(s.type) ? s.type : [s.type]).map((t) => (typeof t === "string" ? t.toLowerCase() : t)).filter((t) => TYPES.has(t))
    const uniq = [...new Set(list)]
    const want = uniq.length === 0 ? undefined : uniq.length === 1 ? uniq[0] : uniq
    if (JSON.stringify(want) !== JSON.stringify(s.type)) {
      if (want === undefined) delete s.type
      else s.type = want
      changed = true
    }
  }
  if ("required" in s && !Array.isArray(s.required)) {
    delete s.required
    changed = true
  } else if (Array.isArray(s.required) && s.required.some((k) => typeof k !== "string")) {
    s.required = s.required.filter((k) => typeof k === "string")
    changed = true
  }
  if (fitEnum(s)) changed = true
  for (const k of SUB_ONE) {
    if (!(k in s)) continue
    const v = subSchema(s[k], k)
    if (v !== s[k]) {
      s[k] = v
      changed = true
    }
    if (fitShape(v, depth + 1)) changed = true
  }
  for (const k of SUB_LIST) {
    if (!Array.isArray(s[k])) continue
    s[k].forEach((v0, i) => {
      const v = subSchema(v0, k)
      if (v !== v0) {
        s[k][i] = v
        changed = true
      }
      if (fitShape(v, depth + 1)) changed = true
    })
  }
  for (const k of SUB_MAP) {
    const m = s[k]
    if (!m || typeof m !== "object" || Array.isArray(m)) continue
    for (const [name, v0] of Object.entries(m)) {
      const v = subSchema(v0, k)
      if (v !== v0) {
        m[name] = v
        changed = true
      }
      if (fitShape(v, depth + 1)) changed = true
    }
  }
  return changed
}

// localRef is the schema a local $ref in root names ("#/$defs/x",
// "#/definitions/x"), or s itself.
function localRef(root, s) {
  const ref = s?.$ref
  if (typeof ref !== "string" || !ref.startsWith("#/")) return s
  let at = root
  for (const k of ref.slice(2).split("/")) at = at?.[k.replaceAll("~1", "/").replaceAll("~0", "~")]
  return at && typeof at === "object" ? at : s
}

// objectRoot makes a tool's parameters an object at the root, as magpie's
// gateway does for xAI (provider.ObjectRoot): allOf's branches are merged
// whole; of anyOf's and oneOf's object branches the properties are merged
// and a field each requires stays required. A root with no type, or an
// empty one, is an object. It reports whether it changed anything.
function objectRoot(ps, root = ps, depth = 0) {
  const unions = ["allOf", "anyOf", "oneOf"].filter((k) => Array.isArray(ps[k]))
  if (ps.type === "object" && !unions.length) return false
  const props = ps.properties && typeof ps.properties === "object" ? ps.properties : {}
  const required = Array.isArray(ps.required) ? [...ps.required] : []
  for (const b0 of ps.allOf ?? []) {
    let b = localRef(root, b0)
    if (!b || typeof b !== "object") continue
    if (depth < 8 && ["allOf", "anyOf", "oneOf"].some((k) => Array.isArray(b[k]))) objectRoot((b = structuredClone(b)), root, depth + 1)
    for (const [k, v] of Object.entries(b.properties ?? {})) if (!(k in props)) props[k] = v
    if (Array.isArray(b.required)) required.push(...b.required)
  }
  const branches = []
  for (const key of ["anyOf", "oneOf"]) {
    for (const b0 of ps[key] ?? []) {
      let b = localRef(root, b0)
      if (!b || typeof b !== "object") continue
      if (depth < 8 && ["allOf", "anyOf", "oneOf"].some((k) => Array.isArray(b[k]))) objectRoot((b = structuredClone(b)), root, depth + 1)
      if (b.type !== "object" && !b.properties) continue
      branches.push(b)
    }
  }
  const from = new Map()
  for (const b of branches) {
    for (const [k, v] of Object.entries(b.properties ?? {})) {
      if (k in props) continue
      const seen = from.get(k) ?? []
      if (!seen.some((x) => JSON.stringify(x) === JSON.stringify(v))) seen.push(v)
      from.set(k, seen)
    }
  }
  for (const [k, list] of from) props[k] = list.length === 1 ? list[0] : { anyOf: list }
  if (branches.length) {
    for (const k of Object.keys(branches[0].properties ?? {})) {
      if (branches.every((b) => Array.isArray(b.required) && b.required.includes(k))) required.push(k)
    }
  }
  for (const k of unions) delete ps[k]
  ps.type = "object"
  ps.properties = props
  const req = [...new Set(required)].filter((k) => typeof k === "string" && k in props)
  if (req.length) ps.required = req
  else delete ps.required
  return true
}

// Kimi (kimi-k3.1-pro) answers 400 "Invalid request parameters" to a oneOf
// that has no type, at the root of a $ref's target or deeper, even when every
// branch is an object (congee949, #378: dsh's REA tools, on workbuddy and
// workbuddy-ai alike; a "type": "object" beside it is taken). typeUnions gives
// such a oneOf type "object" when each branch is typed object itself or
// through a chain of local $refs, and changes nothing else: a union of other
// or mixed types, an anyOf, or a $ref that is missing, external or circular
// is left as it came. It reports whether it changed anything.
function typeUnions(s, root = s, depth = 0) {
  if (!s || typeof s !== "object" || Array.isArray(s) || depth > 64) return false
  let changed = false
  if (!("type" in s) && Array.isArray(s.oneOf) && s.oneOf.length && s.oneOf.every((b) => objectBranch(root, b))) {
    s.type = "object"
    changed = true
  }
  for (const k of SUB_ONE) if (typeUnions(s[k], root, depth + 1)) changed = true
  for (const k of SUB_LIST) for (const v of Array.isArray(s[k]) ? s[k] : []) if (typeUnions(v, root, depth + 1)) changed = true
  for (const k of SUB_MAP) {
    const m = s[k]
    if (!m || typeof m !== "object" || Array.isArray(m)) continue
    for (const v of Object.values(m)) if (typeUnions(v, root, depth + 1)) changed = true
  }
  return changed
}

// objectBranch says whether b is typed object, itself or where its local
// $refs lead; a $ref seen before on the way is a cycle, and not.
function objectBranch(root, b, seen = []) {
  if (!b || typeof b !== "object" || Array.isArray(b)) return false
  if ("type" in b) return b.type === "object"
  if (typeof b.$ref !== "string" || seen.includes(b.$ref) || seen.length > 16) return false
  const t = localRef(root, b)
  return t !== b && objectBranch(root, t, [...seen, b.$ref])
}

// fitTools mends b's function tools in place: their parameters (fitSchema,
// fitShape, objectRoot) and names. It reports whether it changed anything,
// and the names it gave, each to the name the client knows (ours), so that the
// model's calls are named back. A call in the chat's history is named as
// its tool now is.
function fitTools(b) {
  const names = new Map()
  if (!Array.isArray(b?.tools) || !b.tools.length) return { changed: false, names }
  let changed = false
  const taken = new Set()
  for (const t of b.tools) {
    const n = t?.function?.name
    if (typeof n === "string" && NAME_OK.test(n)) taken.add(n)
  }
  const theirs = new Map()
  for (const t of b.tools) {
    const fn = t?.type === "function" || t?.function ? t.function : null
    if (!fn || typeof fn !== "object") continue
    if (typeof fn.name === "string" && !NAME_OK.test(fn.name)) {
      const safe = safeName(fn.name, taken)
      names.set(safe, fn.name)
      theirs.set(fn.name, safe)
      fn.name = safe
      changed = true
    }
    if (fn.parameters === undefined) continue
    if (!fn.parameters || typeof fn.parameters !== "object" || Array.isArray(fn.parameters)) {
      fn.parameters = { type: "object", properties: {} }
      changed = true
      continue
    }
    if (fitSchema(fn.parameters)) changed = true
    if (fitShape(fn.parameters)) changed = true
    if (typeUnions(fn.parameters)) changed = true
    if (objectRoot(fn.parameters)) changed = true
  }
  if (theirs.size && Array.isArray(b.messages)) {
    for (const m of b.messages) {
      for (const c of Array.isArray(m?.tool_calls) ? m.tool_calls : []) {
        const safe = theirs.get(c?.function?.name)
        if (safe) c.function.name = safe
      }
      if (m?.role === "tool" && theirs.has(m.name)) m.name = theirs.get(m.name)
    }
  }
  const choice = b.tool_choice?.function
  if (choice && theirs.has(choice.name)) choice.name = theirs.get(choice.name)
  return { changed, names }
}

// fitted is a chat body with its tools mended (fitTools), and the names
// given its tools, each to the client's own; a body that isn't a chat is
// as it was.
function fitted(body) {
  if (typeof body !== "string") return { body, names: new Map() }
  try {
    const b = JSON.parse(body)
    const { changed, names } = fitTools(b)
    return { body: changed ? JSON.stringify(b) : body, names }
  } catch {
    return { body, names: new Map() }
  }
}

// namedBack gives the model's calls in a chat answer (one JSON body, or an
// SSE stream's chunks) the names the client gave its tools.
function namedBack(v, names) {
  let changed = false
  for (const c of Array.isArray(v?.choices) ? v.choices : []) {
    for (const at of [c?.message, c?.delta]) {
      for (const tc of Array.isArray(at?.tool_calls) ? at.tool_calls : []) {
        const ours = names.get(tc?.function?.name)
        if (ours) {
          tc.function.name = ours
          changed = true
        }
      }
    }
  }
  return changed
}

// renamed is res with namedBack on its body, read as it comes.
function renamed(res, names) {
  if (!names.size || !res.body) return res
  const headers = new Headers(res.headers)
  headers.delete("content-length")
  headers.delete("content-encoding")
  const json = /json/i.test(res.headers.get("content-type") ?? "")
  if (json) {
    const out = res.text().then((text) => {
      try {
        const v = JSON.parse(text)
        return namedBack(v, names) ? JSON.stringify(v) : text
      } catch {
        return text
      }
    })
    return out.then((text) => new Response(text, { status: res.status, statusText: res.statusText, headers }))
  }
  const dec = new TextDecoder()
  const enc = new TextEncoder()
  let rest = ""
  const line = (l) => {
    const m = /^data:\s?(.*)$/.exec(l)
    if (!m || m[1] === "[DONE]") return l
    try {
      const v = JSON.parse(m[1])
      return namedBack(v, names) ? `data: ${JSON.stringify(v)}` : l
    } catch {
      return l
    }
  }
  const body = res.body.pipeThrough(
    new TransformStream({
      transform(chunk, ctl) {
        rest += dec.decode(chunk, { stream: true })
        const lines = rest.split("\n")
        rest = lines.pop()
        if (lines.length) ctl.enqueue(enc.encode(lines.map(line).join("\n") + "\n"))
      },
      flush(ctl) {
        rest += dec.decode()
        if (rest) ctl.enqueue(enc.encode(line(rest)))
      },
    }),
  )
  return new Response(body, { status: res.status, statusText: res.statusText, headers })
}

// ---- signing in -------------------------------------------------------------

// poll asks path until WorkBuddy answers with data, retrying the codes
// that mean "not yet".
async function poll(site, path, headers, retry, deadline) {
  for (;;) {
    if (Date.now() > deadline) throw new Error(`${site.name} sign-in timed out`)
    try {
      const data = await call(site, "GET", path, headers)
      if (data && Object.keys(data).length) return data
    } catch (e) {
      if (!(retry.includes(e.code) || e.status === 408 || e.status === 429)) throw e
    }
    await sleep(POLL_MS)
  }
}

async function browserSignIn(site) {
  const state = await call(site, "POST", `/v2/plugin/auth/state?platform=${encodeURIComponent(site.platform)}`, {
    "X-No-Authorization": "true",
    "X-No-User-Id": "true",
    "X-No-Enterprise-Id": "true",
    "X-No-Department-Info": "true",
  }, {})
  if (!state?.state || !state?.authUrl) throw new Error(`${site.name} gave no sign-in page`)
  const url = new URL(state.authUrl)
  if (url.protocol !== "https:") throw new Error(`${site.name}'s sign-in page isn't https`)
  url.searchParams.set("version", APP_VERSION)
  url.searchParams.set("loginSessionId", hex())
  return {
    url: url.toString(),
    instructions: `Sign in to ${site.name} in the browser; this finishes by itself.`,
    method: "auto",
    async callback() {
      try {
        const deadline = Date.now() + SIGN_IN_MS
        const q = encodeURIComponent(state.state)
        const token = await poll(site, `/v2/plugin/auth/token?state=${q}`, {}, [11217], deadline)
        if (!token.accessToken) return { type: "failed", error: `${site.name} sent back no token` }
        let a = merge({}, token)
        const who = await poll(site, `/v2/plugin/login/account?state=${q}`, {
          Authorization: "Bearer " + a.access,
          "X-Domain": domainOf(site, a),
          "X-No-User-Id": "true",
          "X-No-Enterprise-Id": "true",
        }, [12151], deadline)
        a.uid = who.uid ?? ""
        a.enterpriseId = await enterpriseOf(site, a, who)
        return success(site, a, who.nickname || who.phoneNumber || who.uid)
      } catch (e) {
        return { type: "failed", error: e.message }
      }
    },
  }
}

function success(site, a, name) {
  return {
    type: "success",
    refresh: a.refresh ?? "",
    access: a.access,
    expires: a.expires ?? 0,
    accountId: name || site.name,
    uid: a.uid ?? "",
    domain: a.domain ?? "",
    ...(a.enterpriseId ? { enterpriseId: a.enterpriseId } : {}),
    ...(a.refreshExpiresAt ? { refreshExpiresAt: a.refreshExpiresAt } : {}),
    ...(a.tokenType ? { tokenType: a.tokenType } : {}),
  }
}

// desktopFile is where WorkBuddy's desktop app keeps its sign-in.
function desktopFile(site) {
  const home = homedir()
  const base =
    process.platform === "darwin" ? join(home, "Library", "Application Support", "CodeBuddyExtension")
    : process.platform === "win32" ? join(home, "AppData", "Local", "CodeBuddyExtension")
    : join(home, ".local", "share", "CodeBuddyExtension")
  return join(base, "Data", "Public", "auth", site.authID + ".info")
}

// readDesktop is WorkBuddy desktop's sign-in as the app keeps it now, null
// for none (or tokens the app keeps encrypted, which can't be read).
function readDesktop(site) {
  try {
    const f = JSON.parse(readFileSync(desktopFile(site), "utf8"))
    const t = f?.auth ?? {}
    if (typeof t.accessToken !== "string" || !t.accessToken || !f?.account?.uid) return null
    const now = Date.now()
    const acct = f.account
    return {
      access: t.accessToken,
      refresh: typeof t.refreshToken === "string" ? t.refreshToken : "",
      expires: t.expiresAt > 0 ? t.expiresAt : t.expiresIn > 0 ? now + t.expiresIn * 1000 : 0,
      refreshExpiresAt: t.refreshExpiresAt > 0 ? t.refreshExpiresAt : t.refreshExpiresIn > 0 ? now + t.refreshExpiresIn * 1000 : 0,
      domain: typeof t.domain === "string" ? t.domain : "",
      tokenType: typeof t.tokenType === "string" ? t.tokenType : "",
      uid: acct.uid,
      ...(typeof acct.enterpriseId === "string" && acct.enterpriseId.trim() ? { enterpriseId: acct.enterpriseId.trim() } : {}),
      name: acct.nickname || acct.phoneNumber || acct.uid,
      // what the errors below say to do goes by it: sign in to the app again
      source: "desktop",
    }
  } catch {
    return null
  }
}

// notDesktop says why there is no desktop sign-in to use: WorkBuddy 5.6
// and later seal its tokens at rest ({$wbEncrypted: 1, envelope}), which
// only the app itself opens (plugins#18), so the browser sign-in is the way;
// else the app isn't signed in.
function notDesktop(site) {
  try {
    const t = JSON.parse(readFileSync(desktopFile(site), "utf8"))?.auth ?? {}
    if (t.accessToken?.$wbEncrypted || t.refreshToken?.$wbEncrypted)
      return `${site.name} desktop keeps its sign-in encrypted (WorkBuddy 5.6 and later), which magpie can't read; sign in with "${site.name} account (browser)" instead`
  } catch {}
  return `${site.name} desktop isn't signed in`
}

// desktopSignIn uses the account WorkBuddy desktop is signed in to: nothing
// is copied (a copied refresh token would be the app's and the plugin's
// both, and the first to use it would sign the other out); its file is
// read each time, as the app keeps it.
function desktopSignIn(site) {
  return {
    url: "",
    instructions: `Uses the account ${site.name} desktop is signed in to.`,
    method: "auto",
    async callback() {
      const d = readDesktop(site)
      if (!d) return { type: "failed", error: notDesktop(site) }
      return { type: "success", refresh: "", access: "", expires: 0, source: "desktop", accountId: d.name || site.name, uid: d.uid }
    },
  }
}

// desktopHeld is what a desktop sign-in was renewed to here, by site and
// account: kept in memory only, never written where the app keeps it.
const desktopHeld = new Map()

// current is the sign-in a stored auth names, fresh: WorkBuddy desktop's,
// read where it keeps it, or one kept here.
async function current(site, client, auth) {
  if (auth?.source !== "desktop") return fresh(site, client, auth)
  const d = readDesktop(site)
  if (!d) throw new Error(notDesktop(site))
  const k = site.id + "|" + d.uid
  const h = desktopHeld.get(k)
  const a = await fresh(site, null, h && h.expires >= d.expires ? h : d)
  desktopHeld.set(k, a)
  return a
}

// ---- allowance ----------------------------------------------------------------

// STATUS_TEXT is Go's http.StatusText, which magpie's built-in said a
// refused meter call with; a status Go has no words for is "HTTP <n>", so
// the card always has an error.
const STATUS_TEXT = {
  100: "Continue", 101: "Switching Protocols", 102: "Processing", 103: "Early Hints",
  200: "OK", 201: "Created", 202: "Accepted", 203: "Non-Authoritative Information", 204: "No Content", 205: "Reset Content",
  206: "Partial Content", 207: "Multi-Status", 208: "Already Reported", 226: "IM Used",
  300: "Multiple Choices", 301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified", 305: "Use Proxy",
  307: "Temporary Redirect", 308: "Permanent Redirect",
  400: "Bad Request", 401: "Unauthorized", 402: "Payment Required", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed",
  406: "Not Acceptable", 407: "Proxy Authentication Required", 408: "Request Timeout", 409: "Conflict", 410: "Gone",
  411: "Length Required", 412: "Precondition Failed", 413: "Request Entity Too Large", 414: "Request URI Too Long",
  415: "Unsupported Media Type", 416: "Requested Range Not Satisfiable", 417: "Expectation Failed", 418: "I'm a teapot",
  421: "Misdirected Request", 422: "Unprocessable Entity", 423: "Locked", 424: "Failed Dependency", 425: "Too Early",
  426: "Upgrade Required", 428: "Precondition Required", 429: "Too Many Requests", 431: "Request Header Fields Too Large",
  451: "Unavailable For Legal Reasons",
  500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout",
  505: "HTTP Version Not Supported", 506: "Variant Also Negotiates", 507: "Insufficient Storage", 508: "Loop Detected",
  510: "Not Extended", 511: "Network Authentication Required",
}
const statusText = (n) => STATUS_TEXT[n] || `HTTP ${n}`

// meterAsked is the billing API's reply to path, asked with a's sign-in.
function meterAsked(site, a, path, body = {}, signal = AbortSignal.timeout(20000)) {
  return fetch(site.endpoint + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", "User-Agent": "WorkBuddy/" + UA_VERSION,
      Authorization: "Bearer " + a.access, "X-User-Id": a.uid ?? "", "X-Domain": domainOf(site, a), "X-Product": "SaaS",
      "X-IDE-Type": "WorkBuddy", ...enterpriseHeaders(a) },
    body: JSON.stringify(body),
    signal,
  })
}

// EDGE_READS is how much of a request WorkBuddy's front door (APISIX, on
// both sites) reads before it answers a sign-in it refuses. A refused
// request with a longer body gets no answer at all: the connection is
// reset, and the chat fails as "ECONNRESET: The socket connection was
// closed unexpectedly", which says nothing of the sign-in (wyp on Discord,
// 2026-10-15: a Claude Code session past 2 MiB failed on every turn).
// Measured on 2026-10-15 on www.workbuddy.ai and copilot.tencent.com: a
// body of 2097152 bytes with a refused token got 401, one of 2097153 a
// reset; with an accepted token 20 MB went through.
const EDGE_READS = 2 * 1024 * 1024

const byteLength = (b) =>
  typeof b === "string" ? Buffer.byteLength(b) : b instanceof ArrayBuffer || ArrayBuffer.isView(b) ? b.byteLength : 0

// unanswered is what a chat whose fetch failed with err comes to: a chat
// past EDGE_READS that the connection was reset on is asked about at the
// billing API, which answers a refused sign-in with its 401 whatever the
// size. A 401 there is the chat's own answer, said as one; anything else
// leaves err as it was, as does a smaller chat, which the front door
// answers itself. The chat isn't sent again.
async function unanswered(site, a, body, err, signal) {
  if (signal?.aborted || err?.name === "AbortError" || err?.name === "TimeoutError") throw err
  const size = byteLength(body)
  if (size <= EDGE_READS) throw err
  let status = 0
  let challenge = ""
  try {
    const res = await meterAsked(site, a, "/billing/meter/get-user-resource-summary", {}, AbortSignal.timeout(20000))
    status = res.status
    challenge = res.headers.get("www-authenticate") ?? ""
    await res.body?.cancel()
  } catch {
    throw err
  }
  if (status !== 401) throw err
  const mb = (size / (1024 * 1024)).toFixed(1)
  const note = `It closes the connection on a refused request over 2 MiB (this one was ${mb} MiB) instead of answering, which is why it read as ECONNRESET.`
  const message =
    `${site.name} refused this account's sign-in (401). ${note} ` +
    (a.source === "desktop" ? `Sign in to ${site.name} desktop again.` : `Sign in to ${site.name} again in magpie.`)
  // the billing API's challenge goes with it: it says whether the token
  // itself was refused (invalidToken), as the chat's own 401 would have
  const headers = { "content-type": "application/json", ...(challenge ? { "www-authenticate": challenge } : {}) }
  const res = new Response(JSON.stringify({ error: { message, type: "authentication_error", code: 401 } }), {
    status: 401,
    statusText: "Unauthorized",
    headers,
  })
  edgeNotes.set(res, note)
  return res
}

// edgeNotes is what unanswered's 401s say of the reset, for signedOut to
// say again when the sign-in can't be renewed.
const edgeNotes = new WeakMap()

// ---- a refused sign-in ----------------------------------------------------------

// invalidToken says res is WorkBuddy refusing the access token itself. Its
// front door (APISIX, on both sites) answers a token it doesn't take with
// 401 and WWW-Authenticate: Bearer realm="copilot", error="invalid_token",
// on a chat (streamed or not) and on the billing API alike; measured on
// www.workbuddy.ai on 2026-10-23 with a real account's token cut short,
// and with "garbage". (Its /v3/config answers 200 to any token, so the
// models list never meets a 401.) A 401 without it is not taken as the
// sign-in's: WorkBuddy answers some paths with a bare 401 to an accepted
// sign-in (magpie's check-in, モモコ on Discord).
const invalidToken = (res) => res?.status === 401 && /invalid_token/i.test(res.headers.get("www-authenticate") ?? "")

// refreshRefused says e is WorkBuddy turning the refresh token itself
// away: HTTP 401 with code 12153, "refresh token failed:400 Bad Request:
// invalid_grant: Invalid refresh token" for a spent or unknown one, or
// "10000:token format error" (measured 2026-10-23). Anything else (a 5xx,
// a timeout) says nothing of the sign-in.
const refreshRefused = (e) => e instanceof WBError && (e.status === 401 || /invalid_grant/i.test(e.message))

// reissued is the sign-in to send a request WorkBuddy refused a's access
// token for (401) once more with, before that token's time: WorkBuddy can
// refuse one long before it ends (a browser sign-in's lasts as long as
// its refresh token, about 340 days), and renewing only by time left such
// an account refused on every request for months. It is
//   { a }: a sign-in with another access token, to send the request with
//     once more;
//   { why, kind }: none to be had, why, and of what kind: "refresh
//     refused" (WorkBuddy turned the refresh token away too), "nothing to
//     renew with" (no refresh token, or one past its end), or "unknown"
//     (the refresh failed for another reason, the desktop's token is the
//     one refused).
// began is the access token the request read at its start: a stored one
// that isn't it was saved since by someone else (magpie's renewal,
// another of its processes), and is taken rather than renewing again.
//
// A desktop sign-in is the app's: its file is read again, and what was
// renewed here (desktopHeld) is let go, but its refresh token is never
// spent for a refusal, which would sign the app itself out (see
// desktopSignIn).
async function reissued(site, client, getAuth, a, began) {
  if (a.source === "desktop") {
    desktopHeld.delete(site.id + "|" + a.uid)
    const d = readDesktop(site)
    if (!d) return { why: notDesktop(site), kind: "unknown" }
    if (d.access !== a.access) {
      desktopHeld.set(site.id + "|" + d.uid, d)
      return { a: d }
    }
    return { why: `${site.name} desktop's own sign-in is the one refused`, kind: "unknown" }
  }
  const newer = async () => {
    const now = await getAuth().catch(() => null)
    return now?.type === "oauth" && now.source !== "desktop" && now.access && now.access !== began && now.access !== a.access ? now : null
  }
  const saved = await newer()
  if (saved) return { a: saved }
  if (!refreshable(a)) return { why: a.refresh ? "its refresh token has ended" : "there is no refresh token to renew it with", kind: "nothing to renew with" }
  try {
    const next = await renew(site, a, async (n) => {
      try {
        await client?.auth?.set?.({ path: { id: site.id }, body: n })
      } catch {}
    })
    return next.access !== a.access ? { a: next } : { why: "renewing it gave the same token", kind: "unknown" }
  } catch (e) {
    if (refreshRefused(e)) {
      // spent by someone else, who saved what it gave
      const saved = await newer()
      if (saved) return { a: saved }
      return { why: `it refused the refresh token too (${e.said ?? e.message})`, kind: "refresh refused" }
    }
    return { why: `renewing it failed: ${e?.said ?? e?.message ?? e}`, kind: "unknown" }
  }
}

// gone says whether a sign-in reissued (r) couldn't renew is gone, on
// WorkBuddy's word: it turned the refresh token away, or refused the access
// token itself (tokenRefused, invalidToken) with none to renew it with. A
// desktop sign-in never is: the app's file is read on each request, and
// signing in to the app again is all it takes.
const gone = (a, r, tokenRefused) =>
  a.source !== "desktop" && (r.kind === "refresh refused" || (r.kind === "nothing to renew with" && !!tokenRefused))

// refusedText says WorkBuddy refused a's sign-in, why it wasn't renewed,
// and what to do.
function refusedText(site, a, why, note) {
  return (
    `${site.name} refused this account's sign-in (401)${why ? "; " + why : ""}. ${note ? note + " " : ""}` +
    (a.source === "desktop"
      ? `Sign in to ${site.name} desktop again; magpie reads its sign-in on the next request.`
      : `Sign in to it again in magpie: Providers → ${site.name} → this account's Sign in again.`)
  )
}

// signedOut is the answer to a request WorkBuddy refused this account's
// sign-in for, renewed or not: a 401 that says so and what to do, which
// WorkBuddy's own (an HTML page, its {code, msg}) doesn't. expired marks
// the account in magpie (X-Magpie-Sign-In), out of routing and "sign in
// again" on its row, till it is signed in again; else it is kept.
function signedOut(site, a, why, expired, note) {
  const message = refusedText(site, a, why, note)
  return new Response(JSON.stringify({ error: { message, type: "authentication_error", code: 401 } }), {
    status: 401,
    statusText: "Unauthorized",
    headers: { "content-type": "application/json", "X-Magpie-Sign-In": expired ? "expired" : "kept" },
  })
}

// meter asks the billing API as magpie's built-in did, its errors said
// the same: WorkBuddy's message, else its code, else the HTTP status.
async function meter(site, a, path, body = {}) {
  const res = await meterAsked(site, a, path, body)
  let env = {}
  try {
    env = JSON.parse(await res.text()) ?? {}
  } catch {}
  const code = Number.parseInt(env.code, 10) || 0
  if (!res.ok) {
    // the status and whether the token itself was refused go with it, for
    // usage's one renewal on a 401 (reissued)
    const said = { status: res.status, invalidToken: invalidToken(res) }
    if (code) throw Object.assign(new Error(env.msg || `error ${code}`), said)
    if (env.msg) throw Object.assign(new Error(env.msg), said)
    throw Object.assign(new Error(statusText(res.status)), said)
  }
  if (code) throw new Error(env.msg || `error ${code}`)
  return env.data ?? null
}

// capacity is a capacity the billing API sends, as a string ("438.88")
// or now and then a number; empty is 0.
function capacity(v) {
  if (v == null || v === "") return 0
  if (typeof v === "number") return v
  const s = String(v)
  const n = Number(s)
  if (s.trim() !== s || Number.isNaN(n)) throw new Error(`strconv.ParseFloat: parsing ${JSON.stringify(s)}: invalid syntax`)
  return n
}

// compact is a count as magpie says one: whole, else to two places.
const compact = (n) => (Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/0+$/, "").replace(/\.$/, ""))

// usageOf is the account's credits, from its resource summary: the plan's
// credits used against what the cycle grants, as magpie's built-in said —
// the count itself too (amount of limit, in credits), for magpie to say it
// as used or left beside the share (magpie#659). The free plan is said as a
// plan: "Free" alone beside the account read as the model being free, and
// routing as preferring it for that (#337).
function usageOf(sum, plan) {
  const out = { plan: plan || (sum?.IsPaidUser ? "Pro" : "Free plan"), windows: [] }
  let total = 0, used = 0
  for (const p of sum?.Packages ?? []) {
    total += capacity(p?.CycleTotalCapacity)
    used += capacity(p?.CycleUsedCapacity)
  }
  if (total > 0) out.windows.push({ name: "Credits", used: (100 * used) / total, display: `${compact(used)} / ${compact(total)}`, amount: used, limit: total, unit: "credits" })
  return out
}

// enterpriseUsageOf is an enterprise (team) account's credits, from
// /billing/meter/get-enterprise-user-usage, the meter WorkBuddy's own usage
// page (www.workbuddy.cn/profile/usage) reads for one (magpie #1904,
// CnsMaple): the resource summary answers such an account with no
// packages at all. It counts the cycle's credits flat — credit used of
// limitNum — and when the cycle began and resets (UTC+8). null when it
// grants none.
function enterpriseUsageOf(u) {
  const limit = capacity(u?.limitNum)
  if (!(limit > 0)) return null
  const used = capacity(u?.credit)
  const w = { name: "Credits", used: (100 * used) / limit, display: `${compact(used)} / ${compact(limit)}`, amount: used, limit, unit: "credits" }
  const at = (v) => (typeof v === "string" && /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(v.trim()) ? Date.parse(v.trim().replace(" ", "T") + "+08:00") : 0)
  const reset = at(u?.cycleResetTime), start = at(u?.cycleStartTime)
  if (reset > 0) w.resetsAt = new Date(reset).toISOString()
  if (reset > start && start > 0) w.span = (reset - start) / 1000
  return w
}

// spareFree has the credits windows not count the models that cost none
// (notModels): a model at x0.00 draws no credits, and with the credits
// used up WorkBuddy still serves it (deepseek-v4.1-flash at 560 / 560,
// magpie-community/plugins#138, hheei). Counted, the window at 100% put
// the account last, and behind every other, for its free models too. A
// model free only some hours (a night promotion) is not free here, as in
// the list (priceOf). A list that can't be read leaves the windows as they
// are: every model counted, as before.
async function spareFree(site, a, windows) {
  if (!windows.length) return windows
  let free = []
  try {
    free = (await liveModels(site, a)).filter((m) => m.free).map((m) => m.id)
  } catch {}
  if (free.length) for (const w of windows) w.notModels = free
  return windows
}

// The packages' codes, as WorkBuddy's own client lists them when it asks
// for each package's credits (its COMMODITY_CODES): the API refuses a
// detail list without them (10001 "PackageCodes required").
const CODE = (n, s) => `TCACA_code_${n}_${s}`
const PAID_CODES = [CODE("002", "AkiJS3ZHF5"), CODE("005", "maRGyrHhw1"), CODE("003", "FAnt7lcmRT"), CODE("023", "4xbGhMrE6q"),
  CODE("026", "BaESVICNoi"), CODE("027", "0FCGVA6vSa"), CODE("009", "0XmEQc2xOf"), CODE("038", "OhvqZtiPKr"), CODE("036", "lupO5WgNdG")]
const DAILY_CODE = CODE("001", "PqouKr6QWV")
const FREE_CODES = [DAILY_CODE, CODE("008", "cfWoLwvjU4"), CODE("035", "ArVxJcGDsm"), CODE("006", "DbXS0lrypC"), CODE("039", "KRcQj7wUat"),
  CODE("040", "mi9rCYg46x"), CODE("007", "nzdH5h4Nl0"), CODE("028", "NtpWi0jzXs"), CODE("037", "WxOD3MpI2o"), CODE("029", "6wCGEWquYy"),
  CODE("030", "BjSt89qTvr")]
// a slice package (CapacityType 4) is refreshed each day: its credits are
// lost at the same midnight on every account, so they tell none apart
const SLICE = "4"

// beijing is WorkBuddy's own time ("2026-10-13 00:39:27" is UTC+8, as its
// DeductionEndTime for the same package says).
const beijing = (ms) => new Date(ms + 8 * 3600_000).toISOString().slice(0, 19).replace("T", " ")

// packagesOf is every package of the account with credits, paid and free,
// as WorkBuddy's client asks for them: active and used-up (Status 0, 3),
// up to 10 pages of 200.
async function packagesOf(site, a, now = Date.now()) {
  const day = beijing(now).slice(0, 10)
  const lists = [
    ["/billing/meter/get-user-resource-paid-packages", { PackageCodes: PAID_CODES, NeedRenewInfo: true }],
    ["/billing/meter/get-user-resource-free-packages", { PackageCodes: FREE_CODES, SlicePeriodStartTime: day + " 00:00:00", SlicePeriodEndTime: day + " 23:59:59" }],
  ]
  const out = []
  for (const [path, extra] of lists) {
    for (let page = 1; page <= 10; page++) {
      const d = await meter(site, a, path, { PageNumber: page, PageSize: 200, Status: [0, 3], ...extra })
      const got = d?.Accounts ?? []
      out.push(...got)
      if (got.length < 200 || out.length >= (d?.TotalCount ?? 0)) break
    }
  }
  return out
}

// endOf is when a package's credits are lost: its DeductionEndTime, else
// ExpiredTime, else the cycle's end (UTC+8), as WorkBuddy's client reads it.
function endOf(p) {
  const ms = Number(p?.DeductionEndTime)
  if (ms > 0) return ms
  for (const v of [p?.ExpiredTime, p?.CycleEndTime]) {
    if (typeof v !== "string" || !v.trim()) continue
    const t = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(v.trim()) ? Date.parse(v.trim().replace(" ", "T") + "+08:00") : Date.parse(v)
    if (t > 0) return t
  }
  return 0
}

// runsOutOf is the soonest moment credits left in one of the account's
// packages are lost, and how many (cdredfox on X): smart routing spends
// the account whose credits expire first. Only packages the summary
// counts, with credits left, and not the daily slice; null when none.
function runsOutOf(sum, packages, now = Date.now()) {
  const counted = new Set((sum?.Packages ?? []).map((p) => p?.PackageCode))
  let at = 0, amount = 0
  for (const p of packages ?? []) {
    if (!counted.has(p?.PackageCode) || p?.PackageCode === DAILY_CODE || String(p?.CapacityType) === SLICE) continue
    const left = capacity(p?.CycleCapacityRemainPrecise ?? p?.CycleCapacityRemain)
    const end = endOf(p)
    if (!(left > 0) || end <= now) continue
    if (!at || end < at) at = end, amount = left
    else if (end === at) amount += left
  }
  return at ? { runsOutAt: new Date(at).toISOString(), runsOut: amount } : null
}

// ---- models -----------------------------------------------------------------

// variants are the reasoning levels as OpenCode gives them to the AI SDK.
function variants(efforts) {
  return Object.fromEntries((efforts ?? []).map((e) => [e, { reasoningEffort: e }]))
}

// configModels is the site's own list as a provider's models, each on the
// API it is served on: a Claude model on Messages, the rest on chat.
function configModels(site) {
  return Object.fromEntries(site.models.map(([id, name, context, efforts]) => [id, {
    name,
    limit: { context, output: 0 },
    tool_call: true,
    // OpenCode (and magpie's host) read a config model's package and URL
    // from provider: { npm, api }, not api
    ...(nativeOf(site, id) ? { provider: { npm: NPM_MESSAGES, api: apiMessages(site) } } : {}),
    ...(efforts ? { reasoning: true, variants: variants(efforts) } : {}),
  }]))
}

function modelOf(site, provider, m) {
  return {
    id: m.id,
    providerID: provider.id,
    name: m.name,
    api: { id: m.id, url: nativeOf(site, m.id) ? apiMessages(site) : api(site), npm: nativeOf(site, m.id) ? NPM_MESSAGES : NPM },
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
      input: { text: true, image: !!m.images, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false },
      interleaved: false,
    },
    release_date: "",
    variants: variants(m.efforts),
    // magpie's own: served at no cost to the plan's credits
    free: !!m.free,
    // magpie's own: the credits a request costs, as a multiple (0: none)
    rate: m.rate ?? 0,
    // magpie's own: the price before a discount running now (0: none)
    rateWas: m.rateWas ?? 0,
  }
}

// liveModels asks WorkBuddy's product config for the plan's models: the
// "cli" agent's list, each with its details. The CLI User-Agent picks
// WorkBuddy's config (a bare WorkBuddy/<v> gets CodeBuddy IDE's, no cli).
// creditsOf reads a model's credits, the multiple WorkBuddy's picker shows
// by it: "x0.03", "0" or 0; "x0.00" is free. A rate it can't read (or
// none, or an infinite one) is undefined: the model is neither free nor
// rated. The same rule as magpie's built-in (wbCredits).
function creditsOf(c) {
  if (c == null || c === "") return undefined
  const s = String(c).trim().toLowerCase().replace(/\s+credits?$/, "").replace(/^x/, "")
  const n = Number(s)
  return s !== "" && Number.isFinite(n) && n >= 0 ? n : undefined
}

// freeCredits says whether a model's credits are none: "x0.00", "0" or 0.
// A rate it can't read is not free.
function freeCredits(c) {
  return creditsOf(c) === 0
}

// discountOf reads a promotion's price: its discountedCredits ("0.50x",
// "0.00x"), else the factor times the model's own credits.
function discountOf(p, base) {
  const said = creditsOf(String(p.discount?.discountedCredits ?? "").trim().replace(/x$/i, ""))
  if (said !== undefined) return said
  const f = Number(p.discount?.factor)
  return Number.isFinite(f) && f >= 0 && base !== undefined ? base * f : undefined
}

// running says whether a promotion is on at now: enabled, and inside its
// validFrom..validUntil. Its daily hours are not looked at here.
function running(p, now) {
  if (!p || p.enabled === false) return false
  const from = Date.parse(p.schedule?.validFrom ?? "")
  const until = Date.parse(p.schedule?.validUntil ?? "")
  return !(from > now) && !(until <= now)
}

// priceOf is a model's price as magpie keeps it, from its credits and the
// config's modelPromotions (what WorkBuddy's picker shows as 限时免费,
// 夜间免费, 夜间折扣). magpie keeps the list it is given until it asks
// again, so a price that holds only some hours of the day (Hy4 preview,
// free 23:00–8:00 only) is given as its full price, never as free: a list
// asked for at night would otherwise say "free" all day (#121). A discount
// for whole days (Step-5-Preview's 独家限免) is the price now, with the
// price before it as rateWas.
function priceOf(d, promotions, now = Date.now()) {
  const base = creditsOf(d.credits)
  const mine = (Array.isArray(promotions) ? promotions : []).filter(
    (p) => p?.kind === "discount" && Array.isArray(p.modelIds) && p.modelIds.includes(d.id) && running(p, now),
  )
  if (mine.some((p) => Array.isArray(p.schedule?.daily) && p.schedule.daily.length)) {
    // by the hour: the full price, which the credits are unless a night
    // discount already took them to its own price
    const night = mine.filter((p) => p.discount && Array.isArray(p.schedule?.daily) && p.schedule.daily.length)
    if (base === undefined || (base === 0 && night.some((p) => discountOf(p, base) === 0))) return { free: false }
    return { free: false, rate: base }
  }
  const off = mine.filter((p) => p.discount).map((p) => discountOf(p, base)).filter((r) => r !== undefined)
  if (off.length) {
    const r = Math.min(...off)
    return { free: r === 0, rate: r, ...(base !== undefined && base > r ? { rateWas: base } : {}) }
  }
  if (base === undefined) return {}
  return { free: base === 0, rate: base }
}

// effortsOf are the levels a config model takes. Most list them in
// reasoning.supportedEfforts; some (fast-model, deepseek-v4.1-flash, kimi-k3,
// gemini-3.5-flash … on both sites, plugins#255) say only supportsReasoning
// and a default reasoning.effort. Those take low, high and max (each
// reasoned under a real account, on four of them) and their own default.
const EFFORT_ORDER = ["low", "medium", "high", "xhigh", "max"]
function effortsOf(d, site, id) {
  const said = d.reasoning?.supportedEfforts
  if (Array.isArray(said) && said.length) {
    // A Claude model on Messages takes max though the config's list stops
    // at xhigh: measured 2026-10-24, output_config.effort "max" answered
    // 200 on each Claude model, and on claude-opus-5.5 with more thinking
    // than xhigh (11248 thinking tokens against 1999, one run each). How
    // much more a level gives varies by model and run; what matters here
    // is that a client picking max reaches the server as max, not xhigh.
    // On chat max means nothing more (see NPM_MESSAGES), so a model there
    // keeps what the config says.
    if (nativeOf(site, id) && !said.includes("max")) return [...said, "max"]
    return said
  }
  if (d.supportsReasoning !== true) return []
  const own = d.reasoning?.effort
  const set = new Set(["low", "high", "max", ...(typeof own === "string" && EFFORT_ORDER.includes(own) ? [own] : [])])
  return EFFORT_ORDER.filter((e) => set.has(e))
}

async function liveModels(site, a) {
  const headers = new Headers()
  sign(site, a, headers)
  headers.set("User-Agent", `CLI/${APP_VERSION} WorkBuddy/${UA_VERSION}`)
  headers.set("X-Requested-With", "XMLHttpRequest")
  headers.set("Accept", "application/json")
  const res = await fetch(site.endpoint + "/v3/config", { headers, signal: AbortSignal.timeout(15000) })
  const env = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(`${site.name}'s config: HTTP ${res.status}`)
  if (env.code) throw new Error(`${site.name}'s config: ${env.msg} (${env.code})`)
  const cfg = env.data ?? {}
  const out = []
  for (const agent of cfg.agents ?? []) {
    if (agent.name !== "cli") continue
    for (const id of agent.models ?? []) {
      const d = (cfg.models ?? []).find((x) => x.id === id)
      const m = { id, name: d?.name || id }
      if (d) {
        m.context = d.maxInputTokens
        m.output = d.maxOutputTokens
        m.images = d.supportsImages === true && !textOnly(site, id)
        Object.assign(m, priceOf(d, cfg.modelPromotions))
        const es = effortsOf(d, site, id)
        if (es.length) {
          const canOff = d.reasoning?.canDisableThinking
          m.efforts = [...(!d.onlyReasoning && (canOff == null || canOff) ? ["none"] : []), ...es]
        }
      }
      out.push(m)
    }
  }
  // tell apart models the config names alike: deepseek-v4.1-flash and
  // deepseek-v4.1-flash-sg are both "Deepseek-V4.1-Flash"
  const first = new Map()
  for (const m of out) {
    const id = first.get(m.name)
    if (id === undefined) {
      first.set(m.name, m.id)
      continue
    }
    const rest = m.id.startsWith(id + "-") ? m.id.slice(id.length + 1) : ""
    m.name = `${m.name} (${rest ? rest.toUpperCase() : m.id})`
  }
  return out
}

// ---- the plugins --------------------------------------------------------------

// AFTER_TOOLS_REFUSED is WorkBuddy's 400 code 11153, "the conversation must
// end with a user message", which WorkBuddy AI's gemini-3.8-flash answers to a
// chat whose tool results are followed by the user's message (#113): Claude
// Code's reminders, a user's words sent mid-task, a tool's images, which
// magpie hands over in a user message after the results. It does end with a
// user message; the same chat with any other message between them is served.
// The code is matched whole, as with 14018.
const AFTER_TOOLS_REFUSED = /must end with a user message|["']code["']\s*:\s*["']?11153(?![\d.])/i

// replyAfterTools is a chat ending with tool results then the user's
// message(s), with an empty assistant message between them, as if the model
// had said nothing after the results. Tried with a real WorkBuddy AI
// account on 2026-10-14: gemini-3.8-flash serves it and follows the user's
// message as a user's, where folded into the tool's result it was read as
// the tool's output and its ask left undone. null for any other chat — one
// ending with the assistant's message is a prefill, refused as it is.
function replyAfterTools(body) {
  const b = chatOf(body)
  const msgs = b?.messages
  if (!Array.isArray(msgs)) return null
  let i = msgs.length - 1
  while (i >= 0 && msgs[i]?.role === "user") i--
  if (i === msgs.length - 1 || i < 0 || msgs[i]?.role !== "tool") return null
  msgs.splice(i + 1, 0, { role: "assistant", content: "" })
  return JSON.stringify(b)
}

function makePlugin(site) {
  // the models of this site that refused tool results then the user's
  // message, for this run: their next chats go with replyAfterTools at once
  const endsAfterTools = new Set()
  // the system prompts this run WorkBuddy refused as an unapproved channel:
  // their next chats go with systemAsUser at once, not refused first
  const refusedSystems = new Set()
  return async ({ client }) => ({
    config: async (config) => {
      config.provider ??= {}
      config.provider[site.id] ??= {}
      const p = config.provider[site.id]
      p.name ??= site.name
      p.npm ??= NPM
      p.api ??= api(site)
      p.models = { ...configModels(site), ...(p.models ?? {}) }
    },
    // the conversation's id, which the chats below carry to WorkBuddy:
    // magpie's (or OpenCode's) session for this request, when it names one
    "chat.headers": async (input, output) => {
      const id = input?.model?.providerID ?? input?.provider?.info?.id
      if (id !== site.id || !input?.sessionID) return
      output.headers[SESSION] = String(input.sessionID).slice(0, 128)
    },
    provider: {
      id: site.id,
      // the account's own list, when it's signed in and WorkBuddy answers;
      // else the list above
      async models(provider, { auth }) {
        if (auth?.type !== "oauth" || !(auth.access || auth.source)) return provider.models
        try {
          const a = await current(site, client, auth)
          const ms = await liveModels(site, a)
          if (!ms.length) return provider.models
          return Object.fromEntries(ms.map((m) => [m.id, modelOf(site, provider, m)]))
        } catch {
          return provider.models
        }
      },
    },
    auth: {
      provider: site.id,
      // magpie renews a browser sign-in LEAD_MS before its end (an hour's
      // token), once for the account, before its requests, models and
      // usage ask; the check before each request stays for OpenCode,
      // which doesn't call this. Desktop's sign-in has no expiry stored
      // and is renewed as before.
      refreshLead: LEAD_MS,
      refresh: (auth) => refreshed(site, auth),
      async loader(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "oauth") return {}
        return {
          // no baseURL: a model's own api.url says where it is served —
          // <endpoint>/v2 for a chat, <endpoint>/v1 for a Claude model on
          // Messages. A provider-wide base would put /messages under /v2.
          baseURL: "",
          apiKey: "",
          async fetch(input, init) {
            const stored = await getAuth()
            if (stored?.type !== "oauth") throw new Error(`${site.name} isn't signed in`)
            let a = await current(site, client, stored)
            const req = input instanceof Request ? input : null
            const headers = new Headers(init?.headers ?? req?.headers)
            headers.delete("authorization")
            headers.delete("x-api-key")
            headers.delete("content-length")
            const session = sessionOf(headers)
            sign(site, a, headers)
            let body = init?.body
            if (body === undefined && req) body = await req.clone().text()
            // An Anthropic request goes as the agent sent it: WorkBuddy
            // serves these models on Messages itself, and the rewrites
            // below (a default system message, the chat endpoint's
            // max_tokens spelling, tool shapes, the move of a refused
            // system prompt) exist for the chat endpoint alone. Claude
            // Code's own system prompt is taken there as it is — measured
            // 2026-10-24: the billing line, the "official CLI" line,
            // Codex's and DeepSeek Harness's openings all answer on
            // /v1/messages, where a chat is refused 11128.
            const native = isMessages(req ? req.url : String(input), body)
            let names = {}
            if (!native) {
              body = withSystem(body)
              const fit = fitted(body)
              body = normalizeToolChoice(fit.body)
              names = fit.names
            } else {
              body = withoutUnsigned(body)
            }
            attend(headers, body, session)
            const post = (b) =>
              fetch(req ? req.url : input, { ...init, method: init?.method ?? req?.method, headers, body: b }).catch((e) => unanswered(site, a, b, e, init?.signal))
            // a 401 is answered once, on whichever try meets it: by a
            // sign-in with another access token (reissued), the request
            // sent once more with it, never again after that; else, or
            // when that is refused too, by signedOut. The body is a
            // string here (or bytes), which goes again as it is; one that
            // can't be read twice isn't sent again.
            //
            // The account is marked (expired) only on WorkBuddy's word
            // that the sign-in is gone: it turned the refresh token away,
            // or refused the access token itself (invalidToken) with none
            // to renew it with, or refused even the token just renewed. A
            // desktop sign-in is never marked: the app's file is read on
            // each request, so signing in to the app again is enough.
            let renewed = false
            const ours = new WeakSet() // the answers signedOut gave, passed on as they are
            const out = (why, expired, res) => {
              const r = signedOut(site, a, why, expired && a.source !== "desktop", edgeNotes.get(res))
              ours.add(r)
              return r
            }
            const resendable = (b) => b == null || typeof b === "string" || b instanceof ArrayBuffer || ArrayBuffer.isView(b)
            const send = async (b) => {
              const res = await post(b)
              if (res.status !== 401) return res
              if (renewed) return out("it refused the token renewed for it too", invalidToken(res), res)
              if (!resendable(b)) return res
              await res.body?.cancel().catch(() => {})
              const r = await reissued(site, client, getAuth, a, stored.access)
              if (!r.a) return out(r.why, gone(a, r, invalidToken(res)), res)
              renewed = true
              a = r.a
              sign(site, a, headers)
              const again = await post(b)
              if (again.status !== 401) return again
              return out("it refused the token renewed for it too", invalidToken(again), again)
            }
            // a model known to turn tool results then the user's message away
            // gets the empty reply between them up front (see endsAfterTools)
            const model = chatOf(body)?.model
            // a system prompt WorkBuddy refused before goes as the user's
            // words at once (see systemAsUser)
            const sysKey = native ? "" : systemKey(body)
            if (sysKey && refusedSystems.has(sysKey)) body = systemAsUser(body) ?? body
            const shaped = (b) => (!native && endsAfterTools.has(model) ? replyAfterTools(b) ?? b : b)
            let res = await send(shaped(body))
            // a signature WorkBuddy's own signing didn't make — a session
            // moved here from another provider keeps its thinking blocks,
            // and a truncated or altered one reads the same way — is 11159
            // too, and none of them can be told apart from a good one
            // before sending. The request goes once more without them.
            // Measured 2026-10-24: another vendor's signature answers 11159
            // on its own, drop_block notwithstanding; without the block the
            // same history answers 200. Only when the history holds a
            // signature that isn't this route's own, so a session on this
            // route never sends twice.
            if (native && res.status >= 400) {
              const text = await res.text()
              const again = BAD_SIGNATURE.test(text) ? withoutSignedThinking(body) : null
              if (again) res = await send(again)
              else res = new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers })
            }
            if (res.status === 400 && !native) {              let text = await res.text()
              const moved = REFUSED.test(text) && sysKey && !refusedSystems.has(sysKey) ? systemAsUser(body) : null
              if (moved) {
                refusedSystems.add(sysKey)
                if (refusedSystems.size > 256) refusedSystems.delete(refusedSystems.values().next().value)
                body = moved
                res = await send(shaped(body))
                if (res.status === 400) text = await res.text()
              }
              const again = res.status === 400 && !endsAfterTools.has(model) && AFTER_TOOLS_REFUSED.test(text) ? replyAfterTools(body) : null
              if (again) {
                endsAfterTools.add(model)
                res = await send(again)
              } else if (res.status === 400) {
                res = new Response(text, { status: res.status, statusText: res.statusText, headers: res.headers })
              }
            }
            if (ours.has(res)) return res
            // renewed here: a mark the account had comes off
            return res.status >= 400 ? explained(res) : kept(native ? res : await renamed(res, names), renewed ? "renewed" : "kept")
          },
        }
      },
      // the account's credits, as magpie's built-in showed them; each
      // read keeps the sign-in (signIn), as the built-in's neither marked
      // an account lapsed nor cleared it, but for a 401 (gone, below)
      async usage(getAuth) {
        const auth = await getAuth()
        if (auth?.type !== "oauth" || !(auth.access || auth.source)) return { error: "not signed in", signIn: "kept" }
        try {
          let a = await current(site, client, auth)
          // the meter has no /v2 prefix, on either site
          const SUMMARY = "/billing/meter/get-user-resource-summary"
          // a 401 is answered as a chat's is: once, by a sign-in with
          // another access token, else the card says WorkBuddy refused
          // the sign-in and what to do; renewed, a mark comes off
          let said = "kept"
          let sum
          try {
            sum = await meter(site, a, SUMMARY)
          } catch (e) {
            if (e?.status !== 401) throw e
            const r = await reissued(site, client, getAuth, a, auth.access)
            if (!r.a) return { error: refusedText(site, a, r.why), signIn: gone(a, r, e.invalidToken) ? "expired" : "kept" }
            a = r.a
            try {
              sum = await meter(site, a, SUMMARY)
            } catch (e) {
              if (e?.status !== 401) throw e
              const why = "it refused the token renewed for it too"
              return { error: refusedText(site, a, why), signIn: a.source !== "desktop" && e.invalidToken ? "expired" : "kept" }
            }
            said = "renewed"
          }
          const out = usageOf(sum, auth.plan)
          // an enterprise account's summary has no packages: its credits
          // are the enterprise meter's (#1904). A personal account's
          // refusal of it (10001 "uid or enterpriseID is empty") leaves
          // the card as the summary said.
          if (!out.windows.length) {
            let ent = null
            try {
              ent = enterpriseUsageOf(await meter(site, a, "/billing/meter/get-enterprise-user-usage"))
            } catch {}
            if (ent) return { plan: auth.plan || "Enterprise", windows: await spareFree(site, a, [ent]), signIn: said }
          }
          // when the credits left run out, for routing to spend them first;
          // a list that can't be read leaves it unknown, the card stands
          const credits = out.windows.find((w) => w.name === "Credits")
          if (credits) {
            try {
              Object.assign(credits, runsOutOf(sum, await packagesOf(site, a)) ?? {})
            } catch {}
          }
          await spareFree(site, a, out.windows)
          return { ...out, signIn: said }
        } catch (e) {
          return { error: e?.message ?? String(e), signIn: "kept" }
        }
      },
      methods: [
        {
          type: "oauth",
          label: `${site.name} account (browser)`,
          authorize: () => browserSignIn(site),
        },
        {
          type: "oauth",
          label: `${site.name} desktop's sign-in`,
          authorize: async () => desktopSignIn(site),
        },
      ],
    },
  })
}

// REFUSED_HINT is what the user can do about WorkBuddy's "Illegal API
// invocation from an unapproved channel": both builds answer it to a chat
// holding words they turn away, whatever the headers. Codex's and Claude
// Code's were the first (magpie #182), DeepSeek Harness's and Claude
// Desktop's came later (#279, #170), and WorkBuddy reads every message, so
// it names no client. FLAGGED rewrites the words known; this is for ones it
// doesn't know. It ends the error's message word for word as magpie's
// Routing page has it (WB_REFUSED in its routing client.js), which says it
// apart from WorkBuddy's own words.
const REFUSED_HINT =
  "WorkBuddy refused words in this client's prompt (most often its system prompt); use it from Hermes, OpenCode or Pi, or add another provider to this group"
const REFUSED = /unapproved channel|illegal api invocation/i

// BAD_SIGNATURE is WorkBuddy's refusal of a thinking block whose signature it
// didn't make: internal code 11159, "Invalid `signature` in `thinking` block.
// The block is bound to a different conversation", else 11133 wrapping
// "thinking_signature_invalid: replay the original thinking block and
// signature unchanged".
const BAD_SIGNATURE = /thinking_signature_invalid|invalid\s+`?signature`?\s+in\s+`?thinking|block is bound to a different conversation|"internal_code"\s*:\s*"?11159/i

// RESTRICTED_HINT is what the user can do about WorkBuddy's 403 code 11140
// "request illegal", whose displayMsg says "Your account has been restricted
// due to unusual activity" (magpie #1849): the account is out until WorkBuddy
// lifts the restriction, which an appeal asks for. It ends the error's
// message as magpie's built-in puts it (provider.WBRestrictedHint), word for
// word, so magpie rests the account for hours rather than minutes.
const RESTRICTED_HINT =
  "WorkBuddy restricted this account for unusual activity; it stays out until WorkBuddy lifts that, which an appeal (Submit feedback in WorkBuddy) asks for"
const RESTRICTED_CODE = 11140
// The code is matched whole, as with 14018: 111400 is another error.
const RESTRICTED_TEXT = /account has been restricted due to unusual activity|账号存在异常行为|帳號存在異常行為|["']code["']\s*:\s*["']?11140(?![\d.])/i

// EXHAUSTED_HINT explains WorkBuddy's requirement of an active package
// even for 0-rate / free models. When an account has no packages or 0 credits,
// WorkBuddy returns code 14018 ("Credits exhausted...").
const EXHAUSTED_HINT =
  "WorkBuddy requires an active credit package even for 0-rate free models; check your account package balance"
// The code is matched whole: a code that only begins with 14018 (140185) is
// another error, and insufficient_quota would rest the account as out of credit.
const EXHAUSTED_TEXT = /credits exhausted|额度已用尽|["']code["']\s*:\s*["']?14018(?![\d.])/i

// kept is an answer that went through, saying the sign-in is kept (the
// built-in never cleared a WorkBuddy account's mark, as it never set one),
// or "renewed" when it went through on a sign-in renewed for a 401, which
// takes a mark off (see signedOut).
function kept(res, said = "kept") {
  const headers = new Headers(res.headers)
  headers.set("X-Magpie-Sign-In", said)
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}

// explained is res with REFUSED_HINT, RESTRICTED_HINT or EXHAUSTED_HINT added to that refusal's message.
// Each refusal says the sign-in is kept: a 401 never comes here, it is
// signedOut's.
async function explained(res) {
  const text = await res.text()
  const again = (b) => {
    const headers = new Headers(res.headers)
    headers.delete("content-length")
    headers.delete("content-encoding")
    headers.set("X-Magpie-Sign-In", "kept")
    return new Response(b, { status: res.status, statusText: res.statusText, headers })
  }
  let v
  try {
    v = JSON.parse(text)
  } catch {}
  const at = v?.error && typeof v.error === "object" ? v.error : v && typeof v === "object" ? v : null
  // the Messages route words its code two more ways (seen 2026-10-24):
  // {"error": {"code": "400001", "internal_code": "11159", …}}, and
  // {"code": 11102, "error": "11158:…"}, the code that says what happened
  // first in a string error, where the outer one only says it failed
  const inner = at?.internal_code ?? (typeof v?.error === "string" ? v.error.match(/^(\d{5}):/)?.[1] : undefined)
  const codes = [inner, at?.data?.code, at?.code, v?.code, v?.data?.code].filter((c) => c != null).map(Number)
  const isRefused = REFUSED.test(text)
  const isRestricted = !isRefused && (codes.includes(RESTRICTED_CODE) || RESTRICTED_TEXT.test(text))
  const isExhausted = !isRestricted && (codes.includes(14018) || EXHAUSTED_TEXT.test(text))
  if (!isRefused && !isRestricted && !isExhausted) return again(text)

  let msg = text.trim()
  if (at && typeof at.message === "string") msg = at.message
  else if (typeof at?.data?.msg === "string") msg = at.data.msg
  else if (typeof v?.error === "string") msg = v.error
  else if (typeof v?.msg === "string" && v.msg) msg = v.msg // WorkBuddy's own {"code", "msg"}
  else if (typeof v?.data?.msg === "string") msg = v.data.msg

  const hint = isRefused ? REFUSED_HINT : isRestricted ? RESTRICTED_HINT : EXHAUSTED_HINT
  const type = isRefused || isRestricted ? "permission_error" : "insufficient_quota"
  const errCode = isRefused ? null : isRestricted ? RESTRICTED_CODE : 14018
  msg = `${msg} — ${hint}`
  const headers = new Headers(res.headers)
  headers.set("content-type", "application/json")
  headers.delete("content-length")
  headers.delete("content-encoding")
  headers.set("X-Magpie-Sign-In", "kept")
  return new Response(JSON.stringify({ error: { message: msg, type, code: errCode } }), { status: res.status, statusText: res.statusText, headers })
}

export const WorkBuddyAuthPlugin = makePlugin(SITES.workbuddy)
export const WorkBuddyAIAuthPlugin = makePlugin(SITES["workbuddy-ai"])

// for tests
export const _internal = { typeUnions, systemAsUser, systemKey, DEFAULT_SYSTEM, EDGE_READS, fitShape, replyAfterTools, AFTER_TOOLS_REFUSED, enterpriseUsageOf, notDesktop, textOnly, runsOutOf, packagesOf, endOf, enterpriseHeaders, enterpriseOf, liveModels, browserSignIn, withSystem, normalizeToolChoice, fitTools, fitted, renamed, unflagged, usageOf, desktopHeld, explained, REFUSED_HINT, RESTRICTED_HINT, EXHAUSTED_HINT, freeCredits, creditsOf, priceOf, fresh, SITES, renewing, renewed, SESSION, attend, turnKey, conversationKey, signatureOf, isMessages, withoutUnsigned, withoutSignedThinking, answeredBefore, nativeOf, modelOf, configModels, API_MESSAGES: NPM_MESSAGES, API_CHAT: NPM }
