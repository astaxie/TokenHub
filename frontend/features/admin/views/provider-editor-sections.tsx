import { providerTypeLabel } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { providerTypeOptions } from "../shared/ui";

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

export function AnthropicAuthTypeField({ values, onUpdate }: ProviderEditSectionProps) {
  if (values.type !== "anthropic") return null;
  return (
    <label className="field">
      <span>{tx("Anthropic 认证方式")}</span>
      <select value={values.anthropic_auth_type || "x-api-key"} onChange={(event) => onUpdate("anthropic_auth_type", event.target.value)}>
        <option value="x-api-key">{tx("x-api-key（Anthropic 官方）")}</option>
        <option value="bearer">{tx("Authorization Bearer（兼容服务）")}</option>
      </select>
      <small>{tx("认证密钥始终使用上面的加密 API Key，不需要写入自定义 Headers。")}</small>
    </label>
  );
}

export function ProviderAdvancedFields({
  values,
  onUpdate,
  accountIntegration,
}: ProviderEditSectionProps & { accountIntegration: boolean }) {
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
        <AnthropicAuthTypeField values={values} onUpdate={onUpdate} />
        <label className="field">
          <span>{tx("优先级")}</span>
          <input value={values.priority ?? "10"} type="number" onChange={(event) => onUpdate("priority", event.target.value)} />
        </label>
      </div>
    </section>
  );
}
