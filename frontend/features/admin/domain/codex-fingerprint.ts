export const codexFingerprintModeOption = "codex_fingerprint_mode";

export type CodexFingerprintMode = "off" | "device" | "session" | "full";

export const defaultCodexFingerprintMode: CodexFingerprintMode = "session";

export function normalizeCodexFingerprintMode(value?: string): CodexFingerprintMode {
  return value === "off" || value === "device" || value === "session" || value === "full"
    ? value
    : defaultCodexFingerprintMode;
}

export function applyCodexFingerprintOption(options: Record<string, string>, values: Record<string, string>) {
  if (values.resource_type !== "openai_subscription") {
    delete options[codexFingerprintModeOption];
    return options;
  }
  const mode = normalizeCodexFingerprintMode(values[codexFingerprintModeOption]);
  if (mode === defaultCodexFingerprintMode) delete options[codexFingerprintModeOption];
  else options[codexFingerprintModeOption] = mode;
  return options;
}
