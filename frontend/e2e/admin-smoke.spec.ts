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

for (const authenticated of [true, false]) {
test(`admin can validate and create a custom Provider with authentication = ${authenticated}`, async ({ page }) => {
  await login(page);
  await sidebar(page).getByRole("button", { name: "Provider 渠道", exact: true }).click();
  await expect(page).toHaveURL(/\/providers$/);
  await page.getByRole("button", { name: "新增 Provider" }).click();

  await expect(page.getByRole("heading", { name: "选择接入方式" })).toBeVisible();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "自定义渠道商" }).click();
  const providerName = authenticated ? "E2E Fake Provider" : "E2E Local Provider";
  await page.getByLabel("渠道名称").fill(providerName);
  await page.getByLabel("Base URL").fill(`http://${authenticated ? "127.0.0.1" : "localhost"}:${upstreamPort}${authenticated ? "" : "/open"}/v1`);
  const credential = page.getByLabel("认证密钥（可选）", { exact: true });
  if (authenticated) await credential.fill(upstreamKey);
  else await expect(credential).toHaveValue("");

  await page.getByRole("button", { name: "测试连接" }).click();
  await expect(page.getByRole("status")).toContainText("连接测试通过");
  await page.getByRole("tab", { name: "模型" }).click();
  await expect(page.getByText("e2e-chat-model", { exact: true }).first()).toBeVisible();
  await page.getByRole("switch", { name: "引入 e2e-chat-model" }).click();
  await page.locator("form.provider-modal").getByRole("button", { name: "新增 Provider" }).click();

  await expect(page.getByText(providerName, { exact: true }).first()).toBeVisible();
});
}

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

  const pluginSearch = page.getByRole("searchbox", { name: "搜索插件" });
  await pluginSearch.fill("TokenHub Default Interface Template");
  const configurableRow = page.locator(".plugin-installed-row").filter({
    has: page.getByRole("button", { name: "设置", exact: true }),
  }).first();
  await expect(configurableRow).toBeVisible();
  await expect(configurableRow.locator(".plugin-installed-meta")).toBeVisible();
  await expect(configurableRow.locator(".plugin-title-version")).toHaveCount(0);
  await expect(configurableRow.locator(".plugin-installed-label")).not.toHaveCount(0);
  const detailBox = await configurableRow.getByRole("button", { name: "详情", exact: true }).boundingBox();
  const settingsBox = await configurableRow.getByRole("button", { name: "设置", exact: true }).boundingBox();
  expect(detailBox).not.toBeNull();
  expect(settingsBox).not.toBeNull();
  expect(Math.abs(detailBox!.y - settingsBox!.y)).toBeLessThan(1);
  expect(settingsBox!.x - detailBox!.x - detailBox!.width).toBeGreaterThanOrEqual(7);
  expect(detailBox!.width).toBeGreaterThanOrEqual(80);
  expect(settingsBox!.width).toBeGreaterThanOrEqual(80);

  await pluginSearch.fill("");
  await page.getByRole("button", { name: "查看插件详情 TokenHub Core Provider Settings" }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.admin\.core-provider$/);
  await expect(page.getByText("兼容", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "文件" }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.admin\.core-provider\/files$/);
  await expect(page.getByRole("button", { name: /plugin\.yaml/ })).toBeVisible();
  await expect(page.getByText("该内置插件没有独立安装包。")).toHaveCount(0);
  await page.getByRole("button", { name: "返回插件列表" }).click();

  await page.getByRole("button", { name: "查看插件详情 External Trace Hook" }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.extension\.external-trace$/);
  await expect(page.getByRole("heading", { name: "External Trace Hook" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "这个插件做什么" })).toBeVisible();
  await expect(page.getByText("Contract fixture for an external trace export gateway hook.", { exact: true })).toBeVisible();
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
  await page.getByRole("tab", { name: "UI 模板" }).click();
  const templateRow = page.locator(".plugin-installed-row").filter({ hasText: "TokenHub Default Interface Template" });
  await templateRow.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page).toHaveURL(/\/plugins\/tokenhub\.sim\.default\/settings$/);
  await expect(page.getByRole("heading", { name: "TokenHub Default Interface Template" })).toBeVisible();
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

