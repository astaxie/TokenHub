import { CircleAlert, FolderKanban, Gauge, KeyRound, Pencil, Plus, ShieldCheck, Trash2, UsersRound, X } from "lucide-react";
import { useEffect, useState } from "react";
import { type AdminResource, type ApiContext, type AppData, type Project, type ProjectTeam, type ResourceAction } from "../core/types";
import { costCenterLabel, costCenterSelectOptions, ownerUserLabel, projectMembersForProject, teamLabel, teamSelectOptions, userSelectOptions } from "../domain/entities";
import { formatNumber } from "../domain/formatting";
import { approvalTriggerLabel } from "../domain/labels";
import { countWithUnit, tx } from "../i18n/runtime";
import { adminFetch, pendingProjectQuotaApproval, projectQuotaIssue, projectQuotaPolicy, projectQuotaValues, readAdminError, requestProjectQuotaIncrease, saveProjectQuota } from "../resources/payloads";
import { StatusPill } from "../shared/ui";
import { projectQuotaFields, type ProjectQuotaValues } from "../domain/project-quota";
import { ProjectMemberRow } from "./crud-projects";

export type ProjectWorkspaceMode = "create" | "view" | "edit";

export type ProjectTeamDraftRow = {
  key: string;
  team_id: string;
  role: ProjectTeam["role"];
};

export type ProjectWorkspaceDraft = {
  name: string;
  status: string;
  owner_user_id: string;
  cost_center: string;
  model_access_mode: "inherit" | "restricted";
  allowed_models: string[];
  primary_team_id: string;
  primary_role: ProjectTeam["role"];
  additional_teams: ProjectTeamDraftRow[];
};

let nextTeamDraftRowID = 0;

function teamDraftRow(teamID = "", role: ProjectTeam["role"] = "viewer"): ProjectTeamDraftRow {
  nextTeamDraftRowID += 1;
  return { key: `project-team-draft-${nextTeamDraftRowID}`, team_id: teamID, role };
}

export function projectWorkspaceDraft(project?: Project): ProjectWorkspaceDraft {
  const primaryTeamID = project?.team_id || project?.teams?.find((link) => link.is_primary)?.team_id || "";
  const primaryLink = project?.teams?.find((link) => link.team_id === primaryTeamID);
  const additionalTeams = (project?.teams ?? [])
    .filter((link) => link.team_id !== primaryTeamID)
    .map((link) => teamDraftRow(link.team_id, link.role));
  return {
    name: project?.name ?? "",
    status: project?.status || "active",
    owner_user_id: project?.owner_user_id ?? "",
    cost_center: project?.cost_center ?? "",
    model_access_mode: project?.model_access_mode || ((project?.allowed_models ?? []).length > 0 ? "restricted" : "inherit"),
    allowed_models: project?.allowed_models ?? [],
    primary_team_id: primaryTeamID,
    primary_role: primaryLink?.role ?? "team_leader",
    additional_teams: project ? additionalTeams : [teamDraftRow()],
  };
}

export class ProjectWorkspaceSaveError extends Error {
  projectID?: string;

  constructor(message: string, projectID?: string) {
    super(message);
    this.name = "ProjectWorkspaceSaveError";
    this.projectID = projectID;
  }
}

export async function saveProjectWorkspaceDraft(
  api: ApiContext,
  project: Project | undefined,
  draft: ProjectWorkspaceDraft,
) {
  const payload = {
    name: draft.name.trim(),
    status: draft.status || "active",
    owner_user_id: draft.owner_user_id,
    cost_center: draft.cost_center,
    model_access_mode: draft.model_access_mode,
    allowed_models: draft.model_access_mode === "restricted" ? draft.allowed_models : [],
    team_id: draft.primary_team_id,
  };
  const saved = await projectJSON<Project>(
    api,
    project ? `/api/admin/projects/${encodeURIComponent(project.id)}` : "/api/admin/projects",
    project ? "PATCH" : "POST",
    payload,
    project ? "保存项目" : "创建项目",
  );

  try {
    await syncProjectTeams(api, saved, draft);
  } catch (error) {
    const detail = error instanceof Error ? error.message : tx("保存失败");
    throw new ProjectWorkspaceSaveError(`${tx("项目已保存，但部分团队设置失败")}：${detail}`, saved.id);
  }
  return saved;
}

