import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import type { Provider, ProviderCatalogEntry, ProviderModel } from "../core/types";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderUpsertModal } from "./provider-editor";

const catalog: ProviderCatalogEntry = {
  id: "bulk-provider", name: "Bulk Provider", display_name: "Bulk Provider",
  type: "openai_compatible", base_url: "https://bulk.example.test/v1",
  categories: ["openai"], source: "component-test", models_count: 4,
  models: [
    { id: "existing-model", name: "Existing Model", category: "openai" },
    { id: "alpha-one", name: "Alpha One", category: "openai" },
    { id: "alpha-two", name: "Alpha Two", category: "openai" },
    { id: "beta-one", name: "Beta One", category: "openai" },
  ],
};
const provider: Provider = {
  id: "prv_bulk", name: "Bulk Provider", type: catalog.type, base_url: catalog.base_url,
  status: "active", healthy: true, priority: 10, options: { catalog_id: catalog.id },
};
const imported: ProviderModel = {
  id: "pm_existing", provider_id: provider.id, upstream_model: "existing-model", status: "active",
};

function renderEditor(overrides: Partial<ComponentProps<typeof ProviderUpsertModal>> = {}) {
  const onSaved = vi.fn().mockResolvedValue(undefined);
  render(<ProviderUpsertModal
    api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }}
    catalog={[catalog]} loading={false} mode="edit" provider={provider} providerModels={[imported]}
    onClose={vi.fn()} onSaved={onSaved} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()}
    providerTypeOptions={[{ value: catalog.type, label: "OpenAI Compatible", supportsCustomHeaders: true, apiKeyRequired: true }]}
    {...overrides}
  />);
  return { onSaved };
}

function installCatalog() {
  const writes: Array<{ method: string; path: string; body: unknown }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input)).pathname;
    const method = init?.method ?? "GET";
    if (method === "GET" && path === `/api/admin/provider-catalog/${catalog.id}`) {
      return new Response(JSON.stringify({ data: catalog }));
    }
    if ((method === "PATCH" && path === `/api/admin/providers/${provider.id}`)
      || (method === "POST" && path === "/api/admin/providers")) {
      writes.push({ method, path, body: JSON.parse(String(init?.body)) });
      return new Response(JSON.stringify({ provider, imported_models: 1 }), { status: method === "POST" ? 201 : 200 });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  }));
  return writes;
}

