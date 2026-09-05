import { ExternalLink, Upload } from "lucide-react";
import { pluginManagerTabs, type PluginManagerTabKey } from "../domain/plugin-management";
import { tx } from "../i18n/runtime";

export function PluginManagerHeader({
  activeTab,
  marketplaceWebsiteURL,
  onTabChange,
}: {
  activeTab: PluginManagerTabKey;
  marketplaceWebsiteURL: string;
  onTabChange: (tab: PluginManagerTabKey) => void;
}) {
  return (
    <div className="plugin-manager-topbar">
      <div className="plugin-manager-tabs settings-tabs" role="tablist" aria-label={tx("插件管理模块")}>
        {pluginManagerTabs.map((tab) => (
          <button
            aria-selected={activeTab === tab.key}
            className={activeTab === tab.key ? "settings-tab active" : "settings-tab"}
            key={tab.key}
            onClick={() => onTabChange(tab.key)}
            role="tab"
            type="button"
          >
            {tx(tab.label)}
          </button>
        ))}
      </div>
      <div className="plugin-manager-actions">
        <a className="secondary-button plugin-marketplace-link" href={marketplaceWebsiteURL} rel="noreferrer" target="_blank">
          <ExternalLink size={14} />
          <span>{tx("插件市场")}</span>
        </a>
        <button className="secondary-button plugin-local-install-button" onClick={() => onTabChange("install")} type="button">
          <Upload size={14} />
          <span>{tx("安装本地插件")}</span>
        </button>
      </div>
    </div>
  );
}
