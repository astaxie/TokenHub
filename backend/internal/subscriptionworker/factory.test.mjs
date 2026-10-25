import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

const auth = () => ({ type: "oauth", access: "synthetic-factory", activeOrganizationId: "fixture-org", expires: Date.now() + 3600000 });

test("Factory Standard-only refuses incomplete allowance without treating it as unused", async (t) => {
  const malformed = [{}, { fiveHour: {} }, { fiveHour: { usedPercent: null } }, { fiveHour: { usedPercent: "0" } },
    { fiveHour: { usedPercent: -1 } }, { fiveHour: { usedPercent: 20 }, monthly: {} }];
  let standard;
  const worker = await fixture(t, { network: (request) => {
    assert.ok(request.url.endsWith("/api/billing/limits"));
    return { status: 200, body: JSON.stringify({ limits: { standard }, extraUsageAllowed: true, extraUsageBalanceCents: 10000 }) };
  } });
  for (const value of malformed) {
    standard = value;
    const result = await worker.call("usage", { provider: "factory", auth: auth(), options: { standard_only: true } });
    assert.equal(result.event, "result");
    assert.match(result.result.error, /Standard allowance could not be verified/);
    assert.equal(result.result.windows, undefined);
  }
  standard = { fiveHour: { usedPercent: 0 }, weekly: { usedPercent: 100 } };
  const valid = await worker.call("usage", { provider: "factory", auth: auth(), options: { standard_only: true } });
  assert.deepEqual(valid.result.windows.slice(0, 2).map((window) => window.used), [0, 100]);
});

test("Factory allowance display keeps upstream behavior when Standard-only is disabled", async (t) => {
  const worker = await fixture(t, { network: () => ({ status: 200, body: JSON.stringify({ limits: { standard: { fiveHour: {} } } }) }) });
  const result = await worker.call("usage", { provider: "factory", auth: auth() });
  assert.equal(result.result.windows[0].used, 0);
});

test("Factory Standard-only validates allowance after a same-origin redirect", async (t) => {
  let standard = { fiveHour: {} };
  const worker = await fixture(t, { network: (request) => {
    const url = new URL(request.url);
    if (url.pathname === "/api/billing/limits") {
      return { status: 302, headers: { location: ["/api/billing/limits/"] } };
    }
    assert.equal(url.pathname, "/api/billing/limits/");
    assert.equal(url.origin, new URL(worker.requests[0].url).origin);
    return { status: 200, body: JSON.stringify({ limits: { standard }, extraUsageAllowed: true, extraUsageBalanceCents: 10000 }) };
  } });
  const invalid = await worker.call("usage", { provider: "factory", auth: auth(), options: { standard_only: true } });
  assert.match(invalid.result.error, /Standard allowance could not be verified/);
  assert.equal(invalid.result.windows, undefined);
  standard = { fiveHour: { usedPercent: 0 } };
  const valid = await worker.call("usage", { provider: "factory", auth: auth(), options: { standard_only: true } });
  assert.equal(valid.result.windows[0].used, 0);
  assert.equal(worker.requests.length, 4);
});
