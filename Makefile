.PHONY: test test-hermes-version-guard vet build sqlc-generate migrate-up migrate-down web-test web-typecheck web-build e2e-quick e2e-regression e2e-slow build-hermes-runtime hermes-inject-contract seed-e2e openapi-gen web-types-gen openapi-check local-up local-down local-reset local-stop local-start local-build local-preload local-migrate local-seed local-seed-e2e local-mc-init local-status local-logs local-shell cluster-create .guard-k3d-hosts build-ops-runtime local-build-ops local-init-models

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
HERMES_VARIANT       ?= hermes-v0.18.2
# HERMES_VARIANT_DIR 只能由 HERMES_VARIANT 派生，避免命令行直接指向任意目录绕过版本校验。
override HERMES_VARIANT_DIR := runtime/hermes/$(HERMES_VARIANT)
override HERMES_VERSION := $(strip $(shell if [ -f "$(HERMES_VARIANT_DIR)/version.txt" ]; then cat "$(HERMES_VARIANT_DIR)/version.txt"; fi))
# 普通 Hermes 的产品版本与上游源码 ref 并非始终同号：version.txt 继续决定对外版本和镜像 tag，
# hermes-ref.txt 仅决定 Docker 构建时检出的上游 Git ref。历史 variant 尚未提供 hermes-ref.txt，
# 因此文件缺失时才回退到 HERMES_VERSION，保证旧目录仍可原样构建；文件存在但内容为空时
# 必须保留空值并交由版本守卫拒绝，避免元数据配置错误被兼容回退静默掩盖。
override HERMES_UPSTREAM_REF := $(strip $(shell if [ -f "$(HERMES_VARIANT_DIR)/hermes-ref.txt" ]; then cat "$(HERMES_VARIANT_DIR)/hermes-ref.txt"; else printf '%s' "$(HERMES_VERSION)"; fi))
HERMES_IMAGE_REPO    ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-aigowork
# hermes 镜像 tag 以产品/variant 版本开头（如 v0.18.2-时间戳-源码提交），便于识别对外运行时版本。
# 上游固定 ref 独立记录在 hermes-ref.txt 并透传到镜像元数据，不能再从产品版本号推断。
override HERMES_IMAGE := $(HERMES_IMAGE_REPO):$(HERMES_VERSION)-$(IMAGE_TAG)

# AICC runtime 使用独立构建目录与仓库，生命周期不与普通实例 Hermes 版本列表耦合。
# 版本从客服目录自身读取，tag 同样由客服版本、构建时间和源码提交组成，禁止浮动引用。
override AICC_RUNTIME_DIR := runtime/hermes/hermes-aicc
override AICC_RUNTIME_VERSION := $(strip $(shell if [ -f "$(AICC_RUNTIME_DIR)/version.txt" ]; then cat "$(AICC_RUNTIME_DIR)/version.txt"; fi))
override AICC_RUNTIME_HERMES_REF := $(strip $(shell if [ -f "$(AICC_RUNTIME_DIR)/hermes-ref.txt" ]; then cat "$(AICC_RUNTIME_DIR)/hermes-ref.txt"; fi))
AICC_RUNTIME_IMAGE_REPO ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-aigowork-aicc
override AICC_RUNTIME_IMAGE := $(AICC_RUNTIME_IMAGE_REPO):$(AICC_RUNTIME_VERSION)-$(IMAGE_TAG)
# HERMES_VERSION 的正则转义版（点号转义）：prod-deploy-hermes 用它把 secret.yaml 中
# runtime_images 里「同版本」那一条 ref 精确替换为新 tag，不能误伤其它版本条目
# （runtime_images 现为多版本列表，旧版需保留供回退灰度）。
override HERMES_VERSION_RE := $(subst .,\.,$(HERMES_VERSION))
# hermes 镜像构建额外 flag：默认空，复用 Docker 层缓存（增量构建快，平时用这个）。
# 重建旧 variant 时可能命中陈旧的 install.sh 缓存层——上游 install.sh 是 live 拉取、布局
# 会变（如 uv 从 /root/.local/bin 挪到 /opt/data/bin），旧缓存层的布局与当前 Dockerfile
# 的搬迁步骤不匹配，会在 mv 处失败。此时用 NO_CACHE=1 强制全量重跑，让 install.sh 按当前
# 布局重新安装。仅在撞缓存时打开，别常开（全量重建会重拉基础镜像 / 重跑 apt 与 install.sh，很慢）。
HERMES_BUILD_FLAGS :=
ifeq ($(NO_CACHE),1)
HERMES_BUILD_FLAGS := --no-cache
endif

# HERMES_VARIANTS_ALL：runtime/hermes/ 下全部普通版本化 Hermes 变体（hermes-v*）。
# AICC 使用独立目录和镜像仓库，不属于普通实例 runtime_images，必须通过
# prod-deploy-aicc-runtime 单独发布，不能被该批量命令纳入。
override HERMES_VARIANTS_ALL := $(filter hermes-v%,$(notdir $(wildcard runtime/hermes/hermes-*)))

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
# site-server 部署在 oc-apps namespace（与 app pod 同），需独立的带 -n oc-apps 的 kubectl。
PROD_APPS_NS    ?= oc-apps
override PROD_KUBECTL_APPS := kubectl --kubeconfig $(PROD_KUBECONFIG) -n $(PROD_APPS_NS)
# 线上滚动等待超时：节点拉 ACR 镜像慢（无缓存时可能数十分钟），过短的 rollout status
# 超时会在 pod 还在正常拉镜像时就误报失败。设宽松默认 15min，可命令行覆盖
# （如 make prod-update-config PROD_ROLLOUT_TIMEOUT=1800s）。
PROD_ROLLOUT_TIMEOUT ?= 900s

