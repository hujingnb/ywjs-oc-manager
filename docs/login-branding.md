# 按域名白标登录页

本文档说明「按访问域名（hostname）渲染不同白标登录页」的前端机制，以及**如何新增一个白标变体**。

## 目标与约束

- 目标：按浏览器访问的 `window.location.hostname`，给 `/login` 渲染不同的白标整页登录页（布局、背景、Logo、文案、配色、页面标题/favicon 全部可自定义）。
- **纯前端特性**：不涉及后端 API、不改认证逻辑。
- **域名不绑组织**：hostname 只决定登录页外观；登录归属哪个组织仍由用户在「组织标识」输入框手填，后端全程不感知 hostname。
- **仅平台方编写**：白标变体由本仓库内部维护，不开放客户自助上传，因此无沙箱/审核，也无同源 XSS 风险。
- **改代码 + 发版上线**：新增白标 = 加一个变体组件 + 在注册表登记一行 hostname + 构建发布。

## 组件结构

全部位于 `web/src/pages/login/`：

| 文件 | 职责 |
|---|---|
| `useLogin.ts` | 共享登录行为 composable：登录表单状态、验证码（Altcha）探测与交互、`onSubmit` + 登录后回跳。**所有变体共用同一份认证行为**，避免各变体复制导致逻辑漂移。 |
| `LoginHost.vue` | `/login` 路由入口：`resolveVariant(window.location.hostname)` 选定变体并 `<component :is>` 渲染，挂载时应用变体的 title/favicon。 |
| `variants/registry.ts` | `LoginVariant` 类型、`DEFAULT_VARIANT`、`VARIANTS`（精确 hostname 映射）、`resolveVariant`、`applyVariantChrome`。 |
| `variants/default/DefaultLoginPage.vue` | 默认变体整页（粒子背景 + hero 介绍区），内嵌 `LoginForm`。`ai.ywjs.com` / `ocm.localhost` / 任何未命中的 hostname 都用它。 |
| `variants/default/LoginForm.vue` | 默认变体的登录卡片，接线 `useLogin()`。 |

## 数据流

```
abc.example.com/login
  → 路由 /login → LoginHost
  → resolveVariant('abc.example.com')  // 精确命中 VARIANTS，未命中回 DEFAULT_VARIANT
  → 渲染选定变体（自定义整页布局）
  → 表单绑定 useLogin()
  → 提交 POST /api/v1/auth/login（组织标识仍由用户手填）
  → 成功后按 route.query.redirect 回跳（缺省 '/'）
```

后端不感知 hostname，认证与组织判定逻辑完全不变。

## 新增一个白标变体

### 1. 写变体整页组件

在 `web/src/pages/login/variants/<customer>/` 下新建整页组件，布局随意发挥（自带背景、Logo、配色）。**登录部分必须接线 `useLogin()`**，最省事的方式是直接复用默认的 `LoginForm.vue`：

```vue
<!-- variants/acme/AcmeLoginPage.vue -->
<template>
  <main class="acme-stage">
    <img class="acme-logo" src="./assets/acme-logo.svg" alt="ACME" />
    <div class="acme-shell">
      <LoginForm />
    </div>
  </main>
</template>

<script setup lang="ts">
import LoginForm from '../default/LoginForm.vue'
</script>

<style scoped>
/* 自定义品牌外观 */
</style>
```

若要完全自绘登录表单（不复用 `LoginForm`），则必须遵守下面的**变体作者契约**。

### 2. 在注册表登记 hostname

编辑 `variants/registry.ts`，在 `VARIANTS` 中加一行（精确 hostname 匹配）：

```ts
import AcmeLoginPage from './acme/AcmeLoginPage.vue'

export const VARIANTS: Record<string, LoginVariant> = {
  'login.acme.com': {
    component: AcmeLoginPage,
    documentTitle: 'ACME 控制台登录', // 可选：覆盖浏览器标签标题
    faviconHref: '/acme-favicon.ico', // 可选：覆盖 favicon
  },
}
```

### 3. 构建发布

新增白标需要重新构建前端并发版（`make web-build` → 部署 manager-web）。

## 变体作者契约

若变体自绘登录表单（不复用 `LoginForm`），模板**必须**接线 `useLogin()` 的以下部分，否则验证码或登录会失效：

1. 表单 `submit` 绑定 `onSubmit`；输入框 `v-model` 绑定 `orgCode` / `username` / `password`。
2. `captchaActive` 为真时渲染 Altcha 挂载点，`ref` 绑 `captchaRef`、`@statechange` 绑 `onCaptchaState`。
3. submit 按钮 `disabled` 绑定 `auth.loading || (captchaActive && !captchaVerified)`。
4. 展示 `errorMessage`。

契约同样写在 `useLogin.ts` 顶部注释中，以最新代码为准。

## 页面标题 / favicon

`LoginVariant` 的 `documentTitle` / `faviconHref` 为可选项，由 `LoginHost` 在挂载时经 `applyVariantChrome` 应用；默认变体不设，保持现状。注意 `applyVariantChrome` 只在登录页设置、不在离开时还原——目前无副作用（默认变体两项都不设），但为白标设了标题后，登录成功进入后台仍会沿用该标题，需要时自行评估。

## 测试

- `useLogin` 行为通过其真实宿主 `LoginForm.spec.ts` 覆盖（验证码门控 / 带 payload 提交 / 失败重置 / 204 关闭 / 成功回跳）。
- `registry.spec.ts` 覆盖 `resolveVariant` 精确命中与默认兜底、`applyVariantChrome` 标题/favicon 应用。
- `LoginHost.spec.ts` 用 mock 注册表验证「选定变体渲染 + chrome 应用」。
- 新增变体建议补一个轻量渲染 + 表单接线冒烟测试。

## 本地验证不同 hostname

本地 k3d 的 ingress 只路由 `ocm.localhost` 等固定 host（见 `deploy/k8s/local/ingress.yaml`）。要用真实浏览器验证某个白标 hostname：

1. 在 `deploy/k8s/local/ingress.yaml` 临时加一条 `whitelabel.localhost` host 规则（镜像 `ocm.localhost` 的 `/api`→manager-api、`/`→manager-web 两条 path），`kubectl apply`。`*.localhost` 自动解析到 `127.0.0.1`，无需改 hosts。
2. 在 `VARIANTS` 临时登记该 hostname 指向要验证的变体，`make local-build` 部署。
3. 浏览器访问 `http://whitelabel.localhost/login` 验证命中该变体并可登录。
4. 验证后还原 ingress 与注册表（临时改动不提交）。

> 验证码默认在本地关闭（`captcha.enabled: false`，出题接口返回 204）。要验证 Altcha 开启态，临时把 `deploy/k8s/local/secret.yaml` 的 `captcha.enabled` 改 `true` 并重启 manager-api，验证后还原。
