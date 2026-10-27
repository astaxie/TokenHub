import assert from "node:assert/strict";
import { AsyncLocalStorage } from "node:async_hooks";
import test from "node:test";
import { createNetworkRelay } from "./network.mjs";
import { fixture } from "./worker-fixture.mjs";

async function within(promise, milliseconds) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("HTTP failure inspection did not finish in time")), milliseconds);
    })]);
  } finally { clearTimeout(timer); }
}

async function relayFixture(t, { operation = "fetch", status = 429 } = {}) {
  const scope = new AsyncLocalStorage();
  const events = [];
  const context = { id: "diagnostic", provider: "cline", operation, controller: new AbortController() };
  const relay = createNetworkRelay(scope, async (event) => { events.push(event); });
  t.after(() => context.controller.abort());
  const pending = scope.run(context, () => relay.fetch("https://api.cline.bot/api/v1/chat/completions"));
  const requestID = events[0].request_id;
  const send = (message) => relay.receive({ request_id: requestID, ...message });
  send({ operation: "network.response", status, headers: { "content-type": ["application/json"], "retry-after": ["90"] } });
  return { response: await pending, events, context, send,
    chunk: (data) => send({ operation: "network.chunk", data: Buffer.from(data).toString("base64") }) };
}

test("real Cline failures finish on a complete diagnostic without waiting for EOF", async (t) => {
  for (const status of [429, 302]) await t.test(`HTTP ${status} without a completed body`, async (t) => {
    const diagnostic = JSON.stringify({ error: { message: "synthetic quota exhaustion" } });
    const worker = await fixture(t, { network: (request) => {
      worker.send({ operation: "network.response", request_id: request.request_id, status,
        headers: { "content-type": ["application/json"], "retry-after": ["90"] } });
      worker.send({ operation: "network.chunk", request_id: request.request_id, data: Buffer.from(diagnostic).toString("base64") });
      return null;
    } });
    const description = await worker.call("describe", { provider: "cline" });
    const pending = worker.call("fetch", { provider: "cline", model: description.result.models[0].id,
      auth: { type: "api", key: "synthetic-account-key" }, body: { messages: [{ role: "user", content: "Hello" }], stream: true } });
    const result = await within(pending, 700);
    assert.equal(result.event, "end");
    assert.equal(result.events[0].status, status === 302 ? 502 : status);
    const body = result.events.filter((event) => event.event === "chunk").map((event) => Buffer.from(event.data, "base64").toString()).join("");
    assert.match(body, /synthetic quota exhaustion/);
    assert.deepEqual(worker.cancellations, [worker.requests[0].request_id]);
  });
});

test("real Factory incomplete errors are bounded and retain account state and retry headers", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    worker.send({ operation: "network.response", request_id: request.request_id, status: 403,
      headers: { "content-type": ["application/json"], "retry-after": ["90"] } });
    worker.send({ operation: "network.chunk", request_id: request.request_id,
      data: Buffer.from('{"error":{"message":"token expired').toString("base64") });
    return null;
  } });
  const description = await worker.call("describe", { provider: "factory" });
  const model = description.result.models.find((item) => item.protocol === "anthropic");
  const result = await within(worker.call("fetch", { provider: "factory", model: model.id,
    auth: { type: "api", key: "synthetic-account-key", metadata: { region: "us" } },
    body: { messages: [{ role: "user", content: "Hello" }], max_tokens: 64, stream: true } }), 2000);
  assert.equal(result.event, "end");
  assert.equal(result.events[0].status, 403);
  assert.equal(result.events[0].sign_in, "kept");
  assert.deepEqual(result.events[0].headers["retry-after"], ["90"]);
  const body = result.events.filter((event) => event.event === "chunk").map((event) => Buffer.from(event.data, "base64").toString()).join("");
  assert.doesNotMatch(body, /token expired/);
  assert.deepEqual(worker.cancellations, [worker.requests[0].request_id]);
});

test("oversized error diagnostics fall back to HTTP metadata without exposing a prefix", async (t) => {
  const relay = await relayFixture(t);
  const body = relay.response.text();
  relay.chunk('{"error":{"message":"token expired');
  relay.chunk("x".repeat(64 * 1024));
  assert.equal(await within(body, 200), "");
  assert.equal(relay.response.status, 429);
  assert.equal(relay.response.headers.get("retry-after"), "90");
  assert.equal(relay.events.filter((event) => event.event === "network.cancel").length, 1);
  relay.send({ operation: "network.end" });
  relay.context.controller.abort();
  assert.equal(relay.events.filter((event) => event.event === "network.cancel").length, 1);
});

test("completed plaintext and token rejection diagnostics keep their adapter meaning", async (t) => {
  for (const diagnostic of ["synthetic upstream unavailable", JSON.stringify({ error: "invalid_grant", error_description: "Synthetic refresh token expired" })]) {
    const relay = await relayFixture(t, { status: 400 });
    relay.chunk(diagnostic);
    relay.send({ operation: "network.end" });
    assert.equal(await relay.response.text(), diagnostic);
    assert.equal(relay.response.status, 400);
  }
});

test("caller cancellation during incomplete diagnostic inspection remains cancellation", async (t) => {
  const relay = await relayFixture(t);
  const body = relay.response.text();
  relay.chunk('{"error":');
  relay.context.controller.abort();
  await assert.rejects(body, { code: "request_cancelled", status: 499 });
  assert.equal(relay.events.filter((event) => event.event === "network.cancel").length, 1);
  relay.chunk("late data");
  relay.send({ operation: "network.end" });
});

test("failed diagnostic reads preserve known HTTP status while guarded proxy faults remain errors", async (t) => {
  const relay = await relayFixture(t);
  relay.chunk('{"error":');
  relay.send({ operation: "network.error", error: "upstream_request_failed" });
  assert.equal(await relay.response.text(), "");
  assert.equal(relay.response.status, 429);
  assert.equal(relay.response.headers.get("retry-after"), "90");

  const proxy = await relayFixture(t);
  const body = proxy.response.text();
  proxy.send({ operation: "network.error", error: "provider_proxy_connect_failed", status: 502 });
  await assert.rejects(body, { code: "provider_proxy_connect_failed" });
});

test("successful streams and standalone OAuth operations retain their ordinary body lifecycle", async (t) => {
  await Promise.all([{ status: 200 }, { operation: "auth.start", status: 400 }].map(async (options) => {
    const relay = await relayFixture(t, options);
    const reader = relay.response.body.getReader();
    const first = "x".repeat(65 * 1024);
    relay.chunk(first);
    assert.equal(new TextDecoder().decode((await reader.read()).value), first);
    let settled = false;
    const next = reader.read().then((result) => { settled = true; return result; });
    await new Promise((resolve) => setTimeout(resolve, 1100));
    assert.equal(settled, false, "The inference error timer must not close unrelated response bodies");
    relay.chunk("last");
    assert.equal(new TextDecoder().decode((await next).value), "last");
    relay.send({ operation: "network.end" });
    assert.equal((await reader.read()).done, true);
    assert.equal(relay.events.filter((event) => event.event === "network.cancel").length, 0);
    reader.releaseLock();
  }));
});