async function syncProjectTeams(api: ApiContext, saved: Project, draft: ProjectWorkspaceDraft) {
  const desired = new Map<string, ProjectTeam["role"]>();
  desired.set(draft.primary_team_id, draft.primary_role);
  for (const row of draft.additional_teams) {
    if (row.team_id && row.team_id !== draft.primary_team_id) desired.set(row.team_id, row.role);
  }
  const current = new Map((saved.teams ?? []).map((link) => [link.team_id, link]));

  for (const [teamID, role] of desired) {
    const existing = current.get(teamID);
    if (!existing) {
      const created = await projectJSON<ProjectTeam>(
        api,
        `/api/admin/projects/${encodeURIComponent(saved.id)}/teams`,
        "POST",
        { team_id: teamID, role },
        "关联项目团队",
      );
      current.set(teamID, created);
      continue;
    }
    if (existing.role !== role) {
      const updated = await projectJSON<ProjectTeam>(
        api,
        `/api/admin/projects/${encodeURIComponent(saved.id)}/teams/${encodeURIComponent(teamID)}`,
        "PATCH",
        { role },
        "更新项目团队权限",
      );
      current.set(teamID, updated);
    }
  }

  for (const teamID of current.keys()) {
    if (desired.has(teamID)) continue;
    await projectDelete(
      api,
      `/api/admin/projects/${encodeURIComponent(saved.id)}/teams/${encodeURIComponent(teamID)}`,
      "移除项目团队",
    );
  }
}

async function projectJSON<T>(
  api: ApiContext,
  path: string,
  method: "POST" | "PATCH",
  payload: unknown,
  fallback: string,
) {
  const response = await adminFetch(api, path, { method, body: JSON.stringify(payload) });
  if (!response.ok) throw new Error(await readAdminError(response, tx(fallback)));
  return (await response.json()) as T;
}

async function projectDelete(api: ApiContext, path: string, fallback: string) {
  const response = await adminFetch(api, path, { method: "DELETE" });
  if (!response.ok) throw new Error(await readAdminError(response, tx(fallback)));
}

