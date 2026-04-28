# OpenClaw Manager Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working OpenClaw Manager implementation from the approved product and technical design documents.

**Architecture:** A Go/Gin backend exposes REST APIs, runs in-process workers and schedulers, stores business state in PostgreSQL, and uses Redis for job queueing, short-lived state, and locks. A Vue 3/Vite/TypeScript frontend uses Naive UI, OpenAPI-generated clients, TanStack Query, and Pinia. Local development is managed by docker compose with host-directory bind mounts only.

**Tech Stack:** Go, Gin, pgx, sqlc, golang-migrate, go-redis, Docker SDK, PostgreSQL, Redis, Vue 3, Vite, TypeScript, Naive UI, TanStack Query, Pinia, OpenAPI, docker compose.

---

## Source Documents

- Product requirements: `docs/openclaw-manager-design.md`
- Technical design: `docs/openclaw-manager-technical-design.md`

## Execution Rules

- Every task must finish with verification.
- Every backend change must include relevant tests before or with implementation.
- Every UI page or interaction change must be verified through `chrome-devtools` MCP in a real browser.
- Container persistence must use host-directory bind mounts under `./data/...`; do not add Docker named volumes.
- Keep comments in Chinese for public types, core services, state machines, job handlers, adapters, complex transactions, compensation logic, and external assumptions.
- Commit after each completed task or small coherent group of tasks.
- Do not implement later chunks until the prior chunk's verification is passing.

## File Structure To Create

```text
.
├── docker-compose.yml
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
├── web/
│   ├── package.json
│   ├── vite.config.ts
│   └── src/
└── docs/
```

## Chunk 1: Repository Foundation And Local Runtime

### Task 1: Scaffold Go Module And Tooling

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `cmd/server/main.go`
- Create: `cmd/migrate/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/loader.go`
- Create: `internal/config/loader_test.go`

- [ ] **Step 1: Initialize Go module**

Run:

```bash
go mod init oc-manager
```

Expected: `go.mod` exists with module `oc-manager`.

- [ ] **Step 2: Add backend dependencies**

Run:

```bash
go get github.com/gin-gonic/gin github.com/jackc/pgx/v5 github.com/redis/go-redis/v9 github.com/golang-jwt/jwt/v5 github.com/google/uuid golang.org/x/crypto/argon2 gopkg.in/yaml.v3
```

Expected: dependencies appear in `go.mod`.

- [ ] **Step 3: Write failing config loader tests**

Create `internal/config/loader_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExpandsEnvironmentVariables(t *testing.T) {
	t.Setenv("NEWAPI_ADMIN_TOKEN", "secret-token")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
app:
  env: test
  http_addr: ":8080"
  data_root: "/tmp/ocm"
database:
  url: "postgres://ocm:ocm@localhost:5432/ocm?sslmode=disable"
redis:
  addr: "localhost:6379"
  password: "123456"
newapi:
  base_url: "http://localhost:3000"
  admin_token: "${NEWAPI_ADMIN_TOKEN}"
runtime:
  docker_host: "unix:///var/run/docker.sock"
  openclaw_image: "openclaw-runtime:dev"
`), 0600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NewAPI.AdminToken != "secret-token" {
		t.Fatalf("expected expanded token, got %q", cfg.NewAPI.AdminToken)
	}
}

func TestLoadRejectsMissingRequiredValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`app: {env: test}`), 0600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run:

```bash
go test ./internal/config
```

Expected: FAIL because `Load` is not implemented.

- [ ] **Step 5: Implement config structs and loader**

Create `internal/config/config.go` and `internal/config/loader.go` with Chinese comments on exported types and functions. Include YAML parsing, `${ENV}` expansion using `os.ExpandEnv`, and validation for database URL, Redis address, new-api base URL/token, Docker host, OpenClaw image, and data root.

- [ ] **Step 6: Run config tests**

Run:

```bash
go test ./internal/config
```

Expected: PASS.

- [ ] **Step 7: Add minimal server and migrate commands**

`cmd/server/main.go` should load config path from `--config`, print or log startup metadata, and exit cleanly. `cmd/migrate/main.go` can be a placeholder command that loads config and prints intended migration action until migrations are implemented.

- [ ] **Step 8: Verify build**

Run:

```bash
go build ./...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum cmd internal/config
git commit -m "chore: scaffold Go backend foundation"
```

