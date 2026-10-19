import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Provider, ProviderResource } from "../core/types";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderAPIKeyPool } from "./provider-api-key-pool";
import { ProviderUpsertModal } from "./provider-editor";

const provider: Provider = { id: "prv_pool", name: "Synthetic Provider", type: "openai_compatible", status: "active", healthy: true, priority: 1 };
const resource: ProviderResource = {
  id: "key/one", provider_id: provider.id, name: "Synthetic Key One", resource_type: "api_key",
  status: "active", healthy: true, priority: 2, weight: 75, base_url: "https://region.example.test/v1",
  group: "enterprise", region: "test-region", environment: "test", rate_limit_rpm: 15, token_limit_tpm: 10000, max_concurrency: 3,
  headers: { "X-Synthetic": "masked" }, sensitive_headers: ["X-Synthetic"], options: { plugin_option: "preserve-me" },
};

function setup(resources: ProviderResource[] = [], fetcher = vi.fn().mockResolvedValue(new Response("{}"))) {
  const onSaved = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("fetch", fetcher);
  render(<ProviderAPIKeyPool api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }} provider={provider} resources={resources} onSaved={onSaved} />);
  return { user: userEvent.setup(), fetcher, onSaved };
}

function requestBody(fetcher: ReturnType<typeof vi.fn>, index = 0) {
  return JSON.parse(String(fetcher.mock.calls[index][1].body));
}

