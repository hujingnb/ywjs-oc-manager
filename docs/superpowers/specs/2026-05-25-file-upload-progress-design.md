# 设计文档：文件上传进度条与离开警告

**日期：** 2026-05-25
**状态：** 待批准

## 背景

项目现有三处文件上传：

| 触发位置 | mutation hook | 传输形态 |
| --- | --- | --- |
| `web/src/pages/knowledge/OrgKnowledgePage.vue` | `useUploadOrgKnowledge` | `application/octet-stream` 原始字节流 |
| `web/src/pages/apps/AppKnowledgeTab.vue` | `useUploadAppKnowledge` | `application/octet-stream` 原始字节流 |
| `web/src/pages/platform/AssistantVersionsPage.vue` | `useUploadAssistantVersionSkill` | `multipart/form-data`，字段名 `file` |

三处实现都用浏览器原生 `fetch()`，调用方在按钮上挂 `loading=true`，但无任何字节级进度反馈。

### 用户痛点

上传大文件（知识库附件、skill tar 包动辄 100MB+）时：

1. 页面只显示按钮 loading，看不到任何进度推进；
2. 用户怀疑卡死 / 网络断了，主动按 F5 或关闭 tab；
3. 此时 XHR 请求被浏览器随页面销毁强制中断，上传全功夫白费。

### 技术约束

- 浏览器 `fetch()` API **不能**报告 upload 进度。要拿进度只能改用 `XMLHttpRequest.upload.onprogress`。
- 浏览器没有 API 可以「阻止用户刷新」。只能用 `beforeunload` 事件触发原生「确定离开此页？」确认框（且现代浏览器不允许自定义文案）。
- 真正的「刷新后可恢复」需要后端实现分片接收 + 续传 + 临时存储，本次范围明确不做。

## 设计目标

- 三处上传都增加可视化进度反馈（百分比 + 字节数）。
- 上传过程中用户尝试刷新 / 关闭 tab 时，弹出浏览器原生确认框。
- 上传过程中拉起模态框，UI 阻止误点其他上传按钮，但允许用户主动取消当前上传。
- 批量上传（assistant skill 一次创建多个）共享同一模态框，显示「当前文件进度 + 总体 N/M」。
- 后端零改动，所有 API 契约保持。
- 现有 mutation hook 调用方代码改动最小化（接口扩两个 optional 参数，向后兼容）。

## 非目标

- 不做分片 / 断点续传 / 刷新后恢复（明确超出本次范围）。
- 不做后台上传（用户离开页面后继续上传）。
- 不做并发上传（一次只允许一个上传会话，简化 UI 与冲突处理）。
- 不做上传速率显示、剩余时间估算（v1 可后续追加）。
- 不改动后端任何 handler / service / DB schema。

## 整体架构

新增四个前端文件，改动四个业务文件：

