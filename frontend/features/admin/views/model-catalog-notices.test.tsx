import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { ModelCatalogNotices } from "./model-catalog-notices";

afterEach(() => { cleanup(); vi.useRealTimers(); setActiveLanguage("zh-CN"); });

describe("ModelCatalogNotices", () => {
  it("shows retirement only at the precise shutdown time", () => {
    setActiveLanguage("en");
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-21T02:00:00Z"));
    render(<ModelCatalogNotices metadata={{ lifecycle_status: "deprecated", shutdown_at: "2026-10-21T10:00:00+08:00", replacement_model: "next-model" }} />);
    expect(screen.getByText("The official endpoint has retired; choose a replacement model.")).toBeVisible();
    expect(screen.getByText("Replacement: next-model")).toBeVisible();
  });

  it("keeps redirected aliases distinct from retired endpoints", () => {
    setActiveLanguage("en");
    render(<ModelCatalogNotices metadata={{ lifecycle_status: "redirected", replacement_model: "actual-model", pricing_status: "unverified" }} />);
    expect(screen.getByText("This is a redirected alias; the underlying model may have changed.")).toBeVisible();
    expect(screen.getByText("Costs need configuration; catalog zero values do not mean free usage.")).toBeVisible();
    expect(screen.queryByText(/endpoint has retired/)).not.toBeInTheDocument();
  });

  it.each(["zh-CN", "en", "ja"] as const)("labels preview and catalog-only models in %s", language => {
    setActiveLanguage(language);
    const { container } = render(<ModelCatalogNotices metadata={{ availability: "preview", call_support: "unsupported" }} />);
    expect(container.querySelectorAll("small")).toHaveLength(2);
    if (language !== "zh-CN") expect(container.textContent).not.toMatch(/预览|仅目录/);
  });

  it("renders no notices for an unannotated model", () => {
    const { container } = render(<ModelCatalogNotices metadata={{ source: "custom" }} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows a calendar-only date without inventing a vendor time", () => {
    setActiveLanguage("en");
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-19T11:59:59Z"));
    render(<ModelCatalogNotices metadata={{ lifecycle_status: "deprecated", shutdown_date: "2026-11-18" }} />);
    expect(screen.getByText(/Shutdown date: Nov 18, 2026/)).toBeVisible();
    expect(screen.queryByText(/endpoint has retired/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Official shutdown:/)).not.toBeInTheDocument();
  });

  it("only marks date-only retirement after every timezone has passed the date", () => {
    setActiveLanguage("en");
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-19T12:00:00Z"));
    render(<ModelCatalogNotices metadata={{ lifecycle_status: "deprecated", shutdown_date: "2026-11-18" }} />);
    expect(screen.getByText("The official endpoint has retired; choose a replacement model.")).toBeVisible();
  });

  it("keeps a scheduled alias change distinct from a completed redirect", () => {
    setActiveLanguage("en");
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-16T00:00:00Z"));
    render(<ModelCatalogNotices metadata={{ lifecycle_status: "redirected", redirect_date: "2026-10-19", replacement_model: "new-model" }} />);
    expect(screen.getByText("A redirect is scheduled; verify the model actually serving requests.")).toBeVisible();
    expect(screen.getByText(/Planned redirect date: Oct 19, 2026/)).toBeVisible();
    expect(screen.queryByText("This is a redirected alias; the underlying model may have changed.")).not.toBeInTheDocument();
  });

  it("keeps regional Legacy information separate from global retirement", () => {
    setActiveLanguage("en");
    render(<ModelCatalogNotices metadata={{ regional_lifecycle_status: "legacy", regional_eol_date: "2027-03-30", lifecycle_regions: '["us-east-1","us-west-2"]' }} />);
    expect(screen.getByText(/Legacy in selected regions: us-east-1, us-west-2/)).toBeVisible();
    expect(screen.getByText(/Regional end-of-life date: Mar 30, 2027/)).toBeVisible();
    expect(screen.queryByText(/endpoint has retired/)).not.toBeInTheDocument();
  });
});
