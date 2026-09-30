import type { AdminResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";

for (const scenario of ["default", "saved", "invalid"] as const) {
  test(`settings quota-timezone-${scenario}`, async ({ page, api }, testInfo) => {
    let setting: AdminResource = { id: "cfg_gateway", kind: "settings", name: "Gateway Base Settings", status: "active", fields: { provider_egress_mode: "inherit_environment", dashboard_timezone: "UTC", ...(scenario === "saved" ? { quota_timezone: "Asia/Shanghai" } : {}) } };
    api.define("GET", "/api/admin/resources/settings", () => ({ json: { data: [setting] } }));
    for (const path of ["providers", "provider-adapters", "plugins", "plugin-ui-manifest", "plugin-actions", "plugin-background-jobs", "resources/role-configs", "resources/identity-providers"]) {
      api.respond("GET", `/api/admin/${path}`, { data: [] });
    }
    api.respond("GET", "/api/admin/plugin-chain", { data: { hooks: [] } });
    api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
    api.define("PATCH", "/api/admin/resources/settings/cfg_gateway", input => {
      expect(input.body).toMatchObject({ fields: { quota_timezone: scenario === "invalid" ? "Nope/Nowhere" : "America/New_York", dashboard_timezone: "UTC" } });
      if (scenario === "invalid") return { status: 400, json: { error: { code: "invalid_quota_timezone", message: "quota_timezone must be a valid IANA timezone" } } };
      const updated = input.body as AdminResource;
      setting = { ...setting, fields: updated.fields };
      return { json: setting };
    });
    await page.goto("/settings");
    await page.getByRole("button", { name: "编辑配置", exact: true }).click();
    const timezone = page.getByRole("textbox", { name: /^配额时区/ });
    await expect(timezone).toHaveValue(scenario === "saved" ? "Asia/Shanghai" : "UTC");
    await timezone.scrollIntoViewIfNeeded();
    await capture(page, testInfo, timezone.locator(".."), `quota-timezone-${scenario}-field`, "配额时区及历史用量说明");
    await timezone.fill(scenario === "invalid" ? "Nope/Nowhere" : "America/New_York");
    await page.getByRole("button", { name: "保存", exact: true }).click();
    if (scenario === "invalid") {
      await expect(page.getByText("quota_timezone must be a valid IANA timezone", { exact: true })).toBeVisible();
      await expect(timezone).toHaveValue("Nope/Nowhere");
    } else {
      await expect(timezone).toHaveCount(0);
      const card = page.locator(".system-setting-item").filter({ has: page.getByText("配额时区", { exact: true }) });
      await expect(card).toContainText("America/New_York");
      await capture(page, testInfo, card, `quota-timezone-${scenario}-saved`, "配额时区保存成功");
    }
  });
}
