import assert from "node:assert/strict";
import { AsyncLocalStorage } from "node:async_hooks";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { createServer } from "node:http";
import test from "node:test";
import { validateAuth, validateTarget } from "./validation.mjs";
import { createNetworkRelay } from "./network.mjs";

const directory = dirname(fileURLToPath(import.meta.url));

test("fast response consumers cannot exceed the cumulative byte limit", async () => {
  const scope = new AsyncLocalStorage();
  const events = [];
  const relay = createNetworkRelay(scope, async (event) => { events.push(event); }, { maxResponseBytes: 32 });
  const context = { id: "fast-reader", provider: "grok", resourceID: "resource-test", controller: new AbortController() };
  const pending = scope.run(context, () => relay.fetch("https://cli-chat-proxy.grok.com/v1/responses"));
  const requestID = events[0].request_id;
  relay.receive({ operation: "network.response", request_id: requestID, status: 200 });
  const reader = (await pending).body.getReader();
  let consumed = 0;
  for (let index = 0; index < 4; index++) {
    relay.receive({ operation: "network.chunk", request_id: requestID, data: Buffer.alloc(8).toString("base64") });
    const { value, done } = await reader.read();
    assert.equal(done, false);
    consumed += value.byteLength;
  }
  assert.equal(consumed, 32);
  assert.equal(events.filter((event) => event.event === "network.cancel").length, 0);
  const failed = assert.rejects(reader.read(), { code: "body_too_large", message: "Subscription response exceeds the total size limit" });
  relay.receive({ operation: "network.chunk", request_id: requestID, data: Buffer.from("x").toString("base64") });
  await failed;
  assert.equal(events.at(-1).event, "network.cancel");
  assert.equal(events.at(-1).request_id, requestID);
  relay.receive({ operation: "network.end", request_id: requestID });
  assert.equal(events.filter((event) => event.event === "network.cancel").length, 1);
});

test("relay failures cancel parent streams before discarding their state", async () => {
  const scope = new AsyncLocalStorage();
  const events = [];
  const relay = createNetworkRelay(scope, async (event) => { events.push(event); });
  const context = { id: "relay-test", provider: "grok", resourceID: "resource-test", controller: new AbortController() };
  const pending = scope.run(context, () => relay.fetch("https://cli-chat-proxy.grok.com/v1/responses"));
  const requestID = events[0].request_id;
  relay.receive({ operation: "network.response", request_id: requestID, status: 200 });
  const response = await pending;
  const body = response.text();
  relay.receive({ operation: "network.chunk", request_id: requestID, data: Buffer.alloc(1024 * 1024 + 1).toString("base64") });
  await assert.rejects(body, /too slow/);
  assert.equal(events.at(-1).event, "network.cancel");
  assert.equal(events.at(-1).request_id, requestID);
});

test("rejected redirect destinations cancel the original parent stream", async () => {
  const scope = new AsyncLocalStorage();
  const events = [];
  const relay = createNetworkRelay(scope, async (event) => { events.push(event); });
  const context = { id: "redirect-test", provider: "grok", resourceID: "resource-test", controller: new AbortController() };
  const pending = scope.run(context, () => relay.fetch("https://cli-chat-proxy.grok.com/v1/responses"));
  const rejected = assert.rejects(pending, { code: "upstream_host_not_allowed" });
  const requestID = events[0].request_id;
  relay.receive({ operation: "network.response", request_id: requestID, status: 307, headers: { location: ["https://attacker.example/collect"] } });
  await rejected;
  assert.equal(events.at(-1).event, "network.cancel");
  assert.equal(events.at(-1).request_id, requestID);
  assert.equal(events.filter((event) => event.event === "network.request").length, 1);
});

