import assert from "node:assert/strict";
import { AsyncLocalStorage } from "node:async_hooks";
import test from "node:test";
import { createHost } from "./host.mjs";

const previous = { type: "oauth", access: "synthetic-old", refresh: "synthetic-refresh", expires: 1 };

function fixture(t, { rejectImmediately = false } = {}) {
  const setTimer = globalThis.setTimeout;
  t.mock.method(globalThis, "setTimeout", (callback, delay, ...args) => setTimer(callback, delay === 30000 ? 5 : delay, ...args));
  const updates = [];
  const requests = [];
  const events = [];
  let serial = 0;
  t.mock.method(globalThis, "fetch", async (input, init) => {
    const url = String(input);
    requests.push({ url, init });
    if (url === "https://auth.x.ai/oauth2/token") {
      assert.equal(new URLSearchParams(init.body).get("refresh_token"), "synthetic-refresh");
      return Response.json({ access_token: "synthetic-next", refresh_token: "synthetic-rotated", expires_in: 3600 });
    }
    assert.equal(url, "https://cli-chat-proxy.grok.com/v1/responses");
    return Response.json({ id: "synthetic-response", status: "completed", output: [] });
  });
  const host = createHost({ scope: new AsyncLocalStorage(), emit: async (event) => {
    events.push(event);
    if (event.event === "auth.update") {
      updates.push(event);
      if (rejectImmediately) await host.dispatch({ operation: "auth.ack", update_id: event.update_id, error: "save_failed" });
    }
  } });
  async function call(operation, auth) {
    const id = String(++serial);
    // Keep the test alive while the production save timer is deliberately unref'ed.
    const keepAlive = setTimer(() => {}, 2000);
    try {
      await host.dispatch({ id, operation, provider: "grok", resource_id: "account", auth,
        model: "grok-4.7", protocol: "responses", body: { input: "hello" } });
      return events.findLast((event) => event.id === id && ["result", "end", "error"].includes(event.event));
    } finally { clearTimeout(keepAlive); }
  }
  return { host, call, updates, requests };
}

test("a matching late durable acknowledgement recovers a timed-out rotation", async (t) => {
  const worker = fixture(t);
  assert.equal((await worker.call("refresh", previous)).error.code, "credential_save_failed");
  const update = worker.updates[0];
  // Incoming credentials alone cannot establish that this exact save completed.
  assert.equal((await worker.call("fetch", update.auth)).error.code, "credential_save_failed");
  await worker.host.dispatch({ operation: "auth.ack", update_id: "foreign-update" });
  assert.equal((await worker.call("fetch", update.auth)).error.code, "credential_save_failed");
  assert.equal(worker.requests.length, 1);
  await worker.host.dispatch({ operation: "auth.ack", update_id: update.update_id });
  assert.equal((await worker.call("fetch", structuredClone(update.auth))).event, "end");
  assert.equal(worker.updates.length, 1, "a persisted rotation must not be submitted again");
  assert.equal(worker.requests.length, 2, "recovery must not spend the refresh token twice");
  assert.equal(new Headers(worker.requests[1].init.headers).get("authorization"), "Bearer synthetic-next");
});

for (const rejectImmediately of [false, true]) {
  test(`${rejectImmediately ? "immediate" : "late"} rejection cannot be overridden by a later success`, async (t) => {
    const worker = fixture(t, { rejectImmediately });
    assert.equal((await worker.call("refresh", previous)).error.code, "credential_save_failed");
    const update = worker.updates[0];
    if (!rejectImmediately) await worker.host.dispatch({ operation: "auth.ack", update_id: update.update_id, error: "save_failed" });
    await worker.host.dispatch({ operation: "auth.ack", update_id: update.update_id });
    assert.equal((await worker.call("fetch", update.auth)).error.code, "credential_save_failed");
    assert.equal(worker.requests.length, 1);
    assert.equal(worker.updates.length, 1);
  });
}

test("late recovery never overwrites newer administrator credentials", async (t) => {
  const worker = fixture(t);
  assert.equal((await worker.call("refresh", previous)).error.code, "credential_save_failed");
  const update = worker.updates[0];
  const replacement = { type: "oauth", access: "synthetic-admin", refresh: "synthetic-admin-refresh", expires: Date.now() + 3600000 };
  assert.equal((await worker.call("fetch", replacement)).error.code, "credential_save_failed");
  await worker.host.dispatch({ operation: "auth.ack", update_id: update.update_id });
  assert.equal((await worker.call("fetch", replacement)).event, "end");
  assert.equal(worker.updates.length, 1, "recovery must not write over an administrator revision");
  assert.equal(new Headers(worker.requests[1].init.headers).get("authorization"), "Bearer synthetic-admin");
});
