# OpenAPI code-first 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 swag v2 注解扫描从 Go handler 生成 `openapi/openapi.yaml`，前端 openapi-typescript 从 yaml 生成 `web/src/api/generated.ts` 取代手写 `types.ts`，建立后端代码 → OpenAPI 契约 → 前端类型 的单向同步链路。

**Architecture:** 7 个 task：(1) `internal/api/handlers/dto.go` 集中 24 个 handler 的请求体并改大写导出；(2) 引入 swag v2 + openapi-typescript 工具链 + Makefile 三个 target，main.go 加文档级注解，首批 auth+organizations 加 swag 注解，跑 `make openapi-gen` 验证 swag v2 alpha 可用（**这是验证窗口；如失败立即停下来报告**）；(3-5) 分三批给剩余 handler 加注解；(6) 前端切换 types → generated；(7) `make openapi-check` + AGENTS.md 约定 + 全量验收。

**Tech Stack:** Go 1.25 / Gin / swaggo/swag v2 (alpha) / OpenAPI 3.x / openapi-typescript 7.x / Vue 3 / TypeScript strict

**Spec reference:** `docs/superpowers/specs/2026-05-09-openapi-code-first-design.md`

**关键约束：**

- 每个 task 一个 commit，commit message 用 Conventional Commits 中文摘要 + Co-Authored-By（参考 AGENTS.md）。
- **swag v2 alpha 验证窗口在 Task 2**：如果 `make openapi-gen` 跑不通，**立即 stop 并告诉 controller**，不要自己切换工具链。退路（swag 1.16.x + swagger2openapi）由 controller 决定是否启用。
- handler 业务逻辑不变；现有所有测试必须保持通过。
- 工作目录 `/home/hujing/dir/software/ywjs/oc-manager`。

---

## Chunk 1: 集中 dto + 工具链验证

### Task 1: 把 24 个 handler 内 request 结构体集中到 `dto.go`，导出大写

**Files:**
- Create: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/*.go`（24 个文件，删本地 request struct 定义 + 把使用点改为大写）

**前置阅读：**
- `internal/api/handlers/organizations.go` — 看现有非导出请求结构体的命名与字段（如 `organizationRequest`）
- `internal/api/handlers/members.go` — 同上更复杂示例

**目标**：把 24 个 handler 文件中所有「handler 函数内/上方使用的请求体结构体定义」（小写非导出）搬到新建 `dto.go`，并改为大写导出。同包同名引用，import 不变；handler 函数内部对类型名引用从 `var req createMemberRequest` 改为 `var req CreateMemberRequest`。

不动：
- 响应类型（`service.XxxResult`）— 保留在 service 包
- handler 函数签名、router 注册、业务逻辑

- [ ] **Step 1.1: 全量盘点**

```bash
# 列出 24 个 handler 文件
ls internal/api/handlers/*.go | grep -v _test.go

# 把所有小写 request 结构体定义找出来（grep 不会百分百干净，要人工 review）
grep -nE '^type [a-z][a-zA-Z]+Request\b|^type [a-z][a-zA-Z]+Payload\b|^type [a-z][a-zA-Z]+Form\b' internal/api/handlers/*.go
grep -nE '^type [a-z][a-zA-Z]+Body\b' internal/api/handlers/*.go
# 还要看那些不带 Request/Payload 后缀的小写结构体（如 organizationRequest 实际名是 organizationRequest）
grep -nE '^type [a-z][a-zA-Z]+ struct \{' internal/api/handlers/*.go
```

记录下找到的所有结构体名 + 文件 + 行号到一个 mental list 或 scratch 文件，便于后续核对。

- [ ] **Step 1.2: baseline 测试**

```bash
go test ./internal/api/handlers/... -count=1
go vet ./...
```

预期全绿。如有 fail 先停下告诉 controller。

- [ ] **Step 1.3: 创建 dto.go 集中所有请求体**

创建 `internal/api/handlers/dto.go`，按业务对象分组（用注释分隔）：

```go
// Package handlers 内的 dto.go 集中所有请求体类型与共用错误响应。
// 类型导出（大写）以便 swag 扫描；命名前缀按业务对象归类。
package handlers

// ===== 通用 =====

// ErrorResponse 是所有 4xx / 5xx 失败响应的统一结构。
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// ===== organizations =====

// CreateOrganizationRequest 创建组织的请求体。
type CreateOrganizationRequest struct { /* 按现有 organizationRequest 字段照抄 */ }

// UpdateOrganizationRequest 编辑组织的请求体。
// ...

// ===== members =====
// ...

// ===== apps =====
// ...

// ===== runtime-nodes =====
// ...

// ===== knowledge =====
// ...

// ===== channels =====
// ...

// ===== persona =====
// ...

// ===== audit-logs =====
// ...

// ===== usage =====
// ...

// ===== runtime-operations =====
// ...

// ===== workspace =====
// ...

// ===== files =====
// ...

