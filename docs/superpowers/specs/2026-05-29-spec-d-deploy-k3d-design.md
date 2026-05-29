# spec-D：全栈部署 + 本地 k3d 设计

> 本文是 [`2026-05-29-k8s-migration-design.md`](./2026-05-29-k8s-migration-design.md) §7/§8 的落地细化，专门定义 **Workstream D（全栈部署 + 本地 k3d）**。上层设计里 §7/§8 的设计级决定在此被当作契约。

**目标**：交付一套**裸 YAML 的 k8s manifest**与一个**全自包含的 k3d 本地全栈**（一键 `make local-up`），并为生产**产出标准 k8s YAML + 填值说明**；不做生产部署自动化、不做 cutover。

**架构方针**：`local-first，prod-generate-only`。本地 k3d 跑完整全栈（含 MySQL/Redis/ES/MinIO 有状态件）；生产用标准 k8s，有状态后端外置（托管/已有），manifest 只留连接占位。本地与生产**各一套完整 manifest**，互不共享 base，接受重复换取零耦合、可独立读懂。

**技术栈**：k3d（k3s-in-Docker）、k3d 内置 traefik + local-path + local registry、裸 YAML manifest、`kubectl`、Makefile、go-sql-driver/MySQL（spec-C 产物）。

---

## 1. 前置依赖与定位

设计文档 §9 将 spec-D 定位为**收口工作流**，依赖 A/B/C/E 产物。当前进度：

| spec | 内容 | 状态 |
|---|---|---|
| C | PostgreSQL → MySQL 8 | ✅ 已完成 |
| A+B | 编排 k8s 化 + app 数据模型（manager 去 docker.sock、client-go、app pod、S3） | ❌ 未实现 |
| E | oc-* 收敛为 oc-ops HTTP 服务 | ❌ 未实现 |

**本 spec 的处理策略**（经用户确认「完整设计 D，锁定部署目标」）：把 §5/§7/§8 的设计级决定当作契约，**现在就把完整的部署形态设计出来**，包括依赖 A/B/E 的 app-pod / oc-ops / RBAC / S3 形态——这些以「契约 manifest + 字段说明」交付，供 A/B/E 实现对齐；其真实运行验证留到对应 spec。这样可尽早锁定部署目标，反向约束 A/B/E 的实现。

**代价**：A/B/E 真正落地后，若契约有出入，本 spec 的相关 manifest 需回头修订。已接受。

---

## 2. 关键决策记录

逐项澄清后锁定如下（区别于上层设计的细化/修订都标注）：

| # | 决策 | 说明 |
|---|---|---|
| 1 | 生产用标准 k8s（vendor-neutral），有状态后端**外置** | MySQL/Redis/ES/MinIO(S3) 生产不部署，只填外部连接；registry 沿用阿里云 ACR（`crpi-…cn-beijing.personal.cr.aliyuncs.com`，`ywjs_app` 自建 / `ywjs_public` 上游）|
| 2 | 本地 k3d **内部署**全部有状态件 | MySQL/Redis/ES/MinIO 用 StatefulSet + PVC（local-path）|
| 3 | 本地 PVC 数据**跨重建持久** | `k3d cluster create --volume <repo>/.k3d-data:/var/lib/rancher/k3s/storage@all`，落宿主磁盘，`cluster delete` 也不丢；`.k3d-data` 进 `.gitignore` |
| 4 | 业务服务本地 + 生产都进 k8s | manager-api/web、new-api、ragflow 均 Deployment；指向本地/外部 DB |
| 5 | **裸 YAML，不用 kustomize** | 差异全部外化进各自 Secret/ConfigMap/StorageClass/Ingress |
| 6 | **无 base 目录**，local/ 与 prod/ **各一套完整 manifest** | 修订上层 §7.3「一套 base + 差异文件」；改为两套独立完整集合 |
| 7 | 本地 manager-api **默认跑集群内** | client-go 用 `InClusterConfig`；bootstrap 回调走集群内 Service DNS |
| 8 | 任务**不建 Job**，一律 `kubectl exec` 进容器执行 | migrate/seed/建 bucket 均 exec；与现有 `migrate-up`/`seed-e2e` 习惯一致（`docker compose exec`→`kubectl exec`）|
| 9 | Makefile **只做本地 k3d 操作**；生产**只生成 YAML** | 不做生产 SSH/apply 自动化、不做 cutover |
| 10 | 删根 `docker-compose.yml` + `dev-up`/`dev-down`，删 `.local` | 本地开发统一走 k3d local-up；连带影响见 §9 |

