import { PlayCircle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { type ApiContext, type AppData } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, StatusPill } from "../shared/ui";

type CandidateDecision = {
  route_id: string;
  provider_id: string;
  resource_id?: string;
  provider_model: string;
  allowed: boolean;
  reasons: string[];
};

type RoutingPolicyResolution = {
  effective_policy?: { id: string; name: string; scope: string; scope_id?: string; priority: number };
  match_reason: string;
  candidates: CandidateDecision[];
  selected_route_id?: string;
  error_code?: string;
  error_message?: string;
  access_allowed: boolean;
  access_reasons?: string[];
};

export function RoutingPolicySimulator({ api, data }: { api: ApiContext; data: AppData }) {
  const [projectID, setProjectID] = useState("");
  const [keyID, setKeyID] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<RoutingPolicyResolution | null>(null);
  const keys = useMemo(() => data.keys.filter((key) => !projectID || key.project_id === projectID), [data.keys, projectID]);

  useEffect(() => {
    if (!projectID && data.projects[0]) setProjectID(data.projects[0].id);
    if (!model && data.models[0]) setModel(data.models[0].name);
  }, [data.models, data.projects, model, projectID]);

  useEffect(() => {
    if (!keys.some((key) => key.id === keyID)) setKeyID(keys[0]?.id ?? "");
  }, [keyID, keys]);

  async function simulate(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const response = await adminFetch(api, "/api/admin/routing-policies/simulate", {
        method: "POST",
        body: JSON.stringify({ project_id: projectID, api_key_id: keyID, model }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("策略模拟失败")));
      setResult((await response.json()) as RoutingPolicyResolution);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : tx("策略模拟失败"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <DataSection title="策略预览 / 模拟">
      <form className="routing-policy-simulator" onSubmit={simulate}>
        <div className="routing-policy-simulator-fields">
          <label className="field">
            <span>{tx("项目")}</span>
            <select required value={projectID} onChange={(event) => setProjectID(event.target.value)}>
              <option value="">{tx("请选择")}</option>
              {data.projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
            </select>
          </label>
          <label className="field">
            <span>{tx("API Key")}</span>
            <select required value={keyID} onChange={(event) => setKeyID(event.target.value)}>
              <option value="">{tx("请选择")}</option>
              {keys.map((key) => <option key={key.id} value={key.id}>{key.name} · {key.key_prefix}...{key.key_suffix}</option>)}
            </select>
          </label>
          <label className="field">
            <span>{tx("模型")}</span>
            <select required value={model} onChange={(event) => setModel(event.target.value)}>
              <option value="">{tx("请选择")}</option>
              {data.models.filter((item) => item.status === "active").map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
            </select>
          </label>
          <button className="button routing-policy-simulate-button" disabled={busy || !projectID || !keyID || !model} type="submit">
            <PlayCircle size={16} />{tx(busy ? "模拟中" : "运行模拟")}
          </button>
        </div>
        {error ? <p className="error-banner">{error}</p> : null}
        {result ? <RoutingPolicySimulationResult data={data} result={result} /> : null}
      </form>
    </DataSection>
  );
}

function RoutingPolicySimulationResult({ data, result }: { data: AppData; result: RoutingPolicyResolution }) {
  const policy = result.effective_policy;
  return (
    <div className="routing-policy-simulation-result">
      <div className="routing-policy-resolution-summary">
        <span><strong>{tx("命中策略")}</strong>{policy ? `${policy.name} · ${tx(policy.scope)} · P${policy.priority}` : tx("无绑定策略")}</span>
        <span><strong>{tx("最终路由")}</strong>{result.selected_route_id || tx("无合格候选")}</span>
        <span><strong>{tx("访问允许列表")}</strong><StatusPill status={result.access_allowed ? "active" : "disabled"} /></span>
      </div>
      {result.error_code ? <p className="routing-policy-diagnostic"><strong>{result.error_code}</strong>{result.error_message}</p> : null}
      {result.access_reasons?.length ? <p className="routing-policy-diagnostic">{result.access_reasons.map(reasonLabel).join(" · ")}</p> : null}
      <div className="routing-policy-candidates">
        {result.candidates.map((candidate) => {
          const provider = data.providers.find((item) => item.id === candidate.provider_id);
          const resource = data.providerResources.find((item) => item.id === candidate.resource_id);
          return (
            <article className={candidate.allowed ? "allowed" : "excluded"} key={`${candidate.route_id}:${candidate.resource_id || "provider"}`}>
              <div><strong>{provider?.name || candidate.provider_id}</strong><span>{resource?.name || candidate.resource_id || candidate.provider_model}</span></div>
              <StatusPill status={candidate.allowed ? "active" : "disabled"} />
              <p>{candidate.reasons.map(reasonLabel).join(" · ")}</p>
            </article>
          );
        })}
      </div>
    </div>
  );
}

function reasonLabel(reason: string) {
  const labels: Record<string, string> = {
    eligible: "符合全部约束",
    project_scope_excluded: "路由项目范围不匹配",
    model_not_allowed: "策略未允许该模型",
    provider_not_allowed: "Provider 不在允许列表",
    resource_not_allowed: "Provider 资源不在允许列表",
    required_tags_missing: "缺少必需标签",
    region_not_allowed: "地域边界不匹配",
    environment_not_allowed: "环境边界不匹配",
    project_model_not_allowed: "项目未允许该模型",
    api_key_model_not_allowed: "API Key 未允许该模型",
  };
  return tx(labels[reason] || reason);
}
