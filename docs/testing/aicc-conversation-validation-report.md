# AICC 客服对话 Chrome 验收报告

## 结论

截至 2026-07-16，AICC 对话全场景 Chrome Stable 验收为 **BLOCKED**，不能宣称通过。
安全、来源、意向、会话状态、访客隔离、移动端、Pod 重建和故障重试的真实页面用例已落在
`web/tests/e2e/aicc-conversation-*.spec.ts`。本报告不把“测试被跳过”、页面基础登录通过或仅有静态
渲染测试计作客服验收通过。

## 本地前置与固定证据

| 项目 | 结果 | 证据 |
|---|---|---|
| Kubernetes 隔离 | PASS | `kubectl config current-context` 为 `k3d-ocm`；节点 `k3d-ocm-server-0` 为 Ready。所有新增 Pod 删除命令先校验并显式使用该 context。 |
| Chrome Stable | PASS | `Google Chrome 150.0.7871.114`；Playwright `1.59.1`。 |
| Chrome 页面层检查 | PASS | `OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/login.spec.ts` 退出码 0，实际运行 2 个登录页面场景。 |
| AICC 用例可发现性 | PASS | `npx playwright test --list --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts` 列出 3 文件共 34 个场景。 |
| RAGFlow | PASS | `ocm/ragflow-6b569494f6-7lgp5` 为 `1/1 Running`，此前代理连接拒绝已消除。 |
| 直连公网渲染与工具策略 | PASS | `go test ./internal/integrations/k8sorch -run TestRenderAICCNetworkPolicy -count=1` 通过（该测试同时断言公网 TCP 80/443 和无 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`）；`pytest -q .../test_aicc_tool_policy.py` 为 12 passed。高风险 action 工具仍被拒绝。 |
| AICC 全链路 | BLOCKED | 真实 Chrome 首个 fixture 启动时，`oc-aicc` 既有运行时 Pod 的 `restore` initContainer 调 manager bootstrap 连续返回 HTTP 401，随后 manager 记录 `circuit_open`。因此没有任何对话场景完成，不能把启动了 Chrome 或发现 34 个场景记为通过。 |
| AICC 镜像重建 | BLOCKED | `make local-build` 的 AICC runtime 构建期自检为 256 passed、11 failed：若干测试在镜像内仍按源码同级目录读取 `oc-entrypoint.py` 与 `skills`，但 Dockerfile 仅复制 tests；另有一项 channel 登录断言与当前 SDK 报错文本不一致。该独立构建测试问题尚未定位，未据此推断 Chrome bootstrap HTTP 401 的根因。 |

客服运行时前置条件为：集群 DNS 和公网 TCP 80/443 可达，供 Hermes 原生只读 `web_search`/`web_extract`
使用；AICC 不再部署或依赖受控网页检索代理，也不注入代理环境变量。当前 HTTP 401 的 bootstrap 鉴权/运行时
契约仍待定位；它不是已验证的公网 DNS/TCP 或直连出口问题，本任务不通过伪造回答或降低浏览器断言绕过它。

## 已实现的 Chrome 场景映射

| Spec | 场景 | 对应矩阵 |
|---|---|---|
| `aicc-conversation-security.spec.ts` | 公开链接页和网页挂件 iframe 的知识/来源页面合同、命令/文件/建站/登录/多轮注入拒绝、两个 BrowserContext 隔离、域名/隐私/频控/图片/token 入口边界 | AICC-CAP-001、AICC-CAP-003、AICC-SRC-001~004、AICC-E2E-001 |
| `aicc-conversation-intent.spec.ts` | low/medium、求职/投诉/媒体误判负例、高意向一次邀请与拒绝、升级/更正降级、匿名候选提交联系方式、后台线索可见、意向失败重试一次邀请、同会话双标签并发留资、状态和 390px 中英文移动页面 | AICC-INT-001~005、AICC-STATE-001~004、AICC-E2E-002~003 |
| `aicc-conversation-runtime.spec.ts` | 首轮后删除本地 AICC Pod、等待 Ready 并续聊；RAGFlow/搜索/模型/队列四类显式故障注入、失败重试、未解决刷新与新消息重置 | AICC-BOOT-001~004、AICC-CH-001~003、AICC-E2E-003 |

Chrome 项目使用 `channel: "chrome"`、`headless: false` 与 `retries: 1`；首次重试保留 trace/video，失败保留 screenshot。
原有 `chromium` 项目未删除，继续承担快速回归。测试以 role、label、placeholder 和 web-first assertion
为主，未使用固定 `waitForTimeout`。

以下细项已写入 spec，但因 runtime 启动阻塞均为**待执行**：客服/企业/行业知识单独命中、组合命中和冲突优先级；
公开网络未确认来源；后台线索画像字段证据可视化；授权域名和频率限制的服务端拒绝审计；以及故障恢复后的
实际重试成功。当前实现只在公开页面验证可观察合同，未将这些细项误记为 PASS。

`seed-e2e` 当前只构造组织、成员、应用等通用 fixture，**不**预置客服/企业/行业三层固定事实、冲突网页、
来源标题或绑定关系。因此上述知识场景有独立 `OCM_AICC_KNOWLEDGE_FIXTURE=1` 前置，缺失时逐条标为 BLOCKED。
同样，仓库尚未提供可由 E2E 启停的一次性 RAGFlow/搜索/模型/队列故障 injector；四类恢复场景有独立
`OCM_AICC_FAULT_INJECTION=1` 前置，不能把当前 skip 视为故障恢复已测。

网页挂件 iframe、意向分析失败后恢复重试和同会话多标签并发提交均已有 Chrome 场景；意向重试场景在
`OCM_AICC_INTENT_RETRY_FIXTURE=1` 时会仅对本地 `k3d-ocm` 的 manager-api 注入一次性失败并滚动重启，
server 还要求 `app.env=local` 才会消费该变量；注入器同时暂停重试扫描，待 E2E 读取到持久化失败记录后
显式清除两个变量并滚动重启来释放恢复。该控制面不由 seed-e2e 默认开启，且当前 runtime bootstrap
仍阻塞，故仍为 BLOCKED 的“已实现未运行”子项，不能用于计算意向 precision、recall 或全场景覆盖率。

来源场景在运行时会强制断言来源标题、消息时间、未确认标签，以及公开网络来源的 HTTPS 链接；操作性拒绝会
通过 manager 数据库中 `aicc_message_sources` 的受信任工具来源审计断言为零。当前公开页仅以文本展示来源，
未确认网络来源是否已渲染为可点击链接必须在解除 runtime bootstrap 阻塞后由该断言确认；在此之前仍属于未完成项。

会话 resolved、unresolved 与新消息重置均在 E2E 中直接查询 `aicc_sessions.resolution_status` 作为持久化证据，
不以页面卡片消失代替服务端状态断言。网页挂件的 allowed-domain 拒绝、当前轮图片理解分别需要
`OCM_AICC_WIDGET_DOMAIN_FIXTURE=1` 与 `OCM_AICC_VISION_FIXTURE=1`；当前通用 seed-e2e 未提供，均为独立
BLOCKED 前置。Task 1 的失败基线保留在矩阵末尾，不能被本报告中的静态检查结果覆盖。

## 已尝试但受阻的 Chrome 验收

用户指定的命令使用 `--project=chrome`，但当前 Playwright 配置只有 `chromium` 与真正调用 Chrome Stable 的
`chrome-headed`；前者立即报 `Project(s) "chrome" not found`。随后按现有配置实际运行以下命令，Chrome 已启动、
发现 34 场景并进入首个 fixture，但在上述 bootstrap HTTP 401 处阻塞；为避免每个场景等待 600 秒超时而没有新增
证据，已在确认证据后中止。结果为 **0/34 完成**，不是 PASS、FAIL 或 skip。

```bash
cd web
OCM_AICC_CONVERSATION_E2E=1 OCM_AICC_KNOWLEDGE_FIXTURE=1 OCM_AICC_FAULT_INJECTION=1 \
  npm run test:e2e -- --project=chrome-headed \
  aicc-conversation-security.spec.ts aicc-conversation-intent.spec.ts aicc-conversation-runtime.spec.ts