test("cross-origin redirects reject every adapter credential header and replayed body", async () => {
  async function redirected(init, status = 307, location = "https://joycode.jd.com/collect") {
    const scope = new AsyncLocalStorage();
    const events = [];
    const relay = createNetworkRelay(scope, async (event) => {
      events.push(event);
      if (event.event !== "network.request") return;
      const first = events.filter((item) => item.event === "network.request").length === 1;
      queueMicrotask(() => relay.receive({ operation: "network.response", request_id: event.request_id,
        status: first ? status : 200, headers: first ? { location: [location] } : {}, body: "" }));
    });
    const context = { id: "redirect-test", provider: "joycode", resourceID: "resource-test", controller: new AbortController() };
    const result = scope.run(context, () => relay.fetch("https://api-ai.jd.com/request", init));
    return { result, events };
  }
  for (const name of ["authorization", "cookie", "x-api-key", "ptKey", "X-Authorization", "X-Cloudide-Token", "x-ide-token",
    "x-security-token", "X-Refresh-Token", "x-goog-api-key", "x-nous-refresh-token", "proxy-authorization", "x-future-account-key"]) {
    const { result, events } = await redirected({ headers: { [name]: "synthetic-account-secret" } });
    await assert.rejects(result, { code: "upstream_host_not_allowed" }, name);
    assert.equal(events.filter((event) => event.event === "network.request").length, 1, name);
  }
  const replay = await redirected({ method: "POST", body: "refresh_token=synthetic-account-secret" });
  await assert.rejects(replay.result, { code: "upstream_host_not_allowed" });
  assert.equal(replay.events.filter((event) => event.event === "network.request").length, 1);

  const unsigned = await redirected({ headers: { Accept: "application/json", "User-Agent": "synthetic-client" } });
  assert.equal((await unsigned.result).status, 200);
  assert.equal(unsigned.events.filter((event) => event.event === "network.request").length, 2);
  const postToGet = await redirected({ method: "POST", body: "code=synthetic-code", headers: { "content-type": "text/plain", "content-length": "19" } }, 302);
  assert.equal((await postToGet.result).status, 200);
  const followed = postToGet.events.filter((event) => event.event === "network.request").at(-1);
  assert.equal(followed.method, "GET");
  assert.equal(followed.body, "");
  assert.equal(followed.headers["content-type"], undefined);
  assert.equal(followed.headers["content-length"], undefined);
  const sameOrigin = await redirected({ method: "POST", body: "synthetic-payload", headers: { ptKey: "synthetic-account-ticket" } }, 307, "https://api-ai.jd.com/redirected");
  assert.equal((await sameOrigin.result).status, 200);
  const preserved = sameOrigin.events.filter((event) => event.event === "network.request").at(-1);
  assert.deepEqual(preserved.headers.ptkey, ["synthetic-account-ticket"]);
  assert.equal(Buffer.from(preserved.body, "base64").toString(), "synthetic-payload");
});

