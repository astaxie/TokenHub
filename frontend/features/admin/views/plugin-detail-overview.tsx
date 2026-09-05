import { Braces, ChevronDown, CircleGauge, LayoutPanelTop, Play, Puzzle, RefreshCw, ShieldCheck } from "lucide-react";
import { type ReactNode } from "react";
import {
  type AdminUIContribution,
  type AppData,
  type PluginCapabilityDescriptor,
  type PluginDescriptor,
  type PluginMarketplacePlugin,
} from "../core/types";
import { formatBytes } from "../domain/formatting";
import { localizedContributionTitle } from "../domain/plugin-localization";
import { pluginMarketplaceDisplay } from "../domain/plugin-marketplace";
import { languageLocale, tx } from "../i18n/runtime";

export type PluginPackageInspection = {
  file_count: number;
  total_size: number;
  files: Array<{ path: string; size: number; kind: string; viewable: boolean }>;
};

type FeatureRow = {
  key: string;
  icon: ReactNode;
  title: string;
  description: string;
};

type TechnicalRow = {
  key: string;
  title: string;
  description: string;
  meta: string;
};

export function PluginOverview({
  actions,
  contributions,
  hooks,
  jobs,
  marketplaceEntry,
  packageInspection,
  plugin,
}: {
  actions: AppData["pluginActions"];
  contributions: AdminUIContribution[];
  hooks: AppData["pluginChain"]["hooks"];
  jobs: AppData["pluginBackgroundJobs"];
  marketplaceEntry?: PluginMarketplacePlugin;
  packageInspection?: PluginPackageInspection;
  plugin: PluginDescriptor;
}) {
  const locale = languageLocale();
  const marketplace = pluginMarketplaceDisplay(marketplaceEntry ? {
    ...marketplaceEntry,
    plugin: {
      ...marketplaceEntry.plugin,
      ...plugin,
      distribution: marketplaceEntry.plugin.distribution ?? plugin.distribution,
      marketplace: marketplaceEntry.plugin.marketplace ?? plugin.marketplace,
    },
  } : plugin, { locale });
  const description = pluginDescription(marketplace.description, marketplace.name, plugin, contributions);
  const publisher = marketplace.publisher.name === "unknown" || marketplace.publisher.name === plugin.source
    ? ""
    : marketplace.publisher.name;
  const features = pluginFeatures(plugin, { actions, contributions, hooks, jobs });

  return (
    <div className="plugin-detail-content">
      {description || features.length > 0 ? (
        <section className="plugin-overview-section plugin-overview-purpose" aria-labelledby="plugin-features-title">
          <div className="plugin-overview-purpose-header">
            <SectionTitle icon={<Puzzle size={18} />} title={tx("主要功能")} id="plugin-features-title" />
            {publisher ? <div className="plugin-overview-publisher"><span>{tx("开发者")}</span><strong>{publisher}</strong></div> : null}
          </div>
          {description ? <p className="plugin-overview-description">{description}</p> : null}
          {features.length > 0 ? (
            <div className="plugin-overview-feature-list">
              {features.map((feature) => (
                <div className="plugin-overview-feature" key={feature.key}>
                  <span className="plugin-overview-feature-icon" aria-hidden="true">{feature.icon}</span>
                  <div><strong>{feature.title}</strong><p>{feature.description}</p></div>
                </div>
              ))}
            </div>
          ) : null}
        </section>
      ) : null}

      <section className="plugin-overview-facts" aria-label={tx("安装与运行")}>
        <OverviewFact label={tx("状态")} value={tx(marketplace.lifecycle.labelKey)} tone={marketplace.lifecycle.tone} />
        <OverviewFact label={tx("版本")} value={marketplace.installedVersion || plugin.version || "-"} />
        <OverviewFact label={tx("安装来源")} value={pluginSourceLabel(plugin.source)} />
        <OverviewFact label={tx("更新")} value={pluginUpdateLabel(plugin, marketplace.updateAvailable)} tone={marketplace.updateAvailable ? "warn" : "neutral"} />
      </section>

      <section className="plugin-overview-section" aria-labelledby="plugin-safety-title">
        <SectionTitle icon={<ShieldCheck size={18} />} title={tx("安全与兼容性")} id="plugin-safety-title" />
        <div className="plugin-overview-assurance-list">
          <AssuranceRow
            label={tx("软件来源")}
            status={tx(marketplace.trust.labelKey)}
            description={trustDescription(marketplace.trust.verdict)}
            tone={marketplace.trust.tone}
          />
          <AssuranceRow
            label={tx("系统兼容性")}
            status={tx(marketplace.compatibility.labelKey)}
            description={compatibilityDescription(marketplace.compatibility.verdict)}
            tone={marketplace.compatibility.tone}
          />
        </div>
      </section>

      <details className="plugin-technical-details">
        <summary>
          <Braces size={17} aria-hidden="true" />
          <span><strong>{tx("开发者信息")}</strong><small>{tx("供插件开发和故障排查使用，普通使用无需展开。")}</small></span>
          <ChevronDown size={17} aria-hidden="true" />
        </summary>
        <div className="plugin-technical-content">
          <dl className="plugin-technical-facts">
            <Definition label={tx("插件标识")} value={plugin.id} />
            <Definition label="Plugin API" value={plugin.compatibility?.plugin_api || "-"} />
            <Definition label={tx("核心版本")} value={plugin.compatibility?.core_version || "-"} />
            <Definition label={tx("插件包")} value={packageLabel(packageInspection)} />
          </dl>
          <TechnicalGroup
            title={tx("功能声明")}
            description={tx("告诉 TokenHub 这个插件提供哪些可调用或可配置的功能。")}
            rows={plugin.capabilities.map(capabilityRow)}
          />
          <TechnicalGroup
            title={tx("请求处理挂载点")}
            description={tx("列出插件会在模型请求处理流程的哪些阶段运行。")}
            rows={hooks.map((hook) => ({
              key: hook.hook_id,
              title: hook.hook_id,
              description: hookDescription(hook.stage),
              meta: [hook.stage, hook.subject, hook.failure_policy].filter(Boolean).join(" · "),
            }))}
          />
          <TechnicalGroup
            title={tx("界面扩展")}
            description={tx("列出插件在管理后台中增加的页面、面板或操作入口。")}
            rows={contributions.map((contribution) => ({
              key: contribution.id,
              title: localizedContributionTitle(contribution, locale) || contribution.id,
              description: tx("这是插件添加到 TokenHub 管理界面的内容。"),
              meta: [contribution.id, contribution.slot, contribution.action].filter(Boolean).join(" · "),
            }))}
          />
          <TechnicalGroup
            title={tx("管理动作")}
            description={tx("列出管理员可以主动执行的插件操作。")}
            rows={actions.map((action) => ({
              key: action.action_id,
              title: action.title || action.action_id,
              description: actionDescription(action.kind),
              meta: [action.action_id, action.kind, action.capability].filter(Boolean).join(" · "),
            }))}
          />
          <TechnicalGroup
            title={tx("后台任务")}
            description={tx("列出插件按计划自动运行的维护或同步任务。")}
            rows={jobs.map((job) => ({
              key: job.job_id,
              title: job.title || job.job_id,
              description: tx("这个任务会按设定的时间计划在后台自动运行。"),
              meta: [job.job_id, job.schedule, job.capability].filter(Boolean).join(" · "),
            }))}
          />
        </div>
      </details>
    </div>
  );
}

