# 直传 100% 后补齐「处理中」loading + 文案统一 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让知识库小文件直传（<8MB）在字节传到 100%、等待服务端处理期间复用现有 finalizing 机制显示「处理中…」loading，并把 finalizing 阶段文案从「合并中…」统一为「处理中…」。

**Architecture:** 直传走单个 `octet-stream` XHR；`xhr.upload.onload` 在请求体发送完成（字节传完、等服务端响应）时精确触发。给 `xhrUpload` 加一个可选回调 `onUploadComplete` 绑定该事件，`directUpload` 把它接到既有的 `onFinalizing` 链路（store 的 `currentFinalizing` → 弹窗转圈 + 「处理中…」+ 隐藏取消按钮）。store / 弹窗 / 大文件分片路径不新增状态，仅改展示文案与相关注释。

**Tech Stack:** Vue 3 + TypeScript + Pinia + naive-ui + vue-i18n；测试用 Vitest（`make web-test` → `cd web && npm test -- --run`）。

设计来源：`docs/superpowers/specs/2026-06-27-direct-upload-finalizing-loading-design.md`

---

## 文件结构

| 文件 | 职责 | 本次动作 |
|---|---|---|
| `web/src/api/xhrUpload.ts` | XHR 上传封装 | 新增可选 `onUploadComplete`，绑定 `xhr.upload.onload` |
| `web/src/api/xhrUpload.spec.ts` | xhrUpload 单测 | FakeXHR 支持 `upload.onload`，新增回调用例 |
| `web/src/api/knowledgeUpload.ts` | 直传/分片选择 | `directUpload` 接 `onFinalizing`；主路径与回退直传都透传；订正注释 |
| `web/src/api/knowledgeUpload.spec.ts` | 上传流程单测 | 新增直传触发 onFinalizing、回退直传触发 onFinalizing 用例 |
| `web/src/i18n/locales/zh/components.ts` | 中文文案 | `合并中…` → `处理中…`，订正注释 |
| `web/src/i18n/locales/en/components.ts` | 英文文案 | `Merging…` → `Processing…`，订正注释 |
| `web/src/stores/uploadProgress.ts` | 上传会话状态 | 仅订正 `currentFinalizing` / `onFinalizing` 注释 |
| `web/src/components/UploadProgressModal.vue` | 进度弹窗 | 仅订正「合并」相关注释 |

任务顺序：先底层（xhrUpload）→ 上层（knowledgeUpload）→ 文案 → 注释订正。每个任务自包含、可独立提交。

---

## Task 1: `xhrUpload` 支持「请求体发送完成」回调

**Files:**
- Modify: `web/src/api/xhrUpload.ts`
- Test: `web/src/api/xhrUpload.spec.ts`

- [ ] **Step 1: 给 FakeXHR 补 upload.onload 支持与触发辅助**

在 `web/src/api/xhrUpload.spec.ts` 中修改 `FakeXHR`：把 `upload` 字段加上 `onload`，并新增 `_emitUploadComplete` 辅助方法。

把第 22 行：

```typescript
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null }
```

改为：

```typescript
  // upload 暴露 onprogress 与 onload：onload 对应请求体发送完成（字节已传完、等服务端响应）。
  upload = {
    onprogress: null as ((e: ProgressEvent) => void) | null,
    onload: null as (() => void) | null,
  }
```

在 `_emitProgress` 方法之后（第 43 行后）新增辅助方法：

```typescript
  // 测试辅助：触发 upload.onload，模拟请求体已全部发送完成
  _emitUploadComplete(): void {
    this.upload.onload?.()
  }
```

- [ ] **Step 2: 写失败测试**

在 `web/src/api/xhrUpload.spec.ts` 的 `describe('xhrUpload', ...)` 内、`FormData body` 用例之后新增两条用例：

