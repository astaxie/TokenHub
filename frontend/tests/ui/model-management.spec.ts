import type { Model, ModelRoute, Provider, ProviderModel } from "../../features/admin/core/types";
import type { Locator } from "@playwright/test";
import { test, expect, capture } from "./harness";
import { shellResponses } from "./fixtures/shell";
import type { MockAPI } from "./network";

const provider: Provider = { id: "provider_model_ui", name: "UI Model Provider", type: "mock", priority: 1, status: "active", healthy: true };
const inventory: ProviderModel = { id: "inventory_model_ui", provider_id: provider.id, upstream_model: "ui-upstream-chat", status: "active", call_supported: true };
const template: Model = { id: "template_model_ui", name: "ui-chat-template", category: "other", family: "test", modality: "chat", status: "active", context_window: 32000, input_price_usd_per_1m: 1, output_price_usd_per_1m: 2, capabilities: ["chat", "tools", "vision", "reasoning"], supported_parameters: ["temperature", "top_p", "max_tokens"], metadata: { source: "tokenhub-standard-catalog" } };
const published: Model = { ...template, id: "published_model_ui", name: "ui-published-model-with-a-long-external-name", metadata: { directory_role: "external", endpoints: "chat/completions,anthropic" } };
const disabled: Model = { ...published, id: "disabled_model_ui", name: "ui-disabled-model", status: "disabled" };
const route: ModelRoute = { id: "route_model_ui", model_name: published.name, provider_id: provider.id, provider_model: inventory.upstream_model, priority: 1, weight: 100, status: "active", strategy: "priority_weighted" };

