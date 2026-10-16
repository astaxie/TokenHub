import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import type { StatementResult } from "../features/admin/domain/billing-statements";
import e2eDefaults from "./config.cjs";

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity);
  await page.getByLabel("密码", { exact: true }).fill(process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword);
  await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible();
}

test("admin can convert and export a statement against the real backend", async ({ page }) => {
  await login(page);
  await page.goto("/billing");
  const statement = page.locator("section.section").filter({ has: page.getByRole("heading", { name: "费用对账单", exact: true }) });
  await expect(statement).toBeVisible();
  await statement.getByLabel("对账单类型").selectOption("provider");
  const currency = statement.getByLabel("对账单币种");
  const rate = statement.getByLabel("手动汇率：1 USD 兑目标币种", { exact: true });
  await currency.selectOption("CNY");
  await rate.fill("7.2");

  let statementRequests = 0;
  page.on("request", request => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/api/admin/billing/statements") statementRequests++;
  });
  const convertedResponse = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/admin/billing/statements");
  await statement.getByRole("button", { name: "预览对账单", exact: true }).click();
  const response = await convertedResponse;
  expect(response.status()).toBe(200);
  expect(response.request().postDataJSON()).toMatchObject({ side: "provider", display_currency: "CNY", display_exchange_rate: "7.2" });
  const preview = await response.json() as StatementResult;
  expect(preview.conversion).toEqual({ currency: "CNY", usd_exchange_rate: "7.200000000000" });
  await expect(currency).toHaveValue("CNY");
  await expect(rate).toHaveValue("7.2");
  await expect(statement.locator(".statement-conversion")).toContainText(/手动换算：1 USD = .*7\.20/);

  const exportButton = statement.getByRole("button", { name: "导出当前预览 CSV", exact: true });
  await expect(exportButton).toBeVisible();
  const downloaded = page.waitForEvent("download");
  await exportButton.click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toMatch(/^tokenhub-provider-/);
  const csv = await readFile((await download.path())!, "utf8");
  expect(csv).toContain('"display_currency","CNY"');
  expect(csv).toContain('"display_exchange_rate_usd_to_currency","7.200000000000"');
  expect(csv).toContain(`"generated_at","${preview.generated_at}"`);
  expect(statementRequests).toBe(1);

  await currency.selectOption("USD");
  await expect(exportButton).toHaveCount(0);
  await expect(statement.locator(".statement-conversion")).toHaveCount(0);
  await expect(rate).toHaveCount(0);
  const usdResponse = page.waitForResponse(result => result.request().method() === "POST" && new URL(result.url()).pathname === "/api/admin/billing/statements");
  await statement.getByRole("button", { name: "预览对账单", exact: true }).click();
  const usd = await usdResponse;
  expect(usd.status()).toBe(200);
  expect(usd.request().postDataJSON()).toMatchObject({ display_currency: "USD", display_exchange_rate: "1" });
  const usdPreview = await usd.json() as StatementResult;
  expect(usdPreview.conversion).toEqual({ currency: "USD", usd_exchange_rate: "1.000000000000" });
  await expect(currency).toHaveValue("USD");
  await expect(statement.locator(".statement-conversion")).toContainText(/手动换算：1 USD = .*1\.00/);
  await expect(exportButton).toBeVisible();
  expect(statementRequests).toBe(2);
});
