# 生产 Compose 拆分 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `deploy/` 下生产 Docker Compose 拆成 new-api、ollama、runtime-agent、manage 四个可独立拷贝运行的部署包。

**Architecture:** 本地开发继续使用根目录 `docker-compose.yml`，生产部署只使用 `deploy/<service>/docker-compose.yml`。每个生产目录自带 `.env.example`、README、配置模板和本地 bind mount 持久化目录，compose 只引用预构建镜像，不依赖源码 build。

**Tech Stack:** Docker Compose、PostgreSQL 17、Redis 7、nginx 1.27、new-api、Ollama、Go manager/agent 预构建镜像。

---

## File Structure

- Modify: `.gitignore`
  - 忽略各生产目录的真实 `.env`、真实 YAML、数据和日志，仅允许 `.example` 与 `.gitkeep` 入仓。
- Delete: `deploy/docker-compose.prod.yml`
  - 旧生产入口废弃，避免误用。
- Delete: `deploy/docker-compose.two-agent.yml`
  - 旧双 agent 本地演练入口废弃；本地调试仍保留根 `docker-compose.yml`。
- Delete: `deploy/nginx.conf`
  - nginx 配置移动到 `deploy/manage/nginx.conf`，成为 manage 独立运行包的一部分。
- Create: `deploy/new-api/docker-compose.yml`
  - new-api、new-api-postgres、new-api-redis 完整栈。
- Create: `deploy/new-api/.env.example`
  - new-api 镜像、端口、数据库和 Redis 密码模板。
- Create: `deploy/new-api/README.md`
  - 复制目录、生成 `.env`、启动、备份和对接 manager 的说明。
- Create: `deploy/new-api/data/postgres/.gitkeep`
- Create: `deploy/new-api/data/redis/.gitkeep`
- Create: `deploy/new-api/data/new-api/.gitkeep`
- Create: `deploy/new-api/logs/.gitkeep`
- Create: `deploy/ollama/docker-compose.yml`
  - Ollama 单服务生产栈。
- Create: `deploy/ollama/.env.example`
  - Ollama 镜像、端口、origin 配置模板。
- Create: `deploy/ollama/README.md`
  - GPU/无 GPU 调整、拉模型、new-api 渠道地址说明。
- Create: `deploy/ollama/data/ollama/.gitkeep`
- Create: `deploy/runtime-agent/docker-compose.yml`
  - runtime-agent 单节点生产栈。
- Create: `deploy/runtime-agent/.env.example`
  - agent 镜像、端口和配置路径模板。
- Create: `deploy/runtime-agent/README.md`
  - 每台 runtime node 的配置、启动、防火墙和注册检查说明。
- Create: `deploy/runtime-agent/config/agent.example.yaml`
  - 从根 `config/agent.example.yaml` 派生，默认使用跨服务器 manager endpoint 占位。
- Create: `deploy/runtime-agent/data/agent/.gitkeep`
- Create: `deploy/manage/docker-compose.yml`
  - manager-postgres、manager-redis、manager-api、manager-web、manager-nginx 完整栈。
- Create: `deploy/manage/.env.example`
  - manager/web 镜像、端口、数据库/Redis 密码模板。
- Create: `deploy/manage/README.md`
  - 配置、迁移、启动、TLS、备份、升级入口说明。
- Create: `deploy/manage/config/manager.example.yaml`
  - 从根 `config/manager.example.yaml` 派生，默认使用跨服务器 new-api URL 占位。
- Create: `deploy/manage/nginx.conf`
  - 由旧 `deploy/nginx.conf` 移入 manage 目录。
- Create: `deploy/manage/tls/.gitkeep`
- Create: `deploy/manage/data/postgres/.gitkeep`
- Create: `deploy/manage/data/redis/.gitkeep`
- Create: `deploy/manage/data/manager/.gitkeep`
- Create: `deploy/manage/data/knowledge/.gitkeep`
- Modify: `deploy/README.md`
  - 改为四个生产目录的总览，不再指向旧 compose。
- Modify: `deploy/backup.md`
  - 更新 PostgreSQL、Redis、knowledge、agent 数据路径和备份命令。
- Modify: `deploy/upgrade.md`
  - 更新镜像升级、迁移、agent 滚动升级、回滚命令。

## Task 1: Ignore Rules And Directory Skeleton

