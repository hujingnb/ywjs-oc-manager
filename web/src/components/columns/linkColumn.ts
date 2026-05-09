import { h } from 'vue'
import type { DataTableBaseColumn } from 'naive-ui'

export interface LinkColumnOptions<T> {
  title: string
  key?: string
  text: (row: T) => string
  onClick: (row: T) => void
  subtitle?: (row: T) => string | null | undefined
}

// linkColumn 生成可点击主链接列；可选 subtitle 显示为下方灰色小字。
export function linkColumn<T>(opts: LinkColumnOptions<T>): DataTableBaseColumn<T> {
  return {
    title: opts.title,
    key: opts.key ?? 'link',
    render: (row) => {
      const link = h('a', {
        class: 'data-table-link',
        onClick: () => opts.onClick(row),
      }, h('strong', opts.text(row)))
      const sub = opts.subtitle?.(row)
      return sub ? [link, h('small', { class: 'data-table-subtitle' }, sub)] : link
    },
  }
}