### Task 2: Add Docker Compose Local Environment

**Files:**
- Create: `docker-compose.yml`
- Create: `config/config.yaml.example`
- Modify: `.gitignore`

- [ ] **Step 1: Write compose file with bind mounts only**

Create `docker-compose.yml` with services:

- `manager-postgres`
- `redis`
- `new-api-postgres`
- `new-api`
- `ollama`

Use only host-directory bind mounts:

```yaml
volumes:
  - ./data/manager-postgres:/var/lib/postgresql/data
```

Do not define top-level named `volumes:`.

- [ ] **Step 2: Add config example**

Create `config/config.yaml.example` matching the technical design. Redis must point to `redis:6379` in compose and local examples must use `./data/manager`.

- [ ] **Step 3: Ensure data directory is ignored**

Confirm `.gitignore` contains:

```gitignore
data/
```

- [ ] **Step 4: Validate compose config**

Run:

```bash
docker compose config
```

Expected: config renders successfully and has no top-level named `volumes`.

- [ ] **Step 5: Start infrastructure**

Run:

```bash
docker compose up -d manager-postgres redis new-api-postgres new-api ollama
```

Expected: all services start.

- [ ] **Step 6: Verify service health**

Run:

```bash
docker compose ps
curl -s http://localhost:3000/api/status
curl -s http://localhost:11434/api/tags
```

Expected: new-api status returns success or service status JSON; Ollama tags returns JSON.

- [ ] **Step 7: Pull small Ollama model**

Run:

```bash
docker exec ollama ollama pull qwen2.5:0.5b
docker exec ollama ollama list
```

Expected: `qwen2.5:0.5b` appears in the list.

- [ ] **Step 8: Commit**

```bash
git add docker-compose.yml config/config.yaml.example .gitignore
git commit -m "chore: add local docker compose runtime"
```

## Chunk 2: Database, sqlc, And Domain Foundation

### Task 3: Add Migrations And sqlc Setup

**Files:**
- Create: `migrations/000001_init.up.sql`
- Create: `migrations/000001_init.down.sql`
- Create: `sqlc.yaml`
- Create: `internal/store/queries/*.sql`
- Create: `internal/store/tx.go`
- Generated: `internal/store/sqlc/*`

- [ ] **Step 1: Write initial migration**

Implement tables from the technical design:

- `organizations`
- `users`
- `organization_personas`
- `member_budgets`
- `runtime_nodes`
- `apps`
- `wechat_bindings`
- `knowledge_files`
- `recharge_records`
- `usage_snapshots`
- `jobs`
- `audit_logs`
- `refresh_tokens`

Use UUID primary keys, timestamps, status text fields, and documented indexes.

- [ ] **Step 2: Write down migration**

Drop tables in reverse dependency order.

- [ ] **Step 3: Add sqlc config and base queries**

Include queries for user lookup, organization CRUD, app lookup, job create/update, audit insert, and refresh token operations.

- [ ] **Step 4: Generate sqlc code**

Run:

```bash
sqlc generate
```

Expected: generated code appears under `internal/store/sqlc`.

- [ ] **Step 5: Verify migration up**

Run against compose PostgreSQL:

```bash
go run ./cmd/migrate --config config/config.yaml.example up
```

Expected: migration succeeds.

- [ ] **Step 6: Verify migration down/up in development**

Run:

```bash
go run ./cmd/migrate --config config/config.yaml.example down
go run ./cmd/migrate --config config/config.yaml.example up
```

Expected: both succeed in local dev database.

- [ ] **Step 7: Run tests/build**

Run:

```bash
go test ./...
go build ./...
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add migrations sqlc.yaml internal/store cmd/migrate
git commit -m "feat: add database schema and sqlc store"
```

### Task 4: Implement Domain Enums, State Machines, And Permissions

**Files:**
- Create: `internal/domain/enums.go`
- Create: `internal/domain/errors.go`
- Create: `internal/domain/app_state_machine.go`
- Create: `internal/domain/job_state_machine.go`
- Create: `internal/domain/permissions.go`
- Test: `internal/domain/*_test.go`

- [ ] **Step 1: Write failing tests for app state transitions**

Cover allowed transitions:

- `draft -> initializing`
- `initializing -> binding_waiting`
- `binding_waiting -> binding_failed`
- `binding_waiting -> ready`
- `ready -> running`
- any non-deleted state -> `deleted`

