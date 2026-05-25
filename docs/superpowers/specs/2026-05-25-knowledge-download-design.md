# 设计文档：知识库单文件下载

## 背景

当前组织级知识库和实例级知识库已经支持列表、上传和删除。组织成员可以在
`/knowledge` 浏览组织共享知识库，但不能下载文件；实例知识库也缺少下载入口。
工作目录已经有受保护的单文件下载能力，可作为前后端实现模式参考。

本次需求已确认：

- 组织级知识库和实例级知识库都支持单文件下载。
- 不做目录打包下载，也不做整个知识库归档下载。
- 平台管理员、组织管理员、组织成员只要具备对应知识库的读取权限，就可以下载。
- 下载来源使用 manager 本地知识库主副本，不依赖 runtime node 同步状态。

## 目标

1. 让所有可读用户可以下载组织级知识库中的普通文件。
2. 让所有可读用户可以下载实例级知识库中的普通文件。
3. 下载权限沿用现有 `CanReadOrgKnowledge` 和 `CanReadAppKnowledge`，不新增权限概念。
4. 列表和下载读取同一个 manager 主副本，避免列表可见但下载依赖节点同步状态。
5. 保持 OpenAPI、前端类型、单元测试和浏览器验证同步。

## 非目标

- 不支持目录 zip 下载。
- 不支持整个知识库打包下载。
- 不改变上传、删除、同步任务或同步状态展示逻辑。
- 不改变 runtime agent 的知识库同步接口。
- 不调整知识库目录结构或数据库 schema。

## 后端设计

在现有 `KnowledgeHandler` 下新增两个下载接口：

- `GET /api/v1/organizations/{orgId}/knowledge/file?path=...`
  - 下载组织级知识库单个文件。
  - 权限使用 `auth.CanReadOrgKnowledge`。
  - platform_admin 可跨组织下载；org_admin 和 org_member 只能下载本组织文件。
- `GET /api/v1/apps/{appId}/knowledge/file?org_id=...&owner_user_id=...&path=...`
  - 下载实例级知识库单个文件。
  - 权限使用 `auth.CanReadAppKnowledge`。
  - platform_admin 可下载全部实例知识库；org_admin 可下载本组织应用；org_member 只能下载自己应用。

`KnowledgeService` 增加只读方法，例如：

- `OpenOrgFile(ctx, principal, orgID, relative) (io.ReadCloser, int64, error)`
- `OpenAppFile(ctx, principal, orgID, appID, ownerUserID, relative) (io.ReadCloser, int64, error)`

方法内部复用现有路径拼装规则：

- 组织级：`org/{orgID}/knowledge/{relative}`
- 实例级：`org/{orgID}/app/{appID}/knowledge/{relative}`

文件打开复用 `files.KnowledgeMaster.Open`。`SafeRoot.Resolve` 继续作为路径边界兜底，
拒绝绝对路径和 `..` 越界路径。目标为目录、目标不存在或无法打开时，先沿用现有
`writeKnowledgeError` 的 `400 BAD_REQUEST` 处理，不新增错误码。

HTTP handler 行为：

- 缺少必填 query 参数时返回 `400 BAD_REQUEST`。
- 成功时返回 `200`，`Content-Type: application/octet-stream`。
- 设置 `Content-Disposition`，文件名取 `path.Base(relative)`，让浏览器按原文件名保存。
- 流式 `io.Copy` 写响应，不在 manager 进程缓冲整个文件。

## 前端设计

在 `web/src/api/hooks/useKnowledge.ts` 增加下载工具，模式参考
`web/src/api/hooks/useWorkspace.ts`：

- 复用 `getStoredAccessToken()` 给 `fetch` 添加 `Authorization`。
- 内部新增 `downloadKnowledgeBlob(url, fileName)`，把响应转为 Blob 并触发浏览器下载。
- 导出 `downloadOrgKnowledgeFile(orgId, targetPath, fileName)`。
- 导出 `downloadAppKnowledgeFile(appId, orgId, ownerUserId, targetPath, fileName)`。

页面入口：

- `OrgKnowledgePage.vue`
  - 普通文件行对所有可读用户显示「下载」按钮。
  - 目录行仍只提供进入目录能力。
  - 「删除」按钮仍只在 `canManage` 为 true 时显示。
  - 平台管理员选择组织后，可下载所选组织知识库文件。
- `AppKnowledgeTab.vue`
  - 普通文件行显示「下载」按钮。
  - 「删除」按钮仍只在 `canManage` 为 true 时显示。
  - 目录不提供下载。本次不扩大实例知识库页面的目录浏览能力。

