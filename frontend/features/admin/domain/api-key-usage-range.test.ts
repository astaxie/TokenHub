import { describe, expect, it } from "vitest";
import { apiKeyCustomUsageRange, apiKeyUsageRangeForDays, utcDateInputValue } from "./api-key-usage-range";
import { apiKeyUsageIDFromPath, viewFromPath } from "./formatting";

describe("API key usage ranges", () => {
  it("builds stable preset and inclusive custom UTC ranges", () => {
    const now = new Date("2026-08-21T12:00:00.000Z");
    expect(apiKeyUsageRangeForDays(7, now)).toEqual({
      from: "2026-08-15T00:00:00.000Z",
      to: "2026-08-21T12:00:00.000Z",
    });
    expect(apiKeyCustomUsageRange("2026-08-01", "2026-08-20", now)).toEqual({
      from: "2026-08-01T00:00:00.000Z",
      to: "2026-08-21T00:00:00.000Z",
    });
    expect(utcDateInputValue(now)).toBe("2026-08-21");
  });

  it("rejects reversed and overlong custom ranges", () => {
    const now = new Date("2026-08-21T12:00:00.000Z");
    expect(apiKeyCustomUsageRange("2026-08-20", "2026-08-01", now)).toBeNull();
    expect(apiKeyCustomUsageRange("2025-01-01", "2026-08-01", now)).toBeNull();
  });
});

describe("API key usage routes", () => {
  it("keeps the Key Management view active for encoded detail routes", () => {
    expect(apiKeyUsageIDFromPath("/api-keys/key%2Fencoded/usage")).toBe("key/encoded");
    expect(viewFromPath("/api-keys/key%2Fencoded/usage")).toBe("api-keys");
    expect(apiKeyUsageIDFromPath("/api-keys/key/other")).toBe("");
  });
});
