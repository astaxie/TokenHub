import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { BillingRateCards } from "./billing-rate-cards";

describe("BillingRateCards", () => {
  afterEach(() => {
    setActiveLanguage("en");
    vi.unstubAllGlobals();
  });

  it("localizes an empty billing error response fallback", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 500 })));
    setActiveLanguage("en");
    render(<BillingRateCards api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={emptyData()} />);

    await userEvent.click(screen.getByRole("button", { name: "Load published versions" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Billing operation failed (500)");
  });

  it("renders stacked field groups and labeled actions", async () => {
    const { container } = render(<BillingRateCards api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={emptyData()} />);
    expect(container.querySelectorAll(".form-grid").length).toBeGreaterThan(1);
    expect(container.querySelector(".rate-card-form")).not.toBeNull();
    expect(screen.getByRole("button", { name: "预览费用" })).toHaveClass("button");
    expect(screen.getByRole("button", { name: "发布影子价目" })).toHaveClass("secondary-button");
    expect(screen.getByRole("button", { name: "添加时段" })).toHaveClass("secondary-button");
    await userEvent.click(screen.getByRole("button", { name: "添加时段" }));
    expect(screen.getByRole("checkbox", { name: "周一" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "周日" })).not.toBeChecked();
    expect(container.querySelectorAll(".rate-card-weekdays input:checked + span")).toHaveLength(5);
  });

  it("formats exact original and USD preview amounts in Japanese", async () => {
    setActiveLanguage("ja");
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({
      snapshot: {}, charge: { amount: "123456789012345678.123456789", currency: "CNY", usd: "1234567.89" },
    })));
    const { container } = render(<BillingRateCards api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={emptyData()} />);
    fireEvent.submit(container.querySelector("form")!);
    expect(await screen.findByText("元 123,456,789,012,345,678.123456789")).toBeInTheDocument();
    expect(screen.getByText("$1,234,567.89")).toBeInTheDocument();
  });
});
