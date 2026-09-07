import { expect, test } from "@playwright/test";
import e2eDefaults from "./config.cjs";

test("admin can distinguish missing and explicitly zero usage evidence", async ({ page }) => {
  await page.route("**/api/admin/billing/evidence/review-evidence", route => route.fulfill({ json: { data: [{ kind: "shadow_settlement", at: "2026-09-06T00:00:00Z", data: { attempts: [{ number: 1, attempt_id: "review-attempt", pricing: { usage_evidence: { protocol: "openai", fields: { input_total: { value: 0, state: "reported" }, cache_read: { value: null, state: "missing" }, cache_write_5m: { value: 60, state: "reported" } } } } }] } }] } }));
  await page.goto("/");
  await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity);
  await page.getByLabel("密码", { exact: true }).fill(e2eDefaults.adminPassword);
  await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible();
  await page.goto("/billing");
  await page.getByRole("tab", { name: "调价记录", exact: true }).click();
  await page.getByLabel("请求 ID", { exact: true }).fill("review-evidence");
  await page.getByRole("button", { name: "查询记录", exact: true }).click();
  await page.locator("summary").filter({ hasText: "上游尝试" }).click();
  await expect(page.getByRole("row").filter({ hasText: "总输入 Token" })).toContainText("上游报告");
  await expect(page.getByRole("row").filter({ hasText: "总输入 Token" })).toContainText("0");
  await expect(page.getByRole("row").filter({ hasText: "缓存读取" })).toContainText("未提供");
  await expect(page.getByRole("row").filter({ hasText: "5 分钟缓存写入" })).toContainText("60");
  if (process.env.PRICING_GALLERY_DIR) await page.screenshot({ path: `${process.env.PRICING_GALLERY_DIR}/evidence-desktop.png`, fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  if (process.env.PRICING_GALLERY_DIR) await page.screenshot({ path: `${process.env.PRICING_GALLERY_DIR}/evidence-mobile.png`, fullPage: true });
  await expect.poll(() => page.locator(".billing-page").evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
});
