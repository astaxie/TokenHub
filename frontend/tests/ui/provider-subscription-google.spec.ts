import type { PluginActionDescriptor, ProviderCatalogEntry } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const providers = [
  { id: "antigravity", type: "antigravity_subscription", resourceType: "antigravity_account", name: "Antigravity" },
  { id: "gemini-cli", type: "gemini_cli_subscription", resourceType: "gemini_cli_account", name: "Gemini CLI" },
] as const;
const labels = { client: "Google OAuth 客户端 ID", secret: "Google OAuth 客户端密钥", project: "Google Cloud 项目 ID（可选，部分账号需要）" };

function installGoogleSubscription(api: MockAPI, provider: typeof providers[number], project = "") {
  const model = { id: "ui-google-chat", name: "UI Google Chat", category: "openai", type: "chat" };
  const catalog: ProviderCatalogEntry = { id: provider.id, name: provider.name, display_name: provider.name, type: provider.type, base_url: "", models_count: 0, source: "ui-fixture", models: [] };
  const authorizedCatalog = { ...catalog, models_count: 1, models: [model] };
  const pluginID = "test.google-subscription";
  const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: pluginID, action_id: capability, capability, subject: provider.type, kind: "mutate", metadata: { provider_resource_type: provider.resourceType, subscription_auth: "true", subscription_session_json: "false" } }));
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.respond("GET", `/api/admin/provider-catalog/${provider.id}`, { data: catalog });
  api.respond("GET", "/api/admin/providers", { data: [] });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: provider.type, capabilities: ["chat", "oauth"], provider_policy: { credentials_scope: "resource", supports_custom_headers: false, api_key_required: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: pluginID, name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: provider.resourceType, subject: provider.type }] }] });
  api.respond("GET", "/api/admin/plugin-actions", { data: actions });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.respond("POST", `/api/admin/provider-actions/${provider.type}/oauth.methods`, { data: { methods: [
    { id: "google", label: "Google account", type: "oauth" },
    { id: "google_project", label: "Google account with Cloud project (advanced)", type: "oauth", prompts: [{ key: "organization_id", label: "Google Cloud project ID (optional; required for some accounts)", type: "text" }] },
    { id: "oauth_client", label: "Custom Google OAuth client (advanced)", type: "oauth", prompts: [
      { key: "client_id", label: "Google OAuth client ID", type: "text" },
      { key: "client_secret", label: "Google OAuth client secret", type: "password" },
      { key: "organization_id", label: "Google Cloud project ID (optional; required for some accounts)", type: "text" },
    ] },
  ] } });
  api.define("POST", `/api/admin/provider-actions/${provider.type}/oauth.start`, input => {
    expect(input.body).toEqual({ resource_type: provider.resourceType, method_id: project ? "google_project" : "google", inputs: project ? { organization_id: project } : {} });
    return { json: { data: { session_id: "synthetic-google-session", state: "synthetic-state", flow: "authorization_code", auth_url: "https://auth.example.test/google" } } };
  });
  api.define("POST", `/api/admin/provider-actions/${provider.type}/oauth.exchange`, input => {
    expect(input.body).toEqual({ resource_type: provider.resourceType, session_id: "synthetic-google-session", state: "synthetic-state", code: "synthetic-code", callback_url: "synthetic-code" });
    return { json: { data: { access_token: "synthetic-google-access", refresh_token: "synthetic-google-refresh", client_id: "synthetic-default-client", client_secret: "synthetic-default-secret", ...(project ? { organization_id: project } : {}), catalog: authorizedCatalog } } };
  });
  api.define("POST", "/api/admin/providers", input => {
    expect(input.body).toMatchObject({ type: provider.type, selected_models: [model.id], custom_models: [model], base_url: "" });
    return { status: 201, json: { provider: { id: "prv_ui_google", name: provider.name, type: provider.type, priority: 1, healthy: true, status: "active" }, imported_models: 1 } };
  });
  api.define("POST", "/api/admin/provider-resources", input => {
    expect(input.body).toMatchObject({ provider_id: "prv_ui_google", resource_type: provider.resourceType, credentials: { replace: true, access_token: "synthetic-google-access", refresh_token: "synthetic-google-refresh", client_id: "synthetic-default-client", client_secret: "synthetic-default-secret", ...(project ? { organization_id: project } : {}) } });
    return { status: 201, json: { id: "rsrc_ui_google", provider_id: "prv_ui_google", resource_type: provider.resourceType, name: "Synthetic Google account", healthy: true, status: "active", priority: 1, weight: 100 } };
  });
}

