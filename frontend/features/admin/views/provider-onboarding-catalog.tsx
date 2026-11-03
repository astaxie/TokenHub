import { useState } from "react";
import { Boxes, Check, Plus, RefreshCw, Search } from "lucide-react";
import type { AdminUIContribution, ApiContext, Provider, ProviderCatalogEntry, ProviderCredentialMode } from "../core/types";
import { adminFetch, readAdminError } from "../resources/payloads";
import { providerBrandIconSource } from "../domain/provider-brand-icons";
import { providerCatalogCategory } from "../domain/provider-onboarding-categories";
import { languageLocale, tx } from "../i18n/runtime";

type CatalogCategory = "account" | "provider" | "aggregator" | "local";
type CatalogFilter = "all" | CatalogCategory;

function ProviderBrandIcon({ entry, label }: { entry: ProviderCatalogEntry; label: string }) {
  const source = providerBrandIconSource(entry);
  const [failedSource, setFailedSource] = useState("");
  const hasIcon = Boolean(source && source !== failedSource);
  const className = `provider-onboarding-card-icon${hasIcon ? "" : " fallback"}`;
  return <span aria-hidden="true" className={className} title={label}>
    {hasIcon ? <img alt="" src={source} onError={() => setFailedSource(source)} /> : <Boxes size={22} />}
  </span>;
}

export function ProviderOnboardingCatalog({ api, directEntries, accountEntries, providers, query, onQueryChange, onSelect, onCustom, contributions }: {
  api: ApiContext;
  directEntries: ProviderCatalogEntry[];
  accountEntries: ProviderCatalogEntry[];
  providers: Provider[];
  query: string;
  onQueryChange: (value: string) => void;
  onSelect: (entry: ProviderCatalogEntry, mode: ProviderCredentialMode) => void;
  onCustom: () => void;
  contributions: AdminUIContribution[];
}) {
  const [filter, setFilter] = useState<CatalogFilter>("all");
  const [syncState, setSyncState] = useState<{ loading: boolean; message: string }>({ loading: false, message: "" });

  async function syncCatalog() {
    if (syncState.loading) return;
    setSyncState({ loading: true, message: "" });
    try {
      const resp = await adminFetch(api, "/api/admin/provider-catalog-sync", { method: "POST" });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("目录同步失败，请稍后重试。")));
      const payload = await resp.json() as { providers?: number };
      setSyncState({
        loading: false,
        message: tx("同步供应商目录完成：已加载 {count} 个供应商。").replace("{count}", new Intl.NumberFormat(languageLocale()).format(payload.providers ?? 0)),
      });
    } catch {
      setSyncState({ loading: false, message: tx("目录同步失败，请稍后重试。") });
    }
  }
  const normalized = query.trim().toLowerCase();
  const apiEntries = directEntries.filter(entry => entry.id !== "custom");
  const groups: Array<{ id: CatalogCategory; title: string; mode: ProviderCredentialMode; entries: ProviderCatalogEntry[] }> = [
    { id: "account", title: tx("订阅与账号"), mode: "account_integration", entries: accountEntries },
    { id: "provider", title: tx("供应商"), mode: "provider_api_key", entries: apiEntries.filter(entry => providerCatalogCategory(entry) === "provider") },
    { id: "aggregator", title: tx("中转聚合"), mode: "provider_api_key", entries: apiEntries.filter(entry => providerCatalogCategory(entry) === "aggregator") },
    { id: "local", title: tx("本地部署"), mode: "provider_api_key", entries: apiEntries.filter(entry => providerCatalogCategory(entry) === "local") },
  ];
  function cardDetails(entry: ProviderCatalogEntry) {
    const contribution = contributions.find(item => item.slot === "provider.catalog.card" && item.provider_types?.includes(entry.type));
    const description = contribution?.schema?.description;
    return { title: contribution?.title || entry.display_name || entry.name, description: typeof description === "string" ? description : entry.base_url || "" };
  }
  function isConnected(entry: ProviderCatalogEntry) {
    return providers.some(provider => provider.options?.catalog_id === entry.id || (!provider.options?.catalog_id && provider.type === entry.type && provider.base_url === entry.base_url));
  }
  const searchedGroups = groups.map(group => ({ ...group, total: group.entries.length, entries: group.entries.filter(entry => {
    const details = cardDetails(entry);
    return !normalized || [entry.id, entry.name, entry.display_name, entry.base_url, details.title, details.description].join(" ").toLowerCase().includes(normalized);
  }) }));
  const searchedEntries = searchedGroups.flatMap(group => group.entries);
  const filters: Array<{ id: CatalogFilter; label: string; count: number }> = [
    { id: "all", label: tx("全部"), count: searchedEntries.length },
    ...searchedGroups.filter(group => group.total > 0).map(group => ({ id: group.id, label: group.title, count: group.entries.length })),
  ];
  const activeFilter = filters.some(item => item.id === filter) ? filter : "all";
  const filteredGroups = searchedGroups.filter(group => activeFilter === "all" || activeFilter === group.id);
  return <section className="provider-onboarding-catalog" aria-label={tx("选择供应商")}>
    <p className="provider-onboarding-intro">{tx("选择你要接入的服务，下一步填写密钥或登录账号。")}</p>
    <label className="search-box provider-onboarding-search"><Search size={16} /><input autoFocus value={query} onChange={event => onQueryChange(event.target.value)} placeholder={tx("搜索供应商名称或地址")} /></label>
    <div className="provider-onboarding-filters" role="group" aria-label={tx("供应商分类")}>
      <button type="button" className="provider-onboarding-sync" onClick={syncCatalog} disabled={syncState.loading} title={tx("从公共目录拉取最新的供应商模板，通常每天自动同步一次。")}>
        <RefreshCw size={14} style={syncState.loading ? { animation: "provider-onboarding-sync-rotate 1s linear infinite" } : undefined} />{" "}{syncState.loading ? tx("正在同步…") : tx("同步供应商目录")}
      </button>
      {syncState.message ? <span className="provider-onboarding-sync-status">{syncState.message}</span> : null}
      {filters.map(item => <button key={item.id} type="button" aria-pressed={activeFilter === item.id} onClick={() => setFilter(item.id)}>
        {item.label}{" "}<span>{new Intl.NumberFormat(languageLocale()).format(item.count)}</span>
      </button>)}
    </div>
    {filteredGroups.map(group => group.entries.length > 0 ? <section className="provider-onboarding-group" key={group.id}>
      <h3>{group.title}</h3>
      <div className="provider-onboarding-grid">{group.entries.map(entry => {
        const details = cardDetails(entry);
        const connected = isConnected(entry);
        return <button className="provider-onboarding-card" key={entry.id} onClick={() => onSelect(entry, group.mode)} type="button">
          <ProviderBrandIcon entry={entry} label={details.title} />
          <span className="provider-onboarding-card-copy"><strong>{details.title}</strong><small title={details.description}>{details.description || tx(group.mode === "account_integration" ? "账号授权" : "API Key")}</small></span>
          {connected ? <span className="provider-onboarding-connected"><Check size={13} />{tx("已接入")}</span> : null}
        </button>;
      })}</div>
    </section> : null)}
    {filteredGroups.every(group => group.entries.length === 0) ? <p className="empty compact-empty">{tx("没有匹配的供应商，可使用自定义接入。")}</p> : null}
    <button className="provider-onboarding-card provider-onboarding-custom" onClick={onCustom} type="button"><Plus size={22} /><span className="provider-onboarding-card-copy"><strong>{tx("自定义供应商")}</strong><small>{tx("填写名称和 Base URL")}</small></span></button>
  </section>;
}
