# 渠道扩展、全彩 Logo 与状态合并 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在实例渠道 tab 把预告渠道从 4 个扩到 9 个、用全彩官方品牌内联 SVG 替换汉字 logo、并把详情区头部「当前状态」并入「当前渠道」行，纯前端、不动后端。

**Architecture:** 新增 `ChannelLogo.vue` 内联 SVG 组件，按渠道 `type` 渲染对应品牌 logo（全彩源直接展示，单色源按品牌色着色，未支持渠道经 `grayscale(1)` 灰度化）。`AppChannelsTab.vue` 的 `channels` 数组扩为 9 项、列表与详情区改用 `ChannelLogo`、头部合并为「渠道名 · 状态」单行。同步更新单元测试断言。

**Tech Stack:** Vue 3 (`<script setup lang="ts">`) + naive-ui + Vitest + @vue/test-utils。所有命令在 `web/` 目录下执行。

**设计依据:** [`docs/superpowers/specs/2026-05-28-channel-ui-expand-logo-status-design.md`](../specs/2026-05-28-channel-ui-expand-logo-status-design.md)

---

## 文件结构

- **新建** `web/src/pages/apps/ChannelLogo.vue` — 渠道 logo 展示组件：按 `type` 渲染内联 SVG，承载 logo 方块样式（尺寸、浅色底、圆角、灰度）。
- **新建** `web/src/pages/apps/ChannelLogo.spec.ts` — `ChannelLogo` 单元测试。
- **修改** `web/src/pages/apps/AppChannelsTab.vue` — 扩展 `channels` 到 9 项、列表/详情区改用 `ChannelLogo`、头部合并状态行、移除迁移到 `ChannelLogo` 的旧 logo CSS。
- **修改** `web/src/pages/apps/AppChannelsTab.spec.ts` — 渠道数量、未支持数量、logo 选择器、头部状态文案断言同步。

> **logo 来源说明（已落实，无需再取）：** 计划中各 SVG 已从 Iconify 取定——telegram/whatsapp/discord/slack 用全彩 `logos:` 集，wechat=`tdesign:logo-wechat`、work_wechat=`tdesign:logo-wecom-filled`、dingtalk=`mingcute:dingtalk-fill`、line=`simple-icons:line`、feishu=`icon-park-outline:lark`（均为 `currentColor` 单色，按品牌色着色；未支持渠道反正灰度）。直接使用 Task 1 中给出的 SVG 字符串即可，不要再联网拉取。

---

### Task 1: 新建 ChannelLogo 组件

**Files:**
- Create: `web/src/pages/apps/ChannelLogo.vue`
- Test: `web/src/pages/apps/ChannelLogo.spec.ts`

- [ ] **Step 1: 写失败测试**

创建 `web/src/pages/apps/ChannelLogo.spec.ts`：

```ts
import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'

import ChannelLogo from './ChannelLogo.vue'

describe('ChannelLogo', () => {
  // 指定渠道：根据 type 输出 channel-logo 与 channel-logo--{type} 钩子，并内联 SVG
  it('渲染对应渠道的 class 钩子与内联 SVG', () => {
    const wrapper = mount(ChannelLogo, { props: { type: 'wechat' } })
    expect(wrapper.classes()).toContain('channel-logo')
    expect(wrapper.classes()).toContain('channel-logo--wechat')
    expect(wrapper.html()).toContain('<svg')
  })

  // 未支持渠道：muted=true 时附加灰度 class，并仍渲染 SVG
  it('muted 时附加 muted class', () => {
    const wrapper = mount(ChannelLogo, { props: { type: 'telegram', muted: true } })
    expect(wrapper.classes()).toContain('muted')
    expect(wrapper.html()).toContain('<svg')
  })

  // 详情区大尺寸：large=true 时附加 large class
  it('large 时附加 large class', () => {
    const wrapper = mount(ChannelLogo, { props: { type: 'wechat', large: true } })
    expect(wrapper.classes()).toContain('large')
  })

  // 全部 9 个渠道都必须有 SVG 映射，防止扩展时漏配某个 type
  it('全部渠道 type 均能渲染 SVG', () => {
    const types = [
      'wechat', 'work_wechat', 'feishu', 'dingtalk',
      'telegram', 'whatsapp', 'discord', 'slack', 'line',
    ] as const
    for (const type of types) {
      const wrapper = mount(ChannelLogo, { props: { type } })
      expect(wrapper.html()).toContain('<svg') // 每个 type 都有内联 SVG
    }
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/apps/ChannelLogo.spec.ts`
Expected: FAIL —— 提示无法解析 `./ChannelLogo.vue`（文件尚未创建）。

- [ ] **Step 3: 实现 ChannelLogo.vue**

创建 `web/src/pages/apps/ChannelLogo.vue`（SVG 字符串为已取定的真实品牌 logo，逐字粘贴）：

