import { useEffect, useRef, useState } from "react";
import { type AppData, type ViewKey } from "../core/types";
import { formatNumber, formatTime } from "../domain/formatting";
import { formatTranslationTemplate, tx } from "../i18n/runtime";

export function OverviewStatus({ data, onRefresh }: { data: AppData; onRefresh?: () => Promise<void> }) {
  const refresh = useRef(onRefresh);
  useEffect(() => { refresh.current = onRefresh; }, [onRefresh]);
  const status = data.summaryStatus;
  const available = useOverviewSummaryAvailable(status);
  useEffect(() => {
    if (status?.state !== "preparing") return;
    let stopped = false;
    let timer = setTimeout(poll, 3000);
    async function poll() {
      await refresh.current?.();
      if (!stopped) timer = setTimeout(poll, 3000);
    }
    return () => { stopped = true; clearTimeout(timer); };
  }, [status?.state]);
  useEffect(() => {
    if (status?.state !== "stale" || !status.updated_at) return;
    const delay = Math.max(0, new Date(status.updated_at).getTime() + 60000 - Date.now());
    const timer = setTimeout(() => refresh.current?.(), delay);
    return () => clearTimeout(timer);
  }, [status?.state, status?.updated_at]);
  if (!status || status.state === "ready") return null;
  let message = tx("统计暂时不可用，请稍后刷新");
  if (status.state === "preparing") {
    message = status.processed === undefined ? tx("历史统计准备中") : formatTranslationTemplate(tx("历史统计准备中，已处理 {count} 条记录"), { count: formatNumber(status.processed) });
  } else if (status.state === "stale" && available) {
    message = formatTranslationTemplate(tx("统计刷新失败，显示 {time} 的结果"), { time: formatTime(status.updated_at ?? "") });
  }
  return <div role="status" className="inline-notice">{message}</div>;
}

export function OverviewRecentAlerts({ data, onSelectView }: { data: AppData; onSelectView: (view: ViewKey) => void }) {
  return <section className="overview-panel overview-recent-alerts">
    <div className="overview-panel-head"><h2>{tx("最近告警")}</h2><button className="text-button" type="button" onClick={() => onSelectView("alert-events")}>{tx("查看全部")}</button></div>
    {data.alerts.length === 0 ? <p>{tx("暂无告警")}</p> : <ul>{data.alerts.map((alert) => <li key={alert.id}><time>{formatTime(alert.created_at)}</time><span>{alert.code} · {alert.message}</span></li>)}</ul>}
  </section>;
}

export function useOverviewSummaryAvailable(status: AppData["summaryStatus"]) {
  const [expired, setExpired] = useState(false);
  const deadline = status?.updated_at ? new Date(status.updated_at).getTime() + 60000 : 0;
  useEffect(() => {
    setExpired(false);
    if (status?.state !== "stale") return;
    const timer = setTimeout(() => setExpired(true), Math.max(0, deadline - Date.now()));
    return () => clearTimeout(timer);
  }, [status?.state, deadline]);
  return !status || status.state === "ready" || (status.state === "stale" && !expired && Date.now() < deadline);
}
