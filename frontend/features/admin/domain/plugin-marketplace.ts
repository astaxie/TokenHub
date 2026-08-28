import { type PluginDescriptor, type PluginMarketplacePlugin } from "../core/types";

export type PluginMarketplaceTone = "ok" | "warn" | "error" | "neutral";

export type PluginMarketplaceCompatibilityVerdict = "compatible" | "needs_review" | "incompatible" | "unknown";

export type PluginMarketplaceScreenshotDisplay = {
  url: string;
  thumbnailURL: string;
  alt: string;
  caption: string;
  locale: string;
  width?: number;
  height?: number;
};

export type PluginMarketplaceCompatibilityBadgeDisplay = {
  id: string;
  label: string;
  tone: PluginMarketplaceTone;
  url: string;
};

export type PluginMarketplaceCompatibilityState = {
  verdict: PluginMarketplaceCompatibilityVerdict;
  rawVerdict: string;
  labelKey: string;
  tone: PluginMarketplaceTone;
  reasonCode: string;
  badges: PluginMarketplaceCompatibilityBadgeDisplay[];
};

export type PluginMarketplacePublisherDisplay = {
  id: string;
  name: string;
  verified: boolean;
  verificationLabelKey: string;
  url: string;
  supportURL: string;
  contactURL: string;
};

export type PluginMarketplaceAdvisoryDisplay = {
  id: string;
  severity: string;
  labelKey: string;
  tone: PluginMarketplaceTone;
  title: string;
  url: string;
  publishedAt: string;
  updatedAt: string;
};

export type PluginMarketplaceReleaseNoteDisplay = {
  version: string;
  date: string;
  title: string;
  notes: string;
  url: string;
  items: string[];
};

export type PluginMarketplaceDistributionLinks = {
  marketplaceURL: string;
  repositoryURL: string;
  homepageURL: string;
  downloadURL: string;
  signatureURL: string;
  checksumSHA256: string;
  signatureAlgorithm: string;
  signatureKeyID: string;
  license: string;
  packageReady: boolean;
  signatureReady: boolean;
};

export type PluginMarketplaceDisplayState = {
  plugin: PluginDescriptor | null;
  id: string;
  name: string;
  version: string;
  source: string;
  installed: boolean;
  installedVersion: string;
  updateAvailable: boolean;
  summary: string;
  description: string;
  categories: string[];
  distribution: PluginMarketplaceDistributionLinks;
  compatibility: PluginMarketplaceCompatibilityState;
  trust: {
    verdict: string;
    tone: PluginMarketplaceTone;
    labelKey: string;
    checksumPresent: boolean;
    signaturePresent: boolean;
    reasonCode: string;
  };
  lifecycle: {
    status: string;
    labelKey: string;
    tone: PluginMarketplaceTone;
    restartRequired: boolean;
    mandatory: boolean;
    loadable: boolean;
  };
  publisher: PluginMarketplacePublisherDisplay;
  advisories: PluginMarketplaceAdvisoryDisplay[];
  highestAdvisoryTone: PluginMarketplaceTone;
  screenshots: PluginMarketplaceScreenshotDisplay[];
  releaseNotes: PluginMarketplaceReleaseNoteDisplay[];
  latestReleaseNote: PluginMarketplaceReleaseNoteDisplay | null;
};

type PluginMarketplaceDisplayOptions = {
  locale?: string;
};

export function pluginMarketplaceDisplay(
  input?: PluginDescriptor | PluginMarketplacePlugin | null,
  options: PluginMarketplaceDisplayOptions = {},
): PluginMarketplaceDisplayState {
  const entry = pluginMarketplaceEntry(input);
  const plugin = entry.plugin;
  const marketplace = plugin?.marketplace ?? null;
  const localization = marketplaceLocalization(marketplace?.localizations, options.locale);
  const distribution = pluginMarketplaceDistributionLinks(plugin);
  const compatibility = pluginMarketplaceCompatibilityState(plugin);
  const advisories = pluginMarketplaceAdvisories(plugin);
  const releaseNotes = pluginMarketplaceReleaseNotes(plugin, localization.release_notes);

  return {
    plugin,
    id: firstNonEmpty(plugin?.id),
    name: firstNonEmpty(localization.name, plugin?.name, plugin?.id),
    version: firstNonEmpty(plugin?.version),
    source: firstNonEmpty(plugin?.source, "unknown"),
    installed: entry.installed,
    installedVersion: firstNonEmpty(entry.installedVersion, plugin?.version),
    updateAvailable: entry.updateAvailable,
    summary: firstNonEmpty(localization.summary, marketplace?.summary, plugin?.name, plugin?.id),
    description: firstNonEmpty(localization.description, marketplace?.summary, plugin?.name, plugin?.id),
    categories: pluginMarketplaceCategories(plugin),
    distribution,
    compatibility,
    trust: pluginMarketplaceTrust(plugin, distribution),
    lifecycle: pluginMarketplaceLifecycle(plugin),
    publisher: pluginMarketplacePublisher(plugin),
    advisories,
    highestAdvisoryTone: highestTone(advisories.map((advisory) => advisory.tone)),
    screenshots: pluginMarketplaceScreenshots(plugin),
    releaseNotes,
    latestReleaseNote: latestPluginMarketplaceReleaseNote(releaseNotes, plugin?.version),
  };
}

