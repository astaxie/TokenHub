import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

    expect(screen.getAllByText("已禁用").some((element) => element.matches(".pill"))).toBe(true);
    expect(screen.getByText("许可证 Apache-2.0")).toBeInTheDocument();
    expect(screen.getByText("SHA-256 0123456789ab...cdef")).toBeInTheDocument();
    const titleCell = screen.getByText("Kimi Provider").closest(".plugin-title-cell");
    expect(titleCell).not.toBeNull();
    expect(titleCell?.textContent).toContain("tokenhub.provider.kimi");
    expect(titleCell?.textContent).toContain("1.2.3");
    expect(screen.getByRole("link", { name: "市场" })).toHaveAttribute("href", "https://plugins.tokenhub.example/kimi");
    expect(screen.getByRole("link", { name: /仓库/ })).toHaveAttribute("href", "https://github.com/tokenhub/kimi-provider");
    expect(screen.getByRole("link", { name: /下载/ })).toHaveAttribute("href", "https://plugins.tokenhub.example/kimi/1.2.3.zip");
  });

  it("opens the selected plugin detail page from the registry", () => {
    const onSelectPlugin = vi.fn();
    const data = emptyData();
    data.plugins = [{
      id: "example.detail",
      name: "Detail Example",
      version: "1.0.0",
      source: "local_file",
      status: "enabled",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} onSelectPlugin={onSelectPlugin} />);
    fireEvent.click(screen.getByRole("button", { name: "查看插件详情 Detail Example" }));

    expect(onSelectPlugin).toHaveBeenCalledWith("example.detail");
    expect(screen.queryByRole("button", { name: "设置" })).not.toBeInTheDocument();
  });

  it("opens plugin settings directly from the installed list", () => {
    const onSelectPlugin = vi.fn();
    const data = emptyData();
    data.plugins = [{
      id: "example.settings",
      name: "Settings Example",
      version: "1.0.0",
      source: "local_file",
      status: "enabled",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [{ kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "light", tokens: { accent: "#2563eb" } }) }],
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} onSelectPlugin={onSelectPlugin} />);
    fireEvent.click(screen.getByRole("button", { name: "设置" }));

    expect(onSelectPlugin).toHaveBeenCalledWith("example.settings", "settings");
  });

  it("filters installed plugins by status and search query", () => {
    const data = emptyData();
    data.plugins = [
      {
        id: "example.enabled",
        name: "Enabled Example",
        version: "1.0.0",
        source: "built_in",
        status: "enabled",
        kinds: ["provider"],
        placements: ["gateway_chain"],
        capabilities: [],
      },
      {
        id: "example.disabled",
        name: "Disabled Example",
        version: "1.0.0",
        source: "local_file",
        status: "disabled",
        kinds: ["extension"],
        placements: ["background"],
        capabilities: [],
      },
    ];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "已禁用" }));
    expect(screen.getByText("Disabled Example")).toBeInTheDocument();
    expect(screen.queryByText("Enabled Example")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "全部插件" }));
    fireEvent.change(screen.getByRole("searchbox", { name: "搜索插件" }), { target: { value: "enabled" } });
    expect(screen.getByText("Enabled Example")).toBeInTheDocument();
    expect(screen.queryByText("Disabled Example")).not.toBeInTheDocument();
  });

  it("summarizes repeated capability declarations without duplicate key warnings", () => {
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

    fireEvent.click(screen.getByRole("tab", { name: "Provider 插件" }));
    const capabilityCount = screen.getByText("能力").closest(".plugin-type-capability-count");
    expect(capabilityCount).not.toBeNull();
    expect(within(capabilityCount as HTMLElement).getByText("2")).toBeInTheDocument();
    expect(consoleError.mock.calls.some((call) => String(call[0]).includes("Encountered two children with the same key"))).toBe(false);
  });

  it("shows plugin categories beside the installed list without a separate extension tab", () => {
    const data = emptyData();
    data.plugins = [
      {
        id: "tokenhub.provider.kimi",
        name: "Kimi Provider",
        version: "1.0.0",
        source: "built_in",
        status: "enabled",
        kinds: ["provider"],
        placements: ["gateway_chain"],
        capabilities: [
          { kind: "provider", name: "chat" },
          { kind: "provider", name: "chat_stream" },
        ],
      },
      {
        id: "tokenhub.chain.privacy",
        name: "Privacy Chain",
        version: "1.0.0",
        source: "built_in",
        status: "enabled",
        kinds: ["extension"],
        placements: ["gateway_chain"],
        capabilities: [],
      },
      simPlugin("tokenhub.sim.enterprise", []),
      {
        id: "tokenhub.jobs",
        name: "Quota Jobs",
        version: "1.0.0",
        source: "built_in",
        status: "enabled",
        kinds: ["extension"],
        placements: ["background"],
        capabilities: [],
      },
    ];
    data.pluginChain.hooks = [
      {
        plugin_id: "tokenhub.chain.privacy",
        hook_id: "privacy.pre",
        stage: "privacy_pre",
        priority: 100,
        failure_policy: "fail_closed",
        timeout_millis: 1000,
        mandatory: true,
      },
      {
        plugin_id: "tokenhub.chain.privacy",
        hook_id: "privacy.post",
        stage: "guardrail_post",
        priority: 200,
        failure_policy: "fail_closed",
        timeout_millis: 1000,
        mandatory: true,
      },
    ];
    data.pluginBackgroundJobs = [
      {
        plugin_id: "tokenhub.jobs",
        job_id: "quota.refresh",
        schedule: "10m",
        max_concurrency: 1,
      },
      {
        plugin_id: "tokenhub.jobs",
        job_id: "credentials.refresh",
        schedule: "1m",
        max_concurrency: 1,
      },
    ];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.queryByRole("tab", { name: "扩展类型" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "全部插件" })).toHaveTextContent("4");
    expect(screen.getByRole("tab", { name: "Provider 插件" })).toHaveTextContent("1");
    expect(screen.getByRole("tab", { name: "链路注入" })).toHaveTextContent("1");
    expect(screen.getByRole("tab", { name: "界面模板" })).toHaveTextContent("1");
    expect(screen.getByRole("tab", { name: "后台任务" })).toHaveTextContent("1");
    expect(screen.queryByText("Provider 能力")).not.toBeInTheDocument();
  });

  it("lists provider plugins in a dedicated provider tab", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.qwen",
      name: "Qwen",
      version: "built-in",
      source: "built_in",
      status: "enabled",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [],
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("tab", { name: "Provider 插件" }));

    expect(screen.getByText("Provider 插件清单")).toBeInTheDocument();
    expect(screen.getByText("Qwen")).toBeInTheDocument();
  });

  it("allows built-in provider plugins to be disabled from the installed list", async () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.qwen",
      name: "Qwen",
      version: "built-in",
      source: "built_in",
      status: "enabled",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [],
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin_id: "tokenhub.provider.qwen", status: "disabled", restart_required: false },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.qwen/state");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(String(init.body))).toEqual({ status: "disabled" });
    await waitFor(() => expect(screen.getAllByText("已禁用").some((element) => element.matches(".pill"))).toBe(true));
  });

  it("lists gateway injection plugins instead of raw hook rows", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.chain.privacy",
      name: "Privacy Chain",
      version: "1.0.0",
      source: "marketplace",
      status: "enabled",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
    }];
    data.pluginChain.hooks = [
      {
        plugin_id: "tokenhub.chain.privacy",
        hook_id: "privacy-pre",
        stage: "privacy_pre",
        priority: 1000,
        subject: "chat",
        failure_policy: "fail_closed",
        timeout_millis: 5000,
        mandatory: true,
        reads: ["request_body"],
        writes: ["request_body", "audit"],
      },
      {
        plugin_id: "tokenhub.chain.privacy",
        hook_id: "privacy-cache",
        stage: "cache_lookup",
        priority: 1200,
        subject: "responses",
        failure_policy: "fail_open",
        timeout_millis: 5000,
        mandatory: false,
        reads: ["request_body"],
        writes: ["cache_value"],
      },
    ];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("tab", { name: "链路注入" }));

    expect(screen.getByText("链路注入插件清单")).toBeInTheDocument();
    expect(screen.getByText("Privacy Chain")).toBeInTheDocument();
    expect(screen.getByText("注入点")).toBeInTheDocument();
    const injectionPoints = screen.getByText("强制 1 · 可选 1").closest(".stacked-cell");
    expect(injectionPoints).not.toBeNull();
    expect(within(injectionPoints as HTMLElement).getByText("2")).toBeInTheDocument();
    expect(screen.queryByText("privacy-pre")).not.toBeInTheDocument();
    expect(screen.queryByText("privacy-cache")).not.toBeInTheDocument();
    expect(screen.getByText("适用对象")).toBeInTheDocument();
    expect(screen.getByText("chat")).toBeInTheDocument();
    expect(screen.getByText("responses")).toBeInTheDocument();
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
    fireEvent.click(screen.getByRole("button", { name: "启用" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.local-privacy/state");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(String(init.body))).toEqual({ status: "enabled" });
    await waitFor(() => expect(screen.getByText("需重启 TokenHub 服务")).toBeInTheDocument());
    expect(screen.getAllByText("已启用").some((element) => element.matches(".pill"))).toBe(true);
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

  it("lists background job plugins instead of raw job rows", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.openai-codex",
      name: "OpenAI Codex Subscription",
      version: "built-in",
      source: "built_in",
      status: "enabled",
      kinds: ["provider"],
      placements: ["gateway_chain", "background"],
      capabilities: [],
    }];
    data.pluginBackgroundJobs = [
      {
        plugin_id: "tokenhub.provider.openai-codex",
        job_id: "openai_codex.credentials.refresh_due",
        title: "Refresh credentials",
        capability: "credentials.refresh_due",
        subject: "openai_codex",
        schedule: "1m",
        max_concurrency: 1,
      },
      {
        plugin_id: "tokenhub.provider.openai-codex",
        job_id: "openai_codex.quota.refresh_due",
        title: "Refresh quota",
        capability: "quota.refresh_due",
        subject: "openai_codex",
        schedule: "10m",
        max_concurrency: 1,
        retry: { max_attempts: 2, backoff_millis: 1000 },
      },
    ];
    data.pluginBackgroundRuns = [{
      plugin_id: "tokenhub.provider.openai-codex",
      job_id: "openai_codex.quota.refresh_due",
      trigger: "schedule",
      status: "succeeded",
      attempts: 1,
      started_at: "2026-08-26T10:00:00Z",
      completed_at: "2026-08-26T10:00:01Z",
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("tab", { name: "后台任务" }));

    expect(screen.getByText("后台任务插件清单")).toBeInTheDocument();
    expect(screen.getAllByText("OpenAI Codex Subscription")).toHaveLength(1);
    expect(screen.getByText("任务数量")).toBeInTheDocument();
    expect(screen.getAllByText("2").length).toBeGreaterThan(0);
    expect(screen.getByText("1m")).toBeInTheDocument();
    expect(screen.getByText("10m")).toBeInTheDocument();
    expect(screen.getByText("credentials.refresh_due")).toBeInTheDocument();
    expect(screen.getByText("quota.refresh_due")).toBeInTheDocument();
    expect(screen.getByText("成功 / 1")).toBeInTheDocument();
    expect(screen.getByText("2 / 1000ms")).toBeInTheDocument();
    expect(screen.queryByText("openai_codex.credentials.refresh_due")).not.toBeInTheDocument();
    expect(screen.queryByText("openai_codex.quota.refresh_due")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "运行任务" })).not.toBeInTheDocument();
  });

  it("renders SIM theme and layout contributions", () => {
    const data = emptyData();
    data.pluginUI = [
      {
        plugin_id: "tokenhub.provider.codex",
        id: "route-context",
        slot: "route.detail.panel",
        title: "Route Context",
      },
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
    expect(screen.queryByText("界面贡献清单")).not.toBeInTheDocument();
    expect(screen.getByText("UI 插槽注册")).toBeInTheDocument();
    expect(screen.getByText("开发者信息").closest("details")).not.toHaveAttribute("open");
    expect(screen.getAllByText("Enterprise Theme").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Ops Layout").length).toBeGreaterThan(0);
    expect(screen.getByText("深色")).toBeInTheDocument();
    expect(screen.getByText("紧凑")).toBeInTheDocument();
    fireEvent.click(screen.getByText("开发者信息"));
    expect(screen.getByText("route.detail.panel")).toBeInTheDocument();
  });

  it("sets a whole UI template package through the shell preference callback", () => {
    const onPreferenceChange = vi.fn();
    const onSelectPlugin = vi.fn();
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
        onSelectPlugin={onSelectPlugin}
        onSIMSelectionPreferenceChange={onPreferenceChange}
        simSelectionPreference={{}}
        theme="light"
      />,
    );

    fireEvent.click(screen.getByRole("tab", { name: "界面模板" }));
    expect(screen.getByLabelText("界面模板列表")).toBeInTheDocument();
    expect(screen.queryByLabelText("界面模板插件")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("主题 Token")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("布局预设")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /配置界面模板.*tokenhub\.sim\.enterprise/ }));
    expect(onSelectPlugin).toHaveBeenCalledWith("tokenhub.sim.enterprise", "settings");
    fireEvent.click(screen.getByRole("button", { name: "设为默认模板" }));

    expect(onPreferenceChange).toHaveBeenCalledWith({
      simPluginID: "tokenhub.sim.enterprise",
      themeKey: "tokenhub.sim.enterprise:theme_tokens:enterprise-light",
      themeID: "enterprise-light",
      layoutKey: "tokenhub.sim.enterprise:shell_layout:enterprise-shell",
      layoutID: "enterprise-shell",
    });
  });

  it("localizes plugin metadata contributed by descriptors, UI contributions, background job summaries, and SIM payloads", () => {
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
    fireEvent.click(screen.getByText("Developer Info"));
    expect(screen.getByText("Route Context")).toBeInTheDocument();
    expect(screen.getAllByText("Enterprise SIM").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Enterprise Theme").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Operations Layout").length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("tab", { name: "Background Jobs" }));
    expect(screen.getByText("Background Job Plugin List")).toBeInTheDocument();
    expect(screen.getByText("Codex Subscription")).toBeInTheDocument();
    expect(screen.getByText("codex.refresh")).toBeInTheDocument();

    setActiveLanguage("ja");
    rerender(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} theme="light" />);

    expect(screen.getByText("バックグラウンドジョブプラグイン一覧")).toBeInTheDocument();
    expect(screen.getByText("Codex サブスクリプション")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "UI テンプレート" }));
    fireEvent.click(screen.getByText("開発者情報"));
    expect(screen.getByText("ルートコンテキスト")).toBeInTheDocument();
    expect(screen.getAllByText("エンタープライズ SIM").length).toBeGreaterThan(0);
    expect(screen.getAllByText("エンタープライズテーマ").length).toBeGreaterThan(0);
    expect(screen.getAllByText("運用レイアウト").length).toBeGreaterThan(0);
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
    expect(pluginStyles()).toContain(".plugin-developer-details summary");
    expect(pluginStyles()).toContain(".plugin-title-cell");
    expect(pluginStyles()).toContain(".plugin-title-id");
    expect(pluginStyles()).toContain(".plugin-title-version");
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
    expect(pluginStyles()).toContain(".plugin-installed-row");
    expect(pluginStyles()).toContain(".plugin-status-filters");
    expect(pluginStyles()).toContain(".plugin-extension-nav");
    expect(pluginStyles()).toContain(".plugin-manager-topbar");
    expect(pluginStyles()).toContain(".plugin-manager-actions");
    expect(pluginStyles()).toContain(".plugin-install-runner");
    expect(pluginStyles()).toContain(".plugin-install-panel");
    expect(pluginStyles()).toContain(".plugin-install-file-button");
    expect(pluginStyles()).toContain(".plugin-install-toggle");
    expect(pluginStyles()).toContain(".sim-template-selection-panel");
    expect(pluginStyles()).toContain(".sim-template-item");
    expect(pluginStyles()).toContain(".sim-template-detail-panel");
    expect(pluginStyles()).toContain(".plugin-developer-details");
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
