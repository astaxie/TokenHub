import { appRole } from "../core/navigation";
import { type AdminResource, type AdminUser, type APIKey, type AppData, DEFAULT_PROJECT_ID, type Model, type ModelRoute, type Project, type Provider, type ProviderResource, type RequestLog, type RouteAttemptLog, type UsageBreakdownRow } from "../core/types";
import { modelCategory, modelCategoryLabel } from "./catalog";
import { formatMoney, modelCategoryRank } from "./formatting";
import { compactList, enumValueLabel, fieldKeyLabel, fieldValueLabel, providerTypeLabel, roleLabel, splitList } from "./labels";
import { tx } from "../i18n/runtime";
import { preferredModelCategories } from "./model-categories";

export const codexSubscriptionBaseURL = "https://chatgpt.com/backend-api/codex";

export function rowID(item: unknown) {
  return String(readPath(item, "id") || readPath(item, "name") || JSON.stringify(item));
}

export function rowTitle(item: unknown) {
  return String(readPath(item, "name") || readPath(item, "id") || tx("该记录"));
}

export function readPath(item: unknown, path: string): React.ReactNode {
  const value = path.split(".").reduce<unknown>((acc, key) => {
    if (acc && typeof acc === "object") return (acc as Record<string, unknown>)[key];
    return undefined;
  }, item);
  if (Array.isArray(value)) return value.join(", ");
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") return value;
  if (typeof value === "string") return value;
  if (value == null) return "-";
  return JSON.stringify(value);
}

export function stringifyForm(item: Record<string, unknown>) {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(item)) {
    result[key] = stringifyValue(value);
  }
  return result;
}

