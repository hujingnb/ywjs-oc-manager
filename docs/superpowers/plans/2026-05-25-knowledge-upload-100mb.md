# Knowledge Upload 100MB Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make organization and instance knowledge-base uploads accept files up to 100MB and show the same limit in the frontend before upload starts.

**Architecture:** Define one backend knowledge upload size constant, wire it into manager-api startup, and align nginx with the same 100MB limit. Export one frontend limit helper from the knowledge hook module so both knowledge pages display the same hint and reject oversized files before starting the existing upload progress flow.

**Tech Stack:** Go, Gin, `files.SafeRoot`, nginx, Vue 3, Pinia, TanStack Vue Query, Naive UI, Vitest, Testify.

---

## File Structure

- Modify `internal/files/safe_path.go`: add the canonical 100MB knowledge upload limit constant.
- Modify `internal/files/safe_path_test.go`: cover the exported 100MB constant.
- Create `cmd/server/main_test.go`: verify server wiring creates the knowledge safe root with the 100MB limit.
- Modify `cmd/server/main.go`: use the 100MB constant when creating the knowledge safe root.
- Modify `deploy/manage/nginx.conf`: raise `client_max_body_size` to `100M`.
- Modify `web/src/api/hooks/useKnowledge.ts`: export the frontend 100MB constant, message, and size helper.
- Create `web/src/api/hooks/useKnowledge.spec.ts`: cover frontend limit constants and boundary behavior.
- Create `web/src/pages/apps/AppKnowledgeTab.spec.ts`: cover oversized instance knowledge upload rejection.
- Create `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`: cover oversized organization knowledge upload rejection.
- Modify `web/src/pages/apps/AppKnowledgeTab.vue`: show the 100MB hint and reject oversized files before `uploadProgress.run`.
- Modify `web/src/pages/knowledge/OrgKnowledgePage.vue`: show the 100MB hint and reject oversized files before `uploadProgress.run`.

---

### Task 1: Backend Knowledge Limit Constant

**Files:**
- Modify: `internal/files/safe_path_test.go`
- Modify: `internal/files/safe_path.go`

- [ ] **Step 1: Write the failing constant test**

Replace the import block in `internal/files/safe_path_test.go` with:

