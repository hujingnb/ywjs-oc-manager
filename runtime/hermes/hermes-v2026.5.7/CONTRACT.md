# hermes-v2026.5.7 · Variant 契约

- 上游仓库: https://github.com/NousResearch/hermes-agent
- 锁定 ref: 见同目录 version.txt
- 安装方式: install.sh + uv (FHS layout，代码装到 /usr/local/lib/hermes-agent/)
- 数据迁移: 本 variant 由历史 `hermes-main` 重命名而来，允许 `hermes-main` 到 `hermes-v2026.5.7` 的 no-op 迁移；未来新增 variant 时，迁移模块名使用安全后缀，例如从 `hermes-v2026.5.16` 迁移时模块为 `from_hermes_v2026_5_16.py`。

# 镜像对外命令
- oc-info / oc-doctor / oc-healthcheck
- oc-channel-login / oc-channel-status / oc-channel-unbind
- oc-kanban
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint
