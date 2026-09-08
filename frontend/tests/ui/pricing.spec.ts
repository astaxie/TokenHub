import type { Page } from "@playwright/test";
import { test, expect, capture } from "./harness";
import { fixedTime, model } from "./fixtures/shell";
import { emptyRates, type Card } from "../../features/admin/views/billing-pricing-fields";
import type { MockAPI } from "./network";

function pricingFixture(api: MockAPI) {
  api.respond("GET", "/api/admin/routing-rules", { data: [] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  let card: Card = { kind: "tenant", target: model.name, currency: "USD", source: "model", rates: { ...emptyRates(), input: "2", cache_read: "0.5", output: "6" }, periods: [] };
  let applied: object | undefined;
  api.define("GET", `/api/admin/billing/models/${model.name}/pricing`, () => ({ json: { card: structuredClone(card), fingerprint: "ui-price-basis" } }));
  api.define("POST", "/api/admin/billing/model-pricing/check", input => {
    expect(input.body).toEqual({ card: { ...card, rates: { ...card.rates, input: "3" } }, fingerprint: "ui-price-basis" });
    return { json: { current: structuredClone(card), proposed: (input.body as { card: Card }).card, fingerprint: "ui-price-basis", changed: true } };
  });
  api.define("POST", "/api/admin/billing/model-pricing/apply", input => {
    expect(input.body).toEqual({ card: { ...card, rates: { ...card.rates, input: "3" } }, fingerprint: "ui-price-basis", request_id: expect.any(String), confirmed: true, risk_acknowledged: true });
    const before = structuredClone(card); card = structuredClone((input.body as { card: Card }).card);
    applied = { id: "ui-price-change", model_name: model.name, actor_name: "ui-admin", effective_at: fixedTime, before, after: card, analysis_state: "not_performed", risk_acknowledged: true };
    return { json: applied };
  });
  api.define("GET", "/api/admin/billing/price-changes", () => ({ json: { data: applied ? [applied] : [] } }));
  return () => applied;
}
async function openPricing(page: Page) {
  await page.goto("/models");
  await expect(page.locator(".app-shell")).toBeVisible();
  await page.getByRole("group", { name: "发布状态" }).getByRole("button", { name: "全部", exact: true }).click();
  await page.getByRole("row").filter({ hasText: model.name }).getByRole("button", { name: "定价与收益", exact: true }).click();
  await expect(page.getByLabel("普通输入", { exact: true }).first()).toHaveValue("2");
  return page.locator(".model-pricing-workbench");
}

test("model prices require confirmation and persist within the UI fixture", async ({ page, api }, testInfo) => {
  const applied = pricingFixture(api);
  await openPricing(page);
  await page.getByLabel("普通输入", { exact: true }).first().fill("3");
  await page.getByRole("button", { name: "核对并保存", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "确认调整模型价格" });
  await expect(dialog).toContainText("未进行影响分析");
  await expect(dialog.getByRole("button", { name: "确认应用价格" })).toBeDisabled();
  expect(applied()).toBeUndefined();
  await capture(page, testInfo, dialog, "price-confirmation", "模型调价确认：固定样本，不验证金额算法");
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  expect(api.calls.filter(call => call.path.endsWith("/apply"))).toHaveLength(0);
  await page.getByRole("button", { name: "核对并保存", exact: true }).click();
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "确认应用价格" }).click();
  await expect(page.getByRole("status").filter({ hasText: "模型价格已更新" })).toBeVisible();
  await expect(page.getByLabel("普通输入", { exact: true }).first()).toHaveValue("3");
  await page.reload();
  await page.getByRole("group", { name: "发布状态" }).getByRole("button", { name: "全部", exact: true }).click();
  await page.getByRole("row").filter({ hasText: model.name }).getByRole("button", { name: "定价与收益", exact: true }).click();
  await expect(page.getByLabel("普通输入", { exact: true }).first()).toHaveValue("3");
  await page.locator("summary").filter({ hasText: "实际调价记录" }).click();
  const history = page.locator(".billing-history");
  await expect(history.locator(".billing-version")).toHaveCount(1);
  await capture(page, testInfo, history, "price-applied", "实际调价记录：刷新后读取同一变更");
  expect(api.calls.filter(call => call.path.endsWith("/apply"))).toHaveLength(1);
  expect(api.calls.filter(call => call.path.endsWith("/preview"))).toHaveLength(0);
});

test("model rule simulation rejects invalid token counts before a request", async ({ page, api }, testInfo) => {
  pricingFixture(api);
  await openPricing(page);
  await page.locator("summary").filter({ hasText: "高级：验证计价规则" }).click();
  const form = page.locator(".billing-rule-simulator");
  await form.getByLabel("总输入 Token", { exact: true }).fill("-1");
  await form.getByRole("button", { name: "试算费用" }).click();
  await expect(form.getByRole("alert")).toContainText("Token 数必须是非负安全整数");
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
  await capture(page, testInfo, form, "price-invalid-tokens", "计价表单：无效用量不会发送请求");
});
