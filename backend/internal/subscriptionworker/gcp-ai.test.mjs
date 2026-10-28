import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { createGCPProvider } from "./gcp-ai.mjs";
import { fixture } from "./worker-fixture.mjs";

const provider = "gcp-ai";
const project = "synthetic-project";
const model = "gemini-2.5-pro";
const json = (body, status = 200) => ({ status, body: JSON.stringify(body) });
const bodyOf = (request) => Buffer.from(request.body, "base64").toString();
const adc = { type: "authorized_user", client_id: "synthetic-client", client_secret: "synthetic-secret", refresh_token: "synthetic-refresh" };
const auth = () => ({ type: "oauth", project, location: "global", clientId: adc.client_id, clientSecret: adc.client_secret,
  access: "synthetic-access", refresh: adc.refresh_token, expires: Date.now() + 3600000 });
const token = (access = "synthetic-access", refresh = "synthetic-refresh") => json({ access_token: access, refresh_token: refresh, expires_in: 3600 });
const imported = () => ({ provider, method_id: "1", inputs: { project, adc: JSON.stringify(adc) } });

async function until(get, predicate) {
  for (let index = 0; index < 100; index++) {
    const value = await get();
    if (predicate(value)) return value;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.fail("The worker did not reach the expected state");
}

test("Google browser authorization validates callback state and exchanges its PKCE verifier", async (t) => {
  let consent;
  const worker = await fixture(t, { network: (request) => {
    if (request.url === "https://oauth2.googleapis.com/token") {
      const form = new URLSearchParams(bodyOf(request));
      assert.equal(form.get("grant_type"), "authorization_code");
      assert.equal(form.get("code"), "synthetic-code");
      assert.equal(form.get("redirect_uri"), consent.searchParams.get("redirect_uri"));
      assert.equal(createHash("sha256").update(form.get("code_verifier")).digest("base64url"), consent.searchParams.get("code_challenge"));
      assert.equal(form.get("client_secret"), adc.client_secret);
      return token();
    }
    if (request.url.endsWith("/oauth2/v2/userinfo")) return json({ email: "fixture@example.com", verified_email: true });
    assert.equal(request.url, `https://aiplatform.googleapis.com/v1/projects/${project}/locations/global`);
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-access"]);
    assert.equal(worker.saves.length, 0, "the project must be verified before saving the account");
    return json({ name: `projects/${project}/locations/global` });
  } });
  const started = await worker.call("auth.start", { provider, method_id: "0", inputs: { project, clientId: adc.client_id, clientSecret: adc.client_secret } });
  assert.equal(started.event, "result");
  consent = new URL(started.result.url);
  assert.equal(consent.origin, "https://accounts.google.com");
  assert.equal(consent.searchParams.get("code_challenge_method"), "S256");
  assert.equal(consent.searchParams.get("access_type"), "offline");
  const callback = new URL(consent.searchParams.get("redirect_uri"));
  assert.equal(callback.hostname, "127.0.0.1");
  callback.searchParams.set("code", "synthetic-code");
  callback.searchParams.set("state", "wrong-state");
  assert.equal((await worker.call("auth.callback", { provider, session_id: started.result.session_id, url: callback.href })).result.status, 400);
  assert.equal(worker.requests.length, 0);
  callback.searchParams.set("state", consent.searchParams.get("state"));
  assert.equal((await worker.call("auth.callback", { provider, session_id: started.result.session_id, url: callback.href })).result.status, 200);
  const status = await until(() => worker.call("auth.status", { provider, session_id: started.result.session_id }), (reply) => reply.result?.state !== "pending");
  assert.equal(status.result.state, "complete");
  assert.equal(worker.saves.length, 1);
  assert.equal(worker.saves[0].accountId, "fixture@example.com");
  assert.equal(worker.saves[0].project, project);
  assert.equal((await worker.call("auth.callback", { provider, session_id: started.result.session_id, url: callback.href })).error.code, "invalid_session");
});

test("authorized-user ADC is exchanged and verified without local account discovery", async (t) => {
  const worker = await fixture(t, { network: (request) => {
    if (request.url.endsWith("/token")) {
      const form = new URLSearchParams(bodyOf(request));
      assert.equal(form.get("grant_type"), "refresh_token");
      assert.equal(form.get("refresh_token"), adc.refresh_token);
      return token("synthetic-imported", "synthetic-rotated");
    }
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-imported"]);
    return request.url.endsWith("/userinfo") ? json({ error: "scope unavailable" }, 403) : json({ name: project });
  } });
  const result = await worker.call("auth.start", imported());
  assert.equal(result.event, "result");
  assert.equal(result.result.state, "complete");
  assert.equal(worker.saves.length, 1);
  assert.equal(worker.saves[0].type, "oauth");
  assert.equal(worker.saves[0].access, "synthetic-imported");
  assert.equal(worker.saves[0].refresh, "synthetic-rotated");
  assert.equal(worker.saves[0].location, "global");
  assert.equal(worker.saves[0].source, undefined);
  assert.equal(worker.saves[0].cloudsdkConfig, undefined);
});

test("ADC rejects service accounts, missing token fields and invalid project or location before network", async (t) => {
  const worker = await fixture(t);
  const invalid = [
    { adc: JSON.stringify({ type: "service_account", private_key: "synthetic-key" }) },
    { adc: JSON.stringify({ ...adc, refresh_token: undefined }) }, { adc: "not json" },
    { project: "../other-project" }, { project: "short" }, { project: "a".repeat(31) }, { project: "UPPERCASE" },
    { location: "global/../../other" }, { location: "a".repeat(65) },
  ];
  for (const fields of invalid) {
    const result = await worker.call("auth.start", { ...imported(), inputs: { ...imported().inputs, ...fields } });
    assert.equal(result.event, "error");
    assert.ok(["invalid_credentials", "invalid_request"].includes(result.error.code));
  }
  assert.equal(worker.requests.length, 0);
  assert.equal(worker.saves.length, 0);
});

test("ADC verification retains a 401 rotation and never saves an unverified session", async (t) => {
  let renewals = 0;
  let probes = 0;
  const worker = await fixture(t, { network: (request) => {
    if (request.url.endsWith("/token")) return token(`synthetic-access-${++renewals}`, `synthetic-refresh-${renewals}`);
    if (request.url.endsWith("/userinfo")) {
      assert.deepEqual(request.headers.authorization, ["Bearer synthetic-access-2"]);
      return json({});
    }
    probes++;
    assert.equal(worker.saves.length, 0);
    assert.deepEqual(request.headers.authorization, [`Bearer synthetic-access-${probes}`]);
    return json({}, probes === 1 ? 401 : 200);
  } });
  assert.equal((await worker.call("auth.start", imported())).result.state, "complete");
  assert.equal(worker.saves.length, 1);
  assert.equal(worker.saves[0].access, "synthetic-access-2");
  assert.equal(worker.saves[0].refresh, "synthetic-refresh-2");
});

test("Vertex signing is bound to the resource project and preserves tools and thought signatures", async (t) => {
  const body = { contents: [{ role: "model", parts: [{ functionCall: { name: "lookup", args: { key: "fixture" } }, thoughtSignature: "synthetic-signature" }] },
    { role: "user", parts: [{ functionResponse: { name: "lookup", response: { value: 7 } } }] }],
    tools: [{ functionDeclarations: [{ name: "lookup", parameters: { type: "OBJECT", properties: { key: { type: "STRING" } } } }] }],
    generationConfig: { thinkingConfig: { includeThoughts: true, thinkingBudget: 2048 } } };
  const worker = await fixture(t, { network: (request) => {
    assert.equal(request.url, `https://us-central1-aiplatform.googleapis.com/v1beta1/projects/${project}/locations/us-central1/publishers/google/models/${model}:streamGenerateContent?alt=sse`);
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-access"]);
    assert.deepEqual(request.headers["x-goog-user-project"], [project]);
    assert.equal(request.headers["x-goog-api-key"], undefined);
    assert.equal(request.headers.cookie, undefined);
    assert.deepEqual(JSON.parse(bodyOf(request)), body);
    return { status: 200, headers: { "content-type": ["text/event-stream"] }, chunks: [
      'data: {"candidates":[{"content":{"parts":[{"text":"Hello","thoughtSignature":"synthetic-reply-signature"}]}}]}\n\n',
    ] };
  } });
  const result = await worker.call("fetch", { provider, model, protocol: "gemini", auth: { ...auth(), location: "us-central1" }, body,
    headers: { authorization: ["Bearer caller-token"], "x-goog-user-project": ["other-project"], "x-goog-api-key": ["caller-key"], cookie: ["caller-cookie"] } });
  assert.equal(result.event, "end");
  assert.equal(result.events[0].status, 200);
  assert.match(Buffer.from(result.events.find((event) => event.event === "chunk").data, "base64").toString(), /synthetic-reply-signature/);
  assert.equal(worker.requests.length, 1);
});

test("the Vertex loader rejects other projects and non-model routes", async () => {
  const hooks = await createGCPProvider({ client: { auth: { set: async () => assert.fail("no credential update expected") } } });
  const loader = await hooks.auth.loader(async () => auth());
  for (const target of [
    `https://aiplatform.googleapis.com/v1beta1/projects/other-project/locations/global/publishers/google/models/${model}:generateContent`,
    `https://aiplatform.googleapis.com/v1beta1/projects/${project}/locations/global/publishers/google/models/${model}:delete`,
    `https://aiplatform.googleapis.com/v1beta1/publishers/google/models`,
    `https://attacker.example/v1beta1/projects/${project}/locations/global/publishers/google/models/${model}:generateContent`,
  ]) await assert.rejects(loader.fetch(target, { method: "POST", body: "{}" }), { code: "upstream_host_not_allowed" });
});

for (const rejectSave of [false, true]) {
  test(`Vertex 401 renewal ${rejectSave ? "stops when durable credential storage fails" : "is saved before retrying inference"}`, async (t) => {
    let replies = 0;
    const worker = await fixture(t, { rejectSave, network: (request) => {
      if (request.url.endsWith("/token")) return token("synthetic-new-access", "synthetic-new-refresh");
      replies++;
      if (replies === 1) return json({ error: "expired" }, 401);
      assert.equal(worker.saves.length, 1);
      assert.deepEqual(request.headers.authorization, ["Bearer synthetic-new-access"]);
      return json({ candidates: [] });
    } });
    const result = await worker.call("fetch", { provider, model, auth: auth(), stream: false, body: { contents: [] } });
    if (rejectSave) {
      assert.equal(result.error.code, "credential_save_failed");
      assert.equal(replies, 1);
      assert.equal(JSON.stringify(result).includes("synthetic-new-access"), false);
    } else {
      assert.equal(result.event, "end");
      assert.equal(replies, 2);
      assert.equal(worker.saves[0].refresh, "synthetic-new-refresh");
    }
  });
}

test("Vertex model discovery follows pagination and removes retired and non-chat models", async (t) => {
  const pages = [];
  const probes = [];
  const worker = await fixture(t, { network: (request) => {
    const url = new URL(request.url);
    if (url.pathname === "/v1beta1/publishers/google/models") {
      pages.push(url.searchParams.get("pageToken") || "");
      return pages.length === 1 ? json({ publisherModels: [{ name: "publishers/google/models/gemini-fixture" }, { name: "publishers/google/models/gemini-retired" },
        { name: "publishers/google/models/gemini-tts" }, { name: "publishers/google/models/claude-fixture" }], nextPageToken: "synthetic-next-page" }) :
        json({ publisherModels: [{ name: "publishers/google/models/gemini-second" }, { name: "publishers/google/models/gemini-fixture" }] });
    }
    assert.equal(url.origin, "https://aiplatform.googleapis.com");
    assert.ok(url.pathname.startsWith(`/v1beta1/projects/${project}/locations/global/publishers/google/models/`));
    const id = url.pathname.split("/").at(-1).split(":")[0];
    probes.push(id);
    return json({}, id === "gemini-retired" ? 404 : 200);
  } });
  const result = await worker.call("models", { provider, auth: auth() });
  assert.equal(result.event, "result");
  assert.equal(result.result.fallback, false);
  assert.deepEqual(pages, ["", "synthetic-next-page"]);
  assert.deepEqual(probes.sort(), ["gemini-fixture", "gemini-retired", "gemini-second"]);
  assert.deepEqual(result.result.models.map((entry) => entry.id), ["gemini-fixture", "gemini-second"]);
  assert.ok(result.result.models.every((entry) => entry.protocol === "gemini" && entry.api.url.includes(`/projects/${project}/`)));
});

test("Vertex discovery carries a durable 401 rotation into later pages and probes", async (t) => {
  let renewals = 0;
  let catalogs = 0;
  let probes = 0;
  const worker = await fixture(t, { network: (request) => {
    if (request.url.endsWith("/token")) {
      renewals++;
      assert.equal(new URLSearchParams(bodyOf(request)).get("refresh_token"), "synthetic-refresh");
      return token("synthetic-renewed-access", "synthetic-renewed-refresh");
    }
    const url = new URL(request.url);
    if (url.pathname === "/v1beta1/publishers/google/models" && ++catalogs === 1) {
      assert.deepEqual(request.headers.authorization, ["Bearer synthetic-access"]);
      return json({}, 401);
    }
    assert.equal(worker.saves.length, 1, "one durable rotation must precede subsequent catalog requests");
    assert.deepEqual(request.headers.authorization, ["Bearer synthetic-renewed-access"]);
    if (url.pathname === "/v1beta1/publishers/google/models") {
      return url.searchParams.get("pageToken") ? json({ publisherModels: [{ name: "publishers/google/models/gemini-second" }] }) :
        json({ publisherModels: [{ name: "publishers/google/models/gemini-first" }], nextPageToken: "page-two" });
    }
    assert.ok(url.pathname.endsWith(":countTokens"));
    probes++;
    return json({});
  } });
  const result = await worker.call("models", { provider, auth: auth() });
  assert.equal(result.event, "result");
  assert.deepEqual(result.result.models.map((entry) => entry.id), ["gemini-first", "gemini-second"]);
  assert.equal(catalogs, 3);
  assert.equal(probes, 2);
  assert.equal(renewals, 1);
  assert.equal(worker.saves.length, 1);
  assert.equal(worker.saves[0].refresh, "synthetic-renewed-refresh");
});

for (const operation of ["fetch", "models"]) {
  test(`Vertex cancellation aborts ${operation === "models" ? "model probes without returning a partial catalog" : "inference"}`, async (t) => {
    const worker = await fixture(t, { network: (request) => request.url.includes("/v1beta1/publishers/google/models?") ?
      json({ publisherModels: [{ name: "publishers/google/models/gemini-fixture" }] }) : null });
    const pending = worker.call(operation, { id: "cancel-gcp", provider, model, auth: auth(), body: { contents: [] } });
    const expected = operation === "models" ? 2 : 1;
    await until(async () => worker.requests.length, (count) => count >= expected);
    worker.send({ operation: "cancel", target_id: "cancel-gcp" });
    const result = await pending;
    assert.equal(result.error.code, "request_cancelled");
    assert.ok(worker.cancellations.includes(worker.requests.at(-1).request_id));
  });
}
