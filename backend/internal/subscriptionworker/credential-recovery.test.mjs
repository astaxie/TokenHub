import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

const factory = { provider: "factory", auth: { type: "api", key: "fk-synthetic" }, model: "glm-5.3",
  body: { messages: [{ role: "user", content: "Hello" }] } };
const json = (body) => ({ status: 200, body: JSON.stringify(body) });

test("API metadata persistence retries before inference and keeps the saved snapshot", async (t) => {
  let inference = 0;
  const worker = await fixture(t, { rejectSave: (_auth, count) => count < 3, network: (request) => {
    if (request.url.endsWith("/api/cli/whoami")) return json({ email: "fixture@example.com", region: "" });
    assert.match(request.url, /\/chat\/completions$/);
    assert.equal(worker.saves.length, 3, "inference must wait for a successful durable acknowledgement");
    inference++;
    return json({ choices: [{ message: { role: "assistant", content: "Hello" }, finish_reason: "stop" }] });
  } });
  const failed = await worker.call("fetch", factory);
  assert.equal(failed.error.code, "credential_save_failed");
  assert.equal(worker.requests.length, 1);
  assert.equal((await worker.call("fetch", factory)).error.code, "credential_save_failed");
  assert.equal(worker.requests.length, 1);
  assert.equal((await worker.call("fetch", factory)).event, "end");
  assert.equal(inference, 1);
  assert.deepEqual(worker.saves[0], worker.saves[1]);
  assert.deepEqual(worker.saves[1], worker.saves[2]);
  const stored = await worker.call("refresh", { provider: "factory" });
  assert.deepEqual(stored.result.auth, worker.saves[2], "the stale command auth must not undo recovered metadata");
  assert.equal(stored.result.auth.metadata.email, "fixture@example.com");
});

test("metadata recovery never overwrites changed incoming credentials", async (t) => {
  const worker = await fixture(t, { rejectSave: (_auth, count) => count === 1,
    network: () => json({ email: "fixture@example.com", region: "" }) });
  assert.equal((await worker.call("fetch", factory)).error.code, "credential_save_failed");
  for (const auth of [{ ...factory.auth, key: "fk-replaced" }, { ...factory.auth, metadata: { email: "new@example.com" } }]) {
    assert.equal((await worker.call("fetch", { ...factory, auth })).error.code, "credential_save_failed");
  }
  assert.equal(worker.saves.length, 1, "changed credentials must not authorize a stale metadata write");
  assert.equal(worker.requests.length, 1);
});

test("failed OAuth rotations remain blocked without spending refresh tokens again", async (t) => {
  const auth = { type: "oauth", access: "synthetic-old", refresh: "synthetic-refresh", expires: 1 };
  const worker = await fixture(t, { rejectSave: (_auth, count) => count === 1,
    network: () => json({ access_token: "synthetic-next", refresh_token: "synthetic-rotated", expires_in: 3600 }) });
  assert.equal((await worker.call("refresh", { auth })).error.code, "credential_save_failed");
  for (const incoming of [auth, worker.saves[0]]) {
    assert.equal((await worker.call("models", { auth: incoming })).error.code, "credential_save_failed");
  }
  assert.equal(worker.requests.length, 1);
  assert.equal(worker.saves.length, 1);
});
