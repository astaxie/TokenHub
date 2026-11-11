"use client";

import { ModelCatalogNotices } from "./model-catalog-notices";
import { StatementLauncher } from "./billing-statements";
import { PricingPeriodsEditor } from "./pricing-periods-editor";
import { Clock3, Save, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { type ApiContext, type ProviderModel } from "../core/types";
import { configuredPriceFormValue } from "../domain/configured-pricing";
import { pricingPeriodDrafts, serializePricingPeriodDrafts, type PricingPeriodDraft } from "../domain/pricing-period-draft";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";

type CostDraft = {
  dirty: boolean;
  searchUnit: string;
  input: string;
  cache: string;
  cacheWrite: string;
  cacheWrite5m: string;
  cacheWrite1h: string;
  output: string;
  periods: PricingPeriodDraft[];
};

function costDraft(model: ProviderModel): CostDraft {
  return {
    dirty: false,
    searchUnit: model.metadata?.search_unit_price_usd ?? "",
    input: String(model.input_price_usd_per_1m ?? 0),
    cache: String(model.cache_read_price_usd_per_1m ?? 0),
    cacheWrite: configuredPriceFormValue(model.cache_write_price_usd_per_1m, model.cache_write_price_configured),
    cacheWrite5m: configuredPriceFormValue(model.cache_write_5m_price_usd_per_1m, model.cache_write_5m_price_configured),
    cacheWrite1h: configuredPriceFormValue(model.cache_write_1h_price_usd_per_1m, model.cache_write_1h_price_configured),
    output: String(model.output_price_usd_per_1m ?? 0),
    periods: pricingPeriodDrafts(model.pricing_periods),
  };
}

export function ProviderModelInventory({
  api,
  models,
  onSaved,
}: {
  api: ApiContext;
  models: ProviderModel[];
  onSaved?: () => Promise<void> | void;
}) {
  const [drafts, setDrafts] = useState<Record<string, CostDraft>>({});
  const [savingID, setSavingID] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [scheduleID, setScheduleID] = useState("");
  const scheduleRef = useRef<HTMLElement>(null);
  const scheduleModel = models.find(model => model.id === scheduleID);

  useEffect(() => {
    setDrafts(current => Object.fromEntries(models.map(model => [model.id, current[model.id]?.dirty ? current[model.id] : costDraft(model)])));
  }, [models]);

  function update<K extends Exclude<keyof CostDraft, "dirty">>(modelID: string, key: K, value: CostDraft[K]) {
    setDrafts((current) => ({ ...current, [modelID]: { ...current[modelID], [key]: value, dirty: true } }));
  }

  async function save(model: ProviderModel) {
    const draft = drafts[model.id] ?? costDraft(model);
    const costs = {
      input: draft.input.trim() === "" ? 0 : Number(draft.input),
      cache: draft.cache.trim() === "" ? 0 : Number(draft.cache),
      cacheWrite: draft.cacheWrite.trim() === "" ? 0 : Number(draft.cacheWrite),
      cacheWrite5m: draft.cacheWrite5m.trim() === "" ? 0 : Number(draft.cacheWrite5m),
      cacheWrite1h: draft.cacheWrite1h.trim() === "" ? 0 : Number(draft.cacheWrite1h),
      output: draft.output.trim() === "" ? 0 : Number(draft.output),
    };
    if (Object.values(costs).some((cost) => !Number.isFinite(cost) || cost < 0)) {
      setNotice("");
      setError(tx("每项渠道成本价必须是有限的非负数字。"));
      return;
    }
    setSavingID(model.id);
    setError("");
    setNotice("");
    try {
      let periods;
      try {
        periods = serializePricingPeriodDrafts(draft.periods);
      } catch {
        if (draft.periods.some(period => period.invalidFieldKeys?.length)) {
          setScheduleID(model.id);
          requestAnimationFrame(() => scheduleRef.current?.querySelector<HTMLInputElement>('input[aria-invalid="true"]')?.focus());
        }
        throw new Error(tx("分时价格无效：请检查时区、时间、生效日期和非负价格，最多 64 个时段。"));
      }
      const resp = await adminFetch(api, `/api/admin/provider-models/${encodeURIComponent(model.id)}`, {
        method: "PATCH",
        body: JSON.stringify({
          metadata: { ...model.metadata, pricing_status: "configured", ...(model.modality === "rerank" || model.modality === "embedding" ? { retrieval_pricing_confirmed: "true", search_unit_price_usd: draft.searchUnit.trim() } : {}) },
          input_price_usd_per_1m: costs.input,
          cache_read_price_usd_per_1m: costs.cache,
          cache_write_price_usd_per_1m: costs.cacheWrite,
          cache_write_price_configured: draft.cacheWrite.trim() !== "",
          cache_write_5m_price_usd_per_1m: costs.cacheWrite5m,
          cache_write_5m_price_configured: draft.cacheWrite5m.trim() !== "",
          cache_write_1h_price_usd_per_1m: costs.cacheWrite1h,
          cache_write_1h_price_configured: draft.cacheWrite1h.trim() !== "",
          output_price_usd_per_1m: costs.output,
          pricing_periods: periods,
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("保存渠道成本价")));
      // A later edit creates a different draft and must survive this save's refresh.
      setDrafts(current => current[model.id] === draft ? { ...current, [model.id]: { ...draft, dirty: false } } : current);
      setNotice(`${model.upstream_model} · ${tx("渠道成本价已保存")}`);
      await onSaved?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("保存渠道成本价失败"));
    } finally {
      setSavingID("");
    }
  }

  if (models.length === 0) {
    return (
      <div className="provider-inventory-empty">
        <strong>{tx("尚未引入上游模型")}</strong>
        <span>{tx("请从下方模型列表选择要加入此 Provider 的模型。")}</span>
      </div>
    );
  }

  return (
    <section className="provider-model-inventory">
      <div className="provider-model-inventory-head">
        <div>
          <strong>{tx("已引入模型与渠道成本")}</strong>
          <span>{tx("渠道成本价用于真实成本核算；选择跟随此上游模型的对外价格也会随之更新。")}</span>
          <span>{tx("保存成本会确认当前填写的价格，包括 0；未确认的零值不代表免费。")}</span>
        </div>
        <em>{models.length}</em>
      </div>
      {notice ? <p className="provider-inventory-notice success">{notice}</p> : null}
      {error ? <p className="provider-inventory-notice error" role="alert">{error}</p> : null}
      {scheduleModel ? <section ref={scheduleRef} className="provider-pricing-schedule" aria-label={tx("分时成本设置")}>
        <div className="provider-pricing-schedule-head"><div><span>{tx("分时成本设置")}</span><strong>{scheduleModel.display_name || scheduleModel.upstream_model}</strong></div><button className="icon-button" type="button" aria-label={tx("收起分时设置")} onClick={() => setScheduleID("")}><X size={16} /></button></div>
        <PricingPeriodsEditor key={scheduleModel.id} value={(drafts[scheduleModel.id] ?? costDraft(scheduleModel)).periods} disabled={savingID !== ""} onChange={periods => update(scheduleModel.id, "periods", periods)} />
        <div className="provider-pricing-schedule-actions"><span>{tx("保存会同时更新基础成本与分时价格。")}</span><button className="button" type="button" disabled={savingID !== ""} onClick={() => void save(scheduleModel)}><Save size={14} />{tx(savingID === scheduleModel.id ? "保存中" : "保存成本与时段")}</button></div>
      </section> : null}
      <div className="retrieval-cost-list">
        {models.filter((model) => model.modality === "embedding" || model.modality === "rerank").map((model) => {
          const draft = drafts[model.id] ?? costDraft(model);
          return <div className="retrieval-cost-row" key={model.id}>
            <div className="retrieval-cost-identity"><strong>{model.display_name || model.upstream_model}</strong>{model.display_name && model.display_name !== model.upstream_model ? <span>{model.upstream_model}</span> : null}<span className="retrieval-cost-kind">{model.modality} <StatusPill status={model.status} /></span><ModelCatalogNotices metadata={model.metadata} />{model.call_supported === false ? <span>{tx("当前渠道暂不支持此模型调用")}</span> : null}{model.modality === "embedding" && (model.input_price_usd_per_1m ?? 0) === 0 ? <span>{tx(model.metadata?.retrieval_pricing_confirmed === "true" ? "输入成本已确认免费" : "输入成本尚未确认（不阻断已有线路）")}</span> : null}</div>
            <div className="retrieval-cost-fields">
              <label><span>{tx("输入成本 USD/1M")}</span><input aria-label={`${tx("输入成本 USD/1M")}: ${model.upstream_model}`} min="0" step="0.000001" type="number" value={draft.input} onChange={(event) => update(model.id, "input", event.target.value)} /></label>
              {model.modality === "rerank" ? <label><span>{tx("搜索单元价格 USD/次")}</span><input aria-label={`${tx("搜索单元价格 USD/次")}: ${model.upstream_model}`} type="number" min="0" step="0.000001" value={draft.searchUnit} onChange={(event) => update(model.id, "searchUnit", event.target.value)} /></label> : null}
            </div>
            <div className="retrieval-cost-actions"><button className="text-button provider-cost-save" disabled={savingID === model.id} onClick={() => void save(model)} type="button"><Save size={14} />{tx(savingID === model.id ? "保存中" : "保存成本")}</button><StatementLauncher api={api} side="provider" providerID={model.provider_id} model={model.upstream_model} /></div>
          </div>;
        })}
      </div>
      {models.some((model) => model.modality !== "embedding" && model.modality !== "rerank") ? (
      <div className="provider-model-inventory-table-wrap">
        <table className="provider-model-inventory-table">
          <thead>
            <tr>
              <th>{tx("上游模型")}</th>
              <th>{tx("输入成本 USD/1M")}</th>
              <th>{tx("缓存读成本 USD/1M")}</th>
              <th>{tx("缓存写成本 USD/1M")}</th>
              <th>{tx("5 分钟缓存写成本 USD/1M")}</th>
              <th>{tx("1 小时缓存写成本 USD/1M")}</th>
              <th>{tx("输出成本 USD/1M")}</th>
              <th>{tx("状态")}</th>
              <th>{tx("操作")}</th>
            </tr>
          </thead>
          <tbody>
            {models.filter((model) => model.modality !== "embedding" && model.modality !== "rerank").map((model) => {
              const draft = drafts[model.id] ?? costDraft(model);
              return (
                <tr key={model.id}>
                  <td><strong>{model.display_name || model.upstream_model}</strong><span>{model.upstream_model}</span><ModelCatalogNotices metadata={model.metadata} />{model.call_supported === false ? <span>{tx("当前渠道暂不支持此模型调用")}</span> : null}
                    <button className="text-button provider-pricing-schedule-toggle" type="button" aria-expanded={scheduleID === model.id} onClick={() => setScheduleID(scheduleID === model.id ? "" : model.id)}><Clock3 size={13} />{tx("分时价格")}<span>{new Intl.NumberFormat(languageLocale()).format(draft.periods.length)}</span></button>
                  </td>
                  {(["input", "cache", "cacheWrite", "cacheWrite5m", "cacheWrite1h", "output"] as const).map((key) => (
                    <td key={key}>
                      <input min="0" onChange={(event) => update(model.id, key, event.target.value)} step="0.000001" type="number" value={draft[key]} />
                    </td>
                  ))}
                  <td><StatusPill status={model.status} /></td>
                  <td>
                    <button className="text-button provider-cost-save" disabled={savingID === model.id} onClick={() => void save(model)} type="button">
                      <Save size={14} />{tx(savingID === model.id ? "保存中" : "保存成本")}
                    </button>
                    <StatementLauncher api={api} side="provider" providerID={model.provider_id} model={model.upstream_model} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      ) : null}
    </section>
  );
}
