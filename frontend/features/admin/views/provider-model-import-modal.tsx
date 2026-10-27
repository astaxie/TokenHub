"use client";

import { AlertCircle, X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { flushSync } from "react-dom";
import { type ApiContext, type AppData } from "../core/types";
import { providerImportCandidates, providerImportMetadata, providerImportValues, validImportPrice, validProviderImportDraft, type ProviderImportDraft } from "../domain/provider-model-import";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, modelPayload, readAdminError } from "../resources/payloads";
import { useModalFocus } from "../shared/modal-focus";
import { ProviderImportModelRow } from "./provider-model-import-row";

type ImportResult = { status: "success" | "error"; message?: string };
const blankDraft = (): ProviderImportDraft => ({ input: "", output: "", searchUnit: "" });

export function ProviderModelImportModal({ api, data, onReload, onClose, onOpenProviders }: {
  api: ApiContext;
  data: AppData;
  onReload: () => Promise<void> | void;
  onClose: () => void;
  onOpenProviders: () => void;
}) {
  const providers = data.providers.filter((provider) => provider.status === "active");
  const [providerID, setProviderID] = useState(providers.length === 1 ? providers[0].id : "");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [drafts, setDrafts] = useState<Record<string, ProviderImportDraft>>({});
  const [results, setResults] = useState<Record<string, ImportResult>>({});
  const [bulk, setBulk] = useState(blankDraft);
  const [confirmZeroCost, setConfirmZeroCost] = useState(false);
  const [validationAttempted, setValidationAttempted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const confirmedCosts = useRef(new Set<string>());
  const pendingSubmit = useRef(false);
  const confirmation = useRef<HTMLInputElement>(null);
  const validationID = useId();
  const focus = useModalFocus<HTMLFormElement>(busy ? undefined : onClose);
  const candidates = providerImportCandidates(data, providerID);
  const visible = candidates.filter(({ model }) => [model.upstream_model, model.display_name, model.family].join(" ").toLowerCase().includes(query.trim().toLowerCase()));
  const selectable = visible.filter((item) => !item.collision && (!item.costRequired || item.canConfirmZeroCost) && results[item.model.id]?.status !== "success");
  const pending = candidates.filter((item) => selected.includes(item.model.id) && results[item.model.id]?.status !== "success");
  const zeroCostPending = pending.filter((item) => item.canConfirmZeroCost && !confirmedCosts.current.has(item.model.id));
  const allVisibleSelected = selectable.length > 0 && selectable.every((item) => selected.includes(item.model.id));
  const blocked = pending.some((item) => item.collision || item.costRequired && !item.canConfirmZeroCost);
  const invalidPrices = pending.filter((item) => !validProviderImportDraft(drafts[item.model.id], item.model.modality));
  const successCount = Object.values(results).filter((result) => result.status === "success").length;
  const number = (value: number) => new Intl.NumberFormat(languageLocale()).format(value);
  const validation = blocked ? tx("部分所选模型已不可引入，请取消选择后重试。") : invalidPrices.length
    ? formatTranslationTemplate(tx("还有 {count} 个所选模型的对外价格未填完整或无效。"), { count: number(invalidPrices.length) })
    : zeroCostPending.length && !confirmZeroCost ? tx("请确认所选订阅模型的按量成本。") : "";
  const confirmationInvalid = validationAttempted && !blocked && !invalidPrices.length && zeroCostPending.length > 0 && !confirmZeroCost;
  const validationMessage = validationAttempted && validation
    ? formatTranslationTemplate(tx("尚未引入：{reason}"), { reason: !blocked && !invalidPrices.length && confirmationInvalid ? tx("请勾选上方的订阅成本确认，再次点击“引入所选模型”。") : validation })
    : validation;

  function toggle(id: string) {
    setValidationAttempted(false);
    setConfirmZeroCost(false);
    setSelected((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
    setDrafts((current) => ({ ...current, [id]: current[id] ?? blankDraft() }));
  }

  function selectAll() {
    setValidationAttempted(false);
    setConfirmZeroCost(false);
    const ids = selectable.map((item) => item.model.id);
    setSelected((current) => allVisibleSelected ? current.filter((id) => !ids.includes(id)) : Array.from(new Set([...current, ...ids])));
    setDrafts((current) => ({ ...Object.fromEntries(ids.map((id) => [id, blankDraft()])), ...current }));
  }

  function applyBulk() {
    if (!validImportPrice(bulk.input) || !validImportPrice(bulk.output)) return;
    setValidationAttempted(false);
    setDrafts((current) => ({ ...current, ...Object.fromEntries(pending.map(({ model }) => [model.id, { ...(current[model.id] ?? blankDraft()), input: bulk.input, output: bulk.output }])) }));
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!pending.length || pendingSubmit.current) return;
    if (validation) {
      flushSync(() => { setValidationAttempted(true); if (blocked || invalidPrices.length) setQuery(""); });
      if (blocked || invalidPrices.length) {
        focus.ref.current?.querySelector<HTMLInputElement>(blocked
          ? '.model-provider-import-model[data-blocked="true"] input[type="checkbox"]'
          : '.model-provider-import-row-fields input[aria-invalid="true"]:not(:disabled)')?.focus();
      } else confirmation.current?.focus();
      return;
    }
    const invalidField = focus.ref.current?.querySelector<HTMLInputElement>('.model-provider-import-row-fields input:not(:disabled):invalid');
    if (invalidField) {
      invalidField.focus();
      invalidField.reportValidity();
      return;
    }
    pendingSubmit.current = true;
    setValidationAttempted(false);
    setBusy(true);
    setError("");
    setNotice("");
    let succeeded = 0;
    let failed = 0;
    for (const item of pending) {
      const model = item.model;
      try {
        if (item.canConfirmZeroCost && !confirmedCosts.current.has(model.id)) {
          const costResponse = await adminFetch(api, `/api/admin/provider-models/${encodeURIComponent(model.id)}`, { method: "PATCH", body: JSON.stringify({ metadata: { ...model.metadata, pricing_status: "configured" } }) });
          if (!costResponse.ok) throw new Error(await readAdminError(costResponse, tx("确认订阅模型成本")));
          confirmedCosts.current.add(model.id);
        }
        const payload = { ...modelPayload(providerImportValues(model, drafts[model.id]), providerImportMetadata(model, drafts[model.id])), create_only: true };
        const response = await adminFetch(api, "/api/admin/models", { method: "POST", body: JSON.stringify(payload) });
        if (!response.ok) throw new Error(await readAdminError(response, tx("引入对外模型")));
        succeeded += 1;
        setResults((current) => ({ ...current, [model.id]: { status: "success" } }));
      } catch (cause) {
        failed += 1;
        setResults((current) => ({ ...current, [model.id]: { status: "error", message: cause instanceof Error ? cause.message : tx("操作失败") } }));
      }
    }
    setNotice(formatTranslationTemplate(tx("本次已引入 {success} 个模型，失败 {failed} 个；重试仅处理未成功的模型。"), { success: number(succeeded), failed: number(failed) }));
    try { await onReload(); }
    catch { setError(tx("目录刷新失败，已成功引入的模型不会重复提交。请关闭后刷新目录。")); }
    finally { setBusy(false); pendingSubmit.current = false; }
  }

  return <div className="modal-backdrop" role="presentation">
    <form className="modal provider-model-import-modal" role="dialog" aria-modal="true" aria-label={tx("从 Provider 引入")} aria-busy={busy} data-validation-attempted={validationAttempted || undefined} {...focus} noValidate onSubmit={submit}>
      <header className="model-provider-import-header"><div><h2>{tx("从 Provider 引入")}</h2><p>{tx("每个所选上游模型会生成一个同名对外模型和一条独立路由。")}</p></div><button className="icon-button" type="button" aria-label={tx("关闭")} disabled={busy} onClick={onClose}><X size={18} /></button></header>
      <div className="model-provider-import-body">
        <p className="model-provider-import-help">{tx("对外价格由你单独设置，不会复制 Provider 成本。明确填写 0 表示免费；可在引入后编辑缓存和分时价格。")}</p>
        <div className="model-provider-import-controls">
          <label>{tx("选择 Provider")}<select value={providerID} disabled={busy} onChange={(event) => { setProviderID(event.target.value); setSelected([]); setQuery(""); setConfirmZeroCost(false); setValidationAttempted(false); setNotice(""); setError(""); }}><option value="">{tx("请选择 Provider")}</option>{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></label>
          <label>{tx("搜索 Provider 模型")}<input value={query} disabled={busy} onChange={(event) => setQuery(event.target.value)} /></label>
        </div>
        {!providers.length ? <div className="model-provider-import-empty"><strong>{tx("暂无可用的 Provider")}</strong><button className="secondary-button" type="button" onClick={() => { onClose(); onOpenProviders(); }}>{tx("前往 Provider 渠道")}</button></div> : null}
        {providerID && !candidates.length ? <p className="model-provider-import-empty">{tx("此 Provider 暂无已引入且可调用的模型。请先在 Provider 渠道引入模型。")}</p> : null}
        {providerID && candidates.length > 0 ? <>
          <div className="model-provider-import-selection"><label><input type="checkbox" checked={allVisibleSelected} disabled={busy || !selectable.length} onChange={selectAll} />{tx("全选可引入模型")}</label><span>{formatTranslationTemplate(tx("已选择 {selected} 个，已成功 {success} 个"), { selected: number(pending.length), success: number(successCount) })}</span></div>
          <fieldset className="model-provider-import-bulk" disabled={busy || !pending.length}><legend>{tx("批量设置对外价格")}</legend><label>{tx("统一输入价格")}<input type="number" min="0" step="any" value={bulk.input} onChange={(event) => setBulk({ ...bulk, input: event.target.value })} /></label><label>{tx("统一输出价格")}<input type="number" min="0" step="any" value={bulk.output} onChange={(event) => setBulk({ ...bulk, output: event.target.value })} /></label><button className="secondary-button" type="button" disabled={!validImportPrice(bulk.input) || !validImportPrice(bulk.output)} onClick={applyBulk}>{tx("应用到所选模型")}</button><small>{tx("单位：USD / 1M tokens")}</small><small>{tx("填写统一价格后，请点击“应用到所选模型”；也可逐项填写。")}</small></fieldset>
          {!visible.length ? <p className="model-provider-import-empty">{tx("没有匹配的 Provider 模型")}</p> : null}
          <div className="model-provider-import-models">{visible.map((candidate) => <ProviderImportModelRow key={candidate.model.id} candidate={candidate} draft={drafts[candidate.model.id] ?? blankDraft()} selected={selected.includes(candidate.model.id)} busy={busy} result={results[candidate.model.id]} onToggle={() => toggle(candidate.model.id)} onChange={(key, value) => setDrafts((current) => ({ ...current, [candidate.model.id]: { ...(current[candidate.model.id] ?? blankDraft()), [key]: value } }))} />)}</div>
          {candidates.some((item) => item.costRequired && !item.canConfirmZeroCost) ? <button className="text-button" disabled={busy} type="button" onClick={() => { onClose(); onOpenProviders(); }}>{tx("前往 Provider 渠道配置未确认的成本")}</button> : null}
        </> : null}
      </div>
      <footer className="model-provider-import-footer">
        {zeroCostPending.length ? <label className={`model-provider-import-confirm${confirmationInvalid ? " is-invalid" : ""}`}><input ref={confirmation} type="checkbox" checked={confirmZeroCost} disabled={busy} aria-invalid={confirmationInvalid} aria-describedby={confirmationInvalid ? validationID : undefined} onChange={(event) => setConfirmZeroCost(event.target.checked)} />{tx("确认所选订阅模型的按量成本为 0（订阅费用另行核算）")}</label> : null}
        {validation && !busy ? <p key={validationAttempted ? "error" : "hint"} id={validationID} className={`model-provider-import-validation${validationAttempted ? " is-error" : ""}`} role={validationAttempted ? "alert" : undefined} aria-live={validationAttempted ? undefined : "polite"}>{validationAttempted ? <AlertCircle size={18} aria-hidden="true" /> : null}<span>{validationMessage}</span></p> : null}
        {notice ? <p className="inline-notice" role="status">{notice}</p> : null}
        {error ? <p className="inline-notice error" role="alert">{error}</p> : null}
        <div className="model-provider-import-footer-actions"><button className="secondary-button" type="button" disabled={busy} onClick={onClose}>{tx("关闭")}</button><button className="button" type="submit" disabled={busy || !pending.length} aria-describedby={validation && !busy ? validationID : undefined}>{tx(busy ? "正在引入…" : "引入所选模型")}</button></div>
      </footer>
    </form>
  </div>;
}
