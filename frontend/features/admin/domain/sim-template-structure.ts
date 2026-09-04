import { type AdminUIContribution } from "../core/types";
import { type SIMCapability, type SIMJSONValue, type SIMRegistry } from "./sim-registry";

export type SIMTemplateBlockKind =
  | "theme"
  | "navigation"
  | "topbar"
  | "global_search"
  | "account_area"
  | "content"
  | "page_template"
  | "page_region"
  | "dashboard"
  | "dashboard_card"
  | "ui_contribution";

export type SIMTemplateBlock = {
  key: string;
  pluginID: string;
  kind: SIMTemplateBlockKind;
  title: string;
  placement: string;
  sourceCapability?: SIMCapability;
  contribution?: AdminUIContribution;
  details: Record<string, unknown>;
};

export function simTemplateStructure(
  pluginID: string,
  registry: SIMRegistry,
  contributions: readonly AdminUIContribution[],
): SIMTemplateBlock[] {
  const blocks: SIMTemplateBlock[] = [];
  for (const theme of registry.themeTokens.filter(byPluginID(pluginID))) {
    blocks.push(capabilityBlock(theme, "theme", theme.title, "shell.theme", theme.payload));
  }
  for (const layout of registry.shellLayouts.filter(byPluginID(pluginID))) {
    const shared = { capability: layout.id, ...layout.payload };
    blocks.push(
      capabilityBlock(layout, "navigation", "navigation", "shell.navigation", shared),
      capabilityBlock(layout, "topbar", "topbar", "shell.topbar", shared),
      capabilityBlock(layout, "global_search", "global_search", "shell.topbar.search", shared),
      capabilityBlock(layout, "account_area", "account_area", "shell.topbar.account", shared),
      capabilityBlock(layout, "content", "content", "shell.content", shared),
    );
  }
  for (const template of registry.pageTemplates.filter(byPluginID(pluginID))) {
    const target = stringValue(template.payload.target) || "page";
    blocks.push(capabilityBlock(template, "page_template", template.title, target, template.payload));
    for (const [index, region] of stringArray(template.payload.regions).entries()) {
      blocks.push(capabilityBlock(template, "page_region", region, `${target}.${region}`, {
        template: template.id,
        target,
        region,
        layout: template.payload.layout,
      }, String(index)));
    }
  }
  for (const composition of registry.dashboardCompositions.filter(byPluginID(pluginID))) {
    blocks.push(capabilityBlock(composition, "dashboard", composition.title, "dashboard", composition.payload));
    for (const [index, rawCard] of arrayValue(composition.payload.cards).entries()) {
      const card = objectValue(rawCard);
      if (!card) continue;
      const contributionID = stringValue(card.contribution_id) || `card-${index + 1}`;
      const region = stringValue(card.region) || "main";
      blocks.push(capabilityBlock(composition, "dashboard_card", contributionID, `dashboard.${region}`, {
        composition: composition.id,
        ...card,
      }, String(index)));
    }
  }
  for (const contribution of contributions.filter((item) => item.plugin_id === pluginID)) {
    blocks.push({
      key: `${pluginID}:ui:${contribution.slot}:${contribution.id}`,
      pluginID,
      kind: "ui_contribution",
      title: contribution.title || contribution.id,
      placement: contribution.slot,
      contribution,
      details: {
        id: contribution.id,
        slot: contribution.slot,
        action: contribution.action ?? null,
        schema: contribution.schema ?? null,
      },
    });
  }
  return blocks;
}

function capabilityBlock(
  capability: SIMCapability,
  kind: SIMTemplateBlockKind,
  title: string,
  placement: string,
  details: Record<string, unknown>,
  suffix = "",
): SIMTemplateBlock {
  return {
    key: `${capability.key}:block:${kind}:${placement}${suffix ? `:${suffix}` : ""}`,
    pluginID: capability.pluginID,
    kind,
    title,
    placement,
    sourceCapability: capability,
    details,
  };
}

function byPluginID(pluginID: string) {
  return (capability: SIMCapability) => capability.pluginID === pluginID;
}

function stringArray(value: SIMJSONValue | undefined) {
  return Array.isArray(value) ? value.flatMap((item) => typeof item === "string" && item.trim() ? [item.trim()] : []) : [];
}

function arrayValue(value: SIMJSONValue | undefined): SIMJSONValue[] {
  return Array.isArray(value) ? value : [];
}

function objectValue(value: SIMJSONValue): Record<string, SIMJSONValue> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value : undefined;
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}
