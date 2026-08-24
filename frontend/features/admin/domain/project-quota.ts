export type ProjectQuotaValues = {
  status: string;
  rate_limit_rpm: string;
  token_limit_tpm: string;
  daily_requests: string;
  monthly_requests: string;
  daily_tokens: string;
  monthly_tokens: string;
  daily_cost_usd: string;
  monthly_cost_usd: string;
  max_concurrency: string;
};

export const projectQuotaFields: Array<{ key: keyof ProjectQuotaValues; label: string; suffix?: string }> = [
  { key: "rate_limit_rpm", label: "每分钟请求（RPM）" },
  { key: "token_limit_tpm", label: "每分钟 Token（TPM）" },
  { key: "daily_requests", label: "日请求" },
  { key: "monthly_requests", label: "月请求" },
  { key: "daily_tokens", label: "日 Token" },
  { key: "monthly_tokens", label: "月 Token" },
  { key: "daily_cost_usd", label: "日成本", suffix: "USD" },
  { key: "monthly_cost_usd", label: "月成本", suffix: "USD" },
  { key: "max_concurrency", label: "最大并发" },
];
