import { type CSSProperties } from "react";
import { type AdminUIContribution } from "../core/types";
import { type SIMRegistry, type SIMShellLayout, type SIMThemeTokens } from "./sim-registry";

export type PluginLayoutDensity = "compact" | "comfortable" | "spacious";

export type PluginShellPresentationOptions = {
  simRegistry?: Pick<SIMRegistry, "themeTokens" | "shellLayouts">;
  activeSIMPluginID?: string;
  activeThemeID?: string;
  activeThemeKey?: string;
  activeLayoutID?: string;
  activeLayoutKey?: string;
  themeTokenOverrides?: Record<string, string>;
};

export type PluginShellPresentation = {
  style?: CSSProperties;
  density: PluginLayoutDensity;
  themeContribution?: AdminUIContribution;
  layoutContribution?: AdminUIContribution;
  themeCapability?: SIMThemeTokens;
  layoutCapability?: SIMShellLayout;
};

const tokenNames = new Set([
  "bg",
  "surface",
  "surface-2",
  "surface-3",
  "border",
  "border-2",
  "border-strong",
  "ink",
  "ink-2",
  "ink-3",
  "ink-4",
  "accent",
  "accent-2",
  "accent-weak",
  "accent-weak-2",
  "accent-ink",
  "pos",
  "pos-weak",
  "warn",
  "warn-weak",
  "red",
  "chart-grid",
  "page",
  "shell",
  "surface-soft",
  "text",
  "muted",
  "muted-strong",
  "blue",
  "green",
  "amber",
  "shadow-sm",
  "shadow-md",
  "shadow-lg",
  "shadow",
]);

export function pluginShellPresentation(
  contributions: AdminUIContribution[],
  theme: "light" | "dark",
  options: PluginShellPresentationOptions = {},
): PluginShellPresentation {
  const themeCapability = activeThemeCapability(options.simRegistry?.themeTokens, theme, options);
  const layoutCapability = activeLayoutCapability(options.simRegistry?.shellLayouts, options);
  const themeContribution = themeCapability ? undefined : activeThemeContribution(contributions, theme, options);
  const layoutContribution = layoutCapability ? undefined : activeLayoutContribution(contributions, options);
  return {
    style: themeCapability
      ? pluginThemeStyle(themeCapability, options.themeTokenOverrides)
      : themeContribution
        ? pluginThemeStyle(themeContribution)
        : undefined,
    density: pluginLayoutDensity(layoutCapability ?? layoutContribution),
    themeContribution,
    layoutContribution,
    themeCapability,
    layoutCapability,
  };
}

export function pluginThemeStyle(
  contribution: AdminUIContribution | SIMThemeTokens,
  overrides: Record<string, string> = {},
): CSSProperties | undefined {
  const style: Record<string, string> = {};
  for (const { name, defaultValue } of pluginThemeTokenEntries(themeTokenPayload(contribution))) {
    const override = overrides[name] ?? overrides[`--${name}`];
    const normalizedOverride = typeof override === "string" ? override.trim() : "";
    const value = normalizedOverride && isSafePluginCSSValue(normalizedOverride) ? normalizedOverride : defaultValue.trim();
    if (!isSafePluginCSSValue(value)) continue;
    style[`--${name}`] = value;
  }
  return Object.keys(style).length > 0 ? style as CSSProperties : undefined;
}

/**
 * Canonical, allowlisted theme tokens declared by a template, in declaration order.
 *
 * A template may declare the same token twice, once bare and once `--`-prefixed. The
 * rendered shell applies declarations in order, so the later value is the one that takes
 * effect; the entry keeps the position of the first occurrence and the value of the last
 * so the editor offers exactly what the shell will use. A later declaration the shell
 * would refuse to render never displaces an earlier one it would accept.
 */
export function pluginThemeTokenEntries(tokens: unknown): Array<{ name: string; defaultValue: string }> {
  if (!tokens || typeof tokens !== "object" || Array.isArray(tokens)) return [];
  const entries: Array<{ name: string; defaultValue: string }> = [];
  const positions = new Map<string, number>();
  for (const [rawName, rawValue] of Object.entries(tokens)) {
    const name = rawName.trim().replace(/^--/, "");
    if (!isPluginThemeTokenName(name) || typeof rawValue !== "string") continue;
    const position = positions.get(name);
    if (position === undefined) {
      positions.set(name, entries.length);
      entries.push({ name, defaultValue: rawValue });
      continue;
    }
    if (!isSafePluginCSSValue(rawValue.trim()) && isSafePluginCSSValue(entries[position].defaultValue.trim())) continue;
    entries[position] = { name, defaultValue: rawValue };
  }
  return entries;
}

export function pluginLayoutDensity(contribution?: AdminUIContribution | SIMShellLayout): PluginLayoutDensity {
  const density = layoutDensityPayload(contribution);
  if (density === "compact" || density === "spacious") return density;
  return "comfortable";
}

function activeThemeCapability(
  capabilities: readonly SIMThemeTokens[] | undefined,
  theme: "light" | "dark",
  options: PluginShellPresentationOptions,
) {
  const candidates = sortedSIMCapabilities(capabilities ?? [])
    .filter((capability) => themeCapabilityMatches(capability, theme));
  return selectSIMCapability(candidates, "theme", options);
}

