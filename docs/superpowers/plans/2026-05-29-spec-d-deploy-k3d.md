# spec-D 全栈部署 + 本地 k3d 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用裸 YAML manifest + 一键 `make local-up` 把现有全栈（manager-api/web、new-api、ragflow + MySQL/Redis/ES/MinIO）跑在本地 k3d 集群上，取代根 docker-compose dev 栈；并产出一套「只生成、不自动部署」的生产 k8s YAML。

**Architecture:** `deploy/k8s/local/` 与 `deploy/k8s/prod/` 各一套完整自包含裸 YAML（无 base、无 kustomize、无 Helm），差异全部外化进各自的 Secret/ConfigMap/Ingress；本地有状态件用 StatefulSet + local-path PVC（挂宿主 `.k3d-data` 跨重建持久），生产有状态件外置只填连接占位。`deploy/k8s/contracts/` 放依赖 A/B/E 的 app-pod/oc-ops/RBAC 契约样例（文档化、不 apply）。Makefile 只做本地 k3d 生命周期；migrate/seed/建 bucket 一律 `kubectl exec`，不建 Job。

**Tech Stack:** k3d（k3s-in-Docker）、k3d 内置 traefik + local-path + local registry、`kubectl`、裸 YAML、Makefile、现有 `cmd/server` 镜像（spec-C 已让其启动期自动跑 ocm 基线迁移）。

---

## 重要前置约束（实现者必读）

1. **本 spec 部署的是「当前」server 二进制，A/B/E 尚未实现**。当前 `cmd/server`：
   - 配置是**单个 YAML 文件**（`internal/config/loader.go` 用 `KnownFields(true)`，**无环境变量覆盖**）；必须把整份 `manager.yaml` 作为文件挂进 pod（`OCM_CONFIG=/etc/manager/config.yaml`，见 `cmd/server/Dockerfile:84`）。
   - `Validate()`（`internal/config/loader.go:65`）**仍要求** `runtime.enrollment_secret`、`hermes.system_prompt_template`、`hermes.runtime_images` 等字段——本 spec 的 `manager.yaml` **必须保留这些字段**，否则 pod 启动即 fail。A/B 删除这些字段是后续工作，本 spec 不动。
   - 启动期自动跑 ocm 基线迁移（spec-C 已实现）。所以 local-up **不需要**单独的 ocm 迁移步骤，只需 MySQL 里先有空的 `ocm` 库。
   - manager-api **不挂 docker.sock**（那是 compose dev + 旧 agent 链路）。无 runtime_node 注册时 reconciler tick 是 no-op，服务正常启动。**「创建真实应用」等依赖 agent/编排的路径在本 spec 下不可用**，这与 spec §10 的验收分层一致。

2. **prod 镜像当前只构建 3 个二进制**（`cmd/server/Dockerfile:59-61`）：`oc-manager`、`migrate`、`seed-admin`。**`seed-e2e` 不在镜像里**——Task 14 会把它加进 Dockerfile，e2e 才能改走 `kubectl exec`。

3. **统一命名约定**（全计划复用，勿改名）：

   | 项 | 值 |
   |---|---|
   | k3d 集群名 | `ocm` |
   | k3d registry | 名 `ocm-registry`，地址 `k3d-ocm-registry.localhost:5000` |
   | host-volume 数据目录 | `<repo>/.k3d-data` |
   | 控制面/基础设施 namespace | `ocm` |
   | app pod namespace（契约用） | `oc-apps` |
   | Service 名（ns=ocm） | `mysql` `redis` `elasticsearch` `minio` `manager-api` `manager-web` `new-api` `ragflow` |
   | 本地镜像 tag | `k3d-ocm-registry.localhost:5000/oc-manager-api:dev`、`.../oc-manager-web:dev` |
   | Ingress host | `ocm.localhost`（`/`→web，`/api`+`/healthz`→api）、`newapi.localhost`、`ragflow.localhost` |

4. **统一本地凭证**（写死在 `deploy/k8s/local/secret.example.yaml`，仅本地 dev）：

   | 用途 | 值 |
   |---|---|
   | MySQL root 密码 | `ocm` |
   | MySQL manager 用户 | `ocm` / `ocm`（库 `ocm`）|
   | 三个库 | `ocm`、`new-api`、`rag_flow` |
   | Redis 密码 | `ocm` |
   | ES 密码（`elastic`）| `infini_rag_flow`（沿用 ragflow 默认，减少 ragflow 侧改动）|
   | MinIO root | `ocm` / `ocmsecret123` |
   | MinIO bucket | `oc-apps`（app 存储占位）、`ragflow`（ragflow 用）|
   | master_key | 复用 `config/manager.yaml` 现值 `OWPXfwSR4K07Tl4oyy5EY7UmQYY+fEP91N3n3gxdGX4=` |

5. **DNS 解析**：`*.localhost` 在多数 Linux/macOS 解析到 `127.0.0.1`；k3d registry 主机名 `k3d-ocm-registry.localhost` 需在宿主 `/etc/hosts` 指向 `127.0.0.1`（Task 12 在 `local-up` 前用一个 guard 校验并提示）。

6. 本计划多为「写 YAML → apply → 用 kubectl/curl 验证」，没有 Go 单测；每个 Task 的验证步骤给出确切命令与期望输出。提交遵循项目 Conventional Commits（中文摘要 + 正文），结尾 `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。**只 `git add` 本计划涉及的具体文件**。

---

## 文件结构

```
deploy/k8s/
  local/
    00-namespace.yaml              # ns: ocm + oc-apps
    secret.example.yaml            # 全部本地凭证 + 完整 manager.yaml（key: manager.yaml）
    mysql.yaml                     # StatefulSet+Service+PVC + init ConfigMap(建 3 库与用户)
    redis.yaml                     # StatefulSet+Service+PVC
    elasticsearch.yaml             # StatefulSet+Service+PVC
    minio.yaml                     # StatefulSet+Service+PVC
    manager-api.yaml               # Deployment+Service（挂 secret 的 manager.yaml）
    manager-rbac.yaml              # SA + Role(oc-apps) + RoleBinding（契约 RBAC，本 spec 先建好）
    manager-web.yaml               # Deployment+Service
    new-api.yaml                   # Deployment+Service（SQL_DSN→共享 MySQL new-api 库）
    ragflow.yaml                   # Deployment+Service + service_conf 模板 ConfigMap
    ingress.yaml                   # traefik Ingress：*.localhost 路由
  prod/
    00-namespace.yaml
    secret.example.yaml            # 外部 MySQL/Redis/ES/OSS 连接 + master_key + ACR imagePullSecret 占位
    manager-api.yaml manager-rbac.yaml manager-web.yaml new-api.yaml ragflow.yaml
    ingress.yaml                   # 域名/TLS 占位
    storageclass.example.yaml      # 云盘 CSI StorageClass 占位
    README.md                      # 必填清单 + apply 顺序
  contracts/
    app-pod.deployment.yaml        # Deployment(1)+Recreate + oc-ops 第二容器 + emptyDir + token Secret 样例
    README.md                      # 字段契约说明，供 A/B/E 对齐
  README.md                        # deploy/k8s 总览：local/prod/contracts 各自用途
```

被修改的既有文件：`.gitignore`（加 `.k3d-data/`）、`Makefile`（删 compose dev 段、加 k3d 段、改 build/test/vet/sqlc/migrate/seed-e2e）、`docker-compose.yml`（删除）、`web/playwright.config.ts` + `web/tests/e2e/global-setup.ts`（改走 kubectl exec / `*.localhost`）、`cmd/server/Dockerfile`（加 seed-e2e 二进制）、`docs/local-development.md`（重写）、`CLAUDE.md` + `AGENTS.md`（端口说明同步）。

---

## Task 1: 目录骨架 + .gitignore + 总览 README

**Files:**
- Create: `deploy/k8s/README.md`
- Modify: `.gitignore`（在 `.local/` 行后追加 `.k3d-data/`）

- [ ] **Step 1: 追加 .gitignore 条目**

在 `.gitignore` 中 `.local/` 所在行（第 11 行附近）的**下一行**插入：

```gitignore
.k3d-data/
```

- [ ] **Step 2: 写 deploy/k8s 总览 README**

`deploy/k8s/README.md`：

```markdown
# k8s 部署清单（spec-D）

本目录是 spec-D 的交付物。设计见
`docs/superpowers/specs/2026-05-29-spec-d-deploy-k3d-design.md`。

## 三套独立内容

- `local/`：本地 k3d 全栈，**完整自包含**（含 MySQL/Redis/ES/MinIO 有状态件）。
  通过仓库根 `make local-up` 一键拉起，勿手工逐个 apply。
- `prod/`：生产标准 k8s YAML，**只生成不自动部署**。有状态后端外置，
  仅留连接占位；填值与 apply 顺序见 `prod/README.md`。
- `contracts/`：依赖 spec-A/B/E 的 app-pod / oc-ops / RBAC 契约样例，
  **文档化、不 apply**，供后续 spec 对齐字段。

## 设计要点

- 裸 YAML，无 base、无 kustomize、无 Helm；local 与 prod 各一套完整集合，
  接受重复换取零耦合、可独立读懂。
- 本地凭证为开发固定值，仅用于本地，禁止用于任何线上/共享环境。
```

- [ ] **Step 3: 验证 .gitignore 生效**

Run: `git check-ignore .k3d-data/x 2>/dev/null && echo IGNORED`
Expected: 输出 `IGNORED`

- [ ] **Step 4: Commit**

```bash
git add .gitignore deploy/k8s/README.md
git commit -m "$(cat <<'EOF'
chore(k8s): 新增 deploy/k8s 骨架与 .k3d-data 忽略

为 spec-D 建立 deploy/k8s 总览 README（说明 local/prod/contracts 三套
独立内容的用途与边界），并把 k3d local-path 宿主挂载目录 .k3d-data 加入
.gitignore，避免本地集群数据误入仓。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 本地 namespace

**Files:**
- Create: `deploy/k8s/local/00-namespace.yaml`

- [ ] **Step 1: 写 namespace manifest**

`deploy/k8s/local/00-namespace.yaml`：

```yaml
# 控制面与基础设施 namespace：manager-api/web、new-api、ragflow、
# MySQL/Redis/ES/MinIO 全部部署于此。
apiVersion: v1
kind: Namespace
metadata:
  name: ocm
  labels:
    app.kubernetes.io/part-of: oc-manager
---
# app pod（Hermes 实例）所在 namespace。本 spec 不在此部署任何对象，
# 仅预建给 manager RBAC 授权（manager-rbac.yaml）与 spec-A/B 运行时渲染使用。
apiVersion: v1
kind: Namespace
metadata:
  name: oc-apps
  labels:
    app.kubernetes.io/part-of: oc-manager
```

- [ ] **Step 2: 提交（本任务无法独立 apply 验证，集群在 Task 11 才创建；此处仅静态校验语法）**

Run: `kubectl apply --dry-run=client -f deploy/k8s/local/00-namespace.yaml 2>&1 || python3 -c "import yaml,sys; list(yaml.safe_load_all(open('deploy/k8s/local/00-namespace.yaml'))); print('YAML OK')"`
Expected: 输出含 `namespace/ocm created (dry run)` 或回退分支的 `YAML OK`（取决于本机是否已装 kubectl）。

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/00-namespace.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 namespace（ocm + oc-apps）

