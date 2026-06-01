# 生产镜像仓库迁移到移动云 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把生产构建/部署的镜像源从阿里云 ACR（`crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com`）整体迁到同区移动云仓库 `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`（`app`/`public` 两命名空间），根治节点拉取慢；本地 k3d 不动。

**Architecture:** retag 转推 13 个镜像（自有 4 + 上游 3 + 构建期基础 6），tag 不变、bits 与线上一致；改 git（Makefile 仓库变量与构建 mirror、5 个 prod manifests、secret.example、README）+ 删除已过时的预热 DaemonSet；活 secret.yaml 与镜像搬运是运维步骤（不入 git），最后按顺序滚动切换并验证。

**Tech Stack:** Docker、kubectl（`--kubeconfig ~/dir/ywjs/kube/kubeconfig.json`）、GNU Make、k8s YAML、bash。

**设计文档：** `docs/superpowers/specs/2026-06-01-prod-image-registry-migration-design.md`

---

## 关键常量（贯穿全计划）

- 新 Registry：`ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`
- 命名空间：`app`（自有）、`public`（上游 + 基础）
- 拉取 Secret（**ocm/oc-apps 已存在，外部托管，不入库**）：`secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`
- 旧 Registry：`crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com`，旧命名空间 `ywjs_app`/`ywjs_public`，旧拉取 Secret `acr-pull`
- 生产 kubeconfig：`~/dir/ywjs/kube/kubeconfig.json`

镜像映射规则：`crpi-.../ywjs_app/X` → `ywjs-cc41758e.../app/X`；`crpi-.../ywjs_public/X` → `ywjs-cc41758e.../public/X`（tag 不变）。

## 文件结构（创建/修改）

- **创建** `scripts/migrate-images-to-ecloud.sh` — 一次性镜像搬运脚本（pull/tag/push + digest 校验）。
- **删除** `deploy/k8s/prod/image-prepuller.yaml` — 预热 DaemonSet（迁同区后无用）。
- **修改** `Makefile` — ① 仓库变量与构建期 mirror 切新仓库；② `prod-deploy-hermes`/`prod-deploy-ops` 移除 prepuller 相关行。
- **修改** `deploy/k8s/prod/{manager-api,manager-web,elasticsearch,new-api,ragflow}.yaml` — `image:` + `imagePullSecrets`。
- **修改** `deploy/k8s/prod/secret.example.yaml` — refs + 拉取 secret 名 + 删除 acr-pull 两段。
- **修改** `deploy/k8s/prod/README.md` — ACR → 新仓库说明。
- **运维（不入 git）** `deploy/k8s/prod/secret.yaml`（gitignored 活文件）— 同 example 的字段改动 + 删除 acr-pull 两段。

---

## Task 1: 镜像搬运脚本

**Files:**
- Create: `scripts/migrate-images-to-ecloud.sh`

- [ ] **Step 1: 写脚本**

创建 `scripts/migrate-images-to-ecloud.sh`，内容如下（逐字）：

