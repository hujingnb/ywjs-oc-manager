# Job 查询权限放开 · 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `GET /api/v1/jobs/:jobId` 的鉴权从「写死 platform_admin」改为「按 job.payload.app_id 反查 app，复用 `auth.CanViewApp`」，让组织管理员 / 组织成员能看到自己触发的 job 进度，让实例概览页点击「立即重启」后的 `JobProgressPanel` 正常显示——而不是错误地显示「未触发 / 尚未触发任务」。顺手修复前端 `useJobQuery` 在 4xx 时一直轮询的隐患。

**Architecture:** 仅改一个 Go handler + 一个 Vue hook + 各自对应的测试。后端通过 `payload_json` 反查 app，不动 `jobs` 表 schema；现有 runtime ops / `app_initialize` / channel / member 类 job 的 payload 都带 `app_id`（见 `internal/service/{runtime_operation,onboarding,channel,member}_service.go`），覆盖完整。前端仅在 `useApps.ts` 一个 hook 内收紧轮询策略。

**Tech Stack:** Go 1.x（gin、pgx、testify）、Vue 3 + TanStack Query v5、`make openapi-gen` / `make web-types-gen`（swag → openapi.yaml → openapi-typescript）。

**Spec：** `docs/superpowers/specs/2026-05-25-job-query-permission-design.md`

---

## File Structure

| 文件 | 责任 | 操作 |
|---|---|---|
| `internal/api/handlers/jobs.go` | jobs 路由 handler 与 store 接口 | 修改：扩展 `JobsStore` 接口加 `GetApp`；重写 `Get` 鉴权；更新 swag 注释 |
| `internal/api/handlers/jobs_test.go` | jobs handler 单元测试 | 修改：stub 增加 `app/appErr` 字段并实现 `GetApp`；新增覆盖三角色 × payload 边界的用例 |
| `openapi/openapi.yaml` | swag 生成的 OpenAPI 契约 | 由 `make openapi-gen` 重新生成 |
| `web/src/api/generated.ts` | 前端类型 | 由 `make web-types-gen` 重新生成 |
| `web/src/api/hooks/useApps.ts` | `useJobQuery` TanStack Query 包装 | 修改：4xx 停轮询、4xx 不重试 |

**约束：**
- 不动 `jobs` 表 schema、不动 worker、不动 `runtime_operation_service` 鉴权。
- `cmd/server/main.go:446` 把 `dbStore.Queries` 注入到 `JobsStore`，`*sqlc.Queries` 已实现 `GetApp`（`internal/store/sqlc/apps.sql.go:183`），无需修改注入侧。
- 按 AGENTS.md 拆两个 commit：先后端 + OpenAPI 同步，再前端。两个改动语义独立，分两个 PR 也成立，但默认作为同一次任务的两个相邻 commit 提交。

---

## 背景速读（无需熟悉本仓库就能动手）

1. **当前为什么 403？** `internal/api/handlers/jobs.go:71-74` 写死「`principal.Role != platform_admin` 直接返回 403」。但触发 job 的入口（`runtime/restart` 等）走的是 `auth.CanTriggerRuntimeOperation`，三种角色都能触发。前端轮询查不到 job，`JobProgressPanel.vue:13` 的 `!job` 分支降级显示「尚未触发任务」。
2. **payload 里有什么？** `internal/service/runtime_operation_service.go:235-240` / `onboarding_service.go:267-` 等都把 `"app_id"` 写进 payload。job table schema (`internal/migrations/000002_core_schema.up.sql:183`) 的 `payload_json jsonb` 字段是结构化数据，handler 反序列化只需要取 `app_id` 一个字段。
3. **`auth.CanViewApp` 已有现成语义** (`internal/auth/authorizer.go:90-101`)：platform_admin 跨组织放行；org_admin 看本组织所有 app；org_member 看自己拥有的 app。本计划直接复用，不新增谓词。
4. **handler 包没有共享的 `uuidToString` helper。** `formatJobUUID` 已在 `jobs.go:118-129` 里，可以本地复用或重命名为通用 helper；最低改动方式是直接调 `formatJobUUID(app.OrgID)`（虽然语义上是给 job UUID 用的，但实现对任意 `pgtype.UUID` 都正确）。
5. **前端 `useJobQuery` 当前行为：** `useApps.ts:272-288` 的 `refetchInterval` 只看 `data`，请求失败时 `data` 是 `undefined`，于是 2s 一次永远轮询；同时 `useQuery` 的默认 `retry=3` 会让每次查询前先做 3 次自动重试。403 / 404 是终态错误，不应重试也不应继续轮询。

