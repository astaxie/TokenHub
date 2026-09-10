import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  isSafeMarketplaceURL,
  pluginMarketplaceAdvisories,
  pluginMarketplaceCompatibilityState,
  pluginMarketplaceDisplay,
  pluginMarketplaceDistributionLinks,
  pluginMarketplacePublisher,
  pluginMarketplaceScreenshots,
} = await importTypeScript(new URL("./plugin-marketplace.ts", import.meta.url));

test("plugin marketplace display defaults old backend descriptors deterministically", () => {
  const state = pluginMarketplaceDisplay({
    id: "tokenhub.provider.kimi",
    name: "Kimi Provider",
    version: "1.0.0",
    source: "marketplace",
    kinds: ["provider"],
    placements: ["gateway_chain"],
    capabilities: [],
  });

  assert.equal(state.id, "tokenhub.provider.kimi");
  assert.equal(state.name, "Kimi Provider");
  assert.equal(state.summary, "Kimi Provider");
  assert.equal(state.description, "Kimi Provider");
  assert.deepEqual(state.categories, ["provider"]);
  assert.equal(state.installed, true);
  assert.equal(state.installedVersion, "1.0.0");
  assert.equal(state.updateAvailable, false);
  assert.equal(state.distribution.packageReady, false);
  assert.equal(state.compatibility.verdict, "unknown");
  assert.equal(state.compatibility.tone, "neutral");
  assert.equal(state.trust.verdict, "unverified");
  assert.equal(state.lifecycle.status, "enabled");
  assert.equal(state.lifecycle.loadable, true);
  assert.deepEqual(state.advisories, []);
  assert.deepEqual(state.screenshots, []);
  assert.equal(state.latestReleaseNote, null);
});

test("plugin marketplace display accepts marketplace entries and locale fallbacks", () => {
  const state = pluginMarketplaceDisplay({
    installed: true,
    installed_version: "1.1.0",
    update_available: true,
    plugin: {
      id: "tokenhub.provider.codex",
      name: "Codex Provider",
      version: "1.2.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [],
      marketplace: {
        summary: "Default summary",
        categories: ["provider", "subscription", "provider"],
        localizations: {
          "zh-CN": {
            name: "Codex 订阅",
            summary: "管理 Codex 订阅",
            description: "把 Codex 订阅接入网关。",
          },
        },
      },
    },
  }, { locale: "zh-CN" });

  assert.equal(state.name, "Codex 订阅");
  assert.equal(state.summary, "管理 Codex 订阅");
  assert.equal(state.description, "把 Codex 订阅接入网关。");
  assert.deepEqual(state.categories, ["provider", "subscription"]);
  assert.equal(state.installed, true);
  assert.equal(state.installedVersion, "1.1.0");
  assert.equal(state.updateAvailable, true);
});

test("plugin marketplace display uses the manifest description", () => {
  const state = pluginMarketplaceDisplay({
    id: "tokenhub.extension.trace",
    name: "Trace Export",
    version: "1.0.0",
    description: "Exports gateway traces to the configured destination.",
    source: "local_file",
    kinds: ["extension"],
    placements: ["gateway_chain"],
    capabilities: [],
  });

  assert.equal(state.summary, "Exports gateway traces to the configured destination.");
  assert.equal(state.description, "Exports gateway traces to the configured destination.");
});

test("plugin marketplace links omit unsafe and malformed URLs", () => {
  const distribution = pluginMarketplaceDistributionLinks({
    id: "tokenhub.provider.kimi",
    name: "Kimi",
    version: "1.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    distribution: {
      marketplace_url: "https://plugins.example/kimi",
      repository_url: "javascript:alert(1)",
      homepage_url: "not a url",
      download_url: "https://plugins.example/kimi.zip",
      signature_url: "data:text/plain;base64,abc",
      checksum_sha256: "abc123",
    },
  });

  assert.equal(isSafeMarketplaceURL("https://plugins.example/kimi"), true);
  assert.equal(isSafeMarketplaceURL("http://plugins.example/kimi"), true);
  assert.equal(isSafeMarketplaceURL("javascript:alert(1)"), false);
  assert.equal(isSafeMarketplaceURL("data:text/plain;base64,abc"), false);
  assert.equal(isSafeMarketplaceURL("not a url"), false);
  assert.equal(distribution.marketplaceURL, "https://plugins.example/kimi");
  assert.equal(distribution.repositoryURL, "");
  assert.equal(distribution.homepageURL, "");
  assert.equal(distribution.downloadURL, "https://plugins.example/kimi.zip");
  assert.equal(distribution.signatureURL, "");
  assert.equal(distribution.packageReady, true);
  assert.equal(distribution.signatureReady, false);
});

