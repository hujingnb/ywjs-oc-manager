# AICC 客服高并发过载治理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 使 AICC 公开消息可靠入队、按会话顺序异步调用 Hermes，并在上游过载与 Pod 扩缩容时可恢复、可观测。

**Architecture:** 公开 API 在同一 MySQL 事务内持久化访客消息与任务；Redis 仅作为 AICC 专属任务信号，扫库负责恢复。dispatcher 持有任务租约、会话串行约束与上游/组织/机器人额度；AICC Deployment 使用最低一副本的 HPA。

**Tech Stack:** Go、Gin、sqlc、MySQL、Redis ZSET、Kubernetes autoscaling/v2、Vue 3、Vitest、testify。

---

## 文件结构

- `internal/migrations/000033_aicc_message_tasks.{up,down}.sql`：任务状态、租约、重试和索引。
- `internal/store/queries/aicc.sql` 与 sqlc 生成物：任务读写、领取和恢复查询。
- `internal/service/aicc_dispatcher.{go,test.go}`：顺序调度、额度、退避与熔断。
- `internal/service/aicc_public_service.{go,test.go}`：同步调用改为可靠接收。
- `internal/api/handlers/public_aicc.{go,test.go}`、`dto.go`：202 接收与状态查询。
- `cmd/server/main.go`：AICC 专属队列、dispatcher、扫库和指标装配。
- `internal/integrations/k8sorch/*`：AICC HPA、滚动更新和优雅终止。
- `deploy/k8s/{local,prod}/manager-rbac.yaml`：HPA 操作权限。
- `web/src/{domain/aicc.ts,api/hooks/useAICC.ts,pages/aicc/PublicAICCChatPage.{vue,spec.ts},i18n/locales/{zh,en}/aicc.ts}`：异步状态 UI。

### Task 1: 新增可恢复的消息任务表

**Files:** Create `internal/migrations/000033_aicc_message_tasks.{up,down}.sql`; Modify `internal/migrations/migrations_test.go`, `internal/store/queries/aicc.sql`, `internal/store/sqlc/*`.

- [ ] **Step 1: 写失败迁移测试**

在 `migrations_test.go` 为新迁移写相邻中文注释，断言含 `aicc_message_tasks`、唯一 `message_id`、状态检查、`(status, run_after, id)` 及租约索引。

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/migrations -run Test -count=1`

Expected: FAIL，000033 尚不存在。

- [ ] **Step 3: 实现表和查询**

```sql
CREATE TABLE aicc_message_tasks (
  id CHAR(36) PRIMARY KEY, message_id CHAR(36) NOT NULL, session_id CHAR(36) NOT NULL,
  agent_id CHAR(36) NOT NULL, org_id CHAR(36) NOT NULL, app_id CHAR(36) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'queued', attempts INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 5, run_after DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  lease_token CHAR(36) NULL, lease_expires_at DATETIME(6) NULL, last_error VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT aicc_message_tasks_status_check CHECK (status IN ('queued','processing','retry_wait','completed','failed')),
  UNIQUE KEY uk_aicc_message_tasks_message (message_id), KEY idx_aicc_message_tasks_ready (status, run_after, id),
  KEY idx_aicc_message_tasks_lease (lease_expires_at, id)
);
```

在 `aicc.sql` 定义 `CreateAICCMessageTask`、`GetAICCMessageTaskByMessageID`、`ListReadyAICCMessageTasks`、`LeaseAICCMessageTask`、`CompleteAICCMessageTask`、`RetryAICCMessageTask`、`FailAICCMessageTask`、`RecoverExpiredAICCMessageTaskLeases`。`Lease` 必须在 SQL 中保证同一 session 不存在 `processing` 任务。运行 `make sqlc`，不手改生成物。

- [ ] **Step 4: 验证并提交**

Run: `make sqlc && go test ./internal/migrations ./internal/store/sqlc -count=1`

Expected: PASS。

Commit: `git add internal/migrations/000033_aicc_message_tasks.* internal/migrations/migrations_test.go internal/store/queries/aicc.sql internal/store/sqlc && git commit -m "feat(aicc): 增加客服消息持久化任务" -m "为公开客服消息增加可恢复的队列任务、租约和重试状态。"`

### Task 2: 原子接收公开消息

**Files:** Modify `internal/service/aicc_public_service.go`, `internal/service/aicc_types.go`, `internal/service/aicc_public_service_test.go`.

- [ ] **Step 1: 写失败 service 测试**

新增带中文注释的测试：首次发送只写 visitor message 与 `queued` task、不调用 `AICCHermesChat`；相同 `client_message_id` 返回同一任务；task 写失败时访客消息不提交；同会话第二条消息保留为 queued。

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/service -run 'TestAICCPublic.*(Queues|Idempotent|Atomic)' -count=1`

