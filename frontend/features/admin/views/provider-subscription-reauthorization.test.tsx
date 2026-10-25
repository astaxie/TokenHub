import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor, PluginDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../core/types";
import type { ProviderAccountOAuthResult } from "../core/session";
import { ProviderUpsertModal } from "./provider-editor";

const provider: Provider = { id: "prv_reauthorization", name: "Synthetic subscription", type: "review_subscription", status: "active", healthy: true, priority: 1 };
const catalog: ProviderCatalogEntry = { id: "review-subscription", name: provider.name, display_name: provider.name, type: provider.type, source: "fixture", models_count: 0, models: [] };
const plugin: PluginDescriptor = { id: "test.reauthorization", name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "review_account", subject: provider.type }] };
const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: plugin.id, action_id: capability, capability, subject: provider.type, kind: "mutate", metadata: { subscription_auth: "true", subscription_session_json: "true", provider_resource_type: "review_account" } }));
const resources: ProviderResource[] = ["first", "second"].map(name => ({ id: `account_${name}`, provider_id: provider.id, name, resource_type: "review_account", status: "active", healthy: true, priority: 1, weight: 100, credential_summary: { account_email: `${name}@example.invalid`, account_id: `${name}-identity`, auth_type: "oauth" } }));

function setup(failSave = false, exchange?: Promise<ProviderAccountOAuthResult>) {
  const requests: Array<{ method: string; path: string; body?: Record<string, unknown> }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input)).pathname;
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    requests.push({ method, path, body });
    if (path.includes("provider-catalog")) return new Response(JSON.stringify({ data: catalog }));
    if (path.endsWith("oauth.methods")) return new Response(JSON.stringify({ data: { methods: [{ id: "token", label: "Synthetic token", type: "api", prompts: [{ key: "key", label: "Synthetic setup token" }] }] } }));
    if (path.endsWith("oauth.exchange")) return new Response(JSON.stringify({ data: exchange ? await exchange : { access_token: "synthetic-replacement-token", catalog } }));
    if (method === "PATCH" && path.endsWith("account_first")) {
      if (failSave) { failSave = false; return new Response(JSON.stringify({ error: { message: "Synthetic save failure" } }), { status: 503 }); }
      return new Response(JSON.stringify({ data: resources[0] }));
    }
    throw new Error(`Undeclared request: ${method} ${path}`);
  }));
  const onSaved = vi.fn().mockResolvedValue(undefined);
  const view = render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }} catalog={[catalog]} provider={provider} plugins={[plugin]} pluginActions={[...actions, ...actions.map(action => ({ ...action, subject: "different_subscription" }))]} providerTypeOptions={[{ value: provider.type, label: "Synthetic subscription", supportsCustomHeaders: false }, { value: "different_subscription", label: "Different subscription", supportsCustomHeaders: false }]} resources={resources} loading={false} mode="edit" onClose={vi.fn()} onSaved={onSaved} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()} />);
  return { ...view, requests, onSaved, user: userEvent.setup() };
}

async function authorize(user: ReturnType<typeof userEvent.setup>) {
  await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), "account_first");
  await user.click(screen.getByRole("button", { name: "重新授权账号" }));
  const panel = within(screen.getByRole("region", { name: "更新订阅账号凭据" }));
  await user.click(panel.getByText("导入订阅会话", { selector: "summary" }));
  fireEvent.change(panel.getByLabelText(/^订阅会话 JSON/), { target: { value: '{"refresh":"synthetic-session"}' } });
  await user.click(panel.getByRole("button", { name: "导入订阅会话" }));
  await waitFor(() => expect(panel.getByRole("button", { name: "保存新账号凭据" })).toBeEnabled());
  return panel;
}

