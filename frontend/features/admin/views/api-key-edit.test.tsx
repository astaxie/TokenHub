import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { APIKey } from "../core/types";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { apiKeyConfig } from "../resources/project-key-config";
import { EditModal, EntityTable } from "./settings-table";

const admin = { id: "usr_editor", username: "editor", name: "Key Editor", email: "editor@example.test", role: "admin", status: "active" };
const savedKey: APIKey = {
  id: "key_edit_test", project_id: "prj_edit_test", owner_user_id: admin.id, name: "Editable key", group: "test",
  model_access_mode: "restricted", allowed_models: ["retired-model", "current-model"],
  status: "active", key_prefix: "sk_test", key_suffix: "1234", limits: { daily_tokens: 1000, max_concurrency: 2 },
};
const api = { baseURL: "https://gateway.example.test", adminToken: "synthetic-admin-token" };

function fixture() {
  const data = emptyData();
  data.users = [admin];
  data.projects = [{ id: savedKey.project_id, name: "Edit project", status: "active" }];
  return data;
}

function renderEditor(item = savedKey) {
  const config = apiKeyConfig();
  const onSave = vi.fn();
  render(<EditModal state={{ config, item }} data={fixture()} api={api} currentUser={admin} loading={false} onClose={vi.fn()} onSave={onSave} />);
  return { config, onSave };
}

beforeEach(() => setActiveLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("API key model access editing", () => {
  it("edits an existing allowlist and quota through one PATCH without rotating the key", async () => {
    const user = userEvent.setup();
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    const { config, onSave } = renderEditor();
    expect(screen.getByRole("combobox", { name: /模型访问模式/ })).toHaveValue("restricted");
    const allowed = screen.getByRole("textbox", { name: /模型允许列表/ });
    expect(allowed).toHaveValue("retired-model, current-model");
    await user.clear(allowed);
    await user.type(allowed, "current-model, replacement-model");
    const dailyTokens = screen.getByRole("spinbutton", { name: "日 Token" });
    await user.clear(dailyTokens);
    await user.type(dailyTokens, "2000");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(onSave).toHaveBeenCalledTimes(1);
    await config.update!(api, savedKey, onSave.mock.calls[0][0]);
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, options] = fetch.mock.calls[0];
    expect(url).toBe(`${api.baseURL}/api/admin/api-keys/${savedKey.id}`);
    expect(options.method).toBe("PATCH");
    expect(JSON.parse(options.body)).toEqual({
      name: savedKey.name, group: "test", owner_user_id: admin.id, status: "active",
      model_access_mode: "restricted", allowed_models: ["current-model", "replacement-model"], ip_allowlist: [],
      rate_limit_rpm: null, token_limit_tpm: null,
      limits: { daily_requests: 0, monthly_requests: 0, daily_tokens: 2000, monthly_tokens: 0, daily_cost_usd: 0, monthly_cost_usd: 0, max_concurrency: 2 },
    });
  });

  it.each([
    { label: "explicit deny-all", model_access_mode: "restricted" as const, allowed_models: [], expected: "restricted" },
    { label: "legacy allowlist", model_access_mode: undefined, allowed_models: ["retired-model"], expected: "restricted" },
    { label: "legacy inheritance", model_access_mode: undefined, allowed_models: [], expected: "inherit" },
  ])("preserves $label on an unrelated edit", async ({ model_access_mode, allowed_models, expected }) => {
    const user = userEvent.setup();
    const { onSave } = renderEditor({ ...savedKey, model_access_mode, allowed_models });
    expect(screen.getByRole("combobox", { name: /模型访问模式/ })).toHaveValue(expected);
    await user.type(screen.getByRole("textbox", { name: "Key 名称" }), " updated");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ model_access_mode: expected, allowed_models: allowed_models.join(", ") }));
  });

  it("allows a restricted empty list to switch explicitly to inheritance", async () => {
    const user = userEvent.setup();
    const { onSave } = renderEditor({ ...savedKey, allowed_models: [] });
    expect(screen.getByRole("textbox", { name: /模型允许列表/ })).toHaveValue("");
    await user.selectOptions(screen.getByRole("combobox", { name: /模型访问模式/ }), "inherit");
    expect(screen.queryByRole("textbox", { name: /模型允许列表/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ model_access_mode: "inherit", allowed_models: "" }));
  });

  it("shows model access but hides edit and rotation from an assigned user without management rights", () => {
    render(<EntityTable config={apiKeyConfig()} data={fixture()} items={[savedKey]} currentUser={{ ...admin, role: "user" }} onEdit={vi.fn()} onDelete={vi.fn()} onAction={vi.fn()} />);
    expect(screen.getByRole("cell", { name: "retired-model, current-model" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "编辑" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "轮换" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "使用" })).toBeVisible();
  });
});
