export type StatementSide = "tenant" | "provider" | "margin";
export type StatementQuery = { side: StatementSide; from: string; to: string; timezone: string; customer: string; project_ids: string[]; provider_id: string; resource_id: string; model: string };
export type StatementRow = {
  external_id?: string; external_request_id?: string; usage_quantity?: number; usage_unit?: string;
  id: string; request_id?: string; source: string; at: string; end_at?: string; timezone: string;
  project_id?: string; team_id?: string; api_key_id?: string; model: string; provider_id?: string; resource_id?: string;
  currency: string; amount: string | null; usd: string | null; status: string; reason?: string;
  units: Record<string, number>; lines?: { kind: string; units: number; rate: string; amount: string }[];
  price?: { version?: string; source: string; currency: string; at: string; exchange_rate_version?: string; exchange_rate?: string; rates: Record<string, string> };
};
export type StatementResult = { query: StatementQuery; generated_at: string; from: string; to: string; time_basis: string; rows: StatementRow[]; totals: Record<string, string>; unknown_count: number; incomplete_count: number; estimated_margin_usd: string | null };

// Export exactly the preview payload, not a second query that can include new
// calls or changed supplier records. Quote every field and neutralize formulas.
export function statementCSV(result: StatementResult): string {
  const tenant = result.query.side === "tenant";
  const common = ["id", "source", "at", "end_at", "timezone", "project_id", "team_id", "api_key_id", "model", "currency", "amount", "usd", "status", "reason", "units", "lines", "price"] as const;
  const columns: (keyof StatementRow)[] = tenant ? [...common] : [...common, "provider_id", "resource_id", "request_id", "external_id", "external_request_id", "usage_quantity", "usage_unit"];
  const q = result.query;
  const rows: unknown[][] = [
    ["statement", q.side], ["customer", q.customer], ["project_ids", q.project_ids.join(" | ")],
    ...(tenant ? [] : [["provider_id", q.provider_id], ["resource_id", q.resource_id]]), ["model_filter", q.model],
    ["from_inclusive", result.from], ["to_exclusive", result.to], ["timezone", q.timezone],
    ["generated_at", result.generated_at], ["time_basis", result.time_basis],
    ["unknown_count", result.unknown_count], ["incomplete_count", result.incomplete_count],
    ...Object.entries(result.totals).map(([key, value]) => ["total", key, value]),
    ...(q.side === "margin" ? [["estimated_margin_usd", result.estimated_margin_usd ?? "unknown"]] : []),
    [], [...columns], ...result.rows.map(row => columns.map(key => row[key] ?? "")),
  ];
  return "\uFEFF" + rows.map(row => row.map(value => {
    let text = typeof value === "object" ? JSON.stringify(value) : String(value);
    if (/^[\s\uFEFF]*[=+\-@]/u.test(text) || /^[\t\r\n]/u.test(text)) text = "'" + text;
    return '"' + text.replaceAll('"', '""') + '"';
  }).join(",")).join("\r\n");
}

export function statementMonth(now = new Date()) {
  const date = (year: number, month: number) => `${year}-${String(month).padStart(2, "0")}-01`;
  const year = now.getFullYear(), month = now.getMonth() + 1;
  return { from: date(year, month), to: date(month === 12 ? year + 1 : year, month === 12 ? 1 : month + 1) };
}

// Keep decimal strings exact: Number(amount) would lose precision before Intl
// formats large invoices or token prices.
export function formatStatementAmount(amount: string, currency: string, locale: string): string {
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(amount);
  if (!match) return amount + " " + currency;
  const negative = match[1] === "-";
  const integer = BigInt(match[2]);
  const fraction = (match[3] ?? "").replace(/0+$/, "").padEnd(2, "0");
  const negativeZero = negative && integer === 0n;
  const signed = negative ? (negativeZero ? -1n : -integer) : integer;
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency, minimumFractionDigits: 2 }).formatToParts(signed).map(part => {
      if (part.type === "fraction") return fraction;
      if (negativeZero && part.type === "integer") return "0";
      return part.value;
    }).join("");
  } catch { return amount + " " + currency; }
}
