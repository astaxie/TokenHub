import { Check, Copy, KeyRound } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import Select, { type MultiValue } from "react-select";
import { type AdminUser, type AppData, type FieldConfig, type Model } from "../core/types";
import { modelCategory, modelCategoryLabel } from "../domain/catalog";
import { copyText } from "../domain/clipboard";
import { modelDisplayName } from "../domain/model-display-name";
import { findProvider, modelRoutesFor } from "../domain/entities";
import { compactNumber, routeStrategyLabel } from "../domain/formatting";
import { enumOptionLabel, enumValueLabel, splitList } from "../domain/labels";
import { activeLanguage, clearCustomValidity, handleRequiredFieldInvalid, selectedModelsText, selectedOptionsText, translatedCell, tx } from "../i18n/runtime";
import { PaginationControls, usePagination } from "./pagination";

function HiddenSelectIndicator() {
  return null;
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel = "删除",
  confirmClassName = "danger-confirm",
  loading,
  onCancel,
  onConfirm,
}: {
  title: string;
  message: string;
  confirmLabel?: string;
  confirmClassName?: string;
  loading: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="modal-backdrop" role="presentation">
      <div className="confirm-modal" role="dialog" aria-modal="true">
        <h2>{tx(title)}</h2>
        <p>{tx(message)}</p>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onCancel} type="button">{tx("取消")}</button>
          <button className={confirmClassName} onClick={onConfirm} disabled={loading} type="button">{tx(confirmLabel)}</button>
        </div>
      </div>
    </div>
  );
}

