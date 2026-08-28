import { describe, expect, it } from "vitest";
import { pluginLayoutDensity, pluginShellPresentation, pluginThemeStyle } from "./plugin-theme";

describe("plugin shell presentation", () => {
  it("applies safe default theme tokens for the active mode", () => {
    const presentation = pluginShellPresentation([
      {
        plugin_id: "tokenhub.sim",
        id: "dark-theme",
        slot: "theme.tokens",
        schema: {
          mode: "dark",
          default: true,
          tokens: {
            accent: "#60a5fa",
            "--surface": "#0f172a",
            unknown: "#ffffff",
            bg: "url(https://example.test/bg.css)",
          },
        },
      },
    ], "dark");

    expect(presentation.themeContribution?.id).toBe("dark-theme");
    expect(presentation.style).toEqual({
      "--accent": "#60a5fa",
      "--surface": "#0f172a",
    });
  });

  it("ignores theme tokens for the inactive mode", () => {
    const presentation = pluginShellPresentation([
      {
        plugin_id: "tokenhub.sim",
        id: "dark-theme",
        slot: "theme.tokens",
        schema: { mode: "dark", tokens: { accent: "#60a5fa" } },
      },
    ], "light");

    expect(presentation.themeContribution).toBeUndefined();
    expect(presentation.style).toBeUndefined();
  });

  it("reads compact layout presets from SIM contributions", () => {
    expect(pluginLayoutDensity({
      plugin_id: "tokenhub.sim",
      id: "ops-layout",
      slot: "layout.preset",
      schema: { preset: { density: "compact" } },
    })).toBe("compact");
  });

  it("drops unsupported token payloads", () => {
    expect(pluginThemeStyle({
      plugin_id: "tokenhub.sim",
      id: "unsafe",
      slot: "theme.tokens",
      schema: { tokens: { accent: "#2563eb; color: red" } },
    })).toBeUndefined();
  });

  it("prefers parsed SIM presentation capabilities over legacy Admin UI contributions", () => {
    const presentation = pluginShellPresentation([
      {
        plugin_id: "tokenhub.legacy",
        id: "legacy-theme",
        slot: "theme.tokens",
        schema: { default: true, tokens: { accent: "#ef4444" } },
      },
      {
        plugin_id: "tokenhub.legacy",
        id: "legacy-layout",
        slot: "layout.preset",
        schema: { default: true, preset: { density: "spacious" } },
      },
    ], "dark", {
      simRegistry: {
        themeTokens: [
          {
            key: "tokenhub.sim.enterprise:theme_tokens:dark",
            pluginID: "tokenhub.sim.enterprise",
            pluginName: "Enterprise SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "dark",
            subject: "dark",
            title: "Enterprise Dark",
            order: 100,
            priority: 0,
            payload: { mode: "dark", default: true, tokens: { accent: "#2563eb" } },
          },
        ],
        shellLayouts: [
          {
            key: "tokenhub.sim.enterprise:shell_layout:ops",
            pluginID: "tokenhub.sim.enterprise",
            pluginName: "Enterprise SIM",
            pluginVersion: "1.0.0",
            name: "shell_layout",
            id: "ops",
            subject: "ops",
            title: "Ops Shell",
            order: 100,
            priority: 0,
            payload: { default: true, layout: { density: "compact" } },
          },
        ],
      },
    });

    expect(presentation.themeCapability?.id).toBe("dark");
    expect(presentation.layoutCapability?.id).toBe("ops");
    expect(presentation.themeContribution).toBeUndefined();
    expect(presentation.layoutContribution).toBeUndefined();
    expect(presentation.style).toEqual({ "--accent": "#2563eb" });
    expect(presentation.density).toBe("compact");
  });

  it("selects active SIM theme and layout before default capabilities", () => {
    const presentation = pluginShellPresentation([], "light", {
      activeSIMPluginID: "tokenhub.sim.selected",
      activeThemeID: "selected-light",
      activeLayoutKey: "tokenhub.sim.selected:shell_layout:selected-shell",
      simRegistry: {
        themeTokens: [
          {
            key: "tokenhub.sim.default:theme_tokens:default-light",
            pluginID: "tokenhub.sim.default",
            pluginName: "Default SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "default-light",
            subject: "",
            title: "Default Light",
            order: 1,
            priority: 100,
            payload: { mode: "light", default: true, tokens: { accent: "#111111" } },
          },
          {
            key: "tokenhub.sim.selected:theme_tokens:selected-light",
            pluginID: "tokenhub.sim.selected",
            pluginName: "Selected SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "selected-light",
            subject: "",
            title: "Selected Light",
            order: 50,
            priority: 0,
            payload: { mode: "light", default: false, tokens: { accent: "#22c55e" } },
          },
        ],
        shellLayouts: [
          {
            key: "tokenhub.sim.default:shell_layout:default-shell",
            pluginID: "tokenhub.sim.default",
            pluginName: "Default SIM",
            pluginVersion: "1.0.0",
            name: "shell_layout",
            id: "default-shell",
            subject: "",
            title: "Default Shell",
            order: 1,
            priority: 100,
            payload: { default: true, layout: { density: "spacious" } },
          },
          {
            key: "tokenhub.sim.selected:shell_layout:selected-shell",
            pluginID: "tokenhub.sim.selected",
            pluginName: "Selected SIM",
            pluginVersion: "1.0.0",
            name: "shell_layout",
            id: "selected-shell",
            subject: "",
            title: "Selected Shell",
            order: 50,
            priority: 0,
            payload: { default: false, layout: { density: "compact" } },
          },
        ],
      },
    });

    expect(presentation.themeCapability?.id).toBe("selected-light");
    expect(presentation.layoutCapability?.id).toBe("selected-shell");
    expect(presentation.style).toEqual({ "--accent": "#22c55e" });
    expect(presentation.density).toBe("compact");
  });

  it("resolves SIM default conflicts by order then priority", () => {
    const presentation = pluginShellPresentation([], "dark", {
      simRegistry: {
        themeTokens: [
          {
            key: "tokenhub.sim.low:theme_tokens:late",
            pluginID: "tokenhub.sim.low",
            pluginName: "Low SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "late",
            subject: "",
            title: "Late",
            order: 20,
            priority: 1000,
            payload: { mode: "dark", default: true, tokens: { accent: "#111111" } },
          },
          {
            key: "tokenhub.sim.tie:theme_tokens:low-priority",
            pluginID: "tokenhub.sim.tie",
            pluginName: "Tie SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "low-priority",
            subject: "",
            title: "Low Priority",
            order: 10,
            priority: 1,
            payload: { mode: "dark", default: true, tokens: { accent: "#222222" } },
          },
          {
            key: "tokenhub.sim.tie:theme_tokens:high-priority",
            pluginID: "tokenhub.sim.tie",
            pluginName: "Tie SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "high-priority",
            subject: "",
            title: "High Priority",
            order: 10,
            priority: 10,
            payload: { mode: "dark", default: true, tokens: { accent: "#333333" } },
          },
        ],
        shellLayouts: [],
      },
    });

    expect(presentation.themeCapability?.id).toBe("high-priority");
    expect(presentation.style).toEqual({ "--accent": "#333333" });
  });

  it("falls back to legacy Admin UI presentation when no SIM capability matches", () => {
    const presentation = pluginShellPresentation([
      {
        plugin_id: "tokenhub.legacy",
        id: "legacy-theme",
        slot: "theme.tokens",
        schema: { mode: "dark", default: true, tokens: { accent: "#f97316" } },
      },
      {
        plugin_id: "tokenhub.legacy",
        id: "legacy-layout",
        slot: "layout.preset",
        schema: { default: true, preset: { density: "spacious" } },
      },
    ], "dark", {
      simRegistry: {
        themeTokens: [
          {
            key: "tokenhub.sim.light:theme_tokens:light",
            pluginID: "tokenhub.sim.light",
            pluginName: "Light SIM",
            pluginVersion: "1.0.0",
            name: "theme_tokens",
            id: "light",
            subject: "",
            title: "Light",
            order: 1,
            priority: 0,
            payload: { mode: "light", default: true, tokens: { accent: "#2563eb" } },
          },
        ],
        shellLayouts: [],
      },
    });

    expect(presentation.themeContribution?.id).toBe("legacy-theme");
    expect(presentation.layoutContribution?.id).toBe("legacy-layout");
    expect(presentation.style).toEqual({ "--accent": "#f97316" });
    expect(presentation.density).toBe("spacious");
  });
});