export function pluginMarketplaceDistributionLinks(plugin?: PluginDescriptor | null): PluginMarketplaceDistributionLinks {
  const distribution = plugin?.distribution;
  const downloadURL = safeURLString(distribution?.download_url);
  const signatureURL = safeURLString(distribution?.signature_url);
  const checksumSHA256 = firstNonEmpty(distribution?.checksum_sha256);

  return {
    marketplaceURL: safeURLString(distribution?.marketplace_url),
    repositoryURL: safeURLString(distribution?.repository_url),
    homepageURL: safeURLString(distribution?.homepage_url),
    downloadURL,
    signatureURL,
    checksumSHA256,
    signatureAlgorithm: firstNonEmpty(distribution?.signature_algorithm, plugin?.trust?.signature_algorithm),
    signatureKeyID: firstNonEmpty(distribution?.signature_key_id, plugin?.trust?.signature_key_id),
    license: firstNonEmpty(distribution?.license),
    packageReady: downloadURL !== "" && checksumSHA256 !== "",
    signatureReady: signatureURL !== "" && firstNonEmpty(distribution?.signature_algorithm, plugin?.trust?.signature_algorithm) !== "",
  };
}

export function pluginMarketplaceCompatibilityState(plugin?: PluginDescriptor | null): PluginMarketplaceCompatibilityState {
  const marketplaceCompatibility = plugin?.marketplace?.compatibility;
  const rawVerdict = firstNonEmpty(plugin?.compatibility?.verdict, marketplaceCompatibility?.verdict, "unknown");
  const verdict = normalizeCompatibilityVerdict(rawVerdict);

  return {
    verdict,
    rawVerdict,
    labelKey: compatibilityLabelKey(verdict),
    tone: compatibilityTone(verdict),
    reasonCode: firstNonEmpty(plugin?.compatibility?.reason_code),
    badges: (marketplaceCompatibility?.badges ?? []).flatMap((badge) => {
      const label = firstNonEmpty(badge.label, badge.id);
      if (label === "") return [];
      return [{
        id: firstNonEmpty(badge.id, label),
        label,
        tone: normalizeTone(badge.tone),
        url: safeURLString(badge.url),
      }];
    }),
  };
}

export function pluginMarketplaceAdvisories(plugin?: PluginDescriptor | null): PluginMarketplaceAdvisoryDisplay[] {
  return (plugin?.marketplace?.advisories ?? []).flatMap((advisory) => {
    const title = firstNonEmpty(advisory.title, advisory.id);
    if (title === "") return [];
    const severity = normalizeAdvisorySeverity(advisory.severity);
    return [{
      id: firstNonEmpty(advisory.id, title),
      severity,
      labelKey: advisorySeverityLabelKey(severity),
      tone: advisorySeverityTone(severity),
      title,
      url: safeURLString(advisory.url),
      publishedAt: firstNonEmpty(advisory.published_at),
      updatedAt: firstNonEmpty(advisory.updated_at),
    }];
  });
}

export function pluginMarketplaceScreenshots(plugin?: PluginDescriptor | null): PluginMarketplaceScreenshotDisplay[] {
  return (plugin?.marketplace?.screenshots ?? []).flatMap((screenshot) => {
    const url = safeURLString(screenshot.url);
    if (url === "") return [];
    return [{
      url,
      thumbnailURL: firstNonEmpty(safeURLString(screenshot.thumbnail_url), url),
      alt: firstNonEmpty(screenshot.alt, screenshot.caption, plugin?.name),
      caption: firstNonEmpty(screenshot.caption),
      locale: firstNonEmpty(screenshot.locale),
      width: positiveNumber(screenshot.width),
      height: positiveNumber(screenshot.height),
    }];
  });
}

export function pluginMarketplacePublisher(plugin?: PluginDescriptor | null): PluginMarketplacePublisherDisplay {
  const publisher = plugin?.marketplace?.publisher;
  const verified = Boolean(publisher?.verified);

  return {
    id: firstNonEmpty(publisher?.id),
    name: firstNonEmpty(publisher?.name, publisher?.id, plugin?.source, "unknown"),
    verified,
    verificationLabelKey: verified ? "已验证" : "未验证",
    url: safeURLString(publisher?.url),
    supportURL: safeURLString(publisher?.support_url),
    contactURL: safeURLString(publisher?.contact_url),
  };
}

