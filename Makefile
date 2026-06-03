.PHONY: test vet build sqlc-generate migrate-up migrate-down web-test web-typecheck web-build build-hermes-runtime hermes-inject-contract debug-ollama debug-newapi newapi-probe seed-e2e smoke-v102 openapi-gen web-types-gen openapi-check local-up local-down local-reset local-stop local-start local-build local-migrate local-seed local-seed-e2e local-mc-init local-status local-logs local-shell cluster-create .guard-k3d-hosts build-ops-runtime local-build-ops

SWAG_VERSION := v2.0.0-rc5
OPENAPI_TS_VERSION := 7.13.0

# —— 本地 k3d 集群参数（spec-D）——
K3D_CLUSTER       ?= ocm
K3D_REGISTRY      ?= ocm-registry
K3D_REGISTRY_PORT ?= 5000
K3D_REGISTRY_HOST := k3d-$(K3D_REGISTRY).localhost:$(K3D_REGISTRY_PORT)
K3D_DATA_DIR      := $(CURDIR)/.k3d-data
K8S_NS            ?= ocm
K8S_LOCAL_DIR     := deploy/k8s/local
KUBECTL           ?= kubectl
# local Secret 文件：固定开发凭证，直接入库（deploy/k8s/local/secret.yaml）。
SECRET_FILE       := $(K8S_LOCAL_DIR)/secret.yaml

# 统一镜像 tag：取 make 调用时的本地时间，格式 YYYY-MM-DD-HH-MM-SS。
# 同一次 make 调用中所有生产镜像共享同一时间戳，避免同批镜像 tag 不一致。
# 使用 override 防止命令行 IMAGE_TIMESTAMP=dev 间接生成语义不明或浮动镜像 tag。
override IMAGE_TIMESTAMP := $(shell date +%Y-%m-%d-%H-%M-%S)

# 当前 HEAD 的 8 位短 commit id，用于把本仓库构建产物追溯到源码提交。
# 使用 override 防止命令行 GIT_COMMIT_SHORT=main 改写发布镜像 tag。
override GIT_COMMIT_SHORT := $(strip $(shell git rev-parse --short=8 HEAD 2>/dev/null))

# 工作区脏标记：当存在未提交的 tracked 改动（unstaged 或 staged）时取值 "-dirty"，
# 干净工作区时为空字符串。允许带未提交改动构建镜像，但通过 tag 后缀显式标识
# 该镜像并非来自干净提交，避免事后无法分辨镜像对应的实际源码状态。
# 使用 override 防止命令行覆盖该标记，绕过脏镜像识别。
override GIT_DIRTY_SUFFIX := $(strip $(shell git diff --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null || echo -dirty))

# 本仓库发布镜像统一 tag：构建时间戳 + 源码 commit 前 8 位 + 可选 -dirty 脏标记。
# 使用 override 防止命令行 IMAGE_TAG=latest 绕过 tag 规则。
override IMAGE_TAG := $(IMAGE_TIMESTAMP)-$(GIT_COMMIT_SHORT)$(GIT_DIRTY_SUFFIX)

# 各服务生产镜像仓库（统一走移动云私有仓库 app 命名空间，与集群同区拉取快）。
# 走其他 registry 时在命令行覆盖对应变量即可。
PROD_REGISTRY    ?= ywjs-26257ea5.ecis.huabei-3.cmecloud.cn
PROD_APP_NS      ?= app
# 构建期基础镜像（golang/node/nginx/alpine/python）走的 public 命名空间，作为 DOCKER_HUB_MIRROR。
PROD_PUBLIC_REPO ?= $(PROD_REGISTRY)/public
API_IMAGE_REPO   ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-api
WEB_IMAGE_REPO   ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-web