export function stringifyValue(value: unknown) {
  if (value == null) return "";
  if (Array.isArray(value)) return value.join(", ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

export function findProvider(data: AppData, providerID: string) {
  return data.providers.find((provider) => provider.id === providerID);
}

export function findProviderResource(data: AppData, resourceID: string) {
  return data.providerResources.find((resource) => resource.id === resourceID);
}

export function findProject(data: AppData, projectID: string) {
  return data.projects.find((project) => project.id === projectID);
}

export function firstActiveProject(data: AppData) {
  return data.projects.find((project) => project.id === DEFAULT_PROJECT_ID && project.status === "active")
    ?? data.projects.find((project) => project.status === "active")
    ?? data.projects[0];
}

export function firstIssueableProject(data: AppData, currentUser?: AdminUser | null) {
  return projectSelectOptions(data, currentUser)[0]?.value ?? "";
}

export function firstActiveModel(data: AppData) {
  const directoryModels = data.models.filter((model) => modelIsInDirectory(model, data));
  return directoryModels.find((model) => model.status === "active") ?? directoryModels[0];
}

export function firstActiveProvider(data: AppData) {
  return data.providers.find((provider) => provider.status === "active") ?? data.providers[0];
}

export function firstActiveUser(data: AppData) {
  return data.users.find((user) => user.status === "active") ?? data.users[0];
}

export function firstActiveTeam(data: AppData) {
  return (data.resources.teams ?? []).find((team) => team.status === "active") ?? data.resources.teams?.[0];
}

export function firstCostCenterCode(data: AppData) {
  const item = (data.resources["cost-centers"] ?? []).find((resource) => resource.status === "active")
    ?? data.resources["cost-centers"]?.[0];
  if (!item) return "";
  return stringifyValue(item.fields?.code) || item.id;
}

export function projectSelectOptions(data: AppData, currentUser?: AdminUser | null) {
  return data.projects.filter((project) => projectCanIssueKey(data, project, currentUser)).map((project) => ({
    value: project.id,
    label: projectOptionLabel(data, project),
  }));
}

export function projectMemberProjectSelectOptions(data: AppData) {
  return data.projects
    .filter((project) => project.status === "active")
    .map((project) => ({
      value: project.id,
      label: projectOptionLabel(data, project),
    }));
}

export function projectMemberRoleOptions() {
  return [
    { value: "owner", label: "负责人" },
    { value: "maintainer", label: "维护者" },
    { value: "developer", label: "开发者" },
    { value: "viewer", label: "只读" },
  ];
}

export function oauthDefaultProjectRoleOptions() {
  return [
    { value: "developer", label: "开发者" },
    { value: "viewer", label: "只读" },
    { value: "maintainer", label: "维护者" },
  ];
}

export function modelSelectOptions(data: AppData) {
  return data.models
    .filter((model) => modelIsInDirectory(model, data))
    .slice()
    .sort((left, right) => modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name))
    .map((model) => ({
      value: model.name,
      label: `${model.name} / ${modelCategoryLabel(modelCategory(model))}${model.status !== "active" ? ` / ${enumValueLabel(model.status)}` : ""}`,
    }));
}

export function providerSelectOptions(data: AppData, _currentUser?: AdminUser | null, values?: Record<string, string>) {
  const providers = values?.model_name?.trim() === codexImageModelName
    ? data.providers.filter((provider) => provider.type === "openai_codex")
    : data.providers;
  return providers
    .slice()
    .sort((left, right) => (left.priority - right.priority) || left.name.localeCompare(right.name))
    .map((provider) => ({
      value: provider.id,
      label: `${providerDisplayName(provider, data.providerResources)} / ${providerTypeLabel(providerDisplayType(provider, data.providerResources))}${provider.status !== "active" ? ` / ${enumValueLabel(provider.status)}` : ""}`,
    }));
}

export function providerModelSelectOptions(data: AppData, _currentUser?: AdminUser | null, values?: Record<string, string>) {
  const providerID = values?.provider_id?.trim();
  if (!providerID) return [];
  if (values?.model_name?.trim() === codexImageModelName) {
    return data.providers.some((provider) => provider.id === providerID && provider.type === "openai_codex")
      ? [{ value: "gpt-image-2", label: "gpt-image-2" }]
      : [];
  }
  const seen = new Set<string>();
  return data.providerModels
    .filter((model) => model.provider_id === providerID)
    .filter((model) => {
      if (seen.has(model.upstream_model)) return false;
      seen.add(model.upstream_model);
      return true;
    })
    .map((model) => ({
      value: model.upstream_model,
      label: model.display_name && model.display_name !== model.upstream_model
        ? `${model.upstream_model} / ${model.display_name}`
        : model.upstream_model,
    }))
    .sort((left, right) => left.label.localeCompare(right.label));
}

export function providerDisplayName(provider: Provider, _resources: ProviderResource[]) {
  return provider.name || provider.id;
}

export function providerDisplayType(provider: Provider, _resources: ProviderResource[]) {
  return provider.type;
}

export function providerDisplayBaseURL(provider: Provider, _resources: ProviderResource[]) {
  return provider.base_url || "local mock";
}

export function roleSelectOptions(data: AppData) {
  const configured = (data.resources["role-configs"] ?? [])
    .filter((role) => role.status === "active" && roleConfigAssignable(role))
    .map((role) => {
      const value = stringifyValue(role.fields?.role_key) || role.id;
      return {
        value,
        label: roleDisplayName(role) || roleLabel(value),
      };
    })
    .filter((option) => option.value);
  if (configured.length > 0) {
    return configured;
  }
  return ["user", "team_leader", "admin"].map((role) => ({ value: role, label: roleLabel(role) }));
}

export function roleConfigAssignable(role: AdminResource) {
  const value = stringifyValue(role.fields?.assignable).trim().toLowerCase();
  return value === "" || value === "true" || value === "1" || value === "yes";
}

export function roleDisplayName(role: AdminResource) {
  return stringifyValue(role.fields?.display_name) || role.name;
}

export function roleDisplayLabel(data: AppData, role: string) {
  const normalized = String(role || "").trim();
  const configured = (data.resources["role-configs"] ?? []).find((item) => stringifyValue(item.fields?.role_key) === normalized);
  return configured ? roleDisplayName(configured) : roleLabel(normalized);
}

export function userSelectOptions(data: AppData) {
  return data.users
    .slice()
    .sort((left, right) => (left.status === "active" ? 0 : 1) - (right.status === "active" ? 0 : 1) || (left.name || left.username).localeCompare(right.name || right.username))
    .map((user) => ({
      value: user.id,
      label: `${user.name || user.username} / ${user.email || user.username}${user.status !== "active" ? ` / ${enumValueLabel(user.status)}` : ""}`,
    }));
}

export function apiKeyOwnerSelectOptions(data: AppData, currentUser?: AdminUser | null) {
  const role = currentUser ? appRole(currentUser.role) : "admin";
  const users = data.users.slice();
  if (currentUser && !users.some((user) => user.id === currentUser.id)) {
    users.push(currentUser);
  }
  return users
    .filter((user) => user.status === "active")
    .filter((user) => {
      if (!currentUser || role === "admin") return true;
      if (role === "team_leader") return Boolean(currentUser.team_id) && user.team_id === currentUser.team_id;
      return user.id === currentUser.id;
    })
    .slice()
    .sort((left, right) => (left.name || left.username).localeCompare(right.name || right.username))
    .map((user) => ({
      value: user.id,
      label: `${user.name || user.username} / ${user.email || user.username}`,
    }));
}

export function teamSelectOptions(data: AppData) {
  return (data.resources.teams ?? [])
    .slice()
    .sort((left, right) => (left.status === "active" ? 0 : 1) - (right.status === "active" ? 0 : 1) || (left.name || left.id).localeCompare(right.name || right.id))
    .map((team) => ({
      value: team.id,
      label: `${team.name || team.id}${team.status !== "active" ? ` / ${enumValueLabel(team.status)}` : ""}`,
    }));
}

export function costCenterSelectOptions(data: AppData) {
  return (data.resources["cost-centers"] ?? [])
    .slice()
    .sort((left, right) => (left.name || left.id).localeCompare(right.name || right.id))
    .map((item) => {
      const code = stringifyValue(item.fields?.code) || item.id;
      return {
        value: code,
        label: `${code} / ${item.name || item.id}${item.status !== "active" ? ` / ${enumValueLabel(item.status)}` : ""}`,
      };
    });
}

export function projectOptionLabel(data: AppData, project: Project) {
  return [
    project.name || project.id,
    project.teams?.length ? `团队 ${projectTeamLabels(data, project)}` : project.team_id ? `团队 ${teamLabel(data, project.team_id)}` : "",
    project.owner_user_id ? `负责人 ${ownerUserLabel(data, project.owner_user_id)}` : "",
  ]
    .filter(Boolean)
    .join(" / ");
}

export function projectCanIssueKey(data: AppData, project: Project, currentUser?: AdminUser | null) {
  if (project.status !== "active") return false;
  if (!currentUser) return true;
  const role = appRole(currentUser.role);
  if (role === "admin") return true;
  if (role === "security") return false;
  if (project.owner_user_id && project.owner_user_id === currentUser.id) return true;
  if (projectTeamRoleRank(projectTeamRoleForUser(project, currentUser)) >= projectTeamRoleRank("developer")) return true;
  return projectIssueMembership(data, project.id, currentUser.id);
}

export function userTeamIDs(user?: AdminUser | null) {
  return Array.from(new Set([user?.team_id, ...(user?.team_ids ?? [])].filter((teamID): teamID is string => Boolean(teamID))));
}

export function projectTeamRoleForUser(project: Project, user: AdminUser) {
  const memberships = new Set(userTeamIDs(user));
  let result = "";
  for (const link of project.teams ?? []) {
    if (!memberships.has(link.team_id)) continue;
    let role = link.role;
    if (role === "team_leader") {
      if (appRole(user.role) !== "team_leader") continue;
      role = "maintainer";
    }
    if (projectTeamRoleRank(role) > projectTeamRoleRank(result)) result = role;
  }
  return result;
}

export function projectTeamRoleRank(role: string) {
  if (role === "owner") return 4;
  if (role === "maintainer") return 3;
  if (role === "developer") return 2;
  if (role === "viewer") return 1;
  return 0;
}

export function projectIssueMembership(data: AppData, projectID: string, userID: string) {
  return (data.resources["project-members"] ?? []).some((member) => {
    if (member.status !== "active") return false;
    if (stringifyValue(member.fields?.project_id) !== projectID || stringifyValue(member.fields?.user_id) !== userID) return false;
    const role = stringifyValue(member.fields?.role).trim().toLowerCase();
    return ["owner", "maintainer", "developer"].includes(role) || truthyValue(member.fields?.can_issue_keys);
  });
}

export function projectMembersForProject(data: AppData, projectID: string) {
  return (data.resources["project-members"] ?? [])
    .filter((member) => stringifyValue(member.fields?.project_id) === projectID)
    .slice()
    .sort((left, right) => {
      const leftUser = data.users.find((user) => user.id === stringifyValue(left.fields?.user_id));
      const rightUser = data.users.find((user) => user.id === stringifyValue(right.fields?.user_id));
      return (leftUser?.name || leftUser?.username || stringifyValue(left.fields?.user_id))
        .localeCompare(rightUser?.name || rightUser?.username || stringifyValue(right.fields?.user_id));
    });
}

export function projectMemberCanIssueLabel(item: AdminResource) {
  const role = stringifyValue(item.fields?.role).trim().toLowerCase();
  return ["owner", "maintainer", "developer"].includes(role) || truthyValue(item.fields?.can_issue_keys) ? tx("允许") : tx("不允许");
}

export function projectMemberRoleLabel(role: string) {
  switch (role.trim().toLowerCase()) {
    case "owner":
      return tx("负责人");
    case "maintainer":
      return tx("维护者");
    case "developer":
      return tx("开发者");
    case "viewer":
      return tx("只读");
    default:
      return role || "-";
  }
}

export function truthyValue(value: unknown) {
  const text = stringifyValue(value).trim().toLowerCase();
  return ["true", "1", "yes", "y", "on", "enabled"].includes(text);
}

export function overviewAnnouncements(data: AppData, user: AdminUser) {
  return (data.resources.announcements ?? [])
    .filter((item) => item.status === "active" && announcementTargetsUser(item, user))
    .slice()
    .sort((left, right) => Date.parse(right.updated_at || right.created_at || "") - Date.parse(left.updated_at || left.created_at || ""));
}

export function announcementTargetsUser(item: AdminResource, user: AdminUser) {
  const target = stringifyValue(item.fields?.target).trim();
  if (!target || ["all", "all_users", "everyone"].includes(target.toLowerCase())) return true;
  const targets = splitList(target).map((value) => value.toLowerCase());
  const role = appRole(user.role);
  const identityValues = [
    user.id,
    user.username,
    user.email,
    user.team_id,
    role,
    user.role,
  ].filter(Boolean).map((value) => String(value).toLowerCase());
  if (targets.includes("all_admins") && role !== "user") return true;
  return targets.some((value) => identityValues.includes(value));
}

export function announcementMode(item: AdminResource) {
  const mode = stringifyValue(item.fields?.notify_mode).toLowerCase();
  return mode === "popup" ? "popup" : "silent";
}

export function announcementModeLabel(item: AdminResource) {
  return announcementMode(item) === "popup" ? "弹窗" : "静默";
}

export function ownerUserLabel(data: AppData, owner: string) {
  if (!owner) return "-";
  const user = data.users.find((item) => item.id === owner || item.username === owner || item.email === owner);
  if (!user) return owner;
  return [user.name || user.username, user.email].filter(Boolean).join(" / ");
}

export function apiKeyOwnerUserID(data: AppData, key: APIKey) {
  if (key.owner_user_id) return key.owner_user_id;
  if (key.metadata?.created_by) return key.metadata.created_by;
  return findProject(data, key.project_id)?.owner_user_id || "";
}

export function usageMemberLabel(data: AppData, memberID: string) {
  if (!memberID || memberID === "unknown") return "未归属成员";
  return ownerUserLabel(data, memberID);
}

export function teamLabel(data: AppData, teamID: string) {
  if (!teamID) return "-";
  const team = (data.resources.teams ?? []).find((item) => item.id === teamID);
  return team?.name || teamID;
}

export function userTeamLabels(data: AppData, user: AdminUser) {
  return userTeamIDs(user).map((teamID) => teamLabel(data, teamID)).join(", ") || "-";
}

export function teamMemberCount(data: AppData, team: AdminResource) {
  return data.users.filter((user) => userTeamIDs(user).includes(team.id)).length;
}

export function costCenterLabel(data: AppData, costCenter: string) {
  if (!costCenter) return "-";
  const item = (data.resources["cost-centers"] ?? []).find((resource) => {
    const code = stringifyValue(resource.fields?.code);
    return resource.id === costCenter || code === costCenter;
  });
  if (!item) return costCenter;
  const code = stringifyValue(item.fields?.code) || item.id;
  return `${code} / ${item.name || item.id}`;
}

export function projectName(data: AppData, projectID: string) {
  const project = findProject(data, projectID);
  if (project) return project.name;
  return projectID === DEFAULT_PROJECT_ID ? tx("默认项目空间") : projectID || "-";
}

export function projectOwnerLabel(data: AppData, projectID: string) {
  const project = findProject(data, projectID);
  return ownerUserLabel(data, project?.owner_user_id ?? "");
}

export function projectTeamLabel(data: AppData, projectID: string) {
  const project = findProject(data, projectID);
  return project ? projectTeamLabels(data, project) : "-";
}

export function projectTeamLabels(data: AppData, project: Project) {
  const teamIDs = project.teams?.map((link) => link.team_id) ?? (project.team_id ? [project.team_id] : []);
  return teamIDs.map((teamID) => teamLabel(data, teamID)).join(", ") || "-";
}

export function modelRoutesFor(model: Model, data: AppData) {
  return data.routes
    .filter((route) => route.model_name === model.name)
    .sort((left, right) => (left.priority - right.priority) || (right.weight - left.weight));
}

export function modelIsInDirectory(model: Model, data: AppData) {
  if (model.metadata?.directory_role === "external") return true;
  if (modelRoutesFor(model, data).length > 0) return true;
  const source = model.metadata?.source ?? "";
  return source !== "tokenhub-standard-catalog" && source !== "public-provider-conf";
}

export const codexImageModelName = "codex-gpt-image-2";

export function isCodexSubscriptionImageModel(model: Model | undefined) {
  return model?.name === codexImageModelName ||
    model?.metadata?.execution_type === "codex_subscription_image_generation";
}

export function codexImageCapableResources(data: AppData) {
  const codexProviderIDs = new Set(
    data.providers
      .filter((provider) => provider.type === "openai_codex" && provider.status === "active" && provider.healthy !== false)
      .map((provider) => provider.id),
  );
  return data.providerResources.filter((resource) =>
    codexProviderIDs.has(resource.provider_id) &&
    resource.status === "active" &&
    resource.healthy !== false &&
    resource.options?.image_generation_capability === "supported",
  );
}

export function routeModelCategories(data: AppData) {
  const counts = new Map<string, number>();
  for (const model of data.models.filter((item) => modelIsInDirectory(item, data))) {
    const category = modelCategory(model);
    counts.set(category, (counts.get(category) ?? 0) + 1);
  }
  const items = Array.from(counts.entries())
    .map(([key, count]) => ({ key, label: modelCategoryLabel(key), count }))
    .sort((left, right) => preferredModelCategories.indexOf(left.key) - preferredModelCategories.indexOf(right.key) || left.label.localeCompare(right.label));
  const total = items.reduce((sum, item) => sum + item.count, 0);
  return [{ key: "all", label: modelCategoryLabel("all"), count: total }, ...items];
}

export function filterRouteModels(data: AppData, category: string, scope: "configured" | "all", query: string) {
  const normalizedQuery = query.trim().toLowerCase();
  return data.models
    .filter((model) => modelIsInDirectory(model, data))
    .filter((model) => {
      const routes = modelRoutesFor(model, data);
      if (scope === "configured" && routes.length === 0) return false;
      if (category !== "all" && modelCategory(model) !== category) return false;
      if (!normalizedQuery) return true;
      return routeModelSearchText(model, routes, data).includes(normalizedQuery);
    })
    .sort((left, right) => {
      const leftRoutes = modelRoutesFor(left, data).length;
      const rightRoutes = modelRoutesFor(right, data).length;
      if (leftRoutes !== rightRoutes) return rightRoutes - leftRoutes;
      return modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name);
    });
}