async function fixture(t, { network = () => ({ status: 404, body: "{}" }), rejectSave = false } = {}) {
  const privateHome = await mkdtemp(join(tmpdir(), "tokenhub-worker-test-"));
  const child = spawn(process.execPath, [join(directory, "worker.mjs")], {
    cwd: privateHome, env: { HOME: privateHome, USERPROFILE: privateHome, XDG_CONFIG_HOME: privateHome }, stdio: ["pipe", "pipe", "pipe"],
  });
  const calls = new Map();
  const requests = [];
  const cancellations = [];
  const saves = [];
  let serial = 0;
  let buffer = "";
  let stderr = "";
  child.stderr.on("data", (data) => { stderr += data; });
  const send = (value) => child.stdin.write(JSON.stringify(value) + "\n");
  child.stdout.on("data", (data) => {
    buffer += data;
    for (;;) {
      const end = buffer.indexOf("\n");
      if (end < 0) break;
      const event = JSON.parse(buffer.slice(0, end));
      buffer = buffer.slice(end + 1);
      if (event.event === "network.request") {
        requests.push(event);
        void Promise.resolve(network(event)).then((response) => {
          if (!response) return;
          const headers = response.headers ?? { "content-type": ["application/json"] };
          send({ operation: "network.response", request_id: event.request_id, status: response.status, headers });
          for (const chunk of response.chunks ?? [response.body ?? ""]) send({ operation: "network.chunk", request_id: event.request_id, data: Buffer.from(chunk).toString("base64") });
          send({ operation: "network.end", request_id: event.request_id });
        }).catch((error) => {
          calls.get(event.id)?.reject(error);
          send({ operation: "network.error", request_id: event.request_id });
        });
      } else if (event.event === "auth.update") {
        saves.push(event.auth);
        send({ operation: "auth.ack", update_id: event.update_id, ...(rejectSave ? { error: "save_failed" } : {}) });
      } else if (event.event === "network.cancel") {
        cancellations.push(event.request_id);
      } else {
        const pending = calls.get(event.id);
        if (!pending) continue;
        pending.events.push(event);
        if (["result", "end", "error"].includes(event.event)) {
          calls.delete(event.id);
          clearTimeout(pending.timer);
          pending.resolve({ ...event, events: pending.events });
        }
      }
    }
  });
  child.on("exit", () => {
    for (const call of calls.values()) call.reject(new Error("Worker exited unexpectedly: " + stderr));
  });
  t.after(async () => {
    child.stdin.end();
    if (child.exitCode === null) await once(child, "exit");
    await rm(privateHome, { recursive: true, force: true });
    assert.equal(stderr, "", "worker must not log credentials or vendor diagnostics");
  });
  function call(operation, fields = {}) {
    return new Promise((resolve, reject) => {
      const id = fields.id ?? String(++serial);
      const timer = setTimeout(() => reject(new Error("Worker command timed out: " + operation)), 10000);
      calls.set(id, { resolve, reject, events: [], timer });
      send({ id, operation, resource_id: "resource-test", provider: "grok", ...fields });
    });
  }
  return { call, requests, saves, cancellations, send };
}

test("all bundled subscription providers initialize without discovery or network", async (t) => {
  const worker = await fixture(t);
  const listed = await worker.call("providers");
  assert.equal(listed.result.providers.length, 26);
  for (const provider of listed.result.providers) {
    const result = await worker.call("describe", { provider });
    assert.equal(result.event, "result", provider);
    assert.ok(result.result.models.length > 0, provider);
    assert.ok(result.result.methods.every((method) => !/desktop|CLI's|app's sign-in|桌面端/i.test(method.label)), provider);
    for (const model of result.result.models) assert.ok(["chat", "anthropic", "responses", "gemini"].includes(model.protocol), provider);
  }
  assert.equal(worker.requests.length, 0);
  const rejected = await worker.call("describe", { provider: "../../untrusted" });
  assert.equal(rejected.error.code, "provider_not_supported");
});

test("credential imports reject local discovery and untrusted endpoints", () => {
  for (const auth of [
    { type: "oauth", source: "kiro-cli" },
    { type: "api", key: "fake", metadata: { path: "/tmp/other-account" } },
    { type: "oauth", refresh: JSON.stringify({ base: "https://attacker.example", token: "fake" }) },
    { type: "oauth", access: "fake", metadata: { server: "http://127.0.0.1:8080" } },
  ]) assert.throws(() => validateAuth("devin", auth));
  assert.throws(() => validateTarget("grok", "https://cli-chat-proxy.grok.com.attacker.example/v1"));
  assert.throws(() => validateTarget("grok", "https://cli-chat-proxy.grok.com:8443/v1"));
  assert.equal(validateTarget("grok", "https://cli-chat-proxy.grok.com/v1").hostname, "cli-chat-proxy.grok.com");
});

