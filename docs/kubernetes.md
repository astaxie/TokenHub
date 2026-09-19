# Kubernetes Deployment

Language: English | [简体中文](zh-CN/kubernetes.md) | [日本語](ja/kubernetes.md)

TokenHub ships a Helm chart at `deploy/helm/tokenhub` for Kubernetes clusters. It deploys the single TokenHub container image in `all` run mode: every pod serves both the Go API and gateway on port 8080 and the Next.js admin console on port 3000, the same process layout as the default Docker Compose deployment.

## Requirements

- Kubernetes 1.25+ and Helm 3.8+.
- PostgreSQL: a managed service for production, or the built-in subchart for quick testing (below).
- Recommended: an ingress controller so the console and the OpenAI-compatible API share one hostname. The default annotations target ingress-nginx.

The backend runs schema AutoMigrate on pod start. Take a PostgreSQL backup before upgrading the image tag.

## Quick test with the built-in PostgreSQL

For throwaway clusters and smoke tests, enable the bundled `bitnami/postgresql` subchart:

```bash
# Chart.lock pins the subchart but does not register its repository.
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -hex 16)"
```

The chart composes the database URL from the subchart service. Without an ingress the pod is reachable through port-forwarding (see the install notes). This mode is not sized or tuned for production.

## Production with a managed PostgreSQL

Point `database.url` at a managed PostgreSQL service such as RDS, Cloud SQL, or Azure Database, keep `postgresql.enabled=false`, and enable the ingress:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true \
  --set ingress.host=tokenhub.example.com \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD="<stable-bootstrap-password>" \
  --set database.url='postgresql://tokenhub:password@postgres.example.com:5432/tokenhub?sslmode=require' \
  --set imageStorage.type=pvc \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8\,172.16.0.0/12\,192.168.0.0/16'
```

`TOKENHUB_TRUSTED_PROXY_CIDRS` must cover the ingress controller's source addresses so client IP attribution (rate limits, audit entries) sees real client addresses instead of the controller IP. Private pod/service CIDRs are a reasonable starting point; tighten the list to your cluster. The commas inside one value must be escaped as `\,` because Helm's `--set` parser treats a bare comma as a list separator; a values file avoids the escaping entirely.

With the ingress enabled the chart derives the console's browser-facing API URL (`TOKENHUB_API_BASE_URL`) from the ingress scheme and host, so browser logins reach the API service instead of the visitor's own machine. Publishing the console another way (a load balancer or remote port-forward) requires setting `apiBaseUrl` explicitly.

The first login is username `admin` with the bootstrap password you configured in `secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`:

```bash
kubectl get secret tokenhub-tokenhub-credentials -o jsonpath='{.data.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD}' | base64 -d
```

The chart never generates this password: set a stable value through `secretEnv` and keep it unchanged across upgrades, since the bootstrap seed only runs on an empty database. Setting it through `extraEnv` instead would duplicate the Secret-backed entry the chart already renders. Change the password in the admin console after first login.

The chart sets `TOKENHUB_ENV=prod` by default. Production startup rejects the built-in development admin token, and an explicit `TOKENHUB_ADMIN_TOKEN` (for machine-to-machine admin API calls) can be supplied through `secretEnv`; startup rejects weak values in this mode.

Credentials can also come from existing secrets instead of inline values:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true --set ingress.host=tokenhub.example.com \
  --set credentialsSecret=tokenhub-auth \
  --set database.existingSecret=tokenhub-database
```

The referenced auth secret must provide `TOKENHUB_SECRET_KEY` and `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`; the database secret must provide `TOKENHUB_DATABASE_URL`.

## What the chart deploys

| Resource | Purpose |
| --- | --- |
| Deployment | One pod per replica runs both processes via `tokenhub-run`; environment entries are rendered directly into the pod spec |
| Secrets | `<release>-credentials` for the auth keys and optional admin credentials, `<release>-database` for the composed database URL (each only when the chart manages it) |
| Service | `api` port (8080) and `console` port (3000) |
| PersistentVolumeClaim | Generated image storage, only when `imageStorage.type` is `pvc` (see below) |
| Ingress | API paths to the `api` port, everything else to the `console` port (disabled by default) |
| PodMonitor | Only when `podMonitor.enabled` is true (see below) |
| ExternalSecret | Only when `externalSecret.enabled` is true (see below) |

