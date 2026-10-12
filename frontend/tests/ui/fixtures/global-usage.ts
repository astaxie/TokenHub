import type { Summary, UsageBreakdownRow } from "../../../features/admin/core/types";
import type { UsageRange, UsageReport } from "../../../features/admin/core/usage-report-types";
import { expect } from "../harness";
import type { MockAPI, MockResponse } from "../network";
import { fixedTime, project, user } from "./shell";

export const usageRanges = ["today", "7d", "30d", "all"] as const;
export const usageCounts: Record<UsageRange, number> = { today: 3, "7d": 17, "30d": 43, all: 127 };
const starts: Record<UsageRange, string> = {
  today: "2026-09-06T16:00:00Z", "7d": "2026-08-31T16:00:00Z",
  "30d": "2026-08-08T16:00:00Z", all: "2026-01-01T02:00:00Z",
};

export function usageReport(range: UsageRange, empty = false): UsageReport {
  const count = empty ? 0 : usageCounts[range];
  const summary: Summary = {
    request_count: count, input_tokens: count * 1000, output_tokens: count * 200,
    total_tokens: count * 1200, cached_input_tokens: count * 400,
    cache_write_input_tokens: count * 50, reasoning_output_tokens: count * 40,
    estimated_cost_usd: count * 0.5, errors: 0, usage_record_count: count,
  };
  const row = (id: string): UsageBreakdownRow => ({ id, ...summary });
  const granularity = range === "today" ? "hour" : range === "all" ? "month" : "day";
  const buckets = { today: 10, "7d": 7, "30d": 30, all: 9 }[range];
  const first = new Date(starts[range]);
  const firstCalendarDay = new Date(first.getTime() + 8 * 3600000);
  return {
    range, timezone: "Asia/Shanghai", window_start: starts[range], window_end: fixedTime,
    granularity, summary,
    breakdown: {
      models: empty ? [] : [row(`ui-${range}-model-with-a-long-enterprise-deployment-name`)],
      projects: empty ? [] : [row(project.id)], api_keys: [], members: [],
      providers: [], provider_resources: [], cost_centers: [],
    },
    timeseries: empty ? [] : Array.from({ length: buckets }, (_, index) => {
      const date = new Date(granularity === "hour" ? first : firstCalendarDay);
      if (granularity === "month") date.setUTCMonth(date.getUTCMonth() + index);
      else date.setTime(date.getTime() + index * (granularity === "hour" ? 3600000 : 86400000));
      const requestCount = Math.floor(count / buckets) + (index < count % buckets ? 1 : 0);
      return {
        date: granularity === "hour" ? date.toISOString() : date.toISOString().slice(0, 10),
        request_count: requestCount, input_tokens: requestCount * 1000, output_tokens: requestCount * 200,
        total_tokens: requestCount * 1200, cached_input_tokens: requestCount * 400, estimated_cost_usd: requestCount * 0.5,
      };
    }),
  };
}

type UsageHandler = (range: UsageRange) => MockResponse | Promise<MockResponse>;
export function installUsageFixtures(api: MockAPI, handler: UsageHandler = range => ({ json: usageReport(range) })) {
  api.respond("GET", "/api/admin/api-keys", { data: [] });
  api.respond("GET", "/api/admin/users", { data: [user] });
  for (const path of ["plugin-ui-manifest", "plugin-actions", "resources/teams", "resources/cost-centers"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.define("GET", "/api/admin/usage/report", input => handler(input.query.get("range") as UsageRange), query => {
    expect(query.size).toBe(1);
    expect(usageRanges).toContain(query.get("range"));
  });
}

export function usageCalls(api: MockAPI) {
  return api.calls.filter(call => call.path === "/api/admin/usage/report");
}
