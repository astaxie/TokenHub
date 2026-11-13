import type { AllowanceFieldRow } from "../core/provider-allowance-rows";
import type { AdminResource } from "../core/types";
import type {
  AllowanceCap,
  AllowanceConfig,
  AllowancePolicy,
} from "../core/provider-allowance";
import { tx } from "../i18n/runtime";

function localDate(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}
function isoDate(value: string) {
  return value ? new Date(value).toISOString() : undefined;
}

export function AllowancePolicyFields({
  draft,
  channels,
  onChange,
  caps,
  onCapsChange,
}: {
  draft: AllowanceConfig;
  caps: AllowanceFieldRow<AllowanceCap>[];
  onCapsChange: (rows: AllowanceFieldRow<AllowanceCap>[]) => void;
  channels: AdminResource[];
  onChange: (value: AllowanceConfig) => void;
}) {
  const p = draft.policy;
  const set = (patch: Partial<AllowancePolicy>) =>
    onChange({ ...draft, policy: { ...p, ...patch } });
  const wallet = p.wallet;
  return (
    <>
      <fieldset>
        <legend>{tx("账号使用上限")}</legend>
        <p>
          {tx("达到用户限额时停用该账号，不会自动使用积分或重置卡绕过限额。")}
        </p>
        <div className="allowance-form-grid">
          <label>
            {tx("全局已用比例上限（%）")}
            <input
              type="number"
              min={0}
              max={100}
              value={p.cap.percent ?? ""}
              placeholder={tx("不限制")}
              onChange={(event) =>
                set({ cap: { ...p.cap, percent: Number(event.target.value) } })
              }
            />
          </label>
          <label>
            {tx("全局已用金额上限")}
            <input
              type="number"
              min={0}
              step="any"
              value={p.cap.amount ?? ""}
              placeholder={tx("不限制")}
              onChange={(event) =>
                set({ cap: { ...p.cap, amount: Number(event.target.value) } })
              }
            />
          </label>
        </div>
        {caps.map((row) => (
          <div className="allowance-inline" key={row.id}>
            <input
              aria-label={tx("窗口名称")}
              value={row.name}
              onChange={(event) =>
                onCapsChange(
                  caps.map((item) =>
                    item.id === row.id
                      ? { ...item, name: event.target.value }
                      : item,
                  ),
                )
              }
            />
            <input
              aria-label={tx("窗口比例上限")}
              type="number"
              min={0}
              max={100}
              value={row.value.percent ?? ""}
              onChange={(event) =>
                onCapsChange(
                  caps.map((item) =>
                    item.id === row.id
                      ? {
                          ...item,
                          value: {
                            ...item.value,
                            percent: Number(event.target.value),
                          },
                        }
                      : item,
                  ),
                )
              }
            />
            <input
              aria-label={tx("窗口金额上限")}
              type="number"
              min={0}
              step="any"
              value={row.value.amount ?? ""}
              onChange={(event) =>
                onCapsChange(
                  caps.map((item) =>
                    item.id === row.id
                      ? {
                          ...item,
                          value: {
                            ...item.value,
                            amount: Number(event.target.value),
                          },
                        }
                      : item,
                  ),
                )
              }
            />
            <button
              type="button"
              onClick={() =>
                onCapsChange(caps.filter((item) => item.id !== row.id))
              }
            >
              {tx("删除")}
            </button>
          </div>
        ))}
        <button
          type="button"
          className="secondary"
          onClick={() =>
            onCapsChange([
              ...caps,
              { id: crypto.randomUUID(), name: "", value: {} },
            ])
          }
        >
          {tx("添加窗口限额")}
        </button>
      </fieldset>
      <fieldset>
        <legend>{tx("积分与重置卡")}</legend>
        <label>
          {tx("积分使用策略")}
          <select
            value={p.credits ?? ""}
            onChange={(event) => set({ credits: event.target.value })}
          >
            <option value="">{tx("沿用原有行为")}</option>
            <option value="never">{tx("禁止使用积分")}</option>
            <option value="allow">{tx("允许使用积分")}</option>
            <option value="first">{tx("积分优先")}</option>
            <option value="last">{tx("积分最后使用")}</option>
          </select>
        </label>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={p.reset.on_exhausted}
            onChange={(event) =>
              set({ reset: { ...p.reset, on_exhausted: event.target.checked } })
            }
          />
          {tx("周额度耗尽且无获准候选时自动使用重置卡")}
        </label>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={p.reset.before_expiry}
            onChange={(event) =>
              set({
                reset: { ...p.reset, before_expiry: event.target.checked },
              })
            }
          />
          {tx("重置卡到期前自动使用")}
        </label>
        <div className="allowance-form-grid">
          <label>
            {tx("到期前提前量（秒）")}
            <input
              type="number"
              min={0}
              max={86400}
              value={p.reset.lead_seconds || 1800}
              onChange={(event) =>
                set({
                  reset: {
                    ...p.reset,
                    lead_seconds: Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label>
            {tx("每周自动重置次数上限")}
            <input
              type="number"
              min={1}
              max={100}
              value={p.reset.weekly_limit || 1}
              onChange={(event) =>
                set({
                  reset: {
                    ...p.reset,
                    weekly_limit: Number(event.target.value),
                  },
                })
              }
            />
          </label>
        </div>
        <p>
          {tx(
            "自动重置默认关闭，仅支持具有幂等执行记录的 Codex 重置卡。结果未知时停止自动重试。",
          )}
        </p>
      </fieldset>
      <fieldset>
        <legend>{tx("额度告警")}</legend>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={p.alerts.enabled}
            onChange={(event) =>
              set({ alerts: { ...p.alerts, enabled: event.target.checked } })
            }
          />
          {tx("启用额度告警")}
        </label>
        <div className="allowance-form-grid">
          <label>
            {tx("高用量阈值（%）")}
            <input
              type="number"
              min={1}
              max={100}
              value={p.alerts.used_percent || 90}
              onChange={(event) =>
                set({
                  alerts: {
                    ...p.alerts,
                    used_percent: Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label>
            {tx("低余额阈值")}
            <input
              type="number"
              min={0}
              step="any"
              value={p.alerts.low_balance ?? ""}
              onChange={(event) =>
                set({
                  alerts: {
                    ...p.alerts,
                    low_balance:
                      event.target.value === ""
                        ? undefined
                        : Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label>
            {tx("额度重置前提醒（秒）")}
            <input
              type="number"
              min={0}
              max={86400}
              value={p.alerts.unused_before_reset_seconds ?? ""}
              onChange={(event) =>
                set({
                  alerts: {
                    ...p.alerts,
                    unused_before_reset_seconds: Number(event.target.value),
                  },
                })
              }
            />
          </label>
        </div>
        <p>{tx("同一周期只提醒一次；用量下降或充值后重新启用提醒。")}</p>
        <div className="allowance-channel-options">
          {channels
            .filter((channel) => channel.status === "active")
            .map((channel) => (
              <label className="allowance-check" key={channel.id}>
                <input
                  type="checkbox"
                  checked={(p.alerts.channel_ids ?? []).includes(channel.id)}
                  onChange={(event) =>
                    set({
                      alerts: {
                        ...p.alerts,
                        channel_ids: event.target.checked
                          ? [...(p.alerts.channel_ids ?? []), channel.id]
                          : (p.alerts.channel_ids ?? []).filter(
                              (id) => id !== channel.id,
                            ),
                      },
                    })
                  }
                />
                {channel.name}
              </label>
            ))}
        </div>
        <small>{tx("未选择通知渠道时，仅记录到告警事件。")}</small>
      </fieldset>
      <fieldset>
        <legend>{tx("手动余额与补充额度")}</legend>
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={wallet.enabled}
            onChange={(event) =>
              set({
                wallet: {
                  ...wallet,
                  enabled: event.target.checked,
                  as_of: wallet.as_of ?? new Date().toISOString(),
                  unit: wallet.unit || "USD",
                },
              })
            }
          />
          {tx("启用手动余额估算")}
        </label>
        <p>
          {tx("按 TokenHub 记录的供应商成本扣减，只反映经本网关产生的消费。")}
        </p>
        <div className="allowance-form-grid">
          <label>
            {tx("起始余额")}
            <input
              type="number"
              min={0}
              step="any"
              value={wallet.balance}
              onChange={(event) =>
                set({
                  wallet: { ...wallet, balance: Number(event.target.value) },
                })
              }
            />
          </label>
          <label>
            {tx("余额单位")}
            <input
              value={wallet.unit}
              onChange={(event) =>
                set({ wallet: { ...wallet, unit: event.target.value } })
              }
            />
          </label>
          <label>
            {tx("余额基准时间")}
            <input
              type="datetime-local"
              value={localDate(wallet.as_of)}
              onChange={(event) =>
                set({
                  wallet: { ...wallet, as_of: isoDate(event.target.value) },
                })
              }
            />
          </label>
          <label>
            {tx("每美元折算单位")}
            <input
              type="number"
              min={0}
              step="any"
              value={wallet.cost_per_usd ?? ""}
              placeholder="1"
              onChange={(event) =>
                set({
                  wallet: {
                    ...wallet,
                    cost_per_usd: Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label>
            {tx("定期补充金额")}
            <input
              type="number"
              min={0}
              step="any"
              value={wallet.refill_amount ?? ""}
              onChange={(event) =>
                set({
                  wallet: {
                    ...wallet,
                    refill_amount: Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label>
            {tx("补充间隔（秒）")}
            <input
              type="number"
              min={3600}
              value={wallet.refill_seconds ?? ""}
              onChange={(event) =>
                set({
                  wallet: {
                    ...wallet,
                    refill_seconds: Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label>
            {tx("下次补充时间")}
            <input
              type="datetime-local"
              value={localDate(wallet.refill_at)}
              onChange={(event) =>
                set({
                  wallet: { ...wallet, refill_at: isoDate(event.target.value) },
                })
              }
            />
          </label>
          <label className="allowance-check">
            <input
              type="checkbox"
              checked={wallet.carry_over}
              onChange={(event) =>
                set({ wallet: { ...wallet, carry_over: event.target.checked } })
              }
            />
            {tx("补充时保留未用余额")}
          </label>
        </div>
        {(wallet.grants ?? []).map((grant, index) => (
          <div className="allowance-inline" key={grant.id}>
            <input
              aria-label={tx("补充额度金额")}
              type="number"
              min={0}
              step="any"
              value={grant.amount}
              onChange={(event) =>
                set({
                  wallet: {
                    ...wallet,
                    grants: wallet.grants?.map((item, i) =>
                      i === index
                        ? { ...item, amount: Number(event.target.value) }
                        : item,
                    ),
                  },
                })
              }
            />
            <input
              aria-label={tx("补充额度到期时间")}
              type="datetime-local"
              value={localDate(grant.expires_at)}
              onChange={(event) =>
                set({
                  wallet: {
                    ...wallet,
                    grants: wallet.grants?.map((item, i) =>
                      i === index
                        ? { ...item, expires_at: isoDate(event.target.value) }
                        : item,
                    ),
                  },
                })
              }
            />
            <button
              type="button"
              onClick={() =>
                set({
                  wallet: {
                    ...wallet,
                    grants: wallet.grants?.filter((_, i) => i !== index),
                  },
                })
              }
            >
              {tx("删除")}
            </button>
          </div>
        ))}
        <button
          className="secondary"
          type="button"
          onClick={() =>
            set({
              wallet: {
                ...wallet,
                grants: [
                  ...(wallet.grants ?? []),
                  { id: crypto.randomUUID(), amount: 0 },
                ],
              },
            })
          }
        >
          {tx("添加补充额度")}
        </button>
      </fieldset>
    </>
  );
}
