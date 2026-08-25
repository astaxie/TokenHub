import type { ProviderCatalogEntry } from "../core/types";
import type { OpenAIQuotaWindow } from "./provider-account-ui";

export type OpenAIAccountQuota = {
  account_id?: string;
  email?: string;
  plan_type?: string;
  rate_limit?: {
    allowed: boolean;
    limit_reached: boolean;
    primary_window?: OpenAIQuotaWindow;
    secondary_window?: OpenAIQuotaWindow;
  };
  additional_rate_limits?: Array<{
    limit_name: string;
    metered_feature: string;
    rate_limit?: {
      allowed: boolean;
      limit_reached: boolean;
      primary_window?: OpenAIQuotaWindow;
      secondary_window?: OpenAIQuotaWindow;
    };
  }>;
  rate_limit_reset_credits?: { available_count: number };
  fetched_at: number;
};

export type CodexSubscriptionTestResult = {
  resource_id: string;
  model: string;
  reasoning_effort: string;
  speed: "standard" | "fast";
  upstream_service_tier?: string;
  output_text: string;
  latency_ms: number;
  usage: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
};

export const fallbackCodexReasoningEfforts = ["low", "medium", "high", "xhigh", "max"];

export const codexProviderCatalogSummary: ProviderCatalogEntry = {
  id: "openai-codex",
  name: "OpenAI Codex",
  display_name: "OpenAI Codex",
  type: "openai_codex",
  base_url: "https://chatgpt.com/backend-api/codex",
  categories: ["codex"],
  category_counts: { codex: 0 },
  models_count: 0,
  source: "openai-codex-live",
};

export const grokProviderCatalogSummary: ProviderCatalogEntry = {
  id: "xai-grok",
  name: "Super Grok",
  display_name: "Super Grok",
  type: "xai_grok",
  base_url: "https://cli-chat-proxy.grok.com/v1",
  categories: ["grok"],
  category_counts: { grok: 0 },
  models_count: 0,
  source: "xai-grok-subscription",
};

export const accountProviderCatalogOptions = [codexProviderCatalogSummary, grokProviderCatalogSummary];

export function isCodexAccountCatalog(catalogID?: string) {
  return catalogID === codexProviderCatalogSummary.id;
}

export function isGrokAccountCatalog(catalogID?: string) {
  return catalogID === grokProviderCatalogSummary.id;
}

export function isAccountCatalogID(catalogID?: string) {
  return isCodexAccountCatalog(catalogID) || isGrokAccountCatalog(catalogID);
}

export function accountCatalogSummary(catalogID?: string) {
  if (isGrokAccountCatalog(catalogID)) return grokProviderCatalogSummary;
  return codexProviderCatalogSummary;
}

export function accountResourceTypeForCatalog(catalogID?: string) {
  return isGrokAccountCatalog(catalogID) ? "xai_subscription" : "openai_subscription";
}

export function accountModelCategoryForCatalog(catalogID?: string) {
  return isGrokAccountCatalog(catalogID) ? "grok" : "codex";
}

const accountVendorCredentialKeys = [
  "access_token",
  "refresh_token",
  "id_token",
  "account_email",
  "account_id",
  "organization_id",
  "plan_type",
  "token_type",
  "expires_at",
  "scopes",
] as const;

export function resetAccountVendorCredentials(values: Record<string, string>, catalogID: string) {
  const next: Record<string, string> = { ...values, auth_type: "oauth" };
  for (const key of accountVendorCredentialKeys) next[key] = "";
  next.resource_type = accountResourceTypeForCatalog(catalogID);
  const baseURL = accountCatalogSummary(catalogID).base_url;
  if (baseURL) next.base_url = baseURL;
  return next;
}
