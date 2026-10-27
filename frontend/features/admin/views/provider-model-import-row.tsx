import { validImportPrice, type ProviderImportCandidate, type ProviderImportDraft } from "../domain/provider-model-import";
import { tx } from "../i18n/runtime";

export function ProviderImportModelRow({ candidate, draft, selected, busy, result, onToggle, onChange }: {
  candidate: ProviderImportCandidate;
  draft: ProviderImportDraft;
  selected: boolean;
  busy: boolean;
  result?: { status: "success" | "error"; message?: string };
  onToggle: () => void;
  onChange: (key: keyof ProviderImportDraft, value: string) => void;
}) {
  const { model, collision, costRequired, canConfirmZeroCost } = candidate;
  const succeeded = result?.status === "success";
  const blocked = collision || costRequired && !canConfirmZeroCost;
  return <section className={`model-provider-import-model${succeeded ? " is-success" : ""}`} aria-label={model.upstream_model} data-blocked={blocked && selected && !succeeded || undefined}>
    <div className="model-provider-import-model-head"><label><input type="checkbox" checked={selected} disabled={busy || blocked && !selected || succeeded} onChange={onToggle} /><strong>{model.upstream_model}</strong></label><span>{model.modality || "chat"}</span></div>
    {model.display_name && model.display_name !== model.upstream_model ? <p>{model.display_name}</p> : null}
    {succeeded ? <p className="model-provider-import-success">{tx("已引入，重试时自动跳过")}</p> : collision ? <p>{tx("同名对外模型已存在，不会覆盖现有价格和路由。")}</p> : costRequired && !canConfirmZeroCost ? <p>{tx("请先在 Provider 渠道确认此模型的成本。")}</p> : null}
    {selected ? <fieldset className="model-provider-import-row-fields" disabled={busy || succeeded || blocked}>
      <label className="model-provider-import-model-id">{tx("对外模型 ID")}<input readOnly value={model.upstream_model} /></label>
      <label>{tx("输入价格（USD / 1M tokens）")}<input type="number" min="0" step="any" required aria-invalid={!validImportPrice(draft.input)} value={draft.input} onChange={(event) => onChange("input", event.target.value)} /></label>
      <label>{tx("输出价格（USD / 1M tokens）")}<input type="number" min="0" step="any" required aria-invalid={!validImportPrice(draft.output)} value={draft.output} onChange={(event) => onChange("output", event.target.value)} /></label>
      {model.modality === "embedding" ? <small>{tx("此输入价格同时用作对外 Embedding 价格。")}</small> : null}
      {model.modality === "audio" ? <label>{tx("音频价格（USD / 秒）")}<input type="number" min="0" step="any" required aria-invalid={!validImportPrice(draft.audioSecond ?? "")} value={draft.audioSecond ?? ""} onChange={(event) => onChange("audioSecond", event.target.value)} /><small>{tx("按音频时长计费时使用；请单独填写，不会复制 Provider 成本。")}</small></label> : null}
      {model.modality === "rerank" ? <label>{tx("搜索单元价格 USD/次")}<input type="number" min="0" step="any" aria-invalid={Boolean(draft.searchUnit.trim()) && !validImportPrice(draft.searchUnit)} value={draft.searchUnit} onChange={(event) => onChange("searchUnit", event.target.value)} /></label> : null}
    </fieldset> : null}
    {result?.status === "error" ? <p className="inline-notice error" role="alert">{result.message}</p> : null}
  </section>;
}
