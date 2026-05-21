# 助手版本 Phase 1b 实施计划：版本管理前端页

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为已完成的助手版本后端 API 实现平台管理员的「助手版本管理」前端页——列表、新建/编辑表单、skill tar 上传、删除。

**Architecture:** 标准的 oc-manager 前端分层：`src/api/hooks/useAssistantVersions.ts` 用 `@tanstack/vue-query` 封装 API，`src/pages/platform/AssistantVersionsPage.vue` 用 naive-ui 组件渲染列表 + 内联表单卡片，路由与导航接入 `platform_admin` 专属入口。skill tar 走原生 `fetch` + `FormData`（`apiRequest` 只支持 JSON）。

**Tech Stack:** Vue 3 `<script setup>`、naive-ui、@tanstack/vue-query、vue-router、vitest + @vue/test-utils、lucide-vue-next 图标。

**关联文档：** 设计 spec `docs/superpowers/specs/2026-05-21-assistant-version-design.md` §8.1；Phase 1 后端计划 `docs/superpowers/plans/2026-05-21-assistant-version-phase1.md`（已交付，提供 `/api/v1/assistant-versions*` 与 `/api/v1/runtime-images` 接口）。

**范围：** 仅 Phase 1b——版本管理页。组织 allowlist 选版本、实例绑定版本属 Phase 3，不在本计划。

**前置事实（Phase 1 已交付的后端契约）：**
- `GET /api/v1/assistant-versions` → `{ versions: AssistantVersionResult[] }`
- `GET /api/v1/assistant-versions/:id` → `{ version: AssistantVersionResult }`
- `POST /api/v1/assistant-versions` → 201 `{ version }`，请求体 `CreateAssistantVersionRequest`
- `PUT /api/v1/assistant-versions/:id` → 200 `{ version }`，请求体 `UpdateAssistantVersionRequest`
- `DELETE /api/v1/assistant-versions/:id` → 204
- `POST /api/v1/assistant-versions/:id/skills` → 200 `{ version }`，multipart 表单字段名 `file`
- `DELETE /api/v1/assistant-versions/:id/skills/:skill` → 200 `{ version }`
- `GET /api/v1/runtime-images` → `{ images: RuntimeImageOption[] }`
- `AssistantVersionResult` = `{ id, name, description, system_prompt, image_id, main_model, routing: map[string]string, skills: [{name,file_path,file_size,file_sha256}], revision }`
- 创建/更新请求的 `routing` 是 8 字段对象（`vision/compression/web_extract/session_search/title_generation/approval/skills_hub/mcp`，空字符串表示走主模型）。
- 模型列表接口 `GET /api/v1/models` 已有现成 hook `useModelsQuery`（在 `src/api/hooks/useOrganizations.ts` 中导出），直接复用。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `web/src/api/hooks/useAssistantVersions.ts` | 版本 API hooks + 路由槽位常量 | 新建 |
| `web/src/api/hooks/useAssistantVersions.spec.ts` | hook 单测 | 新建 |
| `web/src/pages/platform/AssistantVersionsPage.vue` | 版本管理页（列表 + 表单 + skill） | 新建 |
| `web/src/pages/platform/AssistantVersionsPage.spec.ts` | 页面组件测试 | 新建 |
| `web/src/app/router.ts` | 新增 `/assistant-versions` 路由 | 修改 |
| `web/src/layouts/DashboardLayout.vue` | 新增平台管理员导航入口 | 修改 |

构建/测试命令（仓库根目录执行）：`make web-typecheck`、`make web-test`。

---

## Task 1：版本 API hooks

**Files:**
- Create: `web/src/api/hooks/useAssistantVersions.ts`
- Create: `web/src/api/hooks/useAssistantVersions.spec.ts`

- [ ] **Step 1：写 hook 实现**

创建 `web/src/api/hooks/useAssistantVersions.ts`：