function activeLayoutCapability(capabilities: readonly SIMShellLayout[] | undefined, options: PluginShellPresentationOptions) {
  return selectSIMCapability(sortedSIMCapabilities(capabilities ?? []), "layout", options);
}

function activeThemeContribution(contributions: AdminUIContribution[], theme: "light" | "dark", options: PluginShellPresentationOptions) {
  const candidates = sortedAdminUIContributions(contributions)
    .filter((contribution) => contribution.slot === "theme.tokens" && themeContributionMatches(contribution, theme));
  return selectAdminUIContribution(candidates, "theme", options);
}

function activeLayoutContribution(contributions: AdminUIContribution[], options: PluginShellPresentationOptions) {
  const candidates = sortedAdminUIContributions(contributions).filter((contribution) => contribution.slot === "layout.preset");
  return selectAdminUIContribution(candidates, "layout", options);
}

function selectSIMCapability<TCapability extends SIMThemeTokens | SIMShellLayout>(
  candidates: TCapability[],
  target: "theme" | "layout",
  options: PluginShellPresentationOptions,
): TCapability | undefined {
  const activeKey = target === "theme" ? options.activeThemeKey : options.activeLayoutKey;
  const activeID = target === "theme" ? options.activeThemeID : options.activeLayoutID;
  return candidates.find((capability) => activeKey && capability.key === activeKey) ??
    candidates.find((capability) => activeID && capability.id === activeID) ??
    candidates.find((capability) => options.activeSIMPluginID && capability.pluginID === options.activeSIMPluginID) ??
    candidates.find((capability) => capability.payload.default === true) ??
    candidates[0];
}

function selectAdminUIContribution(
  candidates: AdminUIContribution[],
  target: "theme" | "layout",
  options: PluginShellPresentationOptions,
): AdminUIContribution | undefined {
  const activeKey = target === "theme" ? options.activeThemeKey : options.activeLayoutKey;
  const activeID = target === "theme" ? options.activeThemeID : options.activeLayoutID;
  return candidates.find((contribution) => activeKey && adminUIContributionKey(contribution) === activeKey) ??
    candidates.find((contribution) => activeID && contribution.id === activeID) ??
    candidates.find((contribution) => options.activeSIMPluginID && contribution.plugin_id === options.activeSIMPluginID) ??
    candidates.find((contribution) => contribution.schema?.default === true) ??
    candidates[0];
}

function themeContributionMatches(contribution: AdminUIContribution, theme: "light" | "dark") {
  const mode = contribution.schema?.mode;
  return mode === undefined || mode === "all" || mode === theme;
}

function themeCapabilityMatches(capability: SIMThemeTokens, theme: "light" | "dark") {
  const mode = capability.payload.mode;
  return mode === "all" || mode === theme;
}

function themeTokenPayload(contribution: AdminUIContribution | SIMThemeTokens) {
  return "payload" in contribution ? contribution.payload.tokens : contribution.schema?.tokens;
}

function layoutDensityPayload(contribution?: AdminUIContribution | SIMShellLayout) {
  if (!contribution) return undefined;
  if ("payload" in contribution) {
    const layout = objectValue(contribution.payload.layout);
    const preset = objectValue(contribution.payload.preset);
    return stringValue(layout?.density) || stringValue(preset?.density) || stringValue(contribution.payload.density);
  }
  const preset = objectValue(contribution.schema?.preset);
  return stringValue(preset?.density);
}

function sortedSIMCapabilities<TCapability extends SIMThemeTokens | SIMShellLayout>(capabilities: readonly TCapability[]) {
  return [...capabilities].sort(compareSIMPresentationCapabilities);
}

function compareSIMPresentationCapabilities(left: SIMThemeTokens | SIMShellLayout, right: SIMThemeTokens | SIMShellLayout) {
  return compareNumber(left.order, right.order) ||
    compareNumber(right.priority, left.priority) ||
    left.pluginID.localeCompare(right.pluginID) ||
    left.name.localeCompare(right.name) ||
    left.id.localeCompare(right.id) ||
    left.key.localeCompare(right.key);
}

function sortedAdminUIContributions(contributions: AdminUIContribution[]) {
  return [...contributions].sort(compareAdminUIContributions);
}

function compareAdminUIContributions(left: AdminUIContribution, right: AdminUIContribution) {
  return compareNumber(numberValue(left.schema?.order, 1000), numberValue(right.schema?.order, 1000)) ||
    compareNumber(numberValue(right.schema?.priority, 0), numberValue(left.schema?.priority, 0)) ||
    left.plugin_id.localeCompare(right.plugin_id) ||
    left.slot.localeCompare(right.slot) ||
    left.id.localeCompare(right.id);
}

function adminUIContributionKey(contribution: AdminUIContribution) {
  return [contribution.plugin_id, contribution.slot, contribution.id].filter(Boolean).join(":");
}

function objectValue(value: unknown) {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function numberValue(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function compareNumber(left: number, right: number) {
  return left < right ? -1 : left > right ? 1 : 0;
}

export function isPluginThemeTokenName(name: string) {
  return tokenNames.has(name.trim().replace(/^--/, ""));
}

export function isSafePluginCSSValue(value: string) {
  if (!value || value.length > 180) return false;
  const normalized = value.toLowerCase();
  return !["url(", "@import", "expression(", "javascript:", "<", ">", "{", "}", ";", "\\", "\"", "'"].some((blocked) => normalized.includes(blocked));
}
