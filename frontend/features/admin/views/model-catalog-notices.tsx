import { useState } from "react";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";

export function ModelCatalogNotices({ metadata }: { metadata?: Record<string, string> }) {
  const [now] = useState(() => Date.now());
  if (!metadata) return null;
  const cutoff = metadata.shutdown_at ? new Date(metadata.shutdown_at) : null;
  const validCutoff = cutoff && Number.isFinite(cutoff.getTime()) ? cutoff : null;
  const retired = metadata.lifecycle_status === "retired" || (metadata.lifecycle_status === "deprecated" && validCutoff !== null && validCutoff.getTime() <= now);
  const messages: string[] = [];
  if (retired) messages.push(tx("官方入口已停用；请选择替代型号。"));
  else if (metadata.lifecycle_status === "deprecated") messages.push(tx("官方已公告弃用，请规划迁移。"));
  else if (metadata.lifecycle_status === "redirected") messages.push(tx("此型号为重定向别名，实际模型可能已变化。"));
  if (validCutoff) messages.push(formatTranslationTemplate(tx("官方停用时间：{date}"), { date: new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "short" }).format(validCutoff) }));
  if (metadata.replacement_model) messages.push(formatTranslationTemplate(tx("替代型号：{model}"), { model: metadata.replacement_model }));
  if (metadata.availability === "preview") messages.push(tx("预览模型"));
  if (metadata.availability === "restricted" || metadata.access_requirement) messages.push(tx("需要指定套餐或额外开通权限。"));
  if (metadata.call_support === "unsupported") messages.push(tx("仅目录收录，当前尚未支持此接口调用。"));
  if (metadata.pricing_status === "unverified" || metadata.pricing_status === "verified_non_token") messages.push(tx("成本尚未配置；目录零值不代表免费。"));
  if (metadata.pricing_note) messages.push(metadata.pricing_note);
  if (!messages.length) return null;
  return <span className="model-catalog-notices">{messages.map((message) => <small key={message}>{message}</small>)}</span>;
}
