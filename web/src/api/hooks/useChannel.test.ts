// 渠道认证纯函数测试覆盖挑战元数据还原和本地 pending 提示条件。
// 这些逻辑不依赖 Vue Query，可直接用同步断言验证状态边界。
import { describe, expect, it } from 'vitest'

import {
  channelChallengeFromProgress,
  formatChannelStatus,
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

describe('formatChannelStatus', () => {
  // 常见渠道状态：后端原值应映射为用户能理解的中文业务文案。
  it.each([
    ['unbound', '未绑定'], // 初始渠道记录：还没有绑定账号。
    ['pending_auth', '等待扫码授权'], // 登录任务已发起，等待二维码扫码或确认。
    ['bound', '已绑定'], // 用户反馈的核心场景：不能直接展示 bound。
    ['failed', '绑定失败'], // worker 或渠道侧返回失败。
    ['expired', '二维码已过期'], // 当前登录二维码已经过期。
    ['unbound_by_user', '已解绑'], // 用户主动解绑后的状态。
    ['deleted', '已删除'], // 渠道记录被删除后的兜底展示。
  ])('status=%s 映射为 %s', (status, label) => {
    expect(formatChannelStatus(status)).toBe(label)
  })

  // 空进度：轮询尚未返回数据时展示“未发起”，不展示 undefined。
  it('status 为空时展示未发起', () => {
    expect(formatChannelStatus(undefined)).toBe('未发起')
  })

  // 未知状态：保留原值帮助发现后端新增状态，同时用中文前缀解释异常。
  it('未知 status 保留原值并加中文前缀', () => {
    expect(formatChannelStatus('new_state')).toBe('未知状态：new_state')
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
