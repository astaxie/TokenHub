import { Activity, AlertCircle, Check, Database, Server, X } from "lucide-react";
import { useEffect, useState } from "react";
import { type ApiContext, type AppData, type DatabaseStatus, type Model } from "../core/types";
import { hasThirdPartyRoute, modelCategory, modelCategoryLabel } from "../domain/catalog";
import { formatNumber, modelAvailabilitySummary } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { DataSection } from "../shared/ui";

export function DatabaseStatusView({ api }: { api: ApiContext; isDark: boolean }) {
  const [status, setStatus] = useState<DatabaseStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchDatabaseStatus();
  }, []);

  const fetchDatabaseStatus = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`${api.baseURL}/api/admin/system/db-status`, {
        headers: {
          "Content-Type": "application/json",
          authorization: `Bearer ${api.adminToken}`,
        },
        credentials: "include",
      });

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`);
      }

      const data: DatabaseStatus = await res.json();
      setStatus(data);
    } catch (err) {
      console.error("Failed to fetch database status:", err);
      setError(err instanceof Error ? err.message : "Unknown error");
    } finally {
      setLoading(false);
    }
  };

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
            onClick={fetchDatabaseStatus}
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
      </div>
    </DataSection>
  );
}

export function modelCatalogPriceBaseline(models: Model[]) {
  const preferred = models.find((model) => /gpt-4\.1-mini|gpt-4o-mini|deepseek-chat/i.test(model.name));
  const preferredCost = preferred ? modelEstimatedMonthlyCost(preferred) : 0;
  if (preferredCost > 0) return preferredCost;
  const costs = models.map(modelEstimatedMonthlyCost).filter((cost) => cost > 0).sort((left, right) => left - right);
  return costs[Math.floor(costs.length / 2)] || costs[0] || 1;
}

export function modelCatalogPriceRow(model: Model, data: AppData, readOnly: boolean, baseline: number) {
  const category = modelCategory(model);
  const inputPrice = modelCatalogInputPrice(model);
  const outputPrice = model.output_price_usd_per_1m || undefined;
  const cacheRead = modelCacheReadPriceInfo(model);
  const monthlyCost = modelEstimatedMonthlyCost(model);
  const priceIndex = monthlyCost > 0 && baseline > 0 ? monthlyCost / baseline : 0;
  const availability = modelAvailabilitySummary(model, data, readOnly);
  const source = modelCatalogSource(model, data);
  const type = modelCatalogTypeBadge(model, monthlyCost, baseline);
  return {
    model,
    availability,
    category,
    categoryLabel: modelCategoryLabel(category),
    inputPrice,
    outputPrice,
    cacheReadPrice: cacheRead.price,
    cacheReadPriceSource: cacheRead.source,
    cacheReadPriceHint: modelCacheReadPriceHint(cacheRead),
    monthlyCost,
    priceIndex,
    contextLabel: model.context_window ? modelCatalogCompactNumber(model.context_window) : "-",
    contextDetail: model.context_window ? tx("上下文窗口") : tx("未配置"),
    sourceLabel: source.label,
    sourceTone: source.tone,
    typeLabel: type.label,
    typeTone: type.tone,
  };
}

export function modelCatalogInputPrice(model: Model) {
  if (model.input_price_usd_per_1m) return model.input_price_usd_per_1m;
  if (model.modality === "embedding" && model.embedding_price_usd_per_1m) return model.embedding_price_usd_per_1m;
  return undefined;
}

export type ModelCacheReadPriceInfo = {
  price?: number;
  source: "configured" | "estimated" | "unavailable";
  ratio?: number;
  unavailableReason?: "embedding" | "missing_input_price";
};

export const defaultCacheReadEstimateRatio = 0.10;
export const deepSeekCacheReadEstimateRatio = 0.02;
export const deepSeekV4ProCacheReadRatio = 1 / 120;

export function modelCacheReadPriceInfo(model: Model): ModelCacheReadPriceInfo {
  if (model.modality === "embedding") {
    return { source: "unavailable", unavailableReason: "embedding" };
  }
  const configured = model.cache_read_price_usd_per_1m || readModelMetadataNumber(model, [
    "cached_input_price_usd_per_1m",
    "cache_read_price_usd_per_1m",
    "cached_read_price_usd_per_1m",
  ]);
  if (configured > 0) return { price: configured, source: "configured" };
  const input = model.input_price_usd_per_1m || 0;
  if (!input) return { source: "unavailable", unavailableReason: "missing_input_price" };
  const category = modelCategory(model);
  const deepSeekV4Pro = `${model.name} ${model.family ?? ""}`.toLowerCase().includes("v4-pro");
  const ratio = category !== "deepseek"
    ? defaultCacheReadEstimateRatio
    : deepSeekV4Pro
      ? deepSeekV4ProCacheReadRatio
      : deepSeekCacheReadEstimateRatio;
  return { price: input * ratio, source: "estimated", ratio };
}

export function modelCachedReadPrice(model: Model) {
  return modelCacheReadPriceInfo(model).price;
}

export function modelCacheReadPriceHint(info: ModelCacheReadPriceInfo) {
  if (info.source === "configured") {
    return `${tx("已配置缓存读价")} $${modelCatalogMoney(info.price || 0)}/1M Token；${tx("命中缓存的输入 Token 按此价格估算成本。")}`;
  }
  if (info.source === "estimated") {
    return `${tx("未配置缓存读价，当前按标准输入价的")} ${cacheReadEstimatePercent(info.ratio || 0)} ${tx("估算；建议按上游实际价格配置。")}`;
  }
  if (info.unavailableReason === "embedding") {
    return tx("Embedding 模型按 Embedding 单价计费，不适用缓存读价。");
  }
  return tx("缺少标准输入价，无法估算缓存读价。");
}

export function cacheReadEstimatePercent(ratio: number) {
  const percent = ratio * 100;
  return `${Number.isInteger(percent) ? percent.toFixed(0) : percent.toFixed(2)}%`;
}

export function readModelMetadataNumber(model: Model, keys: string[]) {
  for (const key of keys) {
    const value = Number(model.metadata?.[key] ?? "");
    if (Number.isFinite(value) && value > 0) return value;
  }
  return 0;
}

export function modelEstimatedMonthlyCost(model: Model) {
  const embeddingPrice = model.embedding_price_usd_per_1m || 0;
  if (model.modality === "embedding" && embeddingPrice > 0) return embeddingPrice * 100;
  const input = model.input_price_usd_per_1m || 0;
  const output = model.output_price_usd_per_1m || 0;
  return input * 100 + output * 50;
}

export function modelCatalogTypeBadge(model: Model, monthlyCost: number, baseline: number) {
  const text = [model.name, model.family, model.modality, ...(model.capabilities ?? [])].join(" ").toLowerCase();
  if (/code|coder|codestral|devstral|codex|build/.test(text)) return { label: "代码", tone: "code" };
  if (/reason|thinking|r1|o1|o3/.test(text)) return { label: "推理", tone: "reasoning" };
  if (/image|vision|video|audio|ocr|multimodal/.test(text)) return { label: "多模态", tone: "media" };
  const index = monthlyCost > 0 && baseline > 0 ? monthlyCost / baseline : 0;
  if (index > 0 && index <= 0.8) return { label: "低价", tone: "low" };
  if (index >= 2.4 || /pro|opus|large|gpt-5|grok-4/.test(text)) return { label: "旗舰", tone: "flagship" };
  return { label: "均衡", tone: "balanced" };
}

export function modelCatalogSource(model: Model, data: AppData) {
  const hasPrice = modelEstimatedMonthlyCost(model) > 0;
  if (!hasPrice) return { label: "未配置", tone: "missing" };
  if (hasThirdPartyRoute(model, data)) return { label: "三方价", tone: "third" };
  return { label: "官方价", tone: "official" };
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

export function modelCatalogPriceValue(value: number | undefined) {
  return value && value > 0 ? `$${modelCatalogMoney(value)}` : "-";
}

export function modelCatalogMoney(value: number) {
  const amount = Math.max(0, value || 0);
  if (amount >= 100) return amount.toFixed(0);
  if (amount >= 10) return amount.toFixed(1);
  if (amount >= 1) return amount.toFixed(2);
  return amount.toFixed(3);
}

export function modelCatalogCompactNumber(value: number) {
  if (value >= 1_000_000) {
    const scaled = value / 1_000_000;
    return `${Number.isInteger(scaled) ? scaled.toFixed(0) : scaled.toFixed(1)}M`;
  }
  if (value >= 1_000) {
    const scaled = value / 1_000;
    return `${Number.isInteger(scaled) ? scaled.toFixed(0) : scaled.toFixed(1)}K`;
  }
  return formatNumber(value || 0);
}
