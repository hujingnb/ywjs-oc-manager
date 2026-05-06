import { createApp } from 'vue'
import { createPinia } from 'pinia'

import { router } from '@/app/router'
import { queryClient, VueQueryPlugin } from '@/app/query-client'
import { setUnauthorizedHandler } from '@/api/client'
import App from '@/App.vue'
import '@/styles/base.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin, { queryClient })

// 全局 401 处理：API 收到 401 时清 token 并跳 login，附上当前路径作为 redirect。
// 避免按钮点击后悄悄失败（mutation error 被业务组件 catch 吞掉，用户以为没操作）。
setUnauthorizedHandler(() => {
  const current = router.currentRoute.value
  if (current.path === '/login') return
  void router.replace({ path: '/login', query: { redirect: current.fullPath } })
})

app.mount('#app')
