# 配置参考

`manager-api` 与 `oc-runtime-agent` 的应用配置全部集中在两份 YAML 文件中；
环境变量仅保留 docker-compose 端口映射与文件路径开关。

## 1. 文件与环境变量约定

| 文件 | 进程 | 环境变量 | 默认值 |
|---|---|---|---|
| `config/manager.yaml` | `cmd/server` / `cmd/migrate` / `cmd/seed-admin` / `cmd/seed-e2e` | `OCM_CONFIG` | `config/manager.yaml` |
| `config/agent.yaml`   | `runtime/agent`（`oc-runtime-agent` binary） | `OC_AGENT_CONFIG` | `config/agent.yaml` |
| `config/manager.example.yaml` | 入仓脱敏模板，每字段附中文注释 | — | — |
| `config/agent.example.yaml`   | 入仓脱敏模板 | — | — |
| `.env`                | 仅 docker-compose 端口映射 | — | — |

**两条铁律**：

1. `config/manager.yaml` 与 `config/agent.yaml` 已加入 `.gitignore`，**严禁提交**。
2. 启动期对所有「必填字段」做严格校验，缺失或非法立即 `fail-fast`。模板留空的
   字段必须在真实文件里改为有效值。

`.env` 升级后只承载端口映射等 compose 参数：

```env
MANAGER_POSTGRES_PORT=15432
MANAGER_REDIS_PORT=6379
NEWAPI_REDIS_PORT=6380
NEWAPI_PORT=3000
OLLAMA_PORT=11434
MANAGER_API_PORT=8080
MANAGER_WEB_PORT=5173
RUNTIME_AGENT_GRPC_PORT=7001
RUNTIME_AGENT_HTTP_PORT=7002
```

## 2. `manager.yaml` 字段速查

### 2.1 `app.*`

| 字段 | 必填 | 含义 | 示例 |
|---|---|---|---|
| `env` | ✅ | 环境标签 `dev` / `staging` / `prod`，仅用于日志识别，不影响逻辑分支 | `dev` |
| `http_addr` | ✅ | HTTP server 监听地址 | `:8080` |
| `public_base_url` | ✅（生产） | 对外 base URL；CORS 白名单 + 邮件回调用；留空时不开 CORS（同源部署） | `http://localhost:8080` |
| `data_root` | ✅ | manager 业务数据根目录（应用工作目录归档等） | `./data/manager` |
| `knowledge_root` | ✅ | 知识库主副本根；下挂 `orgs/<org_id>/...` 与 `apps/<app_id>/...` | `/var/lib/oc-manager/knowledge` |

### 2.2 `database.*`

| 字段 | 必填 | 含义 |
|---|---|---|
| `url` | ✅ | PostgreSQL DSN，含明文密码；只能写到 gitignored 的 `manager.yaml` |

DSN 格式：`postgres://<user>:<pwd>@<host>:<port>/<db>?sslmode=disable`

### 2.3 `redis.*`

| 字段 | 必填 | 含义 |
|---|---|---|
| `addr` | ✅ | `host:port` |
| `password` | — | 未启 ACL/密码时为空串 |
| `db` | — | DB 编号，默认 `0` |
| `key_prefix` | ✅ | 共享 Redis 时避免与 `new-api` 等服务键空间冲突，推荐 `ocm:` |

### 2.4 `auth.*`

| 字段 | 必填 | 含义 |
|---|---|---|
| `cookie_domain` | ✅ | login/refresh cookie 的 Domain；本地 `localhost`，生产填可被前端访问的域名 |
| `access_token_ttl`  | ✅ | Go duration，例如 `15m`、`1h` |
| `refresh_token_ttl` | ✅ | Go duration，建议 `720h`（30 天） |
| `jwt_access_secret`  | ✅ | access JWT 签名密钥；高熵 ≥ 32 字节；泄漏后必须立即轮换 |
| `jwt_refresh_secret` | ✅ | refresh JWT 签名密钥；不能与 access 复用 |
| `csrf_secret`        | ✅ | CSRF double-submit cookie HMAC 密钥 |

> 三个 secret 任意一个为空均启动 `fail-fast`。生成示例：`openssl rand -base64 32`。

### 2.5 `security.*`

| 字段 | 必填 | 含义 |
|---|---|---|
| `master_key` | ✅ | 列加密根密钥；必须是 base64 编码的 32 字节随机数（AES-256-GCM key）。**一旦生成不要轮换**：旧 agent_token 等密文将无法解密 |

