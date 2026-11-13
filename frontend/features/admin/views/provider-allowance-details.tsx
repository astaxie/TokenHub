import { useState } from "react";
import type { AccountAllowance } from "../core/account-allowance-types";
import type {
  AllowanceEntry,
  AllowanceEvent,
  AllowancePoint,
} from "../core/provider-allowance";
import { languageLocale, tx } from "../i18n/runtime";

export function allowanceStatusLabel(
  status: AccountAllowance["status"],
): string {
  switch (status) {
    case "available":
      return tx("额度可用");
    case "exhausted":
      return tx("额度耗尽");
    case "query_error":
      return tx("查询失败");
    default:
      return tx("额度未知");
  }
}
export function allowanceTime(raw?: string) {
  if (!raw) return tx("尚无数据");
  const value = new Date(raw);
  return Number.isFinite(value.getTime())
    ? new Intl.DateTimeFormat(languageLocale(), {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(value)
    : tx("尚无数据");
}
export function AllowanceWindows({ entry }: { entry: AllowanceEntry }) {
  const a = entry.allowance;
  const number = new Intl.NumberFormat(languageLocale(), {
    maximumFractionDigits: 3,
  });
  const percent = new Intl.NumberFormat(languageLocale(), {
    style: "percent",
    maximumFractionDigits: 1,
  });
  return (
    <div className="allowance-window-panel">
      <div className={`allowance-notice ${entry.status}`}>
        <strong>{allowanceStatusLabel(entry.status)}</strong>
        <p>
          {entry.status === "query_error"
            ? tx("查询失败。以下保留上次成功的数据，调度不会把它视为可用额度。")
            : entry.status === "unknown"
              ? tx("额度未知或数据已过期，等待后台刷新。")
              : tx("请求仅读取缓存，后台定期刷新。")}
        </p>
      </div>
      <dl className="allowance-metadata">
        <div>
          <dt>{tx("最近成功采集")}</dt>
          <dd>{allowanceTime(a.fetched_at)}</dd>
        </div>
        <div>
          <dt>{tx("最近查询")}</dt>
          <dd>{allowanceTime(a.checked_at)}</dd>
        </div>
        {a.expires_at ? (
          <div>
            <dt>{tx("套餐到期")}</dt>
            <dd>{allowanceTime(a.expires_at)}</dd>
          </div>
        ) : null}
      </dl>
      {(a.windows ?? []).length === 0 ? (
        <p>{tx("尚无额度窗口，可配置通用采集或手动余额。")}</p>
      ) : null}
      <div className="allowance-windows">
        {(a.windows ?? []).map((window, index) => (
          <article key={`${window.name}:${index}`}>
            <div className="allowance-heading">
              <h4>{window.name || tx("额度窗口")}</h4>
              {window.informational ? <small>{tx("仅供参考")}</small> : null}
            </div>
            <strong>
              {window.unlimited
                ? tx("无限制")
                : window.unknown
                  ? tx("额度未知")
                  : window.balance !== undefined
                    ? `${number.format(window.balance)} ${window.unit ?? ""}`
                    : window.used_percent !== undefined
                      ? percent.format(window.used_percent / 100)
                      : tx("额度未知")}
            </strong>
            {window.used_percent !== undefined && !window.unknown ? (
              <progress
                max={100}
                value={Math.max(0, Math.min(100, window.used_percent))}
                aria-label={window.name || tx("已用比例")}
              />
            ) : null}
            {window.spent !== undefined ? (
              <p>
                {tx("本周期已观测消费：{amount}").replace(
                  "{amount}",
                  `${number.format(window.spent)} ${window.unit ?? ""}`,
                )}
              </p>
            ) : null}
            {window.amount !== undefined && window.limit !== undefined ? (
              <p>
                {number.format(window.amount)} / {number.format(window.limit)}{" "}
                {window.unit}
              </p>
            ) : null}
            <dl>
              {window.reset_at ? (
                <div>
                  <dt>{tx("重置时间")}</dt>
                  <dd>{allowanceTime(window.reset_at)}</dd>
                </div>
              ) : null}
              {window.expires_at ? (
                <div>
                  <dt>{tx("到期时间")}</dt>
                  <dd>{allowanceTime(window.expires_at)}</dd>
                </div>
              ) : null}
              {window.period_seconds ? (
                <div>
                  <dt>{tx("周期")}</dt>
                  <dd>
                    {new Intl.NumberFormat(languageLocale(), {
                      style: "unit",
                      unit: "hour",
                      unitDisplay: "short",
                      maximumFractionDigits: 1,
                    }).format(window.period_seconds / 3600)}
                  </dd>
                </div>
              ) : null}
              {window.models?.length ? (
                <div>
                  <dt>{tx("适用模型")}</dt>
                  <dd>{window.models.join(", ")}</dd>
                </div>
              ) : null}
              {window.excluded_models?.length ? (
                <div>
                  <dt>{tx("排除模型")}</dt>
                  <dd>{window.excluded_models.join(", ")}</dd>
                </div>
              ) : null}
              {window.pool ? (
                <div>
                  <dt>{tx("共享额度池")}</dt>
                  <dd>{window.pool}</dd>
                </div>
              ) : null}
            </dl>
          </article>
        ))}
      </div>
      {a.credits ? (
        <article className="allowance-entitlement">
          <h4>{tx("积分")}</h4>
          <strong>
            {a.credits.unlimited
              ? tx("无限制")
              : a.credits.balance !== undefined
                ? `${number.format(a.credits.balance)} ${a.credits.unit ?? ""}`
                : a.credits.available === true
                  ? tx("可用")
                  : tx("额度未知")}
          </strong>
        </article>
      ) : null}
      {a.resets ? (
        <article className="allowance-entitlement">
          <h4>{tx("重置卡")}</h4>
          <strong>
            {a.resets.unknown ? tx("详情未知") : number.format(a.resets.count)}
          </strong>
          {(a.resets.cards ?? []).map((card, i) => (
            <p key={card.id ?? i}>
              {card.window || tx("重置卡")} ·{" "}
              {card.expires_at
                ? allowanceTime(card.expires_at)
                : tx("未报告到期时间")}
            </p>
          ))}
        </article>
      ) : null}
    </div>
  );
}

export function AllowanceHistory({ points }: { points: AllowancePoint[] }) {
  const names = Array.from(
    new Set(
      points.flatMap((point) =>
        (point.allowance.windows ?? []).map((window) => window.name || ""),
      ),
    ),
  );
  const [selected, setSelected] = useState("");
  const name = names.includes(selected) ? selected : names[0];
  const series = points
    .map((point) => ({
      point,
      window: point.allowance.windows?.find(
        (window) => (window.name || "") === name,
      ),
    }))
    .filter((item) => item.window && !item.window.unknown)
    .reverse();
  const values = series
    .map((item) => ({
      at: new Date(item.point.fetched_at).getTime(),
      value: item.window!.balance ?? item.window!.used_percent,
      window: item.window!,
    }))
    .filter(
      (item): item is typeof item & { value: number } =>
        item.value !== undefined &&
        Number.isFinite(item.value) &&
        Number.isFinite(item.at),
    );
  const max = Math.max(1, ...values.map((item) => item.value));
  const first = values[0]?.at ?? 0;
  const last = values.at(-1)?.at ?? first;
  const number = new Intl.NumberFormat(languageLocale(), {
    maximumFractionDigits: 2,
  });
  const polyline = values
    .map(
      (item) =>
        `${40 + ((item.at - first) / Math.max(1, last - first)) * 540},${180 - (item.value / max) * 150}`,
    )
    .join(" ");
  let spent = 0;
  let elapsed = 0;
  values.slice(1).forEach((item, index) => {
    const previous = values[index];
    if (
      item.window.balance !== undefined &&
      previous.window.balance !== undefined &&
      previous.value > item.value &&
      item.window.reset_at === previous.window.reset_at
    ) {
      spent += previous.value - item.value;
      elapsed += item.at - previous.at;
    }
  });
  const remaining = values.at(-1);
  const days =
    remaining?.window.balance !== undefined && spent > 0 && elapsed > 0
      ? remaining.value / (spent / elapsed) / 86400000
      : undefined;
  return (
    <section className="allowance-history" aria-label={tx("额度历史")}>
      <p>{tx("保留最近 45 天的变化记录。趋势只用于估算。")}</p>
      {points.length === 0 ? (
        <p>{tx("暂无额度历史。成功采集后会自动记录变化。")}</p>
      ) : (
        <>
          <label>
            {tx("额度窗口")}
            <select
              value={name}
              onChange={(event) => setSelected(event.target.value)}
            >
              {names.map((value) => (
                <option key={value} value={value}>
                  {value || tx("额度窗口")}
                </option>
              ))}
            </select>
          </label>
          <svg viewBox="0 0 600 220" role="img" aria-label={tx("额度变化趋势")}>
            <line x1="40" y1="180" x2="580" y2="180" />
            <line x1="40" y1="30" x2="40" y2="180" />
            <text x="3" y="35">
              {number.format(max)}
            </text>
            <text x="18" y="185">
              0
            </text>
            <polyline points={polyline} fill="none" />
            <text x="40" y="210">
              {values.length
                ? new Intl.DateTimeFormat(languageLocale(), {
                    month: "short",
                    day: "numeric",
                  }).format(first)
                : ""}
            </text>
            <text x="520" y="210">
              {values.length
                ? new Intl.DateTimeFormat(languageLocale(), {
                    month: "short",
                    day: "numeric",
                  }).format(last)
                : ""}
            </text>
          </svg>
          {days !== undefined ? (
            <p>
              {tx("按已观测消耗估算，余额可用约 {days} 天。").replace(
                "{days}",
                number.format(days),
              )}
            </p>
          ) : null}
          <div className="allowance-history-table">
            <table>
              <thead>
                <tr>
                  <th>{tx("采集时间")}</th>
                  <th>{tx("已用比例")}</th>
                  <th>{tx("余额")}</th>
                  <th>{tx("状态")}</th>
                </tr>
              </thead>
              <tbody>
                {series
                  .slice()
                  .reverse()
                  .slice(0, 100)
                  .map(({ point, window }) => (
                    <tr key={point.id}>
                      <td>{allowanceTime(point.fetched_at)}</td>
                      <td>
                        {window?.used_percent !== undefined
                          ? new Intl.NumberFormat(languageLocale(), {
                              style: "percent",
                              maximumFractionDigits: 1,
                            }).format(window.used_percent / 100)
                          : "—"}
                      </td>
                      <td>
                        {window?.balance !== undefined
                          ? `${number.format(window.balance)} ${window.unit ?? ""}`
                          : "—"}
                      </td>
                      <td>{allowanceStatusLabel(point.allowance.status)}</td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

export function allowanceEventKind(kind: string) {
  const labels: Record<string, string> = {
    automatic_reset: tx("自动重置"),
    allowance_high_usage: tx("高用量告警"),
    allowance_low_balance: tx("低余额告警"),
    allowance_unused_before_reset: tx("重置前额度提醒"),
    alert_delivery: tx("通知投递"),
  };
  return labels[kind] ?? kind;
}
export function allowanceEventState(state: string) {
  const labels: Record<string, string> = {
    queued: tx("等待执行"),
    pending: tx("已预留"),
    unknown: tx("结果未知"),
    succeeded: tx("已完成"),
    delivered: tx("已送达"),
    failed: tx("失败"),
    cancelled: tx("已取消"),
    fired: tx("已触发"),
    armed: tx("已恢复"),
  };
  return labels[state] ?? state;
}
export function allowanceEventDetails(event: AllowanceEvent) {
  if (event.kind === "alert_delivery") return "";
  if (event.details === "expiring_card") return tx("重置卡即将到期");
  if (event.details === "weekly_exhausted") return tx("周额度耗尽");
  return event.details;
}
