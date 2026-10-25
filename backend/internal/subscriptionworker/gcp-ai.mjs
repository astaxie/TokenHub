import { createHash, randomBytes } from "node:crypto";
import { createServer } from "node:http";
import { failure } from "./validation.mjs";

// Vertex protocol/discovery reference: magpie-community/plugins gcp-ai (MIT).
// TokenHub owns credentials; no gcloud account or browser profile is accessed.
const ID = "gcp-ai";
const TOKEN = "https://oauth2.googleapis.com/token";
const API = "https://aiplatform.googleapis.com";
const LEAD = 120000;
const MODEL = /^gemini-[a-z0-9.-]+$/;
const NON_CHAT = /tts|embedding|transcribe|live|translate|image|nano-banana|omni/;
const SEEDS = ["gemini-4-argon", "gemini-3.8-flash", "gemini-3.7-flash", "gemini-3.6-flash", "gemini-3.5-flash-lite", "gemini-3.5-flash", "gemini-3.1-flash-lite", "gemini-3.1-pro-preview", "gemini-3-flash-preview", "gemini-2.5-flash-lite", "gemini-2.5-flash", "gemini-2.5-pro"];

function settings(auth) {
  const project = String(auth?.project ?? "").trim();
  const location = String(auth?.location || "global").trim();
  if (!/^(?:[a-z][a-z0-9-]{4,28}[a-z0-9]|[0-9]{6,20})$/.test(project) || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(location) || location.length > 64) {
    throw failure("invalid_credentials", "Supply a valid Google Cloud project and Vertex location");
  }
  return { project, location };
}
function credentials(auth) {
  if (auth?.type !== "oauth") throw failure("invalid_credentials", "Connect a Google Cloud user account");
  const result = { ...auth, ...settings(auth) };
  for (const key of ["access", "refresh", "clientId", "clientSecret"]) {
    if (result[key] !== undefined && (typeof result[key] !== "string" || result[key].length > 16384)) throw failure("invalid_credentials", "Google account credentials are invalid");
  }
  if (!result.access && !result.refresh) throw failure("invalid_credentials", "Google account credentials are missing");
  if (result.refresh && (!result.clientId || !result.clientSecret)) throw failure("invalid_credentials", "Supply the OAuth client ID and client secret associated with the refresh token");
  return result;
}
function endpoint(auth) {
  const { project, location } = settings(auth);
  const host = location === "global" ? "aiplatform.googleapis.com" : `${location}-aiplatform.googleapis.com`;
  return `https://${host}/v1beta1/projects/${project}/locations/${location}/publishers/google`;
}
function modelEntry(id, base = "") {
  return { id, name: id, api: { id, url: base, npm: "@ai-sdk/google" }, tool_call: true, attachment: true,
    modalities: { input: ["text", "image", "audio", "video", "pdf"], output: ["text"] } };
}
async function json(response, message) {
  if (!response.ok) {
    await response.body?.cancel();
    throw failure("invalid_credentials", message, response.status === 401 || response.status === 403 ? 401 : 502);
  }
  try { return await response.json(); } catch { throw failure("invalid_upstream", "Google returned an invalid JSON response", 502); }
}
async function token(values, previous) {
  const response = await fetch(TOKEN, { method: "POST", headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(values), redirect: "error", signal: AbortSignal.timeout(30000) });
  const body = await json(response, "Google account authorization or renewal failed");
  if (typeof body.access_token !== "string" || !body.access_token || !(Number(body.expires_in) > 0 && Number(body.expires_in) <= 31 * 86400)) {
    throw failure("invalid_upstream", "Google returned unusable OAuth credentials", 502);
  }
  return credentials({ ...previous, type: "oauth", access: body.access_token, refresh: body.refresh_token || previous.refresh,
    expires: Date.now() + Number(body.expires_in) * 1000 });
}
const projectPrompts = [
  { type: "text", key: "project", message: "Google Cloud project ID", validate: value => !value?.trim() && "A project is required" },
  { type: "text", key: "location", message: "Vertex location (default: global)", placeholder: "global" },
];

