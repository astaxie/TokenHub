import type { Model, ModelRoute, Provider, ProviderModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { model, project, shellResponses } from "./fixtures/shell";
import type { MockAPI } from "./network";

const unmapped: Model = { ...model, id: "mdl_ui_unmapped", name: "ui-unmapped-model" };
const provider: Provider = { id: "prv_ui_route", name: "UI Route Provider", type: "mock", priority: 1, healthy: true, status: "active" };
const route: ModelRoute = { id: "route_ui_edit", model_name: model.name, provider_id: provider.id, provider_model: "ui-upstream", priority: 1, weight: 100, quality_score: 50, cost_score: 50, strategy: "priority_weighted", status: "active", project_scope: "include", project_ids: [project.id], tags: ["retained"], sticky_session: true };
const providerModel: ProviderModel = { id: "pm_ui_route", provider_id: provider.id, upstream_model: route.provider_model, status: "active" };

function installRouteFixtures(api: MockAPI) {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, providers: [provider], models: [model, unmapped] });
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [providerModel] });
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/routing-rules", { data: [route] });
  for (const path of ["provider-resources", "plugins", "provider-adapters", "plugin-ui-manifest", "plugin-actions", "plugin-background-jobs", "provider-catalog"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
}

test("route-management nested-editor-focus-and-save-failure", async ({ page, api }, testInfo) => {
  installRouteFixtures(api);
  api.define("PATCH", `/api/admin/routing-rules/${route.id}`, input => {
    expect(input.body).toMatchObject({ weight: 25, project_scope: "include", project_ids: [project.id], tags: ["retained"], sticky_session: true });
    return { status: 500, json: { error: { message: "Synthetic route save failed" } } };
  });
  await page.goto("/routes");
  await page.getByRole("button", { name: "配置", exact: true }).click();
  const parent = page.getByRole("dialog", { name: "配置模型路由", exact: true });
  const edit = parent.getByRole("button", { name: "编辑", exact: true });
  await edit.focus();
  await page.keyboard.press("Enter");
  const child = page.getByRole("dialog", { name: "路由策略", exact: true });
  await expect(child).toBeFocused();
  await child.getByRole("button", { name: "保存", exact: true }).focus();
  await page.keyboard.press("Tab");
  await expect(child.getByTitle("关闭", { exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(child).toHaveCount(0);
  await expect(parent).toBeVisible();
  await expect(edit).toBeFocused();
  await page.keyboard.press("Enter");
  await child.getByRole("spinbutton", { name: /^流量权重/ }).fill("25");
  await child.getByRole("button", { name: "保存", exact: true }).click();
  await expect(child.getByRole("alert")).toHaveText("Synthetic route save failed");
  await expect(child.getByRole("spinbutton", { name: /^流量权重/ })).toHaveValue("25");
  await capture(page, testInfo, child, "route-management-child-error", "线路编辑：保存失败保留参数并在当前弹窗显示错误", "viewport");
  await page.keyboard.press("Escape");
  const discard = page.getByRole("dialog", { name: "放弃路由更改？", exact: true });
  await expect(discard).toBeFocused();
  await discard.getByRole("button", { name: "取消", exact: true }).click();
  await expect(child.getByRole("spinbutton", { name: /^流量权重/ })).toHaveValue("25");
  await child.getByTitle("关闭", { exact: true }).click();
  await discard.getByRole("button", { name: "放弃更改", exact: true }).click();
  await expect(child).toHaveCount(0);
  await expect(edit).toBeFocused();
  const add = parent.getByRole("button", { name: "添加线路", exact: true });
  await add.click();
  const create = page.getByRole("dialog", { name: `添加线路 · ${model.name}`, exact: true });
  await expect(create).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(create).toHaveCount(0);
  await expect(add).toBeFocused();
});

test("route-management opens-unmapped-model-from-directory", async ({ page, api }, testInfo) => {
  installRouteFixtures(api);
  api.define("POST", "/api/admin/routing-rules", input => {
    expect(input.body).toMatchObject({ model_name: unmapped.name, provider_id: provider.id, provider_model: route.provider_model, weight: 100, priority: 1, strategy: "priority_weighted", project_scope: "all", project_ids: [], sticky_session: false, status: "active" });
    const created = { ...route, id: "route_ui_created", model_name: unmapped.name, project_scope: "all", project_ids: [], tags: [], sticky_session: false };
    api.replaceResponse("GET", "/api/admin/routing-rules", { data: [route, created] });
    return { json: created };
  });
  await page.goto("/models");
  await page.getByRole("button", { name: "草稿/待映射", exact: true }).click();
  const row = page.getByRole("row").filter({ hasText: unmapped.name });
  await row.getByRole("button", { name: `路由策略：${unmapped.name}`, exact: true }).click();
  await expect(page).toHaveURL(/\/routes$/);
  await expect(page.getByRole("tab", { name: /全部模型/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("row").filter({ hasText: unmapped.name })).toBeVisible();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "配置模型路由", exact: true });
  await expect(dialog.getByRole("heading", { name: unmapped.name, exact: true })).toBeVisible();
  await expect(dialog.getByText("该统一模型还没有 Provider 线路")).toBeVisible();
  await capture(page, testInfo, dialog, "route-management-unmapped-model", "从待映射模型进入路由后可直接添加线路");
  await dialog.getByRole("button", { name: "添加线路", exact: true }).click();
  const child = page.getByRole("dialog", { name: `添加线路 · ${unmapped.name}`, exact: true });
  await expect(child).toBeFocused();
  await expect(child.getByRole("combobox", { name: /^模型目录模型/ })).toHaveCount(0);
  await child.getByRole("combobox", { name: /^Provider 模型/ }).selectOption(route.provider_model);
  await child.getByRole("button", { name: "添加路由", exact: true }).click();
  await expect(child).toHaveCount(0);
  await expect(dialog.getByText(route.provider_model, { exact: true })).toBeVisible();
});
