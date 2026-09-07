import { useState } from "react";
import type { ApiContext } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { Field, decimalAmount, localDateTime, type Card } from "./billing-pricing-fields";

export function ModelRuleSimulator({ api, card, embedding }: { api: ApiContext; card: Card; embedding: boolean }) {
  const [counts, setCounts] = useState({ prompt_tokens: "1000", cached_input_tokens: "0", cache_write_other_tokens: "0", cache_write_5m_input_tokens: "0", cache_write_1h_input_tokens: "0", completion_tokens: embedding ? "0" : "1000" });
  const [at, setAt] = useState(localDateTime); const [result, setResult] = useState<{ amount: string; period: string; basis: string } | null>(null);
  const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const basis = JSON.stringify({ card, counts, at });
  async function simulate() {
    setBusy(true); setError(""); setResult(null);
    try {
      const numeric = Object.fromEntries(Object.entries(counts).map(([key, value]) => [key, Number(value)]));
      if (Object.values(numeric).some(value => !Number.isSafeInteger(value) || value < 0)) throw new Error(tx("Token 数必须是非负安全整数"));
      const write = numeric.cache_write_other_tokens + numeric.cache_write_5m_input_tokens + numeric.cache_write_1h_input_tokens;
      if (write + numeric.cached_input_tokens > numeric.prompt_tokens) throw new Error(tx("缓存读取与写入之和不能超过总输入 Token。"));
      const response = await adminFetch(api, "/api/admin/billing/preview", { method: "POST", body: JSON.stringify({ card, at: new Date(at).toISOString(), usage: { ...numeric, cache_write_input_tokens: write } }) });
      if (!response.ok) throw new Error(await readAdminError(response, tx("计费操作失败")));
      const body = await response.json(); setResult({ amount: body.charge.amount, period: body.snapshot.period ?? "", basis });
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("计费操作失败")); } finally { setBusy(false); }
  }
  return <details className="billing-disclosure billing-rule-simulator"><summary>{tx("高级：验证计价规则")}</summary><p>{tx("仅验证当前候选价格，不保存价格，也不作为历史收益结论。")}</p><div className="billing-form-grid">
    {([["prompt_tokens", "总输入 Token"], ["completion_tokens", "输出 Token"], ["cached_input_tokens", "缓存读取 Token"], ["cache_write_other_tokens", "其他缓存写入 Token"], ["cache_write_5m_input_tokens", "5 分钟缓存写入 Token"], ["cache_write_1h_input_tokens", "1 小时缓存写入 Token"]] as const).filter(([key]) => !embedding || key === "prompt_tokens").map(([key, label]) => <Field key={key} label={label}><input inputMode="numeric" value={counts[key]} onChange={e => setCounts({ ...counts, [key]: e.target.value })} /></Field>)}
    <Field label="试算时间"><input type="datetime-local" value={at} onChange={e => setAt(e.target.value)} /></Field></div><button className="secondary-button" type="button" disabled={busy} onClick={() => void simulate()}>{tx("试算费用")}</button>
    {error ? <p role="alert">{error}</p> : null}{result?.basis === basis ? <p className="billing-result"><output>{decimalAmount(result.amount)} USD</output> · {result.period || tx("默认价格")}</p> : null}
  </details>;
}
