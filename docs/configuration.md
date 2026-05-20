# 配置参考

> manager.yaml、agent.yaml 与 .env 全部字段的含义与默认值。部署或排查配置类问题时按章节定位字段。

## 1. config/manager.yaml

manager 进程（`cmd/server` / `cmd/migrate` / `cmd/seed-admin`）读取的配置文件。
进程启动时通过 `OCM_CONFIG` 环境变量指定路径，未设置时默认 `config/manager.yaml`（相对工作目录）。
该文件已加入 `.gitignore`，严禁将含真实密钥的文件提交到仓库；脱敏模板为 `config/manager.example.yaml`。

### 1.1 `app`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `env` | string | `dev` | 运行环境标签，取值约定 `dev` / `staging` / `prod`，仅用于日志和调试上下文识别，不参与逻辑分支 |
| `http_addr` | string | — | HTTP server 监听地址，形如 `:8080`（全部网卡）或 `127.0.0.1:8080`（仅本机）；必填，为空启动 fail-fast |
| `public_base_url` | string | — | 对外可访问的 base URL；用作 CORS 白名单唯一允许 origin 以及邮件回调中的绝对链接基准；留空时不开启 CORS |
| `data_root` | string | — | manager 进程持久化业务数据的根目录（应用工作目录归档等）；必填，为空启动 fail-fast |
| `knowledge_root` | string | — | 知识库主副本根目录（manager 端主拷贝，由 worker 同步到各 runtime 节点）；路径下结构为 `orgs/<org_id>/...`、`apps/<app_id>/...`；必填，为空启动 fail-fast |

### 1.2 `database`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `url` | string | — | PostgreSQL 连接 DSN，格式 `postgres://<user>:<pwd>@<host>:<port>/<db>?sslmode=disable`；含明文密码，只能写到 gitignored 的 `manager.yaml`；必填，为空启动 fail-fast |

### 1.3 `redis`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `addr` | string | — | Redis 服务地址，形如 `host:port`；必填，为空启动 fail-fast |
| `password` | string | `""` | Redis 认证密码；未启用 ACL/密码时填空串 |
| `db` | int | `0` | Redis 逻辑库编号，标准 Redis 取值范围 0–15 |
| `key_prefix` | string | `ocm:` | 所有 Redis key 的统一前缀，避免与共享 Redis 的 new-api 等服务键空间冲突；推荐带分隔符形式如 `ocm:` |

### 1.4 `auth`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `cookie_domain` | string | `localhost` | 登录/refresh cookie 的 Domain 属性；本地开发填 `localhost`，生产填前端可访问的域名 |
| `access_token_ttl` | duration | `15m` | access token 有效期，Go time.Duration 字符串，如 `15m`、`1h`；必须 > 0，为空或非法字符串启动 fail-fast |
| `refresh_token_ttl` | duration | `720h` | refresh token 有效期；约定值 `720h`（30 天）；必须 > 0，为空或非法字符串启动 fail-fast |
| `jwt_access_secret` | string | — | access JWT HMAC 签名密钥；建议高熵字符串长度 ≥ 32 字节；必填，为空启动 fail-fast；泄漏后必须立即轮换 |
| `jwt_refresh_secret` | string | — | refresh JWT HMAC 签名密钥；不能与 `jwt_access_secret` 复用；必填，为空启动 fail-fast |
| `csrf_secret` | string | — | CSRF double-submit cookie HMAC 密钥；必填，为空启动 fail-fast |

### 1.5 `security`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `master_key` | string | — | 敏感字段（agent token、第三方 API key 等）列加密根密钥；必须是 base64 编码的 32 字节随机数，对应 AES-256-GCM key 长度；解码后非 32 字节启动 fail-fast；生成示例见 §4 |

### 1.6 `newapi`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `base_url` | string | — | new-api 网关 base URL，含 scheme、不含尾斜杠，如 `http://new-api:3000`；留空表示不接入 new-api，相关接口会报错或降级，仅适合纯本地调试 |
| `admin_token` | string | — | new-api「个人设置 → 安全设置 → 系统访问令牌」生成的 access_token；不是「令牌」页的 sk- 形式 API token；`base_url` 非空时必填 |
| `model_relay_token` | string | `""` | 可选；new-api 令牌页创建的 sk- 形式 token，仅在 `/api/models` 不可用时降级查询 OpenAI 兼容 `/v1/models`；留空时不尝试 `/v1/models` |
| `admin_user_id` | int | `1` | admin_token 持有者在 new-api 中的用户 ID，对应 `New-Api-User` header 校验；`base_url` 非空时必填；默认账号 `admin/admin123!` 的 id 为 `1` |