describe("Provider model bulk selection", () => {
  beforeEach(() => setActiveLanguage("zh-CN"));
  afterEach(() => { setActiveLanguage("en"); vi.unstubAllGlobals(); });

  it("selects remaining models and preserves hidden selections when clearing filtered models", async () => {
    const user = userEvent.setup();
    const writes = installCatalog();
    const { onSaved } = renderEditor();
    await user.click(screen.getByRole("tab", { name: "模型" }));
    const existing = await screen.findByRole("checkbox", { name: /Existing Model/ });
    expect(existing).toBeChecked();
    expect(existing).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /Beta One/ }));
    await user.click(screen.getByRole("button", { name: "全选" }));
    expect(screen.getByRole("checkbox", { name: /Alpha One/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /Alpha Two/ })).toBeChecked();
    expect(screen.getByText("3 个待引入")).toBeInTheDocument();

    const query = screen.getByPlaceholderText("搜索模型、能力、参数");
    await user.type(query, "alpha");
    await user.click(screen.getByRole("button", { name: "取消全选筛选结果" }));
    expect(screen.getByRole("checkbox", { name: /Alpha One/ })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: /Alpha Two/ })).not.toBeChecked();
    expect(screen.getByText("1 个待引入")).toBeInTheDocument();
    await user.clear(query);
    await user.type(query, "existing-model");
    expect(screen.getByRole("button", { name: "全选筛选结果" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: /Existing Model/ })).toBeChecked();
    await user.clear(query);
    await user.type(query, "no-matching-model");
    expect(screen.getByRole("button", { name: "全选筛选结果" })).toBeDisabled();
    await user.clear(query);
    expect(screen.getByRole("checkbox", { name: /Beta One/ })).toBeChecked();
    expect(writes).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(writes).toEqual([{ method: "PATCH", path: `/api/admin/providers/${provider.id}`, body: expect.objectContaining({ selected_models: ["beta-one"] }) }]);
  });

  it("disables bulk selection when every model has already been imported", async () => {
    const user = userEvent.setup();
    installCatalog();
    renderEditor({ providerModels: catalog.models!.map(model => ({ ...imported, id: `pm_${model.id}`, upstream_model: model.id })) });
    await user.click(screen.getByRole("tab", { name: "模型" }));
    await screen.findByRole("checkbox", { name: /Alpha One/ });
    expect(screen.getByRole("button", { name: "全选" })).toBeDisabled();
    for (const checkbox of screen.getAllByRole("checkbox")) {
      expect(checkbox).toBeChecked();
      expect(checkbox).toBeDisabled();
    }
  });

  it("disables bulk selection while a catalog reload is pending and after it fails", async () => {
    const user = userEvent.setup();
    let resolveReload!: (response: Response) => void;
    const pending = new Promise<Response>(resolve => { resolveReload = resolve; });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      if ((!init?.method || init.method === "GET") && url.pathname === `/api/admin/provider-catalog/${catalog.id}`) {
        if (url.search === "?refresh=true") return pending;
        return new Response(JSON.stringify({ data: catalog }));
      }
      throw new Error(`Unexpected request: ${init?.method ?? "GET"} ${url.pathname}`);
    }));
    renderEditor();
    await user.click(screen.getByRole("tab", { name: "模型" }));
    await screen.findByRole("checkbox", { name: /Alpha One/ });
    expect(screen.getByRole("button", { name: "全选" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "重新加载" }));
    expect(screen.getByRole("button", { name: "全选" })).toBeDisabled();
    expect(screen.getByText("正在加载模型列表...")).toBeInTheDocument();
    await act(async () => resolveReload(new Response("{}", { status: 502 })));
    await screen.findByText("加载失败");
    expect(screen.getByRole("button", { name: "全选" })).toBeDisabled();
  });

  it("disables bulk selection while the provider is saving", async () => {
    const user = userEvent.setup();
    installCatalog();
    renderEditor({ loading: true });
    await user.click(screen.getByRole("tab", { name: "模型" }));
    await screen.findByRole("checkbox", { name: /Alpha One/ });
    expect(screen.getByRole("button", { name: "全选" })).toBeDisabled();
  });

  it("imports the filtered bulk selection through API quick connect", async () => {
    const user = userEvent.setup();
    const writes = installCatalog();
    const { onSaved } = renderEditor({ mode: "create", provider: undefined, providerModels: [] });
    await user.click(screen.getByRole("button", { name: /Bulk Provider/ }));
    await user.type(screen.getByLabelText("API Key", { exact: true }), "synthetic-key");
    await screen.findByRole("switch", { name: "引入 Alpha One" });
    await user.click(screen.getByRole("button", { name: "全选" }));
    for (const item of screen.getAllByRole("switch")) expect(item).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("button", { name: "取消全选" }));
    for (const item of screen.getAllByRole("switch")) expect(item).toHaveAttribute("aria-checked", "false");
    await user.click(screen.getByRole("switch", { name: "引入 Beta One" }));
    const query = screen.getByPlaceholderText("搜索模型、能力、参数");
    await user.type(query, "alpha");
    await user.click(screen.getByRole("button", { name: "全选筛选结果" }));
    for (const item of screen.getAllByRole("switch")) expect(item).toHaveAttribute("aria-checked", "true");
    await user.clear(query);
    expect(screen.getByRole("switch", { name: "移除 Beta One" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("switch", { name: "引入 Existing Model" })).toHaveAttribute("aria-checked", "false");
    expect(writes).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({ method: "POST", path: "/api/admin/providers", body: { selected_models: ["alpha-one", "alpha-two", "beta-one"] } });
  });
});