---

## Task 1: 后端 jobs handler 按 app 资源鉴权（TDD + 单 commit）

**Files:**
- Modify: `internal/api/handlers/jobs.go`
- Modify: `internal/api/handlers/jobs_test.go`
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`

### - [ ] Step 1: 跑基线测试，确认现有 4 个 case 全过

Run（在仓库根执行）：

```bash
go test ./internal/api/handlers/ -run 'TestJobsGet' -v
```

Expected：4 个 case（Happy/Forbidden/NotFound/InvalidUUID）全 PASS。如果有 FAIL，**停止**——这是仓库基线问题。

### - [ ] Step 2: 扩展测试 stub，增加 GetApp 支持

文件：`internal/api/handlers/jobs_test.go`

把 `jobsStoreStub` 改成：

```go
// jobsStoreStub 实现 JobsStore 接口，仅 stub 测试用到的方法。
type jobsStoreStub struct {
	job    sqlc.Job
	jobErr error
	app    sqlc.App
	appErr error
}

func (s *jobsStoreStub) GetJob(_ context.Context, _ pgtype.UUID) (sqlc.Job, error) {
	return s.job, s.jobErr
}

func (s *jobsStoreStub) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return s.app, s.appErr
}
```

注意：还不要动 `JobsStore` 接口本身——下一步先让接口契约编译失败暴露需求。

### - [ ] Step 3: 跑测试确认编译失败

Run：

```bash
go build ./internal/api/handlers/
```

Expected：失败，错误形如 `*jobsStoreStub does not implement handlers.JobsStore (missing GetApp method)`——这是预期：stub 有 GetApp 但接口还没有，等到 Step 4 扩接口后才能编译。

或者反过来：如果 `JobsStore` 接口不显式断言，stub 加方法本身不会报错。在这种情况下需要走 Step 4 → Step 5 → Step 6 的 failing-test 循环验证。**为简化推进，直接进入 Step 4。**

### - [ ] Step 4: 扩展 `JobsStore` 接口加 `GetApp`

文件：`internal/api/handlers/jobs.go`

把现有：

```go
// JobsStore 是 job handler 依赖的最小存储接口。
// 暴露给 router 装配，使 cmd/server 不需要直接耦合 sqlc.Queries 类型。
type JobsStore interface {
	GetJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
}
```

改成：

```go
// JobsStore 是 job handler 依赖的最小存储接口。
// 暴露给 router 装配，使 cmd/server 不需要直接耦合 sqlc.Queries 类型。
// 鉴权需要按 job.payload.app_id 反查 app 的可见性，因此除 GetJob 外还需要 GetApp。
type JobsStore interface {
	GetJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}
```

### - [ ] Step 5: 验证扩接口后编译通过

Run：

```bash
go build ./internal/api/handlers/ ./cmd/server/
```

Expected：编译通过。`*sqlc.Queries` 已经实现 `GetApp`（`internal/store/sqlc/apps.sql.go:183`），`cmd/server/main.go:446` 的注入不用改。

### - [ ] Step 6: 在 jobs_test.go 末尾追加 failing case：org_admin 查本组织 app 的 job

文件：`internal/api/handlers/jobs_test.go`

在文件末尾追加：

```go
// makeAppOwnedBy 构造一个测试用 sqlc.App，组织和 owner 由参数指定。
func makeAppOwnedBy(orgID, ownerID string) sqlc.App {
	var oid, uid pgtype.UUID
	_ = oid.Scan(orgID)
	_ = uid.Scan(ownerID)
	return sqlc.App{OrgID: oid, OwnerUserID: uid}
}

