import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  configuredPriceEntered,
  configuredPriceFormValue,
} = await importTypeScript(new URL("./configured-pricing.ts", import.meta.url));

test("configured price form value distinguishes blank fallback from explicit zero", () => {
  assert.equal(configuredPriceFormValue(0, false), "");
  assert.equal(configuredPriceFormValue(0, true), "0");
  assert.equal(configuredPriceFormValue(2.4, false), "2.4");
});

test("configured price entry treats whitespace as unconfigured", () => {
  assert.equal(configuredPriceEntered(""), false);
  assert.equal(configuredPriceEntered("  "), false);
  assert.equal(configuredPriceEntered("0"), true);
});