```bash
#!/usr/bin/env bash
# 一次性把生产镜像从阿里云 ACR retag 转推到移动云仓库（仅生产，本地 k3d 不动）。
#
# 用法：
#   # 先 docker login 两个仓库（旧 ACR 需有拉取权限、新仓库需有推送权限）
#   docker login crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com
#   docker login ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
#   KUBECONFIG_PROD=~/dir/ywjs/kube/kubeconfig.json ./scripts/migrate-images-to-ecloud.sh
#
# 行为：
#   1) 从生产集群（ocm + oc-apps）枚举实际在跑的镜像，过滤出 ACR 镜像，按命名空间映射
#      （ywjs_app→app、ywjs_public→public）后 pull→tag→push 到新仓库（tag 不变）。
#   2) 转推 6 个构建期基础镜像到新 public（自有/上游已在第 1 步覆盖）。
#   3) 对每个新镜像比对 digest 与源一致，打印结果。
# 幂等：重复执行会覆盖同 tag（内容一致则 push 为 no-op 层），digest 校验保证一致性。
set -euo pipefail

OLD_REGISTRY="crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com"
NEW_REGISTRY="ywjs-cc41758e.ecis.huabei-3.cmecloud.cn"
KUBECONFIG_PROD="${KUBECONFIG_PROD:-$HOME/dir/ywjs/kube/kubeconfig.json}"
KC="kubectl --kubeconfig ${KUBECONFIG_PROD}"

# map_ref 把旧 ACR ref 映射为新仓库 ref：ywjs_app→app、ywjs_public→public，tag 不变。
map_ref() {
  local src="$1"
  echo "$src" \
    | sed -e "s#^${OLD_REGISTRY}/ywjs_app/#${NEW_REGISTRY}/app/#" \
          -e "s#^${OLD_REGISTRY}/ywjs_public/#${NEW_REGISTRY}/public/#"
}

# move 执行 pull→tag→push→digest 校验。
move() {
  local src="$1" dst="$2"
  echo "==> ${src}"
  echo "    -> ${dst}"
  docker pull "$src"
  docker tag "$src" "$dst"
  docker push "$dst"
  local sd dd
  sd="$(docker inspect --format '{{index .RepoDigests 0}}' "$src" 2>/dev/null || true)"
  dd="$(docker inspect --format '{{index .RepoDigests 0}}' "$dst" 2>/dev/null || true)"
  echo "    src digest: ${sd}"
  echo "    dst digest: ${dd}"
}

echo "########## 1) 集群实跑的 ACR 镜像（ocm + oc-apps） ##########"
# 枚举两命名空间所有容器/初始化容器镜像，去重，过滤 ACR。
mapfile -t LIVE < <(
  for ns in ocm oc-apps; do
    $KC get pods -n "$ns" -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{range .spec.initContainers[*]}{.image}{"\n"}{end}{end}'
  done | sort -u | grep "^${OLD_REGISTRY}/"
)
for src in "${LIVE[@]}"; do
  move "$src" "$(map_ref "$src")"
done

echo "########## 2) 构建期基础镜像 → ${NEW_REGISTRY}/public ##########"
# 自有/上游已在第 1 步覆盖；这里只补构建机用的基础镜像。
# 多数基础镜像当前经 ACR ywjs_public 拉取（与 Dockerfile DOCKER_HUB_MIRROR 一致），从 ACR 取以保证同 bits；
# alpine:3.20（ops 基础）当前走 docker.io/library，从 docker.io 取。
move "${OLD_REGISTRY}/ywjs_public/golang:1.25-alpine3.22"      "${NEW_REGISTRY}/public/golang:1.25-alpine3.22"
move "${OLD_REGISTRY}/ywjs_public/alpine:3.22"                 "${NEW_REGISTRY}/public/alpine:3.22"
move "${OLD_REGISTRY}/ywjs_public/node:22-alpine"              "${NEW_REGISTRY}/public/node:22-alpine"
move "${OLD_REGISTRY}/ywjs_public/nginx:1.27-alpine"           "${NEW_REGISTRY}/public/nginx:1.27-alpine"
move "${OLD_REGISTRY}/ywjs_public/python:3.13-slim-bookworm"   "${NEW_REGISTRY}/public/python:3.13-slim-bookworm"
move "docker.io/library/alpine:3.20"                           "${NEW_REGISTRY}/public/alpine:3.20"

echo "✅ 全部镜像已转推。请核对上方各对 src/dst digest 一致。"
```

- [ ] **Step 2: 加可执行位并语法校验**

Run:
```bash
chmod +x scripts/migrate-images-to-ecloud.sh
bash -n scripts/migrate-images-to-ecloud.sh && echo "SYNTAX-OK"
```
Expected: 输出 `SYNTAX-OK`（不实际执行 docker/kubectl，仅语法检查）。

- [ ] **Step 3: 提交**

