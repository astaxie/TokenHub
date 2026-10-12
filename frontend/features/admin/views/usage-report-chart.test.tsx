import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { UsageReport } from "../core/usage-report-types";
import { setActiveLanguage } from "../i18n/runtime";
import { UsageReportChart, UsageReportMetrics } from "./usage-report-chart";

const report: UsageReport = {
  range: "7d", timezone: "America/Los_Angeles", granularity: "day", window_start: "2026-10-02T07:00:00Z", window_end: "2026-10-09T07:00:00Z",
  summary: { request_count: 7, input_tokens: 1200, cached_input_tokens: 300, output_tokens: 400, reasoning_output_tokens: 100, total_tokens: 1600, estimated_cost_usd: 0.02, errors: 2 },
  breakdown: { projects: [], models: [], members: [], providers: [], provider_resources: [], cost_centers: [] },
  timeseries: [{ date: "2026-10-08", request_count: 7, input_tokens: 1200, cached_input_tokens: 300, output_tokens: 400, total_tokens: 1600, estimated_cost_usd: 0.02 }],
};

describe("Usage report metrics and chart", () => {
  it("keeps cached and reasoning tokens within the reported input and output totals", () => {
    render(<><UsageReportMetrics report={report} /><UsageReportChart report={report} /></>);
    expect(within(screen.getByRole("group", { name: "Token 消耗" })).getByTitle("1,600")).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "缓存读取" })).getByText("缓存命中率 25%")).toBeVisible();
    expect(within(screen.getByRole("group", { name: "推理 Token" })).getByText("已计入输出 Token")).toBeVisible();
    expect(within(screen.getByRole("group", { name: "请求数" })).getByText("错误 2 次")).toBeVisible();
    const bar = screen.getByRole("button", { name: /输入 1,200，输出 400，请求 7/ });
    expect(bar.querySelector(".global-usage-bar-input")).toHaveStyle({ height: "75%" });
    expect(bar.querySelector(".global-usage-bar-output")).toHaveStyle({ height: "25%" });
    fireEvent.focus(bar);
    expect(bar).toHaveAttribute("aria-pressed", "true");
    expect(document.querySelector(".global-usage-chart-selection")).toHaveTextContent("输入 1,200，输出 400，请求 7");
  });

  it("preserves calendar dates and makes every bucket available in a data table", () => {
    setActiveLanguage("en");
    render(<UsageReportChart report={report} />);
    expect(screen.getByRole("button", { name: /Oct 8, 2026: input/ })).toBeVisible();
    fireEvent.click(screen.getByText("View trend data"));
    expect(screen.getByRole("table", { name: "Token usage trend" })).toBeVisible();
    expect(screen.getByRole("rowheader", { name: "Oct 8, 2026" })).toBeVisible();
  });

  it("distinguishes repeated daylight-saving hours in the reporting timezone", () => {
    setActiveLanguage("en");
    const point = report.timeseries[0];
    render(<UsageReportChart report={{ ...report, range: "today", timezone: "America/New_York", granularity: "hour", timeseries: [
      { ...point, date: "2026-11-01T05:00:00Z" }, { ...point, date: "2026-11-01T06:00:00Z" },
    ] }} />);
    expect(screen.getByRole("button", { name: /01:00 AM GMT-4/ })).toBeVisible();
    expect(screen.getByRole("button", { name: /01:00 AM GMT-5/ })).toBeVisible();
  });

  it("handles zero usage without invalid percentages and still charts failed requests", () => {
    const empty = { ...report, summary: { ...report.summary, input_tokens: 0, output_tokens: 0, total_tokens: 0, cached_input_tokens: 0 }, timeseries: [] };
    const { rerender } = render(<><UsageReportMetrics report={empty} /><UsageReportChart report={empty} /></>);
    expect(screen.getByText("缓存命中率 0%")).toBeVisible();
    expect(screen.getByText("此时间范围内暂无用量")).toBeVisible();
    rerender(<UsageReportChart report={{ ...empty, timeseries: [{ ...report.timeseries[0], input_tokens: 0, output_tokens: 0, total_tokens: 0 }] }} />);
    expect(screen.queryByText("此时间范围内暂无用量")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /输入 0，输出 0，请求 7/ })).toBeVisible();
  });

  it("formats monthly buckets without shifting to the preceding month", () => {
    setActiveLanguage("en");
    render(<UsageReportChart report={{ ...report, range: "all", granularity: "month", timeseries: [{ ...report.timeseries[0], date: "2026-10-01" }] }} />);
    expect(screen.getByRole("button", { name: /Oct 2026: input/ })).toBeVisible();
  });
});
