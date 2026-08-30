import { Power, PowerOff, RotateCcw, Trash2 } from "lucide-react";
import { type PluginDescriptor } from "../core/types";
import { pluginManagerDisplayState, type PluginManagerDisplayState } from "../domain/plugin-manager";
import { tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";

export type PluginStateDraft = {
  status?: string;
  busy?: boolean;
  error?: string;
  restartRequired?: boolean;
};

export type PluginDeleteDraft = {
  busy: boolean;
  error: string;
  result: string;
};

export type PluginRollbackDraft = {
  busy: boolean;
  error: string;
  result: string;
};

export function PluginLifecycleControl({
  plugin,
  lifecycle,
  draft,
  rollbackDraft,
  onRollback,
  onUpdate,
}: {
  plugin: PluginDescriptor;
  lifecycle: PluginManagerDisplayState;
  draft: PluginStateDraft;
  rollbackDraft: PluginRollbackDraft;
  onRollback: (plugin: PluginDescriptor) => void;
  onUpdate: (plugin: PluginDescriptor, status: string) => void;
}) {
  const effectiveLifecycle = draft.status ? pluginManagerDisplayState({ plugin: { ...plugin, status: draft.status } }) : lifecycle;
  const status = effectiveLifecycle.status;
  const nextStatus = status === "disabled" ? "enabled" : "disabled";
  const canUpdate = plugin.source !== "built_in" && !effectiveLifecycle.mandatory && (status === "enabled" || status === "disabled");
  return (
    <div className="stacked-cell" data-plugin-manager-control="lifecycle">
      <StatusPill status={effectiveLifecycle.pillStatus} label={tx(effectiveLifecycle.labelKey)} />
      {effectiveLifecycle.rollbackTarget ? <span>{tx("回滚目标")} {tx(effectiveLifecycle.rollbackTargetLabelKey)}</span> : null}
      {effectiveLifecycle.rollbackAvailable && effectiveLifecycle.rollbackVersion ? <span>{tx("回滚版本")} {effectiveLifecycle.rollbackVersion}</span> : null}
      {canUpdate ? (
        <button
          className="secondary-button compact-button"
          disabled={Boolean(draft.busy)}
          onClick={() => onUpdate(plugin, nextStatus)}
          title={tx(nextStatus === "enabled" ? "启用插件" : "禁用插件")}
          type="button"
        >
          {nextStatus === "enabled" ? <Power size={14} /> : <PowerOff size={14} />}
          <span>{tx(draft.busy ? "更新中" : nextStatus === "enabled" ? "启用" : "禁用")}</span>
        </button>
      ) : null}
      {effectiveLifecycle.actions.rollback.available ? (
        <button
          className="secondary-button compact-button"
          disabled={rollbackDraft.busy}
          onClick={() => onRollback(plugin)}
          title={tx("回滚插件")}
          type="button"
        >
          <RotateCcw size={14} />
          <span>{tx(rollbackDraft.busy ? "回滚中" : "回滚")}</span>
        </button>
      ) : null}
      {draft.restartRequired || effectiveLifecycle.restartRequired ? <span>{tx("重启后生效")}</span> : null}
      {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {rollbackDraft.error ? <span className="provider-quota-error">{rollbackDraft.error}</span> : null}
      {rollbackDraft.result ? <span>{rollbackDraft.result}</span> : null}
    </div>
  );
}

export function PluginDeleteControl({
  plugin,
  lifecycle,
  draft,
  onDelete,
}: {
  plugin: PluginDescriptor;
  lifecycle: PluginManagerDisplayState;
  draft: PluginDeleteDraft;
  onDelete: (plugin: PluginDescriptor) => void;
}) {
  if (!lifecycle.actions.uninstall.available) {
    return (
      <div className="stacked-cell" data-plugin-manager-control="delete">
        <span className="muted">-</span>
      </div>
    );
  }
  return (
    <div className="stacked-cell" data-plugin-manager-control="delete">
      <button
        className="danger-button compact-button"
        disabled={draft.busy}
        onClick={() => onDelete(plugin)}
        title={tx("卸载插件")}
        type="button"
      >
        <Trash2 size={13} />
        <span>{tx(draft.busy ? "卸载中" : "卸载")}</span>
      </button>
      {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {draft.result ? <span>{draft.result}</span> : null}
    </div>
  );
}
