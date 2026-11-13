import { describe, expect, it } from "vitest";
import type { AccountAllowance, ProviderAccountQuota } from "../core/types";
import { providerAccountQuotaIsLimited, providerAccountQuotaRemainingPercent, providerAccountQuotaStatusLabel, providerAccountQuotaStatusTone } from "./provider-account-ui";

describe("Normalized account quota presentation", () => {
  it.each<{ status: AccountAllowance["status"]; label: string; tone: string; remaining: number | undefined }>([
    { status: "unknown", label: "额度未知", tone: "unknown", remaining: undefined },
    { status: "query_error", label: "查询失败", tone: "limited", remaining: undefined },
    { status: "available", label: "可用", tone: "available", remaining: 65 },
    { status: "exhausted", label: "已达上限", tone: "limited", remaining: 0 },
  ])("uses $status instead of conflicting legacy fields", ({ status, label, tone, remaining }) => {
    const quota: ProviderAccountQuota = { status_label: "Quota reported", allowed: false, limit_reached: true, remaining_percent: 65, allowance: { version: 1, status } };
    expect(providerAccountQuotaStatusLabel(quota)).toBe(label);
    expect(providerAccountQuotaStatusTone(quota)).toBe(tone);
    expect(providerAccountQuotaIsLimited(quota)).toBe(status === "exhausted");
    expect(providerAccountQuotaRemainingPercent(quota)).toBe(remaining);
  });
});
