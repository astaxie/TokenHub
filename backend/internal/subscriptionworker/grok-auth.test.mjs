import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";
import { publicError } from "./validation.mjs";

const auth = { type: "oauth", access: "synthetic-grok-access", refresh: "synthetic-grok-refresh", expires: Date.now() + 3600000 };
const missingScope = { code: "permission-denied", error: "OAuth2 token missing required scope: api:access" };

test("Grok device authorization grants API access before model discovery and inference", async (t) => {
  let authorizedScope = "";
  const worker = await fixture(t, { network: request => {
    const body = Buffer.from(request.body, "base64").toString();
    if (request.url.endsWith("/device/code")) {
      authorizedScope = new URLSearchParams(body).get("scope");
      return { status: 200, body: JSON.stringify({ device_code: "synthetic-device", verification_uri: "https://accounts.x.ai/oauth2/device", interval: 1, expires_in: 10 }) };
    }
    if (request.url === "https://auth.x.ai/oauth2/token") {
      const form = new URLSearchParams(body);
      assert.equal(form.get("grant_type"), "urn:ietf:params:oauth:grant-type:device_code");
      assert.equal(form.get("device_code"), "synthetic-device");
      return { status: 200, body: JSON.stringify({ access_token: auth.access, refresh_token: auth.refresh, expires_in: 3600, scope: authorizedScope }) };
    }
    assert.deepEqual(request.headers.authorization, [`Bearer ${auth.access}`]);
    if (request.url.endsWith("/models")) return { status: 200, body: JSON.stringify({ data: [{ id: "grok-4.7", api_backend: "responses" }] }) };
    assert.equal(request.url, "https://cli-chat-proxy.grok.com/v1/responses");
    assert.equal(JSON.parse(body).model, "grok-4.7");
    if (!authorizedScope.split(/\s+/).includes("api:access")) return { status: 403, body: JSON.stringify(missingScope) };
    return { status: 200, headers: { "content-type": ["text/event-stream"] }, body: "data: {\"type\":\"response.completed\"}\n\n" };
  } });
  const start = await worker.call("auth.start", { method_id: "0" });
  assert.equal(start.event, "result");
  let status;
  for (let attempt = 0; attempt < 30; attempt++) {
    status = await worker.call("auth.status", { session_id: start.result.session_id });
    if (status.result.state === "complete") break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.equal(status.result.state, "complete");
  assert.equal(worker.saves.length, 1);
  assert.equal((await worker.call("models")).result.models[0].id, "grok-4.7");
  const response = await worker.call("fetch", { model: "grok-4.7", protocol: "responses", body: { input: "Reply with OK.", stream: true } });
  assert.equal(response.event, "end");
  assert.equal(response.events.find(event => event.event === "response").status, 200, "The authorized account must be able to infer, not only list models");
  for (const scope of ["openid", "profile", "email", "offline_access", "grok-cli:access", "api:access"]) assert.ok(authorizedScope.split(/\s+/).includes(scope));
  assert.equal(worker.requests.filter(request => request.url === "https://auth.x.ai/oauth2/token").length, 1);
});

test("Grok missing API scope requests reauthorization without refreshing or exposing upstream data", async (t) => {
  const worker = await fixture(t, { network: () => ({ status: 403, body: JSON.stringify({ ...missingScope, account: "private-account@example.invalid", token: "private-upstream-token" }) }) });
  const result = await worker.call("fetch", { auth, model: "grok-4.7", protocol: "responses", body: { input: "Hello" } });
  assert.equal(result.event, "error");
  assert.deepEqual(result.error, { code: "grok_api_scope_missing", status: 409, upstream_status: 403,
    message: "Grok authorization is missing the api:access permission required for inference. Reauthorize this account and save the updated credentials." });
  assert.equal(worker.requests.length, 1, "Refreshing cannot expand the scope of the original grant");
  assert.equal(worker.saves.length, 0);
  assert.ok(!JSON.stringify(result).includes("private-"));
});

test("Grok preserves other forbidden responses and bounds scope inspection", async (t) => {
  let body;
  const worker = await fixture(t, { network: () => ({ status: 403, body }) });
  for (body of [JSON.stringify({ code: "permission-denied", error: "Subscription access denied" }),
    JSON.stringify({ ...missingScope, code: "different-code" }), JSON.stringify({ ...missingScope, error: missingScope.error + " private-token" }),
    "{invalid", JSON.stringify({ ...missingScope, padding: "x".repeat(4096) })]) {
    const result = await worker.call("fetch", { auth, model: "grok-4.7", protocol: "responses", body: { input: "Hello" } });
    assert.equal(result.event, "end");
    assert.equal(result.events.find(event => event.event === "response").status, 403);
    assert.equal(result.events.filter(event => event.event === "chunk").map(event => Buffer.from(event.data, "base64").toString()).join(""), body);
  }
});

test("Grok scope diagnostics retain one-time unauthorized renewal and normal streams", async (t) => {
  let attempts = 0;
  const worker = await fixture(t, { network: request => {
    if (request.url === "https://auth.x.ai/oauth2/token") return { status: 200, body: JSON.stringify({ access_token: "synthetic-new-access", refresh_token: "synthetic-new-refresh", expires_in: 3600 }) };
    if (++attempts === 1) return { status: 401, body: JSON.stringify(missingScope) };
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-new-access"]);
    return { status: 200, headers: { "content-type": ["text/event-stream"] }, chunks: ["data: one\n\n", "data: two\n\n"] };
  } });
  const result = await worker.call("fetch", { auth, model: "grok-4.7", protocol: "responses", body: { input: "Hello" } });
  assert.equal(result.event, "end");
  assert.equal(attempts, 2);
  assert.equal(worker.saves.length, 1);
  assert.equal(result.events.filter(event => event.event === "chunk").map(event => Buffer.from(event.data, "base64").toString()).join(""), "data: one\n\ndata: two\n\n");
});

test("Grok scope public diagnostics ignore arbitrary error properties", () => {
  const result = publicError({ code: "grok_api_scope_missing", message: "private-token", status: 401, upstream_status: 500, stack: "private-stack" });
  assert.equal(result.code, "grok_api_scope_missing");
  assert.equal(result.status, 409);
  assert.equal(result.upstream_status, 403);
  assert.ok(!JSON.stringify(result).includes("private-"));
});
