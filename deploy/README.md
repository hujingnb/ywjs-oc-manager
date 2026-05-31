# 部署总览

> 部署已统一到 Kubernetes，部署清单全部在 [`k8s/`](./k8s/) 下。原先按运行包拆分的
> docker-compose 部署方式（`manage/` / `new-api/` / `ollama/` / `ragflow/` /
> `runtime-agent/`）已随 k8s 迁移收口删除。

## 目录

| 目录 | 用途 | 部署方式 |
|---|---|---|
| [`k8s/local/`](./k8s/local/) | 本地 k3d 全栈，完整自包含（含 MySQL/Redis/ES/MinIO 等有状态件） | 仓库根 `make local-up` 一键拉起，勿手工逐个 apply |
| [`k8s/prod/`](./k8s/prod/) | 生产标准 k8s YAML，有状态后端外置、只生成不自动部署 | 填值后按 `k8s/prod/README.md` 的 apply 顺序部署 |
| [`k8s/contracts/`](./k8s/contracts/) | app-pod / oc-ops / RBAC 契约样例 | 文档化、不 apply |

## 跳转

- [k8s 部署清单总说明](./k8s/README.md)
- [生产部署步骤（填值 + apply 顺序）](./k8s/prod/README.md)
- [本地开发（k3d）](../docs/local-development.md)

## 真实值约定

生产真实连接配置与密钥统一只填 `k8s/prod/secret.yaml`（由 `secret.example.yaml`
复制而来，不进 git）：外部 MySQL/Redis/ES/对象存储连接、`master_key`、jwt/csrf
secret、域名/TLS、各组件 API key 等。待填项以 `__FILL_*__` 前缀标记，
`grep -n '__FILL_' secret.yaml` 无输出即填写完整。详见 `k8s/prod/README.md`。
