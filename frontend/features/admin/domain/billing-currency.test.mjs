import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";
const { normalizeStatementExchangeRateInput, validStatementExchangeRate, readStatementCurrencyPreferences } = await importTypeScript(new URL("./billing-currency.ts", import.meta.url));

test("statement rate input normalizes Russian decimal commas without losing precision", () => {
  for (const [input, expected] of [
    ["90,125", "90.125"],
    ["999999999999999999,123456789012", "999999999999999999.123456789012"],
    ["0,000000000001", "0.000000000001"],
  ]) {
    const normalized = normalizeStatementExchangeRateInput(input, "ru-RU");
    assert.equal(normalized, expected);
    assert.equal(validStatementExchangeRate(normalized), true);
  }
});

test("statement rate input preserves canonical decimals in every supported locale", () => {
  for (const locale of ["en-US", "zh-CN", "ja-JP", "ru-RU"]) {
    for (const value of ["90.125", "999999999999999999.123456789012", "0.000000000001"]) {
      assert.equal(normalizeStatementExchangeRateInput(value, locale), value, locale);
    }
  }
});

test("statement rate input does not reinterpret grouping or repeated separators", () => {
  for (const locale of ["en-US", "zh-CN", "ja-JP"]) {
    for (const value of ["90,125", "1,234.5"]) {
      assert.equal(normalizeStatementExchangeRateInput(value, locale), value, locale);
      assert.equal(validStatementExchangeRate(value), false, locale);
    }
  }
  for (const [input, expected] of [
    ["1 234,5", "1 234.5"], ["1\u00a0234,5", "1\u00a0234.5"], ["1\u202f234,5", "1\u202f234.5"],
    ["1.234,5", "1.234.5"], ["90,,125", "90..125"], ["90,1,25", "90.1.25"],
    [" 90,125", " 90.125"], ["90,125 ", "90.125 "],
  ]) {
    const normalized = normalizeStatementExchangeRateInput(input, "ru-RU");
    assert.equal(normalized, expected);
    assert.equal(validStatementExchangeRate(normalized), false, input);
  }
});

test("statement rates accept positive exact decimals within backend precision", () => {
  for (const value of ["1", "7.25", "0.000000000001", "999999999999999999.123456789012"]) assert.equal(validStatementExchangeRate(value), true, value);
  for (const value of ["", "0", "0.000", "-1", "NaN", "1e3", "01", "1.", "0.0000000000001", "1000000000000000000", " 7.25"]) assert.equal(validStatementExchangeRate(value), false, value);
});

test("statement preferences preserve valid currency rates without inventing defaults", () => {
  assert.deepEqual(readStatementCurrencyPreferences(null), { currency: "USD", rates: {} });
  assert.deepEqual(readStatementCurrencyPreferences("invalid"), { currency: "USD", rates: {} });
  assert.deepEqual(readStatementCurrencyPreferences(JSON.stringify({ currency: "CNY", rates: { CNY: "7.25", JPY: "150", RUB: "0", USD: "7", EUR: "1" } })), { currency: "CNY", rates: { CNY: "7.25", JPY: "150" } });
  assert.deepEqual(readStatementCurrencyPreferences(JSON.stringify({ currency: "EUR", rates: {} })), { currency: "USD", rates: {} });
});
