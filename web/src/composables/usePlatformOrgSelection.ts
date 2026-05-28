// usePlatformOrgSelection 为平台管理员补齐组织选择上下文。
// 后端成员、应用、审计和知识库接口仍按 org_id 分域；平台管理员没有自身 org_id，
// 因此页面需要从组织列表中选出一个目标组织后再发起 org-scoped 查询。
import { computed, ref, watch, type Ref } from 'vue'

import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'

// OrgSelectionUser 是组织选择逻辑需要的最小登录用户视图。
export interface OrgSelectionUser {
  // role 决定是否需要展示平台组织选择器。
  role?: string
  // org_id 是组织角色的默认组织；平台管理员通常为空。
  org_id?: string
}

// usePlatformOrgSelection 返回平台管理员可切换的组织 ID，以及组织角色的自身组织 ID。
export function usePlatformOrgSelection(
  user: Ref<OrgSelectionUser | null | undefined>,
  explicitOrgId: Ref<string | undefined>,
) {
  const isPlatformAdmin = computed(() => user.value?.role === 'platform_admin')
  const selectedOrgId = ref<string | undefined>(explicitOrgId.value)
  const organizationsQuery = useOrganizationsQuery(() => isPlatformAdmin.value)

  // 路由显式传入 orgId 时优先使用路由上下文，避免覆盖组织详情页内的选择。
  watch(explicitOrgId, (orgId) => {
    if (orgId) selectedOrgId.value = orgId
  }, { immediate: true })

  // 平台管理员首次进入组织域页面时，默认选中第一个组织，避免列表因 org_id 为空而停留在空态。
  watch(organizationsQuery.data, (orgs) => {
    if (!isPlatformAdmin.value || selectedOrgId.value || !orgs?.length) return
    selectedOrgId.value = orgs[0].id
  }, { immediate: true })

  const effectiveOrgId = computed(() => {
    if (isPlatformAdmin.value) return selectedOrgId.value
    return explicitOrgId.value ?? user.value?.org_id
  })

  const orgOptions = computed(() =>
    (organizationsQuery.data.value ?? []).map((org) => ({ label: org.name, value: org.id })),
  )

  return {
    isPlatformAdmin,
    selectedOrgId,
    effectiveOrgId,
    orgOptions,
    organizations: organizationsQuery.data,
    organizationsLoading: organizationsQuery.isLoading,
    organizationsError: organizationsQuery.error,
  }
}
