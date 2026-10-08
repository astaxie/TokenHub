import type { AppData, Summary, UsageBreakdown, UsageBreakdownRow } from "../../features/admin/core/types";
import { test, expect, capture, section } from "./harness";
import { project, user } from "./fixtures/shell";

for (const catalogAvailable of [true, false]) {
  const scenario = catalogAvailable ? "named-project" : "unavailable-project";
  test(`usage-attribution ${scenario}`, async ({ page, api }, testInfo) => {
    const rows: UsageBreakdownRow[] = [project.id, "prj_archived", "unknown", "prj_default"].map((id) => ({
      id, request_count: 2, input_tokens: 100, cached_input_tokens: 20,
      output_tokens: 50, total_tokens: 150, estimated_cost_usd: 0.25,
    }));
    const summary: Summary = { request_count: 8, input_tokens: 400, cached_input_tokens: 80, output_tokens: 200, total_tokens: 600, estimated_cost_usd: 1, errors: 0 };
    const breakdown: UsageBreakdown = { projects: rows, models: [], members: [], providers: [], provider_resources: [], cost_centers: [], api_keys: [] };
    const daily: AppData["dailyUsage"] = {
      date: "2026-09-07", timezone: "UTC", window_start: "2026-09-07T00:00:00Z", window_end: "2026-09-08T00:00:00Z", summary, breakdown,
    };
    api.replaceResponse("GET", "/api/admin/overview", { summary, projects: catalogAvailable ? [project] : [], models: [], providers: [], provider_resources: [], alerts: [] });
    api.replaceResponse("GET", "/api/admin/usage/breakdown", breakdown);
    api.respond("GET", "/api/admin/usage/daily", daily);
    api.respond("GET", "/api/admin/users", { data: [user] });
    for (const path of ["api-keys", "usage/timeseries", "resources/teams", "resources/cost-centers", "plugin-ui-manifest", "plugin-actions", "plugin-background-jobs"]) {
      api.respond("GET", `/api/admin/${path}`, { data: [] });
    }

    await page.goto("/usage");
    const attribution = section(page, "项目归因");
    const expectedName = catalogAvailable ? project.name : project.id;
    await expect(attribution.getByRole("row", { name: `${expectedName} 2 150 20 20.0% $0.250000`, exact: true })).toBeVisible();
    await expect(attribution.getByRole("cell", { name: "prj_archived", exact: true })).toBeVisible();
    await expect(attribution.getByRole("cell", { name: "unknown", exact: true })).toBeVisible();
    await expect(attribution.getByRole("cell", { name: "默认项目空间", exact: true })).toBeVisible();
    if (catalogAvailable) await expect(attribution.getByRole("cell", { name: project.id, exact: true })).toHaveCount(0);
    await capture(page, testInfo, attribution, `usage-attribution-${scenario}`, catalogAvailable ? "项目归因：项目名称与历史 ID" : "项目归因：无法解析名称时保留 ID");
  });
}
