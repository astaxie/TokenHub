import { useState } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProviderCatalogEntry } from "../core/types";
import { adminFetch } from "../resources/payloads";
import { ProviderCatalogSyncButton, ProviderOnboardingCatalog } from "./provider-onboarding-catalog";

vi.mock("../resources/payloads", async importOriginal => ({
  ...(await importOriginal<typeof import("../resources/payloads")>()),
  adminFetch: vi.fn(),
}));

const apiEntry: ProviderCatalogEntry = { id: "ui-api", name: "UI API", display_name: "UI API", type: "openai_compatible", base_url: "https://api.example.test/v1", models_count: 2, source: "test" };
const accountEntry: ProviderCatalogEntry = { id: "ui-account", name: "UI Account", display_name: "UI Account", type: "example_account", base_url: "https://account.example.test", models_count: 1, source: "plugin" };
const ollamaEntry: ProviderCatalogEntry = { id: "ollama", name: "Ollama", display_name: "Ollama", type: "local", base_url: "http://127.0.0.1:11434/v1", models_count: 1, source: "plugin" };
const aggregatorEntry: ProviderCatalogEntry = { ...apiEntry, id: "openrouter", name: "OpenRouter", display_name: "OpenRouter", base_url: "https://router.example.test/v1" };
const api = { baseURL: "http://localhost:8080", adminToken: "admin-token" };

