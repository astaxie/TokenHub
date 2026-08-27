import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProviderAPIQuickCatalog, ProviderAPIQuickConnect } from "./provider-api-quick-connect";

function ProviderHarness() {
  const [values, setValues] = useState<Record<string, string>>({
    name: "Local Test Provider",
    type: "openai_compatible",
    base_url: "",
    api_key: "",
    priority: "10",
  });
  return (
    <ProviderAPIQuickConnect
      api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
      catalogID="custom"
      modelCount={0}
      models={[]}
      modelsLoading={false}
      modelsError=""
      modelQuery=""
      selectedModelCount={0}
      selectedModels={{}}
      activeTab="connect"
      values={values}
      onModelQueryChange={vi.fn()}
      onModelToggle={vi.fn()}
      onReloadModels={vi.fn()}
      onTabChange={vi.fn()}
      onUpdate={(key, value) => setValues((current) => ({ ...current, [key]: value }))}
    />
  );
}

describe("ProviderAPIQuickConnect", () => {
  it("renders provider catalog cards contributed by matching plugins", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(
      <ProviderAPIQuickCatalog
        entries={[
          {
            id: "plugin-kimi",
            name: "Kimi Provider",
            display_name: "Kimi Provider",
            type: "kimi_subscription",
            models_count: 0,
            source: "plugin:local_file",
          },
          {
            id: "plugin-zhipu",
            name: "Zhipu Plugin",
            display_name: "Zhipu Plugin",
            type: "zhipu_subscription",
            models_count: 3,
            source: "plugin:local_file",
          },
          {
            id: "openai",
            name: "OpenAI",
            display_name: "OpenAI",
            type: "openai_compatible",
            models_count: 2,
            source: "builtin",
          },
        ]}
        onQueryChange={vi.fn()}
        onSelect={onSelect}
        onSelectCustom={vi.fn()}
        pluginCatalogCards={[
          {
            plugin_id: "tokenhub.provider.kimi",
            id: "catalog-card",
            slot: "provider.catalog.card",
            title: "Kimi Subscription",
            provider_types: ["kimi_subscription"],
            schema: { description: "Connect a Kimi account from the plugin marketplace." },
          },
          {
            plugin_id: "tokenhub.provider.other",
            id: "catalog-card",
            slot: "provider.catalog.card",
            title: "Other Subscription",
            provider_types: ["other_subscription"],
            schema: { description: "Should not render." },
          },
        ]}
        providerTypeOptions={[
          { value: "openai_compatible", label: "OpenAI Compatible Adapter", supportsCustomHeaders: true },
        ]}
        query=""
        selectedID="plugin-kimi"
        total={3}
      />,
    );

    expect(screen.getByText("Kimi Subscription")).toBeInTheDocument();
    expect(screen.getByText("Connect a Kimi account from the plugin marketplace.")).toBeInTheDocument();
    expect(screen.getByText(/Zhipu Plugin · 3/)).toBeInTheDocument();
    expect(screen.queryByText("Other Subscription")).not.toBeInTheDocument();
    expect(screen.getByText(/OpenAI Compatible Adapter/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Kimi Subscription/ }));
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "plugin-kimi", type: "kimi_subscription" }));
  });

  it("enables connection testing only after required fields are present and sends the expected payload", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ healthy: true, latency_ms: 12 }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    render(<ProviderHarness />);

    const testButton = screen.getByRole("button", { name: "测试连接" });
    expect(testButton).toBeDisabled();

    await user.type(screen.getByLabelText("Base URL"), "http://127.0.0.1:18080/v1");
    await user.type(screen.getByLabelText("API Key"), "provider-secret");
    expect(testButton).toBeEnabled();
    await user.click(testButton);

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("API Key 配置有效"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/providers/test-connection");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("authorization")).toBe("Bearer admin-token");
    expect(JSON.parse(String(init.body))).toMatchObject({
      catalog_id: "custom",
      name: "Local Test Provider",
      type: "openai_compatible",
      base_url: "http://127.0.0.1:18080/v1",
      api_key: "provider-secret",
    });
  });

  it("uses plugin preview action schema to make API keys optional", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ healthy: true, latency_ms: 9 }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    function PluginHarness() {
      const [values, setValues] = useState<Record<string, string>>({
        name: "Plugin Local",
        type: "plugin_local",
        base_url: "",
        api_key: "",
        priority: "10",
      });
      return (
        <ProviderAPIQuickConnect
          api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
          catalogID="plugin-local"
          entry={{ id: "plugin-local", name: "Plugin Local", display_name: "Plugin Local", type: "plugin_local", base_url: "http://127.0.0.1:19090/v1", models_count: 0, source: "plugin:local_file" }}
          modelCount={0}
          models={[]}
          modelsLoading={false}
          modelsError=""
          modelQuery=""
          selectedModelCount={0}
          selectedModels={{}}
          activeTab="connect"
          values={values}
          onModelQueryChange={vi.fn()}
          onModelToggle={vi.fn()}
          onReloadModels={vi.fn()}
          onTabChange={vi.fn()}
          onUpdate={(key, value) => setValues((current) => ({ ...current, [key]: value }))}
          pluginActions={[{ plugin_id: "tokenhub.provider.local", action_id: "plugin_local.models.preview", kind: "read", capability: "models.preview", subject: "plugin_local", input_schema: { type: "object", required: ["base_url"] } }]}
        />
      );
    }

    render(<PluginHarness />);

    const testButton = screen.getByRole("button", { name: "测试连接" });
    expect(testButton).toBeDisabled();
    await user.type(screen.getByLabelText("Base URL"), "http://127.0.0.1:19090/v1");
    expect(screen.getByLabelText("Application Token（可选）")).not.toBeRequired();
    expect(testButton).toBeEnabled();
    await user.click(testButton);

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("连接测试通过"));
    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body))).toMatchObject({
      catalog_id: "plugin-local",
      type: "plugin_local",
      base_url: "http://127.0.0.1:19090/v1",
      api_key: "",
    });
  });
});
