import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor } from "../core/types";
import { ProviderAccountQuotaReset } from "./provider-account-quota-reset";

const resource = {
  id: "rsrc_codex",
  provider_id: "prv_codex",
  name: "Codex Account",
  resource_type: "openai_subscription",
  status: "active",
  healthy: true,
  priority: 1,
  weight: 100,
};
const pluginActions: PluginActionDescriptor[] = [
  {
    plugin_id: "tokenhub.provider.openai-codex",
    action_id: "openai_codex.wrong.quota.reset_credits.read",
    kind: "read",
    capability: "quota.reset_credits.read",
    subject: "openai_codex",
    metadata: { provider_resource_type: "wrong_subscription" },
  },
  {
    plugin_id: "tokenhub.provider.openai-codex",
    action_id: "openai_codex.quota.reset_credits.read",
    kind: "read",
    capability: "quota.reset_credits.read",
    subject: "openai_codex",
    metadata: { provider_resource_type: "openai_subscription" },
  },
  {
    plugin_id: "tokenhub.provider.openai-codex",
    action_id: "openai_codex.wrong.quota.reset",
    kind: "mutate",
    capability: "quota.reset",
    subject: "openai_codex",
    metadata: { danger_confirmation: "wrong-danger-confirmation", provider_resource_type: "wrong_subscription" },
  },
  {
    plugin_id: "tokenhub.provider.openai-codex",
    action_id: "openai_codex.quota.reset",
    kind: "mutate",
    capability: "quota.reset",
    subject: "openai_codex",
    metadata: {
      danger_confirmation: "plugin-danger-confirmation",
      provider_resource_type: "openai_subscription",
      "quota_reset.legacy_storage_key_prefixes": "tokenhub.codex-quota-reset.",
      "quota_reset.final_error_codes": "openai_quota_reset_forbidden",
      "quota_reset.unknown_outcome_codes": "openai_quota_reset_outcome_unknown",
    },
  },
];

describe("ProviderAccountQuotaReset", () => {
  it("reads reset credits and submits quota reset through plugin actions", async () => {
    const user = userEvent.setup();
    const onRefreshQuota = vi.fn().mockResolvedValue(true);
    vi.spyOn(crypto, "randomUUID").mockReturnValue("reset-operation-id");
    const stored: Record<string, string> = {};
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => stored[key] ?? null,
        setItem: (key: string, value: string) => {
          stored[key] = value;
        },
        removeItem: (key: string) => {
          delete stored[key];
        },
      },
    });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        data: {
          available_count: 1,
          credits: [{ id: "credit-1", status: "available", expires_at: "2099-01-01T00:00:00Z" }],
          fetched_at: 123,
        },
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { code: "reset", windows_reset: 2 } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { available_count: 0, credits: [], fetched_at: 124 } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderAccountQuotaReset
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={pluginActions}
        providerType="openai_codex"
        quotaBusy={false}
        resource={resource}
        onRefreshQuota={onRefreshQuota}
      />,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.quota.reset_credits.read");
    expect(JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body))).toEqual({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
    });

    await user.click(screen.getByRole("radio", { name: /重置次数 1/ }));
    await user.click(screen.getByRole("button", { name: "重置套餐" }));
    expect(screen.getByRole("heading", { name: "确认重置用量窗口" })).toBeInTheDocument();
    expect(screen.queryByText(/Codex 额度安全操作|确认重置 Codex/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认重置" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(fetchMock.mock.calls[1][0]).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.quota.reset");
    expect(JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body))).toEqual({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
      confirm: true,
      idempotency_key: "reset-operation-id",
      expected_available_count: 1,
      credit_id: "credit-1",
      danger_confirmation: "plugin-danger-confirmation",
    });
    expect(onRefreshQuota).toHaveBeenCalledTimes(1);
  });

  it("stays hidden when quota reset plugin actions are not registered", () => {
    render(
      <ProviderAccountQuotaReset
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[]}
        providerType="openai_codex"
        quotaBusy={false}
        resource={resource}
        onRefreshQuota={vi.fn()}
      />,
    );

    expect(screen.queryByText("重置套餐")).not.toBeInTheDocument();
  });

  it("uses a provider plugin danger fallback when action metadata is absent", async () => {
    const user = userEvent.setup();
    vi.spyOn(crypto, "randomUUID").mockReturnValue("fallback-operation-id");
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: () => null,
        setItem: vi.fn(),
        removeItem: vi.fn(),
      },
    });
    const actionsWithoutDanger = pluginActions.map((action) => action.action_id === "openai_codex.quota.reset"
      ? { ...action, metadata: { provider_resource_type: "openai_subscription" } }
      : action);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        data: {
          available_count: 1,
          credits: [{ id: "credit-1", status: "available", expires_at: "2099-01-01T00:00:00Z" }],
        },
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { code: "reset", windows_reset: 1 } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { available_count: 0, credits: [] } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderAccountQuotaReset
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={actionsWithoutDanger}
        providerType="openai_codex"
        quotaBusy={false}
        resource={resource}
        onRefreshQuota={vi.fn().mockResolvedValue(true)}
      />,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    await user.click(screen.getByRole("radio", { name: /重置次数 1/ }));
    await user.click(screen.getByRole("button", { name: "重置套餐" }));
    await user.click(screen.getByRole("button", { name: "确认重置" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body)).danger_confirmation).toBe(
      "provider-quota-reset:tokenhub.provider.openai-codex:openai_codex.quota.reset",
    );
  });

  it("recovers and clears legacy reset state only when declared by plugin metadata", async () => {
    const user = userEvent.setup();
    const stored: Record<string, string> = {
      "tokenhub.codex-quota-reset.rsrc_codex": JSON.stringify({
        availableCount: 1,
        creditID: "credit-legacy",
        expiresAt: "2099-01-01T00:00:00Z",
        idempotencyKey: "legacy-operation-id",
        attempted: false,
      }),
    };
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => stored[key] ?? null,
        setItem: (key: string, value: string) => {
          stored[key] = value;
        },
        removeItem: (key: string) => {
          delete stored[key];
        },
      },
    });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        data: {
          available_count: 1,
          credits: [{ id: "credit-legacy", status: "available", expires_at: "2099-01-01T00:00:00Z" }],
        },
      }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { code: "already_redeemed", windows_reset: 1 } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { available_count: 0, credits: [] } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderAccountQuotaReset
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={pluginActions}
        providerType="openai_codex"
        quotaBusy={false}
        resource={resource}
        onRefreshQuota={vi.fn().mockResolvedValue(true)}
      />,
    );

    expect(screen.getByRole("heading", { name: "确认重置用量窗口" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认重置" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body)).idempotency_key).toBe("legacy-operation-id");
    expect(stored["tokenhub.codex-quota-reset.rsrc_codex"]).toBeUndefined();
    expect(stored["tokenhub.provider-quota-reset.rsrc_codex"]).toBeUndefined();
  });
});
