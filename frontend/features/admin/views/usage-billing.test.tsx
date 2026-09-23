import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { DailyUsageSection } from "./usage-billing";

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
