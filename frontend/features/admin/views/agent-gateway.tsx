"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Bot, ChevronRight, History, KeyRound, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { type ApiContext } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";

type AgentInstance = {
	id: string;
	name: string;
	url: string;
	protocol_binding: string;
	protocol_version: string;
	status: string;
	healthy: boolean;
	priority: number;
	weight: number;
	max_concurrency: number;
	fixed_cost_usd: number;
	allowed_forward_headers?: string[];
};

type AgentSkill = {
	skill_id: string;
	name: string;
	description: string;
};

type AgentCard = {
	name: string;
	description: string;
	version: string;
	capabilities?: { streaming?: boolean };
};

type Agent = {
	id: string;
	slug: string;
	name: string;
	description: string;
	version: string;
	status: string;
	source: string;
	updated_at: string;
	card: AgentCard;
	instances: AgentInstance[];
	skills: AgentSkill[];
};

type AgentBinding = {
	id: string;
	agent_id: string;
	scope_type: string;
	scope_id: string;
	effect: string;
	skills?: string[];
	status: string;
};

type AgentRevision = {
	id: string;
	revision: number;
	source: string;
	created_by?: string;
	created_at: string;
};

type AgentExecution = {
	id: string;
	root_agent_id: string;
	trace_id: string;
	status: string;
	agent_hops: number;
	model_calls: number;
	mcp_calls: number;
	tokens: number;
	cost_usd: number;
	created_at: string;
};

const emptyRegistration = {
	slug: "",
	cardURL: "",
	upstreamURL: "",
	headers: "{}",
	priority: "0",
	weight: "1",
	maxConcurrency: "0",
	fixedCostUSD: "0",
	allowedForwardHeaders: "X-Request-ID, traceparent",
};

