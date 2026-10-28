import type { PluginActionDescriptor, ProviderCatalogEntry } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installNativeOAuthRetry(api: MockAPI) {
  const type = "chatgpt-subscription", resourceType = "chatgpt_subscription", pluginID = "test.chatgpt-subscription";
  const model = { id: "ui-chatgpt", name: "UI ChatGPT", type: "chat" };
  const catalog: ProviderCatalogEntry = { id: "chatgpt-api", name: "ChatGPT API", display_name: "ChatGPT API", type, source: "ui-fixture", models_count: 0, models: [] };
  const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: pluginID, action_id: capability, capability, subject: type, kind: "mutate", metadata: { provider_resource_type: resourceType, subscription_auth: "true", subscription_session_json: "false" } }));
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.respond("GET", "/api/admin/provider-catalog/chatgpt-api", { data: catalog });
  api.respond("GET", "/api/admin/providers", { data: [] });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type, capabilities: ["chat", "oauth"], provider_policy: { credentials_scope: "resource", api_key_required: false, supports_custom_headers: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: pluginID, name: "ChatGPT API", version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resourceType, subject: type }] }] });
  api.respond("GET", "/api/admin/plugin-actions", { data: actions });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.respond("POST", `/api/admin/provider-actions/${type}/oauth.methods`, { data: { methods: [{ id: "oauth", label: "ChatGPT API", type: "oauth" }] } });
  let starts = 0;
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.start`, input => {
    expect(input.body).toEqual({ resource_type: resourceType, method_id: "oauth", inputs: {} });
    starts++;
    return { json: { data: { session_id: `session-${starts}`, state: `state-${starts}`, flow: "authorization_code", auth_url: "https://auth.example.test/chatgpt" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.cancel`, input => {
    expect(input.body).toEqual({ resource_type: resourceType, session_id: "session-1", state: "state-1" });
    return { json: { data: { status: "cancelled" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.exchange`, input => {
    expect(input.body).toMatchObject({ resource_type: resourceType, session_id: `session-${starts}`, state: `state-${starts}`, code: `code-${starts}`, client_id: `client-${starts}`, callback_url: `http://localhost:1455/auth/callback?code=code-${starts}&state=state-${starts}&client_id=client-${starts}` });
    if (starts === 1) return { status: 409, json: { error: { code: "subscription_oauth_code_rejected", message: "Synthetic rejected callback", details: { oauth_reason: "code_already_used" } } } };
    return { json: { data: { access_token: "synthetic-access", refresh_token: "synthetic-refresh", client_id: "client-2", catalog: { ...catalog, models_count: 1, models: [model] } } } };
  });
  api.define("POST", "/api/admin/providers", input => {
    expect(input.body).toMatchObject({ type, selected_models: [model.id], custom_models: [model] });
    return { status: 201, json: { provider: { id: "prv_ui_chatgpt", name: "ChatGPT API", type, priority: 1, healthy: true, status: "active" }, imported_models: 1 } };
  });
  api.define("POST", "/api/admin/provider-resources", input => {
    expect(input.body).toMatchObject({ provider_id: "prv_ui_chatgpt", resource_type: resourceType, credentials: { replace: true, access_token: "synthetic-access", refresh_token: "synthetic-refresh", client_id: "client-2" } });
    return { status: 201, json: { id: "rsrc_ui_chatgpt", provider_id: "prv_ui_chatgpt", resource_type: resourceType, name: "Synthetic account", healthy: true, status: "active", priority: 1, weight: 100 } };
  });
}

for (const mobile of [false, true]) test(`subscription native OAuth rejected callback restarts ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => { window.open = () => null; });
  installNativeOAuthRetry(api);
  await page.goto("/providers");
  await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
  const editor = page.locator("form.provider-modal");
  await editor.getByRole("button", { name: /ChatGPT API/ }).click();
  const auth = editor.locator("div.provider-account-auth-wide");
  await auth.getByRole("button", { name: "打开授权", exact: true }).click();
  await auth.getByRole("textbox", { name: /^回调结果/ }).fill("http://localhost:1455/auth/callback?code=code-1&state=state-1&client_id=client-1");
  await auth.getByRole("button", { name: "完成授权", exact: true }).click();
  await expect(auth.getByRole("alert")).toHaveText("授权码已被使用。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
  await expect(auth.getByRole("button", { name: "打开授权", exact: true })).toBeEnabled();
  await expect(auth.getByRole("textbox", { name: /^回调结果/ })).toHaveCount(0);
  await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.cancel")).length).toBe(1);
  await auth.scrollIntoViewIfNeeded();
  await capture(page, testInfo, auth, `native-oauth-rejected-${mobile ? "mobile" : "desktop"}`, "原生订阅授权：回调被拒绝后可重新打开授权", "viewport");
  await auth.getByRole("button", { name: "打开授权", exact: true }).click();
  await expect(auth.getByRole("textbox", { name: /^回调结果/ })).toHaveValue("");
  await expect(auth.getByRole("alert")).toHaveCount(0);
  await auth.getByRole("textbox", { name: /^回调结果/ }).fill("http://localhost:1455/auth/callback?code=code-2&state=state-2&client_id=client-2");
  await auth.getByRole("button", { name: "完成授权", exact: true }).click();
  await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
  await capture(page, testInfo, auth, `native-oauth-restarted-${mobile ? "mobile" : "desktop"}`, "原生订阅授权：新会话成功回填凭据", "viewport");
  await editor.getByRole("button", { name: "下一步", exact: true }).click();
  await editor.getByRole("checkbox", { name: /UI ChatGPT/ }).check();
  await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
  await expect(editor).toHaveCount(0);
  expect(api.calls.filter(call => call.path.endsWith("oauth.start"))).toHaveLength(2);
  expect(api.calls.filter(call => call.path.endsWith("oauth.exchange"))).toHaveLength(2);
});