**Files:**
- Modify: `.gitignore`
- Delete: `deploy/docker-compose.prod.yml`
- Delete: `deploy/docker-compose.two-agent.yml`
- Delete: `deploy/nginx.conf`
- Create: all `.gitkeep` files listed in File Structure

- [ ] **Step 1: Update ignore rules**

Append this block to `.gitignore`:

```gitignore

# 生产部署目录：真实配置、数据和日志不入仓，只提交 example 与 .gitkeep。
deploy/**/.env
deploy/manage/config/manager.yaml
deploy/runtime-agent/config/agent.yaml
deploy/**/data/**
deploy/**/logs/**
!deploy/**/data/**/.gitkeep
!deploy/**/logs/**/.gitkeep
```

- [ ] **Step 2: Create directory skeleton**

Run:

```bash
mkdir -p \
  deploy/new-api/data/postgres \
  deploy/new-api/data/redis \
  deploy/new-api/data/new-api \
  deploy/new-api/logs \
  deploy/ollama/data/ollama \
  deploy/runtime-agent/config \
  deploy/runtime-agent/data/agent \
  deploy/manage/config \
  deploy/manage/tls \
  deploy/manage/data/postgres \
  deploy/manage/data/redis \
  deploy/manage/data/manager \
  deploy/manage/data/knowledge

touch \
  deploy/new-api/data/postgres/.gitkeep \
  deploy/new-api/data/redis/.gitkeep \
  deploy/new-api/data/new-api/.gitkeep \
  deploy/new-api/logs/.gitkeep \
  deploy/ollama/data/ollama/.gitkeep \
  deploy/runtime-agent/data/agent/.gitkeep \
  deploy/manage/tls/.gitkeep \
  deploy/manage/data/postgres/.gitkeep \
  deploy/manage/data/redis/.gitkeep \
  deploy/manage/data/manager/.gitkeep \
  deploy/manage/data/knowledge/.gitkeep
```

Expected: directories exist; `.gitkeep` files can be added despite ignored data directories.

- [ ] **Step 3: Remove old production entry files**

Run:

```bash
rm deploy/docker-compose.prod.yml deploy/docker-compose.two-agent.yml deploy/nginx.conf
```

Expected: `git status --short` shows the three deleted files and new deployment directory files.

- [ ] **Step 4: Commit skeleton**

Run:

```bash
git add .gitignore deploy
git add -f deploy/new-api/data/postgres/.gitkeep deploy/new-api/data/redis/.gitkeep deploy/new-api/data/new-api/.gitkeep deploy/new-api/logs/.gitkeep
git add -f deploy/ollama/data/ollama/.gitkeep
git add -f deploy/runtime-agent/data/agent/.gitkeep
git add -f deploy/manage/tls/.gitkeep deploy/manage/data/postgres/.gitkeep deploy/manage/data/redis/.gitkeep deploy/manage/data/manager/.gitkeep deploy/manage/data/knowledge/.gitkeep
git commit -m "chore(deploy): 初始化生产部署目录" -m "为 new-api、ollama、runtime-agent、manage 建立独立生产部署目录骨架，并废弃旧生产 compose 入口。"
```

Expected: commit succeeds.

## Task 2: New API Production Package

**Files:**
- Create: `deploy/new-api/docker-compose.yml`
- Create: `deploy/new-api/.env.example`
- Create: `deploy/new-api/README.md`

- [ ] **Step 1: Create `.env.example`**

Create `deploy/new-api/.env.example`:

```env
COMPOSE_PROJECT_NAME=oc-new-api

NEWAPI_IMAGE=calciumion/new-api:latest
NEWAPI_PORT=3000
NEWAPI_NODE_NAME=new-api-prod-1
NEWAPI_STREAMING_TIMEOUT=600

NEWAPI_POSTGRES_USER=root
NEWAPI_POSTGRES_PASSWORD=CHANGE_ME_NEWAPI_POSTGRES_PASSWORD
NEWAPI_POSTGRES_DB=new-api

NEWAPI_REDIS_PASSWORD=CHANGE_ME_NEWAPI_REDIS_PASSWORD

TZ=Asia/Shanghai
```

- [ ] **Step 2: Create compose file**

Create `deploy/new-api/docker-compose.yml`:

