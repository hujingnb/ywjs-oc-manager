# Agent Runtime Manager

> 面向组织的 Hermes Agent 应用管理后台。负责组织 / 成员 / 应用 / Runtime Node
> 编排，对接 new-api 网关计费，集中管控运行在多个 Runtime Node 上的
> Hermes 容器。

## 核心能力

- 平台 / 组织 / 成员三类角色的账号体系与登录。
- 组织生命周期管理 + Token Credit 充值（实际计费由 new-api 完成）。
- 创建成员账号时同步创建该成员名下唯一的 Hermes 应用，自动分配可用 Runtime Node。
- Runtime Node 注册、心跳、健康自愈（unreachable → active），agent token 长期通信凭证。
- 每个应用对应一个 Docker 容器（经 agent 启停）、一个 new-api api_key、最多一个渠道绑定。
- 组织级与应用级双层知识库：manager 主副本 + agent 节点同步，同步状态可视。
- 应用工作目录浏览、单文件下载、文件夹打包下载（经 agent 文件 API 沙箱代理）。
- token 用量直查 new-api，按平台 / 组织 / 应用三级展示，不缓存。
- 容器启停 / 重启 / 日志 / 资源指标 / 健康探针 / 自动重启策略。
- 全平台审计日志。

## 系统拓扑

```text
┌──────────────────────────── 核心层（manager 所在机器）──────────────────────────┐
│                                                                                │
│   Browser ──► Vue 3 SPA ──► manager-api (Go / Gin)                             │
│                              │                                                 │
│                              ├── PostgreSQL（业务库 / job / 审计）             │
│                              ├── Redis（队列 / 短期状态 / 锁）                 │
│                              ├── new-api（model gateway / 计费）               │
│                              └── ollama（本地模型，可选）                      │
└────────────────────────────────────────┬───────────────────────────────────────┘
                                         │ HTTPS + Bearer token
                       ┌─────────────────┴─────────────────┐
                       ▼                                   ▼
              ┌─────────────────┐                 ┌─────────────────┐
              │ Runtime Node A  │                 │ Runtime Node B  │
              │  oc-runtime-    │                 │  oc-runtime-    │
              │   agent         │   …………………       │   agent         │
              │   ├─ docker     │                 │   ├─ docker     │
              │   │  proxy 7001 │                 │   │  proxy 7001 │
              │   └─ file API   │                 │   └─ file API   │
              │      7002       │                 │      7002       │
              │  Hermes 容器 ×N │                 │  Hermes 容器 ×N │
              └─────────────────┘                 └─────────────────┘
```

详见 [docs/architecture.md](./docs/architecture.md)。

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.25 / Gin / pgx / sqlc / golang-migrate / go-redis / Docker Go SDK |
| 前端 | Vue 3 / Vite 7 / Pinia / vue-router / TanStack Query / Naive UI |
| 数据 | PostgreSQL 17 / Redis 7 |
| Runtime Agent | Go 1.25 / 自签 TLS + Bearer token / Docker socket 代理 + 文件 HTTP API |
| 部署 | Docker Compose / nginx 反代 |

## 仓库结构

```text
oc-manager/
├── cmd/                  manager binary 入口
│   ├── server/             HTTP server（manager-api）
│   ├── migrate/            数据库迁移 CLI
│   ├── seed-admin/         平台管理员种子
│   └── seed-e2e/           Playwright e2e fixture 注入
├── internal/             manager 后端业务代码
│   ├── api/                handlers / middleware / router
│   ├── service/            业务服务层（auth / member / app / runtime / knowledge / …）
│   ├── domain/             状态机与枚举
│   ├── store/              sqlc 生成的 repository
│   ├── runtime/imagesync/  agent runtime 镜像同步到节点
│   ├── scheduler/          job 队列调度器
│   ├── worker/             job 执行器（onboarding / knowledge sync / …）
│   ├── integrations/       new-api / agent client
│   ├── auth/               JWT / CSRF / 密钥
│   ├── files/              路径沙箱
│   ├── log/                日志封装
│   ├── redis/              Redis 客户端封装
│   └── migrations/         golang-migrate up/down SQL
├── runtime/
│   ├── agent/              oc-runtime-agent binary（部署到每个 Runtime Node）
│   └── hermes/             Hermes 容器镜像构建上下文
├── web/                  Vue 3 管理后台 SPA
│   ├── src/
│   │   ├── pages/            apps / org / platform / runtime-nodes / knowledge / …
│   │   ├── api/              HTTP client + endpoints
│   │   ├── stores/           Pinia store
│   │   ├── components/       通用组件
│   │   ├── layouts/          Auth / Dashboard 布局
│   │   └── domain/           枚举与状态机
│   └── tests/                Playwright e2e
├── config/               YAML 配置（example 入仓，真实文件 .gitignore 屏蔽）
├── deploy/               生产 docker-compose 与运维文档
├── docs/                 设计 / 架构 / 用户手册
├── openapi/              OpenAPI 3.0 API 契约
├── scripts/              本地与运维辅助脚本
├── docker-compose.yml    本地 dev compose（manager + agent + 依赖一并启）
└── Makefile              常用 dev / test / build 目标
```