ocm 承载控制面与基础设施；oc-apps 预建给 manager RBAC 与 spec-A/B
运行时渲染 app pod 使用，本 spec 不在其中部署对象。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 本地 Secret（凭证 + 完整 manager.yaml）

**Files:**
- Create: `deploy/k8s/local/secret.example.yaml`
- Modify: `.gitignore`（忽略真值 `deploy/k8s/local/secret.yaml`）

设计依据：config 是单文件、无 env 覆盖（`internal/config/loader.go:18-36`）。把整份 `manager.yaml` 放进 Secret 的 `manager.yaml` 键，manager-api 容器挂载到 `/etc/manager/config.yaml`。其余服务（MySQL/Redis/MinIO/ES/new-api/ragflow）的散装凭证也放同一个 Secret，按需用 `envFrom`/`valueFrom` 引用。

- [ ] **Step 1: 写 .gitignore 忽略真值文件**

在 `.gitignore` 末尾追加：

```gitignore
# 本地 k8s 真值 Secret（从 secret.example.yaml 复制后填好的实际文件）
deploy/k8s/local/secret.yaml
```

- [ ] **Step 2: 写 secret.example.yaml**

`deploy/k8s/local/secret.example.yaml`（本地固定开发凭证，`stringData` 明文便于阅读；`local-up` 直接 apply 本 example，真值文件留作覆盖用）：

```yaml
# 本地开发固定凭证 —— 仅用于本地 k3d，禁止用于任何线上/共享环境。
# local-up 默认直接 apply 本文件。若需自定义，复制为 secret.yaml 后修改，
# 并把 local-up 的 SECRET_FILE 指向 secret.yaml。
apiVersion: v1
kind: Secret
metadata:
  name: ocm-secrets
  namespace: ocm
type: Opaque
stringData:
  # —— 散装凭证：供 MySQL/Redis/MinIO/ES/new-api/ragflow 的 env 引用 ——
  mysql-root-password: "ocm"
  mysql-app-user: "ocm"
  mysql-app-password: "ocm"
  redis-password: "ocm"
  minio-root-user: "ocm"
  minio-root-password: "ocmsecret123"
  elastic-password: "infini_rag_flow"
  # —— manager-api 完整配置（挂载到 /etc/manager/config.yaml）——
  # 注意：当前 server 二进制仍要求 runtime.enrollment_secret 与
  # hermes.system_prompt_template/runtime_images，删字段属 spec-A/B，本 spec 保留。
  manager.yaml: |
    app:
      env: dev
      http_addr: ":8080"
      public_base_url: "http://ocm.localhost"
      data_root: "/var/lib/oc-manager/data"
    database:
      url: "mysql://ocm:ocm@tcp(mysql:3306)/ocm?parseTime=true&loc=UTC&charset=utf8mb4&multiStatements=true"
    redis:
      addr: "redis:6379"
      password: "ocm"
      db: 0
      key_prefix: "ocm:"
    auth:
      cookie_domain: "ocm.localhost"
      access_token_ttl: "1h"
      refresh_token_ttl: "720h"
      jwt_access_secret: "local-dev-access-secret-change-me-32"
      jwt_refresh_secret: "local-dev-refresh-secret-change-me-32"
      csrf_secret: "local-dev-csrf-secret-change-me-32"
    security:
      master_key: "OWPXfwSR4K07Tl4oyy5EY7UmQYY+fEP91N3n3gxdGX4="
    newapi:
      base_url: "http://new-api:3000"
      admin_token: "zLZJ09Qxic+vzcA/urGmQvU69uKq3Jw="
      admin_user_id: 1
    ragflow:
      base_url: "http://ragflow:9380"
      api_key: "ragflow-PLACEHOLDER-REPLACE-AFTER-RAGFLOW-UP"
      request_timeout: "30s"
      chunk_method: "naive"
    hermes:
      runtime_images:
        - id: "v2026.5.16"
          label: "Hermes v2026.5.16（当前）"
          ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00"
      system_prompt_template: |
        你是 Hermes 智能助手。工作目录是 /opt/data/workspace/，所有文件写入该目录。
      workspace:
        archive_retention_days: 14
      llm:
        base_url: "http://new-api:3000/v1"
        default_provider: "openai"
        default_model: "qwen2.5:0.5b"
    agent:
      heartbeat_interval_seconds: 30
    runtime:
      enrollment_secret: "yGD1uFiFK2Oshk1TBp8RWQGS5JLP6HWe66WxNUF2lMA="
      probe:
        interval_seconds: 10
        timeout_seconds: 3
        failure_threshold: 1
        recovery_threshold: 1
```

> 注：`ragflow.api_key` 是占位。ragflow 控制台首次启动后才能生成真实 key；本地验证 manager↔ragflow 链路时再回填并 `kubectl rollout restart deploy/manager-api -n ocm`。manager 启动不强依赖该 key 合法（`RAGFlowConfig.validate` 只校验 base_url+api_key 非空与 URL 合法，不校验 key 真伪）。

- [ ] **Step 3: 校验 YAML 与 manager.yaml 内嵌块合法**

Run:
```bash
python3 -c "
import yaml
d=list(yaml.safe_load_all(open('deploy/k8s/local/secret.example.yaml')))[0]
inner=yaml.safe_load(d['stringData']['manager.yaml'])
assert inner['database']['url'].startswith('mysql://ocm:ocm@tcp(mysql:3306)')
assert inner['app']['public_base_url']=='http://ocm.localhost'
assert inner['runtime']['enrollment_secret']
print('secret.example.yaml OK')
"
```
Expected: `secret.example.yaml OK`

- [ ] **Step 4: Commit**

```bash
git add .gitignore deploy/k8s/local/secret.example.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 Secret（散装凭证 + 完整 manager.yaml）

config 加载是单 YAML 文件、无环境变量覆盖，故把整份 manager.yaml 放入
Secret 的 manager.yaml 键，manager-api 挂载到 /etc/manager/config.yaml；
DSN/Redis/new-api/ragflow 地址改指集群内 Service DNS，public_base_url 与
cookie_domain 改为 ocm.localhost。保留当前 server 仍要求的
runtime.enrollment_secret 与 hermes 字段。真值 secret.yaml 入 gitignore。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 本地 MySQL（StatefulSet + 三库 init）

**Files:**
- Create: `deploy/k8s/local/mysql.yaml`

- [ ] **Step 1: 写 mysql.yaml**

`deploy/k8s/local/mysql.yaml`（init ConfigMap 在 root 初始化时建三库与 manager 用户授权；镜像走 ACR ywjs_public）：

```yaml
# MySQL 8 初始化脚本：建 ocm/new-api/rag_flow 三库，并给 manager 的 ocm 用户授权。
# 通过挂载到 /docker-entrypoint-initdb.d/ 在首次初始化（数据目录为空）时执行一次。
apiVersion: v1
kind: ConfigMap
metadata:
  name: mysql-init
  namespace: ocm
data:
  init.sql: |
    CREATE DATABASE IF NOT EXISTS `ocm` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
    CREATE DATABASE IF NOT EXISTS `new-api` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
    CREATE DATABASE IF NOT EXISTS `rag_flow` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
    CREATE USER IF NOT EXISTS 'ocm'@'%' IDENTIFIED BY 'ocm';
    GRANT ALL PRIVILEGES ON `ocm`.* TO 'ocm'@'%';
    FLUSH PRIVILEGES;
---
apiVersion: v1
kind: Service
metadata:
  name: mysql
  namespace: ocm
spec:
  selector: { app: mysql }
  ports:
    - port: 3306
      targetPort: 3306
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: mysql
  namespace: ocm
spec:
  serviceName: mysql
  replicas: 1
  selector: { matchLabels: { app: mysql } }
  template:
    metadata:
      labels: { app: mysql }
    spec:
      containers:
        - name: mysql
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/mysql:8.0
          args:
            - "--character-set-server=utf8mb4"
            - "--collation-server=utf8mb4_0900_ai_ci"
            - "--default-authentication-plugin=mysql_native_password"
          env:
            - name: MYSQL_ROOT_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: mysql-root-password } }
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 3306
          volumeMounts:
            - { name: data, mountPath: /var/lib/mysql }
            - { name: init, mountPath: /docker-entrypoint-initdb.d }
          readinessProbe:
            exec:
              command: ["sh", "-c", "mysqladmin ping -h127.0.0.1 -uroot -p\"$MYSQL_ROOT_PASSWORD\" --silent"]
            initialDelaySeconds: 10
            periodSeconds: 5
            failureThreshold: 30
      volumes:
        - name: init
          configMap: { name: mysql-init }
  volumeClaimTemplates:
    - metadata: { name: data }
      spec:
        accessModes: ["ReadWriteOnce"]
        resources: { requests: { storage: 5Gi } }
```

- [ ] **Step 2: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/mysql.yaml'))); print('docs=%d' % len(docs)); assert any(d['kind']=='StatefulSet' for d in docs)"`
Expected: `docs=3`

> 集群级运行验证延后到 Task 16（`local-up`）：届时 `kubectl exec mysql-0 -- mysql -uroot -pocm -e 'show databases'` 应列出 `ocm`/`new-api`/`rag_flow`。

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/mysql.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 MySQL StatefulSet 与三库初始化

单副本 MySQL 8 + local-path PVC（5Gi）。init ConfigMap 在首次初始化建
ocm/new-api/rag_flow 三库并给 ocm 用户授权 ocm 库，供 manager/new-api/
ragflow 共享同一 MySQL（new-api、ragflow 用 root，manager 用 ocm 用户）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 本地 Redis

**Files:**
- Create: `deploy/k8s/local/redis.yaml`

- [ ] **Step 1: 写 redis.yaml**

`deploy/k8s/local/redis.yaml`（带密码、appendonly 持久化；manager db0、ragflow db1 共用同实例）：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: redis
  namespace: ocm
spec:
  selector: { app: redis }
  ports:
    - port: 6379
      targetPort: 6379
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
  namespace: ocm
spec:
  serviceName: redis
  replicas: 1
  selector: { matchLabels: { app: redis } }
  template:
    metadata:
      labels: { app: redis }
    spec:
      containers:
        - name: redis
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/redis:7
          command: ["redis-server", "--requirepass", "$(REDIS_PASSWORD)", "--appendonly", "yes"]
          env:
            - name: REDIS_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: redis-password } }
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 6379
          volumeMounts:
            - { name: data, mountPath: /data }
          readinessProbe:
            exec:
              command: ["sh", "-c", "redis-cli -a \"$REDIS_PASSWORD\" ping | grep -q PONG"]
            initialDelaySeconds: 5
            periodSeconds: 5
            failureThreshold: 20
  volumeClaimTemplates:
    - metadata: { name: data }
      spec:
        accessModes: ["ReadWriteOnce"]
        resources: { requests: { storage: 1Gi } }
