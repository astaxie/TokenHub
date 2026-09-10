import { describe, expect, it } from "vitest";

import { codexFingerprintModeLabel, projectKeyDownloadFilename, projectKeyDownloadTemplates } from "./project-key-download-templates";

describe("projectKeyDownloadTemplates", () => {
  it("exposes explicit download metadata for the project key menu", () => {
    expect(projectKeyDownloadTemplates).toHaveLength(2);
    expect(projectKeyDownloadTemplates[0]).toMatchObject({
      id: "codex_cli_config",
      menuLabel: "Codex CLI 配置",
      menuSubtitle: "config.toml · 连接 TokenHub Responses",
      filenameSuffix: "-codex-config.toml",
    });
    expect(projectKeyDownloadTemplates[1]).toMatchObject({
      id: "environment",
      menuLabel: "环境变量模板",
      menuSubtitle: ".env · 替换 Key 占位符",
      filenameSuffix: ".env",
    });
  });

  it("derives sanitized filenames from the template metadata", () => {
    expect(projectKeyDownloadFilename("My Key", projectKeyDownloadTemplates[0])).toBe("my-key-codex-config.toml");
    expect(projectKeyDownloadFilename("My Key", projectKeyDownloadTemplates[1])).toBe("my-key.env");
  });
});

describe("codexFingerprintModeLabel", () => {
  it("maps the known fingerprint modes through declarative metadata", () => {
    expect(codexFingerprintModeLabel("off")).toBe("关闭（透传）");
    expect(codexFingerprintModeLabel("device")).toBe("仅收敛设备");
    expect(codexFingerprintModeLabel("session")).toBe("收敛设备与会话（推荐）");
    expect(codexFingerprintModeLabel("full")).toBe("完全收敛");
    expect(codexFingerprintModeLabel("custom")).toBe("custom");
  });
});
