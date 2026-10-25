import { setTimeout as delay } from "node:timers/promises";
import { _internal } from "./vendor/grok/index.mjs";

// xAI's public discovery document advertises these device and refresh grants.
// The public client identifier is the one recorded by the upstream Grok adapter.
const CLIENT = "b1a00492-073a-47ea-816f-4c329264a828";
const AUTH = "https://auth.x.ai/oauth2";
const BASE = "https://cli-chat-proxy.grok.com/v1";
const SCOPE = "openid profile email offline_access grok-cli:access";
const LEAD = 5 * 60 * 1000;

async function form(path, values) {
  const response = await fetch(AUTH + path, { method: "POST", headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(values), redirect: "error", signal: AbortSignal.timeout(30000) });
  const data = await response.json();
  return { response, data };
}

function tokenAuth(data, previous = {}) {
  if (typeof data.access_token !== "string" || !data.access_token) throw new Error("Grok token endpoint returned no token");
  let identity = {};
  try { identity = JSON.parse(Buffer.from((data.id_token ?? "").split(".")[1], "base64url").toString()); } catch { /* Identity is display metadata only. */ }
  const lifetime = Number(data.expires_in);
  return { ...previous, type: "oauth", access: data.access_token, refresh: data.refresh_token || previous.refresh || "",
    expires: Number.isFinite(lifetime) && lifetime > 0 ? Date.now() + lifetime * 1000 : 0,
    accountId: identity.email || identity.sub || previous.accountId || "Grok", clientId: CLIENT };
}

function headers(access) {
  return { Authorization: `Bearer ${access}`, "x-grok-client-version": "1.0.41", "x-xai-token-auth": "xai-grok-cli",
    "x-grok-client-identifier": "grok-shell", "x-grok-client-mode": "headless", "User-Agent": "grok-shell/1.0.41", "content-type": "application/json" };
}

