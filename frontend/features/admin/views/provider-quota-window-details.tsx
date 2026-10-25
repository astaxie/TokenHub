import type { ProviderAccountQuota } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";

export function ProviderQuotaWindowDetails({ quota }: { quota: ProviderAccountQuota }) {
  const windows = [
    ...(quota.windows ?? []).map(window => ({ window, scope: "" })),
    ...(quota.additional_rate_limits ?? []).flatMap(limit => limit.rate_limit?.primary_window ? [{ window: { ...limit.rate_limit.primary_window, label: limit.limit_name || limit.rate_limit.primary_window.label }, scope: limit.metered_feature || "" }] : []),
  ];
  if (windows.length === 0) return null;
  const number = new Intl.NumberFormat(languageLocale(), { maximumFractionDigits: 1 });
  const percent = new Intl.NumberFormat(languageLocale(), { style: "percent", maximumFractionDigits: 1 });
  const date = new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "medium" });
  return <div className="provider-quota-grid provider-quota-windows">
    {windows.map(({ window, scope }, index) => {
      const reset = window.reset_at ? new Date(window.reset_at * 1000) : null;
      return <div className="provider-quota-metric" key={`${window.key || window.label}:${index}`}>
      <span>{window.label || tx("额度窗口 {number}").replace("{number}", number.format(index + 1))}</span>
      <strong>{Number.isFinite(window.used_percent) ? percent.format(window.used_percent! / 100) : "-"}</strong>
      {reset && Number.isFinite(reset.getTime()) ? <small>{tx("重置时间：{time}").replace("{time}", date.format(reset))}</small> : null}
      {scope ? <small>{scope.startsWith("Excludes: ") ? tx("不适用模型：{models}").replace("{models}", scope.slice(10)) : scope}</small> : null}
    </div>;
    })}
  </div>;
}
