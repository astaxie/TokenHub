export type PluginManagerLifecycleStatus =
  | "enabled"
  | "disabled"
  | "pending_restart"
  | "failed_validation"
  | "rollback_available"
  | "mandatory"
  | "unknown";

export type PluginManagerStatusTone = "ok" | "warn" | "error" | "neutral";

export type PluginManagerActionID = "install" | "enable" | "disable" | "update" | "uninstall" | "rollback" | "operation";

export type PluginManagerActionState = {
  available: boolean;
  disabledReason: "built_in" | "mandatory" | "missing_distribution" | "not_installed" | "already_installed" | "not_applicable" | "";
  labelKey: string;
  busyLabelKey: string;
};

export type PluginManagerLifecyclePayload = {
  status?: string;
  reason?: string;
  restart_required?: boolean;
  health?: string;
  mandatory?: boolean;
  rollback_available?: boolean;
  rollback_version?: string;
  last_error_code?: string;
  audit_event?: string;
  loadable?: boolean;
};

export type PluginManagerDistributionPayload = {
  marketplace_url?: string;
  repository_url?: string;
  download_url?: string;
  checksum_sha256?: string;
  signature_url?: string;
  signature_algorithm?: string;
  signature_key_id?: string;
  homepage_url?: string;
  license?: string;
};

export type PluginManagerPluginPayload = PluginManagerLifecyclePayload & {
  id?: string;
  name?: string;
  version?: string;
  source?: string;
  distribution?: PluginManagerDistributionPayload | null;
  lifecycle?: PluginManagerLifecyclePayload | null;
};

export type PluginManagerMarketplacePayload = {
  installed?: boolean;
  installed_version?: string;
  update_available?: boolean;
};

export type PluginManagerDisplayInput = {
  plugin?: PluginManagerPluginPayload | null;
  marketplace?: PluginManagerMarketplacePayload | null;
};

export type PluginManagerLifecycleDisplayState = {
  status: PluginManagerLifecycleStatus;
  rawStatus: string;
  labelKey: string;
  tone: PluginManagerStatusTone;
  pillStatus: string;
  restartRequired: boolean;
  restartTextKey: string;
  reason: string;
  health: string;
  mandatory: boolean;
  loadable: boolean;
  rollbackAvailable: boolean;
  rollbackVersion: string;
  lastErrorCode: string;
  auditEvent: string;
  unknownStatus: boolean;
};

export type PluginManagerDisplayState = PluginManagerLifecycleDisplayState & {
  installed: boolean;
  installedVersion: string;
  updateAvailable: boolean;
  distributionReady: boolean;
  nextStatus?: "enabled" | "disabled";
  actions: Record<PluginManagerActionID, PluginManagerActionState>;
};

const lifecycleStatuses = new Set<string>([
  "enabled",
  "disabled",
  "pending_restart",
  "failed_validation",
  "rollback_available",
  "mandatory",
]);

export function pluginManagerLifecycleState(plugin?: PluginManagerPluginPayload | null): PluginManagerLifecycleDisplayState {
  const lifecycle = plugin?.lifecycle ?? {};
  const rawStatus = firstNonEmpty(lifecycle.status, plugin?.status, "enabled");
  const normalizedStatus = normalizeLifecycleStatus(rawStatus);
  const restartRequired = Boolean(lifecycle.restart_required ?? plugin?.restart_required) || normalizedStatus === "pending_restart";
  const mandatory = Boolean(lifecycle.mandatory ?? plugin?.mandatory) || normalizedStatus === "mandatory";
  const rollbackVersion = firstNonEmpty(lifecycle.rollback_version, plugin?.rollback_version);
  const rollbackAvailable = (Boolean(lifecycle.rollback_available ?? plugin?.rollback_available) || normalizedStatus === "rollback_available") && rollbackVersion !== "";
  const health = firstNonEmpty(lifecycle.health, plugin?.health, "unknown");
  const explicitLoadable = lifecycle.loadable ?? plugin?.loadable;
  const loadable = typeof explicitLoadable === "boolean" ? explicitLoadable : defaultLifecycleLoadable(normalizedStatus, rollbackAvailable);
  const status = displayLifecycleStatus(normalizedStatus, { mandatory, restartRequired, rollbackAvailable });

  return {
    status,
    rawStatus: rawStatus.trim(),
    labelKey: pluginManagerLifecycleLabelKey(status),
    tone: pluginManagerLifecycleTone(status, health),
    pillStatus: pluginManagerLifecyclePillStatus(status, health),
    restartRequired,
    restartTextKey: restartRequired ? "重启后生效" : "",
    reason: firstNonEmpty(lifecycle.reason, plugin?.reason),
    health,
    mandatory,
    loadable,
    rollbackAvailable,
    rollbackVersion,
    lastErrorCode: firstNonEmpty(lifecycle.last_error_code, plugin?.last_error_code),
    auditEvent: firstNonEmpty(lifecycle.audit_event, plugin?.audit_event),
    unknownStatus: status === "unknown",
  };
}

