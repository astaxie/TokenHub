import { expect, test } from "@playwright/test";
import e2eDefaults from "./config.cjs";

test("built-in plugin cards follow the console language without reloading", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity);
  await page.getByLabel("密码", { exact: true }).fill(process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword);
  await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible();
  const card = page.locator(".admin-ui-dashboard-card").filter({ hasText: "tokenhub.admin.plugin-ecosystem" });
  await expect(card.getByRole("heading", { name: "插件生态", exact: true })).toBeVisible();
  await expect(card.getByText("已注册插件", { exact: true })).toBeVisible();
  for (const [option, title, label] of [
    ["English", "Plugin Ecosystem", "Registered plugins"],
    ["日本語", "プラグインエコシステム", "登録済みプラグイン"],
    ["简体中文", "插件生态", "已注册插件"],
  ]) {
    await page.locator(".language-select-trigger").click();
    await page.getByRole("option", { name: option, exact: true }).click();
    await expect(card.getByRole("heading", { name: title, exact: true })).toBeVisible();
    await expect(card.getByText(label, { exact: true })).toBeVisible();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await card.screenshot({ path: test.info().outputPath("plugin-localization-mobile.png") });
});
