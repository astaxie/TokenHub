import { Boxes, KeyRound, Search, Server, UserRoundCheck } from "lucide-react";
import { type FieldConfig, type ProviderCredentialMode } from "../core/types";
import { enumOptionLabel } from "../domain/labels";
import { clearCustomValidity, handleRequiredFieldInvalid, tx } from "../i18n/runtime";

export function ProviderInlineField({
  field,
  value,
  values,
  onChange,
}: {
  field: FieldConfig;
  value: string;
  values: Record<string, string>;
  onChange: (value: string) => void;
}) {
  if (!(field.visible?.(values) ?? true)) return null;
  const autoComplete = field.autoComplete ?? "off";
  const inputName = `tokenhub-provider-account-${field.key}`;
  const options = (field.options ?? []).map((option) => ({ value: option, label: enumOptionLabel(field.key, option) }));
  if (field.type === "select") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <select value={value} onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }} onInvalid={handleRequiredFieldInvalid} required={field.required}>
          <option value="">{tx("请选择")}</option>
          {options.map((option) => (
            <option key={option.value} value={option.value}>{tx(option.label)}</option>
          ))}
        </select>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  if (field.type === "textarea") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <textarea
          autoComplete={autoComplete}
          data-1p-ignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
          data-lpignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
          name={inputName}
          value={value}
          onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }}
          onInvalid={handleRequiredFieldInvalid}
          placeholder={tx(field.placeholder)}
          required={field.required}
        />
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  if (field.type === "boolean") {
    const checked = value === "true";
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <div className="boolean-toggle" role="radiogroup" aria-label={tx(field.label)}>
          <button aria-checked={checked} className={checked ? "active" : ""} onClick={() => onChange("true")} role="radio" type="button">
            {tx("开启")}
          </button>
          <button aria-checked={!checked} className={!checked ? "active" : ""} onClick={() => onChange("false")} role="radio" type="button">
            {tx("关闭开关")}
          </button>
        </div>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  return (
    <label className="field" data-field-key={field.key}>
      <span>{tx(field.label)}</span>
      <input
        autoComplete={autoComplete}
        data-1p-ignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
        data-lpignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
        name={inputName}
        value={value}
        type={field.type === "number" ? "number" : field.type === "password" ? "password" : "text"}
        onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }}
        onInvalid={handleRequiredFieldInvalid}
        placeholder={tx(field.placeholder)}
        required={field.required}
      />
      {field.help ? <small>{tx(field.help)}</small> : null}
    </label>
  );
}

export function providerCredentialOptions(): Array<{ key: ProviderCredentialMode; label: string; description: string; icon: typeof KeyRound }> {
  return [
    {
      key: "provider_api_key",
      label: "直接 API Key",
      description: "把上游 Key 保存到 Provider，适合单账号或兼容 API。",
      icon: KeyRound,
    },
    {
      key: "account_integration",
      label: "账号资源池",
      description: "适合 OpenAI 账号、Subscription 或多账号轮询，默认通道会自动推荐。",
      icon: UserRoundCheck,
    },
  ];
}

export function providerCreateWizardSteps(credentialMode: ProviderCredentialMode): Array<{ title: string; icon: typeof Search }> {
  if (credentialMode === "provider_api_key") {
    return [
      { title: "接入方式", icon: UserRoundCheck },
      { title: "渠道与 API Key", icon: KeyRound },
    ];
  }
  return [
    { title: "接入方式", icon: UserRoundCheck },
    { title: "渠道信息", icon: Server },
    { title: "账号与凭据", icon: KeyRound },
    { title: "模型库存与确认", icon: Boxes },
  ];
}

export function providerCreateWizardStepTitle(title: string, credentialMode: ProviderCredentialMode) {
  if (credentialMode === "account_integration" && title === "渠道信息") return "默认通道";
  if (title === "账号与凭据") {
    if (credentialMode === "account_integration") return "账号授权";
    return "直接 API Key";
  }
  return title;
}

export function providerCredentialModeLabel(mode: ProviderCredentialMode) {
  return providerCredentialOptions().find((option) => option.key === mode)?.label ?? mode;
}

export function providerAccountResourceReady(values: Record<string, string>) {
  if (values.resource_type === "openai_subscription") {
    return Boolean(values.access_token?.trim() || values.refresh_token?.trim() || values.id_token?.trim());
  }
  return Boolean(values.api_key?.trim());
}
