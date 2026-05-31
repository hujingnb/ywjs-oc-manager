# hermes-v2026.5.16 · Variant 契约

- 上游仓库: https://github.com/NousResearch/hermes-agent
- 锁定 ref: 见同目录 version.txt
- 安装方式: install.sh + uv (FHS layout，代码装到 /usr/local/lib/hermes-agent/)
- 数据迁移: 本 variant 由历史 `hermes-main` 重命名而来，允许 `hermes-main` 到 `hermes-v2026.5.16` 的 no-op 迁移；未来新增 variant 时，迁移模块名使用安全后缀，例如从 `hermes-v2026.5.16` 迁移时模块为 `from_hermes_v2026_5_16.py`。

# 镜像对外命令
- oc-healthcheck（HEALTHCHECK）
- oc-kb（容器内 agent 知识库检索 CLI，调 manager runtime API）
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint

# oc-ops HTTP 服务（spec-E）
- 同镜像第二用途：覆盖 CMD 启动 `python -m uvicorn ocops.server:app --host 0.0.0.0 --port 8080`
- Bearer OC_OPS_TOKEN 鉴权；端点见 docs（info/doctor/cron/kanban/channel 类型化 REST + SSE）
- 运维能力统一经 HTTP 暴露；原 oc-channel-*/oc-cron/oc-kanban/oc-info/oc-doctor CLI shim 已移除，逻辑收敛到 ocops 包
