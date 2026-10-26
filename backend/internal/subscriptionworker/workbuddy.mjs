import { AsyncLocalStorage } from "node:async_hooks";
import { WorkBuddyAuthPlugin, WorkBuddyAIAuthPlugin } from "./vendor/workbuddy/index.mjs";
import { failure } from "./validation.mjs";

const LOGIN_STAGES = new Map([
  ["/v2/plugin/auth/state", "state"], ["/v2/plugin/auth/token", "token"],
  ["/v2/plugin/login/account", "account"], ["/v2/plugin/accounts", "account"],
]);

function numericCode(raw) {
  const code = typeof raw === "string" && /^-?\d+$/.test(raw) ? Number(raw) : raw;
  return Number.isSafeInteger(code) ? code : undefined;
}

// The pinned adapter compares pending codes numerically but retains the raw
// envelope value. Normalize login responses here without changing vendor files.
export async function createWorkBuddyProvider(provider, context) {
  const international = provider === "workbuddy-ai";
  const hooks = await (international ? WorkBuddyAIAuthPlugin : WorkBuddyAuthPlugin)(context);
  const origin = international ? "https://www.workbuddy.ai" : "https://copilot.tencent.com";
  const authorizations = new AsyncLocalStorage();
  const stageFor = (request, operation) => {
    const url = new URL(request.url);
    return ["auth.start", "auth.callback"].includes(operation) && url.origin === origin ? LOGIN_STAGES.get(url.pathname) : undefined;
  };
  const failed = (diagnostics) => Object.assign(new Error("WorkBuddy authorization failed"), {
    code: "workbuddy_authorization_failed", ...diagnostics,
  });
  const method = hooks.auth.methods[0];
  const authorize = method.authorize;
  method.authorize = (...args) => {
    const diagnostics = { stage: "state" };
    return authorizations.run(diagnostics, async () => {
      let authorization;
      try { authorization = await authorize(...args); } catch { throw failed(diagnostics); }
      return { ...authorization, callback_mode: "none", callback: (...callbackArgs) => authorizations.run(diagnostics, async () => {
        try {
          const result = await authorization.callback(...callbackArgs);
          if (result?.type === "success") return result;
        } catch { /* Only bounded stage and numeric status data may leave the adapter. */ }
        throw failed(diagnostics);
      }) };
    });
  };
  hooks["network.request"] = (request, operation) => {
    const stage = stageFor(request, operation);
    const diagnostics = authorizations.getStore();
    if (stage && diagnostics) {
      diagnostics.stage = stage;
      delete diagnostics.upstream_status;
      delete diagnostics.upstream_code;
    }
    return request;
  };
  hooks["network.response"] = async (request, response, operation) => {
    if (!stageFor(request, operation)) return response;
    const diagnostics = authorizations.getStore();
    if (diagnostics) diagnostics.upstream_status = response.status;
    let envelope;
    try { envelope = await (response.ok ? response : response.clone()).json(); } catch {
      if (!response.ok) return response;
      throw failure("invalid_upstream", "WorkBuddy authorization returned an invalid response", 502);
    }
    const code = numericCode(envelope?.code);
    if (diagnostics && code !== undefined) diagnostics.upstream_code = code;
    if (!response.ok) return response;
    if (code === undefined) {
      throw failure("invalid_upstream", "WorkBuddy authorization returned an invalid status code", 502);
    }
    const headers = new Headers(response.headers);
    headers.delete("content-length");
    return new Response(JSON.stringify({ ...envelope, code }), {
      status: response.status, statusText: response.statusText, headers,
    });
  };
  return hooks;
}
