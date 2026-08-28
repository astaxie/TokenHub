import { type PluginActionDescriptor, type PluginBackgroundJobDescriptor } from "../core/types";

export type PluginActionInputFieldType = "string" | "boolean" | "number" | "integer";

export type PluginActionInputField = {
  name: string;
  type: PluginActionInputFieldType;
  required: boolean;
};

type PluginInputSchemaHost = {
  input_schema?: Record<string, unknown>;
};

const supportedInputFieldTypes = new Set<PluginActionInputFieldType>(["string", "boolean", "number", "integer"]);

export function pluginActionKey(pluginID: string, actionID?: string) {
  return `${pluginID}:${actionID ?? ""}`;
}

export function pluginBackgroundJobKey(pluginID: string, jobID?: string) {
  return `${pluginID}:${jobID ?? ""}`;
}

export function pluginActionDeclared(action?: Pick<PluginActionDescriptor, "plugin_id" | "action_id"> | null) {
  return Boolean(action?.plugin_id?.trim() && action?.action_id?.trim());
}

export function pluginActionInputFields(target: PluginInputSchemaHost | PluginActionDescriptor | PluginBackgroundJobDescriptor | null | undefined) {
  const schema = target?.input_schema;
  if (!schema || typeof schema !== "object" || Array.isArray(schema) || schema.type !== "object") return [];
  const normalizedSchema = schema as Record<string, unknown>;
  const requiredValues = Array.isArray(normalizedSchema.required) ? normalizedSchema.required : [];
  const required = new Set(requiredValues.filter((value): value is string => typeof value === "string"));
  const properties = normalizedSchema.properties;
  if (!properties || typeof properties !== "object" || Array.isArray(properties)) return [];
  return Object.entries(properties).flatMap(([name, definition]) => {
    if (!definition || typeof definition !== "object" || Array.isArray(definition)) return [];
    const type = pluginActionInputFieldType((definition as { type?: unknown }).type);
    if (!type) return [];
    return [{ name, type, required: required.has(name) }];
  });
}

export function pluginActionInputDefaults(target: PluginInputSchemaHost | PluginActionDescriptor | PluginBackgroundJobDescriptor | null | undefined) {
  const values: Record<string, string | boolean> = {};
  for (const field of pluginActionInputFields(target)) {
    values[field.name] = field.type === "boolean" ? false : "";
  }
  return values;
}

export function pluginActionInputSchemaSupported(schema?: Record<string, unknown> | null) {
  if (!schema || typeof schema !== "object" || Array.isArray(schema)) return false;
  if (schema.type !== "object") return false;
  const properties = schema.properties;
  if (!properties || typeof properties !== "object" || Array.isArray(properties)) return true;
  return Object.values(properties).every((definition) => {
    if (!definition || typeof definition !== "object" || Array.isArray(definition)) return false;
    return pluginActionInputFieldType((definition as { type?: unknown }).type) !== "";
  });
}

export function pluginActionPayload(target: PluginInputSchemaHost | PluginActionDescriptor | PluginBackgroundJobDescriptor | null | undefined, values: Record<string, string | boolean>) {
  return pluginInputPayload(target, values);
}

export function pluginBackgroundJobPayload(target: PluginInputSchemaHost | PluginActionDescriptor | PluginBackgroundJobDescriptor | null | undefined, values: Record<string, string | boolean>) {
  return pluginInputPayload(target, values);
}

export function redactPluginActionResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactPluginActionResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) => {
    if (sensitivePluginResultKey(key)) return [key, "[redacted]"];
    return [key, redactPluginActionResult(child)];
  }));
}

function pluginInputPayload(target: PluginInputSchemaHost | PluginActionDescriptor | PluginBackgroundJobDescriptor | null | undefined, values: Record<string, string | boolean>) {
  const payload: Record<string, string | number | boolean> = {};
  for (const field of pluginActionInputFields(target)) {
    const value = values[field.name];
    if (field.type === "boolean") {
      payload[field.name] = Boolean(value);
      continue;
    }
    if (field.type === "number" || field.type === "integer") {
      if (value === "") continue;
      payload[field.name] = Number(value);
      continue;
    }
    if (typeof value === "string" && value !== "") {
      payload[field.name] = value;
    }
  }
  return payload;
}

function pluginActionInputFieldType(type: unknown): PluginActionInputFieldType | "" {
  if (typeof type !== "string") return "";
  return supportedInputFieldTypes.has(type as PluginActionInputFieldType) ? type as PluginActionInputFieldType : "";
}

function sensitivePluginResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized === "access_token" ||
    normalized === "refresh_token" ||
    normalized === "id_token" ||
    normalized.includes("secret") ||
    normalized === "credentials" ||
    normalized === "credential" ||
    normalized === "credential_blob" ||
    normalized === "api_key" ||
    normalized.endsWith("_api_key");
}
