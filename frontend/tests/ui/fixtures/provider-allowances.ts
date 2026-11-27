import type {
  AllowanceConfig,
  AllowanceEntry,
  AllowancePoint,
  AllowancePreset,
} from "../../../features/admin/core/provider-allowance";
import type { Provider } from "../../../features/admin/core/types";
import type { MockAPI } from "../network";

export function allowanceConfig(id: string): AllowanceConfig {
  return {
    resource_id: id,
    revision: "synthetic-revision",
    has_token: true,
    source: {
      enabled: true,
      preset: "fusecode",
      url: "https://quota.example.test/v1/usage",
      auth: "bearer",
      refresh_seconds: 60,
      expression:
        "$remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %; Month: subscription.monthly_usage_usd / subscription.monthly_limit_usd %",
      headers: { "X-Quota-Token": "[redacted]" },
      windows: [
        {
          name: "Weekly",
          used: "subscription.weekly_usage_percent",
          period_seconds: 604800,
          reset_path: "subscription.weekly_reset_at",
          models: ["claude-*"],
          pool: "Synthetic shared pool",
        },
      ],
    },
    policy: {
      cap: { percent: 90 },
      windows: { weekly: { percent: 85, amount: 75 } },
      credits: "never",
      reset: {
        on_exhausted: false,
        before_expiry: false,
        lead_seconds: 1800,
        weekly_limit: 1,
      },
      alerts: {
        enabled: true,
        used_percent: 90,
        low_balance: 5,
        unused_before_reset_seconds: 3600,
        channel_ids: ["ui-quota-channel"],
      },
      wallet: {
        enabled: false,
        balance: 50,
        unit: "USD",
        as_of: "2026-09-01T00:00:00Z",
        refill_amount: 50,
        refill_seconds: 2592000,
        refill_at: "2026-10-01T00:00:00Z",
        carry_over: false,
        grants: [
          { id: "ui-grant", amount: 10, expires_at: "2026-09-30T00:00:00Z" },
        ],
      },
    },
  };
}
export function allowanceEntries(): AllowanceEntry[] {
  return (["available", "unknown", "query_error", "exhausted"] as const).map(
    (status, index) => ({
      resource_id: `quota-account-${index}`,
      resource_name: [
        "Synthetic Fusecode",
        "Synthetic Unknown",
        "Synthetic Failed Query",
        "Synthetic Exhausted",
      ][index],
      provider_id: "quota-provider",
      provider_name: "Synthetic Relay",
      provider_type: "anthropic",
      status,
      config: allowanceConfig(`quota-account-${index}`),
      estimated: false,
      allowance: {
        version: 1,
        status,
        source: "fusecode",
        plan: "Synthetic Pro",
        fetched_at: "2026-09-07T01:59:00Z",
        checked_at: "2026-09-07T02:00:00Z",
        expires_at: "2026-10-01T00:00:00Z",
        ...(status === "query_error"
          ? { error_code: "allowance_query_failed" }
          : {}),
        windows: [
          {
            name: "Wallet",
            balance: status === "exhausted" ? 0 : 42.5,
            unit: "USD",
          },
          {
            name: "Weekly",
            used_percent: status === "exhausted" ? 100 : 25,
            period_seconds: 604800,
            reset_at: "2026-09-13T02:00:00Z",
            amount: 25,
            limit: 100,
            unit: "USD",
            models: ["claude-*"],
            pool: "Synthetic shared pool",
            unknown: status === "unknown",
          },
          {
            name: "Monthly",
            used_percent: 15,
            period_seconds: 2592000,
            reset_at: "2026-10-01T00:00:00Z",
          },
        ],
        credits: { balance: 1250, unit: "credits", available: true },
        resets: {
          count: 2,
          checked_at: "2026-09-07T02:00:00Z",
          cards: [
            { id: "synthetic-reset-1", expires_at: "2026-09-10T00:00:00Z" },
            { id: "synthetic-reset-2", expires_at: "2026-10-01T00:00:00Z" },
          ],
        },
      },
    }),
  );
}
export const allowancePresets: AllowancePreset[] = [
  {
    id: "tencent",
    name: "Tencent Token Plan",
    config: {
      enabled: false,
      preset: "tencent",
      url: "https://cloud-quota.example.test/",
      auth: "tencent",
    },
  },
  {
    id: "fusecode",
    name: "Fusecode",
    config: {
      enabled: false,
      preset: "fusecode",
      url: "https://quota.example.test/v1/usage",
      auth: "bearer",
      expression:
        "$remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %",
    },
  },
];
export function allowanceHistory(): AllowancePoint[] {
  const entry = allowanceEntries()[0];
  return [42.5, 45, 50, 55, 60, 65].map((balance, index) => ({
    id: `quota-point-${index}`,
    fetched_at: `2026-09-0${7 - index}T01:59:00Z`,
    allowance: {
      ...entry.allowance,
      windows: [{ name: "Wallet", balance, unit: "USD" }],
    },
  }));
}

