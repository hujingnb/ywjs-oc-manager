<template>
  <div style="display: grid; gap: 18px">
    <!-- 上传区：填写 name/version/description 并选择 tar 文件后提交 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform</p>
          <h2 style="margin: 0">上传平台技能</h2>
        </div>
      </template>
      <n-form label-placement="top" @submit.prevent="onUpload">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="skill 唯一名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="版本 *">
              <n-input v-model:value="form.version" placeholder="如 1.0.0" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="描述">
              <n-input v-model:value="form.description" type="textarea" :rows="2" placeholder="skill 功能说明（可选）" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="Skill 文件 (.tar) *">
              <!-- 原生 file input 隐藏，通过按钮触发，与 AssistantVersionsPage 保持一致 -->
              <input
                ref="fileInputRef"
                type="file"
                accept=".tar"
                style="display: none"
                @change="onFileChange"
              />
              <div style="display: flex; align-items: center; gap: 12px">
                <n-button @click="triggerFileInput">选择文件</n-button>
                <span v-if="form.file" class="state-text" style="margin: 0">{{ form.file.name }}</span>
                <span v-else class="state-text" style="margin: 0">未选择文件</span>
              </div>
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
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
          </n-grid-item>
        </n-grid>
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
import { computed, h, reactive, ref } from 'vue'
import { NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem, NInput, NSpace, useDialog, useMessage } from 'naive-ui'
import { usePlatformSkillsQuery, useUploadPlatformSkill, useDeletePlatformSkill } from '@/api/hooks/useSkills'
import type { PlatformSkill } from '@/api'

// PlatformSkillsPage 是平台管理员的平台库 skill 管理页：上传/列出/删除。
const { data: skills, isLoading, error } = usePlatformSkillsQuery()
const uploadMutation = useUploadPlatformSkill()
const deleteMutation = useDeletePlatformSkill()
const message = useMessage()
const dialog = useDialog()

// 上传表单字段：name/version/description + 文件对象。
const form = reactive({
  name: '',
  version: '',
  description: '',
  file: null as File | null,
})

// 上传操作的反馈文案与错误标记（成功显绿色，失败显红色）。
const uploadFeedback = ref('')
const uploadFeedbackError = ref(false)

// 隐藏的文件选择 input 引用。
const fileInputRef = ref<HTMLInputElement | null>(null)

// canUpload 在必填项（name/version/file）均填写时才允许提交。
const canUpload = computed(
  () => !uploadMutation.isPending.value && Boolean(form.name.trim()) && Boolean(form.version.trim()) && form.file !== null,
)

// triggerFileInput 触发隐藏的文件选择框，与 AssistantVersionsPage 保持一致。
function triggerFileInput() {
  fileInputRef.value?.click()
}

// onFileChange 监听文件选择，更新 form.file 并重置反馈文案。
function onFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  form.file = input.files?.[0] ?? null
  // 重置后允许用户再次选择同名文件。
  input.value = ''
  uploadFeedback.value = ''
  uploadFeedbackError.value = false
}

// onUpload 将 name/version/description/file 通过 multipart 上传到平台库。
// 成功后清空表单并刷新列表（hook onSuccess 自动 invalidate 缓存）。
async function onUpload() {
  if (!canUpload.value || !form.file) return
  uploadFeedback.value = ''
  uploadFeedbackError.value = false
  try {
    await uploadMutation.mutateAsync({
      name: form.name.trim(),
      version: form.version.trim(),
      description: form.description.trim() || undefined,
      file: form.file,
    })
    message.success(`已上传 skill ${form.name} ${form.version}`)
    // 上传成功后重置表单。
    form.name = ''
    form.version = ''
    form.description = ''
    form.file = null
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
