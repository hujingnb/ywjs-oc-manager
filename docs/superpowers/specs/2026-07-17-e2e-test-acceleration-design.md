# E2E 测试提速设计

## 背景

当前前端 E2E 使用 Playwright，`web/playwright.config.ts` 固定配置为 `fullyParallel: false` 和 `workers: 1`。Chromium 项目下共有 65 条测试，既包含登录、组织、成员、控制台等确定性浏览器回归，也包含以下高成本场景：

- AICC Runtime 冷启动、真实模型问答、RAG 检索和知识解析；
- Pod 删除重建、manager-api 滚动和故障注入；
- 三种角色、两种语言、全部可见页面的 i18n 清扫与截图。

现有 `globalSetup` 在每次运行前删除两个业务 namespace 中带项目标签的资源、等待 Pod 退出，再执行 `make seed-e2e` 清空并重建单套共享 fixture。多数业务用例还会重复通过 UI 登录；AICC 测试则反复创建并等待独立 Runtime。上述成本同时影响定向测试和完整回归，并且单套共享数据阻止了安全并行。

## 目标

- 建立快速、确定性完整回归和专项慢测三级入口；
- 在本地 k3d 已就绪的前提下，`make e2e-quick` 完整墙钟时间不超过 60 秒；
- 在相同前提下，`make e2e-regression` 完整墙钟时间不超过 20 分钟；
- 通过 worker 级数据隔离支持 2 至 4 个 Playwright worker 稳定并行；
- 保留关键 UI 闭环、真实浏览器断言和失败诊断证据，不通过删减有效断言换取速度；
- 将真实模型、RAG、运行时破坏和全站清扫从日常回归中隔离，仅在发布前或人工显式运行。

墙钟时间从执行对应 `make e2e-*` 命令开始计算，到命令退出为止，包含 fixture 准备与清理，不包含 `make local-up`、镜像构建和浏览器安装。

## 不在范围内

- 缩减现有业务覆盖范围或降低断言质量；
- 使用 mock 替代专项慢测中的真实模型、RAG 或 Kubernetes 恢复链路；
- 默认运行全量有头浏览器测试；
- 为提速改造生产业务接口或改变生产数据语义；
- 将本地 k3d 启动、镜像构建时间纳入本次 E2E 性能目标；
- 第一阶段直接要求 4 worker，或给专项慢测设置 20 分钟上限。

## 方案对比

### 方案一：三级分层、worker 隔离与并行（采用）

把测试划分为 quick、regression 和 slow，扩展 `seed-e2e` 创建 worker 级 fixture 池，复用角色认证状态，并对确定性回归开启并行。

优点：同时解决套件边界不清、重复准备、共享状态和串行执行问题，提速空间最大，也能降低状态串扰造成的不稳定。缺点：需要同步改造 seed、Playwright fixture 和部分依赖共享状态的用例。

### 方案二：只做标签分层与有限并行

保留单套共享 fixture，仅并行只读用例，写操作和 AICC 测试继续串行。

优点是改动较小；缺点是并行能力受测试语义约束，完整回归很可能无法稳定满足 20 分钟目标，新增写操作用例也会继续扩大串行区。

### 方案三：大部分状态改用 API 或数据库准备

测试前直接准备业务状态，浏览器只验证最终交互。

该方案速度最快，但会绕开较多真实 UI 创建流程，改造范围与漏测风险较高。本设计只在方案一实施后仍存在明确热点时，对非核心前置状态定向采用，不作为整体策略。

## 测试分层与执行入口

### 快速冒烟

`make e2e-quick` 对应 `npm run test:e2e:quick`，运行带 `@quick` 标签的核心确定性场景，包括登录、权限入口、控制台、组织、成员和实例页面的关键交互。

quick 不创建 AICC Runtime，不调用真实模型或 RAG，不执行 Kubernetes 破坏操作，也不执行全站 i18n 截图清扫。默认使用 Chromium headless，目标是开发期间频繁执行并在 60 秒内反馈。

### 确定性完整回归

`make e2e-regression` 对应 `npm run test:e2e:regression`，运行所有不带 `@slow` 标签的用例，因此自然包含 quick。该入口使用隔离 fixture 和 2 至 4 个 worker，是提交前与改动范围匹配的完整确定性浏览器回归入口。

不再保留含义模糊的 `make e2e`，避免开发者误以为它代表快速、完整或包含真实依赖的全部测试。

