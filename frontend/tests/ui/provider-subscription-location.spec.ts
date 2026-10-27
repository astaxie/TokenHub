import type { PluginActionDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const guidance = "Google 订阅服务不支持当前请求所在地区。请检查账号的地区资格以及 TokenHub 服务器的网络出口地区。";

function installLocationFailure(api: MockAPI) {
  const provider: Provider = { id: "prv_ui_google", name: "UI Antigravity", type: "antigravity_subscription", status: "active", healthy: true, priority: 1, options: { catalog_id: "antigravity" } };
  const resource: ProviderResource = { id: "rsrc_ui_google", provider_id: provider.id, name: "UI Google Account", resource_type: "antigravity_account", status: "active", healthy: true, priority: 1, weight: 100, credential_summary: { account_email: "synthetic@example.test", auth_type: "oauth" } };
  const catalog: ProviderCatalogEntry = { id: "antigravity", name: "Antigravity", display_name: "Antigravity", type: provider.type, source: "ui-fixture", models_count: 1, models: [{ id: "ui-google-chat", name: "UI Google Chat", type: "chat" }] };
  const pluginID = "test.google-subscription";
  const action: PluginActionDescriptor = { plugin_id: pluginID, action_id: "probe.run", capability: "probe.run", subject: provider.type, kind: "test", metadata: { provider_resource_type: resource.resource_type, probe_fields: "model,prompt", default_payload_json: '{"model":"ui-google-chat","prompt":"Reply with OK."}' } };
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-resources", { data: [resource] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.define("GET", "/api/admin/provider-catalog/antigravity", () => ({ json: { data: catalog } }), query => {
    expect([{}, { resource_id: resource.id }]).toContainEqual(Object.fromEntries(query));
  });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: provider.type, capabilities: ["chat", "probe", "oauth"], provider_policy: { credentials_scope: "resource", supports_custom_headers: false, api_key_required: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: pluginID, name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resource.resource_type, subject: provider.type }] }] });
  api.respond("GET", "/api/admin/plugin-actions", { data: [action] });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.define("POST", `/api/admin/plugins/${pluginID}/actions/${action.action_id}`, input => {
    expect(input.body).toMatchObject({ provider_id: provider.id, resource_id: resource.id, model: "ui-google-chat" });
    return { status: 502, json: { error: { code: "google_subscription_location_unsupported", message: "Synthetic regional eligibility failure" } } };
  });
  return { provider, resource };
}

for (const mobile of [false, true]) test(`subscription Google regional eligibility failure ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  const { provider, resource } = installLocationFailure(api);
  await page.goto("/providers");
  const row = page.getByRole("row").filter({ hasText: provider.name });
  await row.getByRole("button", { name: "测试", exact: true }).click();
  await expect(page.getByText(guidance, { exact: true })).toBeVisible();
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.locator("form.provider-modal");
  await editor.getByRole("combobox", { name: "账号列表" }).selectOption(resource.id);
  await editor.getByRole("tab", { name: "高级", exact: true }).click();
  const panel = editor.locator(".provider-resource-probe-panel");
  await panel.getByRole("textbox", { name: "真实提示词", exact: true }).fill("Reply with OK.");
  await panel.getByRole("button", { name: "发送真实测试", exact: true }).click();
  await expect(panel.getByText(guidance, { exact: true })).toBeVisible();
  await expect(panel).not.toContainText("凭证被拒绝");
  await expect(panel).not.toContainText("重新进行账号授权");
  await expect(panel.getByRole("button", { name: "发送真实测试", exact: true })).toBeEnabled();
  await panel.getByText(guidance, { exact: true }).scrollIntoViewIfNeeded();
  await capture(page, testInfo, panel, `google-location-failure-${mobile ? "mobile" : "desktop"}`, "Google 订阅：显示地区资格限制及服务端出口检查提示", "viewport");
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