```ts
// 助手版本 API hooks：平台管理员维护版本目录（列表、详情、增删改）与 skill tar 上传。
// 写操作统一失效版本列表缓存；skill 上传走原生 fetch（apiRequest 只支持 JSON body）。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { apiRequest, getCsrfToken, getStoredAccessToken } from '@/api/client'

// AUXILIARY_SLOTS 是智能路由的 8 个 auxiliary 槽位，key 与后端约定一致，顺序固定用于表单渲染。
export const AUXILIARY_SLOTS = [
  { key: 'vision', label: '图像识别' },
  { key: 'compression', label: '上下文压缩' },
  { key: 'web_extract', label: '网页提取' },
  { key: 'session_search', label: '会话搜索' },
  { key: 'title_generation', label: '标题生成' },
  { key: 'approval', label: '智能审批' },
  { key: 'skills_hub', label: '技能检索' },
  { key: 'mcp', label: 'MCP 路由' },
] as const

// AssistantVersionRoutingPayload 是创建/更新请求里的 8 槽位路由对象；空字符串表示该场景走主模型。
export interface AssistantVersionRoutingPayload {
  vision: string
  compression: string
  web_extract: string
  session_search: string
  title_generation: string
  approval: string
  skills_hub: string
  mcp: string
}

// emptyRouting 返回全空的路由对象，作为表单初始值与重置值。
export function emptyRouting(): AssistantVersionRoutingPayload {
  return {
    vision: '', compression: '', web_extract: '', session_search: '',
    title_generation: '', approval: '', skills_hub: '', mcp: '',
  }
}

// AssistantVersionSkillDTO 是版本下单个 skill 的元信息。
export interface AssistantVersionSkillDTO {
  name: string
  file_path: string
  file_size: number
  file_sha256: string
}

// AssistantVersionDTO 是助手版本的前端视图。
export interface AssistantVersionDTO {
  id: string
  name: string
  description: string
  system_prompt: string
  image_id: string
  main_model: string
  // routing 是后端返回的紧凑路由 map，只含非空槽位。
  routing: Record<string, string>
  skills: AssistantVersionSkillDTO[]
  revision: number
}

// AssistantVersionFormPayload 是创建/更新版本的提交体。
export interface AssistantVersionFormPayload {
  name: string
  description: string
  system_prompt: string
  image_id: string
  main_model: string
  routing: AssistantVersionRoutingPayload
}

// RuntimeImageDTO 是配置文件暴露的可选镜像（仅 id + label）。
export interface RuntimeImageDTO {
  id: string
  label: string
}

const VERSION_LIST_KEY = ['assistant-versions'] as const

// useAssistantVersionsQuery 获取全部助手版本；仅平台管理员可读。
export function useAssistantVersionsQuery(enabled?: () => boolean) {
  return useQuery<AssistantVersionDTO[]>({
    queryKey: VERSION_LIST_KEY,
    enabled,
    queryFn: async () => {
      const res = await apiRequest<{ versions: AssistantVersionDTO[] }>('/api/v1/assistant-versions')
      return res.versions
    },
  })
}

// useRuntimeImagesQuery 获取配置文件中的可选镜像；enabled 让调用方只在表单打开时请求。
export function useRuntimeImagesQuery(enabled?: () => boolean) {
  return useQuery<RuntimeImageDTO[]>({
    queryKey: ['runtime-images'],
    enabled,
    queryFn: async () => {
      const res = await apiRequest<{ images: RuntimeImageDTO[] }>('/api/v1/runtime-images')
      return res.images
    },
  })
}

// useCreateAssistantVersion 创建版本，成功后失效列表缓存。
export function useCreateAssistantVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: AssistantVersionFormPayload) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>('/api/v1/assistant-versions', {
        method: 'POST', body: payload,
      })
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useUpdateAssistantVersion 编辑版本。
export function useUpdateAssistantVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, payload }: { id: string; payload: AssistantVersionFormPayload }) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>(`/api/v1/assistant-versions/${id}`, {
        method: 'PUT', body: payload,
      })
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useDeleteAssistantVersion 删除版本。
export function useDeleteAssistantVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await apiRequest<void>(`/api/v1/assistant-versions/${id}`, { method: 'DELETE' })
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useUploadAssistantVersionSkill 上传一个 skill tar（multipart 表单字段名 file）。
// 走原生 fetch：apiRequest 只支持 JSON body，无法发 multipart。
export function useUploadAssistantVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, file }: { id: string; file: File }) => {
      const headers: Record<string, string> = {}
      const token = getStoredAccessToken()
      if (token) headers.Authorization = `Bearer ${token}`
      const csrf = getCsrfToken()
      if (csrf) headers['X-CSRF-Token'] = csrf
      const body = new FormData()
      body.append('file', file)
      const response = await fetch(`/api/v1/assistant-versions/${id}/skills`, {
        method: 'POST', headers, body,
      })
      if (!response.ok) {
        const text = await response.text().catch(() => '')
        throw new Error(text || '上传失败')
      }
      const json = (await response.json()) as { version: AssistantVersionDTO }
      return json.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useDeleteAssistantVersionSkill 删除版本下的一个 skill。
export function useDeleteAssistantVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, skillName }: { id: string; skillName: string }) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>(
        `/api/v1/assistant-versions/${id}/skills/${encodeURIComponent(skillName)}`,
        { method: 'DELETE' },
      )
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}
```

- [ ] **Step 2：写 hook 测试**

