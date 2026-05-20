# hermes-main · Variant 契约

- 上游仓库: https://github.com/NousResearch/hermes-agent
- 锁定 ref: 见同目录 version.txt
- 安装方式: install.sh + uv (FHS layout，代码装到 /usr/local/lib/hermes-agent/)
- 数据迁移: 首版 hermes-main 没有 from_<prev>.py；未来新增 variant 时新建对应 from_hermes-main.py

# 镜像对外命令
- oc-info / oc-doctor / oc-healthcheck
- oc-channel-login / oc-channel-status / oc-channel-unbind
- oc-kanban
- ENTRYPOINT: tini -g -- /usr/local/bin/oc-entrypoint
