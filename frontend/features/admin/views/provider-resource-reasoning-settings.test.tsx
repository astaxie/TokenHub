import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProviderResourceReasoningSettings } from "./provider-resource-reasoning-settings";

describe("ProviderResourceReasoningSettings", () => {
  it("hides plugin account resources using metadata instead of OpenAI subscription literals", () => {
    render(
      <ProviderResourceReasoningSettings
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        provider={{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }}
        providerType="kimi_subscription"
        providerAdapters={[{
          type: "kimi_subscription",
          capabilities: [],
          resource_types: [{ type: "kimi_subscription_account" }],
          provider_policy: { supports_custom_headers: true },
        }]}
        resources={[
          { id: "rsrc_kimi", provider_id: "prv_kimi", name: "Kimi Account", resource_type: "kimi_subscription_account", status: "active", healthy: true, priority: 1, weight: 100 },
          { id: "rsrc_key", provider_id: "prv_kimi", name: "Kimi API Key", resource_type: "api_key", status: "active", healthy: true, priority: 2, weight: 100 },
          { id: "rsrc_opaque", provider_id: "prv_kimi", name: "Kimi Opaque Token", resource_type: "kimi_ephemeral_token", status: "active", healthy: true, priority: 3, weight: 100 },
          { id: "rsrc_codex", provider_id: "prv_kimi", name: "Codex Account", resource_type: "openai_subscription", status: "active", healthy: true, priority: 4, weight: 100 },
          { id: "rsrc_other", provider_id: "prv_other", name: "Other Account", resource_type: "kimi_subscription_account", status: "active", healthy: true, priority: 5, weight: 100 },
        ]}
        onSaved={() => {}}
      />,
    );

    expect(screen.queryByText("Kimi Account")).not.toBeInTheDocument();
    expect(screen.getByText("Kimi API Key")).toBeInTheDocument();
    expect(screen.getByText("Kimi Opaque Token")).toBeInTheDocument();
    expect(screen.getByText("Codex Account")).toBeInTheDocument();
    expect(screen.queryByText("Other Account")).not.toBeInTheDocument();
  });
});
