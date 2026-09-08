import { expect, test } from "@playwright/test";
import e2eDefaults from "./config.cjs";

test("admin can directly confirm model prices without a sample calculation", async ({ page, request }) => {
  const backend = `http://127.0.0.1:${process.env.TOKENHUB_E2E_BACKEND_PORT ?? e2eDefaults.backendPort}`;
  const headers = { authorization: "Bearer e2e_admin_token_0000000000000000" };
  const model = "pricing-direct-e2e";
  const seeded = await request.post(`${backend}/api/admin/models`, { headers, data: { name: model, modality: "chat", status: "active", input_price_usd_per_1m: 2, output_price_usd_per_1m: 6, metadata: { directory_role: "external" } } });
  expect(seeded.status()).toBe(201);
  await page.goto("/"); await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity); await page.getByLabel("密码", { exact: true }).fill(e2eDefaults.adminPassword); await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible(); await page.goto("/models");
  await page.getByRole("group", { name: "发布状态" }).getByRole("button", { name: "全部", exact: true }).click();
  await page.getByRole("row").filter({ hasText: model }).getByRole("button", { name: "定价与收益", exact: true }).click();
  await expect(page.getByLabel("普通输入", { exact: true }).first()).toHaveValue("2");
  await page.getByLabel("普通输入", { exact: true }).first().fill("3");
  await page.getByRole("button", { name: "核对并保存", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "确认调整模型价格" });
  await expect(dialog).toContainText("未进行影响分析");
  await expect(dialog.getByRole("button", { name: "确认应用价格" })).toBeDisabled();
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  expect((await (await request.get(`${backend}/api/admin/billing/models/${model}/pricing`, { headers })).json()).card.rates.input).toBe("2");
  await page.getByRole("button", { name: "核对并保存", exact: true }).click();
  await dialog.getByRole("checkbox", { name: "我已了解本次调价影响及风险" }).check();
  await dialog.getByRole("button", { name: "确认应用价格" }).click();
  await expect(page.getByRole("status").filter({ hasText: "模型价格已更新" })).toBeVisible();
  expect((await (await request.get(`${backend}/api/admin/billing/models/${model}/pricing`, { headers })).json()).card.rates.input).toBe("3");
  await page.getByRole("button", { name: "返回模型目录", exact: true }).click();
  await page.getByRole("row").filter({ hasText: model }).getByRole("button", { name: "编辑", exact: true }).click();
  await page.getByLabel("显示名称", { exact: true }).fill("Updated review model");
  await page.getByRole("combobox", { name: "模型类型", exact: true }).selectOption("custom");
  await page.getByRole("textbox", { name: "系列", exact: true }).fill("review");
  const concurrent = await request.patch(`${backend}/api/admin/models/${model}`, { headers, data: { input_price_usd_per_1m: 4 } });
  expect(concurrent.status()).toBe(200);
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByText("Updated review model", { exact: true }).first()).toBeVisible();
  expect((await (await request.get(`${backend}/api/admin/billing/models/${model}/pricing`, { headers })).json()).card.rates.input).toBe("4");
  await page.getByRole("row").filter({ hasText: model }).getByRole("button", { name: "定价与收益", exact: true }).click();
  await expect(page.getByLabel("普通输入", { exact: true }).first()).toHaveValue("4");
  await page.locator("summary").filter({ hasText: "高级：验证计价规则" }).click();
  await page.getByRole("button", { name: "试算费用", exact: true }).click();
  await expect(page.locator(".billing-rule-simulator output")).toHaveText("0.01 USD");


});
