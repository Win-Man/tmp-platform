# TMP（TiDB 管理平台）构建入口
#
# 常用命令：
#   make build            # 前端构建 + 后端单二进制（bin/tmp，内嵌前端）
#   make backend-build    # 仅编译后端（使用已存在的前端产物）
#   make frontend-build   # 仅构建前端到 backend/web/dist
#   make run              # 构建并以演示模式启动（TMP_MOCK=true）
#   make serve            # 构建并以真实采集模式启动（数据目录=仓库根）
#   make dev-backend      # 后端热启动（不加 mock，连真实集群）
#   make dev-frontend     # 前端开发服务器（代理到 :8080）
#   make test / fmt / vet / clean

SHELL := /bin/bash

# ---- 元信息 ----
APP        := tmp
VERSION    ?= 1.0.1-mvp
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
ROOT       := $(shell pwd)
BIN_DIR    := $(ROOT)/bin
BINARY     := $(BIN_DIR)/$(APP)

GO         ?= go
NPM        ?= npm

LDFLAGS := -s -w \
	-X 'github.com/tmp-platform/backend/internal/config.Version=$(VERSION)' \
	-X 'github.com/tmp-platform/backend/internal/config.BuildTime=$(BUILD_TIME)' \
	-X 'github.com/tmp-platform/backend/internal/config.Commit=$(COMMIT)'

.PHONY: help all build backend-build frontend-build frontend-deps typecheck run run-mock serve dev-backend dev-frontend test fmt vet clean smoke

all: build

help:
	@echo "TMP 构建命令："
	@echo "  make build          前端类型检查 + 构建 + 后端单二进制 -> bin/tmp"
	@echo "  make typecheck      仅前端类型检查（vue-tsc --noEmit）"
	@echo "  make backend-build  仅编译后端（需先 make frontend-build）"
	@echo "  make frontend-build 构建前端到 backend/web/dist"
	@echo "  make run            构建并以演示模式启动（TMP_MOCK=true，:8080）"
	@echo "  make dev-backend    后端直接运行（真实采集模式）"
	@echo "  make dev-frontend   前端开发服务器（:5173，代理 /api → :8080）"
	@echo "  make test/fmt/vet   测试、格式化、静态检查"
	@echo "  make clean          清理产物（保留数据库与 data/）"

# 前端类型检查：与 CI / 本地命令保持一致（package.json 的 typecheck 脚本）
typecheck: frontend-deps
	@echo ">> 前端类型检查（vue-tsc --noEmit）"
	cd frontend && $(NPM) run typecheck

# 前端依赖 + 构建产物（输出到 backend/web/dist，供 //go:embed 打包）
frontend-build: frontend-deps
	@echo ">> 构建前端"
	cd frontend && $(NPM) run build

frontend-deps:
	@if [ ! -d frontend/node_modules ]; then \
		echo ">> 安装前端依赖"; \
		cd frontend && $(NPM) install --no-audit --no-fund; \
	fi

# 单二进制：CGO_ENABLED=0 保证纯 Go、无 glibc 依赖
# 注意：//go:embed dist 要求 backend/web/dist 存在，缺失时自动先构建前端。
backend-build:
	@if [ ! -f backend/web/dist/index.html ]; then \
		echo ">> 未发现前端产物，先执行前端构建"; \
		$(MAKE) frontend-build; \
	fi
	@echo ">> 编译后端（$(VERSION) / $(COMMIT)）"
	cd backend && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/tmp
	@echo ">> 产物：$(BINARY)"

build: typecheck frontend-build backend-build
	@echo ">> 构建完成：$(BINARY)"

# 演示模式启动（无需真实集群）
run: build
	@echo ">> 以演示模式启动（Ctrl-C 退出）"
	TMP_MOCK=true $(BINARY)

run-mock: run

# 真实采集模式启动（连真实集群）
#
# 数据目录必须钉死在仓库根：secret.key 与 tmp.db 是同一条命，
# 换个工作目录启动就会新生成一把密钥，库里已加密的凭据全部作废。
serve: backend-build
	@echo ">> 真实采集模式启动（数据目录=$(ROOT)，地址=$${TMP_ADDR:-:8080}，Ctrl-C 退出）"
	TMP_ADDR=$${TMP_ADDR:-:8080} TMP_DATA_DIR="$(ROOT)" TMP_GIN_MODE=release $(BINARY)

# 后端热启动（数据目录在 backend/data，与 serve 的世界互不相通）
dev-backend:
	cd backend && TMP_GIN_MODE=debug $(GO) run ./cmd/tmp

dev-frontend:
	cd frontend && $(NPM) run dev

test:
	cd backend && CGO_ENABLED=0 $(GO) test ./... 2>&1 | tail -40

fmt:
	cd backend && $(GO) fmt ./...

vet:
	cd backend && CGO_ENABLED=0 $(GO) vet ./...

# 冒烟：构建 → 起服务 → 登录 → 总览 → 集群 → 拓扑 → 巡检 → 报告 → 任务 → 关停
smoke: build
	@bash scripts/smoke.sh

clean:
	rm -rf $(BIN_DIR) backend/web/dist/assets
	@echo ">> 已清理构建产物"
