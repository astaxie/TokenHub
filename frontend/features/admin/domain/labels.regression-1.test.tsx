import { afterEach, describe, expect, it } from "vitest";

import { enumValueLabel, fieldValueLabel } from "./labels";
import { type AppLanguage, setActiveLanguage } from "../i18n/runtime";

describe("codex fingerprint mode labels", () => {
  it("renders all codex fingerprint mode labels from declarative metadata", () => {
    expect(fieldValueLabel("codex_fingerprint_mode", "off")).toBe("关闭（透传）");
    expect(fieldValueLabel("codex_fingerprint_mode", "device")).toBe("仅收敛设备");
    expect(fieldValueLabel("codex_fingerprint_mode", "session")).toBe("收敛设备与会话（推荐）");
    expect(fieldValueLabel("codex_fingerprint_mode", "full")).toBe("完全收敛");
  });
});

describe("decision modality labels", () => {
  afterEach(() => setActiveLanguage("en"));

  it.each<[AppLanguage, string]>([
    ["zh-CN", "决策"],
    ["en", "Decision"],
    ["ja", "判定"],
  ])("localizes the decision modality in %s", (language, expected) => {
    setActiveLanguage(language);
    expect(enumValueLabel("decision")).toBe(expected);
  });
});
