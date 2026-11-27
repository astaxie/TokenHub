import { act, render, renderHook, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { OverviewStatus, useOverviewSummaryAvailable } from "./overview-status";

describe("OverviewStatus", () => {
  it("distinguishes preparing, stale, and unavailable statistics", () => {
    setActiveLanguage("zh-CN");
    const data = emptyData();
    const view = render(<OverviewStatus data={{ ...data, summaryStatus: { state: "preparing", processed: 1234 } }} />);
    expect(screen.getByRole("status")).toHaveTextContent("历史统计准备中");
    view.rerender(<OverviewStatus data={{ ...data, summaryStatus: { state: "stale", updated_at: new Date().toISOString() } }} />);
    expect(screen.getByRole("status")).toHaveTextContent("统计刷新失败");
    view.rerender(<OverviewStatus data={{ ...data, summaryStatus: { state: "error" } }} />);
    expect(screen.getByRole("status")).toHaveTextContent("统计暂时不可用");
  });
});

it("hides a stale result at the cutoff while a refresh is still pending", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
  const status = { state: "stale" as const, updated_at: "2026-01-01T00:00:00Z" };
  const hook = renderHook(() => useOverviewSummaryAvailable(status));
  expect(hook.result.current).toBe(true);
  act(() => vi.advanceTimersByTime(60000));
  expect(hook.result.current).toBe(false);
  hook.unmount();
  vi.useRealTimers();
});