# hermes runtime 生产镜像仓库，与上方三个服务保持一致命名风格。
# HERMES_VARIANT 选择 runtime/hermes/ 下的 versioned variant 子目录（自包含 Dockerfile + 资产）。
# 镜像 tag 从该 variant 的 version.txt 派生，禁止 main / master / latest / dev 等浮动 ref。
HERMES_VARIANT       ?= hermes-v2026.5.16
# HERMES_VARIANT_DIR 只能由 HERMES_VARIANT 派生，避免命令行直接指向任意目录绕过版本校验。
override HERMES_VARIANT_DIR := runtime/hermes/$(HERMES_VARIANT)
override HERMES_VERSION := $(strip $(shell if [ -f "$(HERMES_VARIANT_DIR)/version.txt" ]; then cat "$(HERMES_VARIANT_DIR)/version.txt"; fi))
HERMES_IMAGE_REPO    ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-hermes
# hermes tag 形如 v2026.5.16-2026-05-21-12-00-00-be70e40a，便于从镜像引用直接看出上游版本和源码提交。
override HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TAG)

# ops runtime 镜像仓库（pod initContainer/sidecar 搬运脚本），与其余服务保持一致命名风格。
# 生产发布用 IMAGE_TAG（时间戳 + commit），本地联调固定 :dev。
OPS_IMAGE_REPO ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-ops

# —— 线上 k8s 集群（区别于本地 k3d 的默认 context）——
# 线上集群不在默认 kubeconfig 的 context 里，须用独立 kubeconfig 连接；
# 命名空间与工作负载、容器名以 deploy/k8s/prod 为准（ns=ocm，Deployment 名=容器名）。
# 走其他 kubeconfig / 命名空间时在命令行覆盖 PROD_KUBECONFIG / PROD_NS 即可。
PROD_KUBECONFIG ?= $(HOME)/dir/ywjs/kube/kubeconfig.json
PROD_NS         ?= ocm
override PROD_KUBECTL := kubectl --kubeconfig $(PROD_KUBECONFIG) -n $(PROD_NS)
# 线上滚动等待超时：节点拉 ACR 镜像慢（无缓存时可能数十分钟），过短的 rollout status
# 超时会在 pod 还在正常拉镜像时就误报失败。设宽松默认 15min，可命令行覆盖
# （如 make update-config PROD_ROLLOUT_TIMEOUT=1800s）。
PROD_ROLLOUT_TIMEOUT ?= 900s

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
	@test "$(HERMES_VARIANT)" = "hermes-$(HERMES_VERSION)" || { echo "Hermes variant 名称必须与 version.txt 对齐: $(HERMES_VARIANT) != hermes-$(HERMES_VERSION)" >&2; exit 1; }
	@printf '%s\n' "$(HERMES_VERSION)" | grep -Eq '^v[0-9]+[.][0-9]+[.][0-9]+([._-][A-Za-z0-9_.-]+)?$$' || { echo "Hermes version 必须是完整版本号: $(HERMES_VERSION)" >&2; exit 1; }
	@case "$(HERMES_VERSION)" in \
		main|master|latest|dev|*hermes-main*) echo "Hermes version 不能使用浮动或旧 variant tag: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
	@case "$(HERMES_VERSION)" in \
		*[!A-Za-z0-9_.-]*) echo "Hermes version 包含非法镜像 tag 字符: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
	@case "$(HERMES_VERSION)" in \
		.*|-*) echo "Hermes version 不能以 Docker tag 非法起始字符开头: $(HERMES_VERSION)" >&2; exit 1;; \
	esac
	@version="$(HERMES_VERSION)"; test $${#version} -le 99 || { echo "Hermes version 过长，无法为生产时间戳和提交号预留 Docker tag 长度: $(HERMES_VERSION)" >&2; exit 1; }

.PHONY: .guard-image-git-state
.guard-image-git-state:
	@git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "发布镜像必须在 git worktree 内构建" >&2; exit 1; }
	@test -n "$(GIT_COMMIT_SHORT)" || { echo "无法读取当前 git commit id" >&2; exit 1; }
	@test -z "$(GIT_DIRTY_SUFFIX)" || echo "⚠️  工作区存在未提交的 tracked 改动，镜像 tag 将追加 -dirty 标记: $(IMAGE_TAG)" >&2

##@ 本地开发 (k3d)

