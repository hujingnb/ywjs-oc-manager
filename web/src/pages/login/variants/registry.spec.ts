// registry.spec.ts — 登录变体注册表单测。
// 覆盖：精确 hostname 命中、未命中回默认、title/favicon 副作用应用与缺省不改动。
import type { Component } from 'vue'
import { beforeEach, describe, expect, it } from 'vitest'

import { DEFAULT_VARIANT, applyVariantChrome, resolveVariant } from './registry'

describe('resolveVariant', () => {
  // 精确 hostname 命中返回对应变体。
  it('精确 hostname 命中', () => {
    const fake = { component: {} as Component, documentTitle: 'ABC' }
    const map = { 'abc.example.com': fake }
    expect(resolveVariant('abc.example.com', map)).toBe(fake)
  })

  // 未命中任何 hostname 时回默认变体。
  it('未命中回默认', () => {
    expect(resolveVariant('nope.example.com', {})).toBe(DEFAULT_VARIANT)
  })
})

describe('applyVariantChrome', () => {
  beforeEach(() => {
    // 每例前清理可能残留的图标链接，避免相互污染。
    document.querySelectorAll('link[rel="icon"]').forEach((n) => n.remove())
  })

  // 有覆盖项时设置 document.title 并写入 favicon 链接。
  it('设置标题与 favicon', () => {
    applyVariantChrome({
      component: {} as Component,
      documentTitle: 'ACME',
      faviconHref: '/acme.ico',
    })
    expect(document.title).toBe('ACME')
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link).not.toBeNull()
    expect(link!.getAttribute('href')).toBe('/acme.ico')
  })

  // 无覆盖项时不改动现有标题。
  it('无覆盖项时不改动标题', () => {
    document.title = 'KEEP'
    applyVariantChrome({ component: {} as Component })
    expect(document.title).toBe('KEEP')
  })
})
