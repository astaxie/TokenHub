import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AllowanceMagpieImport } from "./provider-allowance-magpie-import";
import { ProviderAllowanceConfigForm } from "./provider-allowance-config";
import {
  allowanceEntries,
  allowancePresets,
} from "../../../tests/ui/fixtures/provider-allowances";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
afterEach(() => vi.unstubAllGlobals());

describe("Magpie balance migration", () => {
  it("reads native provider files and selects the account", async () => {
    const current = allowanceEntries()[0].config;
    const onImported = vi.fn();
    const fetchMock = vi.fn<typeof fetch>(
      async () => new Response(JSON.stringify(current), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(
      <AllowanceMagpieImport
        api={api}
        draft={current}
        onImported={onImported}
      />,
    );
    const user = userEvent.setup();
    await user.click(screen.getByText("导入 Magpie 额度配置"));
    await user.click(
      screen.getByRole("textbox", { name: "Magpie 供应商 JSON" }),
    );
    await user.paste(
      JSON.stringify({
        providers: [
          { id: "other" },
          {
            id: "codex",
            accountCaps: { "one@example.test": 70, "two@example.test": 80 },
          },
        ],
      }),
    );
    await user.selectOptions(screen.getByLabelText("Magpie 供应商"), "codex");
    await user.type(
      screen.getByLabelText("Magpie 账号或 Key ID"),
      "two@example.test",
    );
    await user.click(screen.getByRole("button", { name: "填入额度配置" }));
    await waitFor(() => expect(onImported).toHaveBeenCalledOnce());
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({
      provider_id: "codex",
      account_ref: "two@example.test",
      revision: current.revision,
      providers: {
        providers: [
          { id: "other" },
          {
            id: "codex",
            accountCaps: { "one@example.test": 70, "two@example.test": 80 },
          },
        ],
      },
    });
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
    await user.click(screen.getByRole("button", { name: "查询余额" }));
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
    await user.click(screen.getByRole("button", { name: "查询余额" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Synthetic refusal",
    );
    expect(field).toHaveValue(
      "$remaining; 周: used / limit %; Missing: absent",
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "查询余额" })).toBeEnabled(),
    );
  });
});
