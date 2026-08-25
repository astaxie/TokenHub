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

const codexCatalog: ProviderCatalogEntry = {
  id: "openai-codex",
  name: "OpenAI Codex",
  display_name: "OpenAI Codex",
  type: "openai_codex",
  base_url: "https://chatgpt.com/backend-api/codex",
  categories: ["codex"],
  models_count: 0,
  source: "openai-codex-live",
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function stubProviderEditorFetch(extra?: (url: string, init?: RequestInit) => Response | undefined) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const extraResponse = extra?.(url, init);
    if (extraResponse) return extraResponse;
    if (url.includes("/api/admin/provider-catalog/")) {
      const id = decodeURIComponent(url.split("/api/admin/provider-catalog/")[1]?.split("?")[0] ?? "");
      const data = id === grokCatalog.id ? grokCatalog : id === codexCatalog.id ? codexCatalog : catalogEntry;
      return jsonResponse({ data });
    }
    return jsonResponse({});
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderCreateEditor() {
  return render(
    <ProviderUpsertModal
      api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
      catalog={[catalogEntry, grokCatalog, codexCatalog]}
      loading={false}
      mode="create"
      onClose={vi.fn()}
      onSaved={vi.fn().mockResolvedValue(undefined)}
      resources={[]}
      setError={vi.fn()}
      setLoading={vi.fn()}
      setNotice={vi.fn()}
      standardModels={[]}
    />,
  );
}

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

  it("clears vendor credentials when switching between Codex and Super Grok", async () => {
    const user = userEvent.setup();
    stubProviderEditorFetch();
    renderCreateEditor();

    await user.click(screen.getByRole("radio", { name: /账号资源池/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    const advancedToken = screen.getByText("高级：手动粘贴 Token").closest("details");
    expect(advancedToken).not.toBeNull();
    advancedToken!.open = true;
    const accessToken = () => advancedToken!.querySelector('[data-field-key="access_token"] input') as HTMLInputElement;
    await user.type(accessToken(), "codex-access-token");
    expect(screen.getByText("已回填账号 Token")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("账号池通道"), "xai-grok");
    expect(screen.getByText("等待授权回填")).toBeInTheDocument();
    expect(accessToken()).toHaveValue("");
    expect(screen.getByText("Super Grok 授权")).toBeInTheDocument();
    expect(screen.queryByText("回调结果")).not.toBeInTheDocument();

    await user.type(accessToken(), "grok-access-token");
    expect(screen.getByText("已回填账号 Token")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("账号池通道"), "openai-codex");
    expect(screen.getByText("等待授权回填")).toBeInTheDocument();
    expect(accessToken()).toHaveValue("");
    expect(screen.getByText("OpenAI/Codex 授权")).toBeInTheDocument();
    expect(screen.getByText("回调结果")).toBeInTheDocument();
  });

  it("resets account-only catalogs when returning to a direct API key", async () => {
    const user = userEvent.setup();
    stubProviderEditorFetch();
    renderCreateEditor();

    await user.click(screen.getByRole("radio", { name: /账号资源池/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.selectOptions(screen.getByLabelText("账号池通道"), "xai-grok");
    await user.click(screen.getByRole("button", { name: "上一步" }));
    await user.click(screen.getByRole("radio", { name: /直接 API Key/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));

    expect(screen.queryByLabelText("账号池通道")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Quality Provider" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("https://provider.example/v1")).toBeInTheDocument();
    expect(screen.getByLabelText("API Key", { exact: true })).toBeInTheDocument();
    expect(screen.queryByDisplayValue("https://cli-chat-proxy.grok.com/v1")).not.toBeInTheDocument();
  });

  it("renders Super Grok device-code guidance instead of the Codex callback UI", async () => {
    const user = userEvent.setup();
    stubProviderEditorFetch();
    renderCreateEditor();

    await user.click(screen.getByRole("radio", { name: /账号资源池/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.selectOptions(screen.getByLabelText("账号池通道"), "xai-grok");
    await user.click(screen.getByRole("button", { name: "下一步" }));

    expect(screen.getByText("使用 Super Grok 设备码授权账号；TokenHub 会换取并保存订阅 Token。")).toBeInTheDocument();
    expect(screen.getByText("Super Grok 授权")).toBeInTheDocument();
    expect(screen.getByText("点击后由后端开始 xAI 设备码授权。在 xAI 页面确认用户码；无需 localhost 回调。")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("http://localhost:1455/auth/callback")).not.toBeInTheDocument();
    expect(screen.queryByText("回调结果")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "解析回填" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "复制固定回调地址" })).not.toBeInTheDocument();
    expect(screen.queryByText("使用 OpenAI/Codex OAuth 授权账号；TokenHub 会在后端换取并保存账号 Token。")).not.toBeInTheDocument();
  });
});
