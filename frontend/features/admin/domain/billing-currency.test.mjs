import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";
const { validStatementExchangeRate, readStatementCurrencyPreferences } = await importTypeScript(new URL("./billing-currency.ts", import.meta.url));

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
