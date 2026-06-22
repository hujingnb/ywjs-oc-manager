// parseStatus 收敛 RAGFlow 解析状态在前端的标签键、标签色与筛选选项，避免在各知识库页重复定义。
// label 字段已从中文字面量迁移为 i18n 键，消费方通过 t() 解析为当前语言文案。

// PARSE_STATUS_LABELS 是已知解析状态到 i18n 键的映射；服务端新增状态时由 parseStatusLabel 原样兜底。
export const PARSE_STATUS_LABELS: Record<string, string> = {
  queued:    'domain.parseStatus.queued',
  running:   'domain.parseStatus.running',
  completed: 'domain.parseStatus.completed',
  failed:    'domain.parseStatus.failed',
  stopped:   'domain.parseStatus.stopped',
}

// parseStatusLabel 把解析状态转成 i18n 键；未知值直接透出便于排障与兼容服务端新增状态。
// 消费方对已知状态调用 t(parseStatusLabel(status))，对未知状态直接展示原始值。
export function parseStatusLabel(status: string): string {
  return PARSE_STATUS_LABELS[status] ?? status
}

// parseStatusTagType 把解析状态映射为 naive-ui 标签色：完成=成功色，进行中=警告色，失败/停止=错误色，
// 其它（含服务端新增的未知状态）保留默认色。此函数不含 i18n，逻辑无变化。
export function parseStatusTagType(status: string): 'success' | 'warning' | 'error' | 'default' {
  if (status === 'completed') return 'success'
  if (status === 'queued' || status === 'running') return 'warning'
  if (status === 'failed' || status === 'stopped') return 'error'
  return 'default'
}

// PARSE_STATUS_FILTER_OPTIONS 是文件列表「解析状态」下拉的静态选项模板，按解析生命周期顺序排列。
// label 为 i18n 键；消费方应在 computed 中调用 t(opt.label) 以确保语言切换时响应式更新。
// 不含「全部」项——由 n-select 的 clearable 表达「清空即全部状态」。
export const PARSE_STATUS_FILTER_OPTIONS: { label: string; value: string }[] = [
  { label: PARSE_STATUS_LABELS.queued,    value: 'queued' },
  { label: PARSE_STATUS_LABELS.running,   value: 'running' },
  { label: PARSE_STATUS_LABELS.completed, value: 'completed' },
  { label: PARSE_STATUS_LABELS.failed,    value: 'failed' },
  { label: PARSE_STATUS_LABELS.stopped,   value: 'stopped' },
]
