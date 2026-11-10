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

function install(api: MockAPI, reject = false, expectedInput: number | null = 1, expectedTimezone = "Asia/Shanghai", expectedAllDay = false) {
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
    expect(periods![0]).toMatchObject({ name: "UI off-peak", timezone: expectedTimezone, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4 });
    if (expectedAllDay) {
      expect(periods![0]).not.toHaveProperty("start_time");
      expect(periods![0]).not.toHaveProperty("end_time");
    } else expect(periods![0]).toMatchObject({ start_time: "23:00", end_time: "08:00" });
    if (expectedInput === null) expect(periods![0]).not.toHaveProperty("input_price_usd_per_1m");
    else expect(periods![0].input_price_usd_per_1m).toBe(expectedInput);
    expect(periods![0]).not.toHaveProperty("invalidFieldKeys");
    expect(periods![0]).not.toHaveProperty("draftID");
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
  await dialog.getByRole("row").filter({ hasText: inventoryModel.upstream_model }).getByRole("button", { name: /分时价格/ }).click();
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

for (const invalidPeriod of ["removed", "remaining"]) {
  test(`provider-pricing-periods deletion-preserves-native-validity-${invalidPeriod}`, async ({ page, api }) => {
    const expectedInput = invalidPeriod === "removed" ? null : 1;
    const stored = install(api, false, expectedInput);
    const remainingPeriod = { name: "UI off-peak", timezone: "Asia/Shanghai", start_time: "23:00", end_time: "08:00", cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4 };
    stored.pricing_periods = [
      { name: "Delete this period", timezone: "UTC", start_time: "08:00", end_time: "09:00" },
      remainingPeriod,
    ];
    api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored] });
    const panel = await open(page);
    await panel.getByLabel("输入（缓存未命中）USD/1M", { exact: true }).nth(invalidPeriod === "removed" ? 0 : 1).fill("1e999");
    await panel.getByRole("button", { name: "删除时段", exact: true }).first().click();
    const rate = panel.getByLabel("输入（缓存未命中）USD/1M", { exact: true });
    await expect(panel.getByLabel("时段名称", { exact: true })).toHaveValue("UI off-peak");
    expect(await rate.evaluate((input: HTMLInputElement) => input.validity.badInput)).toBe(invalidPeriod === "remaining");
    if (invalidPeriod === "remaining") {
      await expect(rate).toHaveAttribute("aria-invalid", "true");
      await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
      await expect(page.locator(".provider-model-inventory").getByRole("alert")).toContainText("分时价格无效");
      expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
      await rate.fill("1");
    } else {
      await expect(rate).toHaveValue("");
    }
    await expect(rate).not.toHaveAttribute("aria-invalid", "true");
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
    const patches = api.calls.filter(call => call.method === "PATCH");
    expect(patches).toHaveLength(1);
    expect((patches[0].body as ProviderModel).pricing_periods).toEqual([{ ...remainingPeriod, ...(expectedInput === null ? {} : { input_price_usd_per_1m: expectedInput }) }]);
  });
}

test("provider-pricing-periods saved-local-timezone-preserves-server-meaning", async ({ page, api }) => {
  const stored = install(api, false, 1, "Local");
  const period = {
    name: "UI off-peak", timezone: "Local", start_time: "23:00", end_time: "08:00",
    input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4,
    effective_from: "2026-11-01T00:00:00+08:00", effective_until: "2026-12-01T00:00:00+08:00",
  };
  stored.pricing_periods = [period];
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored] });
  const panel = await open(page);
  await expect(panel.getByLabel("时区（IANA）", { exact: true })).toHaveValue("Local");
  await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
  await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
  expect(stored.pricing_periods).toEqual([period]);
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
});

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

test("provider-pricing-periods saved-schedule-whitespace", async ({ page, api }, info) => {
  const stored = install(api);
  stored.pricing_periods = [{
    name: "UI off-peak", timezone: " Asia/Shanghai ", start_time: " 23:00 ", end_time: "\t08:00\n",
    input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4,
    effective_from: " 2026-11-01T00:00:00Z ", effective_until: " 2026-12-01T00:00:00Z ",
  }];
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored] });
  const panel = await open(page);
  await expect(panel.getByLabel("开始时间", { exact: true })).toHaveValue("23:00");
  await expect(panel.getByLabel("结束时间", { exact: true })).toHaveValue("08:00");
  await expect(panel.getByLabel("时区（IANA）", { exact: true })).toHaveValue("Asia/Shanghai");
  await capture(page, info, panel, "provider-pricing-periods-saved-schedule", "供应商分时定价：已有时段正确显示时间");
  await panel.getByText("星期、生效日期与缓存写入", { exact: true }).click();
  await panel.getByLabel("生效开始（RFC 3339，可选）", { exact: true }).fill(" ");
  await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
  await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
  expect(stored.pricing_periods[0].effective_until).toBe("2026-12-01T00:00:00Z");
  expect(stored.pricing_periods[0]).not.toHaveProperty("effective_from");
});

