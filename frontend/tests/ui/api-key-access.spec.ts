import type { Page } from "@playwright/test";
import type { APIKey, APIKeyUsageResponse } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { fixedTime, project, user } from "./fixtures/shell";
import type { MockAPI } from "./network";

const original: APIKey = { id: "key_ui_access", name: "UI Setup Key", project_id: project.id, owner_user_id: user.id, status: "active", allowed_models: [], model_access_mode: "inherit", key_prefix: "sk_ui", key_suffix: "5678" };
const usageKey: APIKey = { ...original, id: "key_review_a", name: "UI Usage Key", key_suffix: "aaaa" };
const newSecret = "sk_ui_synthetic_issued_key_1234";
const rotatedSecret = "sk_ui_synthetic_rotated_key_9012";

function setup(api: MockAPI, options: { failRotation?: boolean; loading?: Promise<void> } = {}) {
  const keys: APIKey[] = [structuredClone(original)];
  api.define("GET", "/api/admin/api-keys", () => ({ json: { data: structuredClone(keys) } }));
  api.respond("GET", "/api/admin/users", { data: [user] });
  api.respond("GET", "/api/admin/resources/teams", { data: [] });
  api.respond("GET", "/api/admin/resources/project-members", { data: [] });
  api.define("POST", `/api/admin/projects/${project.id}/keys`, input => {
    expect(input.body).toEqual({ name: "UI Created Key", group: "default", owner_user_id: user.id, model_access_mode: "inherit", allowed_models: [], ip_allowlist: [], limits: { daily_requests: 1000, monthly_requests: 30000, daily_tokens: 100000000, monthly_tokens: 2000000000, daily_cost_usd: 100, monthly_cost_usd: 2000, max_concurrency: 20 } });
    const created = { ...original, id: "key_ui_created", name: "UI Created Key", key_suffix: "1234" };
    keys.push(created);
    return { status: 201, json: { ...created, api_key: newSecret, plain_text_visible_once: true } };
  });
  api.define("POST", `/api/admin/api-keys/${original.id}/rotate`, async input => {
    expect(input.body).toEqual({});
    await options.loading;
    if (options.failRotation) return { status: 503, json: { error: { message: "Synthetic rotation unavailable" } } };
    keys[0].status = "revoked";
    const rotated = { ...original, id: "key_ui_rotated", key_suffix: "9012", rotated_from_id: original.id };
    keys.push(rotated);
    return { status: 201, json: { ...rotated, api_key: rotatedSecret, plain_text_visible_once: true } };
  });
  return keys;
}

async function openKeys(page: Page) {
  await page.goto("/api-keys");
  const row = page.getByRole("row").filter({ hasText: original.name });
  await expect(row).toBeVisible();
  return row;
}

function stubKeyUsagePage(api: MockAPI, key: APIKey) {
  const emptyMetrics = {
    request_count: 0, error_count: 0, average_latency_ms: 0, input_tokens: 0, cached_input_tokens: 0,
    cache_write_input_tokens: 0, input_audio_tokens: 0, output_tokens: 0, reasoning_output_tokens: 0,
    output_audio_tokens: 0, accepted_prediction_tokens: 0, rejected_prediction_tokens: 0, total_tokens: 0, estimated_cost_usd: 0,
  };
  const emptyQuota = { requests: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, cost_usd: 0 };
  const usage: APIKeyUsageResponse = {
    key, range: { from: "2026-08-09T00:00:00.000Z", to: fixedTime }, generated_at: fixedTime, summary: emptyMetrics,
    quota: {
      effective_limits: { rate_limit_rpm: 0, token_limit_tpm: 0, daily_requests: 0, monthly_requests: 0, daily_tokens: 0, monthly_tokens: 0, daily_cost_usd: 0, monthly_cost_usd: 0, max_concurrency: 0 },
      day: { bucket: "2026-09-07", usage: emptyQuota }, month: { bucket: "2026-09", usage: emptyQuota },
    },
    timeseries: [], models: [], errors: [],
  };
  api.define("GET", `/api/admin/api-keys/${key.id}/usage`, () => ({ json: structuredClone(usage) }), query => {
    expect(query.has("from")).toBe(true);
    expect(query.has("to")).toBe(true);
  });
  api.define("GET", "/api/admin/audit/requests", () => ({
    json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } },
  }), () => undefined);
}

