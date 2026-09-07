import { expect, it } from "vitest";
import { decimalAmount } from "./billing-pricing-fields";
it("formats exact decimals without floating point loss", () => {
  expect(decimalAmount("999999999999999999.123456789123")).toBe("999,999,999,999,999,999.123456789123");
  expect(decimalAmount("0.000000000001")).toBe("0.000000000001");
  expect(decimalAmount("0.000000000000")).toBe("0.00");
});
