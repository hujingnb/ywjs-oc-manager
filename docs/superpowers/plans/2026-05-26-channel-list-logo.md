# 实例渠道 Tab 全渠道列表与 Logo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在实例详情渠道 tab 中列出微信、企业微信、飞书、钉钉四个渠道，只有微信可操作，其余渠道置灰并标记暂不支持。

**Architecture:** 保持现有微信绑定 hook、状态格式化和二维码渲染逻辑不变，只在 `AppChannelsTab.vue` 增加本地渠道展示模型和左侧渠道列表布局。未支持渠道不参与 API 参数、不触发 mutation，也不改变后端渠道契约。

**Tech Stack:** Vue 3 `<script setup>`、Naive UI、Vitest、Vue Test Utils、CSS scoped styles。

---

## File Structure

- Modify: `web/src/pages/apps/AppChannelsTab.spec.ts`
  - 增加渠道清单断言：四个渠道全部渲染，微信可用，企业微信/飞书/钉钉置灰并显示“暂不支持”。
  - 保留并扩展已绑定微信状态断言，确保右侧详情仍展示中文状态和绑定身份。
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
  - 新增 `ChannelDisplay` 展示模型和固定渠道列表。
  - 将页面主体改为“左侧渠道列表 + 右侧微信详情”。
  - 新增 scoped CSS，控制可用/置灰 logo、列表布局和移动端单列降级。

No backend, OpenAPI, generated API types, or channel hooks are modified.

---

### Task 1: Channel List UI And Tests

**Files:**
- Modify: `web/src/pages/apps/AppChannelsTab.spec.ts`
- Modify: `web/src/pages/apps/AppChannelsTab.vue`

- [ ] **Step 1: Write the failing channel-list test**

In `web/src/pages/apps/AppChannelsTab.spec.ts`, add this test inside the existing `describe('AppChannelsTab', () => { ... })` block before the existing bound-state test:

```ts
  // 渠道清单：实例详情页必须一次性展示全部规划渠道，并明确哪些渠道当前不可用。
  it('列出全部渠道并置灰暂不支持渠道', () => {
    const wrapper = mountChannelsTab()

    const items = wrapper.findAll('.channel-list-item')
    expect(items).toHaveLength(4)
    expect(items.map(item => item.text())).toEqual([
      expect.stringContaining('微信'),
      expect.stringContaining('企业微信'),
      expect.stringContaining('飞书'),
      expect.stringContaining('钉钉'),
    ])

    const supported = wrapper.findAll('.channel-list-item.supported')
    expect(supported).toHaveLength(1)
    expect(supported[0].text()).toContain('已支持')
    expect(supported[0].text()).toContain('微信')
    expect(wrapper.find('.channel-logo.wechat').exists()).toBe(true)

    const unsupported = wrapper.findAll('.channel-list-item.unsupported')
    expect(unsupported).toHaveLength(3)
    expect(unsupported.every(item => item.attributes('aria-disabled') === 'true')).toBe(true)
    expect(unsupported.every(item => item.text().includes('暂不支持'))).toBe(true)
    expect(wrapper.find('.channel-logo.work-wechat').exists()).toBe(true)
    expect(wrapper.find('.channel-logo.feishu').exists()).toBe(true)
    expect(wrapper.find('.channel-logo.dingtalk').exists()).toBe(true)
  })
```

Then update the existing bound-state test so it also verifies the right-side detail container still carries the current WeChat binding state:

```ts
  // 已绑定状态：页面展示中文“已绑定”，不泄露后端原值 bound 或内部 challenge 空态。
  it('已绑定渠道展示中文状态且不显示 challenge 空态', () => {
    progress.value = {
      status: 'bound',
      bound_identity: 'alice',
      updated_at: '2026-05-25T12:00:00Z',
    }

    const wrapper = mountChannelsTab()
    const detail = wrapper.find('.channel-detail')

    expect(detail.text()).toContain('微信')
    expect(detail.text()).toContain('当前状态：已绑定')
    expect(detail.text()).toContain('已绑定：alice')
    expect(wrapper.text()).not.toContain('当前状态：bound')
    expect(wrapper.text()).not.toContain('尚未发起挑战')
  })
```

- [ ] **Step 2: Run the page test and verify it fails**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/apps/AppChannelsTab.spec.ts
```

Expected: FAIL because the current component does not render `.channel-list-item`, `.channel-logo.*`, or `.channel-detail`.

- [ ] **Step 3: Add the local channel display model**

In `web/src/pages/apps/AppChannelsTab.vue`, add this interface and constant after the `defineProps` line:

```ts
// ChannelDisplay 是渠道 tab 的纯前端展示模型；当前仅 wechat 接入真实绑定能力。
// 其他渠道作为能力边界展示，不参与 API 参数或后端状态机。
interface ChannelDisplay {
  type: 'wechat' | 'work_wechat' | 'feishu' | 'dingtalk'
  name: string
  description: string
  supported: boolean
  statusLabel: string
  logoText: string
  logoClass: string
}

