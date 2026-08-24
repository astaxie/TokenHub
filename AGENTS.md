# TokenHub Agent Guide

## Repository overview

TokenHub is a private enterprise AI gateway with a Go backend, a Next.js admin console, SDK smoke tests, a YAML model catalog, and Docker Compose deployment files.

- `backend/`: Go HTTP API, SQLite/GORM persistence, routing, authentication, administration, and backend tests.
- `frontend/`: Next.js and React admin console.
- `sdk/`: Node.js smoke tests for the OpenAI-compatible API and security policy endpoints.
- `data/model-catalog.yaml`: tracked model catalog source.
- `deploy/`: Docker Compose deployment and environment template.
- `docs/`: English, Simplified Chinese, and Japanese documentation.

## Repository source language

- Use English for source-code identifiers, code comments, test suite and test case titles, developer-facing fixtures, logs, and configuration descriptions.
- Localized UI copy is the exception: keep Simplified Chinese as the canonical literal passed to `tx("...")`, with matching English and Japanese catalog entries.
- Tests may use localized selectors or assertions when they intentionally exercise translated UI, but their suite names, case titles, and diagnostics must remain in English.

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
npm run lint
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

The browser smoke suite starts an isolated Next.js frontend, Go backend, fake
provider upstream, and temporary SQLite database. It does not require real
credentials or an already-running TokenHub stack.

Run the repository gates from the repository root. These enforce the change guidelines below, so a failure names the rule that was broken:

```bash
node --test tools/*.test.mjs
node tools/check-doc-translations.mjs
node tools/check-ui-translations.mjs
node tools/check-env-contract.mjs
node tools/check-source-lines.mjs
```

The translation gate compares the pull request base against its head. Outside CI there is usually no base to compare against, so it runs the existence half and reports that the rest was skipped; pass `--base <sha> --head <sha>` to run it in full.

Run SDK smoke tests from `sdk/` only when a compatible backend is available and the required environment variables are configured:

```bash
npm ci
npm run test:deepseek
npm run test:anthropic-messages
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
- Treat Simplified Chinese as the canonical UI copy passed to `tx("...")`; every literal UI key must have both English and Japanese entries. Keep protocol names, identifiers, units, and user/provider content untranslated when they are data rather than interface copy.
- Do not hard-code user-visible prose in React views. Add it to the translation catalog in the same change; `node --test tools/ui-translations.test.mjs` enforces literal `tx("...")` coverage.
- Build dynamic sentences with locale-aware helpers or complete translated templates rather than concatenating translated fragments. Format user-visible dates, times, numbers, and currencies with `languageLocale()` and `Intl` so they follow the selected application language.
- A few source files are over the line-count ceiling and are frozen at their current size in the `FROZEN` table in `tools/source-lines.mjs`. Avoid broad formatting or unrelated restructuring in them when making a targeted fix; `node tools/check-source-lines.mjs` reports which files are affected.
- Next.js may rewrite `frontend/next-env.d.ts` during development or production builds. Do not commit incidental mode-dependent changes to that generated file.
- Keep `data/model-catalog.yaml` tracked; other files under runtime data directories are intentionally ignored.

## GitHub contribution language

- Agents must write every GitHub issue and pull request title and body in English, regardless of whether they use the GitHub website, an API, a connector, or `gh`.
- Keep unavoidable machine-generated output, logs, identifiers, and code in their original form, but explain them in English.

## GitHub issue guidelines

- Search open and closed issues before creating a new issue.
- Read and follow the closest matching form under `.github/ISSUE_TEMPLATE/`, including every required field. Do not create a blank issue when an existing form applies.
- When a CLI or API cannot submit an Issue Form directly, preserve the form's headings and required information in the issue body.
- Never include API keys, provider credentials, tokens, cookies, unredacted environment files, or other secrets in an issue.

## Pull request guidelines

- Use an English Conventional Commits-style PR title in the format `<type>[optional scope][!]: <short summary>`, limited to 72 characters. Common types include `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `style`, and `revert`. Use a lowercase imperative summary without a trailing period.
- Write every pull request body section in English.
- Before creating a pull request, read and complete `.github/pull_request_template.md`.
- Preserve every template section, replace all placeholders, and explain any skipped or non-applicable checks.
- Do not use `gh pr create --fill` or an ad hoc body that bypasses the template; use the completed template as the final pull request body.
- Create a ready-for-review pull request by default. Use a draft only when explicitly requested.

## Validation expectations

- Run the narrowest relevant test while iterating, then run the full applicable check set before handing off.
- Run `git diff --check` before committing.
- Report any check that could not run and distinguish new failures from failures already present on the base branch.
- For Docker or deployment changes, validate the rendered Compose configuration with `docker compose --env-file deploy/.env.example -f deploy/docker-compose.yml config` when Docker is available.
