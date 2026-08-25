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
});
