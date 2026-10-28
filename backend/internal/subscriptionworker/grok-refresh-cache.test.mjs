import assert from "node:assert/strict";
import test from "node:test";
import { createGrokProvider } from "./grok.mjs";

const tokenURL = "https://auth.x.ai/oauth2/token";
const original = { type: "oauth", access: "synthetic-old-access", refresh: "synthetic-refresh", expires: 1 };

test("Grok deduplicates pending and completed renewals of the same credential revision", async (t) => {
  const response = Promise.withResolvers();
  const fetch = t.mock.method(globalThis, "fetch", async (url) => {
    assert.equal(url, tokenURL);
    return response.promise;
  });
  const provider = await createGrokProvider({ client: {} });
  const first = provider.auth.refresh(original);
  const second = provider.auth.refresh(structuredClone(original));
  assert.equal(fetch.mock.callCount(), 1, "Concurrent stale callers must not spend a rotating token twice");
  response.resolve(Response.json({ access_token: "synthetic-new-access", refresh_token: "synthetic-new-refresh", expires_in: 3600 }));
  const [firstResult, secondResult] = await Promise.all([first, second]);
  assert.deepEqual(secondResult, firstResult);
  assert.deepEqual(await provider.auth.refresh(structuredClone(original)), firstResult);
  assert.equal(fetch.mock.callCount(), 1, "Later callers with the consumed revision must reuse its successful renewal");
});

test("Grok retries a credential revision after its renewal fails", async (t) => {
  let attempts = 0;
  t.mock.method(globalThis, "fetch", async (url) => {
    assert.equal(url, tokenURL);
    if (++attempts === 1) return Response.json({ error: "temporarily_unavailable" }, { status: 503 });
    return Response.json({ access_token: "synthetic-recovered-access", expires_in: 3600 });
  });
  const provider = await createGrokProvider({ client: {} });
  await assert.rejects(provider.auth.refresh(original), { signIn: "kept" });
  const result = await provider.auth.refresh(original);
  assert.equal(result.access, "synthetic-recovered-access");
  assert.equal(result.refresh, original.refresh);
  assert.equal(attempts, 2, "A transient failed promise must not poison future renewal attempts");
});

test("Grok does not coalesce different credential revisions sharing one refresh token", async (t) => {
  const responses = [Promise.withResolvers(), Promise.withResolvers()];
  let attempts = 0;
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, tokenURL);
    assert.equal(new URLSearchParams(init.body).get("refresh_token"), original.refresh);
    return responses[attempts++].promise;
  });
  const provider = await createGrokProvider({ client: {} });
  const first = provider.auth.refresh(original);
  const second = provider.auth.refresh({ ...original, access: "synthetic-another-access", expires: 2 });
  assert.equal(attempts, 2, "Each credential revision must have its own renewal operation");
  responses[1].resolve(Response.json({ access_token: "synthetic-second-result", expires_in: 3600 }));
  responses[0].resolve(Response.json({ access_token: "synthetic-first-result", expires_in: 3600 }));
  assert.equal((await first).access, "synthetic-first-result");
  assert.equal((await second).access, "synthetic-second-result");
});

test("Grok discovery renews its latest local credentials after a proactive renewal receives 401", async (t) => {
  const saves = [];
  let renewals = 0;
  const requestedAccess = [];
  t.mock.method(globalThis, "fetch", async (url, init) => {
    if (url === tokenURL) {
      const refresh = new URLSearchParams(init.body).get("refresh_token");
      assert.equal(refresh, renewals === 0 ? original.refresh : "synthetic-refresh-1");
      renewals++;
      return Response.json({ access_token: `synthetic-access-${renewals}`, refresh_token: `synthetic-refresh-${renewals}`, expires_in: 3600 });
    }
    assert.equal(url, "https://cli-chat-proxy.grok.com/v1/models");
    const access = new Headers(init.headers).get("authorization");
    requestedAccess.push(access);
    assert.equal(access, `Bearer ${saves.at(-1).access}`, "Discovery must use credentials after their successful save");
    if (requestedAccess.length === 1) return Response.json({}, { status: 401 });
    return Response.json({ data: [{ id: "grok-4.7", api_backend: "responses" }] });
  });
  const provider = await createGrokProvider({ client: { auth: { set: async ({ path, body }) => {
    assert.equal(path.id, "grok");
    saves.push(structuredClone(body));
  } } } });
  const models = await provider.provider.models({}, { auth: original });
  assert.ok(models["grok-4.7"]);
  assert.equal(renewals, 2);
  assert.deepEqual(requestedAccess, ["Bearer synthetic-access-1", "Bearer synthetic-access-2"]);
  assert.equal(saves.at(-1).refresh, "synthetic-refresh-2");
});
