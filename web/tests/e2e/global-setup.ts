import { execSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// Playwright globalSetup：在所有 spec 跑之前先执行：
// 1. 在仓库根跑 `make seed-e2e`，把 truncate 业务表 + 重新构造 fixture 一并完成；
// 2. 解析 stdout 末行的 fixture JSON，写到 process.env.OCM_E2E_FIXTURE，
//    供单条 spec 通过 fixtures.ts 的 loadE2EFixture() 读取；
// 3. JSON 不合法直接抛错，避免 spec 拿到半截脏数据。
async function globalSetup() {
  // 在 ESM 下没有 __dirname；用 import.meta.url 反推当前文件目录，再回到仓库根。
  const here = dirname(fileURLToPath(import.meta.url))
  const repoRoot = resolve(here, '../../..')
  const stdout = execSync('make seed-e2e', { cwd: repoRoot }).toString('utf8')
  const lines = stdout.trim().split(/\r?\n/).filter(Boolean)
  const lastLine = lines[lines.length - 1]
  // 简单合法性校验：不是 JSON 就抛错。
  JSON.parse(lastLine)
  process.env.OCM_E2E_FIXTURE = lastLine
}

export default globalSetup