```go
import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Add this test after the existing `TestNewSafeRootRejectsEmpty` test:

```go
// TestKnowledgeMaxFileSizeIs100MB 验证知识库单文件业务上限固定为 100MB。
func TestKnowledgeMaxFileSizeIs100MB(t *testing.T) {
	assert.Equal(t, int64(100*1024*1024), KnowledgeMaxFileSize)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
rtk go test ./internal/files -run TestKnowledgeMaxFileSizeIs100MB -v
```

Expected: FAIL with an undefined identifier error for `KnowledgeMaxFileSize`.

- [ ] **Step 3: Add the 100MB constant**

In `internal/files/safe_path.go`, add this constant after the error var block:

```go
// KnowledgeMaxFileSize 是知识库上传单文件业务上限；部署入口和前端提示必须保持同值。
const KnowledgeMaxFileSize int64 = 100 * 1024 * 1024
```

- [ ] **Step 4: Run the focused backend test**

Run:

```bash
rtk go test ./internal/files -run TestKnowledgeMaxFileSizeIs100MB -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
rtk git add internal/files/safe_path.go internal/files/safe_path_test.go
rtk git commit -m "feat(upload): 增加知识库100MB上限常量" -m "定义知识库上传单文件 100MB 业务上限，供后端装配和前端提示复用同一语义。"
```

---

### Task 2: Server And Nginx Limit Wiring

**Files:**
- Create: `cmd/server/main_test.go`
- Modify: `cmd/server/main.go`
- Modify: `deploy/manage/nginx.conf`

- [ ] **Step 1: Write the failing server wiring test**

Create `cmd/server/main_test.go`:

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/files"
)

// TestNewKnowledgeSafeRootUsesBusinessLimit 验证 server 装配知识库主副本时使用 100MB 业务上限。
func TestNewKnowledgeSafeRootUsesBusinessLimit(t *testing.T) {
	root, err := newKnowledgeSafeRoot(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, files.KnowledgeMaxFileSize, root.MaxFileSize)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
rtk go test ./cmd/server -run TestNewKnowledgeSafeRootUsesBusinessLimit -v
```

Expected: FAIL with an undefined identifier error for `newKnowledgeSafeRoot`.

- [ ] **Step 3: Add the server helper and wire startup through it**

In `cmd/server/main.go`, replace:

```go
	safeRoot, err := files.NewSafeRoot(cfg.App.KnowledgeRoot, 0)
```

with:

```go
	safeRoot, err := newKnowledgeSafeRoot(cfg.App.KnowledgeRoot)
```

Add this helper near the other server setup helpers in `cmd/server/main.go`:

```go
func newKnowledgeSafeRoot(root string) (*files.SafeRoot, error) {
	// 知识库上传上限需要与 nginx client_max_body_size 和前端本地校验保持一致。
	return files.NewSafeRoot(root, files.KnowledgeMaxFileSize)
}
```

- [ ] **Step 4: Raise the nginx request body limit**

In `deploy/manage/nginx.conf`, replace:

```nginx
    client_max_body_size 32M;
```

with:

```nginx
    client_max_body_size 100M;
```

- [ ] **Step 5: Run focused backend checks**

Run:

```bash
rtk go test ./cmd/server ./internal/files
rtk rg -n "client_max_body_size 100M" deploy/manage/nginx.conf
```

Expected: both Go packages pass; `rg` prints the nginx line containing `client_max_body_size 100M;`.

- [ ] **Step 6: Commit**

Run:

```bash
rtk git add cmd/server/main.go cmd/server/main_test.go deploy/manage/nginx.conf
rtk git commit -m "fix(upload): 统一知识库服务端上传上限" -m "manager-api 初始化知识库主副本时显式使用 100MB 单文件上限。\\n\\n同步提高生产 nginx 请求体限制，避免 32MB 以上文件在进入 Go 服务前被 413 拦截。"
```

---

### Task 3: Frontend Knowledge Limit Helper

**Files:**
- Create: `web/src/api/hooks/useKnowledge.spec.ts`
- Modify: `web/src/api/hooks/useKnowledge.ts`

- [ ] **Step 1: Write the failing helper tests**

Create `web/src/api/hooks/useKnowledge.spec.ts`:

```ts
import { describe, expect, it } from 'vitest'

import {
  KNOWLEDGE_UPLOAD_MAX_BYTES,
  KNOWLEDGE_UPLOAD_MAX_LABEL,
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  isKnowledgeUploadTooLarge,
} from './useKnowledge'

describe('知识库上传大小限制', () => {
  // 覆盖前端展示与本地校验共用的 100MB 限制，避免页面文案和判断条件漂移。
  it('导出 100MB 上限和统一提示文案', () => {
    expect(KNOWLEDGE_UPLOAD_MAX_BYTES).toBe(100 * 1024 * 1024)
    expect(KNOWLEDGE_UPLOAD_MAX_LABEL).toBe('100MB')
    expect(KNOWLEDGE_UPLOAD_MAX_MESSAGE).toBe('单文件最大支持 100MB')
  })

  // 覆盖边界：刚好 100MB 允许上传，超过 1 字节立即拒绝。
  it('允许 100MB 文件并拒绝超过 1 字节的文件', () => {
    expect(isKnowledgeUploadTooLarge({ size: KNOWLEDGE_UPLOAD_MAX_BYTES })).toBe(false)
    expect(isKnowledgeUploadTooLarge({ size: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })).toBe(true)
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useKnowledge.spec.ts
```

Expected: FAIL with missing exports from `useKnowledge.ts`.

- [ ] **Step 3: Export the shared helper**

In `web/src/api/hooks/useKnowledge.ts`, add this block after the cache key helpers:

```ts
// 知识库上传单文件上限与 manager-api files.KnowledgeMaxFileSize、nginx client_max_body_size 保持一致。
export const KNOWLEDGE_UPLOAD_MAX_BYTES = 100 * 1024 * 1024
export const KNOWLEDGE_UPLOAD_MAX_LABEL = '100MB'
export const KNOWLEDGE_UPLOAD_MAX_MESSAGE = `单文件最大支持 ${KNOWLEDGE_UPLOAD_MAX_LABEL}`

// isKnowledgeUploadTooLarge 在页面发起上传会话前做本地拦截，避免超限文件进入网络请求。
export function isKnowledgeUploadTooLarge(file: Pick<File, 'size'>): boolean {
  return file.size > KNOWLEDGE_UPLOAD_MAX_BYTES
}
```

- [ ] **Step 4: Run the focused frontend helper tests**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useKnowledge.spec.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
rtk git add web/src/api/hooks/useKnowledge.ts web/src/api/hooks/useKnowledge.spec.ts
rtk git commit -m "feat(upload): 增加知识库前端上传限制工具" -m "导出知识库上传 100MB 上限、提示文案和本地判断函数。\\n\\n后续组织知识库与实例知识库页面复用同一工具，避免提示文案与拦截条件不一致。"
```

---

### Task 4: Oversized Upload Page Tests

**Files:**
- Create: `web/src/pages/apps/AppKnowledgeTab.spec.ts`
- Create: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`

- [ ] **Step 1: Add the instance knowledge oversized-file test**

Create `web/src/pages/apps/AppKnowledgeTab.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES } from '@/api/hooks/useKnowledge'
import AppKnowledgeTab from './AppKnowledgeTab.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'user-1', role: 'org_member', org_id: 'org-1' } }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', () => ({
  canManageApp: () => true,
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ warning: mocks.warning }),
  }
})

vi.mock('@/api/hooks/useKnowledge', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useKnowledge')>('@/api/hooks/useKnowledge')
  return {
    ...actual,
    useAppKnowledgeQuery: () => ({
      data: ref({ path: '', entries: [] }),
      isLoading: ref(false),
      error: ref(null),
    }),
    useUploadAppKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteAppKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
  }
})

function mountTab() {
  return mount(AppKnowledgeTab, {
    props: { appId: 'app-1' },
    global: {
      provide: {
        app: ref({
          id: 'app-1',
          org_id: 'org-1',
          owner_user_id: 'user-1',
          name: '测试实例',
          status: 'running',
          api_key_status: 'active',
        }),
      },
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NDataTable: { template: '<table />' },
        NButton: { template: '<button><slot /></button>' },
      },
    },
  })
}

function oversizedFile(): File {
  const file = new File(['x'], 'huge.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'size', { value: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })
  return file
}

describe('AppKnowledgeTab', () => {
  // 覆盖实例知识库上传超限路径：前端提示 100MB 限制，并且不创建上传会话。
  it('拒绝超过 100MB 的实例知识库文件', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith('单文件最大支持 100MB')
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Add the organization knowledge oversized-file test**

Create `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`:

```ts
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES } from '@/api/hooks/useKnowledge'
import OrgKnowledgePage from './OrgKnowledgePage.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'admin-1', role: 'org_admin', org_id: 'org-1' } }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', () => ({
  canManageOrgKnowledge: () => true,
}))

