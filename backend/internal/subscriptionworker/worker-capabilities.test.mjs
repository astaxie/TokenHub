import assert from "node:assert/strict";
import test from "node:test";
import { enterpriseHost, validateAuth, validateTarget } from "./validation.mjs";
import { fixture } from "./worker-fixture.mjs";

const json = (body) => ({ status: 200, body: JSON.stringify(body) });
const future = () => Date.now() + 3600000;
const qwenAuth = (host) => ({ type: "oauth", uid: "synthetic-user", access: "synthetic-device", refresh: "synthetic-refresh",
  deviceToken: "synthetic-device", deviceRefresh: "synthetic-refresh", deviceChat: true, machineId: "synthetic-machine", expires: future(),
  ...(host ? { enterpriseHost: host, vpc: host } : {}) });
const chunkText = (result) => result.events.filter((item) => item.event === "chunk").map((item) => Buffer.from(item.data, "base64").toString()).join("");

test("enterprise domains bind credentials and every upstream URL to one exact host", () => {
  for (const value of ["localhost", "tenant.local", "tenant.internal", "127.0.0.1", "[::1]", "http://tenant.example",
    "https://user@tenant.example", "https://tenant.example:443", "https://tenant.example/path", "tenant.example?target=other.example"]) {
    assert.throws(() => enterpriseHost(value), undefined, value);
  }
  assert.equal(enterpriseHost("https://TENANT.example/"), "tenant.example");
  const auth = qwenAuth("tenant.example");
  assert.deepEqual(validateAuth("qwenwork-enterprise", auth), auth);
  assert.throws(() => validateAuth("qwenwork-enterprise", qwenAuth()), { code: "invalid_credentials" });
  assert.throws(() => validateAuth("qwenwork-enterprise", auth, "other.example"), { code: "upstream_host_not_allowed" });
  for (const target of ["other.example", "child.tenant.example", "tenant.example.attacker.example", "gateway.qwenwork.cn"]) {
    assert.throws(() => validateTarget("qwenwork-enterprise", "https://" + target, "tenant.example"), { code: "upstream_host_not_allowed" });
  }
  assert.equal(validateTarget("qwenwork-enterprise", "https://tenant.example/api", "tenant.example").hostname, "tenant.example");
  assert.throws(() => validateTarget("qwenwork-enterprise", "https://tenant.example/api"), { code: "upstream_host_not_allowed" });
  assert.throws(() => validateAuth("qwenwork-enterprise", { ...auth, metadata: { endpoint: "https://other.example" } }), { code: "upstream_host_not_allowed" });
  assert.throws(() => validateAuth("cursor", { type: "oauth", refresh: "cursor-agent", access: "synthetic" }), { code: "local_credentials_not_supported" });
  assert.throws(() => validateAuth("commandcode-plan", { type: "api", key: "synthetic", metadata: { cli: true } }), { code: "local_credentials_not_supported" });
});

