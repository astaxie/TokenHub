import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProviderCatalogEntry } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

const directEntry: ProviderCatalogEntry = { id: "example-api", name: "Example API", display_name: "Example API", type: "openai_compatible", base_url: "https://api.example.test/v1", models_count: 0, source: "test" };

describe("Provider API type selection", () => {
  it.each(["custom", "branded"])("excludes account adapters without catalog entries from %s connections", async (connection) => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ data: directEntry }))));
    render(<ProviderUpsertModal
      api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }}
      catalog={connection === "custom" ? [] : [directEntry]}
      loading={false}
      mode="create"
      onClose={vi.fn()}
      onSaved={vi.fn().mockResolvedValue(undefined)}
      setError={vi.fn()}
      setLoading={vi.fn()}
      setNotice={vi.fn()}
      providerAdapters={[{ type: "account_only", capabilities: ["chat"], provider_policy: { credentials_scope: "resource", supports_custom_headers: false } }]}
      providerTypeOptions={[
        { value: "account_only", label: "Account only", supportsCustomHeaders: false },
        { value: "openai_compatible", label: "Compatible API", supportsCustomHeaders: true },
      ]}
    />);

    if (connection === "custom") {
      await user.click(screen.getByRole("button", { name: /自定义供应商/ }));
    } else {
      await user.click(screen.getByRole("button", { name: /Example API/ }));
      await user.click(screen.getByText("高级连接设置"));
    }

    expect(screen.getByRole("combobox", { name: "渠道商类型" })).toHaveValue("openai_compatible");
    expect(screen.queryByRole("option", { name: "Account only" })).not.toBeInTheDocument();
  });
});
