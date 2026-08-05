import { Check, ChevronDown, Globe2 } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
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
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ left: 0, top: 0 });
  const [menuTheme, setMenuTheme] = useState<"light" | "dark">("light");
  const menuID = useId();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const optionsRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const current = languageOptions.find((option) => option.value === language) ?? languageOptions[0];

  useEffect(() => {
    if (!open) return;
    const closeMenu = () => setOpen(false);
    const closeOutside = (event: MouseEvent | FocusEvent) => {
      const target = event.target as Node;
      if (!containerRef.current?.contains(target) && !optionsRef.current?.contains(target)) closeMenu();
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      closeMenu();
      triggerRef.current?.focus();
    };
    const focusFrame = window.requestAnimationFrame(() => {
      optionsRef.current?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus();
    });
    window.addEventListener("mousedown", closeOutside);
    window.addEventListener("focusin", closeOutside);
    window.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", closeMenu);
    window.addEventListener("scroll", closeMenu, true);
    return () => {
      window.cancelAnimationFrame(focusFrame);
      window.removeEventListener("mousedown", closeOutside);
      window.removeEventListener("focusin", closeOutside);
      window.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", closeMenu);
      window.removeEventListener("scroll", closeMenu, true);
    };
  }, [open]);

  function toggleMenu() {
    if (!open && triggerRef.current) {
      const rect = triggerRef.current.getBoundingClientRect();
      const menuWidth = 176;
      const menuHeight = 132;
      const openBelow = window.innerHeight - rect.bottom >= menuHeight + 12;
      setPosition({
        left: Math.min(Math.max(8, rect.right - menuWidth), window.innerWidth - menuWidth - 8),
        top: openBelow ? rect.bottom + 6 : Math.max(8, rect.top - menuHeight - 6),
      });
      setMenuTheme(triggerRef.current.closest('[data-theme="dark"]') ? "dark" : "light");
    }
    setOpen((currentOpen) => !currentOpen);
  }

  function selectLanguage(nextLanguage: AppLanguage) {
    onChange(nextLanguage);
    setOpen(false);
    triggerRef.current?.focus();
  }

  function moveOptionFocus(event: React.KeyboardEvent<HTMLButtonElement>) {
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const options = Array.from(optionsRef.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? []);
    const currentIndex = options.indexOf(event.currentTarget);
    if (event.key === "Home") options[0]?.focus();
    else if (event.key === "End") options.at(-1)?.focus();
    else if (event.key === "ArrowDown") options[(currentIndex + 1) % options.length]?.focus();
    else options[(currentIndex - 1 + options.length) % options.length]?.focus();
  }

  return (
    <div className={className ? `language-select ${className}` : "language-select"} ref={containerRef}>
      <button
        aria-controls={open ? menuID : undefined}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={tx("界面语言")}
        className="language-select-trigger"
        onClick={toggleMenu}
        ref={triggerRef}
        title={tx("界面语言")}
        type="button"
      >
        <Globe2 aria-hidden="true" size={16} />
        <span className="language-select-label">{current.nativeLabel}</span>
        <ChevronDown aria-hidden="true" className="language-select-chevron" size={14} />
      </button>
      {open && typeof document !== "undefined" ? createPortal(
        <div
          aria-label={tx("界面语言")}
          className="language-select-options"
          data-theme={menuTheme}
          id={menuID}
          ref={optionsRef}
          role="listbox"
          style={position}
        >
          {languageOptions.map((option) => {
            const selected = language === option.value;
            return (
              <button
                aria-selected={selected}
                className={selected ? "language-select-option selected" : "language-select-option"}
                key={option.value}
                onClick={() => selectLanguage(option.value)}
                onKeyDown={moveOptionFocus}
                role="option"
                type="button"
              >
                <span>{option.nativeLabel}</span>
                {selected ? <Check aria-hidden="true" size={15} /> : null}
              </button>
            );
          })}
        </div>,
        document.body,
      ) : null}
    </div>
  );
}

export function languageOptionLabel(option: { label: string; nativeLabel: string }, language: AppLanguage) {
  return language === "en" ? option.label : option.nativeLabel;
}
