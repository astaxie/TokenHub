import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  localizedCapabilityTitle,
  localizedContributionTitle,
  localizedPluginName,
} = await importTypeScript(new URL("./plugin-localization.ts", import.meta.url));

test("plugin localization resolves exact locale, language aliases, and fallback fields", () => {
  const plugin = {
    id: "tokenhub.provider.codex",
    name: "Codex Provider",
    localizations: {
      "zh-CN": { name: "Codex 订阅" },
      ja: { name: "Codex サブスクリプション" },
    },
  };

  assert.equal(localizedPluginName(plugin, "zh-CN"), "Codex 订阅");
  assert.equal(localizedPluginName(plugin, "ja-JP"), "Codex サブスクリプション");
  assert.equal(localizedPluginName(plugin, "en-US"), "Codex Provider");
});

test("plugin localization uses marketplace localizations for old descriptor payloads", () => {
  const plugin = {
    id: "tokenhub.provider.kimi",
    name: "Kimi Provider",
    marketplace: {
      localizations: {
        "en-US": { name: "Kimi Subscription" },
        "zh-CN": { name: "Kimi 订阅" },
      },
    },
  };

  assert.equal(localizedPluginName(plugin, "en-GB"), "Kimi Subscription");
  assert.equal(localizedPluginName(plugin, "zh-Hans-CN"), "Kimi 订阅");
});

test("plugin localization resolves contribution schema and metadata titles", () => {
  assert.equal(localizedContributionTitle({
    id: "provider-setup",
    title: "Provider setup",
    schema: {
      localizations: {
        "zh-CN": { title: "Provider 设置" },
      },
    },
  }, "zh-CN"), "Provider 设置");

  assert.equal(localizedContributionTitle({
    id: "quota.refresh",
    title: "Refresh quota",
    metadata: {
      "title:ja-JP": "クォータを更新",
    },
  }, "ja-JP"), "クォータを更新");
});

test("plugin localization resolves SIM capability payload titles", () => {
  const capability = {
    id: "enterprise-theme",
    title: "Enterprise Theme",
    payload: {
      localizations: {
        "zh-CN": { title: "企业主题" },
        "ja-JP": { title: "エンタープライズテーマ" },
      },
    },
  };

  assert.equal(localizedCapabilityTitle(capability, "zh-CN"), "企业主题");
  assert.equal(localizedCapabilityTitle(capability, "ja-JP"), "エンタープライズテーマ");
  assert.equal(localizedCapabilityTitle(capability, "en-US"), "Enterprise Theme");
});
