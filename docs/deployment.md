# Deployment

Language: English | [简体中文](zh-CN/deployment.md) | [日本語](ja/deployment.md)

TokenHub is designed for private deployment with a Go backend, a Next.js admin console, and support for SQLite or PostgreSQL persistence.

## Database Selection

TokenHub supports two database backends:

The commands below use Docker Compose. Both backends are equally supported without Docker; see [Bare-metal Deployment](#bare-metal-deployment-without-docker).

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
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --remove-orphans
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
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d --remove-orphans
```

For detailed PostgreSQL configuration, see the [PostgreSQL Setup Guide](postgresql-setup.md).

### Multi-instance deployment with remote PostgreSQL

The default installation starts one frontend and one backend with SQLite. For horizontal scaling with PostgreSQL managed outside this Compose project, use `deploy/docker-compose.remote-postgres.yml`. It adds an Nginx gateway in front of scalable backend and frontend services and does not start a local database.

```mermaid
flowchart TB
    clients["Clients<br/>Admin Console · OpenAI SDKs"] --> nginx["Nginx Gateway<br/>Load balancing · Health checks"]
    nginx --> frontend["Frontend replicas × N"]
    frontend --> backend["Backend replicas × N"]
    backend <--> providers["Model Providers"]

    local["data/model-catalog.yaml<br/>Candidate-model metadata"] -->|"Startup: parse + upsert templates<br/>cluster lease serializes replicas"| backend
    providerCatalog["data/provider-catalog.json<br/>Tracked Provider templates + candidate models"] -->|"Admin provider setup / refresh"| backend
    backend <-->|"Models · Routes · Provider catalog snapshot<br/>shared state · database locks"| postgres[("Shared PostgreSQL")]

    backend -->|"Provider creation"| rule["Route creation rule<br/>selected candidate → upsert Model → Route<br/>automatic candidate ∩ local Model → Route"]
    local -.-> rule
    providerCatalog -.-> rule
    rule -->|"Create matching Route"| postgres
```

In multi-instance mode:

- Nginx load-balances console, API, and health-check traffic across healthy replicas.
- Backend replicas keep durable configuration, OAuth sessions, quota buckets, audit data, cluster locks, and in-flight concurrency leases in PostgreSQL.
- Lease expiry and ownership decisions use the PostgreSQL clock, avoiding early takeover caused by clock skew between hosts. Heartbeats cancel work when lease ownership is lost.
- Candidate-model metadata from the configured model catalog is synchronized on every backend startup; a cluster lease serializes the idempotent synchronization across replicas.
- Provider templates and candidate models are read from the tracked local provider catalog; runtime configuration does not depend on a remote catalog service.
- The backend persists a local Provider-catalog snapshot in PostgreSQL, so replicas serve the same catalog and a missing local file falls back to the seeded built-in templates.
- Coordination failures release provider capacity without incorrectly marking a healthy model provider as failed.

Set the remote `TOKENHUB_DATABASE_URL`, public gateway URL, production secrets, trusted proxy CIDR, and the desired `TOKENHUB_BACKEND_REPLICAS` and `TOKENHUB_FRONTEND_REPLICAS` values in `deploy/.env`, then run:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d
```

All replicas must use the same `TOKENHUB_SECRET_KEY`. Size `TOKENHUB_DB_MAX_OPEN_CONNS` per replica so the combined pool remains below the PostgreSQL connection limit. Never share a SQLite file between backend replicas.

Run the real two-instance PostgreSQL E2E suite with `./deploy/test-multi-instance.sh`.

## Native Release with systemd

Use the native Release installer for a single Linux host with systemd. Native packages support `linux/amd64` and `linux/arm64`, and bundle the Go backend, the standalone Next.js console, and a matching Node.js runtime.

Download and inspect the installer, then install the latest stable Release:

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

When `TOKENHUB_PUBLIC_HOST` is not set, the installer requests `https://ipinfo.io/json` and uses its validated IP response. If that lookup fails, it falls back to the first address from `hostname -I`, then `127.0.0.1`. The detected egress IP might not be the inbound address when the server is behind NAT, a proxy, or a load balancer, so set `TOKENHUB_PUBLIC_HOST` when users open a different IP address or hostname. IPv6 literals are automatically bracketed when URLs are generated:

```bash
sudo env TOKENHUB_PUBLIC_HOST=tokenhub.example.com \
  bash /tmp/tokenhub-install.sh install
```

The resolved host is stored in `/etc/tokenhub/tokenhub.env` and reused by later installer runs, so upgrade output remains consistent even when automatic IP discovery changes.

To use PostgreSQL from the first start instead of the default SQLite database, pass its URL to the initial installation:

```bash
sudo env \
  TOKENHUB_DATABASE_URL='postgres://user:password@db.example.com:5432/tokenhub?sslmode=require' \
  bash /tmp/tokenhub-install.sh install
```

The installer writes this value to `/etc/tokenhub/tokenhub.env` only when creating the configuration. Later install, upgrade, and rollback runs preserve the existing file; edit it and restart TokenHub when intentionally changing databases.

The first installation generates production secrets and an initial admin password. The password is printed once. Runtime files are kept in separate locations:

- Releases and the `current` symlink: `/opt/tokenhub`
- Configuration and secrets: `/etc/tokenhub/tokenhub.env`
- SQLite database and backups: `/var/lib/tokenhub`
- Generated images: `/var/lib/tokenhub/images`
- Linux systemd unit: `/etc/systemd/system/tokenhub.service`

Edit `/etc/tokenhub/tokenhub.env` when changing public URLs, CORS origins, ports, database settings, or secrets, then restart the service:

```bash
sudo systemctl restart tokenhub
sudo systemctl status tokenhub
sudo journalctl -u tokenhub -f
```

The installer verifies the Release archive against `checksums.txt` before activation and preserves configuration and data during upgrades:

```bash
sudo bash /tmp/tokenhub-install.sh upgrade
sudo bash /tmp/tokenhub-install.sh upgrade --version 0.3.3
sudo bash /tmp/tokenhub-install.sh rollback --version 0.3.2
sudo bash /tmp/tokenhub-install.sh uninstall
```

`upgrade` refuses a target older than the installed version; use the explicit `rollback` command for a downgrade. Upgrading an installation created by an older installer automatically adds `/var/lib/tokenhub/images` as persistent image storage unless `TOKENHUB_IMAGE_STORAGE_DIR` is already configured.

`uninstall` preserves `/etc/tokenhub` and `/var/lib/tokenhub`. Use `uninstall --purge` only when configuration and application data should also be deleted.
The installer records ownership markers in the application, configuration, and state directories. Uninstall refuses to recursively remove an unmarked or mismatched directory, and system-level paths such as `/opt`, `/etc`, and `/var/lib` are never accepted as managed directory targets. A fresh installation rejects equal backend and frontend ports. When `ss` or `lsof` is available, it also rejects occupied ports before downloading a Release. Install and upgrade report success only after the systemd unit is active and both the backend health endpoint and admin console respond; readiness failures include recent service logs.

For a fork, use its installer URL and tell TokenHub which public Release repository to query:

```bash
sudo env TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub \
  bash /tmp/tokenhub-install.sh install --version 0.3.3
```

Native Release installations are labeled `Native Release` in the version panel. Administrators can download and verify an update or rollback directly from the panel, then select **Restart now** to activate it through systemd. Each GitHub Release tag must be a strict `v`-prefixed semantic version and contain the Linux archive and `checksums.txt`; `.github/workflows/native-release.yml` builds and attaches the `linux/amd64` and `linux/arm64` assets when a Release is published.
Previously downloaded, validated releases remain available for rollback when the GitHub Releases API cannot be reached.

## Docker Compose

Create a deployment environment file:

```bash
cp deploy/.env.example deploy/.env
```

Edit `deploy/.env` before starting:

- `TOKENHUB_ADMIN_TOKEN`: Admin API bootstrap token. Use a random value of at least 32 bytes.
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`: Password used only when creating the initial `admin` user. Use at least 12 bytes.
- `TOKENHUB_SECRET_KEY`: Backend secret key. Use a random value of at least 32 bytes and keep it stable.
- `TOKENHUB_IMAGE_TAG`: Managed TokenHub image tag. Default: `latest`.
- `TOKENHUB_PUBLIC_BASE_URL`: Public backend URL shown to users.
- `TOKENHUB_API_BASE_URL`: Backend URL used by the browser admin console. The frontend server reads it at runtime. The deprecated `NEXT_PUBLIC_API_BASE_URL` remains a fallback for one compatibility cycle.
- `TOKENHUB_BACKEND_PORT`: Host port for the backend. Default: `8080`.
- `TOKENHUB_FRONTEND_PORT`: Host port for the admin console. Default: `3000`.
- `TOKENHUB_BACKEND_REPLICAS`: Backend replica count for remote PostgreSQL Compose. Default: `2`.
- `TOKENHUB_FRONTEND_REPLICAS`: Frontend replica count for remote PostgreSQL Compose. Default: `2`.

Start the stack from the repository root:

```bash
./deploy/install.sh
```

The script validates the Compose environment, pulls the published image, and starts the managed application container without building locally. It waits up to 180 seconds for the Compose health check before reporting success. It also removes the obsolete standalone frontend container when upgrading from the former two-container layout; the `tokenhub-data` volume is preserved. If the image cannot be pulled during the initial GHCR rollout, it falls back to building from the local checkout. Validation errors name every unsafe variable without printing their values. If the new backend fails or does not become healthy, the script prints up to 100 log lines from that attempt.

Validate without pulling or starting containers:

```bash
./deploy/install.sh --check-only
```

Use a different environment file with `./deploy/install.sh --env-file /path/to/deploy.env`.

### Published image lifecycle

GitHub Actions publishes the complete `ghcr.io/astaxie/tokenhub-backend` image for `linux/amd64` and `linux/arm64`. Despite the compatibility-preserving image name, it contains the backend, standalone Next.js console, Node.js runtime, and the container supervisor.

- Publishing a GitHub Release with a strict `v`-prefixed semantic tag builds the exact numeric image tag. A non-prerelease also updates the major-minor tag and `latest`.
- `workflow_dispatch` can publish `edge` or an isolated `manual-*` tag. It cannot overwrite release or `latest` tags.
- Pull requests do not build or push container images.
- Merges to `main` do not publish images.

The image is first pushed under a run-specific staging tag and verified before the workflow promotes it to the requested release tags. For reproducible production deployments, pin an exact release tag instead of relying on `latest`.

The first GHCR publication creates a private package. The repository owner must make it public before anonymous deployments can pull it. Until then, a deployment using the default `latest` tag remains usable by automatically falling back to a local source build. If an explicit `TOKENHUB_IMAGE_TAG` cannot be pulled, the installer exits instead of labeling current source as that version.

### Docker version status and rollback

Platform administrators can select the version badge below the TokenHub logo to inspect the running version, check the latest stable GitHub Release, and list up to three older stable releases. Release builds receive their exact version from the publication workflow; local source builds use the package version and are labeled as source builds. Managed update, rollback, and restart requests are recorded in the administrator audit log.

The check makes a time-limited outbound HTTPS request to the public GitHub Releases API and caches successful results for 20 minutes. It checks `astaxie/TokenHub` by default. Maintainers can set `TOKENHUB_RELEASE_REPOSITORY` to another trusted public `owner/repository` when validating releases from a fork. A GitHub outage or a repository without releases does not affect gateway traffic. The panel reports the unavailable state and keeps the current version visible.

For example, check a fork while running from source:

```bash
TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub ./start.sh
```

The default SQLite and local PostgreSQL Compose files run a single managed application container. An administrator can select **Update now**, wait for the checksummed platform Release bundle to be installed under the `tokenhub-releases` volume, and then select **Restart now**. The process exits after responding; Docker's `restart: unless-stopped` policy starts the selected backend and frontend together. The container never mounts the Docker socket or controls the host daemon.

The image version and content fingerprint form the baseline when a newly pulled image first uses the volume. Panel-applied updates survive ordinary restarts and container recreation with the same image because `current` and all installed Release bundles are stored in `tokenhub-releases`. Pulling a different image or rebuilding changed source under the same version activates the new image content. The remote PostgreSQL multi-instance Compose file disables in-place updates because changing only the replica that receives an admin request would split the cluster; its panel instructs operators to update manually with the original Compose file and environment configuration so configured replica counts are preserved. Source deployments continue to show manual guidance. Before rollback, create a database backup and confirm that the target release supports the current schema.

### Optional local build

Build from the current checkout instead of pulling published images:

```bash
./deploy/install.sh --build
```

The following acceleration settings apply only to local source builds.

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
- Model catalog included in the selected backend image

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

## Bare-metal Deployment (without Docker)

`deploy/bare-metal/` runs the same two services with no Docker anywhere. Use it when Docker is unavailable or not permitted. The application is unchanged: the backend is a single Go binary and the console is the Next.js standalone server.

There are two entry points:

| | Use it for | Needs root | Survives reboot |
| --- | --- | --- | --- |
| `run-local.sh` | Running the production build on your own machine | No | No |
| `build.sh` + `install.sh` | Installing on a server under systemd | Yes | Yes |

Both are **SQLite only**. The backend also speaks PostgreSQL, but these scripts do not manage it; see [Database](#database) below.

### Run locally

```bash
./deploy/bare-metal/run-local.sh          # foreground, Ctrl-C stops both
./deploy/bare-metal/run-local.sh -d       # background, returns immediately
./deploy/bare-metal/run-local.sh status
./deploy/bare-metal/run-local.sh logs -f
./deploy/bare-metal/run-local.sh stop
```

Builds both components if needed, then runs them on loopback. The binary, the console bundle, the database, the logs and the pid files all live in `.tokenhub/` inside the repository, which is gitignored; deleting that directory removes every trace. Nothing is installed system-wide and no service account is created.

With `-d` the services detach from the launching shell and keep running after it exits — and after the terminal closes — but not across a reboot; use the systemd installation for that. Both modes write pid files, so `status` and `stop` also work on a foreground instance. `stop` verifies that the recorded pid still belongs to this instance before signalling it, so a recycled pid is never killed by mistake, and both ports are claimed before anything starts so the script cannot report success against an unrelated service already listening.

This is verified on Linux. macOS lacks `setsid`, so the script falls back to walking the process tree when stopping; that path is implemented but untested on macOS.

This runs the **production** build — the same standalone bundle a deployment runs — rather than a dev server, so it surfaces problems that only appear in a production build. It uses development credentials (`admin` / `admin123456`) and binds loopback only, so it is for local use, not a deployment.

Options: `--rebuild`, `--reset` to drop the local database, `--backend-port N`, `--console-port N`, `restart`.

### Requirements

| Host | Requirements |
| --- | --- |
| Build host | Go (the version in `backend/go.mod`), Node 22 or newer, npm, a C compiler |
| Target host | Linux with systemd and GNU coreutils/findutils (standard on every mainstream distribution), Node 22 or newer installed system-wide |

The backend links SQLite through cgo, so a C compiler is required and the resulting binary is tied to the target architecture and libc. Build on a host that matches the target.

Node must be installed system-wide. The service units set `ProtectHome=true`, so an interpreter under a user's home directory (nvm, fnm, asdf) is unreachable at runtime. The installer rejects such an interpreter instead of producing a service that fails later.

### Build a release

```bash
./deploy/bare-metal/build.sh
```

This produces `dist/tokenhub-<version>-<os>-<arch>/` and a matching `.tar.gz`. The directory is self-contained: backend binary, console bundle, both catalogs, unit files, configuration examples, the installer and `SHA256SUMS`. Copy either form to the target host; the target needs no repository checkout and no Go toolchain.

### Install

```bash
sudo ./install.sh --generate-secrets
```

`--generate-secrets` fills `TOKENHUB_SECRET_KEY`, `TOKENHUB_ADMIN_TOKEN` and `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` on a freshly created configuration. Without it, a configuration that still holds `change-me-*` placeholders leaves both units installed but **disabled and not started**, because an enabled unit with an invalid configuration would crash-loop on the next boot.

After starting, the installer polls `/readyz` and the console port and prints `systemctl status` plus recent journal entries if either fails.

Useful flags: `--no-start`, `--install-timers` (backup timer for the configured database), `--skip-backup` (skip the pre-upgrade snapshot; not recommended).

### Installed layout

| Path | Owner | Contents |
| --- | --- | --- |
| `/opt/tokenhub/releases/<version>/` | `root:root` | Immutable release payload |
| `/opt/tokenhub/current` | symlink | Active release; switched atomically |
| `/etc/tokenhub/backend.env` | `root:tokenhub` 0640 | Backend configuration and secrets |
| `/etc/tokenhub/frontend.env` | `root:tokenhub-web` 0640 | Console configuration, no secrets |
| `/var/lib/tokenhub/` | `tokenhub:tokenhub` 0750 | SQLite database, backups, pre-upgrade snapshots |

Two service accounts are used: `tokenhub` for the backend and `tokenhub-web` for the console. They never share a configuration file, so a compromised console cannot read the secret key, the admin token or database credentials. The backend unit runs with `UMask=0077`, keeping the SQLite database and its backups private.

### Database

The database lives at `/var/lib/tokenhub/tokenhub.db` and backups at `/var/lib/tokenhub/backups`. Both paths are absolute in `backend.env` on purpose: the built-in defaults are relative to the working directory, which is the read-only release directory here.

The backend can also run on PostgreSQL, but the installer **rejects** such a configuration rather than half-supporting it. Managing PostgreSQL properly would mean owning the preflight checks, the pre-upgrade `pg_dump`, backup retention, and a client toolchain whose version has to track the server's — none of which this installer does. To run TokenHub on PostgreSQL, configure and supervise the backend yourself; see the [PostgreSQL Setup Guide](postgresql-setup.md).

### Backups

`--install-timers` installs a daily timer that calls the backend's own online-backup API and then deletes expired backups. The application records an expiry but never prunes on its own, so retention is driven by the timer. Never copy a live SQLite file instead; the copy would be torn. The backup holds the backend's single SQLite connection for its duration, so it is scheduled off-peak by default, and the static admin token authenticates as the earliest-created admin user — demoting that user breaks the timer.

The timer exits non-zero on failure and logs to the journal; monitor `systemctl --failed` or add an `OnFailure=` unit.

A restore is only useful together with the original `TOKENHUB_SECRET_KEY`, which decrypts stored provider credentials. Back that key up separately. Backups written to the same disk as the database are a local recovery copy, not disaster recovery; copy them off-host.

### Upgrade and rollback

Run `install.sh` from the new release. It stages the payload, verifies checksums, stops both services, takes a **verified pre-upgrade database snapshot**, switches `current` atomically, and restarts. The previous release is retained.

The snapshot is not optional protection: startup runs `AutoMigrate` unconditionally, so pointing `current` back at the old release does **not** roll the schema back. A rollback means restoring the snapshot as well. The snapshot aborts if `-wal`, `-shm` or `-journal` sidecar files remain after the service stops, because the database was then not closed cleanly.

### Uninstall

```bash
sudo /opt/tokenhub/current/uninstall.sh            # keeps /etc/tokenhub and /var/lib/tokenhub
sudo /opt/tokenhub/current/uninstall.sh --purge    # also deletes the database and every local backup
```

### Exposing the services

Both services bind all interfaces by default, exactly like the Compose deployment. For anything beyond a single-machine trial, put them behind a reverse proxy with TLS, restrict the ports with a firewall, and set `TOKENHUB_TRUSTED_PROXY_CIDRS` to the proxy addresses.

`TOKENHUB_API_BASE_URL` in `frontend.env` is rendered into the page and called **by the browser**, not by the Node process:

- `http://127.0.0.1:8080` only works when the console is opened on the server itself.
- A console served over HTTPS cannot call an HTTP API; browsers block mixed content.
- `TOKENHUB_CORS_ALLOWED_ORIGINS` must list the exact console origin — scheme, host and port, with no path.
- The admin session caches the base URL in the browser's local storage, so changing this value does not affect an existing session until the user logs out or clears site data.

## Backend Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | Runtime environment label |
| `TOKENHUB_HTTP_ADDR` | `:8080` | Backend listen address |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:8080` | Public backend URL shown to users |
| `TOKENHUB_RELEASE_REPOSITORY` | `astaxie/TokenHub` | Trusted public GitHub repository used for version checks, in `owner/repository` form |
| `TOKENHUB_DEPLOYMENT_TYPE` | build-time value | Overrides the deployment type compiled into the binary: `source`, `container` or `native`. The Compose files set `container` |
| `TOKENHUB_MANAGED_UPDATES` | `false` | Allows a container deployment to perform online update and rollback. A native deployment always allows it |
| `TOKENHUB_INSTALL_ROOT` | `/opt/tokenhub` | Managed Release installation root used for online update and rollback |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | empty | Comma-separated proxy IPs or CIDRs allowed to supply `X-Forwarded-For` |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | public URL | Comma-separated browser origins allowed to call the backend |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Bootstrap admin token for Admin API access |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | Password for the initial `admin` user; must be changed before production startup |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | Backend secret key |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | Database connection URL (sqlite:// or postgresql://) |
| `TOKENHUB_DB_HOST` | empty | PostgreSQL host. Setting it builds the DSN from the `TOKENHUB_DB_*` fields instead of `TOKENHUB_DATABASE_URL`, which avoids URL encoding when the password contains `#`, `?`, `/` or `%`. `TOKENHUB_DATABASE_URL` still takes precedence when both are set |
| `TOKENHUB_DB_PORT` | `5432` | PostgreSQL port; used only when `TOKENHUB_DB_HOST` is set |
| `TOKENHUB_DB_USER` | empty | PostgreSQL user; used only when `TOKENHUB_DB_HOST` is set |
| `TOKENHUB_DB_PASSWORD` | empty | PostgreSQL password; used only when `TOKENHUB_DB_HOST` is set |
| `TOKENHUB_DB_NAME` | empty | PostgreSQL database name; used only when `TOKENHUB_DB_HOST` is set |
| `TOKENHUB_DB_SSLMODE` | `disable` | PostgreSQL sslmode; used only when `TOKENHUB_DB_HOST` is set |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | Backup output directory |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/opt/tokenhub/current/catalog/model-catalog.yaml` | Standard model catalog file in managed deployments |
| `TOKENHUB_PROVIDER_CATALOG_FILE` | `/opt/tokenhub/current/catalog/provider-catalog.json` | Provider templates and candidate-model catalog file in managed deployments |
| `TOKENHUB_SEED_DEMO` | `false` | Whether to seed demo data |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider resource failure threshold before cooldown |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | Base cooldown before a parked provider resource is given a half-open retry |
| `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` | `3600` | Upper bound for the exponential backoff applied to repeated recovery failures |
| `TOKENHUB_METRICS_ENABLED` | `false` | Collect Prometheus metrics and serve `GET /metrics` |
| `TOKENHUB_METRICS_TOKEN` | empty | Bearer token for `/metrics`; falls back to the admin token when empty |
| `TOKENHUB_METRICS_PROJECT_LABEL` | `false` | Add `project_id` to gateway metrics; raises series count by the project count |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | Expiry and renewal basis for cluster-wide concurrency leases |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | Expiry and renewal basis for cluster coordination locks |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | Maximum time to drain in-flight requests during shutdown |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Compose grace period before Docker force-stops the backend |
| `TOKENHUB_CACHE_AFFINITY_ENABLED` | `false` | Pin a session to one upstream account so the provider's prompt cache keeps hitting. Off by default because it changes routing behaviour |
| `TOKENHUB_CACHE_AFFINITY_MODELS` | empty | Comma-separated model allowlist for staged rollout; empty means every model |
| `TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE` | `false` | Also accept user-scoped identifiers as affinity keys; off by default because one user's concurrent sessions would share a single account |
| `TOKENHUB_IMAGE_STORAGE_DIR` | `data/images` | Directory holding generated image assets |
| `TOKENHUB_IMAGE_WORKER_CONCURRENCY` | `2` | Number of workers draining the image generation queue |
| `TOKENHUB_IMAGE_QUEUE_CAPACITY` | `64` | Maximum image jobs that may wait in the queue |
| `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` | `300` | Time limit for a single image generation job before it is failed |
| `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS` | `86400` | How long a provider resource marked as lacking image support is skipped before it is probed again |
| `TOKENHUB_DB_MAX_OPEN_CONNS` | `25` | Maximum open database connections (PostgreSQL only) |
| `TOKENHUB_DB_MAX_IDLE_CONNS` | `5` | Maximum idle database connections (PostgreSQL only) |
| `TOKENHUB_DB_CONN_MAX_LIFETIME_MINUTES` | `30` | Maximum connection lifetime in minutes (PostgreSQL only) |

## Frontend Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `TOKENHUB_API_BASE_URL` | `http://localhost:8080` | Backend Admin API URL read by the frontend server at runtime |
| `NEXT_PUBLIC_API_BASE_URL` | empty | Deprecated compatibility fallback; migrate to `TOKENHUB_API_BASE_URL` |

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

## Catalog Files

Published managed images and native archives include matching copies of `data/model-catalog.yaml` and `data/provider-catalog.json`. They are activated with the rest of the release under `/opt/tokenhub/current/catalog/`, so the backend binary and both catalogs always come from the same version. The Provider catalog is vendored from PublicProviderConf and is read locally at runtime; TokenHub does not fetch remote catalog data.

To mount a custom catalog explicitly:

```bash
./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml
```

After editing the configured catalog file, restart the backend or use **Restore Candidate Templates** on the Model Directory's **Candidate Templates** tab. This refreshes reference metadata without removing custom external models and does not publish any template.

The custom mount intentionally overrides the image catalog and is therefore managed separately from `TOKENHUB_IMAGE_TAG`. After updating that file, restart the backend container and confirm the entries on the **Candidate Templates** tab.

`data/model-catalog.yaml` provides reference metadata for candidate templates; it is not a route allowlist and does not publish models. `data/provider-catalog.json` provides Provider templates and the candidate upstream models that can be selected during Provider setup. Importing a selection creates persisted Provider-model inventory. Publishing additionally creates or reuses an external model and adds an enabled mapping. `GET /v1/models` lists only active external models with at least one active route, filtered by the API Key model allowlist when configured. To use a custom Provider catalog, set `TOKENHUB_PROVIDER_CATALOG_FILE` to a local JSON file using the same `providers` structure.

## Reverse Proxy

For production, place TokenHub behind HTTPS and forward:

- Admin console traffic to the frontend service.
- `/v1/*` and `/api/admin/*` traffic to the backend service.

Set request body and streaming timeouts high enough for long model responses.

Use `/livez` for liveness and `/readyz` for readiness. `/readyz` and the backwards-compatible `/healthz` return `503` when the database is unavailable.