describe("ProviderOnboardingCatalog", () => {
  it("keeps configured cards actionable and selects metadata-driven account entries", async () => {
    const user = userEvent.setup();
    const select = vi.fn();
    const custom = vi.fn();
    render(<ProviderOnboardingCatalog directEntries={[apiEntry]} accountEntries={[accountEntry]} providers={[{ id: "existing", name: "Existing Provider", type: apiEntry.type, options: { catalog_id: apiEntry.id }, priority: 1, status: "active", healthy: true }]} query="" onQueryChange={vi.fn()} onSelect={select} onCustom={custom} contributions={[{ id: "card", plugin_id: "example", slot: "provider.catalog.card", provider_types: [accountEntry.type], title: "Example Subscription", schema: { description: "Sign in with your example account" } }]} />);
    expect(within(screen.getByRole("button", { name: /UI API/ })).getByText("已接入")).toBeVisible();
    await user.click(screen.getByRole("button", { name: /UI API/ }));
    expect(select).toHaveBeenLastCalledWith(apiEntry, "provider_api_key");
    await user.click(screen.getByRole("button", { name: /Example Subscription/ }));
    expect(select).toHaveBeenLastCalledWith(accountEntry, "account_integration");
    expect(screen.getByText("Sign in with your example account")).toBeVisible();
    await user.click(screen.getByRole("button", { name: /自定义供应商/ }));
    expect(custom).toHaveBeenCalledOnce();
  });

  it("searches provider URLs and leaves the custom entry available when no provider matches", async () => {
    const user = userEvent.setup();
    function Harness() {
      const [query, setQuery] = useState("");
      return <ProviderOnboardingCatalog directEntries={[apiEntry]} accountEntries={[accountEntry]} providers={[]} query={query} onQueryChange={setQuery} onSelect={vi.fn()} onCustom={vi.fn()} contributions={[{ id: "account-card", plugin_id: "example", slot: "provider.catalog.card", provider_types: [accountEntry.type], schema: { description: "Connect your subscription" } }]} />;
    }
    render(<Harness />);
    const search = screen.getByPlaceholderText("搜索供应商名称或地址");
    await user.type(search, "account.example.test");
    expect(screen.getByRole("button", { name: /UI Account/ })).toBeVisible();
    expect(screen.queryByRole("button", { name: /UI API/ })).not.toBeInTheDocument();
    await user.clear(search);
    await user.type(search, "missing-service");
    expect(screen.getByText("没有匹配的供应商，可使用自定义接入。")).toBeVisible();
    expect(screen.getByRole("button", { name: /自定义供应商/ })).toBeEnabled();
  });

  it("renders the matched brand icon and a shared fallback for unknown providers", () => {
    render(<ProviderOnboardingCatalog directEntries={[ollamaEntry]} accountEntries={[accountEntry]} providers={[]} query="" onQueryChange={vi.fn()} onSelect={vi.fn()} onCustom={vi.fn()} contributions={[]} />);
    const ollamaCard = screen.getByRole("button", { name: /Ollama/ });
    expect(ollamaCard.querySelector("img")).toHaveAttribute("src", "/provider-icons/ollama.svg");
    fireEvent.error(ollamaCard.querySelector("img")!);
    expect(ollamaCard.querySelector("svg")).toBeInTheDocument();
    expect(ollamaCard.querySelector(".provider-onboarding-card-icon")).toHaveClass("fallback");
    expect(screen.getByRole("button", { name: /UI Account/ }).querySelector("svg")).toBeInTheDocument();
  });

  it("combines category and search filters while keeping custom connections available", async () => {
    const user = userEvent.setup();
    const select = vi.fn();
    const custom = vi.fn();
    function Harness() {
      const [query, setQuery] = useState("");
      return <ProviderOnboardingCatalog directEntries={[apiEntry, ollamaEntry, aggregatorEntry]} accountEntries={[accountEntry]} providers={[{ id: "existing", name: "Existing API", type: apiEntry.type, base_url: apiEntry.base_url, priority: 1, status: "disabled", healthy: false }]} query={query} onQueryChange={setQuery} onSelect={select} onCustom={custom} contributions={[]} />;
    }
    render(<Harness />);
    const filters = within(screen.getByRole("group", { name: "供应商分类" }));
    const accounts = filters.getByRole("button", { name: "订阅与账号 1" });
    expect(filters.getByRole("button", { name: "全部 4" })).toHaveAttribute("aria-pressed", "true");
    await user.click(accounts);
    expect(accounts).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: /UI API/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /UI Account/ }));
    expect(select).toHaveBeenLastCalledWith(accountEntry, "account_integration");
    const search = screen.getByPlaceholderText("搜索供应商名称或地址");
    await user.type(search, "api.example.test");
    expect(filters.getByRole("button", { name: "订阅与账号 0" })).toBeEnabled();
    expect(screen.getByText("没有匹配的供应商，可使用自定义接入。")).toBeVisible();
    await user.click(filters.getByRole("button", { name: "供应商 1" }));
    expect(search).toHaveValue("api.example.test");
    expect(screen.getByRole("button", { name: /UI API/ })).toBeVisible();
    await user.clear(search);
    expect(screen.queryByRole("button", { name: /Ollama/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /UI API/ }));
    expect(select).toHaveBeenLastCalledWith(apiEntry, "provider_api_key");
    await user.type(search, "missing-service");
    await user.click(screen.getByRole("button", { name: /自定义供应商/ }));
    expect(custom).toHaveBeenCalledOnce();
    await user.clear(search);
    await user.click(filters.getByRole("button", { name: "中转聚合 1" }));
    expect(screen.queryByRole("button", { name: /UI API/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /OpenRouter/ }));
    expect(select).toHaveBeenLastCalledWith(aggregatorEntry, "provider_api_key");
    await user.click(filters.getByRole("button", { name: "本地部署 1" }));
    expect(screen.queryByRole("button", { name: /OpenRouter/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Ollama/ }));
    expect(select).toHaveBeenLastCalledWith(ollamaEntry, "provider_api_key");
    const all = filters.getByRole("button", { name: "全部 4" });
    all.focus();
    await user.keyboard("{Enter}");
    expect(all).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: /Ollama/ })).toBeVisible();
  });

  it("shows only available connection modes and handles an empty catalog", () => {
    const props = { api, directEntries: [apiEntry], accountEntries: [], providers: [], query: "", onQueryChange: vi.fn(), onSelect: vi.fn(), onCustom: vi.fn(), contributions: [] };
    const { rerender } = render(<ProviderOnboardingCatalog {...props} />);
    const filters = within(screen.getByRole("group", { name: "供应商分类" }));
    expect(filters.queryByRole("button", { name: /订阅与账号/ })).not.toBeInTheDocument();
    fireEvent.click(filters.getByRole("button", { name: "供应商 1" }));
    rerender(<ProviderOnboardingCatalog {...props} directEntries={[]} />);
    expect(filters.getByRole("button", { name: "全部 0" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("没有匹配的供应商，可使用自定义接入。")).toBeVisible();
    expect(screen.getByRole("button", { name: /自定义供应商/ })).toBeEnabled();
  });

});

