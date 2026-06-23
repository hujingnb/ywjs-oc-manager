import { h } from 'vue'
import { NButton, NSpace } from 'naive-ui'
import type { DataTableBaseColumn } from 'naive-ui'

import { i18n } from '@/i18n'

// RowAction 描述列表行上的单个业务操作按钮。
// hidden 和 disabled 都按行计算，用于表达权限、状态互斥或异步中的不可用态。
export interface RowAction<T> {
  // label 支持函数式文案，便于同一列按行展示“启用/禁用”等业务动作。
  label: string | ((row: T) => string)
  // onClick 接收完整行对象，由页面把行 ID、操作类型转换成具体 API 调用。
  onClick: (row: T) => void
  // type 直接透传给 Naive UI 按钮，用于标识主要动作或危险动作。
  type?: 'default' | 'primary' | 'error' | 'warning'
  // disabled 保留按钮但禁止点击，常用于提交中或业务条件暂不满足。
  disabled?: (row: T) => boolean
  // hidden 完全隐藏该动作，常用于启用/禁用等互斥状态按钮。
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
    // 默认标题用全局 i18n（非 setup 上下文），不再硬编码中文「操作」，避免 en 界面漏译。
    title: options.title ?? i18n.global.t('common.table.actions'),
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
