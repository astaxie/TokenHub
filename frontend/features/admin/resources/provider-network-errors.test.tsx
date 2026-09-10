import { describe, expect, it } from "vitest";
import { providerBlockedAddressMessage } from "./provider-network-errors";
import { readAdminError } from "./payloads";

describe("Blocked provider address guidance", () => {
  it.each(["provider_address_blocked", "provider_models_address_blocked"])("retains normalized IP details for %s", async (code) => {
    const details = { blocked_ips: ["198.18.0.81", "fd00::2", "198.18.0.81", "https://secret.example/key", null] };
    const expected = providerBlockedAddressMessage(code, details);
    expect(expected).toContain("被拒绝的 IP：198.18.0.81, fd00::2");
    expect(expected).toContain("系统设置 → 基础设置 → 编辑配置");
    expect(expected).not.toContain("secret");
    const result = await readAdminError(new Response(JSON.stringify({ error: { code, details, message: "secret transport diagnostic" } }), { status: 502 }), "fallback");
    expect(result).toBe(expected);
  });
  it("keeps guidance for older responses without IP details", () => {
    expect(providerBlockedAddressMessage("provider_address_blocked")).toContain("Synthetic DNS / Fake-IP 网段");
    expect(providerBlockedAddressMessage("provider_address_blocked", { blocked_ips: "unexpected" })).not.toContain("被拒绝的 IP");
    expect(providerBlockedAddressMessage("unrelated_error", { blocked_ips: ["198.18.0.81"] })).toBeUndefined();
  });
});
