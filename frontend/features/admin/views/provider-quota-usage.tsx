import type { ProviderAccountQuota } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";
import { providerAccountQuotaPrimaryWindow, providerAccountQuotaUsedPercent } from "./provider-account-ui";

export function ProviderQuotaUsage({ quota }: { quota: ProviderAccountQuota }) {
  const known = [quota.used_percent, quota.remaining_percent, providerAccountQuotaPrimaryWindow(quota)?.used_percent].some(Number.isFinite);
  const used = providerAccountQuotaUsedPercent(quota);
  const percent = new Intl.NumberFormat(languageLocale(), { style: "percent", maximumFractionDigits: 1 });
  return <div className="provider-quota-usage">
    <span>{tx("主窗口使用率")}</span>
    <strong>{known ? percent.format(used / 100) : "-"}</strong>
    {known ? <div aria-label={tx("主窗口使用率")} aria-valuemax={100} aria-valuemin={0} aria-valuenow={used} className="provider-quota-progress" role="progressbar">
      <span style={{ width: `${used}%` }} />
    </div> : null}
  </div>;
}