```vue
<template>
  <!-- 渠道 logo 方块：v-html 注入内联品牌 SVG；color 用于 currentColor 单色源着色，
       已 baked 颜色的全彩 SVG（telegram/whatsapp/discord/slack）会忽略 color。 -->
  <span
    class="channel-logo"
    :class="[`channel-logo--${type}`, { large, muted }]"
    :style="{ color: brandColor }"
    aria-hidden="true"
    v-html="svg"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'

// ChannelLogoType 与 AppChannelsTab 的渠道 type 联合保持一致；新增渠道时两处同步扩展。
export type ChannelLogoType =
  | 'wechat'
  | 'work_wechat'
  | 'feishu'
  | 'dingtalk'
  | 'telegram'
  | 'whatsapp'
  | 'discord'
  | 'slack'
  | 'line'

// large 用于详情区头部（44px），muted 用于未支持渠道（灰度）。
const props = defineProps<{ type: ChannelLogoType; large?: boolean; muted?: boolean }>()

// channelLogos 为各渠道内联品牌 SVG。telegram/whatsapp/discord/slack 为全彩（baked fill）；
// 其余为 currentColor 单色源，由 brandColors 着色。SVG 均来自 Iconify（见计划文件结构说明）。
const channelLogos: Record<ChannelLogoType, string> = {
  // tdesign:logo-wechat —— currentColor 单色，由 brandColors.wechat 着微信绿
  wechat:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M8.796 17.027H8.75c-1.153 0-2.254-.188-3.262-.53L2.65 17.92l.352-2.712C1.162 13.855 0 11.861 0 9.64c0-4.083 3.918-7.39 8.75-7.39c4.174 0 7.665 2.468 8.54 5.77a9 9 0 0 0-.6-.02c-4.364 0-8.19 3.037-8.19 7.11c0 .67.104 1.312.296 1.917M6 8a1 1 0 1 0 0-2a1 1 0 0 0 0 2m5.5.007a1 1 0 1 0 0-2a1 1 0 0 0 0 2"/><path fill="currentColor" d="M21.874 19.52C23.187 18.405 24 16.863 24 15.16C24 11.758 20.754 9 16.75 9S9.5 11.758 9.5 15.161s3.246 6.161 7.25 6.161c.95 0 1.856-.155 2.686-.437l2.438 1.407zm-7.564-5.362a1 1 0 1 1 0-2a1 1 0 0 1 0 2m4.88 0a1 1 0 1 1 0-2a1 1 0 0 1 0 2"/></svg>',
  // tdesign:logo-wecom-filled —— 企业微信，currentColor 单色
  work_wechat:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M12 1c6.075 0 11 4.925 11 11s-4.925 11-11 11S1 18.075 1 12S5.925 1 12 1m3.52 15.49a.35.35 0 0 0-.24.1c-.14.13-.16.34.02.53l.07.07c.44.44.74.99.85 1.57c0 .02.04.23.04.23c.05.19.15.37.29.5c.21.21.51.34.82.34c.3 0 .59-.12.8-.33c.44-.44.44-1.16 0-1.61c-.15-.15-.34-.26-.53-.3l-.15-.03c-.61-.11-1.17-.41-1.62-.86c-.03-.03-.07-.07-.1-.11c-.06-.074-.16-.1-.25-.1M11 4.75c-2.117 0-4.264.77-5.75 2.31C4.111 8.246 3.5 9.72 3.5 11.24c0 1.06.3 2.12.88 3.06c.47.695.993 1.371 1.66 1.89l-.384 1.624a.6.6 0 0 0 .856.673L8.64 17.41c.53.166 1.08.234 1.63.3a8.3 8.3 0 0 0 1.7-.03l.38-.05q.283-.046.564-.112a2.33 2.33 0 0 1-.92-1.605l-.254.037c-.62.067-1.232.03-1.85-.04c-.43-.057-.838-.185-1.25-.31l-1.02.5l.23-.67l-.74-.6c-.513-.401-.917-.934-1.28-1.47c-.4-.65-.61-1.38-.61-2.11c0-1.08.456-2.119 1.26-2.97c1.158-1.198 2.854-1.78 4.5-1.78c1.54 0 3.108.513 4.24 1.58c.365.365.707.75.95 1.21c.177.354.338.722.424 1.107a2.34 2.34 0 0 1 1.811.123c-.075-.716-.33-1.4-.665-2.04c-.329-.62-.776-1.155-1.27-1.65c-1.468-1.38-3.471-2.08-5.47-2.08m9.37 9.77a1.136 1.136 0 0 0-1.1.86l-.03.15a3.1 3.1 0 0 1-.86 1.63c-.04.03-.07.07-.11.1c-.14.13-.14.35 0 .49c.07.06.17.1.26.1h.01c.07 0 .15-.02.26-.13l.07-.07c.44-.44.99-.74 1.57-.85c.023 0 .227-.04.23-.04c.2-.06.37-.16.5-.3c.44-.44.44-1.17 0-1.61c-.21-.21-.5-.33-.8-.33m-4.21-1.07c-.08 0-.16.03-.27.14l-.07.07c-.44.44-.99.74-1.57.85c-.02 0-.23.04-.23.04c-.2.06-.37.16-.5.3c-.44.44-.44 1.17 0 1.61c.21.21.51.34.82.34c.3 0 .59-.12.8-.33c.15-.16.25-.34.29-.53a.4.4 0 0 0 .03-.16c.11-.61.41-1.18.86-1.63c.03-.03.06-.06.1-.09c.146-.115.13-.36 0-.49a.34.34 0 0 0-.26-.12m1.18-1.97c-.3 0-.59.12-.8.33c-.44.44-.44 1.16 0 1.61c.15.15.34.26.53.3c.054.006.144.029.15.03c.61.12 1.17.41 1.62.86c.03.03.07.07.1.11c.08.08.16.1.25.1c.1 0 .16-.04.23-.11c.12-.13.14-.32-.02-.52l-.08-.08c-.44-.44-.74-.99-.85-1.57c0-.02-.04-.23-.04-.23c-.05-.19-.15-.37-.29-.5c-.21-.21-.5-.33-.8-.33"/></svg>',
  // icon-park-outline:lark —— 飞书 / Lark，currentColor 单色
  feishu:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><path fill="currentColor" fill-rule="evenodd" d="M41.072 5.994L3.31 16.52l9.075 9.294l8.414.146l9.683-9.44q-.384-.787-.384-1.318c0-.794.311-1.422.796-1.868q1.244-1.145 2.994-.342zm1.03.734L31.578 44.49l-9.294-9.075L22.137 27l9.375-9.518a2.54 2.54 0 0 0 1.664.495c.902-.05 1.485-.596 1.759-.917a2.35 2.35 0 0 0 .567-1.649a2.57 2.57 0 0 0-.52-1.464z" clip-rule="evenodd"/></svg>',
  // mingcute:dingtalk-fill —— 钉钉，currentColor 单色（取 fill 路径）
  dingtalk:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M6.802 2.02a1 1 0 0 1 .849.22l9.751 8.359a2 2 0 0 1 .235 2.799l-1.06 1.272l.87.436a1 1 0 0 1 .134 1.708l-7 5a1 1 0 0 1-1.539-1.101l1.21-4.034c-2.363-.9-3.747-3.055-4.233-5.483A1 1 0 0 1 7.01 10c-.474-.703-.86-1.42-1.134-2.149c-.649-1.73-.658-3.523.23-5.298a1 1 0 0 1 .696-.533"/></svg>',
  // logos:telegram —— 全彩（baked fill + 渐变）
  telegram:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256"><defs><linearGradient id="chLogoTg" x1="50%" x2="50%" y1="0%" y2="100%"><stop offset="0%" stop-color="#2aabee"/><stop offset="100%" stop-color="#229ed9"/></linearGradient></defs><path fill="url(#chLogoTg)" d="M128 0C94.06 0 61.48 13.494 37.5 37.49A128.04 128.04 0 0 0 0 128c0 33.934 13.5 66.514 37.5 90.51C61.48 242.506 94.06 256 128 256s66.52-13.494 90.5-37.49c24-23.996 37.5-56.576 37.5-90.51s-13.5-66.514-37.5-90.51C194.52 13.494 161.94 0 128 0"/><path fill="#fff" d="M57.94 126.648q55.98-24.384 74.64-32.152c35.56-14.786 42.94-17.354 47.76-17.441c1.06-.017 3.42.245 4.96 1.49c1.28 1.05 1.64 2.47 1.82 3.467c.16.996.38 3.266.2 5.038c-1.92 20.24-10.26 69.356-14.5 92.026c-1.78 9.592-5.32 12.808-8.74 13.122c-7.44.684-13.08-4.912-20.28-9.63c-11.26-7.386-17.62-11.982-28.56-19.188c-12.64-8.328-4.44-12.906 2.76-20.386c1.88-1.958 34.64-31.748 35.26-34.45c.08-.338.16-1.598-.6-2.262c-.74-.666-1.84-.438-2.64-.258c-1.14.256-19.12 12.152-54 35.686c-5.1 3.508-9.72 5.218-13.88 5.128c-4.56-.098-13.36-2.584-19.9-4.708c-8-2.606-14.38-3.984-13.82-8.41c.28-2.304 3.46-4.662 9.52-7.072"/></svg>',
  // logos:whatsapp-icon —— 全彩（baked fill + 渐变）
  whatsapp:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 258"><defs><linearGradient id="chLogoWa1" x1="50%" x2="50%" y1="100%" y2="0%"><stop offset="0%" stop-color="#1faf38"/><stop offset="100%" stop-color="#60d669"/></linearGradient><linearGradient id="chLogoWa2" x1="50%" x2="50%" y1="100%" y2="0%"><stop offset="0%" stop-color="#f9f9f9"/><stop offset="100%" stop-color="#fff"/></linearGradient></defs><path fill="url(#chLogoWa1)" d="M5.463 127.456c-.006 21.677 5.658 42.843 16.428 61.499L4.433 252.697l65.232-17.104a123 123 0 0 0 58.8 14.97h.054c67.815 0 123.018-55.183 123.047-123.01c.013-32.867-12.775-63.773-36.009-87.025c-23.23-23.25-54.125-36.061-87.043-36.076c-67.823 0-123.022 55.18-123.05 123.004"/><path fill="url(#chLogoWa2)" d="M1.07 127.416c-.007 22.457 5.86 44.38 17.014 63.704L0 257.147l67.571-17.717c18.618 10.151 39.58 15.503 60.91 15.511h.055c70.248 0 127.434-57.168 127.464-127.423c.012-34.048-13.236-66.065-37.3-90.15C194.633 13.286 162.633.014 128.536 0C58.276 0 1.099 57.16 1.071 127.416m40.24 60.376l-2.523-4.005c-10.606-16.864-16.204-36.352-16.196-56.363C22.614 69.029 70.138 21.52 128.576 21.52c28.3.012 54.896 11.044 74.9 31.06c20.003 20.018 31.01 46.628 31.003 74.93c-.026 58.395-47.551 105.91-105.943 105.91h-.042c-19.013-.01-37.66-5.116-53.922-14.765l-3.87-2.295l-40.098 10.513z"/><path fill="#fff" d="M96.678 74.148c-2.386-5.303-4.897-5.41-7.166-5.503c-1.858-.08-3.982-.074-6.104-.074c-2.124 0-5.575.799-8.492 3.984c-2.92 3.188-11.148 10.892-11.148 26.561s11.413 30.813 13.004 32.94c1.593 2.123 22.033 35.307 54.405 48.073c26.904 10.609 32.379 8.499 38.218 7.967c5.84-.53 18.844-7.702 21.497-15.139c2.655-7.436 2.655-13.81 1.859-15.142c-.796-1.327-2.92-2.124-6.105-3.716s-18.844-9.298-21.763-10.361c-2.92-1.062-5.043-1.592-7.167 1.597c-2.124 3.184-8.223 10.356-10.082 12.48c-1.857 2.129-3.716 2.394-6.9.801c-3.187-1.598-13.444-4.957-25.613-15.806c-9.468-8.442-15.86-18.867-17.718-22.056c-1.858-3.184-.199-4.91 1.398-6.497c1.431-1.427 3.186-3.719 4.78-5.578c1.588-1.86 2.118-3.187 3.18-5.311c1.063-2.126.531-3.986-.264-5.579c-.798-1.593-6.987-17.343-9.819-23.64"/></svg>',
  // logos:discord-icon —— 全彩（baked #5865f2，viewBox 非正方形 256x199）
  discord:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 199"><path fill="#5865f2" d="M216.856 16.597A208.5 208.5 0 0 0 164.042 0c-2.275 4.113-4.933 9.645-6.766 14.046q-29.538-4.442-58.533 0c-1.832-4.4-4.55-9.933-6.846-14.046a207.8 207.8 0 0 0-52.855 16.638C5.618 67.147-3.443 116.4 1.087 164.956c22.169 16.555 43.653 26.612 64.775 33.193A161 161 0 0 0 79.735 175.3a136.4 136.4 0 0 1-21.846-10.632a109 109 0 0 0 5.356-4.237c42.122 19.702 87.89 19.702 129.51 0a132 132 0 0 0 5.355 4.237a136 136 0 0 1-21.886 10.653c4.006 8.02 8.638 15.67 13.873 22.848c21.142-6.58 42.646-16.637 64.815-33.213c5.316-56.288-9.08-105.09-38.056-148.36M85.474 135.095c-12.645 0-23.015-11.805-23.015-26.18s10.149-26.2 23.015-26.2s23.236 11.804 23.015 26.2c.02 14.375-10.148 26.18-23.015 26.18m85.051 0c-12.645 0-23.014-11.805-23.014-26.18s10.148-26.2 23.014-26.2c12.867 0 23.236 11.804 23.015 26.2c0 14.375-10.148 26.18-23.015 26.18"/></svg>',
  // logos:slack-icon —— 全彩（baked 四色）
  slack:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256"><path fill="#e01e5a" d="M53.841 161.32c0 14.832-11.987 26.82-26.819 26.82S.203 176.152.203 161.32c0-14.831 11.987-26.818 26.82-26.818H53.84zm13.41 0c0-14.831 11.987-26.818 26.819-26.818s26.819 11.987 26.819 26.819v67.047c0 14.832-11.987 26.82-26.82 26.82c-14.83 0-26.818-11.988-26.818-26.82z"/><path fill="#36c5f0" d="M94.07 53.638c-14.832 0-26.82-11.987-26.82-26.819S79.239 0 94.07 0s26.819 11.987 26.819 26.819v26.82zm0 13.613c14.832 0 26.819 11.987 26.819 26.819s-11.987 26.819-26.82 26.819H26.82C11.987 120.889 0 108.902 0 94.069c0-14.83 11.987-26.818 26.819-26.818z"/><path fill="#2eb67d" d="M201.55 94.07c0-14.832 11.987-26.82 26.818-26.82s26.82 11.988 26.82 26.82s-11.988 26.819-26.82 26.819H201.55zm-13.41 0c0 14.832-11.988 26.819-26.82 26.819c-14.831 0-26.818-11.987-26.818-26.82V26.82C134.502 11.987 146.489 0 161.32 0s26.819 11.987 26.819 26.819z"/><path fill="#ecb22e" d="M161.32 201.55c14.832 0 26.82 11.987 26.82 26.818s-11.988 26.82-26.82 26.82c-14.831 0-26.818-11.988-26.818-26.82V201.55zm0-13.41c-14.831 0-26.818-11.988-26.818-26.82c0-14.831 11.987-26.818 26.819-26.818h67.25c14.832 0 26.82 11.987 26.82 26.819s-11.988 26.819-26.82 26.819z"/></svg>',
  // simple-icons:line —— currentColor 单色，由 brandColors.line 着 LINE 绿
  line:
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M19.365 9.863a.631.631 0 0 1 0 1.261H17.61v1.125h1.755a.63.63 0 1 1 0 1.259h-2.386a.63.63 0 0 1-.627-.629V8.108c0-.345.282-.63.63-.63h2.386a.63.63 0 0 1-.003 1.26H17.61v1.125zm-3.855 3.016a.63.63 0 0 1-.631.627a.62.62 0 0 1-.51-.25l-2.443-3.317v2.94a.63.63 0 0 1-1.257 0V8.108a.627.627 0 0 1 .624-.628c.195 0 .375.104.495.254l2.462 3.33V8.108c0-.345.282-.63.63-.63c.345 0 .63.285.63.63zm-5.741 0a.63.63 0 0 1-.631.629a.63.63 0 0 1-.627-.629V8.108c0-.345.282-.63.63-.63c.346 0 .628.285.628.63zm-2.466.629H4.917a.634.634 0 0 1-.63-.629V8.108c0-.345.285-.63.63-.63c.348 0 .63.285.63.63v4.141h1.756a.63.63 0 0 1 0 1.259M24 10.314C24 4.943 18.615.572 12 .572S0 4.943 0 10.314c0 4.811 4.27 8.842 10.035 9.608c.391.082.923.258 1.058.59c.12.301.079.766.038 1.08l-.164 1.02c-.045.301-.24 1.186 1.049.645c1.291-.539 6.916-4.078 9.436-6.975C23.176 14.393 24 12.458 24 10.314"/></svg>',
}

// brandColors 仅作用于 currentColor 单色源（着品牌色）；全彩 baked SVG 会忽略。
// 当前仅 wechat 为 supported 全彩展示，其余渠道在 AppChannelsTab 中 muted 灰度，颜色实际被压平，
// 但仍按官方品牌色登记，便于将来某渠道转 supported 时直接全彩。
const brandColors: Record<ChannelLogoType, string> = {
  wechat: '#07c160',
  work_wechat: '#2d8cff',
  feishu: '#3370ff',
  dingtalk: '#1677ff',
  telegram: '#229ed9',
  whatsapp: '#25d366',
  discord: '#5865f2',
  slack: '#36c5f0',
  line: '#06c755',
}

const svg = computed(() => channelLogos[props.type])
const brandColor = computed(() => brandColors[props.type])
</script>

<style scoped>
/* logo 方块：浅中性底 + 圆角，内部 SVG 按 font-size 缩放（SVG 高 1em）。 */
.channel-logo {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 8px;
  background: #f5f6f7;
  font-size: 22px;
  line-height: 1;
  flex-shrink: 0;
  overflow: hidden;
}

/* 详情区头部大尺寸。 */
.channel-logo.large {
  width: 44px;
  height: 44px;
  border-radius: 10px;
  font-size: 28px;
}

/* 未支持渠道：灰度 + 降透明度表达不可用预告态。 */
.channel-logo.muted {
  filter: grayscale(1);
  opacity: 0.55;
}

/* v-html 注入的 SVG 不受 scoped 作用域影响，需用 :deep 控制尺寸；
   按高度 1em 缩放、宽度自适应（discord 等非正方形 logo 不被压扁）。 */
.channel-logo :deep(svg) {
  display: block;
  width: auto;
  height: 1em;
}
</style>
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/apps/ChannelLogo.spec.ts`
Expected: PASS（4 个用例全绿）。

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 无报错（确认 `ChannelLogoType` 与 `v-html` 写法通过 vue-tsc）。

