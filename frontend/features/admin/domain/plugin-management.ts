import { type AppData } from "../core/types";

export type PluginManagerTabKey = "registry" | "marketplace" | "chain" | "ui" | "actions";

export const defaultPluginMarketplaceWebsiteURL = "https://plugins.betokenhub.com";

export const pluginManagerTabs: Array<{ key: PluginManagerTabKey; label: string }> = [
  { key: "registry", label: "已安装插件" },
  { key: "marketplace", label: "插件市场" },
  { key: "chain", label: "链路注入" },
  { key: "ui", label: "界面与 SIM" },
  { key: "actions", label: "动作任务" },
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
