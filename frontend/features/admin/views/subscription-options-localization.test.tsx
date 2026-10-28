import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AdminUIContribution } from "../core/types";
import { providerPluginOptionFieldKey } from "../domain/provider-plugin-options";
import { localizeBuiltinContribution } from "../i18n/builtin-admin-ui";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderPluginFormSections } from "./provider-plugin-form-sections";
import { providerQuotaPanelTitle } from "./provider-plugin-panels";

const option: AdminUIContribution = { plugin_id: "tokenhub.provider.factory-subscription", id: "subscription-options", slot: "provider.form.section", title: "Subscription account options", provider_types: ["factory_subscription"], schema: { placement: "advanced", fields: [{ name: "standard_only", type: "switch", label: "Use Standard allowance only", default: false }] } };

describe("Subscription options localization", () => {
  afterEach(() => { setActiveLanguage("zh-CN"); });
  it("localizes provider settings while preserving option identifiers and saved values", () => {
    const key = providerPluginOptionFieldKey(option.plugin_id, "standard_only");
    const props = { contributions: [option], placement: "advanced", values: { type: "factory_subscription", [key]: "true" }, onUpdate: vi.fn() };
    const view = render(<ProviderPluginFormSections {...props} />);
    for (const [language, label] of [["zh-CN", "仅使用 Standard 额度"], ["en", "Use Standard allowance only"], ["ja", "Standard 利用枠のみ使用する"]] as const) {
      setActiveLanguage(language);
      view.rerender(<ProviderPluginFormSections {...props} />);
      expect(screen.getByText(label)).toBeInTheDocument();
      expect(screen.getAllByRole("radio")[0]).toHaveAttribute("aria-checked", "true");
      expect((localizeBuiltinContribution(option).schema?.fields as Array<{ name: string }>)[0].name).toBe("standard_only");
    }
  });
  it("localizes quota titles at render time", () => {
    const quota = { ...option, title: "Subscription account quota" };
    for (const [language, title] of [["zh-CN", "订阅账号额度"], ["en", "Subscription account quota"], ["ja", "サブスクリプションの利用枠"]] as const) {
      setActiveLanguage(language);
      expect(providerQuotaPanelTitle(quota)).toBe(title);
    }
  });
});
