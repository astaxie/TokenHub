import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type PluginCapabilityDescriptor } from "../core/types";
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

  it("renders gateway hook subjects", () => {
    const data = emptyData();
    data.pluginChain.hooks = [{
      plugin_id: "tokenhub.provider.kimi",
      hook_id: "kimi-provider-call",
      stage: "provider_call",
      priority: 1000,
      subject: "kimi_subscription",
      failure_policy: "skip_route",
      timeout_millis: 5000,
      mandatory: false,
      reads: ["provider_request"],
      writes: ["provider_response", "usage"],
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("适用对象")).toBeInTheDocument();
    expect(screen.getByText("kimi_subscription")).toBeInTheDocument();
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

  it("uninstalls a local plugin package through the admin endpoint", async () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.local-privacy",
      name: "Local Privacy",
      version: "1.0.0",
      source: "local_file",
      status: "enabled",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin_id: "tokenhub.local-privacy", restart_required: true },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "卸载" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugin-packages/tokenhub.local-privacy");
    expect(init.method).toBe("DELETE");
    await waitFor(() => expect(screen.getByText("tokenhub.local-privacy · 插件卸载完成，重启后生效")).toBeInTheDocument());
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

  it("runs plugin background jobs through the admin endpoint", async () => {
    const data = emptyData();
    data.pluginBackgroundJobs = [{
      plugin_id: "tokenhub.jobs",
      job_id: "quota.refresh",
      title: "Refresh quota",
      capability: "quota.refresh",
      subject: "openai_codex",
      schedule: "*/10 * * * *",
      max_concurrency: 1,
      input_schema: {
        type: "object",
        required: ["resource_id"],
        properties: {
          resource_id: { type: "string" },
        },
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: {
        plugin_id: "tokenhub.jobs",
        job_id: "quota.refresh",
        trigger: "manual",
        status: "succeeded",
        attempts: 1,
        started_at: "2026-08-26T10:00:00Z",
        completed_at: "2026-08-26T10:00:01Z",
        result: { data: { resource_id: "rsrc_1", access_token: "[redacted]" } },
      },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.change(screen.getByLabelText(/resource_id/), { target: { value: "rsrc_1" } });
    fireEvent.click(screen.getByRole("button", { name: "运行任务" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.jobs/background-jobs/quota.refresh/run");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ resource_id: "rsrc_1" });
    await waitFor(() => expect(screen.getByText(/\"status\": \"succeeded\"/)).toBeInTheDocument());
    expect(screen.getByText(/\"access_token\": \"\[redacted\]\"/)).toBeInTheDocument();
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

  it("applies SIM selection changes through the shell preference callback", () => {
    const onPreferenceChange = vi.fn();
    const data = emptyData();
    data.plugins = [
      simPlugin("tokenhub.sim.default", [
        themeCapability("default-light", { mode: "light", default: true, order: 1, priority: 10 }),
        layoutCapability("default-shell", { default: true, order: 1, priority: 10 }),
      ]),
      simPlugin("tokenhub.sim.enterprise", [
        themeCapability("enterprise-light", { mode: "light", order: 20 }),
        layoutCapability("enterprise-shell", { order: 20 }),
      ]),
    ];

    render(
      <PluginsView
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={data}
        onSIMSelectionPreferenceChange={onPreferenceChange}
        simSelectionPreference={{}}
        theme="light"
      />,
    );

    fireEvent.change(screen.getByLabelText("SIM 插件"), { target: { value: "tokenhub.sim.enterprise" } });
    fireEvent.change(screen.getByLabelText("主题 Token"), { target: { value: "tokenhub.sim.enterprise:theme_tokens:enterprise-light" } });
    fireEvent.change(screen.getByLabelText("布局预设"), { target: { value: "tokenhub.sim.enterprise:shell_layout:enterprise-shell" } });
    fireEvent.click(screen.getByRole("button", { name: "应用选择" }));

    expect(onPreferenceChange).toHaveBeenCalledWith({
      simPluginID: "tokenhub.sim.enterprise",
      themeKey: "tokenhub.sim.enterprise:theme_tokens:enterprise-light",
      themeID: "enterprise-light",
      layoutKey: "tokenhub.sim.enterprise:shell_layout:enterprise-shell",
      layoutID: "enterprise-shell",
    });
  });
});

function simPlugin(id: string, capabilities: PluginCapabilityDescriptor[]) {
  return {
    id,
    name: id,
    version: "1.0.0",
    source: "built_in",
    kinds: ["sim"],
    placements: ["presentation"],
    capabilities,
  };
}

function themeCapability(id: string, payload: Record<string, unknown> = {}) {
  return {
    kind: "sim",
    name: "theme_tokens",
    subject: id,
    value: JSON.stringify({
      id,
      mode: "all",
      tokens: { accent: "#2563eb" },
      ...payload,
    }),
  };
}

function layoutCapability(id: string, payload: Record<string, unknown> = {}) {
  return {
    kind: "sim",
    name: "shell_layout",
    subject: id,
    value: JSON.stringify({
      id,
      layout: { density: "comfortable" },
      ...payload,
    }),
  };
}
