import {
  simRegistryFromPlugins,
  type SIMPluginDescriptorLike,
  type SIMShellLayout,
  type SIMThemeMode,
  type SIMThemeTokens,
} from "./sim-registry";

export type SIMSelectionThemeMode = Extract<SIMThemeMode, "light" | "dark">;

export type SIMSelectionPreference = {
  simPluginID: string;
  themeKey: string;
  themeID: string;
  layoutKey: string;
  layoutID: string;
};

export type SIMSelectionPreferencePayload = Partial<{
  active_sim_plugin_id: unknown;
  activeSIMPluginID: unknown;
  sim_plugin_id: unknown;
  simPluginID: unknown;
  plugin_id: unknown;
  pluginID: unknown;
  active_theme_key: unknown;
  activeThemeKey: unknown;
  theme_key: unknown;
  themeKey: unknown;
  active_theme_id: unknown;
  activeThemeID: unknown;
  theme_id: unknown;
  themeID: unknown;
  active_layout_key: unknown;
  activeLayoutKey: unknown;
  layout_key: unknown;
  layoutKey: unknown;
  active_layout_id: unknown;
  activeLayoutID: unknown;
  layout_id: unknown;
  layoutID: unknown;
  sim: unknown;
  theme: unknown;
  layout: unknown;
}>;

export type SIMSelectionWarningCode =
  | "ambiguous_layout_id"
  | "ambiguous_theme_id"
  | "duplicate_capability_key"
  | "duplicate_plugin_id"
  | "malformed_preference"
  | "missing_preferred_layout"
  | "missing_preferred_sim"
  | "missing_preferred_theme";

export type SIMSelectionWarning = {
  code: SIMSelectionWarningCode;
  target: "layout" | "plugin" | "preference" | "theme";
  requested?: string;
  key?: string;
  selected?: string;
  candidates?: string[];
};

export type SIMSelectionSource = "preference" | "plugin_preference" | "default" | "first_available" | "none";

export type SIMSelectionDecision<TCapability> = {
  capability?: TCapability;
  source: SIMSelectionSource;
  matchedBy: "key" | "id" | "plugin" | "default" | "order" | "none";
  requestedKey: string;
  requestedID: string;
  fallback: boolean;
};

export type SIMPluginOption = {
  id: string;
  name: string;
  version: string;
};

export type SIMSelectionInput = {
  plugins?: readonly SIMPluginDescriptorLike[];
  preference?: unknown;
  themeMode?: SIMSelectionThemeMode;
};

export type SIMSelectionResult = {
  preference: SIMSelectionPreference;
  activeSIMPluginID: string;
  activeSIMPlugin?: SIMPluginOption;
  theme: SIMSelectionDecision<SIMThemeTokens>;
  layout: SIMSelectionDecision<SIMShellLayout>;
  warnings: SIMSelectionWarning[];
};

const emptyPreference: SIMSelectionPreference = {
  simPluginID: "",
  themeKey: "",
  themeID: "",
  layoutKey: "",
  layoutID: "",
};

export function resolveSIMSelection(input: SIMSelectionInput): SIMSelectionResult {
  const warnings: SIMSelectionWarning[] = [];
  const preference = simSelectionPreference(input.preference, warnings);
  const registry = simRegistryFromPlugins(input.plugins);
  const plugins = simPluginOptions(input.plugins, registry.themeTokens, registry.shellLayouts, warnings);
  const themeMode = input.themeMode ?? "light";
  const themes = dedupeCapabilities(registry.themeTokens.filter((theme) => themeMatchesMode(theme, themeMode)), "theme", warnings);
  const layouts = dedupeCapabilities(registry.shellLayouts, "layout", warnings);
  const preferredSIMPlugin = preference.simPluginID ? plugins.find((plugin) => plugin.id === preference.simPluginID) : undefined;

  if (preference.simPluginID && !preferredSIMPlugin) {
    warnings.push({
      code: "missing_preferred_sim",
      target: "plugin",
      requested: preference.simPluginID,
    });
  }

  const theme = selectPresentationCapability(themes, "theme", preference, warnings);
  const layout = selectPresentationCapability(layouts, "layout", preference, warnings);
  const activeSIMPluginID = selectedSIMPluginID(preference, preferredSIMPlugin, theme.capability, layout.capability, plugins);

  return {
    preference,
    activeSIMPluginID,
    activeSIMPlugin: plugins.find((plugin) => plugin.id === activeSIMPluginID),
    theme,
    layout,
    warnings,
  };
}