function OverviewFact({ label, tone = "neutral", value }: { label: string; tone?: string; value: string }) {
  return <div><span>{label}</span><strong data-tone={tone}>{value}</strong></div>;
}

function SectionTitle({ icon, id, title }: { icon: ReactNode; id: string; title: string }) {
  return <h2 className="plugin-detail-section-title" id={id}>{icon}{title}</h2>;
}

function AssuranceRow({ description, label, status, tone }: { description: string; label: string; status: string; tone: string }) {
  return (
    <div className="plugin-overview-assurance">
      <div><strong>{label}</strong><p>{description}</p></div>
      <span data-tone={tone}>{status}</span>
    </div>
  );
}

function Definition({ label, value }: { label: string; value: string }) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>;
}

function TechnicalGroup({ description, rows, title }: { description: string; rows: TechnicalRow[]; title: string }) {
  if (rows.length === 0) return null;
  return (
    <section className="plugin-technical-group">
      <header><h3>{title}</h3><p>{description}</p></header>
      <div>
        {rows.map((row) => (
          <div className="plugin-technical-row" key={row.key}>
            <div><strong>{row.title}</strong><p>{row.description}</p></div>
            <code>{row.meta || "-"}</code>
          </div>
        ))}
      </div>
    </section>
  );
}

function pluginDescription(description: string, name: string, plugin: PluginDescriptor, contributions: AdminUIContribution[]) {
  const normalized = description.trim();
  if (normalized && normalized !== name && normalized !== plugin.id) return normalized;
  if (contributions.some((item) => item.slot.startsWith("provider."))) {
    return tx("扩展 Provider 的连接设置和账号资源页面，让管理员直接管理插件提供的高级选项。");
  }
  if (plugin.kinds.includes("provider")) return tx("连接模型服务，并让管理员在 TokenHub 中配置和使用它们。");
  if (plugin.kinds.includes("sim")) return tx("调整 TokenHub 管理后台的界面外观和布局。");
  if (plugin.placements.includes("gateway_chain")) return tx("在模型请求经过网关时执行额外的处理逻辑。");
  if (plugin.placements.includes("background")) return tx("在后台执行自动化维护或同步任务。");
  if (plugin.placements.includes("management_action")) return tx("提供可由管理员按需执行的插件操作。");
  if (contributions.length > 0) return tx("在 TokenHub 管理后台中增加相关页面、面板或操作入口。");
  return "";
}

