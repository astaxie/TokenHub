import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { type PluginDescriptor, type PluginMarketplacePlugin } from "../core/types";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
const installed: PluginDescriptor = {
  id: "review.theme", name: "Installed Theme", version: "1.0.0", source: "local_file",
  status: "disabled", installed: true, kinds: ["sim"], placements: ["presentation"], capabilities: [],
  distribution: { license: "MIT" },
};
const marketplace: PluginMarketplacePlugin = {
  installed: true, installed_version: "1.0.0", update_available: true,
  plugin: {
    ...installed, name: "Marketplace Theme", source: "marketplace", version: "2.0.0", status: "enabled",
    distribution: {
      download_url: "https://plugins.example/theme-2.zip", checksum_sha256: "a".repeat(64),
      signature_url: "https://plugins.example/theme-2.zip.sig", signature_key_id: "review-key",
    },
  },
};

function fixture(entry = marketplace) {
  return { ...emptyData(), plugins: [structuredClone(installed)], pluginMarketplace: [structuredClone(entry)] };
}

describe("Plugin marketplace update review regressions", () => {
  it("updates a redacted installed descriptor from its marketplace candidate and retains the result after reload", async () => {
    const data = fixture();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { version: "2.0.0" }, restart_required: true },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const onReload = vi.fn(async () => {
      view.rerender(<PluginsView api={api} data={{
        ...data,
        plugins: [{ ...installed, version: "2.0.0" }],
        pluginMarketplace: [{ ...marketplace, installed_version: "2.0.0", update_available: false }],
      }} onReload={onReload} />);
    });
    const view = render(<PluginsView api={api} data={data} onReload={onReload} />);
    const row = screen.getByText("Installed Theme").closest("article")!;
    expect(within(row).getByText("1.0.0")).toBeVisible();
    expect(within(row).getByText("已禁用")).toBeVisible();
    expect(screen.queryByText("Marketplace Theme")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "可更新" })).toHaveTextContent("1");
    fireEvent.click(screen.getByRole("button", { name: "可更新" }));
    fireEvent.click(within(row).getByRole("button", { name: "更新" }));
    await waitFor(() => expect(onReload).toHaveBeenCalledOnce());
    expect(fetchMock).toHaveBeenCalledWith(`${api.baseURL}/api/admin/plugins/review.theme/update`, expect.objectContaining({
      method: "POST",
      body: JSON.stringify({
        download_url: marketplace.plugin.distribution!.download_url,
        checksum_sha256: "a".repeat(64),
        signature_url: marketplace.plugin.distribution!.signature_url,
        signature_key_id: "review-key",
      }),
    }));
    expect(screen.getByRole("button", { name: "可更新" })).toHaveTextContent("0");
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("插件已更新至 2.0.0，重启后生效"));
    expect(screen.getByText("Installed Theme").closest("article")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "全部插件" }));
    expect(screen.getAllByRole("status")).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent("插件已更新至 2.0.0，重启后生效");
    expect(screen.getByText("2.0.0")).toBeVisible();
    expect(screen.queryByRole("button", { name: "更新" })).not.toBeInTheDocument();
  });

  it("shows a rejected update in the installed row and allows retry", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { message: "Synthetic update rejection", code: "plugin_signature_invalid" },
    }), { status: 400, headers: { "content-type": "application/json" } })));
    const onReload = vi.fn();
    render(<PluginsView api={api} data={fixture()} onReload={onReload} />);
    fireEvent.click(screen.getByRole("button", { name: "更新" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Synthetic update rejection"));
    expect(screen.getByRole("button", { name: "更新" })).toBeEnabled();
    expect(onReload).not.toHaveBeenCalled();
  });

  it.each(["unverified", "current"])("does not offer an update for an %s marketplace candidate", (kind) => {
    const entry = structuredClone(marketplace);
    if (kind === "unverified") entry.plugin.distribution = undefined;
    else entry.update_available = false;
    render(<PluginsView api={api} data={fixture(entry)} />);
    expect(screen.queryByRole("button", { name: "更新" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "可更新" })).toHaveTextContent("0");
  });

  it("prepares a marketplace URL after selecting a ZIP and removes the previous upload", async () => {
    const data = { ...emptyData(), pluginMarketplace: [{ ...marketplace, installed: false }] };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { plugin: { id: installed.id } } }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    render(<PluginsView api={api} data={data} activeTab="install" />);
    fireEvent.click(screen.getByRole("tab", { name: "上传 ZIP" }));
    fireEvent.change(screen.getByLabelText("插件 ZIP 包"), { target: { files: [new File(["synthetic"], "previous.zip", { type: "application/zip" })] } });
    expect(screen.getByText("previous.zip")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "准备安装" }));
    expect(screen.getByRole("tab", { name: "URL 安装" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("下载 URL")).toHaveValue(marketplace.plugin.distribution!.download_url);
    fireEvent.click(screen.getByRole("tab", { name: "上传 ZIP" }));
    expect(screen.getByText("未选择文件")).toBeVisible();
    fireEvent.click(screen.getByRole("tab", { name: "URL 安装" }));
    fireEvent.click(screen.getByRole("button", { name: "安装插件" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toMatchObject({
      download_url: marketplace.plugin.distribution!.download_url,
      checksum_sha256: "a".repeat(64),
    });
  });
});