## 快速开始（本地）

需要：Linux + Docker ≥ 24，`make`，至少 8 GB 空闲内存。

```bash
# 1. 复制并编辑配置
cp .env.example .env
cp config/manager.example.yaml config/manager.yaml
cp config/agent.example.yaml   config/agent.yaml
${EDITOR:-vi} config/manager.yaml   # 至少改：database.url / redis.password / auth.* / security.master_key
${EDITOR:-vi} config/agent.yaml     # 至少改：agent.token / manager.* 暂留空

# 2. 构建 Hermes runtime 镜像（首次必做）
make build-hermes-runtime

# 3. 拉起依赖 + manager + agent
make dev-up

# 4. 跑数据库迁移
make migrate-up

# 5. 注入平台管理员种子
docker compose run --rm manager-api go run ./cmd/seed-admin

# 6. 浏览器访问
open http://localhost:5173
```

`security.master_key` 生成示例：

```bash
openssl rand -base64 32
```

完整 Make 目标速查与常见问题排查见 [docs/local-development.md](./docs/local-development.md)。

## 构建生产镜像

线上部署所需的四个镜像各自有独立 Dockerfile：

| 镜像 | Dockerfile | 构建上下文 | 引用位置 | 说明 |
|---|---|---|---|---|
| `manager-api` | [`cmd/server/Dockerfile`](./cmd/server/Dockerfile) | 仓库根目录 | `deploy/manage/.env` 的 `OCM_MANAGER_IMAGE` | 多阶段 Go 构建，内置 `oc-manager`、`migrate`、`seed-admin` 三个二进制 |
| `manager-web` | [`web/Dockerfile`](./web/Dockerfile) | `web/` | `deploy/manage/.env` 的 `OCM_WEB_IMAGE` | Node 22 构建 vite 产物 + nginx:alpine 提供静态资源；外层 manager-nginx 完成 TLS 与 `/api` 反代 |
| `oc-runtime-agent` | [`runtime/agent/Dockerfile`](./runtime/agent/Dockerfile) | 仓库根目录 | `deploy/runtime-agent/.env` 的 `OC_RUNTIME_AGENT_IMAGE` | 多阶段 Go 构建，最终镜像仅含静态二进制 + ca-certificates + tzdata |
| `hermes-runtime` | [`runtime/hermes/hermes-v2026.5.16/Dockerfile`](./runtime/hermes/hermes-v2026.5.16/Dockerfile) | `runtime/hermes/hermes-v2026.5.16/` | manager 通过 imagesync 同步到各 Runtime Node | 应用容器运行时镜像，按版本化 variant 独立构建 |

### 构建命令

将 `<registry>/<tag>` 替换为实际镜像仓库与标签：

```bash
# manager-api（构建上下文必须为仓库根目录，Dockerfile 通过 -f 指定）
docker build -f cmd/server/Dockerfile      -t <registry>/oc-manager:<tag>       .

# manager-web（构建上下文为 web/ 子目录）
docker build -f web/Dockerfile             -t <registry>/oc-manager-web:<tag>   web

# oc-runtime-agent（构建上下文必须为仓库根目录）
docker build -f runtime/agent/Dockerfile   -t <registry>/oc-runtime-agent:<tag> .

# hermes-runtime（推荐走 Makefile，自动读取 version.txt 并传入 HERMES_REF）
make build-hermes-image

# 如需直接 docker build，构建上下文必须为版本化 variant，并显式传 HERMES_REF。
docker build --build-arg HERMES_REF=v2026.5.16 -t <registry>/hermes-runtime:<tag> runtime/hermes/hermes-v2026.5.16
```

