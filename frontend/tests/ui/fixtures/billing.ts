import type { StatementQuery, StatementResult, StatementRow } from "../../../features/admin/domain/billing-statements";
import { fixedTime, model, project } from "./shell";

export type StatementState = "complete" | "pending" | "empty";
const row: StatementRow = {
  id: "ui-request-1:tenant", request_id: "ui-request-1", source: "tenant", at: fixedTime, timezone: "Asia/Shanghai",
  project_id: project.id, api_key_id: "key_ui", model: model.name, currency: "USD", amount: "1.08", usd: "1.08",
  status: "estimated", reason: "recorded_tenant_charge", units: { input: 200000, cache_read: 800000, output: 10000 },
  lines: [{ kind: "input", units: 200000, rate: "4", amount: "0.80" }, { kind: "cache_read", units: 800000, rate: "0.25", amount: "0.20" }, { kind: "output", units: 10000, rate: "8", amount: "0.08" }],
};

export function statementResponse(query: StatementQuery, state: StatementState): StatementResult {
  // These are declared UI examples, not a second implementation of billing.
  const provider = query.side === "provider";
  const source = provider ? "provider_estimate" : "tenant";
  const base = { ...structuredClone(row), source, id: provider ? "ui-attempt-1" : row.id, provider_id: "provider_ui", resource_id: "resource_ui" };
  const rows: StatementRow[] = state === "empty" ? [] : [base];
  if (state === "pending") rows.push(
    { ...base, id: "ui-free", request_id: "ui-request-free", amount: "0", usd: "0", units: {}, lines: [] },
    { ...base, id: "ui-pending", request_id: "ui-request-pending", amount: null, usd: null, status: "pending", reason: "pricing_incomplete", units: {}, lines: [] },
  );
  if (query.side === "margin" && state !== "empty") rows.push({ ...base, id: "ui-attempt-cost", source: "provider_estimate", amount: "0.60", usd: "0.60", lines: [], units: { input: 1000000 } });
  return {
    query: structuredClone(query), generated_at: fixedTime,
    from: `${query.from}T00:00:00+08:00`, to: `${query.to}T00:00:00+08:00`,
    time_basis: provider ? "attempt_start" : "request_admission", rows,
    totals: state === "empty" ? {} : { [`${source}:USD`]: "1.08", ...(query.side === "margin" ? { "provider_estimate:USD": "0.60" } : {}) },
    unknown_count: state === "pending" ? 1 : 0, incomplete_count: state === "pending" ? 1 : 0,
    estimated_margin_usd: query.side === "margin" && state === "complete" ? "0.48" : null,
  };
}

export function readStatementQuery(body: unknown): StatementQuery {
  if (!body || typeof body !== "object") throw new Error("Expected statement query object");
  const value = body as Record<string, unknown>;
  for (const key of ["side", "from", "to", "timezone", "customer", "provider_id", "resource_id", "model"]) {
    if (typeof value[key] !== "string") throw new Error(`Expected string field ${key}`);
  }
  if (!["tenant", "provider", "margin"].includes(value.side as string)) throw new Error("Unexpected statement side");
  if (!Array.isArray(value.project_ids) || value.project_ids.some(id => typeof id !== "string")) throw new Error("Expected project_ids string array");
  return value as StatementQuery;
}
