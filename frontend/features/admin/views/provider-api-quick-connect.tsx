import { Check, CircleAlert, CircleCheck, Eye, EyeOff, KeyRound, LoaderCircle, Plus, RefreshCw, Search } from "lucide-react";
import { useRef, useState } from "react";
import { type ApiContext, type ProviderCatalogEntry, type ProviderCatalogModel } from "../core/types";
import { providerTypeLabel } from "../domain/labels";
import { providerHeaderFormError, providerHeadersPayload } from "../domain/provider-headers";
import { formatModelPrice } from "../domain/formatting";
import { providerAnthropicAuthType, providerConnectionTestRunAfterUpdate } from "../domain/provider-custom-upstream";
import { clearCustomValidity, countWithUnit, handleRequiredFieldInvalid, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { providerTypeOptions } from "../shared/ui";
import { ProviderCustomHeaders } from "./provider-custom-headers";
import { AnthropicAuthTypeField } from "./provider-editor-sections";

type ProviderConnectionTestState = {
  status: "idle" | "testing" | "success" | "error";
  latencyMS?: number;
  message?: string;
};

export function ProviderAPIQuickCatalog({
  entries,
  total,
  selectedID,
  query,
  onQueryChange,
  onSelect,
  onSelectCustom,
}: {
  entries: ProviderCatalogEntry[];
  total: number;
  selectedID: string;
  query: string;
  onQueryChange: (value: string) => void;
  onSelect: (entry: ProviderCatalogEntry) => void;
  onSelectCustom: () => void;
}) {
  return (
    <section className="provider-catalog-pane provider-quick-catalog-pane">
      <div className="provider-catalog-head">
        <strong>{tx("选择渠道商")}</strong>
        <span>{countWithUnit(total, "个渠道", "provider", "Provider")}</span>
      </div>
      <div className="provider-template-search provider-quick-search">
        <Search size={14} />
        <input value={query} onChange={(event) => onQueryChange(event.target.value)} placeholder={tx("搜索渠道商名称或 ID")} />
      </div>
      <div className="provider-quick-catalog-list">
        {entries.length === 0 ? (
          <div className="empty compact-empty">{tx("没有匹配的渠道商")}</div>
        ) : entries.map((entry) => {
          const name = entry.display_name || entry.name;
          const active = entry.id === selectedID;
          return (
            <button className={active ? "provider-quick-catalog-item active" : "provider-quick-catalog-item"} key={entry.id} onClick={() => onSelect(entry)} type="button">
              <span className="provider-quick-catalog-avatar">{name.slice(0, 1).toUpperCase()}</span>
              <span className="provider-quick-catalog-copy">
                <strong>{name}</strong>
                <em>{providerTypeLabel(entry.type)} · {countWithUnit(entry.models_count, "个模型", "model", "モデル")}</em>
              </span>
              {active ? <Check size={15} /> : null}
            </button>
          );
        })}
      </div>
      <button className={selectedID === "custom" ? "custom-provider-button provider-quick-custom active" : "custom-provider-button provider-quick-custom"} onClick={onSelectCustom} type="button">
        <Plus size={14} />
        <span>{tx("自定义渠道商")}</span>
        <em>{tx("填写名称、Base URL 和 API Key")}</em>
      </button>
    </section>
  );
}

export function ProviderAPIQuickConnect({
  api,
  catalogID,
  entry,
  modelCount,
  models,
  modelsLoading,
  modelsError,
  modelQuery,
  selectedModelCount,
  selectedModels,
  activeTab,
  values,
  onModelQueryChange,
  onModelToggle,
  onReloadModels,
  onTabChange,
  onUpdate,
}: {
  api: ApiContext;
  catalogID: string;
  entry?: ProviderCatalogEntry;
  modelCount: number;
  models: ProviderCatalogModel[];
  modelsLoading: boolean;
  modelsError: string;
  modelQuery: string;
  selectedModelCount: number;
  selectedModels: Record<string, boolean>;
  activeTab: "connect" | "models" | "advanced";
  values: Record<string, string>;
  onModelQueryChange: (value: string) => void;
  onModelToggle: (modelID: string, enabled: boolean) => void;
  onReloadModels: () => void;
  onTabChange: (tab: "connect" | "models" | "advanced") => void;
  onUpdate: (key: string, value: string) => void;
}) {
  const [showKey, setShowKey] = useState(false);
  const [connectionTest, setConnectionTest] = useState<ProviderConnectionTestState>({ status: "idle" });
  const connectionTestRun = useRef(0);
  const custom = catalogID === "custom";
  const optionalAPIKey = catalogID === "kronk";
  const name = values.name || entry?.display_name || entry?.name || tx("请选择渠道商");
  const connectionReady = Boolean(values.base_url?.trim() && (optionalAPIKey || values.api_key?.trim()));

  function updateConnectionValue(key: string, value: string) {
    const nextRun = providerConnectionTestRunAfterUpdate(connectionTestRun.current, key);
    if (nextRun !== connectionTestRun.current) {
      connectionTestRun.current = nextRun;
      setConnectionTest({ status: "idle" });
    }
    onUpdate(key, value);
  }

  async function testConnection() {
    const headerError = providerHeaderFormError(values.custom_headers);
    if (headerError) { setConnectionTest({ status: "error", message: tx(headerError) }); return; }
    if (!connectionReady) {
      setConnectionTest({ status: "error", message: tx(optionalAPIKey ? "请填写 Base URL 后测试。" : "请填写 Base URL 和 API Key 后测试。") });
      return;
    }
    const run = connectionTestRun.current + 1;
    connectionTestRun.current = run;
    const startedAt = performance.now();
    setConnectionTest({ status: "testing" });
    try {
      const resp = await adminFetch(api, "/api/admin/providers/test-connection", {
        method: "POST",
        body: JSON.stringify({
          catalog_id: catalogID,
          name: values.name,
          type: values.type,
          base_url: values.base_url,
          api_key: values.api_key,
          ...providerHeadersPayload(values.custom_headers),
          anthropic_auth_type: providerAnthropicAuthType(values),
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("测试 Provider 连接")));
      const result = (await resp.json()) as { healthy: boolean; latency_ms: number };
      if (connectionTestRun.current !== run) return;
      setConnectionTest({
        status: "success",
        latencyMS: Math.max(0, result.latency_ms),
        message: tx(optionalAPIKey ? "Kronk 服务连接正常" : "API Key 配置有效"),
      });
    } catch (err) {
      if (connectionTestRun.current !== run || isAuthExpiredError(err)) return;
      setConnectionTest({
        status: "error",
        latencyMS: Math.max(0, Math.round(performance.now() - startedAt)),
        message: err instanceof Error ? err.message : tx("Provider 连接测试失败"),
      });
    }
  }

  return (
    <section className="provider-api-quick-connect">
      <div className="provider-api-quick-hero">
        <div>
          <span>{tx("直接 API Key")}</span>
          <h3>{name}</h3>
          <p>{values.base_url || tx("填写 Base URL 后连接上游")}</p>
        </div>
        <strong>{countWithUnit(selectedModelCount, "个待引入模型", "model to import", "件の取り込み予定モデル")}</strong>
      </div>

      <div className="provider-editor-tabs provider-quick-tabs" role="tablist" aria-label={tx("Provider 编辑区")}>
        <button aria-selected={activeTab === "connect"} className={activeTab === "connect" ? "active" : ""} onClick={() => onTabChange("connect")} role="tab" type="button">{tx("连接")}</button>
        <button aria-selected={activeTab === "models"} className={activeTab === "models" ? "active" : ""} onClick={() => onTabChange("models")} role="tab" type="button">{tx("模型")}</button>
        <button aria-selected={activeTab === "advanced"} className={activeTab === "advanced" ? "active" : ""} onClick={() => onTabChange("advanced")} role="tab" type="button">{tx("高级")}</button>
      </div>

      {activeTab === "connect" ? (
        <div className="provider-quick-tab-panel">
          <div className="provider-api-quick-intro">
            <span><KeyRound size={18} /></span>
            <div>
              <strong>{tx(custom ? "填写连接信息" : optionalAPIKey ? "连接 Kronk Model Server" : "只需填写 API Key")}</strong>
              <p>{tx(custom ? "自定义渠道需要填写名称、Base URL 和 API Key。" : optionalAPIKey ? "未启用 Kronk 认证时可留空；启用后填写 application token。" : "把上游 Key 保存到 Provider，适合单账号或兼容 API。")}</p>
            </div>
          </div>

          {custom ? (
            <div className="provider-quick-required-grid">
              <label className="field">
                <span>{tx("渠道名称")}</span>
                <input value={values.name ?? ""} onChange={(event) => onUpdate("name", event.target.value)} required />
              </label>
              <label className="field">
                <span>Base URL</span>
                <input value={values.base_url ?? ""} onChange={(event) => updateConnectionValue("base_url", event.target.value)} required />
              </label>
            </div>
          ) : (
            <label className="field">
              <span>Base URL</span>
              <input value={values.base_url ?? ""} onChange={(event) => updateConnectionValue("base_url", event.target.value)} />
            </label>
          )}

          <label className="field provider-quick-key-field">
            <span>{optionalAPIKey ? tx("Application Token（可选）") : "API Key"}</span>
            <div className="provider-quick-key-input">
              <input
                autoComplete="new-password"
                autoFocus
                value={values.api_key ?? ""}
                type={showKey ? "text" : "password"}
                onChange={(event) => {
                  clearCustomValidity(event);
                  updateConnectionValue("api_key", event.target.value);
                }}
                onInvalid={handleRequiredFieldInvalid}
                required={!optionalAPIKey}
              />
              <button aria-label={tx(showKey ? "隐藏 API Key" : "显示 API Key")} onClick={() => setShowKey((current) => !current)} type="button">
                {showKey ? <EyeOff size={17} /> : <Eye size={17} />}
              </button>
            </div>
            {entry?.doc_url ? <a href={entry.doc_url} rel="noreferrer" target="_blank">{tx(optionalAPIKey ? "查看 Kronk 文档" : "获取 API Key")}</a> : null}
          </label>
          <div className="provider-quick-test-row">
            <button
              className={connectionTest.status === "testing" ? "secondary-button provider-quick-test-button testing" : "secondary-button provider-quick-test-button"}
              disabled={!connectionReady || connectionTest.status === "testing"}
              onClick={() => void testConnection()}
              type="button"
            >
              {connectionTest.status === "testing" ? <LoaderCircle size={14} /> : <RefreshCw size={14} />}
              {tx(connectionTest.status === "testing" ? "正在测试" : "测试连接")}
            </button>
            {connectionTest.status !== "idle" && connectionTest.status !== "testing" ? (
              <div className={`provider-quick-test-status ${connectionTest.status}`} aria-live="polite" role="status">
                {connectionTest.status === "success" ? <CircleCheck size={15} /> : <CircleAlert size={15} />}
                <span>{connectionTest.message}</span>
                {connectionTest.latencyMS !== undefined ? <strong>{connectionTest.latencyMS} ms</strong> : null}
              </div>
            ) : null}
          </div>
        </div>
      ) : null}

      {activeTab === "models" ? (
        <div className="provider-quick-tab-panel">
          {custom && !values.base_url?.trim() ? (
            <p className="provider-quick-custom-note">{tx("先在“连接”中填写 Base URL 和 API Key，这里会加载自定义渠道的上游模型。")}</p>
          ) : (
            <>
              <div className="provider-quick-model-summary">
                <strong>{tx("模型列表")}</strong>
                <span>{selectedModelCount}/{modelCount} {tx("待引入")}</span>
              </div>
              <div className="provider-quick-model-tools">
                <div className="provider-template-search provider-quick-model-search">
                  <Search size={14} />
                  <input value={modelQuery} onChange={(event) => onModelQueryChange(event.target.value)} placeholder={tx("搜索模型、能力、参数")} />
                </div>
                <button className="secondary-button" onClick={onReloadModels} type="button"><RefreshCw size={14} />{tx("重新加载")}</button>
              </div>
              <div className="provider-quick-model-list">
                {modelsLoading ? (
                  <div className="empty">{tx("正在加载模型列表...")}</div>
                ) : modelsError ? (
                  <div className="empty">{modelsError}</div>
                ) : models.length === 0 ? (
                  <div className="empty">{tx("没有匹配的模型")}</div>
                ) : models.map((model) => {
                  const enabled = selectedModels[model.id] === true;
                  return (
                    <article className={enabled ? "provider-quick-model-item active" : "provider-quick-model-item"} key={model.id}>
                      <div>
                        <strong>{model.display_name || model.name}</strong>
                        <span>{model.canonical_name || model.id} ← {model.id} · {model.family || model.category || model.type || "model"} · {tx("渠道成本")} {formatModelPrice(model)}</span>
                      </div>
                      <button
                        aria-checked={enabled}
                        aria-label={`${tx(enabled ? "移除" : "引入")} ${model.display_name || model.name}`}
                        className={enabled ? "provider-quick-model-switch active" : "provider-quick-model-switch"}
                        onClick={() => onModelToggle(model.id, !enabled)}
                        role="switch"
                        type="button"
                      />
                    </article>
                  );
                })}
              </div>
            </>
          )}
        </div>
      ) : null}

      {activeTab === "advanced" ? (
        <div className="provider-quick-tab-panel">
          <div className="provider-form-grid provider-quick-advanced-grid">
            <label className="field">
              <span>Provider ID</span>
              <input value={values.id ?? ""} onChange={(event) => onUpdate("id", event.target.value)} placeholder={custom ? tx("例如 prv_company_proxy") : tx("留空自动生成")} />
            </label>
            {!custom ? (
              <label className="field">
                <span>{tx("渠道名称")}</span>
                <input value={values.name ?? ""} onChange={(event) => onUpdate("name", event.target.value)} />
              </label>
            ) : null}
            <label className="field">
              <span>{tx("渠道商类型")}</span>
              <select value={values.type ?? ""} onChange={(event) => updateConnectionValue("type", event.target.value)} required>
                {providerTypeOptions.map((option) => <option key={option} value={option}>{providerTypeLabel(option)}</option>)}
              </select>
            </label>
            <AnthropicAuthTypeField values={values} onUpdate={updateConnectionValue} />
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
          <ProviderCustomHeaders
            disabled={values.type === "azure_openai" || values.type === "openai_codex"}
            onChange={(value) => onUpdate("custom_headers", value)}
            value={values.custom_headers ?? "[]"}
          />
        </div>
      ) : null}
    </section>
  );
}
