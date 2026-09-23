# Contributing to TokenHub

English | [简体中文](CONTRIBUTING.zh-CN.md) | [日本語](CONTRIBUTING.ja.md)

TokenHub includes a Go backend, a Next.js admin console, Node.js SDK smoke tests, a YAML model catalog, and Docker Compose deployment files. This guide covers local development, validation, optional AI agent workflows, and pull request preparation.

## Repository Layout

| Path | Purpose |
| --- | --- |
| `backend/` | Go HTTP API, persistence, routing, authentication, administration, and backend tests |
| `frontend/` | Next.js and React admin console |
| `sdk/` | Node.js smoke tests for the OpenAI-compatible API and security policy endpoints |
| `data/model-catalog.yaml` | Tracked model catalog source |
| `deploy/` | Docker Compose deployment and environment templates |
| `docs/` | English, Simplified Chinese, and Japanese documentation |

## Local Development

Local development requires Go 1.26 and Node.js 20 or later. Docker and Docker Compose are required only for container and deployment checks.

Start the full local development stack from the repository root:

```bash
./start.sh
```

To run components separately, start the backend from `backend/`:

```bash
TOKENHUB_CORS_ALLOWED_ORIGINS=http://localhost:3000 go run ./cmd/tokenhub
```

Start the admin console from `frontend/`:

```bash
npm ci
npm run dev
```

Run the SDK smoke tests from `sdk/` only when a compatible backend is available and the required environment variables are configured:

```bash
npm ci
npm run test:deepseek
npm run test:anthropic-messages
npm run test:security-policy
```

## Change Guidelines

- Keep changes focused and preserve unrelated work.
- Add or update tests for backend behavior changes. Prefer in-process fake HTTP or SMTP servers over external network dependencies.
- Preserve compatibility for the OpenAI-compatible `/v1` endpoints unless the change explicitly updates the contract.
- Never commit credentials, local `.env` files, databases, generated backups, or runtime logs.
- Keep environment variable changes synchronized across relevant examples, Compose files, `start.sh`, and deployment documentation.
- Keep shared user-facing documentation synchronized across English, Simplified Chinese, and Japanese.
- Keep `data/model-catalog.yaml` tracked. Do not commit other runtime data files.

## Validation

Run the narrowest relevant test while iterating, followed by the full applicable checks before opening a pull request.

Backend checks from `backend/`:

```bash
gofmt -w path/to/changed.go
go test ./...
go vet ./...
```

Frontend checks from `frontend/`:

```bash
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

The browser smoke suite starts an isolated Next.js frontend, Go backend, fake provider upstream, and temporary SQLite database. It does not require real credentials or an already-running TokenHub stack.

For Docker or deployment changes, render the Compose configuration when Docker is available:

```bash
docker compose --env-file deploy/.env.example \
  -f deploy/docker-compose.yml config
```

Run `git diff --check` for every change. Report any check that could not run and distinguish new failures from failures already present on the base branch.

## Optional AI Agent Development Workflows

TokenHub provides two opt-in workflows for AI-assisted changes:

| Workflow | Choose it for |
| --- | --- |
| [`fast-dev`](docs/development/workflows/fast-dev.md) | Small, well-scoped, low-risk changes that do not alter public APIs, persistence, authentication or authorization, deployment, or cross-component behavior |
| [`feature-dev`](docs/development/workflows/feature-dev.md) | Important features, user-visible or cross-component changes, public API or data-model changes, security-sensitive work, deployment changes, broad refactors, or architectural decisions |

Activate one by naming it in the request, such as `Use fast-dev for this change.` Without an explicit selection, the agent follows the normal repository guidance. If `fast-dev` no longer fits, the agent asks before switching to `feature-dev`. Workflow selection does not authorize commits, pushes, pull requests, merges, or other external writes.

See [AGENTS.md](AGENTS.md#optional-development-workflows) for agent-specific repository instructions.

## Pull Requests

- Write the title and every body section in English.
- Use an English Conventional Commits-style title: `<type>[optional scope][!]: <short summary>`.
- Keep the title at or below 72 characters, use a lowercase imperative summary, and omit the trailing period.
- Complete every section of [the pull request template](.github/pull_request_template.md). Explain skipped or non-applicable checks.
- Describe API compatibility, security, database, environment, deployment, rollout, and rollback effects where applicable.
- Create a ready-for-review pull request by default. Use a draft only when explicitly requested.