```yaml
services:
  new-api-postgres:
    image: postgres:17-alpine
    restart: always
    environment:
      POSTGRES_USER: ${NEWAPI_POSTGRES_USER}
      POSTGRES_PASSWORD: ${NEWAPI_POSTGRES_PASSWORD}
      POSTGRES_DB: ${NEWAPI_POSTGRES_DB}
      TZ: ${TZ:-Asia/Shanghai}
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${NEWAPI_POSTGRES_USER} -d ${NEWAPI_POSTGRES_DB}"]
      interval: 10s
      timeout: 5s
      retries: 20
    networks:
      - new-api-internal

  new-api-redis:
    image: redis:7
    restart: always
    command: ["redis-server", "--requirepass", "${NEWAPI_REDIS_PASSWORD}", "--appendonly", "yes"]
    volumes:
      - ./data/redis:/data
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a ${NEWAPI_REDIS_PASSWORD} ping | grep PONG"]
      interval: 10s
      timeout: 5s
      retries: 20
    networks:
      - new-api-internal

  new-api:
    image: ${NEWAPI_IMAGE}
    restart: always
    command: --log-dir /app/logs
    ports:
      - "${NEWAPI_PORT:-3000}:3000"
    environment:
      SQL_DSN: postgresql://${NEWAPI_POSTGRES_USER}:${NEWAPI_POSTGRES_PASSWORD}@new-api-postgres:5432/${NEWAPI_POSTGRES_DB}
      REDIS_CONN_STRING: redis://:${NEWAPI_REDIS_PASSWORD}@new-api-redis:6379
      TZ: ${TZ:-Asia/Shanghai}
      ERROR_LOG_ENABLED: "true"
      BATCH_UPDATE_ENABLED: "true"
      NODE_NAME: ${NEWAPI_NODE_NAME:-new-api-prod-1}
      STREAMING_TIMEOUT: ${NEWAPI_STREAMING_TIMEOUT:-600}
    depends_on:
      new-api-postgres:
        condition: service_healthy
      new-api-redis:
        condition: service_healthy
    volumes:
      - ./data/new-api:/data
      - ./logs:/app/logs
    networks:
      - new-api-internal
      - new-api-public

networks:
  new-api-internal:
    driver: bridge
  new-api-public:
    driver: bridge
```

- [ ] **Step 3: Create README**

Create `deploy/new-api/README.md`:

```markdown
# new-api 生产部署包

本目录可独立复制到 new-api 服务器运行，包含 new-api、PostgreSQL、Redis 和本地持久化目录。

## 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

首次启动后进入 new-api 后台完成管理员初始化、系统访问令牌生成、Ollama 渠道配置和模型测试。

## 对接 manager

manager 的 `config/manager.yaml` 中：

```yaml
newapi:
  base_url: "https://new-api.example.com"
openclaw:
  llm:
    base_url: "https://new-api.example.com/v1"
```

如果未配置 HTTPS 反代，可临时使用 `http://<new-api-host>:3000` 和
`http://<new-api-host>:3000/v1`。

## 数据

- PostgreSQL：`./data/postgres`
- Redis：`./data/redis`
- new-api 数据：`./data/new-api`
- 日志：`./logs`

备份前建议先确认 `docker compose ps` 中服务健康。
```

- [ ] **Step 4: Validate new-api compose**

Run:

```bash
docker compose --env-file deploy/new-api/.env.example -f deploy/new-api/docker-compose.yml config
./scripts/check-compose-bind-mounts.sh deploy/new-api/docker-compose.yml
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit new-api package**

Run:

```bash
git add deploy/new-api
git commit -m "chore(deploy): 增加 new-api 生产部署包" -m "为 new-api 增加独立 compose、环境变量模板和部署说明，包含私有 PostgreSQL、Redis、数据目录和日志目录。"
```

Expected: commit succeeds.

## Task 3: Ollama Production Package

**Files:**
- Create: `deploy/ollama/docker-compose.yml`
- Create: `deploy/ollama/.env.example`
- Create: `deploy/ollama/README.md`

- [ ] **Step 1: Create `.env.example`**

Create `deploy/ollama/.env.example`:

```env
COMPOSE_PROJECT_NAME=oc-ollama

OLLAMA_IMAGE=ollama/ollama:latest
OLLAMA_PORT=11434
OLLAMA_HOST=0.0.0.0:11434
OLLAMA_ORIGINS=*

TZ=Asia/Shanghai
```

- [ ] **Step 2: Create compose file**

