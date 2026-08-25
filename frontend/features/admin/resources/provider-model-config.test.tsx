import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { providerResourceConfig, providerResourceCredentialRefreshAction, runProviderResourceCredentialRefreshAction, runProviderResourcePluginAction, unwrapPluginActionData } from "./provider-model-config";

describe("providerResourceConfig", () => {
  it("shows credential refresh only when a provider plugin action is registered", async () => {
    const data = emptyData();
    data.providers = [{ id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 }];
    const resource = {
      id: "rsrc_codex",
      provider_id: "prv_codex",
      name: "Codex Account",
      resource_type: "openai_subscription",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
      credential_summary: { has_refresh_token: "true" },
    };

    const refreshAction = providerResourceConfig().actions?.find((action) => action.label === "续租 Token");
    expect(refreshAction).toBeDefined();
    expect(refreshAction?.visible?.(resource, null, data)).toBe(false);

    data.pluginActions = [{
      plugin_id: "tokenhub.provider.openai-codex",
      action_id: "openai_codex.credentials.refresh",
      kind: "mutate",
      capability: "credentials.refresh",
      subject: "openai_codex",
    }];

    expect(providerResourceCredentialRefreshAction(data, resource)?.action_id).toBe("openai_codex.credentials.refresh");
    expect(refreshAction?.visible?.(resource, null, data)).toBe(true);

    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { credential_summary: {} } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await runProviderResourceCredentialRefreshAction({ baseURL: "http://localhost:8080", adminToken: "admin-token" }, resource, data);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.credentials.refresh");
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
      force: true,
    });
  });

  it("runs provider resource plugin actions that return data", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { fetched_at: 123, plan_type: "pro" } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await runProviderResourcePluginAction<{ fetched_at: number; plan_type: string }>(
      { baseURL: "http://localhost:8080", adminToken: "admin-token" },
      {
        id: "rsrc_codex",
        provider_id: "prv_codex",
        name: "Codex Account",
        resource_type: "openai_subscription",
        status: "active",
        healthy: true,
        priority: 1,
        weight: 100,
      },
      {
        plugin_id: "tokenhub.provider.openai-codex",
        action_id: "openai_codex.quota.read",
        kind: "read",
        capability: "quota.read",
        subject: "openai_codex",
      },
      { refresh: true },
      "Query quota",
    );

    expect(result).toEqual({ fetched_at: 123, plan_type: "pro" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.quota.read");
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
      refresh: true,
    });
  });

  it("unwraps plugin action envelopes", () => {
    expect(unwrapPluginActionData<{ ok: boolean }>({ data: { ok: true } })).toEqual({ ok: true });
    expect(unwrapPluginActionData<{ ok: boolean }>({ ok: false })).toEqual({ ok: false });
  });
});
