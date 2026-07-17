import { execFileSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { parseE2EFixturePoolFromOutput } from './fixture-schema'
import {
  createE2ERunID,
  e2eCommandEnv,
  parseE2ESuite,
  resolveWorkerCount,
} from './suite'

// globalSetup 为每次 Playwright 运行创建按 worker 隔离的 fixture pool，并在失败时回收当前 run。
async function globalSetup() {
  // 本地 *.localhost 必须绕过宿主代理，否则代理可能把 k3d Ingress 误报为 Bad Gateway。
  const localBypass = 'ocm.localhost,.localhost,localhost,127.0.0.1'
  process.env.NO_PROXY = [process.env.NO_PROXY, localBypass].filter(Boolean).join(',')
  process.env.no_proxy = [process.env.no_proxy, localBypass].filter(Boolean).join(',')

  // suite 和 worker 数必须与 Playwright 配置复用同一解析规则，避免 seed 与执行范围分叉。
  const suite = parseE2ESuite(process.env.OCM_E2E_SUITE)
  const workers = resolveWorkerCount(suite, process.env.OCM_E2E_WORKERS)
  // 六字节密码学随机源提供 48-bit 隔离空间，编码后恰好满足 Go 的 16 字符限制。
  const runID = createE2ERunID(randomBytes(6))
  // 在 ESM 下没有 __dirname；用 import.meta.url 反推当前文件目录，再回到仓库根。
  const here = dirname(fileURLToPath(import.meta.url))
  const repoRoot = resolve(here, '../../..')
  // Makefile 会按优先级识别多组别名，统一清理后仅用本轮 OCM_E2E_* 参数驱动 seed。
  const runEnv = e2eCommandEnv(process.env, runID, suite, workers, 'seed')

  try {
    const stdout = execFileSync('make', ['seed-e2e'], {
      cwd: repoRoot,
      env: runEnv,
      encoding: 'utf8',
    })
    // 从尾部逐候选执行 JSON、完整 schema 和本轮边界校验，伪 JSON 或旧 run 不得截断查找。
    const pool = parseE2EFixturePoolFromOutput(stdout, runID, suite, workers)

    process.env.OCM_E2E_RUN_ID = runID
    process.env.OCM_E2E_FIXTURE_POOL = JSON.stringify(pool)
  } catch (setupCause) {
    // cleanup 也使用参数数组和精确 run ID；失败诊断只能补充原错，不得掩盖 setup 根因。
    const setupError = setupCause instanceof Error
      ? setupCause
      : new Error('Playwright global setup 失败', { cause: setupCause })
    try {
      execFileSync('make', ['cleanup-e2e'], {
        cwd: repoRoot,
        env: { ...runEnv, OCM_E2E_ACTION: 'cleanup' },
        encoding: 'utf8',
      })
    } catch (cleanupCause) {
      const cleanupMessage = cleanupCause instanceof Error ? cleanupCause.message : String(cleanupCause)
      const diagnostic = `fixture cleanup 失败（run_id=${runID}）：${cleanupMessage}`
      console.error(diagnostic)
      setupError.message = `${setupError.message}\n${diagnostic}`
      // cause 同时保留 setup 原始 cause 与 cleanup cause，便于调用方追踪两条失败链。
      Object.defineProperty(setupError, 'cause', {
        value: { setupCause: setupError.cause, cleanupCause },
        configurable: true,
      })
    }
    throw setupError
  }
}

export default globalSetup
