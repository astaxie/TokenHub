import { useState } from "react";
import { Boxes, Check, Plus, Search } from "lucide-react";
import type { AdminUIContribution, Provider, ProviderCatalogEntry, ProviderCredentialMode } from "../core/types";
import { providerBrandIconSource } from "../domain/provider-brand-icons";
import { tx } from "../i18n/runtime";

function ProviderBrandIcon({ entry, label }: { entry: ProviderCatalogEntry; label: string }) {
  const source = providerBrandIconSource(entry);
  const [failed, setFailed] = useState(false);
  const className = `provider-onboarding-card-icon${source ? "" : " fallback"}`;
  return <span aria-hidden="true" className={className} title={label}>
    {source && !failed ? <img alt="" src={source} onError={() => setFailed(true)} /> : <Boxes size={22} />}
  </span>;
}

export function ProviderOnboardingCatalog({ directEntries, accountEntries, providers, query, onQueryChange, onSelect, onCustom, contributions }: {
  directEntries: ProviderCatalogEntry[];
  accountEntries: ProviderCatalogEntry[];
  providers: Provider[];
  query: string;
  onQueryChange: (value: string) => void;
  onSelect: (entry: ProviderCatalogEntry, mode: ProviderCredentialMode) => void;
  onCustom: () => void;
  contributions: AdminUIContribution[];
}) {
  const normalized = query.trim().toLowerCase();
  const groups: Array<{ title: string; mode: ProviderCredentialMode; entries: ProviderCatalogEntry[] }> = [
    { title: "供应商 API", mode: "provider_api_key", entries: directEntries.filter(entry => entry.id !== "custom") },
    { title: "订阅与账号", mode: "account_integration", entries: accountEntries },
  ];
  function cardDetails(entry: ProviderCatalogEntry) {
    const contribution = contributions.find(item => item.slot === "provider.catalog.card" && item.provider_types?.includes(entry.type));
    const description = contribution?.schema?.description;
    return { title: contribution?.title || entry.display_name || entry.name, description: typeof description === "string" ? description : entry.base_url || "" };
  }
  const filteredGroups = groups.map(group => ({ ...group, entries: group.entries.filter(entry => {
    const details = cardDetails(entry);
    return !normalized || [entry.id, entry.name, entry.display_name, entry.base_url, details.title, details.description].join(" ").toLowerCase().includes(normalized);
  }) }));
  return <section className="provider-onboarding-catalog" aria-label={tx("选择供应商")}>
    <p className="provider-onboarding-intro">{tx("选择你要接入的服务，下一步填写密钥或登录账号。")}</p>
    <label className="search-box provider-onboarding-search"><Search size={16} /><input autoFocus value={query} onChange={event => onQueryChange(event.target.value)} placeholder={tx("搜索供应商名称或地址")} /></label>
    {filteredGroups.map(group => group.entries.length > 0 ? <section className="provider-onboarding-group" key={group.mode}>
      <h3>{tx(group.title)}</h3>
      <div className="provider-onboarding-grid">{group.entries.map(entry => {
        const details = cardDetails(entry);
        const connected = providers.some(provider => provider.options?.catalog_id === entry.id || (!provider.options?.catalog_id && provider.type === entry.type && provider.base_url === entry.base_url));
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
