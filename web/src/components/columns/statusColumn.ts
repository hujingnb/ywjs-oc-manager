import { h } from 'vue'
import type { DataTableBaseColumn } from 'naive-ui'
import StatusBadge from '@/components/StatusBadge.vue'
import type { StatusView } from '@/domain/status'

// statusColumn 生成统一风格的状态列：内部用 StatusBadge 渲染 tone+label。
// view 回调由页面或 domain/status 提供，确保状态翻译和颜色策略不散落在表格内。
export function statusColumn<T>(
  title: string,
  view: (row: T) => StatusView,
  options: { key?: string } = {},
): DataTableBaseColumn<T> {
  return {
    title,
    key: options.key ?? 'status',
    render: (row) => h(StatusBadge, { view: view(row) }),
  }
}
