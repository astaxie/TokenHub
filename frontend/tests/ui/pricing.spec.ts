import { test, expect, section, openBilling, capture } from "./harness";
import { fixedTime, model } from "./fixtures/shell";

test("billing price preview and shadow publication are separate actions", async ({ page, api }, testInfo) => {
  let saved: object | undefined;
  let previewedCard: unknown;
  api.define("POST", "/api/admin/billing/preview", input => {
    expect(input.query.size).toBe(0);
    expect(input.body).toMatchObject({ card: { kind: "tenant", target: model.name, currency: "USD", source: "ui-fixture", rates: { input: "2", cache_read: "0.5", output: "6" } }, usage: { prompt_tokens: 1000000, cached_input_tokens: 800000, completion_tokens: 10000 } });
    previewedCard = structuredClone((input.body as { card: unknown }).card);
    return { json: { snapshot: { period: "default" }, charge: { amount: "0.86", currency: "USD", usd: "0.86" } } };
  });
  api.define("POST", "/api/admin/billing/rate-cards", input => {
    expect(input.body).toEqual(previewedCard);
    saved = { ...(input.body as object), id: "ui-card-1", revision: 1, effective_from: fixedTime };
    return { json: { data: saved } };
  });
  api.define("GET", "/api/admin/billing/rate-cards", () => ({ json: { data: saved ? [saved] : [] } }));
  await openBilling(page);
  const form = section(page, "精确计价与影子核对");
  await form.getByLabel("计价对象").selectOption(model.name);
  await form.getByLabel("价格依据", { exact: true }).fill("ui-fixture");
  await form.getByLabel("普通输入", { exact: true }).fill("2");
  await form.getByLabel("缓存读取", { exact: true }).fill("0.5");
  await form.getByLabel("输出", { exact: true }).fill("6");
  await expect(form.getByRole("button", { name: "发布影子价目" })).toBeDisabled();
  await form.getByRole("button", { name: "预览费用" }).click();
  await expect(form.getByRole("status")).toContainText("US$0.86");
  expect(saved).toBeUndefined();
  await capture(page, testInfo, form, "price-preview", "计价预览：固定样本，不验证金额算法");
  await form.getByRole("button", { name: "发布影子价目" }).click();
  await expect(form.getByText(/影子价目已发布/)).toBeVisible();
  await form.getByLabel("普通输入", { exact: true }).fill("3");
  await expect(form.getByRole("button", { name: "发布影子价目" })).toBeDisabled();
  await page.reload();
  await expect(form.locator("li")).toHaveCount(0);
  await form.getByRole("button", { name: "读取已发布版本" }).click();
  await expect(form.locator("li")).toHaveCount(1);
  await expect(form.locator("li")).toContainText("ui-card-1");
  expect(saved).toMatchObject({ source: "ui-fixture", currency: "USD", rates: { input: "2", cache_read: "0.5", output: "6" } });
  await capture(page, testInfo, form, "price-published", "影子价目：刷新页面后读取同一版本");
  expect(api.calls.filter(call => call.method === "GET" && call.path.endsWith("/rate-cards"))).toHaveLength(1);
  expect(api.calls.filter(call => call.method === "POST" && call.path.endsWith("/rate-cards"))).toHaveLength(1);
});

test("billing invalid token counts fail before a request", async ({ page, api }, testInfo) => {
  await openBilling(page);
  const form = section(page, "精确计价与影子核对");
  await form.getByLabel("计价对象").selectOption(model.name);
  await form.getByLabel("价格依据", { exact: true }).fill("ui-fixture");
  await form.getByLabel("总输入 Token", { exact: true }).fill("-1");
  await form.getByRole("button", { name: "预览费用" }).click();
  await expect(form.getByRole("alert")).toContainText("Token 数必须是非负安全整数");
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
  await capture(page, testInfo, form, "price-invalid-tokens", "计价表单：无效用量不会发送请求");
});
