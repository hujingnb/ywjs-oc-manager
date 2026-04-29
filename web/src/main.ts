import { createApp } from 'vue'
import { createPinia } from 'pinia'

import { router } from '@/app/router'
import { queryClient, VueQueryPlugin } from '@/app/query-client'
import App from '@/App.vue'
import '@/styles/base.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin, { queryClient })
app.mount('#app')
