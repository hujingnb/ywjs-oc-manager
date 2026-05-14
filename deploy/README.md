# Agent Runtime Manager 生产部署指南

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
- new-api：至少允许 manager 和 Hermes 容器访问 OpenAI 兼容接口。
- ollama：建议只允许 new-api 访问 `11434`。
- runtime-agent：只允许 manager 出口网段访问 `7001/7002`。
