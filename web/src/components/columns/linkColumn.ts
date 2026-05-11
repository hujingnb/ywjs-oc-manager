import { h } from 'vue'
import type { DataTableBaseColumn } from 'naive-ui'

// LinkColumnOptions 描述可点击主文本列的业务字段和跳转动作。
export interface LinkColumnOptions<T> {
  // title 是表头文案，由页面按业务对象命名。
  title: string
  // key 默认使用 link；同页多个链接列时可显式覆盖，避免 DataTable key 冲突。
  key?: string
  // text 负责从行数据提取主展示文案，通常是名称或标题。
  text: (row: T) => string
  // onClick 由页面决定跳转或打开详情，列工具不绑定具体路由。
  onClick: (row: T) => void
  // subtitle 用于展示 ID、备注等辅助信息；返回空值时不占位。
  subtitle?: (row: T) => string | null | undefined
}

// linkColumn 生成可点击主链接列；可选 subtitle 显示为下方灰色小字。
export function linkColumn<T>(opts: LinkColumnOptions<T>): DataTableBaseColumn<T> {
  return {
    title: opts.title,
    key: opts.key ?? 'link',
    render: (row) => {
      // 主链接只负责触发页面传入的业务导航，避免列组件知道具体 URL 结构。
      const link = h('a', {
        class: 'data-table-link',
        onClick: () => opts.onClick(row),
      }, h('strong', opts.text(row)))
      const sub = opts.subtitle?.(row)
      return sub ? [link, h('small', { class: 'data-table-subtitle' }, sub)] : link
    },
  }
}
