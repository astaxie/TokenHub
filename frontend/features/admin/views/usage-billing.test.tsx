import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { UsageReport } from "../core/usage-report-types";
import { emptyData } from "../domain/catalog";
import { DailyUsageSection, UsageView } from "./usage-billing";

describe("UsageView project attribution", () => {
  it.each(["admin", "user", "team_leader"] as const)("resolves project names and preserves unavailable identifiers for %s", async (role) => {
    const data = emptyData();
    data.projects = [{ id: "prj_design", name: "Design Platform", status: "active" }];
    data.breakdown.projects = ["prj_design", "prj_archived", "unknown", "prj_default"].map((id) => ({
      id, request_count: 2, input_tokens: 100, cached_input_tokens: 20,
      output_tokens: 50, total_tokens: 150, estimated_cost_usd: 0.25,
    }));
    const user = { id: "usr_reviewer", username: "reviewer", name: "Usage Reviewer", email: "reviewer@example.test", role, status: "active" };
    data.users = [user];
    data.breakdown.members = [{ ...data.breakdown.projects[0], id: user.id }];

    const report: UsageReport = {
      range: "today", timezone: "UTC", window_start: "2026-09-07T00:00:00Z", window_end: "2026-09-07T02:00:00Z",
      granularity: "hour", summary: data.summary, breakdown: data.breakdown, timeseries: [],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(report))));
    render(<UsageView api={{ baseURL: "http://example.test", adminToken: "synthetic-session" }} data={data} user={user} />);

    const projectSection = (await screen.findByRole("heading", { name: "项目归因" })).closest("section")!;
    const projects = within(projectSection);
    const cost = new Intl.NumberFormat("zh-CN", { style: "currency", currency: "USD" }).format(0.25);
    expect(projects.getByRole("row", { name: `Design Platform 2 150 20 ${cost}` })).toBeInTheDocument();
    expect(projects.queryByRole("cell", { name: "prj_design" })).not.toBeInTheDocument();
    expect(projects.getByRole("cell", { name: "prj_archived" })).toBeInTheDocument();
    expect(projects.getByRole("cell", { name: "unknown" })).toBeInTheDocument();
    expect(projects.getByRole("cell", { name: "默认项目空间" })).toBeInTheDocument();
    if (role === "team_leader") {
      fireEvent.click(screen.getByText("更多用量明细"));
      const members = screen.getByRole("heading", { name: "成员用量" }).closest("section")!;
      expect(within(members).getByRole("cell", { name: "Usage Reviewer / reviewer@example.test" })).toBeInTheDocument();
    }
  });
});

describe("DailyUsageSection", () => {
  it("shows mutually exclusive daily token type rows", () => {
    const data = emptyData();
    data.dailyUsage.summary = {
      ...data.dailyUsage.summary,
      input_tokens: 100,
      cached_input_tokens: 20,
      cache_write_input_tokens: 5,
      output_tokens: 50,
      total_tokens: 150,
    };

    render(<DailyUsageSection data={data} user={{ id: "usr_admin", username: "admin", name: "Admin", email: "admin@example.test", role: "admin", status: "active" }} />);

    const table = screen.getByRole("heading", { name: "今日 Token 类型" }).closest("article");
    expect(table).not.toBeNull();
    expect(within(table as HTMLElement).getByRole("row", { name: "输入 75" })).toBeInTheDocument();
    expect(within(table as HTMLElement).getByRole("row", { name: "缓存读 20" })).toBeInTheDocument();
    expect(within(table as HTMLElement).getByRole("row", { name: "缓存写 5" })).toBeInTheDocument();
    expect(within(table as HTMLElement).getByRole("row", { name: "输出 50" })).toBeInTheDocument();
  });

  it("hides provider daily breakdowns from team leaders", () => {
    const data = emptyData();
    data.dailyUsage.breakdown.providers = [{
      id: "provider_sensitive",
      request_count: 1,
      input_tokens: 10,
      cached_input_tokens: 0,
      output_tokens: 5,
      total_tokens: 15,
      estimated_cost_usd: 12.34,
    }];
    data.dailyUsage.breakdown.provider_resources = [{
      id: "resource_sensitive",
      request_count: 1,
      input_tokens: 10,
      cached_input_tokens: 0,
      output_tokens: 5,
      total_tokens: 15,
      estimated_cost_usd: 12.34,
    }];

    render(<DailyUsageSection data={data} user={{ id: "usr_leader", username: "leader", name: "Leader", email: "leader@example.test", role: "team_leader", status: "active" }} />);

    expect(screen.queryByRole("heading", { name: "今日 Provider 用量" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "今日资源账号用量" })).not.toBeInTheDocument();
    expect(screen.queryByText("provider_sensitive")).not.toBeInTheDocument();
    expect(screen.queryByText("resource_sensitive")).not.toBeInTheDocument();
  });
});
