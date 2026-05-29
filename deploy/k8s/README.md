# k8s 部署清单（spec-D）

本目录是 spec-D 的交付物。设计见
`docs/superpowers/specs/2026-05-29-spec-d-deploy-k3d-design.md`。

## 三套独立内容

- `local/`：本地 k3d 全栈，**完整自包含**（含 MySQL/Redis/ES/MinIO 有状态件）。
  通过仓库根 `make local-up` 一键拉起，勿手工逐个 apply。
- `prod/`：生产标准 k8s YAML，**只生成不自动部署**。有状态后端外置，
  仅留连接占位；填值与 apply 顺序见 `prod/README.md`。
- `contracts/`：依赖 spec-A/B/E 的 app-pod / oc-ops / RBAC 契约样例，
  **文档化、不 apply**，供后续 spec 对齐字段。

## 设计要点

- 裸 YAML，无 base、无 kustomize、无 Helm；local 与 prod 各一套完整集合，
  接受重复换取零耦合、可独立读懂。
- 本地凭证为开发固定值，仅用于本地，禁止用于任何线上/共享环境。
