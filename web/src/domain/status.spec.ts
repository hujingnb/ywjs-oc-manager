// status.spec.ts 聚焦验证后端 init 状态机细化后的前端契约：
// 1) appStatusViews 对 4 个新 init 子状态 + 重命名后的 draft 键名是否对齐；
// 2) isInitPhase 是否准确划分出 worker 初始化期间需要渲染进度条的子状态集合。
// label 迁移为 i18n 键后，测试断言键名而非中文字面量。
// 既有通用映射（其它 status / Org / Member / Runtime 节点等）由 status.test.ts 覆盖，
// 此文件不与之重复，仅覆盖本 task 新增的契约面。
import { describe, expect, it } from 'vitest'

import { formatAppStatus, isInitPhase } from './status'

describe('formatAppStatus', () => {
  // 4 个 init 子状态 + draft 的 i18n 键，逐项验证 label 与 tone。
  it.each([
    ['draft',                 'domain.appStatus.draft',                 'neutral'],  // draft：待初始化，避免"草稿"歧义。
    ['pulling_runtime_image', 'domain.appStatus.pulling_runtime_image', 'warning'],  // 拉取阶段：通过 docker proxy 拉 hermes 镜像。
    ['preparing_runtime',     'domain.appStatus.preparing_runtime',     'warning'],  // 配置准备：生成 compose / env 等。
    ['creating_container',    'domain.appStatus.creating_container',    'warning'],  // 创建阶段：docker create 调用。
    ['starting',              'domain.appStatus.starting',              'warning'],  // 启动阶段：docker start，下一步进入 binding_waiting。
  ])('status=%s 映射为 label=%s / tone=%s', (status, label, tone) => {
    const view = formatAppStatus(status)
    expect(view.label).toBe(label)
    expect(view.tone).toBe(tone)
  })

  // 后端如灰度新增 status，返回降级键 + params，消费方可展示含原始值的降级文案。
  it('未知 status 走降级分支', () => {
    const view = formatAppStatus('weird_state')
    // 降级键固定为 domain.appStatus.unknown，消费方需传 params 以展示原始状态值。
    expect(view.label).toBe('domain.appStatus.unknown')
    expect(view.tone).toBe('warning')
    expect(view.params).toEqual({ status: 'weird_state' })
  })
})

describe('isInitPhase', () => {
  // 4 个 init 子状态都返回 true：AppOverviewTab 据此渲染进度条。
  it.each(['pulling_runtime_image', 'preparing_runtime', 'creating_container', 'starting'])(
    '%s 是 init 子状态',
    (status) => {
      expect(isInitPhase(status)).toBe(true)
    },
  )

  // 边界：draft / binding_waiting / running / error / deleted 都不算 init 子状态，
  // 这些状态下不需要进度条（draft 还没开始，binding_waiting 已离开 init 段）。
  it.each(['draft', 'binding_waiting', 'running', 'error', 'deleted'])(
    '%s 不是 init 子状态',
    (status) => {
      expect(isInitPhase(status)).toBe(false)
    },
  )
})
