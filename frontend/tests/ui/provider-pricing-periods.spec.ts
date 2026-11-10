import type { Page } from "@playwright/test";
import type { Provider, ProviderModel } from "../../features/admin/core/types";
import { capture, expect, test } from "./harness";
import type { MockAPI } from "./network";

const provider: Provider = { id: "prv_ui_time_pricing", name: "UI Pricing Provider", type: "mock", priority: 1, status: "active", healthy: true, options: { catalog_id: "local" } };
const inventoryModel: ProviderModel = {
  id: "pm_ui_time_pricing", provider_id: provider.id, upstream_model: "ui-chat-off-peak", display_name: "UI Chat · Time Pricing", modality: "chat", status: "active", call_supported: true,
  input_price_usd_per_1m: 2, cache_read_price_usd_per_1m: 0.2, output_price_usd_per_1m: 8,
  metadata: { pricing_status: "configured", fixture: "synthetic" },
};

function install(api: MockAPI, reject = false) {
  const stored = structuredClone(inventoryModel);
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored] });
  const catalog = { id: "local", name: "UI Pricing Catalog", display_name: "UI Pricing Catalog", type: "mock", models_count: 0, source: "plugin", models: [] };
  api.respond("GET", "/api/admin/provider-catalog", { data: [catalog] });
  api.respond("GET", "/api/admin/provider-catalog/local", { data: catalog });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "mock", capabilities: ["chat"], plugin_id: "tokenhub.provider.mock" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.mock", name: "UI Mock", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "mock" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
  api.define("PATCH", `/api/admin/provider-models/${stored.id}`, request => {
    expect(request.body).toMatchObject({ input_price_usd_per_1m: 2, cache_read_price_usd_per_1m: 0.2, output_price_usd_per_1m: 8, metadata: { pricing_status: "configured", fixture: "synthetic" } });
    const periods = (request.body as ProviderModel).pricing_periods;
    expect(periods).toHaveLength(1);
    expect(periods![0]).toMatchObject({ name: "UI off-peak", timezone: "Asia/Shanghai", start_time: "23:00", end_time: "08:00", input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4 });
    if (reject) return { status: 400, json: { error: { message: "Synthetic pricing schedule rejected" } } };
    Object.assign(stored, request.body);
    api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored] });
    return { json: stored };
  });
  return stored;
}

async function open(page: Page) {
  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: provider.name }).getByRole("button", { name: "编辑", exact: true }).click();
  const dialog = page.locator(".provider-modal");
  await dialog.getByRole("tab", { name: "模型", exact: true }).click();
  await dialog.getByRole("button", { name: /分时价格/ }).click();
  return dialog.getByRole("region", { name: "分时成本设置", exact: true });
}

async function configure(page: Page) {
  const panel = await open(page);
  await expect(panel.getByText("尚未配置时段，全天使用基础价格。")).toBeVisible();
  await panel.getByRole("button", { name: "添加时段", exact: true }).click();
  await panel.getByLabel("时段名称", { exact: true }).fill("UI off-peak");
  await panel.getByLabel("时区（IANA）", { exact: true }).fill("Asia/Shanghai");
  await panel.getByLabel("开始时间", { exact: true }).fill("23:00");
  await panel.getByLabel("结束时间", { exact: true }).fill("08:00");
  await panel.getByLabel("输入（缓存未命中）USD/1M", { exact: true }).fill("1");
  await panel.getByLabel("输入（缓存命中）USD/1M", { exact: true }).fill("0");
  await panel.getByLabel("输出 USD/1M", { exact: true }).fill("4");
  return panel;
}

for (const mobile of [false, true]) {
  const viewport = mobile ? "mobile" : "desktop";
  test(`provider-pricing-periods structured-editor-${viewport}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const stored = install(api);
    const panel = await configure(page);
    await expect(panel.getByLabel("输入（缓存命中）USD/1M", { exact: true })).toHaveValue("0");
    expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1), "The structured editor must fit its available width").toBe(true);
    if (mobile) {
      await panel.getByLabel("开始时间", { exact: true }).scrollIntoViewIfNeeded();
      await capture(page, info, panel, `provider-pricing-periods-schedule-${viewport}`, "供应商分时定价：时区与跨午夜时段", "viewport");
      await panel.getByRole("button", { name: "保存成本与时段", exact: true }).scrollIntoViewIfNeeded();
      await capture(page, info, panel, `provider-pricing-periods-rates-${viewport}`, "供应商分时定价：缓存命中、未命中与输出价格", "viewport");
    } else {
      await capture(page, info, panel, `provider-pricing-periods-editor-${viewport}`, "供应商分时定价：可视化配置完整时段");
    }
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect.poll(() => stored.pricing_periods?.length).toBe(1);
    await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
    await panel.getByRole("button", { name: "收起分时设置" }).click();
    await page.locator(".provider-modal").getByRole("button", { name: /分时价格/ }).click();
    await expect(panel.getByLabel("输入（缓存命中）USD/1M", { exact: true })).toHaveValue("0");
    expect(stored.pricing_periods![0]).not.toHaveProperty("cache_write_price_usd_per_1m");
  });
}

test("provider-pricing-periods save-error-preserves-draft", async ({ page, api }, info) => {
  const stored = install(api, true);
  const panel = await configure(page);
  await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
  const error = page.locator(".provider-model-inventory").getByRole("alert");
  await expect(error).toContainText("Synthetic pricing schedule rejected");
  await expect(panel.getByLabel("时段名称", { exact: true })).toHaveValue("UI off-peak");
  expect(stored.pricing_periods).toBeUndefined();
  await error.scrollIntoViewIfNeeded();
  await capture(page, info, page.locator(".provider-model-inventory"), "provider-pricing-periods-save-error", "供应商分时定价：保存失败保留草稿", "viewport");
});
