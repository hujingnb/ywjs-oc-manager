<template>
  <NModal
    :show="session !== null"
    :mask-closable="false"
    :closable="!isUploading"
    preset="card"
    title="文件上传"
    style="max-width: 480px"
    @close="onClose"
  >
    <div v-if="session" style="display: grid; gap: 12px">
      <!-- 进行中：当前文件 + N/M + 字节进度 -->
      <template v-if="isUploading && currentItem">
        <div>
          <strong>{{ currentItem.label }}</strong>
          <span class="state-text" style="margin-left: 8px">
            ({{ session.currentIndex + 1 }}/{{ session.items.length }})
          </span>
        </div>
        <NProgress type="line" :percentage="currentPct" />
        <p class="state-text">
          {{ formatBytes(session.currentLoaded) }} / {{ formatBytes(currentItem.size) }}
        </p>
        <NButton type="warning" @click="store.cancel()">取消上传</NButton>
      </template>

      <!-- 全部结束：汇总 + 失败详情 + 关闭按钮 -->
      <template v-else>
        <p>
          成功 {{ counts.succeeded }} · 失败 {{ counts.failed }} · 取消 {{ counts.cancelled }}
        </p>
        <NCollapse v-if="failedItems.length">
          <NCollapseItem title="失败详情" name="failed">
            <ul style="margin: 0; padding-left: 16px">
              <li v-for="it in failedItems" :key="it.id">
                {{ it.label }}：{{ it.error }}
              </li>
            </ul>
          </NCollapseItem>
        </NCollapse>
        <NButton type="primary" @click="onClose">关闭</NButton>
      </template>
    </div>
  </NModal>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NCollapse, NCollapseItem, NModal, NProgress } from 'naive-ui'

import { useUploadProgressStore } from '@/stores/uploadProgress'

// UploadProgressModal 是全局唯一的文件上传进度反馈窗口；订阅 store 自动显示 / 隐藏。
// App.vue 根节点统一挂载，业务页面不需要自己渲染 modal。
const store = useUploadProgressStore()
const session = computed(() => store.session)
const isUploading = computed(() => store.isUploading)
const currentItem = computed(() => store.session?.items[store.session.currentIndex] ?? null)

// 字节百分比 guard：零字节文件直接 100%；编码膨胀（loaded > size）截到 100%。
const currentPct = computed(() => {
  const item = currentItem.value
  if (!item || item.size <= 0) return 100
  const loaded = store.session?.currentLoaded ?? 0
  return Math.min(Math.round((loaded / item.size) * 100), 100)
})

// failedItems 仅在会话结束后展示，供用户查看出错原因；不影响进行中视图。
const failedItems = computed(() => store.session?.items.filter(i => i.status === 'failed') ?? [])

const counts = computed(() => {
  const items = store.session?.items ?? []
  return {
    succeeded: items.filter(i => i.status === 'succeeded').length,
    failed: items.filter(i => i.status === 'failed').length,
    cancelled: items.filter(i => i.status === 'cancelled').length,
  }
})

// onClose 同时响应 NModal 的 X 按钮和「关闭」按钮：仅在非上传中时 reset；上传中由 NModal closable=false 阻止触发。
function onClose(): void {
  if (!isUploading.value) {
    store.reset()
  }
}

// formatBytes 与既有页面一致的字节格式化，避免引入 lodash 这类大依赖。
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}
</script>
