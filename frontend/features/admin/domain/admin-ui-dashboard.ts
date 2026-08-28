import { type AdminUIContribution } from "../core/types";
import { simRegistryFromPlugins, type SIMPluginDescriptorLike } from "./sim-registry";

export type DashboardCardSize = "small" | "medium" | "large" | "wide";

export type DashboardCompositionCard = {
  contributionID: string;
  pluginID?: string;
  region?: string;
  size?: DashboardCardSize;
  order: number;
};

export type DashboardComposition = {
  key: string;
  pluginID: string;
  id: string;
  title: string;
  source: "sim" | "admin_ui";
  layout?: "grid" | "operations" | "compact_grid";
  default: boolean;
  order: number;
  priority: number;
  cards: DashboardCompositionCard[];
};

export type DashboardCardEntry = {
  key: string;
  contribution: AdminUIContribution;
  composition?: DashboardComposition;
  slot?: DashboardCompositionCard;
};

export type DashboardCompositionData = {
  plugins?: readonly SIMPluginDescriptorLike[];
  pluginUI?: readonly AdminUIContribution[];
};

const dashboardCardSlot = "dashboard.card";
const dashboardCompositionSlot = "dashboard.composition";
const defaultOrder = 1000;
const dashboardCardSizes: DashboardCardSize[] = ["small", "medium", "large", "wide"];
const safeIdentifierPattern = /^[A-Za-z0-9_.-]+$/u;

export function dashboardCardRegistry(data: DashboardCompositionData): DashboardCardEntry[] {
  const cards = dashboardCardContributions(data.pluginUI);
  const composition = activeDashboardComposition(data);
  if (!composition) return cards.map((contribution) => dashboardCardEntry(contribution));
  return dashboardCardsForComposition(cards, composition);
}

export function dashboardCardContributions(contributions?: readonly AdminUIContribution[]) {
  return (contributions ?? []).filter((contribution) => contribution.slot === dashboardCardSlot);
}

export function dashboardCompositionsFromData(data: DashboardCompositionData): DashboardComposition[] {
  return [
    ...dashboardCompositionsFromSIM(data.plugins),
    ...dashboardCompositionsFromAdminUI(data.pluginUI),
  ].sort(compareDashboardCompositions);
}

export function activeDashboardComposition(data: DashboardCompositionData): DashboardComposition | undefined {
  return dashboardCompositionsFromData(data)[0];
}

function dashboardCompositionsFromSIM(plugins?: readonly SIMPluginDescriptorLike[]): DashboardComposition[] {
  return simRegistryFromPlugins(plugins).dashboardCompositions.flatMap((capability) => {
    const composition = dashboardCompositionFromPayload(capability.payload, {
      key: capability.key,
      pluginID: capability.pluginID,
      id: capability.id,
      title: capability.title,
      source: "sim",
      order: capability.order,
      priority: capability.priority,
    });
    return composition ? [composition] : [];
  });
}

function dashboardCompositionsFromAdminUI(contributions?: readonly AdminUIContribution[]): DashboardComposition[] {
  return (contributions ?? []).flatMap((contribution) => {
    if (contribution.slot !== dashboardCompositionSlot) return [];
    const payload = jsonObjectValue(contribution.schema?.composition);
    if (!payload) return [];
    const composition = dashboardCompositionFromPayload(payload, {
      key: dashboardContributionKey(contribution),
      pluginID: contribution.plugin_id,
      id: contribution.id,
      title: contribution.title || contribution.id,
      source: "admin_ui",
      order: numberValue(contribution.schema?.order, defaultOrder),
      priority: numberValue(contribution.schema?.priority, 0),
      default: contribution.schema?.default === true,
    });
    return composition ? [composition] : [];
  });
}

function dashboardCompositionFromPayload(
  payload: Record<string, unknown>,
  source: Omit<DashboardComposition, "cards" | "default" | "layout"> & { default?: boolean; layout?: DashboardComposition["layout"] },
): DashboardComposition | null {
  const id = safeIdentifier(payload.id) || safeIdentifier(source.id);
  if (!id) return null;
  const cards = dashboardCompositionCards(payload);
  if (cards.length === 0) return null;
  return {
    ...source,
    id,
    title: stringValue(payload.title) || stringValue(payload.name) || source.title || id,
    layout: dashboardLayout(payload.layout) || source.layout,
    default: payload.default === true || source.default === true,
    order: numberValue(payload.order, source.order),
    priority: numberValue(payload.priority, source.priority),
    cards,
  };
}