The ingress mirrors `deploy/nginx.multi-instance.conf`:

| Path | Backend port |
| --- | --- |
| `/api`, `/v1`, `/v1beta`, `/docs`, `/openapi.json`, `/openapi.yaml`, `/healthz`, `/readyz`, `/livez` | `api` (8080) |
| everything else | `console` (3000) |

The default ingress annotations turn response buffering off and raise the read/send timeouts to 300 seconds so streaming responses survive, and set the body size limit to 32 MiB, matching `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES`. Override `ingress.annotations` for other ingress controllers or different limits; keep the body limit at or above the multimodal request limit.

## Health and shutdown

`tokenhub-run` supervises both processes and exits the container when either dies, so Kubernetes restarts the pod on any process failure. The startup and readiness probes HTTP-check the API `/readyz` endpoint, so pods stop receiving traffic while the database is unreachable; the liveness probe checks API `/livez`. The default `terminationGracePeriodSeconds` of 180 covers the backend's default 150-second graceful shutdown window, so in-flight streaming requests drain before the pod is removed. The rollout keeps `maxUnavailable: 0` so capacity is never dropped during upgrades.

## Stateless by default

The chart is stateless by default:

- **Plugins:** runtime-installed plugin packages (admin console uploads and marketplace installs) live on an `emptyDir` and are removed when a pod is replaced. Built-in provider plugins ship inside the image and always survive. To keep installed packages across pod restarts, bake them into a custom image.
- **Releases:** the container materializes its own release bundle into an ephemeral directory on every start. Model and provider catalogs ship inside the image and seed the database on first start; catalog changes made in the admin console live in PostgreSQL and survive pod replacement. In-application self-update is disabled (`TOKENHUB_MANAGED_UPDATES=false`); upgrade by changing `image.tag`:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values --set image.tag=<new-tag>
```

- **Data:** the PostgreSQL server holds all durable state. Back it up with your PostgreSQL tooling; see [PostgreSQL setup](postgresql-setup.md).

## Generated image storage

Generated image bytes live on disk while PostgreSQL stores their metadata, so every replica must see the same directory. `imageStorage` controls where that directory lives:

- `ephemeral` (default): the container filesystem. Acceptable for a single test replica; bytes are lost on pod replacement and peer replicas return 404 for images another replica wrote. The install notes warn when `replicaCount` is above 1.
- `pvc`: the chart renders a `ReadWriteMany` PersistentVolumeClaim (sized by `imageStorage.size`, storage class `imageStorage.storageClass`) and mounts it in every replica. Requires a storage provider that supports multi-access volumes (NFS, EFS, CephFS, ...).
- `existingClaim`: mount a claim you manage yourself through `imageStorage.existingClaim`.

The volume mounts at `imageStorage.mountPath` (`/app/data/images`), which the chart exports as `TOKENHUB_IMAGE_STORAGE_DIR`.

Image-job recovery is scoped to the instance that accepted a request: restarting, upgrading, or scaling the deployment never fails a job that a still-running replica is processing. Jobs owned by an instance that dies are failed after its heartbeat lapses (about 90 seconds), so clients receive a definitive failure instead of waiting forever.
This guarantee needs a backend image that contains the fix. The published `0.8.0` image predates instance-scoped recovery and still fails a live peer's running jobs when another replica starts or stops: with that image keep `replicaCount` at `1`, or pin `image.tag` to a release cut after this chart.

## Monitoring with PodMonitor

When the cluster runs [Prometheus Operator](https://prometheus-operator.dev/), enable a PodMonitor that scrapes `/metrics` on the API port. The metrics endpoint must be switched on through `extraEnv`:

`extraEnv` is replaced wholesale on every upgrade — `--reuse-values` does not merge it — so restate the entries you already set (the trusted-proxy list from the production install above) alongside the new one:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8\,172.16.0.0/12\,192.168.0.0/16' \
  --set 'extraEnv[1].name=TOKENHUB_METRICS_ENABLED' \
  --set-string 'extraEnv[1].value=true' \
  --set podMonitor.enabled=true \
  --set podMonitor.bearerTokenSecret.name=<secret-with-token> \
  --set podMonitor.bearerTokenSecret.key=TOKENHUB_METRICS_TOKEN
```

For the same reason a values file (`-f monitoring-values.yaml` holding the full `extraEnv` list) is easier to maintain across several upgrades.

