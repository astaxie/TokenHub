// Grok (SuperGrok / X Premium+) through the Grok Build CLI's own sign-in.
//
// The CLI (https://x.ai/cli) signs in with a device code and keeps the
// sign-in in its home (GROK_HOME, else ~/.grok) as auth.json. This plugin
// only reads that file: the CLI refreshes the token, which changes each
// time, so a token about to expire is renewed by running the CLI there
// (`grok models`), never by the plugin itself. Requests go to the CLI's
// backend, which speaks OpenAI's Responses API, signed as the CLI signs
// them.
import { spawn, execFile } from "node:child_process"
import { existsSync, mkdirSync, readFileSync, realpathSync, rmSync, statSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { delimiter, dirname, isAbsolute, join, resolve } from "node:path"
import { randomBytes } from "node:crypto"
import { STATUS_CODES } from "node:http"

const PROVIDER = "grok"
const BASE = "https://cli-chat-proxy.grok.com/v1"
const CLIENT_VERSION = "1.0.41"
const CPA_CLIENT_ID = "b1a00492-073a-47ea-816f-4c329264a828"
const CPA_SCOPE = `https://auth.x.ai::${CPA_CLIENT_ID}`
const REFRESH_MARGIN = 5 * 60 * 1000 // a token this close to its end is renewed before a request
const LEAD_MS = REFRESH_MARGIN // and this close, magpie renews it ahead of time (auth.refresh)
const LINK_WAIT = 30 * 1000
const INSTALL = process.platform === "win32" ? "irm https://x.ai/cli/install.ps1 | iex" : "curl -fsSL https://x.ai/cli/install.sh | bash"
// NO_CLI is what a sign-in without Grok Build says: how to install it, and
// that one put somewhere else by hand (a container with no shell to run the
// installer) is taken from GROK_BIN_DIR, as the installer's own folder is,
// while one found on PATH has to be under a .grok folder (yetone/magpie#2223)
const NO_CLI = `install Grok Build first: ${INSTALL}; or set GROK_BIN_DIR to the folder holding its grok binary`

// the tool types Grok's backend takes; it turns the whole request away over another
const TOOLS = new Set(["function", "web_search", "x_search", "image_generation", "collections_search", "file_search",
  "code_execution", "code_interpreter", "mcp", "shell", "tool_search"])

const ANSI = /\x1b\[[0-9;?]*[A-Za-z]/g
const LINK = /https:\/\/\S+/

// linkIn is the page a line of `grok login` gives to open, "" for none: a
// line saying it failed names the endpoint it couldn't reach (`Error: error
// sending request for url (https://auth.x.ai/oauth2/device/code): …`),
// which is no page to open.
const linkIn = (line) => (/\berror\b/i.test(line) ? "" : (line.match(LINK)?.[0] ?? ""))

// ---- the CLI ------------------------------------------------------------------

// cliHome is where the CLI keeps its own sign-in and settings.
function cliHome() {
  return process.env.GROK_HOME || join(homedir(), ".grok")
}

// ownHomes is where this plugin keeps a sign-in made beside the CLI's own.
function ownHomes() {
  return join(process.env.XDG_CONFIG_HOME || join(homedir(), ".config"), "opencode-grok-auth")
}

function isFile(p) {
  try {
    return statSync(p).isFile()
  } catch {
    return false
  }
}

// isGrokBuild tells xAI's grok from any other program by that name: its
// installer keeps it under the grok home.
function isGrokBuild(p) {
  let real = p
  try {
    real = realpathSync(p)
  } catch {}
  return real.replaceAll("\\", "/").toLowerCase().includes("/.grok/")
}

// executable finds the CLI, first to last: the installer's bin
// (GROK_BIN_DIR, GROK_HOME/bin, ~/.grok/bin), each folder on PATH, then
// ~/.local/bin; those two only when it is really Grok Build.
function executable() {
  const name = process.platform === "win32" ? "grok.exe" : "grok"
  const home = homedir()
  const seen = new Set()
  const out = []
  const add = (dir, own) => {
    if (!dir || !isAbsolute(dir)) return
    const p = join(dir, name)
    const k = process.platform === "win32" ? p.toLowerCase() : p
    if (seen.has(k)) return
    seen.add(k)
    out.push({ p, own })
  }
  add(process.env.GROK_BIN_DIR, true)
  add(join(cliHome(), "bin"), true)
  if (home) add(join(home, ".grok", "bin"), true)
  for (const d of (process.env.PATH ?? "").split(delimiter)) add(d.trim(), false)
  if (home) add(join(home, ".local", "bin"), false)
  for (const c of out) if (isFile(c.p) && (c.own || isGrokBuild(c.p))) return c.p
  return ""
}

// envFor runs the CLI as the user runs it, in the given home. GROK_AUTH_PATH
// goes too: grok 1.0.62 keeps its sign-in at that file instead of
// home/auth.json, where this plugin reads it, so a login with it set
// finished signed in and left nothing here.
function envFor(home) {
  const env = {}
  for (const [k, v] of Object.entries(process.env)) {
    if (["GROK_HOME", "GROK_AUTH_PATH", "GROK_AUTH_PROVIDER_COMMAND", "GROK_AUTH_EXPIRED"].includes(k.toUpperCase())) continue
    env[k] = v
  }
  env.GROK_HOME = home
  return env
}

// look reads the sign-in the CLI keeps in home: the first, by key, with a
// token. With none, why says what the file held instead.
//
// who is the account it is: its email, else its xAI user id (the token's
// subject when the file has none). grok 1.0.62 takes both from the
// sign-in's id_token and writes "email": null when that
// has no email claim (an xAI account signed in through X has none), and
// still says "✓ Signed in" (llllleo9 on 𝕏; seen with grok 1.0.62 in a
// sandbox, signed in against a stand-in auth.x.ai).
function look(home) {
  const file = join(home, "auth.json")
  let text
  try {
    text = readFileSync(file, "utf8")
  } catch (e) {
    return { c: null, why: e?.code === "ENOENT" ? `there is no ${file}` : `${file} can't be read (${e?.code ?? e?.message})` }
  }
  // an empty file, or one that isn't JSON, is most likely one the CLI is
  // writing as it is read (it rewrites auth.json when it renews): torn
  // says the read tells nothing, which read() tries again
  if (!text.trim()) return { c: null, why: `${file} is empty`, torn: true }
  let all
  try {
    all = JSON.parse(text)
  } catch {
    return { c: null, why: `${file} isn't JSON`, torn: true }
  }
  if (!all || typeof all !== "object" || Array.isArray(all)) return { c: null, why: `${file} holds no sign-in` }
  const keys = Object.keys(all).sort()
  for (const k of keys) {
    const c = all[k]
    if (c && typeof c.key === "string" && c.key) {
      const t = Date.parse(c.expires_at ?? "")
      const email = typeof c.email === "string" ? c.email : ""
      const sub = cpaClaims(c.key)?.sub
      const user = (typeof c.user_id === "string" && c.user_id) || (typeof sub === "string" ? sub : "")
      const got = { key: c.key, email, user, who: email || user, expires: Number.isFinite(t) ? t : 0, issuer: c.oidc_issuer ?? "" }
      lastGood.set(home, got)
      return { c: got, why: "" }
    }
  }
  return { c: null, why: keys.length ? `${file} has ${keys.join(", ")}, none with a token` : `${file} holds no sign-in` }
}

// lastGood is each home's last sign-in read whole.
const lastGood = new Map()
const TORN_TRIES = 6
const TORN_WAIT = 100

// read is look, a torn read (auth.json caught mid-write) tried again for
// about half a second; still torn, it is the last sign-in read whole there,
// as the CLI itself keeps its previous credentials when auth.json won't
// read ("auth reload: auth.json unreadable, keeping previous credentials").
// No lock is taken: the CLI's auth.json.lock is a flock it holds while it
// renews, which Node can't take, and a reader holding it could only hold a
// renewal back.
async function read(home) {
  let r = look(home)
  for (let i = 1; r.torn && i < TORN_TRIES; i++) {
    await new Promise((ok) => setTimeout(ok, TORN_WAIT))
    r = look(home)
  }
  if (r.torn && lastGood.has(home)) return { c: lastGood.get(home), why: "" }
  return r
}

// credential is the sign-in in home, null when there is none. A file that
// stays torn, with nothing read there before, is unknown, not "not signed
// in": that is an error saying so.
async function credential(home) {
  const r = await read(home)
  if (r.torn) throw new Error(`Grok Build's sign-in couldn't be read (${r.why}); it may be mid-write, try again in a moment`)
  return r.c
}

// shared is whether home is Grok Build's own, which opening Grok Build
// renews; a home of this plugin's (a second account, an imported one) only
// this plugin's runs of the CLI ever touch.
function shared(home) {
  return resolve(home) === resolve(cliHome())
}

// notSignedIn says a home holds no sign-in, and what makes one.
function notSignedIn(home) {
  return shared(home)
    ? "Grok is not signed in; run `grok login`"
    : "Grok is not signed in for this account any more; add the account in magpie again"
}

// cliSaid is what a `grok models` that renewed nothing said: how it ended
// and its last line.
function cliSaid(r) {
  if (!r) return ""
  const last = (r.stderr || r.stdout || "").replace(ANSI, "").split("\n").map((l) => l.trim()).filter(Boolean).at(-1)?.slice(0, 200)
  const how = !r.err ? "ran and left the same token" : r.err.killed ? "timed out" : `exited ${r.err.code ?? "with an error"}`
  return ` (grok models ${how}${last ? `: ${last}` : ""})`
}

// unrenewed is a token Grok turned away, or one past its end, that Grok
// Build, run in its home, didn't replace: the CLI couldn't renew it (xAI's renewal failing, or
// a refresh token used up elsewhere), which signing in with `grok login`
// in ~/.grok doesn't help where the account's home is the plugin's own.
function unrenewed(home, r) {
  const how = shared(home)
    ? "open Grok Build and send it a message, which renews the sign-in magpie reads"
    : "this account's sign-in is kept apart from Grok Build's own, so opening Grok Build doesn't renew it: add the account in magpie again"
  return Object.assign(new Error(`Grok Build couldn't renew the sign-in${cliSaid(r)}; ${how}`), { unrenewed: true })
}

// dead is a token Grok turned away even as Grok Build had just renewed it.
function dead(home) {
  return shared(home)
    ? "Grok turned away Grok Build's sign-in even after renewing it; run `grok login` to sign Grok Build in afresh"
    : "Grok turned away this account's sign-in even after renewing it; add the account in magpie again"
}

function run(file, args, opts) {
  return new Promise((resolve) => {
    execFile(file, args, { timeout: 30000, ...opts }, (err, stdout, stderr) =>
      resolve({ err, stdout: String(stdout ?? ""), stderr: String(stderr ?? "") }),
    )
  })
}

// refreshing runs the CLI's renewals one after another; renewing is the one
// asked for each home, which those who come while it is due or running join
// rather than running the CLI there again.
let refreshing = Promise.resolve()
const renewing = new Map()

// renew has the CLI renew the sign-in in home: a `grok models` there renews
// it as any use of the CLI does.
function renew(exe, home) {
  let r = renewing.get(home)
  if (!r) {
    r = refreshing.then(() => run(exe, ["models"], { cwd: dirname(home), env: envFor(home) })).finally(() => renewing.delete(home))
    refreshing = r.catch(() => {})
    renewing.set(home, r)
  }
  return r
}

// token is the CLI's sign-in in home with a token still good. One about to
// expire is first renewed by the CLI in its own home, and so is one Grok
// turned away: rejected is that token (or true for whatever home holds).
// A token on disk other than the one turned away was renewed already and
// is taken as it is; one the CLI's run leaves as it was is unrenewed,
// never "not signed in" nor "expired".
async function token(home, rejected = false) {
  let c = await credential(home)
  const exe = executable()
  const refused = !!c && (rejected === true || (typeof rejected === "string" && c.key === rejected))
  let r
  if (c && exe && (refused || c.expires - Date.now() < REFRESH_MARGIN)) {
    const was = c.key
    r = await renew(exe, home)
    c = await credential(home)
    if (c && refused && c.key === was) throw unrenewed(home, r)
  }
  if (!c) throw new Error(notSignedIn(home))
  if (!exe && (refused || (c.expires && c.expires <= Date.now()))) throw new Error(`Grok turned the sign-in away and there is no Grok Build here to renew it; ${NO_CLI}`)
  if (c.expires && c.expires <= Date.now()) throw unrenewed(home, r)
  return c
}

let version = { v: CLIENT_VERSION, at: 0 }

// clientVersion is the CLI version the requests say they come from, unless
// the installed one is newer: without it the backend calls the CLI outdated.
async function clientVersion() {
  if (Date.now() - version.at < 10 * 60 * 1000) return version.v
  let v = CLIENT_VERSION
  const exe = executable()
  if (exe) {
    const { err, stdout } = await run(exe, ["--version"], { timeout: 5000 }) // "grok 1.0.41 (4220f3b224a6)"
    const got = !err && stdout.match(/\d+\.\d+\.\d+/)?.[0]
    if (got && newer(got, v)) v = got
  }
  version = { v, at: Date.now() }
  return v
}

function newer(a, b) {
  const x = a.split(".").map(Number), y = b.split(".").map(Number)
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] > y[i]
  return false
}

// headers say a request comes from the Grok CLI.
async function sign(headers, key) {
  const v = await clientVersion()
  const os = process.platform === "darwin" ? "macos" : process.platform === "win32" ? "windows" : process.platform
  const arch = { arm64: "aarch64", x64: "x86_64", ia32: "386" }[process.arch] ?? process.arch
  headers.set("Authorization", `Bearer ${key}`)
  headers.set("x-grok-client-version", v)
  headers.set("x-xai-token-auth", "xai-grok-cli")
  headers.set("x-grok-client-identifier", "grok-shell")
  headers.set("x-grok-client-mode", "headless")
  headers.set("User-Agent", `grok-shell/${v} (${os}; ${arch})`)
}

// bodyText is a request body handed over as bytes read as the text it
// is, so it can be reshaped; any other body is as it came.
function bodyText(body) {
  if (body instanceof ArrayBuffer || ArrayBuffer.isView(body)) return new TextDecoder().decode(body)
  return body
}

// objectRoot is a tool's parameters as a plain object at the root, with no
// anyOf, oneOf or allOf there, folded as magpie's built-in folds them
// (provider.ObjectRoot): allOf's branches are all merged; of anyOf's and
// oneOf's object branches, a union itself folded first, the properties are
// merged, a field each of them requires stays required, and the other
// branches go. Branches may be local $refs. A schema already a plain object
// is returned as it came; the caller's own is never changed.
function objectRoot(ps) {
  return foldRoot(ps, ps, 0)
}

function foldRoot(root, ps, depth) {
  if (!isObj(ps)) return ps
  const has = (k) => Object.hasOwn(ps, k)
  if (ps.type === "object" && !has("anyOf") && !has("oneOf") && !has("allOf")) return ps
  const out = { ...ps }
  const props = { ...(isObj(ps.properties) ? ps.properties : {}) }
  let required = Array.isArray(ps.required) ? [...ps.required] : []
  const ref = (b) => {
    if (!isObj(b)) return null
    if (typeof b.$ref !== "string" || !b.$ref) return b
    for (const k of ["$defs", "definitions"]) {
      const pre = "#/" + k + "/"
      if (b.$ref.startsWith(pre)) {
        const d = root[k]?.[b.$ref.slice(pre.length)]
        return isObj(d) ? d : null
      }
    }
    return null
  }
  for (const b of Array.isArray(ps.allOf) ? ps.allOf : []) {
    const bm = ref(b)
    if (!bm) continue
    if (isObj(bm.properties)) for (const [k, v] of Object.entries(bm.properties)) if (!Object.hasOwn(props, k)) props[k] = v
    if (Array.isArray(bm.required)) required.push(...bm.required)
  }
  delete out.allOf
  const branches = []
  for (const k of ["anyOf", "oneOf"]) {
    for (const b of Array.isArray(ps[k]) ? ps[k] : []) {
      let bm = ref(b)
      if (!bm) continue
      // a branch that is itself a union (zod's discriminated create and
      // update in automation_update) is folded first, not dropped
      if (depth < 8 && ["anyOf", "oneOf", "allOf"].some((u) => Object.hasOwn(bm, u))) bm = foldRoot(root, bm, depth + 1)
      if (bm.type !== "object" && !Object.hasOwn(bm, "properties")) continue
      branches.push(bm)
    }
    delete out[k]
  }
  // a field every branch requires stays required, beside the root's and
  // allOf's own, which no branch can drop; a field the root doesn't define
  // takes each schema the branches give it, as anyOf when they differ
  let common = null
  const from = new Map()
  for (const b of branches) {
    for (const [k, v] of Object.entries(isObj(b.properties) ? b.properties : {})) {
      if (Object.hasOwn(props, k)) continue
      const vs = from.get(k) ?? []
      if (!vs.some((w) => JSON.stringify(w) === JSON.stringify(v))) vs.push(v)
      from.set(k, vs)
    }
    const br = Array.isArray(b.required) ? b.required : []
    common = common === null ? [...br] : common.filter((r) => br.includes(r))
  }
  for (const [k, vs] of from) props[k] = vs.length === 1 ? vs[0] : { anyOf: vs }
  for (const r of common ?? []) if (!required.includes(r)) required.push(r)
  required = [...new Set(required)]
  out.type = "object"
  out.properties = props
  if (required.length) out.required = required
  else delete out.required
  return out
}

const isObj = (v) => !!v && typeof v === "object" && !Array.isArray(v)

// rewrite leaves out what Grok's backend doesn't take: tools of other
// types (a freeform apply_patch, a namespace of sub-agent tools), a web
// search's external_web_access, a tool_choice naming a dropped type or
// with no tools beside it (magpie sends "none" so when a client turned
// tools off, yetone/magpie#3110; Grok refuses it: "A tool_choice was set on
// the request but no tools were specified"), and a
// reasoning item's "content": null, beside which it can't read the
// encrypted reasoning. A function's parameters with a union at the root
// (Codex desktop's codex_app automation_update) are folded to an object,
// which Grok turns away otherwise: "tool parameter root must be an object
// type" (magpie#1271).
function rewrite(body, xSearch = false) {
  if (typeof body !== "string" || (!xSearch && !body.includes('"tools"') && !body.includes('"tool_choice"') && !body.includes('"reasoning"') && !body.includes('"agent_message"'))) return body
  let m
  try {
    m = JSON.parse(body)
  } catch {
    return body
  }
  if (!m || typeof m !== "object" || Array.isArray(m)) return body
  // Read the client's choice before dropping unsupported tools or choices.
  const search = xSearch && (m.tool_choice == null || m.tool_choice === "auto") &&
    (m.tools == null || Array.isArray(m.tools)) &&
    (m.tool_choice === "auto" || Array.isArray(m.tools) && m.tools.length > 0)
  let dirty = false
  if (Array.isArray(m.tools)) {
    m.tools = m.tools.filter((t) => {
      if (!t || typeof t !== "object") return true
      if (!TOOLS.has(t.type)) {
        dirty = true
        return false
      }
      if ("external_web_access" in t) {
        delete t.external_web_access
        dirty = true
      }
      if (t.type === "function") {
        const fn = isObj(t.function) ? t.function : t
        const ps = objectRoot(fn.parameters)
        if (ps !== fn.parameters) {
          fn.parameters = ps
          dirty = true
        }
      }
      return true
    })
    if (m.tool_choice && typeof m.tool_choice === "object" && !TOOLS.has(m.tool_choice.type)) {
      delete m.tool_choice
      dirty = true
    }
  }
  // No choice and no tools can mean a translated "none" on older gateways.
  if (search && (m.tool_choice === "auto" || m.tools?.length) && !m.tools?.some((t) => t?.type === "x_search")) {
    m.tools ??= []
    m.tools.push({ type: "x_search" })
    dirty = true
  }
  if (Object.hasOwn(m, "tool_choice") && !(Array.isArray(m.tools) && m.tools.length)) {
    delete m.tool_choice
    dirty = true
  }
  if (Array.isArray(m.input)) {
    m.input = m.input.map((it) => {
      if (!isObj(it) || it.type !== "agent_message" || Object.hasOwn(it, "encrypted_content") ||
          !Array.isArray(it.content) || !it.content.length ||
          !it.content.every((part) => isObj(part) && !Object.hasOwn(part, "encrypted_content") &&
            (part.type === "input_text" && typeof part.text === "string" ||
             part.type === "input_image" && typeof part.image_url === "string"))) return it
      // Grok takes ordinary messages, not Codex's collaboration item.
      // Sealed tasks remain untouched for the gateway's existing guard.
      const from = typeof it.author === "string" ? it.author : ""
      const to = typeof it.recipient === "string" ? it.recipient : ""
      const header = from && to ? `From ${from} to ${to}` : from ? `From ${from}` : to ? `To ${to}` : ""
      dirty = true
      return { type: "message", role: "user", content: header
        ? [{ type: "input_text", text: `${header}\n\n` }, ...it.content] : it.content }
    })
  }
  for (const it of Array.isArray(m.input) ? m.input : []) {
    if (it && it.type === "reasoning" && "content" in it && it.content === null) {
      delete it.content
      dirty = true
    }
  }
  return dirty ? JSON.stringify(m) : body
}

// ---- models -------------------------------------------------------------------

// the levels the built-in offered for grok-4.7 before Grok listed any (its
// maker's, as models.dev has them): kept when the list can't be read, so
// a move leaves an agent's reasoning level where it was
const DEFAULT_EFFORTS = ["low", "medium", "high", "xhigh"]

const DEFAULT_MODELS = {
  "grok-4.7": {
    name: "Grok 4.7",
    attachment: true,
    reasoning: true,
    tool_call: true,
    modalities: { input: ["text", "image"], output: ["text"] },
    variants: Object.fromEntries(DEFAULT_EFFORTS.map((e) => [e, { reasoningEffort: e }])),
  },
}

// listModels is what the account can use, as the CLI's backend lists it:
// Responses models only, each with its context window and efforts
// (listed hardest first, kept easiest first).
async function listModels(key) {
  const headers = new Headers()
  await sign(headers, key)
  const res = await fetch(`${BASE}/models`, { headers })
  const text = await res.text()
  // a token Grok turned away before its time: the caller has Grok Build
  // renew it and asks once more (models below)
  if (res.status === 401) throw Object.assign(new Error("Grok turned the sign-in away"), { status: 401 })
  if (!res.ok) throw new Error(`Grok models: ${res.status} ${text.slice(0, 200)}`)
  const out = []
  for (const d of JSON.parse(text)?.data ?? []) {
    if (!d?.id || (d.api_backend && d.api_backend !== "responses")) continue
    const efforts = (d.reasoning_efforts ?? []).map((e) => e?.value).filter(Boolean).reverse()
    out.push(...windowsOf(d, efforts))
  }
  if (!out.length) throw new Error("Grok listed no models")
  return out
}

// labelOf is a window as the picker shows it: 500000 is "500k".
const labelOf = (n) => (n % 1000 === 0 ? `${n / 1000}k` : String(n))

// windowsOf is one model for each context window Grok lists. context_window
// is the default and keeps the plain id (grok-4.7), so a saved agent stays
// where it was. Each other size in context_windows is offered beside it
// (grok-4.7@500k), the way the CLI's /context-window offers it. A list with
// one size, or none, stays one model.
function windowsOf(d, efforts) {
  const name = d.name || d.id
  const fallback = Number(d.context_window) || 0
  const listed = (Array.isArray(d.context_windows) ? d.context_windows : [])
    .map(Number)
    .filter((n) => Number.isInteger(n) && n > 0)
  const windows = [...new Set(listed.length ? listed : [fallback].filter(Boolean))]
  const base = windows.includes(fallback) ? fallback : windows[0] ?? 0
  const out = [{ id: d.id, apiId: d.id, name, context: base || fallback, window: base, efforts }]
  for (const w of windows) {
    if (w === base) continue
    out.push({ id: `${d.id}@${labelOf(w)}`, apiId: d.id, name: `${name} @ ${labelOf(w)}`, context: w, window: w, efforts })
  }
  return out
}

// modelOf splits a requested id into the model Grok lists and the window
// the id names. grok-4.7 is grok-4.7 at its default window (no header);
// grok-4.7@500k is grok-4.7 at 500000 tokens. The header is what the CLI
// sends as x-grok-context-window. The proxy serves up to its largest
// window either way and refuses past it; the header names the window the
// caller asked to compact against. A window no multiple of 1000 is named
// by its digits alone (grok-4.7@131072), as labelOf writes it.
function modelOf(id) {
  const at = typeof id === "string" ? id.match(/^(.+)@(\d+)(k?)$/) : null
  if (!at || !(Number(at[2]) > 0)) return { model: id, window: 0 }
  return { model: at[1], window: Number(at[2]) * (at[3] ? 1000 : 1) }
}

// ---- sign-in ------------------------------------------------------------------

// opensPage is whether `grok login --device-auth` opens its page in the
// browser itself, which it does wherever it can: on macOS (NSWorkspace) and
// Windows (ShellExecute) always, with nothing to turn it off; on Linux and
// the BSDs through $BROWSER, else xdg-open where a display is. There it is
// given a $BROWSER that opens nothing, and the one who asked (magpie, which
// knows where the user's browser is) opens it; here the sign-in says the
// page is open already, so it isn't opened a second time (andyfmyth on X:
// "每次都会弹两次验证").
const opensPage = () => process.platform === "darwin" || process.platform === "win32"

// login runs `grok login --device-auth` in home and hands on the first link
// it prints; done is settled when the command is.
function login(exe, home) {
  const env = envFor(home)
  if (!opensPage()) env.BROWSER = "true"
  const child = spawn(exe, ["login", "--device-auth"], {
    cwd: homedir(),
    env,
    stdio: ["ignore", "pipe", "pipe"],
  })
  const tail = []
  let buf = ""
  let found
  const link = new Promise((resolve) => (found = resolve))
  const onData = (chunk) => {
    buf += chunk.toString()
    let i
    while ((i = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, i).replace(/\r$/, "").replace(ANSI, "").trim()
      buf = buf.slice(i + 1)
      if (line) tail.push(line)
      const u = linkIn(line)
      if (u) found(u)
    }
  }
  child.stdout.on("data", onData)
  child.stderr.on("data", onData)
  const done = new Promise((resolve) => {
    child.on("error", (e) => {
      tail.push(e.message)
      found("")
      resolve({ ok: false, why: e.message })
    })
    child.on("close", (code) => {
      const rest = buf.replace(ANSI, "").trim()
      if (rest) {
        tail.push(rest)
        const u = linkIn(rest)
        if (u) found(u)
      }
      found("")
      resolve({ ok: code === 0, why: tail.at(-1) ?? "grok login didn't finish" })
    })
  })
  return { child, link, done, tail }
}

function newHome() {
  const home = join(ownHomes(), randomBytes(9).toString("base64url").replace(/[-_]/g, "").toLowerCase())
  mkdirSync(home, { recursive: true, mode: 0o700 })
  return home
}

// signIn signs in with the CLI's device code: in the CLI's own home when
// that isn't signed in, else in a home of this plugin's, so the CLI's own
// sign-in stays as it is.
async function signIn() {
  const exe = executable()
  if (!exe) throw new Error(NO_CLI)
  // a sign-in mid-write is one: the CLI's own home is left be
  const own = await credential(cliHome()).then((c) => !!c, () => true)
  const home = own ? newHome() : cliHome()
  const { child, link, done, tail } = login(exe, home)
  const url = await Promise.race([link, new Promise((r) => setTimeout(() => r(""), LINK_WAIT))])
  if (!url) {
    child.kill()
    if (own) rmSync(home, { recursive: true, force: true })
    // what it said last is why: where x.ai is out of reach without a
    // proxy, it says it couldn't reach it (𝕏 on Discord)
    const why = tail.at(-1)
    throw new Error(why ? `grok login gave no link to open: ${why}` : "grok login gave no link to open and said nothing: can this machine reach auth.x.ai? Set a proxy in magpie's Settings if it needs one")
  }
  return {
    url,
    // not OpenCode's: the page is open already, by Grok Build itself
    opened: opensPage(),
    instructions: "Approve the sign-in on the page; Grok Build finishes it.",
    method: "auto",
    callback: async () => {
      const r = await done
      const { c, why } = r.ok ? await read(home) : { c: null, why: "" }
      // a sign-in beside the CLI's own needs to know whose it is, so the
      // same account signed in again replaces it instead of adding a second
      if (!c || (own && !c.who)) {
        if (own) rmSync(home, { recursive: true, force: true })
        if (!r.ok) return { type: "failed", error: r.why }
        const said = tail.at(-1) ? ` (it said "${tail.at(-1)}")` : ""
        return {
          type: "failed",
          error: !c
            ? `grok login finished${said}, but left no sign-in where this plugin reads it: ${why}. Run \`grok login\` in a terminal, then add Grok with "Use Grok Build's current sign-in"`
            : `grok login finished${said}, but its sign-in in ${join(home, "auth.json")} names no account (no email, user id or token subject), so it can't be told from the Grok Build sign-in in ${cliHome()}. Sign in to that one with \`grok login\` and add it with "Use Grok Build's current sign-in"`,
        }
      }
      return saved(home, c)
    },
  }
}

function cpaClaims(token) {
  try {
    const part = token.split(".")[1]
    if (!part) return null
    return JSON.parse(Buffer.from(part, "base64url").toString("utf8"))
  } catch {
    return null
  }
}

function cpaAuthFromFile(file) {
  let source
  try {
    source = JSON.parse(readFileSync(file, "utf8"))
  } catch {
    throw new Error("CPA Grok account file is not valid JSON")
  }
  if (!source || typeof source !== "object" || Array.isArray(source) || source.type !== "xai" || source.auth_kind !== "oauth" || source.disabled !== false) {
    throw new Error("CPA file is not an enabled xAI OAuth account")
  }
  if (typeof source.access_token !== "string" || !source.access_token || typeof source.refresh_token !== "string" || !source.refresh_token || typeof source.sub !== "string" || !source.sub || typeof source.email !== "string" || !source.email) {
    throw new Error("CPA xAI account is missing its tokens or identity")
  }
  if (typeof source.last_refresh !== "string" || typeof source.expired !== "string") {
    throw new Error("CPA xAI account has invalid token timestamps")
  }
  const createTime = Date.parse(source.last_refresh)
  const expiresAt = Date.parse(source.expired)
  if (!Number.isFinite(createTime) || !Number.isFinite(expiresAt)) {
    throw new Error("CPA xAI account has invalid token timestamps")
  }
  const claims = cpaClaims(source.access_token)
  const audience = Array.isArray(claims?.aud) ? claims.aud : [claims?.aud]
  if (claims?.iss !== "https://auth.x.ai" || !audience.includes(CPA_CLIENT_ID)) {
    throw new Error("CPA token is not for the Grok Build xAI account")
  }
  return {
    key: source.access_token,
    auth_mode: "oidc",
    create_time: new Date(createTime).toISOString(),
    user_id: source.sub,
    email: source.email,
    refresh_token: source.refresh_token,
    expires_at: new Date(expiresAt).toISOString(),
    oidc_issuer: "https://auth.x.ai",
    oidc_client_id: CPA_CLIENT_ID,
  }
}

async function importCPA(inputs) {
  if (!executable()) throw new Error(NO_CLI)
  const rawPath = String(inputs?.path ?? "").trim()
  const file = resolve(rawPath === "~" ? homedir() : rawPath.startsWith("~/") ? join(homedir(), rawPath.slice(2)) : rawPath)
  if (!isFile(file)) throw new Error("choose a CPA Grok account JSON file")
  const auth = cpaAuthFromFile(file)
  return {
    url: "",
    instructions: "The account is ready to import. Stop using it in CPA before continuing; both clients rotate the same refresh token.",
    method: "auto",
    callback: async () => {
      const home = newHome()
      try {
        writeFileSync(join(home, "auth.json"), JSON.stringify({ [CPA_SCOPE]: auth }, null, 2), { mode: 0o600 })
      } catch (error) {
        rmSync(home, { recursive: true, force: true })
        throw error
      }
      return saved(home, await credential(home))
    },
  }
}

// saved is the sign-in as OpenCode keeps it: refresh is the home the CLI
// keeps it in, access the token it had then.
function saved(home, c) {
  return { type: "success", refresh: home, access: c.key, expires: c.expires, accountId: c.who }
}

// current takes the sign-in the CLI already has, as it is.
async function current() {
  if (!executable()) throw new Error(NO_CLI)
  const home = cliHome()
  if (!(await credential(home))) throw new Error("Grok Build is not signed in; run `grok login`, or sign in with the device code")
  const c = await token(home)
  return {
    url: "",
    instructions: c.email ? `Grok Build is signed in as ${c.email}.` : "Grok Build is signed in.",
    method: "auto",
    callback: async () => saved(home, c),
  }
}

// ---- usage ----------------------------------------------------------------------

// How much of the subscription's allowance is gone, as magpie's built-in
// Grok account shows it (internal/provider/grok_usage.go) and the CLI's own
// /usage reads it: the credits of the current period, weekly for
// SuperGrok, and what on-demand spending has used of its cap.

const PERIODS = { DAILY: ["1 day", 24 * 3600], WEEKLY: ["7 days", 7 * 24 * 3600], MONTHLY: ["Month", 30 * 24 * 3600] }
const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/

// statusText is a refused request's status as magpie says it (Go's
// http.StatusText).
// http.StatusText); one Go has no words for is "HTTP <n>", never empty.
const statusText = (s) =>
  ((GO_TEXT[s] ?? STATUS_CODES[s]) || `HTTP ${s}`)
// where Go's words differ from Node's (509 Go has none for)
const GO_TEXT = { 413: "Request Entity Too Large", 414: "Request URI Too Long", 416: "Requested Range Not Satisfiable", 418: "I'm a teapot", 509: "" }

// netError is a request that got no answer, as Go's http client words it
// (`Get "<url>": …`), with the words magpie's keepLast looks for to keep
// the card it had: no such host, connection refused, timeout, EOF.
function netError(url, e) {
  const m = String(e?.message ?? e)
  const code = String(e?.code ?? e?.cause?.code ?? "")
  let why = m
  if (e?.name === "TimeoutError" || e?.name === "AbortError" || /timed? ?out/i.test(m)) why = "timeout"
  else if (code === "ENOTFOUND" || code === "EAI_AGAIN" || /getaddrinfo|ENOTFOUND|resolve|DNS/i.test(m)) why = "dial tcp: lookup: no such host"
  else if (code === "ECONNREFUSED" || /ECONNREFUSED|Unable to connect|refused/i.test(m)) why = "dial tcp: connection refused"
  else if (/ECONNRESET|socket|closed|fetch failed/i.test(m + code)) why = "EOF"
  return `Get "${url}": ${why}`
}

// jsonError is JSON that won't read, as Go's encoding/json says it.
function jsonError(text) {
  const c = text.trimStart()[0]
  return c === undefined ? "unexpected end of JSON input" : `invalid character '${c}' looking for beginning of value`
}

async function usage(key) {
  const url = `${BASE}/billing?format=credits`
  let res, text
  try {
    res = await fetch(url, {
      headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
      signal: AbortSignal.timeout(15_000),
    })
    if (res.ok) text = await res.text()
  } catch (e) {
    return { error: netError(url, e), windows: [] }
  }
  if (!res.ok) return { error: statusText(res.status), windows: [], status: res.status }
  let data
  try {
    data = JSON.parse(text)
  } catch {
    return { error: jsonError(text), windows: [] }
  }
  const cfg = data?.config ?? {}
  const num = (v) => (typeof v === "number" ? v : 0)
  let [name, span] = ["Allowance", 0]
  let end = cfg.billingPeriodEnd
  if (cfg.currentPeriod) {
    ;[name, span] = PERIODS[String(cfg.currentPeriod.type ?? "").replace(/^USAGE_PERIOD_TYPE_/, "")] ?? ["Allowance", 0]
    end = cfg.currentPeriod.end
  }
  const at = typeof end === "string" && RFC3339.test(end) && !Number.isNaN(Date.parse(end)) ? { resetsAt: end } : {}
  const out = [{ name, used: num(cfg.creditUsagePercent), ...(span ? { span } : {}), ...at }]
  const cap = num(cfg.onDemandCap?.val)
  if (cap > 0) out.push({ name: "On-demand", used: (100 * num(cfg.onDemandUsed?.val)) / cap, ...at, aside: true })
  return { windows: out }
}

// ---- rate limit -------------------------------------------------------------------

// Grok's billing says nothing of a plan's rate limit: a free account's
// credits read 0% used while every request comes back 429. So the account's
// last 429 is kept here, by its CLI home, and usage shows a "Rate limit"
// window spent until the reset the 429 named (Retry-After, or a
// *ratelimit*reset* header), or, when it named none, until a request goes
// through or HOLD_MS has passed. Aside: magpie's gateway already rests an
// account that answered 429; this is only what the card says.
const HOLD_MS = 5 * 60_000
const limited = new Map()

// resetOf is when a 429 says the limit lifts, in ms since the epoch, or 0.
function resetOf(headers, now) {
  const ra = headers.get("retry-after")
  if (ra) {
    const n = Number(ra)
    if (Number.isFinite(n) && n >= 0) return now + n * 1000
    const d = Date.parse(ra)
    if (!Number.isNaN(d)) return d
  }
  for (const [k, v] of headers) {
    if (!/ratelimit.*reset/i.test(k)) continue
    const n = Number(v)
    if (!Number.isFinite(n) || n < 0) continue
    // a time (epoch ms or seconds) or seconds from now
    if (n > 1e12) return n
    if (n > 1e9) return n * 1000
    return now + n * 1000
  }
  return 0
}

// note keeps what a request's answer says of the rate limit.
function note(home, res, now = Date.now()) {
  if (res.status === 429) limited.set(home, { at: now, until: resetOf(res.headers, now) })
  else if (res.ok) limited.delete(home)
}

// rateWindow is the window a recent 429 makes, or none.
function rateWindow(home, now = Date.now()) {
  const l = limited.get(home)
  if (!l) return []
  if (l.until ? l.until <= now : now - l.at >= HOLD_MS) {
    limited.delete(home)
    return []
  }
  const end = l.until || l.at + HOLD_MS
  return [{ name: "Rate limit", used: 100, resetsAt: new Date(end).toISOString(), aside: true }]
}

// kept is res saying magpie is to leave the account's sign-in be.
function kept(res) {
  const headers = new Headers(res.headers)
  headers.delete("content-length")
  headers.delete("content-encoding")
  headers.set("X-Magpie-Sign-In", "kept")
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}

// ---- the plugin ---------------------------------------------------------------

export const GrokAuthPlugin = async ({ client, magpie }) => {
  const remember = async (auth, c) => {
    if (auth.access === c.key && auth.expires === c.expires) return
    try {
      await client.auth.set({ path: { id: PROVIDER }, body: { ...auth, access: c.key, expires: c.expires, accountId: c.who || auth.accountId } })
    } catch {}
  }

  return {
    config: async (cfg) => {
      cfg.provider ??= {}
      cfg.provider[PROVIDER] ??= {
        name: "Grok (SuperGrok)",
        npm: "@ai-sdk/openai",
        api: BASE,
        models: DEFAULT_MODELS,
      }
    },

    auth: {
      provider: PROVIDER,
      options: [
        {
          key: "x_search",
          label: "Native X search",
          description: "Offers X search to Grok when tools are automatic. Keeps existing search filters and explicit tool choices. Grok decides whether to search.",
        },
      ],
      // magpie renews the sign-in LEAD_MS before its end, once for the
      // account, before its requests, models and usage ask for it: the CLI
      // renews it in its home, as before a request, and what it gives is
      // read back. The check before each request stays for OpenCode, which
      // doesn't call this.
      refreshLead: LEAD_MS,
      async refresh(auth) {
        const home = auth?.refresh
        if (auth?.type !== "oauth" || !home) return undefined
        let c = await credential(home)
        // renewed already (by the CLI, or before a request), the store not
        // yet saying so
        const renewed = c && c.key !== auth.access && c.expires - Date.now() >= LEAD_MS
        const exe = executable()
        let r
        if (!renewed && exe) {
          r = await renew(exe, home)
          c = await credential(home)
        }
        // the CLI holds no sign-in there any more (`grok logout`): one
        // has to be made again
        if (!c) throw Object.assign(new Error(notSignedIn(home)), { signIn: "expired" })
        // the CLI couldn't renew it: tried again later
        if (exe && c.expires && c.expires <= Date.now()) throw unrenewed(home, r)
        if (c.key === auth.access && c.expires === auth.expires) return undefined
        // magpie saves what this gives
        const out = { access: c.key, expires: c.expires }
        if (c.email && c.email !== auth.accountId) out.accountId = c.email
        return out
      },
      async loader(getAuth) {
        const auth = await getAuth()
        if (!auth || auth.type !== "oauth") return {}
        return {
          baseURL: BASE,
          apiKey: "grok",
          async fetch(input, init) {
            const auth = await getAuth()
            const home = auth?.refresh
            if (!home) throw new Error("Grok is not signed in; run `grok login`")
            const req = new Request(input, init)
            const search = new URL(req.url).pathname.endsWith("/responses") && magpie?.option?.(PROVIDER, "x_search") === true
            let body = rewrite(bodyText(init?.body), search)
            let parsed = {}
            if (typeof body === "string") {
              try {
                parsed = JSON.parse(body) ?? {}
              } catch {}
            }
            let c = await token(home)
            await remember(auth, c)
            const headers = new Headers(req.headers)
            const asked = modelOf(parsed.model)
            if (asked.model) {
              headers.set("x-grok-model-override", asked.model)
              if (asked.window && typeof body === "string") {
                parsed.model = asked.model
                body = JSON.stringify(parsed)
              }
            }
            if (asked.window) headers.set("x-grok-context-window", String(asked.window))
            if (typeof parsed.prompt_cache_key === "string" && parsed.prompt_cache_key) headers.set("x-grok-conv-id", parsed.prompt_cache_key)
            headers.delete("content-length")
            const send = async (key) => {
              await sign(headers, key)
              return fetch(req.url, { ...init, method: req.method, headers, body })
            }
            // Grok's answer goes on as it came, the account kept, as the
            // built-in never marked a Grok account lapsed nor cleared one.
            // A token Grok turns away before its time (it can, ahead of
            // expires_at) is renewed by Grok Build and the request sent once
            // more; one Grok Build couldn't renew is a 401 saying so, and
            // what helps.
            let res = await send(c.key)
            if (res.status === 401 && (typeof body === "string" || body == null)) {
              try {
                c = await token(home, c.key)
              } catch (e) {
                const said = (await res.text().catch(() => "")).slice(0, 300)
                note(home, res)
                return kept(
                  new Response(JSON.stringify({ error: { message: `${e.message}${said ? `. Grok said: ${said}` : ""}`, type: "authentication_error", code: "sign_in_unrenewed" } }), {
                    status: 401,
                    statusText: res.statusText,
                    headers: { "content-type": "application/json" },
                  }),
                )
              }
              await res.body?.cancel().catch(() => {})
              await remember(auth, c)
              res = await send(c.key)
            }
            note(home, res)
            return kept(res)
          },
        }
      },
      methods: [
        {
          type: "oauth",
          label: "Sign in with Grok Build (device code)",
          authorize: signIn,
        },
        {
          type: "oauth",
          label: "Use Grok Build's current sign-in",
          authorize: current,
        },
        {
          type: "oauth",
          label: "Import CPA Grok account JSON",
          prompts: [
            {
              type: "text",
              key: "path",
              message: "Path to the CPA xAI account JSON. Stop using this account in CPA after import; its refresh token rotates.",
              placeholder: "~/.cli-proxy-api/xai-<email>.json",
              validate: (v) => (String(v ?? "").trim() ? undefined : "Enter the path to a CPA xAI account JSON file"),
            },
          ],
          authorize: importCPA,
        },
      ],
      // magpie's: how much of the subscription's allowance is gone; each
      // read keeps the sign-in (signIn), as the built-in's marked no
      // account lapsed nor cleared one
      async usage(getAuth) {
        const auth = await getAuth()
        let c
        try {
          if (!auth || auth.type !== "oauth" || !auth.refresh) throw new Error("Grok is not signed in; run `grok login`")
          c = await token(auth.refresh)
        } catch (e) {
          return { error: e.message, windows: [], signIn: "kept" }
        }
        await remember(auth, c)
        let u = await usage(c.key)
        // a token turned away is renewed once, as a request's is
        if (u.status === 401) {
          try {
            c = await token(auth.refresh, c.key)
          } catch (e) {
            return { error: e.message, windows: [], signIn: "kept" }
          }
          await remember(auth, c)
          u = await usage(c.key)
        }
        delete u.status
        if (!u.error) u.windows = [...rateWindow(auth.refresh), ...u.windows]
        return { ...u, signIn: "kept" }
      },
    },

    provider: {
      id: PROVIDER,
      async models(provider, { auth } = {}) {
        const have = provider?.models ?? {}
        if (!auth || auth.type !== "oauth" || !auth.refresh) return have
        // a list Grok couldn't give is a failure, not the few configured:
        // magpie keeps the list it had
        let c = await token(auth.refresh)
        let list
        try {
          list = await listModels(c.key)
        } catch (e) {
          if (e?.status !== 401) throw e
          // turned away before its time: renewed once, as a request's is
          c = await token(auth.refresh, c.key)
          list = await listModels(c.key).catch((e) => {
            throw e?.status === 401 ? new Error(dead(auth.refresh)) : e
          })
        }
        const base = have["grok-4.7"] ?? Object.values(have)[0] ?? {}
        const out = {}
        for (const m of list) {
          const was = have[m.id] ?? have[m.apiId] ?? base
          out[m.id] = {
            ...was,
            id: m.id,
            providerID: provider.id,
            name: m.name,
            // the id as offered, window and all: magpie and the AI SDK send
            // api.id as the body's model, and fetch reads the window from it
            // (modelOf) before Grok sees the plain model
            api: { ...(was.api ?? {}), id: m.id, url: BASE, npm: "@ai-sdk/openai" },
            limit: { ...(was.limit ?? {}), context: m.context || was.limit?.context || 0, output: was.limit?.output ?? 0 },
            capabilities: {
              ...(was.capabilities ?? {}),
              reasoning: m.efforts.length > 0 || !!was.capabilities?.reasoning,
              attachment: true,
              toolcall: true,
              input: { ...(was.capabilities?.input ?? {}), text: true, image: true },
            },
            variants: Object.fromEntries(m.efforts.map((e) => [e, { reasoningEffort: e }])),
          }
        }
        return out
      },
    },
  }
}

// for tests
export const _internal = { rewrite, objectRoot, bodyText, limited, HOLD_MS, windowsOf, modelOf }
