import type {
  AllowanceConfig,
  AllowancePolicy,
} from "../core/provider-allowance";
import { tx } from "../i18n/runtime";

export function AllowanceNativePolicyFields({
  draft,
  onChange,
}: {
  draft: AllowanceConfig;
  onChange: (value: AllowanceConfig) => void;
}) {
  const p = draft.policy,
    w = p.wallet;
  const zone =
    w.timezone ||
    p.spend_cap?.timezone ||
    Intl.DateTimeFormat().resolvedOptions().timeZone;
  const set = (patch: Partial<AllowancePolicy>) =>
    onChange({ ...draft, policy: { ...p, ...patch } });
  return (
    <>
      <fieldset>
        <legend>{tx("开销上限")}</legend>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={!!p.spend_cap}
            onChange={(event) =>
              set({
                spend_cap: event.target.checked
                  ? { usd: 20, period: "day", timezone: zone }
                  : undefined,
              })
            }
          />
          {tx("限制此 API Key 的开销")}
        </label>
        {p.spend_cap ? (
          <div className="allowance-form-grid">
            <label>
              {tx("开销上限（USD）")}
              <input
                type="number"
                min={0.01}
                max={1000000}
                step="any"
                value={p.spend_cap.usd}
                onChange={(event) =>
                  set({
                    spend_cap: {
                      ...p.spend_cap!,
                      usd: Number(event.target.value),
                    },
                  })
                }
              />
            </label>
            <label>
              {tx("开销周期")}
              <select
                value={p.spend_cap.period}
                onChange={(event) =>
                  set({
                    spend_cap: {
                      ...p.spend_cap!,
                      period: event.target.value as "day" | "month" | "total",
                      since: undefined,
                      timezone: p.spend_cap?.timezone || zone,
                    },
                  })
                }
              >
                <option value="day">{tx("每天")}</option>
                <option value="month">{tx("每月")}</option>
                <option value="total">{tx("总计")}</option>
              </select>
            </label>
          </div>
        ) : null}
        <p>
          {tx(
            "按已记录的供应商成本计算。日、月周期按所选时区重置；总计从启用时开始，修改金额不会清零。",
          )}
        </p>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={p.cap_stop ?? false}
            onChange={(event) => set({ cap_stop: event.target.checked })}
          />
          {tx("所有账号达到限额时停止，不转到后备供应商")}
        </label>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={p.cap_careful ?? false}
            onChange={(event) => set({ cap_careful: event.target.checked })}
          />
          {tx("接近限额时重新查询，额度未知时暂停使用")}
        </label>
      </fieldset>
      <fieldset>
        <legend>{tx("余额补充")}</legend>
        <div className="allowance-form-grid">
          <label>
            {tx("补充周期")}
            <select
              value={
                w.refill?.every || (w.refill_seconds ? "interval" : "none")
              }
              onChange={(event) => {
                if (event.target.value === "interval") return;
                set({
                  wallet: {
                    ...w,
                    refill:
                      event.target.value === "none"
                        ? undefined
                        : {
                            every: event.target.value as "week" | "month",
                            day: 1,
                          },
                    timezone: w.timezone || zone,
                    refill_amount: undefined,
                    refill_seconds: undefined,
                    refill_at: undefined,
                    carry_over: false,
                  },
                });
              }}
            >
              <option value="none">{tx("不自动补充")}</option>
              <option value="week">{tx("每周")}</option>
              <option value="month">{tx("每月")}</option>
              {w.refill_seconds ? (
                <option value="interval">{tx("固定间隔")}</option>
              ) : null}
            </select>
          </label>
          {w.refill ? (
            <label>
              {w.refill.every === "week"
                ? tx("补充星期（1 为周一，7 为周日）")
                : tx("每月补充日")}
              <input
                type="number"
                min={1}
                max={w.refill.every === "week" ? 7 : 31}
                value={w.refill.day}
                onChange={(event) =>
                  set({
                    wallet: {
                      ...w,
                      refill: { ...w.refill!, day: Number(event.target.value) },
                    },
                  })
                }
              />
            </label>
          ) : null}
          <label>
            {tx("余额与开销时区")}
            <input
              value={w.timezone || p.spend_cap?.timezone || zone}
              onChange={(event) =>
                set({
                  wallet: { ...w, timezone: event.target.value },
                  spend_cap: p.spend_cap
                    ? { ...p.spend_cap, timezone: event.target.value }
                    : undefined,
                })
              }
            />
          </label>
        </div>
        <p>
          {tx(
            "在当天零点恢复为设置的余额，不结转上期余额。月份没有指定日期时，使用当月最后一天。",
          )}
        </p>
      </fieldset>
    </>
  );
}
