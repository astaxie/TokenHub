import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { emptyRates } from "./billing-pricing-fields";
import { ModelRuleSimulator } from "./model-rule-simulator";

describe("ModelRuleSimulator", () => {
  afterEach(() => { setActiveLanguage("en"); vi.unstubAllGlobals(); });
  it("localizes an empty billing error response fallback", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 500 })));
    setActiveLanguage("en");
    render(<ModelRuleSimulator api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} embedding={false} card={{ kind: "tenant", target: "test", source: "model", currency: "USD", rates: emptyRates(), periods: [] }} />);
    await userEvent.click(screen.getByText("Advanced: validate pricing rules"));
    await userEvent.click(screen.getByRole("button", { name: "Calculate cost" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Billing operation failed (500)");
  });
});