四个 Dockerfile 都已经默认走国内源，本地或 CI 环境无需额外配置：

- 公网基础镜像（`golang` / `alpine` / `node` / `nginx` / `python`）通过 `ARG DOCKER_HUB_MIRROR=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public` 走 aliyun 私有 ACR 内已预先镜像同步好的副本
- Go 模块默认 `GOPROXY=https://goproxy.cn,direct` + `GOSUMDB=off`（与 dev 容器一致）
- npm 默认 `NPM_CONFIG_REGISTRY=https://registry.npmmirror.com`
- `hermes-runtime` 的 Debian apt 源默认通过 `ARG DEBIAN_MIRROR_HOST=mirrors.aliyun.com` 替换 `deb.debian.org` / `security.debian.org`

注：`hermes-runtime` 镜像内 `install.sh` 仍会去 hermes-agent.nousresearch.com / GitHub 拉源码与 Node tarball，那部分走第三方 CDN，无法整体国内化。

公网基础镜像的同步清单（推送到 aliyun ywjs_public 命名空间，所有 Docker Hub 原 namespace 折平）：

| 原始公网镜像 | aliyun ywjs_public 路径 | 使用方 |
|---|---|---|
| `docker.io/library/golang:1.25-alpine3.22` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/golang:1.25-alpine3.22` | cmd/server、runtime/agent Dockerfile builder |
| `docker.io/library/alpine:3.22` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/alpine:3.22` | cmd/server、runtime/agent Dockerfile final |
| `docker.io/library/node:22-alpine` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/node:22-alpine` | web Dockerfile builder |
| `docker.io/library/nginx:1.27-alpine` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/nginx:1.27-alpine` | web Dockerfile final + manager-nginx |
| `docker.io/library/python:3.13-slim-bookworm` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/python:3.13-slim-bookworm` | runtime/hermes Dockerfile |
| `docker.io/library/postgres:17-alpine` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/postgres:17-alpine` | manager-postgres + new-api-postgres |
| `docker.io/library/redis:7` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/redis:7` | manager-redis + new-api-redis |
| `docker.io/calciumion/new-api:<tag>` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/new-api:<tag>` | new-api 服务 |
| `docker.io/ollama/ollama:<tag>` | `crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/ollama:<tag>` | ollama 服务 |

需要切回官方源时，对应 build-arg 都可覆盖：

```bash
docker build \
  --build-arg DOCKER_HUB_MIRROR=docker.io/library \
  --build-arg GOPROXY=https://proxy.golang.org,direct \
  -f cmd/server/Dockerfile -t <registry>/oc-manager:<tag> .

docker build \
  --build-arg DOCKER_HUB_MIRROR=docker.io/library \
  --build-arg NPM_REGISTRY=https://registry.npmjs.org \
  -f web/Dockerfile -t <registry>/oc-manager-web:<tag> web
```

注意：`calciumion/new-api` 与 `ollama/ollama` 不在 Docker Hub `library/` 下，切回官方源时需要手工指定完整路径（`docker.io/calciumion/new-api:<tag>` 等），`DOCKER_HUB_MIRROR=docker.io/library` 的覆盖只适用于 `library/` 命名空间的基础镜像。

推送到镜像仓库后，写入对应运行包 `.env`：把 4 个私有镜像的 `:CHANGE_ME_TAG` 替换成具体版本 tag（如 `:v1.0.0`），更严格的环境可进一步固定到 `@sha256:` digest。**生产禁止使用 `:latest`、分支 tag 或版本族 tag**。