# —— Git 远程同步 ——
# 本仓库配两个远程: github 为个人 GitHub(承载 Claude Code 云端开发分支),
# origin 为 ywjs 内部 aliyun codeup。两者间需双向同步当前分支。
# 走其他远程名时在命令行覆盖即可。
GITHUB_REMOTE ?= github
ORIGIN_REMOTE ?= origin

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
# 上游 ref 必须是适配 --branch 安装路径的不可变版本 tag，且能安全进入后续 Docker recipe。
# 守卫直接从 hermes-ref.txt 读取到
# shell 变量，不能把尚未校验的 HERMES_UPSTREAM_REF 插入 shell 源码，否则反引号、命令替换等
# 元字符会先于校验执行；历史 variant 缺少该文件时才使用已校验命名关系的 HERMES_VERSION。
# 正则前先对整值执行字符白名单，避免 grep 按行命中首行后放过后续 payload，并阻断 Make 折叠换行。
.guard-hermes-version:
	@test -f "$(HERMES_VARIANT_DIR)/version.txt" || { echo "Hermes variant 缺少 version.txt: $(HERMES_VARIANT_DIR)/version.txt" >&2; exit 1; }
	@test -n "$(HERMES_VERSION)" || { echo "Hermes version 不能为空: $(HERMES_VARIANT_DIR)/version.txt" >&2; exit 1; }
	@hermes_ref="$(HERMES_VERSION)"; \
	if [ -f "$(HERMES_VARIANT_DIR)/hermes-ref.txt" ]; then hermes_ref=$$(cat "$(HERMES_VARIANT_DIR)/hermes-ref.txt"); fi; \
	test -n "$$hermes_ref" || { echo "Hermes 上游 ref 不能为空: $(HERMES_VARIANT_DIR)/hermes-ref.txt 或 version.txt" >&2; exit 1; }; \
	case "$$hermes_ref" in *[!A-Za-z0-9_.-]*) echo "Hermes 上游 ref 包含非法字符: $$hermes_ref" >&2; exit 1;; esac; \
	printf '%s\n' "$$hermes_ref" | grep -Eq '^v[0-9]+[.][0-9]+[.][0-9]+([._-][A-Za-z0-9_.-]+)?$$' || { echo "Hermes 上游 ref 必须是不可变版本 tag: $$hermes_ref" >&2; exit 1; }
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

.PHONY: .guard-aicc-runtime-version
.guard-aicc-runtime-version:
	@test -f "$(AICC_RUNTIME_DIR)/version.txt" || { echo "AICC runtime 缺少 version.txt: $(AICC_RUNTIME_DIR)/version.txt" >&2; exit 1; }
	@test -f "$(AICC_RUNTIME_DIR)/hermes-ref.txt" || { echo "AICC runtime 缺少 hermes-ref.txt: $(AICC_RUNTIME_DIR)/hermes-ref.txt" >&2; exit 1; }
	@test -n "$(AICC_RUNTIME_VERSION)" || { echo "AICC runtime version 不能为空: $(AICC_RUNTIME_DIR)/version.txt" >&2; exit 1; }
	@test -n "$(AICC_RUNTIME_HERMES_REF)" || { echo "AICC runtime Hermes 上游 ref 不能为空: $(AICC_RUNTIME_DIR)/hermes-ref.txt" >&2; exit 1; }
	@printf '%s\n' "$(AICC_RUNTIME_VERSION)" | grep -Eq '^v[0-9]+[.][0-9]+[.][0-9]+([._-][A-Za-z0-9_.-]+)?$$' || { echo "AICC runtime version 必须是完整版本号: $(AICC_RUNTIME_VERSION)" >&2; exit 1; }
	@case "$(AICC_RUNTIME_VERSION)" in main|master|latest|dev|*main*) echo "AICC runtime version 不能使用浮动 tag: $(AICC_RUNTIME_VERSION)" >&2; exit 1;; esac

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

local-down: ## 删除 k3d 集群（有状态数据在 .k3d-data 固定目录保留，下次 local-up 复用；彻底清空用 local-reset）
	-k3d cluster delete $(K3D_CLUSTER)
	@echo "ℹ️  集群已删除。有状态数据落在 .k3d-data 固定 hostPath 目录"
	@echo "    （mysql/redis/minio/elasticsearch），下次 make local-up 复用、数据不丢；"
	@echo "    如需彻底清空干净重建跑 make local-reset。"

local-reset: local-down ## 清空所有本地数据并重跑 local-up（全新干净重建到可用，含配置初始化）
	# .k3d-data 内是集群内 root 进程写入的 PVC 数据（如 redis appendonly），宿主用户
	# 无权直接 rm；先用一次性 root 容器清空目录内容（镜像走可达的移动云 public alpine），再删空目录。
	-docker run --rm -v $(K3D_DATA_DIR):/data $(PROD_PUBLIC_REPO)/alpine:3.22 sh -c 'rm -rf /data/* /data/.[!.]* 2>/dev/null'
	rm -rf $(K3D_DATA_DIR)
	@echo "✅ 已清空 $(K3D_DATA_DIR)，开始全新重建..."
	$(MAKE) local-up