---

## 3. 目录与 manifest 组织

```
deploy/k8s/
  local/                          # 完整自包含本地全栈，apply 即起
    00-namespace.yaml             # 控制面 ns + oc-apps（app pod 所在）ns
    secret.example.yaml           # 本地固定凭证（真值 gitignore：master_key、MySQL/Redis/MinIO 凭证、per-app token 等）
    configmap.yaml                # 指向集群内 Service DNS 的 MySQL DSN / Redis / ES / S3 endpoint
    mysql.statefulset.yaml        # StatefulSet + PVC + Service；init ConfigMap 建 ocm/new-api/ragflow 三库
    redis.statefulset.yaml
    elasticsearch.statefulset.yaml
    minio.statefulset.yaml        # + Service（API 9000 / console 9001）
    manager-api.yaml              # Deployment + Service（镜像固定 :dev tag、imagePullPolicy: Always）
    manager-api.rbac.yaml         # ServiceAccount + Role + RoleBinding（oc-apps ns 权限）
    manager-web.yaml
    new-api.yaml
    ragflow.yaml
    ingress.yaml                  # traefik Ingress，*.localhost host 路由
  prod/                           # 完整自包含生产集合，只生成、不自动部署
    00-namespace.yaml
    secret.example.yaml           # 外部 MySQL DSN/Redis/ES/OSS + master_key + ACR imagePullSecret 占位
    configmap.yaml
    manager-api.yaml              # 镜像为 ACR 完整 tag 占位
    manager-api.rbac.yaml
    manager-web.yaml
    new-api.yaml
    ragflow.yaml
    ingress.yaml                  # nginx/域名/TLS 占位
    storageclass.example.yaml     # 云盘 CSI StorageClass 占位（若业务负载需 PVC）
    README.md                     # 必填清单 + apply 顺序
  contracts/                      # 依赖 A/B/E 的契约样例（文档化，不直接 apply）
    app-pod.deployment.yaml       # app Deployment(1)+Recreate + oc-ops 第二容器 + emptyDir + bootstrap Secret 样例
    README.md                     # 字段契约说明，供 A/B/E 对齐
```

**裸 YAML 无 overlay 打补丁能力**，故差异完全外化：`manager-api.yaml` 等工作负载靠 `envFrom` 引 `Secret`/`ConfigMap`、PVC 只认 StorageClass 名，本体在 local/ 与 prod/ 里**几乎一致**（差异仅镜像 tag 与 host）；环境差异活在各自的 Secret/ConfigMap/StorageClass/Ingress。本地比生产**多** DB StatefulSet，那几份只在 `local/`。

---

## 4. 组件清单与本地/生产差异

| 组件 | 形态 | 本地 k3d | 生产（生成 YAML）|
|---|---|---|---|
| manager-api | Deployment + SA/RBAC + Service | 集群内，连集群内 MySQL；`InClusterConfig` | 连外部 MySQL（Secret 填）|
| manager-web | Deployment + Service | ✅ | ✅ |
| new-api | Deployment + Service | ✅，连集群内 MySQL/Redis | ✅，连外部 |
| ragflow | Deployment + Service | ✅，连集群内 MySQL/Redis/ES/MinIO | ✅，连外部 |
| MySQL 8 | StatefulSet + PVC | ✅（init 建三库）| **不部署**，外置只填 DSN |
| Redis | StatefulSet/Deployment + PVC | ✅ | **不部署**，外置只填连接 |
| Elasticsearch | StatefulSet + PVC | ✅（ragflow 独占）| **不部署**，外置只填连接 |
| MinIO (S3) | StatefulSet + PVC | ✅（建 app + ragflow bucket）| **不部署**，换云 OSS 只填 endpoint/key |
| Ingress | traefik(本地)/nginx(prod) | `*.localhost` host 路由 | 域名/TLS（占位）|
| app pod | Deployment(1)+Recreate @ oc-apps | manager 运行时渲染（见 §7）| 同左 |

> 「零 PVC」仅针对 Hermes app pod；基础设施有状态件正常用 PVC。

---

## 5. 本地 k3d 拓扑与 `make local-up` 流程

