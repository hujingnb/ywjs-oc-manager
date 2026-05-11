// query-client.ts 集中创建 Vue Query 客户端和插件导出。
// 默认 staleTime/retry 是全站共享策略，单个 hook 需要更快刷新时应显式覆盖。
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      retry: 1,
    },
  },
})

export { VueQueryPlugin }