创建 `web/src/api/hooks/useAssistantVersions.spec.ts`。先看同目录已有的 hook 测试（如 `useCron.spec.ts`、`useChannel.test.ts`）确认 vitest 风格，再写。本测试只覆盖纯函数与槽位常量（mutation/query 的网络行为由页面测试覆盖）：

```ts
import { describe, expect, it } from 'vitest'

import { AUXILIARY_SLOTS, emptyRouting } from './useAssistantVersions'

describe('useAssistantVersions 辅助导出', () => {
  // 验证 8 个 auxiliary 槽位齐全且 key 与后端约定一致。
  it('AUXILIARY_SLOTS 含全部 8 个槽位', () => {
    const keys = AUXILIARY_SLOTS.map(s => s.key)
    expect(keys).toEqual([
      'vision', 'compression', 'web_extract', 'session_search',
      'title_generation', 'approval', 'skills_hub', 'mcp',
    ])
  })

  // 验证 emptyRouting 返回 8 个槽位且全为空字符串。
  it('emptyRouting 返回全空的 8 槽位对象', () => {
    const r = emptyRouting()
    expect(Object.keys(r)).toHaveLength(8)
    expect(Object.values(r).every(v => v === '')).toBe(true)
  })
})
```

- [ ] **Step 3：运行类型检查与测试**

Run: `make web-typecheck && make web-test`
Expected: 通过（含新 spec 的 2 条用例）。

- [ ] **Step 4：提交**

```bash
git add web/src/api/hooks/useAssistantVersions.ts web/src/api/hooks/useAssistantVersions.spec.ts
git commit -m "feat(assistant-version): 新增版本管理前端 API hooks"
```

提交信息：Conventional Commits、中文摘要，正文空一行后以 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` 结尾。

---

## Task 2：版本管理页——列表与新建/编辑表单

**Files:**
- Create: `web/src/pages/platform/AssistantVersionsPage.vue`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/layouts/DashboardLayout.vue`

本任务建出可用的版本管理页（列表 + 新建/编辑表单 + 删除），skill 管理在 Task 3 追加。

- [ ] **Step 1：创建页面组件**

创建 `web/src/pages/platform/AssistantVersionsPage.vue`：

