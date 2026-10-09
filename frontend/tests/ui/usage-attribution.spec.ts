import type { Summary, UsageBreakdown, UsageBreakdownRow } from "../../features/admin/core/types";
import { test, expect, capture, section } from "./harness";
import { fixedTime, project } from "./fixtures/shell";
import { installUsageFixtures } from "./fixtures/global-usage";

for (const catalogAvailable of [true, false]) {
  const scenario = catalogAvailable ? "named-project" : "unavailable-project";
  test(`usage-attribution ${scenario}`, async ({ page, api }, testInfo) => {
    const rows: UsageBreakdownRow[] = [project.id, "prj_archived", "unknown", "prj_default"].map((id) => ({
      id, request_count: 2, input_tokens: 100, cached_input_tokens: 20,
      output_tokens: 50, total_tokens: 150, estimated_cost_usd: 0.25,
    }));
    const summary: Summary = { request_count: 8, input_tokens: 400, cached_input_tokens: 80, output_tokens: 200, total_tokens: 600, estimated_cost_usd: 1, errors: 0 };
    const breakdown: UsageBreakdown = { projects: rows, models: [], members: [], providers: [], provider_resources: [], cost_centers: [], api_keys: [] };
    api.replaceResponse("GET", "/api/admin/overview", { summary, projects: catalogAvailable ? [project] : [], models: [], providers: [], provider_resources: [], alerts: [] });
    installUsageFixtures(api, range => ({ json: {
      range, timezone: "UTC", window_start: "2026-09-07T00:00:00Z", window_end: fixedTime,
      granularity: "hour", summary, breakdown, timeseries: [],
    } }));

    await page.goto("/usage");
    const attribution = section(page, "项目归因");
    const expectedName = catalogAvailable ? project.name : project.id;
    const cost = new Intl.NumberFormat("zh-CN", { style: "currency", currency: "USD" }).format(0.25);
    await expect(attribution.getByRole("row", { name: `${expectedName} 2 150 20 ${cost}`, exact: true })).toBeVisible();
    await expect(attribution.getByRole("cell", { name: "prj_archived", exact: true })).toBeVisible();
    await expect(attribution.getByRole("cell", { name: "unknown", exact: true })).toBeVisible();
    await expect(attribution.getByRole("cell", { name: "默认项目空间", exact: true })).toBeVisible();
    if (catalogAvailable) await expect(attribution.getByRole("cell", { name: project.id, exact: true })).toHaveCount(0);
    await capture(page, testInfo, attribution, `usage-attribution-${scenario}`, catalogAvailable ? "项目归因：项目名称与历史 ID" : "项目归因：无法解析名称时保留 ID");
  });
}
