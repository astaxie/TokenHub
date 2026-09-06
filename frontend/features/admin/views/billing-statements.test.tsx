import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { BillingStatements, StatementLauncher } from "./billing-statements";

const api = { baseURL: "", adminToken: "test" };
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, "showModal");
beforeEach(() => setActiveLanguage("zh-CN"));
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, "showModal", originalShowModal); else Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal"); });
describe("BillingStatements", () => {
  it("invalidates a preview when filters change", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ id: "project", name: "Example" }] }))).mockImplementationOnce((_url, options) => {
      const query = JSON.parse(options.body);
      return Promise.resolve(new Response(JSON.stringify({ query, generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "request_admission", rows: [], totals: {}, unknown_count: 0, incomplete_count: 0, estimated_margin_usd: null })));
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<BillingStatements api={api} />);
    await screen.findByRole("option", { name: "Example (project)" });
    expect(screen.queryByRole("button", { name: "导出当前预览 CSV" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("客户名称"), { target: { value: "Customer" } });
    const projects = screen.getByLabelText("客户项目（可多选）") as HTMLSelectElement;
    projects.options[0].selected = true; fireEvent.change(projects);
    fireEvent.submit(screen.getByRole("button", { name: "预览对账单" }).closest("form")!);
    await screen.findByRole("button", { name: "导出当前预览 CSV" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    fireEvent.change(screen.getByLabelText("客户名称"), { target: { value: "Another customer" } });
    expect(screen.queryByRole("button", { name: "导出当前预览 CSV" })).not.toBeInTheDocument();
  });
  it("reports project-loading failures", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    render(<BillingStatements api={api} />);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("offline"));
  });
});

it("portal preview does not submit the parent provider form", async () => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function(this: HTMLDialogElement) { this.setAttribute("open", ""); } });
  const parentSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: [] }))).mockResolvedValueOnce(new Response(JSON.stringify({ query: { side: "provider", customer: "", timezone: "UTC" }, generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "upstream_attempt_start", rows: [], totals: {}, unknown_count: 0, incomplete_count: 0, estimated_margin_usd: null }))));
  render(<form onSubmit={parentSubmit}><StatementLauncher api={api} side="provider" /></form>);
  fireEvent.click(screen.getByRole("button", { name: "上游费用对账单" }));
  fireEvent.submit(screen.getByRole("button", { name: "预览对账单" }).closest("form")!);
  await screen.findByRole("button", { name: "导出当前预览 CSV" });
  expect(parentSubmit).not.toHaveBeenCalled();
});
