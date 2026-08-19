<p align="center">
  <img src="frontend/public/brand/tokenhub-logo.png" alt="TokenHub" width="96" />
</p>

<h1 align="center">TokenHub</h1>

<p align="center">
  TokenHub gives enterprises a private gateway to unify AI model access and governance, making every request controllable, traceable, and attributable.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white" alt="Docker Compose" />
  <img src="https://img.shields.io/badge/OpenAI-Compatible-10A37F" alt="OpenAI Compatible" />
</p>

<p align="center">
  English | <a href="README.zh-CN.md">简体中文</a> | <a href="README.ja.md">日本語</a>
</p>

## Supported Providers

> [!TIP]
> **Codex subscription ready:** connect OpenAI Codex subscription accounts to TokenHub and serve their models through the same governed gateway as API-based providers. [Set up Codex access →](docs/codex-tokenhub-profile-quick-start.md)

TokenHub includes native adapters for Codex subscriptions, OpenAI, Azure OpenAI, Anthropic, Gemini, DeepSeek, Qwen, and local models, plus a catalog of 150+ provider templates and custom OpenAI-compatible upstreams. Popular integrations include:

<p align="center">
  <img src="docs/assets/provider-showcase.svg" alt="Popular TokenHub provider integrations, including Codex Subscription, OpenAI, Anthropic, Google Gemini, DeepSeek, Qwen, Llama, and custom upstreams." width="100%">
</p>

Provider templates use the matching native adapter when available; otherwise they connect through an OpenAI-compatible endpoint. Models and capabilities vary by upstream service and account.

## Screenshots

<p align="center">
  <img src="docs/assets/screenshots/tokenhub-tour.webp" alt="TokenHub product tour: login, overview, API documentation, provider channels, model catalog, routing policies, usage analytics, and system settings" width="100%">
</p>

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
- SQLite-first private deployment with native systemd and Docker Compose options.
- PostgreSQL supports multi-instance deployments: share state through remote PostgreSQL, scale frontend and backend replicas horizontally, and configure connection pools. See the [deployment guide](docs/deployment.md) and [PostgreSQL setup guide](docs/postgresql-setup.md).
- Console language switching for English, Chinese, and Japanese.
- TokenHub can also connect OpenAI Codex subscription resources and route selected local Codex CLI or desktop sessions through an isolated, recoverable Codex profile. See the [Codex integration guides](docs/codex-tokenhub-profile-quick-start.md).
- Gemini CLI can connect directly to TokenHub's native Gemini API and use GPT models backed by Codex subscription accounts, without CCswitch. See the [Gemini CLI guide](docs/gemini-cli-codex-subscription.md).

## Quick Start

Native Release on a Linux systemd host:

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

Docker Compose from a repository checkout:

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
- Native install password: printed once by the installer
- Docker password: the value of `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`

The native installer verifies Release checksums, installs a systemd service, and enables direct update, rollback, and restart controls in the version panel. The default Docker deployment runs the backend and console in one managed container and provides the same direct controls without mounting the Docker socket. Release bundles are stored in the `tokenhub-releases` volume so ordinary container restarts and recreations preserve a panel-applied update. Multi-instance Docker deployments keep operator-managed Compose updates so every replica changes version together. See the [deployment guide](docs/deployment.md) for both modes.

## Documentation

- [Documentation home](docs/README.md)
- [Architecture](docs/architecture.md)
- [User Guide](docs/user-guide.md)
- [Team Leader Guide](docs/team-leader-guide.md)
- [Administrator Guide](docs/administrator-guide.md)
- [Contributing Guide](CONTRIBUTING.md)

## Contributors

TokenHub grows through product feedback, gateway integrations, documentation, tests, and the steady care of people who run it in real enterprise environments.

<!-- readme: contributors -start -->