- `deploy/manage/.env` → `OCM_MANAGER_IMAGE`、`OCM_WEB_IMAGE`、`MANAGER_POSTGRES_IMAGE`、`MANAGER_REDIS_IMAGE`、`MANAGER_NGINX_IMAGE`
- `deploy/manage/config/manager.yaml` → `hermes.runtime_image`（manager 把该镜像推送到 agent 节点）
- `deploy/runtime-agent/.env` → `OC_RUNTIME_AGENT_IMAGE`

线上私有镜像仓库为 aliyun ACR：

```
crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-api
crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-web
crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-agent
crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes
```

各字段含义见 [deploy/README.md](./deploy/README.md) 与子运行包 README。

### 生产首次启动的两个特殊命令

线上镜像不再包含 Go 工具链，原本 `go run ./cmd/...` 的两步直接走二进制。`manager-api` 镜像同时打包了 `oc-manager`（默认启动 / CMD 入口）、`migrate`、`seed-admin` 三个二进制，三者都从 `OCM_CONFIG=/etc/manager/config.yaml` 读取主配置（由 `deploy/manage/docker-compose.yml` 通过挂载与 `environment` 提供）：

```bash
# 1. 数据库迁移
docker compose run --rm manager-api migrate up

# 2. 注入初始平台管理员（仅首次部署执行；用户名与密码为命令参数，display_name 可选）
docker compose run --rm manager-api seed-admin <username> <password> [display_name]
```

`docker compose run` 会继承服务定义的 `environment`、`volumes`、`depends_on`，因此命令能正常读到挂载进容器的 `/etc/manager/config.yaml`，并在 PostgreSQL / Redis 健康后再开始执行。

镜像里之所以用 `CMD` 而非 `ENTRYPOINT`，正是为了让 `docker compose run --rm manager-api migrate up` 之类的调用能直接覆盖入口去跑另一个二进制，而不会被拼成 `oc-manager migrate up`。

`oc-runtime-agent` 镜像内则包含 `oc-runtime-agent healthcheck` 子命令，作为 `deploy/runtime-agent/docker-compose.yml` 的 compose healthcheck 入口。

## 文档导航

### 开发与设计

- [架构总览](./docs/architecture.md) — 模块图、拓扑、关键数据流；新协作者从这里读起
- [本地开发指南](./docs/local-development.md) — Make 目标、调试账号、常见问题
- [产品设计](./docs/product-design.md) — 角色、对象模型、业务流程、权限
- [技术设计](./docs/technical-design.md) — 后端模块、状态机、接口契约、job
- [Hermes 容器运行机制](./docs/hermes-container.md) — 创建链路、挂载、注入、知识库
- [runtime-agent 工作原理](./docs/runtime-agent.md) — 注册、心跳、探测
- [配置参考](./docs/configuration.md) — manager.yaml / agent.yaml / .env
- [用户手册](./docs/user-manual.md) — 三类角色操作
- [API 契约](./openapi/openapi.yaml) — OpenAPI 3.0
- [协作规范](./AGENTS.md) — 提交、注释、测试规范

### 部署与运维

- [部署总览](./deploy/README.md) — 四个运行包与部署顺序
- [运维手册](./deploy/operations.md) — 备份、恢复、升级、回滚、排查
- [manager 服务部署](./deploy/manage/README.md)
- [new-api 部署](./deploy/new-api/README.md)
- [ollama 部署](./deploy/ollama/README.md)
- [runtime-agent 部署](./deploy/runtime-agent/README.md)

## 端口约定（本地默认）

| 端口 | 服务 |
|---|---|
| 5173 | manager-web (Vite dev) |
| 8080 | manager-api |
| 15432 | manager-postgres |
| 6379 | manager-redis |
| 6380 | new-api-redis |
| 3000 | new-api |
| 11434 | ollama |
| 7001 | oc-runtime-agent docker proxy |
| 7002 | oc-runtime-agent file API |

可通过 `.env` 覆盖宿主机端口映射，详见 [.env.example](./.env.example)。

## 健康检查

- `GET /healthz` → 200 表示 manager-api 进程存活
- 节点状态：UI「运行节点」页或 `GET /api/v1/runtime-nodes`，`last_heartbeat_at` 持续刷新代表 agent 心跳正常

## 许可与反馈

内部项目，许可与发布策略由仓库所有者确定。Issue 与改进建议通过仓库 issue 区或代码评审通道反馈。
