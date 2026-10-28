import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

const auth = { type: "api", key: "synthetic-cline-key" };
const model = "cline-pass/mimo-v2.6-flash";
const request = { provider: "cline", auth, model, protocol: "chat", body: { messages: [{ role: "user", content: "Hello" }] } };
const responseOf = (result) => {
  assert.equal(result.event, "end");
  return result.events.find((event) => event.event === "response");
};

test("Cline preserves only the current inference retry delay across synthesized errors", async (t) => {
  let next;
  const worker = await fixture(t, { network: (event) => {
    assert.equal(new URL(event.url).pathname, "/api/v1/chat/completions");
    if (next === "network-error") {
      worker.send({ operation: "network.error", request_id: event.request_id, error: "upstream_unavailable", status: 502 });
      return null;
    }
    return next;
  } });
  next = { status: 429, headers: { "retry-after": ["37"], "x-private-token": ["synthetic-private"], "x-magpie-sign-in": ["expired"] }, body: "{}" };
  const limited = responseOf(await worker.call("fetch", request));
  assert.equal(limited.status, 429);
  assert.deepEqual(limited.headers["retry-after"], ["37"]);
  assert.equal(limited.headers["x-private-token"], undefined);
  assert.equal(limited.headers["x-magpie-sign-in"], undefined);
  assert.equal(limited.sign_in, undefined);

  next = { status: 200, body: JSON.stringify({ choices: [] }) };
  const success = responseOf(await worker.call("fetch", request));
  assert.equal(success.status, 200);
  assert.equal(success.sign_in, "kept");
  assert.equal(success.headers["retry-after"], undefined);
  next = "network-error";
  const failed = responseOf(await worker.call("fetch", request));
  assert.equal(failed.status, 502);
  assert.equal(failed.headers["retry-after"], undefined);
  next = { status: 429, body: "{}" };
  assert.equal(responseOf(await worker.call("fetch", request)).headers["retry-after"], undefined);
});

test("Cline treats exact model entitlement refusals as model unavailability", async (t) => {
  let next;
  const worker = await fixture(t, { network: () => next });
  for (const [upstreamStatus, error, status] of [
    [403, { code: "ENTITLEMENT_ERROR", message: "the user is not subscribed to required model plan" }, 404],
    [403, { code: "forbidden", message: "Forbidden" }, 403],
    [401, { code: "ENTITLEMENT_ERROR", message: "Unauthorized" }, 403],
    [401, { message: "Unauthorized" }, 401],
  ]) {
    next = { status: upstreamStatus, body: JSON.stringify({ error }) };
    const response = responseOf(await worker.call("fetch", request));
    assert.equal(response.status, status);
    assert.equal(response.headers["retry-after"], undefined);
  }
  next = { status: 403, body: JSON.stringify({ error: { code: "ENTITLEMENT_ERROR" } }) };
  assert.equal(responseOf(await worker.call("fetch", { ...request,
    auth: { type: "oauth", access: "synthetic-oauth", expires: Date.now() + 3600000 } })).status, 404);
  for (const body of ['{"error":{"code":"ENTITLEMENT_ERROR"}', JSON.stringify({ error: { code: "ENTITLEMENT_ERROR_SUFFIX" } })]) {
    next = { status: 403, body };
    assert.equal(responseOf(await worker.call("fetch", request)).status, 403);
  }
});

test("Cline discards a failed pinned attempt's retry metadata before retrying", async (t) => {
  let attempt = 0;
  let next;
  const worker = await fixture(t, { network: (event) => {
    if (++attempt === 1) return { status: 502, headers: { "retry-after": ["91"] }, body: "{}" };
    if (next === "network-error") {
      worker.send({ operation: "network.error", request_id: event.request_id, error: "upstream_unavailable", status: 502 });
      return null;
    }
    return next;
  } });
  for (const [response, status, retry] of [
    ["network-error", 502, undefined],
    [{ status: 503, headers: { "retry-after": ["19"] }, body: "{}" }, 503, ["19"]],
    [{ status: 200, body: JSON.stringify({ choices: [] }) }, 200, undefined],
    [{ status: 503, headers: { "retry-after": ["19"] }, body: JSON.stringify({ error: { code: "SPEND_LIMIT_EXCEEDED" } }) }, 429, undefined],
  ]) {
    attempt = 0; next = response;
    const result = responseOf(await worker.call("fetch", { ...request, model: "cline-pass/deepseek-v4.1-flash" }));
    assert.equal(attempt, 2);
    assert.equal(result.status, status);
    assert.deepEqual(result.headers["retry-after"], retry);
  }
});

test("Cline keeps retry observations isolated between overlapping inference calls", async (t) => {
  let held;
  let concurrent = false;
  const started = Promise.withResolvers();
  const worker = await fixture(t, { network: (event) => {
    if (!concurrent) return { status: 200, body: JSON.stringify({ choices: [] }) };
    if (event.id === "slow") {
      held = event.request_id;
      worker.send({ operation: "network.response", request_id: held, status: 429, headers: { "retry-after": ["17"] } });
      started.resolve();
      return null;
    }
    return { status: 503, headers: { "retry-after": ["41"] }, body: "{}" };
  } });
  await worker.call("fetch", request);
  concurrent = true;
  const slow = worker.call("fetch", { ...request, id: "slow" });
  await started.promise;
  const fast = responseOf(await worker.call("fetch", { ...request, id: "fast" }));
  worker.send({ operation: "network.chunk", request_id: held, data: Buffer.from("{}").toString("base64") });
  worker.send({ operation: "network.end", request_id: held });
  assert.deepEqual(fast.headers["retry-after"], ["41"]);
  assert.deepEqual(responseOf(await slow).headers["retry-after"], ["17"]);
});

test("Cline preserves trusted renewed state when restoring retry metadata", async (t) => {
  const worker = await fixture(t, { network: (event) => new URL(event.url).pathname === "/api/v1/auth/refresh"
    ? { status: 200, body: JSON.stringify({ accessToken: "synthetic-renewed", refreshToken: "synthetic-rotated", expiresAt: Date.now() + 3600000 }) }
    : { status: 429, headers: { "retry-after": ["23"], "x-magpie-sign-in": ["expired"] }, body: "{}" } });
  const response = responseOf(await worker.call("fetch", { ...request,
    auth: { type: "oauth", access: "synthetic-old", refresh: "synthetic-refresh", expires: 0 } }));
  assert.equal(worker.saves.length, 1);
  assert.equal(response.status, 429);
  assert.equal(response.sign_in, "renewed");
  assert.deepEqual(response.headers["retry-after"], ["23"]);
});
