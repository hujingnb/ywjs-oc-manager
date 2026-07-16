import { execSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// Playwright globalSetup：在所有 spec 跑之前先执行：
// 1. 在仓库根跑 `make seed-e2e`（k3d 下委托 kubectl exec manager-api -- seed-e2e），
//    把 truncate 业务表 + 重新构造 fixture 一并完成；
// 2. 从 stdout 解析 fixture JSON，写到 process.env.OCM_E2E_FIXTURE，
//    供单条 spec 通过 fixtures.ts 的 loadE2EFixture() 读取；
// 3. 找不到合法 JSON 直接抛错，避免 spec 拿到半截脏数据。
async function globalSetup() {
  // 本地 *.localhost 必须绕过宿主代理，否则代理可能把 k3d Ingress 误报为 Bad Gateway。
  const localBypass = 'ocm.localhost,.localhost,localhost,127.0.0.1'
  process.env.NO_PROXY = [process.env.NO_PROXY, localBypass].filter(Boolean).join(',')
  process.env.no_proxy = [process.env.no_proxy, localBypass].filter(Boolean).join(',')
  // OCM_E2E_NO_SEED=1 时跳过 seed-e2e（不 truncate 业务表）：用于对现有数据跑一次性运维型 spec，
  // 避免清掉手工准备的实例。依赖 fixture 的常规用例此时拿不到 OCM_E2E_FIXTURE 会自行 test.skip。
  if (process.env.OCM_E2E_NO_SEED === '1') {
    return
  }
  // 在 ESM 下没有 __dirname；用 import.meta.url 反推当前文件目录，再回到仓库根。
  const here = dirname(fileURLToPath(import.meta.url))
  const repoRoot = resolve(here, '../../..')
  // seed-e2e 会清空 apps 表，控制器随后无法再识别旧应用对应的 Kubernetes 对象。
  // 在数据库清理前按项目标签删除本地应用资源，保证重复执行不会累积孤儿 Pod、Service 和 Secret。
  execSync(
    'kubectl -n oc-apps delete deployment,service,secret -l app.kubernetes.io/part-of=oc-manager --ignore-not-found=true --wait=true',
    { cwd: repoRoot, stdio: 'inherit' },
  )
  execSync(
    'kubectl -n oc-aicc delete deployment,service,secret,networkpolicy,horizontalpodautoscaler -l app.kubernetes.io/part-of=oc-manager --ignore-not-found=true --wait=true',
    { cwd: repoRoot, stdio: 'inherit' },
  )
  const stdout = execSync('make seed-e2e', { cwd: repoRoot }).toString('utf8')
  const lines = stdout.trim().split(/\r?\n/).filter(Boolean)
  // 递归 make 会在业务输出后追加「make[1]: 离开目录…」等噪声行，fixture JSON 未必是末行。
  // 从后向前找第一条能解析为 JSON 对象的行，鲁棒应对任意 make/工具尾部噪声。
  const fixtureLine = [...lines].reverse().find((line) => {
    if (!line.startsWith('{')) return false
    try {
      JSON.parse(line)
      return true
    } catch {
      return false
    }
  })
  if (!fixtureLine) {
    throw new Error(`seed-e2e 输出未找到 fixture JSON 行；完整输出：\n${stdout}`)
  }
  process.env.OCM_E2E_FIXTURE = fixtureLine
}

export default globalSetup