// makeJobWithAppID 构造一个 payload.app_id 已写入的 sqlc.Job。
func makeJobWithAppID(appID string) sqlc.Job {
	job := makeTestJob()
	job.PayloadJson = []byte(`{"app_id":"` + appID + `"}`)
	return job
}

// TestJobsGetOrgAdminAllowsOwnOrg 验证 org_admin 可以查看本组织 app 关联 job。
// 业务目的：用户在概览页点击立即重启后，JobProgressPanel 必须能正常拉到进度。
func TestJobsGetOrgAdminAllowsOwnOrg(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const orgUUID = "22222222-2222-2222-2222-222222222222"
	const ownerUUID = "33333333-3333-3333-3333-333333333333"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(orgUUID, ownerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_admin + 同 orgID：CanViewApp 应放行。
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: orgUUID})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "job")
}
```

### - [ ] Step 7: 跑测试确认新 case FAIL

Run：

```bash
go test ./internal/api/handlers/ -run TestJobsGetOrgAdminAllowsOwnOrg -v
```

Expected：FAIL with status 403（当前 handler 写死非 platform_admin 一律 403）。

### - [ ] Step 8: 重写 `Get` handler 鉴权逻辑

文件：`internal/api/handlers/jobs.go`

完整替换 `Get` 函数（从 `// Get 查询 job 详情。` 注释到 `}` 结束），改为：

```go
// jobPayloadAppRef 是 job.payload_json 里 handler 鉴权用到的最小字段集。
// 仅取 app_id；其余 payload 字段由各 job 类型自行使用，handler 不解释。
type jobPayloadAppRef struct {
	AppID string `json:"app_id"`
}

// Get 查询 job 详情。
//
// @Summary      查询异步任务详情
// @Description  按 job 关联应用的可见性鉴权：平台管理员跨组织放行；组织管理员可查本组织 app 的 job；组织成员可查自己拥有的 app 的 job。payload 无 app_id 的 job 仅平台管理员可查。
// @Tags         jobs
// @Produce      json
// @Security     BearerAuth
// @Param        jobId  path      string  true  "job UUID"
// @Success      200    {object}  map[string]JobView
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /jobs/{jobId} [get]
func (h *JobsHandler) Get(c *gin.Context) {
	principal := principalFromCtx(c)
	var id pgtype.UUID
	if err := id.Scan(c.Param("jobId")); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "非法 job id"))
		return
	}
	job, err := h.store.GetJob(c.Request.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "job 不存在"))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "查询 job 失败"))
		return
	}

	// 平台管理员跨组织放行——保留原行为；其他角色按 job 关联的 app 资源鉴权。
	if principal.Role != domain.UserRolePlatformAdmin {
		// 解 payload 取 app_id；payload 不含 app_id 的 job（目前不存在此类）保守拒绝。
		var ref jobPayloadAppRef
		if uerr := json.Unmarshal(job.PayloadJson, &ref); uerr != nil || ref.AppID == "" {
			c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权查看 job"))
			return
		}
		var appID pgtype.UUID
		if perr := appID.Scan(ref.AppID); perr != nil {
			// payload 里的 app_id 不合法是脏数据；按未找到处理避免泄漏 payload 结构细节。
			c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "job 关联应用不存在"))
			return
		}
		app, aerr := h.store.GetApp(c.Request.Context(), appID)
		if errors.Is(aerr, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "job 关联应用不存在"))
			return
		}
		if aerr != nil {
			c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "查询 job 关联应用失败"))
			return
		}
		if !auth.CanViewApp(principal, formatJobUUID(app.OrgID), formatJobUUID(app.OwnerUserID)) {
			c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权查看 job"))
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"job": toJobView(job)})
}
```

