import { useState } from "react";
import { type ApiContext, type ProviderResource } from "../core/types";
import { providerTypeLabel } from "../domain/labels";
import { providerReasoningFieldConfigs, providerSupportsAnthropicReasoning } from "../domain/provider-reasoning";
import { tx } from "../i18n/runtime";
import { adminFetch, providerResourceAttributionPolicyPayload, readAdminError } from "../resources/payloads";
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
  creating = false,
  idPlaceholder,
}: ProviderEditSectionProps & { accountIntegration: boolean; creating?: boolean; idPlaceholder?: string }) {
  const showReasoningCompatibility = providerSupportsAnthropicReasoning(values.type);
  return (
    <section className="provider-edit-section">
      <div className="provider-form-grid">
        <label className="field">
          <span>Provider ID</span>
          <input
            value={values.id ?? ""}
            readOnly={!creating}
            placeholder={idPlaceholder}
            onChange={creating ? (event) => onUpdate("id", event.target.value) : undefined}
          />
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
        {creating ? (
          <label className="field">
            <span>Base URL</span>
            <input value={values.base_url ?? ""} onChange={(event) => onUpdate("base_url", event.target.value)} />
          </label>
        ) : null}
        <label className="field">
          <span>{tx("优先级")}</span>
          <input value={values.priority ?? "10"} type="number" onChange={(event) => onUpdate("priority", event.target.value)} />
        </label>
        <label className="field">
          <span>{tx("Claude Code 归因块")}</span>
          <select value={values.claude_code_attribution_policy ?? "preserve"} onChange={(event) => onUpdate("claude_code_attribution_policy", event.target.value)}>
            <option value="preserve">{tx("保留归因块")}</option>
            <option value="strip">{tx("移除归因块")}</option>
          </select>
          <small>{tx("Anthropic 官方默认保留；明确非官方 Provider 默认移除。自定义且来源不明的 Anthropic 端点默认保留。")}</small>
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

export function ProviderResourceAttributionFields({
  api,
  providerID,
  resources,
  onSaved,
}: {
  api: ApiContext;
  providerID: string;
  resources: ProviderResource[];
  onSaved: () => Promise<void>;
}) {
  const [busyID, setBusyID] = useState("");
  const [error, setError] = useState("");
  const providerResources = resources.filter((resource) => resource.provider_id === providerID);
  if (providerResources.length === 0) return null;

  async function updatePolicy(resource: ProviderResource, policy: string) {
    setBusyID(resource.id);
    setError("");
    try {
      const resp = await adminFetch(api, `/api/admin/provider-resources/${encodeURIComponent(resource.id)}`, {
        method: "PATCH",
        body: JSON.stringify(providerResourceAttributionPolicyPayload(resource, policy)),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("更新 Claude Code 归因策略")));
      await onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("更新 Claude Code 归因策略失败"));
    } finally {
      setBusyID("");
    }
  }

  return (
    <section className="provider-edit-section">
      <div className="wizard-panel-head">
        <h3>{tx("Provider Resource 归因策略")}</h3>
        <p>{tx("每个 Resource 默认继承 Provider 策略，也可以独立覆盖。故障切换时会按实际尝试的 Resource 处理请求。")}</p>
      </div>
      <div className="provider-form-grid">
        {providerResources.map((resource) => (
          <label className="field" key={resource.id}>
            <span>{resource.name}</span>
            <select
              disabled={busyID === resource.id}
              value={resource.options?.claude_code_attribution_policy ?? "inherit"}
              onChange={(event) => void updatePolicy(resource, event.target.value)}
            >
              <option value="inherit">{tx("继承 Provider 策略")}</option>
              <option value="preserve">{tx("保留归因块")}</option>
              <option value="strip">{tx("移除归因块")}</option>
            </select>
          </label>
        ))}
      </div>
      {error ? <p className="provider-quota-error" role="alert">{error}</p> : null}
    </section>
  );
}