### 专项慢测

`make e2e-slow` 对应 `npm run test:e2e:slow`，只运行 `@slow` 用例。慢测再使用以下子标签支持定向执行：

- `@model`：真实模型问答和多轮会话；
- `@rag`：知识上传、解析、检索和来源验证；
- `@k8s-disruptive`：Pod 删除、控制面滚动和故障注入；
- `@i18n-sweep`：多角色、多语言全站遍历和截图。

slow 仅在发布前或人工显式触发。涉及共享基础设施变更的子集串行执行；其他慢测只有在证明资源隔离可靠后才允许有限并行。

## Playwright 项目与标签职责

测试标签决定业务套件，Playwright project 只决定浏览器形态：

- `chromium` 是三个 Makefile 入口的默认项目，始终使用 headless；
- `chrome-headed` 仅供人工可视化验收显式选择，不被任何默认入口重复执行。

这样避免当前 `npm run test:e2e` 同时匹配 Chromium 与有头 Chrome 而重复执行相同业务场景。普通 regression 用 `@slow` 反向筛选，quick 用 `@quick` 正向筛选，slow 用 `@slow` 正向筛选。

## Worker 级 fixture 设计

### Fixture 池

`seed-e2e` 接收本次 suite、`run_id` 和 worker 数，一次生成 fixture 池。每个 worker 至少拥有独立的：

- 企业及企业标识；
- 企业管理员和普通成员账号；
- 实例及其业务记录；
- 会被测试修改的语言、权限、成员和配置状态。

平台管理员可共享只读身份，但修改平台级状态的用例必须使用专属 fixture 或进入串行互斥组。seed 输出从单个 fixture 对象扩展为带 `run_id` 的 fixture 数组，Playwright worker-scoped fixture 根据 `parallelIndex` 领取唯一条目。

fixture 数不足、索引越界或重复分配时立即终止测试，禁止退化为共享数据。worker 数可由环境变量覆盖为 1，以兼容低资源机器，但单 worker 仍使用同一套隔离接口，不恢复旧的全局共享访问模式。

### 资源标识与清理

数据库记录、Kubernetes 资源和临时文件都使用 `run_id` 加 worker 索引标识。运行开始前只清理超过保留期限的 E2E 资源，不再为定向 spec 删除本地全部项目资源。

正常成功时清理本次临时资源；测试失败时保留 `run_id`、trace、截图和视频引用，以支持复现。清理失败单独报告，但不能覆盖原始测试失败。过期资源由下次运行的前置清理兜底。

## 认证状态复用

登录功能本身继续保留真实 UI 登录和错误密码用例。其他业务用例在 fixture 准备阶段为每个“worker + role”生成独立认证状态，并在对应 BrowserContext 中复用，避免每条测试重复填写登录表单、等待跳转和切换语言。

认证状态文件必须位于测试临时目录，文件名包含 `run_id`、worker 索引和角色；运行结束后按资源清理策略处理。不同 worker、角色或 suite 之间不得复用同一状态文件。

## 执行数据流

一次测试运行按以下顺序执行：

1. 根据 suite、worker 数和子标签检查本地 k3d 与所需依赖；
2. 生成唯一 `run_id`，清理过期 E2E 资源；
3. 一次性创建匹配 worker 数的 fixture 池和认证状态；
4. 每个 worker 按索引领取独占 fixture，执行对应标签的测试；
5. reporter 输出总耗时、各 spec 耗时和慢用例排行；
6. 根据运行结果清理或保留本次诊断资源。

定向运行单个 spec 时仍走同一数据流，但只准备该 spec 所属 suite 必需的数据，不执行全量 namespace 清理，也不准备无关的 AICC Runtime。

## 慢测前置检查与互斥

显式运行 slow 时，前置检查根据所选子标签验证模型、RAG、故障注入和本地 Kubernetes context。缺少必要条件时必须在正式执行前快速失败，并列出缺失项，不能依赖大量运行期 `test.skip` 产生假绿色结果。

以下操作属于共享基础设施互斥区：

- 修改或滚动 manager-api 环境变量；
- 删除 AICC Pod 并等待控制器重建；
- 注入会影响其他会话的故障；
- 清理 namespace 级公共资源。

互斥区内的测试串行执行，并在异常退出路径恢复原始环境。普通 quick 和 regression 不执行这些操作。

## 等待与耗时治理