function dashboardCompositionCards(payload: Record<string, unknown>): DashboardCompositionCard[] {
  return [
    ...dashboardCardsFromArray(payload.cards),
    ...dashboardCardsFromSections(payload.sections),
  ].sort(compareDashboardCompositionCards);
}

function dashboardCardsFromSections(value: unknown): DashboardCompositionCard[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((section) => {
    const payload = jsonObjectValue(section);
    if (!payload) return [];
    const region = safeIdentifier(payload.region) || safeIdentifier(payload.id);
    return dashboardCardsFromArray(payload.cards, region);
  });
}

function dashboardCardsFromArray(value: unknown, inheritedRegion?: string): DashboardCompositionCard[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((raw, index) => {
    const card = dashboardCompositionCard(raw, inheritedRegion, index);
    return card ? [card] : [];
  });
}

function dashboardCompositionCard(raw: unknown, inheritedRegion: string | undefined, index: number): DashboardCompositionCard | null {
  if (typeof raw === "string") {
    const contributionID = safeIdentifier(raw);
    return contributionID ? { contributionID, region: inheritedRegion, order: index } : null;
  }
  const payload = jsonObjectValue(raw);
  if (!payload || payload.visible === false || payload.hidden === true) return null;
  const contributionID = safeIdentifier(payload.contribution_id) || safeIdentifier(payload.contributionID) || safeIdentifier(payload.id) || safeIdentifier(payload.card_id);
  if (!contributionID) return null;
  return {
    contributionID,
    pluginID: safeIdentifier(payload.plugin_id) || safeIdentifier(payload.pluginID) || undefined,
    region: safeIdentifier(payload.region) || inheritedRegion,
    size: dashboardCardSize(payload.size),
    order: numberValue(payload.order, index),
  };
}

function dashboardCardsForComposition(contributions: AdminUIContribution[], composition: DashboardComposition): DashboardCardEntry[] {
  const used = new Set<string>();
  return composition.cards.flatMap((slot) => {
    const contribution = contributions.find((candidate) => {
      const key = dashboardContributionKey(candidate);
      if (used.has(key)) return false;
      if (candidate.id !== slot.contributionID) return false;
      return !slot.pluginID || candidate.plugin_id === slot.pluginID;
    });
    if (!contribution) return [];
    used.add(dashboardContributionKey(contribution));
    return [dashboardCardEntry(contribution, composition, slot)];
  });
}

function dashboardCardEntry(contribution: AdminUIContribution, composition?: DashboardComposition, slot?: DashboardCompositionCard): DashboardCardEntry {
  return {
    key: dashboardContributionKey(contribution),
    contribution,
    composition,
    slot,
  };
}

function compareDashboardCompositions(left: DashboardComposition, right: DashboardComposition) {
  return compareBoolean(left.default, right.default) ||
    compareNumber(left.order, right.order) ||
    compareNumber(right.priority, left.priority) ||
    compareSource(left.source, right.source) ||
    left.pluginID.localeCompare(right.pluginID) ||
    left.id.localeCompare(right.id) ||
    left.key.localeCompare(right.key);
}

function compareDashboardCompositionCards(left: DashboardCompositionCard, right: DashboardCompositionCard) {
  return compareNumber(left.order, right.order) ||
    stringValue(left.region).localeCompare(stringValue(right.region)) ||
    stringValue(left.pluginID).localeCompare(stringValue(right.pluginID)) ||
    left.contributionID.localeCompare(right.contributionID);
}

function compareSource(left: DashboardComposition["source"], right: DashboardComposition["source"]) {
  if (left === right) return 0;
  return left === "sim" ? -1 : 1;
}

function compareBoolean(left: boolean, right: boolean) {
  return left === right ? 0 : left ? -1 : 1;
}

function compareNumber(left: number, right: number) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function dashboardContributionKey(contribution: AdminUIContribution) {
  return `${contribution.plugin_id}:${contribution.id}`;
}

function dashboardLayout(value: unknown): DashboardComposition["layout"] | undefined {
  return value === "grid" || value === "operations" || value === "compact_grid" ? value : undefined;
}

function dashboardCardSize(value: unknown): DashboardCardSize | undefined {
  return dashboardCardSizes.includes(value as DashboardCardSize) ? value as DashboardCardSize : undefined;
}

function jsonObjectValue(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function safeIdentifier(value: unknown) {
  const normalized = stringValue(value);
  if (!normalized || normalized.includes("..") || normalized.includes("/") || normalized.includes("\\")) return "";
  return safeIdentifierPattern.test(normalized) ? normalized : "";
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function numberValue(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : fallback;
}
