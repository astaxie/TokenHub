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
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)"
```

The chart composes the database URL from the subchart service. Without an ingress the pod is reachable through port-forwarding (see the install notes). This mode is not sized or tuned for production.

## Production with a managed PostgreSQL

Point `database.url` at a managed PostgreSQL service such as RDS, Cloud SQL, or Azure Database, keep `postgresql.enabled=false`, and enable the ingress:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true \
  --set ingress.host=tokenhub.example.com \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set database.url='postgresql://tokenhub:password@postgres.example.com:5432/tokenhub?sslmode=require' \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16'
```

`TOKENHUB_TRUSTED_PROXY_CIDRS` must cover the ingress controller's source addresses so client IP attribution (rate limits, audit entries) sees real client addresses instead of the controller IP. Private pod/service CIDRs are a reasonable starting point; tighten the list to your cluster.

The first login is username `admin` with the random bootstrap password the chart generated:

```bash
kubectl get secret tokenhub-tokenhub-credentials -o jsonpath='{.data.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD}' | base64 -d
```

Change the password in the admin console after first login; the bootstrap seed only runs on an empty database. Set `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` via `extraEnv` if you want to control it yourself.

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
| Secret | `TOKENHUB_SECRET_KEY`, optional admin credentials, database URL, `secretEnv` entries (when the chart manages them) |
| Service | `api` port (8080) and `console` port (3000) |
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

## Monitoring with PodMonitor

When the cluster runs [Prometheus Operator](https://prometheus-operator.dev/), enable a PodMonitor that scrapes `/metrics` on the API port. The metrics endpoint must be switched on through `extraEnv`:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set 'extraEnv[0].name=TOKENHUB_METRICS_ENABLED' \
  --set 'extraEnv[0].value=true' \
  --set podMonitor.enabled=true \
  --set podMonitor.bearerTokenSecret.name=<secret-with-token> \
  --set podMonitor.bearerTokenSecret.key=TOKENHUB_METRICS_TOKEN
```

The metrics endpoint requires a bearer token: set a dedicated `TOKENHUB_METRICS_TOKEN` (via `secretEnv`) and reference the secret through `podMonitor.bearerTokenSecret`; add `podMonitor.additionalLabels` so your Prometheus installation selects the monitor.

## Credential management with ExternalSecret

When the cluster runs the [External Secrets Operator](https://external-secrets.io/), the chart can fill its credentials secret from an external manager such as Vault or AWS Secrets Manager instead of rendering one itself:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set externalSecret.enabled=true \
  --set externalSecret.secretStore=aws-secrets-manager \
  --set externalSecret.sourceSecretId=tokenhub/prod
```

With AWS Secrets Manager as the example, the three values map to:

1. Store the credentials in one AWS secret whose keys use the `TOKENHUB_*` names:

   ```bash
   aws secretsmanager create-secret --name tokenhub/prod --secret-string '{
     "TOKENHUB_SECRET_KEY": "0123456789abcdef0123456789abcdef",
     "TOKENHUB_DATABASE_URL": "postgresql://tokenhub:password@tokenhub.rds.amazonaws.com:5432/tokenhub?sslmode=require",
     "TOKENHUB_ADMIN_TOKEN": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
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

3. `externalSecret.refreshInterval` controls how often the operator re-extracts the secret (5 minutes by default). Rotating values in AWS takes effect after the next refresh without touching the cluster.

The remote secret is extracted 1:1 into the chart credentials secret and must provide `TOKENHUB_SECRET_KEY`, `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`, and `TOKENHUB_DATABASE_URL`; optional keys such as `TOKENHUB_METRICS_TOKEN` are picked up the same way. This mode uses the `external-secrets.io/v1beta1` API and is mutually exclusive with `postgresql.enabled` and `secretEnv`.

## Values

See the annotated [values.yaml](../deploy/helm/tokenhub/values.yaml) for the full list. Any `TOKENHUB_*` variable documented in [Deployment](deployment.md#backend-environment-variables) can be added through the standard `extraEnv` list and is rendered directly into the pod environment; secret values go through `secretEnv`.

For chart development notes, see the [chart README](../deploy/helm/tokenhub/README.md).
