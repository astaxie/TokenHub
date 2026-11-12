export type AccountRoutingStrategy = "default" | "quota_smart" | "expiry_first" | "least_used" | "drain";

export type AccountAllowance = {
  version: number;
  status: "unknown" | "available" | "exhausted" | "query_error";
  fetched_at?: string;
  checked_at?: string;
  expires_at?: string;
  error_code?: string;
  credits?: { available?: boolean; unlimited?: boolean; balance?: number; unit?: string };
  windows?: Array<{
    name?: string;
    used_percent?: number;
    amount?: number;
    limit?: number;
    balance?: number;
    unit?: string;
    period_seconds?: number;
    reset_at?: string;
    expires_at?: string;
    models?: string[];
    excluded_models?: string[];
    informational?: boolean;
    credits?: boolean;
  }>;
};
