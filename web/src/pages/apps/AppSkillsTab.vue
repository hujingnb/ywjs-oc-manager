<template>
  <!-- AppSkillsTab：实例详情页「技能」tab，供平台/企业管理员管理该实例的技能。 -->
  <!-- SkillManager 内部通过 inject('app') 获取应用上下文以判断写权限，由 AppDetailPage.vue 的 provide 注入。 -->
  <SkillManager :app-id="appId" />
</template>

<script setup lang="ts">
import { inject, type Ref } from 'vue'

import SkillManager from '@/components/SkillManager.vue'
import type { AppDTO } from '@/api/hooks/useApps'

// AppSkillsTab 接受实例 ID 作为 prop，与其它 tab（AppKnowledgeTab 等）保持一致的接口模式。
const props = defineProps<{
  // 目标实例 ID，从路由参数经 props: true 注入，传给 SkillManager。
  appId: string
}>()

// app 由 AppDetailPage 通过 provide('app') 注入；SkillManager 内部再次 inject 以做 canManageApp 判断。
// 此处 inject 仅用于 TypeScript 类型对齐，不做额外操作。
const _app = inject<Ref<AppDTO | null>>('app')

// 避免 TypeScript 未使用变量警告。
void props
void _app
</script>