test("subscription Google project-only login gemini-cli desktop", async ({ page, api }, testInfo) => {
  const provider = providers[1];
  await page.addInitScript(() => { window.open = () => null; });
  installGoogleSubscription(api, provider, "synthetic-cloud-project");
  await page.goto("/providers");
  await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
  const editor = page.locator("form.provider-modal");
  await editor.getByRole("button", { name: /Gemini CLI/ }).click();
  const auth = editor.locator("div.provider-account-auth-wide");
  const methods = auth.getByRole("combobox", { name: "登录方式", exact: true });
  await expect(methods).toHaveValue("google");
  await methods.selectOption("google_project");
  await expect(methods.getByRole("option", { name: "Google 账号（指定 Cloud 项目）", exact: true })).toBeAttached();
  await expect(auth.getByLabel(labels.client, { exact: true })).toHaveCount(0);
  await expect(auth.getByLabel(labels.secret, { exact: true })).toHaveCount(0);
  await auth.getByLabel(labels.project, { exact: true }).fill("synthetic-cloud-project");
  await auth.scrollIntoViewIfNeeded();
  await capture(page, testInfo, auth, "google-project-gemini-cli-desktop", "Google 订阅：指定项目时沿用默认 OAuth 客户端", "viewport");
  await auth.getByRole("button", { name: "打开授权", exact: true }).click();
  await auth.getByRole("textbox", { name: /^回调结果/ }).fill("synthetic-code");
  await auth.getByRole("button", { name: "完成授权", exact: true }).click();
  await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
  await editor.getByRole("button", { name: "下一步", exact: true }).click();
  await editor.getByRole("checkbox", { name: /UI Google Chat/ }).check();
  await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
  await expect(editor).toHaveCount(0);
  expect(api.calls.filter(call => call.path.endsWith("oauth.start"))).toHaveLength(1);
  expect(api.calls.filter(call => call.path === "/api/admin/provider-resources" && call.method === "POST")).toHaveLength(1);
});

for (const provider of providers) for (const variant of ["desktop-light", "desktop-dark", "mobile-light"] as const) {
  test(`subscription Google default login ${provider.id} ${variant}`, async ({ page, api }, testInfo) => {
    if (variant.startsWith("mobile")) await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => { window.open = () => null; });
    installGoogleSubscription(api, provider);
    await page.goto("/providers");
    if (variant.endsWith("dark")) await page.getByRole("button", { name: "切换主题", exact: true }).click();
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: new RegExp(provider.name) }).click();
    const auth = editor.locator("div.provider-account-auth-wide");
    const methods = auth.getByRole("combobox", { name: "登录方式", exact: true });
    const authorize = auth.getByRole("button", { name: "打开授权", exact: true });
    await expect(methods).toHaveValue("google");
    await expect(methods.getByRole("option", { name: "Google 账号（默认）", exact: true })).toBeAttached();
    for (const label of Object.values(labels)) await expect(auth.getByLabel(label, { exact: true })).toHaveCount(0);
    await expect(authorize).toBeEnabled();
    await authorize.scrollIntoViewIfNeeded();
    await expect(authorize).toBeInViewport({ ratio: 1 });
    expect((await authorize.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await capture(page, testInfo, auth, `google-default-${provider.id}-${variant}`, "Google 订阅：默认直接打开账号授权", "viewport");

    await methods.selectOption("oauth_client");
    await expect(methods.getByRole("option", { name: "自定义 Google OAuth（高级）", exact: true })).toBeAttached();
    await expect(auth.getByLabel(labels.client, { exact: true })).toHaveAttribute("type", "text");
    await expect(auth.getByLabel(labels.secret, { exact: true })).toHaveAttribute("type", "password");
    await auth.getByLabel(labels.client, { exact: true }).fill("synthetic-custom-client");
    await auth.getByLabel(labels.secret, { exact: true }).fill("synthetic-custom-secret");
    await auth.getByLabel(labels.project, { exact: true }).fill("synthetic-custom-project");
    await auth.scrollIntoViewIfNeeded();
    await capture(page, testInfo, auth, `google-custom-${provider.id}-${variant}`, "Google 订阅：高级模式提供自定义 OAuth 配置", "viewport");
    await methods.selectOption("google");
    for (const label of Object.values(labels)) await expect(auth.getByLabel(label, { exact: true })).toHaveCount(0);
    await methods.selectOption("oauth_client");
    for (const label of Object.values(labels)) await expect(auth.getByLabel(label, { exact: true })).toHaveValue("");
    await methods.selectOption("google");
    await authorize.click();
    await expect(authorize).toBeDisabled();
    await expect(methods).toBeDisabled();
    await auth.getByRole("textbox", { name: /^回调结果/ }).fill("synthetic-code");
    await auth.getByRole("button", { name: "完成授权", exact: true }).click();
    await expect(auth.getByText("已回填账号 Token", { exact: true })).toBeVisible();
    await editor.getByRole("button", { name: "下一步", exact: true }).click();
    await editor.getByRole("checkbox", { name: /UI Google Chat/ }).check();
    await editor.getByRole("button", { name: "添加供应商并引入模型", exact: true }).click();
    await expect(editor).toHaveCount(0);
    expect(api.calls.filter(call => call.path.endsWith("oauth.start"))).toHaveLength(1);
    expect(api.calls.filter(call => call.path === "/api/admin/provider-resources" && call.method === "POST")).toHaveLength(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}
