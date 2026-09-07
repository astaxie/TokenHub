import { useEffect, useState } from "react";
import type { ApiContext } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";

type FieldEvidence = { value: number | null; state: string };
type UsageEvidence = { protocol: string; fields: Record<string, FieldEvidence>; stream_complete?: boolean; invocation_id?: string; response_id?: string; trace_id?: string };
type Charge = { reason?: string; usage_evidence?: UsageEvidence };
export type EvidenceRow = { kind: string; data: { tenant?: Charge; attempts?: { number?: number; attempt_id?: string; pricing: Charge }[]; [key: string]: unknown } };
const names: Record<string, string> = { input_total: "总输入 Token", input_uncached: "普通输入", cache_read: "缓存读取", cache_write_total: "缓存写入总量", cache_write_5m: "5 分钟缓存写入", cache_write_1h: "1 小时缓存写入", output: "输出", total: "总 Token", reasoning: "推理 Token", input_audio: "输入音频 Token", output_audio: "输出音频 Token", accepted_prediction: "接受预测 Token", rejected_prediction: "拒绝预测 Token" };
function stateName(state: string) { return ({ reported: tx("上游报告"), derived: tx("推导值"), estimated: tx("估算值"), missing: tx("未提供"), invalid: tx("无效用量"), legacy_unverified: tx("旧记录未验证") })[state] ?? state; }
function Fields({ charge }: { charge: Charge }) {
  const evidence = charge.usage_evidence;
  if (!evidence) return <p>{tx("历史记录没有字段来源信息")}</p>;
  return <><p>{evidence.protocol}{evidence.stream_complete === false ? ` · ${tx("流式完成依据不足")}` : ""}</p>
    {evidence.invocation_id ? <p>{tx("供应商调用 ID")}: {evidence.invocation_id}</p> : null}
    {evidence.response_id ? <p>{tx("上游响应 ID")}: {evidence.response_id}</p> : null}
    {evidence.trace_id ? <p>{tx("边缘追踪 ID")}: {evidence.trace_id}</p> : null}
    <div className="table-wrap"><table><thead><tr><th>{tx("计量项目")}</th><th>{tx("数值")}</th><th>{tx("来源")}</th></tr></thead><tbody>{Object.entries(evidence.fields).map(([key, field]) => <tr key={key}><td>{tx(names[key] ?? key)}</td><td>{field.value === null ? "—" : new Intl.NumberFormat(languageLocale()).format(field.value)}</td><td>{stateName(field.state)}</td></tr>)}</tbody></table></div>
  </>;
}
export function UsageEvidenceRows({ rows }: { rows: EvidenceRow[] }) {
  return <>{rows.filter(row => row.kind === "shadow_settlement").map((row, index) => <div key={index}>
    {row.data.tenant ? <details><summary>{tx("下游用量依据")}</summary><Fields charge={row.data.tenant} /></details> : null}
    {row.data.attempts?.map((attempt, n) => <details key={attempt.attempt_id ?? n}><summary>{tx("上游尝试")} {new Intl.NumberFormat(languageLocale()).format(attempt.number ?? n + 1)}</summary><Fields charge={attempt.pricing} /></details>)}
  </div>)}</>;
}
export function RequestUsageEvidence({ api, requestID }: { api: ApiContext; requestID: string }) {
  const [rows, setRows] = useState<EvidenceRow[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    const abort = new AbortController(); setRows(null); setError("");
    void (async () => { try {
      const response = await adminFetch(api, `/api/admin/billing/evidence/${encodeURIComponent(requestID)}`, { signal: abort.signal });
      if (response.status === 404) { if (!abort.signal.aborted) setRows([]); return; }
      if (!response.ok) throw new Error(await readAdminError(response, tx("读取用量证据失败")));
      const body = await response.json(); if (!abort.signal.aborted) setRows(body.data);
    } catch (caught) { if (!abort.signal.aborted) setError(caught instanceof Error ? caught.message : tx("读取用量证据失败")); } })();
    return () => abort.abort();
  }, [api, requestID]);
  return <details className="request-usage-evidence"><summary>{tx("计费用量证据")}</summary><p>{tx("用量记录独立于价格；未提供不等于零，金额匹配不代表 Token 已核实。")}</p>{error ? <p role="alert">{error}</p> : rows ? rows.length ? <UsageEvidenceRows rows={rows} /> : <p>{tx("历史记录没有字段来源信息")}</p> : <p>{tx("正在读取用量证据…")}</p>}</details>;
}
