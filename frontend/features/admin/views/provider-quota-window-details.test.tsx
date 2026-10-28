import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProviderQuotaWindowDetails } from "./provider-quota-window-details";

describe("Subscription quota window details", () => {
  it("shows every global and model-scoped window without hiding excluded models", () => {
    render(<ProviderQuotaWindowDetails quota={{ windows: [{ label: "5 hours", used_percent: 25 }, { label: "7 days", used_percent: 50 }, { label: "30 days", used_percent: 75 }], additional_rate_limits: [{ limit_name: "Core models", metered_feature: "core-model", rate_limit: { allowed: false, limit_reached: true, primary_window: { used_percent: 100, reset_at: 1792936800 } } }, { limit_name: "Standard models", metered_feature: "Excludes: core-model", rate_limit: { allowed: true, limit_reached: false, primary_window: { used_percent: 10 } } }] }} />);
    for (const label of ["5 hours", "7 days", "30 days", "Core models", "Standard models", "core-model", "不适用模型：core-model"]) expect(screen.getByText(label, { exact: true })).toBeInTheDocument();
    expect(screen.getByText("100%")).toBeInTheDocument();
    expect(screen.getByText(/^重置时间：/)).toBeInTheDocument();
  });
  it("ignores invalid reset timestamps from quota plugins", () => {
    render(<ProviderQuotaWindowDetails quota={{ windows: [{ label: "Synthetic allowance", used_percent: 25, reset_at: 1e100 }] }} />);
    expect(screen.getByText("25%")).toBeInTheDocument();
    expect(screen.queryByText(/^重置时间：/)).not.toBeInTheDocument();
  });
});