```

- [ ] **Step 2: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/redis.yaml'))); assert {d['kind'] for d in docs}=={'Service','StatefulSet'}; print('redis.yaml OK')"`
Expected: `redis.yaml OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/redis.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 Redis StatefulSet

单副本 Redis 7 + 密码 + appendonly + local-path PVC（1Gi）。manager（db0）
与 ragflow（db1）共用同实例，密码从 ocm-secrets 注入。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: 本地 Elasticsearch

**Files:**
- Create: `deploy/k8s/local/elasticsearch.yaml`

- [ ] **Step 1: 写 elasticsearch.yaml**

`deploy/k8s/local/elasticsearch.yaml`（single-node，security 开启 + 密码，参数对齐 compose 的 ragflow-es）：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: elasticsearch
  namespace: ocm
spec:
  selector: { app: elasticsearch }
  ports:
    - port: 9200
      targetPort: 9200
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: elasticsearch
  namespace: ocm
spec:
  serviceName: elasticsearch
  replicas: 1
  selector: { matchLabels: { app: elasticsearch } }
  template:
    metadata:
      labels: { app: elasticsearch }
    spec:
      containers:
        - name: elasticsearch
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/elasticsearch:8.11.3
          env:
            - name: node.name
              value: elasticsearch
            - name: ELASTIC_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: elastic-password } }
            - name: discovery.type
              value: single-node
            - name: xpack.security.enabled
              value: "true"
            - name: xpack.security.http.ssl.enabled
              value: "false"
            - name: xpack.security.transport.ssl.enabled
              value: "false"
            - name: bootstrap.memory_lock
              value: "false"
            - name: cluster.routing.allocation.disk.watermark.low
              value: 5gb
            - name: cluster.routing.allocation.disk.watermark.high
              value: 3gb
            - name: cluster.routing.allocation.disk.watermark.flood_stage
              value: 2gb
            - name: ES_JAVA_OPTS
              value: "-Xms1g -Xmx1g"
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 9200
          volumeMounts:
            - { name: data, mountPath: /usr/share/elasticsearch/data }
          readinessProbe:
            httpGet: { path: /, port: 9200 }
            initialDelaySeconds: 20
            periodSeconds: 10
            failureThreshold: 30
  volumeClaimTemplates:
    - metadata: { name: data }
      spec:
        accessModes: ["ReadWriteOnce"]
        resources: { requests: { storage: 5Gi } }
```

- [ ] **Step 2: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/elasticsearch.yaml'))); assert {d['kind'] for d in docs}=={'Service','StatefulSet'}; print('es.yaml OK')"`
Expected: `es.yaml OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/elasticsearch.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 Elasticsearch StatefulSet（ragflow 检索后端）

single-node ES 8.11.3 + security/密码 + local-path PVC（5Gi），参数对齐
原 compose 的 ragflow-es（关闭 SSL、放宽磁盘水位、限堆 1g）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: 本地 MinIO（S3）

**Files:**
- Create: `deploy/k8s/local/minio.yaml`

- [ ] **Step 1: 写 minio.yaml**

`deploy/k8s/local/minio.yaml`（API 9000 / console 9001；bucket 由 `local-mc-init` 在 local-up 里建）：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: minio
  namespace: ocm
spec:
  selector: { app: minio }
  ports:
    - { name: api, port: 9000, targetPort: 9000 }
    - { name: console, port: 9001, targetPort: 9001 }
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: minio
  namespace: ocm
spec:
  serviceName: minio
  replicas: 1
  selector: { matchLabels: { app: minio } }
  template:
    metadata:
      labels: { app: minio }
    spec:
      containers:
        - name: minio
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/minio:RELEASE.2026-03-25T00-00-00Z
          args: ["server", "--console-address", ":9001", "/data"]
          env:
            - name: MINIO_ROOT_USER
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: minio-root-user } }
            - name: MINIO_ROOT_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: minio-root-password } }
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 9000
            - containerPort: 9001
          volumeMounts:
            - { name: data, mountPath: /data }
          readinessProbe:
            httpGet: { path: /minio/health/live, port: 9000 }
            initialDelaySeconds: 10
            periodSeconds: 5
            failureThreshold: 20
  volumeClaimTemplates:
    - metadata: { name: data }
      spec:
        accessModes: ["ReadWriteOnce"]
        resources: { requests: { storage: 5Gi } }
```

- [ ] **Step 2: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/minio.yaml'))); assert {d['kind'] for d in docs}=={'Service','StatefulSet'}; print('minio.yaml OK')"`
Expected: `minio.yaml OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/minio.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 MinIO StatefulSet（S3 对象存储）

单副本 MinIO + local-path PVC（5Gi），API 9000 / console 9001。bucket
（oc-apps、ragflow）由 local-up 的 local-mc-init 步骤 kubectl exec 建。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: manager-api Deployment + Service + RBAC

**Files:**
- Create: `deploy/k8s/local/manager-api.yaml`
- Create: `deploy/k8s/local/manager-rbac.yaml`

设计依据：镜像 CMD 默认 `oc-manager`（`cmd/server/Dockerfile:102`）；`OCM_CONFIG=/etc/manager/config.yaml`；`/healthz` 健康检查（`Dockerfile:72`）。把 Secret 的 `manager.yaml` 键挂为该文件。RBAC 是 spec §7 的契约（manager 对 oc-apps 的权限），本 spec 先建好 SA 并绑定，manager-api pod 用该 SA（即便当前代码尚未用 client-go，也无害）。

- [ ] **Step 1: 写 manager-rbac.yaml**

`deploy/k8s/local/manager-rbac.yaml`：

```yaml
# manager-api 的 ServiceAccount。当前 server 代码尚未用 client-go，
# 预先创建并绑定 oc-apps 权限是 spec §7 的契约，供 spec-A/B 落地后直接生效。
apiVersion: v1
kind: ServiceAccount
metadata:
  name: manager-api
  namespace: ocm
---
# 对 oc-apps namespace 授予 app pod 编排所需权限；明确不含 pods/exec
# （hermes 命令走 oc-ops HTTP，见设计 §7）。
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: manager-app-orchestrator
  namespace: oc-apps
