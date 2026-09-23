import { isPluginThemeTokenName, isSafePluginCSSValue } from "./plugin-theme";
import { type SIMThemeTokens } from "./sim-registry";

export type PluginThemeOverrides = Record<string, Record<string, string>>;

export const pluginThemeOverridesStorageKey = "tokenhub.admin.sim.theme-overrides.v1";

const maxThemes = 64;
const maxTokensPerTheme = 64;

export function normalizePluginThemeOverrides(
  value: unknown,
  themes: readonly SIMThemeTokens[],
): PluginThemeOverrides {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const availableThemes = new Map(themes.map((theme) => [theme.key, theme]));
  const normalized: PluginThemeOverrides = {};
  for (const [themeKey, rawTokens] of Object.entries(value).slice(0, maxThemes)) {
    const theme = availableThemes.get(themeKey);
    if (!theme || !rawTokens || typeof rawTokens !== "object" || Array.isArray(rawTokens)) continue;
    const declaredTokens = new Set(Object.keys(theme.payload.tokens).map(normalizeTokenName));
    const tokens: Record<string, string> = {};
    for (const [rawName, rawValue] of Object.entries(rawTokens).slice(0, maxTokensPerTheme)) {
      const name = normalizeTokenName(rawName);
      if (!declaredTokens.has(name) || !isPluginThemeTokenName(name) || typeof rawValue !== "string") continue;
      const value = rawValue.trim();
      if (!value || !isSafePluginCSSValue(value)) continue;
      tokens[name] = value;
    }
    if (Object.keys(tokens).length > 0) normalized[themeKey] = tokens;
  }
  return normalized;
}

export function readPluginThemeOverrides(
  themes: readonly SIMThemeTokens[],
  storage?: Pick<Storage, "getItem">,
): PluginThemeOverrides {
  const target = storage ?? (typeof window === "undefined" ? undefined : window.localStorage);
  if (!target) return {};
  const raw = target.getItem(pluginThemeOverridesStorageKey);
  if (!raw) return {};
  try {
    return normalizePluginThemeOverrides(JSON.parse(raw) as unknown, themes);
  } catch {
    return {};
  }
}

export function savePluginThemeOverrides(
  overrides: PluginThemeOverrides,
  storage?: Pick<Storage, "setItem">,
) {
  const target = storage ?? (typeof window === "undefined" ? undefined : window.localStorage);
  if (!target) return;
  target.setItem(pluginThemeOverridesStorageKey, JSON.stringify(overrides));
}

function normalizeTokenName(value: string) {
  return value.trim().replace(/^--/, "");
}
