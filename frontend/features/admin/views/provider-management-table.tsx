import { MoreHorizontal, Server } from "lucide-react";
import type { AdminUser, AppData, Provider, ResourceAction, ResourceConfig } from "../core/types";
import { accountProviderCatalogOptionsFromPlugins } from "../domain/provider-account-catalog";
import { providerDisplayBaseURL, providerDisplayName, providerDisplayType } from "../domain/entities";
import { providerTypeLabelFromData } from "../domain/labels";
import { isProviderAccountResourceForData } from "../domain/provider-resource-types";
import { countWithUnit, tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";
import type { ProviderMonitorRow } from "./crud-projects";

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
          const health = snapshot?.state ?? (row.observed24h ? row.statusTone : "unknown");
          const healthLabel = { healthy: "正常", degraded: "降级", down: "故障", unknown: "待观测" }[health];
          const account = accountTypes.has(provider.type) || row.resources.some((resource) => isProviderAccountResourceForData(data, resource));
          const importedCount = data.providerModels.filter((model) => model.provider_id === provider.id).length;
          const actions = (config.actions ?? []).filter((action) => action.visible?.(provider, currentUser, data) ?? true);
          const primaryActions = actions.filter((action) => !action.navigate);
          const secondaryActions = actions.filter((action) => action.navigate);
          const canEdit = Boolean(config.update && (config.canUpdate?.(provider, currentUser, data) ?? true));
          const canDelete = Boolean(config.remove && (config.canRemove?.(provider, currentUser, data) ?? true));
          return (
            <tr key={provider.id}>
              <td><div className="provider-management-name"><Server size={20} aria-hidden="true" /><div><strong>{name}</strong><span>{providerTypeLabelFromData(data, providerDisplayType(provider, row.resources))} · {providerDisplayBaseURL(provider, row.resources)}</span></div></div></td>
              <td data-label={tx("接入方式")}>{tx(account ? "账号接入" : "API 接入")}</td>
              <td data-label={tx("已引入模型")}>{countWithUnit(importedCount, "个模型", "model", "モデル", "models")}</td>
              <td data-label={tx("状态")}><div className="provider-management-status"><StatusPill status={provider.status} /><span className={`provider-monitor-status ${health}`} title={snapshot ? row.statusDetail : undefined}><i />{tx(healthLabel)}</span></div></td>
              <td><div className="provider-management-actions">
                {primaryActions.map((action) => <button className="text-button" key={action.label} onClick={() => onAction(action, provider)} title={tx(action.title ?? action.label)} type="button">{tx(action.label)}</button>)}
                {canEdit ? <button className="text-button" onClick={() => onEdit(provider)} type="button">{tx("编辑")}</button> : null}
                {secondaryActions.length > 0 || canDelete ? <details className="provider-management-more"><summary aria-label={`${tx("更多操作")}: ${name}`}><MoreHorizontal size={17} /><span>{tx("更多")}</span></summary><div>
                  {secondaryActions.map((action) => <button className="text-button" key={action.label} onClick={() => onAction(action, provider)} type="button">{tx(action.label)}</button>)}
                  {canDelete ? <button className="text-button danger" onClick={() => onDelete(provider)} type="button">{tx("删除")}</button> : null}
                </div></details> : null}
              </div></td>
            </tr>
          );
        })}</tbody>
      </table>
    </div>
  );
}
