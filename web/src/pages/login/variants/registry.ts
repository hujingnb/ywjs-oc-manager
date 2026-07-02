// registry.ts — 登录页按域名选变体的注册表与 chrome 副作用。
// 机制：LoginHost 用 window.location.hostname 经 resolveVariant 精确匹配到变体组件；
// 未命中回默认变体。域名不绑组织，仅决定登录页外观；新增白标在 VARIANTS 登记一行。
import type { Component } from 'vue'

import DefaultLoginPage from './default/DefaultLoginPage.vue'

// LoginVariant 描述一个域名对应的登录页变体：整页组件 + 可选的页面标题/favicon 覆盖。
export type LoginVariant = {
  // component：整页登录组件（布局自由，须按 useLogin 契约接线登录行为）。
  component: Component
  // documentTitle：可选，覆盖浏览器标签标题；缺省不改动。
  documentTitle?: string
  // faviconHref：可选，覆盖站点 favicon；缺省不改动。
  faviconHref?: string
}

// DEFAULT_VARIANT：默认变体，ai.ywjs.com / ocm.localhost / 任何未命中 hostname 都用它。
export const DEFAULT_VARIANT: LoginVariant = { component: DefaultLoginPage }

// VARIANTS：精确 hostname → 变体映射。首期为空，新增白标域名在此登记一行。
export const VARIANTS: Record<string, LoginVariant> = {}

// resolveVariant 按精确 hostname 命中变体，未命中回默认。
// variants 参数默认取全局 VARIANTS，仅为便于单测注入 fixture。
export function resolveVariant(
  hostname: string,
  variants: Record<string, LoginVariant> = VARIANTS,
): LoginVariant {
  return variants[hostname] ?? DEFAULT_VARIANT
}

// applyVariantChrome 应用变体的标题/favicon 副作用；无覆盖项则不改动。
// doc 参数默认取全局 document，仅为便于单测注入。
export function applyVariantChrome(variant: LoginVariant, doc: Document = document): void {
  if (variant.documentTitle) {
    doc.title = variant.documentTitle
  }
  if (variant.faviconHref) {
    // 复用已有的 rel=icon 链接，缺失时创建，避免重复插入多个图标链接。
    let link = doc.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!link) {
      link = doc.createElement('link')
      link.rel = 'icon'
      doc.head.appendChild(link)
    }
    link.setAttribute('href', variant.faviconHref)
  }
}
