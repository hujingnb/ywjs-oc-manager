// useBeforeUnloadGuard 在上传会话进行中拦截浏览器刷新 / 关闭 tab，触发原生「确定离开？」确认框。
// 现代浏览器忽略自定义文案，但仍要求事件被 preventDefault 且 returnValue 非空。
// 该 composable 只应在 App 根挂一次，避免重复注册同一个监听器。
import { onBeforeUnmount, onMounted } from 'vue'

import { useUploadProgressStore } from '@/stores/uploadProgress'

export function useBeforeUnloadGuard(): void {
  const store = useUploadProgressStore()

  // handler 直接读 store.isUploading（reactive），不需要 watch；浏览器只在用户尝试离开时触发一次。
  function handler(event: BeforeUnloadEvent): void {
    if (!store.isUploading) return
    event.preventDefault()
    event.returnValue = ''
  }

  onMounted(() => {
    window.addEventListener('beforeunload', handler)
  })
  onBeforeUnmount(() => {
    window.removeEventListener('beforeunload', handler)
  })
}
