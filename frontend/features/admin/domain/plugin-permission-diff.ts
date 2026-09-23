export type PluginPermissionDiffTone = "ok" | "warn" | "error" | "neutral";

export type PluginPermissionDiffVerdict = "allow" | "review_required" | "approval_required" | "unknown";

export type PluginPermissionSensitivity = "public" | "internal" | "sensitive" | "secret" | "unknown";

export type PluginPermissionChangePayload = {
  kind?: string;
  name?: string;
  access?: string;
  sensitivity?: string;
  previous_sensitivity?: string;
  candidate_sensitivity?: string;
};

export type PluginPermissionDiffSummaryPayload = {
  added?: number;
  removed?: number;
  unchanged?: number;
  changed_sensitivity?: number;
};

export type PluginPermissionDiffPayload = {
  available?: boolean;
  verdict?: string;
  reason_code?: string;
  highest_sensitivity?: string;
  summary?: PluginPermissionDiffSummaryPayload | null;
  added?: PluginPermissionChangePayload[];
  removed?: PluginPermissionChangePayload[];
  unchanged?: PluginPermissionChangePayload[];
  changed_sensitivity?: PluginPermissionChangePayload[];
};

export type PluginPermissionDiffPreviewPayload = {
  operation?: string;
  plugin_id?: string;
  current_version?: string;
  candidate_version?: string;
  permission_diff?: PluginPermissionDiffPayload | null;
  trust?: PluginPermissionDiffTrustPayload | null;
  compatibility?: PluginPermissionDiffCompatibilityPayload | null;
};

export type PluginPermissionDiffTrustPayload = {
  verdict?: string;
  checksum_present?: boolean;
  signature_present?: boolean;
  signature_algorithm?: string;
  signature_key_id?: string;
  reason_code?: string;
};

export type PluginPermissionDiffCompatibilityPayload = {
  plugin_api?: string;
  manifest_schema_version?: number;
  core_version?: string;
  verdict?: string;
  reason_code?: string;
};

export type PluginPermissionChangeDisplay = {
  kind: string;
  name: string;
  access: string;
  sensitivity: PluginPermissionSensitivity;
  sensitivityLabelKey: string;
  sensitivityTone: PluginPermissionDiffTone;
  previousSensitivity: PluginPermissionSensitivity;
  previousSensitivityLabelKey: string;
  candidateSensitivity: PluginPermissionSensitivity;
  candidateSensitivityLabelKey: string;
};

export type PluginPermissionDiffSection = {
  id: "added" | "removed" | "unchanged" | "changed_sensitivity";
  labelKey: string;
  count: number;
  tone: PluginPermissionDiffTone;
  changes: PluginPermissionChangeDisplay[];
};

export type PluginPermissionDiffDisplayState = {
  available: boolean;
  operation: "install" | "update" | "unknown";
  pluginID: string;
  currentVersion: string;
  candidateVersion: string;
  verdict: PluginPermissionDiffVerdict;
  rawVerdict: string;
  verdictLabelKey: string;
  tone: PluginPermissionDiffTone;
  reasonCode: string;
  reasonLabelKey: string;
  highestSensitivity: PluginPermissionSensitivity;
  highestSensitivityLabelKey: string;
  highestSensitivityTone: PluginPermissionDiffTone;
  requiresReview: boolean;
  requiresApproval: boolean;
  summary: Required<PluginPermissionDiffSummaryPayload>;
  sections: PluginPermissionDiffSection[];
  trust: PluginPermissionDiffTrustDisplay;
  compatibility: PluginPermissionDiffCompatibilityDisplay;
};

export type PluginPermissionDiffTrustDisplay = {
  verdict: string;
  labelKey: string;
  tone: PluginPermissionDiffTone;
  checksumPresent: boolean;
  signaturePresent: boolean;
  signatureAlgorithm: string;
  signatureKeyID: string;
  reasonCode: string;
};

export type PluginPermissionDiffCompatibilityDisplay = {
  verdict: string;
  labelKey: string;
  tone: PluginPermissionDiffTone;
  pluginAPI: string;
  manifestSchemaVersion: number | undefined;
  coreVersion: string;
  reasonCode: string;
};

