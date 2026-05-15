.PHONY: dev-up dev-down test vet build sqlc-generate migrate-up migrate-down check-compose logs web-test web-typecheck web-build build-hermes-runtime verify-hermes-runtime debug-ollama debug-newapi newapi-probe seed-e2e smoke-v102 openapi-gen web-types-gen openapi-check

SWAG_VERSION := v2.0.0-rc5
OPENAPI_TS_VERSION := 7.13.0

# hermes runtime 生产镜像构建发布参数。
# HERMES_IMAGE_REPO 默认指向 aliyun 私有 ACR 上的 ywjs_app/oc-manager-hermes,
# 走其他 registry 时在命令行覆盖即可。
HERMES_IMAGE_REPO ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
# HERMES_IMAGE_TAG 必须由调用方显式指定,守门 target 会拒绝空值与 latest,
# 与 deploy/operations.md 4.2 "禁用 latest / 分支 tag / 版本族 tag" 的约束保持一致。
HERMES_IMAGE_TAG ?=
HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_IMAGE_TAG)

# 输入 make 不带参数时, 显式跳到 help target, 输出按分组的可用 target 列表。
.DEFAULT_GOAL := help

# help 通过 awk 解析本 Makefile:
#   - "##@ <title>"   行被识别为分组标题, 以加粗色块输出;
#   - "<target>: ... ## <desc>" 行被识别为可见 target, 列出 target + 描述;
#   - 以 "." 开头的内部 target (如 .guard-hermes-image-tag) 不会进入 help, 因为
#     正则 ^[a-zA-Z] 拒绝 dot-prefix 名字, 守门类 target 不需要暴露给用户。
.PHONY: help
help: ## 显示本帮助文档(make 默认 target)
	@awk 'BEGIN { \
		FS = ":.*##"; \
		printf "\n用法:\n  make \033[36m<target>\033[0m [VAR=VALUE ...]\n"; \
	} \
	/^##@ / { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
	/^[a-zA-Z][a-zA-Z0-9_-]*:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 }' \
	$(MAKEFILE_LIST)

##@ 本地开发

dev-up: ## 启动本地 dev compose (manager + agent + 依赖)
	docker compose up -d

dev-down: ## 停止本地 dev compose 全部容器
	docker compose down

##@ 测试 / 静态检查

test: ## 在 manager-api 容器内跑 Go 单元测试 (go test ./...)
	docker compose run --rm --no-deps manager-api go test ./...

integration-test: ## 在 manager-api 容器内跑集成测试 (go test -tags=integration ./...)
	docker compose run --rm \
		-e INTEGRATION_DATABASE_URL=$${INTEGRATION_DATABASE_URL:-postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable} \
		-e INTEGRATION_REDIS_ADDR=$${INTEGRATION_REDIS_ADDR:-manager-redis:6379} \
		manager-api go test -tags=integration ./...

vet: ## 在 manager-api 容器内跑 go vet ./...
	docker compose run --rm --no-deps manager-api go vet ./...

##@ 构建

build: ## 在 manager-api 容器内编译 server / migrate / oc-runtime-agent 三个二进制到 tmp/build/
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/server ./cmd/server
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/migrate ./cmd/migrate
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/oc-runtime-agent ./runtime/agent

##@ 代码生成 (sqlc / OpenAPI / 前端类型)

sqlc-generate: ## 跑 sqlc generate, 覆盖 internal/store 生成代码
	docker compose run --rm --no-deps manager-api go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

##@ 前端开发

web-test: ## 在 manager-web 容器内跑 vitest 单测
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm test -- --run"

web-typecheck: ## 在 manager-web 容器内跑 vue-tsc --noEmit
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run typecheck"

web-build: ## 在 manager-web 容器内跑 vite build
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run build"

##@ Hermes runtime 镜像

build-hermes-runtime: ## 本地构建 hermes runtime dev 镜像 (tag: hermes-runtime:dev)
	docker build -t hermes-runtime:dev ./runtime/hermes

verify-hermes-runtime: ## 跑 verify-hermes-runtime.sh 校验镜像
	./scripts/verify-hermes-runtime.sh

# .guard-hermes-image-tag 校验生产 tag 必须显式指定且不能是 latest;
# build / push / release 三个 hermes 生产镜像 target 都先经过它,避免误推 latest 或空 tag。
# dot-prefix 命名让 help target 的 awk 正则不会把它列出, 是隐藏的内部 target。
.PHONY: .guard-hermes-image-tag
.guard-hermes-image-tag:
	@if [ -z "$(HERMES_IMAGE_TAG)" ]; then \
	  echo "❌ HERMES_IMAGE_TAG 必须显式指定,例如: make release-hermes-image HERMES_IMAGE_TAG=v1.0.0"; \
	  exit 1; \
	fi
	@if [ "$(HERMES_IMAGE_TAG)" = "latest" ]; then \
	  echo "❌ 生产禁用 latest tag,使用具体版本号(参见 deploy/operations.md 4.2)"; \
	  exit 1; \
	fi