vi.mock('@/composables/usePlatformOrgSelection', () => ({
  usePlatformOrgSelection: () => ({
    isPlatformAdmin: ref(false),
    selectedOrgId: ref('org-1'),
    effectiveOrgId: ref('org-1'),
    orgOptions: ref([]),
    organizationsLoading: ref(false),
  }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ warning: mocks.warning }),
  }
})

vi.mock('@/api/hooks/useKnowledge', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useKnowledge')>('@/api/hooks/useKnowledge')
  return {
    ...actual,
    useOrgKnowledgeQuery: () => ({
      data: ref({ path: '', entries: [] }),
      isLoading: ref(false),
      error: ref(null),
    }),
    useUploadOrgKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteOrgKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
    useOrgKnowledgeSyncStatusQuery: () => ({
      data: ref([]),
      isLoading: ref(false),
    }),
    useRetryOrgKnowledgeSync: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
  }
})

function mountPage() {
  return mount(OrgKnowledgePage, {
    global: {
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NSpace: { template: '<div><slot /></div>' },
        NSelect: { template: '<select />' },
        NDataTable: { template: '<table />' },
        NButton: { template: '<button><slot /></button>' },
        NTag: { template: '<span><slot /></span>' },
      },
    },
  })
}

function oversizedFile(): File {
  const file = new File(['x'], 'huge.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'size', { value: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })
  return file
}

describe('OrgKnowledgePage', () => {
  // 覆盖组织知识库上传超限路径：前端提示 100MB 限制，并且不创建上传会话。
  it('拒绝超过 100MB 的组织知识库文件', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith('单文件最大支持 100MB')
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 3: Run the page tests to verify they fail**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/apps/AppKnowledgeTab.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts
```

Expected: FAIL because both pages still call `uploadProgress.run` for oversized files and do not show the 100MB warning.

---

### Task 5: Frontend Page Hint And Local Rejection

**Files:**
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`

- [ ] **Step 1: Update instance knowledge imports**

In `web/src/pages/apps/AppKnowledgeTab.vue`, replace the knowledge import block with:

```ts
import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  isKnowledgeUploadTooLarge,
  useAppKnowledgeQuery,
  useDeleteAppKnowledge,
  useUploadAppKnowledge,
} from '@/api/hooks/useKnowledge'
```

Keep the existing type-only import:

```ts
import type { KnowledgeEntry } from '@/api/hooks/useKnowledge'
```

- [ ] **Step 2: Add the instance knowledge header hint**

In `web/src/pages/apps/AppKnowledgeTab.vue`, replace the `#header-extra` template block with:

```vue
    <template #header-extra>
      <div v-if="canManage" class="upload-actions">
        <span class="upload-limit">{{ KNOWLEDGE_UPLOAD_MAX_MESSAGE }}</span>
        <label class="secondary-button file-picker" :class="{ disabled: !knowledgeContext || uploading }">
          上传文件
          <input type="file" :disabled="!knowledgeContext || uploading" @change="onUploadFile" />
        </label>
      </div>
    </template>
```

- [ ] **Step 3: Add the instance knowledge local size guard**

In `onUploadFile`, add this block immediately after `if (!file) return`:

```ts
  // 前端先拦截超过知识库业务上限的文件，避免创建进度会话后再被网关或后端拒绝。
  if (isKnowledgeUploadTooLarge(file)) {
    message.warning(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    return
  }
```

- [ ] **Step 4: Add instance knowledge styles**

In the scoped style of `web/src/pages/apps/AppKnowledgeTab.vue`, add:

```css
.upload-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

.upload-limit {
  color: rgba(255, 255, 255, 0.64);
  font-size: 12px;
}
```

- [ ] **Step 5: Update organization knowledge imports**

In `web/src/pages/knowledge/OrgKnowledgePage.vue`, replace the knowledge import block with:

```ts
import {
  KNOWLEDGE_UPLOAD_MAX_MESSAGE,
  isKnowledgeUploadTooLarge,
  useDeleteOrgKnowledge,
  useOrgKnowledgeQuery,
  useOrgKnowledgeSyncStatusQuery,
  useRetryOrgKnowledgeSync,
  useUploadOrgKnowledge,
  type KnowledgeEntry,
  type OrgSyncStatusEntry,
} from '@/api/hooks/useKnowledge'
```

- [ ] **Step 6: Add the organization knowledge header hint**

In `web/src/pages/knowledge/OrgKnowledgePage.vue`, replace the `#header-extra` template block with:

```vue
      <template #header-extra>
        <div v-if="canManage" class="upload-actions">
          <span class="upload-limit">{{ KNOWLEDGE_UPLOAD_MAX_MESSAGE }}</span>
          <label class="primary-button">
            <input class="hidden-input" type="file" :disabled="!canManage" @change="onUpload" />
            上传文件
          </label>
        </div>
      </template>
```

- [ ] **Step 7: Add the organization knowledge local size guard**

In `onUpload`, add this block immediately after `if (!file) return`:

```ts
  // 前端先拦截超过知识库业务上限的文件，避免创建进度会话后再被网关或后端拒绝。
  if (isKnowledgeUploadTooLarge(file)) {
    message.warning(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    return
  }
```

- [ ] **Step 8: Add organization knowledge styles**

In the scoped style of `web/src/pages/knowledge/OrgKnowledgePage.vue`, add:

```css
.upload-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

.upload-limit {
  color: rgba(255, 255, 255, 0.64);
  font-size: 12px;
}
```

- [ ] **Step 9: Run focused frontend tests**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useKnowledge.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts
```

Expected: PASS.

- [ ] **Step 10: Commit**

Run:

```bash
rtk git add web/src/api/hooks/useKnowledge.ts web/src/api/hooks/useKnowledge.spec.ts web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppKnowledgeTab.spec.ts web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/knowledge/OrgKnowledgePage.spec.ts
rtk git commit -m "fix(upload): 前端拦截超限知识库文件" -m "组织知识库和实例知识库统一展示 100MB 单文件限制。\\n\\n选择超过上限的文件时直接提示并中止上传，避免进入全局上传进度会话后再由网关或后端返回 413。"
```

---

### Task 6: Final Verification

**Files:**
- Read-only verification across changed backend, frontend, and deployment files.

- [ ] **Step 1: Run backend tests**

Run:

```bash
rtk go test ./internal/files ./cmd/server ./internal/service
```

Expected: PASS for all listed packages.

- [ ] **Step 2: Run frontend tests and typecheck**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useKnowledge.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts
rtk npm --prefix web run typecheck
```

Expected: Vitest PASS and `vue-tsc --noEmit` completes without errors.

- [ ] **Step 3: Confirm generated API files are untouched**

Run:

```bash
rtk git diff -- openapi/openapi.yaml web/src/api/generated.ts
```

Expected: no diff output, because this change does not alter handler signatures, routes, request bodies, or response schemas.

- [ ] **Step 4: Start the local web app for browser verification**

Run this in a long-running terminal session:

```bash
rtk npm --prefix web run dev -- --host 127.0.0.1 --port 5173
```

Expected: Vite prints a local URL on `http://127.0.0.1:5173/`.

- [ ] **Step 5: Verify in a real browser**

Using the running app and a backend environment with the documented local accounts:

1. Log in as manager platform or organization user.
2. Open an instance detail page and switch to the instance knowledge tab.
3. Confirm the upload area shows `单文件最大支持 100MB`.
4. Select a file larger than 100MB and confirm the warning appears without any upload progress session.
5. Select a small file and confirm the upload progress modal appears and the file list refreshes after upload.
6. Open the organization knowledge page and repeat steps 3 through 5.

Expected: both pages show the hint, reject oversized files locally, and still upload small files through the existing progress modal.

---

## Self-Review

- Spec coverage: nginx 100MB, backend 100MB, frontend hint, frontend local rejection, organization and instance pages, tests, and browser verification are each covered by tasks.
- Placeholder scan: the plan avoids deferred placeholders and gives exact files, code blocks, commands, and expected outcomes.
- Type consistency: frontend helper names are introduced in Task 3 and reused unchanged in Task 5; backend constant name is introduced in Task 1 and reused unchanged in Task 2.
