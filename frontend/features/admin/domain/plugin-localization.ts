export type PluginLocalizationRecord = Record<string, {
  name?: unknown;
  title?: unknown;
  summary?: unknown;
  description?: unknown;
  release_notes?: unknown;
}>;

type LocalizablePlugin = {
  id?: unknown;
  name?: unknown;
  localizations?: unknown;
  marketplace?: {
    localizations?: unknown;
  } | null;
};

type LocalizableContribution = {
  id?: unknown;
  title?: unknown;
  localizations?: unknown;
  metadata?: Record<string, string>;
  schema?: Record<string, unknown>;
};

type LocalizableCapability = {
  id?: unknown;
  title?: unknown;
  payload?: Record<string, unknown>;
};

export function localizedPluginName(plugin: LocalizablePlugin | null | undefined, locale: string) {
  return firstNonEmpty(
    localizedFields(plugin?.localizations, locale, ["name", "title"]),
    localizedFields(plugin?.marketplace?.localizations, locale, ["name", "title"]),
    stringValue(plugin?.name),
    stringValue(plugin?.id),
  );
}

export function localizedContributionTitle(contribution: LocalizableContribution | null | undefined, locale: string) {
  return firstNonEmpty(
    localizedFields(contribution?.localizations, locale, ["title", "name"]),
    localizedFields(contribution?.schema?.localizations, locale, ["title", "name"]),
    metadataLocalizedFields(contribution?.metadata, locale, ["title", "name"]),
    stringValue(contribution?.title),
    stringValue(contribution?.id),
  );
}

export function localizedCapabilityTitle(capability: LocalizableCapability | null | undefined, locale: string) {
  return firstNonEmpty(
    localizedFields(capability?.payload?.localizations, locale, ["title", "name"]),
    localizedFields(capability?.payload?.localization, locale, ["title", "name"]),
    stringValue(capability?.title),
    stringValue(capability?.id),
  );
}

function localizedFields(source: unknown, locale: string, fields: string[]) {
  const localizations = localizationRecord(source);
  if (!localizations) return "";
  const byLocale = new Map(Object.entries(localizations).map(([key, value]) => [normalizeLocale(key), value]));
  for (const candidate of localeCandidates(locale)) {
    const localization = byLocale.get(normalizeLocale(candidate));
    if (!localization) continue;
    for (const field of fields) {
      const value = stringValue(localization[field as keyof typeof localization]);
      if (value) return value;
    }
  }
  return "";
}

function metadataLocalizedFields(metadata: Record<string, string> | undefined, locale: string, fields: string[]) {
  if (!metadata) return "";
  const normalized = new Map(Object.entries(metadata).map(([key, value]) => [normalizeMetadataKey(key), value]));
  for (const candidate of localeCandidates(locale)) {
    const localeKey = normalizeLocale(candidate);
    for (const field of fields) {
      const keys = [
        `${field}:${localeKey}`,
        `${field}.${localeKey}`,
        `${field}_${localeKey}`,
      ];
      for (const key of keys) {
        const value = stringValue(normalized.get(normalizeMetadataKey(key)));
        if (value) return value;
      }
    }
  }
  return "";
}

function localizationRecord(source: unknown): PluginLocalizationRecord | null {
  if (!source || typeof source !== "object" || Array.isArray(source)) return null;
  const result: PluginLocalizationRecord = {};
  for (const [locale, value] of Object.entries(source)) {
    if (!value || typeof value !== "object" || Array.isArray(value)) continue;
    result[locale] = value as PluginLocalizationRecord[string];
  }
  return Object.keys(result).length > 0 ? result : null;
}

function localeCandidates(locale: string) {
  const normalized = normalizeLocale(locale);
  const language = normalized.split("-")[0] || normalized;
  const candidates = [normalized, language];
  if (language === "zh") candidates.push("zh-cn", "zh-hans", "zh-hans-cn");
  if (language === "en") candidates.push("en-us", "en-gb");
  if (language === "ja") candidates.push("ja-jp");
  if (language !== "en") candidates.push("en-us", "en");
  return unique(candidates);
}

function normalizeLocale(value: string) {
  return value.trim().replaceAll("_", "-").toLowerCase();
}

function normalizeMetadataKey(value: string) {
  return value.trim().replaceAll("_", "-").toLowerCase();
}

function firstNonEmpty(...values: string[]) {
  return values.find((value) => value.trim() !== "") ?? "";
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function unique(values: string[]) {
  const seen = new Set<string>();
  return values.filter((value) => {
    if (!value || seen.has(value)) return false;
    seen.add(value);
    return true;
  });
}
