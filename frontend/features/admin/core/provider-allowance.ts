import type { AccountAllowance } from "./account-allowance-types";

export type AllowanceCap = { percent?: number; amount?: number };
export type AllowanceWindowRule = {
  name: string;
  used?: string;
  amount?: string;
  limit?: string;
  balance?: string;
  unit?: string;
  period?: string;
  period_seconds?: number;
  reset_path?: string;
  expires_path?: string;
  models?: string[];
  excluded_models?: string[];
  pool?: string;
  informational?: boolean;
  credits?: boolean;
  optional?: boolean;
};
export type AllowanceSource = {
  no_balance_probe?: boolean;
  enabled: boolean;
  access_key_id?: string;
  region?: string;
  plan_id?: string;
  body?: string;
  preset?: string;
  url?: string;
  method?: string;
  auth?: string;
  headers?: Record<string, string>;
  expression?: string;
  unit?: string;
  plan_path?: string;
  expires_path?: string;
  windows?: AllowanceWindowRule[];
  refresh_seconds?: number;
  shared_account_id?: string;
  shared_account_revision?: string;
};
export type AllowancePolicy = {
  cap_stop?: boolean;
  cap_careful?: boolean;
  spend_cap?: {
    usd: number;
    period: "day" | "month" | "total";
    since?: string;
    timezone?: string;
  };
  cap: AllowanceCap;
  windows?: Record<string, AllowanceCap>;
  credits?: string;
  reset: {
    on_exhausted: boolean;
    before_expiry: boolean;
    lead_seconds?: number;
    weekly_limit?: number;
  };
  alerts: {
    channel_ids?: string[];
    enabled: boolean;
    used_percent?: number;
    low_balance?: number;
    unused_before_reset_seconds?: number;
  };
  wallet: {
    refill?: { every: "week" | "month"; day: number };
    timezone?: string;
    enabled: boolean;
    balance: number;
    unit: string;
    as_of?: string;
    cost_per_usd?: number;
    grants?: Array<{
      id: string;
      amount: number;
      expires_at?: string;
      at?: string;
      until?: string;
    }>;
    refill_amount?: number;
    refill_seconds?: number;
    refill_at?: string;
    carry_over: boolean;
  };
};
export type AllowanceConfig = {
  resource_id: string;
  revision: string;
  source: AllowanceSource;
  policy: AllowancePolicy;
  has_token: boolean;
  token?: string;
  clear_token?: boolean;
  updated_at?: string;
};
export type AllowanceEntry = {
  resource_id: string;
  resource_name: string;
  provider_id: string;
  provider_name: string;
  provider_type: string;
  status: AccountAllowance["status"];
  allowance: AccountAllowance;
  config: AllowanceConfig;
  estimated: boolean;
  balance_parts?: AllowanceQueryResult["balance_parts"];
};
export type AllowancePreset = {
  id: string;
  name: string;
  config: AllowanceSource;
};
export type AllowanceQueryResult = {
  query_url?: string;
  query_expression?: string;
  balance_parts?: Array<{
    label?: string;
    text: string;
    percent?: number;
    error?: string;
  }>;
  allowance: AccountAllowance;
};
export type AllowancePoint = {
  id: string;
  fetched_at: string;
  allowance: AccountAllowance;
};
export type AllowanceEvent = {
  id: string;
  kind: string;
  state: string;
  created_at: string;
  updated_at: string;
  details: string;
};
