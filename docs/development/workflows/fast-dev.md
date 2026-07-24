# Fast Dev Workflow

Use `fast-dev` only when the user explicitly selects it. It is intended for small, well-scoped, low-risk changes.

Read this file and `AGENTS.md` before editing. Do not load `feature-dev` at the same time.

## Scope

The change must preserve public APIs, persistence, authentication and authorization, security boundaries, deployment behavior, and backend/frontend contracts. It must not introduce a broad refactor, architectural decision, or new user-visible capability.

If the change exceeds this boundary, stop and ask whether to switch to `feature-dev`; never switch automatically.

## Steps

1. Inspect `git status`, preserve unrelated changes, and read the target code, adjacent tests, and relevant documentation.
2. State the intended scope in one sentence, then make the smallest necessary change. Add a focused regression test when behavior changes.
3. Run the applicable checks from `AGENTS.md`:
   - documentation: `git diff --check` and link, command, and path verification;
   - backend: formatting, focused tests, `go test ./...`, and `go vet ./...`;
   - frontend: `npm ci`, `npm run typecheck`, and `npm run build`;
   - visible UI: browser verification of affected states and viewports;
   - deployment or SDK: the relevant repository checks when their required environment is available.
4. After validation, spawn a fresh review subagent that did not participate in implementation. Give it the final diff and validation results, and require it to remain read-only. Fix accepted findings and rerun affected checks; use at most two rounds. If review subagents are unavailable, report that limitation instead of substituting self-review.
5. Report changes, checks, skipped validation, review results, and remaining risk.

Do not commit runtime artifacts or incidental generated files. Commit, push, or create a pull request only when separately requested.
