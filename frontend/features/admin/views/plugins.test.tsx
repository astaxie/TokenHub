import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type PluginCapabilityDescriptor } from "../core/types";
import { emptyData } from "../domain/catalog";
import { pluginMarketplaceWebsiteURL } from "../domain/plugin-management";
import { setActiveLanguage } from "../i18n/runtime";
import { PluginsView } from "./plugins";

describe("PluginsView", () => {
  afterEach(() => {
    vi.restoreAllMocks();
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
    expect(screen.getByRole("link", { name: "市场" })).toHaveAttribute("href", "https://plugins.tokenhub.example/kimi");
    expect(screen.getByRole("link", { name: /仓库/ })).toHaveAttribute("href", "https://github.com/tokenhub/kimi-provider");
    expect(screen.getByRole("link", { name: /下载/ })).toHaveAttribute("href", "https://plugins.tokenhub.example/kimi/1.2.3.zip");
  });

  it("renders repeated capability tags without duplicate key warnings", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.catalog",
      name: "Provider Catalog",
      version: "built-in",
      source: "built_in",
      status: "enabled",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [
        { kind: "provider_catalog", name: "model_category", value: "{\"key\":\"chat\"}" },
        { kind: "provider_catalog", name: "model_category", value: "{\"key\":\"embedding\"}" },
      ],
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getAllByText("model_category")).toHaveLength(2);
    expect(consoleError.mock.calls.some((call) => String(call[0]).includes("Encountered two children with the same key"))).toBe(false);
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
    fireEvent.click(screen.getByRole("tab", { name: "链路注入" }));

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
    fireEvent.click(screen.getByRole("button", { name: "安装本地插件" }));
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

  it("uploads a local zip plugin package through the admin endpoint", async () => {
    const data = emptyData();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { id: "tokenhub.local.upload" }, restart_required: true },
    }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "安装本地插件" }));
    fireEvent.click(screen.getByRole("tab", { name: "上传 ZIP" }));
    const file = new File(["plugin archive"], "tokenhub-local.zip", { type: "application/zip" });
    fireEvent.change(screen.getByLabelText("插件 ZIP 包"), { target: { files: [file] } });
    fireEvent.change(screen.getByLabelText("SHA-256 校验"), {
      target: { value: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" },
    });
    fireEvent.click(screen.getByLabelText("安装后启用"));
    const installButtons = screen.getAllByRole("button", { name: "安装插件" });
    fireEvent.submit(installButtons[installButtons.length - 1].closest("form") as HTMLFormElement);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/install");
    expect(init.method).toBe("POST");
    expect(init.body).toBeInstanceOf(FormData);
    const form = init.body as FormData;
    expect(form.get("package")).toBe(file);
    expect(form.get("checksum_sha256")).toBe("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef");
    expect(form.get("replace")).toBe("false");
    expect(form.get("enable")).toBe("true");
    expect(new Headers(init.headers).has("content-type")).toBe(false);
    await waitFor(() => expect(screen.getByText("tokenhub.local.upload · 插件安装完成，重启后生效")).toBeInTheDocument());
  });

  it("switches plugin install sources without controlled input warnings", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={emptyData()} />);
    fireEvent.click(screen.getByRole("button", { name: "安装本地插件" }));
    fireEvent.change(screen.getByLabelText("下载 URL"), { target: { value: "https://plugins.example/kimi.zip" } });
    fireEvent.click(screen.getByRole("tab", { name: "上传 ZIP" }));
    fireEvent.click(screen.getByRole("tab", { name: "URL 安装" }));

    const messages = consoleError.mock.calls.map((call) => String(call[0]));
    expect(messages.some((message) => message.includes("A component is changing a controlled input to be uncontrolled"))).toBe(false);
    expect(messages.some((message) => message.includes("A component is changing an uncontrolled input to be controlled"))).toBe(false);
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
    fireEvent.click(screen.getByRole("tab", { name: "动作任务" }));

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
    fireEvent.click(screen.getByRole("tab", { name: "动作任务" }));
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
    fireEvent.click(screen.getByRole("tab", { name: "界面模板" }));

    expect(screen.getByText("界面模板与主题贡献")).toBeInTheDocument();
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

    fireEvent.click(screen.getByRole("tab", { name: "界面模板" }));
    fireEvent.change(screen.getByLabelText("界面模板插件"), { target: { value: "tokenhub.sim.enterprise" } });
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

  it("localizes plugin metadata contributed by descriptors, UI contributions, actions, jobs, and SIM payloads", () => {
    const data = emptyData();
    data.plugins = [
      {
        id: "tokenhub.provider.codex",
        name: "Codex 订阅",
        version: "1.0.0",
        localizations: {
          "en-US": { name: "Codex Subscription" },
          "ja-JP": { name: "Codex サブスクリプション" },
        },
        source: "marketplace",
        status: "enabled",
        kinds: ["provider"],
        placements: ["gateway_chain"],
        capabilities: [],
      },
      {
        id: "tokenhub.sim.enterprise",
        name: "企业 SIM",
        version: "1.0.0",
        localizations: {
          "en-US": { name: "Enterprise SIM" },
          "ja-JP": { name: "エンタープライズ SIM" },
        },
        source: "built_in",
        status: "enabled",
        kinds: ["sim"],
        placements: ["presentation"],
        capabilities: [
          themeCapability("enterprise-light", {
            title: "企业主题",
            mode: "light",
            localizations: {
              "en-US": { title: "Enterprise Theme" },
              "ja-JP": { title: "エンタープライズテーマ" },
            },
          }),
          layoutCapability("enterprise-shell", {
            title: "运维布局",
            localizations: {
              "en-US": { title: "Operations Layout" },
              "ja-JP": { title: "運用レイアウト" },
            },
          }),
        ],
      },
    ];
    data.pluginUI = [{
      plugin_id: "tokenhub.provider.codex",
      id: "route-context",
      slot: "route.detail.panel",
      title: "路由上下文",
      schema: {
        localizations: {
          "en-US": { title: "Route Context" },
          "ja-JP": { title: "ルートコンテキスト" },
        },
      },
    }];
    data.pluginActions = [{
      plugin_id: "tokenhub.provider.codex",
      action_id: "codex.sync",
      kind: "mutate",
      title: "同步 Codex",
      metadata: {
        "title:en-US": "Sync Codex",
        "title:ja-JP": "Codex を同期",
      },
    }];
    data.pluginBackgroundJobs = [{
      plugin_id: "tokenhub.provider.codex",
      job_id: "codex.refresh",
      title: "刷新 Codex",
      localizations: {
        "en-US": { title: "Refresh Codex" },
        "ja-JP": { title: "Codex を更新" },
      },
      capability: "codex.refresh",
      schedule: "10m",
      max_concurrency: 1,
    }];

    setActiveLanguage("en");
    const { rerender } = render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} theme="light" />);

    expect(screen.getByText("Codex Subscription")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "UI Templates" }));
    expect(screen.getByText("Route Context")).toBeInTheDocument();
    expect(screen.getByText("Enterprise Theme · Enterprise SIM")).toBeInTheDocument();
    expect(screen.getByText("Operations Layout · Enterprise SIM")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Actions and Jobs" }));
    expect(screen.getByText("Sync Codex")).toBeInTheDocument();
    expect(screen.getByText("Refresh Codex")).toBeInTheDocument();

    setActiveLanguage("ja");
    rerender(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} theme="light" />);

    expect(screen.getByText("Codex を同期")).toBeInTheDocument();
    expect(screen.getByText("Codex を更新")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "UI テンプレート" }));
    expect(screen.getByText("ルートコンテキスト")).toBeInTheDocument();
    expect(screen.getByText("エンタープライズテーマ · エンタープライズ SIM")).toBeInTheDocument();
    expect(screen.getByText("運用レイアウト · エンタープライズ SIM")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "インストール済みプラグイン" }));
    expect(screen.getByText("Codex サブスクリプション")).toBeInTheDocument();
  });

  it("keeps interface template selection controls on the shared plugin CSS surface", () => {
    render(
      <PluginsView
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={emptyData()}
        onSIMSelectionPreferenceChange={vi.fn()}
        theme="light"
      />,
    );

    fireEvent.click(screen.getByRole("tab", { name: "界面模板" }));
    expect(screen.getByText("界面模板选择面板")).toBeInTheDocument();
    expect(pluginStyles()).toContain(".plugin-action-field select");
    expect(pluginStyles()).toContain(".plugin-action-runner .stacked-cell");
  });

  it("renders stable Plugin Manager hooks for feature-scoped CSS", () => {
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
      distribution: {
        download_url: "https://plugins.example/privacy.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    }];

    const { container } = render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByRole("link", { name: "插件市场" })).toHaveAttribute("href", "https://plugins.betokenhub.com");
    expect(screen.getByRole("button", { name: "安装本地插件" })).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="install"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="registry"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="marketplace"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="chain-hooks"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="ui-contributions"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="template-contributions"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="template-selection"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="actions"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-section="background-jobs"]')).not.toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-control="lifecycle"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-control="distribution"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-manager-control="delete"]')).toBeInTheDocument();
    expect(pluginStyles()).toContain(".plugins-view .metric-grid");
    expect(pluginStyles()).toContain(".plugins-view .metric-card");
    expect(pluginStyles()).toContain(".plugin-manager-topbar");
    expect(pluginStyles()).toContain(".plugin-manager-actions");
    expect(pluginStyles()).toContain(".plugin-install-modal");
    expect(pluginStyles()).toContain(".plugin-install-runner");
    expect(pluginStyles()).toContain(".plugin-install-panel");
    expect(pluginStyles()).toContain(".plugin-install-file-button");
    expect(pluginStyles()).toContain(".plugin-install-toggle");
    expect(pluginStyles()).toContain('.plugins-view [data-plugin-manager-control="lifecycle"] .compact-button');
  });

  it("uses the configured plugin marketplace website URL", () => {
    const data = emptyData();
    data.resources.settings = [{
      id: "cfg_gateway",
      kind: "settings",
      name: "Gateway",
      status: "active",
      fields: { plugin_marketplace_url: "https://plugins.example/custom" },
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByRole("link", { name: "插件市场" })).toHaveAttribute("href", "https://plugins.example/custom");
    expect(pluginMarketplaceWebsiteURL(emptyData())).toBe("https://plugins.betokenhub.com");
    data.resources.settings[0].fields = { plugin_marketplace_url: "javascript:alert(1)" };
    expect(pluginMarketplaceWebsiteURL(data)).toBe("https://plugins.betokenhub.com");
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

function pluginStyles() {
  return readFileSync(resolve("app/styles/redesign/plugins.css"), "utf8");
}
