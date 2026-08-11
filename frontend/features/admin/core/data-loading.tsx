import { appRole, canAccessView, canViewAdminAudit } from "./navigation";
import { type AdminResource, type AdminUser, type AppData, type ViewKey } from "./types";

export type LoadPlan = {
  overview: boolean;
  providers: boolean;
  providerResources: boolean;
  providerModels: boolean;
  keys: boolean;
  routes: boolean;
  logs: boolean;
  auditEvents: boolean;
  alerts: boolean;
  alertDeliveries: boolean;
  approvals: boolean;
  sqliteBackups: boolean;
  breakdown: boolean;
  timeseries: boolean;
  users: boolean;
  providerCatalog: boolean;
  providerMonitoring: boolean;
	billingConnectors: boolean;
	billingRecords: boolean;
	billingSyncRuns: boolean;
	reconciliationRules: boolean;
	reconciliationRuns: boolean;
  resources: string[];
};

export type LoadedData = Partial<Omit<AppData, "resources">> & {
  resources?: Record<string, AdminResource[]>;
};

export function emptyLoadPlan(): LoadPlan {
  return {
    overview: false,
    providers: false,
    providerResources: false,
    providerModels: false,
    keys: false,
    routes: false,
    logs: false,
    auditEvents: false,
    alerts: false,
    alertDeliveries: false,
    approvals: false,
    sqliteBackups: false,
    breakdown: false,
    timeseries: false,
    users: false,
    providerCatalog: false,
    providerMonitoring: false,
		billingConnectors: false,
		billingRecords: false,
		billingSyncRuns: false,
		reconciliationRules: false,
		reconciliationRuns: false,
    resources: [],
  };
}

export function addResourceDependency(plan: LoadPlan, kind: string) {
  if (!plan.resources.includes(kind)) {
    plan.resources.push(kind);
  }
}