export function isSafeMarketplaceURL(value: unknown): boolean {
  return safeURLString(value) !== "";
}

function pluginMarketplaceEntry(input?: PluginDescriptor | PluginMarketplacePlugin | null) {
  if (input && "plugin" in input) {
    return {
      plugin: input.plugin ?? null,
      installed: Boolean(input.installed),
      installedVersion: firstNonEmpty(input.installed_version),
      updateAvailable: Boolean(input.update_available),
    };
  }

  const plugin = input ?? null;
  return {
    plugin,
    installed: Boolean(plugin),
    installedVersion: firstNonEmpty(plugin?.version),
    updateAvailable: false,
  };
}

function pluginMarketplaceTrust(plugin: PluginDescriptor | null, distribution: PluginMarketplaceDistributionLinks) {
  const trust = plugin?.trust;
  const verdict = firstNonEmpty(trust?.verdict, defaultTrustVerdict(plugin, distribution));

  return {
    verdict,
    tone: trustTone(verdict),
    labelKey: trustLabelKey(verdict),
    checksumPresent: Boolean(trust?.checksum_present) || distribution.checksumSHA256 !== "",
    signaturePresent: Boolean(trust?.signature_present) || distribution.signatureURL !== "",
    reasonCode: firstNonEmpty(trust?.reason_code),
  };
}

function pluginMarketplaceLifecycle(plugin?: PluginDescriptor | null) {
  const lifecycle = plugin?.lifecycle;
  const rawStatus = firstNonEmpty(lifecycle?.status, plugin?.status, "enabled");
  const normalized = rawStatus.toLowerCase();
  const restartRequired = Boolean(lifecycle?.restart_required ?? plugin?.restart_required) || normalized === "pending_restart";
  const mandatory = Boolean(lifecycle?.mandatory ?? plugin?.mandatory) || normalized === "mandatory";
  const explicitLoadable = lifecycle?.loadable ?? plugin?.loadable;

  return {
    status: rawStatus,
    labelKey: lifecycleLabelKey(normalized, { mandatory, restartRequired }),
    tone: lifecycleTone(normalized, { mandatory, restartRequired, health: firstNonEmpty(lifecycle?.health, plugin?.health) }),
    restartRequired,
    mandatory,
    loadable: typeof explicitLoadable === "boolean" ? explicitLoadable : normalized === "enabled" || mandatory,
  };
}

function pluginMarketplaceReleaseNotes(plugin?: PluginDescriptor | null, localizedNotes = ""): PluginMarketplaceReleaseNoteDisplay[] {
  const notes = (plugin?.marketplace?.release_notes ?? []).flatMap((releaseNote) => {
    const version = firstNonEmpty(releaseNote.version);
    const title = firstNonEmpty(releaseNote.title, version);
    const body = firstNonEmpty(releaseNote.notes);
    if (version === "" && title === "" && body === "") return [];
    return [{
      version,
      date: firstNonEmpty(releaseNote.date),
      title,
      notes: body,
      url: safeURLString(releaseNote.url),
      items: (releaseNote.items ?? []).map((item) => item.trim()).filter(Boolean),
    }];
  });
  if (notes.length > 0 || localizedNotes === "") return notes;
  return [{
    version: firstNonEmpty(plugin?.version),
    date: "",
    title: firstNonEmpty(plugin?.version),
    notes: localizedNotes,
    url: "",
    items: [],
  }];
}

function latestPluginMarketplaceReleaseNote(releaseNotes: PluginMarketplaceReleaseNoteDisplay[], pluginVersion?: string) {
  if (releaseNotes.length === 0) return null;
  const version = firstNonEmpty(pluginVersion);
  const matchingVersion = releaseNotes.find((releaseNote) => version !== "" && releaseNote.version === version);
  if (matchingVersion) return matchingVersion;
  return releaseNotes
    .map((releaseNote, index) => ({ releaseNote, index, time: parseMarketplaceDate(releaseNote.date) }))
    .sort((left, right) => right.time - left.time || left.index - right.index)[0].releaseNote;
}

function pluginMarketplaceCategories(plugin?: PluginDescriptor | null): string[] {
  return uniqueStrings([...(plugin?.marketplace?.categories ?? []), ...(plugin?.kinds ?? [])]);
}

function marketplaceLocalization(localizations?: Record<string, { name?: string; summary?: string; description?: string; release_notes?: string }> | null, locale = "") {
  if (!localizations) return {};
  const candidates = uniqueStrings([locale, locale.split("-")[0], "zh-CN", "zh", "en"]);
  for (const candidate of candidates) {
    const localization = localizations[candidate];
    if (localization) return localization;
  }
  return Object.values(localizations)[0] ?? {};
}