test.describe("Billing calculator", () => {
  test.use({ timezoneId: "Asia/Shanghai" });
  test("admin can calculate, publish, and inspect prices without advanced configuration", async ({ page }) => {
  await login(page);
  await page.goto("/billing");
  await expect(page.getByRole("heading", { name: "先算清楚，再发布价格" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "外部账单连接器" })).not.toBeVisible();
  await page.getByRole("combobox", { name: "计价对象", exact: true }).selectOption({ index: 1 });
  await page.getByLabel("普通输入", { exact: true }).fill("2");
  await page.getByLabel("输出", { exact: true }).fill("6");
  const input = page.getByLabel("普通输入", { exact: true });
  expect(await input.evaluate((el) => el.getBoundingClientRect().height)).toBeGreaterThanOrEqual(40);
  await page.getByRole("button", { name: "试算费用", exact: true }).click();
  await expect(page.locator(".billing-result output")).toHaveText("0.008 USD");
  await page.locator("summary").filter({ hasText: "单独设置缓存价格" }).click();
  await page.getByLabel("缓存读取", { exact: true }).fill("0.5");
  await expect(page.getByRole("button", { name: "发布用于后续核对" })).not.toBeVisible();
  await page.locator("summary").filter({ hasText: "缓存用量与试算时间" }).click();
  await page.getByLabel("总输入 Token", { exact: true }).fill("1000000");
  await page.getByLabel("缓存读取 Token", { exact: true }).fill("800000");
  await page.getByLabel("输出 Token", { exact: true }).fill("10000");
  await page.getByRole("button", { name: "试算费用", exact: true }).click();
  await expect(page.locator(".billing-result output")).toHaveText("0.86 USD");
  await page.locator("summary").filter({ hasText: "设置峰谷时段" }).click();
  await page.getByRole("button", { name: "添加时段", exact: true }).click();
  await page.locator(".billing-period").getByLabel("普通输入", { exact: true }).fill("4");
  await page.getByLabel("试算时间", { exact: true }).fill("2026-09-07T10:00");
  await page.getByRole("button", { name: "试算费用", exact: true }).click();
  await expect(page.locator(".billing-result output")).toHaveText("1.26 USD");
  await expect(page.locator(".billing-period-badge")).toHaveText("时段 1");
  await page.getByRole("button", { name: "删除时段", exact: true }).click();
  await page.getByRole("button", { name: "试算费用", exact: true }).click();
  await expect(page.locator(".billing-result output")).toHaveText("0.86 USD");
  await page.getByRole("button", { name: "发布用于后续核对", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "已发布。" })).toBeVisible();
  await page.getByRole("tab", { name: "价目与记录" }).click();
  await expect(page.locator(".billing-version").first()).toBeVisible();
  await page.getByRole("tab", { name: "账单与对账" }).click();
  await expect(page.getByRole("heading", { name: "外部账单连接器" })).toBeVisible();
  const last = page.getByRole("heading", { name: "外部账单明细" }).locator("xpath=ancestor::section[1]");
  const next = page.getByRole("heading", { name: "成本对账规则" }).locator("xpath=ancestor::section[1]");
  const bottom = await last.boundingBox();
  const top = await next.boundingBox();
  expect(top!.y - bottom!.y - bottom!.height).toBeGreaterThanOrEqual(18);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("tab", { name: "费用试算" }).click();
  await expect.poll(() => page.locator(".billing-page").evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await expect(page.getByRole("combobox", { name: "计价对象", exact: true })).toBeVisible();
  await expect(page.getByLabel("普通输入", { exact: true })).toHaveValue("2");
  await page.getByRole("button", { name: "界面语言", exact: true }).click();
  await page.getByRole("option", { name: "English", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Cost calculator", exact: true })).toBeVisible();
  await expect.poll(() => page.locator(".billing-page").evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await expect.poll(() => page.locator(".billing-tabs").evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await page.getByRole("tab", { name: "Bills & reconciliation", exact: true }).click();
  await expect(page.locator("#billing-panel-reconciliation")).toBeVisible();
});

});
