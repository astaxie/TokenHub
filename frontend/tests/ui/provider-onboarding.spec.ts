import type { AdapterDescriptor, PluginDescriptor, Provider, ProviderCatalogEntry } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const direct: ProviderCatalogEntry = { id: "ui-direct", name: "UI Direct Service", display_name: "UI Direct Service", type: "openai_compatible", base_url: "https://api.example.test/v1", categories: ["openai"], models_count: 2, source: "ui-fixture" };
const alternate: ProviderCatalogEntry = { ...direct, id: "ui-alternate", name: "UI Alternate Service", display_name: "UI Alternate Service", type: "ui_alternate" };
const customConnection = { name: "UI Custom Draft", base_url: "https://custom.example.test/v1", type: alternate.type, api_key: "synthetic-ui-key" };
const subscription: ProviderCatalogEntry = { id: "ui-subscription", name: "UI Subscription", display_name: "UI Subscription", type: "ui_subscription", base_url: "https://account.example.test", categories: ["openai"], models_count: 1, source: "plugin" };
const catalogModel = { id: "ui-chat", name: "UI Chat", category: "openai", family: "test", type: "chat", input_price_usd_per_1m: 1, output_price_usd_per_1m: 2 };
const subscriptionPlugin: PluginDescriptor = { id: "tokenhub.provider.ui-subscription", name: "UI Subscription", version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "ui_subscription_account", subject: "ui_subscription" }] };
const adapters: AdapterDescriptor[] = [
  { type: "openai_compatible", capabilities: ["chat"], provider_policy: { supports_custom_headers: true, api_key_required: true, default_catalog_provider_type: true } },
  { type: "ui_subscription", capabilities: ["chat"], provider_policy: { supports_custom_headers: false, credentials_scope: "resource" } },
];