```vue
<template>
  <div style="display: grid; gap: 18px">
    <!-- 版本列表 -->
    <DataTableList
      title="助手版本"
      eyebrow="Platform"
      :columns="columns"
      :data="versions ?? []"
      :loading="isLoading"
      :error-message="error?.message"
      :row-key="(row: AssistantVersionDTO) => row.id"
    >
      <template #toolbar>
        <n-button type="primary" @click="openCreate">
          <template #icon><Plus :size="16" /></template>
          新增版本
        </n-button>
      </template>
    </DataTableList>
    <p v-if="actionFeedback" class="state-text" :class="{ danger: actionFeedbackError }">{{ actionFeedback }}</p>

    <!-- 新建 / 编辑表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">{{ editingId ? 'Edit' : 'New' }}</p>
            <h2 style="margin: 0">{{ editingId ? '编辑助手版本' : '新建助手版本' }}</h2>
          </div>
          <n-button quaternary circle @click="closeForm">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="版本名称（唯一）" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="使用镜像 *">
              <n-select
                v-model:value="form.image_id"
                :loading="imagesQuery.isLoading.value"
                :disabled="imagesQuery.isError.value"
                :options="imageOptions"
                placeholder="选择 Hermes 镜像"
              />
              <p v-if="imagesQuery.isError.value" class="state-text danger">镜像列表获取失败，请重试</p>
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="描述">
              <n-input v-model:value="form.description" type="textarea" :rows="2" placeholder="版本用途说明" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="内置提示词 *">
              <n-input
                v-model:value="form.system_prompt"
                type="textarea"
                :rows="4"
                placeholder="可填写助手人设、行为规则等；将注入容器 SOUL.md 的版本层"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="主模型 *">
              <n-select
                v-model:value="form.main_model"
                filterable
                :loading="modelsQuery.isLoading.value"
                :disabled="modelsQuery.isError.value"
                :options="modelOptions"
                placeholder="选择主对话模型"
              />
              <p v-if="modelsQuery.isError.value" class="state-text danger">模型列表获取失败，请重试</p>
            </n-form-item>
          </n-grid-item>
          <!-- 智能路由：8 个 auxiliary 槽位，留空表示走主模型 -->
          <n-grid-item :span="2">
            <p class="eyebrow" style="margin: 4px 0">智能路由（留空走主模型）</p>
          </n-grid-item>
          <n-grid-item v-for="slot in AUXILIARY_SLOTS" :key="slot.key">
            <n-form-item :label="slot.label">
              <n-select
                v-model:value="form.routing[slot.key]"
                filterable
                clearable
                :options="modelOptions"
                placeholder="默认走主模型"
              />
            </n-form-item>
          </n-grid-item>

          <!-- SKILL_SECTION_ANCHOR：Task 3 在此处插入 skill 管理区 -->

          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="closeForm">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="submitting" :disabled="!canSubmit">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, reactive, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import { NButton, NCard, NForm, NFormItem, NGrid, NGridItem, NInput, NSelect, NSpace } from 'naive-ui'

import DataTableList from '@/components/DataTableList.vue'
import { actionColumn } from '@/components/columns'
import { useModelsQuery } from '@/api/hooks/useOrganizations'
import {
  AUXILIARY_SLOTS,
  emptyRouting,
  useAssistantVersionsQuery,
  useCreateAssistantVersion,
  useDeleteAssistantVersion,
  useRuntimeImagesQuery,
  useUpdateAssistantVersion,
  type AssistantVersionDTO,
  type AssistantVersionFormPayload,
} from '@/api/hooks/useAssistantVersions'

// AssistantVersionsPage 是平台管理员的助手版本目录管理页：列表 + 新建/编辑 + 删除。
const { data: versions, isLoading, error } = useAssistantVersionsQuery()
const createMutation = useCreateAssistantVersion()
const updateMutation = useUpdateAssistantVersion()
const deleteMutation = useDeleteAssistantVersion()

// 表单状态：editingId 为 null 时是新建，否则是编辑该 id。
const formVisible = ref(false)
const editingId = ref<string | null>(null)
const submitting = ref(false)
const submitError = ref<string | null>(null)
const actionFeedback = ref('')
const actionFeedbackError = ref(false)

const form = reactive<AssistantVersionFormPayload>({
  name: '', description: '', system_prompt: '', image_id: '', main_model: '',
  routing: emptyRouting(),
})

// 镜像与模型列表仅在表单打开时请求。
const imagesQuery = useRuntimeImagesQuery(() => formVisible.value)
const modelsQuery = useModelsQuery(() => formVisible.value)
const imageOptions = computed(() => (imagesQuery.data.value ?? []).map(img => ({ label: img.label, value: img.id })))
const modelOptions = computed(() => (modelsQuery.data.value ?? []).map(m => ({ label: m.name, value: m.id })))

// canSubmit 在必填项齐备且依赖列表未出错时才允许提交。
const canSubmit = computed(() =>
  !submitting.value
  && !imagesQuery.isError.value
  && !modelsQuery.isError.value
  && Boolean(form.name.trim())
  && Boolean(form.system_prompt.trim())
  && Boolean(form.image_id)
  && Boolean(form.main_model),
)

// resetForm 把表单恢复为空白新建态。
function resetForm() {
  form.name = ''
  form.description = ''
  form.system_prompt = ''
  form.image_id = ''
  form.main_model = ''
  form.routing = emptyRouting()
}

// openCreate 打开空白新建表单。
function openCreate() {
  resetForm()
  editingId.value = null
  submitError.value = null
  formVisible.value = true
}

// openEdit 用已有版本数据填充表单进入编辑态。
// routing 后端只返回非空槽位，用 emptyRouting 兜底补齐 8 个 key。
function openEdit(version: AssistantVersionDTO) {
  form.name = version.name
  form.description = version.description
  form.system_prompt = version.system_prompt
  form.image_id = version.image_id
  form.main_model = version.main_model
  form.routing = { ...emptyRouting(), ...version.routing }
  editingId.value = version.id
  submitError.value = null
  formVisible.value = true
}

// closeForm 关闭表单，不清空（下次 openCreate/openEdit 会重置）。
function closeForm() {
  formVisible.value = false
}

// buildPayload 把表单组装成创建/更新提交体。
function buildPayload(): AssistantVersionFormPayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    system_prompt: form.system_prompt,
    image_id: form.image_id,
    main_model: form.main_model,
    routing: { ...form.routing },
  }
}

// submit 根据 editingId 决定走创建还是更新。
async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  submitError.value = null
  try {
    if (editingId.value) {
      await updateMutation.mutateAsync({ id: editingId.value, payload: buildPayload() })
    } else {
      await createMutation.mutateAsync(buildPayload())
    }
    formVisible.value = false
  } catch (err) {
    submitError.value = err instanceof Error ? err.message : '保存失败'
  } finally {
    submitting.value = false
  }
}

// onDelete 删除版本；后端在版本被引用时返回 409，错误文案直接展示给用户。
async function onDelete(version: AssistantVersionDTO) {
  actionFeedback.value = ''
  actionFeedbackError.value = false
  try {
    await deleteMutation.mutateAsync(version.id)
    actionFeedback.value = `已删除版本 ${version.name}`
  } catch (err) {
    actionFeedbackError.value = true
    actionFeedback.value = err instanceof Error ? err.message : '删除失败'
  }
}

// columns 展示版本基础信息、修订号、skill 数与操作。
const columns = computed(() => [
  {
    title: '名称',
    key: 'name',
    render: (row: AssistantVersionDTO) => [
      h('strong', row.name),
      row.description ? h('small', { class: 'data-table-subtitle' }, row.description) : null,
    ],
  },
  { title: '镜像', key: 'image_id', render: (row: AssistantVersionDTO) => row.image_id || '—' },
  { title: '主模型', key: 'main_model', render: (row: AssistantVersionDTO) => row.main_model || '—' },
  { title: '修订号', key: 'revision', render: (row: AssistantVersionDTO) => `r${row.revision}` },
  { title: 'Skill 数', key: 'skills', render: (row: AssistantVersionDTO) => String(row.skills?.length ?? 0) },
  actionColumn<AssistantVersionDTO>([
    { label: '编辑', type: 'primary', onClick: openEdit },
    { label: '删除', onClick: (r: AssistantVersionDTO) => { void onDelete(r) } },
  ]),
])
</script>
```

