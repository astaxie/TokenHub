import type { PluginActionDescriptor, ProviderCatalogEntry } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installAutomaticCallback(api: MockAPI, denyFirst: boolean) {
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
  let starts = 0, ready = false;
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.start`, input => {
    // The isolated API origin is non-loopback. The UI follows the returned flow;
    // component tests separately verify the local callback opt-in request.
    expect(input.body).toEqual({ resource_type: resourceType, method_id: "oauth", inputs: {} });
    starts++; ready = false;
    return { json: { data: { session_id: `session-${starts}`, state: `state-${starts}`, flow: "auto", callback_mode: "none", poll_interval: 2, auth_url: "https://auth.example.test/chatgpt" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.cancel`, input => {
    expect(input.body).toEqual({ resource_type: resourceType, session_id: "session-1", state: "state-1" });
    return { json: { data: { status: "cancelled" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.exchange`, input => {
    expect(input.body).toEqual({ resource_type: resourceType, session_id: `session-${starts}`, state: `state-${starts}`, code: "" });
    if (!ready) return { json: { data: { status: "pending" } } };
    if (denyFirst && starts === 1) return { status: 409, json: { error: { code: "subscription_oauth_authorization_denied", message: "Synthetic denied callback" } } };
    return { json: { data: { access_token: "synthetic-access", refresh_token: "synthetic-refresh", client_id: `client-${starts}`, catalog: { ...catalog, models_count: 1, models: [model] } } } };
  });
  api.define("POST", "/api/admin/providers", input => {
    expect(input.body).toMatchObject({ type, selected_models: [model.id], custom_models: [model] });
    return { status: 201, json: { provider: { id: "prv_ui_chatgpt", name: "ChatGPT API", type, priority: 1, healthy: true, status: "active" }, imported_models: 1 } };
  });
  api.define("POST", "/api/admin/provider-resources", input => {
    expect(input.body).toMatchObject({ provider_id: "prv_ui_chatgpt", resource_type: resourceType, credentials: { replace: true, access_token: "synthetic-access", refresh_token: "synthetic-refresh", client_id: `client-${starts}` } });
    return { status: 201, json: { id: "rsrc_ui_chatgpt", provider_id: "prv_ui_chatgpt", resource_type: resourceType, name: "Synthetic account", healthy: true, status: "active", priority: 1, weight: 100 } };
  });
  return () => { ready = true; };
}

for (const mobile of [false, true]) for (const denyFirst of [false, true]) test(`subscription ChatGPT server-selected automatic callback ${denyFirst ? "denied then retried" : "completed"} ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => { window.open = () => null; });
  const completeCallback = installAutomaticCallback(api, denyFirst);
  await page.goto("/providers");
  await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
  const editor = page.locator("form.provider-modal");
  await editor.getByRole("button", { name: /ChatGPT API/ }).click();
  const auth = editor.locator("div.provider-account-auth-wide");
  const start = auth.getByRole("button", { name: "打开授权", exact: true });
  await start.click();
  await expect(auth.getByRole("textbox", { name: /^回调结果/ })).toHaveCount(0);
  await expect(auth.getByText("请在供应商网页完成登录后返回这里。TokenHub 会自动回填账号凭据，无需粘贴授权码。回填后请保存账号资源。", { exact: true })).toBeVisible();
  await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.exchange")).length).toBeGreaterThan(0);
  await expect(auth.getByText("已回填账号 Token", { exact: true })).toHaveCount(0);
  await auth.scrollIntoViewIfNeeded();
  const variant = `${denyFirst ? "denied" : "completed"}-${mobile ? "mobile" : "desktop"}`;
  await capture(page, testInfo, auth, `chatgpt-automatic-pending-${variant}`, "ChatGPT 自动回调：等待登录完成，无需粘贴授权码", "viewport");
  completeCallback();
  if (denyFirst) {
    await expect(auth.getByRole("alert")).toHaveText("本次授权未获同意。请重新打开授权，并在登录页同意所需权限。");
    await expect(start).toBeEnabled();
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.cancel")).length).toBe(1);
    await capture(page, testInfo, auth, `chatgpt-automatic-denied-${variant}`, "ChatGPT 自动回调：拒绝授权后可重新登录", "viewport");
    const previousPolls = api.calls.filter(call => call.path.endsWith("oauth.exchange")).length;
    await start.click();
    await expect(auth.getByRole("alert")).toHaveCount(0);
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.exchange")).length).toBeGreaterThan(previousPolls);
    await expect(auth.getByText("已回填账号 Token", { exact: true })).toHaveCount(0);
    completeCallback();
  }
  await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
  await expect(auth.getByRole("textbox", { name: /^回调结果/ })).toHaveCount(0);
  await capture(page, testInfo, auth, `chatgpt-automatic-filled-${variant}`, "ChatGPT 自动回调：凭据回填后提示保存账号", "viewport");
  await editor.getByRole("button", { name: "下一步", exact: true }).click();
  await editor.getByRole("checkbox", { name: /UI ChatGPT/ }).check();
  await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
  await expect(editor).toHaveCount(0);
  expect(api.calls.filter(call => call.path.endsWith("oauth.start"))).toHaveLength(denyFirst ? 2 : 1);
  expect(api.calls.filter(call => call.path === "/api/admin/provider-resources" && call.method === "POST")).toHaveLength(1);
});
