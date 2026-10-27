import { useState } from "react";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";

const calendarDateGraceMs = 36 * 60 * 60 * 1000;

function calendarDate(value?: string): Date | null {
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
  const date = new Date(`${value}T00:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value ? date : null;
}

function timestamp(value?: string): Date | null {
  if (!value || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) || !calendarDate(value.slice(0, 10))) return null;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date : null;
}

function regions(value?: string): string {
  if (!value) return "";
  try {
    const parsed: unknown = JSON.parse(value);
    if (Array.isArray(parsed) && parsed.every(item => typeof item === "string")) return parsed.join(", ");
  } catch { /* Older catalog annotations may use a comma-separated list. */ }
  return value;
}

export function ModelCatalogNotices({ metadata }: { metadata?: Record<string, string> }) {
  const [now] = useState(() => Date.now());
  if (!metadata) return null;
  const validCutoff = timestamp(metadata.shutdown_at);
  const shutdownDate = calendarDate(metadata.shutdown_date);
  const redirectDate = calendarDate(metadata.redirect_date);
  const redirectAt = timestamp(metadata.redirect_at);
  const newUserDate = calendarDate(metadata.new_user_restriction_date);
  const regionalEol = calendarDate(metadata.regional_eol_date);
  const formatDate = (date: Date) => new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeZone: "UTC" }).format(date);
  const cutoff = validCutoff?.getTime() ?? (shutdownDate ? shutdownDate.getTime() + calendarDateGraceMs : null);
  const retired = metadata.lifecycle_status === "retired" || (metadata.lifecycle_status === "deprecated" && cutoff !== null && cutoff <= now);
  const redirectCutoff = redirectAt?.getTime() ?? (redirectDate ? redirectDate.getTime() + calendarDateGraceMs : null);
  const messages: string[] = [];
  if (retired) messages.push(tx("官方入口已停用；请选择替代型号。"));
  else if (metadata.lifecycle_status === "deprecated") messages.push(tx("官方已公告弃用，请规划迁移。"));
  else if (metadata.lifecycle_status === "redirected") messages.push(redirectCutoff !== null && redirectCutoff > now ? tx("此别名已公告重定向计划，请核对实际服务型号。") : tx("此型号为重定向别名，实际模型可能已变化。"));
  if (validCutoff) messages.push(formatTranslationTemplate(tx("官方停用时间：{date}"), { date: new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "short" }).format(validCutoff) }));
  else if (shutdownDate) messages.push(formatTranslationTemplate(tx("官方停用日期：{date}（时区未公布）"), { date: formatDate(shutdownDate) }));
  if (redirectDate) messages.push(formatTranslationTemplate(tx("计划重定向日期：{date}（时区未公布）"), { date: formatDate(redirectDate) }));
  if (newUserDate) messages.push(formatTranslationTemplate(tx("新用户访问限制日期：{date}（时区未公布）"), { date: formatDate(newUserDate) }));
  if (metadata.regional_lifecycle_status === "retiring") messages.push(formatTranslationTemplate(tx("部分地域已公告停用：{regions}；请核对部署地域。"), { regions: regions(metadata.lifecycle_regions) }));
  if (metadata.regional_lifecycle_status === "legacy") messages.push(formatTranslationTemplate(tx("部分地域已进入 Legacy：{regions}；请核对部署地域。"), { regions: regions(metadata.lifecycle_regions) }));
  if (regionalEol) messages.push(formatTranslationTemplate(tx("上述地域停用日期：{date}（时区未公布）"), { date: formatDate(regionalEol) }));
  if (metadata.replacement_model) messages.push(formatTranslationTemplate(tx("替代型号：{model}"), { model: metadata.replacement_model }));
  if (metadata.availability === "preview") messages.push(tx("预览模型"));
  if (metadata.availability === "restricted" || metadata.access_requirement) messages.push(tx("需要指定套餐或额外开通权限。"));
  if (metadata.access_requirement) messages.push(metadata.access_requirement);
  if (metadata.call_support === "unsupported") messages.push(tx("仅目录收录，当前尚未支持此接口调用。"));
  if (metadata.call_support === "unsupported" && metadata.support_note) messages.push(metadata.support_note);
  if (metadata.pricing_status === "unverified" || metadata.pricing_status === "verified_non_token") messages.push(tx("成本尚未配置；目录零值不代表免费。"));
  if (metadata.pricing_note) messages.push(metadata.pricing_note);
  if (!messages.length) return null;
  return <span className="model-catalog-notices">{messages.map((message) => <small key={message}>{message}</small>)}</span>;
}
