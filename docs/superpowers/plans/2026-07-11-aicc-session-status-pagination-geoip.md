# AICC Session Status, Pagination, and GeoIP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add visitor-level resolved/unresolved session marking, consistent session status labels, paginated AICC session management, and built-in GeoIP region resolution with periodic updates.

**Architecture:** Public visitor actions update the session `resolution_status` directly through a session-level API. The management session list keeps using the existing `limit/offset` backend contract and adds frontend pagination state. GeoIP resolution is owned by manager-api: the image ships with ip2region xdb files, runtime downloads the Gitee archive daily to a data directory, and session creation resolves public IPs without blocking the chat flow on lookup failures.

**Tech Stack:** Go 1.25, Gin, sqlc, ip2region Go xdb binding, Vue 3, Naive UI, Vitest, k3d local deployment.

## Global Constraints

- 用户可见文案必须走现有 i18n。
- 不新增 YAML 配置；GeoIP 路径、更新 URL、更新周期使用代码常量。
- GeoIP 更新 URL 使用国内源，不使用 GitHub；当前固定为 Gitee archive zip。
- IP 库不通过 k8s 挂载，manager-api 镜像构建时内置。
- 公开页反馈是会话级状态，不再绑定单条助手回复。
- 每个阶段完成后提交。

---

### Task 1: Session Status API and Public Page Controls

**Files:**
- Modify: `internal/api/handlers/public_aicc.go`
- Modify: `internal/api/handlers/public_aicc_test.go`
- Modify: `internal/service/aicc_public_service.go`
- Modify: `internal/service/aicc_public_service_test.go`
- Modify: `web/src/pages/aicc/PublicAICCChatPage.vue`
- Modify: `web/src/pages/aicc/PublicAICCChatPage.spec.ts`
- Modify: `web/src/api/hooks/useAICC.ts`
- Modify: `web/src/domain/aicc.ts`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

**Interfaces:**
- Produces: `ResolveSession(ctx, sessionToken, resolutionStatus string) (AICCPublicResolutionResult, error)`
- Produces: `POST /api/v1/public/aicc/sessions/{sessionToken}/resolution` with body `{ "resolution_status": "resolved" | "unresolved" }`

- [ ] Write failing handler/service tests for resolved and unresolved status updates.
- [ ] Write failing public page tests for two buttons and selected status disable behavior.
- [ ] Implement session status validation and update.
- [ ] Implement public page controls and i18n labels.
- [ ] Run `go test ./internal/api/handlers ./internal/service` and `npm test -- PublicAICCChatPage.spec.ts`.
- [ ] Commit.

### Task 2: Management Session Status Labels and Pagination

**Files:**
- Modify: `web/src/pages/aicc/AICCSessionsPage.vue`
- Modify: `web/src/pages/aicc/AICCSessionsPage.spec.ts`
- Modify: `web/src/api/hooks/useAICC.ts`
- Modify: `web/src/domain/aicc.ts`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

**Interfaces:**
- Consumes: existing `GET /api/v1/aicc/agents/{agentId}/sessions?limit=&offset=`
- Produces: page state `{ page, pageSize, offset }`

- [ ] Write failing tests for status labels `已解决 / 未解决 / 跟进中`.
- [ ] Write failing tests for pagination query limit/offset and reset-on-filter-change.
- [ ] Implement status label mapping and pagination control.
- [ ] Run `npm test -- AICCSessionsPage.spec.ts` and `npm run typecheck`.
- [ ] Commit.

### Task 3: GeoIP Resolver and Session Region Resolution

**Files:**
- Create: `internal/service/aicc_geoip.go`
- Create: `internal/service/aicc_geoip_test.go`
- Modify: `internal/service/aicc_public_service.go`
- Modify: `internal/service/aicc_public_service_test.go`
- Modify: `cmd/server/main.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `AICCGeoIPResolver.Resolve(ctx context.Context, remoteIP string) string`
- Produces: `NewAICCGeoIPResolver() *AICCGeoIPResolver`

- [ ] Write failing tests for private IP returning empty, China region parsing, and session creation storing resolved region.
- [ ] Add ip2region Go dependency.
- [ ] Implement resolver loading runtime xdb first, then builtin xdb.
- [ ] Inject resolver into `AICCPublicService`.
- [ ] Run `go test ./internal/service`.
- [ ] Commit.

### Task 4: GeoIP Builtin Data and Runtime Updater

**Files:**
- Modify: `cmd/server/Dockerfile`
- Modify: `internal/service/aicc_geoip.go`
- Modify: `internal/service/aicc_geoip_test.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `https://gitee.com/lionsoul/ip2region/repository/archive/master.zip`
- Produces runtime files under `/var/lib/oc-manager/data/geoip/`
- Produces builtin files under `/usr/local/share/oc-manager/geoip/`

- [ ] Write failing tests for extracting xdb files from archive and atomic replacement.
- [ ] Update Dockerfile to download Gitee archive and copy xdb files into the runtime image.
- [ ] Add daily background updater that never blocks session creation.
- [ ] Run `go test ./internal/service` and local Docker build.
- [ ] Commit.

### Task 5: Generated Types, Deployment, and Browser Verification

**Files:**
- Modify generated: `openapi/openapi.yaml`
- Modify generated: `web/src/api/generated.ts`

- [ ] Run `make openapi-gen && make web-types-gen`.
- [ ] Run backend and frontend test/build checks.
- [ ] Run `make local-build` and wait for rollout.
- [ ] Use Chrome DevTools MCP to verify public page resolved/unresolved buttons, management pagination, status labels, and no console issues.
- [ ] Commit any verification fixes.
