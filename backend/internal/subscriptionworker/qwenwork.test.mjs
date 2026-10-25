import assert from "node:assert/strict";
import { AsyncLocalStorage } from "node:async_hooks";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";
import { createNetworkRelay } from "./network.mjs";

test("QwenWork Enterprise identifies its internal seed fallback and recovers to a live catalog", async (t) => {
  let status = 503;
  const worker = await fixture(t, { network: (request) => {
    assert.equal(new URL(request.url).origin, "https://tenant.example");
    assert.equal(new URL(request.url).pathname, "/algo/api/v2/model/list");
    return { status, body: JSON.stringify(status === 503 ? { message: "Model catalog is unavailable" } :
      { qwork: [{ key: "live-fixture", display_name: "Live fixture", max_input_tokens: 4096 }] }) };
  } });
  const auth = { type: "oauth", uid: "synthetic-user", access: "synthetic-device", refresh: "synthetic-refresh",
    deviceToken: "synthetic-device", deviceRefresh: "synthetic-refresh", machineId: "synthetic-machine",
    deviceChat: true, enterpriseHost: "tenant.example", expires: Date.now() + 3600000 };
  for (const nextStatus of [503, 503, 200, 503]) {
    status = nextStatus;
    const result = await worker.call("models", { provider: "qwenwork-enterprise", auth });
    assert.equal(result.event, "result");
    assert.equal(result.result.fallback, status === 503, "a vendor-internal seed list must retain its provenance");
    assert.deepEqual(result.result.models.map((model) => model.id), status === 503 ?
      ["flash", "pro", "qwen3.8-max-preview"] : ["live-fixture"]);
  }
  assert.equal(worker.requests.length, 4);
});

test("response hooks observe one final response within their own account context", async () => {
  const scope = new AsyncLocalStorage();
  const observed = [];
  const requests = new Map();
  const relay = createNetworkRelay(scope, async (event) => {
    if (event.event !== "network.request") return;
    const count = (requests.get(event.id) ?? 0) + 1;
    requests.set(event.id, count);
    queueMicrotask(() => relay.receive({ operation: "network.response", request_id: event.request_id,
      status: count === 1 ? 307 : 503, headers: count === 1 ? { location: ["/final"] } : { "x-fixture": [event.id] },
      body: count === 1 ? "" : Buffer.from("original response").toString("base64") }));
  });
  const hooks = { "network.response": (request, response, operation) => {
    observed.push({ account: scope.getStore().id, url: request.url, status: response.status, operation });
    return response;
  } };
  await Promise.all(["hooked", "unrelated"].map(async (id) => {
    const response = await scope.run({ id, provider: "grok", resourceID: id, operation: "models", controller: new AbortController(),
      account: { hooks: id === "hooked" ? hooks : {} } }, () => relay.fetch("https://cli-chat-proxy.grok.com/original"));
    assert.equal(response.status, 503);
    assert.equal(response.headers.get("x-fixture"), id);
    assert.equal(await response.text(), "original response");
  }));
  assert.deepEqual(observed, [{ account: "hooked", url: "https://cli-chat-proxy.grok.com/original", status: 503, operation: "models" }]);
  assert.deepEqual([...requests.values()], [2, 2]);
});