rules:
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["services", "secrets", "configmaps"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: manager-app-orchestrator
  namespace: oc-apps
subjects:
  - kind: ServiceAccount
    name: manager-api
    namespace: ocm
roleRef:
  kind: Role
  name: manager-app-orchestrator
  apiGroup: rbac.authorization.k8s.io
```

- [ ] **Step 2: 写 manager-api.yaml**

`deploy/k8s/local/manager-api.yaml`（镜像走 k3d registry 的 `:dev` tag + `imagePullPolicy: Always`，配合 `local-build` 的 `rollout restart`）：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: manager-api
  namespace: ocm
spec:
  selector: { app: manager-api }
  ports:
    - port: 8080
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: manager-api
  namespace: ocm
spec:
  replicas: 1
  selector: { matchLabels: { app: manager-api } }
  template:
    metadata:
      labels: { app: manager-api }
    spec:
      serviceAccountName: manager-api
      containers:
        - name: manager-api
          image: k3d-ocm-registry.localhost:5000/oc-manager-api:dev
          imagePullPolicy: Always
          env:
            - name: OCM_CONFIG
              value: /etc/manager/config.yaml
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/manager
              readOnly: true
          readinessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 5
            periodSeconds: 5
            failureThreshold: 30
          livenessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 30
            periodSeconds: 15
      volumes:
        - name: config
          secret:
            secretName: ocm-secrets
            items:
              - { key: manager.yaml, path: config.yaml }
```

- [ ] **Step 3: 校验 YAML 与 healthz 端点存在**

Run:
```bash
python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/manager-api.yaml'))); d=[x for x in docs if x['kind']=='Deployment'][0]; c=d['spec']['template']['spec']['containers'][0]; assert c['volumeMounts'][0]['mountPath']=='/etc/manager'; print('manager-api.yaml OK')"
grep -rq "healthz" internal/ && echo "healthz route exists"
```
Expected: `manager-api.yaml OK` 且 `healthz route exists`

- [ ] **Step 4: Commit**

```bash
git add deploy/k8s/local/manager-api.yaml deploy/k8s/local/manager-rbac.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 manager-api Deployment/Service 与 RBAC

manager-api 以 :dev 镜像跑集群内，OCM_CONFIG 指向由 ocm-secrets 的
manager.yaml 键挂载的 /etc/manager/config.yaml，/healthz 做存活/就绪探针。
预建 ServiceAccount 并绑定 oc-apps 编排权限（deploy/svc/secret/cm CRUD +
pods/log 读，不含 pods/exec），作为 spec §7 契约供 spec-A/B 直接生效。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: manager-web Deployment + Service

**Files:**
- Create: `deploy/k8s/local/manager-web.yaml`

设计依据：web 镜像是 nginx 托管静态 SPA（`web/Dockerfile`），内部只做 SPA fallback；`/api` 路由由上层（这里是 traefik Ingress，Task 10）完成。

- [ ] **Step 1: 写 manager-web.yaml**

`deploy/k8s/local/manager-web.yaml`：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: manager-web
  namespace: ocm
spec:
  selector: { app: manager-web }
  ports:
    - port: 80
      targetPort: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: manager-web
  namespace: ocm
spec:
  replicas: 1
  selector: { matchLabels: { app: manager-web } }
  template:
    metadata:
      labels: { app: manager-web }
    spec:
      containers:
        - name: manager-web
          image: k3d-ocm-registry.localhost:5000/oc-manager-web:dev
          imagePullPolicy: Always
          ports:
            - containerPort: 80
          readinessProbe:
            httpGet: { path: /, port: 80 }
            initialDelaySeconds: 3
            periodSeconds: 5
            failureThreshold: 20
```

- [ ] **Step 2: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/manager-web.yaml'))); assert {d['kind'] for d in docs}=={'Service','Deployment'}; print('manager-web.yaml OK')"`
Expected: `manager-web.yaml OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/manager-web.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 manager-web Deployment/Service

nginx 托管的 SPA 静态镜像（:dev），仅服务静态资源与 SPA 兜底；/api 与
/healthz 的反代由 traefik Ingress 统一处理（见 ingress.yaml）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: new-api Deployment + Service（指共享 MySQL）

**Files:**
- Create: `deploy/k8s/local/new-api.yaml`

设计依据：spec §7「new-api DB 指共享 MySQL（改 DSN）」。new-api 支持 MySQL DSN 格式 `user:pass@tcp(host:3306)/db`。镜像走 ACR ywjs_public 的 new-api。

- [ ] **Step 1: 写 new-api.yaml**

`deploy/k8s/local/new-api.yaml`：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: new-api
  namespace: ocm
spec:
  selector: { app: new-api }
  ports:
    - port: 3000
      targetPort: 3000
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: new-api
  namespace: ocm
spec:
  replicas: 1
  selector: { matchLabels: { app: new-api } }
  template:
    metadata:
      labels: { app: new-api }
    spec:
      containers:
        - name: new-api
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/new-api:latest
          args: ["--log-dir", "/app/logs"]
          env:
            # new-api 的 MySQL DSN 格式：user:pass@tcp(host:port)/dbname
            - name: SQL_DSN
              value: "ocm:ocm@tcp(mysql:3306)/new-api"
            - name: REDIS_CONN_STRING
              value: "redis://:ocm@redis:6379"
            - name: TZ
              value: Asia/Shanghai
            - name: ERROR_LOG_ENABLED
              value: "true"
            - name: BATCH_UPDATE_ENABLED
              value: "true"
            - name: NODE_NAME
              value: new-api-node-1
            - name: STREAMING_TIMEOUT
              value: "600"
          ports:
            - containerPort: 3000
          readinessProbe:
            httpGet: { path: /api/status, port: 3000 }
            initialDelaySeconds: 10
            periodSeconds: 5
            failureThreshold: 30
```

> 注：`SQL_DSN` 用 `ocm` 用户，但 mysql-init 只把 `ocm` 用户授权到 `ocm` 库；需补授 `new-api` 库。**回到 Task 4 的 init.sql**，确保也执行 `GRANT ALL PRIVILEGES ON \`new-api\`.* TO 'ocm'@'%';`（已在下方校验步骤强制核对，若缺则补）。

- [ ] **Step 2: 核对 new-api 用户对 new-api 库有授权**

Run: `grep -q "new-api\`.\* TO 'ocm'" deploy/k8s/local/mysql.yaml && echo "grant present" || echo "MISSING GRANT - fix Task4 init.sql"`
Expected: `grant present`
若输出 `MISSING GRANT`，在 `deploy/k8s/local/mysql.yaml` 的 init.sql 内 `GRANT ALL PRIVILEGES ON \`ocm\`.* ...` 后追加一行：
```sql
GRANT ALL PRIVILEGES ON `new-api`.* TO 'ocm'@'%';
```
并把该改动并入本任务提交。

- [ ] **Step 3: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/new-api.yaml'))); d=[x for x in docs if x['kind']=='Deployment'][0]; envs={e['name']:e['value'] for e in d['spec']['template']['spec']['containers'][0]['env']}; assert envs['SQL_DSN']=='ocm:ocm@tcp(mysql:3306)/new-api'; print('new-api.yaml OK')"`
Expected: `new-api.yaml OK`

- [ ] **Step 4: Commit**

```bash
git add deploy/k8s/local/new-api.yaml deploy/k8s/local/mysql.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 new-api Deployment/Service（指共享 MySQL）

new-api 改用 MySQL DSN 指向共享 mysql 的 new-api 库（原 compose 用独立
postgres），Redis 指共享 redis；并在 mysql-init 给 ocm 用户补授 new-api
库权限。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: ragflow Deployment + Service（指共享 MySQL/Redis/ES/MinIO）

**Files:**
- Create: `deploy/k8s/local/ragflow.yaml`

设计依据：compose 的 ragflow env 映射（`docker-compose.yml:227-267`）+ `service_conf.yaml.template`。ragflow 指共享 MySQL（库 `rag_flow`，用 root）、共享 Redis（db1）、共享 ES、共享 MinIO。`--enable-adminserver` 暴露 9381。

- [ ] **Step 1: 写 ragflow.yaml**

`deploy/k8s/local/ragflow.yaml`（service_conf 模板由 ConfigMap 挂入；env 提供模板插值变量）：

```yaml
# ragflow 的 service_conf 模板，挂到容器内由 ragflow 启动脚本用环境变量插值。
# 内容取自 deploy/ragflow/service_conf.yaml.template；本地指向集群内共享后端。
apiVersion: v1
kind: ConfigMap
metadata:
  name: ragflow-conf
  namespace: ocm
data:
  service_conf.yaml.template: |
    ragflow:
      host: ${RAGFLOW_HOST:-0.0.0.0}
      http_port: 9380
    admin:
      host: ${RAGFLOW_HOST:-0.0.0.0}
      http_port: 9381
    mysql:
      name: '${MYSQL_DBNAME:-rag_flow}'
      user: '${MYSQL_USER:-root}'
      password: '${MYSQL_PASSWORD:-infini_rag_flow}'
      host: '${MYSQL_HOST:-mysql}'
      port: ${MYSQL_PORT:-3306}
      max_connections: 900
      stale_timeout: 300
      max_allowed_packet: ${MYSQL_MAX_PACKET:-1073741824}
    minio:
      user: '${MINIO_USER:-ocm}'
      password: '${MINIO_PASSWORD:-ocmsecret123}'
      host: '${MINIO_HOST:-minio}:9000'
      bucket: '${MINIO_BUCKET:-}'
      prefix_path: '${MINIO_PREFIX_PATH:-}'
    es:
      hosts: 'http://${ES_HOST:-elasticsearch}:9200'
      username: '${ES_USER:-elastic}'
      password: '${ELASTIC_PASSWORD:-infini_rag_flow}'
    redis:
      db: 1
      username: '${REDIS_USERNAME:-}'
      password: '${REDIS_PASSWORD:-infini_rag_flow}'
      host: '${REDIS_HOST:-redis}:6379'
    user_default_llm:
      default_models:
        embedding_model:
          api_key: 'xxx'
          base_url: 'http://${TEI_HOST:-tei}:80'
    permission:
      switch: false
      component: false
      dataset: false
---
apiVersion: v1
kind: Service
metadata:
  name: ragflow
  namespace: ocm
spec:
  selector: { app: ragflow }
  ports:
    - { name: web, port: 80, targetPort: 80 }
    - { name: http-api, port: 9380, targetPort: 9380 }
    - { name: admin-api, port: 9381, targetPort: 9381 }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ragflow
  namespace: ocm
spec:
  replicas: 1
  selector: { matchLabels: { app: ragflow } }
  strategy: { type: Recreate }
  template:
    metadata:
      labels: { app: ragflow }
    spec:
      containers:
        - name: ragflow
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/ragflow:v0.25.6
          args: ["--enable-adminserver"]
          env:
            - name: DOC_ENGINE
              value: elasticsearch
            - name: DEVICE
              value: cpu
            - name: ES_HOST
              value: elasticsearch
            - name: ELASTIC_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: elastic-password } }
            - name: MYSQL_HOST
              value: mysql
            - name: MYSQL_PORT
              value: "3306"
            - name: MYSQL_DBNAME
              value: rag_flow
            - name: MYSQL_USER
              value: root
            - name: MYSQL_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: mysql-root-password } }
            - name: MYSQL_MAX_PACKET
              value: "1073741824"
            - name: MINIO_HOST
              value: minio
            - name: MINIO_USER
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: minio-root-user } }
            - name: MINIO_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: minio-root-password } }
            - name: REDIS_HOST
              value: redis
            - name: REDIS_PASSWORD
              valueFrom: { secretKeyRef: { name: ocm-secrets, key: redis-password } }
            - name: API_PROXY_SCHEME
              value: python
            - name: REGISTER_ENABLED
              value: "1"
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 80
            - containerPort: 9380
            - containerPort: 9381
          volumeMounts:
            - name: conf
              mountPath: /ragflow/conf/service_conf.yaml.template
              subPath: service_conf.yaml.template
              readOnly: true
          readinessProbe:
            httpGet: { path: /, port: 80 }
            initialDelaySeconds: 30
            periodSeconds: 10
            failureThreshold: 60
      volumes:
        - name: conf
          configMap: { name: ragflow-conf }
```

- [ ] **Step 2: 校验 YAML**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/local/ragflow.yaml'))); kinds=sorted(d['kind'] for d in docs); assert kinds==['ConfigMap','Deployment','Service'], kinds; print('ragflow.yaml OK')"`
Expected: `ragflow.yaml OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/ragflow.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 ragflow Deployment/Service（指共享后端）

ragflow 指向共享 MySQL（rag_flow 库，root）、Redis（db1）、ES、MinIO；
service_conf 模板由 ConfigMap 挂入并用 env 插值。Recreate 策略避免有状态
冲突，暴露 web(80)/http-api(9380)/admin(9381)。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: traefik Ingress（*.localhost 路由）

**Files:**
- Create: `deploy/k8s/local/ingress.yaml`

设计依据：k3d 内置 traefik 监听 `:80`（cluster create 映射 `80:80@loadbalancer`，Task 13）。`ocm.localhost` 同域下 `/api`+`/healthz`→manager-api、`/`→manager-web（复刻 `deploy/manage/nginx.conf` 的路由，保证 cookie 同源）。

- [ ] **Step 1: 写 ingress.yaml**

`deploy/k8s/local/ingress.yaml`（traefik 用标准 Ingress 资源即可；路径优先级：精确/前缀更长的先匹配，traefik 默认按规则长度排序，但显式分 path 更稳妥）：

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ocm
  namespace: ocm
  annotations:
    ingress.kubernetes.io/ssl-redirect: "false"
spec:
  ingressClassName: traefik
  rules:
    # 控制台主域：/api 与 /healthz 走后端，其余走前端 SPA。
    - host: ocm.localhost
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: manager-api
                port: { number: 8080 }
          - path: /healthz
            pathType: Prefix
            backend:
              service:
                name: manager-api
                port: { number: 8080 }
          - path: /
            pathType: Prefix
            backend:
              service:
                name: manager-web
                port: { number: 80 }
    # new-api 后台
    - host: newapi.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: new-api
                port: { number: 3000 }
    # ragflow 控制台
    - host: ragflow.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ragflow
                port: { number: 80 }
```

- [ ] **Step 2: 校验 YAML 与路由规则**

Run: `python3 -c "import yaml; ing=yaml.safe_load(open('deploy/k8s/local/ingress.yaml')); hosts={r['host'] for r in ing['spec']['rules']}; assert hosts=={'ocm.localhost','newapi.localhost','ragflow.localhost'}, hosts; print('ingress.yaml OK')"`
Expected: `ingress.yaml OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/k8s/local/ingress.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): 新增本地 traefik Ingress（*.localhost 路由）

ocm.localhost 下 /api 与 /healthz 反代 manager-api、其余反代 manager-web
（同源，复刻生产 nginx 路由）；newapi.localhost、ragflow.localhost 分别
指向各自控制台。替代原 compose 的 5173/3000/8088 固定端口映射。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: manager-api 镜像加入 seed-e2e 二进制

**Files:**
- Modify: `cmd/server/Dockerfile:59-61`（builder 加 build seed-e2e）和 `:78-80`（final 拷贝）

设计依据：e2e 要从 compose 的 `go run ./cmd/seed-e2e` 迁到 k3d 的 `kubectl exec manager-api -- seed-e2e`，但当前镜像不含该二进制。

- [ ] **Step 1: builder 阶段增编 seed-e2e**

把 `cmd/server/Dockerfile` 的 builder 构建块（当前 `:59-61`）：

```dockerfile
RUN go build -trimpath -ldflags='-s -w' -o /out/oc-manager ./cmd/server \
 && go build -trimpath -ldflags='-s -w' -o /out/migrate    ./cmd/migrate \
 && go build -trimpath -ldflags='-s -w' -o /out/seed-admin ./cmd/seed-admin
```

改为：

```dockerfile
RUN go build -trimpath -ldflags='-s -w' -o /out/oc-manager ./cmd/server \
 && go build -trimpath -ldflags='-s -w' -o /out/migrate    ./cmd/migrate \
 && go build -trimpath -ldflags='-s -w' -o /out/seed-admin ./cmd/seed-admin \
 && go build -trimpath -ldflags='-s -w' -o /out/seed-e2e   ./cmd/seed-e2e
```

- [ ] **Step 2: final 阶段拷贝 seed-e2e**

把 final 拷贝块（当前 `:78-80`）：

```dockerfile
COPY --from=builder /out/oc-manager /usr/local/bin/oc-manager
COPY --from=builder /out/migrate    /usr/local/bin/migrate
COPY --from=builder /out/seed-admin /usr/local/bin/seed-admin
```

改为追加一行：

```dockerfile
COPY --from=builder /out/oc-manager /usr/local/bin/oc-manager
COPY --from=builder /out/migrate    /usr/local/bin/migrate
COPY --from=builder /out/seed-admin /usr/local/bin/seed-admin
COPY --from=builder /out/seed-e2e   /usr/local/bin/seed-e2e
```

并把 Dockerfile 第 54-58 行的注释里二进制清单补上 seed-e2e（保持注释与实际一致，AGENTS.md「注释与行为一致」要求）：在 `#   seed-admin  : 平台管理员初始化, 首次部署执行` 下加一行 `#   seed-e2e    : Playwright e2e fixture 注入（OCM_E2E=1 守门）`。