export function routeModelSearchText(model: Model, routes: ModelRoute[], data: AppData) {
  const routeText = routes.flatMap((route) => {
    const provider = findProvider(data, route.provider_id);
    return [
      route.model_name,
      route.provider_model,
      route.provider_id,
      provider?.name,
      provider?.type,
      provider?.base_url,
    ];
  });
  return [model.name, model.id, model.family, model.modality, model.category, ...routeText]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

export function reorderRoutes(routes: ModelRoute[], draggedID: string, targetID: string) {
  if (!draggedID || !targetID || draggedID === targetID) return routes;
  const current = routes.slice();
  const from = current.findIndex((route) => route.id === draggedID);
  const to = current.findIndex((route) => route.id === targetID);
  if (from < 0 || to < 0 || from === to) return routes;
  const [moved] = current.splice(from, 1);
  current.splice(to, 0, moved);
  return current;
}

export function providerRoutesFor(provider: Provider, data: AppData) {
  return data.routes.filter((route) => route.provider_id === provider.id);
}

export function providerRouteSummary(provider: Provider, data: AppData) {
  const routes = providerRoutesFor(provider, data);
  if (routes.length === 0) return "未配置";
  const active = routes.filter((route) => route.status === "active").length;
  const models = Array.from(new Set(routes.map((route) => route.model_name))).slice(0, 3);
  return `${active}/${routes.length} 启用 · ${models.join(", ")}`;
}

export function providerAccountResourceSummary(provider: Provider, data: AppData) {
  const resources = data.providerResources.filter((resource) => resource.provider_id === provider.id && resource.resource_type === "openai_subscription");
  if (resources.length === 0) return <span className="muted-inline">-</span>;
  const active = resources.filter((resource) => resource.status === "active" && resource.healthy).length;
  const first = resources[0]?.credential_summary;
  const label = first?.account_email || first?.account_id || resources[0]?.name || "";
  return (
    <div className="model-name-cell">
      <strong>{active}/{resources.length} {tx("启用")}</strong>
      <span>{label || tx("OpenAI 账号资源")}</span>
    </div>
  );
}

export function providerCostDetailRows(data: AppData) {
  if (data.breakdown.providers.length > 0) return data.breakdown.providers;
  const resourcesByID = new Map(data.providerResources.map((resource) => [resource.id, resource]));
  const providersByID = new Map(data.providers.map((provider) => [provider.id, provider]));
  const totals = new Map<string, UsageBreakdownRow>();
  for (const row of data.breakdown.provider_resources ?? []) {
    const resource = resourcesByID.get(row.id);
    const provider = resource ? providersByID.get(resource.provider_id) : undefined;
    const id = provider?.name || resource?.provider_id || row.id;
    const current = totals.get(id) ?? {
      id,
      request_count: 0,
      input_tokens: 0,
      cached_input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      estimated_cost_usd: 0,
    };
    current.request_count += row.request_count;
    current.input_tokens += row.input_tokens;
    current.cached_input_tokens = (current.cached_input_tokens ?? 0) + (row.cached_input_tokens ?? 0);
    current.output_tokens += row.output_tokens;
    current.total_tokens += row.total_tokens;
    current.estimated_cost_usd += row.estimated_cost_usd;
    totals.set(id, current);
  }
  return Array.from(totals.values());
}

export function providerAuditLabel(data: AppData, log: RequestLog) {
  const provider = log.provider_id ? findProvider(data, log.provider_id) : undefined;
  if (provider) return provider.name;
  if (log.provider_id) return log.provider_id;
  const resource = log.provider_resource_id
    ? data.providerResources.find((item) => item.id === log.provider_resource_id)
    : undefined;
  if (!resource) return "-";
  return findProvider(data, resource.provider_id)?.name || resource.provider_id || "-";
}

export function providerAttemptLabel(data: AppData, attempt: RouteAttemptLog) {
  const provider = attempt.provider_id ? findProvider(data, attempt.provider_id) : undefined;
  if (provider) return provider.name;
  if (attempt.provider_id) return attempt.provider_id;
  const resource = attempt.provider_resource_id
    ? data.providerResources.find((item) => item.id === attempt.provider_resource_id)
    : undefined;
  if (!resource) return "-";
  return findProvider(data, resource.provider_id)?.name || resource.provider_id || "-";
}

export function providerResourceAuditLabel(data: AppData, resourceID?: string) {
  if (!resourceID) return "-";
  const resource = data.providerResources.find((item) => item.id === resourceID);
  if (!resource) return resourceID;
  const provider = findProvider(data, resource.provider_id);
  return [resource.name || resource.id, provider?.name || resource.provider_id].filter(Boolean).join(" / ");
}

export function apiKeyAuditLabel(data: AppData, apiKeyID?: string) {
  if (!apiKeyID) return "-";
  const key = data.keys.find((item) => item.id === apiKeyID);
  if (!key) return apiKeyID;
  return `${key.name || key.id} (${key.key_prefix}...${key.key_suffix})`;
}

export function providerRouteDefaults(provider: Provider, data: AppData) {
  const firstModel = firstActiveModel(data);
  const matchingProviderModel = data.providerModels.find((providerModel) =>
    providerModel.provider_id === provider.id
      && (providerModel.upstream_model === firstModel?.name || providerModel.canonical_name === firstModel?.name),
  );
  return {
    model_name: firstModel?.name ?? "",
    provider_id: provider.id,
    provider_model: matchingProviderModel?.upstream_model ?? "",
    priority: "1",
    weight: "100",
    quality_score: "50",
    cost_score: "50",
    strategy: "priority_weighted",
    project_scope: "all",
    project_ids: "",
    sticky_session: "false",
    status: "active",
  };
}

export function modelRouteDefaults(model: Model, data: AppData) {
  const firstProvider = isCodexSubscriptionImageModel(model)
    ? data.providers.find((provider) => provider.type === "openai_codex" && provider.status === "active")
      ?? data.providers.find((provider) => provider.type === "openai_codex")
    : firstActiveProvider(data);
  const matchingProviderModel = data.providerModels.find((providerModel) =>
    providerModel.provider_id === firstProvider?.id
      && (providerModel.upstream_model === model.name || providerModel.canonical_name === model.name),
  );
  return {
    model_name: model.name,
    provider_id: firstProvider?.id ?? "",
    provider_model: isCodexSubscriptionImageModel(model) ? "gpt-image-2" : matchingProviderModel?.upstream_model ?? "",
    priority: "1",
    weight: "100",
    quality_score: "50",
    cost_score: "50",
    strategy: "priority_weighted",
    project_scope: "all",
    project_ids: "",
    sticky_session: "false",
    status: "active",
  };
}

export function routeScoreSummary(route: ModelRoute) {
  return `质量 ${route.quality_score ?? 50} / 成本 ${route.cost_score ?? 50}`;
}

export function routeProjectScopeSummary(route: ModelRoute, data: AppData) {
  const scope = route.project_scope || "all";
  if (scope === "all") return tx("所有项目");
  const names = (route.project_ids ?? []).map((projectID) => findProject(data, projectID)?.name || projectID);
  const prefix = scope === "include" ? tx("仅指定项目") : tx("排除指定项目");
  return `${prefix}: ${compactList(names) || "-"}`;
}

export function modelCapabilitySummary(model: Model) {
  const capabilities = [
    model.modality,
    ...(model.capabilities ?? []),
    ...(model.supported_parameters ?? []).map((item) => `param:${item}`),
  ].filter(Boolean);
  return compactList(capabilities);
}

export function modelPriceSummary(model: Model) {
  const embedding = model.embedding_price_usd_per_1m || 0;
  if (model.modality === "embedding" || embedding > 0) {
    return `$${formatMoney(embedding)}/1M`;
  }
  const input = model.input_price_usd_per_1m || 0;
  const output = model.output_price_usd_per_1m || 0;
  if (!input && !output) return "$-";
  return `$${formatMoney(input)} / $${formatMoney(output)}`;
}

export function fieldSummary(fields?: Record<string, unknown>) {
  if (!fields || Object.keys(fields).length === 0) return "-";
  return Object.entries(fields)
    .slice(0, 3)
    .map(([key, value]) => `${fieldKeyLabel(key)}: ${fieldValueLabel(key, value)}`)
    .join(" / ");
}