export function ProjectWorkspace({
  mode,
  data,
  project,
  loading,
  onClose,
  onEdit,
  onSave,
  onIssueKey,
  onAction,
  onCreateMember,
  onEditMember,
  onDeleteMember,
}: {
  mode: ProjectWorkspaceMode;
  data: AppData;
  project?: Project;
  loading: boolean;
  onClose: () => void;
  onEdit: () => void;
  onSave: (draft: ProjectWorkspaceDraft) => void;
  onIssueKey: () => void;
  onAction: (action: ResourceAction<Project>) => void;
  onCreateMember: () => void;
  onEditMember: (member: AdminResource) => void;
  onDeleteMember: (member: AdminResource) => void;
}) {
  const [draft, setDraft] = useState<ProjectWorkspaceDraft>(() => projectWorkspaceDraft(project));
  const editable = mode !== "view";

  useEffect(() => {
    setDraft(projectWorkspaceDraft(project));
  }, [mode, project]);

  function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave({
      ...draft,
      additional_teams: draft.additional_teams.filter((row) => row.team_id && row.team_id !== draft.primary_team_id),
    });
  }

  function changePrimaryTeam(nextTeamID: string) {
    setDraft((current) => {
      if (current.primary_team_id === nextTeamID) return current;
      const nextPrimary = current.additional_teams.find((row) => row.team_id === nextTeamID);
      const additional = current.additional_teams.filter((row) => row.team_id !== nextTeamID);
      if (current.primary_team_id && !additional.some((row) => row.team_id === current.primary_team_id)) {
        additional.unshift(teamDraftRow(current.primary_team_id, current.primary_role));
      }
      return {
        ...current,
        primary_team_id: nextTeamID,
        primary_role: nextPrimary?.role ?? "team_leader",
        additional_teams: additional,
      };
    });
  }

  const title = mode === "create" ? tx("创建项目") : project?.name || tx("项目详情");
  const teamCount = new Set([
    draft.primary_team_id,
    ...draft.additional_teams.map((row) => row.team_id),
  ].filter(Boolean)).size;

  return (
    <div className="modal-backdrop project-workspace-backdrop" role="presentation">
      <form className="project-workspace" onSubmit={submit} role="dialog" aria-modal="true" aria-labelledby="project-workspace-title">
        <header className="project-workspace-header">
          <div className="project-workspace-title">
            <span className="project-workspace-icon" aria-hidden="true"><FolderKanban size={20} /></span>
            <div>
              <p className="eyebrow">{tx(mode === "create" ? "项目空间 / 新建" : "项目空间 / 查看与配置")}</p>
              <div className="project-workspace-title-row">
                <h2 id="project-workspace-title">{title}</h2>
                {project ? <StatusPill status={project.status} /> : null}
              </div>
              <p>{tx("在同一个工作台中管理项目信息、团队访问、成员和额度。")}</p>
            </div>
          </div>
          <div className="project-workspace-header-actions">
            {mode === "view" && project ? (
              <>
                <button className="secondary-button" onClick={onIssueKey} type="button"><KeyRound size={15} />{tx("发放 Key")}</button>
                <button className="button" onClick={onEdit} type="button"><Pencil size={15} />{tx("编辑项目")}</button>
              </>
            ) : null}
            <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}><X size={18} /></button>
          </div>
        </header>

        <div className="project-workspace-summary" aria-label={tx("当前配置")}>
          <span><strong>{tx("主团队")}</strong>{draft.primary_team_id ? teamLabel(data, draft.primary_team_id) : tx("待选择")}</span>
          <span><strong>{tx("团队访问")}</strong>{countWithUnit(teamCount, "个团队", "team", "チーム")}</span>
          <span><strong>{tx("项目成员")}</strong>{project ? countWithUnit(projectMembersForProject(data, project.id).length, "人", "member", "人") : tx("创建后配置")}</span>
          <span><strong>{tx("项目额度")}</strong>{projectQuotaPolicy(data, project ?? ({ id: "" } as Project)) ? tx("已配置") : tx("未配置")}</span>
        </div>

        <div className="project-workspace-body">
          <ProjectSection
            icon={<FolderKanban size={18} />}
            index="01"
            title="基本信息"
            description="定义项目的名称、负责人、成本中心和启用状态。"
          >
            {editable ? (
              <div className="project-basics-grid">
                <label className="field">
                  <span>{tx("项目名称")}</span>
                  <input value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} required />
                </label>
                <label className="field">
                  <span>{tx("状态")}</span>
                  <select value={draft.status} onChange={(event) => setDraft((current) => ({ ...current, status: event.target.value }))}>
                    <option value="active">{tx("启用")}</option>
                    <option value="disabled">{tx("停用")}</option>
                  </select>
                </label>
                <label className="field">
                  <span>{tx("项目负责人")}</span>
                  <select value={draft.owner_user_id} onChange={(event) => setDraft((current) => ({ ...current, owner_user_id: event.target.value }))}>
                    <option value="">{tx("请选择")}</option>
                    {userSelectOptions(data).map((option) => <option key={option.value} value={option.value}>{tx(option.label)}</option>)}
                  </select>
                  <small>{tx("负责人默认拥有该项目的 Key 管理权限。")}</small>
                </label>
                <label className="field">
                  <span>{tx("成本中心")}</span>
                  <select value={draft.cost_center} onChange={(event) => setDraft((current) => ({ ...current, cost_center: event.target.value }))}>
                    <option value="">{tx("请选择")}</option>
                    {costCenterSelectOptions(data).map((option) => <option key={option.value} value={option.value}>{tx(option.label)}</option>)}
                  </select>
                </label>
              </div>
            ) : (
              <div className="project-readonly-grid">
                <ProjectSummaryField label="项目名称" value={project?.name || "-"} />
                <ProjectSummaryField label="状态" value={project?.status === "active" ? tx("启用") : tx("停用")} />
                <ProjectSummaryField label="项目负责人" value={ownerUserLabel(data, project?.owner_user_id ?? "")} />
                <ProjectSummaryField label="成本中心" value={costCenterLabel(data, project?.cost_center ?? "")} />
              </div>
            )}
          </ProjectSection>

          <ProjectSection
            icon={<ShieldCheck size={18} />}
            index="02"
            title="团队与权限"
            description="主团队承担成本、审批和责任归属；协作团队按所选角色获得项目访问权限。"
            action={mode === "view" ? <button className="text-button" onClick={onEdit} type="button">{tx("管理团队")}</button> : null}
            emphasized
          >
            {editable ? (
              <ProjectTeamsEditor draft={draft} data={data} onChange={setDraft} onPrimaryTeamChange={changePrimaryTeam} />
            ) : project ? (
              <ProjectTeamsOverview data={data} project={project} />
            ) : null}
          </ProjectSection>

          <ProjectSection
            icon={<UsersRound size={18} />}
            index="03"
            title="项目成员"
            description="直接授权给个人；个人角色与所属团队权限按最高角色合并。"
            action={project ? <button className="secondary-button compact-button" onClick={onCreateMember} type="button"><Plus size={15} />{tx("添加成员")}</button> : null}
          >
            {project ? (
              <ProjectMembersSection data={data} project={project} onEditMember={onEditMember} onDeleteMember={onDeleteMember} />
            ) : (
              <ProjectPendingSection text="创建项目后即可继续添加成员，页面会保留同样的配置结构。" />
            )}
          </ProjectSection>

          <ProjectSection
            icon={<Gauge size={18} />}
            index="04"
            title="模型访问"
            description="项目允许列表先于路由执行，项目内 API Key 只能在此范围内进一步收窄。"
          >
            {editable ? (
              <div className="project-basics-grid">
                <label className="field">
                  <span>{tx("模型访问模式")}</span>
                  <select value={draft.model_access_mode} onChange={(event) => setDraft((current) => ({ ...current, model_access_mode: event.target.value as "inherit" | "restricted" }))}>
                    <option value="inherit">{tx("继承全部已启用模型")}</option>
                    <option value="restricted">{tx("指定模型允许列表")}</option>
                  </select>
                  <small>{tx("Key 的允许列表会与项目范围取交集，不能扩大项目权限。")}</small>
                </label>
                {draft.model_access_mode === "restricted" ? (
                  <label className="field">
                    <span>{tx("项目模型允许列表")}</span>
                    <select
                      multiple
                      value={draft.allowed_models}
                      onChange={(event) => setDraft((current) => ({ ...current, allowed_models: Array.from(event.target.selectedOptions, (option) => option.value) }))}
                    >
                      {data.models.filter((model) => model.status === "active").map((model) => <option key={model.name} value={model.name}>{model.name}</option>)}
                    </select>
                    <small>{tx("不选择任何模型表示禁止项目访问全部模型。")}</small>
                  </label>
                ) : null}
              </div>
            ) : (
              <div className="project-readonly-grid">
                <ProjectSummaryField label="模型访问模式" value={draft.model_access_mode === "inherit" ? tx("继承全部已启用模型") : tx("指定模型允许列表")} />
                <ProjectSummaryField label="模型允许列表" value={draft.model_access_mode === "inherit" ? tx("继承上级范围") : draft.allowed_models.join(", ") || tx("禁止全部模型")} />
              </div>
            )}
          </ProjectSection>

          <ProjectSection
            icon={<Gauge size={18} />}
            index="05"
            title="项目额度"
            description="配置项目专属请求、Token、成本和并发额度。"
          >
            {project ? (
              <ProjectQuotaSection data={data} project={project} onAction={onAction} />
            ) : (
              <ProjectPendingSection text="创建项目后即可配置专属额度；Key 自身额度仍会叠加生效。" />
            )}
          </ProjectSection>
        </div>

        <footer className="project-workspace-actions">
          <div>
            {editable ? <span>{tx("团队设置与基本信息将一起保存。")}</span> : <span>{tx("团队配置始终在项目工作台中显性展示。")}</span>}
          </div>
          <div>
            <button className="secondary-button" onClick={onClose} type="button">{tx(editable ? "取消" : "关闭")}</button>
            {editable ? <button className="button" disabled={loading} type="submit">{tx(mode === "create" ? "创建项目" : "保存更改")}</button> : null}
          </div>
        </footer>
      </form>
    </div>
  );
}