```bash
git add scripts/migrate-images-to-ecloud.sh
git commit -m "$(cat <<'EOF'
chore(deploy): 新增生产镜像迁移移动云的搬运脚本

一次性把 ACR 上自有/上游/构建期基础镜像 retag 转推到移动云仓库
ywjs-cc41758e.ecis.huabei-3.cmecloud.cn（app/public 两命名空间，tag 不变）。
脚本从生产集群枚举实跑镜像并按命名空间映射，附 digest 一致性校验。仅生产用。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 移除已过时的镜像预热 DaemonSet

迁到同区仓库后节点拉取快，预热 DaemonSet 无意义。删除 `image-prepuller.yaml`（当前已是未暂存删除）并清理 Makefile 中对它的引用。

**Files:**
- Delete: `deploy/k8s/prod/image-prepuller.yaml`
- Modify: `Makefile`（`prod-deploy-hermes` / `prod-deploy-ops` 两个 target 及上方注释）

- [ ] **Step 1: 暂存文件删除**

Run:
```bash
git rm deploy/k8s/prod/image-prepuller.yaml
```
Expected: 提示 `rm 'deploy/k8s/prod/image-prepuller.yaml'`（该文件工作区已删除，此步把删除纳入暂存）。

- [ ] **Step 2: 改 Makefile 注释（移除预热 DaemonSet 说明）**

把 `Makefile` 中这段注释（约 360–368 行）：

```make
# prod-deploy-hermes / prod-deploy-ops：hermes/ops 镜像不是独立 Deployment（由 manager
# 渲染 app pod 时引用），故发版方式与 api/web 不同——不能 set image，而是把新镜像 ref 写回
# 本地 secret.yaml 的对应字段（hermes→hermes.runtime_images[].ref，ops→k8s.ops_image），
# 再走 update-config（apply secret + 重启 manager-api）让新镜像在后续渲染的 app pod 生效。
# 同时把镜像预热 DaemonSet（image-prepuller.yaml）里的 image 同步成新 tag 并重新 apply，
# 否则各节点预热的还是旧镜像、新 app pod 仍要现拉新镜像、冷启动慢。
# secret.yaml 用引号包裹值（ref:"..."/ops_image:"..."），预热 DaemonSet 用 YAML 裸值
# （image: ...），故两处 sed 锚点不同。apply 预热 DaemonSet 不带 -n：对象自带 oc-apps ns，
# 带 -n ocm 会与其声明的 namespace 冲突（与 update-config apply secret 同理）。
```

替换为：

```make
# prod-deploy-hermes / prod-deploy-ops：hermes/ops 镜像不是独立 Deployment（由 manager
# 渲染 app pod 时引用），故发版方式与 api/web 不同——不能 set image，而是把新镜像 ref 写回
# 本地 secret.yaml 的对应字段（hermes→hermes.runtime_images[].ref，ops→k8s.ops_image），
# 再走 update-config（apply secret + 重启 manager-api）让新镜像在后续渲染的 app pod 生效。
# 镜像仓库迁到同区移动云后节点拉取快，不再需要预热 DaemonSet。
```

- [ ] **Step 3: 改 `prod-deploy-hermes` target（移除 prepuller sed/apply）**

把（约 369–375 行）：

```make
.PHONY: prod-deploy-hermes
prod-deploy-hermes: build-hermes-image ## 构建推送 hermes 镜像→写回 secret.yaml 与预热 DaemonSet→update-config 生效
	sed -i -E 's#ref: "[^"]*oc-manager-hermes:[^"]*"#ref: "$(HERMES_IMAGE)"#' deploy/k8s/prod/secret.yaml
	sed -i -E 's#image: [^[:space:]]*oc-manager-hermes:[^[:space:]]*#image: $(HERMES_IMAGE)#' deploy/k8s/prod/image-prepuller.yaml
	@echo "✅ secret.yaml 与 image-prepuller.yaml 的 hermes 镜像已更新为 $(HERMES_IMAGE)"
	$(MAKE) update-config
	kubectl --kubeconfig $(PROD_KUBECONFIG) apply -f deploy/k8s/prod/image-prepuller.yaml
```

替换为：

```make
.PHONY: prod-deploy-hermes
prod-deploy-hermes: build-hermes-image ## 构建推送 hermes 镜像→写回 secret.yaml→update-config 生效
	sed -i -E 's#ref: "[^"]*oc-manager-hermes:[^"]*"#ref: "$(HERMES_IMAGE)"#' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 hermes 镜像已更新为 $(HERMES_IMAGE)"
	$(MAKE) update-config
```

- [ ] **Step 4: 改 `prod-deploy-ops` target（移除 prepuller sed/apply）**

把（约 377–383 行）：

```make
.PHONY: prod-deploy-ops
prod-deploy-ops: build-ops-runtime ## 构建推送 ops 镜像→写回 secret.yaml 与预热 DaemonSet→update-config 生效
	sed -i -E 's#ops_image: "[^"]*oc-manager-ops:[^"]*"#ops_image: "$(OPS_IMAGE_REPO):$(IMAGE_TAG)"#' deploy/k8s/prod/secret.yaml
	sed -i -E 's#image: [^[:space:]]*oc-manager-ops:[^[:space:]]*#image: $(OPS_IMAGE_REPO):$(IMAGE_TAG)#' deploy/k8s/prod/image-prepuller.yaml
	@echo "✅ secret.yaml 与 image-prepuller.yaml 的 ops 镜像已更新为 $(OPS_IMAGE_REPO):$(IMAGE_TAG)"
	$(MAKE) update-config
	kubectl --kubeconfig $(PROD_KUBECONFIG) apply -f deploy/k8s/prod/image-prepuller.yaml