- 普通用例移除与业务状态无关的固定等待，优先等待 HTTP 响应、可见 DOM 状态或后端事实；
- i18n 清扫中必要的渲染稳定窗口保留，但该用例归入 slow；
- AICC Runtime 只在确实验证运行时或公开对话的场景创建，非核心前置状态优先复用当前 worker 的已就绪 fixture；
- reporter 每次输出最慢 spec 排行，第一阶段只告警，不因单条用例的偶发耗时直接判失败；
- 默认保持 `retries: 0`。性能目标与稳定性验收均不得依赖 retry 达成。

## 分阶段落地

### 阶段一：基线、标签与入口

记录当前非 slow 用例清单和每个 spec 的耗时，添加三级标签、npm 脚本、Makefile 入口和耗时报告。此阶段不改变用例行为，用清单比对保证 regression 覆盖所有非 slow 用例，quick 是 regression 的严格子集。

### 阶段二：隔离与认证复用

扩展 `seed-e2e` 输出 fixture 池，接入 worker-scoped fixture、`run_id` 资源边界和认证状态。先在单 worker 下验证与原有行为一致，再使用 2 worker 连续运行；稳定后根据本地 CPU、内存、数据库与 k3d 调度负载评估是否提升到 4 worker。

### 阶段三：热点优化

根据慢用例排行减少重复 Runtime 创建、重复登录、全局资源删除和固定等待。只处理有测量证据的热点，不进行无关测试重构，也不为达成时间目标删除有效业务断言。

## 错误处理

- seed、前置检查或认证状态生成失败时立即终止，不启动部分 worker；
- worker fixture 分配异常时输出 `run_id`、worker 索引和 suite，禁止使用其他 worker 数据继续执行；
- slow 依赖缺失时在测试收集后、业务执行前失败，并给出可操作的准备说明；
- 测试失败优先保留 Playwright 原始错误，清理错误作为附加诊断输出；
- 并行引发资源不足时允许显式降低 worker 数，但不得通过增加 retry 掩盖容量问题；
- 有头 Chrome 只能显式请求，缺少图形环境不会影响三个默认 headless 入口。

## 测试与验收

### Seed 与 fixture 单元测试

为以下场景补充单元测试：

- 请求 N 个 worker 时生成 N 份唯一 fixture；
- 每份 fixture 的组织、账号、实例和资源前缀互不相同；
- worker 索引映射稳定且越界立即失败；
- `run_id` 清理只影响本次或过期 E2E 资源；
- 单 worker 兼容路径仍使用隔离 fixture；
- suite 选择不会准备无关的慢测资源。

新增测试与子测试均按仓库规范添加相邻中文业务场景注释，并使用 testify 的 `assert` 和 `require`。

### Playwright 选择与隔离验证

- 校验 quick 清单全部带 `@quick`，且不包含任何 `@slow`；
- 校验 regression 清单等于全部非 slow 用例；
- 校验 slow 及各子标签能独立列出预期用例；
- 让两个 worker 同时修改各自的成员或语言状态，确认结果不串扰；
- 连续交叉运行 quick、regression、quick，确认前一次数据不会改变后一次结果；
- 验证失败时能从 `run_id`、trace 和截图定位对应 worker 资源。

### 性能验收

- 在已就绪的本地 k3d 上连续运行 `make e2e-quick` 三次，每次完整墙钟时间不超过 60 秒；
- 连续运行 `make e2e-regression` 三次，每次完整墙钟时间不超过 20 分钟；
- 两项验收均使用 Chromium headless、`retries: 0`，并包含 seed 与清理时间；
- 使用 2 worker 达标后即可交付，不把 4 worker 作为强制条件；
- `make e2e-slow` 不受 20 分钟限制，但必须支持按子标签定向运行和前置条件快速失败。

## 成功标准

- 仓库只提供语义明确的 `e2e-quick`、`e2e-regression` 和 `e2e-slow` Makefile 入口；
- quick 与 regression 连续三次达到各自时间预算，且不依赖 retry；
- regression 覆盖全部非 slow 用例，slow 测试没有被静默遗漏；
- 至少 2 个 worker 能稳定并行执行写操作用例，不发生跨 worker 数据污染；
- 定向 spec 不再执行无关的全局 Kubernetes 资源删除；
- 登录以外的业务测试能安全复用 worker 级认证状态；
- slow 条件缺失不会产生假绿色结果；
- 失败证据、中文测试注释和现有断言质量保持或增强。