function installDirectory(api: MockAPI) {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: [published, disabled, template], providers: [provider] });
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [inventory] });
  api.respond("GET", "/api/admin/routing-rules", { data: [route, { ...route, id: "secondary_ui_route", provider_model: "ui-secondary-upstream", status: "disabled", project_scope: "include", project_ids: ["prj_ui"] }] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
}

for (const mobile of [false, true]) {
  test(`models compact-directory-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installDirectory(api);
    await page.goto("/models");
    const table = page.locator(".model-directory-table");
    await expect(table.getByRole("columnheader")).toHaveCount(6);
    await expect(table.getByText(disabled.name, { exact: true })).toHaveCount(0);
    await expect(table.getByText("已发布", { exact: true })).toBeVisible();
    await expect(table.getByText("正常", { exact: true })).toBeVisible();
    await expect(table.getByRole("button", { name: "编辑", exact: true })).toBeVisible();
    expect(await table.locator(".model-detail-trigger").evaluateAll(buttons => buttons.every(button => {
      const text = document.createRange();
      text.selectNodeContents(button);
      const label = button.nextElementSibling;
      return !label || text.getBoundingClientRect().bottom <= label.getBoundingClientRect().top + 1;
    })), "Wrapped model names must not overlap their alias labels").toBe(true);
    await expect(page.locator(".model-directory .model-governance-flow")).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await capture(page, info, page.locator(".model-directory"), `models-directory-${mobile ? "mobile" : "desktop"}`, "模型目录：统一名称、上游、状态与价格", "viewport");
    const detailTrigger = table.getByRole("button", { name: `查看模型详情：${published.name}` });
    await detailTrigger.click();
    const detail = page.getByRole("dialog", { name: "模型详情" });
    await expect(detail).toBeFocused();
    for (const value of ["vision", "reasoning", "chat/completions", "anthropic", "top_p", "max_tokens"]) await expect(detail.getByText(value, { exact: true })).toBeVisible();
    await detail.getByRole("heading", { name: "支持参数", exact: true }).scrollIntoViewIfNeeded();
    await capture(page, info, detail, `models-details-capabilities-${mobile ? "mobile" : "desktop"}`, "完整模型能力、协议与参数", "viewport");
    const mappings = detail.getByRole("region", { name: "全部上游映射" });
    await mappings.getByText("ui-secondary-upstream", { exact: true }).scrollIntoViewIfNeeded();
    await expect(mappings.getByText("ui-upstream-chat", { exact: true })).toBeVisible();
    await expect(mappings.getByText("ui-secondary-upstream", { exact: true })).toBeVisible();
    await capture(page, info, detail, `models-details-mappings-${mobile ? "mobile" : "desktop"}`, "完整上游映射与项目作用域", "viewport");
    await page.keyboard.press("Escape");
    await expect(detail).toHaveCount(0);
    await expect(detailTrigger).toBeFocused();
    const rowActions = table.locator(".model-management-actions");
    const inlineStatement = rowActions.getByRole("button", { name: "下游费用对账单", exact: true });
    const more = table.getByRole("button", { name: `更多操作：${published.name}` });
    const actions = page.getByRole("group", { name: `模型操作：${published.name}` });
    const statementInline = await inlineStatement.isVisible();
    if (!statementInline) await more.click();
    const statementAction = statementInline ? inlineStatement : actions.getByRole("button", { name: "下游费用对账单", exact: true });
    await expect(statementAction).toBeVisible();
    await capture(page, info, statementInline ? rowActions : actions, `models-actions-${mobile ? "mobile" : "desktop"}`, "模型操作按可用空间显示");
    await statementAction.click();
    const statement = page.getByRole("dialog", { name: "费用对账单", exact: true });
    const customer = statement.getByLabel("客户名称", { exact: true });
    await customer.click();
    await customer.fill("UI Statement Customer");
    await statement.getByRole("button", { name: "关闭", exact: true }).click();
    await expect(statementInline ? inlineStatement : more).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(actions).not.toBeVisible();
    await page.getByRole("button", { name: "已下线", exact: true }).click();
    await expect(table.getByText(disabled.name, { exact: true })).toBeVisible();
  });
}

async function expectActionsFit(actions: Locator) {
  await expect.poll(() => actions.evaluate(element => {
    const container = element.getBoundingClientRect();
    const bounds = Array.from(element.querySelectorAll<HTMLElement>(":scope > button")).map(button => button.getBoundingClientRect());
    return {
      contained: bounds.every(rect => rect.width > 0 && rect.left >= container.left - 1 && rect.right <= container.right + 1),
      oneRow: bounds.every(rect => Math.abs((rect.top + rect.bottom) / 2 - (bounds[0].top + bounds[0].bottom) / 2) <= 1),
      separated: bounds.every((rect, index) => index === 0 || rect.left >= bounds[index - 1].right),
    };
  }), { message: "Visible model actions must fit on one row without overlap" }).toEqual({ contained: true, oneRow: true, separated: true });
}

async function expectMenuReachable(menu: Locator) {
  await expect.poll(() => menu.evaluate(element => {
    const bounds = element.getBoundingClientRect();
    const finalButton = element.querySelector<HTMLButtonElement>("button:last-child");
    const button = finalButton?.getBoundingClientRect();
    const hit = button ? document.elementFromPoint(button.left + button.width / 2, button.top + button.height / 2) : null;
    return {
      withinViewport: bounds.top >= 0 && bounds.left >= 0 && bounds.bottom <= innerHeight && bounds.right <= innerWidth,
      finalActionReachable: Boolean(finalButton && hit && (hit === finalButton || finalButton.contains(hit))),
    };
  }), { message: "All overflow actions must remain visible and reachable" }).toEqual({ withinViewport: true, finalActionReachable: true });
}

for (const locale of [
  { language: "zh-CN", option: "简体中文", labels: ["编辑", "路由策略", "下游费用对账单", "下线", "删除"], customer: "客户名称", close: "关闭" },
  { language: "en", option: "English", labels: ["Edit", "Routing Policies", "Customer charge statement", "Unpublish", "Delete"], customer: "Customer name", close: "Close" },
  { language: "ja", option: "日本語", labels: ["編集", "ルーティングポリシー", "顧客料金明細書", "非公開", "削除"], customer: "顧客名", close: "閉じる" },
]) {
  test(`models responsive-directory-actions ${locale.language}`, async ({ page, api }, info) => {
    await page.setViewportSize({ width: 2200, height: 1000 });
    installDirectory(api);
    await page.goto("/models");
    const directory = page.locator(".model-directory");
    const row = directory.getByRole("row").filter({ hasText: published.name });
    const actions = row.locator(".model-management-actions");
    const inline = actions.locator(':scope > button:not([data-action-id="more"])');
    const more = actions.locator('[data-action-id="more"]');
    const menu = page.locator(".model-management-menu");
    const reads = () => api.calls.filter(call => ["/api/admin/overview", "/api/admin/provider-models", "/api/admin/routing-rules"].includes(call.path)).length;
    await expect(inline).toHaveCount(5);
    const initialReads = reads();
    await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
    await page.getByRole("option", { name: locale.option, exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    await expect(inline).toHaveText(locale.labels);
    await expect(more).toHaveCount(0);
    await expectActionsFit(actions);
    await capture(page, info, directory, `models-actions-wide-${locale.language}`, "宽屏直接展示全部模型操作", "viewport");

    await actions.locator('[data-action-id="statement"]').click();
    const statement = page.locator("dialog.statement-drawer");
    await statement.getByLabel(locale.customer, { exact: true }).fill("UI Persistent Statement Customer");
    await page.setViewportSize({ width: 1024, height: 1000 });
    await expect(statement.getByLabel(locale.customer, { exact: true })).toHaveValue("UI Persistent Statement Customer");
    await expect(more).toBeVisible();
    await statement.getByRole("button", { name: locale.close, exact: true }).click();
    await expect(more).toBeFocused();

    for (const viewport of [{ name: "narrow", width: 1024, height: 1000 }, { name: "mobile", width: 390, height: 844 }]) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await expect.poll(() => inline.count()).toBeLessThan(5);
      await more.scrollIntoViewIfNeeded();
      await expectActionsFit(actions);
      expect(await row.locator(".directory-model-name").evaluate(element => {
        const cell = element.closest("td")!.getBoundingClientRect();
        return Array.from(element.querySelectorAll("button, div > span")).every(item => item.getBoundingClientRect().right <= cell.right - 9);
      }), "Model names and alias labels must stay inside the model column").toBe(true);
      await expect(menu).toBeHidden();
      const before = await row.boundingBox();
      await more.click();
      await expect(menu).toBeVisible();
      await expect(menu).toHaveCSS("position", "fixed");
      const visibleCount = await inline.count();
      await expect(inline).toHaveText(locale.labels.slice(0, visibleCount));
      await expect(menu.getByRole("button")).toHaveText(locale.labels.slice(visibleCount));
      await expectMenuReachable(menu);
      const after = await row.boundingBox();
      expect(Math.abs(after!.height - before!.height), "Opening More must not change the model row height").toBeLessThanOrEqual(1);
      await capture(page, info, directory, `models-actions-${viewport.name}-${locale.language}`, "根据实际可用宽度收纳模型操作", "viewport");
      await page.keyboard.press("Escape");
      await expect(menu).toBeHidden();
      await expect(more).toBeFocused();
    }

    await more.click();
    await menu.locator('[data-action-id="statement"]').click();
    await statement.getByLabel(locale.customer, { exact: true }).fill("UI Widened Statement Customer");
    await page.setViewportSize({ width: 2200, height: 1000 });
    await expect(statement.getByLabel(locale.customer, { exact: true })).toHaveValue("UI Widened Statement Customer");
    await statement.getByRole("button", { name: locale.close, exact: true }).click();
    await expect(actions.locator('[data-action-id="statement"]')).toBeFocused();
    await expect(inline).toHaveText(locale.labels);
    await expect(more).toHaveCount(0);
    await expectActionsFit(actions);
    expect(reads(), "Resizing and changing language must not reload model data").toBe(initialReads);
  });
}

test("models create-template-save-failure", async ({ page, api }, info) => {
  const pricedTemplate: Model = { ...template, cache_write_price_usd_per_1m: 0, cache_write_5m_price_usd_per_1m: 0, cache_write_1h_price_usd_per_1m: 0, pricing_periods: [{ timezone: "UTC", start_time: "00:00", end_time: "08:00", input_price_usd_per_1m: 0.5 }] };
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: [pricedTemplate], providers: [provider] });
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [inventory] });
  api.respond("GET", "/api/admin/routing-rules", { data: [] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  api.define("POST", "/api/admin/models", input => {
    expect(input.body).toMatchObject({ name: "ui-reviewed-alias", family: template.family, modality: template.modality, context_window: template.context_window, capabilities: template.capabilities, routes: [{ provider_id: provider.id, provider_model: inventory.upstream_model, status: "active" }] });
    expect(input.body).toMatchObject({ cache_write_price_usd_per_1m: 0, cache_write_price_configured: true, cache_write_5m_price_usd_per_1m: 0, cache_write_5m_price_configured: true, cache_write_1h_price_usd_per_1m: 0, cache_write_1h_price_configured: true, pricing_periods: pricedTemplate.pricing_periods });
    return { status: 422, json: { error: { message: "Synthetic model validation failure" } } };
  });
  await page.goto("/models");
  await page.getByRole("button", { name: "新建对外模型", exact: true }).click();
  const editor = page.locator(".model-create-modal");
  await editor.getByRole("button", { name: new RegExp(template.name) }).click();
  await editor.getByRole("button", { name: "下一步：选择 Provider 模型" }).click();
  await expect(editor.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "false");
  for (const label of [/^对外缓存写价 USD\/1M/, /^对外 5 分钟缓存写价 USD\/1M/, /^对外 1 小时缓存写价 USD\/1M/, /^分时价格配置（JSON）/]) await expect(editor.getByLabel(label)).not.toBeVisible();
  await editor.getByLabel("对外模型 ID", { exact: true }).fill("ui-reviewed-alias");
  await editor.getByRole("checkbox", { name: `${provider.name} / ${inventory.upstream_model}` }).check();
  await capture(page, info, editor, "models-create-template", "从模板创建模型：必填字段与定价", "viewport");
  await editor.getByRole("button", { name: "创建对外模型", exact: true }).click();
  await expect(editor.getByRole("alert")).toHaveText("Synthetic model validation failure");
  await expect(editor.getByRole("alert")).toBeFocused();
  await expect(editor.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "true");
  await expect(editor.getByLabel("对外模型 ID", { exact: true })).toHaveValue("ui-reviewed-alias");
  await capture(page, info, editor, "models-create-template-error", "创建模型失败：保留输入并展开设置", "viewport");
  const pricingPeriods = editor.getByLabel(/^分时价格配置（JSON）/);
  await pricingPeriods.scrollIntoViewIfNeeded();
  await expect(pricingPeriods).toHaveValue(JSON.stringify(pricedTemplate.pricing_periods, null, 2));
  for (const label of [/^对外缓存写价 USD\/1M/, /^对外 5 分钟缓存写价 USD\/1M/, /^对外 1 小时缓存写价 USD\/1M/]) await expect(editor.getByLabel(label)).toHaveValue("0");
  await capture(page, info, editor, "models-create-advanced-pricing", "高级模型设置：缓存写入费用与分时价格", "viewport");
});