function ProjectSection({
  icon,
  index,
  title,
  description,
  action,
  emphasized = false,
  children,
}: {
  icon: React.ReactNode;
  index: string;
  title: string;
  description: string;
  action?: React.ReactNode;
  emphasized?: boolean;
  children: React.ReactNode;
}) {
  return (
    <section className={`project-workspace-section${emphasized ? " emphasized" : ""}`}>
      <div className="project-workspace-section-head">
        <div className="project-section-heading">
          <span className="project-section-icon">{icon}</span>
          <div>
            <span>{index}</span>
            <h3>{tx(title)}</h3>
            <p>{tx(description)}</p>
          </div>
        </div>
        {action}
      </div>
      <div className="project-workspace-section-body">{children}</div>
    </section>
  );
}

function ProjectSummaryField({ label, value }: { label: string; value: string }) {
  return (
    <div className="project-summary-field">
      <span>{tx(label)}</span>
      <strong>{value || "-"}</strong>
    </div>
  );
}

function ProjectTeamsEditor({
  draft,
  data,
  onChange,
  onPrimaryTeamChange,
}: {
  draft: ProjectWorkspaceDraft;
  data: AppData;
  onChange: React.Dispatch<React.SetStateAction<ProjectWorkspaceDraft>>;
  onPrimaryTeamChange: (teamID: string) => void;
}) {
  const options = teamSelectOptions(data);
  const selectedTeamIDs = new Set([draft.primary_team_id, ...draft.additional_teams.map((row) => row.team_id)].filter(Boolean));

  function updateAdditional(key: string, patch: Partial<ProjectTeamDraftRow>) {
    onChange((current) => ({
      ...current,
      additional_teams: current.additional_teams.map((row) => row.key === key ? { ...row, ...patch } : row),
    }));
  }

  return (
    <div className="project-teams-editor">
      <div className="project-team-guidance">
        <ShieldCheck size={17} />
        <div>
          <strong>{tx("责任归属和访问权限分开配置")}</strong>
          <span>{tx("主团队决定成本归属、额度审批和默认责任；团队角色决定哪些成员可以访问项目。")}</span>
        </div>
      </div>

      <div className="project-primary-team-card">
        <div className="project-team-card-title">
          <div>
            <strong>{tx("主团队（必选）")}</strong>
            <span className="project-team-kind">{tx("责任团队")}</span>
          </div>
          <p>{tx("只能有 1 个，用于成本归属、额度审批和默认责任。")}</p>
        </div>
        <div className="project-team-editor-grid">
          <label className="field">
            <span>{tx("主团队")}</span>
            <select value={draft.primary_team_id} onChange={(event) => onPrimaryTeamChange(event.target.value)} required>
              <option value="">{tx("选择主团队")}</option>
              {options.map((option) => <option key={option.value} value={option.value}>{tx(option.label)}</option>)}
            </select>
          </label>
          <TeamRoleField
            label="团队在项目中的权限"
            role={draft.primary_role}
            includeLegacy
            onChange={(role) => onChange((current) => ({ ...current, primary_role: role }))}
          />
        </div>
      </div>

      <div className="project-additional-teams">
        <div className="project-additional-teams-head">
          <div>
            <strong>{tx("协作团队（可选）")}</strong>
            <span>{tx("添加后，该团队成员将按所选项目角色获得访问权限。")}</span>
          </div>
        </div>
        <div className="project-team-draft-list">
          {draft.additional_teams.length === 0 ? (
            <div className="project-team-empty"><UsersRound size={18} /><span>{tx("暂无协作团队。需要跨团队协作时可添加。")}</span></div>
          ) : draft.additional_teams.map((row) => (
            <div className="project-team-draft-row" key={row.key}>
              <label className="field">
                <span>{tx("协作团队")}</span>
                <select value={row.team_id} onChange={(event) => updateAdditional(row.key, { team_id: event.target.value })}>
                  <option value="">{tx("请选择")}</option>
                  {options.map((option) => (
                    <option
                      disabled={selectedTeamIDs.has(option.value) && option.value !== row.team_id}
                      key={option.value}
                      value={option.value}
                    >
                      {tx(option.label)}
                    </option>
                  ))}
                </select>
              </label>
              <TeamRoleField
                label="团队项目角色"
                role={row.role}
                includeLegacy={row.role === "team_leader"}
                onChange={(role) => updateAdditional(row.key, { role })}
              />
              <button
                className="danger-button project-team-remove"
                onClick={() => onChange((current) => ({ ...current, additional_teams: current.additional_teams.filter((item) => item.key !== row.key) }))}
                title={tx("移除团队")}
                type="button"
              >
                <Trash2 size={15} />
              </button>
            </div>
          ))}
        </div>
        <button className="project-add-team-button" onClick={() => onChange((current) => ({ ...current, additional_teams: [...current.additional_teams, teamDraftRow()] }))} type="button">
          <Plus size={16} />
          {tx(draft.additional_teams.length === 0 ? "添加协作团队" : "再添加一个团队")}
        </button>
      </div>
    </div>
  );
}

