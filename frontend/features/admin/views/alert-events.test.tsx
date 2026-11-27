import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { AlertEventsView } from "./alert-events";

const api = { baseURL: "http://test.invalid", adminToken: "synthetic-test-token" };

describe("AlertEventsView", () => {
  beforeEach(() => setActiveLanguage("zh-CN"));
  it("requests server pages and resets pagination when filtering", async () => {
    const fetch = vi.fn().mockImplementation(async (url: string) => {
      const query = new URL(url).searchParams;
      return new Response(JSON.stringify({ data: [{ id: "alert_"+query.get("page"), code: "quota_exceeded", message: "Synthetic event", severity: "warning", created_at: "2026-01-01T00:00:00Z" }], total: 120, page: Number(query.get("page")), page_size: Number(query.get("page_size")) }));
    });
    vi.stubGlobal("fetch", fetch);
    render(<AlertEventsView api={api} data={emptyData()} actionPending={false} onAction={vi.fn()} />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(new URL(fetch.mock.calls[0][0]).searchParams.get("page_size")).toBe("50");
    await screen.findByText("Synthetic event");
    fireEvent.click(screen.getByTitle("下一页"));
    await waitFor(() => expect(new URL(fetch.mock.calls.at(-1)![0]).searchParams.get("page")).toBe("2"));
    fireEvent.change(screen.getByLabelText("项目 ID"), { target: { value: "project_test" } });
    await waitFor(() => {
      const query = new URL(fetch.mock.calls.at(-1)![0]).searchParams;
      expect(query.get("page")).toBe("1");
      expect(query.get("project_id")).toBe("project_test");
    });
  });
  it("cancels an obsolete history request", async () => {
    const fetch = vi.fn().mockImplementation((_url, options: RequestInit) => new Promise((_resolve, reject) => {
      options.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
    }));
    vi.stubGlobal("fetch", fetch);
    const view = render(<AlertEventsView api={api} data={emptyData()} actionPending={false} onAction={vi.fn()} />);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    const signal = fetch.mock.calls[0][1].signal;
    view.unmount();
    expect(signal.aborted).toBe(true);
  });
});
