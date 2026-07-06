// login 模块文案（en）。与 zh/login.ts 结构完全一致。
export default {
  // 登录页标题
  heading: 'Console Login',
  // 企业标识字段
  orgCode: {
    label: 'Organization Code',
    placeholder: 'Enterprise users fill in; platform admins leave blank',
  },
  // 账号字段
  username: {
    label: 'Account',
  },
  // 密码字段
  password: {
    label: 'Password',
    placeholder: 'Enter your password',
    show: 'Show password',
    hide: 'Hide password',
  },
  // 验证码区域提示
  captchaHint: '🔄 Verifying…',
  // 登录按钮文案
  submit: 'Log in',
  submitting: 'Logging in…',
  // 登录失败兜底提示（后端未返回具体错误时使用）
  loginFailed: 'Login failed',
  // 表单底部两枚装饰性安全标语
  securityNote: 'Secure runtime access',
  controlNote: 'AI task control plane',
} as const