test("api-key-access existing key shows protocol setup without rotating", async ({ page, api }, testInfo) => {
  setup(api);
  const row = await openKeys(page);
  await row.getByRole("button", { name: "使用", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "使用 API Key" });
  await expect(dialog.getByLabel("接入地址（Base URL）")).toHaveValue("http://tokenhub-ui.invalid/v1");
  await expect(dialog.getByLabel("API Key 占位符")).toHaveValue("YOUR_TOKENHUB_API_KEY");
  await capture(page, testInfo, dialog, "key-existing-setup", "已有 Key：地址与占位符");
  await dialog.getByLabel("接入协议").selectOption("anthropic");
  await expect(dialog.getByLabel("接入地址（Base URL）")).toHaveValue("http://tokenhub-ui.invalid");
  await expect(dialog).toContainText("x-api-key");
  await dialog.getByLabel("接入协议").selectOption("gemini");
  await expect(dialog.getByLabel("接入地址（Base URL）")).toHaveValue("http://tokenhub-ui.invalid");
  await dialog.locator("summary").click();
  await dialog.getByLabel("模型 ID", { exact: true }).fill("ui-review-model");
  await expect(dialog.getByLabel("请求地址", { exact: true })).toHaveValue("http://tokenhub-ui.invalid/v1beta/models/ui-review-model:generateContent");
  await expect(dialog.getByLabel("cURL", { exact: true })).toHaveValue(/x-goog-api-key/);
  await dialog.getByLabel("接入协议").selectOption("responses");
  await expect(dialog.getByLabel("请求地址", { exact: true })).toHaveValue("http://tokenhub-ui.invalid/v1/responses");
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
});

