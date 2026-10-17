import { readFile } from "node:fs/promises";
import { statementCurrencyStorageKey } from "../../features/admin/domain/billing-currency";
import { test, expect, section, capture } from "./harness";
import { readStatementQuery, statementResponse } from "./fixtures/billing";

test.use({ sessionLanguage: "ru", locale: "ru-RU", viewport: { width: 390, height: 844 } });

test("billing currency accepts Russian decimal input and preserves the exact export rate", async ({ page, api }, testInfo) => {
  const rate = "90.125000000001";
  api.define("POST", "/api/admin/billing/statements", input => {
    const query = readStatementQuery(input.body);
    expect(query).toMatchObject({ side: "provider", display_currency: "RUB", display_exchange_rate: rate });
    const response = statementResponse(query, "complete");
    response.conversion = { currency: "RUB", usd_exchange_rate: rate };
    response.rows[0].display_amount = "97.335000000001";
    response.display_totals = { provider_estimate: "97.335000000001" };
    return { json: response };
  });
  await page.goto("/billing");
  const form = section(page, "Акт сверки расходов");
  await form.getByLabel("Тип акта сверки").selectOption("provider");
  await form.getByLabel("Валюта выписки").selectOption("RUB");
  const rateInput = form.getByLabel("Ручной курс: единиц целевой валюты за 1 USD");
  await rateInput.fill("90,125000000001");
  await expect(rateInput).toHaveValue(rate);
  await form.getByRole("button", { name: "Предпросмотр акта сверки", exact: true }).click();
  const conversion = form.locator(".statement-conversion");
  await expect(conversion).toContainText("90,125000000001");
  await expect(conversion).toContainText("97,335000000001");
  await expect.poll(() => form.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
  await conversion.scrollIntoViewIfNeeded();
  await capture(page, testInfo, conversion, "currency-russian-decimal", "手机：俄语小数输入与精确换算");
  const downloaded = page.waitForEvent("download");
  await form.getByRole("button", { name: "Экспорт текущего предпросмотра в CSV", exact: true }).click();
  const csv = await readFile((await (await downloaded).path())!, "utf8");
  expect(csv).toContain(`"display_exchange_rate_usd_to_currency","${rate}"`);
  expect(csv).toContain('"display_total","provider_estimate","97.335000000001"');
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(1);
  expect(await page.evaluate(key => JSON.parse(localStorage.getItem(key)!), statementCurrencyStorageKey)).toEqual({ currency: "RUB", rates: { RUB: rate } });
  await page.reload();
  await expect(form.getByLabel("Валюта выписки")).toHaveValue("RUB");
  await expect(rateInput).toHaveValue(rate);
});