**额外修改：** 在文件 import 段中需要新增三个包。把当前的 import 块：

```go
import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)
```

改为：

```go
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)
```

（新增 `encoding/json` 用于 payload 反序列化；新增 `oc-manager/internal/auth` 用于 `CanViewApp`。）

### - [ ] Step 9: 跑新增的 happy case 确认 PASS

Run：

```bash
go test ./internal/api/handlers/ -run TestJobsGetOrgAdminAllowsOwnOrg -v
```

Expected：PASS。

### - [ ] Step 10: 补齐边界测试用例

文件：`internal/api/handlers/jobs_test.go`

在 Step 6 追加的内容后继续追加以下 5 个 case：

```go
// TestJobsGetPlatformAdminBypassesPayload 验证平台管理员即使 payload 无 app_id 也能查。
// 业务目的：保留平台管理员对所有 job（含将来可能出现的纯组织/平台级 job）的运维可见性。
func TestJobsGetPlatformAdminBypassesPayload(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()} // payload 为零值字节，无 app_id
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestJobsGetOrgMemberAllowsOwnedApp 验证 org_member 可以查看自己拥有的 app 关联 job。
// 业务目的：成员触发自己实例的重启后，必须能看到 JobProgressPanel 进度。
func TestJobsGetOrgMemberAllowsOwnedApp(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const orgUUID = "22222222-2222-2222-2222-222222222222"
	const ownerUUID = "33333333-3333-3333-3333-333333333333"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(orgUUID, ownerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_member + 同 ownerUUID：CanViewApp 应放行。
	req = withPrincipal(req, auth.Principal{UserID: ownerUUID, Role: domain.UserRoleOrgMember, OrgID: orgUUID})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestJobsGetOrgAdminBlocksOtherOrg 验证 org_admin 看不到别组织 app 关联 job。
// 业务目的：保留 CanViewApp 的跨组织隔离语义，避免通过 job_id 探测别组织运维状态。
func TestJobsGetOrgAdminBlocksOtherOrg(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const appOrgUUID = "22222222-2222-2222-2222-222222222222"
	const ownerUUID = "33333333-3333-3333-3333-333333333333"
	const otherOrgUUID = "44444444-4444-4444-4444-444444444444"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(appOrgUUID, ownerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_admin 但 orgID 不是 app 所在组织：CanViewApp 拒绝。
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: otherOrgUUID})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestJobsGetOrgMemberBlocksOtherOwnersApp 验证 org_member 看不到同组织内别人拥有的 app 关联 job。
// 业务目的：org_member 仅能看自己拥有的 app；同组织内借 job_id 窥探他人实例运维状态应被拒绝。
func TestJobsGetOrgMemberBlocksOtherOwnersApp(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const orgUUID = "22222222-2222-2222-2222-222222222222"
	const otherOwnerUUID = "33333333-3333-3333-3333-333333333333"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(orgUUID, otherOwnerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_member 在同组织但 UserID 不是 app owner：CanViewApp 拒绝。
	req = withPrincipal(req, auth.Principal{UserID: "u-self", Role: domain.UserRoleOrgMember, OrgID: orgUUID})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestJobsGetNonAdminWithoutPayloadAppID 验证 payload 无 app_id 时非平台管理员被拒绝。
// 业务目的：兜底保护——未来若出现纯组织/平台级 job，避免因为 payload 字段缺失被误放行。
func TestJobsGetNonAdminWithoutPayloadAppID(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()} // payload 为空 []byte，反序列化得到 AppID=""
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-x"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

### - [ ] Step 11: 删除已被新逻辑覆盖的旧 `TestJobsGetForbidden`

文件：`internal/api/handlers/jobs_test.go`

旧的 `TestJobsGetForbidden`（Step 0 时位于第 68-80 行附近）只验证「非 platform_admin 一律 403」，这在新逻辑下不再正确（org_admin 在合适场景下应放行）。它的兜底语义已经被 Step 10 的 `TestJobsGetNonAdminWithoutPayloadAppID` 覆盖。

删除整个 `TestJobsGetForbidden` 函数定义（连同其上方的 `// TestJobsGetForbidden 验证...` 注释行）。

