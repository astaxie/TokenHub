import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

describe("PluginsView lifecycle controls", () => {
  it("renders rollback-aware lifecycle state without inventing a rollback endpoint", () => {
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
    expect(screen.getByRole("button", { name: "更新" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "卸载" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "启用" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "禁用" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "回滚" })).not.toBeInTheDocument();
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
    expect(screen.queryByRole("button", { name: "禁用" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "更新" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "卸载" })).not.toBeInTheDocument();
  });
});
