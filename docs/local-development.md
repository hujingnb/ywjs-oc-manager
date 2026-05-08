# OpenClaw Manager 本地开发指南

本仓库的本地调试通过 Docker Compose 全程托管，宿主机不直接运行 Go 或 Vite。
本文档汇总最常用的命令、目录结构和验收清单。

## 先决条件

- Docker Engine 24 及以上，已启用 buildx；
- 至少 8GB 空闲内存（OpenClaw runtime 镜像、PostgreSQL、Redis、Ollama 同时启动）；
- 仓库根目录的 `.env.example` 复制为 `.env` 后按需编辑；
- 第一次启动前请执行 `make build-openclaw-runtime` 以产出 OpenClaw runtime 镜像。

## 关键 Make 目标

| 目标 | 作用 |
|---|---|
| `make check-compose` | 校验 `docker-compose.yml` 没有顶层 named volume，所有挂载都是 bind mount。 |
| `make dev-up` | 启动全部服务（manager-api / manager-web / manager-postgres / manager-redis / new-api / new-api-postgres / new-api-redis / ollama / oc-runtime-agent）。 |
| `make dev-down` | 停止并清理容器，本地 bind mount 数据保留在 `./data`。 |
| `make migrate-up` / `make migrate-down` | 在容器内执行迁移。 |
| `make build-openclaw-runtime` | 构建 OpenClaw runtime 镜像。 |
| `make verify-openclaw-runtime` | 验证 OpenClaw 与微信插件已正确安装。 |
| `make sync-openclaw-runtime-image` | 把镜像分发到 runtime node。 |
| `make debug-ollama` | 检查 Ollama API、列出模型并发起一次最小调用。 |
| `make debug-newapi` | 检查 new-api HTTP/数据库/Ollama 渠道。 |
| `make test` / `make vet` / `make build` | 后端测试、静态检查与产物构建。 |
| `make web-test` / `make web-typecheck` / `make web-build` | 前端 vitest、vue-tsc 与 vite build。 |

## 数据目录

`./data` 下按服务划分子目录，全部为 bind mount，避免 Docker named volume：

```
./.local/data/manager-postgres
./.local/data/manager-redis
./.local/data/new-api/postgres
./.local/data/new-api/redis
./.local/data/new-api/data
./.local/data/new-api/logs
./.local/data/ollama
./.local/data/agent
./.local/data/manager-knowledge  KnowledgeMaster 主副本
```

清理本地状态时直接删除对应子目录即可，但请先确认没有未导出的工作内容。

## 推荐验收路径

1. `make check-compose && make dev-up`；
2. `make migrate-up`；
3. `make debug-ollama` 与 `make debug-newapi`；
4. 浏览器访问 `http://localhost:5173/`，使用平台管理员账号登录；
5. 创建一个组织，进入组织后通过 “创建并初始化” 单页表单创建成员；
6. 切换到 “应用” 页查看初始化任务和状态；
7. 切换到 “运行节点” 页 rotate bootstrap，按提示在 oc-runtime-agent 内配置；
8. 知识库页上传一份文档，并在应用工作目录页确认面包屑和归档下载可用。

## 常见问题排查

- **`web-build` 在宿主机失败** ：因为 `web/dist` 是容器内 root 写入的，宿主机缺权限。请通过 `docker exec manager-web sh -c 'rm -rf /app/web/dist && npm run build'` 在容器内构建。
- **chrome-devtools MCP 启动失败** ：宿主机已有其他 Chrome 实例占用 profile，请先关闭再重启 MCP；本仓库 9.1 阶段的浏览器验收依赖该 MCP。
- **`make migrate-up` 报错** ：确认 `manager-postgres` 容器已就绪，可通过 `docker compose logs manager-postgres` 查看。
- **新 API 渠道 Ollama 不可用** ：登录 new-api 管理后台，将 Ollama 渠道指向 `http://ollama:11434`。
- **new-api `turnstile_check` 前置要求** ：manager 创建组织时会调 `POST /api/user/login` 拿
  session cookie，turnstile 开启会拦截 server-to-server 登录。本地 docker compose 默认
  `false`，可通过 `curl http://localhost:3000/api/status | jq .data.turnstile_check` 验证；
  如发现 `true`，在 new-api 后台 系统设置 → 安全设置 关闭。