| 新增 / 改动 | 路径 | 职责 |
| --- | --- | --- |
| 新增 | `web/src/api/xhrUpload.ts` | 底层工具，基于 XMLHttpRequest 发送上传请求，暴露 `onProgress` 回调和 `AbortSignal`；与 `apiRequest` 对齐 401 / CSRF / Bearer 头处理 |
| 新增 | `web/src/stores/uploadProgress.ts` | Pinia setup-style store，沿用 `web/src/stores/auth.ts` 的写法，维护会话状态、文件队列、当前进度、取消句柄 |
| 新增 | `web/src/components/UploadProgressModal.vue` | 全局 Modal，订阅 store，渲染 `n-modal` + `n-progress` + 取消 / 关闭按钮 + 汇总信息 |
| 新增 | `web/src/composables/useBeforeUnloadGuard.ts` | 监听 `beforeunload`，在 `store.isUploading=true` 时 `preventDefault()` 触发浏览器原生确认 |
| 改动 | `web/src/api/hooks/useKnowledge.ts` | `useUploadOrgKnowledge` / `useUploadAppKnowledge` 的 `mutationFn` 从 `fetch` 切到 `xhrUpload`；新增 optional `onProgress` / `signal` 入参；`onSuccess` → `onSettled`，让取消 / 失败也刷新列表 |
| 改动 | `web/src/api/hooks/useAssistantVersions.ts` | `useUploadAssistantVersionSkill` 同上（multipart 形态 → `xhrUpload` 工具同时支持 raw body 与 FormData） |
| 改动 | `web/src/App.vue`（或顶层 layout） | 挂载 `<UploadProgressModal />`，调用 `useBeforeUnloadGuard()` |
| 改动 | `web/src/pages/knowledge/OrgKnowledgePage.vue` | `onUpload` 从直接 `uploadMutation.mutateAsync(...)` 改成 `uploadProgress.run([...], runner)` |
| 改动 | `web/src/pages/apps/AppKnowledgeTab.vue` | 同上 |
| 改动 | `web/src/pages/platform/AssistantVersionsPage.vue` | 单文件 `triggerSkillUpload` 与批量 `uploadPendingSkills` 都走 `uploadProgress.run(items, runner)`，store 内串行执行并维护 N/M |

### Store 实现选型

项目已引入 Pinia（`web/package.json` 含 `pinia: ^3.0.4`），`web/src/stores/auth.ts` 已是 setup-style 写法：`defineStore('auth', () => { ... return {...} })`。新增 store 直接沿用同一风格：

```ts
export const useUploadProgressStore = defineStore('uploadProgress', () => {
  const session = ref<UploadSession | null>(null)
  const isUploading = computed(() => /* ... */)
  async function run(...) { /* ... */ }
  function cancel() { /* ... */ }
  function reset() { /* ... */ }
  return { session, isUploading, run, cancel, reset }
})
```

## 状态契约

```ts
// 单个上传任务的形态
interface UploadItem {
  id: string                    // 自动生成（如 nanoid 或 crypto.randomUUID），便于去重和 log
  label: string                 // 显示名，通常是 file.name
  size: number                  // 字节数，用于计算 %
  status: 'pending' | 'uploading' | 'succeeded' | 'failed' | 'cancelled'
  error?: string                // 仅 failed 用，对应 extractErrorMessage 结果
}

// 一次会话的形态，会话内串行执行 items
interface UploadSession {
  items: UploadItem[]
  currentIndex: number          // 0-based，指向正在传的 item
  currentLoaded: number         // 当前 item 已传字节
  startedAt: number             // 时间戳（v1 仅打 log 用，不显示速率）
}

interface UploadProgressState {
  session: UploadSession | null // null 表示空闲
  isUploading: boolean          // = session !== null && 还有未结束 item
}
```

### Store 公开方法

| 方法 | 说明 |
| --- | --- |
| `run<T>(items: RunItem[], runner: RunnerFn<T>): Promise<RunResult<T>>` | 开启会话；串行执行；resolve 返回 `{ succeeded, failed, cancelled, results }`；**不抛错**（除互斥规则） |
| `cancel(): void` | 触发当前 AbortController.abort()；把后续 pending item 标 `cancelled`；让 `run()` 走完循环并 resolve |
| `reset(): void` | Modal 关闭时调用，把 `session` 置 null；触发 `isUploading` 回到 false |

### 互斥规则

`run` 被调用时，若 `state.session !== null` 且仍有未结束 item，直接抛 `Error('已有上传任务正在进行')`。业务页 catch 后用 `n-message.warning('已有上传任务正在进行，请等待完成')` 提示。这避免两个上传会话同时争同一个 Modal。

### Runner 契约

```ts
interface RunnerContext {
  onProgress: (loaded: number, total: number) => void  // 由 store 注入
  signal: AbortSignal                                  // 由 store 注入
}
type RunnerFn<T> = (item: UploadItem, file: File, ctx: RunnerContext) => Promise<T>
```