Create `deploy/ollama/docker-compose.yml`:

```yaml
services:
  ollama:
    image: ${OLLAMA_IMAGE}
    restart: always
    ports:
      - "${OLLAMA_PORT:-11434}:11434"
    environment:
      OLLAMA_HOST: ${OLLAMA_HOST:-0.0.0.0:11434}
      OLLAMA_ORIGINS: ${OLLAMA_ORIGINS:-*}
      TZ: ${TZ:-Asia/Shanghai}
    volumes:
      - ./data/ollama:/root/.ollama
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
    healthcheck:
      test: ["CMD", "ollama", "list"]
      interval: 30s
      timeout: 10s
      retries: 5
```

- [ ] **Step 3: Create README**

Create `deploy/ollama/README.md`:

```markdown
# Ollama 生产部署包

本目录可独立复制到 Ollama 服务器运行。默认 compose 保留 NVIDIA GPU reservation。

## 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

## 无 GPU 环境

如果服务器没有 NVIDIA GPU，删除 `docker-compose.yml` 中的 `deploy.resources.reservations.devices`
段后再启动。

## 拉取模型

```bash
docker compose exec ollama ollama pull qwen2.5:0.5b
docker compose exec ollama ollama list
```

## 对接 new-api

new-api 后台渠道 base URL 填写：

```text
http://<ollama-host>:11434
```

生产防火墙建议只允许 new-api 服务器访问 `11434`。
```

- [ ] **Step 4: Validate ollama compose**

Run:

```bash
docker compose --env-file deploy/ollama/.env.example -f deploy/ollama/docker-compose.yml config
./scripts/check-compose-bind-mounts.sh deploy/ollama/docker-compose.yml
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit ollama package**

Run:

```bash
git add deploy/ollama
git commit -m "chore(deploy): 增加 ollama 生产部署包" -m "为 Ollama 增加独立 compose、环境变量模板和部署说明，保留模型数据目录与 GPU 配置说明。"
```

Expected: commit succeeds.

## Task 4: Runtime Agent Production Package

**Files:**
- Create: `deploy/runtime-agent/docker-compose.yml`
- Create: `deploy/runtime-agent/.env.example`
- Create: `deploy/runtime-agent/config/agent.example.yaml`
- Create: `deploy/runtime-agent/README.md`

- [ ] **Step 1: Create `.env.example`**

Create `deploy/runtime-agent/.env.example`:

```env
COMPOSE_PROJECT_NAME=oc-runtime-agent

OC_RUNTIME_AGENT_IMAGE=ghcr.io/your-org/oc-runtime-agent:1.0.0
RUNTIME_AGENT_GRPC_PORT=7001
RUNTIME_AGENT_HTTP_PORT=7002
OC_AGENT_CONFIG=/etc/oc-agent/agent.yaml

TZ=Asia/Shanghai
```

- [ ] **Step 2: Create agent config template**

Copy `config/agent.example.yaml` to `deploy/runtime-agent/config/agent.example.yaml`, then change these defaults:

```yaml
agent:
  name: "runtime-node-1"
  advertise_host: "runtime-node-1.example.com"
  max_apps: 3
  data_root: "/var/lib/oc-agent"
  state_dir: "/var/lib/oc-agent/state"
  docker_socket: "/var/run/docker.sock"
  trusted_cidr: "CHANGE_ME_MANAGER_CIDR"
  docker_addr: ":7001"
  file_addr: ":7002"

manager:
  endpoint: "https://manager.example.com/api/v1"
  enrollment_secret: "CHANGE_ME_BASE64_32_BYTES"
  ca_bundle: ""
  skip_verify: false

heartbeat:
  interval_seconds: 30
  failure_log_threshold: 5
```

After copying, ensure these production-specific comments are present near the corresponding fields:

```yaml
  # 生产必须填写 manager 服务器出口 CIDR；留空只适合本地调试。
  trusted_cidr: "CHANGE_ME_MANAGER_CIDR"

manager:
  # 生产填写 manager 对 agent 暴露的 HTTPS API 地址，必须包含 /api/v1。
  endpoint: "https://manager.example.com/api/v1"
  # 生产必须保持 false；只有本地自签调试才允许临时跳过 TLS 校验。
  skip_verify: false
