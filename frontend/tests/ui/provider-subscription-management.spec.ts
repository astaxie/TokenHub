import type { AdminUIContribution, PluginActionDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installManagement(api: MockAPI, kind: "qoder" | "factory", failCheckin = false, balanceOnly = false) {
  const type = `${kind}_subscription`, pluginID = `tokenhub.provider.${kind}-subscription`;
  const provider: Provider = { id: "prv_ui_subscription", name: `UI ${kind} subscription`, type, base_url: "", priority: 1, healthy: true, status: "active", options: { catalog_id: kind, ...(kind === "factory" ? { standard_only: "false" } : {}) } };
  const catalog: ProviderCatalogEntry = { id: kind, name: provider.name, display_name: provider.name, type, source: "ui-fixture", models_count: 0, models: [] };
  const resource: ProviderResource = { id: "account_ui", provider_id: provider.id, name: "UI Subscription Account", resource_type: `${kind}_account`, status: "active", healthy: true, priority: 1, weight: 100, credential_summary: { account_email: "synthetic@example.test", auth_type: "oauth" } };
  const actions: PluginActionDescriptor[] = ["quota.read", ...(kind === "qoder" ? ["checkin.run"] : [])].map(capability => ({ plugin_id: pluginID, action_id: `${type}.${capability}`, capability, subject: type, kind: capability === "quota.read" ? "read" : "mutate", metadata: { provider_resource_type: resource.resource_type }, input_schema: { type: "object", required: ["resource_id"], properties: { resource_id: { type: "string" }, ...(capability === "quota.read" ? { refresh: { type: "boolean" } } : {}) } } }));
  const panels: AdminUIContribution[] = [{ plugin_id: pluginID, id: "subscription-quota", slot: "provider.resource.panel", title: "Subscription account quota", provider_types: [type], resource_types: [resource.resource_type], action: `${type}.quota.read`, schema: { description: "Account allowance, balance, and reset windows reported by the subscription provider." } }];
  if (kind === "qoder") panels.push({ plugin_id: pluginID, id: "subscription-checkin", slot: "provider.resource.panel", title: "Daily check-in", provider_types: [type], resource_types: [resource.resource_type], action: `${type}.checkin.run` });
  else panels.push({ plugin_id: pluginID, id: "subscription-options", slot: "provider.form.section", title: "Subscription account options", provider_types: [type], schema: { placement: "advanced", fields: [{ name: "standard_only", type: "switch", default: false, label: "Use Standard allowance only", help: "Stop using this account when any Standard allowance window is exhausted. Extra usage will not keep the account in routing." }] } });
  api.define("GET", "/api/admin/providers", () => ({ json: { data: [provider] } }));
  api.respond("GET", "/api/admin/provider-resources", { data: [resource] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.define("GET", `/api/admin/provider-catalog/${kind}`, () => ({ json: { data: catalog } }), query => {
    expect([{}, { resource_id: resource.id }]).toContainEqual(Object.fromEntries(query));
  });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type, plugin_id: pluginID, capabilities: ["chat", "quota", "oauth"], provider_policy: { credentials_scope: "resource", api_key_required: false, supports_custom_headers: false } }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: pluginID, name: provider.name, version: "1", source: "built_in", kinds: ["provider", "admin_ui"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resource.resource_type, subject: type }] }] });
  api.respond("GET", "/api/admin/plugin-actions", { data: actions });
  api.respond("GET", "/api/admin/plugin-ui-manifest", { data: panels });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["routing-rules", "audit/events", "providers/monitoring"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.define("POST", `/api/admin/plugins/${pluginID}/actions/${type}.quota.read`, input => {
    expect(input.body).toMatchObject({ provider_id: provider.id, resource_id: resource.id });
    if (balanceOnly) return { json: { data: { status_label: "Quota reported", metrics: [{ label: "Balance", value: "1250" }], fetched_at: 1792929600 } } };
    return { json: { data: { plan_type: "Synthetic Pro", allowed: true, limit_reached: false, used_percent: 35, remaining_percent: 65, windows: [{ label: "5 hours", used_percent: 35, reset_at: 1792936800 }], additional_rate_limits: [{ limit_name: "Core allowance", metered_feature: "synthetic-core-model", rate_limit: { allowed: false, limit_reached: true, primary_window: { used_percent: 100, reset_at: 1792936800 } } }], metrics: [{ label: "Credits", value: "1250" }], fetched_at: 1792929600 } } };
  });
  api.define("POST", `/api/admin/plugins/${pluginID}/actions/${type}.checkin.run`, input => {
    expect(input.body).toEqual({ resource_id: resource.id });
    if (failCheckin) { failCheckin = false; return { status: 503, json: { error: { message: "Synthetic check-in failure" } } }; }
    return { json: { data: { outcome: "claimed", credit: 50, streak: 3 } } };
  });
  api.define("PATCH", `/api/admin/providers/${provider.id}`, input => {
    expect(input.body).toMatchObject({ options: { standard_only: "true" } });
    provider.options = { ...provider.options, standard_only: "true" };
    return { json: { provider, imported_models: 0 } };
  });
  return provider;
}