> 说明：`<!-- SKILL_SECTION_ANCHOR -->` 注释是 Task 3 的插入锚点，本任务保留原样。

- [ ] **Step 2：注册路由**

在 `web/src/app/router.ts`：
1. 在文件顶部 import 区，与其它 `@/pages/platform/*` import 并列处加一行：
   ```ts
   import AssistantVersionsPage from '@/pages/platform/AssistantVersionsPage.vue'
   ```
2. 在 `routes` 的 dashboard 子路由数组里，紧挨 `organizations` 路由之后加一条（`PLATFORM_ONLY` 常量已存在）：
   ```ts
   { path: 'assistant-versions', component: AssistantVersionsPage, meta: { allowedRoles: PLATFORM_ONLY } },
   ```

- [ ] **Step 3：接入导航**

在 `web/src/layouts/DashboardLayout.vue`：
1. `activeKey` 的 `prefixes` 数组里加入 `'/assistant-versions'`。
2. `menuOptions` 里 `isPlatformAdmin` 分支内、`organizations` 菜单项之后加一项。先确认文件顶部已 import 的 lucide 图标，复用一个语义相近的图标（如 `Boxes` 或 `Package`；若未 import，则在 import 区补一个，如 `Boxes`）：
   ```ts
   items.push({ key: '/assistant-versions', label: '助手版本', icon: () => h(Boxes, { size: 18 }) })
   ```
   把 `Boxes` 加进 `lucide-vue-next` 的 import 列表。

- [ ] **Step 4：类型检查与构建**

Run: `make web-typecheck`
Expected: 通过，无类型错误。
Run: `make web-test`
Expected: 既有前端测试全部通过（本任务尚未加页面测试）。

- [ ] **Step 5：提交**

```bash
git add web/src/pages/platform/AssistantVersionsPage.vue web/src/app/router.ts web/src/layouts/DashboardLayout.vue
git commit -m "feat(assistant-version): 新增助手版本管理页与导航入口"
```

提交信息规则同 Task 1。

---

## Task 3：版本管理页——skill 上传与删除

**Files:**
- Modify: `web/src/pages/platform/AssistantVersionsPage.vue`

在编辑态的表单里加 skill 管理区：列出当前 skill、上传 tar、删除单个 skill。skill 操作是独立的即时 API 调用（不随表单「保存」一起提交）。

- [ ] **Step 1：在模板中插入 skill 区**

在 `AssistantVersionsPage.vue` 模板里，把 `<!-- SKILL_SECTION_ANCHOR：Task 3 在此处插入 skill 管理区 -->` 这一行替换为：

```vue
          <!-- skill 管理：仅编辑态显示，操作即时生效 -->
          <n-grid-item v-if="editingId" :span="2">
            <n-form-item label="Skill 列表">
              <div style="display: grid; gap: 8px; width: 100%">
                <div v-if="editingSkills.length === 0" class="state-text">暂无 skill</div>
                <div
                  v-for="skill in editingSkills"
                  :key="skill.name"
                  style="display: flex; align-items: center; justify-content: space-between; gap: 12px"
                >
                  <span>{{ skill.name }} <small class="data-table-subtitle">{{ formatBytes(skill.file_size) }}</small></span>
                  <n-button size="small" tertiary @click="onDeleteSkill(skill.name)">删除</n-button>
                </div>
                <div>
                  <input
                    ref="skillFileInput"
                    type="file"
                    accept=".tar"
                    style="display: none"
                    @change="onSkillFileChange"
                  />
                  <n-button size="small" :loading="skillUploading" @click="triggerSkillUpload">
                    上传 skill tar
                  </n-button>
                </div>
                <p v-if="skillFeedback" class="state-text" :class="{ danger: skillFeedbackError }">{{ skillFeedback }}</p>
              </div>
            </n-form-item>
          </n-grid-item>
```