test("fetch streams through the parent and substitutes only resource credentials", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    assert.equal(request.url, "https://cli-chat-proxy.grok.com/v1/responses");
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-resource-token"]);
    assert.equal(request.headers.cookie, undefined);
    assert.equal(JSON.parse(Buffer.from(request.body, "base64")).model, "grok-4.7");
    return { status: 200, headers: { "content-type": ["text/event-stream"] }, chunks: ["data: {\"delta\":\"hello\"}\n\n", "data: [DONE]\n\n"] };
  } });
  const response = await worker.call("fetch", { auth: { type: "oauth", access: "synthetic-resource-token", expires: Date.now() + 3600000 },
    model: "grok-4.7", protocol: "responses", headers: { authorization: ["Bearer caller-secret"], cookie: ["caller-cookie"] }, body: { input: "hello", stream: true } });
  assert.equal(response.event, "end");
  assert.equal(response.events[0].event, "response");
  assert.equal(response.events[0].status, 200);
  assert.equal(response.events.filter((event) => event.event === "chunk").map((event) => Buffer.from(event.data, "base64").toString()).join(""), "data: {\"delta\":\"hello\"}\n\ndata: [DONE]\n\n");
});

test("rotated credentials require a durable acknowledgement before inference", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    if (request.url === "https://auth.x.ai/oauth2/token") {
      const values = new URLSearchParams(Buffer.from(request.body, "base64").toString());
      assert.equal(values.get("grant_type"), "refresh_token");
      assert.equal(values.get("refresh_token"), "synthetic-refresh");
      return { status: 200, body: JSON.stringify({ access_token: "rotated-access", refresh_token: "rotated-refresh", expires_in: 3600 }) };
    }
    assert.equal(worker.saves.length, 1);
    assert.deepEqual(request.headers.authorization, ["Bearer rotated-access"]);
    return { status: 200, body: "{}" };
  } });
  const result = await worker.call("fetch", { model: "grok-4.7", protocol: "responses", auth: { type: "oauth", access: "old", refresh: "synthetic-refresh", expires: 1 }, body: { input: "hello" } });
  assert.equal(result.event, "end");
  assert.equal(worker.saves[0].refresh, "rotated-refresh");
  assert.equal(worker.requests.length, 2);
});

test("standard SDK loaders sign each model's native protocol with resource credentials", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    const body = JSON.parse(Buffer.from(request.body, "base64"));
    const anthropic = body.model === "glm-5.3-flash";
    const responses = body.model === "deepseek-v4-pro";
    assert.equal(request.url, "https://amr-link.open-design.ai/v1/" + (anthropic ? "messages" : responses ? "responses" : "chat/completions"));
    assert.deepEqual(request.headers[anthropic ? "x-api-key" : "authorization"], [anthropic ? "synthetic-runtime-key" : "Bearer synthetic-runtime-key"]);
    assert.equal(request.headers[anthropic ? "authorization" : "x-api-key"], undefined);
    assert.equal(request.headers.cookie, undefined);
    if (anthropic) assert.deepEqual(request.headers["anthropic-version"], ["2023-06-01"]);
    return { status: 200, body: "{}" };
  } });
  const description = await worker.call("describe", { provider: "opendesign" });
  for (const model of ["glm-5.3-flash", "deepseek-v4-pro", description.result.models.find((item) => item.protocol === "chat").id]) {
    const result = await worker.call("fetch", { provider: "opendesign", model, auth: { type: "api", key: "synthetic-runtime-key" },
      headers: { authorization: ["Bearer caller-secret"], "x-api-key": ["caller-secret"], cookie: ["caller-cookie"] }, body: { messages: [] } });
    assert.equal(result.event, "end", model);
  }
  assert.equal(worker.requests.length, 3);
});

test("model protocol mismatch is rejected before sending inference", async (t) => {
  const worker = await fixture(t);
  const result = await worker.call("fetch", { model: "grok-4.7", protocol: "chat", auth: { type: "oauth", access: "synthetic", expires: Date.now() + 3600000 }, body: { messages: [] } });
  assert.equal(result.error.code, "protocol_mismatch");
  assert.equal(worker.requests.length, 0);
});