```

- [ ] **Step 3: Create compose file**

Create `deploy/runtime-agent/docker-compose.yml`:

```yaml
services:
  oc-runtime-agent:
    image: ${OC_RUNTIME_AGENT_IMAGE}
    restart: always
    environment:
      OC_AGENT_CONFIG: ${OC_AGENT_CONFIG:-/etc/oc-agent/agent.yaml}
      TZ: ${TZ:-Asia/Shanghai}
    ports:
      - "${RUNTIME_AGENT_GRPC_PORT:-7001}:7001"
      - "${RUNTIME_AGENT_HTTP_PORT:-7002}:7002"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./data/agent:/var/lib/oc-agent
      - ./config/agent.yaml:${OC_AGENT_CONFIG:-/etc/oc-agent/agent.yaml}:ro
    healthcheck:
      test: ["CMD-SHELL", "test -S /var/run/docker.sock && test -f ${OC_AGENT_CONFIG:-/etc/oc-agent/agent.yaml}"]
      interval: 30s
      timeout: 5s
      retries: 5
```

- [ ] **Step 4: Create README**

Create `deploy/runtime-agent/README.md`:

```markdown
# Runtime Agent 生产部署包

本目录部署到每台 Runtime Node。agent 通过宿主 Docker socket 管理 OpenClaw 容器，并主动注册到 manager。

## 启动

```bash
cp .env.example .env
cp config/agent.example.yaml config/agent.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/agent.yaml
docker compose up -d
```

## 必改配置

- `agent.name`：节点展示名。
- `agent.advertise_host`：manager 能访问到的节点 IP 或域名。
- `agent.trusted_cidr`：manager 出口网段，例如 `10.0.0.0/24`。
- `manager.endpoint`：manager API 地址，例如 `https://manager.example.com/api/v1`。
- `manager.enrollment_secret`：必须与 manager.yaml 的 `runtime.enrollment_secret` 一致。

## 防火墙

只允许 manager 出口网段访问：

- `7001`：Docker TLS proxy
- `7002`：File API

不要把这两个端口直接暴露到公网。

## 状态检查

```bash
docker compose ps
docker compose logs -f --tail=100 oc-runtime-agent
```
```

- [ ] **Step 5: Validate runtime-agent compose**

Run:

```bash
docker compose --env-file deploy/runtime-agent/.env.example -f deploy/runtime-agent/docker-compose.yml config
./scripts/check-compose-bind-mounts.sh deploy/runtime-agent/docker-compose.yml
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit runtime-agent package**

Run:

```bash
git add deploy/runtime-agent
git commit -m "chore(deploy): 增加 runtime-agent 生产部署包" -m "为 runtime-agent 增加独立 compose、环境变量模板、agent 配置模板和节点部署说明。"
```

Expected: commit succeeds.

## Task 5: Manage Production Package

**Files:**
- Create: `deploy/manage/docker-compose.yml`
- Create: `deploy/manage/.env.example`
- Create: `deploy/manage/config/manager.example.yaml`
- Create: `deploy/manage/nginx.conf`
- Create: `deploy/manage/README.md`

- [ ] **Step 1: Create `.env.example`**

Create `deploy/manage/.env.example`:

```env
COMPOSE_PROJECT_NAME=oc-manage

OCM_MANAGER_IMAGE=ghcr.io/your-org/openclaw-manager:1.0.0
OCM_WEB_IMAGE=ghcr.io/your-org/openclaw-manager-web:1.0.0

MANAGER_HTTP_PORT=80
MANAGER_HTTPS_PORT=443

MANAGER_POSTGRES_USER=ocm
MANAGER_POSTGRES_PASSWORD=CHANGE_ME_MANAGER_POSTGRES_PASSWORD
MANAGER_POSTGRES_DB=ocm

MANAGER_REDIS_PASSWORD=CHANGE_ME_MANAGER_REDIS_PASSWORD

OCM_CONFIG=/etc/manager/config.yaml
TZ=Asia/Shanghai
```

- [ ] **Step 2: Create manager config template**

Copy `config/manager.example.yaml` to `deploy/manage/config/manager.example.yaml`, then change these defaults:

```yaml
app:
  env: prod
  http_addr: ":8080"
  public_base_url: "https://manager.example.com"
  data_root: "/var/lib/oc-manager/data"
  knowledge_root: "/var/lib/oc-manager/knowledge"

database:
  url: "postgres://ocm:CHANGE_ME_MANAGER_POSTGRES_PASSWORD@manager-postgres:5432/ocm?sslmode=disable"

redis:
  addr: "manager-redis:6379"
  password: "CHANGE_ME_MANAGER_REDIS_PASSWORD"
  db: 0
  key_prefix: "ocm:"

auth:
  cookie_domain: "manager.example.com"

newapi:
  base_url: "https://new-api.example.com"
  admin_token: "CHANGE_ME_NEWAPI_ADMIN_TOKEN"
  admin_user_id: 1

openclaw:
  runtime_image: "openclaw-runtime:1.0.0"
  llm:
    base_url: "https://new-api.example.com/v1"
    default_provider: "openai"
    default_model: "qwen2.5:0.5b"
  container_networks: []
```

After copying, keep the required secret placeholders and ensure this comment is present near
`container_networks`:

```yaml
  # 跨服务器生产环境不再使用本地开发的 oc-manager_default 网络。
  # 留空时 OpenClaw 容器使用 runtime 节点默认 bridge 出口访问远端 new-api；
  # 如 runtime 节点有专用 Docker network，则改为实际 network 名称。
  container_networks: []
```

- [ ] **Step 3: Create nginx config**

Create `deploy/manage/nginx.conf` with the old `deploy/nginx.conf` content, keeping upstream names:

```nginx
# 生产 nginx 终止 TLS 并把 /api/* 反向代理到 manager-api，前端静态资源由 manager-web 提供。
# 生产真实部署需要把 server_name 改成实际域名，并替换证书路径。

upstream manager_api {
    server manager-api:8080;
}

upstream manager_web {
    server manager-web:80;
}

server {
    listen 80;
    server_name _;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name _;

    ssl_certificate /etc/nginx/tls/fullchain.pem;
    ssl_certificate_key /etc/nginx/tls/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;

    add_header X-Frame-Options "DENY" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    client_max_body_size 32M;

    location /api/ {
        proxy_pass http://manager_api;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 60s;
    }

    location /healthz {
        proxy_pass http://manager_api;
    }

    location / {
        proxy_pass http://manager_web;
    }
}
```

- [ ] **Step 4: Create compose file**

Create `deploy/manage/docker-compose.yml`:

```yaml
services:
  manager-postgres:
    image: postgres:17-alpine
    restart: always
    environment:
      POSTGRES_USER: ${MANAGER_POSTGRES_USER}
      POSTGRES_PASSWORD: ${MANAGER_POSTGRES_PASSWORD}
      POSTGRES_DB: ${MANAGER_POSTGRES_DB}
      TZ: ${TZ:-Asia/Shanghai}
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${MANAGER_POSTGRES_USER} -d ${MANAGER_POSTGRES_DB}"]
      interval: 10s
      timeout: 5s
      retries: 20
    networks:
      - manager-internal

  manager-redis:
    image: redis:7
    restart: always
    command: ["redis-server", "--requirepass", "${MANAGER_REDIS_PASSWORD}", "--appendonly", "yes"]
    volumes:
      - ./data/redis:/data
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a ${MANAGER_REDIS_PASSWORD} ping | grep PONG"]
      interval: 10s
      timeout: 5s
      retries: 20
    networks:
      - manager-internal

  manager-api:
    image: ${OCM_MANAGER_IMAGE}
    restart: always
    environment:
      OCM_CONFIG: ${OCM_CONFIG:-/etc/manager/config.yaml}
      TZ: ${TZ:-Asia/Shanghai}
    depends_on:
      manager-postgres:
        condition: service_healthy
      manager-redis:
        condition: service_healthy
    volumes:
      - ./config/manager.yaml:${OCM_CONFIG:-/etc/manager/config.yaml}:ro
      - ./data/manager:/var/lib/oc-manager/data
      - ./data/knowledge:/var/lib/oc-manager/knowledge
      - /var/run/docker.sock:/var/run/docker.sock
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
    networks:
      - manager-internal

  manager-web:
    image: ${OCM_WEB_IMAGE}
    restart: always
    networks:
      - manager-internal

  manager-nginx:
    image: nginx:1.27-alpine
    restart: always
    ports:
      - "${MANAGER_HTTP_PORT:-80}:80"
      - "${MANAGER_HTTPS_PORT:-443}:443"
    volumes:
      - ./nginx.conf:/etc/nginx/conf.d/default.conf:ro
      - ./tls:/etc/nginx/tls:ro
    depends_on:
      manager-api:
        condition: service_healthy
      manager-web:
        condition: service_started
    networks:
      - manager-internal
      - manager-public

networks:
  manager-internal:
    driver: bridge
  manager-public:
    driver: bridge
```

