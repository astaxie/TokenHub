import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

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

  it("keeps built-in plugin lifecycle actions read-only", () => {
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

    expect(screen.getByText("已启用")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "回滚" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "禁用" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "更新" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "卸载" })).not.toBeInTheDocument();
  });
});