export function IssuedKeyModal({ value, onClose }: { value: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const [closeCountdown, setCloseCountdown] = useState(3);

  useEffect(() => {
    if (closeCountdown <= 0) return;
    const timer = window.setTimeout(() => setCloseCountdown((current) => Math.max(current - 1, 0)), 1000);
    return () => window.clearTimeout(timer);
  }, [closeCountdown]);

  async function copyKey() {
    setCopied(await copyText(value));
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <div className="confirm-modal issued-key-modal" role="dialog" aria-modal="true" aria-labelledby="issued-key-title">
        <div className="issued-key-icon" aria-hidden="true">
          <KeyRound size={18} />
        </div>
        <div>
          <p className="eyebrow">{tx("新 Key 仅展示一次：")}</p>
          <h2 id="issued-key-title">{tx("新 Key 已生成")}</h2>
          <p>{tx("请现在复制并保存这个 Key。关闭弹窗后将无法再次查看完整 Key，只能通过轮换生成新的 Key。")}</p>
        </div>
        <label className="issued-key-field">
          <span>{tx("完整 Key")}</span>
          <textarea
            readOnly
            value={value}
            onFocus={(event) => event.currentTarget.select()}
          />
        </label>
        <div className="modal-actions">
          <button className="secondary-button" onClick={() => void copyKey()} type="button">
            {copied ? <Check size={16} /> : <Copy size={16} />}
            {copied ? tx("已复制") : tx("复制 Key")}
          </button>
          <button className="button" disabled={closeCountdown > 0} onClick={onClose} type="button">
            {closeCountdown > 0 ? issuedKeyCloseCountdownLabel(closeCountdown) : tx("我已保存，关闭")}
          </button>
        </div>
      </div>
    </div>
  );
}

export function issuedKeyCloseCountdownLabel(seconds: number) {
  if (activeLanguage === "en") return `Close in ${seconds}s`;
  if (activeLanguage === "ja") return `${seconds} 秒後に閉じる`;
  return `${seconds}s 后可关闭`;
}

export function FieldInput({
  field,
  data,
  currentUser,
  values,
  value,
  editing,
  onChange,
}: {
  field: FieldConfig;
  data: AppData;
  currentUser?: AdminUser | null;
  values?: Record<string, string>;
  value: string;
  editing: boolean;
  onChange: (value: string) => void;
}) {
  const [filter, setFilter] = useState("");
  const readOnly = editing && field.readOnlyOnEdit;
  const autoComplete = field.autoComplete ?? "off";
  const inputName = `tokenhub-${field.key}`;
  let options = field.optionsFromData?.(data, currentUser, values) ?? (field.options ?? []).map((option) => ({ value: option, label: enumOptionLabel(field.key, option) }));
  if (field.type !== "multi-select" && field.type !== "tag-select" && value && !options.some((option) => option.value === value)) {
    options = [...options, { value, label: value }];
  }
  if (field.type === "tag-select") {
    const translatedOptions = options.map((option) => ({ ...option, label: tx(option.label) }));
    const optionsByValue = new Map(translatedOptions.map((option) => [option.value, option]));
    const selected = splitList(value).map((item) => optionsByValue.get(item) ?? { value: item, label: item });
    return (
      <div className="field tag-select-field" data-field-key={field.key}>
        <label htmlFor={inputName}>{tx(field.label)}</label>
        <Select
          className="tag-select"
          classNamePrefix="tag-select"
          components={{ ClearIndicator: HiddenSelectIndicator, DropdownIndicator: HiddenSelectIndicator, IndicatorSeparator: HiddenSelectIndicator }}
          inputId={inputName}
          instanceId={inputName}
          isDisabled={readOnly}
          isMulti
          isSearchable
          noOptionsMessage={() => tx("没有匹配的选项")}
          onChange={(next: MultiValue<{ value: string; label: string }>) => onChange(next.map((option) => option.value).join(", "))}
          openMenuOnFocus
          options={translatedOptions}
          placeholder={tx(field.placeholder ?? "请选择")}
          required={field.required}
          value={selected}
        />
        {field.help ? <small>{tx(field.help)}</small> : null}
      </div>
    );
  }
  if (field.type === "multi-select" && (!editing || field.multiSelectOnEdit)) {
    const selected = new Set(splitList(value));
    const normalizedFilter = filter.trim().toLowerCase();
    const filteredOptions = normalizedFilter
      ? options.filter((option) => `${option.label} ${option.value}`.toLowerCase().includes(normalizedFilter))
      : options;
    const selectedCount = selected.size;
    const updateSelected = (next: Set<string>) => onChange(Array.from(next).join(", "));
    return (
      <div className="field multi-select-field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <div className="multi-select-tools">
          <input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={tx(field.placeholder ?? (field.key === "model_name" ? "搜索模型" : "搜索选项"))}
            type="search"
          />
          <button
            className="secondary-button"
            onClick={() => updateSelected(new Set([...selected, ...filteredOptions.map((option) => option.value)]))}
            type="button"
          >
            {tx("全选")}
          </button>
          <button className="secondary-button" onClick={() => onChange("")} type="button">
            {tx("清空")}
          </button>
        </div>
        <div className="multi-select-list">
          {filteredOptions.length === 0 ? (
            <div className="empty">{tx(options.length === 0 && field.emptyOptionsText ? field.emptyOptionsText : (field.key === "model_name" ? "没有匹配的模型" : "没有匹配的选项"))}</div>
          ) : filteredOptions.map((option) => (
            <label className="multi-select-option" key={option.value}>
              <input
                checked={selected.has(option.value)}
                onChange={(event) => {
                  const next = new Set(selected);
                  if (event.target.checked) {
                    next.add(option.value);
                  } else {
                    next.delete(option.value);
                  }
                  updateSelected(next);
                }}
                type="checkbox"
              />
              <span>{tx(option.label)}</span>
            </label>
          ))}
        </div>
        <small>{selectedCount > 0 ? (field.key === "model_name" ? selectedModelsText(selectedCount) : selectedOptionsText(selectedCount)) : tx(field.emptySelectionText ?? (field.key === "model_name" ? "请选择至少一个统一模型" : "请选择至少一个选项"))}</small>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </div>
    );
  }
  if (field.type === "select" || field.type === "multi-select") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <select value={value} onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }} onInvalid={handleRequiredFieldInvalid} required={field.required} disabled={readOnly}>
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
          readOnly={readOnly}
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
          <button
            aria-checked={checked}
            className={checked ? "active" : ""}
            disabled={readOnly}
            onClick={() => onChange("true")}
            role="radio"
            type="button"
          >
            {tx("开启")}
          </button>
          <button
            aria-checked={!checked}
            className={!checked ? "active" : ""}
            disabled={readOnly}
            onClick={() => onChange("false")}
            role="radio"
            type="button"
          >
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
        readOnly={readOnly}
      />
      {field.help ? <small>{tx(field.help)}</small> : null}
    </label>
  );
}

