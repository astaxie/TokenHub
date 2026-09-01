import { type AppData } from "../core/types";

export type PluginManagerTabKey = "registry" | "chain" | "ui" | "jobs";

export const defaultPluginMarketplaceWebsiteURL = "https://plugins.betokenhub.com";

export const pluginManagerTabs: Array<{ key: PluginManagerTabKey; label: string }> = [
  { key: "registry", label: "已安装插件" },
  { key: "chain", label: "链路注入" },
  { key: "ui", label: "界面模板" },
  { key: "jobs", label: "后台任务" },
];

export function pluginMarketplaceWebsiteURL(data: Pick<AppData, "resources">) {
  const settings = data.resources.settings?.find((item) => item.id === "cfg_gateway") ?? data.resources.settings?.[0];
  const configured = typeof settings?.fields?.plugin_marketplace_url === "string" ? settings.fields.plugin_marketplace_url.trim() : "";
  if (!configured) return defaultPluginMarketplaceWebsiteURL;
  try {
    const url = new URL(configured);
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : defaultPluginMarketplaceWebsiteURL;
  } catch {
    return defaultPluginMarketplaceWebsiteURL;
  }
}
