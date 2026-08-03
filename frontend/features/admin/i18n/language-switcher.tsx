import { Globe2 } from "lucide-react";
import { type AppLanguage, languageOptions, tx } from "./runtime";

export function LanguageSwitcher({
  language,
  onChange,
  className,
}: {
  language: AppLanguage;
  onChange: (language: AppLanguage) => void;
  className?: string;
}) {
  return (
    <div className={className ? `language-switcher ${className}` : "language-switcher"} role="radiogroup" aria-label={tx("界面语言")}>
      {languageOptions.map((option) => (
        <button
          aria-checked={language === option.value}
          className={language === option.value ? "active" : ""}
          key={option.value}
          onClick={() => onChange(option.value)}
          role="radio"
          type="button"
        >
          <span>{languageOptionLabel(option, language)}</span>
        </button>
      ))}
    </div>
  );
}

export function LanguageSelect({
  language,
  onChange,
  className,
}: {
  language: AppLanguage;
  onChange: (language: AppLanguage) => void;
  className?: string;
}) {
  return (
    <label className={className ? `language-select ${className}` : "language-select"} title={tx("界面语言")}>
      <Globe2 aria-hidden="true" size={16} />
      <select
        aria-label={tx("界面语言")}
        onChange={(event) => onChange(event.target.value as AppLanguage)}
        value={language}
      >
        {languageOptions.map((option) => (
          <option key={option.value} value={option.value}>{option.nativeLabel}</option>
        ))}
      </select>
    </label>
  );
}

export function languageOptionLabel(option: { label: string; nativeLabel: string }, language: AppLanguage) {
  return language === "en" ? option.label : option.nativeLabel;
}
