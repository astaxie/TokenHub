import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProviderAPIQuickConnect } from "./provider-api-quick-connect";

function ProviderHarness() {
  const [values, setValues] = useState<Record<string, string>>({
    name: "Local Test Provider",
    type: "openai_compatible",
    base_url: "",
    api_key: "",
    priority: "10",
  });
  return (
    <ProviderAPIQuickConnect
      api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
      catalogID="custom"
      modelCount={0}
      models={[]}
      modelsLoading={false}
      modelsError=""
      modelQuery=""
      selectedModelCount={0}
      selectedModels={{}}
      activeTab="connect"
      values={values}
      onModelQueryChange={vi.fn()}
      onModelToggle={vi.fn()}
      onReloadModels={vi.fn()}
      onTabChange={vi.fn()}
      onUpdate={(key, value) => setValues((current) => ({ ...current, [key]: value }))}
    />
  );
}

describe("ProviderAPIQuickConnect", () => {
  it("enables connection testing only after required fields are present and sends the expected payload", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ healthy: true, latency_ms: 12 }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    render(<ProviderHarness />);

    const testButton = screen.getByRole("button", { name: "测试连接" });
    expect(testButton).toBeDisabled();

    await user.type(screen.getByLabelText("Base URL"), "http://127.0.0.1:18080/v1");
    await user.type(screen.getByLabelText("API Key"), "provider-secret");
    expect(testButton).toBeEnabled();
    await user.click(testButton);

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("API Key 配置有效"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/providers/test-connection");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("authorization")).toBe("Bearer admin-token");
    expect(JSON.parse(String(init.body))).toMatchObject({
      catalog_id: "custom",
      name: "Local Test Provider",
      type: "openai_compatible",
      base_url: "http://127.0.0.1:18080/v1",
      api_key: "provider-secret",
    });
  });
});
