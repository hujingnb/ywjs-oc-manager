<template>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">App · Knowledge</p>
        <h2>应用知识库</h2>
      </div>
    </div>

    <p v-if="!app" class="state-text">尚未加载应用信息</p>
    <p v-else-if="!knowledgeContext" class="state-text">无法构造知识库查询上下文（缺 org_id / owner_user_id）</p>
    <p v-else-if="listing.isLoading.value" class="state-text">加载中…</p>
    <p v-else-if="listing.error.value" class="state-text danger">查询失败：{{ listing.error.value?.message }}</p>
    <table v-else>
      <thead>
        <tr>
          <th>名称</th>
          <th>大小</th>
          <th class="actions-column">类型</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="entry in listing.data.value?.entries ?? []" :key="entry.path">
          <td>
            <strong>{{ entry.name }}{{ entry.is_dir ? '/' : '' }}</strong>
          </td>
          <td>{{ entry.is_dir ? '—' : formatBytes(entry.size) }}</td>
          <td class="actions-column">{{ entry.is_dir ? '目录' : '文件' }}</td>
        </tr>
        <tr v-if="!listing.data.value?.entries?.length">
          <td colspan="3" class="state-text">应用知识库暂无文件</td>
        </tr>
      </tbody>
    </table>

    <p class="state-text">
      Phase A 仅展示文件列表；上传 / 删除 / 节点同步状态在 Phase B 阶段补全。
    </p>
  </section>
</template>

<script setup lang="ts">
import { computed, inject, type Ref } from 'vue'

import type { AppDTO } from '@/api/hooks/useApps'
import { useAppKnowledgeQuery } from '@/api/hooks/useKnowledge'

const props = defineProps<{ appId: string }>()
const appIdRef = computed<string | undefined>(() => props.appId)

// 通过 provide 注入的 app；Phase A 不强求 ownerUserId 路径，提供 sentinel 即可。
const app = inject<Ref<AppDTO | null>>('app')

// 应用级知识库 API 需要 org_id + owner_user_id；这两项来自 app DTO。
const knowledgeContext = computed(() => {
  if (!app?.value) return undefined
  return {
    orgId: app.value.org_id,
    ownerUserId: app.value.owner_user_id,
    path: '',
  }
})

const listing = useAppKnowledgeQuery(appIdRef, knowledgeContext)

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}
</script>