业务页传入的 runner 负责调用对应 mutation hook，把 ctx 透传给 hook：

```ts
const upload = useUploadProgressStore()
await upload.run(
  [{ file, label: file.name }],
  async (item, file, ctx) => {
    await uploadMutation.mutateAsync({
      path: item.label,
      file,
      onProgress: ctx.onProgress,
      signal: ctx.signal,
    })
  },
)
```

## 组件设计

### `xhrUpload` 接口

```ts
export interface XhrUploadOptions {
  method?: 'POST' | 'PUT'                              // 默认 POST
  headers?: Record<string, string>                     // 调用方可覆盖
  body: Blob | FormData                                // raw 字节流或 multipart
  onProgress?: (loaded: number, total: number) => void
  signal?: AbortSignal
  withAuth?: boolean                                   // 默认 true
}

export interface XhrUploadResponse {
  status: number
  body: unknown                                        // JSON 解析失败时退回 string
}

export async function xhrUpload(url: string, opts: XhrUploadOptions): Promise<XhrUploadResponse>
```

内部要点：

- `withAuth !== false` 时注入 `Authorization: Bearer ${getStoredAccessToken()}`；
- 写操作（POST/PUT/PATCH/DELETE）自动注入 `X-CSRF-Token: ${getCsrfToken()}`；
- 监听 `xhr.upload.onprogress`，转发到 `opts.onProgress`；
- `signal.addEventListener('abort', () => xhr.abort())`，并在 abort 时 reject `DOMException('aborted', 'AbortError')`；
- 收到 401 且 `withAuth !== false`：调 `clearStoredTokens()` 并触发 `unauthorizedHandler`（与 `apiRequest` 行为完全一致）；
- 非 2xx：构造 `ApiError`（沿用 `client.ts` 的 `extractErrorMessage`）抛出；
- 2xx：根据响应 `content-type` 解析 JSON 或文本，resolve `{status, body}`。

### `UploadProgressModal.vue` 模板要点

```
n-modal(
  :show='session !== null'
  :mask-closable='false'
  :closable='!isUploading'    // 上传过程不允许 X 关
  preset='card'
  title='文件上传'
)
  .body
    .header
      | 正在上传 {{ currentItem.label }}（{{ currentIndex + 1 }}/{{ items.length }}）
    n-progress(type='line' :percentage='currentPct')
    .meta
      | {{ humanSize(currentLoaded) }} / {{ humanSize(currentSize) }}
    .actions
      n-button(v-if='isUploading' @click='store.cancel()') 取消上传
      n-button(v-else type='primary' @click='close()') 关闭
    .summary(v-if='!isUploading')
      | 成功 {{ counts.succeeded }} · 失败 {{ counts.failed }} · 取消 {{ counts.cancelled }}
      n-collapse(v-if='failedItems.length')
        n-collapse-item(title='失败详情')
          li(v-for='it in failedItems') {{ it.label }}：{{ it.error }}
```

### 行为细则

- 会话进行中：`closable=false`、`mask-closable=false`，按钮仅「取消上传」；
- 全部 item 结束（succeeded / failed / cancelled 都算）：按钮变「关闭」，可点 X；
- 关闭时调 `store.reset()`，`isUploading` 回到 false，beforeunload 解除拦截。

### `useBeforeUnloadGuard` 实现

```ts
export function useBeforeUnloadGuard() {
  const store = useUploadProgressStore()
  function handler(e: BeforeUnloadEvent) {
    if (!store.isUploading) return
    e.preventDefault()
    e.returnValue = ''   // 现代浏览器忽略自定义文案，但要求设非空值
  }
  onMounted(() => window.addEventListener('beforeunload', handler))
  onBeforeUnmount(() => window.removeEventListener('beforeunload', handler))
}
```

该 composable **只在 `App.vue` 根挂一次**，不要在子组件再调，避免重复注册。

### mutation hook 接口扩参（向后兼容）

