import { enTranslations } from "./en";
import { jaTranslations } from "./ja";

export const translations = { en: enTranslations, ja: jaTranslations } satisfies Record<"en" | "ja", Record<string, string>>;
