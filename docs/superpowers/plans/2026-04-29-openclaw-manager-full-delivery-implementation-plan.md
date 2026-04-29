# OpenClaw Manager Full Delivery Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete OpenClaw Manager product described by the confirmed design, with dockerized local development, full tests, Chinese comments, browser-verified pages, OpenClaw runtime image build/distribution, Ollama debugging, and new-api browser setup.

**Architecture:** Implement a Go/Gin manager API with PostgreSQL, Redis, jobs, worker, scheduler, agent-backed runtime operations, and a Vue 3/Naive UI web console. Use Docker Compose for all local services; Go runs under `air` inside `manager-api`, web runs under Vite dev server inside `manager-web`, all persistent data uses local bind mounts. Build the OpenClaw runtime image from this repo and distribute it to runtime nodes by digest/hash before app container startup.

**Tech Stack:** Go, Gin, pgx, sqlc, golang-migrate, go-redis, Docker SDK, Vue 3, Vite, TypeScript, Naive UI, Pinia, TanStack Query, OpenAPI, Docker Compose, Air, PostgreSQL, Redis, new-api, Ollama.

---

## Scope Check

This is a master implementation plan for the full product. It is intentionally split into chunks matching the confirmed delivery stages. Each chunk must produce working, tested software and end with a commit. Do not skip tests, Chinese comments, browser verification, or compose validation.

Primary references:

- `docs/openclaw-manager-design.md`
- `docs/openclaw-manager-technical-design.md`
- `docs/superpowers/specs/2026-04-29-openclaw-manager-full-delivery-design.md`

Global rules:

- Do not run backend or frontend directly on the host for local debugging. Use Docker Compose.
- Do not define Docker named volumes. Use local bind mounts only.
- Add complete unit tests with each implementation.
- Public Go types, public methods, DTO fields, service methods, state machines, job handlers, adapters, permissions, compensation logic, security boundaries, and complex transactions must have detailed Chinese comments.
- Every browser-facing chunk must be verified with chrome-devtools MCP.
- If a verification fails, fix and rerun before continuing.

---

## File Structure Map

Create and evolve this structure:

```text
.
├── Makefile
├── docker-compose.yml
├── .env.example
├── .air.toml
├── config/
│   └── config.yaml.example
├── cmd/
│   ├── server/main.go
│   └── migrate/main.go
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── domain/
│   ├── files/
│   ├── integrations/
│   │   ├── agent/
│   │   ├── channel/
│   │   ├── newapi/
│   │   ├── openclaw/
│   │   └── runtime/
│   ├── redis/
│   ├── scheduler/
│   ├── service/
│   ├── store/
│   │   ├── queries/
│   │   └── sqlc/
│   └── worker/
├── migrations/
├── openapi/
│   └── openapi.yaml
├── runtime/
│   ├── agent/
│   └── openclaw/
├── scripts/
│   ├── check-compose-bind-mounts.sh
│   ├── debug-newapi.sh
│   ├── debug-ollama.sh
│   └── verify-openclaw-runtime.sh
└── web/
```

---

## Chunk 1: Engineering Foundation And Dockerized Dev

### Task 1.1: Create Backend Skeleton, Config, Health, And Tests

**Files:**
- Create: `go.mod`
- Create: `cmd/server/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/loader.go`
- Create: `internal/config/loader_test.go`
- Create: `internal/api/router.go`
- Create: `internal/api/handlers/health.go`
- Create: `internal/api/handlers/health_test.go`
- Create: `config/config.yaml.example`
- Create: `.air.toml`

- [ ] Write failing tests for config env expansion, required field validation, and `/healthz`.
- [ ] Run: `docker compose run --rm manager-api go test ./internal/config ./internal/api/handlers`
  Expected: FAIL before implementation.
- [ ] Implement config loader with Chinese comments on public types and validation behavior.
- [ ] Implement Gin router and health handler.
- [ ] Run: `docker compose run --rm manager-api go test ./...`
  Expected: PASS.
- [ ] Run: `docker compose run --rm manager-api go vet ./...`
  Expected: PASS.
- [ ] Commit: `feat: add backend foundation and health check`.

### Task 1.2: Create Compose, Makefile, Bind Mount Check, And Local Config