- [ ] **Step 3: 验证 Dockerfile 编译产物清单**

Run: `grep -c "seed-e2e" cmd/server/Dockerfile`
Expected: `3`（builder build 1 处 + final COPY 1 处 + 注释 1 处）

- [ ] **Step 4: Commit**

```bash
git add cmd/server/Dockerfile
git commit -m "$(cat <<'EOF'
build(image): manager-api 镜像加入 seed-e2e 二进制

e2e fixture 注入从 compose 的 go run ./cmd/seed-e2e 迁到 k3d 的
kubectl exec manager-api -- seed-e2e，需镜像内自带该二进制。builder 增编、
final 拷贝，并同步更新二进制清单注释。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Makefile —— k3d 集群生命周期（create/down/reset）

**Files:**
- Modify: `Makefile`（`.PHONY` 行加目标；删 `##@ 本地开发` 下 `dev-up`/`dev-down`；新增 `##@ 本地 k3d` 段）

设计依据：spec §6。集群创建带 registry + host-volume（`.k3d-data`）+ `80:80@loadbalancer`。变量集中定义，便于复用。

- [ ] **Step 1: 顶部加 k3d 变量**

在 `Makefile` 第 8 行（`OPENAPI_TS_VERSION := 7.13.0`）之后插入：

```makefile
# —— 本地 k3d 集群参数（spec-D）——
K3D_CLUSTER       ?= ocm
K3D_REGISTRY      ?= ocm-registry
K3D_REGISTRY_PORT ?= 5000
K3D_REGISTRY_HOST := k3d-$(K3D_REGISTRY).localhost:$(K3D_REGISTRY_PORT)
K3D_DATA_DIR      := $(CURDIR)/.k3d-data
K8S_NS            ?= ocm
K8S_LOCAL_DIR     := deploy/k8s/local
KUBECTL           ?= kubectl
# local Secret 文件：默认用入仓的 example；存在真值 secret.yaml 时优先用真值。
SECRET_FILE       := $(if $(wildcard $(K8S_LOCAL_DIR)/secret.yaml),$(K8S_LOCAL_DIR)/secret.yaml,$(K8S_LOCAL_DIR)/secret.example.yaml)
```

- [ ] **Step 2: 删除 compose dev 段**

删除 `Makefile` 中 `##@ 本地开发` 标题及其下的 `dev-up`/`dev-down` 两个目标（当前 `:87-93`）。`##@ 本地开发` 标题保留给 k3d 段复用（见 Step 4），即只删 `dev-up:`/`dev-down:` 两块 recipe 与其注释。

- [ ] **Step 3: 从 .PHONY 移除 dev-up/dev-down，加入 k3d 目标**

把第 1 行 `.PHONY:` 列表里的 `dev-up dev-down` 删除，并追加：

```
local-up local-down local-reset local-build local-migrate local-seed local-seed-e2e local-mc-init local-status local-logs local-shell .guard-k3d-hosts
```

- [ ] **Step 4: 新增 k3d 集群生命周期目标**

在原 `##@ 本地开发` 位置写入：

```makefile
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
		--volume $(K3D_DATA_DIR):/var/lib/rancher/k3s/storage@all \
		--port "80:80@loadbalancer" \
		--wait
	@echo "✅ 集群 $(K3D_CLUSTER) 就绪。registry=$(K3D_REGISTRY_HOST)，数据卷=$(K3D_DATA_DIR)"

local-down: ## 删除 k3d 集群（宿主 .k3d-data 数据保留，下次 up 复用）
	-k3d cluster delete $(K3D_CLUSTER)
	@echo "ℹ️  集群已删除；数据仍在 $(K3D_DATA_DIR)（如需清空跑 make local-reset）"

local-reset: local-down ## 删集群并清空 .k3d-data，干净重建（不自动 up）
	rm -rf $(K3D_DATA_DIR)
	@echo "✅ 已清空 $(K3D_DATA_DIR)；跑 make local-up 干净重建"
```

> `.PHONY` 也需补 `cluster-create`（与上面一并加入）。

- [ ] **Step 5: 校验 Makefile 语法（不真正建集群）**

Run: `make -n local-reset 2>&1 | head -5; echo "---"; make -n cluster-create 2>&1 | head -8`
Expected: 打印将执行的 `k3d cluster delete`/`rm -rf .../.k3d-data` 与 `k3d cluster create ... --registry-create ocm-registry:0.0.0.0:5000 ... --volume .../.k3d-data:...@all --port 80:80@loadbalancer`，无 "No rule to make target" 报错。

- [ ] **Step 6: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
feat(make): 新增 k3d 集群生命周期目标，移除 compose dev

删除 dev-up/dev-down；新增 cluster-create（带 local registry、宿主
.k3d-data 数据卷、80:80 端口映射）、local-down（保留数据）、local-reset
（清空 .k3d-data 干净重建），并加 .guard-k3d-hosts 校验 registry 主机名
已写入 /etc/hosts。集中定义 K3D_* 变量供后续 target 复用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Makefile —— 本地镜像构建推送（local-build）

**Files:**
- Modify: `Makefile`（`##@ 本地开发 (k3d)` 段追加 `local-build`）

设计依据：spec §6。本地构建 manager-api/web 镜像、打 `:dev` tag 推 k3d registry、`rollout restart` 让 `imagePullPolicy: Always` 重新拉取。复用现有 Dockerfile（`cmd/server/Dockerfile`、`web/Dockerfile`）。

- [ ] **Step 1: 写 local-build 目标**

在 k3d 段追加：

```makefile
local-build: ## 构建 manager-api/web 镜像推 k3d registry 并滚动重启（改 Go/前端代码后跑）
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-api:dev -f cmd/server/Dockerfile .
	docker build -t $(K3D_REGISTRY_HOST)/oc-manager-web:dev -f web/Dockerfile ./web
	docker push $(K3D_REGISTRY_HOST)/oc-manager-api:dev
	docker push $(K3D_REGISTRY_HOST)/oc-manager-web:dev
	-$(KUBECTL) -n $(K8S_NS) rollout restart deploy/manager-api deploy/manager-web
	@echo "✅ 镜像已推送并触发滚动重启"
```

- [ ] **Step 2: 校验目标存在**

Run: `make -n local-build 2>&1 | grep -c "docker build\|docker push\|rollout restart"`
Expected: `5`

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
feat(make): 新增 local-build 构建推送本地镜像并滚动重启

复用 cmd/server/Dockerfile 与 web/Dockerfile 构建 manager-api/web 的 :dev
镜像，推 k3d local registry，再 rollout restart 让 imagePullPolicy:Always
重新拉取，替代 compose 的源码挂载热重载。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: Makefile —— local-up 编排 + 运维目标

**Files:**
- Modify: `Makefile`（k3d 段追加 `local-up`、`local-migrate`、`local-seed`、`local-seed-e2e`、`local-mc-init`、`local-status`、`local-logs`、`local-shell`）

设计依据：spec §5 编排顺序（无 Job，关键步骤 kubectl exec）。注意 ocm 迁移由 manager-api 启动期自动完成，local-up 不需单独迁移。

- [ ] **Step 1: 写 local-up 与运维目标**

在 k3d 段追加：

```makefile
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
```

> `.PHONY` 已在 Task 14 Step 3 登记这些目标。`local-mc-init` 假定 minio 镜像内含 `mc`；若该 minio 镜像无 `mc`，改用 `local-mc-init` 跑一个临时 `kubectl run --rm` 的 `minio/mc` 客户端 pod（见本任务 Step 3 的回退说明）。

- [ ] **Step 2: 校验 local-up 编排顺序**

Run: `make -n local-up 2>&1 | grep -nE "namespace|secret|mysql.yaml|rollout status statefulset/mysql|local-mc-init|manager-api.yaml|rollout status deploy/manager-api|local-seed" | head -20`
Expected: 顺序为 namespace → secret → 四个 DB apply → DB rollout status → mc-init → 控制面 apply → manager-api rollout → seed。

- [ ] **Step 3: 校验 minio 镜像含 mc（决定 local-mc-init 形态）**

Run（集群尚未起时跳过，此步在首次 `local-up` 后人工确认；若 `local-mc-init` 报 `mc: not found`，把该目标改成）：
```makefile
local-mc-init:
	$(KUBECTL) -n $(K8S_NS) run mc-init --rm -i --restart=Never \
		--image=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/mc:latest -- \
		sh -c 'mc alias set local http://minio:9000 ocm ocmsecret123 && mc mb -p local/oc-apps && mc mb -p local/ragflow && mc ls local'
```
Expected: 记录采用哪种形态；本步无独立断言，结论并入 Task 23 验收。

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
feat(make): 新增 local-up 编排与 k3d 运维目标

local-up 串起建集群→构建镜像→apply（namespace/secret→四个 DB→等就绪→
建桶→控制面/业务/RBAC/Ingress→等就绪→种子管理员）。新增 local-mc-init
（建 oc-apps/ragflow 桶）、local-migrate/local-seed/local-seed-e2e（均
kubectl exec manager-api）、local-status/local-logs/local-shell 运维目标。
ocm 迁移由 manager-api 启动期自动完成，故 local-up 不单独迁移。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: Makefile —— 把 build/test/vet/sqlc/migrate/seed-e2e 迁离 compose

**Files:**
- Modify: `Makefile`（`test`/`integration-test`/`vet`/`build`/`sqlc-generate`/`web-test`/`web-typecheck`/`web-build`/`migrate-up`/`migrate-down`/`seed-e2e`/`logs`/`check-compose` 等依赖 `docker compose` 的目标）

设计依据：spec §9。删 `docker-compose.yml` 后，这些 `docker compose run/exec` 目标全失效，必须改为本地工具链或 k3d exec。

- [ ] **Step 1: 改 Go 测试/构建/生成目标用本地工具链**

把以下目标（当前 `:97-130`、`:291-307`）改为本地直接执行（开发者本机已装 Go 1.25 / Node 22；这是删 compose 后的标准前提，写入 docs/local-development.md，Task 19）：

```makefile
test: ## 跑 Go 单元测试 (go test ./...)
	go test ./...

integration-test: ## 跑集成测试（需本地 k3d MySQL/Redis 经端口转发或外部实例，见 docs/local-development.md）
	INTEGRATION_DATABASE_URL="$${INTEGRATION_DATABASE_URL:?需指向可达的 MySQL，如经 kubectl port-forward}" \
	INTEGRATION_REDIS_ADDR="$${INTEGRATION_REDIS_ADDR:?需指向可达的 Redis}" \
	go test -tags=integration ./...

vet: ## 跑 go vet ./...
	go vet ./...

build: ## 编译 server / migrate / oc-runtime-agent 到 tmp/build/
	go build -o ./tmp/build/server ./cmd/server
	go build -o ./tmp/build/migrate ./cmd/migrate
	go build -o ./tmp/build/oc-runtime-agent ./runtime/agent

sqlc-generate: ## 跑 sqlc generate, 覆盖 internal/store 生成代码
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

web-test: ## 在 web/ 跑 vitest 单测
	cd web && npm install && npm test -- --run

web-typecheck: ## 在 web/ 跑 vue-tsc --noEmit
	cd web && npm install && npm run typecheck

web-build: ## 在 web/ 跑 vite build
	cd web && npm install && npm run build
```