export function AgentGatewayView({ api }: { api: ApiContext }) {
	const [agents, setAgents] = useState<Agent[]>([]);
	const [bindings, setBindings] = useState<AgentBinding[]>([]);
	const [selectedID, setSelectedID] = useState("");
	const [revisions, setRevisions] = useState<AgentRevision[]>([]);
	const [executions, setExecutions] = useState<AgentExecution[]>([]);
	const [registration, setRegistration] = useState(emptyRegistration);
	const [binding, setBinding] = useState({ scopeType: "project", scopeID: "", effect: "allow", skills: "" });
	const [showRegistration, setShowRegistration] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const [notice, setNotice] = useState("");

	const selected = useMemo(() => agents.find((agent) => agent.id === selectedID) ?? agents[0], [agents, selectedID]);
	const selectedBindings = useMemo(() => bindings.filter((item) => item.agent_id === selected?.id), [bindings, selected?.id]);
	const selectedExecutions = useMemo(() => executions.filter((item) => item.root_agent_id === selected?.id), [executions, selected?.id]);
	const loadRevisions = useCallback(async (agentID: string) => {
		const response = await adminFetch(api, `/api/admin/agents/${agentID}/revisions`);
		if (!response.ok) return;
		const payload = (await response.json()) as { data?: AgentRevision[] };
		setRevisions(payload.data ?? []);
	}, [api]);

	const load = useCallback(async () => {
		setBusy(true);
		setError("");
		try {
			const [agentResponse, bindingResponse, executionResponse] = await Promise.all([
				adminFetch(api, "/api/admin/agents"),
				adminFetch(api, "/api/admin/agent-access-bindings"),
				adminFetch(api, "/api/admin/agent-executions?limit=100"),
			]);
			if (!agentResponse.ok) throw new Error(await readAdminError(agentResponse, tx("读取 Agent 列表失败")));
			if (!bindingResponse.ok) throw new Error(await readAdminError(bindingResponse, tx("读取 Agent 访问策略失败")));
			if (!executionResponse.ok) throw new Error(await readAdminError(executionResponse, tx("读取 Agent 执行记录失败")));
			const agentPayload = (await agentResponse.json()) as { data?: Agent[] };
			const bindingPayload = (await bindingResponse.json()) as { data?: AgentBinding[] };
			const executionPayload = (await executionResponse.json()) as { data?: AgentExecution[] };
			setAgents(agentPayload.data ?? []);
			setBindings(bindingPayload.data ?? []);
			setExecutions(executionPayload.data ?? []);
			setSelectedID((current) => current || agentPayload.data?.[0]?.id || "");
		} catch (caught) {
			setError(caught instanceof Error ? caught.message : tx("读取 Agent 列表失败"));
		} finally {
			setBusy(false);
		}
	}, [api]);

	useEffect(() => {
		void load();
	}, [load]);

	useEffect(() => {
		if (!selected?.id) {
			setRevisions([]);
			return;
		}
		void loadRevisions(selected.id);
	}, [loadRevisions, selected?.id]);

	async function registerAgent() {
		setError("");
		setNotice("");
		let headers: Record<string, string>;
		try {
			headers = JSON.parse(registration.headers) as Record<string, string>;
			if (!headers || Array.isArray(headers) || typeof headers !== "object" || Object.values(headers).some((value) => typeof value !== "string")) throw new Error("invalid headers");
		} catch {
			setError(tx("静态请求头必须是 JSON 对象"));
			return;
		}
		setBusy(true);
		try {
			const response = await adminFetch(api, "/api/admin/agents", {
				method: "POST",
				headers: { "content-type": "application/json" },
				body: JSON.stringify({
					slug: registration.slug,
					card_url: registration.cardURL,
					upstream_url: registration.upstreamURL || undefined,
					headers,
					priority: Number(registration.priority) || 0,
					weight: Number(registration.weight) || 1,
					max_concurrency: Number(registration.maxConcurrency) || 0,
					fixed_cost_usd: Number(registration.fixedCostUSD) || 0,
					allowed_forward_headers: registration.allowedForwardHeaders.split(",").map((item) => item.trim()).filter(Boolean),
				}),
			});
			if (!response.ok) throw new Error(await readAdminError(response, tx("注册 Agent 失败")));
			const registered = (await response.json()) as Agent;
			setRegistration(emptyRegistration);
			setShowRegistration(false);
			setNotice(tx("Agent 已注册并生成新版本"));
			await load();
			setSelectedID(registered.id);
			await loadRevisions(registered.id);
		} catch (caught) {
			setError(caught instanceof Error ? caught.message : tx("注册 Agent 失败"));
		} finally {
			setBusy(false);
		}
	}

	async function createBinding() {
		if (!selected || !binding.scopeID.trim()) {
			setError(tx("请填写访问范围标识"));
			return;
		}
		setBusy(true);
		setError("");
		try {
			const response = await adminFetch(api, "/api/admin/agent-access-bindings", {
				method: "POST",
				headers: { "content-type": "application/json" },
				body: JSON.stringify({
					agent_id: selected.id,
					scope_type: binding.scopeType,
					scope_id: binding.scopeID.trim(),
					effect: binding.effect,
					skills: binding.skills.split(",").map((item) => item.trim()).filter(Boolean),
					status: "active",
				}),
			});
			if (!response.ok) throw new Error(await readAdminError(response, tx("保存 Agent 访问策略失败")));
			setBinding((current) => ({ ...current, scopeID: "", skills: "" }));
			setNotice(tx("Agent 访问策略已保存"));
			await load();
		} catch (caught) {
			setError(caught instanceof Error ? caught.message : tx("保存 Agent 访问策略失败"));
		} finally {
			setBusy(false);
		}
	}

	async function removeBinding(id: string) {
		const response = await adminFetch(api, `/api/admin/agent-access-bindings/${id}`, { method: "DELETE" });
		if (!response.ok) {
			setError(await readAdminError(response, tx("删除 Agent 访问策略失败")));
			return;
		}
		setNotice(tx("Agent 访问策略已删除"));
		await load();
	}

	async function restoreRevision(revision: AgentRevision) {
		if (!selected) return;
		setBusy(true);
		const response = await adminFetch(api, `/api/admin/agents/${selected.id}/revisions/${revision.id}`, { method: "POST" });
		if (!response.ok) {
			setError(await readAdminError(response, tx("回滚 Agent 版本失败")));
		} else {
			setNotice(tx("Agent 版本已回滚"));
			await load();
			await loadRevisions(selected.id);
		}
		setBusy(false);
	}

	return (
		<div className="agent-gateway-view">
			<div className="agent-gateway-toolbar">
				<div className="agent-gateway-summary">
					<span><Bot size={17} /> {agents.length} {tx("个 Agent")}</span>
					<span><ShieldCheck size={17} /> {bindings.length} {tx("条访问策略")}</span>
					<span><History size={17} /> {executions.filter((item) => item.status === "running").length} {tx("个运行中执行")}</span>
				</div>
				<div className="agent-gateway-actions">
					<button className="secondary" type="button" disabled={busy} onClick={() => void load()}><RefreshCw size={16} />{tx("刷新")}</button>
					<button className="primary" type="button" onClick={() => setShowRegistration((value) => !value)}><Plus size={16} />{tx("注册 Agent")}</button>
				</div>
			</div>

			{error ? <div className="agent-gateway-message error">{error}</div> : null}
			{notice ? <div className="agent-gateway-message notice">{notice}</div> : null}

			{showRegistration ? (
				<section className="agent-gateway-panel agent-registration-panel">
					<div className="agent-gateway-panel-title"><Bot size={18} /><strong>{tx("从 Agent Card 注册")}</strong></div>
					<div className="agent-gateway-form-grid">
						<label><span>{tx("Agent 标识")}</span><input value={registration.slug} placeholder="research-agent" onChange={(event) => setRegistration({ ...registration, slug: event.target.value })} /></label>
						<label><span>{tx("Agent Card 地址")}</span><input value={registration.cardURL} placeholder="https://agent.example/.well-known/agent-card.json" onChange={(event) => setRegistration({ ...registration, cardURL: event.target.value })} /></label>
						<label><span>{tx("上游地址覆盖")}</span><input value={registration.upstreamURL} placeholder={tx("可选，仅用于审核后覆盖 Card 地址")} onChange={(event) => setRegistration({ ...registration, upstreamURL: event.target.value })} /></label>
						<label><span>{tx("优先级")}</span><input type="number" value={registration.priority} onChange={(event) => setRegistration({ ...registration, priority: event.target.value })} /></label>
						<label><span>{tx("权重")}</span><input type="number" min="1" value={registration.weight} onChange={(event) => setRegistration({ ...registration, weight: event.target.value })} /></label>
						<label><span>{tx("最大并发数")}</span><input type="number" min="0" value={registration.maxConcurrency} placeholder={tx("0 表示不限制")} onChange={(event) => setRegistration({ ...registration, maxConcurrency: event.target.value })} /></label>
						<label><span>{tx("固定调用成本（美元）")}</span><input type="number" min="0" step="0.000001" value={registration.fixedCostUSD} onChange={(event) => setRegistration({ ...registration, fixedCostUSD: event.target.value })} /></label>
						<label className="wide"><span>{tx("允许转发的请求头")}</span><input value={registration.allowedForwardHeaders} placeholder={tx("逗号分隔；认证请求头始终禁止转发")} onChange={(event) => setRegistration({ ...registration, allowedForwardHeaders: event.target.value })} /></label>
						<label className="wide"><span>{tx("静态请求头")}</span><textarea value={registration.headers} rows={3} onChange={(event) => setRegistration({ ...registration, headers: event.target.value })} /></label>
					</div>
					<div className="agent-gateway-form-actions"><button className="primary" type="button" disabled={busy || !registration.slug || !registration.cardURL} onClick={() => void registerAgent()}>{tx("获取、校验并注册")}</button></div>
				</section>
			) : null}

			<div className="agent-gateway-layout">
				<aside className="agent-gateway-list">
					{agents.length === 0 ? <div className="agent-gateway-empty">{tx("尚未注册 Agent")}</div> : agents.map((agent) => (
						<button type="button" key={agent.id} className={agent.id === selected?.id ? "active" : ""} onClick={() => setSelectedID(agent.id)}>
							<span className={`agent-health ${agent.instances.some((instance) => instance.healthy) ? "healthy" : "unhealthy"}`} />
							<span><strong>{agent.name}</strong><small>agent/{agent.slug} · {agent.version}</small></span>
							<ChevronRight size={16} />
						</button>
					))}
				</aside>

				{selected ? (
					<main className="agent-gateway-details">
						<section className="agent-gateway-panel agent-overview-card">
							<div>
								<div className="agent-gateway-panel-title"><Bot size={18} /><strong>{selected.name}</strong><span className="agent-source-badge">{selected.source === "config" ? tx("配置托管") : tx("控制台托管")}</span></div>
								<p>{selected.description || tx("暂无描述")}</p>
							</div>
							<dl><div><dt>{tx("A2A 版本")}</dt><dd>1.0</dd></div><div><dt>{tx("调用模型名")}</dt><dd>agent/{selected.slug}</dd></div><div><dt>{tx("状态")}</dt><dd>{selected.status}</dd></div></dl>
						</section>

						<section className="agent-gateway-panel">
							<div className="agent-gateway-panel-title"><RefreshCw size={18} /><strong>{tx("运行实例")}</strong></div>
							<div className="agent-instance-list">{selected.instances.map((instance) => (
								<div key={instance.id}><span className={`agent-health ${instance.healthy ? "healthy" : "unhealthy"}`} /><div><strong>{instance.name}</strong><small>{instance.url}</small><small>{tx("优先级")} {instance.priority} · {tx("权重")} {instance.weight} · {tx("最大并发数")} {instance.max_concurrency || tx("不限制")}</small></div><em>{instance.protocol_binding} {instance.protocol_version}<br />${instance.fixed_cost_usd || 0}</em></div>
							))}</div>
						</section>

						<section className="agent-gateway-panel">
							<div className="agent-gateway-panel-title"><History size={18} /><strong>{tx("最近执行")}</strong><span>{tx("统一记录 Agent、模型与 MCP 用量")}</span></div>
							<div className="agent-execution-list">{selectedExecutions.length === 0 ? <p>{tx("暂无执行记录")}</p> : selectedExecutions.slice(0, 10).map((execution) => (
								<div key={execution.id}><span className={`agent-execution-status ${execution.status}`}>{execution.status}</span><div><strong>{execution.trace_id}</strong><small>{new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "medium" }).format(new Date(execution.created_at))}</small></div><small>{tx("跳数")} {execution.agent_hops} · {tx("模型调用")} {execution.model_calls} · MCP {execution.mcp_calls} · {new Intl.NumberFormat(languageLocale()).format(execution.tokens)} Token · ${new Intl.NumberFormat(languageLocale(), { minimumFractionDigits: 0, maximumFractionDigits: 6 }).format(execution.cost_usd)}</small></div>
							))}</div>
						</section>

						<section className="agent-gateway-panel">
							<div className="agent-gateway-panel-title"><KeyRound size={18} /><strong>{tx("访问策略")}</strong><span>{tx("默认拒绝；拒绝规则优先")}</span></div>
							<div className="agent-binding-form">
								<select value={binding.scopeType} onChange={(event) => setBinding({ ...binding, scopeType: event.target.value })}><option value="global">{tx("全局")}</option><option value="team">{tx("团队")}</option><option value="project">{tx("项目")}</option><option value="api_key">API Key</option><option value="end_user">{tx("终端用户")}</option><option value="agent">Agent</option><option value="access_group">{tx("访问组")}</option></select>
								<input value={binding.scopeID} placeholder={tx("范围标识；全局使用 *")} onChange={(event) => setBinding({ ...binding, scopeID: event.target.value })} />
								<select value={binding.effect} onChange={(event) => setBinding({ ...binding, effect: event.target.value })}><option value="allow">{tx("允许")}</option><option value="deny">{tx("拒绝")}</option></select>
								<input value={binding.skills} placeholder={tx("技能标识，逗号分隔；留空表示全部")} onChange={(event) => setBinding({ ...binding, skills: event.target.value })} />
								<button className="primary" type="button" disabled={busy} onClick={() => void createBinding()}>{tx("添加策略")}</button>
							</div>
							<div className="agent-binding-list">{selectedBindings.length === 0 ? <p>{tx("尚未配置访问策略，所有调用均会被拒绝。")}</p> : selectedBindings.map((item) => (
								<div key={item.id}><span className={item.effect === "deny" ? "deny" : "allow"}>{item.effect === "deny" ? tx("拒绝") : tx("允许")}</span><strong>{item.scope_type}:{item.scope_id}</strong><small>{item.skills?.join(", ") || tx("全部技能")}</small><button type="button" title={tx("删除策略")} onClick={() => void removeBinding(item.id)}><Trash2 size={15} /></button></div>
							))}</div>
						</section>

						<section className="agent-gateway-panel">
							<div className="agent-gateway-panel-title"><History size={18} /><strong>{tx("版本记录")}</strong></div>
							<div className="agent-revision-list">{revisions.map((revision, index) => (
								<div key={revision.id}><span>#{revision.revision}</span><div><strong>{revision.source === "config" ? tx("配置同步") : tx("控制台变更")}</strong><small>{new Date(revision.created_at).toLocaleString(languageLocale())}</small></div>{index === 0 ? <em>{tx("当前版本")}</em> : <button type="button" disabled={selected.source === "config" || busy} onClick={() => void restoreRevision(revision)}>{tx("回滚到此版本")}</button>}</div>
							))}</div>
						</section>
					</main>
				) : null}
			</div>
		</div>
	);
}
