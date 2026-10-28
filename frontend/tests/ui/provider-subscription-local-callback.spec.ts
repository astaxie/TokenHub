import type { PluginActionDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installAutomaticCallback(api: MockAPI, firstError?: string) {
  const type = "chatgpt-subscription", resourceType = "chatgpt_subscription", pluginID = "test.chatgpt-subscription";
  const model = { id: "ui-chatgpt", name: "UI ChatGPT", type: "chat" };
  const catalog: ProviderCatalogEntry = { id: "chatgpt-api", name: "ChatGPT API", display_name: "ChatGPT API", type, source: "ui-fixture", models_count: 0, models: [] };
  const provider: Provider = { id: "prv_ui_chatgpt", name: "ChatGPT API", type, priority: 1, healthy: true, status: "active", options: { catalog_id: catalog.id } };
  let savedProvider: Provider | undefined, savedResource: ProviderResource | undefined;
  let savedCredentials: Record<string, unknown> | undefined;
  const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: pluginID, action_id: capability, capability, subject: type, kind: "mutate", metadata: { provider_resource_type: resourceType, subscription_auth: "true", subscription_session_json: "false" } }));
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.define("GET", "/api/admin/provider-catalog/chatgpt-api", () => ({ json: { data: catalog } }), query => {
    expect([{}, ...(savedResource ? [{ resource_id: savedResource.id }] : [])]).toContainEqual(Object.fromEntries(query));
  });
  api.define("GET", "/api/admin/providers", () => ({ json: { data: savedProvider ? [savedProvider] : [] } }));
  api.define("GET", "/api/admin/provider-resources", () => ({ json: { data: savedResource ? [savedResource] : [] } }));
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type, capabilities: ["chat", "oauth"], provider_policy: { credentials_scope: "resource", api_key_required: false, supports_custom_headers: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: pluginID, name: "ChatGPT API", version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resourceType, subject: type }] }] });
  api.respond("GET", "/api/admin/plugin-actions", { data: actions });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.respond("POST", `/api/admin/provider-actions/${type}/oauth.methods`, { data: { methods: [{ id: "oauth", label: "ChatGPT API", type: "oauth" }] } });
  let starts = 0, ready = false;
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.start`, input => {
    // The isolated API origin is non-loopback. The UI follows the returned flow;
    // component tests separately verify the local callback opt-in request.
    expect(input.body).toEqual({ resource_type: resourceType, method_id: "oauth", inputs: {}, ...(savedResource ? { resource_id: savedResource.id } : starts ? { registration_id: "synthetic-opaque-registration" } : {}) });
    if (savedResource) expect(savedCredentials).toMatchObject({ user_id: "synthetic-subject", account_id: "synthetic-workspace", client_id: "synthetic-issued-client" });
    starts++; ready = false;
    return { json: { data: { session_id: `session-${starts}`, state: `state-${starts}`, registration_id: "synthetic-opaque-registration", flow: "auto", callback_mode: "none", poll_interval: 2, auth_url: "https://auth.example.test/chatgpt" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.cancel`, input => {
    expect(input.body).toEqual({ resource_type: resourceType, session_id: "session-1", state: "state-1" });
    return { json: { data: { status: "cancelled" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${type}/oauth.exchange`, input => {
    expect(input.body).toEqual({ resource_type: resourceType, session_id: `session-${starts}`, state: `state-${starts}`, code: "" });
    if (!ready) return { json: { data: { status: "pending" } } };
    if (firstError && starts === 1) return { status: 409, json: { error: { code: firstError, message: "Synthetic callback requires retry" } } };
    return { json: { data: { access_token: savedResource ? "synthetic-reauthorized-access" : "synthetic-access", refresh_token: "synthetic-refresh", client_id: "synthetic-issued-client", user_id: "synthetic-subject", account_id: "synthetic-workspace", catalog: { ...catalog, models_count: 1, models: [model] } } } };
  });
  api.define("POST", "/api/admin/providers", input => {
    expect(input.body).toMatchObject({ type, selected_models: [model.id], custom_models: [model] });
    savedProvider = provider;
    return { status: 201, json: { provider, imported_models: 1 } };
  });
  api.define("POST", "/api/admin/provider-resources", input => {
    expect(input.body).toMatchObject({ provider_id: provider.id, resource_type: resourceType, credentials: { replace: true, access_token: "synthetic-access", refresh_token: "synthetic-refresh", client_id: "synthetic-issued-client", user_id: "synthetic-subject", account_id: "synthetic-workspace" } });
    savedCredentials = (input.body as { credentials: Record<string, unknown> }).credentials;
    savedResource = { id: "rsrc_ui_chatgpt", provider_id: provider.id, resource_type: resourceType, name: "Synthetic account", healthy: true, status: "active", priority: 1, weight: 100, credential_summary: { auth_type: "oauth", user_id: String(savedCredentials.user_id), account_id: String(savedCredentials.account_id) } };
    return { status: 201, json: savedResource };
  });
  api.define("PATCH", "/api/admin/provider-resources/rsrc_ui_chatgpt", input => {
    expect(Object.keys(input.body as object)).toEqual(["credentials"]);
    expect(input.body).toMatchObject({ credentials: { replace: true, access_token: "synthetic-reauthorized-access", client_id: savedCredentials?.client_id, user_id: savedCredentials?.user_id, account_id: savedCredentials?.account_id } });
    savedCredentials = (input.body as { credentials: Record<string, unknown> }).credentials;
    return { json: savedResource };
  });
  return () => { ready = true; };
}

for (const mobile of [false, true]) for (const outcome of ["completed", "denied then retried", "registration then retried"]) test(`subscription ChatGPT server-selected automatic callback ${outcome} ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  const firstError = outcome === "completed" ? undefined : outcome === "denied then retried" ? "subscription_oauth_authorization_denied" : "subscription_oauth_registration_retry";
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => { window.open = () => null; });
  const completeCallback = installAutomaticCallback(api, firstError);
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
  const variant = `${outcome.replaceAll(" ", "-")}-${mobile ? "mobile" : "desktop"}`;
  await capture(page, testInfo, auth, `chatgpt-automatic-pending-${variant}`, "ChatGPT 自动回调：等待登录完成，无需粘贴授权码", "viewport");
  completeCallback();
  if (firstError) {
    await expect(auth.getByRole("alert")).toHaveText(firstError === "subscription_oauth_registration_retry" ? "客户端注册已完成。请再次点击“打开授权”并完成登录，以继续连接账号。" : "本次授权未获同意。请重新打开授权，并在登录页同意所需权限。");
    await expect(start).toBeEnabled();
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.cancel")).length).toBe(1);
    await capture(page, testInfo, auth, `chatgpt-automatic-retry-${variant}`, firstError === "subscription_oauth_registration_retry" ? "ChatGPT 自动回调：客户端注册完成后再次打开授权" : "ChatGPT 自动回调：拒绝授权后可重新登录", "viewport");
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
  expect(api.calls.filter(call => call.path.endsWith("oauth.start"))).toHaveLength(firstError ? 2 : 1);
  expect(api.calls.filter(call => call.path === "/api/admin/provider-resources" && call.method === "POST")).toHaveLength(1);
  if (outcome === "registration then retried") {
    await page.getByRole("row").filter({ hasText: "ChatGPT API" }).getByRole("button", { name: "编辑", exact: true }).click();
    await editor.getByRole("combobox", { name: "账号列表" }).selectOption("rsrc_ui_chatgpt");
    const panel = editor.getByRole("region", { name: "更新订阅账号凭据" });
    await panel.getByRole("button", { name: "重新登录", exact: true }).click();
    await panel.getByRole("button", { name: "打开授权", exact: true }).click();
    await expect(panel.getByText("请在供应商网页完成登录后返回这里。TokenHub 会自动回填账号凭据，无需粘贴授权码。回填后请保存账号资源。", { exact: true })).toBeVisible();
    completeCallback();
    await expect(panel.getByRole("button", { name: "保存新账号凭据", exact: true })).toBeEnabled();
    await panel.getByRole("button", { name: "保存新账号凭据", exact: true }).click();
    await expect(panel.getByRole("status")).toHaveText("账号凭据已更新。");
    expect(api.calls.filter(call => call.path.endsWith("oauth.start"))).toHaveLength(3);
    expect(api.calls.filter(call => call.path === "/api/admin/provider-resources/rsrc_ui_chatgpt" && call.method === "PATCH")).toHaveLength(1);
    await panel.scrollIntoViewIfNeeded();
    await capture(page, testInfo, panel, `chatgpt-reauthorization-saved-${variant}`, "ChatGPT 已保存账号：重新授权保留用户身份并更新凭据", "viewport");
  }
});
