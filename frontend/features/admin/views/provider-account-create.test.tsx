import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginDescriptor, ProviderCatalogEntry } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";

const accountCatalog: ProviderCatalogEntry = { id: "openai-codex", name: "Test Account", display_name: "Test Account", type: "openai_codex", base_url: "https://account.example.test", models_count: 0, source: "test", models: [] };
const alternateCatalog = { ...accountCatalog, id: "alternate-account", name: "Alternate Account", display_name: "Alternate Account" };
const plugin: PluginDescriptor = { id: "test.account", name: "Test Account", version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "openai_subscription", subject: accountCatalog.type }] };
type Write = { path: string; method: string; body: Record<string, unknown> };

function setup({ explicitID = "", providerFailure = false, networkFailure = false } = {}) {
  const writes: Write[] = [];
  let providerAttempts = 0;
  let resourceAttempts = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input)).pathname;
    if (path.includes("/provider-catalog/")) return new Response(JSON.stringify({ data: path.endsWith(alternateCatalog.id) ? alternateCatalog : accountCatalog }));
    const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
    writes.push({ path, method: init?.method ?? "GET", body });
    if (path === "/api/admin/providers" && init?.method === "POST") {
      providerAttempts++;
      if (providerFailure && providerAttempts === 1) return new Response(JSON.stringify({ error: { message: "Synthetic provider failure" } }), { status: 503 });
      return new Response(JSON.stringify({ provider: { id: body.id || (providerAttempts === 1 ? "prv_generated" : "prv_alternate"), name: body.name }, imported_models: 0 }), { status: 201 });
    }
    if (path === `/api/admin/providers/${explicitID || "prv_generated"}` && init?.method === "PATCH") return new Response(JSON.stringify({ provider: { id: explicitID || "prv_generated", name: body.name }, imported_models: 0 }));
    if (path === "/api/admin/provider-resources" && init?.method === "POST") {
      resourceAttempts++;
      if (!providerFailure && resourceAttempts === 1) {
        if (networkFailure) throw new TypeError("Synthetic connection failure");
        return new Response(JSON.stringify({ error: { message: "Synthetic resource failure" } }), { status: 409 });
      }
      return new Response(JSON.stringify({ id: "rsrc_created", ...body }), { status: 201 });
    }
    throw new Error(`Unexpected request: ${init?.method} ${path}`);
  }));
  const onSaved = vi.fn().mockResolvedValue(undefined);
  render(<ProviderUpsertModal api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-session" }} catalog={[accountCatalog, alternateCatalog]} plugins={[plugin]} loading={false} mode="create" onClose={vi.fn()} onSaved={onSaved} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()} />);
  return { writes, onSaved, user: userEvent.setup() };
}

async function enterAccount(user: ReturnType<typeof userEvent.setup>, name = "Test Account") {
  await user.click(screen.getByRole("button", { name: new RegExp(name) }));
  await user.click(screen.getByText("高级：手动粘贴 Token"));
  fireEvent.change(screen.getByLabelText(/^访问 Token/), { target: { value: "synthetic-access-token" } });
}

async function submitAccount(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "下一步" }));
  await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
}

describe("Account provider creation retries", () => {
  it.each([
    { explicitID: "", networkFailure: false, label: "generated ID after an HTTP failure" },
    { explicitID: "prv_explicit", networkFailure: true, label: "explicit ID after a network failure" },
  ])("reuses the $label and applies corrected fields", async ({ explicitID, networkFailure }) => {
    const { user, writes, onSaved } = setup({ explicitID, networkFailure });
    await enterAccount(user);
    if (explicitID) {
      await user.click(screen.getByText("高级连接设置"));
      fireEvent.change(screen.getByLabelText("Provider ID"), { target: { value: explicitID } });
    }
    await submitAccount(user);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Provider 已保存，但账号资源创建失败"));
    expect(onSaved).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "上一步" }));
    await user.click(screen.getByText("高级连接设置"));
    expect(screen.getByLabelText("Provider ID")).toHaveValue(explicitID || "prv_generated");
    expect(screen.getByLabelText("Provider ID")).toHaveAttribute("readonly");
    fireEvent.change(screen.getByLabelText("通道名称"), { target: { value: "Corrected Account Provider" } });
    fireEvent.change(screen.getByLabelText(/^账号资源名称/), { target: { value: "Corrected Account Resource" } });
    await submitAccount(user);
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(writes.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /api/admin/providers", "POST /api/admin/provider-resources", `PATCH /api/admin/providers/${explicitID || "prv_generated"}`, "POST /api/admin/provider-resources",
    ]);
    expect(writes[2].body).toMatchObject({ name: "Corrected Account Provider" });
    expect(writes[2].body.id).toBe(explicitID || "prv_generated");
    expect(writes[3].body).toMatchObject({ provider_id: explicitID || "prv_generated", name: "Corrected Account Resource", credentials: { access_token: "synthetic-access-token" } });
  });

  it("retries Provider creation when the Provider request itself failed", async () => {
    const { user, writes, onSaved } = setup({ providerFailure: true });
    await enterAccount(user);
    await submitAccount(user);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Synthetic provider failure"));
    await user.click(screen.getByRole("button", { name: "添加供应商并引入模型" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(writes.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /api/admin/providers", "POST /api/admin/providers", "POST /api/admin/provider-resources",
    ]);
  });

  it("starts a new Provider when switching services after a partial failure", async () => {
    const { user, writes, onSaved } = setup();
    await enterAccount(user);
    await submitAccount(user);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Provider 已保存，但账号资源创建失败"));
    await user.click(screen.getByRole("button", { name: "更换供应商" }));
    await enterAccount(user, "Alternate Account");
    await submitAccount(user);
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(writes.map(({ method, path }) => `${method} ${path}`)).toEqual([
      "POST /api/admin/providers", "POST /api/admin/provider-resources", "POST /api/admin/providers", "POST /api/admin/provider-resources",
    ]);
    expect(writes[2].body).toMatchObject({ catalog_id: alternateCatalog.id });
    expect(writes[2].body.id).toBe("");
    expect(writes[3].body).toMatchObject({ provider_id: "prv_alternate" });
  });
});