export function installAllowances(
  api: MockAPI,
  options: {
    empty?: boolean;
    saveFailure?: boolean;
    loadFailure?: boolean;
  } = {},
) {
  const providers: Provider[] = [
    {
      id: "quota-provider",
      name: "Synthetic Relay",
      type: "anthropic",
      base_url: "https://inference.example.test/v1",
      priority: 1,
      status: "active",
      healthy: true,
    },
  ];
  let rows = options.empty ? [] : allowanceEntries();
  api.respond("GET", "/api/admin/providers", { data: providers });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  api.respond("GET", "/api/admin/provider-adapters", {
    data: [{ type: "anthropic", capabilities: ["chat"] }],
  });
  api.respond("GET", "/api/admin/plugins", { data: [] });
  api.respond("GET", "/api/admin/plugin-marketplace", {
    data: { available: true, plugins: [] },
  });
  api.respond("GET", "/api/admin/plugin-background-jobs", {
    data: [],
    runs: [],
  });
  for (const path of [
    "provider-resources",
    "routing-rules",
    "audit/events",
    "providers/monitoring",
    "plugin-ui-manifest",
    "plugin-actions",
  ])
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define(
    "GET",
    "/api/admin/audit/requests",
    () => ({
      json: {
        data: [],
        pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
        summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
      },
    }),
    () => {},
  );
  api.define("GET", "/api/admin/provider-allowances", () =>
    options.loadFailure
      ? {
          status: 503,
          json: { error: { message: "Synthetic quota cache unavailable" } },
        }
      : { json: { data: rows } },
  );
  api.respond(
    "GET",
    "/api/admin/provider-allowances/presets",
    allowancePresets,
  );
  api.respond("GET", "/api/admin/notification-channels", {
    data: [
      {
        id: "ui-quota-channel",
        kind: "notification-channels",
        name: "Synthetic Operations",
        status: "active",
        fields: { type: "webhook" },
      },
    ],
  });
  for (const entry of allowanceEntries()) {
    const base = `/api/admin/provider-resources/${entry.resource_id}/allowance`;
    api.respond("GET", `${base}/history`, { data: allowanceHistory() });
    api.respond("GET", `${base}/events`, {
      data: [
        {
          id: "quota-event-1",
          kind: "automatic_reset",
          state: "succeeded",
          details: "expiring_card",
          created_at: "2026-09-06T02:00:00Z",
          updated_at: "2026-09-06T02:00:00Z",
        },
        {
          id: "quota-event-2",
          kind: "allowance_high_usage",
          state: "fired",
          details: "Weekly",
          created_at: "2026-09-05T02:00:00Z",
          updated_at: "2026-09-05T02:00:00Z",
        },
      ],
    });
    api.define("PUT", `${base}/config`, (input) => {
      if (options.saveFailure)
        return {
          status: 409,
          json: {
            error: {
              message: "Synthetic configuration changed; reload before saving",
            },
          },
        };
      const config = input.body as AllowanceConfig;
      const saved = {
        ...config,
        revision: "synthetic-saved-revision",
        token: undefined,
      };
      rows = rows.map((row) =>
        row.resource_id === entry.resource_id ? { ...row, config: saved } : row,
      );
      return { json: saved };
    });
    api.respond("POST", `${base}/refresh`, { allowance: entry.allowance });
  }
}
