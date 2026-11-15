import { useId } from "react";
import type {
  AllowanceConfig,
  AllowanceQueryResult,
  AllowanceSource,
} from "../core/provider-allowance";
import { magpieBalancePlaceholder } from "../core/magpie-balance";
import { tx } from "../i18n/runtime";

export function AllowanceBalanceFields({
  draft,
  onChange,
  onQuery,
  querying,
  result,
  queryError,
}: {
  draft: AllowanceConfig;
  onChange: (value: AllowanceConfig) => void;
  onQuery: () => void;
  querying: boolean;
  result?: AllowanceQueryResult;
  queryError: string;
}) {
  const id = useId();
  const source = draft.source;
  const set = (patch: Partial<AllowanceSource>) =>
    onChange({
      ...draft,
      source: {
        ...source,
        ...patch,
        enabled: patch.url?.trim() ? true : source.enabled,
        auth: source.auth || "magpie",
      },
    });
  return (
    <section className="allowance-balance" aria-label={tx("余额配置")}>
      <div className="allowance-balance-row">
        <label htmlFor={`${id}-url`}>{tx("余额查询地址")}</label>
        <div className="allowance-balance-pair">
          <div
            className="allowance-method"
            role="group"
            aria-label={tx("余额请求方法")}
          >
            {["GET", "POST"].map((method) => (
              <button
                key={method}
                type="button"
                aria-pressed={
                  (source.method || "GET").trim().toUpperCase() === method
                }
                onClick={() => set({ method })}
              >
                {method}
              </button>
            ))}
          </div>
          <input
            id={`${id}-url`}
            type="url"
            value={source.url ?? ""}
            onChange={(event) => set({ url: event.target.value })}
            placeholder={tx("留空自动识别余额接口")}
          />
        </div>
      </div>
      <div className="allowance-balance-row">
        <label htmlFor={`${id}-field`}>{tx("余额字段")}</label>
        <div className="allowance-balance-pair">
          <input
            id={`${id}-field`}
            value={source.expression ?? ""}
            onChange={(event) => set({ expression: event.target.value })}
            placeholder={magpieBalancePlaceholder(source.url)}
          />
          <button
            type="button"
            className="secondary"
            disabled={querying || source.auth === "alibaba"}
            onClick={onQuery}
          >
            {querying ? tx("正在查询…") : tx("查询余额")}
          </button>
        </div>
      </div>
      {queryError ? (
        <p role="alert" className="error-banner">
          {queryError}
        </p>
      ) : null}
      {result ? (
        <output
          className="allowance-balance-result"
          aria-label={tx("余额查询结果")}
        >
          {tx("余额")}{" "}
          {result.balance_parts
            ?.map((part) => [part.label, part.text].filter(Boolean).join(" "))
            .join(" · ") || tx("查询完成，请保存后刷新额度详情。")}
        </output>
      ) : null}
      <div className="allowance-balance-row">
        <label htmlFor={`${id}-token`}>{tx("账户余额")}</label>
        <input
          id={`${id}-token`}
          type="password"
          autoComplete="new-password"
          value={draft.token ?? ""}
          onChange={(event) =>
            onChange({
              ...draft,
              token: event.target.value,
              clear_token: false,
              source: {
                ...source,
                auth: ["volcengine", "tencent", "alibaba"].includes(
                  source.auth ?? "",
                )
                  ? source.auth
                  : "magpie",
              },
            })
          }
          placeholder={
            draft.has_token && !draft.clear_token
              ? tx("已保存，留空保留")
              : tx("可选：账户的系统访问令牌；留空使用 API Key")
          }
        />
      </div>
      {result?.query_url && !source.url ? (
        <button
          type="button"
          className="secondary"
          onClick={() =>
            set({
              url: result.query_url,
              expression: source.expression || result.query_expression,
            })
          }
        >
          {tx("使用已找到的余额查询地址")}
        </button>
      ) : null}
      <label className="allowance-check">
        <input
          type="checkbox"
          checked={source.no_balance_probe ?? false}
          onChange={(event) => set({ no_balance_probe: event.target.checked })}
        />
        {tx("不自动探测余额接口")}
      </label>
      {draft.has_token ? (
        <label className="allowance-check">
          <input
            type="checkbox"
            checked={draft.clear_token ?? false}
            onChange={(event) =>
              onChange({ ...draft, clear_token: event.target.checked })
            }
          />
          {tx("删除账户余额令牌")}
        </label>
      ) : null}
      <small>
        {tx(
          "按当前表单查询，无需保存。查询结果不会修改已保存的额度或使用策略。",
        )}
      </small>
      <details>
        <summary>{tx("余额字段语法")}</summary>
        <p>
          {tx(
            "填写 JSON 字段路径，支持 + - * /、括号。前加 $、¥、€、£ 显示币种，后加 % 显示百分比；多段用 ; 分隔，可加标签。数组支持 a.*.used、a[status=active].used，以及 sum、max、min、count。字段缺失显示 —，其他字段照常显示。",
          )}
        </p>
        <code>
          $remaining; 周: subscription.weekly_usage_usd /
          subscription.weekly_limit_usd %; 月: subscription.monthly_usage_usd /
          subscription.monthly_limit_usd %
        </code>
      </details>
    </section>
  );
}
