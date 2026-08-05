export type AppLanguage = "zh-CN" | "en" | "ja";

export const languageOptions: Array<{ value: AppLanguage; label: string; nativeLabel: string }> = [
  { value: "zh-CN", label: "Chinese", nativeLabel: "简体中文" },
  { value: "en", label: "English", nativeLabel: "English" },
  { value: "ja", label: "Japanese", nativeLabel: "日本語" },
];

export function languageFromLocales(locales: readonly string[]): AppLanguage {
  for (const locale of locales) {
    const normalized = locale.trim().toLowerCase();
    if (normalized === "zh" || normalized.startsWith("zh-")) return "zh-CN";
    if (normalized === "ja" || normalized.startsWith("ja-")) return "ja";
    if (normalized === "en" || normalized.startsWith("en-")) return "en";
  }
  return "en";
}

export function preferredLanguage(saved: string | null, locales: readonly string[]): AppLanguage {
  if (saved === "en" || saved === "ja" || saved === "zh-CN") return saved;
  return languageFromLocales(locales);
}
