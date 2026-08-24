const en: Record<string, string> = {
  "网关实测延迟": "Observed Gateway Latency",
  "质量分（0-100）": "Quality Score (0–100)",
  "延迟优先取近24小时成功请求的中位总耗时；质量分综合可用率、延迟和资源健康。": "Latency uses the median total time of successful requests in the last 24 hours when available; quality score combines availability, latency, and resource health.",
};

const ja: Record<string, string> = {
  "网关实测延迟": "観測済みゲートウェイ遅延",
  "质量分（0-100）": "品質スコア（0～100）",
  "延迟优先取近24小时成功请求的中位总耗时；质量分综合可用率、延迟和资源健康。": "遅延は可能な場合、直近24時間の成功リクエストにおける総所要時間の中央値です。品質スコアは可用性、遅延、リソース健全性を総合します。",
};

export const providerMonitoringTranslations = { en, ja };
