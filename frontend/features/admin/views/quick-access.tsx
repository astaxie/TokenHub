import { ArrowRight, Bot, Check, ChevronDown, CircleAlert, KeyRound, Plus, Search, Terminal, X } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { appRole } from "../core/navigation";
import { type AdminUser, type APIKey, type ApiContext, type AppData, type Model } from "../core/types";
import { modelCategory, modelCategoryInitial, modelCategoryLabel } from "../domain/catalog";
import { modelCapabilitySummary, modelPriceSummary, projectSelectOptions } from "../domain/entities";
import { apiGatewayBaseURL, formatNumber, modelAvailabilitySummary, playgroundModels } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { apiKeyConfig } from "../resources/project-key-config";
import { defaultFormValues } from "../resources/payloads";
import { modelBrandIconSource, modelDisplayTitle } from "./database-model-pricing";
import { GatewayCodeBlock } from "./gateway-docs-ui";

type QuickAccessStep = "keys" | "models" | "usage";
type UsageMode = "api" | "tools";
type APIStyle = "chat" | "responses" | "messages";
type ToolStyle = "openai" | "claude";
type CodeLanguage = "curl" | "python" | "node";

export function QuickAccessView({
  api,
  data,
  user,
  loading,
  onCreateKey,
  onManageKeys,
}: {
  api: ApiContext;
  data: AppData;
  user: AdminUser;
  loading: boolean;
  onCreateKey: (values: Record<string, string>, onCreated: () => void) => void;
  onManageKeys: () => void;
}) {
  const hasKeys = data.keys.length > 0;
  const models = useMemo(() => playgroundModels(data), [data]);
  const [openStep, setOpenStep] = useState<QuickAccessStep | null>(() => hasKeys ? "models" : "keys");
  const [previewModelName, setPreviewModelName] = useState("");
  const [confirmedModelName, setConfirmedModelName] = useState("");
  const [quickKeyOpen, setQuickKeyOpen] = useState(false);
  const previousHasKeys = useRef(hasKeys);

  useEffect(() => {
    if (!previewModelName || !models.some((model) => model.name === previewModelName)) {
      setPreviewModelName(models[0]?.name ?? "");
    }
  }, [models, previewModelName]);

  useEffect(() => {
    if (!previousHasKeys.current && hasKeys) setOpenStep("models");
    if (previousHasKeys.current && !hasKeys) {
      setOpenStep("keys");
      setConfirmedModelName("");
    }
    previousHasKeys.current = hasKeys;
  }, [hasKeys]);

  const previewModel = models.find((model) => model.name === previewModelName) ?? models[0];
  const confirmedModel = models.find((model) => model.name === confirmedModelName);

  function toggleStep(step: QuickAccessStep) {
    if (step === "usage" && !confirmedModel) return;
    setOpenStep((current) => current === step ? null : step);
  }

  function confirmModel() {
    if (!previewModel) return;
    setConfirmedModelName(previewModel.name);
    setOpenStep("usage");
  }

  return (
    <div className="quick-access">
      <header className="quick-access-intro">
        <p className="eyebrow">{tx("快速接入")}</p>
        <h1>{tx("通过 API 或 AI 工具调用 TokenHub 模型")}</h1>
        <p>{tx("完成以下配置后，即可使用项目 API Key 调用已授权的模型服务。")}</p>
      </header>

      <div className="quick-access-steps">
        <QuickAccessStepPanel
          step="1"
          title="获取 API Key"
          completed={hasKeys}
          open={openStep === "keys"}
          onToggle={() => toggleStep("keys")}
        >
          <KeyStep keys={data.keys} onCreateKey={() => setQuickKeyOpen(true)} onManageKeys={onManageKeys} />
        </QuickAccessStepPanel>

        <QuickAccessStepPanel
          step="2"
          title="选择模型"
          completed={Boolean(confirmedModel)}
          summary={confirmedModel ? modelDisplayTitle(confirmedModel) : undefined}
          open={openStep === "models"}
          onToggle={() => toggleStep("models")}
        >
          <ModelStep
            data={data}
            models={models}
            previewModel={previewModel}
            onPreview={setPreviewModelName}
            onConfirm={confirmModel}
          />
        </QuickAccessStepPanel>

        {hasKeys ? (
          <QuickAccessStepPanel
            step="3"
            title="选择使用方式"
            disabled={!confirmedModel}
            open={openStep === "usage"}
            onToggle={() => toggleStep("usage")}
          >
            {confirmedModel ? <UsageStep api={api} keyHint={maskedKey(data.keys[0])} model={confirmedModel} /> : null}
          </QuickAccessStepPanel>
        ) : null}
      </div>

      {quickKeyOpen ? (
        <QuickAPIKeyModal
          data={data}
          user={user}
          loading={loading}
          onClose={() => {
            if (!loading) setQuickKeyOpen(false);
          }}
          onCreate={(values) => onCreateKey(values, () => setQuickKeyOpen(false))}
        />
      ) : null}
    </div>
  );
}