for (const provider of ["qwenwork", "qwenwork-enterprise"]) {
  test(`${provider} device authorization persists its account and keeps all requests on its host`, async (t) => {
    const host = provider === "qwenwork" ? "gateway.qwenwork.cn" : "tenant.example";
    const worker = await fixture(t, { network: (request) => {
      const url = new URL(request.url);
      assert.equal(url.hostname, host);
      if (url.pathname.endsWith("/deviceToken/poll")) return json({ token: "synthetic-device", refresh_token: "synthetic-refresh", user_id: "synthetic-user", expires_in: 3600 });
      assert.equal(url.pathname, "/api/v1/adapter/user/account-context");
      return json({ data: { user: { id: "synthetic-user", email: "synthetic@example.com", name: "Fixture" } } });
    } });
    const description = await worker.call("describe", { provider });
    assert.equal(description.result.methods.length, 1);
    assert.equal(description.result.capabilities.checkin, false);
    const started = await worker.call("auth.start", { provider, inputs: provider === "qwenwork" ? {} : { domain: "https://tenant.example/" } });
    assert.equal(started.event, "result");
    assert.equal(new URL(started.result.url).hostname, host);
    assert.equal(new URL(started.result.url).searchParams.has("redirect_uri"), provider === "qwenwork");
    let status;
    for (let attempt = 0; attempt < 100; attempt++) {
      status = await worker.call("auth.status", { provider, session_id: started.result.session_id });
      if (status.result.state !== "pending") break;
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.equal(status.result.state, "complete");
    assert.equal(worker.saves.length, 1);
    assert.equal(worker.saves[0].deviceChat, true);
    assert.equal(worker.saves[0].access, "synthetic-device");
    assert.equal(worker.saves[0].enterpriseHost, provider === "qwenwork-enterprise" ? host : undefined);
    assert.equal((await worker.call("checkin", { provider })).error.code, "operation_not_supported");
  });

  test(`${provider} refreshes, lists models, streams text and reads account quota`, async (t) => {
    const host = provider === "qwenwork" ? "gateway.qwenwork.cn" : "tenant.example";
    const wrap = (body) => "data:" + JSON.stringify({ statusCodeValue: 200, body: typeof body === "string" ? body : JSON.stringify(body) }) + "\n\n";
    const worker = await fixture(t, { network: (request) => {
      const url = new URL(request.url);
      assert.equal(url.hostname, host);
      if (url.pathname.endsWith("/deviceToken/refresh")) return json({ token: "synthetic-renewed", refresh_token: "synthetic-next", expires_in: 3600 });
      assert.equal(worker.saves[0]?.access, "synthetic-renewed", "credential rotation must be durable before account requests");
      if (url.pathname.endsWith("/model/list")) {
        assert.deepEqual(request.headers["cosy-scene"], ["qwork"]);
        assert.deepEqual(request.headers["cosy-clienttype"], ["6"]);
        return json({ qwork: [{ key: "flash", display_name: "Fixture Flash", max_input_tokens: 4096 }] });
      }
      if (url.pathname.endsWith("/agent_chat_generation")) {
        assert.deepEqual(request.headers["cosy-business-product"], ["qoder_work"]);
        return { status: 200, headers: { "content-type": ["text/event-stream"] }, chunks: [
          wrap({ choices: [{ index: 0, delta: { content: "Hello fixture" } }] }),
          wrap({ choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 5, completion_tokens: 2, total_tokens: 7 } }), wrap("[DONE]"),
        ] };
      }
      if (url.pathname.endsWith("/wallets")) return json({ data: { daily_credits: { total_balance: 10 }, longterm_credits: { total_balance: 50 } } });
      assert.equal(url.pathname, "/api/v1/adapter/user/account-context");
      return json({ data: { plan: { name: "Fixture Plan" }, quota: { total: 100, remaining: 60 } } });
    } });
    const listed = await worker.call("models", { provider, auth: { ...qwenAuth(provider === "qwenwork-enterprise" ? host : undefined), expires: 1 } });
    assert.equal(listed.event, "result");
    assert.equal(listed.result.fallback, false);
    assert.equal(listed.result.models[0].id, "flash");
    assert.equal(listed.result.models[0].protocol, "chat");
    const response = await worker.call("fetch", { provider, model: "flash", body: { messages: [{ role: "user", content: "Hello" }], stream: true } });
    assert.equal(response.event, "end");
    assert.equal(response.events[0].status, 200);
    assert.match(chunkText(response), /Hello fixture/);
    assert.match(chunkText(response), /\[DONE\]/);
    const usage = await worker.call("usage", { provider });
    assert.equal(usage.result.plan, "Fixture Plan");
    assert.equal(usage.result.windows[0].display, "40 / 100 credits");
    assert.equal(worker.saves.length, 1);
    if (provider === "qwenwork-enterprise") assert.equal(worker.saves[0].enterpriseHost, host);
  });
}

