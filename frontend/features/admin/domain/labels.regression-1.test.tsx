import { describe, expect, it } from "vitest";

import { fieldValueLabel } from "./labels";

describe("codex fingerprint mode labels", () => {
  it("renders all codex fingerprint mode labels from declarative metadata", () => {
    expect(fieldValueLabel("codex_fingerprint_mode", "off")).toBe("关闭（透传）");
    expect(fieldValueLabel("codex_fingerprint_mode", "device")).toBe("仅收敛设备");
    expect(fieldValueLabel("codex_fingerprint_mode", "session")).toBe("收敛设备与会话（推荐）");
    expect(fieldValueLabel("codex_fingerprint_mode", "full")).toBe("完全收敛");
  });
});