**Files:**
- Create: `docker-compose.yml`
- Create: `Makefile`
- Create: `.env.example`
- Modify: `.gitignore`
- Create: `scripts/check-compose-bind-mounts.sh`
- Test: `scripts/check-compose-bind-mounts.sh`

- [ ] Write `scripts/check-compose-bind-mounts.sh` to fail if compose contains top-level `volumes:` or service mounts without local bind paths.
- [ ] Add Make targets: `dev-up`, `dev-down`, `test`, `vet`, `build`, `migrate-up`, `migrate-down`, `check-compose`, `logs`.
- [ ] Add compose services: `manager-postgres`, `redis`, `new-api-postgres`, `new-api`, `ollama`, `manager-api`, `manager-web`, `oc-runtime-agent`.
- [ ] Ensure every persistent service mount uses `./data/...:/container/path`.
- [ ] Run: `make check-compose`
  Expected: PASS.
- [ ] Run: `make dev-up`
  Expected: all foundation containers start or build.
- [ ] Run: `make dev-down`
  Expected: all containers stop.
- [ ] Commit: `chore: add dockerized local development`.

### Task 1.3: Create Migrations, OpenAPI Baseline, And Migration Tests

**Files:**
- Create: `cmd/migrate/main.go`
- Create: `migrations/000001_init.up.sql`
- Create: `migrations/000001_init.down.sql`
- Create: `openapi/openapi.yaml`
- Modify: `Makefile`

- [ ] Add migration command with Chinese comments explaining production does not auto-migrate.
- [ ] Add empty-but-valid initial schema for migration infrastructure.
- [ ] Add OpenAPI baseline with `/healthz` and `/api/v1/auth/me` initial contract.
- [ ] Run: `make dev-up`
- [ ] Run: `make migrate-up`
  Expected: migration succeeds.
- [ ] Run: `make migrate-down`
  Expected: rollback succeeds.
- [ ] Run: `make build`
  Expected: backend and frontend build targets pass once frontend skeleton exists.
- [ ] Commit: `chore: add migrations and openapi baseline`.

### Task 1.4: Create Frontend Skeleton And Browser-Verified Layout Shell

