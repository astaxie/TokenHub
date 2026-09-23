import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { PluginPermissionDiffPreview } from "./plugin-permission-diff-preview";
import { PluginsView } from "./plugins";

describe("PluginsView permission diff preview", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("previews manual install permissions without exposing raw package fields", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: permissionDiffPayload("install"),
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const { container } = render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={emptyData()} />);
    fireEvent.click(screen.getByRole("button", { name: "安装本地插件" }));
    fireEvent.change(screen.getByLabelText("下载 URL"), { target: { value: "https://plugins.example/secret.zip?token=raw" } });
    fireEvent.change(screen.getByLabelText("SHA-256 校验"), {
      target: { value: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" },
    });
    fireEvent.click(screen.getByRole("button", { name: "预览权限" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/permission-diff");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/secret.zip?token=raw",
      checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    });

    await waitFor(() => expect(screen.getByText("需要批准")).toBeInTheDocument());
    expect(screen.getByText("新增密钥权限")).toBeInTheDocument();
    expect(screen.getByText(/provider_credentials:read:codex/)).toBeInTheDocument();
    expect(container.textContent).not.toContain("secret.zip?token=raw");
    expect(container.textContent).not.toContain("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef");
    expect(container.textContent).not.toContain("PUBLIC KEY");
  });

  it("renders dynamic permission labels from complete Japanese templates", () => {
    setActiveLanguage("ja");
    render(<PluginPermissionDiffPreview
      draft={{ busy: false, error: "", preview: permissionDiffPayload("install") }}
      onPreview={() => undefined}
      showAction={false}
    />);

    expect(screen.getByText("最高機密度：シークレット")).toBeInTheDocument();
    expect(screen.getByText("信頼状態：信頼済み")).toBeInTheDocument();
    expect(screen.getByText("互換性：互換")).toBeInTheDocument();
    expect(screen.getByText("候補バージョン：1.1.0")).toBeInTheDocument();
    expect(screen.getByText("追加された権限：1")).toBeInTheDocument();
  });

  it.each(["url", "checksum", "source"])("invalidates permission evidence when the package %s changes", async (field) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: permissionDiffPayload("install") }), { status: 200 })));
    const { container } = render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }} data={emptyData()} activeTab="install" />);
    fillCandidate("a");
    fireEvent.click(screen.getByRole("button", { name: "预览权限" }));
    await waitFor(() => expect(container.querySelector("[data-plugin-permission-diff-result]")).not.toBeNull());
    if (field === "source") fireEvent.click(screen.getByRole("tab", { name: "上传 ZIP" }));
    else fireEvent.change(screen.getByLabelText(field === "url" ? "下载 URL" : "SHA-256 校验"), { target: { value: field === "url" ? "https://plugins.example/b.zip" : "b".repeat(64) } });
    expect(container.querySelector("[data-plugin-permission-diff-result]")).toBeNull();
    if (field === "source") fireEvent.click(screen.getByRole("tab", { name: "URL 安装" }));
    else fillCandidate("a");
    expect(container.querySelector("[data-plugin-permission-diff-result]")).toBeNull();
  });

  it.each([true, false])("ignores a superseded preview that completes with success=%s", async (successful) => {
    let completeOldPreview!: (response: Response) => void;
    const oldPreview = new Promise<Response>((resolve) => { completeOldPreview = resolve; });
    const latestPayload = { ...permissionDiffPayload("install"), candidate_version: "2.0.0" };
    const fetchMock = vi.fn()
      .mockReturnValueOnce(oldPreview)
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: latestPayload }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "synthetic-admin" }} data={emptyData()} activeTab="install" />);
    fillCandidate("a");
    fireEvent.click(screen.getByRole("button", { name: "预览权限" }));
    expect(screen.getByRole("button", { name: "预览中" })).toBeDisabled();
    fillCandidate("b");
    fireEvent.click(screen.getByRole("button", { name: "预览权限" }));
    await waitFor(() => expect(screen.getByText("候选版本：2.0.0")).toBeVisible());
    await act(async () => {
      completeOldPreview(new Response(JSON.stringify(successful ? { data: permissionDiffPayload("install") } : { error: { message: "Obsolete preview rejection" } }), { status: successful ? 200 : 400 }));
      await oldPreview;
    });
    expect(screen.getByText("候选版本：2.0.0")).toBeVisible();
    expect(screen.queryByText("候选版本：1.1.0")).not.toBeInTheDocument();
    expect(screen.queryByText(/Obsolete preview rejection/)).not.toBeInTheDocument();
  });

});

function fillCandidate(name: string) {
  fireEvent.change(screen.getByLabelText("下载 URL"), { target: { value: `https://plugins.example/${name}.zip` } });
  fireEvent.change(screen.getByLabelText("SHA-256 校验"), { target: { value: name.repeat(64) } });
}

function permissionDiffPayload(operation: "install" | "update") {
  return {
    operation,
    plugin_id: "tokenhub.provider.codex",
    current_version: operation === "update" ? "1.0.0" : "",
    candidate_version: "1.1.0",
    permission_diff: {
      available: true,
      verdict: "approval_required",
      reason_code: "secret_permission_added",
      highest_sensitivity: "secret",
      summary: {
        added: 1,
        removed: 0,
        unchanged: 0,
        changed_sensitivity: 0,
      },
      added: [
        {
          kind: "provider_credentials",
          name: "codex",
          access: "read",
          sensitivity: "secret",
        },
      ],
    },
    trust: {
      verdict: "trusted",
      checksum_present: true,
      signature_present: true,
      signature_algorithm: "ed25519",
      signature_key_id: "official",
    },
    compatibility: {
      verdict: "compatible",
      plugin_api: "v1",
      manifest_schema_version: 1,
      core_version: "0.7.0",
    },
  };
}
