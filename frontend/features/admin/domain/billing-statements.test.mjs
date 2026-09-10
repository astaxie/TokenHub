import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";
const { formatStatementAmount, statementCSV, statementMonth } = await importTypeScript(new URL("./billing-statements.ts", import.meta.url));
const sample = () => ({ query: { side: "tenant", customer: "=bad()", project_ids: ["project"], from: "2020-01-01", to: "2020-02-01", timezone: "UTC", model: "", provider_id: "", resource_id: "" }, generated_at: "2020-02-03T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "request_admission", unknown_count: 0, incomplete_count: 0, totals: { "tenant:USD": "1.123456789012" }, estimated_margin_usd: null, rows: [{ id: "record", source: "tenant", amount: "1.123456789012", currency: "USD", units: {}, provider_id: "hidden_supplier", resource_id: "hidden_account", request_id: "hidden_attempt", model: "model,with comma" }] });
test("statement CSV preserves exact preview amounts and neutralizes formulas", () => {
  const csv = statementCSV(sample());
  assert.ok(csv.includes("'=bad()"));
  assert.ok(csv.includes('"1.123456789012"'));
  assert.ok(csv.includes('"model,with comma"'));
  assert.ok(csv.includes('"to_exclusive"'));
});
test("tenant exports omit upstream identities and margin", () => {
  const csv = statementCSV(sample());
  for (const value of ["hidden_supplier", "hidden_account", "hidden_attempt", "estimated_margin_usd"]) assert.equal(csv.includes(value), false);
});
test("provider exports retain source identity and unknown amounts", () => {
  const data = sample(); data.query.side = "provider"; data.rows[0].amount = null;
  assert.ok(statementCSV(data).includes("hidden_supplier"));
});
test("statement month uses the next month as exclusive end", () => {
  assert.deepEqual(statementMonth(new Date(2026, 11, 31)), { from: "2026-12-01", to: "2027-01-01" });
});

test("statement money formatting preserves decimal precision", () => {
 assert.equal(formatStatementAmount("123456789012345678.123456789012", "USD", "en-US"), "$123,456,789,012,345,678.123456789012");
 assert.equal(formatStatementAmount("-0.25", "USD", "en-US"), "-$0.25");
});
