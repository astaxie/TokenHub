import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
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

  it("previews installed plugin updates through the plugin-scoped endpoint", async () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.codex",
      name: "Codex Provider",
      version: "1.0.0",
      source: "marketplace",
      status: "enabled",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [],
      distribution: {
        download_url: "https://plugins.example/codex.zip",
        checksum_sha256: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: permissionDiffPayload("update"),
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);
    const previewButtons = screen.getAllByRole("button", { name: "预览权限" }) as HTMLButtonElement[];
    const updatePreviewButton = previewButtons.find((button) => !button.disabled);
    if (!updatePreviewButton) throw new Error("Expected an enabled update permission preview button");
    fireEvent.click(updatePreviewButton);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.codex/permission-diff");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/codex.zip",
      checksum_sha256: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
    });
    await waitFor(() => expect(screen.getByText("需要批准")).toBeInTheDocument());
  });
});

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
