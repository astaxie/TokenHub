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

## Security Checklist

| Control | Requirement |
| --- | --- |
| API keys | Show the full secret once, then store only prefix and suffix |
| OAuth redirect URI | Register local and production callback URLs with the identity provider |
| RBAC | Separate user, team leader, administrator, finance, security, and operator scopes |
| Audit retention | Keep request logs and admin events long enough for compliance review |
| Cost controls | Attribute every request to user, project, team, and cost center when possible |

## Screenshot

![Routing policies](assets/screenshots/routes-en.png)