test("api-key-access creation opens setup and closing clears the full key", async ({ page, api }, testInfo) => {
  setup(api);
  await openKeys(page);
  await page.getByRole("button", { name: "发放 Key", exact: true }).click();
  await page.getByRole("button", { name: new RegExp(project.name) }).click();
  await page.getByLabel("归属用户").selectOption(user.id);
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByLabel("Key 名称").fill("UI Created Key");
  for (let step = 0; step < 3; step++) await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "生成 Key" }).click();
  const dialog = page.getByRole("dialog", { name: "使用 API Key" });
  await expect(dialog.getByLabel("完整 Key")).toHaveValue(newSecret);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await dialog.getByRole("button", { name: "复制 Key", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(newSecret);
  await dialog.getByRole("button", { name: "复制地址", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("http://tokenhub-ui.invalid/v1");
  await capture(page, testInfo, dialog, "key-created-setup", "创建成功：直接复制地址与完整 Key（合成数据）");
  await dialog.getByRole("button", { name: "我已保存，关闭" }).click();
  await page.getByRole("row").filter({ hasText: "UI Created Key" }).getByRole("button", { name: "使用", exact: true }).click();
  await expect(dialog.getByLabel("API Key 占位符")).toHaveValue("YOUR_TOKENHUB_API_KEY");
  await expect(dialog.getByLabel("完整 Key")).toHaveCount(0);
  expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toContain(newSecret);
});

test("api-key-access rotation requires confirmation and cancels without a write", async ({ page, api }, testInfo) => {
  let release!: () => void;
  const loading = new Promise<void>(resolve => { release = resolve; });
  const keys = setup(api, { loading });
  const row = await openKeys(page);
  await row.getByRole("button", { name: "轮换", exact: true }).click();
  const confirmation = page.getByRole("dialog", { name: "确认轮换 API Key" });
  await expect(confirmation).toContainText(original.name);
  await expect(confirmation).toContainText("旧 Key 将立即失效");
  await page.keyboard.press("Tab");
  await expect(confirmation.getByRole("button", { name: "取消", exact: true })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(confirmation.getByRole("button", { name: "确认轮换", exact: true })).toBeFocused();
  await capture(page, testInfo, confirmation, "key-rotation-confirmation", "轮换确认：操作对象与旧 Key 失效提示");
  await confirmation.getByRole("button", { name: "取消", exact: true }).click();
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
  expect(keys[0].status).toBe("active");
  await row.getByRole("button", { name: "轮换", exact: true }).click();
  await confirmation.getByRole("button", { name: "确认轮换", exact: true }).click();
  await expect.poll(() => api.calls.filter(call => call.path.endsWith("/rotate")).length).toBe(1);
  await row.getByRole("button", { name: "轮换", exact: true }).click();
  await expect(confirmation).toHaveCount(0);
  release();
  const dialog = page.getByRole("dialog", { name: "使用 API Key" });
  await expect(dialog.getByLabel("完整 Key")).toHaveValue(rotatedSecret);
  await capture(page, testInfo, dialog, "key-rotated-setup", "轮换成功：新 Key 使用窗口（合成数据）");
  expect(api.calls.filter(call => call.path.endsWith("/rotate"))).toHaveLength(1);
});

test("api-key-access failed rotation keeps the existing key and shows an error", async ({ page, api }, testInfo) => {
  const keys = setup(api, { failRotation: true });
  const row = await openKeys(page);
  await row.getByRole("button", { name: "轮换", exact: true }).click();
  await page.getByRole("button", { name: "确认轮换", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "使用 API Key" })).toHaveCount(0);
  await expect(page.getByText("Synthetic rotation unavailable", { exact: true })).toBeVisible();
  expect(keys[0].status).toBe("active");
  await capture(page, testInfo, page.locator(".app-shell"), "key-rotation-failure", "轮换失败：显示错误，不展示新 Key", "viewport");
});

test("api-key-access browser history navigation dismisses rotation confirmation", async ({ page, api }) => {
  setup(api);
  api.respond("GET", "/api/admin/resources/cost-centers", { data: [] });
  await page.goto("/cost-centers");
  await expect(page).toHaveURL(/\/cost-centers$/);
  await page.getByRole("complementary").getByRole("button", { name: "Key 管理", exact: true }).click();
  await expect(page).toHaveURL(/\/api-keys$/);
  const row = page.getByRole("row").filter({ hasText: original.name });
  await row.getByRole("button", { name: "轮换", exact: true }).click();
  const confirmation = page.getByRole("dialog", { name: "确认轮换 API Key" });
  await expect(confirmation).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/\/cost-centers$/);
  await expect(page.getByRole("dialog", { name: "确认轮换 API Key" })).toHaveCount(0);
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
});

test("api-key-access browser history dismisses rotation confirmation within Key Management", async ({ page, api }) => {
  const keys = setup(api);
  keys.push(structuredClone(usageKey));
  stubKeyUsagePage(api, usageKey);
  await page.goto(`/api-keys/${usageKey.id}/usage`);
  await expect(page.getByRole("heading", { name: usageKey.name })).toBeVisible();
  await page.getByRole("complementary").getByRole("button", { name: "Key 管理", exact: true }).click();
  await expect(page).toHaveURL(/\/api-keys$/);
  await page.getByRole("row").filter({ hasText: original.name }).getByRole("button", { name: "轮换", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "确认轮换 API Key" })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`/api-keys/${usageKey.id}/usage$`));
  await expect(page.getByRole("dialog", { name: "确认轮换 API Key" })).toHaveCount(0);
  await page.goForward();
  await expect(page).toHaveURL(/\/api-keys$/);
  await expect(page.getByRole("dialog", { name: "确认轮换 API Key" })).toHaveCount(0);
  expect(api.calls.filter(call => call.method === "POST")).toHaveLength(0);
});

test("api-key-access mobile setup keeps the close action visible", async ({ page, api }, testInfo) => {
  setup(api);
  await page.setViewportSize({ width: 390, height: 844 });
  const row = await openKeys(page);
  await row.getByRole("button", { name: "使用", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "使用 API Key" });
  await expect(dialog.getByRole("button", { name: "关闭", exact: true })).toBeInViewport();
  expect(await dialog.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
  await capture(page, testInfo, dialog, "key-mobile-setup", "移动端：使用窗口与固定关闭按钮", "viewport");
});
