<template>
  <div style="display: grid; gap: 18px">
    <!-- 上传区：粘贴 Markdown 或上传 skill 文件夹，前端校验并打包成扁平 tar 后提交 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform</p>
          <h2 style="margin: 0">上传平台技能</h2>
        </div>
      </template>
      <n-form label-placement="top" @submit.prevent="onUpload">
        <!-- 上传方式切换：粘贴 Markdown（仅一个 SKILL.md）/ 上传 skill 文件夹 -->
        <n-form-item label="上传方式">
          <n-radio-group v-model:value="mode">
            <n-radio-button value="markdown">粘贴 Markdown</n-radio-button>
            <n-radio-button value="folder">上传文件夹</n-radio-button>
          </n-radio-group>
        </n-form-item>

        <!-- 粘贴 Markdown：内容即单个 SKILL.md，需含 frontmatter -->
        <n-form-item v-if="mode === 'markdown'" label="SKILL.md 内容 *">
          <n-input
            v-model:value="mdText"
            type="textarea"
            :rows="10"
            placeholder="粘贴 SKILL.md 全文，需含 --- 包裹的 frontmatter（至少含 name 字段）"
          />
        </n-form-item>

        <!-- 粘贴 Markdown 格式说明 + 一键填充示例 -->
        <div v-if="mode === 'markdown'" class="upload-hint">
          <div class="upload-hint__head">
            <span>
              格式：以 <code>---</code> 包裹的 frontmatter 开头（至少含 <code>name</code>，<code>description</code> 可选），其后是 Markdown 正文。示例：
            </span>
            <n-button text type="primary" size="tiny" @click="fillMarkdownExample">填充示例</n-button>
          </div>
          <pre class="upload-hint__code">{{ markdownExample }}</pre>
        </div>

        <!-- 上传文件夹：选择 skill 文件夹（其中直接包含 SKILL.md） -->
        <n-form-item v-if="mode === 'folder'" label="Skill 文件夹 *">
          <!-- 原生目录选择 input 隐藏，webkitdirectory 在点击前由 triggerFolderInput 动态设置 -->
          <input
            ref="folderInputRef"
            type="file"
            multiple
            style="display: none"
            @change="onFolderChange"
          />
          <div style="display: flex; align-items: center; gap: 12px">
            <n-button @click="triggerFolderInput">选择文件夹</n-button>
            <span v-if="folderName" class="state-text" style="margin: 0">{{ folderName }}（{{ folderFiles.length }} 个文件）</span>
            <span v-else class="state-text" style="margin: 0">未选择文件夹</span>
          </div>
          <!-- 选择前的关键提示：文件夹须含 SKILL.md，且其 frontmatter 为 YAML 含 name/description -->
          <p class="upload-hint" style="margin: 8px 0 0">
            文件夹需包含 <code>SKILL.md</code> 文件；该 <code>.md</code> 文件需包含 YAML 格式的技能名称（<code>name</code>）和描述（<code>description</code>）。
          </p>
        </n-form-item>

        <!-- 上传文件夹使用说明 -->
        <ul v-if="mode === 'folder'" class="upload-hint upload-hint__list">
          <li>选择 <strong>skill 自身的文件夹</strong>，其中需<strong>直接包含 SKILL.md</strong>（即 <code>所选文件夹/SKILL.md</code>，不要选它的上层目录）。</li>
          <li>文件夹内的子目录与附属文件会原样保留（如 <code>scripts/</code>、<code>assets/</code>、<code>reference.md</code>）。</li>
          <li>技能名取自 SKILL.md 的 <code>name</code> 字段，与文件夹名无关；上传时会自动剥掉最外层目录、按扁平结构打包。</li>
        </ul>

        <!-- 解析预览：成功展示识别到的技能 name/description，失败展示红色错误提示 -->
        <p v-if="parsed.error" class="state-text danger" style="margin: 4px 0">{{ parsed.error }}</p>
        <p v-else-if="parsed.meta" class="state-text" style="margin: 4px 0">
          识别到技能：<strong>{{ parsed.meta.name }}</strong>
          <template v-if="parsed.meta.description"> — {{ parsed.meta.description }}</template>
        </p>

        <n-form-item label="版本 *">
          <n-input v-model:value="version" placeholder="如 1.0.0" style="max-width: 240px" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="description" type="textarea" :rows="2" placeholder="技能描述（默认取自 SKILL.md，可修改）" />
        </n-form-item>

        <n-space justify="end">
          <n-button
            type="primary"
            attr-type="submit"
            :loading="uploadMutation.isPending.value"
            :disabled="!canUpload"
          >
            上传
          </n-button>
        </n-space>
        <p v-if="uploadFeedback" class="state-text" :class="{ danger: uploadFeedbackError }">{{ uploadFeedback }}</p>
      </n-form>
    </n-card>

    <!-- 平台库列表 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform</p>
          <h2 style="margin: 0">平台技能列表</h2>
        </div>
      </template>

      <!-- 加载态 -->
      <div v-if="isLoading" class="state-text">加载中…</div>
      <!-- 错误态 -->
      <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
      <!-- 正常态：使用 n-data-table 展示 skill 列表 -->
      <n-data-table
        v-else
        :columns="columns"
        :data="skills ?? []"
        size="small"
        :bordered="false"
        :row-key="(row: PlatformSkill) => row.id"
      />
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { NButton, NCard, NDataTable, NForm, NFormItem, NInput, NRadioButton, NRadioGroup, NSpace, useDialog, useMessage } from 'naive-ui'
import { usePlatformSkillsQuery, useUploadPlatformSkill, useDeletePlatformSkill } from '@/api/hooks/useSkills'
import {
  packFromFolder,
  packFromMarkdown,
  parseSkillFrontmatter,
  type SkillMeta,
  type UploadedFile,
} from '@/domain/skillPackaging'
import type { PlatformSkill } from '@/api'

