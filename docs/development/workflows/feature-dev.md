# Feature Dev Workflow

Use `feature-dev` only when the user explicitly selects it. It covers important features, user-visible or cross-component changes, public API or data-model changes, security-sensitive work, deployment changes, broad refactors, and architectural decisions.

Read this file and `AGENTS.md` before editing. Do not load `fast-dev` at the same time.

## 1. Understand and confirm

1. Inspect `git status` and preserve unrelated or user-authored changes.
2. Read the affected code, tests, contracts, and documentation.
3. Define goals, non-goals, compatibility, rollout, rollback, security impact, and open assumptions.
4. Before implementation, present a high-level design and normal, boundary, and failure test cases in the task context. Wait for confirmation; return here if scope or key tradeoffs change.

## 2. Implement

- Prefer test-backed, independently verifiable steps.
- Preserve `/v1` compatibility unless the confirmed design changes it.
- Treat credentials, keys, authentication, authorization, quotas, routing, audit data, persistence, and distributed coordination as sensitive.
- Keep backend, frontend, SDK, model catalog, environment examples, deployment files, and translated documentation consistent where affected.

## 3. Validate

Run focused tests while iterating, then all applicable checks from `AGENTS.md`:

- backend formatting, `go test ./...`, and `go vet ./...`;
- frontend install, typecheck, build, and browser regression for visible UI;
- SDK smoke tests for API changes when a configured backend is available;
- deployment tests and rendered Compose configuration for deployment changes; and
- `git diff --check` plus link and path verification for documentation.

Record blocked or skipped checks and remaining risk. Never report an unrun or failing check as passing.

## 4. Review and handoff

After validation, spawn a fresh review subagent that did not participate in implementation. Give it the final diff and validation results, and require it to remain read-only. Fix accepted findings, rerun affected checks, and use at most three rounds. If review subagents are unavailable, report that limitation instead of substituting self-review.

Report the result, checks, review outcome, unresolved findings, and remaining risk. Commit, push, or create a pull request only when separately requested; then follow `.github/pull_request_template.md`.
