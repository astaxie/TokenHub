import { useEffect, useMemo, useState } from "react";
import type { ApiContext, Provider, ProviderResource } from "../core/types";
import { providerReasoningFieldConfigs, providerReasoningOverrideFormValues, providerSupportsAnthropicReasoning } from "../domain/provider-reasoning";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, providerResourceToForm, providerResourceUpdatePayload, readAdminError } from "../resources/payloads";
import { ProviderInlineField } from "./provider-editor-fields";

export function ProviderResourceReasoningSettings({
  api,
  provider,
  providerType,
  resources,
  onSaved,
}: {
  api: ApiContext;
  provider: Provider;
  providerType: string;
  resources: ProviderResource[];
  onSaved: () => Promise<void> | void;
}) {
  const scopedResources = useMemo(
    () => resources.filter((resource) => resource.provider_id === provider.id && resource.resource_type !== "openai_subscription"),
    [provider.id, resources],
  );
  const [drafts, setDrafts] = useState<Record<string, Record<string, string>>>({});
  const [busyID, setBusyID] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [savedID, setSavedID] = useState("");

  useEffect(() => {
    setDrafts((current) => Object.fromEntries(scopedResources.map((resource) => [
      resource.id,
      current[resource.id] ?? providerReasoningOverrideFormValues(resource.options, provider.options),
    ])));
  }, [provider.options, scopedResources]);

  function update(resourceID: string, key: string, value: string) {
    setDrafts((current) => ({
      ...current,
      [resourceID]: { ...current[resourceID], [key]: value },
    }));
    setErrors((current) => ({ ...current, [resourceID]: "" }));
    setSavedID("");
  }

  async function save(resource: ProviderResource) {
    const draft = drafts[resource.id] ?? providerReasoningOverrideFormValues(resource.options, provider.options);
    setBusyID(resource.id);
    setSavedID("");
    setErrors((current) => ({ ...current, [resource.id]: "" }));
    try {
      const payload = providerResourceUpdatePayload({ ...providerResourceToForm(resource, provider.options), ...draft });
      const response = await adminFetch(api, `/api/admin/provider-resources/${encodeURIComponent(resource.id)}`, {
        method: "PATCH",
        body: JSON.stringify(payload),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("保存 Provider 资源推理兼容设置")));
      const updated = await response.json() as ProviderResource;
      setDrafts((current) => ({ ...current, [resource.id]: providerReasoningOverrideFormValues(updated.options, provider.options) }));
      setSavedID(resource.id);
      await onSaved();
    } catch (error) {
      if (isAuthExpiredError(error)) return;
      setErrors((current) => ({ ...current, [resource.id]: error instanceof Error ? error.message : tx("保存失败") }));
    } finally {
      setBusyID("");
    }
  }

  if (!providerSupportsAnthropicReasoning(providerType) || scopedResources.length === 0) return null;
  return (
    <section className="provider-quota-panel">
      <div className="wizard-panel-head">
        <h3>{tx("资源级推理参数覆盖")}</h3>
        <p>{tx("资源默认继承 Provider；仅当该上游端点规则不同时启用覆盖。")}</p>
      </div>
      <div className="provider-quota-list">
        {scopedResources.map((resource) => {
          const values = drafts[resource.id] ?? providerReasoningOverrideFormValues(resource.options, provider.options);
          const overridden = values._reasoning_override === "true";
          return (
            <details className="provider-account-runtime" key={resource.id} data-resource-id={resource.id}>
              <summary>
                <strong>{resource.name}</strong>
                <span>{resource.resource_type} · {resource.base_url || tx("继承 Provider Base URL")} · {tx(overridden ? "自定义覆盖" : "继承 Provider")}</span>
              </summary>
              <div className="provider-reasoning-scope">
                <span>{tx("配置来源")}</span>
                <div className="boolean-toggle" role="radiogroup" aria-label={tx("配置来源")}>
                  <button aria-checked={!overridden} className={!overridden ? "active" : ""} onClick={() => update(resource.id, "_reasoning_override", "false")} role="radio" type="button">
                    {tx("继承 Provider")}
                  </button>
                  <button aria-checked={overridden} className={overridden ? "active" : ""} onClick={() => update(resource.id, "_reasoning_override", "true")} role="radio" type="button">
                    {tx("自定义覆盖")}
                  </button>
                </div>
                <small>{tx(overridden ? "保存后，该资源使用下面的完整规则覆盖 Provider 默认规则。" : "当前使用 Provider 默认规则；保存后会清除已有资源级覆盖。")}</small>
              </div>
              {overridden ? (
                <div className="provider-account-fields">
                  {providerReasoningFieldConfigs().map((field) => (
                    <ProviderInlineField
                      field={field}
                      key={`${resource.id}-${field.key}`}
                      onChange={(value) => update(resource.id, field.key, value)}
                      value={values[field.key] ?? ""}
                      values={values}
                    />
                  ))}
                </div>
              ) : null}
              {errors[resource.id] ? <p className="provider-quota-error">{errors[resource.id]}</p> : null}
              {savedID === resource.id ? <p className="provider-credential-note">{tx("Provider 资源推理兼容设置已保存。")}</p> : null}
              <div className="provider-form-actions">
                <button className="secondary-button" disabled={busyID === resource.id} onClick={() => void save(resource)} type="button">
                  {tx(busyID === resource.id ? "保存中" : "保存资源设置")}
                </button>
              </div>
            </details>
          );
        })}
      </div>
    </section>
  );
}
