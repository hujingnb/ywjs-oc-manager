// deliverOptions.spec.ts —— deliver 下拉选项构建纯逻辑单测。
// 覆盖：不投递常驻、仅已绑定渠道入选项、编辑态未知值保留不丢。
// 使用真实 i18n 实例（zh 语言），断言中文标签输出与原有文案一致。
import { describe, expect, it } from 'vitest'

import { i18n } from '@/i18n'

import { buildDeliverOptions } from './deliverOptions'

// 使用真实 i18n 实例的 t 函数，设为中文以断言中文输出。
i18n.global.locale.value = 'zh'
const t = i18n.global.t

describe('buildDeliverOptions', () => {
  // 无已绑定渠道：只有「不投递」。
  it('无绑定渠道仅不投递', () => {
    expect(buildDeliverOptions([], '', t)).toEqual([{ label: '不投递', value: '' }])
  })

  // 已绑定 wechat：追加中文渠道项。
  it('已绑定渠道入选项', () => {
    expect(buildDeliverOptions(['wechat'], '', t)).toEqual([
      { label: '不投递', value: '' },
      { label: '微信', value: 'wechat' },
    ])
  })

  // 编辑态当前值不在已绑定列表：保留为额外项，避免回填丢值。
  it('保留未知的当前值', () => {
    const opts = buildDeliverOptions([], 'email', t)
    expect(opts).toContainEqual({ label: '邮件', value: 'email' })
  })

  // 当前值已在已绑定列表：不重复追加。
  it('当前值已存在不重复', () => {
    expect(buildDeliverOptions(['wechat'], 'wechat', t)).toHaveLength(2)
  })
})
