import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { providerResourceAuthTypeOptionsFromData } from "../domain/provider-resource-types";
import { providerResourcePayload } from "./payloads";
import { exchangeProviderAccountOAuthCode, generateProviderAccountOAuthURL, providerConfig, providerPluginActionDefaultPayload, providerResourceConfig, providerResourceCredentialRefreshAction, providerResourceDraftDefaults, providerResourceTypeOptionsFromData, runProviderAvailabilityTest, runProviderPluginAction, runProviderResourceCredentialRefreshAction, runProviderResourcePluginAction, unwrapPluginActionData } from "./provider-model-config";

describe("providerResourceConfig", () => {
  it("renders provider type display names from plugin catalog metadata", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Plugin",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [{ kind: "provider_type", name: "kimi_subscription" }],
    }];
    data.providerCatalog = [{
      id: "kimi",
      name: "Kimi Catalog",
      display_name: "Kimi Subscription",
      type: "kimi_subscription",
      models_count: 0,
      source: "plugin",
    }];
    data.providers = [{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }];
    const column = providerConfig().columns.find((item) => item.key === "type");
    const rendered = column?.render?.(data.providers[0], data);

    expect(rendered).toBe("Kimi Subscription");
  });

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

  it("uses plugin action metadata for account probe default payloads", async () => {
    const data = emptyData();
    data.providers = [{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }];
    data.providerResources = [{
      id: "rsrc_kimi",
      provider_id: "prv_kimi",
      name: "Kimi Account",
      resource_type: "kimi_subscription_account",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    }];
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Subscription",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain", "management_action"],
      capabilities: [
        { kind: "provider", name: "provider_type", subject: "kimi_subscription" },
        { kind: "provider_resource_type", name: "kimi_subscription_account", subject: "kimi_subscription" },
      ],
    }];
    data.pluginActions = [{
      plugin_id: "tokenhub.provider.kimi",
      action_id: "kimi.probe.run",
      kind: "test",
      capability: "probe.run",
      subject: "kimi_subscription",
      metadata: {
        default_payload_json: JSON.stringify({ model: "kimi-k2", prompt: "ping" }),
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { resource_id: "rsrc_kimi" } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await runProviderAvailabilityTest({ baseURL: "http://localhost:8080", adminToken: "admin-token" }, data.providers[0], data);

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_kimi",
      resource_id: "rsrc_kimi",
      model: "kimi-k2",
      prompt: "ping",
    });
    expect(providerPluginActionDefaultPayload({ metadata: { default_payload_json: "[]" } })).toEqual({});
  });

  it("runs provider-level plugin actions without a resource envelope", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { auth_url: "https://provider.example/oauth", state: "state-1" } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await runProviderPluginAction<{ auth_url: string; state: string }>(
      { baseURL: "http://localhost:8080", adminToken: "admin-token" },
      {
        plugin_id: "tokenhub.provider.openai-codex",
        action_id: "openai_codex.oauth.start",
        kind: "external_redirect",
        capability: "oauth.start",
        subject: "openai_codex",
      },
      { return_url: "http://localhost:3000/providers/callback" },
      "Start OAuth",
    );

    expect(result).toEqual({ auth_url: "https://provider.example/oauth", state: "state-1" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.oauth.start");
    expect(JSON.parse(String(init.body))).toEqual({ return_url: "http://localhost:3000/providers/callback" });
  });

  it("generates provider account OAuth URLs through plugin actions first", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: {
        auth_url: "https://provider.example/oauth",
        session_id: "session-1",
        state: "state-1",
        redirect_uri: "http://localhost:1455/auth/callback",
        expires_at: "2026-01-01T00:00:00Z",
      },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await generateProviderAccountOAuthURL(
      { baseURL: "http://localhost:8080", adminToken: "admin-token" },
      [{
        plugin_id: "tokenhub.provider.openai-codex",
        action_id: "openai_codex.oauth.start",
        kind: "external_redirect",
        capability: "oauth.start",
        subject: "openai_codex",
      }],
      "openai_codex",
      "http://localhost:3000/providers/callback",
    );

    expect(result.auth_url).toBe("https://provider.example/oauth");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.oauth.start");
    expect(JSON.parse(String(init.body))).toEqual({
      redirect_uri: "http://localhost:1455/auth/callback",
      return_url: "http://localhost:3000/providers/callback",
    });
  });

  it("falls back to the legacy OpenAI account OAuth endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      auth_url: "https://legacy.example/oauth",
      session_id: "session-legacy",
      state: "state-legacy",
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await generateProviderAccountOAuthURL(
      { baseURL: "http://localhost:8080", adminToken: "admin-token" },
      [],
      "openai_codex",
      "http://localhost:3000/providers/callback",
    );

    expect(result.auth_url).toBe("https://legacy.example/oauth");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/provider-account-oauth/openai/generate-auth-url");
    expect(JSON.parse(String(init.body))).toEqual({ return_url: "http://localhost:3000/providers/callback" });
  });

  it("exchanges provider account OAuth codes through plugin actions first", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: {
        access_token: "token-redacted",
        refresh_token: "refresh-redacted",
        account_email: "owner@example.com",
      },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await exchangeProviderAccountOAuthCode(
      { baseURL: "http://localhost:8080", adminToken: "admin-token" },
      [{
        plugin_id: "tokenhub.provider.openai-codex",
        action_id: "openai_codex.oauth.exchange",
        kind: "mutate",
        capability: "oauth.exchange",
        subject: "openai_codex",
      }],
      "openai_codex",
      { session_id: "session-1", state: "state-1", code: "code-1" },
    );

    expect(result.account_email).toBe("owner@example.com");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.oauth.exchange");
    expect(JSON.parse(String(init.body))).toEqual({
      session_id: "session-1",
      state: "state-1",
      code: "code-1",
      redirect_uri: "http://localhost:1455/auth/callback",
    });
  });

  it("falls back to the legacy OpenAI account OAuth exchange endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      access_token: "token-redacted",
      account_email: "legacy@example.com",
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await exchangeProviderAccountOAuthCode(
      { baseURL: "http://localhost:8080", adminToken: "admin-token" },
      [],
      "openai_codex",
      { session_id: "session-legacy", state: "state-legacy", code: "code-legacy" },
    );

    expect(result.account_email).toBe("legacy@example.com");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/provider-account-oauth/openai/exchange-code");
    expect(JSON.parse(String(init.body))).toEqual({
      session_id: "session-legacy",
      state: "state-legacy",
      code: "code-legacy",
    });
  });

  it("unwraps plugin action envelopes", () => {
    expect(unwrapPluginActionData<{ ok: boolean }>({ data: { ok: true } })).toEqual({ ok: true });
    expect(unwrapPluginActionData<{ ok: boolean }>({ ok: false })).toEqual({ ok: false });
  });

  it("builds resource type options from provider plugin capabilities", () => {
    const data = emptyData();
    data.providers = [
      { id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 },
      { id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 2 },
    ];
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Subscription",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain", "management_action"],
      capabilities: [
        { kind: "provider_type", name: "kimi_subscription" },
        {
          kind: "provider_resource_type",
          name: "kimi_oauth_account",
          subject: "kimi_subscription",
          value: JSON.stringify({
            type: "kimi_oauth_account",
            display_name: "Kimi OAuth Account",
            auth_modes: ["oauth", "personal_access_token"],
            default: true,
            defaults: { auth_type: "oauth", base_url: "https://api.moonshot.cn/v1" },
          }),
        },
      ],
    }, {
      id: "tokenhub.provider.openai-codex",
      name: "OpenAI Codex Subscription",
      version: "built-in",
      source: "built-in",
      kinds: ["provider"],
      placements: ["gateway_chain", "management_action"],
      capabilities: [
        { kind: "provider_type", name: "openai_codex" },
        {
          kind: "provider_resource_type",
          name: "openai_subscription",
          subject: "openai_codex",
          value: JSON.stringify({
            type: "openai_subscription",
            display_name: "OpenAI Codex Subscription",
            auth_modes: ["oauth", "personal_access_token"],
            default: true,
            defaults: { auth_type: "oauth", base_url: "https://chatgpt.com/backend-api/codex", max_concurrency: "3" },
          }),
        },
      ],
    }];

    expect(providerResourceTypeOptionsFromData(data, null, { provider_id: "prv_kimi" })).toEqual([
      { value: "api_key", label: "API Key" },
      { value: "kimi_oauth_account", label: "Kimi OAuth Account" },
    ]);
    expect(providerResourceTypeOptionsFromData(data, null, { provider_id: "prv_codex" }).map((option) => option.value)).toEqual([
      "openai_subscription",
      "api_key",
    ]);
  });

  it("applies provider resource metadata to account defaults and auth options", () => {
    const data = emptyData();
    data.providers = [{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }];
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Subscription",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [{
        kind: "provider_resource_type",
        name: "kimi_oauth_account",
        subject: "kimi_subscription",
        value: JSON.stringify({
          type: "kimi_oauth_account",
          display_name: "Kimi OAuth Account",
          auth_modes: ["personal_access_token", "oauth"],
          default: true,
          defaults: {
            auth_type: "personal_access_token",
            base_url: "https://api.moonshot.cn/v1",
            max_concurrency: "5",
            group: "kimi",
          },
        }),
      }],
    }];

    expect(providerResourceDraftDefaults({ provider_id: "prv_kimi", name: "Kimi", type: "kimi_subscription" }, data)).toMatchObject({
      provider_id: "prv_kimi",
      name: "Kimi Account",
      resource_type: "kimi_oauth_account",
      auth_type: "personal_access_token",
      base_url: "https://api.moonshot.cn/v1",
      max_concurrency: "5",
      group: "kimi",
    });
    expect(providerResourceAuthTypeOptionsFromData(data, null, { provider_id: "prv_kimi", resource_type: "kimi_oauth_account" })).toEqual([
      { value: "oauth", label: "OAuth" },
      { value: "personal_access_token", label: "Personal Access Token" },
    ]);
  });

  it("uses neutral resource defaults for plugin providers without resource metadata", () => {
    const defaults = providerResourceDraftDefaults({
      provider_id: "prv_custom_plugin",
      name: "Custom Plugin",
      type: "custom_plugin",
    }, emptyData());

    expect(defaults).toMatchObject({
      provider_id: "prv_custom_plugin",
      name: "Custom Plugin Account",
      resource_type: "api_key",
      auth_type: "",
      base_url: "",
    });
  });

  it("uses built-in Provider plugin metadata for Codex account defaults", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.openai-codex",
      name: "OpenAI Codex Subscription",
      version: "built-in",
      source: "built-in",
      kinds: ["provider"],
      placements: ["gateway_chain", "management_action"],
      capabilities: [{
        kind: "provider_resource_type",
        name: "openai_subscription",
        subject: "openai_codex",
        value: JSON.stringify({
          type: "openai_subscription",
          display_name: "OpenAI Codex Subscription",
          auth_modes: ["oauth", "personal_access_token"],
          default: true,
          defaults: {
            auth_type: "oauth",
            base_url: "https://chatgpt.com/backend-api/codex",
            max_concurrency: "3",
          },
        }),
      }],
    }];

    expect(providerResourceDraftDefaults({ provider_id: "prv_codex", name: "Codex", type: "openai_codex" }, data)).toMatchObject({
      provider_id: "prv_codex",
      name: "Codex Account",
      resource_type: "openai_subscription",
      auth_type: "oauth",
      base_url: "https://chatgpt.com/backend-api/codex",
      max_concurrency: "3",
    });
  });

  it("renders plugin resource type display names in account resource tables", () => {
    const data = emptyData();
    data.providers = [{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }];
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Subscription",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [{
        kind: "provider_resource_type",
        name: "kimi_oauth_account",
        subject: "kimi_subscription",
        value: JSON.stringify({
          type: "kimi_oauth_account",
          display_name: "Kimi OAuth Account",
        }),
      }],
    }];
    const column = providerResourceConfig().columns.find((item) => item.key === "resource_type");
    const rendered = column?.render?.({
      id: "rsrc_kimi",
      provider_id: "prv_kimi",
      name: "Kimi Account",
      resource_type: "kimi_oauth_account",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    }, data);

    expect(rendered).toBe("Kimi OAuth Account");
  });

  it("submits plugin account resource credentials with its own credential source", () => {
    const payload = providerResourcePayload({
      provider_id: "prv_kimi",
      name: "Kimi Account",
      resource_type: "kimi_oauth_account",
      auth_type: "personal_access_token",
      access_token: "kimi-token",
      refresh_token: "kimi-refresh",
      base_url: "https://api.moonshot.cn/v1",
    }) as { api_key?: string; credentials?: Record<string, string>; options?: Record<string, string> };

    expect(payload.api_key).toBe("kimi-token");
    expect(payload.credentials).toMatchObject({
      auth_type: "personal_access_token",
      access_token: "kimi-token",
      refresh_token: "kimi-refresh",
    });
    expect(payload.options).toMatchObject({
      credential_source: "kimi_oauth_account",
      auth_type: "personal_access_token",
    });
  });

  it("tests subscription-backed Providers through resource probe plugin actions", async () => {
    const data = emptyData();
    data.providers = [{ id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 }];
    data.providerResources = [{
      id: "rsrc_codex",
      provider_id: "prv_codex",
      name: "Codex Account",
      resource_type: "openai_subscription",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    }];
    data.pluginActions = [{
      plugin_id: "tokenhub.provider.openai-codex",
      action_id: "openai_codex.probe.run",
      kind: "test",
      capability: "probe.run",
      subject: "openai_codex",
      metadata: {
        default_payload_json: JSON.stringify({
          model: "gpt-5.6-luna",
          reasoning_effort: "medium",
          speed: "standard",
          prompt: "Please respond with one short sentence confirming the Codex connection works.",
        }),
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { healthy: true } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await runProviderAvailabilityTest({ baseURL: "http://localhost:8080", adminToken: "admin-token" }, data.providers[0], data);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.probe.run");
    expect(JSON.parse(String(init.body))).toMatchObject({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
      model: "gpt-5.6-luna",
      reasoning_effort: "medium",
      speed: "standard",
    });
  });

  it("tests plugin account Providers through resource probe plugin actions", async () => {
    const data = emptyData();
    data.providers = [{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }];
    data.providerResources = [{
      id: "rsrc_kimi",
      provider_id: "prv_kimi",
      name: "Kimi Account",
      resource_type: "kimi_oauth_account",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    }];
    data.pluginActions = [{
      plugin_id: "tokenhub.provider.kimi",
      action_id: "kimi.probe.run",
      kind: "test",
      capability: "probe.run",
      subject: "kimi_subscription",
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { healthy: true } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await runProviderAvailabilityTest({ baseURL: "http://localhost:8080", adminToken: "admin-token" }, data.providers[0], data);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.kimi/actions/kimi.probe.run");
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_kimi",
      resource_id: "rsrc_kimi",
    });
  });

  it("tests direct Providers through provider probe plugin actions", async () => {
    const data = emptyData();
    data.providers = [{ id: "prv_plugin", name: "Plugin Provider", type: "plugin_provider", status: "active", healthy: true, priority: 1 }];
    data.pluginActions = [{
      plugin_id: "tokenhub.provider.plugin",
      action_id: "provider.probe.run",
      kind: "test",
      capability: "provider.probe.run",
      subject: "plugin_provider",
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { healthy: true } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await runProviderAvailabilityTest({ baseURL: "http://localhost:8080", adminToken: "admin-token" }, data.providers[0], data);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.plugin/actions/provider.probe.run");
    expect(JSON.parse(String(init.body))).toEqual({ provider_id: "prv_plugin" });
  });

  it("falls back to legacy Provider tests while providers are migrated", async () => {
    const data = emptyData();
    data.providers = [{ id: "prv_legacy", name: "Legacy Provider", type: "openai_compatible", status: "active", healthy: true, priority: 1 }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await runProviderAvailabilityTest({ baseURL: "http://localhost:8080", adminToken: "admin-token" }, data.providers[0], data);

    expect(fetchMock.mock.calls[0][0]).toBe("http://localhost:8080/api/admin/providers/prv_legacy/test");
  });
});
