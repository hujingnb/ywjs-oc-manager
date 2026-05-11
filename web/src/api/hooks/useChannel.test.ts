// 渠道认证纯函数测试覆盖挑战元数据还原和本地 pending 提示条件。
// 这些逻辑不依赖 Vue Query，可直接用同步断言验证状态边界。
import { describe, expect, it } from 'vitest'

import {
  channelChallengeFromProgress,
  shouldShowChallengePending,
  type ChannelProgress,
} from '@/api/hooks/useChannel'

describe('channelChallengeFromProgress', () => {
  it('builds qrcode challenge from pending progress metadata', () => {
    const progress: ChannelProgress = {
      status: 'pending_auth',
      updated_at: '2026-05-03T12:00:00Z',
      metadata: {
        type: 'qrcode',
        qrcode: 'https://liteapp.weixin.qq.com/q/test',
        expires_at: '2026-05-03T12:05:00Z',
      },
    }

    expect(channelChallengeFromProgress(progress, 'wechat')).toEqual({
      status: 'pending_auth',
      channel_type: 'wechat',
      challenge_type: 'qrcode',
      qrcode: 'https://liteapp.weixin.qq.com/q/test',
      code: undefined,
      expires_at: '2026-05-03T12:05:00Z',
    })
  })

  it('returns null when progress has no challenge payload', () => {
    expect(channelChallengeFromProgress({ status: 'failed', updated_at: '2026-05-03T12:00:00Z' }, 'wechat')).toBeNull()
  })
})

describe('shouldShowChallengePending', () => {
  it('shows pending hint while login job has started and no challenge exists yet', () => {
    expect(shouldShowChallengePending(true, null, 'pending_auth')).toBe(true)
  })

  it('hides pending hint after failed or bound terminal states', () => {
    expect(shouldShowChallengePending(true, null, 'failed')).toBe(false)
    expect(shouldShowChallengePending(true, null, 'bound')).toBe(false)
  })
})