Expected: FAIL，现有 `SendMessage` 同步调用 `ChatAICC`。

- [ ] **Step 3: 实现最小状态响应**

```go
type AICCPublicMessageResult struct {
    MessageID string `json:"message_id"`
    Status string `json:"status"`
    Text string `json:"text,omitempty"`
    RetryAfterSeconds int32 `json:"retry_after_seconds,omitempty"`
}
```

在现有 `reserveAICCVisitorMessage` 事务内同时 `CreateAICCMessage` 与 `CreateAICCMessageTask`。删除 `SendMessage` 内即时 Hermes 调用和助手消息写入；幂等查询先返回 task，只有 `completed` 返回助手文本。提示词注入仍本地写 `completed` 回答，不入队。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/service -run 'TestAICCPublic' -count=1`

Expected: PASS。

Commit: `git add internal/service/aicc_public_service.go internal/service/aicc_types.go internal/service/aicc_public_service_test.go && git commit -m "feat(aicc): 将公开消息改为可靠入队"`

### Task 3: 编写 dispatcher、重试和熔断

**Files:** Create `internal/service/aicc_dispatcher.go`, `internal/service/aicc_dispatcher_test.go`; Modify `internal/service/aicc_public_chat.{go,test.go}`.

- [ ] **Step 1: 写失败 dispatcher 测试**

覆盖租约、同会话串行、成功后原子写助手消息、429/503/529/超时进入 `retry_wait`、确定性错误直接 `failed`、第 5 次失败终止、过期租约回收、连续 5 次过载熔断 30 秒与半开恢复。

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/service -run TestAICCDispatcher -count=1`

Expected: FAIL，dispatcher 不存在。

- [ ] **Step 3: 实现调度器**

定义 `AICCDispatcher`，注入 task store、`AICCHermesChat`、`AICCConcurrencyLimiter` 和可替换时钟。执行顺序固定为：领取 30 秒租约 → 获取 upstream/org/agent/session 四层令牌 → `buildAICCRuntimePrompt` → `ChatAICC` → 事务写 assistant message 与 `CompleteAICCMessageTask`。仅 HTTP 429/503/529、`context.DeadlineExceeded` 和网络超时重试，退避固定为 2、5、10、20、40 秒并加抖动。修改 `AICCPublicHermesChat`，保留可检查的上游状态/超时错误，不能把上游诊断转换成成功文本。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/service -run 'TestAICC(Dispatcher|PublicHermesChat)' -count=1`

Expected: PASS。

Commit: `git add internal/service/aicc_dispatcher.go internal/service/aicc_dispatcher_test.go internal/service/aicc_public_chat.go internal/service/aicc_public_chat_test.go && git commit -m "feat(aicc): 增加客服过载调度与熔断"`

### Task 4: 接入任务信号与状态 API

**Files:** Modify `cmd/server/main.go`, `internal/api/handlers/public_aicc.{go,test.go}`, `internal/api/handlers/dto.go`, `internal/api/router.go`.

- [ ] **Step 1: 写失败 API 与装配测试**

断言 POST `/messages` 返回 202 和 `{message_id,status:"queued"}`；GET `/messages/:messageId` 返回状态，completed 才有 text；跨 session 的消息 ID 返回未找到；server 为 AICC 使用独立 Redis key `ocm:aicc:message-tasks`。

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/api/handlers -run 'TestPublicAICC.*(Accepted|MessageStatus)' -count=1`

Expected: FAIL，当前接口同步返回 200。

- [ ] **Step 3: 实现**

POST 成功码改为 `http.StatusAccepted`；注册 `GET /api/v1/public/aicc/sessions/:sessionToken/messages/:messageId`。在 `main.go` 新建 `redis.NewRedisQueue(redis.Config{QueueKey: cfg.Redis.KeyPrefix + ":aicc:message-tasks"})`，每秒按“扫库入队、恢复过期租约、Reserve、Dispatch”运行；Redis 失败仅记录并在下一轮扫库恢复，MySQL 始终是真相源。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/api/handlers ./cmd/server -count=1 && make openapi-gen && make web-types-gen && make openapi-check`

Expected: PASS。

Commit: `git add cmd/server/main.go internal/api/handlers internal/api/router.go openapi/openapi.yaml web/src/api/generated.ts && git commit -m "feat(aicc): 提供客服消息异步状态接口"`

### Task 5: 为 AICC 运行时增加 HPA 与优雅缩容

**Files:** Modify `internal/integrations/k8sorch/{orchestrator.go,adapter.go,render.go,render_test.go,adapter_test.go,routing_test.go}` and `deploy/k8s/{local,prod}/manager-rbac.yaml`.

- [ ] **Step 1: 写失败编排测试**

断言 AICC `EnsureApp` 创建 `autoscaling/v2 HorizontalPodAutoscaler`，target 是 `app-<id>`，`minReplicas=1`、CPU 70%、内存 75%、scaleDown stabilization 600 秒；普通 app 无 HPA；`Delete` 删除 HPA。

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/integrations/k8sorch -run '(HPA|AICC)' -count=1`

