// 渠道认证 hooks 负责封装应用渠道绑定进度、挑战生成和解绑操作。
// 渠道 UI 的展示状态由纯函数推导，便于在单元测试中覆盖异步进度边界。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import { i18n } from '@/i18n'

// ChannelChallenge 表示后端返回给用户完成渠道认证的一次性挑战。
export interface ChannelChallenge {
  // 挑战状态，通常与 ChannelProgress.status 对齐。
  status: string
  // 渠道类型，如 wechat。
  channel_type: string
  // 挑战类型，当前支持 qrcode / code，缺省时由 metadata 内容推断。
  challenge_type?: string
  // 二维码 URL 或内容。
  qrcode?: string
  // 短码登录场景的验证码。
  code?: string
  // 挑战过期时间，页面用于倒计时或过期提示。
  expires_at?: string
  // 后端返回的补充提示，不参与核心状态判断。
  hints?: Record<string, string>
  // 异步授权 job ID，存在时调用方可关联进度。
  job_id?: string
}

// ChannelProgress 表示渠道绑定的当前进度快照。
export interface ChannelProgress {
  // 绑定状态，终态包括 bound / failed / expired。
  status: string
  // 已绑定账号标识，绑定成功后用于展示。
  bound_identity?: string
  // 渠道显示名。
  channel_name?: string
  // 失败或过期原因，页面可直接呈现。
  error_message?: string
  // 进度更新时间。
  updated_at: string
  // 挑战元数据，后端可能在进度接口里带回 qrcode/code。
  metadata?: Record<string, string>
}

// CHANNEL_STATUS_KEYS 将后端渠道状态机原值映射为 apps.channels.status 命名空间下的 i18n key。
// 未知状态由 formatChannelStatus 回退到 apps.channels.status.unknown（带 {status} 插值），
// 方便后端新增状态时前端及时暴露差异。
const CHANNEL_STATUS_KEYS: Record<string, string> = {
  unbound: 'apps.channels.status.unbound',
  pending_auth: 'apps.channels.status.pending_auth',
  bound: 'apps.channels.status.bound',
  failed: 'apps.channels.status.failed',
  expired: 'apps.channels.status.expired',
  unbound_by_user: 'apps.channels.status.unbound_by_user',
  deleted: 'apps.channels.status.deleted',
}

// formatChannelStatus 将渠道绑定状态转成用户可读文案；空值表示轮询尚未返回或尚未发起登录。
// 内部使用 i18n 单例翻译，使该纯函数无需额外参数即可在模板和 computed 中复用。
export function formatChannelStatus(status?: string): string {
  const t = i18n.global.t
  if (!status) return t('apps.channels.status.not_started')
  const key = CHANNEL_STATUS_KEYS[status]
  if (key) return t(key)
  return t('apps.channels.status.unknown', { status })
}

// channelChallengeFromProgress 从进度 metadata 中还原可展示挑战。
// 只有 qrcode 或 code 至少存在一个时才返回挑战，避免把普通进度误判成待扫码。
export function channelChallengeFromProgress(progress: ChannelProgress | null | undefined, channelType: string): ChannelChallenge | null {
  const metadata = progress?.metadata
  if (!metadata?.qrcode && !metadata?.code) return null
  return {
    status: progress?.status ?? 'pending_auth',
    channel_type: channelType,
    challenge_type: metadata.type ?? (metadata.qrcode ? 'qrcode' : 'code'),
    qrcode: metadata.qrcode,
    code: metadata.code,
    expires_at: metadata.expires_at,
  }
}

// shouldShowChallengePending 判断是否展示“挑战生成中”提示。
// 调用方需传入本地 authStarted，因为后端轮询首次返回前没有足够信息区分未开始和生成中。
export function shouldShowChallengePending(authStarted: boolean, visibleChallenge: ChannelChallenge | null, status?: string) {
  return authStarted && !visibleChallenge && status !== 'bound' && status !== 'failed' && status !== 'expired'
}

