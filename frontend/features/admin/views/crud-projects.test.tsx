import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { providerChannelAccountDetail, providerMonitorSamples, providerQuotaReadAction } from "./crud-projects";

describe("providerQuotaReadAction", () => {
  it("matches quota plugin actions by the resource Provider type", () => {
    const data = emptyData();
    data.providers = [{ id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 }];
    data.pluginActions = [
      { plugin_id: "tokenhub.provider.other", action_id: "quota.read", kind: "read", capability: "quota.read", subject: "other_provider" },
      { plugin_id: "tokenhub.provider.openai-codex", action_id: "openai_codex.quota.read", kind: "read", capability: "quota.read", subject: "openai_codex" },
    ];

    expect(providerQuotaReadAction(data, {
      id: "rsrc_codex",
      provider_id: "prv_codex",
      name: "Codex Account",
      resource_type: "openai_subscription",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    })?.plugin_id).toBe("tokenhub.provider.openai-codex");
  });
});

describe("providerChannelAccountDetail", () => {
  it("summarizes plugin account resources without relying on OpenAI subscriptions", () => {
    expect(providerChannelAccountDetail([
      {
        id: "rsrc_key",
        provider_id: "prv_kimi",
        name: "Kimi API Key",
        resource_type: "api_key",
        status: "active",
        healthy: true,
        priority: 1,
        weight: 100,
      },
      {
        id: "rsrc_kimi_account",
        provider_id: "prv_kimi",
        name: "Kimi Account",
        resource_type: "kimi_subscription",
        status: "active",
        healthy: true,
        priority: 2,
        weight: 100,
        credential_summary: { account_email: "team@example.com" },
      },
      {
        id: "rsrc_kimi_inactive",
        provider_id: "prv_kimi",
        name: "Kimi Inactive",
        resource_type: "kimi_subscription",
        status: "inactive",
        healthy: true,
        priority: 3,
        weight: 100,
      },
    ])).toBe("1/2 启用 · team@example.com");
  });
});

describe("providerMonitorSamples", () => {
  it("uses account resource test events for plugin account resources", () => {
    const data = emptyData();
    const provider = { id: "prv_kimi", name: "Kimi", type: "kimi", status: "active", healthy: true, priority: 1 };
    const resources = [
      {
        id: "rsrc_kimi_account",
        provider_id: "prv_kimi",
        name: "Kimi Account",
        resource_type: "kimi_subscription",
        status: "active",
        healthy: true,
        priority: 1,
        weight: 100,
      },
    ];
    data.auditEvents = [
      {
        id: "evt_kimi_test",
        action: "test",
        resource_type: "provider_resource",
        resource_id: "rsrc_kimi_account",
        status: "success",
        after_snapshot: JSON.stringify({ healthy: true, latency_ms: 321 }),
        created_at: "2026-08-26T01:02:03Z",
      },
    ];

    const result = providerMonitorSamples(data, provider, resources);

    expect(result.source).toBe("resource_test");
    expect(result.samples).toEqual([
      {
        created_at: "2026-08-26T01:02:03Z",
        success: true,
        latency_ms: 321,
        error_code: undefined,
      },
    ]);
  });
});