// channels 固定列出当前产品规划中需要展示的渠道；supported=false 的渠道只做灰色预告。
const channels: ReadonlyArray<ChannelDisplay> = [
  {
    type: 'wechat',
    name: '微信',
    description: '扫码绑定后接收助手消息',
    supported: true,
    statusLabel: '已支持',
    logoText: '微',
    logoClass: 'wechat',
  },
  {
    type: 'work_wechat',
    name: '企业微信',
    description: '企业内部协作场景',
    supported: false,
    statusLabel: '暂不支持',
    logoText: '企',
    logoClass: 'work-wechat',
  },
  {
    type: 'feishu',
    name: '飞书',
    description: '团队消息与工作台场景',
    supported: false,
    statusLabel: '暂不支持',
    logoText: '飞',
    logoClass: 'feishu',
  },
  {
    type: 'dingtalk',
    name: '钉钉',
    description: '组织通讯与审批场景',
    supported: false,
    statusLabel: '暂不支持',
    logoText: '钉',
    logoClass: 'dingtalk',
  },
]
```

Add this computed value after `channelTypeRef`:

```ts
// activeChannel 当前始终落在微信；保留 computed 是为了让模板只依赖展示模型。
const activeChannel = computed(() => channels.find(channel => channel.type === channelType.value) ?? channels[0])
```

- [ ] **Step 4: Replace the template with the split layout**

In `web/src/pages/apps/AppChannelsTab.vue`, replace the entire `<template>...</template>` block with:

```vue
<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Instance · Channels</p>
        <h2 style="margin: 0">渠道绑定</h2>
      </div>
    </template>

    <div v-if="!appId" class="state-text">请选择目标实例</div>
    <div v-else class="channels-layout">
      <aside class="channel-list" aria-label="渠道列表">
        <button
          v-for="channel in channels"
          :key="channel.type"
          type="button"
          class="channel-list-item"
          :class="{
            active: channel.type === activeChannel.type,
            supported: channel.supported,
            unsupported: !channel.supported,
          }"
          :disabled="!channel.supported"
          :aria-disabled="String(!channel.supported)"
        >
          <span
            class="channel-logo"
            :class="[channel.logoClass, { muted: !channel.supported }]"
            aria-hidden="true"
          >
            {{ channel.logoText }}
          </span>
          <span class="channel-copy">
            <strong>{{ channel.name }}</strong>
            <span>{{ channel.description }}</span>
          </span>
          <span class="channel-support-label">{{ channel.statusLabel }}</span>
        </button>
      </aside>

      <section class="channel-detail" aria-label="微信渠道详情">
        <div class="channel-detail-head">
          <div class="channel-title">
            <span
              class="channel-logo large"
              :class="activeChannel.logoClass"
              aria-hidden="true"
            >
              {{ activeChannel.logoText }}
            </span>
            <div>
              <p class="channel-title-kicker">当前渠道</p>
              <h3>{{ activeChannel.name }}</h3>
            </div>
          </div>

          <n-space :size="8">
            <n-button
              type="primary"
              :disabled="!appId || !canManage"
              :loading="beginning"
              @click="beginAuth"
            >
              {{ primaryButtonLabel }}
            </n-button>
            <n-button
              v-if="showRefreshChallenge"
              :disabled="!canManage"
              :loading="beginning"
              @click="beginAuth"
            >
              {{ beginning ? '生成中…' : '刷新二维码' }}
            </n-button>
            <n-button v-if="canUnbind" @click="unbind">解绑</n-button>
          </n-space>
        </div>

        <p class="state-text">
          当前状态：<strong>{{ statusLabel }}</strong>
          <span v-if="progress?.bound_identity"> ｜ 已绑定：{{ progress.bound_identity }}</span>
        </p>
        <p v-if="progress?.error_message" class="state-text danger">最近错误：{{ progress.error_message }}</p>
        <p v-if="isWaitingForChallenge" class="state-text">正在生成登录二维码…</p>
        <p v-if="challengeExpired" class="state-text danger">
          当前二维码已过期，请点击"刷新二维码"重新生成。
        </p>

        <AuthChallengeRenderer v-if="visibleChallenge" :challenge="visibleChallenge" @rendered="onQrRendered" />
      </section>
    </div>
  </n-card>
</template>
```

- [ ] **Step 5: Add scoped styles for the channel layout**

At the end of `web/src/pages/apps/AppChannelsTab.vue`, add:

```vue
<style scoped>
.channels-layout {
  display: grid;
  grid-template-columns: minmax(200px, 260px) minmax(0, 1fr);
  gap: 18px;
  align-items: stretch;
}

.channel-list {
  display: grid;
  align-content: start;
  gap: 8px;
  padding-right: 16px;
  border-right: 1px solid var(--color-divider);
}

.channel-list-item {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr) auto;
  gap: 10px;
  align-items: center;
  width: 100%;
  min-height: 58px;
  padding: 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  color: var(--color-text-primary);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}

.channel-list-item.supported.active {
  border-color: var(--color-success-border);
  background: var(--color-success-soft);
}

.channel-list-item.unsupported {
  color: var(--color-text-tertiary);
  background: var(--color-neutral-soft);
  cursor: not-allowed;
}