.guard-k3d-hosts: ## 校验 /etc/hosts 已把 k3d registry 主机名指向 127.0.0.1
	@grep -q "k3d-$(K3D_REGISTRY).localhost" /etc/hosts || { \
		echo "❌ 缺少 hosts 记录。请执行（需 sudo）："; \
		echo "   echo '127.0.0.1 k3d-$(K3D_REGISTRY).localhost' | sudo tee -a /etc/hosts"; \
		exit 1; }

cluster-create: .guard-k3d-hosts ## 创建 k3d 集群（带 registry + 宿主数据卷 + 80 端口映射）
	@mkdir -p $(K3D_DATA_DIR)
	k3d cluster create $(K3D_CLUSTER) \
		--registry-create $(K3D_REGISTRY):0.0.0.0:$(K3D_REGISTRY_PORT) \
		--registry-config $(K8S_LOCAL_DIR)/registries.yaml \
		--volume $(K3D_DATA_DIR):/var/lib/rancher/k3s/storage@all \
		--port "80:80@loadbalancer" \
		--wait
	@echo "✅ 集群 $(K3D_CLUSTER) 就绪。registry=$(K3D_REGISTRY_HOST)，数据卷=$(K3D_DATA_DIR)"

local-stop: ## 停止 k3d 集群但不删除（保留数据与镜像，data 不丢；用 local-start 恢复）
	k3d cluster stop $(K3D_CLUSTER)
	@echo "ℹ️  集群已停止；跑 make local-start 原样恢复（PVC/数据/镜像均保留）"

local-start: ## 启动已停止的 k3d 集群（数据与已部署对象原样恢复）
	k3d cluster start $(K3D_CLUSTER)
	@echo "✅ 集群已启动；稍候各 pod 自恢复，可用 make local-status 查看"

local-down: ## 删除 k3d 集群（注意：删集群会重建 PVC，业务数据不保留；保数据请用 local-stop）
	-k3d cluster delete $(K3D_CLUSTER)
	@echo "ℹ️  集群已删除。.k3d-data 旧目录仍在磁盘但不会被下次 up 复用（local-path 按新 PVC uid 建目录）；"
	@echo "    如需保数据重启请改用 make local-stop / make local-start；如需彻底清空跑 make local-reset"

local-reset: local-down ## 删集群并清空 .k3d-data，干净重建（不自动 up）
	# .k3d-data 内是集群内 root 进程写入的 PVC 数据（如 redis appendonly），宿主用户
	# 无权直接 rm；先用一次性 root 容器清空目录内容（镜像走可达的移动云 public alpine），再删空目录。
	-docker run --rm -v $(K3D_DATA_DIR):/data $(PROD_PUBLIC_REPO)/alpine:3.22 sh -c 'rm -rf /data/* /data/.[!.]* 2>/dev/null'
	rm -rf $(K3D_DATA_DIR)
	@echo "✅ 已清空 $(K3D_DATA_DIR)；跑 make local-up 干净重建"

local-build: ## 构建 manager-api/web 镜像推 k3d registry 并滚动重启（改 Go/前端代码后跑）
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-api:dev -f cmd/server/Dockerfile .
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-web:dev -f web/Dockerfile ./web
	docker push $(K3D_REGISTRY_HOST)/oc-manager-api:dev
	docker push $(K3D_REGISTRY_HOST)/oc-manager-web:dev
	-$(KUBECTL) -n $(K8S_NS) rollout restart deploy/manager-api deploy/manager-web
	@echo "✅ 镜像已推送并触发滚动重启"

local-build-ops: ## 构建 ops 镜像推 k3d registry（本地联调用）
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-ops:dev runtime/ops/
	docker push $(K3D_REGISTRY_HOST)/oc-manager-ops:dev