for (const mobile of [false, true]) {
  test(`subscription management balance-only quota ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const provider = installManagement(api, "factory", false, true);
    await page.goto("/providers");
    await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
    const editor = page.locator(".provider-modal");
    await editor.getByRole("tab", { name: "高级", exact: true }).click();
    const quota = editor.locator("section.provider-quota-panel").filter({ has: page.getByRole("heading", { name: "订阅账号额度", exact: true }) });
    await expect(quota.getByText("1250", { exact: true })).toBeVisible();
    await expect(quota.getByText("已读取额度", { exact: true })).toBeVisible();
    await expect(quota.getByRole("progressbar")).toHaveCount(0);
    await expect(quota.locator(".provider-quota-usage strong")).toHaveText("-");
    await quota.evaluate(el => el.scrollIntoView({ block: "center" }));
    await capture(page, testInfo, quota, `subscription-balance-only-${mobile ? "mobile" : "desktop"}`, "订阅账号：仅返回余额时使用率未知", "viewport");
  });

  test(`subscription management quota and check-in ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const provider = installManagement(api, "qoder", true);
    await page.goto("/providers");
    await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
    const editor = page.locator(".provider-modal");
    await editor.getByRole("tab", { name: "高级", exact: true }).click();
    const checkin = editor.locator("article.provider-quota-card").filter({ hasText: "每日签到" });
    await checkin.getByRole("button", { name: "签到", exact: true }).click();
    await expect(checkin.getByRole("alert")).toHaveText("Synthetic check-in failure");
    await checkin.evaluate(el => el.scrollIntoView({ block: "center" }));
    await capture(page, testInfo, checkin, `subscription-checkin-error-${mobile ? "mobile" : "desktop"}`, "订阅签到：失败后可重试", "viewport");
    await checkin.getByRole("button", { name: "签到", exact: true }).click();
    await expect(checkin.getByRole("status")).toContainText("签到成功");
    await expect(checkin.getByRole("status")).toContainText("签到奖励：50");
    await expect(checkin.locator("input")).toHaveCount(0);
    await expect.poll(() => checkin.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await expect.poll(() => checkin.locator(".provider-quota-account").evaluate(el => el.clientWidth)).toBeGreaterThan(160);
    await checkin.evaluate(el => el.scrollIntoView({ block: "center" }));
    await capture(page, testInfo, checkin, `subscription-checkin-${mobile ? "mobile" : "desktop"}`, "订阅签到：已领取奖励", "viewport");
    const quota = editor.locator("section.provider-quota-panel").filter({ has: page.getByRole("heading", { name: "订阅账号额度", exact: true }) });
    await expect(quota.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "35");
    await quota.scrollIntoViewIfNeeded();
    await capture(page, testInfo, quota, `subscription-quota-${mobile ? "mobile" : "desktop"}`, "订阅账号：额度与重置时间", "viewport");
    await quota.getByText("账号与更多用量详情", { exact: true }).click();
    await expect(quota.getByText("Core allowance", { exact: true })).toBeVisible();
    await expect(quota.getByText("synthetic-core-model", { exact: true })).toBeVisible();
    await quota.locator(".provider-quota-windows").evaluate(el => el.scrollIntoView({ block: "center" }));
    await capture(page, testInfo, quota, `subscription-quota-models-${mobile ? "mobile" : "desktop"}`, "订阅账号：按模型显示独立额度", "viewport");
  });

  test(`subscription management saves provider options ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const provider = installManagement(api, "factory");
    await page.goto("/providers");
    await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
    const editor = page.locator(".provider-modal");
    await editor.getByRole("tab", { name: "高级", exact: true }).click();
    const options = editor.locator("section.provider-edit-section").filter({ has: page.getByRole("heading", { name: "订阅账号选项", exact: true }) });
    await options.getByRole("radio").first().click();
    await expect(options.getByRole("radio").first()).toHaveAttribute("aria-checked", "true");
    await options.scrollIntoViewIfNeeded();
    await options.evaluate(el => el.scrollIntoView({ block: "center" }));
    await capture(page, testInfo, options, `subscription-options-${mobile ? "mobile" : "desktop"}`, "订阅选项：仅使用 Standard 额度", "viewport");
    await editor.getByRole("button", { name: "保存", exact: true }).click();
    await expect(editor).toHaveCount(0);
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
  });
}
