import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor, PluginDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

const provider: Provider = { id: "prv_codex_reauth", name: "Synthetic Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 };
const catalog: ProviderCatalogEntry = { id: "openai-codex", name: provider.name, display_name: provider.name, type: provider.type, source: "fixture", models_count: 0, models: [] };
const plugin: PluginDescriptor = { id: "tokenhub.provider.openai-codex", name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "openai_subscription", subject: provider.type }] };
const actions: PluginActionDescriptor[] = ["oauth.start", "oauth.exchange"].map(capability => ({ plugin_id: plugin.id, action_id: `${provider.type}.${capability}`, capability, subject: provider.type, kind: "mutate", metadata: { oauth_redirect_uri: "http://localhost:1455/auth/callback" } }));
const resources: ProviderResource[] = ["first", "second"].map(name => ({ id: `codex_${name}`, provider_id: provider.id, name, resource_type: "openai_subscription", status: "active", healthy: true, priority: 7, weight: 25, credential_summary: { auth_type: "oauth", oauth_reauthorization_required: "true" } }));

function setup(failExchange = false) {
  const requests: Array<{ path: string; method: string; body?: Record<string, unknown> }> = [];
  vi.spyOn(window, "open").mockReturnValue(null);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input)).pathname;
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    requests.push({ path, method, body });
    if (path.includes("provider-catalog")) return new Response(JSON.stringify({ data: catalog }));
    if (path.endsWith("oauth.start")) return new Response(JSON.stringify({ data: { session_id: "codex-session", state: "codex-state", auth_url: "https://auth.example.invalid/authorize", redirect_uri: "http://localhost:1455/auth/callback" } }));
    if (path.endsWith("oauth.exchange")) {
      if (failExchange) { failExchange = false; return new Response(JSON.stringify({ error: { message: "Synthetic rejected code" } }), { status: 400 }); }
      return new Response(JSON.stringify({ data: { access_token: "synthetic-access", refresh_token: "synthetic-refresh", account_id: "replacement-account", account_email: "replacement@example.invalid" } }));
    }
    if (method === "PATCH" && path.endsWith("codex_first")) return new Response(JSON.stringify({ data: resources[0] }));
    throw new Error(`Undeclared request: ${method} ${path}`);
  }));
  const onSaved = vi.fn().mockResolvedValue(undefined);
  render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }} catalog={[catalog]} provider={provider} plugins={[plugin]} pluginActions={actions} resources={resources} loading={false} mode="edit" onClose={vi.fn()} onSaved={onSaved} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()} />);
  return { requests, onSaved, user: userEvent.setup() };
}

async function startLogin(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "重新登录：first" }));
  const panel = within(screen.getByRole("region", { name: "更新订阅账号凭据" }));
  await user.click(panel.getByRole("button", { name: "打开授权" }));
  await user.type(await panel.findByRole("textbox", { name: /^回调结果/ }), "http://localhost:1455/auth/callback?code=synthetic-code&state=codex-state");
  return panel;
}

describe("Saved Codex account reauthorization", () => {
  it("replaces the selected saved account through its existing OAuth actions", async () => {
    const { user, requests, onSaved } = setup();
    const panel = await startLogin(user);
    await user.click(panel.getByRole("button", { name: "完成授权" }));
    await waitFor(() => expect(panel.getByRole("button", { name: "保存新账号凭据" })).toBeEnabled());
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
    await user.click(panel.getByRole("button", { name: "保存新账号凭据" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    const patch = requests.find(request => request.method === "PATCH")!;
    expect(patch.path).toBe("/api/admin/provider-resources/codex_first");
    expect(Object.keys(patch.body!)).toEqual(["credentials"]);
    expect(patch.body?.credentials).toMatchObject({ replace: true, access_token: "synthetic-access", refresh_token: "synthetic-refresh", account_id: "replacement-account", email: "replacement@example.invalid" });
    expect(requests.find(request => request.path.endsWith("oauth.start"))?.body?.redirect_uri).toBe("http://localhost:1455/auth/callback");
    expect(requests.find(request => request.path.endsWith("oauth.exchange"))?.body).toMatchObject({ session_id: "codex-session", state: "codex-state", code: "synthetic-code" });
  });

  it("allows a fresh authorization after a consumed legacy callback is rejected", async () => {
    const { user, requests } = setup(true);
    const panel = await startLogin(user);
    await user.click(panel.getByRole("button", { name: "完成授权" }));
    expect(await panel.findByRole("alert")).toHaveTextContent("Synthetic rejected code");
    expect(panel.queryByRole("textbox", { name: /^回调结果/ })).not.toBeInTheDocument();
    await user.click(panel.getByRole("button", { name: "打开授权" }));
    await waitFor(() => expect(requests.filter(request => request.path.endsWith("oauth.start"))).toHaveLength(2));
    expect(requests.filter(request => request.method === "PATCH")).toHaveLength(0);
  });
});
