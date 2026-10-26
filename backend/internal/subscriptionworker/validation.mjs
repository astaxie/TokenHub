import { isIP } from "node:net";

const DOMAINS = {
  cursor: ["cursor.sh", "cursor.com"],
  kiro: ["kiro.dev", "amazonaws.com", "awsapps.com"],
  qoder: ["qoder.sh", "qoder.com"],
  "qoder-cn": ["qoder.com.cn", "qoder.cn"],
  qwenwork: ["qwenwork.cn"],
  "gcp-ai": ["accounts.google.com", "googleapis.com"],
  workbuddy: ["copilot.tencent.com"],
  "workbuddy-ai": ["workbuddy.ai"],
  "mimo-app": ["xiaomi.com", "xiaomimimo.com"],
  zcode: ["z.ai", "bigmodel.cn"],
  factory: ["factory.ai", "api.workos.com"],
  grok: ["auth.x.ai", "accounts.x.ai", "cli-chat-proxy.grok.com"],
  devin: ["devin.ai", "server.codeium.com"],
  "commandcode-plan": ["commandcode.ai"],
  zed: ["zed.dev"],
  "kimi-work": ["agent-gw.kimi.com", "www.kimi.com"],
  "minimax-code": ["minimax.cn", "minimaxi.com"],
  "minimax-code-global": ["minimax.io"],
  cline: ["cline.bot", "api.workos.com"],
  "trae-cn": ["trae.cn", "mchost.guru"],
  "trae-global": ["trae.ai", "mchost.guru"],
  opendesign: ["open-design.ai"],
  "nous-portal": ["nousresearch.com"],
  raccoon: ["xiaohuanxiong.com"],
  autoclaw: ["autoglm.ai", "zhipuai.cn"],
  "autoclaw-cn": ["zhipuai.cn"],
  codearts: ["huaweicloud.com", "myhuaweicloud.com"],
  joycode: ["api-ai.jd.com", "joycode.jd.com"],
  "opencode-zen-free": ["opencode.ai", "raw.githubusercontent.com"],
  "cloudflare-workers-ai": ["api.cloudflare.com", "models.dev"],
};

export const MAX_BODY = 32 * 1024 * 1024;
export const MAX_AUTH = 512 * 1024;

export function failure(code, message, status = 400) {
  return Object.assign(new Error(message), { code, status });
}

// Enterprise endpoints are explicitly chosen by the administrator, then bound
// to one account/session. The parent still resolves and rejects private IPs.
export function enterpriseHost(value) {
  const input = typeof value === "string" ? value.trim().toLowerCase() : "";
  let host = input;
  if (input.includes("://")) {
    let url;
    try { url = new URL(input); } catch { throw failure("invalid_credentials", "A public enterprise HTTPS domain is required"); }
    if (url.protocol !== "https:" || url.username || url.password || url.port || url.search || url.hash ||
        !["", "/"].includes(url.pathname) || input.replace(/\/$/, "") !== "https://" + url.hostname) {
      throw failure("invalid_credentials", "A public enterprise HTTPS domain is required");
    }
    host = url.hostname;
  }
  if (!/^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/.test(host) ||
      /(?:^|\.)(?:localhost|local|internal)$/.test(host) || isIP(host)) {
    throw failure("invalid_credentials", "A public enterprise HTTPS domain is required");
  }
  return host;
}

export function validateTarget(provider, value, boundHost) {
  let url;
  try { url = new URL(value); } catch { throw failure("invalid_upstream", "Invalid subscription upstream URL"); }
  const host = url.hostname.toLowerCase();
  if (url.protocol !== "https:" || url.username || url.password || url.hash || (url.port && url.port !== "443") ||
      isIP(host.replace(/^\[|\]$/g, "")) || !(provider === "qwenwork-enterprise"
        ? boundHost && host === enterpriseHost(boundHost)
        : DOMAINS[provider]?.some((domain) => host === domain || host.endsWith("." + domain)))) {
    throw failure("upstream_host_not_allowed", "Subscription credentials cannot be sent to this host");
  }
  return url;
}

