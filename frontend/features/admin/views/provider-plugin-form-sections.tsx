import { ExternalLink, Play } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { type AdminUIContribution, type ApiContext, type PluginActionDescriptor, type Provider, type ProviderResource } from "../core/types";
import { providerPluginOptionFieldKey, providerPluginOptionValuesForPlugin } from "../domain/provider-plugin-options";
import { tx } from "../i18n/runtime";
import { runProviderPluginActionEnvelope } from "../resources/provider-model-config";
import { ProviderInlineField } from "./provider-editor-fields";

type PluginFormInputType = "text" | "password" | "textarea" | "select" | "boolean";
type PluginFormActionType = "action_button" | "oauth_button";

type PluginFormBaseField = {
  name: string;
  label: string;
  options?: string[];
  placeholder?: string;
  required?: boolean;
  help?: string;
  defaultValue?: string;
  action?: string;
};

type PluginFormInputField = PluginFormBaseField & { type: PluginFormInputType };
type PluginFormActionField = PluginFormBaseField & { type: PluginFormActionType };
type PluginFormField = PluginFormInputField | PluginFormActionField;

type PluginFormActionState = {
  busy: boolean;
  error: string;
  result: string;
};

export function ProviderPluginFormSections({
  api,
  provider,
  resource,
  slot = "provider.form.section",
  providerType,
  resourceType,
  values,
  contributions,
  actions = [],
  onUpdate,
}: {
  api?: ApiContext;
  provider?: Provider;
  resource?: ProviderResource;
  slot?: "provider.form.section" | "provider.resource.form.section";
  providerType?: string;
  resourceType?: string;
  values: Record<string, string>;
  contributions: AdminUIContribution[];
  actions?: PluginActionDescriptor[];
  onUpdate: (key: string, value: string) => void;
}) {
  const [actionStates, setActionStates] = useState<Record<string, PluginFormActionState>>({});
  const sections = useMemo(
    () => contributions
      .filter((contribution) =>
        contribution.slot === slot &&
        pluginFormContributionMatches(contribution, providerType || values.type, resourceType || values.resource_type),
      )
      .map((contribution) => ({ contribution, fields: pluginFormFields(contribution) }))
      .filter((section) => section.fields.length > 0),
    [contributions, providerType, resourceType, slot, values.resource_type, values.type],
  );
  const actionDescriptors = useMemo(() => new Map(actions.map((action) => [pluginFormActionKey(action.plugin_id, action.action_id), action])), [actions]);
  useEffect(() => {
    for (const { contribution, fields } of sections) {
      for (const field of fields) {
        if (pluginFormFieldIsAction(field)) continue;
        const key = providerPluginOptionFieldKey(contribution.plugin_id, field.name);
        const existingValue = resource?.options?.[field.name] ?? provider?.options?.[field.name];
        const value = existingValue ?? field.defaultValue;
        if (values[key] === undefined && value !== undefined) onUpdate(key, value);
      }
    }
  }, [onUpdate, provider?.options, resource?.options, sections, values]);

  if (sections.length === 0) return null;

  function updateActionState(key: string, patch: Partial<PluginFormActionState>) {
    setActionStates((current) => ({
      ...current,
      [key]: { ...(current[key] ?? { busy: false, error: "", result: "" }), ...patch },
    }));
  }

  async function runFormAction(contribution: AdminUIContribution, field: PluginFormField) {
    if (!api) return;
    const actionID = field.action || contribution.action || "";
    const descriptor = actionDescriptors.get(pluginFormActionKey(contribution.plugin_id, actionID));
    if (!descriptor) return;
    const key = pluginFormFieldStateKey(contribution, field);
    updateActionState(key, { busy: true, error: "", result: "" });
    try {
      const result = await runProviderPluginActionEnvelope(api, descriptor, providerFormActionPayload(provider, resource, values, contribution.plugin_id, providerType, resourceType), field.label);
      const redirectURL = result.redirect_url || schemaString((result.data as Record<string, unknown> | undefined)?.auth_url);
      if (field.type === "oauth_button" && redirectURL) window.open(redirectURL, "_blank", "noopener,noreferrer");
      updateActionState(key, { busy: false, result: JSON.stringify(result.data ?? result.metadata ?? {}, null, 2) });
    } catch (err) {
      updateActionState(key, { busy: false, error: err instanceof Error ? err.message : tx("插件动作执行失败") });
    }
  }

  return (
    <>
      {sections.map(({ contribution, fields }) => (
        <section className="provider-edit-section" key={`${contribution.plugin_id}:${contribution.id}`}>
          <div className="wizard-panel-head">
            <h3>{contribution.title || contribution.id}</h3>
            <p>{tx("插件配置")}</p>
          </div>
          <div className="provider-form-grid">
            {fields.map((field) => {
              const stateKey = pluginFormFieldStateKey(contribution, field);
              if (pluginFormFieldIsAction(field)) {
                const actionID = field.action || contribution.action || "";
                return (
                  <ProviderPluginFormAction
                    busy={actionStates[stateKey]?.busy ?? false}
                    error={actionStates[stateKey]?.error ?? ""}
                    field={field}
                    key={field.name}
                    registered={Boolean(api && actionDescriptors.has(pluginFormActionKey(contribution.plugin_id, actionID)))}
                    result={actionStates[stateKey]?.result ?? ""}
                    onRun={() => void runFormAction(contribution, field)}
                  />
                );
              }
              const key = providerPluginOptionFieldKey(contribution.plugin_id, field.name);
              return (
                <ProviderInlineField
                  field={{ ...field, key }}
                  key={key}
                  onChange={(value) => onUpdate(key, value)}
                  value={values[key] ?? resource?.options?.[field.name] ?? provider?.options?.[field.name] ?? field.defaultValue ?? defaultPluginFieldValue(field)}
                  values={values}
                />
              );
            })}
          </div>
        </section>
      ))}
    </>
  );
}