function TeamRoleField({
  label,
  role,
  includeLegacy = false,
  onChange,
}: {
  label: string;
  role: ProjectTeam["role"];
  includeLegacy?: boolean;
  onChange: (role: ProjectTeam["role"]) => void;
}) {
  return (
    <label className="field project-team-role-field">
      <span>{tx(label)}</span>
      <select value={role} onChange={(event) => onChange(event.target.value as ProjectTeam["role"])}>
        {includeLegacy ? <option value="team_leader">{tx("仅团队负责人（兼容）")}</option> : null}
        <option value="viewer">{tx("只读成员")}</option>
        <option value="developer">{tx("开发成员")}</option>
        <option value="maintainer">{tx("项目维护者")}</option>
      </select>
      <small>{tx(projectTeamRoleDescription(role))}</small>
    </label>
  );
}

function projectTeamRoleDescription(role: ProjectTeam["role"]) {
  switch (role) {
    case "maintainer":
      return "可管理项目、成员和 Key，并可申请提额。";
    case "developer":
      return "可使用项目，并可为自己发放项目 Key。";
    case "viewer":
      return "可查看项目与有权查看的用量报表。";
    default:
      return "仅原团队负责人保留访问；选择新角色后可扩展到团队成员。";
  }
}

function normalizedProjectTeams(project: Project) {
  const links = project.teams?.slice() ?? [];
  if (project.team_id && !links.some((link) => link.team_id === project.team_id)) {
    links.unshift({ project_id: project.id, team_id: project.team_id, role: "team_leader", is_primary: true });
  }
  return links.map((link) => ({ ...link, is_primary: link.team_id === project.team_id || link.is_primary }));
}

