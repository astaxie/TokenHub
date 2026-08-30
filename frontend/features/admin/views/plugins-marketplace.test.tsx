import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginsView } from "./plugins";

describe("PluginsView marketplace view", () => {
  it("renders marketplace metadata, compatibility badges, publisher data, screenshots, and release notes", () => {
    const data = emptyData();
    data.pluginMarketplaceAvailable = true;
    data.pluginMarketplace = [{
      installed: false,
      update_available: false,
      plugin: {
        id: "tokenhub.provider.codex",
        name: "Codex 订阅",
        version: "1.2.3",
        source: "marketplace",
        kinds: ["provider"],
        placements: ["gateway_chain"],
        capabilities: [],
        marketplace: {
          summary: "管理 Codex 订阅",
          categories: ["provider", "subscription"],
          compatibility: {
            verdict: "needs_review",
            badges: [
              { id: "gateway", label: "Gateway", tone: "warn", url: "https://docs.example/gateway" },
            ],
          },
          publisher: {
            id: "tokenhub",
            name: "TokenHub",
            verified: true,
          },
          advisories: [
            {
              id: "CVE-2026-0001",
              severity: "high",
              title: "Header leak",
              url: "https://security.example/CVE-2026-0001",
            },
          ],
          release_notes: [
            {
              version: "1.2.3",
              title: "更安全的代理",
              notes: "修复兼容性问题",
            },
          ],
          screenshots: [
            {
              url: "https://cdn.example/codex.png",
              thumbnail_url: "https://cdn.example/codex-thumb.png",
              alt: "Codex dashboard",
              caption: "Dashboard preview",
            },
          ],
        },
      },
    }];

    const { container } = render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("管理 Codex 订阅")).toBeInTheDocument();
    expect(screen.getByText("provider")).toBeInTheDocument();
    expect(screen.getByText("subscription")).toBeInTheDocument();
    expect(screen.getAllByText("需复核").length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: "Gateway" })).toHaveAttribute("href", "https://docs.example/gateway");
    expect(screen.getByText("TokenHub")).toBeInTheDocument();
    expect(screen.getByText("已验证")).toBeInTheDocument();
    expect(screen.getByText("高危")).toBeInTheDocument();
    expect(screen.getByAltText("Codex dashboard")).toHaveAttribute("src", "https://cdn.example/codex-thumb.png");
    expect(container.querySelector('[data-plugin-marketplace-block="status"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-marketplace-block="compatibility-badges"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-marketplace-block="details"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-marketplace-block="categories"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-marketplace-block="publisher"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-marketplace-block="screenshots"]')).toBeInTheDocument();
    expect(container.querySelector('[data-plugin-marketplace-block="advisories"]')).toBeInTheDocument();
    expect(container.querySelector(".plugin-marketplace-screenshot img")).not.toHaveAttribute("style");
  });

  it("keeps marketplace presentation styles feature-scoped", () => {
    const styles = pluginStyles();

    expect(styles).toContain(".plugin-marketplace-details");
    expect(styles).toContain(".plugin-marketplace-screenshots");
    expect(styles).toContain(".plugin-marketplace-screenshot img");
    expect(styles).toContain(".plugin-marketplace-advisories .tag");
  });
});

function pluginStyles() {
  return readFileSync(resolve("app/styles/redesign/plugins.css"), "utf8");
}