<table>
  <tr>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/astaxie">
        <img src="https://avatars.githubusercontent.com/u/233907?v=4" width="80px" alt="astaxie" />
        <br /><sub><b>astaxie</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/legendtkl">
        <img src="https://avatars.githubusercontent.com/u/2370761?v=4" width="80px" alt="legendtkl" />
        <br /><sub><b>legendtkl</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/Mr0bean">
        <img src="https://avatars.githubusercontent.com/u/19573968?v=4" width="80px" alt="Mr0bean" />
        <br /><sub><b>Mr0bean</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/deepjerry-ai">
        <img src="https://avatars.githubusercontent.com/u/262369278?v=4" width="80px" alt="deepjerry-ai" />
        <br /><sub><b>deepjerry-ai</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/cngump">
        <img src="https://avatars.githubusercontent.com/u/108251?v=4" width="80px" alt="cngump" />
        <br /><sub><b>cngump</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/bailu-ZZ">
        <img src="https://avatars.githubusercontent.com/u/311096537?v=4" width="80px" alt="bailu-ZZ" />
        <br /><sub><b>bailu-ZZ</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/coldbrewtea">
        <img src="https://avatars.githubusercontent.com/u/6879314?v=4" width="80px" alt="coldbrewtea" />
        <br /><sub><b>coldbrewtea</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/samz406">
        <img src="https://avatars.githubusercontent.com/u/3055810?v=4" width="80px" alt="samz406" />
        <br /><sub><b>samz406</b></sub>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/wangle201210">
        <img src="https://avatars.githubusercontent.com/u/19949348?v=4" width="80px" alt="wangle201210" />
        <br /><sub><b>wangle201210</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/CLukeLi">
        <img src="https://avatars.githubusercontent.com/u/252523101?v=4" width="80px" alt="CLukeLi" />
        <br /><sub><b>CLukeLi</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/imaben">
        <img src="https://avatars.githubusercontent.com/u/3390195?v=4" width="80px" alt="imaben" />
        <br /><sub><b>imaben</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/myssl">
        <img src="https://avatars.githubusercontent.com/u/27838738?v=4" width="80px" alt="myssl" />
        <br /><sub><b>myssl</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/exgliuzhi">
        <img src="https://avatars.githubusercontent.com/u/6261701?v=4" width="80px" alt="exgliuzhi" />
        <br /><sub><b>exgliuzhi</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/hoorayman">
        <img src="https://avatars.githubusercontent.com/u/73151874?v=4" width="80px" alt="hoorayman" />
        <br /><sub><b>hoorayman</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/debin-ge">
        <img src="https://avatars.githubusercontent.com/u/21329997?v=4" width="80px" alt="debin-ge" />
        <br /><sub><b>debin-ge</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/ocass-chen">
        <img src="https://avatars.githubusercontent.com/u/172055494?v=4" width="80px" alt="ocass-chen" />
        <br /><sub><b>ocass-chen</b></sub>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/AnxForever">
        <img src="https://avatars.githubusercontent.com/u/130662349?v=4" width="80px" alt="AnxForever" />
        <br /><sub><b>AnxForever</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/yujiewanwan">
        <img src="https://avatars.githubusercontent.com/u/268286250?v=4" width="80px" alt="yujiewanwan" />
        <br /><sub><b>yujiewanwan</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/lxm">
        <img src="https://avatars.githubusercontent.com/u/1918195?v=4" width="80px" alt="lxm" />
        <br /><sub><b>lxm</b></sub>
      </a>
    </td>
    <td align="center" valign="top" width="12.5%">
      <a href="https://github.com/susunola">
        <img src="https://avatars.githubusercontent.com/u/38539169?v=4" width="80px" alt="susunola" />
        <br /><sub><b>susunola</b></sub>
      </a>
    </td>
  </tr>
</table>

<!-- readme: contributors -end -->

<p align="center">
  <a href="https://github.com/astaxie/TokenHub/graphs/contributors">View all contributors</a>
  ·
  <a href="CONTRIBUTING.md">Start contributing</a>
</p>

## Star History

<a href="https://www.star-history.com/?repos=astaxie%2Ftokenhub&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&theme=dark&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
 </picture>
</a>

## License

TokenHub is licensed under the [Apache License 2.0](LICENSE).
