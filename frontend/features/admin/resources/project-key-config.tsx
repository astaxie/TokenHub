import { ChevronDown, Download } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { type AdminResource, type APIKey, type AppData, type FieldConfig, type Project, type ResourceAction, type ResourceConfig } from "../core/types";
import { apiKeyOwnerSelectOptions, apiKeyOwnerUserID, costCenterLabel, costCenterSelectOptions, ownerUserLabel, projectMemberCanIssueLabel, projectMemberProjectSelectOptions, projectMemberRoleLabel, projectMemberRoleOptions, projectName, projectOwnerLabel, projectSelectOptions, projectTeamLabel, stringifyForm, stringifyValue, teamLabel, teamSelectOptions, truthyValue, userSelectOptions } from "../domain/entities";
import { apiGatewayBaseURL } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminDelete, adminFetch, adminMutate, keyPatchPayload, projectQuotaSummary, updateAPIKeyStatus } from "./payloads";
import { StatusPill } from "../shared/ui";

export function projectConfig(): ResourceConfig<Project> {
  return {
    view: "projects",
    title: "项目空间",
    eyebrow: "项目列表",
    description: "项目是企业内部 AI 使用、Key、额度和成本归属的基本单元。",
    createLabel: "新建项目并配置团队",
    columns: [
      { key: "name", label: "项目" },
      { key: "team_id", label: "关联团队", render: (item, ctx) => <ProjectTeamListCell data={ctx} project={item} /> },
      { key: "owner_user_id", label: "负责人", render: (item, ctx) => ownerUserLabel(ctx, item.owner_user_id ?? "") },
      { key: "cost_center", label: "成本中心", render: (item, ctx) => costCenterLabel(ctx, item.cost_center ?? "") },
      { key: "quota", label: "额度", render: (item, ctx) => projectQuotaSummary(ctx, item) },
      { key: "allowed_models", label: "模型访问", render: (item) => modelAccessSummary(item.model_access_mode, item.allowed_models) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      { key: "name", label: "项目名称", required: true },
      { key: "team_id", label: "默认团队", type: "select", optionsFromData: teamSelectOptions, help: "默认团队继续承担成本、审批和责任归属；协作团队在同一个项目工作台中配置。" },
      { key: "owner_user_id", label: "项目负责人", type: "select", optionsFromData: userSelectOptions, help: "负责人默认拥有该项目的 Key 管理权限。" },
      { key: "cost_center", label: "成本中心", type: "select", optionsFromData: costCenterSelectOptions },
      { key: "model_access_mode", label: "模型访问模式", type: "select", options: ["inherit", "restricted"], required: true },
      { key: "allowed_models", label: "项目模型允许列表，逗号分隔", visible: (values) => values.model_access_mode === "restricted", help: "restricted 且留空时禁止项目访问任何模型；所有项目 Key 只能进一步收窄。" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
    ],
    list: (ctx) => ctx.projects,
    create: (ctx, values) => adminMutate(ctx, "/api/admin/projects", "POST", values),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/projects/${item.id}`, "PATCH", values),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/projects/${item.id}`),
    actions: [
      {
        label: "发放 Key",
        title: "为该项目创建内部 API Key",
        modal: (item) => ({
          config: apiKeyConfig(),
          initialValues: {
            project_id: item.id,
            name: `${item.name} Key`,
          },
        }),
      },
    ],
    toForm: (item) => stringifyForm(item),
  };
}

export function ProjectTeamListCell({ data, project }: { data: AppData; project: Project }) {
  const primaryTeamID = project.team_id || project.teams?.find((link) => link.is_primary)?.team_id || "";
  const additionalCount = new Set((project.teams ?? []).map((link) => link.team_id).filter((teamID) => teamID && teamID !== primaryTeamID)).size;
  return (
    <div className="project-list-team-cell">
      <span className={primaryTeamID ? "" : "missing"}>
        <strong>{primaryTeamID ? teamLabel(data, primaryTeamID) : tx("未配置主团队")}</strong>
        {primaryTeamID ? <em>{tx("主团队")}</em> : null}
      </span>
      {additionalCount > 0 ? <small>+{additionalCount} {tx("协作团队")}</small> : null}
    </div>
  );
}

export function projectMemberConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    {
      key: "project_id",
      label: "项目空间",
      type: "select",
      optionsFromData: projectMemberProjectSelectOptions,
      required: true,
      visible: () => false,
    },
    {
      key: "user_id",
      label: "用户",
      type: "select",
      optionsFromData: userSelectOptions,
      required: true,
      readOnlyOnEdit: true,
    },
    {
      key: "role",
      label: "项目角色",
      type: "select",
      optionsFromData: projectMemberRoleOptions,
      required: true,
      help: "owner/maintainer 可管理项目 Key；developer 可发放自己的 Key；viewer 只用于可见和统计。",
    },
    {
      key: "can_issue_keys",
      label: "允许发 Key",
      type: "boolean",
      help: "需要单独给 viewer 或特殊成员开放发 Key 时启用。",
    },
    { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
  ];
  return {
    view: "project-members",
    title: "项目成员",
    eyebrow: "项目成员",
    description: "把用户分配到一个或多个项目空间，并控制项目内 Key 发放权限。",
    createLabel: "分配项目成员",
    columns: [
      { key: "user_id", label: "用户", render: (item, ctx) => ownerUserLabel(ctx, stringifyValue(item.fields?.user_id)) },
      { key: "role", label: "项目角色", render: (item) => projectMemberRoleLabel(stringifyValue(item.fields?.role)) },
      { key: "can_issue_keys", label: "发 Key", render: (item) => projectMemberCanIssueLabel(item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields,
    list: (ctx) => ctx.resources["project-members"] ?? [],
    create: (ctx, values, data) => adminMutate(ctx, "/api/admin/resources/project-members", "POST", projectMemberPayload(values, data)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/resources/project-members/${item.id}`, "PATCH", projectMemberPayload(values, undefined, item)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/resources/project-members/${item.id}`),
    toForm: (item) => ({
      project_id: stringifyValue(item.fields?.project_id),
      user_id: stringifyValue(item.fields?.user_id),
      role: stringifyValue(item.fields?.role) || "developer",
      can_issue_keys: stringifyValue(item.fields?.can_issue_keys || "false"),
      status: item.status,
    }),
  };
}

export function projectMemberInitialValues(project: Project): Record<string, string> {
  return {
    project_id: project.id,
    role: "developer",
    can_issue_keys: "false",
    status: "active",
  };
}

export function projectMemberPayload(values: Record<string, string>, data?: AppData, existing?: AdminResource) {
  const user = data?.users.find((item) => item.id === values.user_id);
  const displayName = user ? user.name || user.username || user.email : values.user_id;
  return {
    name: existing?.name || `${displayName || "项目成员"} 项目成员`,
    status: values.status || existing?.status || "active",
    fields: {
      project_id: values.project_id || stringifyValue(existing?.fields?.project_id),
      user_id: values.user_id || stringifyValue(existing?.fields?.user_id),
      role: values.role || stringifyValue(existing?.fields?.role) || "developer",
      can_issue_keys: truthyValue(values.can_issue_keys),
    },
  };
}

export function apiKeyConfig(): ResourceConfig<APIKey> {
  return {
    view: "api-keys",
    title: "Key 管理",
    eyebrow: "内部 Key 列表",
    description: "按项目发放内部 API Key，限制模型白名单、分钟速率、额度、并发和有效期。",
    createLabel: "发放 Key",
    columns: [
      { key: "name", label: "名称" },
      { key: "project_id", label: "归属项目", render: (item, ctx) => projectName(ctx, item.project_id) },
      { key: "owner_user_id", label: "归属用户", render: (item, ctx) => ownerUserLabel(ctx, apiKeyOwnerUserID(ctx, item)) },
      { key: "project_owner", label: "项目负责人", render: (item, ctx) => projectOwnerLabel(ctx, item.project_id) },
      { key: "project_team", label: "团队", render: (item, ctx) => projectTeamLabel(ctx, item.project_id) },
      { key: "key_prefix", label: "Key", render: (item) => `${item.key_prefix}...${item.key_suffix}` },
      { key: "allowed_models", label: "模型", render: (item) => modelAccessSummary(item.model_access_mode, item.allowed_models) },
      { key: "ip_allowlist", label: "IP 白名单", render: (item) => (item.ip_allowlist ?? []).join(", ") || tx("不限") },
      { key: "rate_limit_rpm", label: "RPM" },
      { key: "token_limit_tpm", label: "TPM" },
      { key: "limits.max_concurrency", label: "并发" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      {
        key: "project_id",
        label: "归属项目",
        type: "select",
        required: true,
        optionsFromData: projectSelectOptions,
        help: "只显示当前账号拥有发 Key 权限的项目；一个人可以被分配到多个项目。",
        readOnlyOnEdit: true,
      },
      {
        key: "owner_user_id",
        label: "归属用户",
        type: "select",
        required: true,
        optionsFromData: apiKeyOwnerSelectOptions,
        help: "Key 的用量会计入该用户；平台管理员发放时请明确选择实际使用人。",
      },
      { key: "name", label: "Key 名称", required: true },
      { key: "group", label: "用途/环境", placeholder: "prod、dev、backend-service" },
      { key: "model_access_mode", label: "模型访问模式", type: "select", options: ["inherit", "restricted"], required: true, help: "inherit 表示继承项目范围；restricted 表示与项目允许列表取交集。" },
      { key: "allowed_models", label: "模型允许列表，逗号分隔", visible: (values) => values.model_access_mode === "restricted", help: "restricted 且留空表示禁止此 Key 访问任何模型。" },
      { key: "ip_allowlist", label: "IP 白名单，逗号分隔", help: "留空表示不限来源 IP。" },
      { key: "rate_limit_rpm", label: "每分钟请求数（RPM）", type: "number", help: "留空表示继承项目或全局策略；填 0 表示 Key 层不额外限速，上级限制仍然生效。若要停止调用，请将 Key 状态设为 disabled。" },
      { key: "token_limit_tpm", label: "每分钟 Token 数（TPM）", type: "number", help: "留空表示继承项目或全局策略；填 0 表示 Key 层不额外限速。流式请求会先预留预算，再按实际用量结算。" },
      { key: "daily_requests", label: "日请求", type: "number" },
      { key: "monthly_requests", label: "月请求", type: "number" },
      { key: "daily_tokens", label: "日 Token", type: "number" },
      { key: "monthly_tokens", label: "月 Token", type: "number" },
      { key: "daily_cost_usd", label: "日成本 USD", type: "number" },
      { key: "monthly_cost_usd", label: "月成本 USD", type: "number" },
      { key: "max_concurrency", label: "最大并发", type: "number" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled", "revoked"], required: true },
    ],
    list: (ctx) => ctx.keys,
    create: async () => undefined,
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/api-keys/${item.id}`, "PATCH", keyPatchPayload(values)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/api-keys/${item.id}`),
    actions: [
      {
        label: "轮换",
        title: "生成新 Key，并立即吊销旧 Key",
        run: async (ctx, item) => {
          const resp = await adminFetch(ctx, `/api/admin/api-keys/${item.id}/rotate`, {
            method: "POST",
            body: JSON.stringify({}),
          });
          if (!resp.ok) throw new Error(`rotate api key ${resp.status}`);
          const payload = (await resp.json()) as { api_key: string };
          window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: payload.api_key }));
        },
        doneMessage: (item) => `${item.name} 已轮换，新 Key 已展示`,
      },
    ],
    toForm: (item) => ({
      project_id: item.project_id,
      owner_user_id: item.owner_user_id || item.metadata?.created_by || "",
      name: item.name,
      group: item.group ?? "default",
      model_access_mode: item.model_access_mode || ((item.allowed_models ?? []).length > 0 ? "restricted" : "inherit"),
      allowed_models: (item.allowed_models ?? []).join(", "),
      ip_allowlist: (item.ip_allowlist ?? []).join(", "),
      rate_limit_rpm: item.rate_limit_rpm === undefined ? "" : String(item.rate_limit_rpm),
      token_limit_tpm: item.token_limit_tpm === undefined ? "" : String(item.token_limit_tpm),
      daily_requests: String(item.limits?.daily_requests ?? ""),
      monthly_requests: String(item.limits?.monthly_requests ?? ""),
      daily_tokens: String(item.limits?.daily_tokens ?? ""),
      monthly_tokens: String(item.limits?.monthly_tokens ?? ""),
      daily_cost_usd: String(item.limits?.daily_cost_usd ?? ""),
      monthly_cost_usd: String(item.limits?.monthly_cost_usd ?? ""),
      max_concurrency: String(item.limits?.max_concurrency ?? ""),
      status: item.status,
    }),
  };
}

function modelAccessSummary(mode: string | undefined, allowed: string[] | undefined) {
  const normalized = mode || ((allowed ?? []).length > 0 ? "restricted" : "inherit");
  if (normalized === "inherit") return tx("继承上级范围");
  return (allowed ?? []).join(", ") || tx("禁止全部模型");
}

export function APIKeyStatusSwitch({
  item,
  onToggle,
}: {
  item: APIKey;
  onToggle: (status: "active" | "disabled") => void;
}) {
  if (item.status !== "active" && item.status !== "disabled") {
    return <StatusPill status={item.status} />;
  }
  const enabled = item.status === "active";
  const nextStatus = enabled ? "disabled" : "active";
  return (
    <button
      aria-checked={enabled}
      className={enabled ? "status-switch active" : "status-switch"}
      onClick={(event) => {
        event.stopPropagation();
        onToggle(nextStatus);
      }}
      role="switch"
      title={enabled ? tx("点击停用 API Key") : tx("点击启用 API Key")}
      type="button"
    >
      <span className="status-switch-track">
        <span className="status-switch-thumb" />
      </span>
      <strong>{enabled ? tx("启用") : tx("停用")}</strong>
    </button>
  );
}

export function APIKeyDownloadMenu({ item, data, baseURL }: { item: APIKey; data: AppData; baseURL: string }) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ left: 0, top: 0 });
  const menuRef = useRef<HTMLDivElement | null>(null);
  const optionsRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const closeMenu = () => setOpen(false);
    const close = (event: MouseEvent) => {
      const target = event.target as Node;
      if (!menuRef.current?.contains(target) && !optionsRef.current?.contains(target)) closeMenu();
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeMenu();
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", closeMenu);
    window.addEventListener("scroll", closeMenu, true);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", closeMenu);
      window.removeEventListener("scroll", closeMenu, true);
    };
  }, [open]);

  function toggleMenu() {
    if (!open && triggerRef.current) {
      const rect = triggerRef.current.getBoundingClientRect();
      const menuHeight = 112;
      const openBelow = window.innerHeight - rect.bottom >= menuHeight + 12;
      setPosition({
        left: Math.max(8, rect.right - 248),
        top: openBelow ? rect.bottom + 6 : Math.max(8, rect.top - menuHeight - 6),
      });
    }
    setOpen((current) => !current);
  }

  function downloadConfig() {
    downloadTextTemplate(
      codexConfigTemplate(item, data, baseURL),
      `${templateFilename(item.name)}-codex-config.toml`,
      "text/plain;charset=utf-8",
    );
    setOpen(false);
  }

  function downloadEnvironment() {
    downloadTextTemplate(
      tokenHubEnvironmentTemplate(item),
      `${templateFilename(item.name)}.env`,
      "text/plain;charset=utf-8",
    );
    setOpen(false);
  }

  return (
    <div className="api-key-download-menu" ref={menuRef}>
      <button
        aria-expanded={open}
        aria-haspopup="menu"
        className="text-button api-key-download-trigger"
        onClick={toggleMenu}
        ref={triggerRef}
        title={tx("下载 Key 配置模板")}
        type="button"
      >
        <Download size={13} />
        <span>{tx("下载")}</span>
        <ChevronDown size={12} />
      </button>
      {open ? createPortal(
        <div className="api-key-download-options" ref={optionsRef} role="menu" style={position}>
          <button onClick={downloadConfig} role="menuitem" type="button">
            <strong>{tx("Codex CLI 配置")}</strong>
            <span>config.toml · {tx("连接 TokenHub Responses")}</span>
          </button>
          <button onClick={downloadEnvironment} role="menuitem" type="button">
            <strong>{tx("环境变量模板")}</strong>
            <span>.env · {tx("替换 Key 占位符")}</span>
          </button>
        </div>,
        document.body,
      ) : null}
    </div>
  );
}

