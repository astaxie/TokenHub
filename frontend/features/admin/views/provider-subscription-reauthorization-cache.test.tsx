import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AdminUIContribution, PluginActionDescriptor, PluginDescriptor, Provider, ProviderCatalogEntry, ProviderResource } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

vi.mock("./provider-subscription-reauthorization", () => ({
  ProviderSubscriptionReauthorization: ({ onSaved }: { onSaved: () => Promise<void> }) =>
    <button type="button" onClick={() => { void onSaved().catch(() => {}); }}>Replace test account</button>,
}));

const provider: Provider = { id: "prv_cache", name: "Synthetic subscription", type: "review_subscription", status: "active", healthy: true, priority: 1 };
const resource: ProviderResource = { id: "account_cache", provider_id: provider.id, name: "Synthetic account", resource_type: "review_account", status: "active", healthy: true, priority: 1, weight: 100 };
const catalog: ProviderCatalogEntry = { id: "review-subscription", name: provider.name, display_name: provider.name, type: provider.type, source: "fixture", models_count: 1, models: [] };
const plugin: PluginDescriptor = { id: "test.cache", name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resource.resource_type, subject: provider.type }] };
const actions: PluginActionDescriptor[] = [{ plugin_id: plugin.id, action_id: "quota.read", capability: "quota.read", subject: provider.type, kind: "read", metadata: { provider_resource_type: resource.resource_type } }];
const contributions: AdminUIContribution[] = [{ plugin_id: plugin.id, id: "quota", slot: "provider.resource.panel", title: "Synthetic quota", provider_types: [provider.type], resource_types: [resource.resource_type], action: "quota.read" }];

function modelCatalog(name: string) {
  return { ...catalog, models: [{ id: name, name, type: "chat" }] };
}

function setup(deferOld = false) {
  let replaced = false;
  const requests: string[] = [];
  const pendingCatalogs: Array<(response: Response) => void> = [];
  const pendingQuotas: Array<(response: Response) => void> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input));
    requests.push(url.toString());
    if (url.pathname.includes("provider-catalog")) {
      if (deferOld && !replaced) return new Promise<Response>(resolve => pendingCatalogs.push(resolve));
      return new Response(JSON.stringify({ data: modelCatalog(replaced ? "New model" : "Old model") }));
    }
    if (url.pathname.endsWith("quota.read")) {
      if (deferOld && !replaced) return new Promise<Response>(resolve => pendingQuotas.push(resolve));
      const input = JSON.parse(String(init?.body ?? "{}"));
      const refreshed = replaced && input.refresh === true;
      return new Response(JSON.stringify({ data: { plan_type: refreshed ? "New plan" : "Old plan", windows: [{ used_percent: refreshed ? 10 : 90 }] } }));
    }
    throw new Error(`Undeclared request: ${url}`);
  }));
  const onAccountsChanged = vi.fn(async () => { replaced = true; throw new Error("Synthetic parent reload failure"); });
  render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }} catalog={[catalog]} provider={provider} plugins={[plugin]} pluginActions={actions} pluginUI={contributions} resources={[resource]} loading={false} mode="edit" onClose={vi.fn()} onSaved={vi.fn()} onAccountsChanged={onAccountsChanged} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()} />);
  return { user: userEvent.setup(), requests, pendingCatalogs, pendingQuotas, onAccountsChanged };
}

describe("Subscription credential replacement refreshes account observations", () => {
  it("reloads cached models and quota and clears pending choices even when the parent reload fails", async () => {
    const { user, onAccountsChanged } = setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), resource.id);
    await user.click(screen.getByRole("tab", { name: "模型" }));
    await user.click(await screen.findByRole("checkbox", { name: /Old model/ }));
    await user.click(screen.getByRole("tab", { name: "高级" }));
    expect(await screen.findByText("Old plan")).toBeVisible();
    await user.click(screen.getByRole("tab", { name: "连接" }));
    await user.click(screen.getByRole("button", { name: "Replace test account" }));
    await waitFor(() => expect(onAccountsChanged).toHaveBeenCalledOnce());
    await user.click(screen.getByRole("tab", { name: "模型" }));
    expect(await screen.findByRole("checkbox", { name: /New model/ })).not.toBeChecked();
    expect(screen.queryByRole("checkbox", { name: /Old model/ })).not.toBeInTheDocument();
    expect(screen.getByText("0 个待引入")).toBeVisible();
    await user.click(screen.getByRole("tab", { name: "高级" }));
    expect(await screen.findByText("New plan")).toBeVisible();
    expect(screen.queryByText("Old plan")).not.toBeInTheDocument();
  });

  it("ignores old in-flight model and quota reads after the replacement is saved", async () => {
    const { user, requests, pendingCatalogs, pendingQuotas } = setup(true);
    await user.selectOptions(screen.getByRole("combobox", { name: "账号列表" }), resource.id);
    await user.click(screen.getByRole("tab", { name: "高级" }));
    await waitFor(() => expect(pendingQuotas.length).toBeGreaterThan(0));
    await user.click(screen.getByRole("tab", { name: "连接" }));
    const oldCatalogCount = requests.filter(url => url.includes("provider-catalog")).length;
    await user.click(screen.getByRole("button", { name: "Replace test account" }));
    await waitFor(() => expect(requests.filter(url => url.includes("provider-catalog")).length).toBeGreaterThan(oldCatalogCount));
    await user.click(screen.getByRole("tab", { name: "高级" }));
    expect(await screen.findByText("New plan")).toBeVisible();
    await act(async () => {
      pendingCatalogs.forEach(resolve => resolve(new Response(JSON.stringify({ data: modelCatalog("Late old model") }))));
      pendingQuotas.forEach(resolve => resolve(new Response(JSON.stringify({ data: { plan_type: "Late old plan", windows: [{ used_percent: 99 }] } }))));
    });
    expect(screen.getByText("New plan")).toBeVisible();
    expect(screen.queryByText("Late old plan")).not.toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "模型" }));
    expect(await screen.findByRole("checkbox", { name: /New model/ })).toBeVisible();
    expect(screen.queryByRole("checkbox", { name: /Late old model/ })).not.toBeInTheDocument();
  });
});