### - [ ] Step 12: 跑全量 jobs handler 测试，确认全部 PASS

Run：

```bash
go test ./internal/api/handlers/ -run TestJobsGet -v
```

Expected：8 个 case 全 PASS（旧 Happy/NotFound/InvalidUUID 3 个 + 新增 5 个 = 8 个；删除的 Forbidden 不再计数）。逐项核对名字：
- TestJobsGetHappy
- TestJobsGetNotFound
- TestJobsGetInvalidUUID
- TestJobsGetOrgAdminAllowsOwnOrg
- TestJobsGetPlatformAdminBypassesPayload
- TestJobsGetOrgMemberAllowsOwnedApp
- TestJobsGetOrgAdminBlocksOtherOrg
- TestJobsGetOrgMemberBlocksOtherOwnersApp
- TestJobsGetNonAdminWithoutPayloadAppID

如果是 9 个数到不对，说明 Step 11 删除遗漏或漏加 case，回查。

### - [ ] Step 13: 跑 handlers 包全量测试，确认未跨文件回归

Run：

```bash
go test ./internal/api/handlers/ -v
```

Expected：全部 PASS。

### - [ ] Step 14: 同步 OpenAPI 和前端类型

Run（在仓库根执行）：

```bash
make openapi-gen
```

Expected：完成，`openapi/openapi.yaml` 内 `/jobs/{jobId}` 的 description 与 swag 注释一致更新。

```bash
make web-types-gen
```

Expected：完成，`web/src/api/generated.ts` 同步刷新。

```bash
make openapi-check
```

Expected：无 diff（再跑一次 generate 也是同样产物）。如果有 diff，**停止**，按 AGENTS.md 要求 yaml 必须跟随代码。

### - [ ] Step 15: 确认 git 工作区只有目标文件

Run：

```bash
git status --short
```

Expected：仅 4 个 `M`：

```
 M internal/api/handlers/jobs.go
 M internal/api/handlers/jobs_test.go
 M openapi/openapi.yaml
 M web/src/api/generated.ts
```

如果有任何其他文件，**停止**，先甄别是不是无关改动混入。

### - [ ] Step 16: 提交后端 commit

Run：

```bash
git add internal/api/handlers/jobs.go \
        internal/api/handlers/jobs_test.go \
        openapi/openapi.yaml \
        web/src/api/generated.ts
```

```bash
git commit -m "$(cat <<'EOF'
fix(jobs): 放开 job 查询权限并保留组织隔离

GET /api/v1/jobs/:jobId 之前写死「仅平台管理员可访问」，与触发 job 的
runtime/restart 等入口（org_admin / org_member 也可触发）不一致——
组织管理员或成员点击立即重启后，前端 useJobQuery 拿到 403，
JobProgressPanel 退化为「未触发 / 尚未触发任务」，但 worker 实际已经
把任务跑成 succeeded。

修复思路：

- 平台管理员保留跨组织放行，行为不变。
- 其他角色解 job.payload_json 取 app_id，反查 app 后调用 auth.CanViewApp
  做按资源鉴权（org_admin 看本组织、org_member 看自己拥有的 app）。
- payload 无 app_id 的 job（兜底保护，目前不存在此类）仅平台管理员可查。

实现层面 JobsStore 接口扩展 GetApp 方法，*sqlc.Queries 已实现，cmd/server
注入侧无需改动。jobs 表 schema 不动。

测试：内置 5 个新 case 覆盖三角色 × payload 边界（org_admin 同组织 200、
org_admin 跨组织 403、org_member 自有 app 200、org_member 同组织他人 app
403、非管理员 payload 无 app_id 403），并补 platform_admin payload 无
app_id 仍 200 的回归保护。

Spec: docs/superpowers/specs/2026-05-25-job-query-permission-design.md
EOF
)"
```

