# 生产镜像仓库迁移到移动云（退 ACR）设计

- 日期：2026-06-01
- 范围：仅生产（本地 k3d 不动）
- 状态：已通过 brainstorming 评审，待写实现计划

## 1. 背景与目标

生产构建/部署的镜像源当前是阿里云 ACR
`crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com`，与移动云集群跨公网，
节点拉取慢（无缓存时单层拉取超过 kubelet 的 `image-pull-progress-deadline` 被
`context canceled`，导致 `ImagePullBackOff` 长时间无法就绪）。

目标：把生产**构建期**与**部署期**镜像整体迁到与集群同区的移动云仓库，根治节点
拉取慢；彻底退掉 ACR 对生产的参与。本地 k3d 联调环境保持不变。

## 2. 新仓库坐标

- Registry 域名：`ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`
- 命名空间：
  - `app`：自有镜像（替代 ACR `ywjs_app`）
  - `public`：上游镜像与构建期基础镜像（替代 ACR `ywjs_public`）
- 拉取 Secret（**已在 `ocm` 与 `oc-apps` 两个命名空间存在，外部托管、不纳入本仓库
  secret.yaml**）：`secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`

## 3. 镜像清单与映射

retag 转推（不重新构建），tag 全部不变，bits 与线上完全一致。

### 3.1 自有镜像（→ `app/`，部署期，集群节点拉）

| 旧（ACR `ywjs_app/`） | 新（`app/`） | 当前线上 tag |
|---|---|---|
| `oc-manager-api` | `oc-manager-api` | `2026-06-01-19-17-02-97893d0a` |
| `oc-manager-web` | `oc-manager-web` | `2026-06-01-18-35-24-97893d0a` |
| `oc-manager-hermes` | `oc-manager-hermes` | `v2026.5.16-2026-05-31-20-46-58-40207c59-dirty` |
| `oc-manager-ops` | `oc-manager-ops` | `2026-05-31-21-09-32-b7ac999d` |

> 注：api/web 的线上 tag 会随发版变化，以执行迁移时 `kubectl get po -n ocm` 实际
> 在跑的为准；hermes/ops tag 同理以 `oc-apps` 实际为准。

### 3.2 上游镜像（→ `public/`，部署期）

| 旧（ACR `ywjs_public/`） | 新（`public/`） |
|---|---|
| `elasticsearch:8.11.3` | `elasticsearch:8.11.3` |
| `new-api:v1.0.0-rc.10` | `new-api:v1.0.0-rc.10` |
| `ragflow:v0.25.6` | `ragflow:v0.25.6` |

### 3.3 构建期基础镜像（→ `public/`，构建机拉）

Dockerfile 通过 `DOCKER_HUB_MIRROR`（默认 ACR `ywjs_public`）/ `ALPINE_MIRROR` 拉取：

| 镜像 | 当前来源 | 用途 |
|---|---|---|
| `golang:1.25-alpine3.22` | ACR `ywjs_public` | cmd/server builder |
| `alpine:3.22` | ACR `ywjs_public` | cmd/server runtime |
| `node:22-alpine` | ACR `ywjs_public` | web builder |
| `nginx:1.27-alpine` | ACR `ywjs_public` | web runtime |
| `python:3.13-slim-bookworm` | ACR `ywjs_public` | hermes base |
| `alpine:3.20` | `docker.io/library` | ops base（`ALPINE_MIRROR`） |

合计 4（自有）+ 3（上游）+ 6（基础）= **13 个镜像**。

## 4. 一次性镜像搬运（运维步骤，构建机执行）

对每个镜像 `docker pull <旧源> → docker tag <旧> <新> → docker push <新>`。
前置：构建机已有 ACR 拉取权限、可达 docker.io（经代理）、并 `docker login` 新仓库。

- 自有/上游/多数基础：从 ACR 拉（确保与线上同 bits）。
- `alpine:3.20`：从 `docker.io/library` 拉。

实现计划阶段产出一个可重复执行的搬运脚本（幂等：已存在的 tag 可跳过或覆盖），
列清 13 条 pull/tag/push，并在结尾打印校验（对每个新镜像 `docker manifest inspect`
或 `crane digest` 比对 digest 与源一致）。

## 5. 代码/配置改动（git，聚焦生产）

