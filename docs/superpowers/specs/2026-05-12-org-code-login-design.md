# 组织标识登录命名空间设计

## 背景

当前 `users.username` 是全局唯一，登录也只按用户名查询。这个模型会导致不同组织无法创建相同短用户名，例如多个组织都想使用 `admin` 或 `zhangsan`。用户确认采用“组织标识 + 用户名 + 密码”的登录方式，并保留平台管理员不填写组织标识的登录习惯。

## 目标

- 新增组织登录标识，让不同组织可以拥有重复的短用户名。
- 保留现有 `organizations.name` 作为展示名称，允许继续修改。
- 平台管理员继续使用原用户名密码登录，组织标识留空。
- 组织管理员创建成员时只填写组织内短用户名，不在前端或后端拼接前缀。
- 数据库约束必须直接表达唯一性边界，避免只依赖前端约定。

## 非目标

- 不实现可修改的登录组织标识。
- 不保留“前端拼 `<组织标识>-` 到用户名”的兼容模型。
- 不引入多租户自助注册或找回密码能力。
- 不改变现有角色权限模型。

## 推荐方案

新增 `organizations.code` 作为不可变登录命名空间。`organizations.name` 继续表示组织展示名，可由现有组织资料更新流程修改。

`users.username` 改为组织内用户名。组织用户通过 `(org_id, username)` 唯一；平台管理员通过 `org_id IS NULL` 下的 `username` 唯一。

登录请求增加可选 `org_code`：

- `org_code` 为空：只查平台管理员账号，组织用户不会被命中。
- `org_code` 非空：先按 `organizations.code` 查询组织，再按 `(org_id, username)` 查询组织用户。
- 平台管理员带 `org_code` 登录应失败，避免角色边界混淆。

## 已比较方案

### 方案一：组织标识 + 组织内用户名

这是推荐方案。模型清晰，展示名和登录命名空间分离，数据库约束能直接防止同组织重复用户名。

代价是需要迁移数据库唯一约束，并同步登录 API、前端登录页、组织创建页和 OpenAPI 生成物。

### 方案二：保留全局用户名并自动加前缀

这个方案改动较小，但数据库仍存储带前缀的全局用户名。成员列表、重置密码确认和审计展示都会暴露拼接后的账号，不利于长期维护。

### 方案三：增加 `local_username`，保留全局 `username`

这个方案适合兼容已有外部系统，但本项目目前没有必须保留全局登录名的集成需求。长期维护两套用户名字段容易产生不一致。

## 数据模型

新增迁移建议命名为 `000013_organization_code_login`。

`organizations`：

- 增加 `code text NOT NULL`。
- 增加唯一约束或唯一索引，保证 `code` 全局唯一。
- 增加格式检查约束：小写字母、数字、短横线，长度 3-32，不能以短横线开头或结尾。
- 为历史组织自动生成 code。默认从组织名称转 slug；无法转出有效 code 或冲突时追加短 UUID 后缀。

`users`：

- 删除现有 `username text NOT NULL UNIQUE` 产生的全局唯一约束。
- 新增 `UNIQUE (org_id, username) WHERE org_id IS NOT NULL`。
- 新增 `UNIQUE (username) WHERE org_id IS NULL`。
- 保留 `users_platform_org_check`，继续要求平台管理员无组织、组织角色必须归属组织。

迁移 down：

- 恢复 `users.username` 全局唯一约束前必须确认数据仍满足全局唯一。若不同组织已有重复用户名，down 会失败，这是符合预期的保护行为。
- 删除 `organizations.code` 前先删除相关唯一索引和检查约束。

## 后端设计

### sqlc 查询

新增查询：

- `GetOrganizationByCode(ctx, code)`
- `GetUserByOrgAndUsername(ctx, org_id, username)`

保留 `GetUserByUsername` 供平台管理员登录、种子命令和必要的兼容路径使用。服务层调用时必须明确区分平台登录和组织登录。

### AuthService

`LoginInput` 增加 `OrgCode string`。

登录流程：

1. 修剪 `OrgCode` 和 `Username` 前后空白。
2. `OrgCode == ""` 时调用 `GetUserByUsername`，并要求返回用户角色为 `platform_admin` 且 `org_id` 为空。
3. `OrgCode != ""` 时调用 `GetOrganizationByCode`，再调用 `GetUserByOrgAndUsername`。
4. 组织登录命中的用户必须是 `org_admin` 或 `org_member`。
5. 密码校验和 `ensureUserEnabled` 复用现有逻辑。
6. 用户名、组织标识或密码错误都返回 `ErrInvalidCredentials`，避免泄露账号是否存在。

### OrganizationService

`OrganizationInput` 增加 `Code string`，仅创建组织时使用。

创建组织时：

