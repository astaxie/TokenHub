import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProviderAllowanceConfigForm } from "./provider-allowance-config";
import {
  allowanceEntries,
  allowancePresets,
} from "../../../tests/ui/fixtures/provider-allowances";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
afterEach(() => vi.unstubAllGlobals());

function setup() {
  const entry = allowanceEntries()[0];
  entry.config.source.headers = {
    "X-First": "synthetic-first",
    "X-Second": "synthetic-second",
  };
  entry.config.policy.windows = {
    weekly: { percent: 70 },
    monthly: { percent: 90 },
  };
  const fetchMock = vi.fn<typeof fetch>(
    async (_input, init) =>
      new Response(
        JSON.stringify(
          init?.method === "PUT" ? JSON.parse(String(init.body)) : { data: [] },
        ),
        { status: 200 },
      ),
  );
  vi.stubGlobal("fetch", fetchMock);
  const saved = vi.fn();
  render(
    <ProviderAllowanceConfigForm
      api={api}
      entry={entry}
      presets={allowancePresets}
      onSaved={saved}
    />,
  );
  return { user: userEvent.setup(), fetchMock, saved };
}
async function save(state: ReturnType<typeof setup>) {
  await state.user.click(screen.getByRole("button", { name: "保存额度配置" }));
  await waitFor(() => expect(state.saved).toHaveBeenCalledOnce());
  return JSON.parse(
    String(
      state.fetchMock.mock.calls.find(([, init]) => init?.method === "PUT")![1]
        ?.body,
    ),
  );
}

describe("Provider allowance form editing", () => {
  it("preserves typed model delimiters and normalizes scopes on save", async () => {
    const state = setup();
    const models = screen.getByLabelText("适用模型（逗号分隔）");
    const excluded = screen.getByLabelText("排除模型（逗号分隔）");
    await state.user.clear(models);
    await state.user.type(models, "gpt-*, claude-*,");
    await state.user.type(excluded, "gpt-mini*, claude-haiku*,");
    expect(models).toHaveValue("gpt-*, claude-*,");
    expect(excluded).toHaveValue("gpt-mini*, claude-haiku*,");
    const payload = await save(state);
    expect(payload.source.windows[0].models).toEqual(["gpt-*", "claude-*"]);
    expect(payload.source.windows[0].excluded_models).toEqual([
      "gpt-mini*",
      "claude-haiku*",
    ]);
  });
  it("keeps cap row identity, order, and values through renames and tab switches", async () => {
    const state = setup();
    await state.user.click(screen.getByRole("button", { name: "使用与权益" }));
    const names = screen.getAllByLabelText("窗口名称");
    await state.user.clear(names[0]);
    await state.user.type(names[0], "monthly");
    expect(names[1]).toHaveValue("monthly");
    await state.user.clear(names[0]);
    await state.user.type(names[0], "daily");
    expect(names[0]).toHaveValue("daily");
    expect(names[1]).toHaveValue("monthly");
    expect(screen.getAllByLabelText("窗口比例上限")[0]).toHaveValue(70);
    await state.user.click(screen.getByRole("button", { name: "额度采集" }));
    await state.user.click(screen.getByRole("button", { name: "使用与权益" }));
    const payload = await save(state);
    expect(payload.policy.windows).toEqual({
      daily: { percent: 70 },
      monthly: { percent: 90 },
    });
  });
  it("keeps header row identity and values while renaming the first header", async () => {
    const state = setup();
    const names = screen.getAllByLabelText("请求头名称");
    await state.user.clear(names[0]);
    await state.user.type(names[0], "X-New");
    expect(names[0]).toHaveValue("X-New");
    expect(names[1]).toHaveValue("X-Second");
    await state.user.click(screen.getByRole("button", { name: "使用与权益" }));
    const payload = await save(state);
    expect(payload.source.headers).toEqual({
      "X-New": "synthetic-first",
      "X-Second": "synthetic-second",
    });
  });
});
