/**
 * 国际版网页 OAuth —— 完整本地实现，不依赖 workbuddy2api / opencodex 面板。
 *
 * 宿主协议形态：authorize(inputs) 启动本地临时 HTTP 服务器（127.0.0.1 上
 * 官方 TokenServer 端口池之一），立即返回 { url: 本地引导页, method: "auto", callback }；
 * 浏览器全流程在本地页完成：
 *
 *   引导页(内嵌) ──拉 /captcha-config──▶ 未启用滑块 ──▶ 直接「继续授权」
 *                     │启用
 *                     ▼
 *              加载 AliyunCaptcha SDK(CDN) → 用户拖滑块 → 回执 POST /captcha
 *                     ▼
 *   「继续授权」──POST /open-auth──▶ 插件带回执调上游 {vendor}-oauth-url
 *                     ▼
 *        页面自动跳授权页（Google/z.ai）→ 302 回 /callback?code=…&state=…
 *                     ▼
 *        callback() 里用 code 调 {vendor}-oauth-login 换 token → {type:"success"}
 */

import { createHash, randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import { Buffer } from "node:buffer";
import * as http from "node:http";

const APP_ID = "100003";
const APP_KEY = "38d2391985e2369a5fb8227d8e6cd5e5";
const PRODUCT = "autoclaw";
const CHANNEL = "AutoClaw4";
const CLIENT_VERSION = "1.18.1";
const CN_CHANNEL = "AutoClaw26";
const CN_CLIENT_VERSION = "2.1.0";
const PLATFORM = process.platform === "darwin" ? "mac" : process.platform === "win32" ? "win" : "linux";
/** X-Tm: the official Linux client sends "linux-x64"; the overseas gateway
 * answers the bare "linux" with 403 {"message":"forbidden"} (#367). */
function platformTm(platform = process.platform) {
  return platform === "darwin" ? "mac" : platform === "win32" ? "win" : "linux-x64";
}
const PLATFORM_TM = platformTm();
const USER_AGENT = `AutoClaw/${CLIENT_VERSION} (${PLATFORM}-${process.arch})`;

const GLOBAL_HOST = "https://autoglm-api.autoglm.ai";
const CN_HOST = "https://autoglm-api.zhipuai.cn";

const CHAT_PATH = "/autoclaw-proxy/proxy/autoclaw/v1/chat/completions";
const CN_CHAT_PATH = "/autoclaw-proxy/proxy/autoclaw/chat/completions";
const MANAGED_HOST = "https://autoglm-acceleration-api.zhipuai.cn";
const OFFICIAL_SYSTEM_PROMPT = 'You are AutoClaw. Answer the user directly and concisely.';
const WALLETS_PATH = `/agent-assetmgr/api/v2/wallets?biz_app_id=${PRODUCT}`;

const FLAGGED = [
  // Claude Code 2.1.30x opens with an SDK identity line, not the CLI one;
  // the CN gateway refuses "Claude Agent SDK" in any case with 406. Take the
  // trailing period and the article/possessive so the replacement reads as
  // a sentence, not "assistant.." or "Anthropic's the agent framework".
  [/You are a Claude agent, built on Anthropic's Claude Agent SDK\.?/gi, "You are a coding assistant."],
  [/(?:Anthropic's |the )?Claude Agent SDK/gi, "the agent framework"],
  [/You are an AI agent powered by DeepSeek Harness/gi, "You are an AI agent powered by DeepSeek-Harness"],
  [/You are Claude Code/gi, "You are the Claude Code CLI"],
  [/You are Codex/gi, "You are the Codex agent"],
  [/Codex CLI/gi, "Codex command-line tool"],
  [/You are opencode/gi, "You are the opencode agent"],
  [/You are Cursor/gi, "You are the Cursor agent"],
  [/You are Cline/gi, "You are the Cline agent"],
  [/You are Qwen Code/gi, "You are the Qwen Code agent"],
  [/You are ZCode/gi, "You are the ZCode agent"],
  [/operating inside pi, a coding agent harness/gi, "operating inside the pi coding agent harness"],
];

function unflagged(text) {
  for (const [re, to] of FLAGGED) text = text.replace(re, to);
  return text;
}

function unflag(messages) {
  let changed = false;
  for (const m of messages) {
    if (!m || (m.role !== "system" && m.role !== "developer")) continue;
    if (typeof m.content === "string") {
      const t = unflagged(m.content);
      if (t !== m.content) {
        m.content = t;
        changed = true;
      }
    } else if (Array.isArray(m.content)) {
      for (const part of m.content) {
        if (typeof part?.text !== "string") continue;
        const t = unflagged(part.text);
        if (t !== part.text) {
          part.text = t;
          changed = true;
        }
      }
    }
  }
  return changed;
}

const REFUSED_HINT =
  "AutoClaw refuses chats whose system prompt carries another agent's identity line (Claude Code, Claude Agent SDK, Codex, opencode, Cursor, Cline, Qwen Code, ZCode, pi, DeepSeek Harness); this plugin rewrites the ones it knows, so an unknown one needs another provider in this group";

async function explained(res) {
  if (res.status < 400) return res;
  const headers = new Headers(res.headers);
  headers.delete("content-length");
  headers.delete("content-encoding");
  headers.set("X-Magpie-Sign-In", "kept");
  const text = await res.text().catch(() => "");
  if (res.status !== 406) return new Response(text, { status: res.status, statusText: res.statusText, headers });
  let said = "";
  try {
    said = JSON.parse(text)?.error?.message ?? "";
  } catch {}
  headers.set("content-type", "application/json");
  return new Response(
    JSON.stringify({ error: { message: `${said || "406 Not Acceptable"} — ${REFUSED_HINT}`, type: "permission_error", code: null } }),
    { status: res.status, statusText: res.statusText, headers },
  );
}

// A 403 is not always the sign-in: the CN managed gateway answers an
// exhausted quota 403 {"code":810000,"message":"GLM-5.3 免费额度已用完,…"}
// while the same token's next request with an unknown model gets as far as
// "非法模型" (plugins#274). Such a refusal is the account's quota: it goes on
// as a 429 with the gateway's own words, and the sign-in is kept.
const QUOTA_CODES = new Set(["810000"]);
const QUOTA_WORDS = /额度|余额不足|用完|用尽|quota|insufficient[_ ]?(balance|credit)|exhausted/i;

/** said is the code and message of a parsed refusal body, read from its code/message fields only. */
function said(d) {
  if (!d || typeof d !== "object") return { code: null, message: "" };
  return {
    code: d.code ?? d.error?.code ?? null,
    message: String(d.message ?? d.msg ?? d.error?.message ?? "").trim(),
  };
}

/** quotaRefusal is a 403 chat answer read as a spent quota, or null when it isn't one (a 401 or any other 403 is still the sign-in). */
async function quotaRefusal(res) {
  if (res.status !== 403) return null;
  const text = await res.clone().text().catch(() => "");
  let d;
  try { d = JSON.parse(text); } catch { return null; }
  if (!d || typeof d !== "object") return null;
  const { code, message } = said(d);
  if (!QUOTA_CODES.has(String(code)) && !(message && QUOTA_WORDS.test(message))) return null;
  try { res.body?.cancel?.(); } catch {}
  return new Response(JSON.stringify({ error: { message: `AutoClaw: ${message || "quota exhausted"}`, type: "rate_limit_error", code } }), {
    status: 429,
    headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "kept" },
  });
}

// A 403 can be the account itself: a banned AutoClaw account answers
// HTTP 403 {"code":410004,"message":"The account has been banned"} on every
// route (小八, Discord 2026-10-23). That is no expired sign-in: signing in
// again gets the same account and the same refusal, and reading it as
// expired made magpie drop the token while the user never saw why. It goes
// on as a 403 with the vendor's words, and the sign-in is kept. Only the
// body's code or message field is read, never the whole body.
const BANNED_CODES = new Set(["410004"]);
const BANNED_WORDS = /\bbanned\b|封禁|封号|账号异常|账户异常|account (has been |is |was )?(suspended|frozen|disabled|blocked|restricted)|账[号户](已|已被|被)?(冻结|停用|禁用)/i;

/** bannedSaid is the vendor's words when a parsed body is an account-level refusal (a ban), or "" when it isn't one. */
function bannedSaid(d) {
  const { code, message } = said(d);
  if (BANNED_CODES.has(String(code))) return message || `account refused (code ${code})`;
  return message && BANNED_WORDS.test(message) ? message : "";
}

/** bannedText is bannedSaid over a raw body text. */
function bannedText(text) {
  let d;
  try { d = JSON.parse(text); } catch { return ""; }
  return bannedSaid(d);
}

/** accountRefusal is a 401/403 chat answer read as the account refused (banned), or null when it isn't one. */
async function accountRefusal(res) {
  if (res.status !== 401 && res.status !== 403) return null;
  const text = await res.clone().text().catch(() => "");
  const message = bannedText(text);
  if (!message) return null;
  let code = null;
  try { code = said(JSON.parse(text)).code; } catch {}
  try { res.body?.cancel?.(); } catch {}
  return new Response(JSON.stringify({ error: { message: `AutoClaw: ${message}`, type: "permission_error", code } }), {
    status: 403,
    headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "kept" },
  });
}

/** refusedNotSignIn is a 401/403 that is the account's (quota or ban), not its sign-in, or null. */
async function refusedNotSignIn(res) {
  return (await quotaRefusal(res)) ?? (await accountRefusal(res));
}

function autoclawRewriteBody(rawBody) {
  let payload;
  try {
    payload = JSON.parse(typeof rawBody === "string" ? rawBody : new TextDecoder().decode(rawBody));
  } catch {
    return null;
  }
  if (!payload || typeof payload !== "object" || !Array.isArray(payload.messages)) return null;

  unflag(payload.messages);

  const idx = payload.messages.findIndex((m) => m && m.role === "system");
  if (idx >= 0) {
    const cur = payload.messages[idx].content;
    if (typeof cur === "string") {
      if (!cur.startsWith(OFFICIAL_SYSTEM_PROMPT)) {
        payload.messages[idx] = { ...payload.messages[idx], content: `${OFFICIAL_SYSTEM_PROMPT}\n\n${cur}` };
      }
    } else if (Array.isArray(cur)) {
      payload.messages[idx] = {
        ...payload.messages[idx],
        content: [{ type: "text", text: OFFICIAL_SYSTEM_PROMPT }, ...cur],
      };
    } else {
      payload.messages[idx] = { ...payload.messages[idx], content: OFFICIAL_SYSTEM_PROMPT };
    }
  } else {
    payload.messages = [{ role: "system", content: OFFICIAL_SYSTEM_PROMPT }, ...payload.messages];
  }

  delete payload.max_tokens;
  delete payload.max_completion_tokens;
  return payload;
}


const OAUTH_EXPIRY_SKEW_MS = 10 * 60 * 1000;
const FALLBACK_EXPIRES_IN_S = 5 * 3600;
/** 本地 OAuth 会话上限：callback 的等待是 5 分钟；宿主不调 callback 时兜底释放端口。 */
const OAUTH_SESSION_TIMEOUT_MS = 10 * 60 * 1000;

const VARIANTS = Object.fromEntries(["low", "high", "max"].map((effort) => [effort, { reasoningEffort: effort }]));

/** 模型清单：键 = magpie 里显示的 id；值里的 id = 上游接受的 routeModelId。 */
function overseasModels() {
  // Live verified overseas catalog: zai_glm-5-turbo and tdpsk_deepseek-* are gone upstream (400 非法模型).
  return {
    "zai_auto": { name: "GLM Auto (路由)", id: "zai_auto", limit: { context: 200000, output: 32000 }, reasoning: true, tool_call: true },
    "zai_auto-fast": { name: "GLM Auto Fast", id: "zai_auto-fast", limit: { context: 200000, output: 32000 }, reasoning: true, tool_call: true },
    "zaicoding_glm-5.3": { name: "GLM-5.3 Coding", id: "zaicoding_glm-5.3", limit: { context: 1000000, output: 131072 }, reasoning: true, tool_call: true },
    "zai_glm-5.3-flash": { name: "GLM-5.3 Flash", id: "zai_glm-5.3-flash", limit: { context: 1000000, output: 32000 }, reasoning: true, tool_call: true },
  };
}

/** The CN live managed-model catalog, saved by AutoClaw 2.1.0. */
function managedModels() {
  return {
    "zai_auto": { name: "Auto", id: "zai_auto", limit: { context: 1048576, output: 393216 }, reasoning: true, attachment: true, tool_call: true, variants: VARIANTS },
    "zai_auto-fast": { name: "Auto-Fast", id: "zai_auto-fast", limit: { context: 1048576, output: 131072 }, reasoning: true, attachment: true, tool_call: true, variants: VARIANTS },
    "zaicoding_glm-5.3": { name: "GLM-5.3", id: "zaicoding_glm-5.3", limit: { context: 1048576, output: 307200 }, reasoning: true, attachment: false, tool_call: true, variants: VARIANTS },
    "zai_glm-5.3-flash": { name: "GLM-5.3-Flash", id: "zai_glm-5.3-flash", limit: { context: 1048576, output: 131072 }, reasoning: true, attachment: true, tool_call: true, variants: VARIANTS },
  };
}

function autoclawModels(providerId = "autoclaw") {
  return providerId === "autoclaw-cn" ? managedModels() : overseasModels();
}

// ---------------------------------------------------------------------------
// 基础工具
// ---------------------------------------------------------------------------

function md5Hex(input) {
  return createHash("md5").update(input).digest("hex");
}

function randomHex(n) {
  return randomBytes(n).toString("hex");
}

/** 64 位十六进制 device_id（官方客户端形态）。 */
function newDeviceId() {
  return randomHex(32);
}

/** 业务签名头（task/wallet/登录/refresh），WAF 要求带客户端 UA。 */
function bizHeaders(accessToken) {
  const ts = String(Math.floor(Date.now() / 1000));
  const h = {
    "Content-Type": "application/json",
    "Accept": "*/*",
    "User-Agent": USER_AGENT,
    "X-Version": CLIENT_VERSION,
    "X-Tm": PLATFORM_TM,
    "X-Product": PRODUCT,
    "X-Auth-Appid": APP_ID,
    "X-Auth-TimeStamp": ts,
    "X-Auth-Sign": md5Hex(`${APP_ID}&${ts}&${APP_KEY}`),
    "X-Trace-Id": randomHex(16),
    "X-Lang": "zh-CN",
    "X-Channel": CHANNEL,
    "Origin": "http://localhost:18432",
    "Referer": "http://localhost:18432/",
  };
  if (accessToken) h["Authorization"] = `Bearer ${accessToken}`;
  return h;
}

function nonEmptyString(v) {
  return typeof v === "string" && v.length > 0 ? v : undefined;
}

/** 剥掉 token 上误带的 "Bearer " 前缀（老凭据/代理回包可能带；7863 侧同样的容错）。 */
function bareToken(v) {
  let t = String(v ?? "").trim();
  while (t.toLowerCase().startsWith("bearer ")) t = t.slice(7).trim();
  return t;
}

function dedupe(values) {
  return [...new Set(values.filter(Boolean))];
}

/** 读 JWT payload 字段（不校验签名——只取自己账号的 device_id / exp）。 */
function jwtClaim(token, key) {
  const raw = bareToken(token);
  const part = raw.split(".")[1];
  if (!part) return undefined;
  try {
    const json = Buffer.from(part.replace(/-/g, "+").replace(/_/g, "/"), "base64").toString("utf8");
    return JSON.parse(json)[key];
  } catch {
    return undefined;
  }
}

/** access token 真实过期时间（epoch ms）。 */
function jwtExpiresAtMs(token) {
  const exp = jwtClaim(token, "exp");
  return typeof exp === "number" && Number.isFinite(exp) && exp > 0 ? exp * 1000 : undefined;
}

async function callEnvelope(path, { method = "GET", body, base }) {
  let response, text;
  try {
    response = await fetch(`${base}${path}`, {
      method,
      headers: bizHeaders(),
      ...(body !== undefined ? { body } : {}),
    });
    text = await response.text();
  } catch (cause) {
    throw Object.assign(new Error(`AutoClaw request failed: ${cause?.message ?? cause}`), { cause });
  }
  let parsed;
  try { parsed = text ? JSON.parse(text) : undefined; } catch { parsed = undefined; }
  if (!parsed || (parsed.code === undefined && response.status >= 400)) {
    const banned = bannedSaid(parsed);
    const err = new Error(banned
      ? `AutoClaw: ${banned}`
      : `AutoClaw ${path.split("?")[0]} non-envelope response (HTTP ${response.status}): ${text.slice(0, 200)}`);
    // 401/403 不论响应体形态都是登录失效（账号被封除外：那是账号，不是登录）
    if (response.status === 401 || response.status === 403) err.signIn = banned ? "kept" : "expired";
    throw err;
  }
  return { status: response.status, code: parsed.code, msg: parsed.msg ?? parsed.message, data: parsed.data, banned: bannedSaid(parsed) };
}

/** 验证码必须按数字发送（服务端按类型校验，字符串 → 400001）。 */
function codeValue(code) {
  const s = (code ?? "").trim();
  return /^\d+$/.test(s) ? Number(s) : s;
}

/** token 响应 → magpie 凭据字段（不含 type——由 magpie 按 oauth 方式包装）。 */
function tokenFields(data, fallbackRefresh) {
  const access = bareToken(nonEmptyString(data.access_token));
  if (!access) throw new Error("AutoClaw token response contained no access_token");
  const refresh = bareToken(nonEmptyString(data.refresh_token) ?? fallbackRefresh);
  if (!refresh) throw new Error("AutoClaw token response contained no refresh_token");
  // expires_in 常缺省；JWT exp 才是事实来源（约 24h）。按 5h 记会白跑 refresh（轮换有吊销风险）。
  const jwtExpires = jwtExpiresAtMs(access);
  const expiresInS = typeof data.expires_in === "number" && data.expires_in > 0 ? data.expires_in : undefined;
  const expires = jwtExpires !== undefined
    ? jwtExpires - OAUTH_EXPIRY_SKEW_MS
    : Date.now() + (expiresInS ?? FALLBACK_EXPIRES_IN_S) * 1000 - OAUTH_EXPIRY_SKEW_MS;
  return { access, refresh, expires };
}

// ---------------------------------------------------------------------------
// 签到与积分
// ---------------------------------------------------------------------------

async function fetchWallets(accessToken, hosts) {
  for (const host of dedupe(hosts)) {
    let payload;
    try {
      const res = await fetch(host + WALLETS_PATH, {
        method: "GET",
        headers: bizHeaders(accessToken),
        redirect: "error",
        signal: AbortSignal.timeout(20_000),
      });
      if (!res.ok) {
        // a banned account is so on every host: say it instead of "no data"
        const banned = (res.status === 401 || res.status === 403) ? bannedText(await res.text().catch(() => "")) : "";
        if (banned) throw Object.assign(new Error(`AutoClaw: ${banned}`), { banned: true });
        continue;
      }
      payload = await res.json();
    } catch (e) {
      if (e?.banned) throw e;
      continue;
    }
    if (!payload || payload.code !== 0 || !payload.data) continue;
    return payload.data;
  }
  return null;
}

/** 每日签到（幂等）。返回解析后的结果（可能为 null）。 */
async function dailySignin(accessToken, host) {
  try {
    const res = await fetch(`${host}/autoclaw-proxy/proxy/autoclaw-task-complete`, {
      method: "POST",
      headers: bizHeaders(accessToken),
      body: JSON.stringify({ task_id: "daily_signin", source_id: PRODUCT }),
      signal: AbortSignal.timeout(15_000),
    });
    const env = await res.json().catch(() => null);
    return env?.data ?? null; // { success, already_completed, reward_points, continuous_days }
  } catch {
    return null;
  }
}

/** 查任务列表；401/403 throw（登录失效），HTTP 其他失败/非 envelope 返回 null，网络错误 throw。 */
async function taskList(accessToken, host) {
  const res = await fetch(`${host}/autoclaw-proxy/proxy/autoclaw-task-list`, {
    method: "GET",
    headers: bizHeaders(accessToken),
    signal: AbortSignal.timeout(15_000),
  });
  if (res.status === 401 || res.status === 403) {
    const banned = bannedText(await res.text().catch(() => ""));
    if (banned) throw Object.assign(new Error(`AutoClaw: ${banned}`), { banned: true, signIn: "kept" });
    throw new Error(`登录已失效（HTTP ${res.status}）`);
  }
  if (!res.ok) return null;
  const env = await res.json().catch(() => null);
  return Array.isArray(env?.data) ? env.data : null;
}

/** 查询 daily_signin 任务状态（容错版：任何失败返回 null）。 */
async function signinStatus(accessToken, host) {
  try {
    const tasks = await taskList(accessToken, host);
    if (!tasks) return null;
    const t = tasks.find((x) => x?.task_id === "daily_signin");
    return t ? { status: String(t.status ?? ""), desc: String(t.status_description ?? ""), reward: Number(t.reward_points ?? 0) } : null;
  } catch {
    return null;
  }
}

/** 可选数字：>0 才返回，否则 undefined（checkin 的 credit/streak 允许缺省）。 */
function optNumber(v) {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

/** dailySignin 结果 → magpie 原生 checkin outcome（{outcome, credit?, streak?, message?}）。 */
function checkinOutcome(r) {
  if (!r) return { outcome: "failed", message: "签到接口无响应" };
  if (r.success) {
    return {
      outcome: "claimed",
      credit: optNumber(r.reward_points),
      streak: optNumber(r.continuous_days),
      message: `已签到${r.reward_points ? ` +${r.reward_points}` : ""}${r.continuous_days ? ` · 连续 ${r.continuous_days} 天` : ""}`,
    };
  }
  if (r.already_completed) return { outcome: "done", credit: optNumber(r.reward_points) };
  return { outcome: "failed", message: String(r.message ?? r.msg ?? "签到失败") };
}

/** 本地去重键（成功才记；按小时允许重试）。 */
const signinTriedLocal = new Map(); // key → hour-stamp of last SUCCESSFUL signin

/**
 * 补签去重键：每个登录一个（#254）。显示名可以重复（AutoClaw 默认昵称改不了），
 * 所以按 uid，没有 uid 的旧账号按 refresh token 的哈希；只用供应商 id 会让
 * 同一供应商下的第二个账号当天永远不补签。
 */
function signinAccountKey(providerId, auth) {
  const uid = String(auth?.uid ?? "").trim();
  if (uid) return `${providerId}|uid:${uid}`;
  const tok = bareToken(String(auth?.refresh ?? auth?.access ?? ""));
  return `${providerId}|tok:${createHash("sha256").update(tok).digest("hex").slice(0, 16)}`;
}

/**
 * 签到配置合并：插件 options（plugins.json 的 options 字段）优先，
 * 其次配置文件 ~/.config/magpie/autoclaw.json（热读取，GUI 没有选项入口时的保底通道）：
 *   { "signin": true, "signinHour": null, "signinOnUsage": false }
 * 两处都没有 → signin=true、signinOnUsage=false（自动补签默认关：
 * 签到交给 magpie 原生 checkin 开关；旧版宿主/需要旧行为时显式开 signinOnUsage）。
 */
function signinConfig(options) {
  let fileCfg = {};
  try {
    // magpie 配置目录固定（文档：~/.config/magpie 或 $XDG_CONFIG_HOME/magpie）
    const cfgDir = process.env.XDG_CONFIG_HOME
      ? `${process.env.XDG_CONFIG_HOME}/magpie`
      : `${process.env.HOME ?? ""}/.config/magpie`;
    fileCfg = JSON.parse(readFileSync(`${cfgDir}/autoclaw.json`, "utf8"));
  } catch { /* 无文件/解析失败 → 用默认 */ }
  const merged = { ...fileCfg, ...(options ?? {}) };
  return {
    signin: merged.signin !== false,               // 自动补签总开关（默认开）
    signinOnUsage: merged.signinOnUsage === true,  // usage/refresh 链路自动补签（默认关，见上）
    signinHour: typeof merged.signinHour === "number" ? merged.signinHour : null,
  };
}

/** 依据合并配置决定是否补签（usage/refresh 旧链路；原生 checkin 不经过这里）。 */
async function maybeSignin(accessToken, host, options, reason, accountKey) {
  void reason;
  const cfg = signinConfig(options);
  if (!cfg.signin || !cfg.signinOnUsage) return;
  if (cfg.signinHour !== null && new Date().getHours() !== cfg.signinHour) return;
  const hourStamp = `${new Date().toISOString().slice(0, 13)}`; // YYYY-MM-DDTHH（每小时可重试一次）
  const dayKey = `${accountKey ?? "x"}`;
  const lastOk = signinTriedLocal.get(dayKey);
  if (typeof lastOk === "string" && lastOk.slice(0, 10) === hourStamp.slice(0, 10)) return; // 今天已成功
  const r = await dailySignin(accessToken, host);
  if (r && (r.success || r.already_completed)) {
    signinTriedLocal.set(dayKey, hourStamp);
  }
}

// ---------------------------------------------------------------------------
// 用户资料（GUI 账户命名：昵称/手机号，替代无意义的 uid 哈希 → 「已登录」）
// ---------------------------------------------------------------------------

/**
 * 账号自己的 device_id：登录时生成并随登录请求发出的那个，服务端写进了 token 的 device_id claim。
 * 官方 AutoClaw（1.18.5 getUserInfoWithAccessToken → withWebInfo）在 user-profile 里发的就是
 * 登录用的设备 id（getAuthState().deviceId），不是固定串。
 */
function accountDeviceId(deviceId, ...tokens) {
  const given = nonEmptyString(deviceId);
  if (given) return given;
  for (const t of tokens) {
    const claim = nonEmptyString(jwtClaim(t, "device_id"));
    if (claim) return claim;
  }
  return "";
}

/** POST /userapi/v1/user-profile → { user_name, user_phone, user_id, … }；失败返回 null。 */
async function fetchUserProfile(accessToken, base, deviceId, refreshToken) {
  try {
    const res = await fetch(`${base}/userapi/v1/user-profile`, {
      method: "POST",
      headers: bizHeaders(accessToken),
      body: JSON.stringify({ source_id: PRODUCT, device_id: accountDeviceId(deviceId, accessToken, refreshToken) }),
      signal: AbortSignal.timeout(15_000),
    });
    if (!res.ok) return null;
    const env = await res.json().catch(() => null);
    const d = env?.data;
    if (!d || typeof d !== "object") return null;
    return {
      userId: nonEmptyString(d.user_id),
      name: nonEmptyString(d.user_name),
      phone: nonEmptyString(d.user_phone),
    };
  } catch {
    return null;
  }
}

/** GUI 显示名：昵称 > 手机号 > uid（workbuddy 社区插件同款优先级 nickname/phone/uid）。 */
function accountDisplayName(profile, fallback) {
  if (!profile) return fallback;
  return profile.name ?? profile.phone ?? profile.userId ?? fallback;
}

// ---------------------------------------------------------------------------
// 刷新（refresh token 轮换；host 必须与账号来源匹配）
// ---------------------------------------------------------------------------

async function refreshTokens(refreshToken, preferredHosts) {
  const deviceId = nonEmptyString(jwtClaim(refreshToken, "device_id")) ?? "";
  const body = JSON.stringify({ source_id: PRODUCT, device_id: deviceId, refresh_token: refreshToken });
  const isSmsAccount = /^agent/i.test(String(jwtClaim(refreshToken, "source_id") ?? ""));
  const ordered = isSmsAccount
    ? [CN_HOST, GLOBAL_HOST, ...preferredHosts]
    : [GLOBAL_HOST, CN_HOST, ...preferredHosts];

  let result = null;
  for (const base of dedupe(ordered)) {
    result = await callEnvelope("/userapi/v1/refresh", { method: "POST", body, base });
    if (result.code !== 0 && result.code === 400002) {
      result = await callEnvelope("/userapi/v1/agent-refresh", { method: "POST", body, base });
    }
    if (result.code !== 400000) break;
  }
  if (!result) throw new Error("AutoClaw refresh: no host attempted");
  if (result.code !== 0 || !result.data) {
    // a banned account is refused, not signed out: the sign-in is kept
    if (result.banned) throw Object.assign(new Error(`AutoClaw: ${result.banned}`), { signIn: "kept" });
    const dead = result.status === 401 || result.status === 403
      || /invalid[_ ]?grant|revoked|expired|登录已失效|token 失效/i.test(String(result.msg ?? ""));
    const err = new Error(`AutoClaw token refresh failed (HTTP ${result.status}): ${result.msg ?? `code ${result.code}`}`);
    if (dead) err.signIn = "expired";
    throw err;
  }
  return tokenFields(result.data, refreshToken);
}

// ---------------------------------------------------------------------------
// 插件工厂
// ---------------------------------------------------------------------------

function managedHeaders(token, model) {
  const ts = String(Math.floor(Date.now() / 1000));
  const rid = randomHex(16);
  const headers = new Headers(bizHeaders());
  headers.set("Accept", "application/json");
  headers.set("User-Agent", `AutoClaw/${CN_CLIENT_VERSION} (${PLATFORM}-${process.arch})`);
  headers.set("X-Channel", CN_CHANNEL);
  headers.set("X-Version", CN_CLIENT_VERSION);
  headers.set("X-Trace-Id", rid);
  headers.set("X-Request-Id", rid);
  headers.set("X-Request-Model", model);
  headers.set("X-Client-Type", "pc");
  headers.set("x_trace_id", "autoclaw-desktop");
  headers.set("X-Agent-Id", "main");
  headers.set("X-Authorization", `Bearer ${bareToken(token)}`);
  headers.delete("Authorization");
  return headers;
}

function rewriteManagedBody(body) {
  const payload = JSON.parse(String(body ?? "{}"));
  const model = typeof payload.model === "string" ? payload.model : "zai_auto";
  const messages = Array.isArray(payload.messages) ? payload.messages : [];
  payload.model = model;
  // The gateway rejects max_tokens, but accepts both streaming modes.
  delete payload.max_tokens;
  delete payload.max_completion_tokens;
  unflag(messages);
  const [first, ...rest] = messages;
  if (first?.role === "system") {
    // Prefix inside the caller's existing system message avoids competing
    // system roles while satisfying the gateway's AutoClaw check.
    const content = Array.isArray(first.content)
      ? [{ type: "text", text: OFFICIAL_SYSTEM_PROMPT }, ...first.content]
      : `${OFFICIAL_SYSTEM_PROMPT}\n\n${String(first.content ?? "")}`;
    payload.messages = [{ ...first, content }, ...rest];
  } else {
    payload.messages = [{ role: "system", content: OFFICIAL_SYSTEM_PROMPT }, ...messages];
  }
  return { payload, model };
}

function makeAutoclawPlugin({ providerId, providerName, homeHost, loginMethod }) {
  return async ({ client }, options) => ({
    config: async (cfg) => {
      cfg.provider ??= {};
      cfg.provider[providerId] ??= {
        name: providerName,
        npm: "@ai-sdk/openai-compatible",
        api: homeHost,
        models: autoclawModels(providerId),
      };
    },

    auth: {
      provider: providerId,
      methods: [loginMethod],

      async refresh(auth) {
        const t = await refreshTokens(bareToken(auth.refresh), [homeHost]);
        await maybeSignin(t.access, homeHost, options, "refresh", signinAccountKey(providerId, auth));
        // 存量账号就地正名：登录响应缺昵称的旧账号（GUI 显示「已登录」），
        // 刷新时补拉 user-profile。magpie 的 refresh 返回字段会合并进账号。
        const old = String(auth.accountId ?? auth.email ?? "");
        if (!old || /^(已登录|[0-9a-f]{24,})$/.test(old)) {
          const profile = await fetchUserProfile(t.access, homeHost, jwtClaim(bareToken(auth.refresh), "device_id"), t.refresh);
          const name = accountDisplayName(profile, "");
          if (name) {
            t.accountId = name;
            t.email = name;
          }
        }
        return t;
      },

      async loader(getAuth) {
        const auth = await getAuth();
        if (auth?.type !== "oauth" || !auth.access) return {};
        const accessToken = bareToken(auth.access);
        const hosts = providerId === "autoclaw-cn" ? [CN_HOST, GLOBAL_HOST] : [GLOBAL_HOST, CN_HOST];

        return {
          baseURL: homeHost,
          async fetch(input, init) {
            if (providerId !== "autoclaw-cn") {
              const headers = new Headers(init?.headers);
              // magpie's own hint headers (x-magpie-*) are for the plugin, never the vendor
              for (const k of [...headers.keys()]) if (k.toLowerCase().startsWith("x-magpie-")) headers.delete(k);
              headers.set("User-Agent", USER_AGENT);
              headers.set("X-Authorization", `Bearer ${accessToken}`);
              headers.set("X-Client-Type", "pc");
              headers.set("X-Product", PRODUCT);
              headers.delete("X-Harness-Type");
              headers.set("X-Tm", PLATFORM_TM);
              headers.set("X-Version", CLIENT_VERSION);
              headers.set("X-Lang", "zh-CN");
              headers.set("X-Channel", CHANNEL);
              headers.set("x_trace_id", "autoclaw-desktop");
              headers.delete("Authorization");

              let routeModel = "";
              let nextBody = init?.body;
              const rewritten = autoclawRewriteBody(init?.body);
              if (rewritten) {
                routeModel = String(rewritten.model ?? "");
                nextBody = JSON.stringify(rewritten);
                headers.delete("content-length");
              } else {
                try {
                  routeModel = String(JSON.parse(String(init?.body ?? "{}")).model ?? "");
                } catch {}
              }
              if (routeModel) headers.set("X-Request-Model", routeModel);
              headers.set("X-Request-Id", randomHex(16));

              const tryFetch = (host) =>
                fetch(`${host}${CHAT_PATH}`, { ...init, headers, body: nextBody, signal: init?.signal });

              const res1 = await tryFetch(hosts[0]);
              if (res1.status !== 401 && res1.status !== 403) return explained(res1);
              // a spent quota or a ban is the account's on every host: not asked again
              const spent1 = await refusedNotSignIn(res1);
              if (spent1) return spent1;
              try { res1.body?.cancel?.(); } catch {}
              const res2 = await tryFetch(hosts[1]);
              const spent2 = await refusedNotSignIn(res2);
              if (spent2) return spent2;
              if (res2.status === 401 || res2.status === 403) {
                try { res2.body?.cancel?.(); } catch {}
                return new Response(JSON.stringify({ error: { message: "AutoClaw 登录已失效（两个 host 均拒绝该 token）", type: "invalid_request_error" } }), {
                  status: 401,
                  headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "expired" },
                });
              }
              return explained(res2);
            }

            // The CN managed gateway is the AutoClaw 2.1.0 account-points
            // channel. It requires the system prefix and rejects max_tokens.
            const { payload, model } = rewriteManagedBody(init?.body);
            const headers = managedHeaders(accessToken, model);
            const res = await fetch(MANAGED_HOST + CN_CHAT_PATH, {
              ...init,
              method: "POST",
              headers,
              body: JSON.stringify(payload),
            });
            if (res.status !== 401 && res.status !== 403) return explained(res);
            const spent = await refusedNotSignIn(res);
            if (spent) return spent;
            try { res.body?.cancel?.(); } catch {}
            return new Response(JSON.stringify({ error: { message: "AutoClaw 登录已失效", type: "invalid_request_error" } }), {
              status: 401,
              headers: { "Content-Type": "application/json", "X-Magpie-Sign-In": "expired" },
            });
          },
        };
      },

      async usage(getAuth) {
        const auth = await getAuth();
        if (auth?.type !== "oauth" || !auth.access) return { error: "未登录", windows: [] };
        const hosts = providerId === "autoclaw-cn" ? [CN_HOST, GLOBAL_HOST] : [GLOBAL_HOST, CN_HOST];
        const token = bareToken(auth.access);
        const cfg = signinConfig(options);
        try {
          const [data, signin] = await Promise.all([
            fetchWallets(token, hosts),
            signinStatus(token, hosts[0]).then((s) => s ?? signinStatus(token, hosts[1] ?? hosts[0])),
          ]);
          // 旧链路自动补签（默认关，需 signinOnUsage=true）：查额度即触发。
          // 放在状态查询之后：未签才补，补完重查一次状态展示。
          if (signin && signin.status !== "completed" && (cfg.signin && cfg.signinOnUsage)) {
            await maybeSignin(token, hosts[0], options, "usage", signinAccountKey(providerId, auth));
          }
          const signinFinal = signin?.status === "completed"
            ? signin
            : (await signinStatus(token, hosts[0]).then((s) => s ?? signinStatus(token, hosts[1] ?? hosts[0])) ?? signin);
          if (!data) return { error: "积分接口无数据（token 可能不属于该 host）", windows: [] };
          const total = Number(data.total_balance);
          if (!Number.isFinite(total)) return { error: "积分响应异常", windows: [] };

          const rows = Array.isArray(data.wallets) ? data.wallets : [];
          const named = rows
            .filter((r) => r.display !== false && Number(r.balance) > 0)
            .map((r) => ({ name: String(r.display_name ?? r.public_wallet_type ?? "积分"), balance: Number(r.balance) }))
            .filter((w) => Math.round(w.balance) !== Math.round(total))
            .slice(0, 3);

          // 签到窗口行 = 状态展示（开关指示交给 magpie 原生 checkin 的 Settings 开关）
          const signinWindow = signinFinal
            ? [{
                name: "每日签到",
                // used 是 0–100 的百分比；签到行只表达状态，状态在 display 里
                used: 0,
                display: signinFinal.status === "completed"
                  ? `今日已签 ✓${signinFinal.reward ? ` +${signinFinal.reward}` : ""}`
                  : `今日未签（+${signinFinal.reward || 400}）`,
                aside: true,
              }]
            : [];

          // 积分是纯余额，没有总量也没有已用量：只放在 balance 里。做成
          // used: 0 的窗口，卡片会给它配上「剩余 100%」（#376）。细分钱包
          // 一并写进 balance。
          const parts = named.map((w) => `${w.name} ${Math.round(w.balance)}`);
          return {
            balance: `${Math.round(total)} 积分${parts.length ? `（${parts.join(" · ")}）` : ""}`,
            windows: signinWindow,
          };
        } catch (e) {
          if (e?.banned) return { error: e.message, signIn: "kept", windows: [] };
          return { error: `积分查询失败: ${e?.message ?? e}`, windows: [] };
        }
      },

      /**
       * 原生每日签到钩子（magpie ≥ v0.1.1083）。由宿主调度：Settings → Usage → Plugins
       * 的 Daily check-in 开关（默认关）控制；每天每账号一次，结果上 Usage 卡片。
       * 契约：返回 {outcome, credit?, streak?, message?}；throw → failed（30 分钟后重试）。
       */
      async checkin(getAuth) {
        const auth = await getAuth();
        if (auth?.type !== "oauth" || !auth.access) throw new Error("未登录");
        const token = bareToken(auth.access);
        const hosts = providerId === "autoclaw-cn" ? [CN_HOST, GLOBAL_HOST] : [GLOBAL_HOST, CN_HOST];

        // 先查任务状态：区分「无签到活动」（inactive）与「查询失败」（throw → failed 重试）
        let task = null;
        let lastErr = null;
        for (const h of hosts) {
          try {
            const tasks = await taskList(token, h);
            if (tasks) {
              task = tasks.find((x) => x?.task_id === "daily_signin") ?? null;
              break;
            }
          } catch (e) {
            lastErr = e;
            if (e?.banned) break;
          }
        }
        if (!task && lastErr) throw lastErr;
        if (!task) return { outcome: "inactive", message: "当前没有签到活动" };
        if (String(task.status ?? "") === "completed") {
          return { outcome: "done", credit: optNumber(task.reward_points) };
        }

        // 补签：主 host 优先，无响应再试备用 host（服务端幂等）
        const r = await dailySignin(token, hosts[0]).catch(() => null)
          ?? await dailySignin(token, hosts[1] ?? hosts[0]).catch(() => null);
        return checkinOutcome(r);
      },
    },

    provider: {
      id: providerId,
      async models(provider) {
        return provider.models;
      },
    },
  });
}

// ---------------------------------------------------------------------------
// 登录方式：国内手机验证码
// ---------------------------------------------------------------------------

const phoneSmsMethod = {
  type: "oauth",
  label: "AutoClaw 手机验证码登录（国内）",
  async authorize(inputs) {
    const phone = String(inputs?.phone ?? "").trim();
    // authorize 失败必须 throw（返回 {type:"failed"} 会被宿主当 "auto" 分支无参调 callback()）。
    if (!/^1\d{10}$/.test(phone)) {
      throw new Error("请输入 11 位手机号（不带 +86）");
    }
    const deviceId = newDeviceId();
    const send = await callEnvelope("/userapi/v1/agent-send-code", {
      method: "POST",
      body: JSON.stringify({ source_id: PRODUCT, device_id: deviceId, phone }),
      base: CN_HOST,
    });
    if (send.code !== 0) {
      throw new Error(`发送验证码失败: ${send.msg ?? `code ${send.code}`}`);
    }
    return {
      url: "",
      instructions: `验证码已发送到 ${phone.slice(0, 3)}****${phone.slice(-4)} 的短信，请输入收到的验证码。`,
      method: "code",
      async callback(code) {
        const res = await callEnvelope("/userapi/v1/agent-login/", {
          method: "POST",
          body: JSON.stringify({ source_id: PRODUCT, device_id: deviceId, phone, code: codeValue(code) }),
          base: CN_HOST,
        });
        if (res.code !== 0 || !res.data?.access_token) {
          return { type: "failed", error: `登录失败: ${res.msg ?? `code ${res.code}`}` };
        }
        const t = tokenFields(res.data);
        // GUI 显示名：拉 user-profile 取昵称/手机号（登录响应的 user_name 常为空 → 显示「已登录」）
        const profile = await fetchUserProfile(t.access, CN_HOST, deviceId);
        const displayName = accountDisplayName(profile, phone);
        return {
          type: "success",
          ...t,
          accountId: displayName,
          email: displayName,
          uid: nonEmptyString(res.data.user_id) ?? undefined,
        };
      },
    };
  },
  prompts: [
    { type: "text", key: "phone", message: "手机号（11 位，不带 +86）", placeholder: "13800138000",
      validate: (v) => (/^1\d{10}$/.test(String(v ?? "").trim()) ? undefined : "11 位手机号") },
  ],
};

// ---------------------------------------------------------------------------
// 登录方式：国际网页 OAuth（本地完整实现：滑块 + 授权 + 回调捕获）
// ---------------------------------------------------------------------------

const overseaOAuthMethod = {
  type: "oauth",
  label: "AutoClaw 网页登录（Google / z.ai）",
  async authorize(inputs) {
    const vendor = inputs?.vendor === "zai" ? "zai" : "google";
    return await startLocalOAuth(vendor);
  },
  prompts: [
    { type: "select", key: "vendor", message: "登录方式",
      options: [
        { label: "Google 账号", value: "google" },
        { label: "z.ai 账号", value: "zai" },
      ] },
  ],
};

/**
 * 启动本地 OAuth：立即返回 {url, method:"auto", callback}。
 * url = 本地引导页（滑块在内完成，之后自动跳授权页）；callback 挂起等 code，
 * 拿到后直连上游换 token。
 */
async function startLocalOAuth(vendor, openFn) {
  const deviceId = newDeviceId();
  const state = {
    captchaReceipt: null,
    oauthCode: null,
    oauthState: "",
    // /open-auth 从上游授权页地址里读出的 state；回调必须原样带回（防 login CSRF）
    expectedOauthState: null,
    error: null,
  };
  const waiters = [];
  const notify = () => { while (waiters.length) waiters.shift()(); };
  const waitFor = (timeoutMs, what) => new Promise((resolve, reject) => {
    const check = () => {
      if (state.error) { cleanup(); reject(new Error(state.error)); return true; }
      if (state.oauthCode) { cleanup(); resolve(state.oauthCode); return true; }
      return false;
    };
    const timer = setTimeout(() => { cleanup(); reject(new Error(`${what} 等待超时（${Math.round(timeoutMs / 60000)} 分钟）— 请重试登录`)); }, timeoutMs);
    const w = () => { check(); };
    const cleanup = () => { clearTimeout(timer); const i = waiters.indexOf(w); if (i >= 0) waiters.splice(i, 1); };
    waiters.push(w);
    check();
  });

  // 官方 TokenServer 端口池（OAuth client 注册的回调白名单端口）。
  // 18432 被占（如官方客户端在跑）时依次尝试其余。
  const FIXED_PORTS = [18432, 19654, 19723, 53699];

  // 探测可用端口（listen → 记下 → 关闭），随后本函数末尾立即 listen 占住，
  // 直到 callback 完成或 OAUTH_SESSION_TIMEOUT_MS 超时（宿主可能不调 callback）。
  let fixedPort = 0;
  const probe = http.createServer();
  for (const p of FIXED_PORTS) {
    const ok = await new Promise((resolve) => {
      probe.once("error", () => resolve(false));
      probe.listen(p, "127.0.0.1", () => { probe.close(() => resolve(true)); });
    });
    if (ok) { fixedPort = p; break; }
  }
  if (!fixedPort) {
    throw new Error(`本地端口 ${FIXED_PORTS.join("/")} 均被占用——OAuth 回调需要其中之一（官方 client 白名单端口）。请关闭占用进程（可能是 AutoClaw 桌面端或其 TokenServer）后重试。`);
  }

  const server = http.createServer((req, res) => {
    const base = `http://127.0.0.1:${server.address()?.port ?? 0}`;
    let url;
    try { url = new URL(req.url, base); } catch { res.writeHead(400); res.end(); return; }
    const send = (status, type, body, extra = {}) => {
      res.writeHead(status, { "Content-Type": type, "Cache-Control": "no-store", ...extra });
      res.end(body);
    };
    // 写接口仅接受本机来源：浏览器同源 fetch 一定带 Origin（127.0.0.1/localhost）；
    // curl 等非浏览器客户端无 Origin，放行。防其他网页跨源 POST /abort 干扰登录。
    const localOrigin = (req) => {
      const o = req.headers.origin;
      return !o || /^https?:\/\/(127\.0\.0\.1|localhost|\[::1\])(:\d+)?$/i.test(String(o));
    };
    if (["/captcha", "/open-auth", "/abort"].includes(url.pathname) && !localOrigin(req)) {
      send(403, "application/json", JSON.stringify({ error: "forbidden origin" }));
      return;
    }

    if (req.method === "OPTIONS") {
      send(204, "text/plain", "", { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Headers": "content-type", "Access-Control-Allow-Methods": "GET,POST,OPTIONS" });
      return;
    }

    if (url.pathname === "/" || url.pathname === "/index.html") {
      send(200, "text/html; charset=utf-8", guidePageHtml(vendor));
      return;
    }

    if (url.pathname === "/captcha-config") {
      callEnvelope("/userapi/overseasv1/oauth-captcha-config", { method: "POST", body: "{}", base: GLOBAL_HOST })
        .then((r) => send(200, "application/json", JSON.stringify(r.data ?? { enabled: false })))
        .catch(() => send(502, "application/json", JSON.stringify({ enabled: false, error: "config fetch failed" })));
      return;
    }

    const readBody = () => new Promise((resolve) => {
      let b = "";
      req.on("data", (c) => { b += c; });
      req.on("end", () => { try { resolve(JSON.parse(b || "{}")); } catch { resolve({}); } });
    });

    if (url.pathname === "/captcha" && req.method === "POST") {
      readBody().then((j) => {
        // 阿里 SDK 把回执作为 captchaVerifyCallback 首参本身（JSON 字符串），
        // 兜底 captchaVerifyParam 字段两种形态。
        const receipt = typeof j.receipt === "string" ? j.receipt
          : typeof j.captchaVerifyParam === "string" ? j.captchaVerifyParam : "";
        state.captchaReceipt = receipt.trim();
        send(200, "application/json", JSON.stringify({ ok: !!state.captchaReceipt }), { "Access-Control-Allow-Origin": "*" });
      });
      return;
    }

    if (url.pathname === "/open-auth" && req.method === "POST") {
      (async () => {
        // ⚠️ redirect_uri 必须是官方 client 注册过的白名单形态：
        // http://localhost:<port>/auth/callback-<vendor>（host=localhost、固定路径、
        // 端口为官方 TokenServer 端口池之一），随机端口/自定路径会被
        // {"detail":"Redirect URI not registered for this client"} 拒绝。
        const redirectUri = `http://localhost:${fixedPort}/auth/callback-${vendor}`;
        const bodyObj = { source_id: PRODUCT, device_id: deviceId, navigate_uri: redirectUri };
        if (state.captchaReceipt) bodyObj.ali_captcha_verify_param = state.captchaReceipt;
        const r = await callEnvelope(`/userapi/overseasv1/${vendor}-oauth-url`, {
          method: "POST", body: JSON.stringify(bodyObj), base: GLOBAL_HOST,
        });
        if (r.code !== 0 || !r.data) {
          send(200, "application/json", JSON.stringify({ error: `获取授权页失败: ${r.msg ?? `code ${r.code}`}` }));
          return;
        }
        const authorizeUrl = String(r.data.oauth_url ?? r.data.url ?? r.data.login_url ?? r.data.auth_url ?? r.data.verify_url ?? "");
        if (!authorizeUrl) {
          send(200, "application/json", JSON.stringify({ error: "上游未返回授权页地址" }));
          return;
        }
        // 记下上游放进授权页的 state：回调必须原样带回，否则拒收（防伪造回调劫持登录）
        let handedOutState = "";
        try { handedOutState = new URL(authorizeUrl).searchParams.get("state") ?? ""; } catch { /* 非法地址按缺 state 处理 */ }
        if (!handedOutState) {
          send(200, "application/json", JSON.stringify({ error: "上游授权页地址缺少 state 参数，已取消登录（回调校验必需）" }));
          return;
        }
        state.expectedOauthState = handedOutState;
        send(200, "application/json", JSON.stringify({ url: authorizeUrl }));
      })().catch((e) => send(200, "application/json", JSON.stringify({ error: `获取授权页失败: ${e?.message ?? e}` })));
      return;
    }

    // 官方注册的回调路径：/auth/callback-<vendor>（redirect_uri 传的就是它）。
    // 等待窗口内任何网页都能导航到本端口——code 必须配上 /open-auth 记下的
    // state 才收，否则只回失败页：把别人的 code 塞进来会登录成别人的账号。
    if (url.pathname === `/auth/callback-${vendor}`) {
      const code = url.searchParams.get("code");
      const cbState = url.searchParams.get("state") ?? "";
      const accepted = !!code && state.expectedOauthState !== null && cbState === state.expectedOauthState;
      if (accepted) {
        state.oauthCode = code;
        state.oauthState = cbState;
        notify();
        send(200, "text/html; charset=utf-8", callbackPageHtml(true));
      } else {
        // 拒收但不动会话状态：之后到达的合法回调仍能完成登录
        const note = !code ? undefined
          : state.expectedOauthState === null ? "授权尚未发起或已结束，请回引导页重试。"
          : "回调校验失败（state 不匹配），已拒绝——请回 magpie 重新登录。";
        send(200, "text/html; charset=utf-8", callbackPageHtml(false, note));
      }
      return;
    }

    if (url.pathname === "/abort" && req.method === "POST") {
      readBody().then((j) => {
        state.error = String(j.reason ?? "用户取消");
        notify();
        send(200, "application/json", JSON.stringify({ ok: true }));
      });
      return;
    }

    send(404, "text/plain", "not found");
  });

  const serverReady = new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(fixedPort, "127.0.0.1", () => resolve(fixedPort));
  });

  // Do not hand the host a page until the listener is ready. The host owns
  // opening authorization URLs, including Windows and remote web sessions.
  await serverReady;

  // 会话最长 OAUTH_SESSION_TIMEOUT_MS：宿主可能永远不调 callback（用户在 magpie
  // 里取消），定时器负责关服务器、释放官方 TokenServer 端口。
  let sessionDone = false;
  let sessionClosed = false;
  const closeServer = () => {
    if (sessionClosed) return;
    sessionClosed = true;
    clearTimeout(sessionTimer);
    if (!sessionDone) {
      state.error ??= "本地登录已超时，服务器已关闭——请重新发起登录";
      notify();
    }
    try { server.close(); } catch { /* 未 listening/已关闭 */ }
    server.closeAllConnections?.();
  };
  const sessionTimer = setTimeout(closeServer, OAUTH_SESSION_TIMEOUT_MS);
  sessionTimer.unref?.();

  return {
    url: `http://127.0.0.1:${fixedPort}/`, // The host opens this only after listen succeeds.
    instructions: `本地登录页已就绪（未自动打开时手动访问 http://127.0.0.1:${fixedPort}/ ）：请完成滑块验证并登录授权，完成后自动返回 magpie。`,
    method: "auto",
    async callback() {
      const port = await serverReady;
      const guide = `http://127.0.0.1:${port}/`;
      const redirectUri = `http://localhost:${port}/auth/callback-${vendor}`;
      try {
        if (openFn) openFn(guide); // Optional test hook; production uses the host URL.
        const code = await waitFor(5 * 60 * 1000, "浏览器登录");
        const res = await callEnvelope(`/userapi/overseasv1/${vendor}-oauth-login`, {
          method: "POST",
          body: JSON.stringify({ source_id: PRODUCT, device_id: deviceId, code, state: state.oauthState, navigate_uri: redirectUri }),
          base: GLOBAL_HOST,
        });
        if (res.code !== 0 || !res.data?.access_token) {
          return { type: "failed", error: `登录失败: ${res.msg ?? `code ${res.code}`}` };
        }
        const t = tokenFields(res.data);
        // GUI 显示名：拉 user-profile 取昵称（Google/z.ai 账号通常有昵称或邮箱）
        const profile = await fetchUserProfile(t.access, GLOBAL_HOST, deviceId);
        const displayName = accountDisplayName(profile, `${vendor} 账号`);
        return {
          type: "success",
          ...t,
          accountId: displayName,
          email: displayName,
          uid: nonEmptyString(res.data.user_id) ?? undefined,
        };
      } finally {
        sessionDone = true;
        closeServer();
      }
    },
  };
}

/** 引导页：滑块（如启用）→ 继续授权 → 自动跳授权页。 */
function guidePageHtml(vendor) {
  const vendorName = vendor === "zai" ? "z.ai" : "Google";
  return `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>AutoClaw ${vendorName} 登录</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
 body{font-family:-apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif;background:#f5f6fa;display:flex;justify-content:center;padding:40px 16px;margin:0}
 .card{background:#fff;border-radius:16px;box-shadow:0 8px 30px rgba(0,0,0,.08);max-width:460px;width:100%;padding:32px}
 h1{font-size:20px;margin:0 0 8px}
 p.sub{color:#666;font-size:14px;margin:0 0 20px}
 .step{font-size:14px;color:#333;margin:10px 0;display:flex;gap:8px;align-items:flex-start}
 .step .n{background:#4f6ef7;color:#fff;border-radius:50%;width:20px;height:20px;display:inline-flex;align-items:center;justify-content:center;font-size:12px;flex:none}
 #captchaBox{margin:16px 0;padding:16px;border:1px dashed #ccc;border-radius:10px;display:none}
 #captchaEle{min-height:44px}
 button.primary{background:#4f6ef7;color:#fff;border:none;border-radius:10px;padding:12px 22px;font-size:15px;cursor:pointer;width:100%;margin-top:16px}
 button.primary:disabled{background:#b9c0f4;cursor:not-allowed}
 .msg{font-size:13px;margin-top:10px;min-height:18px}
 .msg.ok{color:#1a9c5b}.msg.err{color:#d43c3c}.msg.warn{color:#c98a00}
 #done{display:none;text-align:center;padding:20px 0}
 .spin{display:inline-block;width:28px;height:28px;border:3px solid #e3e6f7;border-top-color:#4f6ef7;border-radius:50%;animation:r 0.8s linear infinite;margin-bottom:10px}
 @keyframes r{to{transform:rotate(360deg)}}
</style></head><body>
<div class="card">
 <h1>AutoClaw · ${vendorName} 登录</h1>
 <p class="sub">本页面由 magpie 插件本地生成，关闭本页即取消登录。</p>
 <div id="steps">
  <div class="step"><span class="n">1</span><span id="stepCaptcha">检查安全验证…</span></div>
  <div id="captchaBox"><div id="captchaEle"></div></div>
  <div class="step"><span class="n">2</span><span>跳转 ${vendorName} 授权页完成登录</span></div>
 </div>
 <button class="primary" id="goBtn" disabled>继续授权</button>
 <div class="msg" id="msg"></div>
 <div id="done"><div class="spin"></div><div>已完成授权，正在返回 magpie…</div></div>
</div>
<script>
const HIDDEN_TRIGGER_ID="__acHiddenTrigger";
(function ensureHiddenTrigger(){
  let b=document.getElementById(HIDDEN_TRIGGER_ID);
  if(!b){b=document.createElement("button");b.id=HIDDEN_TRIGGER_ID;b.type="button";b.tabIndex=-1;
    b.setAttribute("aria-hidden","true");b.style.cssText="position:fixed;width:1px;height:1px;opacity:0;pointer-events:none;overflow:hidden";
    document.body.appendChild(b);}
})();
function msg(m,cls){const e=document.getElementById("msg");e.textContent=m;e.className="msg "+(cls||"");}
async function boot(){
  let cfg={enabled:false};
  try{
    const r=await fetch("/captcha-config");cfg=await r.json();
  }catch(e){}
  if(cfg&&cfg.enabled){
    document.getElementById("stepCaptcha").textContent="请点击下方按钮完成安全验证";
    const box=document.getElementById("captchaBox");box.style.display="block";
    window.__acRegion=cfg.region||"ga";window.__acPrefix=cfg.prefix||"sq51tr";
    window.AliyunCaptchaConfig=window.AliyunCaptchaConfig||{region:window.__acRegion,prefix:window.__acPrefix};
    msg("正在加载安全验证组件…","warn");
    await loadCaptchaLib();
    if(!window.initAliyunCaptcha){
      msg("验证组件加载失败（网络/广告拦截），请刷新重试","err");return;
    }
    // ⚠️ 官方流程：init → warmup ≥2.1s → 程序化 click 隐藏按钮 → 组件弹出验证窗。
    // click 太快组件未就绪 → 弹出空白框（实测踩坑）。等待 2.3s。
    window.initAliyunCaptcha({
      SceneId:cfg.scene_id,mode:"popup",element:"#captchaEle",button:"#"+HIDDEN_TRIGGER_ID,
        captchaVerifyCallback:(params)=>{
          const receipt=typeof params==="string"?params:(params&&typeof params.captchaVerifyParam==="string"?params.captchaVerifyParam:JSON.stringify(params||{}));
          fetch("/captcha",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({receipt})})
            .then(()=>{window.__acReceiptSent=true;}).catch(()=>{window.__acReceiptSent=true;});
          return {captchaResult:true};
        },
      onBizResultCallback:()=>{},getInstance:()=>{},
      slideStyle:{width:320,height:40},language:"cn",
      onError:(err)=>{msg("安全验证出错: "+((err&&err.message)||"")+"，请点击重试","err");}
    });
    msg("验证组件已就绪，正在弹出验证窗…","warn");
    setTimeout(()=>{document.getElementById("__acHiddenTrigger").click();},2300);
    // 轮询等回执上报完成（回执 POST /captcha 在 captchaVerifyCallback 里异步发）
    const t0=Date.now();
    await (function pollReceipt(){
      if(window.__acReceiptSent||Date.now()-t0>180000)return;
      return new Promise(r=>setTimeout(r,500)).then(pollReceipt);
    })();
    window.__acReceiptSent=false;
    msg("滑块验证通过 ✓","ok");
  }else{
    document.getElementById("stepCaptcha").textContent="无需安全验证 ✓";
  }
  const btn=document.getElementById("goBtn");btn.disabled=false;btn.textContent="继续授权";
}
function loadCaptchaLib(){
  return new Promise((resolve)=>{
    if(window.initAliyunCaptcha){resolve();return;}
    const s=document.createElement("script");
    s.src="https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js";
    s.async=true;s.onload=()=>resolve();s.onerror=()=>resolve();
    document.head.appendChild(s);
    setTimeout(()=>resolve(),10000);
  });
}
let leavingForAuthorization=false;
document.getElementById("goBtn").addEventListener("click",async()=>{
  const btn=document.getElementById("goBtn");btn.disabled=true;btn.textContent="正在获取授权页…";
  try{
    const r=await fetch("/open-auth",{method:"POST"});
    const j=await r.json();
    if(j.error){msg(j.error,"err");btn.disabled=false;btn.textContent="继续授权";return;}
    document.getElementById("steps").style.display="none";btn.style.display="none";
    document.getElementById("done").style.display="block";
    setTimeout(()=>{leavingForAuthorization=true;location.href=j.url;},600);
  }catch(e){msg("本地登录连接已断开或会话已结束，请回 magpie 重新发起登录："+e.message,"err");btn.disabled=false;btn.textContent="继续授权";}
});
window.addEventListener("beforeunload",()=>{
  if(leavingForAuthorization)return;
  try{navigator.sendBeacon("/abort",new Blob([JSON.stringify({reason:"页面关闭"})],{type:"application/json"}));}catch(_){}
});
boot().catch((e)=>msg("初始化失败："+e.message,"err"));
</script></body></html>`;
}

/** 授权回调落地页；note 覆盖默认文案（如 state 校验被拒的原因）。 */
function callbackPageHtml(ok, note) {
  return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>AutoClaw 登录</title>
<style>body{font-family:-apple-system,"PingFang SC",sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#f5f6fa}
.c{background:#fff;border-radius:16px;box-shadow:0 8px 30px rgba(0,0,0,.08);padding:40px 48px;text-align:center}
h1{font-size:18px;margin:0 0 8px;color:${ok ? "#1a9c5b" : "#d43c3c"}}</style></head>
<body><div class="c"><h1>${ok ? "✓ 授权成功" : "✗ 授权失败"}</h1>
<p style="color:#666;font-size:14px">${note ?? (ok ? "正在返回 magpie 完成登录，本页可以关闭。" : "回调缺少 code，请回 magpie 重试。")}</p></div></body></html>`;
}

// ---------------------------------------------------------------------------
// 导出
// ---------------------------------------------------------------------------

/** provider "autoclaw"：国际版（z.ai / Google 网页 OAuth，本地完整实现）。 */
export const AutoclawAuthPlugin = makeAutoclawPlugin({
  providerId: "autoclaw",
  providerName: "AutoClaw (Zhipu)",
  homeHost: GLOBAL_HOST,
  loginMethod: overseaOAuthMethod,
});

/** provider "autoclaw-cn"：国内版（手机验证码）。 */
export const AutoclawCnAuthPlugin = makeAutoclawPlugin({
  providerId: "autoclaw-cn",
  providerName: "AutoClaw 国内 (Zhipu)",
  homeHost: CN_HOST,
  loginMethod: phoneSmsMethod,
});

/** 测试辅助（magpie/OpenCode 不会把它当插件调用）。 */
export const _internal = {
  quotaRefusal, accountRefusal, bannedSaid,
  makePluginForTest: makeAutoclawPlugin,
  platformTm, md5Hex, jwtClaim, tokenFields, codeValue, bizHeaders, bareToken, newDeviceId,
  dailySignin, signinStatus, maybeSignin, signinAccountKey, signinConfig, checkinOutcome, refreshTokens,
  fetchUserProfile, accountDeviceId, accountDisplayName, startLocalOAuth,
  FLAGGED, unflagged, unflag, autoclawRewriteBody, explained, OFFICIAL_SYSTEM_PROMPT, REFUSED_HINT,
  guidePageHtml, callbackPageHtml,
};
