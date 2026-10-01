import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ProviderCatalogEntry } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

const catalog: ProviderCatalogEntry = {
  id: "discovery-service",
  name: "Discovery Service",
  display_name: "Discovery Service",
  type: "openai_compatible",
  base_url: "https://discovery.example.test/v1",
  categories: ["openai"],
  models_count: 1,
  source: "test",
  models: [{ id: "discovered-chat", name: "Discovered Chat", category: "openai" }],
};

describe("Provider onboarding discovery", () => {
  afterEach(() => vi.useRealTimers());

  it.each([
    ["manual reload", "重新加载"],
    ["connection test", "测试连接"],
  ])("keeps a model selection after %s completes before automatic discovery", async (_action, buttonName) => {
    vi.useFakeTimers();
    const discoveredConnections: unknown[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/provider-catalog/discovery-service")) return new Response(JSON.stringify({ data: catalog }));
      if (url.endsWith("/provider-catalog/custom") && init?.method === "POST") {
        discoveredConnections.push(JSON.parse(String(init.body)));
        return new Response(JSON.stringify({ data: { ...catalog, id: "custom" } }));
      }
      if (url.endsWith("/providers/test-connection")) return new Response(JSON.stringify({ healthy: true, latency_ms: 1 }));
      throw new Error(`Unexpected request: ${init?.method ?? "GET"} ${url}`);
    }));
    const view = render(<ProviderUpsertModal
      api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }}
      catalog={[catalog]}
      loading={false}
      mode="create"
      onClose={vi.fn()}
      onSaved={vi.fn()}
      setError={vi.fn()}
      setLoading={vi.fn()}
      setNotice={vi.fn()}
      providerTypeOptions={[{ value: catalog.type, label: "OpenAI Compatible", supportsCustomHeaders: true, apiKeyRequired: true, defaultCatalogProviderType: true }]}
    />);
    try {
      await act(async () => fireEvent.click(screen.getByRole("button", { name: /自定义供应商/ })));
      fireEvent.change(screen.getByLabelText("渠道名称", { exact: true }), { target: { value: "Custom Discovery" } });
      fireEvent.change(screen.getByLabelText("Base URL", { exact: true }), { target: { value: "https://custom.example.test/v1" } });
      fireEvent.change(screen.getByLabelText("API Key", { exact: true }), { target: { value: "synthetic-key" } });
      expect(discoveredConnections).toHaveLength(0);

      await act(async () => fireEvent.click(screen.getByRole("button", { name: buttonName })));
      expect(discoveredConnections).toHaveLength(1);
      expect(discoveredConnections[0]).toMatchObject({ base_url: "https://custom.example.test/v1", api_key: "synthetic-key" });
      fireEvent.click(screen.getByRole("switch", { name: "引入 Discovered Chat" }));
      expect(screen.getByRole("switch", { name: "移除 Discovered Chat" })).toHaveAttribute("aria-checked", "true");

      await act(async () => vi.advanceTimersByTimeAsync(350));

      expect(discoveredConnections).toHaveLength(1);
      expect(screen.getByRole("switch", { name: "移除 Discovered Chat" })).toHaveAttribute("aria-checked", "true");
    } finally {
      view.unmount();
    }
  });
});
