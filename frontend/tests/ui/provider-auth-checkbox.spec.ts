import type { Provider } from "../../features/admin/core/types";
import type { MockAPI } from "./network";
import { test, expect, capture } from "./harness";

// Regression coverage for the provider editor's "remove saved credential" checkbox.
// The .checkbox-line rule previously inherited .field input { width: 100%; min-height: 44px },
// stretching the checkbox into a full-width block and dropping its label onto the next line.
const provider: Provider = {
  id: "prv_ui_saved_auth",
  name: "UI Saved Auth Provider",
  type: "openai_compatible",
  base_url: "https://auth.example.test/v1",
  priority: 1,
  status: "active",
  healthy: true,
};

function installSavedAuthProvider(api: MockAPI) {
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  api.respond("GET", "/api/admin/provider-catalog/custom", { data: { id: "custom", name: "Custom Provider", type: provider.type, models_count: 0, models: [], source: "builtin" } });
  api.define("POST", "/api/admin/provider-catalog/custom", input => {
    expect(input.body).toMatchObject({ provider_id: provider.id, type: provider.type, base_url: provider.base_url });
    return { json: { data: { id: "custom", name: "Custom Provider", type: provider.type, models_count: 0, models: [], source: "builtin" } } };
  });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: provider.type, capabilities: ["chat"], provider_policy: { api_key_required: false, supports_custom_headers: true } }] });
  api.respond("GET", "/api/admin/plugins", { data: [] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions", "provider-resources"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
}

for (const viewport of [
  { name: "desktop", width: 1440, height: 1000 },
  { name: "mobile", width: 390, height: 844 },
]) {
  test(`provider saved-auth checkbox keeps native size at ${viewport.name} width`, async ({ page, api }, testInfo) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    installSavedAuthProvider(api);
    await page.goto("/providers");
    await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();

    const line = page.locator("label.checkbox-line");
    await expect(line).toBeVisible();
    await expect(line).toContainText("移除已保存的认证密钥");

    const checkbox = line.locator('input[type="checkbox"]');
    const label = line.locator("span").last();
    const box = await checkbox.boundingBox();
    const labelBox = await label.boundingBox();
    expect(box).not.toBeNull();
    expect(labelBox).not.toBeNull();

    // Native checkbox dimensions: before the fix it inherited width: 100% and a 44px min-height.
    expect(box!.width).toBeLessThan(40);
    expect(box!.height).toBeLessThan(40);
    // Same-line placement: the label sits beside the checkbox, not wrapped below it.
    expect(Math.abs(box!.y - labelBox!.y)).toBeLessThan(8);
    expect(box!.width).toBeLessThan(labelBox!.width);

    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await capture(page, testInfo, line, `provider-saved-auth-checkbox-${viewport.name}`, "已保存认证 Provider 的移除密钥复选框保持原生尺寸", "section");
  });
}
