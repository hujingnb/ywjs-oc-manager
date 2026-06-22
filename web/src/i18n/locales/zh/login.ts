// login 模块文案（zh）。登录页所有用户可见文案的中文原文。
export default {
  // 登录页标题
  heading: '登录控制台',
  // 企业标识字段
  orgCode: {
    label: '企业标识',
    placeholder: '企业用户填写，平台管理员留空',
  },
  // 账号字段
  username: {
    label: '账号',
  },
  // 密码字段
  password: {
    label: '密码',
    placeholder: '请输入密码',
    show: '显示密码',
    hide: '隐藏密码',
  },
  // 验证码区域提示
  captchaHint: '🔄 人机校验中…',
  // 登录按钮文案
  submit: '登录',
  submitting: '登录中…',
  // 登录失败兜底提示（后端未返回具体错误时使用）
  loginFailed: '登录失败',
} as const