test("seed model fallback does not claim a live account inventory", async (t) => {
  const worker = await fixture(t, { network: () => ({ status: 503, body: "{}" }) });
  const result = await worker.call("models", { provider: "opendesign", auth: { type: "api", key: "synthetic-runtime-key" } });
  assert.equal(result.result.fallback, true);
  assert.ok(result.result.models.length > 0);
  const missing = await worker.call("fetch", { provider: "opendesign", model: "unknown-account-model", body: { messages: [] } });
  assert.equal(missing.error.code, "model_not_found");
  assert.ok(worker.requests.every(request => request.url.endsWith("/models")));
});

test("cancel aborts the corresponding parent transport request", async (t) => {
  const worker = await fixture(t, { network: () => null });
  const response = worker.call("fetch", { id: "cancel-me", model: "grok-4.7", auth: { type: "oauth", access: "synthetic", expires: Date.now() + 3600000 }, body: { input: "hello" } });
  for (let attempt = 0; attempt < 100 && !worker.requests.length; attempt++) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(worker.requests.length, 1);
  worker.send({ operation: "cancel", target_id: "cancel-me" });
  assert.equal((await response).error.code, "request_cancelled");
  assert.deepEqual(worker.cancellations, [worker.requests[0].request_id]);
});

test("credentials cannot follow an otherwise allowed cross-origin redirect", async (t) => {
  const worker = await fixture(t, { network: () => ({ status: 307, headers: { location: ["https://accounts.x.ai/collect"] }, body: "" }) });
  const result = await worker.call("fetch", { model: "grok-4.7", auth: { type: "oauth", access: "synthetic", expires: Date.now() + 3600000 }, body: { input: "hello" } });
  assert.equal(result.error.code, "upstream_host_not_allowed");
  assert.equal(worker.requests.length, 1);
});

test("accounts in separate resource scopes never share credentials", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    assert.deepEqual(request.headers.authorization, ["Bearer " + request.resource_id]);
    return { status: 200, body: "{}" };
  } });
  for (const resource_id of ["account-one", "account-two", "account-one"]) {
    const result = await worker.call("fetch", { resource_id, model: "grok-4.7", auth: { type: "oauth", access: resource_id, expires: Date.now() + 3600000 }, body: { input: "hello" } });
    assert.equal(result.event, "end");
  }
});

test("failed credential persistence stops inference and does not expose token values", async (t) => {
  const worker = await fixture(t, { rejectSave: true, network: () => ({ status: 200, body: JSON.stringify({ access_token: "must-stay-private", refresh_token: "rotated-private", expires_in: 3600 }) }) });
  const result = await worker.call("fetch", { model: "grok-4.7", auth: { type: "oauth", access: "old", refresh: "old-refresh", expires: 1 }, body: { input: "hello" } });
  assert.equal(result.error.code, "credential_save_failed");
  assert.equal(worker.requests.length, 1);
  assert.doesNotMatch(JSON.stringify(result.error), /must-stay-private|rotated-private/);
});

test("model discovery preserves account-specific protocols and context variants", async (t) => {
  const worker = await fixture(t, { network: () => ({ status: 200, body: JSON.stringify({ data: [
    { id: "grok-4.7", context_window: 256000, context_windows: [256000, 500000], reasoning_efforts: [{ value: "high" }, { value: "low" }] },
    { id: "image-only", api_backend: "images" },
  ] }) }) });
  const result = await worker.call("models", { auth: { type: "oauth", access: "synthetic", expires: Date.now() + 3600000 } });
  assert.deepEqual(result.result.models.map((model) => model.id), ["grok-4.7", "grok-4.7@500k"]);
  assert.equal(result.result.models[1].limit.context, 500000);
  assert.ok(result.result.models.every((model) => model.protocol === "responses"));
});