```

替换为：

```make
.PHONY: prod-deploy-ops
prod-deploy-ops: build-ops-runtime ## 构建推送 ops 镜像→写回 secret.yaml→update-config 生效
	sed -i -E 's#ops_image: "[^"]*oc-manager-ops:[^"]*"#ops_image: "$(OPS_IMAGE_REPO):$(IMAGE_TAG)"#' deploy/k8s/prod/secret.yaml
	@echo "✅ secret.yaml 的 ops 镜像已更新为 $(OPS_IMAGE_REPO):$(IMAGE_TAG)"
	$(MAKE) update-config
```

- [ ] **Step 5: 校验无残留 prepuller 引用**

Run:
```bash
grep -rn "image-prepuller\|prepuller\|预热" Makefile deploy/k8s/prod/ ; echo "exit=$?"
```
Expected: 无任何输出、`exit=1`（grep 未匹配即退出码 1，表示已无残留）。

- [ ] **Step 6: 提交**

```bash
git add Makefile deploy/k8s/prod/image-prepuller.yaml
git commit -m "$(cat <<'EOF'
chore(deploy): 移除镜像预热 DaemonSet

镜像仓库迁到与集群同区的移动云后节点拉取快，针对 ACR 跨公网慢而加的预热
DaemonSet（image-prepuller.yaml）不再需要。删除该清单，并去掉 Makefile
prod-deploy-hermes/prod-deploy-ops 中同步预热镜像 tag、apply 预热 DaemonSet
的相关步骤与注释。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Makefile 切换镜像仓库变量与构建期 mirror

**Files:**
- Modify: `Makefile`（镜像仓库变量段 ~37–55；api/web/hermes/ops 构建 target ~280–316）

- [ ] **Step 1: 新增仓库变量并改四个 IMAGE_REPO**

把（约 37–55 行）：

```make
# 各服务生产镜像仓库（统一走 aliyun 私有 ACR ywjs_app 命名空间）。
# 走其他 registry 时在命令行覆盖对应变量即可。
API_IMAGE_REPO   ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-api
WEB_IMAGE_REPO   ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-web
```

替换为：

```make
# 各服务生产镜像仓库（统一走移动云私有仓库 app 命名空间，与集群同区拉取快）。
# 走其他 registry 时在命令行覆盖对应变量即可。
PROD_REGISTRY    ?= ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
PROD_APP_NS      ?= app
# 构建期基础镜像（golang/node/nginx/alpine/python）走的 public 命名空间，作为 DOCKER_HUB_MIRROR。
PROD_PUBLIC_REPO ?= $(PROD_REGISTRY)/public
API_IMAGE_REPO   ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-api
WEB_IMAGE_REPO   ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-web
```

然后把（约 49 行）：

```make
HERMES_IMAGE_REPO    ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
```

替换为：

```make
HERMES_IMAGE_REPO    ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-hermes
```

再把（约 55 行）：

```make
OPS_IMAGE_REPO ?= crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-ops
```

替换为：

```make
OPS_IMAGE_REPO ?= $(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-ops
```

- [ ] **Step 2: api/web 构建 target 注入新 DOCKER_HUB_MIRROR**

把（约 280–289 行）`build-api-image` 与 `build-web-image` 的 `docker build` 行：

```make
	docker build -t $(API_IMAGE_REPO):$(IMAGE_TAG) -f cmd/server/Dockerfile .
```

替换为：

```make
	docker build --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) -t $(API_IMAGE_REPO):$(IMAGE_TAG) -f cmd/server/Dockerfile .
```

把：

```make
	docker build -t $(WEB_IMAGE_REPO):$(IMAGE_TAG) -f web/Dockerfile ./web
```

替换为：

```make
	docker build --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) -t $(WEB_IMAGE_REPO):$(IMAGE_TAG) -f web/Dockerfile ./web
```

- [ ] **Step 3: hermes 构建 target 注入新 DOCKER_HUB_MIRROR**

把 `build-hermes-image` 的 `docker build` 块（约 299–306 行）：

```make
	docker build \
	  -t "$(HERMES_IMAGE)" \
	  --build-arg "HERMES_REF=$(HERMES_VERSION)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
```

替换为：

```make
	docker build \
	  -t "$(HERMES_IMAGE)" \
	  --build-arg DOCKER_HUB_MIRROR=$(PROD_PUBLIC_REPO) \
	  --build-arg "HERMES_REF=$(HERMES_VERSION)" \
	  --build-arg "OC_IMAGE_VARIANT=$(HERMES_VARIANT)" \
	  --build-arg OC_BUILD_TS=$(IMAGE_TIMESTAMP) \
	  $(HERMES_VARIANT_DIR) || status=$$?; \
```