for (const saveFrom of ["schedule", "table", "collapsed", "reopened", "switched"]) {
  test(`provider-pricing-periods invalid-number-is-not-inheritance-${saveFrom}`, async ({ page, api }) => {
    const stored = install(api);
    if (saveFrom === "switched") api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored, { ...inventoryModel, id: "pm_ui_alternate", upstream_model: "ui-alternate-chat", pricing_periods: [{ timezone: "UTC", start_time: "00:00", end_time: "08:00" }] }] });
    const panel = await configure(page);
    const rate = panel.getByLabel("输入（缓存未命中）USD/1M", { exact: true });
    await rate.fill("1e999");
    expect(await rate.evaluate((input: HTMLInputElement) => input.validity.badInput)).toBe(true);
    await expect(rate).toHaveAttribute("aria-invalid", "true");
    if (saveFrom === "collapsed" || saveFrom === "reopened") await panel.getByRole("button", { name: "收起分时设置" }).click();
    if (saveFrom === "reopened") await page.locator(".provider-modal").getByRole("button", { name: /分时价格/ }).click();
    if (saveFrom === "switched") {
      await page.getByRole("row").filter({ hasText: "ui-alternate-chat" }).getByRole("button", { name: /分时价格/ }).click();
      await expect(rate).not.toHaveAttribute("aria-invalid", "true");
      expect(await rate.evaluate((input: HTMLInputElement) => input.validity.badInput)).toBe(false);
      await page.getByRole("row").filter({ hasText: inventoryModel.upstream_model }).getByRole("button", { name: /分时价格/ }).click();
    }
    const save = saveFrom === "table" || saveFrom === "collapsed"
      ? page.locator(".provider-model-inventory-table").getByRole("button", { name: "保存成本", exact: true })
      : panel.getByRole("button", { name: "保存成本与时段", exact: true });
    await save.click();
    await expect(page.locator(".provider-model-inventory").getByRole("alert")).toContainText("分时价格无效");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
    await expect(rate).toBeFocused();
    await rate.fill("1");
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
  });
}

for (const saveFrom of ["schedule", "table", "collapsed", "reopened", "switched"]) {
  test(`provider-pricing-periods partial-clocks-are-not-all-day-${saveFrom}`, async ({ page, api }) => {
    const stored = install(api);
    if (saveFrom === "switched") api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored, { ...inventoryModel, id: "pm_ui_alternate", upstream_model: "ui-alternate-chat", pricing_periods: [{ timezone: "UTC", start_time: "00:00", end_time: "08:00" }] }] });
    const panel = await configure(page);
    const start = panel.getByLabel("开始时间", { exact: true });
    const end = panel.getByLabel("结束时间", { exact: true });
    for (const clock of [start, end]) {
      await clock.press("Home");
      await clock.press("Delete");
      expect(await clock.evaluate((input: HTMLInputElement) => input.validity.badInput)).toBe(true);
      await expect(clock).toHaveValue("");
      await expect(clock).toHaveAttribute("aria-invalid", "true");
    }
    if (saveFrom === "collapsed" || saveFrom === "reopened") await panel.getByRole("button", { name: "收起分时设置" }).click();
    if (saveFrom === "reopened") await page.locator(".provider-modal").getByRole("button", { name: /分时价格/ }).click();
    if (saveFrom === "switched") {
      await page.getByRole("row").filter({ hasText: "ui-alternate-chat" }).getByRole("button", { name: /分时价格/ }).click();
      await expect(start).toHaveValue("00:00");
      await expect(start).not.toHaveAttribute("aria-invalid", "true");
      await page.getByRole("row").filter({ hasText: inventoryModel.upstream_model }).getByRole("button", { name: /分时价格/ }).click();
    }
    const save = saveFrom === "table" || saveFrom === "collapsed"
      ? page.locator(".provider-model-inventory-table").getByRole("button", { name: "保存成本", exact: true })
      : panel.getByRole("button", { name: "保存成本与时段", exact: true });
    await save.click();
    await expect(page.locator(".provider-model-inventory").getByRole("alert")).toContainText("分时价格无效");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
    await expect(start).toBeFocused();
    await start.fill("23:00");
    await end.fill("08:00");
    await expect(start).not.toHaveAttribute("aria-invalid", "true");
    await expect(end).not.toHaveAttribute("aria-invalid", "true");
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
  });
}