Expected: FAIL，现有编排只创建 Deployment/Service/Secret。

- [ ] **Step 3: 实现 HPA 与更新策略**

新增 `RenderAICCHPA(spec, namespace)`，仅 AICC 的 adapter `EnsureApp` apply、`Delete` 清理。AICC Deployment 改 `RollingUpdate`，`maxUnavailable: 0`、`maxSurge: 1`、`terminationGracePeriodSeconds: 90`；普通 app 保持 `Recreate`。两个 RBAC 文件加入 `autoscaling` 的 `horizontalpodautoscalers` 的 get/list/watch/create/update/patch/delete。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/integrations/k8sorch -count=1`

Expected: PASS。

Commit: `git add internal/integrations/k8sorch deploy/k8s/local/manager-rbac.yaml deploy/k8s/prod/manager-rbac.yaml && git commit -m "feat(aicc): 为客服运行时启用自动扩缩容"`

### Task 6: 更新公开端异步体验

**Files:** Modify `web/src/domain/aicc.ts`, `web/src/api/hooks/useAICC.ts`, `web/src/pages/aicc/PublicAICCChatPage.{vue,spec.ts}`, `web/src/i18n/locales/{zh,en}/aicc.ts`.

- [ ] **Step 1: 写失败页面测试**

覆盖：202 后显示访客消息和“排队中”占位；轮询 completed 后只追加一次助手回答；retry_wait 显示重试；failed 显示可点击重试；刷新只恢复已完成消息，不重复提交。

- [ ] **Step 2: 验证失败**

Run: `cd web && npm test -- PublicAICCChatPage.spec.ts --run`

Expected: FAIL，页面依赖同步 `text`。

- [ ] **Step 3: 实现轮询与幂等重试**

扩展 domain 类型与 `sendAICCPublicMessage`，新增 `fetchAICCPublicMessageStatus`。页面保留发送时的 `client_message_id`，按 `retry_after_seconds`（缺省 2 秒）轮询，用户重试必须复用该 ID。加入中英文 queued、retrying、busy、retry 文案。

- [ ] **Step 4: 前端与真实浏览器验证并提交**

Run: `cd web && npm test -- PublicAICCChatPage.spec.ts --run && npm run build`

Expected: PASS。

在本地 k3d 用真实浏览器验证正常回答、`retry_wait`、失败提示、刷新恢复；不得用 curl 替代。

Commit: `git add web/src/domain/aicc.ts web/src/api/hooks/useAICC.ts web/src/pages/aicc/PublicAICCChatPage.vue web/src/pages/aicc/PublicAICCChatPage.spec.ts web/src/i18n/locales && git commit -m "feat(aicc): 展示客服排队与恢复状态"`

### Task 7: 指标、压力验证与交付

**Files:** Modify `cmd/server/main.go`, `docs/local-development.md`; Create `internal/service/aicc_dispatcher_integration_test.go`.

- [ ] **Step 1: 写指标和集成失败用例**

验证 queued/retry/failed/completed/circuit-open/lease-recovered 计数、等待时长、在途数；指标标签只能有 agent、org、upstream、result，绝不能有访客文本或 token。集成用例模拟并发、429、503、超时和 dispatcher 重启后恢复。

- [ ] **Step 2: 实现与验证**

注册 `aicc_message_tasks_total`、`aicc_message_queue_wait_seconds`、`aicc_upstream_circuit_state`、`aicc_dispatch_inflight`、`aicc_task_lease_recoveries_total`；文档说明本地模拟 429/503/超时与观察 HPA 的方式，不含生产凭证。

Run: `go test ./... && make openapi-check && cd web && npm test -- --run && npm run build`

Expected: PASS。

- [ ] **Step 3: 交付提交**

Run: `git status --short && git diff --check`

Commit: `git add cmd/server/main.go internal/service/aicc_dispatcher_integration_test.go docs/local-development.md && git commit -m "feat(aicc): 监控客服过载调度状态"`

只暂存计划内文件，保留已有无关未跟踪文件。

## 计划自检

- 可靠接收：Task 1–2；会话顺序、恢复、退避和熔断：Task 3–4；最低一热副本和优雅缩容：Task 5；访客状态：Task 6；监控与压力验收：Task 7。
- sqlc、OpenAPI 和 TypeScript 生成物均由项目命令生成；未手动编辑。
- 没有未决占位项或未定义的接口、状态与数据表。
