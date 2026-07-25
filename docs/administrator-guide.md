# Administrator Guide

Language: English | [简体中文](zh-CN/administrator-guide.md) | [日本語](ja/administrator-guide.md)

This guide is for platform administrators, security operators, and infrastructure owners who run TokenHub as an enterprise AI gateway.

## Administrator Scope

| Area | Responsibility |
| --- | --- |
| Provider Channels | Configure upstream Base URLs, credentials, resources, and health checks |
| Model Catalog | Maintain standard model names, capabilities, context windows, and pricing units |
| Routing Policies | Map standard models to provider models with priority, weight, and failover strategy |
| Projects and Teams | Define ownership boundaries for keys, quota, and cost attribution |
| Identity Sources | Configure OAuth or OIDC login providers for enterprise sign-in |
| Security and Audit | Review request logs, admin events, key rotation, and policy changes |

## Production Setup Order

1. Configure at least one identity source and keep a controlled administrator account.
2. Add upstream providers such as `OpenAI Production`, `Azure East US`, or `Internal Model Gateway`.
3. Import or maintain the model catalog using English model names such as `gpt-4.1-mini`.
4. Create enabled routing rules for every model that should be callable.
5. Create teams, projects, cost centers, and default quota policies.
6. Validate the flow with Model Playground and request logs.
7. Review usage attribution before issuing keys broadly.

## Provider Catalog Availability

TokenHub stores the last known-good public provider catalog in the database. A new installation seeds a built-in catalog and attempts to download the complete public catalog before the backend starts accepting requests. If that initialization download fails, the backend starts with the built-in snapshot and retries on the next startup. Public snapshots older than 24 hours refresh in the background. Ordinary **Provider Channels** requests only read the database, and a refresh atomically replaces the snapshot only after the download passes completeness validation. If GitHub is slow or unavailable, administrators continue using the stored snapshot.

## Routing Requirements

Users should only see callable models. A model is callable when it is active in the catalog and has at least one enabled routing rule.

| State | Admin UI behavior |
| --- | --- |
| Active model with enabled route | Normal model card |
| Active model without route | Visually distinct background so admins can spot missing configuration |
| Disabled model | Hidden from ordinary users |
| Unhealthy provider route | Visible in routing diagnostics and request logs |

## Prompt Cache Pricing

The model catalog accepts an optional cache read price in USD per 1 million tokens. When it is configured, cached input tokens use that price in estimated costs. When it is left blank, TokenHub estimates the cache read price at about 0.83% of the standard input price for DeepSeek V4 Pro, 2% for other DeepSeek models, and 10% for other non-embedding models. The model pricing table marks estimated values and explains the applied ratio on hover.

## Catalog Recovery

Deleting a model removes the database record and its routes, but it does not edit `data/model-catalog.yaml` or the file configured by `TOKENHUB_MODEL_CATALOG_FILE`. Backend startup syncs that configured catalog again. Administrators can also use **Restore Factory Catalog** in the Model Catalog page to re-import and overwrite standard models from the configured catalog while keeping manually added models.

## Security Checklist

| Control | Requirement |
| --- | --- |
| API keys | Show the full secret once, then store only prefix and suffix |
| OAuth redirect URI | Register local and production callback URLs with the identity provider |
| RBAC | Separate user, team leader, administrator, finance, security, and operator scopes |
| Audit retention | Keep request logs and admin events long enough for compliance review |
| Cost controls | Attribute every request to user, project, team, and cost center when possible |

## Chinese Enterprise Identity Sources

In **Identity Sources**, select a built-in DingTalk, Feishu, or WeCom template. The template fills the public endpoints and claim mappings; only override the advanced endpoints when traffic must pass through an enterprise proxy or a compatible private deployment.

Creating an identity source uses three required steps: choose the source, enter its connection settings, and configure the login entry plus first-login grants. The connection step links to the selected provider's official setup guide so you can create the application and obtain its credentials. Generic OIDC and OAuth2 templates instead tell you to consult the actual provider's application-registration guide and link to the relevant protocol reference. From the third step, templates with complete endpoint defaults can use **Skip and Finish**; otherwise the advanced endpoint fields become required. You can also open advanced settings to override endpoint, scope, and claim defaults. Editing an existing source keeps the complete form available on one screen.

Use the public TokenHub backend URL with the callback path `/api/admin/auth/oauth/callback`. You may leave Callback URL blank to derive it from the incoming backend host; when setting it explicitly, the complete URL must exactly match the redirect URL registered with the identity provider.

| Provider | Required application configuration | TokenHub behavior |
| --- | --- | --- |
| DingTalk | Create a web application, enable user authorization, register the callback URL, and copy its App Key and App Secret | Uses the DingTalk v1.0 JSON token API and user access-token header. If the authorized profile has no email, TokenHub derives a stable internal email from `unionId`. |
| Feishu | Create an enterprise self-built application, enable web authorization, register the callback URL, and copy its App ID and App Secret. Grant profile and enterprise-email access when available. | Uses the Feishu OAuth v2 token API and unwraps the `data` user-info response. If email is unavailable, TokenHub derives a stable internal email from `union_id`. |
| WeCom | Create a custom application and configure its trusted web authorization domain. Copy the Corp ID, application Secret, and Agent ID, and grant the application permission to read the required directory members. | Uses WeCom CorpApp login, exchanges the application token, resolves the callback code to `UserId`, and then reads the member profile. `biz_mail` is preferred; a stable internal email is derived from `userid` when needed. |

The derived addresses end in `<provider>.tokenhub.local`. They are internal account identifiers, not deliverable mailboxes. Keep a controlled password administrator until the new login has been tested end to end.

## Screenshot

![Routing policies](assets/screenshots/routes-en.png)