**集群基线**：
- 单 server 节点；映射 `80:80@loadbalancer` 给 traefik。
- `--volume <repo>/.k3d-data:/var/lib/rancher/k3s/storage@all`：local-path PVC 数据落宿主，跨 `local-down`（cluster delete）持久。
- 挂 k3d 内置 **local registry**（如 `k3d-ocm-registry.localhost:5000`）：本地构建的 manager-api/web、hermes 镜像推此处，免每次 `k3d image import` 慢路径。
- new-api/ragflow/MySQL/Redis/ES/MinIO 镜像从阿里云 ACR `ywjs_public` 拉（已有镜像源）。

**`make local-up` 编排顺序**（无 Job，关键任务用 `kubectl exec`）：
1. `k3d cluster create`（带 registry + host-volume + 端口映射）。
2. `kubectl apply` `local/` 的 namespace + Secret + ConfigMap + 四个 DB StatefulSet（MySQL 挂 init ConfigMap 建 `ocm`/`new-api`/`ragflow` 三库）→ `kubectl rollout status` 等 Ready。
3. `kubectl apply` `local/` 业务/控制面 + RBAC + Ingress。manager-api **启动时自动跑 ocm 基线迁移**（spec-C 已实现的启动迁移）；new-api/ragflow 自建表 → 等 Ready。
4. `kubectl exec` 进 minio pod → `mc` 建 app 存储 bucket + ragflow bucket。
5. `kubectl exec` 进 manager-api pod → 跑 seed-admin（平台管理员），可选 seed-e2e。

**访问**：traefik Ingress + `*.localhost` host 路由（`ocm.localhost` = manager-web、`api.localhost`/同域 path、`newapi.localhost`、`ragflow.localhost`），替代现 compose 的 `5173/3000/8088` 固定端口。`*.localhost` 在主流系统解析到 `127.0.0.1`，配合 `80:80@loadbalancer` 直达。

**manager 运行位置**：默认集群内（`InClusterConfig`），bootstrap 回调走集群内 Service DNS。

---

## 6. Makefile k3d 生命周期目标（仅本地）

| target | 作用 |
|---|---|
| `local-up` | 全流程一键起：create → apply → wait → exec(migrate 自动/bucket/seed) |
| `local-down` | `k3d cluster delete`（host-volume 数据保留）|
| `local-reset` | `local-down` + 清 `.k3d-data` 干净重建 |
| `local-build` | 构建 manager-api/web(+hermes) 推 k3d registry + `kubectl rollout restart`（固定 `:dev` tag + `imagePullPolicy: Always`，免 sed）|
| `local-migrate` | `kubectl exec` manager-api → `migrate up`/`down` |
| `local-seed` / `local-seed-e2e` | `kubectl exec` manager-api → seed-admin / seed-e2e（`OCM_E2E=1` 守门）|
| `local-mc-init` | `kubectl exec` minio → `mc` 建 bucket（并入 `local-up`，亦可单跑）|
| `local-status` / `local-logs svc=` / `local-shell svc=` | 集群/pod/ingress 状态、日志、进容器 |
| `local-kubeconfig` | 导出/合并 kubeconfig 到 `~/.kube/config` |

---

## 7. A/B/E 契约 manifest（`deploy/k8s/contracts/`）

依赖 A/B/E 兑现，本 spec 按 §5/§7 出**形态 + 字段契约**，不直接 apply：

- **app-pod**：`Deployment(replicas=1, strategy=Recreate)` + Service；第二容器 `oc-ops`（基于 hermes 镜像构建、同版本标签）；共享 `emptyDir /opt/data`；Secret 注入 bootstrap/控制 token。**由 manager 运行时渲染并 apply 到 `oc-apps` ns，不静态部署**。
- **manager RBAC**：ServiceAccount 对 `oc-apps` ns：`deployments`/`services`/`secrets`/`configmaps` 的 CRUD、`pods` get·list·watch、`pods/log` get；**不含 `pods/exec`**（hermes 命令走 oc-ops HTTP）。
- **per-app 控制 token**：manager↔pod 双向复用的 Secret（pod→manager bootstrap 拉配置、manager→oc-ops 调命令），范围仅 oc-* verb。
- **S3 bucket 布局**：app 工作区 prefix、删除时 `archive/` 归档前缀、ragflow 独立 bucket；`manifest(含 api_key)` **不进 S3**，由 manager bootstrap 端点内存渲染经认证通道交付 pod。

---

## 8. 配置与密钥变更（§8）

