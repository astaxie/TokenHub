import { useEffect, useMemo } from "react";
import { type AdminUIContribution, type Provider } from "../core/types";
import { providerPluginOptionFieldKey } from "../domain/provider-plugin-options";
import { tx } from "../i18n/runtime";
import { ProviderInlineField } from "./provider-editor-fields";

type PluginFormField = {
  name: string;
  type: "text" | "password" | "textarea" | "select" | "boolean";
  label: string;
  options?: string[];
  placeholder?: string;
  required?: boolean;
  help?: string;
};

export function ProviderPluginFormSections({
  provider,
  values,
  contributions,
  onUpdate,
}: {
  provider?: Provider;
  values: Record<string, string>;
  contributions: AdminUIContribution[];
  onUpdate: (key: string, value: string) => void;
}) {
  const sections = useMemo(
    () => contributions
      .filter((contribution) =>
        contribution.slot === "provider.form.section" &&
        (contribution.provider_types?.length ? contribution.provider_types.includes(values.type) : true),
      )
      .map((contribution) => ({ contribution, fields: pluginFormFields(contribution) }))
      .filter((section) => section.fields.length > 0),
    [contributions, values.type],
  );
  useEffect(() => {
    if (!provider?.options) return;
    for (const { contribution, fields } of sections) {
      for (const field of fields) {
        const key = providerPluginOptionFieldKey(contribution.plugin_id, field.name);
        const value = provider.options[field.name];
        if (values[key] === undefined && value !== undefined) onUpdate(key, value);
      }
    }
  }, [onUpdate, provider?.options, sections, values]);

  if (sections.length === 0) return null;

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
              const key = providerPluginOptionFieldKey(contribution.plugin_id, field.name);
              return (
                <ProviderInlineField
                  field={{ ...field, key }}
                  key={key}
                  onChange={(value) => onUpdate(key, value)}
                  value={values[key] ?? provider?.options?.[field.name] ?? defaultPluginFieldValue(field)}
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
    }];
  });
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
    default:
      return "";
  }
}

function defaultPluginFieldValue(field: PluginFormField) {
  return field.type === "boolean" ? "false" : "";
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaStringArray(value: unknown) {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : undefined;
}
