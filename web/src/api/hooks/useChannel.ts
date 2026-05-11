// 渠道认证 hooks 负责封装应用渠道绑定进度、挑战生成和解绑操作。
// 渠道 UI 的展示状态由纯函数推导，便于在单元测试中覆盖异步进度边界。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

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
      if (!appId.value || !channelType.value) throw new Error('缺少应用或渠道类型')
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

// useUnbindChannel 解绑渠道。
// 解绑成功后刷新进度缓存，让页面回到未绑定状态。
export function useUnbindChannel(appId: Ref<string | undefined>, channelType: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!appId.value || !channelType.value) throw new Error('缺少应用或渠道类型')
      await apiRequest<void>(`/api/v1/apps/${appId.value}/channels/${channelType.value}/unbind`, {
        method: 'POST',
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: progressKey(appId.value, channelType.value) })
    },
  })
}
