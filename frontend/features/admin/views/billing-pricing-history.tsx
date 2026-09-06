import { useEffect, useState } from "react";
import { RefreshCw, Search } from "lucide-react";
import type { ApiContext, AppData } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection } from "../shared/ui";
import { type Card, Field, displayDate, decimalAmount, rateLabels } from "./billing-pricing-fields";

type Evidence = { kind: string; at: string; data: { tenant?: { status: string; reason?: string; charge?: { amount: string; currency: string } }; [key: string]: unknown } };
export function BillingPricingHistory({ api, data, revision }: { api: ApiContext; data: AppData; revision: number }) {
  const [cards, setCards] = useState<Card[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [requestID, setRequestID] = useState("");
  const [evidence, setEvidence] = useState<Evidence[] | null>(null);
  const [queryError, setQueryError] = useState("");
  const [querying, setQuerying] = useState(false);
  useEffect(() => {
    let active = true;
    async function load() {
      setLoading(true); setError("");
      try {
        const response = await adminFetch(api, "/api/admin/billing/rate-cards");
        if (!response.ok) throw new Error(await readAdminError(response, "计费操作失败"));
        const result = await response.json();
        if (active) setCards(result.data);
      } catch (caught) { if (active) setError(caught instanceof Error ? caught.message : tx("计费操作失败")); }
      finally { if (active) setLoading(false); }
    }
    void load();
    return () => { active = false; };
  }, [api, refresh, revision]);
  async function lookup() {
    setQuerying(true); setQueryError(""); setEvidence(null);
    try {
      const response = await adminFetch(api, `/api/admin/billing/evidence/${encodeURIComponent(requestID.trim())}`);
      if (response.status === 404) throw new Error(tx("没有找到计费记录，请检查请求 ID，并确认请求发生在功能启用之后。"));
      if (!response.ok) throw new Error(await readAdminError(response, "计费操作失败"));
      setEvidence((await response.json()).data);
    } catch (caught) { setQueryError(caught instanceof Error ? caught.message : tx("计费操作失败")); }
    finally { setQuerying(false); }
  }
  function targetLabel(card: Card) {
    if (card.kind === "tenant") return card.target;
    const provider = data.providers.find((item) => card.target.startsWith(`${item.id}:`));
    return provider ? `${provider.name} / ${card.target.slice(provider.id.length + 1)}` : card.target;
  }
  return <div className="billing-panel billing-history">
    <DataSection title="已发布价目"><div className="billing-section-toolbar"><p className="billing-hint">{tx("这里保存用于核对的价格版本，实际收费仍由模型目录管理。")}</p><button type="button" className="secondary-button" disabled={loading} onClick={() => setRefresh((value) => value + 1)}><RefreshCw size={15} />{tx("刷新")}</button></div>
      {error ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}
      {error ? null : loading ? <p className="billing-hint">{tx("正在读取价目…")}</p> : cards.length === 0 ? <div className="billing-empty"><h3>{tx("还没有发布价目")}</h3><p>{tx("先到「费用试算」比较价格，确认结果后再发布。")}</p></div> : <div className="billing-version-list">{cards.map((card) => <details key={card.id} className="billing-version"><summary><div><strong>{targetLabel(card)}</strong><small>{tx(card.kind === "tenant" ? "租户费用" : "上游成本")} · {displayDate(card.effective_from)}</small></div><span>{card.currency} · v{card.revision}</span></summary><dl className="billing-rate-summary">{rateLabels.map(([key, label]) => <div key={key}><dt>{tx(label)}</dt><dd>{card.rates[key] ? decimalAmount(card.rates[key]) : tx("未配置")}</dd></div>)}</dl><p className="billing-hint">{tx("价格依据")}: {card.source}</p><details className="billing-disclosure"><summary>{tx("查看完整配置")}</summary><pre>{JSON.stringify(card, null, 2)}</pre></details></details>)}</div>}
    </DataSection>
    <DataSection title="查询请求计费记录"><p className="billing-hint">{tx("从请求日志复制请求 ID，查看当时使用的价格、用量和上游尝试记录。")}</p><form className="billing-lookup" onSubmit={(event) => { event.preventDefault(); void lookup(); }}><Field label="请求 ID"><input disabled={querying} value={requestID} onChange={(event) => { setRequestID(event.target.value); setEvidence(null); setQueryError(""); }} placeholder="req_…" /></Field><button disabled={querying || !requestID.trim()} type="submit" className="primary-button"><Search size={15} />{tx(querying ? "查询中…" : "查询记录")}</button></form>
      {queryError ? <p role="alert" className="billing-notice billing-error">{queryError}</p> : null}
      {evidence ? <ol className="billing-evidence-list">{evidence.map((row, index) => <li key={index}><strong>{tx(row.kind === "admission" ? "请求准入" : row.kind === "attempt_prepared" ? "上游尝试" : "费用核对")}</strong><time>{displayDate(row.at)}</time>{row.data.tenant ? <p>{row.data.tenant.status === "pending" ? tx("待核实：用量、价格或交付依据尚不完整。") : row.data.tenant.charge ? `${decimalAmount(row.data.tenant.charge.amount)} ${row.data.tenant.charge.currency}` : tx("暂无费用结果")}</p> : null}<details className="billing-disclosure"><summary>{tx("查看原始记录")}</summary><pre>{JSON.stringify(row.data, null, 2)}</pre></details></li>)}</ol> : null}
    </DataSection>
  </div>;
}
