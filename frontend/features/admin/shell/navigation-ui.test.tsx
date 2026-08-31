import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { adminConsoleSIMSelectionPreference, adminConsoleShellState, adminConsoleSIMSelectionStorageKey, readAdminConsoleSIMSelectionPreference, saveAdminConsoleSIMSelectionPreference } from "./admin-console";
import { Sidebar } from "./navigation-ui";

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
});