# build-hermes-image 在本地构建 hermes runtime 生产镜像,直接打上 aliyun ACR 完整 tag,
# 不推送,便于发布前先在本地跑 verify-hermes-runtime 等校验脚本。
.PHONY: build-hermes-image
build-hermes-image: .guard-hermes-image-tag ## 本地构建 hermes runtime 生产镜像, 打上 aliyun ACR tag (需 HERMES_IMAGE_TAG)
	docker build -t $(HERMES_IMAGE) ./runtime/hermes

# push-hermes-image 推送已构建的 hermes runtime 生产镜像; 构建步骤独立,
# 方便在 ACR 凭据未就绪 / verify 未通过时只 build 不 push。
.PHONY: push-hermes-image
push-hermes-image: .guard-hermes-image-tag ## 推送 hermes runtime 生产镜像到 aliyun ACR (需 HERMES_IMAGE_TAG)
	docker push $(HERMES_IMAGE)

# release-hermes-image 一步完成本地构建 + 推送,是日常发版入口;
# 推送完成后输出最终镜像引用,方便复制到 deploy/manage/config/manager.yaml 的 hermes.runtime_image。
.PHONY: release-hermes-image
release-hermes-image: build-hermes-image push-hermes-image ## 本地构建并推送 hermes runtime 生产镜像 (需 HERMES_IMAGE_TAG)
	@echo "✅ hermes 镜像 $(HERMES_IMAGE) 已构建并推送"

##@ 调试脚本

debug-ollama: ## 跑 debug-ollama.sh, 探测 ollama 状态
	./scripts/debug-ollama.sh

debug-newapi: ## 跑 debug-newapi.sh, 探测 new-api 状态
	./scripts/debug-newapi.sh

newapi-probe: ## 跑 newapi-probe.sh, 用最小请求探测 new-api 渠道
	@bash scripts/newapi-probe.sh

##@ 数据库迁移

migrate-up: ## 在 manager-api 容器内对生产数据库执行 up 迁移
	docker compose run --rm manager-api go run ./cmd/migrate up

migrate-down: ## 在 manager-api 容器内回滚最近一次迁移
	docker compose run --rm manager-api go run ./cmd/migrate down

##@ 部署 / 运维 / Smoke

check-compose: ## 跑 check-compose-bind-mounts.sh, 校验 compose 挂载是否合法
	./scripts/check-compose-bind-mounts.sh

logs: ## 持续 tail 所有 compose 服务日志 (最近 200 行)
	docker compose logs -f --tail=200

# seed-e2e：在 manager-api 容器里跑 cmd/seed-e2e，OCM_E2E=1 守门。
# 会 TRUNCATE e2e 业务表后重建 fixture，stdout 末行是 fixture JSON 供 Playwright 解析。
seed-e2e: ## 在 manager-api 容器内注入 Playwright e2e fixture (OCM_E2E=1 守门)
	docker compose run --rm -e OCM_E2E=1 manager-api go run ./cmd/seed-e2e

smoke-v102:  ## 跑 v1.0.2 干净环境 smoke（前置：阶段 0 完成）
	@bash scripts/v102-smoke.sh

##@ OpenAPI / 前端类型 (与代码生成段相互引用)

.PHONY: openapi-gen
openapi-gen: ## 后端注解扫描，覆盖 openapi/openapi.yaml
	go run github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION) init \
		--generalInfo main.go \
		--dir cmd/server,internal/api/handlers,internal/service,internal/domain \
		--output openapi \
		--outputTypes yaml \
		--v3.1
	@mv openapi/swagger.yaml openapi/openapi.yaml

.PHONY: web-types-gen
web-types-gen: ## 前端从 yaml 生成 TypeScript 类型
	cd web && npx openapi-typescript@$(OPENAPI_TS_VERSION) ../openapi/openapi.yaml -o src/api/generated.ts

.PHONY: openapi-check
openapi-check: openapi-gen ## 校验 yaml 是否与代码同步（git 工作区干净才过）
	@git diff --exit-code openapi/openapi.yaml \
		|| (echo "❌ openapi/openapi.yaml 与代码不同步，请跑 make openapi-gen 并 commit"; exit 1)
	@echo "✅ openapi.yaml 与代码同步"
