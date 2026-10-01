import type { Model, ModelRoute, ModelRoutePolicy, Provider, ProviderModel } from "../../features/admin/core/types";
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

test("route-management last-route-deletion", async ({ page, api }, testInfo) => {
  installRouteFixtures(api);
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, providers: [provider], models: [unmapped, model] });
  api.define("DELETE", `/api/admin/routing-rules/${route.id}`, () => {
    api.replaceResponse("GET", "/api/admin/routing-rules", { data: [] });
    return { status: 204, json: null };
  });
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  api.define("POST", "/api/admin/routing-rules", async input => {
    expect(input.body).toMatchObject({ model_name: unmapped.name, provider_id: provider.id, provider_model: route.provider_model, strategy: "priority_weighted", project_scope: "all", status: "active" });
    await pending;
    const created = { ...route, id: "route_ui_after_delete", model_name: unmapped.name, project_scope: "all", project_ids: [], tags: [], sticky_session: false };
    api.replaceResponse("GET", "/api/admin/routing-rules", { data: [created] });
    return { json: created };
  });
  try {
    await page.goto("/routes");
    await page.getByRole("button", { name: "配置", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "配置模型路由", exact: true });
    await dialog.getByTitle("删除", { exact: true }).click();
    const confirmation = page.getByRole("dialog", { name: "确认删除", exact: true });
    await confirmation.getByRole("button", { name: "删除", exact: true }).click();
    await expect(confirmation).toHaveCount(0);
    await expect(dialog.getByText("该统一模型还没有 Provider 线路", { exact: true })).toBeVisible();
    await expect(dialog.getByRole("heading", { name: model.name, exact: true })).toBeVisible();
    const add = dialog.getByRole("button", { name: "添加线路", exact: true });
    await expect(add).toBeEnabled();
    await capture(page, testInfo, dialog, "route-management-last-route-empty-editor", "删除最后一条线路后仍在当前模型中，可继续添加线路");
    await add.click();
    const sameModel = page.getByRole("dialog", { name: `添加线路 · ${model.name}`, exact: true });
    await expect(sameModel).toBeFocused();
    await sameModel.getByRole("button", { name: "取消", exact: true }).click();
    await expect(sameModel).toHaveCount(0);
    await expect(add).toBeFocused();
    await dialog.getByRole("button", { name: "关闭", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "还没有路由策略", exact: true })).toBeVisible();
    await capture(page, testInfo, page.locator(".model-governance-empty-state"), "route-management-last-route-closed", "主动关闭空模型编辑器后显示全局空状态");

    await page.getByRole("button", { name: "为模型添加路由", exact: true }).click();
    const otherModel = page.getByRole("dialog", { name: `添加线路 · ${unmapped.name}`, exact: true });
    await otherModel.getByRole("combobox", { name: /^Provider 模型/ }).selectOption(route.provider_model);
    await otherModel.getByRole("button", { name: "添加路由", exact: true }).click();
    await expect(otherModel.getByRole("button", { name: "添加路由", exact: true })).toBeDisabled();
    await expect(dialog).toHaveCount(0);
    release();
    await expect(otherModel).toHaveCount(0);
    await expect(page.getByRole("row").filter({ hasText: unmapped.name })).toBeVisible();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("row").filter({ hasText: model.name })).toHaveCount(0);
  } finally {
    release();
  }
});

for (const state of ["unknown-desktop", "unknown-mobile", "mixed-known-unknown"] as const) {
  test(`route-management ${state}`, async ({ page, api }, testInfo) => {
    if (state === "unknown-mobile") await page.setViewportSize({ width: 390, height: 844 });
    installRouteFixtures(api);
    const unknown: ModelRoute = { ...route, id: "route_ui_unknown", priority: 2, weight: 23, quality_score: 71, cost_score: 84, strategy: "future-strategy" };
    const routes: ModelRoute[] = state === "mixed-known-unknown"
      ? [{ ...route, weight: 75, quality_score: 62, cost_score: 39, strategy: "quality" }, unknown]
      : [unknown];
    api.replaceResponse("GET", "/api/admin/routing-rules", { data: routes });
    const saved: ModelRoutePolicy[] = [];
    api.define("PATCH", `/api/admin/model-routing-policies/${model.name}`, input => {
      const policy = input.body as ModelRoutePolicy;
      expect(policy.strategy).toBe("balanced");
      expect(policy.routes).toEqual(routes.map(item => ({ route_id: item.id, weight: item.weight, quality_score: item.quality_score, cost_score: item.cost_score })));
      saved.push(policy);
      const updated = routes.map(item => ({ ...item, strategy: policy.strategy, priority: 1 }));
      api.replaceResponse("GET", "/api/admin/routing-rules", { data: updated });
      return { json: { strategy: policy.strategy, data: updated, semantic_routing: policy.semantic_routing } };
    });
    await page.goto("/routes");
    await expect(page.getByRole("cell", { name: state === "mixed-known-unknown" ? "策略不一致" : "future-strategy" })).toBeVisible();
    await page.getByRole("button", { name: "配置", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "配置模型路由", exact: true });
    const tabs = dialog.getByRole("tablist", { name: "模型路由策略", exact: true }).getByRole("tab");
    await expect(tabs).toHaveCount(7);
    for (const tab of await tabs.all()) {
      await expect(tab).toBeVisible();
      await expect(tab).toHaveAttribute("aria-selected", "false");
    }
    const warning = dialog.getByText("此模型包含未知路由策略：future-strategy。请选择受支持的策略后再保存。", { exact: true });
    await expect(warning).toBeVisible();
    await expect(dialog.getByRole("spinbutton")).toHaveCount(0);
    await expect(dialog.getByRole("button", { name: "查看当前策略说明", exact: true })).toHaveCount(0);
    if (state === "mixed-known-unknown") await expect(dialog.getByText("当前 Provider 线路策略不一致，应用后将统一为所选模型策略。", { exact: true })).toBeVisible();
    const apply = dialog.getByRole("button", { name: "应用策略", exact: true });
    await expect(apply).toBeDisabled();
    await capture(page, testInfo, dialog, `route-management-${state}-warning`, "未知路由策略：明确选择前不显示默认策略参数", "viewport");

    await dialog.getByRole("tab", { name: "综合评分", exact: true }).click();
    await expect(dialog.getByRole("tab", { name: "综合评分", exact: true })).toHaveAttribute("aria-selected", "true");
    await expect(warning).toHaveCount(0);
    for (const [index, item] of routes.entries()) {
      await expect(dialog.getByRole("spinbutton", { name: "权重", exact: true }).nth(index)).toHaveValue(String(item.weight));
      await expect(dialog.getByRole("spinbutton", { name: "质量", exact: true }).nth(index)).toHaveValue(String(item.quality_score));
      await expect(dialog.getByRole("spinbutton", { name: "成本", exact: true }).nth(index)).toHaveValue(String(item.cost_score));
    }
    await expect(apply).toBeEnabled();
    if (state === "unknown-mobile") await dialog.getByRole("spinbutton", { name: "权重", exact: true }).scrollIntoViewIfNeeded();
    await capture(page, testInfo, dialog, `route-management-${state}-selected`, "明确选择综合评分后保留原有权重和评分", "viewport");
    await apply.click();
    await expect.poll(() => saved.length).toBe(1);
    await expect(apply).toBeDisabled();
    await dialog.getByRole("button", { name: "关闭", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("cell", { name: "综合评分" })).toBeVisible();
  });
}
