# OpenAPI code-first：swag 注解扫描 + 前端类型生成

- 日期：2026-05-09
- 范围：Go 后端 handler 注解 + Makefile + 前端类型层
- 主线项编号：A-3（出自 2026-05-09 全面体检报告）

## 1. 背景

体检发现 OpenAPI 与代码无机器同步：

- `openapi/openapi.yaml` 是手写（OpenAPI 3.0.3，1367 行，38 paths / 44 operations / 32 schemas，最后改于 9 天前 commit `eed9ac8`）
- 24 个 handler 文件（共 3326 行）的请求结构体散落定义、命名小写非导出（如 `createMemberRequest`）
- 响应类型靠 `service.XxxResult` 经 `c.JSON()` 序列化
- `web/src/api/types.ts`（73 行）手写 DTO，无生成痕迹；hook 里还有本地 `OnboardMemberPayload` 等业务 payload 类型
- 完全没有 swag/swaggo/openapi-typescript 工具链；Makefile 无 `openapi` target；无 build 校验同步

风险：契约和实现可静默偏离，等线上联调才发现。

## 2. 目标

- 后端引入 `swaggo/swag` 注解扫描，从 Go 代码生成 `openapi/openapi.yaml`（取代手写）
- 前端引入 `openapi-typescript` 从 yaml 生成 TS 类型，取代手写 `types.ts`
- Makefile 新增 `openapi-gen` / `web-types-gen` / `openapi-check` 三个 target
- handler 请求结构体集中整理到 `internal/api/handlers/dto.go`，导出大写命名

## 3. 非目标（避免范围蔓延）

- **不**暴露 Swagger UI（`gin-swagger` 不引入；交互浏览靠 yaml 文件 + 编辑器插件）
- **不**引入 CI / pre-commit hook（项目无 CI；Makefile `openapi-check` target 用于本地手工校验）
- **不**改动 service 层 `XxxResult` 类型（swag 跨包扫描，注解里直接引用 `service.OrganizationResult`）
- **不**改动 router.go 注册结构（保留 `RegisterXxxRoutes` 分散注册 + `NewRouter` 集中编排）
- **不**改动 Pinia store 与 TanStack Query hook 行为（仅替换类型 import 来源）
- **不**生成「按 endpoint 划分的客户端」（即不把 `apiRequest()` 替换为生成的 fetcher；只生成 schema 类型，请求逻辑保留手写）

## 4. 设计

### 4.1 关键决策（已与决策方对齐）

| 决策点 | 选择 | 理由 |
|---|---|---|
| handler 请求结构体位置 | **集中到 `internal/api/handlers/dto.go`**，导出大写命名 | swag 需导出类型才能扫到；集中便于查找；24 个 handler 共享一个文件总长度可控（预估 400-600 行） |
| 前端类型策略 | **openapi-typescript 生成全量**，删手写 `types.ts`；hook 里业务 payload 从 `components['schemas'][...]` 派生 | 单一真相源；hook 不再持有重复类型 |
| Swagger UI | **不暴露**（YAGNI） | 二进制保持干净；编辑器/Swagger Editor 直接看 yaml 即够 |
| OpenAPI 版本 | **swag v2 alpha 直出 OpenAPI 3.x** | 现有 yaml 是 3.0.3，沿用 3.x；接受 alpha 风险，spec 风险段定义退路 |

### 4.2 工具链选型

| 用途 | 工具 | 引入方式 |
|---|---|---|
| Go 注解扫描 | `github.com/swaggo/swag/v2/cmd/swag` | Makefile 用 `go run github.com/swaggo/swag/v2/cmd/swag@<pinned-version> init ...`；不入 `go.mod` 直接依赖（避免污染运行时依赖） |
| 前端类型生成 | `openapi-typescript`（latest stable） | `web/package.json` 的 `devDependencies`；通过 `npx openapi-typescript` 调用 |
| OpenAPI 验证 | swag 自身验证语法；额外不引入 | yaml 校验靠 swag init 出错回退 + 手工 review |

**版本固定**：spec 落地时把 swag v2 与 openapi-typescript 的具体版本号写到 Makefile + package.json（避免 `@latest` 漂移）。

### 4.3 后端文件结构

新增：
```
internal/api/handlers/
└── dto.go                ← 新增：集中所有请求体 + 错误响应类型
```

修改（24 个 handler 文件 + 1 个 main）：
```
cmd/server/main.go         ← 头部加 swag 文档级注解（@title @version @basePath @securityDefinitions）
internal/api/handlers/
├── auth.go                ← 改：删本地 request struct，引用 dto.X；加 swag 函数级注解
├── organizations.go       ← 同上
├── members.go             ← 同上
├── apps.go / runtime_nodes.go / knowledge.go / channels.go / 其余 17 个 handler ← 同上
└── ...
```

替换：
```
openapi/openapi.yaml       ← 由 swag init 生成覆盖
```

### 4.4 dto.go 结构示例

