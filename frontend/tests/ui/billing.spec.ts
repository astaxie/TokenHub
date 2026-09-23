import type { Locator } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { test, expect, section, openBilling, capture } from "./harness";
import { readStatementQuery, statementResponse } from "./fixtures/billing";
import { model, project } from "./fixtures/shell";
import { statements } from "./scenarios/billing";

async function customer(form: Locator) {
  await form.getByLabel("客户名称", { exact: true }).fill("UI Review Customer");
  await form.getByLabel("客户项目（可多选）").selectOption(project.id);
}
for (const scenario of statements) {
  test(`billing ${scenario.id}`, async ({ page, api }, testInfo) => {
    api.define("POST", "/api/admin/billing/statements", input => ({ json: statementResponse(readStatementQuery(input.body), scenario.state) }));
    await openBilling(page);
    const form = section(page, "费用对账单");
    await form.getByLabel("对账单类型").selectOption(scenario.side);
    if (scenario.side !== "provider") await customer(form);
    await form.getByRole("button", { name: "预览对账单", exact: true }).click();
    await expect(form.getByRole("button", { name: "导出当前预览 CSV", exact: true })).toBeVisible();
    const query = readStatementQuery(api.calls.find(call => call.method === "POST")?.body);
    expect(query).toEqual({
      side: scenario.side, from: "2026-09-01", to: "2026-10-01", timezone: "Asia/Shanghai",
      customer: scenario.side === "provider" ? "" : "UI Review Customer",
      project_ids: scenario.side === "provider" ? [] : [project.id],
      provider_id: "", resource_id: "", model: "",
    });
    if (scenario.state === "pending") {
      await expect(form.getByRole("status")).toContainText("未知金额: 1");
      await expect(form.getByText("US$0.00", { exact: true })).toBeVisible();
      await expect(form.getByText("未知", { exact: true })).toBeVisible();
    } else if (scenario.state === "complete") {
      await expect(form.getByText(model.name, { exact: true }).first()).toBeVisible();
      if (scenario.side === "margin") await expect(form.getByText(/预计毛利.*0\.48/)).toBeVisible();
    } else {
      await expect(form.locator("tbody tr")).toHaveCount(0);
    }
    await capture(page, testInfo, form, scenario.id, scenario.title);
    if (scenario.side === "tenant" && scenario.state === "complete") {
      const downloadEvent = page.waitForEvent("download");
      await form.getByRole("button", { name: "导出当前预览 CSV", exact: true }).click();
      const download = await downloadEvent;
      const csv = await readFile((await download.path())!, "utf8");
      expect(csv).toContain('"UI Review Customer"');
      expect(csv).toContain('"1.08"');
      expect(api.calls.filter(call => call.method === "POST")).toHaveLength(1);
      await form.getByLabel("开始日期", { exact: true }).fill("2026-08-01");
      await expect(form.getByRole("button", { name: "导出当前预览 CSV", exact: true })).toHaveCount(0);
    }
  });
}

test("billing request failure is actionable", async ({ page, api }, testInfo) => {
  api.define("POST", "/api/admin/billing/statements", () => ({ status: 503, json: { error: { code: "fixture_unavailable", message: "Statement service unavailable" } } }));
  await openBilling(page);
  const form = section(page, "费用对账单");
  await customer(form);
  await form.getByRole("button", { name: "预览对账单", exact: true }).click();
  await expect(form.getByRole("alert")).toContainText("Statement service unavailable");
  await expect(form.getByRole("button", { name: "预览对账单", exact: true })).toBeEnabled();
  await expect(form.getByRole("button", { name: "导出当前预览 CSV", exact: true })).toHaveCount(0);
  await capture(page, testInfo, form, "statement-error", "对账单失败与重试入口");
});

test("billing loading prevents duplicate submissions", async ({ page, api }, testInfo) => {
  let release!: () => void;
  const wait = new Promise<void>(resolve => { release = resolve; });
  api.define("POST", "/api/admin/billing/statements", async input => {
    await wait;
    return { json: statementResponse(readStatementQuery(input.body), "complete") };
  });
  try {
    await openBilling(page);
    const form = section(page, "费用对账单");
    await customer(form);
    await form.getByRole("button", { name: "预览对账单", exact: true }).click();
    await expect(form.getByRole("button", { name: "生成中", exact: true })).toBeDisabled();
    await expect(form.getByLabel("开始日期", { exact: true })).toBeDisabled();
    await capture(page, testInfo, form, "statement-loading", "加载中：阻止重复提交");
    release();
    await expect(form.getByRole("button", { name: "导出当前预览 CSV", exact: true })).toBeVisible();
    expect(api.calls.filter(call => call.method === "POST")).toHaveLength(1);
  } finally { release(); }
});

test("billing mobile retains statement actions", async ({ page, api }, testInfo) => {
  api.define("POST", "/api/admin/billing/statements", input => ({ json: statementResponse(readStatementQuery(input.body), "pending") }));
  await page.setViewportSize({ width: 390, height: 844 });
  await openBilling(page);
  const form = section(page, "费用对账单");
  await form.getByLabel("对账单类型").selectOption("provider");
  await form.getByRole("button", { name: "预览对账单", exact: true }).click();
  await expect(form.getByRole("status")).toBeVisible();
  await expect(form.getByRole("button", { name: "导出当前预览 CSV", exact: true })).toBeEnabled();
  await expect.poll(() => form.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
  await form.evaluate(element => element.scrollIntoView({ block: "start" }));
  await capture(page, testInfo, form, "statement-mobile-form", "手机：对账单表单", "viewport");
  await form.evaluate(element => element.scrollIntoView({ block: "end" }));
  await capture(page, testInfo, form, "statement-mobile-results", "手机：对账单状态与导出", "viewport");
});
