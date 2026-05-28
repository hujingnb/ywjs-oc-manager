// useMemberApp 为 org_member 提供其唯一活跃实例的 ID。
// 侧边栏和路由守卫依赖此 composable 决定导航目标。
import { computed } from 'vue'

import { useAppsByOrgQuery } from '@/api/hooks/useApps'
import { useAuthStore } from '@/stores/auth'

export function useMemberApp() {
  const auth = useAuthStore()

  // org_member 的 org_id 即为查询范围；非 org_member 时 orgId 为 undefined，query 不启用。
  const orgId = computed(() => auth.isOrgMember ? auth.user?.org_id : undefined)
  const { data: apps, isLoading } = useAppsByOrgQuery(orgId)

  // 从企业实例列表中筛选当前用户拥有的实例（数据库保证最多一个）。
  const memberApp = computed(() =>
    apps.value?.find(app => app.owner_user_id === auth.user?.id),
  )

  const appId = computed(() => memberApp.value?.id)
  const hasApp = computed(() => Boolean(appId.value))

  return { appId, hasApp, isLoading }
}