- [ ] **Step 2：在 `<script setup>` 中加 skill 逻辑**

在 import 区，把 `useAssistantVersions` 的 import 补上两个 hook 与 skill 类型：

```ts
  useUploadAssistantVersionSkill,
  useDeleteAssistantVersionSkill,
  type AssistantVersionSkillDTO,
```

（合入已有的 `import { ... } from '@/api/hooks/useAssistantVersions'` 列表。）

在 `<script setup>` 内、`openEdit` 定义附近，加入 skill 状态与逻辑：

```ts
// skill 管理状态：editingSkills 是当前编辑版本的 skill 列表，随上传/删除即时刷新。
const uploadSkillMutation = useUploadAssistantVersionSkill()
const deleteSkillMutation = useDeleteAssistantVersionSkill()
const editingSkills = ref<AssistantVersionSkillDTO[]>([])
const skillFileInput = ref<HTMLInputElement | null>(null)
const skillUploading = ref(false)
const skillFeedback = ref('')
const skillFeedbackError = ref(false)

// formatBytes 把字节数格式化为人类可读大小。
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

// triggerSkillUpload 触发隐藏的文件选择框。
function triggerSkillUpload() {
  skillFileInput.value?.click()
}

// onSkillFileChange 在选中文件后立即上传到当前编辑的版本。
async function onSkillFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许重复选择同名文件
  if (!file || !editingId.value) return
  skillUploading.value = true
  skillFeedback.value = ''
  skillFeedbackError.value = false
  try {
    const updated = await uploadSkillMutation.mutateAsync({ id: editingId.value, file })
    editingSkills.value = updated.skills
    skillFeedback.value = `已上传 skill ${file.name}`
  } catch (err) {
    skillFeedbackError.value = true
    skillFeedback.value = err instanceof Error ? err.message : '上传失败'
  } finally {
    skillUploading.value = false
  }
}

// onDeleteSkill 从当前编辑的版本删除一个 skill。
async function onDeleteSkill(skillName: string) {
  if (!editingId.value) return
  skillFeedback.value = ''
  skillFeedbackError.value = false
  try {
    const updated = await deleteSkillMutation.mutateAsync({ id: editingId.value, skillName })
    editingSkills.value = updated.skills
    skillFeedback.value = `已删除 skill ${skillName}`
  } catch (err) {
    skillFeedbackError.value = true
    skillFeedback.value = err instanceof Error ? err.message : '删除失败'
  }
}
```

- [ ] **Step 3：在 `openCreate` / `openEdit` 中同步 `editingSkills`**

在 `openCreate` 函数体末尾（`formVisible.value = true` 之前或之后）加：
```ts
  editingSkills.value = []
  skillFeedback.value = ''
```
在 `openEdit` 函数体末尾加：
```ts
  editingSkills.value = [...version.skills]
  skillFeedback.value = ''
```

- [ ] **Step 4：类型检查与测试**

Run: `make web-typecheck && make web-test`
Expected: 全部通过。

- [ ] **Step 5：提交**

```bash
git add web/src/pages/platform/AssistantVersionsPage.vue
git commit -m "feat(assistant-version): 版本管理页支持 skill 上传与删除"
```

提交信息规则同 Task 1。

---

## Task 4：版本管理页组件测试

**Files:**
- Create: `web/src/pages/platform/AssistantVersionsPage.spec.ts`

参照 `web/src/pages/platform/OrganizationsPage.spec.ts` 的 mock 风格（mock hooks、stub naive-ui 组件、stub `DataTableList`）。

- [ ] **Step 1：写页面测试**

创建 `web/src/pages/platform/AssistantVersionsPage.spec.ts`：