```typescript
  // onUploadComplete：请求体发送完成（upload.onload）时被调用一次，用于进入「处理中」反馈。
  it('请求体发送完成触发 onUploadComplete', async () => {
    const onUploadComplete = vi.fn()
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']), onUploadComplete })
    FakeXHR.lastInstance!._emitUploadComplete()
    FakeXHR.lastInstance!._complete(200, '{}')
    await promise
    expect(onUploadComplete).toHaveBeenCalledTimes(1)
  })

  // 不传 onUploadComplete：upload.onload 触发也不报错（可选回调，缺省无操作）。
  it('未传 onUploadComplete 时 upload.onload 不报错', async () => {
    const { xhrUpload } = await import('./xhrUpload')
    const promise = xhrUpload('/api/v1/upload', { body: new Blob(['x']) })
    FakeXHR.lastInstance!._emitUploadComplete()
    FakeXHR.lastInstance!._complete(200, '{}')
    await expect(promise).resolves.toMatchObject({ status: 200 })
  })
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd web && npm test -- --run xhrUpload`
Expected: FAIL —「请求体发送完成触发 onUploadComplete」失败，`onUploadComplete` 调用次数为 0（因为 `xhrUpload` 尚未绑定 `xhr.upload.onload`）。

- [ ] **Step 4: 实现 onUploadComplete**

在 `web/src/api/xhrUpload.ts` 的 `XhrUploadOptions` 接口中，`signal` 字段之后新增：

```typescript
  // 请求体发送完成（字节已全部上传、等待服务端响应）时触发。
  // 直传场景据此进入「处理中」反馈，避免进度卡在 100% 看起来像卡死。
  onUploadComplete?: () => void
```

在 `xhrUpload` 函数体内，进度事件绑定块（现 `if (opts.onProgress) { ... }`）之后新增：

```typescript
    // upload.onload：浏览器在请求体发送完成时触发——此刻字节已全部上传，仅在等服务端响应。
    if (opts.onUploadComplete) {
      xhr.upload.onload = () => {
        opts.onUploadComplete!()
      }
    }
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd web && npm test -- --run xhrUpload`
Expected: PASS（全部 xhrUpload 用例通过）。

- [ ] **Step 6: 提交**

```bash
git add web/src/api/xhrUpload.ts web/src/api/xhrUpload.spec.ts
git commit -m "feat(upload): xhrUpload 暴露 onUploadComplete 回调

绑定 xhr.upload.onload，在请求体发送完成（字节已全部上传、等待服务端
响应）时触发。直传场景据此进入「处理中」反馈，消除进度卡在 100% 的
卡死错觉。可选回调，未传时行为不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: 直传接入 onFinalizing（含回退直传路径）

**Files:**
- Modify: `web/src/api/knowledgeUpload.ts`
- Test: `web/src/api/knowledgeUpload.spec.ts`

- [ ] **Step 1: 写失败测试**

在 `web/src/api/knowledgeUpload.spec.ts` 中，xhrUpload 的 mock 需要能模拟「触发 onUploadComplete 后再 resolve」。当前 mock 用 `mockResolvedValue`，不会调用传入的 `onUploadComplete`。新增两条用例，用 `mockImplementation` 手动触发回调。

在 `describe('知识库文件上传', ...)` 内、「小文件走直传不分片」用例之后新增：

```typescript
  // 小文件直传：xhrUpload 收到 onUploadComplete 后（请求体发完），onFinalizing 被触发一次，
  // 用于前端在等服务端响应期间显示「处理中…」。
  it('小文件直传请求体发完后触发 onFinalizing', async () => {
    vi.mocked(xhrUpload).mockImplementation(async (_url, opts) => {
      opts.onUploadComplete?.() // 模拟浏览器请求体发送完成
      return { status: 202, body: {} }
    })
    const onFinalizing = vi.fn()
    await uploadKnowledgeFile(target, makeFile(1024), undefined, undefined, onFinalizing)
    expect(onFinalizing).toHaveBeenCalledTimes(1)
  })