const progressKey = (appId: string | undefined, channelType: string | undefined) =>
  ['channel-progress', appId, channelType] as const

// useChannelProgressQuery 查询当前渠道登录进度。
// 默认 4 秒刷新一次，前端可以叠加显式触发挑战。
// appId 或 channelType 缺失时暂停请求，防止详情页切换过程中打到错误路径。
export function useChannelProgressQuery(appId: Ref<string | undefined>, channelType: Ref<string | undefined>) {
  return useQuery<ChannelProgress | null>({
    queryKey: ['channel-progress', appId, channelType],
    enabled: () => Boolean(appId.value && channelType.value),
    refetchInterval: 4000,
    queryFn: async () => {
      if (!appId.value || !channelType.value) return null
      const response = await apiRequest<{ progress: ChannelProgress }>(
        `/api/v1/apps/${appId.value}/channels/${channelType.value}/auth`,
      )
      return response.progress
    },
  })
}

// useBeginChannelAuth 触发挑战。
// 成功后仅失效当前渠道进度缓存，挑战内容由 mutation 返回值立即展示。
export function useBeginChannelAuth(appId: Ref<string | undefined>, channelType: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!appId.value || !channelType.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/${channelType.value}/auth`,
        { method: 'POST' },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, channelType.value) })
    },
  })
}

// FeishuAuthBody 描述飞书发起绑定的请求体。
// 仅扫码自动创建一种模式，只需携带部署域 domain，由 worker 异步建应用并回写二维码。
export interface FeishuAuthBody {
  // 部署域：feishu（国内）/ lark（国际），决定后端调用的开放平台 endpoint。
  domain: string
}

// useBeginFeishuAuth 触发飞书渠道扫码绑定，区别于通用 useBeginChannelAuth 在于发起需携带 domain body。
// 复用通用进度轮询（GET /channels/feishu/auth）与解绑接口，仅发起入口不同。
// 成功后失效飞书进度缓存，让轮询尽快拉到扫码二维码。
export function useBeginFeishuAuth(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (body: FeishuAuthBody) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      // apiRequest 接收原始对象 body 并在内部 JSON 序列化、补 Content-Type，故此处直接透传 body。
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/feishu/auth`,
        { method: 'POST', body },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, 'feishu') })
    },
  })
}

// WorkWechatAuthBody 描述企业微信发起绑定的请求体（手填智能机器人凭证）。
export interface WorkWechatAuthBody {
  // 企业微信智能机器人 Bot ID。
  bot_id: string
  // 机器人 Secret（仅提交，不回显）。
  secret: string
}

// useBeginWorkWechatAuth 触发企业微信手填绑定，发起需携带 bot_id+secret body。
// 复用通用进度轮询（GET /channels/work_wechat/auth）与解绑接口，仅发起入口不同。
// 成功后失效企业微信进度缓存，让轮询尽快拉到连通态。
export function useBeginWorkWechatAuth(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (body: WorkWechatAuthBody) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      // apiRequest 接收原始对象 body 并在内部 JSON 序列化、补 Content-Type，故此处直接透传 body。
      const response = await apiRequest<{ challenge: ChannelChallenge }>(
        `/api/v1/apps/${appId.value}/channels/work_wechat/auth`,
        { method: 'POST', body },
      )
      return response.challenge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, 'work_wechat') })
    },
  })
}

// useUnbindChannel 解绑渠道。
// 解绑成功后刷新进度缓存，让页面回到未绑定状态。
export function useUnbindChannel(appId: Ref<string | undefined>, channelType: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!appId.value || !channelType.value) throw new Error(i18n.global.t('common.errors.missingChannelParam'))
      await apiRequest<void>(`/api/v1/apps/${appId.value}/channels/${channelType.value}/unbind`, {
        method: 'POST',
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, channelType.value) })
    },
  })
}