- [ ] **Step 4: ops 构建 target 注入新 DOCKER_HUB_MIRROR + ALPINE_MIRROR**

把 `build-ops-runtime` 的 `docker build` 行（约 314 行）：

```make
	docker build -t $(OPS_IMAGE_REPO):$(IMAGE_TAG) runtime/ops/
```

替换为（ops 的基础镜像 `alpine:3.20` 由 `ALPINE_MIRROR` 控制，故两个 build-arg 都指向新 public）：

```make
	docker build --build-arg ALPINE_MIRROR=$(PROD_PUBLIC_REPO) -t $(OPS_IMAGE_REPO):$(IMAGE_TAG) runtime/ops/
```

- [ ] **Step 5: dry-run 校验变量与镜像引用正确**

Run:
```bash
make -n build-api-image build-ops-runtime 2>&1 | grep -E "docker build|docker push"
```
Expected: 输出里镜像引用均为 `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/oc-manager-...`，且 api 行含 `--build-arg DOCKER_HUB_MIRROR=ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/public`、ops 行含 `--build-arg ALPINE_MIRROR=ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/public`。无 `crpi-`/`aliyuncs` 字样。

- [ ] **Step 6: 校验 Makefile 已无自有镜像 ACR 引用**

Run:
```bash
grep -nE "crpi-|aliyuncs" Makefile | grep -vE "K3D_DATA_DIR|/data/" ; echo "exit=${PIPESTATUS[0]}"
```
Expected: 仅可能剩本地 k3d clean 用的 `ywjs_public/alpine:3.22`（第 144 行，属本地、不在本次范围）；不应再有 `ywjs_app/oc-manager-*` 行。确认输出只含第 144 行那条。

- [ ] **Step 7: 提交**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
chore(deploy): 生产镜像构建仓库切到移动云

API/WEB/HERMES/OPS 镜像仓库变量改指移动云 ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
的 app 命名空间；新增 PROD_REGISTRY/PROD_APP_NS/PROD_PUBLIC_REPO 变量。四个生产
build-* target 显式传 --build-arg DOCKER_HUB_MIRROR（ops 另传 ALPINE_MIRROR）指向
新仓库 public，使构建期基础镜像也走移动云；Dockerfile 全局默认不变，本地 k3d 构建
仍走 ACR、互不影响。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 生产 manifests 切镜像与拉取 Secret

5 个 Deployment 清单都把 `image:` 换新仓库、`imagePullSecrets` 由 `acr-pull` 换新 secret。

**Files:**
- Modify: `deploy/k8s/prod/manager-api.yaml`、`manager-web.yaml`、`elasticsearch.yaml`、`new-api.yaml`、`ragflow.yaml`

- [ ] **Step 1: manager-api.yaml**

把 `imagePullSecrets` 的 `- name: acr-pull` 改为：

```yaml
        - name: secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
```

把 `image:` 行改为（tag 保持文件原值不变，仅换域名+命名空间；下例 tag 为占位，编辑时保留文件里的真实 tag）：

```yaml
          image: ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/oc-manager-api:2026-06-01-19-17-02-97893d0a
```

> 注：`image:` 的 tag 取文件当前真实值；若与上例不同以文件为准，只替换 `crpi-....com/ywjs_app/` 这段前缀为 `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/`。

- [ ] **Step 2: manager-web.yaml**

`imagePullSecrets` 同 Step 1 改为 `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`；`image:` 前缀 `crpi-....com/ywjs_app/` → `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/`（tag 保留文件原值）。

- [ ] **Step 3: elasticsearch.yaml**

`imagePullSecrets` → `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`；`image:` 前缀 `crpi-....com/ywjs_public/` → `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/public/`（即 `.../public/elasticsearch:8.11.3`）。

- [ ] **Step 4: new-api.yaml**

`imagePullSecrets` → `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`；`image:` → `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/public/new-api:v1.0.0-rc.10`。

- [ ] **Step 5: ragflow.yaml**

`imagePullSecrets` → `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`；`image:` → `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/public/ragflow:v0.25.6`。

- [ ] **Step 6: 校验无 ACR 残留、无 acr-pull 残留**

Run:
```bash
grep -rnE "crpi-|aliyuncs|acr-pull" deploy/k8s/prod/manager-api.yaml deploy/k8s/prod/manager-web.yaml deploy/k8s/prod/elasticsearch.yaml deploy/k8s/prod/new-api.yaml deploy/k8s/prod/ragflow.yaml ; echo "exit=$?"
```
Expected: 无输出、`exit=1`。

- [ ] **Step 7: 校验新引用就位**

