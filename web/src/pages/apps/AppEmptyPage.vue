<!-- web/src/pages/apps/AppEmptyPage.vue -->
<template>
  <div class="empty-container">
    <n-empty :description="emptyDescription">
      <template #icon>
        <Bot :size="48" :stroke-width="1" />
      </template>
      <!-- org_admin 可自助建实例:给跳成员页的入口;其余角色仅提示联系管理员,不展示按钮。 -->
      <template v-if="auth.isOrgAdmin" #extra>
        <n-button size="small" @click="goCreate">去成员页创建实例</n-button>
      </template>
    </n-empty>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NEmpty } from 'naive-ui'
import { Bot } from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'

// 空状态页同时服务 org_member(无实例,需联系管理员)与 org_admin(可自助建实例)。
const auth = useAuthStore()
const router = useRouter()

// emptyDescription:文案随角色变化,管理员可自助创建、成员需联系管理员。
const emptyDescription = computed(() =>
  auth.isOrgAdmin ? '你还没有属于自己的实例' : '请联系管理员创建实例',
)

// goCreate:跳成员页;org_admin 在自己那一行用「为该成员创建实例」完成自助建实例。
function goCreate() {
  router.push('/members')
}
</script>

<style scoped>
.empty-container {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
  min-height: 400px;
}
</style>
