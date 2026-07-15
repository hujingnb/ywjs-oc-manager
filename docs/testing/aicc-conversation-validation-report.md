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
| AICC 用例可发现性 | PASS | `npx playwright test --list --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts` 列出 3 文件共 32 个场景。 |
| AICC 全链路 | BLOCKED | `ocm/ragflow-65489bf9b5-s2bw2` 为 `0/1 CrashLoopBackOff`，检查时 restart count 为 45。 |

RAGFlow 上一轮日志的关键错误为：容器访问 `host.k3d.internal:7890` 代理被拒绝（`Connection refused`），
继而无法从 `openaipublic.blob.core.windows.net` 下载 `cl100k_base.tiktoken`，最终 `requests.exceptions.ProxyError`
使初始化退出。该问题属于本地 RAGFlow 网络/镜像缓存前置，不应由 AICC 页面或测试代码绕过、降级或伪造回复。

## 已实现的 Chrome 场景映射

| Spec | 场景 | 对应矩阵 |
|---|---|---|
| `aicc-conversation-security.spec.ts` | 公开链接页和网页挂件 iframe 的知识/来源页面合同、命令/文件/建站/登录/多轮注入拒绝、两个 BrowserContext 隔离、域名/隐私/频控/图片/token 入口边界 | AICC-CAP-001、AICC-CAP-003、AICC-SRC-001~004、AICC-E2E-001 |
| `aicc-conversation-intent.spec.ts` | low/medium、求职/投诉/媒体误判负例、高意向一次邀请与拒绝、升级/更正降级、匿名候选提交联系方式、后台线索可见、意向失败重试一次邀请、同会话双标签并发留资、状态和 390px 中英文移动页面 | AICC-INT-001~005、AICC-STATE-001~004、AICC-E2E-002~003 |
| `aicc-conversation-runtime.spec.ts` | 首轮后删除本地 AICC Pod、等待 Ready 并续聊；RAGFlow/搜索/模型/队列四类显式故障注入、失败重试、未解决刷新与新消息重置 | AICC-BOOT-001~004、AICC-CH-001~003、AICC-E2E-003 |

Chrome 项目使用 `channel: "chrome"`、`headless: false`；首次重试保留 trace/video，失败保留 screenshot。
原有 `chromium` 项目未删除，继续承担快速回归。测试以 role、label、placeholder 和 web-first assertion
为主，未使用固定 `waitForTimeout`。

以下细项已写入 spec，但因 runtime 阻塞均为**待执行**：客服/企业/行业知识单独命中、组合命中和冲突优先级；
公开网络未确认来源；后台线索画像字段证据可视化；授权域名和频率限制的服务端拒绝审计；以及故障恢复后的
实际重试成功。当前实现只在公开页面验证可观察合同，未将这些细项误记为 PASS。

`seed-e2e` 当前只构造组织、成员、应用等通用 fixture，**不**预置客服/企业/行业三层固定事实、冲突网页、
来源标题或绑定关系。因此上述知识场景有独立 `OCM_AICC_KNOWLEDGE_FIXTURE=1` 前置，缺失时逐条标为 BLOCKED。
同样，仓库尚未提供可由 E2E 启停的一次性 RAGFlow/搜索/模型/队列故障 injector；四类恢复场景有独立
`OCM_AICC_FAULT_INJECTION=1` 前置，不能把当前 skip 视为故障恢复已测。

网页挂件 iframe、意向分析失败后恢复重试和同会话多标签并发提交均已有 Chrome 场景；意向重试场景在
`OCM_AICC_INTENT_RETRY_FIXTURE=1` 时会仅对本地 `k3d-ocm` 的 manager-api 注入一次性失败并滚动重启，
server 还要求 `app.env=local` 才会消费该变量；注入器同时暂停重试扫描，待 E2E 读取到持久化失败记录后
显式清除两个变量并滚动重启来释放恢复。该控制面不由 seed-e2e 默认开启，且当前 RAGFlow/runtime
仍阻塞，故仍为 BLOCKED 的“已实现未运行”子项，不能用于计算意向 precision、recall 或全场景覆盖率。

来源场景在运行时会强制断言来源标题、消息时间、未确认标签，以及公开网络来源的 HTTPS 链接；操作性拒绝会
通过 manager 数据库中 `aicc_message_sources` 的受信任工具来源审计断言为零。当前公开页仅以文本展示来源，
未确认网络来源是否已渲染为可点击链接必须在解除 RAGFlow 阻塞后由该断言确认；在此之前仍属于未完成项。

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
npx playwright test --list --project=chrome-headed ...  # PASS，32 tests
OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/login.spec.ts # PASS
OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts # 32 skipped（保护开关，非 PASS）
```