function pluginFeatures(plugin: PluginDescriptor, related: {
  actions: AppData["pluginActions"];
  contributions: AdminUIContribution[];
  hooks: AppData["pluginChain"]["hooks"];
  jobs: AppData["pluginBackgroundJobs"];
}): FeatureRow[] {
  const rows: FeatureRow[] = [];
  const capabilities = plugin.capabilities;
  if (plugin.kinds.includes("provider") || capabilities.some((item) => item.kind.startsWith("provider"))) {
    rows.push({ key: "provider", icon: <CircleGauge size={17} />, title: tx("模型服务接入"), description: tx("在 Provider 管理中添加和配置这个插件支持的模型服务。") });
  }
  if (related.hooks.length > 0 || plugin.placements.includes("gateway_chain")) {
    rows.push({ key: "gateway", icon: <RefreshCw size={17} />, title: tx("请求处理"), description: tx("在模型请求通过网关时自动执行这个插件提供的处理逻辑。") });
  }
  if (capabilities.some((item) => item.kind === "sim") || plugin.kinds.includes("sim")) {
    rows.push({ key: "appearance", icon: <LayoutPanelTop size={17} />, title: tx("界面外观"), description: tx("为 TokenHub 提供界面主题、布局或页面样式。") });
  }
  const contributionSlots = new Set(related.contributions.map((item) => item.slot));
  if (contributionSlots.has("provider.form.section")) {
    rows.push({ key: "provider-form", icon: <LayoutPanelTop size={17} />, title: tx("Provider 高级设置"), description: tx("在新建或编辑 Provider 时增加此插件提供的连接与高级选项。") });
  }
  if (contributionSlots.has("provider.model.panel")) {
    rows.push({ key: "provider-model", icon: <LayoutPanelTop size={17} />, title: tx("模型能力设置"), description: tx("在 Provider 模型页面增加此插件提供的模型能力选项。") });
  }
  if (contributionSlots.has("provider.resource.form.section") || contributionSlots.has("provider.resource.panel")) {
    rows.push({ key: "provider-resource", icon: <LayoutPanelTop size={17} />, title: tx("账号资源设置"), description: tx("在 Provider 账号和资源页面增加此插件提供的配置与状态信息。") });
  }
  if (related.contributions.some((item) => !item.slot.startsWith("provider."))) {
    rows.push({ key: "ui", icon: <LayoutPanelTop size={17} />, title: tx("管理界面"), description: tx("在 TokenHub 管理后台中增加相关页面、面板或操作入口。") });
  }
  if (related.actions.length > 0 || plugin.placements.includes("management_action")) {
    rows.push({ key: "actions", icon: <Play size={17} />, title: tx("管理操作"), description: tx("提供可由管理员按需执行的插件操作。") });
  }
  if (related.jobs.length > 0 || plugin.placements.includes("background")) {
    rows.push({ key: "jobs", icon: <RefreshCw size={17} />, title: tx("自动任务"), description: tx("按计划在后台执行维护或同步任务。") });
  }
  return rows;
}

