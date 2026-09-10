import { type AdminUIContribution } from "../core/types";
import { languageLocale } from "../i18n/runtime";

export type AdminUIFieldType = "metric" | "text" | "code_viewer";

export type AdminUIField = {
  name: string;
  type: AdminUIFieldType;
  label: string;
  source?: string;
  value?: string | number | boolean;
  format?: "compact" | "money_usd" | "number" | "percent" | string;
  help?: string;
};

export type AdminUIPluginPage = {
  key: string;
  pluginID: string;
  id: string;
  title: string;
  description: string;
  contribution: AdminUIContribution;
};

export function adminUIContributionsForSlot(contributions: AdminUIContribution[], slot: string) {
  return contributions.filter((contribution) => contribution.slot === slot);
}

export function adminUIPluginPages(contributions: AdminUIContribution[]): AdminUIPluginPage[] {
  return adminUIContributionsForSlot(contributions, "nav.section")
    .map((contribution) => ({
      key: adminUIPluginPageKey(contribution),
      pluginID: contribution.plugin_id,
      id: contribution.id,
      title: contribution.title || contribution.id,
      description: schemaString(contribution.schema?.description),
      contribution,
    }));
}

export function adminUIPluginPageKey(contribution: AdminUIContribution) {
  return JSON.stringify([contribution.plugin_id, contribution.id]);
}

export function adminUIFields(contribution: AdminUIContribution): AdminUIField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    const type = adminUIFieldType(schemaString(field.type));
    const name = schemaString(field.name);
    if (!type || !name) return [];
    return [{
      name,
      type,
      label: schemaString(field.label) || name,
      source: schemaString(field.source),
      value: schemaScalar(field.value),
      format: schemaString(field.format),
      help: schemaString(field.help),
    }];
  });
}

export function adminUIFieldValue(context: unknown, field: AdminUIField, locale = languageLocale()) {
  const rawValue = field.value ?? adminUISourceValue(context, field.source);
  return formatAdminUIValue(rawValue, field, locale);
}

export function formatAdminUIValue(rawValue: unknown, field: { format?: string; type?: string }, locale = languageLocale()) {
  if (rawValue === undefined || rawValue === null || rawValue === "") return "-";
  if (field.format === "money_usd") return formatMoney(Number(rawValue) || 0, locale);
  if (field.format === "compact") return compactNumber(Number(rawValue) || 0, locale);
  if (field.format === "percent") return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 3 }).format((Number(rawValue) || 0) / 100);
  if (typeof rawValue === "number") return formatNumber(rawValue, locale);
  if (typeof rawValue === "boolean") return rawValue ? "true" : "false";
  if (field.type === "code_viewer" && typeof rawValue === "object") return JSON.stringify(rawValue, null, 2);
  return String(rawValue);
}

export function adminUIActionKey(pluginID: string, actionID?: string) {
  return JSON.stringify([pluginID, actionID ?? ""]);
}

export function redactAdminUIResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactAdminUIResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) =>
    sensitiveAdminUIResultKey(key) ? [key, "[redacted]"] : [key, redactAdminUIResult(child)],
  ));
}

function adminUIFieldType(type: string): AdminUIFieldType | "" {
  switch (type) {
    case "metric":
    case "text":
    case "code_viewer":
      return type;
    default:
      return "";
  }
}

function adminUISourceValue(context: unknown, source?: string) {
  if (!source) return undefined;
  const segments = source.split(".").map((segment) => segment.trim()).filter(Boolean);
  let current = context;
  for (const segment of segments) {
    if (segment === "length" && Array.isArray(current)) {
      current = current.length;
      continue;
    }
    if (Array.isArray(current) && arrayIndexPattern.test(segment)) {
      current = current[Number(segment)];
      continue;
    }
    if (!current || typeof current !== "object" || Array.isArray(current)) return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

function sensitiveAdminUIResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized.includes("token") || normalized.includes("secret") || normalized.includes("credential") || normalized.includes("api_key");
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}

const arrayIndexPattern = /^\d+$/;

function formatNumber(value: number, locale: string) {
  return new Intl.NumberFormat(locale).format(value || 0);
}

function compactNumber(value: number, locale: string) {
  return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 2 }).format(value || 0);
}

function formatMoney(value: number, locale: string) {
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: value >= 1 ? 2 : 6,
    maximumFractionDigits: value >= 1 ? 2 : 6,
  }).format(value || 0);
}
