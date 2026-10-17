import { readFile } from "node:fs/promises";
import { test, expect, section, openBilling, capture } from "./harness";
import { readStatementQuery, statementResponse } from "./fixtures/billing";

const conversions = [
  { currency: "CNY", rate: "7.25", amount: "7.83", formatted: "¥7.83" },
  { currency: "JPY", rate: "150", amount: "162", formatted: "JP¥162.00" },
  { currency: "RUB", rate: "90", amount: "97.2", formatted: "RUB 97.20" },
];
for (const scenario of conversions) {
  test(`billing currency ${scenario.currency} preview and CSV preserve the conversion snapshot`, async ({ page, api }, testInfo) => {
    api.define("POST", "/api/admin/billing/statements", input => {
      const query = readStatementQuery(input.body);
      expect(query).toMatchObject({ side: "provider", display_currency: scenario.currency, display_exchange_rate: scenario.rate });
      const response = statementResponse(query, "pending");
      response.conversion = { currency: scenario.currency, usd_exchange_rate: scenario.rate };
      response.display_totals = { provider_estimate: scenario.amount };
      response.rows[0].display_amount = scenario.amount;
      response.rows[1].display_amount = "0";
      response.rows[2].display_amount = null;
      return { json: response };
    });
    await openBilling(page);
    const form = section(page, "费用对账单");
    await form.getByLabel("对账单类型").selectOption("provider");
    await form.getByLabel("对账单币种").selectOption(scenario.currency);
    await expect(form.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveValue("");
    await form.getByLabel("手动汇率：1 USD 兑目标币种").fill(scenario.rate);
    await form.getByRole("button", { name: "预览对账单", exact: true }).click();
    await expect(form.getByText(`换算金额：${scenario.formatted}`, { exact: true })).toBeVisible();
    await expect(form.getByText("换算金额：未知", { exact: true })).toBeVisible();
    await expect(form.getByText("US$1.08", { exact: true })).toBeVisible();
    await expect(form.getByRole("status")).toContainText("未知金额: 1");
    await form.evaluate(element => element.scrollIntoView({ block: "start" }));
    await capture(page, testInfo, form, `currency-${scenario.currency}-form`, `${scenario.currency}：币种与手动汇率`, "viewport");
    await form.locator(".statement-conversion").evaluate(element => element.scrollIntoView({ block: "start" }));
    await capture(page, testInfo, form, `currency-${scenario.currency}-results`, `${scenario.currency}：原币、换算金额与未知值`, "viewport");
    const downloaded = page.waitForEvent("download");
    await form.getByRole("button", { name: "导出当前预览 CSV", exact: true }).click();
    const csv = await readFile((await (await downloaded).path())!, "utf8");
    expect(csv).toContain(`"display_currency","${scenario.currency}"`);
    expect(csv).toContain(`"display_exchange_rate_usd_to_currency","${scenario.rate}"`);
    expect(csv).toContain(`"display_total","provider_estimate","${scenario.amount}"`);
    expect(csv).toContain('"display_amount"');
    expect(csv).toContain('"1.08"');
    expect(api.calls.filter(call => call.method === "POST")).toHaveLength(1);
    await form.getByLabel("手动汇率：1 USD 兑目标币种").fill("2");
    await expect(form.getByRole("button", { name: "导出当前预览 CSV", exact: true })).toHaveCount(0);
  });
}

test("billing currency rejects missing or invalid manual rates without a request", async ({ page, api }, testInfo) => {
  await openBilling(page);
  const form = section(page, "费用对账单");
  await form.getByLabel("对账单类型").selectOption("provider");
  await form.getByLabel("对账单币种").selectOption("CNY");
  for (const rate of ["", "0", "-7.25", "1e2"]) {
    await form.getByLabel("手动汇率：1 USD 兑目标币种").fill(rate);
    await form.getByRole("button", { name: "预览对账单", exact: true }).click();
    await expect(form.getByRole("alert")).toContainText("请输入大于 0 的手动汇率");
  }
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
  await capture(page, testInfo, form, "currency-invalid-rate", "换算：汇率缺失或无效时阻止生成");
});

test("billing currency remembers each manual rate and supports mobile controls", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openBilling(page);
  const form = section(page, "费用对账单");
  await form.getByLabel("对账单币种").selectOption("CNY");
  await form.getByLabel("手动汇率：1 USD 兑目标币种").fill("7.25");
  await form.getByLabel("对账单币种").selectOption("JPY");
  await expect(form.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveValue("");
  await form.getByLabel("手动汇率：1 USD 兑目标币种").fill("150");
  await page.reload();
  await expect(form.getByLabel("对账单币种")).toHaveValue("JPY");
  await expect(form.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveValue("150");
  await form.getByLabel("对账单币种").selectOption("CNY");
  await expect(form.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveValue("7.25");
  await expect.poll(() => form.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
  await form.getByLabel("对账单币种").evaluate(element => element.scrollIntoView({ block: "start" }));
  await capture(page, testInfo, form, "currency-mobile-preferences", "手机：恢复保存的币种与手动汇率", "viewport");
  await form.getByLabel("对账单币种").selectOption("USD");
  await expect(form.getByLabel("手动汇率：1 USD 兑目标币种")).toHaveCount(0);
});

test("billing currency long exact amounts remain readable on mobile", async ({ page, api }, testInfo) => {
  const rate = "999999999999999999.999999999999";
  const converted = "999999999999999999999999999998000000.000000000000";
  api.define("POST", "/api/admin/billing/statements", input => {
    const query = readStatementQuery(input.body);
    expect(query).toMatchObject({ side: "provider", display_currency: "CNY", display_exchange_rate: rate });
    const response = statementResponse(query, "complete");
    response.conversion = { currency: "CNY", usd_exchange_rate: rate };
    response.rows[0].amount = response.rows[0].usd = rate;
    response.rows[0].display_amount = converted;
    response.totals = { "provider_estimate:USD": rate };
    response.display_totals = { provider_estimate: converted };
    return { json: response };
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await openBilling(page);
  const form = section(page, "费用对账单");
  await form.getByLabel("对账单类型").selectOption("provider");
  await form.getByLabel("对账单币种").selectOption("CNY");
  await form.getByLabel("手动汇率：1 USD 兑目标币种").fill(rate);
  await form.getByRole("button", { name: "预览对账单", exact: true }).click();
  const conversion = form.locator(".statement-conversion");
  await expect(conversion).toContainText("999,999,999,999,999,999,999,999,999,998,000,000.00");
  await expect.poll(() => conversion.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
  await expect.poll(() => form.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
  await conversion.scrollIntoViewIfNeeded();
  await capture(page, testInfo, conversion, "currency-mobile-long-amounts", "手机：长金额与高精度汇率换行");
});
