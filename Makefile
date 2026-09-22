# 乐云企业网盘 · 开发与构建
#
# 日常部署请直接用 ./deploy.sh 与 ./update.sh，这里的目标面向本地开发。

VERSION ?= 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := bin/leyun

.PHONY: help
help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: web
web: ## 构建前端（产物落到 internal/web/dist）
	cd web && npm install --no-audit --no-fund && npm run build

.PHONY: build
build: ## 构建后端（需先 make web，否则前端是占位页）
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/leyun

.PHONY: all
all: web build ## 前端 + 后端一起构建

.PHONY: run
run: ## 本地启动后端（默认 8080）
	go run ./cmd/leyun -config config.yaml

.PHONY: dev
dev: ## 启动前端开发服务器（接口代理到本机 8080）
	cd web && npm run dev

.PHONY: test
test: ## 运行 Go 测试
	go test ./...

.PHONY: test-v
test-v: ## 运行 Go 测试（详细输出）
	go test -v ./internal/...

.PHONY: lint
lint: ## 静态检查
	gofmt -l . | grep -v '^web/' | (! grep .) || (echo "上述文件需要 gofmt" && exit 1)
	go vet ./...
	cd web && npm run typecheck

.PHONY: docker
docker: ## 构建 Docker 镜像
	docker build -t leyun:$(VERSION) --build-arg VERSION=$(VERSION) .

.PHONY: clean
clean: ## 清理构建产物（不动 data/）
	rm -rf bin/ internal/web/dist/assets internal/web/dist/index.html web/dist

.PHONY: release
release: web ## 交叉编译 linux/darwin/windows amd64+arm64
	@mkdir -p dist
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  → $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags "$(LDFLAGS)" -o dist/leyun-$$os-$$arch$$ext ./cmd/leyun; \
	done
	@echo "产物在 dist/"