export function DataSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="section">
      <div className="section-header">
        <h2>{tx(title)}</h2>
      </div>
      <div className="section-body">{children}</div>
    </section>
  );
}

export function SimpleTable({
  columns,
  rows,
  paginationKey,
}: {
  columns: string[];
  rows: React.ReactNode[][];
  paginationKey?: string;
}) {
  if (rows.length === 0) return <div className="empty">{tx("暂无数据")}</div>;
  if (paginationKey) {
    return <PaginatedSimpleTable columns={columns} rows={rows} paginationKey={paginationKey} />;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>{columns.map((column) => <th key={column}>{tx(column)}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={index}>{row.map((cell, cellIndex) => <td key={cellIndex}>{translatedCell(cell)}</td>)}</tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function PaginatedSimpleTable({
  columns,
  rows,
  paginationKey,
}: {
  columns: string[];
  rows: React.ReactNode[][];
  paginationKey: string;
}) {
  const pagination = usePagination(rows.length, paginationKey);
  const visibleRows = useMemo(
    () => rows.slice(pagination.startIndex, pagination.endIndex),
    [rows, pagination.startIndex, pagination.endIndex],
  );
  return (
    <>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>{columns.map((column) => <th key={column}>{tx(column)}</th>)}</tr>
          </thead>
          <tbody>
            {visibleRows.map((row, index) => (
              <tr key={pagination.startIndex + index}>{row.map((cell, cellIndex) => <td key={cellIndex}>{translatedCell(cell)}</td>)}</tr>
            ))}
          </tbody>
        </table>
      </div>
      <PaginationControls pagination={pagination} totalItems={rows.length} />
    </>
  );
}

export function StatusPill({ status, label }: { status: string; label?: string }) {
  const normalized = String(status).toLowerCase();
  const kind =
    normalized === "active" || normalized === "healthy" || normalized === "ok" || normalized === "confirmed" || normalized === "approved"
      ? "ok"
      : normalized === "warning" || normalized === "degraded" || normalized === "pending"
        ? "warn"
        : normalized === "error" || normalized === "down" || normalized === "disabled" || normalized === "rejected" || normalized === "failed" || normalized === "revoked" || normalized === "expired"
          ? "error"
          : "";
  return <span className={`pill ${kind}`}>{label ? tx(label) : enumValueLabel(status)}</span>;
}

export function ModelNameCell({ model }: { model: Model }) {
  const title = modelDisplayName(model.metadata, model.name);
  return (
    <div className="model-name-cell">
      <strong>{title}</strong>
      <span>{title !== model.name ? `${model.name} · ` : ""}{modelCategoryLabel(modelCategory(model))} · {model.family || "-"} · {model.modality || "chat"} · {model.context_window ? `${compactNumber(model.context_window)} ctx` : "ctx -"}</span>
    </div>
  );
}

export function ModelRouteProviders({ model, data }: { model: Model; data: AppData }) {
  const routes = modelRoutesFor(model, data);
  if (routes.length === 0) {
    return <span className="muted-inline">{tx("未配置线路")}</span>;
  }
  return (
    <div className="route-provider-list">
      {routes.slice(0, 4).map((route) => {
        const provider = findProvider(data, route.provider_id);
        return (
          <div className="route-provider-chip" key={route.id}>
            <span className={route.status === "active" ? "route-dot ok" : "route-dot"} />
            <strong>{provider?.name || route.provider_id}</strong>
            <em>{route.provider_model}</em>
            <small>{routeStrategyLabel(route.strategy)} · P{route.priority} · W{route.weight}</small>
          </div>
        );
      })}
      {routes.length > 4 ? <span className="route-overflow">+{routes.length - 4}</span> : null}
    </div>
  );
}

export const providerTypeOptions = ["mock", "openai", "openai_codex", "openai_compatible", "azure_openai", "anthropic", "gemini", "deepseek", "qwen", "local", "kronk"];
