# 生产 Docker Compose 拆分设计

## 背景

当前生产部署入口集中在 `deploy/docker-compose.prod.yml`，并假设数据库、Redis、
new-api、ollama 等由外部基础设施提供；本地调试入口 `docker-compose.yml` 则把
manager、new-api、ollama、runtime-agent、PostgreSQL、Redis 都放在一个 compose
里。后续生产环境会把 new-api、ollama、runtime-agent、manager 分别部署到不同
服务器，因此生产部署资产需要按服务边界拆成可独立交付的目录。

本设计只覆盖 `deploy/` 下生产部署包的重构。本地调试环境继续使用根目录
`docker-compose.yml` 一键启动，不改变现有本地开发行为。

## 目标

- 每个生产服务一个独立目录，目录拷到对应服务器后可通过 `docker compose up -d`
  启动。
- 每个目录包含自身运行所需的 compose、环境变量模板、配置模板、README 和本地
  持久化目录约定。
- 生产 compose 只引用预构建镜像，不在服务器上依赖源码构建镜像。
- 每个服务目录自带需要的 PostgreSQL、Redis、配置文件、数据目录等基础设施定义，
  形成独立完整的部署包。
- 旧生产 compose 入口直接废弃，文档统一指向新的四个目录，避免误用旧入口。

## 非目标

- 不改根目录 `docker-compose.yml` 的本地调试拓扑。
- 不新增 CI 镜像构建流程，也不新增生产镜像 Dockerfile。
- 不自动配置 new-api 后台的 Ollama 渠道；该步骤仍由运维在 new-api 后台完成，或
  后续另行设计脚本。
- 不在设计阶段修改实际 compose 文件；实现阶段按本 spec 执行。

## 目录结构

生产部署目录拆成四个自包含运行包：

```text
deploy/
├── new-api/
│   ├── docker-compose.yml
│   ├── .env.example
│   ├── README.md
│   ├── data/
│   │   ├── postgres/.gitkeep
│   │   ├── redis/.gitkeep
│   │   └── new-api/.gitkeep
│   └── logs/.gitkeep
├── ollama/
│   ├── docker-compose.yml
│   ├── .env.example
│   ├── README.md
│   └── data/ollama/.gitkeep
├── runtime-agent/
│   ├── docker-compose.yml
│   ├── .env.example
│   ├── README.md
│   ├── config/agent.example.yaml
│   └── data/agent/.gitkeep
└── manage/
    ├── docker-compose.yml
    ├── .env.example
    ├── README.md
    ├── config/manager.example.yaml
    ├── nginx.conf
    ├── tls/.gitkeep
    └── data/
        ├── postgres/.gitkeep
        ├── redis/.gitkeep
        ├── manager/.gitkeep
        └── knowledge/.gitkeep
```

各目录可以独立复制到目标服务器；复制后运维基于 `.env.example` 生成 `.env`，
基于 `config/*.example.yaml` 生成真实配置文件，再执行 `docker compose up -d`。

## 服务边界

### new-api

`deploy/new-api/` 负责运行 new-api 及其私有 PostgreSQL、Redis：

- `new-api-postgres`：new-api 数据库，数据持久化到 `./data/postgres`。
- `new-api-redis`：new-api 缓存和队列，数据持久化到 `./data/redis`。
- `new-api`：使用 `.env` 中的 `NEWAPI_IMAGE`，日志挂载到 `./logs`，数据挂载到
  `./data/new-api`。

new-api 默认对外暴露 `3000`。manager 所在服务器通过 `http(s)://<new-api-host>:3000`
访问 new-api 管理接口和 OpenAI 兼容接口。

### ollama

`deploy/ollama/` 负责运行 Ollama：

- `ollama`：使用 `.env` 中的 `OLLAMA_IMAGE`，模型数据持久化到 `./data/ollama`。

默认保留 NVIDIA GPU reservation，README 说明无 GPU 环境需要移除或调整该段配置。
Ollama 默认暴露 `11434`，建议防火墙只允许 new-api 所在服务器访问。

### runtime-agent

`deploy/runtime-agent/` 负责每台 runtime node 上的 agent：

- `oc-runtime-agent`：使用 `.env` 中的 `OC_RUNTIME_AGENT_IMAGE`。
- 挂载宿主 `/var/run/docker.sock`，让 agent 代理 Docker 操作。
- 挂载 `./config/agent.yaml` 到容器内只读路径，并通过 `OC_AGENT_CONFIG` 指定。
- 挂载 `./data/agent` 保存 agent 数据、TLS 证书、注册状态、应用工作目录和知识库镜像。