.channel-list-item:disabled {
  opacity: 1;
}

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

.channel-copy {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.channel-copy strong {
  font-size: 14px;
}

.channel-copy span {
  overflow: hidden;
  color: var(--color-text-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-list-item.unsupported .channel-copy span {
  color: var(--color-text-tertiary);
}

.channel-support-label {
  min-width: 58px;
  color: var(--color-text-secondary);
  font-size: 12px;
  text-align: right;
  white-space: nowrap;
}

.channel-list-item.supported .channel-support-label {
  color: var(--color-success-text);
  font-weight: 700;
}

.channel-detail {
  min-width: 0;
}

.channel-detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.channel-title {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: 12px;
}

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

@media (max-width: 760px) {
  .channels-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .channel-list {
    padding-right: 0;
    padding-bottom: 14px;
    border-right: 0;
    border-bottom: 1px solid var(--color-divider);
  }

  .channel-detail-head {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
```

- [ ] **Step 6: Run the page test and verify it passes**

Run:

```bash
rtk npm --prefix web test -- --run src/pages/apps/AppChannelsTab.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Run focused frontend tests**

Run:

```bash
rtk npm --prefix web test -- --run src/api/hooks/useChannel.test.ts src/components/__tests__/AuthChallengeRenderer.spec.ts src/pages/apps/AppChannelsTab.spec.ts
```

Expected: PASS.

- [ ] **Step 8: Run typecheck**

Run:

```bash
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 9: Commit the implementation**

Run:

```bash
rtk git add web/src/pages/apps/AppChannelsTab.vue web/src/pages/apps/AppChannelsTab.spec.ts
rtk git commit -m "feat(channel): 展示实例渠道列表和 logo" -m "在实例详情渠道 tab 中增加微信、企业微信、飞书、钉钉四个渠道入口。\\n\\n当前仅微信保持可操作，企业微信、飞书、钉钉置灰并标记暂不支持；微信绑定、刷新二维码和解绑流程继续复用现有逻辑。\\n\\n测试覆盖全渠道列表、未支持渠道置灰以及已绑定微信状态展示。"
```

Expected: commit succeeds and contains only the two frontend files.

---

### Task 2: Browser Verification

**Files:**
- No source edits unless verification finds a defect.

- [ ] **Step 1: Start the local stack**

Run:

```bash
rtk make dev-up
rtk make migrate-up
rtk make seed-e2e
```

Expected: compose services start, migrations finish, and seed fixture is created. If Docker is unavailable, record that browser verification could not run because the local stack cannot start.

- [ ] **Step 2: Open the channels tab in a real browser**

Use Chrome or Playwright-controlled Chromium and navigate to:

```text
http://localhost:5173/login
```

Login with:

```text
组织标识：test-org
账号：test-org
密码：test-org123
```

Then open the seeded instance from the instance list and switch to the `渠道` tab.

Expected:
- The page shows a left channel list and right WeChat detail panel.
- The left list contains `微信`、`企业微信`、`飞书`、`钉钉`.
- `微信` is visually active and shows `已支持`.
- `企业微信`、`飞书`、`钉钉` are greyed out and show `暂不支持`.
- The right panel still shows the WeChat status line and available WeChat actions.
- No console errors appear during page load or tab switching.

- [ ] **Step 3: Verify responsive layout**

In the same browser session, resize the viewport to a narrow mobile-like width around `390px`.

Expected:
- Channel list stacks above the WeChat detail panel.
- Channel names, support labels, and action buttons do not overlap.
- Unsupported channel rows remain visibly grey and disabled.

- [ ] **Step 4: Fix any browser-only defect**

If verification finds layout overlap, inaccessible controls, or console errors, make the smallest scoped change in `web/src/pages/apps/AppChannelsTab.vue`, then rerun:

```bash
rtk npm --prefix web test -- --run src/pages/apps/AppChannelsTab.spec.ts
rtk npm --prefix web run typecheck
```

Expected: PASS after the fix.

- [ ] **Step 5: Commit browser verification fixes if needed**

If Step 4 changed source files, run:

```bash
rtk git add web/src/pages/apps/AppChannelsTab.vue web/src/pages/apps/AppChannelsTab.spec.ts
rtk git commit -m "fix(channel): 修正渠道列表响应式展示" -m "根据真实浏览器验证结果调整实例详情渠道 tab 的列表和详情布局。\\n\\n修复窄屏下渠道名称、支持状态或操作按钮可能重叠的问题，并保留微信绑定流程不变。\\n\\n验证已重新运行渠道页单元测试和前端类型检查。"
```

Expected: commit succeeds only when Step 4 produced code changes.

---

## Self-Review

- Spec coverage: Task 1 implements the four-channel list, logo-like local badges, WeChat-only operation, unsupported grey states, and existing WeChat detail preservation. Task 2 covers the required real-browser validation.
- Placeholder scan: no unresolved marker text remains; every code-editing step includes concrete code.
- Type consistency: `ChannelDisplay.type`, `logoClass`, `activeChannel`, `.channel-list-item`, `.channel-logo.*`, and `.channel-detail` are defined before tests rely on them.
