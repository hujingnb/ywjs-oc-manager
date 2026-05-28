# 组织改名为企业的用户可见文案设计

## 背景

当前系统以 `organization/org` 作为租户模型的内部名称，中文产品文案统一使用“组织”。为了让前台表达更贴近客户语境，本次将用户可见中文术语从“组织”调整为“企业”。

本次是展示术语调整，不改变租户模型、权限模型、API 契约的英文标识或数据库结构。内部继续使用 `organization` / `org`，例如 `org_id`、`org_admin`、`/api/v1/organizations`、`organizations` 表和 sqlc 类型。

## 目标

- 前端用户可见文案中，将“组织”统一替换为“企业”。
- 后端返回给用户或 OpenAPI 文档展示的中文说明中，将“组织”统一替换为“企业”。
- 正式产品和使用文档中，将面向用户的“组织”概念改为“企业”。
- 保持内部英文标识稳定，避免破坏 API、数据库、权限和既有集成。

## 非目标

- 不重命名 Go、TypeScript、SQL、OpenAPI schema 的英文类型、字段、方法、目录或文件。
- 不修改 API 路径，例如 `/api/v1/organizations` 继续保留。
- 不修改角色枚举值，例如 `org_admin` / `org_member` 继续保留。
- 不做数据库迁移，不修改表名、列名、索引名或历史数据。
- 不追改历史设计稿、实施计划和排查报告，例如 `docs/superpowers/**`、`docs/reports/**` 中的既有历史记录。
- 不引入 i18n 或术语配置层；本次按现有项目风格直接更新文案。

## 术语映射

| 原中文 | 新中文 | 说明 |
|---|---|---|
| 组织 | 企业 | 默认替换规则，适用于租户实体、列表、详情、选择器、知识库、审计、用量等场景。 |
| 组织标识 | 企业标识 | 登录命名空间 `code` 的展示名；内部字段仍是 `code` / `org_code`。 |
| 组织管理员 | 企业管理员 | 中文角色展示；内部角色值仍是 `org_admin`。 |
| 组织成员 | 企业成员 | 中文角色展示；内部角色值仍是 `org_member`。 |
| 本组织 | 本企业 | 权限说明和错误提示中的归属关系。 |
| 跨组织 | 跨企业 | 权限边界说明。 |
| 组织级知识库 | 企业级知识库 | 知识库层级展示名；内部 scope 仍可保留 `org`。 |

英文代码标识保留原样。中文注释如果只服务开发者理解内部 `org` 模型，可以保持“组织”；如果该注释会进入 Swagger、错误响应、页面测试断言或正式用户文档，应改为“企业”。

## 影响范围

### 前端 UI

需要覆盖 `web/src` 中所有用户可见中文：

- 菜单与首页入口：例如侧边栏“组织”、角色首页“组织管理/组织知识库”。
- 页面标题、eyebrow、tab、表格列、表单 label、placeholder、按钮、空态、提示和 toast。
- 登录相关：`组织标识` 改为 `企业标识`，平台管理员留空的说明同步调整。
- 权限说明页：角色列名和操作描述改为企业语义。
- 复制登录信息：复制内容中的“组织标识/名称”改为“企业标识/名称”。
- 前端测试断言中的用户可见中文同步改为新术语。

前端 helper、hook、路由、类型命名仍沿用现状，例如 `useOrganizations`、`OrganizationsPage`、`OrgKnowledgePage`、`formatOrgStatus` 不重命名。

### 后端中文输出与 OpenAPI 注解

需要覆盖 `internal` 中会被用户看到或生成进 OpenAPI 的中文：

- HTTP handler 的 Swagger 注解：`@Summary`、`@Description`、`@Param`、请求体/响应字段中文说明。
- DTO 注释中会进入 OpenAPI 的中文说明。
- service/handler 返回给前端的业务错误文案，例如“组织标识必须…”应改为“企业标识必须…”。
- 审计和状态展示中返回给前端的中文标签。
- 后端测试中对应错误文案和 OpenAPI 生成结果的断言。

开发者内部注释不强制全量替换，尤其是解释 `org_id`、`organization` 表、权限谓词或历史迁移语义的注释可保留“组织”，避免“企业”与内部英文命名产生误导。

### 正式文档

需要更新正式用户和产品文档：

- `README.md`
- `docs/product-design.md`
- `docs/user-manual.md`
- `docs/knowledge-base.md`
- `docs/architecture.md`
- `docs/local-development.md`
- 其它面向当前产品说明的正式文档，如果包含用户可见“组织”术语，也按同一规则处理。

历史文件不追改：

- `docs/superpowers/specs/**`
- `docs/superpowers/plans/**`
- `docs/reports/**`

## OpenAPI 与生成类型

如果实现阶段修改了 Swagger 注解或 DTO 注释，必须按仓库规则运行：

1. `make openapi-gen`
2. `make web-types-gen`
3. `make openapi-check`

`openapi/openapi.yaml` 和 `web/src/api/generated.ts` 不手工编辑，只由生成命令更新。生成产物中的英文字段和路径应保持不变，中文 description/summary 变为企业语义。

## 测试计划

- 前端：运行受影响的 Vitest 用例，重点覆盖登录、菜单、企业列表、知识库、成员、用量、审计、权限说明页。
- 后端：运行改动到的 handler/service/domain 测试，特别是错误消息断言和角色标签断言。
- OpenAPI：如果改了 Swagger 注解，运行 `make openapi-check` 验证契约生成一致。
- 浏览器验收：使用真实浏览器登录平台管理员和企业管理员账号，检查以下入口不再出现核心旧文案：
  - `/login`
  - `/organizations`
  - `/knowledge`
  - `/members`
  - `/usage`
  - `/audit-logs`
  - `/platform/permissions`

浏览器验收重点是用户可见界面，不要求历史报告、旧设计文档或内部英文标识消失。

## 风险与约束

- 最大风险是替换过度，误改 `org_id`、`org_admin`、API 路径、SQL 或内部类型名。实施阶段应优先按文件语义判断，不能机械全局替换。
- 第二个风险是替换不足，导致同一页面同时出现“组织”和“企业”。前端页面和正式文档需要用 `rg "组织"` 做人工复核，并区分用户文案与内部注释。
- OpenAPI 生成产物会产生较大 diff，但只要来源是 Swagger 注解和 DTO 注释变化即可接受。
- 本次不引入术语常量层，后续如果继续有多套客户术语，再单独设计 i18n 或 label registry。

## 验收标准

- 用户可见中文主路径统一使用“企业”语义。
- 内部英文协议和数据库命名无破坏性变化。
- 改过的测试通过；若因环境原因无法运行，交付说明明确列出未运行项和风险。
- 修改 Swagger 注解时，OpenAPI 和前端生成类型已同步生成。
- 真实浏览器验证通过，确认主导航和核心页面没有遗留“组织”作为租户展示术语。