local-build: local-build-ops local-build-aicc-runtime ## 构建 manager-api/web 与客服 runtime 镜像推 k3d registry 并滚动重启
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-api:dev -f cmd/server/Dockerfile .
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-web:dev -f web/Dockerfile ./web
	docker push $(K3D_REGISTRY_HOST)/oc-manager-api:dev
	docker push $(K3D_REGISTRY_HOST)/oc-manager-web:dev
	# k3d 节点可能继承仅能解析公网域名的 DNS；将本次构建的 manager 镜像直接导入节点，
	# 配合本地 manifest 的 IfNotPresent，滚动更新无需再解析 Docker 网络内部 registry 名。
	docker save --platform linux/amd64 $(K3D_REGISTRY_HOST)/oc-manager-api:dev | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import -
	docker save --platform linux/amd64 $(K3D_REGISTRY_HOST)/oc-manager-web:dev | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import -
	-$(KUBECTL) -n $(K8S_NS) rollout restart deploy/manager-api deploy/manager-web
	@echo "✅ 镜像已推送并触发滚动重启"

local-build-ops: ## 构建 ops 镜像推 k3d registry（本地联调用）
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-ops:dev runtime/ops/
	docker push $(K3D_REGISTRY_HOST)/oc-manager-ops:dev
	# 固定 :dev 标签的 app initContainer 直接导入节点，避免 containerd 对本地 HTTP registry 重拉失败。
	docker save --platform linux/amd64 $(K3D_REGISTRY_HOST)/oc-manager-ops:dev | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import -

.PHONY: local-build-aicc-runtime
local-build-aicc-runtime: aicc-runtime-inject-contract ## 构建客服专用 runtime 镜像推 k3d registry（本地联调用）
	status=0; \
	docker build $(HERMES_BUILD_FLAGS) \
	  -t $(K3D_REGISTRY_HOST)/oc-manager-aigowork-aicc:dev \
	  --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) \
	  --build-arg "HERMES_REF=$(AICC_RUNTIME_HERMES_REF)" \
	  --build-arg "OC_IMAGE_VARIANT=hermes-aicc" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(AICC_RUNTIME_DIR) || status=$$?; \
	rm -rf $(AICC_RUNTIME_DIR)/ocops-contract $(AICC_RUNTIME_DIR)/kanban-contract $(AICC_RUNTIME_DIR)/cron-contract; \
	if [ $$status -ne 0 ]; then exit $$status; fi
	docker push $(K3D_REGISTRY_HOST)/oc-manager-aigowork-aicc:dev
	# 本地 k3d 节点使用固定 :dev 标签时，直接导入节点以避免 registry 临时不可达导致旧镜像继续运行。
	docker save --platform linux/amd64 $(K3D_REGISTRY_HOST)/oc-manager-aigowork-aicc:dev | docker exec -i k3d-$(K3D_CLUSTER)-server-0 ctr images import -

# LOCAL_PRELOAD_IMAGES：local-up 需要的 docker.io 重镜像。节点 containerd 经镜像源拉这些镜像
# 时快时卡（mysql/es 曾致 rollout 超时，ragflow 几 GB 更易卡），故改为宿主 docker 拉取（走宿主
# daemon 镜像源，必要时换更快的源）后用 k3d 原生 `k3d image import` 灌入集群，pod 调度即命中本地
# 镜像、不再走节点慢拉。local-up 含配置初始化，需 new-api 与 ragflow Running，故二者一并预载。
# redis 走 ACR、minio 走 pgsty，节点直拉无瓶颈，不入此列。宿主 docker 镜像缓存跨 local-reset
# （删集群）保留，首拉成功后续仅做秒级导入。
LOCAL_PRELOAD_IMAGES := busybox:1.36 mysql:8.0 elasticsearch:8.11.3 calciumion/new-api:latest infiniflow/ragflow:v0.25.6

local-preload: # 内部：local-up 调用——宿主拉取重镜像并 k3d image import 灌入集群（规避节点慢拉导致 rollout 超时）
	@for img in $(LOCAL_PRELOAD_IMAGES); do \
		echo "==> 预载 $$img"; \
		docker image inspect $$img >/dev/null 2>&1 || docker pull $$img || exit 1; \
		k3d image import $$img -c $(K3D_CLUSTER) || exit 1; \
	done
	@echo "✅ 基础镜像已 import 到集群"

