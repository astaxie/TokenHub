import type { Locator, Page } from "@playwright/test";
import type { Model, ModelRoute, Provider, UsagePoint } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { model, shellResponses } from "./fixtures/shell";
import type { MockAPI } from "./network";

const publishedModel: Model = {
  ...model,
  metadata: { directory_role: "external" },
  capabilities: ["chat", "tools"],
  supported_parameters: ["temperature", "max_tokens"],
};
const usage: UsagePoint = {
  date: "2026-09-07", request_count: 3, input_tokens: 1000000,
  output_tokens: 10000, total_tokens: 1010000, estimated_cost_usd: 1.08,
};
const provider: Provider = { id: "prv_ui_console", name: "UI Console Provider", type: "mock", priority: 1, status: "active", healthy: true };
const route: ModelRoute = { id: "route_ui_console", model_name: publishedModel.name, provider_id: provider.id, provider_model: "ui-console-upstream", priority: 1, weight: 100, status: "active", strategy: "priority_weighted" };

function installConsole(api: MockAPI, models: Model[] = [publishedModel]) {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, models, providers: models.length ? [provider] : [] });
  api.respond("GET", "/api/admin/usage/timeseries", { data: [usage] });
  api.respond("GET", "/api/admin/plugins", { data: [] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-chain", { data: { hooks: [] } });
  api.respond("GET", "/api/admin/plugin-ui-manifest", { data: [] });
  api.respond("GET", "/api/admin/plugin-actions", { data: [] });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.respond("GET", "/api/admin/resources/announcements", { data: [] });
  api.respond("GET", "/api/admin/routing-rules", { data: models.length ? [route] : [] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({
    json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 } },
  }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
}

async function expectHorizontalFit(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "The console must fit the viewport horizontally").toBe(true);
  expect(await page.locator(".content-panel").evaluate(element => element.scrollWidth <= element.clientWidth + 1), "The content panel must not hide horizontal overflow").toBe(true);
}

async function expectIcon(control: Locator) {
  await expect(control).toBeVisible();
  await expect(control.locator("svg").first()).toBeVisible();
}

for (const viewport of [
  { name: "desktop", width: 1440, height: 1000 },
  { name: "mobile", width: 390, height: 844 },
]) {
  test(`console-responsive ${viewport.name} navigation, icons and model details`, async ({ page, api }, info) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    installConsole(api);
    await page.goto("/overview");
    const shell = page.locator(".app-shell");
    const topbar = page.locator(".topbar");
    await expect(page.getByRole("heading", { name: "网关概览", exact: true })).toBeVisible();
    const requests = page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) });
    await expect(requests.locator(".metric-value")).toHaveText("3");
    await expectIcon(requests.locator(".metric-label"));
    await expectHorizontalFit(page);
    await capture(page, info, shell, `console-overview-${viewport.name}`, "控制台：总览内容与图标", "viewport");

    const theme = topbar.getByRole("button", { name: "切换主题", exact: true });
    await expectIcon(theme);
    await expect(theme).toBeInViewport({ ratio: 1 });
    await theme.click();
    await expect(shell).toHaveAttribute("data-theme", "dark");
    await expectIcon(theme);
    await theme.click();
    await expect(shell).toHaveAttribute("data-theme", "light");

    const language = topbar.getByRole("button", { name: "界面语言", exact: true });
    await expectIcon(language);
    await expect(language).toBeInViewport({ ratio: 1 });
    await language.click();
    await page.getByRole("option", { name: "English", exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.getByRole("heading", { name: "Gateway Overview", exact: true })).toBeVisible();
    await topbar.getByRole("button", { name: "Interface Language", exact: true }).click();
    await page.getByRole("option", { name: "简体中文", exact: true }).click();

    if (viewport.name === "desktop") {
      const sidebar = page.locator(".sidebar");
      const logo = sidebar.getByRole("img", { name: "TokenHub", exact: true });
      await expect(logo).toBeVisible();
      expect(await logo.evaluate(element => (element as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
      await expectIcon(sidebar.getByRole("button", { name: "折叠菜单", exact: true }));
      await sidebar.getByRole("button", { name: "折叠菜单", exact: true }).click();
      await expect(sidebar).toHaveClass(/collapsed/);
      await expectIcon(sidebar.getByRole("button", { name: "模型目录", exact: true }));
      await sidebar.locator(".brand").hover();
      await expect(sidebar.getByRole("button", { name: "展开菜单", exact: true })).toHaveCSS("opacity", "1");
      await sidebar.getByRole("button", { name: "展开菜单", exact: true }).click();
      await expect(sidebar).not.toHaveClass(/collapsed/);
    }

    const trend = page.locator(".overview-trend-panel");
    await trend.getByRole("button", { name: "成本", exact: true }).click();
    await expect(trend.locator(".overview-panel-head strong")).toHaveText("$1.08");
    await expect(trend.getByRole("img", { name: "成本与用量趋势", exact: true })).toBeVisible();
    await trend.scrollIntoViewIfNeeded();
    await capture(page, info, trend, `console-trend-${viewport.name}`, "控制台：成本趋势切换", "viewport");

    const search = topbar.getByRole("textbox");
    await expectIcon(topbar.locator(".top-search"));
    await search.fill(publishedModel.name);
    const result = page.getByRole("option", { name: new RegExp(publishedModel.name) });
    await expectIcon(result);
    await result.click();
    await expect(page).toHaveURL(/\/models$/);
    await expect(page.getByRole("heading", { name: "对外模型目录", exact: true })).toBeVisible();
    await expectHorizontalFit(page);
    const detailTrigger = page.getByRole("button", { name: `查看模型详情：${publishedModel.name}`, exact: true });
    await expect(detailTrigger).toBeVisible();
    await capture(page, info, shell, `console-models-${viewport.name}`, "控制台：通过搜索打开模型目录", "viewport");
    await detailTrigger.click();
    const detail = page.getByRole("dialog", { name: "模型详情", exact: true });
    await expect(detail).toBeFocused();
    await expect(detail.getByText("tools", { exact: true })).toBeVisible();
    await expect(detail.getByText("max_tokens", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(detail).toHaveCount(0);
    await expect(detailTrigger).toBeFocused();
    await page.getByRole("navigation", { name: "当前位置" }).getByRole("button", { name: "TokenHub", exact: true }).click();
    await expect(page).toHaveURL(/\/overview$/);
    await expect(page.getByRole("heading", { name: "网关概览", exact: true })).toBeVisible();
  });
}

test("console-responsive mobile model empty state remains readable", async ({ page, api }, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  installConsole(api, []);
  await page.goto("/models");
  await expect(page.getByText("先引入可用的 Provider 模型", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "前往 Provider 渠道", exact: true })).toBeEnabled();
  await expectHorizontalFit(page);
  await capture(page, info, page.locator(".content-panel"), "console-models-empty-mobile", "控制台：移动端模型空状态", "viewport");
});
