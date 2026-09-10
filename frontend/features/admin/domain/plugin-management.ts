import { type AppData } from "../core/types";

export type PluginManagerTabKey = "installed" | "install";

export type PluginExtensionCategoryKey = "provider" | "chain" | "ui" | "jobs";

export type PluginStatusFilterKey = "all" | "enabled" | "disabled" | "setup" | "updates";

export const defaultPluginMarketplaceWebsiteURL = "";

export const pluginManagerTabs: Array<{ key: PluginManagerTabKey; label: string }> = [
  { key: "installed", label: "已安装插件" },
  { key: "install", label: "浏览插件" },
];

export const pluginExtensionCategories: Array<{ key: PluginExtensionCategoryKey; label: string }> = [
  { key: "provider", label: "Provider 集成" },
  { key: "chain", label: "请求链路" },
  { key: "ui", label: "UI 模板" },
  { key: "jobs", label: "自动化" },
];

export const pluginStatusFilters: Array<{ key: PluginStatusFilterKey; label: string }> = [
  { key: "all", label: "全部插件" },
  { key: "enabled", label: "已启用" },
  { key: "disabled", label: "已禁用" },
  { key: "setup", label: "待配置" },
  { key: "updates", label: "可更新" },
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
