import { ArrowLeft, Boxes, Braces, File, FileCode2, FolderOpen, LockKeyhole, PackageOpen, Settings, ShieldCheck, Workflow } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { type AdminUIContribution, type ApiContext, type AppData, type PluginDescriptor } from "../core/types";
import { formatBytes } from "../domain/formatting";
import { localizedContributionTitle, localizedPluginName } from "../domain/plugin-localization";
import { type PluginDetailSection } from "../domain/plugin-detail-route";
import { type PluginThemeOverrides } from "../domain/plugin-theme-overrides";
import { simRegistryFromPlugins } from "../domain/sim-registry";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { PluginTemplateSettings } from "./plugin-template-settings";

type PluginPermission = {
  kind: string;
  name: string;
  access: string;
  sensitivity: string;
};

type PackageFile = {
  path: string;
  size: number;
  kind: string;
  viewable: boolean;
};

type PackageInspection = {
  file_count: number;
  total_size: number;
  files: PackageFile[];
};

type PluginDetailDescriptor = PluginDescriptor & {
  permissions?: PluginPermission[];
};

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
  const simRegistry = useMemo(() => simRegistryFromPlugins(data.plugins), [data.plugins]);

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
  return (
    <div className="plugin-detail-view">
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
        <DetailTab active={section === "overview"} icon={<Boxes size={15} />} label={tx("概览")} onClick={() => onNavigate(pluginID, "overview")} />
        <DetailTab active={section === "files"} icon={<FolderOpen size={15} />} label={tx("文件")} onClick={() => onNavigate(pluginID, "files")} />
        <DetailTab active={section === "settings"} icon={<Settings size={15} />} label={tx("配置")} onClick={() => onNavigate(pluginID, "settings")} />
      </nav>

      {loading ? <p className="plugin-detail-state">{tx("正在读取插件详情")}</p> : null}
      {error ? <div className="inline-error" role="alert">{error}</div> : null}
      {!loading && !error && !plugin ? (
        <div className="plugin-detail-empty"><PackageOpen size={30} aria-hidden="true" /><strong>{tx("未找到插件")}</strong></div>
      ) : null}
      {!loading && plugin && section === "overview" ? (
        <PluginOverview
          actions={actions}
          contributions={contributions}
          hooks={hooks}
          jobs={jobs}
          packageInspection={detail?.package}
          plugin={plugin}
          onOpenSettings={() => onNavigate(pluginID, "settings")}
        />
      ) : null}
      {!loading && plugin && section === "files" ? (
        <PluginFiles
          content={fileContent}
          error={fileError}
          loading={fileLoading}
          packageInspection={detail?.package}
          selectedPath={selectedPath}
          onSelect={setSelectedPath}
        />
      ) : null}
      {!loading && plugin && section === "settings" ? (
        <PluginSettings
          activeThemeKey={activeThemeKey}
          contributions={contributions}
          permissions={detail?.plugin.permissions ?? []}
          pluginID={pluginID}
          registry={simRegistry}
          themeOverrides={themeOverrides}
          onThemeTokenOverridesChange={onThemeTokenOverridesChange}
        />
      ) : null}
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

function PluginOverview({
  actions,
  contributions,
  hooks,
  jobs,
  packageInspection,
  plugin,
  onOpenSettings,
}: {
  actions: AppData["pluginActions"];
  contributions: AdminUIContribution[];
  hooks: AppData["pluginChain"]["hooks"];
  jobs: AppData["pluginBackgroundJobs"];
  packageInspection?: PackageInspection;
  plugin: PluginDetailDescriptor;
  onOpenSettings: () => void;
}) {
  return (
    <div className="plugin-detail-content">
      <section className="plugin-detail-summary" aria-label={tx("插件摘要")}>
        <SummaryItem label={tx("来源")} value={pluginSourceLabel(plugin.source)} />
        <SummaryItem label={tx("状态")} value={pluginStatusLabel(plugin.status)} />
        <SummaryItem label={tx("类型")} value={plugin.kinds.join(", ") || "-"} />
        <SummaryItem label={tx("运行位置")} value={plugin.placements.join(", ") || "-"} />
        <SummaryItem label={tx("文件数量")} value={packageInspection ? String(packageInspection.file_count) : "-"} />
        <SummaryItem label={tx("包大小")} value={packageInspection ? formatBytes(packageInspection.total_size) : "-"} />
      </section>

      <section className="plugin-detail-section">
        <SectionTitle icon={<ShieldCheck size={17} />} title={tx("信任与兼容性")} />
        <dl className="plugin-detail-definition-grid">
          <Definition label={tx("信任")} value={plugin.trust?.verdict || tx("未声明")} />
          <Definition label={tx("兼容性")} value={plugin.compatibility?.verdict || tx("未声明")} />
          <Definition label="Plugin API" value={plugin.compatibility?.plugin_api || "-"} />
          <Definition label={tx("核心版本")} value={plugin.compatibility?.core_version || "-"} />
          <Definition label={tx("可加载")} value={plugin.loadable === false ? tx("否") : tx("是")} />
          <Definition label={tx("独立插件包")} value={packageInspection ? tx("是") : tx("内置实现")} />
        </dl>
      </section>

      <section className="plugin-detail-section">
        <SectionTitle icon={<Braces size={17} />} title={tx("实现清单")} />
        <div className="plugin-detail-counts">
          <Count label={tx("能力")} value={plugin.capabilities.length} />
          <Count label="Hook" value={hooks.length} />
          <Count label={tx("界面贡献")} value={contributions.length} />
          <Count label={tx("动作")} value={actions.length} />
          <Count label={tx("后台任务")} value={jobs.length} />
        </div>
        <ImplementationGroup title={tx("能力")} empty={tx("暂无声明能力")} rows={plugin.capabilities.map((item) => ({ key: `${item.kind}:${item.subject ?? ""}:${item.name}:${item.value ?? ""}`, title: item.name, meta: [item.kind, item.subject, item.value].filter(Boolean).join(" · ") }))} />
        <ImplementationGroup title="Hook" empty={tx("暂无链路 Hook")} rows={hooks.map((item) => ({ key: item.hook_id, title: item.hook_id, meta: [item.stage, item.subject, item.failure_policy].filter(Boolean).join(" · ") }))} />
        <ImplementationGroup title={tx("界面贡献")} empty={tx("暂无界面贡献")} rows={contributions.map((item) => ({ key: item.id, title: localizedContributionTitle(item, languageLocale()) || item.id, meta: [item.slot, item.action].filter(Boolean).join(" · "), onSelect: onOpenSettings }))} />
        <ImplementationGroup title={tx("动作")} empty={tx("暂无插件动作")} rows={actions.map((item) => ({ key: item.action_id, title: item.title || item.action_id, meta: [item.kind, item.capability].filter(Boolean).join(" · ") }))} />
        <ImplementationGroup title={tx("后台任务")} empty={tx("暂无后台任务")} rows={jobs.map((item) => ({ key: item.job_id, title: item.title || item.job_id, meta: [item.schedule, item.capability].filter(Boolean).join(" · ") }))} />
      </section>
    </div>
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
  contributions,
  permissions,
  pluginID,
  registry,
  themeOverrides,
  onThemeTokenOverridesChange,
}: {
  activeThemeKey?: string;
  contributions: AdminUIContribution[];
  permissions: PluginPermission[];
  pluginID: string;
  registry: ReturnType<typeof simRegistryFromPlugins>;
  themeOverrides: PluginThemeOverrides;
  onThemeTokenOverridesChange?: (themeKey: string, values: Record<string, string>) => void;
}) {
  return (
    <div className="plugin-detail-content">
      <PluginTemplateSettings
        activeThemeKey={activeThemeKey}
        contributions={contributions}
        overrides={themeOverrides}
        pluginID={pluginID}
        registry={registry}
        onThemeTokenOverridesChange={onThemeTokenOverridesChange}
      />
      <section className="plugin-detail-section">
        <SectionTitle icon={<ShieldCheck size={17} />} title={tx("权限声明")} />
        {permissions.length ? (
          <div className="plugin-permission-list">
            {permissions.map((permission) => (
              <div key={`${permission.kind}:${permission.name}:${permission.access}`}>
                <strong>{permission.name}</strong>
                <span>{permission.kind} · {permission.access} · {permission.sensitivity}</span>
              </div>
            ))}
          </div>
        ) : <p className="plugin-detail-empty-line">{tx("暂无声明权限")}</p>}
      </section>
      <section className="plugin-detail-section">
        <SectionTitle icon={<Workflow size={17} />} title={tx("配置贡献")} />
        {contributions.length ? contributions.map((item) => (
          <article className="plugin-config-contribution" key={item.id}>
            <div><strong>{localizedContributionTitle(item, languageLocale()) || item.id}</strong><span>{item.slot}</span></div>
            {item.schema ? <pre><code>{JSON.stringify(item.schema, null, 2)}</code></pre> : <p>{tx("该贡献没有声明配置 Schema。")}</p>}
          </article>
        )) : <p className="plugin-detail-empty-line">{tx("暂无配置贡献")}</p>}
      </section>
    </div>
  );
}

function SummaryItem({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function Definition({ label, value }: { label: string; value: string }) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>;
}

function Count({ label, value }: { label: string; value: number }) {
  return <div><strong>{value}</strong><span>{label}</span></div>;
}

function SectionTitle({ icon, title }: { icon: ReactNode; title: string }) {
  return <h2 className="plugin-detail-section-title">{icon}{title}</h2>;
}

function ImplementationGroup({ title, empty, rows }: { title: string; empty: string; rows: Array<{ key: string; title: string; meta: string; onSelect?: () => void }> }) {
  return (
    <div className="plugin-implementation-group">
      <h3>{title}<span>{rows.length}</span></h3>
      {rows.length ? <div>{rows.map((row) => row.onSelect ? (
        <button className="plugin-implementation-row plugin-implementation-link" type="button" key={row.key} onClick={row.onSelect}>
          <strong>{row.title}</strong><span>{row.meta || "-"}</span>
        </button>
      ) : <div className="plugin-implementation-row" key={row.key}><strong>{row.title}</strong><span>{row.meta || "-"}</span></div>)}</div> : <p>{empty}</p>}
    </div>
  );
}

function pluginSourceLabel(value: string) {
  if (value === "built_in") return tx("内置");
  if (value === "marketplace") return tx("插件市场");
  if (value === "local_file") return tx("本地文件");
  return value || tx("未声明");
}

function pluginStatusLabel(value?: string) {
  if (value === "enabled") return tx("已启用");
  if (value === "disabled") return tx("已禁用");
  return value || tx("未声明");
}
