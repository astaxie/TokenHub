import { ArrowLeft, Boxes, File, FileCode2, FolderOpen, LockKeyhole, PackageOpen, Settings } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { type ApiContext, type AppData, type PluginDescriptor } from "../core/types";
import { formatBytes } from "../domain/formatting";
import { localizedPluginName } from "../domain/plugin-localization";
import { type PluginDetailSection } from "../domain/plugin-detail-route";
import { pluginMarketplaceWebsiteURL, type PluginManagerTabKey } from "../domain/plugin-management";
import { type PluginThemeOverrides } from "../domain/plugin-theme-overrides";
import { simRegistryFromPlugins } from "../domain/sim-registry";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { PluginTemplateSettings } from "./plugin-template-settings";
import { PluginManagerHeader } from "./plugin-manager-header";
import { PluginOverview, type PluginPackageInspection } from "./plugin-detail-overview";

type PackageInspection = PluginPackageInspection;

type PluginDetailDescriptor = PluginDescriptor;

type PluginDetailResponse = {
  plugin: PluginDetailDescriptor;
  package?: PackageInspection;
};

type PackageFileContent = {
  path: string;
  size: number;
  kind: string;
  content: string;
};

export function PluginDetailView({
  api,
  activeThemeKey,
  data,
  pluginID,
  section,
  themeOverrides = {},
  onBack,
  onNavigate,
  managerTab = "installed",
  themeMode = "light",
  onSelectManagerTab,
  onThemeTokenOverridesChange,
}: {
  api: ApiContext;
  activeThemeKey?: string;
  data: AppData;
  pluginID: string;
  section: PluginDetailSection;
  themeOverrides?: PluginThemeOverrides;
  onBack: () => void;
  onNavigate: (pluginID: string, section: PluginDetailSection) => void;
  managerTab?: PluginManagerTabKey;
  themeMode?: "light" | "dark";
  onSelectManagerTab?: (tab: PluginManagerTabKey) => void;
  onThemeTokenOverridesChange?: (themeKey: string, values: Record<string, string>) => void;
}) {
  const [detail, setDetail] = useState<PluginDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedPath, setSelectedPath] = useState("");
  const [fileContent, setFileContent] = useState<PackageFileContent | null>(null);
  const [fileLoading, setFileLoading] = useState(false);
  const [fileError, setFileError] = useState("");
  const locale = languageLocale();
  const fallbackPlugin = data.plugins.find((plugin) => plugin.id === pluginID);
  const plugin = detail?.plugin ?? fallbackPlugin;
  const hooks = useMemo(() => data.pluginChain.hooks.filter((hook) => hook.plugin_id === pluginID), [data.pluginChain.hooks, pluginID]);
  const contributions = useMemo(() => data.pluginUI.filter((item) => item.plugin_id === pluginID), [data.pluginUI, pluginID]);
  const actions = useMemo(() => data.pluginActions.filter((item) => item.plugin_id === pluginID), [data.pluginActions, pluginID]);
  const jobs = useMemo(() => data.pluginBackgroundJobs.filter((item) => item.plugin_id === pluginID), [data.pluginBackgroundJobs, pluginID]);
  const simRegistry = useMemo(() => {
    const hasPluginDescriptor = data.plugins.some((item) => item.id === pluginID);
    return simRegistryFromPlugins(hasPluginDescriptor || !detail?.plugin ? data.plugins : [...data.plugins, detail.plugin]);
  }, [data.plugins, detail?.plugin, pluginID]);
  const hasSettings = simRegistry.themeTokens.some((theme) => theme.pluginID === pluginID);
  const pluginAvailable = Boolean(plugin);
  const visibleSection = section === "settings" && !hasSettings ? "overview" : section;

  useEffect(() => {
    if (pluginAvailable && section === "settings" && !hasSettings) onNavigate(pluginID, "overview");
  }, [hasSettings, onNavigate, pluginAvailable, pluginID, section]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setDetail(null);
    setSelectedPath("");
    setFileContent(null);
    setFileError("");
    setFileLoading(false);
    adminFetch(api, `/api/admin/plugins/${encodeURIComponent(pluginID)}/detail`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("读取插件详情")));
        const payload = await response.json() as { data?: PluginDetailResponse };
        if (!payload.data?.plugin) throw new Error(tx("读取插件详情失败"));
        // A response that resolves after the plugin changed belongs to the previous one.
        if (controller.signal.aborted) return;
        setDetail(payload.data);
      })
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setError(reason instanceof Error ? reason.message : tx("读取插件详情失败"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api, pluginID]);

  useEffect(() => {
    if (section !== "files" || selectedPath || !detail?.package) return;
    setSelectedPath(detail.package.files.find((file) => file.viewable)?.path ?? "");
  }, [detail?.package, section, selectedPath]);

  useEffect(() => {
    const path = selectedPath;
    const selected = detail?.package?.files.find((file) => file.path === path);
    // Every path that previews nothing has to clear the preview too, or the state left
    // behind by the previous selection is read as belonging to the current one.
    if (section !== "files" || !detail?.package || !path || !selected?.viewable) {
      setFileLoading(false);
      setFileError("");
      setFileContent(null);
      return;
    }
    const controller = new AbortController();
    setFileLoading(true);
    setFileError("");
    setFileContent(null);
    adminFetch(api, `/api/admin/plugins/${encodeURIComponent(pluginID)}/file?path=${encodeURIComponent(path)}`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("读取插件文件")));
        const payload = await response.json() as { data?: PackageFileContent };
        if (!payload.data) throw new Error(tx("读取插件文件失败"));
        // A preview that resolves after the selection changed belongs to the old file.
        if (controller.signal.aborted) return;
        setFileContent(payload.data);
      })
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setFileError(reason instanceof Error ? reason.message : tx("读取插件文件失败"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setFileLoading(false);
      });
    return () => controller.abort();
  }, [api, detail?.package, pluginID, section, selectedPath]);

  const pluginName = plugin ? localizedPluginName(plugin, locale) : pluginID;
  const marketplaceWebsiteURL = pluginMarketplaceWebsiteURL(data);
  return (
    <div className="plugin-detail-view">
      <PluginManagerHeader
        activeTab={managerTab}
        marketplaceWebsiteURL={marketplaceWebsiteURL}
        onTabChange={(tab) => tab === managerTab ? onBack() : onSelectManagerTab?.(tab)}
      />
      <section className="plugin-detail-surface">
        <header className="plugin-detail-header">
          <button className="plugin-detail-back compact-button secondary" type="button" onClick={onBack}>
            <ArrowLeft size={15} aria-hidden="true" />
            {tx("返回插件列表")}
          </button>
          <div className="plugin-detail-heading">
            <div className="plugin-detail-icon" aria-hidden="true"><PackageOpen size={21} /></div>
            <div>
              <span>{tx("插件详情")}</span>
              <h1>{pluginName}</h1>
              <p>{pluginID}{plugin?.version ? ` · ${plugin.version}` : ""}</p>
            </div>
          </div>
        </header>

        <nav className="plugin-detail-tabs settings-tabs" role="tablist" aria-label={tx("插件详情页面")}>
          <DetailTab active={visibleSection === "overview"} icon={<Boxes size={15} />} label={tx("概览")} onClick={() => onNavigate(pluginID, "overview")} />
          <DetailTab active={visibleSection === "files"} icon={<FolderOpen size={15} />} label={tx("文件")} onClick={() => onNavigate(pluginID, "files")} />
          {hasSettings ? <DetailTab active={visibleSection === "settings"} icon={<Settings size={15} />} label={tx("设置")} onClick={() => onNavigate(pluginID, "settings")} /> : null}
        </nav>

        <div className="plugin-detail-body">
          {loading ? <p className="plugin-detail-state">{tx("正在读取插件详情")}</p> : null}
          {error ? <div className="inline-error" role="alert">{error}</div> : null}
          {!loading && !error && !plugin ? (
            <div className="plugin-detail-empty"><PackageOpen size={30} aria-hidden="true" /><strong>{tx("未找到插件")}</strong></div>
          ) : null}
          {!loading && plugin && visibleSection === "overview" ? (
            <PluginOverview
              actions={actions}
              contributions={contributions}
              hooks={hooks}
              jobs={jobs}
              marketplaceEntry={data.pluginMarketplace.find((entry) => entry.plugin.id === pluginID)}
              packageInspection={detail?.package}
              plugin={plugin}
            />
          ) : null}
          {!loading && plugin && visibleSection === "files" ? (
            <PluginFiles
              content={fileContent}
              error={fileError}
              loading={fileLoading}
              packageInspection={detail?.package}
              selectedPath={selectedPath}
              onSelect={setSelectedPath}
            />
          ) : null}
          {!loading && plugin && visibleSection === "settings" ? (
            <PluginSettings
              activeThemeKey={activeThemeKey}
              pluginID={pluginID}
              registry={simRegistry}
              themeMode={themeMode}
              themeOverrides={themeOverrides}
              onThemeTokenOverridesChange={onThemeTokenOverridesChange}
            />
          ) : null}
        </div>
      </section>
    </div>
  );
}