- [ ] **Step 6: 提交**

```bash
git add web/src/pages/apps/ChannelLogo.vue web/src/pages/apps/ChannelLogo.spec.ts
git commit -m "$(cat <<'EOF'
feat(channels): 新增 ChannelLogo 内联品牌 SVG 组件

按渠道 type 渲染对应品牌 logo：telegram/whatsapp/discord/slack 用全彩 SVG，
微信等单色源按品牌色着色，未支持渠道经 grayscale 灰度化。承载 logo 方块的
尺寸、浅色底、圆角与灰度样式，供渠道列表与详情区复用。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 扩展渠道清单到 9 个并接入 ChannelLogo

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.vue`（`ChannelDisplay` 接口、`channels` 数组、列表与详情区模板、移除旧 logo CSS）
- Test: `web/src/pages/apps/AppChannelsTab.spec.ts`（列表数量与 logo 断言）

- [ ] **Step 1: 改失败测试（列表数量 + logo 选择器）**

修改 `web/src/pages/apps/AppChannelsTab.spec.ts` 中第一个用例「列出全部渠道并置灰暂不支持渠道」，整体替换为：

```ts
  // 渠道清单：实例详情页必须一次性展示全部规划渠道（9 个），并明确哪些渠道当前不可用。
  it('列出全部渠道并置灰暂不支持渠道', () => {
    const wrapper = mountChannelsTab()

    const items = wrapper.findAll('.channel-list-item')
    expect(items).toHaveLength(9)
    expect(items.map(item => item.text())).toEqual([
      expect.stringContaining('微信'),
      expect.stringContaining('企业微信'),
      expect.stringContaining('飞书'),
      expect.stringContaining('钉钉'),
      expect.stringContaining('Telegram'),
      expect.stringContaining('WhatsApp'),
      expect.stringContaining('Discord'),
      expect.stringContaining('Slack'),
      expect.stringContaining('Line'),
    ])

    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(1)
    expect(supported[0].text()).toContain('已支持')
    expect(supported[0].text()).toContain('微信')
    // 微信 logo 用新的 type 钩子，且为内联 SVG
    expect(wrapper.find('.channel-logo--wechat').exists()).toBe(true)

    const unsupported = wrapper.findAll('.channel-list-item.unsupported')
    expect(unsupported).toHaveLength(8)
    expect(unsupported.every(item => item.attributes('aria-disabled') === 'true')).toBe(true)
    expect(unsupported.every(item => item.attributes('disabled') !== undefined)).toBe(true)
    expect(unsupported.every(item => item.text().includes('暂不支持'))).toBe(true)
    // 未支持渠道 logo 均带灰度 muted 标记（含已有 3 个 + 新增 5 个 = 8 个）
    expect(wrapper.findAll('.channel-logo.muted')).toHaveLength(8)
    expect(wrapper.find('.channel-logo--telegram').exists()).toBe(true)
    expect(wrapper.find('.channel-logo--whatsapp').exists()).toBe(true)
    expect(wrapper.find('.channel-logo--line').exists()).toBe(true)
  })
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts -t '列出全部渠道'`
Expected: FAIL —— `items` 长度仍为 4（数组未扩展）、且找不到 `.channel-logo--wechat`（仍是旧汉字 logo）。

