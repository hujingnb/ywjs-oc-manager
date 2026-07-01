// apps/conversations 文案（zh）：会话管理 tab 的全部可见字符串。
export default {
  // 新建会话按钮
  new: '新建会话',
  // 发送按钮（空闲态）
  send: '发送',
  // 发送按钮（流式发送中）
  sending: '发送中…',
  // 输入框占位文案
  placeholder: '输入消息…',
  // 会话条目重命名操作
  rename: '重命名',
  // 会话条目删除操作
  delete: '删除',
  // 无会话时的空状态提示
  empty: '暂无会话',
  // 附件按钮文案
  attach: '文件',
  // 发送按钮（任务进行中，点击入队而非立即发送）
  queueSend: '排队发送',
  // 待发送队列面板标题
  queueTitle: '待发送队列',
  // 任务进行中的状态提示（队列面板内）
  generating: '回复生成中…',
  // 队列项编辑操作
  queueEdit: '编辑',
  // 队列项编辑态保存
  queueSave: '保存',
  // 队列项编辑态取消
  queueCancel: '取消',
  // 队列项删除操作
  queueRemove: '删除',
  // 失败队列项重试操作
  queueRetry: '重试',
  // 失败队列项状态标记
  queueFailed: '发送失败',
  // ─── 语音输入 ───────────────────────────────────────────────────────────────
  voice: {
    // 麦克风按钮：空闲态提示(开始录音)
    start: '语音输入',
    // 录音中提示(再次点击结束)
    recording: '录音中，点击结束',
    // 识别处理中提示
    transcribing: '识别中…',
    // 下载模型进度(带百分比参数)
    downloading: '下载模型 {percent}%',
    // 模型选择弹层标题
    pickTitle: '选择语音识别模型',
    // 下载源分组标签
    sourceLabel: '下载源',
    // 下载源选项：国内镜像
    sourceDomestic: '国内镜像',
    // 下载源选项：官方站点
    sourceOfficial: 'HuggingFace 官方',
    // 模型档位分组标签
    tierLabel: '模型档位',
    // 档位说明：tiny
    tierTiny: '轻量（最快，中文一般）',
    // 档位说明：base
    tierBase: '均衡（推荐）',
    // 档位说明：small
    tierSmall: '精准（最准，最大最慢）',
    // 弹层确认按钮
    confirm: '下载并使用',
    // 切换模型入口
    switch: '切换模型',
    // 错误文案
    errors: {
      // 麦克风权限被拒或非安全上下文
      permissionDenied: '无法访问麦克风，请检查浏览器权限',
      // 浏览器不支持所需能力
      notSupported: '当前浏览器不支持语音输入',
      // 未识别到有效语音
      noSpeech: '未识别到语音',
      // 模型下载失败
      downloadFailed: '模型下载失败，可切换下载源后重试',
      // 识别过程出错
      transcribeFailed: '语音识别失败，请重试',
    },
  },
} as const
