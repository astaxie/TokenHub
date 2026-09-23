import type { AdminUser, Model, Project, Summary, UsageBreakdown } from "../../../features/admin/core/types";

export const fixedTime = "2026-09-07T02:00:00.000Z";
export const model = { id: "mdl_ui", name: "ui-review-model", family: "test", modality: "chat", status: "active", input_price_usd_per_1m: 2, output_price_usd_per_1m: 6 } satisfies Model;
export const project = { id: "prj_ui", name: "UI Review Project", status: "active" } satisfies Project;
export const user = { id: "usr_ui", username: "ui-admin", name: "UI Review Admin", email: "ui-admin@example.test", role: "admin", status: "active" } satisfies AdminUser;
const summary = { request_count: 3, input_tokens: 1000000, output_tokens: 10000, total_tokens: 1010000, estimated_cost_usd: 1.08, errors: 0 } satisfies Summary;
const breakdown = { projects: [], models: [], members: [], providers: [], provider_resources: [], cost_centers: [], api_keys: [] } satisfies UsageBreakdown;

// Every route is explicit. Missing endpoints are handled by the strict harness.
export function shellResponses(): Map<string, unknown> {
  return new Map<string, unknown>([
    ["GET /api/admin/overview", { summary, projects: [project], models: [model], providers: [], provider_resources: [], alerts: [] }],
    ["GET /api/admin/projects", { data: [project] }],
    ["GET /api/admin/provider-models", { data: [] }],
    ["GET /api/admin/usage/breakdown", breakdown],
    ["GET /api/admin/billing/connectors", { data: [] }],
    ["GET /api/admin/billing/records", { data: [] }],
    ["GET /api/admin/billing/sync-runs", { data: [] }],
    ["GET /api/admin/billing/reconciliation-rules", { data: [] }],
    ["GET /api/admin/billing/reconciliations", { data: [] }],
    ["GET /api/admin/system/version", { current_version: "ui-fixture", latest_version: "ui-fixture", has_update: false, build_type: "source", deployment_type: "source", update_supported: false }],
  ].map(([key, value]) => [key, structuredClone(value)] as [string, unknown]));
}
