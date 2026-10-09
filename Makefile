.PHONY: help fmt test test-race check-coverage vet vet-live lint build dev sync webui-check favicon-sync start docker-build docker-up docker-down docker-logs frontend-deps frontend-lint frontend-test frontend-conventions frontend-build check

help: ## 显示此帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ── Go ──────────────────────────────────────────────────────────────

fmt: ## 检查 Go 格式（gofmt -l 必须为空，对齐 CI）
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

test: ## 运行 Go 测试（-count=1 对齐验收纪律）
	go test -count=1 ./...

test-race: ## 运行 Go 测试（race 检测器；需 cgo 与 C 工具链）
	go test -race -count=1 ./...

check-coverage: ## 关键包覆盖率下限守卫（见 scripts/check-coverage.sh）
	./scripts/check-coverage.sh

vet: ## 运行 Go vet
	go vet ./...

vet-live: ## 用 live 标签运行 Go vet（live 测试的类型检查门）
	go vet -tags live ./...

build: ## 将 gateway 与 updater 二进制构建到 bin/
	CGO_ENABLED=0 go build -trimpath -o bin/agent2api ./cmd/server
	CGO_ENABLED=0 go build -trimpath -o bin/agent2api-updater ./cmd/updater

# ── 前端 ────────────────────────────────────────────────────────

frontend-deps: ## 安装前端依赖
	cd frontend && npm ci

frontend-lint: ## 对前端执行 lint
	cd frontend && npm run lint

frontend-test: ## 运行前端单元测试（vitest）
	cd frontend && npm test

frontend-conventions: ## 校验前端样式/层级约定（字号、颜色字面量、超长 className、pages 值导入；存量见 baseline）
	cd frontend && node ./scripts/check-conventions.mjs

frontend-build: ## 构建前端
	cd frontend && NODE_OPTIONS="--max-old-space-size=1536" npm run build

sync: frontend-build ## 构建前端并把内嵌资源同步进 Go（单次构建）
	cd frontend && node ./scripts/sync-static.mjs

webui-check: frontend-build ## 校验内嵌资源与前端构建一致（对齐 CI verify）
	cd frontend && node ./scripts/sync-static.mjs
	git diff --exit-code -- internal/webui/static

favicon-sync: sync ## 将 favicon 套件同步进内嵌的 webui 静态资源
	@echo "synced: frontend/public -> internal/webui/static"
	@ls -1 frontend/public/favicon*.svg frontend/public/apple-touch-icon.svg frontend/public/og-card.svg frontend/public/site.webmanifest 2>/dev/null

# ── Docker ──────────────────────────────────────────────────────────

docker-build: ## 从源码构建容器镜像
	cd deploy && docker compose up -d --build

docker-up: ## 启动服务（拉取预构建镜像）
	cd deploy && docker compose pull && docker compose up -d

docker-down: ## 停止服务
	cd deploy && docker compose down

docker-logs: ## 跟踪服务日志
	cd deploy && docker compose logs -f agent2api

# ── 聚合 ───────────────────────────────────────────────────────

lint: vet frontend-lint ## 运行所有 linter

check: fmt lint build vet-live test check-coverage frontend-test frontend-conventions webui-check ## 对齐 CI verify（不含双 OS 测试矩阵与 cross-build）

start: ## 启动 Docker 部署并打印首次运行的 API key
	./scripts/start.sh

dev: ## 在本地运行 Go 服务端（需要 .env 或环境变量）
	go run ./cmd/server