function ProviderPluginFormAction({
  busy,
  error,
  field,
  registered,
  result,
  onRun,
}: {
  busy: boolean;
  error: string;
  field: PluginFormActionField;
  registered: boolean;
  result: string;
  onRun: () => void;
}) {
  const Icon = field.type === "oauth_button" ? ExternalLink : Play;
  return (
    <div className="field provider-plugin-action-field" data-field-key={field.name}>
      <span>{tx(field.label)}</span>
      <button className="secondary-button" disabled={busy || !registered} onClick={onRun} type="button">
        <Icon size={14} />
        {tx(busy ? "执行中" : field.label)}
      </button>
      {field.help ? <small>{tx(field.help)}</small> : null}
      {!registered ? <small>{tx("该插件动作尚未注册。")}</small> : null}
      {error ? <small className="provider-quota-error">{error}</small> : null}
      {result && result !== "{}" ? <pre className="plugin-action-result">{result}</pre> : null}
    </div>
  );
}

function pluginFormFields(contribution: AdminUIContribution): PluginFormField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    const name = schemaString(field.name);
    if (!name) return [];
    const type = pluginFormFieldType(schemaString(field.type));
    if (!type) return [];
    return [{
      name,
      type,
      label: schemaString(field.label) || name,
      options: schemaStringArray(field.options),
      placeholder: schemaString(field.placeholder),
      required: field.required === true,
      help: schemaString(field.help),
      defaultValue: schemaDefaultValue(field.default ?? field.default_value),
      action: schemaString(field.action),
    }];
  });
}

function pluginFormContributionMatches(contribution: AdminUIContribution, providerType: string, resourceType: string) {
  return (!contribution.provider_types?.length || contribution.provider_types.includes(providerType)) &&
    (!contribution.resource_types?.length || contribution.resource_types.includes(resourceType));
}

function pluginFormFieldType(type: string): PluginFormField["type"] | "" {
  switch (type) {
    case "":
    case "text":
    case "url":
      return "text";
    case "secret":
      return "password";
    case "select":
    case "segmented":
      return "select";
    case "switch":
      return "boolean";
    case "action_button":
    case "oauth_button":
      return type;
    default:
      return "";
  }
}

function defaultPluginFieldValue(field: PluginFormField) {
  return field.type === "boolean" ? "false" : "";
}

function pluginFormFieldIsAction(field: PluginFormField): field is PluginFormActionField {
  return field.type === "action_button" || field.type === "oauth_button";
}

function pluginFormFieldStateKey(contribution: AdminUIContribution, field: PluginFormField) {
  return `${contribution.plugin_id}:${contribution.id}:${field.name}`;
}

function pluginFormActionKey(pluginID: string, actionID: string) {
  return `${pluginID}:${actionID}`;
}

function providerFormActionPayload(provider: Provider | undefined, resource: ProviderResource | undefined, values: Record<string, string>, pluginID: string, explicitProviderType?: string, explicitResourceType?: string) {
  const providerID = provider?.id || values.provider_id || values.id || undefined;
  const effectiveProviderType = explicitProviderType || values.type || provider?.type || "";
  const effectiveResourceType = explicitResourceType || values.resource_type || resource?.resource_type || "";
  return {
    provider_id: providerID,
    provider_type: effectiveProviderType,
    resource_id: resource?.id || undefined,
    resource_type: effectiveResourceType,
    provider: {
      id: providerID || "",
      name: provider?.name || values.name || "",
      type: effectiveProviderType,
      base_url: provider?.base_url || (resource ? "" : values.base_url) || "",
    },
    resource: resource ? {
      id: resource.id,
      name: values.name || resource.name,
      resource_type: effectiveResourceType,
      base_url: values.base_url || resource.base_url || "",
      group: values.group || resource.group || "",
      region: values.region || resource.region || "",
      environment: values.environment || resource.environment || "",
    } : undefined,
    options: providerPluginOptionValuesForPlugin(values, pluginID),
  };
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaStringArray(value: unknown) {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : undefined;
}

function schemaDefaultValue(value: unknown) {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return undefined;
}
