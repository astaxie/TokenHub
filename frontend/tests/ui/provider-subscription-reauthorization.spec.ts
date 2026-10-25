import type { PluginActionDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installReauthorization(api: MockAPI) {
  const provider: Provider = { id: "prv_ui_reauthorization", name: "UI Subscription Reauthorization", type: "ui_subscription", status: "active", healthy: true, priority: 1, options: { catalog_id: "ui-subscription" } };
  const catalog: ProviderCatalogEntry = { id: "ui-subscription", name: provider.name, display_name: provider.name, type: provider.type, source: "ui-fixture", models_count: 0, models: [] };
  const resource: ProviderResource = { id: "account_ui_reauthorization", provider_id: provider.id, name: "UI Existing Account", resource_type: "ui_account", status: "active", healthy: true, priority: 7, weight: 25, credential_summary: { account_email: "old@example.invalid", account_id: "old-identity", auth_type: "oauth", oauth_reauthorization_required: "true" } };
  const pluginID = "test.ui-reauthorization";
  const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: pluginID, action_id: capability, capability, subject: provider.type, kind: "mutate", metadata: { subscription_auth: "true", subscription_session_json: "true", provider_resource_type: resource.resource_type } }));
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.define("GET", "/api/admin/provider-resources", () => ({ json: { data: [resource] } }));
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.define("GET", "/api/admin/provider-catalog/ui-subscription", () => ({ json: { data: catalog } }), query => {
    expect([{}, { resource_id: resource.id }]).toContainEqual(Object.fromEntries(query));
  });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: provider.type, capabilities: ["chat", "oauth"], provider_policy: { credentials_scope: "resource", api_key_required: false, supports_custom_headers: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: pluginID, name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resource.resource_type, subject: provider.type }] }] });
  api.respond("GET", "/api/admin/plugin-actions", { data: actions });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.respond("POST", `/api/admin/provider-actions/${provider.type}/oauth.methods`, { data: { methods: [] } });
  api.define("POST", `/api/admin/provider-actions/${provider.type}/oauth.exchange`, input => {
    expect(input.body).toEqual({ resource_type: resource.resource_type, mode: "import", session_json: '{"access":"synthetic-import"}' });
    return { json: { data: { session_json: '{"access":"synthetic-rotated"}', catalog } } };
  });
  let attempts = 0;
  api.define("PATCH", `/api/admin/provider-resources/${resource.id}`, input => {
    expect(Object.keys(input.body as object)).toEqual(["credentials"]);
    expect(input.body).toMatchObject({ credentials: { replace: true, session_json: '{"access":"synthetic-rotated"}', access_token: "", refresh_token: "", account_id: "", email: "" } });
    if (++attempts === 1) return { status: 503, json: { error: { message: "Synthetic credential save failure" } } };
    resource.credential_summary = { auth_type: "oauth" };
    return { json: { data: resource } };
  });
  return { provider, resource };
}

for (const mobile of [false, true]) test(`subscription reauthorization replaces only the selected account credentials ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  const { provider, resource } = installReauthorization(api);
  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.locator(".provider-modal");
  const panel = editor.getByRole("region", { name: "更新订阅账号凭据" });
  await expect(panel).toContainText("请在顶部账号列表中选择一个账号，再更新其凭据。");
  await editor.getByRole("combobox", { name: "账号列表" }).selectOption(resource.id);
  await panel.getByRole("button", { name: "重新授权账号", exact: true }).click();
  await panel.getByText("导入订阅会话", { exact: true }).first().click();
  await panel.getByLabel(/^订阅会话 JSON/).fill('{"access":"synthetic-import"}');
  await panel.getByRole("button", { name: "导入订阅会话", exact: true }).click();
  await expect(panel.getByRole("button", { name: "保存新账号凭据" })).toBeEnabled();
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
  await panel.getByRole("button", { name: "保存新账号凭据" }).click();
  await expect(panel.getByRole("alert")).toHaveText("Synthetic credential save failure");
  await panel.evaluate(el => el.scrollIntoView({ block: "center" }));
  await capture(page, testInfo, panel, `subscription-reauthorization-retry-${mobile ? "mobile" : "desktop"}`, "订阅账号：凭据保存失败后可重试", "viewport");
  await panel.getByRole("button", { name: "保存新账号凭据" }).click();
  await expect(panel.getByRole("status")).toHaveText("账号凭据已更新。");
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(2);
  await panel.evaluate(el => el.scrollIntoView({ block: "center" }));
  await capture(page, testInfo, panel, `subscription-reauthorization-saved-${mobile ? "mobile" : "desktop"}`, "订阅账号：新凭据已保存", "viewport");
});
