import { expect, test, type APIRequestContext } from "@playwright/test";
import e2eDefaults from "./config.cjs";

const headers = { authorization: "Bearer e2e_admin_token_0000000000000000" };
const backend = `http://127.0.0.1:${process.env.TOKENHUB_E2E_BACKEND_PORT ?? e2eDefaults.backendPort}`;
async function post(request: APIRequestContext, path: string, data: object) {
  const response = await request.post(backend + path, { headers, data });
  expect(response.status(), await response.text()).toBe(201);
  return response.json();
}
test("admin can inspect both cost bases and save a verified analysis", async ({ page, request }) => {
  const model = "pricing-impact-e2e";
  const provider = (await post(request, "/api/admin/providers", { name: "Pricing E2E", type: "openai_compatible", base_url: `http://127.0.0.1:${process.env.TOKENHUB_E2E_UPSTREAM_PORT ?? e2eDefaults.upstreamPort}/v1`, api_key: e2eDefaults.upstreamKey, status: "active", create_routes: false })).provider;
  await post(request, "/api/admin/provider-models/import", { provider_id: provider.id, models: [{ id: "e2e-chat-model", name: "E2E chat" }] });
  const inventory = await (await request.get(`${backend}/api/admin/provider-models`, { headers })).json();
  const pm = inventory.data.find((item: { provider_id: string }) => item.provider_id === provider.id);
  expect((await request.patch(`${backend}/api/admin/provider-models/${pm.id}`, { headers, data: { input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0.2, output_price_usd_per_1m: 2 } })).status()).toBe(200);
  await post(request, "/api/admin/models", { name: model, family: "review", category: "custom", modality: "chat", status: "active", input_price_usd_per_1m: 2, cache_read_price_usd_per_1m: 0.5, output_price_usd_per_1m: 6, metadata: { directory_role: "external" } });
  await post(request, "/api/admin/routing-rules", { model_name: model, provider_id: provider.id, provider_model: "e2e-chat-model", status: "active", priority: 1, weight: 100 });
  const user = await post(request, "/api/admin/users", { username: "pricing-e2e-user", email: "pricing-e2e@example.test", role: "user", status: "active", password: "e2e-pricing-owner-password" });
  const project = await post(request, "/api/admin/projects", { name: "Pricing Review", owner_user_id: user.id, status: "active" });
  const key = await post(request, `/api/admin/projects/${project.id}/keys`, { name: "Pricing E2E key", owner_user_id: user.id, allowed_models: [model], model_access_mode: "restricted" });
  const response = await request.post(`${backend}/v1/chat/completions`, { headers: { authorization: `Bearer ${key.api_key}` }, data: { model, messages: [{ role: "user", content: "hello" }] } });
  expect(response.status(), await response.text()).toBe(200);
  await page.goto("/"); await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity); await page.getByLabel("密码", { exact: true }).fill(e2eDefaults.adminPassword); await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible(); await page.goto("/models");
  await page.getByRole("group", { name: "发布状态" }).getByRole("button", { name: "全部", exact: true }).click();
  await page.getByRole("row").filter({ hasText: model }).getByRole("button", { name: "定价与收益", exact: true }).click();
  await expect(page.getByLabel("普通输入", { exact: true }).first()).toHaveValue("2"); await page.getByLabel("普通输入", { exact: true }).first().fill("3");
  await page.locator("summary").filter({ hasText: "可选：历史收益分析" }).click();
  const today = new Date(); const tomorrow = new Date(today); tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
  await page.getByLabel("分析时区", { exact: true }).fill("UTC"); await page.getByLabel("开始日期", { exact: true }).fill(today.toISOString().slice(0, 10)); await page.getByLabel("截止日期（不含）", { exact: true }).fill(tomorrow.toISOString().slice(0, 10));
  const result = page.getByRole("region", { name: "收益分析结果" });
  await expect(result.getByText("1.19 USD", { exact: true }).first()).toBeVisible();
  if (process.env.PRICING_GALLERY_DIR) await result.screenshot({ path: `${process.env.PRICING_GALLERY_DIR}/historical-analysis.png` });
  expect((await request.patch(`${backend}/api/admin/provider-models/${pm.id}`, { headers, data: { input_price_usd_per_1m: 2 } })).status()).toBe(200);
  await page.getByLabel("成本口径", { exact: true }).selectOption("current_procurement");
  await expect(result.getByText("0.69 USD", { exact: true }).first()).toBeVisible();
  if (process.env.PRICING_GALLERY_DIR) await result.screenshot({ path: `${process.env.PRICING_GALLERY_DIR}/procurement-analysis.png` });
  await page.getByRole("button", { name: "核对并保存", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "确认调整模型价格" }); await expect(dialog).toContainText("当前采购价情景");
  if (process.env.PRICING_GALLERY_DIR) await page.screenshot({ path: `${process.env.PRICING_GALLERY_DIR}/confirmation.png`, fullPage: true });
  await dialog.getByRole("checkbox", { name: "我已了解本次调价影响及风险" }).check(); await dialog.getByRole("button", { name: "确认应用价格" }).click();
  await expect(page.getByRole("status").filter({ hasText: "模型价格已更新" })).toBeVisible();
  const changes = await (await request.get(`${backend}/api/admin/billing/price-changes`, { headers })).json();
  const applied = changes.data.find((item: { model_name: string; analysis_state: string }) => item.model_name === model && item.analysis_state === "performed");
  expect(applied.analysis.basis).toBe("current_procurement"); expect(applied.analysis.computable).toBe(1);
  await page.clock.setFixedTime(new Date("2026-09-07T16:30:00Z"));
  await page.locator("summary").filter({ hasText: "可选：历史收益分析" }).click();
  await page.getByLabel("分析时区", { exact: true }).fill("UTC");
  await page.getByRole("button", { name: "最近 7 个完整日", exact: true }).click();
  await expect(page.getByLabel("截止日期（不含）", { exact: true })).toHaveValue("2026-09-07");

});
