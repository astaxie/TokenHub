import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

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

function loginNetwork(provider, codes, override) {
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
        { code: codes.success, data: { accessToken: sensitive, refreshToken: "synthetic-refresh", expiresIn: 3600 } };
    } else if (url.pathname === "/v2/plugin/login/account") {
      assert.deepEqual(request.headers.authorization, ["Bearer " + sensitive]);
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