生成：

```bash
openssl rand -base64 32
```

### 2.6 `newapi.*`

| 字段 | 必填 | 含义 |
|---|---|---|
| `base_url` | （留空 = 不接入 new-api） | 形如 `http://new-api:3000`；**留空仅适合纯本地无计费调试**，相关接口会报错或降级 |
| `admin_token` | base_url 非空时 ✅ | new-api「个人设置 → 安全设置 → 系统访问令牌」生成的 access_token；不是 `sk-` 形式 token |
| `admin_user_id` | base_url 非空时 ✅ | 整数；admin_token 持有者用户 ID（默认 `admin/admin123!` 是 `1`） |

### 2.7 `openclaw.*`

OpenClaw 容器构建相关：

| 字段 | 必填 | 含义 |
|---|---|---|
| `runtime_image` | ✅ | OpenClaw 容器镜像 tag；imagesync 会用 `docker save / load` 分发到节点 |
| `system_prompt_template` | ✅ | 容器系统人设模板。**必须包含三个占位符**：`{{workspace_dir}}` / `{{knowledge_org_dir}}` / `{{knowledge_app_dir}}`，缺任一启动 fail-fast |
| `workspace.archive_retention_days` | ✅ | agent 端归档保留天数；`0` 表示不清理（仅本地调试） |
| `llm.base_url` | — | OpenClaw 容器从 docker network 看到的 new-api OpenAI 兼容 endpoint，必须含 `/v1` 后缀；留空时容器内 pi-coding-agent 走默认路由，无法命中本地 ollama |
| `llm.default_provider` | — | 写入容器 `/root/.pi/agent/settings.json`，常用 `openai` |
| `llm.default_model` | — | 必须是 new-api 渠道里实际可路由的名字，例如 `qwen2.5:0.5b` |
> 业务 user 凭据由 manager 自动管理：组织创建时调 new-api 创业务 user，
> 加密落 `organizations.newapi_user_credentials_ciphertext`。应用初始化
> 时 manager 用密文里的 access_token 调 `POST /api/token/:id/key` 拿
> 完整 sk- 注入容器，每个应用的 token 独立隔离，不再有全局 sk- 配置项。
> （v1.0.2 GA 收尾改造，参考前序 spec §1）
| `container_networks` | ✅ | OpenClaw 容器要连接的 docker network 列表；必须包含 `new-api` 所在 network。docker-compose project 默认派生 `<project>_default`（本仓库 `oc-manager_default`） |

### 2.8 `agent.*`

manager 侧的 agent 协议参数（**不是** runtime-agent 自身配置）：

| 字段 | 必填 | 含义 |
|---|---|---|
| `heartbeat_interval_seconds` | ✅ | runtime-agent 注册成功后回写并按此频率上报心跳；manager 用 90s 阈值判定离线 |

## 3. `agent.yaml` 字段速查

`oc-runtime-agent` 自身配置，部署到每个 Runtime Node。

### 3.1 `agent.*`

| 字段 | 必填 | 含义 |
|---|---|---|
| `data_root` | ✅ | 业务数据根；下挂 `orgs/<org_id>/...` 与 `apps/<app_id>/...`。容器部署一般指向 docker volume 挂载点 |
| `state_dir` | ✅ | agent 自身状态目录，存自签 TLS 证书 / 注册结果。推荐 `data_root` 子目录 |
| `docker_socket` | ✅ | 宿主机 Docker daemon socket，通常 `/var/run/docker.sock` |
| `token` | ✅（生产） | agent ↔ manager 的 Bearer token；留空表示不做 Bearer 校验，**仅本地无授权 dev** |
| `trusted_cidr` | 推荐 | 允许调用 agent 的客户端 CIDR，例如 `10.0.0.0/8`；留空 = 不做 IP 限制 |
| `docker_addr` | ✅ | docker proxy HTTP server 监听地址，例如 `:7001` |
| `file_addr` | ✅ | 文件 / scope API HTTP server 监听地址，例如 `:7002` |

### 3.2 `manager.*`

节点首次 register 完成后由运维回填。三字段（`endpoint` / `node_id` / `agent_token`）
**要么同时为空，要么同时填齐**；只填部分会启动 fail-fast。

| 字段 | 含义 |
|---|---|
| `endpoint` | 形如 `https://manager.example/api/v1` |
| `node_id` | `POST /agent/register` 响应中的 `runtime_nodes.id`（UUID） |
| `agent_token` | `POST /agent/register` 响应中的长期 bearer token |
| `ca_bundle` | manager 自签 TLS CA PEM 全文；空则信任系统根证书 |
| `skip_verify` | 仅本地调试用；生产必须 `false`，否则会跳过 manager TLS 校验 |

