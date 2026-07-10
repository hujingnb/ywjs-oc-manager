import { inject, type ComputedRef, type InjectionKey, type Ref } from 'vue'

import type { AICCAgent } from '@/domain/aicc'

export interface AICCConsoleContext {
  agents: ComputedRef<AICCAgent[]>
  selectedAgentId: Ref<string | undefined>
  selectedAgent: ComputedRef<AICCAgent | undefined>
  agentsLoading: ComputedRef<boolean>
  agentsError: ComputedRef<Error | null>
  selectAgent: (agentId?: string) => void
  startCreateAgent: () => void
}

export const AICCConsoleContextKey: InjectionKey<AICCConsoleContext> = Symbol('AICCConsoleContext')

// AICC 管理页面必须挂载在 /aicc-console 工作台内；缺少上下文说明路由结构被误用。
export function useRequiredAICCConsoleContext(): AICCConsoleContext {
  const context = inject(AICCConsoleContextKey)
  if (!context) {
    throw new Error('AICC console context is not provided')
  }
  return context
}
