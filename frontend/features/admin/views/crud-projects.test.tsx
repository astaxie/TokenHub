import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { providerQuotaReadAction } from "./crud-projects";

describe("providerQuotaReadAction", () => {
  it("matches quota plugin actions by the resource Provider type", () => {
    const data = emptyData();
    data.providers = [{ id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 }];
    data.pluginActions = [
      { plugin_id: "tokenhub.provider.other", action_id: "quota.read", kind: "read", capability: "quota.read", subject: "other_provider" },
      { plugin_id: "tokenhub.provider.openai-codex", action_id: "openai_codex.quota.read", kind: "read", capability: "quota.read", subject: "openai_codex" },
    ];

    expect(providerQuotaReadAction(data, {
      id: "rsrc_codex",
      provider_id: "prv_codex",
      name: "Codex Account",
      resource_type: "openai_subscription",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    })?.plugin_id).toBe("tokenhub.provider.openai-codex");
  });
});