```ts
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import AssistantVersionsPage from './AssistantVersionsPage.vue'
import type { AssistantVersionDTO } from '@/api/hooks/useAssistantVersions'

const createVersion = vi.hoisted(() => vi.fn())
const updateVersion = vi.hoisted(() => vi.fn())
const deleteVersion = vi.hoisted(() => vi.fn())

// 一个用于列表与编辑回填的样例版本。
const sampleVersion: AssistantVersionDTO = {
  id: 'ver-1', name: '标准版', description: '默认版本', system_prompt: '你是助手',
  image_id: 'v2026.5.16', main_model: 'qwen', routing: { vision: 'gpt' },
  skills: [{ name: 'weather', file_path: 'p', file_size: 2048, file_sha256: 'ab' }], revision: 2,
}

vi.mock('@/api/hooks/useAssistantVersions', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useAssistantVersions')>(
    '@/api/hooks/useAssistantVersions',
  )
  return {
    ...actual,
    useAssistantVersionsQuery: () => ({
      data: ref([sampleVersion]), isLoading: ref(false), error: ref(null),
    }),
    useRuntimeImagesQuery: () => ({
      data: ref([{ id: 'v2026.5.16', label: 'Hermes v2026.5.16' }]),
      isLoading: ref(false), isError: ref(false),
    }),
    useCreateAssistantVersion: () => ({ mutateAsync: createVersion }),
    useUpdateAssistantVersion: () => ({ mutateAsync: updateVersion }),
    useDeleteAssistantVersion: () => ({ mutateAsync: deleteVersion }),
    useUploadAssistantVersionSkill: () => ({ mutateAsync: vi.fn() }),
    useDeleteAssistantVersionSkill: () => ({ mutateAsync: vi.fn() }),
  }
})

vi.mock('@/api/hooks/useOrganizations', () => ({
  useModelsQuery: () => ({
    data: ref([{ id: 'qwen', name: 'qwen' }]), isLoading: ref(false), isError: ref(false),
  }),
}))

// stub 出最小可交互的 naive-ui 组件集合，与 OrganizationsPage.spec.ts 保持一致风格。
function mountPage() {
  return mount(AssistantVersionsPage, {
    global: {
      stubs: {
        NButton: defineComponent({
          props: ['loading', 'disabled'],
          emits: ['click'],
          setup(p, { slots, emit }) {
            return () => h('button', { disabled: p.disabled, onClick: () => emit('click') }, slots.default?.())
          },
        }),
        NCard: defineComponent({ setup(_, { slots }) { return () => h('section', [slots.header?.(), slots.default?.()]) } }),
        NForm: defineComponent({ props: ['model'], setup(_, { slots }) { return () => h('form', slots.default?.()) } }),
        NFormItem: defineComponent({
          props: ['label'],
          setup(p, { slots }) { return () => h('label', [h('span', p.label), slots.default?.()]) },
        }),
        NGrid: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        NGridItem: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        NInput: defineComponent({
          props: ['value'],
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('input', {
              value: p.value,
              onInput: (e: Event) => emit('update:value', (e.target as HTMLInputElement).value),
            })
          },
        }),
        NSelect: defineComponent({
          props: { value: [String], options: Array, disabled: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled, value: p.value,
              onChange: (e: Event) => emit('update:value', (e.target as HTMLSelectElement).value),
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        NSpace: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        DataTableList: defineComponent({
          props: {
            columns: { type: Array as PropType<DataTableColumn<AssistantVersionDTO>[]>, required: true },
            data: { type: Array as PropType<AssistantVersionDTO[]>, required: true },
          },
          setup(p, { slots }) {
            return () => h('section', [
              slots.toolbar?.(),
              h('table', [h('tbody', p.data.map(row =>
                h('tr', { key: row.id }, p.columns.map((col) => {
                  if ('render' in col && col.render) return h('td', [col.render(row, 0)])
                  return h('td', '')
                }))))]),
            ])
          },
        }),
      },
    },
  })
}

describe('AssistantVersionsPage', () => {
  // 列表展示已有版本的名称与修订号。
  it('展示版本列表', () => {
    const wrapper = mountPage()
    expect(wrapper.text()).toContain('标准版')
    expect(wrapper.text()).toContain('r2')
  })

  // 点击新增版本打开空白表单。
  it('点击新增版本打开表单', async () => {
    const wrapper = mountPage()
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('新增版本'))
    expect(addBtn).toBeTruthy()
    await addBtn!.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('新建助手版本')
  })

  // 填写必填项后提交调用创建接口。
  it('创建版本时提交表单数据', async () => {
    createVersion.mockResolvedValue(sampleVersion)
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text().includes('新增版本'))!.trigger('click')
    await nextTick()
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('新版本')          // 名称
    await inputs[1].setValue('一些描述')         // 描述 textarea
    await inputs[2].setValue('你是助手')         // 内置提示词 textarea
    const selects = wrapper.findAll('select')
    await selects[0].setValue('v2026.5.16')      // 镜像
    await selects[1].setValue('qwen')            // 主模型
    await wrapper.find('form').trigger('submit')
    expect(createVersion).toHaveBeenCalledWith(expect.objectContaining({
      name: '新版本', image_id: 'v2026.5.16', main_model: 'qwen',
    }))
  })

  // 点击编辑用已有版本回填表单并走更新接口。
  it('编辑版本时回填并调用更新接口', async () => {
    updateVersion.mockResolvedValue(sampleVersion)
    const wrapper = mountPage()
    const editBtn = wrapper.findAll('button').find(b => b.text() === '编辑')
    expect(editBtn).toBeTruthy()
    await editBtn!.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('编辑助手版本')
    await wrapper.find('form').trigger('submit')
    expect(updateVersion).toHaveBeenCalledWith(expect.objectContaining({ id: 'ver-1' }))
  })

  // 点击删除调用删除接口。
  it('删除版本调用删除接口', async () => {
    deleteVersion.mockResolvedValue(undefined)
    const wrapper = mountPage()
    const delBtn = wrapper.findAll('button').find(b => b.text() === '删除')
    expect(delBtn).toBeTruthy()
    await delBtn!.trigger('click')
    expect(deleteVersion).toHaveBeenCalledWith('ver-1')
  })
})
```

