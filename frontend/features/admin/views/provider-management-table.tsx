import { Boxes } from "lucide-react";
import { useState } from "react";
import type { AdminUser, AppData, Provider, ProviderCatalogEntry, ResourceAction, ResourceConfig } from "../core/types";
import { accountProviderCatalogOptionsFromPlugins } from "../domain/provider-account-catalog";
import { providerDisplayBaseURL, providerDisplayName, providerDisplayType } from "../domain/entities";
import { providerTypeLabelFromData } from "../domain/labels";
import { providerBrandIconSource } from "../domain/provider-brand-icons";
import { isProviderAccountResourceForData } from "../domain/provider-resource-types";
import { countWithUnit, tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";
import type { ProviderMonitorRow } from "./crud-projects";
import { ProviderManagementActions, type ProviderManagementAction } from "./provider-management-actions";

function ProviderManagementIcon({ provider, data, label }: { provider: Provider; data: AppData; label: string }) {
  const [failedSource, setFailedSource] = useState("");
  const catalogID = provider.options?.catalog_id?.trim();
  const entry: ProviderCatalogEntry = data.providerCatalog.find((item) => item.id === catalogID)
    ?? { id: catalogID || provider.type, name: provider.name, display_name: label, type: provider.type, source: "provider", models_count: 0 };
  const source = providerBrandIconSource(entry);
  const hasIcon = Boolean(source && source !== failedSource);
  return <span aria-hidden="true" className={`provider-management-icon${hasIcon ? "" : " fallback"}`} title={label}>
    {hasIcon ? <img alt="" src={source} onError={() => setFailedSource(source)} /> : <Boxes size={20} />}
  </span>;
}

export function ProviderManagementTable({ rows, data, config, currentUser, onAction, onEdit, onDelete }: {
  rows: ProviderMonitorRow[];
  data: AppData;
  config: ResourceConfig<Provider>;
  currentUser: AdminUser | null;
  onAction: (action: ResourceAction<Provider>, provider: Provider) => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
}) {
  const accountTypes = new Set(accountProviderCatalogOptionsFromPlugins(data.providerCatalog, data.plugins, data.providerAdapters).map((entry) => entry.type));
  return (
    <div className="provider-management-table-wrap">
      <table className="provider-management-table">
        <thead><tr><th>{tx("供应商")}</th><th>{tx("接入方式")}</th><th>{tx("已引入模型")}</th><th>{tx("状态")}</th><th>{tx("操作")}</th></tr></thead>
        <tbody>{rows.map((row) => {
          const provider = row.provider;
          const name = providerDisplayName(provider, row.resources);
          const snapshot = data.providerMonitoring.find((item) => item.provider.id === provider.id);
          const health = row.statusTone;
          const healthLabel = { healthy: "正常", degraded: "降级", down: "故障", unknown: "待观测" }[health];
          const account = accountTypes.has(provider.type) || row.resources.some((resource) => isProviderAccountResourceForData(data, resource));
          const importedCount = data.providerModels.filter((model) => model.provider_id === provider.id).length;
          const actions = (config.actions ?? []).filter((action) => action.visible?.(provider, currentUser, data) ?? true);
          const primaryActions = actions.filter((action) => !action.navigate);
          const secondaryActions = actions.filter((action) => action.navigate);
          const canEdit = Boolean(config.update && (config.canUpdate?.(provider, currentUser, data) ?? true));
          const canDelete = Boolean(config.remove && (config.canRemove?.(provider, currentUser, data) ?? true));
          const actionItem = (action: ResourceAction<Provider>): ProviderManagementAction => ({ id: action.label, label: tx(action.label), title: tx(action.title ?? action.label), onClick: () => onAction(action, provider) });
          const rowActions: ProviderManagementAction[] = [
            ...primaryActions.map(actionItem),
            ...(canEdit ? [{ id: "edit", label: tx("编辑"), onClick: () => onEdit(provider) }] : []),
            ...secondaryActions.map(actionItem),
            ...(canDelete ? [{ id: "delete", label: tx("删除"), danger: true, onClick: () => onDelete(provider) }] : []),
          ];
          return (
            <tr key={provider.id}>
              <td><div className="provider-management-name"><ProviderManagementIcon data={data} label={name} provider={provider} /><div><strong>{name}</strong><span>{providerTypeLabelFromData(data, providerDisplayType(provider, row.resources))} · {providerDisplayBaseURL(provider, row.resources)}</span></div></div></td>
              <td data-label={tx("接入方式")}>{tx(account ? "账号接入" : "API 接入")}</td>
              <td data-label={tx("已引入模型")}>{countWithUnit(importedCount, "个模型", "model", "モデル", "models")}</td>
              <td data-label={tx("状态")}><div className="provider-management-status"><StatusPill status={provider.status} /><span className={`provider-monitor-status ${health}`} title={snapshot ? row.statusDetail : undefined}><i />{tx(healthLabel)}</span></div></td>
              <td><ProviderManagementActions actions={rowActions} name={name} /></td>
            </tr>
          );
        })}</tbody>
      </table>
    </div>
  );
}