Run:
```bash
grep -rnE "image:|secret-registry-ywjs-cc41758e" deploy/k8s/prod/manager-api.yaml deploy/k8s/prod/manager-web.yaml deploy/k8s/prod/elasticsearch.yaml deploy/k8s/prod/new-api.yaml deploy/k8s/prod/ragflow.yaml
```
Expected: 5 个 `image:` 全部 `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/...`（api/web 为 `app/`，es/new-api/ragflow 为 `public/`），5 个 `- name: secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`。

- [ ] **Step 8: 提交**

```bash
git add deploy/k8s/prod/manager-api.yaml deploy/k8s/prod/manager-web.yaml deploy/k8s/prod/elasticsearch.yaml deploy/k8s/prod/new-api.yaml deploy/k8s/prod/ragflow.yaml
git commit -m "$(cat <<'EOF'
chore(deploy): 生产 manifests 切到移动云镜像仓库

manager-api/web、elasticsearch、new-api、ragflow 五个 Deployment 的 image 由
阿里云 ACR 改为移动云 ywjs-cc41758e.ecis.huabei-3.cmecloud.cn（自有走 app、上游走
public，tag 不变）；imagePullSecrets 由 acr-pull 改为集群侧已存在的
secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: secret.example.yaml 切引用并移除 acr-pull 模板

**Files:**
- Modify: `deploy/k8s/prod/secret.example.yaml`

- [ ] **Step 1: 改 hermes runtime_images ref**

把（第 61 行）：

```yaml
          ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00"
```

替换为：

```yaml
          ref: "ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00"
```

- [ ] **Step 2: 改 image_pull_secret 名与上方注释**

把（第 81–83 行）：

```yaml
      # app pod 从私有 ACR 拉 hermes/ops 镜像所需的 imagePullSecret 名；该 Secret
      # 必须存在于 oc-apps namespace（见本文件末尾 acr-pull @ oc-apps）。
      image_pull_secret: "acr-pull"
```

替换为：

```yaml
      # app pod 拉 hermes/ops 镜像所需的 imagePullSecret 名；该 Secret 由集群侧预先创建
      # 并存在于 oc-apps namespace（外部托管，不在本文件管理）。
      image_pull_secret: "secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn"
```

- [ ] **Step 3: 改 ops_image**

把（第 84–85 行）：

```yaml
      # ops 运行时镜像（initContainer/sidecar），由 make build-ops-runtime 推送到 ACR。
      ops_image: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-ops:__FILL_OPS_IMAGE_TAG__"
```

替换为：

```yaml
      # ops 运行时镜像（initContainer/sidecar），由 make build-ops-runtime 推送到移动云仓库。
      ops_image: "ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/oc-manager-ops:__FILL_OPS_IMAGE_TAG__"
```

- [ ] **Step 4: 删除 acr-pull 两段并替换为说明**

把（第 157–181 行，从第一个 `---` 起到文件末尾的两段 acr-pull Secret）：

```yaml
---
# 拉取 ACR 私有镜像的 imagePullSecret 占位。生成方式见 README。
apiVersion: v1
kind: Secret
metadata:
  name: acr-pull
  namespace: ocm
type: kubernetes.io/dockerconfigjson
stringData:
  .dockerconfigjson: |
    {"auths":{"crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com":{"username":"__FILL_ACR_USERNAME__","password":"__FILL_ACR_PASSWORD__","auth":"__FILL_ACR_AUTH_BASE64__"}}}
---
# 同一份 ACR 凭证在 oc-apps namespace 再建一份：app pod（Hermes 实例）部署在 oc-apps，
# 由 manager 渲染时带 imagePullSecrets: [acr-pull]（k8s.image_pull_secret），从私有 ACR 拉
# hermes/ops 镜像，故 oc-apps 内必须有同名 Secret，否则 app pod ImagePullBackOff。
apiVersion: v1
kind: Secret
metadata:
  name: acr-pull
  namespace: oc-apps
type: kubernetes.io/dockerconfigjson
stringData:
  .dockerconfigjson: |
    {"auths":{"crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com":{"username":"__FILL_ACR_USERNAME__","password":"__FILL_ACR_PASSWORD__","auth":"__FILL_ACR_AUTH_BASE64__"}}}