function DetailTab({ active, icon, label, onClick }: { active: boolean; icon: ReactNode; label: string; onClick: () => void }) {
  return (
    <button className={active ? "active" : ""} type="button" role="tab" aria-selected={active} onClick={onClick}>
      {icon}{label}
    </button>
  );
}

function PluginFiles({ content, error, loading, packageInspection, selectedPath, onSelect }: { content: PackageFileContent | null; error: string; loading: boolean; packageInspection?: PackageInspection; selectedPath: string; onSelect: (path: string) => void }) {
  if (!packageInspection) {
    return <div className="plugin-detail-empty"><PackageOpen size={30} aria-hidden="true" /><strong>{tx("该内置插件没有独立安装包。")}</strong></div>;
  }
  return (
    <div className="plugin-file-explorer">
      <aside className="plugin-file-list" aria-label={tx("文件清单")}>
        <div className="plugin-file-list-header">
          <strong>{tx("文件清单")}</strong>
          <span>{packageInspection.file_count} · {formatBytes(packageInspection.total_size)}</span>
        </div>
        <div className="plugin-file-items">
          {packageInspection.files.map((file) => (
            <button
              className={selectedPath === file.path ? "active" : ""}
              type="button"
              key={file.path}
              disabled={!file.viewable}
              onClick={() => onSelect(file.path)}
              title={file.viewable ? tx("可预览") : tx("此文件不支持在线预览")}
            >
              {file.viewable ? <FileCode2 size={15} aria-hidden="true" /> : <LockKeyhole size={15} aria-hidden="true" />}
              <span><strong>{file.path}</strong><small>{file.kind} · {formatBytes(file.size)}</small></span>
            </button>
          ))}
        </div>
      </aside>
      <section className="plugin-file-preview" aria-label={tx("文件预览")}>
        <header>
          <div>{content ? <FileCode2 size={15} aria-hidden="true" /> : <File size={15} aria-hidden="true" />}<strong>{selectedPath || tx("文件预览")}</strong></div>
          {content ? <span>{content.kind} · {formatBytes(content.size)}</span> : null}
        </header>
        {loading ? <p className="plugin-detail-state">{tx("正在读取插件文件")}</p> : null}
        {error ? <div className="inline-error" role="alert">{error}</div> : null}
        {!loading && !error && content ? <pre><code>{content.content}</code></pre> : null}
        {!loading && !error && !content ? <p className="plugin-file-placeholder">{tx("选择一个文件查看内容")}</p> : null}
      </section>
    </div>
  );
}

function PluginSettings({
  activeThemeKey,
  pluginID,
  registry,
  themeMode,
  themeOverrides,
  onThemeTokenOverridesChange,
}: {
  activeThemeKey?: string;
  pluginID: string;
  registry: ReturnType<typeof simRegistryFromPlugins>;
  themeMode: "light" | "dark";
  themeOverrides: PluginThemeOverrides;
  onThemeTokenOverridesChange?: (themeKey: string, values: Record<string, string>) => void;
}) {
  return (
    <div className="plugin-detail-content">
      <PluginTemplateSettings
        activeThemeKey={activeThemeKey}
        overrides={themeOverrides}
        pluginID={pluginID}
        registry={registry}
        themeMode={themeMode}
        onThemeTokenOverridesChange={onThemeTokenOverridesChange}
      />
    </div>
  );
}
