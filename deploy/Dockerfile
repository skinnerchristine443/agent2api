# ---- 前端 ----
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --cache /tmp/npm-cache
COPY frontend/ ./
RUN NODE_OPTIONS="--max-old-space-size=1536" npm run build

# ---- go 控制面 ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG APP_VERSION=dev
ARG APP_COMMIT=unknown
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /web/dist/ ./internal/webui/static/
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -ldflags "-s -w -X agent2api/internal/buildinfo.Version=${APP_VERSION} -X agent2api/internal/buildinfo.Commit=${APP_COMMIT}" -o /out/agent2api ./cmd/server \
  && CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -ldflags "-s -w -X agent2api/internal/buildinfo.Version=${APP_VERSION} -X agent2api/internal/buildinfo.Commit=${APP_COMMIT}" -o /out/agent2api-updater ./cmd/updater

# ---- 单一运行时 ----
# 不含 Node.js：所有 provider 完全在进程内运行（纯 Go）。
FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl tini \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/agent2api /app/agent2api
COPY --from=build /out/agent2api-updater /app/agent2api-updater
# 非 root 运行（审查 T48）：固定 UID/GID=10001。命名卷首次挂载会按
# 镜像内 /data 的属主初始化（Docker 文档行为）；root 时代创建的既有卷
# 需一次性 chown——见 docker-compose.yml 注记。数字 UID 无需 passwd 条目。
RUN mkdir -p /data && chown 10001:10001 /data && chmod 700 /data
ENV HOST=0.0.0.0 \
    PORT=3010 \
    AGENT2API_DATA_DIR=/data \
    AGENT2API_RUNTIME_DIR=/run/agent2api
EXPOSE 3010
USER 10001:10001
ENTRYPOINT ["/usr/bin/tini","--"]
CMD ["/app/agent2api"]
