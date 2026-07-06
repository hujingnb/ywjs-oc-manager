# hermes-v2026.7.1 · Variant 契约

- 上游仓库: https://github.com/NousResearch/hermes-agent
- 锁定 ref: 见同目录 version.txt
- 安装方式: install.sh + uv (FHS layout，代码装到 /usr/local/lib/hermes-agent/)
- 上游引擎语义版本: v0.18.0（上游 git tag `v2026.7.1` 的 pyproject.toml version；上一版 `hermes-v2026.6.5` 为 v0.16.0）
- 数据迁移: 本 variant 由上一版 `hermes-v2026.6.5` 整体复制并升级 version.txt 而来。跨 variant 数据过渡由 `migrator/` 基于文件检测完成（按 data_root 自身 `.oc-state.json` 记录的版本判断，而非按来源版本名 dispatch），对升级 / 降级、任意来源版本通用，不需要为 `hermes-v2026.6.5` 单独写 `from_hermes_v2026_6_5.py` 迁移模块。当前各 variant 的 on-disk 布局一致，从 `hermes-v2026.6.5` 切换到本 variant 为 no-op 过渡。

# 镜像对外命令
- oc-healthcheck（HEALTHCHECK）
- oc-kb（容器内 agent 知识库检索 CLI，调 manager runtime API）
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint

# oc-ops HTTP 服务（spec-E）
- 同镜像第二用途：覆盖 CMD 启动 `python -m uvicorn ocops.server:app --host 0.0.0.0 --port 8080`
- Bearer OC_OPS_TOKEN 鉴权；端点见 docs（info/doctor/cron/kanban/channel 类型化 REST + SSE）
- 运维能力统一经 HTTP 暴露；原 oc-channel-*/oc-cron/oc-kanban/oc-info/oc-doctor CLI shim 已移除，逻辑收敛到 ocops 包
- HTTP 控制面契约由构建期注入的 `ocops-contract/` 提供，镜像内路径为 `/usr/local/lib/ocops/contract/`
