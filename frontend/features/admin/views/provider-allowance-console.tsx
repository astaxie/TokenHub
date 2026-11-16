"use client";

import { RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useId, useState } from "react";
import type { ApiContext, Provider } from "../core/types";
import type {
  AllowanceEntry,
  AllowancePoint,
  AllowancePreset,
  AllowanceEvent,
} from "../core/provider-allowance";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { ProviderAllowanceConfigForm } from "./provider-allowance-config";
import {
  AllowanceHistory,
  AllowanceWindows,
  allowanceStatusLabel,
  allowanceTime,
  allowanceEventKind,
  allowanceEventState,
  allowanceEventDetails,
} from "./provider-allowance-details";

export function ProviderAllowanceConsole({
  api,
  providers,
  canWrite = () => true,
}: {
  api: ApiContext;
  providers: Provider[];
  canWrite?: (providerID: string) => boolean;
}) {
  const detailID = useId();
  const [rows, setRows] = useState<AllowanceEntry[]>([]);
  const [presets, setPresets] = useState<AllowancePreset[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [selected, setSelected] = useState("");
  const [tab, setTab] = useState<"windows" | "history" | "config" | "events">(
    "windows",
  );
  const [history, setHistory] = useState<AllowancePoint[]>([]);
  const [events, setEvents] = useState<AllowanceEvent[]>([]);
  const [detailError, setDetailError] = useState("");
  const [detailLoading, setDetailLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [list, options] = await Promise.all([
        adminFetch(api, "/api/admin/provider-allowances"),
        adminFetch(api, "/api/admin/provider-allowances/presets"),
      ]);
      if (!list.ok)
        throw new Error(await readAdminError(list, tx("额度读取失败")));
      if (!options.ok)
        throw new Error(await readAdminError(options, tx("额度读取失败")));
      const response = (await list.json()) as { data: AllowanceEntry[] };
      setRows(response.data);
      setPresets(await options.json());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : tx("额度读取失败"));
    } finally {
      setLoading(false);
    }
  }, [api]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!selected || (tab !== "history" && tab !== "events")) return;
    let active = true;
    setDetailLoading(true);
    setDetailError("");
    setHistory([]);
    setEvents([]);
    void (async () => {
      try {
        const response = await adminFetch(
          api,
          `/api/admin/provider-resources/${encodeURIComponent(selected)}/allowance/${tab}`,
        );
        if (!response.ok)
          throw new Error(await readAdminError(response, tx("额度读取失败")));
        const result = await response.json();
        if (active) {
          if (tab === "history") setHistory(result.data);
          else setEvents(result.data);
        }
      } catch (cause) {
        if (active)
          setDetailError(
            cause instanceof Error ? cause.message : tx("额度读取失败"),
          );
      } finally {
        if (active) setDetailLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, [api, selected, tab]);
  const providerIDs = new Set(providers.map((provider) => provider.id));
  const scoped = rows.filter((row) => providerIDs.has(row.provider_id));
  const visible = scoped.filter(
    (row) =>
      (!status || row.status === status) &&
      `${row.provider_name} ${row.resource_name}`
        .toLowerCase()
        .includes(query.toLowerCase()),
  );
  const entry = scoped.find((row) => row.resource_id === selected);
  async function refresh(row: AllowanceEntry) {
    setRefreshing(true);
    setDetailError("");
    try {
      if (row.config.source.enabled || !row.estimated) {
        const response = await adminFetch(
          api,
          `/api/admin/provider-resources/${encodeURIComponent(row.resource_id)}/allowance/refresh`,
          { method: "POST" },
        );
        if (!response.ok)
          throw new Error(await readAdminError(response, tx("套餐查询失败")));
      }
      await load();
    } catch (cause) {
      setDetailError(
        cause instanceof Error ? cause.message : tx("套餐查询失败"),
      );
      await load();
    } finally {
      setRefreshing(false);
    }
  }
  return (
    <section className="allowance-console" aria-label={tx("额度与权益")}>
      <div className="allowance-heading">
        <div>
          <h2>{tx("额度与权益")}</h2>
          <p>{tx("查看余额、周期额度、积分与重置卡，配置采集和使用策略。")}</p>
        </div>
        <button
          className="secondary"
          type="button"
          onClick={() => void load()}
          disabled={loading}
        >
          <RefreshCw size={16} />
          {tx("重新读取")}
        </button>
      </div>
      <div
        className="allowance-summary"
        role="group"
        aria-label={tx("额度状态筛选")}
      >
        <button
          aria-pressed={!status}
          onClick={() => setStatus("")}
          type="button"
        >
          <strong>
            {new Intl.NumberFormat(languageLocale()).format(scoped.length)}
          </strong>
          {tx("全部账号")}
        </button>
        {(["available", "exhausted", "unknown", "query_error"] as const).map(
          (value) => (
            <button
              data-status={value}
              key={value}
              aria-pressed={status === value}
              type="button"
              onClick={() => setStatus(status === value ? "" : value)}
            >
              <strong>
                {new Intl.NumberFormat(languageLocale()).format(
                  scoped.filter((row) => row.status === value).length,
                )}
              </strong>
              {allowanceStatusLabel(value)}
            </button>
          ),
        )}
      </div>
      {error ? (
        <div role="alert" className="error-banner">
          {error}
        </div>
      ) : null}
      <label className="allowance-search">
        <Search size={16} />
        <input
          aria-label={tx("搜索额度账号")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={tx("搜索额度账号")}
        />
      </label>
      {loading ? <p role="status">{tx("正在读取额度缓存…")}</p> : null}
      <div className="allowance-layout">
        <div className="allowance-accounts" aria-label={tx("额度账号")}>
          {!loading && visible.length === 0 ? (
            <p>{tx("没有符合条件的账号。")}</p>
          ) : null}
          {visible.map((row) => (
            <button
              className={`allowance-account${selected === row.resource_id ? " selected" : ""}`}
              type="button"
              key={row.resource_id}
              aria-pressed={selected === row.resource_id}
              onClick={() => {
                setSelected(row.resource_id);
                setTab("windows");
                setDetailError("");
              }}
            >
              <span>
                <strong>{row.resource_name}</strong>
                <small>{row.provider_name}</small>
              </span>
              <span className={`allowance-status ${row.status}`}>
                {allowanceStatusLabel(row.status)}
              </span>
              <span className="allowance-account-meta">
                {row.allowance.plan ||
                  row.config.source.preset ||
                  tx("自定义额度")}
                {row.estimated ? ` · ${tx("估算余额")}` : ""}
              </span>
              <small>{allowanceTime(row.allowance.checked_at)}</small>
            </button>
          ))}
        </div>
        <div className="allowance-detail">
          {entry ? (
            <>
              <div className="allowance-heading">
                <div>
                  <h3>{entry.resource_name}</h3>
                  <p>{entry.provider_name}</p>
                </div>
                <button
                  className="secondary"
                  type="button"
                  disabled={refreshing || !canWrite(entry.provider_id)}
                  onClick={() => void refresh(entry)}
                >
                  <RefreshCw size={16} />
                  {refreshing ? tx("正在刷新…") : tx("刷新上游额度")}
                </button>
              </div>
              <div
                className="allowance-tabs"
                role="tablist"
                aria-label={tx("账号额度详情")}
                onKeyDown={(event) => {
                  const buttons = Array.from(
                    event.currentTarget.querySelectorAll<HTMLButtonElement>(
                      '[role="tab"]',
                    ),
                  );
                  const index = buttons.indexOf(
                    document.activeElement as HTMLButtonElement,
                  );
                  let next = index;
                  if (event.key === "ArrowRight")
                    next = (index + 1) % buttons.length;
                  else if (event.key === "ArrowLeft")
                    next = (index - 1 + buttons.length) % buttons.length;
                  else if (event.key === "Home") next = 0;
                  else if (event.key === "End") next = buttons.length - 1;
                  else return;
                  event.preventDefault();
                  buttons[next]?.click();
                  buttons[next]?.focus();
                }}
              >
                {(
                  [
                    ["windows", tx("额度窗口")],
                    ["history", tx("额度历史")],
                    ["config", tx("采集与权益策略")],
                    ["events", tx("权益与告警记录")],
                  ] as const
                ).map(([value, label]) => (
                  <button
                    type="button"
                    key={value}
                    role="tab"
                    id={`${detailID}-${value}-tab`}
                    aria-controls={`${detailID}-panel`}
                    tabIndex={tab === value ? 0 : -1}
                    aria-selected={tab === value}
                    onClick={() => setTab(value)}
                  >
                    {label}
                  </button>
                ))}
              </div>
              <div
                role="tabpanel"
                id={`${detailID}-panel`}
                aria-labelledby={`${detailID}-${tab}-tab`}
                tabIndex={0}
              >
                {detailError ? (
                  <div className="error-banner" role="alert">
                    {detailError}
                  </div>
                ) : null}
                {detailLoading ? (
                  <p role="status">{tx("正在读取额度缓存…")}</p>
                ) : null}
                {tab === "windows" ? <AllowanceWindows entry={entry} /> : null}
                {tab === "history" && !detailLoading ? (
                  <AllowanceHistory points={history} />
                ) : null}
                {tab === "events" && !detailLoading ? (
                  <div className="allowance-events">
                    {events.length === 0 ? (
                      <p>{tx("暂无权益或告警记录。")}</p>
                    ) : (
                      events.map((event) => (
                        <article key={event.id}>
                          <strong>{allowanceEventKind(event.kind)}</strong>
                          <span>{allowanceEventState(event.state)}</span>
                          <p>{allowanceEventDetails(event)}</p>
                          <small>{allowanceTime(event.updated_at)}</small>
                        </article>
                      ))
                    )}
                  </div>
                ) : null}
                {tab === "config" ? (
                  <ProviderAllowanceConfigForm
                    key={`${entry.resource_id}:${entry.config.revision}`}
                    api={api}
                    readOnly={!canWrite(entry.provider_id)}
                    entry={entry}
                    entries={scoped.filter(
                      (row) =>
                        row.provider_id === entry.provider_id &&
                        row.resource_id !== entry.resource_id,
                    )}
                    presets={presets}
                    onSaved={load}
                  />
                ) : null}
              </div>
            </>
          ) : (
            <div className="allowance-empty">
              {tx("选择一个账号，查看额度或配置采集。")}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