- [ ] **Step 2: migrate/seed-e2e/logs/check-compose 迁到 k3d 或删除**

- `migrate-up` / `migrate-down`：改为委托 k3d 目标（保留旧名做别名，避免肌肉记忆失效）：

```makefile
migrate-up: ## 对本地 k3d 数据库执行 up 迁移（= local-migrate）
	$(MAKE) local-migrate

migrate-down: ## 回滚本地 k3d 最近一次迁移（= local-migrate DOWN=1）
	$(MAKE) local-migrate DOWN=1
```

- `seed-e2e`：改为委托 `local-seed-e2e`：

```makefile
seed-e2e: ## 注入 Playwright e2e fixture（= local-seed-e2e）
	$(MAKE) local-seed-e2e
```

- `logs`：改为 `local-status` 提示（删 compose logs）：

```makefile
logs: ## 提示用 local-logs（compose 已移除）
	@echo "compose 已移除，请用：make local-logs svc=<服务名>（如 manager-api）"
```

- `check-compose`：该目标依赖 `scripts/check-compose-bind-mounts.sh` 校验 compose 挂载，compose 删除后无意义。**删除 `check-compose` 目标**，并从 `.PHONY` 移除 `check-compose`。脚本文件 `scripts/check-compose-bind-mounts.sh` 属遗留物，**本任务不删脚本**（仅提及，遵循「不删他人既有文件」），在 Task 19 docs 里标注其失效。

- [ ] **Step 3: 校验无残留 docker compose 引用（除生产 deploy/）**

Run: `grep -nE "docker compose|docker-compose" Makefile | grep -v "deploy/" || echo "NO compose refs in Makefile core"`
Expected: `NO compose refs in Makefile core`（若仍有命中，逐条改掉）

- [ ] **Step 4: 校验 Go 目标可解析**

Run: `make -n test build vet 2>&1 | grep -E "go test|go build|go vet"`
Expected: 打印 `go test ./...`、三条 `go build ...`、`go vet ./...`，且不含 `docker compose`。

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "$(cat <<'EOF'
refactor(make): 测试/构建/迁移目标迁离 compose

删 docker-compose.yml 的前置改造：test/vet/build/sqlc-generate/web-* 改用
本机 Go/Node 工具链；migrate-up/down、seed-e2e 委托对应 k3d local-* 目标；
logs 改提示 local-logs；删除失效的 check-compose 目标。integration-test
改为要求显式提供可达的 MySQL/Redis 连接（经 kubectl port-forward 或外部实例）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 18: 删除根 docker-compose.yml

**Files:**
- Delete: `docker-compose.yml`

设计依据：spec 决策 #10。先确认 Makefile 已无 compose 依赖（Task 17 完成），再删。`.local/` 数据按决策保留不删。

- [ ] **Step 1: 确认无引用后删除**

Run:
```bash
grep -rnl "docker-compose.yml\|docker compose" --include=Makefile . 2>/dev/null | grep -v deploy/ ; \
git rm docker-compose.yml
```
Expected: 第一条 grep 无输出（核心已无引用），`git rm` 显示 `rm 'docker-compose.yml'`。

- [ ] **Step 2: 确认 e2e 仍引用（留到 Task 19 修）**

Run: `grep -rn "make seed-e2e\|docker compose" web/ || echo "web clean"`
Expected: 命中 `web/tests/e2e/global-setup.ts` 的 `make seed-e2e`（Task 19 处理）。仅记录，不在此修。

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
chore: 删除根 docker-compose.yml，本地开发改用 k3d

本地联调统一走 make local-up（k3d 全栈）。Makefile 已先行迁离 compose
（见上一提交）。按 spec-D 决策，.local 旧数据暂时保留不删，作为过渡期
安全网；compose 文件删除后它即成孤儿数据。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 19: Playwright e2e 迁到 k3d

**Files:**
- Modify: `web/tests/e2e/global-setup.ts`
- Modify: `web/playwright.config.ts:30`（baseURL 默认值）

设计依据：globalSetup 现在跑 `make seed-e2e`（已在 Task 17 委托给 `local-seed-e2e` → `kubectl exec manager-api -- seed-e2e`），仍打印 fixture JSON 末行，所以 globalSetup **逻辑不变**，但要把 baseURL 默认从 `http://localhost:5173` 改为 `http://ocm.localhost`。

- [ ] **Step 1: 改 playwright baseURL 默认值**

`web/playwright.config.ts` 第 30 行：

```typescript
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173',
```

改为：

```typescript
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://ocm.localhost',
```

- [ ] **Step 2: 更新 global-setup 注释（命令链路已变）**

`web/tests/e2e/global-setup.ts` 中描述 `make seed-e2e` 行为的注释（第 5-9 行附近）补一句说明现在 `make seed-e2e` 委托 `kubectl exec manager-api -- seed-e2e`（k3d）。把：

```typescript
// 1. 在仓库根跑 `make seed-e2e`，把 truncate 业务表 + 重新构造 fixture 一并完成；
```

改为：

```typescript
// 1. 在仓库根跑 `make seed-e2e`（k3d 下委托 kubectl exec manager-api -- seed-e2e），
//    把 truncate 业务表 + 重新构造 fixture 一并完成；
```

代码体 `execSync('make seed-e2e', ...)` **不变**（仍调 make target，target 内部已切到 k3d）。

- [ ] **Step 3: 校验**

Run: `grep -q "ocm.localhost" web/playwright.config.ts && grep -q "kubectl exec manager-api" web/tests/e2e/global-setup.ts && echo "e2e migrated"`
Expected: `e2e migrated`

- [ ] **Step 4: Commit**

```bash
git add web/playwright.config.ts web/tests/e2e/global-setup.ts
git commit -m "$(cat <<'EOF'
test(e2e): Playwright 迁到 k3d（baseURL ocm.localhost）

baseURL 默认从 http://localhost:5173 改为 http://ocm.localhost（traefik
Ingress）。globalSetup 仍跑 make seed-e2e，但该 target 已委托
kubectl exec manager-api -- seed-e2e，注释同步说明链路变化。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 20: 重写 docs/local-development.md + 同步端口说明

**Files:**
- Modify: `docs/local-development.md`（重写为 k3d 流程）
- Modify: `CLAUDE.md` 与根 `AGENTS.md` 的「本地调试账号」表端口列

设计依据：spec §9。先看现有文件确认结构再改。

- [ ] **Step 1: 重写 docs/local-development.md**

先 `Read docs/local-development.md` 了解既有章节，再整体替换为 k3d 版，核心内容（保留文件原有的“账号”等仍有效段落，仅替换 compose 流程部分）：

````markdown
# 本地开发环境（k3d）

本地开发统一用 k3d 跑全栈，已取代旧的 docker-compose 联调栈
（根 `docker-compose.yml` 已删除）。

## 前置依赖

- Docker、`k3d`、`kubectl`、Go 1.25、Node 22（后两者用于本机跑测试/构建）。
- 一次性配置：把 k3d registry 主机名指向本机：
  ```bash
  echo '127.0.0.1 k3d-ocm-registry.localhost' | sudo tee -a /etc/hosts
  ```

## 一键起停

```bash
make local-up      # 建集群→构建镜像→部署全栈→建桶→种子管理员
make local-status  # 查看 pod / ingress
make local-down    # 删集群（.k3d-data 数据保留）
make local-reset   # 删集群并清空 .k3d-data，干净重建（随后再 make local-up）
```

## 访问入口（traefik Ingress, *.localhost → 127.0.0.1:80）

| 服务 | 地址 | 账号 | 密码 |
|---|---|---|---|
| manager 后台 | http://ocm.localhost | `admin`（组织标识留空）| `admin123` |
| new-api 后台 | http://newapi.localhost | `admin` | `admin123!` |
| ragflow 控制台 | http://ragflow.localhost | `admin@ragflow.io` | `admin` |

## 改代码后

- 改 Go / 前端代码：`make local-build`（重建 :dev 镜像 + 推 registry + 滚动重启）。
- 跑测试/检查（本机工具链）：`make test`、`make vet`、`make web-test`、`make web-typecheck`。
- 数据库迁移：`make local-migrate`（或 `make migrate-up`，等价）。
- e2e fixture：`make local-seed-e2e`（或 `make seed-e2e`）。
- 看日志 / 进容器：`make local-logs svc=manager-api`、`make local-shell svc=manager-api`。

## 数据持久化

- 有状态件（MySQL/Redis/ES/MinIO）的 PVC 数据落在宿主 `<repo>/.k3d-data`，
  跨 `make local-down`/`local-up` 持久；`make local-reset` 才清空。
- 旧 compose 的 `.local/` 数据已不再使用，但暂时保留未删（过渡期安全网）。
- `scripts/check-compose-bind-mounts.sh` 是 compose 时代遗留校验脚本，已失效。

## 已知限制（依赖 spec-A/B/E）

- 创建真实 app 实例、渠道绑定等依赖 k8s 编排 / oc-ops 的路径，在 spec-A/B/E
  落地前不可用。当前可验证：登录、组织/成员管理、new-api provision、助手版本、
  知识库等不依赖 app pod 编排的功能。
````

- [ ] **Step 2: 同步 CLAUDE.md / AGENTS.md 端口说明**

`CLAUDE.md` 与根 `AGENTS.md`「本地调试账号」表里 manager `http://localhost:5173`、new-api `http://localhost:3000`、RAGFlow `http://localhost:8088` 改为 `http://ocm.localhost` / `http://newapi.localhost` / `http://ragflow.localhost`；并把表下方「以上端口为默认值，可通过 .env 的 NEWAPI_PORT / RAGFLOW_WEB_HTTP_PORT 等覆盖」一句改为「以上地址经 k3d traefik Ingress 暴露（*.localhost → 127.0.0.1:80），见 docs/local-development.md」。

> 注意：`CLAUDE.md` 与根 `AGENTS.md` 可能内容相同或互相 include；两文件都改，保持一致。

- [ ] **Step 3: 校验**

Run: `grep -q "ocm.localhost" docs/local-development.md && grep -q "ocm.localhost" CLAUDE.md && grep -q "ocm.localhost" AGENTS.md && echo "docs synced"`
Expected: `docs synced`

- [ ] **Step 4: Commit**

```bash
git add docs/local-development.md CLAUDE.md AGENTS.md
git commit -m "$(cat <<'EOF'
docs: 本地开发文档与端口说明迁到 k3d

重写 docs/local-development.md 为 k3d 流程（前置依赖、make local-* 一键
起停、*.localhost 访问入口、改代码/迁移/e2e/日志命令、数据持久化与 A/B/E
未落地的已知限制）。同步 CLAUDE.md 与 AGENTS.md 本地调试账号表的访问地址
从 localhost:端口 改为 *.localhost Ingress。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 21: 生产 prod/ manifest（只生成，不部署）

**Files:**
- Create: `deploy/k8s/prod/00-namespace.yaml`、`secret.example.yaml`、`manager-api.yaml`、`manager-rbac.yaml`、`manager-web.yaml`、`new-api.yaml`、`ragflow.yaml`、`ingress.yaml`、`storageclass.example.yaml`、`README.md`

设计依据：spec §1/§9。prod 是 local 的「去 DB + 外部连接占位 + ACR 镜像 + 域名/TLS」变体。manager-api/web/new-api/ragflow 的工作负载结构与 local 几乎一致，差异仅镜像 ref、连接指向外部、imagePullSecrets。

- [ ] **Step 1: namespace + RBAC（复用 local 结构）**

`deploy/k8s/prod/00-namespace.yaml` 内容与 `deploy/k8s/local/00-namespace.yaml` 相同（ns `ocm` + `oc-apps`）。`deploy/k8s/prod/manager-rbac.yaml` 与 `deploy/k8s/local/manager-rbac.yaml` 相同（RBAC 与环境无关）。

- [ ] **Step 2: secret.example.yaml（外部连接占位）**

`deploy/k8s/prod/secret.example.yaml`：

```yaml
# 生产 Secret 占位 —— 复制为 secret.yaml 后填真实值，切勿提交真值。
# 有状态后端（MySQL/Redis/ES/对象存储）由外部托管，这里只填连接信息。
apiVersion: v1
kind: Secret
metadata:
  name: ocm-secrets
  namespace: ocm
