# 本地开发指南

> 在 Linux + Docker 24+ 环境起本地 manager + agent + 依赖、跑测试、定位常见问题。新协作者按本文一遍走通即可进入正常开发。

## 1 前置条件

- Linux，Docker Engine ≥ 24（已启用 buildx）
- `make`
- 至少 8 GB 空闲内存（PostgreSQL × 2、Redis × 2、new-api、ollama、manager-api、manager-web 同时运行）

## 2 第一次起本地

按顺序执行以下命令，走通后本地环境即可用。

```bash
# 1. 复制环境变量配置，按需编辑端口等覆盖项
cp .env.example .env

# 2. 构建 Hermes runtime 镜像（仅第一次或 runtime/hermes 有改动时需要）
make build-hermes-runtime

# 3. 校验 docker-compose.yml 挂载配置正确
make check-compose

# 4. 启动所有服务
make dev-up

# 5. 执行数据库迁移
make migrate-up

# 6. 注入 e2e 种子数据（可选，用于 Playwright 测试或本地快速体验）
make seed-e2e
```

启动后各服务的默认端口：

| 服务 | 默认端口 | 说明 |
|---|---|---|
| manager-web | 5173 | 前端开发服务器 |
| manager-api | 8080 | 后端 API |
| manager-postgres | 15432 | manager 数据库（本机工具可直连） |
| manager-redis | 6379 | manager 缓存 |
| new-api | 3000 | OpenAI-compatible API 入口 |
| new-api-postgres | （无宿主机映射） | new-api 专用数据库 |
| new-api-redis | 6380 | new-api 专用 Redis |
| ollama | 11434 | 本地模型服务 |

> ollama 容器默认请求 NVIDIA GPU；无 GPU 环境可在 `docker-compose.yml` 中移除 `deploy.resources` 段。

## 3 调试账号

| 角色 | 组织标识 | 用户名 / 密码 |
|---|---|---|
| new-api 管理员 | — | `admin` / `admin123` |
| manager 平台管理员 | 留空 | `admin` / `admin123` |
| manager 测试组织管理员 | `test-org` | `test-org` / `test-org123` |
| manager 组织成员 | `test-org` | `test-org-user1` / `test-org-user1` |

> 测试组织本身的标识为 `test-org`，由 `make seed-e2e` 注入。平台管理员登录时组织标识留空即可。

## 4 常用 Make 目标速查

### 后端

| 目标 | 说明 |
|---|---|
| `make test` | 在 manager-api 容器内跑全部 Go 单元测试（`go test ./...`） |
| `make integration-test` | 跑集成测试（需要数据库和 Redis 就绪，通过环境变量指定连接地址） |
| `make vet` | 在容器内跑 `go vet ./...` 静态检查 |
| `make build` | 在容器内编译 server、migrate、oc-runtime-agent 三个二进制产物到 `./tmp/build/` |

### 前端

| 目标 | 说明 |
|---|---|
| `make web-test` | 在 manager-web 容器内跑 vitest 单元测试 |
| `make web-typecheck` | 在 manager-web 容器内跑 vue-tsc 类型检查 |
| `make web-build` | 在 manager-web 容器内执行 vite build |

### 代码生成

| 目标 | 说明 |
|---|---|
| `make openapi-gen` | 扫描后端 swag 注解，覆盖生成 `openapi/openapi.yaml` |
| `make web-types-gen` | 从 `openapi/openapi.yaml` 生成 `web/src/api/generated.ts` TypeScript 类型 |
| `make openapi-check` | 跑 `openapi-gen` 后校验 git 工作区是否干净，yaml 漂移则报错 |
| `make sqlc-generate` | 调用 sqlc 从 SQL 查询文件生成 Go 类型和方法 |

### 运行与日志

| 目标 | 说明 |
|---|---|
| `make dev-up` | 以 detach 模式启动全部 compose 服务 |
| `make dev-down` | 停止并销毁容器（bind mount 数据保留在 `./.local/data/`） |
| `make logs` | 跟踪全部服务日志，保留最近 200 行 |

### 数据库

| 目标 | 说明 |
|---|---|
| `make migrate-up` | 在容器内执行 `cmd/migrate up`，将数据库迁移到最新版本 |
| `make migrate-down` | 在容器内执行 `cmd/migrate down`，回滚最近一次迁移 |
| `make seed-e2e` | 在容器内运行 `cmd/seed-e2e`，TRUNCATE 业务表后重建 e2e fixture |

### Hermes runtime 与调试

| 目标 | 说明 |
|---|---|
| `make build-hermes-runtime` | 构建 `hermes-runtime:dev` 镜像（`./runtime/hermes`） |
| `make build-hermes-runtime` | 构建 Hermes runtime 镜像，并在 Dockerfile 构建期自动运行 runtime pytest 自检 |
| `make check-compose` | 校验 `docker-compose.yml` 所有挂载均为 bind mount，无 named volume |
| `make debug-ollama` | 检查 Ollama API 可达性、列出模型并发起最小调用 |
| `make debug-newapi` | 检查 new-api HTTP、数据库、Ollama 渠道连通性 |
| `make newapi-probe` | 运行 new-api 探针脚本做快速健康检查 |

## 5 常见问题排查

**compose 启动异常**

先跑 `make check-compose` 确认挂载配置无误，再用 `make logs` 或 `docker compose logs <service>` 查看具体服务日志。

**数据库连不上**

检查 `.env` 是否覆盖了 `MANAGER_POSTGRES_PORT`，确认端口与应用配置一致。用 `docker compose logs manager-postgres` 确认容器已通过 healthcheck。

**`make migrate-up` 报错**

通常是 `manager-postgres` 容器尚未就绪。等容器 healthcheck 通过后重试，或用 `docker compose ps` 确认状态为 `healthy`。

**前端类型检查或构建失败**

若报 `generated.ts` 相关错误，先跑 `make web-types-gen` 确认 TypeScript 类型与当前 `openapi/openapi.yaml` 同步。若 yaml 本身也过期，先跑 `make openapi-gen` 再跑 `make web-types-gen`。

**new-api turnstile 拦截 server-to-server 登录**

manager 创建组织时会对 new-api 发起 server-to-server 登录请求，若 new-api 开启了 turnstile 验证则会被拦截。通过 `curl http://localhost:3000/api/status | jq .data.turnstile_check` 确认；若返回 `true`，在 new-api 管理后台「系统设置 → 安全设置」关闭。

**Ollama 渠道不可用**

登录 new-api 管理后台，将 Ollama 渠道地址指向 `http://ollama:11434`（容器内网地址，不是宿主机地址）。
