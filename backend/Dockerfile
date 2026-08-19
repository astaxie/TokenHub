ARG NODE_IMAGE=node:22.23.1-bookworm-slim

FROM ${NODE_IMAGE} AS frontend-deps

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

FROM ${NODE_IMAGE} AS frontend-builder

WORKDIR /src/frontend
COPY --from=frontend-deps /src/frontend/node_modules ./node_modules
COPY frontend/ ./
RUN npm run build && \
    server_path="$(find .next/standalone -type f -name server.js -exec sh -c \
      'for candidate do candidate_root=${candidate%/*}; if [ -d "$candidate_root/.next" ] && [ -f "$candidate_root/package.json" ]; then echo "$candidate"; fi; done' \
      sh {} +)" && \
    test -n "$server_path" && \
    test "$(printf '%s\n' "$server_path" | wc -l)" -eq 1 && \
    standalone_root="$(dirname "$server_path")" && \
    mkdir -p /out/frontend/.next && \
    cp -a "$standalone_root/." /out/frontend/ && \
    cp -a .next/static /out/frontend/.next/static && \
    cp -a public /out/frontend/public && \
    test -f /out/frontend/server.js

FROM golang:1.26-alpine AS backend-builder

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ARG TOKENHUB_VERSION=0.6.0
ARG TOKENHUB_BUILD_TYPE=source
ENV GOPROXY=$GOPROXY
RUN apk add --no-cache gcc musl-dev
COPY backend/go.mod ./backend/go.mod
COPY backend/go.sum ./backend/go.sum
COPY backend/cmd ./backend/cmd
COPY backend/internal ./backend/internal
RUN cd backend && \
    case "$TOKENHUB_BUILD_TYPE" in source|release) ;; *) exit 1 ;; esac && \
    CGO_ENABLED=1 go build \
      -tags "netgo osusergo" \
      -trimpath \
      -ldflags="-s -w -linkmode external -extldflags '-static' -X main.buildVersion=${TOKENHUB_VERSION} -X main.buildType=${TOKENHUB_BUILD_TYPE} -X main.deploymentType=container" \
      -o /out/tokenhub ./cmd/tokenhub

FROM ${NODE_IMAGE}

ARG TOKENHUB_VERSION=0.6.0
LABEL org.opencontainers.image.version="${TOKENHUB_VERSION}"

COPY --from=backend-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN command -v bash >/dev/null && \
    command -v setpriv >/dev/null && \
    test -s /etc/ssl/certs/ca-certificates.crt

ENV NODE_ENV=production \
    TOKENHUB_DEPLOYMENT_TYPE=container \
    TOKENHUB_INSTALL_ROOT=/opt/tokenhub \
    TOKENHUB_MODEL_CATALOG_FILE=/opt/tokenhub/current/catalog/model-catalog.yaml \
    TOKENHUB_PROVIDER_CATALOG_FILE=/opt/tokenhub/current/catalog/provider-catalog.json \
    TOKENHUB_RUN_MODE=all

WORKDIR /app
COPY --from=backend-builder --chown=node:node /out/tokenhub /usr/local/lib/tokenhub-image/bin/tokenhub
COPY --from=frontend-builder --chown=node:node /usr/local/bin/node /usr/local/lib/tokenhub-image/bin/node
COPY --from=frontend-builder --chown=node:node /out/frontend /usr/local/lib/tokenhub-image/frontend
COPY --chown=node:node deploy/native/tokenhub-run /usr/local/lib/tokenhub-image/bin/tokenhub-run
COPY --chown=node:node deploy/native/tokenhub.service /usr/local/lib/tokenhub-image/deploy/tokenhub.service
COPY --chown=node:node data/model-catalog.yaml /usr/local/lib/tokenhub-image/catalog/model-catalog.yaml
COPY --chown=node:node data/provider-catalog.json /usr/local/lib/tokenhub-image/catalog/provider-catalog.json
COPY deploy/container/tokenhub-build-id /usr/local/bin/tokenhub-build-id
COPY deploy/container/tokenhub-entrypoint /usr/local/bin/tokenhub-entrypoint

RUN printf '%s\n' "$TOKENHUB_VERSION" >/usr/local/lib/tokenhub-image/VERSION && \
    chmod 0755 \
      /usr/local/bin/tokenhub-build-id \
      /usr/local/bin/tokenhub-entrypoint \
      /usr/local/lib/tokenhub-image/bin/node \
      /usr/local/lib/tokenhub-image/bin/tokenhub \
      /usr/local/lib/tokenhub-image/bin/tokenhub-run && \
    tokenhub-build-id /usr/local/lib/tokenhub-image && \
    chown -R node:node /usr/local/lib/tokenhub-image && \
    mkdir -p /app/data /opt/tokenhub && \
    chown node:node /app/data /opt/tokenhub

EXPOSE 8080 3000
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=12 \
  CMD node -e "Promise.all([fetch('http://127.0.0.1:8080/healthz'),fetch('http://127.0.0.1:3000')]).then(rs=>process.exit(rs.every(r=>r.ok)?0:1)).catch(()=>process.exit(1))"

ENTRYPOINT ["/usr/local/bin/tokenhub-entrypoint"]
