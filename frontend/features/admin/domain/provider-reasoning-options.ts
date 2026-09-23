export const reasoningOptionKeys = [
  "reasoning_effort_values",
  "reasoning_effort_map",
  "reasoning_effort_unsupported",
  "reasoning_budget_map",
  "preserve_reasoning_content",
] as const;

export function providerHasReasoningOverrides(options?: Record<string, string>) {
  return reasoningOptionKeys.some((key) => Object.prototype.hasOwnProperty.call(options ?? {}, key));
}

export function providerReasoningFormValues(options?: Record<string, string>) {
  const source = options ?? {};
  return {
    _existing_options: JSON.stringify(source),
    reasoning_effort_values: source.reasoning_effort_values ?? "",
    reasoning_effort_map: source.reasoning_effort_map ?? "",
    reasoning_effort_unsupported: source.reasoning_effort_unsupported || "omit",
    reasoning_budget_map: source.reasoning_budget_map ?? "",
    preserve_reasoning_content: source.preserve_reasoning_content === "true" ? "true" : "false",
  };
}

export function providerReasoningOverrideFormValues(
  resourceOptions?: Record<string, string>,
  providerOptions?: Record<string, string>,
) {
  const overridden = providerHasReasoningOverrides(resourceOptions);
  const displayed = overridden
    ? { ...(providerOptions ?? {}), ...(resourceOptions ?? {}) }
    : providerOptions;
  return {
    ...providerReasoningFormValues(displayed),
    _existing_options: JSON.stringify(resourceOptions ?? {}),
    _reasoning_override: overridden ? "true" : "false",
  };
}

export function serializeProviderReasoningOptions(
  values: Record<string, string>,
  base?: Record<string, string>,
  invalidJSONObjectMessage = (label: string) => `${label}必须是键值均为字符串的 JSON 对象。`,
) {
  const options = { ...readExistingOptions(values._existing_options), ...base };
  for (const key of reasoningOptionKeys) delete options[key];

  if (values._reasoning_override === "false") return options;

  const allowed = values.reasoning_effort_values
    ?.split(",")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean)
    .join(",");
  if (allowed || values._reasoning_override === "true") options.reasoning_effort_values = allowed;

  const effortMap = normalizedJSONObject(values.reasoning_effort_map, "推理强度映射", invalidJSONObjectMessage);
  if (effortMap || values._reasoning_override === "true") options.reasoning_effort_map = effortMap;

  const unsupported = values.reasoning_effort_unsupported?.trim().toLowerCase();
  if (unsupported && (unsupported !== "omit" || values._reasoning_override === "true")) options.reasoning_effort_unsupported = unsupported;

  const budgetMap = normalizedJSONObject(values.reasoning_budget_map, "推理预算映射", invalidJSONObjectMessage);
  if (budgetMap || values._reasoning_override === "true") options.reasoning_budget_map = budgetMap;

  if (values.preserve_reasoning_content === "true" || values._reasoning_override === "true") {
    options.preserve_reasoning_content = values.preserve_reasoning_content === "true" ? "true" : "false";
  }
  return options;
}

function readExistingOptions(raw?: string) {
  if (!raw?.trim()) return {};
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, string> : {};
  } catch {
    return {};
  }
}

function normalizedJSONObject(
  raw: string | undefined,
  label: string,
  invalidJSONObjectMessage: (label: string) => string,
) {
  if (!raw?.trim()) return "";
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) || Object.values(parsed).some((value) => typeof value !== "string")) throw new Error();
    return JSON.stringify(parsed);
  } catch {
    throw new Error(invalidJSONObjectMessage(label));
  }
}
