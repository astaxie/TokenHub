import { useState } from "react";
import type { UsagePoint } from "../core/types";
import type { UsageReport } from "../core/usage-report-types";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";

function count(value: number, compact = false) {
  return new Intl.NumberFormat(languageLocale(), compact ? { notation: "compact", maximumFractionDigits: 1 } : {}).format(value);
}

export function UsageReportMetrics({ report }: { report: UsageReport }) {
  const { summary } = report;
  const cached = summary.cached_input_tokens ?? 0;
  const hitRate = new Intl.NumberFormat(languageLocale(), { style: "percent", maximumFractionDigits: 1 }).format(summary.input_tokens > 0 ? Math.min(1, Math.max(0, cached / summary.input_tokens)) : 0);
  const metrics = [
    { label: tx("Token 消耗"), value: summary.total_tokens, detail: formatTranslationTemplate(tx("输入 {input} · 输出 {output}"), { input: count(summary.input_tokens, true), output: count(summary.output_tokens, true) }) },
    { label: tx("缓存读取"), value: cached, detail: formatTranslationTemplate(tx("缓存命中率 {rate}"), { rate: hitRate }) },
    { label: tx("推理 Token"), value: summary.reasoning_output_tokens ?? 0, detail: tx("已计入输出 Token") },
    { label: tx("请求数"), value: summary.request_count, detail: formatTranslationTemplate(tx("错误 {count} 次"), { count: count(summary.errors) }) },
  ];
  return <section className="global-usage-metrics global-usage-stats" aria-label={tx("用量概览")}>
    {metrics.map((metric) => <div className="global-usage-metric" key={metric.label} role="group" aria-label={metric.label}>
      <strong title={count(metric.value)}>{count(metric.value, true)}</strong>
      <span>{metric.label}</span>
      <small>{metric.detail}</small>
    </div>)}
  </section>;
}

function pointLabel(point: UsagePoint, report: UsageReport, compact: boolean) {
  const hourly = report.granularity === "hour";
  const date = new Date(hourly ? point.date : `${point.date.slice(0, 10)}T00:00:00Z`);
  if (!Number.isFinite(date.getTime())) return point.date;
  const options: Intl.DateTimeFormatOptions = hourly
    ? (compact ? { hour: "2-digit", minute: "2-digit" } : { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZoneName: "shortOffset" })
    : report.granularity === "month" ? { year: "numeric", month: "short" } : { month: "short", day: "numeric", ...(compact ? {} : { year: "numeric" }) };
  return new Intl.DateTimeFormat(languageLocale(), { ...options, timeZone: hourly ? report.timezone : "UTC" }).format(date);
}

function pointDescription(point: UsagePoint, report: UsageReport) {
  return formatTranslationTemplate(tx("{time}：输入 {input}，输出 {output}，请求 {requests}"), {
    time: pointLabel(point, report, false), input: count(point.input_tokens), output: count(point.output_tokens), requests: count(point.request_count),
  });
}

export function UsageReportChart({ report }: { report: UsageReport }) {
  const [activeDate, setActiveDate] = useState<string | null>(null);
  const points = report.timeseries;
  const activePoint = points.find((point) => point.date === activeDate);
  const max = Math.max(1, ...points.map((point) => point.input_tokens + point.output_tokens));
  const hasRequests = points.some((point) => point.request_count > 0 || point.total_tokens > 0);
  const labelInterval = Math.max(1, Math.ceil((points.length - 1) / 4));
  return <section className="global-usage-chart" aria-label={tx("Token 用量趋势")}>
    <header className="global-usage-chart-head">
      <div><h2>{tx("Token 用量趋势")}</h2><p>{formatTranslationTemplate(tx("统计时区：{timezone}"), { timezone: report.timezone })}</p></div>
      <div className="global-usage-legend"><span><i className="input" />{tx("输入")}</span><span><i className="output" />{tx("输出")}</span></div>
    </header>
    {!hasRequests ? <div className="global-usage-chart-empty">{tx("此时间范围内暂无用量")}</div> : <>
      <div className="global-usage-chart-scale" aria-hidden="true">{count(max, true)}</div>
      <div className="global-usage-chart-scroll">
        <div className="global-usage-bars" style={{ gridTemplateColumns: `repeat(${points.length}, minmax(8px, 1fr))` }}>
          {points.map((point, index) => <div className="global-usage-bar-column" key={point.date}>
            <button type="button" className="global-usage-bar" aria-label={pointDescription(point, report)} aria-pressed={activeDate === point.date} onFocus={() => setActiveDate(point.date)} onMouseEnter={() => setActiveDate(point.date)} onClick={() => setActiveDate(point.date)}>
              <span className="global-usage-bar-output" aria-hidden="true" style={{ height: `${point.output_tokens / max * 100}%` }} />
              <span className="global-usage-bar-input" aria-hidden="true" style={{ height: `${point.input_tokens / max * 100}%` }} />
            </button>
            <span className={`global-usage-bar-label${index === 0 ? " first" : index === points.length - 1 ? " last" : ""}`} aria-hidden="true">{index % labelInterval === 0 || index === points.length - 1 ? pointLabel(point, report, true) : ""}</span>
          </div>)}
        </div>
      </div>
      <p className="global-usage-chart-selection" aria-live="polite">{activePoint ? pointDescription(activePoint, report) : tx("悬停或聚焦柱形可查看用量明细")}</p>
      <details className="global-usage-chart-data">
        <summary>{tx("查看趋势数据")}</summary>
        <div className="global-usage-chart-table"><table>
          <caption>{tx("Token 用量趋势")}</caption>
          <thead><tr><th scope="col">{tx("时间")}</th><th scope="col">{tx("输入 Token")}</th><th scope="col">{tx("输出 Token")}</th><th scope="col">{tx("调用次数")}</th></tr></thead>
          <tbody>{points.map((point) => <tr key={point.date}><th scope="row">{pointLabel(point, report, false)}</th><td>{count(point.input_tokens)}</td><td>{count(point.output_tokens)}</td><td>{count(point.request_count)}</td></tr>)}</tbody>
        </table></div>
      </details>
    </>}
  </section>;
}
