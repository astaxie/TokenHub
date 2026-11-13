import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProviderAllowanceConsole } from "./provider-allowance-console";
import { ProviderAllowanceConfigForm } from "./provider-allowance-config";
import {
  allowanceEntries,
  allowancePresets,
} from "../../../tests/ui/fixtures/provider-allowances";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Provider allowance console", () => {
  it("reads cached data without refreshing or spending entitlements", async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async (input) =>
        new Response(
          JSON.stringify(
            String(input).endsWith("/presets")
              ? allowancePresets
              : { data: allowanceEntries() },
          ),
          { status: 200 },
        ),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(
      <ProviderAllowanceConsole
        api={api}
        providers={[
          {
            id: "quota-provider",
            name: "Synthetic Relay",
            type: "anthropic",
            base_url: "",
            status: "active",
            healthy: true,
            priority: 1,
          },
        ]}
      />,
    );
    await userEvent
      .setup()
      .click(
        await screen.findByRole("button", { name: /Synthetic Failed Query/ }),
      );
    expect(
      screen.getByText(
        "查询失败。以下保留上次成功的数据，调度不会把它视为可用额度。",
      ),
    ).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(
      fetchMock.mock.calls.every(
        ([, init]) => !init?.method || init.method === "GET",
      ),
    ).toBe(true);
  });
  it("preserves the edited rule and revision when a save conflicts", async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async () =>
        new Response(
          JSON.stringify({ error: { message: "Synthetic revision conflict" } }),
          { status: 409 },
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
    const expression = screen.getAllByRole("textbox", {
      name: "余额表达式",
    })[0];
    await user.clear(expression);
    await user.type(expression, "$data.balance");
    await user.click(screen.getByRole("button", { name: "保存额度配置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Synthetic revision conflict",
    );
    expect(expression).toHaveValue("$data.balance");
    expect(onSaved).not.toHaveBeenCalled();
    const payload = JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(payload).toMatchObject({
      revision: "synthetic-revision",
      source: {
        expression: "$data.balance",
        headers: { "X-Quota-Token": "[redacted]" },
      },
      policy: { reset: { on_exhausted: false, before_expiry: false } },
    });
    expect(payload).not.toHaveProperty("token");
  });
  it("saves a deliberate credit policy without enabling automatic resets", async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async (_input, init) =>
        new Response(
          JSON.stringify(
            init?.method === "PUT"
              ? JSON.parse(String(init.body))
              : { data: [] },
          ),
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
    await user.click(screen.getByRole("button", { name: "使用与权益" }));
    await user.selectOptions(screen.getByLabelText("积分使用策略"), "last");
    await user.click(screen.getByRole("button", { name: "保存额度配置" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    const save = fetchMock.mock.calls.find(
      ([, init]) => init?.method === "PUT",
    )!;
    expect(JSON.parse(String(save[1]?.body)).policy).toMatchObject({
      credits: "last",
      reset: { on_exhausted: false, before_expiry: false },
    });
  });
});
