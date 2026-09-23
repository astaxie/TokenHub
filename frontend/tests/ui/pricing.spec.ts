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

test("billing exact pricing fields wrap instead of crowding one row", async ({ page }, testInfo) => {
  await openBilling(page);
  const trail = page.getByRole("navigation", { name: "当前位置" });
  await expect(trail.getByRole("button", { name: "TokenHub" })).toBeVisible();
  await expect(trail).toContainText("成本治理");
  await expect(trail.locator("[aria-current='page']")).toHaveText("成本账单");
  const form = section(page, "精确计价与影子核对");
  await form.scrollIntoViewIfNeeded();
  const input = form.getByLabel("普通输入", { exact: true });
  const box = await input.boundingBox();
  expect(box?.width ?? 0, "rate inputs must stay wide enough to type").toBeGreaterThan(140);
  const identityHeights = await form.locator(".form-grid").first().locator(":scope > label").evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().height));
  expect(identityHeights.length).toBeGreaterThan(0);
  expect(identityHeights.every((height) => height >= 48), "identity fields stack the label above the control").toBe(true);
  await capture(page, testInfo, form, "price-form-layout", "精确计价表单：字段分列而不是挤成一行");
  await page.locator(".page-breadcrumb").scrollIntoViewIfNeeded();
  await capture(page, testInfo, page.locator(".content-panel"), "price-form-page", "成本账单：面包屑与计价表单", "viewport");
  await form.getByRole("button", { name: "添加时段" }).click();
  const period = form.getByRole("group", { name: "时段覆盖" });
  await expect(period.getByRole("checkbox", { name: "周一" })).toBeChecked();
  await expect(period.getByRole("checkbox", { name: "周日" })).not.toBeChecked();
  await expect(period.locator("input:checked + span")).toHaveCount(5);
  await capture(page, testInfo, period, "price-form-period", "精确计价：时段覆盖与星期芯片");
});

test("billing exact pricing layout stays usable on a phone", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openBilling(page);
  const trail = page.getByRole("navigation", { name: "当前位置" });
  await expect(trail.getByRole("button", { name: "TokenHub" })).toBeVisible();
  await expect(trail.locator("[aria-current='page']")).toHaveText("成本账单");
  await expect.poll(() => page.locator(".page-header").evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  const form = section(page, "精确计价与影子核对");
  await form.getByRole("button", { name: "添加时段" }).click();
  const purpose = await form.getByLabel("价目用途").boundingBox();
  const target = await form.getByLabel("计价对象").boundingBox();
  expect(purpose && target, "identity fields render").toBeTruthy();
  expect(target!.y, "pricing fields stack into one column on a phone").toBeGreaterThan(purpose!.y + purpose!.height - 1);
  await expect(form.getByRole("checkbox", { name: "周一" })).toBeChecked();
  await expect.poll(() => form.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  await expect.poll(() => form.locator(".rate-card-actions").first().evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
  await page.locator(".page-header").evaluate((element) => element.scrollIntoView({ block: "start" }));
  await capture(page, testInfo, page.locator(".page-header"), "price-mobile-header", "手机：面包屑", "viewport");
  await form.evaluate((element) => element.scrollIntoView({ block: "start" }));
  await capture(page, testInfo, form, "price-mobile-form", "手机：精确计价表单", "viewport");
  await form.getByRole("group", { name: "时段覆盖" }).evaluate((element) => element.scrollIntoView({ block: "start" }));
  await capture(page, testInfo, form, "price-mobile-period", "手机：时段覆盖与星期芯片", "viewport");
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