### 1.7 `hermes`

Hermes Agent runtime 容器相关配置。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `runtime_image` | string | `hermes-runtime:v2026.5.16-dev` | Hermes 容器镜像引用（name:tag 或 digest）；tag 必须固定到具体 Hermes 版本，runtime 节点上必须存在该镜像，imagesync 用 `docker save / load` 分发 |
| `system_prompt_template` | string | — | 平台级系统人设模板（SOUL.md PlatformPrompt 层），支持任意中英文 prompt 内容 |
| `workspace.archive_retention_days` | int | `14` | agent 端归档目录保留天数；`0` 表示不清理，仅适合本地调试 |
| `llm.base_url` | string | — | Hermes 容器从 docker network 看到的 new-api OpenAI 兼容 endpoint，必须含 `/v1` 后缀；写入容器内 `config.yaml` 的 `model.base_url` |
| `llm.default_provider` | string | `openai` | 默认 provider 名称，写入容器内 `config.yaml` 的 `model.provider`；常用 `openai`（兼容 new-api OpenAI 兼容协议） |
| `llm.default_model` | string | `qwen2.5:0.5b` | 默认模型名，必须是 new-api 渠道里实际可路由的名字 |
| `container_networks` | []string | `["oc-manager_default"]` | Hermes 容器要连接的 docker network 列表；必须包含 new-api 所在 network，否则容器无法解析 `new-api` hostname；docker compose project 默认派生 `<project>_default`，本仓库为 `oc-manager_default` |

> 容器实际使用的 `OPENAI_API_KEY` 由 manager 替每个应用通过 new-api `POST /api/token/:id/key` 自动拉取后加密落库（`apps.newapi_key_ciphertext`），每个应用的 token 独立隔离，无需在此填写全局 sk-。

### 1.8 `agent`

manager 侧用于与 runtime-agent 协调的参数（非 runtime-agent 自身配置）。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `heartbeat_interval_seconds` | int | `30` | runtime-agent 注册成功后回写并按此频率上报心跳；manager 以 90 秒阈值判定节点离线（`NodeHealthReconciler`） |

### 1.9 `runtime`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `enrollment_secret` | string | — | runtime-agent 自动注册共享密钥；必须是 base64 编码的 32 字节随机数；manager 与所有 agent 必须一致；生成示例见 §4 |
| `probe.interval_seconds` | int | `60` | manager 主动探测 runtime-agent docker/file 两个端口的轮询间隔（秒） |
| `probe.timeout_seconds` | int | `3` | 单次 HTTP 探测超时秒数 |
| `probe.failure_threshold` | int | `3` | active 节点连续探测失败达到该次数后进入 `degraded` 状态 |
| `probe.recovery_threshold` | int | `2` | degraded 节点连续探测成功达到该次数后恢复 `active` 状态 |

---

## 2. config/agent.yaml

`oc-runtime-agent` 进程（部署到每个 runtime 节点）的配置文件。
进程启动时通过 `OC_AGENT_CONFIG` 环境变量指定路径，未设置时默认 `config/agent.yaml`（相对工作目录）。
该文件已加入 `.gitignore`，严禁将含真实密钥的文件提交到仓库；脱敏模板为 `config/agent.example.yaml`。