function capabilityRow(capability: PluginCapabilityDescriptor): TechnicalRow {
  return {
    key: `${capability.kind}:${capability.subject ?? ""}:${capability.name}`,
    title: capability.name,
    description: capabilityDescription(capability),
    meta: [capability.kind, capability.subject, capability.value ? tx("含配置数据") : ""].filter(Boolean).join(" · "),
  };
}

function capabilityDescription(capability: PluginCapabilityDescriptor) {
  if (capability.name === "theme_tokens") return tx("提供可由管理员选择或调整的界面主题值。");
  if (capability.name === "shell_layout") return tx("定义管理后台导航和内容区域的布局方式。");
  if (capability.name === "page_template") return tx("定义特定管理页面的内容布局。");
  if (capability.name === "dashboard_composition") return tx("定义仪表盘中显示的内容及排列方式。");
  if (capability.kind.startsWith("provider")) return tx("声明这个插件支持的模型服务或 Provider 配置。");
  return tx("告诉 TokenHub 这个插件提供的一项扩展功能。");
}

function hookDescription(stage: string) {
  if (stage === "before_provider") return tx("在请求发送给模型服务之前运行。");
  if (stage === "after_provider") return tx("在模型服务返回响应之后运行。");
  return tx("在模型请求处理流程的指定阶段运行。");
}

function actionDescription(kind: string) {
  if (kind === "read") return tx("读取插件信息，不会更改系统配置。");
  if (kind === "test") return tx("检查插件连接或配置是否可以正常工作。");
  if (kind === "mutate") return tx("执行会更改插件或系统状态的管理操作。");
  if (kind === "external_redirect") return tx("打开插件提供的外部管理页面。");
  if (kind === "import_export") return tx("导入或导出这个插件管理的数据。");
  return tx("这是插件提供的一项管理员操作。");
}

function trustDescription(verdict: string) {
  if (verdict === "trusted") return tx("此插件来自受信任来源，或已通过软件包签名验证。");
  if (verdict === "unverified") return tx("此插件未经过签名验证，请确认安装来源可信。");
  if (verdict === "rejected") return tx("此插件未通过信任校验，不能安全加载。");
  return tx("暂时没有足够信息验证此插件的来源。");
}

function compatibilityDescription(verdict: string) {
  if (verdict === "compatible") return tx("此版本可在当前 TokenHub 核心版本上运行。");
  if (verdict === "needs_review") return tx("此版本需要管理员确认兼容性后再使用。");
  if (verdict === "incompatible") return tx("此版本与当前 TokenHub 核心版本不兼容。");
  return tx("插件没有提供可确认的兼容性信息。");
}

function pluginSourceLabel(value: string) {
  if (value === "built_in") return tx("内置");
  if (value === "marketplace") return tx("插件市场");
  if (value === "local_file") return tx("本地文件");
  return value || tx("未声明");
}

function pluginUpdateLabel(plugin: PluginDescriptor, updateAvailable: boolean) {
  if (updateAvailable) return tx("有新版本可用");
  if (plugin.source === "built_in") return tx("随 TokenHub 更新");
  return tx("暂无可用更新");
}

function packageLabel(packageInspection?: PluginPackageInspection) {
  if (!packageInspection) return tx("内置实现");
  return `${packageInspection.file_count} ${tx("个文件")} · ${formatBytes(packageInspection.total_size)}`;
}
