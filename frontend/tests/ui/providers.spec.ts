import type { Provider } from "../../features/admin/core/types";
import type { Locator } from "@playwright/test";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

function installProviders(api: MockAPI) {
  const providers: Provider[] = [
    { id: "prv_ui_local", name: "UI Local Cluster", type: "mock", base_url: "", priority: 1, status: "active", healthy: true },
    { id: "prv_ui_internal", name: "UI Internal Cluster", type: "mock", base_url: "http://inference.example.test/v1", priority: 2, status: "active", healthy: true },
  ];
  api.respond("GET", "/api/admin/providers", { data: providers });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "local", name: "Mock Catalog", display_name: "Mock Provider", type: "mock", models_count: 0, source: "plugin" }] });
  api.respond("GET", "/api/admin/provider-catalog/local", { data: { id: "local", name: "Local Cluster", type: "mock", models_count: 0, models: [], source: "plugin" } });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "mock", capabilities: ["chat"], plugin_id: "tokenhub.provider.mock" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.mock", name: "Mock Plugin", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "mock" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.define("GET", "/api/admin/audit/requests", () => ({ json: {
    data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
    summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
  } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });

}

test("providers local-provider-labels", async ({ page, api }, testInfo) => {
  installProviders(api);
  await page.goto("/providers");
  const local = page.getByRole("row").filter({ hasText: "UI Local Cluster" });
  const internal = page.getByRole("row").filter({ hasText: "UI Internal Cluster" });
  await expect(local.getByText("本地服务 · 本地服务", { exact: true })).toBeVisible();
  await expect(internal.getByText("本地服务 · http://inference.example.test/v1", { exact: true })).toBeVisible();
  await expect(page.locator(".provider-channel-list")).not.toContainText(/mock/i);
  await capture(page, testInfo, page.locator(".provider-channel-list"), "providers-local-labels", "本地服务类型与地址显示");

  const providerReads = () => api.calls.filter(call => call.path === "/api/admin/providers").length;
  const initialProviderReads = providerReads();
  for (const locale of [
    { language: "en", option: "English", label: "Local Provider", edit: "Edit", advanced: "Advanced", type: "Provider Type", close: "Close" },
    { language: "ja", option: "日本語", label: "ローカルサービス", edit: "編集", advanced: "詳細", type: "Provider タイプ", close: "閉じる" },
    { language: "zh-CN", option: "简体中文", label: "本地服务", edit: "编辑", advanced: "高级", type: "渠道商类型", close: "关闭" },
  ]) {
    await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
    await page.getByRole("option", { name: locale.option, exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    await expect(local.getByText(`${locale.label} · ${locale.label}`, { exact: true })).toBeVisible();
    await local.getByRole("button", { name: locale.edit, exact: true }).click();
    const editor = page.locator(".provider-modal");
    await editor.getByRole("tab", { name: locale.advanced, exact: true }).click();
    const providerType = editor.getByRole("combobox", { name: locale.type, exact: true });
    await expect(providerType).toHaveValue("mock");
    await expect(providerType.locator("option:checked")).toHaveText(locale.label);
    await capture(page, testInfo, editor, `providers-local-labels-${locale.language}`, "切换语言后的本地服务类型");
    await editor.getByTitle(locale.close, { exact: true }).click();
    expect(providerReads(), "Language changes must not reload provider data").toBe(initialProviderReads);
  }
});

for (const mobile of [false, true]) {
  test(`providers compact-management ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installProviders(api);
    await page.goto("/providers");
    const listing = page.locator(".provider-channel-list");
    await expect(listing).toHaveClass(/provider-channel-list-manage/);
    await expect(listing.locator(".provider-management-table-wrap")).toHaveCSS("overflow-y", "visible");
    await expect(page.locator(".page-context-header")).toContainText("2/2已启用供应商");
    await expect(page.locator(".page-context-header")).not.toContainText("健康 Provider");
    await expect(listing.getByText("待观测", { exact: true })).toHaveCount(2);
    await expect(listing.getByRole("columnheader", { name: "账号配额" })).toHaveCount(0);
    await capture(page, testInfo, listing, `providers-management-${mobile ? "mobile" : "desktop"}`, "供应商简洁列表与独立健康状态");
    const actions = listing.getByRole("row").filter({ hasText: "UI Internal Cluster" }).locator(".provider-management-actions");
    await expect(actions.locator(":scope > button")).toHaveText(["测试", "编辑", "配置路由", "删除"]);
    await expect(actions.locator(".provider-management-more")).toHaveCount(0);
    await expectActionControlsWithinRow(actions);
    const search = page.getByPlaceholder("搜索名称、ID、状态");
    await search.fill("Internal");
    await expect(listing.getByRole("row").filter({ hasText: "UI Local Cluster" })).toHaveCount(0);
    await listing.getByRole("button", { name: "可用性监控", exact: true }).click();
    await expect(listing).not.toHaveClass(/provider-channel-list-manage/);
    await expect(listing.getByRole("columnheader", { name: "账号配额" })).toBeVisible();
    await expect(listing.getByText("待观测", { exact: true })).toHaveCount(1);
    await expect(search).toHaveValue("Internal");
    await expect(listing.getByRole("row").filter({ hasText: "UI Internal Cluster" })).toBeVisible();
    await capture(page, testInfo, listing, `providers-monitoring-${mobile ? "mobile" : "desktop"}`, "可用性监控：无观测数据时保持待观测状态", "viewport");
    await listing.getByRole("button", { name: "供应商列表", exact: true }).click();
    await expect(search).toHaveValue("Internal");
    await expect(listing.getByRole("row").filter({ hasText: "UI Local Cluster" })).toHaveCount(0);
    await expect(listing.getByRole("row").filter({ hasText: "UI Internal Cluster" })).toBeVisible();
  });
}

async function expectActionControlsWithinRow(actions: Locator) {
  await expect.poll(() => actions.evaluate(element => {
    const container = element.getBoundingClientRect();
    const controls = Array.from(element.querySelectorAll<HTMLElement>(":scope > button, :scope > details > summary"));
    const bounds = controls.map(control => control.getBoundingClientRect());
    return {
      contained: bounds.every(rect => rect.width > 0 && rect.left >= container.left - 1 && rect.right <= container.right + 1),
      oneRow: bounds.every(rect => Math.abs((rect.top + rect.bottom) / 2 - (bounds[0].top + bounds[0].bottom) / 2) <= 1),
      separated: bounds.every((rect, index) => index === 0 || rect.left >= bounds[index - 1].right),
    };
  }), { message: "Visible provider actions must fit on one row without clipping or overlap" }).toEqual({ contained: true, oneRow: true, separated: true });
}

for (const locale of [
  { language: "zh-CN", option: "简体中文", labels: ["测试", "编辑", "配置路由", "删除"] },
  { language: "en", option: "English", labels: ["Test", "Edit", "Configure Routes", "Delete"] },
  { language: "ja", option: "日本語", labels: ["テスト", "編集", "ルート設定", "削除"] },
]) {
  test(`providers responsive-management-actions ${locale.language}`, async ({ page, api }, testInfo) => {
    await page.setViewportSize({ width: 1800, height: 1000 });
    installProviders(api);
    await page.goto("/providers");
    const listing = page.locator(".provider-channel-list");
    const row = listing.getByRole("row").filter({ hasText: "UI Internal Cluster" });
    const actions = row.locator(".provider-management-actions");
    await expect(actions.locator(":scope > button")).toHaveCount(4);
    const providerReads = () => api.calls.filter(call => call.path === "/api/admin/providers").length;
    const initialProviderReads = providerReads();
    await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
    await page.getByRole("option", { name: locale.option, exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    await expect(actions.locator(":scope > button")).toHaveText(locale.labels);
    await expect(actions.locator(".provider-management-more")).toHaveCount(0);
    await expectActionControlsWithinRow(actions);
    const naturalActionWidth = await actions.locator(":scope > button").evaluateAll(buttons => buttons.reduce((total, button) => total + button.getBoundingClientRect().width, 0));
    await capture(page, testInfo, listing, `providers-actions-wide-${locale.language}`, "宽屏直接展示全部供应商操作");

    for (const viewport of [{ name: "narrow", width: 1024, height: 1000 }, { name: "mobile", width: 390, height: 844 }]) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      if (viewport.name === "mobile" && locale.language !== "zh-CN") {
        await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
        await page.getByRole("option", { name: "简体中文", exact: true }).click();
        await expect(actions.locator(":scope > button")).toHaveText(["测试", "编辑", "配置路由", "删除"]);
        await expect(actions.locator(".provider-management-more")).toHaveCount(0);
        await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
        await page.getByRole("option", { name: locale.option, exact: true }).click();
        await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
      }
      const available = await actions.evaluate(element => ({ width: element.getBoundingClientRect().width, gap: Number.parseFloat(getComputedStyle(element).columnGap) || 0 }));
      const allActionsFit = naturalActionWidth + available.gap * (locale.labels.length - 1) <= available.width;
      if (viewport.name === "narrow") expect(allActionsFit, "The narrow desktop scenario must exercise actual action overflow").toBe(false);
      await expect(actions.locator("button[data-action-id]")).toHaveText(locale.labels);
      if (allActionsFit) {
        await expect(actions.locator(":scope > button")).toHaveText(locale.labels);
        await expect(actions.locator(".provider-management-more")).toHaveCount(0);
      } else {
        await expect.poll(() => actions.locator(":scope > button").count()).toBeLessThan(4);
        const more = actions.locator(".provider-management-more");
        await expect(more.locator("summary")).toBeVisible();
        const overflow = more.locator(":scope > div");
        await expect(overflow).toBeHidden();
        const before = await row.boundingBox();
        expect(before).not.toBeNull();
        await more.locator("summary").click();
        await expect(overflow).toBeVisible();
        await expect(overflow).toHaveCSS("position", "absolute");
        const after = await row.boundingBox();
        expect(after).not.toBeNull();
        expect(Math.abs(after!.height - before!.height), "Opening More must not change the provider row height").toBeLessThanOrEqual(1);
        const inlineCount = await actions.locator(":scope > button").count();
        await expect(overflow.getByRole("button")).toHaveText(locale.labels.slice(inlineCount));
      }
      await expectActionControlsWithinRow(actions);
      await capture(page, testInfo, listing, `providers-actions-${viewport.name}-${locale.language}`, "根据实际可用宽度收纳供应商操作", "viewport");
      if (!allActionsFit) {
        await actions.locator(".provider-management-more > summary").click();
        await expect(actions.locator(".provider-management-more > div")).toBeHidden();
      }
    }

    await page.setViewportSize({ width: 1800, height: 1000 });
    await expect(actions.locator(":scope > button")).toHaveText(locale.labels);
    await expect(actions.locator(".provider-management-more")).toHaveCount(0);
    await expectActionControlsWithinRow(actions);
    expect(providerReads(), "Resizing and changing language must not reload provider data").toBe(initialProviderReads);
  });
}
