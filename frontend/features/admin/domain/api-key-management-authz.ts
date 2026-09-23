import { type AdminUser, type APIKey, type AppData, type Project } from "../core/types";

export function apiKeyCanManage(data: AppData, key: APIKey, currentUser?: AdminUser | null) {
  if (!currentUser) return false;
  const role = adminAppRole(currentUser.role);
  if (role === "admin") return true;
  if (key.metadata?.created_by?.trim() === currentUser.id) return true;
  const project = data.projects.find((item) => item.id === key.project_id);
  if (!project) return false;
  if (project.owner_user_id && project.owner_user_id === currentUser.id) return true;
  if (projectRoleRank(projectTeamRoleForUser(project, currentUser, activeTeamIDs(data))) >= projectRoleRank("maintainer")) return true;
  return projectManageMembership(data, project.id, currentUser.id);
}

function adminAppRole(role: string) {
  const normalized = String(role || "").trim().toLowerCase();
  if (normalized === "admin" || normalized === "system_admin") return "admin";
  if (normalized === "team_leader" || normalized === "teamlead" || normalized === "project_admin") return "team_leader";
  return "user";
}

function userTeamIDs(user: AdminUser) {
  return Array.from(new Set([user.team_id, ...(user.team_ids ?? [])].filter((teamID): teamID is string => Boolean(teamID))));
}

function projectTeamRoleForUser(project: Project, user: AdminUser, activeTeams: Set<string>) {
  const memberships = new Set(userTeamIDs(user));
  let result = "";
  for (const link of project.teams ?? []) {
    if (!activeTeams.has(link.team_id)) continue;
    if (!memberships.has(link.team_id)) continue;
    let role = link.role;
    if (role === "team_leader") {
      if (adminAppRole(user.role) !== "team_leader") continue;
      role = "maintainer";
    }
    if (projectRoleRank(role) > projectRoleRank(result)) result = role;
  }
  return result;
}

function projectRoleRank(role: string) {
  if (role === "owner") return 4;
  if (role === "maintainer") return 3;
  if (role === "developer") return 2;
  if (role === "viewer") return 1;
  return 0;
}

function projectManageMembership(data: AppData, projectID: string, userID: string) {
  return (data.resources["project-members"] ?? []).some((member) => {
    if (member.status !== "active") return false;
    if (stringifyValue(member.fields?.project_id) !== projectID || stringifyValue(member.fields?.user_id) !== userID) return false;
    const role = stringifyValue(member.fields?.role).trim().toLowerCase();
    return role === "owner" || role === "maintainer";
  });
}

function activeTeamIDs(data: AppData) {
  return new Set(
    (data.resources.teams ?? [])
      .filter((team) => team.status === "" || team.status === "active")
      .map((team) => team.id),
  );
}

function stringifyValue(value: unknown) {
  if (value == null) return "";
  if (Array.isArray(value)) return value.join(", ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