```go
// Package handlers 内的 dto.go 集中所有请求体类型与共用错误响应。
// 导出（大写）以便 swag 扫描，命名前缀按业务对象归类（CreateMember* / UpdateOrganization*）。
package handlers

// CreateOrganizationRequest 创建组织的请求体。
type CreateOrganizationRequest struct {
	Name                     string  `json:"name" binding:"required"`
	ContactName              string  `json:"contact_name,omitempty"`
	ContactPhone             string  `json:"contact_phone,omitempty"`
	Remark                   string  `json:"remark,omitempty"`
	CreditWarningThreshold   *int    `json:"credit_warning_threshold,omitempty"`
}

// UpdateOrganizationRequest 编辑组织的请求体。
type UpdateOrganizationRequest struct { /* ... */ }

// CreateMemberRequest 创建成员的请求体。
type CreateMemberRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"display_name,omitempty"`
	Password    string `json:"password" binding:"required,min=8"`
	Role        string `json:"role" binding:"required,oneof=org_admin org_member"`
}

// ...其他 20+ request 类型

// ErrorResponse 通用错误响应。所有 handler 的 4xx/5xx 都用这个类型。
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}
```

### 4.5 main.go 文档级注解

`cmd/server/main.go` 头部插入（在 `package main` 上方）：

```go
// Package main 是 manager-api HTTP 服务入口。
//
// @title           OpenClaw Manager API
// @version         1.0
// @description     OpenClaw 多组织管理后台 API
// @BasePath        /api/v1
//
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     登录后获得的 JWT access token，前缀 "Bearer "。
package main
```

### 4.6 handler 函数级注解模板

每个 handler 函数前固定 8-10 行注解。模板：

```go
// Create 创建组织。
//
// @Summary      创建组织
// @Description  平台管理员创建一个新组织，并初始化 new-api 凭证
// @Tags         organizations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateOrganizationRequest  true  "组织创建请求"
// @Success      201   {object}  service.OrganizationResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Router       /organizations [post]
func (h *OrganizationsHandler) Create(c *gin.Context) { ... }
```

注解规则：

- `@Tags` 按业务对象分组（organizations / members / apps / runtime-nodes / knowledge / audit-logs / channels / persona / usage / runtime-operations / workspace / files / auth）
- `@Param` 必填请求体一律 `body body XxxRequest true "中文描述"`；query 参数用 `@Param name query string false "..."`
- `@Success` 一律引用 `service.XxxResult`（跨包扫描）；列表接口加 `{array}` 修饰：`{array} service.MemberResult`
- `@Failure` 至少含 400 / 401 / 403 / 404 中适用的；都用 `ErrorResponse`
- `@Router` 路径不含 `/api/v1` 前缀（已在 `@BasePath` 声明）
- 二进制 / 流式响应（如 `workspace.Archive`）：`@Produce application/octet-stream` + `@Success 200 "二进制流"`
- 文件上传：`@Accept multipart/form-data` + `@Param file formData file true "文件"`

### 4.7 Makefile 新增 target

```makefile
SWAG_VERSION := v2.0.0-rc4   # spec 落地时按当时实际可用版本固定
OPENAPI_TS_VERSION := 7.x    # 同上

.PHONY: openapi-gen
openapi-gen:  ## 后端注解扫描，覆盖 openapi/openapi.yaml
	go run github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION) init \
		--generalInfo cmd/server/main.go \
		--dir cmd/server,internal/api/handlers,internal/service,internal/domain \
		--output openapi \
		--outputTypes yaml \
		--v3.1

.PHONY: web-types-gen
web-types-gen:  ## 前端从 yaml 生成 TypeScript 类型
	cd web && npx openapi-typescript ../openapi/openapi.yaml -o src/api/generated.ts

.PHONY: openapi-check
openapi-check: openapi-gen  ## 校验 yaml 是否与代码同步（预期 git 工作区干净）
	@git diff --exit-code openapi/openapi.yaml \
		|| (echo "❌ openapi/openapi.yaml 与代码不同步，请跑 make openapi-gen 提交"; exit 1)
	@echo "✅ openapi.yaml 与代码同步"
```

`make web-types-gen` 不依赖 `openapi-gen`（前端常基于已 commit 的 yaml 工作）；本地开发流程：改 handler → `make openapi-gen` → `make web-types-gen` → 提交三件改动。

### 4.8 前端类型生成产物

新增 `web/src/api/generated.ts`：由 openapi-typescript 输出，包含 `paths` 和 `components` 两个顶级 export。**入 git**（让 reviewer 能看到契约变化）。

`web/src/api/types.ts` **删除**。

各 hook 文件 import 改为：

```ts
// 改前
import type { Member, OnboardMemberPayload } from '@/api/types'

