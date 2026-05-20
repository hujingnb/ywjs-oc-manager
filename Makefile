.PHONY: dev-up dev-down test vet build sqlc-generate migrate-up migrate-down check-compose logs web-test web-typecheck web-build build-hermes-runtime hermes-inject-contract debug-ollama debug-newapi newapi-probe seed-e2e smoke-v102 openapi-gen web-types-gen openapi-check ssh-manager ssh-agent1 ssh-newapi logs-api logs-agent1 psql-manager redis-manager bh-logs

# 加载 .env（-include 在文件不存在时静默跳过，不报错）。
# docker compose 会自动读取 .env，Makefile 显式 include 是为了让 SSH 等 target 也能访问其中变量。
-include .env

SWAG_VERSION := v2.0.0-rc5
OPENAPI_TS_VERSION := 7.13.0

# 统一镜像 tag：取 make 调用时的本地时间，格式 YYYY-MM-DD-HH-MM-SS。
# 同一次 make 调用中所有生产镜像共享同一时间戳，避免同批镜像 tag 不一致。
IMAGE_TIMESTAMP := $(shell date +%Y-%m-%d-%H-%M-%S)

# 各服务生产镜像仓库（统一走 aliyun 私有 ACR ywjs_app 命名空间）。
# 走其他 registry 时在命令行覆盖对应变量即可。
API_IMAGE_REPO   ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-api
AGENT_IMAGE_REPO ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-agent
WEB_IMAGE_REPO   ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-web

# hermes runtime 生产镜像仓库，与上方三个服务保持一致命名风格。
# HERMES_VARIANT 选择 runtime/hermes/ 下的 versioned variant 子目录（自包含 Dockerfile + 资产）。
# 镜像 tag 从该 variant 的 version.txt 派生，禁止 main / master / latest / dev 等浮动 ref。
HERMES_VARIANT       ?= hermes-v2026.5.16
HERMES_VARIANT_DIR   := runtime/hermes/$(HERMES_VARIANT)
override HERMES_VERSION := $(strip $(shell if [ -f "$(HERMES_VARIANT_DIR)/version.txt" ]; then cat "$(HERMES_VARIANT_DIR)/version.txt"; fi))
HERMES_IMAGE_REPO    ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
# hermes tag 形如 v2026.5.16-2026-05-21-12-00-00，便于从镜像引用直接看出上游版本。
override HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TIMESTAMP)

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