- [ ] **Step 3: 扩展 ChannelDisplay 与 channels 数组**

在 `web/src/pages/apps/AppChannelsTab.vue` 中修改 `ChannelDisplay` 接口与 `channels` 数组。

将接口（原第 120-128 行）改为（去掉 `logoText` / `logoClass`，扩展 `type` 联合）：

```ts
// ChannelDisplay 是渠道 tab 的纯前端展示模型；当前仅 wechat 接入真实绑定能力。
// 其他渠道作为能力边界展示，不参与 API 参数或后端状态机。logo 由 ChannelLogo 按 type 渲染。
interface ChannelDisplay {
  type:
    | 'wechat'
    | 'work_wechat'
    | 'feishu'
    | 'dingtalk'
    | 'telegram'
    | 'whatsapp'
    | 'discord'
    | 'slack'
    | 'line'
  name: string
  description: string
  supported: boolean
  statusLabel: string
}
```

将 `channels` 数组（原第 131-168 行）整体替换为 9 项（去掉 `logoText` / `logoClass`）：

```ts
// channels 固定列出当前产品规划中需要展示的渠道；supported=false 的渠道只做灰色预告。
const channels: ReadonlyArray<ChannelDisplay> = [
  { type: 'wechat', name: '微信', description: '扫码绑定后接收助手消息', supported: true, statusLabel: '已支持' },
  { type: 'work_wechat', name: '企业微信', description: '企业内部协作场景', supported: false, statusLabel: '暂不支持' },
  { type: 'feishu', name: '飞书', description: '团队消息与工作台场景', supported: false, statusLabel: '暂不支持' },
  { type: 'dingtalk', name: '钉钉', description: '组织通讯与审批场景', supported: false, statusLabel: '暂不支持' },
  { type: 'telegram', name: 'Telegram', description: '海外即时通讯与 Bot 接入场景', supported: false, statusLabel: '暂不支持' },
  { type: 'whatsapp', name: 'WhatsApp', description: '海外用户触达与客服场景', supported: false, statusLabel: '暂不支持' },
  { type: 'discord', name: 'Discord', description: '社区与游戏玩家场景', supported: false, statusLabel: '暂不支持' },
  { type: 'slack', name: 'Slack', description: '团队协作与工作流场景', supported: false, statusLabel: '暂不支持' },
  { type: 'line', name: 'Line', description: '日本与东南亚用户场景', supported: false, statusLabel: '暂不支持' },
]
```