local-up: cluster-create local-build ## 一键拉起本地全栈（建集群→构建镜像→部署→建桶→种子管理员）
	# 1) namespace + secret 先行
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/00-namespace.yaml
	$(KUBECTL) apply -f $(SECRET_FILE)
	# 2) 有状态后端，等就绪
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/mysql.yaml \
		-f $(K8S_LOCAL_DIR)/redis.yaml \
		-f $(K8S_LOCAL_DIR)/elasticsearch.yaml \
		-f $(K8S_LOCAL_DIR)/minio.yaml
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/mysql --timeout=300s
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/redis --timeout=120s
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/minio --timeout=120s
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/elasticsearch --timeout=300s
	# 3) 建 MinIO bucket
	$(MAKE) local-mc-init
	# 4) 控制面 / 业务 + RBAC + Ingress
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/manager-rbac.yaml \
		-f $(K8S_LOCAL_DIR)/manager-api.yaml \
		-f $(K8S_LOCAL_DIR)/manager-web.yaml \
		-f $(K8S_LOCAL_DIR)/new-api.yaml \
		-f $(K8S_LOCAL_DIR)/ragflow.yaml \
		-f $(K8S_LOCAL_DIR)/ollama.yaml \
		-f $(K8S_LOCAL_DIR)/ingress.yaml
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-api --timeout=180s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-web --timeout=120s
	# 5) 种子平台管理员（幂等）
	$(MAKE) local-seed
	@echo "✅ 本地全栈就绪："
	@echo "   manager 控制台 http://ocm.localhost"
	@echo "   new-api 后台    http://newapi.localhost"
	@echo "   ragflow 控制台  http://ragflow.localhost"

local-mc-init: ## 在 minio 容器内建 app/ragflow bucket（幂等）
	$(KUBECTL) -n $(K8S_NS) exec statefulset/minio -- sh -c '\
		mc alias set local http://127.0.0.1:9000 "$$MINIO_ROOT_USER" "$$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; \
		mc mb -p local/oc-apps; mc mb -p local/ragflow; mc ls local'

local-migrate: ## kubectl exec manager-api 跑迁移（默认 up；DOWN=1 则回滚一次）
	@if [ "$(DOWN)" = "1" ]; then \
		$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- migrate down; \
	else \
		$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- migrate up; \
	fi

local-seed: ## kubectl exec manager-api 种子平台管理员 admin/admin123（幂等）
	$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- seed-admin admin admin123

local-seed-e2e: ## kubectl exec manager-api 注入 Playwright e2e fixture（OCM_E2E=1 守门），打印 fixture JSON
	@$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- env OCM_E2E=1 seed-e2e

local-status: ## 查看本地集群 pod / ingress 状态
	$(KUBECTL) -n $(K8S_NS) get pods,svc,ingress

local-logs: ## tail 指定服务日志（用法：make local-logs svc=manager-api）
	$(KUBECTL) -n $(K8S_NS) logs -f deploy/$(svc)

local-shell: ## 进入指定服务容器（用法：make local-shell svc=manager-api）
	$(KUBECTL) -n $(K8S_NS) exec -it deploy/$(svc) -- sh

##@ 测试 / 静态检查

test: ## 跑 Go 单元测试 (go test ./...)
	go test ./...

integration-test: ## 跑集成测试（需本地 k3d MySQL/Redis 经端口转发或外部实例，见 docs/local-development.md）
	INTEGRATION_DATABASE_URL="$${INTEGRATION_DATABASE_URL:?需指向可达的 MySQL，如经 kubectl port-forward}" \
	INTEGRATION_REDIS_ADDR="$${INTEGRATION_REDIS_ADDR:?需指向可达的 Redis}" \
	go test -tags=integration ./...

vet: ## 跑 go vet ./...
	go vet ./...

##@ 构建

build: ## 编译 server / migrate 到 tmp/build/
	go build -o ./tmp/build/server ./cmd/server
	go build -o ./tmp/build/migrate ./cmd/migrate

##@ 代码生成 (sqlc / OpenAPI / 前端类型)

sqlc-generate: ## 跑 sqlc generate, 覆盖 internal/store 生成代码
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

##@ 前端开发

web-test: ## 在 web/ 跑 vitest 单测
	cd web && npm install && npm test -- --run

web-typecheck: ## 在 web/ 跑 vue-tsc --noEmit
	cd web && npm install && npm run typecheck

web-build: ## 在 web/ 跑 vite build
	cd web && npm install && npm run build

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

# api 构建上下文为仓库根目录（需访问 go.mod / internal/ 等源码），
# web 构建上下文为 web/ 子目录（前端工程相对自包含，无需整个仓库）。
# 同一次 make 调用中多个服务共享 IMAGE_TAG，保证同批镜像 tag 一致且可追溯到同一源码提交。

