import type { AppData, Model, ModelRoute, SemanticRoutingCandidate, SemanticRoutingPolicy } from "../core/types";
import { formatTranslationTemplate, tx } from "../i18n/runtime";

export const semanticRoutingMetadataKey = "tokenhub_semantic_routing";
export const defaultJevInstructions = "Choose the candidate whose configured criteria best match the latest user task.";

export function readSemanticRoutingPolicy(model: Model): SemanticRoutingPolicy {
  const fallback: SemanticRoutingPolicy = { mode: "off", min_confidence: 0.65 };
  try {
    const parsed = JSON.parse(model.metadata?.[semanticRoutingMetadataKey] ?? "null");
    if (parsed?.instructions !== undefined && typeof parsed.instructions !== "string") return fallback;
    if (parsed?.default_candidate_id !== undefined && typeof parsed.default_candidate_id !== "string") return fallback;
    if (parsed?.candidates !== undefined && (!Array.isArray(parsed.candidates) || !parsed.candidates.every((candidate: unknown) => {
      if (!candidate || typeof candidate !== "object") return false;
      const fields = candidate as Record<string, unknown>;
      return ["id", "provider_id", "provider_model", "criteria"].every(key => typeof fields[key] === "string");
    }))) return fallback;
    if (parsed && ["off", "shadow", "enforce"].includes(parsed.mode) && typeof parsed.min_confidence === "number" && Number.isFinite(parsed.min_confidence) && parsed.min_confidence >= 0 && parsed.min_confidence <= 1) return parsed;
  } catch { /* Invalid legacy metadata leaves semantic routing disabled. */ }
  return fallback;
}

export function jevModelOptions(routes: ModelRoute[], data: AppData, saved: SemanticRoutingPolicy): SemanticRoutingCandidate[] {
  const unique = new Map<string, SemanticRoutingCandidate>();
  for (const route of routes) {
    const key = JSON.stringify([route.provider_id, route.provider_model]);
    if (unique.has(key)) continue;
    const previous = saved.candidates?.find(candidate => candidate.provider_id === route.provider_id && candidate.provider_model === route.provider_model);
    const catalog = data.providerModels.find(model => model.provider_id === route.provider_id && model.upstream_model === route.provider_model);
    unique.set(key, previous ?? { id: route.id, provider_id: route.provider_id, provider_model: route.provider_model, criteria: catalog?.metadata?.routing_description ?? "" });
  }
  return [...unique.values()];
}

export function initialJevPolicy(model: Model, routes: ModelRoute[], data: AppData): SemanticRoutingPolicy {
  const saved = readSemanticRoutingPolicy(model);
  const options = jevModelOptions(routes, data, saved);
  const candidates = saved.candidates ? saved.candidates.flatMap(candidate => options.filter(option => option.id === candidate.id)) : options;
  return { ...saved, instructions: saved.instructions ?? defaultJevInstructions, candidates, default_candidate_id: saved.default_candidate_id ?? candidates[0]?.id ?? "" };
}

export function validJevPolicy(value: SemanticRoutingPolicy) {
  const bytes = (text: string) => new TextEncoder().encode(text).length;
  const candidates = value.candidates ?? [];
  return Number.isFinite(value.min_confidence) && value.min_confidence >= 0 && value.min_confidence <= 1
    && !!value.instructions?.trim() && bytes(value.instructions) <= 4096
    && candidates.length > 0 && candidates.length <= 32
    && candidates.some(candidate => candidate.id === value.default_candidate_id)
    && candidates.every(candidate => !!candidate.criteria.trim() && bytes(candidate.criteria) <= 2048);
}

export function SemanticRoutingFields({ value, routes, data, disabled, onChange }: {
  value: SemanticRoutingPolicy;
  routes: ModelRoute[];
  data: AppData;
  disabled: boolean;
  onChange: (value: SemanticRoutingPolicy) => void;
}) {
  const options = jevModelOptions(routes, data, value);
  const candidates = value.candidates ?? [];
  const name = (candidate: SemanticRoutingCandidate) => `${candidate.provider_model} · ${data.providers.find(provider => provider.id === candidate.provider_id)?.name ?? candidate.provider_id}`;
  function toggle(candidate: SemanticRoutingCandidate, checked: boolean) {
    const next = checked ? [...candidates, candidate] : candidates.filter(item => item.id !== candidate.id);
    onChange({ ...value, candidates: next, default_candidate_id: next.some(item => item.id === value.default_candidate_id) ? value.default_candidate_id : next[0]?.id ?? "" });
  }
  return (
    <fieldset className="semantic-routing-fields" disabled={disabled}>
      <legend>{tx("Jev 智能路由设置")}</legend>
      <p>{tx("根据请求内容选择候选模型，再由 TokenHub 调用目标模型。适用于 Chat Completions 和 Responses，支持流式输出。")}</p>
      <label className="field">
        <span>{tx("模型选择指令")}</span>
        <textarea rows={2} value={value.instructions ?? ""} onChange={event => onChange({ ...value, instructions: event.target.value })} />
      </label>
      <div className="form-grid">
        <label className="field">
          <span>{tx("默认模型")}</span>
          <select value={value.default_candidate_id ?? ""} onChange={event => onChange({ ...value, default_candidate_id: event.target.value })}>
            <option value="">{tx("请选择默认模型")}</option>
            {candidates.map(candidate => <option key={candidate.id} value={candidate.id}>{name(candidate)}</option>)}
          </select>
        </label>
        <label className="field">
          <span>{tx("最低置信度")}</span>
          <input type="number" min="0" max="1" step="0.01" value={Number.isFinite(value.min_confidence) ? value.min_confidence : ""} onChange={event => onChange({ ...value, min_confidence: event.target.value === "" ? NaN : Number(event.target.value) })} />
        </label>
      </div>
      <p>{tx("超时或低置信度时优先使用默认模型；默认模型不可用时按候选列表顺序回退。置信度不代表任务成功率。")}</p>
      <div className="jev-candidates">
        {options.length === 0 ? <p>{tx("请先添加模型线路，再配置候选模型。")}</p> : options.map(option => {
          const selected = candidates.find(candidate => candidate.id === option.id);
          return <div className="jev-candidate" key={option.id}>
            <label className="jev-candidate-toggle"><input type="checkbox" checked={!!selected} onChange={event => toggle(option, event.target.checked)} /><strong>{name(option)}</strong></label>
            {selected ? <label className="field">
              <span>{tx("适用条件")}</span>
              <textarea aria-label={formatTranslationTemplate(tx("{model} 的适用条件"), { model: name(option) })} rows={2} value={selected.criteria} onChange={event => onChange({ ...value, candidates: candidates.map(candidate => candidate.id === option.id ? { ...candidate, criteria: event.target.value } : candidate) })} />
            </label> : null}
          </div>;
        })}
      </div>
      <p>{tx("候选模型来自当前统一模型的已有线路。同一 Provider 下同一模型的多个账号合并为一个选项。")}</p>
      <p>{tx("服务端须开启 Jev 并允许当前项目向 TypeSafe 发送必要的用户文本。已有会话绑定和 Responses 续接优先保持原线路。")}</p>
    </fieldset>
  );
}
