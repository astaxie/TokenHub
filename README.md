<p align="center">
  <img src="frontend/public/brand/tokenhub-logo.png" alt="TokenHub" width="96" />
</p>

<h1 align="center">TokenHub</h1>

<p align="center">
  TokenHub is a private enterprise AI gateway with role-based workspaces for users, team leaders, and administrators.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/Next.js-16.2.9-black?logo=nextdotjs" alt="Next.js 16.2.9" />
  <img src="https://img.shields.io/badge/React-19.2.7-61DAFB?logo=react&logoColor=111111" alt="React 19.2.7" />
  <img src="https://img.shields.io/badge/SQLite-first-003B57?logo=sqlite&logoColor=white" alt="SQLite first" />
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white" alt="Docker Compose" />
  <img src="https://img.shields.io/badge/OpenAI-Compatible-10A37F" alt="OpenAI Compatible" />
  <img src="https://img.shields.io/badge/i18n-ZH%20%7C%20EN%20%7C%20JA-6f42c1" alt="i18n ZH EN JA" />
</p>

<p align="center">
  English | <a href="README.zh-CN.md">简体中文</a> | <a href="README.ja.md">日本語</a>
</p>

## Screenshots

| Login Console | Gateway Overview |
| --- | --- |
| ![Login Console](docs/assets/screenshots/login-en.png) | ![Gateway Overview](docs/assets/screenshots/overview-en.png) |
| API Documentation | Provider Channels |
| ![API Documentation](docs/assets/screenshots/gateway-en.png) | ![Provider Channels](docs/assets/screenshots/providers-en.png) |
| Model Catalog | Routing Policies |
| ![Model Catalog](docs/assets/screenshots/models-en.png) | ![Routing Policies](docs/assets/screenshots/routes-en.png) |
| Usage Analytics | System Settings |
| ![Usage Analytics](docs/assets/screenshots/usage-en.png) | ![System Settings](docs/assets/screenshots/settings-en.png) |

## Designed Around Three Roles

TokenHub separates everyday model usage, team governance, and platform administration so enterprise users see the workflows that match their responsibility.

| Role | Workspace Focus | Guide |
| --- | --- | --- |
| User | Find available models, create project-scoped API keys, call the model API, and review personal usage | [User Guide](docs/user-guide.md) |
| Team Leader | Manage project spaces, project members, project keys, team reports, and project cost attribution | [Team Leader Guide](docs/team-leader-guide.md) |
| Administrator | Configure providers, model catalog, routing policies, identity sources, RBAC, audit, and cost controls | [Administrator Guide](docs/administrator-guide.md) |

## Platform Capabilities

- OpenAI-compatible model APIs: `/v1/chat/completions`, `/v1/responses`, `/v1/embeddings`; Anthropic Messages APIs: `/v1/messages`, `/v1/messages/count_tokens`.
- OpenAI-compatible image generation and reference-image editing through `/v1/images/generations` and `/v1/images/edits`, with asynchronous jobs and server-side image retention; `codex-gpt-image-2` uses Codex subscription capacity, while `gpt-image-2` uses OpenAI API providers. See the [image generation guide](docs/user-guide.md#codex-subscription-image-generation).
- Provider channels for OpenAI-compatible, Azure OpenAI, Anthropic, Gemini, DeepSeek, Qwen, local vLLM/Ollama, and custom upstreams.
- Model catalog and routing policies with priority, weight, failover order, and route health diagnostics.
- Project-scoped key management with team ownership, member permissions, quotas, and concurrency controls.
- Usage analytics and request logs attributed to user, project, team, model, and cost center.
- Identity source configuration for OAuth/OIDC enterprise sign-in, plus RBAC and audit trails.
- Clean console with compact role-aware navigation, global search, light/dark mode, and split-view API documentation.
- SQLite-first private deployment with Docker Compose support.
- PostgreSQL supports multi-instance deployments: share state through remote PostgreSQL, scale frontend and backend replicas horizontally, and configure connection pools. See the [deployment guide](docs/deployment.md).
- Console language switching for English, Chinese, and Japanese.
- TokenHub can also connect OpenAI Codex subscription resources and route selected local Codex CLI or desktop sessions through an isolated, recoverable Codex profile. See the [Codex integration guides](docs/codex-tokenhub-profile-quick-start.md).

## Quick Start

```bash
cp deploy/.env.example deploy/.env
# Replace every change-me value in deploy/.env with a strong secret.
./deploy/install.sh
```

Open:

- Admin console: `http://localhost:3000`
- Backend API: `http://localhost:8080`
- Health check: `http://localhost:8080/healthz`

Initial admin login:

- Username: `admin`
- Password: the value of `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`

The deployment script validates production credentials, pulls published images, and starts the containers without building locally. Until the images are publicly available, a failed pull of the default `latest` tag automatically falls back to a local source build; an explicitly selected tag never does. The script reports each unsafe variable without printing secret values. If Compose fails because a backend container created or restarted by that attempt is unhealthy, it automatically shows only that attempt's recent backend logs. Use `./deploy/install.sh --build` to request a local build explicitly.

## Documentation

- [Documentation home](docs/README.md)
- [Architecture](docs/architecture.md)
- [User Guide](docs/user-guide.md)
- [Team Leader Guide](docs/team-leader-guide.md)
- [Administrator Guide](docs/administrator-guide.md)
- [Contributing Guide](CONTRIBUTING.md)
- [简体中文文档](docs/zh-CN/README.md)
- [日本語ドキュメント](docs/ja/README.md)

## Contributors

TokenHub grows through product feedback, gateway integrations, documentation, tests, and the steady care of people who run it in real enterprise environments.

<p align="center">
  <a href="https://github.com/astaxie/TokenHub/graphs/contributors">
    <img src="https://contrib.rocks/image?repo=astaxie/TokenHub" alt="TokenHub contributors" />
  </a>
</p>

<p align="center">
  <a href="https://github.com/astaxie/TokenHub/graphs/contributors">View all contributors</a>
  ·
  <a href="CONTRIBUTING.md">Start contributing</a>
</p>

## Star History

[![GitHub Repo stars](https://img.shields.io/github/stars/astaxie/TokenHub?style=social)](https://github.com/astaxie/TokenHub/stargazers)

Track TokenHub's growth on the [live Star History chart](https://star-history.com/#astaxie/TokenHub&Date).

## License

TokenHub is licensed under the [Apache License 2.0](LICENSE).
