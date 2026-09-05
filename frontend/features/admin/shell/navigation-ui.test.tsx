import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { adminConsoleSIMSelectionPreference, adminConsoleShellState, adminConsoleSIMSelectionStorageKey, readAdminConsoleSIMSelectionPreference, saveAdminConsoleSIMSelectionPreference } from "./admin-console";
import { pageRecordCount, Sidebar } from "./navigation-ui";

describe("Sidebar", () => {
  it("keeps plugin nav section contributions out of the sidebar", () => {
    const onSelectPluginPage = vi.fn();
    const data = emptyData();
    data.pluginUI = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      id: "ecosystem-page",
      slot: "nav.section",
      title: "Plugin Ecosystem",
      schema: { description: "Inspect plugins." },
    }];

    render(
      <Sidebar
        activePluginPageKey=""
        activeView="overview"
        collapsed={false}
        data={data}
        onLogout={vi.fn()}
        onSelect={vi.fn()}
        onSelectPluginPage={onSelectPluginPage}
        onToggleCollapse={vi.fn()}
        onToggleGroup={vi.fn()}
        openGroups={{}}
        user={{ id: "usr_admin", username: "admin", name: "Admin", email: "admin@example.test", role: "admin", status: "active" }}
      />,
    );

    expect(screen.getByText("插件管理")).toBeInTheDocument();
    expect(screen.queryByText("插件扩展")).not.toBeInTheDocument();
    expect(screen.queryByText("Plugin Ecosystem")).not.toBeInTheDocument();
    expect(onSelectPluginPage).not.toHaveBeenCalled();
  });

  it("hides plugin nav section contributions from non-admin users", () => {
    const data = emptyData();
    data.pluginUI = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      id: "ecosystem-page",
      slot: "nav.section",
      title: "Plugin Ecosystem",
    }];

    render(
      <Sidebar
        activePluginPageKey=""
        activeView="overview"
        collapsed={false}
        data={data}
        onLogout={vi.fn()}
        onSelect={vi.fn()}
        onSelectPluginPage={vi.fn()}
        onToggleCollapse={vi.fn()}
        onToggleGroup={vi.fn()}
        openGroups={{}}
        user={{ id: "usr_user", username: "user", name: "User", email: "user@example.test", role: "user", status: "active" }}
      />,
    );

    expect(screen.queryByText("插件扩展")).not.toBeInTheDocument();
    expect(screen.queryByText("Plugin Ecosystem")).not.toBeInTheDocument();
  });

  it("passes active SIM selection into shell presentation and persists it", () => {
    const data = emptyData();
    data.plugins = [
      {
        id: "tokenhub.sim.default",
        name: "Default SIM",
        version: "1.0.0",
        source: "built_in",
        kinds: ["sim"],
        placements: ["presentation"],
        capabilities: [
          {
            kind: "sim",
            name: "theme_tokens",
            subject: "default-light",
            value: JSON.stringify({
              id: "default-light",
              title: "Default Light",
              mode: "light",
              default: true,
              tokens: { accent: "#111111" },
            }),
          },
          {
            kind: "sim",
            name: "shell_layout",
            subject: "default-shell",
            value: JSON.stringify({
              id: "default-shell",
              title: "Default Shell",
              default: true,
              layout: { density: "spacious" },
            }),
          },
        ],
      },
      {
        id: "tokenhub.sim.selected",
        name: "Selected SIM",
        version: "1.0.0",
        source: "built_in",
        kinds: ["sim"],
        placements: ["presentation"],
        capabilities: [
          {
            kind: "sim",
            name: "theme_tokens",
            subject: "selected-light",
            value: JSON.stringify({
              id: "selected-light",
              title: "Selected Light",
              mode: "light",
              default: false,
              tokens: { accent: "#22c55e" },
            }),
          },
          {
            kind: "sim",
            name: "shell_layout",
            subject: "selected-shell",
            value: JSON.stringify({
              id: "selected-shell",
              title: "Selected Shell",
              default: false,
              layout: { density: "compact" },
            }),
          },
        ],
      },
    ];

    const shellState = adminConsoleShellState(data, "light", {
      activeSIMPluginID: "tokenhub.sim.selected",
      activeThemeKey: "tokenhub.sim.selected:theme_tokens:selected-light",
      activeLayoutKey: "tokenhub.sim.selected:shell_layout:selected-shell",
    });

    expect(shellState.simSelection.activeSIMPluginID).toBe("tokenhub.sim.selected");
    expect(shellState.simSelection.theme.capability?.id).toBe("selected-light");
    expect(shellState.simSelection.layout.capability?.id).toBe("selected-shell");
    expect(shellState.shellPresentation.themeCapability?.id).toBe("selected-light");
    expect(shellState.shellPresentation.layoutCapability?.id).toBe("selected-shell");
    expect(adminConsoleSIMSelectionPreference(shellState.simSelection)).toEqual({
      simPluginID: "tokenhub.sim.selected",
      themeKey: "tokenhub.sim.selected:theme_tokens:selected-light",
      themeID: "selected-light",
      layoutKey: "tokenhub.sim.selected:shell_layout:selected-shell",
      layoutID: "selected-shell",
    });

    const store = new Map<string, string>();
    const storage = {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => {
        store.set(key, value);
      },
    };

    saveAdminConsoleSIMSelectionPreference(shellState.simSelection, storage);
    expect(store.get(adminConsoleSIMSelectionStorageKey)).toBe(JSON.stringify({
      simPluginID: "tokenhub.sim.selected",
      themeKey: "tokenhub.sim.selected:theme_tokens:selected-light",
      themeID: "selected-light",
      layoutKey: "tokenhub.sim.selected:shell_layout:selected-shell",
      layoutID: "selected-shell",
    }));
    expect(readAdminConsoleSIMSelectionPreference(storage)).toEqual({
      simPluginID: "tokenhub.sim.selected",
      themeKey: "tokenhub.sim.selected:theme_tokens:selected-light",
      themeID: "selected-light",
      layoutKey: "tokenhub.sim.selected:shell_layout:selected-shell",
      layoutID: "selected-shell",
    });
  });

  it("selects the knowledge sidebar SIM template and keeps its shell CSS scoped", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.sim.knowledge-sidebar",
      name: "Knowledge Sidebar",
      version: "built-in",
      source: "built_in",
      kinds: ["sim"],
      placements: ["presentation"],
      capabilities: [
        {
          kind: "sim",
          name: "theme_tokens",
          subject: "knowledge-sidebar-light",
          value: JSON.stringify({
            id: "knowledge-sidebar-light",
            title: "Knowledge Sidebar Light",
            mode: "light",
            tokens: { accent: "#203f78", "accent-weak": "#eaf2ff" },
          }),
        },
        {
          kind: "sim",
          name: "shell_layout",
          subject: "knowledge-rounded-sidebar",
          value: JSON.stringify({
            id: "knowledge-rounded-sidebar",
            title: "Knowledge Rounded Sidebar",
            layout: { density: "comfortable" },
          }),
        },
      ],
    }];

    const shellState = adminConsoleShellState(data, "light", {
      simPluginID: "tokenhub.sim.knowledge-sidebar",
    });

    expect(shellState.simSelection.activeSIMPluginID).toBe("tokenhub.sim.knowledge-sidebar");
    expect(shellState.shellPresentation.style).toMatchObject({
      "--accent": "#203f78",
      "--accent-weak": "#eaf2ff",
    });
    expect(shellState.shellPresentation.density).toBe("comfortable");
    const customizedShellState = adminConsoleShellState(data, "light", {
      simPluginID: "tokenhub.sim.knowledge-sidebar",
    }, {
      "tokenhub.sim.knowledge-sidebar:theme_tokens:knowledge-sidebar-light": { accent: "#16a34a" },
    });
    expect(customizedShellState.shellPresentation.style).toMatchObject({ "--accent": "#16a34a" });
    expect(templateStyles()).toContain('.app-shell[data-sim-plugin-id="tokenhub.sim.knowledge-sidebar"] .nav-title');
    expect(templateStyles()).toContain('.app-shell[data-sim-plugin-id="tokenhub.sim.knowledge-sidebar"] .nav-item.active');
    expect(responsiveStyles()).toContain('.app-shell[data-sim-plugin-id="tokenhub.sim.knowledge-sidebar"].sidebar-collapsed');
  });
});

describe("pageRecordCount", () => {
  it("counts installed plugins for the plugin management page", () => {
    const data = emptyData();
    data.plugins = [
      { id: "example.first", name: "First", version: "1.0.0", source: "local_file", kinds: ["extension"], placements: ["gateway_chain"], capabilities: [] },
      { id: "example.second", name: "Second", version: "1.0.0", source: "built_in", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [] },
    ];

    expect(pageRecordCount("plugins", data)).toBe(2);
  });

  it("reports no records for a page without a countable list", () => {
    expect(pageRecordCount("plugins", emptyData())).toBe(0);
    expect(pageRecordCount("overview", emptyData())).toBe(0);
  });
});

function templateStyles() {
  return readFileSync(resolve("app/styles/redesign/templates.css"), "utf8");
}

function responsiveStyles() {
  return readFileSync(resolve("app/styles/redesign/responsive.css"), "utf8");
}
