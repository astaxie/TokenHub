# Team LLM API Rollout Guide

Language: English | [简体中文](zh-CN/team-leader-guide.md) | [日本語](ja/team-leader-guide.md)

This guide is for team leaders who help applications call approved large language models through project-scoped TokenHub API keys.

## Team Leader Responsibility

| Area | What to manage |
| --- | --- |
| Project | Own the boundary for members, keys, quota, and cost attribution |
| Members | Add the application owner or developer to the project member panel |
| API Keys | Issue keys under the project that should own usage and cost |
| Models | Verify the key can see the intended model list |
| Reports | Review usage by member, project, model, and cost center |

## Collaborate Across Teams

A project can link multiple teams. The project's **primary team** remains the single accountable team for default ownership, cost attribution, and approval responsibility. The project owner also remains a single user. Linking another team grants access; it does not copy the project's API keys, model permissions, quota, budget, or routing policy.

Each linked team has one project role:

| Team project role | Effective permission |
| --- | --- |
| `viewer` | View the project and its permitted reports |
| `developer` | Viewer access plus issuing a project key owned by the current user |
| `maintainer` | Developer access plus project, member, and key management allowed by the user's console role |

Direct project membership and every linked-team role are merged deterministically: `owner` > `maintainer` > `developer` > `viewer`. A user who belongs to several linked teams receives the highest role. Administrators manage a user's primary and additional memberships in **User Management**. Existing single-team projects migrate in compatibility mode, which preserves access for their team leader without granting new access to ordinary same-team users; an administrator can replace that compatibility role in Project Details.

In **Project Spaces**, select a project and use **Linked Teams** to add a team, change its role, or remove it. Access changes take effect on the next request. Only active teams can be assigned to users or newly linked to projects; disabling a linked team immediately stops its project role from granting access. The primary team cannot be removed until another primary team is assigned, the last linked team cannot be removed, and a team referenced by a project or user cannot be deleted.

The management API exposes the same operations:

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/api/admin/projects/{project_id}/teams?limit=50&offset=0` | List linked teams with pagination |
| `POST` | `/api/admin/projects/{project_id}/teams` | Link `{ "team_id": "...", "role": "viewer|developer|maintainer" }` |
| `PATCH` | `/api/admin/projects/{project_id}/teams/{team_id}` | Change the linked team's role |
| `DELETE` | `/api/admin/projects/{project_id}/teams/{team_id}` | Remove a non-primary, non-last team link |

## Roll Out a Project Key

1. Create or select a project in **Project Spaces**.
2. Click the project and add the application owner in the right-side member panel.
3. Open **Key Management** and create the key under that project.
4. Limit the key to the models and quota needed by the application.
5. Validate the key with `GET /v1/models`.
6. Hand the key to the application owner through your internal secret process.

## Validate Available Models

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models" \
  --header "Authorization: Bearer PROJECT_API_KEY" \
  --header "Content-Type: application/json"
```

The returned `data[].id` values are the model IDs the application can use.

## Validate Chat Calls

```bash
curl --request POST \
  --url "http://localhost:8080/v1/chat/completions" \
  --header "Authorization: Bearer PROJECT_API_KEY" \
  --header "Content-Type: application/json" \
  --data '{
    "model": "gpt-4.1-mini",
    "messages": [
      {"role": "user", "content": "Write a concise project onboarding checklist."}
    ],
    "stream": false
  }'
```

## Governance Checks

| Check | Why it matters |
| --- | --- |
| Project owner | Usage and cost need a clear owner |
| Member role | Only trusted project members should issue or rotate keys |
| Model scope | The key should expose only the models the application needs |
| Quota | Quota and concurrency should match expected traffic |
| Logs | Failed calls should be traceable by `request_id` |

## Common Errors

| Status | Team leader action |
| --- | --- |
| 401 | Confirm the application uses the active project key |
| 403 | Check project membership and allowed model scope |
| 429 | Review quota, concurrency, and key/project limits |
| 503 | Ask an administrator to check route and provider health |
| 500 | Use `request_id` in Request Logs to inspect the upstream error |

## Screenshot

![Gateway documentation](assets/screenshots/gateway-en.png)