```ts
// 三个 hook 的 mutationFn 输入新增可选字段：
interface UploadOrgKnowledgeInput {
  path: string
  file: File
  onProgress?: (loaded: number, total: number) => void   // 新增
  signal?: AbortSignal                                    // 新增
}
```

旧调用方不传 `onProgress` / `signal` 也能工作（`xhrUpload` 内部都是 optional）。所有 mutation hook 的 `onSuccess` 改为 `onSettled`，让取消 / 失败也触发列表 `invalidateQueries`。

## 数据流（关键时序）

### 单文件上传（知识库场景）

```
User 选文件 → onUpload(event)
  └─ uploadProgress.run([{file, label: file.name}], runner)
       ├─ store.session = { items:[item0], currentIndex:0, currentLoaded:0 }
       ├─ isUploading 翻 true → Modal 自动显示 + beforeunload 拦截激活
       ├─ for each item:
       │    ├─ item.status = 'uploading'
       │    ├─ ctrl = new AbortController()
       │    ├─ await runner(item, file, { onProgress, signal: ctrl.signal })
       │    │    └─ uploadMutation.mutateAsync({ path, file, onProgress, signal })
       │    │         └─ xhrUpload(url, { body, onProgress, signal, headers })
       │    │              ├─ xhr.upload.onprogress → onProgress(e.loaded, e.total)
       │    │              ├─ xhr.onload (2xx) → resolve
       │    │              ├─ xhr.onload (非2xx) → reject ApiError
       │    │              └─ signal abort → xhr.abort() + reject AbortError
       │    ├─ resolve → item.status = 'succeeded'
       │    ├─ AbortError → item.status = 'cancelled'，后续 pending 全部标 cancelled，break
       │    └─ 其他错误 → item.status = 'failed', error=msg；继续下一个 item
       ├─ 循环结束 → isUploading 翻 false → beforeunload 解除
       ├─ Modal 切到「汇总 + 关闭」模式
       └─ run() resolve { succeeded, failed, cancelled, results }
```

### 批量上传（assistant skill 的 `uploadPendingSkills`）

```
User 创建 version + 暂存 N 个 skill → 点保存
  └─ created = await createVersion()
  └─ result = await uploadProgress.run(
         skills.map(s => ({ file: s.tar, label: s.name })),
         async (item, file, ctx) => {
           await uploadSkillMutation.mutateAsync({
             id: created.id, file, onProgress: ctx.onProgress, signal: ctx.signal,
           })
         },
       )
  ├─ 全成功 → 路由跳详情页（与现有逻辑一致）
  ├─ 部分失败 → 业务页 n-message 提示；用户在 Modal 汇总区看详情
  └─ 用户中途取消 → 同样以「已取消 K 个」收尾，不抛错
```

### 取消时序

```
User 点「取消上传」
  └─ store.cancel()
       ├─ session.currentAbort.abort()
       │    └─ xhrUpload 内 xhr.abort() → reject AbortError
       │         └─ runner 抛 AbortError → 上层 catch 标 'cancelled'
       ├─ 后续 pending item 全部标 'cancelled'
       └─ run() 主循环检测到 cancelled，break，resolve
```

### 进度回调串联

- `xhr.upload.onprogress` 事件浏览器本身节流到 ~16ms，无需额外节流；
- `mutationFn` 收到 `onProgress` 后**不做任何包装**，直接透传给 `xhrUpload`；
- store 把 `currentLoaded` 用 reactive 暴露给模板，`computed` 算 `%`：
  - 零字节文件 guard：`size > 0 ? Math.min(loaded/size, 1) * 100 : 100`
  - loaded 超过 size（编码膨胀）guard：用 `Math.min(loaded/size, 1)`

### 与 query invalidate 的关系

mutation hook 的 `onSettled`（取代原 `onSuccess`）在每个 item 结束时触发对应 `invalidateQueries`。批量场景下每成功 / 失败 / 取消一个就刷一次列表；这比原来更勤快，不影响正确性。