local-up: cluster-create local-build ## 一键拉起本地全栈（建集群→构建镜像→预载镜像→部署→建桶→种子管理员）
	# 1) namespace + secret 先行
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/00-namespace.yaml
	$(KUBECTL) apply -f $(SECRET_FILE)
	# 1.1) CoreDNS 外网解析补丁：k3d 默认上游对外网域名 SERVFAIL；网页提取需要解析任意
	#      站点，故将主 Corefile 的默认上游替换为 pod 可达的阿里公共 DNS。此时仅系统 pod
	#      在跑，重启 coredns 干扰最小；后端/业务 pod 随后才拉起，DNS 已就绪。
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/coredns-custom.yaml
	@$(KUBECTL) -n kube-system get configmap coredns -o json | \
		jq '.data.Corefile |= sub("forward \\. /etc/resolv\\.conf"; "forward . 223.5.5.5 223.6.6.6")' | \
		$(KUBECTL) apply -f -
	# 1.1.1) host.k3d.internal 解析补丁：k3d 集群重启后 CoreDNS NodeHosts 会丢失
	#      host.k3d.internal（k3d 已知问题），令经宿主代理出网的 new-api 解析该名失败，
	#      表现为对话/出网整体 500。coredns-custom 是持久 ConfigMap、每次 CoreDNS reload
	#      都会导入，故在此追加一个静态 hosts server block 兜住重启后的解析。网关 IP 随
	#      集群重建可能变化（曾见 172.18/172.19），故运行时动态探测 k3d 网络网关注入，
	#      不写死；必须在上面的 apply 之后执行——apply 按 last-applied 会剪除文件外的键。
	@GW=$$(docker network inspect k3d-$(K3D_CLUSTER) -f '{{range .IPAM.Config}}{{.Gateway}}{{end}}') && \
		echo "   host.k3d.internal -> $$GW" && \
		$(KUBECTL) patch configmap coredns-custom -n kube-system --type merge -p \
		"{\"data\":{\"host-k3d.server\":\"host.k3d.internal:53 {\n    hosts {\n        $$GW host.k3d.internal\n        fallthrough\n    }\n}\n\"}}"
	-$(KUBECTL) -n kube-system rollout restart deploy/coredns
	-$(KUBECTL) -n kube-system rollout status deploy/coredns --timeout=60s
	# 1.2) Traefik 入口超时补丁：web entrypoint readTimeout 默认 60s，叠加 manager 上传
	#      限速（512KB/s）会导致 >~30MB 的知识库文件读 body 超时被掐断（详见该文件注释）。
	#      用 HelmChartConfig 把 readTimeout 调到 600s；helm-controller 重跑 install job 生效。
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/traefik-config.yaml
	# 1.3) 预载重镜像到节点：节点 containerd 经镜像源拉 mysql/es/ragflow 等大镜像极慢，
	#      会导致下面 rollout status 超时；改由宿主拉取后灌入节点，pod 调度即命中本地镜像。
	$(MAKE) local-preload
	# 2) 有状态后端，等就绪
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/mysql.yaml \
		-f $(K8S_LOCAL_DIR)/redis.yaml \
		-f $(K8S_LOCAL_DIR)/elasticsearch.yaml \
		-f $(K8S_LOCAL_DIR)/minio.yaml
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/mysql --timeout=900s
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/redis --timeout=300s
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/minio --timeout=300s
	$(KUBECTL) -n $(K8S_NS) rollout status statefulset/elasticsearch --timeout=600s
	# 3) 建 MinIO bucket
	$(MAKE) local-mc-init
	# 4) 控制面 / 业务 + RBAC + Ingress
	$(KUBECTL) apply -f $(K8S_LOCAL_DIR)/manager-rbac.yaml \
		-f $(K8S_LOCAL_DIR)/manager-api.yaml \
		-f $(K8S_LOCAL_DIR)/manager-web.yaml \
		-f $(K8S_LOCAL_DIR)/new-api.yaml \
		-f $(K8S_LOCAL_DIR)/ragflow.yaml \
		-f $(K8S_LOCAL_DIR)/ingress.yaml
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-api --timeout=300s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/manager-web --timeout=300s
	# 5) 等 new-api 与 ragflow 就绪：配置初始化依赖二者 Running（ragflow 首启需下
	#    tiktoken/模型，给足超时）。
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/new-api --timeout=600s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/ragflow --timeout=900s
	# 6) 种子平台管理员（幂等）
	$(MAKE) local-seed
	# 7) 配置初始化：new-api 渠道/令牌 + RAGFlow 模型 + manager secret 回填并重启
	#    （读 .env 厂商 key，幂等；脚本内部还会再等 new-api/RAGFlow 接口就绪）。
	$(MAKE) local-init-models
	@echo "✅ 本地全栈就绪并已完成配置初始化："
	@echo "   manager 控制台 http://ocm.localhost"
	@echo "   new-api 后台    http://newapi.localhost"
	@echo "   ragflow 控制台  http://ragflow.localhost"

# local-mc-init：在 minio 容器内建 app/ragflow bucket（幂等）。local-up 自动调用，无需手动，故不在 help 列出。
local-mc-init:
	$(KUBECTL) -n $(K8S_NS) exec statefulset/minio -- sh -c '\
		mc alias set local http://127.0.0.1:9000 "$$MINIO_ROOT_USER" "$$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; \
		mc mb -p local/oc-apps; mc mb -p local/ragflow; mc ls local'

# local-migrate：kubectl exec manager-api 跑迁移（默认 up；DOWN=1 则回滚一次）。
# 迁移已由 manager-api 启动时自动执行，一般无需手动；需手动迁移用 migrate-up / migrate-down，故不在 help 列出。
local-migrate:
	@if [ "$(DOWN)" = "1" ]; then \
		$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- migrate down; \
	else \
		$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- migrate up; \
	fi

# local-seed：kubectl exec manager-api 种子平台管理员 admin/admin123（幂等）。local-up 自动调用，无需手动，故不在 help 列出。
local-seed:
	$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- seed-admin admin admin123

