import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

const auth = { type: "oauth", access: "synthetic-grok-token", expires: Date.now() + 3600000 };

test("Grok incomplete or malformed allowance remains unknown", async (t) => {
  let payload;
  const worker = await fixture(t, { network: (request) => {
    assert.equal(request.url, "https://cli-chat-proxy.grok.com/v1/billing?format=credits");
    return { status: 200, body: JSON.stringify(payload) };
  } });
  for (const config of [undefined, {}, { creditUsagePercent: null }, { creditUsagePercent: "" },
    ...[" ", "invalid", "Infinity", -1, true, false, [], {}].map((creditUsagePercent) => ({ creditUsagePercent }))]) {
    payload = { config };
    const result = await worker.call("usage", { auth });
    assert.equal(result.event, "result");
    assert.equal(result.result.windows[0].used, undefined, "An incomplete allowance must not report zero usage");
  }
});

test("Grok preserves explicit unused and exhausted allowance with its reset", async (t) => {
  const resetsAt = "2026-11-01T00:00:00Z";
  let creditUsagePercent;
  const worker = await fixture(t, { network: () => ({ status: 200,
    body: JSON.stringify({ config: { creditUsagePercent, currentPeriod: { end: resetsAt } } }) }) });
  for (const value of [0, "0", 37.5, "37.5", 100, "100", 125]) {
    creditUsagePercent = value;
    const result = await worker.call("usage", { auth });
    assert.equal(result.event, "result");
    assert.deepEqual(result.result.windows, [{ name: "Allowance", used: Number(value), resetsAt }]);
  }
});
