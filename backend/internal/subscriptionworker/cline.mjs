import { AsyncLocalStorage } from "node:async_hooks";
import { ClinePlugin } from "./vendor/cline/index.mjs";

// The pinned adapter rewrites inference errors without their routing metadata.
// Keep observations within one loader call and discard them before every retry.
export async function createClineProvider(context) {
  const hooks = await ClinePlugin(context);
  const inference = new AsyncLocalStorage();
  hooks["network.request"] = (request) => {
    const state = inference.getStore();
    if (state) state.response = null;
    return request;
  };
  hooks["network.response"] = async (request, response, operation) => {
    const state = inference.getStore();
    const url = new URL(request.url);
    if (!state || operation !== "fetch" || url.origin !== "https://api.cline.bot" || url.pathname !== "/api/v1/chat/completions") return response;
    const metadata = { status: response.status, retryAfter: response.headers.get("retry-after"), entitlement: false };
    state.response = metadata;
    if (response.status === 403) {
      // The shared relay bounds error diagnostics by size and time. Only an
      // exact code in complete JSON can distinguish model access from login.
      try { metadata.entitlement = (await response.clone().json())?.error?.code === "ENTITLEMENT_ERROR"; }
      catch { /* Malformed or incomplete diagnostics keep the HTTP classification. */ }
    }
    return response;
  };
  const load = hooks.auth.loader;
  hooks.auth.loader = async (...args) => {
    const loader = await load(...args);
    if (typeof loader.fetch !== "function") return loader;
    return { ...loader, fetch: (...args) => inference.run({ response: null }, async () => {
      const response = await loader.fetch(...args);
      const observed = inference.getStore().response;
      if (response.ok || !observed || response.status !== observed.status) return response;
      const headers = new Headers(response.headers);
      if (observed.entitlement && headers.get("x-magpie-sign-in") !== "expired") {
        await response.body?.cancel();
        headers.delete("content-length");
        headers.delete("content-encoding");
        headers.set("content-type", "application/json");
        return new Response(JSON.stringify({ error: { message: "The Cline account does not include this model", type: "model_not_found", code: 404 } }), { status: 404, headers });
      }
      if (!observed.retryAfter || headers.has("retry-after")) return response;
      headers.set("retry-after", observed.retryAfter);
      return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
    }) };
  };
  return hooks;
}
