import { providerTypeLabel } from "../domain/labels";
import { providerReasoningFieldConfigs, providerSupportsAnthropicReasoning } from "../domain/provider-reasoning";
import { tx } from "../i18n/runtime";
import { providerTypeOptions } from "../shared/ui";
import { ProviderInlineField } from "./provider-editor-fields";

export { providerReasoningFormValues } from "../domain/provider-reasoning";

type ProviderEditSectionProps = {
  values: Record<string, string>;
  onUpdate: (key: string, value: string) => void;
};

export function ProviderConnectionFields({ values, onUpdate }: ProviderEditSectionProps) {
  return (
    <section className="provider-edit-section">
      <div className="provider-form-grid provider-connect-form-grid">
        <label className="field">
          <span>Base URL</span>
          <input value={values.base_url ?? ""} onChange={(event) => onUpdate("base_url", event.target.value)} />
        </label>
        <label className="field">
          <span>API Key</span>
          <input
            autoComplete="new-password"
            value={values.api_key ?? ""}
            type="password"
            onChange={(event) => onUpdate("api_key", event.target.value)}
          />
          <small>{tx("留空表示不修改现有 Key；填写新值才会覆盖。")}</small>
        </label>
      </div>
    </section>
  );
}

export function ProviderAdvancedFields({
  values,
  onUpdate,
  accountIntegration,
}: ProviderEditSectionProps & { accountIntegration: boolean }) {
  const showReasoningCompatibility = providerSupportsAnthropicReasoning(values.type);
  return (
    <section className="provider-edit-section">
      <div className="provider-form-grid">
        <label className="field">
          <span>Provider ID</span>
          <input value={values.id ?? ""} readOnly />
        </label>
        <label className="field">
          <span>{tx(accountIntegration ? "通道名称" : "渠道名称")}</span>
          <input value={values.name ?? ""} onChange={(event) => onUpdate("name", event.target.value)} required />
        </label>
        <label className="field">
          <span>{tx(accountIntegration ? "兼容协议" : "渠道商类型")}</span>
          <select value={values.type ?? ""} onChange={(event) => onUpdate("type", event.target.value)} required>
            {providerTypeOptions.map((option) => <option key={option} value={option}>{providerTypeLabel(option)}</option>)}
          </select>
        </label>
        <label className="field">
          <span>{tx("优先级")}</span>
          <input value={values.priority ?? "10"} type="number" onChange={(event) => onUpdate("priority", event.target.value)} />
        </label>
      </div>
      {showReasoningCompatibility ? <details className="provider-account-runtime">
        <summary>
          <strong>{tx("Anthropic 推理参数兼容")}</strong>
          <span>{tx("Provider 默认规则，适用于 Claude Code 等 Anthropic Messages 客户端。")}</span>
        </summary>
        <div className="provider-account-fields">
          {providerReasoningFieldConfigs().map((field) => (
            <ProviderInlineField
              key={field.key}
              field={field}
              value={values[field.key] ?? ""}
              values={values}
              onChange={(value) => onUpdate(field.key, value)}
            />
          ))}
        </div>
      </details> : null}
    </section>
  );
}
