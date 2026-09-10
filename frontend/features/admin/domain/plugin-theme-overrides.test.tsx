import { describe, expect, it } from "vitest";
import { normalizePluginThemeOverrides, pluginThemeOverridesStorageKey, readPluginThemeOverrides, savePluginThemeOverrides } from "./plugin-theme-overrides";
import { type SIMThemeTokens } from "./sim-registry";

const theme: SIMThemeTokens = {
  key: "example.sim:theme_tokens:light",
  pluginID: "example.sim",
  pluginName: "Example",
  pluginVersion: "1.0.0",
  name: "theme_tokens",
  id: "light",
  subject: "light",
  title: "Example Light",
  order: 10,
  priority: 0,
  payload: { mode: "light", default: true, tokens: { accent: "#2563eb", surface: "#ffffff" } },
};

describe("plugin theme overrides", () => {
  it("keeps only declared, allowlisted, safe token values", () => {
    expect(normalizePluginThemeOverrides({
      [theme.key]: {
        accent: "#16a34a",
        surface: "url(https://example.test/image.png)",
        "--surface": "u\\72l(https://example.test/image.png)",
        unknown: "#000000",
      },
      "unknown:theme": { accent: "#000000" },
    }, [theme])).toEqual({ [theme.key]: { accent: "#16a34a" } });
  });

  it("round trips normalized overrides through storage", () => {
    const values = { [theme.key]: { accent: "#16a34a" } };
    const store = new Map<string, string>();
    const storage = {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => store.set(key, value),
    };
    savePluginThemeOverrides(values, storage);
    expect(store.get(pluginThemeOverridesStorageKey)).toBe(JSON.stringify(values));
    expect(readPluginThemeOverrides([theme], storage)).toEqual(values);
  });
});
