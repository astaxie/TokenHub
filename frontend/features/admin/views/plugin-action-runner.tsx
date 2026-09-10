import { Play } from "lucide-react";
import { type FormEvent, type ReactNode } from "react";
import { type PluginActionDescriptor, type PluginBackgroundJobDescriptor } from "../core/types";
import { pluginActionInputFields, pluginActionInputSchemaSupported, type PluginActionInputField } from "../domain/plugin-actions";

export type PluginInputRunnerDraft = {
  values: Record<string, string | boolean>;
  busy: boolean;
  error: string;
  result: string;
};

export type PluginInputRunnerLabels = {
  submit: ReactNode;
  submitting: ReactNode;
  unsupportedSchema: ReactNode;
};

type PluginInputRunnerDescriptor = {
  input_schema?: Record<string, unknown>;
};

type PluginInputRunnerProps<TDescriptor extends PluginInputRunnerDescriptor> = {
  descriptor: TDescriptor;
  draft: PluginInputRunnerDraft;
  onChange: (descriptor: TDescriptor, field: string, value: string | boolean) => void;
  onSubmit: (descriptor: TDescriptor, event: FormEvent<HTMLFormElement>) => void;
  labels: PluginInputRunnerLabels;
};

export function PluginActionRunner({
  action,
  draft,
  labels,
  onChange,
  onSubmit,
}: {
  action: PluginActionDescriptor;
  draft: PluginInputRunnerDraft;
  labels: PluginInputRunnerLabels;
  onChange: (action: PluginActionDescriptor, field: string, value: string | boolean) => void;
  onSubmit: (action: PluginActionDescriptor, event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <PluginInputRunner
      descriptor={action}
      draft={draft}
      labels={labels}
      onChange={onChange}
      onSubmit={onSubmit}
    />
  );
}

export function PluginBackgroundJobRunner({
  draft,
  job,
  labels,
  onChange,
  onSubmit,
}: {
  job: PluginBackgroundJobDescriptor;
  draft: PluginInputRunnerDraft;
  labels: PluginInputRunnerLabels;
  onChange: (job: PluginBackgroundJobDescriptor, field: string, value: string | boolean) => void;
  onSubmit: (job: PluginBackgroundJobDescriptor, event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <PluginInputRunner
      descriptor={job}
      draft={draft}
      labels={labels}
      onChange={onChange}
      onSubmit={onSubmit}
    />
  );
}

function PluginInputRunner<TDescriptor extends PluginInputRunnerDescriptor>({
  descriptor,
  draft,
  labels,
  onChange,
  onSubmit,
}: PluginInputRunnerProps<TDescriptor>) {
  const fields = pluginActionInputFields(descriptor);
  const unsupported = descriptor.input_schema && !pluginActionInputSchemaSupported(descriptor.input_schema);
  return (
    <form className="plugin-action-runner" onSubmit={(event) => onSubmit(descriptor, event)}>
      {unsupported ? <p className="empty-state">{labels.unsupportedSchema}</p> : null}
      {fields.map((field) => (
        <PluginInputFieldView
          draft={draft}
          field={field}
          key={field.name}
          onChange={(name, value) => onChange(descriptor, name, value)}
        />
      ))}
      <button className="secondary-button plugin-action-button" disabled={draft.busy || Boolean(unsupported)} type="submit">
        <Play size={14} />
        <span>{draft.busy ? labels.submitting : labels.submit}</span>
      </button>
      {draft.error ? <p className="provider-quota-error">{draft.error}</p> : null}
      {draft.result ? <pre className="plugin-action-result">{draft.result}</pre> : null}
    </form>
  );
}

function PluginInputFieldView({
  draft,
  field,
  onChange,
}: {
  draft: PluginInputRunnerDraft;
  field: PluginActionInputField;
  onChange: (field: string, value: string | boolean) => void;
}) {
  return (
    <label className="plugin-action-field">
      <span>{field.name}{field.required ? " *" : ""}</span>
      {field.type === "boolean" ? (
        <input
          checked={Boolean(draft.values[field.name])}
          onChange={(event) => onChange(field.name, event.currentTarget.checked)}
          type="checkbox"
        />
      ) : (
        <input
          onChange={(event) => onChange(field.name, event.currentTarget.value)}
          required={field.required}
          type={field.type === "number" || field.type === "integer" ? "number" : "text"}
          value={String(draft.values[field.name] ?? "")}
        />
      )}
    </label>
  );
}
