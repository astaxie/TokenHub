import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProviderCatalogEntry } from "../core/types";
import { ProviderOnboardingCatalog } from "./provider-onboarding-catalog";

const apiEntry: ProviderCatalogEntry = { id: "ui-api", name: "UI API", display_name: "UI API", type: "openai_compatible", base_url: "https://api.example.test/v1", models_count: 2, source: "test" };
const accountEntry: ProviderCatalogEntry = { id: "ui-account", name: "UI Account", display_name: "UI Account", type: "example_account", base_url: "https://account.example.test", models_count: 1, source: "plugin" };
const ollamaEntry: ProviderCatalogEntry = { id: "ollama", name: "Ollama", display_name: "Ollama", type: "local", base_url: "http://127.0.0.1:11434/v1", models_count: 1, source: "plugin" };

describe("ProviderOnboardingCatalog", () => {
  it("keeps configured cards actionable and selects metadata-driven account entries", async () => {
    const user = userEvent.setup();
    const select = vi.fn();
    const custom = vi.fn();
    render(<ProviderOnboardingCatalog directEntries={[apiEntry]} accountEntries={[accountEntry]} providers={[{ id: "existing", name: "Existing Provider", type: apiEntry.type, options: { catalog_id: apiEntry.id }, priority: 1, status: "active", healthy: true }]} query="" onQueryChange={vi.fn()} onSelect={select} onCustom={custom} contributions={[{ id: "card", plugin_id: "example", slot: "provider.catalog.card", provider_types: [accountEntry.type], title: "Example Subscription", schema: { description: "Sign in with your example account" } }]} />);
    expect(screen.getByText("已接入")).toBeVisible();
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
});