### 5.1 Makefile

- 新增变量：
  - `PROD_REGISTRY ?= ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`
  - `PROD_APP_NS ?= app`
  - `PROD_PUBLIC_NS ?= public`
- `API_IMAGE_REPO` / `WEB_IMAGE_REPO` / `HERMES_IMAGE_REPO` / `OPS_IMAGE_REPO`
  改为 `$(PROD_REGISTRY)/$(PROD_APP_NS)/oc-manager-*`。
- 生产 `build-*` 目标显式传 `--build-arg DOCKER_HUB_MIRROR=$(PROD_REGISTRY)/$(PROD_PUBLIC_NS)`，
  ops 目标额外传 `--build-arg ALPINE_MIRROR=$(PROD_REGISTRY)/$(PROD_PUBLIC_NS)`。
  **不修改 Dockerfile 的 `DOCKER_HUB_MIRROR`/`ALPINE_MIRROR` 默认值**（本地 k3d 构建
  仍走 ACR，互不影响）。
- `update-config` 里改 hermes ref 的 `sed`（当前匹配 `oc-manager-hermes:`）保持有效，
  确认对新仓库路径仍正确替换。

### 5.2 生产 manifests（`deploy/k8s/prod/`）

`manager-api.yaml`、`manager-web.yaml`、`elasticsearch.yaml`、`new-api.yaml`、`ragflow.yaml`：

- `image:` → 新仓库引用（按 §3 映射）。
- `imagePullSecrets[].name`：`acr-pull` → `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`。

### 5.3 secret（`secret.yaml` 实活 + `secret.example.yaml`）

- 内嵌 `manager.yaml`：
  - `k8s.image_pull_secret`：`acr-pull` → `secret-registry-ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`
  - `hermes.runtime_images[].ref` → 新 `app/oc-manager-hermes:<tag>`
  - `k8s.ops_image` → 新 `app/oc-manager-ops:<tag>`
- **删除内嵌的 `acr-pull` dockerconfigjson 两段（`@ocm` 与 `@oc-apps`）**：新拉取
  Secret 外部托管、已存在，无须本仓库创建。`update-config` 的 `apply -f secret.yaml`
  随之退化为单文档（仅 `ocm-secrets`），原「多文档不能带 -n」的注意事项相应更新。
- `secret.example.yaml`：把 acr-pull 示例替换为「拉取 Secret 由集群侧预先创建，名称
  见 `image_pull_secret`」的说明，并把所有 ACR 占位 ref 改成新仓库占位。

### 5.4 docs

- `deploy/k8s/prod/README.md` 及相关说明：更新仓库地址、命名空间、拉取 Secret 名、
  以及 §4 搬运步骤；移除已不适用的 ACR 凭证生成说明。

## 6. 切换顺序

1. 搬运 §3 的 13 个镜像到新仓库（§4 脚本）。
2. 改 git（Makefile / prod manifests / secret.yaml + example / docs）并提交。
3. `make update-config`：推新 `ocm-secrets`，使 `ops_image` / `runtime_images` /
   `image_pull_secret` 生效，重启 `manager-api`。
4. 滚动控制面与中间件：`kubectl set image`（api/web）或 `kubectl apply` manifests
   （elasticsearch/new-api/ragflow），等 rollout 完成。
5. 新建一个测试实例，验证 app pod 用新仓库 + 新拉取 Secret 拉 hermes/ops 成功。

## 7. 验证

- `kubectl get po -n ocm` 与 `-n oc-apps`：所有 `image:` 指向
  `ywjs-cc41758e.ecis.huabei-3.cmecloud.cn`，全部 `Running`、无 `ImagePullBackOff`；
  节点拉取耗时应明显下降。
- 按项目规范用**真实浏览器**验证 manager 后台登录、应用列表、新建实例端到端正常。

## 8. 不在本次范围 / 后续

- 本地 k3d（`deploy/k8s/local/*`、本地 registry、Dockerfile 默认 mirror、Makefile
  clean 用的 ACR alpine）全部保持不动。
- 迁到同区仓库后节点拉取快，无须再引入镜像预热（prepuller）等针对 ACR 慢的临时缓解。
- ACR 侧旧镜像不在本次删除范围；确认新仓库稳定运行后可另行清理。