# local-init-models：初始化 new-api/RAGFlow 模型与管理 token（读 .env 厂商 key，幂等）。
# local-up 末尾自动执行，改 .env 后可单独重跑（make local-init-models），故不在 help 列出。
local-init-models:
	python3 scripts/local-init-models.py
	# 脚本把新生成的 newapi.admin_token / ragflow.api_key 回填进 git 跟踪的 secret.yaml，
	# 每次 local-up / local-reset 后这两行都会变、留下脏工作区。此处自动提交「仅该文件」的变更
	# （path-scoped，不裹挟其它改动），保持工作区干净、token 随库走。无变更则跳过、不产生空提交。
	@if [ -n "$$(git status --porcelain -- $(SECRET_FILE))" ]; then \
		git commit -q -m "chore(local): 回填 new-api/ragflow token 到本地 secret.yaml" -- $(SECRET_FILE) && \
		echo "✅ 已自动提交 secret.yaml 的 token 回填"; \
	else \
		echo "ℹ️  secret.yaml 无变更，跳过自动提交"; \
	fi

local-seed-e2e: ## kubectl exec manager-api 注入 Playwright e2e fixture（OCM_E2E=1 守门），打印 fixture JSON
	@$(KUBECTL) -n $(K8S_NS) exec deploy/manager-api -- env OCM_E2E=1 seed-e2e

local-status: ## 查看本地集群 pod / ingress 状态
	$(KUBECTL) -n $(K8S_NS) get pods,svc,ingress

local-logs: ## tail 指定服务日志（用法：make local-logs svc=manager-api）
	$(KUBECTL) -n $(K8S_NS) logs -f deploy/$(svc)

local-shell: ## 进入指定服务容器（用法：make local-shell svc=manager-api）
	$(KUBECTL) -n $(K8S_NS) exec -it deploy/$(svc) -- sh

##@ 测试 / 静态检查

test: test-hermes-version-guard ## 跑 Hermes 版本守卫回归测试和 Go 单元测试 (go test ./...)
	go test ./...

test-hermes-version-guard: ## 跑普通 Hermes 上游 ref 版本守卫回归测试
	./scripts/hermes-version-guard_test.sh

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

e2e-quick: ## 无头运行一分钟内核心 Playwright 冒烟
	cd web && npm run test:e2e:quick

e2e-regression: ## 无头并行运行全部确定性 Playwright 回归
	cd web && npm run test:e2e:regression

e2e-slow: ## 显式运行真实模型、RAG 与破坏性专项慢测
	cd web && npm run test:e2e:slow

##@ Hermes runtime 镜像

hermes-inject-contract: .guard-hermes-version ## 把 HERMES_VARIANT 指定变体的 ocops 契约工件注入目录
	rm -rf $(HERMES_VARIANT_DIR)/ocops-contract $(HERMES_VARIANT_DIR)/kanban-contract $(HERMES_VARIANT_DIR)/cron-contract
	cp -r runtime/hermes/ocops-contract $(HERMES_VARIANT_DIR)/ocops-contract

build-hermes-runtime: hermes-inject-contract ## 本地 dev 构建 hermes runtime（需 HERMES_VARIANT 指定变体）
	status=0; \
	docker build $(HERMES_BUILD_FLAGS) \
	  -t "hermes-runtime:$(HERMES_VERSION)-dev" \
	  --build-arg "HERMES_REF=$(HERMES_UPSTREAM_REF)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
	rm -rf $(HERMES_VARIANT_DIR)/ocops-contract $(HERMES_VARIANT_DIR)/kanban-contract $(HERMES_VARIANT_DIR)/cron-contract; \
	exit $$status

.PHONY: aicc-runtime-inject-contract
aicc-runtime-inject-contract: .guard-aicc-runtime-version ## 注入客服专用 runtime 所需的 ocops 契约工件
	rm -rf $(AICC_RUNTIME_DIR)/ocops-contract $(AICC_RUNTIME_DIR)/kanban-contract $(AICC_RUNTIME_DIR)/cron-contract
	cp -r runtime/hermes/ocops-contract $(AICC_RUNTIME_DIR)/ocops-contract

.PHONY: build-aicc-runtime
build-aicc-runtime: .guard-image-git-state aicc-runtime-inject-contract ## 构建并推送客服专用 Hermes runtime 镜像
	status=0; \
	docker build $(HERMES_BUILD_FLAGS) \
	  -t "$(AICC_RUNTIME_IMAGE)" \
	  --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) \
	  --build-arg "HERMES_REF=$(AICC_RUNTIME_HERMES_REF)" \
	  --build-arg "OC_IMAGE_VARIANT=hermes-aicc" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(AICC_RUNTIME_DIR) || status=$$?; \
	rm -rf $(AICC_RUNTIME_DIR)/ocops-contract $(AICC_RUNTIME_DIR)/kanban-contract $(AICC_RUNTIME_DIR)/cron-contract; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	docker push "$(AICC_RUNTIME_IMAGE)"
	@echo "✅ AICC runtime 镜像 $(AICC_RUNTIME_IMAGE) 已构建并推送"

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
	docker build $(HERMES_BUILD_FLAGS) \
	  -t "$(HERMES_IMAGE)" \
	  --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) \
	  --build-arg "HERMES_REF=$(HERMES_UPSTREAM_REF)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
	rm -rf $(HERMES_VARIANT_DIR)/ocops-contract $(HERMES_VARIANT_DIR)/kanban-contract $(HERMES_VARIANT_DIR)/cron-contract; \
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