## 异常与边界处理

| 场景 | 触发点 | 行为 |
| --- | --- | --- |
| 网络中断 / 502 / 5xx | `xhr.onerror` / 非 2xx 响应 | item.status=`failed`，`error=extractErrorMessage(body, status)`；批量场景继续下一个；onSettled 触发 invalidate |
| 用户点取消 | `store.cancel()` → AbortError | item.status=`cancelled`；后续 pending 同标；run() resolve，不抛 |
| 401 未授权 | xhrUpload 识别 status=401 | 与 `apiRequest` 完全一致：`clearStoredTokens()` + `unauthorizedHandler(path)`；item 标 failed；run() 不抛，全局 401 处理器接管路由跳转 |
| 403 / 409 等业务错 | 非 2xx | 同「网络中断」分支，error 文案来自后端 `error` / `message` 字段 |
| 文件被 OS 删除后才上传 | `xhr.onerror` | 归到 onerror，item.status=failed，error="文件读取失败" |
| 浏览器 tab 被关闭 | beforeunload 拦截后用户仍选「离开」 | XHR 随页面销毁自动 abort，无 JS 机会处理；后端会看到截断的请求体并自行清理（后端责任，不在本次范围） |
| Modal 渲染前 store.run() 抛互斥错 | 业务页 catch | 用 `n-message.warning('已有上传任务正在进行，请等待完成')` 提示；本次上传请求不发出 |
| 零字节文件 | `xhr.upload.onprogress` 可能不触发 | computed 内 guard `size > 0 ? ... : 100` |
| loaded 超过 size | 浏览器编码膨胀 | `Math.min(loaded/size, 1) * 100` |
| AbortController 复用 | 每个 item 独立 new，不跨 item 复用 | — |
| `useBeforeUnloadGuard` 重复绑定 | 只在 App 根挂一次，子组件禁用 | — |
| Vue 路由切换 | 不卸载 App.vue 根，store 与 Modal 保留 | — |
| HMR 重载组件文件 | 可能丢 store；v1 不处理 | 开发期已知问题 |
| 超长文件名 | Modal 内 `n-ellipsis` 单行截断，hover tooltip | — |
| token 过期续期 | xhrUpload 一次性读 token，不重试 | 与 `apiRequest` 现状一致；过期会以 401 失败 |

### CSRF / token 头一致性

`xhrUpload` 必须沿用：

- `getStoredAccessToken()` → `Authorization: Bearer ${token}`（withAuth 默认 true）；
- `getCsrfToken()` → `X-CSRF-Token: ${csrf}`（POST/PUT/PATCH/DELETE 自动加）。

这与现有三个 `useUpload*` hook 内 fetch 块的逻辑完全等价，只是搬到工具函数里复用。

## 测试策略

### 新增单元测试

| 文件 | 覆盖目标 |
| --- | --- |
| `web/src/api/xhrUpload.spec.ts` | 进度回调串联；AbortSignal 触发 xhr.abort；401 触发 token 清理 + unauthorizedHandler；非 2xx 抛 ApiError；CSRF + Bearer 头注入；零字节 / FormData / Blob body |
| `web/src/stores/uploadProgress.spec.ts` | 单文件 run 成功；单文件失败；批量部分失败仍继续；取消传播给后续 pending；互斥规则（第二次 run 抛错）；reset 把 session 置 null |
| `web/src/components/UploadProgressModal.spec.ts` | 会话中渲染「取消」按钮，结束后渲染「关闭」按钮；当前文件名 + N/M 文案；失败列表展开；mask-closable 与 closable 在不同 phase 切换 |
| `web/src/composables/useBeforeUnloadGuard.spec.ts` | beforeunload 仅在 isUploading=true 时 preventDefault；卸载时 removeEventListener |

### 改动现有测试