// ===== auth =====
// ...
```

每个 struct 的字段必须 1:1 复制原非导出版本（含 json tag、binding tag、注释）。**不要改动业务字段定义**。

- [ ] **Step 1.4: 各 handler 文件删本地定义 + 改大写引用**

对每个 handler 文件：
1. 删除文件内 `type xxxRequest struct {...}` 定义
2. 在 handler 函数体内 `var req xxxRequest` / `payload := xxxRequest{...}` / `c.ShouldBindJSON(&xxxRequest{})` 等所有引用点改为大写（`XxxRequest`）
3. import 不变（同包同名）

`organizations.go` 示例：

```go
// 改前
func (h *OrganizationsHandler) Create(c *gin.Context) {
	var req organizationRequest
	if err := c.ShouldBindJSON(&req); err != nil { ... }
	...
}

type organizationRequest struct {
	Name string `json:"name" binding:"required"`
	...
}

// 改后（dto.go 已经定义了 CreateOrganizationRequest；本文件只剩 handler）
func (h *OrganizationsHandler) Create(c *gin.Context) {
	var req CreateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil { ... }
	...
}
```

注意：

- 如果某 handler 同时有 Create / Update 两个端点，原可能共用一个 `organizationRequest`，搬到 dto.go 时拆为 `CreateOrganizationRequest` / `UpdateOrganizationRequest`（按各自端点 binding tag 差异决定是否拆）；如果原本就是同一份且端点共用，也可以只搬一份并起名 `OrganizationRequest`。**保持原行为，不要改 binding tag 和字段类型**。
- 如果 handler 内有不被 binding 的 internal struct（如转换中间结构），不归入 dto.go——只搬「请求体」类型。

- [ ] **Step 1.5: 验证 grep 干净**

```bash
# 旧的小写 struct 名不应再被引用
grep -rnE '\borganizationRequest\b|\bcreateMemberRequest\b|\bxxxRequest\b' internal/api/handlers/ | grep -v dto.go
```

预期：除 dto.go 外（如果你按推荐起的是大写名）无命中。如有命中说明替换不全，回去补。

```bash
# 确认 dto.go 至少包含 ErrorResponse 与多个 *Request
grep -cE '^type [A-Z][a-zA-Z]+Request\b|^type ErrorResponse\b' internal/api/handlers/dto.go
```

预期 ≥ 5（按业务量估算，至少 organizations / members / apps / runtime-nodes 各 1+ ErrorResponse 共 5）。

- [ ] **Step 1.6: vet + test**

```bash
go vet ./...
go test ./internal/api/handlers/... -count=1
go test ./... -count=1
```

全绿。如 fail，先停下告诉 controller，**不要乱改测试**——可能某测试 mock 了旧名字需要同步更新（这是合理的延伸，但需要 controller 决定）。

- [ ] **Step 1.7: 自检 + commit**

```bash
git status --short
```

应只看到：
- 新增 `internal/api/handlers/dto.go`
- 修改若干 `internal/api/handlers/*.go`
- 可能修改若干 `internal/api/handlers/*_test.go`（如 mock 类型名变了）

```bash
git add internal/api/handlers/
git commit -m "$(cat <<'EOF'
refactor(api): 把 24 个 handler 的请求体集中到 dto.go 并导出大写命名

为后续 swag 注解扫描做准备：swag 只能扫到导出（大写）类型。
本步骤仅搬家 + 改名，不动 binding tag、字段定义或业务逻辑。
响应类型仍保留在 service 包（service.XxxResult），由 swag 跨包扫描。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤300 字）

```
状态：DONE / DONE_WITH_CONCERNS / NEEDS_CONTEXT / BLOCKED

完成内容：
- dto.go 总行数 + struct 数量
- 改动文件清单（修改了多少个 *.go 文件 + 测试文件）
- baseline 与回归测试结果
- commit SHA

如有 concern（如多个端点共用 struct 但 binding 不同 / 测试 mock 类型名同步等）请简述。
```

---

### Task 2: 工具链准备 + main.go 文档级注解 + 首批 handler（auth + organizations）— **swag v2 alpha 验证窗口**

**Files:**
- Modify: `Makefile`（加 `openapi-gen / web-types-gen / openapi-check` 三 target）
- Modify: `web/package.json`（加 `openapi-typescript` devDependency）
- Modify: `web/.gitignore`（屏蔽 `swagger.json` / `docs/` 等 swag 中间产物，如有）
- Modify: `cmd/server/main.go`（头部加 swag 文档级注解）
- Modify: `internal/api/handlers/auth.go` + `organizations.go`（每个 endpoint 加 swag 注解）
- Replace: `openapi/openapi.yaml`（被 `make openapi-gen` 生成覆盖）

**关键约束**：本 task 是 swag v2 alpha 的验证窗口。如果 `make openapi-gen` 跑不通或产出明显错误，**立即停下来报告 controller**，不要自行切换到 swag 1.16.x + swagger2openapi 两步 pipeline。退路由 controller 决定是否启用。

- [ ] **Step 2.1: 调研 swag v2 当前可用版本**

```bash
go list -m -versions github.com/swaggo/swag/v2 2>&1 | tail -5
```

或 fallback：

```bash
curl -sf https://proxy.golang.org/github.com/swaggo/swag/v2/@v/list | sort -V | tail -10
```

挑一个 alpha / RC / latest 版本（如 `v2.0.0-rc.4`）。**记录这个版本号**，后续 Makefile 用 `SWAG_VERSION` 固定。

如果 swag v2 不存在或都是非常旧的 alpha：报告 controller，不要继续。

- [ ] **Step 2.2: 调研 openapi-typescript 当前稳定版**

```bash
cd web && npm view openapi-typescript version 2>&1 | tail -2
```

记录版本（如 `7.x.y`）。

- [ ] **Step 2.3: 修改 Makefile，加 3 个 target**

读 `Makefile` 现有结构（target 列表），在合适位置追加：

```makefile
SWAG_VERSION := v2.0.0-rc.4   # 用 Step 2.1 调研到的版本替换
OPENAPI_TS_VERSION := 7.4.0    # 用 Step 2.2 调研到的版本替换

.PHONY: openapi-gen
openapi-gen: ## 后端注解扫描，覆盖 openapi/openapi.yaml
	go run github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION) init \
		--generalInfo cmd/server/main.go \
		--dir cmd/server,internal/api/handlers,internal/service,internal/domain \
		--output openapi \
		--outputTypes yaml \
		--v3.1

.PHONY: web-types-gen
web-types-gen: ## 前端从 yaml 生成 TypeScript 类型
	cd web && npx openapi-typescript@$(OPENAPI_TS_VERSION) ../openapi/openapi.yaml -o src/api/generated.ts

.PHONY: openapi-check
openapi-check: openapi-gen ## 校验 yaml 是否与代码同步（git 工作区干净才过）
	@git diff --exit-code openapi/openapi.yaml \
		|| (echo "❌ openapi/openapi.yaml 与代码不同步，请跑 make openapi-gen 并 commit"; exit 1)
	@echo "✅ openapi.yaml 与代码同步"
```

注意：
- `--v3.1` 是 swag v2 让产出 OpenAPI 3.x 的关键 flag；如果版本里 flag 名不同（如 `--openapi-version 3.0`），按实际 swag 帮助页改
- 如果 swag init 不接受 `--outputTypes yaml`（v2 的 flag 可能变名），按 swag v2 实际 help 调整
- spec 落地后，下面 swag init 命令的 flag 必须 verified by `swag init --help`

- [ ] **Step 2.4: 加 openapi-typescript 到 web/package.json**

```bash
cd web && npm install --save-dev openapi-typescript@7.4.0
```

或手工编辑 `web/package.json` 的 devDependencies 加这一行，再 `cd web && npm install`。版本号 = Step 2.2 调研结果。

- [ ] **Step 2.5: 加 .gitignore 条目**

读 `web/.gitignore`（或仓库根 `.gitignore`），在合适位置加：

```
# swag 中间产物（仅 yaml 是契约源；docs.go 与 swagger.json 不入仓）
docs.go
docs/swagger.json
docs/swagger.yaml
docs/docs.go
```

- [ ] **Step 2.6: cmd/server/main.go 加文档级注解**

读 `cmd/server/main.go` 头部（package 行上方），在 package 上方插入：

```go
// Package main 是 manager-api HTTP 服务入口。
//
// @title           OpenClaw Manager API
// @version         1.0
// @description     OpenClaw 多组织管理后台 API。
// @BasePath        /api/v1
//
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     登录后获得的 JWT access token，前缀 "Bearer "。
package main
```

注意：v2 的 securityDefinitions 语法可能略有差异（如 `@securitydefinitions` 或 yaml 风格）。先按 swag v1 兼容写法，跑 `make openapi-gen` 看产出，按 swag v2 实际错误调整。

- [ ] **Step 2.7: auth.go 每个 endpoint 加 swag 注解**

读 `internal/api/handlers/auth.go`，识别所有 handler 函数（如 `Login / Refresh / Logout` 等）。在每个函数前加注解。

`Login` 示例：

```go
// Login 平台/组织成员凭用户名密码登录。
//
// @Summary      登录
// @Description  返回 access_token + refresh_token + Principal 快照
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "登录请求"
// @Success      200   {object}  service.LoginResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) { ... }
```

注意：
- 登录类 endpoint 通常不需要 `@Security BearerAuth`（未登录态）；其他 endpoint 默认要加
- response 类型按 service 层实际类型名（如 `service.LoginResult`）；如果 service 层用了别的命名，按实际写

- [ ] **Step 2.8: organizations.go 每个 endpoint 加 swag 注解**

同 Step 2.7 模式。`organizations.go` 通常含 `Create / List / Get / Update / Disable / Enable / SoftDelete` 等。每个 endpoint 加完整注解。

- [ ] **Step 2.9: 第一次跑 `make openapi-gen` 验证**

```bash
make openapi-gen 2>&1 | tail -40
```

预期输出：
- swag 报告找到 N 个端点
- 生成 `openapi/openapi.yaml` 替换原文件
- 退出码 0

如果失败（错误消息任何 `unknown flag` / `parse error` / `cannot find package` 等）：
- **stop，把完整错误输出贴给 controller**，等 controller 决定是退路（切换到 swag 1.x + swagger2openapi）还是调整 Makefile flag

如果成功：

```bash
git diff --stat openapi/openapi.yaml
head -30 openapi/openapi.yaml
```

确认：
- 顶部是 `openapi: 3.x.x`（不是 `swagger: '2.0'`）
- `info.title: OpenClaw Manager API`
- `paths.` 含 `/auth/login` 与 `/organizations`（首批 handler）

如果产出是 swagger 2.0 格式（顶部有 `swagger: '2.0'`），意味着 `--v3.1` flag 没生效，stop 报告 controller。

- [ ] **Step 2.10: vet + test 全过**

```bash
go vet ./...
go test ./... -count=1
```

预期：vet 无新增警告（注解是注释，不影响编译）；测试全绿。

- [ ] **Step 2.11: 自检 + commit**

```bash
git status --short
git diff --stat
```

预期 staged 文件：
- `Makefile`
- `web/package.json` + `web/package-lock.json`
- `.gitignore` 或 `web/.gitignore`
- `cmd/server/main.go`
- `internal/api/handlers/auth.go`
- `internal/api/handlers/organizations.go`
- `openapi/openapi.yaml`（被生成覆盖）

```bash
git add Makefile web/package.json web/package-lock.json .gitignore web/.gitignore \
  cmd/server/main.go internal/api/handlers/auth.go internal/api/handlers/organizations.go \
  openapi/openapi.yaml
git commit -m "$(cat <<'EOF'
feat(api): 引入 swag v2 + openapi-typescript 工具链；首批 handler 加注解

工具链：
- Makefile 新增 openapi-gen / web-types-gen / openapi-check 三 target，
  swag 与 openapi-typescript 版本固定在 SWAG_VERSION / OPENAPI_TS_VERSION
- web/package.json 加 openapi-typescript devDependency
- .gitignore 屏蔽 swag 中间产物（docs.go / swagger.json）

代码：
- cmd/server/main.go 头部加 swag 文档级注解（@title / @BasePath /
  @securityDefinitions BearerAuth）
- auth.go / organizations.go 每个 handler 函数加 @Summary / @Tags / @Param /
  @Success / @Failure / @Router / @Security 注解

openapi/openapi.yaml 由 make openapi-gen 生成覆盖；后续 task 给剩余 handler
加注解时再次重生。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤350 字）

```
状态：DONE / BLOCKED

工具链：
- swag v2 实际版本：vX.Y.Z
- openapi-typescript 版本：N.N.N

swag 验证窗口：
- make openapi-gen 是否跑通：Y / N
- 如 N，错误消息（最后 30 行 stderr）：
- 产出 yaml 顶部 5 行（确认是 OpenAPI 3.x 不是 2.0）：

注解：
- main.go 文档级注解已加：Y / N
- auth.go endpoints 加注解数：N
- organizations.go endpoints 加注解数：N

测试 / vet 结果：
- commit SHA：

如有 BLOCKED 请详细描述卡点。
```

---

## Chunk 2: 剩余 handler 加注解

### Task 3: members + apps + runtime-nodes 加 swag 注解

**Files:**
- Modify: `internal/api/handlers/members.go`
- Modify: `internal/api/handlers/apps.go`
- Modify: `internal/api/handlers/runtime_nodes.go`
- Modify: `openapi/openapi.yaml`（由 make openapi-gen 重生）

**前置**：Task 2 已验证 swag v2 工作；现在批量加注解。

- [ ] **Step 3.1: 阅读首批已加注解的 handler 作为模板**

读 `internal/api/handlers/auth.go` 与 `organizations.go` 头几个 handler 函数，理解 Task 2 用的注解格式（`@Summary @Tags @Accept @Produce @Security @Param @Success @Failure @Router`）。后续按相同风格加。

- [ ] **Step 3.2: members.go 加注解**

每个 handler 函数（如 `List / Create / Get / SetRole / SetStatus / DeleteMember / ResetPassword` 等）按 spec 第 4.6 节模板加注解。

`Create` 示例：

```go
// Create 创建成员并初始化关联应用。
//
// @Summary      创建组织成员
// @Description  组织管理员或平台管理员创建一个新成员，并触发 onboarding（创建唯一应用 + 初始化 newapi 凭证）
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateMemberRequest  true  "成员创建请求"
// @Success      201   {object}  service.MemberResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Router       /members [post]
func (h *MembersHandler) Create(c *gin.Context) { ... }
```

list 类端点用 `{array}` 修饰：`@Success 200 {array} service.MemberResult`。

- [ ] **Step 3.3: apps.go 加注解**

`apps.go` 含 `List / Get / Trigger / Delete` 等。按相同模式加。

- [ ] **Step 3.4: runtime_nodes.go 加注解**

含 `List / Register / Get / SetMaxApps / Disable / Enable / RotateBootstrap` 等。

- [ ] **Step 3.5: 跑 `make openapi-gen` + 验证**

```bash
make openapi-gen 2>&1 | tail -20
```

预期生成成功。

```bash
grep -cE '^  /members\|^  /apps\|^  /runtime-nodes' openapi/openapi.yaml
head -50 openapi/openapi.yaml
```

确认 paths 节含新加的端点。

- [ ] **Step 3.6: vet + test + commit**

```bash
go vet ./...
go test ./... -count=1
```

```bash
git add internal/api/handlers/members.go internal/api/handlers/apps.go \
  internal/api/handlers/runtime_nodes.go openapi/openapi.yaml
git commit -m "$(cat <<'EOF'
feat(api): members / apps / runtime-nodes handler 加 swag 注解

每个 handler 函数前加 @Summary / @Tags / @Param / @Success / @Failure /
@Router / @Security 注解；list 端点用 {array} 修饰响应类型。

openapi/openapi.yaml 由 make openapi-gen 重生覆盖。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤200 字）

```
状态：DONE / BLOCKED
- 三个 handler 文件加注解的 endpoint 数：N+N+N
- make openapi-gen 跑通：Y / N
- yaml paths 含 /members /apps /runtime-nodes：Y / N
- test / vet 结果
- commit SHA
```

---

### Task 4: knowledge + channels + persona + audit-logs + usage 加注解

**Files:**
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/channels.go`
- Modify: `internal/api/handlers/persona.go`
- Modify: `internal/api/handlers/audit_logs.go`（实际文件名以 `ls` 结果为准）
- Modify: `internal/api/handlers/usage.go`
- Modify: `openapi/openapi.yaml`

- [ ] **Step 4.1: 逐文件加注解**

按 Task 3 模式逐个文件加注解。注意几个特殊场景：

- **knowledge.go** 含文件上传：用 `@Accept multipart/form-data` + `@Param file formData file true "文件"`
- **audit_logs.go** 通常只读：操作只有 GET，注意 `@Param` 用 `query` 类型描述过滤参数
- **persona.go** / **usage.go** 都是普通 JSON CRUD，按标准模板

- [ ] **Step 4.2: 跑 `make openapi-gen`**

```bash
make openapi-gen 2>&1 | tail -10
```

如有 swag 解析错误（特别是 multipart / formData 的注解语法），先停下告诉 controller。

- [ ] **Step 4.3: vet + test + commit**

```bash
go vet ./...
go test ./... -count=1

git add internal/api/handlers/knowledge.go internal/api/handlers/channels.go \
  internal/api/handlers/persona.go internal/api/handlers/audit_logs.go \
  internal/api/handlers/usage.go openapi/openapi.yaml
git commit -m "$(cat <<'EOF'
feat(api): knowledge / channels / persona / audit-logs / usage handler 加注解

knowledge 端点含 multipart/form-data 上传，audit-logs 端点用 query 参数描述
过滤条件，其他按标准 JSON CRUD 模式。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤200 字）：同 Task 3 风格

---

### Task 5: 收尾 — runtime-operations + workspace + files + 剩余 handler

**Files:**
- Modify: `internal/api/handlers/runtime_operations.go`
- Modify: `internal/api/handlers/workspace.go`
- Modify: `internal/api/handlers/files.go`（如存在）
- Modify: 仍未加注解的 handler（grep 找）
- Modify: `openapi/openapi.yaml`

- [ ] **Step 5.1: 找剩余未加注解的 handler**

```bash
# 找所有 handler 公共方法（receiver 是 *XxxHandler 的 func）
grep -nE '^func \(h \*[A-Z][a-zA-Z]+Handler\) [A-Z]' internal/api/handlers/*.go | grep -v _test.go | wc -l
```

记录总数。然后看哪些函数前**没有**`@Summary` 注解：

```bash
# 找还没加注解的（heuristic：函数前 5 行无 @Summary）
for f in internal/api/handlers/*.go; do
  [[ "$f" == *_test.go ]] && continue
  # awk 找出每个 func 行号，回溯 5 行看是否有 @Summary
  awk '/^func \(h \*[A-Z][a-zA-Z]+Handler\) [A-Z]/{
    found=0
    for (i=NR-6; i<NR; i++) {
      if (lines[i] ~ /@Summary/) { found=1; break }
    }
    if (!found) print FILENAME":"NR": "$0
  }
  { lines[NR]=$0 }' "$f"
done
```

或者更简单：跑 `make openapi-gen` 看 swag 报告找到多少个 endpoint，对比 `paths` 节实际生成数量。

- [ ] **Step 5.2: 给剩余 handler 加注解**

按已有模式加。**特殊场景**：

- **workspace.go** 含 `Archive`（二进制 zip 流响应）：用 `@Produce application/octet-stream` + `@Success 200 "二进制流"`
- **runtime_operations.go**：所有端点都用 `service.RuntimeOperationResult` 作为成功响应；含「轮换 bootstrap」「触发 docker 操作」等

- [ ] **Step 5.3: 跑 `make openapi-gen` 最后一次（覆盖所有 handler）**

```bash
make openapi-gen 2>&1 | tail -10
```

预期：swag 报告找到 ~44 个 endpoint（与 spec 调研一致）。

```bash
grep -c '^  /' openapi/openapi.yaml
```

预期 ≥ 38（与原 yaml paths 数量级一致；可以多于原数，因为 spec 可能漏盘点）。

- [ ] **Step 5.4: 全量校验**

```bash
go vet ./...
go test ./... -count=1
make openapi-check 2>&1
```

`make openapi-check` 应该 ✅（因为 Step 5.3 已经 make openapi-gen 一次，工作区干净后才 commit）。

但实际 commit 流程是先生成再 commit，所以 openapi-check 现在应该过——除非有未 add 的更新。

- [ ] **Step 5.5: 自检 + commit**

```bash
git status --short
```

应只有 handler 文件 + `openapi/openapi.yaml` 的改动。

```bash
git add internal/api/handlers/ openapi/openapi.yaml
git commit -m "$(cat <<'EOF'
feat(api): runtime-operations / workspace / files / 剩余 handler 加注解

收尾 batch。workspace.Archive 二进制流响应用 @Produce application/octet-stream；
runtime-operations 全部用 service.RuntimeOperationResult。

至此 24 个 handler 全部加注解，openapi.yaml 完全由 swag 生成。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤250 字）

```
状态：DONE / BLOCKED
- 本批加注解 endpoint 数
- 全仓 endpoint 总数（grep 验证）
- yaml paths 总数
- 任何 swag 解析特殊问题（二进制流 / 上传等）
- test / vet / openapi-check 结果
- commit SHA
```

---

## Chunk 3: 前端切换 + 验收

### Task 6: 前端切换 types.ts → generated.ts

**Files:**
- Create: `web/src/api/generated.ts`（由 `make web-types-gen` 生成）
- Create: `web/src/api/index.ts`（集中类型 alias）
- Delete: `web/src/api/types.ts`
- Modify: `web/src/api/hooks/*.ts`（13 个 hook 文件，改 import）

- [ ] **Step 6.1: 生成 generated.ts**

```bash
make web-types-gen 2>&1 | tail -10
```

预期：成功生成 `web/src/api/generated.ts`，包含 `paths` 和 `components` 两个顶级 export。

```bash
head -30 web/src/api/generated.ts
wc -l web/src/api/generated.ts
```

确认顶部含 `// This file was auto-generated by openapi-typescript.` 这类标识；行数预期 500-2000 行。

- [ ] **Step 6.2: 创建 index.ts 集中 alias**

新建 `web/src/api/index.ts`：

```ts
// 对外暴露的业务类型 alias，从生成的 schema 派生。
// 各 hook 文件应 `import type { Member } from '@/api'` 而非直接引用 generated.ts，
// 让重命名 / 拆分对调用方透明。
import type { components } from './generated'

// 业务对象
export type Organization = components['schemas']['Organization']
export type Member = components['schemas']['Member']
export type App = components['schemas']['App']
export type RuntimeNode = components['schemas']['RuntimeNode']
export type AuditLog = components['schemas']['AuditLog']
export type OrgPersona = components['schemas']['OrgPersona']
// ...其他业务类型 alias

// 鉴权
export type AuthUser = components['schemas']['AuthUser']
export type TokenPair = components['schemas']['TokenPair']
export type LoginResult = components['schemas']['LoginResult']
// ...

// 请求 payload（对应 swag 的 *Request 类型）
export type CreateMemberRequest = components['schemas']['CreateMemberRequest']
export type CreateOrganizationRequest = components['schemas']['CreateOrganizationRequest']
// ...

// 重导出 generated.ts（如有 hook 直接用 paths 类型）
export type { paths, components } from './generated'
```

具体哪些 alias 必要：grep 现有 `web/src/api/types.ts` 的 export 清单 + `web/src/api/hooks/*.ts` 中的本地类型（如 `OnboardMemberPayload`），逐一映射到 `components['schemas'][...]`。如果 schema 名不同（swag 生成的 schema 可能叫 `handlers.CreateMemberRequest` 含包前缀），按 `components` 实际 key 写。

- [ ] **Step 6.3: 改各 hook 文件 import**

```bash
ls web/src/api/hooks/*.ts
```

逐文件处理：

- 把 `import type { Xxx } from '@/api/types'` 改为 `import type { Xxx } from '@/api'`
- hook 内本地定义的 payload 类型（如 `OnboardMemberPayload`）也改为从 `@/api` import

例：

```ts
// 改前 (web/src/api/hooks/useMembers.ts)
import type { Member } from '@/api/types'
type OnboardMemberPayload = { ... }

// 改后
import type { Member, CreateMemberRequest } from '@/api'
// 不再有本地 OnboardMemberPayload；直接用 CreateMemberRequest
```

注意：如果 hook 中的本地 payload 类型与 `CreateXxxRequest` schema 不完全一致（如 hook 多了几个客户端字段），保留本地类型，但 base 字段从 `CreateXxxRequest` 派生：

```ts
import type { CreateMemberRequest } from '@/api'
type OnboardMemberPayload = CreateMemberRequest & { extraClientField?: string }
```

- [ ] **Step 6.4: 删除手写 types.ts**

```bash
git rm web/src/api/types.ts
```

如果有别的文件还 import 自 `@/api/types` 而不是 `@/api`：

```bash
git grep -nE "from ['\"]@/api/types['\"]" web/src/
```

应输出空。如有命中，全部改为 `from '@/api'`。

- [ ] **Step 6.5: typecheck + test + build**

```bash
cd web && npm run typecheck 2>&1 | tail -15
cd web && npm run test --run 2>&1 | tail -10
cd web && npm run build 2>&1 | tail -10
```

预期：typecheck 无错（这是关键 — 派生类型必须能流通）；test 与 baseline 等价（4 个 ConfirmActionModal known issue 不归本 task）；build 成功。

如 typecheck 报错（比如 `Property 'foo' does not exist on type 'Member'`）：
- 看 `web/src/api/generated.ts` 中 `Member` schema 实际字段
- 与 hook 调用方期望对比，可能是 swag 生成的字段名与原 types.ts 略有差异（如蛇形 vs 驼峰、可选字段 vs 必填）
- 必要时回 handler 加 swag 注解（@Param body 字段标注 required）让生成结果对齐
- 如差异严重 stop 报告 controller

- [ ] **Step 6.6: 自检 + commit**

```bash
git status --short
```

预期：
- `D web/src/api/types.ts`
- `A web/src/api/generated.ts`
- `A web/src/api/index.ts`
- 多个 `M web/src/api/hooks/*.ts`

```bash
git add web/src/api/ web/src/pages/ # 如果 pages 也有 import 改动
git commit -m "$(cat <<'EOF'
refactor(web): 切换为 openapi-typescript 生成的类型，删手写 types.ts

- web/src/api/generated.ts 由 make web-types-gen 生成（入 git）
- web/src/api/index.ts 新增，集中业务类型 alias，hook 与 page 从 @/api 导入
- web/src/api/types.ts 删除
- 各 hook 中本地 payload 类型从 components['schemas'][...] 派生

至此前后端类型走单向同步链路：handler swag 注解 → openapi.yaml →
generated.ts → @/api。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤250 字）

```
状态：DONE / BLOCKED
- generated.ts 行数
- index.ts 中 alias 数量
- 改动 hook 文件数
- typecheck / test / build 结果
- 任何 schema 字段不对齐问题 + 解决方式
- commit SHA
```

---

### Task 7: openapi-check + AGENTS.md + 全量验收

**Files:**
- Modify: `AGENTS.md`（加 OpenAPI 同步约定段落）

- [ ] **Step 7.1: 跑 `make openapi-check`**

```bash
make openapi-check 2>&1
```

预期输出 `✅ openapi.yaml 与代码同步`。

如有 diff（说明前面 task 跑 make openapi-gen 后 yaml 又被手动改过、或 swag 不同次运行结果略有不同）：

```bash
git diff openapi/openapi.yaml
```

看 diff 内容：
- 如果是排序差异（schema 顺序、定义顺序）：swag 输出不稳定，可以接受 commit 一次让 yaml stabilize
- 如果是字段差异：必有 handler 注解未跟上代码，定位修正

- [ ] **Step 7.2: AGENTS.md 增加 OpenAPI 同步段落**

读 `AGENTS.md`，在合适位置（建议「权限校验」节之后）插入：

```markdown
## OpenAPI 同步

- API 契约由 swag 注解扫描生成 `openapi/openapi.yaml`，前端类型由
  `make web-types-gen` 从 yaml 生成 `web/src/api/generated.ts`。两个文件都入
  git，提交时必须保持同步。
- 修改 handler 函数签名 / 请求体 / 响应类型 / 路由后，必须跑 `make openapi-gen`
  + `make web-types-gen`，把变更连同代码一起提交。
- `make openapi-check` 用于本地校验：跑 `make openapi-gen` 后 git 工作区应保持干净，
  否则说明 yaml 未跟随代码更新。
- 新增 handler 时，请求体类型放 `internal/api/handlers/dto.go` 并导出大写命名；
  响应仍用 `service.XxxResult`（swag 跨包扫描）。
- 不要手工编辑 `openapi/openapi.yaml` 与 `web/src/api/generated.ts`——它们是
  生成产物。
```

- [ ] **Step 7.3: 全量 DoD 验收**

逐项跑命令并记录结果：

```bash
# DoD-1: dto.go 存在
ls internal/api/handlers/dto.go

# DoD-2: 24 个 handler 不再持有内部小写 request struct
grep -nE '^type [a-z][a-zA-Z]+Request\b|^type [a-z][a-zA-Z]+Form\b' internal/api/handlers/*.go | grep -v dto.go
# 预期为空

# DoD-3: main.go 含 @title 等文档级注解
grep -E '@title|@BasePath|@securityDefinitions' cmd/server/main.go
# 预期 ≥ 3 行命中

# DoD-4: 所有 handler 函数前有 @Summary 注解
total_handlers=$(grep -nE '^func \(h \*[A-Z][a-zA-Z]+Handler\) [A-Z]' internal/api/handlers/*.go | grep -v _test.go | wc -l)
total_summaries=$(grep -B 6 -E '^func \(h \*[A-Z][a-zA-Z]+Handler\) [A-Z]' internal/api/handlers/*.go | grep -c '@Summary')
echo "handlers=$total_handlers summaries=$total_summaries"
# 预期 total_handlers == total_summaries

# DoD-5: make openapi-gen 能跑
make openapi-gen 2>&1 | tail -3

# DoD-6: make web-types-gen 能跑
make web-types-gen 2>&1 | tail -3

# DoD-7: types.ts 已删
ls web/src/api/types.ts 2>&1
# 预期：no such file

# DoD-8: hook 全部 import @/api
git grep -nE "from ['\"]@/api/types['\"]" web/src/
# 预期为空

# DoD-9: make openapi-check 输出 ✅
make openapi-check 2>&1

# DoD-10: 全量 typecheck / build / test
make web-typecheck 2>&1 | tail -3
make web-build 2>&1 | tail -3
make test 2>&1 | tail -3

# DoD-11: AGENTS.md 含 OpenAPI 同步段落
grep -A 3 '^## OpenAPI 同步' AGENTS.md
```

- [ ] **Step 7.4: commit AGENTS.md**

```bash
git add AGENTS.md
git commit -m "$(cat <<'EOF'
docs(agents): 增加 OpenAPI 同步约定

明确 openapi.yaml 与 generated.ts 都是生成产物，必须跟随 handler 改动
同步提交；新增 handler 时请求体放 dto.go 并导出大写命名。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤300 字）

```
状态：DONE / BLOCKED

DoD 验收明细（逐项 Y/N）：
- DoD-1: dto.go 存在
- DoD-2: 内部小写 request struct grep 空
- DoD-3: main.go 含文档级注解
- DoD-4: handler 函数 vs @Summary 匹配数（N/N）
- DoD-5: make openapi-gen 跑通
- DoD-6: make web-types-gen 跑通
- DoD-7: types.ts 已删
- DoD-8: 无 @/api/types 残留 import
- DoD-9: make openapi-check ✅
- DoD-10: typecheck / build / test 结果
- DoD-11: AGENTS.md 含 OpenAPI 同步段落

commit SHA：

如有 BLOCKED 请详细描述卡点。
```

---

## 完成定义

所有 task 完成后必须满足：

- [ ] **DoD-1:** `internal/api/handlers/dto.go` 存在，集中所有请求体类型
- [ ] **DoD-2:** 24 个 handler 不再持有内部小写 request struct
- [ ] **DoD-3:** `cmd/server/main.go` 头部含 swag 文档级注解
- [ ] **DoD-4:** 所有 handler 函数前有完整 swag 注解
- [ ] **DoD-5:** `make openapi-gen` 跑通
- [ ] **DoD-6:** `make web-types-gen` 跑通
- [ ] **DoD-7:** `web/src/api/types.ts` 已删
- [ ] **DoD-8:** 所有 hook 文件 import `@/api` 或 `@/api/generated`
- [ ] **DoD-9:** `make openapi-check` 输出 ✅
- [ ] **DoD-10:** `make web-typecheck && make web-build && make test` 全过
- [ ] **DoD-11:** `AGENTS.md` 含「OpenAPI 同步」段落

---

## 回滚策略

每个 task 一个独立 commit，可单独 `git revert`。

最坏情况下：
- 想回退到 yaml 手写状态：`git revert` Task 7 → 1 倒序逐个 revert，或 `git reset --hard <Task 0 之前 commit>`（master 直接开发，需用户授权）
- Task 6（前端切换）独立可退；其他 task 互依（Task 3-5 依赖 Task 1-2）

---

## 风险与应对

| 风险 | 何时出现 | 应对 |
|---|---|---|
| swag v2 alpha 不工作 / 不接受 `--v3.1` flag | Task 2 Step 2.9 | **stop 报告 controller**；controller 决定是否切换到 swag 1.16.x + swagger2openapi 两步 pipeline；plan 暂不细写 fallback 路径，由 controller 当场重做 spec/plan |
| swag 跨包扫描 `service.XxxResult` 失败 | Task 2 Step 2.9 | 把响应类型也搬到 dto.go（spec 风险段已预见）；spec 范围扩大但可控 |
| swag 无法解析 binary stream / multipart 注解 | Task 5 Step 5.2 | 局部手 patch yaml（违背 code-first 但只局部），spec 风险段已说明 |
| 前端 typecheck 因 schema 字段命名差异（蛇形 vs 驼峰）失败 | Task 6 Step 6.5 | 在 swag 注解里 / Go struct json tag 里调整 case；如果 swag v2 自动转 case 与 openapi-typescript 不一致，需要在 generated.ts → index.ts 的 alias 层做映射 |
| handler 注解 `@Router` 路径写错（含 / 不含 /api/v1 前缀混用） | Task 2-5 任一 | spec 4.6 节明确：路径不含 /api/v1 前缀（已在 @BasePath 声明）；review 时核对 |
| 新增 handler 时开发者忘跑 `make openapi-gen` 导致 yaml 与代码漂移 | 改造完成后日常维护 | `make openapi-check` 提供本地校验路径；AGENTS.md 已加约定（DoD-11） |