agent 默认暴露 `7001` 和 `7002`。生产防火墙只允许 manager 出口网段访问这两个端口。
每台机器必须修改 `agent.name`、`agent.advertise_host`、`agent.trusted_cidr`、
`manager.endpoint` 和 `manager.enrollment_secret`。

### manage

`deploy/manage/` 负责 manager 核心服务及其私有 PostgreSQL、Redis、nginx：

- `manager-postgres`：manager 业务库，数据持久化到 `./data/postgres`。
- `manager-redis`：manager 队列、锁和短期状态，数据持久化到 `./data/redis`。
- `manager-api`：使用 `.env` 中的 `OCM_MANAGER_IMAGE`，读取
  `./config/manager.yaml`，持久化业务数据到 `./data/manager`，知识库主副本到
  `./data/knowledge`。
- `manager-web`：使用 `.env` 中的 `OCM_WEB_IMAGE`，只在 compose 内部网络暴露。
- `manager-nginx`：对外暴露 `80/443`，挂载 `nginx.conf` 和 `./tls`。

manager-api 与 manager-web 不直接对外发布端口；外部流量统一走 nginx。首次部署和
升级时通过 `docker compose run --rm manager-api ./migrate up` 执行数据库迁移。

## 配置约定

- `.env.example` 只放 compose 启动参数：镜像名、端口映射、数据库密码、Redis 密码、
  对外域名和远端服务地址占位。
- 真实 `.env`、`config/manager.yaml`、`config/agent.yaml` 不提交。
- `manage/config/manager.example.yaml` 从现有 `config/manager.example.yaml` 派生，但默认
  使用跨服务器占位：
  - `database.url` 指向 `manager-postgres:5432`。
  - `redis.addr` 指向 `manager-redis:6379`。
  - `newapi.base_url` 使用 `https://new-api.example.com` 形式的占位。
  - `openclaw.llm.base_url` 使用 `https://new-api.example.com/v1` 形式的占位。
  - `openclaw.container_networks` 不再默认写本仓库本地 compose 网络
    `oc-manager_default`。跨服务器生产环境可留空，让 OpenClaw 容器使用 runtime
    节点 Docker 默认 bridge 出口访问远端 new-api；如 runtime 节点有专用 Docker
    network，则由运维按实际网络名填写。README 说明必须保证容器能访问 new-api 的
    OpenAI 兼容 endpoint。
- `runtime-agent/config/agent.example.yaml` 从现有 `config/agent.example.yaml` 派生，但默认
  使用 `https://manager.example.com/api/v1` 形式的 manager endpoint 占位，并保留
  `skip_verify: false`。

## 网络与安全

- manage 服务器对公网只开放 nginx `80/443`。
- runtime-agent 服务器只对 manager 出口网段开放 `7001/7002`。
- new-api 服务器按实际业务需要开放 `3000` 或由反代暴露 HTTPS；至少 manager 和
  OpenClaw 容器必须能访问。
- ollama 服务器建议只允许 new-api 服务器访问 `11434`。
- `security.master_key`、JWT secret、CSRF secret、new-api admin token、
  runtime enrollment secret、数据库密码和 Redis 密码只能写入真实配置或 `.env`，不能
  写入入仓文件。

## 旧入口处理

实施时直接废弃旧生产入口：

- 删除 `deploy/docker-compose.prod.yml`。
- 删除 `deploy/docker-compose.two-agent.yml`。
- 更新 `deploy/README.md`、`deploy/backup.md`、`deploy/upgrade.md` 中所有旧 compose
  路径、启动命令、迁移命令和回滚命令。

如果需要保留历史说明，只在文档中说明“旧入口已废弃，请使用
`deploy/<service>/docker-compose.yml`”，不再保留可执行的旧生产 compose 入口。

## 验证

实现完成后执行以下检查：

```bash
docker compose -f deploy/new-api/docker-compose.yml config
docker compose -f deploy/ollama/docker-compose.yml config
docker compose -f deploy/runtime-agent/docker-compose.yml config
docker compose -f deploy/manage/docker-compose.yml config

./scripts/check-compose-bind-mounts.sh deploy/new-api/docker-compose.yml
./scripts/check-compose-bind-mounts.sh deploy/ollama/docker-compose.yml
./scripts/check-compose-bind-mounts.sh deploy/runtime-agent/docker-compose.yml
./scripts/check-compose-bind-mounts.sh deploy/manage/docker-compose.yml
```

默认不启动真实生产容器做验证，因为生产镜像 tag、真实密钥、TLS 证书、域名和跨服务器
地址都由部署环境提供。若需要额外 smoke，应在 staging 上补充真实 `.env` 和真实 YAML
后执行。
