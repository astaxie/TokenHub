import { ShieldCheck } from "lucide-react";
import {
  pluginPermissionDiffDisplay,
  type PluginPermissionDiffDisplayState,
  type PluginPermissionDiffPreviewPayload,
} from "../domain/plugin-permission-diff";
import { formatLocaleNumber, formatTranslationTemplate, tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";

export type PluginPermissionDiffPreviewDraft = {
  busy: boolean;
  error: string;
  preview: PluginPermissionDiffPreviewPayload | null;
};

export function emptyPermissionPreviewDraft(): PluginPermissionDiffPreviewDraft {
  return { busy: false, error: "", preview: null };
}

export function PluginPermissionDiffPreview({
  disabled = false,
  draft,
  onPreview,
  showAction = true,
}: {
  disabled?: boolean;
  draft: PluginPermissionDiffPreviewDraft;
  onPreview: () => void;
  showAction?: boolean;
}) {
  const state = draft.preview ? pluginPermissionDiffDisplay(draft.preview) : null;
  return (
    <div className="plugin-permission-diff-preview" data-plugin-permission-diff-preview>
      {showAction ? (
        <button
          className="secondary-button compact-button"
          disabled={disabled || draft.busy}
          onClick={onPreview}
          title={tx("预览权限")}
          type="button"
        >
          <ShieldCheck size={13} />
          <span>{tx(draft.busy ? "预览中" : "预览权限")}</span>
        </button>
      ) : null}
      {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {state ? <PermissionDiffResult state={state} /> : null}
    </div>
  );
}

function PermissionDiffResult({ state }: { state: PluginPermissionDiffDisplayState }) {
  const visibleSections = state.sections.filter((section) => section.count > 0 || section.changes.length > 0);
  const template = (key: string, values: Record<string, string>) => formatTranslationTemplate(tx(key), values);
  return (
    <div className="plugin-permission-diff-result" data-plugin-permission-diff-result>
      <div className="tag-list plugin-permission-diff-status">
        <StatusPill status={statusPillStatus(state.tone)} label={tx(state.verdictLabelKey)} />
        <StatusPill status={statusPillStatus(state.highestSensitivityTone)} label={template("最高敏感度：{level}", { level: tx(state.highestSensitivityLabelKey) })} />
        <StatusPill status={statusPillStatus(state.trust.tone)} label={template("信任状态：{status}", { status: tx(state.trust.labelKey) })} />
        <StatusPill status={statusPillStatus(state.compatibility.tone)} label={template("兼容性：{status}", { status: tx(state.compatibility.labelKey) })} />
      </div>
      <div className="tag-list plugin-permission-diff-summary">
        {state.currentVersion ? <span className="tag">{template("当前版本：{version}", { version: state.currentVersion })}</span> : null}
        {state.candidateVersion ? <span className="tag">{template("候选版本：{version}", { version: state.candidateVersion })}</span> : null}
        <span className="tag">{tx(state.reasonLabelKey)}</span>
        <span className="tag">{template("新增权限：{count}", { count: formatLocaleNumber(state.summary.added) })}</span>
        <span className="tag">{template("移除权限：{count}", { count: formatLocaleNumber(state.summary.removed) })}</span>
        <span className="tag">{template("敏感度变化：{count}", { count: formatLocaleNumber(state.summary.changed_sensitivity) })}</span>
      </div>
      {visibleSections.length > 0 ? (
        <div className="plugin-permission-diff-sections">
          {visibleSections.map((section) => (
            <section className="plugin-permission-diff-section" key={section.id}>
              <div className="tag-list">
                <StatusPill status={statusPillStatus(section.tone)} label={tx(section.labelKey)} />
                <span className="tag">{formatLocaleNumber(section.count)}</span>
              </div>
              <div className="plugin-permission-diff-changes">
                {section.changes.slice(0, 6).map((change) => (
                  <span className="tag" key={`${section.id}:${change.kind}:${change.name}:${change.access}`}>
                    {change.kind}:{change.access}:{change.name} · {tx(change.sensitivityLabelKey)}
                  </span>
                ))}
              </div>
            </section>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function statusPillStatus(tone: string) {
  if (tone === "warn") return "pending";
  return tone;
}
