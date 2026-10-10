import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProviderModelInventory } from "./provider-model-inventory";

afterEach(() => { vi.unstubAllGlobals(); });

describe("Provider inventory cost confirmation", () => {
  it.each([undefined, "reference", "unverified"])("persists an explicit zero-price confirmation from %s", async pricingStatus => {
    const fetchMock = vi.fn<typeof fetch>(async () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();
    render(<ProviderModelInventory
      api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }}
      models={[{
        id: "confirmed-free", provider_id: "synthetic-provider", upstream_model: "synthetic-chat", modality: "chat", status: "active",
        metadata: { custom_note: "preserve", ...(pricingStatus ? { pricing_status: pricingStatus } : {}) },
      }]}
      onSaved={onSaved}
    />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "保存成本" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    const init = fetchMock.mock.calls[0]?.[1];
    expect(JSON.parse(String(init?.body))).toMatchObject({
      metadata: { custom_note: "preserve", pricing_status: "configured" },
      input_price_usd_per_1m: 0,
      output_price_usd_per_1m: 0,
      cache_read_price_usd_per_1m: 0,
      cache_write_price_configured: false,
      cache_write_5m_price_configured: false,
      cache_write_1h_price_configured: false,
    });
  });
});