### 2.1 `agent`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `name` | string | —（自动生成） | 节点展示名；为空时 manager 使用 `agent_id` 前 8 位生成默认名 |
| `advertise_host` | string | 本机 hostname | agent 上报给 manager 的对外可达主机名或 IP；生产建议显式配置为 manager 能访问的地址 |
| `max_apps` | int\|null | null（不限） | 节点最大可承载应用数；`null` 或留空表示不限制，`0` 表示暂停接收新应用。该值在 agent 启动 enroll 时同步到 manager 端，修改后重启 agent 生效 |
| `data_root` | string | — | 业务数据根目录；下挂 `orgs/<org_id>/...`、`apps/<app_id>/...`；容器部署一般指向 docker volume 挂载点；必填，为空启动 fail-fast |
| `state_dir` | string | — | agent 自身状态目录，存放自签 TLS 证书、注册结果等元数据；推荐放在 `data_root` 子目录便于卷挂载持久化；必填，为空启动 fail-fast |
| `docker_socket` | string | `/var/run/docker.sock` | 宿主机 Docker daemon socket 路径；agent 通过它做镜像 inspect / load 以及 docker proxy 反向代理 |
| `trusted_cidr` | string | `""` | 允许调用 agent 接口的客户端 CIDR，如 `10.0.0.0/8`；留空表示不做 IP 限制（仅本地 dev 场景）；与 token 双重校验，CIDR 失败直接 403 |
| `docker_addr` | string | — | docker proxy HTTP server 监听地址，如 `:7001`；manager 通过该端口走 TLS + Bearer 调 docker API；必填，为空启动 fail-fast |
| `file_addr` | string | — | 文件/scope API HTTP server 监听地址，如 `:7002`；处理 `/v1/scopes/*`、`/v1/files/ping`、`/healthz` 等路由；必填，为空启动 fail-fast |

### 2.2 `manager`

agent 启动时使用 `enrollment_secret` 主动注册，注册成功后 `node_id` 与 `agent_token` 自动写入 `state_dir`，无需管理员后台手动创建节点。

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `endpoint` | string | — | manager API 地址，形如 `https://manager.example/api/v1` |
| `enrollment_secret` | string | — | 与 `manager.yaml` 的 `runtime.enrollment_secret` 完全一致；必填 |
| `ca_bundle` | string | `""` | manager 自签 TLS CA 的 PEM 全文；空则信任系统根证书 |
| `skip_verify` | bool | `false` | 仅本地调试用；生产必须为 `false`，否则跳过 manager TLS 证书校验 |

### 2.3 `heartbeat`

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `interval_seconds` | int | `30` | 心跳上报间隔秒数；最小值 `5`，小于该值启动 fail-fast |
| `failure_log_threshold` | int | `5` | 连续心跳失败达到该次数时打 ERROR 日志，便于 ops 抓告警 |

---

## 3. .env / 端口映射

`.env` 文件仅用于 docker-compose 端口映射，不承载应用配置。修改端口后需重新 `docker compose up -d` 使端口变更生效。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MANAGER_POSTGRES_PORT` | `15432` | manager PostgreSQL 暴露到宿主机的端口，方便数据库工具连接 |
| `MANAGER_REDIS_PORT` | `6379` | manager Redis 暴露到宿主机的端口 |
| `NEWAPI_REDIS_PORT` | `6380` | new-api Redis 暴露到宿主机的端口，与 manager Redis 端口隔离 |
| `NEWAPI_PORT` | `3000` | new-api HTTP 服务端口 |
| `OLLAMA_PORT` | `11434` | Ollama HTTP 服务端口，用于 new-api 配置模型渠道 |
| `MANAGER_API_PORT` | `8080` | manager API HTTP 端口 |
| `MANAGER_WEB_PORT` | `5173` | manager Web Vite dev server 端口 |
| `RUNTIME_AGENT_GRPC_PORT` | `7001` | runtime-agent docker proxy 控制面端口 |
| `RUNTIME_AGENT_HTTP_PORT` | `7002` | runtime-agent 文件/scope API 以及健康检查端口 |

---

## 4. 密钥生成与轮换

### 生成

`security.master_key` 与 `runtime.enrollment_secret` 均要求 base64 编码的 32 字节随机数，使用以下命令生成：

```bash
openssl rand -base64 32
```

- `security.master_key`：填入 `manager.yaml` 的 `security.master_key`
- `runtime.enrollment_secret`：同一个值同时填入 `manager.yaml` 的 `runtime.enrollment_secret` 和所有 agent 的 `manager.enrollment_secret`

### 轮换流程

`security.master_key` 轮换会导致已加密的 agent token、第三方 API key 等密文全部失效，必须配合密钥版本迁移流程，**不支持平滑轮换**，轮换前请确认影响范围。

`runtime.enrollment_secret` 轮换步骤：

1. 生成新密钥：`openssl rand -base64 32`
2. 同步修改 `manager.yaml` 的 `runtime.enrollment_secret` 与全部 agent 的 `manager.enrollment_secret`
3. 重启 manager：`docker compose restart manager-api`
4. 逐节点重启 agent，重启后 agent 会用新密钥重新 enroll 并写入新的 `agent_token` 到 `state_dir`