// PlatformSkillsPage 是平台管理员的平台库 skill 管理页：上传/列出/删除。
// 上传支持两种方式：粘贴单个 SKILL.md，或上传整个 skill 文件夹；前端校验 frontmatter
// 并打包成扁平 tar 后再走 multipart 上传（name/description 自动取自 frontmatter）。
const { data: skills, isLoading, error } = usePlatformSkillsQuery()
const uploadMutation = useUploadPlatformSkill()
const deleteMutation = useDeletePlatformSkill()
const message = useMessage()
const dialog = useDialog()

// 上传方式：markdown=粘贴 SKILL.md 全文；folder=上传 skill 文件夹。
const mode = ref<'markdown' | 'folder'>('markdown')
// 粘贴 Markdown 模式的 SKILL.md 文本。
const mdText = ref('')
// 上传文件夹模式：已读入的文件列表与所选文件夹名（仅用于展示）。
const folderFiles = ref<UploadedFile[]>([])
const folderName = ref('')
// 手填版本号；描述默认取自 frontmatter，可手动修改。
const version = ref('')
const description = ref('')

// markdownExample 是粘贴 Markdown 模式的格式示例，既用于页面展示，也用于「填充示例」按钮。
const markdownExample = `---
name: my-skill
description: 一句话描述这个技能的用途
---

# My Skill

用 Markdown 说明这个技能：什么时候触发、做什么、怎么用。`

// fillMarkdownExample 把示例模板填入文本域，方便用户在其基础上修改。
function fillMarkdownExample() {
  mdText.value = markdownExample
}

// 上传操作的反馈文案与错误标记（成功显绿色，失败显红色）。
const uploadFeedback = ref('')
const uploadFeedbackError = ref(false)

// 隐藏的目录选择 input 引用。
const folderInputRef = ref<HTMLInputElement | null>(null)

// parsed 实时解析当前输入，得到 frontmatter 的 name/description 或校验错误，供预览与提交按钮使用。
// markdown 模式只解析 frontmatter（不打包）；folder 模式调用 packFromFolder 以同时校验扁平布局。
const parsed = computed<{ meta: SkillMeta | null; error: string }>(() => {
  try {
    if (mode.value === 'markdown') {
      if (!mdText.value.trim()) return { meta: null, error: '' }
      return { meta: parseSkillFrontmatter(mdText.value), error: '' }
    }
    if (folderFiles.value.length === 0) return { meta: null, error: '' }
    const r = packFromFolder(folderFiles.value)
    return { meta: { name: r.name, description: r.description }, error: '' }
  } catch (e) {
    return { meta: null, error: e instanceof Error ? e.message : String(e) }
  }
})

// 解析出 frontmatter 的 description 后自动预填到表单（仅当用户尚未手填时），保持「自动带出但可编辑」。
watch(
  () => parsed.value.meta?.description,
  (desc) => {
    if (desc && !description.value.trim()) {
      description.value = desc
    }
  },
)

// canUpload 在解析成功（拿到 name）且版本号已填、且不在上传中时才允许提交。
const canUpload = computed(
  () => !uploadMutation.isPending.value && parsed.value.meta !== null && Boolean(version.value.trim()),
)

// triggerFolderInput 在点击前动态设置 webkitdirectory，触发浏览器目录选择框。
// （以属性方式设置而非写在模板里，避免 webkitdirectory 这一非标准属性触发 vue-tsc 类型告警。）
function triggerFolderInput() {
  const el = folderInputRef.value
  if (!el) return
  el.setAttribute('webkitdirectory', '')
  el.setAttribute('directory', '')
  el.click()
}

