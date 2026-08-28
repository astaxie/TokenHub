import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProviderPluginPanels, providerPanelFields, providerPanelFieldValue, providerPanelResources } from "./provider-plugin-panels";

describe("ProviderPluginPanels", () => {
  it("runs provider resource panel actions through the plugin action endpoint", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { status: "ok", access_token: "secret-token" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderPluginPanels
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        provider={{ id: "prv_plugin", name: "Plugin Provider", type: "plugin_provider", status: "active", healthy: true, priority: 10 }}
        resources={[
          { id: "rsrc_plugin", provider_id: "prv_plugin", name: "Plugin Account", resource_type: "plugin_account", status: "active", healthy: true, priority: 10, weight: 1 },
          { id: "rsrc_key", provider_id: "prv_plugin", name: "Plugin API Key", resource_type: "api_key", status: "active", healthy: true, priority: 10, weight: 1 },
          { id: "rsrc_other_provider", provider_id: "prv_other", name: "Other Provider Account", resource_type: "plugin_account", status: "active", healthy: true, priority: 10, weight: 1 },
        ]}
        contributions={[
          {
            plugin_id: "tokenhub.provider.plugin",
            id: "quota",
            slot: "provider.resource.panel",
            title: "Plugin Quota",
            provider_types: ["plugin_provider"],
            resource_types: ["plugin_account"],
            action: "quota.read",
            schema: {
              fields: [
                { name: "provider_type", type: "text", label: "Provider type", source: "provider.type" },
                { name: "resource_count", type: "metric", label: "Resources", source: "resources.length", format: "number" },
                { name: "resource_status", type: "text", label: "Resource status", source: "resource.status" },
              ],
            },
          },
          { plugin_id: "tokenhub.provider.other", id: "quota", slot: "provider.resource.panel", title: "Other Quota", provider_types: ["other_provider"], action: "quota.read" },
        ]}
        actions={[
          { plugin_id: "tokenhub.provider.plugin", action_id: "quota.read", kind: "read", capability: "quota.read", subject: "plugin_provider" },
        ]}
      />,
    );

    expect(screen.getByText("Plugin Quota")).toBeInTheDocument();
    expect(screen.getByText("Provider type")).toBeInTheDocument();
    expect(screen.getByText("plugin_provider")).toBeInTheDocument();
    expect(screen.getByText("Resources")).toBeInTheDocument();
    expect(screen.getByText("1")).toBeInTheDocument();
    expect(screen.getByText("Resource status")).toBeInTheDocument();
    expect(screen.queryByText("Plugin API Key")).not.toBeInTheDocument();
    expect(screen.queryByText("Other Provider Account")).not.toBeInTheDocument();
    expect(screen.queryByText("Other Quota")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /执行插件面板/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.plugin/actions/quota.read");
    expect(JSON.parse(String(init.body))).toEqual({ provider_id: "prv_plugin", resource_id: "rsrc_plugin", refresh: true });
    await waitFor(() => expect(screen.getByText(/\[redacted\]/)).toBeInTheDocument());
    expect(screen.queryByText("secret-token")).not.toBeInTheDocument();
  });

  it("formats provider resource panel schema fields", () => {
    const fields = providerPanelFields({
      plugin_id: "tokenhub.provider.plugin",
      id: "quota",
      slot: "provider.resource.panel",
      schema: {
        fields: [
          { name: "requests", type: "metric", label: "Requests", source: "resource.rate_limit_rpm", format: "compact" },
          { name: "healthy", type: "text", label: "Healthy", source: "resource.healthy" },
          { name: "resource", type: "code_viewer", label: "Resource", source: "resource" },
          { name: "ignored", type: "table", label: "Ignored" },
        ],
      },
    });

    expect(fields).toHaveLength(3);
    expect(providerPanelFieldValue({
      provider: { id: "prv_plugin", name: "Plugin Provider", type: "plugin_provider", status: "active", healthy: true, priority: 10 },
      resource: { id: "rsrc_plugin", provider_id: "prv_plugin", name: "Plugin Account", resource_type: "api_key", status: "active", healthy: true, priority: 10, weight: 1, rate_limit_rpm: 1200 },
      resources: [],
    }, fields[0])).toBe("1.20K");
    expect(providerPanelFieldValue({
      provider: { id: "prv_plugin", name: "Plugin Provider", type: "plugin_provider", status: "active", healthy: true, priority: 10 },
      resource: { id: "rsrc_plugin", provider_id: "prv_plugin", name: "Plugin Account", resource_type: "api_key", status: "active", healthy: true, priority: 10, weight: 1 },
      resources: [],
    }, fields[1])).toBe("true");
    expect(providerPanelFieldValue({
      provider: { id: "prv_plugin", name: "Plugin Provider", type: "plugin_provider", status: "active", healthy: true, priority: 10 },
      resource: { id: "rsrc_plugin", provider_id: "prv_plugin", name: "Plugin Account", resource_type: "api_key", status: "active", healthy: true, priority: 10, weight: 1 },
      resources: [],
    }, fields[2])).toContain("rsrc_plugin");
  });

  it("renders resource system prompt transform panels from declarative layouts", async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn().mockResolvedValue(undefined);
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ resource: { id: "rsrc_plugin" } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderPluginPanels
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        provider={{ id: "prv_plugin", name: "Plugin Provider", type: "plugin_provider", status: "active", healthy: true, priority: 10 }}
        resources={[
          { id: "rsrc_plugin", provider_id: "prv_plugin", name: "Plugin Account", resource_type: "plugin_account", status: "active", healthy: true, priority: 10, weight: 1, options: { system_prompt_transform_policy: "preserve" } },
          { id: "rsrc_other_provider", provider_id: "prv_other", name: "Other Provider Account", resource_type: "plugin_account", status: "active", healthy: true, priority: 10, weight: 1 },
        ]}
        contributions={[{
          plugin_id: "tokenhub.admin.core-provider",
          id: "resource-system-prompt-transform",
          slot: "provider.resource.panel",
          title: "Provider resource system prompt transform",
          schema: { layout: "resource_system_prompt_transform" },
        }]}
        actions={[]}
        onSaved={onSaved}
      />,
    );

    expect(screen.getByLabelText("Plugin Account")).toHaveValue("preserve");
    expect(screen.queryByLabelText("Other Provider Account")).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Plugin Account"), "strip");

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/provider-resources/rsrc_plugin");
    expect(JSON.parse(String(init.body))).toMatchObject({
      provider_id: "prv_plugin",
      options: { system_prompt_transform_policy: "strip" },
    });
  });

  it("scopes provider resource panel resources by Provider and declared resource types", () => {
    expect(providerPanelResources({
      plugin_id: "tokenhub.provider.plugin",
      id: "quota",
      slot: "provider.resource.panel",
      resource_types: ["plugin_account"],
    }, [
      { id: "rsrc_plugin", provider_id: "prv_plugin", name: "Plugin Account", resource_type: "plugin_account", status: "active", healthy: true, priority: 10, weight: 1 },
      { id: "rsrc_key", provider_id: "prv_plugin", name: "Plugin API Key", resource_type: "api_key", status: "active", healthy: true, priority: 10, weight: 1 },
      { id: "rsrc_other", provider_id: "prv_other", name: "Other Account", resource_type: "plugin_account", status: "active", healthy: true, priority: 10, weight: 1 },
    ], "prv_plugin").map((resource) => resource.id)).toEqual(["rsrc_plugin"]);
  });
});