**Files:**
- Create: `web/package.json`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json`
- Create: `web/src/main.ts`
- Create: `web/src/app/router.ts`
- Create: `web/src/app/query-client.ts`
- Create: `web/src/layouts/AuthLayout.vue`
- Create: `web/src/layouts/DashboardLayout.vue`
- Create: `web/src/pages/login/LoginPage.vue`
- Create: `web/src/pages/dashboard/DashboardHome.vue`
- Create: `web/src/styles/base.css`
- Create: `web/src/domain/status.ts`
- Test: `web/src/domain/status.test.ts`

- [ ] Write a frontend unit test for status label formatting.
- [ ] Implement the confirmed ops-console layout: fixed left nav, top status bar, dense main content.
- [ ] Add Make targets: `web-typecheck`, `web-test`, `web-build`.
- [ ] Run: `docker compose run --rm manager-web npm test -- --run`
  Expected: PASS.
- [ ] Run: `docker compose run --rm manager-web npm run typecheck`
  Expected: PASS.
- [ ] Run: `docker compose run --rm manager-web npm run build`
  Expected: PASS.
- [ ] Run: `make dev-up`.
- [ ] Use chrome-devtools MCP to open manager web URL.
  Verify: page loads, left nav exists, top status bar exists, text does not overlap.
- [ ] Commit: `feat: add web console shell`.

### Task 1.5: Build OpenClaw Runtime Image And Debug Ollama/new-api

**Files:**
- Create: `runtime/openclaw/Dockerfile`
- Create: `runtime/openclaw/healthcheck.sh`
- Create: `runtime/openclaw/verify-install.sh`
- Create: `runtime/agent/Dockerfile`
- Create: `runtime/agent/main.go`
- Create: `scripts/verify-openclaw-runtime.sh`
- Create: `scripts/debug-ollama.sh`
- Create: `scripts/debug-newapi.sh`
- Modify: `Makefile`
- Modify: `docker-compose.yml`

- [ ] Add `make build-openclaw-runtime`.
- [ ] Add OpenClaw runtime Dockerfile that installs OpenClaw, WeChat plugin, dependencies, and verification scripts.
- [ ] Add minimal `oc-runtime-agent` health/fake API container for phase 1.
- [ ] Add `make debug-ollama` to verify API, list models, pull the configured small model, and run one minimal call.
- [ ] Add `make debug-newapi` to verify HTTP, health/admin access, database connectivity, and Ollama channel readiness.
- [ ] Run: `make build-openclaw-runtime`
  Expected: image builds.
- [ ] Run: `scripts/verify-openclaw-runtime.sh`
  Expected: OpenClaw and WeChat plugin install checks pass.
- [ ] Run: `make debug-ollama`
  Expected: Ollama is reachable and small model call succeeds.
- [ ] Open new-api in browser with chrome-devtools MCP.
  Verify: management page loads, Ollama channel is configured and usable.
- [ ] Run: `make debug-newapi`
  Expected: service and DB checks pass.
- [ ] Commit: `chore: add runtime image and external service debug`.

---

## Chunk 2: Auth, Organizations, Permissions, And Audit

### Task 2.1: Add Schema And sqlc Queries

**Files:**
- Create: `migrations/000002_identity.up.sql`
- Create: `migrations/000002_identity.down.sql`
- Create: `internal/store/queries/users.sql`
- Create: `internal/store/queries/organizations.sql`
- Create: `internal/store/queries/audit_logs.sql`
- Create: `internal/store/queries/recharge_records.sql`
- Create: `internal/store/queries/refresh_tokens.sql`
- Create: `sqlc.yaml`

- [ ] Write migration for users, organizations, personas, recharge records, audit logs, refresh tokens.
- [ ] Add unique and lookup indexes from the technical design.
- [ ] Run migration up/down in containers.
- [ ] Generate sqlc code.
- [ ] Commit: `feat: add identity schema`.

### Task 2.2: Implement Auth And Permissions With Unit Tests

**Files:**
- Create: `internal/auth/password.go`
- Create: `internal/auth/password_test.go`
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/jwt_test.go`
- Create: `internal/auth/refresh_token.go`
- Create: `internal/domain/permissions.go`
- Create: `internal/domain/permissions_test.go`
- Create: `internal/service/auth_service.go`
- Create: `internal/service/auth_service_test.go`
- Create: `internal/api/middleware/auth.go`
- Create: `internal/api/handlers/auth.go`

- [ ] Write tests for Argon2id password verification, JWT validation, refresh token revoke, disabled org/user denial, and role boundaries.
- [ ] Implement auth with detailed Chinese comments around token security and disabled-user behavior.
- [ ] Run: `docker compose run --rm manager-api go test ./internal/auth ./internal/domain ./internal/service`
- [ ] Commit: `feat: add authentication and permission checks`.

### Task 2.3: Implement Organization, Member Basics, Recharge, And Audit Pages

**Files:**
- Create: `internal/service/organization_service.go`
- Create: `internal/service/member_service.go`
- Create: `internal/service/audit_service.go`
- Create: `internal/api/handlers/organizations.go`
- Create: `internal/api/handlers/members.go`
- Create: `internal/api/handlers/audit.go`
- Create: `web/src/pages/platform/OrganizationsPage.vue`
- Create: `web/src/pages/org/MembersPage.vue`
- Create: `web/src/pages/audit/AuditLogsPage.vue`
- Create: `web/src/api/hooks/useOrganizations.ts`
- Create: `web/src/api/hooks/useMembers.ts`
- Create: `web/src/api/hooks/useAuditLogs.ts`

- [ ] Write service tests for organization CRUD, disable/enable, member visibility, recharge audit.
- [ ] Implement APIs and update OpenAPI.
- [ ] Add frontend table pages using confirmed list-page template.
- [ ] Run backend tests, web tests, typecheck, build.
- [ ] Use chrome-devtools MCP to verify login shell, organization list, member list, audit list.
- [ ] Commit: `feat: add organization and member management basics`.

---

## Chunk 3: Runtime Node, Agent, And Image Distribution

### Task 3.1: Runtime Node Schema, Services, And Agent Registration

**Files:**
- Create: `migrations/000003_runtime_nodes.up.sql`
- Create: `migrations/000003_runtime_nodes.down.sql`
- Create: `internal/store/queries/runtime_nodes.sql`
- Create: `internal/service/runtime_node_service.go`
- Create: `internal/service/runtime_node_service_test.go`
- Create: `internal/api/handlers/runtime_nodes.go`
- Create: `internal/integrations/agent/endpoints.go`