# prod-update-config 把改好的线上 secret 推到集群并让 manager-api 加载新配置：
# 1) apply secret.yaml 更新 Secret 对象（含 manager.yaml 完整配置、外部后端连接、ACR 凭证）；
# 2) rollout restart 重启 manager-api，重新挂载并加载新配置（进程不热重载配置文件，必须重启）。
# apply 不带 -n：secret.yaml 是多文档（ocm-secrets + ocm/oc-apps 两个 acr-pull），各对象自带
# namespace，指定 -n 会与声明 oc-apps 的对象冲突；restart/status 才用带 -n ocm 的 PROD_KUBECTL。
.PHONY: prod-update-config
prod-update-config: ## apply 线上 secret.yaml 并重启 manager-api 使新配置生效
	kubectl --kubeconfig $(PROD_KUBECONFIG) apply -f deploy/k8s/prod/secret.yaml
	$(PROD_KUBECTL) rollout restart deploy/manager-api
	$(PROD_KUBECTL) rollout status deploy/manager-api --timeout=$(PROD_ROLLOUT_TIMEOUT)
	@echo "✅ 线上 secret 已更新，manager-api 已重启加载新配置"

# ROLLOUT_SITE_SERVER：罐头配方——把 site-server（oc-apps namespace）的镜像 set 成本次
# manager-api 的 $(IMAGE_TAG) 并等滚动完成。site-server 与 manager-api 共用同一镜像（同一 CI 产物）。
# 供 prod-deploy-site-server 与 prod-deploy-manager 共用：用 define 内联而非递归 $(MAKE)，
# 因为 IMAGE_TAG 含构建时间戳、子 make 会重算导致 tag 漂移到一个未推送的值。
# site-server 不存在（未启用 web-publish / 未首次 apply）时跳过而非报错，避免无关环境部署失败。
define ROLLOUT_SITE_SERVER
@if $(PROD_KUBECTL_APPS) get deploy/site-server >/dev/null 2>&1; then \
	$(PROD_KUBECTL_APPS) set image deploy/site-server site-server=$(API_IMAGE_REPO):$(IMAGE_TAG); \
	$(PROD_KUBECTL_APPS) rollout status deploy/site-server --timeout=$(PROD_ROLLOUT_TIMEOUT); \
	echo "✅ 线上 site-server 已更新为 $(API_IMAGE_REPO):$(IMAGE_TAG)"; \
else \
	echo "ℹ️  site-server 未部署，跳过（首次部署先 kubectl --kubeconfig $(PROD_KUBECONFIG) apply -f deploy/k8s/prod/site-server.yaml）"; \
fi
endef

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

# prod-deploy-site-server：site-server 与 manager-api 共用同一镜像，部署在 oc-apps namespace。
# 构建推送 manager-api 镜像后，set image 滚动更新 site-server（首次部署仍需先 kubectl apply
# deploy/k8s/prod/site-server.yaml 创建 Deployment/Service，本目标只做镜像滚动更新）。
.PHONY: prod-deploy-site-server
prod-deploy-site-server: build-api-image ## 构建推送 manager-api 镜像并滚动更新线上 site-server（oc-apps）
	$(ROLLOUT_SITE_SERVER)

# prod-deploy-manager 一次性发布 api + web：先完成两镜像的构建推送（共享同一
# IMAGE_TAG，保证同源），再连续 set image 两个 Deployment，最后各自等滚动完成，
# 避免分两次调用导致 tag 不一致或多轮无谓重启。
# site-server 与 manager-api 同镜像，一并滚动更新到同一 IMAGE_TAG（未部署则自动跳过）。
.PHONY: prod-deploy-manager
prod-deploy-manager: build-api-image build-web-image ## 构建推送 api+web 并一次性滚动更新线上 manager（含 site-server）
	$(PROD_KUBECTL) set image deploy/manager-api manager-api=$(API_IMAGE_REPO):$(IMAGE_TAG)
	$(PROD_KUBECTL) set image deploy/manager-web manager-web=$(WEB_IMAGE_REPO):$(IMAGE_TAG)
	$(PROD_KUBECTL) rollout status deploy/manager-api --timeout=$(PROD_ROLLOUT_TIMEOUT)
	$(PROD_KUBECTL) rollout status deploy/manager-web --timeout=$(PROD_ROLLOUT_TIMEOUT)
	@echo "✅ 线上 manager 已更新：api=$(API_IMAGE_REPO):$(IMAGE_TAG) web=$(WEB_IMAGE_REPO):$(IMAGE_TAG)"
	$(ROLLOUT_SITE_SERVER)

# prod-deploy-hermes / prod-deploy-ops：hermes/ops 镜像不是独立 Deployment（由 manager
# 渲染 app pod 时引用），故发版方式与 api/web 不同——不能 set image，而是把新镜像 ref 写回
# 本地 secret.yaml 的对应字段（hermes→hermes.runtime_images[].ref，ops→k8s.ops_image），
# 再走 prod-update-config（apply secret + 重启 manager-api）让新镜像在后续渲染的 app pod 生效。
# 镜像仓库迁到同区移动云后节点拉取快，不再需要预热 DaemonSet。
.PHONY: prod-deploy-hermes
prod-deploy-hermes: .prod-deploy-hermes-one ## 构建推送单个 hermes 镜像(HERMES_VARIANT)→写回 secret.yaml→prod-update-config 生效
	$(MAKE) prod-update-config