describe("ProviderCatalogSyncButton", () => {
  async function clickSync(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: /同步供应商目录/ }));
  }

  it("posts to the explicit sync endpoint and reports the provider count", async () => {
    const user = userEvent.setup();
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response(JSON.stringify({ source: "upstream-provider-catalog", providers: 258, models_count: 9000, duration_ms: 1800 }), { status: 200 }));
    render(<ProviderCatalogSyncButton api={api} />);
    await clickSync(user);
    expect(await screen.findByText("同步供应商目录完成：已加载 258 个供应商。")).toBeVisible();
    expect(adminFetch).toHaveBeenCalledWith(api, "/api/admin/provider-catalog-sync", { method: "POST" });
  });

  it("shows a retryable failure message when the sync endpoint fails", async () => {
    const user = userEvent.setup();
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response("upstream unavailable", { status: 502 }));
    render(<ProviderCatalogSyncButton api={api} />);
    await clickSync(user);
    expect(await screen.findByText("目录同步失败，请稍后重试。")).toBeVisible();
  });

  it("disables the button and reports progress while the sync is running", async () => {
    const user = userEvent.setup();
    let release: (() => void) | undefined;
    vi.mocked(adminFetch).mockImplementationOnce(() => new Promise<Response>(resolve => {
      release = () => resolve(new Response(JSON.stringify({ source: "upstream-provider-catalog", providers: 2 }), { status: 200 }));
    }));
    render(<ProviderCatalogSyncButton api={api} />);
    await clickSync(user);
    const busy = await screen.findByRole("button", { name: /正在同步/ });
    expect(busy).toBeDisabled();
    release?.();
    expect(await screen.findByText("同步供应商目录完成：已加载 2 个供应商。")).toBeVisible();
    await waitFor(() => expect(screen.getByRole("button", { name: /同步供应商目录/ })).toBeEnabled());
  });

  it("calls the reload callback after a successful sync so imported providers appear", async () => {
    const user = userEvent.setup();
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response(JSON.stringify({ source: "upstream-provider-catalog", providers: 2, models_count: 9000, duration_ms: 1800 }), { status: 200 }));
    const onSynced = vi.fn();
    render(<ProviderCatalogSyncButton api={api} onSynced={onSynced} />);
    await clickSync(user);
    await waitFor(() => expect(onSynced).toHaveBeenCalledOnce());
    expect(await screen.findByText("同步供应商目录完成：已加载 2 个供应商。")).toBeVisible();
  });

  it("reports the whole sync as failed when the reload callback rejects", async () => {
    const user = userEvent.setup();
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response(JSON.stringify({ source: "upstream-provider-catalog", providers: 2, models_count: 9000, duration_ms: 1800 }), { status: 200 }));
    const onSynced = vi.fn().mockRejectedValue(new Error("catalog reload failed"));
    render(<ProviderCatalogSyncButton api={api} onSynced={onSynced} />);
    await clickSync(user);
    // Announcing success while the cards still show the old catalog is the defect
    // being fixed, so an unreloadable catalog is surfaced as a failed sync. Retrying
    // repeats both steps and is safe: the upstream fetch is idempotent.
    expect(await screen.findByText("目录同步失败，请稍后重试。")).toBeVisible();
    expect(onSynced).toHaveBeenCalledOnce();
  });

  it("marks the failure message so it does not read as success", async () => {
    const user = userEvent.setup();
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response("upstream unavailable", { status: 502 }));
    render(<ProviderCatalogSyncButton api={api} />);
    await clickSync(user);
    const status = await screen.findByRole("status");
    expect(status).toHaveClass("failed");
  });
});