- [ ] Test bootstrap token hash, expiry, single consumption, concurrent registration, rotate, heartbeat recovery.
- [ ] Implement runtime node CRUD and agent register/heartbeat endpoints.
- [ ] Add Chinese comments explaining bootstrap token and agent token security.
- [ ] Run: `docker compose run --rm manager-api go test ./internal/service ./internal/integrations/agent`.
- [ ] Commit: `feat: add runtime node registration and heartbeat`.

### Task 3.2: Agent File API, Runtime Adapter Interfaces, And Image Distribution

**Files:**
- Create: `internal/integrations/agent/file_client.go`
- Create: `internal/integrations/agent/file_client_test.go`
- Create: `internal/integrations/runtime/adapter.go`
- Create: `internal/integrations/runtime/agent_backed.go`
- Create: `internal/integrations/runtime/agent_backed_test.go`
- Create: `internal/service/image_distribution_service.go`
- Create: `internal/service/image_distribution_service_test.go`
- Modify: `runtime/agent/main.go`

- [ ] Test fake agent image digest check, missing image, mismatched hash, tar upload, docker load success/failure.
- [ ] Implement client interfaces with Chinese comments documenting TLS and Bearer auth.
- [ ] Implement ImageDistributionService: skip same digest, upload/load on missing or mismatch, retryable errors on failure.
- [ ] Run tests.
- [ ] Commit: `feat: add agent-backed runtime and image distribution`.

### Task 3.3: Runtime Node Frontend

**Files:**
- Create: `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`
- Create: `web/src/pages/runtime-nodes/RuntimeNodeDetailPage.vue`
- Create: `web/src/api/hooks/useRuntimeNodes.ts`
- Create: `web/src/components/RuntimeStatusTag.vue`

- [ ] Add component tests for status tag and rotate bootstrap UI state.
- [ ] Implement list/detail using confirmed list/detail templates.
- [ ] Use chrome-devtools MCP to verify create node, view node, rotate bootstrap, status display.
- [ ] Commit: `feat: add runtime node console pages`.

---

## Chunk 4: Jobs, Worker, Scheduler, And State Machines

### Task 4.1: Jobs Schema And Queue

**Files:**
- Create: `migrations/000004_jobs.up.sql`
- Create: `migrations/000004_jobs.down.sql`
- Create: `internal/store/queries/jobs.sql`
- Create: `internal/redis/queue.go`
- Create: `internal/redis/queue_test.go`
- Create: `internal/domain/job_state_machine.go`
- Create: `internal/domain/job_state_machine_test.go`

- [ ] Test pending/running/succeeded/failed/canceled transitions, delayed queue behavior, Redis loss recovery premise.
- [ ] Implement queue and state machine.
- [ ] Commit: `feat: add job persistence and redis queue`.

### Task 4.2: Worker, Scheduler, Reconciler

**Files:**
- Create: `internal/worker/worker.go`
- Create: `internal/worker/worker_test.go`
- Create: `internal/worker/handlers/registry.go`
- Create: `internal/scheduler/scheduler.go`
- Create: `internal/scheduler/scheduler_test.go`
- Create: `internal/api/handlers/jobs.go`
- Create: `web/src/components/JobProgressPanel.vue`

- [ ] Test job lock, max attempts, exponential backoff, failure writeback, pending job requeue.
- [ ] Implement worker and scheduler with Chinese comments on PostgreSQL as fact source.
- [ ] Add job progress UI.
- [ ] Verify job page/panel in browser.
- [ ] Commit: `feat: add worker scheduler and job progress`.

---

## Chunk 5: Member-Created App Initialization

### Task 5.1: Apps And Channel Bindings Schema

**Files:**
- Create: `migrations/000005_apps.up.sql`
- Create: `migrations/000005_apps.down.sql`
- Create: `internal/store/queries/apps.sql`
- Create: `internal/store/queries/channel_bindings.sql`
- Create: `internal/domain/app_state_machine.go`
- Create: `internal/domain/app_state_machine_test.go`

- [ ] Test app state transitions and api_key status independence.
- [ ] Add unique active owner app index.
- [ ] Commit: `feat: add app and channel schema`.

### Task 5.2: Create Member With App Transaction

