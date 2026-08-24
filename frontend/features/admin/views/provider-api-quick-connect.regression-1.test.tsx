import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

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
});
