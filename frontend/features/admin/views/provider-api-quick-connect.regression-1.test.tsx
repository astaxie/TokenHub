import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { type AppLanguage, setActiveLanguage } from "../i18n/runtime";
import { ProviderAPIQuickConnect } from "./provider-api-quick-connect";

describe("Provider model catalog error state", () => {
  it("replaces the import counter with a load failure state", () => {
    render(
      <ProviderAPIQuickConnect
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalogID="custom"
        modelCount={0}
        models={[]}
        modelsLoading={false}
        modelsError="上游拒绝了 Provider 凭据，请检查 API Key 或认证配置。"
        modelQuery=""
        selectedModelCount={0}
        selectedModels={{}}
        activeTab="models"
        values={{ name: "Failing Provider", type: "openai_compatible", base_url: "https://provider.example/v1", api_key: "invalid" }}
        onModelQueryChange={vi.fn()}
        onModelToggle={vi.fn()}
        onReloadModels={vi.fn()}
        onTabChange={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    expect(screen.getByText("加载失败")).toBeInTheDocument();
    expect(screen.getByText("上游拒绝了 Provider 凭据，请检查 API Key 或认证配置。")).toBeInTheDocument();
    expect(screen.queryByText("0/0 待引入")).not.toBeInTheDocument();
  });

  it.each<[AppLanguage, string]>([
    ["zh-CN", "2 个待引入模型"],
    ["en", "2 models to import"],
    ["ja", "2 件の取り込み予定モデル"],
  ])("renders the model summary as a complete %s count label", (language, expected) => {
    setActiveLanguage(language);
    const { container } = render(
      <ProviderAPIQuickConnect
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        catalogID="custom"
        modelCount={3}
        models={[]}
        modelsLoading={false}
        modelsError=""
        modelQuery=""
        selectedModelCount={2}
        selectedModels={{}}
        activeTab="models"
        values={{ name: "Test Provider", type: "openai_compatible", base_url: "https://provider.example/v1", api_key: "valid" }}
        onModelQueryChange={vi.fn()}
        onModelToggle={vi.fn()}
        onReloadModels={vi.fn()}
        onTabChange={vi.fn()}
        onUpdate={vi.fn()}
      />,
    );

    expect(container.querySelector(".provider-quick-model-summary span")).toHaveTextContent(expected);
    setActiveLanguage("zh-CN");
  });
});
