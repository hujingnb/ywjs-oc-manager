# Runtime Agent Auto Enroll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Runtime agent 启动后自动注册到 manager，管理员后台不再创建节点或轮换 bootstrap。

**Architecture:** manager 通过共享 `runtime.enrollment_secret` 接收 `POST /agent/enroll`，按 agent 持久化 `agent_id` 幂等创建或刷新 `runtime_nodes`。agent 在 `state_dir` 保存 `agent-id`、`node-id`、`agent-token`，心跳 401 时自动重新 enroll；manager 周期主动探测 agent docker/file 端口并维护 `degraded` 状态。

**Tech Stack:** Go、Gin、pgx/sqlc、PostgreSQL migrations、Vue 3、Naive UI、OpenAPI codegen。

---

### Task 1: 数据模型与 sqlc

**Files:**
- Create: `internal/migrations/000012_runtime_nodes_auto_enroll.up.sql`
- Create: `internal/migrations/000012_runtime_nodes_auto_enroll.down.sql`
- Modify: `sqlc.yaml`
- Modify: `internal/store/queries/runtime_nodes.sql`
- Generate: `internal/store/sqlc/*`

- [ ] Add runtime node `agent_id` and probe columns, remove bootstrap columns, and allow `degraded`.
- [ ] Add sqlc queries for enroll insert/update, lookup by `agent_id`, heartbeat status-preserving update, and probe result updates.
- [ ] Run `make sqlc-generate`.

### Task 2: manager service/API/config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/domain/enums.go`
- Modify: `internal/service/runtime_node_service.go`
- Modify: `internal/api/handlers/agent.go`
- Modify: `internal/api/handlers/runtime_nodes.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] Require `runtime.enrollment_secret`, default probe settings, and add redaction.
- [ ] Replace old `RegisterAgent` with `EnrollAgent`; delete `CreateNode` and `RotateBootstrap`.
- [ ] Add `POST /api/v1/agent/enroll` with constant-time bearer validation.
- [ ] Keep only list/get/patch/disable/enable admin node endpoints.

### Task 3: manager probe reconciler

**Files:**
- Create: `internal/integrations/agent/probe.go`
- Create: `internal/service/probe_reconciler.go`
- Modify: `cmd/server/main.go`

- [ ] Probe docker `_ping` and file `/v1/files/ping` using the node CA and cached token.
- [ ] Update probe timestamps/streaks and transition `active` ↔ `degraded`.
- [ ] Do not mark apps error for `degraded`.

### Task 4: runtime agent self-enroll

**Files:**
- Create: `runtime/agent/state.go`
- Create: `runtime/agent/enroll.go`
- Modify: `runtime/agent/config/config.go`
- Modify: `runtime/agent/config/loader.go`
- Modify: `runtime/agent/heartbeat.go`
- Modify: `runtime/agent/main.go`

- [ ] Persist `agent-id`, `node-id`, and `agent-token` in `state_dir` with 0600 files.
- [ ] Enroll with `manager.enrollment_secret`; retry with capped exponential backoff.
- [ ] Read heartbeat token from state; re-enroll on 401.
- [ ] Protect `/v1/files/ping` with bearer auth.

### Task 5: frontend and generated API

**Files:**
- Modify: `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`
- Modify: `web/src/api/hooks/useRuntimeNodes.ts`
- Modify: `web/src/domain/status.ts`
- Generate: `openapi/openapi.yaml`
- Generate: `web/src/api/generated.ts`

- [ ] Remove create and rotate bootstrap UI/actions.
- [ ] Show automatic enrollment/probe information and support `degraded`.
- [ ] Run `make openapi-gen` and `make web-types-gen`.

### Task 6: docs and verification

**Files:**
- Modify: `config/manager.example.yaml`
- Modify: `config/agent.example.yaml`
- Modify: `docs/configuration.md`
- Modify: `docs/architecture.md`
- Modify: `docs/user-manual.md`
- Modify: `deploy/README.md`
- Create: `docs/runtime-agent-auto-enroll-principles.md`

- [ ] Document agent deployment steps for the new flow.
- [ ] Document auto-enroll and active probe principles.
- [ ] Run `go test ./...`, `npm test -- --run`, `npm run typecheck`, `make openapi-check`, and fix failures.
