import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AdminUser } from "../core/types";
import type { UsageRange, UsageReport } from "../core/usage-report-types";
import { emptyData } from "../domain/catalog";
import { GlobalUsageView } from "./global-usage";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-session" };
const admin: AdminUser = { id: "ui-admin", username: "ui-admin", name: "UI Admin", email: "ui@example.test", role: "admin", status: "active" };

function report(range: UsageRange, requests: number): UsageReport {
  const data = emptyData();
  return { range, timezone: "America/Chicago", window_start: "2026-10-08T05:00:00Z", window_end: "2026-10-08T09:00:00Z", granularity: "day", summary: { ...data.summary, request_count: requests }, breakdown: data.breakdown, timeseries: [] };
}
function response(value: UsageReport) { return new Response(JSON.stringify(value)); }
function pending<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
function requests() { return within(screen.getByRole("group", { name: "请求数" })).getByText(/^[0-9]+$/); }

afterEach(() => { vi.useRealTimers(); });

describe("Global usage ranges", () => {
  it("hides previous range totals and ignores an older response after rapid switching", async () => {
    const user = userEvent.setup();
    const week = pending<Response>();
    const month = pending<Response>();
    const fetch = vi.fn((input: string) => input.endsWith("range=7d") ? week.promise : input.endsWith("range=30d") ? month.promise : Promise.resolve(response(report("today", 11))));
    vi.stubGlobal("fetch", fetch);
    render(<GlobalUsageView api={api} data={emptyData()} user={admin} />);
    await waitFor(() => expect(requests()).toHaveTextContent("11"));
    await user.click(screen.getByRole("button", { name: "7 天" }));
    expect(screen.queryByRole("group", { name: "请求数" })).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("正在加载用量…");
    await user.click(screen.getByRole("button", { name: "30 天" }));
    await act(async () => { month.resolve(response(report("30d", 33))); });
    expect(requests()).toHaveTextContent("33");
    await act(async () => { week.resolve(response(report("7d", 22))); });
    expect(requests()).toHaveTextContent("33");
    expect(screen.getByRole("button", { name: "30 天" })).toHaveAttribute("aria-pressed", "true");
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([`${api.baseURL}/api/admin/usage/report?range=today`, `${api.baseURL}/api/admin/usage/report?range=7d`, `${api.baseURL}/api/admin/usage/report?range=30d`]);
  });

  it("shows request errors without treating failures as zero usage and retries the selected range", async () => {
    const user = userEvent.setup();
    const fetch = vi.fn().mockResolvedValueOnce(response(report("today", 11)))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: "Synthetic report unavailable" } }), { status: 503 }))
      .mockResolvedValueOnce(response(report("all", 44)));
    vi.stubGlobal("fetch", fetch);
    render(<GlobalUsageView api={api} data={emptyData()} user={admin} />);
    await waitFor(() => expect(requests()).toHaveTextContent("11"));
    await user.click(screen.getByRole("button", { name: "全部" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Synthetic report unavailable");
    expect(screen.queryByRole("group", { name: "请求数" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(requests()).toHaveTextContent("44"));
    expect(fetch.mock.calls.at(-1)?.[0]).toBe(`${api.baseURL}/api/admin/usage/report?range=all`);
  });

  it("refreshes on a timer without overlapping requests and aborts on unmount", async () => {
    vi.useFakeTimers();
    const first = pending<Response>();
    const fetch = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(response(report("today", 2)));
    vi.stubGlobal("fetch", fetch);
    const { unmount } = render(<GlobalUsageView api={api} data={emptyData()} user={admin} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetch).toHaveBeenCalledTimes(1);
    await act(async () => { first.resolve(response(report("today", 1))); });
    expect(requests()).toHaveTextContent("1");
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(requests()).toHaveTextContent("2");
    const signal = fetch.mock.calls.at(-1)?.[1].signal as AbortSignal;
    unmount();
    expect(signal.aborted).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("keeps restricted provider attribution out of personal usage details", async () => {
    const payload = report("today", 1);
    payload.breakdown.providers = [{ id: "private-provider", request_count: 1, input_tokens: 10, output_tokens: 5, total_tokens: 15, estimated_cost_usd: 2 }];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(payload)));
    render(<GlobalUsageView api={api} data={emptyData()} user={{ ...admin, role: "user" }} />);
    await waitFor(() => expect(requests()).toHaveTextContent("1"));
    fireEvent.click(screen.getByText("更多用量明细"));
    expect(screen.queryByText("private-provider")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "供应商用量" })).not.toBeInTheDocument();
  });
});
