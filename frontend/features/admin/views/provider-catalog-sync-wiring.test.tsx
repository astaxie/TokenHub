import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApiContext } from "../core/types";
import { adminFetch } from "../resources/payloads";
import { fetchProviderCatalog } from "../resources/provider-catalog";
import { ProviderCatalogSyncButton } from "./provider-onboarding-catalog";

vi.mock("../resources/payloads", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./payloads")>();
  return { ...actual, adminFetch: vi.fn() };
});

const api = { baseURL: "http://tokenhub.test", adminToken: "ui-fixture-session" } as ApiContext;

// This exercises the production wiring rather than a mocked callback: the button calls
// the real fetchProviderCatalog, which is the function that actually distinguishes a
// failed catalog read. The defect being fixed was that the page loader resolved after a
// failed read, so the button reported success while the imported provider was absent.
describe("provider catalog sync through the production reload", () => {
  afterEach(() => {
    vi.mocked(adminFetch).mockReset();
  });

  it("reports failure when the sync succeeds but the catalog reload fails", async () => {
    const user = userEvent.setup();
    let reloaded: unknown[] | undefined;
    vi.mocked(adminFetch)
      .mockResolvedValueOnce(new Response(JSON.stringify({
        source: "upstream-provider-catalog", providers: 258, models_count: 9000, duration_ms: 1800,
      }), { status: 200 }))
      .mockResolvedValueOnce(new Response("upstream unavailable", { status: 503 }));

    render(<ProviderCatalogSyncButton
      api={api}
      onSynced={async () => {
        reloaded = await fetchProviderCatalog(api);
      }}
    />);
    await user.click(screen.getByRole("button", { name: /同步供应商目录/ }));

    // The sync POST succeeded, but the catalog never reached the owning state, so a
    // success message here would describe a provider that is not on screen.
    expect(await screen.findByText("目录同步失败，请稍后重试。")).toBeVisible();
    expect(screen.queryByText(/同步供应商目录完成/)).toBeNull();
    expect(reloaded).toBeUndefined();
  });

  it("reports success only after the reloaded catalog arrives", async () => {
    const user = userEvent.setup();
    let reloaded: unknown[] | undefined;
    vi.mocked(adminFetch)
      .mockResolvedValueOnce(new Response(JSON.stringify({
        source: "upstream-provider-catalog", providers: 258, models_count: 9000, duration_ms: 1800,
      }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        data: [{ id: "zai", name: "Z.AI", base_url: "https://api.z.ai/v1" }],
      }), { status: 200 }));

    render(<ProviderCatalogSyncButton
      api={api}
      onSynced={async () => {
        reloaded = await fetchProviderCatalog(api);
      }}
    />);
    await user.click(screen.getByRole("button", { name: /同步供应商目录/ }));

    expect(await screen.findByText("同步供应商目录完成：已加载 258 个供应商。")).toBeVisible();
    expect(reloaded).toHaveLength(1);
  });
});