- [ ] **Step 4: 列表模板改用 ChannelLogo**

在 `AppChannelsTab.vue` 顶部 `<script setup>` 增加导入（紧跟现有 `import AuthChallengeRenderer ...` 之后）：

```ts
import ChannelLogo from './ChannelLogo.vue'
```

将列表项里的旧汉字 logo（原模板第 28-34 行）：

```vue
          <span
            class="channel-logo"
            :class="[channel.logoClass, { muted: !channel.supported }]"
            aria-hidden="true"
          >
            {{ channel.logoText }}
          </span>
```

替换为：

```vue
          <ChannelLogo :type="channel.type" :muted="!channel.supported" />
```

- [ ] **Step 5: 详情区头部 logo 改用 ChannelLogo**

将详情区头部的旧大号汉字 logo（原模板第 46-52 行）：

```vue
            <span
              class="channel-logo large"
              :class="activeChannel.logoClass"
              aria-hidden="true"
            >
              {{ activeChannel.logoText }}
            </span>
```

替换为（activeChannel 恒为已支持的微信，全彩、不 muted）：

```vue
            <ChannelLogo :type="activeChannel.type" large />
```

- [ ] **Step 6: 移除迁移到 ChannelLogo 的旧 logo CSS**

删除 `AppChannelsTab.vue` `<style scoped>` 中以下整段（原第 343-372 行）——这些样式已搬到 `ChannelLogo.vue`：