export function pluginManagerDisplayState(input: PluginManagerDisplayInput): PluginManagerDisplayState {
  const plugin = input.plugin ?? null;
  const marketplace = input.marketplace ?? null;
  const lifecycle = pluginManagerLifecycleState(plugin);
  const builtIn = plugin?.source === "built_in";
  const pluginPresent = Boolean(plugin);
  const installed = pluginPresent ? (marketplace ? Boolean(marketplace.installed) : true) : false;
  const distributionReady = pluginManagerDistributionReady(plugin);
  const mutable = installed && !builtIn && !lifecycle.mandatory;
  const updateAvailable = Boolean(marketplace?.update_available) || (!marketplace && distributionReady && !builtIn);
  const nextStatus = mutable ? pluginManagerNextStatus(lifecycle.status) : undefined;
  const operationAvailable = installed && lifecycle.loadable && lifecycle.status !== "pending_restart" && lifecycle.status !== "failed_validation";

  return {
    ...lifecycle,
    installed,
    installedVersion: firstNonEmpty(marketplace?.installed_version, plugin?.version),
    updateAvailable,
    distributionReady,
    nextStatus,
    actions: {
      install: actionState(!installed && distributionReady, "安装插件", "安装中", installed ? "already_installed" : "missing_distribution"),
      enable: actionState(mutable && lifecycle.status === "disabled", "启用", "更新中", actionDisabledReason({ builtIn, mandatory: lifecycle.mandatory, installed })),
      disable: actionState(mutable && nextStatus === "disabled", "禁用", "更新中", actionDisabledReason({ builtIn, mandatory: lifecycle.mandatory, installed })),
      update: actionState(installed && !builtIn && updateAvailable && distributionReady, "更新", "更新中", builtIn ? "built_in" : distributionReady ? "not_applicable" : "missing_distribution"),
      uninstall: actionState(mutable, "卸载", "卸载中", actionDisabledReason({ builtIn, mandatory: lifecycle.mandatory, installed })),
      rollback: actionState(mutable && lifecycle.rollbackAvailable, "回滚", "回滚中", lifecycle.rollbackAvailable ? actionDisabledReason({ builtIn, mandatory: lifecycle.mandatory, installed }) : "not_applicable"),
      operation: actionState(operationAvailable, "执行", "执行中", installed ? "not_applicable" : "not_installed"),
    },
  };
}

export function pluginManagerDistributionReady(plugin?: PluginManagerPluginPayload | null): boolean {
  const distribution = plugin?.distribution;
  return Boolean(distribution && nonEmpty(distribution.download_url) && nonEmpty(distribution.checksum_sha256));
}

export function pluginManagerNextStatus(status: PluginManagerLifecycleStatus): "enabled" | "disabled" | undefined {
  if (status === "disabled") return "enabled";
  if (status === "enabled" || status === "rollback_available") return "disabled";
  return undefined;
}

export function pluginManagerLifecycleLabelKey(status: PluginManagerLifecycleStatus): string {
  switch (status) {
    case "enabled":
      return "已启用";
    case "disabled":
      return "已禁用";
    case "pending_restart":
      return "待重启";
    case "failed_validation":
      return "校验失败";
    case "rollback_available":
      return "可回滚";
    case "mandatory":
      return "强制启用";
    default:
      return "未知";
  }
}

export function pluginManagerLifecycleTone(status: PluginManagerLifecycleStatus, health = "unknown"): PluginManagerStatusTone {
  if (status === "unknown") return "neutral";
  if (status === "failed_validation" || health === "unhealthy") return "error";
  if (status === "pending_restart" || status === "rollback_available") return "warn";
  if (status === "enabled" || status === "mandatory" || health === "healthy") return "ok";
  if (status === "disabled") return "error";
  return "neutral";
}

export function pluginManagerLifecyclePillStatus(status: PluginManagerLifecycleStatus, health = "unknown"): string {
  const tone = pluginManagerLifecycleTone(status, health);
  if (tone === "ok") return "active";
  if (tone === "warn") return "warning";
  if (tone === "error") return status === "disabled" ? "disabled" : "failed";
  return "unknown";
}

function displayLifecycleStatus(
  status: PluginManagerLifecycleStatus,
  flags: { mandatory: boolean; restartRequired: boolean; rollbackAvailable: boolean },
): PluginManagerLifecycleStatus {
  if (status === "unknown") return "unknown";
  if (flags.mandatory) return "mandatory";
  if (flags.restartRequired) return "pending_restart";
  if (flags.rollbackAvailable) return "rollback_available";
  return status;
}

function normalizeLifecycleStatus(status: string): PluginManagerLifecycleStatus {
  const normalized = status.trim().toLowerCase();
  return lifecycleStatuses.has(normalized) ? (normalized as PluginManagerLifecycleStatus) : "unknown";
}

function defaultLifecycleLoadable(status: PluginManagerLifecycleStatus, rollbackAvailable: boolean): boolean {
  return status === "enabled" || status === "mandatory" || rollbackAvailable;
}

function actionState(available: boolean, labelKey: string, busyLabelKey: string, disabledReason: PluginManagerActionState["disabledReason"]): PluginManagerActionState {
  return {
    available,
    disabledReason: available ? "" : disabledReason,
    labelKey,
    busyLabelKey,
  };
}

function actionDisabledReason(flags: { builtIn: boolean; mandatory: boolean; installed: boolean }): PluginManagerActionState["disabledReason"] {
  if (!flags.installed) return "not_installed";
  if (flags.builtIn) return "built_in";
  if (flags.mandatory) return "mandatory";
  return "not_applicable";
}

function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    if (nonEmpty(value)) return value.trim();
  }
  return "";
}

function nonEmpty(value: string | undefined | null): value is string {
  return typeof value === "string" && value.trim() !== "";
}