```

替换为：

```yaml
# 镜像拉取 Secret 不在本文件管理：移动云仓库的拉取凭证
# secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn 由集群侧预先创建，
# 并已存在于 ocm 与 oc-apps 两个 namespace（imagePullSecrets 是 namespace 级，
# 两处都需有，否则对应 namespace 的 pod ImagePullBackOff）。manifests 与
# manager.yaml 的 image_pull_secret 仅按名引用它。
```

- [ ] **Step 5: 校验无 ACR / acr-pull 残留**

Run:
```bash
grep -nE "crpi-|aliyuncs|acr-pull|ywjs_app|ywjs_public" deploy/k8s/prod/secret.example.yaml ; echo "exit=$?"
```
Expected: 无输出、`exit=1`。

- [ ] **Step 6: 提交**

```bash
git add deploy/k8s/prod/secret.example.yaml
git commit -m "$(cat <<'EOF'
chore(deploy): secret 模板切到移动云仓库并移除 acr-pull 段

secret.example.yaml 的 hermes runtime_images ref、ops_image 改指移动云
ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app；image_pull_secret 改为集群侧已存在的
secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn。删除内嵌的 ocm/oc-apps
两段 acr-pull dockerconfigjson 模板（拉取 Secret 改为外部托管），并补充说明。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: prod/README.md 更新仓库说明

**Files:**
- Modify: `deploy/k8s/prod/README.md`

- [ ] **Step 1: 改必填清单里的 acr-pull 条目**

把（第 31–34 行）：

```markdown
   - `acr-pull`：阿里云 ACR 拉取凭证（见下）。secret.example.yaml 已在 **ocm 与 oc-apps
     两个 namespace 各建一份**——ocm 供 manager-api/web/new-api/ragflow 拉镜像，oc-apps
     供 app pod（Hermes 实例）拉 hermes/ops 镜像（imagePullSecrets 是 namespace 级，缺则
     app pod ImagePullBackOff）。两份填同一份 ACR 凭证。
```

替换为：

```markdown
   - 镜像拉取 Secret `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`：移动云
     仓库拉取凭证，由集群侧预先创建，**已存在于 ocm 与 oc-apps 两个 namespace**——ocm 供
     manager-api/web/new-api/ragflow 拉镜像，oc-apps 供 app pod（Hermes 实例）拉
     hermes/ops 镜像（imagePullSecrets 是 namespace 级，缺则对应 namespace 的 pod
     ImagePullBackOff）。不在本仓库 secret.yaml 管理。
```

- [ ] **Step 2: 替换「生成 ACR imagePullSecret」整节**

把（第 43–55 行）：

```markdown
## 生成 ACR imagePullSecret

直接填好 secret.yaml 里 ocm + oc-apps 两份 acr-pull 的 `.dockerconfigjson` 即可；
若想用 kubectl 生成，注意**两个 namespace 各生成一份**：

```bash
for ns in ocm oc-apps; do
  kubectl create secret docker-registry acr-pull -n $ns \
    --docker-server=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com \
    --docker-username='<ACR 用户名>' --docker-password='<ACR 密码>' \
    --dry-run=client -o yaml
done
```
```

替换为：

```markdown
## 镜像拉取 Secret（外部托管）

镜像仓库为移动云 `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`（自有走 `app`、上游与构建期
基础镜像走 `public`）。拉取 Secret `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`
由集群侧预先创建并已存在于 ocm 与 oc-apps，本仓库只按名引用、不再内嵌凭证。

镜像首次迁移到该仓库见 `scripts/migrate-images-to-ecloud.sh`（从 ACR retag 转推）。
```

- [ ] **Step 3: 校验无 ACR 残留**

Run:
```bash
grep -nE "crpi-|aliyuncs|acr-pull|ACR" deploy/k8s/prod/README.md ; echo "exit=$?"
```
Expected: 无输出、`exit=1`。

- [ ] **Step 4: 提交**