test("provider boolean options apply per command without leaking between account requests", async (t) => {
  const bodies = [];
  const worker = await fixture(t, { network: (request) => { bodies.push(JSON.parse(Buffer.from(request.body, "base64"))); return json({}); } });
  for (const [provider, key] of [["grok", "x_search"], ["factory", "standard_only"], ["commandcode-plan", "zdr"]]) {
    const description = await worker.call("describe", { provider });
    assert.deepEqual(description.result.options.map((option) => option.key), [key]);
  }
  for (const options of [{ x_search: true }, {}, { x_search: false }]) {
    assert.equal((await worker.call("fetch", { model: "grok-4.7", options,
      auth: { type: "oauth", access: "synthetic", expires: future() }, body: { input: "Hi", tool_choice: "auto" } })).event, "end");
  }
  assert.deepEqual(bodies[0].tools, [{ type: "x_search" }]);
  assert.equal(bodies[1].tools, undefined);
  assert.equal(bodies[2].tools, undefined);
  for (const options of [{ x_search: "true" }, { undeclared: true }, []]) {
    assert.equal((await worker.call("usage", { options })).error.code, "invalid_request");
  }
  assert.equal(worker.requests.length, 3);
});

test("daily check-in returns claimed credits and preserves inactive and unsupported states", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    assert.equal(new URL(request.url).hostname, "openapi.qoder.com.cn");
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-device"]);
    return request.url.endsWith("/claim") ? json({ status: "CLAIMED", benefit: { amount: 100 } }) :
      json({ campaigns: [{ campaignId: "fixture", actionType: "CLAIM_BENEFIT", claimStatus: "CLAIMABLE", benefit: { amount: 100 } }] });
  } });
  const result = await worker.call("checkin", { provider: "qoder-cn", auth: { ...qwenAuth(), deviceChat: false } });
  assert.deepEqual(result.result, { outcome: "claimed", credit: 100 });
  const inactive = await worker.call("checkin", { provider: "trae-global" });
  assert.equal(inactive.result.outcome, "inactive");
  assert.equal((await worker.call("checkin", { provider: "cursor" })).error.code, "operation_not_supported");
  assert.equal(worker.requests.length, 2);
});

test("WorkBuddy China discovers Claude Messages while international accounts retain Chat", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    const url = new URL(request.url);
    if (url.pathname === "/v3/config") return json({ data: { agents: [{ name: "cli", models: ["claude-fixture"] }], models: [{ id: "claude-fixture", supportsImages: true }] } });
    const china = url.hostname === "copilot.tencent.com";
    assert.equal(url.pathname, china ? "/v1/messages" : "/v2/chat/completions");
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-workbuddy"]);
    if (china) {
      assert.deepEqual(request.headers["anthropic-version"], ["2023-06-01"]);
      const body = JSON.parse(Buffer.from(request.body, "base64"));
      assert.equal(body.system, "Fixture instructions");
      assert.deepEqual(body.output_config, { effort: "max" });
    }
    return { status: 200, headers: { "content-type": ["text/event-stream"] }, body: china ? "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n" : "data: [DONE]\n\n" };
  } });
  for (const provider of ["workbuddy", "workbuddy-ai"]) {
    const listed = await worker.call("models", { provider, auth: { type: "oauth", access: "synthetic-workbuddy", uid: "fixture", expires: future() } });
    assert.equal(listed.result.fallback, false);
    assert.equal(listed.result.models[0].protocol, provider === "workbuddy" ? "anthropic" : "chat");
    const result = await worker.call("fetch", { provider, model: "claude-fixture", body: { messages: [{ role: "user", content: "Hi" }],
      system: "Fixture instructions", max_tokens: 64, output_config: { effort: "max" }, stream: true } });
    assert.equal(result.event, "end");
    assert.equal(result.events[0].status, 200);
  }
});