function ProjectTeamsOverview({ data, project }: { data: AppData; project: Project }) {
  const links = normalizedProjectTeams(project);
  const primary = links.find((link) => link.is_primary);
  const additional = links.filter((link) => !link.is_primary);
  return (
    <div className="project-teams-overview">
      <div className={`project-primary-team-card${primary ? "" : " missing"}`}>
        <div className="project-team-card-title">
          <div>
            <strong>{tx("主团队")}</strong>
            <span className="project-team-kind">{tx("责任团队")}</span>
          </div>
          <p>{tx("负责项目成本归属、额度审批和最终责任。")}</p>
        </div>
        {primary ? <ProjectTeamAccessRow data={data} link={primary} /> : (
          <div className="project-team-warning"><CircleAlert size={16} /><span>{tx("尚未设置主团队，请立即进入编辑模式完成配置。")}</span></div>
        )}
      </div>
      <div className="project-additional-teams">
        <div className="project-additional-teams-head">
          <div>
            <strong>{tx("协作团队")}</strong>
            <span>{tx("通过团队关系获得项目访问权限。")}</span>
          </div>
          <span>{countWithUnit(additional.length, "个", "team", "チーム")}</span>
        </div>
        {additional.length === 0 ? (
          <div className="project-team-empty"><UsersRound size={18} /><span>{tx("暂无协作团队。需要跨团队协作时可添加。")}</span></div>
        ) : (
          <div className="project-team-access-list">
            {additional.map((link) => <ProjectTeamAccessRow data={data} key={link.team_id} link={link} />)}
          </div>
        )}
      </div>
    </div>
  );
}

function ProjectTeamAccessRow({ data, link }: { data: AppData; link: ProjectTeam }) {
  return (
    <div className="project-team-access-row">
      <div>
        <strong>{teamLabel(data, link.team_id)}</strong>
        <span>{tx(projectTeamRoleDescription(link.role))}</span>
      </div>
      <span className={`project-team-role role-${link.role}`}>{tx(projectTeamRoleLabel(link.role))}</span>
    </div>
  );
}

function projectTeamRoleLabel(role: ProjectTeam["role"]) {
  if (role === "team_leader") return "仅团队负责人（兼容）";
  if (role === "maintainer") return "项目维护者";
  if (role === "developer") return "开发成员";
  return "只读成员";
}

