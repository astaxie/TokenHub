import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";
import { validateAuth } from "./validation.mjs";

const providers = ["workbuddy", "workbuddy-ai"];
const sensitive = "synthetic-private-token";

async function terminalStatus(worker, provider, sessionID) {
  for (let attempt = 0; attempt < 400; attempt++) {
    const reply = await worker.call("auth.status", { provider, session_id: sessionID });
    assert.equal(reply.event, "result");
    if (reply.result.state !== "pending") return reply.result;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.fail("WorkBuddy authorization did not finish");
}

function loginNetwork(provider, codes, override, domain) {
  const origin = provider === "workbuddy" ? "https://copilot.tencent.com" : "https://www.workbuddy.ai";
  const polls = { token: 0, account: 0 };
  return { polls, network(request) {
    const url = new URL(request.url);
    assert.equal(url.origin, origin);
    const custom = override?.(url);
    if (custom) return custom;
    let payload;
    if (url.pathname === "/v2/plugin/auth/state") {
      assert.equal(request.method, "POST");
      payload = { code: codes.success, data: { state: "synthetic-state", authUrl: origin + "/signin" } };
    } else if (url.pathname === "/v2/plugin/auth/token") {
      assert.equal(url.searchParams.get("state"), "synthetic-state");
      payload = ++polls.token === 1 ? { code: codes.token, msg: "Pending browser authorization" } :
        { code: codes.success, data: { accessToken: sensitive, refreshToken: "synthetic-refresh", expiresIn: 3600, ...(domain ? { domain } : {}) } };
    } else if (url.pathname === "/v2/plugin/login/account") {
      assert.deepEqual(request.headers.authorization, ["Bearer " + sensitive]);
      if (domain) assert.deepEqual(request.headers["x-domain"], [domain]);
      payload = ++polls.account === 1 ? { code: codes.account, msg: "Pending account selection" } :
        { code: codes.success, data: { uid: "synthetic-user", type: "personal" } };
    } else assert.fail("Unexpected WorkBuddy login endpoint: " + url.pathname);
    return { status: 200, body: JSON.stringify(payload) };
  } };
}

test("WorkBuddy browser authorization accepts numeric and string success and pending codes", async (t) => {
  for (const provider of providers) {
    for (const [name, codes] of Object.entries({
      numeric: { success: 0, token: 11217, account: 12151 },
      string_success: { success: "0", token: 11217, account: 12151 },
      string_token_pending: { success: 0, token: "11217", account: 12151 },
      string_account_pending: { success: 0, token: 11217, account: "12151" },
      all_strings: { success: "0", token: "11217", account: "12151" },
    })) {
      await t.test(provider + "/" + name, async (t) => {
        const upstream = loginNetwork(provider, codes);
        const worker = await fixture(t, upstream);
        const start = await worker.call("auth.start", { provider, method_id: "0" });
        assert.equal(start.event, "result");
        assert.equal(start.result.method, "auto");
        assert.equal(start.result.callback_mode, "none");
        const status = await terminalStatus(worker, provider, start.result.session_id);
        assert.equal(status.state, "complete");
        assert.deepEqual(upstream.polls, { token: 2, account: 2 });
        assert.equal(worker.saves.length, 1);
        assert.equal(worker.saves[0].access, sensitive);
        assert.equal(worker.saves[0].uid, "synthetic-user");
      });
    }
  }
});

test("WorkBuddy account domain remains header metadata through login, model discovery, and refresh", async (t) => {
  for (const provider of providers) {
    await t.test(provider, async (t) => {
      const domain = "enterprise-account.example";
      const origin = provider === "workbuddy" ? "https://copilot.tencent.com" : "https://www.workbuddy.ai";
      const login = loginNetwork(provider, { success: 0, token: 11217, account: 12151 }, undefined, domain);
      const observed = [];
      const worker = await fixture(t, { network(request) {
        const url = new URL(request.url);
        assert.equal(url.origin, origin, "Account domain must never select a network destination");
        if (url.pathname === "/v3/config" || url.pathname === "/v2/plugin/auth/token/refresh") {
          assert.deepEqual(request.headers["x-domain"], [domain]);
          observed.push(url.pathname);
          return { status: 200, body: JSON.stringify(url.pathname === "/v3/config" ?
            { code: 0, data: { agents: [{ name: "cli", models: ["fixture-model"] }], models: [{ id: "fixture-model" }] } } :
            { code: 0, data: { accessToken: "synthetic-rotated", refreshToken: "synthetic-rotated-refresh", expiresIn: 3600, domain } }) };
        }
        return login.network(request);
      } });
      const start = await worker.call("auth.start", { provider });
      assert.equal((await terminalStatus(worker, provider, start.result.session_id)).state, "complete");
      assert.equal(worker.saves[0].domain, domain);
      const models = await worker.call("models", { provider });
      assert.equal(models.result.models[0].id, "fixture-model");
      const refresh = await worker.call("refresh", { provider });
      assert.equal(refresh.event, "result");
      assert.equal(refresh.result.auth.domain, domain);
      assert.equal(refresh.result.auth.access, "synthetic-rotated");
      assert.deepEqual(observed, ["/v3/config", "/v2/plugin/auth/token/refresh"]);
    });
  }
});

test("WorkBuddy domain exception rejects header injection and preserves endpoint and redirect guards", async (t) => {
  const auth = { type: "oauth", access: sensitive, refresh: "synthetic-refresh", uid: "fixture", expires: Date.now() + 3600000 };
  for (const provider of providers) {
    await t.test(provider, async (t) => {
      const worker = await fixture(t, { network: () => ({ status: 307, headers: { location: ["https://enterprise-account.example/collect"] }, body: "" }) });
      for (const domain of ["header\r\nX-Secret: value", "nul\0value", "tab\tvalue", "delete\u007f", "x".repeat(1025), 12, null, {}, []]) {
        const result = await worker.call("import", { provider, auth: { ...auth, domain } });
        assert.equal(result.error?.code, "invalid_credentials");
      }
      for (const fields of [
        { metadata: { domain: "enterprise-account.example" } }, { endpoint: "https://enterprise-account.example" },
        { baseURL: "http://127.0.0.1:8080" }, { metadata: { domain: "https://attacker.example" } },
      ]) {
        const result = await worker.call("import", { provider, auth: { ...auth, domain: "enterprise-account.example", ...fields } });
        assert.equal(result.error?.code, "upstream_host_not_allowed");
      }
      assert.equal(worker.requests.length, 0);
      assert.equal(worker.saves.length, 0);
      const imported = await worker.call("import", { provider, auth: { ...auth, domain: "enterprise-account.example" } });
      assert.equal(imported.result.state, "complete");
      const refresh = await worker.call("refresh", { provider });
      assert.equal(refresh.event, "error");
      assert.equal(worker.requests.length, 1, "Account metadata must not allow cross-origin credential redirects");
      assert.notEqual(new URL(worker.requests[0].url).hostname, "enterprise-account.example");
    });
  }
  assert.throws(() => validateAuth("grok", { ...auth, domain: "enterprise-account.example" }), { code: "upstream_host_not_allowed" });
});

test("WorkBuddy authorization preserves HTTP failures and refuses malformed or unknown codes without leaking diagnostics", async (t) => {
  for (const provider of providers) {
    for (const [name, response] of [
      ...[401, 403, 500].map((status) => ["http_" + status, { status, body: JSON.stringify({ code: "0", msg: sensitive, data: { accessToken: sensitive } }) }]),
      ...[19999, "19999", "11217trailing", "0trailing", false, null, {}].map((code) => ["code_" + JSON.stringify(code), {
        status: 200, body: JSON.stringify({ code, msg: sensitive, data: { accessToken: sensitive } }),
      }]),
    ]) {
      await t.test(provider + "/" + name, async (t) => {
        const upstream = loginNetwork(provider, { success: 0, token: 11217, account: 12151 }, (url) =>
          url.pathname === "/v2/plugin/auth/token" ? response : null);
        const worker = await fixture(t, upstream);
        const start = await worker.call("auth.start", { provider, method_id: "0" });
        assert.equal(start.event, "result");
        const status = await terminalStatus(worker, provider, start.result.session_id);
        assert.equal(status.state, "failed");
        assert.equal(worker.saves.length, 0);
        assert.equal(upstream.polls.account, 0);
        assert.equal(status.error.code, "workbuddy_authorization_failed");
        assert.equal(status.error.stage, "token");
        assert.equal(status.error.upstream_status, response.status);
        const code = JSON.parse(response.body).code;
        const numeric = typeof code === "string" && /^-?\d+$/.test(code) ? Number(code) : code;
        assert.equal(status.error.upstream_code, Number.isSafeInteger(numeric) ? numeric : undefined);
        assert.ok(!JSON.stringify(status).includes(sensitive), "Authorization diagnostics exposed an upstream credential");
      });
    }
  }
});

test("WorkBuddy authorization diagnostics identify the failed stage and discard stale response codes", async (t) => {
  for (const provider of providers) {
    for (const [stage, path] of [["state", "/v2/plugin/auth/state"], ["account", "/v2/plugin/login/account"]]) {
      await t.test(provider + "/" + stage, async (t) => {
        const worker = await fixture(t, loginNetwork(provider, { success: 0, token: 11217, account: 12151 }, (url) =>
          url.pathname === path ? { status: 403, body: JSON.stringify({ code: "19999", msg: sensitive }) } : null));
        const start = await worker.call("auth.start", { provider, method_id: "0" });
        const error = stage === "state" ? start.error : (await terminalStatus(worker, provider, start.result.session_id)).error;
        assert.deepEqual(error, { code: "workbuddy_authorization_failed", message: "WorkBuddy authorization did not complete", status: 400,
          stage, upstream_status: 403, upstream_code: 19999 });
        assert.equal(worker.saves.length, 0);
      });
    }
    await t.test(provider + "/transport_after_pending", async (t) => {
      let attempts = 0;
      const worker = await fixture(t, loginNetwork(provider, { success: 0, token: 11217, account: 12151 }, (url) => {
        if (url.pathname === "/v2/plugin/auth/token" && ++attempts > 1) return Promise.reject(new Error(sensitive));
        return null;
      }));
      const start = await worker.call("auth.start", { provider, method_id: "0" });
      const status = await terminalStatus(worker, provider, start.result.session_id);
      assert.equal(status.state, "failed");
      assert.deepEqual(status.error, { code: "workbuddy_authorization_failed", message: "WorkBuddy authorization did not complete", status: 400, stage: "token" });
      assert.equal(worker.saves.length, 0);
    });
  }
});
