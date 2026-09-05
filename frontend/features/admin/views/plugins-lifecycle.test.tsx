import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type PluginDescriptor } from "../core/types";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

const api = { baseURL: "http://localhost:8080", adminToken: "admin-token" };

describe("PluginsView lifecycle controls", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders rollback-aware lifecycle state and exposes the rollback action", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.local-rollback",
      name: "Rollback Plugin",
      version: "1.0.0",
      source: "local_file",
      status: "rollback_available",
      rollback_available: true,
      rollback_version: "1.0.0",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
      distribution: {
        download_url: "https://plugins.example/rollback.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("可回滚")).toBeInTheDocument();
    expect(screen.getByText("回滚版本 1.0.0")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "回滚" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "更新" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "卸载" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "启用" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "禁用" })).not.toBeInTheDocument();
  });

  it("renders failed startup lifecycle state with built-in fallback rollback target", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.local-startup-failed",
      name: "Startup Failed Plugin",
      version: "2.0.0",
      source: "local_file",
      status: "failed_startup",
      health: "unhealthy",
      rollback_target: "built_in",
      last_error_code: "plugin_startup_failed",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
      distribution: {
        download_url: "https://plugins.example/startup-failed.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("启动失败")).toBeInTheDocument();
    expect(screen.getByText("回滚目标 内置")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "回滚" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "启用" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "禁用" })).not.toBeInTheDocument();
  });

  it("sends the rollback request through the admin endpoint", async () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.local-rollback",
      name: "Rollback Plugin",
      version: "2.0.0",
      source: "local_file",
      status: "rollback_available",
      rollback_available: true,
      rollback_version: "1.0.0",
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
      distribution: {
        download_url: "https://plugins.example/rollback.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { version: "1.0.0" }, rollback_version: "1.0.0", restart_required: true },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "回滚" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.local-rollback/rollback");
    expect(init.method).toBe("POST");
    expect(init.body).toBeUndefined();
    await waitFor(() => expect(screen.getByText("1.0.0 · 插件回滚完成，重启后生效")).toBeInTheDocument());
  });

  it("allows built-in plugins to be disabled from the installed list", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.openai",
      name: "OpenAI",
      version: "1.0.0",
      source: "built_in",
      status: "enabled",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [],
      distribution: {
        download_url: "https://plugins.example/openai.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      },
    }];

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getAllByText("已启用").some((element) => element.matches(".pill"))).toBe(true);
    expect(screen.queryByRole("button", { name: "回滚" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "禁用" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "更新" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "卸载" })).not.toBeInTheDocument();
  });
});

