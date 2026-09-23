export type PluginDetailSection = "overview" | "files" | "settings";

export type PluginDetailRoute = {
  pluginID: string;
  section: PluginDetailSection;
};

export function pluginDetailRouteFromPath(pathname: string): PluginDetailRoute | null {
  const match = pathname.match(/^\/plugins\/([^/]+)(?:\/(files|settings))?\/?$/);
  if (!match?.[1]) return null;
  try {
    const pluginID = decodeURIComponent(match[1]).trim();
    if (!pluginID || pluginID.includes("/")) return null;
    return { pluginID, section: (match[2] as PluginDetailSection | undefined) ?? "overview" };
  } catch {
    return null;
  }
}

export function pluginDetailPath(pluginID: string, section: PluginDetailSection = "overview") {
  const base = `/plugins/${encodeURIComponent(pluginID)}`;
  return section === "overview" ? base : `${base}/${section}`;
}
