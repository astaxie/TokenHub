import { RotateCcw, Save } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { isSafePluginCSSValue, pluginThemeTokenEntries } from "../domain/plugin-theme";
import { type PluginThemeOverrides } from "../domain/plugin-theme-overrides";
import { localizedCapabilityTitle } from "../domain/plugin-localization";
import { type SIMRegistry, type SIMThemeTokens } from "../domain/sim-registry";
import { languageLocale, tx } from "../i18n/runtime";

const emptyTokenOverrides: Record<string, string> = {};

export function PluginTemplateSettings({
  activeThemeKey,
  overrides,
  pluginID,
  registry,
  themeMode = "light",
  onThemeTokenOverridesChange,
}: {
  activeThemeKey?: string;
  overrides: PluginThemeOverrides;
  pluginID: string;
  registry: SIMRegistry;
  themeMode?: "light" | "dark";
  onThemeTokenOverridesChange?: (themeKey: string, values: Record<string, string>) => void;
}) {
  const themes = useMemo(
    () => registry.themeTokens.filter((theme) => theme.pluginID === pluginID),
    [pluginID, registry.themeTokens],
  );
  const [selectedKey, setSelectedKey] = useState("");
  const activeTheme = themes.find((theme) => theme.key === activeThemeKey);
  const activeMode = activeTheme?.payload.mode ?? themeMode;
  const preferred = activeTheme
    ?? themes.find((theme) => theme.payload.mode === activeMode)
    ?? themes.find((theme) => theme.payload.default);
  const selected = themes.find((theme) => theme.key === selectedKey) ?? preferred ?? themes[0];

  useEffect(() => {
    if (selectedKey && themes.some((theme) => theme.key === selectedKey)) return;
    setSelectedKey(preferred?.key ?? themes[0]?.key ?? "");
  }, [preferred?.key, selectedKey, themes]);

  if (!selected) return null;
  return (
    <div className="plugin-settings-page">
      {themes.length > 1 ? (
        <div className="plugin-settings-tabs" role="tablist" aria-label={tx("主题方案")}>
          {themes.map((theme) => (
            <button
              aria-selected={theme.key === selected.key}
              className={theme.key === selected.key ? "active" : ""}
              key={theme.key}
              onClick={() => setSelectedKey(theme.key)}
              role="tab"
              type="button"
            >
              {localizedCapabilityTitle(theme, languageLocale())}
            </button>
          ))}
        </div>
      ) : null}
      <ThemeSettings
        key={selected.key}
        capability={selected}
        overrides={overrides[selected.key] ?? emptyTokenOverrides}
        onChange={onThemeTokenOverridesChange}
      />
    </div>
  );
}

function ThemeSettings({
  capability,
  overrides,
  onChange,
}: {
  capability: SIMThemeTokens;
  overrides: Record<string, string>;
  onChange?: (themeKey: string, values: Record<string, string>) => void;
}) {
  const [draft, setDraft] = useState<Record<string, string>>(overrides);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  // Only allowlisted token names survive `normalizePluginThemeOverrides`, so anything
  // else offered here would be accepted by the editor and then silently dropped. The
  // shell resolves the same declarations through `pluginThemeTokenEntries`, so the
  // editor shows the default the shell actually applies.
  const editableTokens = useMemo(() => pluginThemeTokenEntries(capability.payload.tokens), [capability.payload.tokens]);
  const groups = tokenGroups(editableTokens);

  function apply() {
    const values = Object.fromEntries(editableTokens.flatMap(({ name }) => {
      const value = draft[name]?.trim() ?? "";
      return value ? [[name, value]] : [];
    }));
    if (Object.values(values).some((value) => !isSafePluginCSSValue(value))) {
      setNotice("");
      setError(tx("样式值不安全"));
      return;
    }
    onChange?.(capability.key, values);
    setError("");
    setNotice(tx("设置已保存"));
  }

  function restore() {
    setDraft({});
    setError("");
    setNotice(tx("已恢复默认设置"));
    onChange?.(capability.key, {});
  }

  return (
    <div className="plugin-settings-form">
      <p className="plugin-theme-token-hint">
        <span>{tx("只能调整模板已经声明的安全主题 Token。")}</span>
        <span>{tx("调整保存在当前浏览器。")}</span>
      </p>
      {groups.map((group) => (
        <section className="plugin-settings-group" key={group.name}>
          <h2>{groupTitle(group.name)}</h2>
          <div className="plugin-settings-list">
            {group.tokens.map(({ name, defaultValue }) => {
              const value = draft[name] ?? defaultValue;
              const pickerValue = colorPickerValue(value);
              const label = tokenLabel(name);
              return (
                <label className="plugin-setting-row" key={name}>
                  <span className="plugin-setting-copy">
                    <strong>{label}</strong>
                    <small>{tokenDescription(name)}</small>
                  </span>
                  <span className="plugin-setting-control">
                    {pickerValue ? (
                      <input
                        aria-label={`${label} ${tx("颜色")}`}
                        className="plugin-theme-color-input"
                        type="color"
                        value={pickerValue}
                        onChange={(event) => setDraft((current) => ({ ...current, [name]: event.target.value }))}
                      />
                    ) : null}
                    <input
                      aria-label={`${label} ${tx("当前值")}`}
                      type="text"
                      value={value}
                      onChange={(event) => setDraft((current) => ({ ...current, [name]: event.target.value }))}
                    />
                  </span>
                </label>
              );
            })}
          </div>
        </section>
      ))}
      {editableTokens.length > 0 ? (
        <>
          {error ? <div className="inline-error" role="alert">{error}</div> : null}
          {notice ? <p className="plugin-settings-notice" role="status">{notice}</p> : null}
          <div className="plugin-settings-actions">
            <button className="compact-button secondary" type="button" onClick={restore}><RotateCcw size={14} aria-hidden="true" />{tx("恢复默认")}</button>
            <button className="compact-button" type="button" onClick={apply}><Save size={14} aria-hidden="true" />{tx("保存设置")}</button>
          </div>
        </>
      ) : null}
    </div>
  );
}