function normalizeCompatibilityVerdict(verdict: string): PluginMarketplaceCompatibilityVerdict {
  const normalized = verdict.toLowerCase();
  if (normalized === "compatible" || normalized === "needs_review" || normalized === "incompatible") return normalized;
  return "unknown";
}

function compatibilityTone(verdict: PluginMarketplaceCompatibilityVerdict): PluginMarketplaceTone {
  if (verdict === "compatible") return "ok";
  if (verdict === "needs_review") return "warn";
  if (verdict === "incompatible") return "error";
  return "neutral";
}

function compatibilityLabelKey(verdict: PluginMarketplaceCompatibilityVerdict): string {
  switch (verdict) {
    case "compatible":
      return "兼容";
    case "needs_review":
      return "需复核";
    case "incompatible":
      return "不兼容";
    default:
      return "未知";
  }
}

function normalizeAdvisorySeverity(severity?: string): string {
  const normalized = firstNonEmpty(severity, "info").toLowerCase();
  if (normalized === "critical" || normalized === "high" || normalized === "medium" || normalized === "low" || normalized === "info") return normalized;
  return "info";
}

function advisorySeverityTone(severity: string): PluginMarketplaceTone {
  if (severity === "critical" || severity === "high") return "error";
  if (severity === "medium") return "warn";
  return "neutral";
}

function advisorySeverityLabelKey(severity: string): string {
  switch (severity) {
    case "critical":
      return "严重";
    case "high":
      return "高危";
    case "medium":
      return "中危";
    case "low":
      return "低危";
    default:
      return "提示";
  }
}

function trustTone(verdict: string): PluginMarketplaceTone {
  const normalized = verdict.toLowerCase();
  if (normalized === "trusted") return "ok";
  if (normalized === "rejected") return "error";
  if (normalized === "unverified") return "warn";
  return "neutral";
}

function trustLabelKey(verdict: string): string {
  const normalized = verdict.toLowerCase();
  if (normalized === "trusted") return "已信任";
  if (normalized === "rejected") return "已拒绝";
  if (normalized === "unverified") return "未验证";
  return "未知";
}

function defaultTrustVerdict(plugin: PluginDescriptor | null, distribution: PluginMarketplaceDistributionLinks): string {
  if (!plugin) return "unknown";
  if (plugin.source === "built_in") return "trusted";
  if (distribution.signatureReady) return "trusted";
  return "unverified";
}

function lifecycleLabelKey(status: string, flags: { mandatory: boolean; restartRequired: boolean }): string {
  if (flags.mandatory) return "强制启用";
  if (flags.restartRequired) return "待重启";
  if (status === "enabled") return "已启用";
  if (status === "disabled") return "已禁用";
  if (status === "failed_validation") return "校验失败";
  if (status === "rollback_available") return "可回滚";
  return "未知";
}

function lifecycleTone(status: string, flags: { mandatory: boolean; restartRequired: boolean; health: string }): PluginMarketplaceTone {
  if (status === "failed_validation" || flags.health === "unhealthy") return "error";
  if (flags.restartRequired || status === "rollback_available") return "warn";
  if (flags.mandatory || status === "enabled" || flags.health === "healthy") return "ok";
  if (status === "disabled") return "error";
  return "neutral";
}

function normalizeTone(tone?: string): PluginMarketplaceTone {
  const normalized = firstNonEmpty(tone).toLowerCase();
  if (normalized === "ok" || normalized === "warn" || normalized === "error" || normalized === "neutral") return normalized;
  return "neutral";
}

function highestTone(tones: PluginMarketplaceTone[]): PluginMarketplaceTone {
  if (tones.includes("error")) return "error";
  if (tones.includes("warn")) return "warn";
  if (tones.includes("ok")) return "ok";
  return "neutral";
}

function safeURLString(value: unknown): string {
  if (typeof value !== "string" || value.trim() === "") return "";
  try {
    const url = new URL(value.trim());
    if (url.protocol !== "https:" && url.protocol !== "http:") return "";
    return url.href;
  } catch {
    return "";
  }
}

function isFinitePositive(value: number | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0;
}

function positiveNumber(value: number | undefined): number | undefined {
  return isFinitePositive(value) ? value : undefined;
}

function parseMarketplaceDate(value: string): number {
  if (value === "") return 0;
  const time = Date.parse(value);
  return Number.isFinite(time) ? time : 0;
}

function uniqueStrings(values: Array<string | undefined | null>): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const normalized = firstNonEmpty(value);
    if (normalized === "" || seen.has(normalized)) continue;
    seen.add(normalized);
    result.push(normalized);
  }
  return result;
}

function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim() !== "") return value.trim();
  }
  return "";
}