```css
.channel-logo {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 8px;
  color: #ffffff;
  font-size: 14px;
  font-weight: 800;
  line-height: 1;
  flex-shrink: 0;
}

.channel-logo.large {
  width: 44px;
  height: 44px;
  border-radius: 10px;
  font-size: 17px;
}

.channel-logo.wechat {
  background: #1aad19;
}

.channel-logo.work-wechat,
.channel-logo.feishu,
.channel-logo.dingtalk,
.channel-logo.muted {
  background: #c7ccd1;
}
```

> 注意：`.channel-list-item` 的 `grid-template-columns: 36px ...` 仍依赖 logo 宽度 36px，`ChannelLogo` 默认尺寸正是 36px，保持对齐无需改动。

- [ ] **Step 7: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts -t '列出全部渠道'`
Expected: PASS。

- [ ] **Step 8: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 无报错（`ChannelDisplay.type` 与 `ChannelLogo` 的 `ChannelLogoType` 一致，`channel.logoText` / `logoClass` 已无引用）。

- [ ] **Step 9: 提交**

```bash
git add web/src/pages/apps/AppChannelsTab.vue web/src/pages/apps/AppChannelsTab.spec.ts
git commit -m "$(cat <<'EOF'
feat(channels): 渠道列表扩到 9 个并改用全彩 logo

