# TokenHub Documentation

Language: English | [简体中文](zh-CN/README.md) | [日本語](ja/README.md)

TokenHub documentation is now organized around the three roles used in an enterprise AI gateway. The default documentation language is English. Localized Chinese and Japanese versions use the same English screenshots and the same English sample data set.

## Architecture and Deployment

- [Architecture](architecture.md): deployment modes, control and data planes, model request flow, persistence, and security boundaries.
- [Deployment](deployment.md): Docker Compose, environment variables, databases, reverse proxying, and health checks.
- [PostgreSQL Setup Guide](postgresql-setup.md): PostgreSQL configuration, operations, and migration.
- [Performance Benchmarking](performance-benchmarking.md): reproducible gateway comparisons, internal allocation benchmarks, and regression budgets.

## Role Guides

| Guide | Audience | Primary workflow |
| --- | --- | --- |
| [User Guide](user-guide.md) | Employees and application developers | Find available models, create project keys, call the model API, and review personal usage |
| [Team Leader Guide](team-leader-guide.md) | Team owners and project maintainers | Manage project spaces, members, API keys, team reports, and project cost attribution |
| [Administrator Guide](administrator-guide.md) | Platform administrators and security operators | Configure providers, model catalog, routing, identity sources, RBAC, audit, and cost controls |
| [Agent Token Cost API](agent-token-cost-api.md) | Local reporting agents and platform administrators | Create least-privilege analytics credentials and pull filtered, aggregated, incremental token costs as JSON or CSV |
| [A2A 1.0 Agent Gateway](a2a-agent-gateway.md) | Platform administrators and Agent developers | Register reviewed Agents, configure least-privilege access, invoke A2A tasks, and enforce runtime budgets |
| [Image Generation Guide](user-guide.md#codex-subscription-image-generation) | Application developers using image models | Generate and edit images, run asynchronous jobs, and distinguish Codex subscription capacity from OpenAI API usage |
| [Connect Codex to TokenHub: Profile Quick Setup](codex-tokenhub-profile-quick-start.md) | Codex users who only need an isolated profile | Create the profile, set the key, validate the connection, and recover |
| [Connect Codex to TokenHub: Four Configuration Methods and Recovery](codex-tokenhub-configuration.md) | Codex users and developers comparing integration methods | Configure TokenHub through a profile, process-local override, global CLI settings, or the desktop app |

## Shared English Sample Data

The examples and screenshots use one English data set so every language version stays visually consistent:

| Object | Example |
| --- | --- |
| Organization | Acme AI Platform |
| Team | Platform Engineering |
| Project | Payments Assistant |
| Cost center | AI Platform Cost Center |
| Model | gpt-4.1-mini |
| API key placeholder | YOUR_TOKENHUB_API_KEY |

## Screenshot Set

Product screenshots are captured from the English UI. The Codex guides use the same real, redacted terminal screenshots in every language version.

| Screen | File |
| --- | --- |
| Gateway documentation | `assets/screenshots/gateway-en.png` |
| Overview | `assets/screenshots/overview-en.png` |
| Models | `assets/screenshots/models-en.png` |
| Routes | `assets/screenshots/routes-en.png` |
| Usage | `assets/screenshots/usage-en.png` |
| Settings | `assets/screenshots/settings-en.png` |
| Codex Profile configuration | `assets/codex-profile/tokenhub-profile-config-redacted.png` |
| Codex Profile status | `assets/codex-profile/codex-status-redacted.png` |

## Recommended Reading Order

1. Start with the guide that matches your role.
2. Read the “Concepts” section in the in-app API Documentation page if you need shared vocabulary.
3. Use the API examples in the User Guide for application integration.
4. Review the Administrator Guide before production rollout.