The metrics endpoint requires a bearer token: set a dedicated `TOKENHUB_METRICS_TOKEN` (via `secretEnv`) and reference the secret through `podMonitor.bearerTokenSecret`; add `podMonitor.additionalLabels` so your Prometheus installation selects the monitor.

## Credential management with ExternalSecret

When the cluster runs the [External Secrets Operator](https://external-secrets.io/), the chart can fill its credentials secret from an external manager such as Vault or AWS Secrets Manager instead of rendering one itself:

Migrating an existing release needs the previously configured credential and database values cleared, because `--reuse-values` keeps them and they conflict with this mode:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set externalSecret.enabled=true \
  --set externalSecret.secretStore=aws-secrets-manager \
  --set externalSecret.sourceSecretId=tokenhub/prod \
  --set database.url=null \
  --set postgresql.enabled=false \
  --set-string 'secretEnv.TOKENHUB_SECRET_KEY=' \
  --set-string 'secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD='
```

The empty-string assignments matter: Helm deletes a map key that is assigned `null`, and a deleted key no longer produces the pod's secret environment reference even after ESO fills the secret — the container then fails startup. `postgresql.enabled=false` instead of `null` for the same reason: `null` strips the boolean the subchart condition keys on and leaves the bundled PostgreSQL rendered. Assigning `database.url=null` is safe because the pod reads the database URL through the secret only.

On a fresh install without prior values the same command works without those overrides. The chart stops managing the credentials secret during this transition; the operator takes ownership of it on the first sync.

With AWS Secrets Manager as the example, the three values map to:

1. Store the credentials in one AWS secret whose keys use the `TOKENHUB_*` names:

   ```bash
   aws secretsmanager create-secret --name tokenhub/prod --secret-string '{
     "TOKENHUB_SECRET_KEY": "0123456789abcdef0123456789abcdef",
     "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD": "a-stable-bootstrap-password",
     "TOKENHUB_DATABASE_URL": "postgresql://tokenhub:password@tokenhub.rds.amazonaws.com:5432/tokenhub?sslmode=require"
   }'
   ```

   `externalSecret.sourceSecretId` is this secret's name (`tokenhub/prod`).

2. Create a `ClusterSecretStore` once per cluster; its metadata name is `externalSecret.secretStore`. With IRSA authentication it references the chart's service account, so annotate that account with the role:

   ```bash
   helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
     --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/tokenhub-external-secrets
   ```

   ```yaml
   apiVersion: external-secrets.io/v1beta1
   kind: ClusterSecretStore
   metadata:
     name: aws-secrets-manager # externalSecret.secretStore
   spec:
     provider:
       aws:
         service: SecretsManager
         region: ap-northeast-1
         auth:
           jwt:
             serviceAccountRef:
               name: tokenhub-tokenhub # the chart's service account
               namespace: tokenhub
   ```

   Grant the role `secretsmanager:GetSecretValue` on `tokenhub/*`, plus `kms:Decrypt` when a customer-managed KMS key is used.

3. `externalSecret.refreshInterval` controls how often the operator re-extracts the secret (5 minutes by default). Rotating values in AWS updates the Kubernetes secret after the next refresh; because the pod reads the values as environment variables, run `kubectl rollout restart` on the deployment afterwards to pick them up.

The remote secret is extracted 1:1 into the chart credentials secret and must provide `TOKENHUB_SECRET_KEY`, `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`, and `TOKENHUB_DATABASE_URL` — the pod creates non-optional environment references for all three, and a missing key leaves the container in `CreateContainerConfigError` after the first sync. Optional keys such as `TOKENHUB_METRICS_TOKEN` are picked up the same way and are consumed by name; to expose an extra key as a pod environment variable, list it in `secretEnv` with an empty value. This mode uses the `external-secrets.io/v1beta1` API and is mutually exclusive with `postgresql.enabled` and `secretEnv` values.

## Values

See the annotated [values.yaml](../deploy/helm/tokenhub/values.yaml) for the full list. Any `TOKENHUB_*` variable documented in [Deployment](deployment.md#backend-environment-variables) can be added through the standard `extraEnv` list and is rendered directly into the pod environment; secret values go through `secretEnv`.

For chart development notes, see the [chart README](../deploy/helm/tokenhub/README.md).