在微信/企业微信/飞书/钉钉基础上新增 Telegram/WhatsApp/Discord/Slack/Line
五个灰色「暂不支持」预告项；列表与详情区 logo 由汉字方块改为 ChannelLogo
渲染的全彩品牌 SVG，未支持渠道灰度展示。不改后端、不改绑定流程。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: 合并详情区头部「渠道名 · 状态」

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.vue`（头部标题模板、状态行拆分、新增内联状态 CSS、移除 kicker CSS）
- Test: `web/src/pages/apps/AppChannelsTab.spec.ts`（已绑定用例的头部文案）

- [ ] **Step 1: 改失败测试（头部状态文案）**

修改 `web/src/pages/apps/AppChannelsTab.spec.ts` 最后一个用例「已绑定渠道展示中文状态且不显示 challenge 空态」，整体替换为：

```ts
  // 已绑定状态：头部以「微信 · 已绑定」呈现，已绑定身份单独成行；不泄露后端原值 bound。
  it('已绑定渠道头部展示「渠道名 · 状态」且不显示 challenge 空态', () => {
    progress.value = {
      status: 'bound',
      bound_identity: 'alice',
      updated_at: '2026-05-25T12:00:00Z',
    }

    const wrapper = mountChannelsTab()
    const detail = wrapper.find('.channel-detail')

    expect(detail.text()).toContain('微信')
    expect(detail.text()).toContain('· 已绑定') // 状态并入头部同一行
    expect(detail.text()).toContain('已绑定：alice') // 绑定身份仍单独成行
    expect(detail.text()).not.toContain('当前渠道') // 去掉 kicker
    expect(detail.text()).not.toContain('当前状态：') // 去掉旧状态前缀
    expect(wrapper.text()).not.toContain('bound') // 不泄露后端原值
    expect(wrapper.text()).not.toContain('尚未发起挑战')
  })
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts -t '已绑定'`
Expected: FAIL —— 当前模板仍输出「当前渠道」kicker 与「当前状态：已绑定」，新断言 `not.toContain('当前状态：')` 与 `toContain('· 已绑定')` 均不满足。

- [ ] **Step 3: 改头部标题为「渠道名 · 状态」**

将详情区头部标题块（原模板第 45-57 行整个 `.channel-title` 容器）替换为：

```vue
          <div class="channel-title">
            <ChannelLogo :type="activeChannel.type" large />
            <h3 class="channel-title-text">
              {{ activeChannel.name }}
              <span class="channel-status-inline">· {{ statusLabel }}</span>
            </h3>
          </div>
