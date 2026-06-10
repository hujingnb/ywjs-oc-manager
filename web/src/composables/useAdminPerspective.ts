// useAdminPerspective 维护企业管理员(org_admin)的「视角」状态并持久化到 localStorage。
// 视角决定 DashboardLayout 渲染哪一套菜单:'manage'=企业管理视角,'instance'=我的实例视角。
// 仅 org_admin 真正消费此状态;其余角色即使读到默认值也无副作用(菜单分支不依赖)。
import { ref } from 'vue'

// AdminPerspective:两种视角的字面量类型。
export type AdminPerspective = 'manage' | 'instance'

// STORAGE_KEY:持久化键名(完整键 'oc.admin.perspective')。带 oc. 前缀避免与其他业务键冲突。
const STORAGE_KEY = 'oc.admin.perspective'

// readPerspective:从 localStorage 读取视角;无值或非法值统一回退到默认的企业管理视角。
function readPerspective(): AdminPerspective {
  return localStorage.getItem(STORAGE_KEY) === 'instance' ? 'instance' : 'manage'
}

// perspective:模块级单例响应式状态。所有调用方共享同一 ref,任一处 setPerspective 变更
// 可跨组件即时响应(避免每次调用新建 ref 导致状态不共享)。初值取自持久化,支持刷新保持。
const perspective = ref<AdminPerspective>(readPerspective())

export function useAdminPerspective() {
  // setPerspective:切换视角并写持久化,保证刷新后保持同一视角。
  function setPerspective(next: AdminPerspective) {
    perspective.value = next
    localStorage.setItem(STORAGE_KEY, next)
  }

  // resetPerspective:清除持久化并把状态回默认(退出登录时调用),避免换账号串视角。
  function resetPerspective() {
    perspective.value = 'manage'
    localStorage.removeItem(STORAGE_KEY)
  }

  return { perspective, setPerspective, resetPerspective }
}
