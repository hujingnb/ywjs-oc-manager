import { mkdirSync } from 'node:fs'
import { writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { expect, test } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// ESM 下无 __dirname；用 import.meta.url 反推当前文件目录。
const HERE = dirname(fileURLToPath(import.meta.url))

// l4-i18n-sweep：三角色 × 中英 × 全部门控可见页面的 i18n 渲染清扫。
//
// 对每个 (角色,页面,语言) 组合：整页加载后提取全部可见文本，自动判定：
//   1. i18n key 裸露（界面直接显示形如 a.b.c 的点分小写标识符）——确定性 bug 信号；
//   2. en 页残留中日韩表意文字（漏译）；
//   3. zh 页残留拉丁文本（去掉品牌/技术词白名单后）——疑似漏译，留人工复核。
// 同时每页截图存档到 docs/superpowers/verifications/screenshots/。
//
// 前置：make local-up + globalSetup 跑 seed-e2e 注入 fixture。
// 运行：npm run test:e2e -- l4-i18n-sweep.spec.ts
//
// 这是页面级 i18n 回归网（与单测级 completeness.spec.ts 互补）：completeness 校验
// 文案树结构对齐，本清扫校验「真实渲染到屏幕上的字符串」无裸 key、无跨语言残留。

// 截图与发现报告输出目录（仓库根的 docs 下）。
const OUT_DIR = resolve(HERE, '../../../docs/superpowers/verifications')
const SHOT_DIR = resolve(OUT_DIR, 'screenshots')

// app 详情页签：全角色可见（runtime 仅平台管理员）。
const APP_TABS = ['overview', 'kanban', 'cron', 'channels', 'knowledge', 'skills', 'workspace', 'audit']

// zh 页允许残留的拉丁串白名单（品牌名 / 技术术语 / 协议名 / 模型名等，非漏译）。
// 命中其一即视为合理英文，不计入疑似漏译。大小写不敏感的「包含」匹配。
const LATIN_ALLOW = [
  'OC', 'FlashAI', 'Manager', 'AGENT RUNTIME MANAGER', 'ENTERPRISE AI AGENT PLATFORM',
  'Verified', 'Token', 'API', 'AI', 'Agent', 'ID', 'URL', 'CPU', 'GPU', 'RAM', 'S3',
  'MinIO', 'RAGFlow', 'Hermes', 'GB', 'MB', 'KB', 'http', 'gpt', 'claude', 'qwen',
  'embedding', 'rerank', 'admin', 'wechat', 'WeChat', 'v1', 'JSON', 'YAML', 'Markdown',
  'ClawHub', 'Skill', 'CSV', 'UUID', 'SDK', 'OpenAI', 'SiliconFlow', 'bge', 'bce',
]

// 角色可见页面清单（path）；app/recharge 用 fixture 注入的真实 id。
function pagesFor(role: 'platform_admin' | 'org_admin' | 'org_member', appId: string, orgId: string): string[] {
  const appBase = `/apps/${appId}`
  const appTabs = APP_TABS.map((t) => `${appBase}/${t}`)
  const common = ['/', '/knowledge', '/skills', '/usage', '/apps', ...appTabs]
  if (role === 'platform_admin') {
    return [
      '/console', '/organizations', '/assistant-versions', '/platform/industry-knowledge',
      '/platform/skills', '/platform/custom-skills', '/platform/permissions', '/members',
      '/audit-logs', `${appBase}/runtime`, `/platform/organizations/${orgId}/recharge`, ...common,
    ]
  }
  if (role === 'org_admin') {
    return ['/org-console', '/members', '/members/new', '/audit-logs', '/balance', ...common]
  }
  return common // org_member
}

// scanPage 在页面上下文提取可见文本并判定三类 i18n 问题。
async function scanPage(page: import('@playwright/test').Page) {
  return page.evaluate((allow: string[]) => {
    const KEY_RE = /^[a-z][a-zA-Z0-9]*(\.[a-zA-Z0-9_]+){1,}$/
    const seen = new Set<string>()
    const texts: string[] = []
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    let n: Node | null
    while ((n = walker.nextNode())) {
      const t = (n.textContent || '').trim()
      if (!t) continue
      const el = n.parentElement
      if (!el) continue
      const s = getComputedStyle(el)
      if (s.display === 'none' || s.visibility === 'hidden') continue
      if (seen.has(t)) continue
      seen.add(t)
      texts.push(t)
    }
    // key 裸露：整串是点分小写标识符（排除路径 / 版本号）。
    const keyLeaks = texts.filter((t) => KEY_RE.test(t) && !t.includes('/') && !/\d+\.\d+\.\d+/.test(t))
    const hasCJK = (x: string) => /[一-鿿]/.test(x)
    const cjkResidue = texts.filter(hasCJK)
    // zh 页疑似漏译：含字母、无中文、去掉白名单命中后仍存在的串。
    const latinSuspect = texts.filter(
      (t) => /[a-zA-Z]/.test(t) && !hasCJK(t) && !allow.some((w) => t.toLowerCase().includes(w.toLowerCase())),
    )
    return { keyLeaks, cjkResidue, latinSuspect, total: texts.length }
  }, LATIN_ALLOW)
}

// 收集所有发现，测试结束后写盘。
type Finding = { role: string; lang: string; path: string; finalPath: string; keyLeaks: string[]; residue: string[] }
const findings: Finding[] = []

test.beforeAll(() => {
  mkdirSync(SHOT_DIR, { recursive: true })
})

test.afterAll(() => {
  writeFileSync(resolve(OUT_DIR, 'l4-sweep-findings.json'), JSON.stringify(findings, null, 2), 'utf8')
})

// 每个角色一个用例：登录后按 en、zh 两遍遍历其全部可见页面。
for (const role of ['platform_admin', 'org_admin', 'org_member'] as const) {
  test(`L4 i18n 清扫 - ${role}`, async ({ page }) => {
    let fx
    try {
      fx = loadE2EFixture()
    } catch {
      test.skip(true, '无后端 fixture，跳过 L4 清扫')
      return
    }
    test.setTimeout(300_000)

    await loginAs(page, role, fx)
    const paths = pagesFor(role, fx.app_id, fx.org_id)

    for (const lang of ['en', 'zh'] as const) {
      // 必须用顶栏 LocaleSwitcher 切换并持久化到 DB（setLocale persist=true）：仅设 localStorage
      // 会被登录后 applyFromUser(DB user.locale) 覆盖，导致页面未真正切到目标语言。
      // 切到 / 首页（必有顶栏），点开选择器选目标语言，写 DB 后续 goto 经 applyFromUser 保持。
      await page.goto('/', { waitUntil: 'networkidle', timeout: 20_000 }).catch(() => {})
      const target = lang === 'zh' ? '简体中文' : 'English'
      await page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ }).click()
      await page.locator('.n-dropdown-option', { hasText: target }).click()
      await page.waitForTimeout(800) // 等 PATCH /auth/me/locale 落库 + i18n 应用
      for (const path of paths) {
        try {
          await page.goto(path, { waitUntil: 'networkidle', timeout: 20_000 })
        } catch {
          // 个别页面网络长轮询导致 networkidle 超时，退化为 domcontentloaded 后继续扫描。
          await page.goto(path, { waitUntil: 'domcontentloaded', timeout: 20_000 }).catch(() => {})
        }
        // 给异步组件 / i18n 渲染一点时间。
        await page.waitForTimeout(600)
        const res = await scanPage(page)
        const finalPath = new URL(page.url()).pathname
        const residue = lang === 'en' ? res.cjkResidue : res.latinSuspect
        if (res.keyLeaks.length || residue.length) {
          findings.push({ role, lang, path, finalPath, keyLeaks: res.keyLeaks, residue })
        }
        // 截图存档：role-lang-<path 安全化>.png。
        const safe = path.replace(/[/:]/g, '_').replace(/^_/, '') || 'home'
        await page.screenshot({ path: resolve(SHOT_DIR, `${role}-${lang}-${safe}.png`), fullPage: true }).catch(() => {})
      }
    }
    // 回归守卫：i18n key 裸露（界面直接显示 a.b.c 标识符）是无歧义的 bug，断言为零。
    // 残留（CJK/拉丁）含数据值、品牌名与有意设计英文，存在误报，仅汇总到 JSON 供人工裁决，不在此断言。
    const roleKeyLeaks = findings.filter((f) => f.role === role && f.keyLeaks.length)
    expect(roleKeyLeaks, `存在 i18n key 裸露: ${JSON.stringify(roleKeyLeaks)}`).toHaveLength(0)
    expect(paths.length).toBeGreaterThan(0)
  })
}
