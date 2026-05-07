# OpenClaw Manager 升级与回滚手册

面向运维：覆盖 v1.0 RC 起的版本升级流程、迁移管理、回滚策略。

## 版本号约定

遵循 SemVer：`MAJOR.MINOR.PATCH`。

- **MAJOR**：含破坏性 API / schema 变更，必须读 release notes 并准备数据迁移演练。
- **MINOR**：新功能，向后兼容；可能含 schema 增量迁移（永远是非破坏性 add column / new table）。
- **PATCH**：bug fix；不动 schema 不动 API。

镜像 tag 与 git tag 严格对应：`vX.Y.Z` git tag → `:X.Y.Z` 镜像 tag。
不要用 `:latest` 部署生产，无法回滚。

## 升级前检查

1. 拉最新 release notes 确认变更范围。
2. **先备份**：参见 [`backup.md`](./backup.md)。Postgres dump 必做，
   manager-knowledge 卷 tar 必做；agent 节点 rsync 视改动量决定。
3. 在 staging 跑一次完整演练：拉新镜像 → 跑迁移 → 启动 → 跑 smoke。
4. 准备回滚计划（详见下文）。

## 升级步骤

### Step 1 拉新镜像

```sh
docker pull ghcr.io/your-org/openclaw-manager:1.1.0
docker pull ghcr.io/your-org/openclaw-manager-web:1.1.0
docker pull oc-runtime-agent:1.1.0
```

### Step 2 跑迁移

迁移用 manager-api 镜像跑（独立容器，不影响在跑的服务）：

```sh
docker run --rm \
  --env-file .env \
  ghcr.io/your-org/openclaw-manager:1.1.0 \
  ./migrate up
```

迁移失败 → 立即停止升级，按 release notes 走数据修复流程。
**迁移要永远是 idempotent + non-destructive**：MINOR 升级只允许 add，不允许 drop / alter type。

### Step 3 滚动替换 manager-api

```sh
# 更新 .env 里 OCM_MANAGER_IMAGE
sed -i 's|openclaw-manager:.*|openclaw-manager:1.1.0|' .env
# nginx 反代会在 manager-api 健康前后无缝切换；如果只有一个实例需要短暂中断
docker compose -f deploy/docker-compose.prod.yml up -d manager-api
```

### Step 4 滚动替换 agent

每个节点逐一升级，避免同时下线所有节点导致应用全部异常：

```sh
docker stop oc-runtime-agent
docker rm oc-runtime-agent
docker run -d --name oc-runtime-agent --restart=always \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/oc-agent:/var/lib/oc-agent \
  -v /etc/oc-agent/agent.yaml:/etc/oc-agent/agent.yaml:ro \
  -p 7001:7001 -p 7002:7002 \
  -e OC_AGENT_CONFIG=/etc/oc-agent/agent.yaml \
  oc-runtime-agent:1.1.0
```

agent 重启不丢 OpenClaw 容器（容器本身不重启）；但 agent 重启期间
manager 调 inspect / stats 会失败，UI 上短暂显示「资源指标尚未采集」。

### Step 5 验收

- `curl https://manager.internal/healthz` → 200
- 平台总览 6 项计数与升级前一致
- 跑一个测试应用 onboard，检查容器正常起 + healthz 探针 success
- 关注 manager-api / agent 日志 5 分钟，确认无异常

## 回滚策略

### 紧急回滚（升级后 24h 内）

如果升级后出现核心功能不可用：

```sh
# 1. 切回旧镜像 tag
sed -i 's|openclaw-manager:1.1.0|openclaw-manager:1.0.0|' .env
docker compose -f deploy/docker-compose.prod.yml up -d manager-api

# 2. 如果新 MINOR 的迁移 add 了字段且新代码强依赖，需要回滚 schema
docker run --rm --env-file .env ghcr.io/your-org/openclaw-manager:1.1.0 ./migrate down 1
```

`migrate down 1` 只回退最近一次迁移；多次迁移要逐次回退并核对每个 down.sql。

### 数据回滚

只在确认升级后没有写入新结构数据时才能纯靠 rollback migration 恢复。
否则必须 restore postgres dump（参见 backup.md）→ 数据丢失从 dump 时刻到现在。

### 回滚检查清单

- [ ] manager-api 进程跑旧版本 + healthz 200
- [ ] agent 节点跑旧 / 新版本均可（agent 协议向后兼容）
- [ ] 平台总览六项计数与回滚前一致
- [ ] 一个测试应用 onboard 成功
- [ ] 已撤销新版本镜像 tag，避免 ops 误用

## 镜像标签管理

- `:dev` → 开发环境最新 main
- `:stage` → staging 环境，每周构建
- `:1.0.0` `:1.1.0` ... → 生产环境 SemVer 标签
- `:latest` → 不在生产用

每次发版同时打 `:1.x.y` 与 `:1.x` 两个 tag，让 ops 可以选「跟到最新 patch」或「锁版本」。

## 常见问题

- **迁移卡住**：检查 postgres pg_locks，是否有别的 session 持锁；优先杀 idle in transaction 会话。
- **agent 升级后 register 失败**：先看 `agent-tls*` 是否要重生成；再看 manager-api 日志是否有 cert SAN mismatch。
- **回滚后 worker 处理不完旧 job**：旧版本不认识新加的 job_type 会标记为失败 + 触发 dead letter。需要在回滚前先把新 job_type 标记为 `cancelled`。
