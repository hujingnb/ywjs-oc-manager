import type { DataTableBaseColumn } from 'naive-ui'

// timeColumn 生成时间列：值为空时返回 placeholder（默认 '—'）。
export function timeColumn<T>(
  title: string,
  pick: (row: T) => string | null | undefined,
  options: { key?: string; placeholder?: string } = {},
): DataTableBaseColumn<T> {
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
