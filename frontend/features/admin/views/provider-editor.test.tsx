import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type Provider, type ProviderCatalogEntry } from "../core/types";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderUpsertModal } from "./provider-editor";

const catalogEntry: ProviderCatalogEntry = {
  id: "quality-provider",
  name: "Quality Provider",
  display_name: "Quality Provider",
  type: "openai_compatible",
  base_url: "https://provider.example/v1",
  categories: ["openai"],
  models_count: 0,
  source: "component-test",
};

const codexAccountCatalogEntry: ProviderCatalogEntry = {
  ...catalogEntry,
  id: "openai-codex",
  name: "OpenAI Codex",
  display_name: "OpenAI Codex",
  type: "openai_codex",
  base_url: "https://chatgpt.com/backend-api/codex",
  categories: ["codex"],
};

describe("ProviderUpsertModal", () => {
  afterEach(() => {
    setActiveLanguage("en");
    vi.unstubAllGlobals();
  });

  it("submits a Provider creation payload from the API key flow", async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn().mockResolvedValue(undefined);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/api/admin/provider-catalog/quality-provider")) {
        return new Response(JSON.stringify({ data: catalogEntry }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (url.endsWith("/api/admin/providers") && init?.method === "POST") {
        return new Response(JSON.stringify({ provider: { id: "prv_quality" }, imported_models: 0 }), {
          status: 201,
          headers: { "content-type": "application/json" },
        });
      }
      throw new Error(`Unexpected request: ${init?.method ?? "GET"} ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[catalogEntry]}
        loading={false}
        mode="create"
        onClose={vi.fn()}
        onSaved={onSaved}
        pluginUI={[
          {
            plugin_id: "tokenhub.provider.quality",
            id: "tenant",
            slot: "provider.form.section",
            title: "Quality Settings",
            provider_types: ["openai_compatible"],
            schema: { fields: [{ name: "tenant_id", type: "text", label: "Tenant ID" }] },
          },
        ]}
        resources={[]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "http://localhost:8080/api/admin/provider-catalog/quality-provider",
      expect.any(Object),
    ));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.type(screen.getByLabelText("Tenant ID"), "tenant-001");
    await user.type(screen.getByLabelText("API Key", { exact: true }), "provider-secret");
    await user.click(screen.getByRole("button", { name: "新增 Provider" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    const createCall = fetchMock.mock.calls.find(([input, init]) =>
      String(input).endsWith("/api/admin/providers") && init?.method === "POST",
    );
    expect(createCall).toBeDefined();
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({
      name: "Quality Provider",
      type: "openai_compatible",
      base_url: "https://provider.example/v1",
      api_key: "provider-secret",
      catalog_id: "quality-provider",
      options: { tenant_id: "tenant-001" },
    });
  });

  it("preserves the visible and total model counts in the legacy directory summary", async () => {
    const user = userEvent.setup();
    const detail: ProviderCatalogEntry = {
      ...catalogEntry,
      id: "custom",
      models_count: 3,
      models: [
        { id: "model-a", name: "model-a" },
        { id: "model-b", name: "model-b" },
      ],
    };
    const provider: Provider = {
      id: "prv_custom",
      name: "Custom Provider",
      type: "openai_compatible",
      base_url: "https://provider.example/v1",
      status: "active",
      healthy: true,
      priority: 10,
      options: { catalog_id: "custom" },
    };
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ data: detail }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    setActiveLanguage("zh-CN");

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[detail]}
        loading={false}
        mode="edit"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        provider={provider}
        resources={[]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await user.click(screen.getByRole("tab", { name: "模型" }));

    expect(await screen.findByText("2/3 个可引入模型")).toBeInTheDocument();
  });

  it("defaults Anthropic auth selection from adapter metadata", async () => {
    const user = userEvent.setup();
    setActiveLanguage("zh-CN");
    const anthropicEntry = { ...catalogEntry, id: "anthropic-plugin", type: "anthropic" };

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[anthropicEntry]}
        loading={false}
        mode="create"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        providerTypeOptions={[{ value: "anthropic", label: "Anthropic", supportsCustomHeaders: true, authModes: ["bearer"] }]}
        resources={[]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.click(screen.getByRole("tab", { name: "高级" }));

    expect(screen.getByText("Anthropic 认证方式").closest("label")?.querySelector("select")).toHaveValue("bearer");
  });

  it("shows the provider OAuth callback from plugin action metadata", async () => {
    const user = userEvent.setup();
    setActiveLanguage("zh-CN");

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[codexAccountCatalogEntry]}
        loading={false}
        mode="create"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        plugins={[{
          id: "tokenhub.provider.openai-codex",
          name: "OpenAI Codex Subscription",
          version: "built-in",
          source: "built_in",
          kinds: ["provider"],
          placements: [],
          capabilities: [{ kind: "provider_resource_type", name: "openai_subscription", subject: "openai_codex" }],
        }]}
        pluginActions={[{
          plugin_id: "tokenhub.provider.openai-codex",
          action_id: "openai_codex.oauth.start",
          kind: "external_redirect",
          capability: "oauth.start",
          subject: "openai_codex",
          metadata: { oauth_redirect_uri: "https://callback.example/provider/oauth" },
        }]}
        resources={[]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await user.click(screen.getByRole("radio", { name: /账号资源池/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.click(screen.getByRole("button", { name: "下一步" }));

    expect(screen.getByDisplayValue("https://callback.example/provider/oauth")).toBeInTheDocument();
  });

  it("loads account catalogs for non-Codex Provider plugins", async () => {
    const kimiCatalog = {
      ...catalogEntry,
      id: "kimi-subscription",
      name: "Kimi Subscription",
      display_name: "Kimi Subscription",
      type: "kimi_subscription",
    };
    const provider: Provider = {
      id: "prv_kimi",
      name: "Kimi Pool",
      type: "kimi_subscription",
      base_url: "https://kimi.example/api",
      status: "active",
      healthy: true,
      priority: 10,
    };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ data: kimiCatalog }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[kimiCatalog]}
        loading={false}
        mode="edit"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        plugins={[{
          id: "tokenhub.provider.kimi",
          name: "Kimi Subscription",
          version: "built-in",
          source: "built_in",
          kinds: ["provider"],
          placements: [],
          capabilities: [{ kind: "provider_resource_type", name: "kimi_subscription_account", subject: "kimi_subscription" }],
        }]}
        provider={provider}
        resources={[{
          id: "rsrc_kimi",
          provider_id: "prv_kimi",
          name: "Kimi Account",
          resource_type: "kimi_subscription_account",
          status: "active",
          healthy: true,
          priority: 1,
          weight: 100,
        }]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "http://localhost:8080/api/admin/provider-catalog/kimi-subscription?resource_id=rsrc_kimi",
      expect.any(Object),
    ));
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/provider-catalog/openai-codex"))).toBe(false);
  });

  it("renders quota panel copy from non-Codex Provider plugin metadata", async () => {
    const user = userEvent.setup();
    setActiveLanguage("zh-CN");
    const kimiCatalog = {
      ...catalogEntry,
      id: "kimi-subscription",
      name: "Kimi Subscription",
      display_name: "Kimi Subscription",
      type: "kimi_subscription",
    };
    const provider: Provider = {
      id: "prv_kimi",
      name: "Kimi Pool",
      type: "kimi_subscription",
      base_url: "https://kimi.example/api",
      status: "active",
      healthy: true,
      priority: 10,
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/provider-catalog/kimi-subscription")) {
        return new Response(JSON.stringify({ data: kimiCatalog }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (url.includes("/api/admin/plugins/tokenhub.provider.kimi/actions/kimi.quota.read")) {
        return new Response(JSON.stringify({ data: { fetched_at: 123, plan_type: "team" } }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[kimiCatalog]}
        loading={false}
        mode="edit"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        pluginActions={[{
          plugin_id: "tokenhub.provider.kimi",
          action_id: "kimi.quota.read",
          kind: "read",
          capability: "quota.read",
          subject: "kimi_subscription",
          metadata: {
            panel_title: "Kimi subscription quota",
            panel_description: "Quota view provided by the Kimi provider plugin.",
          },
        }]}
        plugins={[{
          id: "tokenhub.provider.kimi",
          name: "Kimi Subscription",
          version: "built-in",
          source: "built_in",
          kinds: ["provider"],
          placements: [],
          capabilities: [{ kind: "provider_resource_type", name: "kimi_subscription_account", subject: "kimi_subscription" }],
        }]}
        provider={provider}
        resources={[{
          id: "rsrc_kimi",
          provider_id: "prv_kimi",
          name: "Kimi Account",
          resource_type: "kimi_subscription_account",
          status: "active",
          healthy: true,
          priority: 1,
          weight: 100,
        }]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/provider-catalog/kimi-subscription"))).toBe(true));
    await user.click(screen.getByRole("tab", { name: "高级" }));

    expect(await screen.findByText("Kimi subscription quota")).toBeInTheDocument();
    expect(screen.getByText("Quota view provided by the Kimi provider plugin.")).toBeInTheDocument();
    expect(screen.queryByText(/ChatGPT\/Codex/)).not.toBeInTheDocument();
  });
});
