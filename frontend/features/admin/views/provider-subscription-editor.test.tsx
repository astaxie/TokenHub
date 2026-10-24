import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor, PluginDescriptor, ProviderCatalogEntry } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

const catalog: ProviderCatalogEntry = { id: "review-subscription", name: "Review Subscription", display_name: "Review Subscription", type: "review_subscription", base_url: "", categories: ["openai"], models_count: 0, source: "fixture", models: [] };
const plugin: PluginDescriptor = { id: "test.review-subscription", name: "Review Subscription", version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "review_account", subject: catalog.type }] };
const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: plugin.id, action_id: capability, capability, kind: "mutate", subject: catalog.type, metadata: { subscription_auth: "true", subscription_session_json: "true", provider_resource_type: "review_account" } }));
const importedCatalog = { ...catalog, models_count: 1, models: [{ id: "imported-model", name: "Imported model", category: "openai", type: "chat" }] };

function setup(results: unknown[], existing = false, pluginActions = actions) {
  const requests: Array<{ url: string; body: unknown }> = [];
  let finishExisting!: (response: Response) => void;
  const pendingExisting = new Promise<Response>(resolve => { finishExisting = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input));
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    requests.push({ url: url.toString(), body });
    if (url.pathname.endsWith("oauth.methods")) return new Response(JSON.stringify({ data: { methods: [] } }));
    if (url.pathname.endsWith("oauth.exchange")) return new Response(JSON.stringify({ data: results.shift() }));
    if (url.pathname.includes("provider-catalog")) return url.searchParams.has("resource_id") ? pendingExisting : new Response(JSON.stringify({ data: body ? importedCatalog : catalog }));
    throw new Error(`Undeclared request: ${url}`);
  }));
  const view = render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }} catalog={[catalog]} plugins={[plugin]} pluginActions={pluginActions} resources={existing ? [{ id: "existing-account", provider_id: "existing-provider", resource_type: "review_account", name: "Existing Account", status: "active", healthy: true, priority: 1, weight: 100 }] : []} loading={false} mode="create" onClose={vi.fn()} onSaved={vi.fn()} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()} />);
  return { ...view, requests, finishExisting, user: userEvent.setup() };
}

async function importSession(user: ReturnType<typeof userEvent.setup>, container: HTMLElement, value: string) {
  const auth = within(container.querySelector("div.provider-account-auth-wide") as HTMLElement);
  const section = auth.getByText("导入订阅会话", { selector: "summary" }).closest("details");
  if (!section?.open) await user.click(auth.getByText("导入订阅会话", { selector: "summary" }));
  fireEvent.change(auth.getByLabelText(/^订阅会话 JSON/), { target: { value } });
  await user.click(auth.getByRole("button", { name: "导入订阅会话" }));
  await waitFor(() => expect(auth.getByLabelText(/^订阅会话 JSON/)).toHaveValue(""));
}