describe("Saved subscription account reauthorization", () => {
  it("requires an explicit account and saves only the replacement credential snapshot", async () => {
    const { user, requests, onSaved } = setup();
    expect(screen.getByText("请在顶部账号列表中选择一个账号，再更新其凭据。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "重新授权账号" })).not.toBeInTheDocument();
    const panel = await authorize(user);
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
    await user.click(panel.getByRole("button", { name: "保存新账号凭据" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    const patch = requests.find(request => request.method === "PATCH")!;
    expect(patch.path).toBe("/api/admin/provider-resources/account_first");
    expect(Object.keys(patch.body!)).toEqual(["credentials"]);
    expect(patch.body?.credentials).toMatchObject({ replace: true, access_token: "synthetic-replacement-token", session_json: "", email: "", account_id: "", refresh_token: "" });
    expect(panel.getByText("账号凭据已更新。")).toBeVisible();
  });

  it("does not transfer pending credentials to another account", async () => {
    const { user, requests } = setup();
    await authorize(user);
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), "account_second");
    expect(screen.queryByRole("button", { name: "保存新账号凭据" })).not.toBeInTheDocument();
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
  });

  it("retains the replacement for retry when saving fails", async () => {
    const { user, requests, onSaved } = setup(true);
    const panel = await authorize(user);
    await user.click(panel.getByRole("button", { name: "保存新账号凭据" }));
    expect(await panel.findByRole("alert")).toHaveTextContent("Synthetic save failure");
    expect(onSaved).not.toHaveBeenCalled();
    await user.click(panel.getByRole("button", { name: "保存新账号凭据" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(2);
  });

  it("ignores a late import response after switching the selected account", async () => {
    let finish!: (result: ProviderAccountOAuthResult) => void;
    const { user, requests } = setup(false, new Promise(resolve => { finish = resolve; }));
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), "account_first");
    await user.click(screen.getByRole("button", { name: "重新授权账号" }));
    await user.click(screen.getByText("导入订阅会话", { selector: "summary" }));
    fireEvent.change(screen.getByLabelText(/^订阅会话 JSON/), { target: { value: '{"access":"pending-session"}' } });
    await user.click(screen.getByRole("button", { name: "导入订阅会话" }));
    await waitFor(() => expect(requests.some(request => request.path.endsWith("oauth.exchange"))).toBe(true));
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), "account_second");
    finish({ access_token: "late-first-account-token" });
    await user.click(screen.getByRole("button", { name: "重新授权账号" }));
    expect(screen.getByRole("button", { name: "保存新账号凭据" })).toBeDisabled();
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
  });

  it("reports a refresh failure separately after the credentials are saved", async () => {
    const { user, requests, onSaved } = setup();
    onSaved.mockRejectedValueOnce(new Error("Synthetic refresh failure"));
    const panel = await authorize(user);
    await user.click(panel.getByRole("button", { name: "保存新账号凭据" }));
    expect(await panel.findByRole("alert")).toHaveTextContent("账号凭据已保存，但页面刷新失败。请重新打开编辑器。");
    expect(panel.getByRole("status")).toHaveTextContent("账号凭据已更新。");
    expect(panel.queryByRole("button", { name: "保存新账号凭据" })).not.toBeInTheDocument();
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(1);
  });

  it("does not authorize a saved account through an unsaved provider type", async () => {
    const { user, requests } = setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), "account_first");
    await user.click(screen.getByRole("tab", { name: "高级" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "兼容协议" }), "different_subscription");
    await user.click(screen.getByRole("tab", { name: "连接" }));
    expect(screen.queryByRole("region", { name: "更新订阅账号凭据" })).not.toBeInTheDocument();
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
  });

  it("does not submit the provider form when Enter is pressed in authorization inputs", async () => {
    const { user, requests } = setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), "account_first");
    await user.click(screen.getByRole("button", { name: "重新授权账号" }));
    await user.type(await screen.findByLabelText("Synthetic setup token"), "synthetic-token{Enter}");
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
    expect(screen.getByRole("region", { name: "更新订阅账号凭据" })).toBeVisible();
  });
});
