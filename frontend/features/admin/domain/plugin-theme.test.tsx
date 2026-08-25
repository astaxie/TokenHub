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
});
