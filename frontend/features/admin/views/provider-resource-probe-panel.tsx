import { Send } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { type ApiContext, type PluginActionDescriptor, type ProviderCatalogEntry, type ProviderResource } from "../core/types";
import { tx } from "../i18n/runtime";
import { isAuthExpiredError } from "../resources/payloads";
import { providerPluginActionDefaultPayload, providerPluginActionForCapability, runProviderResourcePluginAction } from "../resources/provider-model-config";
import { providerResourceAccountLabel, QuotaMetric } from "./provider-account-ui";

type ProviderResourceProbeValues = {
  model: string;
  reasoning_effort: string;
  speed: string;
  prompt: string;
};

type ProviderResourceProbeResult = {
  resource_id?: string;
  model?: string;
  reasoning_effort?: string;
  speed?: string;
  upstream_service_tier?: string;
  output_text?: string;
  latency_ms?: number;
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    total_tokens?: number;
  };
};

export function ProviderResourceProbePanel({
  api,
  accountCatalogErrors,
  accountCatalogLoading,
  accountResources,
  pluginActions,
  providerType,
  selectedAccountCatalog,
  selectedAccountID,
  selectedAccountResources,
}: {
  api: ApiContext;
  accountCatalogErrors: Record<string, string>;
  accountCatalogLoading: boolean;
  accountResources: ProviderResource[];
  pluginActions: PluginActionDescriptor[];
  providerType: string;
  selectedAccountCatalog: ProviderCatalogEntry | null;
  selectedAccountID: string;
  selectedAccountResources: ProviderResource[];
}) {
  const action = providerPluginActionForCapability(pluginActions, providerType, "probe.run");
  const fields = useMemo(() => probePanelFields(action), [action]);
  const probeModels = useMemo(() => selectedAccountCatalog?.models ?? [], [selectedAccountCatalog?.models]);
  const [values, setValues] = useState<ProviderResourceProbeValues>(() => probePanelDefaults(action));
  const selectedModel = probeModels.find((model) => model.id === values.model);
  const reasoningEfforts = selectedModel?.metadata?.supported_reasoning_levels?.split(",").map((value) => value.trim()).filter(Boolean) ?? ["medium"];
  const [busyID, setBusyID] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [results, setResults] = useState<Record<string, ProviderResourceProbeResult>>({});

  useEffect(() => {
    setValues(probePanelDefaults(action));
    setErrors({});
    setResults({});
  }, [action]);

  useEffect(() => {
    if (!fields.has("model") || probeModels.length === 0) return;
    setValues((current) => {
      const nextModel = probeModels.some((model) => model.id === current.model)
        ? current.model
        : probeModels[0].id;
      const model = probeModels.find((item) => item.id === nextModel);
      const efforts = model?.metadata?.supported_reasoning_levels?.split(",").map((value) => value.trim()).filter(Boolean) ?? ["medium"];
      const nextEffort = efforts.includes(current.reasoning_effort)
        ? current.reasoning_effort
        : model?.metadata?.default_reasoning_level || efforts[0] || "medium";
      if (nextModel === current.model && nextEffort === current.reasoning_effort) return current;
      return { ...current, model: nextModel, reasoning_effort: nextEffort };
    });
  }, [fields, probeModels]);

  async function runProbe() {
    if (!action) return;
    if (selectedAccountResources.length === 0) {
      setErrors({ selection: tx("请选择要测试的账号") });
      return;
    }
    setBusyID(selectedAccountID);
    setErrors({});
    await Promise.all(selectedAccountResources.map(async (resource) => {
      try {
        const result = await runProviderResourcePluginAction<ProviderResourceProbeResult>(api, resource, action, probePayload(values, fields), tx("账号资源测试"));
        setResults((current) => ({ ...current, [resource.id]: result }));
      } catch (err) {
        if (isAuthExpiredError(err)) return;
        setErrors((current) => ({ ...current, [resource.id]: err instanceof Error ? err.message : tx("账号资源测试失败") }));
      }
    }));
    setBusyID("");
  }

  if (!action) return null;

  return (
    <section className="provider-quota-panel provider-codex-test-panel">
      <div className="wizard-panel-head">
        <h3>{tx("真实请求测试")}</h3>
        <p>{tx("使用顶部选中的账号发送真实账号资源测试；选择全部账号时，每个账号都会分别发起一次请求并展示独立结果。")}</p>
      </div>
      {selectedAccountID === "all" ? <p className="provider-account-intersection-note">{tx("当前模型列表是所有账号可用模型的交集，确保所选模型能够被每个账号真实调用。")}</p> : null}
      <div className="provider-codex-test-controls">
        {fields.has("model") ? (
          <label className="field">
            <span>{tx("模型")}</span>
            <select disabled={accountCatalogLoading || probeModels.length === 0} value={values.model} onChange={(event) => setValues((current) => ({ ...current, model: event.target.value }))}>
              {probeModels.map((model) => <option key={model.id} value={model.id}>{model.display_name || model.name}</option>)}
            </select>
            {accountCatalogLoading ? <small>{tx("正在从所选账号加载模型目录...")}</small> : null}
          </label>
        ) : null}
        {fields.has("reasoning_effort") ? (
          <label className="field">
            <span>{tx("推理强度")}</span>
            <select disabled={accountCatalogLoading || probeModels.length === 0} value={values.reasoning_effort} onChange={(event) => setValues((current) => ({ ...current, reasoning_effort: event.target.value }))}>
              {reasoningEfforts.map((value) => <option key={value} value={value}>{value}</option>)}
            </select>
          </label>
        ) : null}
        {fields.has("speed") ? (
          <label className="field">
            <span>{tx("速度")}</span>
            <select disabled={accountCatalogLoading || probeModels.length === 0} value={values.speed} onChange={(event) => setValues((current) => ({ ...current, speed: event.target.value }))}>
              <option value="standard">{tx("标准")}</option>
              <option value="fast">{tx("快速")}</option>
            </select>
          </label>
        ) : null}
        {fields.has("prompt") ? (
          <label className="field provider-codex-test-prompt">
            <span>{tx("真实提示词")}</span>
            <textarea rows={3} value={values.prompt} onChange={(event) => setValues((current) => ({ ...current, prompt: event.target.value }))} placeholder={tx("输入要真实发送给上游的内容")} />
          </label>
        ) : null}
      </div>
      {Object.entries(accountCatalogErrors).map(([resourceID, message]) => <p className="provider-quota-error" key={resourceID}>{accountResources.find((resource) => resource.id === resourceID)?.name || resourceID}：{message}</p>)}
      <div className="provider-codex-test-actions">
        <button className="primary-button" disabled={probeDisabled(busyID, accountCatalogLoading, probeModels.length, values, fields)} onClick={() => void runProbe()} type="button">
          <Send size={14} />
          {tx(busyID ? "正在真实调用" : selectedAccountID === "all" ? "测试全部账号" : "发送真实测试")}
        </button>
      </div>
      <div className="provider-quota-list">
        {selectedAccountResources.map((resource) => <ProbeResourceResultCard error={errors[resource.id]} key={resource.id} resource={resource} result={results[resource.id]} />)}
      </div>
      {errors.selection ? <p className="provider-quota-error">{errors.selection}</p> : null}
    </section>
  );
}

