import { expect, test, type Page } from "@playwright/test";
import e2eDefaults from "./config.cjs";

const adminIdentity = e2eDefaults.adminIdentity;
const adminPassword = process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword;
const upstreamPort = Number(process.env.TOKENHUB_E2E_UPSTREAM_PORT ?? e2eDefaults.upstreamPort);
const upstreamKey = process.env.TOKENHUB_E2E_UPSTREAM_KEY ?? e2eDefaults.upstreamKey;

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("账号 / 邮箱").fill(adminIdentity);
  await page.getByLabel("密码", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible();
}

function sidebar(page: Page) {
  return page.getByRole("complementary").first();
}

test("admin can sign in and sign out of the console", async ({ page }) => {
  await login(page);
  await page.getByTitle("退出登录").click();
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
});

test("admin can validate and create a custom Provider", async ({ page }) => {
  await login(page);
  await sidebar(page).getByRole("button", { name: "Provider 渠道", exact: true }).click();
  await expect(page).toHaveURL(/\/providers$/);
  await page.getByRole("button", { name: "新增 Provider" }).click();

  await expect(page.getByRole("heading", { name: "选择接入方式" })).toBeVisible();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "自定义渠道商" }).click();
  await page.getByLabel("渠道名称").fill("E2E Fake Provider");
  await page.getByLabel("Base URL").fill(`http://127.0.0.1:${upstreamPort}/v1`);
  await page.getByLabel("API Key", { exact: true }).fill(upstreamKey);

  await page.getByRole("button", { name: "测试连接" }).click();
  await expect(page.getByRole("status")).toContainText("API Key 配置有效");
  await page.getByRole("tab", { name: "模型" }).click();
  await expect(page.getByText("e2e-chat-model", { exact: true }).first()).toBeVisible();
  await page.getByRole("switch", { name: "引入 e2e-chat-model" }).click();
  await page.locator("form.provider-modal").getByRole("button", { name: "新增 Provider" }).click();

  await expect(page.getByText("E2E Fake Provider", { exact: true }).first()).toBeVisible();
});

test("admin can issue an API Key and open its usage page", async ({ page }) => {
  await login(page);
  await sidebar(page).getByRole("button", { name: "Key 管理", exact: true }).click();
  await expect(page).toHaveURL(/\/api-keys$/);
  await page.getByRole("button", { name: /创建 API Key|发放 Key/ }).click();
  await page.getByRole("button", { name: /Default Project Space/ }).click();
  await page.getByLabel("归属用户").selectOption({ index: 1 });
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByLabel("Key 名称").fill("E2E Regression Key");
  await page.getByLabel("用途/环境").fill("browser-smoke");
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "生成 Key" }).click();

  const issuedKeyDialog = page.getByRole("dialog", { name: "新 Key 已生成" });
  await expect(issuedKeyDialog).toBeVisible();
  await expect(issuedKeyDialog.getByLabel("完整 Key")).toHaveValue(/^sk_/);
  const closeButton = issuedKeyDialog.getByRole("button", { name: "我已保存，关闭" });
  await expect(closeButton).toBeEnabled({ timeout: 5_000 });
  await closeButton.click();

  const keyRow = page.getByRole("row").filter({ hasText: "E2E Regression Key" });
  await keyRow.getByRole("link", { name: "用量" }).click();
  await expect(page).toHaveURL(/\/api-keys\/[^/]+\/usage$/);
  await expect(page.getByRole("heading", { name: "E2E Regression Key" })).toBeVisible();
  await expect(page.getByText("当前 Key 有效额度", { exact: true })).toBeVisible();
  await expect(page.getByText("所选条件下暂无请求", { exact: true })).toBeVisible();
});

test("admin can inspect plugin details and files without fake settings", async ({ page }) => {
  await login(page);
  await sidebar(page).getByRole("button", { name: "插件管理", exact: true }).click();
  await expect(page).toHaveURL(/\/plugins$/);

  await page.getByRole("button", { name: "查看插件详情 External Trace Hook" }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.extension\.external-trace$/);
  await expect(page.getByRole("heading", { name: "External Trace Hook" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "这个插件做什么" })).toBeVisible();
  await expect(page.getByText("请求处理", { exact: true })).toBeVisible();
  await expect(page.getByText("export", { exact: true })).not.toBeVisible();
  await page.getByText("开发者信息", { exact: true }).click();
  await expect(page.getByText("export", { exact: true })).toBeVisible();
  await expect(page.getByText("告诉 TokenHub 这个插件提供的一项扩展功能。", { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  const overflowingTechnicalElements = await page.locator(".plugin-technical-details").evaluate((details) => [details, ...details.querySelectorAll<HTMLElement>("*")]
    .filter((element) => element.scrollWidth > element.clientWidth + 1)
    .map((element) => `${element.tagName.toLowerCase()}.${element.className}:${element.clientWidth}/${element.scrollWidth}`));
  expect(overflowingTechnicalElements).toEqual([]);

  await page.getByRole("tab", { name: "文件" }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.extension\.external-trace\/files$/);
  await expect(page.getByRole("button", { name: /plugin\.yaml/ })).toBeVisible();
  await page.getByRole("button", { name: /hook\.sh/ }).click();
  await expect(page.locator(".plugin-file-preview pre")).toContainText("#!/bin/sh");

  await expect(page.getByRole("tab", { name: "设置" })).toHaveCount(0);

  await page.getByRole("tab", { name: "文件" }).click();
  const fileListBox = await page.locator(".plugin-file-list").boundingBox();
  const previewBox = await page.locator(".plugin-file-preview").boundingBox();
  expect(fileListBox).not.toBeNull();
  expect(previewBox).not.toBeNull();
  expect(previewBox!.y).toBeGreaterThanOrEqual(fileListBox!.y + fileListBox!.height - 1);
});

test("admin can adjust UI template settings", async ({ page }) => {
  await login(page);
  await sidebar(page).getByRole("button", { name: "插件管理", exact: true }).click();
  await page.getByRole("tab", { name: "扩展类型" }).click();
  await page.getByRole("tab", { name: "界面模板" }).click();
  await page.getByRole("button", { name: "配置界面模板 TokenHub 默认界面模板" }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.sim\.default\/settings$/);
  await expect(page.getByRole("heading", { name: "TokenHub 默认界面模板" })).toBeVisible();
  await page.reload();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.sim\.default\/settings$/);
  await expect(page.getByRole("tab", { name: "TokenHub 默认浅色" })).toBeVisible();
  await page.getByRole("tab", { name: "TokenHub 默认浅色" }).click();
  await page.getByRole("textbox", { name: "主题色 当前值" }).fill("#16a34a");
  await page.getByRole("button", { name: "保存设置" }).click();
  await expect(page.getByRole("status")).toContainText("设置已保存");
  await expect(page.locator(".app-shell")).toHaveAttribute("style", /--accent: #16a34a/);

  await page.getByRole("button", { name: "恢复默认" }).click();
  await expect(page.locator(".app-shell")).toHaveAttribute("style", /--accent: #3e7bf6/);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await expect.poll(() => page.locator(".plugin-setting-row").evaluateAll((rows) => rows.every((row) => row.scrollWidth <= row.clientWidth))).toBe(true);
});
