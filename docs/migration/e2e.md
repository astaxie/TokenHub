# End-to-End Migration Tests

## Overview

The E2E harness starts real LiteLLM and TokenHub services and validates a
remote Admin API migration cycle against a mocked OpenAI-compatible upstream.
Mailpit provides local SMTP delivery for the user-import password-reset flow.

## Prerequisites

- Docker and Docker Compose
- Node.js 20+
- Go toolchain matching the repository

## Running Locally

```bash
cd backend && go build -o tokenhub-migrate ./cmd/tokenhub-migrate/
docker compose -f deploy/docker-compose.migration-e2e.yml config
docker compose -f deploy/docker-compose.migration-e2e.yml up -d --wait
cd sdk/migration-e2e
npm ci
TOKENHUB_MIGRATE_BIN=../../backend/tokenhub-migrate npm run test:litellm
docker compose -f ../../deploy/docker-compose.migration-e2e.yml down -v
```

## Fixture Assets

- Compose fixture: `deploy/litellm-config.yaml`
- Mock upstream config: `deploy/mock-upstream.conf`
- Extraction fixture: `sdk/migration-e2e/fixtures/proxy_config.yaml`
- Harness: `sdk/migration-e2e/litellm-e2e.mjs`

## Current Scope

The harness currently proves:
1. LiteLLM stack boots from the checked-in fixture
2. LiteLLM can answer a mocked chat-completion request
3. TokenHub accepts the extracted bundle through the real Admin API
4. `verify` passes after apply and re-apply creates and updates zero resources
5. Checkpoint and one-time API key files are persisted
6. Rollback removes created resources, including the imported user, and a
   subsequent verify detects the rolled-back state

## CI

The workflow runs migration unit checks on relevant backend, docs, SDK, deploy, and workflow changes. The E2E job runs on pushes and on PRs labeled `migration:e2e`.

## Troubleshooting

- Ensure Docker daemon is running
- Check that ports 4000, 8080, and 8081 are available
- Review `docker compose logs` for service-level errors
