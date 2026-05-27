# 生产部署总览

> 生产部署由五个独立运行包组成（new-api / ollama / ragflow / runtime-agent / manage），
> 各运行包可独立复制到对应服务器后直接启动，子目录各有自己的 README。

## 1. 部署拓扑

| 目录 | 部署机器 | 主要服务 | 对外端口 |
|---|---|---|---|
| `new-api/` | new-api 服务器 | new-api + PostgreSQL + Redis | `3000`（HTTP，可由前置反代覆盖） |
| `ollama/` | GPU 节点（可与 new-api 同机） | Ollama 模型服务 | `11434` |
| `ragflow/` | RAGFlow 服务器（可与 manager 同机） | RAGFlow + MySQL + Redis/Valkey + MinIO + Elasticsearch | `8088`（控制台）、`9380`（HTTP API）、`9381`（Admin） |
| `runtime-agent/` | 每台 Runtime Node | oc-runtime-agent | `7001`（gRPC）、`7002`（HTTP） |
| `manage/` | manager 服务器 | manager-api + manager-web + nginx + PostgreSQL + Redis | `80` / `443` |

根目录 `docker-compose.yml` 仅用于本地调试。

## 2. 推荐部署顺序

1. **ollama**：启动模型服务，拉取并验证所需模型（`ollama pull <model>`）。
2. **new-api**：启动后在后台配置 Ollama 渠道，并在「个人设置 → 安全设置 → 系统访问令牌」生成管理用 access token。
3. **ragflow**：复制 `deploy/ragflow/.env.example` 为 `.env`，填入真实密码与端口，
   启动独立 RAGFlow 服务：

   ```sh
   cd deploy/ragflow
   docker compose up -d
   ```

4. **manage**：把 new-api 地址和 admin token 写入 `config/manager.yaml`（参考
   `config/manager.example.yaml`），启动服务（manager-api 启动时自动跑
   `migrate up`，多副本由 golang-migrate 的 PG advisory lock 互斥）：

   ```sh
   cd deploy/manage
   docker compose up -d
   ```

   同时把 `ragflow.base_url` 指向独立 RAGFlow HTTP API，例如
   `http://host.docker.internal:9380`（同机部署）或 `https://ragflow.example.com`。
   切换到 RAGFlow 知识库后，manager 不会自动导入旧本地知识库目录中的文件；
   生产升级前需要先导出旧文件，升级后通过组织 / 实例知识库页面重新上传。

5. **runtime-agent × N**：在每台 Runtime Node 写入 `config/agent.yaml`，
   与 manager 使用相同的 enrollment secret，启动后自动完成注册：

   ```sh
   cd deploy/runtime-agent
   docker compose up -d
   ```

## 3. 防火墙摘要

| 服务 | 开放对象 | 端口 |
|---|---|---|
| manager（nginx） | 公网 | `80` / `443` |
| new-api | manager 服务器、Hermes 容器所在网段 | `3000`（或自定义端口） |
| ollama | new-api 服务器 | `11434` |
| ragflow | manager 服务器、受控运维入口 | `8088`（Web 控制台）、`9380`（仅 manager 访问）、`9381`（仅受控运维访问） |
| runtime-agent | manager 出口网段 | `7001`（gRPC）、`7002`（HTTP） |

runtime-agent 的两个端口不应对公网开放，manager 通过内网 IP 或 VPN 访问即可。

## 4. 真实值约定

生产真实值只写入以下文件，不进 git：

- `deploy/manage/.env`、`deploy/manage/config/manager.yaml`
- `deploy/new-api/.env`
- `deploy/ollama/.env`
- `deploy/ragflow/.env`
- `deploy/runtime-agent/.env`、`deploy/runtime-agent/config/agent.yaml`
- TLS 证书文件（`deploy/manage/tls/`）

各运行包提供 `.env.example` 和 `config/*.example.yaml` 作为占位模板，
基于 example 复制后填入真实值。`MASTER_KEY` 等高敏感密钥与备份介质分离存储，
不得写入备份档案。

## 5. 跳转

- [manager 服务部署](./manage/README.md)
- [new-api 部署](./new-api/README.md)
- [ollama 部署](./ollama/README.md)
- [RAGFlow 部署](./ragflow/README.md)
- [runtime-agent 部署](./runtime-agent/README.md)
- [运维手册](./operations.md)
