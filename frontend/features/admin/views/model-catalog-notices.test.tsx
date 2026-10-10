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
});