describe("Subscription editor catalog lifecycle", () => {
  it("retains a catalog returned with native access tokens without issuing a preview request", async () => {
    const { container, requests, user } = setup([{ access_token: "native-token", refresh_token: "native-refresh", client_id: "synthetic-client", client_secret: "synthetic-client-secret", catalog: importedCatalog }]);
    await user.click(screen.getByRole("button", { name: /Review Subscription/ }));
    await importSession(user, container, '{"access":"original-session"}');
    await user.click(screen.getByText("高级：手动粘贴 Token", { selector: "summary" }));
    expect(screen.getByLabelText("OAuth 客户端密钥")).toHaveValue("synthetic-client-secret");
    expect(screen.getByLabelText("OAuth 客户端密钥")).toHaveAttribute("type", "password");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    expect(await screen.findByRole("checkbox", { name: /Imported model/ })).toBeVisible();
    expect(requests.filter(request => request.url.includes("provider-catalog") && request.body)).toHaveLength(0);
  });

  it("invalidates authorized models when manual subscription credentials change", async () => {
    const { container, requests, user } = setup([{ access_token: "native-token", catalog: importedCatalog }]);
    await user.click(screen.getByRole("button", { name: /Review Subscription/ }));
    await importSession(user, container, '{"access":"original-session"}');
    await user.click(screen.getByText("高级：手动粘贴 Token", { selector: "summary" }));
    fireEvent.change(screen.getByLabelText(/^访问 Token/), { target: { value: "different-native-token" } });
    await user.click(screen.getByRole("button", { name: "下一步" }));
    expect(screen.queryByRole("checkbox", { name: /Imported model/ })).not.toBeInTheDocument();
    expect(screen.getByText("请先保存账号，再加载可用模型。")).toBeVisible();
    expect(requests.filter(request => request.url.includes("provider-catalog") && request.body)).toHaveLength(0);
  });

  it("retains native credentials after catalog discovery fails without repeating the preview", async () => {
    const { container, requests, user } = setup([{ access_token: "rotated-native-token", catalog_error: "subscription_models_unavailable" }]);
    await user.click(screen.getByRole("button", { name: /Review Subscription/ }));
    await importSession(user, container, '{"access":"original-session"}');
    await user.click(screen.getByText("高级：手动粘贴 Token", { selector: "summary" }));
    expect(screen.getByLabelText(/^访问 Token/)).toHaveValue("rotated-native-token");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    expect(screen.getByText("账号已授权，模型目录暂时不可用。请保存账号后重试。")).toBeVisible();
    expect(requests.filter(request => request.url.includes("provider-catalog") && request.body)).toHaveLength(0);
  });

  it("keeps credential preview for legacy account providers", async () => {
    const legacyActions = actions.map(action => ({ ...action, metadata: { provider_resource_type: "review_account" } }));
    const { requests, user } = setup([], false, legacyActions);
    await user.click(screen.getByRole("button", { name: /Review Subscription/ }));
    await user.click(screen.getByText("高级：手动粘贴 Token", { selector: "summary" }));
    fireEvent.change(screen.getByLabelText(/^访问 Token/), { target: { value: "legacy-token" } });
    await user.click(screen.getByRole("button", { name: "下一步" }));
    expect(await screen.findByRole("checkbox", { name: /Imported model/ })).toBeVisible();
    expect(requests.some(request => request.url.includes("provider-catalog") && (request.body as { access_token?: string })?.access_token === "legacy-token")).toBe(true);
  });

  it("replaces an in-flight existing-account catalog with imported account models", async () => {
    const { container, requests, finishExisting, user } = setup([{ session_json: '{"access":"new-session"}', catalog: importedCatalog }], true);
    await user.click(screen.getByRole("button", { name: /Review Subscription/ }));
    await waitFor(() => expect(requests.some(request => request.url.includes("resource_id=existing-account"))).toBe(true));
    await importSession(user, container, '{"access":"original-session"}');
    await user.click(screen.getByRole("button", { name: "下一步" }));
    expect(await screen.findByRole("checkbox", { name: /Imported model/ })).toBeVisible();
    await act(async () => { finishExisting(new Response(JSON.stringify({ data: { ...catalog, models: [{ id: "stale-model", name: "Stale model", category: "openai" }] } }))); });
    expect(screen.queryByRole("checkbox", { name: /Stale model/ })).not.toBeInTheDocument();
  });

  it("discards selected models from a previous account when the new catalog is unavailable", async () => {
    const { container, user } = setup([{ session_json: '{"access":"first-session"}', catalog: importedCatalog }, { session_json: '{"access":"second-session"}', catalog_unavailable: true }]);
    await user.click(screen.getByRole("button", { name: /Review Subscription/ }));
    await importSession(user, container, '{"access":"original-session"}');
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.click(await screen.findByRole("checkbox", { name: /Imported model/ }));
    await user.click(screen.getByRole("button", { name: "上一步" }));
    await importSession(user, container, '{"access":"replacement-session"}');
    await user.click(screen.getByRole("button", { name: "下一步" }));
    expect(screen.queryByRole("checkbox", { name: /Imported model/ })).not.toBeInTheDocument();
    expect(screen.getByText("账号已授权，模型目录暂时不可用。请保存账号后重试。")).toBeInTheDocument();
  });
});