### 3.3 `heartbeat.*`

| 字段 | 默认 | 含义 |
|---|---|---|
| `interval_seconds` | `30` | 心跳间隔；最小 `5`，小于会启动 fail-fast |
| `failure_log_threshold` | `5` | 连续失败到该次数时打 ERROR 日志，便于 ops 抓告警 |

## 4. 启动顺序速记

```bash
# 1. 复制脱敏模板
cp config/manager.example.yaml config/manager.yaml
cp config/agent.example.yaml   config/agent.yaml

# 2. 生成 master_key（一次性）
echo "security:" >> config/manager.yaml
echo "  master_key: \"$(openssl rand -base64 32)\"" >> config/manager.yaml
# 然后回到编辑器把这一行剪贴到 security: 下；不要遗留两段 security:

# 3. 改其他必填字段
${EDITOR:-vi} config/manager.yaml
${EDITOR:-vi} config/agent.yaml

# 4. 启动
docker compose up -d
```

## 5. 升级指引（从老版本 .env 模式迁移）

老版本通过 `.env` 注入应用配置；从 v1.0 GA 起这些 env 不再被读取，必须搬到
yaml：

| 旧 env | 新 yaml 字段 |
|---|---|
| `DATABASE_URL` | `database.url` |
| `REDIS_ADDR` | `redis.addr` |
| `REDIS_PASSWORD` | `redis.password` |
| `JWT_ACCESS_SECRET` | `auth.jwt_access_secret` |
| `JWT_REFRESH_SECRET` | `auth.jwt_refresh_secret` |
| `CSRF_SECRET` | `auth.csrf_secret` |
| `MASTER_KEY` | `security.master_key` |
| `NEWAPI_BASE_URL` | `newapi.base_url` |
| `NEWAPI_ADMIN_TOKEN` | `newapi.admin_token` |
| `NEWAPI_ADMIN_USER_ID` | `newapi.admin_user_id` |
| `OPENCLAW_LLM_BASE_URL` | `openclaw.llm.base_url` |
| `OPENCLAW_LLM_DEFAULT_PROVIDER` | `openclaw.llm.default_provider` |
| `OPENCLAW_LLM_DEFAULT_MODEL` | `openclaw.llm.default_model` |
| `OPENCLAW_LLM_OPENAI_API_KEY` | 已废弃，v1.0.2 起由 manager 按应用动态注入，无需配置 |
| `OCM_KNOWLEDGE_ROOT` | `app.knowledge_root` |

迁移完成后 `.env` 只剩 `*_PORT` 端口映射。

## 6. 启动期校验清单

启动失败时优先核对：

- [ ] `database.url` 能连通（`pg_isready`）
- [ ] `redis.addr` 能连通；密码正确
- [ ] `auth.jwt_access_secret` / `auth.jwt_refresh_secret` / `auth.csrf_secret` 都
      非空且高熵
- [ ] `auth.access_token_ttl` / `auth.refresh_token_ttl` 是合法 Go duration 字符串
- [ ] `security.master_key` 是 base64 编码 + 解码后正好 32 字节
- [ ] `openclaw.system_prompt_template` 含三个占位符 `{{workspace_dir}}` /
      `{{knowledge_org_dir}}` / `{{knowledge_app_dir}}`
- [ ] `openclaw.container_networks` 至少含 `new-api` 所在 network
- [ ] agent 端 `agent.docker_addr` / `file_addr` 不冲突，且对 manager 网络可达
- [ ] agent 端 `agent.token` 与 manager 注册节点时下发的 `agent_token` 完全一致

## 7. 安全要点

- `security.master_key` 与 `auth.*` secret 的轮换会导致旧密文 / 旧 token 全部失效，
  必须配合密钥版本迁移流程；第一版**不支持** master_key 平滑轮换。
- agent 与 manager 之间用自签 CA + bearer token 双向校验；不要把节点 7001/7002
  暴露到公网，应仅放行 manager 出口 CIDR。
- 任何含明文凭据的字段（`database.url` / `redis.password` / `auth.*` /
  `security.master_key` / `newapi.admin_token`
  / `agent.token` / `manager.agent_token`）只能写到 gitignored 的真实 yaml；
  例文件保持脱敏占位。
