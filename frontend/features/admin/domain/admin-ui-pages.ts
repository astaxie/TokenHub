import { type AdminUIContribution } from "../core/types";
import { adminUIPluginPages, type AdminUIPluginPage } from "./admin-ui-registry";
import { simRegistryFromPlugins, type SIMPluginDescriptorLike } from "./sim-registry";

export type AdminUIPageTemplateSource = "sim" | "admin_ui";

export type AdminUIPageLayout = "stack" | "split" | "grid" | "metrics" | "detail" | "compact";

export type AdminUIPageDensity = "default" | "comfortable" | "compact";

export type AdminUIPageFrame = "section" | "plain" | "tool";

export type AdminUIPageTemplate = {
  key: string;
  pluginID: string;
  id: string;
  title: string;
  source: AdminUIPageTemplateSource;
  targetSlot: "nav.section" | string;
  targetPageID: string;
  targetPluginID?: string;
  layout?: AdminUIPageLayout;
  region?: string;
  density?: AdminUIPageDensity;
  frame?: AdminUIPageFrame;
  order: number;
  priority: number;
  default: boolean;
};

export type AdminUIPluginPageEntry = AdminUIPluginPage & {
  template?: AdminUIPageTemplate;
};

export type AdminUIPageRegistryData = {
  plugins?: readonly SIMPluginDescriptorLike[];
  pluginUI?: readonly AdminUIContribution[];
};

const pageTemplateSlot = "page.template";
const navSectionSlot = "nav.section";
const defaultOrder = 1000;
const safeIdentifierPattern = /^[A-Za-z0-9_.:-]+$/u;
const pageLayouts: AdminUIPageLayout[] = ["stack", "split", "grid", "metrics", "detail", "compact"];
const pageDensities: AdminUIPageDensity[] = ["default", "comfortable", "compact"];
const pageFrames: AdminUIPageFrame[] = ["section", "plain", "tool"];

export function adminUIPageRegistry(data: AdminUIPageRegistryData): AdminUIPluginPageEntry[] {
  const templates = adminUIPageTemplatesFromData(data);
  return adminUIPluginPages([...(data.pluginUI ?? [])]).map((page) => ({
    ...page,
    template: activeTemplateForPage(page, templates),
  }));
}

export function adminUIPageTemplatesFromData(data: AdminUIPageRegistryData): AdminUIPageTemplate[] {
  return [
    ...adminUIPageTemplatesFromSIM(data.plugins),
    ...adminUIPageTemplatesFromAdminUI(data.pluginUI),
  ].sort(comparePageTemplates);
}

export function adminUIPageTemplatesFromSIM(plugins?: readonly SIMPluginDescriptorLike[]): AdminUIPageTemplate[] {
  return simRegistryFromPlugins(plugins).pageTemplates.flatMap((capability) => {
    const payloads = pageTemplatePayloads(capability.payload);
    return payloads.flatMap((payload, index) => {
      const template = pageTemplateFromPayload(payload, {
        key: payloads.length > 1 ? `${capability.key}:${index}` : capability.key,
        pluginID: capability.pluginID,
        id: capability.id,
        title: capability.title,
        source: "sim",
        order: capability.order,
        priority: capability.priority,
      });
      return template ? [template] : [];
    });
  });
}

export function adminUIPageTemplatesFromAdminUI(contributions?: readonly AdminUIContribution[]): AdminUIPageTemplate[] {
  return (contributions ?? []).flatMap((contribution) => {
    if (contribution.slot !== pageTemplateSlot) return [];
    const payloads = pageTemplatePayloads(contribution.schema?.template ?? contribution.schema);
    return payloads.flatMap((payload, index) => {
      const template = pageTemplateFromPayload(payload, {
        key: payloads.length > 1 ? `${adminUIPageContributionKey(contribution)}:${index}` : adminUIPageContributionKey(contribution),
        pluginID: contribution.plugin_id,
        id: contribution.id,
        title: contribution.title || contribution.id,
        source: "admin_ui",
        order: numberValue(contribution.schema?.order, defaultOrder),
        priority: numberValue(contribution.schema?.priority, 0),
        default: contribution.schema?.default === true,
      });
      return template ? [template] : [];
    });
  });
}

function pageTemplatePayloads(value: unknown): Record<string, unknown>[] {
  const payload = jsonObjectValue(value);
  if (!payload) return [];
  if (Array.isArray(payload.pages)) return payload.pages.flatMap((page) => {
    const pagePayload = jsonObjectValue(page);
    return pagePayload ? [{ ...payload, ...pagePayload, pages: undefined }] : [];
  });
  return [payload];
}

