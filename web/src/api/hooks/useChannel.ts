import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

export interface ChannelChallenge {
  status: string
  channel_type: string
  challenge_type?: string
  qrcode?: string
  code?: string
  expires_at?: string
  hints?: Record<string, string>
  job_id?: string
}

export interface ChannelProgress {
  status: string
  bound_identity?: string
  channel_name?: string
  error_message?: string
  updated_at: string
  metadata?: Record<string, string>
}

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

export function shouldShowChallengePending(authStarted: boolean, visibleChallenge: ChannelChallenge | null, status?: string) {
  return authStarted && !visibleChallenge && status !== 'bound' && status !== 'failed' && status !== 'expired'
}

const progressKey = (appId: string | undefined, channelType: string | undefined) =>
  ['channel-progress', appId, channelType] as const

// useChannelProgressQuery 查询当前渠道登录进度。
// 默认 4 秒刷新一次，前端可以叠加显式触发挑战。
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