test("plugin marketplace compatibility prefers runtime verdict and keeps safe badges", () => {
  const state = pluginMarketplaceCompatibilityState({
    id: "tokenhub.extension.cache",
    name: "Cache",
    version: "1.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    compatibility: {
      plugin_api: "1",
      manifest_schema_version: 1,
      core_version: "2.0.0",
      verdict: "incompatible",
      reason_code: "core_too_old",
    },
    marketplace: {
      compatibility: {
        verdict: "compatible",
        badges: [
          { id: "gateway", label: "Gateway", tone: "ok", url: "https://docs.example/gateway" },
          { id: "bad", label: "Bad", tone: "strange", url: "javascript:alert(1)" },
        ],
      },
    },
  });

  assert.equal(state.verdict, "incompatible");
  assert.equal(state.rawVerdict, "incompatible");
  assert.equal(state.labelKey, "不兼容");
  assert.equal(state.tone, "error");
  assert.equal(state.reasonCode, "core_too_old");
  assert.deepEqual(state.badges, [
    { id: "gateway", label: "Gateway", tone: "ok", url: "https://docs.example/gateway" },
    { id: "bad", label: "Bad", tone: "neutral", url: "" },
  ]);
});

test("plugin marketplace advisories derive severity tones and drop empty records", () => {
  const advisories = pluginMarketplaceAdvisories({
    id: "tokenhub.extension.privacy",
    name: "Privacy",
    version: "1.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    marketplace: {
      advisories: [
        { id: "CVE-1", severity: "high", title: "Header leak", url: "https://security.example/CVE-1" },
        { severity: "medium", title: "Review needed", url: "data:text/plain,unsafe" },
        { severity: "critical" },
      ],
    },
  });

  assert.deepEqual(advisories, [
    {
      id: "CVE-1",
      severity: "high",
      labelKey: "高危",
      tone: "error",
      title: "Header leak",
      url: "https://security.example/CVE-1",
      publishedAt: "",
      updatedAt: "",
    },
    {
      id: "Review needed",
      severity: "medium",
      labelKey: "中危",
      tone: "warn",
      title: "Review needed",
      url: "",
      publishedAt: "",
      updatedAt: "",
    },
  ]);
});

test("plugin marketplace screenshots normalize safe image links and dimensions", () => {
  const screenshots = pluginMarketplaceScreenshots({
    id: "tokenhub.admin.dashboard",
    name: "Dashboard",
    version: "1.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    marketplace: {
      screenshots: [
        {
          url: "https://cdn.example/screen.png",
          thumbnail_url: "javascript:alert(1)",
          caption: "Runtime panel",
          locale: "en",
          width: 1280,
          height: -1,
        },
        { url: "data:image/png;base64,abc", alt: "Unsafe" },
      ],
    },
  });

  assert.deepEqual(screenshots, [{
    url: "https://cdn.example/screen.png",
    thumbnailURL: "https://cdn.example/screen.png",
    alt: "Runtime panel",
    caption: "Runtime panel",
    locale: "en",
    width: 1280,
    height: undefined,
  }]);
});

test("plugin marketplace publisher display sanitizes external links", () => {
  const publisher = pluginMarketplacePublisher({
    id: "tokenhub.provider.gm",
    name: "GM",
    version: "1.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    marketplace: {
      publisher: {
        id: "official",
        name: "TokenHub",
        verified: true,
        url: "https://tokenhub.example",
        support_url: "mailto:support@example.com",
        contact_url: "javascript:alert(1)",
      },
    },
  });

  assert.equal(publisher.name, "TokenHub");
  assert.equal(publisher.verified, true);
  assert.equal(publisher.verificationLabelKey, "已验证");
  assert.equal(publisher.url, "https://tokenhub.example/");
  assert.equal(publisher.supportURL, "");
  assert.equal(publisher.contactURL, "");
});

test("plugin marketplace display derives latest release notes deterministically", () => {
  const state = pluginMarketplaceDisplay({
    id: "tokenhub.sim.billing",
    name: "Billing SIM",
    version: "2.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    marketplace: {
      localizations: {
        en: { release_notes: "Localized fallback release." },
      },
      release_notes: [
        { version: "1.0.0", date: "2026-01-01", title: "Initial", notes: "First release.", url: "https://docs.example/1" },
        { version: "2.0.0", date: "2025-01-01", title: "Current", notes: "Current release.", items: [" Added billing ", ""] },
        { version: "1.5.0", date: "2026-06-01", title: "Previous", notes: "Previous release." },
      ],
    },
  }, { locale: "en-US" });

  assert.equal(state.releaseNotes.length, 3);
  assert.equal(state.latestReleaseNote.version, "2.0.0");
  assert.deepEqual(state.latestReleaseNote.items, ["Added billing"]);
  assert.equal(state.releaseNotes[0].url, "https://docs.example/1");
});

test("plugin marketplace display falls back to localized release notes when structured notes are absent", () => {
  const state = pluginMarketplaceDisplay({
    id: "tokenhub.admin.theme",
    name: "Theme",
    version: "1.0.0",
    source: "marketplace",
    kinds: [],
    placements: [],
    capabilities: [],
    marketplace: {
      localizations: {
        en: { release_notes: "Theme polish." },
      },
    },
  }, { locale: "en" });

  assert.deepEqual(state.releaseNotes, [{
    version: "1.0.0",
    date: "",
    title: "1.0.0",
    notes: "Theme polish.",
    url: "",
    items: [],
  }]);
  assert.equal(state.latestReleaseNote.version, "1.0.0");
});