export async function createGCPProvider({ client }) {
  const renewals = new Map();
  async function refresh(auth) {
    const previous = credentials(auth);
    if (!previous.refresh) throw failure("invalid_credentials", "Import renewable Google account credentials or sign in again");
    const key = createHash("sha256").update(JSON.stringify(previous)).digest("hex");
    if (renewals.has(key)) return renewals.get(key);
    if (renewals.size >= 16) renewals.clear();
    const pending = token({ grant_type: "refresh_token", refresh_token: previous.refresh, client_id: previous.clientId, client_secret: previous.clientSecret }, previous);
    renewals.set(key, pending);
    try { return await pending; } catch (error) { renewals.delete(key); throw error; }
  }
  async function current(getAuth, force = false) {
    let auth = credentials(await getAuth());
    if (force || !auth.access || (Number(auth.expires) > 0 && Number(auth.expires) <= Date.now() + LEAD)) {
      auth = await refresh(auth);
      await client.auth.set({ path: { id: ID }, body: auth });
    }
    return auth;
  }
  async function send(getAuth, target, init = {}, onAuth) {
    let auth = await current(getAuth);
    onAuth?.(auth);
    const sendWith = value => {
      const url = new URL(target);
      const ownBase = endpoint(value);
      const catalog = url.origin === API && url.pathname === "/v1beta1/publishers/google/models";
      const location = url.origin === API && url.pathname === `/v1/projects/${value.project}/locations/${value.location}`;
      const modelPath = url.href.startsWith(ownBase + "/models/") && /^gemini-[a-z0-9.-]+:(?:generateContent|streamGenerateContent|countTokens)$/.test(url.pathname.slice(new URL(ownBase).pathname.length + 8));
      if ((!catalog && !location && !modelPath) || url.username || url.password || url.hash) throw failure("upstream_host_not_allowed", "Google credentials may only access the selected project's Vertex routes");
      const headers = new Headers(init.headers);
      for (const name of ["authorization", "cookie", "x-api-key", "x-goog-api-key", "x-goog-user-project"]) headers.delete(name);
      headers.set("authorization", `Bearer ${value.access}`);
      headers.set("x-goog-user-project", value.project);
      return fetch(url, { ...init, headers, redirect: "error", signal: init.signal ?? AbortSignal.timeout(20000) });
    };
    let response = await sendWith(auth);
    if (response.status === 401 && auth.refresh) {
      await response.body?.cancel();
      auth = await current(getAuth, true);
      onAuth?.(auth);
      response = await sendWith(auth);
    }
    return response;
  }
  async function verify(value) {
    // Authorization is persisted only after verification. A 401 can rotate
    // tokens here too, so carry that rotation into the final saved session.
    let auth = credentials(value);
    const request = () => fetch(`${API}/v1/projects/${auth.project}/locations/${auth.location}`, {
      headers: { authorization: `Bearer ${auth.access}`, "x-goog-user-project": auth.project },
      redirect: "error", signal: AbortSignal.timeout(20000),
    });
    let response = await request();
    if (response.status === 401 && auth.refresh) {
      await response.body?.cancel();
      auth = await refresh(auth);
      response = await request();
    }
    await json(response, "The account cannot access the selected Vertex project and location");
    // Cloud-platform ADC may lack userinfo scope; labels are optional.
    try {
      const response = await fetch("https://www.googleapis.com/oauth2/v2/userinfo", { headers: { authorization: `Bearer ${auth.access}` }, redirect: "error", signal: AbortSignal.timeout(15000) });
      if (response.ok) {
        const identity = await response.json();
        if (identity.verified_email && typeof identity.email === "string") auth.accountId = identity.email;
      } else await response.body?.cancel();
    } catch { /* Optional labels do not invalidate a verified Vertex account. */ }
    return auth;
  }
  async function authorize(inputs) {
    const account = { type: "oauth", ...settings(inputs), clientId: inputs.clientId?.trim(), clientSecret: inputs.clientSecret?.trim() };
    if (!account.clientId || !account.clientSecret || account.clientId.length > 4096 || account.clientSecret.length > 16384) throw failure("invalid_credentials", "Supply your Google desktop OAuth client ID and client secret");
    const state = randomBytes(32).toString("base64url");
    const verifier = randomBytes(32).toString("base64url");
    let complete;
    const pending = new Promise(resolve => { complete = resolve; });
    const server = createServer((request, response) => {
      const url = new URL(request.url, "http://127.0.0.1");
      if (request.method !== "GET" || url.pathname !== "/oauth2callback" || url.searchParams.get("state") !== state || !url.searchParams.get("code")) {
        response.writeHead(400).end("Invalid Google OAuth callback");
        return;
      }
      complete(url.searchParams.get("code"));
      response.writeHead(200, { "content-type": "text/plain", "cache-control": "no-store" }).end("Authorization received. Return to TokenHub.");
      server.close();
    });
    server.on("close", () => complete(null));
    await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
    const redirect = `http://127.0.0.1:${server.address().port}/oauth2callback`;
    const query = new URLSearchParams({ client_id: account.clientId, redirect_uri: redirect, response_type: "code", access_type: "offline", prompt: "consent select_account",
      scope: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email", state,
      code_challenge: createHash("sha256").update(verifier).digest("base64url"), code_challenge_method: "S256" });
    return { url: `https://accounts.google.com/o/oauth2/v2/auth?${query}`, method: "auto",
      instructions: "Approve Google access. If your browser cannot reach the backend callback, paste the complete localhost callback URL into TokenHub.",
      async callback() {
        const code = await pending;
        if (!code) return { type: "failed" };
        const auth = await token({ grant_type: "authorization_code", code, code_verifier: verifier, redirect_uri: redirect, client_id: account.clientId, client_secret: account.clientSecret }, account);
        if (!auth.refresh) throw failure("invalid_credentials", "Google did not grant offline access; authorize again with consent");
        return { ...await verify(auth), type: "success" };
      } };
  }
  return {
    config(config) {
      config.provider ??= {};
      config.provider[ID] = { name: "GCP AI", npm: "@ai-sdk/google", api: API,
        models: Object.fromEntries(SEEDS.map(id => [id, modelEntry(id)])) };
    },
    auth: { provider: ID, refreshLead: LEAD, refresh,
      methods: [
        { type: "oauth", label: "Google Cloud account", prompts: [
          { type: "text", key: "clientId", message: "Google desktop OAuth client ID" },
          { type: "password", key: "clientSecret", message: "Google OAuth client secret" }, ...projectPrompts], authorize },
        { type: "api", keyless: true, label: "Import Google authorized-user ADC", prompts: [
          { type: "password", key: "adc", message: "Authorized-user ADC JSON" }, ...projectPrompts],
          async authorize(inputs) {
            let adc;
            try { adc = JSON.parse(inputs.adc); } catch { throw failure("invalid_credentials", "Paste an authorized-user ADC JSON object"); }
            if (adc?.type !== "authorized_user" || !adc.refresh_token || !adc.client_id || !adc.client_secret) throw failure("invalid_credentials", "Only explicit authorized-user ADC credentials are supported");
            const auth = await refresh({ type: "oauth", ...settings(inputs), refresh: adc.refresh_token, clientId: adc.client_id, clientSecret: adc.client_secret });
            return { ...await verify(auth), type: "success" };
          } },
      ],
      async loader(getAuth) {
        const auth = await current(getAuth);
        const baseURL = endpoint(auth);
        return { baseURL, apiKey: "tokenhub-gcp", async fetch(input, init = {}) {
          const request = new Request(input, { ...init, duplex: "half" });
          const target = new URL(request.url);
          if (!target.href.startsWith(baseURL + "/models/") || request.method !== "POST") throw failure("upstream_host_not_allowed", "Vertex requests must use the connected account and a model action");
          return send(getAuth, target, { method: "POST", headers: request.headers, body: await request.text(), signal: request.signal });
        } };
      },
    },
    provider: { id: ID, async models(_provider, context) {
      let auth = await current(() => context.auth);
      const getAuth = () => auth;
      // Discovery receives a snapshot from the host. Retain durable rotations
      // so later pages and model probes use the renewed account immediately.
      const rememberAuth = (updated) => { auth = updated; };
      const ids = new Set();
      let pageToken = "";
      for (let page = 0; page < 20; page++) {
        const query = new URLSearchParams({ pageSize: "200", ...(pageToken ? { pageToken } : {}) });
        const body = await json(await send(getAuth, `${API}/v1beta1/publishers/google/models?${query}`, {}, rememberAuth), "Vertex model discovery failed");
        for (const row of body.publisherModels ?? []) {
          const id = String(row.name ?? "").split("/").pop();
          if (MODEL.test(id) && !NON_CHAT.test(id)) ids.add(id);
        }
        pageToken = body.nextPageToken || "";
        if (!pageToken) break;
        if (page === 19) throw failure("invalid_upstream", "Vertex returned too many catalog pages", 502);
      }
      const base = endpoint(auth);
      const models = [...ids];
      const visible = new Array(models.length);
      let cursor = 0;
      await Promise.all(Array.from({ length: Math.min(6, models.length) }, async () => {
        for (let i = cursor++; i < models.length; i = cursor++) {
          try {
            const response = await send(getAuth, `${base}/models/${models[i]}:countTokens`, { method: "POST", headers: { "content-type": "application/json" },
              body: JSON.stringify({ contents: [{ role: "user", parts: [{ text: "x" }] }] }), signal: AbortSignal.timeout(8000) }, rememberAuth);
            visible[i] = response.status !== 404;
            await response.body?.cancel();
          } catch (error) {
            if (["credential_save_failed", "invalid_credentials", "request_cancelled"].includes(error.code) || error.name === "AbortError") throw error;
            visible[i] = true;
          }
        }
      }));
      return Object.fromEntries(models.filter((_, i) => visible[i]).map(id => [id, modelEntry(id, base)]));
    } },
  };
}