export function loadPlanForView(user: AdminUser, view: ViewKey): LoadPlan {
  const plan = emptyLoadPlan();
  const can = (target: ViewKey) => canAccessView(user, target);

  switch (view) {
    case "overview":
      plan.overview = true;
      plan.breakdown = true;
      plan.timeseries = true;
      plan.logs = can("audit");
      plan.users = appRole(user.role) === "team_leader";
      if (appRole(user.role) === "team_leader") {
        addResourceDependency(plan, "teams");
      }
      addResourceDependency(plan, "announcements");
      break;
    case "playground":
      plan.overview = true;
      plan.routes = can("routes");
      break;
    case "gateway":
      plan.overview = true;
      plan.keys = can("api-keys");
      plan.routes = can("routes");
      plan.logs = can("audit");
      if (appRole(user.role) === "user" || appRole(user.role) === "team_leader") {
        addResourceDependency(plan, "project-members");
      }
      break;
    case "usage":
      plan.overview = true;
      plan.breakdown = true;
      plan.timeseries = true;
      plan.users = can("users") || appRole(user.role) === "team_leader";
      if (appRole(user.role) !== "user") {
        addResourceDependency(plan, "teams");
        addResourceDependency(plan, "cost-centers");
      }
      break;
    case "billing":
      plan.breakdown = true;
      plan.users = appRole(user.role) === "team_leader";
		if (appRole(user.role) === "admin") {
			plan.billingConnectors = true;
			plan.billingRecords = true;
			plan.billingSyncRuns = true;
			plan.reconciliationRules = true;
			plan.reconciliationRuns = true;
		}
      break;
    case "audit":
      plan.overview = true;
      plan.keys = can("api-keys");
      plan.auditEvents = canViewAdminAudit(user);
      break;
    case "providers":
      plan.providers = true;
      plan.providerResources = true;
      plan.overview = true;
      plan.routes = true;
      plan.logs = can("audit");
      plan.auditEvents = canViewAdminAudit(user);
      plan.breakdown = can("usage") || can("billing");
      plan.providerCatalog = true;
      plan.providerModels = true;
      plan.providerMonitoring = true;
      break;
    case "models":
      plan.overview = true;
      plan.routes = can("routes");
      plan.providerModels = can("routes");
      plan.providerCatalog = can("routes");
      break;
    case "routes":
      plan.overview = true;
      plan.routes = true;
      plan.providerModels = true;
      break;
    case "routing-policies":
      plan.overview = true;
      plan.keys = true;
      plan.routes = true;
      plan.providerResources = true;
      addResourceDependency(plan, "routing-policies");
      break;
    case "projects":
      plan.overview = true;
      plan.logs = true;
      plan.users = can("users") || appRole(user.role) === "team_leader";
      plan.approvals = can("approvals");
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "cost-centers");
      addResourceDependency(plan, "quota-policies");
      addResourceDependency(plan, "project-members");
      break;
    case "project-members":
      plan.overview = true;
      plan.users = true;
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "project-members");
      break;
    case "api-keys":
      plan.overview = true;
      plan.keys = true;
      plan.users = can("users") || appRole(user.role) === "team_leader";
      if (appRole(user.role) !== "user") {
        addResourceDependency(plan, "teams");
      }
      addResourceDependency(plan, "project-members");
      break;
    case "teams":
      plan.users = true;
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "cost-centers");
      break;
    case "users":
      plan.users = true;
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "role-configs");
      break;
    case "settings":
      addResourceDependency(plan, "settings");
      addResourceDependency(plan, "role-configs");
      addResourceDependency(plan, "identity-providers");
      break;
    case "security-policies":
      plan.overview = true;
      addResourceDependency(plan, "security-policies");
      break;
    case "quota-policies":
    case "cost-centers":
    case "approval-flows":
    case "reports":
    case "notification-channels":
    case "monitors":
    case "proxies":
    case "announcements":
    case "identity-providers":
      addResourceDependency(plan, view);
      break;
    case "alerts":
      addResourceDependency(plan, "alert-rules");
      break;
    case "alert-events":
      plan.alerts = true;
      break;
    case "alert-deliveries":
      plan.alertDeliveries = true;
      break;
    case "approvals":
      plan.approvals = true;
      break;
    case "sqlite-backups":
      plan.sqliteBackups = true;
      break;
  }

  return plan;
}

export function mergeLoadedData(current: AppData, loaded: LoadedData): AppData {
  return {
    ...current,
    summary: loaded.summary ?? current.summary,
    projects: loaded.projects ?? current.projects,
    providers: loaded.providers ?? current.providers,
    providerResources: loaded.providerResources ?? current.providerResources,
    providerModels: loaded.providerModels ?? current.providerModels,
    models: loaded.models ?? current.models,
    routes: loaded.routes ?? current.routes,
    logs: loaded.logs ?? current.logs,
    auditEvents: loaded.auditEvents ?? current.auditEvents,
    alerts: loaded.alerts ?? current.alerts,
    alertDeliveries: loaded.alertDeliveries ?? current.alertDeliveries,
    approvals: loaded.approvals ?? current.approvals,
    sqliteBackups: loaded.sqliteBackups ?? current.sqliteBackups,
    users: loaded.users ?? current.users,
    breakdown: loaded.breakdown ?? current.breakdown,
    timeseries: loaded.timeseries ?? current.timeseries,
    keys: loaded.keys ?? current.keys,
    providerCatalog: loaded.providerCatalog ?? current.providerCatalog,
    providerMonitoring: loaded.providerMonitoring ?? current.providerMonitoring,
		billingConnectors: loaded.billingConnectors ?? current.billingConnectors,
		billingRecords: loaded.billingRecords ?? current.billingRecords,
		billingSyncRuns: loaded.billingSyncRuns ?? current.billingSyncRuns,
		reconciliationRules: loaded.reconciliationRules ?? current.reconciliationRules,
		reconciliationRuns: loaded.reconciliationRuns ?? current.reconciliationRuns,
    resources: loaded.resources ? { ...current.resources, ...loaded.resources } : current.resources,
  };
}