下载中使用页面级 `downloading` 状态禁用下载按钮，避免重复触发。下载失败时使用现有
`n-message` 或页面 `errorMessage` 展示失败信息。

## 数据流

1. 用户在组织知识库或实例知识库页面看到文件列表。
2. 用户点击某个普通文件行的「下载」按钮。
3. 前端通过带 Authorization 的 `fetch` 请求对应下载接口。
4. handler 校验必填参数并从登录上下文取 `principal`。
5. service 使用现有读取权限谓词校验主体是否可读该知识库。
6. service 通过 `KnowledgeMaster.Open` 从 manager 主副本打开文件流。
7. handler 把文件流写入响应，前端转为 Blob 触发浏览器下载。

## 错误处理

- 缺少 `path`：`400 BAD_REQUEST`。
- 实例知识库缺少 `org_id` 或 `owner_user_id`：`400 BAD_REQUEST`。
- 没有读取权限：`403 KNOWLEDGE_FORBIDDEN`。
- 知识库主副本未配置：`503 KNOWLEDGE_NOT_CONFIGURED`。
- 文件不存在、目标是目录、非法路径或打开失败：沿用现有知识库错误映射为
  `400 BAD_REQUEST`，响应信息走安全错误文本。

## OpenAPI

新增两个下载 handler 的 swag 注解：

- `@Produce application/octet-stream`
- `@Success 200 {string} binary "二进制文件流"`
- 组织级路由：`/organizations/{orgId}/knowledge/file [get]`
- 实例级路由：`/apps/{appId}/knowledge/file [get]`

修改 handler 路由后必须运行：

- `make openapi-gen`
- `make web-types-gen`

生成的 `openapi/openapi.yaml` 和 `web/src/api/generated.ts` 随代码一起提交，不手工编辑。

## 测试设计

后端 service：

- org_member 可下载本组织组织知识库文件。
- platform_admin 可下载组织级知识库文件。
- platform_admin 可下载实例级知识库文件。
- org_member 不能下载其他 owner 的实例知识库文件。
- 缺失、非法或越界 path 返回错误。

后端 handler：

- 组织知识库下载成功返回 `200` 和文件内容。
- 组织知识库下载缺少 `path` 返回 `400`。
- 实例知识库下载成功返回 `200` 和文件内容。
- 实例知识库缺少 `org_id`、`owner_user_id` 或 `path` 返回 `400`。

前端：

- `useKnowledge` 下载工具请求正确 URL，并触发浏览器下载。
- `OrgKnowledgePage` 对组织成员显示「下载」，不显示「删除」。
- `OrgKnowledgePage` 对组织管理员同时显示「下载」和「删除」。
- `AppKnowledgeTab` 对可读用户显示「下载」。
- `AppKnowledgeTab` 的「删除」仍只由 `canManage` 控制。

浏览器验证：

- 用组织成员登录，进入 `/knowledge` 下载组织知识库单文件。
- 进入该成员自己的实例知识库，下载实例知识库单文件。
- 用组织管理员登录，确认组织知识库和本组织实例知识库文件可下载。
- 用平台管理员登录，选择组织后确认组织知识库文件可下载。

## 影响范围

预计修改文件：

- `internal/api/handlers/knowledge.go`
- `internal/api/handlers/knowledge_test.go`
- `internal/service/knowledge_service.go`
- `internal/service/knowledge_service_test.go`
- `web/src/api/hooks/useKnowledge.ts`
- `web/src/api/hooks/useKnowledge.spec.ts`
- `web/src/pages/knowledge/OrgKnowledgePage.vue`
- `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
- `web/src/pages/apps/AppKnowledgeTab.vue`
- `web/src/pages/apps/AppKnowledgeTab.spec.ts`
- `openapi/openapi.yaml`
- `web/src/api/generated.ts`

可能补充文档：

- `docs/user-manual.md`
- `docs/product-design.md`

## 风险与约束

- 下载使用 manager 主副本，因此不代表 runtime node 已经同步成功；这与当前知识库页面
  “管理主副本”的语义一致。
- `Content-Disposition` 文件名需要处理特殊字符，至少使用 `path.Base` 避免把目录路径暴露为保存名。
- 下载接口是受保护接口，前端必须使用带 Authorization 的 `fetch`，不能直接使用裸链接。
- 本次只做单文件下载，目录下载能力需要后续单独设计归档范围、文件名和大目录流式行为。
