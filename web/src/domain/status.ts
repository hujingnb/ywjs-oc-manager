export interface StatusView {
  label: string
  tone: 'neutral' | 'success' | 'warning' | 'danger'
}

const appStatusViews: Record<string, StatusView> = {
  draft: { label: '草稿', tone: 'neutral' },
  initializing: { label: '初始化中', tone: 'warning' },
  binding_waiting: { label: '待绑定', tone: 'warning' },
  binding_failed: { label: '绑定失败', tone: 'danger' },
  running: { label: '运行中', tone: 'success' },
  stopped: { label: '已停止', tone: 'neutral' },
  error: { label: '异常', tone: 'danger' },
  deleted: { label: '已删除', tone: 'neutral' },
}

// formatAppStatus 将后端状态机值映射为页面可展示的中文标签和视觉语义。
// 未识别状态使用 warning，便于在测试环境中尽早发现后端新增状态未同步到前端。
export function formatAppStatus(status: string): StatusView {
  return appStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}
