# 按域名白标登录页 — 设计

- 日期：2026-07-02
- 状态：待评审
- 范围：`web/`（纯前端），后端无改动

## 背景与目标

当前登录页由 manager 内的 Vue SPA 提供，品牌元素（`AGENT RUNTIME MANAGER` 标题、hero 文案、指标卡、配色、粒子背景动效）全部硬编码在 `web/src/layouts/AuthLayout.vue` 与 `web/src/pages/login/LoginPage.vue`（走 i18n catalog），与访问域名无关；本地 `ocm.localhost`、线上 `ai.ywjs.com` 共用同一套页面。

**目标**：为白标客户定制——按浏览器访问的 `hostname` 给登录页渲染不同的整页外观，让客户感觉是「自己的平台」。可变范围是**整页任意布局**（布局、背景、logo、文案、配色、可选页面标题/favicon 全部自由），不局限于换文字或换肤令牌。

**非目标 / 明确约束**：

- 不修改认证逻辑、不改「组织标识」手填流程、不动任何后端 API。**这是纯前端特性**。
- **域名不绑组织**：hostname 只决定登录页外观，登录归属哪个组织仍由用户手填「组织标识」，后端全程不感知 hostname。
- 自定义页面**只由平台方（内部）编写**，不开放客户自助上传 → 无需沙箱/审核，无 XSS/凭证窃取信任问题。
- 新增/修改白标域名**可接受改代码 + 重新发版**，无需运行时后台配置。
- hostname 命中采用**精确域名映射**（不做通配符/后缀匹配）。
- 首期**只搭机制 + 默认变体**，不落任何示例白标变体；真实客户后续按机制增量添加。

## 方案选型

- **方案 A（采用）**：变体组件 + 共享登录 composable + hostname 精确注册表，纯前端。布局完全自由、登录行为零重复、类型安全、后端零改动，新客户 = 加一个组件 + 注册一行。
- 方案 B（否决）：单模板 + 数据化主题令牌（配色/文字/logo 配置化）。无法满足「任意布局」诉求。
- 方案 C（否决）：Vite 多入口每域名独立打包。在「共享 SPA + 改代码发版」前提下更重、拆包、ops 复杂，无必要。

## 组件结构

全部落在 `web/src/pages/login/` 下：

### 1. `useLogin.ts`（composable）

抽取当前 `LoginPage.vue` 的**全部登录行为**，作为唯一实现供所有变体复用：

- 响应式状态：`orgCode` / `username` / `password` / `showPassword` / `errorMessage`。
- 验证码：挂载时探测 `/api/v1/auth/altcha-challenge`（204 → `captchaActive=false` 关闭），`captchaActive` / `captchaVerified` / `captchaPayload`、`onCaptchaState(e)`、以及失败后 `reset()+verify()` 的重置逻辑；暴露 `captchaRef` 供变体绑定 widget。
- `onSubmit()`：调用 `useAuthStore().login(username, password, orgCode, payload?)`，成功后按 `route.query.redirect` 回跳，失败写 `errorMessage` 并重置验证码。
- `auth.loading` 透传给变体做按钮禁用。

返回上述 state + 方法。**登录行为只此一份**，避免各变体复制导致认证逻辑漂移。

### 2. `variants/registry.ts`

```ts
type LoginVariant = {
  component: Component        // 整页组件，异步 import 以便按需分包
  documentTitle?: string     // 可选：覆盖 <title>
  faviconHref?: string       // 可选：覆盖 favicon
}

const VARIANTS: Record<string, LoginVariant>  // key = 精确 hostname
const DEFAULT_VARIANT: LoginVariant            // 默认变体
function resolveVariant(hostname: string): LoginVariant  // 精确命中，否则回 DEFAULT_VARIANT
```

首期 `VARIANTS` 为空（或仅含默认域名显式指向默认变体），`DEFAULT_VARIANT` 指向默认变体。`ai.ywjs.com` / `ocm.localhost` / 任何未命中 hostname 全部走默认。

### 3. `LoginHost.vue`

`/login` 路由的入口组件，取代现在直接挂的 `AuthLayout`：

- 挂载时 `resolveVariant(window.location.hostname)` 选定变体，`<component :is="variant.component" />` 渲染。
- 应用可选副作用：`variant.documentTitle` 覆盖 `document.title`、`variant.faviconHref` 覆盖 favicon。默认变体不设这两项 → 保持现状。

### 4. `variants/default/DefaultLoginPage.vue`

把现有 `AuthLayout.vue`（粒子背景 + 极光/网格/扫描 + hero 介绍区）与 `LoginPage.vue`（登录卡片表单）**合并为一个整页组件**，登录行为改用 `useLogin()`。视觉与交互与现状**保持 100% 一致**，作为回归基线。

## 变体作者契约

新增变体时布局可任意发挥，但模板**必须**接线以下几点（写进 `useLogin.ts` 文件注释）：

1. 表单提交绑定 `useLogin` 的 `onSubmit`，输入框双向绑定 `orgCode`/`username`/`password`。
2. `captchaActive` 为真时渲染 altcha 挂载点并绑定 `captchaRef` 与 `onCaptchaState`。
3. submit 按钮禁用条件绑定 `auth.loading || (captchaActive && !captchaVerified)`。
4. 展示 `errorMessage`。

省略以上任一接线点会导致验证码或登录失效。

## 数据流

`abc.example.com/login` → 路由命中 `LoginHost` → `resolveVariant('abc.example.com')` → 渲染对应变体（自定义布局）→ 表单绑定 `useLogin` → 提交仍到 `/api/v1/auth/login`（组织标识由用户手填）→ 成功按 redirect 回跳。后端全程不感知 hostname。

## 影响面与清理

- 路由 `web/src/app/router.ts`：`/login` 由 `AuthLayout` + 子 `LoginPage` 改为直接挂 `LoginHost`。
- `AuthLayout.vue` 仅服务 `/login`、唯一子页是 `LoginPage`，无其它引用 → 内容并入 `DefaultLoginPage.vue` 后移除 `AuthLayout.vue` 与 `LoginPage.vue`。
- 对应测试 `AuthLayout.spec.ts` / `LoginPage.spec.ts` 的断言迁移到新结构。

## 测试

- `useLogin` 单测：登录成功回跳 redirect；登录失败展示错误信息并重置验证码；验证码探测返回 204 时关闭验证码、放行按钮（迁移现有 `LoginPage.spec.ts` 的行为断言）。
- `registry` 单测：精确 hostname 命中返回对应变体；未命中回 `DEFAULT_VARIANT`；可注册一个测试用 fixture 变体验证命中路径。
- `DefaultLoginPage` 渲染测试：关键字段与结构存在（迁移 `LoginPage.spec` + `AuthLayout.spec` 的结构断言）。

## 交付验证

按 CLAUDE.md 要求用真实浏览器验证（非 curl）：本地借 k3d `*.localhost` ingress 模拟两个 hostname，注册一个临时白标变体，验证——

1. 默认域名（`ocm.localhost`）渲染默认变体、能登录成功；
2. 临时白标域名渲染其变体、且同样能登录成功。

验证通过后移除临时变体（首期不落示例变体）。
