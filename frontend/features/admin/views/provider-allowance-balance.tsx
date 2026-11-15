import { useId, useState } from "react";
import type {
  AllowanceConfig,
  AllowanceQueryResult,
  AllowanceSource,
} from "../core/provider-allowance";
import {
  importMagpieBalance,
  magpieBalancePlaceholder,
} from "../core/magpie-balance";
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
  const [importText, setImportText] = useState("");
  const [importError, setImportError] = useState("");
  const source = draft.source;
  const set = (patch: Partial<AllowanceSource>) =>
    onChange({ ...draft, source: { ...source, ...patch } });
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
            placeholder="https://fusecode.ai/v1/usage"
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
            })
          }
          placeholder={
            draft.has_token && !draft.clear_token
              ? tx("已保存，留空保留")
              : tx("可选：账户的系统访问令牌；留空使用 API Key")
          }
        />
      </div>
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
      <details>
        <summary>{tx("导入 Magpie 余额配置")}</summary>
        <label>
          {tx("Magpie 供应商 JSON")}
          <textarea
            rows={4}
            value={importText}
            onChange={(event) => setImportText(event.target.value)}
            placeholder={
              '{"balanceURL":"https://fusecode.ai/v1/usage","balancePath":"$remaining","balanceMethod":"GET"}'
            }
          />
        </label>
        <small>
          {tx(
            "粘贴一个供应商的 JSON。只导入余额地址、方法、字段、账户令牌和请求头。",
          )}
        </small>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            try {
              onChange(importMagpieBalance(importText, draft));
              setImportText("");
              setImportError("");
            } catch {
              setImportError(
                tx(
                  "请输入一个含 balanceURL 的 Magpie 供应商 JSON；令牌和请求头须为原始字符串。",
                ),
              );
            }
          }}
        >
          {tx("填入余额配置")}
        </button>
        {importError ? (
          <p role="alert" className="error-banner">
            {importError}
          </p>
        ) : null}
      </details>
    </section>
  );
}