describe("Provider API Key pool", () => {
  beforeEach(() => setActiveLanguage("zh-CN"));
  afterEach(() => vi.unstubAllGlobals());

  it("validates empty names and credentials before creating a scoped resource", async () => {
    const { user, fetcher, onSaved } = setup();
    expect(screen.getByText("尚未添加 API Key 资源，当前使用 Provider 默认密钥。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "添加 API Key" }));
    await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
    expect(screen.getByRole("alert")).toHaveTextContent("请填写 API Key 名称。");
    await user.type(screen.getByLabelText("API Key 名称"), "  Test Key  ");
    await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
    expect(screen.getByRole("alert")).toHaveTextContent("请填写 API Key，或选择使用 Provider 已保存的密钥。");
    expect(fetcher).not.toHaveBeenCalled();
    const keyInput = screen.getByLabelText(/^API Key/, { selector: 'input[type="password"]' });
    expect(keyInput).toHaveAttribute("type", "password");
    await user.type(keyInput, "synthetic-key-two");
    await user.click(screen.getByText("调度与限流设置"));
    await user.clear(screen.getByLabelText("权重"));
    await user.type(screen.getByLabelText("权重"), "50");
    await user.clear(screen.getByLabelText("最大并发"));
    await user.type(screen.getByLabelText("最大并发"), "4");
    await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(fetcher.mock.calls[0][0]).toBe("http://localhost:8080/api/admin/provider-resources");
    expect(fetcher.mock.calls[0][1].method).toBe("POST");
    expect(requestBody(fetcher)).toMatchObject({ provider_id: provider.id, resource_type: "api_key", name: "Test Key", api_key: "synthetic-key-two", priority: 1, weight: 50, max_concurrency: 4 });
    expect(screen.queryByLabelText(/^API Key/, { selector: 'input[type="password"]' })).not.toBeInTheDocument();
    expect(screen.queryByText("synthetic-key-two")).not.toBeInTheDocument();
  });

  it("requires an explicit choice to inherit the saved Provider key", async () => {
    const { user, fetcher, onSaved } = setup();
    await user.click(screen.getByRole("button", { name: "添加 API Key" }));
    await user.type(screen.getByLabelText("API Key 名称"), "Default Key");
    await user.type(screen.getByLabelText(/^API Key/, { selector: 'input[type="password"]' }), "discarded-draft-secret");
    await user.click(screen.getByLabelText("使用 Provider 已保存的密钥"));
    expect(screen.queryByLabelText(/^API Key/, { selector: 'input[type="password"]' })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(requestBody(fetcher)).toMatchObject({ provider_id: provider.id, api_key: "", name: "Default Key", rate_limit_rpm: 0, token_limit_tpm: 0, max_concurrency: 0 });
  });

  describe.each(["create", "edit"] as const)("%s scheduling validation", (mode) => {
    it.each([{ field: "priority", label: "优先级" }, { field: "weight", label: "权重" }].flatMap((field) => [
      { ...field, value: "0", description: "zero" },
      { ...field, value: "-1", description: "negative" },
      { ...field, value: "", description: "blank" },
      { ...field, value: "1.5", description: "fractional" },
    ]))("rejects $description $field without saving", async ({ label, value }) => {
      const { user, fetcher, onSaved } = setup(mode === "edit" ? [resource] : []);
      await user.click(screen.getByRole("button", { name: mode === "edit" ? "编辑" : "添加 API Key" }));
      if (mode === "create") {
        await user.type(screen.getByLabelText("API Key 名称"), "Validation Key");
        await user.click(screen.getByLabelText("使用 Provider 已保存的密钥"));
      }
      await user.click(screen.getByText("调度与限流设置"));
      fireEvent.change(screen.getByLabelText(label), { target: { value } });
      await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
      expect(screen.getByRole("alert")).toHaveTextContent("优先级和权重须为正整数，限流和并发须为非负整数。");
      expect(fetcher).not.toHaveBeenCalled();
      expect(onSaved).not.toHaveBeenCalled();
      expect(screen.getByLabelText(label)).toHaveValue(value === "" ? null : Number(value));
    });
  });

  it("keeps blank edited credentials and unrelated advanced settings untouched", async () => {
    const { user, fetcher, onSaved } = setup([resource, { ...resource, id: "other-provider", provider_id: "another-provider", name: "Other Provider Key" }, { ...resource, id: "subscription", resource_type: "openai_subscription", name: "Subscription" }]);
    expect(screen.queryByText("Other Provider Key")).not.toBeInTheDocument();
    expect(screen.queryByText("Subscription")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "编辑" }));
    expect(screen.getByLabelText(/^API Key/, { selector: 'input[type="password"]' })).toHaveValue("");
    await user.clear(screen.getByLabelText("API Key 名称"));
    await user.type(screen.getByLabelText("API Key 名称"), "Renamed Key");
    await user.click(screen.getByText("调度与限流设置"));
    fireEvent.change(screen.getByLabelText("优先级"), { target: { value: "3" } });
    await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(fetcher.mock.calls[0][0]).toBe("http://localhost:8080/api/admin/provider-resources/key%2Fone");
    expect(fetcher.mock.calls[0][1].method).toBe("PATCH");
    expect(requestBody(fetcher)).toMatchObject({ name: "Renamed Key", base_url: resource.base_url, group: resource.group, region: resource.region, environment: resource.environment, healthy: true, priority: 3, weight: 75, rate_limit_rpm: 15, token_limit_tpm: 10000, max_concurrency: 3 });
    for (const key of ["api_key", "options", "headers", "sensitive_headers"]) expect(requestBody(fetcher)).not.toHaveProperty(key);
  });

  it("preserves the secret draft on failure and retries without submitting the outer Provider form", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: "Synthetic save failure" } }), { status: 503 })).mockResolvedValueOnce(new Response("{}"));
    const { user, onSaved } = setup([], fetcher);
    await user.click(screen.getByRole("button", { name: "添加 API Key" }));
    await user.type(screen.getByLabelText("API Key 名称"), "Retry Key");
    await user.type(screen.getByLabelText(/^API Key/, { selector: 'input[type="password"]' }), "synthetic-retry-key");
    await user.click(screen.getByRole("button", { name: "保存 API Key 资源" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Synthetic save failure");
    expect(screen.getByLabelText("API Key 名称")).toHaveValue("Retry Key");
    expect(screen.getByLabelText(/^API Key/, { selector: 'input[type="password"]' })).toHaveValue("synthetic-retry-key");
    expect(onSaved).not.toHaveBeenCalled();
    const event = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    fireEvent(screen.getByLabelText(/^API Key/, { selector: 'input[type="password"]' }), event);
    expect(event.defaultPrevented).toBe(true);
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("updates status without losing scheduling and deletes only after confirmation", async () => {
    const { user, fetcher, onSaved } = setup([resource], vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ success: 1, failed: 0 }))).mockResolvedValueOnce(new Response(null, { status: 204 })));
    await user.click(screen.getByRole("button", { name: "停用" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(fetcher.mock.calls[0][0]).toBe("http://localhost:8080/api/admin/provider-resources/bulk");
    expect(fetcher.mock.calls[0][1].method).toBe("POST");
    expect(requestBody(fetcher)).toEqual({ action: "disable", ids: [resource.id] });
    await user.click(screen.getByRole("button", { name: "删除" }));
    expect(fetcher).toHaveBeenCalledTimes(1);
    const confirmation = screen.getByRole("group", { name: "删除 API Key 资源" });
    expect(confirmation).toHaveTextContent(resource.name);
    await user.click(within(confirmation).getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(2));
    expect(fetcher.mock.calls[1][1]).toMatchObject({ method: "DELETE" });
    expect(fetcher.mock.calls[1][0]).toBe("http://localhost:8080/api/admin/provider-resources/key%2Fone");
  });

  it("enables a disabled unhealthy resource using the recovery action", async () => {
    const disabled = { ...resource, status: "disabled", healthy: false };
    const { user, fetcher, onSaved } = setup([disabled], vi.fn().mockResolvedValue(new Response(JSON.stringify({ success: 1, failed: 0 }))));
    await user.click(screen.getByRole("button", { name: "启用" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(fetcher.mock.calls[0][0]).toBe("http://localhost:8080/api/admin/provider-resources/bulk");
    expect(fetcher.mock.calls[0][1].method).toBe("POST");
    expect(requestBody(fetcher)).toEqual({ action: "enable", ids: [resource.id] });
    expect(screen.getByRole("status")).toHaveTextContent("API Key 资源已更新。");
  });

  it.each([
    { name: "HTTP failure", result: { error: { message: "Synthetic status failure" } }, status: 503, message: "Synthetic status failure" },
    { name: "bulk failure", result: { success: 0, failed: 1, errors: ["Synthetic bulk failure"] }, status: 200, message: "Synthetic bulk failure" },
    { name: "partial bulk failure", result: { success: 1, failed: 1, errors: ["Synthetic partial failure"] }, status: 200, message: "Synthetic partial failure" },
    { name: "incomplete bulk result", result: {}, status: 200, message: "保存失败" },
  ])("reports $name without a success notice and allows retry", async ({ result, status, message }) => {
    const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(result), { status }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ success: 1, failed: 0 })));
    const { user, onSaved } = setup([{ ...resource, status: "disabled", healthy: false }], fetcher);
    await user.click(screen.getByRole("button", { name: "启用" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "启用" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(requestBody(fetcher, 1)).toEqual({ action: "enable", ids: [resource.id] });
  });

  it("does not expose the direct API Key pool for account adapters", async () => {
    const accountProvider = { ...provider, type: "subscription_plugin", options: { catalog_id: "subscription" } };
    const entry = { id: "subscription", name: "Subscription", display_name: "Subscription", type: accountProvider.type, models_count: 0, source: "plugin" };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: entry }))));
    render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }} provider={accountProvider} mode="edit" catalog={[entry]} loading={false} onClose={vi.fn()} onSaved={vi.fn()} setLoading={vi.fn()} setError={vi.fn()} setNotice={vi.fn()} providerAdapters={[{ type: accountProvider.type, capabilities: ["chat"], provider_policy: { credentials_scope: "resource", supports_custom_headers: false } }]} />);
    await waitFor(() => expect(screen.getByRole("tab", { name: "连接" })).toBeVisible());
    expect(screen.queryByRole("region", { name: "API Key 资源池" })).not.toBeInTheDocument();
  });
});
