import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

describe("PluginsView", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

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

  it("updates local plugin state through the admin endpoint", async () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.local-privacy",
      name: "Local Privacy",
      version: "1.0.0",
      source: "local_file",
      status: "disabled",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin_id: "tokenhub.local-privacy", status: "enabled", restart_required: true },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: /启用/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.local-privacy/state");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(String(init.body))).toEqual({ status: "enabled" });
    await waitFor(() => expect(screen.getByText("重启后生效")).toBeInTheDocument());
    expect(screen.getByText("已启用")).toBeInTheDocument();
  });

  it("installs a verified plugin package through the admin endpoint", async () => {
    const data = emptyData();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { id: "tokenhub.marketplace.kimi" }, restart_required: true },
    }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.change(screen.getByLabelText("下载 URL"), { target: { value: "https://plugins.example/kimi.zip" } });
    fireEvent.change(screen.getByLabelText("SHA-256 校验"), {
      target: { value: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" },
    });
    fireEvent.click(screen.getByLabelText("允许替换"));
    fireEvent.click(screen.getByLabelText("安装后启用"));
    const installButtons = screen.getAllByRole("button", { name: "安装插件" });
    fireEvent.click(installButtons[installButtons.length - 1]);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/install");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/kimi.zip",
      checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      replace: true,
      enable: true,
    });
    await waitFor(() => expect(screen.getByText("tokenhub.marketplace.kimi · 插件安装完成，重启后生效")).toBeInTheDocument());
  });

  it("updates an installed plugin package through the admin endpoint", async () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.marketplace.kimi",
      name: "Marketplace Kimi",
      version: "1.0.0",
      source: "marketplace",
      status: "enabled",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
      distribution: {
        download_url: "https://plugins.example/kimi.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { version: "1.1.0" }, restart_required: true },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "更新" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.marketplace.kimi/update");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/kimi.zip",
      checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    });
    await waitFor(() => expect(screen.getByText("1.1.0 · 插件更新完成，重启后生效")).toBeInTheDocument());
  });

  it("installs a marketplace plugin from its distribution metadata", async () => {
    const data = emptyData();
    data.pluginMarketplaceAvailable = true;
    data.pluginMarketplaceSourceURL = "https://plugins.example/index.json";
    data.pluginMarketplace = [{
      plugin: {
        id: "tokenhub.marketplace.kimi",
        name: "Marketplace Kimi",
        version: "1.0.0",
        source: "marketplace",
        status: "enabled",
        kinds: ["provider"],
        placements: ["gateway_chain"],
        capabilities: [],
        distribution: {
          download_url: "https://plugins.example/kimi.zip",
          checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        },
      },
      installed: false,
      update_available: false,
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { id: "tokenhub.marketplace.kimi" }, restart_required: true },
    }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    const installButtons = screen.getAllByRole("button", { name: "安装插件" });
    fireEvent.click(installButtons[installButtons.length - 1]);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/install");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/kimi.zip",
      checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      replace: false,
      enable: false,
    });
    await waitFor(() => expect(screen.getByText("tokenhub.marketplace.kimi · 插件安装完成，重启后生效")).toBeInTheDocument());
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
