import type { Model, ModelRoute, Provider, ProviderModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { shellResponses } from "./fixtures/shell";

const provider: Provider = { id: "provider_model_ui", name: "UI Model Provider", type: "mock", priority: 1, status: "active", healthy: true };
const inventory: ProviderModel = { id: "inventory_model_ui", provider_id: provider.id, upstream_model: "ui-upstream-chat", status: "active", call_supported: true };
const template: Model = { id: "template_model_ui", name: "ui-chat-template", category: "other", family: "test", modality: "chat", status: "active", context_window: 32000, input_price_usd_per_1m: 1, output_price_usd_per_1m: 2, capabilities: ["chat", "tools", "vision", "reasoning"], supported_parameters: ["temperature", "top_p", "max_tokens"], metadata: { source: "tokenhub-standard-catalog" } };
const published: Model = { ...template, id: "published_model_ui", name: "ui-published-model-with-a-long-external-name", metadata: { directory_role: "external", endpoints: "chat/completions,anthropic" } };
const disabled: Model = { ...published, id: "disabled_model_ui", name: "ui-disabled-model", status: "disabled" };
const route: ModelRoute = { id: "route_model_ui", model_name: published.name, provider_id: provider.id, provider_model: inventory.upstream_model, priority: 1, weight: 100, status: "active", strategy: "priority_weighted" };

for (const mobile of [false, true]) {
  test(`models compact-directory-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: [published, disabled, template], providers: [provider] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: [inventory] });
    api.respond("GET", "/api/admin/routing-rules", { data: [route, { ...route, id: "secondary_ui_route", provider_model: "ui-secondary-upstream", status: "disabled", project_scope: "include", project_ids: ["prj_ui"] }] });
    api.respond("GET", "/api/admin/provider-catalog", { data: [] });
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
    await table.getByRole("button", { name: `更多操作：${published.name}` }).click();
    const actions = page.getByRole("group", { name: `模型操作：${published.name}` });
    await expect(actions.getByRole("button", { name: "编辑", exact: true })).toHaveCount(0);
    await expect(actions.getByRole("button", { name: "下游费用对账单", exact: true })).toBeVisible();
    await capture(page, info, actions, `models-actions-${mobile ? "mobile" : "desktop"}`, "模型更多操作");
    await page.keyboard.press("Escape");
    await expect(actions).not.toBeVisible();
    await page.getByRole("button", { name: "已下线", exact: true }).click();
    await expect(table.getByText(disabled.name, { exact: true })).toBeVisible();
  });
}

test("models create-template-save-failure", async ({ page, api }, info) => {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: [template], providers: [provider] });
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [inventory] });
  api.respond("GET", "/api/admin/routing-rules", { data: [] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  api.define("POST", "/api/admin/models", input => {
    expect(input.body).toMatchObject({ name: "ui-reviewed-alias", family: template.family, modality: template.modality, context_window: template.context_window, capabilities: template.capabilities, routes: [{ provider_id: provider.id, provider_model: inventory.upstream_model, status: "active" }] });
    return { status: 422, json: { error: { message: "Synthetic model validation failure" } } };
  });
  await page.goto("/models");
  await page.getByRole("button", { name: "新建对外模型", exact: true }).click();
  const editor = page.locator(".model-create-modal");
  await editor.getByRole("button", { name: new RegExp(template.name) }).click();
  await editor.getByRole("button", { name: "下一步：选择 Provider 模型" }).click();
  await expect(editor.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "false");
  await editor.getByLabel("对外模型 ID", { exact: true }).fill("ui-reviewed-alias");
  await editor.getByRole("checkbox", { name: `${provider.name} / ${inventory.upstream_model}` }).check();
  await capture(page, info, editor, "models-create-template", "从模板创建模型：必填字段与定价", "viewport");
  await editor.getByRole("button", { name: "创建对外模型", exact: true }).click();
  await expect(editor.getByRole("alert")).toHaveText("Synthetic model validation failure");
  await expect(editor.getByRole("alert")).toBeFocused();
  await expect(editor.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "true");
  await expect(editor.getByLabel("对外模型 ID", { exact: true })).toHaveValue("ui-reviewed-alias");
  await capture(page, info, editor, "models-create-template-error", "创建模型失败：保留输入并展开设置", "viewport");
});
