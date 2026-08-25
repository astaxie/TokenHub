"use client";

import { usePathname, useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type LoadedData, loadPlanForView, mergeLoadedData } from "../core/data-loading";
import { allNavGroupTitles, canAccessView, defaultViewForRole, rememberRecentView, standaloneViewMeta } from "../core/navigation";
import { clearOAuthAuthorizationResponse, clearOAuthLoginResult, clearPendingOAuthLogin, clearProviderAccountOAuthResultFromLocation, clearSavedSession, consumePasswordResetToken, forwardOAuthAuthorizationResponse, hasPendingProviderAccountOAuthResult, isOAuthAuthorizationResponse, isProviderAccountOAuthAuthorizationResponse, readOAuthLoginResult, readPendingOAuthLogin, readProviderAccountOAuthResultFromLocation, readSavedSession, savePendingProviderAccountOAuthResult, saveSession } from "../core/session";
import { type AdapterDescriptor, type AdminResource, type AdminUIContribution, type AdminUser, type AlertDelivery, type AlertEvent, type APIKey, type AppData, type ApprovalRequest, type AuditEvent, authExpiredEventName, type BillingConnector, type BillingRecord, type BillingSyncRun, type ConfirmState, type GatewayChainPlan, languageStorageKey, type LoginIdentityProvider, type ModalState, type Model, type ModelRoute, type ModelRoutePolicy, notificationChannelTypes, type PluginActionDescriptor, type PluginBackgroundJobDescriptor, type PluginBackgroundJobRunRecord, type PluginDescriptor, type Project, type Provider, type ProviderCatalogEntry, type ProviderModel, type ProviderMonitoringSnapshot, type ProviderResource, type ReconciliationRule, type ReconciliationRun, type ReportExportHistoryItem, type RequestLog, type ResourceAction, type ResourceConfig, type SettingsTabKey, type SQLiteBackup, type ToolbarAction, type UsageBreakdown, type UsageDaily, type UsagePoint, type ViewKey, viewRoutes } from "../core/types";
import { emptyData, emptySummary, filterByModelCategory, filterRows } from "../domain/catalog";
import { filterAPIKeys } from "../domain/api-key-filter";
import { auditRequestPagePath } from "../domain/audit-request-page";
import { modelRouteDefaults, rowTitle } from "../domain/entities";
import { apiKeyUsageIDFromPath, uniqueUIID, viewFromPath } from "../domain/formatting";
import { reportDatasetLabel } from "../domain/labels";
import { exchangeOAuthLoginCode, resolvePendingOAuthLoginResult } from "../domain/oauth-login";
import { pluginShellPresentation } from "../domain/plugin-theme";
import { resourceCreateTarget } from "../domain/resource-create-target";
import { type AppLanguage, bulkDeleteConfirmMessage, deleteConfirmMessage, importUsersDoneMessage, importUsersSkippedMessage, isIssuedAPIKey, readSavedLanguage, setActiveLanguage, tx } from "../i18n/runtime";
import { createKeyWithCapture } from "../resources/generic-config";
import { downloadReport } from "../resources/governance-config";
import { adminFetch, adminMutate, importUsersFromCSVContent, isAuthExpiredError, loadRequestLabel, notificationChannelDefaults, permissionPartialLoadMessage, readAdminError, readLoadError, restoreDefaultModelCatalog } from "../resources/payloads";
import { projectMemberConfig, projectMemberInitialValues } from "../resources/project-key-config";
import { resourceConfigFor } from "../resources/settings-config";
import { usePagination } from "../shared/pagination";
import { currentOAuthReturnURL, LoginView, ResetPasswordView } from "../shared/auth";
import { ConfirmDialog, IssuedKeyModal, providerTypeOptionsFromData } from "../shared/ui";
import { PageHeader, Sidebar, StatusStack, TopNav } from "./navigation-ui";
import { ResponsiveVersionStatus } from "./version-status";
import { AuditView } from "../views/audit";
import { CrudView, ReportsView } from "../views/crud-projects";
import { DatabaseStatusView } from "../views/database-model-pricing";
import { GatewayView } from "../views/gateway-view";
import { ModelDirectoryView } from "../views/model-directory";
import { RouteStrategyView } from "../views/model-catalog";
import { modelRoutePolicyPayload } from "../views/model-routing-policy";
import { OverviewView } from "../views/overview";
import { PlaygroundPage } from "../views/playground";
import { PluginPageView } from "../views/admin-ui-plugin-pages";
import { PluginsView } from "../views/plugins";
import { ProviderUpsertModal } from "../views/provider-editor";
import { ProjectWorkspace, type ProjectWorkspaceDraft, type ProjectWorkspaceMode, ProjectWorkspaceSaveError, saveProjectWorkspaceDraft } from "../views/project-workspace";
import { RoutingPolicySimulator } from "../views/routing-policy-simulator";
import { ContentSecurityPolicies, SecurityPolicyTabs } from "../views/security-policies";
import { APIKeyWizardModal, UserImportModal } from "../views/modals";
import { EditModal, SettingsView } from "../views/settings-table";
import { BillingView, UsageView } from "../views/usage-billing";
import { APIKeyUsageView } from "../views/api-key-usage";

