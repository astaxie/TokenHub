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
});
