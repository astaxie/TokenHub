import type { AdminResource } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { user } from "./fixtures/shell";

for (const scenario of ["save", "failed-save"] as const) {
  test(`quota default-user-${scenario}`, async ({ page, api }, testInfo) => {
    let policies: AdminResource[] = [];
    api.respond("GET", "/api/admin/api-keys", { data: [] });
    api.respond("GET", "/api/admin/users", { data: [user] });
    api.respond("GET", "/api/admin/resources/teams", { data: [] });
    api.define("GET", "/api/admin/resources/quota-policies", () => ({ json: { data: policies } }));
    api.define("POST", "/api/admin/resources/quota-policies", (input) => {
      expect(input.body).toMatchObject({ name: "Default monthly allowance", status: "active", fields: { scope: "user", scope_id: "all_users", monthly_cost_usd: 50 } });
      if (scenario === "failed-save") return { status: 400, json: { error: { message: "Policy could not be saved" } } };
      const policy = { ...(input.body as AdminResource), id: "quota_default" };
      policies = [policy];
      return { json: policy };
    });
    api.define("PATCH", "/api/admin/resources/quota-policies/quota_default", (input) => {
      expect(input.body).toMatchObject({ fields: { scope: "user", scope_id: "all_users", monthly_cost_usd: 25 } });
      policies = [{ ...(input.body as AdminResource), id: "quota_default" }];
      return { json: policies[0] };
    });
    await page.goto("/quota-policies");
    await page.getByRole("button", { name: "新增额度策略", exact: true }).click();
    await page.getByRole("textbox", { name: "名称", exact: true }).fill("Default monthly allowance");
    await page.getByRole("combobox", { name: "状态", exact: true }).selectOption("active");
    await page.getByRole("combobox", { name: "作用域", exact: true }).selectOption("user");
    const target = page.getByRole("combobox", { name: /^作用域对象/ });
    await target.selectOption("all_users");
    await page.getByRole("spinbutton", { name: "月成本 USD", exact: true }).fill("50");
    await capture(page, testInfo, target.locator(".."), `default-user-${scenario}-target`, "默认用户额度：独立额度与严格合并说明");
    await page.getByRole("button", { name: "保存", exact: true }).click();
    if (scenario === "failed-save") {
      await expect(page.getByText("Policy could not be saved", { exact: true })).toBeVisible();
      await expect(target).toHaveValue("all_users");
      await expect(page.getByRole("spinbutton", { name: "月成本 USD", exact: true })).toHaveValue("50");
      return;
    }
    const row = page.getByRole("row").filter({ hasText: "Default monthly allowance" });
    await expect(row).toContainText("按用户分别统计");
    await expect(row).toContainText("所有用户（每人独立额度）");
    await row.getByRole("button", { name: "编辑", exact: true }).click();
    await expect(target).toHaveValue("all_users");
    await page.getByRole("spinbutton", { name: "月成本 USD", exact: true }).fill("25");
    await page.getByRole("button", { name: "保存", exact: true }).click();
    await expect(target).toHaveCount(0);
    await expect(row).toContainText("按用户分别统计");
    await capture(page, testInfo, row, "default-user-saved-policy", "默认额度保存后保留每人独立统计语义");
  });
}
