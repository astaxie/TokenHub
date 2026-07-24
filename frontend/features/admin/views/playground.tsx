import { Check, Code2, Copy, Plus, Send, Sparkles, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { type ApiContext, type ApiExampleLanguage, type AppData, type PlaygroundChatPayload, type PlaygroundMessage } from "../core/types";
import { modelCategory, modelCategoryLabel } from "../domain/catalog";
import { activeRouteCount, apiExampleLanguages, apiExampleScripts, extractAssistantText, formatMoney, formatNumber, playgroundModels, readAPIError, routeStrategyLabel, uniqueUIID } from "../domain/formatting";
import { activeLanguage, countWithUnit, defaultPlaygroundSystemPrompt, isDefaultPlaygroundSystemPrompt, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError } from "../resources/payloads";
import { DetailField } from "./audit";

export function PlaygroundPage({ api, data, canViewRoutes }: { api: ApiContext; data: AppData; canViewRoutes: boolean }) {
  return (
    <section className="playground-page">
      <PlaygroundPanel api={api} data={data} canViewRoutes={canViewRoutes} />
    </section>
  );
}

export function PlaygroundPanel({
  api,
  data,
  canViewRoutes,
}: {
  api: ApiContext;
  data: AppData;
  canViewRoutes: boolean;
}) {
  const models = useMemo(() => {
    const candidates = playgroundModels(data, canViewRoutes);
    return canViewRoutes ? candidates.filter((model) => activeRouteCount(model.name, data) > 0) : candidates;
  }, [data.models, data.routes, canViewRoutes]);
  const [modelName, setModelName] = useState(models[0]?.name ?? "");
  const [messages, setMessages] = useState<PlaygroundMessage[]>([]);
  const [draft, setDraft] = useState("");
  const [systemPrompt, setSystemPrompt] = useState(defaultPlaygroundSystemPrompt);
  const [responseFormat, setResponseFormat] = useState("text");
  const [maxTokens, setMaxTokens] = useState("4096");
  const [temperature, setTemperature] = useState("0.7");
  const [reasoningEffort, setReasoningEffort] = useState("");
  const [presencePenalty, setPresencePenalty] = useState("0.0");
  const [frequencyPenalty, setFrequencyPenalty] = useState("0.0");
  const [minP, setMinP] = useState("0.00");
  const [topK, setTopK] = useState("50");
  const [showCode, setShowCode] = useState(false);
  const [showModelDetails, setShowModelDetails] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lastResult, setLastResult] = useState<PlaygroundChatPayload | null>(null);
  const selectedModel = models.find((model) => model.name === modelName);
  const selectedModelRouteCount = canViewRoutes && selectedModel ? activeRouteCount(selectedModel.name, data) : 0;
  const contextWindow = selectedModel?.context_window ?? 0;
  const maxTokenLimit = Math.max(4096, Math.min(contextWindow || 32768, 200000));
  const inputPrice = selectedModel?.input_price_usd_per_1m ?? 0;
  const outputPrice = selectedModel?.output_price_usd_per_1m ?? 0;
  const supportsReasoningEffort = Boolean(
    selectedModel?.capabilities?.includes("reasoning")
    || selectedModel?.supported_parameters?.some((parameter) => parameter === "reasoning" || parameter === "reasoning_effort"),
  );

  useEffect(() => {
    if (!modelName && models[0]?.name) {
      setModelName(models[0].name);
      return;
    }
    if (modelName && !models.some((model) => model.name === modelName)) {
      setModelName(models[0]?.name ?? "");
    }
  }, [modelName, models]);

  const languageVersion = activeLanguage;
  useEffect(() => {
    setSystemPrompt((current) => isDefaultPlaygroundSystemPrompt(current) ? defaultPlaygroundSystemPrompt() : current);
  }, [languageVersion]);

  useEffect(() => {
    const parsed = Number(maxTokens);
    if (Number.isFinite(parsed) && parsed > maxTokenLimit) {
      setMaxTokens(String(maxTokenLimit));
    }
  }, [maxTokenLimit, maxTokens]);

  async function sendMessage(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    const content = draft.trim();
    if (!content || !modelName || loading) return;
    const userMessage: PlaygroundMessage = { id: uniqueUIID("msg"), role: "user", content };
    const nextMessages = [...messages, userMessage];
    const messagesForRequest = systemPrompt.trim()
      ? [{ role: "system", content: systemPrompt.trim() }, ...nextMessages.map((message) => ({ role: message.role, content: message.content }))]
      : nextMessages.map((message) => ({ role: message.role, content: message.content }));
    setMessages(nextMessages);
    setDraft("");
    setError("");
    setLoading(true);
    try {
      const numericTemperature = Number(temperature);
      const numericMaxTokens = Number(maxTokens);
      const resp = await adminFetch(api, "/api/admin/playground/chat", {
        method: "POST",
        body: JSON.stringify({
          model: modelName,
          messages: messagesForRequest,
          max_tokens: Number.isFinite(numericMaxTokens) && numericMaxTokens > 0 ? Math.round(numericMaxTokens) : undefined,
          temperature: Number.isFinite(numericTemperature) ? numericTemperature : undefined,
          reasoning_effort: supportsReasoningEffort && reasoningEffort ? reasoningEffort : undefined,
        }),
      });
      if (!resp.ok) {
        throw new Error(await readAPIError(resp));
      }
      const payload = (await resp.json()) as PlaygroundChatPayload;
      const assistantText = extractAssistantText(payload);
      setLastResult(payload);
      setMessages((current) => [
        ...current,
        {
          id: uniqueUIID("msg"),
          role: "assistant",
          content: assistantText || tx("模型没有返回可展示内容。"),
        },
      ]);
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("演练请求失败"));
    } finally {
      setLoading(false);
    }
  }

  function clearHistory() {
    setMessages([]);
    setLastResult(null);
    setError("");
  }

  return (
    <div className="playground-shell">
      <aside className="playground-config" aria-label={tx("模型配置")}>
        <label className="playground-model-select">
          <select value={modelName} onChange={(event) => setModelName(event.target.value)} disabled={models.length === 0}>
            {models.length === 0 ? <option value="">{tx("暂无聊天模型")}</option> : null}
            {models.map((model) => {
              const routeCount = canViewRoutes ? activeRouteCount(model.name, data) : 0;
              return (
                <option key={model.name} value={model.name}>
                  {!canViewRoutes ? model.name : routeCount > 0 ? `${model.name} · ${countWithUnit(routeCount, "条路由", "route", "件のルート")}` : `${model.name} · ${tx("未配置路由")}`}
                </option>
              );
            })}
          </select>
        </label>

        <div className="playground-config-body">
          <h2>{tx("模型配置")}</h2>
          <label className="playground-field">
            <span>{tx("响应格式")}</span>
            <select value={responseFormat} onChange={(event) => setResponseFormat(event.target.value)}>
              <option value="text">text</option>
            </select>
          </label>
          {supportsReasoningEffort ? (
            <label className="playground-field">
              <span>{tx("推理强度")}</span>
              <select value={reasoningEffort} onChange={(event) => setReasoningEffort(event.target.value)}>
                <option value="">{tx("模型默认")}</option>
                {["none", "minimal", "low", "medium", "high", "xhigh", "max"].map((effort) => (
                  <option key={effort} value={effort}>{effort}</option>
                ))}
              </select>
            </label>
          ) : null}
          <label className="playground-field">
            <span>{tx("系统提示")}</span>
            <textarea value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} />
          </label>
          <PlaygroundConfigSlider label="max_tokens" value={maxTokens} onChange={setMaxTokens} min={128} max={maxTokenLimit} step={128} />
          <PlaygroundConfigSlider label="temperature" value={temperature} onChange={setTemperature} min={0} max={2} step={0.1} />
          <PlaygroundConfigSlider label="presence_penalty" value={presencePenalty} onChange={setPresencePenalty} min={-2} max={2} step={0.1} />
          <PlaygroundConfigSlider label="frequency_penalty" value={frequencyPenalty} onChange={setFrequencyPenalty} min={-2} max={2} step={0.1} />
          <PlaygroundConfigSlider label="min_p" value={minP} onChange={setMinP} min={0} max={1} step={0.01} />
          <PlaygroundConfigSlider label="top_k" value={topK} onChange={setTopK} min={0} max={100} step={1} />
          <div className="playground-functions">
            <strong>{tx("函数")}</strong>
            <button type="button" className="secondary-button compact" disabled title={tx("函数调用配置待接入")}>
              <Plus size={14} />
              {tx("添加函数")}
            </button>
          </div>
        </div>
      </aside>

      <section className="playground-main" aria-label={tx("模型演练对话")}>
        <div className="playground-model-bar">
          <div className="playground-model-title">
            <button type="button" className="playground-copy-model" title={tx("复制模型名")} onClick={() => navigator.clipboard?.writeText(modelName).catch(() => undefined)}>
              <strong>{modelName || tx("选择模型")}</strong>
              <Copy size={13} />
            </button>
            <span>
              {contextWindow ? `${formatNumber(contextWindow)} ${tx("上下文")}` : `${tx("上下文")} -`}
              <em />
              ${formatMoney(inputPrice)}/Mt {tx("输入")}
              <em />
              ${formatMoney(outputPrice)}/Mt {tx("输出")}
              {canViewRoutes ? (
                <>
                  <em />
                  {countWithUnit(selectedModelRouteCount, "条启用路由", "active route", "件の有効ルート")}
                </>
              ) : null}
            </span>
          </div>
          <div className="playground-actions">
            <button type="button" className={showModelDetails ? "secondary-button compact active" : "secondary-button compact"} onClick={() => setShowModelDetails((value) => !value)}>
              <Sparkles size={14} />
              {tx("模型详情")}
            </button>
            <button type="button" className={showCode ? "secondary-button compact active" : "secondary-button compact"} onClick={() => setShowCode((value) => !value)}>
              <Code2 size={14} />
              {tx("查看代码")}
            </button>
            <button type="button" className="secondary-button compact" onClick={clearHistory} disabled={messages.length === 0 && !lastResult}>
              <Trash2 size={14} />
              {tx("清空历史")}
            </button>
          </div>
        </div>

        {showModelDetails ? (
          <div className="playground-detail-strip">
            <DetailField label="类型" value={selectedModel ? modelCategoryLabel(modelCategory(selectedModel)) : "-"} />
            <DetailField label="能力" value={selectedModel?.modality || "chat"} />
            <DetailField label="上下文" value={contextWindow ? formatNumber(contextWindow) : "-"} />
            <DetailField label="最近路由" value={lastResult?.route ? `${lastResult.route.provider_name || lastResult.route.provider_id} / ${lastResult.route.provider_model}` : "待请求"} />
          </div>
        ) : null}

        {showCode ? (
          <div className="playground-code-drawer">
            <PlaygroundAPIExamples baseURL={api.baseURL} modelName={modelName || selectedModel?.name || "gpt-4.1-mini"} />
          </div>
        ) : null}

        {lastResult?.route ? (
          <div className="playground-route">
            <span>{lastResult.route.provider_name || lastResult.route.provider_id || "-"}</span>
            <em>{lastResult.route.provider_model || "-"}</em>
            <small>{lastResult.route.resource_name || lastResult.route.resource_id || tx("默认资源")} · {routeStrategyLabel(lastResult.route.strategy)} · {countWithUnit(lastResult.attempts?.length ?? 0, "次尝试", "attempt", "回試行")}</small>
          </div>
        ) : null}

        {error ? <div className="status-line error playground-error">{error}</div> : null}

        <div className="playground-chat">
          {messages.length === 0 ? (
            <div className="playground-empty">
              <Sparkles size={22} />
              <strong>{tx("试用")} {modelName || tx("当前模型")}</strong>
              <span>{models.length === 0 ? tx("当前没有可演练模型。请先在路由策略里启用至少一条模型线路。") : tx("体验一下，看看模型在 TokenHub 网关上的表现")}</span>
            </div>
          ) : (
            messages.map((message) => (
              <div className={`playground-message ${message.role}`} key={message.id}>
                <span>{message.role === "assistant" ? "Assistant" : message.role === "system" ? "System" : "User"}</span>
                <p>{message.content}</p>
              </div>
            ))
          )}
        </div>

        <form className="playground-composer" onSubmit={sendMessage}>
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={tx("说点什么...")}
            disabled={loading || models.length === 0}
          />
          <div className="playground-composer-actions">
            <button className="secondary-button compact" type="button" disabled title={tx("文件上传待接入")}>
              <Plus size={14} />
              {tx("上传文件")}
            </button>
            {lastResult?.usage ? (
              <div className="playground-foot">
                <span>Prompt {formatNumber(lastResult.usage.prompt_tokens ?? 0)}</span>
                <span>Cached {formatNumber(lastResult.usage.cached_input_tokens ?? 0)}</span>
                <span>Completion {formatNumber(lastResult.usage.completion_tokens ?? 0)}</span>
                <span>Total {formatNumber(lastResult.usage.total_tokens ?? 0)}</span>
                {lastResult.request_id ? <span>{lastResult.request_id}</span> : null}
              </div>
            ) : null}
            <button className="playground-send-button" disabled={loading || !draft.trim() || models.length === 0} type="submit" title={tx("发送")}>
              <Send size={18} />
            </button>
          </div>
        </form>
      </section>
    </div>
  );
}

