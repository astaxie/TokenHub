import type { Provider, ProviderCatalogEntry, ProviderResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const provider: Provider = { id: "prv_ui_pool", name: "UI Key Pool Provider", type: "openai_compatible", base_url: "https://pool.example.test/v1", priority: 1, status: "active", healthy: true, options: { catalog_id: "ui-pool" } };
const catalog: ProviderCatalogEntry = { id: "ui-pool", name: provider.name, display_name: provider.name, type: provider.type, base_url: provider.base_url, source: "ui-fixture", models_count: 0, models: [] };
const keyName = "UI Production Key With a Long Regional Deployment Name";

function installPool(api: MockAPI, failSave = false) {
  let resources: ProviderResource[] = [];
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.respond("GET", "/api/admin/provider-catalog/ui-pool", { data: catalog });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: provider.type, capabilities: ["chat"], provider_policy: { api_key_required: true, supports_custom_headers: true } }] });
  api.respond("GET", "/api/admin/plugins", { data: [] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.define("GET", "/api/admin/provider-resources", () => ({ json: { data: resources } }));
  api.define("POST", "/api/admin/provider-resources", input => {
    const body = input.body as ProviderResource & { api_key: string };
    expect(body).toMatchObject({ provider_id: provider.id, resource_type: "api_key", status: "active", healthy: true, priority: 1, weight: 100 });
    expect([keyName, "UI Saved Default Key"]).toContain(body.name);
    expect(body.api_key).toBe(body.name === keyName ? "synthetic-pool-key" : "");
    if (failSave) { failSave = false; return { status: 503, json: { error: { message: "Synthetic resource save failure" } } }; }
    const resource: ProviderResource = {
      id: `rsrc_ui_key_${resources.length + 1}`, provider_id: body.provider_id, name: body.name,
      resource_type: body.resource_type, status: body.status, healthy: body.healthy,
      priority: body.priority, weight: body.weight, max_concurrency: body.max_concurrency,
      rate_limit_rpm: body.rate_limit_rpm, token_limit_tpm: body.token_limit_tpm,
    };
    resources.push(resource);
    return { status: 201, json: resource };
  });
  for (const id of ["rsrc_ui_key_1", "rsrc_ui_key_2"]) {
    api.define("PATCH", `/api/admin/provider-resources/${id}`, input => {
      const body = input.body as Partial<ProviderResource>;
      expect(body).toMatchObject({ provider_id: provider.id, resource_type: "api_key", healthy: true });
      expect(body).not.toHaveProperty("api_key");
      expect(body).not.toHaveProperty("headers");
      expect(body).not.toHaveProperty("options");
      resources = resources.map(resource => resource.id === id ? { ...resource, ...body } : resource);
      return { json: resources.find(resource => resource.id === id) };
    });
    api.define("DELETE", `/api/admin/provider-resources/${id}`, () => {
      resources = resources.filter(resource => resource.id !== id);
      return { json: { deleted: true } };
    });
  }
}

for (const viewport of [{ name: "desktop", width: 1440, height: 1000 }, { name: "mobile", width: 390, height: 844 }]) {
  test(`providers api-key-pool ${viewport.name}`, async ({ page, api }, testInfo) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    installPool(api);
    await page.goto("/providers");
    await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
    const pool = page.getByRole("region", { name: "API Key 资源池", exact: true });
    await expect(pool.getByText("尚未添加 API Key 资源，当前使用 Provider 默认密钥。")).toBeVisible();
    await pool.scrollIntoViewIfNeeded();
    await capture(page, testInfo, pool, `provider-key-pool-empty-${viewport.name}`, "API Key 资源池空状态", "viewport");
    await pool.getByRole("button", { name: "添加 API Key", exact: true }).click();
    const editor = pool.getByRole("group", { name: "添加 API Key 资源", exact: true });
    await editor.getByLabel("API Key 名称", { exact: true }).fill(keyName);
    const keyInput = editor.locator('input[type="password"]');
    await expect(keyInput).toHaveAttribute("autocomplete", "new-password");
    await keyInput.fill("synthetic-pool-key");
    await editor.getByRole("button", { name: "保存 API Key 资源", exact: true }).click();
    await expect(pool.getByText(keyName, { exact: true })).toBeVisible();
    await expect(editor).toHaveCount(0);
    await pool.getByRole("button", { name: "添加 API Key", exact: true }).click();
    await editor.getByLabel("API Key 名称", { exact: true }).fill("UI Saved Default Key");
    await editor.getByLabel("使用 Provider 已保存的密钥").check();
    await expect(editor.locator('input[type="password"]')).toHaveCount(0);
    await editor.getByRole("button", { name: "保存 API Key 资源", exact: true }).click();
    await expect(pool.getByRole("listitem")).toHaveCount(2);
    await pool.getByRole("list").scrollIntoViewIfNeeded();
    await expect(pool).not.toContainText("synthetic-pool-key");
    await capture(page, testInfo, pool, `provider-key-pool-list-${viewport.name}`, "同一 Provider 的多个密钥与操作", "viewport");

    const first = pool.getByRole("listitem").filter({ hasText: keyName });
    await first.getByRole("button", { name: "编辑", exact: true }).click();
    const edit = pool.getByRole("group", { name: "编辑 API Key 资源", exact: true });
    await expect(edit.locator('input[type="password"]')).toHaveValue("");
    await edit.getByText("调度与限流设置", { exact: true }).click();
    await edit.getByLabel("权重", { exact: true }).fill("75");
    await edit.getByLabel("最大并发", { exact: true }).fill("3");
    await expect(edit.getByLabel("权重", { exact: true })).toHaveValue("75");
    await expect(edit.getByLabel("最大并发", { exact: true })).toHaveValue("3");
    await edit.locator("legend").evaluate(element => element.scrollIntoView({ block: "start" }));
    await expect(edit.getByLabel("API Key 名称", { exact: true })).toBeInViewport();
    await capture(page, testInfo, edit, `provider-key-pool-editor-top-${viewport.name}`, "密钥编辑与展开的调度设置顶部", "viewport");
    await edit.getByRole("button", { name: "保存 API Key 资源", exact: true }).evaluate(element => element.scrollIntoView({ block: "end" }));
    await expect(edit.getByLabel("最大并发", { exact: true })).toBeInViewport();
    await capture(page, testInfo, edit, `provider-key-pool-editor-bottom-${viewport.name}`, "密钥限流、并发设置与保存操作", "viewport");
    await edit.getByRole("button", { name: "保存 API Key 资源", exact: true }).click();
    await expect(edit).toHaveCount(0);
    const update = api.calls.find(call => call.method === "PATCH" && call.path.endsWith("rsrc_ui_key_1"));
    expect(update?.body).toMatchObject({ weight: 75, max_concurrency: 3 });
    await first.getByRole("button", { name: "停用", exact: true }).click();
    await expect(first.getByRole("button", { name: "启用", exact: true })).toBeVisible();
    await first.getByRole("button", { name: "删除", exact: true }).click();
    const confirmation = pool.getByRole("group", { name: "删除 API Key 资源", exact: true });
    await expect(confirmation).toContainText("绑定它的路由将改为自动选择其他资源或 Provider 默认连接");
    expect(api.calls.filter(call => call.method === "DELETE")).toHaveLength(0);
    await confirmation.getByRole("button", { name: "确认删除", exact: true }).click();
    await expect(pool.getByRole("listitem")).toHaveCount(1);
    await expect(pool.getByText("UI Saved Default Key", { exact: true })).toBeVisible();
    expect(api.calls.filter(call => call.path === "/api/admin/providers" && call.method !== "GET")).toHaveLength(0);
  });
}