test("device authorization completes asynchronously and persists its account", async (t) => {
  const worker = await fixture(t, { network: (request) => request.url.endsWith("/device/code") ?
    { status: 200, body: JSON.stringify({ device_code: "synthetic-device", user_code: "ABCD", verification_uri: "https://accounts.x.ai/oauth2/device", interval: 1, expires_in: 10 }) } :
    { status: 200, body: JSON.stringify({ access_token: "device-access", refresh_token: "device-refresh", expires_in: 3600 }) } });
  const start = await worker.call("auth.start", { method_id: "0" });
  assert.equal(start.result.state, "pending");
  assert.equal(start.result.url, "https://accounts.x.ai/oauth2/device");
  let status;
  for (let attempt = 0; attempt < 30; attempt++) {
    status = await worker.call("auth.status", { session_id: start.result.session_id });
    if (status.result.state === "complete") break;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert.equal(status.result.state, "complete");
  assert.equal(worker.saves[0].refresh, "device-refresh");
  const replay = await worker.call("auth.callback", { session_id: start.result.session_id, code: "replayed-code" });
  assert.equal(replay.error.code, "invalid_session");
});

test("callback relay rejects listeners outside the authorization session", async (t) => {
  const worker = await fixture(t);
  const start = await worker.call("auth.start", { provider: "zed", method_id: "0" });
  assert.equal(start.event, "result");
  const result = await worker.call("auth.callback", { provider: "zed", session_id: start.result.session_id, url: "http://127.0.0.1:1/admin" });
  assert.equal(result.error.code, "callback_not_allowed");
  const cancelled = await worker.call("auth.cancel", { provider: "zed", session_id: start.result.session_id });
  assert.equal(cancelled.result.state, "cancelled");
});

test("callback relay binds the listener address and accepts its localhost alias", async (t) => {
  const worker = await fixture(t);
  const start = await worker.call("auth.start", { provider: "zed", method_id: "0" });
  assert.equal(start.event, "result");
  const port = Number(new URL(start.result.url).searchParams.get("native_app_port"));
  assert.ok(port > 0);
  let foreignCalls = 0;
  const foreign = createServer((_request, response) => { foreignCalls++; response.end("unowned listener"); });
  await new Promise((resolve, reject) => { foreign.once("error", reject); foreign.listen(port, "::1", resolve); });
  t.after(() => new Promise((resolve) => foreign.close(resolve)));
  const fields = { provider: "zed", session_id: start.result.session_id };
  const rejected = await worker.call("auth.callback", { ...fields, url: `http://[::1]:${port}/admin` });
  assert.equal(rejected.error?.code, "callback_not_allowed");
  assert.equal(foreignCalls, 0);
  for (const host of ["127.0.0.1", "localhost"]) {
    const owned = await worker.call("auth.callback", { ...fields, url: `http://${host}:${port}/missing-callback` });
    assert.equal(owned.result?.status, 404, host);
  }
  const cancelled = await worker.call("auth.cancel", fields);
  assert.equal(cancelled.result.state, "cancelled");
});

test("local authorization landing pages are marked only for owned listeners", async (t) => {
  const worker = await fixture(t);
  const start = await worker.call("auth.start", { provider: "autoclaw", method_id: "0", inputs: { vendor: "google" } });
  assert.equal(start.event, "result");
  assert.equal(start.result.local_listener, true);
  const url = new URL(start.result.url);
  assert.equal(url.hostname, "127.0.0.1");
  assert.ok(["18432", "19654", "19723", "53699"].includes(url.port));
  assert.equal(worker.requests.length, 0);
  const cancel = await worker.call("auth.cancel", { provider: "autoclaw", session_id: start.result.session_id });
  assert.equal(cancel.result.state, "cancelled");
});

test("vendored source remains byte-identical to its pinned provenance manifest", async () => {
  const provenance = JSON.parse(await readFile(join(directory, "vendor/PROVENANCE.json")));
  assert.equal(provenance.commit, "c9a0215f31d401ca4249fac33d03e6b97e44ebbb");
  for (const file of provenance.files) {
    const body = await readFile(join(directory, file.path));
    assert.equal(createHash("sha256").update(body).digest("hex"), file.sha256, file.path);
  }
});
