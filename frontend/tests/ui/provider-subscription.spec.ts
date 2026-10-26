import type { PluginActionDescriptor, PluginDescriptor, ProviderCatalogEntry, ProviderCatalogModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const catalog: ProviderCatalogEntry = { id: "ui-subscription", name: "UI Subscription", display_name: "UI Subscription", type: "ui_subscription", base_url: "", categories: ["openai"], models_count: 0, source: "ui-fixture", models: [] };
const plugin: PluginDescriptor = { id: "test.subscription", name: "UI Subscription", version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "ui_account", subject: catalog.type }] };
const model = { id: "ui-chat", name: "UI Chat", category: "openai", type: "chat" };
function install(api: MockAPI, device: boolean | "token" | "auto", pollFailure = false, options: { models?: ProviderCatalogModel[]; selectedModels?: string[] } = {}) {
  let browserAuthorized = false;
  const models = options.models ?? [model];
  const selectedModels = options.selectedModels ?? models.map(entry => entry.id);
  const authorizedCatalog = { ...catalog, models_count: models.length, models };
  const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: plugin.id, action_id: capability, capability, subject: catalog.type, kind: "mutate", metadata: { provider_resource_type: "ui_account", subscription_auth: "true", subscription_session_json: device === "token" ? "false" : "true" } }));
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.respond("GET", "/api/admin/provider-catalog/ui-subscription", { data: catalog });
  api.respond("GET", "/api/admin/providers", { data: [] });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: catalog.type, capabilities: ["chat"], provider_policy: { credentials_scope: "resource", supports_custom_headers: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [plugin] });
  api.respond("GET", "/api/admin/plugin-actions", { data: actions });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.respond("POST", `/api/admin/provider-actions/${catalog.type}/oauth.methods`, { data: { methods: [{ id: "login", label: "Synthetic login", type: "oauth", ...(device === "token" ? { prompts: [{ key: "client_id", label: "OAuth client ID", type: "password" }, { key: "client_secret", label: "OAuth client secret", type: "password" }] } : {}) }] } });
  api.define("POST", `/api/admin/provider-actions/${catalog.type}/oauth.start`, input => {
    expect(input.body).toMatchObject({ resource_type: "ui_account", method_id: "login" });
    if (device === "token") expect(input.body).toMatchObject({ inputs: { client_id: "synthetic-client", client_secret: "synthetic-client-secret" } });
    return { json: { data: { session_id: "ui-login", state: "synthetic-state", ...(device === "auto" ? { flow: "auto", callback_mode: "none" } : { flow: "device_code", user_code: "ABCD-EFGH" }), poll_interval: device === "token" || device === "auto" || pollFailure ? 2 : 60, auth_url: "https://auth.example.test/login" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${catalog.type}/oauth.cancel`, input => {
    expect(input.body).toEqual({ resource_type: "ui_account", session_id: "ui-login", state: "synthetic-state" });
    return { json: { data: { status: "cancelled" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${catalog.type}/oauth.exchange`, input => {
    if (pollFailure) {
      expect(input.body).toEqual({ resource_type: "ui_account", session_id: "ui-login", state: "synthetic-state", code: "" });
      return { status: 502, json: { error: { message: "Synthetic authorization polling failure" } } };
    }
    if (device === "token") {
      expect(input.body).toEqual({ resource_type: "ui_account", session_id: "ui-login", state: "synthetic-state", code: "" });
      return { json: { data: { access_token: "synthetic-native-token", client_id: "synthetic-client", client_secret: "synthetic-client-secret", catalog: authorizedCatalog } } };
    }
    if (device === "auto") {
      expect(input.body).toEqual({ resource_type: "ui_account", session_id: "ui-login", state: "synthetic-state", code: "" });
      return { json: { data: browserAuthorized ? { session_json: '{"refresh":"synthetic-rotated"}', catalog: authorizedCatalog } : { status: "pending" } } };
    }
    expect(device).toBe(false);
    expect(input.body).toEqual({ resource_type: "ui_account", mode: "import", session_json: '{"refresh":"synthetic-original"}' });
    return { json: { data: { session_json: '{"refresh":"synthetic-rotated"}', catalog: authorizedCatalog } } };
  });
  api.define("POST", "/api/admin/providers", input => {
    expect(input.body).toMatchObject({ type: catalog.type, selected_models: selectedModels, base_url: "", custom_models: models });
    return { status: 201, json: { provider: { id: "prv_ui", name: catalog.name, type: catalog.type, priority: 1, healthy: true, status: "active" }, imported_models: selectedModels.length } };
  });
  api.define("POST", "/api/admin/provider-resources", input => {
    expect(input.body).toMatchObject({ provider_id: "prv_ui", resource_type: "ui_account", base_url: "", credentials: { replace: true, ...(device === "token" ? { access_token: "synthetic-native-token", client_id: "synthetic-client", client_secret: "synthetic-client-secret" } : { session_json: '{"refresh":"synthetic-rotated"}' }) } });
    return { status: 201, json: { id: "rsrc_ui", provider_id: "prv_ui", resource_type: "ui_account", name: "Synthetic account", healthy: true, status: "active", priority: 1, weight: 100 } };
  });
  return () => { browserAuthorized = true; };
}

for (const viewport of ["desktop", "mobile"] as const) {
  test(`subscription model bulk selection preserves filtered-out choices ${viewport}`, async ({ page, api }, testInfo) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    const models = [
      { ...model, id: "ui-assistant-alpha", name: "UI Assistant Alpha" },
      { ...model, id: "ui-assistant-beta", name: "UI Assistant Beta" },
      { ...model, id: "ui-vision", name: "UI Vision" },
    ];
    install(api, false, false, { models, selectedModels: ["ui-vision"] });
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Subscription/ }).click();
    const auth = editor.locator("div.provider-account-auth-wide");
    await auth.locator("summary").filter({ hasText: "导入订阅会话" }).click();
    await auth.getByLabel(/^订阅会话 JSON/).fill('{"refresh":"synthetic-original"}');
    await auth.getByRole("button", { name: "导入订阅会话", exact: true }).click();
    await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
    await editor.getByRole("button", { name: "下一步", exact: true }).click();
    const directory = editor.locator(".provider-model-list");
    const toolbar = editor.locator(".provider-model-tools");
    const search = toolbar.getByPlaceholder("搜索模型、能力、参数");
    await expect(directory.getByRole("checkbox")).toHaveCount(3);
    await expect(directory.locator("input:checked")).toHaveCount(0);
    await toolbar.getByRole("button", { name: "全选", exact: true }).click();
    await expect(directory.locator("input:checked")).toHaveCount(3);
    await expect(toolbar.getByRole("button", { name: "取消全选", exact: true })).toBeEnabled();
    await expect.poll(() => editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await expect.poll(() => toolbar.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await toolbar.scrollIntoViewIfNeeded();
    await capture(page, testInfo, directory, `subscription-model-select-all-${viewport}`, "订阅模型：全选目录中的三个模型", "viewport");
    await toolbar.getByRole("button", { name: "取消全选", exact: true }).click();
    await expect(directory.locator("input:checked")).toHaveCount(0);
    await directory.getByRole("checkbox", { name: /UI Vision/ }).check();
    await search.fill("assistant");
    await expect(directory.getByRole("checkbox")).toHaveCount(2);
    await toolbar.getByRole("button", { name: "全选筛选结果", exact: true }).click();
    await expect(directory.locator("input:checked")).toHaveCount(2);
    await expect(toolbar.getByRole("button", { name: "取消全选筛选结果", exact: true })).toBeEnabled();
    await expect.poll(() => toolbar.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await toolbar.scrollIntoViewIfNeeded();
    await capture(page, testInfo, directory, `subscription-model-select-filtered-${viewport}`, "订阅模型：全选筛选结果并保留隐藏选择", "viewport");
    await toolbar.getByRole("button", { name: "取消全选筛选结果", exact: true }).click();
    await expect(directory.locator("input:checked")).toHaveCount(0);
    await search.fill("missing-model");
    await expect(directory.getByText("没有匹配的模型", { exact: true })).toBeVisible();
    await expect(toolbar.getByRole("button", { name: "全选筛选结果", exact: true })).toBeDisabled();
    await expect.poll(() => editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await toolbar.scrollIntoViewIfNeeded();
    await capture(page, testInfo, directory, `subscription-model-select-empty-${viewport}`, "订阅模型：空筛选结果禁用全选", "viewport");
    await search.clear();
    await expect(directory.getByRole("checkbox")).toHaveCount(3);
    await expect(directory.getByRole("checkbox", { name: /UI Vision/ })).toBeChecked();
    await expect(directory.locator("input:checked")).toHaveCount(1);
    await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
    await expect(editor).toHaveCount(0);
    expect(api.calls.filter(call => call.path === "/api/admin/providers" && call.method === "POST")).toHaveLength(1);
  });

  test(`subscription browser authorization fills credentials automatically ${viewport}`, async ({ page, api }, testInfo) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => { window.open = () => null; });
    const authorize = install(api, "auto");
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Subscription/ }).click();
    const auth = editor.locator("div.provider-account-auth-wide");
    await auth.getByRole("button", { name: "打开授权", exact: true }).click();
    await expect(auth.getByText("请在供应商网页完成登录后返回这里。TokenHub 会自动回填账号凭据，无需粘贴授权码。回填后请保存账号资源。", { exact: true })).toBeVisible();
    await expect(auth.getByLabel("回调结果", { exact: true })).toHaveCount(0);
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.exchange")).length).toBeGreaterThan(0);
    await auth.scrollIntoViewIfNeeded();
    await capture(page, testInfo, auth, `subscription-browser-pending-${viewport}`, "订阅授权：网页登录后自动回填，无需粘贴", "viewport");
    authorize();
    await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
    await expect(auth.getByText("账号凭据已回填，请保存账号资源以完成接入。", { exact: true })).toBeVisible();
    await capture(page, testInfo, auth, `subscription-browser-complete-${viewport}`, "订阅授权：凭据已回填，提示保存账号资源", "viewport");
    await editor.getByRole("button", { name: "下一步", exact: true }).click();
    await editor.getByRole("checkbox", { name: /UI Chat/ }).check();
    await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
    await expect(editor).toHaveCount(0);
    expect(api.calls.filter(call => call.path === "/api/admin/provider-resources" && call.method === "POST")).toHaveLength(1);
  });

  test(`subscription device authorization and cancellation ${viewport}`, async ({ page, api }, testInfo) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => { window.open = () => null; });
    install(api, true);
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Subscription/ }).click();
    await editor.getByRole("button", { name: "打开授权", exact: true }).click();
    await expect(editor.getByText("ABCD-EFGH", { exact: true })).toBeVisible();
    const auth = editor.locator("div.provider-account-auth-wide");
    await expect.poll(() => auth.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await auth.scrollIntoViewIfNeeded();
    await capture(page, testInfo, auth, `subscription-device-${viewport}`, "订阅授权：设备码与取消操作", "viewport");
    await auth.getByRole("button", { name: "取消", exact: true }).click();
    await expect(editor.getByText("ABCD-EFGH", { exact: true })).toHaveCount(0);
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.cancel")).length).toBe(1);
  });

  test(`subscription failed polling cancels the remote session ${viewport}`, async ({ page, api }, testInfo) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => { window.open = () => null; });
    install(api, true, true);
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Subscription/ }).click();
    const auth = editor.locator("div.provider-account-auth-wide");
    await auth.getByRole("button", { name: "打开授权", exact: true }).click();
    await expect(auth.getByRole("alert")).toContainText("Synthetic authorization polling failure");
    await expect(auth.getByText("ABCD-EFGH", { exact: true })).toHaveCount(0);
    await expect(auth.getByRole("button", { name: "打开授权", exact: true })).toBeEnabled();
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.cancel")).length).toBe(1);
    await auth.scrollIntoViewIfNeeded();
    await capture(page, testInfo, auth, `subscription-poll-failure-${viewport}`, "订阅授权：检查失败后释放会话并允许重试", "viewport");
    await auth.getByRole("button", { name: "打开授权", exact: true }).click();
    await expect(auth.getByText("ABCD-EFGH", { exact: true })).toBeVisible();
    await auth.getByRole("button", { name: "取消", exact: true }).click();
    await expect.poll(() => api.calls.filter(call => call.path.endsWith("oauth.cancel")).length).toBe(2);
  });

  for (const mode of ["session", "token"] as const) test(`subscription ${mode} import saves credentials and models without a base URL ${viewport}`, async ({ page, api }, testInfo) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => { window.open = () => null; });
    install(api, mode === "token" ? "token" : false);
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Subscription/ }).click();
    const auth = editor.locator("div.provider-account-auth-wide");
    if (mode === "token") {
      await auth.getByLabel("OAuth client ID", { exact: true }).fill("synthetic-client");
      await auth.getByLabel("OAuth client secret", { exact: true }).fill("synthetic-client-secret");
      await expect(auth.getByLabel("OAuth client secret", { exact: true })).toHaveAttribute("type", "password");
      await auth.getByRole("button", { name: "打开授权", exact: true }).click();
    } else {
      await auth.locator("summary").filter({ hasText: "导入订阅会话" }).click();
      await auth.getByLabel(/^订阅会话 JSON/).fill('{"refresh":"synthetic-original"}');
      await auth.getByRole("button", { name: "导入订阅会话", exact: true }).click();
    }
    await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
    if (mode === "session") await expect(auth.getByLabel(/^订阅会话 JSON/)).toHaveValue("");
    await auth.scrollIntoViewIfNeeded();
    await capture(page, testInfo, auth, `subscription-${mode}-import-${viewport}`, "订阅授权：会话导入完成", "viewport");
    await editor.getByRole("button", { name: "下一步", exact: true }).click();
    await editor.getByRole("checkbox", { name: /UI Chat/ }).check();
    await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
    await expect(editor).toHaveCount(0);
    expect(api.calls.filter(call => call.path === "/api/admin/provider-resources" && call.method === "POST")).toHaveLength(1);
    expect(api.calls.filter(call => call.path.includes("provider-catalog/") && call.method === "POST")).toHaveLength(0);
  });
}