test("providers api-key-pool save-error", async ({ page, api }, testInfo) => {
  installPool(api, true);
  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
  const pool = page.getByRole("region", { name: "API Key 资源池", exact: true });
  await pool.getByRole("button", { name: "添加 API Key", exact: true }).click();
  const editor = pool.getByRole("group", { name: "添加 API Key 资源", exact: true });
  await editor.getByLabel("API Key 名称", { exact: true }).fill(keyName);
  await editor.locator('input[type="password"]').fill("synthetic-pool-key");
  await editor.getByRole("button", { name: "保存 API Key 资源", exact: true }).click();
  await expect(pool.getByRole("alert")).toContainText("Synthetic resource save failure");
  await expect(editor.getByLabel("API Key 名称", { exact: true })).toHaveValue(keyName);
  await expect(editor.locator('input[type="password"]')).toHaveValue("synthetic-pool-key");
  await pool.getByRole("alert").scrollIntoViewIfNeeded();
  await capture(page, testInfo, pool, "provider-key-pool-save-error", "保存失败保留密钥草稿并允许重试", "viewport");
  await editor.getByRole("button", { name: "保存 API Key 资源", exact: true }).click();
  await expect(pool.getByRole("alert")).toHaveCount(0);
  await expect(pool.getByRole("listitem")).toHaveCount(1);
});