```

并把「后端未启用分片时回退直传」用例改造为同时断言回退后的直传也触发 onFinalizing。替换原 `it('后端未启用分片时回退直传', ...)` 整段为：

```typescript
  // init 返回 503 分片不可用：回退到直传，保证功能可用；回退后的直传同样在请求体发完时触发 onFinalizing。
  it('后端未启用分片时回退直传并触发 onFinalizing', async () => {
    vi.mocked(apiRequest).mockRejectedValue(
      Object.assign(new Error('unavailable'), { status: 503, body: { code: 'KNOWLEDGE_MULTIPART_UNAVAILABLE' } }),
    )
    vi.mocked(xhrUpload).mockImplementation(async (_url, opts) => {
      opts.onUploadComplete?.() // 回退直传：模拟请求体发送完成
      return { status: 202, body: {} }
    })

    const onFinalizing = vi.fn()
    await uploadKnowledgeFile(target, makeFile(17 * 1024 * 1024), undefined, undefined, onFinalizing)

    // 直传被调用（POST 到 directPath），且未发生 PUT 分片
    const calls = vi.mocked(xhrUpload).mock.calls
    expect(calls).toHaveLength(1)
    expect(calls[0][0]).toContain('/api/v1/organizations/o1/knowledge?filename=')
    expect(calls[0][1].method).toBe('POST')
    // 回退直传也进入「处理中」阶段
    expect(onFinalizing).toHaveBeenCalledTimes(1)
  })
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npm test -- --run knowledgeUpload`
Expected: FAIL —「小文件直传请求体发完后触发 onFinalizing」与回退用例失败，`onFinalizing` 调用次数为 0（`directUpload` 尚未把 `onFinalizing` 接到 `onUploadComplete`）。

- [ ] **Step 3: 实现 directUpload 接入 onFinalizing**

在 `web/src/api/knowledgeUpload.ts` 中：

(a) 订正 `uploadKnowledgeFile` 顶部文档注释（现第 28-29 行），把「直传不调，直传的 100% 仍是上传中」改为：

```typescript
// uploadKnowledgeFile 按文件大小选择直传或分片上传；onProgress 上报聚合字节进度，signal 支持取消，
// onFinalizing 在字节传完、进入服务端处理阶段时触发：分片上传在 complete 前调用，直传在请求体发完、
// 等服务端响应期间调用。前端据此显示「处理中…」，避免进度卡在 100% 看起来像卡死。
```

(b) 在 `uploadKnowledgeFile` 的两处 `directUpload` 调用补传 `onFinalizing`：

主路径（现第 38 行）：

```typescript
  if (file.size < CHUNK_THRESHOLD) {
    await directUpload(target.directPath, file, onProgress, signal, onFinalizing)
    return
  }
```

回退路径（现第 46 行）：

```typescript
    if (isMultipartUnavailable(err)) {
      await directUpload(target.directPath, file, onProgress, signal, onFinalizing)
      return
    }