type TokenGroupName = "background" | "text" | "accent" | "status" | "border" | "other";

function tokenGroups(tokens: Array<{ name: string; defaultValue: string }>) {
  const groups = new Map<TokenGroupName, Array<{ name: string; defaultValue: string }>>();
  for (const entry of tokens) {
    const group = tokenGroup(entry.name);
    groups.set(group, [...(groups.get(group) ?? []), entry]);
  }
  const order: TokenGroupName[] = ["background", "text", "accent", "status", "border", "other"];
  return order.flatMap((name) => {
    const entries = groups.get(name);
    return entries?.length ? [{ name, tokens: entries }] : [];
  });
}

function tokenGroup(name: string): TokenGroupName {
  if (name === "bg" || name.startsWith("surface") || name === "page" || name === "shell") return "background";
  if (name === "ink" || name.startsWith("ink-") || name === "text" || name.startsWith("muted")) return "text";
  if (name === "accent" || name.startsWith("accent-") || name === "blue") return "accent";
  if (name === "pos" || name.startsWith("pos-") || name === "warn" || name.startsWith("warn-") || name === "red" || name === "green" || name === "amber") return "status";
  if (name.startsWith("border") || name.startsWith("shadow") || name === "chart-grid") return "border";
  return "other";
}

function groupTitle(group: TokenGroupName) {
  if (group === "background") return tx("背景与面板");
  if (group === "text") return tx("文字");
  if (group === "accent") return tx("品牌与强调");
  if (group === "status") return tx("状态颜色");
  if (group === "border") return tx("边框与阴影");
  return tx("其他外观");
}

function tokenLabel(name: string) {
  const labels: Record<string, string> = {
    bg: tx("页面背景"), page: tx("页面背景"), shell: tx("应用背景"), surface: tx("主要面板"), "surface-2": tx("次要面板"), "surface-3": tx("强调面板"), "surface-soft": tx("柔和面板"),
    ink: tx("主要文字"), text: tx("主要文字"), "ink-2": tx("次要文字"), "ink-3": tx("辅助文字"), "ink-4": tx("弱化文字"), muted: tx("辅助文字"), "muted-strong": tx("次要文字"),
    accent: tx("主题色"), "accent-2": tx("辅助主题色"), "accent-weak": tx("主题浅色背景"), "accent-weak-2": tx("主题淡色背景"), "accent-ink": tx("主题色上的文字"), blue: tx("蓝色"),
    pos: tx("成功状态"), "pos-weak": tx("成功状态背景"), green: tx("绿色"), warn: tx("警告状态"), "warn-weak": tx("警告状态背景"), amber: tx("琥珀色"), red: tx("错误状态"),
    border: tx("普通边框"), "border-2": tx("清晰边框"), "border-strong": tx("强调边框"), "chart-grid": tx("图表网格"), shadow: tx("面板阴影"), "shadow-sm": tx("轻微阴影"), "shadow-md": tx("普通阴影"), "shadow-lg": tx("明显阴影"),
  };
  return labels[name] ?? name;
}

function tokenDescription(name: string) {
  if (name === "bg" || name === "page") return tx("用于页面最底层的背景。");
  if (name === "shell") return tx("用于应用主框架的背景。");
  if (name.startsWith("surface")) return tx("用于页面中的面板和内容区域。");
  if (name === "ink" || name === "text") return tx("用于标题和主要内容。");
  if (name.startsWith("ink-") || name.startsWith("muted")) return tx("用于说明、提示和弱化信息。");
  if (name === "accent") return tx("用于按钮、选中项和重点信息。");
  if (name.startsWith("accent-")) return tx("用于主题色相关的文字和浅色背景。");
  if (name === "pos" || name === "green" || name.startsWith("pos-")) return tx("用于成功和正常状态。");
  if (name === "warn" || name === "amber" || name.startsWith("warn-")) return tx("用于需要注意的状态。");
  if (name === "red") return tx("用于错误和危险操作。");
  if (name.startsWith("border")) return tx("用于分隔内容和突出控件边界。");
  if (name.startsWith("shadow")) return tx("用于区分浮层和页面层级。");
  if (name === "chart-grid") return tx("用于图表中的辅助网格线。");
  return tx("调整这个插件的界面外观。");
}

function colorPickerValue(value: string) {
  const normalized = value.trim();
  if (/^#[0-9a-f]{6}$/i.test(normalized)) return normalized;
  if (/^#[0-9a-f]{3}$/i.test(normalized)) {
    return `#${normalized.slice(1).split("").map((part) => `${part}${part}`).join("")}`;
  }
  return "";
}
