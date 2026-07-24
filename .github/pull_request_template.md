> PR title format: `<type>[optional scope][!]: <short summary>` in English, max 72 characters.
> Common types include `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `style`, and `revert`. Use a lowercase imperative summary without a trailing period.

## Summary

<!-- What problem does this PR solve, and why is the change needed? -->

## Related Issue

<!-- Link the issue or ticket when one exists. Use "N/A" otherwise. -->

## Changes

<!-- Describe the main changes. Group related backend, frontend, SDK, deployment, catalog, and documentation updates. -->

-

## Type of Change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor or maintenance
- [ ] Documentation
- [ ] Deployment or configuration

## Verification

<!-- List the exact commands or manual checks you ran and their results. Explain why any relevant check was skipped. -->

- [ ] Backend: `gofmt` on changed Go files, `go test ./...`, and `go vet ./...`
- [ ] Frontend: `npm run typecheck` and `npm run build`
- [ ] SDK smoke tests against a compatible backend
- [ ] Docker Compose configuration rendered successfully
- [ ] Other focused or manual verification described below

Verification details:

-

## Compatibility, Security, and Operations

<!-- Call out API contract changes, migrations, rollout/rollback needs, and security-sensitive behavior. Use "None" where applicable. -->

- OpenAI-compatible `/v1` API impact:
- Security or credential-handling impact:
- Database, environment, or deployment impact:
- Rollout and rollback considerations:

## Checklist

- [ ] Tests were added or updated for behavior changes, or the reason they are unnecessary is documented.
- [ ] No credentials, local `.env` files, databases, backups, or runtime logs are included.
- [ ] Environment variable changes are synchronized across examples, Compose, `start.sh`, and deployment documentation where applicable.
- [ ] Shared user-facing behavior is documented consistently in English, Simplified Chinese, and Japanese where applicable.
- [ ] `data/model-catalog.yaml` remains tracked and catalog changes were reviewed where applicable.
- [ ] `git diff --check` passes.
