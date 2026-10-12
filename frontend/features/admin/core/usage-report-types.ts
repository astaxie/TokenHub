import type { Summary, UsageBreakdown, UsagePoint } from "./types";

export type UsageRange = "today" | "7d" | "30d" | "all";

export type UsageReport = {
  range: UsageRange;
  timezone: string;
  window_start: string;
  window_end: string;
  granularity: "hour" | "day" | "month";
  summary: Summary;
  breakdown: UsageBreakdown;
  timeseries: UsagePoint[];
};