- [ ] **Step 5: Create README**

Create `deploy/manage/README.md`:

```markdown
# Manager 生产部署包

本目录可独立复制到 manager 服务器运行，包含 manager-api、manager-web、nginx、PostgreSQL 和 Redis。

## 启动

```bash
cp .env.example .env
cp config/manager.example.yaml config/manager.yaml
${EDITOR:-vi} .env
${EDITOR:-vi} config/manager.yaml
mkdir -p tls
# 放入 tls/fullchain.pem 和 tls/privkey.pem
docker compose up -d manager-postgres manager-redis
docker compose run --rm manager-api ./migrate up
docker compose up -d
```

## 必改配置

- `.env`：镜像 tag、数据库密码、Redis 密码、HTTP/HTTPS 端口。
- `config/manager.yaml`：域名、JWT/CSRF secret、`security.master_key`、
  `runtime.enrollment_secret`、new-api 地址和 admin token。
- `tls/fullchain.pem` / `tls/privkey.pem`：生产 TLS 证书。

## 运行检查

```bash
docker compose ps
curl -k https://manager.example.com/healthz
```

## 数据

- PostgreSQL：`./data/postgres`
- Redis：`./data/redis`
- manager 数据：`./data/manager`
- 知识库主副本：`./data/knowledge`
```

- [ ] **Step 6: Validate manage compose**

Run:

```bash
docker compose --env-file deploy/manage/.env.example -f deploy/manage/docker-compose.yml config
./scripts/check-compose-bind-mounts.sh deploy/manage/docker-compose.yml
```

Expected: both commands exit 0.

- [ ] **Step 7: Commit manage package**

Run:

```bash
git add deploy/manage
git commit -m "chore(deploy): 增加 manager 生产部署包" -m "为 manager 增加独立 compose、环境变量模板、配置模板、nginx 配置和部署说明，包含私有 PostgreSQL 与 Redis。"
```

Expected: commit succeeds.

## Task 6: Deployment Documentation

**Files:**
- Modify: `deploy/README.md`
- Modify: `deploy/backup.md`
- Modify: `deploy/upgrade.md`

- [ ] **Step 1: Rewrite deploy overview**

Replace `deploy/README.md` with:

```markdown
# OpenClaw Manager 生产部署指南

`deploy/` 下生产部署入口已拆成四个独立运行包：

| 目录 | 部署机器 | 服务 |
|---|---|---|
| `new-api/` | new-api 服务器 | new-api + PostgreSQL + Redis |
| `ollama/` | Ollama 服务器 | Ollama |
| `runtime-agent/` | 每台 Runtime Node | oc-runtime-agent |
| `manage/` | manager 服务器 | manager-api + manager-web + nginx + PostgreSQL + Redis |

根目录 `docker-compose.yml` 仅用于本地调试。旧的 `deploy/docker-compose.prod.yml` 和
`deploy/docker-compose.two-agent.yml` 已废弃。

## 推荐部署顺序

1. 部署 `ollama/`，拉取并验证模型。
2. 部署 `new-api/`，在后台配置 Ollama 渠道并生成系统访问令牌。
3. 部署 `manage/`，把 new-api 地址和 token 写入 `config/manager.yaml`，执行迁移。
4. 在每台 Runtime Node 部署 `runtime-agent/`，使用与 manager 一致的 enrollment secret 自动注册。

每个子目录都包含自己的 README。生产真实值只写入 `.env`、`config/manager.yaml`、
`config/agent.yaml` 和 TLS 文件，不提交到 git。

## 防火墙摘要

- manager：公网开放 `80/443`。
- new-api：至少允许 manager 和 OpenClaw 容器访问 OpenAI 兼容接口。
- ollama：建议只允许 new-api 访问 `11434`。
- runtime-agent：只允许 manager 出口网段访问 `7001/7002`。
```

- [ ] **Step 2: Update backup guide**

Update `deploy/backup.md` so the data table uses these locations:

