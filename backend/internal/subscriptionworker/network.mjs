import { randomUUID } from "node:crypto";
import { failure, headerObject, MAX_BODY, toHeaders, validateTarget } from "./validation.mjs";
import { guardFactoryAllowance } from "./factory.mjs";

// Only ordinary request metadata is safe across origins. Bundled adapters use
// credential headers beyond Authorization (for example ptKey/X-Authorization),
// so a denylist of known token names would silently miss future adapters.
const CROSS_ORIGIN_SAFE_HEADERS = new Set([
  "accept", "accept-encoding", "accept-language", "cache-control", "content-length", "content-type", "pragma", "user-agent",
]);

// Every vendor fetch is executed by the parent's guarded HTTP transport. DNS,
// redirects, proxy selection and connection limits remain Go responsibilities.
export function createNetworkRelay(scope, emit, { maxResponseBytes = 64 * 1024 * 1024 } = {}) {
  const pending = new Map();

  async function once(input, init = {}) {
    const context = scope.getStore();
    if (!context) throw failure("invalid_request", "A subscription request context is required");
    if (context.account?.saveError) throw context.account.saveError;
    let request = new Request(input, { ...init, duplex: "half", redirect: "manual" });
    if (context.account?.hooks?.["network.request"]) request = await context.account.hooks["network.request"](request, context.operation);
    validateTarget(context.provider, request.url, context.enterpriseHost);
    const body = request.body ? Buffer.from(await request.arrayBuffer()) : Buffer.alloc(0);
    if (body.length > MAX_BODY) throw failure("body_too_large", "Subscription request exceeds the size limit");
    const signal = AbortSignal.any([request.signal, context.controller.signal]);
    signal.throwIfAborted();
    const requestID = randomUUID();
    let settle;
    const promise = new Promise((resolve, reject) => { settle = { resolve, reject }; });
    const state = { ...settle, context, bytes: 0, controller: null, aborted: false };
    const abort = () => {
      if (state.aborted) return;
      state.aborted = true;
      const error = failure("request_cancelled", "Subscription request was cancelled", 499);
      state.reject(error);
      state.controller?.error(error);
      pending.delete(requestID);
      void emit({ id: context.id, event: "network.cancel", request_id: requestID });
    };
    state.dispose = () => signal.removeEventListener("abort", abort);
    signal.addEventListener("abort", abort, { once: true });
    pending.set(requestID, state);
    await emit({ id: context.id, event: "network.request", request_id: requestID, resource_id: context.resourceID,
      provider: context.provider, url: request.url, method: request.method, headers: headerObject(request.headers),
      body: body.toString("base64") });
    return promise;
  }

  async function fetch(input, init = {}) {
    const original = new Request(input, { ...init, duplex: "half" });
    let request = original;
    // fetch follows redirects by default. Reimplement that here so each hop is
    // revalidated and credentials never cross origins. The Go relay must not
    // follow redirects itself.
    for (let redirects = 0; redirects <= 5; redirects++) {
      const copy = request.clone();
      const response = await once(request);
      // The allowance policy belongs to the original request even when its
      // endpoint redirects to a canonical path before returning the payload.
      if (![301, 302, 303, 307, 308].includes(response.status)) {
        const context = scope.getStore();
        const guarded = await guardFactoryAllowance(context, original, response);
        const hook = context.account?.hooks?.["network.response"];
        return hook ? hook(original, guarded, context.operation) : guarded;
      }
      const location = response.headers.get("location");
      if (!location || original.redirect === "manual") return response;
      if (original.redirect === "error" || redirects === 5) {
        await response.body?.cancel();
        throw failure("upstream_host_not_allowed", "Subscription upstream redirect was refused");
      }
      await response.body?.cancel();
      const target = new URL(location, request.url);
      validateTarget(scope.getStore().provider, target.href, scope.getStore().enterpriseHost);
      const useGet = response.status === 303 || ((response.status === 301 || response.status === 302) && request.method === "POST");
      if (target.origin !== new URL(request.url).origin &&
          ([...request.headers.keys()].some((key) => !CROSS_ORIGIN_SAFE_HEADERS.has(key)) || (!useGet && copy.body))) {
        throw failure("upstream_host_not_allowed", "Subscription credentials cannot follow a cross-origin redirect");
      }
      const headers = new Headers(request.headers);
      if (useGet) { headers.delete("content-type"); headers.delete("content-length"); }
      request = new Request(target, { method: useGet ? "GET" : request.method, headers,
        body: useGet || !copy.body ? undefined : await copy.arrayBuffer(), signal: request.signal, duplex: "half" });
    }
    throw failure("invalid_upstream", "Too many subscription redirects");
  }

  function receive(message) {
    const state = pending.get(message.request_id);
    if (!state) return;
    const end = () => { state.dispose(); pending.delete(message.request_id); };
    try {
      if (message.operation === "network.response") {
        if (state.controller) throw new Error("duplicate network response");
        state.context.egressError = null;
        const headers = toHeaders(message.headers);
        // Account state is declared only by the trusted adapter after fetch.
        // An upstream response cannot manufacture that routing instruction.
        headers.delete("x-magpie-sign-in");
        headers.delete("content-encoding");
        headers.delete("content-length");
        const empty = [101, 204, 205, 304].includes(message.status);
        if (message.body !== undefined || empty) {
          const bytes = Buffer.from(message.body ?? "", "base64");
          if (bytes.length > MAX_BODY || bytes.length > maxResponseBytes) throw failure("body_too_large", "Subscription response exceeds the size limit");
          state.resolve(new Response(empty ? null : bytes, { status: message.status, headers }));
          end();
          return;
        }
        const stream = new ReadableStream({
          start(controller) { state.controller = controller; },
          cancel() {
            end();
            return emit({ id: state.context.id, event: "network.cancel", request_id: message.request_id });
          },
        }, { highWaterMark: 65536, size: (chunk) => chunk.byteLength });
        state.resolve(new Response(stream, { status: message.status, headers }));
      } else if (message.operation === "network.chunk") {
        const chunk = Buffer.from(message.data ?? "", "base64");
        state.bytes += chunk.length;
        // A fast JSON/text consumer can accumulate the whole response outside
        // the queue, so bound total bytes independently of backpressure.
        if (state.bytes > maxResponseBytes) {
          throw failure("body_too_large", "Subscription response exceeds the total size limit");
        }
        // Keep a separate bound for a reader that falls behind the upstream.
        if (chunk.length > 1024 * 1024 || (state.controller?.desiredSize ?? 0) < -MAX_BODY) {
          throw failure("body_too_large", "Subscription response reader is too slow");
        }
        if (!state.controller) throw new Error("network response headers are missing");
        state.controller.enqueue(chunk);
      } else if (message.operation === "network.end") {
        state.controller?.close();
        end();
      } else if (message.operation === "network.error") {
        const error = failure(message.error, "Subscription upstream network request failed", message.status ?? 502);
        // Some pinned adapters catch fetch errors and manufacture an HTTP
        // response. Keep guarded transport failures in the command context so
        // that conversion cannot turn a shared proxy fault into account blame.
        if (["provider_proxy_config_error", "provider_proxy_connect_failed", "provider_proxy_auth_failed", "provider_proxy_timeout"].includes(message.error)) {
          state.context.egressError = error;
        }
        throw error;
      }
    } catch (error) {
      state.reject(error);
      try { state.controller?.error(error); } catch { /* The consumer already closed the stream. */ }
      end();
      void emit({ id: state.context.id, event: "network.cancel", request_id: message.request_id }).catch(() => {});
    }
  }

  return { fetch, receive };
}