function ProjectMembersSection({
  data,
  project,
  onEditMember,
  onDeleteMember,
}: {
  data: AppData;
  project: Project;
  onEditMember: (member: AdminResource) => void;
  onDeleteMember: (member: AdminResource) => void;
}) {
  const members = projectMembersForProject(data, project.id);
  return (
    <div>
      <div className="project-access-inheritance-note">
        <UsersRound size={16} />
        <span>{tx("同一用户从多个团队或单独成员关系获得权限时，系统采用最高角色。")}</span>
      </div>
      <div className="project-member-list">
        {members.length === 0 ? (
          <div className="empty compact-empty">{tx("暂无项目成员")}</div>
        ) : members.map((member) => (
          <ProjectMemberRow
            key={member.id}
            data={data}
            member={member}
            onEdit={() => onEditMember(member)}
            onDelete={() => onDeleteMember(member)}
          />
        ))}
      </div>
    </div>
  );
}

function ProjectPendingSection({ text }: { text: string }) {
  return (
    <div className="project-workspace-pending">
      <CircleAlert size={17} />
      <span>{tx(text)}</span>
    </div>
  );
}

function ProjectQuotaSection({
  data,
  project,
  onAction,
}: {
  data: AppData;
  project: Project;
  onAction: (action: ResourceAction<Project>) => void;
}) {
  const quota = projectQuotaPolicy(data, project);
  const [values, setValues] = useState<ProjectQuotaValues>(() => projectQuotaValues(quota));

  useEffect(() => {
    setValues(projectQuotaValues(quota));
  }, [project.id, quota]);

  const hasQuota = Boolean(quota);
  const quotaIssue = projectQuotaIssue(data, project);
  const pendingApproval = pendingProjectQuotaApproval(data, project);
  return (
    <div className="project-workspace-quota">
      <div className="quota-status-row">
        <div>
          <strong>{hasQuota ? tx("已配置项目专属额度") : tx("未配置项目专属额度")}</strong>
          <span>{tx("留空或填 0 表示该项不限额；Key 自身额度仍会叠加生效。")}</span>
        </div>
        <StatusPill status={values.status || "active"} />
      </div>

      {quotaIssue || pendingApproval ? (
        <div className="quota-request-banner">
          <div>
            <strong>{pendingApproval ? tx("已有额度提升申请待审批") : tx("最近触发了项目额度限制")}</strong>
            <span>
              {pendingApproval
                ? `${approvalTriggerLabel(pendingApproval.trigger)} ${pendingApproval.id}，${tx("可在审批记录中处理。")}`
                : `${formatNumber(quotaIssue?.count ?? 0)} ${tx("次额度不足，请填写希望提升后的目标额度再提交审批。")}`}
            </span>
          </div>
          {pendingApproval ? <StatusPill status="pending" label="待审批" /> : <StatusPill status="warning" label="需提升" />}
        </div>
      ) : null}

      <label className="field project-quota-status-field">
        <span>{tx("状态")}</span>
        <select value={values.status} onChange={(event) => setValues((current) => ({ ...current, status: event.target.value }))}>
          <option value="active">{tx("启用")}</option>
          <option value="disabled">{tx("停用")}</option>
        </select>
      </label>

      <div className="project-quota-grid">
        {projectQuotaFields.map((field) => (
          <label className="field" key={field.key}>
            <span>{tx(field.label)}</span>
            <input
              min="0"
              type="number"
              value={values[field.key]}
              onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}
            />
            {field.suffix ? <small>{field.suffix}</small> : null}
          </label>
        ))}
      </div>

      <div className="project-quota-actions">
        {quotaIssue && !pendingApproval ? (
          <button
            className="secondary-button"
            onClick={() => onAction({
              label: "提升额度申请",
              title: "提交项目额度提升审批",
              run: (ctx) => requestProjectQuotaIncrease(ctx, project, quota, values),
              doneMessage: () => `${project.name || project.id} 的额度提升申请已提交`,
            })}
            type="button"
          >
            {tx("提升额度申请")}
          </button>
        ) : null}
        <button
          className="button"
          onClick={() => onAction({
            label: "保存额度",
            title: "保存项目额度",
            run: (ctx) => saveProjectQuota(ctx, project, quota, values),
            doneMessage: () => `${project.name || project.id} 的额度已保存`,
          })}
          type="button"
        >
          {tx("保存额度")}
        </button>
      </div>
    </div>
  );
}
