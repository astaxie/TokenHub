import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { type AdminUIContribution } from "../core/types";
import { emptyData } from "../domain/catalog";
import { PluginOverview } from "./plugin-detail-overview";
import { PluginPageView } from "../views/admin-ui-plugin-pages";
import { ProviderPluginFormSections } from "../views/provider-plugin-form-sections";
import { localizeBuiltinContribution } from "../i18n/builtin-admin-ui";
import { type AppLanguage, setActiveLanguage } from "../i18n/runtime";

const contribution: AdminUIContribution = {
  plugin_id: "tokenhub.admin.plugin-ecosystem", id: "ecosystem-page", slot: "nav.section", title: "Plugin Ecosystem",
  schema: { description: "Inspect the plugin registry, Admin UI contributions, gateway hooks, and plugin actions.", fields: [
    { name: "plugins", type: "metric", label: "Registered plugins", source: "plugins.length" },
  ] },
};

describe("Built-in Admin UI localization", () => {
  it("updates a mounted plugin page when switching languages", () => {
    const data = emptyData();
    data.pluginUI = [contribution];
    const props = { activePageKey: "tokenhub.admin.plugin-ecosystem:ecosystem-page", api: { baseURL: "", adminToken: "" }, data, onSelectPage: vi.fn() };
    const view = render(<PluginPageView {...props} />);
    for (const [language, title, label] of [
      ["zh-CN", "插件生态", "已注册插件"], ["en", "Plugin Ecosystem", "Registered plugins"],
      ["ja", "プラグインエコシステム", "登録済みプラグイン"], ["zh-CN", "插件生态", "已注册插件"],
    ]) {
      setActiveLanguage(language as AppLanguage);
      view.rerender(<PluginPageView {...props} />);
      expect(screen.getByRole("heading", { name: title })).toBeInTheDocument();
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(data.pluginUI[0]).toBe(contribution);
    expect(contribution.title).toBe("Plugin Ecosystem");
  });

  it("localizes built-in contribution titles in plugin details and honors explicit translations", () => {
    const plugin = { id: contribution.plugin_id, name: "TokenHub Plugin Ecosystem Dashboard", version: "built-in", source: "built_in", kinds: ["admin_ui"], placements: ["presentation"], capabilities: [] };
    const props = { plugin, contributions: [contribution], actions: [], hooks: [], jobs: [] };
    const view = render(<PluginOverview {...props} />);
    expect(screen.getByText("插件生态", { exact: true })).toBeInTheDocument();
    view.rerender(<PluginOverview {...props} contributions={[{ ...contribution, localizations: { "zh-CN": { title: "自定义插件入口" } } }]} />);
    expect(screen.getByText("自定义插件入口", { exact: true })).toBeInTheDocument();
  });

  it("refreshes memoized provider fields without changing option values", () => {
    const contributions: AdminUIContribution[] = [{
      plugin_id: "tokenhub.admin.core-provider", id: "provider-advanced-settings", slot: "provider.form.section",
      title: "Provider advanced settings", schema: { placement: "advanced", fields: [{
        name: "system_prompt_transform_policy", type: "select", target: "provider", label: "System prompt transform", options: ["preserve", "strip"],
      }] },
    }];
    const props = { contributions, placement: "advanced", values: { system_prompt_transform_policy: "preserve" }, onUpdate: vi.fn() };
    const view = render(<ProviderPluginFormSections {...props} />);
    for (const [language, label] of [["zh-CN", "系统提示词转换"], ["en", "System prompt transform"], ["ja", "システムプロンプト変換"]]) {
      setActiveLanguage(language as AppLanguage);
      view.rerender(<ProviderPluginFormSections {...props} />);
      expect(screen.getByLabelText(label)).toHaveValue("preserve");
    }
  });

  it("preserves third-party metadata, unknown prose, identifiers, and values", () => {
    const thirdParty = { ...contribution, plugin_id: "example.custom" };
    expect(localizeBuiltinContribution(thirdParty)).toBe(thirdParty);
    const input = { ...contribution, title: "constructor", schema: { fields: [
      { name: "Plugin Ecosystem", label: "Custom label", help: "Custom help", value: "Plugin Ecosystem", options: ["strip"] },
    ] } };
    expect(localizeBuiltinContribution(input)).toEqual(input);
  });

  it("covers all presentation strings registered by the built-in Admin UI provider", () => {
    const source = readFileSync(resolve(process.cwd(), "../backend/internal/server/builtin_admin_ui_plugins.go"), "utf8");
    const strings = [...source.matchAll(/(?:Title:|"(?:description|label|help)":)\s*"([^"\n]+)"/g)].map((match) => match[1]);
    expect(strings.length).toBeGreaterThan(25);
    for (const language of ["zh-CN", "ja"] as const) {
      setActiveLanguage(language);
      for (const title of strings) {
        expect(localizeBuiltinContribution({ ...contribution, title }).title, `${language}: ${title}`).not.toBe(title);
      }
    }
    setActiveLanguage("en");
    for (const title of strings) expect(localizeBuiltinContribution({ ...contribution, title }).title).toBe(title);
  });
});
