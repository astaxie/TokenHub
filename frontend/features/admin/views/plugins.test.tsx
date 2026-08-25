import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

describe("PluginsView", () => {
  it("renders background job descriptors", () => {
    const data = emptyData();
    data.pluginBackgroundJobs = [{
      plugin_id: "tokenhub.jobs",
      job_id: "quota.refresh",
      title: "Refresh quota",
      capability: "quota.refresh",
      subject: "openai_codex",
      schedule: "*/10 * * * *",
      max_concurrency: 1,
      retry: { max_attempts: 2, backoff_millis: 1000 },
    }];
    data.pluginBackgroundRuns = [{
      plugin_id: "tokenhub.jobs",
      job_id: "quota.refresh",
      trigger: "schedule",
      status: "succeeded",
      attempts: 1,
      started_at: "2026-08-26T10:00:00Z",
      completed_at: "2026-08-26T10:00:01Z",
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("后台任务清单")).toBeInTheDocument();
    expect(screen.getAllByText("quota.refresh")).toHaveLength(2);
    expect(screen.getByText("*/10 * * * *")).toBeInTheDocument();
    expect(screen.getByText("成功 / 1")).toBeInTheDocument();
    expect(screen.getByText("2 / 1000ms")).toBeInTheDocument();
  });

  it("renders SIM theme and layout contributions", () => {
    const data = emptyData();
    data.pluginUI = [
      {
        plugin_id: "tokenhub.sim.enterprise",
        id: "enterprise-theme",
        slot: "theme.tokens",
        title: "Enterprise Theme",
        schema: {
          mode: "dark",
          tokens: {
            accent: "#2563eb",
            surface: "#ffffff",
          },
        },
      },
      {
        plugin_id: "tokenhub.sim.enterprise",
        id: "ops-layout",
        slot: "layout.preset",
        title: "Ops Layout",
        schema: {
          preset: { density: "compact" },
        },
      },
    ];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("SIM 与主题贡献")).toBeInTheDocument();
    expect(screen.getAllByText("Enterprise Theme").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Ops Layout").length).toBeGreaterThan(0);
    expect(screen.getByText("深色")).toBeInTheDocument();
    expect(screen.getByText("紧凑")).toBeInTheDocument();
  });
});
