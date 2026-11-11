import {
  type AdapterDescriptor, type AdminResource, type AdminUIContribution, type AdminUser,
  type AlertDelivery, type AlertEvent, type APIKey, type ApprovalRequest, type AuditEvent,
  type BillingConnector, type BillingRecord, type BillingSyncRun, type GatewayChainPlan,
  type ModelRoute, type PluginActionDescriptor, type PluginBackgroundJobDescriptor,
  type PluginBackgroundJobRunRecord, type PluginDescriptor, type PluginMarketplacePlugin,
  type Provider, type ProviderCatalogEntry, type ProviderModel, type ProviderMonitoringSnapshot,
  type ProviderResource, type ReconciliationRule, type ReconciliationRun, type RequestLog,
  type SQLiteBackup, type UsageBreakdown, type UsageDaily, type UsagePoint,
} from "../core/types";
import type { LoadedData } from "../core/data-loading";
import { emptySummary } from "../domain/catalog";

// applyViewPayload decodes one loaded admin response into the fields it owns. The
// request-to-field mapping is large but mechanical, so it lives here rather than inline
// in the console component: every branch is independent, which keeps the console's load
// loop readable and lets this table grow without pushing that component over its
// line-count ceiling.
export async function applyViewPayload(loaded: LoadedData, name: string, resp: Response): Promise<void> {
  if (!resp.ok) return;
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
  } else if (name === "plugin-marketplace") {
    const payload = (await resp.json()) as {
      data?: {
        source_url?: string;
        available?: boolean;
        error?: string;
        plugins?: PluginMarketplacePlugin[];
      };
    };
    loaded.pluginMarketplace = payload.data?.plugins ?? [];
    loaded.pluginMarketplaceSourceURL = payload.data?.source_url;
    loaded.pluginMarketplaceAvailable = payload.data?.available;
    loaded.pluginMarketplaceError = payload.data?.error;
  } else if (name === "plugin-chain") {
    const payload = (await resp.json()) as { data: GatewayChainPlan };
    loaded.pluginChain = payload.data ? { ...payload.data, hooks: payload.data.hooks ?? [] } : { hooks: [] };
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