describe("PluginsView lifecycle state updates", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  // The admin API reports the status twice, and the nested copy is the one
  // `pluginManagerLifecycleState` reads first, so both are populated here.
  function enabledPlugin(status = "enabled"): PluginDescriptor {
    return {
      id: "tokenhub.local-privacy",
      name: "Local Privacy",
      version: "1.0.0",
      source: "local_file",
      status,
      lifecycle: {
        status,
        restart_required: false,
        health: "healthy",
        mandatory: false,
        rollback_available: false,
        loadable: status !== "disabled",
      },
      kinds: ["extension"],
      placements: ["gateway_chain"],
      capabilities: [],
    };
  }

  function dataWith(plugin: PluginDescriptor) {
    const data = emptyData();
    data.plugins = [plugin];
    return data;
  }

  function statusFilterCount(label: string) {
    return screen.getByRole("button", { name: label }).querySelector("strong")?.textContent;
  }

  function stubStateFetch(status: string) {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin_id: "tokenhub.local-privacy", status, restart_required: false },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  function deferStateFetch() {
    let settle: (outcome: { ok: true; status: string } | { ok: false; reason: Error }) => void = () => undefined;
    const pending = new Promise<Response>((resolve, reject) => {
      settle = (outcome) => {
        if (!outcome.ok) {
          reject(outcome.reason);
          return;
        }
        resolve(new Response(JSON.stringify({
          data: { plugin_id: "tokenhub.local-privacy", status: outcome.status, restart_required: false },
        }), { status: 200, headers: { "content-type": "application/json" } }));
      };
    });
    vi.stubGlobal("fetch", vi.fn().mockReturnValue(pending));
    return { pending, settle };
  }

  it("shows the requested status while the state request is still in flight", async () => {
    const { settle } = deferStateFetch();

    render(<PluginsView api={api} data={dataWith(enabledPlugin())} />);
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));

    const pending = await screen.findByRole("button", { name: "更新中" });
    expect(pending).toBeDisabled();
    expect(screen.getAllByText("已禁用").some((element) => element.matches(".pill"))).toBe(true);
    expect(statusFilterCount("已禁用")).toBe("1");
    expect(statusFilterCount("已启用")).toBe("0");

    await act(async () => {
      settle({ ok: true, status: "disabled" });
    });

    expect(screen.getByRole("button", { name: "启用" })).toBeInTheDocument();
    expect(statusFilterCount("已禁用")).toBe("1");
  });

  it("rolls the row back to the server status when the state request fails", async () => {
    const { pending, settle } = deferStateFetch();
    pending.catch(() => undefined);

    render(<PluginsView api={api} data={dataWith(enabledPlugin())} />);
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));
    expect(await screen.findByRole("button", { name: "更新中" })).toBeDisabled();

    await act(async () => {
      settle({ ok: false, reason: new Error("plugin state update rejected") });
    });

    expect(screen.getByRole("button", { name: "禁用" })).toBeEnabled();
    expect(screen.getAllByText("已启用").some((element) => element.matches(".pill"))).toBe(true);
    expect(statusFilterCount("已启用")).toBe("1");
    expect(statusFilterCount("已禁用")).toBe("0");
    expect(screen.getByText("plugin state update rejected")).toBeInTheDocument();
  });

  it("reflects an accepted disable in the row and in the status filter counts", async () => {
    const fetchMock = stubStateFetch("disabled");

    render(<PluginsView api={api} data={dataWith(enabledPlugin())} />);
    expect(statusFilterCount("已启用")).toBe("1");
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.local-privacy/state");
    expect(init.method).toBe("PATCH");
    expect(await screen.findByRole("button", { name: "启用" })).toBeInTheDocument();
    expect(screen.getAllByText("已禁用").some((element) => element.matches(".pill"))).toBe(true);
    expect(statusFilterCount("已禁用")).toBe("1");
    expect(statusFilterCount("已启用")).toBe("0");
  });

  it("keeps the lifecycle button busy until the reload settles", async () => {
    stubStateFetch("disabled");
    let releaseReload = () => undefined as void;
    const onReload = vi.fn(() => new Promise<void>((resolve) => {
      releaseReload = () => resolve();
    }));

    render(<PluginsView api={api} data={dataWith(enabledPlugin())} onReload={onReload} />);
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));

    await waitFor(() => expect(onReload).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: "更新中" })).toBeDisabled();

    releaseReload();
    expect(await screen.findByRole("button", { name: "启用" })).toBeEnabled();
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it("keeps the accepted status when the reloaded list is still stale", async () => {
    stubStateFetch("disabled");
    const onReload = vi.fn(() => Promise.resolve());

    const { rerender } = render(<PluginsView api={api} data={dataWith(enabledPlugin())} onReload={onReload} />);
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));
    expect(await screen.findByRole("button", { name: "启用" })).toBeInTheDocument();

    rerender(<PluginsView api={api} data={dataWith(enabledPlugin())} onReload={onReload} />);

    expect(screen.getByRole("button", { name: "启用" })).toBeInTheDocument();
    expect(statusFilterCount("已禁用")).toBe("1");
  });

  it("hands the row back to the server list once the reload reports the same status", async () => {
    stubStateFetch("disabled");
    const onReload = vi.fn(() => Promise.resolve());

    const { rerender } = render(<PluginsView api={api} data={dataWith(enabledPlugin())} onReload={onReload} />);
    fireEvent.click(screen.getByRole("button", { name: "禁用" }));
    expect(await screen.findByRole("button", { name: "启用" })).toBeInTheDocument();

    rerender(<PluginsView api={api} data={dataWith(enabledPlugin("disabled"))} onReload={onReload} />);
    expect(screen.getByRole("button", { name: "启用" })).toBeInTheDocument();

    rerender(<PluginsView api={api} data={dataWith(enabledPlugin("enabled"))} onReload={onReload} />);
    expect(screen.getByRole("button", { name: "禁用" })).toBeInTheDocument();
    expect(statusFilterCount("已启用")).toBe("1");
  });
});