```

> 说明：本步已包含 Task 2 Step 5 的 `ChannelLogo` 大号 logo；若 Task 2 已替换过，这里只需改 `<div>` 内 `<p class="channel-title-kicker">当前渠道</p>` + `<h3>` 两行为上面的 `<h3 class="channel-title-text">` 结构。

- [ ] **Step 4: 拆分原状态行（状态进头部、绑定身份留下方）**

将原独立状态段（原模板第 80-83 行）：

```vue
        <p class="state-text">
          当前状态：<strong>{{ statusLabel }}</strong>
          <span v-if="progress?.bound_identity"> ｜ 已绑定：{{ progress.bound_identity }}</span>
        </p>
```

替换为（状态已移到头部，这里只保留绑定身份成行）：

```vue
        <p v-if="progress?.bound_identity" class="state-text">已绑定：{{ progress.bound_identity }}</p>
```

> 其后的「最近错误」「正在生成登录二维码…」「二维码已过期」三段（原第 84-88 行）与 `AuthChallengeRenderer` 保持不变。

- [ ] **Step 5: 调整 CSS（新增内联状态样式、移除 kicker 样式）**

在 `AppChannelsTab.vue` `<style scoped>` 中，将原 kicker 相关规则（原第 428-440 行）：

```css
.channel-title h3,
.channel-title-kicker {
  margin: 0;
}

.channel-title h3 {
  font-size: 16px;
}

.channel-title-kicker {
  color: var(--color-text-secondary);
  font-size: 12px;
}
```

替换为：

```css
.channel-title-text {
  margin: 0;
  font-size: 16px;
}

/* 状态文字与渠道名同行，用次要色 + 常规字重以区分主次。 */
.channel-status-inline {
  color: var(--color-text-secondary);
  font-size: 14px;
  font-weight: 400;
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts -t '已绑定'`
Expected: PASS。

- [ ] **Step 7: 跑整个渠道 tab 测试套件**

Run: `cd web && npx vitest run src/pages/apps/AppChannelsTab.spec.ts src/pages/apps/ChannelLogo.spec.ts`
Expected: 全部 PASS（含「非微信 channelType」用例——详情区仍显示「微信 · 未发起」，不含「飞书」）。

- [ ] **Step 8: 提交**

```bash
git add web/src/pages/apps/AppChannelsTab.vue web/src/pages/apps/AppChannelsTab.spec.ts
git commit -m "$(cat <<'EOF'
feat(channels): 详情区头部合并为「渠道名 · 状态」单行

去掉「当前渠道」与「当前状态：」两个 kicker，状态以次要色文字并入渠道名
同一行；绑定身份、错误、二维码等提示仍保留在头部下方。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 整体验证

**Files:** 无（仅运行校验）

- [ ] **Step 1: 全量前端测试**

Run: `cd web && npx vitest run`
Expected: 全部 PASS（确认改动未波及其他用例）。

- [ ] **Step 2: 类型检查**

Run: `cd web && npm run typecheck`
Expected: 无报错。

- [ ] **Step 3: 浏览器人工验证（不可用 curl 替代）**

按 `CLAUDE.md` 交付前检查要求，用真实浏览器登录 manager 后台（http://localhost:5173，admin / admin123），进入任一实例的渠道 tab，逐项核对：

- 左侧列表展示 9 个渠道，顺序为微信→企业微信→飞书→钉钉→Telegram→WhatsApp→Discord→Slack→Line。
- 微信 logo 为全彩绿色、可点选；其余 8 个为灰度 logo、置灰且「暂不支持」、不可点。
- 右侧详情区头部为「微信 · {状态}」单行（如未绑定显示「微信 · 未发起」，已绑定显示「微信 · 已绑定」）。
- 已绑定时下方单独一行显示「已绑定：{身份}」；错误/二维码生成/过期提示位置正常。
- 发起登录、刷新二维码、解绑流程与改动前一致。

若发现问题先修复再重新验证，直到正常后再交付。

- [ ] **Step 4: 确认无关改动**

Run: `git status`
Expected: 仅 `web/src/pages/apps/ChannelLogo.vue`、`ChannelLogo.spec.ts`、`AppChannelsTab.vue`、`AppChannelsTab.spec.ts` 变更；无后端、无 `openapi/openapi.yaml`、无 `web/src/api/generated.ts` 改动。

---

## 自检（计划对照设计）

**1. 设计覆盖：**
- 需求①渠道扩展到 9 个 → Task 2（`channels` 数组 + 列表数量测试）。✓
- 需求②全彩 logo + 浅底方块 + 未支持灰度 → Task 1（`ChannelLogo` 组件、全彩/单色源、grayscale）+ Task 2（列表/详情接入、muted）。✓
- 需求③头部「渠道名 · 状态」合并 → Task 3。✓
- 非目标（不动后端/OpenAPI/生成类型、无远程依赖）→ Task 4 Step 4 校验；SVG 全部内联。✓

**2. 占位符扫描：** 无 TBD/TODO；所有 SVG 为真实内联 markup，所有命令含预期输出。✓

**3. 类型一致性：** `ChannelLogo` 的 `ChannelLogoType` 与 `AppChannelsTab` 的 `ChannelDisplay.type` 联合完全一致（9 个，含下划线 `work_wechat`）；class 钩子统一为 `channel-logo--{type}`；`muted` / `large` prop 命名前后一致。✓
