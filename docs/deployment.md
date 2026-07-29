# Deployment

Language: English | [简体中文](zh-CN/deployment.md) | [日本語](ja/deployment.md)

TokenHub is designed for private deployment with a Go backend, a Next.js admin console, and support for SQLite or PostgreSQL persistence.

## Database Selection

TokenHub supports two database backends:

### SQLite (Default)

**Advantages:**
- Zero configuration, no separate database service required
- Suitable for small to medium deployments
- Simple backups (direct file copy)

**Use cases:**
- Development and testing environments
- Deployments with fewer than 1000 users
- Single-server deployments

**Deployment:**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

### PostgreSQL (Production Recommended)

**Advantages:**
- Enterprise-grade database for high concurrency scenarios
- Better transaction support and data integrity
- Supports replication and high availability

**Use cases:**
- Production environments
- Deployments with more than 1000 users
- High-availability requirements

**Deployment:**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d
```

For detailed PostgreSQL configuration, see the [PostgreSQL Setup Guide](postgresql-setup.md).

### Multi-instance deployment with remote PostgreSQL

Use `deploy/docker-compose.remote-postgres.yml` when PostgreSQL is managed outside this Compose project. It adds an Nginx gateway in front of scalable backend and frontend services and does not start a local database.

Set the remote `TOKENHUB_DATABASE_URL`, public gateway URL, production secrets, and trusted proxy CIDR, then run:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d \
  --scale tokenhub-backend=3 \
  --scale tokenhub-frontend=2
```

All replicas must use the same `TOKENHUB_SECRET_KEY`. Size `TOKENHUB_DB_MAX_OPEN_CONNS` per replica so the combined pool remains below the PostgreSQL connection limit. Never share a SQLite file between backend replicas.

Run the real two-instance PostgreSQL E2E suite with `./deploy/test-multi-instance.sh`.

## Docker Compose

Create a deployment environment file:

```bash
cp deploy/.env.example deploy/.env
```

Edit `deploy/.env` before starting:

- `TOKENHUB_ADMIN_TOKEN`: Admin API bootstrap token. Use a random value of at least 32 bytes.
- `TOKENHUB_INTEGRATION_TOKEN`: Dedicated credential for external platform integration events and model access key control endpoints. Use a different random value of at least 32 bytes.
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`: Password used only when creating the initial `admin` user. Use at least 12 bytes.
- `TOKENHUB_SECRET_KEY`: Backend secret key. Use a random value of at least 32 bytes and keep it stable.
- `TOKENHUB_PUBLIC_BASE_URL`: Public backend URL shown to users.
- `NEXT_PUBLIC_API_BASE_URL`: Backend URL used by the browser admin console.
- `TOKENHUB_BACKEND_PORT`: Host port for the backend. Default: `8080`.
- `TOKENHUB_FRONTEND_PORT`: Host port for the admin console. Default: `3000`.

Start the stack from the repository root:

```bash
./deploy/install.sh
```

The script validates the Compose environment before building. Validation errors name every unsafe variable without printing its value. If Compose fails and a backend container created or restarted by that attempt is exited, restarting, dead, or unhealthy, the script prints up to 100 backend log lines from that attempt. Failures outside the backend do not export unrelated backend logs.

Validate without building or starting containers:

```bash
./deploy/install.sh --check-only
```

Use a different environment file with `./deploy/install.sh --env-file /path/to/deploy.env`.

### Optional server-side build acceleration

The project Dockerfiles do not hard-code regional package mirrors. If your server has slow access to Docker Hub, npm, or Go module sources, configure acceleration on the deployment host instead of editing Dockerfiles.

For Docker base image pulls, configure Docker daemon registry mirrors on the server, for example in `/etc/docker/daemon.json`, then restart Docker:

```json
{
	"registry-mirrors": [
		"https://<your-docker-registry-mirror>"
	]
}
```

For dependency downloads during image builds, prefer configuring an outbound HTTP/HTTPS proxy for Docker or BuildKit on the server. This keeps builds portable and avoids committing environment-specific npm or Go proxy settings to the repository.

If you deploy in an environment where direct access to upstream registries is slow, the following server-side examples can be used as references:

```bash
# Go module downloads
go env -w GOPROXY=https://goproxy.cn,direct

# npm package downloads
npm config set registry https://registry.npmmirror.com
```

These commands configure the server or build environment. Do not add them directly to project Dockerfiles unless you intentionally maintain an environment-specific fork.

The compose file starts:

- Backend on `http://localhost:8080`
- Frontend on `http://localhost:3000`
- SQLite data stored in the named Docker volume `tokenhub-data`
- Model catalog mounted from `data/model-catalog.yaml`

Check status:

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
```

Initial admin login:

- Username: `admin`
- Password: the configured `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`

For `prod`, `production`, staging, and other non-development environments, startup rejects placeholder values, admin tokens or secret keys shorter than 32 bytes, and bootstrap passwords shorter than 12 bytes.

View or follow logs manually:

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs -f
```

Stop:

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down
```

Stop and remove the SQLite data volume:

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down -v
```

Only use `down -v` when you intentionally want to delete local data.

### External platform integration endpoints

The dedicated integration token authenticates these internal endpoints and must not be replaced by the Admin Token or a model access key:

- `POST /api/internal/integration/events`: apply tenant, organization, principal, project, and workload projections.
- `GET /api/internal/integration/reconciliation?tenant_id=...`: return the tenant Inbox event watermark and per-aggregate counts for upstream reconciliation. It does not return event payloads or projection records.
- `GET /api/internal/model-access-keys?tenant_id=...`: list integration-managed model access keys within one tenant; optional `project_id`, `principal_id`, and effective `status` filters are evaluated by TokenHub. The endpoint supports `page` and `page_size` (maximum 100) and returns pagination metadata.
- `POST /api/internal/model-access-keys`: issue a model access key for an active projected project and principal/workload. `request_id` is mandatory and idempotent; only the first successful response contains `api_key`.
- `POST /api/internal/model-access-keys/{id}/reveal`: reveal an active key after exact `tenant_id`, `principal_type`, and `principal_id` ownership checks. Every successful reveal is audited and the key value is never included in audit data.
- `POST /api/internal/model-access-keys/{id}/revoke`: revoke an integration-managed key within the supplied `tenant_id`. Optional `principal_type` and `principal_id` enforce self-service ownership. Revocation has no rotation or grace period.
- `GET /api/internal/providers`: list providers with server-side `name`, `type`, `status`, and `health` filters and pagination. The response excludes API keys, headers, options, and resource secrets.
- `GET /api/internal/routes`: list model routes with server-side `model_name`, `provider_id`, `strategy`, and `status` filters and pagination. The response includes safe provider/resource names and routing metadata only.

Integration-managed projects are materialized into TokenHub serving projects in the same transaction that applies the project projection. Key creation is rejected until its tenant, project, and user/workload projections are active. Temporarily disabling a tenant, user, or workload makes its related keys unusable without changing their stored key status; eligible keys work again after the projection is re-enabled. Deleting a tenant or workload, or removing a user from a tenant, permanently revokes the affected keys. User status only controls personal keys and does not implicitly disable keys owned by that user's workloads. Explicitly revoked and expired keys never become usable again when a projection is re-enabled.

Integration-managed model access keys store both a one-way authentication hash and an AES-GCM ciphertext encrypted with `TOKENHUB_SECRET_KEY`. Creation fails rather than persisting plaintext if encryption cannot be produced. Keep `TOKENHUB_SECRET_KEY` stable across restarts and replicas; changing it makes existing keys impossible to reveal, although hash-based gateway authentication continues to work.

## Backend Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | Runtime environment label |
| `TOKENHUB_HTTP_ADDR` | `:8080` | Backend listen address |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:8080` | Public backend URL shown to users |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | empty | Comma-separated proxy IPs or CIDRs allowed to supply `X-Forwarded-For` |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | public URL | Comma-separated browser origins allowed to call the backend |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Bootstrap admin token for Admin API access |
| `TOKENHUB_INTEGRATION_TOKEN` | `change-me-tokenhub-integration-token` | Dedicated token for external platform integration events and model access key control endpoints |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | Password for the initial `admin` user; must be changed before production startup |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | Backend secret key |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | Database connection URL (sqlite:// or postgresql://) |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | Backup output directory |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/app/catalog/model-catalog.yaml` | Standard model catalog file |
| `TOKENHUB_SEED_DEMO` | `false` | Whether to seed demo data |
| `TOKENHUB_LOG_LEVEL` | `info` | Log level |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider resource failure threshold before cooldown |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | Provider resource cooldown seconds |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | Expiry and renewal basis for cluster-wide concurrency leases |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | Expiry and renewal basis for cluster coordination locks |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | Maximum time to drain in-flight requests during shutdown |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Compose grace period before Docker force-stops the backend |
| `TOKENHUB_DB_MAX_OPEN_CONNS` | `25` | Maximum open database connections (PostgreSQL only) |
| `TOKENHUB_DB_MAX_IDLE_CONNS` | `5` | Maximum idle database connections (PostgreSQL only) |
| `TOKENHUB_DB_CONN_MAX_LIFETIME_MINUTES` | `30` | Maximum connection lifetime in minutes (PostgreSQL only) |

## Frontend Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `NEXT_PUBLIC_API_BASE_URL` | `http://localhost:8080` | Backend Admin API URL |
| `NEXT_PUBLIC_APP_NAME` | `TokenHub` | Display name |

## Data and Backups

SQLite is the persistent source for projects, keys, Providers, routes, users, request logs, usage, alerts, approvals, sessions, and backup records.

In the one-command compose deployment:

- Database path inside the backend container: `/app/data/tokenhub.db`
- Backup path inside the backend container: `/app/data/backups`
- Docker volume name: `tokenhub-data`

Recommended production setup:

- Store the SQLite database on a persistent disk.
- Store backups outside the application container.
- Rotate old backups according to your retention policy.
- Keep provider credentials and admin tokens in a secret manager or protected environment variables.

## Model Catalog

The deployment mounts the repository file `data/model-catalog.yaml` into the backend container as `/app/catalog/model-catalog.yaml`.

To update the standard catalog:

1. Edit `data/model-catalog.yaml`.
2. Restart the backend container.
3. Open `Model Catalog` in the admin console and confirm the entries.

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml restart tokenhub-backend
```

## Reverse Proxy

For production, place TokenHub behind HTTPS and forward:

- Admin console traffic to the frontend service.
- `/v1/*` and `/api/admin/*` traffic to the backend service.

Set request body and streaming timeouts high enough for long model responses.

Use `/livez` for liveness and `/readyz` for readiness. `/readyz` and the backwards-compatible `/healthz` return `503` when the database is unavailable.
