# TokenHub Agent Guide

## Repository overview

TokenHub is a private enterprise AI gateway with a Go backend, a Next.js admin console, SDK smoke tests, a YAML model catalog, and Docker Compose deployment files.

- `backend/`: Go HTTP API, SQLite/GORM persistence, routing, authentication, administration, and backend tests.
- `frontend/`: Next.js and React admin console.
- `sdk/`: Node.js smoke tests for the OpenAI-compatible API and security policy endpoints.
- `data/model-catalog.yaml`: tracked model catalog source.
- `deploy/`: Docker Compose deployment and environment template.
- `docs/`: English, Simplified Chinese, and Japanese documentation.

## Development commands

Run backend checks from `backend/`:

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

Run frontend checks from `frontend/`:

```bash
npm ci
npm run typecheck
npm run build
```

Run SDK smoke tests from `sdk/` only when a compatible backend is available and the required environment variables are configured:

```bash
npm ci
npm run test:deepseek
npm run test:security-policy
```

Start the full local development stack from the repository root with `./start.sh`. Start the containerized stack with the commands documented in `docs/deployment.md`.

## Optional development workflows

TokenHub provides two optional AI-agent workflows:

| Workflow | Intended use | Instructions |
| --- | --- | --- |
| `fast-dev` | Small, well-scoped, low-risk changes that do not alter public APIs, persistence, authentication or authorization, deployment, or cross-component behavior | [docs/development/workflows/fast-dev.md](docs/development/workflows/fast-dev.md) |
| `feature-dev` | Important features, user-visible behavior, cross-component changes, public API or data-model changes, security-sensitive work, deployment changes, broad refactors, or work that needs an architectural decision | [docs/development/workflows/feature-dev.md](docs/development/workflows/feature-dev.md) |

Use a workflow only when the user explicitly names it; otherwise follow the normal repository guidance. Read only the selected workflow before editing. If `fast-dev` no longer fits, ask before switching to `feature-dev`. Workflow selection never authorizes commits, pushes, pull requests, merges, or other external writes.

## Change guidelines

- Keep changes focused and preserve unrelated work in the checkout.
- Add or update tests for backend behavior changes. Prefer in-process fake HTTP or SMTP servers over external network dependencies.
- Preserve API compatibility for the OpenAI-compatible `/v1` endpoints unless the task explicitly changes the contract.
- Treat authentication, API keys, provider credentials, reset tokens, audit payloads, forwarded headers, and exported data as security-sensitive.
- Never commit real credentials, local `.env` files, SQLite databases, generated backups, or runtime logs.
- Keep environment variable additions synchronized across relevant `.env.example` files, `deploy/docker-compose.yml`, `start.sh`, and deployment documentation.
- Keep user-facing documentation synchronized across English, Simplified Chinese, and Japanese when changing shared behavior.
- `frontend/app/page.tsx` and `frontend/app/globals.css` are intentionally large. Avoid broad formatting or unrelated restructuring when making a targeted UI fix.
- Next.js may rewrite `frontend/next-env.d.ts` during development or production builds. Do not commit incidental mode-dependent changes to that generated file.
- Keep `data/model-catalog.yaml` tracked; other files under runtime data directories are intentionally ignored.

## Pull request guidelines

- Use an English Conventional Commits-style PR title in the format `<type>[optional scope][!]: <short summary>`, limited to 72 characters. Common types include `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `style`, and `revert`. Use a lowercase imperative summary without a trailing period.
- Before creating a pull request, read and complete `.github/pull_request_template.md`.
- Preserve every template section, replace all placeholders, and explain any skipped or non-applicable checks.
- Do not use `gh pr create --fill` or an ad hoc body that bypasses the template; use the completed template as the final pull request body.
- Create a ready-for-review pull request by default. Use a draft only when explicitly requested.

## Validation expectations

- Run the narrowest relevant test while iterating, then run the full applicable check set before handing off.
- Run `git diff --check` before committing.
- Report any check that could not run and distinguish new failures from failures already present on the base branch.
- For Docker or deployment changes, validate the rendered Compose configuration with `docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml config` when Docker is available.
