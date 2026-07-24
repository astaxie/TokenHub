import { formatNumber } from "../domain/formatting";
import { type GatewayDocBundle, type GatewayDocStats } from "./gateway-view";

export function gatewayEnglishDocs(stats: GatewayDocStats): GatewayDocBundle {
  return {
    nav: {
      title: "Documentation",
      subtitle: "Role-based guides and API references",
      searchPlaceholder: "Search guides, APIs, or error codes",
      noResults: "No matching documents",
    },
    eyebrow: "TokenHub Docs",
    title: "Role-Based Gateway Guides",
    description: "TokenHub is documented around three enterprise roles: users call approved models, team leaders manage projects and members, and administrators govern providers, routing, identity, audit, and cost.",
    languageLabel: "Documentation language",
    quickInfoLabel: "API basics",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "Sample model",
      currentConfig: "Current configuration",
      activeRoutes: `${formatNumber(stats.activeRouteCountValue)} active route${stats.activeRouteCountValue === 1 ? "" : "s"}`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} API Key${stats.apiKeyCount === 1 ? "" : "s"}`,
    },
    groups: [
      {
        title: "Introduction",
        items: [
          {
            id: "overview",
            group: "Introduction",
            badge: "DOC",
            title: "Platform Overview",
            description: "Understand how TokenHub connects models, projects, keys, routing, audit, and cost attribution into one governed AI access path.",
            details: [
              { label: "Primary entry points", value: "Model Playground / Key Management / Usage Analytics" },
              { label: "Application API", value: "/v1/*" },
              { label: "Admin API", value: "/api/admin/*" },
              { label: "Data scope", value: "Personal / Team / Platform" },
            ],
            notesTitle: "First steps",
            notes: [
              "Users start with available models, key management, personal usage, and request logs; they do not need provider credentials.",
              "Team leaders work from project spaces and use the project detail panel to manage members, keys, quotas, and attribution.",
              "Administrators connect providers, publish model catalog entries, enable routing rules, configure identity sources, and monitor audit and cost controls.",
            ],
          },
          {
            id: "concepts",
            group: "Introduction",
            badge: "DOC",
            title: "Core Concepts",
            description: "A shared vocabulary for the resource and permission boundaries in the enterprise AI gateway.",
            table: {
              title: "Concepts",
              columns: ["Concept", "Meaning"],
              rows: [
                ["Project", "An internal application or business space. It is the basic unit for keys, quota, members, and cost attribution."],
                ["API Key", "A credential attached to a project and used by applications to call /v1/* model endpoints."],
                ["Model Catalog", "The standard model list shown to users. A model is callable only when it has an enabled route."],
                ["Routing Rule", "Maps a standard model to an upstream provider model and defines priority, weight, and strategy."],
                ["Provider", "An upstream model service or internal model resource with Base URL, credentials, and health state."],
                ["Usage Attribution", "Requests, tokens, and cost are attributed to users, projects, teams, and cost centers."],
              ],
            },
          },
        ],
      },
      {
        title: "Role Guides",
        items: [
          {
            id: "user-guide",
            group: "Role Guides",
            badge: "USER",
            title: "User Guide",
            description: "Users focus on available models, project keys, API examples, personal usage, and request logs.",
            details: [
              { label: "Default menu", value: "Overview / API Documentation / Model Playground" },
              { label: "Resource scope", value: `${formatNumber(stats.visibleModelCount)} visible models` },
              { label: "Key ownership", value: "Assigned project" },
              { label: "Report scope", value: "Personal usage" },
            ],
            notesTitle: "Daily workflow",
            notes: [
              "Open Available Models or Model Playground to confirm which models are callable for your account.",
              "Open Key Management, choose an assigned project, and create or copy an application API key.",
              "Applications should call model endpoints such as /v1/chat/completions, /v1/responses, and /v1/embeddings.",
              "For 401, 403, or 429 responses, copy request_id into Request Logs or ask your team leader to adjust project access.",
            ],
            table: {
              title: "What users can do",
              columns: ["Task", "Where", "Notes"],
              rows: [
                ["Review models", "Available Models", "Shows the models callable by the current account."],
                ["Test prompts", "Model Playground", "Checks prompts, responses, routing, and estimated cost."],
                ["Manage keys", "Key Management", "Keys must be created under an assigned project."],
                ["Review usage", "Usage Analytics", "Shows only requests, tokens, and cost visible to the current account."],
              ],
            },
          },
          {
            id: "team-leader-guide",
            group: "Role Guides",
            badge: "LEAD",
            title: "Team Leader Guide",
            description: "Team leaders manage project spaces, project members, key issuance, team reports, and project-level cost attribution.",
            details: [
              { label: "Default menu", value: "Team Overview / Projects / Key Management" },
              { label: "Projects", value: `${formatNumber(stats.projectCount)} projects` },
              { label: "Member management", value: "Project detail side panel" },
              { label: "Report scope", value: "Team and project usage" },
            ],
            notesTitle: "Project governance workflow",
            notes: [
              "Create or select a project in Project Spaces. A project is the boundary for members, keys, quota, and cost attribution.",
              "Click a project to open the right-side detail panel, then view, add, edit, or remove project members there.",
              "When issuing keys, use project membership roles to decide whether a user can create application keys.",
              "Use Team Reports to compare usage by member, project, model, and cost center.",
            ],
            table: {
              title: "Project membership roles",
              columns: ["Role", "Default capability"],
              rows: [
                ["Owner", "Manages project settings, members, keys, and quota."],
                ["Maintainer", "Maintains members and keys; suitable for project technical owners."],
                ["Developer", "Creates and uses project keys; suitable for application developers."],
                ["Viewer", "Views project data and usage but cannot issue keys."],
              ],
            },
          },
          {
            id: "administrator-guide",
            group: "Role Guides",
            badge: "ADMIN",
            title: "Administrator Guide",
            description: "Administrators govern providers, model catalog, routing policies, identity sources, RBAC, audit, security, and cost controls.",
            details: [
              { label: "Default menu", value: "Platform Overview / Providers / Routes / Settings" },
              { label: "Providers", value: `${formatNumber(stats.providerCount)} providers` },
              { label: "Routing rules", value: `${formatNumber(stats.routeCount)} rules` },
              { label: "Users", value: `${formatNumber(stats.userCount)} users` },
            ],
            notesTitle: "Production setup order",
            notes: [
              "Configure upstream Base URLs, credentials, resource groups, and health checks in Provider Channels.",
              "Maintain standard public model names, capability tags, context windows, and price units in Model Catalog.",
              "Create at least one enabled routing rule for every model that should be visible and callable.",
              "Configure identity providers, role permissions, default policies, audit retention, and enterprise integrations in System Settings.",
            ],
            table: {
              title: "Administrator checklist",
              columns: ["Area", "Check"],
              rows: [
                ["Identity", "Configure at least one enterprise identity source and retain a controlled administrator account."],
                ["Routing", "Models without configured routes must be visually distinguished in the admin model catalog."],
                ["Security", "API keys are shown once; rotation and deletion must leave audit records."],
                ["Cost", "Provider, project, team, and cost center attribution should remain traceable."],
              ],
            },
          },
        ],
      },
      {
        title: "API Reference",
        items: [
          {
            id: "model-api",
            group: "API Reference",
            title: "Model API",
            method: "POST",
            path: "/v1/chat/completions",
            description: "Call OpenAI-compatible model endpoints with a project API key.",
            params: {
              title: "Request parameters",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["Authorization", "header", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["model", "string", "Yes", `Standard model name, for example ${stats.sampleModel}`],
                ["messages", "array", "Yes", "system/user/assistant message array"],
                ["stream", "boolean", "No", "When true, returns an SSE streaming response"],
              ],
            },
            examplesTitle: "English examples",
            examples: [{ title: "Chat completion", code: stats.chatCurl }],
          },
          {
            id: "keys-projects",
            group: "API Reference",
            badge: "REF",
            title: "Keys and Projects",
            description: "Keys always belong to projects. One person can belong to multiple projects and chooses the project when creating a key.",
            table: {
              title: "Assignment model",
              columns: ["Object", "Managed by", "Notes"],
              rows: [
                ["Project", "Administrator or team leader", "Contains members, keys, quota, and cost attribution."],
                ["Membership", "Project Owner or Maintainer", "Controls whether a user can view the project or issue keys."],
                ["API Key", "Authorized project member", "Can call only models visible to the project and backed by enabled routes."],
              ],
            },
          },
          {
            id: "troubleshooting",
            group: "API Reference",
            badge: "REF",
            title: "Troubleshooting",
            description: "Use status codes to locate API key, project membership, model routing, and quota problems.",
            table: {
              title: "Common errors",
              columns: ["Status", "Code", "Fix"],
              rows: [
                ["401", "invalid_api_key", "Check that Authorization uses a TokenHub API key."],
                ["403", "project_forbidden / model_not_allowed", "Check project membership and whether the model is open to the project."],
                ["404/503", "provider_unavailable", "Enable a route for the model or check upstream provider health."],
                ["429", "quota_exceeded", "Check project quota, concurrency limits, and provider resource limits."],
                ["500", "upstream_error", `Inspect request_id in Request Logs; current log sample is ${formatNumber(stats.requestLogCount)} records.`],
              ],
            },
          },
        ],
      },
    ],
  };
}