export function codexConfigTemplate(item: APIKey, data: AppData, apiBaseURL: string) {
  const model = codexTemplateModel(item, data);
  const baseURL = apiGatewayBaseURL(apiBaseURL);
  return `# TokenHub Codex CLI configuration for ${item.name}
# Set the API key before starting Codex:
# export TOKENHUB_API_KEY="REPLACE_WITH_YOUR_TOKENHUB_API_KEY"

model = "${escapeTomlString(model)}"
model_provider = "tokenhub"
model_reasoning_effort = "medium"

[model_providers.tokenhub]
name = "TokenHub - ${escapeTomlString(item.name)}"
base_url = "${escapeTomlString(baseURL)}"
wire_api = "responses"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "Set TOKENHUB_API_KEY to REPLACE_WITH_YOUR_TOKENHUB_API_KEY"
`;
}

export function tokenHubEnvironmentTemplate(item: APIKey) {
  return `# TokenHub API Key for ${item.name}
TOKENHUB_API_KEY=REPLACE_WITH_YOUR_TOKENHUB_API_KEY
`;
}

function codexTemplateModel(item: APIKey, data: AppData) {
  const activeModels = new Set(data.models.filter((model) => model.status === "active").map((model) => model.name));
  const routedModels = data.routes
    .filter((route) => route.status === "active" && activeModels.has(route.model_name))
    .map((route) => route.model_name);
  const allowedModels = (item.allowed_models ?? []).filter(Boolean);
  const candidates = allowedModels.length > 0 ? allowedModels.filter((model) => routedModels.includes(model)) : routedModels;
  return candidates[0] ?? allowedModels[0] ?? "REPLACE_WITH_MODEL_ID_FROM_V1_MODELS";
}

function escapeTomlString(value: string) {
  return value.replaceAll("\\", "\\\\").replaceAll("\"", "\\\"");
}

function templateFilename(value: string) {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
  return normalized || "tokenhub-key";
}

function downloadTextTemplate(content: string, filename: string, type: string) {
  const url = window.URL.createObjectURL(new Blob([content], { type }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  window.URL.revokeObjectURL(url);
}

export function apiKeyStatusAction(status: "active" | "disabled"): ResourceAction<APIKey> {
  return {
    label: status === "active" ? "启用" : "禁用",
    title: status === "active" ? "重新启用该 API Key" : "立即禁用该 API Key",
    run: (ctx, item) => updateAPIKeyStatus(ctx, item, status),
    doneMessage: (item) => tx(`${item.name} 已${status === "active" ? "启用" : "禁用"}`),
  };
}