.PHONY: build-api-image
build-api-image: .guard-image-git-state ## 构建并推送 manager-api 生产镜像，tag 取时间戳 + git commit
	docker build --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) -t $(API_IMAGE_REPO):$(IMAGE_TAG) -f cmd/server/Dockerfile .
	docker push $(API_IMAGE_REPO):$(IMAGE_TAG)
	@echo "✅ manager-api 镜像 $(API_IMAGE_REPO):$(IMAGE_TAG) 已构建并推送"

.PHONY: build-web-image
build-web-image: .guard-image-git-state ## 构建并推送 manager-web 生产镜像，tag 取时间戳 + git commit
	docker build --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) -t $(WEB_IMAGE_REPO):$(IMAGE_TAG) -f web/Dockerfile ./web
	docker push $(WEB_IMAGE_REPO):$(IMAGE_TAG)
	@echo "✅ manager-web 镜像 $(WEB_IMAGE_REPO):$(IMAGE_TAG) 已构建并推送"

# build-hermes-image 构建并推送 hermes runtime 生产镜像，直接打上移动云仓库完整 tag。
# build context 取自 HERMES_VARIANT 指向的子目录（自包含 Dockerfile + 资产）。
# build 与 push 在同一 shell 块内执行：先 build，无论成败都清理注入的 contract 目录，
# build 成功才 push（失败则带原始退出码中止）。推送完成后输出最终镜像引用，
# 方便填入 deploy/k8s/{local,prod}/secret.yaml 内嵌 manager.yaml 的 hermes.runtime_images。
.PHONY: build-hermes-image
build-hermes-image: .guard-image-git-state .guard-hermes-version hermes-inject-contract ## 构建并推送 hermes runtime 生产镜像（需 HERMES_VARIANT 指定变体）
	status=0; \
	docker build \
	  -t "$(HERMES_IMAGE)" \
	  --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) \
	  --build-arg "HERMES_REF=$(HERMES_VERSION)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
	rm -rf $(HERMES_VARIANT_DIR)/kanban-contract $(HERMES_VARIANT_DIR)/cron-contract; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	docker push "$(HERMES_IMAGE)"
	@echo "✅ hermes 镜像 $(HERMES_IMAGE) 已构建并推送"

# build-ops-runtime 构建 ops runtime 镜像并推生产 registry。
# runtime/ops/ 是自包含 build context（含 Dockerfile 与脚本），无需额外前置依赖。
# 镜像 tag 复用 IMAGE_TAG（时间戳 + commit + 可选 -dirty），与其他服务保持一致可追溯性。
build-ops-runtime: .guard-image-git-state ## 构建 ops 运行时镜像并推生产 registry
	docker build --build-arg ALPINE_MIRROR=$(PROD_PUBLIC_REPO) -t $(OPS_IMAGE_REPO):$(IMAGE_TAG) runtime/ops/
	docker push $(OPS_IMAGE_REPO):$(IMAGE_TAG)
	@echo "✅ ops 镜像 $(OPS_IMAGE_REPO):$(IMAGE_TAG) 已构建并推送"

##@ 生产部署 (k8s)

# update-config 把改好的线上 secret 推到集群并让 manager-api 加载新配置：
# 1) apply secret.yaml 更新 Secret 对象（含 manager.yaml 完整配置、外部后端连接、ACR 凭证）；
# 2) rollout restart 重启 manager-api，重新挂载并加载新配置（进程不热重载配置文件，必须重启）。
# apply 不带 -n：secret.yaml 是多文档（ocm-secrets + ocm/oc-apps 两个 acr-pull），各对象自带
# namespace，指定 -n 会与声明 oc-apps 的对象冲突；restart/status 才用带 -n ocm 的 PROD_KUBECTL。
.PHONY: update-config
update-config: ## apply 线上 secret.yaml 并重启 manager-api 使新配置生效
	kubectl --kubeconfig $(PROD_KUBECONFIG) apply -f deploy/k8s/prod/secret.yaml
	$(PROD_KUBECTL) rollout restart deploy/manager-api
	$(PROD_KUBECTL) rollout status deploy/manager-api --timeout=$(PROD_ROLLOUT_TIMEOUT)
	@echo "✅ 线上 secret 已更新，manager-api 已重启加载新配置"

