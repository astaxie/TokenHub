import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { importMagpieBalance } from "../core/magpie-balance";
import { ProviderAllowanceConfigForm } from "./provider-allowance-config";
import {
  allowanceEntries,
  allowancePresets,
} from "../../../tests/ui/fixtures/provider-allowances";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
afterEach(() => vi.unstubAllGlobals());

describe("Magpie balance migration", () => {
  it("imports balance settings without importing inference keys or changing policies", () => {
    const current = allowanceEntries()[0].config;
    const imported = importMagpieBalance(
      JSON.stringify({
        key: "ignored-inference-key",
        balanceURL: "https://relay.example/v1/usage",
        balancePath: "$remaining; 周: used / limit %",
        balanceMethod: "post",
        headers: { "X-Key": "{token}" },
      }),
      current,
    );
    expect(imported.source).toEqual({
      enabled: true,
      auth: "magpie",
      url: "https://relay.example/v1/usage",
      expression: "$remaining; 周: used / limit %",
      method: "POST",
      headers: { "X-Key": "{token}" },
      refresh_seconds: 60,
    });
    expect(imported.policy).toEqual(current.policy);
    expect(imported.clear_token).toBe(true);
    expect(JSON.stringify(imported)).not.toContain("ignored-inference-key");
    expect(() =>
      importMagpieBalance(
        '{"balanceURL":"https://relay.example/balance","balanceToken":{"set":true}}',
        current,
      ),
    ).toThrow();
  });

  it("checks the unsaved form with saved credentials and preserves it on failure", async () => {
    let fail = false;
    const fetchMock = vi.fn<typeof fetch>(async () =>
      fail
        ? new Response(
            JSON.stringify({ error: { message: "Synthetic refusal" } }),
            { status: 502 },
          )
        : new Response(
            JSON.stringify({
              allowance: { version: 1, status: "exhausted" },
              balance_parts: [
                { text: "$0.00" },
                { label: "周", text: "100.7%" },
                { label: "月", text: "25.2%" },
              ],
            }),
            { status: 200 },
          ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();
    render(
      <ProviderAllowanceConfigForm
        api={api}
        entry={allowanceEntries()[0]}
        presets={allowancePresets}
        onSaved={onSaved}
      />,
    );
    const user = userEvent.setup();
    const field = screen.getByRole("textbox", {
      name: "余额字段",
    });
    await user.clear(field);
    await user.type(field, "$remaining; 周: used / limit %");
    await user.click(screen.getByRole("button", { name: "POST" }));
    await user.click(
      screen.getByRole("button", { name: "查询余额" }),
    );
    expect(await screen.findByLabelText("余额查询结果")).toHaveTextContent(
      "余额 $0.00 · 周 100.7% · 月 25.2%",
    );
    expect(fetchMock.mock.calls[0][0]).toContain("/allowance/preview");
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({
      revision: "synthetic-revision",
      source: {
        method: "POST",
        expression: "$remaining; 周: used / limit %",
        headers: { "X-Quota-Token": "[redacted]" },
      },
    });
    expect(onSaved).not.toHaveBeenCalled();
    fail = true;
    await user.type(field, "; Missing: absent");
    expect(screen.queryByLabelText("余额查询结果")).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "查询余额" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Synthetic refusal",
    );
    expect(field).toHaveValue(
      "$remaining; 周: used / limit %; Missing: absent",
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "查询余额" }),
      ).toBeEnabled(),
    );
  });
});
