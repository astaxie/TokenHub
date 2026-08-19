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
  await expect(page.getByRole("heading", { name: "登录控制台" })).toBeVisible();
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

test("admin can issue a one-time API Key", async ({ page }) => {
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
});