export function PlaygroundConfigSlider({
  label,
  value,
  onChange,
  min,
  max,
  step,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  min: number;
  max: number;
  step: number;
}) {
  const numeric = Number(value);
  const rangeValue = Number.isFinite(numeric) ? Math.min(max, Math.max(min, numeric)) : min;
  return (
    <label className="playground-slider">
      <div>
        <span>{label}</span>
        <input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} />
      </div>
      <input type="range" value={rangeValue} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} />
    </label>
  );
}

export function PlaygroundAPIExamples({ baseURL, modelName }: { baseURL: string; modelName: string }) {
  const [language, setLanguage] = useState<ApiExampleLanguage>("python");
  const [copied, setCopied] = useState(false);
  const examples = useMemo(() => apiExampleScripts(baseURL, modelName), [baseURL, modelName]);
  const current = examples[language];

  async function copyCurrent() {
    try {
      await navigator.clipboard?.writeText(current);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }

  return (
    <section className="api-example-panel">
      <div className="api-example-header">
        <div>
          <strong>{tx("API 使用")}</strong>
          <span>{tx("使用以下代码示例集成 TokenHub 模型接口")}</span>
        </div>
        <button className="icon-button subtle" onClick={() => void copyCurrent()} type="button" title={tx("复制代码")}>
          {copied ? <Check size={15} /> : <Copy size={15} />}
        </button>
      </div>
      <div className="api-example-tabs" role="tablist" aria-label={tx("API 调用语言")}>
        {apiExampleLanguages.map((item) => (
          <button
            aria-selected={language === item.key}
            className={language === item.key ? "api-example-tab active" : "api-example-tab"}
            key={item.key}
            onClick={() => {
              setLanguage(item.key);
              setCopied(false);
            }}
            role="tab"
            type="button"
          >
            {item.label}
          </button>
        ))}
      </div>
      <div className="api-example-meta">
        <span>Chat Completions</span>
        <em>{modelName || tx("未选择模型")}</em>
      </div>
      <pre className="api-code-block"><code>{current}</code></pre>
    </section>
  );
}