export async function createGrokProvider({ client, magpie }) {
  let renewal;
  const renewed = new Map();
  async function refresh(auth) {
    if (!auth.refresh) throw Object.assign(new Error("Grok session must be authorized again"), { signIn: "expired" });
    if (renewed.has(auth.refresh)) return renewed.get(auth.refresh);
    if (renewal) return renewal;
    renewal = (async () => {
      const { response, data } = await form("/token", { grant_type: "refresh_token", client_id: CLIENT, refresh_token: auth.refresh });
      if (!response.ok) throw Object.assign(new Error("Grok token renewal failed"), { signIn: data.error === "invalid_grant" ? "expired" : "kept" });
      const next = tokenAuth(data, auth);
      renewed.set(auth.refresh, next);
      return next;
    })();
    try { return await renewal; } finally { renewal = null; }
  }
  async function current(getAuth, force = false) {
    let auth = await getAuth();
    if (!auth?.access) throw new Error("Grok account is not connected");
    if (force || (auth.expires > 0 && auth.expires - Date.now() < LEAD)) {
      auth = await refresh(auth);
      await client.auth.set({ path: { id: "grok" }, body: auth });
    }
    return auth;
  }
  async function send(getAuth, path, init = {}) {
    let auth = await current(getAuth);
    const request = (value) => fetch(BASE + path, { ...init, headers: { ...init.headers, ...headers(value.access) }, redirect: "error" });
    let response = await request(auth);
    if (response.status === 401 && auth.refresh) {
      await response.body?.cancel();
      auth = await current(getAuth, true);
      response = await request(auth);
    }
    return response;
  }
  return {
    config(config) {
      config.provider ??= {};
      config.provider.grok = { name: "Grok (SuperGrok)", npm: "@ai-sdk/openai", api: BASE,
        models: { "grok-4.7": { name: "Grok 4.7", attachment: true, tool_call: true, reasoning: true, modalities: { input: ["text", "image"] } } } };
    },
    auth: {
      provider: "grok", refreshLead: LEAD, refresh,
      options: [{ key: "x_search", label: "Native X search",
        description: "Offers X search when tools are automatic, preserving existing search filters and explicit tool choices." }],
      methods: [{ type: "oauth", label: "Grok account (device code)", async authorize() {
        const { response, data } = await form("/device/code", { client_id: CLIENT, scope: SCOPE });
        if (!response.ok || !data.device_code || !(data.verification_uri_complete || data.verification_uri)) throw new Error("Grok device authorization failed");
        return { url: data.verification_uri_complete || data.verification_uri, instructions: `Approve the device code ${data.user_code || ""} in your browser.`, method: "auto",
          async callback() {
            let interval = Math.max(1, Number(data.interval) || 5);
            const deadline = Date.now() + Math.min(1800, Math.max(1, Number(data.expires_in) || 600)) * 1000;
            while (Date.now() < deadline) {
              await delay(interval * 1000);
              const result = await form("/token", { grant_type: "urn:ietf:params:oauth:grant-type:device_code", client_id: CLIENT, device_code: data.device_code });
              if (result.response.ok) return { ...tokenAuth(result.data), type: "success" };
              if (result.data.error === "slow_down") { interval += 5; continue; }
              if (result.data.error !== "authorization_pending") return { type: "failed" };
            }
            return { type: "failed" };
          } };
      } }],
      async loader(getAuth) {
        return { baseURL: BASE, apiKey: "grok", async fetch(input, init = {}) {
          const request = new Request(input, { ...init, duplex: "half" });
          if (new URL(request.url).pathname !== "/v1/responses") return new Response(null, { status: 404 });
          const body = _internal.rewrite(await request.text(), magpie?.option?.("grok", "x_search") === true);
          const parsed = JSON.parse(body);
          const model = _internal.modelOf(parsed.model);
          const extra = {};
          if (model.window) { parsed.model = model.model; extra["x-grok-context-window"] = String(model.window); }
          if (model.model) extra["x-grok-model-override"] = model.model;
          if (parsed.prompt_cache_key) extra["x-grok-conv-id"] = parsed.prompt_cache_key;
          return send(getAuth, "/responses", { method: "POST", headers: extra, body: JSON.stringify(parsed), signal: request.signal });
        } };
      },
      async usage(getAuth) {
        const response = await send(getAuth, "/billing?format=credits");
        if (!response.ok) throw new Error("Grok allowance could not be read");
        const config = (await response.json()).config ?? {};
        const period = config.currentPeriod ?? {};
        const end = period.end || config.billingPeriodEnd;
        const reported = config.creditUsagePercent;
        const used = typeof reported === "number" || typeof reported === "string" && reported.trim() !== "" ? Number(reported) : NaN;
        return { windows: [{ name: "Allowance", ...(Number.isFinite(used) && used >= 0 ? { used } : {}),
          ...(typeof end === "string" && Number.isFinite(Date.parse(end)) ? { resetsAt: end } : {}) }] };
      },
    },
    provider: { id: "grok", async models(provider, { auth }) {
      const response = await send(() => auth, "/models");
      if (!response.ok) throw new Error("Grok models could not be read");
      const out = {};
      for (const row of (await response.json()).data ?? []) {
        if (!row.id || (row.api_backend && row.api_backend !== "responses")) continue;
        const efforts = (row.reasoning_efforts ?? []).map((entry) => entry.value).filter(Boolean).reverse();
        for (const model of _internal.windowsOf(row, efforts)) out[model.id] = { id: model.id, name: model.name,
          api: { id: model.id, url: BASE, npm: "@ai-sdk/openai" }, limit: { context: model.context, output: 0 },
          capabilities: { reasoning: efforts.length > 0, attachment: true, toolcall: true, input: { text: true, image: true }, output: { text: true } },
          variants: Object.fromEntries(efforts.map((effort) => [effort, { reasoningEffort: effort }])) };
      }
      if (!Object.keys(out).length) throw new Error("Grok listed no models");
      return out;
    } },
  };
}