function ProbeResourceResultCard({ error, resource, result }: { error?: string; resource: ProviderResource; result?: ProviderResourceProbeResult }) {
  return (
    <article className="provider-quota-card">
      <div className="provider-quota-card-head">
        <div><strong>{resource.name}</strong><span>{providerResourceAccountLabel(resource)}</span></div>
        <span className={error ? "provider-test-status error" : result ? "provider-test-status success" : "provider-test-status"}>{tx(error ? "失败" : result ? "完成" : "等待测试")}</span>
      </div>
      {result ? (
        <div className="provider-codex-test-result">
          <div className="provider-quota-grid">
            {result.model ? <QuotaMetric label="模型" value={result.model} /> : null}
            {result.reasoning_effort ? <QuotaMetric label="推理强度" value={result.reasoning_effort} /> : null}
            {result.speed ? <QuotaMetric label="请求速度" value={result.speed === "fast" ? "快速" : "标准"} /> : null}
            {result.upstream_service_tier ? <QuotaMetric label="上游 Service Tier" value={result.upstream_service_tier || "未返回"} /> : null}
            {result.latency_ms !== undefined ? <QuotaMetric label="耗时" value={`${result.latency_ms} ms`} /> : null}
            {result.usage?.prompt_tokens !== undefined ? <QuotaMetric label="输入 Token" value={String(result.usage.prompt_tokens)} /> : null}
            {result.usage?.completion_tokens !== undefined ? <QuotaMetric label="输出 Token" value={String(result.usage.completion_tokens)} /> : null}
            {result.usage?.total_tokens !== undefined ? <QuotaMetric label="总 Token" value={String(result.usage.total_tokens)} /> : null}
          </div>
          {result.output_text ? <pre>{result.output_text}</pre> : null}
        </div>
      ) : error ? <p className="provider-quota-error">{error}</p> : <p className="provider-credential-note">{tx("填写提示词后发送；返回内容来自真实上游。")}</p>}
    </article>
  );
}

function probePanelFields(action?: PluginActionDescriptor) {
  const configured = action?.metadata?.probe_fields?.split(",").map((field) => field.trim()).filter(Boolean);
  return new Set(configured?.length ? configured : ["model", "prompt"]);
}

function probePanelDefaults(action?: PluginActionDescriptor): ProviderResourceProbeValues {
  const payload = providerPluginActionDefaultPayload(action);
  return {
    model: stringPayload(payload.model),
    reasoning_effort: stringPayload(payload.reasoning_effort) || "medium",
    speed: stringPayload(payload.speed) || "standard",
    prompt: stringPayload(action?.metadata?.ui_default_prompt),
  };
}

function probePayload(values: ProviderResourceProbeValues, fields: Set<string>) {
  return Object.fromEntries(Object.entries(values).filter(([key]) => fields.has(key)));
}

function probeDisabled(busyID: string, loading: boolean, modelCount: number, values: ProviderResourceProbeValues, fields: Set<string>) {
  return Boolean(busyID) || loading || (fields.has("model") && modelCount === 0) || (fields.has("prompt") && !values.prompt.trim());
}

function stringPayload(value: unknown) {
  return typeof value === "string" ? value : "";
}
