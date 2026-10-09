import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginDescriptor, ProviderCatalogEntry } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

const catalog: ProviderCatalogEntry = { id: "ui-direct", name: "UI Service", display_name: "UI Service", type: "openai_compatible", base_url: "https://api.example.test/v1", models_count: 1, source: "test", models: [{ id: "ui-chat", name: "UI Chat", category: "openai" }] };
const options = [{ value: "openai_compatible", label: "OpenAI Compatible", supportsCustomHeaders: true, apiKeyRequired: true, defaultCatalogProviderType: true }];

function renderProvider(overrides: Partial<React.ComponentProps<typeof ProviderUpsertModal>> = {}) {
  const onSaved = vi.fn().mockResolvedValue(undefined);
  const setError = vi.fn();
  render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }} catalog={[catalog]} loading={false} mode="create" onClose={vi.fn()} onSaved={onSaved} setError={setError} setLoading={vi.fn()} setNotice={vi.fn()} providerTypeOptions={options} {...overrides} />);
  return { onSaved, setError };
}

describe("Provider onboarding", () => {
  it("preserves the next provider draft when a previous connection test completes", async () => {
    const user = userEvent.setup();
    const alternate: ProviderCatalogEntry = { ...catalog, id: "ui-alternate", name: "Alternate Service", display_name: "Alternate Service", models: [{ id: "alternate-chat", name: "Alternate Chat", category: "openai" }] };
    let resolveConnection!: (response: Response) => void;
    const connectionResponse = new Promise<Response>(resolve => { resolveConnection = resolve; });
    const requests: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      requests.push(url);
      if (url.endsWith("/providers/test-connection")) return connectionResponse;
      if (url.includes(`/provider-catalog/${alternate.id}`)) return new Response(JSON.stringify({ data: alternate }));
      if (url.includes(`/provider-catalog/${catalog.id}`)) return new Response(JSON.stringify({ data: catalog }));
      throw new Error(`Unexpected request: ${url}`);
    }));
    renderProvider({ catalog: [catalog, alternate] });
    await user.click(screen.getByRole("button", { name: /UI Service/ }));
    await user.type(screen.getByLabelText("API Key", { exact: true }), "synthetic-key");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(requests.some(url => url.endsWith("/providers/test-connection"))).toBe(true));
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await user.click(screen.getByRole("button", { name: /Alternate Service/ }));
    await user.click(await screen.findByRole("switch", { name: "引入 Alternate Chat" }));
    const alternateRequests = requests.filter(url => url.includes(`/provider-catalog/${alternate.id}`)).length;

    await act(async () => resolveConnection(new Response(JSON.stringify({ healthy: true, latency_ms: 1 }), { status: 200 })));

    expect(screen.getByRole("switch", { name: "移除 Alternate Chat" })).toHaveAttribute("aria-checked", "true");
    expect(requests.filter(url => url.includes(`/provider-catalog/${alternate.id}`))).toHaveLength(alternateRequests);
  });

  it("requires a model selection and imports it without publishing a model or creating a route", async () => {
    const user = userEvent.setup();
    const requests: Array<{ url: string; method: string; body?: unknown }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      requests.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (url.includes("/provider-catalog/ui-direct")) return new Response(JSON.stringify({ data: catalog }));
      if (url.endsWith("/api/admin/providers") && init?.method === "POST") return new Response(JSON.stringify({ provider: { id: "created-provider" }, imported_models: 1 }), { status: 201 });
      throw new Error(`Unexpected request: ${url}`);
    }));
    const { onSaved, setError } = renderProvider();
    expect(screen.queryByRole("radiogroup", { name: "选择接入方式" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /UI Service/ }));
    await user.type(screen.getByLabelText("API Key", { exact: true }), "synthetic-key");
    await screen.findByRole("switch", { name: "引入 UI Chat" });
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(screen.getByText("高级连接设置").closest("details")).not.toHaveAttribute("open");
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    expect(setError).toHaveBeenLastCalledWith("请至少选择一个要引入 Provider 的上游模型。");
    expect(screen.getByRole("alert")).toHaveTextContent("请至少选择一个要引入 Provider 的上游模型。");
    expect(requests.filter(request => request.method === "POST")).toHaveLength(0);
    await user.click(screen.getByRole("switch", { name: "引入 UI Chat" }));
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    const writes = requests.filter(request => request.method === "POST");
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({ url: "http://localhost:8080/api/admin/providers", body: { selected_models: ["ui-chat"], api_key: "synthetic-key", catalog_id: catalog.id } });
  });

  it("keeps the selected models and credentials when saving fails", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => String(input).includes("provider-catalog")
      ? new Response(JSON.stringify({ data: catalog }))
      : new Response(JSON.stringify({ error: { message: "Synthetic save failed" } }), { status: 500 })));
    const { onSaved, setError } = renderProvider();
    await user.click(screen.getByRole("button", { name: /UI Service/ }));
    await user.type(screen.getByLabelText("API Key", { exact: true }), "synthetic-key");
    await user.click(await screen.findByRole("switch", { name: "引入 UI Chat" }));
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    await waitFor(() => expect(setError).toHaveBeenLastCalledWith("Synthetic save failed"));
    expect(screen.getByRole("alert")).toHaveTextContent("Synthetic save failed");
    expect(screen.getByLabelText("API Key", { exact: true })).toHaveValue("synthetic-key");
    expect(screen.getByRole("switch", { name: "移除 UI Chat" })).toHaveAttribute("aria-checked", "true");
    expect(onSaved).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await user.click(screen.getByRole("button", { name: /UI Service/ }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("preserves an edited connection during automatic discovery and invalidates the previous model selection", async () => {
    const user = userEvent.setup();
    const discovery: Array<Record<string, unknown>> = [];
    const providerWrites: unknown[] = [];
    const proxyURL = "https://proxy.example.test/v1";
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const body = init?.body ? JSON.parse(String(init.body)) : {};
      if (url.includes("/provider-catalog/ui-direct")) {
        discovery.push(body);
        const models = body.base_url === proxyURL ? [{ id: "proxy-chat", name: "Proxy Chat", category: "openai" }] : catalog.models;
        return new Response(JSON.stringify({ data: { ...catalog, models } }));
      }
      if (url.endsWith("/api/admin/providers")) {
        providerWrites.push(body);
        return new Response(JSON.stringify({ provider: { id: "created-provider" }, imported_models: 1 }), { status: 201 });
      }
      throw new Error(`Unexpected request: ${url}`);
    }));
    const { onSaved } = renderProvider({
      providerTypeOptions: [...options, { ...options[0], value: "alternate-compatible", label: "Alternate API" }],
      pluginActions: [{ plugin_id: "test.preview", action_id: "models.preview", capability: "models.preview", subject: catalog.type, kind: "read" }],
    });
    await user.click(screen.getByRole("button", { name: /UI Service/ }));
    fireEvent.change(screen.getByLabelText("API Key", { exact: true }), { target: { value: "synthetic-key" } });
    await waitFor(() => expect(discovery.at(-1)?.api_key).toBe("synthetic-key"));
    await user.click(await screen.findByRole("switch", { name: "引入 UI Chat" }));
    await user.click(screen.getByText("高级连接设置"));
    fireEvent.change(screen.getByLabelText("渠道名称"), { target: { value: "Enterprise Proxy" } });
    fireEvent.change(screen.getByLabelText("渠道商类型"), { target: { value: "alternate-compatible" } });
    fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: proxyURL } });
    fireEvent.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    expect(providerWrites).toHaveLength(0);
    expect(screen.getByRole("alert")).toHaveTextContent("请先加载自定义渠道的上游模型");
    await waitFor(() => expect(discovery.at(-1)).toMatchObject({ name: "Enterprise Proxy", type: "alternate-compatible", base_url: proxyURL, api_key: "synthetic-key" }));
    const nextModel = await screen.findByRole("switch", { name: "引入 Proxy Chat" });
    expect(screen.getByLabelText("渠道名称")).toHaveValue("Enterprise Proxy");
    expect(screen.getByLabelText("渠道商类型")).toHaveValue("alternate-compatible");
    expect(screen.getByLabelText("Base URL")).toHaveValue(proxyURL);
    expect(screen.queryByRole("switch", { name: "移除 UI Chat" })).not.toBeInTheDocument();
    await user.click(nextModel);
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(providerWrites).toEqual([expect.objectContaining({ name: "Enterprise Proxy", type: "alternate-compatible", base_url: proxyURL, selected_models: ["proxy-chat"] })]);
  });

  it("initializes custom connections from a direct default and preserves the same custom draft", async () => {
    const user = userEvent.setup();
    const alternate = { ...catalog, id: "ui-alternate", name: "Alternate Service", display_name: "Alternate Service", type: "alternate-compatible" };
    const discovery: Array<Record<string, unknown>> = [];
    const providerWrites: unknown[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const body = init?.body ? JSON.parse(String(init.body)) : {};
      if (url.includes("/provider-catalog/custom")) {
        discovery.push(body);
        return new Response(JSON.stringify({ data: { ...catalog, id: "custom", type: body.type } }));
      }
      if (url.includes("/provider-catalog/")) return new Response(JSON.stringify({ data: url.endsWith(alternate.id) ? alternate : catalog }));
      if (url.endsWith("/api/admin/providers")) {
        providerWrites.push(body);
        return new Response(JSON.stringify({ provider: { id: "created-provider" }, imported_models: 1 }), { status: 201 });
      }
      throw new Error(`Unexpected request: ${url}`);
    }));
    const { onSaved } = renderProvider({
      catalog: [catalog, alternate],
      providerTypeOptions: [...options, { ...options[0], value: alternate.type, label: "Alternate API", defaultCatalogProviderType: false, authModes: ["x-api-key", "bearer"] }],
    });
    await user.click(screen.getByRole("button", { name: /Alternate Service/ }));
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await user.click(screen.getByRole("button", { name: /自定义供应商/ }));
    expect(screen.getByLabelText("渠道商类型")).toHaveValue("openai_compatible");
    await user.selectOptions(screen.getByLabelText("渠道商类型"), alternate.type);
    fireEvent.change(screen.getByLabelText("渠道名称"), { target: { value: "Custom Draft" } });
    fireEvent.change(screen.getByLabelText("Base URL"), { target: { value: "https://custom.example.test/v1" } });
    fireEvent.change(screen.getByLabelText("API Key", { exact: true }), { target: { value: "synthetic-key" } });
    await user.click(screen.getByText("高级连接设置"));
    await user.selectOptions(screen.getByLabelText(/^认证方式/), "bearer");
    await user.selectOptions(screen.getByLabelText(/^系统提示词转换/), "preserve");
    await waitFor(() => expect(discovery.at(-1)).toMatchObject({ type: alternate.type, provider_auth_mode: "bearer", api_key: "synthetic-key" }));
    await user.click(await screen.findByRole("switch", { name: "引入 UI Chat" }));
    const loadedCatalogs = discovery.length;
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await user.click(screen.getByRole("button", { name: /自定义供应商/ }));
    expect(screen.getByLabelText("渠道商类型")).toHaveValue(alternate.type);
    expect(screen.getByLabelText("渠道名称")).toHaveValue("Custom Draft");
    expect(screen.getByLabelText("Base URL")).toHaveValue("https://custom.example.test/v1");
    expect(screen.getByLabelText("API Key", { exact: true })).toHaveValue("synthetic-key");
    expect(screen.getByLabelText(/^认证方式/)).toHaveValue("bearer");
    expect(screen.getByLabelText(/^系统提示词转换/)).toHaveValue("preserve");
    expect(screen.getByRole("switch", { name: "移除 UI Chat" })).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(discovery).toHaveLength(loadedCatalogs);
    expect(providerWrites).toEqual([expect.objectContaining({ type: alternate.type, selected_models: ["ui-chat"], system_prompt_transform_policy: "preserve" })]);
  });

  it("clears account credentials between service cards and restores an API adapter for custom connections", async () => {
    const user = userEvent.setup();
    const accounts = ["alpha", "beta"].map(name => ({ ...catalog, id: name, name: `UI ${name}`, display_name: `UI ${name}`, type: `${name}_subscription`, models: [], models_count: 0 }));
    const plugins: PluginDescriptor[] = accounts.map(entry => ({ id: entry.id, name: entry.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: `${entry.id}_account`, subject: entry.type }] }));
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ data: catalog }))));
    renderProvider({ catalog: [catalog, ...accounts], plugins, providerTypeOptions: [...options, ...accounts.map(entry => ({ value: entry.type, label: entry.name, supportsCustomHeaders: false, apiKeyRequired: false, defaultCatalogProviderType: false, systemPromptTransformDefault: "preserve" }))] });
    await user.click(screen.getByRole("button", { name: /UI alpha/ }));
    await user.click(screen.getByText("高级：手动粘贴 Token"));
    await user.type(screen.getByLabelText(/^访问 Token/), "synthetic-account-token");
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await user.click(screen.getByRole("button", { name: /UI beta/ }));
    await user.click(screen.getByText("高级：手动粘贴 Token"));
    expect(screen.getByLabelText(/^访问 Token/)).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await user.click(screen.getByRole("button", { name: /自定义供应商/ }));
    await user.click(screen.getByText("高级连接设置"));
    expect(screen.getByLabelText("渠道商类型")).toHaveValue("openai_compatible");
    expect(screen.getByLabelText(/^系统提示词转换/)).toHaveValue("strip");
    expect(screen.getByLabelText("API Key", { exact: true })).toHaveValue("");
  });
});