- 只有平台管理员可创建组织，沿用现有权限。
- 校验 `Code` 格式和必填。
- 调用 `CreateOrganization` 写入 `code`。
- 创建首个管理员时直接保存短用户名，例如 `admin`。
- 不做 `<code>-<username>` 拼接。

更新组织资料时：

- `OrganizationRequest` 不包含 `code`。
- `UpdateOrganization` 不修改 `code`。

`OrganizationResult` 增加 `code`，供前端展示和后续选择器使用。

### MemberService 与 OnboardingService

普通新增成员和一键开户都继续接收短用户名：

- `CreateMember` 写入 `(org.ID, input.Username)`。
- `OnboardMember` 事务内写入 `(org.ID, input.Username)`。
- 同组织重复用户名由数据库唯一索引兜底，service 将错误包装为创建失败。

## API 设计

`LoginRequest`：

- 新增 `org_code string`，非必填。为空表示平台管理员登录。

`CreateOrganizationRequest`：

- 新增 `code string`，必填。

`OrganizationResult`：

- 新增 `code string`。

`OrganizationRequest`：

- 不新增 `code`。

修改 DTO、响应结构或 swag 注解后，必须运行：

```bash
rtk make openapi-gen
rtk make web-types-gen
```

## 前端设计

### 登录页

登录表单增加“组织标识”输入框，提示“平台管理员可留空”。提交时传：

```ts
{
  org_code,
  username,
  password,
}
```

平台管理员仍可只输入 `admin / admin123` 登录。组织管理员和组织成员必须填写组织标识。

### 组织管理页

创建组织表单增加“组织标识 *”字段。

组织列表增加“组织标识”列，便于平台管理员告知组织用户登录时应填写的 code。

### 成员页与一键开户页

用户名输入框继续表示短用户名。页面可在表单附近展示当前组织标识，但不把它拼入 `username`。

成员列表继续展示短用户名；重置密码确认也使用短用户名。

## 种子与本地调试

`cmd/seed-admin` 继续创建 `platform_admin`，不需要组织标识。

`cmd/seed-e2e` 需要为测试组织写入稳定 code，例如：

- `test-org`

本地调试账号说明应更新为：

- 平台管理员：组织标识留空，`admin / admin123`
- 测试组织管理员：组织标识 `test-org`，用户名 `test-org`，密码 `test-org123`
- 组织成员：组织标识 `test-org`，用户名 `test-org-user1`，密码 `test-org-user1`

## 错误处理

- 登录时组织标识不存在、用户不存在或密码错误统一返回“用户名或密码错误”。
- 组织标识格式非法在创建组织时返回 400 级业务错误。
- 组织标识重复依赖数据库唯一约束兜底，service 返回创建组织失败。
- 组织被禁用时继续返回 `ErrOrgDisabled` 对应的 403。
- 用户被禁用时继续返回 `ErrUserDisabled` 对应的 403。

## 测试计划

后端 service：

- 平台管理员空 `org_code` 登录成功。
- 平台管理员带 `org_code` 登录失败。
- 组织用户带正确 `org_code` 登录成功。
- 组织用户空 `org_code` 登录失败。
- 错误 `org_code` 登录失败。
- 不同组织允许相同 `username`。
- 同一组织重复 `username` 创建失败。
- 创建组织要求合法且唯一的 `code`。

后端 handler：

- 登录 DTO 透传 `org_code`。
- 创建组织 DTO 透传 `code`。
- 组织响应包含 `code`。

前端：

- 登录 store/API 请求包含 `org_code`。
- 登录页组织标识为空时仍可提交平台管理员登录。
- 组织创建 payload 包含 `code`。
- 组织列表展示 `code`。

生成与集成：

- `rtk make sqlc-generate`
- `rtk go test ./internal/service ./internal/api/handlers ./cmd/seed-e2e`
- `rtk make openapi-gen`
- `rtk make web-types-gen`
- `rtk npm --prefix web run test:unit -- --run`
- `rtk npm --prefix web run type-check`

## 风险与约束

- 这是前后端同步发布变更。旧前端不传 `org_code` 时，组织用户无法登录。
- 迁移 down 在已有跨组织重复用户名后无法恢复全局唯一用户名，这是预期的数据保护。
- 自动生成历史组织 code 可能不够友好；如果生产环境需要指定更可读的 code，应在升级前准备一次性映射 SQL。
- `organizations.code` 创建后不可修改，后续如要支持改 code，需要单独设计登录影响、审计和通知流程。

## 验收标准

- 平台管理员可在组织标识留空时登录。
- 组织用户必须填写正确组织标识才能登录。
- 两个不同组织可以创建相同短用户名。
- 同一组织不能创建重复短用户名。
- 组织展示名修改不影响登录组织标识。
- OpenAPI 和前端生成类型与后端 DTO 保持同步。
