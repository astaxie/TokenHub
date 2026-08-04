"use client";

import { Save } from "lucide-react";
import { useEffect, useState } from "react";
import { type ApiContext, type ProviderModel } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";

type CostDraft = {
  input: string;
  cache: string;
  output: string;
};

function costDraft(model: ProviderModel): CostDraft {
  return {
    input: String(model.input_price_usd_per_1m ?? 0),
    cache: String(model.cache_read_price_usd_per_1m ?? 0),
    output: String(model.output_price_usd_per_1m ?? 0),
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

  useEffect(() => {
    setDrafts(Object.fromEntries(models.map((model) => [model.id, costDraft(model)])));
  }, [models]);

  function update(modelID: string, key: keyof CostDraft, value: string) {
    setDrafts((current) => ({ ...current, [modelID]: { ...current[modelID], [key]: value } }));
  }

  async function save(model: ProviderModel) {
    const draft = drafts[model.id] ?? costDraft(model);
    const costs = {
      input: draft.input.trim() === "" ? 0 : Number(draft.input),
      cache: draft.cache.trim() === "" ? 0 : Number(draft.cache),
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
      const resp = await adminFetch(api, `/api/admin/provider-models/${encodeURIComponent(model.id)}`, {
        method: "PATCH",
        body: JSON.stringify({
          input_price_usd_per_1m: costs.input,
          cache_read_price_usd_per_1m: costs.cache,
          output_price_usd_per_1m: costs.output,
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("保存渠道成本价")));
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
          <span>{tx("渠道成本价用于请求审计和真实成本核算，不会改变模型目录中的对外统一价。")}</span>
        </div>
        <em>{models.length}</em>
      </div>
      {notice ? <p className="provider-inventory-notice success">{notice}</p> : null}
      {error ? <p className="provider-inventory-notice error">{error}</p> : null}
      <div className="provider-model-inventory-table-wrap">
        <table className="provider-model-inventory-table">
          <thead>
            <tr>
              <th>{tx("上游模型")}</th>
              <th>{tx("输入成本 USD/1M")}</th>
              <th>{tx("缓存读成本 USD/1M")}</th>
              <th>{tx("输出成本 USD/1M")}</th>
              <th>{tx("状态")}</th>
              <th>{tx("操作")}</th>
            </tr>
          </thead>
          <tbody>
            {models.map((model) => {
              const draft = drafts[model.id] ?? costDraft(model);
              return (
                <tr key={model.id}>
                  <td><strong>{model.display_name || model.upstream_model}</strong><span>{model.upstream_model}</span></td>
                  {(["input", "cache", "output"] as const).map((key) => (
                    <td key={key}>
                      <input min="0" onChange={(event) => update(model.id, key, event.target.value)} step="0.000001" type="number" value={draft[key]} />
                    </td>
                  ))}
                  <td><StatusPill status={model.status} /></td>
                  <td>
                    <button className="text-button provider-cost-save" disabled={savingID === model.id} onClick={() => void save(model)} type="button">
                      <Save size={14} />{tx(savingID === model.id ? "保存中" : "保存成本")}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
