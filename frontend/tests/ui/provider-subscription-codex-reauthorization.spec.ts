import type { PluginActionDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installCodexReauthorization(api: MockAPI) {
  const provider: Provider = { id: "prv_ui_codex_reauth", name: "UI Codex Reauthorization", type: "openai_codex", status: "active", healthy: true, priority: 1, options: { catalog_id: "openai-codex" } };
  const catalog: ProviderCatalogEntry = { id: "openai-codex", name: provider.name, display_name: provider.name, type: provider.type, source: "ui-fixture", models_count: 0, models: [] };
  const resource: ProviderResource = { id: "account_ui_codex", provider_id: provider.id, name: "UI Existing Codex Account", resource_type: "openai_subscription", status: "active", healthy: true, priority: 7, weight: 25, credential_summary: { account_email: "old@example.invalid", auth_type: "oauth", oauth_reauthorization_required: "true" } };
  const pluginID = "tokenhub.provider.openai-codex";
  const actions: PluginActionDescriptor[] = ["oauth.start", "oauth.exchange"].map(capability => ({ plugin_id: pluginID, action_id: `${provider.type}.${capability}`, capability, subject: provider.type, kind: "mutate", metadata: { oauth_redirect_uri: "http://localhost:1455/auth/callback" } }));
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.define("GET", "/api/admin/provider-resources", () => ({ json: { data: [resource] } }));
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.define("GET", "/api/admin/provider-catalog/openai-codex", () => ({ json: { data: catalog } }), query => {
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
  api.define("POST", "/api/admin/provider-actions/openai_codex/oauth.start", input => {
    expect(input.body).toEqual({ method_id: "", inputs: {}, redirect_uri: "http://localhost:1455/auth/callback" });
    return { json: { data: { session_id: "synthetic-codex-session", state: "synthetic-state", auth_url: "https://auth.example.test/login", redirect_uri: "http://localhost:1455/auth/callback" } } };
  });
  api.define("POST", "/api/admin/provider-actions/openai_codex/oauth.exchange", input => {
    expect(input.body).toEqual({ session_id: "synthetic-codex-session", state: "synthetic-state", code: "synthetic-code", client_id: "", callback_url: "http://localhost:1455/auth/callback?code=synthetic-code&state=synthetic-state" });
    return { json: { data: { access_token: "synthetic-codex-token", refresh_token: "synthetic-refresh", account_id: "replacement-codex" } } };
  });
  api.define("PATCH", `/api/admin/provider-resources/${resource.id}`, input => {
    expect(Object.keys(input.body as object)).toEqual(["credentials"]);
    expect(input.body).toMatchObject({ credentials: { replace: true, access_token: "synthetic-codex-token", refresh_token: "synthetic-refresh", account_id: "replacement-codex", session_json: "", email: "" } });
    resource.credential_summary = { auth_type: "oauth", account_id: "replacement-codex" };
    return { json: { data: resource } };
  });
  return { provider, resource };
}

for (const mobile of [false, true]) test(`subscription Codex reauthorization preserves the existing account ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => { window.open = () => null; });
  const { provider, resource } = installCodexReauthorization(api);
  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.locator(".provider-modal");
  const panel = editor.getByRole("region", { name: "更新订阅账号凭据" });
  await expect(editor.getByRole("combobox", { name: "账号列表" })).toHaveValue(resource.id);
  await expect(panel).toContainText("账号 Token 已过期或会话已失效，请重新登录后保存新凭据。");
  await panel.getByRole("button", { name: "重新登录", exact: true }).click();
  await panel.getByRole("button", { name: "打开授权", exact: true }).click();
  await expect(panel).toContainText("授权完成后，即使 localhost 页面显示无法访问，也请复制地址栏中的完整 callback URL 粘贴到这里。");
  await panel.getByRole("textbox", { name: /^回调结果/ }).fill("http://localhost:1455/auth/callback?code=synthetic-code&state=synthetic-state");
  await panel.getByRole("textbox", { name: /^回调结果/ }).scrollIntoViewIfNeeded();
  await capture(page, testInfo, panel, `subscription-codex-reauthorization-${mobile ? "mobile" : "desktop"}`, "Codex 订阅账号：重新登录后回填完整回调地址", "viewport");
  await panel.getByRole("button", { name: "完成授权", exact: true }).click();
  await expect(panel.getByRole("button", { name: "保存新账号凭据" })).toBeEnabled();
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
  await panel.getByRole("button", { name: "保存新账号凭据" }).click();
  await expect(panel.getByRole("status")).toHaveText("账号凭据已更新。");
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
});