- **删**：`runtime.enrollment_secret`、agent docker/file endpoint、docker socket 挂载、节点容量配置。
- **增**：
  - `k8s.*`：namespace（`oc-apps`）、in-cluster/kubeconfig 双模、imagePullSecrets、app 资源 requests/limits 默认。
  - `storage.s3.*`：endpoint、bucket、region、credentials、SSE。
  - MySQL DSN（替换 `database.url`；本地指集群内 `mysql` Service，生产指外部）。
- **k8s Secret 清单**：master_key、MySQL 凭证、S3/MinIO 凭证、ACR imagePullSecret、per-app 控制 token、new-api/ragflow 各自 secret。本地真值走 `secret.example.yaml` + gitignore；生产留占位由用户填。

---

## 9. 清理与连带影响

删根 `docker-compose.yml` + `dev-up`/`dev-down`、删 `.local` 会波及以下，需在实现计划中一并处理：

| 受影响项 | 现状 | 迁移方向 |
|---|---|---|
| `.local/` 数据 | compose dev 栈 bind-mount（已 gitignore）| 实现阶段 `rm -rf .local` |
| `make build` / `web-build` / `build-hermes-runtime` | 在 compose 的 manager-api/web 容器内编译到 `tmp/build/` | 改为本地工具链编译，或 `local-build` 在 k3d/构建容器内完成；生产镜像 `build-*-image` 走 `docker build` 不受影响 |
| Playwright e2e | globalSetup 走 `make seed-e2e`（compose exec）| 改指 k3d：`kubectl exec` manager-api 跑 seed-e2e，baseURL 指向 `*.localhost` Ingress |
| `docs/local-development.md` | docker-compose 联调说明 | 重写为 k3d local-up 流程 |
| `deploy/manage/psql.sh`/`redis-cli.sh` 等 | compose 辅助脚本 | 评估保留/改 `kubectl exec`（按需，非阻塞）|
| CLAUDE.md/AGENTS.md「本地调试账号」端口 | `5173/3000/8088` | 同步为 `*.localhost` host 路由 |

> `deploy/*` 生产 compose + `deploy-*` SSH 部署目标**本 spec 不动**（生产 cutover 依赖 A/B/E，留后续）。

---

## 10. 测试与验收

因 A/B/E 未落地，验收**分两层**：

**本 spec 可验（必须通过）**：
- `make local-up` 冒烟：全部 pod `Ready`、`local-status` 无 CrashLoop。
- 四控制台经 `*.localhost` 可访问：manager-web、manager-api（健康检查 / 登录 200 + JWT）、new-api 后台、ragflow 控制台。
- 平台管理员登录、现有功能在 k3d/MySQL 上跑通（建组织、new-api provision、助手版本、知识库等不依赖 app-pod 编排的路径）。
- `local-down` 后数据保留、`local-up` 复用；`local-reset` 干净重建。
- 沿用项目规范：真实浏览器逐角色（platform_admin/org_admin/org_member）验证。

**依赖 A/B/E（标注「契约就绪、待对应 spec 验证」）**：
- app pod 真实编排（Deployment+Recreate）、oc-ops HTTP 调用、S3 工作区同步、bootstrap 回调闭环。

---

## 11. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| 契约先行于实现 | A/B/E 未落地，contracts/ 与配置键可能需回头改 | 已接受；contracts/ 集中、便于后续对齐修订 |
| 删 compose 波及构建/e2e | `build`/`web-build`/e2e seed 依赖 compose 容器 | §9 列明迁移路径，实现计划逐项处理后再删 |
| 裸 YAML 多环境差异手工维护 | 无 overlay，local/ 与 prod/ 有重复 | 用户明确选择两套独立；以 README + 字段契约约束一致性 |
| `*.localhost` 解析差异 | 个别系统不把 `*.localhost` 解析到 127.0.0.1 | 文档提供 `/etc/hosts` 兜底；或 `local-status` 打印直达 URL |
| local-path 单节点绑定 | PVC 绑到单 server 节点，多节点扩展受限 | 本地单 server 足够；生产有状态件外置不受影响 |
| ES 内存占用 | ragflow 的 ES 在本地吃内存 | 文档标注最低内存；可按需关闭 ragflow 子栈 |

---

## 12. 范围外（不在本 spec）

- A/B/E 的实际代码实现（client-go 编排、app 数据模型、S3 存储、oc-ops HTTP 服务）。
- 生产集群创建、生产部署自动化、从 docker-compose 的生产 cutover。
- 生产托管 DB/OSS 的实际接入与数据迁移（manager 与 new-api 均全新库，无 ETL）。
- Prometheus 等增强可观测性（metrics-server 足够本期）。
