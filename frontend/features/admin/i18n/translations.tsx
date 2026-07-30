import { enTranslations } from "./en";
import { jaTranslations } from "./ja";
import { routingTranslations } from "./routing";

export const translations: Record<"en" | "ja", Record<string, string>> = {
  en: { ...enTranslations, ...routingTranslations.en },
  ja: { ...jaTranslations, ...routingTranslations.ja },
};
