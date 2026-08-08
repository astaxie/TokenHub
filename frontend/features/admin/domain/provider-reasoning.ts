import { type FieldConfig } from "../core/types";
import { tx } from "../i18n/runtime";
import {
  providerHasReasoningOverrides,
  providerReasoningFormValues,
  providerReasoningOverrideFormValues,
  serializeProviderReasoningOptions,
} from "./provider-reasoning-options";

export { providerHasReasoningOverrides, providerReasoningFormValues, providerReasoningOverrideFormValues };

const anthropicReasoningProviderTypes = new Set([
  "openai",
  "openai_compatible",
  "azure_openai",
  "deepseek",
  "qwen",
  "local",
]);

export function providerSupportsAnthropicReasoning(providerType?: string) {
  return anthropicReasoningProviderTypes.has(providerType?.trim().toLowerCase() ?? "");
}

export function providerReasoningFieldConfigs(visible?: FieldConfig["visible"]): FieldConfig[] {
  return [
    {
      key: "reasoning_effort_values",
      label: "推理强度允许值",
      placeholder: "none, low, medium, high, max",
      help: "填写上游实际接受的 reasoning_effort 值，逗号分隔；留空表示不限制。",
      visible,
    },
    {
      key: "reasoning_effort_map",
      label: "推理强度映射",
      type: "textarea",
      placeholder: '{"minimal":"low","xhigh":"max"}',
      help: "把 Claude Code 的推理强度转换为上游值；填写 JSON 对象。",
      visible,
    },
    {
      key: "reasoning_effort_unsupported",
      label: "不支持值处理",
      type: "select",
      options: ["passthrough", "omit", "reject"],
      help: "透传原值、删除参数，或在调用上游前返回明确错误。",
      visible,
    },
    {
      key: "reasoning_budget_map",
      label: "推理预算映射",
      type: "textarea",
      placeholder: '{"2048":"low","8192":"medium","16384":"high","*":"max"}',
      help: "按 thinking.budget_tokens 的最大 Token 数映射推理强度；* 表示兜底值。",
      visible,
    },
    {
      key: "preserve_reasoning_content",
      label: "回传推理内容",
      type: "boolean",
      help: "仅在上游支持后续 assistant 消息接收 reasoning_content 时开启。",
      visible,
    },
  ];
}

export function providerReasoningOptions(values: Record<string, string>, base?: Record<string, string>) {
  return serializeProviderReasoningOptions(
    values,
    base,
    (label) => tx(`${label}必须是键值均为字符串的 JSON 对象。`),
  );
}
