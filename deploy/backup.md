# OpenClaw Manager 备份与恢复手册

本文档面向运维：覆盖 v1.0 RC 数据持久化与备份恢复的关键路径。

## 数据范围

| 数据 | 存储位置 | 是否必须备份 | 备注 |
|---|---|---|---|
| manager 业务库 | 外部 PostgreSQL / RDS | ✅ 关键 | 含组织 / 成员 / 应用 / 审计 / 任务 / refresh_tokens |
| Redis 任务队列 | 外部 Redis / 托管 Redis | 📋 可选 | scheduler 会从 jobs 表重建，丢一个调度周期后自愈 |
| manager `data_root` | manager-knowledge volume | ✅ 关键 | 组织级知识库主副本，丢失后所有节点 sync-status 全部失败 |
| agent `node_data_root` | 每节点 `/var/lib/oc-agent` | ✅ 关键 | 应用 workspace / state / logs；OpenClaw 历史会话与产物 |
| new-api 库（独立部署） | new-api 项目自备 | ✅ 关键 | manager 不接管 new-api 的备份 |
| TLS 证书 | nginx 卷 / acme 仓库 | ⚠️ 注意 | 续期前备份避免证书丢失 |

`MASTER_KEY` **不要写入备份**：与备份分离存储到密钥管理 KMS / 密码本，
否则备份泄露时所有 agent_token 密文都能被解密。

## PostgreSQL 备份

每日 dump（保留 7 天）：

```sh
pg_dump "$OCM_DATABASE_URL" -Fc > /backups/ocm-$(date +%Y%m%d).dump
find /backups -name "ocm-*.dump" -mtime +7 -delete
```

恢复（破坏性，请先确认目标库为空）：

```sh
pg_restore --clean --if-exists --dbname "$OCM_DATABASE_URL" /backups/ocm-20260601.dump
```

恢复后必须重新跑迁移，确保 schema 升到当前版本：

```sh
docker compose -f deploy/docker-compose.prod.yml run --rm manager-api ./migrate up
```

## Redis 备份

仅用于减少恢复时的 job 重新调度延迟。Redis 默认 RDB 持久化已经够用：

外部 Redis / 托管 Redis 的备份按对应服务商或运维入口执行；本仓库的
`deploy/docker-compose.prod.yml` 不启动 Redis 实例。

## manager 数据卷

```sh
docker run --rm -v oc-manager_manager-knowledge:/data -v /backups:/out alpine \
  tar czf /out/knowledge-$(date +%Y%m%d).tar.gz -C /data .
```

## agent 节点数据

每个 runtime node 上：

```sh
rsync -aH --delete /var/lib/oc-agent/ backup-host:/backups/oc-agent/$(hostname)/
```

恢复：先停 agent 容器，rsync 回去，再启动 agent。删除 `state/agent-tls*`
让 agent 重新签证书（避免 hostname 变化导致 SAN mismatch）。

## 灾难恢复演练

每季度演练一次：

1. 拉昨天的 postgres dump + knowledge tar 到测试环境。
2. 跑迁移，启动 manager-api。
3. 启一个测试 agent，在 `config/agent.yaml` 写入测试用 bearer token 后到 manager 注册。
4. 在 platform dashboard 检查应用计数与 prod 一致。
5. 在测试组织新建一个应用，验证完整 onboard → 容器创建 → 健康检查闭环。

演练记录追加到 `docs/uat/disaster-recovery-log.md`。
