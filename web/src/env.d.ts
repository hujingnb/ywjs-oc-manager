// 声明 Vue 单文件组件模块，供 TypeScript 在 import '*.vue' 时获得组件类型。
declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<object, object, unknown>
  export default component
}
