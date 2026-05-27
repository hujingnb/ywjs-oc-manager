# RAGFlow 部署包

本目录独立启动 RAGFlow 及其 MySQL、Redis/Valkey、MinIO、Elasticsearch 依赖。
manager 不加入该 Compose 网络，只通过 RAGFlow HTTP API 访问。

## 部署要求

- 生产使用独立 `deploy/ragflow/docker-compose.yml`，不要把 RAGFlow 依赖合并到
  `deploy/manage/docker-compose.yml`。
- 生产必须复制本目录 `.env.example` 后填入真实强密码，不要复用根目录 `.env`
  中仅供本地联调的默认密码。
- RAGFlow、MySQL、Redis/Valkey、MinIO、Elasticsearch 镜像必须固定具体 tag 或
  digest，避免 `latest` 漂移。
- Elasticsearch 默认内存上限由 `RAGFLOW_MEM_LIMIT` 控制，需按宿主资源调大或调小。
- RAGFlow Web、HTTP API、Admin API 默认只绑定 `127.0.0.1`。如需跨主机访问，
  只应绑定到受控内网地址，并通过防火墙、VPN、堡垒机或反向代理限制来源。
- MySQL、Redis/Valkey、MinIO、Elasticsearch 只在 `ragflow-internal` 网络内通信，
  不通过宿主机端口暴露。

## 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

启动后，在 RAGFlow 服务器本机或 SSH 隧道内访问：

- RAGFlow 控制台：`http://127.0.0.1:${RAGFLOW_WEB_HTTP_PORT}`
- RAGFlow HTTP API：`http://127.0.0.1:${RAGFLOW_HTTP_PORT}`
- RAGFlow Admin API：`http://127.0.0.1:${RAGFLOW_ADMIN_HTTP_PORT}`

数据与日志默认落在当前目录：

- `./data/mysql`：RAGFlow MySQL 元数据
- `./data/redis`：RAGFlow Redis/Valkey 持久化
- `./data/minio`：上传原文件与中间产物
- `./data/elasticsearch`：检索与向量索引
- `./logs`：RAGFlow 服务日志

## manager 配置

如果 RAGFlow 和 manager 在同一台服务器且都使用本仓库部署包，manager 需要能访问
RAGFlow HTTP API。常见做法是把 `RAGFLOW_HTTP_BIND` 改成 manager 容器可访问的
宿主内网地址或 host-gateway 地址，并用防火墙限制来源，然后在
`deploy/manage/config/manager.yaml` 配置：

```yaml
ragflow:
  base_url: "http://host.docker.internal:9380"
  api_key: "CHANGE_ME_MANAGER_ONLY_RAGFLOW_API_KEY"
```

`api_key` 在 RAGFlow 控制台中创建，只保存在 manager 后端配置里，不下发给
Hermes 或浏览器。

如果 RAGFlow 部署在独立服务器，把 `base_url` 改为该服务器的内网地址或受控
HTTPS 入口，例如 `https://ragflow.example.com`。RAGFlow HTTP API 只需要对
manager 服务器开放，不应直接对公网开放。

## 模型配置

RAGFlow 调模型的 key 不由 manager 管理。管理员需要在 RAGFlow 控制台手工配置
existing new-api 的 base URL、专用 API key，并选择 DeepSeek 模型。

推荐初始化顺序：

1. 首次初始化如需注册管理员，临时把 `.env` 中 `RAGFLOW_REGISTER_ENABLED` 改为 `1` 并重启 RAGFlow；
2. 创建管理员后立即把 `RAGFLOW_REGISTER_ENABLED` 改回 `0` 并再次重启；
3. 在 new-api 中创建 RAGFlow 专用模型 API key；
4. 在 RAGFlow 控制台配置 new-api base URL 和该 key；
5. 选择 DeepSeek 模型作为解析、embedding、retrieval 使用的模型；
6. 上传一个测试文档，确认解析和 retrieval 可以跑通；
7. 在 RAGFlow 控制台创建 manager 专用 RAGFlow API key，并写入
   `deploy/manage/config/manager.yaml` 的 `ragflow.api_key`。

## 维护说明

- RAGFlow Web 只面向管理员开放；HTTP API 只面向 manager 服务器开放；Admin API
  仅用于受控运维排障。
- 数据库与对象存储保持 Compose 内部网络可见即可。
- manager 知识库接口报错时，先确认 `docker compose ps` 中 RAGFlow 及依赖服务
  均为 healthy，再从 manager 服务器访问 `http://<ragflow-host>:${RAGFLOW_HTTP_PORT}`。
