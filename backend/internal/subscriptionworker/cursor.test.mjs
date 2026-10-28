import assert from "node:assert/strict";
import test from "node:test";
import { createCursorProvider } from "./cursor.mjs";
import { validateAuth } from "./validation.mjs";
import { fixture } from "./worker-fixture.mjs";

const provider = "cursor";
const token = (subject) => `eyJhbGciOiJIUzI1NiJ9.${Buffer.from(JSON.stringify({ sub: subject, exp: Math.floor(Date.now() / 1000) + 3600 })).toString("base64url")}.synthetic`;
const auth = (teamId, account = "fixture") => ({ type: "oauth", access: token(account), expires: Date.now() + 3600000, ...(teamId !== undefined ? { teamId } : {}) });
const json = (body) => ({ status: 200, body: JSON.stringify(body) });

test("Cursor team IDs must be explicit positive integers in imported resource credentials", () => {
  for (const teamId of ["42", 0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, null, {}, []]) {
    assert.throws(() => validateAuth(provider, auth(teamId)), { code: "invalid_credentials" });
  }
  assert.equal(validateAuth(provider, auth(42)).teamId, 42);
  assert.equal(validateAuth(provider, auth()).teamId, undefined);
});

test("Cursor usage and model discovery carry each resource's imported team", async (t) => {
  const seen = [];
  const tokens = new Map();
  const worker = await fixture(t, { network: (request) => {
    seen.push(request);
    const url = new URL(request.url);
    if (url.hostname.endsWith("cursor.com")) {
      assert.equal(request.headers["x-cursor-team-id"], undefined);
      return json({});
    }
    const expected = request.resource_id === "team-resource" ? ["42"] : undefined;
    assert.deepEqual(request.headers["x-cursor-team-id"], expected);
    assert.deepEqual(request.headers.authorization, [`Bearer ${tokens.get(request.resource_id)}`]);
    if (url.pathname.endsWith("/GetPlanInfo")) return json({ planInfo: { planName: "Teams" } });
    if (url.pathname.endsWith("/AvailableModels")) return json({ models: [{ name: "fixture-model", displayName: "Fixture", default: true }] });
    if (url.pathname.endsWith("/GetUsableModels")) return json({ models: [{ modelId: "fixture-model" }] });
    if (url.pathname.endsWith("/GetCurrentPeriodUsage") || url.pathname.endsWith("/GetHardLimit")) assert.deepEqual(JSON.parse(Buffer.from(request.body, "base64")), {});
    return json({ billingCycleEnd: String(Date.now() + 3600000), planUsage: { autoPercentUsed: 10, apiPercentUsed: 20, totalPercentUsed: 15 } });
  } });
  for (const resource_id of ["team-resource", "personal-resource", "team-resource"]) {
    const credentials = auth(resource_id === "team-resource" ? 42 : undefined, resource_id);
    tokens.set(resource_id, credentials.access);
    const result = await worker.call("usage", { provider, resource_id, auth: credentials });
    assert.equal(result.event, "result");
    assert.equal(result.result.plan, "Teams");
    const models = await worker.call("models", { provider, resource_id, auth: credentials });
    assert.equal(models.event, "result");
    assert.ok(models.result.models.some((entry) => entry.id === "fixture-model"));
  }
  assert.ok(seen.some((request) => request.url.endsWith("/AvailableModels")));
});

test("Cursor transport binds team metadata to the imported credential and declared operation", async () => {
  let credentials = auth(42);
  const hooks = await createCursorProvider({}, () => credentials);
  const apply = (url, tokenValue, operation = "fetch", body = "{}") => hooks["network.request"](new Request(url, {
    method: "POST", headers: { authorization: `Bearer ${tokenValue}`, "x-cursor-team-id": "caller-supplied", "content-type": "application/json" }, body,
  }), operation);
  const origin = "https://api2.cursor.sh";
  for (const path of ["/agent.v1.AgentService/RunSSE", "/agent.v1.AgentService/BidiAppend", "/aiserver.v1.DashboardService/GetHardLimit"]) {
    const request = await apply(origin + path, credentials.access);
    assert.equal(request.headers.get("x-cursor-team-id"), "42");
    assert.deepEqual(await request.json(), {});
  }
  const usage = await apply(origin + "/aiserver.v1.DashboardService/GetFilteredUsageEvents", credentials.access, "fetch", '{"page":1,"teamId":999}');
  assert.equal(usage.headers.get("x-cursor-team-id"), "42");
  assert.deepEqual(await usage.json(), { page: 1, teamId: 42 });
  for (const request of [await apply(origin + "/test", token("different-account")), await apply(origin + "/test", credentials.access, "auth.start"),
    await apply("https://cursor.com/api/dashboard/test", credentials.access)]) assert.equal(request.headers.get("x-cursor-team-id"), null);
  credentials = { type: "api", key: "synthetic-key", teamId: 43 };
  assert.equal((await apply(origin + "/agent.v1.AgentService/RunSSE", "synthetic-exchanged-token")).headers.get("x-cursor-team-id"), "43");
  credentials = auth();
  assert.equal((await apply(origin + "/test", credentials.access)).headers.get("x-cursor-team-id"), null);
});
