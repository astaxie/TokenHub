import { Braces, CreditCard, LayoutDashboard, Menu, PanelTop, RotateCcw, Save, Search, SlidersHorizontal, SquareDashed, UserRound } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { type AdminUIContribution } from "../core/types";
import { isSafePluginCSSValue } from "../domain/plugin-theme";
import { type PluginThemeOverrides } from "../domain/plugin-theme-overrides";
import { localizedCapabilityTitle, localizedContributionTitle } from "../domain/plugin-localization";
import { type SIMRegistry, type SIMThemeTokens } from "../domain/sim-registry";
import { simTemplateStructure, type SIMTemplateBlock, type SIMTemplateBlockKind } from "../domain/sim-template-structure";
import { languageLocale, tx } from "../i18n/runtime";

const emptyTokenOverrides: Record<string, string> = {};

export function PluginTemplateSettings({
  activeThemeKey,
  contributions,
  overrides,
  pluginID,
  registry,
  onThemeTokenOverridesChange,
}: {
  activeThemeKey?: string;
  contributions: readonly AdminUIContribution[];
  overrides: PluginThemeOverrides;
  pluginID: string;
  registry: SIMRegistry;
  onThemeTokenOverridesChange?: (themeKey: string, values: Record<string, string>) => void;
}) {
  const blocks = useMemo(
    () => simTemplateStructure(pluginID, registry, contributions),
    [contributions, pluginID, registry],
  );
  const [selectedKey, setSelectedKey] = useState("");
  const preferred = blocks.find((block) => block.sourceCapability?.key === activeThemeKey);
  const selected = blocks.find((block) => block.key === selectedKey) ?? preferred ?? blocks[0];

  useEffect(() => {
    if (selectedKey && blocks.some((block) => block.key === selectedKey)) return;
    setSelectedKey(preferred?.key ?? blocks[0]?.key ?? "");
  }, [blocks, preferred?.key, selectedKey]);

  if (blocks.length === 0) return null;
  return (
    <section className="plugin-detail-section">
      <h2 className="plugin-detail-section-title"><SquareDashed size={17} aria-hidden="true" />{tx("模板结构")}</h2>
      <div className="plugin-template-editor">
        <aside className="plugin-template-block-list" aria-label={tx("模板块")}>
          <div className="plugin-template-block-list-heading">
            <strong>{tx("模板块")}</strong><span>{blocks.length}</span>
          </div>
          {blocks.map((block) => (
            <button
              className={block.key === selected?.key ? "plugin-template-block-button active" : "plugin-template-block-button"}
              key={block.key}
              type="button"
              onClick={() => setSelectedKey(block.key)}
            >
              {blockIcon(block.kind)}
              <span><strong>{blockTitle(block)}</strong><small>{block.placement}</small></span>
            </button>
          ))}
        </aside>
        {selected ? (
          <div className="plugin-template-block-detail" aria-live="polite">
            <header>
              <div>{blockIcon(selected.kind)}<span><small>{blockKindLabel(selected.kind)}</small><strong>{blockTitle(selected)}</strong></span></div>
              <code>{selected.placement}</code>
            </header>
            <dl>
              <div><dt>{tx("位置")}</dt><dd>{selected.placement}</dd></div>
              <div><dt>{tx("来源能力")}</dt><dd>{selected.sourceCapability?.name ?? tx("界面贡献")}</dd></div>
            </dl>
            {selected.sourceCapability?.name === "theme_tokens" ? (
              <ThemeTokenEditor
                key={selected.sourceCapability.key}
                capability={selected.sourceCapability}
                overrides={overrides[selected.sourceCapability.key] ?? emptyTokenOverrides}
                onChange={onThemeTokenOverridesChange}
              />
            ) : null}
            <div className="plugin-template-declaration">
              <strong><Braces size={14} aria-hidden="true" />{tx("声明内容")}</strong>
              <pre><code>{JSON.stringify(selected.details, null, 2)}</code></pre>
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function ThemeTokenEditor({
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

  function apply() {
    const values = Object.fromEntries(Object.entries(draft).flatMap(([name, rawValue]) => {
      const value = rawValue.trim();
      return value ? [[name, value]] : [];
    }));
    if (Object.values(values).some((value) => !isSafePluginCSSValue(value))) {
      setNotice("");
      setError(tx("样式值不安全"));
      return;
    }
    onChange?.(capability.key, values);
    setError("");
    setNotice(tx("样式调整已应用"));
  }

  function restore() {
    setDraft({});
    setError("");
    setNotice(tx("已恢复模板默认值"));
    onChange?.(capability.key, {});
  }

  return (
    <div className="plugin-theme-token-editor">
      <div className="plugin-theme-token-heading">
        <div><SlidersHorizontal size={15} aria-hidden="true" /><span><strong>{tx("样式调整")}</strong><small>{localizedCapabilityTitle(capability, languageLocale())}</small></span></div>
        <span>{capability.payload.mode}</span>
      </div>
      <div className="plugin-theme-token-list">
        {Object.entries(capability.payload.tokens).map(([rawName, defaultValue]) => {
          const name = rawName.replace(/^--/, "");
          const value = draft[name] ?? defaultValue;
          const pickerValue = colorPickerValue(value);
          return (
            <label className="plugin-theme-token-row" key={name}>
              <span><strong>--{name}</strong><small>{tx("默认值")}: {defaultValue}</small></span>
              <span className="plugin-theme-token-control">
                {pickerValue ? (
                  <input
                    aria-label={`${name} ${tx("颜色")}`}
                    className="plugin-theme-color-input"
                    type="color"
                    value={pickerValue}
                    onChange={(event) => setDraft((current) => ({ ...current, [name]: event.target.value }))}
                  />
                ) : null}
                <input
                  aria-label={`${name} ${tx("当前值")}`}
                  type="text"
                  value={value}
                  onChange={(event) => setDraft((current) => ({ ...current, [name]: event.target.value }))}
                />
              </span>
            </label>
          );
        })}
      </div>
      {error ? <div className="inline-error" role="alert">{error}</div> : null}
      {notice ? <p className="plugin-theme-token-notice" role="status">{notice}</p> : null}
      <div className="plugin-theme-token-actions">
        <button className="compact-button secondary" type="button" onClick={restore}><RotateCcw size={14} aria-hidden="true" />{tx("恢复默认")}</button>
        <button className="compact-button" type="button" onClick={apply}><Save size={14} aria-hidden="true" />{tx("应用调整")}</button>
      </div>
    </div>
  );
}

function blockTitle(block: SIMTemplateBlock) {
  if (block.contribution) return localizedContributionTitle(block.contribution, languageLocale());
  if (["theme", "page_template", "dashboard"].includes(block.kind)) return localizedCapabilityTitle(block.sourceCapability, languageLocale());
  if (["page_region", "dashboard_card"].includes(block.kind)) return block.title;
  return blockKindLabel(block.kind);
}

function blockKindLabel(kind: SIMTemplateBlockKind) {
  const labels: Record<SIMTemplateBlockKind, string> = {
    theme: tx("主题方案"),
    navigation: tx("导航栏"),
    topbar: tx("顶部栏"),
    global_search: tx("全局搜索"),
    account_area: tx("账号区"),
    content: tx("内容区"),
    page_template: tx("页面模板"),
    page_region: tx("页面区域"),
    dashboard: tx("仪表盘"),
    dashboard_card: tx("仪表盘卡片"),
    ui_contribution: tx("界面贡献块"),
  };
  return labels[kind];
}

function blockIcon(kind: SIMTemplateBlockKind): ReactNode {
  const props = { size: 15, "aria-hidden": true } as const;
  if (kind === "navigation") return <Menu {...props} />;
  if (kind === "topbar") return <PanelTop {...props} />;
  if (kind === "global_search") return <Search {...props} />;
  if (kind === "account_area") return <UserRound {...props} />;
  if (kind === "dashboard_card") return <CreditCard {...props} />;
  if (kind === "dashboard") return <LayoutDashboard {...props} />;
  if (kind === "theme") return <SlidersHorizontal {...props} />;
  return <SquareDashed {...props} />;
}

function colorPickerValue(value: string) {
  const normalized = value.trim();
  if (/^#[0-9a-f]{6}$/i.test(normalized)) return normalized;
  if (/^#[0-9a-f]{3}$/i.test(normalized)) {
    return `#${normalized.slice(1).split("").map((part) => `${part}${part}`).join("")}`;
  }
  return "";
}
