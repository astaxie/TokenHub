import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { providerPluginOptionFieldKey } from "../domain/provider-plugin-options";
import { providerPayload, providerResourcePayload } from "../resources/payloads";
import { ProviderPluginFormSections } from "./provider-plugin-form-sections";

describe("ProviderPluginFormSections", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders matching provider form sections and preserves existing provider options", async () => {
    const user = userEvent.setup();
    const updateSpy = vi.fn();
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const enabledKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "cache_enabled");

    function Harness() {
      const [values, setValues] = useState<Record<string, string>>({ type: "plugin_provider" });
      function onUpdate(key: string, value: string) {
        updateSpy(key, value);
        setValues((current) => ({ ...current, [key]: value }));
      }
      return (
        <ProviderPluginFormSections
          contributions={[
            {
              plugin_id: "tokenhub.provider.plugin",
              id: "account",
              slot: "provider.form.section",
              title: "Plugin Account",
              provider_types: ["plugin_provider"],
              schema: {
                fields: [
                  { name: "tenant_id", type: "text", label: "Tenant ID", required: true },
                  { name: "cache_enabled", type: "switch", label: "Cache Enabled" },
                ],
              },
            },
            {
              plugin_id: "tokenhub.provider.other",
              id: "account",
              slot: "provider.form.section",
              title: "Other Account",
              provider_types: ["other_provider"],
              schema: { fields: [{ name: "tenant_id", type: "text", label: "Other Tenant" }] },
            },
          ]}
          onUpdate={onUpdate}
          provider={{
            id: "prv_plugin",
            name: "Plugin Provider",
            type: "plugin_provider",
            status: "active",
            healthy: true,
            priority: 10,
            options: { tenant_id: "tenant-existing", cache_enabled: "true" },
          }}
          values={values}
        />
      );
    }

    render(<Harness />);

    expect(screen.getByText("Plugin Account")).toBeInTheDocument();
    expect(screen.queryByText("Other Account")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Tenant ID")).toHaveValue("tenant-existing");
    expect(screen.getByText("开启").closest("button")).toHaveAttribute("aria-checked", "true");

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(tenantKey, "tenant-existing");
      expect(updateSpy).toHaveBeenCalledWith(enabledKey, "true");
    });

    await user.clear(screen.getByLabelText("Tenant ID"));
    await user.type(screen.getByLabelText("Tenant ID"), "tenant-updated");
    expect(updateSpy).toHaveBeenLastCalledWith(tenantKey, "tenant-updated");
  });

  it("writes plugin form values into the provider options payload", () => {
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const cacheKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "cache_enabled");
    const data = {
      plugins: [],
      providerCatalog: [],
      providers: [],
      providerAdapters: [{
        type: "plugin_provider",
        capabilities: ["chat"],
        provider_policy: { supports_custom_headers: true, auth_modes: ["oauth", "x-api-key"], system_prompt_transform_default: "preserve" },
      }],
    };
    const payload = providerPayload({
      id: "prv_plugin",
      name: "Plugin Provider",
      type: "plugin_provider",
      status: "active",
      healthy: "true",
      priority: "10",
      base_url: "https://provider.example/v1",
      api_key: "secret",
      provider_auth_mode: "oauth",
      [tenantKey]: "tenant-updated",
      [cacheKey]: "false",
    }, data);

    expect(payload.provider_auth_mode).toBe("oauth");
    expect(payload.anthropic_auth_type).toBe("oauth");
    expect(payload.system_prompt_transform_policy).toBe("preserve");
    expect(payload.claude_code_attribution_policy).toBe("preserve");
    expect(payload.options).toMatchObject({
      tenant_id: "tenant-updated",
      cache_enabled: "false",
    });
  });

  it("hydrates plugin form defaults into the provider options payload", async () => {
    const updateSpy = vi.fn();
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const modeKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "routing_mode");
    const enabledKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "cache_enabled");

    function Harness() {
      const [values, setValues] = useState<Record<string, string>>({ type: "plugin_provider" });
      function onUpdate(key: string, value: string) {
        updateSpy(key, value);
        setValues((current) => ({ ...current, [key]: value }));
      }
      return (
        <ProviderPluginFormSections
          contributions={[{
            plugin_id: "tokenhub.provider.plugin",
            id: "defaults",
            slot: "provider.form.section",
            title: "Plugin Defaults",
            provider_types: ["plugin_provider"],
            schema: {
              fields: [
                { name: "tenant_id", type: "text", label: "Tenant ID", default: "tenant-default" },
                { name: "routing_mode", type: "select", label: "Routing Mode", options: ["balanced", "strict"], default_value: "balanced" },
                { name: "cache_enabled", type: "switch", label: "Cache Enabled", default: true },
              ],
            },
          }]}
          onUpdate={onUpdate}
          values={values}
        />
      );
    }

    render(<Harness />);

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(tenantKey, "tenant-default");
      expect(updateSpy).toHaveBeenCalledWith(modeKey, "balanced");
      expect(updateSpy).toHaveBeenCalledWith(enabledKey, "true");
    });

    const payload = providerPayload({
      id: "prv_plugin",
      name: "Plugin Provider",
      type: "plugin_provider",
      status: "active",
      healthy: "true",
      priority: "10",
      base_url: "https://provider.example/v1",
      api_key: "secret",
      [tenantKey]: "tenant-default",
      [modeKey]: "balanced",
      [enabledKey]: "true",
    });
    expect(payload.options).toMatchObject({
      tenant_id: "tenant-default",
      routing_mode: "balanced",
      cache_enabled: "true",
    });
  });

  it("renders provider resource form sections and writes plugin options into resource payloads", async () => {
    const updateSpy = vi.fn();
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "resource_tenant");
    const modeKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "account_mode");

    function Harness() {
      const [values, setValues] = useState<Record<string, string>>({
        provider_id: "prv_plugin",
        name: "Plugin Account",
        resource_type: "plugin_oauth_account",
        status: "active",
        healthy: "true",
      });
      function onUpdate(key: string, value: string) {
        updateSpy(key, value);
        setValues((current) => ({ ...current, [key]: value }));
      }
      return (
        <ProviderPluginFormSections
          contributions={[
            {
              plugin_id: "tokenhub.provider.plugin",
              id: "resource-account",
              slot: "provider.resource.form.section",
              title: "Plugin Resource",
              provider_types: ["plugin_provider"],
              resource_types: ["plugin_oauth_account"],
              schema: {
                fields: [
                  { name: "resource_tenant", type: "text", label: "Resource Tenant", default: "tenant-default" },
                  { name: "account_mode", type: "select", label: "Account Mode", options: ["standard", "strict"], default: "standard" },
                ],
              },
            },
            {
              plugin_id: "tokenhub.provider.plugin",
              id: "other-resource",
              slot: "provider.resource.form.section",
              title: "Other Resource",
              provider_types: ["plugin_provider"],
              resource_types: ["other_account"],
              schema: { fields: [{ name: "ignored", type: "text", label: "Ignored" }] },
            },
          ]}
          onUpdate={onUpdate}
          providerType="plugin_provider"
          resourceType="plugin_oauth_account"
          slot="provider.resource.form.section"
          values={values}
        />
      );
    }

    render(<Harness />);

    expect(screen.getByText("Plugin Resource")).toBeInTheDocument();
    expect(screen.queryByText("Other Resource")).not.toBeInTheDocument();
    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(tenantKey, "tenant-default");
      expect(updateSpy).toHaveBeenCalledWith(modeKey, "standard");
    });

    const payload = providerResourcePayload({
      provider_id: "prv_plugin",
      name: "Plugin Account",
      resource_type: "plugin_oauth_account",
      status: "active",
      healthy: "true",
      [tenantKey]: "tenant-default",
      [modeKey]: "standard",
    });

    expect(payload.options).toMatchObject({
      resource_tenant: "tenant-default",
      account_mode: "standard",
    });
  });

  it("runs plugin form action buttons with provider context and scoped plugin options", async () => {
    const user = userEvent.setup();
    const updateSpy = vi.fn();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { status: "queued" } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const otherKey = providerPluginOptionFieldKey("tokenhub.provider.other", "tenant_id");

    render(
      <ProviderPluginFormSections
        actions={[{
          plugin_id: "tokenhub.provider.plugin",
          action_id: "plugin.sync",
          kind: "mutate",
          capability: "provider.sync",
          subject: "plugin_provider",
        }]}
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        contributions={[{
          plugin_id: "tokenhub.provider.plugin",
          id: "actions",
          slot: "provider.form.section",
          title: "Plugin Actions",
          provider_types: ["plugin_provider"],
          schema: {
            fields: [
              { name: "tenant_id", type: "text", label: "Tenant ID" },
              { name: "sync", type: "action_button", label: "Sync Catalog", action: "plugin.sync" },
            ],
          },
        }]}
        onUpdate={updateSpy}
        provider={{
          id: "prv_plugin",
          name: "Plugin Provider",
          type: "plugin_provider",
          base_url: "https://provider.example/v1",
          status: "active",
          healthy: true,
          priority: 10,
        }}
        values={{
          id: "draft-id",
          name: "Draft Provider",
          type: "plugin_provider",
          base_url: "https://draft.example/v1",
          api_key: "must-not-leak",
          [tenantKey]: "tenant-001",
          [otherKey]: "tenant-other",
        }}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Sync Catalog" }));

    await waitFor(() => expect(screen.getByText(/queued/u)).toBeInTheDocument());
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.plugin/actions/plugin.sync");
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_plugin",
      provider_type: "plugin_provider",
      resource_type: "",
      provider: {
        id: "prv_plugin",
        name: "Plugin Provider",
        type: "plugin_provider",
        base_url: "https://provider.example/v1",
      },
      options: { tenant_id: "tenant-001" },
    });
    expect(String(init.body)).not.toContain("must-not-leak");
    expect(String(init.body)).not.toContain("tenant-other");
  });

  it("opens plugin OAuth action redirect URLs", async () => {
    const user = userEvent.setup();
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { auth_url: "https://provider.example/oauth" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderPluginFormSections
        actions={[{
          plugin_id: "tokenhub.provider.plugin",
          action_id: "plugin.oauth.start",
          kind: "external_redirect",
          capability: "oauth.start",
          subject: "plugin_provider",
        }]}
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        contributions={[{
          plugin_id: "tokenhub.provider.plugin",
          id: "oauth",
          slot: "provider.form.section",
          title: "Plugin OAuth",
          provider_types: ["plugin_provider"],
          schema: {
            fields: [
              { name: "oauth", type: "oauth_button", label: "Authorize Account", action: "plugin.oauth.start" },
            ],
          },
        }]}
        onUpdate={vi.fn()}
        values={{ type: "plugin_provider" }}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Authorize Account" }));

    await waitFor(() => expect(openSpy).toHaveBeenCalledWith("https://provider.example/oauth", "_blank", "noopener,noreferrer"));
  });
});
