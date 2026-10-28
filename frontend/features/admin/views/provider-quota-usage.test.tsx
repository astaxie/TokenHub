import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProviderQuotaUsage } from "./provider-quota-usage";

describe("Subscription quota usage", () => {
  it("shows unknown usage without a progress bar for balance-only responses", () => {
    render(<ProviderQuotaUsage quota={{ metrics: [{ label: "Balance", value: "1250" }] }} />);
    expect(screen.getByText("-")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.queryByText("0%")).not.toBeInTheDocument();
  });

  it("preserves an explicitly reported zero usage", () => {
    render(<ProviderQuotaUsage quota={{ windows: [{ used_percent: 0 }] }} />);
    expect(screen.getByText("0%")).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
  });
});