function pageTemplateFromPayload(
  payload: Record<string, unknown>,
  source: Omit<AdminUIPageTemplate, "default" | "targetPageID" | "targetSlot"> & {
    default?: boolean;
    targetPageID?: string;
    targetSlot?: string;
  },
): AdminUIPageTemplate | null {
  const rawTargetSlot = firstValue(payload.slot, payload.target_slot, payload.targetSlot);
  const targetSlot = rawTargetSlot === undefined ? source.targetSlot || navSectionSlot : safeSlot(rawTargetSlot);
  const rawLayout = payload.layout;
  const layout = pageLayout(rawLayout);
  const rawRegion = payload.region;
  const region = rawRegion === undefined ? "" : safeIdentifier(rawRegion);
  const rawDensity = payload.density;
  const density = pageDensity(rawDensity);
  const rawFrame = payload.frame;
  const frame = pageFrame(rawFrame);
  const targetPageID = safeIdentifier(payload.page_id) ||
    safeIdentifier(payload.pageID) ||
    safeIdentifier(payload.contribution_id) ||
    safeIdentifier(payload.contributionID) ||
    safeIdentifier(payload.target_page_id) ||
    safeIdentifier(payload.targetPageID) ||
    safeIdentifier(payload.target) ||
    safeIdentifier(source.targetPageID);
  if (!targetPageID || !targetSlot) return null;
  if (rawLayout !== undefined && !layout) return null;
  if (rawRegion !== undefined && !region) return null;
  if (rawDensity !== undefined && !density) return null;
  if (rawFrame !== undefined && !frame) return null;
  return {
    ...source,
    id: safeIdentifier(payload.id) || safeIdentifier(source.id) || targetPageID,
    title: stringValue(payload.title) || stringValue(payload.name) || source.title || targetPageID,
    targetSlot,
    targetPageID,
    targetPluginID: safeIdentifier(payload.plugin_id) || safeIdentifier(payload.pluginID) || safeIdentifier(payload.target_plugin_id) || safeIdentifier(payload.targetPluginID) || undefined,
    layout,
    region: region || undefined,
    density,
    frame,
    order: numberValue(payload.order, source.order),
    priority: numberValue(payload.priority, source.priority),
    default: payload.default === true || source.default === true,
  };
}

function activeTemplateForPage(page: AdminUIPluginPage, templates: AdminUIPageTemplate[]) {
  return templates.find((template) => templateMatchesPage(template, page));
}

function templateMatchesPage(template: AdminUIPageTemplate, page: AdminUIPluginPage) {
  if (template.targetSlot !== navSectionSlot) return false;
  if (template.targetPageID !== page.id) return false;
  return !template.targetPluginID || template.targetPluginID === page.pluginID;
}

function comparePageTemplates(left: AdminUIPageTemplate, right: AdminUIPageTemplate) {
  return compareBoolean(left.default, right.default) ||
    compareNumber(left.order, right.order) ||
    compareNumber(right.priority, left.priority) ||
    compareSource(left.source, right.source) ||
    left.pluginID.localeCompare(right.pluginID) ||
    left.id.localeCompare(right.id) ||
    left.key.localeCompare(right.key);
}

function compareSource(left: AdminUIPageTemplateSource, right: AdminUIPageTemplateSource) {
  if (left === right) return 0;
  return left === "sim" ? -1 : 1;
}

function compareBoolean(left: boolean, right: boolean) {
  return left === right ? 0 : left ? -1 : 1;
}

function compareNumber(left: number, right: number) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function pageLayout(value: unknown): AdminUIPageLayout | undefined {
  return pageLayouts.includes(value as AdminUIPageLayout) ? value as AdminUIPageLayout : undefined;
}

function pageDensity(value: unknown): AdminUIPageDensity | undefined {
  return pageDensities.includes(value as AdminUIPageDensity) ? value as AdminUIPageDensity : undefined;
}

function pageFrame(value: unknown): AdminUIPageFrame | undefined {
  return pageFrames.includes(value as AdminUIPageFrame) ? value as AdminUIPageFrame : undefined;
}

function jsonObjectValue(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function safeSlot(value: unknown) {
  const slot = safeIdentifier(value);
  if (!slot) return "";
  return slot.includes(":") ? "" : slot;
}

function safeIdentifier(value: unknown) {
  const normalized = stringValue(value);
  if (!normalized || normalized.includes("..") || normalized.includes("/") || normalized.includes("\\") || normalized.includes("<")) return "";
  return safeIdentifierPattern.test(normalized) ? normalized : "";
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function numberValue(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : fallback;
}

function adminUIPageContributionKey(contribution: AdminUIContribution) {
  return `${contribution.plugin_id}:${contribution.id}`;
}

function firstValue(...values: unknown[]) {
  return values.find((value) => value !== undefined);
}
