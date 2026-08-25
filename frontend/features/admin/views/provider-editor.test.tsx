import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { type ProviderCatalogEntry } from "../core/types";
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
    });
  });

  it("does not offer Codex quota or live-test actions in a Super Grok editor", async () => {
    const user = userEvent.setup();
    const grokCatalog: ProviderCatalogEntry = {
      id: "xai-grok",
      name: "Super Grok",
      display_name: "Super Grok",
      type: "xai_grok",
      base_url: "https://cli-chat-proxy.grok.com/v1",
      categories: ["grok"],
      models_count: 0,
      source: "xai-grok-subscription",
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/admin/provider-catalog/")) {
        return new Response(JSON.stringify({ data: grokCatalog }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (url.includes("/quota")) {
        throw new Error("Super Grok editors must not query Codex quota");
      }
      return new Response(JSON.stringify({}), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[grokCatalog]}
        loading={false}
        mode="edit"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        provider={{
          id: "prv_grok",
          name: "Super Grok",
          type: "xai_grok",
          base_url: grokCatalog.base_url,
          status: "active",
          healthy: true,
          priority: 10,
          options: { catalog_id: "xai-grok", model_category: "grok" },
        }}
        resources={[{
          id: "res_grok",
          provider_id: "prv_grok",
          name: "Super Grok Account",
          resource_type: "xai_subscription",
          status: "active",
          healthy: true,
          priority: 10,
          weight: 1,
        }]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
        standardModels={[]}
      />,
    );

    await user.click(screen.getByRole("tab", { name: "高级" }));
    expect(screen.queryByRole("heading", { name: "订阅额度" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "查询用量与重置次数" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Codex 真实请求测试" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/quota"))).toBe(false);
  });
});