| 文件 | 改动 |
| --- | --- |
| `web/src/pages/platform/AssistantVersionsPage.spec.ts` | mock 的 `uploadSkill` 入参新增 `onProgress` / `signal`；新增用例验证 `uploadProgress.run` 被以正确 items 调用（mock 整个 store） |
| 其他知识库页面测试（若有） | 同上 |

### 测试约束（按 `AGENTS.md`）

- 前端用 Vitest，断言用 `expect(...).toBe(...)` / `rejects.toThrow(...)`，与项目其他前端 spec 保持一致；
- 每个 `it` / `describe` 必须有相邻中文注释，说明覆盖的业务场景；
- table-driven 用例每条单独中文注释覆盖场景。

### jsdom 下的 XHR 测试

jsdom 不实现 `XMLHttpRequest.upload` 的进度事件。测试中用 `vi.stubGlobal('XMLHttpRequest', FakeXHR)` 替换：FakeXHR 是手写最小实现，能手动触发 `upload.onprogress` / `onload` / `onerror` / `abort`，并暴露 `requestHeaders` 数组用于断言头部注入。

### 浏览器手测清单（按 `AGENTS.md`「全功能浏览器验证」要求）

按这个清单在真浏览器走一遍：

1. 组织知识库上传 ~200MB 文件 → Modal 出现，进度条平滑增长到 100%，关闭后列表显示新文件；
2. 应用知识库同上；
3. assistant skill 单独上传 → 同上；
4. assistant skill 批量上传（一次创建多个 skill）→ Modal 显示「正在上传 X（2/3）—— …%」，全部跑完；
5. 取消测试：上传途中点取消，Modal 切到汇总，显示 cancelled；后端日志确认连接被断；
6. 网络中断测试：上传途中 DevTools Network 切 Offline，Modal 显示失败 + 错误文案；
7. 离开警告：上传中按 F5 / 关 tab → 浏览器原生确认框出现；选「留下」上传继续；上传结束后再按 F5 不再提示；
8. 互斥测试：上传途中再点上传按钮 → `n-message.warning` 提示，新请求不发出；
9. 401 测试：上传途中手动清 `ocm.access_token` localStorage（让下个上传 401）→ 自动跳登录页；
10. 零字节文件 → Modal 出现且立即结束，不显示 NaN%。

### 回归确认

- 现有 `mutateAsync({path, file})` 调用方加 `onProgress` / `signal` 后行为不变（两者均 optional）；
- `onSuccess` 改 `onSettled` 后，列表刷新比之前更勤快（失败 / 取消也刷新），不影响正确性。

## 实施顺序建议

1. 写 `xhrUpload.ts` + 单测（与业务解耦，先打地基）；
2. 写 `uploadProgress.ts` store + 单测；
3. 写 `UploadProgressModal.vue` + 单测；
4. 写 `useBeforeUnloadGuard.ts` + 单测；
5. 在 `App.vue` 挂载 Modal + guard；
6. 改三个 mutation hook 内部切到 `xhrUpload`，对外接口扩参；
7. 改三个业务页面（org / app 知识库 + assistant skill）走 `uploadProgress.run`；
8. 更新被影响的现有 spec；
9. 浏览器手测清单全过；
10. 提交。

## 影响范围与风险

- **后端**：无改动。
- **前端公共**：`App.vue` 增加全局 Modal + beforeunload，对其他页面无副作用（Modal 默认不显示）。
- **前端三个上传业务页面**：调用方代码替换为 `uploadProgress.run`，原直接 `mutateAsync` 的等待行为被 store 内部 await 接管，对业务后续逻辑（如路由跳转、列表刷新）无影响。
- **mutation hook 调用方**：接口扩参向后兼容，但 `onSuccess → onSettled` 变更让失败 / 取消也刷新列表，需要 spec 用例确认这点不会引起意外断言失败。
- **风险点**：jsdom 不支持 XHR upload progress —— 已用 FakeXHR 规避；批量场景下并发刷新 query 可能在网络抖动时短暂闪烁列表 —— 可接受。