Cover rejected transition: `deleted -> running`.

- [ ] **Step 2: Write failing tests for permissions**

Cover:

- `platform_admin` can access all orgs.
- `org_admin` only accesses own org.
- `org_member` only accesses own apps.
- disabled user/org is rejected.

- [ ] **Step 3: Run tests to verify failure**

```bash
go test ./internal/domain
```

Expected: FAIL.

- [ ] **Step 4: Implement domain with Chinese comments**

Add exported enums and transition helpers. Comments must describe why invalid transitions are blocked.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/domain
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain
git commit -m "feat: add domain states and permissions"
```

## Chunk 3: Auth, API Shell, And OpenAPI

### Task 5: Implement Authentication

**Files:**
- Create: `internal/auth/password.go`
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/refresh_token.go`
- Create: `internal/api/middleware/auth.go`
- Create: `internal/api/handlers/auth.go`
- Test: `internal/auth/*_test.go`

- [ ] **Step 1: Write password hashing tests**

Assert Argon2id hash verifies correct password and rejects wrong password.

- [ ] **Step 2: Write JWT tests**

Assert access token contains user ID, role, org ID, and expiry.

- [ ] **Step 3: Write refresh token tests**

Assert only token hash is persisted and revoked token cannot refresh.

- [ ] **Step 4: Implement auth package with Chinese comments**

Keep sensitive token values out of logs and errors.

- [ ] **Step 5: Implement auth handlers**

Endpoints:

- `POST /api/v1/auth/login`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/me`

- [ ] **Step 6: Run tests**

```bash
go test ./internal/auth ./internal/api/...
```

Expected: PASS.

- [ ] **Step 7: Verify API manually**

Run server and use curl against login/me with a seeded admin user.

- [ ] **Step 8: Commit**

```bash
git add internal/auth internal/api
git commit -m "feat: add authentication APIs"
```

### Task 6: Add API Router, Error Model, And OpenAPI Generation

**Files:**
- Create: `internal/api/router.go`
- Create: `internal/api/errors.go`
- Create: `internal/api/dto/*.go`
- Create: `api/openapi.yaml` or generated OpenAPI output
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Define error response tests**

Verify errors serialize as:

```json
{
  "code": "ERROR_CODE",
  "message": "中文错误信息",
  "request_id": "uuid",
  "details": {}
}
```

- [ ] **Step 2: Implement router and middleware**

Add request ID, recovery, auth context, JSON errors, and CORS/CSRF placeholders.

- [ ] **Step 3: Add OpenAPI schema generation**

Choose one implementation path and document it in code:

- generated static `api/openapi.yaml`, or
- Go annotation generation.

The schema must include Chinese DTO field descriptions.

- [ ] **Step 4: Verify schema**

Run:

```bash
go test ./internal/api/...
```

Then validate OpenAPI with the selected tool.

- [ ] **Step 5: Commit**

```bash
git add internal/api api cmd/server
git commit -m "feat: add API shell and OpenAPI contract"
```

## Chunk 4: Core Business APIs

### Task 7: Organizations And Recharge

**Files:**
- Create: `internal/service/organization_service.go`
- Create: `internal/service/audit_service.go`
- Create: `internal/api/handlers/organizations.go`
- Create: `internal/integrations/newapi/client.go`
- Test: `internal/service/organization_service_test.go`
- Test: `internal/integrations/newapi/client_test.go`

- [ ] **Step 1: Write service tests with fake new-api**

Cover:

- create organization writes organization and stores `newapi_user_id`.
- recharge calls new-api before writing successful recharge record.
- failed new-api recharge writes failed record or returns error without success record.

- [ ] **Step 2: Implement fakeable new-api interface**

Add Chinese comments explaining external API assumptions.

- [ ] **Step 3: Implement organization service**

Include permission checks and audit logging.

- [ ] **Step 4: Add handlers**

Endpoints from technical design under `/api/v1/organizations`.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/service ./internal/integrations/newapi ./internal/api/...
```

Expected: PASS.

- [ ] **Step 6: Verify API with curl**

Create org, list orgs, recharge org.

- [ ] **Step 7: Commit**

```bash
git add internal/service internal/api/handlers internal/integrations/newapi
git commit -m "feat: add organization and recharge APIs"
```

### Task 8: Members, Persona, And Budgets

**Files:**
- Create: `internal/service/member_service.go`
- Create: `internal/service/persona_service.go`
- Create: `internal/service/budget_service.go`
- Create: `internal/api/handlers/members.go`
- Create: `internal/api/handlers/persona.go`
- Create: `internal/api/handlers/budgets.go`
- Test: service tests for each

- [ ] **Step 1: Write failing service tests**

Cover:

- org admin creates member in own org.
- org admin cannot manage another org.
- member budget update is restricted to org admin.
- persona version increments.
- member override policy is respected.

- [ ] **Step 2: Implement services with Chinese comments**

Keep role checks in service, not only handlers.

- [ ] **Step 3: Add handlers and DTOs**

Add members, persona, and budget endpoints.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service ./internal/api/...
```

Expected: PASS.

- [ ] **Step 5: Verify with curl**

Create member, set persona, set budget.

- [ ] **Step 6: Commit**

```bash
git add internal/service internal/api/handlers internal/api/dto
git commit -m "feat: add member persona and budget APIs"
```

## Chunk 5: Redis Jobs, Worker, Scheduler

### Task 9: Implement Redis Queue And Job Store

**Files:**
- Create: `internal/redis/client.go`
- Create: `internal/redis/queue.go`
- Create: `internal/redis/lock.go`
- Create: `internal/worker/worker.go`
- Create: `internal/worker/handlers/registry.go`
- Create: `internal/scheduler/scheduler.go`
- Test: `internal/redis/*_test.go`
- Test: `internal/worker/*_test.go`

- [ ] **Step 1: Write queue tests**

Use fake Redis or integration Redis. Cover enqueue, dequeue, delayed requeue, and lock acquisition.

- [ ] **Step 2: Write worker tests**

Cover:

- worker ignores Redis job ID if PostgreSQL job is canceled.
- failed retryable job returns to pending and schedules retry.
- nonretryable job becomes failed.

- [ ] **Step 3: Implement queue and worker with Chinese comments**

Document that PostgreSQL is the job fact source and Redis is runtime distribution.

- [ ] **Step 4: Implement reconciler**

On startup and interval, query pending jobs due now and enqueue missing IDs.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/redis ./internal/worker ./internal/scheduler
```

Expected: PASS.

- [ ] **Step 6: Verify against compose Redis/PostgreSQL**

Create a test job, run worker, confirm job status transitions.

- [ ] **Step 7: Commit**

```bash
git add internal/redis internal/worker internal/scheduler internal/store/queries
git commit -m "feat: add Redis backed job worker"
```

## Chunk 6: Runtime, OpenClaw, Apps, And Knowledge

### Task 10: Runtime Adapter And App Directories

**Files:**
- Create: `internal/integrations/runtime/runtime.go`
- Create: `internal/integrations/runtime/docker/client.go`
- Create: `internal/files/app_dirs.go`
- Test: runtime and files tests

- [ ] **Step 1: Write app directory tests**

Assert app directories are created under configured `data_root`:

```text
apps/{app_id}/config
apps/{app_id}/state
apps/{app_id}/knowledge
apps/{app_id}/logs
```

- [ ] **Step 2: Write runtime spec tests**

Assert container spec uses host bind mounts, not Docker named volumes.

- [ ] **Step 3: Implement directory manager and runtime interface**

Use Chinese comments for bind mount security and persistence assumptions.

- [ ] **Step 4: Add Docker SDK implementation**

Implement create/start/stop/restart/remove/inspect/logs/stats/exec.

- [ ] **Step 5: Run unit tests**

```bash
go test ./internal/files ./internal/integrations/runtime/...
```

Expected: PASS.

- [ ] **Step 6: Run Docker integration verification**

Create a temporary container with a bind mount, exec `echo ok`, fetch logs, remove container.

- [ ] **Step 7: Commit**

```bash
git add internal/files internal/integrations/runtime
git commit -m "feat: add Docker runtime adapter"
```

### Task 11: OpenClaw Adapter And CLI Parsing

**Files:**
- Create: `internal/integrations/openclaw/adapter.go`
- Create: `internal/integrations/openclaw/parser.go`
- Test: `internal/integrations/openclaw/parser_test.go`

- [ ] **Step 1: Write parser tests**

Cover common outputs:

- QR URL in stdout.
- terminal QR payload text.
- expired QR message.
- login success marker.
- plugin missing error.

- [ ] **Step 2: Implement parser**

Return structured `QRCodeResult`, `BindingResult`, and typed errors.

- [ ] **Step 3: Implement adapter around runtime exec**

Commands:

- `openclaw channels login --channel openclaw-weixin`
- health check command or fallback container state.
- knowledge import/delete commands as configured.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/integrations/openclaw
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/openclaw
git commit -m "feat: add OpenClaw CLI adapter"
```

### Task 12: App Lifecycle Services And APIs

**Files:**
- Create: `internal/service/app_service.go`
- Create: `internal/service/wechat_service.go`
- Create: `internal/service/knowledge_service.go`
- Create: `internal/api/handlers/apps.go`
- Create: `internal/api/handlers/wechat.go`
- Create: `internal/api/handlers/knowledge.go`
- Create: `internal/worker/handlers/app_initialize.go`
- Create: `internal/worker/handlers/app_runtime.go`
- Create: `internal/worker/handlers/wechat.go`
- Create: `internal/worker/handlers/knowledge.go`
- Test: service and worker tests

- [ ] **Step 1: Write service tests**

Cover:

- creating app draft.
- initialize creates job.
- publish only allowed from `ready`.
- delete creates delete job.
- org member only sees own app.

- [ ] **Step 2: Write worker tests with fake adapters**

Cover:

- app initialize creates api_key, app dirs, container, starts it, and moves to `binding_waiting`.
- container creation failure disables/cleans api_key state.
- wechat login failure keeps container running and marks binding failed.
- delete app removes container and disables key.

- [ ] **Step 3: Implement app services and handlers**

All exported service methods must have Chinese comments.

- [ ] **Step 4: Implement worker handlers**

Use fakeable interfaces for new-api, runtime, openclaw, files.

- [ ] **Step 5: Implement knowledge upload**

Write upload files to `data_root/apps/{app_id}/knowledge`, create `knowledge_import` job.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/service ./internal/worker ./internal/api/...
```

Expected: PASS.

- [ ] **Step 7: Verify with local runtime**

Use a test image or prepared OpenClaw runtime image to initialize an app and confirm:

- app directory exists.
- container uses bind mounts.
- logs endpoint returns recent lines.
- job history updates.

- [ ] **Step 8: Commit**

```bash
git add internal/service internal/api/handlers internal/worker/handlers
git commit -m "feat: add app lifecycle and OpenClaw operations"
```

## Chunk 7: Usage, Audit, Health, And Budget Automation

### Task 13: Usage And Audit APIs

**Files:**
- Create: `internal/service/usage_service.go`
- Create: `internal/api/handlers/usage.go`
- Create: `internal/api/handlers/audit.go`
- Test: usage and audit service tests

- [ ] **Step 1: Write usage tests with fake new-api**

Cover app, member, org, platform aggregation.

- [ ] **Step 2: Implement usage service**

Do not treat `usage_snapshots` as billing source. Add Chinese comments stating new-api is the billing source.

- [ ] **Step 3: Implement audit query API**

Support org, actor, action, target, result, date range filters.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service ./internal/api/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/usage_service.go internal/api/handlers/usage.go internal/api/handlers/audit.go
git commit -m "feat: add usage and audit APIs"
```

### Task 14: Budget Scheduler And Runtime Status Refresh

**Files:**
- Create: `internal/worker/handlers/budget.go`
- Create: `internal/worker/handlers/runtime_refresh.go`
- Modify: `internal/scheduler/scheduler.go`
- Test: scheduler and worker tests

- [ ] **Step 1: Write budget worker tests**

Cover:

- warn-only org does not disable keys.
- auto-disable org creates disable key job.
- used credit snapshot updates.

- [ ] **Step 2: Write runtime refresh tests**

Cover running, stopped, error, unknown, budget_limited cases.

- [ ] **Step 3: Implement scheduler**

Create periodic jobs for:

- `budget_check_member`
- `runtime_refresh_status`
- `app_health_check`
- job reconciler.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/scheduler ./internal/worker
```

Expected: PASS.

- [ ] **Step 5: Verify in running server**

Start server with worker/scheduler enabled; confirm jobs are created and processed.

- [ ] **Step 6: Commit**

```bash
git add internal/scheduler internal/worker/handlers
git commit -m "feat: add budget and runtime schedulers"
```

## Chunk 8: Frontend Foundation

### Task 15: Scaffold Vue 3 App

**Files:**
- Create: `web/package.json`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json`
- Create: `web/src/main.ts`
- Create: `web/src/app/router.ts`
- Create: `web/src/app/query-client.ts`
- Create: `web/src/stores/auth.ts`
- Create: `web/src/layouts/AuthLayout.vue`
- Create: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1: Initialize frontend**

Run:

```bash
npm create vite@latest web -- --template vue-ts
```

Then install dependencies:

```bash
cd web
npm install naive-ui @vicons/ionicons5 vue-router pinia @tanstack/vue-query axios
```

- [ ] **Step 2: Add app providers and router**

Set up Naive UI provider, Vue Router, Pinia, and Query client.

- [ ] **Step 3: Add generated API placeholder**

Create `web/src/api/client.ts` and a placeholder `web/src/api/generated/README.md`. Full generated client is added after OpenAPI generation is stable.

- [ ] **Step 4: Run typecheck/build**

Run:

```bash
cd web
npm run build
```

Expected: PASS.

- [ ] **Step 5: Start frontend and verify in browser**

Run:

```bash
cd web
npm run dev
```

Use `chrome-devtools` MCP to open the local URL. Verify page loads without console errors.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat: scaffold Vue frontend"
```

### Task 16: Login And Dashboard Shell

**Files:**
- Create: `web/src/pages/login/LoginPage.vue`
- Create: `web/src/domain/permissions.ts`
- Create: `web/src/components/DataTableToolbar.vue`
- Modify: router/layouts/auth store

- [ ] **Step 1: Write frontend tests or component checks**

Add tests if frontend test runner is configured; otherwise use browser verification as mandatory validation.

- [ ] **Step 2: Implement login page**

Use Naive UI form, username/password fields, submit button, and error display.

- [ ] **Step 3: Implement dashboard shell**

Dynamic menu based on role:

- platform admin routes.
- org admin routes.
- org member routes.

- [ ] **Step 4: Run build**

```bash
cd web
npm run build
```

Expected: PASS.

- [ ] **Step 5: Verify with Chrome DevTools MCP**

Open login page and dashboard with mocked or real auth. Verify:

- form labels and button text do not overlap.
- invalid login error appears.
- menu items match role.
- no console errors.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat: add login and dashboard shell"
```

## Chunk 9: Frontend Business Pages

### Task 17: Organization, Member, Persona, Budget Pages

**Files:**
- Create: `web/src/pages/platform/OrganizationsPage.vue`
- Create: `web/src/pages/org/MembersPage.vue`
- Create: `web/src/pages/org/PersonaPage.vue`
- Create: `web/src/pages/org/BudgetsPage.vue`
- Create: `web/src/api/hooks/*.ts`

- [ ] **Step 1: Generate or wire API client**

Generate TypeScript client from OpenAPI and wrap with Query hooks.

- [ ] **Step 2: Implement pages**

Use Naive UI tables, forms, modals, and status tags.

- [ ] **Step 3: Run build**

```bash
cd web
npm run build
```

Expected: PASS.

- [ ] **Step 4: Verify with Chrome DevTools MCP**

Open each page. Verify:

- lists load.
- create/edit dialogs work.
- validation messages show.
- table text does not overlap.
- no console errors.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat: add organization member persona budget pages"
```

### Task 18: App Wizard, Runtime, WeChat, Knowledge Pages

**Files:**
- Create: `web/src/pages/apps/AppListPage.vue`
- Create: `web/src/pages/apps/AppCreateWizard.vue`
- Create: `web/src/pages/apps/AppDetailPage.vue`
- Create: `web/src/pages/apps/AppWechatPage.vue`
- Create: `web/src/pages/apps/AppKnowledgePage.vue`
- Create: `web/src/components/AppStatusTag.vue`
- Create: `web/src/components/RuntimeStatusTag.vue`
- Create: `web/src/components/JobProgressPanel.vue`
- Create: `web/src/components/UploadKnowledgeFile.vue`

- [ ] **Step 1: Implement Query hooks**

Hooks:

- `useAppsQuery`
- `useAppDetailQuery`
- `useInitializeAppMutation`
- `useAppJobsQuery`
- `useWechatBindingQuery`
- `useStartWechatLoginMutation`
- `useAppRuntimeQuery`
- `useAppLogsQuery`

- [ ] **Step 2: Implement app wizard**

Steps:

1. basic info.
2. persona.
3. knowledge upload optional.
4. initialize resources.
5. wechat bind.
6. publish.

- [ ] **Step 3: Implement app detail**

Include status, job panel, runtime state, logs, budget state, knowledge files, danger zone.

- [ ] **Step 4: Run build**

```bash
cd web
npm run build
```

Expected: PASS.

- [ ] **Step 5: Verify with Chrome DevTools MCP**

Use browser to verify:

- wizard can move step by step.
- initialization shows pending/running/succeeded state.
- QR payload panel updates.
- logs refresh button works.
- file upload error and success states are visible.
- mobile and desktop widths have no incoherent overlap.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat: add app wizard and runtime pages"
```

### Task 19: Usage And Audit Pages

**Files:**
- Create: `web/src/pages/usage/UsagePage.vue`
- Create: `web/src/pages/audit/AuditLogPage.vue`
- Create: `web/src/domain/formatters.ts`

- [ ] **Step 1: Implement usage hooks and page**

Support app, member, org, platform scopes based on role.

- [ ] **Step 2: Implement audit log page**

Filters: organization, actor, action, target, result, date range.

- [ ] **Step 3: Run build**

```bash
cd web
npm run build
```

Expected: PASS.

- [ ] **Step 4: Verify with Chrome DevTools MCP**

Open usage and audit pages. Verify filters, loading states, empty states, and table layout.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat: add usage and audit pages"
```

## Chunk 10: End-To-End Verification And Hardening

### Task 20: Full Local Integration Verification

**Files:**
- Create: `docs/local-verification.md`
- Optional Create: `scripts/verify-local.sh`

- [ ] **Step 1: Start local environment**

Run:

```bash
docker compose up -d
```

Expected: PostgreSQL, Redis, new-api, Ollama, backend, and frontend are running.

- [ ] **Step 2: Pull small model**

Run:

```bash
docker exec ollama ollama pull qwen2.5:0.5b
docker exec ollama ollama list
```

Expected: small model is present.

- [ ] **Step 3: Run backend tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run frontend build**

```bash
cd web
npm run build
```

Expected: PASS.

- [ ] **Step 5: Verify browser flows with Chrome DevTools MCP**

Required flows:

- login.
- create organization.
- create member.
- create app draft.
- initialize app.
- verify container bind mount under `./data/manager/apps/{app_id}`.
- trigger WeChat login and see QR state or controlled fake output.
- upload knowledge file.
- check logs/runtime page.
- delete app and verify container removed.

- [ ] **Step 6: Verify no named volumes**

Run:

```bash
docker compose config | grep -n "^volumes:" || true
```

Expected: no top-level named volumes section.

- [ ] **Step 7: Document results**

Write commands, expected output, and any known manual steps into `docs/local-verification.md`.

- [ ] **Step 8: Commit**

```bash
git add docs/local-verification.md scripts/verify-local.sh
git commit -m "docs: add local verification guide"
```

### Task 21: Final Review Gate

**Files:**
- Modify as needed based on review findings.

- [ ] **Step 1: Run all verification**

Run:

```bash
go test ./...
go build ./...
cd web && npm run build
```

Expected: all pass.

- [ ] **Step 2: Run docker compose verification**

Run:

```bash
docker compose config
docker compose ps
```

Expected: config valid; expected services visible.

- [ ] **Step 3: Browser verification**

Use `chrome-devtools` MCP to verify all implemented key pages and flows.

- [ ] **Step 4: Check comments and tests**

Confirm:

- public Go types and core methods have Chinese comments.
- core domain/service tests exist.
- OpenClaw parser tests exist.
- job/Redis tests exist.
- frontend build passes.

- [ ] **Step 5: Commit final fixes**

```bash
git add .
git commit -m "chore: finalize OpenClaw manager implementation"
```

## Out Of Scope For This Plan

- Multi-node runtime agent.
- Redis/NATS replacement for cross-node event bus.
- SSE/WebSocket live push.
- Organization shared knowledge base.
- Fine-grained RBAC.
- Real payment, invoice, or RMB settlement.
- Production-grade OpenClaw runtime Dockerfile if it is provided externally.

## Handoff

Implementation should proceed chunk by chunk. Do not skip verification steps. If a page is added or changed, use `chrome-devtools` MCP to validate the browser behavior before committing that task.
