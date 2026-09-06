import { useEffect, useState } from "react";
import { RefreshCw, Search } from "lucide-react";
import type { ApiContext, AppData } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection } from "../shared/ui";
import { type Card, Field, displayDate, decimalAmount, rateLabels } from "./billing-pricing-fields";

type PriceChange = { id: string; model_name: string; actor_name: string; effective_at: string; before: Card; after: Card };

type Evidence = { kind: string; at: string; data: { tenant?: { status: string; reason?: string; charge?: { amount: string; currency: string } }; [key: string]: unknown } };
export function BillingPricingHistory({ api, revision }: { api: ApiContext; data: AppData; revision: number }) {
  const [cards, setCards] = useState<PriceChange[]>([]);
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
        const response = await adminFetch(api, "/api/admin/billing/price-changes");
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
  return <div className="billing-panel billing-history">
    <DataSection title="实际调价记录"><div className="billing-section-toolbar"><p className="billing-hint">{tx("只记录已经应用到模型的价格变更；试算与取消操作不会产生记录。")}</p><button type="button" className="secondary-button" disabled={loading} onClick={() => setRefresh((value) => value + 1)}><RefreshCw size={15} />{tx("刷新")}</button></div>
      {error ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}
      {error ? null : loading ? <p className="billing-hint">{tx("正在读取调价记录…")}</p> : cards.length === 0 ? <div className="billing-empty"><h3>{tx("还没有调价记录")}</h3><p>{tx("到「模型调价」试算，并确认应用后，这里会记录变更。")}</p></div> : <div className="billing-version-list">{cards.map((change) => <details key={change.id} className="billing-version"><summary><div><strong>{change.model_name}</strong><small>{change.actor_name || tx("系统")} · {displayDate(change.effective_at)}</small></div><span>{tx("已生效")}</span></summary><div className="billing-confirm-periods">{([change.before, change.after] as const).map((card, index) => <div key={index}><strong>{tx(index === 0 ? "调整前" : "调整后")}</strong><dl className="billing-rate-summary">{rateLabels.map(([key, label]) => <div key={key}><dt>{tx(label)}</dt><dd>{card.rates[key] ? decimalAmount(card.rates[key]) : tx("模型默认规则")}</dd></div>)}</dl></div>)}</div><details className="billing-disclosure"><summary>{tx("查看完整配置")}</summary><pre>{JSON.stringify(change, null, 2)}</pre></details></details>)}</div>}
    </DataSection>
    <DataSection title="查询请求计费记录"><p className="billing-hint">{tx("从请求日志复制请求 ID，查看当时使用的价格、用量和上游尝试记录。")}</p><form className="billing-lookup" onSubmit={(event) => { event.preventDefault(); void lookup(); }}><Field label="请求 ID"><input disabled={querying} value={requestID} onChange={(event) => { setRequestID(event.target.value); setEvidence(null); setQueryError(""); }} placeholder="req_…" /></Field><button disabled={querying || !requestID.trim()} type="submit" className="primary-button"><Search size={15} />{tx(querying ? "查询中…" : "查询记录")}</button></form>
      {queryError ? <p role="alert" className="billing-notice billing-error">{queryError}</p> : null}
      {evidence ? <ol className="billing-evidence-list">{evidence.map((row, index) => <li key={index}><strong>{tx(row.kind === "admission" ? "请求准入" : row.kind === "attempt_prepared" ? "上游尝试" : "费用核对")}</strong><time>{displayDate(row.at)}</time>{row.data.tenant ? <p>{row.data.tenant.status === "pending" ? tx("待核实：用量、价格或交付依据尚不完整。") : row.data.tenant.charge ? `${decimalAmount(row.data.tenant.charge.amount)} ${row.data.tenant.charge.currency}` : tx("暂无费用结果")}</p> : null}<details className="billing-disclosure"><summary>{tx("查看原始记录")}</summary><pre>{JSON.stringify(row.data, null, 2)}</pre></details></li>)}</ol> : null}
    </DataSection>
  </div>;
}