# .prod-deploy-hermes-one：构建推送当前 HERMES_VARIANT 镜像并把对应版本那条 ref 写回 secret.yaml，
# 但不触发 prod-update-config。拆出此内部 target 是为了让单变体 prod-deploy-hermes 与全量
# prod-deploy-hermes-all 共用构建+写回逻辑：后者遍历全部 variant 时只在末尾统一 prod-update-config
# 一次，避免每个 variant 都 apply secret + 重启 manager-api 一遍（重启一次即可让全部新 ref 生效）。
.PHONY: .prod-deploy-hermes-one
.prod-deploy-hermes-one: build-hermes-image
	# 只替换 tag 版本号为本次部署版本（$(HERMES_VERSION)）的那条 ref，保留 runtime_images
	# 列表中其它版本条目不动；否则多版本列表会被全部覆盖成同一镜像（曾误伤旧版回退条目）。
	sed -i -E 's#ref: "[^"]*oc-manager-aigowork:$(HERMES_VERSION_RE)-[^"]*"#ref: "$(HERMES_IMAGE)"#' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 hermes 镜像（$(HERMES_VERSION)）已更新为 $(HERMES_IMAGE)"

# prod-deploy-hermes-all：遍历 runtime/hermes/ 下全部 versioned variant（HERMES_VARIANTS_ALL），
# 逐个构建推送镜像并把各自版本的 ref 写回 secret.yaml，最后只 prod-update-config 一次。
# 每个 variant 用子 make 递归覆盖 HERMES_VARIANT，使 HERMES_VERSION / HERMES_IMAGE 等派生变量
# 按该 variant 的 version.txt 重新求值（这些变量在 Makefile 解析期即绑定，必须靠子 make 重算）。
# 任一 variant 构建失败立即中止（exit 1），不会带着半成品继续 prod-update-config。
.PHONY: prod-deploy-hermes-all
prod-deploy-hermes-all: ## 构建推送全部 hermes variant 镜像→写回 secret.yaml→prod-update-config 生效
	@test -n "$(strip $(HERMES_VARIANTS_ALL))" || { echo "未发现任何 hermes variant（runtime/hermes/hermes-*）" >&2; exit 1; }
	@for v in $(HERMES_VARIANTS_ALL); do \
		echo "—— 部署 hermes variant: $$v ——"; \
		$(MAKE) .prod-deploy-hermes-one HERMES_VARIANT=$$v || exit 1; \
	done
	$(MAKE) prod-update-config
	@echo "✅ 全部 hermes variant 已更新：$(HERMES_VARIANTS_ALL)"

.PHONY: prod-deploy-aicc-runtime
prod-deploy-aicc-runtime: build-aicc-runtime ## 构建客服专用镜像→更新 aicc.runtime_image→逐个升级隐藏应用
	# 限定 aicc 配置段替换，避免误改普通实例 hermes.runtime_images 或 ops_image。
	sed -i -E '/^    aicc:/,/^    [a-z_]+:/{s#^      runtime_image: "[^"]*"#      runtime_image: "$(AICC_RUNTIME_IMAGE)"#;}' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 AICC runtime 镜像已更新为 $(AICC_RUNTIME_IMAGE)"
	$(MAKE) prod-update-config

.PHONY: prod-deploy-ops
prod-deploy-ops: build-ops-runtime ## 构建推送 ops 镜像→写回 secret.yaml→prod-update-config 生效
	sed -i -E 's#ops_image: "[^"]*oc-manager-ops:[^"]*"#ops_image: "$(OPS_IMAGE_REPO):$(IMAGE_TAG)"#' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 ops 镜像已更新为 $(OPS_IMAGE_REPO):$(IMAGE_TAG)"
	$(MAKE) prod-update-config

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

##@ 数据库迁移

migrate-up: ## 对本地 k3d 数据库执行 up 迁移（= local-migrate）
	$(MAKE) local-migrate

migrate-down: ## 回滚本地 k3d 最近一次迁移（= local-migrate DOWN=1）
	$(MAKE) local-migrate DOWN=1

##@ 部署 / 运维

# seed-e2e：在 manager-api 容器里跑 cmd/seed-e2e，OCM_E2E=1 守门。
# 会 TRUNCATE e2e 业务表后重建 fixture，stdout 末行是 fixture JSON 供 Playwright 解析。
seed-e2e: ## 注入 Playwright e2e fixture（= local-seed-e2e）
	$(MAKE) local-seed-e2e

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

##@ Git 远程同步

# 以下命令统一针对「当前所在分支」(BRANCH 运行时取 git rev-parse)，不写死 master。
# 设计取舍: pull 只做 fast-forward、push 默认 ff; 两边分叉时一律停下并打印手动
# force 命令，绝不自动 force，避免误覆盖对端独有提交。详见
# docs/superpowers/specs/2026-06-14-makefile-dual-remote-sync-design.md。