export function QuickAPIKeyModal({
  data,
  user,
  loading,
  onClose,
  onCreate,
}: {
  data: AppData;
  user: AdminUser;
  loading: boolean;
  onClose: () => void;
  onCreate: (values: Record<string, string>) => void;
}) {
  const personalKey = appRole(user.role) === "user";
  const projectOptions = useMemo(() => projectSelectOptions(data, user), [data, user]);
  const [values, setValues] = useState<Record<string, string>>(() => {
    const defaults = defaultFormValues(apiKeyConfig(), data, user);
    return {
      ...defaults,
      project_id: personalKey ? "" : defaults.project_id,
      name: "",
      status: "active",
    };
  });
  const selectedProject = data.projects.find((project) => project.id === values.project_id);
  const hasCreateScope = personalKey || (projectOptions.length > 0 && Boolean(values.project_id));
  const canCreate = hasCreateScope && Boolean(values.name.trim());

  useEffect(() => {
    if (personalKey) return;
    if (projectOptions.some((option) => option.value === values.project_id)) return;
    setValues((current) => ({ ...current, project_id: projectOptions[0]?.value ?? "" }));
  }, [personalKey, projectOptions, values.project_id]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canCreate || loading) return;
    onCreate({ ...values, name: values.name.trim() });
  }

  return (
    <div className="modal-backdrop quick-key-backdrop" role="presentation">
      <form aria-labelledby="quick-key-title" aria-modal="true" className="quick-key-modal" onSubmit={submit} role="dialog">
        <header className="quick-key-modal-head">
          <div className="quick-key-modal-icon" aria-hidden="true"><KeyRound size={19} /></div>
          <div>
            <h2 id="quick-key-title">{tx("快速创建 API Key")}</h2>
            <p>{tx("填写名称即可创建，模型范围和安全额度将使用平台默认配置。")}</p>
          </div>
          <button aria-label={tx("关闭")} className="icon-button subtle" disabled={loading} onClick={onClose} title={tx("关闭")} type="button">
            <X size={17} />
          </button>
        </header>

        {hasCreateScope ? (
          <div className="quick-key-modal-body">
            <label className="quick-key-field">
              <span>{tx("Key 名称")}</span>
              <input
                autoFocus
                disabled={loading}
                maxLength={80}
                onChange={(event) => setValues((current) => ({ ...current, name: event.target.value }))}
                placeholder={tx("例如：研发环境调试")}
                required
                value={values.name}
              />
            </label>

            {personalKey ? null : projectOptions.length > 1 ? (
              <label className="quick-key-field">
                <span>{tx("归属项目")}</span>
                <select
                  disabled={loading}
                  onChange={(event) => setValues((current) => ({ ...current, project_id: event.target.value }))}
                  value={values.project_id}
                >
                  {projectOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                </select>
              </label>
            ) : (
              <div className="quick-key-project">
                <span>{tx("归属项目")}</span>
                <strong>{selectedProject?.name || projectOptions[0]?.label}</strong>
              </div>
            )}

            <div className="quick-key-defaults">
              <Check size={15} />
              <span>{tx("创建后可调用全部可路由模型，并使用平台默认安全额度。")}</span>
            </div>
          </div>
        ) : (
          <div className="quick-key-no-project">
            <CircleAlert size={22} />
            <strong>{tx("当前没有可发放 Key 的项目")}</strong>
            <p>{tx("请联系项目负责人或管理员将你加入项目并授予发放 Key 权限。")}</p>
          </div>
        )}

        <footer className="quick-key-modal-actions">
          <button className="secondary-button" disabled={loading} onClick={onClose} type="button">
            {hasCreateScope ? tx("取消") : tx("关闭")}
          </button>
          {hasCreateScope ? (
            <button className="button" disabled={!canCreate || loading} type="submit">
              {loading ? tx("发放中") : tx("创建 Key")}
            </button>
          ) : null}
        </footer>
      </form>
    </div>
  );
}

