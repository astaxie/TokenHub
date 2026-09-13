# TokenHub Helm chart

Deploys the single TokenHub container image in `all` run mode: one pod serves
the Go API and gateway on port 8080 and the Next.js admin console on port 3000.
See [docs/kubernetes.md](../../../docs/kubernetes.md) for the user guide
(routing table, health model, stateless plugin behavior, upgrades); this README
covers chart maintenance.

## Quick test (built-in PostgreSQL)

```bash
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY=$(openssl rand -hex 32)
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
- Required values (`database`, `secretEnv.TOKENHUB_SECRET_KEY`) fail template rendering with
  actionable messages instead of installing placeholders.
- `TOKENHUB_MANAGED_UPDATES` is forced off: upgrades happen by changing
  `image.tag` and rolling pods.