- [ ] **Step 2：运行测试**

Run: `make web-test`
Expected: `AssistantVersionsPage` 的 5 条用例全部通过；其余前端测试不回归。
Run: `make web-typecheck`
Expected: 通过。

- [ ] **Step 3：提交**

```bash
git add web/src/pages/platform/AssistantVersionsPage.spec.ts
git commit -m "test(assistant-version): 覆盖版本管理页列表与增删改"
```

提交信息规则同 Task 1。

---

## Task 5：真实浏览器功能验证

**Files:** 无（验证任务）

按仓库 AGENTS.md「所有新功能必须用真实浏览器验证」，本任务用真实浏览器走完整版本管理流程。

- [ ] **Step 1：准备环境**

确认本地环境已起（`make dev-up` 或已运行的 docker compose 栈：manager、postgres、new-api），并已 `make migrate-up`。前端：在 `web/` 下 `npm run dev` 启动 Vite，或使用既有部署。

- [ ] **Step 2：用平台管理员登录并验证**

用平台管理员账号（组织标识留空，`admin` / `admin123`）登录后台。在真实浏览器中验证：
- 左侧导航出现「助手版本」入口，点击进入页面。
- 列表正常加载（首次为空或显示已有版本）。
- 「新增版本」：填名称、描述、内置提示词,选镜像与主模型,配几个智能路由槽位 → 保存 → 列表出现新版本,修订号 `r1`。
- 「编辑」：改内置提示词 → 保存 → 修订号变为 `r2`；只改描述再保存 → 修订号不变。
- 编辑态上传一个含合法 `SKILL.md` 的 tar → skill 列表出现该 skill；删除它 → 列表清空。
- 「删除」一个未被引用的版本 → 从列表消失。
- 镜像下拉项来自配置 `hermes.runtime_images`；模型下拉项来自 new-api 模型列表。

- [ ] **Step 3：记录验证结果**

把验证步骤与结果写入交付说明。若发现问题，先修复并重新验证，直到流程全部正常再交付。

---

## Self-Review

**Spec 覆盖（设计 spec §8.1 助手版本管理页）：**
- 列表 + 新建/编辑表单 → Task 2。
- 字段：名称、描述、内置提示词（textarea + 提示文案「可填写助手人设、行为规则等」）、使用镜像 select（走 `/runtime-images`）、主模型 select、8 个 auxiliary 路由 select → Task 2。
- skill tar 上传组件、可上传多个、可删除单个 → Task 3。
- 平台管理员专属（路由 `PLATFORM_ONLY` + 导航 `isPlatformAdmin` 分支）→ Task 2。

**不在本计划：** 组织 allowlist 多选、实例绑定版本（Phase 3）；实例列表 `version_synced` 提示（Phase 4）。

**Placeholder 扫描：** Task 2 的 `<!-- SKILL_SECTION_ANCHOR -->` 是 Task 3 的明确插入锚点，非未完成占位；Task 3 Step 1 明确要求替换该行。其余步骤均含完整代码与命令。

**类型一致性：** `AssistantVersionDTO` / `AssistantVersionFormPayload` / `AssistantVersionSkillDTO` / `AssistantVersionRoutingPayload` / `RuntimeImageDTO` 在 hook、页面、测试三处命名一致。`AUXILIARY_SLOTS` 的 8 个 key 与后端 `auxiliarySlots`、manifest routing 字段一致。`useModelsQuery` 复用自 `useOrganizations.ts`（已导出）。

---

## 后续

Phase 1b 交付后,助手版本特性的「目录管理」闭环完成（后端 + 前端）。后续 Phase 2-5 各自独立成计划：Phase 2（manifest v2 + oc-entrypoint + 第二个 variant）、Phase 3（组织 allowlist + 实例绑定）、Phase 4（version_synced + 重启刷新）、Phase 5（清理旧 persona/model）。