export function pluginPermissionDiffDisplay(input?: PluginPermissionDiffPreviewPayload | null): PluginPermissionDiffDisplayState {
  const diff = input?.permission_diff ?? null;
  const added = permissionChanges(diff?.added);
  const removed = permissionChanges(diff?.removed);
  const unchanged = permissionChanges(diff?.unchanged);
  const changedSensitivity = permissionChanges(diff?.changed_sensitivity);
  const summary = permissionDiffSummary(diff?.summary, {
    added: added.length,
    removed: removed.length,
    unchanged: unchanged.length,
    changed_sensitivity: changedSensitivity.length,
  });
  const rawVerdict = firstNonEmpty(diff?.verdict, "unknown");
  const verdict = normalizeVerdict(rawVerdict);
  const highestSensitivity = normalizeSensitivity(firstNonEmpty(diff?.highest_sensitivity, highestSensitivityFromChanges([
    ...added,
    ...removed,
    ...unchanged,
    ...changedSensitivity,
  ])));

  return {
    available: Boolean(diff?.available),
    operation: normalizeOperation(input?.operation),
    pluginID: firstNonEmpty(input?.plugin_id),
    currentVersion: firstNonEmpty(input?.current_version),
    candidateVersion: firstNonEmpty(input?.candidate_version),
    verdict,
    rawVerdict,
    verdictLabelKey: verdictLabelKey(verdict),
    tone: verdictTone(verdict),
    reasonCode: firstNonEmpty(diff?.reason_code),
    reasonLabelKey: reasonLabelKey(diff?.reason_code),
    highestSensitivity,
    highestSensitivityLabelKey: sensitivityLabelKey(highestSensitivity),
    highestSensitivityTone: sensitivityTone(highestSensitivity),
    requiresReview: verdict === "review_required" || verdict === "approval_required",
    requiresApproval: verdict === "approval_required",
    summary,
    sections: [
      permissionDiffSection("added", "新增权限", "warn", added, summary.added),
      permissionDiffSection("removed", "移除权限", "ok", removed, summary.removed),
      permissionDiffSection("changed_sensitivity", "敏感度变化", "warn", changedSensitivity, summary.changed_sensitivity),
      permissionDiffSection("unchanged", "未变化权限", "neutral", unchanged, summary.unchanged),
    ],
    trust: trustDisplay(input?.trust),
    compatibility: compatibilityDisplay(input?.compatibility),
  };
}

function permissionDiffSection(
  id: PluginPermissionDiffSection["id"],
  labelKey: string,
  tone: PluginPermissionDiffTone,
  changes: PluginPermissionChangeDisplay[],
  count: number,
): PluginPermissionDiffSection {
  return { id, labelKey, tone, changes, count };
}

function permissionChanges(changes?: PluginPermissionChangePayload[] | null): PluginPermissionChangeDisplay[] {
  if (!Array.isArray(changes)) return [];
  return changes.map((change) => {
    const sensitivity = normalizeSensitivity(change.sensitivity);
    const previousSensitivity = normalizeSensitivity(change.previous_sensitivity);
    const candidateSensitivity = normalizeSensitivity(firstNonEmpty(change.candidate_sensitivity, change.sensitivity));
    return {
      kind: firstNonEmpty(change.kind, "unknown"),
      name: firstNonEmpty(change.name, "unknown"),
      access: firstNonEmpty(change.access, "unknown"),
      sensitivity,
      sensitivityLabelKey: sensitivityLabelKey(sensitivity),
      sensitivityTone: sensitivityTone(sensitivity),
      previousSensitivity,
      previousSensitivityLabelKey: sensitivityLabelKey(previousSensitivity),
      candidateSensitivity,
      candidateSensitivityLabelKey: sensitivityLabelKey(candidateSensitivity),
    };
  });
}

function permissionDiffSummary(
  summary: PluginPermissionDiffSummaryPayload | null | undefined,
  fallback: Required<PluginPermissionDiffSummaryPayload>,
): Required<PluginPermissionDiffSummaryPayload> {
  return {
    added: nonNegativeInteger(summary?.added, fallback.added),
    removed: nonNegativeInteger(summary?.removed, fallback.removed),
    unchanged: nonNegativeInteger(summary?.unchanged, fallback.unchanged),
    changed_sensitivity: nonNegativeInteger(summary?.changed_sensitivity, fallback.changed_sensitivity),
  };
}

function trustDisplay(trust?: PluginPermissionDiffTrustPayload | null): PluginPermissionDiffTrustDisplay {
  const verdict = firstNonEmpty(trust?.verdict, "unverified");
  return {
    verdict,
    labelKey: trustLabelKey(verdict),
    tone: trustTone(verdict),
    checksumPresent: Boolean(trust?.checksum_present),
    signaturePresent: Boolean(trust?.signature_present),
    signatureAlgorithm: firstNonEmpty(trust?.signature_algorithm),
    signatureKeyID: firstNonEmpty(trust?.signature_key_id),
    reasonCode: firstNonEmpty(trust?.reason_code),
  };
}

