import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type Provider, type ProviderCatalogEntry } from "../core/types";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderUpsertModal } from "./provider-editor";

const customCatalog: ProviderCatalogEntry = {
  id: "custom",
  name: "Custom",
  display_name: "Custom",
  type: "openai_compatible",
  base_url: "https://provider.example/v1",
  categories: ["openai"],
  models_count: 0,
  source: "component-test",
};

const provider: Provider = {
  id: "prv_custom",
  name: "Custom Provider",
  type: "openai_compatible",
  base_url: "https://provider.example/v1",
  status: "active",
  healthy: true,
  priority: 10,
  options: { catalog_id: "custom", custom_option: "keep" },
};

describe("manual provider models in the editor", () => {
  afterEach(() => {
    setActiveLanguage("en");
    vi.unstubAllGlobals();
  });

  it("saves a typed upstream model as a custom catalog entry", async () => {
    setActiveLanguage("zh-CN");
    const user = userEvent.setup();
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PATCH") {
        return new Response(JSON.stringify({ provider, imported_models: 1 }), { status: 200 });
      }
      return new Response(JSON.stringify({ error: { message: "Discovery unavailable" } }), { status: 502 });
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderUpsertModal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalog={[customCatalog]}
        loading={false}
        mode="edit"
        onClose={vi.fn()}
        onSaved={vi.fn().mockResolvedValue(undefined)}
        provider={provider}
        providerModels={[{ id: "model-1", provider_id: provider.id, upstream_model: "existing", status: "active" }]}
        resources={[]}
        setError={vi.fn()}
        setLoading={vi.fn()}
        setNotice={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("tab", { name: "模型" }));
    const id = screen.getByLabelText("上游模型 ID");
    await user.type(id, "existing");
    await user.click(screen.getByRole("button", { name: "添加手动模型" }));
    expect(screen.getByRole("alert")).toHaveTextContent("该模型已引入。");

    await user.clear(id);
    await user.type(id, "private-model-id");
    await user.type(screen.getByLabelText("模型显示名称（可选）"), "Private Model");
    await user.click(screen.getByRole("button", { name: "添加手动模型" }));
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));
    const call = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH");
    const payload = JSON.parse(String(call?.[1]?.body));
    expect(payload).toMatchObject({
      catalog_id: "custom",
      model_category: "",
      preserve_catalog: true,
      selected_models: ["private-model-id"],
      custom_models: [{ id: "private-model-id", name: "Private Model", display_name: "Private Model" }],
      options: { custom_option: "keep" },
    });
  });
});
