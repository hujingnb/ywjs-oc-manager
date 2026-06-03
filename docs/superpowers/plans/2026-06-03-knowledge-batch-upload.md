# Knowledge Batch Upload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add batch upload for org and app knowledge bases through multi-select file input and drag-and-drop, while reusing the existing single-file backend API and global upload progress modal.

**Architecture:** Keep the backend contract unchanged. Add a small frontend helper for collecting and filtering files, then wire both knowledge pages to pass a `File[]` into the existing `uploadProgress.run` serial upload flow.

**Tech Stack:** Vue 3, TypeScript, Pinia `uploadProgress` store, TanStack Vue Query mutations, Naive UI, Vitest.

---

## File Structure

- Create: `web/src/pages/knowledge/knowledgeUploadBatch.ts`
  - Shared helper for input/drop file collection, oversized-file filtering, drag detection, and conversion to `uploadProgress` items.
- Create: `web/src/pages/knowledge/knowledgeUploadBatch.spec.ts`
  - Unit tests for the shared helper.
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
  - Add `multiple`, drag/drop handling, and batch upload queue construction for org knowledge.
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
  - Add tests for org multi-select and drag/drop upload wiring.
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
  - Add `multiple`, drag/drop handling, and batch upload queue construction for app knowledge.
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`
  - Add tests for app multi-select and drag/drop upload wiring.
- Modify: `docs/knowledge-base.md`
  - Document multi-select, drag/drop, serial upload, failure behavior, and the current 1GB single-file limit.
- Modify: `docs/user-manual.md`
  - Update org and app knowledge upload instructions for users.

No backend files, generated OpenAPI files, or generated frontend API types should change.

## Task 1: Shared Batch Upload Helper

**Files:**
- Create: `web/src/pages/knowledge/knowledgeUploadBatch.ts`
- Create: `web/src/pages/knowledge/knowledgeUploadBatch.spec.ts`

- [ ] **Step 1: Write the failing helper tests**

Create `web/src/pages/knowledge/knowledgeUploadBatch.spec.ts` with this content:

```ts
import { describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES, KNOWLEDGE_UPLOAD_MAX_MESSAGE } from '@/api/hooks/useKnowledge'
import {
  filterKnowledgeUploadFiles,
  hasKnowledgeFilesInDrag,
  knowledgeFilesFromDrop,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from './knowledgeUploadBatch'

// fileWithSize 构造指定 size 的 File，用于覆盖上传上限边界。
function fileWithSize(name: string, size: number): File {
  const file = new File(['x'], name)
  Object.defineProperty(file, 'size', { value: size, configurable: true })
  return file
}

describe('knowledgeUploadBatch', () => {
  // 覆盖 input 多选：原生 FileList 应按选择顺序转为数组。
  it('从 input 中按顺序收集多个文件', () => {
    const input = document.createElement('input')
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')
    Object.defineProperty(input, 'files', { value: [first, second], configurable: true })

    expect(knowledgeFilesFromInput(input)).toEqual([first, second])
  })

  // 覆盖拖拽文件：只收集 kind=file 且 getAsFile 成功的条目，目录或文本项会被忽略。
  it('从 drop 事件中收集文件并忽略非文件项', () => {
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')
    const event = {
      dataTransfer: {
        items: [
          { kind: 'file', getAsFile: () => first }, // 场景：普通文件进入上传队列。
          { kind: 'string', getAsFile: () => null }, // 场景：拖拽文本不应进入上传队列。
          { kind: 'file', getAsFile: () => null }, // 场景：目录项在这里表现为无法取到 File，应被忽略。
          { kind: 'file', getAsFile: () => second }, // 场景：后续普通文件保持原顺序。
        ],
      },
    } as unknown as DragEvent

    expect(knowledgeFilesFromDrop(event)).toEqual([first, second])
  })

  // 覆盖 dragover 判断：存在文件项时才标记为可上传拖拽。
  it('识别包含文件的拖拽事件', () => {
    const fileEvent = {
      dataTransfer: {
        items: [
          { kind: 'string' }, // 场景：混入文本项时不影响文件判断。
          { kind: 'file' }, // 场景：至少一个文件项即可允许页面进入拖拽态。
        ],
      },
    } as unknown as DragEvent
    const textEvent = {
      dataTransfer: {
        items: [
          { kind: 'string' }, // 场景：纯文本拖拽不应触发上传态。
        ],
      },
    } as unknown as DragEvent

    expect(hasKnowledgeFilesInDrag(fileEvent)).toBe(true)
    expect(hasKnowledgeFilesInDrag(textEvent)).toBe(false)
  })

  // 覆盖单文件上限过滤：超过上限的文件被跳过，合法文件仍继续上传。
  it('过滤超过单文件上限的文件并保留合法文件', () => {
    const warning = vi.fn()
    const ok = fileWithSize('ok.md', KNOWLEDGE_UPLOAD_MAX_BYTES)
    const tooLarge = fileWithSize('too-large.md', KNOWLEDGE_UPLOAD_MAX_BYTES + 1)

    expect(filterKnowledgeUploadFiles([tooLarge, ok], warning)).toEqual([ok])
    expect(warning).toHaveBeenCalledWith(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
  })

  // 覆盖批量 items：上传队列 label 使用文件名，File 对象必须原样传递给 XHR 上传。
  it('把文件转换为 uploadProgress items', () => {
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    expect(toKnowledgeUploadItems([first, second])).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
  })
})
```

- [ ] **Step 2: Run helper tests and verify they fail**

Run:

```bash
cd web && npm run test -- src/pages/knowledge/knowledgeUploadBatch.spec.ts --run
```

Expected: FAIL because `./knowledgeUploadBatch` does not exist.

- [ ] **Step 3: Add the helper implementation**

Create `web/src/pages/knowledge/knowledgeUploadBatch.ts` with this content:

```ts
// knowledgeUploadBatch 提供知识库多文件上传的前端编排 helper。
// 后端仍是单文件接口，这里只负责把 input/drop 事件转成 uploadProgress 可消费的队列。
import { KNOWLEDGE_UPLOAD_MAX_MESSAGE, isKnowledgeUploadTooLarge } from '@/api/hooks/useKnowledge'
import type { RunItem } from '@/stores/uploadProgress'

type WarningFn = (message: string) => void

// knowledgeFilesFromInput 将原生 file input 的 FileList 转为数组，保留浏览器选择顺序。
export function knowledgeFilesFromInput(input: HTMLInputElement): File[] {
  return Array.from(input.files ?? [])
}

// knowledgeFilesFromDrop 从拖拽事件中收集文件；目录和文本项不会返回 File，因此会被过滤。
export function knowledgeFilesFromDrop(event: DragEvent): File[] {
  const transfer = event.dataTransfer
  if (!transfer) return []
  if (transfer.items && transfer.items.length > 0) {
    return Array.from(transfer.items)
      .filter(item => item.kind === 'file')
      .map(item => item.getAsFile())
      .filter((file): file is File => file instanceof File)
  }
  return Array.from(transfer.files ?? [])
}

// hasKnowledgeFilesInDrag 用于 dragenter/dragover 判断是否需要进入可上传视觉态。
export function hasKnowledgeFilesInDrag(event: DragEvent): boolean {
  const transfer = event.dataTransfer
  if (!transfer) return false
  if (transfer.items && transfer.items.length > 0) {
    return Array.from(transfer.items).some(item => item.kind === 'file')
  }
  return (transfer.files?.length ?? 0) > 0
}

// filterKnowledgeUploadFiles 只做单文件上限拦截；容量不足等动态条件交给后端逐个判断。
export function filterKnowledgeUploadFiles(files: File[], warning: WarningFn): File[] {
  const accepted: File[] = []
  let rejectedCount = 0
  for (const file of files) {
    if (isKnowledgeUploadTooLarge(file)) {
      rejectedCount += 1
      continue
    }
    accepted.push(file)
  }
  if (rejectedCount === 1) {
    warning(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
  } else if (rejectedCount > 1) {
    warning(`已跳过 ${rejectedCount} 个超过上限的文件，${KNOWLEDGE_UPLOAD_MAX_MESSAGE}`)
  }
  return accepted
}

// toKnowledgeUploadItems 将 File[] 转成全局上传进度 store 的最小队列结构。
export function toKnowledgeUploadItems(files: File[]): RunItem[] {
  return files.map(file => ({ file, label: file.name }))
}
```

- [ ] **Step 4: Run helper tests and verify they pass**

Run:

```bash
cd web && npm run test -- src/pages/knowledge/knowledgeUploadBatch.spec.ts --run
```

Expected: PASS.

- [ ] **Step 5: Commit helper**

Run:

```bash
git add web/src/pages/knowledge/knowledgeUploadBatch.ts web/src/pages/knowledge/knowledgeUploadBatch.spec.ts
git commit -m "feat(web): 增加知识库批量上传辅助方法" -m "抽取知识库多文件上传的前端 helper。\n\nhelper 负责从 input/drop 事件收集文件、过滤超过单文件上限的文件，并转换为全局上传进度队列。\n\n测试覆盖多选、拖拽、目录忽略、上限过滤和队列转换。"
```

Expected: commit succeeds.

## Task 2: Org Knowledge Batch Upload

**Files:**
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`

- [ ] **Step 1: Write failing org page tests**

In `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`, add these test cases inside the existing `describe('OrgKnowledgePage', () => { ... })` block:

```ts
  // 覆盖企业知识库多选上传：多个文件应按选择顺序交给全局上传队列。
  it('支持一次选择多个企业知识库文件上传', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('input[type="file"]')
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    Object.defineProperty(input.element, 'files', { value: [first, second], configurable: true })
    await input.trigger('change')

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
  })

  // 覆盖企业知识库拖拽上传：拖入多个文件时复用同一批量上传流程。
  it('支持拖拽多个企业知识库文件上传', async () => {
    const wrapper = mountPage()
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    await wrapper.find('section').trigger('drop', {
      dataTransfer: {
        items: [],
        files: [first, second],
      },
    })

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
  })
```

- [ ] **Step 2: Run org tests and verify they fail**

Run:

```bash
cd web && npm run test -- src/pages/knowledge/OrgKnowledgePage.spec.ts --run
```

Expected: FAIL because the page still reads only `files[0]` and has no drop handler.

- [ ] **Step 3: Wire org page batch upload**

In `web/src/pages/knowledge/OrgKnowledgePage.vue`, make these exact changes.

Replace the opening `n-card` tag:

```vue
    <n-card :bordered="true">
```

with:

```vue
    <n-card
      :bordered="true"
      class="knowledge-drop-zone"
      :class="{ 'drag-active': dragActive && canManage }"
      @dragenter.prevent="onDragEnter"
      @dragover.prevent="onDragOver"
      @dragleave.prevent="onDragLeave"
      @drop.prevent="onDropUpload"
    >
```

Replace the file input:

```vue
            <input class="hidden-input" type="file" :disabled="!canManage" @change="onUpload" />
```

with:

```vue
            <input class="hidden-input" type="file" multiple :disabled="!canManage" @change="onUpload" />
```

Replace the `useKnowledge` import block:

```ts
  formatKnowledgeBytes,
  isKnowledgeUploadOverRemaining,
  isKnowledgeUploadTooLarge,
  useDeleteOrgKnowledge,
```

with:

```ts
  formatKnowledgeBytes,
  useDeleteOrgKnowledge,
```

Add this import below the existing imports from stores:

```ts
import {
  filterKnowledgeUploadFiles,
  hasKnowledgeFilesInDrag,
  knowledgeFilesFromDrop,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from './knowledgeUploadBatch'
```

Add this state near `const downloading = ref(false)`:

```ts
// dragActive 标记当前卡片是否处于可上传拖拽态，仅有写权限时才会置 true。
const dragActive = ref(false)
```

Replace the entire existing `onUpload` function with:

```ts
// uploadFiles 把多选或拖拽得到的文件交给全局上传队列；容量不足等动态失败由后端逐个返回。
async function uploadFiles(files: File[]) {
  const uploadableFiles = filterKnowledgeUploadFiles(files, message.warning)
  if (uploadableFiles.length === 0) return
  try {
    await uploadProgress.run(toKnowledgeUploadItems(uploadableFiles), async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    // 唯一会被抛出的错误是「会话互斥」：仅此一种情况下提示用户。
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}

// onUpload 将文件上传到 RAGFlow 组织 dataset；上传进度统一由全局 UploadProgressModal 展示。
// 互斥规则：会话进行中 store.run 抛错，业务侧用 n-message 提示用户。
async function onUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = knowledgeFilesFromInput(input)
  // 不论是否真正发起上传，都清空 input.value 以便用户重新选择同名文件。
  input.value = ''
  if (!canManage.value) return
  await uploadFiles(files)
}

// onDragEnter 在拖入文件时打开视觉态；纯文本拖拽不影响知识库卡片。
function onDragEnter(event: DragEvent) {
  if (!canManage.value || !hasKnowledgeFilesInDrag(event)) return
  dragActive.value = true
}

// onDragOver 持续维持可上传视觉态，并让浏览器显示 copy dropEffect。
function onDragOver(event: DragEvent) {
  if (!canManage.value || !hasKnowledgeFilesInDrag(event)) return
  dragActive.value = true
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'copy'
  }
}

// onDragLeave 在拖拽离开卡片时关闭视觉态。
function onDragLeave() {
  dragActive.value = false
}

// onDropUpload 处理拖拽文件上传；目录或非文件项会在 helper 中被过滤。
async function onDropUpload(event: DragEvent) {
  dragActive.value = false
  if (!canManage.value) return
  await uploadFiles(knowledgeFilesFromDrop(event))
}
```

Add this CSS before `.hidden-input`:

```css
.knowledge-drop-zone {
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.knowledge-drop-zone.drag-active {
  border-color: var(--color-brand);
  box-shadow: 0 0 0 2px rgba(255, 106, 0, 0.14);
}
```

- [ ] **Step 4: Run org tests and verify they pass**

Run:

```bash
cd web && npm run test -- src/pages/knowledge/OrgKnowledgePage.spec.ts --run
```

Expected: PASS.

- [ ] **Step 5: Commit org page**

Run:

```bash
git add web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/knowledge/OrgKnowledgePage.spec.ts
git commit -m "feat(web): 企业知识库支持批量上传" -m "企业知识库上传入口支持多选文件和拖拽文件。\n\n批量上传复用全局上传进度弹窗串行执行，单个文件失败不阻塞后续文件。\n\n前端仅拦截超过单文件上限的文件，容量不足等动态失败由后端逐个返回。"
```

Expected: commit succeeds.

## Task 3: App Knowledge Batch Upload

**Files:**
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`

- [ ] **Step 1: Write failing app page tests**

In `web/src/pages/apps/AppKnowledgeTab.spec.ts`, add these test cases inside the existing `describe('AppKnowledgeTab', () => { ... })` block:

```ts
  // 覆盖实例知识库多选上传：多个文件应按选择顺序交给全局上传队列。
  it('支持一次选择多个实例知识库文件上传', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    Object.defineProperty(input.element, 'files', { value: [first, second], configurable: true })
    await input.trigger('change')

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
  })

  // 覆盖实例知识库拖拽上传：拖入多个文件时复用同一批量上传流程。
  it('支持拖拽多个实例知识库文件上传', async () => {
    const wrapper = mountTab()
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    await wrapper.find('section').trigger('drop', {
      dataTransfer: {
        items: [],
        files: [first, second],
      },
    })

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
  })
```

- [ ] **Step 2: Run app tests and verify they fail**

Run:

```bash
cd web && npm run test -- src/pages/apps/AppKnowledgeTab.spec.ts --run
```

Expected: FAIL because the page still reads only `files[0]` and has no drop handler.

- [ ] **Step 3: Wire app page batch upload**

In `web/src/pages/apps/AppKnowledgeTab.vue`, make these exact changes.

Replace the opening `n-card` tag:

```vue
  <n-card :bordered="true">
```

with:

```vue
  <n-card
    :bordered="true"
    class="knowledge-drop-zone"
    :class="{ 'drag-active': dragActive && canManage }"
    @dragenter.prevent="onDragEnter"
    @dragover.prevent="onDragOver"
    @dragleave.prevent="onDragLeave"
    @drop.prevent="onDropUpload"
  >
```

Replace the file input:

```vue
            <input type="file" :disabled="uploading" @change="onUploadFile" />
```

with:

```vue
            <input type="file" multiple :disabled="uploading" @change="onUploadFile" />
```

Replace the `useKnowledge` import block:

```ts
  formatKnowledgeBytes,
  isKnowledgeUploadOverRemaining,
  isKnowledgeUploadTooLarge,
  useAppKnowledgeQuery,
```

with:

```ts
  formatKnowledgeBytes,
  useAppKnowledgeQuery,
```

Add this import below the existing store imports:

```ts
import {
  filterKnowledgeUploadFiles,
  hasKnowledgeFilesInDrag,
  knowledgeFilesFromDrop,
  knowledgeFilesFromInput,
  toKnowledgeUploadItems,
} from '@/pages/knowledge/knowledgeUploadBatch'
```

Add this state near `const downloading = ref(false)`:

```ts
// dragActive 标记当前卡片是否处于可上传拖拽态，仅有写权限时才会置 true。
const dragActive = ref(false)
```

Replace the entire existing `onUploadFile` function with:

```ts
// uploadFiles 把多选或拖拽得到的文件交给全局上传队列；容量不足等动态失败由后端逐个返回。
async function uploadFiles(files: File[]) {
  const uploadableFiles = filterKnowledgeUploadFiles(files, message.warning)
  if (uploadableFiles.length === 0) return
  try {
    await uploadProgress.run(toKnowledgeUploadItems(uploadableFiles), async (_item, f, ctx) => {
      await uploadMutation.mutateAsync({
        file: f,
        onProgress: ctx.onProgress,
        signal: ctx.signal,
      })
    })
  } catch (err) {
    message.warning(err instanceof Error ? err.message : '已有上传任务正在进行')
  }
}

// onUploadFile 处理原生 file input 事件；上传进度统一由全局 UploadProgressModal 展示。
// 失败 / 取消的视觉反馈也来自 Modal 汇总区，本页只承担互斥提示。
async function onUploadFile(event: Event) {
  errorMessage.value = ''
  const input = event.target as HTMLInputElement
  const files = knowledgeFilesFromInput(input)
  input.value = ''
  if (!canManage.value) return
  await uploadFiles(files)
}

// onDragEnter 在拖入文件时打开视觉态；纯文本拖拽不影响知识库卡片。
function onDragEnter(event: DragEvent) {
  if (!canManage.value || !hasKnowledgeFilesInDrag(event)) return
  dragActive.value = true
}

// onDragOver 持续维持可上传视觉态，并让浏览器显示 copy dropEffect。
function onDragOver(event: DragEvent) {
  if (!canManage.value || !hasKnowledgeFilesInDrag(event)) return
  dragActive.value = true
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'copy'
  }
}

// onDragLeave 在拖拽离开卡片时关闭视觉态。
function onDragLeave() {
  dragActive.value = false
}

// onDropUpload 处理拖拽文件上传；目录或非文件项会在 helper 中被过滤。
async function onDropUpload(event: DragEvent) {
  errorMessage.value = ''
  dragActive.value = false
  if (!canManage.value) return
  await uploadFiles(knowledgeFilesFromDrop(event))
}
```

Add this CSS before `.file-picker`:

```css
.knowledge-drop-zone {
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.knowledge-drop-zone.drag-active {
  border-color: var(--color-brand);
  box-shadow: 0 0 0 2px rgba(255, 106, 0, 0.14);
}
```

- [ ] **Step 4: Run app tests and verify they pass**

Run:

```bash
cd web && npm run test -- src/pages/apps/AppKnowledgeTab.spec.ts --run
```

Expected: PASS.

- [ ] **Step 5: Commit app page**

Run:

```bash
git add web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppKnowledgeTab.spec.ts
git commit -m "feat(web): 实例知识库支持批量上传" -m "实例知识库上传入口支持多选文件和拖拽文件。\n\n批量上传复用全局上传进度弹窗串行执行，单个文件失败不阻塞后续文件。\n\n前端仅拦截超过单文件上限的文件，容量不足等动态失败由后端逐个返回。"
```

Expected: commit succeeds.

## Task 4: Documentation and Final Verification

**Files:**
- Modify: `docs/knowledge-base.md`
- Modify: `docs/user-manual.md`

- [ ] **Step 1: Update knowledge-base documentation**

In `docs/knowledge-base.md`, replace the “行为” bullets under `### 4.1 管理后台上传（人工）` with:

```markdown
行为：

- 支持一次选择多个文件，也支持把多个文件拖拽到知识库文件列表卡片区域。
- 批量上传按文件顺序串行执行；单个文件失败不会阻塞后续文件，结束后由上传进度弹窗汇总成功、失败和取消数量。
- 每个文件上传成功后都会进入对应 dataset 并**触发解析**；上传成功只表示「文件已进入 RAGFlow 且解析已触发」，
  **不等待解析完成**，解析状态后续异步刷新。
- 单文件上限 **1GB**（与后端 `maxKnowledgeUploadBytes` 和前端 `KNOWLEDGE_UPLOAD_MAX_BYTES` 保持一致）。
```

- [ ] **Step 2: Update user manual app knowledge section**

In `docs/user-manual.md`, under `#### 实例知识库 tab（/apps/:appId/knowledge）`, replace the upload paragraph:

```markdown
右上角「上传文件」按钮（`<label>` 包装原生 `file input`），选择文件后立即上传到当前实例知识库并触发解析。
```

with:

```markdown
右上角「上传文件」按钮（`<label>` 包装原生 `file input`）支持一次选择多个文件，也可以把多个文件拖拽到知识库文件列表卡片区域。批量上传会按文件顺序串行执行，每个文件上传后立即进入当前实例知识库并触发解析；单个文件失败不会阻塞后续文件，上传进度弹窗会汇总成功、失败和取消数量。
```

- [ ] **Step 3: Update user manual org knowledge section**

In `docs/user-manual.md`, under `### 2.5 企业级知识库`, replace the upload line:

```markdown
**上传文件**：点击右上角「上传文件」，选择文件后上传到企业知识库。上传完成表示文件已进入 RAGFlow 并触发解析，不等待解析完成。
```

with:

```markdown
**上传文件**：点击右上角「上传文件」可一次选择多个文件，也可以把多个文件拖拽到知识库文件列表卡片区域。批量上传按文件顺序串行执行；单个文件失败不会阻塞后续文件。每个文件上传完成表示该文件已进入 RAGFlow 并触发解析，不等待解析完成。
```

- [ ] **Step 4: Run focused frontend tests**

Run:

```bash
cd web && npm run test -- src/pages/knowledge/knowledgeUploadBatch.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts --run
```

Expected: PASS.

- [ ] **Step 5: Run typecheck**

Run:

```bash
cd web && npm run typecheck
```

Expected: PASS.

- [ ] **Step 6: Verify OpenAPI generated files did not change**

Run:

```bash
git status --short openapi/openapi.yaml web/src/api/generated.ts
```

Expected: no output.

- [ ] **Step 7: Commit docs and verification**

Run:

```bash
git add docs/knowledge-base.md docs/user-manual.md
git commit -m "docs: 更新知识库批量上传说明" -m "同步企业知识库和实例知识库的多选与拖拽上传说明。\n\n文档明确批量上传串行执行、单个文件失败不阻塞后续文件，并修正单文件上限为当前代码使用的 1GB。"
```

Expected: commit succeeds.

## Final Checks

- [ ] **Step 1: Confirm working tree is clean**

Run:

```bash
git status --short
```

Expected: no output.

- [ ] **Step 2: Review commit history for task boundaries**

Run:

```bash
git log --oneline -n 5
```

Expected: includes separate commits for helper, org page, app page, and docs.

## Plan Self-Review

- Spec coverage: the plan covers both org and app entry points, multi-select, drag/drop, serial upload, per-file failure behavior, frontend-only orchestration, docs, and no backend/OpenAPI changes.
- Placeholder scan: the plan contains concrete file paths, code snippets, commands, expected results, and commit messages. It contains no deferred implementation markers.
- Type consistency: helper function names used in page tasks match the helper implementation in Task 1: `knowledgeFilesFromInput`, `knowledgeFilesFromDrop`, `hasKnowledgeFilesInDrag`, `filterKnowledgeUploadFiles`, and `toKnowledgeUploadItems`.