```markdown
| manager 业务库 | `deploy/manage/data/postgres` | ✅ 关键 |
| manager Redis | `deploy/manage/data/redis` | 📋 可选 |
| manager 知识库主副本 | `deploy/manage/data/knowledge` | ✅ 关键 |
| agent 节点数据 | `deploy/runtime-agent/data/agent` on each Runtime Node | ✅ 关键 |
| new-api 库 | `deploy/new-api/data/postgres` | ✅ 关键 |
| new-api Redis | `deploy/new-api/data/redis` | 📋 可选 |
| Ollama 模型 | `deploy/ollama/data/ollama` | 可按模型拉取策略决定 |
| TLS 证书 | `deploy/manage/tls` | ⚠️ 注意 |
```

Replace old migration restore command with:

```bash
cd deploy/manage
docker compose run --rm manager-api ./migrate up
```

Add these tar backup examples:

```bash
# manager 服务器：备份知识库主副本
tar czf /backups/manager-knowledge-$(date +%Y%m%d).tar.gz -C deploy/manage/data knowledge

# runtime node：备份 agent 数据
tar czf /backups/runtime-agent-$(hostname)-$(date +%Y%m%d).tar.gz -C deploy/runtime-agent/data agent
```

- [ ] **Step 3: Update upgrade guide**

Update `deploy/upgrade.md` commands:

```bash
# manager
cd deploy/manage
${EDITOR:-vi} .env
docker compose pull manager-api manager-web
docker compose run --rm manager-api ./migrate up
docker compose up -d manager-api manager-web manager-nginx

# runtime-agent, on each node
cd deploy/runtime-agent
${EDITOR:-vi} .env
docker compose pull oc-runtime-agent
docker compose up -d oc-runtime-agent

# rollback manager image tags
cd deploy/manage
${EDITOR:-vi} .env
docker compose up -d manager-api manager-web manager-nginx
```

Remove references to `deploy/docker-compose.prod.yml` and direct `docker run` agent upgrade commands.

- [ ] **Step 4: Search for stale old compose references**

Run:

```bash
rg -n "docker-compose\\.prod|docker-compose\\.two-agent|deploy/docker-compose|deploy/nginx\\.conf" deploy docs README.md
```

Expected: no references to the deleted old production files except historical notes in the committed design/plan docs.

- [ ] **Step 5: Commit deployment docs**

Run:

```bash
git add deploy/README.md deploy/backup.md deploy/upgrade.md
git commit -m "docs(deploy): 更新拆分后的生产部署说明" -m "将生产部署文档改为 new-api、ollama、runtime-agent、manage 四个独立入口，并同步备份、升级和回滚命令。"
```

Expected: commit succeeds.

## Task 7: Final Verification

**Files:**
- Verify all deploy files.

- [ ] **Step 1: Run compose config checks**

Run:

```bash
docker compose --env-file deploy/new-api/.env.example -f deploy/new-api/docker-compose.yml config
docker compose --env-file deploy/ollama/.env.example -f deploy/ollama/docker-compose.yml config
docker compose --env-file deploy/runtime-agent/.env.example -f deploy/runtime-agent/docker-compose.yml config
docker compose --env-file deploy/manage/.env.example -f deploy/manage/docker-compose.yml config
```

Expected: all four commands exit 0.

- [ ] **Step 2: Run bind mount checks**

Run:

```bash
./scripts/check-compose-bind-mounts.sh deploy/new-api/docker-compose.yml
./scripts/check-compose-bind-mounts.sh deploy/ollama/docker-compose.yml
./scripts/check-compose-bind-mounts.sh deploy/runtime-agent/docker-compose.yml
./scripts/check-compose-bind-mounts.sh deploy/manage/docker-compose.yml
```

Expected: each command prints `compose 挂载检查通过`.

- [ ] **Step 3: Confirm local dev compose was not changed**

Run:

```bash
git diff --name-only HEAD~6..HEAD -- docker-compose.yml
```

Expected: no output. If output appears, inspect and revert only accidental local-dev changes.

- [ ] **Step 4: Confirm old production entry files are gone**

Run:

```bash
test ! -e deploy/docker-compose.prod.yml
test ! -e deploy/docker-compose.two-agent.yml
test ! -e deploy/nginx.conf
```

Expected: all tests exit 0.

- [ ] **Step 5: Review final status**

Run:

```bash
git status --short
git log --oneline -6
```

Expected: clean working tree; recent commits correspond to skeleton, new-api, ollama, runtime-agent, manage, docs.