for (const clearFrom of ["mounted", "reopened"]) {
  test(`provider-pricing-periods deliberately-clear-partial-clocks-${clearFrom}`, async ({ page, api }) => {
    install(api, false, 1, "Asia/Shanghai", true);
    const panel = await configure(page);
    const start = panel.getByLabel("开始时间", { exact: true });
    const end = panel.getByLabel("结束时间", { exact: true });
    for (const clock of [start, end]) {
      await clock.press("Home");
      await clock.press("Delete");
      await expect(clock).toHaveAttribute("aria-invalid", "true");
    }
    if (clearFrom === "reopened") {
      await panel.getByRole("button", { name: "收起分时设置" }).click();
      await page.locator(".provider-modal").getByRole("button", { name: /分时价格/ }).click();
      await start.press("Backspace");
    } else await start.fill("");
    await expect(start).not.toHaveAttribute("aria-invalid", "true");
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect(page.locator(".provider-model-inventory").getByRole("alert")).toContainText("分时价格无效");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
    if (clearFrom === "reopened") await end.press("Delete");
    else await end.fill("");
    await expect(end).not.toHaveAttribute("aria-invalid", "true");
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
  });
}

test("provider-pricing-periods partial-start-does-not-preserve-all-day", async ({ page, api }) => {
  const stored = install(api, false, 1, "Asia/Shanghai", true);
  stored.pricing_periods = [{ name: "UI off-peak", timezone: "Asia/Shanghai", input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4 }];
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored] });
  const panel = await open(page);
  const start = panel.getByLabel("开始时间", { exact: true });
  await start.press("ArrowUp");
  await expect(start).toHaveValue("");
  expect(await start.evaluate((input: HTMLInputElement) => input.validity.badInput)).toBe(true);
  await expect(start).toHaveAttribute("aria-invalid", "true");
  await page.locator(".provider-model-inventory-table").getByRole("button", { name: "保存成本", exact: true }).click();
  await expect(page.locator(".provider-model-inventory").getByRole("alert")).toContainText("分时价格无效");
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
  await start.fill("");
  await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
  await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
  expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
});

test("provider-pricing-periods invalid-schedule-does-not-block-another-model", async ({ page, api }) => {
  const stored = install(api);
  const other = { ...inventoryModel, id: "pm_ui_precise", upstream_model: "ui-precise-chat", input_price_usd_per_1m: 0.123456789 };
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [stored, other] });
  api.define("PATCH", `/api/admin/provider-models/${other.id}`, request => {
    expect(request.body).toMatchObject({ input_price_usd_per_1m: other.input_price_usd_per_1m, pricing_periods: [] });
    return { json: { ...other, ...request.body as object } };
  });
  const panel = await configure(page);
  const rate = panel.getByLabel("输入（缓存未命中）USD/1M", { exact: true });
  await rate.fill("1e999");
  await expect(rate).toHaveAttribute("aria-invalid", "true");
  await page.getByRole("row").filter({ hasText: other.upstream_model }).getByRole("button", { name: "保存成本", exact: true }).click();
  await expect(page.locator(".provider-model-inventory")).toContainText(`${other.upstream_model} · 渠道成本价已保存`);
  expect(api.calls.filter(call => call.method === "PATCH").map(call => call.path)).toEqual([`/api/admin/provider-models/${other.id}`]);
});

for (const reopen of [false, true]) {
  test(`provider-pricing-periods invalid-number-can-be-cleared-to-inheritance-${reopen ? "reopened" : "native"}`, async ({ page, api }) => {
    install(api, false, null);
    const panel = await configure(page);
    const rate = panel.getByLabel("输入（缓存未命中）USD/1M", { exact: true });
    await rate.fill("1e999");
    await expect(rate).toHaveAttribute("aria-invalid", "true");
    if (reopen) {
      await panel.getByRole("button", { name: "收起分时设置" }).click();
      await page.locator(".provider-modal").getByRole("button", { name: /分时价格/ }).click();
      await expect(rate).toHaveAttribute("aria-invalid", "true");
    }
    await rate.fill("");
    await expect(rate).not.toHaveAttribute("aria-invalid", "true");
    await panel.getByRole("button", { name: "保存成本与时段", exact: true }).click();
    await expect(page.locator(".provider-model-inventory")).toContainText("渠道成本价已保存");
    expect(api.calls.filter(call => call.method === "PATCH")).toHaveLength(1);
  });
}
