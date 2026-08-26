import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

describe("PluginsView", () => {
  it("renders plugin distribution metadata", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Provider",
      version: "1.2.3",
      source: "marketplace",
      status: "disabled",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [{ kind: "provider", name: "kimi_subscription" }],
      distribution: {
        marketplace_url: "https://plugins.tokenhub.example/kimi",
        repository_url: "https://github.com/tokenhub/kimi-provider",
        download_url: "https://plugins.tokenhub.example/kimi/1.2.3.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        signature_url: "https://plugins.tokenhub.example/kimi/1.2.3.sig",
        homepage_url: "https://tokenhub.example/plugins/kimi",
        license: "Apache-2.0",
      },
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("分发")).toBeInTheDocument();
    expect(screen.getByText("已禁用")).toBeInTheDocument();
    expect(screen.getByText("许可证 Apache-2.0")).toBeInTheDocument();
    expect(screen.getByText("SHA-256 0123456789ab...cdef")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /市场/ })).toHaveAttribute("href", "https://plugins.tokenhub.example/kimi");
    expect(screen.getByRole("link", { name: /仓库/ })).toHaveAttribute("href", "https://github.com/tokenhub/kimi-provider");
    expect(screen.getByRole("link", { name: /下载/ })).toHaveAttribute("href", "https://plugins.tokenhub.example/kimi/1.2.3.zip");
  });

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
