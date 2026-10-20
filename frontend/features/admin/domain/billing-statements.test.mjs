import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";
const { formatStatementAmount, formatStatementQuantity, statementCSV, statementMonth } = await importTypeScript(new URL("./billing-statements.ts", import.meta.url));
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

test("statement native quantities retain exact decimals and locale separators", () => {
  assert.equal(formatStatementQuantity("123456789012345678.1234567890123456789", "en-US"), "123,456,789,012,345,678.1234567890123456789");
  assert.equal(formatStatementQuantity("12.50", "zh-CN"), "12.5");
  assert.equal(formatStatementQuantity("0", "ja-JP"), "0");
  assert.equal(formatStatementQuantity("1.25e1", "de-DE"), "12,5");
  assert.equal(formatStatementQuantity("1.25e-3", "en-US"), "0.00125");
  assert.equal(formatStatementQuantity("12.5e3", "en-US"), "12,500");
});

test("statement CSV preserves conversion snapshot metadata and unknown amounts", () => {
  const data = sample();
  data.query.side = "margin";
  data.conversion = { currency: "CNY", usd_exchange_rate: "7.250000000000" };
  data.display_totals = { tenant: "8.145061720337" };
  data.display_estimated_margin = null;
  data.rows[0].display_amount = "8.145061720337";
  data.rows.push({ id: "unknown", source: "tenant", amount: null, currency: "USD", display_amount: null });
  const csv = statementCSV(data);
  assert.ok(csv.includes('"display_currency","CNY"'));
  assert.ok(csv.includes('"display_exchange_rate_usd_to_currency","7.250000000000"'));
  assert.ok(csv.includes('"display_total","tenant","8.145061720337"'));
  assert.ok(csv.includes('"display_estimated_margin","unknown"'));
  assert.ok(csv.includes('"display_amount"'));
  assert.ok(csv.includes('"1.123456789012"'));
  assert.ok(csv.split("\r\n").find(line => line.startsWith('"unknown"')).endsWith(',""'));
});

test("statement money formatting retains fractional token costs in supported currencies", () => {
  for (const [currency, locale] of [["CNY", "zh-CN"], ["JPY", "ja-JP"], ["RUB", "ru-RU"]]) {
    assert.match(formatStatementAmount("0.123456789012", currency, locale), /123456789012/);
    assert.match(formatStatementAmount("0", currency, locale), /0/);
  }
});
