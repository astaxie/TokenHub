import { afterEach, describe, expect, it, vi } from "vitest";
import { adminFetch } from "./payloads";
import type { ApiContext } from "../core/types";
import { fetchProviderCatalog } from "./provider-catalog";

vi.mock("./payloads", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../resources/payloads")>();
  return { ...actual, adminFetch: vi.fn() };
});

const api = { baseURL: "http://tokenhub.test", adminToken: "ui-fixture-session" } as ApiContext;

// These cover the production reload callback the sync button awaits. The button tests
// only exercise a mocked callback, which cannot catch a callback that resolves after a
// failed read: that is exactly how the modal reported success on a stale catalog.
describe("fetchProviderCatalog", () => {
  afterEach(() => {
    vi.mocked(adminFetch).mockReset();
  });

  it("returns the reloaded entries so the sync can be reported as succeeded", async () => {
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response(JSON.stringify({
      data: [{ id: "zai", name: "Z.AI", base_url: "https://api.z.ai/v1" }],
    }), { status: 200 }));

    const entries = await fetchProviderCatalog(api);

    expect(entries).toHaveLength(1);
    expect(entries[0].id).toBe("zai");
  });

  it("rejects when the catalog read fails after a successful sync", async () => {
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response("upstream unavailable", { status: 503 }));

    // Rejecting is the contract the button depends on: it awaits this call inside its
    // try block, so a rejection turns the whole sync into the failure message instead
    // of a success message over old cards.
    await expect(fetchProviderCatalog(api)).rejects.toThrow();
  });

  it("treats a missing data field as an empty catalog", async () => {
    vi.mocked(adminFetch).mockResolvedValueOnce(new Response(JSON.stringify({}), { status: 200 }));

    await expect(fetchProviderCatalog(api)).resolves.toEqual([]);
  });
});
