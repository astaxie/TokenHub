import { type CSSProperties } from "react";
import { type AdminUIContribution } from "../core/types";

export type PluginLayoutDensity = "compact" | "comfortable" | "spacious";

export type PluginShellPresentation = {
  style?: CSSProperties;
  density: PluginLayoutDensity;
  themeContribution?: AdminUIContribution;
  layoutContribution?: AdminUIContribution;
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

export function pluginShellPresentation(contributions: AdminUIContribution[], theme: "light" | "dark"): PluginShellPresentation {
  const themeContribution = activeThemeContribution(contributions, theme);
  const layoutContribution = activeLayoutContribution(contributions);
  return {
    style: themeContribution ? pluginThemeStyle(themeContribution) : undefined,
    density: pluginLayoutDensity(layoutContribution),
    themeContribution,
    layoutContribution,
  };
}

export function pluginThemeStyle(contribution: AdminUIContribution): CSSProperties | undefined {
  const tokens = contribution.schema?.tokens;
  if (!tokens || typeof tokens !== "object" || Array.isArray(tokens)) return undefined;
  const style: Record<string, string> = {};
  for (const [rawName, rawValue] of Object.entries(tokens)) {
    const name = rawName.trim().replace(/^--/, "");
    if (!tokenNames.has(name) || typeof rawValue !== "string") continue;
    const value = rawValue.trim();
    if (!safeCSSValue(value)) continue;
    style[`--${name}`] = value;
  }
  return Object.keys(style).length > 0 ? style as CSSProperties : undefined;
}

export function pluginLayoutDensity(contribution?: AdminUIContribution): PluginLayoutDensity {
  const preset = contribution?.schema?.preset;
  if (!preset || typeof preset !== "object" || Array.isArray(preset)) return "comfortable";
  const density = (preset as { density?: unknown }).density;
  if (density === "compact" || density === "spacious") return density;
  return "comfortable";
}

function activeThemeContribution(contributions: AdminUIContribution[], theme: "light" | "dark") {
  const candidates = contributions.filter((contribution) => contribution.slot === "theme.tokens" && themeContributionMatches(contribution, theme));
  return candidates.find((contribution) => contribution.schema?.default === true) ?? candidates[0];
}

function activeLayoutContribution(contributions: AdminUIContribution[]) {
  const candidates = contributions.filter((contribution) => contribution.slot === "layout.preset");
  return candidates.find((contribution) => contribution.schema?.default === true) ?? candidates[0];
}

function themeContributionMatches(contribution: AdminUIContribution, theme: "light" | "dark") {
  const mode = contribution.schema?.mode;
  return mode === undefined || mode === "all" || mode === theme;
}

function safeCSSValue(value: string) {
  if (!value || value.length > 180) return false;
  const normalized = value.toLowerCase();
  return !["url(", "@import", "expression(", "javascript:", "<", ">", "{", "}", ";"].some((blocked) => normalized.includes(blocked));
}
