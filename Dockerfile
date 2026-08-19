# syntax=docker/dockerfile:1
#
# Multi-stage build for the white-box multi-agent platform.
#
# Stage layout:
#   1. web-v1 / web-v2  — build the two Vue frontends (go:embed consumes their dist/)
#   2. go-build        — CGO_ENABLED=0 static build of the server binary (frontends embedded)
#   3. runtime         — minimal alpine image, non-root, /healthz healthcheck
#
# Notes:
#   - modernc.org/sqlite is pure-Go (CGO_ENABLED=0), so the binary is fully static.
#   - git is installed because the worktree feature (WORKTREE_ENABLED) shells out to `git`.
#     If you disable worktree (default off) and the Docker sandbox, git can be removed.
#   - SQLite is single-writer: run ONE replica with a persistent volume (see deploy/k8s).

# ---------------------------------------------------------------------------
# Frontend v1
# ---------------------------------------------------------------------------
FROM node:22-bookworm-slim AS web-v1
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# Frontend v2 (default UI, served at /)
# ---------------------------------------------------------------------------
FROM node:22-bookworm-slim AS web-v2
WORKDIR /src/web-v2
COPY web/v2/package.json web/v2/package-lock.json* ./
RUN npm ci
COPY web/v2/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# Go server build (frontends embedded via go:embed)
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS go-build
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Source (node_modules excluded via .dockerignore).
COPY . .

# Overlay freshly built frontends over any stale local dist.
COPY --from=web-v1 /src/web/dist ./web/dist
COPY --from=web-v2 /src/web-v2/dist ./web/v2/dist

RUN go build -trimpath -ldflags "-s -w" -o /out/server ./cmd/server

# ---------------------------------------------------------------------------
# Runtime
# ---------------------------------------------------------------------------
FROM alpine:3.20 AS runtime

# ca-certificates: outbound HTTPS to LLM providers.
# git: worktree feature shells out to `git` (no-op if WORKTREE_ENABLED=false).
# tzdata: sane timestamps in logs/audit.
RUN apk add --no-cache ca-certificates git tzdata \
    && addgroup -S app \
    && adduser -S app -G app

WORKDIR /app

COPY --from=go-build /out/server /app/server
COPY .env.example /app/.env.example

# Runtime configuration (overridable via `docker run -e`).
ENV SERVER_PORT=8080 \
    DB_PATH=/data/app.db \
    LLM_USE_MOCK=false \
    LOG_LEVEL=info

EXPOSE 8080
VOLUME ["/data"]

# Non-root execution.
USER app

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/app/server"]
CMD ["--port", "8080"]
