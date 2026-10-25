import { languageLocale, tx } from "../i18n/runtime";

export function ProviderCheckinAction({ busy, disabled, error, result, onRun }: {
  busy: boolean;
  disabled: boolean;
  error: string;
  result: string;
  onRun: () => void;
}) {
  const outcome = checkinResult(result);
  const number = new Intl.NumberFormat(languageLocale());
  return <div className="plugin-action-runner">
    <button className="secondary-button" type="button" disabled={disabled || busy} onClick={onRun}>{tx(busy ? "签到中" : "签到")}</button>
    {error ? <p className="provider-quota-error" role="alert">{error}</p> : null}
    {outcome ? <div role="status">
      <p>{checkinStatus(outcome.outcome)}</p>
      {typeof outcome.credit === "number" ? <p>{tx("签到奖励：{amount}").replace("{amount}", number.format(outcome.credit))}</p> : null}
      {typeof outcome.streak === "number" ? <p>{tx("连续签到 {days} 天").replace("{days}", number.format(outcome.streak))}</p> : null}
    </div> : null}
  </div>;
}

function checkinResult(raw: string): { outcome: string; credit?: number; streak?: number } | null {
  try {
    const parsed = JSON.parse(raw);
    const result = parsed.data ?? parsed;
    if (!["claimed", "done", "inactive", "ineligible"].includes(result?.outcome)) return null;
    return { outcome: result.outcome, ...(Number.isFinite(result.credit) ? { credit: result.credit } : {}), ...(Number.isFinite(result.streak) ? { streak: result.streak } : {}) };
  } catch { return null; }
}

function checkinStatus(outcome: string) {
  switch (outcome) {
    case "claimed": return tx("签到成功");
    case "done": return tx("今天已签到");
    case "inactive": return tx("当前没有可领取的签到奖励");
    default: return tx("此账号暂不符合签到条件");
  }
}