export function simSelectionPreference(payload: unknown, warnings: SIMSelectionWarning[] = []): SIMSelectionPreference {
  const root = objectPayload(payload);
  if (!root) {
    if (payload !== undefined && payload !== null) {
      warnings.push({ code: "malformed_preference", target: "preference" });
    }
    return { ...emptyPreference };
  }
  const sim = objectValue(root.sim);
  const theme = objectValue(root.theme);
  const layout = objectValue(root.layout);
  return {
    simPluginID: firstString(root.active_sim_plugin_id, root.activeSIMPluginID, root.sim_plugin_id, root.simPluginID, root.plugin_id, root.pluginID, sim?.plugin_id, sim?.pluginID, sim?.id),
    themeKey: firstString(root.active_theme_key, root.activeThemeKey, root.theme_key, root.themeKey, theme?.key, theme?.active_key, theme?.activeKey),
    themeID: firstString(root.active_theme_id, root.activeThemeID, root.theme_id, root.themeID, theme?.id, theme?.active_id, theme?.activeID),
    layoutKey: firstString(root.active_layout_key, root.activeLayoutKey, root.layout_key, root.layoutKey, layout?.key, layout?.active_key, layout?.activeKey),
    layoutID: firstString(root.active_layout_id, root.activeLayoutID, root.layout_id, root.layoutID, layout?.id, layout?.active_id, layout?.activeID),
  };
}

function selectPresentationCapability<TCapability extends SIMThemeTokens | SIMShellLayout>(
  capabilities: readonly TCapability[],
  target: "theme" | "layout",
  preference: SIMSelectionPreference,
  warnings: SIMSelectionWarning[],
): SIMSelectionDecision<TCapability> {
  const requestedKey = target === "theme" ? preference.themeKey : preference.layoutKey;
  const requestedID = target === "theme" ? preference.themeID : preference.layoutID;
  const missingCode = target === "theme" ? "missing_preferred_theme" : "missing_preferred_layout";
  const ambiguousCode = target === "theme" ? "ambiguous_theme_id" : "ambiguous_layout_id";
  const byKey = requestedKey ? capabilities.find((capability) => capability.key === requestedKey) : undefined;
  if (byKey) {
    return decision(byKey, "preference", "key", requestedKey, requestedID, false);
  }

  if (requestedKey) {
    warnings.push({ code: missingCode, target, requested: requestedKey });
  }

  if (requestedID) {
    const idMatches = capabilities.filter((capability) => capability.id === requestedID);
    const scoped = preference.simPluginID ? idMatches.find((capability) => capability.pluginID === preference.simPluginID) : undefined;
    const selected = scoped ?? idMatches[0];
    if (selected) {
      if (!scoped && idMatches.length > 1) {
        warnings.push({
          code: ambiguousCode,
          target,
          requested: requestedID,
          selected: selected.key,
          candidates: idMatches.map((capability) => capability.key),
        });
      }
      return decision(selected, "preference", "id", requestedKey, requestedID, Boolean(requestedKey));
    }
    warnings.push({ code: missingCode, target, requested: requestedID });
  }

  if (preference.simPluginID) {
    const pluginMatches = capabilities.filter((capability) => capability.pluginID === preference.simPluginID);
    const selected = pluginMatches.find((capability) => capability.payload.default === true) ?? pluginMatches[0];
    if (selected) return decision(selected, "plugin_preference", "plugin", requestedKey, requestedID, true);
  }

  const defaultCapability = capabilities.find((capability) => capability.payload.default === true);
  if (defaultCapability) return decision(defaultCapability, "default", "default", requestedKey, requestedID, true);

  const first = capabilities[0];
  if (first) return decision(first, "first_available", "order", requestedKey, requestedID, true);

  return {
    source: "none",
    matchedBy: "none",
    requestedKey,
    requestedID,
    fallback: Boolean(requestedKey || requestedID || preference.simPluginID),
  };
}

function decision<TCapability>(
  capability: TCapability,
  source: SIMSelectionSource,
  matchedBy: SIMSelectionDecision<TCapability>["matchedBy"],
  requestedKey: string,
  requestedID: string,
  fallback: boolean,
): SIMSelectionDecision<TCapability> {
  return { capability, source, matchedBy, requestedKey, requestedID, fallback };
}

