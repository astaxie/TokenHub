import { Activity, AlertCircle, Check, Database, Server, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { type ApiContext, type DatabaseStatus, type Model, type SchemaEvolutionStatus } from "../core/types";
import { evolutionReasonText } from "../i18n/db-evolution-reasons";
import { formatLocaleNumber, formatTranslationTemplate, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError } from "../resources/payloads";
import { DataSection } from "../shared/ui";

export function DatabaseStatusView({ api }: { api: ApiContext; isDark: boolean }) {
  const [status, setStatus] = useState<DatabaseStatus | null>(null);
  const [schema, setSchema] = useState<SchemaEvolutionStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Holds whichever request is outstanding, from the mount-time load or the refresh
  // button. A newer load cancels the older one so a slow response cannot overwrite a
  // fresher one, and unmounting cancels the last.
  const inFlight = useRef<AbortController | null>(null);

  const fetchDatabaseStatus = useCallback(async () => {
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;
    setLoading(true);
    setError(null);
    try {
      const res = await adminFetch(api, "/api/admin/system/db-status", { signal: controller.signal });

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`);
      }

      const data: DatabaseStatus = await res.json();
      setStatus(data);
      // The evolution status is read-only diagnostics; a failure leaves the
      // base status intact rather than failing the whole view.
      try {
        const schemaRes = await adminFetch(api, "/api/admin/system/schema-status", { signal: controller.signal });
        if (schemaRes.ok) {
          setSchema((await schemaRes.json()) as SchemaEvolutionStatus);
        }
      } catch (schemaErr) {
        if (!(schemaErr instanceof DOMException && schemaErr.name === "AbortError") && !isAuthExpiredError(schemaErr)) {
          setSchema(null);
        }
      }
    } catch (err) {
      // Aborted by a newer load or by unmounting, or an expired session the logout event
      // adminFetch dispatches already handles. Neither is this view's error to report.
      if (err instanceof DOMException && err.name === "AbortError") return;
      if (isAuthExpiredError(err)) return;
      console.error("Failed to fetch database status:", err);
      setError(err instanceof Error ? err.message : "Unknown error");
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void fetchDatabaseStatus();
    return () => inFlight.current?.abort();
  }, [fetchDatabaseStatus]);

  if (loading) {
    return (
      <DataSection title={tx("数据库状态")}>
        <div className="database-status-message">{tx("加载中")}...</div>
      </DataSection>
    );
  }

  if (error) {
    return (
      <DataSection title={tx("数据库状态")}>
        <div className="database-status-error" role="alert">
          <AlertCircle />
          <span>{tx("加载失败")}: {error}</span>
        </div>
      </DataSection>
    );
  }

  if (!status) {
    return (
      <DataSection title={tx("数据库状态")}>
        <div className="database-status-message">{tx("无数据")}</div>
      </DataSection>
    );
  }

  return (
    <DataSection title={tx("数据库状态")}>
      <div className="database-status">
        <div className="database-status-actions">
          <button
            type="button"
            onClick={() => void fetchDatabaseStatus()}
            className="button"
          >
            {tx("刷新")}
          </button>
        </div>

        <div className="database-status-grid">
          {/* Database type */}
          <article className="database-status-card">
            <div className="database-status-card-header">
              <span className="database-status-card-icon database">
                <Database />
              </span>
              <h2>{tx("数据库类型")}</h2>
            </div>
            <div className="database-status-card-value">
              {status.database_type === "postgres" ? "PostgreSQL" : "SQLite"}
            </div>
          </article>

          {/* Runtime environment */}
          <article className="database-status-card">
            <div className="database-status-card-header">
              <span className="database-status-card-icon runtime">
                <Server />
              </span>
              <h2>{tx("运行环境")}</h2>
            </div>
            <div className="database-status-value-row">
              <span className={`database-status-indicator${status.is_docker ? " active" : ""}`} />
              <span className="database-status-card-value">
                {status.is_docker ? tx("Docker 容器") : tx("本地进程")}
              </span>
            </div>
          </article>

          {/* Connection status */}
          <article className="database-status-card">
            <div className="database-status-card-header">
              <span className="database-status-card-icon connection">
                <Activity />
              </span>
              <h2>{tx("连接状态")}</h2>
            </div>
            <div className="database-status-value-row">
              {status.connection_ok ? (
                <>
                  <Check className="database-status-state-icon normal" />
                  <span className="database-status-state normal">{tx("正常")}</span>
                </>
              ) : (
                <>
                  <X className="database-status-state-icon error" />
                  <span className="database-status-state error">{tx("异常")}</span>
                </>
              )}
            </div>
          </article>

          {/* PostgreSQL version (only shown for PostgreSQL) */}
          {status.database_type === "postgres" && status.postgres_version && (
            <article className="database-status-card">
              <div className="database-status-card-header">
                <span className="database-status-card-icon version">
                  <Database />
                </span>
                <h2>PostgreSQL {tx("版本")}</h2>
              </div>
              <div className="database-status-version">
                {status.postgres_version.split('\n')[0]}
              </div>
            </article>
          )}
        </div>

        {/* Database connection info */}
        {status.database_url && (
          <div className="database-status-details">
            <div className="database-status-card-header">
              <span className="database-status-card-icon details">
                <Database />
              </span>
              <h2>{tx("数据库连接信息")}</h2>
            </div>
            <div className="database-status-url">
              {status.database_url}
            </div>
            <div className="database-status-note">
              * {tx("密码已隐藏以保护敏感信息")}
            </div>
          </div>
        )}

        {/* Read-only database evolution state */}
        {schema && (
          <div className="database-status-details">
            <div className="database-status-card-header">
              <span className="database-status-card-icon version">
                <Database />
              </span>
              <h2>{tx("数据库演进")}</h2>
            </div>
            <div className="database-status-value-row">
              <span className={`database-status-state ${schema.ready ? "normal" : "error"}`}>
                {schema.ready ? tx("就绪") : tx("未就绪")}
              </span>
              <span className="database-status-card-value">
                {formatTranslationTemplate(tx("数据库状态版本 {version}"), {
                  version: formatLocaleNumber(schema.schema_version),
                })}
              </span>
              {schema.compatibility && (
                <span className="database-status-note">
                  {formatTranslationTemplate(tx("兼容范围 {min} – {max}"), {
                    min: formatLocaleNumber(schema.compatibility.min_compatible),
                    max: formatLocaleNumber(schema.compatibility.max_compatible),
                  })}
                </span>
              )}
            </div>
            {!schema.ready && (schema.reason_code || schema.reason) && (
              <div className="database-status-note" role="alert">{evolutionReasonText(schema)}</div>
            )}
            <div className="database-status-note">
              {formatTranslationTemplate(tx("待执行迁移：{count}"), {
                count: formatLocaleNumber((schema.pending_expand?.length ?? 0) + (schema.pending_contract?.length ?? 0)),
              })}
            </div>
            <div className="database-status-note">
              {formatTranslationTemplate(tx("数据回填：{count}"), {
                count: formatLocaleNumber(schema.backfills?.length ?? 0),
              })}
            </div>
            {schema.instances && schema.instances.length > 0 && (
              <div className="database-status-note">
                {formatTranslationTemplate(tx("在线实例：{instances}"), {
                  instances: schema.instances
                    .map((instance) => `${instance.instance_id} (${instance.release})`)
                    .join(", "),
                })}
              </div>
            )}
          </div>
        )}
      </div>
    </DataSection>
  );
}

export function modelBrandIconSource(category: string) {
  const sources: Record<string, string> = {
    openai: "/model-icons/openai.svg",
    claude: "/model-icons/claude.svg",
    deepseek: "/model-icons/deepseek.svg",
    gemini: "/model-icons/gemini.svg",
    qwen: "/model-icons/qwen.svg",
    glm: "/model-icons/glm.svg",
    kimi: "/model-icons/kimi.svg",
    doubao: "/model-icons/doubao.svg",
    ernie: "/model-icons/ernie.svg",
    baichuan: "/model-icons/baichuan.svg",
    minimax: "/model-icons/minimax.svg",
    stepfun: "/model-icons/stepfun.svg",
    wanx: "/model-icons/wanx.svg",
    paddlepaddle: "/model-icons/paddlepaddle.svg",
    microsoft: "/model-icons/microsoft.svg",
    llama: "/model-icons/llama.svg",
    mistral: "/model-icons/mistral.svg",
    grok: "/model-icons/grok.svg",
  };
  return sources[category] ?? "";
}

export function modelDisplayTitle(model: Model) {
  return model.metadata?.title || model.name;
}
