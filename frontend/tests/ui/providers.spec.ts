import type { Provider } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";

test("providers local-provider-labels", async ({ page, api }, testInfo) => {
  const providers: Provider[] = [
    { id: "prv_ui_local", name: "UI Local Cluster", type: "mock", base_url: "", priority: 1, status: "active", healthy: true },
    { id: "prv_ui_internal", name: "UI Internal Cluster", type: "mock", base_url: "http://inference.example.test/v1", priority: 2, status: "active", healthy: true },
  ];
  api.respond("GET", "/api/admin/providers", { data: providers });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "local", name: "Mock Catalog", display_name: "Mock Provider", type: "mock", models_count: 0, source: "plugin" }] });
  api.respond("GET", "/api/admin/provider-catalog/local", { data: { id: "local", name: "Local Cluster", type: "mock", models_count: 0, models: [], source: "plugin" } });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "mock", capabilities: ["chat"], plugin_id: "tokenhub.provider.mock" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.mock", name: "Mock Plugin", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "mock" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.define("GET", "/api/admin/audit/requests", () => ({ json: {
    data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
    summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
  } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });

  await page.goto("/providers");
  const local = page.getByRole("row").filter({ hasText: "UI Local Cluster" });
  const internal = page.getByRole("row").filter({ hasText: "UI Internal Cluster" });
  await expect(local.getByText("本地服务 · 本地服务", { exact: true })).toBeVisible();
  await expect(internal.getByText("本地服务 · http://inference.example.test/v1", { exact: true })).toBeVisible();
  await expect(page.locator(".provider-channel-list")).not.toContainText(/mock/i);
  await capture(page, testInfo, page.locator(".provider-channel-list"), "providers-local-labels", "本地服务类型与地址显示");

  const providerReads = () => api.calls.filter(call => call.path === "/api/admin/providers").length;
  const initialProviderReads = providerReads();
  for (const locale of [
    { language: "en", option: "English", label: "Local Provider", edit: "Edit", advanced: "Advanced", type: "Provider Type", close: "Close" },
    { language: "ja", option: "日本語", label: "ローカルサービス", edit: "編集", advanced: "詳細", type: "Provider タイプ", close: "閉じる" },
    { language: "zh-CN", option: "简体中文", label: "本地服务", edit: "编辑", advanced: "高级", type: "渠道商类型", close: "关闭" },
  ]) {
    await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
    await page.getByRole("option", { name: locale.option, exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    await expect(local.getByText(`${locale.label} · ${locale.label}`, { exact: true })).toBeVisible();
    await local.getByRole("button", { name: locale.edit, exact: true }).click();
    const editor = page.locator(".provider-modal");
    await editor.getByRole("tab", { name: locale.advanced, exact: true }).click();
    const providerType = editor.getByRole("combobox", { name: locale.type, exact: true });
    await expect(providerType).toHaveValue("mock");
    await expect(providerType.locator("option:checked")).toHaveText(locale.label);
    await capture(page, testInfo, editor, `providers-local-labels-${locale.language}`, "切换语言后的本地服务类型");
    await editor.getByTitle(locale.close, { exact: true }).click();
    expect(providerReads(), "Language changes must not reload provider data").toBe(initialProviderReads);
  }
});