const FILE_KEYS = /^(?:path|file|filePath|home|directory|cwd|command|executable|idePath|clientIdHash|authFile)$/i;
const HOST_KEYS = /(?:base(?:Url|Host)?|api(?:Url)?|server|endpoint|host|issuer|domain)$/i;
const BAD_KEYS = new Set(["__proto__", "prototype", "constructor"]);

export function validateAuth(provider, value, boundHost) {
  if (!value || typeof value !== "object" || Array.isArray(value) || !["api", "oauth"].includes(value.type)) {
    throw failure("invalid_credentials", "Import an OAuth session object or an API key object");
  }
  if (Buffer.byteLength(JSON.stringify(value)) > MAX_AUTH) throw failure("credentials_too_large", "Subscription credentials exceed the size limit");
  if ((provider === "cursor" && value.refresh === "cursor-agent") ||
      (provider === "commandcode-plan" && value.metadata?.cli)) {
    throw failure("local_credentials_not_supported", "Import credential values; local account discovery is not accepted");
  }
  if (provider === "cursor" && value.teamId !== undefined && (!Number.isSafeInteger(value.teamId) || value.teamId <= 0)) {
    throw failure("invalid_credentials", "Cursor teamId must be a positive safe integer");
  }
  if (provider === "qwenwork-enterprise") {
    const importedHost = enterpriseHost(value.enterpriseHost);
    if (boundHost && importedHost !== enterpriseHost(boundHost)) {
      throw failure("upstream_host_not_allowed", "Credential rotation cannot change the enterprise domain");
    }
    boundHost = importedHost;
  }
  function walk(node, depth = 0) {
    if (depth > 20) throw failure("invalid_credentials", "Subscription credentials are too deeply nested");
    if (!node || typeof node !== "object") return;
    for (const [key, item] of Object.entries(node)) {
      if (BAD_KEYS.has(key) || FILE_KEYS.test(key) || (key === "source" && item)) {
        throw failure("local_credentials_not_supported", "Import credential values; local files and executable references are not accepted");
      }
      if (typeof item === "string") {
        if (HOST_KEYS.test(key) && item) {
          if (/^https?:\/\//i.test(item)) validateTarget(provider, item, boundHost);
          else if (/[.]/.test(item) && !/[\s{}]/.test(item)) validateTarget(provider, "https://" + item, boundHost);
        }
        if (/^[\[{]/.test(item.trim())) {
          try { walk(JSON.parse(item), depth + 1); } catch (error) { if (error.code) throw error; }
        }
      } else walk(item, depth + 1);
    }
  }
  walk(value);
  return structuredClone(value);
}

export function publicError(error) {
  const safe = new Set(["invalid_credentials", "credentials_too_large", "local_credentials_not_supported", "upstream_host_not_allowed",
    "provider_not_supported", "operation_not_supported", "method_not_supported", "invalid_upstream", "invalid_request", "invalid_session",
    "callback_not_allowed", "credential_save_failed", "request_cancelled", "body_too_large", "model_not_found", "protocol_mismatch",
    "provider_proxy_config_error", "provider_proxy_connect_failed", "provider_proxy_auth_failed", "provider_proxy_timeout"]);
  const code = safe.has(error?.code) ? error.code : "subscription_operation_failed";
  return { code, message: safe.has(error?.code) ? error.message : "The subscription provider request failed. Check the account and upstream connection.",
    ...(Number.isInteger(error?.status) ? { status: error.status } : {}),
    ...(["expired", "kept", "renewed"].includes(error?.signIn) ? { sign_in: error.signIn } : {}) };
}

export function headerObject(headers) {
  const result = Object.create(null);
  for (const [key, value] of headers) if (key !== "set-cookie") result[key] = [value];
  const cookies = headers.getSetCookie?.() ?? [];
  if (cookies.length) result["set-cookie"] = cookies;
  return result;
}

export function toHeaders(value = {}) {
  const headers = new Headers();
  for (const [key, values] of Object.entries(value)) {
    for (const item of Array.isArray(values) ? values : [values]) headers.append(key, String(item));
  }
  return headers;
}