```

在修复 bootstrap 401、确认 AICC runtime 能 Ready，并确认公网 DNS/TCP 80/443 可达后，执行三轮命令；每轮应记录 trace/video/screenshot
路径、控制台未处理错误、未授权工具审计数、跨访客泄漏数和意向 precision/recall。只有三轮均 PASS
且人工 Chrome 复核公开链接与移动挂件后，矩阵中的三个 AICC-E2E 条目才能改为 PASS。

## 本次静态验证

```text
cd web && npm run typecheck                              # PASS
npx tsc --noEmit                                         # FAIL：5 个既有 Vue spec 类型错误，见下文
npx playwright test --list --project=chrome-headed ...  # PASS，34 tests
OCM_E2E_NO_SEED=1 npx playwright test --project=chrome-headed tests/e2e/login.spec.ts # PASS
OCM_AICC_CONVERSATION_E2E=1 ... --project=chrome                       # BLOCKED：项目名不存在
OCM_AICC_CONVERSATION_E2E=1 ... --project=chrome-headed                # BLOCKED：真实 Chrome 启动，0/34 完成，AICC bootstrap HTTP 401
go test ./internal/integrations/k8sorch -run TestRenderAICCNetworkPolicy -count=1 # PASS
PYTHONPATH=runtime/hermes/hermes-aicc pytest -q runtime/hermes/hermes-aicc/tests # PASS：267 passed, 1 warning
go test ./internal/... ./cmd/server -count=1                            # BLOCKED：绝大多数包已输出 ok；`internal/service` 子进程运行逾 2 分 45 秒未完成，已按防卡死规则中止，不能记为全量通过
cd web && npm test -- --run && npm run typecheck && npm run build       # PASS：105 files / 754 tests；typecheck/build 通过
make openapi-check && git diff --check                                   # PASS
```

`npm run typecheck` 执行项目配置的 `vue-tsc --noEmit`，本次通过。裸 `npx tsc --noEmit` 在 2026-07-16
报告 5 个既有测试文件类型错误：`SkillDetailDrawer.spec.ts`、`LocaleSwitcher.spec.ts`（2 项）、
`TicketTargetsEditor.spec.ts`、`AppKnowledgeTab.spec.ts`。这些文件不在本轮 AICC 改动范围，因此不能将
裸 `tsc` 结果写为 PASS；同时也不影响已记录的 `vue-tsc` 项目类型检查结果。

## 2026-07-21 客服体系验证补强

本轮新增的是 AICC 本地 `chromium` slow/model 定向验收，不改变 2026-07-16 对全场景 Chrome Stable
验收的 BLOCKED 结论。新增场景覆盖知识库修改、多文件组合检索、设置重启生效、企业模型 revision 绑定
rollout、暂停客服不被 rollout 唤醒，以及手机号正式线索计数与同会话重复提交去重。

| 范围 | 结果 | 证据 |
|---|---|---|
| 知识库修改与组合检索 | PASS | `cd web && npm run typecheck` PASS；`npm run test:e2e:slow -- tests/e2e/aicc-knowledge.spec.ts -g '修改当前客服知识库后运行时检索使用新内容'` PASS，1/1；`-g '当前客服知识库可组合多个文件检索'` PASS，1/1。公开模型精确复述知识编号曾出现拒答，因此新增断言以 runtime 检索事实和公开页非技术错误边界为准。 |
| 设置重启与模型切换 | PASS | `cd web && npm run typecheck` PASS；`npm run test:e2e:slow -- tests/e2e/aicc.spec.ts -g '重启生效|更换模型|暂停中的智能客服'` PASS，3/3；revision 修正后 `-g '更换模型|暂停中的智能客服'` PASS，2/2。 |
| 线索手机号与去重 | PASS | `cd web && npm run typecheck` PASS；`OCM_AICC_CONVERSATION_E2E=1 npm run test:e2e:slow -- tests/e2e/aicc.spec.ts -g '公开访客提交留资后企业管理员可查看线索和导出 CSV'` PASS，1/1。 |
| 全量 E2E | NOT RUN | 本轮是定向补强，未执行全量 AICC E2E。项目脚本没有 `npm run test:e2e`，当前可用脚本为 `test:e2e:quick`、`test:e2e:regression`、`test:e2e:slow`。 |

已确认缺口：

- 手机号格式校验当前未实现。公开页和后端 `SubmitLeadValues` 只校验必填非空和字段 key，不能把非法手机号拒绝写成 E2E 通过项。
- 暂停客服在企业模型 rollout 后保持 `paused` 且未被自动 stamp 到新 revision；手动启动后可公开接待，但当前产品未证明会在手动启动时应用最新企业模型 revision。
- “无关问题拒绝”依赖真实模型稳定输出；本轮尝试的新增 case 长时间未收敛，未提交未验证断言。已有安全 spec 仍覆盖命令执行、文件写入、网页登录和提示词注入拒绝及来源审计。
