import { afterEach, describe, expect, it } from "vitest";
import { evolutionReasonText, rollbackCompatibilityReasonText } from "./db-evolution-reasons";
import { setActiveLanguage } from "./runtime";

afterEach(() => setActiveLanguage("en"));

describe("database evolution reason localization", () => {
  it("maps every backend readiness reason code without exposing diagnostics", () => {
    setActiveLanguage("en");
    const reasonCodes = [
      "handle_unavailable",
      "runner_error",
      "ledger_unreadable",
      "baseline_missing",
      "heartbeat_failing",
      "dirty_migration",
      "ledger_verification_failed",
      "expand_pending",
      "backfill_ledger_unreadable",
      "blocking_backfills_pending",
    ];

    for (const reasonCode of reasonCodes) {
      const rendered = evolutionReasonText({
        ready: false,
        reason: "raw backend diagnostic",
        reason_code: reasonCode,
        schema_version: 7,
        dirty_version: 6,
        blocking_backfills_pending: ["accounts-v2"],
      });
      expect(rendered).not.toContain("raw backend diagnostic");
      expect(rendered.length).toBeGreaterThan(0);
    }
  });

  it("uses a localized generic message for an unknown readiness code", () => {
    setActiveLanguage("ja");
    expect(evolutionReasonText({
      ready: false,
      reason: "raw backend diagnostic",
      reason_code: "future_reason",
      schema_version: 7,
    })).toBe("データベース進化状態を取得できません。サーバーログを確認してください");
  });

  it("formats rollback compatibility codes and parameters", () => {
    setActiveLanguage("en");
    expect(rollbackCompatibilityReasonText({
      compatibility_reason_code: "database_version_outside_range",
      compatibility_reason_params: { state: 4, release: "0.4.0", min: 0, max: 1 },
    })).toBe("Database state version 4 is outside version 0.4.0's compatibility range 0 – 1");
    expect(rollbackCompatibilityReasonText({
      compatibility_reason_code: "future_reason",
    })).toBe("The database compatibility check did not pass");
  });

  it("formats numeric reason parameters with the active non-English locale", () => {
    setActiveLanguage("ja");
    expect(evolutionReasonText({
      ready: false,
      reason_code: "dirty_migration",
      schema_version: 12345,
    })).toBe("バージョン 12,345 のマイグレーションが不完全な状態です。修復が必要です");
    expect(rollbackCompatibilityReasonText({
      compatibility_reason_code: "database_version_outside_range",
      compatibility_reason_params: { state: 12345, release: "0.4.0", min: 1000, max: 9999 },
    })).toBe("データベース状態バージョン 12,345 はバージョン 0.4.0 の互換範囲 1,000 – 9,999 外です");
  });
});