# 私有: 从 REMOTE 拉当前分支并 fast-forward 本地。要求工作区干净，避免 merge
# 触及未提交改动; 分叉无法快进时停下并提示以远程为准的手动命令。
.PHONY: .git-pull
.git-pull:
	@BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	 git diff --quiet && git diff --cached --quiet || { echo "❌ 工作区有未提交改动，请先提交或暂存再 pull" >&2; exit 1; }; \
	 echo "⬇️  fetch $(REMOTE) $$BRANCH"; \
	 git fetch $(REMOTE) $$BRANCH; \
	 git merge --ff-only $(REMOTE)/$$BRANCH || { \
	   echo "❌ 本地 $$BRANCH 与 $(REMOTE)/$$BRANCH 已分叉，无法快进。" >&2; \
	   echo "   如确认以远程为准，手动执行: git reset --hard $(REMOTE)/$$BRANCH" >&2; \
	   exit 1; }

# 私有: 把当前分支推到 REMOTE。push 被拒(远程已分叉)时停下并提示 force 命令。
.PHONY: .git-push
.git-push:
	@BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	 echo "⬆️  push $(REMOTE) $$BRANCH"; \
	 git push $(REMOTE) $$BRANCH || { \
	   echo "❌ 推送被拒，远程 $(REMOTE) 可能已分叉。" >&2; \
	   echo "   如确认以本地为准，手动执行: git push --force-with-lease $(REMOTE) $$BRANCH" >&2; \
	   exit 1; }

# 私有: 经本地当前分支把 FROM 同步到 TO(先 ff 拉 FROM，再推 TO)，任一步失败即中止。
.PHONY: .git-sync
.git-sync:
	@$(MAKE) --no-print-directory .git-pull REMOTE=$(FROM)
	@$(MAKE) --no-print-directory .git-push REMOTE=$(TO)

.PHONY: pull-github
pull-github: ## 从 github 拉当前分支到本地(ff-only)
	@$(MAKE) --no-print-directory .git-pull REMOTE=$(GITHUB_REMOTE)

.PHONY: push-github
push-github: ## 把本地当前分支推到 github
	@$(MAKE) --no-print-directory .git-push REMOTE=$(GITHUB_REMOTE)

.PHONY: pull-origin
pull-origin: ## 从 origin 拉当前分支到本地(ff-only)
	@$(MAKE) --no-print-directory .git-pull REMOTE=$(ORIGIN_REMOTE)

.PHONY: push-origin
push-origin: ## 把本地当前分支推到 origin
	@$(MAKE) --no-print-directory .git-push REMOTE=$(ORIGIN_REMOTE)

push-all:              ## 把本地当前分支同时推到 github 和 origin
	@$(MAKE) --no-print-directory .git-push REMOTE=$(GITHUB_REMOTE)
	@$(MAKE) --no-print-directory .git-push REMOTE=$(ORIGIN_REMOTE)

.PHONY: sync-github-to-origin
sync-github-to-origin: ## 同步 github 当前分支到 origin(经本地)
	@$(MAKE) --no-print-directory .git-sync FROM=$(GITHUB_REMOTE) TO=$(ORIGIN_REMOTE)

.PHONY: sync-origin-to-github
sync-origin-to-github: ## 同步 origin 当前分支到 github(经本地)
	@$(MAKE) --no-print-directory .git-sync FROM=$(ORIGIN_REMOTE) TO=$(GITHUB_REMOTE)

# 自动判向: 按祖先关系判定两个远程哪个更新，把更新方 pull 到本地再 push 到另一方。
# 两边相等则免操作; 真分叉(互不为祖先)时无法自动判定，停下并提示改用显式 sync-*。
.PHONY: sync-remotes
sync-remotes: ## 自动判定两远程哪个更新并双向对齐(当前分支)
	@BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	 git diff --quiet && git diff --cached --quiet || { echo "❌ 工作区有未提交改动，请先提交或暂存再同步" >&2; exit 1; }; \
	 echo "⬇️  fetch $(GITHUB_REMOTE)/$(ORIGIN_REMOTE) $$BRANCH"; \
	 git fetch $(GITHUB_REMOTE) $$BRANCH; \
	 git fetch $(ORIGIN_REMOTE) $$BRANCH; \
	 G=$$(git rev-parse $(GITHUB_REMOTE)/$$BRANCH); \
	 O=$$(git rev-parse $(ORIGIN_REMOTE)/$$BRANCH); \
	 if [ "$$G" = "$$O" ]; then echo "✅ 两个远程 $$BRANCH 已一致，无需同步"; exit 0; fi; \
	 if git merge-base --is-ancestor $$G $$O; then \
	   echo "ℹ️  origin 领先，以 origin 为更新方 → 同步到 github"; NEWER=$(ORIGIN_REMOTE); OTHER=$(GITHUB_REMOTE); \
	 elif git merge-base --is-ancestor $$O $$G; then \
	   echo "ℹ️  github 领先，以 github 为更新方 → 同步到 origin"; NEWER=$(GITHUB_REMOTE); OTHER=$(ORIGIN_REMOTE); \
	 else \
	   echo "❌ 两个远程 $$BRANCH 已分叉，互不为祖先，无法自动判定更新方。" >&2; \
	   echo "   $(GITHUB_REMOTE)/$$BRANCH = $$G" >&2; \
	   echo "   $(ORIGIN_REMOTE)/$$BRANCH = $$O" >&2; \
	   echo "   请先人工确认以哪方为准，再用 make sync-github-to-origin 或 make sync-origin-to-github。" >&2; \
	   exit 1; \
	 fi; \
	 $(MAKE) --no-print-directory .git-pull REMOTE=$$NEWER && \
	 $(MAKE) --no-print-directory .git-push REMOTE=$$OTHER