**Files:**
- Modify: `internal/service/member_service.go`
- Create: `internal/service/app_service.go`
- Create: `internal/service/app_service_test.go`
- Modify: `internal/api/handlers/members.go`
- Create: `web/src/pages/org/CreateMemberPage.vue`

- [ ] Test transaction success and rollback on user/app/binding/audit/job failure.
- [ ] Implement `POST /members` to create user, app, channel binding, audit, and `app_initialize` job.
- [ ] Implement single-page grouped form.
- [ ] Verify in browser: form validation, submit, redirect to app detail with job state.
- [ ] Commit: `feat: create members with linked apps`.

### Task 5.3: new-api Adapter, Prompt Rendering, And app_initialize Handler

**Files:**
- Create: `internal/integrations/newapi/client.go`
- Create: `internal/integrations/newapi/client_test.go`
- Create: `internal/integrations/openclaw/prompt.go`
- Create: `internal/integrations/openclaw/prompt_test.go`
- Create: `internal/worker/handlers/app_initialize.go`
- Create: `internal/worker/handlers/app_initialize_test.go`

- [ ] Test new-api error mapping with fake HTTP server.
- [ ] Test prompt variables, unreplaced placeholder detection, and platform/org/app order.
- [ ] Test app_initialize idempotency: api_key exists, directories exist, image digest same, container exists.
- [ ] Implement initialization: active node check, image distribution, api_key create, dirs, knowledge sync, prompt, container create/start, health, status update.
- [ ] Commit: `feat: initialize linked OpenClaw apps`.

---

## Chunk 6: Channel Binding And OpenClaw Integration

### Task 6.1: Channel Adapter Registry And WeChat

**Files:**
- Create: `internal/integrations/channel/adapter.go`
- Create: `internal/integrations/channel/registry.go`
- Create: `internal/integrations/channel/registry_test.go`
- Create: `internal/integrations/channel/wechat.go`
- Create: `internal/integrations/channel/wechat_test.go`
- Create: `internal/integrations/openclaw/parser.go`
- Create: `internal/integrations/openclaw/parser_test.go`

- [ ] Test registry routing, QR challenge parsing, expired/failed outputs, unparseable output.
- [ ] Implement JSON wrapper fallback requirement if CLI output cannot be stable.
- [ ] Commit: `feat: add channel adapter and wechat parsing`.

### Task 6.2: Channel APIs And Frontend

**Files:**
- Create: `internal/service/channel_service.go`
- Create: `internal/service/channel_service_test.go`
- Create: `internal/api/handlers/channels.go`
- Create: `web/src/pages/apps/AppChannelsTab.vue`
- Create: `web/src/components/AuthChallengeRenderer.vue`

- [ ] Test login, polling, retry, unbind, failure state, permission denial.
- [ ] Implement APIs and QR renderer.
- [ ] Verify with chrome-devtools MCP: QR display, retry, expired state, error visibility.
- [ ] Commit: `feat: add channel binding workflow`.

---

## Chunk 7: Knowledge And Workspace

### Task 7.1: Safe Path And Knowledge Services

**Files:**
- Create: `internal/files/safe_path.go`
- Create: `internal/files/safe_path_test.go`
- Create: `internal/files/knowledge_master.go`
- Create: `internal/files/knowledge_master_test.go`
- Create: `internal/service/knowledge_service.go`
- Create: `internal/service/knowledge_service_test.go`
- Create: `internal/api/handlers/knowledge.go`

- [ ] Test absolute path, `..`, URL encoding, symlink, socket/device, max size.
- [ ] Implement manager master copy and org/app upload/delete/list.
- [ ] Implement app sync rollback and org async sync jobs.
- [ ] Commit: `feat: add knowledge master and sync APIs`.

### Task 7.2: Workspace Proxy And File UI

**Files:**
- Create: `internal/service/workspace_service.go`
- Create: `internal/service/workspace_service_test.go`
- Create: `internal/api/handlers/workspace.go`
- Create: `web/src/pages/apps/AppWorkspaceTab.vue`
- Create: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Create: `web/src/pages/apps/AppKnowledgeTab.vue`

