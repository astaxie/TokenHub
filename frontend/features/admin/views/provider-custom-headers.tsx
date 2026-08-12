import { useMemo } from "react";
import {
  effectiveProviderHeaderEntries,
  parseProviderHeaderEntries,
  providerHeaderEntryErrors,
  providerHeaderMask,
  serializeProviderHeaderEntries,
  type ProviderHeaderEntry,
} from "../domain/provider-headers";
import { tx } from "../i18n/runtime";

export function ProviderCustomHeaders({
  value,
  onChange,
  inheritedValue = "",
  disabled = false,
  validationErrors = [],
}: {
  value: string;
  onChange: (value: string) => void;
  inheritedValue?: string;
  disabled?: boolean;
  validationErrors?: string[];
}) {
  const entries = useMemo(() => parseProviderHeaderEntries(value), [value]);
  const inherited = useMemo(() => parseProviderHeaderEntries(inheritedValue), [inheritedValue]);
  const effective = useMemo(() => effectiveProviderHeaderEntries(inherited, entries), [entries, inherited]);
  const errors = Array.from(new Set([...providerHeaderEntryErrors(entries), ...providerHeaderEntryErrors(effective)]));
  const locked = disabled && validationErrors.length === 0;

  function changeEntry(index: number, patch: Partial<ProviderHeaderEntry>) {
    onChange(serializeProviderHeaderEntries(entries.map((entry, current) => current === index ? { ...entry, ...patch, ...(patch.name !== undefined && patch.name !== entry.name ? { retained: false } : {}) } : entry)));
  }

  function removeEntry(index: number) {
    onChange(serializeProviderHeaderEntries(entries.filter((_, current) => current !== index)));
  }

  function addEntry() {
    onChange(serializeProviderHeaderEntries([...entries, { name: "", value: "", sensitive: false }]));
  }

  return (
    <div className="provider-custom-headers">
      <div className="provider-custom-headers-head">
        <div>
          <strong>{tx("自定义请求头")}</strong>
          <span>{tx("仅发送管理员保存的固定请求头；Resource 的同名项覆盖 Provider 默认值。")}</span>
        </div>
        <button className="secondary-button" disabled={disabled} onClick={addEntry} type="button">{tx("新增请求头")}</button>
      </div>
      {validationErrors.length > 0 ? <p className="provider-custom-header-errors" role="alert">{tx("已保存的请求头配置不合规，修正或删除后才能生效。")}</p> : null}
      {disabled ? <p className="provider-credential-note">{tx("此适配器管理自己的客户端身份，暂不支持自定义请求头。")}</p> : null}
      {entries.length === 0 ? <p className="provider-custom-headers-empty">{tx("尚未配置自定义请求头。")}</p> : (
        <div className="provider-custom-header-list">
          {entries.map((entry, index) => (
            <div className="provider-custom-header-row" key={`${index}-${entry.name}`}>
              <label className="field">
                <span>{tx("请求头名称")}</span>
                <input disabled={locked} placeholder="User-Agent" value={entry.name} onChange={(event) => changeEntry(index, { name: event.target.value })} />
              </label>
              <label className="field">
                <span>{tx("请求头值")}</span>
                <input
                  autoComplete="new-password"
                  disabled={locked}
                  placeholder={entry.sensitive ? providerHeaderMask : "TokenHub-Custom-Client/1.0"}
                  type={entry.sensitive ? "password" : "text"}
                  value={entry.value}
                  onChange={(event) => changeEntry(index, { value: event.target.value })}
                />
              </label>
              <label className="provider-custom-header-sensitive">
                <input checked={entry.sensitive} disabled={locked} type="checkbox" onChange={(event) => changeEntry(index, { sensitive: event.target.checked })} />
                <span>{tx("敏感值")}</span>
              </label>
              <button className="secondary-button danger" disabled={locked} onClick={() => removeEntry(index)} type="button">{tx("删除")}</button>
            </div>
          ))}
        </div>
      )}
      {errors.length > 0 ? <div className="provider-custom-header-errors" role="alert">{errors.map((error) => <span key={error}>{tx(error)}</span>)}</div> : null}
      {effective.length > 0 ? (
        <details className="provider-custom-header-preview">
          <summary>{tx("最终请求头预览")}</summary>
          <div>
            {effective.map((entry) => (
              <code key={entry.name}>{entry.name}: {entry.sensitive ? providerHeaderMask : entry.value}</code>
            ))}
          </div>
        </details>
      ) : null}
      <small>{tx("Authorization、API Key、Content-Type、Host 和逐跳传输头由 TokenHub 管理，不能覆盖。敏感值保存后仅显示遮盖；留空保留，删除整行才会清除。")}</small>
    </div>
  );
}
