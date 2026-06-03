# 用户自助修改密码 — 设计文档

- 日期：2026-06-03
- 状态：已确认，待实现
- 作者：hujing + Codex

## 背景与目标

当前系统只支持企业管理员在成员管理页强制重置成员密码。普通已登录用户无法自行修改
自己的登录密码，文档也说明“不提供自助找回；请联系管理员重置”。

本次目标是新增“已登录用户修改自己的登录密码”能力：用户输入当前密码、新密码和确认
新密码；后端校验当前密码正确后更新 `users.password_hash`；修改成功后前端清理会话并
跳回登录页，要求用户用新密码重新登录。

## 已确认决策

1. **范围是自助改密，不是找回密码。** 用户必须已登录且知道旧密码；忘记密码仍走现有
   管理员“重置密码”流程。
2. **管理员重置流程保持不变。** `POST /api/v1/members/{userId}/password` 继续只表达
   管理员强制重置成员密码，不要求旧密码。
3. **自助改密放在 auth 边界。** 新接口挂在 `/api/v1/auth/password`，由 `AuthService`
   处理当前用户身份、旧密码校验和新密码写入。
4. **成功后退出登录。** 前端收到 204 后清理本地 access/refresh token 并跳转 `/login`，
   用户需用新密码重新登录。
5. **不新增账号设置整页。** 前端入口放在已存在的 `DashboardLayout.vue` 侧边栏用户区，
   用弹窗完成表单提交。

## 方案比较

### 推荐方案：`POST /api/v1/auth/password` + 侧边栏弹窗

- 后端职责清晰：当前用户改密属于认证上下文，避免和管理员成员管理混在一起。
- 前端改动小：复用现有登录态、全局 API client 和 Naive UI 表单 / 弹窗能力。
- 成功后重新登录，用户能明确感知密码已变更，旧页面状态也不会继续依赖旧会话。

### 备选方案：复用成员路由

例如新增 `POST /api/v1/members/{userId}/password/change`。优点是复用成员服务概念，
但需要额外防止用户提交他人 `userId`，且“管理员重置”和“本人改密”语义容易混在同一
路由族里。

### 备选方案：新增 `/account` 页面

适合以后集中做个人资料、安全设置、会话管理等能力。本次只有一个改密动作，新增整页、
导航和路由会扩大范围。

## 后端设计

### Service 边界

在 `internal/service/auth_service.go` 增加自助改密能力，复用现有 `AuthStore` 获取当前
用户，并扩展 store 接口以支持 `UpdateUserPassword`。

业务流程：

1. 通过 `principal.UserID` 调用 `GetUser` 读取当前用户。
2. 复用 `ensureUserEnabled` 检查用户和所属企业仍为 active。
3. 校验 `old_password` 与当前 `PasswordHash` 是否匹配。
4. 校验 `new_password` 非空且至少 8 位。
5. 校验 `new_password` 与 `old_password` 明文不完全相同。
6. 生成新 Argon2id hash，调用 `UpdateUserPassword` 写入 `users.password_hash`。

旧密码错误返回 `ErrInvalidCredentials`，避免泄露更多账号状态细节。新密码非法返回
`ErrMemberCreateInvalid`，复用成员创建 / 重置密码已有的 400 映射，避免扩大统一错误体系。
为了让单元测试保持快速可控，`AuthService` 需要像 `MemberService` 一样可注入密码 hash
函数；生产默认使用 `auth.HashPassword(..., auth.DefaultPasswordParams)`。

### Handler 与路由

在 `internal/api/handlers/auth.go` 增加已认证路由：

```text
POST /api/v1/auth/password
```

请求体放入 `internal/api/handlers/dto.go`：

```json
{
  "old_password": "current-password",
  "new_password": "new-password"
}
```

该接口注册到 `RegisterAuthMeRoutes` 所在的 authenticated group，继续受 Bearer token
与 CSRF double-submit 校验保护。成功返回 `204 No Content`。

错误映射：

- 请求体缺字段：400，沿用 `writeBindError`。
- 新密码为空、不足 8 位或与旧密码相同：400。
- 当前密码错误：401，`INVALID_CREDENTIALS`。
- 用户或企业已禁用：403。
- 其它未知错误：500，响应脱敏。

## 前端设计

### 入口与交互

在 `web/src/layouts/DashboardLayout.vue` 的侧边栏用户区增加“修改密码”按钮，位置靠近
“退出”。点击后打开弹窗表单，字段为：

- 当前密码。
- 新密码。
- 确认新密码。

前端基础校验：

- 三项必填。
- 新密码至少 8 位。
- 新旧密码不能完全相同。
- 确认新密码必须与新密码一致。

校验通过后提交 `POST /api/v1/auth/password`。成功后清理本地 token 并跳转 `/login`；
弹窗内不保留密码内容。失败时在弹窗内展示后端安全文案。

### API 封装

在 `web/src/stores/auth.ts` 增加 `changePassword(oldPassword, newPassword)`，因为成功后的
会话清理和跳登录与 auth store 强相关。实现阶段不新增单独 `useChangePassword` hook，
避免同时引入两套调用路径。

## OpenAPI 同步

新增 handler 请求体和路由后必须同步生成：

```text
make openapi-gen
make web-types-gen
```

生成产物 `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 需要随实现一起提交，不手工
编辑。

## 测试计划

### 后端 service 测试

- 旧密码正确、新密码合法时，写入的新 hash 不等于明文新密码。
- 旧密码错误时拒绝，并且不调用密码更新。
- 新密码为空、不足 8 位或与旧密码相同时拒绝。
- 用户被禁用或所属企业被禁用时拒绝。

### 后端 handler 测试

- 成功请求返回 204，并把 principal 与请求体透传给 service。
- 缺少 `old_password` 或 `new_password` 返回 400。
- 当前密码错误映射为 401。

### 前端单元测试

- auth store 或 API 调用提交 `/api/v1/auth/password`，body 字段正确。
- 弹窗校验确认密码不一致、密码不足 8 位、新旧密码相同等异常路径。
- 成功后清 token 并跳转登录页。

### 浏览器验证

交付前使用真实浏览器验证：

1. 使用本地账号登录 manager 后台。
2. 打开“修改密码”弹窗，输入旧密码、新密码并提交。
3. 确认页面跳回登录页。
4. 使用新密码重新登录成功。
5. 验证旧密码登录失败。

## 影响范围

- `internal/service/auth_service.go` 及其测试。
- `internal/api/handlers/auth.go`、`dto.go`、handler 测试与路由注册。
- `web/src/layouts/DashboardLayout.vue`、`web/src/stores/auth.ts` 或单独 hook、对应前端测试。
- OpenAPI 生成产物。

## 不做

- 不做忘记密码 / 邮件找回 / 短信验证。
- 不做会话列表、踢下线或服务端批量撤销 refresh token。
- 不强制修改 new-api 侧密码；manager 登录密码只维护在本系统 `users.password_hash`。
- 不新增独立账号设置页面。
