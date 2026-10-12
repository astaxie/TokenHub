import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import type { RequestDetail } from "../core/types";
import { RequestDetailPanel } from "./audit";

const detail: RequestDetail = {
  log: { id: "log", request_id: "req-one", project_id: "p", api_key_id: "k", model: "rerank", status_code: 200, latency_ms: 12, created_at: "2026-09-22T00:00:00Z" },
  attempts: [], usage: [],
  payload: { id: "body", request_id: "req-one", request_body: '{"query":"capital"}', response_body: '{"results":[{"index":1}]}', request_truncated: false, response_truncated: false, created_at: "2026-09-22T00:00:00Z" },
};

it("keeps request tabs independent and resets them when selecting another request", async () => {
  const errors = vi.spyOn(console, "error").mockImplementation(() => {});
  try {
    const user = userEvent.setup();
    const props = { data: emptyData(), requestID: "req-one", detail, loading: false, error: "", showProviderCost: false };
    const view = render(<RequestDetailPanel {...props} />);
    expect(screen.getByRole("tabpanel", { name: "Response" })).toHaveTextContent("results");
    await user.click(screen.getByRole("tab", { name: "Request" }));
    expect(screen.getByRole("tabpanel", { name: "Request" })).toHaveTextContent("capital");
    view.rerender(<RequestDetailPanel {...props} requestID="req-two" detail={{ ...detail, log: { ...detail.log, request_id: "req-two" } }} />);
    expect(screen.getByRole("tabpanel", { name: "Response" })).toHaveTextContent("results");
    expect(errors).not.toHaveBeenCalled();
  } finally { errors.mockRestore(); }
});