export function AdminConsole({ defaultBaseURL }: { defaultBaseURL: string }) {
  const pathname = usePathname();
  const router = useRouter();
  const routeView = viewFromPath(pathname);
  const apiKeyUsageID = apiKeyUsageIDFromPath(pathname);
  const [language, setLanguage] = useState<AppLanguage>(() => readSavedLanguage());
  const [theme, setTheme] = useState<"light" | "dark">("light");
  const [baseURL, setBaseURL] = useState(defaultBaseURL);
  const [adminToken, setAdminToken] = useState("");
  const [currentUser, setCurrentUser] = useState<AdminUser | null>(null);
  const [loginIdentityProviders, setLoginIdentityProviders] = useState<LoginIdentityProvider[]>([]);
  const [oauthReturnURL, setOAuthReturnURL] = useState(() => currentOAuthReturnURL());
  const [bootstrapped, setBootstrapped] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [openNavGroups, setOpenNavGroups] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(allNavGroupTitles.map((title) => [title, true])),
  );
  const [activeView, setActiveView] = useState<ViewKey>(routeView);
  const [activePluginPageKey, setActivePluginPageKey] = useState(() => pluginPageKeyFromLocation());
  const [data, setData] = useState<AppData>(emptyData());
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [modelCategoryFilter, setModelCategoryFilter] = useState("all");
  const [routeModelQuery, setRouteModelQuery] = useState("");
  const [settingsTab, setSettingsTab] = useState<SettingsTabKey>("settings");
  const [securityPolicyTab, setSecurityPolicyTab] = useState<"access" | "content">("access");
  const [modal, setModal] = useState<ModalState<any> | null>(null);
  const [projectWorkspace, setProjectWorkspace] = useState<{ mode: ProjectWorkspaceMode; projectID?: string } | null>(null);
  const [providerCreateOpen, setProviderCreateOpen] = useState(false);
  const [providerEditItem, setProviderEditItem] = useState<Provider | null>(null);
  const [apiKeyWizardOpen, setApiKeyWizardOpen] = useState(false);
  const [apiKeyWizardInitialValues, setApiKeyWizardInitialValues] = useState<Record<string, string>>({});
  const [userImportOpen, setUserImportOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<ConfirmState<any> | null>(null);
  const [confirmRestoreModels, setConfirmRestoreModels] = useState(false);
  const [issuedKey, setIssuedKey] = useState("");
  const loadRef = useRef<(view?: ViewKey) => Promise<void>>(async () => undefined);
  const [reportHistory, setReportHistory] = useState<ReportExportHistoryItem[]>([]);
  const [resetToken, setResetToken] = useState("");

  const api = useMemo(() => ({ baseURL, adminToken }), [baseURL, adminToken]);
  const providerTypeOptions = useMemo(() => providerTypeOptionsFromData(data), [data]);
  const activeConfig = resourceConfigFor(activeView);
  const activeMeta = activeConfig ?? standaloneViewMeta[activeView] ?? standaloneViewMeta.overview!;
  const shellPresentation = useMemo(() => pluginShellPresentation(data.pluginUI, theme), [data.pluginUI, theme]);
  setActiveLanguage(language);

  function changeLanguage(nextLanguage: AppLanguage) {
    setLanguage(nextLanguage);
  }

  function toggleTheme() {
    setTheme((value) => (value === "light" ? "dark" : "light"));
  }

  const selectView = useCallback((view: ViewKey, options: { replace?: boolean; routeModelQuery?: string; pluginPageKey?: string } = {}) => {
    if (view !== activeView) {
      setNotice("");
      setError("");
      setIssuedKey("");
      setModelCategoryFilter(view === "notification-channels" ? "webhook" : "all");
    }
    if (view === "routes") setRouteModelQuery(options.routeModelQuery ?? "");
    setActivePluginPageKey(view === "plugin-pages" ? options.pluginPageKey ?? activePluginPageKey : "");
    if (view !== "projects") setProjectWorkspace(null);
    setActiveView(view);
    const nextPath = viewRoutes[view];
    const suffix = pluginPageURLSuffix(view, options.pluginPageKey);
    if (pathname === nextPath && suffix === currentLocationSuffix()) return;
    const nextURL = `${nextPath}${suffix}`;
    if (options.replace) {
      router.replace(nextURL);
    } else {
      router.push(nextURL);
    }
  }, [activePluginPageKey, activeView, pathname, router]);

  const selectPluginPage = useCallback((key: string) => {
    selectView("plugin-pages", { pluginPageKey: key });
  }, [selectView]);

  function openRoutes(model?: Model) {
    selectView("routes", { routeModelQuery: model?.name ?? "" });
  }

  useEffect(() => {
    let cancelled = false;
    async function bootstrapSession() {
      const passwordResetToken = consumePasswordResetToken();
      if (passwordResetToken) {
        clearSavedSession();
        clearOAuthLoginResult();
        clearOAuthAuthorizationResponse();
        clearPendingOAuthLogin();
        setResetToken(passwordResetToken);
        setBootstrapped(true);
        return;
      }
      const saved = readSavedSession();
      const pendingLogin = readPendingOAuthLogin();
      const oauthCallback = resolvePendingOAuthLoginResult(window.location, pendingLogin);
      if (oauthCallback.status === "none" && pendingLogin && forwardOAuthAuthorizationResponse(pendingLogin.baseURL)) return;
      if (
        oauthCallback.status === "none" &&
        !isProviderAccountOAuthAuthorizationResponse() &&
        isOAuthAuthorizationResponse()
      ) {
        clearOAuthAuthorizationResponse();
        clearPendingOAuthLogin();
        setError(tx("OAuth 登录失败"));
        setBootstrapped(true);
        return;
      }
      if (oauthCallback.status === "unexpected") {
        clearOAuthLoginResult();
        clearPendingOAuthLogin();
        setError(tx("OAuth 登录失败"));
        setBootstrapped(true);
        return;
      }
      if (oauthCallback.status === "ready" && oauthCallback.result.error) {
        clearOAuthLoginResult();
        clearPendingOAuthLogin();
        setError(tx("OAuth 登录失败"));
        setBootstrapped(true);
        return;
      }
      if (oauthCallback.status === "ready" && oauthCallback.result.code) {
        const sessionBaseURL = oauthCallback.baseURL;
        setBaseURL(sessionBaseURL);
        setLoading(true);
        try {
          const exchange = await exchangeOAuthLoginCode(sessionBaseURL, oauthCallback.result.code, oauthCallback.codeVerifier);
          const resp = new Response(exchange.body, { status: exchange.status });
          if (!exchange.ok) {
            throw new Error(await readAdminError(resp, tx("登录失败")));
          }
          const payload = JSON.parse(exchange.body) as { token?: string; user?: AdminUser; expires_at?: string };
          if (!payload.token || !payload.user || !payload.expires_at) {
            throw new Error(tx("登录失败"));
          }
          if (cancelled) return;
          setData(emptyData());
          setAdminToken(payload.token);
          setCurrentUser(payload.user);
          saveSession({ baseURL: sessionBaseURL, token: payload.token, user: payload.user, expiresAt: payload.expires_at });
          setError("");
        } catch (err) {
          if (!cancelled) setError(err instanceof Error ? err.message : tx("OAuth 登录失败"));
        } finally {
          clearOAuthLoginResult();
          clearPendingOAuthLogin();
          if (!cancelled) {
            setLoading(false);
            setBootstrapped(true);
          }
        }
        return;
      }
      clearOAuthLoginResult();
      if (saved) {
        setBaseURL(saved.baseURL);
        setAdminToken(saved.token);
        setCurrentUser(saved.user);
      }
      setBootstrapped(true);
    }
    void bootstrapSession();
    return () => {
      cancelled = true;
    };
  }, [defaultBaseURL]);

  useEffect(() => {
    if (typeof window !== "undefined") {
      setOAuthReturnURL(currentOAuthReturnURL());
    }
  }, []);

  useEffect(() => {
    const result = readProviderAccountOAuthResultFromLocation();
    if (!result) return;
    savePendingProviderAccountOAuthResult(result);
    clearProviderAccountOAuthResultFromLocation();
  }, []);

  useEffect(() => {
    if (!currentUser || !hasPendingProviderAccountOAuthResult()) return;
    selectView("providers", { replace: true });
    setProviderCreateOpen(true);
    setNotice(tx("收到账号授权回调，已打开账号池创建向导。"));
  }, [currentUser, selectView]);

  useEffect(() => {
    if (!bootstrapped || currentUser) return;
    let cancelled = false;
    async function loadLoginIdentityProviders() {
      try {
        const resp = await fetch(`${baseURL.replace(/\/$/, "")}/api/admin/auth/identity-providers`);
        if (!resp.ok) {
          if (!cancelled) setLoginIdentityProviders([]);
          return;
        }
        const payload = (await resp.json()) as { data?: LoginIdentityProvider[] };
        if (!cancelled) setLoginIdentityProviders(payload.data ?? []);
      } catch {
        if (!cancelled) setLoginIdentityProviders([]);
      }
    }
    void loadLoginIdentityProviders();
    return () => {
      cancelled = true;
    };
  }, [baseURL, bootstrapped, currentUser]);

  useEffect(() => {
    setActiveLanguage(language);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(languageStorageKey, language);
      document.documentElement.lang = language === "zh-CN" ? "zh-CN" : language;
    }
  }, [language]);

  useEffect(() => {
    if (!bootstrapped || !adminToken || !currentUser) return;
    if (!canAccessView(currentUser, activeView)) return;
    void load(activeView);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- load is an orchestration command; the explicit state keys define when it runs.
  }, [bootstrapped, adminToken, currentUser, activeView]);

  useEffect(() => {
    if (!bootstrapped || !adminToken || !currentUser || activeView !== "usage") return;
    const timer = window.setInterval(() => {
      void loadRef.current("usage");
    }, 30_000);
    return () => window.clearInterval(timer);
  }, [activeView, adminToken, bootstrapped, currentUser]);

  useEffect(() => {
    if (activeView === "notification-channels" && !notificationChannelTypes.includes(modelCategoryFilter)) {
      setModelCategoryFilter("webhook");
    }
  }, [activeView, modelCategoryFilter]);

  useEffect(() => {
    if (!currentUser) return;
    if (!canAccessView(currentUser, activeView)) {
      selectView(defaultViewForRole(currentUser), { replace: true });
    }
  }, [currentUser, activeView, selectView]);

  useEffect(() => {
    if (!currentUser || !canAccessView(currentUser, activeView)) return;
    rememberRecentView(activeView);
  }, [activeView, currentUser]);

  useEffect(() => {
    if (isOAuthAuthorizationResponse()) return;
    if (readOAuthLoginResult()) return;
    setNotice("");
    setError("");
    setModelCategoryFilter(routeView === "notification-channels" ? "webhook" : "all");
    setActiveView(routeView);
    setActivePluginPageKey(routeView === "plugin-pages" ? pluginPageKeyFromLocation() : "");
  }, [routeView]);

  useEffect(() => {
    function onAuthExpired() {
      clearSavedSession();
      setAdminToken("");
      setCurrentUser(null);
      setData(emptyData());
      setModal(null);
      setProjectWorkspace(null);
      setProviderCreateOpen(false);
      setProviderEditItem(null);
      setApiKeyWizardOpen(false);
      setApiKeyWizardInitialValues({});
      setUserImportOpen(false);
      setConfirmDelete(null);
      setConfirmRestoreModels(false);
      setIssuedKey("");
      setNotice("");
      setError("");
      selectView("overview", { replace: true });
      setLoading(false);
    }
    window.addEventListener(authExpiredEventName, onAuthExpired);
    return () => window.removeEventListener(authExpiredEventName, onAuthExpired);
  }, [selectView]);

  useEffect(() => {
    function onIssuedKey(event: Event) {
      const detail = (event as CustomEvent<string>).detail;
      if (!detail) return;
      if (isIssuedAPIKey(detail)) {
        setNotice("");
        setIssuedKey(detail);
      } else {
        setIssuedKey("");
        setNotice(detail);
      }
    }
    window.addEventListener("tokenhub-issued-key", onIssuedKey);
    return () => window.removeEventListener("tokenhub-issued-key", onIssuedKey);
  }, []);

  async function load(view: ViewKey = activeView) {
    if (!adminToken || !currentUser) return;
    if (!canAccessView(currentUser, view)) return;
    setLoading(true);
    setError("");
    try {
      const plan = loadPlanForView(currentUser, view);
      const requests: Array<{ name: string; request: Promise<Response>; optional?: boolean }> = [];
      const queue = (enabled: boolean, name: string, path: string) => {
        if (enabled) requests.push({ name, request: adminFetch(api, path) });
      };

      queue(plan.overview, "overview", "/api/admin/overview");
      queue(plan.providers, "providers", "/api/admin/providers");
      queue(plan.providerResources, "provider-resources", "/api/admin/provider-resources");
      queue(plan.providerModels, "provider-models", "/api/admin/provider-models");
      queue(plan.keys, "api-keys", "/api/admin/api-keys");
      queue(plan.routes, "routes", "/api/admin/routing-rules");
      queue(plan.logs, "audit", auditRequestPagePath({ page: 1, pageSize: 20, status: "all", query: "" }));
      queue(plan.auditEvents, "audit-events", "/api/admin/audit/events");
      queue(plan.alerts, "alerts", "/api/admin/alerts");
      queue(plan.alertDeliveries, "alert-deliveries", "/api/admin/alert-deliveries");
      queue(plan.approvals, "approvals", "/api/admin/approvals");
      queue(plan.sqliteBackups, "sqlite-backups", "/api/admin/sqlite/backups");
      queue(plan.dailyUsage, "daily-usage", "/api/admin/usage/daily");
      queue(plan.breakdown, "breakdown", "/api/admin/usage/breakdown");
      queue(plan.timeseries, "timeseries", "/api/admin/usage/timeseries");
      queue(plan.users, "users", "/api/admin/users");
      queue(plan.providerCatalog, "provider-catalog", "/api/admin/provider-catalog");
      queue(plan.providerAdapters, "provider-adapters", "/api/admin/provider-adapters");
      queue(plan.providerMonitoring, "provider-monitoring", "/api/admin/providers/monitoring");
      queue(plan.plugins, "plugins", "/api/admin/plugins");
      queue(plan.pluginChain, "plugin-chain", "/api/admin/plugin-chain");
      queue(plan.pluginUI, "plugin-ui", "/api/admin/plugin-ui-manifest");
      queue(plan.pluginActions, "plugin-actions", "/api/admin/plugin-actions");
      queue(plan.pluginBackgroundJobs, "plugin-background-jobs", "/api/admin/plugin-background-jobs");
			queue(plan.billingConnectors, "billing-connectors", "/api/admin/billing/connectors");
			queue(plan.billingRecords, "billing-records", "/api/admin/billing/records");
			queue(plan.billingSyncRuns, "billing-sync-runs", "/api/admin/billing/sync-runs");
			queue(plan.reconciliationRules, "reconciliation-rules", "/api/admin/billing/reconciliation-rules");
			queue(plan.reconciliationRuns, "reconciliation-runs", "/api/admin/billing/reconciliations");
      for (const kind of plan.resources) {
        requests.push({ name: `resource:${kind}`, request: adminFetch(api, `/api/admin/resources/${kind}`), optional: true });
      }

      const responses = await Promise.all(requests.map((item) => item.request));
      const skippedResources: string[] = [];
      for (let index = 0; index < responses.length; index += 1) {
        const resp = responses[index];
        if (!resp.ok) {
          if (resp.status === 403 && requests[index].optional) {
            skippedResources.push(loadRequestLabel(requests[index].name));
            continue;
          }
          throw new Error(await readLoadError(resp, requests[index].name));
        }
      }

      const loaded: LoadedData = {};
      for (let index = 0; index < responses.length; index += 1) {
        const name = requests[index].name;
        const resp = responses[index];
        if (!resp.ok) continue;
        if (name === "overview") {
          const overview = await resp.json();
          loaded.summary = overview.summary ?? emptySummary();
          loaded.projects = overview.projects ?? [];
          loaded.providers = overview.providers ?? [];
          loaded.providerResources = overview.provider_resources ?? [];
          loaded.models = overview.models ?? [];
          loaded.alerts = overview.alerts ?? [];
        } else if (name === "providers") {
          const payload = (await resp.json()) as { data: Provider[] };
          loaded.providers = payload.data ?? [];
        } else if (name === "provider-resources") {
          const payload = (await resp.json()) as { data: ProviderResource[] };
          loaded.providerResources = payload.data ?? [];
        } else if (name === "provider-models") {
          const payload = (await resp.json()) as { data: ProviderModel[] };
          loaded.providerModels = payload.data ?? [];
        } else if (name === "api-keys") {
          const payload = (await resp.json()) as { data: APIKey[] };
          loaded.keys = payload.data ?? [];
        } else if (name === "routes") {
          const payload = (await resp.json()) as { data: ModelRoute[] };
          loaded.routes = payload.data ?? [];
        } else if (name === "audit") {
          const payload = (await resp.json()) as { data: RequestLog[] };
          loaded.logs = payload.data ?? [];
        } else if (name === "audit-events") {
          const payload = (await resp.json()) as { data: AuditEvent[] };
          loaded.auditEvents = payload.data ?? [];
        } else if (name === "alerts") {
          const payload = (await resp.json()) as { data: AlertEvent[] };
          loaded.alerts = payload.data ?? [];
        } else if (name === "alert-deliveries") {
          const payload = (await resp.json()) as { data: AlertDelivery[] };
          loaded.alertDeliveries = payload.data ?? [];
        } else if (name === "approvals") {
          const payload = (await resp.json()) as { data: ApprovalRequest[] };
          loaded.approvals = payload.data ?? [];
        } else if (name === "sqlite-backups") {
          const payload = (await resp.json()) as { data: SQLiteBackup[] };
          loaded.sqliteBackups = payload.data ?? [];
        } else if (name === "daily-usage") {
          loaded.dailyUsage = (await resp.json()) as UsageDaily;
        } else if (name === "breakdown") {
          loaded.breakdown = (await resp.json()) as UsageBreakdown;
        } else if (name === "timeseries") {
          const payload = (await resp.json()) as { data: UsagePoint[] };
          loaded.timeseries = payload.data ?? [];
        } else if (name === "users") {
          const payload = (await resp.json()) as { data: AdminUser[] };
          loaded.users = payload.data ?? [];
        } else if (name === "provider-catalog") {
          const payload = (await resp.json()) as { data: ProviderCatalogEntry[] };
          loaded.providerCatalog = payload.data ?? [];
        } else if (name === "provider-adapters") {
          const payload = (await resp.json()) as { data: AdapterDescriptor[] };
          loaded.providerAdapters = payload.data ?? [];
        } else if (name === "provider-monitoring") {
          const payload = (await resp.json()) as { data: ProviderMonitoringSnapshot[] };
          loaded.providerMonitoring = payload.data ?? [];
        } else if (name === "plugins") {
          const payload = (await resp.json()) as { data: PluginDescriptor[] };
          loaded.plugins = payload.data ?? [];
        } else if (name === "plugin-chain") {
          const payload = (await resp.json()) as { data: GatewayChainPlan };
          loaded.pluginChain = payload.data ?? { hooks: [] };
        } else if (name === "plugin-ui") {
          const payload = (await resp.json()) as { data: AdminUIContribution[] };
          loaded.pluginUI = payload.data ?? [];
        } else if (name === "plugin-actions") {
          const payload = (await resp.json()) as { data: PluginActionDescriptor[] };
          loaded.pluginActions = payload.data ?? [];
        } else if (name === "plugin-background-jobs") {
          const payload = (await resp.json()) as { data: PluginBackgroundJobDescriptor[]; runs?: PluginBackgroundJobRunRecord[] };
          loaded.pluginBackgroundJobs = payload.data ?? [];
          loaded.pluginBackgroundRuns = payload.runs ?? [];
				} else if (name === "billing-connectors") {
					const payload = (await resp.json()) as { data: BillingConnector[] };
					loaded.billingConnectors = payload.data ?? [];
				} else if (name === "billing-records") {
					const payload = (await resp.json()) as { data: BillingRecord[] };
					loaded.billingRecords = payload.data ?? [];
				} else if (name === "billing-sync-runs") {
					const payload = (await resp.json()) as { data: BillingSyncRun[] };
					loaded.billingSyncRuns = payload.data ?? [];
				} else if (name === "reconciliation-rules") {
					const payload = (await resp.json()) as { data: ReconciliationRule[] };
					loaded.reconciliationRules = payload.data ?? [];
				} else if (name === "reconciliation-runs") {
					const payload = (await resp.json()) as { data: ReconciliationRun[] };
					loaded.reconciliationRuns = payload.data ?? [];
        } else if (name.startsWith("resource:")) {
          const kind = name.slice("resource:".length);
          const payload = (await resp.json()) as { data: AdminResource[] };
          loaded.resources = { ...(loaded.resources ?? {}), [kind]: payload.data ?? [] };
        }
      }

      setData((current) => mergeLoadedData(current, loaded));
      if (skippedResources.length > 0) {
        setNotice(permissionPartialLoadMessage(skippedResources));
      }
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("连接失败"));
    } finally {
      setLoading(false);
    }
  }
  loadRef.current = load;

  async function login(identity: string, password: string) {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch(`${baseURL.replace(/\/$/, "")}/api/admin/auth/login`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ identity, password }),
      });
      if (!resp.ok) throw new Error(`login ${resp.status}`);
      const payload = (await resp.json()) as { token: string; user: AdminUser; expires_at: string };
      setData(emptyData());
      setAdminToken(payload.token);
      setCurrentUser(payload.user);
      saveSession({ baseURL, token: payload.token, user: payload.user, expiresAt: payload.expires_at });
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("登录失败"));
    } finally {
      setLoading(false);
    }
  }

  async function resetPassword(token: string, password: string) {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const resp = await fetch(`${baseURL.replace(/\/$/, "")}/api/admin/auth/reset-password`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token, password }),
      });
      if (!resp.ok) throw new Error(`reset password ${resp.status}`);
      setResetToken("");
      setNotice(tx("密码已重置，请使用新密码登录"));
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("密码重置失败"));
    } finally {
      setLoading(false);
    }
  }

  async function logout() {
    if (adminToken) {
      await adminFetch(api, "/api/admin/auth/logout", { method: "POST" }).catch(() => undefined);
    }
    clearSavedSession();
    setAdminToken("");
    setCurrentUser(null);
    setData(emptyData());
    setProjectWorkspace(null);
    setUserImportOpen(false);
    setApiKeyWizardOpen(false);
    setApiKeyWizardInitialValues({});
    setConfirmRestoreModels(false);
    selectView("overview", { replace: true });
  }

  async function saveModal(values: Record<string, string>) {
    if (!modal) return;
    setLoading(true);
    setError("");
    try {
      if (modal.item) {
        await modal.config.update?.(api, modal.item, values);
      } else {
        await modal.config.create?.(api, values, data);
      }
      setModal(null);
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("保存失败"));
    } finally {
      setLoading(false);
    }
  }

  async function saveProjectWorkspace(draft: ProjectWorkspaceDraft) {
    const existing = projectWorkspace?.projectID
      ? data.projects.find((project) => project.id === projectWorkspace.projectID)
      : undefined;
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const saved = await saveProjectWorkspaceDraft(api, existing, draft);
      await load("projects");
      setProjectWorkspace({ mode: "view", projectID: saved.id });
      setNotice(tx(existing ? "项目设置已保存" : "项目已创建，团队权限已配置"));
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      if (err instanceof ProjectWorkspaceSaveError && err.projectID) {
        await load("projects");
        setProjectWorkspace({ mode: "edit", projectID: err.projectID });
      }
      setError(err instanceof Error ? err.message : tx("保存失败"));
    } finally {
      setLoading(false);
    }
  }

  async function deleteItem<T>(config: ResourceConfig<T>, item: T) {
    if (!config.remove) return;
    setLoading(true);
    setError("");
    try {
      await config.remove(api, item);
      setIssuedKey("");
      setConfirmDelete(null);
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("删除失败"));
    } finally {
      setLoading(false);
    }
  }

  async function deleteItems<T>(config: ResourceConfig<T>, items: T[]) {
    if (!config.remove || items.length === 0) return;
    setLoading(true);
    setError("");
    let deletedCount = 0;
    try {
      for (const item of items) {
        await config.remove(api, item);
        deletedCount += 1;
      }
      setIssuedKey("");
      setConfirmDelete(null);
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      if (deletedCount > 0) {
        setConfirmDelete(null);
        await load();
      }
      setError(err instanceof Error ? err.message : tx("删除失败"));
    } finally {
      setLoading(false);
    }
  }

  async function restoreModelsFromDefaultCatalog() {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      await restoreDefaultModelCatalog(api);
      setConfirmRestoreModels(false);
      setNotice(tx("模型参考目录已同步"));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setLoading(false);
    }
  }

  async function importUsersFromCSV(content: string) {
    const trimmed = content.trim();
    if (!trimmed) {
      setNotice("");
      setError(tx("请先粘贴 CSV 内容。"));
      return;
    }
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const result = await importUsersFromCSVContent(api, trimmed);
      const created = result.created ?? 0;
      const updated = result.updated ?? 0;
      const skipped = result.skipped ?? 0;
      setUserImportOpen(false);
      await load("users");
      setNotice(importUsersDoneMessage(created, updated, skipped));
      if (skipped > 0 && result.errors?.length) {
        setError(importUsersSkippedMessage(skipped, result.errors.slice(0, 3).join("；")));
      }
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("用户导入失败"));
    } finally {
      setLoading(false);
    }
  }

  function openCreateRoute(model: Model) {
    if (!activeConfig) return;
    if (loading) {
      setNotice("");
      setError(tx("数据加载中，请稍后再操作。"));
      return;
    }
    if (data.providers.length === 0) {
      setNotice("");
      setError(tx("请先新增 Provider 渠道，再配置路由策略。"));
      selectView("providers");
      return;
    }
    const routeConfig = activeConfig as ResourceConfig<ModelRoute>;
    setModal({
      config: {
        ...routeConfig,
        title: `${tx("添加线路")} · ${model.name}`,
        fields: routeConfig.fields.filter((field) => field.key !== "model_name"),
      },
      initialValues: modelRouteDefaults(model, data),
    });
  }

  function openCreateAPIKey() {
    if (loading) {
      setNotice("");
      setError(tx("数据加载中，请稍后再操作。"));
      return;
    }
    setIssuedKey("");
    setApiKeyWizardInitialValues({});
    setApiKeyWizardOpen(true);
  }

  function quickCreateAPIKey(values: Record<string, string>, onCreated: () => void) {
    void createKeyWithCapture(api, values, setIssuedKey, setNotice, load, setLoading, setError, onCreated);
  }

  function openCreateForCurrentView() {
    if (!activeConfig) return;
    if (loading) {
      setNotice("");
      setError(tx("数据加载中，请稍后再操作。"));
      return;
    }
    switch (resourceCreateTarget(activeConfig.view)) {
      case "provider-modal":
        setProviderCreateOpen(true);
        return;
      case "project-workspace":
        setProjectWorkspace({ mode: "create" });
        return;
      case "notification-channel-modal":
        setModal({ config: activeConfig, initialValues: notificationChannelDefaults(modelCategoryFilter) });
        return;
      case "api-key-wizard":
        openCreateAPIKey();
        return;
      default:
        setModal({ config: activeConfig });
    }
  }

  async function saveModelRoutingPolicy(model: Model, policy: ModelRoutePolicy, successMessage = `已应用 ${model.name} 的模型路由策略`) {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      await adminMutate(api, `/api/admin/model-routing-policies/${encodeURIComponent(model.name)}`, "PATCH", policy);
      setNotice(tx(successMessage));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("更新模型路由策略失败"));
    } finally {
      setLoading(false);
    }
  }

  function reorderModelRoutes(model: Model, orderedRoutes: ModelRoute[]) {
    return saveModelRoutingPolicy(
      model,
      modelRoutePolicyPayload("priority_only", orderedRoutes),
      `已更新 ${model.name} 的 Provider 调用顺序`,
    );
  }

  const viewItems = activeConfig?.list(data) ?? [];
  const categoryItems = filterByModelCategory(activeConfig?.view, viewItems, modelCategoryFilter, data);
  const filteredItems =
    activeConfig?.view === "api-keys" ? filterAPIKeys(categoryItems as APIKey[], query) : filterRows(categoryItems, query);
  const crudPagination = usePagination(filteredItems.length, `${activeView}:${modelCategoryFilter}:${query}`);
  const pagedItems = useMemo(
    () => filteredItems.slice(crudPagination.startIndex, crudPagination.endIndex),
    [filteredItems, crudPagination.startIndex, crudPagination.endIndex],
  );
  const workspaceProject = projectWorkspace?.projectID
    ? data.projects.find((project) => project.id === projectWorkspace.projectID)
    : undefined;

  if (!bootstrapped) {
    return <main className="login-shell" />;
  }

  if (resetToken && !currentUser) {
    return (
      <ResetPasswordView
        loading={loading}
        error={error}
        language={language}
        theme={theme}
        onLanguageChange={changeLanguage}
        onThemeToggle={toggleTheme}
        token={resetToken}
        onReset={(token, password) => void resetPassword(token, password)}
      />
    );
  }

  if (!currentUser) {
    return (
      <LoginView
        loading={loading}
        error={error}
        baseURL={baseURL}
        identityProviders={loginIdentityProviders}
        oauthReturnURL={oauthReturnURL}
        language={language}
        theme={theme}
        onLanguageChange={changeLanguage}
        onThemeToggle={toggleTheme}
        onLogin={(identity, password) => void login(identity, password)}
      />
    );
  }

  return (
    <main
      className={sidebarCollapsed ? "app-shell sidebar-collapsed" : "app-shell"}
      data-layout-density={shellPresentation.density}
      data-theme={theme}
      style={shellPresentation.style}
    >
      <Sidebar
        activeView={activeView}
        onSelect={selectView}
        user={currentUser}
        data={data}
        activePluginPageKey={activePluginPageKey}
        onLogout={() => void logout()}
        onSelectPluginPage={selectPluginPage}
        collapsed={sidebarCollapsed}
        onToggleCollapse={() => setSidebarCollapsed((value) => !value)}
        openGroups={openNavGroups}
        onToggleGroup={(title) =>
          setOpenNavGroups((current) => ({ ...current, [title]: current[title] === false }))
        }
      />
      <ResponsiveVersionStatus api={api} user={currentUser} />

      <section className="workspace">
        <TopNav
          activeView={activeView}
          data={data}
          user={currentUser}
          language={language}
          theme={theme}
          onLanguageChange={changeLanguage}
          onSelectView={selectView}
          onThemeToggle={toggleTheme}
        />

        <div className={activeView === "playground" ? "content-panel playground-content-panel" : "content-panel"}>
          {activeView === "playground" || activeView === "overview" || apiKeyUsageID ? null : (
            <PageHeader activeView={activeView} data={data} meta={activeMeta} user={currentUser} />
          )}

          <StatusStack
            error={error}
            notice={notice}
            onClearError={() => setError("")}
            onClearNotice={() => setNotice("")}
          />

          {activeView === "playground" || apiKeyUsageID ? null : <div className="divider" />}

          {apiKeyUsageID ? (
            <APIKeyUsageView api={api} data={data} user={currentUser} keyID={apiKeyUsageID} onBack={() => selectView("api-keys")} />
          ) : activeView === "overview" ? (
            <OverviewView data={data} user={currentUser} onSelectView={selectView} />
          ) : activeView === "playground" ? (
            <PlaygroundPage api={api} data={data} canViewRoutes={canAccessView(currentUser, "routes")} />
          ) : activeView === "gateway" ? (
            <GatewayView
              api={api}
              data={data}
              user={currentUser}
              language={language}
              onLanguageChange={changeLanguage}
              loading={loading}
              onQuickCreateKey={quickCreateAPIKey}
              onManageKeys={() => selectView("api-keys")}
            />
          ) : activeView === "usage" ? (
            <UsageView api={api} data={data} user={currentUser} />
          ) : activeView === "billing" ? (
            <BillingView api={api} data={data} user={currentUser} loading={loading} onReload={() => load("billing")} />
          ) : activeView === "audit" ? (
            <AuditView api={api} data={data} user={currentUser} />
          ) : activeView === "database-status" ? (
            <DatabaseStatusView api={api} isDark={theme === "dark"} />
          ) : activeView === "plugins" ? (
            <PluginsView api={api} data={data} />
          ) : activeView === "plugin-pages" ? (
            <PluginPageView activePageKey={activePluginPageKey} api={api} data={data} onSelectPage={selectPluginPage} />
          ) : activeView === "settings" ? (
            <SettingsView
              api={api}
              data={data}
              activeTab={settingsTab}
              language={language}
              onTabChange={setSettingsTab}
              onLanguageChange={changeLanguage}
              onRestoreModelCatalog={() => setConfirmRestoreModels(true)}
              onCreate={(config) => setModal({ config })}
              onEdit={(config, item) => setModal({ config, item })}
              onDelete={(config, item) => setConfirmDelete({ config, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
              onToolbarAction={(action, items) => void runToolbarAction(action, items)}
            />
          ) : activeView === "security-policies" && activeConfig ? (
            <div className="security-policies-view">
              <SecurityPolicyTabs active={securityPolicyTab} onChange={setSecurityPolicyTab} />
              {securityPolicyTab === "content" ? (
                <ContentSecurityPolicies api={api} data={data} />
              ) : (
                <CrudView
                  config={activeConfig}
                  data={data}
                  api={api}
                  user={currentUser}
                  items={pagedItems}
                  monitorItems={filteredItems}
                  totalItems={filteredItems.length}
                  loading={loading}
                  query={query}
                  currentUser={currentUser}
                  pagination={crudPagination}
                  categoryFilter={modelCategoryFilter}
                  onCategoryFilter={setModelCategoryFilter}
                  onQuery={setQuery}
                  onCreate={openCreateForCurrentView}
                  onEdit={(item) => setModal({ config: activeConfig, item })}
                  onDelete={(item) => setConfirmDelete({ config: activeConfig, item })}
                  onAction={(action, item) => void runResourceAction(action, item, data)}
                  onToolbarAction={(action) => void runToolbarAction(action, filteredItems)}
                />
              )}
            </div>
          ) : activeView === "routes" && activeConfig ? (
            <RouteStrategyView
              config={activeConfig as ResourceConfig<ModelRoute>}
              data={data}
              initialQuery={routeModelQuery}
              loading={loading}
              onCreate={openCreateRoute}
              onOpenModels={() => selectView("models")}
              onOpenProviders={() => selectView("providers")}
              onEdit={(route) => setModal({ config: activeConfig, item: route })}
              onDelete={(route) => setConfirmDelete({ config: activeConfig, item: route })}
              onReorder={(model, routes) => void reorderModelRoutes(model, routes)}
              onSavePolicy={(model, policy) => void saveModelRoutingPolicy(model, policy)}
            />
          ) : activeView === "models" && activeConfig ? (
            <ModelDirectoryView
              api={api}
              config={activeConfig as ResourceConfig<Model>}
              data={data}
              loading={loading}
              readOnly={!canAccessView(currentUser, "routes")}
              onReload={() => load("models")}
              onCreateModel={() => setModal({ config: activeConfig })}
              onOpenProviders={() => selectView("providers")}
              onOpenRoutes={openRoutes}
              onEditModel={(item) => setModal({ config: activeConfig, item })}
              onDeleteModel={(item) => setConfirmDelete({ config: activeConfig, item })}
            />
          ) : activeView === "reports" && activeConfig ? (
            <ReportsView
              config={activeConfig as ResourceConfig<AdminResource>}
              data={data}
              history={reportHistory}
              loading={loading}
              onCreate={() => setModal({ config: activeConfig })}
              onEdit={(item) => setModal({ config: activeConfig, item })}
              onDelete={(item) => setConfirmDelete({ config: activeConfig, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
              onExport={(dataset) => void exportReportDataset(dataset)}
            />
          ) : activeConfig ? (
            <>
              {activeView === "routing-policies" ? <RoutingPolicySimulator api={api} data={data} /> : null}
              <CrudView
              config={activeConfig}
              data={data}
              api={api}
              user={currentUser}
              items={pagedItems}
              monitorItems={filteredItems}
              totalItems={filteredItems.length}
              loading={loading}
              query={query}
              currentUser={currentUser}
              pagination={crudPagination}
              categoryFilter={modelCategoryFilter}
              onCategoryFilter={setModelCategoryFilter}
              onQuery={setQuery}
              onCreate={openCreateForCurrentView}
              onEdit={(item) => {
                if (activeConfig.view === "projects") {
                  setProjectWorkspace({ mode: "edit", projectID: (item as Project).id });
                  return;
                }
                if (activeConfig.view === "providers") {
                  setProviderEditItem(item as Provider);
                  return;
                }
                setModal({ config: activeConfig, item });
              }}
              onDelete={(item) => setConfirmDelete({ config: activeConfig, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
              onProjectOpen={(project) => setProjectWorkspace({ mode: "view", projectID: project.id })}
              onToolbarAction={(action) => void runToolbarAction(action, filteredItems)}
              />
            </>
          ) : null}
        </div>
      </section>

      {projectWorkspace && (!projectWorkspace.projectID || workspaceProject) ? (
        <ProjectWorkspace
          mode={projectWorkspace.mode}
          data={data}
          project={workspaceProject}
          loading={loading}
          onClose={() => {
            if (!loading) setProjectWorkspace(null);
          }}
          onEdit={() => setProjectWorkspace((current) => current ? { ...current, mode: "edit" } : current)}
          onSave={(draft) => void saveProjectWorkspace(draft)}
          onIssueKey={() => {
            if (!workspaceProject) return;
            setIssuedKey("");
            setApiKeyWizardInitialValues({ project_id: workspaceProject.id, name: `${workspaceProject.name} Key` });
            setApiKeyWizardOpen(true);
          }}
          onAction={(action) => {
            if (workspaceProject) void runResourceAction(action, workspaceProject, data);
          }}
          onCreateMember={() => {
            if (workspaceProject) setModal({ config: projectMemberConfig(), initialValues: projectMemberInitialValues(workspaceProject) });
          }}
          onEditMember={(member) => setModal({ config: projectMemberConfig(), item: member })}
          onDeleteMember={(member) => setConfirmDelete({ config: projectMemberConfig(), item: member })}
        />
      ) : null}

      {modal ? (
        <EditModal
          state={modal}
          data={data}
          api={api}
          currentUser={currentUser}
          loading={loading}
          onClose={() => setModal(null)}
          onSave={(values) => {
            if (modal.config.view === "api-keys" && !modal.item) {
              void createKeyWithCapture(api, values, setIssuedKey, setNotice, load, setLoading, setError, () => setModal(null));
              return;
            }
            void saveModal(values);
          }}
        />
      ) : null}

      {providerCreateOpen ? (
        <ProviderUpsertModal
          mode="create"
          api={api}
          catalog={data.providerCatalog}
          standardModels={data.models}
          providerModels={data.providerModels}
          resources={data.providerResources}
          pluginUI={data.pluginUI}
          pluginActions={data.pluginActions}
          plugins={data.plugins}
          providerTypeOptions={providerTypeOptions}
          loading={loading}
          onClose={() => setProviderCreateOpen(false)}
          onSaved={async () => {
            setProviderCreateOpen(false);
            await load();
          }}
          setLoading={setLoading}
          setError={setError}
          setNotice={setNotice}
        />
      ) : null}

      {providerEditItem ? (
        <ProviderUpsertModal
          mode="edit"
          provider={providerEditItem}
          api={api}
          catalog={data.providerCatalog}
          standardModels={data.models}
          providerModels={data.providerModels}
          routes={data.routes}
          resources={data.providerResources.filter((resource) => resource.provider_id === providerEditItem.id)}
          pluginUI={data.pluginUI}
          pluginActions={data.pluginActions}
          plugins={data.plugins}
          providerTypeOptions={providerTypeOptions}
          loading={loading}
          onClose={() => setProviderEditItem(null)}
          onSaved={async () => {
            setProviderEditItem(null);
            await load();
          }}
          onAccountsChanged={async () => {
            await load();
          }}
          setLoading={setLoading}
          setError={setError}
          setNotice={setNotice}
        />
      ) : null}

      {apiKeyWizardOpen ? (
        <APIKeyWizardModal
          data={data}
          currentUser={currentUser}
          initialValues={apiKeyWizardInitialValues}
          loading={loading}
          onClose={() => {
            if (!loading) {
              setApiKeyWizardOpen(false);
              setApiKeyWizardInitialValues({});
            }
          }}
          onCreate={(values) => {
            setIssuedKey("");
            void createKeyWithCapture(api, values, setIssuedKey, setNotice, load, setLoading, setError, () => {
              setApiKeyWizardOpen(false);
              setApiKeyWizardInitialValues({});
            });
          }}
        />
      ) : null}

      {userImportOpen ? (
        <UserImportModal
          loading={loading}
          onClose={() => setUserImportOpen(false)}
          onImport={(content) => void importUsersFromCSV(content)}
        />
      ) : null}

      {confirmDelete ? (
        <ConfirmDialog
          title={tx("确认删除")}
          message={confirmDelete.items?.length ? bulkDeleteConfirmMessage(confirmDelete.items.length) : deleteConfirmMessage(rowTitle(confirmDelete.item))}
          loading={loading}
          onCancel={() => setConfirmDelete(null)}
          onConfirm={() => {
            if (confirmDelete.items?.length) {
              void deleteItems(confirmDelete.config, confirmDelete.items);
              return;
            }
            if (confirmDelete.item) void deleteItem(confirmDelete.config, confirmDelete.item);
          }}
        />
      ) : null}

      {confirmRestoreModels ? (
        <ConfirmDialog
          title={tx("同步模型参考目录")}
          message={tx("将从配置文件重新同步跟踪模型元数据；Provider 模型库存、路由和手工新增的其他对外模型会保留。")}
          confirmLabel="同步"
          confirmClassName="button"
          loading={loading}
          onCancel={() => setConfirmRestoreModels(false)}
          onConfirm={() => void restoreModelsFromDefaultCatalog()}
        />
      ) : null}

      {issuedKey ? (
        <IssuedKeyModal
          value={issuedKey}
          onClose={() => setIssuedKey("")}
        />
      ) : null}

    </main>
  );

  async function runResourceAction<T>(action: ResourceAction<T>, item: T, appData: AppData) {
    if (action.navigate) {
      selectView(action.navigate(item));
      return;
    }
    if (action.modal) {
      const nextModal = action.modal(item, appData);
      if (nextModal.config.view === "api-keys" && !nextModal.item) {
        setIssuedKey("");
        setApiKeyWizardInitialValues(nextModal.initialValues ?? {});
        setApiKeyWizardOpen(true);
        return;
      }
      setModal(nextModal);
      return;
    }
    if (!action.run) return;
    setLoading(true);
    setError("");
    setNotice("");
    try {
      await action.run(api, item, appData);
      setNotice(tx(action.doneMessage?.(item) ?? "操作已完成"));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setLoading(false);
    }
  }

  async function runToolbarAction(action: ToolbarAction, items: unknown[]) {
    if (action.kind === "import-users") {
      setError("");
      setNotice("");
      setUserImportOpen(true);
      return;
    }
    if (!action.run) return;
    setLoading(true);
    setError("");
    setNotice("");
    try {
      await action.run(api, items);
      setNotice(tx(action.doneMessage?.() ?? "操作已完成"));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setLoading(false);
    }
  }

  async function exportReportDataset(dataset: string) {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const result = await downloadReport(api, dataset);
      if (result) {
        setNotice(tx(`${reportDatasetLabel(dataset)} 已导出`));
        setReportHistory((current) => [
          {
            id: uniqueUIID("export"),
            dataset,
            file_name: result.fileName,
            period: result.period,
            exported_at: new Date().toISOString(),
          },
          ...current,
        ].slice(0, 8));
      }
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("导出失败"));
    } finally {
      setLoading(false);
    }
  }
}

function pluginPageKeyFromLocation() {
  if (typeof window === "undefined") return "";
  return new URLSearchParams(window.location.search).get("plugin_page") ?? "";
}

function pluginPageURLSuffix(view: ViewKey, pluginPageKey?: string) {
  if (typeof window === "undefined") return "";
  const hash = window.location.hash;
  if (view !== "plugin-pages") return hash;
  const params = new URLSearchParams();
  if (pluginPageKey) params.set("plugin_page", pluginPageKey);
  const query = params.toString();
  return `${query ? `?${query}` : ""}${hash}`;
}

function currentLocationSuffix() {
  if (typeof window === "undefined") return "";
  return `${window.location.search}${window.location.hash}`;
}
