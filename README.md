# Agent Runtime Manager

面向组织的 Hermes Agent 应用管理后台。负责组织 / 成员 / 应用 / Runtime Node 编排，
对接 [`new-api`](https://github.com/Calcium-Ion/new-api) 网关计费，集中管控运行在
多个 Runtime Node 上的 Hermes 容器。

> 第一版定位为「单 manager + 多 Runtime Node」管理后台。manager 不直接读节点
> Docker socket 或文件系统，所有节点级操作经由部署在节点上的 `oc-runtime-agent`
> 完成。

## 核心能力

- 平台 / 组织 / 成员 三类角色的账号体系与登录。
- 组织生命周期管理 + Token Credit 充值（实际计费由 `new-api` 完成）。
- 创建成员账号时同步创建该成员名下唯一的 Hermes 应用，自动分配可用 Runtime Node。
- Runtime Node 注册、心跳、健康自愈（unreachable → active），bootstrap token 一次性发放、
  agent token 长期通信凭证。
- 每个应用对应一个 Docker 容器（经 agent 启停）、一个 `new-api api_key`、
  最多一个渠道绑定（第一版仅微信扫码）。
- 组织级与应用级双层知识库：manager 主副本 + agent 节点同步，同步状态可视。
- 应用工作目录浏览、单文件下载、文件夹打包下载（经 agent 文件 API 沙箱代理）。
- token 用量直查 `new-api`，按平台 / 组织 / 应用三级展示，不缓存。
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
              │  Hermes 容器 ×N                   │  Hermes 容器 ×N  │
              └─────────────────┘                 └─────────────────┘
```

详见 [`docs/architecture.md`](./docs/architecture.md)。

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
├── docs/                 设计 / 架构 / 用户手册 / 验证报告
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
make check-compose
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

更多本地开发命令、Make 目标速查、常见问题排查见 [`docs/local-development.md`](./docs/local-development.md)。

## 文档导航

### 入门 / 概览

- [架构概览](./docs/architecture.md) — 模块图、拓扑、关键数据流；新协作者从这里读起
- [本地开发指南](./docs/local-development.md) — Make 目标、目录结构、验收清单、常见问题

### 使用手册

- [用户手册](./docs/user-manual.md) — 平台管理员 / 组织管理员 / 组织成员 三类角色的功能与操作

### 设计 / 实现

- [产品需求与设计](./docs/openclaw-manager-design.md) — PRD：角色 / 对象模型 / 业务流程
- [技术实现设计](./docs/openclaw-manager-technical-design.md) — 后端模块 / 状态机 / job / 接口契约
- [验证报告](./docs/verification-report.md) — v1.0 GA / v1.0.1 实测时序与验收
- [API 契约](./openapi/openapi.yaml) — OpenAPI 3.0

### 部署 / 运维

- [部署指南](./deploy/README.md) — 生产 docker-compose、TLS、节点注册、双 agent 演练
- [配置参考](./docs/configuration.md) — `manager.yaml` / `agent.yaml` / `.env` 字段速查
- [备份与恢复](./deploy/backup.md) — 数据范围、PostgreSQL dump、知识库主副本备份、灾难演练
- [升级与回滚](./deploy/upgrade.md) — SemVer 约定、迁移、滚动替换、紧急回滚

### 协作规范

- [AGENTS.md](./AGENTS.md) — 提交信息、代码注释、单元测试与交付前检查规范

## 开发流程速查

```bash
make test                      # 后端单测
make integration-test          # 后端集成测试（INTEGRATION_DATABASE_URL/REDIS 必须可用）
make vet                       # go vet
make build                     # 编译 server / migrate / oc-runtime-agent

make web-test                  # 前端 vitest
make web-typecheck             # vue-tsc --noEmit
make web-build                 # vite build

make sqlc-generate             # 重新生成 sqlc 代码

make logs                      # docker compose logs -f --tail=200

make seed-e2e                  # Playwright e2e fixture（OCM_E2E=1 守门）
```

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

可通过 `.env` 覆盖宿主机端口映射，详见 [`.env.example`](./.env.example)。

## 健康检查

- `GET /healthz` → 200 表示 manager-api 进程存活
- 节点状态：UI 「运行节点」页或 `GET /api/v1/runtime-nodes`，`last_heartbeat_at` 持续刷新代表 agent 心跳正常

## 许可与反馈

内部项目，许可与发布策略由仓库所有者确定。Issue 与改进建议通过仓库 issue 区或代码评审通道反馈。
