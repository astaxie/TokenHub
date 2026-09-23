import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiKeyAccessConfig, apiKeyPlaceholder } from "../domain/api-key-access";
import { setActiveLanguage } from "../i18n/runtime";
import { APIKeyAccessDialogs, APIKeyAccessModal, openAPIKeyAccess } from "./api-key-access";
import type { APIKey } from "../core/types";
import { ConfirmDialog } from "./ui";

const savedKey: APIKey = { id: "key_test", name: "Test key", project_id: "project_test", allowed_models: [], status: "active", key_prefix: "sk_test", key_suffix: "1234" };
const secret = "sk_synthetic_access_test_only";

beforeEach(() => setActiveLanguage("zh-CN"));
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("API key setup", () => {
  it("keeps keyboard focus inside a busy confirmation", () => {
    const cancel = vi.fn();
    render(<ConfirmDialog title="确认轮换 API Key" message="Test confirmation" loading onCancel={cancel} onConfirm={() => undefined} />);
    const dialog = screen.getByRole("dialog");
    expect(fireEvent.keyDown(dialog, { key: "Tab" })).toBe(false);
    expect(dialog).toHaveFocus();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(cancel).not.toHaveBeenCalled();
  });
  it.each([
    ["chat", "/v1", "/v1/chat/completions", "Authorization: Bearer"],
    ["responses", "/v1", "/v1/responses", "Authorization: Bearer"],
    ["anthropic", "", "/v1/messages", "x-api-key:"],
    ["gemini", "", "/v1beta/models/test-model:generateContent", "x-goog-api-key:"],
  ] as const)("builds %s setup with protocol-specific paths and authentication", (protocol, suffix, endpoint, header) => {
    const config = apiKeyAccessConfig("https://gateway.example.test/prefix/v1/", protocol, secret, "test-model");
    expect(config.base).toBe(`https://gateway.example.test/prefix${suffix}`);
    expect(config.endpoint).toBe(`https://gateway.example.test/prefix${endpoint}`);
    expect(config.example).toContain(header);
    expect(config.example).toContain(secret);
    expect(config.example).not.toContain("3000");
    if (protocol === "anthropic") expect(config.example).toContain("anthropic-version: 2023-06-01");
  });

  it("uses placeholders and escapes shell metacharacters in examples", () => {
    expect(apiKeyAccessConfig("https://gateway.example.test", "chat", "", "").example).toContain(apiKeyPlaceholder);
    expect(apiKeyAccessConfig("https://gateway.example.test", "chat", "", "").example).toContain("YOUR_MODEL");
    const config = apiKeyAccessConfig("https://gateway.example.test", "chat", "key'$(echo unsafe)", "model'$(echo unsafe)");
    expect(config.example).toContain("key'\\''$(echo unsafe)");
    expect(config.example).toContain("model'\\''$(echo unsafe)");
    expect(apiKeyAccessConfig("https://gateway.example.test/v1beta/", "gemini", "", "folder/model").base).toBe("https://gateway.example.test");
    expect(apiKeyAccessConfig("https://gateway.example.test/v1beta/", "gemini", "", "folder/model").endpoint).toBe("https://gateway.example.test/v1beta/models/folder%2Fmodel:generateContent");
  });

  it("copies a newly issued key and removes it after closing and reopening", async () => {
    vi.useFakeTimers();
    const copy = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText: copy } });
    function Host() {
      const [issuedKey, setIssuedKey] = useState(secret);
      return <APIKeyAccessDialogs baseURL="https://gateway.example.test" issuedKey={issuedKey} onCloseIssuedKey={() => setIssuedKey("")} />;
    }
    render(<Host />);
    expect(screen.getByLabelText("完整 Key")).toHaveValue(secret);
    expect(screen.getByRole("button", { name: "3s 后可关闭" })).toBeDisabled();
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "复制 Key" })));
    expect(copy).toHaveBeenCalledWith(secret);
    for (let i = 0; i < 3; i++) act(() => vi.advanceTimersByTime(1000));
    fireEvent.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    act(() => openAPIKeyAccess(savedKey));
    expect(screen.getByLabelText("API Key 占位符")).toHaveValue(apiKeyPlaceholder);
    expect(screen.queryByDisplayValue(secret)).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    vi.unstubAllGlobals();
  });

  it("reports failed copying without claiming success", async () => {
    vi.stubGlobal("navigator", { clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) } });
    render(<APIKeyAccessModal baseURL="https://gateway.example.test" onClose={() => undefined} />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "复制地址" })));
    expect(screen.getByRole("status")).toHaveTextContent("复制失败");
    vi.unstubAllGlobals();
  });

  it.each(["disabled", "revoked", "expired"])("warns when a saved key is %s", status => {
    render(<APIKeyAccessModal baseURL="https://gateway.example.test" apiKey={{ ...savedKey, status: status === "expired" ? "active" : status, expires_at: status === "expired" ? "2000-01-01T00:00:00Z" : undefined }} onClose={() => undefined} />);
    expect(screen.getByRole("status")).toHaveTextContent("不能用于发起请求");
  });
});
