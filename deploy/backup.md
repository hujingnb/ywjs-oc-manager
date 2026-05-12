# OpenClaw Manager 备份与恢复手册

本文档面向运维：覆盖 v1.0 RC 数据持久化与备份恢复的关键路径。

## 数据范围

| 数据 | 存储位置 | 是否必须备份 | 备注 |
|---|---|---|---|
| manager 业务库 | `deploy/manage/data/postgres` | ✅ 关键 | 含组织 / 成员 / 应用 / 审计 / 任务 / refresh_tokens |
| manager Redis | `deploy/manage/data/redis` | 📋 可选 | scheduler 会从 jobs 表重建，丢一个调度周期后自愈 |
| manager 知识库主副本 | `deploy/manage/data/knowledge` | ✅ 关键 | 丢失后所有节点 sync-status 全部失败 |
| agent 节点数据 | `deploy/runtime-agent/data/agent` on each Runtime Node | ✅ 关键 | 应用 workspace / state / logs；OpenClaw 历史会话与产物 |
| new-api 库 | `deploy/new-api/data/postgres` | ✅ 关键 | new-api 账号、渠道、令牌和用量数据 |
| new-api Redis | `deploy/new-api/data/redis` | 📋 可选 | 丢失后按 new-api 自身恢复策略处理 |
| Ollama 模型 | `deploy/ollama/data/ollama` | 可按模型拉取策略决定 | 可重新拉取的模型可不纳入关键备份 |
| TLS 证书 | `deploy/manage/tls` | ⚠️ 注意 | 续期前备份避免证书丢失 |

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
cd deploy/manage
docker compose run --rm manager-api ./migrate up
```

## Redis 备份

仅用于减少恢复时的 job 重新调度延迟。Redis 默认 RDB 持久化已经够用：

拆分后的生产包使用本地 bind mount：manager Redis 位于 `deploy/manage/data/redis`，
new-api Redis 位于 `deploy/new-api/data/redis`。如生产改用外部 Redis / 托管 Redis，
备份按对应服务商或运维入口执行。

## manager 数据卷

```sh
# manager 服务器：备份知识库主副本
tar czf /backups/manager-knowledge-$(date +%Y%m%d).tar.gz -C deploy/manage/data knowledge
```

## agent 节点数据

每个 runtime node 上：

```sh
# runtime node：备份 agent 数据
tar czf /backups/runtime-agent-$(hostname)-$(date +%Y%m%d).tar.gz -C deploy/runtime-agent/data agent
```

恢复：先停 agent 容器，解压备份覆盖 `deploy/runtime-agent/data/agent`，再启动 agent。删除 `state/agent-tls*`
让 agent 重新签证书（避免 hostname 变化导致 SAN mismatch）。

## 灾难恢复演练

每季度演练一次：

1. 拉昨天的 postgres dump + knowledge tar 到测试环境。
2. 跑迁移，启动 manager-api。
3. 启一个测试 agent，在 `config/agent.yaml` 写入测试用 bearer token 后到 manager 注册。
4. 在 platform dashboard 检查应用计数与 prod 一致。
5. 在测试组织新建一个应用，验证完整 onboard → 容器创建 → 健康检查闭环。

演练记录追加到 `docs/uat/disaster-recovery-log.md`。
