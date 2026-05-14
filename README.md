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