.PHONY: .guard-hermes-version
.guard-hermes-version:
	@test -f "$(HERMES_VARIANT_DIR)/version.txt" || { echo "Hermes variant 缺少 version.txt: $(HERMES_VARIANT_DIR)/version.txt" >&2; exit 1; }
	@test -n "$(HERMES_VERSION)" || { echo "Hermes version 不能为空: $(HERMES_VARIANT_DIR)/version.txt" >&2; exit 1; }
	@case "$(HERMES_VERSION)" in \
		main|master|latest|dev|*hermes-main*) echo "Hermes version 不能使用浮动或旧 variant tag: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
	@case "$(HERMES_VERSION)" in \
		*[!A-Za-z0-9_.-]*) echo "Hermes version 包含非法镜像 tag 字符: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
	@case "$(HERMES_VERSION)" in \
		.*|-*) echo "Hermes version 不能以 Docker tag 非法起始字符开头: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
	@version="$(HERMES_VERSION)"; test $${#version} -le 108 || { echo "Hermes version 过长，无法为生产时间戳预留 Docker tag 长度: $(HERMES_VERSION)" >&2; exit 1; }

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

hermes-inject-contract: .guard-hermes-version ## 把 HERMES_VARIANT 指定变体的契约工件注入目录
	rm -rf $(HERMES_VARIANT_DIR)/kanban-contract
	cp -r runtime/hermes/kanban-contract $(HERMES_VARIANT_DIR)/kanban-contract
	rm -rf $(HERMES_VARIANT_DIR)/cron-contract
	cp -r runtime/hermes/cron-contract $(HERMES_VARIANT_DIR)/cron-contract

build-hermes-runtime: hermes-inject-contract ## 本地 dev 构建 hermes runtime（需 HERMES_VARIANT 指定变体）
	status=0; \
	docker build \
	  -t "hermes-runtime:$(HERMES_VERSION)-dev" \
	  --build-arg "HERMES_REF=$(HERMES_VERSION)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
	rm -rf $(HERMES_VARIANT_DIR)/kanban-contract $(HERMES_VARIANT_DIR)/cron-contract; \
	exit $$status

##@ 镜像构建发布

# api / agent 构建上下文为仓库根目录（需访问 go.mod / internal/ 等源码），
# web 构建上下文为 web/ 子目录（前端工程相对自包含，无需整个仓库）。
# 同一次 make 调用中四个服务共享 IMAGE_TIMESTAMP，保证同批镜像 tag 一致。

.PHONY: build-api-image
build-api-image: ## 本地构建 manager-api 生产镜像，tag 取当前时间戳
	docker build -t $(API_IMAGE_REPO):$(IMAGE_TIMESTAMP) -f cmd/server/Dockerfile .

.PHONY: push-api-image
push-api-image:
	docker push $(API_IMAGE_REPO):$(IMAGE_TIMESTAMP)

.PHONY: release-api-image
release-api-image: build-api-image push-api-image ## 构建并推送 manager-api 生产镜像
	@echo "✅ manager-api 镜像 $(API_IMAGE_REPO):$(IMAGE_TIMESTAMP) 已构建并推送"

.PHONY: build-agent-image
build-agent-image: ## 本地构建 runtime-agent 生产镜像，tag 取当前时间戳
	docker build -t $(AGENT_IMAGE_REPO):$(IMAGE_TIMESTAMP) -f runtime/agent/Dockerfile .

.PHONY: push-agent-image
push-agent-image:
	docker push $(AGENT_IMAGE_REPO):$(IMAGE_TIMESTAMP)

.PHONY: release-agent-image
release-agent-image: build-agent-image push-agent-image ## 构建并推送 runtime-agent 生产镜像
	@echo "✅ runtime-agent 镜像 $(AGENT_IMAGE_REPO):$(IMAGE_TIMESTAMP) 已构建并推送"

.PHONY: build-web-image
build-web-image: ## 本地构建 manager-web 生产镜像，tag 取当前时间戳
	docker build -t $(WEB_IMAGE_REPO):$(IMAGE_TIMESTAMP) -f web/Dockerfile ./web

.PHONY: push-web-image
push-web-image:
	docker push $(WEB_IMAGE_REPO):$(IMAGE_TIMESTAMP)

.PHONY: release-web-image
release-web-image: build-web-image push-web-image ## 构建并推送 manager-web 生产镜像
	@echo "✅ manager-web 镜像 $(WEB_IMAGE_REPO):$(IMAGE_TIMESTAMP) 已构建并推送"

# build-hermes-image 在本地构建 hermes runtime 生产镜像，直接打上 aliyun ACR 完整 tag，
# 不推送，便于发布前先在本地完成构建期自检。
# build context 取自 HERMES_VARIANT 指向的子目录（自包含 Dockerfile + 资产）。
.PHONY: build-hermes-image
build-hermes-image: hermes-inject-contract ## 本地构建 hermes runtime 生产镜像（需 HERMES_VARIANT 指定变体）
	status=0; \
	docker build \
	  -t "$(HERMES_IMAGE)" \
	  --build-arg "HERMES_REF=$(HERMES_VERSION)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
	rm -rf $(HERMES_VARIANT_DIR)/kanban-contract $(HERMES_VARIANT_DIR)/cron-contract; \
	exit $$status

# push-hermes-image 仅供 release-hermes-image 在同一次 make 调用内复用；
# IMAGE_TIMESTAMP 每次 make 会重算，独立执行 push 可能找不到刚构建的 tag。
.PHONY: push-hermes-image
push-hermes-image: .guard-hermes-version
	docker push "$(HERMES_IMAGE)"

# release-hermes-image 一步完成本地构建 + 推送，是日常发版入口；
# 推送完成后输出最终镜像引用，方便复制到 deploy/manage/config/manager.yaml 的 hermes.runtime_image。
.PHONY: release-hermes-image
release-hermes-image: build-hermes-image push-hermes-image ## 构建并推送 hermes runtime 生产镜像（需 HERMES_VARIANT 指定变体）
	@echo "✅ hermes 镜像 $(HERMES_IMAGE) 已构建并推送"

# deploy-api / deploy-web / deploy-agent：一键完成本地构建推送 + 远程更新部署。
# 远程步骤：sed 原地更新 .env 中的镜像变量 → docker compose pull → docker compose up -d。
# SSH 凭据复用 PROD_MANAGER_SSH_* / PROD_AGENT1_SSH_* 变量（从 .env 加载）。
# deploy-agent 无法直接访问 agent 服务器，经由 manager 内网跳转，与 ssh-agent1 相同。

.PHONY: deploy-all
deploy-all: deploy-api deploy-web deploy-agent ## 构建推送全部服务镜像并部署（api + web + agent）

.PHONY: deploy-api
deploy-api: release-api-image ## 构建推送 manager-api 并部署到 manage 服务器（更新 .env + compose up）
	sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
		-p $(PROD_MANAGER_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		$(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
		"cd /opt/oc-manage \
		 && sed -i 's|OCM_MANAGER_IMAGE=.*|OCM_MANAGER_IMAGE=$(API_IMAGE_REPO):$(IMAGE_TIMESTAMP)|' .env \
		 && docker compose pull \
		 && docker compose up -d"

.PHONY: deploy-web
deploy-web: release-web-image ## 构建推送 manager-web 并部署到 manage 服务器（更新 .env + compose up）
	sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
		-p $(PROD_MANAGER_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		$(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
		"cd /opt/oc-manage \
		 && sed -i 's|OCM_WEB_IMAGE=.*|OCM_WEB_IMAGE=$(WEB_IMAGE_REPO):$(IMAGE_TIMESTAMP)|' .env \
		 && docker compose pull \
		 && docker compose up -d"

.PHONY: deploy-agent
deploy-agent: release-agent-image ## 构建推送 runtime-agent 并部署到 agent 服务器（经 manager 跳转，更新 .env + compose up）
	sshpass -p "$(PROD_AGENT1_SSH_PASS)" ssh \
		-p $(PROD_AGENT1_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-o "ProxyCommand=sshpass -p '$(PROD_MANAGER_SSH_PASS)' ssh -W %h:%p -p $(PROD_MANAGER_SSH_PORT) -o StrictHostKeyChecking=no $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST)" \
		$(PROD_AGENT1_SSH_USER)@$(PROD_AGENT1_SSH_HOST) \
		"cd /opt/runtime-agent \
		 && sed -i 's|OC_RUNTIME_AGENT_IMAGE=.*|OC_RUNTIME_AGENT_IMAGE=$(AGENT_IMAGE_REPO):$(IMAGE_TIMESTAMP)|' .env \
		 && docker compose pull \
		 && docker compose up -d"

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
	docker compose logs
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

##@ 运维 SSH

# ssh-manager 直接 SSH 到 manager 公网 IP，端口和密码从 .env 读取。
ssh-manager: ## SSH 连接线上 manager 服务器（需 .env 中配置 PROD_MANAGER_SSH_* 变量）
	sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
		-p $(PROD_MANAGER_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-t $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
		"cd /opt/oc-manage && exec bash -l"

# ssh-agent1 无法直接访问，先以 manager 为跳板通过内网连接 agent-1。
# ProxyCommand 负责建立到 manager 的 SSH 隧道，外层 sshpass 再认证 agent-1。
ssh-agent1: ## SSH 连接线上 agent-1（经由 manager 内网跳转，需 .env 中配置 PROD_MANAGER_SSH_* 和 PROD_AGENT1_SSH_* 变量）
	sshpass -p "$(PROD_AGENT1_SSH_PASS)" ssh \
		-p $(PROD_AGENT1_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-o "ProxyCommand=sshpass -p '$(PROD_MANAGER_SSH_PASS)' ssh -W %h:%p -p $(PROD_MANAGER_SSH_PORT) -o StrictHostKeyChecking=no $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST)" \
		-t $(PROD_AGENT1_SSH_USER)@$(PROD_AGENT1_SSH_HOST) \
		"cd /opt/runtime-agent && exec bash -l"

# ssh-newapi 直接 SSH 到线上 new-api 服务器（独立公网 IP，与 manager 不同台），无需经过 manager 跳转。
ssh-newapi: ## SSH 连接线上 new-api 服务器（需 .env 中配置 PROD_NEWAPI_SSH_* 变量）
	sshpass -p "$(PROD_NEWAPI_SSH_PASS)" ssh \
		-p $(PROD_NEWAPI_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-t $(PROD_NEWAPI_SSH_USER)@$(PROD_NEWAPI_SSH_HOST) \
		"exec bash -l"

# psql-manager 在 manage 服务器上进入 manager-postgres 容器的交互式 psql。
# 密码通过 source .env 读取服务器上的 MANAGER_POSTGRES_PASSWORD，
# 再以 PGPASSWORD 环境变量注入 docker compose exec，避免明文出现在命令行。
psql-manager: ## SSH 进入线上 Postgres 交互式 psql
	sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
		-p $(PROD_MANAGER_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-t $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
		"cd /opt/oc-manage && source .env && docker compose exec -e PGPASSWORD=\$$MANAGER_POSTGRES_PASSWORD manager-postgres psql -U \$$MANAGER_POSTGRES_USER -d \$$MANAGER_POSTGRES_DB"

# redis-manager 在 manage 服务器上进入 manager-redis 容器的交互式 redis-cli。
# manager-redis 容器已通过 docker-compose.yml 将 REDISCLI_AUTH 注入容器环境变量，
# redis-cli 自动读取该变量完成认证，无需额外传密码。
redis-manager: ## SSH 进入线上 Redis 交互式 redis-cli
	sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
		-p $(PROD_MANAGER_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-t $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
		"cd /opt/oc-manage && docker compose exec manager-redis redis-cli"

# bh-logs 抓取 manager-api 和 runtime-agent 两个容器中的 [hujingnb] 调试日志。
# 配合 bug-hunting skill 使用：加完调试日志并部署后，用此命令一次性拿回全部标记行，
# 再将输出粘贴给 bug-hunting skill 做阶段 B 分析。
# tee 同时输出到终端和 /tmp/oc-debug-logs.txt，再用 xclip 复制到剪切板。
# 依赖：xclip（apt install xclip）。
bh-logs: ## 抓取线上 [hujingnb] 调试日志并复制到剪切板（用于 bug-hunting 分析）
	@{ \
		echo "===== manager-api ====="; \
		sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
			-p $(PROD_MANAGER_SSH_PORT) \
			-o StrictHostKeyChecking=no \
			$(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
			"cd /opt/oc-manage && docker compose logs manager-api 2>&1 | grep hujingnb"; \
		echo "===== agent-1 ====="; \
		sshpass -p "$(PROD_AGENT1_SSH_PASS)" ssh \
			-p $(PROD_AGENT1_SSH_PORT) \
			-o StrictHostKeyChecking=no \
			-o "ProxyCommand=sshpass -p '$(PROD_MANAGER_SSH_PASS)' ssh -W %h:%p -p $(PROD_MANAGER_SSH_PORT) -o StrictHostKeyChecking=no $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST)" \
			$(PROD_AGENT1_SSH_USER)@$(PROD_AGENT1_SSH_HOST) \
			"cd /opt/runtime-agent && docker compose logs 2>&1 | grep hujingnb"; \
	} | tee /tmp/oc-debug-logs.txt
	@xclip -selection clipboard < /tmp/oc-debug-logs.txt && echo "✅ 已复制到剪切板"

# logs-api 在 manage 服务器上持续 tail manager-api 容器日志，Ctrl+C 退出。
# -t 分配伪终端，确保 Ctrl+C 信号能正确传递给远端 docker compose 进程。
logs-api: ## 查看线上 manager-api 容器日志（持续 tail，Ctrl+C 退出）
	sshpass -p "$(PROD_MANAGER_SSH_PASS)" ssh \
		-p $(PROD_MANAGER_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-t $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST) \
		"cd /opt/oc-manage && docker compose logs manager-api"

# logs-agent1 经由 manager 内网跳转到 agent-1，持续 tail runtime-agent 容器日志。
logs-agent1: ## 查看线上 agent-1 容器日志（经 manager 跳转，持续 tail，Ctrl+C 退出）
	sshpass -p "$(PROD_AGENT1_SSH_PASS)" ssh \
		-p $(PROD_AGENT1_SSH_PORT) \
		-o StrictHostKeyChecking=no \
		-o "ProxyCommand=sshpass -p '$(PROD_MANAGER_SSH_PASS)' ssh -W %h:%p -p $(PROD_MANAGER_SSH_PORT) -o StrictHostKeyChecking=no $(PROD_MANAGER_SSH_USER)@$(PROD_MANAGER_SSH_HOST)" \
		-t $(PROD_AGENT1_SSH_USER)@$(PROD_AGENT1_SSH_HOST) \
		"cd /opt/runtime-agent && docker compose logs"
