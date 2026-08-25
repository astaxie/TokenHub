import { PlugZap } from "lucide-react";
import { type AdminUIContribution, type AppData } from "../core/types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";

type DashboardMetricField = {
  name: string;
  type: "metric";
  label: string;
  source?: string;
  value?: string | number | boolean;
  format?: "compact" | "money_usd" | "number" | "percent" | string;
  help?: string;
};

export function AdminUIDashboardCards({ data }: { data: AppData }) {
  const cards = data.pluginUI
    .filter((contribution) => contribution.slot === "dashboard.card")
    .map((contribution) => ({ contribution, fields: dashboardMetricFields(contribution) }))
    .filter((card) => card.fields.length > 0);

  if (cards.length === 0) return null;

  return (
    <section className="overview-plugin-cards">
      {cards.map(({ contribution, fields }) => (
        <article className="overview-panel admin-ui-dashboard-card" key={`${contribution.plugin_id}:${contribution.id}`}>
          <div className="admin-ui-dashboard-card-head">
            <span><PlugZap size={16} /></span>
            <div>
              <h2>{contribution.title || contribution.id}</h2>
              <p>{contribution.plugin_id}</p>
            </div>
          </div>
          <div className="admin-ui-dashboard-metrics">
            {fields.map((field) => (
              <div className="admin-ui-dashboard-metric" key={field.name}>
                <span>{field.label}</span>
                <strong>{dashboardMetricValue(data, field)}</strong>
                {field.help ? <small>{field.help}</small> : null}
              </div>
            ))}
          </div>
        </article>
      ))}
    </section>
  );
}

export function dashboardMetricFields(contribution: AdminUIContribution): DashboardMetricField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    if (schemaString(field.type) !== "metric") return [];
    const name = schemaString(field.name);
    if (!name) return [];
    return [{
      name,
      type: "metric",
      label: schemaString(field.label) || name,
      source: schemaString(field.source),
      value: schemaScalar(field.value),
      format: schemaString(field.format),
      help: schemaString(field.help),
    }];
  });
}

export function dashboardMetricValue(data: AppData, field: DashboardMetricField) {
  const rawValue = field.value ?? dashboardSourceValue(data, field.source);
  if (rawValue === undefined || rawValue === null || rawValue === "") return "-";
  if (field.format === "money_usd") return `$${formatMoney(Number(rawValue) || 0)}`;
  if (field.format === "compact") return compactNumber(Number(rawValue) || 0);
  if (field.format === "percent") return `${formatNumber(Number(rawValue) || 0)}%`;
  if (typeof rawValue === "number") return formatNumber(rawValue);
  if (typeof rawValue === "boolean") return rawValue ? "true" : "false";
  return String(rawValue);
}

function dashboardSourceValue(data: AppData, source?: string) {
  if (!source) return undefined;
  const segments = source.split(".").map((segment) => segment.trim()).filter(Boolean);
  let current: unknown = data;
  for (const segment of segments) {
    if (segment === "length" && Array.isArray(current)) {
      current = current.length;
      continue;
    }
    if (!current || typeof current !== "object" || Array.isArray(current)) return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}
