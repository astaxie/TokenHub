import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { languageFromLocales, preferredLanguage } from "../frontend/features/admin/i18n/language-preference.ts";

describe("admin language preference", () => {
  it("detects supported browser languages", () => {
    assert.equal(languageFromLocales(["zh-CN"]), "zh-CN");
    assert.equal(languageFromLocales(["zh-Hant-TW"]), "zh-CN");
    assert.equal(languageFromLocales(["ja-JP"]), "ja");
    assert.equal(languageFromLocales(["en-GB"]), "en");
  });

  it("uses the first supported browser language and otherwise falls back to English", () => {
    assert.equal(languageFromLocales(["fr-FR", "ja-JP", "en-US"]), "ja");
    assert.equal(languageFromLocales(["fr-FR", "de-DE"]), "en");
  });

  it("keeps a saved choice ahead of browser detection", () => {
    assert.equal(preferredLanguage("en", ["zh-CN"]), "en");
    assert.equal(preferredLanguage("zh-CN", ["en-US"]), "zh-CN");
    assert.equal(preferredLanguage("unsupported", ["ja-JP"]), "ja");
  });
});