// onFolderChange 读入所选文件夹下全部文件（含 webkitRelativePath 与字节），供后续打包。
async function onFolderChange(event: Event) {
  const input = event.target as HTMLInputElement
  const list = input.files
  if (!list || list.length === 0) {
    folderFiles.value = []
    folderName.value = ''
    return
  }
  const arr: UploadedFile[] = []
  for (const f of Array.from(list)) {
    const buf = new Uint8Array(await f.arrayBuffer())
    arr.push({ relativePath: f.webkitRelativePath || f.name, data: buf })
  }
  folderFiles.value = arr
  // 顶层目录名（webkitRelativePath 首段）即所选文件夹名，仅用于展示。
  folderName.value = arr[0]?.relativePath.split('/')[0] ?? ''
  // 重置后允许再次选择同一文件夹。
  input.value = ''
  uploadFeedback.value = ''
  uploadFeedbackError.value = false
}

// onUpload 在浏览器内把输入打包成扁平 tar，再走 multipart 上传到平台库。
// 成功后重置表单并刷新列表（hook onSuccess 自动 invalidate 缓存）。
async function onUpload() {
  if (!canUpload.value) return
  uploadFeedback.value = ''
  uploadFeedbackError.value = false
  try {
    // 提交时再打包：拿到 name/description（来自 frontmatter）与扁平 tar 字节。
    const result = mode.value === 'markdown' ? packFromMarkdown(mdText.value) : packFromFolder(folderFiles.value)
    // result.tar 是 Uint8Array，作为 BlobPart 传入 File；显式标注规避新版 TS lib 对
    // ArrayBufferLike vs ArrayBuffer 的泛型差异告警。
    const file = new File([result.tar as BlobPart], `${result.name}.tar`, { type: 'application/x-tar' })
    await uploadMutation.mutateAsync({
      name: result.name,
      version: version.value.trim(),
      // 描述以用户手填为准，未填则回退到 frontmatter 的 description。
      description: description.value.trim() || result.description || undefined,
      file,
    })
    message.success(`已上传 skill ${result.name} ${version.value.trim()}`)
    // 上传成功后重置表单。
    mode.value = 'markdown'
    mdText.value = ''
    folderFiles.value = []
    folderName.value = ''
    version.value = ''
    description.value = ''
  } catch (err) {
    uploadFeedbackError.value = true
    uploadFeedback.value = err instanceof Error ? err.message : '上传失败'
  }
}

// onDelete 弹出 useDialog().warning 二次确认后再执行删除，避免误操作。
function onDelete(skill: PlatformSkill) {
  dialog.warning({
    title: '删除 Skill',
    content: `确定删除 skill「${skill.name} ${skill.version}」？删除后不可恢复。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteMutation.mutateAsync(skill.id)
        message.success(`已删除 skill ${skill.name} ${skill.version}`)
      } catch (err) {
        message.error(err instanceof Error ? err.message : '删除失败')
      }
    },
  })
}

// formatBytes 将字节数格式化为人类可读的大小，与 AssistantVersionsPage 保持一致。
function formatBytes(n?: number): string {
  if (n == null) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

// columns 定义 n-data-table 列：name、version、file_size、操作（删除）。
const columns = computed(() => [
  {
    title: '名称',
    key: 'name',
    render: (row: PlatformSkill) => [
      h('strong', row.name),
      row.description ? h('small', { class: 'data-table-subtitle' }, row.description) : null,
    ],
  },
  {
    title: '版本',
    key: 'version',
    render: (row: PlatformSkill) => row.version,
  },
  {
    title: '文件大小',
    key: 'file_size',
    render: (row: PlatformSkill) => formatBytes(row.file_size),
  },
  {
    title: '操作',
    key: 'actions',
    render: (row: PlatformSkill) =>
      h(
        NButton,
        {
          size: 'small',
          type: 'error',
          disabled: deleteMutation.isPending.value,
          onClick: () => onDelete(row),
        },
        { default: () => '删除' },
      ),
  },
])
</script>

<style scoped>
/* upload-hint：上传区的格式说明/使用说明文案，弱化为次要灰字 */
.upload-hint {
  margin: -4px 0 12px;
  font-size: 12px;
  line-height: 1.6;
  color: #8a8f99;
}
.upload-hint__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
/* upload-hint__code：SKILL.md 示例代码块，等宽字体 + 浅底，保留换行 */
.upload-hint__code {
  margin: 0;
  padding: 10px 12px;
  background: rgba(0, 0, 0, 0.045);
  border-radius: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
  color: #4b5563;
}
.upload-hint__list {
  margin: 0 0 12px;
  padding-left: 18px;
}
.upload-hint__list li {
  margin: 3px 0;
}
/* 行内 code 标记：键名/路径片段 */
.upload-hint :deep(code),
.upload-hint code {
  padding: 1px 5px;
  background: rgba(0, 0, 0, 0.06);
  border-radius: 4px;
  font-family: ui-monospace, monospace;
  font-size: 11px;
}
</style>