function selectedSIMPluginID(
  preference: SIMSelectionPreference,
  preferredSIMPlugin: SIMPluginOption | undefined,
  theme: SIMThemeTokens | undefined,
  layout: SIMShellLayout | undefined,
  plugins: readonly SIMPluginOption[],
) {
  if (preferredSIMPlugin) return preferredSIMPlugin.id;
  return theme?.pluginID || layout?.pluginID || plugins[0]?.id || "";
}

function simPluginOptions(
  plugins: readonly SIMPluginDescriptorLike[] | undefined,
  themes: readonly SIMThemeTokens[],
  layouts: readonly SIMShellLayout[],
  warnings: SIMSelectionWarning[],
): SIMPluginOption[] {
  const capabilityPluginIDs = new Set([...themes, ...layouts].map((capability) => capability.pluginID).filter(Boolean));
  const options = new Map<string, SIMPluginOption>();
  const duplicateIDs = new Set<string>();
  for (const plugin of plugins ?? []) {
    const id = stringValue(plugin.id);
    if (!id || (!pluginHasSIMKind(plugin) && !capabilityPluginIDs.has(id))) continue;
    if (options.has(id)) {
      duplicateIDs.add(id);
      continue;
    }
    options.set(id, {
      id,
      name: stringValue(plugin.name) || id,
      version: stringValue(plugin.version),
    });
  }
  for (const capabilityID of capabilityPluginIDs) {
    if (!options.has(capabilityID)) options.set(capabilityID, { id: capabilityID, name: capabilityID, version: "" });
  }
  for (const id of duplicateIDs) {
    warnings.push({ code: "duplicate_plugin_id", target: "plugin", requested: id, selected: id });
  }
  return [...options.values()].sort((left, right) => left.id.localeCompare(right.id));
}

function dedupeCapabilities<TCapability extends SIMThemeTokens | SIMShellLayout>(
  capabilities: readonly TCapability[],
  target: "theme" | "layout",
  warnings: SIMSelectionWarning[],
) {
  const byKey = new Map<string, TCapability>();
  for (const capability of capabilities) {
    const existing = byKey.get(capability.key);
    if (!existing) {
      byKey.set(capability.key, capability);
      continue;
    }
    const winner = compareCapabilityForCollision(capability, existing) < 0 ? capability : existing;
    byKey.set(capability.key, winner);
    warnings.push({
      code: "duplicate_capability_key",
      target,
      key: capability.key,
      selected: winner.key,
      candidates: [existing.key, capability.key],
    });
  }
  return [...byKey.values()].sort(comparePresentationCapability);
}

function compareCapabilityForCollision(left: SIMThemeTokens | SIMShellLayout, right: SIMThemeTokens | SIMShellLayout) {
  return compareNumber(left.order, right.order) ||
    compareNumber(right.priority, left.priority) ||
    left.pluginID.localeCompare(right.pluginID) ||
    left.id.localeCompare(right.id) ||
    left.key.localeCompare(right.key);
}

function comparePresentationCapability(left: SIMThemeTokens | SIMShellLayout, right: SIMThemeTokens | SIMShellLayout) {
  return compareCapabilityForCollision(left, right);
}

function themeMatchesMode(theme: SIMThemeTokens, mode: SIMSelectionThemeMode) {
  return theme.payload.mode === "all" || theme.payload.mode === mode;
}

function objectPayload(payload: unknown): Record<string, unknown> | null {
  if (typeof payload === "string") return parseJSONObject(payload);
  return objectValue(payload);
}

function parseJSONObject(value: string) {
  const trimmed = value.trim();
  if (!trimmed) return null;
  try {
    return objectValue(JSON.parse(trimmed) as unknown);
  } catch {
    return null;
  }
}

function objectValue(value: unknown) {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function firstString(...values: unknown[]) {
  for (const value of values) {
    const text = stringValue(value);
    if (text) return text;
  }
  return "";
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function pluginHasSIMKind(plugin: SIMPluginDescriptorLike) {
  const kinds = Array.isArray((plugin as { kinds?: unknown }).kinds) ? (plugin as { kinds?: unknown[] }).kinds ?? [] : [];
  return kinds.some((kind) => stringValue(kind) === "sim");
}

function compareNumber(left: number, right: number) {
  return left < right ? -1 : left > right ? 1 : 0;
}