type: Opaque
stringData:
  # manager-api 完整配置：把外部 MySQL/Redis、OSS、域名、master_key 填好。
  manager.yaml: |
    app:
      env: prod
      http_addr: ":8080"
      public_base_url: "https://REPLACE_WITH_DOMAIN"
      data_root: "/var/lib/oc-manager/data"
    database:
      url: "mysql://USER:PASS@tcp(EXTERNAL_MYSQL_HOST:3306)/ocm?parseTime=true&loc=UTC&charset=utf8mb4&multiStatements=true"
    redis:
      addr: "EXTERNAL_REDIS_HOST:6379"
      password: "REPLACE"
      db: 0
      key_prefix: "ocm:"
    auth:
      cookie_domain: "REPLACE_WITH_DOMAIN"
      access_token_ttl: "1h"
      refresh_token_ttl: "720h"
      jwt_access_secret: "REPLACE_WITH_32B_SECRET"
      jwt_refresh_secret: "REPLACE_WITH_32B_SECRET"
      csrf_secret: "REPLACE_WITH_32B_SECRET"
    security:
      master_key: "REPLACE_WITH_BASE64_32B_KEY"
    newapi:
      base_url: "http://new-api:3000"
      admin_token: "REPLACE"
      admin_user_id: 1
    ragflow:
      base_url: "http://ragflow:9380"
      api_key: "REPLACE_AFTER_RAGFLOW_PROVISION"
      request_timeout: "30s"
      chunk_method: "naive"
    hermes:
      runtime_images:
        - id: "v2026.5.16"
          label: "Hermes v2026.5.16"
          ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-2026-05-21-12-00-00"
      system_prompt_template: |
        你是 Hermes 智能助手。工作目录是 /opt/data/workspace/。
      workspace:
        archive_retention_days: 14
      llm:
        base_url: "http://new-api:3000/v1"
        default_provider: "openai"
        default_model: "REPLACE"
    agent:
      heartbeat_interval_seconds: 30
    runtime:
      enrollment_secret: "REPLACE_WITH_BASE64_32B"
      probe:
        interval_seconds: 10
        timeout_seconds: 3
        failure_threshold: 1
        recovery_threshold: 1
  # new-api / ragflow 等服务连接外部后端用的散装值
  mysql-root-password: "REPLACE"
  redis-password: "REPLACE"
  minio-root-user: "REPLACE"
  minio-root-password: "REPLACE"
  elastic-password: "REPLACE"
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
    {"auths":{"crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com":{"username":"REPLACE","password":"REPLACE","auth":"REPLACE_BASE64"}}}
```

- [ ] **Step 3: manager-api/web/new-api/ragflow 生产工作负载**

四个文件结构与 `deploy/k8s/local/` 同名文件一致，仅三处差异：
1. 镜像 ref：manager-api → `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-api:REPLACE_TAG`，web → `.../ywjs_app/oc-manager-web:REPLACE_TAG`，`imagePullPolicy: IfNotPresent`；new-api/ragflow 仍用 `ywjs_public` 镜像。
2. 每个 pod template `spec` 增 `imagePullSecrets: [{ name: acr-pull }]`。
3. new-api `SQL_DSN` / ragflow 各 `*_HOST` env 指向外部主机（从 secret 注入或直接占位 `EXTERNAL_*`）；prod **不含** mysql/redis/es/minio 的 StatefulSet 文件。

`deploy/k8s/prod/manager-api.yaml`（完整）：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: manager-api
  namespace: ocm
spec:
  selector: { app: manager-api }
  ports:
    - port: 8080
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: manager-api
  namespace: ocm
spec:
  replicas: 2
  selector: { matchLabels: { app: manager-api } }
  template:
    metadata:
      labels: { app: manager-api }
    spec:
      serviceAccountName: manager-api
      imagePullSecrets:
        - name: acr-pull
      containers:
        - name: manager-api
          image: crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-api:REPLACE_TAG
          imagePullPolicy: IfNotPresent
          env:
            - name: OCM_CONFIG
              value: /etc/manager/config.yaml
            - name: TZ
              value: Asia/Shanghai
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/manager
              readOnly: true
          readinessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 5
            periodSeconds: 5
            failureThreshold: 30
          livenessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 30
            periodSeconds: 15
      volumes:
        - name: config
          secret:
            secretName: ocm-secrets
            items:
              - { key: manager.yaml, path: config.yaml }
```

`deploy/k8s/prod/manager-web.yaml`：与 local 同，镜像改 `.../ywjs_app/oc-manager-web:REPLACE_TAG`、`imagePullPolicy: IfNotPresent`、加 `imagePullSecrets: [{name: acr-pull}]`、`replicas: 2`。

`deploy/k8s/prod/new-api.yaml`：与 local 同，但 `SQL_DSN` 值 `USER:PASS@tcp(EXTERNAL_MYSQL_HOST:3306)/new-api`、`REDIS_CONN_STRING` `redis://:PASS@EXTERNAL_REDIS_HOST:6379`，加 `imagePullSecrets`。

`deploy/k8s/prod/ragflow.yaml`：与 local 同（含 ragflow-conf ConfigMap），但 `MYSQL_HOST`/`REDIS_HOST`/`ES_HOST`/`MINIO_HOST` 改为外部主机占位 `EXTERNAL_*`，加 `imagePullSecrets`。

> 实现时直接复制对应 local 文件再改这三处，确保结构一致（type-consistency）。

- [ ] **Step 4: ingress + storageclass 占位**

`deploy/k8s/prod/ingress.yaml`：复制 local ingress，把 host 改为占位 `REPLACE_WITH_DOMAIN`（manager）、`REPLACE_WITH_NEWAPI_DOMAIN`、`REPLACE_WITH_RAGFLOW_DOMAIN`，`ingressClassName` 注释说明生产可能用 nginx，并加 TLS 块占位：

```yaml
  tls:
    - hosts: [ "REPLACE_WITH_DOMAIN" ]
      secretName: ocm-tls
```

`deploy/k8s/prod/storageclass.example.yaml`：

```yaml
# 生产若有需要 PVC 的业务负载，按云厂商 CSI 填 provisioner。占位示例（阿里云云盘）。
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ocm-ssd
provisioner: diskplugin.csi.alibabacloud.com
parameters:
  type: cloud_essd
reclaimPolicy: Retain
volumeBindingMode: WaitForFirstConsumer
```

- [ ] **Step 5: prod/README.md（填值清单 + apply 顺序）**

`deploy/k8s/prod/README.md`：

```markdown
# 生产部署清单（spec-D，只生成不自动部署）

本目录是标准 k8s YAML，不含有状态后端（MySQL/Redis/ES/对象存储外置）。
manager-api/web、new-api、ragflow 与 RBAC 与本地一致，差异仅镜像 ref、
外部连接、imagePullSecrets、域名/TLS。

## 必填清单

1. `secret.example.yaml` → 复制为 `secret.yaml`，填：
   - manager.yaml：外部 MySQL DSN、Redis、master_key（base64 32B）、
     jwt/csrf secrets、public_base_url/cookie_domain（真实域名）、
     runtime.enrollment_secret（base64 32B）、ragflow.api_key。
   - 散装值：new-api/ragflow 连外部后端的 mysql/redis/es/minio 凭证。
   - `acr-pull`：阿里云 ACR 拉取凭证（见下）。
2. 各工作负载 YAML 的镜像 `REPLACE_TAG` → 实际发布 tag（Makefile release-*-image 产出）。
3. `ingress.yaml` 的 `REPLACE_WITH_*_DOMAIN` 与 TLS secret。
4. 业务若需 PVC，按云厂商改 `storageclass.example.yaml` 的 provisioner。

## 生成 ACR imagePullSecret

```bash
kubectl create secret docker-registry acr-pull -n ocm \
  --docker-server=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com \
  --docker-username='<ACR 用户名>' --docker-password='<ACR 密码>' \
  --dry-run=client -o yaml
```

## apply 顺序

```bash
kubectl apply -f 00-namespace.yaml
kubectl apply -f secret.yaml            # 你填好的真值
kubectl apply -f storageclass.example.yaml   # 按需
kubectl apply -f manager-rbac.yaml
kubectl apply -f manager-api.yaml -f manager-web.yaml -f new-api.yaml -f ragflow.yaml
kubectl apply -f ingress.yaml
```

## 范围外

生产集群创建、从 docker-compose 的 cutover、外部托管 DB/OSS 的实际接入与
数据迁移不在本 spec 内（依赖 spec-A/B/E）。
```

- [ ] **Step 6: .gitignore 忽略 prod 真值**

`.gitignore` 追加 `deploy/k8s/prod/secret.yaml`。

- [ ] **Step 7: 校验全部 prod YAML 语法**

Run:
```bash
for f in deploy/k8s/prod/*.yaml; do python3 -c "import yaml,sys; list(yaml.safe_load_all(open('$f'))); print('$f OK')"; done
```
Expected: 每个 `.yaml` 打印 `... OK`。

- [ ] **Step 8: Commit**

```bash
git add deploy/k8s/prod .gitignore
git commit -m "$(cat <<'EOF'
feat(k8s): 新增生产 prod manifest（只生成不部署）

标准 k8s YAML，不含有状态后端：manager-api(replicas=2)/web/new-api/ragflow
+ RBAC 结构同 local，差异为 ACR 镜像 ref(REPLACE_TAG)、imagePullSecrets、
外部 MySQL/Redis/ES/OSS 连接占位、域名/TLS。附 secret.example、
storageclass 占位与 README（必填清单 + ACR pull secret 生成 + apply 顺序）。
prod/secret.yaml 真值入 gitignore。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 22: contracts/ —— A/B/E 契约样例

**Files:**
- Create: `deploy/k8s/contracts/app-pod.deployment.yaml`、`deploy/k8s/contracts/README.md`

设计依据：spec §7。app-pod/oc-ops 由 manager 运行时渲染、不静态部署；这里只给「字段契约 + 样例 YAML」供 spec-A/B/E 对齐。

- [ ] **Step 1: 写 app-pod 契约样例**

`deploy/k8s/contracts/app-pod.deployment.yaml`：

```yaml
# 契约样例（不 apply）：spec-A/B 运行时由 manager 渲染并 apply 到 oc-apps ns。
# 展示 app pod 的目标形态：单副本 + Recreate + hermes 主容器 + oc-ops 第二容器
#（spec-E）+ 共享 emptyDir + bootstrap/控制 token Secret。<APP_ID> 等为占位。
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-<APP_ID>
  namespace: oc-apps
  labels:
    app: "<APP_ID>"
    app.kubernetes.io/part-of: oc-manager
