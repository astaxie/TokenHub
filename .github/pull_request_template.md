> Write the PR title and every body section in English.
> PR title format: `<type>[optional scope][!]: <short summary>`, max 72 characters.
> Common types include `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `style`, and `revert`. Use a lowercase imperative summary without a trailing period.

## Summary

<!-- What problem does this PR solve, and why is the change needed? -->

## Related Issue

<!-- Link the issue or ticket when one exists. Use "N/A" otherwise. -->

## Changes

<!-- List the key changes. Keep implementation details brief. -->

-

## Type of Change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor or maintenance
- [ ] Documentation
- [ ] Deployment or configuration

## Verification

<!-- List the checks run and their results. Note any relevant checks that were skipped. -->

-

## Compatibility, Security, and Operations

<!-- Note any API, security, data, configuration, deployment, rollout, or rollback impact. Use "None" if there is none. -->

None.

## Checklist

- [ ] The PR title and body are written in English.
- [ ] Tests were added or updated for behavior changes, or the reason they are unnecessary is documented.
- [ ] No credentials, local `.env` files, databases, backups, or runtime logs are included.
- [ ] Environment variable changes are synchronized across examples, Compose, `start.sh`, and deployment documentation where applicable.
- [ ] Shared user-facing behavior is documented consistently in English, Simplified Chinese, and Japanese where applicable.
- [ ] `data/model-catalog.yaml` remains tracked and catalog changes were reviewed where applicable.
- [ ] `git diff --check` passes.
