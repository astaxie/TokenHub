import type { Model, ModelRoute, Provider, ProviderModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { shellResponses } from "./fixtures/shell";
import type { MockAPI } from "./network";

const provider: Provider = { id: "ui-pricing-provider", name: "UI Pricing Provider", type: "mock", priority: 1, status: "active", healthy: true };
const source: ProviderModel = {
  id: "ui-pricing-source", provider_id: provider.id, upstream_model: "ui-chat-source", modality: "chat", status: "active", call_supported: true,
  input_price_usd_per_1m: 0.4, cache_read_price_usd_per_1m: 0.04, output_price_usd_per_1m: 0.8,
  metadata: { pricing_status: "configured" },
  pricing_periods: [{ name: "Off-peak", timezone: "Asia/Shanghai", start_time: "23:00", end_time: "08:00", input_price_usd_per_1m: 0.2, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 0.4 }],
};
const model: Model = { id: "ui-public-pricing", name: "ui-public-chat", family: "ui", category: "other", modality: "chat", status: "active", input_price_usd_per_1m: 10, output_price_usd_per_1m: 20, context_window: 32000, metadata: { directory_role: "external", custom_note: "preserve" } };
const route: ModelRoute = { id: "ui-pricing-route", model_name: model.name, provider_id: provider.id, provider_model: source.upstream_model, status: "active", priority: 1, weight: 100 };

function install(api: MockAPI, linked = false, missing = false, disableBrokenSource = false) {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  let stored = structuredClone(model);
  if (linked) stored.metadata = { ...stored.metadata, pricing_mode: "provider", pricing_provider_model_id: source.id };
  function refresh() {
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: [stored], providers: [provider] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: missing ? [] : [source, { ...source, id: "ui-unconfirmed", upstream_model: "ui-unconfirmed", metadata: { pricing_status: "unverified" } }] });
  }
  api.respond("GET", "/api/admin/routing-rules", { data: [route] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  refresh();
  api.define("PATCH", `/api/admin/models/${model.name}`, input => {
    const body = input.body as Model;
    expect(body).toMatchObject({ input_price_usd_per_1m: 10, output_price_usd_per_1m: 20, metadata: { custom_note: "preserve", pricing_mode: missing && !disableBrokenSource ? "custom" : "provider" } });
    if (!missing || disableBrokenSource) expect(body.metadata?.pricing_provider_model_id).toBe(source.id);
    if (disableBrokenSource) expect(body.status).toBe("disabled");
    stored = { ...stored, ...body };
    refresh();
    return { json: stored };
  });
}

for (const mobile of [false, true]) {
  test(`pricing-source link-and-reopen-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    install(api);
    await page.goto("/models");
    await page.locator(".model-directory-table").getByRole("button", { name: "编辑", exact: true }).click();
    const editor = page.getByRole("dialog", { name: "模型目录", exact: true });
    await editor.getByRole("combobox", { name: "对外定价方式", exact: true }).selectOption("provider");
    const chooser = editor.getByLabel(/^定价来源模型/);
    await expect(chooser.getByRole("option", { name: /ui-unconfirmed/ })).toHaveCount(0);
    await chooser.selectOption(source.id);
    await expect(editor.getByLabel(/^对外输入价/)).toHaveCount(0);
    const panel = editor.getByRole("region", { name: "对外定价方式", exact: true });
    const preview = panel.getByLabel("来源价格预览", { exact: true });
    await expect(preview.getByText("Off-peak", { exact: true })).toBeVisible();
    await expect(preview).toContainText("23:00 – 08:00");
    await expect(preview).toContainText("Asia/Shanghai");
    await chooser.scrollIntoViewIfNeeded();
    await capture(page, info, panel, `pricing-source-choice-${mobile ? "mobile" : "desktop"}`, "对外模型选择已确认的上游价格来源", mobile ? "viewport" : "section");
    await preview.locator(".model-pricing-source-period").scrollIntoViewIfNeeded();
    await capture(page, info, preview, `pricing-source-preview-${mobile ? "mobile" : "desktop"}`, "来源基础价格、免费缓存与跨午夜优惠时段", "viewport");
    await editor.getByRole("button", { name: "保存", exact: true }).click();
    await expect(editor).toHaveCount(0);
    const row = page.locator(".model-directory-table").getByRole("row").filter({ hasText: model.name });
    await expect(row).toContainText("跟随上游定价");
    await expect(row).not.toContainText("$10");
    await capture(page, info, page.locator(".model-directory"), `pricing-source-directory-${mobile ? "mobile" : "desktop"}`, "模型目录显示跟随上游，避免展示已停用的独立价格", "viewport");
    await row.getByRole("button", { name: "编辑", exact: true }).click();
    await expect(editor.getByRole("combobox", { name: "对外定价方式", exact: true })).toHaveValue("provider");
    await expect(editor.getByLabel(/^定价来源模型/)).toHaveValue(source.id);
    await editor.getByRole("combobox", { name: "对外定价方式", exact: true }).selectOption("custom");
    await expect(editor.getByLabel(/^对外输入价/)).toHaveValue("10");
  });
}

test("pricing-source unavailable-source-recovery", async ({ page, api }, info) => {
  install(api, true, true);
  await page.goto("/models");
  await page.locator(".model-directory-table").getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.getByRole("dialog", { name: "模型目录", exact: true });
  await expect(editor.getByRole("alert")).toContainText("原定价来源已不可用或成本未确认");
  await editor.getByRole("alert").scrollIntoViewIfNeeded();
  await capture(page, info, editor, "pricing-source-unavailable", "来源不可用时提示重新选择或恢复独立价格", "viewport");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
  await editor.getByRole("combobox", { name: "对外定价方式", exact: true }).selectOption("custom");
  await expect(editor.getByLabel(/^对外输入价/)).toHaveValue("10");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  await expect(editor).toHaveCount(0);
});

test("pricing-source disable-with-unavailable-source", async ({ page, api }, info) => {
  install(api, true, true, true);
  await page.goto("/models");
  await page.locator(".model-directory-table").getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.getByRole("dialog", { name: "模型目录", exact: true });
  const chooser = editor.getByLabel(/^定价来源模型/);
  await expect(chooser).toHaveAttribute("required", "");
  await editor.getByRole("combobox", { name: "状态", exact: true }).selectOption("disabled");
  await expect(chooser).not.toHaveAttribute("required");
  await expect(editor.getByRole("alert")).toContainText("可以停用模型并保留原定价来源");
  await editor.getByRole("alert").scrollIntoViewIfNeeded();
  await capture(page, info, editor, "pricing-source-disable-unavailable", "来源失效时仍可停用模型并保留定价引用", "viewport");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  await expect(editor).toHaveCount(0);
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
  await page.getByRole("button", { name: "已下线", exact: true }).click();
  await page.locator(".model-directory-table").getByRole("button", { name: "编辑", exact: true }).click();
  await editor.getByRole("combobox", { name: "状态", exact: true }).selectOption("active");
  await expect(chooser).toHaveAttribute("required", "");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  await expect(editor).toBeVisible();
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
});