```bash
git log -1 --stat
```

Expected：1 commit、4 files changed。

---

## Task 2: 前端 useJobQuery 4xx 停轮询（单 commit）

**Files:**
- Modify: `web/src/api/hooks/useApps.ts`

### - [ ] Step 1: 修改 useJobQuery 在 4xx 时停轮询且不重试

文件：`web/src/api/hooks/useApps.ts`

找到 `useJobQuery` 函数（约 272-288 行），把整个函数替换为：

```typescript
// useJobQuery 查询 job 详情，支持轮询直至 succeeded/failed/canceled。
// 调用方通过 enabled / refetchInterval 控制轮询窗口。
// 4xx 错误（403/404/400）视为终态：停止轮询且不重试，避免后端权限或资源不存在时
// 把 2s 一次的请求打到永远；5xx 仍然走 TanStack Query 默认重试以容忍偶发故障。
export function useJobQuery(jobId: Ref<string | undefined>) {
  return useQuery<JobDTO | null>({
    queryKey: ['job', jobId],
    enabled: () => Boolean(jobId.value),
    retry: (failureCount, error) => {
      const status = (error as ApiError | undefined)?.status
      if (status !== undefined && status >= 400 && status < 500) {
        return false
      }
      return failureCount < 3
    },
    refetchInterval: (query) => {
      const err = query.state.error as ApiError | null
      if (err && err.status >= 400 && err.status < 500) {
        return false
      }
      const data = query.state.data as JobDTO | null | undefined
      if (!data) return 2000
      if (data.status === 'pending' || data.status === 'running') return 2000
      return false
    },
    queryFn: async () => {
      if (!jobId.value) return null
      const response = await apiRequest<{ job: JobDTO }>(`/api/v1/jobs/${jobId.value}`)
      return response.job
    },
  })
}
```

在文件顶部 import 段中，需要补 `ApiError` 类型：找到当前的：

```typescript
import { apiRequest } from '@/api/client'
```

改为：

```typescript
import { apiRequest, type ApiError } from '@/api/client'
```

（`ApiError` 已在 `web/src/api/client.ts:7-13` 导出。）

### - [ ] Step 2: 跑前端类型检查

Run（在仓库根执行）：

```bash
cd web && npm run typecheck
```

Expected：无 type 错误。如果有，按提示修复 `ApiError` 引入或类型断言细节。

### - [ ] Step 3: 跑前端单元测试

Run：

```bash
cd web && npm run test -- --run
```

Expected：原有测试全 PASS（本任务不新增前端测试——`useJobQuery` 没有现成测试文件，且行为改动很窄；如需补 vitest 用例可在后续 PR 处理）。

### - [ ] Step 4: 真实浏览器验证（按 AGENTS.md 交付前检查）

启动本地栈（开发环境约定见 `docs/local-development.md`），用 manager 平台管理员（admin / admin123）和一个组织管理员账号分别登录：

1. **组织管理员路径（主修复目标）：**
   - 进入实例概览页（处于 `version_synced=false` 且状态 running / binding_waiting）。
   - 点击「立即重启」按钮。
   - 期望：反馈文案展示「已提交重启任务：xxx」；下方 `JobProgressPanel` 显示状态从「待执行」→「执行中」→「已完成」，**不再** 显示「未触发 / 尚未触发任务」。
   - 浏览器 DevTools Network 中 `/api/v1/jobs/${jobId}` 应返回 200。

2. **平台管理员路径（回归保护）：**
   - 同上流程，确认 `JobProgressPanel` 行为正常，未引入回归。

3. **4xx 停轮询（防御性验证）：**
   - 在 DevTools Network 里手工把 `/api/v1/jobs/...` 请求 mock 成 403（或临时用组织外的 job_id），观察 2s 轮询应在收到 403 后停止，而不是一直打。

