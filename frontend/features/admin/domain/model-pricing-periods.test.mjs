import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  modelPricingPeriodsInvalidPeriodError,
  modelPricingPeriodsObjectArrayError,
  parseModelPricingPeriods,
} = await importTypeScript(new URL("./model-pricing-periods.ts", import.meta.url));

test("pricing period parser accepts JSON object arrays", () => {
  const periods = [{
    timezone: "Asia/Shanghai",
    start_time: "00:00",
    end_time: "08:30",
    input_price_usd_per_1m: 0.14,
    output_price_usd_per_1m: 0.28,
  }];

  assert.deepEqual(parseModelPricingPeriods(JSON.stringify(periods)), periods);
});

test("pricing period parser rejects non-array JSON", () => {
  assert.throws(
    () => parseModelPricingPeriods("{\"timezone\":\"UTC\"}"),
    new RegExp(modelPricingPeriodsObjectArrayError),
  );
});

test("pricing period parser rejects invalid period fields", () => {
  for (const periods of [
    [{ timezone: "Mars/Olympus", start_time: "00:00", end_time: "01:00" }],
    [{ timezone: "UTC", start_time: "0:00", end_time: "01:00" }],
    [{ timezone: "UTC", start_time: "00:00" }],
    [{ effective_from: "tomorrow" }],
    [{ input_price_usd_per_1m: -1 }],
    [{ output_price_usd_per_1m: "0.4" }],
  ]) {
    assert.throws(
      () => parseModelPricingPeriods(JSON.stringify(periods)),
      new RegExp(modelPricingPeriodsInvalidPeriodError),
    );
  }
});
