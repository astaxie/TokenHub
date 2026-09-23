import { afterEach, describe, expect, it } from "vitest";

import { type AppLanguage, countRatioWithUnit, setActiveLanguage } from "./runtime";

describe("localized count ratios", () => {
  afterEach(() => setActiveLanguage("en"));

  it.each<[AppLanguage, string]>([
    ["zh-CN", "2/3 个可引入模型"],
    ["en", "2/3 importable models"],
    ["ja", "2/3 件の取り込み可能モデル"],
  ])("preserves the current and total counts in %s", (language, expected) => {
    setActiveLanguage(language);

    expect(countRatioWithUnit(2, 3, "个可引入模型", "importable model", "件の取り込み可能モデル", "importable models")).toBe(expected);
  });
});