function installProviderFixtures(api: MockAPI, state: string) {
  const custom = state === "custom" || state === "late-connection";
  let releaseConnection: (() => void) | undefined;
  const delayedConnection = state === "late-connection"
    ? new Promise<{ json: { healthy: boolean; latency_ms: number } }>((resolve) => {
      releaseConnection = () => resolve({ json: { healthy: true, latency_ms: 35 } });
    })
    : undefined;
  const providers: Provider[] = state.startsWith("catalog") ? [{ id: "prv_ui_existing", name: "Existing UI Service", type: direct.type, base_url: direct.base_url, priority: 10, healthy: true, status: "active", options: { catalog_id: direct.id } }] : [];
  const extraCatalog = state === "catalog-many" ? Array.from({ length: 8 }, (_, index) => ({ ...direct, id: `ui-catalog-${index}`, name: `UI Service ${index + 2}`, display_name: index === 3 ? "UI Enterprise Service With a Long Regional Deployment Name" : `UI Service ${index + 2}`, base_url: `https://region-${index + 2}.example.test/enterprise/compatible/v1` })) : [];
  api.respond("GET", "/api/admin/providers", { data: providers });
  api.respond("GET", "/api/admin/provider-catalog", { data: [direct, ...extraCatalog, ...(custom ? [alternate] : []), subscription] });
  let catalogFailed = state === "model-failure";
  api.define("GET", "/api/admin/provider-catalog/ui-direct", () => catalogFailed
    ? { status: 503, json: { error: { message: "Synthetic catalog unavailable" } } }
    : { json: { data: { ...direct, models: [catalogModel, { ...catalogModel, id: "ui-reasoning", name: "UI Reasoning" }] } } }, query => {
    expect(query.size === 0 || (query.size === 1 && query.get("refresh") === "true")).toBe(true);
    if (query.get("refresh") === "true") catalogFailed = false;
  });
  const customAdapters: AdapterDescriptor[] = custom ? [{ type: alternate.type, capabilities: ["chat"], provider_policy: { supports_custom_headers: true, api_key_required: true, auth_modes: ["x-api-key", "bearer"] } }] : [];
  api.respond("GET", "/api/admin/provider-adapters", { data: [...adapters, ...customAdapters] });
  if (custom) {
    api.respond("GET", "/api/admin/provider-catalog/ui-alternate", { data: { ...alternate, models: [catalogModel] } });
    api.define("POST", "/api/admin/provider-catalog/custom", input => {
      expect(input.body).toMatchObject(customConnection);
      expect(["x-api-key", "bearer"]).toContain((input.body as { provider_auth_mode: string }).provider_auth_mode);
      return { json: { data: { ...direct, ...customConnection, id: "custom", models_count: 1, models: [catalogModel] } } };
    });
  }
  api.respond("GET", "/api/admin/plugins", { data: [subscriptionPlugin] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.respond("GET", "/api/admin/plugin-actions", { data: [{ plugin_id: subscriptionPlugin.id, action_id: "oauth.start", kind: "external_redirect", capability: "oauth.start", subject: subscription.type, metadata: { oauth_redirect_uri: "https://callback.example.test/oauth" } }] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.define("POST", "/api/admin/providers/test-connection", input => {
    expect(input.body).toMatchObject({ catalog_id: direct.id, base_url: direct.base_url, api_key: "synthetic-ui-key" });
    return delayedConnection ?? (state === "connection-failure" ? { status: 502, json: { error: { message: "Synthetic provider rejected the key" } } } : { json: { healthy: true, latency_ms: 35 } });
  });
  api.define("POST", "/api/admin/providers", input => {
    const connection = custom ? customConnection : direct;
    const catalogID = custom ? "custom" : direct.id;
    expect(input.body).toMatchObject({ catalog_id: catalogID, name: connection.name, type: connection.type, api_key: "synthetic-ui-key", selected_models: [catalogModel.id] });
    if (custom) expect(input.body).toMatchObject({ base_url: customConnection.base_url, system_prompt_transform_policy: "preserve", custom_models: [expect.objectContaining({ id: catalogModel.id })] });
    if (state === "save-failure") return { status: 500, json: { error: { message: "Synthetic provider save failed" } } };
    const provider: Provider = { id: "prv_ui_created", name: connection.name, type: connection.type, base_url: connection.base_url, priority: 10, healthy: true, status: "active", options: { catalog_id: catalogID } };
    api.replaceResponse("GET", "/api/admin/providers", { data: [provider] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: [{ id: "pm_ui_created", provider_id: provider.id, upstream_model: catalogModel.id, status: "active" }] });
    return { status: 201, json: { provider, imported_models: 1 } };
  });
  return () => releaseConnection?.();
}

for (const viewport of ["desktop", "mobile"] as const) {
  test(`provider-onboarding custom-draft ${viewport}`, async ({ page, api }, testInfo) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    installProviderFixtures(api, "custom");
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Alternate Service/ }).click();
    await expect(editor.getByLabel("Base URL", { exact: true })).toHaveValue(alternate.base_url!);
    await editor.getByRole("button", { name: "更换供应商" }).click();
    await editor.getByRole("button", { name: /自定义供应商/ }).click();
    const protocol = editor.getByRole("combobox", { name: "渠道商类型", exact: true });
    await expect(protocol).toBeVisible();
    await expect(protocol).toHaveAttribute("required", "");
    await expect(protocol).toHaveValue(direct.type);
    await expect(editor.locator("details.provider-onboarding-advanced")).not.toHaveAttribute("open", "");
    await protocol.scrollIntoViewIfNeeded();
    await capture(page, testInfo, editor, `provider-onboarding-custom-required-${viewport}`, "自定义供应商：名称、地址与必填协议直接可见", "viewport");

    await protocol.selectOption(alternate.type);
    await editor.getByLabel("渠道名称", { exact: true }).fill(customConnection.name);
    await editor.getByLabel("Base URL", { exact: true }).fill(customConnection.base_url);
    await editor.getByLabel("API Key", { exact: true }).fill(customConnection.api_key);
    await editor.getByText("高级连接设置", { exact: true }).click();
    await editor.getByRole("combobox", { name: /^认证方式/ }).selectOption("bearer");
    await editor.getByRole("combobox", { name: /^系统提示词转换/ }).selectOption("preserve");
    const discoveryCalls = () => api.calls.filter(call => call.method === "POST" && call.path === "/api/admin/provider-catalog/custom");
    await expect.poll(() => discoveryCalls().at(-1)?.body).toMatchObject({ ...customConnection, provider_auth_mode: "bearer" });
    await editor.getByRole("switch", { name: "引入 UI Chat" }).click();
    const loadedCatalogs = discoveryCalls().length;
    await editor.getByRole("button", { name: "更换供应商" }).click();
    await editor.getByRole("button", { name: /自定义供应商/ }).click();
    await expect(protocol).toHaveValue(alternate.type);
    await expect(editor.getByLabel("渠道名称", { exact: true })).toHaveValue(customConnection.name);
    await expect(editor.getByLabel("Base URL", { exact: true })).toHaveValue(customConnection.base_url);
    await expect(editor.getByLabel("API Key", { exact: true })).toHaveValue(customConnection.api_key);
    await expect(editor.getByRole("combobox", { name: /^认证方式/ })).toHaveValue("bearer");
    await expect(editor.getByRole("combobox", { name: /^系统提示词转换/ })).toHaveValue("preserve");
    const selectedModel = editor.getByRole("switch", { name: "移除 UI Chat" });
    await expect(selectedModel).toHaveAttribute("aria-checked", "true");
    await selectedModel.scrollIntoViewIfNeeded();
    await capture(page, testInfo, editor, `provider-onboarding-custom-restored-${viewport}`, "返回同一自定义供应商：保留连接草稿与已选模型", "viewport");
    await editor.getByRole("button", { name: "添加供应商并引入模型" }).click();
    await expect(editor).toHaveCount(0);
    expect(discoveryCalls()).toHaveLength(loadedCatalogs);
    expect(api.calls.filter(call => call.method === "POST" && call.path === "/api/admin/providers")).toHaveLength(1);
    expect(api.calls.some(call => call.method === "POST" && /\/(models|routing-rules)$/.test(call.path))).toBe(false);
  });
}

test("provider-onboarding delayed connection result does not clear the next provider draft", async ({ page, api }, testInfo) => {
  const releaseConnection = installProviderFixtures(api, "late-connection");
  try {
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await editor.getByRole("button", { name: /UI Direct Service/ }).click();
    await editor.getByLabel("API Key", { exact: true }).fill("synthetic-ui-key");
    await editor.getByRole("button", { name: "测试连接" }).click();
    await expect.poll(() => api.calls.filter(call => call.method === "POST" && call.path === "/api/admin/providers/test-connection").length).toBe(1);

    await editor.getByRole("button", { name: "更换供应商" }).click();
    await editor.getByRole("button", { name: /UI Alternate Service/ }).click();
    const alternateModel = editor.getByRole("switch", { name: "引入 UI Chat" });
    await alternateModel.click();
    const selectedAlternateModel = editor.getByRole("switch", { name: "移除 UI Chat" });
    await expect(selectedAlternateModel).toHaveAttribute("aria-checked", "true");
    const alternateCatalogRequests = api.calls.filter(call => call.method === "GET" && call.path === "/api/admin/provider-catalog/ui-alternate").length;

    const connectionResponse = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/admin/providers/test-connection");
    releaseConnection();
    expect(await (await connectionResponse).finished()).toBeNull();
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
    await expect(selectedAlternateModel).toHaveAttribute("aria-checked", "true");
    await expect.poll(() => api.calls.filter(call => call.method === "GET" && call.path === "/api/admin/provider-catalog/ui-alternate").length).toBe(alternateCatalogRequests);
    await capture(page, testInfo, editor, "provider-onboarding-delayed-test-selected", "迟到连接结果不清空新供应商模型草稿", "viewport");
  } finally {
    releaseConnection();
  }
});

for (const state of ["catalog", "catalog-many", "api-complete", "connection-failure", "model-failure", "save-failure", "account", "mobile"] as const) {
  test(`provider-onboarding ${state}`, async ({ page, api }, testInfo) => {
    if (state === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    installProviderFixtures(api, state);
    await page.goto("/providers");
    await page.getByRole("button", { name: "添加供应商", exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await expect(editor.getByRole("heading", { name: "选择供应商" })).toBeVisible();
    await expect(editor.getByRole("button", { name: /UI Subscription/ })).toBeVisible();
    if (state === "catalog-many") {
      await expect(editor.locator(".provider-onboarding-card")).toHaveCount(11);
      await expect(editor.getByRole("button", { name: /UI Enterprise Service With a Long Regional Deployment Name/ })).toBeVisible();
      await capture(page, testInfo, editor, "provider-onboarding-catalog-many", "多供应商目录：分组卡片、长名称与长地址");
      return;
    }
    if (state === "catalog") {
      const card = editor.getByRole("button", { name: /UI Direct Service/ });
      await expect(card).toContainText("已接入");
      await expect(card).toBeEnabled();
      await capture(page, testInfo, editor, "provider-onboarding-catalog", "供应商卡片：API 与账号同屏，已接入服务仍可添加");
      await editor.getByPlaceholder("搜索供应商名称或地址").fill("no-such-provider");
      await expect(editor.getByText("没有匹配的供应商，可使用自定义接入。")).toBeVisible();
      await expect(editor.getByRole("button", { name: /自定义供应商/ })).toBeEnabled();
      return;
    }
    if (state === "account") {
      await editor.getByRole("button", { name: /UI Subscription/ }).click();
      await expect(editor.getByRole("button", { name: "打开授权" })).toBeVisible();
      await expect(editor.locator('input[value="https://callback.example.test/oauth"]')).toBeVisible();
      await expect(editor.locator("details.provider-onboarding-advanced")).not.toHaveAttribute("open", "");
      await capture(page, testInfo, editor, "provider-onboarding-account", "选择账号服务后直接授权，通道设置按需展开", "viewport");
      await editor.getByText("高级连接设置", { exact: true }).click();
      await expect(editor.getByLabel("通道名称", { exact: true })).toHaveValue(subscription.name);
      return;
    }
    await editor.getByRole("button", { name: /UI Direct Service/ }).click();
    await expect(editor.getByRole("tab")).toHaveCount(0);
    await editor.getByLabel("API Key", { exact: true }).fill("synthetic-ui-key");
    if (state === "model-failure") {
      await expect(editor.locator(".provider-quick-model-list").getByText("provider catalog 503")).toBeVisible();
      await capture(page, testInfo, editor, "provider-onboarding-catalog-error", "模型加载失败时保留连接信息并允许重试", "viewport");
      await editor.getByRole("button", { name: "重新加载" }).click();
      await expect(editor.getByRole("switch", { name: "引入 UI Chat" })).toBeVisible();
      await expect(editor.getByRole("alert")).toHaveCount(0);
      await expect(editor.getByLabel("API Key", { exact: true })).toHaveValue("synthetic-ui-key");
      return;
    }
    await editor.getByRole("button", { name: "测试连接" }).click();
    if (state === "connection-failure") {
      await expect(editor.getByRole("status")).toContainText("Synthetic provider rejected the key");
      await expect(editor.getByLabel("API Key", { exact: true })).toHaveValue("synthetic-ui-key");
      await capture(page, testInfo, editor, "provider-onboarding-connection-error", "连接失败时保留密钥和配置", "viewport");
      return;
    }
    await expect(editor.getByRole("status")).toContainText("API Key 配置有效");
    const modelSwitch = editor.getByRole("switch", { name: "引入 UI Chat" });
    await modelSwitch.click();
    await expect(editor.getByRole("switch", { name: "移除 UI Chat" })).toHaveAttribute("aria-checked", "true");
    if (state === "api-complete" || state === "mobile") {
      await editor.getByRole("button", { name: "测试连接" }).click();
      await expect(editor.getByRole("status")).toContainText("API Key 配置有效");
      await expect(editor.getByRole("switch", { name: "移除 UI Chat" })).toHaveAttribute("aria-checked", "true");
    }
    await capture(page, testInfo, editor, `provider-onboarding-${state === "save-failure" ? "save-review" : state}`, "填写密钥与选择模型在同一页面", "viewport");
    if (state === "mobile") {
      await editor.getByRole("button", { name: "添加供应商并引入模型" }).scrollIntoViewIfNeeded();
      await capture(page, testInfo, editor, "provider-onboarding-mobile-save", "移动端：模型选择与保存按钮", "viewport");
    }
    await editor.getByRole("button", { name: "添加供应商并引入模型" }).click();
    if (state === "save-failure") {
      await expect(editor.getByRole("alert")).toHaveText("Synthetic provider save failed");
      await expect(editor.getByLabel("API Key", { exact: true })).toHaveValue("synthetic-ui-key");
      await expect(editor.getByRole("switch", { name: "移除 UI Chat" })).toHaveAttribute("aria-checked", "true");
      await capture(page, testInfo, editor.getByRole("alert"), "provider-onboarding-save-failure", "供应商保存失败：弹窗内显示原因并保留草稿");
      return;
    }
    await expect(editor).toHaveCount(0);
    expect(api.calls.some(call => call.method === "POST" && /\/(models|routing-rules)$/.test(call.path))).toBe(false);
    expect(api.calls.filter(call => call.method === "POST" && call.path === "/api/admin/providers")).toHaveLength(1);
  });
}
