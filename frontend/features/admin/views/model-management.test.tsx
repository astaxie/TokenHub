import { useState } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { type Model } from "../core/types";
import { emptyData } from "../domain/catalog";
import { type AppLanguage, setActiveLanguage } from "../i18n/runtime";
import { modelConfig } from "../resources/provider-model-config";
import { ModelCreateModal } from "./model-create-modal";
import { ModelDirectoryView } from "./model-directory";

const template: Model = { id: "template", name: "qwen-template", category: "qwen", family: "qwen", modality: "chat", status: "active", context_window: 32000, capabilities: ["chat", "tools"], input_price_usd_per_1m: 1, output_price_usd_per_1m: 2, metadata: { source: "tokenhub-standard-catalog" } };

function fixture() {
  const data = emptyData();
  data.models = [template];
  data.providers = [{ id: "p", name: "Test Provider", type: "mock", priority: 1, status: "active", healthy: true }];
  data.providerModels = [{ id: "pm", provider_id: "p", upstream_model: "upstream-model", status: "active", call_supported: true }];
  return data;
}

function CreateHarness({ selected = template, submitError = "", save = vi.fn() }: { selected?: Model; submitError?: string; save?: (values: Record<string, string>) => void }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const data = fixture();
  data.models = [selected];
  return <ModelCreateModal config={modelConfig()} data={data} values={values} setValues={setValues} loading={false} onClose={vi.fn()} onSave={save} submitError={submitError} />;
}

async function selectTemplate() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /qwen-template/ }));
  await user.click(screen.getByRole("button", { name: "下一步：选择 Provider 模型" }));
  return user;
}

describe("Model creation progressive disclosure", () => {
  it("keeps model identity, routes and prices visible while preserving folded template values", async () => {
    const save = vi.fn();
    render(<CreateHarness save={save} />);
    expect(screen.getByRole("dialog", { name: "新建对外模型" })).toHaveFocus();
    const user = await selectTemplate();
    expect(screen.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByLabelText("系列")).not.toBeVisible();
    expect(screen.getByLabelText("对外模型 ID")).toBeVisible();
    expect(screen.getByLabelText(/^对外输入价 USD\/1M/)).toBeVisible();
    expect(screen.getByRole("button", { name: "创建对外模型" })).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "Test Provider / upstream-model" }));
    await user.click(screen.getByRole("button", { name: "创建对外模型" }));
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ name: "qwen-template", family: "qwen", modality: "chat", context_window: "32000", capabilities: "chat, tools", input_price_usd_per_1m: "1", initial_provider_models: "p|upstream-model" }));
  });

  it("opens custom configuration and focuses the external model ID", async () => {
    const user = userEvent.setup();
    render(<CreateHarness />);
    await user.click(screen.getByRole("button", { name: /自定义对外模型/ }));
    await user.click(screen.getByRole("button", { name: "下一步：选择 Provider 模型" }));
    expect(screen.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("对外模型 ID")).toHaveFocus();
  });

  it("opens incomplete templates and focuses their missing required field", async () => {
    render(<CreateHarness selected={{ ...template, family: "" }} />);
    await selectTemplate();
    expect(screen.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("系列")).toHaveFocus();
  });

  it("reveals and focuses an invalid folded field instead of trapping form submission", async () => {
    const save = vi.fn();
    render(<CreateHarness save={save} />);
    const user = await selectTemplate();
    await user.click(screen.getByRole("checkbox", { name: "Test Provider / upstream-model" }));
    await user.click(screen.getByRole("button", { name: "高级模型设置" }));
    await user.clear(screen.getByLabelText("系列"));
    await user.click(screen.getByRole("button", { name: "高级模型设置" }));
    await user.click(screen.getByRole("button", { name: "创建对外模型" }));
    await waitFor(() => expect(screen.getByLabelText("系列")).toHaveFocus());
    expect(screen.getByLabelText("系列")).toBeVisible();
    expect(screen.getByRole("alert")).toBeVisible();
    expect(save).not.toHaveBeenCalled();
  });

  it("preserves edits and reveals configuration when the server rejects submission", async () => {
    const view = render(<CreateHarness />);
    const user = await selectTemplate();
    await user.clear(screen.getByLabelText("对外模型 ID"));
    await user.type(screen.getByLabelText("对外模型 ID"), "custom-alias");
    view.rerender(<CreateHarness submitError="Synthetic model validation failure" />);
    expect(screen.getByRole("alert")).toHaveFocus();
    expect(screen.getByLabelText("系列")).toBeVisible();
    expect(screen.getByLabelText("对外模型 ID")).toHaveValue("custom-alias");
  });
});