// 改后
import type { components } from '@/api/generated'
type Member = components['schemas']['Member']
type OnboardMemberPayload = components['schemas']['OnboardMemberRequest']
```

如重复声明 alias 较多，可在 `web/src/api/index.ts`（新建）中集中：

```ts
import type { components } from './generated'
export type Member = components['schemas']['Member']
export type Organization = components['schemas']['Organization']
// ...其余 alias
```

让 hook 文件 `import type { Member } from '@/api'`，与改造前 import 路径接近。

### 4.9 .gitignore / 入仓策略

| 文件 | 入 git | 理由 |
|---|---|---|
| `openapi/openapi.yaml` | ✅ | 契约变化必须可 review |
| `web/src/api/generated.ts` | ✅ | 同上；前端构建不依赖运行时生成 |
| `swagger.json` 等 swag 产生的中间文件 | ❌ `.gitignore` 屏蔽 | 仅 yaml 是契约源 |

## 5. 改造分批策略

24 个 handler 一次性加注解风险大。按业务域分批：

| 批次 | handler | 端点数预估 | 备注 |
|---|---|---|---|
| 1 | dto.go 集中（请求结构体搬家 + 大写导出，不加注解） | — | 24 个 handler 改为 import dto.X |
| 2 | 工具链 + Makefile target；main.go 文档级注解；首批 handler（auth + organizations） | ~6 endpoint | 跑 `make openapi-gen` 第一次成功生成可用 yaml |
| 3 | members + apps + runtime-nodes | ~12 endpoint | 主线业务 |
| 4 | knowledge + channels + persona + audit-logs + usage | ~10 endpoint |  |
| 5 | runtime-operations + workspace + files + 其他剩余 | ~8 endpoint |  |
| 6 | 前端切换：generated.ts 引入 + 更新 hook import + 删 types.ts | — |  |
| 7 | Makefile `openapi-check` 校验通过；最终验收 | — |  |

每批完结时跑 `make openapi-gen` 看产出 diff，确认无意外缺失。

## 6. 测试策略

- **后端**：handler 业务逻辑不变，现有 `internal/api/handlers/*_test.go`、`internal/service/*_test.go` 全部保持通过；不补 swag 注解的单测（注解是元数据，正确性靠 `swag init` 不出错 + 生成 yaml 人工 review）
- **前端**：`web/src/api/generated.ts` 不写单测（生成产物）；现有 hook 测试（如有）通过即可；`make web-typecheck` 必须无新增错
- **校验**：`make openapi-check` 在 spec 完成后执行一次，确认本地工作区干净

## 7. 风险与缓解

| 风险 | 严重度 | 缓解 |
|---|---|---|
| swag v2 alpha 版本有未修 bug，无法生成可用 yaml | **高** | 退路：回退到 `swag 1.16.x` + `swagger2openapi`（两步 pipeline）。spec 落地的 batch 2（工具链）是验证窗口；如失败立即切换方案，spec 此节同步更新 |
| 跨包扫描 `service.XxxResult` 失败（swag 文档对跨包有限制） | 中 | batch 2 验证；如失败将响应类型也搬到 dto.go（再加 600 行；可接受） |
| 文件上传 / 二进制流注解 swag 表现力不够 | 低 | 实施时手工 review 这两个 endpoint 的生成产物；必要时 yaml 手 patch（违背 code-first 但只局部） |
| handler 内 request struct 改大写导致编译错（包外引用，但实际是包内） | 低 | dto.go 与 handler 同包，无 import path 改动；编译期立即暴露 |
| openapi-typescript 与 swag v2 输出格式不兼容（如 schema 命名空间冲突） | 中 | batch 6 验证；如冲突需要手工 alias 或 swag 注解里调整 schema name |
| 生成 yaml 比手写 yaml 缺少描述 / 示例（手写 yaml 信息更丰富） | 中 | 接受这条 trade-off：注解阶段把核心 description 加上；example / extension 字段留待未来补 |
| 「无 CI」意味着开发者忘跑 openapi-gen 时 yaml 与代码不同步 | 中 | `make openapi-check` 提供主动校验路径；AGENTS.md 增加一条「改 handler 必须跑 openapi-gen」 |

## 8. 完成定义（DoD）

- [ ] `internal/api/handlers/dto.go` 存在，集中所有请求体类型
- [ ] 24 个 handler 文件不再持有内部小写 request struct（grep 验证）
- [ ] `cmd/server/main.go` 顶部含 swag 文档级注解
- [ ] 所有 handler 函数前有完整 swag 注解（@Summary / @Tags / @Router / @Success / @Failure / @Security 至少齐全）
- [ ] `make openapi-gen` 能跑通，覆盖 `openapi/openapi.yaml`
- [ ] `make web-types-gen` 能跑通，生成 `web/src/api/generated.ts`
- [ ] `web/src/api/types.ts` 已删除
- [ ] 所有 hook 文件 import 改为 `@/api/generated` 或 `@/api`
- [ ] `make openapi-check` 输出 ✅
- [ ] `make web-typecheck && make web-build && make test` 全过
- [ ] AGENTS.md 增加 OpenAPI 同步约定段落

## 9. 后续

- 本 spec 落地后进入 writing-plans 出更细的 task 拆分。
- 「contract 描述/示例丰富化」「response 类型也搬到 dto」「Swagger UI 暴露」等留给后续独立 spec。