# prod-deploy-api / prod-deploy-web：构建推送对应镜像后，用 kubectl set image
# 滚动更新线上 Deployment。set image 只改容器镜像引用、不动 deploy/k8s/prod 的
# yaml 与 secret，发版纯靠镜像 tag 滚动，避免手工改 manifest。
# build-* 与 set image 在同一次 make 调用内共享同一 IMAGE_TAG，保证推送的镜像
# 和滚动更新引用的 tag 完全一致。Deployment 名与容器名一致（manager-api/manager-web）。
.PHONY: prod-deploy-api
prod-deploy-api: build-api-image ## 构建推送 manager-api 并滚动更新线上 deploy/manager-api
	$(PROD_KUBECTL) set image deploy/manager-api manager-api=$(API_IMAGE_REPO):$(IMAGE_TAG)
	$(PROD_KUBECTL) rollout status deploy/manager-api --timeout=$(PROD_ROLLOUT_TIMEOUT)
	@echo "✅ 线上 manager-api 已更新为 $(API_IMAGE_REPO):$(IMAGE_TAG)"

.PHONY: prod-deploy-web
prod-deploy-web: build-web-image ## 构建推送 manager-web 并滚动更新线上 deploy/manager-web
	$(PROD_KUBECTL) set image deploy/manager-web manager-web=$(WEB_IMAGE_REPO):$(IMAGE_TAG)
	$(PROD_KUBECTL) rollout status deploy/manager-web --timeout=$(PROD_ROLLOUT_TIMEOUT)
	@echo "✅ 线上 manager-web 已更新为 $(WEB_IMAGE_REPO):$(IMAGE_TAG)"

# prod-deploy-manager 一次性发布 api + web：先完成两镜像的构建推送（共享同一
# IMAGE_TAG，保证同源），再连续 set image 两个 Deployment，最后各自等滚动完成，
# 避免分两次调用导致 tag 不一致或多轮无谓重启。
.PHONY: prod-deploy-manager
prod-deploy-manager: build-api-image build-web-image ## 构建推送 api+web 并一次性滚动更新线上 manager
	$(PROD_KUBECTL) set image deploy/manager-api manager-api=$(API_IMAGE_REPO):$(IMAGE_TAG)
	$(PROD_KUBECTL) set image deploy/manager-web manager-web=$(WEB_IMAGE_REPO):$(IMAGE_TAG)
	$(PROD_KUBECTL) rollout status deploy/manager-api --timeout=$(PROD_ROLLOUT_TIMEOUT)
	$(PROD_KUBECTL) rollout status deploy/manager-web --timeout=$(PROD_ROLLOUT_TIMEOUT)
	@echo "✅ 线上 manager 已更新：api=$(API_IMAGE_REPO):$(IMAGE_TAG) web=$(WEB_IMAGE_REPO):$(IMAGE_TAG)"

# prod-deploy-hermes / prod-deploy-ops：hermes/ops 镜像不是独立 Deployment（由 manager
# 渲染 app pod 时引用），故发版方式与 api/web 不同——不能 set image，而是把新镜像 ref 写回
# 本地 secret.yaml 的对应字段（hermes→hermes.runtime_images[].ref，ops→k8s.ops_image），
# 再走 update-config（apply secret + 重启 manager-api）让新镜像在后续渲染的 app pod 生效。
# 镜像仓库迁到同区移动云后节点拉取快，不再需要预热 DaemonSet。
.PHONY: prod-deploy-hermes
prod-deploy-hermes: build-hermes-image ## 构建推送 hermes 镜像→写回 secret.yaml→update-config 生效
	sed -i -E 's#ref: "[^"]*oc-manager-hermes:[^"]*"#ref: "$(HERMES_IMAGE)"#' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 hermes 镜像已更新为 $(HERMES_IMAGE)"
	$(MAKE) update-config

