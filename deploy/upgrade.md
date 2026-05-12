# OpenClaw Manager 升级与回滚手册

面向运维：覆盖 v1.0 RC 起的版本升级流程、迁移管理、回滚策略。

## 版本号约定

遵循 SemVer：`MAJOR.MINOR.PATCH`。

- **MAJOR**：含破坏性 API / schema 变更，必须读 release notes 并准备数据迁移演练。
- **MINOR**：新功能，向后兼容；可能含 schema 增量迁移（永远是非破坏性 add column / new table）。
- **PATCH**：bug fix；不动 schema 不动 API。

镜像发布标签与 git tag 严格对应：`vX.Y.Z` git tag → `:X.Y.Z` 镜像发布标签。
`:X.Y.Z` SemVer tag 仅作为 CI / release notes 识别发布版本的标签；生产发布
必须在 `.env` 中固定到内容寻址的 `@sha256:` digest。不要使用 `:1.x`
或 `:X.Y.Z` tag 作为生产 `.env` 值，否则重启、扩容和回滚时可能拉到不同镜像内容。

## 升级前检查

1. 拉最新 release notes 确认变更范围。
2. **先备份**：参见 [`backup.md`](./backup.md)。Postgres dump 必做，
   manager 知识库主副本 tar 必做；agent 节点数据 tar 视改动量决定。
3. 在 staging 跑一次完整演练：拉新镜像 → 跑迁移 → 启动 → 跑 smoke。
4. 准备回滚计划（详见下文）。

## 升级步骤

### Step 1 升级 manager

```sh
# manager
cd deploy/manage
${EDITOR:-vi} .env
docker compose pull manager-api manager-web
docker compose run --rm manager-api ./migrate up
docker compose up -d manager-api manager-web manager-nginx
```

在生产 `.env` 中把 `OCM_MANAGER_IMAGE` 和 `OCM_WEB_IMAGE` 更新到
`...@sha256:<digest>`。`:X.Y.Z` tag 可由 CI / release notes 用来识别新版本，
但运维部署前必须先解析并固定对应 digest。
迁移失败 → 立即停止升级，按 release notes 走数据修复流程。
**迁移要永远是 idempotent + non-destructive**：MINOR 升级只允许 add，不允许 drop / alter type。

### Step 2 滚动替换 agent

每个节点逐一升级，避免同时下线所有节点导致应用全部异常：

```sh
# runtime-agent, on each node
cd deploy/runtime-agent
${EDITOR:-vi} .env
docker compose pull oc-runtime-agent
docker compose up -d oc-runtime-agent
```

在每台 Runtime Node 的生产 `.env` 中把 `OC_RUNTIME_AGENT_IMAGE` 更新到
`...@sha256:<digest>`。`:X.Y.Z` tag 可由 CI / release notes 用来识别 runtime-agent
发布版本，但运维部署前必须先解析并固定对应 digest。
agent 重启不丢 OpenClaw 容器（容器本身不重启）；但 agent 重启期间
manager 调 inspect / stats 会失败，UI 上短暂显示「资源指标尚未采集」。

### Step 3 验收

- `curl https://manager.internal/healthz` → 200
- 平台总览 6 项计数与升级前一致
- 跑一个测试应用 onboard，检查容器正常起 + healthz 探针 success
- 关注 manager-api / agent 日志 5 分钟，确认无异常

## 回滚策略

### 紧急回滚（升级后 24h 内）

如果升级后出现核心功能不可用：

```sh
# rollback manager image digests
cd deploy/manage
${EDITOR:-vi} .env
docker compose up -d manager-api manager-web manager-nginx
```

回滚前把 `.env` 中 `OCM_MANAGER_IMAGE` 和 `OCM_WEB_IMAGE` 改回升级前记录的
`...@sha256:<digest>`。
如果新 MINOR 的迁移 add 了字段且新代码强依赖，需要按 release notes 明确指引处理
schema；不要在未确认数据兼容性的情况下直接执行破坏性回滚。

```sh
# rollback runtime-agent, on each node
cd deploy/runtime-agent
${EDITOR:-vi} .env
docker compose pull oc-runtime-agent
docker compose up -d oc-runtime-agent
```

在每台 Runtime Node 回滚前，把 `.env` 中 `OC_RUNTIME_AGENT_IMAGE` 改回升级前
记录的 `...@sha256:<digest>`，避免回滚时拉到已被重指向的镜像引用。

### 数据回滚

只在确认升级后没有写入新结构数据时才能纯靠 rollback migration 恢复。
否则必须 restore postgres dump（参见 backup.md）→ 数据丢失从 dump 时刻到现在。

### 回滚检查清单

- [ ] manager-api 进程跑旧版本 + healthz 200
- [ ] agent 节点跑旧 / 新版本均可（agent 协议向后兼容）
- [ ] 平台总览六项计数与回滚前一致
- [ ] 一个测试应用 onboard 成功
- [ ] 已撤销容易误用的新版本镜像别名

## 镜像标签管理

- `:dev` → 开发环境最新 main
- `:stage` → staging 环境，每周构建
- `:1.0.0` `:1.1.0` ... → release notes / CI 识别发布版本的 SemVer 标签
- `:1.x` → 可选发布 family 标签，仅供测试或人工拉取，不用于生产 `.env`

每次发版可以用 `:1.x.y` 这类完整版本 tag 标识发布版本；生产 `.env` 中的
镜像引用必须解析并固定为 `@sha256:` digest，确保重启、扩容和回滚时拉取到
同一个镜像内容。

## 常见问题

- **迁移卡住**：检查 postgres pg_locks，是否有别的 session 持锁；优先杀 idle in transaction 会话。
- **agent 升级后 register 失败**：先看 `agent-tls*` 是否要重生成；再看 manager-api 日志是否有 cert SAN mismatch。
- **回滚后 worker 处理不完旧 job**：旧版本不认识新加的 job_type 会标记为失败 + 触发 dead letter。需要在回滚前先把新 job_type 标记为 `cancelled`。