function QuickAccessStepPanel({
  step,
  title,
  summary,
  completed = false,
  disabled = false,
  open,
  onToggle,
  children,
}: {
  step: string;
  title: string;
  summary?: string;
  completed?: boolean;
  disabled?: boolean;
  open: boolean;
  onToggle: () => void;
  children: React.ReactNode;
}) {
  return (
    <section className={`quick-access-step${open ? " open" : ""}${disabled ? " disabled" : ""}`}>
      <button
        aria-expanded={open}
        className="quick-access-step-toggle"
        disabled={disabled}
        onClick={onToggle}
        type="button"
      >
        <span className="quick-access-step-title">
          <strong>Step {step}</strong>
          <span>{tx(title)}</span>
          {completed ? <Check className="quick-access-complete" aria-label={tx("已完成")} size={18} strokeWidth={2.4} /> : null}
        </span>
        <span className="quick-access-step-meta">
          {summary ? <small>{summary}</small> : null}
          <ChevronDown aria-hidden="true" size={17} />
        </span>
      </button>
      {open ? <div className="quick-access-step-content">{children}</div> : null}
    </section>
  );
}

function KeyStep({
  keys,
  onCreateKey,
  onManageKeys,
}: {
  keys: APIKey[];
  onCreateKey: () => void;
  onManageKeys: () => void;
}) {
  const [query, setQuery] = useState("");
  const normalizedQuery = query.trim().toLowerCase();
  const visibleKeys = keys.filter((key) => [key.name, key.key_prefix, key.key_suffix, key.project_id]
    .join(" ")
    .toLowerCase()
    .includes(normalizedQuery));

  return (
    <div className="quick-access-key-step">
      <p>{tx("调用模型服务需要使用 API Key 进行身份鉴权。完整 Key 仅在创建时展示，请妥善保存。")}</p>
      <label className="quick-access-search quick-access-key-search">
        <Search size={16} />
        <input
          aria-label={tx("搜索 API Key")}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={tx("输入 API Key 名称搜索")}
          type="search"
          value={query}
        />
      </label>

      <div className="quick-access-key-table-wrap">
        <table className="quick-access-key-table">
          <thead>
            <tr>
              <th>{tx("名称")}</th>
              <th>API Key</th>
              <th>{tx("模型范围")}</th>
              <th>{tx("IP 白名单")}</th>
              <th>{tx("操作")}</th>
            </tr>
          </thead>
          <tbody>
            {visibleKeys.map((key) => (
              <tr key={key.id}>
                <td><strong>{key.name}</strong><span className={`quick-access-key-status ${key.status}`}>{tx(key.status === "active" ? "启用" : "停用")}</span></td>
                <td><code>{maskedKey(key)}</code></td>
                <td>{key.allowed_models?.length ? key.allowed_models.join(", ") : tx("全部可路由模型")}</td>
                <td>{key.ip_allowlist?.length ? key.ip_allowlist.join(", ") : tx("不限")}</td>
                <td><button className="quick-access-text-button" onClick={onManageKeys} type="button">{tx("管理")}</button></td>
              </tr>
            ))}
          </tbody>
        </table>

        {visibleKeys.length === 0 ? (
          <div className="quick-access-key-empty">
            <span className="quick-access-key-empty-icon" aria-hidden="true"><KeyRound size={25} /></span>
            <strong>{keys.length ? tx("未找到匹配的 API Key") : tx("你还没有创建任何 API Key")}</strong>
            <p>{keys.length ? tx("请尝试其他名称或清空搜索条件。") : tx("创建后即可通过 API Key 调用 TokenHub 提供的模型和推理服务。")}</p>
            {!keys.length ? (
              <button className="quick-access-text-button create" onClick={onCreateKey} type="button">
                <Plus size={15} />{tx("创建 API Key")}
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function ModelStep({
  data,
  models,
  previewModel,
  onPreview,
  onConfirm,
}: {
  data: AppData;
  models: Model[];
  previewModel?: Model;
  onPreview: (name: string) => void;
  onConfirm: () => void;
}) {
  const [query, setQuery] = useState("");
  const normalizedQuery = query.trim().toLowerCase();
  const visibleModels = models.filter((model) => [model.name, modelDisplayTitle(model), model.family, model.modality, ...(model.capabilities ?? [])]
    .join(" ")
    .toLowerCase()
    .includes(normalizedQuery));

  return (
    <div className="quick-access-model-step">
      <p>{tx("从当前账号可用的模型中选择一个，确认后会生成对应的接入示例。")}</p>
      <label className="quick-access-search">
        <Search size={16} />
        <input
          aria-label={tx("搜索模型")}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={tx("搜索模型名称或能力")}
          type="search"
          value={query}
        />
      </label>

      {models.length ? (
        <>
          <div className="quick-access-model-picker">
            <div className="quick-access-model-list" role="listbox" aria-label={tx("可用模型")}>
              {visibleModels.map((model) => (
                <ModelOption
                  key={model.id}
                  model={model}
                  selected={previewModel?.name === model.name}
                  onSelect={() => onPreview(model.name)}
                />
              ))}
              {!visibleModels.length ? <p className="quick-access-no-model-results">{tx("未找到匹配的模型")}</p> : null}
            </div>
            <div className="quick-access-model-detail">
              {previewModel ? <ModelDetail data={data} model={previewModel} /> : <p>{tx("请从左侧选择模型查看详情")}</p>}
            </div>
          </div>

          <div className="quick-access-model-confirm">
            <div>
              <span>{tx("已选模型")}</span>
              <strong>{previewModel ? modelDisplayTitle(previewModel) : "-"}</strong>
              {previewModel ? <code>{previewModel.name}</code> : null}
            </div>
            <button className="button" disabled={!previewModel} onClick={onConfirm} type="button">
              {tx("选择此模型并继续")}<ArrowRight size={16} />
            </button>
          </div>
        </>
      ) : (
        <div className="quick-access-model-empty">
          <CircleAlert size={24} />
          <strong>{tx("当前没有可用模型")}</strong>
          <p>{tx("请联系管理员启用模型目录并配置可用的 Provider 路由。")}</p>
        </div>
      )}
    </div>
  );
}

function ModelOption({ model, selected, onSelect }: { model: Model; selected: boolean; onSelect: () => void }) {
  const category = modelCategory(model);
  const icon = modelBrandIconSource(category);
  const capabilities = (model.capabilities ?? []).slice(0, 2);
  return (
    <button
      aria-selected={selected}
      className={`quick-access-model-option${selected ? " selected" : ""}`}
      onClick={onSelect}
      role="option"
      type="button"
    >
      <span className="quick-access-model-icon" aria-hidden="true">
        {icon ? <img alt="" src={icon} /> : modelCategoryInitial(category, model.name)}
      </span>
      <span className="quick-access-model-option-copy">
        <strong>{modelDisplayTitle(model)}</strong>
        <small>{model.name}</small>
      </span>
      <span className="quick-access-model-tags">
        {capabilities.map((capability) => <em key={capability}>{tx(capability)}</em>)}
      </span>
    </button>
  );
}

function ModelDetail({ model, data }: { model: Model; data: AppData }) {
  const category = modelCategory(model);
  const availability = modelAvailabilitySummary(model, data, true);
  const capabilities = modelCapabilitySummary(model);
  return (
    <div>
      <div className="quick-access-model-detail-head">
        <span className="quick-access-model-icon large" aria-hidden="true">
          {modelBrandIconSource(category) ? <img alt="" src={modelBrandIconSource(category)} /> : modelCategoryInitial(category, model.name)}
        </span>
        <div>
          <h3>{modelDisplayTitle(model)}</h3>
          <code>{model.name}</code>
        </div>
        <span className={`quick-access-availability ${availability.tone}`}>{tx(availability.label)}</span>
      </div>
      <p className="quick-access-model-description">{model.metadata?.description || tx(availability.detail)}</p>

      <dl className="quick-access-model-facts">
        <div><dt>{tx("模型系列")}</dt><dd>{model.family || modelCategoryLabel(category)}</dd></div>
        <div><dt>{tx("上下文窗口")}</dt><dd>{model.context_window ? `${formatNumber(model.context_window)} tokens` : "-"}</dd></div>
        <div><dt>{tx("参考价格")}</dt><dd>{modelPriceSummary(model)}</dd></div>
        <div><dt>{tx("模型能力")}</dt><dd>{capabilities || "-"}</dd></div>
      </dl>
    </div>
  );
}

function UsageStep({ api, keyHint, model }: { api: ApiContext; keyHint: string; model: Model }) {
  const [mode, setMode] = useState<UsageMode>("api");
  const [apiStyle, setAPIStyle] = useState<APIStyle>("chat");
  const [toolStyle, setToolStyle] = useState<ToolStyle>("openai");
  const [language, setLanguage] = useState<CodeLanguage>("curl");
  const baseURL = apiGatewayBaseURL(api.baseURL);
  const code = mode === "api"
    ? apiExamples(baseURL, model.name, apiStyle)[language]
    : toolExamples(baseURL, model.name, toolStyle)[language];

  return (
    <div className="quick-access-usage-step">
      <div className="quick-access-tabs" role="tablist" aria-label={tx("使用方式")}>
        <button aria-selected={mode === "api"} className={mode === "api" ? "active" : ""} onClick={() => setMode("api")} role="tab" type="button">
          <Terminal size={16} />{tx("API 接入")}
        </button>
        <button aria-selected={mode === "tools"} className={mode === "tools" ? "active" : ""} onClick={() => { setMode("tools"); setLanguage("curl"); }} role="tab" type="button">
          <Bot size={16} />{tx("AI 工具接入")}
        </button>
      </div>

      <div className="quick-access-usage-toolbar">
        {mode === "api" ? (
          <div className="quick-access-segmented" aria-label={tx("API 类型")}>
            <button className={apiStyle === "chat" ? "active" : ""} onClick={() => setAPIStyle("chat")} type="button">Chat Completions</button>
            <button className={apiStyle === "responses" ? "active" : ""} onClick={() => setAPIStyle("responses")} type="button">Responses API</button>
            <button className={apiStyle === "messages" ? "active" : ""} onClick={() => setAPIStyle("messages")} type="button">Anthropic Messages</button>
          </div>
        ) : (
          <div className="quick-access-segmented" aria-label={tx("AI 工具类型")}>
            <button className={toolStyle === "openai" ? "active" : ""} onClick={() => setToolStyle("openai")} type="button">{tx("OpenAI 兼容工具")}</button>
            <button className={toolStyle === "claude" ? "active" : ""} onClick={() => setToolStyle("claude")} type="button">Claude Code</button>
          </div>
        )}
        <span>{tx("当前模型")} <code>{model.name}</code></span>
      </div>

      <p className="quick-access-key-notice">
        <KeyRound size={15} />
        <span>{tx("示例使用环境变量保存完整 Key。当前凭证：")} <code>{keyHint}</code></span>
      </p>

      <div className="quick-access-code-shell">
        <div className="quick-access-code-tabs" role="tablist" aria-label={tx("代码语言")}>
          {(mode === "api"
            ? (["curl", "python", "node"] as CodeLanguage[])
            : (["curl", "node"] as CodeLanguage[])
          ).map((item) => (
            <button aria-selected={language === item} className={language === item ? "active" : ""} key={item} onClick={() => setLanguage(item)} role="tab" type="button">
              {item === "curl" ? (mode === "api" ? "cURL" : tx("环境变量")) : item === "node" ? (mode === "api" ? "Node.js" : "JSON") : "Python"}
            </button>
          ))}
        </div>
        <GatewayCodeBlock code={code} />
      </div>
    </div>
  );
}

function maskedKey(key?: APIKey) {
  if (!key) return "YOUR_TOKENHUB_API_KEY";
  return `${key.key_prefix}...${key.key_suffix}`;
}

function apiExamples(baseURL: string, model: string, style: APIStyle): Record<CodeLanguage, string> {
  const endpoint = style === "chat" ? "chat/completions" : style === "responses" ? "responses" : "messages";
  const body = style === "chat" ? `{
    "model": "${model}",
    "messages": [
      {"role": "user", "content": "Hello, TokenHub"}
    ]
  }` : style === "responses" ? `{
    "model": "${model}",
    "input": "Hello, TokenHub"
  }` : `{
    "model": "${model}",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Hello, TokenHub"}
    ]
  }`;
  const pythonCall = style === "chat" ? `client.chat.completions.create(
    model="${model}",
    messages=[{"role": "user", "content": "Hello, TokenHub"}],
)` : style === "responses" ? `client.responses.create(
    model="${model}",
    input="Hello, TokenHub",
)` : `client.messages.create(
    model="${model}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello, TokenHub"}],
)`;
  const nodeCall = style === "chat" ? `client.chat.completions.create({
  model: "${model}",
  messages: [{ role: "user", content: "Hello, TokenHub" }],
})` : style === "responses" ? `client.responses.create({
  model: "${model}",
  input: "Hello, TokenHub",
})` : `client.messages.create({
  model: "${model}",
  max_tokens: 1024,
  messages: [{ role: "user", content: "Hello, TokenHub" }],
})`;
  const anthropicHostURL = baseURL.replace(/\/v1\/?$/, "");
  return {
    curl: `curl -X POST '${baseURL}/${endpoint}' \\
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \\
${style === "messages" ? "  -H 'anthropic-version: 2023-06-01' \\\n" : ""}  -H 'Content-Type: application/json' \\
  -d '${body}'`,
    python: style === "messages" ? `import os
from anthropic import Anthropic

client = Anthropic(
    api_key=os.environ["TOKENHUB_API_KEY"],
    base_url="${anthropicHostURL}",
)

response = ${pythonCall}
print(response)` : `import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["TOKENHUB_API_KEY"],
    base_url="${baseURL}",
)

response = ${pythonCall}
print(response)`,
    node: style === "messages" ? `import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "${anthropicHostURL}",
});

const response = await ${nodeCall};
console.log(response);` : `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "${baseURL}",
});

const response = await ${nodeCall};
console.log(response);`,
  };
}

function toolExamples(baseURL: string, model: string, style: ToolStyle): Record<CodeLanguage, string> {
  if (style === "claude") {
    const anthropicHostURL = baseURL.replace(/\/v1\/?$/, "");
    return {
      curl: `export ANTHROPIC_BASE_URL="${anthropicHostURL}"
export ANTHROPIC_AUTH_TOKEN="$TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="${model}"

claude`,
      node: `{
  "env": {
    "ANTHROPIC_BASE_URL": "${anthropicHostURL}",
    "ANTHROPIC_AUTH_TOKEN": "${"${TOKENHUB_API_KEY}"}",
    "ANTHROPIC_MODEL": "${model}"
  }
}`,
      python: "",
    };
  }
  return {
    curl: `export OPENAI_API_KEY="$TOKENHUB_API_KEY"
export OPENAI_BASE_URL="${baseURL}"
export OPENAI_MODEL="${model}"

# Start an OpenAI-compatible AI tool in this shell.`,
    node: `{
  "env": {
    "OPENAI_API_KEY": "${"${TOKENHUB_API_KEY}"}",
    "OPENAI_BASE_URL": "${baseURL}",
    "OPENAI_MODEL": "${model}"
  }
}`,
    python: "",
  };
}