function compatibilityDisplay(compatibility?: PluginPermissionDiffCompatibilityPayload | null): PluginPermissionDiffCompatibilityDisplay {
  const verdict = firstNonEmpty(compatibility?.verdict, "unknown");
  return {
    verdict,
    labelKey: compatibilityLabelKey(verdict),
    tone: compatibilityTone(verdict),
    pluginAPI: firstNonEmpty(compatibility?.plugin_api),
    manifestSchemaVersion: positiveInteger(compatibility?.manifest_schema_version),
    coreVersion: firstNonEmpty(compatibility?.core_version),
    reasonCode: firstNonEmpty(compatibility?.reason_code),
  };
}

function normalizeOperation(operation?: string): PluginPermissionDiffDisplayState["operation"] {
  const normalized = firstNonEmpty(operation).toLowerCase();
  if (normalized === "install" || normalized === "update") return normalized;
  return "unknown";
}

function normalizeVerdict(verdict: string): PluginPermissionDiffVerdict {
  const normalized = verdict.trim().toLowerCase();
  if (normalized === "allow" || normalized === "review_required" || normalized === "approval_required") return normalized;
  return "unknown";
}

function normalizeSensitivity(sensitivity?: string): PluginPermissionSensitivity {
  const normalized = firstNonEmpty(sensitivity).toLowerCase();
  if (normalized === "public" || normalized === "internal" || normalized === "sensitive" || normalized === "secret") return normalized;
  return "unknown";
}

function highestSensitivityFromChanges(changes: PluginPermissionChangeDisplay[]): PluginPermissionSensitivity {
  return changes.reduce<PluginPermissionSensitivity>((highest, change) => {
    return sensitivityRank(change.sensitivity) > sensitivityRank(highest) ? change.sensitivity : highest;
  }, "public");
}

function verdictLabelKey(verdict: PluginPermissionDiffVerdict): string {
  switch (verdict) {
    case "allow":
      return "可继续";
    case "review_required":
      return "需要复核";
    case "approval_required":
      return "需要批准";
    default:
      return "未知";
  }
}

function verdictTone(verdict: PluginPermissionDiffVerdict): PluginPermissionDiffTone {
  if (verdict === "allow") return "ok";
  if (verdict === "review_required") return "warn";
  if (verdict === "approval_required") return "error";
  return "neutral";
}

function reasonLabelKey(reason?: string): string {
  switch (firstNonEmpty(reason)) {
    case "unchanged":
      return "权限未变化";
    case "permission_reduced":
      return "权限减少";
    case "permission_added":
      return "新增权限";
    case "sensitive_permission_added":
      return "新增敏感权限";
    case "secret_permission_added":
      return "新增密钥权限";
    default:
      return "未知原因";
  }
}

function sensitivityLabelKey(sensitivity: PluginPermissionSensitivity): string {
  switch (sensitivity) {
    case "public":
      return "公开";
    case "internal":
      return "内部";
    case "sensitive":
      return "敏感";
    case "secret":
      return "密钥";
    default:
      return "未知";
  }
}

function sensitivityTone(sensitivity: PluginPermissionSensitivity): PluginPermissionDiffTone {
  if (sensitivity === "public") return "neutral";
  if (sensitivity === "internal") return "ok";
  if (sensitivity === "sensitive") return "warn";
  if (sensitivity === "secret") return "error";
  return "neutral";
}

function trustLabelKey(verdict: string): string {
  switch (verdict) {
    case "trusted":
      return "可信";
    case "unsigned_allowed":
      return "允许未签名";
    case "review_required":
      return "需要复核";
    case "rejected":
      return "已拒绝";
    default:
      return "未验证";
  }
}

function trustTone(verdict: string): PluginPermissionDiffTone {
  if (verdict === "trusted" || verdict === "unsigned_allowed") return "ok";
  if (verdict === "review_required") return "warn";
  if (verdict === "rejected") return "error";
  return "neutral";
}

function compatibilityLabelKey(verdict: string): string {
  switch (verdict) {
    case "compatible":
      return "兼容";
    case "needs_review":
      return "需要复核";
    case "incompatible":
      return "不兼容";
    default:
      return "未知";
  }
}

function compatibilityTone(verdict: string): PluginPermissionDiffTone {
  if (verdict === "compatible") return "ok";
  if (verdict === "needs_review") return "warn";
  if (verdict === "incompatible") return "error";
  return "neutral";
}

function sensitivityRank(sensitivity: PluginPermissionSensitivity): number {
  if (sensitivity === "secret") return 3;
  if (sensitivity === "sensitive") return 2;
  if (sensitivity === "internal") return 1;
  return 0;
}

function nonNegativeInteger(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : fallback;
}

function positiveInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}

function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim() !== "") return value.trim();
  }
  return "";
}
