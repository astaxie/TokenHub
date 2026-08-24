import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { setActiveLanguage } from "../i18n/runtime";
import { enumOptionLabel, fieldValueLabel } from "./labels";

describe("model modality labels", () => {
  beforeEach(() => setActiveLanguage("zh-CN"));
  afterEach(() => setActiveLanguage("en"));

  it("localizes input and output modality option values", () => {
    expect(enumOptionLabel("input_modalities", "text")).toBe("文本");
    expect(enumOptionLabel("output_modalities", "image")).toBe("图像");
  });

  it("localizes modality arrays consistently", () => {
    expect(fieldValueLabel("input_modalities", ["text", "audio"])).toBe("文本, 音频");
  });
});