function directory(readOnly = false) {
  const data = fixture();
  data.models = [
    { ...template, name: "published-model", capabilities: ["chat", "tools", "vision", "reasoning"], supported_parameters: ["temperature", "top_p", "max_tokens"], metadata: { directory_role: "external", endpoints: "chat/completions,anthropic" } },
    { ...template, name: "draft-model", metadata: { directory_role: "external" } },
  ];
  data.routes = [{ id: "r", model_name: "published-model", provider_id: "p", provider_model: "upstream-model", status: "active", priority: 1, weight: 100 }];
  data.providers.push({ id: "secondary", name: "Secondary Provider", type: "mock", priority: 2, status: "active", healthy: true });
  data.projects = [{ id: "internal", name: "Internal Project", status: "active" }];
  data.routes.push({ id: "r-secondary", model_name: "published-model", provider_id: "secondary", provider_model: "secondary-upstream-model", status: "disabled", priority: 2, weight: 100, project_scope: "include", project_ids: ["internal"] });
  const edit = vi.fn();
  render(<ModelDirectoryView api={{ baseURL: "", adminToken: "" }} config={modelConfig()} data={data} loading={false} readOnly={readOnly} onReload={vi.fn()} onCreateModel={vi.fn()} onOpenProviders={vi.fn()} onOpenRoutes={vi.fn()} onEditModel={edit} onDeleteModel={vi.fn()} />);
  return { edit };
}

describe("Compact model directory", () => {
  it.each<[AppLanguage, string, string, string, string]>([
    ["en", "View model details: published-model", "Routing policy: published-model", "More actions: published-model", "Model actions: published-model"],
    ["ja", "モデル詳細を表示：published-model", "ルーティングポリシー：published-model", "その他の操作：published-model", "モデルの操作：published-model"],
  ])("localizes complete dynamic action names in %s", async (language, detailsName, routingName, moreName, actionsName) => {
    const user = userEvent.setup();
    setActiveLanguage(language);
    directory();
    const row = screen.getByRole("row", { name: /published-model/ });
    expect(within(row).getByRole("button", { name: detailsName })).toBeVisible();
    expect(within(row).getByRole("button", { name: routingName })).toBeVisible();
    await user.click(within(row).getByRole("button", { name: moreName }));
    expect(screen.getByRole("group", { name: actionsName })).toBeVisible();
  });

  it("defaults to published models and keeps secondary actions behind an accessible menu", async () => {
    const user = userEvent.setup();
    const { edit } = directory();
    expect(screen.getAllByRole("columnheader")).toHaveLength(6);
    expect(screen.queryByText("draft-model")).not.toBeInTheDocument();
    const row = screen.getByRole("row", { name: /published-model/ });
    expect(within(row).getByText("已发布")).toBeVisible();
    expect(within(row).getByText("正常")).toBeVisible();
    expect(within(row).getByRole("button", { name: "编辑" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "下游费用对账单" })).not.toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: /更多操作/ }));
    expect(screen.getByRole("button", { name: "下游费用对账单" })).toHaveFocus();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(within(row).getByRole("button", { name: /更多操作/ })).toHaveFocus();
    await user.click(within(row).getByRole("button", { name: "编辑" }));
    expect(edit).toHaveBeenCalledWith(expect.objectContaining({ name: "published-model" }));
    await user.click(screen.getByRole("button", { name: "草稿/待映射" }));
    await user.click(screen.getByRole("button", { name: /更多操作/ }));
    expect(screen.getByRole("button", { name: "发布" })).toBeDisabled();
  });

  it("keeps upstream identities and management controls out of the read-only directory", () => {
    directory(true);
    expect(screen.getAllByRole("columnheader")).toHaveLength(4);
    expect(screen.queryByText("Test Provider")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /更多操作|新建对外模型|路由策略/ })).not.toBeInTheDocument();
    expect(screen.getByRole("row", { name: /published-model/ })).toHaveTextContent("当前账号可用");
  });

  it("opens complete model facts and every mapping by keyboard and restores focus on close", async () => {
    const user = userEvent.setup();
    directory();
    const trigger = screen.getByRole("button", { name: "查看模型详情：published-model" });
    trigger.focus();
    await user.keyboard("{Enter}");
    const dialog = screen.getByRole("dialog", { name: "模型详情" });
    expect(dialog).toHaveFocus();
    for (const value of ["vision", "reasoning", "chat/completions", "anthropic", "top_p", "max_tokens", "32,000", "upstream-model", "secondary-upstream-model"]) expect(within(dialog).getByText(value, { exact: true })).toBeVisible();
    expect(within(dialog).getByText(/Internal Project/)).toBeVisible();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "模型详情" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("provides complete capabilities in read-only details without exposing upstream mappings", async () => {
    const user = userEvent.setup();
    directory(true);
    await user.click(screen.getByRole("button", { name: "查看模型详情：published-model" }));
    const dialog = screen.getByRole("dialog", { name: "模型详情" });
    expect(within(dialog).getByText("reasoning")).toBeVisible();
    expect(within(dialog).getByText("anthropic")).toBeVisible();
    expect(within(dialog).getByText("max_tokens")).toBeVisible();
    expect(within(dialog).queryByRole("region", { name: "全部上游映射" })).not.toBeInTheDocument();
    expect(dialog).not.toHaveTextContent("Secondary Provider");
    expect(dialog).not.toHaveTextContent("upstream-model");
    expect(dialog).not.toHaveTextContent("Internal Project");
  });
});