spec:
  replicas: 1
  strategy:
    type: Recreate          # 先停旧再起新，保证同一 app 至多一个写者（避免 S3 脑裂）
  selector: { matchLabels: { app: "<APP_ID>" } }
  template:
    metadata:
      labels: { app: "<APP_ID>" }
    spec:
      imagePullSecrets:
        - name: acr-pull
      containers:
        # 主容器：Hermes runtime。镜像由助手版本 image_id 解析。
        - name: hermes
          image: "<HERMES_IMAGE_REF>"
          env:
            - name: HERMES_HOME
              value: /opt/data
          volumeMounts:
            - { name: data, mountPath: /opt/data }
        # 第二容器：oc-ops HTTP 服务（spec-E），基于 hermes 镜像构建、同版本标签。
        # manager 通过 per-app 控制 token 调它执行 oc-* 命令；替代旧 agent 的 pods/exec。
        - name: oc-ops
          image: "<OC_OPS_IMAGE_REF>"
          ports:
            - containerPort: 8080
          env:
            - name: OC_OPS_TOKEN
              valueFrom: { secretKeyRef: { name: app-<APP_ID>-token, key: control-token } }
          volumeMounts:
            - { name: data, mountPath: /opt/data }
      volumes:
        - name: data
          emptyDir: {}          # 零 PVC：工作区落 S3，pod 本地仅 emptyDir
---
# per-app 控制 token（manager↔pod 双向复用）。manifest(含 api_key) 不进此处，
# 由 manager bootstrap 端点内存渲染经认证通道交付，见设计 §7。
apiVersion: v1
kind: Secret
metadata:
  name: app-<APP_ID>-token
  namespace: oc-apps
type: Opaque
stringData:
  control-token: "<RANDOM_PER_APP_TOKEN>"
```

- [ ] **Step 2: 写 contracts/README.md**

`deploy/k8s/contracts/README.md`：

```markdown
# A/B/E 契约样例（不 apply）

本目录是 spec-D 为尚未实现的 spec-A/B/E 预先固定的部署契约，**仅文档化**，
不参与 make local-up / prod apply。spec-A/B/E 落地时据此对齐字段。

## app-pod.deployment.yaml

app 实例的目标形态：
- `Deployment replicas=1` + `strategy: Recreate`：同一 app 至多一个写者，
  避免 S3 工作区脑裂。
- 主容器 `hermes`（镜像由助手版本 image_id 解析）+ 第二容器 `oc-ops`
  （spec-E，基于 hermes 镜像、同版本标签，HTTP+token）。
- 共享 `emptyDir /opt/data`：零 PVC，工作区同步到 S3。
- `app-<APP_ID>-token` Secret：manager↔pod 双向复用的 per-app 控制 token
  （pod→manager bootstrap 拉配置、manager→oc-ops 调命令）。

## manager RBAC

manager 对 oc-apps ns 的权限已在 `../local/manager-rbac.yaml` /
`../prod/manager-rbac.yaml` 定义（deploy/svc/secret/cm CRUD + pods/log 读，
不含 pods/exec）。spec-A 的 client-go 编排直接复用该 SA。

## S3 bucket 布局

- app 工作区 prefix、删除时 `archive/` 归档前缀（设计 §5）。
- manifest(含 api_key) 不落 S3，由 manager bootstrap 端点内存渲染。
```

- [ ] **Step 3: 校验**

Run: `python3 -c "import yaml; docs=list(yaml.safe_load_all(open('deploy/k8s/contracts/app-pod.deployment.yaml'))); d=[x for x in docs if x['kind']=='Deployment'][0]; assert d['spec']['strategy']['type']=='Recreate'; names=[c['name'] for c in d['spec']['template']['spec']['containers']]; assert names==['hermes','oc-ops'], names; print('contracts OK')"`
Expected: `contracts OK`

- [ ] **Step 4: Commit**

```bash
git add deploy/k8s/contracts
git commit -m "$(cat <<'EOF'
docs(k8s): 新增 A/B/E 契约样例（app-pod + oc-ops + token）

contracts/ 文档化（不 apply）spec-A/B/E 的部署目标：app Deployment(1)+
Recreate、hermes 主容器 + oc-ops 第二容器、共享 emptyDir 零 PVC、per-app
控制 token Secret，以及 manager RBAC 与 S3 bucket 布局指引。供后续 spec
落地时对齐字段。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 23: 端到端验收（local-up 冒烟 + 三角色浏览器）

**Files:** 无（验收任务，不改代码；若发现缺陷回到对应 Task 修复）

设计依据：spec §10 分层验收。本任务必须真实跑起集群，不能跳过或假装通过（项目交付前检查 + verification 记忆）。

- [ ] **Step 1: 一次性 hosts 配置（首次）**

Run:
```bash
grep -q "k3d-ocm-registry.localhost" /etc/hosts || echo '127.0.0.1 k3d-ocm-registry.localhost' | sudo tee -a /etc/hosts
```
Expected: hosts 含该记录（`.guard-k3d-hosts` 不再报错）。

- [ ] **Step 2: 干净拉起全栈**

Run: `make local-reset; make local-up`
Expected: 末尾打印三个 `*.localhost` 入口；中途各 `rollout status` 返回 `successfully rolled out`。若 `local-mc-init` 报 `mc: not found`，按 Task 16 Step 3 的回退形态改 `local-mc-init` 后重跑 `make local-mc-init`。

- [ ] **Step 3: 集群级断言**

Run:
```bash
kubectl -n ocm get pods
kubectl -n ocm exec statefulset/mysql -- mysql -uroot -pocm -e "show databases" | grep -E "ocm|new-api|rag_flow"
kubectl -n ocm exec deploy/manager-api -- wget -qO- http://127.0.0.1:8080/healthz; echo
curl -s -o /dev/null -w "%{http_code}\n" http://ocm.localhost/healthz
```
Expected: 所有 pod `Running`/`Ready`；三个库都在；`/healthz` 容器内与经 Ingress 都返回 `200`/healthy。

- [ ] **Step 4: 平台管理员登录冒烟**

Run:
```bash
curl -s -X POST http://ocm.localhost/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123","org_code":""}' -i | head -20
```
Expected: HTTP `200`，响应含 access token / Set-Cookie。（登录路由确切路径以 `openapi/openapi.yaml` 为准；若与此不符，按 yaml 调整 URL，仅验证用。）

- [ ] **Step 5: 三角色真实浏览器验证（chrome-devtools MCP）**

注入 e2e fixture 拿到三角色账号：

Run: `make local-seed-e2e`
Expected: 末行打印 fixture JSON，含 `org_admin_login`/`org_member_login` 与密码。

然后用 chrome-devtools MCP 真实浏览器逐角色走查（不可用 curl 替代前端逻辑，遵循 CLAUDE.md 交付前检查）：
- platform_admin（admin/admin123，组织标识留空）：登录 → 组织列表 → 成员/助手版本/知识库页面加载，0 console error。
- org_admin（fixture 账号）：登录 → 本组织范围内页面正常。
- org_member（fixture 账号）：登录 → 受限视图正常；尝试越权写组织知识库 → 期望 403 KNOWLEDGE_FORBIDDEN（沿用 spec-C 验收同款边界）。

逐角色截图 + console 检查，整理为逐项验证矩阵（遵循 verification 记忆要求）。

- [ ] **Step 6: 数据持久化验证**

Run:
```bash
make local-down
make local-up
kubectl -n ocm exec deploy/manager-api -- sh -c 'echo persisted-check'
curl -s -X POST http://ocm.localhost/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123","org_code":""}' -o /dev/null -w "%{http_code}\n"
```
Expected: `local-down`/`local-up` 后 admin 仍能登录（`200`）——证明 `.k3d-data` 持久化生效（管理员是 down 之前 seed 的，未重新 seed 也在）。

- [ ] **Step 7: 记录验收结论**

把 Step 3-6 的命令输出与浏览器截图证据整理成验收矩阵，写入交付说明（不入 git，作为 PR/汇报材料）。明确标注：
- ✅ 本 spec 可验项全过；
- ⏸ app pod 真实编排 / oc-ops / S3 工作区同步 / bootstrap 回调 = 「契约就绪，待 spec-A/B/E 验证」。

- [ ] **Step 8: 无独立提交**（本任务不改仓库文件；若修复缺陷，提交归入对应 Task 的修复 commit）

---

## 自审

**1. Spec 覆盖**（逐节核对 `2026-05-29-spec-d-deploy-k3d-design.md`）：
- §2 决策表 #1 vendor-neutral + ACR：Task 21 prod manifest ✅；#2 本地内置 DB：Task 4-7 ✅；#3 host-volume 持久 + `.k3d-data`：Task 1/14 ✅；#4 业务进 k8s：Task 8-11 ✅；#5 裸 YAML 无 kustomize：全程 ✅；#6 无 base、两套完整：local/ 与 prod/ 各自完整 ✅；#7 manager 默认集群内 InClusterConfig：Task 8（注：当前代码未用 client-go，已在前置约束标注，pod 仍用 SA）✅；#8 不建 Job、kubectl exec：Task 16 ✅；#9 Makefile 仅本地、prod 只生成：Task 14-17 / 21 ✅；#10 删 compose+dev-up/down、保留 .local：Task 17/18 ✅。
- §3 目录：Task 1-12/21/22 覆盖 local/prod/contracts 全部文件 ✅。
- §5 local-up 顺序：Task 16 严格按 namespace→secret→DB→等→建桶→控制面→等→seed ✅。
- §6 Makefile target 表：Task 14-17 覆盖 local-up/down/reset/build/migrate/seed/seed-e2e/mc-init/status/logs/shell ✅。
- §7 配置密钥 + §8：Task 3（manager.yaml in Secret，保留 enrollment_secret）+ Task 21（prod 占位）✅。
- §7 契约：Task 8（RBAC）+ Task 22（app-pod/oc-ops/token/S3）✅。
- §9 清理连带：Task 13（seed-e2e 入镜像）/17（build/web-build/test/vet）/18（删 compose）/19（e2e）/20（docs + 端口）✅。
- §10 验收分层：Task 23 ✅。
- §11 风险（mc 是否在 minio 镜像、`*.localhost` 解析、ragflow api_key 占位）均在对应 Task 给出处置 ✅。

**2. 占位符扫描**：无 TBD/“略”。prod 的 `REPLACE_*` 是**生产填值占位**（设计本意），非计划占位；契约的 `<APP_ID>` 同理。

**3. 类型一致性**：Service/Secret/ConfigMap 名（`mysql`/`redis`/`elasticsearch`/`minio`/`ocm-secrets`/`manager-api` 等）、镜像 tag（`k3d-ocm-registry.localhost:5000/oc-manager-api:dev`）、ns（`ocm`/`oc-apps`）、host（`ocm.localhost` 等）、Makefile 变量（`K3D_*`/`K8S_NS`/`SECRET_FILE`）全计划统一，未见同物异名。manager-api 的 `manager.yaml` Secret 键 → 挂 `/etc/manager/config.yaml` 与 `OCM_CONFIG`（`Dockerfile:84`）一致。

发现并已在计划内处理的一处隐患：new-api 用 `ocm` MySQL 用户但 init 只授权 `ocm` 库 → Task 10 Step 2 强制补 `new-api` 库授权。
