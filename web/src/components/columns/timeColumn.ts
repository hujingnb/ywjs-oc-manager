import type { DataTableBaseColumn } from 'naive-ui'

// timeColumn 生成时间列：值为空时返回 placeholder（默认 '—'）。
// pick 只负责取出后端时间字段，格式化和空值降级在列工具内统一处理。
export function timeColumn<T>(
  title: string,
  pick: (row: T) => string | null | undefined,
  options: { key?: string; placeholder?: string } = {},
): DataTableBaseColumn<T> {
  // placeholder 对缺失时间统一展示，避免列表中出现 undefined/null。
  const placeholder = options.placeholder ?? '—'
  return {
    title,
    key: options.key ?? 'time',
    render: (row) => {
      const v = pick(row)
      return v ? new Date(v).toLocaleString() : placeholder
    },
  }
}
