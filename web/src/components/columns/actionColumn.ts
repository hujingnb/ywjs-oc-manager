import { h } from 'vue'
import { NButton, NSpace } from 'naive-ui'
import type { DataTableBaseColumn } from 'naive-ui'

export interface RowAction<T> {
  label: string | ((row: T) => string)
  onClick: (row: T) => void
  type?: 'default' | 'primary' | 'error' | 'warning'
  disabled?: (row: T) => boolean
  hidden?: (row: T) => boolean
}

// actionColumn 生成操作列：每个 RowAction 渲染为 NButton，整体用 NSpace 排列。
// 隐藏 / 禁用 / 标签 都支持函数式响应当前行；type 必须是静态值，
// 需要按行变色时用多条 RowAction 配合 hidden 互斥渲染。
export function actionColumn<T>(
  actions: RowAction<T>[],
  options: { title?: string; key?: string } = {},
): DataTableBaseColumn<T> {
  return {
    title: options.title ?? '操作',
    key: options.key ?? 'actions',
    render: (row) => h(NSpace, { size: 'small' }, {
      default: () => actions
        .filter((a) => !a.hidden?.(row))
        .map((a) => h(NButton, {
          size: 'small',
          type: a.type ?? 'default',
          disabled: a.disabled?.(row) ?? false,
          onClick: () => a.onClick(row),
        }, {
          default: () => typeof a.label === 'function' ? a.label(row) : a.label,
        })),
    }),
  }
}
