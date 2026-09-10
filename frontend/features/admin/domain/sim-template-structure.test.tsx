import { describe, expect, it } from "vitest";
import { simRegistryFromPlugins } from "./sim-registry";
import { simTemplateStructure } from "./sim-template-structure";

describe("SIM template structure", () => {
  it("expands shell, page, dashboard, and UI declarations into inspectable blocks", () => {
    const pluginID = "example.sim";
    const registry = simRegistryFromPlugins([{
      id: pluginID,
      name: "Example",
      version: "1.0.0",
      capabilities: [
        { kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "light", mode: "light", tokens: { accent: "#2563eb" } }) },
        { kind: "sim", name: "shell_layout", value: JSON.stringify({ id: "shell", navigation: "sidebar", density: "compact" }) },
        { kind: "sim", name: "page_template", value: JSON.stringify({ id: "detail", target: "provider.detail", layout: "two_column", regions: ["main", "side"] }) },
        { kind: "sim", name: "dashboard_composition", value: JSON.stringify({ id: "dashboard", cards: [{ contribution_id: "cost", region: "main", size: "wide" }] }) },
      ],
    }]);
    const blocks = simTemplateStructure(pluginID, registry, [{ plugin_id: pluginID, id: "search", slot: "nav.section", title: "Search" }]);

    expect(blocks.map((block) => block.kind)).toEqual([
      "theme",
      "navigation",
      "topbar",
      "global_search",
      "account_area",
      "content",
      "page_template",
      "page_region",
      "page_region",
      "dashboard",
      "dashboard_card",
      "ui_contribution",
    ]);
    expect(blocks.find((block) => block.kind === "page_region" && block.title === "side")?.placement).toBe("provider.detail.side");
    expect(blocks.find((block) => block.kind === "dashboard_card")?.details).toMatchObject({ contribution_id: "cost", region: "main" });
  });
});