```

(c) 给 `directUpload` 新增参数并透传为 `onUploadComplete`。替换整个 `directUpload` 函数（现第 53-68 行）为：

```typescript
// directUpload 以 application/octet-stream 把整个文件直传到知识库端点（原有行为）。
// onFinalizing 在请求体发送完成（字节已全部上传、等服务端把文件推给 RAGFlow）时触发，
// 前端据此从字节进度切到「处理中…」，消除卡在 100% 的错觉。
async function directUpload(
  directPath: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
  onFinalizing?: () => void,
): Promise<void> {
  const params = new URLSearchParams({ filename: file.name })
  await xhrUpload(`${directPath}?${params.toString()}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/octet-stream' },
    body: file,
    onProgress,
    signal,
    onUploadComplete: onFinalizing,
  })
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npm test -- --run knowledgeUpload`
Expected: PASS（含新增直传用例、改造后的回退用例，及原有分片用例）。

- [ ] **Step 5: 提交**

```bash
git add web/src/api/knowledgeUpload.ts web/src/api/knowledgeUpload.spec.ts
git commit -m "feat(upload): 小文件直传也进入「处理中」finalizing 阶段

directUpload 新增 onFinalizing 参数，透传为 xhrUpload 的 onUploadComplete：
请求体发完、等服务端推送 RAGFlow 期间触发，前端复用既有 finalizing 链路
显示 loading。主路径与「分片不可用回退直传」路径都接入。订正相关注释。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: 文案统一为「处理中…」/「Processing…」

**Files:**
- Modify: `web/src/i18n/locales/zh/components.ts:184-185`
- Modify: `web/src/i18n/locales/en/components.ts:184-185`
- Test: `web/src/stores/uploadProgress.spec.ts`、`web/src/components/__tests__/UploadProgressModal.spec.ts`（检查是否硬编码旧文案）

- [ ] **Step 1: 检查测试中是否硬编码了旧文案**

Run: `cd web && grep -rn "合并中\|Merging" src/`
Expected: 列出所有引用。除 i18n 文件本身外，若有测试断言 `合并中…` / `Merging…`，记录文件与行号，下一步一并改为新文案；若无（预期 store/modal 测试通过 i18n key 间接断言，不硬编码字面量），跳过测试改动。

- [ ] **Step 2: 改中文文案与注释**

在 `web/src/i18n/locales/zh/components.ts` 第 184-185 行：

```typescript
    // finalizing：上传字节已传完、服务端处理（合并分片 / 推送 RAGFlow）期间的提示，避免看起来卡在 100%。
    finalizing: '处理中…',
```

- [ ] **Step 3: 改英文文案与注释**

在 `web/src/i18n/locales/en/components.ts` 第 184-185 行：

```typescript
    // finalizing：上传字节已传完、服务端处理（合并分片 / 推送 RAGFlow）期间的提示，避免看起来卡在 100%。
    finalizing: 'Processing…',
```

- [ ] **Step 4: 若 Step 1 发现硬编码断言，改为新文案**

仅当 Step 1 找到测试中硬编码 `合并中…` / `Merging…` 时执行：把断言里的旧字面量替换为 `处理中…` / `Processing…`。若 Step 1 未发现，跳过本步。

- [ ] **Step 5: 运行相关测试确认通过**

Run: `cd web && npm test -- --run uploadProgress UploadProgressModal`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/i18n/locales/zh/components.ts web/src/i18n/locales/en/components.ts
git commit -m "feat(upload): finalizing 文案统一为「处理中…」/Processing…

「合并中…」是实现视角（用户不理解「合并」），改为用户视角的「处理中…」，
直传与分片两条路径共用同一文案。保留 finalizing key 名不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

> 若 Step 4 改了测试文件，把对应测试文件一并 `git add` 进本次提交。

---

## Task 4: 订正 store 与弹窗中与新行为不符的注释

**Files:**
- Modify: `web/src/stores/uploadProgress.ts`（`currentFinalizing` 字段注释、`RunnerContext.onFinalizing` 注释、循环内 onFinalizing 注释）
- Modify: `web/src/components/UploadProgressModal.vue`（模板内「合并阶段」注释、`isFinalizing` 计算属性注释）

本任务只改注释，不改逻辑，无新增测试；完成后跑一遍全量前端测试确保零回归。

- [ ] **Step 1: 订正 uploadProgress.ts 注释**

在 `web/src/stores/uploadProgress.ts`：

`UploadSession.currentFinalizing` 字段注释（现第 29-31 行）改为：

```typescript
  // 当前 item 是否进入服务端「处理/收尾」阶段（字节已传完 100%）：
  // 分片上传在 complete 期间、直传在请求体发完等服务端响应期间，UI 据此显示「处理中…」而非干卡 100%。
  // 可选：run() 总会初始化为 false，但部分测试夹具构造 session 时不关心该字段。
```

`RunnerContext.onFinalizing` 注释（现第 43-44 行）改为：

```typescript
// RunnerContext 由 store 注入给 runner：onProgress 上报字节进度，signal 响应取消，
// onFinalizing 在字节传完、进入服务端处理阶段时调用（分片上传 complete 前、直传请求体发完后）。
```

循环内 `onFinalizing` 回调注释（现第 127 行）改为：

```typescript
            // 进入服务端处理阶段：同样加 currentIndex 守卫，避免延迟回调污染下一文件。
```

- [ ] **Step 2: 订正 UploadProgressModal.vue 注释**

在 `web/src/components/UploadProgressModal.vue`：

模板内进度文案注释（现第 22 行）改为：

```vue
        <!-- 处理阶段：字节已传完，服务端在合并分片 / 推送 RAGFlow，显示「处理中…」避免看起来卡死在 100% -->
```

`isFinalizing` 计算属性注释（现第 74-75 行）改为：

```typescript
// isFinalizing 标识当前文件进入服务端处理阶段（字节 100% 但仍在处理）：分片 complete 期间或直传等响应期间，
// 用于把「X MB / Y MB」换成「处理中…」并让进度条动画起来，消除"卡在 100%"的错觉。
```

- [ ] **Step 3: 跑全量前端测试确认零回归**

Run: `make web-test`
Expected: PASS（全部前端单测通过）。

- [ ] **Step 4: 提交**

```bash
git add web/src/stores/uploadProgress.ts web/src/components/UploadProgressModal.vue
git commit -m "docs(upload): 订正 finalizing 注释，涵盖直传处理阶段

直传现在也会进入 finalizing；把 store 与弹窗里「仅分片/合并中」的注释
改为涵盖两条路径、文案为「处理中」，保持注释与行为一致。仅注释，无逻辑改动。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: 浏览器真机验证（CLAUDE.md 要求）

**Files:** 无代码改动；本地 k3d 环境手工验证。

- [ ] **Step 1: 确认前端类型检查与全量测试通过**

Run: `make web-typecheck && make web-test`
Expected: 均 PASS。

- [ ] **Step 2: 在本地 manager 后台上传小文件验证「处理中…」**

用浏览器登录本地 manager 后台（http://ocm.localhost，admin/admin123），进入某组织或应用的知识库页面，上传一个 **<8MB** 文件，观察：
- 进度条到 100% 后出现转圈动画 + 文案「处理中…」，取消按钮消失；
- 服务端响应后切到汇总视图「成功 N」。
Expected: 不再出现进度停在 100% 无动画的卡死观感。

- [ ] **Step 3: 上传大文件验证文案为「处理中…」**

上传一个 **≥8MB** 文件，观察 finalizing 阶段文案为「处理中…」（不再是「合并中…」）。
Expected: 通过。

- [ ] **Step 4: 切换语言验证英文文案**

把界面语言切到英文，重复 Step 2 / Step 3，确认文案为 `Processing…`。
Expected: 通过。

> 如任一步发现问题，先修复并重新验证，直到全部正常再交付。

---

## Self-Review 记录

- **Spec 覆盖：** 直传补 loading（Task 1+2）、文案统一处理中/Processing（Task 3）、注释订正（Task 4）、浏览器三场景 + 中英文验证（Task 5）、回退直传路径覆盖（Task 2 Step 1/3）—— 均有对应任务。非目标（列表解析阶段、标识符改名）已在 spec 明确排除，计划未触碰。
- **占位符扫描：** 无 TBD/TODO；每个代码步骤均给出完整代码与精确文件/行号、精确命令与预期输出。
- **类型一致性：** 新增标识符贯穿一致——`onUploadComplete`（xhrUpload 选项）、`onFinalizing`（directUpload 参数）、既有 `currentFinalizing`/`isFinalizing` 不改名；Task 1 定义的 `onUploadComplete` 在 Task 2 的 `directUpload` 中以 `onUploadComplete: onFinalizing` 消费，签名匹配。
