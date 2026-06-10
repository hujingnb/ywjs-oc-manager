// useOwnApp 取「当前登录用户自己的实例」，对 org_member 与 org_admin 通用。
// 「一人一 Agent」对 org_admin 也适用：org_admin 可能拥有 owner_user_id=自己的实例。
// 与 useMemberApp 的区别仅在于 org_admin 也启用查询；useMemberApp 仍专供成员侧边栏/路由守卫，
// 不在此处复用以避免成员导航回归。
import { computed } from 'vue'

import { useAppsByOrgQuery } from '@/api/hooks/useApps'
import { useAuthStore } from '@/stores/auth'

export function useOwnApp() {
  const auth = useAuthStore()

  // org_member 或 org_admin 的 org_id 即为查询范围；其余角色 orgId 为 undefined，query 不启用。
  const orgId = computed(() => (auth.isOrgMember || auth.isOrgAdmin) ? auth.user?.org_id : undefined)
  const { data: apps, isLoading } = useAppsByOrgQuery(orgId)

  // 从组织实例列表中筛选当前用户拥有的实例（数据库保证最多一个）。
  const ownApp = computed(() =>
    apps.value?.find(app => app.owner_user_id === auth.user?.id),
  )

  const appId = computed(() => ownApp.value?.id)
  const hasApp = computed(() => Boolean(appId.value))
  // app：当前用户实例对象（归一化为 null），供页面 provide('app') 给 SkillManager 做归属权限判断。
  const app = computed(() => ownApp.value ?? null)

  return { appId, hasApp, isLoading, app }
}
