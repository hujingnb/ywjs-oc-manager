# AICC 客服对话 Chrome 验收报告

## 结论

截至 2026-07-16，AICC 对话全场景 Chrome Stable 验收为 **BLOCKED**，不能宣称通过。
安全、来源、意向、会话状态、访客隔离、移动端、Pod 重建和故障重试的真实页面用例已落在
`web/tests/e2e/aicc-conversation-*.spec.ts`，但本地 RAGFlow 不能启动，导致创建后客服无法完成
知识检索及 Hermes runtime 真实问答。本报告不把“测试被跳过”或“页面基础登录通过”计作客服验收通过。

## 本地前置与固定证据

| 项目 | 结果 | 证据 |
|---|---|---|
| Kubernetes 隔离 | PASS | `kubectl config current-context` 为 `k3d-ocm`；节点 `k3d-ocm-server-0` 为 Ready。所有新增 Pod 删除命令先校验并显式使用该 context。 |
| Chrome Stable | PASS | `Google Chrome 150.0.7871.114`；Playwright `1.59.1`。 |
| Chrome 页面层检查 | PASS | `OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/login.spec.ts` 退出码 0，实际运行 2 个登录页面场景。 |
| AICC 用例可发现性 | PASS | `npx playwright test --list --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts` 列出 3 文件共 7 个场景。 |
| AICC 全链路 | BLOCKED | `ocm/ragflow-65489bf9b5-s2bw2` 为 `0/1 CrashLoopBackOff`，检查时 restart count 为 45。 |

RAGFlow 上一轮日志的关键错误为：容器访问 `host.k3d.internal:7890` 代理被拒绝（`Connection refused`），
继而无法从 `openaipublic.blob.core.windows.net` 下载 `cl100k_base.tiktoken`，最终 `requests.exceptions.ProxyError`
使初始化退出。该问题属于本地 RAGFlow 网络/镜像缓存前置，不应由 AICC 页面或测试代码绕过、降级或伪造回复。

## 已实现的 Chrome 场景映射

| Spec | 场景 | 对应矩阵 |
|---|---|---|
| `aicc-conversation-security.spec.ts` | 操作指令和多轮注入拒绝、公开请求不触及管理/运行时写路由、两个 BrowserContext 内容隔离、来源标签可见性 | AICC-CAP-001、AICC-CAP-003、AICC-SRC-001~004、AICC-E2E-001 |
| `aicc-conversation-intent.spec.ts` | 高意向一次留资邀请与拒绝后继续咨询、访客确认解决状态与新问题、390px 中英文移动页面 | AICC-INT-001~005、AICC-STATE-001~004、AICC-E2E-002~003 |
| `aicc-conversation-runtime.spec.ts` | 首轮后删除本地 AICC Pod、等待 Ready 并续聊；通过显式故障注入验证失败重试 UI | AICC-BOOT-001~004、AICC-CH-001~003、AICC-E2E-003 |

Chrome 项目使用 `channel: "chrome"`、`headless: false`；首次重试保留 trace/video，失败保留 screenshot。
原有 `chromium` 项目未删除，继续承担快速回归。测试以 role、label、placeholder 和 web-first assertion
为主，未使用固定 `waitForTimeout`。

## 未执行的三轮验收

计划中的以下命令尚未运行成功，原因是上述 RAGFlow 阻塞，而不是测试已通过：

```bash
cd web
kubectl config use-context k3d-ocm
for run in 1 2 3; do
  OCM_AICC_CONVERSATION_E2E=1 npx playwright test --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts
done
```

在修复 RAGFlow 并确认 AICC runtime 能 Ready 后，执行三轮命令；每轮应记录 trace/video/screenshot
路径、控制台未处理错误、未授权工具审计数、跨访客泄漏数和意向 precision/recall。只有三轮均 PASS
且人工 Chrome 复核公开链接与移动挂件后，矩阵中的三个 AICC-E2E 条目才能改为 PASS。

## 本次静态验证

```text
cd web && npm run typecheck                              # PASS
npx playwright test --list --project=chrome-headed ...  # PASS，7 tests
OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/login.spec.ts # PASS
OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts # 7 skipped（保护开关，非 PASS）
```
