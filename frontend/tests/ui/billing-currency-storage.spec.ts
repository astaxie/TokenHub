import { statementCurrencyStorageKey } from "../../features/admin/domain/billing-currency";
import { test, expect, section, openBilling, capture } from "./harness";

test("billing currency edits in separate tabs preserve other saved rates", async ({ page, context }, testInfo) => {
  await openBilling(page);
  const first = section(page, "费用对账单");
  await first.getByLabel("对账单币种").selectOption("CNY");
  await first.getByLabel("手动汇率：1 USD 兑目标币种").fill("7.1");
  await first.getByLabel("对账单币种").selectOption("JPY");
  await first.getByLabel("手动汇率：1 USD 兑目标币种").fill("150");
  await first.getByLabel("对账单币种").selectOption("CNY");
  const otherPage = await context.newPage();
  await openBilling(otherPage);
  const second = section(otherPage, "费用对账单");
  await expect(second.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveValue("7.1");
  await first.getByLabel("手动汇率：1 USD 兑目标币种").fill("7.3");
  await second.getByLabel("对账单币种").selectOption("JPY");
  const saved = () => page.evaluate(key => JSON.parse(localStorage.getItem(key)!), statementCurrencyStorageKey);
  expect((await saved()).rates.CNY).toBe("7.3");
  await second.getByLabel("手动汇率：1 USD 兑目标币种").fill("151");
  expect(await saved()).toEqual({ currency: "JPY", rates: { CNY: "7.3", JPY: "151" } });
  await page.reload();
  await first.getByLabel("对账单币种").selectOption("CNY");
  await expect(first.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveValue("7.3");
  await first.evaluate(element => element.scrollIntoView({ block: "start" }));
  await capture(page, testInfo, first, "currency-preserved-rate", "多窗口：保留其他窗口更新的汇率", "viewport");
});