- [ ] Test list/download/archive permission, path safety, size limits, audit.
- [ ] Implement read-only workspace proxy through AgentFileClient.
- [ ] Implement read-only file manager UI with breadcrumbs and download actions.
- [ ] Verify in browser: upload/delete knowledge, sync status, workspace breadcrumbs, file download/archive actions.
- [ ] Commit: `feat: add knowledge and workspace pages`.

---

## Chunk 8: Usage, Runtime Ops, And Complete Console

### Task 8.1: Usage And Runtime Operation APIs

**Files:**
- Create: `internal/service/usage_service.go`
- Create: `internal/service/usage_service_test.go`
- Create: `internal/service/runtime_operation_service.go`
- Create: `internal/service/runtime_operation_service_test.go`
- Create: `internal/api/handlers/usage.go`
- Create: `internal/api/handlers/app_runtime.go`

- [ ] Test usage permissions and new-api failures.
- [ ] Test start/stop/restart job creation, high-risk audit, disabled account behavior.
- [ ] Implement APIs.
- [ ] Commit: `feat: add usage and runtime operations`.

### Task 8.2: Complete Role Consoles

**Files:**
- Create/Modify: `web/src/pages/platform/*`
- Create/Modify: `web/src/pages/org/*`
- Create/Modify: `web/src/pages/apps/*`
- Create/Modify: `web/src/pages/usage/*`
- Create/Modify: `web/src/components/AppStatusTag.vue`
- Create/Modify: `web/src/components/ConfirmActionModal.vue`
- Create/Modify: `web/src/components/DataTableToolbar.vue`

- [ ] Add component tests for status tags, confirm modal, toolbar, role menu visibility.
- [ ] Implement platform pages: overview, orgs, recharge, apps, runtime nodes, usage, admins, audit.
- [ ] Implement org pages: overview, members, apps, persona, knowledge, usage, audit.
- [ ] Implement member pages: overview, my app, channels, knowledge, org knowledge read-only, workspace, usage, settings.
- [ ] Run web tests, typecheck, build.
- [ ] Use chrome-devtools MCP to verify all critical pages, form validation, buttons, modals, async refresh, no text overlap.
- [ ] Commit: `feat: complete role-based management console`.

---

## Chunk 9: Final Validation, Documentation, And Release Readiness

### Task 9.1: End-To-End Local Verification

**Files:**
- Create: `docs/local-development.md`
- Create: `docs/verification-report.md`
- Modify: `Makefile`
- Modify: `README.md`

- [ ] Run: `make check-compose`
- [ ] Run: `make build-openclaw-runtime`
- [ ] Run: `make dev-up`
- [ ] Run: `make migrate-up`
- [ ] Run: `make debug-ollama`
- [ ] Use browser to configure/verify new-api Ollama channel.
- [ ] Run: `make debug-newapi`
- [ ] Run: `make test`
- [ ] Run: `make vet`
- [ ] Run: `make web-test`
- [ ] Run: `make web-typecheck`
- [ ] Run: `make web-build`
- [ ] Run critical E2E manually or with Playwright: login, create organization, create node, agent register, create member+app, initialize, bind channel, knowledge, workspace, runtime operations, delete.
- [ ] Document exact commands, outcomes, screenshots or DOM snapshot notes in `docs/verification-report.md`.
- [ ] Commit: `docs: add local development and verification report`.

### Task 9.2: Final Self-Review

**Files:**
- Modify: `docs/verification-report.md`

- [ ] Search for unfinished markers in code and docs: `rg -n "TODO|TBD|待定|暂不明确|panic\\(|console\\.log"`.
- [ ] Check all public Go symbols have useful Chinese comments: `golint` equivalent if available, plus manual spot check.
- [ ] Check no Docker named volumes: `make check-compose`.
- [ ] Check OpenAPI client generation works.
- [ ] Check generated code is not manually edited.
- [ ] Check git status is clean after final commit.
- [ ] Commit: `chore: final readiness cleanup`.

---

## Execution Notes

- Use frequent commits exactly at task boundaries.
- If a task reveals a missing design decision, stop and update the spec before implementing.
- Do not weaken test or browser verification requirements to move forward faster.
- If new-api management APIs do not support a required operation, document the exact missing API, implement the agreed fallback, and add tests around the fallback.
- If OpenClaw CLI output cannot be parsed reliably, add a JSON wrapper inside `runtime/openclaw` and make tests target the wrapper contract.

Plan complete.