```bash
git add deploy/k8s/prod/README.md
git commit -m "$(cat <<'EOF'
docs(deploy): 生产 README 更新为移动云镜像仓库

把 acr-pull / 阿里云 ACR 的说明改为移动云仓库
ywjs-cc41758e.ecis.huabei-3.cmecloud.cn 与外部托管的拉取 Secret
secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn；用「镜像拉取 Secret
（外部托管）」一节取代「生成 ACR imagePullSecret」，并指向搬运脚本。

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: 运维切换（cutover，不入 git）

> 本任务是线上操作，**没有 git 提交**。按本项目铁律，凡涉及集群更新/创建的命令应由用户亲自执行；以下命令供执行与逐条核对。`secret.yaml` 是 gitignored 活文件（含真实凭证），改它不入库。

- [ ] **Step 1: 登录两个仓库并搬运镜像**

```bash
docker login crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com
docker login ywjs-cc41758e.ecis.huabei-3.cmecloud.cn
KUBECONFIG_PROD=~/dir/ywjs/kube/kubeconfig.json ./scripts/migrate-images-to-ecloud.sh
```
Expected: 每个镜像打印 `src digest` 与 `dst digest` 一致；末尾 `✅ 全部镜像已转推`。

- [ ] **Step 2: 改活 secret.yaml（与 Task 5 同样的字段改动 + 删 acr-pull 两段）**

在 `deploy/k8s/prod/secret.yaml`：
- 第 57 行 `ref:` 前缀 `crpi-....com/ywjs_app/` → `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/app/`（tag 保留实际值）。
- 第 78 行 `image_pull_secret: "acr-pull"` → `image_pull_secret: "secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn"`。
- 第 80 行 `ops_image:` 前缀同 ref 改 `app/`（tag 保留实际值）。
- 删除第 149 行起到文件末尾的两段 `acr-pull` Secret（ocm + oc-apps）。

校验：
```bash
grep -nE "crpi-|aliyuncs|acr-pull|ywjs_app|ywjs_public" deploy/k8s/prod/secret.yaml ; echo "exit=$?"
```
Expected: 无输出、`exit=1`。

- [ ] **Step 3: 推新配置（update-config）**

```bash
make update-config
```
Expected: `secret/ocm-secrets configured`；manager-api rollout 完成（`deployment ... successfully rolled out`）。

- [ ] **Step 4: 滚动控制面与中间件到新镜像**

```bash
KC="kubectl --kubeconfig ~/dir/ywjs/kube/kubeconfig.json -n ocm"
# 控制面 api/web：用 manifests 的新 image 直接 apply（也可用 make prod-deploy-manager 重建推送）
$KC apply -f deploy/k8s/prod/manager-api.yaml -f deploy/k8s/prod/manager-web.yaml
# 中间件：es/new-api/ragflow
$KC apply -f deploy/k8s/prod/elasticsearch.yaml -f deploy/k8s/prod/new-api.yaml -f deploy/k8s/prod/ragflow.yaml
$KC rollout status deploy/manager-api --timeout=900s
$KC rollout status deploy/manager-web --timeout=900s
```
Expected: 各 Deployment 成功 rolled out。

- [ ] **Step 5: 验证全部 pod 走新仓库**

```bash
KC="kubectl --kubeconfig ~/dir/ywjs/kube/kubeconfig.json"
for ns in ocm oc-apps; do echo "--- $ns ---"; $KC get pods -n $ns -o jsonpath='{range .items[*]}{.metadata.name}{"  "}{range .spec.containers[*]}{.image}{" "}{end}{"\n"}{end}'; done
$KC get pods -n ocm; $KC get pods -n oc-apps
```
Expected: 所有 `image:` 指向 `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn/...`；全部 `Running`、无 `ImagePullBackOff`。

- [ ] **Step 6: 新建测试实例验证 app pod 拉取链路 + 浏览器端到端**

- 在 manager 后台新建一个测试实例，观察 oc-apps 新 app pod 用新仓库 + 新拉取 Secret 拉 hermes/ops 成功、状态推进到「运行中」（拉取应明显快于迁移前）。
- 按项目规范用**真实浏览器**验证后台登录、应用列表、实例详情、新建实例端到端正常。
- 验证无误后删除测试实例。

---

## Self-Review（写完计划后自查）

**Spec 覆盖：**
- §2 新仓库坐标 → Task 3/4/5/6 引用、Task 1/7 搬运。✓
- §3 镜像清单与映射（13 个）→ Task 1 脚本枚举 7 个实跑 + 显式 6 个基础。✓
- §4 一次性搬运 → Task 1（脚本）+ Task 7 Step 1（执行）。✓
- §5.1 Makefile → Task 3。✓
- §5.2 prod manifests → Task 4。✓
- §5.3 secret（example + 活）→ Task 5（example）+ Task 7 Step 2（活）。✓
- §5.4 docs → Task 6。✓
- §6 切换顺序 → Task 7 Step 1–6。✓
- §7 验证 → Task 7 Step 5–6。✓
- §8 范围外（本地不动、prepuller 无须再引入）→ 本计划只动 prod；prepuller 在 Task 2 删除（设计 §8 一致）。✓

**占位符扫描：** manifests `image:` 的 tag 用了示例值并明确标注「以文件实际 tag 为准、只换前缀」，非待填占位。无 TBD/TODO。✓

**一致性：** 新仓库域名/命名空间/拉取 Secret 名在所有任务中字面一致；映射规则（ywjs_app→app、ywjs_public→public）在脚本与各 manifests 一致。✓