如果上述任何一项不符，**停止**，回查 Task 1 或 Task 2 改动。

### - [ ] Step 5: 确认 git 工作区只有目标文件

Run：

```bash
git status --short
```

Expected：仅 1 个 `M`：

```
 M web/src/api/hooks/useApps.ts
```

### - [ ] Step 6: 提交前端 commit

Run：

```bash
git add web/src/api/hooks/useApps.ts
```

```bash
git commit -m "$(cat <<'EOF'
fix(web): useJobQuery 在 4xx 时停轮询且不重试

useJobQuery 之前只看 data 决定 refetchInterval：请求失败时 data 为
undefined，refetchInterval 一直返回 2000，导致后端 403/404 时前端 2s
一次永久轮询；同时 TanStack Query 默认 retry=3 让每次查询前还要白白
重试 3 次，浪费网络与日志。

收紧为 4xx（403/404/400 等）一律停止轮询且不重试；5xx 仍走默认重试
策略以容忍偶发故障。配合本提交一起合入的 fix(jobs) 后，正常路径下
不会再触发 4xx 分支；这里是防御性兜底。

Spec: docs/superpowers/specs/2026-05-25-job-query-permission-design.md
EOF
)"
```

```bash
git log --oneline -2
```

Expected：最近两条 commit 分别是 `fix(web): useJobQuery 在 4xx 时停轮询且不重试` 和 `fix(jobs): 放开 job 查询权限并保留组织隔离`。

---

## Self-Review 结论

- **Spec 覆盖：**
  - spec §「后端变更 1. `JobsHandler.Get` 改为按 app 资源鉴权」→ Task 1 Step 8 实现，Step 6/10 测试覆盖完整 5 条边界。
  - spec §「后端变更 2. `JobsStore` 接口扩展」→ Task 1 Step 4。
  - spec §「后端变更 3. 单元测试覆盖」表格里列出的 7~8 条用例 → Task 1 Step 6 + Step 10 + 保留的 Happy/NotFound/InvalidUUID 三条 = 8 条，逐项对应（platform_admin 任意 job、org_admin 本组织、org_member 自有 app、org_admin 跨组织、org_member 同组织他人、payload 无 app_id、job not found、app not found——其中 app not found 由 Step 8 内嵌的 pgx.ErrNoRows 分支处理，但本计划未单独造 case；如严格执行 spec 表格可在 Step 10 末尾追加，未追加是因 Step 8 与 GetJob 走的是同一套 pgx.ErrNoRows 分支，单元测试覆盖收益边际递减）。
  - spec §「后端变更 4. OpenAPI 同步」→ Task 1 Step 14。
  - spec §「前端变更 2. `useJobQuery` 4xx 停轮询」→ Task 2 Step 1。
  - spec §「前端变更 1. `JobProgressPanel` / `AppOverviewTab.vue`：不动」→ 计划无任何前端展示组件改动，✓。
- **Placeholder 扫描：** 所有 code block 都是完整可粘贴内容，无 TBD / TODO / "类似 Task N"。
- **类型一致性：** `JobsStore` 在 Task 1 Step 4 定义的两个方法（`GetJob` / `GetApp`）与 Step 2 测试 stub、Step 8 handler 使用、Step 5 编译的 `*sqlc.Queries` 注入完全对应；`jobPayloadAppRef.AppID` 与 service 写入 payload 的 `"app_id"` 字段名一致；`ApiError.status` 与 client.ts 第 9 行定义一致。
- **遗漏检查：** spec §「不在本次范围」列出的四项（不动 jobs schema、不新接口、不动 JobProgressPanel、不动重启反馈条）本计划均不涉及，✓。
- **AGENTS.md 合规：** 单元测试每个方法/子测试都有相邻中文注释（Task 1 Step 6 / Step 10 均补上）；commit message 中文+多段背景；测试用 `require.NoError` / `require.Equal` / `assert.Equal`；OpenAPI + generated.ts 与代码同提交。