.PHONY: prod-deploy-ops
prod-deploy-ops: build-ops-runtime ## 构建推送 ops 镜像→写回 secret.yaml→update-config 生效
	sed -i -E 's#ops_image: "[^"]*oc-manager-ops:[^"]*"#ops_image: "$(OPS_IMAGE_REPO):$(IMAGE_TAG)"#' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 ops 镜像已更新为 $(OPS_IMAGE_REPO):$(IMAGE_TAG)"
	$(MAKE) update-config

##@ 生产数据库 (k8s)

# prod-db 起一个连到线上 manager MySQL 的交互式 mysql 会话。线上 MySQL 由外部托管、
# 不在 ocm 集群内，util namespace 的 mysql 客户端 pod 自带 mysql 客户端，作为跳板连到
# 托管实例（与手敲 `kubectl exec -it -n util deploy/mysql -- mysql ...` 等价）。
# 连接凭证（host/port/user/password/库名）是生产敏感信息，绝不写进入库的 Makefile——
# 一律从 gitignored 的根 .env 读取（占位见 .env.example 的 PROD_DB_* 段）。
# 注意：这是交互式会话、可执行写 SQL；请自行确认每条语句后再回车。
# util 不属于本项目的 ocm/oc-apps，仅借其 mysql 客户端做跳板；可命令行覆盖 PROD_DB_NS。
PROD_DB_NS ?= util
.PHONY: prod-db
prod-db: ## 连接线上 manager MySQL（凭证读自 .env 的 PROD_DB_*，交互式 mysql 会话）
	@test -f .env || { echo "缺少 .env，请从 .env.example 复制并填好 PROD_DB_* 段"; exit 1; }
	@set -a; . ./.env; set +a; \
	: "$${PROD_DB_HOST:?请在 .env 设置 PROD_DB_HOST}"; \
	: "$${PROD_DB_USER:?请在 .env 设置 PROD_DB_USER}"; \
	: "$${PROD_DB_PASS:?请在 .env 设置 PROD_DB_PASS}"; \
	kubectl --kubeconfig $(PROD_KUBECONFIG) -n $(PROD_DB_NS) exec -it deploy/mysql -- \
	  env LANG=C.UTF-8 mysql \
	    --host="$$PROD_DB_HOST" --port="$${PROD_DB_PORT:-3306}" \
	    --user="$$PROD_DB_USER" -p"$$PROD_DB_PASS" \
	    --database="$${PROD_DB_NAME:-manager}"

##@ 调试脚本

debug-ollama: ## 跑 debug-ollama.sh, 探测 ollama 状态
	./scripts/debug-ollama.sh

debug-newapi: ## 跑 debug-newapi.sh, 探测 new-api 状态
	./scripts/debug-newapi.sh

newapi-probe: ## 跑 newapi-probe.sh, 用最小请求探测 new-api 渠道
	@bash scripts/newapi-probe.sh

##@ 数据库迁移

migrate-up: ## 对本地 k3d 数据库执行 up 迁移（= local-migrate）
	$(MAKE) local-migrate

migrate-down: ## 回滚本地 k3d 最近一次迁移（= local-migrate DOWN=1）
	$(MAKE) local-migrate DOWN=1

##@ 部署 / 运维 / Smoke

# seed-e2e：在 manager-api 容器里跑 cmd/seed-e2e，OCM_E2E=1 守门。
# 会 TRUNCATE e2e 业务表后重建 fixture，stdout 末行是 fixture JSON 供 Playwright 解析。
seed-e2e: ## 注入 Playwright e2e fixture（= local-seed-e2e）
	$(MAKE) local-seed-e2e

smoke-v102:  ## 跑 v1.0.2 干净环境 smoke（前置：阶段 0 完成）
	@bash scripts/v102-smoke.sh

##@ OpenAPI / 前端类型 (与代码生成段相互引用)

.PHONY: openapi-gen
openapi-gen: ## 后端注解扫描，覆盖 openapi/openapi.yaml
	go run github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION) init \
		--generalInfo main.go \
		--dir cmd/server,internal/api/handlers,internal/service,internal/domain,internal/integrations/ocops \
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
