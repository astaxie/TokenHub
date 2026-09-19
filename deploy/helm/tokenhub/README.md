# TokenHub Helm chart

Deploys the single TokenHub container image in `all` run mode: one pod serves
the Go API and gateway on port 8080 and the Next.js admin console on port 3000.
See [docs/kubernetes.md](../../../docs/kubernetes.md) for the user guide
(routing table, health model, stateless plugin behavior, upgrades); this README
covers chart maintenance.

## Quick test (built-in PostgreSQL)

```bash
# Chart.lock pins the subchart but does not register its repository.
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY=$(openssl rand -hex 32) \
  --set secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=$(openssl rand -hex 16)
```

The chart composes the database URL from the bitnami/postgresql subchart
service. This mode exists for throwaway clusters and smoke tests only.

## Production (managed PostgreSQL)

Point `database.url` (or `database.existingSecret`) at a managed PostgreSQL
service such as RDS or Cloud SQL, leave `postgresql.enabled=false`, and set
`ingress.enabled=true` with `ingress.host`. The backend runs schema AutoMigrate
on pod start; take a PostgreSQL backup before upgrading the image tag.

Set `TOKENHUB_TRUSTED_PROXY_CIDRS` (via `extraEnv`) to the ingress controller's CIDR so
client IP attribution is correct.

## Chart notes

- Any `TOKENHUB_*` variable from docs/deployment.md can be added via the
  standard `extraEnv` list; secret values go through `secretEnv` and are
  rendered into the chart-managed secret.
- Required values (`database`, `secretEnv.TOKENHUB_SECRET_KEY`,
  `secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`) fail template rendering with
  actionable messages instead of installing placeholders.
- The pod environment defaults to `TOKENHUB_ENV=prod` (via `environment`) so
  the image's development admin token is rejected; `TOKENHUB_API_BASE_URL` is
  derived from the ingress scheme and host, or set through `apiBaseUrl`.
- Changing a chart-managed secret rolls the pods through a checksum
  annotation; externally managed secrets need a manual `kubectl rollout
  restart` after rotation.
- Generated image bytes require shared storage across replicas
  (`imageStorage.type=pvc` or `existingClaim`); the ephemeral default is for
  single test replicas.
- Overlapping replicas also need a backend image with instance-scoped
  image-job recovery. The published `0.8.0` image predates that fix: keep
  `replicaCount=1` with it, or pin `image.tag` to a later release.
- `TOKENHUB_MANAGED_UPDATES` is forced off: upgrades happen by changing
  `image.tag` and rolling pods.
